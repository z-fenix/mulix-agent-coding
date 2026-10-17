package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---- 帧布局（FR-001：alt-screen 全接管——转写视口 + 底部固定区）----

// TestFrameLayout pins：给定 H=24，转写视口行数 = 24 − 底部固定区行数；
// 底部固定区含编辑器行与状态行；帧总行数恰为 H（alt-screen 满屏接管）。
func TestFrameLayout(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	bottomRows := lipgloss.Height(m.bottomView())
	frame := m.frame()
	if got := m.transcriptState.vp.Height(); got != 24-bottomRows {
		t.Fatalf("viewport height = %d, want %d (24 − bottom %d)", got, 24-bottomRows, bottomRows)
	}
	if got := lipgloss.Height(frame); got != 24 {
		t.Fatalf("frame height = %d, want 24（alt-screen 满屏）", got)
	}
	// 底部固定区构成：编辑器行（composer）+ 状态行（023 D12：❯ 提示符仅首行）
	if !strings.Contains(m.bottomView(), "❯ ") {
		t.Fatalf("bottom region missing composer:\n%s", m.bottomView())
	}
	if !strings.Contains(m.bottomView(), "空闲") {
		t.Fatalf("bottom region missing status line:\n%s", m.bottomView())
	}
	// 转写内容进视口区（不在底部固定区）
	m.appendLine("hello viewport")
	frame = m.frame()
	if !strings.Contains(frame, "hello viewport") {
		t.Fatalf("frame missing transcript line:\n%s", frame)
	}
	if got := m.transcriptState.vp.Height(); got != 24-bottomRows {
		t.Fatalf("viewport height changed after append: %d, want %d", got, 24-bottomRows)
	}
}

// ---- 满帧铺底 + 无色层结构保留（2026-10-17-dsh-tui-visual-redesign Task 3；
// 设计 §4.3）----

// buildTestFrame 组装最小 frame（复用 tui_test.go 的 newTestModel 基建）：转写
// 一行 CJK 内容 + 80×24 尺寸——行集覆盖转写视口/滚动条列/底部固定区（编辑器 +
// 状态行），内容行供铺底与结构保留断言。
func buildTestFrame(t *testing.T) string {
	t.Helper()
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	m.appendLine("hello frame 铺底")
	return m.frame()
}

// sgrThemeRe 主题层 SGR（256 手选/真彩直排形态：38;5;N / 48;5;N / 38;2;r;g;b /
// 48;2;r;g;b）。
var sgrThemeRe = regexp.MustCompile(`\x1b\[(?:38|48);(?:5;\d+|2;\d+;\d+;\d+)m`)

// sgrCount 主题层 SGR 计数——无色层「SGR 空集」断言面（与 theme_test.go
// TestNoColorFallback 只查 38;2;/38;5; 同口径）。基础 16 色形态不在禁列：
// bubbles textarea 组件默认样式（CursorLine bg-0 / Prompt fg-7）主题无关、
// 先于本任务在场，编辑器换装归 Task 7。
func sgrCount(s string) int { return len(sgrThemeRe.FindAllString(s, -1)) }

// frameHasBGEveryLine 有色层满帧铺底钉点：frame 逐行含 bg 槽 SGR（预期形态由
// activeTheme.bgColor 经 sgrBg 推导——与槽位单源；空白行/滚动条列/底部固定区
// 同样在场）。
func frameHasBGEveryLine(t *testing.T) bool {
	t.Helper()
	bg := sgrBg(activeTheme.bgColor)
	if bg == "" {
		t.Log("activeTheme.bgColor 为空——有色层前提不成立")
		return false
	}
	bgSeq := "\x1b[" + bg + "m"
	for i, line := range strings.Split(buildTestFrame(t), "\n") {
		if !strings.Contains(line, bgSeq) {
			t.Logf("第 %d 行未铺底：%q", i, line)
			return false
		}
	}
	return true
}

