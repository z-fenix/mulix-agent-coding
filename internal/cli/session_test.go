package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
