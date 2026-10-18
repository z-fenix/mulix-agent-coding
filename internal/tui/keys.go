package tui

import (
	"context"
	"time"

	"charm.land/bubbletea/v2"
)

// ---- 键位语义（023 D10 取消语义阶梯；集中此文件）----
//
// 渲染态键位阶梯（FR-011：语义无歧义；审批卡与 agents 页签交互态（Task 15
// 收编口径 agentsTabInteractive）为治理独占态，在 handleKey 前置分支拦截，
// 不走本阶梯）：
//
//	Esc（忙碌）  pendingSubmit 在途 → 丢弃提交（代际递增作废迟到收据）+ 还原草稿
//	            否则回合进行中    → 本地取消意图 + 异步 ctrl.Cancel（幂等）
//	Esc（空闲）  清空输入
//	Ctrl+C      活跃选区 → 复制选区（消费高亮；Task 21 接入第一档）
//	            有输入 → 清空输入（清空按不武装）
//	            空输入 → 窗口内二按退出（首按武装并提示）
//
// agents 页签交互态的 Esc（Task 15）在独占分支内消费（model.go
// handleAgentsKey）：退出页签交互（焦点回主列），不关面板、不清输入——面板
// 关闭归 /agents toggle / F2；侧栏/面板聚焦的取焦档见下方 handleEscapeKey。
//
// 忙碌判定键 off m.running（回合生命周期）；m.swapping 过渡期旧控制器已收口，
// 取消/丢弃无处可打，Esc 为 no-op。

// quitArmWindow 是 Ctrl+C 双击退出的确认窗口（D10 防误触）。
const quitArmWindow = 1500 * time.Millisecond

// pendingSubmit 是 Enter 已派发、TurnStartedEvent 未到的在途提交（D10 上屏
// 时机：用户气泡在回合开始事件到达时才上屏——中断窗口内气泡不进转写）。gen
// 记录派发时的模型代际计数：丢弃路径递增计数，使迟到的提交收据（submitDoneMsg）
// 与本代际失配而作废（气泡不上屏由 pending 置 nil 保证——同刻至多一笔在途）。
type pendingSubmit struct {
	text string // TurnStarted 上屏的用户气泡文本（Enter 时的裁剪输入）
	gen  uint64 // 派发时代际计数（与 submitDoneMsg.gen 对账）
}

// handleEscapeKey 渲染态 Esc 阶梯（D10）：忙碌且气泡未上屏 → 丢弃在途提交并
// 还原草稿（代际递增——迟到的 TurnStarted 不上屏、迟到的收据不渲染失败行）；
// 忙碌且回合进行中 → 本地取消意图 + 异步 Cancel（已在途则幂等不重发）；换绑
// 过渡期 no-op；空闲 → 聚焦态（侧栏/面板——Task 14 扩展面板档）先取焦回主列
// （Task 13 最小焦点形——阶梯扩展档，清空档之前；取焦消费本次 Esc 不连清输入，
// 预览/开合态保留），否则清空输入。忙碌/换绑档
// 语义不动（取消优先于取焦——焦点保持，Esc 取消后仍可侧栏导航）。
func (m *tuiModel) handleEscapeKey() (tea.Model, tea.Cmd) {
	if m.swapping {
		return m, nil
	}
	if m.running {
		if m.pendingSubmit != nil {
			// 丢弃在途提交：还原草稿（忙期窗口内用户未再输入时——已有输入
			// 以用户键入为准，弃稿不覆盖）；代际递增使迟到收据作废。
			if m.composerValue() == "" {
				m.restoreComposer(m.pendingSubmit.text)
			}
			m.pendingSubmit = nil
			m.genCounter++
			return m, nil
		}
		if !m.cancelIntent {
			m.cancelIntent = true
			return m, m.cancelCmd()
		}
		return m, nil // 取消已在途——幂等
	}
	if m.focus != focusMain { // 侧栏/面板聚焦先取焦回主列（Task 13 最小焦点形
		// + Task 14 面板档——阶梯扩展档，清空档之前；取焦消费本次 Esc 不连清
		// 输入，预览/开合态保留）
		m.focus = focusMain
		return m, nil
	}
	m.clearComposer()
	return m, nil
}

// handleCtrlCKey 渲染态 Ctrl+C 阶梯（FR-011 防误触）：第一档——活跃转写
// 选区非空 → 复制并消费高亮（不清输入、不武装退出——Task 21 接入，对照参考
// 「Ctrl+C copies the selection」惯例）；无选区：有输入 → 清空（清空按不武装
// ）；空输入首按 → 武装并提示；窗口内二按 → 退出，窗外 → 重武装。
func (m *tuiModel) handleCtrlCKey() tea.Cmd {
	if sel := m.transcriptState.sel; sel.active && !sel.empty() {
		text := m.transcriptState.selectedText()
		m.transcriptState.sel = selection{}
		return m.copyCmd(text)
	}
	if m.composerValue() != "" {
		m.clearComposer()
		m.quitArm = time.Time{}
		return nil
	}
	now := time.Now()
	if !m.quitArm.IsZero() && now.Sub(m.quitArm) <= quitArmWindow {
		return tea.Quit
	}
	m.quitArm = now
	m.appendLine("[退出] 再按一次 Ctrl+C 退出（1.5s 内）")
	return nil
}

// commitPendingBubble 在 TurnStartedEvent 到达时上屏在途用户气泡（D10 上屏
// 时机）。无 pending（丢弃后迟到的 TurnStarted、或换绑清账后的新会话回合）为
// no-op。
func (m *tuiModel) commitPendingBubble() {
	if m.pendingSubmit == nil {
		return
	}
	m.transcriptState.append(userBlock{text: m.pendingSubmit.text})
	m.pendingSubmit = nil
}

// cancelDoneMsg 是回合取消的异步回执（Cancel 是控制协议时长级调用——Update 内
// 不阻塞，与 submitCmd/agentsCancelCmd 同纪律，T28 评审 Critical-1 同类）。
type cancelDoneMsg struct{ err error }

// cancelCmd 异步取消当前回合（D10 Esc 阶梯；快照 ctrl——回执到达前换绑也不
// 得把取消打给新控制器）。
func (m *tuiModel) cancelCmd() tea.Cmd {
	ctrl := m.ctrl
	return func() tea.Msg { return cancelDoneMsg{err: ctrl.Cancel(context.Background())} }
}
