// http_test.go——HTTP API 面钉子(2026-10-18-web-ui-mvp Task 7):路由表、
// 错误映射(409/403/404/400)、同源防护、合并列表、审批往返。装配桩 =
// FakeHost × 真实 control 栈(host_test testAssemble 同形态,包内自实现——
// 测试基建不跨包复用),canUseTool 信封照搬 internal/e2e 系统边界注入惯例。
package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/guard"
	"mulix/internal/sessionx"
)

const testWorkspace = "w"

type httpEnv struct {
	srv  *httptest.Server
	root string // sessions root(桩装配镜像同根——磁盘会话合并可见)
	mgr  *hostManager
	opts Options
}

// newHTTPEnv 构造 manager + mux + httptest 服务:装配桩 = stubAssemble
// (FakeHost 按 script 回放;镜像落 env.root)。closeAllHosts 先于 mgr.Close
// 收口在册 host(泄漏的泵/镜像句柄会在 Windows 上卡 TempDir 清理)。
func newHTTPEnv(t *testing.T, script engine.FakeScript) *httpEnv {
	t.Helper()
	root := t.TempDir()
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.Web.WorkspaceRoots = []string{testWorkspace}
	mgr := newHostManager(time.Hour)
	t.Cleanup(mgr.Close)
	closeAllHosts(t, mgr)
	opts := Options{
		Config:       cfg,
		Workspace:    testWorkspace,
		SessionsRoot: root,
		Stderr:       os.Stderr,
		assemble:     stubAssemble(t, root, script),
	}
	srv := httptest.NewServer(newMux(mgr, opts))
	t.Cleanup(srv.Close)
	return &httpEnv{srv: srv, root: root, mgr: mgr, opts: opts}
}

// closeAllHosts 测试收口缝:manager.Close 只停扫描不 close host(见 host.go
// Close 注释)——这里同包直读在册表,先 CancelAll 释放可能 pending 的审批
// (pending 期间 ctrl.Close 会等泵,泵阻塞在 gate 审批窗口——不释放则收口
// 等满超时),再逐个 close(幂等,与 sweep 竞争无碍)。
func closeAllHosts(t *testing.T, mgr *hostManager) {
	t.Helper()
	t.Cleanup(func() {
		mgr.mu.Lock()
		hosts := make([]*sessionHost, 0, len(mgr.hosts))
		for _, h := range mgr.hosts {
			hosts = append(hosts, h)
		}
		mgr.mu.Unlock()
		for _, h := range hosts {
			if h.bridge != nil {
				h.bridge.CancelAll("test-cleanup")
			}
			_ = h.close(context.Background())
		}
	})
}

// stubAssemble 是 HTTP 测试的装配桩(testAssemble 同构,Script 可指定):
// 每会话独立 store/meter/桥/Gate(桥接 Approver,审批事件入镜像+webSink),
// 引擎 = FakeHost(script)。Gate 超时 10s——审批往返/冲突窗口充足。装配
// 成功后经 h.assembleRelease 移交资源收口(host.close 级联——assembleReal
// 同纪律;不设则 stub 句柄泄漏,Windows 上卡 TempDir 清理)。
func stubAssemble(t *testing.T, root string, script engine.FakeScript) assembleFn {
	t.Helper()
	return func(ctx context.Context, h *sessionHost) (*control.Controller, *control.UserApprovalBridge, error) {
		store, err := sessionx.Create(root, sessionx.Options{ID: h.ID, Workspace: h.Workspace})
		if err != nil {
			return nil, nil, err
		}
		var meter *cache.Meter
		var ctrl *control.Controller
		fail := func(err error) (*control.Controller, *control.UserApprovalBridge, error) {
			if ctrl != nil {
				_ = ctrl.Close(ctx)
			}
			if meter != nil {
				_ = meter.Close()
			}
			_ = store.Close()
			return nil, nil, err
		}
		meter, err = cache.NewMeter(cache.MeterOptions{
			Root: root, SessionID: h.ID,
			Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
		})
		if err != nil {
			return fail(err)
		}
		cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
		if err != nil {
			return fail(err)
		}
		policy, err := guard.NewPolicy(guard.Preset(cfg.Permissions.Preset), nil)
		if err != nil {
			return fail(err)
		}
		bridge := control.NewUserApprovalBridge()
		gate, err := guard.NewGate(guard.GateOptions{Policy: policy, Approver: bridge, Timeout: 10 * time.Second})
		if err != nil {
			return fail(err)
		}
		ctrl, err = control.NewController(control.Options{
			HostFactory: func(engine.BootConfig) (engine.Host, error) { return engine.NewFakeHost(script), nil },
			Sink:        h.sink, Store: store, Meter: meter,
			Gate: gate, Approvals: bridge,
			Config: *cfg, Workspace: h.Workspace,
			Kernel: kernelCtxWithLoop(),
		})
		if err != nil {
			return fail(err)
		}
		if err := ctrl.NewSession(ctx); err != nil {
			return fail(err)
		}
		h.assembleRelease = func() {
			_ = meter.Close()
			_ = store.Close()
		}
		return ctrl, bridge, nil
	}
}

