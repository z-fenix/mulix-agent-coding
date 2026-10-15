# P5c PTC 模式 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** goja 沙箱执行环境(internal/ptc)+ ptc_execute 工具注册(standard profile only)——模型写 JS 脚本组合多步工具调用。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/015-p5c-ptc/specs/2026-10-12-p5c-design.md`)①`internal/ptc`:goja.Runtime 封装(注册工具暴露为 JS 全局函数、超时中断、安全白名单);②`ptc_execute` registry.Tool 实现(standard profile only;参数=脚本文本;治理回路照旧)。

**Tech Stack:** Go 标准库 + goja(github.com/dop251/goja,MIT,纯 Go 零 CGO ✓;经批准的第二个第三方依赖)。

**Spec:** `docs/specs/015-p5c-ptc/spec.md`

**Design:** `.mulix/.runtime/015-p5c-ptc/specs/2026-10-12-p5c-design.md`

## Global Constraints

- FR-103 沙箱安全:无 require/fetch/fs/network 逃逸;全局对象白名单。
- FR-104 超时 30s 缺省(fail-closed)。
- FR-106 零第三方新增(除 goja 已批准)。
- FR-107 分层矩阵登记 `internal/ptc: {internal/registry}`。
- 每任务 `go test ./... -count=1` 全绿再 commit。

## Review Focus

- 沙箱安全:require/fetch/fs/network 逃逸拒绝(测试钉)。
- 超时 fail-closed:30s 缺省,超时中断+错误返回。
- profile 接线:standard 注册/minimal 不注册。
- 既有矩阵零回归。

---

## Task 1: goja 依赖 + internal/ptc 沙箱

**Files:**
- Modify: `go.mod`(go get goja)
- Create: `internal/ptc/ptc.go`
- Modify: `internal/lint/rules.go`(矩阵登记)
- Test: `internal/ptc/ptc_test.go`(Create)

**Interfaces:**
- Produces: `type Sandbox struct{}` + `NewSandbox(tools map[string]registry.Tool, timeout time.Duration) *Sandbox` + `(s *Sandbox) Execute(script string) (string, error)`(注册工具暴露为 JS 全局函数;超时中断;安全白名单)。

- [ ] **Step 1: 写失败测试**

Create `internal/ptc/ptc_test.go`:

```go
package ptc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mulix/internal/config"
	"sync"

	"mulix/internal/registry"
)

type stubTool struct {
	name, result string
}

func (s *stubTool) Name() string                        { return s.name }
func (s *stubTool) Description() string                 { return "stub" }
func (s *stubTool) Schema() json.RawMessage             { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) ReadOnly() bool                      { return true }
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return s.result, nil
}

// TestSandboxToolCall 钉 015 FR-102:注册工具暴露为 JS 函数——脚本调用
// 工具并获得结果。
func TestSandboxToolCall(t *testing.T) {
	tools := map[string]registry.Tool{
		"echo": &stubTool{name: "echo", result: "hello from echo"},
	}
	s := NewSandbox(tools, 5*time.Second)
	result, err := s.Execute(`echo({message: "hello"})`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(result, "hello from echo") {
		t.Fatalf("result = %q, want echo result", result)
	}
}

// TestSandboxEscapeRejected 钉 015 FR-103:沙箱安全——require/fetch/fs/
// network 逃逸拒绝(fail-closed)。
func TestSandboxEscapeRejected(t *testing.T) {
	s := NewSandbox(nil, 5*time.Second)
	escapes := []string{
		`require("fs")`,
		`fetch("http://evil.com")`,
		`globalThis.process.exit(0)`,
		`import("fs")`,
	}
	for _, script := range escapes {
		_, err := s.Execute(script)
		if err == nil {
			t.Fatalf("escape %q must fail-closed", script)
		}
	}
}

// TestSandboxTimeout 钉 015 FR-104:超时 fail-closed(30s 缺省;测试用短超时)。
func TestSandboxTimeout(t *testing.T) {
	s := NewSandbox(nil, 100*time.Millisecond)
	_, err := s.Execute(`while(true) {}`)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout err = %v, want timeout", err)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/ptc/ -count=1`
Expected: FAIL(包未存在——编译期 RED,新包 TDD 形态,台账记 Ruling)。

- [ ] **Step 3: 实现**

```bash
go get github.com/dop251/goja@latest
```

Create `internal/ptc/ptc.go`:

