(FR-102 集成测试——订阅经 ctx.Effect 登记、Dispose 自动退订——移至 **Task 10**(Effect 于该任务落地;此处仅保留 Subscribe 的 effect-ready 注释)。)
# P4a 内核升级 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go 复刻 cordis/dsh 内核语义(忠实还原,不自简化):内嵌 NATS 事件骨干(JetStream 持久化)+ 五分发语义 + effect 可逆订阅 + Service 即插件 + inject 拓扑解析 + Mount 回滚补偿 + 红线修订落档。

**Architecture:**(architectural,设计文档已批准:`.mulix/.runtime/010-p4a-inject-effect/specs/2026-10-11-kernel-upgrade-design.md`)双层:持久骨干=内嵌 `nats-server`(JetStream,FileStorage,InProcessServer 连接)+ 分发语义层=`internal/eventbus`(五模式进程内投递 + 声明表 + effect 订阅)。kernel 仅定义 `EventPublisher`/`EventSubscriber` 小接口(零 NATS 依赖);`internal/eventbus` 承载 NATS(经批准的首个第三方依赖)。镜像 events.jsonl 仍为 1:1 审计面;发布顺序先镜像后总线(P4b 接线)。

**Tech Stack:** Go 标准库 + NATS(nats-server/v2 内嵌、nats.go 客户端——经批准的唯一第三方例外)。

**Spec:** `docs/specs/010-p4a-inject-effect/spec.md`

**Design:** `.mulix/.runtime/010-p4a-inject-effect/specs/2026-10-11-kernel-upgrade-design.md`

## Global Constraints

- FR-105:既有 kernel API(Context/Provide/Service/Mount/Dispose)零破坏;既有 subagent 消费方零回归。
- FR-107:依赖=NATS 例外经批准;kernel 本体零第三方(小接口消费);分层矩阵登记 `internal/eventbus`。
- FR-108:总线 JetStream 持久化;镜像仍为 1:1 审计面;本变更不建镜像流→总线桥(P4b)。
- FR-109:新机制单测矩阵覆盖;每任务 `go test ./... -count=1` 全绿再 commit。
- 测试基建约定:临时总线一律 `t.TempDir()` 作 JetStream FileStorage 目录,用后即弃。

## Review Focus

- NATS 内嵌启停:InProcessServer 连接、JetStream 启用、Stream 配置(FileStorage/Limits retention)——启停无泄漏(端口不监听 TCP:仅内嵌连接)。
- 五分发语义逐一对齐 §2.2 表:waterfall 短路、bail 首非 nil 停、parallel panic 聚合、serial 首错停。
- 声明表 fail-closed:未声明 subject 的 Publish/Subscribe 拒绝。
- effect 订阅:Dispose 自动退订(Drain);unsub 手动撤销成对。
- inject:拓扑序乱序重排、缺键含键名、环含路径、重复 ServiceKey 报错;普通 Plugin 兼容。
- Mount 回滚补偿:effect undo 逆序执行(替换静默截断);既有 subagent 用法零破坏。
- 红线落档:全仓 grep 无旧禁令残留。

---

## Task 1: eventbus 包骨架——内嵌 NATS 启停 + JetStream

**Files:**
- Create: `internal/eventbus/eventbus.go`
- Modify: `go.mod`(go get nats)、`internal/lint/rules.go`(矩阵登记 `internal/eventbus`)
- Test: `internal/eventbus/eventbus_test.go`

**Interfaces:**
- Consumes: NATS 库(nats-server/v2 server 包、nats.go 客户端)。
- Produces: `type Bus struct{}` + `type BusOptions struct{ StoreDir string }` + `OpenBus(opts BusOptions) (*Bus, error)`(启动内嵌 server+JetStream,InProcess 连接)+ `(*Bus) Close() error`(Drain 收口)+ `(*Bus) Conn() *nats.Conn` + `(*Bus) JS() nats.JetStreamContext`(后续任务消费)。

- [ ] **Step 1: go get + 矩阵登记**

```bash
go get github.com/nats-io/nats-server/v2@latest github.com/nats-io/nats.go@latest
```

`internal/lint/rules.go` layerMatrix 增:`"internal/eventbus": {"internal/kernel"}`(Task 10 消费 kernel 接口;NATS 为外部依赖不在矩阵管辖)。

- [ ] **Step 2: 写失败测试(TDD 形态:新包——RED=包未存在的编译期失败,台账记 Ruling)**

```go
package eventbus

import (
	"path/filepath"
	"testing"
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
```

- [ ] **Step 3: 实现**

`internal/eventbus/eventbus.go`:

