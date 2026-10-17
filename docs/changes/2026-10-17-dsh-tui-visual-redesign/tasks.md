# mulix TUI 重设计（dsh 设计语言与布局移植）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 把 internal/tui 从 Reasonix 单列视觉升级为 dsh 设计语言的三栏终端形态（令牌换装 + 满帧铺底 + 三档布局 + 会话侧栏 + 信息面板 + 统一符号表），治理与交互零回归。

**Architecture:** 主题槽位在 023 三层机制上扩充并整体换源为 dsh 派生值（theme.go）；frame 从单列合成改为「侧栏列 ‖ 主列（转写+底部区） ‖ 面板列」三列 JoinHorizontal（layout.go），窄档 <100 列退化为现状等价单列；侧栏/面板为新文件 sidebar.go/panel.go，经 focusZone 聚焦模型（F2/F3/F4/F6）路由键位；agents/doctor/cache 模态收编为面板页签（键位语义平移，转写行为行不变）。

**Tech Stack:** Go 1.27、charm.land/bubbletea/v2 + lipgloss/v2（既有）、charmbracelet/x/ansi（既有）。零新第三方依赖。

**Spec:** `docs/specs/2026-10-17-dsh-tui-visual-redesign/spec.md`
**Design:** `.mulix/.runtime/2026-10-17-dsh-tui-visual-redesign/specs/2026-10-17-dsh-tui-redesign-design.md`

## Global Constraints

- Go 1.27；本变更零新增第三方依赖（设计反目标）。
- 治理零回归：审批独占键位（1/y/2/a/n/Esc）与治理路径逐字节不变；治理/独占态测试断言语义不修改。
- 既有交互语义不变：/ 命令、Ctrl+B 展开、Ctrl+C 阶梯、Esc 取消阶梯、流式、折叠、补全；事件渲染矩阵行为行不删减。
- 界面文案保持中文；面板不造数（无数据源整体省略）。
- 界面不拦截 1–9 数字键进输入（面板/侧栏非聚焦态不截获键位）。
- NO_COLOR/TERM=dumb 无色层：槽位全空、无 bg 铺底、glyph/结构/布局分区全部保留。
- 主题不持久化（023 D8）；stdout Sink 不在本变更面。
- 每任务 `go test ./... -count=1` 全绿 + `go vet ./...` 干净 + `gofmt -l` 空 + `go test ./internal/lint/ -count=1` 过（分层）后 commit；只 add 本任务文件；`.mulix/.runtime/**` 不入提交。
- 分层纪律：internal/tui 为既有登记包，不新增跨层 import。

## Review Focus

1. **窄端边界（W=99/100/129/130）**：三档判定与 F2/F3/F4 守卫在阈值 ±1 处不崩、不出现 0 宽列——期望：99 列单列 + 守卫提示行；130 列三栏且主列 ≥80。→ Task 9 `TestColumnWidthsThresholds`。
2. **NO_COLOR 满帧结构**：无色层下铺底消失但三列构成、面板/侧栏内容、glyph 全部保留——期望：SGR 输出空集且 frame 行数/分区与有色层一致。→ Task 3 `TestNoColorFrameStructure`。
3. **CJK 宽文本 × 铺底换行**：含中文的正文行在 surface bg 整行填充后换行数与显示宽计量不变（wrapcache 契约）——期望：同一 CJK 段落铺底前后 wrappedLines 行数相等。→ Task 5 `TestCJKWrapUnchangedWithBG`。
4. **审批独占态拦截面板键**：审批卡在场时 F2/F3/F4/F6/↑↓Enter 一律被独占态吞掉、不路由面板——期望：审批键位矩阵逐字节不变。→ Task 17 `TestApprovalInterceptsPanelKeys`。
5. **换绑后侧栏/面板数据不串场**：swapSession 成功后侧栏高亮指向新会话、变更聚合为空——期望：旧会话条目零残留。→ Task 14 `TestSwapResetsChangesPanel`。

---

## Task 1: 主题槽位扩充 + dsh dark 派生值

**Files:**
- Modify: `internal/tui/theme.go`
- Modify（修钉：槽位值变更打红的既有 SGR 钉点，仅形态钉点、行为断言不动）: `internal/tui/mapping_test.go:104,163,238,362`、`internal/tui/diffview_test.go:106,124,127,130`、`internal/tui/toolcard_test.go:89-90`、`internal/tui/markdown_test.go:221`
- Test: `internal/tui/theme_test.go`

