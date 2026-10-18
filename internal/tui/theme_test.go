package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"mulix/internal/event"
)

// ---- 主题系统（023 FR-014/D8）----
//
// 钉点：5×2 预设齐全（手选 256 回退表逐值）、/theme 实时换肤（含已渲染块
// 重排换色）、NO_COLOR 退避无色、滚动条随主题。环境敏感点一律 t.Setenv 钉死；
// 全局 activeTheme 的变更用后即还原（resetTheme）——后行测试（toolcard 等）
// 依赖出厂缺省的确定性 256 层。

// resetTheme 注册出厂缺省还原（确定性 256 层 deepseek 暗——与环境探测无关，
// 对照包级初值 defaultActiveTheme）。
func resetTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { applyTheme(defaultActiveTheme()) })
}

// TestThemePresets 5×2 预设齐全：暗/亮归属与手选 256 回退表逐值钉死；显式
// -dark 后缀等价裸名；未知名（含套娃后缀）不解析。
func TestThemePresets(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLORTERM", "") // 钉 256 层——手选回退表逐值断言
	for _, want := range []struct {
		name        string
		dark        bool
		accentValue string
	}{
		{"deepseek", true, "111"},
		{"graphite", true, "173"},
		{"ember", true, "209"},
		{"aurora", true, "79"},
		{"midnight", true, "141"},
		{"deepseek-light", false, "69"},
		{"graphite-light", false, "173"},
		{"ember-light", false, "167"},
		{"aurora-light", false, "65"},
		{"midnight-light", false, "104"},
	} {
		th, ok := themeByName(want.name)
		if !ok {
			t.Fatalf("preset %s must resolve", want.name)
		}
		if th.name != want.name || th.dark != want.dark {
			t.Fatalf("%s resolved as (name %q dark %v)", want.name, th.name, th.dark)
		}
		if string(th.accentColor) != want.accentValue {
			t.Fatalf("%s accent = %q, want %q（手选 256 回退）", want.name, th.accentColor, want.accentValue)
		}
		// 语义槽齐全：色彩层开启下消费槽与预留槽都不得为空。
		for slot, v := range map[string]string{
			"dim": string(th.dimColor), "red": string(th.redColor),
			"yellow": string(th.yellowColor), "green": string(th.greenColor),
			"cyan": string(th.cyanColor), "magenta": string(th.magentaColor),
			"fg": string(th.fgColor), "bg": string(th.bgColor),
		} {
			if v == "" {
				t.Fatalf("%s slot %s must not be empty", want.name, slot)
			}
		}
	}
	// 暗色五强调色两两不同（预设表不得重复取值）。
	seen := map[string]bool{}
	for _, name := range []string{"deepseek", "graphite", "ember", "aurora", "midnight"} {
		th, _ := themeByName(name)
		if seen[string(th.accentColor)] {
			t.Fatalf("dark accent %q duplicated across presets", th.accentColor)
		}
		seen[string(th.accentColor)] = true
	}
	// 显式 -dark 后缀等价裸名。
	if th, ok := themeByName("ember-dark"); !ok || th.name != "ember" || !th.dark {
		t.Fatalf("ember-dark must resolve as dark ember, got (ok=%v name=%q dark=%v)", ok, th.name, th.dark)
	}
	// 未知名一律 false。
	for _, bad := range []string{"nope", "", "graphite-dark-light", "graphite_light"} {
		if _, ok := themeByName(bad); ok {
			t.Fatalf("unknown theme %q must not resolve", bad)
		}
	}
}

