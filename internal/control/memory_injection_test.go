package control

// M13/T13.2 记忆召回注入测试（brief Step 1 ①②③ + 溯源 env）：
//   ① 开关关：Submit 后发出的 user message 无注入，boot 面与开启时逐字节一致；
//   ② 开关开：query 命中 → turn tail 含 [记忆召回] 块（system prompt / mcp-config
//      / AllowedTools 绝不改变——SC-003 护栏）；
//   ③ 预算截断：k/budget 按配置传给检索、真实 store 预算内截断；
//   ④ MULIX_MEMORY_SESSION 溯源 env：开启 + 注册面含 mulix 条目时注入 Boot Env
//     （绝不入 mcp-config），关闭时缺席。
//
// 注册表断言（brief ① 的「memory 工具不在注册表」）遵循 Task 3 裁定：注册表
// 全局态 + 包级 Once 跨测试共享，disabled 反向断言不可靠——注册缺席由组合根
// 侧（internal/cli memory wiring 测试 + mulixmemory 包 RegisterConditional
// enabled 侧）钉，本文件只钉注入行为与 boot 面。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/memory"
	"mulix/internal/registry"
)

// stubMemoryRecall 是 MemoryRecall 窄缝的测试替身：记录检索参数、返回可编程命中。
type stubMemoryRecall struct {
	mu     sync.Mutex
	query  string
	k      int
	budget int
	hits   []memory.Entry
}

func (s *stubMemoryRecall) Search(query string, k int, budgetChars int) []memory.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query, s.k, s.budget = query, k, budgetChars
	return s.hits
}

func (s *stubMemoryRecall) calls() (string, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query, s.k, s.budget
}

// resultTurn 是最小回合脚本（一次 Send 即收口）。
func resultTurn() engine.FakeScript {
	return engine.FakeScript{Turns: [][]engine.Event{
		{{Kind: engine.KindResult, SessionID: "s", Result: &engine.ResultInfo{SubType: "success"}}},
	}}
}

// TestMemoryRecallDisabledZeroDelta 钉 ①：Memory nil（[tools] memory_search=false
// 的 cli 接线结果）→ 发出的 user message 无注入，boot 面与开启时逐字节一致
// （护栏对照：同参双跑 + cache.Compose 独立重算基线）。
func TestMemoryRecallDisabledZeroDelta(t *testing.T) {
	run := func(enabled bool) (engine.BootConfig, []engine.UserMessage) {
		fake := engine.NewFakeHost(resultTurn())
		sink := &recordingSink{}
		var boot engine.BootConfig
		c := newTestControllerCapturingBoot(t, fake, sink, &boot, func(o *Options) {
			if enabled {
				o.Memory = &stubMemoryRecall{hits: []memory.Entry{
					{ID: "m1", Text: "alpha 决策", SourceSession: "s1", CreatedAt: "2026-10-08T00:00:00Z"},
				}}
			}
		})
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		if err := c.Submit(context.Background(), "hello 项目"); err != nil {
			t.Fatalf("submit: %v", err)
		}
		return boot, fake.Sent()
	}
	disabledBoot, disabledSent := run(false)
	enabledBoot, _ := run(true)

	// ① 关：无注入
	if len(disabledSent) != 1 {
		t.Fatalf("disabled sent = %d messages", len(disabledSent))
	}
	if len(disabledSent[0].TurnTail) != 0 {
		t.Fatalf("disabled TurnTail = %v, want empty", disabledSent[0].TurnTail)
	}
	if disabledSent[0].Text != "hello 项目" {
		t.Fatalf("disabled text = %q, want original", disabledSent[0].Text)
	}
	// ② 护栏：boot 面逐字节一致（AppendSystemPrompt/MCPConfigJSON/AllowedTools）
	if disabledBoot.AppendSystemPrompt != enabledBoot.AppendSystemPrompt ||
		disabledBoot.MCPConfigJSON != enabledBoot.MCPConfigJSON ||
		strings.Join(disabledBoot.AllowedTools, ",") != strings.Join(enabledBoot.AllowedTools, ",") {
		t.Fatalf("boot face diverges:\ndisabled: %+v\nenabled:  %+v", disabledBoot, enabledBoot)
	}
	// 独立重算基线（P1 组装器，不知道记忆存在——Task 4 disabled 测试同法）
	art, err := cache.Compose(mustDefaultConfig(), "w", emptyRegistry{})
	if err != nil {
		t.Fatalf("compose baseline: %v", err)
	}
	if disabledBoot.AppendSystemPrompt != art.AppendSystemPrompt ||
		disabledBoot.MCPConfigJSON != art.MCPConfigJSON {
		t.Fatalf("disabled boot face diverges from P1 baseline:\nboot: %+v\nbase: %+v", disabledBoot, art)
	}
}