**Interfaces:**
- Consumes: 既有 slotColor/themeColor/resolveSlot/sgrFg/buildTheme（theme.go 现有）。
- Produces: `theme` 新槽位字段（全部 `themeColor`）：`surface1Color, surface2Color, surface3Color, borderColor, fgSecondaryColor, fgTertiaryColor, codeBlockColor, diffAddedBGColor, diffAddedMarkerColor, diffDeletedBGColor, diffDeletedMarkerColor`；既有 `bgColor`（满帧铺底）、`fgColor`（正文主色）语义升级；`modeSlots` 同步扩充；`func sgrBg(c themeColor) string`（48;2 / 48;5，空槽返回 ""）；dark 槽位值 = 设计 §4.1 表。

- [X] **Step 1: Write the failing test** — `theme_test.go` 追加（256 层确定性初值口径）：

```go
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
func TestSgrBg(t *testing.T) {
	if got := sgrBg("234"); got != "48;5;234" { t.Fatalf("48 层: %q", got) }
	if got := sgrBg("#151517"); got != "48;2;21;21;23" { t.Fatalf("真彩层: %q", got) }
	if got := sgrBg(""); got != "" { t.Fatalf("空槽: %q", got) }
}
```

（`assertSlot` 为测试文件既有或新增的槽位字符串比较 helper。）

- [X] **Step 2: Run test to verify it fails** — `go test ./internal/tui/ -run 'TestDSHDarkSlots|TestSgrBg' -count=1`。Expected: FAIL（surface1Color 等字段未定义/编译错误 = 编译期 RED，台账记 Ruling 说明形态）。
- [X] **Step 3: Implement** — theme.go：modeSlots/theme 增字段；darkSlots 按设计 §4.1 填值（bg #151517/234、surface1 #232324/235、surface2 #2C2C2E/236、surface3 #353638/237、border #43454A/238、fg #F9FAFB/231、fgSecondary #CFD3D6/252、fgTertiary #ADB2B8/145、dim #81858C/244、codeBlock #1B1B1C/235、diffAddedBG #1F3124/22、diffAddedMarker #41C977/114、diffDeletedBG #3C1F1B/52、diffDeletedMarker #FA423E/203）；既有 green/red/yellow（amber）槽值同步换 dsh 状态色（green #4ED17E/114、amber #F7AD31/214、red #F25A5A/203——FR-003）；sgrBg 镜像 sgrFg；buildTheme 装配新槽。
- [X] **Step 4: Run test to verify it passes** — 同 Step 2 命令。Expected: PASS。
- [X] **Step 5: 全量门禁 + commit** — 修钉文件一并 add（mapping/diffview/toolcard/markdown 四测试文件，仅 SGR 字面钉点、行为断言不动）

```bash
go test ./... -count=1 && go vet ./... && gofmt -l internal/tui/ && go test ./internal/lint/ -count=1
git add internal/tui/theme.go internal/tui/theme_test.go internal/tui/mapping_test.go internal/tui/diffview_test.go internal/tui/toolcard_test.go internal/tui/markdown_test.go
git commit -m "feat(tui): 主题槽位扩充——dsh dark 派生值与 sgrBg"
```

## Task 2: dsh light 派生值

**Files:**
- Modify: `internal/tui/theme.go`
- Modify（如有 light 侧既有 SGR 钉点打红，同 Task 1 口径修钉）: 涉及 `-light` 主题钉点的测试文件
- Test: `internal/tui/theme_test.go`

**Interfaces:**
- Consumes: Task 1 新槽位。
- Produces: lightSlots 全槽 dsh light 值（下列逐值）；`-light` 主题名解析不变。

- [X] **Step 1: Write the failing test**：

```go
func TestDSHLightSlots(t *testing.T) {
	th, ok := themeByName("graphite-light")
	if !ok { t.Fatal("graphite-light 解析失败") }
	assertSlot(t, th.bgColor, "231")            // #FFFFFF
	assertSlot(t, th.surface1Color, "255")      // bluish-50 #F9FAFB
	assertSlot(t, th.surface2Color, "254")      // bluish-60 #F5F6F7
	assertSlot(t, th.surface3Color, "253")      // bluish-75 #F1F3F5
	assertSlot(t, th.borderColor, "252")        // bluish-150 #E9ECF2
	assertSlot(t, th.fgColor, "16")             // bluish-1000 #0F1115
	assertSlot(t, th.fgSecondaryColor, "241")   // bluish-700 #61666B
	assertSlot(t, th.fgTertiaryColor, "244")    // bluish-600 #81858C
	assertSlot(t, th.dimColor, "246")           // bluish-500 #979DA6
	assertSlot(t, th.codeBlockColor, "255")     // markdown-code-block = bluish-50
	assertSlot(t, th.diffAddedBGColor, "194")   // #E6F4E7（256 手选语义绿晕）
	assertSlot(t, th.diffDeletedBGColor, "224") // #FCE6E2
	assertSlot(t, th.diffAddedMarkerColor, "28")   // #01A241
	assertSlot(t, th.diffDeletedMarkerColor, "124") // #BA2723
	assertSlot(t, th.accentColor, "173")        // graphite-light accent 不变
}
```

