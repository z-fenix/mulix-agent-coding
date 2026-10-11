package eventbus

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
