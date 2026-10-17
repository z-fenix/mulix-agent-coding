# Feature Specification: 引擎启动语义对齐 claude-agent-sdk（027）

**Change**: `2026-10-17-go-engine-provider`

**Created**: 2026-10-17

**Status**: Draft

**Input**: User description: "不要用 node 启用（黑屏 30s），参考 claude-agent-sdk-python 方式，通过进程调用 claude code——启动语义对齐 SDK"

> 调研基线（两份独立勘察，结论已对账）：(a) claude-agent-sdk-python（D:\workplace\std\claude-agent-sdk-python @ f7547d7）= **进程包装 claude CLI**（spawn + stream-json NDJSON + 控制协议），无 agent loop、无 HTTP 调用——mulix 现架构与官方 SDK 同构；(b) mulix 引擎接缝三层（Host → llm.Provider → EventSource/agentloop）治理核引擎无关。(c) **30s 黑屏根因**：`Host.Start` 阻塞等 CLI 的 init 事件，而实测 init 在首条 user message 之后才发出（claudehost.go:255-257 代码注释钉死：stdin 开 20s 无 init、消息后 1.1s 到达；握手超时窗默认 30s）→ SDK 从不等 init，mulix 等——差异全在此。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 启动即见界面（P1）

用户从任意目录启动 mulix-cli：**1–2 秒内**TUI 完整呈现（横幅/编辑器/状态栏，状态栏显示「引擎就绪中」），不再黑屏等待引擎。引擎 init 事件迟到到达时流式消化（事件保序），状态栏转为就绪，编辑器可提交。

**Why this priority**: 用户报障的直接标的；架构语义变化的核心。

**Independent Test**: 状态机单测（Start 立即返回 + init 迟到路由保序）+ ConPTY 拍帧计时（启动→可交互 <3s）。

**Acceptance Scenarios**:

1. **Given** 冷启动，**When** 运行 mulix-cli，**Then** ≤3s TUI 可交互（状态栏「引擎就绪中」），进程存活。
2. **Given** 引擎 init 在首条 user 消息后到达，**When** init 事件入流，**Then** 事件保序分发（stash/flush）、版本对账完成、状态栏转就绪、提交放行。
3. **Given** 引擎进程秒退（如 CLI 损坏），**When** Start 返回后引擎退出，**Then** fail-closed 报错呈现（✗ 引擎已退出 + 原因），不悬挂。

### User Story 2 - 提交不等 init（SDK 语义对齐） (Priority: P1)

init 到达前用户提交首条消息：user 帧直写 stdin（SDK 方式），CLI 收到后 init（实测 1.1s），治理对账（lock 版本 vs init.CLIVersion）在提交路径上完成；对账失败快速终止会话并镜像留痕（fail-closed 不弱化——从「黑屏 30s 后拒绝」变为「秒级拒绝」）。

**Independent Test**: 单测——init 未到时 Submit 直写 stdin；对账失败路径终止 + trace 留痕。

**Acceptance Scenarios**:

1. **Given** init 未到，**When** 用户提交，**Then** user 帧写入 stdin，提交进入引擎（不被黑屏阻塞）。
2. **Given** init 到达且版本不符 lock，**When** 对账执行，**Then** 会话快速终止 + engine_drift 留痕（--allow-engine-drift 语义不变）。
3. **Given** 引擎从未发 init（损坏 CLI），**When** 用户提交并等待，**Then** 有界超时后 fail-closed 终止（不无限悬挂）。

### User Story 3 - 协议契约对齐 SDK 最小集 (Priority: P2)

进程调用契约对齐 claude-agent-sdk-python 的已验证形态：NDJSON 帧集（既有）、控制信封方向/应答形态（既有）、stdin 关闭时机（result 帧 + 无 in-flight task——SDK 契约）；initialize 控制请求按需评估（mulix 现以 flag 面替代其用途——保持 flag 面，不盲目补子类型）。

**Independent Test**: 协议面差异清单核对（SDK schema vs mulix decoder）——缺失子类型逐条裁定「需补/不需要」。

**Acceptance Scenarios**:

1. **Given** SDK 协议 schema 对照，**Then** 差异清单逐条有「已对齐/语义等价/不需要」结论并入 spec Clarifications。

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `Host.Start` MUST 在进程 spawn + stdio 泵就绪后立即返回；MUST NOT 阻塞等待引擎 init 事件（握手窗阻塞语义退役）。
- **FR-002**: init 事件 MUST 经事件路由流式消化（stash/flush 保序——023 已有机制，改造为非阻塞路径）；版本对账（lock vs init.CLIVersion）MUST 在「首条 Submit 前」完成，失败快速终止 + engine_drift/trace 留痕。
- **FR-003**: TUI MUST 引擎未就绪即渲染；状态栏 MUST 呈现「引擎就绪中」→ 就绪态迁移；就绪前编辑器可见（提交行为按 FR-002）。
- **FR-004**: Submit MUST NOT 等 init——user 帧直写 stdin（SDK 方式）；init 前的治理钩子（无 init 则无法核对引擎身份）由「Submit 前对账 + 有界超时 fail-closed」承接（对账窗默认沿用现有 2s 宽限语义的重定义，具体值设计定）。
- **FR-005**: 握手超时（HandshakeTimeout 30s）阻塞语义退役；引擎进程秒退的 fail-closed 呈现保留（✗ 引擎已退出）。
- **FR-006**: 治理面零弱化：镜像先行/审批独占/120s fail-closed/可慢不可丢/围堵校验（checkGovernedResults）全部不变；版本对账时点迁移不改变「未经对账不得放行治理」的强约束（对账前置到提交路径）。
- **FR-007**: 协议契约差异清单（SDK schema vs mulix decoder）MUST 逐条结论化入 spec Clarifications（已对齐/语义等价/不需要）。

### Key Entities

- **引擎就绪态**（新）：NOT_READY → READY（init 对账通过）→ FAILED（对账失败/引擎退出）；由事件流驱动，状态栏消费。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 冷启动到 TUI 可交互 ≤3s（ConPTY 拍帧计时，三次取中位）。
- **SC-002**: init 迟到路径：单测证明事件保序、对账前置、版本不符快速终止；全量 27 包绿 + vet + gofmt + 分层 lint。
- **SC-003**: 真机冒烟：非仓目录启动 → 立即交互 → 提交 → 回合完成 → 镜像 1:1（沿用 025 拍帧驱动方法），证据落 docs/evidence/。

## Assumptions

- SDK 的 initialize 控制请求不移植（mulix 以 flag 面 + --mcp-config 达成同用途；YAGNI——子类型是 CLI 版本耦合面）。
- 协议帧集/控制信封/权限回路（MCP tools/call）语义等价，保持现状。
- 「直调 Messages API」明确不在本变更（用户已纠正方向：进程调用 claude code）。

## Out of Scope

- 引擎栈更换（API 直调）；SDK 子类型全集移植；reasonix 式进程内 HTTP 引擎；引擎热切换 config 键（产品化引擎选择另立变更）。

## Clarifications

<!-- clarify 阶段填充 -->
