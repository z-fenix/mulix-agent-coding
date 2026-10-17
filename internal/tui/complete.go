package tui

import (
	"strings"

	"charm.land/bubbletea/v2"
)

// ---- 斜杠补全菜单（023 FR-009，语义对照参考 DeepSeek-Reasonix complete.go）----
//
// 触发面：编辑器整行是纯 "/词" 记号（无空白）时按输入过滤命令目录；出现空白
//（词界）或离开 "/" 前缀即收口。↑/↓ 环绕选择；Tab/Enter 补全——整行替换为
//「命令名 + 尾随空格」（尾随空格出词范围，菜单经 syncComplete 收口，下一次
// Enter 即常规提交）；Enter 消歧（参考 chat_tui.go:1433-1444）：选中项与输入
// 完全一致 → 关菜单落回常规 Enter 直接提交（不重复补全）；Esc 关闭（忙碌时
// 落回取消阶梯，参考 chat_tui.go:1454-1456）。≤8 行窗口随选中项滚动。
// 菜单是 composer 邻接浮层（非独占态）：编辑继续、其余键落 textarea 后重过滤
//（对照参考 chat_tui.go:1424-1459 的消费/透传分流）。

// maxCompleteRows 补全菜单可见行数上限（对照参考 maxCompRows = 8）。
const maxCompleteRows = 8

// slashCmd 命令目录条目：name 补全命令名（含斜杠），desc 一句话用途。
type slashCmd struct {
	name string
	desc string
}

// slashCatalog 命令目录（当前命令集；新命令须同步 slash() 分发与未知命令
// 回退行的清单，目录只负责可见性）。
var slashCatalog = []slashCmd{
	{"/new", "开新会话"},
	{"/resume", "续链会话（无参弹选择器）"},
	{"/cache", "缓存命中率与成本汇总"},
	{"/doctor", "环境自检"},
	{"/agents", "子代理列表"},
	{"/rewind", "引擎 checkpoint 回退（占位）"},
	{"/theme", "切换配色主题（graphite/ember/aurora/midnight[+ -light]）"},
	{"/quit", "退出 mulix"},
}

// completeMenu 补全菜单状态（brief 签名）：items 过滤后的候选命令名（目录
// 序），sel 选中下标，query 当前过滤词（斜杠后文本）。items 空 = 菜单关闭
// （零值即关闭态——过滤空集即收口的另一面）。
type completeMenu struct {
	items []string
	sel   int
	query string
}

// completeOpen 报告菜单是否打开。
func (m *tuiModel) completeOpen() bool { return len(m.complete.items) > 0 }

// closeComplete 收口菜单（状态清零——下次触发从全目录、sel=0 重开）。
func (m *tuiModel) closeComplete() { m.complete = completeMenu{} }

// syncComplete 按当前编辑器全文重算菜单（每个 composer 变更点之后调用——
// 对照参考 updateCompletion 在 Update 末尾统一重过滤）：整行纯 "/词" 记号且
// 过滤非空才打开；其余形态一律收口。
func (m *tuiModel) syncComplete() {
	val := m.composerValue()
	if !strings.HasPrefix(val, "/") || strings.ContainsAny(val, " \t\n") {
		m.closeComplete()
		return
	}
	items := filterSlash(slashCatalog, val)
	if len(items) == 0 {
		m.closeComplete()
		return
	}
	// 过滤收窄时沿用选中项（参考 setCompletion：sel 仍在界内才保留，否则
	// 复位首项）。
	sel := 0
	if m.complete.sel < len(items) {
		sel = m.complete.sel
	}
	m.complete = completeMenu{items: items, sel: sel, query: val[1:]}
}

// filterSlash 目录过滤（对照参考 fuzzyFilterSlash）：大小写不敏感前缀命中
// 优先，其余按子序列命中（query 各字符按序出现即可——"/dc" 命中 /doctor），
// 各组内保持目录序；无命中返回 nil（调用方据此收口）。
func filterSlash(catalog []slashCmd, query string) []string {
	lq := strings.ToLower(query)
	var prefix, rest []string
	for _, c := range catalog {
		l := strings.ToLower(c.name)
		switch {
		case strings.HasPrefix(l, lq):
			prefix = append(prefix, c.name)
		case subsequenceMatch(l, lq):
			rest = append(rest, c.name)
		}
	}
	return append(prefix, rest...) // 双空 = nil（无命中）
}