// frameStructureIntact 无色层结构保留（验收 2）：同内容有色/无色两帧对比——
// 总行数一致且恰为 H=24（alt-screen 满屏）、转写/编辑器/状态行内容全部存活。
// 有色参照以显式 256 层构建主题（NO_COLOR 在场时 themeByName 探测退避——参照
// 不得走探测路径）。
func frameStructureIntact(t *testing.T) bool {
	t.Helper()
	thColored, ok := buildTheme("ember", true, tierANSI256)
	if !ok {
		t.Fatal("ember must resolve")
	}
	applyTheme(thColored)
	colored := buildTestFrame(t)
	thNone, ok := buildTheme("ember", true, tierNone)
	if !ok {
		t.Fatal("ember must resolve")
	}
	applyTheme(thNone)
	plain := buildTestFrame(t)
	coloredLines, plainLines := strings.Split(colored, "\n"), strings.Split(plain, "\n")
	if len(coloredLines) != len(plainLines) {
		t.Logf("行数漂移：colored=%d plain=%d", len(coloredLines), len(plainLines))
		return false
	}
	if len(plainLines) != 24 {
		t.Logf("帧高漂移：%d != 24（alt-screen 满屏）", len(plainLines))
		return false
	}
	for _, want := range []string{"hello frame 铺底", "❯ ", "空闲"} {
		if !strings.Contains(plain, want) {
			t.Logf("无色层内容缺失：%q", want)
			return false
		}
	}
	return true
}

// TestFramePaintsBackground 有色层满帧铺底（设计 §4.3）：frame 每行含 bg SGR
// （48;5;234——dsh dark 板 bg 256 手选，出厂初值；或真彩层 48;2 等价形态）。
func TestFramePaintsBackground(t *testing.T) {
	// 有色层：frame 每行含 bg SGR（48;5;234 或 38 层等价形态）
	if !frameHasBGEveryLine(t) {
		t.Fatal("frame 存在未铺底行")
	}
}

// TestNoColorFrameStructure 无色层结构保留：SGR 空集 + 行数与分区不变。
// 无色层测试纪律（照 theme_test.go TestNoColorFallback）：包级 activeTheme 为
// 确定性 256 层、不探测环境——Setenv 不清槽位，必须重建主题再断言；ember 预设。
func TestNoColorFrameStructure(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	resetTheme(t)
	th, ok := themeByName("ember")
	if !ok {
		t.Fatal("ember must resolve under NO_COLOR")
	}
	applyTheme(th)
	// 结构保留：行数与分区不变；主题层 SGR 空集（textarea 组件默认基础 16 色
	// 不在禁列——见 sgrCount 注记）
	if sgrCount(buildTestFrame(t)) != 0 {
		t.Fatal("NO_COLOR 残留主题层 SGR")
	}
	if !frameStructureIntact(t) {
		t.Fatal("无色层结构缺失")
	}
}

// TestFenceBGSurvivesFramePaint（Task 5 修复轮 1 / 评审 Important-1 钉点）：
// 围栏底 × 帧底合成的直接证据——renderMarkdown 围栏输出经帧出口 paintBG
// （真实宽度）后，codeBlockColor（235）在合成行存活，且每个行内重置点呈固定
// 三元序「reset → 帧底 234（paintBG 插序在前）→ 围栏底 235（fenceBG 重铺在
// 后）」——SGR 后写生效，围栏底不被帧底冲掉。重铺交互由本钉点作证，不再是
// 静态推理（隐性前提：lipgloss v2 对内嵌 SGR 不重排插序——本钉点同时钉住
// 该前提）。
func TestFenceBGSurvivesFramePaint(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme()) // 钉确定性 256 层（防前序测试主题泄漏）
	th := defaultActiveTheme()
	out := renderMarkdown("```go\nfmt.Println(1)\n```", 60, &th)
	if len(out) == 0 {
		t.Fatal("fence must render")
	}
	painted := paintBG(strings.Join(out, "\n"), 60)
	if !strings.Contains(painted, "\x1b[48;5;235m") {
		t.Fatalf("fence bg must survive the frame paint:\n%q", painted)
	}
	// 顺序钉点：重置点后先帧底插序、后围栏底重铺（三元序逐字面）。
	if !strings.Contains(painted, "\x1b[0m\x1b[48;5;234m\x1b[48;5;235m") {
		t.Fatalf("fence bg must repaint after the frame bg insert at resets:\n%q", painted)
	}
}

// ---- chrome 换装（2026-10-17-dsh-tui-visual-redesign Task 7；设计 §6）----

