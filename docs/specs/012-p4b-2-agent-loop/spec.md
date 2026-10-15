# Feature Specification: P4b-2 agent loop 抽取与镜像总线桥

**Change**: `012-p4b-2-agent-loop`

**Created**: 2026-10-12

**Status**: Draft

**Input**: User description: "P4b-2:agent loop 抽取(ctx.agentLoop)+ 镜像→总线桥"——P4 程序第二期(下),裁定 A 的另一半;P4 收官变更。

## 背景(问题陈述)

P4b-1 已把组合根装配 kernel 服务化。P4b-2 完成剩余两块:

1. **agent loop 硬编码于 controller**:泵循环(逐行解码 → 事件分流 → 治理回调)与 controller 的 44 个方法纠缠——loop 本身不是插件(dsh 形态:`ctx.agentLoop` 服务,loop 可替换)。
2. **镜像事件不上总线**:P4a 红线裁定了双持久面(镜像=审计真相源;JetStream=事件骨干),但桥(镜像事件流 → 总线)未接线——插件无法经总线观测会话事件流,dsh 的「一切协作走事件」在会话面缺席。

同时 controller 现状是事件面的**唯一发布通道**(镜像先行的单写者泵)——抽取不得破坏:先持久化后发布、单写者、1:1 镜像、fail-closed 四红线。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - AgentLoop 接口与默认驱动插件 (Priority: P1)

回合循环抽取为 `AgentLoop` 接口(输入=引擎事件源 Provider + 治理回调集;职责=逐行解码、分流、把治理动作委托回调);默认驱动插件实现之并挂为 `ctx.agentLoop` 服务;controller 经接口消费 loop(治理回调注入),行为零变更。

**Why this priority**: loop 可替换是 dsh 最自指的一环;P5 的 PTC 模式依赖此接缝。

**Independent Test**: 默认驱动挂载下,既有全矩阵(e2e/单飞/watcher/收口/Boot 护栏)全绿且事件序列一致;AgentLoop 接口不 import control 具体类型(分层矩阵保持)。

**Acceptance Scenarios**:

1. **Given** 默认 AgentLoop 插件挂载, **When** 既有 e2e(委派链/崩溃恢复/审批回路)运行, **Then** 全绿,事件序列与抽取前一致。
2. **Given** AgentLoop 接口, **When** 检查其依赖, **Then** 仅含小接口(回调集)与 engine/event 通用类型,无 control 具体类型。

### User Story 2 - 镜像→总线桥 (Priority: P2)

controller publish 尾部(镜像先行之后)单一注入点:把已持久化的事件同步 Emit 到事件总线(subject=事件 kind;payload=原始行)。先镜像后总线顺序可证;桥错误 fail-visible(Notice),不阻断主流程。

**Why this priority**: 插件经总线观测会话事件流——dsh 协作模型的会话面闭环;依赖 P4a 总线与 P4b-1 服务键。

**Independent Test**: 桥测试——镜像写入后,总线上可订阅到同事件(subject 含 kind,payload=原始行);桥注入点唯一(grep);总线发布失败注入 → Notice 可见且回合不受阻。

**Acceptance Scenarios**:

1. **Given** 桥接线, **When** 任一事件经 controller.publish(镜像已写), **Then** 总线 Emit 同事件(subject=`mulix.mirror.<kind>`),顺序与镜像一致。
2. **Given** 总线 Emit 失败(注入), **When** publish, **Then** 镜像已写(不回滚)、Notice(fail-visible)落镜像、回合继续。

## Edge Cases

