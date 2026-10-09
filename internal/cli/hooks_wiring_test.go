package cli

// M16 评审修复轮 1（F1）：自定义钩子的组合根接线。此前 NewScriptConsumer
// 在 cli 侧零调用——消费者被产出但从未挂载，非法 [[hooks.custom.rules]] 与
// 降级告警在运行期是惰性的。修复 = assembleController 在规则非空时构造
// 消费者（声明校验失败 → 拒绝启动 fail-closed，与 permissions 规则解析
// 失败/memory store Open 失败同款），经 control.Options.HookConsumers 注入
// control 挂载（四时点 Hooks.On 追加适配回调）；Handle 错误（规则超时/
// 崩溃降级）由 control 落为 NoticeEvent（warning，IsMeta，镜像先行红线）
// ——发布通道只有 control 一条（design 002 §2.1「不造第二个事件总线」）。
//
// 测试形态：buildComponents 全链路（memory_wiring_test 同款）× FakeHost
// 回放脚本（KindHookResponse 注入钩子时点，control routing_test 同款）；
// 脚本 stub 用自重执行 helper process（guard/hooks_custom_test 同款模式
// ——cli 测试二进制自持一份，guard 的测试 helper 不跨包可见）。

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/sessionx"
)

const hookWireHelperEnv = "MULIX_HOOKWIRE_HELPER"

// hookWireObservation 是 helper 进程的观察记录（argv + stdin 原文）。
type hookWireObservation struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

// TestHookWiringHelperProcess 是自重执行的钩子脚本 stub：SLEEP_MS 先睡眠
// （超时用例——kill 收口不落盘）、OUT 落观察记录（argv/stdin 断言面）。
func TestHookWiringHelperProcess(t *testing.T) {
	if os.Getenv(hookWireHelperEnv) != "1" {
		return
	}
	if ms := os.Getenv("MULIX_HOOKWIRE_SLEEP_MS"); ms != "" {
		n, err := strconv.Atoi(ms)
		if err != nil {
			t.Fatalf("sleep ms: %v", err)
		}
		time.Sleep(time.Duration(n) * time.Millisecond)
	}
	if out := os.Getenv("MULIX_HOOKWIRE_OUT"); out != "" {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			t.Fatalf("read stdin: %v", err)
		}
		obs := hookWireObservation{Args: os.Args, Stdin: string(stdin)}
		b, err := json.Marshal(obs)
		if err != nil {
			t.Fatalf("marshal observation: %v", err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatalf("write observation: %v", err)
		}
	}
}

// hookWireHelperCommand 构造指向本测试二进制 helper 模式的规则 command。
func hookWireHelperCommand(t *testing.T) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	return []string{exe, "-test.run=^TestHookWiringHelperProcess$"}
}

// hookWireSink 记录 NoticeEvent 与 TurnDone（降级告警与 fail-open 收口面）。
type hookWireSink struct {
	mu      sync.Mutex
	notices []event.NoticeEvent
	turns   int
}

func (s *hookWireSink) Publish(ev any) {
	switch e := ev.(type) {
	case event.NoticeEvent:
		s.mu.Lock()
		s.notices = append(s.notices, e)
		s.mu.Unlock()
	case event.TurnDoneEvent:
		s.mu.Lock()
		s.turns++
		s.mu.Unlock()
	}
}

func (s *hookWireSink) warningContents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, n := range s.notices {
		if n.Level == "warning" {
			out = append(out, n.Content)
		}
	}
	return out
}

func (s *hookWireSink) turnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns
}