// TestScrollbarDefaultAccent 钉（brief）：滚动条 thumb = 当前默认 accent 111
// （deepseek 暗——Task 8 起出厂缺省字面）+ track dim 244。thumb/track 槽源已由
// T18 接线（ember 同源钉 TestThemeScrollbar）——本钉为出厂缺省字面。
func TestScrollbarDefaultAccent(t *testing.T) {
	resetTheme(t)
	m, _, _ := newTestModel(t)
	m.transcriptState.vp.SetHeight(10)
	m.transcriptState.wrappedLines = make([]string, 50) // 溢出 → thumb 在场
	sb := m.scrollbarView()
	if !strings.Contains(sb, "\x1b[38;5;111m") {
		t.Fatalf("scrollbar thumb must pin the default accent 111:\n%q", sb)
	}
	if !strings.Contains(sb, "\x1b[38;5;244m") {
		t.Fatalf("scrollbar track must take the dim slot:\n%q", sb)
	}
}

// TestChromeSurfaceDress 钉：补全菜单（无框浮层——渲染点自铺 surface1）与
// resume 选择器（styleCard 卡——bottomView 合成铺面，与授权卡同法）surface1
// 底 + accent 选中；未选中行不带 accent。
func TestChromeSurfaceDress(t *testing.T) {
	resetTheme(t)
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	// 补全菜单：整行纯 "/" 记号 → 菜单开（sel=0 首项 /new）。
	m = update(t, m, typeStr(m, "/"))
	if !m.completeOpen() {
		t.Fatalf("completion menu must open on \"/\": %q", m.composerValue())
	}
	menu := m.completeMenuView()
	if !strings.Contains(menu, "\x1b[48;5;235m") {
		t.Fatalf("completion menu must take the surface1 slot:\n%q", menu)
	}
	var selLine, plainLine string
	for _, line := range strings.Split(menu, "\n") {
		if strings.Contains(line, "/new") {
			selLine = line
		}
		if strings.Contains(line, "/resume") {
			plainLine = line
		}
	}
	if !strings.Contains(selLine, "\x1b[38;5;111m") {
		t.Fatalf("selected item must take the accent slot:\n%q", selLine)
	}
	if strings.Contains(plainLine, "\x1b[38;5;111m") {
		t.Fatalf("unselected item must not take accent:\n%q", plainLine)
	}
	// resume 选择器：surface1 底（bottomView 卡底合成）+ 选中行 accent、未选中
	// 行平文（accent 断言在 pickerView 渲染点——卡边框字符自带 accent SGR，
	// bottomView 整行断言会把边框着色误计入未选中行）。
	m.picker = &resumePicker{items: []SessionSummary{
		{ID: "s-aaaa", EngineID: "e-111111", Model: "m1", Workspace: "w1", CreatedAt: "2026-10-15T10:00:00Z"},
		{ID: "s-bbbb", EngineID: "e-222222", Model: "m2", Workspace: "w2", CreatedAt: "2026-10-15T11:00:00Z"},
	}, sel: 1}
	if bv := m.bottomView(); !strings.Contains(bv, "\x1b[48;5;235m") {
		t.Fatalf("resume picker must take the surface1 slot:\n%s", bv)
	}
	selRow, plainRow := false, false
	for _, line := range strings.Split(m.pickerView(), "\n") {
		if strings.Contains(line, "s-aaaa") {
			plainRow = !strings.Contains(line, "\x1b[38;5;111m")
		}
		if strings.Contains(line, "s-bbbb") {
			selRow = strings.Contains(line, "\x1b[38;5;111m")
		}
	}
	if !selRow || !plainRow {
		t.Fatalf("picker selection accent wrong: selRow=%v plainRow=%v\n%s", selRow, plainRow, m.pickerView())
	}
}

// ---- 三档列宽几何（2026-10-17-dsh-tui-visual-redesign Task 9）----