- [X] **Step 2: verify RED** — 同 Task 1 命令模式。Expected: FAIL。
- [X] **Step 3: Implement** — lightSlots 按上述值填（真彩 hex 同步给全槽：bg #FFFFFF、surface1 #F9FAFB、surface2 #F5F6F7、surface3 #F1F3F5、border #E9ECF2、fg #0F1115、fgSecondary #61666B、fgTertiary #81858C、dim #979DA6、codeBlock #F9FAFB、diffAddedBG #E6F4E7、diffDeletedBG #FCE6E2、markers #01A241/#BA2723；green/amber/red 槽 light 值同步 dsh light 状态色 #22C55E/35、#DD8629/172、#EC1313/196）。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `test(tui): dsh light 派生槽位`。

## Task 3: 满帧铺底 + 无色层结构保留

**Files:**
- Modify: `internal/tui/theme.go`（refreshThemeStyles 增 bg 样式）、`internal/tui/layout.go`（frame 收口铺底）
- Test: `internal/tui/layout_test.go`、`internal/tui/theme_test.go`

**Interfaces:**
- Consumes: Task 1 `bgColor`/`sgrBg`。
- Produces: `func paintBG(line string, width int) string`（line 显示宽不足 width 时右补空格并整行铺 bg 槽——lipgloss `Background+Width`；bg 槽空（无色层）原样返回）；frame 输出每行均经 paintBG。

- [X] **Step 1: Write the failing test**：

```go
func TestFramePaintsBackground(t *testing.T) {
	// 有色层：frame 每行含 bg SGR（48;5;234 或 38 层等价形态）
	if !frameHasBGEveryLine() { t.Fatal("frame 存在未铺底行") }
}
func TestNoColorFrameStructure(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// 结构保留：行数与分区不变；SGR 空集
	if sgrCount(buildTestFrame()) != 0 { t.Fatal("NO_COLOR 残留 SGR") }
	if !frameStructureIntact() { t.Fatal("无色层结构缺失") }
}
```

（两个 helper 在测试文件内组装最小 frame——现有 tui_test.go 的 frame 测试基建复用。**无色层测试纪律**：包级 activeTheme 为确定性 256 层、不探测环境——NO_COLOR 用例须照 theme_test.go `TestNoColorFallback` 模式，先 `t.Setenv("NO_COLOR","1")` 再 `applyTheme(themeByName("ember"))` 重建主题后断言（照 TestNoColorFallback 真实用法——deepseek 预设 Task 8 才存在）；直接 Setenv 不清槽位。）
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement** — paintBG + frame() 出口统一包 `paintBG(rendered, m.width)`（多行字符串逐行处理）。
- [X] **Step 4: verify GREEN** + 既有 SGR 钉点测试（theme_test/tui_test/mapping_test）同步修钉（断言集合更新，行为断言不动）。Expected: 全 PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 满帧铺底——bg 槽整行填充与无色层结构保留`。

## Task 4: 符号表与工具卡三态

**Files:**
- Modify: `internal/tui/toolcard.go`、`internal/tui/model.go`（委派卡）、`internal/tui/theme.go`（styleKeycap）
- Test: `internal/tui/toolcard_test.go`、`internal/tui/agents_test.go`

**Interfaces:**
- Consumes: Task 1 槽位。
- Produces: 工具卡三态符号 `●`（运行，accent）/`✓`（完成，green）/`✗`（失败，red）；委派卡 ●/✓ 沿符号表、⊘ 失败保留；`styleKeycap = Background(surface3).Foreground(fgSecondary)`。符号表常量：`symBrand="◆" symRun="●" symDone="✓" symFail="✗" symDelegFail="⊘" symCancel="⏹" symNotice="·" symWarn="！" symFold="▸" symPrompt="❯" symConnector="⎿" symThumb="█" symTrack="│"`（toolcard.go 顶部 const 块，后续任务引用）。

- [X] **Step 1: Write the failing test** — toolcard_test 断言：运行中卡头行含 `●` 不含 `· 运行中`；完成卡 `✓`；失败卡 `✗` + red 槽着色；委派卡 `⊘` 失败形态保留；矩阵行（mapping_test）对应行的字面更新。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement** — toolcard 状态符号切换 + 委派卡同表 + styleKeycap；`·` 只留通知。
- [X] **Step 4: verify GREEN**（含矩阵行更新后全绿）。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 工具卡三态符号——dsh 状态点语言`。

## Task 5: transcript 换装（气泡/横幅/头行/围栏）

