# Feature Specification: P4b-1 核心插件化(上)——kernel 服务化、llm 接缝与引擎清单冻结快照

**Change**: `011-p4b-agent-loop-llm`

**Created**: 2026-10-11

**Status**: Draft

**Input**: User description: "P4b:核心插件化"——P4 程序第二期(上)。用户裁定(2026-10-11,选项 A):**拆两个 change**——本变更(P4b-1)=组合根服务化 + llm.Provider 接缝 + 引擎清单冻结快照(治理不动);**P4b-2(012,另开)**=agent loop 抽取(ctx.agentLoop)+ 镜像→总线桥。

## 背景(问题陈述)

P4a 已交付内核机制(事件总线/inject/effect/Service)。P4b 把 **controller 的手写装配迁上 kernel**——现状:`control.Options` 是一份手写服务容器(HostFactory/Sink/Store/Meter/Gate/Registry/Memory/HookConsumers/...共 14 个依赖字段),组合根(cli)显式构造;agent loop(泵 + 事件分流 + 收口)硬编码于 controller(1837 行/44 方法);引擎抽象(engine.Host)未与「模型适配器」概念对齐;镜像事件不出现在事件总线上(P4a 红线:先镜像后总线,桥属 P4b);工具注册冻结是单层(无插件可逆层)。

P4b 完成后:dsh 的核心形态在 mulix 落地——`ctx.mirror`/`ctx.sink`/`ctx.gate`/`ctx.meter`/`ctx.llm`/`ctx.agentLoop` 服务键就位,组合根只做 profile 级装配;行为**零变更**(全量回归 + 既有 TUI/run/TUI 面逐字节)。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 组合根 kernel 化(治理三层/镜像服务化) (Priority: P1)

组合根(cli/openSession)创建根 kernel Context,把既有依赖(Sink/Store/Meter/Gate/Registry/Memory/Hooks/...)以 **kernel 服务键** Provide(`ctx.sink`/`ctx.store`/`ctx.meter`/`ctx.gate`/...);controller 经 Service 查找消费(Options 结构保留为装配载体但逐字段源自 kernel 服务)。行为零变更。

**Why this priority**: 服务化是后续全部插件化的接线基础。

**Independent Test**: 组合根单测断言根 Context 各服务键就位;全量回归绿(既有 cli/control 测试零语义改动)。

**Acceptance Scenarios**:

1. **Given** 组合根装配会话, **When** 检查根 Context, **Then** sink/store/meter/gate/registry/memory/hooks 服务键全部就位且值与 Options 字段一致。
2. **Given** 既有全量测试, **When** 服务化改造落地, **Then** 全绿(行为逐字节不变)。

### User Story 2 - llm.Provider 接缝与 claude CLI 适配器插件 (Priority: P1)

定义 `llm.Provider` 接缝(引擎对话回合的抽象:提交任务 → 接收事件流);**claude CLI 适配器**作为默认 Provider 插件(内部封装既有 engine.Host/ClaudeHost 机制,stream-json + 控制协议原样);engine.lock 治理随适配器走(漂移治理语义不变);`ctx.llm` 服务键就位。Provider 可经插件替换(本变更不实现其他 Provider)。

**Why this priority**: 模型可替换是 dsh 公式的左半;接缝先行使后续 Provider 插件成为可能。

**Independent Test**: 接缝接口编译期钉(claude 适配器实现之);FakeHost 既有测试路径经 Provider 走通(FakeProvider 包装);全量回归绿。

**Acceptance Scenarios**:

1. **Given** claude CLI 适配器插件挂载, **When** 会话发起回合, **Then** 引擎子进程行为与 P4a 前逐字节一致(stream-json/控制协议/engine.lock 治理不变)。
2. **Given** `llm.Provider` 接缝, **When** 检查, **Then** 接口不含 claude 特有概念(可测试地证明接缝干净:接口方法签名仅含通用回合语义)。

### User Story 3 - 引擎可见清单冻结快照 (Priority: P2)

工具注册双层化落地:插件注册经 effect 可逆;**引擎可见清单在回合开始时冻结快照**(mcp-config 组装取冻结快照)——回合中途的注册/注销不影响已冻结清单,新工具下回合生效。

**Why this priority**: 双层注册是 010 红线修订的落地半边;隔离引擎契约稳定性与插件可逆性。

**Independent Test**: 冻结快照测试(回合开始后注册的新工具不入当轮引擎清单,下回合生效)。

**Acceptance Scenarios**:

1. **Given** 插件在回合中途 effect 注册新工具, **When** 本回合引擎清单核对, **Then** 清单为回合开始冻结快照(新工具下回合生效)。

## Edge Cases

