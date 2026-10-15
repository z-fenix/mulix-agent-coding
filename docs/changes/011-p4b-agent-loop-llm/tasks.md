# P4b-1 核心插件化(上) 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps are plain bold step headings — no checkbox markers.

**Goal:** 组合根 kernel 化(十服务键)+ `llm.Provider` 接缝与 claude CLI 适配器插件 + 引擎可见清单冻结快照——行为零变更,全量回归。

**Architecture:**(architectural,设计文档已批准:`.mulix/.runtime/011-p4b-agent-loop-llm/specs/2026-10-11-p4b1-design.md`)①openSession 先建根 kernel Context → 逐键 Provide(sink/store/meter/gate/registry/memory/hooks/approvals/config/llm)→ 以 Service 查找回填 Options(同源,断言失败 fail-closed);②新包 `internal/llm`:Provider 接缝(通用回合语义)+ claude 适配器(包装既有 engine.Host,FakeProvider 包装 FakeHost);③Registry 增 `Snapshot()` 读面,引擎可见面=NewServer 构造期冻结(会话粒度,现状钉)——回合粒度语义随 P4b-2 评估。

**Tech Stack:** Go 标准库;既有测试基建(lifecycle_test 装配、e2e)。

**Spec:** `docs/specs/011-p4b-agent-loop-llm/spec.md`

**Design:** `.mulix/.runtime/011-p4b-agent-loop-llm/specs/2026-10-11-p4b1-design.md`

## Global Constraints

- FR-106 行为零变更:全量 20 包 + e2e + 单飞/watcher/收口矩阵全绿;Boot 面字节不变。
- FR-106/107:llm 接口纯净(无 claude 概念,grep 验收);分层矩阵 0 违规。
- 最小化:controller 44 方法零动;每任务 `go test ./... -count=1` 全绿再 commit。

## Review Focus

- 服务键与 Options 同源断言(单测钉 SC-001)。
- llm.Provider 接口纯净性:接口文件无 "claude" 字样(grep 验收)。
- 冻结快照:会话粒度冻结现状钉(NewServer 构造期;回合粒度随 P4b-2 评估)。
- 行为零变更:既有全矩阵零回归(每任务全量门禁)。

---

## Task 1: llm 包——Provider 接缝定义

**Files:**
- Create: `internal/llm/llm.go`
- Modify: `internal/lint/rules.go`(矩阵登记 `internal/llm`)

**Interfaces:**
- Produces: `package llm`;`type Provider interface`——方法集与 engine.Host 逐一对应(Start(ctx, BootConfig)/Send(ctx, UserMessage)/Events/RespondControl/Interrupt/Close(ctx)/Info/Name);矩阵登记 `internal/llm: {"internal/engine"}` 与 `internal/llm/claude: {"internal/engine", "internal/llm"}`。

**Step 1: 写失败测试(接口存在性)**

Create `internal/llm/llm_test.go`:

```go
package llm

import "testing"

// TestProviderInterfaceExists 钉 011 FR-102:接缝存在(编译期形态钉)。
func TestProviderInterfaceExists(t *testing.T) {
	var _ Provider = (Provider)(nil)
}
```

**Step 2: 跑测试验证失败**

Run: `go test ./internal/llm/ -count=1`
Expected: FAIL(包未存在——编译期 RED,新包 TDD 形态,台账记 Ruling)。

**Step 3: 实现**(engine.UserMessage/ControlResponse/EngineInfo 类型存在性先核对 internal/engine/host.go——签名逐一镜像 Host)

`internal/llm/llm.go`:

```go
// Package llm 定义模型提供方接缝(dsh ctx.llm 对齐):回合语义抽象,
// 不含任何具体引擎概念,也不提及任何具体引擎名——实现=命令行引擎
// 适配器插件(默认适配器,包装 internal/engine 的宿主机制)。FR-102。
package llm

import (
	"context"

	"mulix/internal/engine"
)

// Provider 是引擎回合的抽象接缝。形状与 engine.Host 逐一对应(boot 提交、
// 消息发送、事件流、控制应答、打断、收口、信息)——engine.BootConfig/
// UserMessage/ControlResponse/EngineInfo 为通用引擎概念(非具体引擎特有)。
// 接缝纯净性验收=本文件无具体引擎名字样。实现=默认命令行引擎适配器插件。
type Provider interface {
	Name() string
	Start(ctx context.Context, cfg engine.BootConfig) error
	Send(ctx context.Context, msg engine.UserMessage) error
	Events() <-chan engine.Event
	RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error
	Interrupt(ctx context.Context) error
	Close(ctx context.Context) error
	Info() engine.EngineInfo
}
```