```go
// Package eventbus 承载 mulix 的事件骨干(T8-②/009 后续,P4a 010):
// 内嵌 nats-server(JetStream,FileStorage)提供 append-only 事件持久流;
// 进程内 InProcess 连接零网络跃迁;五分发语义层见后续任务。
// 依赖红线修订:NATS 为经批准的首个第三方运行时依赖(用户裁定 2026-10-11)。
package eventbus

import (
	"fmt"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Bus 是内嵌 NATS 事件骨干的句柄:server + 进程内连接 + JetStream 上下文。
type Bus struct {
	srv *server.Server
	nc  *nats.Conn
	js  nats.JetStreamContext
}

// BusOptions 是 OpenBus 的选项:StoreDir 为 JetStream FileStorage 目录。
type BusOptions struct {
	StoreDir string
}

// OpenBus 启动内嵌 server(不监听 TCP——仅 InProcess 连接)、启用 JetStream、
// 建立进程内连接。
func OpenBus(opts BusOptions) (*Bus, error) {
	srv, err := server.NewServer(&server.Options{
		JetStream: true,
		StoreDir:  opts.StoreDir,
		NoLog:     true,
		Port:      -1, // 不监听 TCP(nats 默认 4222——内嵌形态仅 InProcess 连接)
	})
	if err != nil {
		return nil, fmt.Errorf("eventbus: new server: %w", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * 1e9) {
		return nil, fmt.Errorf("eventbus: server not ready within 10s")
	}
	nc, err := nats.Connect(srv.ClientURL(), nats.InProcessServer(srv))
	if err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("eventbus: connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		srv.Shutdown()
		return nil, fmt.Errorf("eventbus: jetstream: %w", err)
	}
	return &Bus{srv: srv, nc: nc, js: js}, nil
}

// Conn 返回进程内连接(后续任务:发布与订阅)。
func (b *Bus) Conn() *nats.Conn { return b.nc }

// JS 返回 JetStream 上下文(后续任务:Stream 管理)。
func (b *Bus) JS() nats.JetStreamContext { return b.js }

// Close 收口:关闭连接与内嵌 server(Drain 为异步——直接 Close 避免
// Shutdown 截断 Drain;持久化由 JetStream 写路径保证,发布调用已同步返回)。
func (b *Bus) Close() error {
	if b.nc != nil {
		b.nc.Close()
	}
	if b.srv != nil {
		b.srv.Shutdown()
	}
	return nil
}
```

- [ ] **Step 4: 跑测试验证通过**

Run: `go test ./internal/eventbus/ -count=1 -v`
Expected: PASS

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净(lint 矩阵登记后自食其力绿)

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/eventbus/ internal/lint/rules.go
git commit -m "feat(eventbus): 内嵌 NATS 骨架——JetStream 启停+矩阵登记(010 FR-107/108;P4a T1)"
```

---

## Task 2: 声明表(fail-closed subject 校验)

**Files:**
- Modify: `internal/eventbus/eventbus.go`
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Consumes: Task 1 的 `Bus`/`JS()`。
- Produces: `(*Bus) DeclareEvents(names ...string) error`(幂等;为每名确保 subject `mulix.<name>` 的 Stream 存在)+ `(*Bus) declared(name) bool`(内部)。

- [ ] **Step 1: 写失败测试**

```go
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
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestDeclareEventsAndFailClosed -count=1 -v`
Expected: FAIL——validateDeclared 未定义(编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`eventbus.go` 增(Bus 增 `declared map[string]bool` 字段与互斥保护):

```go
// DeclareEvents 幂等声明事件名:为 subject mulix.<name> 确保 JetStream Stream
// (Storage=File,Retention=Limits——append-only 持久流);未声明名经
// declared 校验拒绝(fail-closed,FR-101 类型化)。
func (b *Bus) DeclareEvents(names ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, name := range names {
		subject := "mulix." + name
		if b.declared[subject] {
			continue
		}
		stream := streamName(name)
		if _, err := b.js.StreamInfo(stream); err == nil {
			b.declared[subject] = true
			continue
		}
		if _, err := b.js.AddStream(&nats.StreamConfig{
			Name:      stream,
			Subjects:  []string{subject},
			Storage:   nats.FileStorage,
			Retention: nats.LimitsPolicy,
		}); err != nil {
			return fmt.Errorf("eventbus: declare %q: %w", name, err)
		}
		b.declared[subject] = true
	}
	return nil
}

func streamName(event string) string { return "MULIX_" + event }

// validateDeclared 校验 subject 已声明(fail-closed)。
func (b *Bus) validateDeclared(subject string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.declared[subject] {
		return fmt.Errorf("eventbus: subject %q not declared (fail-closed)", subject)
	}
	return nil
}
```

并让本任务测试的拒绝断言经 `validateDeclared`(Subscribe/Publish 语义层在 T3-T4 接入;本任务先提供校验原语与幂等声明)。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): 事件声明表——JetStream Stream 幂等+fail-closed 校验(010 FR-101;P4a T2)"
```

(Stream 布局 Ruling:本任务采用**每事件一 Stream(MULIX_<event>)**;设计 §2.4 的「每会话一 Stream(MULIX_<sessionID>)」为 P4b 目标形态——过渡期差异随 P4b 订阅桥统一,台账记录。)

---

## Task 3: Publish(Emit 持久化面)+ Subscribe(effect 可逆订阅)

**Files:**
- Modify: `internal/eventbus/eventbus.go`
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Consumes: Task 1/2。
- Produces: `(*Bus) Publish(name string, payload []byte) error`(JetStream 发布,持久化)+ `(*Bus) Subscribe(name string, fn func(payload []byte)) (unsub func(), err error)`(进程内投递;unsub=Drain 退订)。

- [ ] **Step 1: 写失败测试**

