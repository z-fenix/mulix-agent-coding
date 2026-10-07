package guard

import (
	"context"
	"testing"
)

// TestHookEventConstants pins：钩子事件词表与引擎 hook_event 一致
// （engine.HookInfo.HookEvent：SessionStart|PreToolUse|PostToolUse|Stop）。
func TestHookEventConstants(t *testing.T) {
	if HookSessionStart != "SessionStart" || HookPreToolUse != "PreToolUse" ||
		HookPostToolUse != "PostToolUse" || HookStop != "Stop" {
		t.Fatalf("hook event constants drifted: %q %q %q %q",
			HookSessionStart, HookPreToolUse, HookPostToolUse, HookStop)
	}
}

// TestHooksDispatchOrder pins：同一事件的多个回调按注册顺序依次执行。
func TestHooksDispatchOrder(t *testing.T) {
	h := NewHooks()
	var order []string
	h.On(HookPreToolUse, func(ctx context.Context, p HookPayload) { order = append(order, "a") })
	h.On(HookPreToolUse, func(ctx context.Context, p HookPayload) { order = append(order, "b") })
	h.On(HookPreToolUse, func(ctx context.Context, p HookPayload) { order = append(order, "c") })
	h.Dispatch(context.Background(), HookPayload{Event: HookPreToolUse, Tool: "Bash"})
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("order = %v, want [a b c]", order)
	}
}

// TestHooksPanicIsolation pins：回调 panic 不外泄——后续回调照常执行，
// recovered 值经 onPanic 上报（事件、回调序号、panic 值）。
func TestHooksPanicIsolation(t *testing.T) {
	h := NewHooks()
	var secondRan bool
	type panicReport struct {
		ev      HookEvent
		handler int
		value   any
	}
	var reports []panicReport
	h.OnPanic = func(ev HookEvent, handlerIndex int, recovered any) {
		reports = append(reports, panicReport{ev, handlerIndex, recovered})
	}
	h.On(HookSessionStart, func(ctx context.Context, p HookPayload) { panic("boom-1") })
	h.On(HookSessionStart, func(ctx context.Context, p HookPayload) { secondRan = true })
	h.On(HookSessionStart, func(ctx context.Context, p HookPayload) { panic("boom-2") })
	h.Dispatch(context.Background(), HookPayload{Event: HookSessionStart})
	if !secondRan {
		t.Fatal("handler after panicking handler must still run")
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(reports))
	}
	if reports[0].ev != HookSessionStart || reports[0].handler != 0 || reports[0].value != "boom-1" {
		t.Fatalf("report 0 = %+v", reports[0])
	}
	if reports[1].handler != 2 || reports[1].value != "boom-2" {
		t.Fatalf("report 1 = %+v", reports[1])
	}
}

// TestHooksPanicWithoutReporter pins：未设 onPanic 时 panic 同样被吞掉
// （隔离是默认行为，不依赖调用方配置）。
func TestHooksPanicWithoutReporter(t *testing.T) {
	h := NewHooks()
	ran := false
	h.On(HookStop, func(ctx context.Context, p HookPayload) { panic("ignored") })
	h.On(HookStop, func(ctx context.Context, p HookPayload) { ran = true })
	h.Dispatch(context.Background(), HookPayload{Event: HookStop})
	if !ran {
		t.Fatal("isolation must not depend on onPanic being set")
	}
}

// TestHooksPanickingReporterSwallowed pins：OnPanic 上报自身 panic 也被吞
// （隔离不因消费者故障失效——reportPanic 二级 recover）。
func TestHooksPanickingReporterSwallowed(t *testing.T) {
	h := NewHooks()
	h.OnPanic = func(ev HookEvent, handlerIndex int, recovered any) {
		panic("reporter exploded")
	}
	ran := false
	h.On(HookStop, func(ctx context.Context, p HookPayload) { panic("first") })
	h.On(HookStop, func(ctx context.Context, p HookPayload) { ran = true })
	h.Dispatch(context.Background(), HookPayload{Event: HookStop})
	if !ran {
		t.Fatal("panicking reporter must not break dispatch isolation")
	}
}

// TestHooksUnknownEventNoOp pins：未知事件词表派发为 no-op（静默是良性
// 失败模式——engine 新增 hook_event 时 guard 侧零处理）。
func TestHooksUnknownEventNoOp(t *testing.T) {
	h := NewHooks()
	called := false
	h.On(HookStop, func(ctx context.Context, p HookPayload) { called = true })
	h.Dispatch(context.Background(), HookPayload{Event: HookEvent("Nope")})
	if called {
		t.Fatal("unknown event must not fire Stop handlers")
	}
}

// TestHooksEventFiltering pins：回调只收注册时绑定的事件类型。
func TestHooksEventFiltering(t *testing.T) {
	h := NewHooks()
	startCalls, postCalls := 0, 0
	h.On(HookSessionStart, func(ctx context.Context, p HookPayload) { startCalls++ })
	h.On(HookPostToolUse, func(ctx context.Context, p HookPayload) { postCalls++ })
	h.Dispatch(context.Background(), HookPayload{Event: HookSessionStart})
	h.Dispatch(context.Background(), HookPayload{Event: HookSessionStart})
	h.Dispatch(context.Background(), HookPayload{Event: HookPostToolUse})
	if startCalls != 2 || postCalls != 1 {
		t.Fatalf("start=%d post=%d", startCalls, postCalls)
	}
}

// TestHooksDispatchNoHandlers pins：无回调事件为 no-op（不 panic）。
func TestHooksDispatchNoHandlers(t *testing.T) {
	h := NewHooks()
	h.Dispatch(context.Background(), HookPayload{Event: HookStop})
}

// TestHooksPayloadPassThrough pins：载荷原样送达回调（control 从
// engine.HookInfo 填充；guard 不依赖 engine——分层）。
func TestHooksPayloadPassThrough(t *testing.T) {
	h := NewHooks()
	var got HookPayload
	h.On(HookPreToolUse, func(ctx context.Context, p HookPayload) { got = p })
	in := []byte(`{"command":"go test"}`)
	h.Dispatch(context.Background(), HookPayload{
		Event: HookPreToolUse, HookID: "h1", HookName: "audit", Tool: "Bash", Input: in,
	})
	if got.Event != HookPreToolUse || got.HookID != "h1" || got.HookName != "audit" ||
		got.Tool != "Bash" || string(got.Input) != `{"command":"go test"}` {
		t.Fatalf("payload = %+v", got)
	}
}

// TestHooksCtxPassThrough pins：ctx 透传（控制方取消传导到回调）。
func TestHooksCtxPassThrough(t *testing.T) {
	h := NewHooks()
	type key struct{}
	var ok bool
	h.On(HookStop, func(ctx context.Context, p HookPayload) {
		_, ok = ctx.Value(key{}).(string)
	})
	ctx := context.WithValue(context.Background(), key{}, "v")
	h.Dispatch(ctx, HookPayload{Event: HookStop})
	if !ok {
		t.Fatal("ctx value not visible in handler")
	}
}