- AgentLoop 的治理回调集多大(44 方法全注入?)时怎么办?——**按事件分流面收敛**:loop 只需要「解码后的事件 → 治理动作」的单一入口(onEvent(ev)),分流逻辑留在 loop,治理动作经回调集接口(方法数=事件类数,由设计裁定);不把 controller 整体注入。
- 桥的 subject 命名(事件 kind 含点/驼峰)时怎么办?——subject= `mulix.mirror.<kind>`;kind 原样(声明表按 kind 声明);具体命名归设计。
- 桥发布的 double-persist(JetStream 又存一份)时怎么办?——P4a 已裁定双持久面职责分立(镜像=审计,JetStream=骨干+replay 地基);桥是两面的连接器,非新持久面。
- Close 后桥发布越过时怎么办?——桥在 publish 内部,受既有「publish 不越过 Close」纪律约束;无新窗口。
- 抽取后 controller 的内部调用者(44 方法互调)怎么办?——只搬「泵循环 + 分发」;治理方法留在 controller(经回调集暴露给 loop);controller 内部互调不受影响。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: 新包 `internal/agentloop` MUST 定义 `AgentLoop` 接口与回调集小接口(解码后事件→治理动作;方法数=治理面收敛);默认驱动实现(从 controller pump 搬家:DecodeLine/分流语义逐字节)。
- **FR-101a**: 默认 loop 的事件源 MUST 为 `llm.Provider`(dsh:agent loop 消费 ctx.llm);**Bind 机制落地**(P4b-1 遗留):claude 适配器增 `Bind(host engine.Host)`,绑定后 seam-only 的 ErrNotBound 形态退役(回合方法委托宿主)——接缝获得首个真实消费者。
- **FR-102**: 默认 AgentLoop MUST 挂为 `ctx.agentLoop` 服务键;controller 经接口消费(治理回调注入 + Provider 经 ctx.llm);行为零变更(既有全矩阵绿)。
- **FR-103**: AgentLoop 包 MUST 不 import control(分层矩阵登记 `internal/agentloop: {engine, event, kernel}`);依赖以小接口注入。
- **FR-104**: 镜像→总线桥 MUST 在 controller.publish 尾部单一注入点 Emit(subject=`mulix.mirror.<kind>`,payload=原始行);先镜像后总线顺序 MUST 可证(测试)。
- **FR-105**: 桥错误 MUST fail-visible(Notice warning,fail-visible 不静默),MUST NOT 阻断回合/镜像。
- **FR-106**: 四红线不动:先持久化后发布(桥在镜像后)、单写者、1:1、fail-closed。
- **FR-107**: 全量门禁绿(20+ 包/vet/gofmt/layers);行为零变更。

## Key Entities *(include if feature involves data)*

- **AgentLoop**: 回合循环抽象(事件源 → 解码 → 分发到回调集);默认驱动=泵逻辑搬家。
- **回调集(LoopCallbacks)**: 治理动作小接口(onInit/onAssistant/onToolResult/onResult/onControlRequest/onNotice 同型);controller 实现之。
- **镜像→总线桥**: publish 尾部的总线 Emit 注入点;subject=mulix.mirror.<kind>。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 默认 AgentLoop 挂载下既有全矩阵(e2e/单飞/watcher/收口/Boot 护栏)全绿,事件序列一致。
- **SC-002**: AgentLoop 包不 import control(分层矩阵 0 违规)。
- **SC-003**: 桥测试(镜像后总线同事件可见 + 顺序)+ 失败注入(Notice fail-visible)落地。
- **SC-004**: ctx.agentLoop 服务键就位;P4 收官(P4a/P4b-1/P4b-2 全归档)。

## Assumptions

- 泵逻辑「搬家」而非重写:DecodeLine/分流顺序/收口纪律逐字节保留(行为零变更的根基)。
- 回调集接口的粒度=事件类数(设计期精化);不做事件名字符串派发。
- 总线桥的 subject 每会话 Stream 布局(P4a Task 2 Ruling 的 P4b 目标形态)随桥一并评估;过渡期每事件 Stream 可接受(如需统一,记录 Ruling)。
- ctx.agentLoop 的消费方=controller;P5 的 PTC/可替换 loop 在此接缝上扩展。

## Out of Scope

- P4c:HTTP/SSE 入口插件、profiles/bundles。
- P5:fork/replay/search、PTC(loop 接缝的消费者)、tool-cordis。
- 泵行为重写、事件协议变更、治理语义变更。

## Clarifications

### Session 2026-10-12

- Q: AgentLoop 的事件源走 ctx.llm(Provider)还是 engine.Host 直连? → A: **走 Provider**(裁定 A)——dsh 忠实(agent loop 消费 ctx.llm);顺路落地 P4b-1 遗留的 Bind 机制(claude 适配器 Bind(host),ErrNotBound 形态退役)——接缝获得首个真实消费者。
- 跳过理由:回调集粒度/双持久面/Close 窗口/抽取边界已在 Edge Cases 与假设裁定。