**Files:**
- Modify: `internal/tui/transcript.go`、`internal/tui/theme.go`（styleBubble）、`internal/tui/markdown.go`
- Modify（修钉）: `internal/tui/chrome_test.go`（userBlock 前缀钉点——`›` 前缀**保留**，补进符号表「用户消息前缀 ›」）
- Test: `internal/tui/transcript_test.go`、`internal/tui/theme_test.go`

**Interfaces:**
- Consumes: Task 1 槽位、Task 3 paintBG。
- Produces: `styleBubble = Background(surface2)`（无边框，1 格内衬）用于 userBlock；**`›` 前缀保留**（气泡内首行前缀，识别冗余线索）；横幅/头行 accent 着色不变结构；围栏底 codeBlockColor；行内代码 accent。

- [X] **Step 1: Write the failing test** — userBlock 渲染行含 surface2 bg SGR（236）且无圆角边框字符；横幅 accent + 铺底；围栏内容行含 codeBlock bg（235）；`TestCJKWrapUnchangedWithBG`：同一含中文段落经 bubble/paintBG 后 wrappedLines 行数与不加 bg 时相等（Review Focus 3 钉点）。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): transcript 换装——用户气泡/围栏底/dsh 文字层级`。

## Task 6: diff 卡 bg 槽消费

**Files:**
- Modify: `internal/tui/diffview.go`
- Test: `internal/tui/diffview_test.go`

**Interfaces:**
- Consumes: Task 1 `diffAddedBGColor/diffDeletedBGColor/diffAddedMarkerColor/diffDeletedMarkerColor`。
- Produces: diff 加行 = `Background(diffAddedBG)` 整行 + 标记/行号 `Foreground(diffAddedMarker)`；删行同构；hunk 间 ⋮ 与 gutter dim 不变；无色层退避平文（现状）。

- [X] **Step 1: Write the failing test** — 渲染含 +/− 行的 diff，断言加行 SGR 含 `48;5;22`、删行含 `48;5;52`、标记前景 114/203；NO_COLOR 下无 SGR。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement** — renderDiff 换装（真彩层喂 hex 同源）。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): diff 卡消费 bg 预留槽——dsh diff 令牌`。

## Task 7: 编辑器/审批卡/状态栏/滚动条换装

**Files:**
- Modify: `internal/tui/composer.go`、`internal/tui/approval.go`、`internal/tui/status.go`、`internal/tui/layout.go`（scrollbarView）、`internal/tui/complete.go`、`internal/tui/resume_picker.go`
- Test: `internal/tui/composer_test.go`、`internal/tui/approval_test.go`、`internal/tui/status_test.go`、`internal/tui/layout_test.go`

**Interfaces:**
- Consumes: Task 1 槽位与 Task 4 styleKeycap。
- Produces: 编辑器 surface1 底 + ❯ accent（symPrompt）；审批卡 accent 边框 + surface1 底 + 键帽样式；状态栏行 1 徽章 accent 底 + bg 槽 fg、模型名 fgSecondary、行 2 caption；滚动条 thumb accent / track dim；补全菜单与 picker surface1 底 + accent 选中。**审批卡键位与文本语义逐字节不变（仅样式）**。

- [X] **Step 1: Write the failing test** — 各渲染点 SGR 钉点（编辑器行 48;5;235、滚动条 thumb = **当前默认 accent 173**——deepseek 111 钉点随 Task 8 落、徽章 accent 底 + 234 前景）；审批卡断言键位行文本逐字节等于现值。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): chrome 换装——编辑器/审批/状态栏/滚动条`。

## Task 8: 主题目录——deepseek 默认 + 四预设重调校

**Files:**
- Modify: `internal/tui/theme.go`、`internal/tui/complete.go:38`（/theme 目录描述串补 deepseek——文案顺手修）
- Modify（修钉：默认 accent 173→111 打红的钉点）: `internal/tui/mapping_test.go:123,306`、`internal/tui/toolcard_test.go:34`、`internal/tui/markdown_test.go:217,220-224`（允许色集合 {173,245} → {111,244}）
- Test: `internal/tui/theme_test.go`（含 /theme 命令钉点 TestThemeSwitch）

**Interfaces:**
- Consumes: Task 1/2 槽位（底板已 dsh 化）。
- Produces: accentPresets 增 `deepseek`（dark #7AAAFF/111，light #4176E6/69）并置于首位；`defaultThemeName = "deepseek"`（包级 activeTheme 初值随之 111——Task 3/7 已按当前默认钉点的测试在此同步修钉）；graphite/ember/aurora/midnight accent 值不变、底板随 dark/lightSlots 已换 dsh；`themeCatalogList()` 程序化生成含 deepseek。

- [X] **Step 1: Write the failing test** — `defaultActiveTheme()` accent = "111"、name = "deepseek"；`themeByName("deepseek")`/`("deepseek-light")` 解析 ok 且 accent 对；catalog 首项 deepseek；既有四名解析不回归。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): deepseek 默认主题与预设目录重推导`。

