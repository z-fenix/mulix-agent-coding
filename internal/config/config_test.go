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

func warnMentions(warnings []string, key string) bool {
	for _, w := range warnings {
		if strings.Contains(w, key) {
			return true
		}
	}
	return false
}
