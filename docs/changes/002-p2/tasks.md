# Mulix P2 生态 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 P2 范围：子代理编排（委派/回收/治理/取消）、项目级记忆服务（BM25 召回 + memory_* 工具）、外部 MCP server 配置透传、CLAUDE.md/AGENTS.md 变更提醒、用户自定义钩子、分层检查工具（repolint CI 化），达成 spec 的 SC-001..SC-006。

**Architecture:** 继承 P1「方案 C 托管边界混合」与全部红线（先持久化后发布、镜像 1:1、注册期冻结、C1–C5 缓存护栏、Gate 治理唯一入口、C5 turn-tail 注入唯一）。新增 `internal/subagent`（编排器，经 cli 注入的 SessionFactory 组装子会话，避免 subagent→control→subagent 环）、`internal/memory`（BM25+JSONL）、`internal/lint`（行级分层扫描）；tools 子包 `mulixsubagent`（HTTP 回调桥，同 mulixpermission 模式）、`mulixmemory`（serve 子进程直写工作区文件）。外部 MCP server 走配置透传（mulix 不代理协议）。里程碑：M12 子代理 → M13 记忆 → M14 外部 MCP → M15 指令 watcher → M16 自定义钩子 → M17 分层工具 → M18 验收。

**Tech Stack:** Go 1.26（module `mulix`）；P1 依赖集不变（cobra/bubbletea/BurntSushi toml/go-sdk）；**不新增第三方依赖**（BM25、sha256 轮询、lint 扫描全部标准库实现）。

**Spec:** `docs/specs/002-p2/spec.md`

**Design:** `.mulix/.runtime/002-p2/specs/2026-10-08-p2-ecosystem-design.md`（2026-10-08 用户批准；三分叉裁决：MCP 工具驱动 / 配置透传 / 项目级记忆）

## Global Constraints

- 事件红线（design 001 §6）：镜像写入是 Sink 发布的前置条件——先持久化后发布；新事件类型（DelegationStarted/Completed/Cancelled/Failed）必须进 sessionx codec（`eventFromKind` switch + 往返测试）。
- 缓存护栏（C1–C5）：外部 server 条目进 mcp-config 后，两次启动逐字节一致（护栏扩展测试钉死）；记忆召回只走回合 user message 尾部（turn tail），绝不改 AppendSystemPrompt/MCPConfigJSON/AllowedTools。
- 注册期冻结：工具面在进程启动期决定（memory_search=false 时 memory_* 不注册）；会话内零增减。
- 权限红线：子代理工具调用经与主代理相同的 Gate/Approver 回路；无人值守 fail-closed；绝不 `--dangerously-skip-permissions`。
- 分层红线（design 001 §5.3）：tools 子包不反向 import 内核包（回调桥 HTTP 解耦）；`internal/subagent` 不 import `internal/control`（经 `SessionFactory` 接口反转，cli 为组合根）；leaves 禁止 import 内核包。
- 配置：所有新配置键（`[mcp.servers]`/`[hooks.custom]`/`[tools] memory_search`/子代理限额/召回预算）进分层加载与 `TestDefaultPathsIncludesProjectLayer` 同级的默认值测试。
- 执行约定：每个任务先写测试（RED→验证 RED）再最小实现（GREEN→验证 GREEN）；证据入台账 `.mulix/.runtime/002-p2/sdd/tasks/progress.md`（格式 `Task N: complete (commit X)`）；每任务一次普通 git commit（中文消息）。
- go 命令 dot-source `.mulix/.runtime/001-p1-core-loop-and-cache-foundation/sdd/goenv.ps1`（GOMODCACHE/GOPROXY 沿用）。

## Review Focus

- **子代理取消后的孤儿回合**：取消/父会话关闭时子代理若留下 TurnStarted 无 TurnDone，P1 恢复门将永久拒绝该工作区——预期：级联取消走 resume_reconciled 同款合成收口。钉：Task 4 的 e2e（真实镜像工厂：spawn 后 mulix_cancel → 子镜像无孤儿回合且 1:1 不变量成立；父会话 Close → 级联取消）。
- **记忆文件损坏尾部**：entries.jsonl 被截断/半行写入后，召回与 save 不得整体失败——预期：隔离坏段 + 告警 + 好数据保留。钉：Task 5 损坏注入测试。
- **外部 server 名称冲突与保留名**：`[mcp.servers]` name="mulix" 或与内置名重复时必须启动失败（而非静默覆盖）——预期：配置错误 exit 2。钉：Task 7 校验矩阵。
- **钩子脚本注入面**：custom hook command 数组元素含 shell 元字符时必须按 argv 直接 exec（不经 shell 解释）——预期：元字符作为参数字面量传递。钉：Task 9 exec 测试（argv 断言）。
- **watcher 误报**：文件 mtime 变化但内容相同（touch）不得产生提醒——预期：sha256 比较而非 mtime。钉：Task 8 touch 测试。

---

## Task 1: 事件类型与 codec 扩展（M12 前置）

**Files:**
- Modify: `internal/event/event.go`（Kind 常量 + 4 个事件结构体）
- Modify: `internal/sessionx/codec.go`（eventFromKind switch 分支）
- Test: `internal/event/event_test.go`（仅追加 Kind 常量/JSON tag 断言——**package event 不得 import sessionx，会成环**）、`internal/sessionx/replay_test.go`（往返测试放这里：package sessionx 已 import event）

