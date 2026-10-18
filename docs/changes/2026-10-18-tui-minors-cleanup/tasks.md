# TUI deferred minors 清理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 落地 dsh 重设计变更归档台账中 26 条修复类 deferred minors（18 测试补强 + 8 生产微改），行为零回归，处置表 61 条全量入 report。

**Architecture:** 两任务批量执行——Task 1 纯测试文件补强（判别性断言逐条），Task 2 生产微改（每处带钉点）。bounded 设计批准于 chat（2026-10-18）：处置表即需求，行为收紧仅防御路径。

**Tech Stack:** Go 1.27、既有 bubbletea/lipgloss 测试基建。零新依赖。

**Spec:** `docs/specs/2026-10-18-tui-minors-cleanup/spec.md`

**Design:** approved in chat（处置表即设计；FR-001/002 逐项清单为准）

## Global Constraints

- 行为语义零回归：收紧仅防御路径；既有断言语义不弱化（修钉仅字面）。
- 每项判别性（错误实现/缺钉必红）；守卫型用例落地即绿的按 T18 口径记台账不硬造红。
- 零新增依赖；不碰 go.mod/go.sum（既有未提交改动非本变更产物）。
- 每任务全量门禁：`go test ./... -count=1 && go vet ./... && gofmt -l internal/tui/ && go test ./internal/lint/ -count=1`（gofmt 须先 `gofmt -w internal/tui/` 归一本仓文件——master 重检出后工作副本为 CRLF 检出态，`git diff` 为空证明纯检出噪声）。
- 就地分支 2026-10-18-tui-minors-cleanup（前变更 Ruling：hook 锚定主仓 state）。

## Review Focus

1. **判别性倒退**：本批全是加钉/收紧——任何一项若落地即绿且无变异/推导证明判别力，即为假钉。→ Task 1/2 各项 Expected 注明判别来源。
2. **行为收紧越界**：moveSidebar 守卫/panelScrollKey 谓词/joinStatusSides 截断三处只许影响防御路径（正常路径逐字节不变）——各带「正常路径不变」反断言。→ Task 2 Step 1 对应用例。
3. ** tripwire 误伤**：TestToolCardSymbols 逐名恰现一次、sgrCount 基础 16 色禁列扩展可能误伤既有合法输出——落地时若红须逐处核对是钉对了还是钉错了。→ Task 1 对应步骤。

---

## Task 1: 测试补强 18 条

**Files:**
- Modify: `internal/tui/theme_test.go`（sgrBg 分支用例、:440 注释精简）、`internal/tui/layout_test.go`（reset-repaint 幸存钉、sgrCount 基础色禁列）、`internal/tui/toolcard_test.go`（符号逐名恰现一次）、`internal/tui/transcript_test.go`（气泡几何溢出钉）、`internal/tui/diffview_test.go`（三元序合成钉、Strip 一致化）、`internal/tui/composer_test.go`（Dress 主题切换支补断言）、`internal/tui/layout_test.go`（中档关侧栏行）、`internal/tui/sidebar_test.go`（SwapGate 冗余断言清理）、`internal/tui/panel_test.go`（零侧省略钉、doctor/cache 色钉、落转写腿钉、seedUsageTurn 提取共享 testutil 或 panel_test 内导出复用）、`internal/tui/markdown_test.go`（注释 245→244）

**Interfaces:**
- Consumes: 既有测试基建（resetTheme/applyTheme/defaultActiveTheme/paintBG/renderDiff/listWindow/frameHasBGEveryLine 等）。
- Produces: 上表 18 项断言在场且判别；seedUsageTurn 共享化后 tui_test/panel_test 双方引用同一 helper。