## Task 9: 三档列宽几何

**Files:**
- Modify: `internal/tui/layout.go`
- Test: `internal/tui/layout_test.go`

**Interfaces:**
- Consumes: 无。
- Produces: `func columnWidths(termW int, sidebarOpen, panelOpen bool) (sidebar, main, panel int)`（评审 Issue 5：纯宽度签名表达不了开合几何——宽档 `sidebar = 16 if sidebarOpen else 0`、`panel = 32 if panelOpen else 0`、`main = W−sidebar−panel`；中档 100–129 `panel 恒 0`、`sidebar = 16 if open else 0`、`main = W−sidebar`；窄档 <100 恒 `(0, W, 0)`）。main 恒 ≥80（开合收窄由档位阈值保证）。档位守卫判定用 W 直接比较（<100 侧栏守卫、<130 面板守卫），不用 sidebar==0（open=false 同样得 0）。

- [X] **Step 1: Write the failing test** — `TestColumnWidthsThresholds`：表驱动（W, sidebarOpen, panelOpen）∈ {80,·,·}×{99,·,·}×{(100,true,false),(129,true,true)}×{(130,四组合)}×{(150,true,true)} 断言三元组；含 Review Focus 1 边界（99→(0,99,0)；130 全开→(16,82,32)；130 关面板→(16,114,0)）。
- [X] **Step 2: verify RED**。Expected: FAIL（未定义）。
- [X] **Step 3: Implement** — 两个阈值条件，无其他逻辑。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 三档列宽几何——dsh columns 让位顺序`。

## Task 10: frame 三列合成 + 窄档等价

**Files:**
- Modify: `internal/tui/layout.go`、`internal/tui/model.go`（`func (m *tuiModel) mainWidth() int` + 底部区宽度收口 + F2/F3 开合字段接线预留）、`internal/tui/status.go`（joinStatusSides 宽度实参）、`internal/tui/composer.go`（SetWidth 实参经 model 传入）、`internal/tui/approval.go`（卡片宽度实参）
- Test: `internal/tui/layout_test.go`、`internal/tui/tui_test.go`

**Interfaces:**
- Consumes: Task 9 `columnWidths(termW, sidebarOpen, panelOpen)`；Task 3 paintBG。
- Produces: frame() = `lipgloss.JoinHorizontal(lipgloss.Top, sidebarCol, mainCol, panelCol)`——mainCol = `JoinVertical(转写视口(宽 main-1), bottom区)`；**底部区宽度收口到主列**（评审 Issue 7）：`mainWidth()` 返回 columnWidths 的 main，composer `ta.SetWidth(mainWidth)`（resize 分支与开合变更时同步）、statusView `joinStatusSides(..., mainWidth())`、approvalCardView `w := mainWidth()-4`、completeMenuView 同理——否则 JoinVertical 撑破帧宽；窄档 (0,W,0) 输出与现状单列构成逐行同构（无侧栏/面板列）；宽度变化（WindowSizeMsg）路径的锚点机制零改写照旧生效；**面板/侧栏开合的重排与锚点捕获在 Task 12 落地**（评审 Issue 4：pendingAnchor 只挂 WindowSizeMsg 宽度变化分支，开合须显式捕获——本任务不涉及）。

- [X] **Step 1: Write the failing test** — `TestFrameThreeTierComposition`：W=150（全开）断言首行含侧栏列区（16 列）与面板列（32 列）+ **帧宽断言：每行显示宽 ≤150**（ansi.StringWidth，防超宽破版）；W=80 输出与单列基线（构造同输入单列渲染）逐行一致。
- [X] **Step 2: verify RED**。Expected: FAIL（构成 + 宽度断言红）。
- [X] **Step 3: Implement** — frame() 按 columnWidths 分派；窄档走现路径（零改写保证等价）；宽/中档空列以空白列合成；底部区四消费点接 mainWidth()。
- [X] **Step 4: verify GREEN**（既有滚动/锚点/矩阵测试全绿）。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): frame 三列合成——底部区收口主列宽、窄档与现状等价`。

## Task 11: 侧栏视图与 F3 开合

**Files:**
- Create: `internal/tui/sidebar.go`
- Modify: `internal/tui/model.go`（字段 `sidebarOpen bool` 缺省 true、F3 键位、视图接线）、`internal/tui/layout_test.go`
- Test: `internal/tui/sidebar_test.go`（新）

