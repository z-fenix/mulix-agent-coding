package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
)

// ---- /resume 会话选择器（023 FR-009；设计 §7.5）----
//
// 列表态模态（与 /agents 面板同级互斥：composer 冻结、滚轮冻结；审批卡在
// handleKey 前置分支优先——面板打开期间到账的审批先裁决）：↑/↓ 环绕选择、
// Enter 关面板并经 SessionSwapper 换绑（与 /resume <engine-id> 同一接缝——
// CloseCurrent → 排空 → Open 次序由 swapSession 钉死）、esc/q 关闭。数据源
// Options.ListSessions（组合根注入：sessionx.ScanSessions 摘要投影，新在前）；
// 回调未注入/空表 → transcript 留痕，不进入面板态（防御面，不静默）。

// resumePicker 选择器状态：items 打开时刻的数据快照（列表不随后续 Scan 变化
// ——选中下标稳定），sel 选中下标。
type resumePicker struct {
	items []SessionSummary
	sel   int
}

// openResumePicker 打开无参 /resume 选择器：回调未注入 → 留痕（防御性——
// 生产组合根必接线）；空表 → 留痕无可续链会话；否则进入列表态（sel=0，
// ScanSessions 新在前）。
func (m *tuiModel) openResumePicker() tea.Cmd {
	if m.opts.ListSessions == nil {
		m.appendLine("[resume] 会话列表未注入（组合根缺 ListSessions 接线）")
		return nil
	}
	items := m.opts.ListSessions()
	if len(items) == 0 {
		m.appendLine("[resume] 没有可续链的会话镜像（先 mulix 开新会话）")
		return nil
	}
	m.picker = &resumePicker{items: items, sel: 0}
	return nil
}

// movePicker 环绕移动选中项（与补全菜单同环绕口径）。
func (m *tuiModel) movePicker(delta int) {
	n := len(m.picker.items)
	if n == 0 {
		return
	}
	m.picker.sel = ((m.picker.sel+delta)%n + n) % n
}

// handlePickerKey 选择器键位（列表态独占 composer）：↑/↓ 选择、Enter 换绑
// 选中项（关面板 → swapSession 异步——swapping 忙门禁随置位接管）、esc/q
// 关闭。
func (m *tuiModel) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Code == tea.KeyEscape || msg.Code == 'q':
		m.picker = nil
	case msg.Code == tea.KeyUp:
		m.movePicker(-1)
	case msg.Code == tea.KeyDown:
		m.movePicker(+1)
	case msg.Code == tea.KeyEnter:
		if m.picker.sel >= len(m.picker.items) {
			return m, nil
		}
		engineID := m.picker.items[m.picker.sel].EngineID
		m.picker = nil
		if engineID == "" {
			// fail-closed：无链条目不得静默退化为 /new 语义（Open("") 开新
			// 会话——实测陷阱；选择器条目永不该空链，防御性拒绝并留痕）。
			m.appendLine("[resume] 该会话没有引擎 session id，无法续链")
			return m, nil
		}
		return m, m.swapSession(engineID)
	}
	return m, nil
}

// pickerView 渲染选择器（agentsPanelView 同形态的列表卡）：mulix id | 引擎
// id | 模型 | 工作区 | 创建时刻（RFC3339 截到分钟）| 无人值守标注；选中行
// ▸ 标记；底部键位提示。
func (m *tuiModel) pickerView() string {
	var b strings.Builder
	b.WriteString("会话（新在前）\n")
	for i, it := range m.picker.items {
		marker := "  "
		if i == m.picker.sel {
			marker = "▸ "
		}
		att := ""
		if it.Unattended {
			att = " · 无人值守"
		}
		b.WriteString(fmt.Sprintf("%s%-10s %-10s %s %s %s%s\n",
			marker, shortID(it.ID), shortID(it.EngineID), it.Model, it.Workspace, shortTime(it.CreatedAt), att))
	}
	b.WriteString("[↑/↓] 选择  [enter] 续链  [esc] 关闭\n")
	return b.String()
}

// shortTime 创建时刻展示形（RFC3339 截到分钟：2026-10-15T10:00 →
// 2026-10-15 10:00）；空串/短串原样。
func shortTime(s string) string {
	if len(s) > 16 {
		return strings.Replace(s[:16], "T", " ", 1)
	}
	return s
}
