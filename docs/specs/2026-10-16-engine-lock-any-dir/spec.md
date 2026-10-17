# Feature Specification: engine.lock 任意目录可执行（026）

**Change**: `2026-10-16-engine-lock-any-dir`

**Created**: 2026-10-16

**Status**: Draft

**Input**: User description: "修复 engine.lock 按 CWD 相对读取导致的『可任意目录执行』缺失"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 从任意目录启动 mulix-cli (Priority: P1)

用户在非仓库目录（如 `C:\Users\Zhang`）运行 mulix-cli：引擎锁按解析链找到并照常校验（版本漂移拒绝/放行语义不变），不再报 `open engine.lock: The system cannot find the file specified`。

**Why this priority**: 直接修复用户报障；锁内容校验语义零改动。

**Independent Test**: 单测解析链优先序 + 真机非仓目录启动冒烟。

**Acceptance Scenarios**:

1. **Given** 工作区根（或其子目录）存在 engine.lock，**When** 从该目录启动，**Then** 使用工作区锁（仓库提交的治理面优先，现行为保持）。
2. **Given** 工作区无 engine.lock、可执行文件同目录有，**When** 从任意目录启动，**Then** 使用 exe 同目录锁。
3. **Given** 两处都无，**When** 启动，**Then** fail-closed 报缺失（错误信息列出已搜索路径）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 锁解析链 MUST 为：工作区根 engine.lock → 可执行文件同目录 engine.lock → 现行相对路径（保持既有错误形态）；命中即止。
- **FR-002**: 锁内容校验（版本漂移拒绝/--allow-engine-drift 放行/损坏拒绝）MUST 语义零改动——本变更只改「在哪找」，不改「怎么校验」。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 解析链表驱动单测（三态）；全量 27 包绿 + vet + gofmt。
- **SC-002**: 真机：从非仓目录启动 go\bin\mulix-cli.exe 成功进入 TUI（锁取 exe 同目录）。

## Assumptions

- 锁仍为工作区治理面：仓库提交的 lock 对该工作区优先（多工作区可各自钉版）；exe 同目录锁是全局安装形态的兜底（装好即任意目录可用）。

## Out of Scope

- 校验语义、--allow-engine-drift、engine.lock 内容格式；嵌入式锁（go:embed）。

## Clarifications

### Session 2026-10-16

- 澄清跳过：解析链设计由用户裁定目标（可任意目录执行）+ 治理优先序（工作区锁优先、exe 同目录兜底、fail-closed 不变）唯一确定；无未决分歧。