// TestMemoryRecallInjectedAtTurnTail 钉 ②：命中 → turn tail 追加 [记忆召回] 块
// （格式 [记忆召回]\n- <entry text>\n），user 正文原样，检索参数 = 配置默认
// （k=3 / budget=2000），镜像留痕（SC-006 召回注入可审计）。
func TestMemoryRecallInjectedAtTurnTail(t *testing.T) {
	stub := &stubMemoryRecall{hits: []memory.Entry{
		{ID: "m1", Text: "alpha 决策", SourceSession: "s1", CreatedAt: "2026-10-08T00:00:00Z"},
		{ID: "m2", Text: "beta 约定", SourceSession: "s1", CreatedAt: "2026-10-08T00:00:01Z"},
	}}
	fake := engine.NewFakeHost(resultTurn())
	sink := &recordingSink{}
	c := newTestControllerCapturingBoot(t, fake, sink, &engine.BootConfig{}, func(o *Options) {
		o.Memory = stub
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hello 项目"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	sent := fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d messages", len(sent))
	}
	if sent[0].Text != "hello 项目" {
		t.Fatalf("user text mutated: %q", sent[0].Text)
	}
	if len(sent[0].TurnTail) != 1 {
		t.Fatalf("TurnTail = %d blocks, want 1 (%v)", len(sent[0].TurnTail), sent[0].TurnTail)
	}
	want := "[记忆召回]\n- alpha 决策\n- beta 约定\n"
	if sent[0].TurnTail[0] != want {
		t.Fatalf("recall block = %q, want %q", sent[0].TurnTail[0], want)
	}
	// 检索参数：query=提交文本，k/budget=配置默认
	if q, k, budget := stub.calls(); q != "hello 项目" || k != 3 || budget != 2000 {
		t.Fatalf("search args = (%q, %d, %d), want (提交文本, 3, 2000)", q, k, budget)
	}
	// 镜像留痕：TurnStarted 后有召回 notice
	mirror, _ := c.Mirror().ReadAllEvents()
	started, noticed := false, false
	for _, ev := range mirror {
		switch e := ev.(type) {
		case event.TurnStartedEvent:
			started = true
		case event.NoticeEvent:
			if started && strings.Contains(e.Content, "记忆召回") {
				noticed = true
			}
		}
	}
	if !started || !noticed {
		t.Fatalf("recall notice missing in mirror (started=%v noticed=%v)", started, noticed)
	}
}

// TestMemoryRecallNoHitsSilent 钉 spec Edge Cases：命中 0 条静默跳过（无块、无留痕）。
func TestMemoryRecallNoHitsSilent(t *testing.T) {
	stub := &stubMemoryRecall{hits: nil}
	fake := engine.NewFakeHost(resultTurn())
	c := newTestControllerCapturingBoot(t, fake, &recordingSink{}, &engine.BootConfig{}, func(o *Options) {
		o.Memory = stub
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || len(sent[0].TurnTail) != 0 {
		t.Fatalf("no-hit TurnTail = %v, want empty", sent[0].TurnTail)
	}
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "记忆召回") {
			t.Fatalf("no-hit must not leave recall notice: %+v", n)
		}
	}
}

