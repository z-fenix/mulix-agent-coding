# P4b-2 agent loop 抽取与镜像总线桥 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agent loop 抽取为可替换插件(`ctx.agentLoop`)+ Bind 机制落地 + 镜像→总线桥——行为零变更,四红线不动,P4 收官。

**Architecture:**(architectural,设计文档已批准:`.mulix/.runtime/012-p4b-2-agent-loop/specs/2026-10-12-p4b2-design.md`)①`internal/agentloop`:`Loop` 接口(Run(ctx, EventSource, Callbacks))+ Callbacks(10 方法=事件类数,含 OnEngineClosed 整体承接泵尾部);`Default` 驱动=泵逻辑逐字节搬家。②事件源:`EventSource{Events(); Host()}` 最小接口(llm.Provider 满足);claude 适配器 `Bind(host)`(重复 Bind 报错)落地。③镜像→总线桥:publish 尾部单一注入点 Emit(subject=`mulix.mirror.<kind>`),先镜像后总线,失败 fail-visible。

**Tech Stack:** Go 标准库;kernel 小接口(EventPublisher/EventSubscriber——P4a);既有测试基建。

**Spec:** `docs/specs/012-p4b-2-agent-loop/spec.md`

**Design:** `.mulix/.runtime/012-p4b-2-agent-loop/specs/2026-10-12-p4b2-design.md`

## Global Constraints

- FR-106 四红线不动:先持久化后发布(桥在镜像后)、单写者、1:1、fail-closed。
- FR-101a:loop 事件源=llm.Provider;Bind 机制落地(claude 适配器)。
- FR-103:agentloop 包不 import control(矩阵登记 `internal/agentloop: {engine, event, kernel}`);依赖以小接口注入。
- 行为零变更:每任务 `go test ./... -count=1` 全绿再 commit;事件序列一致由既有 e2e 断言承载。

## Review Focus

- OnEngineClosed 整体承接:Critical-1 顺序(关闭性发布 → running=false → close(ch))在 controller 回调实现内保持——顺序错=回退终评 Critical-1。
- dispatch 语义逐字节:10 类路由 + default 跳过 + 内联 Notice 两类(Trace/PermissionDenied)。
- Bind 前后语义:未绑定 ErrNotBound/nil(既有测试不变);Bind 后委托;重复 Bind 报错。
- 桥顺序:镜像写 → Sink → 总线 Emit(publish 内三段顺序);Emit 失败 Notice 且不阻断。
- 既有矩阵零回归:e2e/单飞/watcher/收口/Boot 护栏(每任务全量门禁)。

---

## Task 1: claude 适配器 Bind 机制

**Files:**
- Modify: `internal/llm/claude/adapter.go`
- Test: `internal/llm/claude/adapter_test.go`(追加)

**Interfaces:**
- Consumes: 011 的 seam-only 形态(ErrNotBound/nil 流)。
- Produces: `(*ClaudeAdapter) Bind(host engine.Host) error`(首次绑定成功;重复 Bind → 错误含 "already bound");绑定后回合方法逐方法委托 host;`Host() engine.Host`(绑定后返回宿主;未绑定 nil)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/llm/claude/adapter_test.go`:

```go
// TestBindLifecycle 钉 012 FR-101a:Bind 前 ErrNotBound/nil;Bind 后委托;
// 重复 Bind 报错(already bound)。
func TestBindLifecycle(t *testing.T) {
	a := NewClaudeAdapter()
	fake := engine.NewFakeHost(engine.FakeScript{})
	// Bind 前:seam-only 语义(既有断言面)。
	if err := a.Start(context.Background(), engine.BootConfig{}); err != ErrNotBound {
		t.Fatalf("pre-bind start = %v, want ErrNotBound", err)
	}
	// Bind。
	if err := a.Bind(fake); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if a.Host() == nil {
		t.Fatal("bound host must be exposed via Host()")
	}
	if err := a.Start(context.Background(), engine.BootConfig{}); err != nil {
		t.Fatalf("post-bind start must delegate: %v", err)
	}
	// 重复 Bind 报错。
	if err := a.Bind(engine.NewFakeHost(engine.FakeScript{})); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("double bind err = %v, want already bound", err)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/llm/claude/ -run TestBindLifecycle -count=1 -v`
Expected: FAIL(Bind/Host 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

adapter.go 改造(struct 增字段;方法改委托形态):

```go
type ClaudeAdapter struct {
	mu   sync.Mutex
	host engine.Host // Bind 后就位(会话生命周期一次)
}

// Bind 绑定宿主实例(会话生命周期一次;重复 Bind 报错——start 路径唯一调用点)。
// 绑定后 seam-only 的 ErrNotBound 形态退役:回合方法逐方法委托宿主(012 FR-101a)。
func (a *ClaudeAdapter) Bind(host engine.Host) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.host != nil {
		return fmt.Errorf("llm/claude: adapter already bound")
	}
	a.host = host
	return nil
}

