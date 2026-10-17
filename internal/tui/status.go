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

// statusView 双行状态栏（bottomView 尾部消费；行内槽色渲染期自持——styleStatus
// 外层 dim 仍由调用方施加作兜底，refreshThemeStyles 重铸，T18）。
func (m *tuiModel) statusView() string {
	s := m.statusState.snapshot
	// 行 1（025 FR-006 + 2026-10-17 换装设计 §6）：模式徽章 pill 居左（accent
	// 底 + bg 槽 fg——styleBadge 既有机制；引擎就绪中/异常 amber/red 语义底，
	// 见 statusBadge）+「模型 <model>」右对齐（fg-secondary）；会话 id8 退场
	//（对照基线行 1 无会话标识）。
	right := ""
	if s.Model != "" {
		right = sgrWrap(sgrFg(activeTheme.fgSecondaryColor), "模型 "+s.Model)
	}
	row1 := joinStatusSides(m.statusBadge(m.statusMode()), right, m.width)
	// 行 2（025 FR-006）：缓存命中率（本次 = TurnTokens.CacheRead/(Input+Write
	// +Read)，与 Meter.Hit 同式 cache/meter.go:145-146；平均 = SessionHit）+
	// 费用；上下文/压缩/余额段无数据源省略（D7）。
	tt := s.TurnTokens
	hitDen := tt.InputTokens + tt.CacheWriteTokens + tt.CacheReadTokens
	turnHit := 0.0
	if hitDen > 0 {
		turnHit = float64(tt.CacheReadTokens) / float64(hitDen) * 100
	}
	groups := []string{
		fmt.Sprintf("缓存 本次命中 %.1f%%", turnHit),
		fmt.Sprintf("平均 %.1f%%", s.MeterReport.SessionHit*100),
		fmt.Sprintf("费用 $%.4f", s.MeterReport.Cost),
	}
	if s.MeterReport.ColdTurns > 0 {
		groups = append(groups, fmt.Sprintf("❄冷回合 %d", s.MeterReport.ColdTurns))
	}
	// 行 2 数据带 caption 色（设计令牌表 caption/dim 同槽——dim；显式施色使
	// 渲染点自持，不依赖调用方 styleStatus；空槽原样返回）。
	return row1 + "\n" + sgrWrap(sgrFg(activeTheme.dimColor), strings.Join(groups, " · "))
}

// statusBadge 行 1 徽章渲染：交互态走 styleBadge（accent 底 + bg 槽 fg——
// 025 FR-006 既有机制，逐字节不变）；引擎就绪中 = amber（yellow 槽）、引擎
// 异常 = red（red 槽）——FR-003 状态语义色经 dsh 状态令牌不弱化（设计 §6
// 状态栏），pill 形态（两端空格）与文本不变；语义槽空（无色层）退既有机制。
func (m *tuiModel) statusBadge(label string) string {
	var slot themeColor
	switch m.statusState.snapshot.Ready {
	case "FAILED":
		slot = activeTheme.redColor
	case "NOT_READY":
		slot = activeTheme.yellowColor
	default:
		return styleBadge.Render(" " + label + " ")
	}
	bg := sgrBg(slot)
	if bg == "" {
		return styleBadge.Render(" " + label + " ")
	}
	params := bg
	if fg := sgrFg(activeTheme.bgColor); fg != "" {
		params += ";" + fg
	}
	return sgrWrap(params, " "+label+" ")
}

// statusMode 行 1 左侧交互态：治理独占态优先（与 handleKey 的独占层级一致）
// → 换绑/运行 → 空闲。
func (m *tuiModel) statusMode() string {
	// 引擎就绪态徽章（027 FR-003）：FAILED 红色 > NOT_READY「引擎就绪中」
	// amber > 交互态（语义底色见 statusBadge——2026-10-17 换装设计 §6；READY
	// 落入下方既有交互态分支）。
	switch m.statusState.snapshot.Ready {
	case "FAILED":
		return "引擎异常"
	case "NOT_READY":
		if m.statusState.snapshot.Running {
			return "运行中·引擎就绪中"
		}
		return "引擎就绪中"
	}
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

// line 回执行（025 FR-005，形态对照基线 `本轮  9.6K tok · in 8.1K · cached
// 7.6K · new 541 · reasoning 1.2K · ≈¥0.0066`）：字段名/次序/分隔对齐
// reasonix，数值取 mulix UsageEvent 真实字段映射（in=UncachedInputTokens、
// cached=ReadTokens、new=WriteTokens、out=OutputTokens、reasoning=
// ThinkingTokens）；币种保持 mulix 的 $。零值为 Meter 真实记账，如实渲染。
// 人性化计数（≥1000 → %.1fK）对照基线排版。
func (b receipt) line() string {
	u := b.usage
	total := u.UncachedInputTokens + u.WriteTokens + u.ReadTokens + u.OutputTokens + u.ThinkingTokens
	return fmt.Sprintf("本轮  %s tok · in %s · cached %s · new %s · out %s · reasoning %s · ≈$%.4f",
		humanizeTok(total), humanizeTok(u.UncachedInputTokens), humanizeTok(u.ReadTokens),
		humanizeTok(u.WriteTokens), humanizeTok(u.OutputTokens), humanizeTok(u.ThinkingTokens),
		u.CostUSD)
}

// humanizeTok token 计数人性化（对照基线 9.6K 排版）：≥1000 → %.1fK，其余
// 原样整数。
func humanizeTok(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// render 回执块按宽渲染（块接口）：首行为通栏分隔线（对照基线回执上缘
// `─────`），次行回执正文——分隔线是 receipt 块的首行而非独立块（024 A4：
// 块拓扑稳定）。逐行弱化（历史留痕安静色阶）。
func (b receipt) render(width int) []string {
	inner := max(width-2, 1)
	lines := wrapText(b.line(), inner)
	out := []string{"  " + dimLine(strings.Repeat("─", inner))}
	for _, l := range lines {
		out = append(out, "  "+dimLine(l))
	}
	return out
}
