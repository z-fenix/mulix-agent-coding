# Feature Specification: TUI deferred minors 清理（dsh 重设计收尾）

**Change**: `2026-10-18-tui-minors-cleanup`

**Created**: 2026-10-18

**Status**: Draft

**Input**: User description: "处理 Deferred minors：约 40 条全量在台账"——2026-10-17-dsh-tui-visual-redesign 归档时台账 61 条 deferred minors 的用户处置裁定：26 条修复（本变更）、14 条已消解（后续任务已处理，核验在案）、13 条维持记录（理由在案）、8 条记录性关闭（随本变更报告存档）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 修复类 26 条落地 (Priority: P1)

上一变更（dsh TUI 重设计）任务评审沉淀的 26 条 deferred minors 按处置表全部落地：18 条测试补强（分支覆盖/钉点判别力/断言一致性）+ 8 条生产微改（显式 case、守卫、谓词收紧、注释陈旧值）。行为语义零变化——三处行为收紧（joinStatusSides 防御截断、moveSidebar swapping 守卫、面板滚动 no-op 谓词收紧）均有测试钉。

**Why this priority**: 用户唯一裁定项；全部为小改，一批落地。

**Independent Test**: 全量门禁绿 + 新增/强化断言逐条在场（判别性：错误实现必红）。

**Acceptance Scenarios**:

1. **Given** 处置表 26 项，**When** 逐项落地后跑全量门禁，**Then** `go test ./... -count=1`、`go vet`、`gofmt -l internal/tui/`、`internal/lint` 全绿全净。
2. **Given** 每项新增/强化断言，**When** 对照实现复核，**Then** 判别性成立（能红）。

### User Story 2 - 处置表全量存档 (Priority: P2)

61 条处置结论全量写入本变更 report.md（已消解 14 / 修复 26 / 维持 13（逐条理由）/ 记录性关闭 8），上游台账不再悬挂未处置项。

**Independent Test**: report.md 含 61 行处置记录，与台账一一对应。

**Acceptance Scenarios**:

1. **Given** 上游台账 61 条，**When** 对照本变更 report，**Then** 每条有唯一终局处置（无未处置悬挂）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 测试补强 18 条 MUST 落地：T1 sgrBg 分支测试（基础 16 色 40-47/100-107、解析失败、越界返回空）+ theme_test.go:440 注释精简；T3 reset-repaint 幸存钉点（逐行 bgSeq 次数 ≥ reset 匹配数 + 1）+ sgrCount 禁列扩展基础 16 色形态；T4 TestToolCardSymbols 逐名恰现一次（tripwire 收紧）；T5 气泡几何溢出钉点（width−6 算术显示宽断言）；T6 diff 行三元序合成钉（reset→帧底→diff 底经过 paintBG）+ TestDiffRowBGSlots 定位器 ansi.Strip 一致化；T7 TestComposerDress 主题切换支补 surface1/默认样式清零断言；T9 columnWidths 中档关侧栏覆盖行（129,false,false）；T13 TestSidebarSwapGate F6 腿冗余断言清理；T14 变更面板零侧省略钉点（+0/−0 不发出）；T15 panelEmptyState 死 case（tabChanges）删除；T16 seedUsageTurn 测试 helper 提取共享（消除与 tui_test 播种形状重复）；T17 W<130 残焦解冻直接钉（statusMode 非页签 + 组合器解冻）；T18 矩阵行 28–29 doctor/cache 内容色钉；Final 聚焦+变更+未展开预览的 PgUp/PgDn 落转写腿钉。
- **FR-002**: 生产微改 8 条 MUST 落地：T4 toolcard.go header switch 显式 `case toolRunning`；T8 markdown_test.go 注释「轨 245」→244、TestDeepseekDefault 守卫语义精确化（len==10 或语义注释）；T9 columnWidths return 风格统一（mutate-then-single-return）；T13 moveSidebar swapping-first 留痕拒绝（与 cycleFocus 同口径）；T13 sidebarSelMark「› 」符号表注记（与 symUserMsg 同字形异义声明）；T14 keys.go 全角逗号注释修正；Final panelScrollKey 谓词收紧（可滚内容可见才消费——窗外预览/空态不再 no-op 消费）+ cyclePanelTab 归零 panel.scroll（跨页签渍留）；T10 joinStatusSides gap<2 回退路径防御截断右组（行宽不超 mainWidth）。
- **FR-003**: 行为语义 MUST 零回归：上述收紧仅限防御路径（正常路径逐字节不变）；既有全部测试断言语义不弱化（修钉仅字面）。
- **FR-004**: 处置表（61 条终局结论）MUST 全量写入 report.md 并与上游台账一一对应。

### Key Entities

- 处置表（61 条）：上游台账行 → 终局处置（已消解/修复/维持/关闭）的映射记录。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 全量门禁绿：build/test 27 包/vet/gofmt(internal+tui 干净)/lint。
- **SC-002**: 26 项逐条在场且判别（FR-001/002 对照表全勾）。
- **SC-003**: 行为零回归：全量套件绿且治理/矩阵行为行未弱化。
- **SC-004**: 处置表 61 条全量入 report.md。

## Assumptions

- 「处理」= 用户裁定按上文 61 条处置表执行（14 已消解核验、13 维持理由在案、26 修复、8 记录关闭）；③ 维持项的理由（评审已给）不重开。
- 生产微改中的三处行为收紧属防御路径，非产品语义变更。
- 就地分支（同前变更 Ruling：hook 锚定主仓 state）。

## Out of Scope

- ③ 维持记录 13 条（理由在案，不重开）。
- 任何非台账来源的改进。

## Clarifications

<!-- clarify 阶段在此记录问题与裁定 -->