// fixtureTurnScript 读 happy-turn fixture(FakeHost 单回合:assistant→Read 工具→result)。
func fixtureTurnScript(t *testing.T) engine.FakeScript {
	t.Helper()
	fixture, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return fixture
}

// scriptWithApproval 在 fixture 回合 result 前注入 can_use_tool 控制请求
// (e2e newEnv 同手法——pump 只接受回合内控制请求;Write 属 edit 类,default
// 预设 → ask → 桥 pending,turn 停在审批等待态)。
func scriptWithApproval(t *testing.T, requestID, toolUseID string) engine.FakeScript {
	t.Helper()
	script := fixtureTurnScript(t)
	turn0 := script.Turns[0]
	withCR := make([]engine.Event, 0, len(turn0)+1)
	for _, ev := range turn0 {
		if ev.Kind == engine.KindResult {
			withCR = append(withCR, *canUseToolReq(requestID, toolUseID, "Write", `{"file_path":"w.txt"}`))
		}
		withCR = append(withCR, ev)
	}
	script.Turns[0] = withCR
	return script
}

// canUseToolReq 构造引擎发来的 can_use_tool 控制请求信封(照搬
// internal/e2e/e2e_test.go canUseTool——权限回路入口的系统边界形态)。
func canUseToolReq(requestID, toolUseID, tool, input string) *engine.Event {
	args := map[string]any{"tool_name": tool, "tool_use_id": toolUseID}
	if input != "" {
		args["input"] = json.RawMessage(input)
	}
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID + "-mcp",
		"method":  "tools/call",
		"params":  map[string]any{"name": "permission", "arguments": args},
	})
	return &engine.Event{Kind: engine.KindControlRequest, SessionID: "s-http", Control: &engine.ControlRequest{
		RequestID: requestID,
		Request: engine.ControlRequestBody{
			SubType:    "mcp_message",
			ServerName: "mulix",
			Message:    msg,
		},
	}}
}

