// Package eventbus 承载 mulix 的事件骨干(P4a 010):内嵌 nats-server
// (JetStream,FileStorage)提供 append-only 事件持久流;进程内 InProcess
// 连接零网络跃迁;五分发语义层见后续任务。
// 依赖红线修订:NATS 为经批准的首个第三方运行时依赖(用户裁定 2026-10-11)。
package eventbus

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Bus 是内嵌 NATS 事件骨干的句柄:server + 进程内连接 + JetStream 上下文。
type Bus struct {
	srv            *server.Server
	nc             *nats.Conn
	js             nats.JetStreamContext
	mu             sync.RWMutex
	declared       map[string]bool
	handlers       map[string][]func([]byte)
	wfHandlers     map[string][]WaterfallHandler
	serialHandlers map[string][]func([]byte) error
	bailHandlers   map[string][]func(any) any
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
	return &Bus{srv: srv, nc: nc, js: js, declared: map[string]bool{}, handlers: map[string][]func([]byte){}, wfHandlers: map[string][]WaterfallHandler{}, serialHandlers: map[string][]func([]byte) error{}, bailHandlers: map[string][]func(any) any{}}, nil
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

// DeclareEvents 幂等声明事件名:为 subject mulix.<name> 确保 JetStream Stream
// (Storage=File,Retention=Limits——append-only 持久流);未声明名经
// declared 校验拒绝(fail-closed,FR-101 类型化)。
// Stream 布局 Ruling:每事件一 Stream(过渡形态;每会话一 Stream 属 P4b)。
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

func streamName(event string) string {
	// NATS Stream 名不允许点号:事件名的点净化为下划线(subject 保留原形)。
	return "MULIX_" + strings.ReplaceAll(event, ".", "_")
}

// validateDeclared 校验 subject 已声明(fail-closed)。
func (b *Bus) validateDeclared(subject string) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.declared[subject] {
		return fmt.Errorf("eventbus: subject %q not declared (fail-closed)", subject)
	}
	return nil
}

// Publish 经 JetStream 发布(持久化)+ 进程内单轨投递(handlers 表);
// 返回首个错误(全量聚合经 Emit)。裁定:持久化与投递同源于单轨,
// 双通知/双轨问题 009 评审裁决排除。
func (b *Bus) Publish(name string, payload []byte) error {
	if errs := b.Emit(name, payload); len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Emit 与 Publish 同源(持久化+单轨投递),返回全量错误聚合。FR-101。
func (b *Bus) Emit(name string, payload []byte) (errs []error) {
	subject := "mulix." + name
	if err := b.validateDeclared(subject); err != nil {
		return []error{err}
	}
	if _, err := b.js.Publish(subject, payload); err != nil {
		errs = append(errs, fmt.Errorf("eventbus: publish %q: %w", name, err))
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

// safeCall 隔离订阅 panic(不因单订阅 panic 中断通知序)。
func safeCall(fn func([]byte), payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("eventbus: subscriber panic: %v", r)
		}
	}()
	fn(payload)
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

// sameFunc 以闭包代码指针比较(同字面量注册/注销配对)。
func sameFunc(a, b any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// WaterfallNext 是瀑布链的续传函数(不调用即短路)。
type WaterfallNext func(arg any) any

// WaterfallHandler 是 waterfall 订阅签名(环绕中间件)。
type WaterfallHandler func(arg any, next WaterfallNext) any

// SubscribeWaterfall 登记 waterfall 监听器(可逆;unsub 成对移除首匹配)。
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
			if sameFunc(h, fn) {
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
	chain := next
	for i := len(hs) - 1; i >= 0; i-- {
		h := hs[i]
		n := chain
		chain = func(a any) any { return h(a, n) }
	}
	return chain(arg)
}

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

// SubscribeSerial 登记 serial 订阅(可逆;unsub 成对移除首匹配)。
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

// Bail 按注册序执行,首个非 nil 结果停并返回 (结果, true);全 nil → (nil, false)。FR-101。
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

// SubscribeBail 登记 bail 订阅(可逆;unsub 成对移除首匹配)。
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
