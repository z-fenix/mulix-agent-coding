# P6 loop 插件化 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** kernel 成为 loop 权威真源:loop 以 Plugin 形态挂载,control 经 ctx 装配期解析(fail-closed),`Options.Loop` 退役;行为零变更。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/021-p6-loop-plugin-kernel-authority/specs/2026-10-14-loop-plugin-design.md`)①`agentloop.NewPlugin()`(kernel.Plugin;Mount=Provide `"agentloop"`=Loop)——内建 loop 经插件路径挂载;②`control.Options` 增 `Kernel *kernel.Context`,NewController 装配期解析+断言(缺键/类型不符 fail-closed),pump 消费 `c.loop`;③`Options.Loop` 退役(零消费点)。

**Tech Stack:** Go 标准库。

**Spec:** `docs/specs/021-p6-loop-plugin-kernel-authority/spec.md`

**Design:** `.mulix/.runtime/021-p6-loop-plugin-kernel-authority/specs/2026-10-14-loop-plugin-design.md`

## Global Constraints

- FR-104 / FR-105:每任务 `go test ./... -count=1` 全绿再 commit;vet/gofmt/lint layers 干净;零第三方新增;分层零新边(control→agentloop/kernel、agentloop→kernel 均已在矩阵)。
- 行为零变更(FR-104):Default pump 语义逐字节;Source 接缝(012 FR-101a)不动;不做兼容兜底(fail-closed 是语义本体)。
- 子会话继承=assembleKernelContext 单点各自装配回填(主/子会话各建 root,回填点覆盖两路——评审 Minor 6 表述修正)。

## Review Focus

- 缺键/类型不符双钉文案精确(`服务键缺失`/`类型不符 (%T, want agentloop.Loop)`)。
- 测试面注入完整性:8 调用点(helper 集中+e2e×2+subagent_wiring×1)——漏一处即该测试 fail-closed 爆炸(编译期不可见,运行期红——全量绿即证明注入完整)。
- `Options.Loop` 退役零残留(grep 钉)。
- pump 行为零变更(dispatch 路由/泵尾部不动——只换 loop 来源)。
- Mount 失败回滚:同键已注册场景沿 kernel 语义。

---

## Task 1: agentloop.Plugin + 装配 Mount

**Files:**
- Modify: `internal/agentloop/agentloop.go`(Plugin 追加)
- Modify: `internal/cli/session.go`(assembleKernelContext 裸 Provide → Mount)
- Test: `internal/agentloop/agentloop_test.go`(追加)

**Interfaces:**
- Produces: `func NewPlugin() Plugin` + `(Plugin) Name() string`("agentloop")+ `(Plugin) Mount(*kernel.Context) error`(Provide `"agentloop"`=Loop(Default{}))。

- [ ] **Step 1: 写失败测试**

`internal/agentloop/agentloop_test.go` 追加(import 按现状补 `mulix/internal/kernel`):

