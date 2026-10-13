# P5d tool-cordis 自指工具 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `kernel_inspect`(只读巡检)+ `cordis_run`(goja 沙箱 JS 全量执行)两个自指工具注册到引擎可见面(standard profile only);cordis_define/undefine/stop 不实现(治理红线);P5a..P5c deferred 台账清偿。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/016-p5d-tool-cordis/specs/2026-10-12-p5d-design.md`)①`kernel.Context.Keys()` 读面(排序快照,链上去重);②新包 `internal/tools/mulixkernel`:KernelInspectTool(服务键/profile/工具面 JSON)+ CordisRunTool(复用 P5c `ptc.Sandbox`,JS 语言全量,安全同 P5c);③serve 子进程接线——引擎工具面 = serve 进程(015 终审 Critical 先例),前台服务键经 `MULIX_KERNEL_KEYS` Boot env 桥注入(MULIX_SUBAGENT_CALLBACK 同款裁定)。

**Tech Stack:** Go 标准库 + goja(既有,无新增第三方)。

**Spec:** `docs/specs/016-p5d-tool-cordis/spec.md`

**Design:** `.mulix/.runtime/016-p5d-tool-cordis/specs/2026-10-12-p5d-design.md`

## Global Constraints

- FR-103 / SC-003:cordis_define/undefine/stop **不实现**——源码 grep 零命中 + 注册面零命中(双钉)。
- FR-104:自指工具走既有治理回路(guard/审批/镜像),不新增治理形态;kernel_inspect ReadOnly=true(015 终审先例:只读工具无审批需求);cordis_run ReadOnly=false。
- FR-106 / SC-004:零第三方新增;每任务 `go test ./... -count=1` 全绿再 commit。
- 分层矩阵登记 `"internal/tools/mulixkernel": {"internal/config", "internal/ptc", "internal/registry"}`(config 为 leaf;ptc→registry 既有;kernel 键经 []string 注入——mulixkernel 不 import kernel)。
- kernel 包只加读面(`Keys`),Provide/Mount/Dispose 语义不动——**运行时改框架 = 治理红线,工具与内核读面都不得扩写面**。
- 注册时序红线(015 终审 Critical):一切注册在 `ServeMCP` 的 `registry.NewServer` 冻结**之前**。

## Review Focus

- 跨进程数据源:服务键的真身在**前台**进程(assembleKernelContext),而引擎工具面在 **serve 子进程**——Keys 经 `MULIX_KERNEL_KEYS` Boot env 桥;env 缺席(独立/测试运行)必须降级非阻断(工具返回空 services,不 fail serve 启动)——测试钉。
- `MULIX_KERNEL_KEYS` 注入门控与先例一致:`hasMulixRegistryEntry()` + 绝不入 mcp-config(SC-003 字节稳定)。
- cordis_run 逃逸矩阵复用 P5c 钉:require/fetch/process.exit/import 拒绝 + 超时 fail-closed(30s 缺省)。
- SC-003 双钉:注册面 seam 测试(standard 只注册 kernel_inspect/cordis_run)+ 源码 grep 零命中门禁。
- minimal profile 两工具都不注册(standard-only;014 ToolFilter 照旧不动)。
- FR-101 的「事件总线订阅数」:serve 进程无总线桥(前台键不跨进程暴露,015 同款裁定)——JSON 字段如实恒 0,实现期 Ruling 入账(见 Task 2 注)。

---

## Task 1: kernel.Context.Keys() 读面

**Files:**
- Modify: `internal/kernel/kernel.go`(Service 方法后追加 Keys)
- Test: `internal/kernel/kernel_test.go`(追加)

**Interfaces:**
- Produces: `func (c *Context) Keys() []string`——沿自身→祖先链收集服务键(子遮蔽父:同键去重,子优先),排序返回。后续 Task 2/3 消费。

- [ ] **Step 1: 写失败测试**

追加到 `internal/kernel/kernel_test.go`:

