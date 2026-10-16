# 引擎工具治理旁路修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 封堵 claude CLI 2.1.30x 对 Bash/PowerShell 只读命令的引擎自主放行（绕开 `mcp__mulix__permission` 审批），并加运行时围堵兜底一切未知放行类。

**Architecture:** 两层——Layer 1 在 Boot 面向引擎注入 `--settings` 的 `permissions.ask=["Bash","PowerShell"]`（实验证实 ask 规则压制引擎只读自动放行且优先于 allow 规则），使 shell 调用全部进入既有 Gate 审批回路；Layer 2 在 Controller 对「运行态收到、未经裁决、非白名单」的 tool_result 判治理违规并终止会话（fail-closed，防引擎漂移引入新放行类）。零新增依赖、零分层矩阵变化。

**Tech Stack:** Go 1.27（禁 -race，CGO_ENABLED=0）+ 既有 internal/cache / internal/engine / internal/control；测试用 FakeHost 注入。

**Spec:** `docs/specs/2026-10-16-engine-tool-governance-fix/spec.md`

**Design:** approved（bounded track；用户裁定原文「两层都做（推荐）」AskUserQuestion 2026-10-16）：`.mulix/.runtime/2026-10-16-engine-tool-governance-fix/specs/2026-10-16-engine-governance-design.md`

## Global Constraints

- TDD 先红后绿；每任务 `go test ./... -count=1` 全绿 + `gofmt -l .` 干净 + `go vet ./...` 干净才 commit；conventional 前缀；只 add 本任务文件；`.mulix/.runtime/**` 不入提交。
- 先持久化后发布：违规留痕必须走 `c.publish`（镜像 Append 成功才 Sink）。
- engine.lock 不修改（cli_version 钉维持 2.1.296，drift 流程不变——用户裁定 2026-10-16）。
- kernel/engine 为 leaf 级：engine 不得 import config（SettingsJSON 以 string 传输）。
- 强制询问面固定常量、不经用户配置（治理红线无旁路开关）。
- 测试断言与注释中界面/断言字符串遵循仓内中文惯例。

## Review Focus

- 允许清单（`config.Permissions.Allow`）里的 shell 条目迁移到 Gate 裁决后**结果不变**：allow 命中 → 放行（Task 4 正例 + Task 5 冒烟对账钉）。
- Esc 取消后的孤儿 tool_result（running=false）不得误杀会话（Task 4 孤儿正例钉；run2 t3 语义）。
- 引擎 deny 后回流的 is_error tool_result 属已裁决调用，不得触发围堵（Task 4 deny 正例钉）。
- Boot 面指纹必须覆盖 SettingsJSON（新增字段漏入 canonical struct = 指纹漂移；Task 1 字段覆盖探针钉）。
- 围堵终止必须异步（泵 goroutine 内同步 `host.Close` 会死锁——exit 事件需泵消费；Task 4 经 engine_closed 合成收口路径验证不悬挂）。

---

## Task 1: cache——Boot 面注入 SettingsJSON 与指纹

**Files:**
- Modify: `internal/cache/boot.go`（BootArtifact 字段、Compose 注入、fingerprint canonical struct）
- Test: `internal/cache/boot_test.go`

**Interfaces:**
- Consumes: 既有 `Compose(cfg config.Config, workspace string, reg Registry) (BootArtifact, error)`、`fingerprint(a BootArtifact) string`。
- Produces: `BootArtifact.SettingsJSON string`——恒为字节 `{"permissions":{"ask":["Bash","PowerShell"]}}`（非空）；`fingerprint` 覆盖该字段（同输入同指纹、异 SettingsJSON 异指纹）。Task 2/3 消费此字段。

- [X] **Step 1: 写失败测试**——在 `internal/cache/boot_test.go` 追加：