```go
// Package ptc 提供程序化工具调用(PTC)沙箱(015 FR-101..103):
// goja.Runtime 封装——注册工具暴露为 JS 全局函数;超时中断;安全白名单
// (无 require/fetch/fs/network 逃逸)。
package ptc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"mulix/internal/registry"
)

// Sandbox 封装 goja.Runtime:注册工具暴露为 JS 全局函数。
type Sandbox struct {
	tools   map[string]registry.Tool
	timeout time.Duration
}

// NewSandbox 创建沙箱(tools=暴露给脚本的工具子集;timeout=执行上限)。
func NewSandbox(tools map[string]registry.Tool, timeout time.Duration) *Sandbox {
	return &Sandbox{tools: tools, timeout: timeout}
}

// Execute 在沙箱中运行脚本;注册工具暴露为 JS 全局函数(名称→函数,参数=
// JS 对象→json→Tool args);超时经 Runtime.Interrupt 中断;安全白名单:
// 不注册 require/fetch/fs/network(模型不可信输入——沙箱是安全边界)。
func (s *Sandbox) Execute(script string) (string, error) {
	rt := goja.New()
	// 超时中断。
	timer := time.AfterFunc(s.timeout, func() {
		rt.Interrupt("timeout")
	})
	defer timer.Stop()

	// 注册工具为 JS 全局函数。
	for name, tool := range s.tools {
		t := tool // 闭包捕获
		fn := func(call goja.FunctionCall) goja.Value {
			argsJSON, _ := json.Marshal(call.Argument(0).Export())
			result, err := t.Execute(context.Background(), argsJSON)
			if err != nil {
				panic(rt.ToValue(err.Error()))
			}
			return rt.ToValue(result)
		}
		rt.Set(name, fn)
	}

	// 执行。
	result, err := rt.RunString(script)
	if err != nil {
		if goja.IsInterrupted(err) {
			return "", fmt.Errorf("ptc: script timeout after %v", s.timeout)
		}
		return "", fmt.Errorf("ptc: script error: %w", err)
	}
	return result.String(), nil
}
```

`internal/lint/rules.go` layerMatrix 增:`"internal/ptc": {"internal/registry"}`。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/ptc/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/ptc/ internal/lint/rules.go
git commit -m "feat(ptc): goja 沙箱——工具暴露+超时+安全白名单(015 FR-101..103;P5c T1)"
```

---

## Task 2: ptc_execute 工具注册 + profile 接线

**Files:**
- Create: `internal/ptc/execute.go`(PTCExecuteTool)
- Modify: `internal/cli/session.go`(standard profile 时注册 ptc_execute)
- Test: `internal/ptc/ptc_test.go`(追加)
- (SC-003 profile 接线断言由 Task 3 承载——本任务只交付沙箱+工具实现)

**Interfaces:**
- Consumes: Task 1 Sandbox;014 ProfileMode;既有 registry.Tool 接口。
- Produces: `type PTCExecuteTool struct{}` + `NewPTCExecuteTool(tools func() map[string]registry.Tool, timeout time.Duration) *PTCExecuteTool`(registry.Tool 实现——Name()="ptc_execute";Execute = Sandbox 运行脚本);standard profile 注册。

- [ ] **Step 1: 写失败测试**

追加到 `internal/ptc/ptc_test.go`:

```go
// TestPTCExecuteTool 钉 015 FR-104:ptc_execute 工具——脚本文本→沙箱执行。
func TestPTCExecuteTool(t *testing.T) {
	tools := map[string]registry.Tool{
		"echo": &stubTool{name: "echo", result: "executed"},
	}
	tool := NewPTCExecuteTool(func() map[string]registry.Tool { return tools }, 5*time.Second)
	if tool.Name() != "ptc_execute" {
		t.Fatalf("name = %q", tool.Name())
	}
	args := json.RawMessage(`{"script": "echo({message: 'test'})"}`)
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(result, "executed") {
		t.Fatalf("result = %q", result)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/ptc/ -run TestPTCExecuteTool -count=1`
Expected: FAIL(NewPTCExecuteTool 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/ptc/execute.go`:

```go
// PTCExecuteTool 实现 registry.Tool:模型提交 PTC 脚本(参数=脚本文本)→
// Sandbox 执行 → 结果返回(治理回路照旧)。standard profile 注册。
package ptc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mulix/internal/registry"
)

// PTCExecuteTool 实现 registry.Tool。
type PTCExecuteTool struct {
	tools   func() map[string]registry.Tool
	timeout time.Duration
}

// NewPTCExecuteTool 创建(tools=延迟取工具快照——组合根注册完后调用)。
func NewPTCExecuteTool(tools func() map[string]registry.Tool, timeout time.Duration) *PTCExecuteTool {
	return &PTCExecuteTool{tools: tools, timeout: timeout}
}

func (t *PTCExecuteTool) Name() string        { return "ptc_execute" }
func (t *PTCExecuteTool) Description() string { return "执行 PTC 脚本组合多步工具调用" }
func (t *PTCExecuteTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"script":{"type":"string","description":"JS 脚本(调用注册工具组合多步操作)"}},"required":["script"]}`)
}
func (t *PTCExecuteTool) ReadOnly() bool { return false }