```go
// TestContextKeys 钉 016 FR-101 前置:Keys 读面——链上收集、子遮蔽父去重、
// 排序返回(巡检语义:子作用域覆盖语义与 Service 一致)。
func TestContextKeys(t *testing.T) {
	root := NewContext(nil)
	if err := root.Provide("sink", 1); err != nil {
		t.Fatal(err)
	}
	if err := root.Provide("meter", 2); err != nil {
		t.Fatal(err)
	}
	child := NewContext(root)
	if err := child.Provide("sink", 3); err != nil { // 遮蔽父同名键
		t.Fatal(err)
	}
	if err := child.Provide("gate", 4); err != nil {
		t.Fatal(err)
	}

	// 根:仅自身键,排序。
	got := root.Keys()
	if len(got) != 2 || got[0] != "meter" || got[1] != "sink" {
		t.Fatalf("root.Keys() = %v, want [meter sink]", got)
	}
	// 子:链上收集,同键去重(3 键,不是 4)。
	got = child.Keys()
	if len(got) != 3 || got[0] != "gate" || got[1] != "meter" || got[2] != "sink" {
		t.Fatalf("child.Keys() = %v, want [gate meter sink]", got)
	}
	// 空作用域:空切片非 nil。
	empty := NewContext(nil)
	if ks := empty.Keys(); ks == nil || len(ks) != 0 {
		t.Fatalf("empty.Keys() = %v, want empty non-nil", ks)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/kernel/ -run TestContextKeys -count=1`