```go
// TestComposeInjectsShellAskRules pins：治理常量注入面（字节钉死）——
// 引擎自主放行类（只读/沙箱）经 ask 规则强制走审批回路（2026-10-16 旁路修复）。
func TestComposeInjectsShellAskRules(t *testing.T) {
	artifact, err := Compose(testConfig(), "ws", nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	const want = `{"permissions":{"ask":["Bash","PowerShell"]}}`
	if artifact.SettingsJSON != want {
		t.Fatalf("SettingsJSON = %q, want %q", artifact.SettingsJSON, want)
	}
}

// TestFingerprintCoversSettingsJSON pins：指纹 canonical struct 覆盖
// SettingsJSON——漏字段即指纹漂移（CLAUDE.md 维护警告的字段覆盖探针）。
func TestFingerprintCoversSettingsJSON(t *testing.T) {
	artifact, err := Compose(testConfig(), "ws", nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	f1 := fingerprint(artifact)
	artifact.SettingsJSON = `{"permissions":{"ask":["Bash"]}}`
	if f2 := fingerprint(artifact); f1 == f2 {
		t.Fatal("fingerprint ignores SettingsJSON (drift)")
	}
}
```

（`testConfig()` 用该测试文件既有的 `baseCfg()` helper（boot_test.go:19，Preset="default"）——评审员核定实名，直接使用。）

- [X] **Step 2: 跑测试看它失败**

Run: `go test ./internal/cache/ -run 'TestComposeInjectsShellAskRules|TestFingerprintCoversSettingsJSON' -count=1`
Expected: FAIL——`SettingsJSON = ""` / `fingerprint ignores SettingsJSON`（编译期 RED 则在台账记 Ruling 说明形态）。

- [X] **Step 3: 最小实现**——`internal/cache/boot.go`：

```go
// BootArtifact 追加字段（AllowedTools 之后）：
	// SettingsJSON 是注入引擎的 --settings 文件内容（治理面：permissions.ask
	// 强制 shell 工具走审批回路——引擎只读自动放行类的对冲；固定常量，不经
	// 用户配置）。空面语义与 AllowedTools 同约定：恒非空（治理默认）。
	SettingsJSON string

// governanceAskTools 是强制询问的引擎工具清单（治理常量；Ask 规则优先于
// 引擎 allow 与内置只读放行——2026-10-16 实验 r1/r3，证据见 design §1）。
var governanceAskTools = []string{"Bash", "PowerShell"}

// Compose 内 artifact 初始化处追加：
		SettingsJSON:        composeShellAskRules(),

// 新函数（fingerprint 之前）：
// composeShellAskRules 序列化强制询问面（encoding/json map 键序稳定——字节
// 钉死；SC 同款稳定性口径）。
func composeShellAskRules() string {
	payload, err := json.Marshal(map[string]any{
		"permissions": map[string]any{"ask": governanceAskTools},
	})
	if err != nil {
		// 常量序列化不会失败；防御性兜底（与 fingerprint 同姿态）
		return ""
	}
	return string(payload)
}

// fingerprint 的 canonical struct 追加字段（EngineEnv 之前，按字段序）：
		SettingsJSON       string   `json:"settings_json"`
// 及对应赋值 SettingsJSON: a.SettingsJSON。
```

- [X] **Step 4: 跑测试看它通过**

Run: `go test ./internal/cache/ -count=1`
Expected: PASS（全包绿——既有字节稳定/空面测试不得回归）。

- [X] **Step 5: 全量 + 提交**

Run: `go test ./... -count=1 && gofmt -l . && go vet ./...`
Expected: 全绿/干净。

```bash
git add internal/cache/boot.go internal/cache/boot_test.go
git commit -m "feat(cache): boot 面注入 shell 工具强制询问规则并入指纹(治理旁路修复 L1)"
```

---

## Task 2: engine——BootConfig.SettingsJSON 与 --settings 物化

**Files:**
- Modify: `internal/engine/host.go`（BootConfig 字段）、`internal/engine/claudehost.go`（buildArgs 物化、cleanupTemp）
- Test: `internal/engine/claudehost_test.go`

**Interfaces:**
- Consumes: 既有 `buildArgs(cfg BootConfig) ([]string, string, error)`、`cleanupTemp()`、`h.mcpTemp` 生命周期（Close/spawn 失败路径清理）。
- Produces: `BootConfig.SettingsJSON string`（leaf 级 string 传输，不 import config）；非空 → args 在 `--setting-sources` 对之后追加 `--settings <临时文件路径>`，文件内容 = SettingsJSON，临时文件由 `cleanupTemp` 清理。Task 3 消费。

