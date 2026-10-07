package control

import (
	"context"
	"sync"

	"mulix/internal/guard"
)

// UserApprovalBridge 把 guard.Gate 的审批请求桥接到人工裁决入口
// （Controller.Approve / ApproveSession——TUI 审批卡片的数据源）。桥是
// guard.Approver 的 control 侧实现（guard 不依赖 control——分层红线）。
//
// 关联模型：Gate 内部自造审批 id（guard.ApprovalRequest.ToolUseID），而
// 人工裁决以「引擎 tool_use_id」为准（can_use_tool 参数）——control 在调
// Gate.Evaluate 前经 SetAlias 登记别名，桥以双键登记同一待审请求；泵串行
// （单 goroutine 分发）保证至多一个别名在飞。
type UserApprovalBridge struct {
	mu      sync.Mutex
	pending map[string]*approvalWait // gate 审批 id 与别名（引擎 tool_use_id）双键
	alias   string                   // 下一次登记追加的别名
}

// approvalWait 是一个待审请求及其裁决通道（容量 1：裁决恰一次）。
type approvalWait struct {
	req guard.ApprovalRequest
	ch  chan guard.ApprovalVerdict
}

// NewUserApprovalBridge 构造空桥。
func NewUserApprovalBridge() *UserApprovalBridge {
	return &UserApprovalBridge{pending: map[string]*approvalWait{}}
}

// SetAlias 为下一次 ApproveRequest 登记追加别名（引擎 tool_use_id ↔ gate
// 审批 id 的关联；Evaluate 串行，登记即消费）。
func (b *UserApprovalBridge) SetAlias(toolUseID string) {
	b.mu.Lock()
	b.alias = toolUseID
	b.mu.Unlock()
}

// ApproveRequest 实现 guard.Approver：登记 pending 后阻塞等待裁决或窗口
// 关闭（ctx 由 Gate 以审批窗口限时——超时返回 err，Gate 归 fail-closed）。
func (b *UserApprovalBridge) ApproveRequest(ctx context.Context, req guard.ApprovalRequest) (guard.ApprovalVerdict, error) {
	w := &approvalWait{req: req, ch: make(chan guard.ApprovalVerdict, 1)}
	b.mu.Lock()
	b.pending[req.ToolUseID] = w
	if b.alias != "" && b.alias != req.ToolUseID {
		b.pending[b.alias] = w
	}
	alias := b.alias
	b.alias = ""
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.ToolUseID)
		if alias != "" {
			delete(b.pending, alias)
		}
		b.mu.Unlock()
	}()
	select {
	case v := <-w.ch:
		return v, nil
	case <-ctx.Done():
		return guard.ApprovalVerdict{}, ctx.Err()
	}
}

// Resolve 交付裁决（toolUseID 可为 gate 审批 id 或别名）。返回 false =
// 无此待审请求或已在途（另一键已裁决）。
func (b *UserApprovalBridge) Resolve(toolUseID string, v guard.ApprovalVerdict) bool {
	b.mu.Lock()
	w := b.pending[toolUseID]
	b.mu.Unlock()
	if w == nil {
		return false
	}
	select {
	case w.ch <- v:
		return true
	default:
		return false
	}
}

// PendingRequest 返回待审请求的原始描述（ApproveSession 取工具与规范化范围）。
func (b *UserApprovalBridge) PendingRequest(toolUseID string) (guard.ApprovalRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if w, ok := b.pending[toolUseID]; ok {
		return w.req, true
	}
	return guard.ApprovalRequest{}, false
}

// HasPending 报告某请求是否仍在等待裁决。
func (b *UserApprovalBridge) HasPending(toolUseID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending[toolUseID] != nil
}

// CancelAll 以 deny 裁决全部待审请求（Cancel 中断时解阻塞泵；裁决理由随
// approval_resolved 镜像留痕）。幂等：已在途的裁决不被覆盖。
func (b *UserApprovalBridge) CancelAll(resolvedBy string) {
	b.mu.Lock()
	seen := map[*approvalWait]bool{}
	var ws []*approvalWait
	for _, w := range b.pending {
		if !seen[w] {
			seen[w] = true
			ws = append(ws, w)
		}
	}
	b.mu.Unlock()
	v := guard.ApprovalVerdict{Allow: false, ResolvedBy: resolvedBy}
	for _, w := range ws {
		select {
		case w.ch <- v:
		default:
		}
	}
}
