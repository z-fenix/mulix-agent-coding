package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// fixturePath 解析仓库根 fixtures/engine 下的 fixture。
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "engine", name))
	if err != nil {
		t.Fatalf("fixture path: %v", err)
	}
	return abs
}

// collectFakeEvents 带超时收集事件。
func collectFakeEvents(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	var got []Event
	deadline := time.After(5 * time.Second)
	for len(got) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("events closed after %d/%d", len(got), n)
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timeout: %d/%d events", len(got), n)
		}
	}
	return got
}

// TestFakeHostFixtureReplayHappyTurn pins：happy-turn fixture 回放——
// Start 回放 pre 段（hook 对 + init，无噪音），首次 Send 回放 turn 段
// （assistant..result）；Info 指纹 = "fake"、session/版本与 fixture 一致。
func TestFakeHostFixtureReplayHappyTurn(t *testing.T) {
	script, err := FixtureScript(fixturePath(t, "happy-turn.jsonl"))
	if err != nil {
		t.Fatalf("fixture script: %v", err)
	}
	inits, noise := 0, 0
	for _, ev := range script.OnStart {
		switch {
		case ev.Kind == KindInit:
			inits++
		case ev.Kind == KindNoise:
			noise++
		}
	}
	if inits != 1 || noise != 0 {
		t.Fatalf("OnStart must be exactly one init and noise-free, got init=%d noise=%d", inits, noise)
	}
	f := NewFakeHost(script)
	if err := f.Start(context.Background(), BootConfig{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	pre := collectFakeEvents(t, f.Events(), len(script.OnStart))
	if pre[0].Kind != KindHookStarted || pre[1].Kind != KindHookResponse || pre[2].Kind != KindInit {
		t.Fatalf("fixture order wrong: %s/%s/%s", pre[0].Kind, pre[1].Kind, pre[2].Kind)
	}
	info := f.Info()
	if info.Fingerprint != "fake" {
		t.Fatalf("fingerprint = %q, want fake", info.Fingerprint)
	}
	if info.SessionID != "9ce73bff-164b-4488-a4e6-7d4e1fa4efc6" || info.CLIVersion != "2.1.291" {
		t.Fatalf("info from fixture init wrong: %+v", info)
	}

	if err := f.Send(context.Background(), UserMessage{Text: "turn 1"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	turn := collectFakeEvents(t, f.Events(), len(script.Turns[0]))
	if turn[0].Kind != KindAssistant || turn[len(turn)-1].Kind != KindResult {
		t.Fatalf("turn segment shape wrong: %s..%s", turn[0].Kind, turn[len(turn)-1].Kind)
	}
	if got := turn[len(turn)-1].Result.ResultText; got == "" {
		t.Fatal("result text empty")
	}
	if err := f.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestFakeHostFixtureUsageTwoTurns pins：usage fixture 两回合——第二回合
// 段内含重发的 init/hooks（按 fixture 原样回放；幂等消费是 control 的职责）；
// 两个 result 的 result_index/成本依次为 0/0.111375、1/0.222565。
func TestFakeHostFixtureUsageTwoTurns(t *testing.T) {
	script, err := FixtureScript(fixturePath(t, "usage.jsonl"))
	if err != nil {
		t.Fatalf("fixture script: %v", err)
	}
	if len(script.Turns) != 2 {
		t.Fatalf("usage fixture must yield 2 turns, got %d", len(script.Turns))
	}
	// 段首重发钉：turn2 首事件必须是重发的 init（幂等消费是 control 的职责）
	if script.Turns[1][0].Kind != KindInit {
		t.Fatalf("turn2 must start with re-emitted init, got %s", script.Turns[1][0].Kind)
	}
	for _, seg := range append(script.Turns, script.OnStart) {
		for _, ev := range seg {
			if ev.Kind == KindNoise {
				t.Fatal("noise must not enter the script")
			}
		}
	}
	f := NewFakeHost(script)
	if err := f.Start(context.Background(), BootConfig{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	collectFakeEvents(t, f.Events(), len(script.OnStart))

	if err := f.Send(context.Background(), UserMessage{Text: "t1"}); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	turn1 := collectFakeEvents(t, f.Events(), len(script.Turns[0]))
	if turn1[len(turn1)-1].Result.ResultIndex == nil || *turn1[len(turn1)-1].Result.ResultIndex != 0 {
		t.Fatalf("turn1 result_index wrong: %+v", turn1[len(turn1)-1].Result)
	}
	if got := turn1[len(turn1)-1].Result.TotalCostUSD; got == nil || *got != 0.111375 {
		t.Fatalf("turn1 cost wrong: %v", got)
	}

	if err := f.Send(context.Background(), UserMessage{Text: "t2"}); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	turn2 := collectFakeEvents(t, f.Events(), len(script.Turns[1]))
	last := turn2[len(turn2)-1]
	if last.Kind != KindResult || last.Result.ResultIndex == nil || *last.Result.ResultIndex != 1 {
		t.Fatalf("turn2 result wrong: %+v", last)
	}
	if got := last.Result.TotalCostUSD; got == nil || *got != 0.222565 {
		t.Fatalf("turn2 cost wrong: %v", got)
	}
	if err := f.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestFakeHostScriptExhausted pins：Turns 耗尽 → Send 显式报错（可调试，
// 不静默循环）。
func TestFakeHostScriptExhausted(t *testing.T) {
	f := NewFakeHost(FakeScript{
		OnStart: nil, // 合成 init
		Turns:   [][]Event{{{Kind: KindResult, Result: &ResultInfo{}}}},
	})
	if err := f.Start(context.Background(), BootConfig{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	collectFakeEvents(t, f.Events(), 1) // 合成 init
	if err := f.Send(context.Background(), UserMessage{Text: "only"}); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	collectFakeEvents(t, f.Events(), 1)
	if err := f.Send(context.Background(), UserMessage{Text: "extra"}); err == nil {
		t.Fatal("exhausted script must fail Send")
	}
	_ = f.Close(context.Background())
}

// TestFakeHostCloseBeforeStart pins：Close 先于 Start —— Start 返回错误
// （不 panic：handshake 只由 Close 关闭一次），Close 幂等。
func TestFakeHostCloseBeforeStart(t *testing.T) {
	f := NewFakeHost(FakeScript{})
	if err := f.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := f.Start(context.Background(), BootConfig{}); err == nil {
		t.Fatal("Start after Close must fail")
	}
	if err := f.Close(context.Background()); err != nil {
		t.Fatalf("close idempotent: %v", err)
	}
}

// TestFakeHostControlInjection pins：可编程控制请求注入点——注入的
// control_request 进入事件流；RespondControl 按信封记录（id+payload，供
// e2e 断言）；Interrupt 计数。
func TestFakeHostControlInjection(t *testing.T) {
	f := NewFakeHost(FakeScript{})
	if err := f.Start(context.Background(), BootConfig{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	collectFakeEvents(t, f.Events(), 1) // 合成 init

	cr := ControlRequest{RequestID: "r-1", Request: ControlRequestBody{SubType: "mcp_message"}}
	if err := f.InjectControlRequest(cr); err != nil {
		t.Fatalf("inject: %v", err)
	}
	ev := collectFakeEvents(t, f.Events(), 1)[0]
	if ev.Kind != KindControlRequest || ev.Control == nil || ev.Control.RequestID != "r-1" {
		t.Fatalf("injected control request wrong: %+v", ev)
	}

	payload := ControlResponse{SubType: "success", Response: []byte(`{"behavior":"allow"}`)}
	if err := f.RespondControl(context.Background(), "r-1", payload); err != nil {
		t.Fatalf("respond: %v", err)
	}
	if err := f.Interrupt(context.Background()); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	responses := f.Responses()
	if len(responses) != 1 || responses[0].ID != "r-1" || string(responses[0].Payload.Response) != `{"behavior":"allow"}` {
		t.Fatalf("responses wrong: %+v", responses)
	}
	if got := f.Interrupts(); got != 1 {
		t.Fatalf("interrupts = %d", got)
	}
	_ = f.Close(context.Background())
}

// TestFakeHostCloseClosesEvents pins：Close 后事件通道关闭、Send 报错、
// Close 幂等。
func TestFakeHostCloseClosesEvents(t *testing.T) {
	f := NewFakeHost(FakeScript{})
	if err := f.Start(context.Background(), BootConfig{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := f.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := f.Close(context.Background()); err != nil {
		t.Fatalf("close must be idempotent: %v", err)
	}
	// 排空缓冲直到关闭（合成 init 可能未读）
	for {
		_, ok := <-f.Events()
		if !ok {
			break
		}
	}
	if err := f.Send(context.Background(), UserMessage{Text: "x"}); err == nil {
		t.Fatal("Send after Close must fail")
	}
	if err := f.InjectControlRequest(ControlRequest{RequestID: "r"}); err == nil {
		t.Fatal("inject after Close must fail")
	}
}

// TestFakeHostSentRecords pins：Send 的输入可断言（e2e 校验 turn tail 注入）。
func TestFakeHostSentRecords(t *testing.T) {
	f := NewFakeHost(FakeScript{
		Turns: [][]Event{{{Kind: KindResult, Result: &ResultInfo{}}}},
	})
	if err := f.Start(context.Background(), BootConfig{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 消费合成 init（Start 已入缓冲）
	collectFakeEvents(t, f.Events(), 1)
	if err := f.Send(context.Background(), UserMessage{Text: "hello", TurnTail: []string{"tail-1"}}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := f.Sent()
	if len(sent) != 1 || sent[0].Text != "hello" || len(sent[0].TurnTail) != 1 || sent[0].TurnTail[0] != "tail-1" {
		t.Fatalf("sent wrong: %+v", sent)
	}
	_ = f.Close(context.Background())
}
