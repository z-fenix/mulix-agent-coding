// e2e_test.go——serve 全栈 e2e(2026-10-18-web-ui-mvp Task 13):httptest 起
// newMux+hostManager,装配 = FakeHost × 真实 control/guard/sessionx/cache 栈
// (复用 http_test/sse_test/assemble_test 既有 helper——同包测试基建不跨包
// 复用,新增文件只做场景编排,不改既有签名)。五场景:
//
//  1. happy 回合 HTTP 全序对账(SSE 全序 = 回放全序 = 镜像全序,逐位 1:1);
//  2. 审批 allow 闭环(request/resolved 落 SSE 与镜像,回合续跑收口);
//  3. 交互端离线:宽限(注入缩短)到点无人认领 → fail-closed deny 全部待审;
//  4. 跨进程锁:新鲜 web.lock → POST open 409 呈持有者 pid;
//  5. cancel:待审一并 fail-closed 拒绝 + 回合收口(FR-007 闭环)。
//
// 红线断言面:先镜像后可见(SSE 见到 turn_done 时镜像必已含全部事件——
// 1:1 对账即其观测面);fail-closed(离线 deny/锁 409);治理语义零改动
// (事件词汇 = control 原生,resolved_by 只读不断言改写)。
package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mulix/internal/engine"
	"mulix/internal/event"
)

// happyTurnKinds 是 happy-turn fixture 单回合的镜像事件全序(Submit 起始的
// turn_started + fixture 事实序:thinking → 工具请求 → 工具结果 → 文本 →
// usage → done)。happy 场景钉 HTTP 全栈逐位一致。
var happyTurnKinds = []string{
	"turn_started", "assistant_thinking", "tool_use_requested",
	"tool_result", "assistant_text", "usage", "turn_done",
}

// sseFramesUntil 收集 SSE 帧直至 finalKind 到达(含),返回沿途全部帧。
func sseFramesUntil(t *testing.T, s *sseStream, finalKind string, timeout time.Duration) []map[string]any {
	t.Helper()
	var frames []map[string]any
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				t.Fatalf("SSE 流在收集 %s 前终结(已收 %d 帧)", finalKind, len(frames))
			}
			frames = append(frames, f)
			if k, _ := f["kind"].(string); k == finalKind {
				return frames
			}
		case <-deadline:
			t.Fatalf("timeout collecting %q (got %d frames)", finalKind, len(frames))
		}
	}
}

// kindsOfFrames 帧序列 → kind 序列。
func kindsOfFrames(frames []map[string]any) []string {
	kinds := make([]string, 0, len(frames))
	for _, f := range frames {
		k, _ := f["kind"].(string)
		kinds = append(kinds, k)
	}
	return kinds
}

// evOf 取帧的 ev 对象(缺形返回 nil——断言面自行判空)。
func evOf(f map[string]any) map[string]any {
	ev, _ := f["ev"].(map[string]any)
	return ev
}

// mirrorEvents 读在册会话的镜像事件(relaxed——泵并发追加下好行照读)。
func mirrorEvents(t *testing.T, env *httpEnv, id string) ([]any, []string) {
	t.Helper()
	h, ok := env.mgr.get(id)
	if !ok {
		t.Fatalf("session %s 不在册", id)
	}
	events, warnings, err := h.ctrl.Mirror().ReadEventsRelaxed()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	return events, warnings
}

