package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// ---- 主题系统（023 FR-014/D8，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）internal/cli/theme.go：语义槽
// + 逐槽手选 256 色回退（不做距离降采样——暗色低晶度底色经距离塌缩成灰、丢
// 失红绿语义，回退必须手选）+ 色彩层退避（真彩 hex / 256 手选 / NO_COLOR·dumb
// 无色）与会话级 /theme 换肤的结构思路。adapted：
//  1. 预设收敛（D8）——参考 8 组强调色 × 18 槽；本仓 4 强调色（graphite/
//     ember/aurora/midnight）× 暗/亮两底，非强调槽取参考暗/亮双底板的对应槽
//     （faint/err/warn/success/info/secondary/muted/userBubbleBG）。槽位为
//     「渲染点实际消费 + 预留」最小集：accent/dim/red/yellow 已消费，
//     green/cyan/magenta/fg/bg 预留（diff 卡/选择高亮随后续任务）。
//  2. 作用域——参考切换后持久化用户 config（persistTheme）；本仓会话级内存
//     态，不写 config（D8：FR-014 未要求持久化，config 包本变更零改动）。
//  3. 色彩层探测——参考直连 colorprofile 包；本仓不新增直接依赖，按其环境
//     规则自实现三层探测（NO_COLOR / COLORTERM / TERM=dumb，见 probeColorTier）。
//     256 层 SGR 在更低层终端由 bubbletea 输出管线统一降采样，手选值不被改写。
//  4. 全局单源——参考 activeCLITheme + refreshCLIStyles 同构：主题驻包级
//     activeTheme（块渲染 helper 与 lipgloss 样式无模型访问路径，换肤经
//     applyTheme 统一生效）；环境探测只发生在 Run 入口与 /theme 重建点，包级
//     初值为确定性 256 层——单测 SGR 钉点与初值同源（对照参考 ansiAccent 注记）。
//  5. 槽位形态——brief 签名记作 lipgloss.Color；lipgloss v2 无该类型（Color
//     是 string→color.Color 构造函数），槽位以解析后的字符串形态（themeColor：
//     空 / hex / 256 索引）承载——sgrFg 参数化与单测逐值断言同源，样式构建点
//     经 lipgloss.Color() 构造（refreshThemeStyles）。

// slotColor 槽位色值对：真彩 hex + 手选 256 回退（对照参考 cliColor——成对
// 存放，构建层按色彩层取用）。
type slotColor struct {
	hex   string
	xterm int
}

// themeColor 已解析槽位色：""（无色层）/ "#rrggbb"（真彩层）/ "0".."255"
// （256 层手选索引）。sgrFg 据此参数化 SGR，refreshThemeStyles 据此构造
// lipgloss 颜色。
type themeColor string

// accentPreset 强调色预设（D8：4 强调色起步，结构对齐参考 cliThemeStyles）。
// dark 为暗底主形态（裸名），light 为亮底调校变体（名后缀 -light）。
type accentPreset struct {
	name        string
	dark, light slotColor
}

// accentPresets 强调色预设表：暗色四组移植参考 themeStyles（graphite/ember/
// aurora/midnight）；亮色变体取参考亮板同族强调色（graphite←sandstone、
// ember←linen、midnight←porcelain），aurora 亮底无同族参考值、手选暗青
// （#2f8a76 ≈ 256 色 65——手选纪律同参考 cliColor 注记）。
var accentPresets = []accentPreset{
	{name: "graphite", dark: slotColor{"#d97757", 173}, light: slotColor{"#c2613f", 173}},
	{name: "ember", dark: slotColor{"#f06d38", 209}, light: slotColor{"#bd5d4d", 167}},
	{name: "aurora", dark: slotColor{"#34c3a6", 79}, light: slotColor{"#2f8a76", 65}},
	{name: "midnight", dark: slotColor{"#b18cff", 141}, light: slotColor{"#7d63c8", 104}},
}

// modeSlots 底色模式的非强调槽（移植参考 cliDarkTheme/cliLightTheme 对应槽）。
type modeSlots struct{ dim, red, yellow, green, cyan, magenta, fg, bg slotColor }

var (
	darkSlots = modeSlots{
		dim:     slotColor{"#858b96", 245},
		red:     slotColor{"#e0696a", 167},
		yellow:  slotColor{"#d9a441", 179},
		green:   slotColor{"#74b87a", 108},
		cyan:    slotColor{"#56b6c2", 80},
		magenta: slotColor{"#b18cff", 141},
		fg:      slotColor{"#c0c4cc", 251},
		bg:      slotColor{"#222631", 235},
	}
	lightSlots = modeSlots{
		dim:     slotColor{"#82796f", 243},
		red:     slotColor{"#b94b4d", 131},
		yellow:  slotColor{"#b68120", 136},
		green:   slotColor{"#5d9b66", 65},
		cyan:    slotColor{"#2f5fa8", 25},
		magenta: slotColor{"#7d63c8", 104},
		fg:      slotColor{"#555049", 239},
		bg:      slotColor{"#f5f0e8", 255},
	}
)

