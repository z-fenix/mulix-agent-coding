# mulix Web UI MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 为 mulix 提供浏览器交互界面：本地 HTTP/SSE 服务 + 内嵌 SPA，功能对齐 TUI 核心闭环（会话、实时 transcript、提交/中断、审批 fail-closed），治理语义零改动。

**Architecture:** 新前端层 `internal/serve`（layerMatrix 已预留槽）作为会话权威宿主：每会话一个 `control.Controller` + fan-out `webSink`，浏览器为纯视图（HTTP JSON + SSE，React 内嵌）。自行组装（不 import internal/cli），复用 sessionx/control/guard/cache/kernel/engine 全部底层。详见设计文档。

**Tech Stack:** Go（零新增 Go 依赖）+ React 18/TypeScript/Vite（npm，依赖清单见 Global Constraints）。

**Spec:** `docs/specs/2026-10-18-web-ui-mvp/spec.md`

**Design:** `.mulix/.runtime/2026-10-18-web-ui-mvp/specs/2026-10-18-web-ui-mvp-design.md`

## Global Constraints

- 每任务必须 `go test ./... -count=1` 全绿再 commit；`gofmt -l .` 干净是提交前置；conventional 前缀（feat/test/chore/docs）；只 add 本任务文件；`.mulix/.runtime/**` 不入提交。
- 红线：事件先写镜像再出 Sink（1:1）；镜像单写者；fail-closed（非法输入/无人值守 ask → 拒绝收口，不静默）；Sink 实现不得重入 Submit/Resume/Approve（internal/control/controller.go:48-52）。
- 治理/镜像/回合收口语义零改动：不改 `ReadAllEvents`/`Replay`/`UserApprovalBridge`/Gate 行为；120s 审批窗口保留（2026-10-18 用户裁定）。
- 本变更零新增 Go 第三方依赖；npm 依赖已批准 12 包（react, react-dom, vite, @vitejs/plugin-react, typescript, marked, highlight.js, @fontsource/ibm-plex-sans, @fontsource/ibm-plex-mono, vitest, @testing-library/react, 以及 Task 11 为 XSS 防护新增 dompurify——plan reviewer 注意：此包为安全必需，需随计划一并获批）。
- `internal/serve` 是唯一 import `internal/*` 的 web 层（layerMatrix 登记）；`web/` 为 npm 项目不入 Go 矩阵；`internal/serve/dist` 是唯一 embed 点。
- 服务默认绑定 127.0.0.1:3180；mutating 端点必须过同源防护（SameSite=Strict Cookie + Origin 校验）。
- SSE 背压：订阅者缓冲满 → 断开该客户端，绝不阻塞 pump。
- 断线宽限 10s；跨进程锁心跳 10s tick、新鲜阈值 30s。
- -race 因 CGO_ENABLED=0 不可用，测试不加 -race，如实记录。
- 阶段门：本计划写 `docs/specs/.../spec.md` 的步骤若被 PreToolUse hook 拒绝，按步骤内降级路径处理（记 Ruling 顺延），不得 --force。

## Review Focus

1. 慢 SSE 客户端阻塞泵（背压失效会把镜像单写者卡死）→ 期望：发布恒不阻塞，满缓冲客户端被断开。钉住测试：Task 3 Step 1。
2. 交互端瞬时断连 → 不可逆审批拒绝（EventSource 自动重连场景）→ 期望：10s 宽限内重连不触发拒绝。钉住测试：Task 5 Step 1（TestGraceReconnectCancelsGone）。
3. 损坏段回放改写镜像字节（把只读回放做成 `Replay()` 的变异语义）→ 期望：`ReadEventsRelaxed` 后 `events.jsonl` 字节不变。钉住测试：Task 2 Step 1。
4. 跨域页面驱动 mutating API（drive-by CSRF 打 127.0.0.1:3180）→ 期望：无 Cookie/异源 Origin 的 POST 一律 403。钉住测试：Task 7 Step 1。
5. 模型/工具输出经 markdown 注入脚本（XSS）→ 期望：任何事件文本渲染后 DOM 中不存在可执行脚本。钉住测试：Task 11 Step 1。

---

## Task 1: spec 回填 + config `[web]` 节

**Files:**
- Modify: `docs/specs/2026-10-18-web-ui-mvp/spec.md`（Clarifications/FR-009/US2 场景 3/FR 新两条/Assumptions——内容见设计文档 §11）
- Modify: `internal/config/config.go`（Config 结构 ~57-69、defaultConfig ~149-159、校验 ~243-245 惯例处）
- Test: `internal/config/web_test.go`

**Interfaces:**
- Consumes: 无（首任务）。
- Produces: `type WebConfig struct { Port int; Bind string; WorkspaceRoots []string; IdleCloseMinutes int }`；`Config.Web WebConfig \`toml:"web"\``。默认值：Port 3180、Bind "127.0.0.1"、WorkspaceRoots 空（serve 时取 cwd）、IdleCloseMinutes 10。TOML 键：`port`/`bind`/`workspace_roots`/`idle_close_minutes`。非法值 fail-closed：port ≤0 或 >65535、bind 为空串显式配置、idle_close_minutes ≤0 → Load 返回错误（仿 `[eventbus]` 门 config.go:243-245）。

