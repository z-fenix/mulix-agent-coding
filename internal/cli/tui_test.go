package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
)

// ---- 真实装配辅助（原 tui_test.go 的 cli 侧留存——服务 TestCliSwapper 与
// 各 wiring 测试；TUI 状态机测试已平移 internal/tui） ----

func testConfig() *config.Config {
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		panic(err)
	}
	return cfg
}

// fakeHostFactory 注入 FakeHost（happy-turn——握手/回合/收口全绿的默认脚本）。
func fakeHostFactory(t *testing.T) func(engine.BootConfig) (engine.Host, error) {
	t.Helper()
	script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return func(engine.BootConfig) (engine.Host, error) { return engine.NewFakeHost(script), nil }
}

func mustOpenSession(t *testing.T, o Options, resumeID string, sink control.Sink) *sessionHandle {
	t.Helper()
	if o.HostFactory == nil {
		o.HostFactory = fakeHostFactory(t)
	}
	if sink == nil {
		sink = discardSink{}
	}
	h, code, err := openSession(o, resumeID, true, sink)
	if err != nil || code != 0 {
		t.Fatalf("open session: code=%d err=%v", code, err)
	}
	return h
}

func seedUsageSession(t *testing.T, usageRoot string) *sessionHandle {
	t.Helper()
	o := Options{Workspace: t.TempDir(), SessionsRoot: t.TempDir(), UsageRoot: usageRoot, Config: testConfig()}
	h := mustOpenSession(t, o, "", nil)
	if _, err := h.meter.Observe(cache.TurnUsage{InputTokens: 100, CacheReadTokens: 300, OutputTokens: 20}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	return h
}

// discardSink 丢弃事件（装配辅助）。
type discardSink struct{}

func (discardSink) Publish(any) {}

// ---- 用例 ----

// TestCliSwapper pins cliSwapper 装配语义（TestSlashNew/TestSlashResume 的
// 真装配半——tui 侧只钉接缝顺序与重绑，本测试钉 openSession 装配事实）：
// CloseCurrent 收口当前 handle（旧控制面此后拒绝提交——fail-closed）；Open
// 以同 sink 开新会话并换绑 cur；resume 走镜像续链；失败路径旧会话不可恢复
// （cur 置 nil）。事件通道归 tui（换绑排空在 tui 侧，见 internal/tui
// TestSlashNew 的残响清点）。
func TestCliSwapper(t *testing.T) {
	// Workspace "w" 与 seedSession 的镜像工作区一致（resume 续链校验工作区绑定
	// ——与原 TestSlashResume 同约定）。
	o := Options{Workspace: "w", SessionsRoot: t.TempDir(), UsageRoot: t.TempDir(),
		Config: testConfig(), HostFactory: fakeHostFactory(t)}
	sink := discardSink{}
	h := mustOpenSession(t, o, "", sink)
	oldID := h.id
	oldCtrl := h.ctrl
	sw := &cliSwapper{o: o, cur: h}

	// CloseCurrent：旧 handle 收口——旧控制面此后拒绝提交
	sw.CloseCurrent()
	if err := oldCtrl.Submit(context.Background(), "x"); !errors.Is(err, control.ErrNoSession) {
		t.Fatalf("old ctrl after CloseCurrent: err=%v, want ErrNoSession", err)
	}

	// Open("")：同 sink 开新会话 + cur 换绑
	ctrl2, id2, err := sw.Open("", sink)
	if err != nil {
		t.Fatalf("open new: %v", err)
	}
	if id2 == "" || id2 == oldID {
		t.Fatalf("new session id = %q (old %q)", id2, oldID)
	}
	if ctrl2 == nil {
		t.Fatal("new controller nil")
	}
	if sw.cur == nil || sw.cur.id != id2 {
		t.Fatal("cur must rebind to the new handle")
	}
	sw.CloseCurrent() // 收口新会话（测试卫生）

	// Open(<engine-id>)：镜像续链（resume 装配路径）。先收口旧会话——换绑
	// 次序 close → open（Open 只换绑 cur，不代收旧句柄）。
	seed := seedSession(t, o.SessionsRoot, "s-res", "engine-res")
	defer seed.Close()
	h2 := mustOpenSession(t, o, "", sink)
	sw2 := &cliSwapper{o: o, cur: h2}
	sw2.CloseCurrent()
	_, id3, err := sw2.Open("engine-res", sink)
	if err != nil {
		t.Fatalf("open resume: %v", err)
	}
	if !strings.HasSuffix(id3, "s-res") {
		t.Fatalf("resumed id = %q, want suffix s-res", id3)
	}
	sw2.CloseCurrent()

	// 失败路径：未知引擎 id → 报错且 cur 置 nil（旧会话已不可恢复）
	h3 := mustOpenSession(t, o, "", sink)
	sw3 := &cliSwapper{o: o, cur: h3}
	sw3.CloseCurrent()
	if _, _, err := sw3.Open("engine-missing", sink); err == nil {
		t.Fatal("open with unknown engine id must fail")
	}
	if sw3.cur != nil {
		t.Fatal("failed open must leave cur nil")
	}
}
