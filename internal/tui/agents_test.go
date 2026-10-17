package tui

import (
	"context"
	"errors"
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

// TestTUIAgentsPanelTab pins（Task 15 收编——brief Step 1）：/agents 打开面板
// agents 页签（W<130 留痕拒绝——F2 守卫同族）；↑/↓ 选择语义保持（上界止步）、
// c 取消选中运行中子代理（治理路径 agentsCancelCmd 不变、取消保游标）；Esc 退
// 出页签交互（焦点回主列，面板不关——关闭归 /agents toggle）；独占语义三面
// （statusMode「agents 页签」/ modalOpen 手势冻结 / handlePaste 粘贴冻结）。
func TestTUIAgentsPanelTab(t *testing.T) {
	m, stub, _ := newTestModel(t)
	// 未接线：stub.Subagent() == nil → 提示，不进入交互
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.agentsTabInteractive() {
		t.Fatal("tab must not be interactive when subagent is disabled")
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

	// W<130：/agents 留痕拒绝（不开面板、不聚焦、不切页签）
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.agentsTabInteractive() || m.panel.tab == tabAgents {
		t.Fatalf("narrow width must refuse /agents: interactive=%v tab=%v",
			m.agentsTabInteractive(), m.panel.tab)
	}
	if !strings.Contains(m.View().Content, "面板需 ≥130 列") {
		t.Fatalf("width refusal missing:\n%s", m.View().Content)
	}

	m = update(t, m, tea.WindowSizeMsg{Width: 140, Height: 24})
	// 开：/agents → 面板开 + agents 页签 + 聚焦
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.agentsTabInteractive() || m.panel.tab != tabAgents || m.focus != focusPanel {
		t.Fatalf("/agents must open the agents tab: interactive=%v tab=%v focus=%v",
			m.agentsTabInteractive(), m.panel.tab, m.focus)
	}
	view := m.View().Content
	for _, want := range []string{"running", "任务一", "任务二", "深度"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agents tab view missing %q:\n%s", want, view)
		}
	}
	// 独占语义三面：statusMode「agents 页签」、手势冻结（modalOpen）、粘贴冻结
	if got := m.statusMode(); got != "agents 页签" {
		t.Fatalf("statusMode = %q, want agents 页签", got)
	}
	if !m.modalOpen() {
		t.Fatal("agents tab interaction must report modalOpen (gesture freeze)")
	}
	m = update(t, m, tea.PasteMsg{Content: "leak"})
	if m.composerValue() != "" {
		t.Fatalf("paste must not reach the frozen composer: %q", m.composerValue())
	}
	// ↑/↓ 选择（旧语义：上界止步）+ c 取消选中（治理路径不变、取消保游标）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.panel.agentsSel != 0 {
		t.Fatalf("sel must clamp at top: %d", m.panel.agentsSel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.panel.agentsSel != 1 {
		t.Fatalf("sel must move down: %d", m.panel.agentsSel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.panel.agentsSel != 0 {
		t.Fatalf("sel must move up: %d", m.panel.agentsSel)
	}
	m = update(t, m, keyPress('c'))
	infos := mgr.List()
	if infos[0].Status != subagent.StatusCancelled {
		t.Fatalf("first agent status = %v, want cancelled", infos[0].Status)
	}
	if m.panel.agentsSel != 0 {
		t.Fatalf("cancel must keep the selection: %d", m.panel.agentsSel)
	}
	// Esc：退出页签交互（焦点回主列）——面板不关、页签保持（F2 才关）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.agentsTabInteractive() || m.focus != focusMain {
		t.Fatalf("esc must exit tab interaction: interactive=%v focus=%v",
			m.agentsTabInteractive(), m.focus)
	}
	if !m.panel.open || m.panel.tab != tabAgents {
		t.Fatalf("esc must not close the panel: open=%v tab=%v", m.panel.open, m.panel.tab)
	}
	if got := m.statusMode(); got != "空闲" {
		t.Fatalf("statusMode after exit = %q, want 空闲", got)
	}
	if m.modalOpen() {
		t.Fatal("gestures must unfreeze after exiting the interaction")
	}
	// toggle：面板已开在 agents 页签 → /agents 关闭（F2 同路径）
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.panel.open {
		t.Fatal("/agents on the agents tab must close the panel (toggle)")
	}
	// 再开：/agents 重开面板至 agents 页签（游标归零）
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.agentsTabInteractive() {
		t.Fatal("/agents must reopen the agents tab")
	}
	if m.panel.agentsSel != 0 {
		t.Fatalf("reopen must reset the cursor: %d", m.panel.agentsSel)
	}
}

// TestTUIAgentsTabEmpty pins（brief 空态保留）：接线但无子代理 → 「暂无子代理」
// 空态 + 键位提示；空表 c no-op（不崩溃、游标不动）；Esc 退出交互。
func TestTUIAgentsTabEmpty(t *testing.T) {
	m, stub, _ := newTestModel(t)
	mgr, err := subagent.NewManager(subagent.Options{
		Root: t.TempDir(),
		Factory: func(context.Context, string) (subagent.SessionHandle, error) {
			return nil, errors.New("unused")
		},
		Publish: func(any) {},
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	stub.sub = mgr
	m = update(t, m, tea.WindowSizeMsg{Width: 140, Height: 24})
	m = update(t, m, typeStr(m, "/agents"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.agentsTabInteractive() {
		t.Fatal("agents tab must open when subagent wired")
	}
	view := m.View().Content
	if !strings.Contains(view, "暂无子代理") || !strings.Contains(view, "[esc] 返回") {
		t.Fatalf("empty state missing:\n%s", view)
	}
	m = update(t, m, keyPress('c'))
	if m.panel.agentsSel != 0 {
		t.Fatalf("empty cancel must keep the cursor: %d", m.panel.agentsSel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.agentsTabInteractive() {
		t.Fatal("esc must exit the interaction")
	}
}

// ---- 交互态 Ctrl+C 逃生口补钉（Task 15 deferred minor——Task 17 闭环）----

// TestAgentsTabCtrlCQuits 钉（Task 15 deferred minor「agents 交互态单按 Ctrl+C
// 退出无测试钉」——Task 17 补钉）：agents 页签交互态单击 Ctrl+C 直接退出
// （handleKey Ctrl+C 前置分支先于独占分支——独占态不可困死用户）；不走渲染
// 态双击武装阶梯（武装按返回 nil 命令——cmd 非 nil 即退出路径）。
func TestAgentsTabCtrlCQuits(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width = 140 // 交互态谓词宽度项（T17 可见性对账）
	m.panel.tab = tabAgents
	m.focus = focusPanel // panel.open 缺省 true——交互态成立
	if !m.agentsTabInteractive() {
		t.Fatal("precondition: agents tab interactive")
	}
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("single Ctrl+C in agents tab interaction must quit (escape hatch)")
	}
}