- [X] **Step 1: 回填 spec 裁定**——按设计文档 §11 三条修订 spec.md。若 hook 拒绝写 docs/specs：不 --force，把修订全文记入 `.mulix/.runtime/2026-10-18-web-ui-mvp/sdd/ledger`（Ruling：来源=设计裁定 2026-10-18，原文见设计 §11），顺延到 verify 阶段回填，本步记为完成（降级路径已执行）。

- [X] **Step 2: Write the failing test**

```go
// internal/config/web_test.go
func TestWebConfigDefaults(t *testing.T) {
	// 空 toml → Web 零值经 Load 规整后：Port 3180, Bind "127.0.0.1", IdleCloseMinutes 10, WorkspaceRoots 空
}
func TestWebConfigParse(t *testing.T) {
	// [web] port=4000 bind="127.0.0.1" workspace_roots=["a","b"] idle_close_minutes=5 → 逐字段断言
}
func TestWebConfigValidationFailClosed(t *testing.T) {
	// port=0 / port=70000 / idle_close_minutes=-1 → Load 返回 error 且消息含 "web"
}
```

- [X] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestWebConfig -v`
Expected: FAIL（Web 字段/校验不存在）

- [X] **Step 4: Implement** `WebConfig` 解析+默认值+校验（minimal：仿 `[eventbus]` 三态门与 defaultConfig 惯例）。

- [X] **Step 5: Run full suite**

Run: `go test ./... -count=1 && gofmt -l .`
Expected: 全绿，gofmt 无输出

- [X] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/web_test.go docs/specs/2026-10-18-web-ui-mvp/spec.md
git commit -m "feat(config): [web] 配置节——web UI 服务参数（fail-closed 校验）"
```

## Task 2: sessionx 非变异回放读

**Files:**
- Modify: `internal/sessionx/sessionx.go`（新增函数，不动 ReadAllEvents/Replay）
- Test: `internal/sessionx/relaxed_test.go`

**Interfaces:**
- Consumes: `eventFromKind`（sessionx/codec.go:55）、`jsonl` 包超限判定（sessionx 既有 import）。
- Produces: `func (s *Store) ReadEventsRelaxed() (events []any, warnings []string, err error)`——好行照读（decode 成 event），半行/超限行 → warnings 追加（含行号），**不改写镜像任何字节**；文件不可读 → err。顺序 = 行序。

- [X] **Step 1: Write the failing test**

```go
// internal/sessionx/relaxed_test.go
func TestReadEventsRelaxedClean(t *testing.T) {
	// Create + Append TurnStarted/AssistantText → 读回 2 事件、0 警告
}
func TestReadEventsRelaxedHalfLine(t *testing.T) {
	// 追加半行（截断 JSON）→ 1 警告（含 "line"）+ 好事件全部返回；断言 events.jsonl 字节与写后一致（os.Stat size + 逐字节对比）
}
func TestReadEventsRelaxedOversized(t *testing.T) {
	// 超限行（绕过 Store.Append 直写 >16MiB 原始行）→ 1 警告 + 其余事件照回；镜像字节不变
}
func TestReadEventsRelaxedMissing(t *testing.T) {
	// 空 Store 无文件 → err
}
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessionx/ -run TestReadEventsRelaxed -v`
Expected: FAIL（方法不存在，编译错误即 RED 形态，Ruling 记编译期 RED）

- [X] **Step 3: Implement** `ReadEventsRelaxed`：以 `jsonl.Reader.ReadAllWithStatus()` 为底（Damage.HalfTail / Oversized[].LineNo 即警告来源，internal/jsonl/jsonl.go:229；超限阈值 MaxLineBytes=16MiB 不可注入），好行 `eventFromKind` 解码；无任何写操作。

- [X] **Step 4: Run full suite**

Run: `go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/sessionx/sessionx.go internal/sessionx/relaxed_test.go
git commit -m "feat(sessionx): ReadEventsRelaxed——非变异回放读，损坏段降级为警告"
```

## Task 3: internal/serve 骨架 + webSink + layerMatrix

**Files:**
- Create: `internal/serve/serve.go`、`internal/serve/sink.go`、`internal/serve/dist.go`（embed）、`internal/serve/dist/index.html`（占位）
- Modify: `Makefile`（`web-build` 目标先留桩，注记 make 为 CI-only、本机直接跑底层命令）、`.gitignore`
- Test: `internal/serve/sink_test.go`、`internal/serve/serve_test.go`

