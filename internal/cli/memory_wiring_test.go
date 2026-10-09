// M13/T13.2 组合根接线测试：
//   - resolveMemoryStore：开关关不开库（零行为面）；子会话预置实例原样复用
//     （会话树恰一 Store——Review Focus「记忆名称冲突」的组合根防线）；
//     开启且无预置 → 打开 <workspace>/.mulix/memory；Open 失败 = 组装失败
//     （fail-closed，不静默降级）。
//   - buildComponents 全链路：开启 → 注入 [记忆召回] 块且 boot 面与关闭时
//     逐字节一致；关闭 → 无注入。
//   - 工厂闭包把父会话解析的 Store 下传子会话（磁盘库删除后子会话仍可召回
//     ——证明复用的是内存实例而非重新 Open）。
package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/memory"
	"mulix/internal/sessionx"
	"mulix/internal/tools/mulixmemory"
)

func memoryTestConfig(search bool) *config.Config {
	cfg := testConfig()
	cfg.Tools.MemorySearch = search
	return cfg
}

// seedMemory 在 workspace 的记忆库预置一条可召回条目。
func seedMemory(t *testing.T, ws, text string) *memory.Store {
	t.Helper()
	st, err := memory.Open(mulixmemory.Dir(ws))
	if err != nil {
		t.Fatalf("seed memory open: %v", err)
	}
	if _, err := st.Add(text, "seed"); err != nil {
		t.Fatalf("seed memory add: %v", err)
	}
	return st
}