// Host 返回绑定的宿主(未绑定 nil——EventSource.Host() 透传面)。
func (a *ClaudeAdapter) Host() engine.Host {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.host
}
```

回合方法改委托形态(以 Start 为例,其余 Send/Events/RespondControl/Interrupt/Close/Info 同型):

```go
func (a *ClaudeAdapter) Start(ctx context.Context, cfg engine.BootConfig) error {
	if h := a.Host(); h != nil {
		return h.Start(ctx, cfg)
	}
	return ErrNotBound // 未绑定:fail-closed(既有语义保持)
}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/llm/... -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿(既有 seam-only 测试语义保持:未绑定 ErrNotBound)。

- [ ] **Step 5: Commit**

```bash
git add internal/llm/claude/
git commit -m "feat(llm/claude): Bind 机制——宿主绑定与委托(012 FR-101a;P4b-2 T1)"
```

---

## Task 2: agentloop 包——Loop 接口 + Callbacks + Default 驱动

**Files:**
- Create: `internal/agentloop/agentloop.go`
- Modify: `internal/lint/rules.go`(矩阵登记)
- Test: `internal/agentloop/agentloop_test.go`

**Interfaces:**
- Consumes: engine(Event/Host)。
- Produces: `type EventSource interface { Events() <-chan engine.Event; Host() engine.Host }` + `type Callbacks interface`(10 方法)+ `type Loop interface { Run(ctx context.Context, src EventSource, cb Callbacks) error }` + `type Default struct{}`。

- [ ] **Step 1: 写失败测试**

Create `internal/agentloop/agentloop_test.go`:

