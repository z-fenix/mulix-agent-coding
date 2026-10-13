package cli

import (
	"os"
	"strings"
	"testing"

	"mulix/internal/agentloop"
	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/guard"
	"mulix/internal/llm"
	"mulix/internal/sessionx"
)

// testAssemblySink 是最小 Sink(Publish no-op——装配测试不消费事件)。
type testAssemblySink struct{}

func (testAssemblySink) Publish(any) {}

// TestAssembleKernelContextServiceKeys 钉 011 FR-101(SC-001):装配产出的
// 依赖以 kernel 服务键登记——键就位且与 Options 字段同源(同值)。
func TestAssembleKernelContextServiceKeys(t *testing.T) {
	store := &sessionx.Store{}
	meter := &cache.Meter{}
	gate := &guard.Gate{}
	sink := testAssemblySink{}
	cfg := config.Config{}
	o := control.Options{
		HostFactory: func(boot engine.BootConfig) (engine.Host, error) { return nil, nil },
		Sink:        sink,
		Store:       store,
		Meter:       meter,
		Gate:        gate,
		Config:      cfg,
	}
	root, opts, err := assembleKernelContext(o)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, k := range []string{"sink", "store", "meter", "gate", "config", "agentloop"} {
		if _, ok := root.Service(k); !ok {
			t.Fatalf("key %q missing (SC-001)", k)
		}
	}
	if v, ok := root.Service("agentloop"); ok {
		if _, isDefault := v.(*agentloop.Default); !isDefault {
			t.Fatalf("agentloop = %T, want *agentloop.Default", v)
		}
	}
	if v, ok := root.Service("sink"); !ok || v.(control.Sink) != sink {
		t.Fatalf("sink same-source check failed")
	}
	if v, ok := root.Service("store"); !ok || v.(*sessionx.Store) != store {
		t.Fatalf("store same-source check failed")
	}
	if v, ok := root.Service("meter"); !ok || v.(*cache.Meter) != meter {
		t.Fatalf("meter same-source check failed")
	}
	if v, ok := root.Service("gate"); !ok || v.(*guard.Gate) != gate {
		t.Fatalf("gate same-source check failed")
	}
	if opts.Store != store || opts.Meter != meter || opts.Gate != gate {
		t.Fatalf("back-filled Options mismatch")
	}
	if _, ok := root.Service("approvals"); ok {
		t.Fatal("approvals key must be absent when nil (nil=功能关闭)")
	}
	// llm 键:seam-only 适配器(键就位 + Name 断言——SC-002 间接面)。
	vllm, ok := root.Service("llm")
	if !ok {
		t.Fatal("llm key missing (011 FR-101)")
	}
	if p, ok := vllm.(llm.Provider); !ok || p.Name() != "claude-cli" {
		t.Fatalf("llm provider = %v, want claude-cli adapter", vllm)
	}
}

// TestProfileServiceKey 钉 014 FR-104(终审 Important 修复:计划 T3 Step 1
// 的覆盖缺口补齐):profile 服务键就位且值=standard。
func TestProfileServiceKey(t *testing.T) {
	cfg := config.Config{}
	o := control.Options{
		HostFactory: func(boot engine.BootConfig) (engine.Host, error) { return nil, nil },
		Sink:        testAssemblySink{},
		Store:       &sessionx.Store{},
		Meter:       &cache.Meter{},
		Gate:        &guard.Gate{},
		Config:      cfg,
	}
	root, _, err := assembleKernelContext(o)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	v, ok := root.Service("profile")
	if !ok {
		t.Fatal("profile key missing (FR-104)")
	}
	if pm, isMode := v.(config.ProfileMode); !isMode || pm != config.ProfileStandard {
		t.Fatalf("profile = %v, want standard", v)
	}
}

// withEventbusEnabled 返回开总线门控的配置副本(018:T2 起 flag 退役——
// 单真源=cfg.Eventbus;testConfig 定义在 tui_test.go 343 行,同包直用)。
func withEventbusEnabled() *config.Config {
	cfg := testConfig()
	cfg.Eventbus.RawEnable = "true"
	return cfg
}

// TestKernelKeysIncludeEventsKey 钉 016 终审 Important:events 键注册于
// assembleKernelContext 返回之后(总线开启时)——KernelKeys 快照必须取自
// 装配末尾,否则 kernel_inspect.services 在总线会话确定性静默缺键。
func TestKernelKeysIncludeEventsKey(t *testing.T) {
	var boot engine.BootConfig
	// 手工清理:总线为进程级单例(JetStream 句柄持有至进程关停)——
	// t.TempDir 的 RemoveAll 在 Windows 上会撞文件锁。
	ws, err := os.MkdirTemp("", "mulix016")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })
	opts := Options{Workspace: ws, SessionsRoot: ws, UsageRoot: ws,
		Config: withEventbusEnabled(),
		HostFactory: func(bc engine.BootConfig) (engine.Host, error) {
			boot = bc
			script, ferr := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
			if ferr != nil {
				return nil, ferr
			}
			return engine.NewFakeHost(script), nil
		}}
	h := mustOpenSession(t, opts, "", nil)
	defer h.close()
	found := false
	for _, e := range boot.Env {
		if strings.HasPrefix(e, "MULIX_KERNEL_KEYS=") && strings.Contains(e, "events") {
			found = true
		}
	}
	if !found {
		t.Fatalf("MULIX_KERNEL_KEYS missing events key (env=%v)", boot.Env)
	}
}