**Interfaces:**
- Consumes: P1 事件模式（`{"kind","ev"}` 信封；`eventKindOf`/`eventFromKind`）。
- Produces:
  - `event.DelegationStarted Kind = "delegation_started"`；`type DelegationStartedEvent struct { Seq int64 `json:"seq"`; SubSessionID string `json:"sub_session_id"`; Task string `json:"task"`; Depth int `json:"depth"` }`
  - `event.DelegationCompleted Kind = "delegation_completed"`；`type DelegationCompletedEvent struct { Seq int64 `json:"seq"`; SubSessionID string `json:"sub_session_id"`; Summary string `json:"summary"`; TotalTokens int64 `json:"total_tokens"`; CostUSDCents int64 `json:"cost_usd_cents"` }`
  - `event.DelegationCancelled Kind = "delegation_cancelled"`；`type DelegationCancelledEvent struct { Seq int64 `json:"seq"`; SubSessionID string `json:"sub_session_id"`; Reason string `json:"reason"` }`
  - `event.DelegationFailed Kind = "delegation_failed"`；`type DelegationFailedEvent struct { Seq int64 `json:"seq"`; SubSessionID string `json:"sub_session_id"`; Reason string `json:"reason"` }`

- [ ] **Step 1: 写失败测试**（**放 internal/sessionx/replay_test.go**——走镜像同一条编解码路径：Append → 重开 → ReadAllEvents → DeepEqual，P1 `TestAppendMultipleKindsRoundTrip` 同模式；event_test.go 另加仅断言 Kind 常量值与 json tag 的小测试）

```go
func TestDelegationEventsRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-deleg", Workspace: "w", EngineID: "e", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.Close()
	events := []any{
		event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub1", Task: "add tests", Depth: 1},
		event.DelegationCompletedEvent{Seq: 2, SubSessionID: "sub1", Summary: "done", TotalTokens: 100, CostUSDCents: 7},
		event.DelegationCancelledEvent{Seq: 3, SubSessionID: "sub1", Reason: "user"},
		event.DelegationFailedEvent{Seq: 4, SubSessionID: "sub1", Reason: "engine crash"},
	}
	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("append %T: %v", ev, err)
		}
	}
	r, err := OpenStore(filepath.Join(root, "s-deleg"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	got, err := r.ReadAllEvents()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, want := range events {
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("event %d mismatch: got %#v want %#v", i, got[i], want)
		}
	}
}
```

- [ ] **Step 2: 验证 RED** — `go test ./internal/sessionx/ ./internal/event/ -count=1`；Expected: FAIL（sessionx 往返测试：undefined event.DelegationStartedEvent；event 常量测试：undefined）。
- [ ] **Step 3: 最小实现**——event.go 加 4 个 Kind 常量与结构体（字段与 Interfaces 一致，全部带 json tag）；sessionx/codec.go 的 eventFromKind 加 4 个 case。
- [ ] **Step 4: 验证 GREEN** — 同命令；Expected: PASS（全部既有测试不回归）。
- [ ] **Step 5: Commit** — `git commit -m "feat(event): 委派链事件类型与 codec 扩展（M12/T12.1）"`。

## Task 2: kernel（Go 版 cordis 模式）+ subagent.Manager 状态机与限额（M12）

**Files:**
- Create: `internal/kernel/kernel.go`
- Test: `internal/kernel/kernel_test.go`
- Create: `internal/subagent/subagent.go`
- Test: `internal/subagent/subagent_test.go`

**Interfaces:**
- Consumes: Task 1 事件（`event.Delegation*Event`）；设计 002 §2.1/§3.0（用户指令：cordis 架构模式——引擎只是插件）。
- Produces（后续任务依赖的精确签名）:

```go
package kernel

// Context 是层级作用域：子作用域继承父注入（查找沿祖先链），Dispose 先子后父。
type Context struct{ /* parent, services, disposables, children, mu */ }
func NewContext(parent *Context) *Context // 根 = NewContext(nil)
func (c *Context) Provide(name string, svc any) error // 同作用域重复注册 → error
func (c *Context) Service(name string) (any, bool)    // 自身 → 祖先链
func (c *Context) OnDispose(fn func())                // LIFO 清理注册
func (c *Context) Mount(p Plugin) error               // p.Mount(c)；失败即整体回滚（已注册服务移除）
func (c *Context) Dispose()                           // 先子后父：children LIFO → 自身 disposables LIFO（幂等）
func (c *Context) Parent() *Context
type Plugin interface {
	Name() string
	Mount(c *Context) error
}
```

```go
package subagent

// SessionFactory 由 cli（组合根）注入：为子代理挂载插件集（store/meter/gate/
// engine provider——引擎只是插件之一）并返回会话句柄。subagent 包不 import
// control（防 import 环）——工厂闭包在 cli 侧实现（Task 4）。
type SessionHandle interface {
	ID() string
	Done() <-chan struct{}     // 回合收口（TurnDone 或引擎退出）时关闭
	Err() error                // 收口原因（nil = 正常完成）
	Summary() string           // 最终 assistant 文本（回合收口时可用）
	TotalTokens() int64
	CostUSDCents() int64
	Cancel(ctx context.Context) error
	Close(ctx context.Context) error
}
type SessionFactory func(ctx context.Context, task string) (SessionHandle, error)

type Options struct {
	Root          string // sessions 根（与主会话同池）
	MaxDepth      int    // 默认 3
	MaxConcurrent int    // 默认 4
	Factory       SessionFactory // 必填：子会话组装工厂（cli 组合根注入）
	Publish       func(ev any)   // 必填：父会话事件发布（control 注入；红线：先持久化后发布由父侧保证）
	Now           func() time.Time
}
func NewManager(opts Options) (*Manager, error) // Root/Factory/Publish 必填，限额<=0 取默认
func (m *Manager) Spawn(ctx context.Context, parentID, task string, depth int) (subID string, err error) // depth >= MaxDepth → 错误（含 "depth"）；running 数 >= MaxConcurrent → 错误（含 "concurrent"）
func (m *Manager) Poll(ctx context.Context, subID string) (Status, error)
func (m *Manager) Cancel(ctx context.Context, subID, reason string) error
func (m *Manager) List() []Info // Info{SubSessionID, ParentID, Task, Status, Depth}
func (m *Manager) Close(ctx context.Context) // 级联取消全部运行中子代理（先子后父）

type Status string
const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
	StatusFailed    Status = "failed"
)
```