- 服务键缺失的组合根(如测试只注入部分依赖)时怎么办?——controller 必需依赖缺失 → 构造期 fail-closed(沿用既有 NewController 校验,补充 kernel 侧一致性断言)。
- llm.Provider 接缝与 engine.Host 的关系(包装还是替换)时怎么办?——**包装**(适配器内部持有既有 Host 机制;Host 接口与其测试不动)——最小面;替换属未来 Provider 插件的事。
- 冻结快照与 effect 注销竞争时怎么办?——快照取「回合开始时刻」的注册表副本(不可变);回合内注销不影响已冻结清单(语义:引擎拿到的清单按回合开始定)。
- agent loop 抽取与镜像桥(本变更裁定移出)时怎么办?——路由 P4b-2(012),其设计将独立评审。
- 行为兼容的验证边界时怎么办?——以「既有全量测试 + 事件序列一致性」为验收;不新增快照对比工具(人工 diff 由测试承载)。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: 组合根 MUST 创建根 kernel Context 并将既有依赖以服务键 Provide(sink/store/meter/gate/registry/memory/hooks/llm/agentLoop);controller 消费经 Service 查找;`NewController` 必需依赖缺失 → 构造期 fail-closed(既有校验保持)。
- **FR-102**: `llm.Provider` 接缝 MUST 定义(通用回合语义:提交/事件流/收口,无 claude 特有概念);claude CLI 适配器插件 MUST 实现之(内部封装既有 engine.Host 机制,协议与治理原样);engine.lock 漂移治理随适配器走(语义不变)。
- **FR-103**: 工具注册双层化:插件注册经 effect 可逆;引擎可见清单 MUST 取「回合开始时刻」冻结快照(mcp-config 组装源=快照);回合内注销不影响已冻结清单。
- **FR-104**: agent loop 抽取与镜像→总线桥 MUST 路由 P4b-2(012)——本变更不实施(裁定 A)。
- **FR-106**: 行为零变更:既有全量测试(20 包)+ e2e(委派链/崩溃恢复/审批回路)+ 单飞/watcher/收口矩阵 MUST 全绿,事件序列与改造前一致。
- **FR-105**: kernel 红线一致性:持久真相源仍是镜像 events.jsonl(总线 JetStream 持久化为 P4a 已裁定形态);本变更 MUST NOT 引入新的持久化介质。
- **FR-106**: 分层矩阵保持:llm 新包的 import 不越层(适配器 import engine);矩阵登记更新并入 lint 自检。
- **FR-107**: 全量门禁绿(20+ 包/vet/gofmt/layers)。

## Key Entities *(include if feature involves data)*

- **kernel 服务键**: `ctx.sink/ctx.store/ctx.meter/ctx.gate/ctx.registry/ctx.memory/ctx.hooks/ctx.llm/ctx.agentLoop`——组合根装配、controller/插件消费。
- **llm.Provider**: 引擎回合抽象接缝;claude CLI 适配器=默认插件(engine.lock 随行)。
- **路由项(P4b-2)**: agent loop 抽取与镜像→总线桥——裁定 A 移出,独立 change 评审。
- **冻结快照**: 回合开始时刻的工具注册表副本(引擎可见清单来源)。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 根 Context 服务键全部就位且与 Options 一致(单测钉)。
- **SC-002**: claude CLI 适配器实现 llm.Provider(编译期钉);引擎行为逐字节不变(e2e/冒烟回归)。
- **SC-003**: 冻结快照测试落地(快照语义钉);loop/桥路由 P4b-2。
- **SC-004**: agent loop/桥路由 P4b-2(012)记录在案(裁定 A)。
- **SC-005**: 全量门禁绿;分层矩阵 0 违规。

## Assumptions

- controller 的 Options 结构保留(装配载体),kernel 化指**依赖来源与服务键就位**——不强行改写 controller 内部为 ctx 调用(行为零变更优先;内部 ctx 化可渐进)。
- engine.Host 机制(stream-json/控制协议)在适配器内部原样保留——P4b 不重写引擎协议。
- profiles(standard/minimal)属 P4c;本变更只留 profile 挂载点。

## Out of Scope

- P4b-2(012):agent loop 抽取(ctx.agentLoop)、镜像→总线桥。
- P4c:HTTP/SSE 入口插件、profiles/bundles 落地。
- P5:fork/replay/search、PTC 模式、tool-cordis 自指工具。
- 引擎协议重写、llm.Provider 的新 Provider 实现(仅接缝+claude 适配器)、TUI 行为变更。
- 累积 deferred(minor)清单的额外清偿(已有路由)。

## Clarifications

### Session 2026-10-11

- 跳过理由:边界裁定(包装 vs 替换、loop 注入边界、桥失败、冻结竞争、兼容验证)已在 spec 预裁定;仅存的体量歧义经用户裁定(选项 A:拆两个 change)。
- Q: P4b 体量如何切? → A: 拆两个 change——011(P4b-1)=组合根服务化 + llm.Provider 接缝 + 引擎清单冻结快照;012(P4b-2)=agent loop 抽取 + 镜像→总线桥(独立设计评审)。