- [X] **Step 1: 逐项写失败/强化断言**（判别性口径逐项）：
  1. sgrBg：`sgrBg("3")=="31"`、`sgrBg("11")=="91"`、`sgrBg("999")==""`、`sgrBg("#XYZ")==""`、`sgrBg("-1")==""`（判别：基础色映射/防御分支现无覆盖）。
  2. theme_test :440 注释精简为陈述句（纯注释，无断言）。
  3. reset-repaint 幸存钉：frameHasBGEveryLine 增「每行 bgSeq 出现次数 ≥ sgrResetRe 匹配数 + 1」腿（判别：重置点后不重铺即红）。
  4. sgrCount 禁列扩展：40-47/100-107 形态入禁列（判别：主题层发出基础色即红；落地时若既有测试产出基础色 SGR，核对为组件默认样式残留则修测试预期，为主题槽泄漏则报 BLOCKED——不得静默放宽）。
  5. TestToolCardSymbols：13 符号逐名在 pairs 中恰现一次（判别：重复/缺失行写法现被 tripwire 放过）。
  6. 气泡几何钉：userBlock 渲染行显示宽 ≤ width（ansi.StringWidth，CJK 混排行；判别：width-6 算术回退 width-4 即溢出红）。
  7. diff 三元序合成钉：`paintBG(strings.Join(renderDiff(...),"\n"), w)` 含 `\x1b[0m\x1b[48;5;234m\x1b[48;5;22m`（判别：顺序颠倒/帧底后无 diff 底即红；参照 TestFenceBGSurvivesFramePaint 形态）。
  8. TestDiffRowBGSlots 定位器：上下文行定位改 ansi.Strip（判别：夹具含 'm' 行前置时裸 Contains 误分类——以夹具重排验证或以评审口径记录）。
  9. TestComposerDress 主题切换支：切 ember 后 surface1 (48;5;235) 仍铺 + 无 `ESC[40m`/`ESC[37m`（判别：默认样式回归即红）。
  10. columnWidths 增行 `{"中档129只关侧栏", 129, false, false, 0, 129, 0}`（判别：现无覆盖行，实现改动即红）。
  11. TestSidebarSwapGate 删 F6 腿冗余留痕断言（focus 断言承载；纯清理）。
  12. 零侧省略钉：单侧 +n 条目渲染不含 `−0`（判别：无条件双发即红）。
  13. panelEmptyState 删 tabChanges 死 case（生产文件微改，与 Task 2 合并提交亦可——放本项）。
  14. seedUsageTurn 提取共享（消 panel_test/tui_test 播种形状重复；引用同一 helper）。
  15. doctor/cache 矩阵行 28–29 补色钉（内容行槽色——doctor dim/cache 指引 caption；判别：错槽即红）。
  16. W<130 残焦解冻直接钉：聚焦面板→W 降 120→断言 statusMode 非「agents 页签」+ composerCursor 正常（判别：谓词回退即红）。
  17. 落转写腿钉：聚焦+变更+未展开预览 → PgUp/PgDn → panel.scroll 不动 + 转写滚动（判别：误消费即红）。
  18. markdown_test 注释 245→244（纯注释）。
- [X] **Step 2: 逐项 verify RED**（新判别面应红；纯注释/清理项记台账不硬造红）。
- [X] **Step 3: 测试侧实现**（helper 提取、断言落地；不触生产行为——本任务零生产改动）。
- [X] **Step 4: verify GREEN + 全量门禁**。Expected: 27 包 ok + vet + gofmt（先 -w）+ lint。
- [X] **Step 5: Commit** — `test(tui): minors 清理——测试补强 18 条`。

## Task 2: 生产微改 8 条

**Files:**
- Modify: `internal/tui/toolcard.go`（显式 case）、`internal/tui/markdown_test.go`（注释）、`internal/tui/theme_test.go`（TestDeepseekDefault 守卫语义）、`internal/tui/layout.go`（columnWidths return 统一、joinStatusSides 防御截断）、`internal/tui/sidebar.go`（moveSidebar swapping 守卫、selMark 注记）、`internal/tui/keys.go`（注释全角逗号）、`internal/tui/panel.go`（panelScrollKey 谓词收紧、cyclePanelTab 归零 scroll）

**Interfaces:**
- Consumes: Task 1 的钉点（Task 2 生产改动由 Task 1 新增断言护航）。
- Produces: 防御路径收紧三处（moveSidebar/panelScrollKey/joinStatusSides）各带「正常路径不变」反断言。

- [X] **Step 1: 逐项写失败断言**（判别性）：
  1. moveSidebar swapping 守卫：`m.swapping=true` → ↑/↓ 留痕拒绝、sel 不动（判别：现无守卫即红）。
  2. panelScrollKey 谓词收紧：窗外预览（sel 移远致预览出窗）PgUp/PgDn 不消费、落转写；空态 doctor/cache 不消费（判别：现消费即红）。
  3. cyclePanelTab 归零 scroll：滚动后切页签 scroll==0（判别：渍留即红）。
  4. joinStatusSides 防御截断：左+右超 mainWidth 时右组截断至行宽 ≤ mainWidth（判别：现超宽即红；gap≥2 正常路径逐字节不变反断言）。
  5. 显式 case toolRunning（行为等价，编译期；无新钉——记台账）。
  6. columnWidths return 统一（行为等价重构，既有 12 行表护航）。
  7. 注释三处（selMark 同字形异义声明、keys.go 全角逗号、markdown_test 245→244 如 Task 1 未清）。
  8. TestDeepseekDefault 守卫改 `len(fields) != 10` + 注释语义。
- [X] **Step 2: verify RED**（1–4 红；5–8 等价/注释项记台账）。
- [X] **Step 3: 生产实现**（四处行为/结构改动 + 注释）。
- [X] **Step 4: verify GREEN + 全量门禁**。Expected: 全绿全净。
- [X] **Step 5: Commit** — `fix(tui): minors 清理——防御收紧与注释一致化`（按用户裁定改原子提交：8 项各自独立 commit）。

## Task 3: 处置表存档

**Files:**
- Modify: `docs/changes/2026-10-18-tui-minors-cleanup/report.md`（verify 阶段产出，本任务在 tasks.md 记占位；report 由 verify 阶段写）

**Interfaces:**
- Consumes: 上游台账 61 条。
- Produces: report.md 61 行终局处置表（verify 阶段落盘）。

- [ ] **Step 1**: verify 阶段写 report 时按 spec FR-004 产出处置表（本任务为占位锚——执行在 verify）。