```go
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
	// 持久化:同 StoreDir 重开后 replay 到(新订阅从 Stream 起点收历史)。
	// replay 断言属 P5;此处仅核 JetStream 流存在。
	if _, err := b.JS().StreamInfo(streamName("echo")); err != nil {
		t.Fatalf("stream persisted: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestPublishSubscribeRoundTrip -count=1`
Expected: FAIL(Publish/Subscribe 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// Publish 经 JetStream 发布(payload 持久化到 Stream);fail-closed 校验声明。
func (b *Bus) Publish(name string, payload []byte) error {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return err
	}
	if _, err := b.js.Publish(subject, payload); err != nil {
		return fmt.Errorf("eventbus: publish %q: %w", name, err)
	}
	return nil
}

// Subscribe 建立进程内投递订阅(登记 handlers 表——进程内投递唯一路径,
// 双轨重复投递问题 009 评审已裁决单轨);返回 unsub(成对移除)。
// JetStream 持久由 Publish 侧承载(发布即持久);replay 订阅属 P5。
// FR-102 集成:订阅可经调用方 ctx.Effect 登记(unsub 为 undo)→ Dispose
// 自动退订(集成测试钉于 Task 10 Step 3b——Effect 于该任务落地)。
func (b *Bus) Subscribe(name string, fn func(payload []byte)) (func(), error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.handlers[subject] = append(b.handlers[subject], fn)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		hs := b.handlers[subject]
		for i, h := range hs {
			if sameFunc(h, fn) {
				b.handlers[subject] = append(hs[:i], hs[i+1:]...)
				break
			}
		}
	}, nil
}
```
- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): Publish/Subscribe——JetStream 持久+单轨投递+effect 集成(010 FR-108/FR-102;P4a T3)"
```

---

## Task 4: Emit 语义(聚合错误)

**Files:**
- Modify: `internal/eventbus/eventbus.go`
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Consumes: Task 3。
- Produces: `(*Bus) Emit(name string, payload []byte) []error`——持久发布 + 全进程内订阅按序通知(忽略订阅返回),错误聚合(发布错 + 各订阅错)。

- [ ] **Step 1: 写失败测试**