kernel 与 Manager 的关系：Manager 每个 spawn 创建子 Context 并把返回的 SessionHandle 注册进它（`OnDispose(handle.Close)`）；Cancel = handle.Cancel + 终态记录 + 子 Context.Dispose()；Close = 逐子 Cancel（kernel Dispose 语义保证先子后父）。注意：kernel 不 import 任何 internal 包（leaf 级）；Manager 的 stub 测试不经过 kernel 也能跑（kernel 行为由 kernel_test 独立钉死）——两层解耦。

- [ ] **Step 0: kernel 的 TDD 循环（Part A）**——写 `internal/kernel/kernel_test.go`：①Provide/Service 注入与祖先回溯（子作用域读到父服务、同名子覆盖父）②重复注册报错 ③Dispose 先子后父（记录调用序断言）④Dispose 幂等 ⑤Mount 成功贡献服务、Mount 失败回滚（部分注册被撤销）。验证 RED（包不存在）→ 最小实现 → 验证 GREEN。
- [ ] **Step 1: Manager 的失败测试**（状态机 + 限额 + 判环 + 级联取消，工厂用 stub——与 kernel 解耦，不经 kernel 也能跑）:

```go
package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/event"
)

type stubHandle struct {
	id      string
	done    chan struct{}
	err     error
	summary string
	tokens  int64
	cents   int64
	mu      sync.Mutex
	cancels int
}
func (h *stubHandle) ID() string { return h.id }
func (h *stubHandle) Done() <-chan struct{} { return h.done }
func (h *stubHandle) Err() error { return h.err }
func (h *stubHandle) Summary() string { return h.summary }
func (h *stubHandle) TotalTokens() int64 { return h.tokens }
func (h *stubHandle) CostUSDCents() int64 { return h.cents }
func (h *stubHandle) Cancel(ctx context.Context) error { h.mu.Lock(); h.cancels++; h.mu.Unlock(); return nil }
func (h *stubHandle) Close(ctx context.Context) error { return nil }

func newTestManager(t *testing.T, factory SessionFactory, publish func(any)) *Manager {
	t.Helper()
	m, err := NewManager(Options{Root: t.TempDir(), Factory: factory, Publish: publish})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	return m
}

func TestSpawnDepthLimit(t *testing.T) {
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return &stubHandle{id: "subX", done: make(chan struct{})}, nil
	}, func(any) {})
	_, err := m.Spawn(context.Background(), "p1", "task", 3) // depth==MaxDepth(默认3) → 拒绝
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth limit: %v", err)
	}
}

func TestSpawnConcurrentLimit(t *testing.T) {
	n := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		n++
		return &stubHandle{id: fmt.Sprintf("s%d", n), done: make(chan struct{})}, nil
	}, func(any) {})
	for i := 0; i < 4; i++ {
		if _, err := m.Spawn(context.Background(), "p", fmt.Sprintf("t%d", i), 0); err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
	}
	if _, err := m.Spawn(context.Background(), "p", "t5", 0); err == nil || !strings.Contains(err.Error(), "concurrent") {
		t.Fatalf("concurrency limit: %v", err)
	}
}

func TestPollRecordsCompletionAndPublishes(t *testing.T) {
	var mu sync.Mutex
	var pub []any
	h := &stubHandle{id: "sub1", done: make(chan struct{}), summary: "did it", tokens: 120, cents: 9}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) { mu.Lock(); pub = append(pub, ev); mu.Unlock() })
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if st, _ := m.Poll(context.Background(), "sub1"); st != StatusRunning {
		t.Fatalf("pre status = %v", st)
	}
	close(h.done) // 模拟子回合收口
	if st, err := m.Poll(context.Background(), "sub1"); err != nil || st != StatusCompleted {
		t.Fatalf("poll: %v %v", st, err)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range pub {
		if c, ok := ev.(event.DelegationCompletedEvent); ok && c.SubSessionID == "sub1" && c.TotalTokens == 120 {
			found = true
		}
	}
	if !found {
		t.Fatalf("DelegationCompleted not published: %v", pub)
	}
}

func TestCancelClosesSubcleanly(t *testing.T) {
	var mu sync.Mutex
	var pub []any
	h := &stubHandle{id: "sub1", done: make(chan struct{})}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) { mu.Lock(); pub = append(pub, ev); mu.Unlock() })
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := m.Cancel(context.Background(), "sub1", "user"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if st, _ := m.Poll(context.Background(), "sub1"); st != StatusCancelled {
		t.Fatalf("status = %v, want cancelled", st)
	}
	h.mu.Lock()
	cancels := h.cancels
	h.mu.Unlock()
	if cancels != 1 {
		t.Fatalf("handle cancels = %d", cancels)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range pub {
		if c, ok := ev.(event.DelegationCancelledEvent); ok && c.SubSessionID == "sub1" && c.Reason == "user" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DelegationCancelled not published: %v", pub)
	}
}

func TestCloseCancelsAllRunning(t *testing.T) {
	hs := []*stubHandle{
		{id: "s1", done: make(chan struct{})},
		{id: "s2", done: make(chan struct{})},
	}
	i := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		i++
		return hs[i-1], nil
	}, func(any) {})
	for k := 0; k < 2; k++ {
		if _, err := m.Spawn(context.Background(), "p", fmt.Sprintf("t%d", k), 0); err != nil {
			t.Fatalf("spawn %d: %v", k, err)
		}
	}
	m.Close(context.Background())
	for _, h := range hs {
		h.mu.Lock()
		c := h.cancels
		h.mu.Unlock()
		if c != 1 {
			t.Fatalf("handle %s cancels = %d, want 1", h.id, c)
		}
	}
	for _, id := range []string{"s1", "s2"} {
		if st, _ := m.Poll(context.Background(), id); st != StatusCancelled {
			t.Fatalf("%s status = %v", id, st)
		}
	}
}
```

