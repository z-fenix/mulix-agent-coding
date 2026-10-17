package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
// （48;5;234——graphite 暗 256 层出厂初值，或真彩层 48;2 等价形态）。
func TestFramePaintsBackground(t *testing.T) {
	// 有色层：frame 每行含 bg SGR（48;5;234 或 38 层等价形态）
	if !frameHasBGEveryLine(t) {
		t.Fatal("frame 存在未铺底行")
	}
}

// TestNoColorFrameStructure 无色层结构保留：SGR 空集 + 行数与分区不变。
// 无色层测试纪律（照 theme_test.go TestNoColorFallback）：包级 activeTheme 为
// 确定性 256 层、不探测环境——Setenv 不清槽位，必须重建主题再断言；ember 预设
// （deepseek Task 8 才存在）。
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