// runHookWiring 驱动一次 buildComponents 全链路：rules 写入 cfg.Hooks
// （nil = 保持默认零规则）→ FakeHost 回放（回合段 = [PreToolUse
// hook_response] + result——段内事件严格保序，钩子分发先于 TurnDone）→
// NewSession + Submit → 返回（sink, store, 错误）。构建错误原样返回
// （拒绝启动断言面，此时 store 已收口）；store 一并返回供镜像 Replay
// （Close 之后 Replay 仍可读——只读面）。ws 为空 → 新建 TempDir 工作区；
// bootOut 非空时捕获 Boot 面（护栏对照用）。组件收口序 ctrl → meter →
// store（sessionHandle.close 同序；t.Fatalf 经 Goexit 同样触发收口）。
func runHookWiring(t *testing.T, ws string, rules []config.HookRule, withHookEvent bool, bootOut *engine.BootConfig) (*hookWireSink, *sessionx.Store, error) {
	t.Helper()
	if ws == "" {
		ws = t.TempDir()
	}
	cfg := testConfig()
	if rules != nil {
		cfg.Hooks.Custom.Rules = rules
	}
	var boot engine.BootConfig
	o := Options{
		Config:       cfg,
		Workspace:    ws,
		SessionsRoot: t.TempDir(),
		UsageRoot:    t.TempDir(),
		HostFactory: func(b engine.BootConfig) (engine.Host, error) {
			boot = b
			var turn []engine.Event
			if withHookEvent {
				turn = append(turn, engine.Event{Kind: engine.KindHookResponse, SessionID: "s",
					Hook: &engine.HookInfo{HookID: "h1", HookName: "audit", HookEvent: "PreToolUse",
						Output: "out", Outcome: "success"}})
			}
			turn = append(turn, engine.Event{Kind: engine.KindResult, SessionID: "s",
				Result: &engine.ResultInfo{SubType: "success"}})
			return engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{turn}}), nil
		},
	}
	store, err := sessionx.Create(o.SessionsRoot, sessionx.Options{
		ID: "s-hookwire", Workspace: ws, Unattended: true,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	meter, err := cache.NewMeter(cache.MeterOptions{Root: o.UsageRoot, SessionID: "s-hookwire",
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	defer meter.Close()
	sink := &hookWireSink{}
	ctrl, _, err := buildComponents(o, store, meter, sink, false)
	if err != nil {
		return sink, store, err
	}
	defer func() { _ = ctrl.Close(context.Background()) }() // 先于 store/meter（LIFO）
	if err := ctrl.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := ctrl.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if bootOut != nil {
		*bootOut = boot
	}
	return sink, store, nil
}

// TestHookWiringMountsConsumerAndTriggers pins F1 ①（挂载 + 触发）：规则
// 非空 → 消费者挂载，PreToolUse 时点触发脚本（helper stub 落观察记录，
// stdin 携带完整事件 JSON）；成功路径无降级告警。
func TestHookWiringMountsConsumerAndTriggers(t *testing.T) {
	out := filepath.Join(t.TempDir(), "obs.json")
	t.Setenv(hookWireHelperEnv, "1")
	t.Setenv("MULIX_HOOKWIRE_OUT", out)
	sink, _, err := runHookWiring(t, "", []config.HookRule{
		{Event: "PreToolUse", Command: hookWireHelperCommand(t), TimeoutMS: 10000},
	}, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook script must have executed via the mounted consumer: %v", err)
	}
	var obs hookWireObservation
	if err := json.Unmarshal(b, &obs); err != nil {
		t.Fatalf("observation: %v", err)
	}
	if !strings.Contains(obs.Stdin, `"event":"PreToolUse"`) || !strings.Contains(obs.Stdin, `"hook_id":"h1"`) {
		t.Fatalf("stdin must carry the dispatched payload: %q", obs.Stdin)
	}
	if warns := sink.warningContents(); len(warns) != 0 {
		t.Fatalf("successful script must not warn: %v", warns)
	}
}

// TestHookWiringTimeoutDegradesWithMirrorNotice pins F1 ①（告警路径）：
// 规则超时 → control 适配层把 Handle 错误落为 NoticeEvent（warning）——
// sink 与镜像（Replay）都可见（镜像先行红线：镜像 Append 先于 Sink 发布）；
// 回合照常收口（fail-open，降级不悬挂主流程）。
func TestHookWiringTimeoutDegradesWithMirrorNotice(t *testing.T) {
	t.Setenv(hookWireHelperEnv, "1")
	t.Setenv("MULIX_HOOKWIRE_SLEEP_MS", "30000") // 远超规则超时——kill 收口
	sink, store, err := runHookWiring(t, "", []config.HookRule{
		{Event: "PreToolUse", Command: hookWireHelperCommand(t), TimeoutMS: 150},
	}, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sink.turnCount() == 0 {
		t.Fatal("degraded hook must not block the turn (fail-open)")
	}
	warns := sink.warningContents()
	if len(warns) != 1 || !strings.Contains(warns[0], "自定义钩子降级") || !strings.Contains(warns[0], "timeout") {
		t.Fatalf("timeout must publish one degrade warning: %v", warns)
	}
	// 镜像可审计（SC-006）：同一告警在镜像可 Replay——钉「走了发布通道」
	// 而非直写 sink。
	events, replayWarnings, err := store.Replay()
	if err != nil || len(replayWarnings) != 0 {
		t.Fatalf("replay: %v %v", err, replayWarnings)
	}
	mirrorHit := false
	for _, ev := range events {
		if n, ok := ev.(event.NoticeEvent); ok && n.Level == "warning" &&
			strings.Contains(n.Content, "自定义钩子降级") {
			mirrorHit = true
		}
	}
	if !mirrorHit {
		t.Fatal("degrade warning must be mirrored (mirror-first red line)")
	}
}

// TestHookWiringInvalidRulesRefuseStart pins F1 ②：非法声明 → 组装失败
// 拒绝启动（fail-closed，与 memory store Open 失败同款——绝不静默丢弃）。
func TestHookWiringInvalidRulesRefuseStart(t *testing.T) {
	_, _, err := runHookWiring(t, "", []config.HookRule{
		{Event: "Foo", Command: []string{"x"}},
	}, false, nil)
	if err == nil {
		t.Fatal("invalid rules must refuse session assembly (fail-closed)")
	}
	if !strings.Contains(err.Error(), "hooks.custom.rules") || !strings.Contains(err.Error(), "event") {
		t.Fatalf("error must name the config section and field: %v", err)
	}
}

// TestHookWiringEmptyRulesZeroDelta pins F1 ③：空规则（声明了 [hooks.custom]
// 但 rules=[]）与完全无 hooks 配置 → 行为零差（无脚本执行、无告警）且
// Boot 面逐字节一致（disabled-face 先例同款护栏对照；同 workspace——
// AppendSystemPrompt 内嵌工作区路径，异 workspace 会造成无关差异）。
func TestHookWiringEmptyRulesZeroDelta(t *testing.T) {
	out := filepath.Join(t.TempDir(), "obs.json")
	t.Setenv(hookWireHelperEnv, "1")
	t.Setenv("MULIX_HOOKWIRE_OUT", out) // 若空规则被误挂载，触发时点会落盘——断言缺席
	ws := t.TempDir()
	run := func(t *testing.T, rules []config.HookRule) (engine.BootConfig, *hookWireSink) {
		t.Helper()
		var boot engine.BootConfig
		sink, _, err := runHookWiring(t, ws, rules, true, &boot)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return boot, sink
	}
	bootEmpty, sinkEmpty := run(t, []config.HookRule{})
	bootAbsent, sinkAbsent := run(t, nil)
	if bootEmpty.AppendSystemPrompt != bootAbsent.AppendSystemPrompt ||
		bootEmpty.MCPConfigJSON != bootAbsent.MCPConfigJSON ||
		strings.Join(bootEmpty.AllowedTools, ",") != strings.Join(bootAbsent.AllowedTools, ",") {
		t.Fatalf("boot face diverges between empty rules and absent hooks:\nempty:  %+v\nabsent: %+v", bootEmpty, bootAbsent)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("empty rules must not mount any consumer (no script execution): %v", err)
	}
	if warns := sinkEmpty.warningContents(); len(warns) != 0 || len(sinkAbsent.warningContents()) != 0 {
		t.Fatalf("empty rules must not warn: %v / %v", sinkEmpty.warningContents(), sinkAbsent.warningContents())
	}
}