```go
package agentloop

import (
	"context"
	"testing"

	"mulix/internal/engine"
)

// fakeSource/记录回调——泵语义逐字节对齐的判定面。
type fakeSource struct {
	events chan engine.Event
	host   engine.Host
}

func (s *fakeSource) Events() <-chan engine.Event { return s.events }
func (s *fakeSource) Host() engine.Host           { return s.host }

type recordCallbacks struct {
	route    []string
	hostSeen []engine.Host
	closed   bool
}

func (r *recordCallbacks) OnInit(host engine.Host, ev engine.Event) {
	r.route = append(r.route, "init")
	r.hostSeen = append(r.hostSeen, host)
}
func (r *recordCallbacks) OnAssistant(ev engine.Event)    { r.route = append(r.route, "assistant") }
func (r *recordCallbacks) OnToolResult(ev engine.Event)   { r.route = append(r.route, "toolresult") }
func (r *recordCallbacks) OnResult(ev engine.Event)       { r.route = append(r.route, "result") }
func (r *recordCallbacks) OnTrace(ev engine.Event)        { r.route = append(r.route, "trace") }
func (r *recordCallbacks) OnPermissionDenied(ev engine.Event) {
	r.route = append(r.route, "permission_denied")
}
func (r *recordCallbacks) OnExit(ev engine.Event) { r.route = append(r.route, "exit") }
func (r *recordCallbacks) OnControlRequest(host engine.Host, ev engine.Event) {
	r.route = append(r.route, "control_request")
	r.hostSeen = append(r.hostSeen, host)
}
func (r *recordCallbacks) OnHookResponse(ev engine.Event) {
	r.route = append(r.route, "hook_response")
}
func (r *recordCallbacks) OnEngineClosed() { r.route = append(r.route, "engine_closed"); r.closed = true }

// TestDefaultRunRoutesByKindAndDrains 钉 012 FR-101:按 Kind 路由到回调
// (逐字节对齐现状 dispatch);流关闭 → OnEngineClosed;Run 返回 nil。
func TestDefaultRunRoutesByKindAndDrains(t *testing.T) {
	src := &fakeSource{events: make(chan engine.Event, 8), host: engine.NewFakeHost(engine.FakeScript{})}
	cb := &recordCallbacks{}
	// 事件序列:init/assistant/toolresult/result/trace/permission_denied/
	// exit/control_request/hook_response/unknown(跳过不入回调——design §1.2 裁定)。
	src.events <- engine.Event{Kind: engine.KindInit}
	src.events <- engine.Event{Kind: engine.KindAssistant}
	src.events <- engine.Event{Kind: engine.KindToolResult}
	src.events <- engine.Event{Kind: engine.KindResult}
	src.events <- engine.Event{Kind: engine.KindTrace}
	src.events <- engine.Event{Kind: engine.KindPermissionDenied}
	src.events <- engine.Event{Kind: engine.KindExit}
	src.events <- engine.Event{Kind: engine.KindControlRequest}
	src.events <- engine.Event{Kind: engine.KindHookResponse}
	src.events <- engine.Event{Kind: engine.EventKind("unknown_kind")}
	close(src.events)

	err := Default{}.Run(context.Background(), src, cb)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"init", "assistant", "toolresult", "result", "trace",
		"permission_denied", "exit", "control_request", "hook_response"}
	if len(cb.route) != len(want) {
		t.Fatalf("route = %v, want %v (unknown 跳过不入回调)", cb.route, want)
	}
	for i := range want {
		if cb.route[i] != want[i] {
			t.Fatalf("route[%d] = %q, want %q", i, cb.route[i], want[i])
		}
	}
	if !cb.closed {
		t.Fatal("OnEngineClosed must fire after drain")
	}
	if len(cb.hostSeen) != 2 { // init + control_request 需要 host 透传
		t.Fatalf("host passthrough count = %d, want 2", len(cb.hostSeen))
	}
}
```

