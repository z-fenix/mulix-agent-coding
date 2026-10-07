package cache

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"mulix/internal/config"
)

// fakeRegistry 是 Task 25 registry 的最小测试替身：返回给定顺序的服务器清单。
type fakeRegistry struct {
	servers []MCPServer
}

func (f fakeRegistry) MCPServers() []MCPServer { return f.servers }

func baseCfg() config.Config {
	cfg := config.Config{}
	cfg.DefaultModel = "claude-sonnet-4-5"
	cfg.Permissions.Preset = "default"
	cfg.Permissions.Allow = []string{"Bash(go test:*)"}
	cfg.Permissions.Deny = []string{"Bash(rm -rf*)"}
	return cfg
}

// TestBootComposeByteStable 是 §8.1 护栏：同配置两次 Compose，
// 追加段 + mcp-config JSON + allowedTools 序列逐字节一致。
func TestBootComposeByteStable(t *testing.T) {
	cfg := baseCfg()
	reg := fakeRegistry{servers: []MCPServer{
		{Name: "browser", Command: []string{"npx", "-y", "@browser/mcp"}, Env: map[string]string{"DEBUG": "0"}},
		{Name: "mulix-permit", Command: []string{"mulix-permit"}},
	}}
	a1, err := Compose(cfg, "D:\\ws\\demo", reg)
	if err != nil {
		t.Fatalf("compose 1: %v", err)
	}
	a2, err := Compose(cfg, "D:\\ws\\demo", reg)
	if err != nil {
		t.Fatalf("compose 2: %v", err)
	}
	if a1.AppendSystemPrompt != a2.AppendSystemPrompt {
		t.Fatal("AppendSystemPrompt not byte-stable")
	}
	if a1.MCPConfigJSON != a2.MCPConfigJSON {
		t.Fatalf("MCPConfigJSON not byte-stable:\n%s\n---\n%s", a1.MCPConfigJSON, a2.MCPConfigJSON)
	}
	if !reflect.DeepEqual(a1.AllowedTools, a2.AllowedTools) {
		t.Fatalf("AllowedTools not stable: %v vs %v", a1.AllowedTools, a2.AllowedTools)
	}
	if !reflect.DeepEqual(a1.DisallowedTools, a2.DisallowedTools) {
		t.Fatalf("DisallowedTools not stable: %v vs %v", a1.DisallowedTools, a2.DisallowedTools)
	}
	if a1.Fingerprint != a2.Fingerprint {
		t.Fatalf("Fingerprint not stable: %s vs %s", a1.Fingerprint, a2.Fingerprint)
	}
}

// TestBootComposeOrderInsensitive 是 §8.1 护栏：输入参数遍历顺序不影响输出字节
// （registry 返回顺序不同 + env map 同内容 → 输出必须逐字节一致）。
func TestBootComposeOrderInsensitive(t *testing.T) {
	cfg := baseCfg()
	r1 := fakeRegistry{servers: []MCPServer{
		{Name: "alpha", Command: []string{"a"}, Env: map[string]string{"K1": "v1", "K2": "v2"}},
		{Name: "beta", Command: []string{"b"}},
	}}
	r2 := fakeRegistry{servers: []MCPServer{
		{Name: "beta", Command: []string{"b"}},
		{Name: "alpha", Command: []string{"a"}, Env: map[string]string{"K2": "v2", "K1": "v1"}},
	}}
	a1, err := Compose(cfg, "w", r1)
	if err != nil {
		t.Fatalf("compose 1: %v", err)
	}
	a2, err := Compose(cfg, "w", r2)
	if err != nil {
		t.Fatalf("compose 2: %v", err)
	}
	if a1.MCPConfigJSON != a2.MCPConfigJSON {
		t.Fatalf("MCPConfigJSON order-sensitive:\n%s\n---\n%s", a1.MCPConfigJSON, a2.MCPConfigJSON)
	}
	if a1.Fingerprint != a2.Fingerprint {
		t.Fatal("Fingerprint order-sensitive")
	}
	// allowedTools 保持配置的显式顺序（列表显式顺序，不重排）
	want := []string{"Bash(go test:*)"}
	if !reflect.DeepEqual(a1.AllowedTools, want) {
		t.Fatalf("AllowedTools = %v, want %v (explicit config order)", a1.AllowedTools, want)
	}
}