**Interfaces:**
- Consumes: Task 1 `config.Config`。
- Produces:
```go
type Options struct {
	Config *config.Config; Workspace string; SessionsRoot string
	Bind string; Port int; NoOpen bool; AllowEngineDrift bool
	Stdout, Stderr io.Writer
}
func Run(ctx context.Context, opts Options) error // http.Server + mux（本任务仅 /api/healthz + embed 静态）；ctx 取消优雅关停
type webSink struct; func newWebSink() *webSink
func (s *webSink) Publish(ev any)                 // control.Sink 实现；非阻塞 fan-out，满缓冲订阅者被断开（close 其 channel）
func (s *webSink) Subscribe() (<-chan any, func()) // 返回事件流与退订；顺序 = Publish 序
```

- [X] **Step 1: Write the failing test**

```go
// internal/serve/sink_test.go
func TestWebSinkFanOutOrder(t *testing.T)      { /* 两订阅者按 Publish 序收到同序事件 */ }
func TestWebSinkSlowSubscriberDropped(t *testing.T) {
	// 订阅者不取事件 → Publish 超过缓冲（1024）不阻塞、该订阅者 channel 被关闭、其余订阅者不受影响
}
func TestWebSinkUnsubscribe(t *testing.T)      { /* 退订后不再收到 */ }
```

```go
// internal/serve/serve_test.go
func TestHealthz(t *testing.T)   { /* httptest：GET /api/healthz → 200 {"ok":true} */ }
func TestEmbedDistServed(t *testing.T) { /* GET / → 占位 index.html 200 */ }
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -v`
Expected: FAIL（包不存在，编译期 RED，Ruling 记形态）

- [X] **Step 3: Implement** sink（mutex + map；Publish 非阻塞 select-send，满则 close+delete）与 serve（http.Server、ReadHeaderTimeout、ctx 优雅 Shutdown、embed `//go:embed all:dist`）。`.gitignore` 顺序敏感（全局 `dist/` 规则已排除本目录）：先 `!internal/serve/dist/`（目录解除忽略）、再 `internal/serve/dist/*`、再 `!internal/serve/dist/index.html`（占位白名单）；同批加 `web/node_modules/`。`internal/lint/rules.go:29` 的 `"internal/serve": {"internal/*"}` 槽已预注册——**验证存在即可**，不产生 diff，不提交该文件（若缺失才补，并 Ruling 说明）。

- [X] **Step 4: Run layers lint + full suite**

Run: `go vet ./... && go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/serve/ Makefile .gitignore
git commit -m "feat(serve): web 服务骨架——webSink fan-out/背压丢弃 + embed 占位"
```

## Task 4: 审批通路——UserApprovalBridge 复用 + interactiveGone 取消器

**Files:**
- Create: `internal/serve/approval.go`
- Test: `internal/serve/approval_test.go`

**Interfaces:**
- Consumes: `control.NewUserApprovalBridge()`（guard.Approver 已实现；plan 评审 C1 裁定：**不自制 Approver**——`control.Options.Approvals` 是具体类型 `*UserApprovalBridge`，必须双槽注入（`Options.Approvals` 与 `GateOptions.Approver` 同一实例，先例 internal/cli/session.go:177-184）；gone 拒绝经既有 `bridge.CancelAll(resolvedBy)`（internal/control/approval.go:107-125）达成。
- Produces:
```go
func watchInteractiveGone(gone <-chan struct{}, bridge *control.UserApprovalBridge) (stop func())
// gone close → bridge.CancelAll("interactive_offline")（deny 全部 pending，留痕 ApprovalResolvedEvent 由既有路径落镜像）；幂等、stop 后退出
// 审批决定的 HTTP 通路 = controller.Approve/ApproveSession（读 Options.Approvals 生效）；SetAlias 由 controller.evaluateRuling 既有调用（controller.go:1807-1809）
```

- [X] **Step 1: Write the failing test**

```go
// internal/serve/approval_test.go
func TestGoneCancelsPending(t *testing.T)   { // 发起 ApproveRequest（goroutine）→ close gone → 该请求返回 deny 且 resolvedBy 含 "interactive_offline" }
func TestGoneNoPendingHarmless(t *testing.T) { // 无 pending 时 close gone 不 panic、后续请求正常 }
func TestStopExitsWatcher(t *testing.T)     { // stop 后 close gone → 无 goroutine 泄漏（不触发 CancelAll） }
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run 'TestGone|TestStop' -v`
Expected: FAIL（编译期 RED，Ruling 记形态）

- [X] **Step 3: Implement** watcher（select gone/stop；单次 CancelAll）。

- [X] **Step 4: Run full suite**

Run: `go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/serve/approval.go internal/serve/approval_test.go
git commit -m "feat(serve): 审批通路——UserApprovalBridge 复用 + gone 即拒取消器（fail-closed）"
```

## Task 5: sessionHost 状态机 + 跨进程锁

**Files:**
- Create: `internal/serve/host.go`（状态机）、`internal/serve/lock.go`
- Test: `internal/serve/host_test.go`、`internal/serve/lock_test.go`

