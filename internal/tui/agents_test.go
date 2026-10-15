package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"charm.land/bubbletea/v2"

	"mulix/internal/subagent"
)

// agentsStubHandle 是 /agents 面板测试的最小句柄（真实 Manager 消费）。
type agentsStubHandle struct {
	id      string
	summary string
	done    chan struct{}
	cancels int
	mu      sync.Mutex
}

func (h *agentsStubHandle) ID() string                  { return h.id }
func (h *agentsStubHandle) Done() <-chan struct{}       { return h.done }
func (h *agentsStubHandle) Err() error                  { return nil }
func (h *agentsStubHandle) Summary() string             { return h.summary }
func (h *agentsStubHandle) TotalTokens() int64          { return 100 }
func (h *agentsStubHandle) CostUSDCents() int64         { return 3 }
func (h *agentsStubHandle) Close(context.Context) error { return nil }
func (h *agentsStubHandle) Cancel(context.Context) error {
	h.mu.Lock()
	h.cancels++
	h.mu.Unlock()
	return nil
}

// TestTUIAgentsPanel pins（brief Step 3 TUI 面）：/agents 未启用 → 命令提示；
// 启用 → 列表视图（子会话 id/状态/任务/深度）；c 取消选中运行中子代理；
// esc 关闭。
func TestTUIAgentsPanel(t *testing.T) {
	m, stub, _ := newTestModel(t)
	// 未启用：stub.Subagent() == nil → 提示，不进入面板态
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.agentsOpen {
		t.Fatal("panel must not open when subagent is disabled")
	}
	if !strings.Contains(m.View().Content, "未启用") {
		t.Fatalf("disabled hint missing:\n%s", m.View().Content)
	}

	// 启用：真实 Manager + stub 句柄（两行列表，深度 0/1）
	var stubSeq atomic.Int64
	mgr, err := subagent.NewManager(subagent.Options{
		Root: t.TempDir(),
		Factory: func(context.Context, string) (subagent.SessionHandle, error) {
			return &agentsStubHandle{
				id: fmt.Sprintf("stub-%d", stubSeq.Add(1)), done: make(chan struct{}),
			}, nil
		},
		Publish: func(any) {},
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if _, err := mgr.Spawn(context.Background(), "p", "任务一", 0); err != nil {
		t.Fatalf("spawn 1: %v", err)
	}
	if _, err := mgr.Spawn(context.Background(), "p", "任务二", 1); err != nil {
		t.Fatalf("spawn 2: %v", err)
	}
	stub.sub = mgr
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.agentsOpen {
		t.Fatal("panel must open when subagent enabled")
	}
	view := m.View().Content
	for _, want := range []string{"running", "任务一", "任务二", "深度"} {
		if !strings.Contains(view, want) {
			t.Fatalf("panel view missing %q:\n%s", want, view)
		}
	}
	// c 取消选中的第一个 running 子代理
	m = update(t, m, keyPress('c'))
	infos := mgr.List()
	if infos[0].Status != subagent.StatusCancelled {
		t.Fatalf("first agent status = %v, want cancelled", infos[0].Status)
	}
	// esc 关闭面板
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.agentsOpen {
		t.Fatal("esc must close the panel")
	}
}