- [ ] **Step 2: 验证 RED** — `go test ./internal/kernel/ ./internal/subagent/ -count=1`；Expected: FAIL（两个包均不存在）。
- [ ] **Step 3: 最小实现**——kernel（Context/Provide/Service/OnDispose/Mount/Dispose，零 internal 依赖）+ Manager（`map[subID]*subEntry{handle, parentID, task, depth, status, childCtx}`；Spawn 判深度（沿 parentID 链查 List）与并发（running 计数）→ Factory → 子 Context 注册 OnDispose(handle.Close)；goroutine `go func(){ <-h.Done(); 记录终态; Publish(对应事件); childCtx.Dispose() }()`；Cancel = handle.Cancel + 终态记录 + 发布 + childCtx.Dispose()；Close 遍历 running）。
- [ ] **Step 4: 验证 GREEN** — Expected: PASS（kernel + subagent 两包）。
- [ ] **Step 5: Commit** — `git commit -m "feat(kernel,subagent): cordis 模式内核与编排器状态机限额（M12/T12.2）"`。

## Task 3: mulix_spawn/poll/cancel 工具 + 回调桥（M12）

**Files:**
- Create: `internal/tools/mulixsubagent/subagent.go`、`internal/tools/mulixsubagent/subagent_test.go`
- Modify: `internal/control/controller.go`（子代理回调服务器——复用 permissionCallbackServer 模式：token 路径 + MaxBytesReader + closed 拒绝）、`internal/control/controller.go`（env 注入 `MULIX_SUBAGENT_CALLBACK`，Boot Env 通道，绝不入 mcp-config）。**cmd/mulix/main.go 不加 blank import（条件注册，见 Interfaces）**
- Test: `internal/control/subagent_callback_test.go`

**Interfaces:**
- Consumes: Task 2 的 Manager；P1 permissionCallbackServer 模式（`startPermissionCallback` 同构）。
- Produces: 工具 schema——`mulix_spawn{task: string, required}`、`mulix_poll{sub_session_id: string, required}`、`mulix_cancel{sub_session_id: string, required}`；回调 body：`{op:"spawn"|"poll"|"cancel", task?, sub_session_id?, parent_id?, depth?}`；应答 JSON `{ok, sub_session_id?, status?, summary?, error?}`。**注册方式（对 P1 规则 4 的修订）**：本包**不走** blank-import init() 自注册（P1 mulixpermission 先例是无条件注册，子代理需按 SubagentFactory 是否为 nil 条件注册）——导出 `RegisterConditional(srv *registry.Server, enabled bool)`（内部 sync.Once：enabled=false 时整体跳过且不重复注册；registry.Register 对重复名 panic，故必须一次调用），由 cli 组合根在配置加载后、`registry.NewServer` 冻结前调用（cli 可 import tools 子包——组合根特权，与 cmd 同级）。

- [ ] **Step 1: 写失败测试**（control 侧：三操作经 HTTP 回调到 Manager stub；fail-closed 矩阵——无回调地址/非 POST/坏 body 一律错误应答）:

```go
package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"mulix/internal/subagent"
)

type stubManager struct {
	spawnID string
	spawned int
	cancels int
}

func (s *stubManager) Spawn(task, parentID string, depth int) (string, error) {
	s.spawned++
	return s.spawnID, nil
}
func (s *stubManager) Poll(subID string) (subagent.Status, error) { return subagent.StatusRunning, nil }
func (s *stubManager) Cancel(subID, reason string) error          { s.cancels++; return nil }

func postOp(t *testing.T, addr string, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post("http://"+addr+"/op", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestSubagentCallbackBridge(t *testing.T) {
	t.Run("spawn→poll", func(t *testing.T) {
		mgr := &stubManager{spawnID: "sub1"}
		pcs := startSubagentCallback(t, mgr) // 测试助手：起监听、返回地址（复用 P1 permission 桥模式）
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "spawn", "task": "t", "parent_id": "p", "depth": 0})
		if r["sub_session_id"] != "sub1" || r["ok"] != true {
			t.Fatalf("spawn resp = %v", r)
		}
		pr := postOp(t, pcs.addr, map[string]any{"op": "poll", "sub_session_id": "sub1"})
		if pr["status"] != "running" {
			t.Fatalf("poll resp = %v", pr)
		}
	})
	t.Run("bad op denied", func(t *testing.T) {
		pcs := startSubagentCallback(t, &stubManager{})
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "evil"})
		if r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "op") {
			t.Fatalf("bad op resp = %v", r)
		}
	})
	t.Run("closed session denied", func(t *testing.T) {
		pcs := startSubagentCallback(t, &stubManager{})
		pcs.setClosed() // 模拟控制器已关闭
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "spawn", "task": "t", "parent_id": "p", "depth": 0})
		if r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "closed") {
			t.Fatalf("closed resp = %v", r)
		}
	})
}
```

