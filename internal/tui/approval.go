package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbletea/v2"
)

// ---- 授权卡（023 D11 / FR-010：键位对齐参考 + 参数摘要；治理语义逐字节不变）----
//
// 独占语义（Task 2/3 承接，本文件只动呈现与键位）：approval 非 nil 时 composer
// 冻结（输入与提交都被独占分支拦截，其余键不泄漏——handleKey 前置分流）；
// 120s 超时 fail-closed 在上游 guard/control，TUI 不计时不兜底。Ctrl+C 单击
// 退出（Minor-4 逃生口）在 handleKey 的 Ctrl+C 前置分支，先于本文件的键位表。

// approvalCard 待裁决工具请求（ApprovalRequestEvent 的卡片投影）：input 原样
// 留存供 inputSummary 按当前帧宽重算（重排跟宽，不重放事件）。
type approvalCard struct {
	toolUseID string
	tool      string
	input     json.RawMessage
}

// handleApprovalKey 授权键位（023 D11 对齐参考 chat_tui.go handleApprovalKey）：
// 1/y/Enter 允许一次、2/a 本会话允许、n/Esc 拒绝（逃生等价）。映射只认裸键
// （对照参考 msg.String() 的形态判定——Ctrl+A/Y/N 等修饰组合不得误触授权
// 动作，尤其 Ctrl+C 旁滑的 Ctrl+A 等价于全会话放行）；未映射键一律忽略
// （独占态不泄漏）。Enter 仅在授权卡在场时映射为允许——独占分支先于渲染态
// 提交分流，永不触达 Submit。
func (m *tuiModel) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Mod != 0 { // 修饰组合非授权键位——忽略（fail-closed）
		return m, nil
	}
	switch msg.Code {
	case '1', 'y':
		m.resolve(m.approval.toolUseID, true, false)
	case '2', 'a':
		m.resolve(m.approval.toolUseID, true, true)
	case 'n', tea.KeyEscape:
		m.resolve(m.approval.toolUseID, false, false)
	case tea.KeyEnter:
		m.resolve(m.approval.toolUseID, true, false)
	}
	return m, nil
}

// resolve 执行审批动作并立即清卡（回执事件随后到达时幂等跳过）。治理语义
// （Task 2/3 原样迁移）：Approve/ApproveSession 按 by="user" 打控制面；失败
// 留痕 [审批失败]；卡片即刻清除。
func (m *tuiModel) resolve(toolUseID string, allow, session bool) {
	var err error
	if session {
		err = m.ctrl.ApproveSession(toolUseID, "user")
	} else {
		err = m.ctrl.Approve(toolUseID, allow, "user")
	}
	if err != nil {
		m.appendLine("[审批失败] " + err.Error())
	}
	m.approval = nil
}

// approvalCardView 授权卡文本（layout.go bottomView 独占分支调用）：头行
// 工具 <tool>（id8）+ 参数摘要（inputSummary ≤2 行按帧宽）+ 键位提示行。键位
// 提示行键名走键帽样式（设计 §6 授权卡——styleKeycap；键位与文本语义逐字节
// 不变，仅样式——TestApprovalCardDress 钉剥 SGR 后与现值全等）。
func (m *tuiModel) approvalCardView() string {
	w := m.width - 4 // styleCard 横向装饰：RoundedBorder 左右各 1 + Padding(0,1) 左右各 1
	if w < 16 {
		w = 16
	}
	var b strings.Builder
	fmt.Fprintf(&b, "工具 %s（%s）等待授权", m.approval.tool, shortID(m.approval.toolUseID))
	if s := inputSummary(m.approval.input, w); s != "" {
		b.WriteString("\n" + s)
	}
	b.WriteString("\n" + styleKeycap.Render("[1/y/Enter]") + " 本次允许  " +
		styleKeycap.Render("[2/a]") + " 本会话允许  " +
		styleKeycap.Render("[n/Esc]") + " 拒绝")
	return b.String()
}

// ---- 参数摘要（FR-010：Input JSON 平铺截断）----

// summaryValueCap 单值缩略上限（rune 数）：超限截断加省略号——单个长参数
// 不得吃满整卡两行预算（对照参考 #4682：截断藏住关键字段反成盲区，故先按
// 值收敛、再按整卡折行）。
const summaryValueCap = 40

// inputSummary 把工具 Input JSON 平铺为紧凑 key:value 摘要：顶层键排序拼接
// （Go map 无序——摘要必须确定性）；字符串值去引号直读，嵌套值紧凑 JSON；
// 长值缩略；整体按宽 w 折行至 ≤2 行，溢出以省略号收尾。空/非法 JSON 返回
// 空串（无摘要行——呈现层降级不硬失败：授权裁决语义不因摘要失败改变）。
func inputSummary(raw json.RawMessage, w int) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+":"+summaryValue(obj[k]))
	}
	return wrapSummary(strings.Join(pairs, ", "), w)
}

// summaryValue 单参数值渲染：字符串去引号直读（参数摘要按人读优化）；数字
// /布尔按标量；嵌套结构紧凑 JSON（键序由 encoding/json 归一）。
func summaryValue(v any) string {
	switch t := v.(type) {
	case string:
		return abbrevRunes(t, summaryValueCap)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return "null"
	default:
		return abbrevRunes(compactJSON(t), summaryValueCap)
	}
}

// compactJSON 任意 JSON 值的紧凑编码（禁 HTML 转义——命令行里的 > < & 原样可读）。
func compactJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n") // Encoder 追加换行
}

// abbrevRunes 按 rune 截断（CJK 安全）：超限保留前 n-1 rune 加省略号。
func abbrevRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// wrapSummary 硬折行至 ≤2 行（键值对连续体不逐词回绕——词界折行会把单键
// 拉成多行破版）：溢出 2 行时第二行截宽-1 加省略号。
func wrapSummary(s string, w int) string {
	if w < 1 {
		w = 1
	}
	r := []rune(s)
	var lines []string
	for len(r) > w {
		lines = append(lines, string(r[:w]))
		r = r[w:]
	}
	lines = append(lines, string(r))
	if len(lines) > 2 {
		tail := []rune(lines[1])
		lines = lines[:2]
		lines[1] = string(tail[:w-1]) + "…"
	}
	return strings.Join(lines, "\n")
}
