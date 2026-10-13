package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTOML seeds a layer file with content and returns its path.
func writeTOML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layer.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return path
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// TestDefaultsWithoutFiles pins the built-in defaults (design §12 骨架).
func TestDefaultsWithoutFiles(t *testing.T) {
	cfg, warnings, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cfg.DefaultModel != "claude-sonnet-4-5" {
		t.Errorf("DefaultModel = %q", cfg.DefaultModel)
	}
	if cfg.Cache.HitRateFloor != 0.70 {
		t.Errorf("HitRateFloor = %v", cfg.Cache.HitRateFloor)
	}
	if cfg.Cache.ColdAfterIdle != "4m" {
		t.Errorf("ColdAfterIdle = %q", cfg.Cache.ColdAfterIdle)
	}
	if cfg.Permissions.Preset != "default" {
		t.Errorf("Preset = %q", cfg.Permissions.Preset)
	}
	if cfg.Engine.AllowDrift {
		t.Errorf("AllowDrift default must be false")
	}
	if cfg.Engine.Path != "claude" {
		t.Errorf("Engine.Path default = %q, want claude", cfg.Engine.Path)
	}
	if cfg.Tools.MemorySearch {
		t.Errorf("MemorySearch default must be false")
	}
}

// TestSubagentConfigDefaultsAndLayering pins（P2 Global Constraints：新配置键
// 进默认值与分层加载测试）：[subagent] max_depth/max_concurrent 内置默认
// 3/4（design 002 §3），项目层可覆盖（user 层优先级更高，锁定面不含此键）。
func TestSubagentConfigDefaultsAndLayering(t *testing.T) {
	cfg, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.Subagent.MaxDepth != 3 || cfg.Subagent.MaxConcurrent != 4 {
		t.Fatalf("subagent defaults = %+v, want max_depth 3 / max_concurrent 4", cfg.Subagent)
	}
	project := writeTOML(t, "[subagent]\nmax_depth = 1\nmax_concurrent = 2\n")
	cfg2, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load project layer: %v", err)
	}
	if cfg2.Subagent.MaxDepth != 1 || cfg2.Subagent.MaxConcurrent != 2 {
		t.Fatalf("project layer subagent = %+v, want max_depth 1 / max_concurrent 2", cfg2.Subagent)
	}
	// 项目层只写一个键 → 另一个键保默认（BurntSushi 解码保底值语义）
	partial := writeTOML(t, "[subagent]\nmax_depth = 5\n")
	cfg3, _, err := NewLoader().Load(Flags{}, Paths{Project: partial})
	if err != nil {
		t.Fatalf("load partial layer: %v", err)
	}
	if cfg3.Subagent.MaxDepth != 5 || cfg3.Subagent.MaxConcurrent != 4 {
		t.Fatalf("partial layer subagent = %+v, want 5/4", cfg3.Subagent)
	}
}

// TestPriorityMatrix pins flag > project > user > default (design §12).
func TestPriorityMatrix(t *testing.T) {
	user := writeTOML(t, "default_model = \"user-model\"\n\n[cache]\nhit_rate_floor = 0.5\n")
	project := writeTOML(t, "default_model = \"project-model\"\n\n[cache]\nhit_rate_floor = 0.9\n")

	// default only
	cfg, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil || cfg.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("default layer: %v %q", err, cfg.DefaultModel)
	}
	// user beats default
	cfg, _, err = NewLoader().Load(Flags{}, Paths{User: user})
	if err != nil || cfg.DefaultModel != "user-model" || cfg.Cache.HitRateFloor != 0.5 {
		t.Fatalf("user layer: %v %q %v", err, cfg.DefaultModel, cfg.Cache.HitRateFloor)
	}
	// project beats user
	cfg, _, err = NewLoader().Load(Flags{}, Paths{User: user, Project: project})
	if err != nil || cfg.DefaultModel != "project-model" || cfg.Cache.HitRateFloor != 0.9 {
		t.Fatalf("project layer: %v %q %v", err, cfg.DefaultModel, cfg.Cache.HitRateFloor)
	}
	// user key absent in project falls through (per-key merge, not per-file)
	projectPartial := writeTOML(t, "[cache]\nhit_rate_floor = 0.9\n")
	cfg, _, err = NewLoader().Load(Flags{}, Paths{User: user, Project: projectPartial})
	if err != nil || cfg.DefaultModel != "user-model" || cfg.Cache.HitRateFloor != 0.9 {
		t.Fatalf("per-key merge: %v %q %v", err, cfg.DefaultModel, cfg.Cache.HitRateFloor)
	}
	// flag beats everything
	cfg, _, err = NewLoader().Load(Flags{Model: strPtr("flag-model")}, Paths{User: user, Project: project})
	if err != nil || cfg.DefaultModel != "flag-model" {
		t.Fatalf("flag layer: %v %q", err, cfg.DefaultModel)
	}
}

