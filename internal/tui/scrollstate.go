package tui

import (
	"charm.land/bubbletea/v2"
)

// ---- 显式尾随状态机与滚动手势（023 D5，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix (MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix) internal/cli/scroll_state.go：
// 状态机逐函数对齐参考 scroll_state.go，注释语义逐条对照。
// 两处适配：
//  1. 接收者——参考方法挂 chatTUI（字段平铺模型本体）；本仓滚动状态收敛为
//     tuiModel.scrollState 子结构（D4 骨架落位），方法挂 *tuiModel。
//  2. modalOpen 豁免集——参考为 approval/chooser/rewind/mcp 等 10 项；本仓
//     = 授权卡 + /agents + /resume 选择器 + 补全菜单（FR-009 Task 15 增入）。
//
// 此前的隐式尾随（syncViewport 无条件 GotoBottom）在"应用消息前的 wasAtBottom
// 快照"方案下有已知缺陷：开授权卡/面板这类仅改变底部区高度（bottomRows 增大、
// 视口变矮）的模态会让 AtBottom() 翻假，永久关闭尾随直到用户手动回底（参考
// #6430）；高频开合面板的长会话表现为「滚动坏了」（参考 #6978）。本状态机
// 不取位置快照——只有显式手势（滚轮/PgUp/拖拽/Ctrl+Home）写 userScrolled。
//
// 规则（对照参考 scroll_state.go:14-20）：
//   - 默认 followTail：新内容把视口钉在底部；
//   - 显式滚离底部 → userScrolled（滚轮上/PgUp/滚动条拖拽/Ctrl+Home…）；
//   - 回到底部（滚到底/PgDn 到底/Ctrl+End/forceGotoBottom 会话切换）→ 恢复
//     followTail；
//   - 仅改变底部行数的模态开合永不翻转模式（height-only 变化在跟随态下仍钉底，
//     见 frame() 的钉底条件）。

// scrollFollowMode 是转写尾随的显式状态机（对照参考 scroll_state.go:21-26）。
type scrollFollowMode uint8

const (
	scrollFollowTail   scrollFollowMode = iota // 新内容钉底（默认）
	scrollUserScrolled                         // 用户显式滚离——新内容不得拉动视口
)

// scrollState 滚动关注点子结构（D4 骨架占位落位）：尾随模式与单帧强制置底。
type scrollState struct {
	mode            scrollFollowMode
	forceGotoBottom bool // 单帧豁免：置底并恢复跟随（会话切换等），消费帧即清
}

// markUserScrolled 记录用户有意离开实时尾部（对照参考 scroll_state.go:28-31）。
func (m *tuiModel) markUserScrolled() {
	m.scrollState.mode = scrollUserScrolled
}

// markFollowTail 在用户（或会话切换）回到最新输出后恢复实时尾随（对照参考
// scroll_state.go:33-37）。
func (m *tuiModel) markFollowTail() {
	m.scrollState.mode = scrollFollowTail
}

// shouldFollowTail 报告新转写内容是否应把视口钉在底部。forceGotoBottom 单帧
// 恒胜（对照参考 scroll_state.go:39-43）。
func (m *tuiModel) shouldFollowTail() bool {
	return m.scrollState.mode == scrollFollowTail || m.scrollState.forceGotoBottom
}

// modalOpen 为「抢占底部行数（或整个主区）的面板」激活时的真值。height-only
// 布局变化不得据此标记 userScrolled（对照参考 scroll_state.go:45-58）。豁免集
// ：授权卡 + /agents 面板 + /resume 选择器 + 补全菜单（023 FR-009 Task 15 增入
// ——菜单/选择器都抬高底部区；菜单虽非独占态，同属 height-only 开合）。
func (m *tuiModel) modalOpen() bool {
	return m.approval != nil || m.agentsOpen || m.picker != nil || m.completeOpen()
}

// syncScrollModeAfterGesture 在主动滚动命令后按当前视口位置归位跟随态：落在
// 底部恢复 followTail；其余偏移标记 userScrolled（对照参考 scroll_state.go:60-69）。
func (m *tuiModel) syncScrollModeAfterGesture() {
	if m.transcriptState.vp.AtBottom() {
		m.markFollowTail()
		return
	}
	m.markUserScrolled()
}

// wheelScrollRows 滚轮单次滚动行数（对照参考 chat_tui.go:1113/1115 的 3 行）。
const wheelScrollRows = 3

// handleWheel 滚轮手势：模态独占态不泄漏（授权卡/agents 面板——与键位门禁同
// 语义，D10/D11 治理独占期交互冻结）；渲染态先试编辑器嵌套滚动（023 D12：
// 编辑器内容超过 MaxHeight 行时 textarea 内建 viewport 可滚——把消息喂给它，
// ScrollYOffset 前后差即消费凭据；未消费 = 未溢出或已到边，落转写视口续滚，
// 对照参考 chat_tui.go:1095-1118 的 composer 内滚轮续转写嵌套）；转写滚上/下
// 并按落点归位跟随态。
func (m *tuiModel) handleWheel(msg tea.MouseWheelMsg) {
	if m.modalOpen() {
		return
	}
	before := m.composerState.ta.ScrollYOffset()
	m.composerState.ta, _ = m.composerState.ta.Update(msg)
	if m.composerState.ta.ScrollYOffset() != before {
		return // 编辑器消费了滚轮（溢出内滚）——不透传转写
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.transcriptState.vp.ScrollUp(wheelScrollRows)
	case tea.MouseWheelDown:
		m.transcriptState.vp.ScrollDown(wheelScrollRows)
	}
	m.syncScrollModeAfterGesture()
}