// theme 语义槽主题（023 FR-014/D8）：name/dark 为身份（/theme 清单与 -light
// 变体命名），其余为槽位。槽位字段带 Color 后缀——Go 不允许字段与方法同名，
// 方法名保留 accent/dim/red/yellow 作为消费面（markdown 渲染器与转写 helper
// 的既有调用点）。
type theme struct {
	name string
	dark bool
	// accent 强调色（标题/列表标记/行内代码/围栏内容/卡点/卡片边框/滚动条 thumb）。
	accentColor themeColor
	// dim 弱化（围栏轨/分隔线/链接目标/状态注记/思考/回执/滚动条 track）。
	dimColor themeColor
	// red 失败（失败卡 ⊘/详情、回合失败与引擎退出收口行）。
	redColor themeColor
	// yellow 告警（warning 级通知、回合取消收口行）。
	yellowColor themeColor
	// green/cyan/magenta/fg/bg 预留槽（diff 加/删、工具读/proc 类别色、正文/
	// 底色——diff 卡与选择高亮随后续任务接入；D8 槽位结构一次成型，扩展无成本）。
	greenColor   themeColor
	cyanColor    themeColor
	magentaColor themeColor
	fgColor      themeColor
	bgColor      themeColor
}

// defaultTheme markdown nil-th 的中性缺省（Task 7 契约保留，槽位表达对齐：
// accent = 基础青 SGR 36、dim = 基础亮黑 SGR 90——经 sgrFg 的 0-15 基础色
// 映射，与既有渲染观感一致）。真实渲染路径经块接口线程化 activeTheme（见
// transcript.go assistantBlock）。
func defaultTheme() *theme {
	return &theme{name: "neutral", accentColor: "6", dimColor: "8"}
}

// accent 强调槽包裹；dim 弱化槽；red 失败槽；yellow 告警槽（SGR 参数串渲染
// 期从已解析槽位推导——空槽原样返回，不着色）。
func (t *theme) accent(s string) string { return sgrWrap(sgrFg(t.accentColor), s) }
func (t *theme) dim(s string) string    { return sgrWrap(sgrFg(t.dimColor), s) }
func (t *theme) red(s string) string    { return sgrWrap(sgrFg(t.redColor), s) }
func (t *theme) yellow(s string) string { return sgrWrap(sgrFg(t.yellowColor), s) }

// sgrWrap 以 SGR 参数串包裹文本（空参数原样返回；对照参考 style.go sgr——
// colorprofile 门控在色彩层选型时完成，此处只做参数化）。
func sgrWrap(params, s string) string {
	if params == "" {
		return s
	}
	return "\x1b[" + params + "m" + s + "\x1b[0m"
}

// sgrFg 槽位前景 SGR 参数串：hex → 38;2;r;g;b（真彩）；0-15 → 基础 16 色
// （30-37 / 90-97——与 xterm 0-15 同值）；16-255 → 38;5;N；空槽/不可解析返回
// 空串（不着色）。
func sgrFg(c themeColor) string {
	s := string(c)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "#") {
		r, g, b, ok := parseHexRGB(s)
		if !ok {
			return ""
		}
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return ""
	}
	switch {
	case n < 8:
		return strconv.Itoa(30 + n)
	case n < 16:
		return strconv.Itoa(82 + n) // 90 + (n-8)：基础亮色 90-97
	default:
		return "38;5;" + strconv.Itoa(n)
	}
}

// parseHexRGB 解析 #rrggbb（对照参考 parseHexColor——预设表与 SGR 参数化共用）。
func parseHexRGB(hex string) (r, g, b int, ok bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	rr, errR := strconv.ParseUint(hex[0:2], 16, 8)
	gg, errG := strconv.ParseUint(hex[2:4], 16, 8)
	bb, errB := strconv.ParseUint(hex[4:6], 16, 8)
	return int(rr), int(gg), int(bb), errR == nil && errG == nil && errB == nil
}

// colorTier 色彩层（对照参考 colorOn/trueColorTerminal 的两层 + colorprofile
// 的 NO_COLOR 规则）。
type colorTier uint8

const (
	tierNone      colorTier = iota // 无色（NO_COLOR / TERM=dumb）——槽位全空，装饰属性保留
	tierANSI256                    // 逐槽手选 256 回退（不做距离降采样，D8）
	tierTrueColor                  // 真彩 hex 直排
)

// probeColorTier 探测当前色彩层（对照参考的 colorprofile 适配——按其环境规则
// 自实现，不新增直接依赖）：
//   - NO_COLOR（ParseBool 真值，https://no-color.org/）→ tierNone；
//   - TERM=dumb → tierNone（无能力终端；windows 常规 TERM 未设不受影响——
//     Windows Terminal/cmd.exe 不定义 TERM）；
//   - COLORTERM=truecolor|24bit → tierTrueColor；
//   - 其余 → tierANSI256（不依赖 TERM 解析：256 层 SGR 在更低层终端由输出
//     管线统一降采样，手选值不被距离算法改写）。
func probeColorTier() colorTier {
	if nc, err := strconv.ParseBool(os.Getenv("NO_COLOR")); err == nil && nc {
		return tierNone
	}
	if os.Getenv("TERM") == "dumb" {
		return tierNone
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return tierTrueColor
	}
	return tierANSI256
}