// TestFlagsOverrideAllFields pins the flag layer on every flag P1 wires.
func TestFlagsOverrideAllFields(t *testing.T) {
	user := writeTOML(t, `
default_model = "user-model"
[permissions]
preset = "default"
[engine]
allow_drift = false
`)
	cfg, _, err := NewLoader().Load(Flags{Model: strPtr("flag-model"), Preset: strPtr("acceptEdits"), AllowDrift: boolPtr(true)}, Paths{User: user})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultModel != "flag-model" || cfg.Permissions.Preset != "acceptEdits" || !cfg.Engine.AllowDrift {
		t.Fatalf("flags did not win: %+v", cfg)
	}
}

// TestCorruptedFileRecovery pins: a corrupted layer is skipped with a
// loadWarning, the rest of the stack loads, and the original file is never
// modified (design §12: last-known-good 恢复，绝不改写原文件).
func TestCorruptedFileRecovery(t *testing.T) {
	user := writeTOML(t, "default_model = \"user-model\"\n")
	projectPath := writeTOML(t, "default_model = \"not [ valid toml")
	before, _ := os.ReadFile(projectPath)

	loader := NewLoader()
	cfg, warnings, err := loader.Load(Flags{}, Paths{User: user, Project: projectPath})
	if err != nil {
		t.Fatalf("load must recover from corruption: %v", err)
	}
	if cfg.DefaultModel != "user-model" {
		t.Fatalf("corrupt project layer must fall through to user: %q", cfg.DefaultModel)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "project") {
		t.Fatalf("corruption must emit a warning naming the layer: %v", warnings)
	}
	after, _ := os.ReadFile(projectPath)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("load must never rewrite the original file")
	}
	// last-known-good anchors the most recent usable load (skipped layers
	// included): the recovery consumer can always fall back to it.
	if loader.LastGood() == nil {
		t.Fatal("usable load should anchor LastGood")
	}
	cleanUser := writeTOML(t, "default_model = \"clean-model\"\n")
	cfg2, _, _ := loader.Load(Flags{}, Paths{User: cleanUser})
	if got := loader.LastGood(); got == nil || got.DefaultModel != cfg2.DefaultModel {
		t.Fatalf("LastGood must track the last clean load: %+v", got)
	}
}

// TestCorruptedUserLayerStillLoads pins recovery when the USER layer is corrupt.
func TestCorruptedUserLayerStillLoads(t *testing.T) {
	userBad := writeTOML(t, "[cache\nhit_rate_floor = 0.5\n")
	cfg, warnings, err := NewLoader().Load(Flags{}, Paths{User: userBad})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("defaults must survive corrupt user layer: %q", cfg.DefaultModel)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "user") {
		t.Fatalf("corrupt user layer must warn: %v", warnings)
	}
}

// TestLockEngineAllowDrift pins key-level user-global lock: the project layer
// may not override engine.allow_drift (warning + not effective).
func TestLockEngineAllowDrift(t *testing.T) {
	user := writeTOML(t, "[engine]\nallow_drift = false\n")
	project := writeTOML(t, "[engine]\nallow_drift = true\n")
	cfg, warnings, err := NewLoader().Load(Flags{}, Paths{User: user, Project: project})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.AllowDrift {
		t.Fatal("project must not override user-global engine.allow_drift")
	}
	if !warnMentions(warnings, "engine.allow_drift") {
		t.Fatalf("lock violation must warn with the key path: %v", warnings)
	}
	// Project may not introduce the key when user is silent, either.
	cfg, warnings, _ = NewLoader().Load(Flags{}, Paths{Project: project})
	if cfg.Engine.AllowDrift || !warnMentions(warnings, "engine.allow_drift") {
		t.Fatalf("project-introduced locked key must be dropped: %+v %v", cfg.Engine, warnings)
	}
}