- [ ] **Step 2: 验证 RED** — `go test ./internal/control/ -count=1 -run TestSubagentCallback`；Expected: FAIL（startSubagentCallback 未定义）。
- [ ] **Step 3: 最小实现**——control：`startSubagentCallback`（窄接口 `interface{ Spawn(task, parentID string, depth int) (string, error); Poll(subID string) (subagent.Status, error); Cancel(subID, reason string) error }` 或直接 `*subagent.Manager`）+ mux 注册 token 路径（127.0.0.1 + 16 字节 hex token，MaxBytesReader 1MiB，closed 检查——permissionCallbackServer 同构）；control Options 增 `SubagentFactory subagent.SessionFactory`（nil = 子代理禁用）与 `SubagentLimits`（MaxDepth/MaxConcurrent，零值取默认）；start() 中 Factory 非空时组装 Manager（`subagent.Options{Root: sessionsRoot, Factory, Publish: store.Append 后经 sink 发布（先持久化后发布红线）}`）并注入 env `boot.Env = append(boot.Env, "MULIX_SUBAGENT_CALLBACK="+addr)`（仅当 hasMulixRegistryEntry()——Boot Env 通道，绝不入 mcp-config）；tools/mulixsubagent：三工具 Execute → 读 env → POST → 透传 JSON（fail-closed deny 矩阵同 mulixpermission：env 缺失/超时/非 200/坏 JSON → ok=false 错误应答）+ `RegisterConditional(srv, enabled)`。
- [ ] **Step 4: 验证 GREEN** — `go test ./internal/control/ ./internal/tools/mulixsubagent/ -count=1`；Expected: PASS。
- [ ] **Step 5: Commit** — `git commit -m "feat(subagent): spawn/poll/cancel 工具与前台回调桥（M12/T12.3）"`。

## Task 4: cli 组装接线 + /agents 面板（M12 收口）

**Files:**
- Modify: `internal/cli/session.go`（SessionFactory 闭包：调 buildComponents 同序组装子会话——store.Create(parentID 衍生 id) + meter + policy/gate（继承父 Approver）+ controller；子 Controller 的 sink = 捕获式 no-op sink，事件落子镜像）
- Modify: `internal/cli/tui*.go`（`/agents` 面板：List() 渲染 + c 取消）
- Modify: `internal/config/config.go`（`[subagent] max_depth/max_concurrent`，默认 3/4）
- Test: `internal/cli/subagent_wiring_test.go`

**Interfaces:**
- Consumes: Task 2/3 全部签名。
- Produces: control Options 的 `SubagentFactory subagent.SessionFactory` 由 cli 在 buildComponents 组装时以闭包注入；`func (c *Controller) Subagent() *subagent.Manager`（窄 getter，TUI 面板消费；未启用时返回 nil）；SessionFactory 闭包内部调 `assembleSession`——从 buildComponents 抽取的共用组装函数（签名 `assembleSession(hostFactory, root, workspace, cfg, approvals, attended bool, task string) (SessionHandle-like, error)`，避免组装序复制；**组装 = kernel 挂载**：子 Context + 依次 Mount store/meter/gate/engine 插件（engine provider 由 hostFactory 决定——claude 或 FakeHost 都只是插件，设计 002 §2.1）；子会话 store 的 ID = 父 id 派生如 `<parent>-sub-<n>`，meta 记 parent_id）。

- [ ] **Step 1: 写失败测试**（wiring + config 默认值）:

```go
func TestSubagentWiringEndToEnd(t *testing.T) {
	// FakeHost 主引擎发 mulix_spawn 工具调用 → 控制请求 → 回调桥 →
	// SubagentFactory 造**真实镜像**子会话（sessionx.Create 到 sessionsRoot）→
	// mulix_poll 返回 completed + summary。
	// 断言：①主镜像含 DelegationStarted 与 DelegationCompleted（usage 摘要非零）
	// ②子镜像文件存在于 sessionsRoot 且 meta.parent_id == 主会话 id
	// ③【Review Focus 孤儿回合钉】mulix_cancel 后：子镜像无孤儿回合
	//  （TurnStarted ↔ TurnDone/EngineExited 成对）且 1:1 不变量成立
	//  （复用 P1 e2e 的镜像对账断言）；父会话 Close → 级联取消同断言。
}

func TestSubagentConfigDefaults(t *testing.T) {
	cfg := config.Default() // 或分层加载空配置
	if cfg.Subagent.MaxDepth != 3 || cfg.Subagent.MaxConcurrent != 4 {
		t.Fatalf("defaults: %+v", cfg.Subagent)
	}
}

func TestSubagentDisabledWhenFactoryNil(t *testing.T) {
	// Options.SubagentFactory == nil：cli 不调 RegisterConditional(enabled=true)
	// → 注册表无 mulix_spawn 条目，Boot Env 无 MULIX_SUBAGENT_CALLBACK，
	// boot 面与 P1 逐字节一致。
}
```

（实现时按 P1 cli 既有 e2e/stub 模式补齐三个测试的完整断言体——TestSubagentWiringEndToEnd 复用 P1 FakeHost 工具调用注入路径。）
- [ ] **Step 2: 验证 RED** — `go test ./internal/cli/ -count=1`；Expected: FAIL。
- [ ] **Step 3: 最小实现**——cli：`assembleSession` 抽取（buildComponents 改为复用它）+ Factory 闭包（子 Approvals 继承父 bridge）+ control Options.SubagentFactory 传递 + **cli 在注册面冻结前调 `mulixsubagent.RegisterConditional(srv, factory != nil)`** + config `[subagent] max_depth/max_concurrent` 解析（默认 3/4）；TUI：`/agents` 面板（bubbletea 列表视图：子会话 id/状态/任务/深度；`c` 键调 mgr.Cancel；ctrl.Subagent()==nil 时命令提示未启用）。
- [ ] **Step 4: 验证 GREEN** — 全 cli 包 PASS；`go test ./... -count=1` 不回归。
- [ ] **Step 5: Commit** — `git commit -m "feat(cli): 子代理组装接线与 /agents 面板（M12/T12.4）"`。

