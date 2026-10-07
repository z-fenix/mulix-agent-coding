package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

// FakeScript 是 FakeHost 的回放脚本。
//
//   - OnStart：Start 后立即回放的事件（fixture 的 pre 段——init/hooks）；
//     不含 init 时宿主合成一个最小 init 完成握手。
//   - Turns：每次 Send 按序回放一段（fixture 的 assistant..result 段）；
//     耗尽后 Send 返回错误（显式、可调试，不静默循环）。
type FakeScript struct {
	OnStart []Event
	Turns   [][]Event
}

// FakeControlResponse 是一次 RespondControl 的记录（e2e 断言用）。
type FakeControlResponse struct {
	ID      string
	Payload ControlResponse
}

// FakeHost 是 Host 的脚本化实现（design §7.5）：按 fixture 序列回放事件 +
// 可编程控制请求注入点。e2e 用「假引擎 × 真实 control/cache/sessionx 栈」。
// 并发模型：单发射协程串行化 OnStart/Turns/注入——**段内事件顺序严格保持；
// 跨源（Turns vs 注入）的先后不作承诺**（消费方按 Kind 处理，勿依赖）。
type FakeHost struct {
	script FakeScript

	events    chan Event
	injectCh  chan ControlRequest
	turnCh    chan []Event
	done      chan struct{}
	doneOnce  sync.Once
	emitter   chan struct{} // 发射协程结束信号
	startOnce sync.Once
	handshake chan struct{}

	mu         sync.Mutex
	sent       []UserMessage
	responses  []FakeControlResponse
	interrupts int
	turnIdx    int
	closed     bool
	started    bool
	startErr   error
	info       EngineInfo
}

var _ Host = (*FakeHost)(nil)

// NewFakeHost 构造假引擎（Start 即握手，无子进程）。
func NewFakeHost(script FakeScript) *FakeHost {
	return &FakeHost{
		script:    script,
		events:    make(chan Event, 128),
		injectCh:  make(chan ControlRequest, 8),
		turnCh:    make(chan []Event, 8),
		done:      make(chan struct{}),
		emitter:   make(chan struct{}),
		handshake: make(chan struct{}),
	}
}

// FixtureScript 从 fixture jsonl 构造回放脚本。分段规则：
//   - KindNoise（informational/status/thinking_tokens 等 decoder 容忍类）
//     不进入脚本（回放保持语义事件序列）；
//   - 首个回合段收段前的语义事件 → OnStart（fixture 的 init/hooks 段）；
//   - 其余按 KindResult 切段：段含 result；回合段之间的 init 重发/hooks
//     忠实保留在**下一回合段首**（时序与 fixture 一致；幂等消费是 control
//     的职责——Task 22 交接前提）；
//   - 尾段无 result 收口时按段保留（畸形 fixture 显式保留给回放）。
func FixtureScript(path string) (FakeScript, error) {
	f, err := os.Open(path)
	if err != nil {
		return FakeScript{}, fmt.Errorf("fake: open fixture: %w", err)
	}
	defer f.Close()
	var script FakeScript
	var cur []Event
	inTurn := false // 当前处于某回合段（assistant..result）内
	sc := newLargeScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := DecodeLine(line)
		if err != nil {
			return FakeScript{}, fmt.Errorf("fake: fixture %s: %w", path, err)
		}
		if ev == nil || ev.Kind == KindNoise {
			continue // 未知（skip 类）/噪音（容忍类）：回放不含
		}
		switch {
		case ev.Kind == KindAssistant:
			inTurn = true
			cur = append(cur, *ev)
		case inTurn:
			cur = append(cur, *ev)
			if ev.Kind == KindResult {
				script.Turns = append(script.Turns, cur)
				cur = nil
				inTurn = false
			}
		case len(script.Turns) == 0:
			// 首个回合段收段前：init/hooks 归 OnStart
			script.OnStart = append(script.OnStart, *ev)
		default:
			// 回合段之间（result 之后、下一 assistant 之前）：
			// init 重发/hooks 归下一回合段首
			cur = append(cur, *ev)
		}
	}
	if err := sc.Err(); err != nil {
		return FakeScript{}, fmt.Errorf("fake: fixture %s: %w", path, err)
	}
	if inTurn || len(cur) > 0 { // 尾段未收口——按段保留
		script.Turns = append(script.Turns, cur)
	}
	return script, nil
}

