package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/sessionx"
)

// ---- 会话编排：resume 定位 / 恢复引导 / 无人值守实跑 ----

func testOptions(t *testing.T) (Options, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	return Options{
		Stdout: &out, Stderr: &errOut,
		Workspace:    t.TempDir(),
		SessionsRoot: t.TempDir(),
		UsageRoot:    t.TempDir(),
	}, &out, &errOut
}

// seedSession 建一个会话镜像并写 meta（含引擎 id 可选）。
func seedSession(t *testing.T, root, id, engineID string) *sessionx.Store {
	t.Helper()
	s, err := sessionx.Create(root, sessionx.Options{ID: id, Workspace: "w", Model: "m", EngineID: engineID})
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestResolveResume pins（--resume 定位矩阵）：
//   - 无值 → 当前 workspace 最新会话的镜像引擎 id；
//   - 有值 → FindByEngineID（限本 workspace）；
//   - 引擎 id 为空的镜像 → 拒绝（Resume("") 会静默新开引擎会话——实测陷阱，
//     claudehost 对空 id 跳过 --resume）；
//   - 未命中 → 明确报错。
func TestResolveResume(t *testing.T) {
	root := t.TempDir()
	// 会话无引擎 id（引擎 init 未到）→ 拒绝
	seedSession(t, root, "s1", "")
	opts := Options{Workspace: "w", SessionsRoot: root}
	if _, _, err := resolveResume("", opts); err == nil || !strings.Contains(err.Error(), "没有可续链") {
		t.Fatalf("empty engine id = %v, want 拒绝", err)
	}
	// 有引擎 id 后可解析
	s := seedSession(t, root, "s2", "engine-xyz")
	defer s.Close()
	id, dir, err := resolveResume("", opts)
	if err != nil || id != "engine-xyz" || !strings.Contains(dir, "s2") {
		t.Fatalf("latest = (%q, %q, %v)", id, dir, err)
	}
	// 显式值命中
	id, dir, err = resolveResume("engine-xyz", opts)
	if err != nil || id != "engine-xyz" || !strings.Contains(dir, "s2") {
		t.Fatalf("by id = (%q, %q, %v)", id, dir, err)
	}
	// 显式值未命中
	if _, _, err := resolveResume("engine-nope", opts); err == nil {
		t.Fatal("miss must error")
	}
}

// TestRecoveryGuard pins：最新会话存在未收口回合（TurnStarted 无 TurnDone）
// 时启动新会话被拒并指引 --resume。
func TestRecoveryGuard(t *testing.T) {
	root := t.TempDir()
	s := seedSession(t, root, "s-open", "engine-open")
	if err := s.Append(event.TurnStartedEvent{Seq: 1, SessionID: "s-open"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	opts := Options{Workspace: "w", SessionsRoot: root}
	if err := checkRecovery(opts); err == nil || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("recovery = %v, want 指引 --resume", err)
	}
	// 收口后放行
	if err := s.Append(event.TurnDoneEvent{Seq: 2, Turn: 1}); err != nil {
		t.Fatalf("append done: %v", err)
	}
	if err := checkRecovery(opts); err != nil {
		t.Fatalf("closed turn must pass: %v", err)
	}
}

// TestRunUnattendedE2E pins：`run` 全链路接线（FakeHost 注入）——用户输入
// 进引擎、助手文本出 stdout、回合收口、退出码 0；权限回路面（prompt tool +
// mcp-config mulix 条目）在无人值守同样接线。
func TestRunUnattendedE2E(t *testing.T) {
	opts, out, errOut := testOptions(t)
	opts.HostFactory = func(boot engine.BootConfig) (engine.Host, error) {
		if boot.PermissionPromptTool != "mcp__mulix__permission" {
			t.Errorf("prompt tool = %q, want wired", boot.PermissionPromptTool)
		}
		if !strings.Contains(boot.MCPConfigJSON, "mulix") {
			t.Errorf("mcp config = %q, want mulix entry", boot.MCPConfigJSON)
		}
		if boot.SettingsJSON != `{"permissions":{"ask":["Bash","PowerShell"]}}` {
			t.Errorf("settings json = %q, want governance ask face", boot.SettingsJSON)
		}
		script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
		if err != nil {
			t.Errorf("fixture: %v", err)
		}
		return engine.NewFakeHost(script), nil
	}
	code := RunPrompt(context.Background(), opts, "hi there", "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "happy.txt") {
		t.Fatalf("stdout = %q, want assistant text", out.String())
	}
}

// TestRunExitCodeOnErrorTurn pins（T27 评审 Important-2）：IsError 收口的
// 回合 → RunPrompt 退出码 1，stderr/stdout 带失败标记（无人值守/CI 可判定
// 回合失败）；正常回合退出码 0 的对照由 TestRunUnattendedE2E 钉。
func TestRunExitCodeOnErrorTurn(t *testing.T) {
	opts, out, errOut := testOptions(t)
	opts.HostFactory = func(boot engine.BootConfig) (engine.Host, error) {
		return engine.NewFakeHost(engine.FakeScript{
			Turns: [][]engine.Event{{
				{Kind: engine.KindResult, SessionID: "e-err", UUID: "r-err",
					Result: &engine.ResultInfo{SubType: "error", IsError: true,
						ResultText: "boom", Usage: engine.Usage{OutputTokens: 1}}},
			}},
		}), nil
	}
	if code := RunPrompt(context.Background(), opts, "hi", ""); code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "回合失败") {
		t.Fatalf("stdout = %q, want 失败标记", out.String())
	}
}

// canUseToolEvent 构造引擎 can_use_tool 控制请求（与 control routing 测试
// 同构；cli 面复验配置规则接线——终评冒烟发现的缺口）。
func canUseToolEvent(requestID, toolUseID, tool, input string) engine.Event {
	args := map[string]any{"tool_name": tool, "tool_use_id": toolUseID}
	if input != "" {
		args["input"] = json.RawMessage(input)
	}
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID + "-mcp",
		"method":  "tools/call",
		"params":  map[string]any{"name": "permission", "arguments": args},
	})
	return engine.Event{Kind: engine.KindControlRequest, SessionID: "s", Control: &engine.ControlRequest{
		RequestID: requestID,
		Request:   engine.ControlRequestBody{SubType: "mcp_message", ServerName: "mulix", Message: msg},
	}}
}