## Task 5: memory 存储与 BM25（M13）

**Files:**
- Create: `internal/memory/memory.go`（存储+tombstone+损坏隔离）、`internal/memory/bm25.go`
- Test: `internal/memory/memory_test.go`、`internal/memory/bm25_test.go`

**Interfaces:**
- Produces:

```go
func Open(dir string) (*Store, error)                 // dir = <workspace>/.mulix/memory
func (s *Store) Add(text, sourceSession string) (id string, err error)
func (s *Store) Delete(id string) error               // 追加 tombstone
func (s *Store) List() ([]Entry, error)               // Entry{ID, Text, SourceSession, CreatedAt}
func (s *Store) Search(query string, k int, budgetChars int) []Entry // BM25 top-k，预算内按分截断
func (s *Store) IsolateReport() []string              // 损坏段报告（测试/告警用）
```

- [ ] **Step 1: 写失败测试**——Add/List 往返；Delete 后 List 不含且文件重放仍不含；**损坏注入**（半行截断 + 中间垃圾行 → Open 成功、IsolateReport 非空、好条目保留、Add 继续可用）；BM25：中文 bigram 命中（「记住本项目用 pnpm」query「pnpm」命中）、英文词命中、预算截断（budget=10 chars → 只留前 10 字符内条目或丢弃并按分排序）。
- [ ] **Step 2: 验证 RED** — `go test ./internal/memory/ -count=1`；Expected: FAIL（包不存在）。
- [ ] **Step 3: 最小实现**——JSONL 追加（复用 P1 jsonl 包的写入口径）；Open 全量重放（tombstone 收敛 + 坏行隔离进 `.damaged` 同款语义——P1 sessionx 有先例，照搬模式）；BM25：k1=1.5, b=0.75，标准实现。
- [ ] **Step 4: 验证 GREEN** — Expected: PASS。
- [ ] **Step 5: Commit** — `git commit -m "feat(memory): JSONL 存储与 BM25 检索（M13/T13.1）"`。

## Task 6: memory_* 工具 + control 注入 + 开关（M13 收口）

**Files:**
- Create: `internal/tools/mulixmemory/memory.go`、`internal/tools/mulixmemory/memory_test.go`
- Modify: `internal/control/controller.go`（Submit 路径：memory_search=true 时检索 + turn-tail 追加——注入函数唯一化，供 repolint 钉）、`internal/config/config.go`（`[tools] memory_search` 默认 false + `[memory] recall_top_k=3 / recall_budget_chars=2000`）。**注册方式**：与 mulixsubagent 同理不走 blank-import init()——导出 `RegisterConditional(srv *registry.Server, workspace string, enabled bool)`（sync.Once；enabled=false 跳过且不重复注册），cli 组合根在配置加载后、注册面冻结前调用
- Test: `internal/control/memory_injection_test.go`

**Interfaces:**
- Consumes: Task 5 Store；P1 registry.Tool。
- Produces: 工具 `memory_save{text}`/`memory_list{}`/`memory_delete{id}`（serve 子进程直写工作区 `.mulix/memory/`——cwd 语义）；control 注入文本格式：`[记忆召回]\n- <entry text>\n`（追加在 user message 尾部）。

- [ ] **Step 1: 写失败测试**——①开关关：Submit 后发出的 user message 无注入且 memory 工具不在注册表（`registry.Registered()` 断言）②开关开：query 命中 → user message 尾部含 `[记忆召回]` 块，且 **AppendSystemPrompt/MCPConfigJSON 与关闭时逐字节一致**（护栏）③预算截断 ④工具 save/list/delete 直写文件与 store 重放一致。
- [ ] **Step 2: 验证 RED** — `go test ./internal/control/ ./internal/tools/mulixmemory/ -count=1`；Expected: FAIL。
- [ ] **Step 3: 最小实现**——control 注入函数 `withMemoryRecall(ctx, text) string`（唯一调用点，供 Task 10 lint 规则扫描）；tools 经 workspace 参数打开 Store（Open 失败 → 工具错误应答，不崩溃）+ **cli 在注册面冻结前调 `mulixmemory.RegisterConditional(srv, workspace, cfg.Tools.MemorySearch)`**。
- [ ] **Step 4: 验证 GREEN** — 全量 `go test ./... -count=1` PASS（护栏不回归是本任务的验收核心）。
- [ ] **Step 5: Commit** — `git commit -m "feat(memory): memory_* 工具与 turn-tail 召回注入（M13/T13.2）"`。

## Task 7: 外部 MCP server 配置透传（M14）

**Files:**
- Modify: `internal/config/config.go`（`[mcp.servers]` 数组：name/command/env/timeout_ms；保留名校验）、`internal/config/config_test.go`
- Modify: `internal/cache/boot.go`（Compose 合并内置+外部条目；顺序：内置先、外部按声明序；env 不做运行期注入）
- Test: `internal/cache/boot_test.go`（追加）、`internal/config/config_test.go`（追加）

**Interfaces:**
- Produces: `config.MCPServerDecl{Name, Command []string, Env map[string]string, TimeoutMS int}`；Compose 输出条目 `{"type":"stdio","command":[...],"env":{...}}`；冲突/非法 → Compose error。

