# Feature Specification: P5c PTC 模式——程序化工具调用

**Change**: `015-p5c-ptc`

**Created**: 2026-10-12

**Status**: Draft

**Input**: User description: "P5c:PTC 模式——程序化工具调用(模型写程序组合多步工具调用;脚本宿主选型:goja/expr/Wasm,设计期裁定)"——dsh Code mode 的 Go 对齐。

## 背景(问题陈述)

dsh 的 Code mode(0.1.2-rc.1 改名 PTC)允许模型写一个 TypeScript 程序来组合多步工具调用——将 N 次独立的 LLM 往返压缩为 1 次程序执行。mulix 当前只有逐步工具调用(每 tool_use 一次 LLM 往返)，多步操作延迟高、token 消耗大。

Go 复刻需要选择脚本宿主：**goja**(纯 Go JS 解释器,零 CGO ✓)vs **expr**(受限表达式,非图灵完备)vs **Wasm**(重,安全边界强)。dsh 用 TS——JS 语义最自然，goja 是最接近的 Go 等价物。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 脚本宿主选型与集成 (Priority: P1)

选定脚本宿主(设计期裁定：goja 推荐——纯 Go 零 CGO ✓ 分层矩阵、JS 语义与 dsh TS 对齐、成熟维护)；集成到 `internal/ptc` 新包(沙箱执行环境：注册工具暴露为 JS 函数、执行脚本、收集结果)。

**Why this priority**: 脚本宿主是 PTC 的根基；选型错误后期不可逆。

**Independent Test**: goja 沙箱内执行脚本 → 调用注册的 JS 函数(映射 mulix 工具)→ 收集结果 → 无文件系统/网络逃逸。

**Acceptance Scenarios**:

1. **Given** 沙箱内注册了 echo 工具, **When** 执行 `echo("hello")`, **Then** 返回 "hello"(工具调用走 mulix Tool.Execute 路径)。
2. **Given** 脚本试图访问 `require("fs")` / `fetch()` / Go 标准库, **When** 执行, **Then** 沙箱拒绝(undefined / fail-closed)。

### User Story 2 - PTC 工具注册(引擎可见面) (Priority: P2)

`ptc_execute` 工具注册到 registry(引擎可见面)：模型提交 PTC 脚本(参数=脚本文本)→ 沙箱执行 → 结果返回。工具走既有治理回路(guard/审批/镜像)。

**Why this priority**: PTC 的引擎可见入口；治理回路复用既有架构。

**Independent Test**: 注册 `ptc_execute` → 引擎 tool_use 调用 → 沙箱执行 → 结果回引擎(既有 e2e 管线)。

**Acceptance Scenarios**:

1. **Given** ptc_execute 注册, **When** 引擎 tool_use 调用, **Then** 沙箱执行脚本并返回结果(经治理回路)。
2. **Given** 脚本执行超时, **When** 超时阈值(设计期定，缺省 30s), **Then** fail-closed 中断+错误返回。

### User Story 3 - profile 接线 (Priority: P3)

PTC 仅在 standard profile 下注册(minimal 不注册——薄环境评测不需要)；`ctx.profile` 消费。

**Why this priority**: profile 消费的首个真实场景(014 铺垫)。

**Independent Test**: standard profile → ptc_execute 注册；minimal → 不注册。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: 脚本宿主 = goja(纯 Go JS ES5+ 解释器；零 CGO ✓ 分层矩阵；MIT ✓)。
- **FR-102**: `internal/ptc` 新包:沙箱执行环境(注册工具暴露为 JS 函数、超时、结果收集)。
- **FR-103**: 沙箱安全:无 require/fetch/文件系统/网络逃逸；全局对象白名单。
- **FR-104**: `ptc_execute` 工具注册(standard profile only)；参数=脚本文本；超时 30s 缺省。
- **FR-105**: 工具调用走既有治理回路(guard/审批/镜像)。
- **FR-106**: 全量门禁绿；零第三方新增(除 goja 已批准)。
- **FR-107**: 分层矩阵登记 `internal/ptc: {internal/registry}`。

## Key Entities *(include if feature involves data)*

- **PTC 沙箱**: goja.Runtime 实例(注册工具暴露为 JS 函数；超时中断)。
- **ptc_execute**: 引擎可见工具(参数=脚本文本；执行 PTC 脚本并返回结果)。

## Success Criteria *(mandatory)*

- **SC-001**: 沙箱执行测试(工具调用/逃逸拒绝)全绿。
- **SC-002**: ptc_execute 注册+e2e(治理回路)绿。
- **SC-003**: profile 接线(standard 注册/minimal 不注册)。
- **SC-004**: 全量门禁绿。

## Assumptions

- goja 为推荐宿主(设计期裁定，可变)；不支持 ES6+ 模块/async/await(ES5+ 子集)。
- PTC 工具调用经既有 Tool.Execute 路径(不走 HTTP/SSE)。
- 脚本由模型生成(不可信输入)——沙箱是安全边界，非信任边界。

## Out of Scope

- P5d(tool-cordis 自指工具)。
- ES6+ / 模块系统 / async-await。
- PTC 脚本的持久化/版本管理。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->
