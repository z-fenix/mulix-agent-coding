package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// ---- 语义保留转写块（FR-001）----
//
// 块是 transcript 的语义单元（一事件/一输入/一系统行 = 一块），render(width)
// 把语义源按宽渲染为可见行（含样式）。渲染行缓存与 viewport 喂给在此收口；
// 换行缓存的增量重渲染见 wrapcache.go（023 D6 移植）。

// block 语义保留转写块：语义源按宽渲染（含样式；换行走 lipgloss——宽度感知
// 含 CJK 与 SGR 断点平衡，对照参考 transcript.go:311 wrapTranscript 同法）。
type block interface {
	render(width int) []string
}

// wrapText 语义源按宽换行为行切片（wrapcache 按块缓存此结果）。
func wrapText(s string, width int) []string {
	if width <= 0 {
		width = 1
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

// plainBlock 系统行块（notice/turn/审批留痕等）——旧行为迁移包装：appendLine
// 一调用一块。
type plainBlock struct{ text string }

func (b plainBlock) render(width int) []string { return wrapText(b.text, width) }

// userBlock 用户输入块（composer 提交回显接线随后续任务；本任务先立块型）。
type userBlock struct{ text string }

func (b userBlock) render(width int) []string { return wrapText(b.text, width) }

// assistantBlock 助手文本块：markdown 源经 renderMarkdown 按宽渲染（标题/
// 加粗/行内代码/围栏/列表 + CJK 强调修正）。主题线程化（Task 18 FR-014/D8）：
// 传包级 activeTheme 指针——渲染时读取当前槽位，/theme 换肤 + 换行缓存失效后
// 重排即换色。
type assistantBlock struct{ text string }

func (b assistantBlock) render(width int) []string {
	return renderMarkdown(b.text, width, &activeTheme)
}

// transcriptState 是 transcript 渲染关注点容器：语义块流 + 按块换行缓存
// （wrapCache，023 D6）+ bubbles viewport + 左键拖选/滚动条拖拽状态（023
// Story4 Task 21——几何与事件处理见本文件下半）。
type transcriptState struct {
	blocks    []block        // 语义块流（追加为主；运行中块经 rewriteLast 原地改写）
	wrapCache                // 换行缓存（字段与方法见 wrapcache.go；023 D6 移植）
	vp        viewport.Model // 转写视口（喂给用克隆行切片，见 feedViewportContent）
	sel       selection      // 左键拖选态（active 门控高亮与复制）
	// 滚动条拖拽态（scrollbarDrag 期间 motion 按行映射视口偏移；grabOffset
	// 保拇指相对位不跳变——见 grabRowOffset/dragScrollbar）。
	scrollbarDrag       bool
	scrollbarGrabOffset int
}

func newTranscriptState() transcriptState {
	return transcriptState{vp: viewport.New()}
}

// append 追加语义块——不触碰缓存：sync 的 suffix 路径自动覆盖 wrapBlockCount
// 之后的块（对照参考：append 不失效，sync 兜底）。
func (ts *transcriptState) append(b block) {
	ts.blocks = append(ts.blocks, b)
}

// rewriteLast 原地改写运行中块（流式/思考/工具尾——同一语义块的增量更新，
// 块数不变），并失效该块缓存（invalidateWrapFrom，对照参考 setTranscriptBlock
// → invalidateWrapFrom）——下次 sync 只重排此块。空块流防御性视为追加。
func (ts *transcriptState) rewriteLast(b block) {
	if len(ts.blocks) == 0 {
		ts.blocks = append(ts.blocks, b)
		return
	}
	ts.blocks[len(ts.blocks)-1] = b
	ts.invalidateWrapFrom(len(ts.blocks) - 1)
}

// lines 全量行：经换行缓存同步（增量——append 只重排后缀；宽度变更/历史收缩/
// 空缓存才全量重建）后返回展平行。返回内部缓存切片——消费方只读；viewport
// 喂给经 feedViewportContent 克隆。
func (ts *transcriptState) lines(width int) []string {
	ts.syncWrappedLines(width, false)
	return ts.wrappedLines
}

// syncViewport 按宽高同步转写视口：缓存 sync（增量）→ 行列表有变才克隆喂给。
// 不再无条件置底——钉底决策归模型层尾随状态机（023 D5：finalize/frame 按
// shouldFollowTail 钉底；上翻锁定下 offset 不得被拉回，见 scrollstate.go）。
// 返回行列表是否变化（viewport 已重新喂给）。克隆契约（对照参考
// wrap_cache.go:77-88）：viewport 持有传入切片本体，后续缓存改写/增长不得
// 波及已持有存储。
func (ts *transcriptState) syncViewport(width, height int) bool {
	ts.vp.SetWidth(width)
	ts.vp.SetHeight(height)
	if ts.syncWrappedLines(width, false) {
		ts.feedViewportContent()
		return true
	}
	return false
}

// ---- 左键拖选与滚动条拖拽（023 Story4 / Task 21，MIT 改编）----
//
// 本节改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）internal/cli/transcript.go：
// selPos/selection/caret 映射/selSpan/selectedText/scrollbarYOffset/inScrollbar
// 逐函数对照。adapted：
//  1. 接收者——参考方法挂 chatTUI；本仓选择/拖拽状态收敛为 transcriptState
//     字段（D4 骨架——transcript 关注点容器），几何方法挂 *transcriptState，
//     事件处理（需 modalOpen 门禁）挂 *tuiModel（文件尾）。
//  2. 复制面——参考 copyToClipboard 本地写失败回退 OSC 52；本仓 OSC 52 仅留
//     接口不实现（brief 钉定：SSH 专项非目标，executor Ruling 入台账）——
//     copyToClipboard 接缝即接口位，失败只留痕不回退。
//  3. 复制文本——参考有 copyRendered/math 语义重构面；本仓块流无该旁路，
//     统一走 selectedDisplayText 同型的显示文本提取（SGR 剥离 + 尾空格修剪）。
//  4. 边缘自动滚动 ticker（参考 autoScrollMsg）不移植——拖到可视边缘钳制在
//     窗口边（caret 钳制），跨窗选择留待真机反馈再议（非 brief 承诺）。

// selPos 选择锚点：绝对内容行下标 + 视觉列（滚动无关——滚动不移动锚点）。
type selPos struct{ line, col int }

// selection 左键拖选状态：anchor 拖选起点、head 当前头；active 门控高亮与
// 复制。坐标为绝对内容行——追加式内容不移动既有行下标（见 selectionTest
// 与报告「选区 vs 活内容」决定）。
type selection struct {
	active       bool
	anchor, head selPos
}

// ordered 返回按行列排序后的 [start, end)（anchor/head 无序进入）。
func (s selection) ordered() (start, end selPos) {
	if s.anchor.line > s.head.line || (s.anchor.line == s.head.line && s.anchor.col > s.head.col) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// empty 选区是否为空（anchor==head——单击未拖动）。
func (s selection) empty() bool { return s.anchor == s.head }

// selStyle 选区高亮（反显——对照参考 selStyle；主题槽随真机反馈再议）。
var selStyle = lipgloss.NewStyle().Reverse(true)

// caret 屏幕格 (x, y) → 绝对内容位（钳制在可见窗口内：y∈[0,h)、x∈[0,cw]）。
func (ts *transcriptState) caret(x, y int) selPos {
	h := ts.vp.Height()
	if y < 0 {
		y = 0
	}
	if y > h-1 {
		y = h - 1
	}
	if x < 0 {
		x = 0
	}
	if cw := ts.vp.Width(); x > cw {
		x = cw
	}
	return selPos{line: ts.vp.YOffset() + y, col: x}
}

// selSpan 选择在第 idx 内容行上的 [lo, hi) 视觉列区间（行不在选区内 ok=false）。
// cw 收口右界——多行选择高亮穿透到右缘（对照参考 selSpan）。
func selSpan(idx int, start, end selPos, cw int) (lo, hi int, ok bool) {
	if idx < start.line || idx > end.line {
		return 0, 0, false
	}
	lo, hi = 0, cw
	if idx == start.line {
		lo = start.col
	}
	if idx == end.line {
		hi = end.col
	}
	if hi > cw {
		hi = cw
	}
	if lo >= hi {
		return 0, 0, false
	}
	return lo, hi, true
}

// selectedText 活跃选区的纯文本（显示文本提取——SGR 剥离、行尾空格修剪、
// 行间 \n 连接；对照参考 selectedDisplayText）。非活跃/空选区返回 ""。
func (ts *transcriptState) selectedText() string {
	if !ts.sel.active || ts.sel.empty() {
		return ""
	}
	start, end := ts.sel.ordered()
	lines := ts.wrappedLines
	var out []string
	for idx := start.line; idx <= end.line && idx < len(lines); idx++ {
		w := ansi.StringWidth(lines[idx])
		lo, hi := 0, w
		if idx == start.line {
			lo = start.col
		}
		if idx == end.line {
			hi = end.col
		}
		if hi > w {
			hi = w
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(lines[idx], lo, hi)), " "))
	}
	return strings.Join(out, "\n")
}

// transcriptView 可见窗口渲染（含选区高亮）——对照参考 renderTranscript：
// viewport 只持状态，帧面由这里按 h 行绘制（行按宽补齐；选区行经 StyleRanges
// 反显）。拖拽每帧重渲染，本函数保持 O(h)。
func (ts *transcriptState) transcriptView() string {
	h := ts.vp.Height()
	cw := ts.vp.Width()
	if h <= 0 || cw <= 0 {
		return ""
	}
	lines := ts.wrappedLines
	total := len(lines)
	yoff := ts.vp.YOffset()
	start, end := ts.sel.ordered()
	blank := strings.Repeat(" ", cw)
	rows := make([]string, h)
	for r := range rows {
		idx := yoff + r
		line := blank // 窗口外/空区补满宽
		if idx >= 0 && idx < total {
			line = padToWidth(clampWidth(lines[idx], cw), cw) // 防御截断 + 补齐
		}
		if ts.sel.active && !ts.sel.empty() {
			if lo, hi, ok := selSpan(idx, start, end, cw); ok {
				line = lipgloss.StyleRanges(line, lipgloss.NewRange(lo, hi, selStyle))
			}
		}
		rows[r] = line
	}
	return strings.Join(rows, "\n")
}

// padToWidth 行按显示宽补齐到 w（短行补空格——StyleRanges 高亮与滚动条列对齐
// 都要求行等宽；对照参考 wrapTranscript 的等宽输出契约）。
func padToWidth(s string, w int) string {
	if d := w - ansi.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// inScrollbar (x, y) 是否落在滚动条列（末列、视口行范围内、且确有溢出——
// 无溢出滚动条列为空白，点击落转写选区）。
func (ts *transcriptState) inScrollbar(x, y int) bool {
	h := ts.vp.Height()
	return h > 0 && y >= 0 && y < h && x == ts.vp.Width() && len(ts.wrappedLines) > h
}

// grabRowOffset 滚动条拖拽的抓取偏移：点击落在 thumb 上取行内相对位（拇指
// 不跳变）；落在 track 上取 thumb 中点（对照参考 scrollbarGrabRowOffset）。
func (ts *transcriptState) grabRowOffset(row int) int {
	thumbStart, thumbSize := scrollbarThumb(ts.vp.Height(), ts.vp.YOffset(), len(ts.wrappedLines))
	if row >= thumbStart && row < thumbStart+thumbSize {
		return row - thumbStart
	}
	return thumbSize / 2
}

// scrollbarYOffset 滚动条行 → 视口偏移映射（抓取偏移保拇指相对位；四舍五入
// 让顶/底两端可达——对照参考 scrollbarYOffset）。
func scrollbarYOffset(height, row, total, grabOffset int) int {
	if total <= height {
		return 0
	}
	_, thumbSize := scrollbarThumb(height, 0, total)
	maxTop := height - thumbSize
	if maxTop <= 0 {
		return 0
	}
	top := min(max(row-grabOffset, 0), maxTop)
	maxYoff := total - height
	return (top*maxYoff + maxTop/2) / maxTop
}

// dragScrollbar 拖拽步进：按当前行映射视口偏移，并立即按落点归位尾随态
// （流式事件在拖拽间隙不得凭陈旧 followTail 把视口拉回底部——对照参考
// dragScrollbar 的 #6430/#6978 同步）。
func (m *tuiModel) dragScrollbar(row int) {
	ts := &m.transcriptState
	ts.vp.SetYOffset(scrollbarYOffset(ts.vp.Height(), row, len(ts.wrappedLines), ts.scrollbarGrabOffset))
	m.syncScrollModeAfterGesture()
}

// ---- 鼠标事件接线（模型层；治理独占门禁在前）----

// handleMouseClick 左键按下：滚动条列 → 开始拖拽（清活跃选区、记抓取偏移、
// 即刻步进）；转写区 → 武装新选区（anchor=head=落点）。其余（模态/非左键/
// 底部固定区）忽略。对照参考 MouseClickMsg 分支的左键路径（右/中键语义不移植
// ——brief 只钉左键拖选）。
func (m *tuiModel) handleMouseClick(msg tea.MouseClickMsg) {
	if m.modalOpen() {
		return
	}
	if msg.Button != tea.MouseLeft {
		return
	}
	ts := &m.transcriptState
	if ts.inScrollbar(msg.X, msg.Y) {
		ts.sel = selection{}
		ts.scrollbarDrag = true
		ts.scrollbarGrabOffset = ts.grabRowOffset(msg.Y)
		m.dragScrollbar(msg.Y)
		return
	}
	if msg.Y < ts.vp.Height() {
		ts.scrollbarDrag = false // 防御：丢失 release 的残拽不复燃
		at := ts.caret(msg.X, msg.Y)
		ts.sel = selection{active: true, anchor: at, head: at}
	}
}

// handleMouseMotion 拖拽步进（CellMotion 模式仅按键期间上报 motion——即拖拽）：
// 滚动条拖拽优先；其次延伸选区 head。
func (m *tuiModel) handleMouseMotion(msg tea.MouseMotionMsg) {
	if m.modalOpen() {
		return
	}
	ts := &m.transcriptState
	if ts.scrollbarDrag {
		m.dragScrollbar(msg.Y)
		return
	}
	if ts.sel.active {
		ts.sel.head = ts.caret(msg.X, msg.Y)
	}
}

// handleMouseRelease 左键松开：滚动条拖拽收口（终步进 + 清拖拽态）；拖选
// 收口——空选区（单击）清除，非空复制（松开即复制，终端惯例；高亮保留为
// 「已选」视觉残影，任意击键散去——keys.go handleKey）。模态期松开：手势被
// 治理门禁中止——清除不复制。返回复制命令（无则 nil）。
func (m *tuiModel) handleMouseRelease(msg tea.MouseReleaseMsg) tea.Cmd {
	if m.modalOpen() {
		m.transcriptState.sel = selection{}
		m.transcriptState.scrollbarDrag = false
		m.transcriptState.scrollbarGrabOffset = 0
		return nil
	}
	ts := &m.transcriptState
	if ts.scrollbarDrag {
		m.dragScrollbar(msg.Y)
		ts.scrollbarDrag = false
		ts.scrollbarGrabOffset = 0
		return nil
	}
	if msg.Button == tea.MouseLeft && ts.sel.active {
		if ts.sel.empty() {
			ts.sel = selection{}
			return nil
		}
		return m.copyCmd(ts.selectedText())
	}
	return nil
}

// copyToClipboard 剪贴板写入接缝（包级变量——测试替换；生产 = atotto/
// clipboard.WriteAll，FR-017 裁定的 clipboard 家族经此消费为直接依赖）。
// OSC 52 回退按 brief 钉定不实现（SSH 专项非目标）——本接口即降范围留位。
var copyToClipboard func(string) error = clipboard.WriteAll

// copyDoneMsg 异步剪贴板写入回执（T28 Critical-1 同纪律：Update 内不阻塞）。
type copyDoneMsg struct{ err error }

// copyCmd 异步写剪贴板（选区复制/Ctrl+C 复制档共用）。
func (m *tuiModel) copyCmd(text string) tea.Cmd {
	return func() tea.Msg { return copyDoneMsg{err: copyToClipboard(text)} }
}
