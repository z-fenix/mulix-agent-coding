package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
)

// ---- 编辑器（023 D12 / FR-007：多行编辑器 + 历史召回）----
//
// composer 迁入 bubbles textarea（Task 4 依赖入库随取）：动态高度 1→8 行
// （MaxHeight 封顶，超出部分编辑器内滚——滚轮嵌套语义见 scrollstate.go），
// 真实终端光标（SetVirtualCursor(false)——光标落点即输入点，IME 候选窗锚定），
// 词级编辑键位对照参考 chat_tui.go:700 configureChatTextarea。提交历史召回
// （↑ 旧 ↓ 新、保草稿）对照参考 chat_tui.go:757-813。

const (
	// composerPromptWidth 是 "❯ " 的格宽——续行收到两空格对齐，文本与真实
	// 光标保持同列（对照参考：箭头只在首行，续行不重复）。
	composerPromptWidth = 2
	// composerMaxRows 是编辑器动态高度上限（超出内滚，滚轮嵌套见
	// handleWheel）。
	composerMaxRows = 8
	// composerCharLimit 是输入字符上限（对照参考 16384）。
	composerCharLimit = 16384
)

// composerState 输入行关注点子结构（D4 骨架本任务落位，取代平铺 m.input）：
// ta 持有编辑状态与键位；history/histIdx/draft 是提交历史召回游标——histIdx
// -1 = 停在草稿（未在召回）。
type composerState struct {
	ta      textarea.Model
	history []string // 成功派发的提交（Enter → pendingSubmit 路径）
	histIdx int      // 召回游标（-1 = 未在召回，停在草稿）
	draft   string   // 召回前的草稿（↓ 越过最新一条时还原）
}

// newComposer 构造编辑器并配置键位（对照参考 chat_tui.go:700
// configureChatTextarea）：Enter 提交（模型在 handleKey 拦截，不进 textarea），
// Alt+Enter / Ctrl+J / Shift+Enter 换行；词移动与删词 Alt 与 Ctrl 双轨
// （macOS 惯例 Alt+方向、Windows/Linux 终端发 Ctrl+方向——参考注释同源）。
func newComposer() composerState {
	ta := textarea.New()
	ta.SetPromptFunc(composerPromptWidth, func(info textarea.PromptInfo) string {
		if info.LineNumber != 0 {
			return ""
		}
		return "❯ "
	})
	ta.CharLimit = composerCharLimit
	// 提示符与真实光标已表明输入起点——空闲 composer 保持安静（对照参考）。
	ta.Placeholder = ""
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = composerMaxRows
	ta.MaxContentHeight = ta.CharLimit
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	// 真实终端光标（非样式虚拟光标）——View 经 tea.View.Cursor 定位到输入点。
	ta.SetVirtualCursor(false)
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j", "shift+enter"))
	ta.KeyMap.WordForward = key.NewBinding(key.WithKeys("alt+right", "alt+f", "ctrl+right"))
	ta.KeyMap.WordBackward = key.NewBinding(key.WithKeys("alt+left", "alt+b", "ctrl+left"))
	ta.KeyMap.DeleteWordBackward = key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w", "ctrl+backspace"))
	ta.Focus()
	ta.SetWidth(80) // WindowSizeMsg 到达前的合理缺省（同 newTuiModel）
	return composerState{ta: ta, histIdx: -1}
}

// composerValue 当前输入全文（多行含 \n）。
func (m *tuiModel) composerValue() string {
	return m.composerState.ta.Value()
}

// clearComposer 清空输入并复位召回态（Esc 空闲档 / Ctrl+C 清空档 / 提交后）。
func (m *tuiModel) clearComposer() {
	m.composerState.ta.SetValue("")
	m.composerState.histIdx = -1
	m.composerState.draft = ""
}

// restoreComposer 还原草稿文本（D10 忙碌 Esc 丢弃在途提交的还原路径）——
// 光标置尾（SetValue 语义），召回游标复位。
func (m *tuiModel) restoreComposer(text string) {
	m.composerState.ta.SetValue(text)
	m.composerState.histIdx = -1
	m.composerState.draft = ""
}

// rememberSubmitted 记录成功派发的提交入历史（对照参考 chat_tui.go:757
// rememberSubmittedInput）：去重紧邻重复；召回游标与草稿复位回草稿态。
func (m *tuiModel) rememberSubmitted(text string) {
	cs := &m.composerState
	n := len(cs.history)
	if n == 0 || cs.history[n-1] != text {
		cs.history = append(cs.history, text)
	}
	cs.histIdx = -1
	cs.draft = ""
}

// recallHistory 历史召回（对照参考 chat_tui.go:768 recallSubmittedInput）：
// delta<0 = ↑（更旧），delta>0 = ↓（更新）；返回是否消费了该键。忙碌期
// （running/swapping）不做召回——brief「↑↓ 空闲时历史召回」；对照参考
// chat_tui.go:1455-1470 在 tuiRunning 期间把 ↑/↓ 路由为光标移动：返回 false
// 落 textarea，多行草稿内是移动光标，单行首/末行 no-op（忙碌期草稿被历史
// 条目可见替换是危害向量——回合收口后 Enter 会提交召回文本而非草稿）。
// 首 ↑ 存草稿并跳最新一条；召回中改稿即以改后为新草稿基线；多行召回项内
// ↑/↓ 先归 textarea 移动光标（首行 ↑ / 末行 ↓ 才跨条）；越过最新一条还原
// 召回前草稿。
func (m *tuiModel) recallHistory(delta int) bool {
	if m.running || m.swapping {
		return false // 忙碌门禁：↑/↓ 归 textarea 光标移动（见上）
	}
	cs := &m.composerState
	if len(cs.history) == 0 {
		return false
	}
	if cs.histIdx >= 0 && cs.ta.Value() != cs.history[cs.histIdx] {
		// 召回项被改——改后即新草稿基线（对照参考 resetSubmittedInputRecall）。
		cs.histIdx = -1
	}
	idx := cs.histIdx
	if idx < 0 {
		if delta > 0 {
			return false // 已在草稿——没有更新的可召回
		}
		if cs.ta.Line() != 0 {
			return false // 首行 ↑ 才入历史；其余行归 textarea 移动光标
		}
		cs.draft = cs.ta.Value()
		idx = len(cs.history) - 1
	} else {
		if delta < 0 && cs.ta.Line() != 0 {
			return false
		}
		if delta > 0 && cs.ta.Line() != cs.ta.LineCount()-1 {
			return false
		}
		idx += delta
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cs.history) {
		cs.histIdx = -1
		m.setComposerValue(cs.draft)
		return true
	}
	cs.histIdx = idx
	m.setComposerValue(cs.history[idx])
	return true
}

// setComposerValue 整串替换输入（召回翻页共用）——光标置尾。
func (m *tuiModel) setComposerValue(s string) {
	m.composerState.ta.SetValue(s)
}