// httpDo 发一个带可选 Cookie/Origin/JSON 体/每标签页身份头的请求。
// clientID 变参非空时另设 X-Mulix-Client 头(I1:生产前端 mutating fetch 同形态)。
func httpDo(t *testing.T, method, url, cookieVal, origin, body string, clientID ...string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	if cookieVal != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieVal})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if len(clientID) > 0 && clientID[0] != "" {
		req.Header.Set(clientIDHeader, clientID[0])
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// createAndOpen 走 create(201)+open(interactive)完整链,返回会话 id。
func createAndOpen(t *testing.T, env *httpEnv, clientID string) string {
	t.Helper()
	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions", clientID, env.srv.URL, `{"workspace":"`+testWorkspace+`"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d, want 201(body: %s)", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("create 响应缺 id: %v", err)
	}
	resp2 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+created.ID+"/open", clientID, env.srv.URL, `{}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("open status = %d, want 200(body: %s)", resp2.StatusCode, body)
	}
	var opened struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&opened); err != nil || opened.Role != "interactive" {
		t.Fatalf("open role = %+v (%v), want interactive", opened, err)
	}
	return created.ID
}

// getSessionState 读 state 投影;任何失败返回 nil(轮询条件下不 Fatal)。
func getSessionState(t *testing.T, env *httpEnv, id string) map[string]any {
	t.Helper()
	resp, err := http.Get(env.srv.URL + "/api/sessions/" + id + "/state")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var m map[string]any
	if json.NewDecoder(resp.Body).Decode(&m) != nil {
		return nil
	}
	return m
}

// waitForCond 轮询条件至超时(10ms 节拍;cond 内不得 Fatal)。
func waitForCond(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// readNDJSONFrames 逐行解 JSON 帧列表。
func readNDJSONFrames(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	var frames []map[string]any
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("NDJSON 帧 %q: %v", line, err)
		}
		frames = append(frames, m)
	}
	return frames
}

// newDiskSession 在 sessions root 造一个未打开的磁盘会话(meta + 空 events)。
func newDiskSession(t *testing.T, root, id string) {
	t.Helper()
	st, err := sessionx.Create(root, sessionx.Options{ID: id, Workspace: testWorkspace})
	if err != nil {
		t.Fatalf("create disk session %s: %v", id, err)
	}
	t.Cleanup(func() { _ = st.Close() })
}

// ---- 测试 ----

// TestSessionListAndCreate 钉:GET /api/workspaces(roots 投影);POST create
// → 201+id;GET list = 本进程打开(真实状态)+ 未打开磁盘会话(启发式)合并;
// 未知 workspace → 400;未知 id state → 404。
func TestSessionListAndCreate(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})

	resp := httpDo(t, "GET", env.srv.URL+"/api/workspaces", "", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspaces status = %d, want 200", resp.StatusCode)
	}
	var ws struct {
		Workspaces []string `json:"workspaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ws); err != nil || len(ws.Workspaces) != 1 || ws.Workspaces[0] != testWorkspace {
		t.Fatalf("workspaces = %+v (%v), want [%s]", ws, err, testWorkspace)
	}

	id := createAndOpen(t, env, "list-client")

	resp2 := httpDo(t, "POST", env.srv.URL+"/api/sessions", "bad-ws", env.srv.URL, `{"workspace":"nope"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown workspace status = %d, want 400", resp2.StatusCode)
	}

	newDiskSession(t, env.root, "s-disk")

	resp3 := httpDo(t, "GET", env.srv.URL+"/api/sessions", "", "", "")
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp3.StatusCode)
	}
	var list struct {
		Sessions []struct {
			ID           string `json:"id"`
			Workspace    string `json:"workspace"`
			Status       string `json:"status"`
			LastActivity string `json:"last_activity"`
			Open         bool   `json:"open"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&list); err != nil {
		t.Fatalf("list 解码: %v", err)
	}
	var foundOpen, foundDisk bool
	for _, s := range list.Sessions {
		switch s.ID {
		case id:
			foundOpen = true
			if !s.Open || s.Workspace != testWorkspace {
				t.Fatalf("open entry = %+v, want open=true workspace=%s", s, testWorkspace)
			}
			switch s.Status {
			case "ready", "not_ready", "running", "failed":
			default:
				t.Fatalf("open entry status = %q, want 真实状态集", s.Status)
			}
		case "s-disk":
			foundDisk = true
			if s.Open {
				t.Fatal("磁盘会话 open = true, want false(未打开)")
			}
			if s.Workspace != testWorkspace {
				t.Fatalf("磁盘会话 workspace = %q, want %s(meta 投影)", s.Workspace, testWorkspace)
			}
			if s.LastActivity == "" {
				t.Fatal("磁盘会话缺 last_activity(events.jsonl mtime 启发式)")
			}
			if s.Status != "empty" {
				t.Fatalf("空镜像磁盘会话 status = %q, want empty", s.Status)
			}
		}
	}
	if !foundOpen || !foundDisk {
		t.Fatalf("list 缺条目: open=%v disk=%v (all=%+v)", foundOpen, foundDisk, list.Sessions)
	}

	resp4, err := http.Get(env.srv.URL + "/api/sessions/nope/state")
	if err != nil {
		t.Fatalf("state unknown: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown state status = %d, want 404", resp4.StatusCode)
	}
}