// assertTripleAccounting 是 1:1 对账核心(assertSinkMirrorsStore 的 HTTP 全栈
// 形态):SSE 实时流观测序 = GET /events 回放序(首帧 warnings 头,其后事件帧)
// = 权威镜像事件序,三者逐位一致。
func assertTripleAccounting(t *testing.T, env *httpEnv, id string, sse []string) {
	t.Helper()
	resp := httpDo(t, "GET", env.srv.URL+"/api/sessions/"+id+"/events", "", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", resp.StatusCode)
	}
	frames := readNDJSONFrames(t, resp)
	if len(frames) < 2 {
		t.Fatalf("回放帧 = %d, want ≥2(warnings 头帧 + 事件帧)", len(frames))
	}
	if _, hasWarnings := frames[0]["warnings"]; !hasWarnings {
		t.Fatalf("回放首帧缺 warnings 头: %v", frames[0])
	}
	replay := kindsOfFrames(frames[1:])
	events, warnings := mirrorEvents(t, env, id)
	if len(warnings) != 0 {
		t.Fatalf("镜像完好会话的回放 warnings = %v, want 空", warnings)
	}
	storeKinds := kindsOf(t, events)
	if len(storeKinds) != len(sse) {
		t.Fatalf("镜像(%d)与 SSE(%d)事件数不等: store=%v sse=%v", len(storeKinds), len(sse), storeKinds, sse)
	}
	for i := range storeKinds {
		if storeKinds[i] != sse[i] {
			t.Fatalf("对账[%d]: store=%s sse=%s\nstore=%v\nsse=%v", i, storeKinds[i], sse[i], storeKinds, sse)
		}
	}
	if len(replay) != len(sse) {
		t.Fatalf("回放(%d)与 SSE(%d)帧数不等: replay=%v sse=%v", len(replay), len(sse), replay, sse)
	}
	for i := range replay {
		if replay[i] != sse[i] {
			t.Fatalf("回放[%d]: replay=%s sse=%s", i, replay[i], sse[i])
		}
	}
}

// submitText 走交互端 submit 通路(202 断言 + 回合号=1)。
func submitText(t *testing.T, env *httpEnv, id, clientID, text string) {
	t.Helper()
	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", clientID, env.srv.URL, `{"text":"`+text+`"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("submit status = %d, want 202(body: %s)", resp.StatusCode, body)
	}
	var out struct {
		Turn int64 `json:"turn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Turn != 1 {
		t.Fatalf("submit turn = %+v (%v), want 1", out, err)
	}
}

// TestE2EHappyTurnViaHTTP 钉(场景 1):create → open → SSE → submit →
// 回合事件全序到齐(词汇与相对序 = fixture 事实序)→ GET events 回放与
// SSE 全序逐位一致且与镜像 1:1(先镜像后可见的对账观测面)。
func TestE2EHappyTurnViaHTTP(t *testing.T) {
	env := newHTTPEnv(t, fixtureTurnScript(t))
	id := createAndOpen(t, env, "e2e-happy")

	s := openSSE(t, env.srv.URL, id, "e2e-happy")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	submitText(t, env, id, "e2e-happy", "run the fixture")

	sse := kindsOfFrames(sseFramesUntil(t, s, "turn_done", 10*time.Second))
	if len(sse) != len(happyTurnKinds) {
		t.Fatalf("回合事件数 = %d, want %d(kinds=%v)", len(sse), len(happyTurnKinds), sse)
	}
	for i := range happyTurnKinds {
		if sse[i] != happyTurnKinds[i] {
			t.Fatalf("SSE 帧序[%d] = %s, want %s(全序=%v)", i, sse[i], happyTurnKinds[i], sse)
		}
	}
	assertTripleAccounting(t, env, id, sse)
}

// TestE2EApprovalFlowViaHTTP 钉(场景 2):canUseTool 控制请求(权限回路系统
// 边界)→ SSE approval_request → POST allow → 工具续跑 → approval_resolved
// (allow,user) 紧随 request 落 SSE,镜像两事件各恰一次,全栈 1:1 对账。
func TestE2EApprovalFlowViaHTTP(t *testing.T) {
	env := newHTTPEnv(t, scriptWithApproval(t, "req-e2e-appr", "tu-e2e-appr"))
	id := createAndOpen(t, env, "e2e-approver")

	s := openSSE(t, env.srv.URL, id, "e2e-approver")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	submitText(t, env, id, "e2e-approver", "write it")

	pre := sseFramesUntil(t, s, "approval_request", 10*time.Second)
	if ev := evOf(pre[len(pre)-1]); ev == nil || ev["tool_use_id"] != "tu-e2e-appr" {
		t.Fatalf("approval_request ev = %v, want tool_use_id=tu-e2e-appr", ev)
	}

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/approval", "e2e-approver", env.srv.URL, `{"tool_use_id":"tu-e2e-appr","decision":"allow"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("approval status = %d, want 200(body: %s)", resp.StatusCode, body)
	}

	rest := sseFramesUntil(t, s, "turn_done", 10*time.Second)
	frames := append(append([]map[string]any{}, pre...), rest...)
	sse := kindsOfFrames(frames)
	reqIdx, resIdx := -1, -1
	for i, k := range sse {
		switch k {
		case "approval_request":
			if reqIdx == -1 {
				reqIdx = i
			}
		case "approval_resolved":
			if resIdx == -1 {
				resIdx = i
			}
		}
	}
	if reqIdx == -1 || resIdx != reqIdx+1 {
		t.Fatalf("审批链帧序异常: request=%d resolved=%d (kinds=%v)", reqIdx, resIdx, sse)
	}
	if ev := evOf(frames[resIdx]); ev == nil || ev["decision"] != "allow" || ev["source"] != "user" || ev["resolved_by"] != "user" {
		t.Fatalf("approval_resolved ev = %v, want allow/user/user", ev)
	}
	// allow 后回合续跑至收口(usage 先于 turn_done——记账后收口)
	usageIdx := -1
	for i, k := range sse {
		if k == "usage" {
			usageIdx = i
			break
		}
	}
	if usageIdx == -1 || usageIdx < resIdx || sse[len(sse)-1] != "turn_done" {
		t.Fatalf("allow 后回合未按序续跑: usage=%d kinds=%v", usageIdx, sse)
	}
	// 镜像:审批两事件各恰一次 + 全栈 1:1
	assertTripleAccounting(t, env, id, sse)
	events, _ := mirrorEvents(t, env, id)
	reqCount, resCount := 0, 0
	for _, e := range events {
		switch e.(type) {
		case event.ApprovalRequestEvent:
			reqCount++
		case event.ApprovalResolvedEvent:
			resCount++
		}
	}
	if reqCount != 1 || resCount != 1 {
		t.Fatalf("镜像审批链 1:1 破坏: request=%d resolved=%d", reqCount, resCount)
	}
}