// behaviorOf 提取控制应答的 behavior JSON（与 control routing 测试同构）。
func behaviorOfResp(t *testing.T, resp engine.ControlResponse) map[string]any {
	t.Helper()
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Response, &envelope); err != nil {
		t.Fatalf("response not MCP envelope: %v (%s)", err, resp.Response)
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatalf("empty MCP content: %s", resp.Response)
	}
	var behavior map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &behavior); err != nil {
		t.Fatalf("content text not behavior JSON: %v", err)
	}
	return behavior
}

// TestConfigAllowRulesGateRound pins（终评冒烟发现）：config permissions.allow
// 必须接入 guard 策略——此前恒为 nil，无人值守下写操作全被 fail-closed 拒绝
// （真实引擎空转 deny 循环，mulix run 冒烟 16 分钟无产出）。
// 口径：allow=["Write"] 的无人值守回合里，can_use_tool(Write) 经 policy 直判
// allow（无 Approver、无拒绝回执、回合正常收口 exit 0）。
func TestConfigAllowRulesGateRound(t *testing.T) {
	opts, _, errOut := testOptions(t)
	cfg := defaultTestConfig(t)
	cfg.Permissions.Allow = []string{"Write"}
	opts.Config = &cfg
	var fake *engine.FakeHost
	opts.HostFactory = func(boot engine.BootConfig) (engine.Host, error) {
		script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
		if err != nil {
			return nil, err
		}
		turn := script.Turns[0]
		withCR := make([]engine.Event, 0, len(turn)+1)
		for _, ev := range turn {
			if ev.Kind == engine.KindResult {
				withCR = append(withCR, canUseToolEvent("req-cfg", "tu-cfg", "Write", `{"file_path":"x.txt"}`))
			}
			withCR = append(withCR, ev)
		}
		script.Turns = [][]engine.Event{withCR}
		fake = engine.NewFakeHost(script)
		return fake, nil
	}
	if code := RunPrompt(context.Background(), opts, "hi", ""); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	found := false
	for _, r := range fake.Responses() {
		if r.ID == "req-cfg" {
			found = true
			if b := behaviorOfResp(t, r.Payload); b["behavior"] != "allow" {
				t.Fatalf("Write must be policy-allowed via config rules, got %v", b["behavior"])
			}
		}
	}
	if !found {
		t.Fatal("control response for req-cfg must be recorded")
	}
}

// TestConfigUnmatchedToolDeniesUnderUnattended pins：未命中 allow 的工具在
// 无人值守下 fail-closed deny（allow 规则生效的对照面）。
func TestConfigUnmatchedToolDeniesUnderUnattended(t *testing.T) {
	opts, _, errOut := testOptions(t)
	cfg := defaultTestConfig(t)
	cfg.Permissions.Allow = []string{"Write"}
	opts.Config = &cfg
	var fake *engine.FakeHost
	opts.HostFactory = func(boot engine.BootConfig) (engine.Host, error) {
		script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
		if err != nil {
			return nil, err
		}
		turn := script.Turns[0]
		withCR := make([]engine.Event, 0, len(turn)+1)
		for _, ev := range turn {
			if ev.Kind == engine.KindResult {
				withCR = append(withCR, canUseToolEvent("req-den", "tu-den", "WebFetch", `{"url":"https://x"}`))
			}
			withCR = append(withCR, ev)
		}
		script.Turns = [][]engine.Event{withCR}
		fake = engine.NewFakeHost(script)
		return fake, nil
	}
	if code := RunPrompt(context.Background(), opts, "hi", ""); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	for _, r := range fake.Responses() {
		if r.ID == "req-den" {
			if b := behaviorOfResp(t, r.Payload); b["behavior"] != "deny" {
				t.Fatalf("unmatched tool under unattended must deny, got %v", b["behavior"])
			}
		}
	}
}

// TestConfigBadRuleFailsClosed pins：坏规则字符串 → 会话构建失败（拒绝启动，
// 绝不静默丢弃规则——exit 2 配置错误）。
func TestConfigBadRuleFailsClosed(t *testing.T) {
	opts, _, errOut := testOptions(t)
	cfg := defaultTestConfig(t)
	cfg.Permissions.Allow = []string{"Bad Tool"}
	opts.Config = &cfg
	opts.HostFactory = func(boot engine.BootConfig) (engine.Host, error) {
		return engine.NewFakeHost(engine.FakeScript{}), nil
	}
	if code := RunPrompt(context.Background(), opts, "hi", ""); code != 2 {
		t.Fatalf("exit = %d, want 2 (config error); stderr: %s", code, errOut.String())
	}
}

// defaultTestConfig 载入默认分层（无文件 → 内置默认）——规则接线测试的
// 配置基座。
func defaultTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return *cfg
}