// TestSubmitConflictWhileRunning 钉(FR-006):回合在飞时第二次 submit → 409,
// 消息含「回合」;首次 submit → 202 + 回合号。回合占用 = 审批 pending(确定性
// 阻塞泵);SSE 收到 approval_request 后提交第二次,deny 收尾(不靠 gate 超时
// ——pending 前的 CancelAll 有竞态,收口会等满审批窗口)。
func TestSubmitConflictWhileRunning(t *testing.T) {
	env := newHTTPEnv(t, scriptWithApproval(t, "req-conflict", "tu-conflict"))
	id := createAndOpen(t, env, "c-conflict")

	s := openSSE(t, env.srv.URL, id, "c-conflict")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "c-conflict", env.srv.URL, `{"text":"first"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("first submit status = %d, want 202(body: %s)", resp.StatusCode, body)
	}
	var out struct {
		Turn int64 `json:"turn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Turn != 1 {
		t.Fatalf("first submit turn = %+v (%v), want 1", out, err)
	}

	// approval_request 在镜像+Sink 可见 ⇔ gate 已阻塞在桥上 ⇔ running=true:
	// 此窗口内第二次 submit 必撞回合互斥。
	s.waitFrame(t, "approval_request", 5*time.Second)

	resp2 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "c-conflict", env.srv.URL, `{"text":"second"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second submit status = %d, want 409", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body), "回合") {
		t.Fatalf("409 消息须含「回合」(FR-006): %s", body)
	}

	// deny 收尾:回合收口,清理不再等 gate 超时。
	resp3 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/approval", "c-conflict", env.srv.URL, `{"tool_use_id":"tu-conflict","decision":"deny"}`)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("deny status = %d, want 200", resp3.StatusCode)
	}
	s.waitFrame(t, "turn_done", 5*time.Second)
}

// TestReadonlyForbidden 钉:非持有者 submit/approval/cancel → 403;takeover
// open → c2 interactive 且原持有者降级 403。
func TestReadonlyForbidden(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})
	id := createAndOpen(t, env, "c1")

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/open", "c2", env.srv.URL, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("c2 open status = %d, want 200", resp.StatusCode)
	}
	var opened struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil || opened.Role != "readonly" {
		t.Fatalf("c2 open role = %+v (%v), want readonly", opened, err)
	}

	for _, tc := range []struct {
		name, path, body string
	}{
		{"submit", "/submit", `{"text":"hi"}`},
		{"approval", "/approval", `{"tool_use_id":"x","decision":"allow"}`},
		{"cancel", "/cancel", `{}`},
	} {
		resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+tc.path, "c2", env.srv.URL, tc.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("readonly %s status = %d, want 403", tc.name, resp.StatusCode)
		}
	}

	resp2 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/open", "c2", env.srv.URL, `{"takeover":true}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("c2 takeover status = %d, want 200", resp2.StatusCode)
	}
	var takeover struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&takeover); err != nil || takeover.Role != "interactive" {
		t.Fatalf("c2 takeover role = %+v (%v), want interactive", takeover, err)
	}

	resp3 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "c1", env.srv.URL, `{"text":"hi"}`)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Fatalf("被接管后 c1 submit status = %d, want 403", resp3.StatusCode)
	}
}

// TestPerTabClientIDIdentity 钉(评审 I1,spec 具名裁定「后开者只读跟随」):
// 同浏览器的两个标签页共享同一会话 Cookie、各持独立每标签页令牌
// (X-Mulix-Client 头 mutating 面 / ?client= 查询参数 SSE 面)——后开者
// 只读跟随,不再因 Cookie 同值而复用交互权;同源防护层不变(无 Cookie 的
// mutating 仍 403);takeover 与 SSE 断开释放仍按每标签页身份生效。
func TestPerTabClientIDIdentity(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})

	// tab1 create+open(共享 Cookie ck + 令牌 tab1)→ interactive
	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions", "ck", env.srv.URL, `{"workspace":"`+testWorkspace+`"}`, "tab1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d, want 201(body: %s)", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("create 响应缺 id: %v", err)
	}
	id := created.ID

	roleOf := func(t *testing.T, url, clientID string) string {
		t.Helper()
		r := httpDo(t, "POST", url, "ck", env.srv.URL, `{}`, clientID)
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(r.Body)
			t.Fatalf("open status = %d, want 200(body: %s)", r.StatusCode, body)
		}
		var out struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			t.Fatalf("open 解码: %v", err)
		}
		return out.Role
	}
	openURL := env.srv.URL + "/api/sessions/" + id + "/open"
	if got := roleOf(t, openURL, "tab1"); got != "interactive" {
		t.Fatalf("tab1 open role = %s, want interactive", got)
	}

	// 后开者只读跟随(修复前:clientID 取自 Cookie,tab2 与 tab1 同值 →
	// 误授予 interactive——spec 具名裁定的缺口)
	if got := roleOf(t, openURL, "tab2"); got != "readonly" {
		t.Fatalf("tab2(后开标签页) open role = %s, want readonly", got)
	}
	resp3 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "ck", env.srv.URL, `{"text":"hi"}`, "tab2")
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Fatalf("tab2 submit status = %d, want 403(后开者只读)", resp3.StatusCode)
	}
	if got := roleOf(t, openURL, "tab1"); got != "interactive" {
		t.Fatalf("tab1 仍应持有交互权, got %s", got)
	}

	// SSE ?client= 承载同一身份:tab1 无 Cookie 纯查询参数订阅,断开后释放
	// 的是 tab1 令牌(修复前 clientIDOf 认 Cookie,无 Cookie → 释放 no-op,
	// 持有者滞留 → 下方 tab2 无接管不得 interactive)
	s := openSSEWithClient(t, env.srv.URL, id, "", "tab1")
	s.waitConnected(t, 2*time.Second)
	s.resp.Body.Close()
	waitForCond(t, 2*time.Second, "tab1 断开释放生效(持有者清空)", func() bool {
		h, ok := env.mgr.get(id)
		return ok && holderOf(h) == ""
	})
	if got := roleOf(t, openURL, "tab2"); got != "interactive" {
		t.Fatalf("tab1 断开宽限释放后 tab2 open role = %s, want interactive", got)
	}

	// takeover 语义仍按每标签页身份工作:tab1 接管 → interactive;tab2 降级
	respT := httpDo(t, "POST", openURL, "ck", env.srv.URL, `{"takeover":true}`, "tab1")
	var takeover struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(respT.Body).Decode(&takeover); err != nil || takeover.Role != "interactive" {
		body, _ := io.ReadAll(respT.Body)
		respT.Body.Close()
		t.Fatalf("tab1 takeover role = %+v (%v), want interactive(body: %s)", takeover, err, body)
	}
	respT.Body.Close()
	resp4 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "ck", env.srv.URL, `{"text":"hi"}`, "tab2")
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusForbidden {
		t.Fatalf("被接管后 tab2 submit status = %d, want 403", resp4.StatusCode)
	}
}