**Interfaces:**
- Consumes: Task 3 webSink、Task 4 `watchInteractiveGone`；sessionx.Create/OpenStore/control.Controller（既有）。
- Produces:
```go
type sessionRole int; const (roleReadonly sessionRole = iota; roleInteractive)
type sessionSummary struct { ID, Workspace, Status string; LastActivity time.Time; Open bool }
type sessionHost struct { ID string; Workspace string; ctrl *control.Controller; sink *webSink; bridge *control.UserApprovalBridge; ... }
type assembleFn func(ctx context.Context, h *sessionHost) (*control.Controller, *control.UserApprovalBridge, error) // Task 6 注入真实装配；本任务测试用 FakeHost 最小桩
func newSessionHost(ctx context.Context, id, workspace string, resume bool, assemble assembleFn) (*sessionHost, error)
// 内部：gone := make(chan struct{})；宽限计时逻辑写该 chan；启动 watchInteractiveGone(gone, bridge)（close(ctx) 时 stop）
func (h *sessionHost) claim(clientID string) (sessionRole, error)   // 空闲→interactive；被他人持有→readonly,nil
func (h *sessionHost) release(clientID string)                      // 持有者断开调用；宽限（可注入，默认 10s）内再 claim → 取消 gone 计时
func (h *sessionHost) interactiveGone() <-chan struct{}             // 持有者断开且宽限未归 → close
func acquireSessionLock(sessionDir string) (release func(), err error) // <dir>/web.lock {pid,heartbeat_unix}；心跳 goroutine 10s tick（可注入）；新鲜(<30s)→err 呈持有者 pid；过期/缺失→接管
func (h *sessionHost) close(ctx context.Context) error              // watcher stop → ctrl.Close → 锁释放
type hostManager struct; func newHostManager(idle time.Duration) *hostManager // idle=WebConfig.IdleCloseMinutes（消费 Task 1 产物）
func (m *hostManager) get(id string) (*sessionHost, bool)
func (m *hostManager) list() []sessionSummary                        // 打开会话报真实状态；未打开的由 Task 7 与 ScanSessions 启发式合并
```

- [X] **Step 1: Write the failing test**

```go
// internal/serve/lock_test.go
func TestLockAcquireAndRefuse(t *testing.T)  { // acquire 成功 → 第二次 acquire err 含 pid；release 后可再 acquire }
func TestLockStaleTakeover(t *testing.T)     { // 手写过期心跳锁文件 → acquire 成功 }
func TestLockHeartbeatRefresh(t *testing.T)  { // acquire 后心跳 tick（注入缩短）→ 文件心跳已更新 }

// internal/serve/host_test.go
func TestClaimInteractiveThenReadonly(t *testing.T) { /* c1 claim→interactive；c2 claim→readonly */ }
func TestReleaseThenReclaim(t *testing.T)           { /* c1 release → c2 claim→interactive */ }
func TestInteractiveGoneAfterGrace(t *testing.T)    { /* c1 release → 宽限（注入 10ms）内 gone 未关；超时后关闭 */ }
func TestGraceReconnectCancelsGone(t *testing.T)    { /* c1 release 后宽限内再 claim → gone 永不关闭（计时器取消）*/ }
func TestIdleCloseClosesHost(t *testing.T)          { /* 会话空闲超 idle（注入缩短）且无连接 → host.close 被触发 */ }
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run 'TestLock|TestClaim|TestRelease|TestInteractive|TestGrace' -v`
Expected: FAIL（编译期 RED，Ruling 记形态）

- [X] **Step 3: Implement** 状态机（claim/release/gone+宽限计时器可注入）与锁（tmp+rename 原子写、心跳 tick 可注入）。newSessionHost 以 assemble 闭包组装，本任务桩内用 `engine.NewFakeHost` 最小装配驱动 NewSession/Resume 成功即可（FakeHost 是 internal/engine 导出类型）。

- [X] **Step 4: Run full suite**

Run: `go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/serve/host.go internal/serve/lock.go internal/serve/host_test.go internal/serve/lock_test.go
git commit -m "feat(serve): sessionHost 交互令牌状态机（10s 宽限）+ 跨进程会话锁（fail-closed）"
```

## Task 6: serve 侧 controller 装配（buildComponents 对齐）

**Files:**
- Create: `internal/serve/assemble.go`
- Test: `internal/serve/assemble_test.go`

