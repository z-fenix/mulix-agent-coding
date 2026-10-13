# Feature Specification: P5d tool-cordis 自指工具

**Change**: `016-p5d-tool-cordis`

**Created**: 2026-10-12

**Status**: Draft

**Input**: User description: "P5d:tool-cordis 自指工具——kernel_inspect/define/run/stop(Agent 运行时改造框架;安全模型设计期裁定)"——dsh tool-cordis 的 Go 对齐;P5 收官变更。

## 背景(问题陈述)

dsh 的 tool-cordis 是最自指的插件:给 Agent 提供 `cordis_inspect`(巡检运行中进程)、`cordis_define`(现场定义插件)、`cordis_run`(`node:vm` 沙箱执行)、`cordis_stop` / `cordis_undefine` 等工具——Agent 可在运行时检查并改造自己的框架。

mulix 的 Go 复刻需要对齐这一能力面，同时裁定安全模型：**自指工具是治理红线的边界测试**——Agent 能定义/执行/卸载代码，权限治理如何覆盖？

## User Scenarios & Testing *(mandatory)*

### User Story 1 - kernel_inspect(巡检) (Priority: P1)

Agent 经 `kernel_inspect` 工具查询运行时状态：已注册服务键、已挂载插件、事件总线订阅、profile——返回结构化 JSON。

**Why this priority**: 只读巡检无安全风险，是最安全的自指工具；为后续 define/run 提供状态可见性。

**Independent Test**: standard profile 启动 → kernel_inspect → 返回服务键列表(profile/sink/store/meter/gate/registry/memory/agentLoop/llm)。

**Acceptance Scenarios**:

1. **Given** standard profile 会话, **When** kernel_inspect, **Then** 返回 JSON 含全部已注册服务键与 profile 模式。

### User Story 2 - cordis_run(沙箱执行) (Priority: P2)

Agent 经 `cordis_run` 在 goja 沙箱中执行任意 JS 代码(复用 P5c 的 goja 沙箱基础设施)；与 P5c ptc_execute 的区别：ptc_execute 限定为工具调用，cordis_run 不限(JS 语言全量)。

**Why this priority**: 沙箱基础设施已就位(P5c)——增量小;安全裁定=同 ptc_execute(无宿主能力)。

**Independent Test**: 沙箱执行 JS 表达式 → 返回结果；逃逸尝试 fail-closed(复用 P5c 测试矩阵)。

**Acceptance Scenarios**:

1. **Given** cordis_run 注册, **When** Agent 提交 JS 表达式, **Then** 沙箱执行并返回结果。
2. **Given** 逃逸尝试, **When** 沙箱执行, **Then** fail-closed(复用 P5c 安全矩阵)。

### User Story 3 - 安全模型裁定 (Priority: P2)

自指工具的权限治理：kernel_inspect(只读)不额外审批；cordis_run(fail-closed 沙箱)走既有 allow 清单；**不做 cordis_define/undefine/stop**(运行时改框架 = 治理红线——Agent 不能修改自己的权限/工具面)。

**Why this priority**: 安全边界是产品裁定；不做比做更安全。

**Independent Test**: cordis_define/stop 工具不存在于注册面(grep 零命中)。

**Acceptance Scenarios**:

1. **Given** P5d 交付, **When** grep 注册面, **Then** kernel_inspect/cordis_run 存在；cordis_define/undefine/stop 不存在。
2. **Given** 自指工具走治理回路, **When** 审批/镜像检查, **Then** 既有红线(先持久化后发布/1:1)不变。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: `kernel_inspect` 工具注册(registry.Tool)——返回 JSON(服务键列表/profile 模式/事件总线订阅数)。
- **FR-102**: `cordis_run` 工具注册(registry.Tool;复用 P5c goja 沙箱)——参数=JS 表达式;超时 30s 缺省。
- **FR-103**: cordis_define/undefine/stop **不实现**(运行时改框架 = 治理红线)。
- **FR-104**: 自指工具走既有治理回路(guard/审批/镜像)。
- **FR-105**: P5a..P5c 累积 deferred 清偿(逐项核销)。
- **FR-106**: 全量门禁绿；零第三方新增。

## Key Entities *(include if feature involves data)*

- **自指工具**: kernel_inspect(只读巡检)、cordis_run(沙箱执行)。
- **安全裁定**: 不做 define/undefine/stop(治理红线)。

## Success Criteria *(mandatory)*

- **SC-001**: kernel_inspect 返回正确状态。
- **SC-002**: cordis_run 沙箱执行+逃逸拒绝。
- **SC-003**: cordis_define/stop 不存在(grep 零命中)。
- **SC-004**: 全量门禁绿。

## Assumptions

- P5c goja 沙箱基础设施直接复用(cordis_run 同 ptc_execute 的沙箱路径)。
- 自指工具仅在 standard profile 注册(minimal 不注册)。

## Out of Scope

- cordis_define/undefine/stop(治理红线)、运行时框架改造、P6+ 方向。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->

- clarify 跳过(spec 边界自足;设计期裁定如下,计划评审轮 2026-10-13 补记)。
- **cordis_run 工具面**:复用 ptc.Sandbox 后仅暴露 ReadOnly 注册工具(ptc.go 既有治理过滤;cordis_run/ptc_execute 自身 ReadOnly=false 不进沙箱)——「JS 语言全量」指语言面,治理面同 P5c fail-closed。
- **FR-101 事件总线订阅数**:引擎工具面在 serve 子进程,总线桥在前台进程(015 终审 Critical 同款进程拓扑)——kernel_inspect 的 `eventbus_subscriptions` 如实恒 0(前台指标不跨进程暴露);实现期 Ruling 入账。
- **服务键数据源**:服务键真身在前台(assembleKernelContext),经 `MULIX_KERNEL_KEYS` Boot env 桥送达 serve 侧 kernel_inspect(MULIX_MEMORY_SESSION 同款裁定:绝不入 mcp-config;缺席=降级非阻断)。