// TestOriginAndCookieEnforced 钉(同源防护):无 Cookie POST → 403;Cookie +
// 异源 Origin → 403;Cookie + 无 Origin(非浏览器客户端,文档化选择)与同源
// Origin → 通过;GET / 下发 mulix_session Cookie(SameSite=Strict)。
func TestOriginAndCookieEnforced(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})
	createURL := env.srv.URL + "/api/sessions"
	createBody := `{"workspace":"` + testWorkspace + `"}`

	resp := httpDo(t, "POST", createURL, "", "", createBody)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 Cookie POST status = %d, want 403", resp.StatusCode)
	}

	resp2 := httpDo(t, "POST", createURL, "ck", "http://evil.example", createBody)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("异源 Origin POST status = %d, want 403", resp2.StatusCode)
	}

	resp3 := httpDo(t, "POST", createURL, "ck", "", createBody)
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp3.Body)
		t.Fatalf("Cookie+无 Origin POST status = %d, want 201(非浏览器客户端放行,body: %s)", resp3.StatusCode, body)
	}

	resp4 := httpDo(t, "POST", createURL, "ck2", env.srv.URL, createBody)
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp4.Body)
		t.Fatalf("Cookie+同源 Origin POST status = %d, want 201(body: %s)", resp4.StatusCode, body)
	}

	resp5, err := http.Get(env.srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp5.Body.Close()
	var issued *http.Cookie
	for _, ck := range resp5.Cookies() {
		if ck.Name == sessionCookieName {
			issued = ck
		}
	}
	if issued == nil || issued.Value == "" {
		t.Fatalf("GET / 未下发 %s Cookie", sessionCookieName)
	}
	if issued.SameSite != http.SameSiteStrictMode {
		t.Fatalf("Cookie SameSite = %v, want Strict", issued.SameSite)
	}
}

