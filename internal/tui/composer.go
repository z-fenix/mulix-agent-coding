package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbletea/v2"
)

const (
	// pasteFoldThresholdLines / pasteFoldThresholdChars 是粘贴折叠阈值（023
	// FR-008）：bracketed paste 达到任一阈值即折叠为可删记号——行数按规范化
	//（\r\n|\r → \n）后计，字符按 rune 计（CJK 同权）。
	pasteFoldThresholdLines = 3
	pasteFoldThresholdChars = 200
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
// -1 = 停在草稿（未在召回）；folds/nextFoldID 是粘贴折叠账目（FR-008）。
type composerState struct {
	ta      textarea.Model
	history []string // 成功派发的提交（Enter → pendingSubmit 路径）
	histIdx int      // 召回游标（-1 = 未在召回，停在草稿）
	draft   string   // 召回前的草稿（↓ 越过最新一条时还原）

	folds      []pasteFold // 编辑器内在册折叠记号（随编辑器内容增删同步）
	nextFoldID int         // 折叠 id 游标（单调递增，删除不复用）
}

// pasteFold 一次大粘贴的折叠账目（023 FR-008）：body 持原文全文，编辑器内
// 只落 label 记号（单文本单元，可整体删除）；id 会话内单调递增（删除不复用
// ——记号串无歧义，重复粘贴各得各的记号）。
type pasteFold struct {
	id    int    // 会话内单调递增的折叠序号（记号串 #N）
	lines int    // 原文行数（规范化后；记号串 M 行）
	body  string // 原文全文（提交展开的数据源；记号删除即随账丢弃）
}

// label 折叠记号（编辑器可见文本；提交展开按此子串整段替换——标签被改坏即
// 不再匹配，按字面文本对待）。
func (f pasteFold) label() string {
	return fmt.Sprintf("[粘贴 #%d · %d 行]", f.id, f.lines)
}

// pasteLineCount 规范化行数（\r\n|\r → \n 后按 \n 计；空串 0 行）。
func pasteLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s), "\n") + 1
}

// shouldFoldPaste 折叠阈值判定（≥pasteFoldThresholdLines 行 或
// ≥pasteFoldThresholdChars 字符，023 FR-008）。
func shouldFoldPaste(s string) bool {
	return pasteLineCount(s) >= pasteFoldThresholdLines ||
		len([]rune(s)) >= pasteFoldThresholdChars
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
// 在册 fold 一并剪除——草稿弃即粘贴正文弃（不残留、不复活）。
func (m *tuiModel) clearComposer() {
	m.composerState.ta.SetValue("")
	m.composerState.histIdx = -1
	m.composerState.draft = ""
	m.syncComposerFolds()
}

// restoreComposer 还原草稿文本（D10 忙碌 Esc 丢弃在途提交的还原路径）——
// 光标置尾（SetValue 语义），召回游标复位。
func (m *tuiModel) restoreComposer(text string) {
	m.composerState.ta.SetValue(text)
	m.composerState.histIdx = -1
	m.composerState.draft = ""
	m.syncComposerFolds()
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

// setComposerValue 整串替换输入（召回翻页共用）——光标置尾。不剪除 fold：
// 召回是草稿的临时可见替换，↓ 越过最新一条还原草稿时记号须仍有效（fold 账
// 目随编辑器逐键同步与清空/还原路径剪除，见 syncComposerFolds）。
func (m *tuiModel) setComposerValue(s string) {
	m.composerState.ta.SetValue(s)
}

// ---- 粘贴折叠（023 FR-008）----
//
// bracketed paste 达阈值折叠为可删记号：folds 账目持原文，编辑器只落记号
//（单文本单元）；提交时记号位置替换回全文（镜像/引擎只见全文）。记号生命
// 周期随编辑器逐键同步：删除或改坏即剪除账目，正文不残留、提交不复活。

// handlePaste bracketed paste 入口（Update 路由 tea.PasteMsg）：治理独占态
// （审批卡 / agents 面板）composer 冻结——粘贴不泄漏（与 handleKey 同门禁）；
// 达折叠阈值折叠为可删记号，低于阈值原文直插（textarea 原生粘贴路径）。回合
// 进行中不设门禁——忙期起草下一条输入与键入同权。
func (m *tuiModel) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.approval != nil || m.agentsOpen {
		return m, nil
	}
	if shouldFoldPaste(msg.Content) {
		m.insertPasteFold(msg.Content)
		return m, nil
	}
	var cmd tea.Cmd
	m.composerState.ta, cmd = m.composerState.ta.Update(msg)
	m.syncComposerFolds() // 选区覆盖记号的原生粘贴路径——记号可能随选区被替换
	return m, cmd
}

// insertPasteFold 折叠入编辑器（FR-008）：folds 记账原文，编辑器只落记号
// （单文本单元 + 尾随空格词边界）；先删当前选区（覆盖式粘贴语义，与 textarea
// 原生 PasteMsg 路径同法）。插入后同步 fold 在册状态——CharLimit 截断等导致
// 记号不完整时即剪除，残段按字面文本对待。
func (m *tuiModel) insertPasteFold(body string) {
	cs := &m.composerState
	if cs.ta.HasSelection() {
		cs.ta.DeleteSelection()
	}
	cs.nextFoldID++
	f := pasteFold{id: cs.nextFoldID, lines: pasteLineCount(body), body: body}
	cs.folds = append(cs.folds, f)
	cs.ta.InsertString(f.label() + " ")
	m.syncComposerFolds()
}

// syncComposerFolds 记号生命周期同步（FR-008）：标签已不在编辑器中的 fold
// 即剪除——记号被整体删除则正文随账丢弃（不残留、提交不复活）；标签被改坏
// 则按字面文本对待（不再展开，正文一并弃）。编辑器内容每次变更后调用。
func (m *tuiModel) syncComposerFolds() {
	cs := &m.composerState
	if len(cs.folds) == 0 {
		return
	}
	v := cs.ta.Value()
	kept := cs.folds[:0]
	for _, f := range cs.folds {
		if strings.Contains(v, f.label()) {
			kept = append(kept, f)
		}
	}
	cs.folds = kept
}

// expandFolds 提交展开（FR-008）：编辑器文本中的在册记号替换为原文全文——
// 单趟从左到右按最早记号位置展开，替换进来的正文不再参与匹配（正文内偶现
// 的同形串不误展开）；同一记号串出现多次（用户复制记号文本）按同义展开，
// 与参考实现的 ReplaceAll 同语义；不在册/已剪除的记号形文本一律按字面对待。
func (m *tuiModel) expandFolds(s string) string {
	folds := m.composerState.folds
	if len(folds) == 0 {
		return s
	}
	var b strings.Builder
	for {
		at, pick := -1, -1
		for idx, f := range folds {
			if p := strings.Index(s, f.label()); p >= 0 && (at < 0 || p < at) {
				at, pick = p, idx
			}
		}
		if pick < 0 {
			break
		}
		b.WriteString(s[:at])
		b.WriteString(folds[pick].body)
		s = s[at+len(folds[pick].label()):]
	}
	b.WriteString(s)
	return b.String()
}
