package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"mulix/internal/event"
)

// ---- 授权卡治理测试（Task 16 FR-010：自 tui_test.go 迁入并升级键位 023 D11）----
//
// 迁移纪律：断言语义与迁移前逐条相同（独占冻结、拒绝/会话/允许三动作、裁决
// 留痕行、Ctrl+C 逃生）——只换键位：1/y/Enter=允许一次、2/a=本会话允许、
// n/Esc=拒绝。治理本体（独占分流、resolve 调用形态、120s 超时上游）不动。

// TestApprovalExclusiveState pins（审批态/渲染态互斥）：审批卡可见时 composer
// 输入被忽略（按键不进 input）；Enter 在审批态映射为允许一次（独占分支先行
// ——绝不触达 Submit）；结算清卡回渲染态后输入恢复。
func TestApprovalExclusiveState(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{
		Seq: 1, ToolUseID: "toolu-1", Tool: "Write",
		Input: json.RawMessage(`{"path":"a.go"}`),
	}})
	if m.approval == nil {
		t.Fatal("approval card must show on request")
	}
	// 卡片呈现（FR-010）：键位提示 + 参数摘要
	view := m.View().Content
	if !strings.Contains(view, "本次允许") {
		t.Fatalf("approval card must render actions:\n%s", view)
	}
	if !strings.Contains(view, "path:a.go") {
		t.Fatalf("approval card must render input summary:\n%s", view)
	}
	// 互斥：审批态下输入不进 composer
	before := m.composerValue()
	m = update(t, m, keyPress('x'))
	if m.composerValue() != before {
		t.Fatalf("input changed in approval state: %q", m.composerValue())
	}
	// 互斥：Enter 是允许键（不提交）——Submit 必须保持 0 调用
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 0 {
		t.Fatalf("submit called in approval state: %v", stub.submits)
	}
	if len(stub.approves) != 1 || stub.approves[0] != [3]string{"toolu-1", "true", "user"} {
		t.Fatalf("approves = %v", stub.approves)
	}
	if m.approval != nil {
		t.Fatal("card must clear after resolve")
	}
	// 回渲染态：输入恢复
	m = update(t, m, keyPress('y'))
	if m.composerValue() != "y" {
		t.Fatalf("input = %q, want y", m.composerValue())
	}
}

// TestApprovalDenyAndSession pins：n=拒绝、Esc=拒绝（逃生等价）、2/a=本会话
// 允许（数字直选与字母等价两映射都钉）；裁决回执幂等清卡 + [裁决] 行格式
// 原样；Ctrl+C 在审批态可退出（Minor-4）。
func TestApprovalDenyAndSession(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t2", Tool: "Bash"}})
	m = update(t, m, keyPress('n'))
	if len(stub.approves) != 1 || stub.approves[0][1] != "false" {
		t.Fatalf("deny not recorded: %v", stub.approves)
	}
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t3", Tool: "Edit"}})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(stub.approves) != 2 || stub.approves[1][1] != "false" {
		t.Fatalf("esc deny not recorded: %v", stub.approves)
	}
	// 裁决回执：幂等清卡 + [裁决] id → decision/source 留痕行（格式原样）
	m = update(t, m, tuiEvent{ev: event.ApprovalResolvedEvent{Seq: 2, ToolUseID: "t3", Decision: "deny", Source: "user"}})
	if m.approval != nil {
		t.Fatal("resolved card must clear")
	}
	if !strings.Contains(m.View().Content, "[裁决] t3 → deny/user") {
		t.Fatalf("resolved line missing:\n%s", m.View().Content)
	}
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t4", Tool: "Write"}})
	m = update(t, m, keyPress('2'))
	if len(stub.sessionGrants) != 1 || stub.sessionGrants[0][0] != "t4" {
		t.Fatalf("session grant not recorded: %v", stub.sessionGrants)
	}
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t5", Tool: "Write"}})
	m = update(t, m, keyPress('a'))
	if len(stub.sessionGrants) != 2 || stub.sessionGrants[1][0] != "t5" {
		t.Fatalf("session grant not recorded: %v", stub.sessionGrants)
	}
	// Ctrl+C 审批态逃生
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t6", Tool: "Bash"}})
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("Ctrl+C in approval state must quit")
	}
}