// TestLockDangerFullPreset pins value-level user-global lock: the project
// layer may not grant the danger-full preset; the user layer is the authority.
func TestLockDangerFullPreset(t *testing.T) {
	project := writeTOML(t, "[permissions]\npreset = \"danger-full\"\n")
	cfg, warnings, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Permissions.Preset == "danger-full" {
		t.Fatal("project must not grant danger-full preset")
	}
	if !warnMentions(warnings, "permissions.preset") {
		t.Fatalf("lock violation must warn: %v", warnings)
	}
	// User MAY grant danger-full (user-global 准入).
	userDanger := writeTOML(t, "[permissions]\npreset = \"danger-full\"\n")
	cfg, _, err = NewLoader().Load(Flags{}, Paths{User: userDanger})
	if err != nil || cfg.Permissions.Preset != "danger-full" {
		t.Fatalf("user layer is the danger-full authority: %v %q", err, cfg.Permissions.Preset)
	}
	// Project's non-privileged value still works.
	projectDefault := writeTOML(t, "[permissions]\npreset = \"plan\"\n")
	cfg, _, err = NewLoader().Load(Flags{}, Paths{User: userDanger, Project: projectDefault})
	if err != nil || cfg.Permissions.Preset != "plan" {
		t.Fatalf("project non-privileged preset must apply: %v %q", err, cfg.Permissions.Preset)
	}
}

// TestLockMechanismExtensible pins the extension mechanism itself: a newly
// registered user-global key (P1 骨架尚无凭据来源/遥测键位) is enforced the
// moment it is registered — 键位引入即纳入锁定.
func TestLockMechanismExtensible(t *testing.T) {
	path := "telemetry.enabled" // P2 键位；机制测试
	RegisterLockedKey(path)
	t.Cleanup(func() { delete(keyLocks, path) })

	raw := map[string]any{"telemetry": map[string]any{"enabled": true}}
	warnings := applyLockedRules(raw)
	if !warnMentions(warnings, path) {
		t.Fatalf("registered key must be enforced: %v", warnings)
	}
	if section, ok := raw["telemetry"].(map[string]any); ok {
		if _, present := section["enabled"]; present {
			t.Fatal("registered locked key must be dropped from the project layer")
		}
	}
}

// TestLockedKeyFromProjectDroppedWhiteBox pins (white-box) that a locked key
// set by the project layer never reaches the merge: enforcement removes it
// from the raw layer map before any merging happens.
func TestLockedKeyFromProjectDroppedWhiteBox(t *testing.T) {
	raw := map[string]any{"engine": map[string]any{"allow_drift": true}}
	warnings := applyLockedRules(raw)
	if !warnMentions(warnings, "engine.allow_drift") {
		t.Fatalf("lock violation must warn with the key path: %v", warnings)
	}
	if section, ok := raw["engine"].(map[string]any); ok {
		if _, present := section["allow_drift"]; present {
			t.Fatal("locked key from project must be dropped before merge")
		}
	}
	// Non-privileged project values on value-locked paths survive.
	raw2 := map[string]any{"permissions": map[string]any{"preset": "plan"}}
	if w := applyLockedRules(raw2); len(w) != 0 {
		t.Fatalf("non-privileged value must not warn: %v", w)
	}
}

// TestSchemaMismatchRecovers pins: a syntactically valid layer whose value
// types don't match the schema (hit_rate_floor = "0.9") is treated as
// corrupted — skip + warn, other layers survive, Load never hard-fails.
func TestSchemaMismatchRecovers(t *testing.T) {
	user := writeTOML(t, "default_model = \"user-model\"\n")
	bad := writeTOML(t, "[cache]\nhit_rate_floor = \"0.9\"\n")
	cfg, warnings, err := NewLoader().Load(Flags{}, Paths{User: user, Project: bad})
	if err != nil {
		t.Fatalf("schema mismatch must not hard-fail: %v", err)
	}
	if cfg.DefaultModel != "user-model" || cfg.Cache.HitRateFloor != 0.70 {
		t.Fatalf("schema-mismatched layer must be skipped: %+v", cfg)
	}
	if !warnMentions(warnings, "project") {
		t.Fatalf("schema mismatch must warn naming the layer: %v", warnings)
	}
}

