// sink.go——webSink:control.Sink 的 web 前端实现(2026-10-18-web-ui-mvp Task 3)。
//
// 与 control 侧同步 Sink 背压(P1 刻意为之:慢消费者拖住控制流保证顺序)
// 相反,webSink 面向浏览器订阅者:非阻塞 fan-out——订阅者缓冲(sinkBufferSize)
// 满即被断开(close 其 channel 并移出集合),绝不阻塞发布者。浏览器侧断线
// 由前端重连 + 镜像回放补偿,不反馈进控制流。
//
// 红线(control.Sink 注释):Publish 实现内不得重入 Submit/Resume/Approve
// ——本实现仅对订阅者 channel 做 select-send,天然满足。
package serve

import "sync"

// sinkBufferSize 单订阅者事件缓冲;满即断开(丢订阅者,不丢发布者节奏)。
const sinkBufferSize = 1024

// webSink 是事件扇出器:Publish 单写多读,Subscribe/Unsubscribe 并发安全。
type webSink struct {
	mu   sync.Mutex
	subs map[chan any]struct{}
}

func newWebSink() *webSink {
	return &webSink{subs: make(map[chan any]struct{})}
}

// Publish 非阻塞 fan-out:对每个在册订阅者 select-send,缓冲满即断开
// (close+delete)。同锁内 close 先于 delete,退订侧以「map 在册」为准判重,
// 不会双重 close。
func (s *webSink) Publish(ev any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
			close(ch)
			delete(s.subs, ch)
		}
	}
}

// Subscribe 返回事件流与退订函数。事件按 Publish 序送达(单锁串行发布 +
// channel FIFO);channel 关闭(被断开或退订)即流终结。
func (s *webSink) Subscribe() (<-chan any, func()) {
	ch := make(chan any, sinkBufferSize)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	unsub := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// 已被 Publish 断开的订阅者不在册:不重复 close。
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
	return ch, unsub
}
