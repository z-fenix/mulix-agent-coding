// http.go——HTTP API 全表(2026-10-18-web-ui-mvp Task 7;路由表 = 设计 §4
// = task-7-brief 契约)。
//
// 错误映射:ErrTurnRunning/回合在飞 → 409;readonly(非交互端持有者)→ 403;
// 未知 id → 404;resume 遇未收口回合 → 409;跨进程锁拒绝 → 409(呈持有者
// pid);未知 workspace → 400;审批无此待审请求 → 404(裁定:tool_use_id 是
// 寻址资源,重试同 id 不会成功,404 优于 409——报告记录)。
//
// 同源防护(fail-closed):全部 mutating(POST)端点要求会话 Cookie 在册 且
// Origin 同源或缺失——缺失放行是刻意选择:非浏览器客户端(curl/测试)不携带
// Origin;浏览器侧的跨站 POST 必带 Origin,异源一律 403,SameSite=Strict
// Cookie 是第二层(CSRF 面双保险)。
//
// 红线:不给 internal/control、internal/guard 增改一行;hostManager/sessionHost
// 语义零改动(同包内只读访问未导出字段,状态机写面仍走 claim/release/close)。
package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mulix/internal/control"
	"mulix/internal/event"
	"mulix/internal/sessionx"
)

// maxBodyBytes 是请求体上限(防御性;会话面请求均为小 JSON)。
const maxBodyBytes = 1 << 20

// approvalResolvedBy 是 web 审批的留痕身份(approval_resolved.resolved_by)。
const approvalResolvedBy = "user"

// webAPI 是会话 HTTP 面的接收器:manager + Options(装配参数/工作区清单)。
type webAPI struct {
	mgr  *hostManager
	opts Options
}

// ---- 通用面 ----

// writeJSON/writeError 统一 JSON 应答形态。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// clientIDHeader / clientIDQuery 是每标签页客户端身份的两个载体(评审 I1):
// mutating fetch 以 X-Mulix-Client 头携带;SSE 订阅(EventSource 无法设自定义
// 头)以 ?client= 查询参数携带。
const (
	clientIDHeader = "X-Mulix-Client"
	clientIDQuery  = "client"
)

// clientIDOf 解析客户端身份(claim/release/readonly 校验与 SSE 断开释放消费
// 的身份串),优先级:每标签页令牌(头 → 查询参数)> 会话 Cookie 值。
//
// 为什么不止认 Cookie(评审 I1):同浏览器的多个标签页共享 Cookie,Cookie 值
// 做身份会让后开标签页复用交互权(spec 具名裁定是「后开者只读跟随」)——前端
// 以 sessionStorage 随机令牌区分标签页。Cookie 回退保留简单客户端与既有测试
// (curl/httplib 不带头)的通路。同源防护层与本解析无关:mutating 中间件仍先
// 校验 Cookie + Origin——跨站页面既不能带我们的 Cookie(SameSite=Strict)也
// 无法设置自定义头(CORS 预检必败),身份串可从外部提供不放大 CSRF 面。
func clientIDOf(r *http.Request) string {
	if v := r.Header.Get(clientIDHeader); v != "" {
		return v
	}
	if v := r.URL.Query().Get(clientIDQuery); v != "" {
		return v
	}
	ck, err := r.Cookie(sessionCookieName)
	if err != nil || ck.Value == "" {
		return ""
	}
	return ck.Value
}

// randomToken 生成随机令牌(16 字节 hex;cli newSessionID 同形态独立实现
// ——组合根不互依)。
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 熵源失败极罕见;时间兜底(可预测令牌只影响并列概率,不影响正确性)
		return fmt.Sprintf("s%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// logf 留痕面(Stderr 缺省丢弃)。
func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "mulix: "+format+"\n", args...)
}