**Interfaces:**
- Consumes: Task 10 frame 列位；`Options.ListSessions`（model.go:34）。
- Produces: `type sidebarState struct { sel int }` + `func (m *tuiModel) sidebarView(width int) string`——顶 `◆ mulix`（accent）、「会话」目、清单行（当前会话 surface2 高亮 + fg-primary，其余 fg-secondary 截断省略号）、底部「/new 新会话」提示、空态指引文案；`func (m *tuiModel) toggleSidebar() tea.Cmd`——**W<100 时经 appendLine 留痕「侧栏需 ≥100 列」不进入开态**（评审 Issue 5 修正：档位判定用 W 直接比较；提示走 appendLine 非 appendNotice）。本任务清单数据用桩（Task 13 接真源）。

- [X] **Step 1: Write the failing test** — sidebarView：桩清单 3 项断言高亮行/截断/空态文案；toggleSidebar 在 W=90 时留痕不开态。
- [X] **Step 2: verify RED**。Expected: FAIL（编译期 RED——新符号未定义，见台账 Ruling）。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 侧栏视图与 F3 开合守卫`。

## Task 12: 面板骨架与 F2/F4 守卫

**Files:**
- Create: `internal/tui/panel.go`
- Modify: `internal/tui/model.go`（`panel panelState` 缺省 `{open:true}`、F2/F4 键位、视图接线）
- Test: `internal/tui/panel_test.go`（新）

**Interfaces:**
- Consumes: Task 10 frame 列位与 mainWidth()。
- Produces: `type panelTab uint8` + 常量 `tabChanges/tabAgents/tabDoctor/tabCache`；`type panelState struct { open, focus bool; tab panelTab; sel int; previewPath string; scroll int }`（**open 缺省 true 须在 newTuiModel 显式初始化**——Go 零值 open:false）；`func (m *tuiModel) panelView(width int) string`——页签行（active accent + 其余 dim）、分隔线、空态指引；`func (m *tuiModel) togglePanel() tea.Cmd`（**W<130 时经 appendLine 留痕「面板需 ≥130 列」**）；`func (m *tuiModel) cyclePanelTab()`（W<130 时留痕）。本任务各 tab 内容区为空态占位（Task 13–16 落内容）。
- **本任务追加（评审 Issue 4 移入）**：侧栏/面板开合路径**显式捕获 pendingAnchor**（重排前 `anchorForOffset` 记录视口顶行块位、重排后 `offsetForAnchor` 恢复——不能依赖 WindowSizeMsg，同宽开合不触发 resize 路径）；`TestAnchorSurvivesPanelToggle`：溢出转写上翻中部、同宽（150→150）面板开合，视口锚回同块（±1 行）。

- [X] **Step 1: Write the failing test** — panelView 页签行断言（active accent）；W=120 togglePanel 留痕；cyclePanelTab 环绕。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 右栏面板骨架——页签与开合守卫`。

## Task 13: 侧栏会话清单接线 + 聚焦恢复

**Files:**
- Modify: `internal/tui/sidebar.go`、`internal/tui/model.go`
- Test: `internal/tui/sidebar_test.go`、`internal/tui/tui_test.go`

**Interfaces:**
- Consumes: Task 11 sidebarState；`Options.ListSessions() []SessionSummary`（swapper.go:39 类型）；`swapSession(resumeID string)`（model.go:1012）。
- Produces: sidebarState 增 `sessions []SessionSummary`（打开时刷新快照，同 picker 口径：打开时刻快照不随 Scan 变化）；聚焦侧栏时 ↑/↓ 环绕移动 sel、Enter 调 `swapSession(items[sel].EngineID)`——**镜像 picker 的空 EngineID fail-closed 守卫**（resume_picker.go:69-74 同款：EngineID 为空 → appendLine 留痕拒绝，不调 swap）；当前会话（selfID 匹配 SessionSummary.ID）行高亮。测试经 newTestModel 注入桩 ListSessions（现构造器不设该回调——测试内显式赋值 m.opts.ListSessions）。