// TestE2EInteractiveGoneDenyViaHTTP 钉(场景 3,红线 fail-closed):审批挂起
// (泵阻塞在桥)→ 交互端断开 SSE → 宽限(注入缩短)到点无人认领 → gone →
// 桥 CancelAll(interactive_offline) deny 全部待审 → approval_resolved(deny,
// user,interactive_offline) 落镜像恰一次,回合收口不悬挂。绝不允许静默放行。
func TestE2EInteractiveGoneDenyViaHTTP(t *testing.T) {
	old := gracePeriod
	gracePeriod = 30 * time.Millisecond
	defer func() { gracePeriod = old }()

	env := newHTTPEnv(t, scriptWithApproval(t, "req-gone", "tu-gone"))
	id := createAndOpen(t, env, "e2e-gone")

	s := openSSE(t, env.srv.URL, id, "e2e-gone")
	s.waitConnected(t, 2*time.Second)

	submitText(t, env, id, "e2e-gone", "write it")
	s.waitFrame(t, "approval_request", 10*time.Second) // 泵已阻塞在审批桥

	// 交互端断开 = 持有者 release → 缩短后的宽限到点无人认领 → gone
	s.resp.Body.Close()

	var resolved event.ApprovalResolvedEvent
	found := false
	waitForCond(t, 10*time.Second, "approval_resolved(interactive_offline) 落镜像", func() bool {
		events, _ := mirrorEvents(t, env, id)
		for _, e := range events {
			if r, ok := e.(event.ApprovalResolvedEvent); ok {
				resolved, found = r, true
			}
		}
		return found
	})
	if resolved.Decision != "deny" || resolved.Source != "user" || resolved.ResolvedBy != "interactive_offline" {
		t.Fatalf("approval_resolved = %+v, want deny/user/interactive_offline(fail-closed)", resolved)
	}
	// 审批链 1:1:请求与裁决各恰一次;回合收口不悬挂
	events, _ := mirrorEvents(t, env, id)
	reqCount, resCount, doneCount := 0, 0, 0
	for _, e := range events {
		switch e.(type) {
		case event.ApprovalRequestEvent:
			reqCount++
		case event.ApprovalResolvedEvent:
			resCount++
		case event.TurnDoneEvent:
			doneCount++
		}
	}
	if reqCount != 1 || resCount != 1 {
		t.Fatalf("镜像审批链 1:1 破坏: request=%d resolved=%d", reqCount, resCount)
	}
	waitForCond(t, 10*time.Second, "回合收口(turn_done 落镜像)", func() bool {
		events, _ := mirrorEvents(t, env, id)
		for _, e := range events {
			if _, ok := e.(event.TurnDoneEvent); ok {
				return true
			}
		}
		return false
	})
}