Expected: FAIL(`c.Keys undefined`——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/kernel/kernel.go` Service 方法后追加(import 补 `sort`):

```go
// Keys 返回已注册服务键的只读快照:沿自身→祖先链收集,同键去重(子遮蔽
// 父,与 Service 查找语义一致),排序返回。巡检读面(016 FR-101 前置)——
// 仅读,不扩写面(治理红线:自指工具不得修改运行时)。
func (c *Context) Keys() []string {
	c.mu.Lock()
	keys := make([]string, 0, len(c.services))
	for name := range c.services {
		keys = append(keys, name)
	}
	c.mu.Unlock()
	if c.parent != nil {
		for _, k := range c.parent.Keys() {
			dup := false
			for _, have := range keys {
				if have == k {
					dup = true
					break
				}
			}
			if !dup {
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/kernel/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/
git commit -m "feat(kernel): Context.Keys() 巡检读面——链上收集去重排序(016 FR-101 前置;P5d T1)"
```

---

## Task 2: internal/tools/mulixkernel——KernelInspectTool + CordisRunTool

**Files:**
- Create: `internal/tools/mulixkernel/inspect.go`
- Create: `internal/tools/mulixkernel/cordis.go`
- Create: `internal/tools/mulixkernel/mulixkernel_test.go`
- Modify: `internal/lint/rules.go`(矩阵登记)

**Interfaces:**
- Consumes: Task 1 `Context.Keys()`(经 keys 参数注入,见 Task 3 桥);P5c `ptc.NewSandbox(tools, timeout) / Execute(script)`;既有 `registry.Tool` 接口(Name/Description/Schema/ReadOnly/Execute)。
- Produces:
  - `func NewKernelInspectTool(keys []string, profile config.ProfileMode, tools func() map[string]registry.Tool) *KernelInspectTool`——Execute 返回 JSON:`{"services":[...],"profile":"standard","tools":[...],"ptc_registered":bool,"eventbus_subscriptions":0}`。
  - `func NewCordisRunTool(tools func() map[string]registry.Tool, timeout time.Duration) *CordisRunTool`——参数 `{"code":"..."}`,Execute = `ptc.NewSandbox(tools(), timeout).Execute(code)`(JS 语言全量+注册工具面,安全白名单同 P5c)。
  - `func RegisterSelfRefTools(standard bool, keys []string, profile config.ProfileMode, tools func() map[string]registry.Tool, reg func(registry.Tool))`——Task 3 消费的注册 seam(standard 守卫 + 顺序注册 kernel_inspect→cordis_run)。

- [ ] **Step 1: 写失败测试**

Create `internal/tools/mulixkernel/mulixkernel_test.go`:

```go
package mulixkernel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mulix/internal/config"
	"mulix/internal/registry"
)

type stubTool struct {
	name, result string
}

func (s *stubTool) Name() string            { return s.name }
func (s *stubTool) Description() string     { return "stub" }
func (s *stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) ReadOnly() bool          { return true }
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return s.result, nil
}

func toolsFunc() map[string]registry.Tool {
	return map[string]registry.Tool{
		"ptc_execute": &stubTool{name: "ptc_execute", result: "x"},
		"echo":        &stubTool{name: "echo", result: "y"},
	}
}

// TestKernelInspectTool 钉 016 FR-101/SC-001:巡检 JSON——服务键、profile、
// 工具面、ptc_registered(快照活在 Execute 时)。
func TestKernelInspectTool(t *testing.T) {
	tool := NewKernelInspectTool(
		[]string{"profile", "registry", "meter"},
		config.ProfileStandard,
		toolsFunc,
	)
	if tool.Name() != "kernel_inspect" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !tool.ReadOnly() {
		t.Fatal("kernel_inspect 必须只读(015 终审先例:只读工具无审批需求)")
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rep struct {
		Services              []string `json:"services"`
		Profile               string   `json:"profile"`
		Tools                 []string `json:"tools"`
		PtcRegistered         bool     `json:"ptc_registered"`
		EventbusSubscriptions int      `json:"eventbus_subscriptions"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v (out=%s)", err, out)
	}
	if len(rep.Services) != 3 || rep.Services[0] != "meter" {
		t.Fatalf("services = %v", rep.Services)
	}
	if rep.Profile != "standard" {
		t.Fatalf("profile = %q", rep.Profile)
	}
	if !rep.PtcRegistered {
		t.Fatal("ptc_registered = false, want true")
	}
	if rep.EventbusSubscriptions != 0 {
		t.Fatalf("eventbus_subscriptions = %d, want 0(serve 进程无桥——前台键不跨进程)", rep.EventbusSubscriptions)
	}
	// env 缺席形态(独立运行):keys 空——降级非阻断。
	empty := NewKernelInspectTool(nil, config.ProfileStandard, toolsFunc)
	out, err = empty.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, `"services":[]`) {
		t.Fatalf("degraded out = %q err = %v, want services=[] 非阻断", out, err)
	}
}

// TestCordisRunTool 钉 016 FR-102/SC-002:JS 全量执行+工具暴露+逃逸拒绝
// +超时 fail-closed(安全矩阵复用 P5c)。
func TestCordisRunTool(t *testing.T) {
	tool := NewCordisRunTool(toolsFunc, 5*time.Second)
	if tool.Name() != "cordis_run" {
		t.Fatalf("name = %q", tool.Name())
	}
	// JS 全量:纯计算(不限工具调用)。
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"code": "[1,2,3].map(x => x * 2).join(',')"}`))
	if err != nil || !strings.Contains(out, "2,4,6") {
		t.Fatalf("pure js out = %q err = %v", out, err)
	}
	// 工具暴露(同 P5c 沙箱路径)。
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"code": "echo({})"}`))
	if err != nil || !strings.Contains(out, "y") {
		t.Fatalf("tool call out = %q err = %v", out, err)
	}
	// 逃逸 fail-closed(复用 P5c 矩阵)。
	for _, code := range []string{
		`require("fs")`,
		`fetch("http://evil.com")`,
		`globalThis.process.exit(0)`,
		`import("fs")`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"code":`+quote(code)+`}`)); err == nil {
			t.Fatalf("escape %q must fail-closed", code)
		}
	}
	// 超时 fail-closed(测试用短超时;toolsFunc 非 nil——func 求值纪律)。
	tiny := NewCordisRunTool(toolsFunc, 100*time.Millisecond)
	if _, err := tiny.Execute(context.Background(), json.RawMessage(`{"code": "while(true) {}"}`)); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout err = %v, want timeout", err)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestRegisterSelfRefTools 钉 016 FR-103/SC-003 前半:注册面双钉——