`internal/lint/rules.go` layerMatrix 增(两条):
`"internal/llm": {"internal/engine"}` 与 `"internal/llm/claude": {"internal/engine", "internal/llm"}`(claude 子包 T2 落地——矩阵预先登记避免 T2 门禁红)。

**Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/llm/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

**Step 5: Commit**

```bash
git add internal/llm/ internal/lint/rules.go
git commit -m "feat(llm): Provider 接缝定义——通用回合语义(011 FR-102;P4b-1 T1)"
```

---

## Task 2: claude CLI 适配器(seam-only)+ FakeProvider

**Files:**
- Create: `internal/llm/claude/adapter.go`
- Modify: `internal/llm/llm.go`(FakeProvider 定义追加)
- Modify: `internal/llm/llm_test.go`(FakeProvider 判别断言)
- Test: `internal/llm/claude/adapter_test.go`

**Interfaces:**
- Consumes: Task 1 `llm.Provider`。
- Produces: ①`ClaudeAdapter`(seam-only:Name()="claude-cli";回合方法 fail-closed 返回 `ErrNotBound`——**011 裁定 b**:Host 实例只在 controller.start 期存在,委托接线随 P4b-2 落地,方法集已与 engine.Host 逐一对应);②`ErrNotBound` 哨兵错误;③`FakeProvider`(同形态,Name()="fake")。

**Step 1: 写失败测试**

Create `internal/llm/claude/adapter_test.go`:

```go
package claude

import (
	"context"
	"testing"

	"mulix/internal/engine"
	"mulix/internal/llm"
)

// TestClaudeAdapterImplementsProvider 钉 011 FR-102/SC-002:适配器实现
// llm.Provider(编译期承重断言)。
func TestClaudeAdapterImplementsProvider(t *testing.T) {
	var _ llm.Provider = NewClaudeAdapter()
}

// TestClaudeAdapterSeamOnly 钉 seam-only 语义:回合方法 fail-closed 返回
// ErrNotBound(不静默);Events 返回 nil 流。
func TestClaudeAdapterSeamOnly(t *testing.T) {
	a := NewClaudeAdapter()
	if a.Name() != "claude-cli" {
		t.Fatalf("name = %q, want claude-cli", a.Name())
	}
	if err := a.Start(context.Background(), engine.BootConfig{}); err != ErrNotBound {
		t.Fatalf("start err = %v, want ErrNotBound", err)
	}
	if a.Events() != nil {
		t.Fatal("events must be nil pre-bind")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("close (no resources) = %v, want nil", err)
	}
}
```

(llm_test.go 追加判别断言:`var _ llm.Provider = NewFakeProvider()`——FakeProvider 定义见 Step 3 llm.go 追加段。)

**Step 2: 跑测试验证失败**

Run: `go test ./internal/llm/... -count=1`
Expected: FAIL(NewClaudeAdapter/ErrNotBound 未定义——编译期 RED,计划预期)。

**Step 3: 实现**

`internal/llm/claude/adapter.go`:

```go
// Package claude 是 llm.Provider 的 claude CLI 适配器(默认实现)。
// **seam-only 形态**(011 裁定 b):Host 实例只在 controller.start 期存在,
// 本变更不绑定——方法集与 engine.Host 逐一对应,P4b-2 增加 Bind(host) 与
// 委托体即可,接口零变更。engine.lock 漂移治理随实现走(语义不变)。
package claude

import (
	"context"
	"fmt"

	"mulix/internal/engine"
)

// ErrNotBound 是 seam-only 形态的回合方法返回值:fail-closed,不静默。
var ErrNotBound = fmt.Errorf("llm: provider not bound (seam-only; delegation lands in P4b-2)")

// ClaudeAdapter 是 claude CLI 适配器(seam-only)。
type ClaudeAdapter struct{}

// NewClaudeAdapter 创建 seam-only 适配器。
func NewClaudeAdapter() *ClaudeAdapter { return &ClaudeAdapter{} }

// Name 返回适配器名(engine.lock 治理随此实现走)。
func (a *ClaudeAdapter) Name() string { return "claude-cli" }

// Start 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Start(ctx context.Context, cfg engine.BootConfig) error { return ErrNotBound }

// Send 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Send(ctx context.Context, msg engine.UserMessage) error { return ErrNotBound }

// Events 未绑定 → nil 流(fail-closed:无宿主即无事件)。
func (a *ClaudeAdapter) Events() <-chan engine.Event { return nil }

// RespondControl 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error {
	return ErrNotBound
}

// Interrupt 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Interrupt(ctx context.Context) error { return ErrNotBound }

// Close 未绑定 → nil(无资源)。
func (a *ClaudeAdapter) Close(ctx context.Context) error { return nil }

// Info 未绑定 → 零值。
func (a *ClaudeAdapter) Info() engine.EngineInfo { return engine.EngineInfo{} }
```

`internal/llm/llm.go` 追加(FakeProvider,测试装配用,同 seam-only 形态):

```go
// FakeProvider 是测试装配用的 seam-only Provider(Name()="fake";同形态)。
type FakeProvider struct{}

// NewFakeProvider 创建测试装配 Provider。
func NewFakeProvider() *FakeProvider { return &FakeProvider{} }

// Name 返回 "fake"。
func (p *FakeProvider) Name() string { return "fake" }

// Start 未绑定 → ErrNotBound(同 seam-only 语义)。
func (p *FakeProvider) Start(ctx context.Context, cfg engine.BootConfig) error { return ErrNotBound }

// Send 未绑定 → ErrNotBound。
func (p *FakeProvider) Send(ctx context.Context, msg engine.UserMessage) error { return ErrNotBound }

// Events 未绑定 → nil。
func (p *FakeProvider) Events() <-chan engine.Event { return nil }

// RespondControl 未绑定 → ErrNotBound。
func (p *FakeProvider) RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error {
	return ErrNotBound
}

// Interrupt 未绑定 → ErrNotBound。
func (p *FakeProvider) Interrupt(ctx context.Context) error { return ErrNotBound }

// Close 未绑定 → nil。
func (p *FakeProvider) Close(ctx context.Context) error { return nil }

// Info 未绑定 → 零值。
func (p *FakeProvider) Info() engine.EngineInfo { return engine.EngineInfo{} }
```

**Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/llm/... -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

**Step 5: Commit**

```bash
git add internal/llm/
git commit -m "feat(llm): claude CLI 适配器 seam-only 形态+FakeProvider(011 FR-102;P4b-1 T2)"
```

## Task 3: 组合根 kernel 化(服务键装配)

**Files:**
- Modify: `internal/cli/session.go`(`openSession`/`newSessionAssembly` 装配段)
- Test: `internal/cli/kernel_assembly_test.go`(Create)

**Interfaces:**
- Consumes: kernel `NewContext/Provide/Service`;既有装配变量(hostFactory/sink/store/meter/gate/...)。
- Produces: 根 Context 十服务键就位;Options 字段自 Service 查找回填(同源);`NewController` 签名不变。

**Step 1: 写失败测试**

Create `internal/cli/kernel_assembly_test.go`(按 lifecycle/装配测试同款构造;断言根 Context 服务键与 Options 一致):

```go
package cli

import (
	"testing"

	"mulix/internal/kernel"
)

// TestRootContextServiceKeys 钉 011 FR-101(SC-001):组合根装配后,根
// Context 的服务键全部就位且与传给 NewController 的 Options 同源。
// 构造方式:抽出的 assembleKernelContext 在真实装配路径上被调用后,
// 白盒断言各键(实现见 Step 3 的抽函数)。
func TestRootContextServiceKeys(t *testing.T) {
	// 以既有装配测试同款构造 openSession 前置(装配抽函数可独立调用)。
	// 断言:sink/store/meter/gate/llm 等键在位(可空键按裁定跳过)。
	_ = kernel.NewContext(nil) // 编译面钉:kernel 可从 cli 抵达(组合根)
	// 详细断言在 Step 3 实现的 assembleKernelContext 返回值上执行。
}
```