// Start 完成握手：回放 OnStart（无 init 时合成最小 init），Info 由首个 init 推导。
// Close 先于 Start 是使用错误——返回错误（不放行握手）。
func (f *FakeHost) Start(_ context.Context, _ BootConfig) error {
	f.startOnce.Do(func() {
		f.mu.Lock()
		if f.closed {
			// Close-before-Start：Close 的未启动路径已/将关闭 handshake
			//（放行本处等待）——这里绝不能再 close（重复关闭 panic）。
			f.startErr = errors.New("fake: host closed before start")
			f.mu.Unlock()
			return
		}
		f.started = true
		f.mu.Unlock()
		onStart := f.script.OnStart
		var info EngineInfo
		hasInit := false
		for _, ev := range onStart {
			if ev.Kind == KindInit && ev.Init != nil {
				hasInit = true
				info = EngineInfo{
					CLIVersion:  ev.Init.CLIVersion,
					SessionID:   ev.SessionID,
					Model:       ev.Init.Model,
					Fingerprint: "fake",
				}
				break
			}
		}
		if !hasInit {
			// 合成最小 init（手写脚本的最简形态；session/version 为 fake 常量）
			const fakeSession = "00000000-0000-4000-8000-0000000000fa"
			init := &InitInfo{
				CLIVersion:     "2.1.291-fake",
				Model:          "fake-model",
				PermissionMode: "default",
			}
			ev := Event{Kind: KindInit, SessionID: fakeSession, UUID: "fake-init", Init: init}
			onStart = append([]Event{ev}, onStart...)
			info = EngineInfo{CLIVersion: init.CLIVersion, SessionID: fakeSession, Model: init.Model, Fingerprint: "fake"}
		}
		f.mu.Lock()
		f.info = info // 写侧与 Info() 读侧同锁（评审 Minor-1）
		f.mu.Unlock()
		go f.emit(onStart)
	})
	<-f.handshake
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startErr
}

// emit 是唯一的事件发射协程：OnStart → 握手信号 → Turns/注入/关闭三路选择。
// defer 保证任何退出路径都放行握手（并发 Close 的兜底）。
func (f *FakeHost) emit(onStart []Event) {
	defer func() {
		select {
		case <-f.handshake:
		default:
			close(f.handshake)
		}
		close(f.emitter)
	}()
	for _, ev := range onStart {
		if !f.tryDeliver(ev) {
			return
		}
		if ev.Kind == KindInit {
			select {
			case <-f.handshake:
			default:
				close(f.handshake)
			}
		}
	}
	for {
		select {
		case <-f.done:
			return
		case cr := <-f.injectCh:
			ev := Event{Kind: KindControlRequest, SessionID: f.info.SessionID, Control: &cr}
			if !f.tryDeliver(ev) {
				return
			}
		case turn := <-f.turnCh:
			for _, ev := range turn {
				if !f.tryDeliver(ev) {
					return
				}
			}
		}
	}
}

// tryDeliver 投递事件（关闭后丢弃并返回 false——发射协程终止）。
func (f *FakeHost) tryDeliver(ev Event) bool {
	select {
	case f.events <- ev:
		return true
	case <-f.done:
		return false
	}
}

// Events 返回事件流。
func (f *FakeHost) Events() <-chan Event { return f.events }

// Send 记录输入并回放下一段 Turn（耗尽 → 错误；关闭 → 错误）。
func (f *FakeHost) Send(_ context.Context, msg UserMessage) error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return errors.New("fake: host closed")
	}
	if f.turnIdx >= len(f.script.Turns) {
		f.mu.Unlock()
		return fmt.Errorf("fake: script exhausted (%d turns)", len(f.script.Turns))
	}
	f.sent = append(f.sent, msg)
	f.turnIdx++
	f.mu.Unlock()
	select {
	case f.turnCh <- f.script.Turns[f.turnIdx-1]:
		return nil
	case <-f.done:
		return errors.New("fake: host closed")
	}
}

// InjectControlRequest 可编程注入一个控制请求事件（e2e 审批链路演练）。
func (f *FakeHost) InjectControlRequest(cr ControlRequest) error {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return errors.New("fake: host closed")
	}
	select {
	case f.injectCh <- cr:
		return nil
	case <-f.done:
		return errors.New("fake: host closed")
	}
}

// RespondControl 记录应答（不组 wire——FakeHost 无 stdin；断言用）。
func (f *FakeHost) RespondControl(_ context.Context, id string, payload ControlResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("fake: host closed")
	}
	f.responses = append(f.responses, FakeControlResponse{ID: id, Payload: payload})
	return nil
}

// Interrupt 计数（演练用）。
func (f *FakeHost) Interrupt(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("fake: host closed")
	}
	f.interrupts++
	return nil
}

// Close 停止发射并关闭事件通道（幂等；Start 未调用也安全——不等待发射协程）。
func (f *FakeHost) Close(_ context.Context) error {
	f.doneOnce.Do(func() {
		f.mu.Lock()
		f.closed = true
		started := f.started
		f.mu.Unlock()
		close(f.done)
		if started {
			<-f.emitter
		} else {
			select {
			case <-f.handshake:
			default:
				close(f.handshake) // 放行可能阻塞的 Start（startErr 已置）
			}
		}
		close(f.events)
	})
	return nil
}

// Info 返回握手后的引擎身份（Start 前零值）。
func (f *FakeHost) Info() EngineInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info
}

// Sent 返回已收输入的副本（断言用）。
func (f *FakeHost) Sent() []UserMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]UserMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

// Responses 返回控制应答记录的副本（断言用）。
func (f *FakeHost) Responses() []FakeControlResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeControlResponse, len(f.responses))
	copy(out, f.responses)
	return out
}

// Interrupts 返回中断次数。
func (f *FakeHost) Interrupts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interrupts
}