// TestApprovalKeysFull pins 全键位映射表（023 D11）：1/y/Enter→允许一次、
// 2/a→本会话允许、n/Esc→拒绝；未映射键（含数字 3 与普通字符）不结算卡片、
// 不触达控制面——独占态不泄漏。
func TestApprovalKeysFull(t *testing.T) {
	cases := []struct {
		name    string
		key     tea.KeyPressMsg
		allow   string // "true"/"false" = Approve allow；"" = 不走 Approve
		session bool
	}{
		{"1 允许一次", keyPress('1'), "true", false},
		{"y 允许一次", keyPress('y'), "true", false},
		{"enter 允许一次", tea.KeyPressMsg{Code: tea.KeyEnter}, "true", false},
		{"2 本会话允许", keyPress('2'), "", true},
		{"a 本会话允许", keyPress('a'), "", true},
		{"n 拒绝", keyPress('n'), "false", false},
		{"esc 拒绝", tea.KeyPressMsg{Code: tea.KeyEscape}, "false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, stub, _ := newTestModel(t)
			m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "tk", Tool: "Bash"}})
			m = update(t, m, tc.key)
			if m.approval != nil {
				t.Fatalf("%s: card must clear after mapped key", tc.name)
			}
			if tc.session {
				if len(stub.sessionGrants) != 1 || stub.sessionGrants[0][0] != "tk" || stub.sessionGrants[0][1] != "user" {
					t.Fatalf("%s: sessionGrants = %v", tc.name, stub.sessionGrants)
				}
				if len(stub.approves) != 0 {
					t.Fatalf("%s: session key must not Approve: %v", tc.name, stub.approves)
				}
			} else {
				if len(stub.approves) != 1 || stub.approves[0] != [3]string{"tk", tc.allow, "user"} {
					t.Fatalf("%s: approves = %v", tc.name, stub.approves)
				}
				if len(stub.sessionGrants) != 0 {
					t.Fatalf("%s: must not ApproveSession: %v", tc.name, stub.sessionGrants)
				}
			}
		})
	}
	// 未映射键不泄漏：不结算卡片、不触达控制面
	for _, k := range []rune{'3', 'x'} {
		m, stub, _ := newTestModel(t)
		m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "tk", Tool: "Bash"}})
		m = update(t, m, keyPress(k))
		if m.approval == nil {
			t.Fatalf("unmapped key %q must not resolve the card", k)
		}
		if len(stub.approves)+len(stub.sessionGrants) != 0 {
			t.Fatalf("unmapped key %q must not call the controller", k)
		}
	}
	// 修饰组合不误触授权（裸键判定）：Ctrl+A 不得等价于本会话放行
	//（Ctrl+C 逃生口的旁滑组合——全会话授权动作必须只能裸键触发）
	m, stub, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "tk", Tool: "Bash"}})
	m = update(t, m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.approval == nil {
		t.Fatal("ctrl+a must not resolve the card")
	}
	if len(stub.approves)+len(stub.sessionGrants) != 0 {
		t.Fatalf("ctrl+a must not approve session: %v %v", stub.approves, stub.sessionGrants)
	}
}