// standard 只注册 kernel_inspect/cordis_run;minimal 不注册。
func TestRegisterSelfRefTools(t *testing.T) {
	var registered []string
	reg := func(t registry.Tool) { registered = append(registered, t.Name()) }

	RegisterSelfRefTools(false, nil, config.ProfileMinimal, toolsFunc, reg)
	if len(registered) != 0 {
		t.Fatalf("minimal registered = %v, want empty", registered)
	}
	RegisterSelfRefTools(true, []string{"profile"}, config.ProfileStandard, toolsFunc, reg)
	if len(registered) != 2 || registered[0] != "kernel_inspect" || registered[1] != "cordis_run" {
		t.Fatalf("standard registered = %v, want [kernel_inspect cordis_run]", registered)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/tools/mulixkernel/ -count=1`
Expected: FAIL(包未存在——编译期 RED,新包 TDD 形态,台账记 Ruling)。

- [ ] **Step 3: 实现**

Create `internal/tools/mulixkernel/inspect.go`:

```go
// Package mulixkernel 提供 P5d 自指工具(kernel_inspect/cordis_run):
// Agent 对运行时的只读巡检与 goja 沙箱执行。cordis_define/undefine/stop
// 不实现(016 FR-103 治理红线:Agent 不能修改自己的权限/工具面)。
package mulixkernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"mulix/internal/config"
	"mulix/internal/registry"
)

// KernelInspectTool 实现 registry.Tool:只读巡检(016 FR-101)。keys 为
// 前台内核服务键快照(MULIX_KERNEL_KEYS Boot env 桥注入,Task 3);tools
// 延迟取本进程(serve)注册面快照——ptc_registered 在 Execute 时判定。
type KernelInspectTool struct {
	keys    []string
	profile config.ProfileMode
	tools   func() map[string]registry.Tool
}

// NewKernelInspectTool 创建巡检工具(keys 可为 nil——env 缺席降级,非阻断)。
func NewKernelInspectTool(keys []string, profile config.ProfileMode, tools func() map[string]registry.Tool) *KernelInspectTool {
	return &KernelInspectTool{keys: keys, profile: profile, tools: tools}
}

func (t *KernelInspectTool) Name() string        { return "kernel_inspect" }
func (t *KernelInspectTool) Description() string { return "巡检 mulix 运行时:服务键、profile、注册工具面" }
func (t *KernelInspectTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *KernelInspectTool) ReadOnly() bool { return true }

func (t *KernelInspectTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc := append([]string{}, t.keys...)
	sort.Strings(svc)
	snapshot := t.tools()
	toolNames := make([]string, 0, len(snapshot))
	ptcRegistered := false
	for name := range snapshot {
		toolNames = append(toolNames, name)
		if name == "ptc_execute" {
			ptcRegistered = true
		}
	}
	sort.Strings(toolNames)
	// eventbus_subscriptions:serve 进程无总线桥(前台指标不跨进程暴露,
	// 015 同款裁定)——如实恒 0,实现期 Ruling 入账。
	rep := struct {
		Services              []string `json:"services"`
		Profile               string   `json:"profile"`
		Tools                 []string `json:"tools"`
		PtcRegistered         bool     `json:"ptc_registered"`
		EventbusSubscriptions int      `json:"eventbus_subscriptions"`
	}{svc, string(t.profile), toolNames, ptcRegistered, 0}
	b, err := json.Marshal(rep)
	if err != nil {
		return "", fmt.Errorf("kernel_inspect: marshal: %w", err)
	}
	return string(b), nil
}
```

Create `internal/tools/mulixkernel/cordis.go`:

```go
package mulixkernel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mulix/internal/ptc"
	"mulix/internal/registry"
)

// CordisRunTool 实现 registry.Tool:goja 沙箱执行任意 JS(016 FR-102;
// 复用 P5c ptc.Sandbox——JS 语言全量,安全白名单/超时同 P5c fail-closed)。
type CordisRunTool struct {
	tools   func() map[string]registry.Tool
	timeout time.Duration
}

