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

// ---- M14/Task 7：外部 MCP server 配置透传 ----

// parsedMCPConfig 是外部条目测试的 mcp-config 解析形状（P1 形状测试同款）。
type parsedMCPConfig struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"mcpServers"`
}

func parseMCPConfig(t *testing.T, raw string) parsedMCPConfig {
	t.Helper()
	var parsed parsedMCPConfig
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("mcp-config not json: %v\n%s", err, raw)
	}
	return parsed
}

// TestComposeMergesExternalServers 钉 M14 ①：两个外部 server + 内置 mulix →
// mcp-config 恰 3 条目、名称排序字节序（沿用 P1 composeMCPConfig 的 map 序列化
// 确定性——不实现自定义顺序序列化），两次 Compose 逐字节一致（SC-003 护栏
// 扩展到外部条目）。输出条目形状沿 P1：{"type":"stdio","command","args","env"}。
func TestComposeMergesExternalServers(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.Servers = []config.MCPServerDecl{
		{Name: "zeta", Command: []string{"npx", "-y", "@zeta/mcp"}},
		{Name: "alpha", Command: []string{"python", "-m", "alpha_mcp"}, Env: map[string]string{"A": "1"}},
	}
	reg := fakeRegistry{servers: []MCPServer{{Name: "mulix", Command: []string{"mulix", "mcp", "serve"}}}}
	a1, err := Compose(cfg, "w", reg)
	if err != nil {
		t.Fatalf("compose 1: %v", err)
	}
	a2, err := Compose(cfg, "w", reg)
	if err != nil {
		t.Fatalf("compose 2: %v", err)
	}
	if a1.MCPConfigJSON != a2.MCPConfigJSON {
		t.Fatalf("MCPConfigJSON not byte-stable with external entries:\n%s\n---\n%s", a1.MCPConfigJSON, a2.MCPConfigJSON)
	}
	if a1.Fingerprint != a2.Fingerprint {
		t.Fatal("Fingerprint not byte-stable with external entries")
	}
	parsed := parseMCPConfig(t, a1.MCPConfigJSON)
	if len(parsed.MCPServers) != 3 {
		t.Fatalf("got %d servers, want 3 (2 external + built-in mulix): %s", len(parsed.MCPServers), a1.MCPConfigJSON)
	}
	// 字节级：名称排序 alpha < mulix < zeta
	iAlpha := strings.Index(a1.MCPConfigJSON, `"alpha"`)
	iMulix := strings.Index(a1.MCPConfigJSON, `"mulix"`)
	iZeta := strings.Index(a1.MCPConfigJSON, `"zeta"`)
	if iAlpha < 0 || iMulix < 0 || iZeta < 0 || iAlpha > iMulix || iMulix > iZeta {
		t.Fatalf("servers not name-sorted in bytes: %s", a1.MCPConfigJSON)
	}
	// 外部条目形状（registry 条目同款：stdio + command/args + env 静态值）
	a := parsed.MCPServers["alpha"]
	if a.Type != "stdio" || a.Command != "python" ||
		!reflect.DeepEqual(a.Args, []string{"-m", "alpha_mcp"}) || a.Env["A"] != "1" {
		t.Fatalf("alpha def wrong: %+v", a)
	}
	if z := parsed.MCPServers["zeta"]; z.Command != "npx" || !reflect.DeepEqual(z.Args, []string{"-y", "@zeta/mcp"}) {
		t.Fatalf("zeta def wrong: %+v", z)
	}
	if m := parsed.MCPServers["mulix"]; m.Command != "mulix" || !reflect.DeepEqual(m.Args, []string{"mcp", "serve"}) {
		t.Fatalf("built-in mulix def wrong: %+v", m)
	}
}

// TestComposeRejectsReservedMulixName 钉 M14 ②：外部条目占用内置保留名 mulix
// → Compose error（fail-closed，绝不静默覆盖/共存——design 002 §5「内置名
// mulix 保留」）。有内置条目时是重名冲突；无内置条目（空注册面）时保留名
// 同样拒绝。
func TestComposeRejectsReservedMulixName(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.Servers = []config.MCPServerDecl{{Name: "mulix", Command: []string{"evil"}}}
	builtin := fakeRegistry{servers: []MCPServer{{Name: "mulix", Command: []string{"mulix", "mcp", "serve"}}}}
	if _, err := Compose(cfg, "w", builtin); err == nil {
		t.Fatal("external server named mulix must fail compose (built-in present)")
	}
	if _, err := Compose(cfg, "w", fakeRegistry{}); err == nil {
		t.Fatal("reserved name mulix must fail compose even without the built-in entry")
	}
}

// TestComposeRejectsEmptyExternalCommand 钉 M14 ③：外部条目 command 空 →
// Compose error（与 P1 registry 条目的空 command 校验同一错误面）。
func TestComposeRejectsEmptyExternalCommand(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.Servers = []config.MCPServerDecl{{Name: "broken", Command: nil}}
	if _, err := Compose(cfg, "w", fakeRegistry{}); err == nil || !strings.Contains(err.Error(), "empty command") {
		t.Fatalf("empty external command must fail compose, got %v", err)
	}
}

// TestComposeMCPToolTimeoutEnv 钉 M14 ④：timeout_ms 计算为引擎 env
// MCP_TOOL_TIMEOUT（Boot Env 通道面——与回调地址同款 "KEY=VALUE" 串）；
// 多 server 取最小值、单 server 用其声明值、未声明用默认 60000；
// 超时绝不进 mcp-config 字节（SC-003 同款裁定：mcp-config 面只含 server 定义），
// 但入启动面指纹（BootArtifact 新字段必须进摘要——fingerprint 维护注）。
func TestComposeMCPToolTimeoutEnv(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.Servers = []config.MCPServerDecl{
		{Name: "slow", Command: []string{"s"}, TimeoutMS: 120000},
		{Name: "fast", Command: []string{"f"}, TimeoutMS: 30000},
	}
	art, err := Compose(cfg, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose multi: %v", err)
	}
	if want := []string{"MCP_TOOL_TIMEOUT=30000"}; !reflect.DeepEqual(art.EngineEnv, want) {
		t.Fatalf("EngineEnv = %v, want %v (min across servers)", art.EngineEnv, want)
	}
	if strings.Contains(art.MCPConfigJSON, "MCP_TOOL_TIMEOUT") {
		t.Fatalf("MCP_TOOL_TIMEOUT must never enter mcp-config bytes: %s", art.MCPConfigJSON)
	}
	solo := baseCfg()
	solo.MCP.Servers = []config.MCPServerDecl{{Name: "one", Command: []string{"x"}, TimeoutMS: 45000}}
	art2, err := Compose(solo, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose single: %v", err)
	}
	if want := []string{"MCP_TOOL_TIMEOUT=45000"}; !reflect.DeepEqual(art2.EngineEnv, want) {
		t.Fatalf("EngineEnv = %v, want %v (single server declared value)", art2.EngineEnv, want)
	}
	def := baseCfg()
	def.MCP.Servers = []config.MCPServerDecl{{Name: "d", Command: []string{"x"}}}
	art3, err := Compose(def, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose default: %v", err)
	}
	if want := []string{"MCP_TOOL_TIMEOUT=60000"}; !reflect.DeepEqual(art3.EngineEnv, want) {
		t.Fatalf("EngineEnv = %v, want %v (default 60000)", art3.EngineEnv, want)
	}
	// 仅 timeout 不同（mcp-config 字节不变）→ 指纹必须变化（env 入启动面摘要）
	onlyTimeout := baseCfg()
	onlyTimeout.MCP.Servers = []config.MCPServerDecl{
		{Name: "slow", Command: []string{"s"}, TimeoutMS: 120000},
		{Name: "fast", Command: []string{"f"}, TimeoutMS: 31000},
	}
	art4, err := Compose(onlyTimeout, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose timeout-only delta: %v", err)
	}
	if art4.Fingerprint == art.Fingerprint {
		t.Fatal("fingerprint must react to MCP_TOOL_TIMEOUT (launch-face field)")
	}
}

// TestComposeExternalEnvStaticPassthrough 钉 M14 ⑤：外部条目 env 静态透传进
// mcp-config（与声明逐键一致——mulix 不做运行期注入，终评 Important-1 教训）；
// 未声明 env 的条目无 env 键（P1 registry 条目同款形状）。
func TestComposeExternalEnvStaticPassthrough(t *testing.T) {
	cfg := baseCfg()
	env := map[string]string{"GITHUB_TOKEN": "t0k", "LOG_LEVEL": "debug"}
	cfg.MCP.Servers = []config.MCPServerDecl{{Name: "gh", Command: []string{"npx", "-y", "gh-mcp"}, Env: env}}
	art, err := Compose(cfg, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	parsed := parseMCPConfig(t, art.MCPConfigJSON)
	gh := parsed.MCPServers["gh"]
	if !reflect.DeepEqual(gh.Env, env) {
		t.Fatalf("external env must pass through statically: got %v, want %v", gh.Env, env)
	}
	plain := baseCfg()
	plain.MCP.Servers = []config.MCPServerDecl{{Name: "plain", Command: []string{"p"}}}
	art2, err := Compose(plain, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose plain: %v", err)
	}
	parsed2 := parseMCPConfig(t, art2.MCPConfigJSON)
	if e := parsed2.MCPServers["plain"].Env; len(e) != 0 {
		t.Fatalf("decl without env must not carry an env key: %v", e)
	}
}

// TestComposeMCPToolTimeoutRespectsLargeDeclaredValues 钉评审修复轮 1
// Important-1：60000 是 per-server 缺省兜底，**不是封顶**——最小值在全部外部
// server 的生效值里取真实 min（修复前 min 以 60000 为种子只减不增，把更大的
// 声明值钳回 60000，违反 brief ④「单 server 用其声明值」）。
func TestComposeMCPToolTimeoutRespectsLargeDeclaredValues(t *testing.T) {
	solo := baseCfg()
	solo.MCP.Servers = []config.MCPServerDecl{{Name: "solo", Command: []string{"x"}, TimeoutMS: 120000}}
	art, err := Compose(solo, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose single large: %v", err)
	}
	if want := []string{"MCP_TOOL_TIMEOUT=120000"}; !reflect.DeepEqual(art.EngineEnv, want) {
		t.Fatalf("EngineEnv = %v, want %v (single declared value above default)", art.EngineEnv, want)
	}
	both := baseCfg()
	both.MCP.Servers = []config.MCPServerDecl{
		{Name: "a", Command: []string{"a"}, TimeoutMS: 90000},
		{Name: "b", Command: []string{"b"}, TimeoutMS: 120000},
	}
	art2, err := Compose(both, "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose multi large: %v", err)
	}
	if want := []string{"MCP_TOOL_TIMEOUT=90000"}; !reflect.DeepEqual(art2.EngineEnv, want) {
		t.Fatalf("EngineEnv = %v, want %v (true min of declared values)", art2.EngineEnv, want)
	}
}

// TestComposeNoExternalServersEngineEnvEmpty 钉评审修复轮 1 F3（控制器裁定，
// 撤销 R-T7-1 的「恒透出默认」）：无外部声明 → EngineEnv 为 nil——P1-only
// boot.Env 零 delta；空面形状 = nil（与 AllowedTools 空面 nil 的既有指纹
// canonical 约定一致，此处钉死）。内置 mulix 条目单独在场也不透出（超时是
// 外部声明面的属性）。
func TestComposeNoExternalServersEngineEnvEmpty(t *testing.T) {
	empty, err := Compose(baseCfg(), "w", fakeRegistry{})
	if err != nil {
		t.Fatalf("compose empty registry: %v", err)
	}
	if empty.EngineEnv != nil {
		t.Fatalf("EngineEnv = %v, want nil (no external servers = zero boot env delta)", empty.EngineEnv)
	}
	builtinOnly := fakeRegistry{servers: []MCPServer{{Name: "mulix", Command: []string{"mulix", "mcp", "serve"}}}}
	art, err := Compose(baseCfg(), "w", builtinOnly)
	if err != nil {
		t.Fatalf("compose builtin only: %v", err)
	}
	if art.EngineEnv != nil {
		t.Fatalf("EngineEnv = %v, want nil (built-in entry alone must not emit MCP_TOOL_TIMEOUT)", art.EngineEnv)
	}
}

// TestComposeRejectsDuplicateExternalNames 钉 F4a（评审要求的回归 pin——行为
// 已由 P1 共享校验循环保证，评审裁定补钉防回归）：两个同名外部条目 → error。
func TestComposeRejectsDuplicateExternalNames(t *testing.T) {
	cfg := baseCfg()
	cfg.MCP.Servers = []config.MCPServerDecl{
		{Name: "dup", Command: []string{"a"}},
		{Name: "dup", Command: []string{"b"}},
	}
	if _, err := Compose(cfg, "w", fakeRegistry{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate external names must fail compose, got %v", err)
	}
}