// TestMemoryRecallZeroConfigFallback 钉防御性兜底：手拼 Config（Memory 零值）
// 时 k/budget 回退默认 3/2000（分层加载的默认值已由 config 包钉）。
func TestMemoryRecallZeroConfigFallback(t *testing.T) {
	stub := &stubMemoryRecall{hits: []memory.Entry{{ID: "m1", Text: "x"}}}
	fake := engine.NewFakeHost(resultTurn())
	c := newTestControllerCapturingBoot(t, fake, &recordingSink{}, &engine.BootConfig{}, func(o *Options) {
		cfg := mustDefaultConfig()
		cfg.Memory = config.MemoryConfig{} // 零值（手拼 Config 场景）
		o.Config = cfg
		o.Memory = stub
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hello"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, k, budget := stub.calls(); k != 3 || budget != 2000 {
		t.Fatalf("zero-config search args = (%d, %d), want fallback (3, 2000)", k, budget)
	}
}

// TestMemoryRecallBudgetTruncationRealStore 钉 ③（集成）：真实 *memory.Store
// 作召回源——预算内按分截断（Task 5 Search 语义经 control 消费）。
func TestMemoryRecallBudgetTruncationRealStore(t *testing.T) {
	st, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.Add("短条目命中", "s1"); err != nil {
		t.Fatalf("add short: %v", err)
	}
	if _, err := st.Add("这是一条非常非常长的记忆条目内容用来撑爆注入预算的测试数据", "s1"); err != nil {
		t.Fatalf("add long: %v", err)
	}
	fake := engine.NewFakeHost(resultTurn())
	c := newTestControllerCapturingBoot(t, fake, &recordingSink{}, &engine.BootConfig{}, func(o *Options) {
		cfg := mustDefaultConfig()
		cfg.Memory.RecallBudgetChars = 10 // rune 预算：只容得下 5-rune 短条目
		o.Config = cfg
		o.Memory = st
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "条目"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || len(sent[0].TurnTail) != 1 {
		t.Fatalf("TurnTail = %v, want 1 block", sent[0].TurnTail)
	}
	block := sent[0].TurnTail[0]
	if !strings.Contains(block, "短条目命中") {
		t.Fatalf("block missing short entry: %q", block)
	}
	if strings.Contains(block, "撑爆注入预算") {
		t.Fatalf("budget must truncate long entry: %q", block)
	}
}

// TestMemorySessionEnvInjection 钉 ④：Memory 接线 + 注册面含 mulix 条目
// （serve 子进程存在）→ Boot Env 携带 MULIX_MEMORY_SESSION=<mulix 会话 id>
// （Boot Env 通道，绝不入 mcp-config——MULIX_SUBAGENT_CALLBACK 同款裁定）；
// 关闭时缺席。
func TestMemorySessionEnvInjection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		wantEnvVar bool
	}{
		{name: "memory disabled = no session env", enabled: false, wantEnvVar: false},
		{name: "memory enabled = session env stamped", enabled: true, wantEnvVar: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := engine.NewFakeHost(engine.FakeScript{})
			sink := &recordingSink{}
			var boot engine.BootConfig
			c := newTestControllerCapturingBoot(t, fake, sink, &boot, func(o *Options) {
				if tc.enabled {
					o.Memory = &stubMemoryRecall{}
				}
				o.Registry = registry.NewServer(registry.Options{Name: "mulix", Version: "test",
					SelfCommand: []string{"mulix.exe", "mcp", "serve"}})
			})
			if err := c.NewSession(context.Background()); err != nil {
				t.Fatalf("new session: %v", err)
			}
			has := false
			for _, e := range boot.Env {
				if e == "MULIX_MEMORY_SESSION=s-lc" { // newTestControllerCapturingBoot 的镜像会话 id
					has = true
				}
			}
			if has != tc.wantEnvVar {
				t.Fatalf("MULIX_MEMORY_SESSION in env = %v, want %v (env=%v)", has, tc.wantEnvVar, boot.Env)
			}
			// env 不入面：mcp-config 与有无记忆无关
			art, err := cache.Compose(mustDefaultConfig(), "w", c.opts.Registry)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			if boot.MCPConfigJSON != art.MCPConfigJSON {
				t.Fatalf("mcp-config must not carry memory env:\nboot: %s\nbase: %s", boot.MCPConfigJSON, art.MCPConfigJSON)
			}
		})
	}
}