// TestApprovalRoundTrip 钉(审批通路端到端):canUseTool fixture → SSE 收
// approval_request → POST allow → SSE 收 approval_resolved(allow,user)且
// 镜像含两事件各恰一次(1:1;先镜像后可见由 control 保证——此处经回放面复核)。
func TestApprovalRoundTrip(t *testing.T) {
	env := newHTTPEnv(t, scriptWithApproval(t, "req-http", "tu-http"))
	id := createAndOpen(t, env, "approver")

	s := openSSE(t, env.srv.URL, id, "approver")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "approver", env.srv.URL, `{"text":"write it"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d, want 202", resp.StatusCode)
	}

	reqFrame := s.waitFrame(t, "approval_request", 5*time.Second)
	ev, _ := reqFrame["ev"].(map[string]any)
	if got, _ := ev["tool_use_id"].(string); got != "tu-http" {
		t.Fatalf("approval_request tool_use_id = %v, want tu-http", ev["tool_use_id"])
	}

	resp2 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/approval", "approver", env.srv.URL, `{"tool_use_id":"tu-http","decision":"allow"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("approval status = %d, want 200(body: %s)", resp2.StatusCode, body)
	}

	resFrame := s.waitFrame(t, "approval_resolved", 5*time.Second)
	rev, _ := resFrame["ev"].(map[string]any)
	if rev["decision"] != "allow" || rev["source"] != "user" || rev["resolved_by"] != "user" {
		t.Fatalf("approval_resolved = %+v, want allow/user/user", rev)
	}

	// 回合收口(allow 后泵续跑 fixture 余下事件 → turn_done)
	waitForCond(t, 10*time.Second, "回合收口", func() bool {
		st := getSessionState(t, env, id)
		running, _ := st["running"].(bool)
		return st != nil && !running
	})

	// 镜像 1:1:回放面含两事件各恰一次
	resp3 := httpDo(t, "GET", env.srv.URL+"/api/sessions/"+id+"/events", "", "", "")
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", resp3.StatusCode)
	}
	frames := readNDJSONFrames(t, resp3)
	reqCount, resCount := 0, 0
	for _, f := range frames {
		switch f["kind"] {
		case "approval_request":
			reqCount++
		case "approval_resolved":
			resCount++
		}
	}
	if reqCount != 1 || resCount != 1 {
		t.Fatalf("镜像审批链 1:1 破坏: approval_request=%d approval_resolved=%d", reqCount, resCount)
	}
}

// TestEventsReplayWithWarnings 钉(FR-003):半行镜像 → events 端点返回好
// 事件 + warnings 头帧非空(非变异回放——镜像字节不动)。
func TestEventsReplayWithWarnings(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})

	st, err := sessionx.Create(env.root, sessionx.Options{ID: "s-half", Workspace: testWorkspace})
	if err != nil {
		t.Fatalf("create s-half: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close s-half: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(env.root, "s-half", "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	if _, err := f.WriteString(`{"kind":"turn_started","ev":{"seq":1,"session_id":"s-half","engine_session_id":"e1","pinned_model":"m","unattended":false}}` + "\n"); err != nil {
		t.Fatalf("write good line: %v", err)
	}
	if _, err := f.WriteString(`{"kind":"turn_d`); err != nil { // 半行——损坏段
		t.Fatalf("write half line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close events: %v", err)
	}

	resp := httpDo(t, "GET", env.srv.URL+"/api/sessions/s-half/events", "", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200(损坏段降级不阻塞)", resp.StatusCode)
	}
	frames := readNDJSONFrames(t, resp)
	if len(frames) < 2 {
		t.Fatalf("frames = %d, want ≥2(头帧 + 事件帧)", len(frames))
	}
	warnings, _ := frames[0]["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("头帧 warnings = %v, want 非空(FR-003)", frames[0]["warnings"])
	}
	var sawGood bool
	for _, fr := range frames[1:] {
		if fr["kind"] == "turn_started" {
			sawGood = true
			ev, _ := fr["ev"].(map[string]any)
			if ev["session_id"] != "s-half" {
				t.Fatalf("好事件 ev = %+v, want session_id=s-half", ev)
			}
		}
	}
	if !sawGood {
		t.Fatalf("好事件未回放(frames=%+v)", frames)
	}

	resp2, err := http.Get(env.srv.URL + "/api/sessions/no-such/events")
	if err != nil {
		t.Fatalf("events unknown: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown events status = %d, want 404", resp2.StatusCode)
	}
}

// TestStateSnapshot 钉:GET state → Snapshot 投影(session_id/ready/running/
// meter 对位);未知 id → 404。
func TestStateSnapshot(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})
	id := createAndOpen(t, env, "c-state")

	st := getSessionState(t, env, id)
	if st == nil {
		t.Fatal("state 读取失败")
	}
	if st["session_id"] != id {
		t.Fatalf("session_id = %v, want %s", st["session_id"], id)
	}
	switch st["ready"] {
	case "NOT_READY", "READY", "FAILED":
	default:
		t.Fatalf("ready = %v, want 就绪态集", st["ready"])
	}
	if running, ok := st["running"].(bool); !ok || running {
		t.Fatalf("running = %v, want false", st["running"])
	}
	if _, ok := st["meter"].(map[string]any); !ok {
		t.Fatalf("meter = %v, want 对象", st["meter"])
	}

	resp, err := http.Get(env.srv.URL + "/api/sessions/nope/state")
	if err != nil {
		t.Fatalf("state unknown: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown state status = %d, want 404", resp.StatusCode)
	}
}
