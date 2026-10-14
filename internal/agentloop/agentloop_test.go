package agentloop

import (
	"context"
	"testing"

	"mulix/internal/engine"

	"mulix/internal/kernel"
)

// fakeSource/记录回调——泵语义逐字节对齐的判定面。
type fakeSource struct {
	events chan engine.Event
	host   engine.Host
}

func (s *fakeSource) Events() <-chan engine.Event { return s.events }
func (s *fakeSource) Host() engine.Host           { return s.host }

type recordCallbacks struct {
	route    []string
	hostSeen []engine.Host
	closed   bool
}

func (r *recordCallbacks) OnInit(host engine.Host, ev engine.Event) {
	r.route = append(r.route, "init")
	r.hostSeen = append(r.hostSeen, host)
}
func (r *recordCallbacks) OnAssistant(ev engine.Event)  { r.route = append(r.route, "assistant") }
func (r *recordCallbacks) OnToolResult(ev engine.Event) { r.route = append(r.route, "toolresult") }
func (r *recordCallbacks) OnResult(ev engine.Event)     { r.route = append(r.route, "result") }
func (r *recordCallbacks) OnTrace(ev engine.Event)      { r.route = append(r.route, "trace") }
func (r *recordCallbacks) OnPermissionDenied(ev engine.Event) {
	r.route = append(r.route, "permission_denied")
}
func (r *recordCallbacks) OnExit(ev engine.Event) { r.route = append(r.route, "exit") }
func (r *recordCallbacks) OnControlRequest(host engine.Host, ev engine.Event) {
	r.route = append(r.route, "control_request")
	r.hostSeen = append(r.hostSeen, host)
}
func (r *recordCallbacks) OnHookResponse(ev engine.Event) {
	r.route = append(r.route, "hook_response")
}
func (r *recordCallbacks) OnEngineClosed() {
	r.route = append(r.route, "engine_closed")
	r.closed = true
}

// TestDefaultRunRoutesByKindAndDrains 钉 012 FR-101:按 Kind 路由到回调
// (逐字节对齐现状 dispatch);流关闭 → OnEngineClosed;Run 返回 nil。
func TestDefaultRunRoutesByKindAndDrains(t *testing.T) {
	src := &fakeSource{events: make(chan engine.Event, 16), host: engine.NewFakeHost(engine.FakeScript{})}
	cb := &recordCallbacks{}
	// 事件序列:9 路由类 + unknown(跳过不入回调——design §1.2 裁定)。
	src.events <- engine.Event{Kind: engine.KindInit}
	src.events <- engine.Event{Kind: engine.KindAssistant}
	src.events <- engine.Event{Kind: engine.KindToolResult}
	src.events <- engine.Event{Kind: engine.KindResult}
	src.events <- engine.Event{Kind: engine.KindTrace}
	src.events <- engine.Event{Kind: engine.KindPermissionDenied}
	src.events <- engine.Event{Kind: engine.KindExit}
	src.events <- engine.Event{Kind: engine.KindControlRequest}
	src.events <- engine.Event{Kind: engine.KindHookResponse}
	src.events <- engine.Event{Kind: engine.EventKind("unknown_kind")}
	close(src.events)

	err := Default{}.Run(context.Background(), src, cb)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"init", "assistant", "toolresult", "result", "trace",
		"permission_denied", "exit", "control_request", "hook_response", "engine_closed"}
	if len(cb.route) != len(want) {
		t.Fatalf("route = %v, want %v (unknown 跳过不入回调;engine_closed 收口)", cb.route, want)
	}
	for i := range want {
		if cb.route[i] != want[i] {
			t.Fatalf("route[%d] = %q, want %q", i, cb.route[i], want[i])
		}
	}
	if !cb.closed {
		t.Fatal("OnEngineClosed must fire after drain")
	}
	if len(cb.hostSeen) != 2 { // init + control_request 需要 host 透传
		t.Fatalf("host passthrough count = %d, want 2", len(cb.hostSeen))
	}
}

// TestReplayRoutesMirrorEvents 钉 013 FR-102:镜像事件流经 Callbacks 重放
// (路由序列对齐);replay 不触发 OnEngineClosed(只读重放)。
func TestReplayRoutesMirrorEvents(t *testing.T) {
	cb := &recordCallbacks{}
	events := []engine.Event{
		{Kind: engine.KindInit},
		{Kind: engine.KindAssistant},
		{Kind: engine.KindResult},
	}
	if err := Replay(context.Background(), events, cb); err != nil {
		t.Fatalf("replay: %v", err)
	}
	want := []string{"init", "assistant", "result"}
	if len(cb.route) != len(want) {
		t.Fatalf("route = %v, want %v", cb.route, want)
	}
	for i := range want {
		if cb.route[i] != want[i] {
			t.Fatalf("route[%d] = %q, want %q", i, cb.route[i], want[i])
		}
	}
	if cb.closed {
		t.Fatal("replay must not fire OnEngineClosed (只读重放)")
	}
}

// TestPluginMount 钉 021 FR-101:loop 插件形态——Mount 提供键/取回 Loop
// 接口/同键回滚沿 kernel 语义。
func TestPluginMount(t *testing.T) {
	root := kernel.NewContext(nil)
	if err := root.Mount(NewPlugin()); err != nil {
		t.Fatalf("mount: %v", err)
	}
	keys := root.Keys()
	found := false
	for _, k := range keys {
		if k == "agentloop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("keys = %v, want agentloop", keys)
	}
	svc, ok := root.Service("agentloop")
	if !ok {
		t.Fatal("service agentloop missing")
	}
	if _, ok := svc.(Loop); !ok {
		t.Fatalf("service type %T, want Loop", svc)
	}
	// 同键已注册→Mount 失败回滚(fail-closed 键即契约)。
	if err := root.Mount(NewPlugin()); err == nil {
		t.Fatal("duplicate mount must fail-closed")
	}
}