- [X] **Step 1: 写失败测试**——修改 `TestBuildArgs`：cfg 增加 `SettingsJSON: "{\"permissions\":{\"ask\":[\"Bash\",\"PowerShell\"]}}",`；**buildArgs 签名钉死改为 4 返回值 `(args, mcpTemp, settingsTemp, err)`**（评审员核定：既有两处调用 claudehost_test.go:501/532 的三变量解构与 Start claudehost.go:140 全部同步更新，遗漏即编译错）；`want` 序列在 `"--setting-sources", "user,project",` 之后插入 `"--settings", "<实测路径>",`（比对前从首次调用的 `args` 记录实测路径回填 want，并对两次 buildArgs 物化的临时文件 `defer os.Remove` 防泄漏）；并在函数尾追加物化内容断言：

```go
	// --settings 物化：内容逐字节 = SettingsJSON
	idx = -1
	for i, a := range args2 {
		if a == "--settings" {
			idx = i
		}
	}
	if idx < 0 || idx+1 >= len(args2) {
		t.Fatal("--settings missing")
	}
	data, err = os.ReadFile(args2[idx+1])
	if err != nil || string(data) != cfg.SettingsJSON {
		t.Fatalf("settings temp file wrong: %v %q", err, data)
	}
```

新增独立清理测试：

```go
// TestSettingsTempCleanedOnClose pins：--settings 临时文件随 cleanupTemp 清理
// （与 mcp-config 临时文件同生命周期纪律）。
func TestSettingsTempCleanedOnClose(t *testing.T) {
	h := NewClaudeHost(ClaudeHostOptions{LockPath: testLockPath(t)})
	cfg := BootConfig{
		PermissionMode: "default",
		SettingSources: []string{"user"},
		SettingsJSON:   `{"permissions":{"ask":["Bash"]}}`,
	}
	_, _, settingsTemp, err := h.buildArgs(cfg)
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if _, err := os.Stat(settingsTemp); err != nil {
		t.Fatalf("settings temp missing: %v", err)
	}
	h.cleanupTemp()
	if _, err := os.Stat(settingsTemp); !os.IsNotExist(err) {
		t.Fatalf("settings temp not cleaned: %v", err)
	}
}
```

- [X] **Step 2: 跑测试看它失败**

Run: `go test ./internal/engine/ -run 'TestBuildArgs|TestSettingsTempCleanedOnClose' -count=1`
Expected: FAIL——编译期（BootConfig 无 SettingsJSON 字段）或 `--settings missing`。编译期 RED 在台账记 Ruling。

- [X] **Step 3: 最小实现**——

`internal/engine/host.go` BootConfig 追加：

```go
	// SettingsJSON 是 --settings 文件内容（治理面注入；leaf 级以 string 传输，
	// 引擎包不解释其结构）。非空 → buildArgs 物化临时文件并追加 --settings。
	SettingsJSON string
```

`internal/engine/claudehost.go`：

```go
// ClaudeHost 字段追加（mcpTemp 之后）：
	settingsTemp string // --settings 物化文件（Close/失败路径清理）

// buildArgs：--setting-sources 之后、--model 之前插入：
	if cfg.SettingsJSON != "" {
		f, err := os.CreateTemp("", "mulix-settings-*.json")
		if err != nil {
			return nil, "", fmt.Errorf("engine: materialize settings: %w", err)
		}
		if _, err := f.WriteString(cfg.SettingsJSON); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, "", fmt.Errorf("engine: write settings: %w", err)
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return nil, "", fmt.Errorf("engine: close settings: %w", err)
		}
		args = append(args, "--settings", f.Name())
	}
// 注意：buildArgs 返回值需同时暴露 settingsTemp（改签名为
// ([]string, string, string, error)——(args, mcpTemp, settingsTemp, err)），
// Start 的 h.settingsTemp = settingsTemp；调用点同步。
// 或者维持双返回值签名、以结构体小改——实现者任选其一，但 Start 失败路径
// (spawnErr → cleanupTemp) 与 Close → cleanupTemp 必须两者都清。

// cleanupTemp 扩展：
func (h *ClaudeHost) cleanupTemp() {
	if h.mcpTemp != "" {
		_ = os.Remove(h.mcpTemp)
		h.mcpTemp = ""
	}
	if h.settingsTemp != "" {
		_ = os.Remove(h.settingsTemp)
		h.settingsTemp = ""
	}
}
```