// TestComposeSystemPromptSnapshot pins C1: static template + startup-params
// snapshot (workspace/model/mode), no timestamps (byte-stable across calls).
func TestComposeSystemPromptSnapshot(t *testing.T) {
	cfg := baseCfg()
	cfg.Permissions.Preset = "plan"
	artifact, err := Compose(cfg, "D:\\ws\\proj", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	p := artifact.AppendSystemPrompt
	for _, want := range []string{"D:\\ws\\proj", "claude-sonnet-4-5", "plan"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "20") && strings.Contains(p, ":") && strings.Contains(p, "T") {
		t.Fatalf("system prompt must not embed a timestamp:\n%s", p)
	}
}

// TestComposeMCPConfigShape pins the --mcp-config JSON shape: mcpServers map,
// stdio defs with command/args/env, names in sorted order at byte level.
func TestComposeMCPConfigShape(t *testing.T) {
	cfg := baseCfg()
	reg := fakeRegistry{servers: []MCPServer{
		{Name: "zeta", Command: []string{"z"}},
		{Name: "alpha", Command: []string{"npx", "-y", "pkg"}, Env: map[string]string{"B": "2", "A": "1"}},
	}}
	artifact, err := Compose(cfg, "w", reg)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Type    string            `json:"type"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(artifact.MCPConfigJSON), &parsed); err != nil {
		t.Fatalf("mcp-config not json: %v\n%s", err, artifact.MCPConfigJSON)
	}
	if len(parsed.MCPServers) != 2 {
		t.Fatalf("got %d servers, want 2", len(parsed.MCPServers))
	}
	alpha := parsed.MCPServers["alpha"]
	if alpha.Command != "npx" || !reflect.DeepEqual(alpha.Args, []string{"-y", "pkg"}) {
		t.Fatalf("alpha def wrong: %+v", alpha)
	}
	if alpha.Env["A"] != "1" || alpha.Env["B"] != "2" {
		t.Fatalf("alpha env wrong: %v", alpha.Env)
	}
	// 字节级：alpha 排在 zeta 前（名字排序），env 键 A 在 B 前（键排序）
	aIdx := strings.Index(artifact.MCPConfigJSON, `"alpha"`)
	zIdx := strings.Index(artifact.MCPConfigJSON, `"zeta"`)
	if aIdx < 0 || zIdx < 0 || aIdx > zIdx {
		t.Fatalf("servers not name-sorted in bytes: %s", artifact.MCPConfigJSON)
	}
	envA := strings.Index(artifact.MCPConfigJSON, `"A":"1"`)
	envB := strings.Index(artifact.MCPConfigJSON, `"B":"2"`)
	if envA < 0 || envB < 0 || envA > envB {
		t.Fatalf("env keys not sorted in bytes: %s", artifact.MCPConfigJSON)
	}
}

// TestComposePermissionModeMapping pins the preset → engine --permission-mode
// mapping (design §9: preset 是 mulix 语义；engine 侧取最严格模式，危险预设由
// Go 策略层叠加放行).
func TestComposePermissionModeMapping(t *testing.T) {
	cases := map[string]string{
		"default":     "default",
		"acceptEdits": "acceptEdits",
		"plan":        "plan",
		"danger-full": "default", // 引擎保持最严格审批模式；放行由 mulix 策略层在权限工具里做
	}
	for preset, want := range cases {
		cfg := baseCfg()
		cfg.Permissions.Preset = preset
		artifact, err := Compose(cfg, "w", fakeRegistry{})
		if err != nil {
			t.Fatalf("preset %s: %v", preset, err)
		}
		if artifact.PermissionMode != want {
			t.Fatalf("preset %s → %q, want %q", preset, artifact.PermissionMode, want)
		}
	}
	// 未知预设 fail-closed（boot 拒绝，而不是静默回退）
	cfg := baseCfg()
	cfg.Permissions.Preset = "bogus"
	if _, err := Compose(cfg, "w", fakeRegistry{}); err == nil {
		t.Fatal("unknown preset must fail compose")
	}
}

// TestComposeFingerprintSensitivity pins: 指纹对启动面变化敏感（可观测性有意义）。
func TestComposeFingerprintSensitivity(t *testing.T) {
	cfg1 := baseCfg()
	cfg2 := baseCfg()
	cfg2.DefaultModel = "claude-opus-4-6"
	a1, err := Compose(cfg1, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose 1: %v", err)
	}
	a2, err := Compose(cfg2, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose 2: %v", err)
	}
	if a1.Fingerprint == a2.Fingerprint {
		t.Fatal("fingerprint must change when the boot surface changes")
	}
	if len(a1.Fingerprint) != 16 {
		t.Fatalf("fingerprint = %q, want 16 hex chars", a1.Fingerprint)
	}
}

// TestComposeEmptyRegistry pins: 空 registry → mcpServers 空对象（仍合法 JSON）。
func TestComposeEmptyRegistry(t *testing.T) {
	artifact, err := Compose(baseCfg(), "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(artifact.MCPConfigJSON, `"mcpServers":{}`) {
		t.Fatalf("empty registry must yield empty mcpServers object: %s", artifact.MCPConfigJSON)
	}
}

// TestComposeRejectsBadServerDefs pins boundary validation (fail-closed boot):
// duplicate names and empty commands are rejected, never silently corrupted.
func TestComposeRejectsBadServerDefs(t *testing.T) {
	cfg := baseCfg()
	dup := fakeRegistry{servers: []MCPServer{
		{Name: "x", Command: []string{"a"}},
		{Name: "x", Command: []string{"b"}},
	}}
	if _, err := Compose(cfg, "w", dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate server name must fail compose, got %v", err)
	}
	emptyCmd := fakeRegistry{servers: []MCPServer{
		{Name: "x", Command: nil},
	}}
	if _, err := Compose(cfg, "w", emptyCmd); err == nil || !strings.Contains(err.Error(), "empty command") {
		t.Fatalf("empty command must fail compose, got %v", err)
	}
}

// TestComposeListOrderAndDedup pins: allow/deny keep config's explicit order
// (multi-element) and dedupe preserves first occurrence.
func TestComposeListOrderAndDedup(t *testing.T) {
	cfg := baseCfg()
	cfg.Permissions.Allow = []string{"Bash(go vet:*)", "Edit(docs/**)", "Bash(go test:*)", "Edit(docs/**)"}
	cfg.Permissions.Deny = []string{"Bash(rm -rf*)", "Bash(git push*)", "Bash(rm -rf*)"}
	artifact, err := Compose(cfg, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	wantAllow := []string{"Bash(go vet:*)", "Edit(docs/**)", "Bash(go test:*)"}
	wantDeny := []string{"Bash(rm -rf*)", "Bash(git push*)"}
	if !reflect.DeepEqual(artifact.AllowedTools, wantAllow) {
		t.Fatalf("AllowedTools = %v, want %v", artifact.AllowedTools, wantAllow)
	}
	if !reflect.DeepEqual(artifact.DisallowedTools, wantDeny) {
		t.Fatalf("DisallowedTools = %v, want %v", artifact.DisallowedTools, wantDeny)
	}
}

// TestComposeSystemPromptGolden pins the rendered C1 snapshot byte-exactly —
// the strongest form of the anti-timestamp/no-drift pin (template changes are
// Cache-impact-gated).
func TestComposeSystemPromptGolden(t *testing.T) {
	artifact, err := Compose(baseCfg(), "D:\\ws\\demo", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	want := "You are running inside mulix, a coding-agent harness.\n\n" +
		"Startup snapshot (frozen for this session):\n" +
		"- Workspace: D:\\ws\\demo\n" +
		"- Engine model: claude-sonnet-4-5\n" +
		"- Permission mode: default\n\n" +
		"Rules:\n" +
		"- Dynamic context (memory recall, runtime state) is appended by mulix at the tail of user messages; it never appears in this prompt.\n" +
		"- Tool approvals flow through mulix's permission queue with a fail-closed timeout; unapproved tools are denied by the engine.\n" +
		"- Keep the first response turn identical in structure: greets only when greeted."
	if artifact.AppendSystemPrompt != want {
		t.Fatalf("golden mismatch:\n got: %q\nwant: %q", artifact.AppendSystemPrompt, want)
	}
}