// mutating 是同源防护中间件:会话 Cookie 在册 + Origin 同源或缺失,否则 403
// (校验先于任何 body 解析——403 不泄露路由细节)。Cookie 在册直接查 Cookie
// 而非 clientIDOf(评审 I1 后身份可来自头/查询参数,但 Cookie 仍是浏览器
// 同源性的凭据——本层语义零改动)。
func (a *webAPI) mutating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie(sessionCookieName); err != nil || ck.Value == "" {
			writeError(w, http.StatusForbidden, "missing session cookie")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
		}
		next(w, r)
	}
}

// spa 静态面(GET /):首次到访下发会话 Cookie(随机令牌,SameSite=Strict,
// Path=/)——Cookie 是同源防护凭据与 clientID 的回退载体(评审 I1 后优先
// 身份是每标签页令牌:头/查询参数,见 clientIDOf)。
func (a *webAPI) spa() http.Handler {
	static := http.FileServerFS(distFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if clientIDOf(r) == "" {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookieName, Value: randomToken(),
				Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: true,
			})
		}
		static.ServeHTTP(w, r)
	})
}

// ---- 工作区 ----

// allowedRoots 工作区清单:Config.Web.WorkspaceRoots 优先;空 → [Options.
// Workspace](serve 启动时取 cwd 的约定)。
func (a *webAPI) allowedRoots() []string {
	if a.opts.Config != nil && len(a.opts.Config.Web.WorkspaceRoots) > 0 {
		return a.opts.Config.Web.WorkspaceRoots
	}
	if a.opts.Workspace != "" {
		return []string{a.opts.Workspace}
	}
	return []string{}
}

func (a *webAPI) handleWorkspaces(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": a.allowedRoots()})
}

// workspaceAllowed 校验工作区 ∈ 清单(字面匹配——UI 从 /api/workspaces 取值
// 回传;路径归一化是第二期)。
func (a *webAPI) workspaceAllowed(ws string) bool {
	if ws == "" {
		return false
	}
	for _, root := range a.allowedRoots() {
		if root == ws {
			return true
		}
	}
	return false
}

// workspaceRecoveryBlocked 是 create 的 cli 恢复门(互指 internal/cli/
// session.go checkRecovery:86-110——本工作区**最新有效会话**存在未收口回合
// 即拒绝,更老镜像不检查;恢复门是「上次运行中断」判定,非全库审计)。
func (a *webAPI) workspaceRecoveryBlocked(ws string) (string, bool) {
	sessions, _, err := sessionx.ScanSessions(a.opts.SessionsRoot)
	if err != nil { // 镜像根不可读 = 判定不能 → fail-closed 拒绝
		return fmt.Sprintf("scan sessions: %v", err), true
	}
	for _, info := range sessions { // 新在前——首个工作区匹配者即最新有效会话
		if info.Workspace != ws {
			continue
		}
		store, err := sessionx.OpenStore(sessionx.SessionDir(a.opts.SessionsRoot, info.ID))
		if err != nil {
			continue // 打不开的镜像不是恢复对象(诊断面不阻塞)
		}
		need, openTurn, err := control.RecoveryRequired(store)
		_ = store.Close()
		if err != nil {
			continue
		}
		if need {
			return fmt.Sprintf("会话 %s 回合 t%d 未收口（上次运行中断），拒绝在工作区开新会话；请先收口该回合或续链", info.ID, openTurn), true
		}
		return "", false
	}
	return "", false
}

// ---- 列表 ----

// sessionEntry 是列表条目(JSON 投影;open=true = 本进程在册真实状态,
// open=false = 磁盘镜像 + 启发式)。
type sessionEntry struct {
	ID           string `json:"id"`
	Workspace    string `json:"workspace"`
	Status       string `json:"status"`
	LastActivity string `json:"last_activity,omitempty"`
	Open         bool   `json:"open"`
}