**Interfaces:**
- Consumes: Task 5 assembleFn 槽、Task 4 `watchInteractiveGone`；既有包：sessionx、guard、cache、control、kernel、agentloop、llm/claude、engine、config、eventbus（门）。语义对照 `internal/cli/session.go:132-356`（132-302 buildComponents/assembleController + 304-356 assembleKernelContext——kernel provide 列表在 304-356；不 import cli，按其行为独立实现）。
- Produces:
```go
func assembleReal(ctx context.Context, h *sessionHost) (*control.Controller, *control.UserApprovalBridge, error)
// 装配清单（对齐 cli 语义）：
//   bridge := control.NewUserApprovalBridge()（attended；双槽：guard.GateOptions.Approver = bridge 且 control.Options.Approvals = bridge——先例 cli/session.go:177-184）
//   guard.NewPolicy(Preset+config deny/allow) → guard.Gate(GateOptions{Policy, Approver: bridge})
//   meter := cache.NewMeter(...resume offset via cache.NextTurnNumber)；memory/hook/subagent 按 config 缺省装配
//   kernel: provide sink/store/meter/gate/config/approvals/memory/profile + mount agentloop.NewPlugin() + llm=claude adapter（并回填 o.Source 与 o.Kernel——cli/session.go:350-354 惯例）
//   eventbus 门：cfg.Eventbus.Enabled() → sharedBus per workspace（endpoint 文件语义对齐 cli/session.go:781-804）
//   hostFactory := func(engine.BootConfig) (engine.Host, error) { return engine.NewClaudeHost(engine.ClaudeHostOptions{LockPath: resolveEngineLockPath(ws)}), nil }（HostFactory 签名见 controller.go:61；形态对齐 productionHostFactory cli/session.go:383-389）
//   control.NewController(Options{..., AllowEngineDrift: opts, Sink: h.sink, ...})
//   resume 通路：先 serve 侧 checkRecovery 门（open turn → 拒绝），过门后 ctrl.Resume(engineID)
func resolveEngineLockPath(workspace string) string // 对齐 cli/session.go:391-416 链：workspace 根 → exe 目录 → "engine.lock"
```

- [X] **Step 1: Write the failing test**

```go
// internal/serve/assemble_test.go（FakeHost × 真实栈，对齐 internal/e2e 惯例）
func TestAssembleNewSessionHappyTurn(t *testing.T) {
	// assembleReal（HostFactory 换 engine.FakeHost+FixtureScript）→ ctrl.Submit → 回合收口；
	// 断言：镜像先于 sink（记录 sink 收到时 store 已含该事件，仿 e2e assertSinkMirrorsStore）；
	// Usage/TurnDone 事件在 sink 流中按镜像序出现
}
func TestAssembleResumeRecoveryGate(t *testing.T) { /* 造 open-turn 镜像 → 走 serve resume 通路（checkRecovery 门）被拒，而非裸 ctrl.Resume（后者本就合成 resume_reconciled 不拒）*/ }
func TestAssembleEngineDriftRefused(t *testing.T) { /* HostFactory 返回漂移错误 → 错误上抛供 UI 呈现（FR-013）*/ }
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run TestAssemble -v`
Expected: FAIL（编译期 RED，Ruling 记形态）

- [X] **Step 3: Implement** assembleReal（逐条对齐 cli 装配语义；engine.lock 解析链独立实现，注释互指 cli/session.go:391。NewController fail-closed 要求 Kernel 非 nil 且含 agentloop 服务键——对照 internal/e2e/e2e_test.go:48-54 kernelCtxWithLoop 模式）。

- [X] **Step 4: Run full suite**

Run: `go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/serve/assemble.go internal/serve/assemble_test.go
git commit -m "feat(serve): controller 装配——guard/cache/kernel/agentloop 全栈对齐 cli 语义"
```

## Task 7: HTTP API + 同源防护 + SSE

**Files:**
- Create: `internal/serve/http.go`（路由+中间件）、`internal/serve/sse.go`
- Modify: `internal/serve/serve.go`（mux 接入 hostManager）
- Test: `internal/serve/http_test.go`、`internal/serve/sse_test.go`

**Interfaces:**
- Consumes: Task 5 hostManager/sessionHost、Task 6 assembleReal、Task 4 webApprovalBridge、Task 2 ReadEventsRelaxed、`control.ErrTurnRunning`。
- Produces（路由表 = 设计 §4）：
```go
func newMux(mgr *hostManager, opts Options) http.Handler
// GET  /api/workspaces | /api/sessions | /api/sessions/{id}/events | /{id}/stream(SSE) | /{id}/state
// POST /api/sessions {workspace} | /api/sessions/{id}/open {takeover?} | /{id}/submit {text} | /{id}/cancel | /{id}/approval {tool_use_id,decision}
// 中间件：GET / 下发 mulix_session Cookie（SameSite=Strict，随机令牌）；mutating 校验 Cookie+Origin（缺失/异源 → 403）
// 错误映射：ErrTurnRunning→409；readonly→403；未知 id→404；open turn resume 拒→409；跨进程锁拒绝→409（呈持有者 pid）
// SSE 帧格式 = 镜像 wire record 同形 {"kind":"...","ev":{...}}（kind 用 sessionx.EventKindOf）；events 端点回放同形 + {"warnings":[...]} 头帧
// GET /api/sessions：本进程打开的报 hostManager 真实状态；未打开的与 sessionx.ScanSessions 合并 + 启发式（events.jsonl mtime + 末事件 kind；设计 §6 已知边界）
```

- [X] **Step 1: Write the failing test**