```go
// TestEmitAggregatesErrors 钉 FR-101 Emit 语义:持久发布 + 全订阅按序通知,
// 错误聚合(发布错与订阅错并列返回)。
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
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestEmitAggregatesErrors -count=1`
Expected: FAIL(Emit 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// Emit:持久发布 + 进程内全订阅按序通知(忽略订阅方 panic 与返回);
// 错误聚合返回(声明校验错 + 发布错)。FR-101。
func (b *Bus) Emit(name string, payload []byte) (errs []error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return []error{err}
	}
	if err := b.Publish(name, payload); err != nil {
		errs = append(errs, err)
	}
	b.mu.RLock()
	handlers := append([]func([]byte){}, b.handlers[subject]...)
	b.mu.RUnlock()
	for _, fn := range handlers {
		if err := safeCall(fn, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// safeCall 隔离订阅 panic(Emit 语义:不因单订阅 panic 中断通知序)。
func safeCall(fn func([]byte), payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("eventbus: subscriber panic: %v", r)
		}
	}()
	fn(payload)
	return nil
}
```

(注:进程内投递**单轨**——handlers 表为唯一投递路径(009 评审裁决:双轨会重复投递且 order 断言竞态);NATS 侧仅 Publish 持久化,不建 core 订阅。Bus 增 `handlers map[string][]func([]byte)` 字段。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): Emit 语义——聚合错误+订阅序(010 FR-101;P4a T4)"
```

---

## Task 5: Waterfall(链式环绕 + 短路)

**Files:**
- Modify: `internal/eventbus/eventbus.go`
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Consumes: Task 4(handlers 表)。
- Produces: `type WaterfallNext func(arg any) any` + `(*Bus) Waterfall(name string, arg any, next WaterfallNext) any`——监听器签名 `func(arg any, next WaterfallNext) any`;不调 next 即短路。

- [ ] **Step 1: 写失败测试**

```go
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
	// 全链穿透:A+1 → B+10 → next 直通(×2)。
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
	// 短路:A 不调 next → 其返回值即最终值(B 不执行)。
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
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestWaterfallChainAndShortCircuit -count=1`
Expected: FAIL(SubscribeWaterfall/Waterfall 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// WaterfallNext 是瀑布链的续传函数(不调用即短路)。
type WaterfallNext func(arg any) any

// WaterfallHandler 是 waterfall 订阅签名。
type WaterfallHandler func(arg any, next WaterfallNext) any

// SubscribeWaterfall 登记 waterfall 监听器(可逆;unsub 成对)。
func (b *Bus) SubscribeWaterfall(name string, fn WaterfallHandler) (func(), error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.wfHandlers[subject] = append(b.wfHandlers[subject], fn)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		hs := b.wfHandlers[subject]
		for i, h := range hs {
			if sameFunc(h, fn) { // 见注:按序移除首个匹配
				b.wfHandlers[subject] = append(hs[:i], hs[i+1:]...)
				break
			}
		}
	}, nil
}

// Waterfall 链式环绕:注册序 = 外层到内层;不调 next 即短路(其返回值为
// 最终值);全链穿透则执行调用方 next。FR-101。
func (b *Bus) Waterfall(name string, arg any, next WaterfallNext) any {
	subject := "mulix." + name
	b.mu.RLock()
	hs := append([]WaterfallHandler{}, b.wfHandlers[subject]...)
	b.mu.RUnlock()
	// 从最内层向外构建:chain_i = h_i(chain_{i+1})
	chain := next
	for i := len(hs) - 1; i >= 0; i-- {
		h := hs[i]
		n := chain
		chain = func(a any) any { return h(a, n) }
	}
	return chain(arg)
}
```

(注:`sameFunc` 用 reflect.ValueOf(f).Pointer() 比较(闭包指针相等性); SubscribeWaterfall 与 Waterfall 的监听器表独立于 handlers(签名不同)。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): Waterfall 链式环绕+短路(010 FR-101;P4a T5)"
```

---

## Task 6: Parallel(并发 + panic 聚合)

**Files:**
- Modify: `internal/eventbus/eventbus.go`
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Consumes: Task 4 handlers 表。
- Produces: `(*Bus) Parallel(name string, payload []byte) []error`——每订阅一个 goroutine 并发全跑;单订阅 panic recover 记为该订阅错误;等待全部完成。

- [ ] **Step 1: 写失败测试**

```go
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
	unsub1, _ := b.Subscribe("par", func([]byte) { <-done; close(slowRan) }) // 慢订阅:等主流程放行
	defer close(done)
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
	_ = unsub1
	_ = unsub2
	_ = unsub3
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestParallelRunsAllAndAggregates -count=1`
Expected: FAIL(Parallel 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// Parallel 每订阅一个 goroutine 并发全跑;单订阅 panic recover 记为该订阅
// 错误;阻塞至全部完成。FR-101。
func (b *Bus) Parallel(name string, payload []byte) (errs []error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return []error{err}
	}
	b.mu.RLock()
	handlers := append([]func([]byte){}, b.handlers[subject]...)
	b.mu.RUnlock()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, fn := range handlers {
		wg.Add(1)
		go func(fn func([]byte)) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("eventbus: subscriber panic: %v", r))
					mu.Unlock()
				}
			}()
			if err := safeCall(fn, payload); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(fn)
	}
	wg.Wait()
	return errs
}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): Parallel 并发全跑+panic 聚合(010 FR-101;P4a T6)"
```

---

## Task 7: Serial 与 Bail

**Files:**
- Modify: `internal/eventbus/eventbus.go`
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Consumes: Task 4 handlers 表(serial);Task 5 表(bail——独立签名表 `bailHandlers map[string][]func(any) any`)。
- Produces: `(*Bus) Serial(name string, payload []byte) error`(按注册序,首错停)+ `(*Bus) Bail(name string, payload any) (any, bool)`(按序,首个非 nil 结果停)。

- [ ] **Step 1: 写失败测试**

```go
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
→ (nil,false)。
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
```

(注:Subscribe 语义扩展——serial/bail 各需独立订阅签名与表:`SubscribeSerial(name, func([]byte) error)`、`SubscribeBail(name, func(any) any)`;三张表并行,模式间互不串扰。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run 'TestSerial|TestBail' -count=1`
Expected: FAIL(SubscribeSerial/Serial/SubscribeBail/Bail 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// Serial 按注册序执行,首个非 nil error 停止并返回。FR-101。
func (b *Bus) Serial(name string, payload []byte) error {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return err
	}
	b.mu.RLock()
	handlers := append([]func([]byte) error{}, b.serialHandlers[subject]...)
	b.mu.RUnlock()
	for _, fn := range handlers {
		if err := fn(payload); err != nil {
			return err
		}
	}
	return nil
}

// SubscribeSerial 登记 serial 订阅(可逆;unsub 成对)。
func (b *Bus) SubscribeSerial(name string, fn func([]byte) error) (func(), error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.serialHandlers[subject] = append(b.serialHandlers[subject], fn)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		hs := b.serialHandlers[subject]
		for i, h := range hs {
			if sameFunc(h, fn) {
				b.serialHandlers[subject] = append(hs[:i], hs[i+1:]...)
				break
			}
		}
	}, nil
}

// Bail 按注册序执行,首个非 nil 结果停并返回 (结果, true)。FR-101。
func (b *Bus) Bail(name string, payload any) (any, bool) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return nil, false
	}
	b.mu.RLock()
	handlers := append([]func(any) any{}, b.bailHandlers[subject]...)
	b.mu.RUnlock()
	for _, fn := range handlers {
		if v := fn(payload); v != nil {
			return v, true
		}
	}
	return nil, false
}

// SubscribeBail 登记 bail 订阅(可逆;unsub 成对)。
func (b *Bus) SubscribeBail(name string, fn func(any) any) (func(), error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.bailHandlers[subject] = append(b.bailHandlers[subject], fn)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		hs := b.bailHandlers[subject]
		for i, h := range hs {
			if sameFunc(h, fn) {
				b.bailHandlers[subject] = append(hs[:i], hs[i+1:]...)
				break
			}
		}
	}, nil
}
```

(Bus 增 `serialHandlers map[string][]func([]byte) error` 与 `bailHandlers map[string][]func(any) any` 字段;`sameFunc` 用 reflect.ValueOf(f).Pointer() 比较(闭包指针相等性)。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): Serial 首错停+Bail 首非nil停(010 FR-101;P4a T7)"
```

