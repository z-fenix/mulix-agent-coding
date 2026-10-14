# Feature Specification: P6 loop 插件化——kernel 权威重接(缝内化)

**Change**: `021-p6-loop-plugin-kernel-authority`

**Created**: 2026-10-14

**Status**: Draft

**Input**: CLAUDE.md §3.3/§3.4 P6 目标(loop 插件化——ctx.agentLoop kernel 定位覆盖);用户裁定(2026-10-14):方案 A(权威重接,不做第二 loop 实现)+ fail-closed 缺键语义 + 方案 1(ctx 直传+装配期校验)。

## 背景(问题陈述)

012 交付的 `agentloop.Loop` 接缝可替换,但 kernel 权威缺失:assembleKernelContext 往 kernel 登记 `"agentloop"` 键是**装饰性**的——pump 实际消费的 loop 来自 `control.Options.Loop`(且零消费点,恒 nil→`Default{}` 兜底)。kernel 不是权威真源,「loop 本身是插件」的 dsh 目标态(ctx.agentLoop)未覆盖。

P6 缝内化:kernel 成为 loop 的**权威真源**——loop 以 Plugin 形态挂载(Mount/effect 生命周期),control 经 ctx 解析消费,缺键 fail-closed。第二 loop 实现**不做**(A 裁定:扩展性由机制证明,不由样例证明;第二实现留 P7)。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - loop 插件形态 (Priority: P1)

agentloop 包新增 `Plugin`(实现 kernel.Plugin:Name="agentloop",Mount=Provide `"agentloop"` 键,值为 `Loop` 接口);assembleKernelContext 裸 Provide 改 `root.Mount(agentloop.NewPlugin())`——内建 loop 也经插件路径挂载,未来任何 loop 实现 Plugin 即插入。

**Why this priority**: 「loop 本身是插件」的形态本体。

**Independent Test**: Mount 后 root.Keys() 含 agentloop;Service 取回 `agentloop.Loop`。

**Acceptance Scenarios**:

1. **Given** NewPlugin, **When** root.Mount, **Then** Service("agentloop") 返回 `agentloop.Loop`(Default 实例)。
2. **Given** Mount 失败场景(同键已注册), **When** Mount, **Then** 沿 kernel Mount 回滚语义(fail-closed)。

### User Story 2 - kernel 权威消费(fail-closed) (Priority: P1)

`control.Options` 增 `Kernel *kernel.Context`(根 ctx 直传;control→kernel 边既有);NewController **装配期**解析 `Service("agentloop")` → 断言 `agentloop.Loop`;缺键/类型不符 = 组装失败拒启动(`control: agentloop: ...`)。pump 消费解析产物;`Options.Loop` 字段与 nil→Default 兜底退役(零消费点实证)。

**Why this priority**: kernel 权威的本体;静默兜底会掩盖装配错误。

**Independent Test**: 缺键 ctx → NewController 报错;类型不符 → 报错;正确键 → pump 行为与 020 逐字节一致(happy-turn 矩阵回归)。

**Acceptance Scenarios**:

1. **Given** ctx 无 agentloop 键, **When** NewController, **Then** fail-closed(`agentloop: 服务键缺失`)。
2. **Given** 键值非 Loop 类型, **When** NewController, **Then** fail-closed(`类型不符`)。
3. **Given** 正确装配(Plugin Mount), **When** 完整回合, **Then** 行为与 020 逐字节一致(既有矩阵零回归)。

## Requirements *(mandatory)*

- **FR-101**: agentloop 包新增 `Plugin`(`NewPlugin()` 返回 kernel.Plugin;Mount=Provide `"agentloop"` 键=Loop);assembleKernelContext 改 `root.Mount(agentloop.NewPlugin())`。
- **FR-102**: `control.Options` 增 `Kernel *kernel.Context`;NewController 装配期解析+断言 `agentloop.Loop`(缺键/类型不符 fail-closed);pump 消费解析产物。
- **FR-103**: `Options.Loop` 字段退役(全仓零残留);nil→Default 兜底删除。
- **FR-104**: 行为零变更——既有全矩阵回归绿(happy-turn/delegation/PTC 全链)。
- **FR-105**: 全量门禁绿;零第三方新增;分层矩阵按需登记(control→kernel 边已存在则零变更)。

## Success Criteria

- **SC-001**: Plugin Mount 语义正确(键就位/回滚)。
- **SC-002**: NewController 缺键/类型不符 fail-closed(双钉)。
- **SC-003**: 行为零变更(全矩阵回归)。
- **SC-004**: `Options.Loop` 零残留。
- **SC-005**: 全量门禁绿。

## Out of Scope

- 第二 loop 实现、`[loop]` 配置面(方案 A 裁定)、Source 接缝变更(012 FR-101a 不动)、loop 运行中热替换。

## Assumptions

- control→kernel import 边已存在(EventPublisher 小接口)——零新边(实现期核验矩阵)。
- subagent 子会话装配路径同源(assembleKernelContext 单点)——子会话天然继承。
- Kernel ctx 生命周期:根 ctx 由 cli 装配持有,control 不 Dispose(既有所有权不变)。

## Clarifications

- clarify 跳过理由:方案裁定已在 brainstorm 完成(A/fail-closed/方案 1,2026-10-14 用户三连裁定),spec 是裁定的成文化,无开放问题。