// TestListReplacementPinned pins: lists replace (not concatenate) across
// layers — concatenation would silently widen the allow list.
func TestListReplacementPinned(t *testing.T) {
	user := writeTOML(t, "[permissions]\ndeny = [\"a\"]\nallow = [\"u1\"]\n")
	project := writeTOML(t, "[permissions]\ndeny = [\"b\"]\n")
	cfg, _, err := NewLoader().Load(Flags{}, Paths{User: user, Project: project})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(cfg.Permissions.Deny, []string{"b"}) {
		t.Fatalf("deny must be replaced by project: %v", cfg.Permissions.Deny)
	}
	if !reflect.DeepEqual(cfg.Permissions.Allow, []string{"u1"}) {
		t.Fatalf("allow must fall through from user: %v", cfg.Permissions.Allow)
	}
}

// TestExplicitZeroValueRoundTrip pins: an explicit TOML zero beats the
// built-in default (presence-based overwrite, not zero-value detection).
func TestExplicitZeroValueRoundTrip(t *testing.T) {
	project := writeTOML(t, "[cache]\nhit_rate_floor = 0.0\n")
	cfg, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Cache.HitRateFloor != 0 {
		t.Fatalf("explicit zero must beat default 0.70: %v", cfg.Cache.HitRateFloor)
	}
}

// TestMemoryConfigDefaultsAndLayering pins（P2 Global Constraints：新配置键
// 进默认值与分层加载测试）：[memory] recall_top_k=3 / recall_budget_chars=2000
// （design 002 §4；2000 字符预算 = spec Assumptions），项目层可覆盖、单键覆盖
// 时另一键保默认（BurntSushi 解码保底值语义）。
func TestMemoryConfigDefaultsAndLayering(t *testing.T) {
	cfg, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.Memory.RecallTopK != 3 || cfg.Memory.RecallBudgetChars != 2000 {
		t.Fatalf("memory defaults = %+v, want recall_top_k 3 / recall_budget_chars 2000", cfg.Memory)
	}
	project := writeTOML(t, "[memory]\nrecall_top_k = 5\nrecall_budget_chars = 512\n")
	cfg2, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load project layer: %v", err)
	}
	if cfg2.Memory.RecallTopK != 5 || cfg2.Memory.RecallBudgetChars != 512 {
		t.Fatalf("project layer memory = %+v, want 5/512", cfg2.Memory)
	}
	partial := writeTOML(t, "[memory]\nrecall_top_k = 7\n")
	cfg3, _, err := NewLoader().Load(Flags{}, Paths{Project: partial})
	if err != nil {
		t.Fatalf("load partial layer: %v", err)
	}
	if cfg3.Memory.RecallTopK != 7 || cfg3.Memory.RecallBudgetChars != 2000 {
		t.Fatalf("partial layer memory = %+v, want top_k 7 with budget default 2000", cfg3.Memory)
	}
}

// TestMCPConfigDefaultsAndLayering pins（P2 Global Constraints：新配置键进
// 默认值与分层加载测试）：[mcp] servers 默认空；[[mcp.servers]] 数组解析
// name/command/env/timeout_ms（design 002 §5）；缺省键保零值（env 空、
// timeout_ms 0 = Compose 侧默认 60000）；跨层整组替换（数组语义，非拼接，
// TestListReplacementPinned 同款裁定）。
func TestMCPConfigDefaultsAndLayering(t *testing.T) {
	cfg, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if len(cfg.MCP.Servers) != 0 {
		t.Fatalf("default mcp servers must be empty: %+v", cfg.MCP.Servers)
	}
	project := writeTOML(t, `
[[mcp.servers]]
name = "github"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "t0k" }
timeout_ms = 30000

[[mcp.servers]]
name = "fs"
command = ["npx", "-y", "@modelcontextprotocol/server-fs"]
`)
	cfg2, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load project layer: %v", err)
	}
	if len(cfg2.MCP.Servers) != 2 {
		t.Fatalf("project layer servers = %+v, want 2 declarations", cfg2.MCP.Servers)
	}
	gh := cfg2.MCP.Servers[0]
	if gh.Name != "github" ||
		!reflect.DeepEqual(gh.Command, []string{"npx", "-y", "@modelcontextprotocol/server-github"}) ||
		gh.TimeoutMS != 30000 || gh.Env["GITHUB_TOKEN"] != "t0k" {
		t.Fatalf("github decl = %+v", gh)
	}
	// 缺省键保零值：未写 env/timeout_ms 的声明不误取默认（Compose 侧解释）
	fs := cfg2.MCP.Servers[1]
	if fs.Name != "fs" || !reflect.DeepEqual(fs.Command, []string{"npx", "-y", "@modelcontextprotocol/server-fs"}) ||
		fs.TimeoutMS != 0 || len(fs.Env) != 0 {
		t.Fatalf("fs decl = %+v", fs)
	}
	// 数组跨层整组替换：项目层 servers 覆盖用户层（不拼接）
	user := writeTOML(t, "[[mcp.servers]]\nname = \"user-only\"\ncommand = [\"u\"]\n")
	cfg3, _, err := NewLoader().Load(Flags{}, Paths{User: user, Project: project})
	if err != nil {
		t.Fatalf("load user+project layers: %v", err)
	}
	if len(cfg3.MCP.Servers) != 2 || cfg3.MCP.Servers[0].Name != "github" {
		t.Fatalf("project layer must replace the user servers array: %+v", cfg3.MCP.Servers)
	}
}