// subsequenceMatch 大小写已折叠的子序列判定（对照参考 subsequenceMatch）；
// 空 query 恒真（调用方保证 query 以 "/" 起头非空，防御面兜底）。
func subsequenceMatch(target, query string) bool {
	if query == "" {
		return true
	}
	qr := []rune(query)
	ti := 0
	for _, r := range target {
		if r == qr[ti] {
			ti++
			if ti == len(qr) {
				return true
			}
		}
	}
	return false
}

// moveComplete 环绕移动选中项（对照参考 moveCompletion 取模环绕——末项 ↓ 回
// 首项、首项 ↑ 到末项）。
func (m *tuiModel) moveComplete(delta int) {
	n := len(m.complete.items)
	if n == 0 {
		return
	}
	m.complete.sel = ((m.complete.sel+delta)%n + n) % n
}

// completeExact 报告选中项与输入完全一致（Enter 消歧凭据——参考
// completionExactLabel）。
func (m *tuiModel) completeExact() bool {
	if m.complete.sel >= len(m.complete.items) {
		return false
	}
	return strings.TrimSpace(m.composerValue()) == m.complete.items[m.complete.sel]
}

// acceptComplete 应用选中项：整行替换为「命令名 + 尾随空格」、光标落尾
// （菜单仅在整行纯记号时激活——替换跨即整行；尾随空格出词范围，setComposerValue
// 内的 syncComplete 随即收口，下一次 Enter 走常规提交）。
func (m *tuiModel) acceptComplete() {
	if m.complete.sel >= len(m.complete.items) {
		m.closeComplete()
		return
	}
	m.setComposerValue(m.complete.items[m.complete.sel] + " ")
}

// handleCompleteKey 菜单激活期的键位拦截：↑/↓/Tab/Enter/Esc 在此消费；其余
// 键返回 consumed=false 落 textarea 并由调用方重过滤。Enter 精确命中与忙碌
// Esc 返回 false 落回主阶梯（提交/取消）——菜单已先行收口。
func (m *tuiModel) handleCompleteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case msg.Code == tea.KeyUp:
		m.moveComplete(-1)
		return m, nil, true
	case msg.Code == tea.KeyDown:
		m.moveComplete(+1)
		return m, nil, true
	case msg.Code == tea.KeyTab:
		m.acceptComplete()
		return m, nil, true
	case msg.Code == tea.KeyEnter && msg.Mod&(tea.ModAlt|tea.ModShift) == 0:
		if m.completeExact() {
			// 消歧：已完整 → 关菜单落回常规 Enter 提交（参考
			// chat_tui.go:1433 exact-label 语义）。
			m.closeComplete()
			return m, nil, false
		}
		m.acceptComplete()
		return m, nil, true
	case msg.Code == tea.KeyEscape:
		m.closeComplete()
		if m.running {
			return m, nil, false // 忙碌：落回取消阶梯（参考 chat_tui.go:1454）
		}
		return m, nil, true
	}
	return m, nil, false
}

// completeMenuView 渲染菜单（composer 上方，对照参考 renderCompletion "draws
// the menu above the input box"——无框行列表紧贴输入行）：≤8 行窗口随选中项
// 滚动、选中行 ▸ 标记、描述与键位提示弱化。关闭态返回空串（bottomView 不占行）。
func (m *tuiModel) completeMenuView() string {
	if !m.completeOpen() {
		return ""
	}
	descs := make(map[string]string, len(slashCatalog))
	for _, c := range slashCatalog {
		descs[c.name] = c.desc
	}
	items := m.complete.items
	start := 0
	if len(items) > maxCompleteRows {
		start = min(max(m.complete.sel-maxCompleteRows/2, 0), len(items)-maxCompleteRows)
	}
	end := min(start+maxCompleteRows, len(items))
	var b strings.Builder
	for i := start; i < end; i++ {
		if i == m.complete.sel {
			// accent 选中（设计 §6 补全菜单——surface1 底 + accent 选中）。
			b.WriteString(activeTheme.accent("▸ " + items[i]))
		} else {
			b.WriteString("  " + items[i])
		}
		if d := descs[items[i]]; d != "" {
			b.WriteString("  " + dimLine(d))
		}
		b.WriteString("\n")
	}
	b.WriteString(dimLine("[↑/↓] 选择  [tab/enter] 补全  [esc] 关闭"))
	// surface1 菜单底（设计 §6——无框浮层整块铺面，渲染点自铺；行内 dim 重置
	// 点由 paintSurface 重铺自愈，与帧底合成序同构）。空槽（无色层）原样返回。
	return paintSurface(b.String(), activeTheme.surface1Color)
}