// TestColumnWidthsThresholds 钉（brief）：三档列宽几何三元组——窄档 <100 恒
// (0,W,0)（开合无关，以双关/双开两端钉）；中档 100–129 面板恒 0；宽档 ≥130
// 侧栏 16 / 面板 32 随开合让位，main = W−让位和。Review Focus 1 边界：99 与
// 129 贴窄/中档上沿、130 全开与只开面板贴宽档下沿（main 恒 ≥80 由档位阈值保证）。
func TestColumnWidthsThresholds(t *testing.T) {
	tests := []struct {
		name        string
		w           int
		sidebarOpen bool
		panelOpen   bool
		wantSide    int
		wantMain    int
		wantPanel   int
	}{
		{"窄档80双关", 80, false, false, 0, 80, 0},
		{"窄档80双开——开合无关", 80, true, true, 0, 80, 0},
		{"窄档99双关——窄档上沿", 99, false, false, 0, 99, 0},
		{"窄档99双开——窄档上沿开合无关", 99, true, true, 0, 99, 0},
		{"中档100只开侧栏", 100, true, false, 16, 84, 0},
		{"中档100全开——面板恒0", 100, true, true, 16, 84, 0},
		{"中档129全开——面板恒0", 129, true, true, 16, 113, 0},
		{"宽档130全开", 130, true, true, 16, 82, 32},
		{"宽档130只开面板", 130, false, true, 0, 98, 32},
		{"宽档130只开侧栏", 130, true, false, 16, 114, 0},
		{"宽档130全关", 130, false, false, 0, 130, 0},
		{"宽档150全开", 150, true, true, 16, 102, 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			side, main, panel := columnWidths(tt.w, tt.sidebarOpen, tt.panelOpen)
			if side != tt.wantSide || main != tt.wantMain || panel != tt.wantPanel {
				t.Fatalf("columnWidths(%d,%v,%v) = (%d,%d,%d), want (%d,%d,%d)",
					tt.w, tt.sidebarOpen, tt.panelOpen, side, main, panel,
					tt.wantSide, tt.wantMain, tt.wantPanel)
			}
		})
	}
}

// ---- frame 三列合成（2026-10-17-dsh-tui-visual-redesign Task 10）----

// TestFrameThreeTierComposition 钉（Task 10 Step 1；Task 11 侧栏接线修钉）两档构成：
//
//   - W=150 全开（侧栏 Task 11 接线 m.sidebarOpen、面板 Task 12 接线
//     m.panel.open——缺省均 true，设计裁定默认三栏常开）：三列 JoinHorizontal
//     ——首行 16 列侧栏列区（列 0–15 侧栏视图：品牌行 ◆ mulix 领衔）、转写
//     内容自主列（列 16）起、滚动条列在列 117（主列 101+1 收口凭据）、
//     32 列面板列区（列 118–149 面板视图：页签行变更/代理/诊断/缓存 领衔）；
//     帧宽断言：每行显示宽 ≤150（ansi.StringWidth——底部区未收口主列即超宽
//     破版 / paintBG 折行漂高）。
//   - W=80 窄档与单列基线逐行一致：基线 = 旧单列构成公式（转写‖滚动条 +
//     底部区 + 铺底）对同输入孪生模型的重建。无色层（NO_COLOR）跑——有色层
//     paintBG 会把各行归一填充到帧宽，构成偏差（如误走 JoinVertical 的行填
//     充）被掩掉；无色层下填充差逐字节可见。视口喂给/钉底状态机由 frame 统
//     一收敛（基线孪生同点收敛后重建）——构成钉点归本测试，状态机钉点归
//     TestFrameLayout / 锚点矩阵。窄档不变量属负空间守卫：侧栏视图窄档永不
//     渲染（几何退化 (0,W,0)——Task 11 sidebarColumn 仅 sidebar>0 消费），
//     基线与现状构成天然一致。
func TestFrameThreeTierComposition(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme()) // 钉确定性 256 层（有色层段 bg 非空）

	// ---- W=150 全开：三列构成 + 帧宽断言 ----
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 150, Height: 24}) // composer SetWidth 经 mainWidth 接线
	const seeds = 30
	for i := range seeds {
		m.appendLine(fmt.Sprintf("第%02d行", i))
	}
	wantTop := seeds - (24 - lipgloss.Height(m.bottomView())) // 尾随钉底：yoff = total − vpH
	wantFirst := fmt.Sprintf("第%02d行", wantTop)
	lines := strings.Split(m.frame(), "\n")
	if len(lines) != 24 {
		t.Fatalf("帧高漂移：%d != 24（alt-screen 满屏）", len(lines))
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 150 {
			t.Fatalf("行 %d 显示宽 %d > 150（三列构成超宽破版）", i, w)
		}
	}
	// 列位断言（显示列——ansi.Cut 按格切，Strip 后比内容）
	first := ansi.Strip(lines[0])
	if side := ansi.Cut(first, 0, 16); !strings.Contains(side, "◆ mulix") {
		t.Fatalf("首行列 0–15 缺侧栏品牌行（Task 11 侧栏视图）：%q", side)
	}
	if got := ansi.Cut(first, 16, 150); !strings.HasPrefix(got, wantFirst) {
		t.Fatalf("首行转写内容未自主列（列 16）起：want %q, got %q", wantFirst, got)
	}
	if sb := ansi.Cut(first, 117, 118); strings.TrimSpace(sb) == "" {
		t.Fatalf("首行列 117 非滚动条列（主列宽 101+1 收口凭据）：%q", sb)
	}
	if panel := ansi.Cut(first, 118, 150); !strings.Contains(panel, "变更") || !strings.Contains(panel, "缓存") {
		t.Fatalf("首行列 118–149 缺面板页签行（Task 12 面板视图）：%q", panel)
	}

	// ---- W=80 窄档与单列基线逐行一致（无色层）----
	t.Setenv("NO_COLOR", "1")
	resetTheme(t)
	th, ok := themeByName("ember")
	if !ok {
		t.Fatal("ember must resolve under NO_COLOR")
	}
	applyTheme(th)

	build := func() *tuiModel {
		n, _, _ := newTestModel(t)
		n = update(t, n, tea.WindowSizeMsg{Width: 80, Height: 24})
		for i := range seeds {
			n.appendLine(fmt.Sprintf("第%02d行", i))
		}
		return n
	}
	got := build().frame()
	base := build()
	base.frame() // 视口喂给/钉底状态机与被测帧同收敛点（帧内副作用幂等）
	want := lipgloss.JoinHorizontal(lipgloss.Top,
		base.transcriptState.transcriptView(), base.scrollbarView()) + "\n" + base.bottomView()
	want = paintBG(want, 80)
	if got != want {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
		div := -1
		for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
			if gotLines[i] != wantLines[i] {
				div = i
				break
			}
		}
		t.Fatalf("窄档（W=80）与单列基线不一致：got %d 行 / want %d 行 / 首个分歧行 %d\ngot:  %q\nwant: %q",
			len(gotLines), len(wantLines), div, got, want)
	}
}

