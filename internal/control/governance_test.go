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

// hasViolationNotice 报告镜像是否含 [governance_violation] 留痕。
func hasViolationNotice(t *testing.T, c *Controller) bool {
	return violationNoticeOf(t, c) != nil
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
	// 围堵未实现时本回合永不收口（fixture 无 result）→ 以超时上界驱动：
	// GREEN 路径违规终止 → engine_closed 合成 → Submit 正常返回 nil。
	submitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Submit(submitCtx, "hi"); err != nil {
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

// TestGovernanceViolationIncludesInputDigest pins：违规留痕含输入摘要——
// 跟踪形态（assistant 声明过的 tool_use）取 Input 截断摘要；未跟踪形态
// （无 assistant 捕获、运行态直回流）以结果内容前缀兜底（FR-206 审计面）。
func TestGovernanceViolationIncludesInputDigest(t *testing.T) {
	t.Run("tracked", func(t *testing.T) {
		fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{
			toolUseTurn("tu-d1", "PowerShell", false),
		}})
		sink := &recordingSink{}
		c := govController(t, fake, sink, mustDefaultConfig())
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		submitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Submit(submitCtx, "hi"); err != nil {
			t.Fatalf("submit: %v", err)
		}
		v := violationNoticeOf(t, c)
		if v == nil {
			t.Fatal("no violation notice")
		}
		if !strings.Contains(v.Content, "echo hi") {
			t.Fatalf("tracked violation lacks input digest: %q", v.Content)
		}
	})
	t.Run("untracked", func(t *testing.T) {
		// 无 assistant 捕获：tool_result 直抵运行态泵（relay 形态，turn 由
		// 纯 result 之外的途径驱动——先跑一个白名单 Read 回合置 running，
		// 第二回合内直接注入未跟踪 tool_result？——不可行：Submit 互斥。
		// 改用 chanSource 直投：assistant(Read)→tool_result→result 收口后
		// 无法再进运行态。此处以真实路径构造：Submit 一个「只有未跟踪
		// tool_result」的回合——fixture 无 assistant，tool_result 运行态到达。
		fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{{
			{Kind: engine.KindToolResult, SessionID: "s", UUID: "r-u1",
				ToolResult: &engine.ToolResultMsg{Blocks: []engine.ToolResultBlock{{
					ToolUseID: "tu-u9", Content: "rm -rf /tmp/x; done", IsError: false}}}},
		}}})
		sink := &recordingSink{}
		c := govController(t, fake, sink, mustDefaultConfig())
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		submitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Submit(submitCtx, "hi"); err != nil {
			t.Fatalf("submit: %v", err)
		}
		v := violationNoticeOf(t, c)
		if v == nil {
			t.Fatal("no violation notice for untracked result")
		}
		if !strings.Contains(v.Content, "rm -rf /tmp/x") {
			t.Fatalf("untracked violation lacks content digest: %q", v.Content)
		}
	})
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
		name   string
		effect guard.Decision
		isErr  bool
	}{
		{"allow", guard.DecisionAllow, false},
		{"deny", guard.DecisionDeny, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turn := toolUseTurn("tu-j-"+tc.name, "Bash", true)
			fake := engine.NewFakeHost(engine.FakeScript{
				Turns: [][]engine.Event{append(turn, successResult("res-j-"+tc.name))},
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
// Host 返回真实宿主——onInit 消费 host.Info()（指纹由宿主自述）。
type chanSource struct {
	ch   chan engine.Event
	host engine.Host
}

func (s *chanSource) Events() <-chan engine.Event { return s.ch }
func (s *chanSource) Host() engine.Host           { return s.host }

// TestGovernanceOrphanAfterCancel pins：真实回合收口（running: true→false，
// Submit→Send→FakeHost emit 经 relay 进泵——时序确定性来自 Submit 自身调
// Send）之后到达的孤儿 tool_result 不触发围堵且泵不崩（run2 t3 Esc 孤儿防御
// 语义，FR-207）。
func TestGovernanceOrphanAfterCancel(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{{
		{Kind: engine.KindAssistant, SessionID: "s", UUID: "a-or",
			Assistant: &engine.AssistantMsg{ID: "msg-or", Model: "fake-model",
				Blocks: []engine.ContentBlock{{Type: "tool_use", ID: "tu-or",
					Name: "Read", Input: []byte(`{"file_path":"x"}`)}}}},
		{Kind: engine.KindToolResult, SessionID: "s", UUID: "r-or",
			ToolResult: &engine.ToolResultMsg{Blocks: []engine.ToolResultBlock{{
				ToolUseID: "tu-or", Content: "content", IsError: false}}}},
		successResult("res-or"),
	}}})
	src := &chanSource{ch: make(chan engine.Event, 8), host: fake}
	go func() { // relay：FakeHost 投递 → 泵事件源（Source 覆盖后泵只读 chanSource）
		for ev := range fake.Events() {
			src.ch <- ev
		}
	}()
	sink := &recordingSink{}
	c := govController(t, fake, sink, mustDefaultConfig(),
		func(o *Options) { o.Source = src })
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit turn 1: %v", err)
	}
	// 孤儿：收口后的 tool_result（引擎取消残留形态；running=false）——直投源
	src.ch <- engine.Event{
		Kind: engine.KindToolResult, SessionID: "s", UUID: "r-late",
		ToolResult: &engine.ToolResultMsg{Blocks: []engine.ToolResultBlock{{
			ToolUseID: "tu-or", Content: "late", IsError: false}}},
	}
	// 2s 内出现违规即失败（负断言）；超时无违规 = 通过
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			close(src.ch) // 泵尾合成 engine_closed（running 已 false——无 TurnDone 重发）
			return
		case <-time.After(20 * time.Millisecond):
		}
		if violationNoticeOf(t, c) != nil {
			t.Fatal("orphan tool_result must not violate")
		}
	}
}
