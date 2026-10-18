// approval.go——审批通路(2026-10-18-web-ui-mvp Task 4)。
//
// 评审 C1 裁定:web UI 不自制 guard.Approver——control.Options.Approvals
// 是具体类型 *control.UserApprovalBridge,serve 复用 control.NewUserApprovalBridge()
// 双槽注入(Options.Approvals 与 guard.GateOptions.Approver 同一实例,先例
// internal/cli/session.go:177-184);审批决定的 HTTP 通路即既有
// controller.Approve/ApproveSession(别名登记走 controller.evaluateRuling
// 既有 SetAlias)。本文件只补 web 侧特有的一段:交互端离线取消器。
//
// fail-closed 红线:无交互端 → 拒绝,绝不静默放行——gone 关闭经既有
// bridge.CancelAll("interactive_offline") deny 全部待审请求,裁决理由随
// approval_resolved 镜像事件留痕;不给 internal/control、internal/guard
// 增改一行(治理语义零改动)。
package serve

import (
	"sync"

	"mulix/internal/control"
)

// interactiveOfflineBy 传给 CancelAll 的留痕理由(前端可见的拒绝原因)。
const interactiveOfflineBy = "interactive_offline"

// watchInteractiveGone 监听交互端离线:gone 关闭(或构造时已关闭)→ 以
// interactiveOfflineBy 调 bridge.CancelAll 恰一次,deny 全部待审请求,
// 随后退出。stop() 幂等且阻塞至 watcher 完全退出,之后不再响应 gone 的
// 关闭;stop 与 gone 竞争至多触发一次 CancelAll、绝不 panic(取消点复核
// stopped,让位先到的 stop)。
func watchInteractiveGone(gone <-chan struct{}, bridge *control.UserApprovalBridge) (stop func()) {
	stopped := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		select {
		case <-gone:
			select {
			case <-stopped: // stop 先到:让位退出,不取消
			default:
				bridge.CancelAll(interactiveOfflineBy)
			}
		case <-stopped:
		}
	}()
	return func() {
		once.Do(func() { close(stopped) })
		<-done
	}
}