```go
// internal/serve/http_test.go
func TestSessionListAndCreate(t *testing.T)      { /* GET list（FakeHost 装配桩）→ JSON 字段 id/workspace/status；POST create → 201 + id */ }
func TestSubmitConflictWhileRunning(t *testing.T) { /* 占住回合 → 第二次 submit 409，消息含 "回合"（FR-006）*/ }
func TestReadonlyForbidden(t *testing.T)         { /* c2 只读 → submit/approval 403；takeover open → c2 interactive */ }
func TestOriginAndCookieEnforced(t *testing.T)   { /* 无 Cookie POST → 403；Cookie+异源 Origin → 403；同源 → 通过 */ }
func TestApprovalRoundTrip(t *testing.T)         { /* FakeHost canUseTool fixture → SSE 收 approval_request → POST allow → SSE 收 approval_resolved(allow,user) 且镜像含二事件（1:1）*/ }
func TestEventsReplayWithWarnings(t *testing.T)  { /* 半行镜像 → events 端点返回好事件 + warnings 非空（FR-003）*/ }
func TestStateSnapshot(t *testing.T)             { /* GET state → Snapshot 投影 JSON */ }

// internal/serve/sse_test.go
func TestSSEFirstEventAndOrder(t *testing.T)     { /* 订阅 → 触发事件 → 帧 = Publish 序；Fiber 断开不 panic */ }
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run 'TestSession|TestSubmit|TestReadonly|TestOrigin|TestApproval|TestEvents|TestState|TestSSE' -v`
Expected: FAIL（编译期 RED，Ruling 记形态）

- [X] **Step 3: Implement** 路由（标准库 net/http + Go 1.22 method pattern）、中间件、SSE（http.Flusher、20s 心跳注释行、订阅者满缓冲断开复用 webSink 语义）。

- [X] **Step 4: Run full suite**

Run: `go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/serve/http.go internal/serve/sse.go internal/serve/serve.go internal/serve/http_test.go internal/serve/sse_test.go
git commit -m "feat(serve): HTTP/SSE API 全表 + 同源防护 + 错误映射（409/403/404）"
```

## Task 8: `mulix web` CLI 接线

**Files:**
- Create: `internal/cli/web.go`
- Modify: `internal/cli/root.go`（命令表 ~140-149 注册 `web`）
- Test: `internal/cli/web_test.go`

**Interfaces:**
- Consumes: Task 3 serve.Run/Options；Task 1 config。
- Produces: `mulix web [--port N] [--bind ADDR] [--no-open] [--allow-engine-drift]`——默认端口取 config `[web].port`；启动后打印 URL 并（默认）用系统浏览器打开；`Options.RunWeb func(ctx, Options, port int, noOpen bool) error` 注入点（测试替身），production 实现调 serve.Run。

- [X] **Step 1: Write the failing test**

```go
// internal/cli/web_test.go（Options.RunWeb 注入捕获测试；helper-process 模式本任务不需要——serve 无子进程面）
func TestWebCommandWiring(t *testing.T) { /* RunWeb 替身捕获：端口默认 3180 透传、--port 覆盖、--no-open 透传、Stdout 打印 URL */ }
func TestWebCommandHelpRegistered(t *testing.T) { /* root --help 含 "web" */ }
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestWeb -v`
Expected: FAIL（编译期 RED，Ruling 记形态）

- [X] **Step 3: Implement** web.go（cobra 命令 + production RunWeb：serve.Run + 可选浏览器打开，Windows 用 `rundll32 url.dll,FileProtocolHandler`，跨平台探测）。

- [X] **Step 4: Run full suite**

Run: `go build ./... && go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add internal/cli/web.go internal/cli/web_test.go internal/cli/root.go
git commit -m "feat(cli): mulix web 子命令——serve 接线与浏览器打开"
```

## Task 9: 前端脚手架 + 构建管线

**Files:**
- Create: `web/package.json`、`web/vite.config.ts`、`web/tsconfig.json`、`web/src/main.tsx`、`web/src/App.tsx`、`web/index.html`、`web/src/theme.css`
- Modify: `Makefile`（web-build 实装）、`.gitignore`（internal/serve/dist 产物忽略+占位白名单）
- Test: `web/src/app.test.tsx`（vitest）

**Interfaces:**
- Consumes: Task 3 embed dist。
- Produces: `make web-build` = `npm --prefix web ci && npm --prefix web run build` → 产物输出 `internal/serve/dist`（vite `build.outDir`）；`npm --prefix web run test`（vitest run）。App 空壳渲染「mulix」标题与 healthz 状态点。

- [X] **Step 1: Write the failing test** `web/src/app.test.tsx`：渲染 App 断言标题文本存在（testing-library）。

- [X] **Step 2: Run test to verify it fails** Run: `npm --prefix web run test` Expected: FAIL（App 未实现/依赖未装）

- [X] **Step 3: Implement** 脚手架：package.json 锁定 Global Constraints 的 12 包（含 dompurify）；vite outDir 指向 `../internal/serve/dist`（emptyOutDir true）；theme.css 落设计 §8.1 令牌（--accent 等 CSS 变量，deepseek dark 缺省 + 5 预设 × dark/light `[data-theme]` 切换）；Makefile web-build（CI-only，本机直接跑 Step 4 命令——Makefile 头注惯例）；占位 index.html 被真实构建替换、`.gitignore` 保占位可重建。