- [X] **Step 4: 跑测试看它通过**

Run: `go test ./internal/engine/ -count=1`
Expected: PASS（TestBuildArgs 新序列 + 清理测试 + 既有握手/泵测试全绿）。

- [X] **Step 5: 全量 + 提交**

Run: `go test ./... -count=1 && gofmt -l . && go vet ./...`
Expected: 全绿/干净。

```bash
git add internal/engine/host.go internal/engine/claudehost.go internal/engine/claudehost_test.go
git commit -m "feat(engine): --settings 物化与生命周期清理(治理旁路修复 L1 传输面)"
```

---

## Task 3: control 接线——composeBoot 传递 SettingsJSON

**Files:**
- Modify: `internal/control/controller.go`（composeBoot）
- Test: `internal/cli/session_test.go`、`internal/cli/hooks_wiring_test.go`

**Interfaces:**
- Consumes: Task 1 `artifact.SettingsJSON`、Task 2 `boot.SettingsJSON`。
- Produces: 真实引擎 Boot 面含 `--settings`（经 composeBoot 全链路）；wiring 测试钉字节。

- [X] **Step 1: 写失败测试**——`internal/cli/session_test.go` 的 `TestRunUnattendedE2E`（或同文件其它 boot 断言测试）HostFactory 内追加：

```go
		if boot.SettingsJSON != `{"permissions":{"ask":["Bash","PowerShell"]}}` {
			t.Errorf("settings json = %q, want governance ask face", boot.SettingsJSON)
		}
```

`internal/cli/hooks_wiring_test.go` 的 boot 面一致性断言（bootEmpty vs bootAbsent）追加 `bootEmpty.SettingsJSON != bootAbsent.SettingsJSON` 分支（空规则/无 hooks 不得改 Boot 面）。

- [X] **Step 2: 跑测试看它失败**

Run: `go test ./internal/cli/ -run 'TestRunUnattendedE2E|TestHookWiring' -count=1`
Expected: FAIL——`settings json = ""`（composeBoot 未传递）。字段缺编译错则记 Ruling。

- [X] **Step 3: 最小实现**——`composeBoot` 在 `base.PermissionMode = artifact.PermissionMode` 之后追加：

```go
	base.SettingsJSON = artifact.SettingsJSON
```

- [X] **Step 4: 跑测试看它通过**

Run: `go test ./internal/cli/ -count=1 && go test ./internal/control/ -count=1`
Expected: PASS（wiring 全绿；control 零回归）。

- [X] **Step 5: 全量 + 提交**

Run: `go test ./... -count=1 && gofmt -l . && go vet ./...`
Expected: 全绿/干净。

```bash
git add internal/control/controller.go internal/cli/session_test.go internal/cli/hooks_wiring_test.go
git commit -m "feat(control): composeBoot 传递治理 --settings 注入面(治理旁路修复 L1 接线)"
```

---

## Task 4: control 围堵——未裁决 tool_result 治理违规终止

**Files:**
- Modify: `internal/control/controller.go`（governedTools 状态、onAssistant 捕获、onToolResult 校验、onResult 清理、违规处置）
- Test: `internal/control/governance_test.go`（新）

**Interfaces:**
- Consumes: 既有 `c.resolved`（裁决缓存）、`c.running`、`c.opts.Config.Permissions.Allow`、`c.publish`、`c.host`。
- Produces: 治理违规留痕（`NoticeEvent{Level:"error", IsMeta:true, Content:"[governance_violation] …"}`）+ 引擎异步终止（pump 尾合成 engine_closed 收口 + engineDead）。测试经 FakeHost 回合 fixture 驱动。

- [X] **Step 1: 写失败测试**——新建 `internal/control/governance_test.go`：