// TestMCPServersEnvTypeMismatchRecovers 钉 F4b（评审要求的 pin，R-T7-3 机制
// 在 [mcp.servers] 本节的实证）：env 值非字符串 → 层按损坏处理（skip +
// warning 命名层），默认值与其余层存活——config.Loader「永远返回可用配置」
// 契约不变。
func TestMCPServersEnvTypeMismatchRecovers(t *testing.T) {
	bad := writeTOML(t, `
[[mcp.servers]]
name = "x"
command = ["c"]

[mcp.servers.env]
PORT = 123
`)
	cfg, warnings, err := NewLoader().Load(Flags{}, Paths{Project: bad})
	if err != nil {
		t.Fatalf("type mismatch must not hard-fail: %v", err)
	}
	if len(cfg.MCP.Servers) != 0 {
		t.Fatalf("type-mismatched layer must be skipped: %+v", cfg.MCP.Servers)
	}
	if !warnMentions(warnings, "project") {
		t.Fatalf("damaged layer must warn naming the layer: %v", warnings)
	}
	if cfg.DefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("defaults must survive the skipped layer: %q", cfg.DefaultModel)
	}
}

// TestHooksCustomDefaultsAndLayering pins（P2 Global Constraints：新配置键进
// 默认值与分层加载测试）：[hooks.custom] rules 默认空；[[hooks.custom.rules]]
// 数组解析 event/command/timeout_ms（design 002 §7）；缺省键保零值
// （timeout_ms 0 = 消费侧解释默认）；跨层整组替换（数组语义，非拼接，
// TestMCPConfigDefaultsAndLayering 同款裁定）。取值校验（event ∈ P1 四时点、
// command 非空、timeout ≤30000）在 guard.NewScriptConsumer 构造期收口
// （启动拒绝 fail-fast）——config 只负责分层解析，与 [mcp.servers] 的
// 「Compose 侧校验」同款分工。
func TestHooksCustomDefaultsAndLayering(t *testing.T) {
	cfg, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if len(cfg.Hooks.Custom.Rules) != 0 {
		t.Fatalf("default hooks rules must be empty: %+v", cfg.Hooks.Custom.Rules)
	}
	project := writeTOML(t, `
[[hooks.custom.rules]]
event = "PreToolUse"
command = ["python", "scripts/hook.py"]
timeout_ms = 5000

[[hooks.custom.rules]]
event = "Stop"
command = ["go", "run", "./scripts/notify"]
`)
	cfg2, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load project layer: %v", err)
	}
	if len(cfg2.Hooks.Custom.Rules) != 2 {
		t.Fatalf("project layer rules = %+v, want 2 declarations", cfg2.Hooks.Custom.Rules)
	}
	pre := cfg2.Hooks.Custom.Rules[0]
	if pre.Event != "PreToolUse" ||
		!reflect.DeepEqual(pre.Command, []string{"python", "scripts/hook.py"}) ||
		pre.TimeoutMS != 5000 {
		t.Fatalf("pre rule = %+v", pre)
	}
	// 缺省键保零值：未写 timeout_ms 的规则不误取默认（消费侧解释）
	stop := cfg2.Hooks.Custom.Rules[1]
	if stop.Event != "Stop" ||
		!reflect.DeepEqual(stop.Command, []string{"go", "run", "./scripts/notify"}) ||
		stop.TimeoutMS != 0 {
		t.Fatalf("stop rule = %+v", stop)
	}
	// 数组跨层整组替换：项目层 rules 覆盖用户层（不拼接）
	user := writeTOML(t, "[[hooks.custom.rules]]\nevent = \"SessionStart\"\ncommand = [\"u\"]\n")
	cfg3, _, err := NewLoader().Load(Flags{}, Paths{User: user, Project: project})
	if err != nil {
		t.Fatalf("load user+project layers: %v", err)
	}
	if len(cfg3.Hooks.Custom.Rules) != 2 || cfg3.Hooks.Custom.Rules[0].Event != "PreToolUse" {
		t.Fatalf("project layer must replace the user rules array: %+v", cfg3.Hooks.Custom.Rules)
	}
}