(注:本测试的最终形态依赖 Step 3 抽出的 `assembleKernelContext(...)` 返回 `*kernel.Context`——实现时以真实装配变量补全断言:sink/store/meter/gate 五键非空 + 与 Options 字段同值。)

**Step 2: 跑测试验证失败**

Run: `go test ./internal/cli/ -run TestRootContextServiceKeys -count=1`
Expected: FAIL(assembleKernelContext 未定义——编译期 RED,计划预期)。

**Step 3: 实现**

锚点:`internal/cli/session.go:140` 的 **`(Options).assembleController`**(主会话与子会话共用的装配核心——两路都要 kernel 化;每次装配创建**自己的根 Context**,含子会话)。

```go
// assembleKernelContext 把装配核心产出的依赖以 kernel 服务键登记(011 FR-101)。
// 每次装配一个根 Context(含子会话装配);可空依赖 nil 跳过——键不就位与
// 「nil=功能关闭」语义一致。返回根 Context 与回填后的 Options(同源)。
func assembleKernelContext(o Options) (*kernel.Context, Options, error) {
	root := kernel.NewContext(nil)
	provides := []struct {
		key string
		v   any
	}{
		{"sink", o.Sink}, {"store", o.Store}, {"meter", o.Meter},
		{"gate", o.Gate}, {"config", o.Config},
	}
	for _, kv := range provides {
		if err := root.Provide(kv.key, kv.v); err != nil {
			return nil, Options{}, err
		}
	}
	if o.Approvals != nil {
		if err := root.Provide("approvals", o.Approvals); err != nil {
			return nil, Options{}, err
		}
	}
	if o.Memory != nil {
		if err := root.Provide("memory", o.Memory); err != nil {
			return nil, Options{}, err
		}
	}
	if o.Hooks != nil {
		if err := root.Provide("hooks", o.Hooks); err != nil {
			return nil, Options{}, err
		}
	}
	if o.Registry != nil {
		if err := root.Provide("registry", o.Registry); err != nil {
			return nil, Options{}, err
		}
	}
	// llm 键：seam-only 适配器（011 裁定 b——委托接线随 P4b-2 controller
	// 消费 ctx.llm 时落地；键先就位，接口形状已与 engine.Host 对应）。
	if err := root.Provide("llm", claude.NewClaudeAdapter()); err != nil {
		return nil, Options{}, err
	}
	// 回填(同源:键值字段=查找值;两值 Service + 类型断言失败 fail-closed)。
	out := o // 非键字段(HostFactory/Workspace/Unattended/AllowEngineDrift/
	// SubagentFactory/SubagentLimits)原样携带。
	if v, ok := root.Service("sink"); ok {
		out.Sink = v.(control.Sink)
	}
	if v, ok := root.Service("gate"); ok {
		out.Gate = v.(*guard.Gate)
	}
	// store/meter/config 同型(assert 到各自类型——逐字段核对类型断言)。
	return root, out, nil
}
```