- [X] **Step 1: Write the failing test** — 桩 ListSessions 3 项：清单渲染同谓词输出；↑/↓ 移动 sel；Enter 触发 swapSession 桩记录实参 = 选中项 EngineID；当前会话高亮。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 侧栏会话清单——与 resume 同源、聚焦恢复`。

## Task 14: 变更聚合 + 变更 tab

**Files:**
- Modify: `internal/tui/panel.go`、`internal/tui/model.go`
- Test: `internal/tui/panel_test.go`

**Interfaces:**
- Consumes: Task 12 panelState；toolCard 的 `tool/diff/state/arg` 字段（toolcard.go）。
- Produces: **聚合缓存制**（评审 Issue 3 修正：换绑不重置转写块流——model.go:256-287 只清 thinking/answer/cancel，块流持久是现行为且属 023 钉定语义，本变更不改）：`type changeEntry struct { path string; added, deleted int; card *toolCard }`；model 增 `changes []changeEntry` 缓存——工具卡收口处（ToolResultEvent 关卡 upsert 点）对写类 done 卡增量维护（按 path upsert、+/− 累计，计数口径与 renderDiff 同源），**swapDoneMsg 处理时清空 `m.changes`**（面板聚合态归零，转写不动）；`func (m *tuiModel) collectChanges() []changeEntry` = 读缓存；变更 tab 渲染条目行（path + `+n` green `−m` red）、↑/↓ 选中 surface2 高亮、Enter 展开/收起 previewPath 的 renderDiff 预览（面板内滚动）；`TestSwapResetsChangesPanel`：桩 swapDoneMsg 后 collectChanges 为空（Review Focus 5 钉点——聚合缓存清空，不触碰转写）。

- [X] **Step 1: Write the failing test** — 桩收口路径（ToolResultEvent 关卡）：2 个不同 path 的 done Edit 卡 + 1 个运行中卡 + 1 个失败卡 → collectChanges 恰 2 条目且计数正确；同 path 两卡累计；渲染行断言；Enter 预览开合；swapDoneMsg 清缓存断言。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): 变更面板——会话级写类聚合与 diff 预览`。

## Task 15: agents tab 收编

**Files:**
- Modify: `internal/tui/panel.go`、`internal/tui/model.go`（agentsOpen 模态迁页签；/agents 命令 → 打开面板至 agents tab）、`internal/tui/layout.go:40`（bottomView agentsOpen 分支收窄）、`internal/tui/status.go:77`（statusMode 文案）、`internal/tui/scrollstate.go:68`（modalOpen 收编口径）、`internal/tui/composer.go:227`（handlePaste 独占门禁引用 agentsOpen——同款收编口径）
- Test: `internal/tui/agents_test.go`、`internal/tui/mapping_test.go`、`internal/tui/status_test.go`、`internal/tui/scrollstate_test.go`

**Interfaces:**
- Consumes: Task 12 面板骨架；`agentsPanelView()` 内容生成逻辑（model.go:960）；`mgr.List()/Subagent()`。
- Produces: agents tab 内容 = 旧模态列表（id|状态|任务|深度）+ ↑/↓ 选择 + c 取消（键位语义平移——聚焦面板 agents tab 时路由同旧 handleAgentsKey）；`agentsOpen` 字段删除，四处引用收编——bottomView switch 收窄为 授权卡/选择器/编辑器；statusMode 聚焦 agents tab 时「agents 页签」；**modalOpen()（scrollstate.go:68 门禁滚轮/手势）与 handlePaste 独占门禁（composer.go:227）同一收编口径 = `panel.open && panel.focus && panel.tab == tabAgents`**（粘贴不泄漏与滚轮不泄漏是同一独占语义的两条门禁，等价独占：composer 冻结、滚轮冻结，范围与旧模态一致）；/agents 命令 = 打开面板（W≥130 守卫外放行）+ 切 agents tab + 聚焦。

- [X] **Step 1: Write the failing test** — 既有 agents 测试迁改：/agents 打开面板 agents tab；↑/↓/c 语义断言保持；矩阵/独占态相关断言更新（composer 冻结条件）。
- [X] **Step 2: verify RED**。Expected: FAIL。
- [X] **Step 3: Implement**。
- [X] **Step 4: verify GREEN**。Expected: PASS。
- [X] **Step 5: 门禁 + commit** — `feat(tui): agents 模态收编面板页签——键位语义平移`。

## Task 16: doctor/cache tab 双呈现

**Files:**
- Modify: `internal/tui/panel.go`、`internal/tui/model.go`
- Test: `internal/tui/panel_test.go`、`internal/tui/mapping_test.go`

**Interfaces:**
- Consumes: Task 12 面板骨架；`doctorDoneMsg{lines []string}`（model.go）；/cache 面板内容生成（现 /cache 输出路径）。
- Produces: model 增 `doctorLines []string`（doctorDoneMsg 到达时存储——转写 appendLine 照旧，双呈现）；doctor tab 渲染 doctorLines（空态「/doctor 命令运行」指引）；cache tab 渲染 /cache 同源内容（缓存投影摘要的常驻展示；空态指引）。/doctor、/cache 转写行为行**不变**。

- [ ] **Step 1: Write the failing test** — doctorDoneMsg 后：转写行仍在（矩阵断言不动）+ doctor tab 含同 lines；cache tab 与 /cache 输出同源；空态文案。
- [ ] **Step 2: verify RED**。Expected: FAIL。
- [ ] **Step 3: Implement**。
- [ ] **Step 4: verify GREEN**。Expected: PASS。
- [ ] **Step 5: 门禁 + commit** — `feat(tui): doctor/cache 页签常驻化——转写双呈现`。