func warnMentions(warnings []string, key string) bool {
	for _, w := range warnings {
		if strings.Contains(w, key) {
			return true
		}
	}
	return false
}

// TestProfileModeParsing 钉 014 FR-101:三态(缺省/minimal/非法 fail-closed)。
func TestProfileModeParsing(t *testing.T) {
	cfg1, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("default load: %v", err)
	}
	if cfg1.Profile.Normalized() != ProfileStandard {
		t.Fatalf("default mode = %v, want standard", cfg1.Profile.Normalized())
	}
	// minimal(文件路径经 Paths.Project——与 DefaultPaths 文件约定一致)。
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mulix.toml")
	os.WriteFile(cfgPath, []byte("[profile]\nmode = \"minimal\"\n"), 0o644)
	cfg2, _, err := NewLoader().Load(Flags{}, Paths{Project: cfgPath})
	if err != nil {
		t.Fatalf("minimal load: %v", err)
	}
	if cfg2.Profile.Normalized() != ProfileMinimal {
		t.Fatalf("minimal mode = %v, want minimal", cfg2.Profile.Normalized())
	}
	// 非法 fail-closed(错误含 mode 值)。
	os.WriteFile(cfgPath, []byte("[profile]\nmode = \"bogus\"\n"), 0o644)
	_, _, err = NewLoader().Load(Flags{}, Paths{Project: cfgPath})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("bogus mode err = %v, want fail-closed with mode value", err)
	}
}

// TestEventbusEnabled 钉 018 FR-101:[eventbus] 三态——缺省关/true/false/
// 非法 fail-closed(错误含原值)。
func TestEventbusEnabled(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		want   bool
		wantEr bool
	}{
		{raw: "", want: false},
		{raw: "false", want: false},
		{raw: "true", want: true},
		{raw: "yes", wantEr: true},
		{raw: "TRUE", wantEr: true},
	} {
		got, err := EventbusConfig{RawEnable: tc.raw}.Enabled()
		if tc.wantEr {
			if err == nil || !strings.Contains(err.Error(), tc.raw) || !strings.Contains(err.Error(), `enable = "true"`) {
				t.Fatalf("raw %q err = %v, want fail-closed 含原值+引号串指路", tc.raw, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("raw %q = (%v,%v), want (%v,nil)", tc.raw, got, err, tc.want)
		}
	}
}

// TestEventbusGateLoadFailClosed 钉 018 FR-101(Load 级,终审 Minor 1):
// [eventbus] enable 非法值经真实 TOML Load fail-closed(错误含原值);且
// 校验先于 lastGood 赋值——失败的配置不得成为 last good(LastGood 为 nil)。
func TestEventbusGateLoadFailClosed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mulix.toml")
	os.WriteFile(cfgPath, []byte("[eventbus]\nenable = \"yes\"\n"), 0o644)
	l := NewLoader()
	_, _, err := l.Load(Flags{}, Paths{Project: cfgPath})
	if err == nil || !strings.Contains(err.Error(), "yes") {
		t.Fatalf("enable=yes err = %v, want fail-closed 含原值", err)
	}
	if l.LastGood() != nil {
		t.Fatalf("LastGood = %+v, want nil(校验必须先于 lastGood 赋值——拒绝的配置不得成为 last good)", l.LastGood())
	}
	// 合法值经 Load 正常通过(引号串形态——自然布尔 true 会被 toml 解码器
	// 拒绝,终审 Minor 2 路由文档明示)。
	os.WriteFile(cfgPath, []byte("[eventbus]\nenable = \"true\"\n"), 0o644)
	cfg, _, err := l.Load(Flags{}, Paths{Project: cfgPath})
	if err != nil {
		t.Fatalf("enable=true load: %v", err)
	}
	if on, err := cfg.Eventbus.Enabled(); err != nil || !on {
		t.Fatalf("enabled = (%v,%v), want (true,nil)", on, err)
	}
}
