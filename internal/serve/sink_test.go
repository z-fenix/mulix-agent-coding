// sink_test.go——webSink 非阻塞 fan-out 语义钉子(2026-10-18-web-ui-mvp Task 3)。
//
// 三条契约:两订阅者按 Publish 序收到同序事件;订阅者缓冲(sinkBufferSize)
// 满即被断开(close channel)且发布者不阻塞、其他订阅者不受影响;退订后
// 不再送达。红线(control.Sink 注释):Sink 实现不得重入 Submit/Resume/
// Approve——本 sink 仅向自身 channel select-send,天然满足。
//
// 测试确定性纪律:快订阅者「跟得上」用同步交错表达(每发布一个即取走
// 一个),不依赖 goroutine 调度——并发消费者在全量测试负载下可被饿死整个
// 发布突发,导致快侧缓冲真实填满而被契约内正确断开(实测踩过)。
package serve

import (
	"testing"
	"time"
)

func TestWebSinkFanOutOrder(t *testing.T) {
	s := newWebSink()
	ch1, unsub1 := s.Subscribe()
	ch2, unsub2 := s.Subscribe()
	defer unsub1()
	defer unsub2()

	const n = 5
	for i := 0; i < n; i++ {
		s.Publish(i)
	}
	// 单锁串行发布 + channel FIFO ⇒ 各订阅者按 Publish 序收到同序事件。
	for i := 0; i < n; i++ {
		if got := <-ch1; got != i {
			t.Fatalf("订阅者1 第 %d 个事件: got %v want %v", i, got, i)
		}
		if got := <-ch2; got != i {
			t.Fatalf("订阅者2 第 %d 个事件: got %v want %v", i, got, i)
		}
	}
}

func TestWebSinkSlowSubscriberDropped(t *testing.T) {
	s := newWebSink()
	slow, unsubSlow := s.Subscribe()
	fast, unsubFast := s.Subscribe()
	defer unsubFast()

	// 慢订阅者全程不取事件;快订阅者每发布一个同步取走一个(显式交错)。
	// 若 Publish 阻塞,发布循环挂起直至测试超时——循环走完即「非阻塞」证据。
	const total = sinkBufferSize + 16
	for i := 0; i < total; i++ {
		s.Publish(i)
		if got := <-fast; got != i {
			t.Fatalf("快订阅者第 %d 个事件: got %v want %v", i, got, i)
		}
	}

	// 慢订阅者应被断开:残留恰为前 sinkBufferSize 个事件(先填满缓冲再断开,
	// 已入缓冲事件不丢),随后见 channel 关闭。
	drainCountUntilClosed(t, slow, sinkBufferSize, "慢订阅者 channel")
	unsubSlow() // 已被断开的订阅者再退订:不得双重 close panic

	// 断开后快订阅者仍在册:后续发布继续送达(其他订阅者不受影响)。
	s.Publish("after-drop")
	if got := <-fast; got != "after-drop" {
		t.Fatalf("慢订阅者断开后快订阅者未收到后续事件: got %v", got)
	}
}

// drainCountUntilClosed 取尽 ch 残留缓冲并断言条数;2s 内未见关闭即失败
// (不依赖 10 分钟测试超时定位)。
func drainCountUntilClosed(t *testing.T, ch <-chan any, want int, what string) {
	t.Helper()
	got := 0
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if got != want {
					t.Fatalf("%s 残留 %d, want %d(应先填满缓冲再断开)", what, got, want)
				}
				return
			}
			_ = ev
			got++
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 未按契约关闭", what)
		}
	}
}

func TestWebSinkUnsubscribe(t *testing.T) {
	s := newWebSink()
	ch, unsub := s.Subscribe()

	s.Publish(1)
	if got := <-ch; got != 1 {
		t.Fatalf("退订前事件: got %v want 1", got)
	}

	unsub()
	s.Publish(2) // 已退订:不送达、不 panic
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("退订后仍收到事件 %v", ev)
		}
		// ok=false:退订关闭 channel(契约——消费者侧 range 得以终结)
	default:
		t.Fatal("退订后 channel 未关闭(契约:unsub 应 close 使 range 终结)")
	}
}