// NewCordisRunTool 创建(tools=注册工具面快照函数;timeout=执行上限,30s 缺省)。
func NewCordisRunTool(tools func() map[string]registry.Tool, timeout time.Duration) *CordisRunTool {
	return &CordisRunTool{tools: tools, timeout: timeout}
}

func (t *CordisRunTool) Name() string        { return "cordis_run" }
func (t *CordisRunTool) Description() string { return "在 fail-closed 沙箱中执行 JS 代码(全量语言;可调用注册工具)" }
func (t *CordisRunTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"JS 代码(沙箱内执行;可调用注册工具函数)"}},"required":["code"]}`)
}
func (t *CordisRunTool) ReadOnly() bool { return false }

func (t *CordisRunTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("cordis_run: parse args: %w", err)
	}
	s := ptc.NewSandbox(t.tools(), t.timeout) // func 求值——015 execute.go:39 同款
	return s.Execute(params.Code)
}
```

`RegisterSelfRefTools` 追加到 `inspect.go`(import 补 `time`):

```go
// RegisterSelfRefTools 是自指工具注册的 testable seam(015 RegisterPTCExecute
// 同款形态):standard 守卫 + 顺序注册 kernel_inspect→cordis_run(调用方保证
// 在 NewServer 冻结前、ptc_execute 注册之后调用)。minimal 不注册。
func RegisterSelfRefTools(standard bool, keys []string, profile config.ProfileMode, tools func() map[string]registry.Tool, reg func(registry.Tool)) {
	if !standard {
		return
	}
	reg(NewKernelInspectTool(keys, profile, tools))
	reg(NewCordisRunTool(tools, 30*time.Second))
}
```

`internal/lint/rules.go` layerMatrix 增:`"internal/tools/mulixkernel": {"internal/config", "internal/ptc", "internal/registry"}`。