// TestE2EReconnectWithinGraceReclaims 钉(评审 C1,FR-009 裁定的线级契约):
// 审批挂起 → 交互端断开 SSE(服务端 release 进入宽限)→ 宽限内重连重申交互权
// (POST open——前端 stream.ts 每次 EventSource open 同形态)→ claim 取消宽限
// 离线计时 → 审批**不**被 fail-closed deny(镜像无 interactive_offline 裁决),
// 且重连后的审批 POST 照常成功、回合收口。前端若不在重连时重 claim,本契约
// 即被破坏(宽限到点 CancelAll 拒绝在审请求——正是评审 C1 的失败链)。
func TestE2EReconnectWithinGraceReclaims(t *testing.T) {
	old := gracePeriod
	gracePeriod = 300 * time.Millisecond
	defer func() { gracePeriod = old }()

	env := newHTTPEnv(t, scriptWithApproval(t, "req-reconnect", "tu-reconnect"))
	id := createAndOpen(t, env, "e2e-reconnect")

	s := openSSE(t, env.srv.URL, id, "e2e-reconnect")
	s.waitConnected(t, 2*time.Second)

	submitText(t, env, id, "e2e-reconnect", "write it")
	s.waitFrame(t, "approval_request", 10*time.Second) // 泵已阻塞在审批桥

	// 交互端断开 = 持有者 release → 进入宽限;等服务端确实观察到断开
	// (release 生效)再重连——否则同 client claim 先于 release 只是幂等空转,
	// 测不到「宽限计时被取消」这一契约面。
	s.resp.Body.Close()
	waitForCond(t, 2*time.Second, "服务端 release 生效(持有者清空)", func() bool {
		h, ok := env.mgr.get(id)
		return ok && holderOf(h) == ""
	})

	// 宽限内重连:重开流 + POST open 重申交互权(前端 stream.ts onopen 同形态)
	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/open", "e2e-reconnect", env.srv.URL, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("re-claim open status = %d, want 200(body: %s)", resp.StatusCode, body)
	}
	var opened struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil || opened.Role != "interactive" {
		t.Fatalf("re-claim role = %+v (%v), want interactive(宽限内重连即交互端就位)", opened, err)
	}

	// 宽限已过(等待数倍宽限时长)而审批未被 interactive_offline deny
	time.Sleep(3 * gracePeriod)
	events, _ := mirrorEvents(t, env, id)
	for _, e := range events {
		if r, ok := e.(event.ApprovalResolvedEvent); ok {
			t.Fatalf("宽限内重连仍被 fail-closed deny(重 claim 未取消离线计时): %+v", r)
		}
	}

	// 重连后的流 + 审批 POST 照常成功,回合收口(allow 后泵续跑)
	s2 := openSSE(t, env.srv.URL, id, "e2e-reconnect")
	defer s2.resp.Body.Close()
	s2.waitConnected(t, 2*time.Second)

	resp2 := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/approval", "e2e-reconnect", env.srv.URL, `{"tool_use_id":"tu-reconnect","decision":"allow"}`)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("approval after reconnect status = %d, want 200(body: %s)", resp2.StatusCode, body)
	}
	resFrame := s2.waitFrame(t, "approval_resolved", 10*time.Second)
	if ev := evOf(resFrame); ev == nil || ev["decision"] != "allow" || ev["resolved_by"] != "user" {
		t.Fatalf("approval_resolved ev = %v, want allow/user", ev)
	}
	s2.waitFrame(t, "turn_done", 10*time.Second)
}

