# Feature Specification: 引擎工具治理旁路修复

**Change**: `2026-10-16-engine-tool-governance-fix`

**Created**: 2026-10-16

**Status**: Draft

**Input**: 用户指令（2026-10-16，原文要点）：调查 mulix 仓库的引擎治理旁路——TUI P3 真机冒烟发现 claude CLI 2.1.300 的 PowerShell 工具调用未经 `mcp__mulix__permission` 审批即直接执行（同会话 Write/Edit 正常触发授权请求，差异指向引擎侧工具分类）。要求：1) 复现并确认；2) 定位 internal/engine 为何未把 PowerShell 纳入审批回路；3) 按 TDD 修复，确保所有引擎工具调用一律经审批回路或显式白名单；4) 更新 engine.lock 相关测试。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Shell 工具调用全部经过审批回路 (Priority: P1)

mulix 会话中，模型通过引擎 shell 工具（Bash / PowerShell）执行的**任何**命令——包括引擎内部分类为「只读」的命令（如 `echo`）——都必须先经 mulix 审批回路裁决后才执行。运营者在镜像里能看到每一次 shell 调用的 approval_request 与 approval_resolved（含引擎原本会静默自动放行的只读命令）。

**Why this priority**: 这是治理红线的主体缺口——引擎自主放行层让 `PowerShell(echo)` 类调用完全绕开 mulix 治理，静默执行（P3 冒烟 run1 t4 实录）。防住它，旁路即闭合。

**Independent Test**: 出仓构建 mulix-cli，在临时工作区发一个「运行 echo」的回合——镜像必须出现该调用的 approval_request/approval_resolved；未经裁决的 shell 调用不得出现 tool_result。

**Acceptance Scenarios**:

1. **Given** 引擎默认权限模式与 mulix 门禁，**When** 模型调用 PowerShell 执行 `echo`（只读类命令），**Then** 引擎先经审批回路询问 mulix，Gate 按 allow 清单/预设矩阵裁决（无人值守时 allow 清单命中即自动放行，未命中即 fail-closed 拒绝），镜像记录完整审批事件对。
2. **Given** 同上，**When** 模型调用 Bash/PowerShell 执行写类命令（如 `Set-Content`），**Then** 行为与现状一致：经审批回路，写类默认询问（attended）或按预设矩阵裁决。
3. **Given** mulix 配置 allow 清单含 shell 条目，**When** 该条目命中，**Then** 调用仍被放行（裁决结果不变），但由 mulix Gate 裁决并全程入审计台账。

---

### User Story 2 - 漏网工具调用的运行时围堵 (Priority: P2)

对**未经审批回路裁决**且不属于显式白名单（mulix 配置 allow 清单、mulix 内置 MCP 工具、只读文件工具）就产生 `tool_result` 的引擎工具调用，mulix 视为治理违规：镜像留痕 + 立即终止引擎会话（fail-closed）。这层不依赖引擎行为——引擎漂移或未来版本引入新的自主放行类时，mulix 侧仍能兜底。

**Why this priority**: 纵深防御。User Story 1 封住已知的放行类；本故事保证任何未知的放行类变成「响亮的失败」而非静默旁路。

**Independent Test**: 构造一个未裁决、非白名单工具的 tool_result 事件（fakehost 注入）——镜像出现治理违规留痕，引擎被终止，后续回合被拒绝。

**Acceptance Scenarios**:

1. **Given** 活跃回合中引擎对某 tool_use 直接回流 tool_result（无对应裁决），**When** 该工具不在白名单，**Then** mulix 发布 `[governance_violation]` 留痕，终止引擎，活跃回合以错误收口，后续 Submit 拒绝。
2. **Given** 活跃回合中 tool_result 对应的调用已被裁决（allow 或 deny），**When** 结果回流，**Then** 会话正常继续（deny 的拒绝结果照常呈现）。
3. **Given** 回合已被本地取消（Esc）后的孤儿 tool_result，**When** 结果在非运行态到达，**Then** 不触发治理违规（既有孤儿防御语义保持）。

---

### Edge Cases