(注:①`ptc.Sandbox` 内部仅暴露 ReadOnly 工具(ptc.go 既有治理过滤)——cordis_run 暴露的注册工具面即 ReadOnly 面,cordis_run/ptc_execute 自身 ReadOnly=false 不进沙箱,治理自洽;此裁定入 spec Clarifications;②t.tools 为 func,传给 NewSandbox 前必须求值 `t.tools()`。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/tools/mulixkernel/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/tools/mulixkernel/ internal/lint/rules.go
git commit -m "feat(tools): mulixkernel 自指工具——kernel_inspect+cordis_run(016 FR-101/102;P5d T2)"
```

---

## Task 3: serve 接线——MULIX_KERNEL_KEYS Boot env 桥 + 注册面落地 + SC-003 双钉

**Files:**
- Modify: `internal/control/controller.go`(Options 增 KernelKeys;boot.Env 注入 MULIX_KERNEL_KEYS——MULIX_MEMORY_SESSION 块后)
- Modify: `internal/cli/session.go`(assembleKernelContext 同源回填 KernelKeys)
- Modify: `internal/cli/mcpserve.go`(ServeMCP 解析 env + standard 注册两工具——ptc_execute 注册之后、NewServer 冻结之前)
- Test: `internal/control/memory_injection_test.go`(env 桥钉,追加);SC-003 源码零命中走门禁命令(Step 4)

**Interfaces:**
- Consumes: Task 2 `RegisterSelfRefTools`;015 既有 `registry.SnapshotToolMap()`;control.Boot 的 Env 通道(MULIX_SUBAGENT_CALLBACK/MULIX_PERMISSION_CALLBACK/MULIX_MEMORY_SESSION 先例)。
- Produces: `control.Options.KernelKeys []string`(零值=不变,既有调用方零破坏——014 newRegistryServer 先例);env `MULIX_KERNEL_KEYS=<逗号分隔服务键>`(仅 `len>0 && hasMulixRegistryEntry()` 时注入;绝不入 mcp-config)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/control/memory_injection_test.go`(锚:`TestMemorySessionEnvInjection`(305 行)+ helper `newTestControllerCapturingBoot`(318 行)——同装配骨架,同包直用):

```go
// TestBootEnvKernelKeys 钉 016 FR-101 桥:MULIX_KERNEL_KEYS 仅在
// KernelKeys 非空且注册面含 mulix 条目时注入(TestMemorySessionEnvInjection
// 同款裁定:Boot Env 通道,绝不入 mcp-config;缺席=降级非阻断)。
func TestBootEnvKernelKeys(t *testing.T) {
	for _, tc := range []struct {
		name       string
		keys       []string
		wantEnvVar bool
	}{
		{name: "no keys = no env", keys: nil, wantEnvVar: false},
		{name: "keys present = env injected", keys: []string{"profile", "registry"}, wantEnvVar: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := engine.NewFakeHost(engine.FakeScript{})
			sink := &recordingSink{}
			var boot engine.BootConfig
			c := newTestControllerCapturingBoot(t, fake, sink, &boot, func(o *Options) {
				o.KernelKeys = tc.keys
				o.Registry = registry.NewServer(registry.Options{Name: "mulix", Version: "test",
					SelfCommand: []string{"mulix.exe", "mcp", "serve"}})
			})
			if err := c.NewSession(context.Background()); err != nil {
				t.Fatalf("new session: %v", err)
			}
			has := false
			for _, e := range boot.Env {
				if e == "MULIX_KERNEL_KEYS=profile,registry" {
					has = true
				}
			}
			if has != tc.wantEnvVar {
				t.Fatalf("MULIX_KERNEL_KEYS in env = %v, want %v (env=%v)", has, tc.wantEnvVar, boot.Env)
			}
			// env 不入面:mcp-config 与有无内核键无关(SC-003 字节稳定)。
			art, err := cache.Compose(mustDefaultConfig(), "w", c.opts.Registry)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			if boot.MCPConfigJSON != art.MCPConfigJSON {
				t.Fatalf("mcp-config must not carry kernel keys env:\nboot: %s\nbase: %s", boot.MCPConfigJSON, art.MCPConfigJSON)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run TestBootEnvKernelKeys -count=1`
Expected: FAIL(`o.KernelKeys unknown field`——KernelKeys 未定义,编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/control/controller.go`:

1. `Options` 结构体增字段(零值=不变):

```go
	// KernelKeys 是前台内核根 Context 的服务键快照(016 FR-101):经 Boot
	// env 桥 MULIX_KERNEL_KEYS 送达 serve 子进程的 kernel_inspect——服务键
	// 真身在前台,引擎工具面在 serve(015 终审 Critical 同款进程拓扑)。
	KernelKeys []string
```

2. Boot 组装处(MULIX_MEMORY_SESSION 块之后、`composeBoot` 之前,import 确认 `strings`):

```go
	// 内核服务键桥(016 FR-101):MULIX_KERNEL_KEYS——Boot Env 通道,绝不入
	// mcp-config(SC-003 字节稳定,与 MULIX_SUBAGENT_CALLBACK 同一裁定)。
	// 仅注册面含 mulix 条目(存在消费它的 serve 子进程)时注入;缺席=降级
	// 非阻断(kernel_inspect 返回空 services)。
	if len(c.opts.KernelKeys) > 0 && c.hasMulixRegistryEntry() {
		boot.Env = append(boot.Env, "MULIX_KERNEL_KEYS="+strings.Join(c.opts.KernelKeys, ","))
	}
```

`internal/cli/session.go` `assembleKernelContext` 末尾(return 前)同源回填:

```go
	// 服务键快照回填(016 FR-101):供 control 经 Boot env 桥送达 serve 侧
	// kernel_inspect(Keys 读面——Task 1)。
	o.KernelKeys = root.Keys()
```

`internal/cli/mcpserve.go` `ServeMCP` 内,既有 ptc_execute 注册块之后:

```go
	// 自指工具(016 FR-101/102):standard profile 注册 kernel_inspect/
	// cordis_run——在 ptc_execute 之后(kernel_inspect 的 ptc_registered 于
	// Execute 时从注册面快照判定)、NewServer 冻结之前(015 终审 Critical
	// 红线)。服务键经 MULIX_KERNEL_KEYS Boot env 桥;缺席=空(降级非阻断)。
	var kernelKeys []string
	if v := os.Getenv("MULIX_KERNEL_KEYS"); v != "" {
		kernelKeys = strings.Split(v, ",")
	}
	mulixkernel.RegisterSelfRefTools(
		cfg.Profile.Normalized() == config.ProfileStandard,
		kernelKeys,
		cfg.Profile.Normalized(),
		func() map[string]registry.Tool { return registry.SnapshotToolMap() },
		registry.Register,
	)
```

(import 补 `strings` 与 `mulix/internal/tools/mulixkernel`。)

- [ ] **Step 4: 跑测试验证通过 + 零回归 + SC-003 门禁**

Run: `go test ./internal/control/ ./internal/cli/ ./internal/tools/mulixkernel/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

SC-003 源码零命中门禁(必须逐字跑,期望退出码 1 = 零命中):

```bash
grep -rn "cordis_define\|cordis_undefine\|cordis_stop" internal/ cmd/ || echo "SC-003 PASS: 零命中"
```

Expected: `SC-003 PASS: 零命中`(若命中——红线违规,阻塞,回 Task 2 排查)。

- [ ] **Step 5: Commit**

```bash
git add internal/control/ internal/cli/
git commit -m "feat(control,cli): 自指工具 serve 接线——MULIX_KERNEL_KEYS 桥+SC-003(016 FR-104;P5d T3)"
```

---

## Task 4: FR-105 deferred 清偿 + 全矩阵回归(SC-004)

**Files:**
- 只读扫描 + 台账:`.mulix/.runtime/013-p5a-fork-replay-search/sdd/tasks/progress.md`、`.mulix/.runtime/014-p5b-profiles-standard-minimal/sdd/tasks/progress.md`、`.mulix/.runtime/015-p5c-ptc/sdd/tasks/progress.md`
- 证据:`docs/evidence/016-p5d-regression.md`(Create)

**Interfaces:**
- Consumes: Task 1..3 全部产出。
- Produces: deferred 清偿记录(逐项核销或「无未清项」如实入账)+ 全矩阵回归证据。

- [ ] **Step 1: deferred 台账扫描(FR-105)**

逐条读 013/014/015 三个 ledger,提取全部 deferred/set-aside/路由项,逐项裁定:
- 可核销 → 当场修复(TDD:先失败测试后实现,同纪律);
- 已被他变更清偿 → 记「核销依据」;
- 仍应 deferred → 记路由(P6+ / 不修),一行「决定 — 理由 — 错了代价」。

已知基线(编排者预读,实现者必须复核):013 T6 deferred batch 已清偿(Task 6 complete);014 Minor(lastGood 顺序)终审修复轮已闭环;015 终审 Critical/Important 已修复轮闭环、set-aside 三项已由 executor 裁定入账。预期「无未清项」——若复核发现新未清项,如实清偿或路由,不隐瞒。

- [ ] **Step 2: 全矩阵回归(SC-004)**

```bash
go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers
grep -rn "cordis_define\|cordis_undefine\|cordis_stop" internal/ cmd/ || echo "SC-003 PASS"
```

Expected: 全绿 / 0 违规 / SC-003 零命中。

- [ ] **Step 3: 证据落盘**

写 `docs/evidence/016-p5d-regression.md`:门禁命令+退出码、kernel_inspect/cordis_run 注册断言摘录、deferred 清偿结论、P5d 收官判据(SC-001..004 逐项 PASS/FAIL)。

- [ ] **Step 4: Commit**

```bash
git add docs/evidence/016-p5d-regression.md
git commit -m "test(evidence): P5d deferred 清偿+全矩阵回归证据(016 FR-105/106;P5d T4)"
```
