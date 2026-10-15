# P5b profiles 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Profile 配置解析(3 态)+ minimal 注册面跳过 + ctx.profile 服务键 + e2e 钉——行为零变更(standard 缺省)。

**Architecture:**(architectural,设计文档已批准:`.mulix/.runtime/014-p5b-profiles-standard-minimal/specs/2026-10-12-p5b-design.md`;评审员 Issue 1 修正——**注册面过滤在 `registry.NewServer` 层**(非 init() 层:mulix_usage/permission 经无条件 init() 注册,无 SelfRegister 接缝;NewServer 冻结全局注册表时按 profile 过滤——引擎可见面=过滤后子集);①config 增 ProfileConfig(mode 三态 fail-closed);②NewServer 增 `ToolFilter` option(minimal 时只暴露引擎原生 bash/shell——内置 MCP 工具不进 tools/list);③ctx.profile 服务键;④e2e 钉(helper subprocess——干净全局注册表)。

**Tech Stack:** Go 标准库;既有测试基建(helper subprocess 模式——serve_wiring_test 先例)。

**Spec:** `docs/specs/014-p5b-profiles-standard-minimal/spec.md`

**Design:** `.mulix/.runtime/014-p5b-profiles-standard-minimal/specs/2026-10-12-p5b-design.md`

## Global Constraints

- FR-103 standard 行为零变更;FR-105 全量门禁绿。
- profile 仅影响工具注册面;权限/缓存/镜像治理三层不变。
- EnableEventBus 门控设置位置=**生产命令装配**(RunPrompt/TUI/serve 的 Options 构造,经 withDefaults 后显式置位)——不可放 withDefaults 内(测试同经)。

## Review Focus

- 三态解析(缺省/minimal/非法 fail-closed)。
- minimal 注册面:NewServer 工具过滤后 tools/list 仅含引擎原生工具——mulix 内置工具不出现。
- standard 零变更:既有全矩阵 + serve_wiring_test 的 mulix_usage 基线钉保持。

---

## Task 1: config 解析(3 态)

**Files:**
- Modify: `internal/config/config.go`(ProfileConfig/ProfileMode/解析校验)
- Test: `internal/config/config_test.go`(追加)

**Interfaces:**
- Produces: `type ProfileMode string`(standard|minimal)+ `type ProfileConfig struct { Mode string }` + `(p ProfileConfig) Mode() ProfileMode`(标准化:""→standard)+ Load 末尾校验(非法 fail-closed)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/config/config_test.go`:

```go
// TestProfileModeParsing 钉 014 FR-101:三态(缺省/minimal/非法 fail-closed)。
func TestProfileModeParsing(t *testing.T) {
	// 缺省(无 [profile] 节)→ standard。
	cfg1, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("default load: %v", err)
	}
	if cfg1.Profile.Mode() != ProfileStandard {
		t.Fatalf("default mode = %v, want standard", cfg1.Profile.Mode())
	}
	// minimal(文件路径经 Paths.Project——与 DefaultPaths 文件约定一致)。
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mulix.toml")
	os.WriteFile(cfgPath, []byte("[profile]\nmode = \"minimal\"\n"), 0o644)
	cfg2, _, err := NewLoader().Load(Flags{}, Paths{Project: cfgPath})
	if err != nil {
		t.Fatalf("minimal load: %v", err)
	}
	if cfg2.Profile.Mode() != ProfileMinimal {
		t.Fatalf("minimal mode = %v, want minimal", cfg2.Profile.Mode())
	}
	// 非法 fail-closed(错误含 mode 值)。
	os.WriteFile(cfgPath, []byte("[profile]\nmode = \"bogus\"\n"), 0o644)
	_, _, err = NewLoader().Load(Flags{}, Paths{Project: cfgPath})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("bogus mode err = %v, want fail-closed with mode value", err)
	}
}
```

(注:①`Paths.Project` 为 mulix.toml 文件路径——与 DefaultPaths 的文件约定一致(评审员核实);②`strings`/`filepath`/`os` 按需补 import。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/config/ -run TestProfileModeParsing -count=1`
Expected: FAIL(ProfileConfig/ProfileMode/ProfileStandard/ProfileMinimal 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/config/config.go` 追加:

```go
// ProfileMode 是运行模式(standard=全工具集缺省;minimal=薄环境评测)。
type ProfileMode string

const (
	ProfileStandard ProfileMode = "standard"
	ProfileMinimal  ProfileMode = "minimal"
)

// ProfileConfig 是 [profile] 节(014 FR-101)。
type ProfileConfig struct {
	Mode string `toml:"mode"`
}

// Mode 返回标准化后的运行模式(""→standard;非法在 Load 末尾 fail-closed)。
func (p ProfileConfig) Mode() ProfileMode {
	if p.Mode == string(ProfileMinimal) {
		return ProfileMinimal
	}
	return ProfileStandard
}
```

`Config` 增字段:`Profile ProfileConfig \`toml:"profile"\``。Load 末尾(全部字段装载后):

```go
	// profile mode 校验(014 FR-101:fail-closed,错误含 mode 值)。
	switch m := cfg.Profile.Mode; m {
	case "", string(ProfileStandard), string(ProfileMinimal):
		// 合法("" 缺省已在 Mode() 标准化)。
	default:
		return nil, nil, fmt.Errorf("config: profile.mode %q must be \"standard\" or \"minimal\"", m)
	}
```

(注:cfg.Profile.Mode() 此时已标准化(""→standard)——校验的是原始字符串 p.Mode 而非标准化后值。实现形态:Load 末尾对 `cfg.Profile.Mode` 原始值做 switch 校验。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/config/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): Profile 解析——三态 fail-closed(014 FR-101;P5b T1)"
```

---

## Task 2: registry 工具过滤(NewServer profile option)

**Files:**
- Modify: `internal/registry/registry.go`(NewServer 增 ToolFilter option)
- Test: `internal/registry/registry_test.go`(追加)

**Interfaces:**
- Consumes: 既有 NewServer/Options(评审员确认:NewServer 冻结全局注册表——过滤点在此)。
- Produces: `Options.ToolFilter map[string]bool`(nil=全部;非 nil=仅暴露键集内工具——tools/list 与 tools/call 均过滤)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/registry/registry_test.go`:

```go
// TestNewServerToolFilter 钉 014 FR-102:ToolFilter 非空时仅暴露键集内工具
// (tools/list 经 InMemory transports + ListTools 协议级黑盒——011 同款)。
func TestNewServerToolFilter(t *testing.T) {
	ctx := context.Background()
	resetRegistry(t)
	Register(&stubTool{name: "mulix_usage", desc: "u", schema: `{"type":"object"}`})
	Register(&stubTool{name: "mulix_permission", desc: "p", schema: `{"type":"object"}`})
	// 全暴露(nil filter = standard)。
	sAll := NewServer(Options{Name: "full"})
	cT1, sT1 := mcp.NewInMemoryTransports()
	ss1, err := sAll.mcpServer.Connect(ctx, sT1, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer ss1.Close()
	client1 := mcp.NewClient(&mcp.Implementation{Name: "probe"}, nil)
	cs1, err := client1.Connect(ctx, cT1, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs1.Close()
	res1, err := cs1.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res1.Tools) != 2 {
		t.Fatalf("unfiltered tools = %d, want 2", len(res1.Tools))
	}
	// minimal 过滤(仅 mulix_usage;mulix_permission 不可见)。
	sMin := NewServer(Options{Name: "min", ToolFilter: map[string]bool{"mulix_usage": true}})
	cT2, sT2 := mcp.NewInMemoryTransports()
	ss2, err := sMin.mcpServer.Connect(ctx, sT2, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer ss2.Close()
	client2 := mcp.NewClient(&mcp.Implementation{Name: "probe"}, nil)
	cs2, err := client2.Connect(ctx, cT2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs2.Close()
	res2, err := cs2.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res2.Tools) != 1 || res2.Tools[0].Name != "mulix_usage" {
		t.Fatalf("filtered tools = %v, want [mulix_usage]", res2.Tools)
	}
}
```

(注:①`mcp` 为 go-sdk 包(github.com/modelcontextprotocol/go-sdk/mcp)——registry_test 已 import 或补;②InMemory transports 模式 = 011 协议级黑盒同款;③测试注册的 stubTool 与 011 注册可能竞争——测试串行(Go 同包测试默认串行),且 resetRegistry 清空。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/registry/ -run TestNewServerToolFilter -count=1`
Expected: FAIL(Options.ToolFilter 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/registry/registry.go`:

```go
// Options 增字段:
	// ToolFilter 是工具名→是否暴露的白名单(014 FR-102;nil=全部——
	// standard;非 nil=仅暴露键集内工具——minimal 的引擎可见面子集)。
	ToolFilter map[string]bool
```

`NewServer` 的 `for _, t := range registeredTools()` 循环体头部增过滤:

```go
		if opts.ToolFilter != nil && !opts.ToolFilter[t.Name()] {
			continue // profile 过滤:非白名单工具不进 tools/list
		}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/registry/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/registry/
git commit -m "feat(registry): NewServer ToolFilter——profile 工具面过滤(014 FR-102;P5b T2)"
```

---

## Task 3: minimal 组合根接线 + ctx.profile 服务键

**Files:**
- Modify: `internal/cli/session.go`(`assembleController` 按 profile 过滤 Registry + `assembleKernelContext` 增 profile 键)
- Test: `internal/cli/kernel_assembly_test.go`(扩展)

**Interfaces:**
- Consumes: Task 1 ProfileMode;Task 2 ToolFilter;kernel 服务键模式(011)。
- Produces: `ctx.profile` 键就位(ProfileMode);minimal 时 Registry 构造带 ToolFilter。

- [ ] **Step 1: 写失败测试**

`internal/cli/kernel_assembly_test.go` 的 `TestAssembleKernelContextServiceKeys` 扩展:

```go
	// profile 键(014 FR-104)。
	if v, ok := root.Service("profile"); !ok {
		t.Fatal("profile key missing")
	} else {
		if pm, isMode := v.(config.ProfileMode); !isMode || pm != config.ProfileStandard {
			t.Fatalf("profile = %v, want standard", v)
		}
	}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/cli/ -run TestAssembleKernelContextServiceKeys -count=1`
Expected: FAIL(profile 键未 Provide——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

(a) `assembleController` 内 profile 分支(评审员核实:两个 RegisterConditional 在 newRegistryServer 前;过滤在 NewServer option):

```go
	// profile 分支(014 FR-102):minimal 时 NewServer 带 ToolFilter——
	// 内置 MCP 工具不进 tools/list(引擎原生 bash/shell 由 CLI 自带)。
	profile := cfg.Profile.Mode()
	// selfCmd 沿既有 newRegistryServer 的 selfCommand() 错误回退(空=纯进程内)。
	selfCmd, selfErr := selfCommand()
	if selfErr != nil {
		selfCmd = nil
	}
	regOpts := registry.Options{
		Name:        "mulix",
		SelfCommand: selfCmd,
		ToolFilter:  nil, // standard=全暴露
	}
	if profile == config.ProfileMinimal {
		regOpts.ToolFilter = map[string]bool{
			"permission": true, // 权限回路必需(注册名非 mulix_permission 前缀)
		}
	}
	reg := registry.NewServer(regOpts)
```

(b) `assembleKernelContext` 增 profile 键(agentloop 键 011 已 Provide——非遗漏,勿重复):

```go
	if err := root.Provide("profile", o.Config.Profile.Mode()); err != nil {
		return nil, Options{}, err
	}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/cli/ ./internal/kernel/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): minimal 组合根接线+ctx.profile 键(014 FR-102;P5b T3)"
```

---

## Task 4: e2e 钉(minimal 启动 + 工具面过滤)

**Files:**
- Modify: `internal/cli/commands_test.go`(追加)或 serve_wiring_test.go(helper subprocess 模式——评审员推荐)

**Interfaces:**
- Consumes: Task 1-3。
- Produces: e2e 断言(minimal 启动成功 + tools/list 过滤)。

- [ ] **Step 1: e2e 断言**

经 helper subprocess 模式(评审员推荐——干净全局注册表):

```go
// TestMinimalProfileBoot 钉 014 FR-102(SC-002):minimal profile 启动成功,
// tools/list 仅含权限回路必需工具(无 mulix_usage)。
// 经 helper subprocess——干净全局注册表(进程内 Once 污染规避)。
func TestMinimalProfileBoot(t *testing.T) {
	if os.Getenv("TEST_MINIMAL_PROFILE") == "1" {
		// 子进程:以 minimal mulix.toml 启动 mulix mcp serve(或 run)。
		// 断言:tools/list 仅含白名单工具(无 mulix_usage)。
	}
	// 父进程:构造 minimal mulix.toml → exec self → 子进程断言。
}
```

(最终形态按 serve_wiring_test.go 的 helper subprocess 同款落位——**不可省断言**:tools/list 过滤。)

- [ ] **Step 2: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿

```bash
git add internal/cli/
git commit -m "test(cli): minimal profile e2e 钉——helper subprocess(014 FR-102;P5b T4)"
```

---

## Task 5: 全矩阵回归收口

**Files:**
- (无新代码——收口核验)

- [ ] **Step 1: 全矩阵**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ cmd/ && go run ./cmd/mulix lint layers`
Expected: 20+ 包全绿/0 违规;standard 零变更(既有全矩阵含 serve_wiring mulix_usage 基线)。

- [ ] **Step 2: 台账**

ledger 记录回归结论;P5b 收官判据(profiles 落地 + SC 全 PASS)写入 task 报告。
