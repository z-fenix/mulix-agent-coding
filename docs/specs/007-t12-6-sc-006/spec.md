# Feature Specification: T12-6 SC-006 计数修正

**Change**: `007-t12-6-sc-006`

**Created**: 2026-10-11

**Status**: Draft

**Input**: User description: "P3 路由⑤ T12-⑥:SC-006 计数修正(4+3→5+3)"——P2 验收台账的文档准确性修正。

## 背景(问题陈述)

P2 验收文档对 SC-006(自定义钩子事件审计)的测试计数**两处不一致**:

- `docs/changes/002-p2/report.md`(SC-006 行):「guard **5** 测 + cli 3 测」;
- `docs/evidence/p2-acceptance.md`(SC-006 行):「guard **4** 测 + cli 组合根 3 测」。

仓库实际:`internal/guard` 钩子脚本消费核心测试 5 个(`TestNewScriptConsumerValidation`、`TestScriptConsumerExecArgvVerbatim`、`TestScriptConsumerJSONResponseAccepted`、`TestScriptConsumerTimeoutDegrades`、`TestScriptConsumerCrashDegrades`)+ 组合根 cli 3 测(`internal/cli/hooks_wiring_test.go`)。report.md 的 5+3 与事实一致,p2-acceptance.md 的 4+3 为计数笔误——台账准确性是 P2 终评确立的治理要求(T12 SC-006「过度声明」教训的同类面),笔误必须修正。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 台账计数与事实一致 (Priority: P1)

维护者核对 P2 验收台账的 SC-006 测试计数:以仓库实际测试函数为准(grep 可复核),`p2-acceptance.md` 的 SC-006 行修正为 5+3,与 `report.md` 一致;修正为最小改动(仅该计数),并以核对证据(实际测试函数清单)佐证。

**Why this priority**: 单点笔误修正,一次到位。

**Independent Test**: grep 验收文档中的计数陈述与 `internal/guard`/`internal/cli` 实际测试函数清单一致;两文档间不再矛盾。

**Acceptance Scenarios**:

1. **Given** 仓库实际钩子测试清单, **When** 核对两份台账, **Then** `p2-acceptance.md` SC-006 行计数修正为 5+3(与 report.md 一致),其余内容不动。
2. **Given** 修正后的台账, **When** 复核, **Then** 计数与可复核事实一致(测试函数名入档)。

### Edge Cases

- 核对时发现计数口径分歧(如 HelperProcess 模式入口算不算一个「测」)时怎么办?——计数口径以 report.md 既有口径(5+3,脚本消费核心测试)为准;口径说明随修正一行入档,避免再次漂移。
- 核对时发现其他计数错误时怎么办?——只修本变更范围内的 SC-006 计数;其他 discrepancies 如实记录路由,不扩散。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: `docs/evidence/p2-acceptance.md` SC-006 行的钩子测试计数 MUST 从「guard 4 测」修正为「guard 5 测」(5+3),与 report.md 及仓库事实一致。
- **FR-102**: 修正 MUST 附核对依据(实际测试函数清单),口径与 report.md 一致。
- **FR-103**: 修正 MUST 最小化:仅该计数与口径说明,不动 SC-006 行的其他断言。
- **FR-104**: 全量门禁 MUST 保持绿;除该文档外无文件变更。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 两份台账 SC-006 计数一致(5+3)且与仓库实际测试函数清单一致。
- **SC-002**: 修正最小化(git diff 仅 p2-acceptance.md 一处计数+口径说明);门禁绿。

## Assumptions

- 计数口径沿用 report.md 既有陈述(脚本消费核心测试 5 个;HelperProcess 模式入口不计入——它不是断言测试而是测试基础设施)。
- 本变更为文档准确性修正,无代码改动。

## Out of Scope

- P3 路由其余项(⑥⑦⑧⑨ 及廉价测试补强批次)。
- 其他文档的计数/陈述核对(如有发现,记录路由不修)。

## Clarifications

### Session 2026-10-11

- Q: 台账计数的口径如何定(P2 路由笔记的 5+3 与当前仓库事实 8+4 非 helper 测试不符)? → A: **历史口径**(裁定 A)——p2-acceptance.md 修正为 5+3 与 report.md 一致,口径说明明确标注「report.md 既有口径(历史约定),不等于当前 grep 事实」;实际非 helper 测试 guard 8 + cli 4 的差异如实入档并路由(不追溯改写 P2 历史记录;台账是时点记录)。FR-102 的「核对依据」相应改为:入档实际清单与 5+3 口径的差异说明。
