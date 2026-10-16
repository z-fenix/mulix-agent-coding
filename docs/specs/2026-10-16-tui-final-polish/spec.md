# Feature Specification: TUI 收尾打磨（023 终审路由项）

**Change**: `2026-10-16-tui-final-polish`

**Created**: 2026-10-16

**Status**: Draft

**Input**: User description: "修复 2. 终审路由的后续小变更：resize 阅读锚点、死代码清理三件、EngineExited 工具卡清扫"

> 背景：023（TUI 重构）终审路由的三项遗留，全部有终审裁定与 023 设计 §7.4 依据。本变更只做这三件，不扩范围。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - resize 后阅读位置保持（FR-006 尾项清偿） (Priority: P1)

用户上翻阅读历史时调整窗口宽度：转写按新宽度重排后，视口停在用户正在阅读的内容附近（按块锚定：重排前记录视口顶行所属块与块内偏移，重排后恢复对应行位），而非跳回底部或随机位置。跟随态用户不受影响（仍尾随）。

**Why this priority**: 023 spec FR-006「尽量保持」的唯一未清尾项；终审裁定路由本变更。

**Independent Test**: 单测——溢出转写、上翻至中部、宽度变更，断言视口顶行仍对准同一块内容；真机可并入下次冒烟不强制。

**Acceptance Scenarios**:

1. **Given** 转写溢出且用户上翻至中部块 X，**When** 窗口宽度变化触发语义重排，**Then** 视口顶行对准块 X 重排后的对应行（±1 行容差），跟随态不翻转。
2. **Given** 用户处于尾随态，**When** 宽度变化，**Then** 行为与现状一致（尾随不丢）。

### User Story 2 - 引擎退出后运行中工具卡终态清扫 (Priority: P1)

引擎崩溃（EngineExitedEvent）时，所有仍处「运行中」的工具卡立即收口为明确的「中断」终态（带 `引擎退出` 详情），不再永久渲染「· 运行中」。

**Independent Test**: 单测——运行中卡片 + EngineExited 事件 → 卡片终态断言；覆盖矩阵行更新。

**Acceptance Scenarios**:

1. **Given** 回合中有运行中工具卡，**When** EngineExitedEvent 到达，**Then** 卡片转为中断终态（非「运行中」），详情含引擎退出说明；转写其余行为不变。

### User Story 3 - 死代码清理 (Priority: P2)

删除 023 终审确认的三处死代码：`clearWrapCache`（无调用方）、`useLegacyViewportScrollClear`（T21 帧管线换装后无生产消费方）、`lines(width)`（test-only 生产代码——删除后其测试改用替代观测面）。纯删除，无行为变化。

**Independent Test**: 全量套件 + lint 层级绿；`go vet` 无未使用符号告警。

**Acceptance Scenarios**:

1. **Given** 三处符号删除，**When** 全量构建/测试/lint，**Then** 全绿，无残留引用。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 宽度变化重排 MUST 按块锚定恢复阅读位置（跟随态行为不变）；实现形态按 023 设计 §7.4 与参考实现 transcriptResizeAnchor 同构。
- **FR-002**: EngineExitedEvent MUST 将全部运行中工具卡收口为中断终态（含详情），覆盖矩阵行同步更新。
- **FR-003**: `clearWrapCache`/`useLegacyViewportScrollClear`/`lines(width)` MUST 删除，残留引用（含测试）同步清理。

### Key Entities

无新实体；锚定所需中间态（锚点快照）为渲染期局部变量。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 三项均有先红后绿的单测（锚定/清扫/删除编译）；全量 `go test ./... -count=1` + vet + gofmt + lint 层级绿。
- **SC-002**: 现有覆盖矩阵与治理测试语义不变（清扫仅新增 EngineExited 分支行）。

## Assumptions

- 沿用 023 的接缝与测试模式（纯消息驱动）；不引入新依赖；引擎侧 PowerShell 旁路由并行会话处理（本变更不触碰 internal/engine）。

## Out of Scope

- 引擎侧任何改动；台账其余 deferred minors（终审已 triage 可带病合并）。

## Clarifications

### Session 2026-10-16

- 澄清跳过裁定：三项内容与实现口径均由 023 终审（fable 全分支评审）裁定并记录在案（ledger 路由清单 + 设计 §7.4 + 参考实现锚点机制），无未决分歧；跳过 clarify 阶段，下游返工风险由「实现口径已终审定案」兜底。
