package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"mulix/internal/event"
)

// ---- 状态栏与回合回执（023 FR-012 / D16 / D7）----
//
// 版式对照 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors）
// internal/cli/status_footer.go 的布局思想（改编，非整段移植）：
//   - 行 1 = 交互态（左）+ 模型/会话身份（右，宽端右对齐——对照
//     layoutStatusSides 的左右合成）；
//   - 行 2 = 数据带（参考为 git 身份 + 遥测组；mulix 用 Snapshot 可得字段）。
//
// D7 状态数据（兜底纪律，TestContextGaugeFallback 钉）：全部字段来自 500ms
// Snapshot 轮询快照；快照没有的字段整体省略，绝不造数——
//   - 上下文仪表（参考 renderContextStatusGroups 的 `tokens (pct)`）：现版
//     control.Snapshot（controller.go:1042）无上下文 token 字段，不渲染；
//   - 工作区段（参考行 2 的 git repo/branch）：Snapshot.Dir 是会话镜像目录，
//     其基名即会话 id（sessionx 镜像 <root>/<id>）——与行 1 右侧 id8 重复，
//     无独立工作区字段，一并省略。

// statusView 双行状态栏（bottomView 尾部消费；styleStatus 由调用方施加——
// 样式随主题 dim 槽派生，refreshThemeStyles 重铸，T18）。
func (m *tuiModel) statusView() string {
	s := m.statusState.snapshot
	right := shortID(m.selfID)
	if s.Model != "" {
		right = s.Model + " · " + right
	}
	row1 := joinStatusSides(m.statusMode(), right, m.width)
	groups := []string{
		fmt.Sprintf("命中率 %.1f%%", s.MeterReport.SessionHit*100),
		// 回合累计：回合号 + 本回合 tokens（口径同迁移前状态栏——ThinkingTokens
		// 为 output 细目，回试行单独呈现，此处不重计）。
		fmt.Sprintf("回合 t%d %d tok", s.Turn, s.TurnTokens.InputTokens+s.TurnTokens.CacheWriteTokens+
			s.TurnTokens.CacheReadTokens+s.TurnTokens.OutputTokens),
		fmt.Sprintf("成本 $%.4f", s.MeterReport.Cost),
	}
	if s.MeterReport.ColdTurns > 0 {
		groups = append(groups, fmt.Sprintf("❄冷回合 %d", s.MeterReport.ColdTurns))
	}
	return row1 + "\n" + strings.Join(groups, " · ")
}

// statusMode 行 1 左侧交互态：治理独占态优先（与 handleKey 的独占层级一致）
// → 换绑/运行 → 空闲。
func (m *tuiModel) statusMode() string {
	switch {
	case m.approval != nil:
		return "审批中"
	case m.agentsOpen:
		return "agents 面板"
	case m.picker != nil:
		return "resume 选择"
	case m.swapping:
		return "换绑中"
	case m.statusState.snapshot.Running:
		return "运行中"
	default:
		return "空闲"
	}
}

// joinStatusSides 行 1 左右合成（对照参考 layoutStatusSides）：宽度足够时右组
// 贴行尾；窄端退化为「左 + 两空格 + 右」不折行——状态行行数由 frame 经
// lipgloss.Height 计量，折行会顶压转写视口，窄端牺牲对齐保行数稳定。
func joinStatusSides(left, right string, width int) string {
	if right == "" {
		return left
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left + "  " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---- 回合回执（D16）----

// receipt 回执块：UsageEvent 到达即在该回合答案下方渲染的用量明细行（迁移前
// TUI 忽略 UsageEvent；状态栏照旧走 Snapshot 轮询——两者并存不冲突）。字段
// 口径 = Task 13 钉死的 UsageEvent：UncachedInputTokens/WriteTokens/ReadTokens/
// OutputTokens/ThinkingTokens/CostUSD/HitRate（Write = cache_creation，与
// Output 相互独立；HitRate 为 0..1 分数）。
type receipt struct {
	turn  int64
	usage event.UsageEvent
}

// line 回试行（设计 §6）：`✓ 回合 tN · 入N/写N/读N/出N/思N tok · $0.xxxx ·
// 命中N%`。零值为 Meter 真实记账（引擎未报 usage 的回合），如实渲染。
func (b receipt) line() string {
	u := b.usage
	return fmt.Sprintf("✓ 回合 t%d · 入%d/写%d/读%d/出%d/思%d tok · $%.4f · 命中%.0f%%",
		b.turn, u.UncachedInputTokens, u.WriteTokens, u.ReadTokens, u.OutputTokens,
		u.ThinkingTokens, u.CostUSD, u.HitRate*100)
}

// render 回试行按宽渲染（块接口）：先按宽换行再逐行弱化（思考块同法——回执
// 是历史留痕，用比运行态安静的色阶，对照参考 renderTurnReceipt 的 quieter
// palette 注记）。
func (b receipt) render(width int) []string {
	lines := wrapText(b.line(), width)
	for i, l := range lines {
		lines[i] = dimLine(l)
	}
	return lines
}