(注:`engine.EventKind("unknown_kind")` 造未知 kind——现状 dispatch 的 default 分支=跳过不入回调。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/agentloop/ -count=1`
Expected: FAIL(包未存在——编译期 RED,新包 TDD 形态,台账记 Ruling)。

- [ ] **Step 3: 实现**

`internal/agentloop/agentloop.go`:

```go
// Package agentloop 提供回合循环抽象(P4b-2,dsh ctx.agentLoop 对齐):
// loop 可替换;默认驱动=controller 泵逻辑逐字节搬家(解码后事件按 Kind
// 路由到治理回调集;流关闭 → OnEngineClosed)。FR-101。
// 依赖约束:不 import control——治理动作经 Callbacks 小接口注入(方法数
// =事件类数;FR-103)。
package agentloop

import (
	"context"

	"mulix/internal/engine"
)

// EventSource 是事件源最小接口(llm.Provider 满足——012 裁定 A:
// agent loop 消费 ctx.llm)。
type EventSource interface {
	Events() <-chan engine.Event
	Host() engine.Host // OnInit/OnControlRequest 的宿主透传
}

// Callbacks 是治理动作回调集(controller 实现之;方法数=事件类数)。
type Callbacks interface {
	OnInit(host engine.Host, ev engine.Event)
	OnAssistant(ev engine.Event)
	OnToolResult(ev engine.Event)
	OnResult(ev engine.Event)
	OnTrace(ev engine.Event)
	OnPermissionDenied(ev engine.Event)
	OnExit(ev engine.Event)
	OnControlRequest(host engine.Host, ev engine.Event)
	OnHookResponse(ev engine.Event)
	OnEngineClosed()
}

// Loop 是回合循环抽象(可替换——P5 PTC 模式在此接缝扩展)。
type Loop interface {
	Run(ctx context.Context, src EventSource, cb Callbacks) error
}

// Default 是默认驱动:泵逻辑逐字节搬家(controller.go pump/dispatch,
// 012 FR-101)。
type Default struct{}

// Run 消费事件流直至关闭,按 Kind 路由到回调;流关闭 → OnEngineClosed。
func (Default) Run(ctx context.Context, src EventSource, cb Callbacks) error {
	for ev := range src.Events() {
		dispatch(src, cb, ev)
	}
	cb.OnEngineClosed()
	return nil
}

// dispatch 单事件路由(与 controller 既有 dispatch 语义逐字节一致;default
// 未知 kind → 跳过——design §1.2 裁定,扩展点留 P5)。
func dispatch(src EventSource, cb Callbacks, ev engine.Event) {
	switch ev.Kind {
	case engine.KindInit:
		cb.OnInit(src.Host(), ev)
	case engine.KindAssistant:
		cb.OnAssistant(ev)
	case engine.KindToolResult:
		cb.OnToolResult(ev)
	case engine.KindResult:
		cb.OnResult(ev)
	case engine.KindTrace:
		cb.OnTrace(ev)
	case engine.KindPermissionDenied:
		cb.OnPermissionDenied(ev)
	case engine.KindExit:
		cb.OnExit(ev)
	case engine.KindControlRequest:
		cb.OnControlRequest(src.Host(), ev)
	case engine.KindHookResponse:
		cb.OnHookResponse(ev)
	default:
		// 未知 kind:跳过(与现状 dispatch default 一致——design §1.2 裁定,
		// 不进回调;OnUnknown 不设——扩展点留 P5)。
	}
}
```



- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/agentloop/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/agentloop/ internal/lint/rules.go
git commit -m "feat(agentloop): Loop 接口+Callbacks+Default 驱动(012 FR-101/103;P4b-2 T2)"
```

---

## Task 3: controller 接线(泵搬家 + 回调实现)

**Files:**
- Modify: `internal/control/controller.go`(pump/dispatch 搬家 + controllerCallbacks 实现 + Options.Loop)
- Modify: `internal/lint/rules.go`(control 矩阵增 `internal/agentloop`)
- Test: 既有全矩阵(零改动=行为零变更证据)

**Interfaces:**
- Consumes: Task 2 的 Loop/Callbacks/EventSource;Task 1 的适配器 Host()。
- Produces: `Options.Loop agentloop.Loop`(nil=Default);controllerCallbacks(c)实现 Callbacks;pump 改为 loop 消费。

- [ ] **Step 1: 写失败测试**

`internal/control/controller_test.go` 追加(Options.Loop 缺省=Default 的显式钉):

```go
// TestDefaultLoopWired 钉 012 FR-102:Options.Loop nil → Default 驱动
// (既有泵语义经 loop 承载——行为零变更)。
func TestDefaultLoopWired(t *testing.T) {
	// 经 newTestControllerWithPolicyRules 装配(Loop 缺省)→ 发起 FakeHost
	// 回合 → 既有断言全绿即承载。显式断言:c.loop 为 *agentloop.Default。
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	if _, ok := c.opts.Loop.(*agentloop.Default); !ok {
		t.Fatalf("loop = %T, want *agentloop.Default (nil=Default)", c.opts.Loop)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run TestDefaultLoopWired -count=1`
Expected: FAIL(Options.Loop 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

①`Options` 增 `Loop agentloop.Loop`(nil=Default)与 `Source agentloop.EventSource`(nil=测试用 hostAsSource 包装);control 矩阵增 `internal/agentloop`(FR-103);②`pump(host)` 改:

```go
func (c *Controller) pump(src agentloop.EventSource) {
	defer close(c.pumpDone)
	loop := c.opts.Loop
	if loop == nil {
		loop = &agentloop.Default{}
	}
	_ = loop.Run(context.Background(), src, controllerCallbacks{c})
}

// hostAsSource 是测试用 EventSource(engine.Host 自身实现 Events;Host() 返回自身)。
type hostAsSource struct{ host engine.Host }

func (s hostAsSource) Events() <-chan engine.Event { return s.host.Events() }
func (s hostAsSource) Host() engine.Host           { return s.host }
```

③`dispatch`/泵尾部整体搬入 `controllerCallbacks`(embed c):

```go
// controllerCallbacks 让 agentloop 调用治理动作(方法体=原 dispatch 各分支
// 与泵尾部——逐字节搬家,FR-101)。
type controllerCallbacks struct{ c *Controller }

func (h controllerCallbacks) OnInit(host engine.Host, ev engine.Event) { h.c.onInit(host, ev) }
func (h controllerCallbacks) OnAssistant(ev engine.Event)              { h.c.onAssistant(ev) }
func (h controllerCallbacks) OnToolResult(ev engine.Event)             { h.c.onToolResult(ev) }
func (h controllerCallbacks) OnResult(ev engine.Event)                 { h.c.onResult(ev) }
func (h controllerCallbacks) OnTrace(ev engine.Event)                  { /* 原 KindTrace 内联段原样 */ }
func (h controllerCallbacks) OnPermissionDenied(ev engine.Event)       { /* 原 KindPermissionDenied 内联段原样 */ }
func (h controllerCallbacks) OnExit(ev engine.Event)                   { h.c.onExit(ev) }
func (h controllerCallbacks) OnControlRequest(host engine.Host, ev engine.Event) {
	h.c.onControlRequest(host, ev)
}
func (h controllerCallbacks) OnHookResponse(ev engine.Event) { h.c.onHook(ev) }
func (h controllerCallbacks) OnEngineClosed()                { /* 泵尾部合成原样搬家(Critical-1 顺序保持) */ }
```

④EventSource 选择裁定:生产路径=claude 适配器(`Bind(host)` 后 Host()/Events() 可用,经 `ctx.llm` 服务键取用);测试路径=`hostAsSource` 小包装(engine.Host 自身实现 Events+Host)。⑤NewController 构造期（**指针语义统一**）：`Loop == nil → c.opts.Loop = &agentloop.Default{}`；`Source == nil → c.opts.Source = hostAsSource{host}`（start 内兜底——HostFactory 产出 host 后：经小接口 `interface{ Bind(engine.Host) error }` 断言适配器并 `Bind(host)`；**生产路径的 Source=适配器**（组合根接线，见 Task 4），hostAsSource 仅为测试形态；适配器具体类型不进 control——分层矩阵保持）。

(注:泵尾部中 `close(c.pumpDone)` 留在 pump goroutine 包装(不在 OnEngineClosed);OnEngineClosed 承接「关闭性发布→running=false→close(turnDone ch)」——Critical-1 顺序逐字节。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/control/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规(既有全矩阵=行为零变更证据)

- [ ] **Step 5: Commit**

```bash
git add internal/control/ internal/lint/rules.go
git commit -m "feat(control): 泵搬家至 agentloop——Loop 接线+回调实现(012 FR-101/102/103;P4b-2 T3)"
```

---

## Task 4: 镜像→总线桥

**Files:**
- Modify: `internal/control/controller.go`(Options.EventPublisher + publish 尾 Emit)、`internal/cli/session.go`(组合根 OpenBus+Provide "events")
- Test: `internal/control/mirror_bridge_test.go`(Create)

**Interfaces:**
- Consumes: kernel.EventPublisher(P4a);eventbus.OpenBus(P4a)。
- Produces: 桥(镜像后总线 Emit);`Options.EventPublisher kernel.EventPublisher`(nil=桥关闭——既有装配兼容)。

- [ ] **Step 1: 写失败测试**

Create `internal/control/mirror_bridge_test.go`:

```go
package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"mulix/internal/event"
	"mulix/internal/kernel"
)

// recordBus 是 kernel.EventPublisher 的记录实现(桥观测面)。
// Publish(name, payload):name=事件名(总线内部补 "mulix." 前缀成 subject)。
// 承重断言:recordBus 实现 kernel.EventPublisher(桥消费面)。
var _ kernel.EventPublisher = (*recordBus)(nil)

type recordBus struct {
	declared  []string
	published []struct {
		name    string
		payload []byte
	}
	failOn string // 注入:该事件名发布失败
}

func (b *recordBus) DeclareEvents(names ...string) error {
	b.declared = append(b.declared, names...)
	return nil
}

func (b *recordBus) Publish(name string, payload []byte) error {
	if b.failOn != "" && name == b.failOn {
		return fmt.Errorf("bus explode (injected)")
	}
	b.published = append(b.published, struct {
		name    string
		payload []byte
	}{name, payload})
	return nil
}

// TestMirrorBridgeOrderAndPayload 钉 012 FR-104:镜像先行后总线 Emit
// (事件名=mirror.<kind>;总线内部补 "mulix." 成 subject);payload=镜像行
// 同源序列化;顺序可证。
func TestMirrorBridgeOrderAndPayload(t *testing.T) {
	bus := &recordBus{}
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.mu.Lock()
	c.evPub = bus // NewController 已从 opts.EventPublisher 复制;此处注入测试观测面
	c.mu.Unlock()
	// 触发一次 publish(直接调,单点观测)。
	c.publish(event.TurnStartedEvent{Seq: 1, SessionID: "s-test"})
	// 镜像已有该事件(先持久化);总线同事件名+payload。
	if len(bus.published) != 1 {
		t.Fatalf("bus published = %d, want 1", len(bus.published))
	}
	if bus.published[0].name != "mirror.turn_started" {
		t.Fatalf("event name = %q, want mirror.turn_started", bus.published[0].name)
	}
	var probe map[string]any
	if err := json.Unmarshal(bus.published[0].payload, &probe); err != nil {
		t.Fatalf("payload must be the mirror line (json): %v", err)
	}
	if probe["kind"] != "turn_started" {
		t.Fatalf("payload kind = %v", probe["kind"])
	}
}

// TestMirrorBridgeFailureFailVisible 钉 FR-105:总线失败 → 镜像已写
// (不回滚)+ fail-visible 留痕(直接 Store.Append+Sink.Publish——pubMu
// 不可重入,禁走 c.publish)+ 桥自禁(后续 publish 不再尝试总线)。
func TestMirrorBridgeFailureFailVisible(t *testing.T) {
	bus := &recordBus{failOn: "mirror.turn_started"}
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.mu.Lock()
	c.evPub = bus
	c.mu.Unlock()
	c.publish(event.TurnStartedEvent{Seq: 1, SessionID: "s-test"})
	// 镜像已有该事件(不回滚)。
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	found := false
	for _, ev := range mirror {
		if _, ok := ev.(event.TurnStartedEvent); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("mirror must keep the event (桥失败不阻断镜像)")
	}
	// fail-visible:直接 Append 的 warning Notice 落镜像(不经 c.publish——
	// pubMu 不可重入;不经桥——防递归)。
	warnFound := false
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "eventbus") {
			warnFound = true
		}
	}
	if !warnFound {
		t.Fatal("bridge failure must leave a fail-visible Notice (FR-105)")
	}
	// 桥自禁:第二次 publish 不再尝试总线(bus.published 不增长)。
	n := len(bus.published)
	c.publish(event.TurnStartedEvent{Seq: 2, SessionID: "s-test"})
	if len(bus.published) != n {
		t.Fatal("bridge must self-disable after failure (防递归)")
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run TestMirrorBridge -count=1`
Expected: FAIL(EventPublisher/evPub 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

①`Options` 增 `EventPublisher kernel.EventPublisher`(nil=桥关——既有装配兼容);`NewController` 复制到 `c.evPub`;②`publish` 尾部(既有 Sink 发布之后):

```go
	// 镜像→总线桥(012 FR-104/105):先镜像后总线(顺序红线);失败
	// fail-visible + 桥自禁(防递归)。**禁走 c.publish/禁经桥发 Notice**——
	// pubMu 不可重入(controller.go:48-50 红线);留痕走直接
	// Store.Append+Sink.Publish(与既有镜像失败分支 1378-1381 同款)。
	if c.evPub != nil && !c.bridgeDisabled {
		subject := "mirror." + string(sessionx.EventKindOf(ev))
		if err := c.evPub.Publish(subject, mirrorLine); err != nil {
			c.mu.Lock()
			c.bridgeDisabled = true // 桥自禁(防递归;首次失败已留痕)
			c.mu.Unlock()
			// fail-visible:直接镜像+Sink(不经 c.publish/不经桥)。
			warn := event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
				Content: fmt.Sprintf("eventbus: 镜像桥发布失败(%s): %v", subject, err)}
			if aerr := c.opts.Store.Append(warn); aerr == nil {
				c.opts.Sink.Publish(warn)
			}
		}
	}
```

③`kindOf`/`mirrorLine` 来源:`sessionx.EventKindOf(ev)`(导出既有 eventKindOf——sessionx 拥有 kind 映射,单一来源);mirrorLine=镜像 append 的同源序列化(实现形态:Append 返回写入行或 publish 内以同 marshaler 序列化——字节一致为准,实现时落位)。④声明:组合根 OpenBus 后 `bus.DeclareEvents(<全部 kind 的 mirror.*>)` 一次性声明。⑤Controller 增 `evPub kernel.EventPublisher` + `bridgeDisabled bool` 字段。

⑥`internal/cli/session.go` 组合根:**总线接线经 `Options.EnableEventBus` 门控(默认关;真实入口 openSession 开——测试装配默认关,零文件句柄落 TempDir)**;进程级单例按工作区键复用(TUI 换会话/子会话装配复用同实例——避免同目录并发 JetStream);

```go
// 总线单例按工作区键隔离(sync.Once 不带键——首个 TempDir 工作区会锁死
// 后续测试的 StoreDir;Windows FileStorage 句柄锁目录,清理即失败)。
// DeclareEvents 失败/目录被清 → 桥自禁既有路径兜底(测试不受阻)。
var (
	busMu   sync.Mutex
	busByWS = map[string]*eventbus.Bus{}
)

func sharedEventBus(o Options) *eventbus.Bus {
	key := o.Workspace
	busMu.Lock()
	defer busMu.Unlock()
	if b, ok := busByWS[key]; ok {
		return b
	}
	b, err := eventbus.OpenBus(eventbus.BusOptions{
		StoreDir: filepath.Join(o.Workspace, ".mulix", "bus"),
	})
	if err != nil {
		return nil // 桥关闭(fail-visible 语义无 Notice 面;如实记录)
	}
	busByWS[key] = b
	return b
}

// CloseSharedBus 关闭全部工作区总线(进程退出调用;FileStorage 同步发布
// 已持久,无 Close 亦安全——显式收口为卫生面)。
func CloseSharedBus() {
	busMu.Lock()
	defer busMu.Unlock()
	for _, b := range busByWS {
		_ = b.Close()
	}
	busByWS = map[string]*eventbus.Bus{}
}
```

(openSession 内:`if o.EnableEventBus { if bus := sharedEventBus(o); bus != nil { bus.DeclareEvents(<全部 mirror.*>); ... } }`——门控关=桥关闭(测试零文件句柄);OpenBus 失败=桥关闭,如实记录。)

**llm 适配器→Source 生产接线(012 FR-101a;在 `assembleKernelContext` 内回填——011 已在该函数 Provide("llm", adapter),无双 Provide):**

`assembleKernelContext`(session.go)内,既有 `root.Provide("llm", adapter)` 之后:

```go
	// Source 回填(012 FR-101a 生产路径):适配器即 agentloop.EventSource
	// (Events+Host)——泵经 ctx.llm 消费宿主事件流;start 内 HostFactory
	// 产出 host 后,controller 经小接口 interface{ Bind(engine.Host) error }
	// 断言并 Bind(适配器)——seam-only 形态退役。
	o.Source = adapter
```

(装配测试断言:`opts.Source` 与 `"llm"` 键值同实例——与 sink/store/meter 的同源核对同款。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/control/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿(桥测试直接注入 evPub——不受 EnableEventBus 门控影响;门控只管组合根接线)

- [ ] **Step 5: Commit**

```bash
git add internal/control/ internal/cli/
git commit -m "feat(control,cli): 镜像→总线桥——先镜像后总线+fail-visible+桥自禁(012 FR-104/105;P4b-2 T4)"
```

## Task 5: ctx.agentLoop 挂载(组合根)

**Files:**
- Modify: `internal/cli/session.go`(assembleKernelContext 增 agentloop 键——Default 驱动)
- Test: `internal/cli/kernel_assembly_test.go`(键断言)

**Interfaces:**
- Consumes: Task 2 Loop;Task 3 Options.Loop。
- Produces: `ctx.agentLoop` 键就位(*agentloop.Default)。

- [ ] **Step 1: 断言增强**

`internal/cli/kernel_assembly_test.go` 的键集断言增 `agentloop`:

```go
	for _, k := range []string{"sink", "store", "meter", "gate", "config", "agentloop"} {
		if _, ok := root.Service(k); !ok {
			t.Fatalf("key %q missing (SC-001)", k)
		}
	}
	if v, ok := root.Service("agentloop"); ok {
		if _, isDefault := v.(*agentloop.Default); !isDefault {
			t.Fatalf("agentloop = %T, want *agentloop.Default", v)
		}
	}
```

- [ ] **Step 2: 实现**

`assembleKernelContext` 的 provides 列表增 `{"agentloop", &agentloop.Default{}}`(指针语义,与 T3 写回一致)。

- [ ] **Step 3: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: 全绿/0 违规

```bash
git add internal/cli/
git commit -m "feat(cli): ctx.agentLoop 服务键——Default 驱动挂载(012 FR-102;P4b-2 T5)"
```

---

## Task 6: 全矩阵回归收口

**Files:**
- (无新代码——收口核验)

**Interfaces:**
- Consumes: Task 1-5。
- Produces: 回归结论(task 报告);P4 收官判据(SC-004)。

- [ ] **Step 1: 全矩阵**

Run: `go test ./... -count=1 -v && go vet ./... && gofmt -l internal/ cmd/ && go run ./cmd/mulix lint layers`
Expected: 20+ 包全绿/0 违规;425+ 子测试(行为零变更实证);既有 e2e(委派链/崩溃恢复/审批回路)全绿。

- [ ] **Step 2: 台账与文档收口(T7 并入)**

ledger 记录回归结论;P4 收官判据(SC-004:ctx.agentLoop 就位+三变更归档)写入 task 报告;CLAUDE.md §3.3 的「P4b 待办」行更新(P4b-2 完成,ctx.agentLoop 就位)。