// TestApprovalInputSummary pins 参数摘要（FR-010）：Input JSON 平铺为紧凑
// key:value（键排序确定性）、字符串值去引号、嵌套值紧凑 JSON、长值缩略、
// 整体按宽折行 ≤2 行溢出加省略号；空/非法 JSON 降级为空摘要（不硬失败）。
func TestApprovalInputSummary(t *testing.T) {
	// 空 / 非法 JSON → 空摘要（无摘要行，授权裁决语义不受影响）
	if got := inputSummary(nil, 40); got != "" {
		t.Fatalf("nil input must summarize empty, got %q", got)
	}
	if got := inputSummary(json.RawMessage(`not-json`), 40); got != "" {
		t.Fatalf("invalid JSON must summarize empty, got %q", got)
	}
	// 平铺 key:value，键排序（Go map 无序——输出必须确定）
	if got, want := inputSummary(json.RawMessage(`{"b":2,"a":true}`), 80), "a:true, b:2"; got != want {
		t.Fatalf("flat summary = %q, want %q", got, want)
	}
	// 字符串值去引号直读
	if got, want := inputSummary(json.RawMessage(`{"command":"go test ./..."}`), 80), "command:go test ./..."; got != want {
		t.Fatalf("string value = %q, want %q", got, want)
	}
	// 嵌套值紧凑 JSON（禁 HTML 转义——命令原样可读；map 键序归一）
	if got, want := inputSummary(json.RawMessage(`{"edits":[{"old":"x","new":"y"}]}`), 200), `edits:[{"new":"y","old":"x"}]`; got != want {
		t.Fatalf("nested value = %q, want %q", got, want)
	}
	// 长值缩略（单值上限内截断加省略号——单键不得吃满两行预算）
	long := strings.Repeat("x", 60)
	got := inputSummary(json.RawMessage(`{"command":"`+long+`"}`), 80)
	if want := "command:" + strings.Repeat("x", 39) + "…"; got != want {
		t.Fatalf("abbrev value = %q (%d runes), want %q", got, len([]rune(got)), want)
	}
	// 宽度折行 ≤2 行：两长值超两行预算 → 第二行截宽-1 加省略号
	two := `{"a":"` + strings.Repeat("x", 60) + `","b":"` + strings.Repeat("y", 60) + `"}`
	got = inputSummary(json.RawMessage(two), 40)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("wrapped lines = %d, want 2: %q", len(lines), got)
	}
	for _, l := range lines {
		if len([]rune(l)) > 40 {
			t.Fatalf("line over width: %q (%d runes)", l, len([]rune(l)))
		}
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated summary must end with ellipsis: %q", got)
	}
}

// ---- 授权卡换装（2026-10-17-dsh-tui-visual-redesign Task 7；设计 §6 授权卡）----

// TestApprovalCardDress 钉：accent 边框（styleCard 既有——不变量钉）+ surface1
// 卡底（48;5;235）+ 键帽 styleKeycap（surface3 底 48;5;237 + fgSecondary 前
// 38;5;252）；键位行文本剥 SGR、掐边框/内衬后与现值逐字节相等——治理语义与
// 文案零改动（仅样式；本断言为不变量钉，换装前后都必须绿）。
func TestApprovalCardDress(t *testing.T) {
	resetTheme(t)
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{
		Seq: 1, ToolUseID: "toolu-1", Tool: "Write",
		Input: json.RawMessage(`{"path":"a.go"}`),
	}})
	card := m.bottomView()
	if !strings.Contains(card, "\x1b[48;5;235m") { // surface1 卡底
		t.Fatalf("approval card must take the surface1 slot:\n%s", card)
	}
	if !strings.Contains(card, "\x1b[38;5;173m") { // accent 边框（既有机制）
		t.Fatalf("approval card must keep the accent border:\n%s", card)
	}
	if !strings.Contains(card, "48;5;237") { // 键帽 surface3 底（lipgloss 合并序 fg;bg——无 ESC 前缀钉，TestStyleKeycap 同法）
		t.Fatalf("approval keycaps must take the surface3 slot:\n%s", card)
	}
	if !strings.Contains(card, "38;5;252") { // 键帽 fgSecondary 前（合并序同上）
		t.Fatalf("approval keycaps must take the fgSecondary slot:\n%s", card)
	}
	// 键位行文本逐字节不变（仅样式换装——键位行含两组双空格分隔）。
	const keymap = "[1/y/Enter] 本次允许  [2/a] 本会话允许  [n/Esc] 拒绝"
	found := false
	for _, line := range strings.Split(card, "\n") {
		stripped := strings.Trim(ansi.Strip(line), "│ ")
		if strings.Contains(stripped, "[1/y/Enter]") {
			found = true
			if stripped != keymap {
				t.Fatalf("keymap line drifted:\n got %q\nwant %q", stripped, keymap)
			}
		}
	}
	if !found {
		t.Fatalf("keymap line missing:\n%s", card)
	}
}