// handleList GET /api/sessions:manager 在册(真实状态)在前,未打开的与
// ScanSessions 合并(ScanSessions created_at 序;已打开去重)。ScanSessions
// 的损坏 meta 警告如实透出(不静默)。
func (a *webAPI) handleList(w http.ResponseWriter, _ *http.Request) {
	entries := []sessionEntry{}
	seen := map[string]bool{}
	for _, s := range a.mgr.list() {
		entries = append(entries, sessionEntry{
			ID: s.ID, Workspace: s.Workspace, Status: s.Status,
			LastActivity: s.LastActivity.UTC().Format(time.RFC3339), Open: true,
		})
		seen[s.ID] = true
	}
	infos, warnings, err := sessionx.ScanSessions(a.opts.SessionsRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan sessions: %v", err))
		return
	}
	for _, info := range infos {
		if seen[info.ID] {
			continue
		}
		status, lastActivity := a.notOpenProjection(sessionx.SessionDir(a.opts.SessionsRoot, info.ID))
		entries = append(entries, sessionEntry{
			ID: info.ID, Workspace: info.Workspace, Status: status, LastActivity: lastActivity,
		})
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": entries, "warnings": warnings})
}

// notOpenProjection 是未打开会话的启发式投影(设计 §6 已知边界:非权威——
// 打开面/续链门才做权威判定):last_activity = events.jsonl mtime;末事件
// kind → interrupted(尾回合未收口)/ failed(turn_done 且 is_error)/
// closed / empty(镜像空)/ unknown(不可读)。
func (a *webAPI) notOpenProjection(dir string) (string, string) {
	path := filepath.Join(dir, "events.jsonl")
	fi, err := os.Stat(path)
	if err != nil {
		return "unknown", ""
	}
	last := fi.ModTime().UTC().Format(time.RFC3339)
	if fi.Size() == 0 {
		return "empty", last
	}
	tail, ok := lastLineTail(path)
	if !ok {
		return "unknown", last // 非空而末行不可解(半行尾/超限尾):如实不可判
	}
	switch tail.kind {
	case string(event.TurnStarted):
		return "interrupted", last
	case string(event.TurnDone):
		if tail.isError {
			return "failed", last
		}
	}
	return "closed", last
}

// tailScanWindow 限制末行回扫窗口(列表面只需要 kind,不整读镜像——大镜像
// 的列表开销有界)。
const tailScanWindow = 64 << 10

type tailInfo struct {
	kind    string
	isError bool
}

// lastLineTail 读镜像末行提取 kind/is_error;不可解(超限尾行/半行尾/空)→
// false = 启发式不可判。
func lastLineTail(path string) (tailInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return tailInfo{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return tailInfo{}, false
	}
	n := int64(tailScanWindow)
	if st.Size() < n {
		n = st.Size()
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, st.Size()-n); err != nil {
		return tailInfo{}, false
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var rec struct {
			Kind string `json:"kind"`
			Ev   struct {
				IsError bool `json:"is_error"`
			} `json:"ev"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			return tailInfo{}, false
		}
		return tailInfo{kind: rec.Kind, isError: rec.Ev.IsError}, true
	}
	return tailInfo{}, false
}

// ---- 创建/打开 ----

// hostAssembly 装配闭包与参数:测试缝(opts.assemble)优先;生产 assembleReal
// + assembleParams(Cfg/SessionsRoot/AllowDrift/Stderr;UsageRoot 空 → 镜像根
// 父目录,HostFactory nil → 生产 claude 宿主——assemble.go 零值语义)。
func (a *webAPI) hostAssembly() (assembleFn, hostOption) {
	fn := a.opts.assemble
	if fn == nil {
		fn = assembleReal
	}
	p := assembleParams{
		Cfg:          a.opts.Config,
		SessionsRoot: a.opts.SessionsRoot,
		AllowDrift:   a.opts.AllowEngineDrift,
		Stderr:       a.opts.Stderr,
	}
	return fn, withAssembly(p)
}

// handleCreate POST /api/sessions {workspace}:清单校验 → cli 恢复门(409)→
// 建镜像(sessionx.Create)→ 跨进程 web.lock(评审 M1:锁在装配前——锁失败/
// 装配失败不留孤儿镜像;engine 引导耗时数秒,锁先就位防他人打开半装配会话)
// → 装配 host(preparedStore 所有权移交 assembleReal)→ 入册。新建随机 id 无
// 并列可能,目录只可能由本次创建落盘——任何失败路径 best-effort 移除。
func (a *webAPI) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workspace string `json:"workspace"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !a.workspaceAllowed(req.Workspace) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown workspace %q", req.Workspace))
		return
	}
	if reason, blocked := a.workspaceRecoveryBlocked(req.Workspace); blocked {
		writeError(w, http.StatusConflict, reason)
		return
	}
	id := randomToken()
	dir := sessionx.SessionDir(a.opts.SessionsRoot, id)
	st, err := sessionx.Create(a.opts.SessionsRoot, sessionx.Options{
		ID: id, Workspace: req.Workspace,
		Unattended: false, // web 会话恒 attended(评审 C1,assembleReal 同口径)
	})
	if err != nil {
		// Create 失败:目录可能是本次半建残骸(meta/events 写入中断)——
		// best-effort 清除(随机 id 无并列,不存在他人目录可误删)。
		_ = os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create session: %v", err))
		return
	}
	release, lerr := acquireLock(dir)
	if lerr != nil {
		_ = st.Close()
		_ = os.RemoveAll(dir) // M1:锁失败不留孤儿镜像
		writeError(w, http.StatusConflict, fmt.Sprintf("session lock: %v", lerr))
		return
	}
	fn, opt := a.hostAssembly()
	h, err := newSessionHost(r.Context(), id, req.Workspace, false, fn, opt, withPreparedStore(st))
	if err != nil {
		// 装配失败:store 已由 assembleReal fail 闭包收口——释放锁并移除
		// 本次创建的镜像目录(评审 M1 later-failure 分支)。
		release()
		_ = os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create session: %v", err))
		return
	}
	h.attachLock(release)
	a.mgr.add(h)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "workspace": req.Workspace, "status": h.summary().Status})
}