---

## Task 8: Service 即插件(NewService/ServiceKey)

**Files:**
- Modify: `internal/kernel/kernel.go`(Service 接口 + NewService 语法糖)
- Test: `internal/kernel/kernel_test.go`(追加)

**Interfaces:**
- Consumes: 既有 Plugin/Context(008 前 API 零破坏)。
- Produces: `type Service interface { Plugin; ServiceKey() string }` + `NewService(key string, deps []string, name string, apply func(c *Context, deps map[string]any) (any, error)) Plugin`(Mount 时注入 deps → apply 产出值 → 自动 Provide key);重复 ServiceKey 校验属 Task 9 解析器。

- [ ] **Step 1: 写失败测试**

```go
// TestServiceProvidesKeyedValue 钉 009 FR-102 前置(Service 即插件):
// NewService 插件 Mount 后,其键可经 Service 查得,值即 apply 产出。
func TestServiceProvidesKeyedValue(t *testing.T) {
	c := NewContext(nil)
	svc := NewService("llm", nil, "llm-adapter", func(c *Context, deps map[string]any) (any, error) {
		return "claude-cli-adapter", nil
	})
	if err := c.Mount(svc); err != nil {
		t.Fatalf("mount: %v", err)
	}
	v, ok := c.Service("llm")
	if !ok || v != "claude-cli-adapter" {
		t.Fatalf("service = (%v,%v), want (claude-cli-adapter,true)", v, ok)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/kernel/ -run TestServiceProvidesKeyedValue -count=1`
