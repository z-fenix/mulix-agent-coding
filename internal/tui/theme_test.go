package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"

	"mulix/internal/event"
)

// ---- 主题系统（023 FR-014/D8）----
//
// 钉点：4×2 预设齐全（手选 256 回退表逐值）、/theme 实时换肤（含已渲染块
// 重排换色）、NO_COLOR 退避无色、滚动条随主题。环境敏感点一律 t.Setenv 钉死；
// 全局 activeTheme 的变更用后即还原（resetTheme）——后行测试（toolcard 等）
// 依赖出厂缺省的确定性 256 层。

// resetTheme 注册出厂缺省还原（确定性 256 层 graphite 暗——与环境探测无关，
// 对照包级初值 defaultActiveTheme）。
func resetTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { applyTheme(defaultActiveTheme()) })
}

// TestThemePresets 4×2 预设齐全：暗/亮归属与手选 256 回退表逐值钉死；显式
// -dark 后缀等价裸名；未知名（含套娃后缀）不解析。
func TestThemePresets(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLORTERM", "") // 钉 256 层——手选回退表逐值断言
	for _, want := range []struct {
		name        string
		dark        bool
		accentValue string
	}{
		{"graphite", true, "173"},
		{"ember", true, "209"},
		{"aurora", true, "79"},
		{"midnight", true, "141"},
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
	// 暗色四强调色两两不同（预设表不得重复取值）。
	seen := map[string]bool{}
	for _, name := range []string{"graphite", "ember", "aurora", "midnight"} {
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

// assertSlot 槽位字符串比较 helper（256 层确定性初值口径——逐槽钉点）。
func assertSlot(t *testing.T, got themeColor, want string) {
	t.Helper()
	if string(got) != want {
		t.Fatalf("slot = %q, want %q", got, want)
	}
}

// ---- dsh 派生槽位（2026-10-17-dsh-tui-visual-redesign Task 1）----

func TestDSHDarkSlots(t *testing.T) {
	th := defaultActiveTheme() // graphite→本任务后仍为 graphite？否——Task 8 才换默认；此处钉槽位值
	// 本任务后 defaultActiveTheme 仍返回旧默认 accent（Task 8 换 deepseek），
	// 但全部槽位换 dsh 值：
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
	assertSlot(t, th.accentColor, "173") // graphite accent 不变（Task 8 换默认）
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
}