// TestE2ECrossProcessLockViaHTTP 钉(场景 4,红线 fail-closed):会话目录被
// 外部进程新鲜锁(pid=99999,heartbeat=now)持有 → POST open 409 且 body 呈
// 持有者 pid——绝不静默接管别人活着的会话。
func TestE2ECrossProcessLockViaHTTP(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})
	newDiskSession(t, env.root, "s-e2e-locked")
	payload, err := json.Marshal(webLock{PID: 99999, HeartbeatUnix: time.Now().Unix()})
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "s-e2e-locked", lockFileName), payload, 0o644); err != nil {
		t.Fatalf("write web.lock: %v", err)
	}

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/s-e2e-locked/open", "e2e-locker", env.srv.URL, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("open status = %d, want 409(body: %s)", resp.StatusCode, body)
	}
	var out struct {
		Error string `json:"error"`
		PID   int    `json:"pid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if out.PID != 99999 {
		t.Fatalf("409 body pid = %d, want 99999(呈持有者)", out.PID)
	}
	if out.Error == "" {
		t.Fatal("409 body error 为空")
	}
}

// TestE2ECancelViaHTTP 钉(场景 5,FR-007 闭环):审批挂起 → POST cancel →
// control.Cancel 语义 = 待审一并 fail-closed 拒绝(resolved_by=cancelled:
// interrupt)+ host.Interrupt 到引擎;回合续跑至收口。turn_done 的 subtype 归
// 引擎 result 所有(control 不产 canceled 收口形态——已核 controller.go:
// Subtype 直取 r.SubType),故断言面 = deny 裁决留痕 + 回合收口 + Interrupt
// 不吞待审。
func TestE2ECancelViaHTTP(t *testing.T) {
	env := newHTTPEnv(t, scriptWithApproval(t, "req-cancel", "tu-cancel"))
	id := createAndOpen(t, env, "e2e-canceller")

	s := openSSE(t, env.srv.URL, id, "e2e-canceller")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	submitText(t, env, id, "e2e-canceller", "write it")
	s.waitFrame(t, "approval_request", 10*time.Second)

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/cancel", "e2e-canceller", env.srv.URL, `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("cancel status = %d, want 200(body: %s)", resp.StatusCode, body)
	}

	resFrame := s.waitFrame(t, "approval_resolved", 10*time.Second)
	if ev := evOf(resFrame); ev == nil || ev["decision"] != "deny" || ev["resolved_by"] != "cancelled:interrupt" {
		t.Fatalf("cancel 后 approval_resolved ev = %v, want deny/cancelled:interrupt", ev)
	}
	s.waitFrame(t, "turn_done", 10*time.Second)

	events, _ := mirrorEvents(t, env, id)
	resCount, doneCount := 0, 0
	for _, e := range events {
		switch v := e.(type) {
		case event.ApprovalResolvedEvent:
			resCount++
			if v.Decision != "deny" || v.ResolvedBy != "cancelled:interrupt" {
				t.Fatalf("镜像 approval_resolved = %+v, want deny/cancelled:interrupt", v)
			}
		case event.TurnDoneEvent:
			doneCount++
		}
	}
	if resCount != 1 || doneCount != 1 {
		t.Fatalf("镜像收口形态异常: approval_resolved=%d turn_done=%d", resCount, doneCount)
	}
}