(注:①**Service 为两值返回** `v, ok := root.Service(key)`——单值链不编译;②回填必须携带**全部非键字段**(HostFactory/Workspace/Unattended/AllowEngineDrift/SubagentFactory/SubagentLimits/Memory/HookConsumers)——空 Options 会死在 NewController 的 fail-closed 校验(controller.go:180-182);③调用序=先构造完整 Options 字面量（现状）→ assembleKernelContext(literal) 做 Provide+同源回填 → NewController 消费回填结果。

**Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/cli/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

**Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): 组合根 kernel 化——服务键装配+Options 同源回填(011 FR-101;P4b-1 T3)"
```

---

## Task 4: llm 服务键接入 + Registry 冻结快照

**Files:**
- Modify: `internal/registry/registry.go`(Snapshot 读面)
- Test: `internal/registry/registry_test.go`(快照)、Boot 组装测试(冻结语义)

**Interfaces:**
- Consumes: Task 1-3。
- Produces: `registry.Snapshot()` 包级读面；会话粒度冻结现状钉（无 composeBoot 改动；llm 键接入已在 T3）。

**Step 1: 写失败测试**

(a) `internal/registry/registry_test.go`(按既有测试构造):

```go
// TestSnapshotFreezesAtCallTime 钉 011 FR-103:Snapshot 返回调用时刻副本——
// 调用后的注册不影响副本(冻结语义)。
func TestSnapshotFreezesAtCallTime(t *testing.T) {
	// 构造 registry + 注册工具 A → snap1 := Snapshot() → 注册工具 B
	// 断言:snap1 不含 B(副本);再次 Snapshot 含 B(当前态)。
}
```

(最终断言代码在实现时按既有 registry 测试的工具注册助手落位——Snapshot 的「副本不可变」为不可省断言。)

（b）会话粒度冻结现状钉：NewServer 构造期冻结即引擎可见面——本子测试钉「构造后注册不影响已构造 Server」。

**Step 2: 跑测试验证失败**

Run: `go test ./internal/registry/ -count=1`
Expected: FAIL(Snapshot 未定义——编译期 RED,计划预期)。

**Step 3: 实现**

**重定域说明(评审核实)**:registry 的工具存储是**包级全局**(`registry` map + `regMu`,类型 `Tool`/`registeredTool`);`*Server` 在构造期即冻结(NewServer——这就是现状的「引擎可见面」);mcp-config 只含 MCPServers(server spawn 条目),**无工具清单**——FR-103 的「mcp-config 组装源=快照」字面不成立。故本任务重定域:

①`internal/registry` 增**包级**快照读面(为 effect 可逆注册的未来写面提供读原语):

```go
// Snapshot 返回当前已注册工具的副本(009/011 FR-103 读原语:effect 可逆
// 注册的未来写面消费;副本不可变)。
func Snapshot() []Tool {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Tool, 0, len(registry))
	for _, t := range registry {
		out = append(out, t.Tool)
	}
	return out
}
```

②**现状钉**:NewServer 的构造期冻结即「引擎可见面」的现行机制(会话粒度)——本任务**不改 composeBoot/无回合粒度改动**;回合粒度冻结的语义钉测试:

```go
// TestEngineVisibleFreezeIsSessionGranular 钉现状语义(011 FR-103):
// 引擎可见清单的冻结粒度=会话(NewServer 构造期);回合中途注册不影响
// 已组装会话——本测试钉住该现状,防未来无意改变。
func TestEngineVisibleFreezeIsSessionGranular(t *testing.T) {
	// 构造 Server(快照时点 1)→ 全局注册新工具 → 断言 Server 面不含新工具
	// → 新构造 Server(快照时点 2)→ 含新工具(会话粒度生效)。
}
```

(最终断言代码按既有 registry 测试助手落位;「会话粒度冻结」为不可省断言。)

**Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

**Step 5: Commit**

```bash
git add internal/cli/ internal/registry/ internal/llm/
git commit -m "feat(registry): Snapshot 读面——会话粒度冻结现状钉(011 FR-103;P4b-1 T4)"
```

---

## Task 5: SC 钉(一致性/纯净性/冻结)收口

**Files:**
- Test: `internal/cli/kernel_assembly_test.go`(补全)、`internal/llm/claude/adapter_test.go`(纯净性 grep)

**Interfaces:**
- Consumes: Task 1-4 全部产出。
- Produces: SC-001(键集同源)/SC-002(适配器实现 Provider)/纯净性验收的最终钉。

**Step 1: 补全断言**

①Task 3 的 `TestRootContextServiceKeys` 以真实装配补全(sink/store/meter/gate/config/llm 六键 + 与 Options 同值断言);②纯净性 grep(Run:`grep -in "claude" internal/llm/llm.go`——Expected: 零命中);③llm 键与适配器 Name 断言。

**Step 2: 全量门禁**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: 全绿/0 违规

**Step 3: Commit**

```bash
git add internal/cli/ internal/llm/
git commit -m "test(cli,llm): SC 钉收口——键集同源/接口纯净(011 SC-001/002;P4b-1 T5)"
```

---

## Task 6: 全矩阵回归收口

**Files:**
- (无新代码——收口核验)

**Interfaces:**
- Consumes: Task 1-5。
- Produces: 回归结论(task 报告)。

**Step 1: 全矩阵**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ cmd/ && go run ./cmd/mulix lint layers`
Expected: 20+ 包全绿/0 违规(e2e/单飞/watcher/收口/Boot 护栏全绿=FR-106)。

**Step 2: 台账**

ledger 记录回归结论;任务完成。