Expected: FAIL(NewService 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/kernel/kernel.go` 追加:

```go
// Service 是「提供服务的插件」:键即 ctx.<key>;Mount 完成即服务就绪
// (cordis 忠实模型——服务本身就是插件;键即契约,消费方按 key 断言类型)。
type Service interface {
	Plugin
	ServiceKey() string
}

// NewService 是 Service 的语法糖实现:注入 deps 中的服务值,apply 产出服务
// 值并自动 Provide(key 即 ServiceKey)。deps 缺失时 Mount 返回错误
// (指名缺键;与 MountAll 拓扑解析配合,乱序提交由解析器重排)。
func NewService(key string, deps []string, name string,
	apply func(c *Context, deps map[string]any) (any, error)) Plugin {
	return &servicePlugin{name: name, key: key, deps: deps, apply: apply}
}

type servicePlugin struct {
	name  string
	key   string
	deps  []string
	apply func(c *Context, deps map[string]any) (any, error)
}

func (p *servicePlugin) Name() string       { return p.name }
func (p *servicePlugin) ServiceKey() string { return p.key }
func (p *servicePlugin) Inject() []string   { return p.deps }

func (p *servicePlugin) Mount(c *Context) error {
	deps := map[string]any{}
	for _, k := range p.deps {
		v, ok := c.Service(k)
		if !ok {
			return fmt.Errorf("kernel: service %q missing dep %q", p.key, k)
		}
		deps[k] = v
	}
	v, err := p.apply(c, deps)
	if err != nil {
		return err
	}
	return c.Provide(p.key, v)
}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/kernel/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/
git commit -m "feat(kernel): Service 即插件——NewService 语法糖(010 FR-102 前置;P4a T8)"
```

---

## Task 9: inject 拓扑解析器(MountAll)

**Files:**
- Modify: `internal/kernel/kernel.go`
- Test: `internal/kernel/kernel_test.go`(追加)

**Interfaces:**
- Consumes: Task 8 的 Service/Inject。
- Produces: `func (c *Context) MountAll(plugins ...Plugin) error`——拓扑序解析:普通 Plugin(无 Inject/ServiceKey)按声明序最先挂载;Service 按 Inject 键建边(提供键=ServiceKey),Kahn 序;缺键拒挂(含键名);环/自环报错含路径;重复 ServiceKey 报错。

- [ ] **Step 1: 写失败测试**

```go
// TestMountAllTopologicalOrder 钉 009 FR-103:乱序提交自动重排(消费先于
// 提供出现亦可);普通插件声明序最先。
func TestMountAllTopologicalOrder(t *testing.T) {
	c := NewContext(nil)
	order := []string{}
	plain := plainPlugin("plain", func() { order = append(order, "plain") })
	llm := NewService("llm", nil, "llm", func(c *Context, _ map[string]any) (any, error) {
		order = append(order, "llm")
		return "adapter", nil
	})
	agent := NewService("agents", []string{"llm"}, "agents", func(c *Context, deps map[string]any) (any, error) {
		order = append(order, "agents")
		return deps["llm"], nil
	})
	if err := c.MountAll(agent, plain, llm); err != nil { // 乱序:agents 在 llm 前
		t.Fatalf("mount all: %v", err)
	}
	if len(order) != 3 || order[0] != "plain" || order[1] != "llm" || order[2] != "agents" {
		t.Fatalf("order = %v, want [plain llm agents]", order)
	}
}

// TestMountAllMissingKeyAndCycle 钉 009 FR-103:缺键拒挂含键名;环报错含路径。
func TestMountAllMissingKeyAndCycle(t *testing.T) {
	c := NewContext(nil)
	missing := NewService("x", []string{"not-provided"}, "x", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	if err := c.MountAll(missing); err == nil || !strings.Contains(err.Error(), "not-provided") {
		t.Fatalf("missing key err = %v, want mention not-provided", err)
	}
	a := NewService("a", []string{"b"}, "a", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	bCycle := NewService("b", []string{"a"}, "b", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	if err := c.MountAll(a, bCycle); err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("cycle err = %v, want cycle path mentioning a", err)
	}
	// 自环(Edge Case 裁定):Service inject 自身键 → 循环依赖报错。
	self := NewService("self", []string{"self"}, "self", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	if err := c.MountAll(self); err == nil || !strings.Contains(err.Error(), "self -> self") {
		t.Fatalf("self-loop err = %v, want self -> self", err)
	}
}
```

(plainPlugin 为既有测试助手或本测试新加的最小 Plugin 实现:name + Mount 记序。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/kernel/ -run TestMountAll -count=1`
Expected: FAIL(MountAll 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// MountAll 批量挂载:普通 Plugin(未实现 Inject)按声明序最先;Service 按
// Inject 键拓扑排序(Kahn;提供键=ServiceKey);缺键/环/重复键 fail-closed。
// 单个 Mount 保持既有语义(无解析——调用方自理顺序)。FR-103。
func (c *Context) MountAll(plugins ...Plugin) error {
	var plain []Plugin
	var svcs []Service
	provided := map[string]Service{} // ServiceKey -> Service(重复键检测)
	for _, p := range plugins {
		svc, ok := p.(Service)
		if !ok {
			plain = append(plain, p)
			continue
		}
		if _, dup := provided[svc.ServiceKey()]; dup {
			return fmt.Errorf("kernel: duplicate ServiceKey %q", svc.ServiceKey())
		}
		provided[svc.ServiceKey()] = svc
		svcs = append(svcs, svc)
	}
	for _, p := range plain { // 普通插件声明序最先挂载
		if err := c.Mount(p); err != nil {
			return err
		}
	}
	// Kahn:入边 = Service.Inject 中被其他 Service 提供的键;已就绪键(作用域
	// 已有服务)视为无入边;缺键(无人提供且不在作用域)→ 拒挂。
	existing := func(key string) bool {
		_, ok := c.Service(key)
		return ok
	}
	degree := map[string]int{}
	dependents := map[string][]Service{}
	for _, s := range svcs {
		for _, k := range s.Inject() {
			if k == s.ServiceKey() {
				return fmt.Errorf("kernel: dependency cycle (self-loop): %s -> %s", s.ServiceKey(), s.ServiceKey())
			}
			if _, ok := provided[k]; ok {
				degree[s.ServiceKey()]++
				dependents[k] = append(dependents[k], s)
			} else if !existing(k) {
				return fmt.Errorf("kernel: mount %q missing dep %q", s.ServiceKey(), k)
			}
		}
	}
	var queue []Service
	for _, s := range svcs {
		if degree[s.ServiceKey()] == 0 {
			queue = append(queue, s)
		}
	}
	mounted := 0
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if err := c.Mount(s); err != nil {
			return err
		}
		mounted++
		for _, dep := range dependents[s.ServiceKey()] {
			degree[dep.ServiceKey()]--
			if degree[dep.ServiceKey()] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	if mounted != len(svcs) { // 剩余即环
		var names []string
		for _, s := range svcs {
			if degree[s.ServiceKey()] > 0 {
				names = append(names, s.ServiceKey())
			}
		}
		return fmt.Errorf("kernel: dependency cycle among %v", names)
	}
	return nil
}
```

(注:环路径含环内键名清单;精确路径形态 `a -> b -> a` 的美化属实现细节,断言只要求含键名。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/kernel/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/
git commit -m "feat(kernel): MountAll 拓扑解析——缺键/环/重复键 fail-closed(010 FR-103;P4a T9)"
```

---

## Task 10: Effect 可逆注册 + Mount 回滚补偿细化

**Files:**
- Modify: `internal/kernel/kernel.go`(`Effect` 方法 + `Mount` 回滚段)
- Test: `internal/kernel/kernel_test.go`(追加)

**Interfaces:**
- Consumes: 既有 disposables LIFO 机制。
- Produces: `func (c *Context) Effect(fn func() (undo func(), err error)) error`(fn 成功 → undo 入 LIFO;fn 错误 → 返回错误);`Mount` 回滚细化(本次挂载期已执行 effect 的 undo **逆序补偿执行**,替换静默截断;服务删除照旧)。

- [ ] **Step 1: 写失败测试**

```go
// TestEffectRollbackCompensation 钉 009 FR-104:第二个 effect 的 fn 返回错误
// → Mount 失败,第一个 effect 的 undo 已逆序补偿执行(非静默截断)。
func TestEffectRollbackCompensation(t *testing.T) {
	c := NewContext(nil)
	var compensations []string
	p := plainPlugin("with-effects", func() {})
	// 带 effect 的插件:Mount 内登记两个 effect,第二个返回错误。
	ef := effectfulPlugin("ef", c, []func() (func(), error){
		func() (func(), error) {
			return func() { compensations = append(compensations, "undo-1") }, nil
		},
		func() (func(), error) {
			return nil, fmt.Errorf("effect-2 failed")
		},
	})
	if err := c.MountAll(p, ef); err == nil {
		t.Fatal("mount must fail on effect error")
	}
	if len(compensations) != 1 || compensations[0] != "undo-1" {
		t.Fatalf("compensations = %v, want [undo-1] (逆序补偿)", compensations)
	}
}

// TestEffectDisposeLIFO 钉 009 FR-104:成功路径下 effect undo 在 Dispose
// 时 LIFO 逆序执行。
func TestEffectDisposeLIFO(t *testing.T) {
	c := NewContext(nil)
	var order []string
	ef := effectfulPlugin("ef", c, []func() (func(), error){
		func() (func(), error) { return func() { order = append(order, "undo-1") }, nil },
		func() (func(), error) { return func() { order = append(order, "undo-2") }, nil },
	})
	if err := c.MountAll(ef); err != nil {
		t.Fatalf("mount: %v", err)
	}
	c.Dispose()
	if len(order) != 2 || order[0] != "undo-2" || order[1] != "undo-1" {
		t.Fatalf("order = %v, want [undo-2 undo-1] (LIFO)", order)
	}
}
```

(effectfulPlugin 为本测试新增的最小插件助手:Mount 内对目标 Context 逐个调用 c.Effect;plainPlugin 复用 Task 9 助手。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/kernel/ -run TestEffect -count=1`
Expected: FAIL(Effect 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

```go
// Effect 登记可逆注册:fn 成功执行后,undo 进入 disposables(LIFO——Dispose
// 逆序执行,FR-104);fn 返回错误 → 原样上抛(Mount 期由回滚补偿机制处理)。
func (c *Context) Effect(fn func() (undo func(), err error)) error {
	undo, err := fn()
	if err != nil {
		return err
	}
	if undo != nil {
		c.OnDispose(undo)
	}
	return nil
}
```

`Mount` 回滚段细化(替换 `c.disposables = c.disposables[:disposablesBefore]`):

```go
	if err := p.Mount(c); err != nil {
		c.mu.Lock()
		added := append([]func(){}, c.disposables[disposablesBefore:]...)
		c.disposables = c.disposables[:disposablesBefore]
		c.mu.Unlock()
		// 回滚补偿(009 FR-104 细化):本次挂载期间登记的清理(含 effect
		// undo)逆序执行——注册是可逆副作用,回滚必须跑清理而非丢弃。
		for i := len(added) - 1; i >= 0; i-- {
			added[i]()
		}
		c.mu.Lock()
		for name := range c.services {
			if _, was := existing[name]; !was {
				delete(c.services, name)
			}
		}
		c.mu.Unlock()
		return fmt.Errorf("kernel: mount plugin %q: %w", p.Name(), err)
	}
```

(注:先跑补偿再删服务——补偿函数可能读取挂载期状态;锁分段与既有「锁外执行清理」纪律一致。既有行为差异:回滚不再静默丢弃 disposables——009 spec FR-104 行为细化,subagent 不走 Mount 失败路径,零影响。)

- [ ] **Step 3b: FR-102 集成测试(订阅经 ctx.Effect 登记,Dispose 自动退订)**

追加到 `internal/kernel/kernel_test.go`(import mulix/internal/eventbus——测试文件不参与分层扫描,lint_test 先例):

```go
// TestSubscribeEffectDisposeIntegration 钉 FR-102:订阅经 ctx.Effect 登记
// (unsub 为 undo)——作用域 Dispose 后投递停止,其他订阅不受影响。
func TestSubscribeEffectDisposeIntegration(t *testing.T) {
	b, err := eventbus.OpenBus(eventbus.BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("scoped"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	c := NewContext(nil)
	hits := 0
	if err := c.Effect(func() (undo func(), err error) {
		unsub, err := b.Subscribe("scoped", func([]byte) { hits++ })
		return unsub, err
	}); err != nil {
		t.Fatalf("effect: %v", err)
	}
	otherHits := 0
	unsubOther, err := b.Subscribe("scoped", func([]byte) { otherHits++ })
	if err != nil {
		t.Fatalf("subscribe other: %v", err)
	}
	defer unsubOther()
	c.Dispose()
	_ = b.Publish("scoped", []byte("x"))
	time.Sleep(100 * time.Millisecond)
	if hits != 0 {
		t.Fatalf("disposed scope still received: %d", hits)
	}
	if otherHits == 0 {
		t.Fatal("other subscribers must be unaffected by scope disposal")
	}
}
```

- [ ] **Step 4: 既有断言修订(回滚补偿新事实)+ 全量门禁**

既有 `TestMountFailureRollsBack`(kernel_test.go:131-160)断言「rolled-back disposable
must not run」——回滚补偿新事实下该断言过时:修订为断言「补偿已执行」并在台账记 Ruling
(依据:010 FR-104 行为细化;此处修订,Task 12 仅复核)。

Run: `go test ./internal/kernel/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/
git commit -m "feat(kernel): Effect 可逆注册+Mount 回滚补偿+既有断言修订(010 FR-104;P4a T10)"
```

---

## Task 11: kernel 小接口接线(events 服务键)+ 红线修订落档

**Files:**
- Modify: `internal/kernel/kernel.go`(EventPublisher/EventSubscriber 小接口 + doc 重写)
- Test: `internal/kernel/kernel_test.go`(接口满足性)

**Interfaces:**
- Consumes: Task 1-7 的 eventbus(实现方)。
- Produces: `type EventPublisher interface { DeclareEvents(names ...string) error; Publish(name string, payload []byte) error }` + `type EventSubscriber interface { Subscribe(name string, fn func(payload []byte)) (func(), error) }`——kernel 经此消费总线(零 NATS 依赖);包 doc 红线修订落档;组合根 Provide "events" 服务的用法示例注释。

- [ ] **Step 1: 写失败测试(接口满足性)**

```go
// TestEventInterfacesSatisfied 钉 010 FR-107:kernel 小接口经 eventbus 实现
// 满足(kernel 零 NATS 依赖——接口在 kernel,实现在 eventbus;满足性测试放
// eventbus 测试文件,见对应断言)。
// 本测试钉 kernel 侧:接口存在且 Publish/Subscribe 契约形态正确。
func TestEventInterfacesContract(t *testing.T) {
	var _ EventPublisher = (EventPublisher)(nil)
	var _ EventSubscriber = (EventSubscriber)(nil)
}
```

(eventbus 侧承重断言:`var _ kernel.EventPublisher = (*Bus)(nil)` 与 `var _ kernel.EventSubscriber = (*Bus)(nil)` 于 Task 11 Step 3 加入 eventbus_test.go——本任务只钉 kernel 侧接口存在性。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/kernel/ -run TestEventInterfacesContract -count=1`
Expected: FAIL(EventPublisher/EventSubscriber 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

kernel.go 接口 + 包 doc 重写:

```go
// EventPublisher/EventSubscriber 是事件骨干的 kernel 侧小接口:kernel 及其
// 插件经此消费事件能力而不依赖 NATS(实现=internal/eventbus;组合根 Provide
// "events" 服务键)。FR-107。
type EventPublisher interface {
	DeclareEvents(names ...string) error
	Publish(name string, payload []byte) error
}

type EventSubscriber interface {
	Subscribe(name string, fn func(payload []byte)) (func(), error)
}
```

包 doc 红线修订(替换「不做事件发布」段,FR-106):

```go
// Package kernel 提供 Go 版 cordis 模式内核：层级 Context 作用域、服务注入
// (inject + Service 即插件)、插件挂载生命周期(MountAll 拓扑解析、Mount
// 回滚补偿)、effect 可逆注册与类型化事件总线小接口。
//
// 红线(修订来源:用户裁定 2026-10-11,变更 010——按 dsh 实现逻辑修订):
//   - leaf 级包——不 import 任何 internal 包;除经批准的 NATS 例外
//     (经 EventPublisher/EventSubscriber 小接口隔离,kernel 本体不 import NATS);
//   - 事件总线为内存订阅+JetStream 持久骨干(实现 internal/eventbus);
//     镜像 events.jsonl 是唯一持久审计真相源——持久化审计语义不经总线;
//   - 双层注册:插件注册 effect 可逆(本包 Effect);引擎可见清单回合开始
//     冻结快照(机制属 P4b);
//   - dispose 级联(先子后父)与 Mount 回滚补偿(已执行 effect undo 逆序)。
```

Run(落档验收): `grep -rn "不做事件发布\|不设第二通道" internal/ *.md | grep -v ".mulix/.runtime"`
Expected: 零命中(internal/ 与顶层 md 无旧禁令残留;docs/specs 与 docs/changes 中的引文=本变更自身的规范引文,属合法引用,排除在验收外)。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/kernel/ -count=1 -v && go test ./... -count=1`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/kernel.go internal/kernel/kernel_test.go
git commit -m "feat(kernel): EventPublisher/EventSubscriber 小接口+红线修订落档(010 FR-106/107;P4a T11)"
```

---

## Task 12: 全矩阵回归与 subagent 兼容核验

**Files:**
- Test: `internal/kernel/kernel_test.go`(既有断言复核)、`internal/subagent/`(既有测试零改动核验)
- (无新代码——收口核验任务)

**Interfaces:**
- Consumes: Task 1-11 全部产出。
- Produces: 全矩阵回归结论(写入 task 报告);FR-105 零破坏实证。

- [ ] **Step 1: kernel 既有断言复核**

Run: `go test ./internal/kernel/ -count=1 -v`
Expected: 既有测试全部 PASS(回滚补偿的断言修订已在 Task 10 完成;本步仅复核零回归)。

- [ ] **Step 2: subagent 兼容核验**

Run: `go test ./internal/subagent/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: subagent 全绿(其 kernel 用法=NewContext/OnDispose,零改动)+ 全量绿。

- [ ] **Step 3: 矩阵自食其力**

Run: `go run ./cmd/mulix lint layers`
Expected: 零违规(eventbus 已登记;kernel 仍 leaf;新增 import 不越层)。

- [ ] **Step 4: Commit(如有断言修订)**

```bash
git add internal/kernel/kernel_test.go internal/subagent/
git commit -m "test(kernel): 既有断言按回滚补偿新事实复核(010 FR-105;P4a T12)"
```
(若无改动则无 commit;任务完成以全矩阵回归绿为准。)