// TestBottomZoneCollarWidth 钉（评审 Issue 7——底部区宽度收口主列）：宽档
// W=150（main = 102 = 150−16−32，几何钉于 TestColumnWidthsThresholds）下底部
// 区消费点收口主列宽——composer 编辑器宽（resize 分支接线）、状态行 1 左右
// 合成宽、授权卡内容宽（长参数摘要按 mainWidth−4 折行，卡总宽含 styleCard
// 横向装饰 4 = mainWidth）。补全菜单无宽度消费点（渲染行内容有界；加截断会
// 改窄档字节——窄档等价性禁改），无断言面（Ruling/报告记）。
func TestBottomZoneCollarWidth(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 150, Height: 24})
	m = update(t, m, statusTick{}) // 快照入模（行 1 右组「模型 …」数据源——D7）
	const main = 102               // 宽档 150 全开让位：150−16−32

	// composer：编辑器宽收口主列——ta.Width() = SetWidth 实参（mainWidth）减
	// 提示符预留（bubbles SetWidth 语义：内容宽 = 配置宽 − promptWidth；Base
	// 无边框无水平装饰，dressStyles 清零后预留恰为 composerPromptWidth）。
	if got := m.composerState.ta.Width(); got != main-composerPromptWidth {
		t.Fatalf("composer content width = %d, want %d（mainWidth %d − 提示符预留 %d）",
			got, main-composerPromptWidth, main, composerPromptWidth)
	}
	// 状态行 1：左右合成贴主列行尾（右组收口在列 mainWidth）
	row1 := strings.Split(m.statusView(), "\n")[0]
	if got := ansi.StringWidth(ansi.Strip(row1)); got != main {
		t.Fatalf("status row1 width = %d, want %d", got, main)
	}
	// 授权卡：20 键长参数摘要按 mainWidth−4 折行（首行恰满宽）→ 卡总宽
	// = mainWidth；未收口时按 m.width−4 折行 → 卡 150 超主列（撑破帧宽形态）。
	var parts []string
	for i := range 20 {
		parts = append(parts, fmt.Sprintf(`"k%02d":"xxxxxxxxxx"`, i))
	}
	m.approval = &approvalCard{toolUseID: "t-12345678", tool: "Bash",
		input: json.RawMessage("{" + strings.Join(parts, ",") + "}")}
	if got := lipgloss.Width(styleCard.Render(m.approvalCardView())); got != main {
		t.Fatalf("approval card width = %d, want %d（mainWidth）", got, main)
	}
}