```go
package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/sessionx"
)

// govConfig 返回带 allow 清单的配置（显式白名单正例用）。
func govConfig(allow ...string) config.Config {
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		panic(err)
	}
	cfg.Permissions.Allow = allow
	return *cfg
}

// govController 构造无人值守控制器（config 可注入、Options 可再加工）。
// 骨架与 newTestControllerWithPolicyRules 同源——Config 用参数，经 mutate
// 注入 Source 等测试缝。
func govController(t *testing.T, host engine.Host, sink Sink, cfg config.Config, mutate ...func(*Options)) *Controller {
	t.Helper()
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{
		ID: "s-test", Workspace: "w", Model: "m", PinnedModel: "m",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: "s-test",
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	t.Cleanup(func() { meter.Close() })
	policy, err := guard.NewPolicy(guard.PresetDefault, nil)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}
	opts := Options{
		HostFactory: func(engine.BootConfig) (engine.Host, error) { return host, nil },
		Sink:        sink, Store: store, Meter: meter, Gate: gate,
		Config: cfg, Workspace: "w", Unattended: true,
		Kernel: kernelCtxWithLoop(),
	}
	for _, m := range mutate {
		m(&opts)
	}
	c, err := NewController(opts)
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// toolUseTurn 构造回合段：assistant(tool_use) → 可选 can_use_tool 裁决请求
// → tool_result（不含 result 收口——调用方按需追加）。
func toolUseTurn(toolUseID, tool string, adjudicated bool) []engine.Event {
	turn := []engine.Event{{
		Kind: engine.KindAssistant, SessionID: "s", UUID: "a-" + toolUseID,
		Assistant: &engine.AssistantMsg{ID: "msg-" + toolUseID, Model: "fake-model",
			Blocks: []engine.ContentBlock{{Type: "tool_use", ID: toolUseID,
				Name: tool, Input: []byte(`{"command":"echo hi"}`)}},
		},
	}}
	if adjudicated {
		turn = append(turn, canUseTool("req-"+toolUseID, toolUseID, tool, `{"command":"echo hi"}`))
	}
	turn = append(turn, engine.Event{
		Kind: engine.KindToolResult, SessionID: "s", UUID: "r-" + toolUseID,
		ToolResult: &engine.ToolResultMsg{Blocks: []engine.ToolResultBlock{{
			ToolUseID: toolUseID, Content: "hi", IsError: false}}},
	})
	return turn
}

// successResult 是回合正常收口事件。
func successResult(uuid string) engine.Event {
	return engine.Event{Kind: engine.KindResult, SessionID: "s", UUID: uuid,
		Result: &engine.ResultInfo{SubType: "success", Usage: engine.Usage{OutputTokens: 1}}}
}

// hasViolationNotice 报告镜像是否含 [governance_violation] 留痕。
func hasViolationNotice(t *testing.T, c *Controller) bool {
	return violationNoticeOf(t, c) != nil
}

// violationNoticeOf 返回镜像中的治理违规留痕（无则 nil）。
func violationNoticeOf(t *testing.T, c *Controller) *event.NoticeEvent {
	t.Helper()
	mirror, _ := c.Mirror().ReadAllEvents()
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "[governance_violation]") {
			cp := n
			return &cp
		}
	}
	return nil
}

// TestGovernanceViolationTerminatesSession pins：运行态收到未裁决、非白名单
// 工具（PowerShell）的 tool_result → 违规留痕 + 引擎终止（engine_closed 收口）
// + 后续 Submit 拒绝（fail-closed）。fixture 不含 result 收口——回合只能经
// 围堵终止路径闭合（engine_closed 合成），断言无竞态。
func TestGovernanceViolationTerminatesSession(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{
		toolUseTurn("tu-v1", "PowerShell", false),
	}})
	sink := &recordingSink{}
	c := govController(t, fake, sink, mustDefaultConfig())
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	violation := violationNoticeOf(t, c)
	if violation == nil {
		t.Fatal("no governance_violation notice in mirror")
	}
	if !strings.Contains(violation.Content, "PowerShell") || !strings.Contains(violation.Content, "tu-v1") {
		t.Fatalf("violation notice lacks tool/id: %q", violation.Content)
	}
	// 引擎已死：后续 Submit 拒绝
	if err := c.Submit(context.Background(), "again"); err == nil {
		t.Fatal("submit after violation must fail (engineDead)")
	}
	// engine_closed 收口在镜像
	var sawClosed bool
	mirror, _ := c.Mirror().ReadAllEvents()
	for _, ev := range mirror {
		if td, ok := ev.(event.TurnDoneEvent); ok && td.Subtype == "engine_closed" {
			sawClosed = true
		}
	}
	if !sawClosed {
		t.Fatal("no engine_closed TurnDone after violation")
	}
}

// TestGovernanceAllowListPass pins：mulix config allow 清单命中的未裁决
// tool_result 不触发围堵（显式白名单语义；allow 迁移结果不变性）。
func TestGovernanceAllowListPass(t *testing.T) {
	turn := toolUseTurn("tu-a1", "PowerShell", false)
	fake := engine.NewFakeHost(engine.FakeScript{
		Turns: [][]engine.Event{append(turn, successResult("res-a1"))},
	})
	sink := &recordingSink{}
	c := govController(t, fake, sink, govConfig("PowerShell(echo:*)"))
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if hasViolationNotice(t, c) {
		t.Fatal("allow-listed tool must not violate")
	}
}

// TestGovernanceAdjudicatedPass pins：经审批回路裁决的 tool_result 不触发
// 围堵——allow 与 deny 两形态（deny 的 is_error 回流属已裁决，FR-208）。
func TestGovernanceAdjudicatedPass(t *testing.T) {
	for _, tc := range []struct {
		name  string
		effect guard.Decision
		isErr bool
	}{
		{"allow", guard.DecisionAllow, false},
		{"deny", guard.DecisionDeny, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turn := toolUseTurn("tu-j-"+tc.name, "Bash", true)
			fake := engine.NewFakeHost(engine.FakeScript{
				Turns: [][]engine.Event{append(turn, successResult("res-j-" + tc.name))},
			})
			sink := &recordingSink{}
			c := newTestControllerWithPolicyRules(t, fake, sink,
				policyRuleIn{raw: "Bash(echo:*)", effect: tc.effect})
			if err := c.NewSession(context.Background()); err != nil {
				t.Fatalf("new session: %v", err)
			}
			if err := c.Submit(context.Background(), "hi"); err != nil {
				t.Fatalf("submit: %v", err)
			}
			if hasViolationNotice(t, c) {
				t.Fatalf("adjudicated (%s) call must not violate", tc.name)
			}
			resolved := resolvedOf(t, c, "tu-j-"+tc.name)
			if resolved.Decision != string(tc.effect) {
				t.Fatalf("resolved = %+v", resolved)
			}
		})
	}
}

// TestGovernanceReadOnlyPass pins：只读文件工具（Read/Glob/Grep）与内置
// MCP（mcp__mulix__ 前缀）未裁决回流不触发围堵。
func TestGovernanceReadOnlyPass(t *testing.T) {
	for _, tool := range []string{"Read", "Glob", "Grep", "mcp__mulix__memory"} {
		t.Run(tool, func(t *testing.T) {
			turn := toolUseTurn("tu-ro", tool, false)
			fake := engine.NewFakeHost(engine.FakeScript{
				Turns: [][]engine.Event{append(turn, successResult("res-ro"))},
			})
			sink := &recordingSink{}
			c := govController(t, fake, sink, mustDefaultConfig())
			if err := c.NewSession(context.Background()); err != nil {
				t.Fatalf("new session: %v", err)
			}
			if err := c.Submit(context.Background(), "hi"); err != nil {
				t.Fatalf("submit %s: %v", tool, err)
			}
			if hasViolationNotice(t, c) {
				t.Fatalf("%s must not violate", tool)
			}
		})
	}
}

// chanSource 是测试用 EventSource：从 channel 逐事件投递（孤儿时序控制）。
type chanSource struct{ ch chan engine.Event }

func (s *chanSource) Events() <-chan engine.Event { return s.ch }
func (s *chanSource) Host() engine.Host           { return nil }

// TestGovernanceOrphanAfterCancel pins：真实回合收口（running: true→false）
// 之后到达的孤儿 tool_result 不触发围堵且泵不崩（run2 t3 Esc 孤儿防御语义，
// FR-207）。经 chanSource 控制时序：init → assistant(tool_use) → result
// （onResult 正常收口）→ 孤儿 tool_result。
func TestGovernanceOrphanAfterCancel(t *testing.T) {
	events := make(chan engine.Event, 8)
	sink := &recordingSink{}
	c := govController(t, engine.NewFakeHost(engine.FakeScript{}), sink, mustDefaultConfig(),
		func(o *Options) { o.Source = &chanSource{ch: events} })
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	events <- engine.Event{Kind: engine.KindInit, SessionID: "s", UUID: "init-or",
		Init: &engine.InitInfo{CLIVersion: "2.1.291-fake", Model: "fake-model", PermissionMode: "default"}}
	turn := toolUseTurn("tu-or", "PowerShell", false)
	events <- turn[0]             // assistant(tool_use)：running=true
	events <- successResult("res-or") // onResult 正常收口：running=false
	// 孤儿：收口后的 tool_result（引擎取消残留形态）
	events <- engine.Event{
		Kind: engine.KindToolResult, SessionID: "s", UUID: "r-orphan",
		ToolResult: &engine.ToolResultMsg{Blocks: []engine.ToolResultBlock{{
			ToolUseID: "tu-or", Content: "late", IsError: false}}},
	}
	// 2s 内出现违规即失败（负断言）；超时无违规 = 通过
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			close(events)
			return
		case <-time.After(20 * time.Millisecond):
		}
		if violationNoticeOf(t, c) != nil {
			t.Fatal("orphan tool_result must not violate")
		}
	}
}
```