// openResult 是 open 的应答(id + 角色名)。
type openResult struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// roleName 角色名投影。
func roleName(r sessionRole) string {
	if r == roleInteractive {
		return "interactive"
	}
	return "readonly"
}

// holderOf/isInteractive 是同包内对持有者状态的只读访问(状态机写面仍走
// claim/release——http 层不自持任何交互态)。
func holderOf(h *sessionHost) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.holder
}

func isInteractive(h *sessionHost, clientID string) bool {
	if clientID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.holder == clientID
}

// isRecoveryRefusal 识别装配闭包内恢复门的拒绝(openForResume 错误面;HTTP
// 层已前置权威判定,此处兜装配窗口内的竞态)。
func isRecoveryRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), "未收口")
}

// handleOpen POST /api/sessions/{id}/open {takeover?}:在册 → (takeover 先
// 释放现持有者)claim;未在册 → 镜像存在性(404)→ 恢复门(未收口 → 409)
// → 跨进程锁(新鲜 → 409 呈 pid)→ resume 装配 → 入册 → claim。应答含角色。
func (a *webAPI) handleOpen(w http.ResponseWriter, r *http.Request) {
	clientID := clientIDOf(r)
	id := r.PathValue("id")
	var req struct {
		Takeover bool `json:"takeover"`
	}
	if body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes)); err == nil && len(body) > 0 {
		if jerr := json.Unmarshal(body, &req); jerr != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if h, ok := a.mgr.get(id); ok {
		if req.Takeover {
			if cur := holderOf(h); cur != "" && cur != clientID {
				h.release(cur) // 现持有者进入宽限;下方 claim 取消之
			}
		}
		role, err := h.claim(clientID)
		if err != nil { // host 已 close(注册表摘除竞态):冲突呈现
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, openResult{ID: id, Role: roleName(role)})
		return
	}
	dir := sessionx.SessionDir(a.opts.SessionsRoot, id)
	if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err != nil {
		writeError(w, http.StatusNotFound, "unknown session "+id)
		return
	}
	store, err := sessionx.OpenStore(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open mirror: %v", err))
		return
	}
	need, openTurn, rerr := control.RecoveryRequired(store)
	ws := ""
	if meta, merr := store.ReadMeta(); merr == nil {
		ws, _ = meta["workspace"].(string)
	}
	_ = store.Close()
	if rerr != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("恢复检查失败: %v", rerr))
		return
	}
	if need {
		writeError(w, http.StatusConflict, fmt.Sprintf("会话 %s 回合 t%d 未收口（上次运行中断），拒绝续链", id, openTurn))
		return
	}
	release, lerr := acquireLock(dir)
	if lerr != nil {
		pid := 0
		if l, rerr := readLock(filepath.Join(dir, lockFileName)); rerr == nil {
			pid = l.PID
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": lerr.Error(), "pid": pid})
		return
	}
	fn, opt := a.hostAssembly()
	h, err := newSessionHost(r.Context(), id, ws, true, fn, opt)
	if err != nil {
		release()
		if isRecoveryRefusal(err) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open session: %v", err))
		return
	}
	h.attachLock(release)
	a.mgr.add(h)
	role, err := h.claim(clientID)
	if err != nil { // 防御:新构造 host 不会 closed;失败即收口
		_ = h.close(context.Background())
		a.mgr.remove(id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, openResult{ID: id, Role: roleName(role)})
}

// ---- 交互(交互端持有者限定) ----

// requireInteractive 校验 clientID 是当前交互端持有者(readonly → 403)。
func requireInteractive(w http.ResponseWriter, h *sessionHost, clientID string) bool {
	if isInteractive(h, clientID) {
		return true
	}
	writeError(w, http.StatusForbidden, "readonly: interactive holder required")
	return false
}

// handleSubmit POST /{id}/submit {text}:预检回合在飞(ErrTurnRunning 的同步
// 前置——running 在 Submit 入口同步置位,预检挡住常态冲突)→ 409「回合」
// (FR-006 消息契约);否则 goroutine 提交(Submit 阻塞至回合收口,绝不在
// 请求内等待)→ 202 + 回合号。预检后的并发竞态窗口丢弃留痕(Stderr)。
func (a *webAPI) handleSubmit(w http.ResponseWriter, r *http.Request) {
	clientID := clientIDOf(r)
	id := r.PathValue("id")
	h, ok := a.mgr.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown session "+id)
		return
	}
	if !requireInteractive(w, h, clientID) {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text required")
		return
	}
	snap := h.ctrl.Snapshot()
	if snap.Running {
		writeError(w, http.StatusConflict, "回合进行中：等待当前回合收口后再提交")
		return
	}
	turn := snap.Turn + 1
	text := req.Text
	go func() {
		if err := h.ctrl.Submit(context.Background(), text); err != nil {
			// 竞态窗口丢弃(预检后另一并发 submit 抢先 → ErrTurnRunning):
			// 留痕不静默;常态冲突已由预检 409 挡住。
			logf(a.opts.Stderr, "submit turn %d dropped: %v", turn, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"turn": turn})
}

// handleCancel POST /{id}/cancel:交互端限定;ctrl.Cancel(控制协议 interrupt,
// 待审一并 fail-closed 拒绝——control 语义零改动)。
func (a *webAPI) handleCancel(w http.ResponseWriter, r *http.Request) {
	clientID := clientIDOf(r)
	id := r.PathValue("id")
	h, ok := a.mgr.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown session "+id)
		return
	}
	if !requireInteractive(w, h, clientID) {
		return
	}
	if err := h.ctrl.Cancel(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancel: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleApproval POST /{id}/approval {tool_use_id,decision}:decision ∈
// allow|allow_session|deny → ctrl.Approve/ApproveSession(resolved_by=user);
// 无此待审请求 → 404(错误映射见文件头裁定)。
func (a *webAPI) handleApproval(w http.ResponseWriter, r *http.Request) {
	clientID := clientIDOf(r)
	id := r.PathValue("id")
	h, ok := a.mgr.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown session "+id)
		return
	}
	if !requireInteractive(w, h, clientID) {
		return
	}
	var req struct {
		ToolUseID string `json:"tool_use_id"`
		Decision  string `json:"decision"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ToolUseID == "" {
		writeError(w, http.StatusBadRequest, "tool_use_id required")
		return
	}
	var aerr error
	switch req.Decision {
	case "allow":
		aerr = h.ctrl.Approve(req.ToolUseID, true, approvalResolvedBy)
	case "allow_session":
		aerr = h.ctrl.ApproveSession(req.ToolUseID, approvalResolvedBy)
	case "deny":
		aerr = h.ctrl.Approve(req.ToolUseID, false, approvalResolvedBy)
	default:
		writeError(w, http.StatusBadRequest, "decision must be allow|allow_session|deny")
		return
	}
	if errors.Is(aerr, control.ErrNoPendingApproval) {
		writeError(w, http.StatusNotFound, "no pending approval for "+req.ToolUseID)
		return
	}
	if aerr != nil {
		writeError(w, http.StatusInternalServerError, aerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- 状态与回放 ----

// handleState GET /{id}/state:Snapshot 投影(锁内纯内存读)。
func (a *webAPI) handleState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h, ok := a.mgr.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown session "+id)
		return
	}
	snap := h.ctrl.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": snap.SessionID, "engine_session_id": snap.EngineSessionID,
		"model": snap.Model, "cli_version": snap.CLIVersion,
		"turn": snap.Turn, "running": snap.Running, "ready": snap.Ready,
		"dir": snap.Dir, "meter": snap.MeterReport, "turn_tokens": snap.TurnTokens,
	})
}

// handleEvents GET /{id}/events:非变异回放(ReadEventsRelaxed——好行照读,
// 损坏段降级 warnings,镜像字节不动)。应答 = NDJSON 帧:头帧 {"warnings":
// [...]},其后 wire-record 帧 {"kind","ev"}(与 SSE 帧同形)。在册会话读
// ctrl.Mirror()(泵并发追加下的半行尾由 relaxed 语义降级);未打开读磁盘
// 镜像。完整好行治理形态异常 → 断流(fail-closed,不静默缺帧)。
func (a *webAPI) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		events   []any
		warnings []string
	)
	if h, ok := a.mgr.get(id); ok {
		var err error
		events, warnings, err = h.ctrl.Mirror().ReadEventsRelaxed()
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read events: %v", err))
			return
		}
	} else {
		dir := sessionx.SessionDir(a.opts.SessionsRoot, id)
		if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err != nil {
			writeError(w, http.StatusNotFound, "unknown session "+id)
			return
		}
		store, err := sessionx.OpenStore(dir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("open mirror: %v", err))
			return
		}
		defer store.Close()
		events, warnings, err = store.ReadEventsRelaxed()
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read events: %v", err))
			return
		}
	}
	if warnings == nil {
		warnings = []string{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	if err := enc.Encode(map[string]any{"warnings": warnings}); err != nil {
		return
	}
	for _, ev := range events {
		frame, ferr := wireRecordJSON(ev)
		if ferr != nil {
			logf(a.opts.Stderr, "events %s: %v(回放中止)", id, ferr)
			return
		}
		if _, err := w.Write(append(frame, '\n')); err != nil {
			return
		}
	}
}