// TestThemeSwitch /theme 实时换肤：切换前已渲染的 markdown 块持 graphite
// accent SGR，/theme ember 后同块重排为 ember accent SGR（换行缓存全量失效
// ——live switch 换色不留旧底）；themeState.name 镜像同步；未知名不切换并列
// 清单；无参列清单带当前名。
func TestThemeSwitch(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor") // 钉真彩层——精确 hex SGR 断言
	resetTheme(t)
	if th, ok := themeByName("graphite"); ok {
		applyTheme(th) // 初值主题为确定性 256 层——两侧统一钉真彩层再比对
	}
	m, _, _ := newTestModel(t)
	if m.themeState.name != "graphite" {
		t.Fatalf("initial themeState.name = %q, want graphite", m.themeState.name)
	}
	// 预置已渲染 markdown（标题走 accent 槽）。
	m = update(t, m, tuiEvent{ev: event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "# 标题\n\n正文"}})
	before := strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
	if !strings.Contains(before, "\x1b[38;2;217;119;87m") { // graphite #d97757
		t.Fatalf("graphite accent SGR missing before switch:\n%s", before)
	}
	// /theme ember 实时切换。
	m = update(t, m, typedInput("/theme ember"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.themeState.name != "ember" {
		t.Fatalf("themeState.name = %q, want ember", m.themeState.name)
	}
	after := strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
	if !strings.Contains(after, "\x1b[38;2;240;109;56m") { // ember #f06d38
		t.Fatalf("ember accent SGR missing after switch:\n%s", after)
	}
	if strings.Contains(after, "\x1b[38;2;217;119;87m") {
		t.Fatalf("graphite SGR must be re-rendered away after live switch:\n%s", after)
	}
	// 未知名：不切换，留痕列清单。
	m = update(t, m, typedInput("/theme nope"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.themeState.name != "ember" {
		t.Fatalf("unknown name must not switch, name = %q", m.themeState.name)
	}
	log := strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
	if !strings.Contains(log, "未知主题 nope") || !strings.Contains(log, "graphite-light") {
		t.Fatalf("unknown theme must list the catalog:\n%s", log)
	}
	// 无参：列清单 + 当前名。
	m = update(t, m, typedInput("/theme"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	log = strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
	if !strings.Contains(log, "midnight-light") || !strings.Contains(log, "（当前 ember）") {
		t.Fatalf("bare /theme must list catalog with current theme:\n%s", log)
	}
}

// TestNoColorFallback NO_COLOR=1 退避：槽位全空、渲染无真彩/256 SGR（装饰
// 属性不在禁列——no-color.org 只禁色）、内容原样存活。
func TestNoColorFallback(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	resetTheme(t)
	th, ok := themeByName("ember")
	if !ok {
		t.Fatal("ember must resolve under NO_COLOR")
	}
	if string(th.accentColor) != "" {
		t.Fatalf("NO_COLOR must blank the slots, accent = %q", th.accentColor)
	}
	applyTheme(th)
	card := &toolCard{tool: "Bash", arg: "npm test", state: toolRunning}
	out := strings.Join(card.render(80), "\n") +
		strings.Join(renderMarkdown("# 标题", 80, &activeTheme), "\n") +
		styleCard.Render("x") + styleStatus.Render("y")
	if strings.Contains(out, "\x1b[38;2;") || strings.Contains(out, "\x1b[38;5;") {
		t.Fatalf("NO_COLOR must degrade to no color SGR:\n%q", out)
	}
	if !strings.Contains(out, "npm test") {
		t.Fatalf("content must survive the degrade:\n%q", out)
	}
}

// TestThemeScrollbar 滚动条随主题（T6 移交项）：thumb=accent 槽、track=dim 槽
// ——与主题方法同源断言（层级无关）。
func TestThemeScrollbar(t *testing.T) {
	resetTheme(t)
	th, ok := themeByName("ember")
	if !ok {
		t.Fatal("ember must resolve")
	}
	applyTheme(th)
	m, _, _ := newTestModel(t)
	m.transcriptState.vp.SetHeight(10)
	m.transcriptState.wrappedLines = make([]string, 50) // 溢出 → thumb 在场
	sb := m.scrollbarView()
	if !strings.Contains(sb, th.accent("█")) {
		t.Fatalf("scrollbar thumb must take the accent slot:\n%q", sb)
	}
	if !strings.Contains(sb, th.dim("│")) {
		t.Fatalf("scrollbar track must take the dim slot:\n%q", sb)
	}
}

// TestStyleKeycap 键帽样式（Task 4 Produces：surface3 底 + fgSecondary 前——
// 设计 §6 授权卡/§7 键帽行；Task 7 审批卡消费）。256 层确定性初值口径钉字面。
func TestStyleKeycap(t *testing.T) {
	resetTheme(t)
	out := styleKeycap.Render("y")
	if !strings.Contains(out, "48;5;237") { // surface3 256 手选（graphite 暗 · 出厂初值）
		t.Fatalf("keycap bg must take the surface3 slot:\n%q", out)
	}
	if !strings.Contains(out, "38;5;252") { // fgSecondary 256 手选
		t.Fatalf("keycap fg must take the fgSecondary slot:\n%q", out)
	}
}

// assertSlot 槽位字符串比较 helper（256 层确定性初值口径——逐槽钉点）。
func assertSlot(t *testing.T, got themeColor, want string) {
	t.Helper()
	if string(got) != want {
		t.Fatalf("slot = %q, want %q", got, want)
	}
}

// TestStyleBubble 用户气泡样式（Task 5 Produces：surface2 底 + 1 格水平内衬、
// 无边框——设计 §6，transcript.go userBlock 消费）。256 层确定性初值口径钉字面
// （TestStyleKeycap 同法）；内衬断言剥 SGR（lipgloss v2 逐段铺底——padding 段
// 与内容段各自带 bg，SGR 零宽）。
func TestStyleBubble(t *testing.T) {
	resetTheme(t)
	out := styleBubble.Render("x")
	if !strings.Contains(out, "48;5;236") { // surface2 256 手选（graphite 暗 · 出厂初值）
		t.Fatalf("bubble bg must take the surface2 slot:\n%q", out)
	}
	if strings.ContainsAny(out, "╭╮╰╯") {
		t.Fatalf("bubble must be borderless (dsh 底色块):\n%q", out)
	}
	if stripped := ansi.Strip(out); stripped != " x " { // 1 格内衬（padding 结构——无色层同样在场）
		t.Fatalf("bubble must carry 1-cell horizontal padding, stripped = %q (raw %q)", stripped, out)
	}
}

// ---- dsh 派生槽位（2026-10-17-dsh-tui-visual-redesign Task 1）----

func TestDSHDarkSlots(t *testing.T) {
	th := defaultActiveTheme() // deepseek 暗（Task 8 起出厂缺省）；此处钉槽位值
	// 非强调槽为 dsh dark 值（底板与强调色预设解耦——预设换值不牵动底板）：
	assertSlot(t, th.bgColor, "234")
	assertSlot(t, th.surface1Color, "235")
	assertSlot(t, th.surface2Color, "236")
	assertSlot(t, th.surface3Color, "237")
	assertSlot(t, th.borderColor, "238")
	assertSlot(t, th.fgColor, "231")
	assertSlot(t, th.fgSecondaryColor, "252")
	assertSlot(t, th.fgTertiaryColor, "145")
	assertSlot(t, th.dimColor, "244")
	assertSlot(t, th.codeBlockColor, "235")
	assertSlot(t, th.diffAddedBGColor, "22")
	assertSlot(t, th.diffAddedMarkerColor, "114")
	assertSlot(t, th.diffDeletedBGColor, "52")
	assertSlot(t, th.diffDeletedMarkerColor, "203")
	assertSlot(t, th.greenColor, "114")  // green-400 #4ED17E（FR-003 状态语义色）
	assertSlot(t, th.yellowColor, "214") // amber-400 #F7AD31
	assertSlot(t, th.redColor, "203")    // red-400 #F25A5A
	assertSlot(t, th.accentColor, "111") // deepseek 默认 accent（Task 8 换默认）
}

// ---- deepseek 出厂缺省（2026-10-17-dsh-tui-visual-redesign Task 8）----

// TestDeepseekDefault 钉：出厂缺省 = deepseek 暗（FR-002——dsh dark 板品牌蓝
// #7AAAFF 手选 111）；deepseek/deepseek-light 预设解析（light 板 #4176E6 手选
// 69）；目录程序化生成且 deepseek 对居首。
func TestDeepseekDefault(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLORTERM", "") // 钉 256 层——手选回退逐值断言
	th := defaultActiveTheme()
	if th.name != "deepseek" || !th.dark {
		t.Fatalf("default theme = (name %q dark %v), want deepseek dark", th.name, th.dark)
	}
	assertSlot(t, th.accentColor, "111")
	for _, want := range []struct{ name, accent string }{
		{"deepseek", "111"},
		{"deepseek-light", "69"},
	} {
		got, ok := themeByName(want.name)
		if !ok {
			t.Fatalf("preset %s must resolve", want.name)
		}
		if got.name != want.name {
			t.Fatalf("preset %s resolved as %q", want.name, got.name)
		}
		assertSlot(t, got.accentColor, want.accent)
	}
	fields := strings.Fields(themeCatalogList())
	// minors 清理（T8 deferred）：目录恰 10 项（5 预设 × dark/light）——精确
	// 下界语义（防预设增删漂移；首两项仍钉 deepseek 置首）。
	if len(fields) != 10 || fields[0] != "deepseek" || fields[1] != "deepseek-light" {
		t.Fatalf("catalog must lead with the deepseek pair, got %q", themeCatalogList())
	}
}

// ---- dsh light 派生槽位（2026-10-17-dsh-tui-visual-redesign Task 2）----

// TestDSHLightSlots lightSlots 全槽 dsh light 值钉点（设计 §4.2：亮底白底 +
// bluish 表面层级 + 正文反转暗化；状态色 green-500/amber-500/red-600）。
func TestDSHLightSlots(t *testing.T) {
	th, ok := themeByName("graphite-light")
	if !ok {
		t.Fatal("graphite-light 解析失败")
	}
	assertSlot(t, th.bgColor, "231")                // #FFFFFF
	assertSlot(t, th.surface1Color, "255")          // bluish-50 #F9FAFB
	assertSlot(t, th.surface2Color, "254")          // bluish-60 #F5F6F7
	assertSlot(t, th.surface3Color, "253")          // bluish-75 #F1F3F5
	assertSlot(t, th.borderColor, "252")            // bluish-150 #E9ECF2
	assertSlot(t, th.fgColor, "16")                 // bluish-1000 #0F1115
	assertSlot(t, th.fgSecondaryColor, "241")       // bluish-700 #61666B
	assertSlot(t, th.fgTertiaryColor, "244")        // bluish-600 #81858C
	assertSlot(t, th.dimColor, "246")               // bluish-500 #979DA6
	assertSlot(t, th.codeBlockColor, "255")         // markdown-code-block = bluish-50
	assertSlot(t, th.diffAddedBGColor, "194")       // #E6F4E7（256 手选语义绿晕）
	assertSlot(t, th.diffDeletedBGColor, "224")     // #FCE6E2
	assertSlot(t, th.diffAddedMarkerColor, "28")    // #01A241
	assertSlot(t, th.diffDeletedMarkerColor, "124") // #BA2723
	assertSlot(t, th.greenColor, "35")              // green-500 #22C55E（FR-003 状态语义色——T2 遗留补钉）
	assertSlot(t, th.yellowColor, "172")            // amber-500 #DD8629
	assertSlot(t, th.redColor, "196")               // red-600 #EC1313
	assertSlot(t, th.accentColor, "173")            // graphite-light accent 不变
}

func TestSgrBg(t *testing.T) {
	if got := sgrBg("234"); got != "48;5;234" {
		t.Fatalf("48 层: %q", got)
	}
	if got := sgrBg("#151517"); got != "48;2;21;21;23" {
		t.Fatalf("真彩层: %q", got)
	}
	if got := sgrBg(""); got != "" {
		t.Fatalf("空槽: %q", got)
	}
	// minors 清理（T1 deferred）：基础 16 色映射与防御分支——此前仅测 256/hex/空三例。
	// sgrBg 是背景映射：0-7 → 40+n、8-15 → 92+n（100-107）。
	if got := sgrBg("3"); got != "43" {
		t.Fatalf("基础色 bg: %q", got)
	}
	if got := sgrBg("11"); got != "103" {
		t.Fatalf("基础亮色 bg: %q", got)
	}
	if got := sgrBg("999"); got != "" {
		t.Fatalf("越界索引须不着色: %q", got)
	}
	if got := sgrBg("#XYZ"); got != "" {
		t.Fatalf("非法 hex 须不着色: %q", got)
	}
	if got := sgrBg("-1"); got != "" {
		t.Fatalf("负数索引须不着色: %q", got)
	}
}

// ---- 渲染点暗/亮双主题矩阵（终审 Finding 3——SC-003 light 渲染点钉点补全）----
//
// SC-003 要求暗/亮两模式每渲染点槽位断言；此前 light 侧只有槽位表钉
// （TestDSHLightSlots）无渲染点 SGR 钉。本矩阵把高频渲染点的暗色钉参数化为
// dark/light 双跑（themeByName("deepseek")/("deepseek-light")——256 手选值是
// 钉点口径，真彩 hex 不钉，既有纪律）：横幅/助手头行 accent、工具卡三态符号
// ●✓✗ 着色、状态栏徽章（accent 底 + bg 槽 fg；NOT_READY amber / FAILED red
// 语义底）、焦点名 caption、diff 头计数 green/red、滚动条 thumb/track。
func TestDSHRenderPointThemeMatrix(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLORTERM", "") // 钉 256 层——手选值断言
	for _, tt := range []struct {
		name    string
		preset  string
		accent  string // 横幅/助手头行/工具运行 ●/徽章底/滚动条 thumb
		dim     string // 焦点名 caption/滚动条 track
		green   string // 工具完成 ✓/diff 头 +A
		red     string // 工具失败 ✗/diff 头 -B
		amber   string // NOT_READY 徽章底
		badgeFg string // 徽章 fg = bg 槽（TestStatusBarDress 不变量）
	}{
		{"deepseek", "deepseek", "111", "244", "114", "203", "214", "234"},
		{"deepseek-light", "deepseek-light", "69", "246", "35", "196", "172", "231"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			th, ok := themeByName(tt.preset)
			if !ok {
				t.Fatalf("preset %s must resolve", tt.preset)
			}
			resetTheme(t)
			applyTheme(th)
			// 横幅 + 助手头行：accent 槽（TestChromeBannerBG/TestChromeTurnHeader
			// dark 侧同渲染点）。
			bannerLine := bannerBlock{model: "claude-x"}.render(80)[0]
			if !strings.Contains(bannerLine, "\x1b[38;5;"+tt.accent+"m◆") {
				t.Fatalf("banner accent %s:\n%q", tt.accent, bannerLine)
			}
			headerLine := assistantHeaderBlock{}.render(80)[0]
			if !strings.Contains(headerLine, "\x1b[38;5;"+tt.accent+"m◆ mulix") {
				t.Fatalf("assistant header accent %s:\n%q", tt.accent, headerLine)
			}
			// 工具卡三态符号 ●✓✗ 着色（TestToolCardLifecycle/TestToolCardFailure
			// dark 侧同渲染点）。
			run := (&toolCard{tool: "Bash", arg: "x", state: toolRunning}).render(80)[0]
			if !strings.Contains(run, "\x1b[38;5;"+tt.accent+"m●\x1b[0m") {
				t.Fatalf("running ● must take accent %s:\n%q", tt.accent, run)
			}
			done := (&toolCard{tool: "Bash", arg: "x", state: toolDone, outputTail: []string{"ok"}}).render(80)[0]
			if !strings.Contains(done, "\x1b[38;5;"+tt.green+"m✓\x1b[0m") {
				t.Fatalf("done ✓ must take green %s:\n%q", tt.green, done)
			}
			fail := (&toolCard{tool: "Bash", arg: "x", state: toolFailed, failedDetail: "boom"}).render(80)[0]
			if !strings.Contains(fail, "\x1b[38;5;"+tt.red+"m✗\x1b[0m") {
				t.Fatalf("failed ✗ must take red %s:\n%q", tt.red, fail)
			}
			// diff 头计数 green/red（TestDiffHeaderCounts dark 侧同渲染点）。
			head := renderDiff(diffInput{path: "a.go", verb: "改", before: "a\nb\n", after: "a\nc\nd\n"}, 80, activeTheme)[0]
			if !strings.Contains(head, "\x1b[38;5;"+tt.green+"m+2") || !strings.Contains(head, "\x1b[38;5;"+tt.red+"m-1") {
				t.Fatalf("diff head counts must take green %s / red %s:\n%q", tt.green, tt.red, head)
			}
			// 滚动条 thumb(accent)/track(dim)（TestScrollbarDefaultAccent dark 侧
			// 同渲染点）。
			m, _, _ := newTestModel(t)
			m.transcriptState.vp.SetHeight(10)
			m.transcriptState.wrappedLines = make([]string, 50)
			if sb := m.scrollbarView(); !strings.Contains(sb, "\x1b[38;5;"+tt.accent+"m█") ||
				!strings.Contains(sb, "\x1b[38;5;"+tt.dim+"m│") {
				t.Fatalf("scrollbar thumb accent %s / track dim %s:\n%q", tt.accent, tt.dim, sb)
			}
			// 状态栏徽章（accent 底 + bg 槽 fg）+ 焦点名 caption（
			// TestStatusBarDress/TestStatusBarFocusName dark 侧同渲染点）。
			m = update(t, m, statusTick{})
			r1 := strings.Split(m.statusView(), "\n")[0]
			if !strings.Contains(r1, "48;5;"+tt.accent) || !strings.Contains(r1, "38;5;"+tt.badgeFg) {
				t.Fatalf("badge must take accent bg %s / bg-slot fg %s:\n%q", tt.accent, tt.badgeFg, r1)
			}
			m.width = 150
			m.focus = focusPanel
			row2 := strings.Split(m.statusView(), "\n")[1]
			if !strings.HasPrefix(row2, "\x1b[38;5;"+tt.dim+"m") || !strings.Contains(row2, "焦点 面板") {
				t.Fatalf("focus name must take the caption/dim slot %s:\n%q", tt.dim, row2)
			}
			// 语义徽章：NOT_READY amber / FAILED red 底（FR-003 状态语义色）。
			m2, _, _ := newTestModel(t)
			m2.statusState.snapshot.Ready = "NOT_READY"
			if r1 := strings.Split(m2.statusView(), "\n")[0]; !strings.Contains(r1, "48;5;"+tt.amber) {
				t.Fatalf("NOT_READY badge must take amber %s:\n%q", tt.amber, r1)
			}
			m2.statusState.snapshot.Ready = "FAILED"
			if r1 := strings.Split(m2.statusView(), "\n")[0]; !strings.Contains(r1, "48;5;"+tt.red) {
				t.Fatalf("FAILED badge must take red %s:\n%q", tt.red, r1)
			}
		})
	}
}
