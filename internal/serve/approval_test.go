// approval_test.go——interactiveGone 取消器钉子(2026-10-18-web-ui-mvp
// Task 4):gone 关闭 → 既有 bridge.CancelAll("interactive_offline") deny
// 全部待审(fail-closed);stop 后退出不再响应。驱动真实
// control.UserApprovalBridge——评审 C1 裁定 serve 不自制 Approver。
package serve

import (
	"context"
	"testing"
	"time"

	"mulix/internal/control"
	"mulix/internal/guard"
)

// waitForPending 轮询直至请求在桥上登记为待审(ApproveRequest 在别的
// goroutine 阻塞中的标志),超时即失败。
func waitForPending(t *testing.T, bridge *control.UserApprovalBridge, toolUseID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bridge.HasPending(toolUseID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("待审请求 %q 未在桥上登记", toolUseID)
}

// approveAsync 在后台发起 ApproveRequest(阻塞等待),结果回传通道。
func approveAsync(bridge *control.UserApprovalBridge, toolUseID string) <-chan approvalOutcome {
	ch := make(chan approvalOutcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		v, err := bridge.ApproveRequest(ctx, guard.ApprovalRequest{ToolUseID: toolUseID, Tool: "Bash"})
		ch <- approvalOutcome{verdict: v, err: err}
	}()
	return ch
}

type approvalOutcome struct {
	verdict guard.ApprovalVerdict
	err     error
}

func TestGoneCancelsPending(t *testing.T) {
	bridge := control.NewUserApprovalBridge()
	gone := make(chan struct{})
	stop := watchInteractiveGone(gone, bridge)
	defer stop()

	res := approveAsync(bridge, "t1")
	waitForPending(t, bridge, "t1")
	close(gone)

	select {
	case out := <-res:
		if out.err != nil {
			t.Fatalf("ApproveRequest err = %v, want nil(经 CancelAll 解阻塞)", out.err)
		}
		if out.verdict.Allow {
			t.Fatal("verdict.Allow = true, want false(fail-closed:离线即拒绝)")
		}
		if out.verdict.ResolvedBy != "interactive_offline" {
			t.Fatalf("verdict.ResolvedBy = %q, want %q", out.verdict.ResolvedBy, "interactive_offline")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close(gone) 后 ApproveRequest 仍未解阻塞")
	}
}

func TestGoneNoPendingHarmless(t *testing.T) {
	bridge := control.NewUserApprovalBridge()
	gone := make(chan struct{})
	stop := watchInteractiveGone(gone, bridge)

	close(gone) // 无 pending:CancelAll 空转,不得 panic
	stop()      // watcher 已自行退出;stop 幂等,不 panic

	// 桥不受影响:后续请求正常登记、正常裁决(allow 全程未被篡改)。
	res := approveAsync(bridge, "t2")
	waitForPending(t, bridge, "t2")
	if !bridge.Resolve("t2", guard.ApprovalVerdict{Allow: true, ResolvedBy: "user:test"}) {
		t.Fatal("Resolve = false, want true(空转后桥仍可裁决)")
	}
	out := <-res
	if out.err != nil {
		t.Fatalf("ApproveRequest err = %v, want nil", out.err)
	}
	if !out.verdict.Allow || out.verdict.ResolvedBy != "user:test" {
		t.Fatalf("verdict = %+v, want allow/user:test", out.verdict)
	}
}

func TestStopExitsWatcher(t *testing.T) {
	bridge := control.NewUserApprovalBridge()
	gone := make(chan struct{})
	stop := watchInteractiveGone(gone, bridge)

	res := approveAsync(bridge, "t3")
	waitForPending(t, bridge, "t3")

	stop()      // 阻塞至 watcher 完全退出(无泄漏)
	close(gone) // 晚到的 gone:不得触发 CancelAll

	if !bridge.Resolve("t3", guard.ApprovalVerdict{Allow: true, ResolvedBy: "user:test"}) {
		t.Fatal("Resolve = false, want true(stop 后 gone 关闭不得抢先 deny)")
	}
	out := <-res
	if out.err != nil {
		t.Fatalf("ApproveRequest err = %v, want nil", out.err)
	}
	if !out.verdict.Allow || out.verdict.ResolvedBy != "user:test" {
		t.Fatalf("verdict = %+v, want allow/user:test(CancelAll 不该触发)", out.verdict)
	}
}

func TestGoneAlreadyClosedFiresImmediately(t *testing.T) {
	bridge := control.NewUserApprovalBridge()
	// 先登记待审再建 watcher:已关闭的 gone 让 CancelAll 在 watcher 启动
	// 即触发,待审在册才能稳定接到 deny(顺序反了则 deny 落空,与实现
	// 无关的测试伪失败)。
	res := approveAsync(bridge, "t4")
	waitForPending(t, bridge, "t4")

	gone := make(chan struct{})
	close(gone) // watcher 构造时 gone 已关闭:立即取消,不得等待
	stop := watchInteractiveGone(gone, bridge)
	defer stop()

	select {
	case out := <-res:
		if out.err != nil || out.verdict.Allow || out.verdict.ResolvedBy != "interactive_offline" {
			t.Fatalf("outcome = (%+v, %v), want deny/interactive_offline/nil err", out.verdict, out.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("已关闭的 gone 未触发 CancelAll")
	}
}