（`kernelCtxWithLoop`/`mustDefaultConfig`/`recordingSink`/`canUseTool`/`resolvedOf` 均为同包既有 helper。评审员核定注意点：`config.NewLoader().Load` 返回 `*Config`——govConfig 必须 `return *cfg`；`kernel` 包不被测试文件直接引用、勿 import。）

- [X] **Step 2: 跑测试看它失败**

Run: `go test ./internal/control/ -run TestGovernance -count=1`
Expected: FAIL——`no governance_violation notice in mirror`（围堵未实现；PowerShell 漏放）。

- [X] **Step 3: 最小实现**——`internal/control/controller.go`：

```go
// Controller 字段追加（inflight 之后）：
	governedTools map[string]governedTool // tool_use_id → 工具名+回合（围堵消费；onResult 按 turn 清理）
	violateOnce   sync.Once               // 违规终止恰一次（并发结果块窗口）

// 类型：
// governedTool 是围堵台账条目。
type governedTool struct {
	tool string
	turn int64
}

// 常量（permissionServerName 旁）：
// governanceBuiltinPrefix 是 mulix 内置 MCP 工具前缀（白名单构造）。
const governanceBuiltinPrefix = "mcp__mulix__"

// governanceReadOnlyTools 是只读文件工具白名单（引擎构造即只读；本变更裁定：
// 其余引擎自主放行类一律违规——spec FR-205/Assumptions）。
var governanceReadOnlyTools = map[string]bool{"Read": true, "Glob": true, "Grep": true}

// NewController 初始化处（inflight 同点）追加：
		governedTools: map[string]governedTool{},

// onAssistant 的 tool_use case 追加捕获（publish 之后）：
		case "tool_use":
			c.publish(event.ToolUseRequestedEvent{...}) // 既有
			c.mu.Lock()
			c.governedTools[b.ID] = governedTool{tool: b.Name, turn: turn}
			c.mu.Unlock()

// onToolResult 重写：
func (c *Controller) onToolResult(ev engine.Event) {
	if ev.ToolResult == nil {
		return
	}
	turn := c.currentTurn()
	for _, b := range ev.ToolResult.Blocks {
		c.publish(event.ToolResultEvent{Seq: c.nextSeq(), Turn: turn,
			ToolUseID: b.ToolUseID, Content: b.Content, IsError: b.IsError})
	}
	c.checkGovernedResults(turn, ev.ToolResult.Blocks)
}

// checkGovernedResults 围堵校验（镜像 1:1 先行——上面 publish 已落；违规
// 留痕走同款 publish）。仅运行态校验：非运行态的孤儿结果（Esc 取消残留）
// 不触发（FR-207）。
func (c *Controller) checkGovernedResults(turn int64, blocks []engine.ToolResultBlock) {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if !running {
		return
	}
	for _, b := range blocks {
		c.mu.Lock()
		_, adjudicated := c.resolved[b.ToolUseID]
		gov, tracked := c.governedTools[b.ToolUseID]
		allow := c.opts.Config.Permissions.Allow
		c.mu.Unlock()
		if adjudicated || c.governanceExempt(gov.tool, tracked, allow) {
			continue
		}
		tool := "unknown"
		if tracked {
			tool = gov.tool
		}
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "error", IsMeta: true,
			Content: fmt.Sprintf("[governance_violation] 工具 %s（call %s）未经审批回路即回流结果，终止会话（fail-closed）",
				tool, b.ToolUseID)})
		c.violateOnce.Do(func() {
			host := c.host
			go func() {
				_ = host.Close(context.Background()) // 异步：泵 goroutine 同步 Close 会死锁
			}()
		})
	}
}

// governanceExempt 白名单判定：config allow 清单 ∨ mulix 内置 MCP ∨ 只读文件工具。
func (c *Controller) governanceExempt(tool string, tracked bool, allow []string) bool {
	if !tracked {
		return false // 未跟踪（孤儿）且运行态 → 不豁免（响亮失败）
	}
	if governanceReadOnlyTools[tool] || strings.HasPrefix(tool, governanceBuiltinPrefix) {
		return true
	}
	for _, a := range allow {
		if a == tool || strings.HasPrefix(a, tool+"(") {
			return true
		}
	}
	return false
}

// onResult 的清理循环（resolved 回收同点）追加：
	for id, g := range c.governedTools {
		if g.turn <= turn {
			delete(c.governedTools, id)
		}
	}
```