- [X] **Step 4: Verify build + embed**

Run: `npm --prefix web ci && npm --prefix web run build && go build ./... && go test ./internal/serve/ -run TestEmbedDistServed -v`
Expected: 构建产物落 dist；go build 通过；embed 测试 PASS

- [X] **Step 5: 恢复占位并提交**

```bash
git checkout -- internal/serve/dist/index.html   # 仓库内只留占位（fresh clone embed 可编译），构建产物走 .gitignore
git add web/ Makefile .gitignore internal/serve/dist
git commit -m "feat(web): 前端脚手架——React+Vite+TS、主题令牌、embed 构建管线"
```

## Task 10: API client + SSE 对账 + 布局壳

**Files:**
- Create: `web/src/api.ts`、`web/src/types.ts`、`web/src/stream.ts`、`web/src/components/{Header,Sidebar,Composer,TranscriptPane}.tsx`
- Test: `web/src/stream.test.ts`

**Interfaces:**
- Consumes: Task 7 路由表与错误码；Task 9 令牌。
- Produces:
```ts
// types.ts：与 internal/event JSON 一一对应的 TS union（Kind 十五种 + warnings 帧）
// api.ts：fetchJson 封装（credentials:'same-origin'；409/403/404 → 类型化错误）
// stream.ts：openSessionStream(id, handlers) —— 先 GET events 对账（warnings 头帧→回调）再 EventSource；
//           (kind,seq) 幂等去重；onerror → 指数退避重连（重连后重新对账）；close() 语义
// components：Header（会话标题/状态/主题切换）、Sidebar（会话列表+新建+工作区选择）、Composer（受控输入+发送/中断）、TranscriptPane（空态「选择或新建会话」）
```

- [X] **Step 1: Write the failing test**

```ts
// web/src/stream.test.ts
it('dedups (kind,seq)', ...)        // 同帧重放两次 → handler 只收一次
it('replays then subscribes live', ...) // mock fetch+EventSource：对账 3 事件后 live 1 事件，序 = 镜像序
it('reconnects with backoff and re-reconciles', ...) // error 后重连，去重避免重复渲染
it('surfaces warnings frame', ...)  // warnings 头帧 → onWarnings 回调
```

- [X] **Step 2: Run test to verify it fails** Run: `npm --prefix web run test` Expected: FAIL

- [X] **Step 3: Implement** 上列四文件 + 组件最小态。

- [X] **Step 4: Run full suite**

Run: `npm --prefix web run test && npm --prefix web run build && go test ./... -count=1`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): API client + SSE 对账去重重连 + 布局壳组件"
```

## Task 11: transcript 渲染（markdown/工具卡/diff）

**Files:**
- Create: `web/src/transcript/{reducer.ts,MarkdownText.tsx,ToolCard.tsx,DiffBlock.tsx,ThinkingBlock.tsx,SystemLine.tsx,DelegationCard.tsx}`
- Test: `web/src/transcript/reducer.test.ts`、`web/src/transcript/render.test.tsx`

**Interfaces:**
- Consumes: Task 10 types/stream。
- Produces:
```ts
// reducer.ts：events → TranscriptEntry[]（纯函数，可测）
//   assistant_text 连续段合并；tool_use_requested+tool_result 按 tool_use_id 配对成卡片态（含耗时）；
//   approval_request/resolved → 独立条目；thinking → 折叠块；notice → 按 level 弱化行；usage → 回执行；delegation_* → 卡片
// MarkdownText：marked 渲染 + highlight.js（按需注册常用语言）+ DOMPurify.sanitize（XSS 防线，Review Focus 5）
// DiffBlock：unified diff 解析（自研 ~百行）→ 行号 + 加/删行底色（--diff-added-bg/-marker 等令牌）
```

- [X] **Step 1: Write the failing test**

```ts
// reducer.test.ts：配对（use→result 耗时）、text 连续合并、孤立 use（无 result，引擎中断态）、approval 两态
// render.test.tsx：
it('sanitizes script injection in markdown', ...)   // <script>alert(1)</script> 渲染后 DOM 无 script 节点（Review Focus 5）
it('renders fenced code with highlight classes', ...)
it('renders unified diff rows with add/del classes', ...)
it('renders tool card collapsed summary and expandable detail', ...)
```

- [X] **Step 2: Run test to verify it fails** Run: `npm --prefix web run test` Expected: FAIL

- [X] **Step 3: Implement** 上列组件（对齐 TUI 呈现语义：TurnDone 正常完成不渲染行、EngineExited 扫尾失败卡——reducer 内处理）。

- [X] **Step 4: Run full suite**

Run: `npm --prefix web run test && npm --prefix web run build && go test ./... -count=1`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add web/src/transcript
git commit -m "feat(web): transcript 渲染——markdown 高亮/工具卡/diff/thinking/系统行（XSS 消毒）"
```

## Task 12: 交互面（提交/中断/审批/接管/会话管理）

