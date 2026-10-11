package eventbus

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mulix/internal/kernel"
)

// 承重断言(010 FR-107):eventbus 实现 kernel 事件小接口——kernel 零 NATS
// 依赖的隔离边界由编译期保证。
var _ kernel.EventPublisher = (*Bus)(nil)
var _ kernel.EventSubscriber = (*Bus)(nil)

// TestOpenCloseBus 钉 FR-107/FR-108 骨架:内嵌 server + JetStream 可启停,
// InProcess 连接可用,Close 后资源收口(不监听 TCP——仅内嵌连接)。
func TestOpenCloseBus(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b.Conn() == nil || b.JS() == nil {
		t.Fatal("in-process conn and JetStream context must be available")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestDeclareEventsAndFailClosed 钉 FR-101 类型化:声明幂等;未声明 subject
// 经 validateDeclared 拒绝(fail-closed)。
// 注:发布/订阅层的 fail-closed 接线属 Task 3(Subscribe/Publish 定义处);
// 本任务只钉声明表与校验原语。
func TestDeclareEventsAndFailClosed(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("turn.started"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := b.DeclareEvents("turn.started"); err != nil { // 幂等
		t.Fatalf("declare idempotent: %v", err)
	}
	if err := b.validateDeclared("mulix.turn.started"); err != nil {
		t.Fatalf("declared subject must validate: %v", err)
	}
	if err := b.validateDeclared("mulix.undeclared.event"); err == nil {
		t.Fatal("undeclared subject must fail validateDeclared (fail-closed)")
	}
	if !strings.Contains(b.validateDeclared("mulix.undeclared.event").Error(), "not declared") {
		t.Fatal("error must name the fail-closed reason")
	}
}

// TestPublishSubscribeRoundTrip 钉 FR-108:发布持久化到 JetStream;进程内
// 订阅收到相同 payload;unsub 后不再收到。
func TestPublishSubscribeRoundTrip(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("echo"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	got := make(chan []byte, 1)
	unsub, err := b.Subscribe("echo", func(p []byte) { got <- p })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := b.Publish("echo", []byte(`{"k":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case p := <-got:
		if string(p) != `{"k":1}` {
			t.Fatalf("payload = %s", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive payload")
	}
	unsub()
	if err := b.Publish("echo", []byte(`{"k":2}`)); err != nil {
		t.Fatalf("publish2: %v", err)
	}
	select {
	case p := <-got:
		t.Fatalf("unsubscribed still received: %s", p)
	case <-time.After(200 * time.Millisecond):
	}
	// 持久化:JetStream 流存在(replay 断言属 P5)。
	if _, err := b.JS().StreamInfo(streamName("echo")); err != nil {
		t.Fatalf("stream persisted: %v", err)
	}
}

// TestEmitAggregatesErrors 钉 FR-101 Emit 语义:持久发布 + 全订阅按序通知,
// 错误聚合(声明校验错/发布错并列返回)。
func TestEmitAggregatesErrors(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("notify"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	order := make([]int, 0, 2)
	unsub1, _ := b.Subscribe("notify", func([]byte) { order = append(order, 1) })
	defer unsub1()
	unsub2, _ := b.Subscribe("notify", func([]byte) { order = append(order, 2) })
	defer unsub2()
	if errs := b.Emit("notify", []byte("x")); len(errs) != 0 {
		t.Fatalf("emit errors = %v, want none", errs)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("order = %v, want [1 2] (按注册序)", order)
	}
	if errs := b.Emit("undeclared.emit", []byte("x")); len(errs) == 0 {
		t.Fatal("emit on undeclared must aggregate a fail-closed error")
	}
}

// TestWaterfallChainAndShortCircuit 钉 FR-101 Waterfall:链式环绕——监听器
// 收 (arg,next),全链调 next → 直通;不调 next 即短路(其返回值为最终值)。
func TestWaterfallChainAndShortCircuit(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("wf"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	unsubA, _ := b.SubscribeWaterfall("wf", func(arg any, next WaterfallNext) any {
		return next(arg.(int) + 1)
	})
	defer unsubA()
	unsubB, _ := b.SubscribeWaterfall("wf", func(arg any, next WaterfallNext) any {
		return next(arg.(int) + 10)
	})
	defer unsubB()
	if got := b.Waterfall("wf", 1, func(a any) any { return a.(int) * 2 }); got != 24 {
		t.Fatalf("chain result = %v, want 24 ((1+1+10)*2)", got)
	}
	unsubA2, _ := b.SubscribeWaterfall("wf", func(any, WaterfallNext) any { return "short" })
	defer unsubA2()
	called := false
	unsubB2, _ := b.SubscribeWaterfall("wf", func(arg any, next WaterfallNext) any {
		called = true
		return next(arg)
	})
	defer unsubB2()
	if got := b.Waterfall("wf", 1, func(a any) any { return a }); got != "short" {
		t.Fatalf("short-circuit result = %v, want short", got)
	}
	if called {
		t.Fatal("short-circuited listener must not run (B 未执行)")
	}
}

// TestParallelRunsAllAndAggregates 钉 FR-101 Parallel:并发全跑(含慢订阅)、
// 单订阅 panic 被收为错误(不炸总线)、错误聚合。
func TestParallelRunsAllAndAggregates(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("par"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	done := make(chan struct{})
	slowRan := make(chan struct{})
	unsub1, _ := b.Subscribe("par", func([]byte) { <-done; close(slowRan) })
	_ = unsub1
	unsub2, _ := b.Subscribe("par", func([]byte) { panic("subscriber exploded") })
	defer unsub2()
	unsub3, _ := b.Subscribe("par", func([]byte) {})
	defer unsub3()
	close(done) // 放行慢订阅(必须在 Parallel 之前——Parallel 阻塞至全部返回)
	errs := b.Parallel("par", []byte("x"))
	<-slowRan // 慢订阅必须已跑完(Parallel 等待全部)
	if len(errs) != 1 || !strings.Contains(fmt.Sprint(errs[0]), "subscriber exploded") {
		t.Fatalf("errs = %v, want exactly the panic error", errs)
	}
}

// TestSerialStopsOnFirstError 钉 FR-101 Serial:按注册序,首错停(后续不执行)。
func TestSerialStopsOnFirstError(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("ser"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	ran := []int{}
	unsub1, _ := b.SubscribeSerial("ser", func([]byte) error { ran = append(ran, 1); return nil })
	defer unsub1()
	unsub2, _ := b.SubscribeSerial("ser", func([]byte) error { ran = append(ran, 2); return fmt.Errorf("boom") })
	defer unsub2()
	unsub3, _ := b.SubscribeSerial("ser", func([]byte) error { ran = append(ran, 3); return nil })
	defer unsub3()
	if err := b.Serial("ser", []byte("x")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("serial err = %v, want boom", err)
	}
	if len(ran) != 2 || ran[0] != 1 || ran[1] != 2 {
		t.Fatalf("ran = %v, want [1 2] (首错停,后续不执行)", ran)
	}
}

// TestBailStopsOnFirstNonNil 钉 FR-101 Bail:按序,首个非 nil 结果停;全 nil
// 返回 (nil,false)。
func TestBailStopsOnFirstNonNil(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("bail"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	unsub1, _ := b.SubscribeBail("bail", func(any) any { return "first-hit" })
	defer unsub1()
	unsub2, _ := b.SubscribeBail("bail", func(any) any { return "never" })
	defer unsub2()
	if v, found := b.Bail("bail", nil); !found || v != "first-hit" {
		t.Fatalf("bail = (%v,%v), want (first-hit,true)", v, found)
	}
}

// TestEmitPanicPropagates 钉设计 §2.2 规范表(Emit panic=沿调用栈传播;
// 仅 Parallel recover)——终审 Important 修复的判别测试。
func TestEmitPanicPropagates(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("boom"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	unsub, _ := b.Subscribe("boom", func([]byte) { panic("emit subscriber panic") })
	defer unsub()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Emit must propagate subscriber panic (fail-closed, 设计 §2.2)")
		}
	}()
	_ = b.Emit("boom", []byte("x"))
}
