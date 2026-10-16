# Feature Specification: 引擎治理收尾（settingsTemp 泄漏与 Task 流形态核验）

**Change**: `2026-10-16-engine-governance-cleanup`

**Created**: 2026-10-16

**Status**: Draft

**Input**: 用户指令（2026-10-16，原文）：清偿上一变更（2026-10-16-engine-tool-governance-fix）的两项 deferred——① buildArgs 双物化+TMP 异常并发时的 settingsTemp 泄漏（终审 Minor）；② Task 子代理流的 parent_tool_use_id 建模（终审 Declined-to-judge）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Boot 临时文件零泄漏 (Priority: P1)

buildArgs 双物化路径（SettingsJSON 与 MCPConfigJSON 同时非空）中，第二段物化失败时第一段已落盘的临时文件不再泄漏——每次启动尝试无论成败，`$TMP` 不残留 mulix-*.json。

**Why this priority**: 唯一的代码缺陷项；治理 Boot 面的卫生底线。

**Independent Test**: 注入第二段物化失败（seam），断言第一段临时文件已被删除且 buildArgs 返回错误。

**Acceptance Scenarios**:

1. **Given** SettingsJSON 非空且第一段物化成功，**When** 第二段（mcp）物化失败，**Then** 已物化的 settings 临时文件被删除，buildArgs 返回错误。
2. **Given** 两段都成功，**When** Start/Close 收口，**Then** 两文件均被清理（既有行为回归保持）。

---

### User Story 2 - Task 子代理流形态结论落档 (Priority: P2)

对终审 Declined-to-judge 项的核验结论固化为仓库内文档与协议红线：mulix 不传 `--forward-subagent-text`，泵面上引擎原生 Task 只呈现为普通 tool_use→tool_result（父调用即治理面），子代理内部事件不上流；未来任何接线引入该 flag 前必须先做 parent_tool_use_id 治理建模。

**Why this priority**: 核验已得出决定性结论（引擎二进制证据 + mulix 接线核查），落档即可闭环。

**Independent Test**: 文档核验——engine 包协议文档含该事实与红线；grep 确认 mulix 无 `--forward-subagent-text` 接线。

**Acceptance Scenarios**:

1. **Given** engine 包协议文档，**When** 阅读子代理流形态小节，**Then** 含「Task 父调用为治理面/子事件不上流/启用 forwarding 前须建模」三项事实。
2. **Given** 仓内代码，**When** 检索 `--forward-subagent-text`，**Then** 零接线命中（红线由文档声明 + 后续评审把关）。

---

### Edge Cases

- SettingsJSON 为空（理论不发生——治理常量恒注入）时单物化路径回归不受影响。
- 引擎原生 Task 工具调用本身经审批回路裁决（非只读，引擎必问询）；其内部执行引擎自治，与 shell 工具同型——父调用为治理面。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-301**: buildArgs MUST 在任一后续物化失败分支清理本调用已物化的全部临时文件（含 settingsTemp 与 mcpTemp），不留 `$TMP` 残留。
- **FR-302**: 上述失败清理 MUST 有自动化测试覆盖（seam 注入失败，观察第一段文件被删）。
- **FR-303**: engine 包协议文档 MUST 记录子代理流形态结论：Task 父调用为治理面；子代理内部事件不经 mulix 泵（`--forward-subagent-text` 未接线）；启用 forwarding 前必须先做 parent_tool_use_id 治理建模（红线）。
- **FR-304**: engine.lock 与既有治理语义 MUST 零改动（纯收尾变更）。

### Key Entities

- **物化失败清理面**：buildArgs 内的临时文件生命周期（成功→Start 落字段→cleanupTemp；失败→本调用内即清）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-101**: 全量 `go test ./... -count=1` 绿 + `go vet` 干净 + 改动文件 gofmt（LF 副本口径）零偏差。
- **SC-102**: FR-301/302 的新测试先红后绿；FR-303 文档核验可 grep 复证。

## Assumptions

- ② 的核验证据：引擎二进制内 `--forward-subagent-text` 为 opt-in flag（--print+stream-json 才生效）；mulix 全仓零接线（grep 实证）；wire schema 的 `parent_tool_use_id` 仅在 forwarding 启用时填充。
- 引擎原生 Task 的治理姿态（父调用审批、内部自治）与 shell 工具同型，沿用既有裁定不另行建模。

## Out of Scope

- 启用 `--forward-subagent-text` 及 parent_tool_use_id 治理建模（红线已声明，留待真实需求出现时另立变更）。
- 引擎原生 Task 工具的禁用/白名单裁定（当前经审批回路即满足治理；如需收紧属产品裁定）。

## Clarifications

- clarify 跳过（clarify_skipped=true）：两项均为上一变更终审 deferred 的直接清偿，范围与形态由终审记录与核验证据唯一确定。