## Task 17: 焦点模型 F6 + 聚焦可见性

**Files:**
- Modify: `internal/tui/model.go`（`focus focusZone`、F6/Esc 扩展、键位路由）、`internal/tui/status.go`（焦点名）、`internal/tui/panel.go`/`sidebar.go`（聚焦边框）
- Test: `internal/tui/keys_test.go`、`internal/tui/status_test.go`、`internal/tui/approval_test.go`

**Interfaces:**
- Consumes: Task 11–16 侧栏/面板交互。
- Produces: `type focusZone uint8` + `focusMain/focusSidebar/focusPanel`；`func (m *tuiModel) cycleFocus()`——主列→侧栏（开）→面板（开）→主列环绕，目标区未开跳过；聚焦区边框线色 accent（未聚焦 border）；状态栏行 2 尾部焦点名（「焦点 侧栏」caption 色，主列焦点省略）；Esc 阶梯扩展：审批独占（不变）→ 聚焦面板 agents/变更交互（Task 15/14 语义）→ 运行态阶梯（不变）→ 空闲：聚焦态先取焦 → 清空输入。**审批在场时 F2/F3/F4/F6/↑↓Enter 全部被独占分支拦截**（Review Focus 4）。

- [ ] **Step 1: Write the failing test** — `TestFocusCycle`：F6 循环路径（面板关时跳过面板）；`TestApprovalInterceptsPanelKeys`：审批在场按 F2/F4/F6/↑，状态与审批态不变且无面板开合（治理矩阵逐字节）；Esc 取焦不关面板；状态栏焦点名。
- [ ] **Step 2: verify RED** — `TestFocusCycle` 应红（F6 未实现）；**`TestApprovalInterceptsPanelKeys` 可能落地即绿**（审批分支在 handleKey 最前 model.go:709-711、handleApprovalKey 未映射键一律忽略 approval.go:34-48——本测试是回归守卫，按 Task 18 口径记台账「守卫型用例不硬造红」）。Expected: FAIL（至少 TestFocusCycle）。
- [ ] **Step 3: Implement** — focusZone/cycleFocus/焦点可见性/Esc 取焦扩展；审批独占分支保持最前。
- [ ] **Step 4: verify GREEN**（含全部键位/治理测试）。Expected: PASS。
- [ ] **Step 5: 门禁 + commit** — `feat(tui): 焦点模型——F6 循环聚焦与可见性`。

## Task 18: 矩阵补全 + 全量门禁收口

**Files:**
- Modify: `internal/tui/mapping_test.go`（矩阵新增行）、`internal/tui/*`（收口漏项）
- Test: `internal/tui/mapping_test.go`

**Interfaces:**
- Consumes: 全部前序任务的渲染面。
- Produces: 事件渲染覆盖矩阵新增行：diff 卡 bg 槽行（写/改）、面板页签行（变更/agents/doctor/cache 空态与内容态）、布局档位行（W=150/120/90/80 四档 frame 构成）、符号行（●/✓/✗ 三态）；行为语义行清点无删减。

- [ ] **Step 1: Write the failing test** — 上述新增行（判别性断言：错误实现必红的宽/色断言）。
- [ ] **Step 2: verify RED** — 逐行核对（部分行可能因前序任务已绿——该情况记台账，不硬造红；现矩阵实为 **21 行**，清点以真实行为准）。
- [ ] **Step 3: Implement** — 修矩阵暴露的漏项（若有）。
- [ ] **Step 4: verify GREEN** — `go test ./... -count=1 && go vet ./... && gofmt -l . && go test ./internal/lint/ -count=1`。Expected: 全绿全净。
- [ ] **Step 5: commit** — `test(tui): 事件渲染矩阵补全——diff/面板/档位/符号行`。

## Task 19: 真机冒烟证据

**Files:**
- Create: `docs/evidence/smoke-tui-dsh-redesign-2026-10-17.md`（日期以实际执行日为准）

**Interfaces:**
- Consumes: 全部前序任务。
- Produces: FR-108 惯例冒烟证据——引擎版本、工作区、时间线、退出码、摘录；场景：启动帧（三栏）、一次含 Read/Write 的回合（工具三态 + diff bg）、审批真实裁决、Esc 取消、面板开合与页签（变更聚合/agents/doctor/cache）、/theme deepseek 切换、暗/亮/NO_COLOR 三形态摘录。

- [ ] **Step 1: 真机执行冒烟**（构建到仓外可运行位置——本机写入管控惯例），逐场景记录。
- [ ] **Step 2: 证据落盘** — docs/evidence 文档含全部清单项；受阻项如实记录不冒充。
- [ ] **Step 3: commit** — `docs(tui): dsh 重设计真机冒烟证据`。