func (t *PTCExecuteTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("ptc: parse args: %w", err)
	}
	s := NewSandbox(t.tools(), t.timeout)
	return s.Execute(params.Script)
}
```

`internal/cli/session.go` `assembleController` 内 standard profile 注册(插入点=`assembleController` 内 `Registry:` 字段之前——NewServer 冻结前):

```go
	// PTC(015 FR-104):standard profile 注册 ptc_execute(minimal 不注册)。
	// sync.Once 防重复注册(Register 全局 panic;主会话+子会话各调一次)。
	// profile 为进程不变量(子会话继承)——Once 内守卫是安全位置。
	// var ptcOnce sync.Once 已在包级声明(见 assembleController 外)。
	ptcOnce.Do(func() {
		if cfg.Profile.Normalized() != config.ProfileStandard {
			return // minimal:不注册(FR-104;ToolFilter 在 NewServer 层已排除)
		}
		snapshot := registry.Snapshot()
		toolMap := make(map[string]registry.Tool, len(snapshot))
		for _, t := range snapshot {
			toolMap[t.Name()] = t
		}
		registry.Register(ptc.NewPTCExecuteTool(
			func() map[string]registry.Tool { return toolMap },
			30*time.Second,
		))
	})
```

(注:①`registry.Register` 是全局注册——注册期冻结(newRegistryServer 的 NewServer)之前调用;②minimal profile 不注册——薄环境评测不需要;③`mulix/internal/ptc` 与 `time` import 补入。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/ptc/ ./internal/cli/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/ptc/ internal/cli/
git commit -m "feat(ptc,cli): ptc_execute 工具+profile 接线(015 FR-104;P5c T2)"
```

---

## Task 3: 全矩阵回归 + SC-003 断言 + RegisterPTCExecute testable seam

**Files:**
- Modify: `internal/ptc/ptc.go`(RegisterPTCExecute testable seam)
- Modify: `internal/cli/session.go`(assembleController 改调 testable seam)
- Test: `internal/ptc/ptc_test.go`(追加)

**Interfaces:**
- Consumes: Task 1 Sandbox;Task 2 PTCExecuteTool;014 ProfileMode。
- Produces: `func RegisterPTCExecute(standard bool, once *sync.Once, reg func(registry.Tool))`——profile 守卫 + Snapshot 转换 + reg 回调(012 FR-104;assembleController 调用点;测试传 fresh Once+observer 驱动双 profile)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/ptc/ptc_test.go`(import 补 `sync`):

```go
// TestRegisterPTCExecuteProfileWiring 钉 015 FR-104(SC-003):standard
// 注册 / minimal 不注册——testable seam 驱动双 profile(fresh Once)。
func TestRegisterPTCExecuteProfileWiring(t *testing.T) {
	registered := []string{}
	reg := func(t registry.Tool) { registered = append(registered, t.Name()) }

	// minimal:fresh Once → 不注册。
	RegisterPTCExecute(false, &sync.Once{}, reg)
	if len(registered) != 0 {
		t.Fatalf("minimal registered = %v, want empty", registered)
	}

	// standard:fresh Once → 注册。
	once := &sync.Once{}
	RegisterPTCExecute(true, once, reg)
	if len(registered) != 1 || registered[0] != "ptc_execute" {
		t.Fatalf("standard registered = %v, want [ptc_execute]", registered)
	}

	// Once 语义:同一 Once 第二次调用 → 不重复注册。
	RegisterPTCExecute(true, once, reg)
	if len(registered) != 1 {
		t.Fatalf("second call on same Once = %v, want no change (Once 语义)", registered)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/ptc/ -run TestRegisterPTCExecute -count=1`
Expected: FAIL(RegisterPTCExecute 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/ptc/ptc.go` 追加(import 补 `sync`):

```go
// RegisterPTCExecute 是 ptc_execute 注册的 testable seam:profile 守卫 +
// Snapshot 转换 + reg 回调(assembleController 调用点;测试可传自定义
// once+reg 驱动双 profile)。profile 为进程不变量——Once 内守卫是安全位置。
func RegisterPTCExecute(standard bool, once *sync.Once, reg func(registry.Tool)) {
	once.Do(func() {
		if !standard {
			return // minimal:不注册
		}
		snapshot := registry.Snapshot()
		toolMap := make(map[string]registry.Tool, len(snapshot))
		for _, t := range snapshot {
			toolMap[t.Name()] = t
		}
		reg(ptc.NewPTCExecuteTool(
			func() map[string]registry.Tool { return toolMap },
			30*time.Second,
		))
	})
}
```

`internal/cli/session.go` `assembleController` 内改调 testable seam(替换原 Once 内联块;保留包级 `var ptcOnce`——Once 传入 seam):

```go
	ptc.RegisterPTCExecute(cfg.Profile.Normalized() == config.ProfileStandard, &ptcOnce, registry.Register)
```

(注:①`var ptcOnce sync.Once` 在 assembleController 外声明为包级变量——`&ptcOnce` 传入 seam;②`registry.Register` 为 void(panic 而非 error),reg 回调签名一致;③`"sync"` import 补入 ptc.go(无需 config——standard bool 参数);④session.go 已有 `fmt`/`sync` import——无需补。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/ptc/ ./internal/cli/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/ptc/ internal/cli/
git commit -m "feat(ptc,cli): RegisterPTCExecute testable seam——profile 接线(015 FR-104;P5c T3)"
```