- [X] **Step 4: 跑测试看它通过**

Run: `go test ./internal/control/ -count=1`
Expected: PASS（治理五例 + 既有 routing/e2e 全绿）。

- [X] **Step 5: 全量 + 提交**

Run: `go test ./... -count=1 && gofmt -l . && go vet ./...`
Expected: 全绿/干净。

```bash
git add internal/control/controller.go internal/control/governance_test.go
git commit -m "feat(control): 未裁决工具结果运行时围堵——违规留痕+引擎终止(治理旁路修复 L2)"
```

---

## Task 5: verify——全量验证与真机冒烟

**Files:**
- Create: `docs/evidence/smoke-engine-governance-2026-10-16.md`
- Modify: `docs/changes/2026-10-16-engine-tool-governance-fix/report.md`（verify 阶段产物）

**Interfaces:**
- Consumes: Task 1–4 全绿分支。
- Produces: SC-001/002 真机证据 + verify 收口。

- [ ] **Step 1: 全量门禁**

Run: `go build ./... && go test ./... -count=1 && gofmt -l . && go vet ./...`
Expected: 全绿/干净（-race 不可用如实记录）。

- [ ] **Step 2: 真机冒烟（SC-001/002）**——出仓构建（禁仓根直跑）：

```bash
go build -o "$TEMP/mulix-cli-gov.exe" ./cmd/mulix-cli
```

临时工作区 + engine.lock 副本（drift 用 `--allow-engine-drift`，CLI 现为 2.1.301 ≠ 钉 2.1.296）；`mulix run` 无人值守两回合：
1. `运行命令 echo gov-smoke-readonly`——**断言镜像出现 PowerShell 调用的 approval_request + approval_resolved**（修复前零审批事件；allow 清单未命中 → unattended fail-closed 拒绝，属 Gate 正确裁决）。若配置 allow 含 `PowerShell(echo:*)`，对账 allow 命中 → resolved{allow}（结果不变性）。
2. 写类命令回合（attended 不可用时跳过，以自动化测试对账 SC-002 的 Gate 语义覆盖）。
证据（引擎版本/工作区/时间线/退出码/镜像摘录）落 `docs/evidence/smoke-engine-governance-2026-10-16.md`。

- [ ] **Step 3: verify 提交**

```bash
git add docs/evidence/smoke-engine-governance-2026-10-16.md
git commit -m "docs(evidence): 引擎治理旁路修复真机冒烟——SC-001/002(2026-10-16)"
```