```go
// TestPluginMount 钉 021 FR-101:loop 插件形态——Mount 提供键/取回 Loop
// 接口/同键回滚沿 kernel 语义。
func TestPluginMount(t *testing.T) {
	root := kernel.NewContext(nil)
	if err := root.Mount(NewPlugin()); err != nil {
		t.Fatalf("mount: %v", err)
	}
	keys := root.Keys()
	found := false
	for _, k := range keys {
		if k == "agentloop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("keys = %v, want agentloop", keys)
	}
	svc, ok := root.Service("agentloop")
	if !ok {
		t.Fatal("service agentloop missing")
	}
	if _, ok := svc.(Loop); !ok {
		t.Fatalf("service type %T, want Loop", svc)
	}
	// 同键已注册→Mount 失败回滚(fail-closed 键即契约)。
	if err := root.Mount(NewPlugin()); err == nil {
		t.Fatal("duplicate mount must fail-closed")
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/agentloop/ -run TestPluginMount -count=1`
Expected: FAIL(`undefined: NewPlugin`——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`agentloop.go` 追加(import 确认 kernel——矩阵已允许):

```go
// Plugin 是内建 loop 的插件壳(021 FR-101):「loop 本身是插件」——内建
// Default 也经 kernel 插件路径挂载,未来任何 loop 实现 kernel.Plugin
// (Mount 提供 "agentloop" 键)即替换。
type Plugin struct{}

// NewPlugin 创建内建 loop 插件。
func NewPlugin() Plugin { return Plugin{} }

// Name 实现 kernel.Plugin。
func (Plugin) Name() string { return "agentloop" }

// Mount 实现 kernel.Plugin:提供 "agentloop" 服务键(值=Loop 接口;指针
// 形态——kernel_assembly_test 既有断言 v.(*agentloop.Default),评审 Critical 3)。
func (Plugin) Mount(c *kernel.Context) error {
	return c.Provide("agentloop", Loop(&Default{}))
}
```

`internal/cli/session.go` assembleKernelContext:`root.Provide("agentloop", &agentloop.Default{})` 改:

```go
	if err := root.Mount(agentloop.NewPlugin()); err != nil {
		return nil, control.Options{}, fmt.Errorf("assemble: %w", err)
	}
```

(注:原 Provide 错误返回形态照旧;Mount 失败沿 kernel 回滚。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/agentloop/ ./internal/cli/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿(Mount 后既有 kernel_assembly 测试的 agentloop 键断言不变——键仍就位)。

- [ ] **Step 5: Commit**

```bash
git add internal/agentloop/ internal/cli/session.go
git commit -m "feat(agentloop,cli): loop 插件壳+装配 Mount——内建经插件路径(021 FR-101;T1)"
```

---

## Task 2: control 权威消费 + Options.Loop 退役

**Files:**
- Modify: `internal/control/controller.go`(Options.Kernel+NewController 校验+Controller.loop+pump 改造+Options.Loop 删)
- Modify: `internal/cli/session.go`(assembleKernelContext 内部回填 `o.Kernel = root`(评审 Important 5——238 字面量处 root 不在作用域))
- Test: `internal/control/lifecycle_test.go`(helper 注入+负向双钉,追加)
- Modify: `internal/control/controller_test.go`(**4 个构造点**:84 newTestController/151 WithRules/194 Attended/474 直调——评审 Important 4:各 helper 独立构造,非 lifecycle 单点)
- Modify: `internal/e2e/e2e_test.go`(113/325 直调;**补 kernel import**;e2e 包内无 helper——自建最小 loop ctx 或复制 kernelCtxWithLoop 形态)、`internal/cli/subagent_wiring_test.go`(482 直调;**补 kernel import**)

**Interfaces:**
- Consumes: Task 1 Plugin(Mount 提供键);012 Source 现形态(不动)。
- Produces: `Options.Kernel *kernel.Context`;`Controller.loop agentloop.Loop`(装配期解析);`Options.Loop` 删除。

- [ ] **Step 1: 写失败测试**

`internal/control/lifecycle_test.go` 追加(helper `kernelCtxWithLoop` + 负向双钉):

```go
// kernelCtxWithLoop 返回挂载 loop 插件的最小根 ctx(021 测试注入缝)。
func kernelCtxWithLoop() *kernel.Context {
	root := kernel.NewContext(nil)
	if err := root.Mount(agentloop.NewPlugin()); err != nil {
		panic(err)
	}
	return root
}

// TestNewControllerRequiresKernelLoop 钉 021 FR-102:缺 Kernel/缺键/类型
// 不符三态 fail-closed(键即契约——缺 loop 的 controller 不该存在)。
func TestNewControllerRequiresKernelLoop(t *testing.T) {
	base := func() Options {
		root := t.TempDir()
		store, err := sessionx.Create(root, sessionx.Options{ID: "s-loop-test"})
		if err != nil {
			t.Fatal(err)
		}
		// Gate 构造照 newTestControllerCapturingBoot 既有形态逐字段对齐
		// (guard.NewPolicy+NewGate(GateOptions{...})——评审 Critical 1:
		// NewGate 签名是 GateOptions 值类型,nil 非法)。
		policy, perr := guard.NewPolicy(guard.PresetDefault, nil)
		if perr != nil {
			t.Fatal(perr)
		}
		gate, gerr := guard.NewGate(guard.GateOptions{Policy: policy})
		if gerr != nil {
			t.Fatal(gerr)
		}
		return Options{HostFactory: func(engine.BootConfig) (engine.Host, error) {
			return engine.NewFakeHost(engine.FakeScript{}), nil
		}, Sink: &recordingSink{}, Store: store, Meter: &cache.Meter{}, Gate: gate}
	}
	// ① Kernel nil。
	if _, err := NewController(base()); err == nil || !strings.Contains(err.Error(), "kernel context") {
		t.Fatalf("nil kernel err = %v", err)
	}
	// ② ctx 缺 agentloop 键。
	o := base()
	o.Kernel = kernel.NewContext(nil)
	if _, err := NewController(o); err == nil || !strings.Contains(err.Error(), "服务键缺失") {
		t.Fatalf("missing key err = %v", err)
	}
	// ③ 键值类型不符。
	o.Kernel = kernel.NewContext(nil)
	if err := o.Kernel.Provide("agentloop", "not-a-loop"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewController(o); err == nil || !strings.Contains(err.Error(), "类型不符") {
		t.Fatalf("wrong type err = %v", err)
	}
	// ④ 正确键 → 组装成功。
	o.Kernel = kernelCtxWithLoop()
	if _, err := NewController(o); err != nil {
		t.Fatalf("valid ctx err = %v", err)
	}
}
```

(import 以 lifecycle_test.go 现状为准补 `kernel/agentloop/guard/cache/strings`——Gate 构造形态若与现 helper 不符,参照 base() 在 newTestControllerCapturingBoot 中的既有装配逐字段对齐,不新造。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run TestNewControllerRequiresKernelLoop -count=1`
Expected: FAIL(`unknown field Kernel`——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`controller.go`:

1. Options:`Loop   agentloop.Loop` 行删除;增:

```go
	// Kernel 是根 Context(021 FR-102):loop 的权威真源——NewController
	// 装配期解析 "agentloop" 键(键即契约,缺键/类型不符 fail-closed)。
	Kernel *kernel.Context
```

2. NewController:require 检查后追加解析(见设计 §2 代码;`c := &Controller{... loop: loop, ...}` 增字段)。

3. Controller struct 增 `loop agentloop.Loop`;pump 改:

```go
	loop := c.loop // kernel 权威解析产物(021 FR-102;装配期 fail-closed 保证非 nil)
	_ = loop.Run(context.Background(), src, controllerCallbacks{c})
```

(原 `c.opts.Loop`/nil→Default 兜底删除。)

`internal/cli/session.go`:**不在 238 字面量处插**(root 是该调用的返回值,作用域不存在——评审 Important 5);在 assembleKernelContext 内部、`o.KernelKeys = root.Keys()` 同位回填:

```go
	// loop 权威 ctx 回填(021 FR-102):control 经此装配期解析 agentloop
	// 键(主会话与子会话各自装配——单点回填两路覆盖)。
	o.Kernel = root
```

(子会话继承机制=assembleKernelContext 单点各自装配回填,非 Options 副本传递——评审 Minor 6 措辞修正;subagentSessionFactory o2:=o 在 session.go:414 与本机制无耦合。)

测试面注入(评审 Important 4 完整清单——**8 调用点逐处**):`newTestControllerCapturingBoot`(lifecycle_test.go:25)构造 Options 处增 `Kernel: kernelCtxWithLoop(),`;`controller_test.go` 的 newTestController(84)/newTestControllerWithRules(151)/newTestControllerAttended(194)三 helper 各自构造点 + 474 直调点逐处增;e2e_test.go:113/325、subagent_wiring_test.go:482 直调点逐处增(两文件补 kernel import;e2e 自建最小 loop ctx)。

- [ ] **Step 4: 跑测试验证通过 + 退役零残留 + 全量**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers`
Expected: 全绿/干净/0 违规(任何测试漏注入会 fail-closed 红——全量绿即注入完整证明)。

Run(退役钉): `grep -rn "opts.Loop\|Loop:.*agentloop\|Options.Loop" internal/ cmd/ | grep -v Kernel || echo "T2 PASS: Options.Loop 零残留"`
Expected: `T2 PASS: Options.Loop 零残留`。

- [ ] **Step 5: Commit**

```bash
git add internal/control/ internal/cli/ internal/e2e/
git commit -m "feat(control,cli): loop kernel 权威解析——装配期 fail-closed+Options.Loop 退役(021 FR-102/103;T2)"
```

---

## Task 3: 全矩阵回归 + SC 逐项

**Files:**
- 证据:`docs/evidence/021-loop-plugin-regression.md`(Create)

**Interfaces:**
- Consumes: Task 1/2 全部产出。
- Produces: SC-001..005 逐项判定 + 全矩阵回归证据。

- [ ] **Step 1: SC 逐项核验**

```bash
go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers
grep -rn "opts.Loop\|Options.Loop" internal/ cmd/ | grep -v Kernel || echo "SC-004 PASS"
```

Expected: 全绿/0 违规/零残留。SC-001..003 由 T1/T2 测试钉承载;SC-003 行为零变更=全矩阵(含 e2e happy-turn/delegation/PTC)绿。

- [ ] **Step 2: 证据落盘**

写 `docs/evidence/021-loop-plugin-regression.md`:门禁命令+退出码、负向双钉摘录、8 调用点注入清单、SC 逐项表。

- [ ] **Step 3: Commit**

```bash
git add docs/evidence/021-loop-plugin-regression.md
git commit -m "test(evidence): P6 loop 插件化回归证据(021 FR-104/105;T3)"
```