// blockMemoryDir 用一个普通文件占据 <ws>/.mulix/memory 位置——任何新 Open 必败。
func blockMemoryDir(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".mulix"), 0o755); err != nil {
		t.Fatalf("mkdir .mulix: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".mulix", "memory"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	return ws
}

// TestResolveMemoryStore 钉组合根的 Store 解析三分支。
func TestResolveMemoryStore(t *testing.T) {
	t.Run("disabled opens nothing", func(t *testing.T) {
		ws := t.TempDir()
		o := Options{Workspace: ws}
		st, err := resolveMemoryStore(o, *memoryTestConfig(false))
		if err != nil || st != nil {
			t.Fatalf("disabled resolve = (%v, %v), want (nil, nil)", st, err)
		}
		if _, statErr := os.Stat(mulixmemory.Dir(ws)); !os.IsNotExist(statErr) {
			t.Fatalf("disabled must not create memory dir: %v", statErr)
		}
	})
	t.Run("preset store reused without reopen", func(t *testing.T) {
		ws := blockMemoryDir(t) // 任何新 Open 必败——复用才可能成功
		o := Options{Workspace: ws}
		preset := seedMemory(t, t.TempDir(), "预置实例")
		o.memoryStore = preset
		st, err := resolveMemoryStore(o, *memoryTestConfig(true))
		if err != nil || st != preset {
			t.Fatalf("preset resolve = (%p, %v), want same instance, nil error", st, err)
		}
	})
	t.Run("enabled opens workspace dir", func(t *testing.T) {
		ws := t.TempDir()
		o := Options{Workspace: ws}
		st, err := resolveMemoryStore(o, *memoryTestConfig(true))
		if err != nil || st == nil {
			t.Fatalf("enabled resolve = (%v, %v), want store, nil error", st, err)
		}
		if _, statErr := os.Stat(mulixmemory.Dir(ws)); statErr != nil {
			// 空库尚无 entries.jsonl（Task 5：首次 Add 才落文件）——Open 的
			// 落地面是 MkdirAll 出的目录本身。
			t.Fatalf("enabled must create workspace memory dir: %v", statErr)
		}
	})
	t.Run("enabled with broken dir fails closed", func(t *testing.T) {
		ws := blockMemoryDir(t)
		o := Options{Workspace: ws}
		st, err := resolveMemoryStore(o, *memoryTestConfig(true))
		if err == nil || st != nil {
			t.Fatalf("broken dir resolve = (%v, %v), want error", st, err)
		}
	})
}

// TestMemoryWiringInjectsRecallAndBootFaceStable 钉 buildComponents 全链路：
// 开启 → 真实 store 注入 [记忆召回] 块；关闭 → 无注入；两种形态的 boot 面
// （AppendSystemPrompt/MCPConfigJSON/AllowedTools）逐字节一致（SC-003）。
func TestMemoryWiringInjectsRecallAndBootFaceStable(t *testing.T) {
	type runOut struct {
		sent []engine.UserMessage
		boot engine.BootConfig
	}
	run := func(t *testing.T, search bool, ws string) runOut {
		t.Helper()
		script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		var fake *engine.FakeHost
		var boot engine.BootConfig
		o := Options{
			Config:       memoryTestConfig(search),
			Workspace:    ws,
			SessionsRoot: t.TempDir(),
			UsageRoot:    t.TempDir(),
			HostFactory: func(b engine.BootConfig) (engine.Host, error) {
				boot = b
				fake = engine.NewFakeHost(script)
				return fake, nil
			},
		}
		store, err := sessionx.Create(o.SessionsRoot, sessionx.Options{
			ID: "s-memwire", Workspace: ws, Unattended: true,
		})
		if err != nil {
			t.Fatalf("create store: %v", err)
		}
		defer store.Close()
		meter, err := cache.NewMeter(cache.MeterOptions{Root: o.UsageRoot, SessionID: "s-memwire",
			Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}})
		if err != nil {
			t.Fatalf("meter: %v", err)
		}
		defer meter.Close()
		ctrl, _, err := buildComponents(o, store, meter, discardSink{}, false)
		if err != nil {
			t.Fatalf("buildComponents: %v", err)
		}
		if err := ctrl.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		if err := ctrl.Submit(context.Background(), "pnpm 依赖用什么管理?"); err != nil {
			t.Fatalf("submit: %v", err)
		}
		return runOut{sent: fake.Sent(), boot: boot}
	}

	// 同一 workspace 跑开/关两形态：AppendSystemPrompt 内嵌工作区路径
	//（C1 启动快照），异 workspace 会造成与记忆无关的面差异——护栏对照
	// 必须同参。关闭形态不开库（种子库闲置），只影响注入不影响面。
	ws := t.TempDir()
	seedMemory(t, ws, "本项目用 pnpm 管理依赖")
	on := run(t, true, ws)
	off := run(t, false, ws)

	if len(on.sent) != 1 || len(on.sent[0].TurnTail) != 1 {
		t.Fatalf("enabled TurnTail = %#v, want 1 recall block", on.sent)
	}
	block := on.sent[0].TurnTail[0]
	if !strings.HasPrefix(block, "[记忆召回]\n- 本项目用 pnpm 管理依赖\n") {
		t.Fatalf("recall block = %q", block)
	}
	if len(off.sent) != 1 || len(off.sent[0].TurnTail) != 0 {
		t.Fatalf("disabled TurnTail = %#v, want empty", off.sent)
	}
	if on.boot.AppendSystemPrompt != off.boot.AppendSystemPrompt ||
		on.boot.MCPConfigJSON != off.boot.MCPConfigJSON ||
		strings.Join(on.boot.AllowedTools, ",") != strings.Join(off.boot.AllowedTools, ",") {
		t.Fatalf("boot face diverges between memory on/off:\non:  %+v\noff: %+v", on.boot, off.boot)
	}
}