**Files:**
- Create: `web/src/components/{ApprovalBar,SessionListView,WorkspacePicker,StatusBar}.tsx`、`web/src/hooks/{useTurnState.ts,useApproval.ts}`
- Modify: `web/src/components/{Composer,TranscriptPane,Sidebar}.tsx`
- Test: `web/src/interaction.test.tsx`

**Interfaces:**
- Consumes: Task 10 api/stream、Task 11 reducer。
- Produces:
```ts
// Composer：回合中禁用提交、显示 [中断]；409 → 行内提示「回合未结束」（FR-006）
// ApprovalBar：独占焦点拦截（composer 冻结），工具名+参数摘要+倒计时（event.timeout 秒），三动作 allow/allow_session/deny（键盘可达 y/a/n + 按钮）；决定后清除
// 只读态：403 → 只读横幅 + 「接管」按钮（POST /open takeover）
// SessionListView/WorkspacePicker：列表（状态/最近活动）、新建会话、工作区选择（未选→提交禁用 FR-011）
// StatusBar：Snapshot 轮询（500ms，对齐 TUI statusTick）；漂移/not-ready 错误呈现（FR-013）；损坏段警告横幅
```

- [X] **Step 1: Write the failing test**

```tsx
// web/src/interaction.test.tsx
it('submits prompt and clears input', ...)            // mock api
it('shows 409 as inline turn-running notice', ...)
it('approval bar countdown and three decisions', ...) // timeout=120 → 倒计时存在；点 allow → api 收 allow；y/a/n 键位
it('readonly banner with takeover', ...)              // 403 → 横幅；接管 → api 调 open
it('workspace gate: no workspace disables composer', ...)
it('drift error surfaces in status bar', ...)
```

- [X] **Step 2: Run test to verify it fails** Run: `npm --prefix web run test` Expected: FAIL

- [X] **Step 3: Implement** 上列组件与 hooks。

- [X] **Step 4: Run full suite**

Run: `npm --prefix web run test && npm --prefix web run build && go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): 交互面——审批拦截条/接管/会话管理/状态呈现"
```

## Task 13: e2e 全栈集成 + 真实冒烟

**Files:**
- Create: `internal/serve/e2e_test.go`、`docs/evidence/2026-10-18-web-ui-mvp.md`
- Modify: `docs/changes/2026-10-18-web-ui-mvp/report.md`（verify 阶段产物，本任务只占位）

**Interfaces:**
- Consumes: Task 3-8 全部（httptest 起 serve.Run + FakeHost fixture，对齐 internal/e2e/e2e_test.go 惯例）。
- Produces: 全栈回归证据。

- [X] **Step 1: Write the failing test**

```go
// internal/serve/e2e_test.go（FakeHost × 真实 serve 栈：httptest 起 newMux+hostManager+assembleReal 换 FakeHost）
func TestE2EHappyTurnViaHTTP(t *testing.T) {
	// create session → submit → SSE 收齐 turn_started/assistant_text/tool_use/tool_result/usage/turn_done
	// events 回放与 SSE 序一致且与 store 1:1（对账断言）
}
func TestE2EApprovalFlowViaHTTP(t *testing.T) {
	// fixture canUseTool → SSE approval_request → POST allow → 工具继续 → approval_resolved 落镜像
}
func TestE2EInteractiveGoneDenyViaHTTP(t *testing.T) {
	// 审批挂起 → 客户端断开 SSE → 宽限（注入缩短）未归 → approval_resolved(deny,interactive_offline) 落镜像
}
func TestE2ECrossProcessLockViaHTTP(t *testing.T) {
	// 会话被外部进程锁（手写新鲜锁）→ POST open 409 呈持有者 pid
}
func TestE2ECancelViaHTTP(t *testing.T) {
	// submit → cancel → turn_done(取消态) 落镜像且 SSE 收到（FR-007 闭环）
}
```

- [X] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run TestE2E -v`
Expected: FAIL（缺集成装配）

- [X] **Step 3: Implement** 测试装配 helper（复用 Task 5/7 已有件；缺什么补什么，不改既有签名）。

- [X] **Step 4: Run full suite**

Run: `go vet ./... && go test ./... -count=1 && gofmt -l .`
Expected: 全绿

- [X] **Step 5: 真实冒烟（FR-108 惯例）**

Run: `go build -o <仓外构建目录>/mulix-cli.exe ./cmd/mulix-cli && <该目录>/mulix-cli web --no-open`
真实 claude 引擎跑一轮（提交 prompt → 观察 transcript/审批 → 回合收口），证据（引擎版本/工作区/时间线/退出码/摘录）落 `docs/evidence/2026-10-18-web-ui-mvp.md`。环境受阻（无引擎/网络）→ 如实记录受阻事实与替代验证，不以自动化测试冒充。

- [X] **Step 6: Commit**

```bash
git add internal/serve/e2e_test.go docs/evidence/2026-10-18-web-ui-mvp.md
git commit -m "test(serve): e2e 全栈回归（HTTP×FakeHost×治理）+ 真实冒烟证据"
```
