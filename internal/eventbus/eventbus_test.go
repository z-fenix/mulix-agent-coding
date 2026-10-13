package eventbus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go"

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

// TestOpenBusEndpointHandshake 钉 018 FR-103 前置:监听+endpoint 握手——
// endpoint 文件存在且非 InProcess 客户端可经它连接收发。
// subject 真身:桥声明 "mirror.test" → Emit 加 "mulix." 前缀 →
// 总线实际 subject = mulix.mirror.test(评审 Critical 修正)。
func TestOpenBusEndpointHandshake(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()

	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	url := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(url, "nats://127.0.0.1:") {
		t.Fatalf("endpoint = %q, want nats://127.0.0.1:<port>", url)
	}
	// 独立连接(不经 InProcess 选项)——跨进程形态核验。
	if err := b.DeclareEvents("mirror.test"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatalf("external connect: %v", err)
	}
	defer nc.Close()
	got := make(chan string, 1)
	sub, err := nc.Subscribe("mulix.mirror.test", func(m *nats.Msg) { got <- string(m.Data) })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	nc.Flush() // SUB 协议先于 PUB 到达服务器(否则订阅注册竞态——探针坐实)
	if err := b.Publish("mirror.test", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-got:
		if msg != "hello" {
			t.Fatalf("msg = %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("external subscriber got nothing")
	}
}

// syncBuffer:回调 goroutine 写 out 与主 goroutine 轮询并发——mutex 化
// (评审 Minor 修正:-race 不可用不等于无竞态)。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestObserveMirrors 钉 018 FR-103:观察面——订阅 mulix.mirror.* 实时收行;
// ctx 取消干净退出;连接失败 fail-closed。
func TestObserveMirrors(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}

	var out syncBuffer
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ObserveMirrors(ctx, strings.TrimSpace(string(raw)), &out) }()
	time.Sleep(200 * time.Millisecond) // 订阅就绪
	// subject 真身:声明 "mirror.turn_started" → Emit 加前缀 → mulix.mirror.turn_started。
	if err := b.DeclareEvents("mirror.turn_started"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := b.Publish("mirror.turn_started", []byte(`{"seq":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "turn_started") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "turn_started") {
		t.Fatalf("observe out = %q, want kind 行", out.String())
	}
	stop() // 干净退出
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("observe exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observe did not exit after ctx cancel")
	}
	// 连接失败 fail-closed。
	if err := ObserveMirrors(context.Background(), "nats://127.0.0.1:1", io.Discard); err == nil {
		t.Fatal("bad url must fail-closed")
	}
}

// TestObserveMirrorsPeerClosed 钉 019 FR-102:对端(会话进程)关闭→观察面
// 报错退出(NoReconnect——禁自动重连,静默冻结窗口关闭)。
func TestObserveMirrorsPeerClosed(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- ObserveMirrors(context.Background(), strings.TrimSpace(string(raw)), io.Discard) }()
	time.Sleep(200 * time.Millisecond) // 订阅就绪
	b.Close()                          // 对端死亡
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("peer closed must fail-visible (非 nil), 不允许静默冻结")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("observe frozen after peer close (NoReconnect 未生效?)")
	}
}

// TestBusCloseRemovesEndpoint 钉 019 FR-103:进程收尾清理——Close 后
// endpoint 不存在(观察面不再读到死端点);幂等(Close 二次不报错)。
func TestBusCloseRemovesEndpoint(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "endpoint")); err != nil {
		t.Fatalf("endpoint must exist before close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "endpoint")); !os.IsNotExist(err) {
		t.Fatalf("endpoint after close = %v, want NotExist", err)
	}
	if err := b.Close(); err != nil { // 幂等
		t.Fatalf("second close: %v", err)
	}
}

// TestObserveSummaryRuneSafe 钉 019 FR-103:多字节字符截断不 mojibake
// (rune 边界——200 字节处劈开中文不再产生非法 UTF-8)。
func TestObserveSummaryRuneSafe(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	var out syncBuffer
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- ObserveMirrors(ctx, strings.TrimSpace(string(raw)), &out) }()
	time.Sleep(200 * time.Millisecond)
	long := strings.Repeat("好", 300) // 900 字节——触发 200 字节截断
	if err := b.DeclareEvents("mirror.t"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := b.Publish("mirror.t", []byte(long)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !utf8.ValidString(lastLine(out.String())) {
		time.Sleep(20 * time.Millisecond)
	}
	line := lastLine(out.String())
	if !utf8.ValidString(line) {
		t.Fatalf("truncated line not valid UTF-8 (字节劈开多字节字符): %q", line)
	}
	// 截断本身也钉(评审 Advisory):行 payload 部分不得超 200 字节上限
	// (66 个完整「好」=198 字节——「截断含完整尾字符」)。
	if !strings.Contains(line, strings.Repeat("好", 66)) || strings.Contains(line, strings.Repeat("好", 67)) {
		t.Fatalf("truncation boundary wrong: %q", line)
	}
	stop()
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