// TestMemoryWiringOpenFailureFailsAssembly 钉：记忆已启用而库不可用 → 组装
// 失败（fail-closed；绝不静默降级成「开着开关却无记忆」的会话）。
func TestMemoryWiringOpenFailureFailsAssembly(t *testing.T) {
	ws := blockMemoryDir(t)
	o := Options{
		Config:       memoryTestConfig(true),
		Workspace:    ws,
		SessionsRoot: t.TempDir(),
		UsageRoot:    t.TempDir(),
		HostFactory:  fakeHostFactory(t),
	}
	store, err := sessionx.Create(o.SessionsRoot, sessionx.Options{
		ID: "s-memfail", Workspace: ws, Unattended: true,
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	meter, err := cache.NewMeter(cache.MeterOptions{Root: o.UsageRoot, SessionID: "s-memfail",
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	defer meter.Close()
	if _, _, err := buildComponents(o, store, meter, discardSink{}, false); err == nil {
		t.Fatalf("buildComponents must fail when memory enabled but dir unusable")
	}
}

// TestMemoryStoreSharedWithSubagentTree 钉会话树恰一 Store：工厂闭包把父会话
// 解析的实例下传子会话——磁盘库删除后子会话仍能从内存实例召回（若错误地
// 重新 Open，得到的是空库，无命中）。
func TestMemoryStoreSharedWithSubagentTree(t *testing.T) {
	ws := t.TempDir()
	preset := seedMemory(t, ws, "本项目用 pnpm 管理依赖")
	// 磁盘库已删：重新 Open = 空库（MkdirAll 重建），唯有内存实例可召回
	if err := os.RemoveAll(mulixmemory.Dir(ws)); err != nil {
		t.Fatalf("remove memory dir: %v", err)
	}
	script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var mu sync.Mutex
	var childFake *engine.FakeHost
	hf := func(engine.BootConfig) (engine.Host, error) {
		f := engine.NewFakeHost(script)
		mu.Lock()
		childFake = f
		mu.Unlock()
		return f, nil
	}
	o := Options{
		Config:       memoryTestConfig(true),
		Workspace:    ws,
		SessionsRoot: t.TempDir(),
		UsageRoot:    t.TempDir(),
		HostFactory:  hf, // 子会话宿主沿 Options 解析（assembleController 口径）
	}
	factory := o.subagentSessionFactory("s-parent", nil, *o.Config, hf, false, preset)
	h, err := factory(context.Background(), "pnpm 依赖用什么管理?")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, "child turn done", func() bool {
		select {
		case <-h.Done():
			return true
		default:
			return false
		}
	})
	if err := h.Err(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("child err: %v", err)
	}
	// 句柄归还先行（Windows：TempDir 清理依赖镜像句柄已关——Task 4 惯例）
	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("child close: %v", err)
	}
	mu.Lock()
	sent := childFake.Sent()
	mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("child sent = %d messages", len(sent))
	}
	if len(sent[0].TurnTail) != 1 || !strings.Contains(sent[0].TurnTail[0], "本项目用 pnpm 管理依赖") {
		t.Fatalf("child TurnTail = %#v, want recall from shared in-memory store", sent[0].TurnTail)
	}
}

// TestMemoryDisabledSubtreeInheritsZeroDelta 钉：记忆关闭的树，工厂下传 nil
// Store——子会话同样零注入（开关是树级一致的）。
func TestMemoryDisabledSubtreeInheritsZeroDelta(t *testing.T) {
	ws := t.TempDir()
	script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var mu sync.Mutex
	var childFake *engine.FakeHost
	hf := func(engine.BootConfig) (engine.Host, error) {
		f := engine.NewFakeHost(script)
		mu.Lock()
		childFake = f
		mu.Unlock()
		return f, nil
	}
	o := Options{
		Config:       memoryTestConfig(false),
		Workspace:    ws,
		SessionsRoot: t.TempDir(),
		UsageRoot:    t.TempDir(),
		HostFactory:  hf, // 子会话宿主沿 Options 解析（assembleController 口径）
	}
	factory := o.subagentSessionFactory("s-parent", nil, *o.Config, hf, false, nil)
	h, err := factory(context.Background(), "随便一个任务")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, "child turn done", func() bool {
		select {
		case <-h.Done():
			return true
		default:
			return false
		}
	})
	// 句柄归还先行（Windows：TempDir 清理依赖镜像句柄已关——Task 4 惯例）
	_ = h.Close(context.Background())
	mu.Lock()
	sent := childFake.Sent()
	mu.Unlock()
	if len(sent) != 1 || len(sent[0].TurnTail) != 0 {
		t.Fatalf("disabled child TurnTail = %#v, want empty", sent)
	}
}