// resolveSlot 槽位按色彩层选型（对照参考 themeLipColor 的预解析——把回退值
// 直接交给 lipgloss，绕开其距离降采样；无色层返回空槽，SGR 方法与样式双双
// 跳过）。
func resolveSlot(c slotColor, tier colorTier) themeColor {
	switch tier {
	case tierTrueColor:
		return themeColor(c.hex)
	case tierANSI256:
		return themeColor(strconv.Itoa(c.xterm))
	default:
		return ""
	}
}

// buildTheme 组装主题：强调色按名查预设、按模式取槽，非强调槽取底色模式板
// （对照参考 buildCLITheme/applyCLIThemeStyle 的「底板 × 强调色」合成）。
func buildTheme(accentName string, dark bool, tier colorTier) (theme, bool) {
	accentName = strings.ToLower(strings.TrimSpace(accentName))
	for _, p := range accentPresets {
		if p.name != accentName {
			continue
		}
		accent, mode := p.dark, darkSlots
		name := p.name
		if !dark {
			accent, mode = p.light, lightSlots
			name += "-light"
		}
		return theme{
			name:         name,
			dark:         dark,
			accentColor:  resolveSlot(accent, tier),
			dimColor:     resolveSlot(mode.dim, tier),
			redColor:     resolveSlot(mode.red, tier),
			yellowColor:  resolveSlot(mode.yellow, tier),
			greenColor:   resolveSlot(mode.green, tier),
			cyanColor:    resolveSlot(mode.cyan, tier),
			magentaColor: resolveSlot(mode.magenta, tier),
			fgColor:      resolveSlot(mode.fg, tier),
			bgColor:      resolveSlot(mode.bg, tier),
		}, true
	}
	return theme{}, false
}

// themeByName 按名解析主题（/theme 消费面）：裸名 = 暗底主形态，-light 后缀 =
// 亮底变体，-dark 后缀显式等价裸名；未知名（含套娃后缀）false。
func themeByName(name string) (theme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	dark := true
	if base, ok := strings.CutSuffix(name, "-light"); ok {
		name, dark = base, false
	} else if base, ok := strings.CutSuffix(name, "-dark"); ok {
		name = base
	}
	return buildTheme(name, dark, probeColorTier())
}

// themeCatalogList 可用主题清单（/theme 无参与未知名的留痕面——程序化生成，
// 与预设表同源不漂移）。
func themeCatalogList() string {
	names := make([]string, 0, len(accentPresets)*2)
	for _, p := range accentPresets {
		names = append(names, p.name, p.name+"-light")
	}
	return strings.Join(names, " ")
}

// defaultThemeName 出厂缺省强调色（graphite 暗——参考默认主题同款）。
const defaultThemeName = "graphite"

// defaultActiveTheme 出厂缺省主题：graphite 暗 · 确定性 256 层（不探测环境
// ——单测 SGR 钉点与包级初值同源；环境适配只在 Run 入口与 /theme 重建点生效，
// 见 probeColorTier）。
func defaultActiveTheme() theme {
	t, _ := buildTheme(defaultThemeName, true, tierANSI256)
	return t
}

// activeTheme 当前会话主题（对照参考 activeCLITheme——包级单源：块渲染
// helper 与 lipgloss 样式无模型访问路径，换肤经 applyTheme 统一生效）。
var (
	activeTheme = defaultActiveTheme()
	styleStatus = lipgloss.NewStyle()                                                // 状态栏（dim 槽着色，refreshThemeStyles 重铸）
	styleCard   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1) // 模态卡（accent 槽边框）
)

// applyTheme 换肤：activeTheme 换本体 + lipgloss 样式重铸（对照参考
// setCLIThemeStyle → refreshCLIStyles）。
func applyTheme(t theme) {
	activeTheme = t
	refreshThemeStyles()
}

// refreshThemeStyles 按 activeTheme 重铸包级 lipgloss 样式：空槽跳过（无色层
// 不着色）。真彩层喂 hex、256 层喂手选索引——lipgloss 收到的是层内直排版，
// 其距离降采样不参与（对照参考 themeLipColor 的预解析意图）。
func refreshThemeStyles() {
	styleStatus = lipgloss.NewStyle()
	if string(activeTheme.dimColor) != "" {
		styleStatus = styleStatus.Foreground(lipgloss.Color(string(activeTheme.dimColor)))
	}
	styleCard = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if string(activeTheme.accentColor) != "" {
		styleCard = styleCard.BorderForeground(lipgloss.Color(string(activeTheme.accentColor)))
	}
}

func init() { refreshThemeStyles() }