- mulix 配置 allow 清单中的 shell 条目：裁决从「引擎短路」移到「mulix Gate 政策裁决」，放行结果不变，审计面变全。无人值守会话中 allow 命中仍自动放行（不产生人工等待）。
- 引擎 deny 后回流的 is_error tool_result：属于已裁决调用（裁决=deny），不触发围堵。
- mulix 内置 MCP 工具（`mcp__mulix__*` 前缀）由 mulix 自身实现与注册，属白名单构造，不触发围堵。
- 子代理会话与主会话共用同一 Controller 审批/围堵路径，行为一致。
- engine.lock 版本钉与 drift 流程不变（用户裁定）；本变更只改 Boot 面，不改 lock 文件。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-201**: mulix MUST 在 Boot 面向引擎注入「强制询问」规则，使引擎对 Bash 与 PowerShell 工具的任何调用（含引擎内部分类为只读/沙箱可放行的命令）在执行前先经 mulix 审批回路。
- **FR-202**: 强制询问规则面 MUST 纳入 Boot 面指纹（启动面摘要覆盖全部拉起参数）。
- **FR-203**: 注入的强制询问面 MUST 按会话冻结（Boot 面字节稳定：同配置同字节），且不依赖用户配置可变面。
- **FR-204**: 审批回路对 shell 调用的裁决 MUST 继续遵循既有 Gate 语义（allow 清单 → 预设矩阵 → 审批桥 → 无人值守 fail-closed 超时拒绝）。
- **FR-205**: Controller MUST 跟踪每个引擎 tool_use 的工具名与裁决状态，并对「运行态收到、未经裁决、且不属于显式白名单（配置 allow 清单 ∨ mulix 内置 MCP 工具 ∨ 只读文件工具 Read/Glob/Grep）」的 tool_result 判定治理违规。
- **FR-206**: 治理违规 MUST 先留痕（镜像 1:1 审计面，[governance_violation] 前缀、含工具名/调用 id/输入摘要）再终止引擎；终止后活跃回合错误收口，后续 Submit 拒绝。
- **FR-207**: 非运行态到达的孤儿 tool_result（本地取消残留）MUST NOT 触发治理违规（保持既有孤儿防御语义）。
- **FR-208**: 已裁决调用（allow 或 deny）的 tool_result MUST 正常透传，不触发围堵。
- **FR-209**: Boot 面相关测试（参数集逐字、指纹稳定性、wiring 断言）MUST 与新注入面对齐；engine.lock 的 cli_version 钉与 drift 流程 MUST 保持不变（用户裁定 2026-10-16）。

### Key Entities *(include if feature involves data)*

- **强制询问规则面**：注入给引擎的权限设置片段（ask 列表）；会话级冻结、入指纹。
- **裁决台账**：tool_use_id → 裁决（allow/deny/timeout）与来源；既有 resolved 缓存的围堵消费面。
- **治理违规事件**：镜像留痕事件（notice 形态），违规工具、调用 id、输入摘要、处置（终止）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 真机冒烟（出仓构建 + 临时工作区）：「运行 echo」回合的镜像包含该 PowerShell 调用的 approval_request 与 approval_resolved 事件对（修复前该场景零审批事件）。
- **SC-002**: 真机冒烟：写类 shell 命令回合行为与修复前一致（attended 询问 / 预设矩阵裁决）。
- **SC-003**: 全量 `go test ./... -count=1` 绿 + `gofmt -l` 干净 + `go vet ./...` 干净；围堵正例/反例（裁决过、白名单、孤儿、违规）各有自动化测试钉住。
- **SC-004**: Boot 面指纹随注入面变化（同输入同指纹、异输入异指纹），参数集逐字测试含新 `--settings` 面。

## Assumptions

- 引擎（claude CLI）的 ask 规则优先级高于其 allow 规则与内置只读自动放行（实验 r1/r3 已证实，2026-10-16，harness 留存 `Temp\mulix-bypass-repro\`）。
- 只读文件工具（Read/Glob/Grep）由引擎构造即只读，其自动执行可接受（与 mulix 既有治理姿态一致）；其余未列白名单的引擎工具若被引擎自主放行，按 FR-205/206 响亮失败，事后由运营者裁定是否入白名单。
- 强制询问面固定为 mulix 治理默认（不可经用户配置关闭）——治理红线不提供旁路开关。
- 本变更不涉及 TUI 层改动（审批呈现路径不变）。

## Out of Scope

- engine.lock 版本钉调整（维持 2.1.296；drift 流程不变）。
- 只读白名单之外的引擎工具放行类排查与逐项收编（围堵层兜底，后续变更裁定）。
- TUI 对治理违规事件的专门呈现（notice 通道即可承载）。
- CLAUDE_CODE_USE_POWERSHELL_TOOL=0 路径（实验证实其导致引擎拒启，不作修复手段）。

## Clarifications

- clarify 阶段跳过（clarify_skipped=true）：用户已在启动指令与 AskUserQuestion 裁定（2026-10-16「两层都做（推荐）」）中消解了全部方案级歧义；两层语义、白名单构成、engine.lock 不动的裁定原文见 Assumptions 与 FR-209。