- [ ] **Step 1: 写失败测试**——①两个外部 server + 内置 mulix → mcp-config 含 3 条目、**名称排序输出**（P1 composeMCPConfig 经 map 序列化已是 name-sorted——沿用该确定性排序，不实现自定义顺序序列化；逐字节稳定断言两次 Compose 一致）②name="mulix" 冲突 → error ③command 空 → error ④timeout_ms：多个 server 不同 timeout_ms → 取最小值透传为引擎 env `MCP_TOOL_TIMEOUT`（Boot Env 通道，与回调地址同一面；单 server 用其声明值）⑤外部条目 env 静态透传。
- [ ] **Step 2: 验证 RED** — `go test ./internal/cache/ ./internal/config/ -count=1`；Expected: FAIL。
- [ ] **Step 3: 最小实现**——Compose 合并 + 校验 + env 计算（timeout 默认 60000，多 server 取 min）；config 解析 + 默认值测试。R1 对齐：FR-108 的 mulix 侧验收 = env 透传正确 + 默认值，不测 mulix 侧行为（引擎原生超时）。
- [ ] **Step 4: 验证 GREEN** — Expected: PASS（P1 字节稳定测试不回归）。
- [ ] **Step 5: Commit** — `git commit -m "feat(cache,config): 外部 MCP server 配置透传与校验（M14）"`。

## Task 8: 指令文件快照加载 + watcher + mulix instructions（M15）

**Files:**
- Modify: `internal/control/controller.go`（**新增启动期快照加载**：start 序列读 workspace 的 CLAUDE.md/AGENTS.md（存在才读）→ `map[filename]{content, sha256}` 存入控制器；P1 并未实现该读取——本任务补齐 FR-109 的前半）。随后启动 watcher goroutine：2s ticker 轮询，sha256 比较；变更 → `NoticeEvent{level=info, content="指令文件变更: <file>（快照保持，下会话生效）"}`；Close 停止 ticker
- Create: `internal/cli/instructions.go`（`mulix instructions`：输出来源文件与内容摘要——读控制器快照）
- Test: `internal/control/watcher_test.go`

**Interfaces:**
- Consumes: 无 P1 依赖（快照加载是本任务新增；design 001 C3 的「启动快照」在 P1 落地为 AppendSystemPrompt 模板，未读指令文件——本任务补全）。
- Produces: `loadInstructionSnapshots(workspace string) map[string]instructionSnap`（`instructionSnap{Content string; SHA256 string}`，文件不存在则键缺席）；`watchInstructions(ctx, files map[string]instructionSnap, publish func(event.NoticeEvent), interval time.Duration)`（导出便于测试）。

- [ ] **Step 1: 写失败测试**——①快照加载：tempdir 放 CLAUDE.md → map 含内容与 sha256；空目录 → 空 map ②内容变化 → 恰好一条 Notice（同文件多次变化在两次轮询间只报一次）③touch（mtime 变内容同）→ 无 Notice（sha256 而非 mtime）④删除文件 → 提醒事件（同修改语义）⑤Close 后 goroutine 退出（channel 等待退出信号，无泄漏）。
- [ ] **Step 2: 验证 RED** — `go test ./internal/control/ -count=1 -run 'TestWatch|TestLoadInstruction'`；Expected: FAIL。
- [ ] **Step 3: 最小实现**——快照加载（os.ReadFile + sha256）+ ticker 轮询（interval 可注入，测试 10ms）+ start 序列接线 + `mulix instructions` 子命令输出。
- [ ] **Step 4: 验证 GREEN** — Expected: PASS。
- [ ] **Step 5: Commit** — `git commit -m "feat(control): 指令文件变更轮询提醒与 mulix instructions（M15）"`。

## Task 9: 用户自定义钩子（M16）

**Files:**
- Modify: `internal/config/config.go`（`[[hooks.custom.rules]] event/command/timeout_ms`，启动校验：event ∈ P1 四时点、command 非空、timeout ≤30000）
- Modify: `internal/guard/hooks.go`（自定义钩子消费者：追加事件分发目标）
- Test: `internal/guard/hooks_custom_test.go`、`internal/config/config_test.go`（追加）

**Interfaces:**
- Consumes: P1 guard.Hooks 四时点分发与 hook_response 语义。
- Produces: `NewScriptConsumer(rules []config.HookRule) (HookConsumer, error)`——`Handle(ctx, payload guard.HookPayload) error`（guard 既有扩展点是 `HookFunc(ctx, HookPayload)`，control 在各时点分发）：exec argv（不经 shell）、stdin=payload 的完整事件 JSON、超时/崩溃按既定 fail-open/fail-closed 语义 + 告警。

- [ ] **Step 1: 写失败测试**——①合法声明构建成功；非法（event=Foo/command 空/timeout>30000）→ 启动拒绝矩阵 ②exec argv 字面量（command=["python","-c","print(1); print(args)"] 元字符不解释——用 echo 风格 stub 脚本断言 argv 原样）③脚本正常输出 JSON → 按语义生效 ④脚本超时 → 降级 + 告警事件 ⑤脚本崩溃（exit 3）→ 同上 ⑥P1 内置消费者仍收到事件（一条事件多消费者）。
- [ ] **Step 2: 验证 RED** — `go test ./internal/guard/ ./internal/config/ -count=1`；Expected: FAIL。
- [ ] **Step 3: 最小实现**——exec.CommandContext（argv 直传）+ json.Decoder(stdin) + context 超时。
- [ ] **Step 4: 验证 GREEN** — Expected: PASS。
- [ ] **Step 5: Commit** — `git commit -m "feat(guard,config): 用户自定义钩子——校验、argv 执行与降级矩阵（M16）"`。

## Task 10: 分层检查工具 mulix lint（M17）

