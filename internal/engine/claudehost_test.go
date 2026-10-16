package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- 假 CLI（Go helper-process 惯用法：测试二进制自充当引擎） ----

// TestMain：带 MULIX_FAKE_CLI_MODE=1 时本进程是「假 CLI」；否则正常跑测试。
func TestMain(m *testing.M) {
	if os.Getenv("MULIX_FAKE_CLI_MODE") == "1" {
		runFakeCLI()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeScript struct {
	SessionID         string   `json:"session_id"`
	Version           string   `json:"version"`
	Model             string   `json:"model"`
	PermissionMode    string   `json:"permission_mode"`
	BeforeInput       []string `json:"before_input"`
	PerInput          []string `json:"per_input"`
	StderrLines       []string `json:"stderr_lines"`
	GarbageLine       string   `json:"garbage_line"`
	EchoStdinToStderr bool     `json:"echo_stdin_to_stderr"`
	HangOnEOF         bool     `json:"hang_on_eof"`
	// DeferInitToInput（终评协议适配）：真实 claude 2.1.291 的 init 在首条
	// user message 之后才发出——置位时 initLine 延迟到首个 stdin 输入。
	DeferInitToInput bool `json:"defer_init_to_input"`
}

func runFakeCLI() {
	raw, err := os.ReadFile(os.Getenv("MULIX_FAKE_CLI_SCRIPT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakecli:", err)
		os.Exit(3)
	}
	var script fakeScript
	if err := json.Unmarshal(raw, &script); err != nil {
		fmt.Fprintln(os.Stderr, "fakecli:", err)
		os.Exit(3)
	}
	if script.SessionID == "" {
		script.SessionID = "f0000000-0000-4000-8000-000000000001"
	}
	if script.Version == "" {
		script.Version = "2.1.291"
	}
	if script.Model == "" {
		script.Model = "fake-model"
	}
	if script.PermissionMode == "" {
		script.PermissionMode = "default"
	}

	initLine := fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q,"cwd":".","tools":["Bash","Write"],"mcp_servers":[{"name":"fake","status":"connected","source":"user"}],"model":%q,"permissionMode":%q,"apiKeySource":"none","claude_code_version":%q,"uuid":"u-init"}`,
		script.SessionID, script.Model, script.PermissionMode, script.Version)

	for _, l := range script.StderrLines {
		fmt.Fprintln(os.Stderr, l)
	}
	if script.GarbageLine != "" {
		fmt.Fprintln(os.Stdout, script.GarbageLine)
	}
	// 忠实真实引擎顺序（fixture 实证）：hook 对在 init 之前
	for _, l := range script.BeforeInput {
		fmt.Fprintln(os.Stdout, l)
	}
	if !script.DeferInitToInput {
		fmt.Fprintln(os.Stdout, initLine)
	}

	sc := newLargeScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Bytes()
		if script.EchoStdinToStderr {
			fmt.Fprintf(os.Stderr, "STDIN:%s\n", line)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &probe) == nil && probe.Type == "user" {
			if script.DeferInitToInput {
				fmt.Fprintln(os.Stdout, initLine)
			}
			for _, l := range script.PerInput {
				fmt.Fprintln(os.Stdout, l)
			}
		}
	}
	if script.HangOnEOF {
		time.Sleep(time.Hour) // 挂起等杀（select{} 会触发死锁检测）
	}
}

// ---- 测试助手 ----

func testLockPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.lock")
	lock := `{"cli_version":"2.1.291","protocol_probe":{"init_version_field":"claude_code_version"},"pinned_at":"2026-10-06","baseline":{"cache_hit_rate":0.0}}`
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	return path
}

func fakeEnv(t *testing.T, scriptFile string) []string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", scriptFile))
	if err != nil {
		t.Fatalf("script path: %v", err)
	}
	return []string{"MULIX_FAKE_CLI_MODE=1", "MULIX_FAKE_CLI_SCRIPT=" + abs, "FAKE_CREDENTIAL=supersecret99"}
}

func newTestHost(t *testing.T, scriptFile string, mutate func(*BootConfig, *ClaudeHostOptions)) *ClaudeHost {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	cfg := BootConfig{
		EnginePath:     exe,
		Workspace:      t.TempDir(),
		PermissionMode: "default",
		SettingSources: []string{"user", "project"},
		Env:            fakeEnv(t, scriptFile),
	}
	opts := ClaudeHostOptions{LockPath: testLockPath(t)}
	if mutate != nil {
		mutate(&cfg, &opts)
	}
	h := NewClaudeHost(opts)
	h.cfg = cfg // Start 前预置（测试直接用 h.cfg 调 Start）
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

// collectEvents 带超时地从通道收集 n 个事件。
func collectEvents(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	var got []Event
	deadline := time.After(10 * time.Second)
	for len(got) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("events closed after %d/%d events", len(got), n)
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout collecting events: %d/%d", len(got), n)
		}
	}
	return got
}

// ---- Step 1 规格护栏测试 ----

// TestStartRequiresExplicitPermissionMode pins：--permission-mode 必须显式
// （实测：用户环境默认 auto 会绕过审批——fail-closed 拒绝启动）。
func TestStartRequiresExplicitPermissionMode(t *testing.T) {
	h := newTestHost(t, "fake-happy.json", func(c *BootConfig, o *ClaudeHostOptions) {
		c.PermissionMode = ""
	})
	err := h.Start(context.Background(), h.cfg)
	if err == nil || !strings.Contains(err.Error(), "permission-mode") {
		t.Fatalf("empty permission mode must fail start, got %v", err)
	}
}

// TestStartRequiresUserSettingSource pins：--setting-sources 必须含 user 作用域
// （实测：仅 project 时凭据不加载 → Not logged in）。
func TestStartRequiresUserSettingSource(t *testing.T) {
	h := newTestHost(t, "fake-happy.json", func(c *BootConfig, o *ClaudeHostOptions) {
		c.SettingSources = []string{"project"}
	})
	err := h.Start(context.Background(), h.cfg)
	if err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("setting-sources without user must fail start, got %v", err)
	}
}

// TestStartRefusesVersionMismatch pins FR-003：版本不匹配默认拒绝启动（命名测试）。
func TestStartRefusesVersionMismatch(t *testing.T) {
	h := newTestHost(t, "fake-drift.json", func(c *BootConfig, o *ClaudeHostOptions) {
		c.AllowEngineDrift = false
	})
	err := h.Start(context.Background(), h.cfg)
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("version mismatch must refuse start, got %v", err)
	}
	// 拒绝后不得留僵尸进程（Start 失败路径必须清理）
	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("close after refused start: %v", err)
	}
}

// TestStartAllowsDriftWithTrace pins FR-003/SC-005：显式放行留痕——
// trace 事件（code=engine_drift，含 lock/actual 版本）进入事件流（control
// 将其持久化为镜像留痕事件）。
func TestStartAllowsDriftWithTrace(t *testing.T) {
	h := newTestHost(t, "fake-drift.json", func(c *BootConfig, o *ClaudeHostOptions) {
		c.AllowEngineDrift = true
	})
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("drift allowed start: %v", err)
	}
	events := collectEvents(t, h.Events(), 1)
	var trace *Event
	for _, ev := range events {
		if ev.Kind == KindTrace {
			trace = &ev
		}
	}
	if trace == nil || trace.Trace == nil || trace.Trace.Code != "engine_drift" {
		t.Fatalf("drift trace event missing: %+v", events)
	}
	var details struct {
		LockVersion   string `json:"lock_version"`
		ActualVersion string `json:"actual_version"`
	}
	if err := json.Unmarshal(trace.Trace.Details, &details); err != nil {
		t.Fatalf("trace details: %v", err)
	}
	if details.LockVersion != "2.1.291" || details.ActualVersion != "9.9.9" {
		t.Fatalf("trace details wrong: %+v", details)
	}
	// Info 反映实际引擎版本与 lock 探针指纹
	info := h.Info()
	if info.CLIVersion != "9.9.9" || info.Fingerprint != "claude_code_version" {
		t.Fatalf("info after drift: %+v", info)
	}
}

// TestSpawnEventsSendAndGracefulClose pins：spawn + 初始化握手 + 事件泵 +
// Send wire + 优雅关闭（EOF → 自然退出 → exit 事件）。
func TestSpawnEventsSendAndGracefulClose(t *testing.T) {
	h := newTestHost(t, "fake-happy.json", nil)
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	events := collectEvents(t, h.Events(), 3) // hook_started, hook_response, init（fixture 序）
	if events[0].Kind != KindHookStarted || events[1].Kind != KindHookResponse || events[2].Kind != KindInit {
		t.Fatalf("handshake order wrong: %s/%s/%s", events[0].Kind, events[1].Kind, events[2].Kind)
	}
	info := h.Info()
	if info.SessionID != "11111111-2222-4333-8444-555555555555" || info.CLIVersion != "2.1.291" ||
		info.Model != "fake-model" {
		t.Fatalf("info wrong: %+v", info)
	}

	if err := h.Send(context.Background(), UserMessage{Text: "hello", TurnTail: []string{"runtime-state block"}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	resultEvents := collectEvents(t, h.Events(), 2) // assistant + result
	if resultEvents[0].Kind != KindAssistant || resultEvents[1].Kind != KindResult {
		t.Fatalf("turn events wrong: %s/%s", resultEvents[0].Kind, resultEvents[1].Kind)
	}
	if got := resultEvents[1].Result.ResultText; got != "fake reply" {
		t.Fatalf("result text = %q", got)
	}

	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	// 关闭后通道收尾：排空缓冲直到 exit 事件，随后通道关闭
	waitExit(t, h.Events(), "graceful")
}

// waitExit 排空事件直到 KindExit（校验 reason）或通道关闭。
func waitExit(t *testing.T, ch <-chan Event, wantReason string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("exit event expected before channel close")
			}
			if ev.Kind == KindExit {
				if ev.Exit == nil || ev.Exit.Reason != wantReason {
					t.Fatalf("exit event wrong: %+v", ev)
				}
				// exit 之后通道应随即关闭
				if _, ok := <-ch; ok {
					t.Fatal("events must be closed after exit")
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting exit event")
		}
	}
}

// TestStartProvisionalWithoutInit pins（终评协议适配 Critical-1）：真实
// claude 2.1.291 的 init 在首条 user message 之后才发出——spawn 窗口内无
// init 且进程存活 → Start 临时放行（provisional）；首个 init 随首回合到达
// 后照常校验/验收（Info 填充、事件直通）。
func TestStartProvisionalWithoutInit(t *testing.T) {
	h := newTestHost(t, "fake-lateinit.json", func(c *BootConfig, o *ClaudeHostOptions) {
		o.HandshakeTimeout = 500 * time.Millisecond // 窗口快速过期
	})
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("provisional start must succeed: %v", err)
	}
	if info := h.Info(); info.SessionID != "" || info.CLIVersion != "" {
		t.Fatalf("info must be empty before init, got %+v", info)
	}
	// 首条消息触发 init → 校验通过 → 回合事件照常
	if err := h.Send(context.Background(), UserMessage{Text: "hello"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	events := collectEvents(t, h.Events(), 5) // hook 对（provisional 期 stash）→ init → assistant → result
	if events[0].Kind != KindHookStarted || events[1].Kind != KindHookResponse || events[2].Kind != KindInit ||
		events[3].Kind != KindAssistant || events[4].Kind != KindResult {
		t.Fatalf("post-provisional order wrong: %s/%s/%s/%s/%s",
			events[0].Kind, events[1].Kind, events[2].Kind, events[3].Kind, events[4].Kind)
	}
	if info := h.Info(); info.SessionID != "11111111-2222-4333-8444-555555555555" || info.CLIVersion != "2.1.291" {
		t.Fatalf("info after deferred init wrong: %+v", info)
	}
	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestDeferredDriftRefusal pins（终评协议适配）：provisional start 后首个
// init 校验失败（版本漂移且未放行）→ 终止引擎 + 以拒绝原因合成 exit 事件
// （fail-closed：SC-005 的拒绝语义在新协议下前移为「首轮拒绝」）。
func TestDeferredDriftRefusal(t *testing.T) {
	h := newTestHost(t, "fake-lateindrift.json", func(c *BootConfig, o *ClaudeHostOptions) {
		o.HandshakeTimeout = 500 * time.Millisecond
	})
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("provisional start must succeed: %v", err)
	}
	if err := h.Send(context.Background(), UserMessage{Text: "hello"}); err != nil {
		t.Fatalf("send: %v", err) // 消息已写——拒绝发生在 init 校验点
	}
	// 合成 exit（reason=refused: drift…）先于 monitor 的 killed exit
	waitExit(t, h.Events(), "refused: engine: version drift: lock 2.1.291, actual 9.9.9 (refusing; allow with --allow-engine-drift)")
	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestCloseBoundedKill pins：忽略 EOF 的引擎 → 有界关闭超时后杀进程树。
func TestCloseBoundedKill(t *testing.T) {
	h := newTestHost(t, "fake-hang.json", nil)
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if err := h.Close(ctx); err != nil {
		t.Fatalf("bounded close: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("close took %v, must be bounded", elapsed)
	}
	waitExit(t, h.Events(), "killed")
}

// TestStderrRingRedaction pins：stderr 泵 → 脱敏 → 环形缓冲，端到端——
// fake-happy 启动时写两条 stderr（sk- 形态密钥 + 注入凭据值），断言环形缓冲
// 中只见 [REDACTED]（凭据经环境变量，不落日志，design §7.4）。
func TestStderrRingRedaction(t *testing.T) {
	h := newTestHost(t, "fake-happy.json", func(c *BootConfig, o *ClaudeHostOptions) {
		o.StderrRingSize = 16
	})
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	// stderr 行与握手并发落地——轮询直到两条诊断行出现（或超时）
	deadline := time.Now().Add(5 * time.Second)
	var joined string
	for time.Now().Before(deadline) {
		joined = strings.Join(h.StderrTail(16), "\n")
		if strings.Contains(joined, "startup diag") && strings.Count(joined, "startup diag") >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Count(joined, "startup diag") < 2 {
		t.Fatalf("stderr lines did not arrive: %q", joined)
	}
	if strings.Contains(joined, "sk-ant-api03-SECRETVALUE123") {
		t.Fatalf("key shape leaked: %q", joined)
	}
	if strings.Contains(joined, "supersecret99") {
		t.Fatalf("injected credential leaked: %q", joined)
	}
	if !strings.Contains(joined, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", joined)
	}
}

// TestRedactStderrLine pins 脱敏规则：sk- 形态密钥、Bearer token、注入凭据值。
func TestRedactStderrLine(t *testing.T) {
	redactors := buildRedactors([]string{"FAKE_CREDENTIAL=supersecret99"})
	cases := []struct {
		in, wantContains, wantAbsent string
	}{
		{"key sk-ant-api03-SECRETVALUE123 leaked", "[REDACTED]", "sk-ant-api03-SECRETVALUE123"},
		{"auth Bearer abc.def.ghi-jkl done", "[REDACTED]", "abc.def.ghi-jkl"},
		{"cred=supersecret99 tail", "[REDACTED]", "supersecret99"},
		{"plain log line", "plain log line", ""},
	}
	for _, c := range cases {
		got := redactStderrLine(c.in, redactors)
		if !strings.Contains(got, c.wantContains) {
			t.Fatalf("redact(%q) = %q, want contains %q", c.in, got, c.wantContains)
		}
		if c.wantAbsent != "" && strings.Contains(got, c.wantAbsent) {
			t.Fatalf("redact(%q) = %q, must not contain %q", c.in, got, c.wantAbsent)
		}
	}
}

// TestRespondControlEnvelope pins：控制协议请求路由——引擎 control_request
// 可读，RespondControl 按 §1/§5 信封应答（request_id 以参数覆写，Task 15 交接）。
func TestRespondControlEnvelope(t *testing.T) {
	h := newTestHost(t, "fake-control.json", nil)
	if err := h.Start(context.Background(), h.cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	events := collectEvents(t, h.Events(), 2) // init + control_request（脚本 before_input）
	var cr *Event
	for _, ev := range events {
		if ev.Kind == KindControlRequest {
			cr = &ev
		}
	}
	if cr == nil || cr.Control == nil || cr.Control.RequestID != "r-42" {
		t.Fatalf("control request missing: %+v", events)
	}
	payload := ControlResponse{SubType: "success", RequestID: "WILL-BE-OVERWRITTEN",
		Response: json.RawMessage(`{"ok":true}`)}
	if err := h.RespondControl(context.Background(), "r-42", payload); err != nil {
		t.Fatalf("respond control: %v", err)
	}
	// Send 也回显（wire 形状断言）
	if err := h.Send(context.Background(), UserMessage{Text: "ping"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // 回显落 stderr
	joined := strings.Join(h.StderrTail(32), "\n")
	if !strings.Contains(joined, `"type":"control_response"`) ||
		!strings.Contains(joined, `"request_id":"r-42"`) ||
		!strings.Contains(joined, `"subtype":"success"`) {
		t.Fatalf("control_response envelope wrong:\n%s", joined)
	}
	if strings.Contains(joined, "WILL-BE-OVERWRITTEN") {
		t.Fatal("payload request_id must be overwritten by the id parameter")
	}
	if !strings.Contains(joined, `"type":"user"`) || !strings.Contains(joined, `"text":"ping"`) {
		t.Fatalf("user message wire shape wrong:\n%s", joined)
	}
}

// TestMalformedStdoutFailsPump pins：stdout 非法行 → 泵失败（fail-closed），
// Start 握手报错、进程被清理。
func TestMalformedStdoutFailsPump(t *testing.T) {
	h := newTestHost(t, "fake-garbage.json", nil)
	err := h.Start(context.Background(), h.cfg)
	if err == nil {
		t.Fatal("garbage stdout must fail handshake")
	}
	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("close after pump failure: %v", err)
	}
}

// TestBuildArgs pins：§7.2 参数集逐字（顺序固定；可选 flag 按内容出现）。
func TestBuildArgs(t *testing.T) {
	h := NewClaudeHost(ClaudeHostOptions{LockPath: testLockPath(t)})
	cfg := BootConfig{
		PermissionMode:        "default",
		SettingSources:        []string{"user", "project"},
		Model:                 "claude-opus-5[1M]",
		PermissionPromptTool:  "mcp__mulix__approve",
		AppendSystemPrompt:    "SNAPSHOT",
		AllowedTools:          []string{"Bash(go test:*)", "Edit(docs/**)"},
		DisallowedTools:       []string{"Bash(rm -rf*)"},
		ResumeEngineSessionID: "eng-1",
		ForkSession:           true,
		SettingsJSON:          `{"permissions":{"ask":["Bash","PowerShell"]}}`,
	}
	args, mcpTemp, settingsTemp, err := h.buildArgs(cfg)
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if mcpTemp != "" {
		t.Fatalf("no mcp temp expected, got %q", mcpTemp)
	}
	defer os.Remove(settingsTemp)
	want := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "default",
		"--setting-sources", "user,project",
		"--settings", settingsTemp,
		"--model", "claude-opus-5[1M]",
		"--permission-prompt-tool", "mcp__mulix__approve",
		"--append-system-prompt", "SNAPSHOT",
		"--allowedTools", "Bash(go test:*),Edit(docs/**)",
		"--disallowedTools", "Bash(rm -rf*)",
		"--resume", "eng-1",
		"--fork-session",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (full: %v)", i, args[i], want[i], args)
		}
	}
	// MCPConfigJSON 非空 → 物化临时文件
	cfg.MCPConfigJSON = `{"mcpServers":{}}`
	args2, mcpTemp2, settingsTemp2, err := h.buildArgs(cfg)
	if err != nil {
		t.Fatalf("buildArgs mcp: %v", err)
	}
	defer os.Remove(mcpTemp2)
	defer os.Remove(settingsTemp2)
	idx := -1
	for i, a := range args2 {
		if a == "--mcp-config" {
			idx = i
		}
	}
	if idx < 0 || idx+1 >= len(args2) {
		t.Fatal("--mcp-config missing")
	}
	data, err := os.ReadFile(args2[idx+1])
	if err != nil || string(data) != `{"mcpServers":{}}` {
		t.Fatalf("mcp temp file wrong: %v %q", err, data)
	}
	// --settings 物化：内容逐字节 = SettingsJSON
	sidx := -1
	for i, a := range args2 {
		if a == "--settings" {
			sidx = i
		}
	}
	if sidx < 0 || sidx+1 >= len(args2) {
		t.Fatal("--settings missing")
	}
	data, err = os.ReadFile(args2[sidx+1])
	if err != nil || string(data) != cfg.SettingsJSON {
		t.Fatalf("settings temp file wrong: %v %q", err, data)
	}
}

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
	// buildArgs 是纯物化（路径经返回值交接，Start 落字段）——单测 cleanupTemp
	// 需直设字段（Ruling：与 mcpTemp 同语义的隔离测试形态）。
	h.settingsTemp = settingsTemp
	if _, err := os.Stat(settingsTemp); err != nil {
		t.Fatalf("settings temp missing: %v", err)
	}
	h.cleanupTemp()
	if _, err := os.Stat(settingsTemp); !os.IsNotExist(err) {
		t.Fatalf("settings temp not cleaned: %v", err)
	}
}