**Files:**
- Create: `internal/lint/lint.go`（规则引擎）、`internal/lint/rules.go`（分层规则——**含 design 002 §1 新增面**：subagent 可 import engine/sessionx/cache/guard/memory 但不得 import control；tools/mulixmemory 可 import memory；tools/mulixsubagent 不得 import 任何 internal 包；cli 与 cmd 为组合根可 import tools 子包）、`internal/lint/lint_test.go`
- Create: `internal/cli/lint.go`（`mulix lint layers` 子命令：exit 0/1 + 违规报告——cli/lint.go 不存在，新建）
- Modify: `.github/workflows/ci.yml`（lint job 步骤 `go run ./cmd/mulix lint layers`）
- Create: `docs/lint/ratchet.json`（首轮基线生成）

**Interfaces:**
- Consumes: design 001 §5.3 分层表（内置于 rules.go）。
- Produces: `lint.RunLayers(repoRoot string, ratchetPath string) ([]Violation, error)`——`Violation{File, Line, Rule, Detail}`；ratchet：`map[file]int` 允许违规计数，新增 → 违规；退出码 1 = 有违规。

- [ ] **Step 1: 写失败测试**——①分层导入矩阵：fixture 树中 leaf→kernel import → Violation(Rule="layer-import") ②tools 子包 import 内核包 → Violation ③棘轮：计数增 → Violation(Rule="ratchet")；减/平 → 通过 ④turn-tail：`AppendSystemPrompt` 引用超出 allowlist → Violation（P1 TestNoSecondSystemPromptPath 规则并入）⑤干净树 → 零违规 exit 0。
- [ ] **Step 2: 验证 RED** — `go test ./internal/lint/ -count=1`；Expected: FAIL。
- [ ] **Step 3: 最小实现**——walk `.go` 文件 + import 块行扫描（regex 先例规模化）；ratchet JSON 读写；`-fix` 基线生成模式（首轮人工生成基线入库）。
- [ ] **Step 4: 验证 GREEN** + 自食其力：对 mulix 自身仓库跑 `go run ./cmd/mulix lint layers`；Expected: PASS（零违规或基线内）。
- [ ] **Step 5: Commit** — `git commit -m "feat(lint,cli): 分层检查工具与 CI 接线（M17）"`。

## Task 11: P1 复审遗留修复（M18 前置）

**Files:**
- Modify: `internal/engine/claudehost.go`（N2：startErr 读取经 mutex 或 signalStart 前局部返回）
- Modify: `internal/control/controller.go`（N4：并发重复投递时 resolved 只发布一次 approval_resolved——rulingFor 单飞等待方跳过重复发布）
- Test: `internal/control/singleflight_test.go`（N5：并发同 tool_use_id 回调 → 恰好 1 次 approval_request + 1 次 approval_resolved + 等待方拿到同裁决；closed 后回调 → deny）

**Interfaces:**
- Consumes: P1 rulingFor 单飞、permissionCallbackServer。
- Produces: 无新接口——纯修复。

- [ ] **Step 1: 写失败测试**（singleflight_test.go：20 goroutine 并发同 ID POST → 镜像 approval_request 计数==1、approval_resolved 计数==1；closed 会话 POST → deny JSON）。
- [ ] **Step 2: 验证 RED** — Expected: FAIL（N4：计数 >1 或竞态）。
- [ ] **Step 3: 修复**——rulingFor 返回 (ruling, leader bool)；follower 跳过 resolved 发布；startErr 经 `sync.Once` 写入后快照读取。
- [ ] **Step 4: 验证 GREEN** — 全量 PASS（-race 若 CI 可用；本地无 cgo 如实记录）。
- [ ] **Step 5: Commit** — `git commit -m "fix(control,engine): P1 复审 N2/N4/N5——单飞发布唯一化与 startErr 同步（M18）"`。

## Task 12: P2 验收清单与真实引擎冒烟（M18 收口）

**Files:**
- Create: `docs/evidence/p2-acceptance.md`、`docs/evidence/p2-smoke.md`
- Modify: `.mulix/.runtime/002-p2/sdd/tasks/progress.md`（台账收口）

**Interfaces:**
- Consumes: SC-001..006 全部测试与冒烟产物。
- Produces: 验收文档（每条 SC 映射真实测试名——**逐一 grep 核验后才可写入**，P1 终评教训）。

- [ ] **Step 1: SC 映射草稿**——SC-001 子代理真实委派冒烟（真实仓库 spawn 多步任务：读改测提交 + poll 回收 + /agents 面板截图性记录）、SC-002 记忆开关 A/B（护栏测试 + 开启冒烟）、SC-003 lint CI 步骤（本地等价命令 + 违规注入复现）、SC-004 外部 server 调用/超时/崩溃（FakeHost 矩阵 + 真实 echo server 冒烟）、SC-005 watcher 触碰测试、SC-006 镜像 1:1（子代理崩溃恢复 e2e）。
- [ ] **Step 2: 真实引擎冒烟执行**（同 P1 冒烟 A/B 方法：独立 git 工作区 + mulix run 真实任务 + mulix-real.exe rebuilt），记录 commit 哈希与输出摘录。
- [ ] **Step 3: 验收文档写入**（诚实限制段落：子代理真实 TTY 面板交互、外部 server 冒烟的引擎 env 依赖等）。
- [ ] **Step 4: 全量门禁** — `go test ./... -count=1` / `go vet ./...` / `gofmt -l internal cmd` / `go run ./cmd/mulix lint layers` / 三平台交叉编译。
- [ ] **Step 5: Commit** — `git commit -m "docs: P2 验收清单与冒烟证据（M18）"`。
