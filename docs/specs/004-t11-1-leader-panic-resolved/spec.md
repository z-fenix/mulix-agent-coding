# Feature Specification: T11-1 单飞裁决的异常韧性与有界化

**Change**: `004-t11-1-leader-panic-resolved`

**Created**: 2026-10-11

**Status**: Draft

**Input**: User description: "P3 路由② T11-①:单飞 leader panic 悬挂 + resolved 裁决表无界增长(defer 式清理)"——P2 复审路由项,治理/可靠性 hardening。

## 背景(问题陈述)

mulix 的审批裁决走单飞(single-flight)模式:同一 `tool_use_id` 的并发/重发审批请求只评估一次(leader),其余请求等待并复用裁决。P2 复审确认了该机制的正常语义(exactly-once 发布、广播唤醒),同时路由出两个异常面缺陷:

1. **leader panic 悬挂**:单飞 leader 在评估期间若发生 panic(如 `Gate.Evaluate` 或事件发布路径),它永远不会写入裁决缓存、永远不会广播收口——所有等待方将在等待通道上**永久悬挂**,且该次调用的在飞记录永不清理。审批回路整体卡死,只能靠进程外干预。
2. **裁决表无界增长**:裁决缓存表只进不出——长会话中每次工具调用(含允许类工具)都会累积一条缓存记录,无任何回收时机。会话内存随工具调用次数线性增长,无上限。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - leader 异常不再悬挂审批回路 (Priority: P1)

长会话中某次工具调用的单飞 leader 在评估期间异常(panic)。正在等待该裁决的其他请求(并发回调桥、引擎重发信封)在有限时间内被唤醒,各自得到一致的 fail-closed 裁决(deny,来源标注为异常兜底),镜像如实记录该异常路径;主会话继续运转,后续工具调用不受影响。

**Why this priority**: 审批回路悬挂是全会话级卡死(不可恢复、无超时出口),是本变更两条缺陷中后果最重的一条;且修复面与 User Story 2 相邻,先行交付不冲突。

**Independent Test**: 在并发回调测试中注入 leader panic(等效异常路径),多个等待方并发请求同一 `tool_use_id`:全部在测试超时内返回、裁决一致为 fail-closed、镜像事件如实落档、无 goroutine 残留悬挂。

**Acceptance Scenarios**:

1. **Given** 同一 `tool_use_id` 有多个并发等待方, **When** 单飞 leader 在评估期间 panic, **Then** 全部等待方在有限时间内被唤醒,拿到一致的 fail-closed 裁决(deny,timeout 词表语义),进程不悬挂、不崩溃退出。
2. **Given** leader panic 已发生且无等待方, **When** 同一 `tool_use_id` 的后续请求到达, **Then** 不复用悬挂状态——按新裁决重新评估(治理默认 fail-closed),无残留在飞条目阻塞。
3. **Given** 正常路径(无异常), **When** 并发重复投递同一 `tool_use_id`, **Then** 行为与现状完全一致:恰一次 `approval_request`、恰一次 `approval_resolved`(仅 leader 发布)、等待方复用同一裁决。

### User Story 2 - 长会话内存有界 (Priority: P2)

会话运行数小时、工具调用数千次后,裁决缓存表占用不随调用次数无界增长:已收口回合的裁决条目被回收,表的规模在任意时刻与「当前进行中的审批」相关,而非历史总量。

**Why this priority**: 无界增长是慢性劣化(长会话内存压力),后果轻于悬挂但违背 harness 长跑定位;修复依赖与 US1 相同的收口路径,排在其次。

**Independent Test**: 驱动大量(如千次)工具调用回合并全部收口后,裁决缓存表规模回落到常数基线;进行中的审批仍有条目(语义不变)。

**Acceptance Scenarios**:

1. **Given** 一个完成了 N 次工具调用且全部收口的会话, **When** 检查裁决缓存表规模, **Then** 规模为常数基线(与 N 无关),不随历史调用数增长。
2. **Given** 某次审批仍在进行中(回合未收口), **When** 检查裁决缓存表, **Then** 该条目仍在(重发信封复用语义不受清理影响)。

### Edge Cases

- leader panic 发生在 `approval_request` 发布**之前**时怎么办?——等待方仍被唤醒并 fail-closed;镜像可能只有 `approval_resolved`(兜底来源标注)而无对应 request——异常路径如实留痕,不补发、不伪装成正常路径。
- 回合收口后同一 `tool_use_id` 又被重发(协议下不应发生)时怎么办?——缓存已清理,按新请求重新评估并发布新事件;不承诺跨回合缓存复用(协议中重发仅发生在裁决等待期内)。
- leader panic 与回合收口竞争时怎么办?——清理与唤醒的先后不得产生死锁或二次 panic;等待方拿到 fail-closed 裁决后,回合正常收口。
- 清理时仍有等待方在等一个已裁决未收口的条目时怎么办?——清理时机 MUST 保证「进行中的审批不受影响」(US2 验收 2):条目仅在不再可能有等待方之后回收。
- 多个子会话(子代理)并发时怎么办?——每个会话控制器各自持有自己的裁决表与在飞表,清理与异常恢复互不跨会话影响。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: 单飞 leader 在评估期间发生异常(panic)时,MUST NOT 使任何等待方永久悬挂:全部等待方 MUST 在有限时间内被唤醒,并得到一致的 fail-closed 裁决(deny;镜像词表按既有 timeout/兜底来源语义标注)。
- **FR-102**: leader 异常后,该次调用的在飞记录 MUST 被清理(不泄漏):同一 `tool_use_id` 的后续请求不被悬挂状态阻塞,按新裁决重新评估。
- **FR-103**: 裁决缓存表 MUST 有界:已收口回合的条目 MUST 被回收,使会话内该表规模与「进行中的审批」相关而非与历史工具调用总量相关;清理 MUST NOT 影响「进行中的审批」的重发复用语义。
- **FR-104**: 正常路径行为 MUST 逐字节保持:恰一次 `approval_request`、恰一次 `approval_resolved`(仅 leader 发布)、等待方复用同一裁决、缓存命中直接返回——P2 复审确认的 exactly-once 语义与既有并发矩阵不回归。
- **FR-105**: 异常兜底裁决 MUST 遵循既有 fail-closed 红线(deny + timeout 词表),MUST NOT 引入「异常时放行」或静默吞异常——异常路径在镜像中如实可见。
- **FR-106**: leader 异常路径 MUST 可测试:并发测试能够注入等效异常(测试钩子或等效手段),并以超时保护证明「有限时间唤醒、无悬挂、无 goroutine 泄漏」。
- **FR-107**: 本变更 MUST NOT 改变审批裁决的评估顺序与治理语义(allow 清单、预设矩阵、审批人交互、别名配对不变量均不动)。

### Key Entities *(include if feature involves data)*

- **裁决缓存表(Resolved Rulings)**: 会话控制器持有的「tool_use_id → 裁决」映射;属性:仅由单飞 leader 写入、重发信封复用、本变更起有界(回合收口回收)。
- **在飞表(Inflight)**: 「tool_use_id → 收口广播通道」映射;属性:leader 创建、收口时广播并删除;本变更起异常路径也保证删除。
- **单飞 Leader / Follower**: 同一 `tool_use_id` 并发请求中首个评估者为 leader(发布事件并写缓存),其余为 follower(等待复用);本变更起 leader 异常时 follower 有有限时间出口。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 注入 leader 异常的并发测试中,全部等待方在测试超时(秒级)内返回,裁决一致为 fail-closed,无 goroutine 悬挂泄漏——审批回路无卡死路径。
- **SC-002**: 模拟千次工具调用且全部回合收口的会话,裁决缓存表规模回落到常数基线(与调用次数无关);进行中审批语义不变。
- **SC-003**: 既有单飞并发矩阵(20 goroutine 同 ID)与全量门禁在修复后全绿——exactly-once 与正常路径零回归。
- **SC-004**: 异常路径在镜像中如实可见(兜底裁决来源可审计),无静默吞异常。

## Assumptions

- leader panic 概率极低;本变更是**韧性修复**(异常发生时不悬挂、fail-closed、可审计、有界),不是 panic 根因的消除或进程级 recover 策略的重新设计。
- 清理时机按「回合收口」为默认候选(协议中重发仅发生在裁决等待期内,回合收口后条目不再被需要);具体机制(收口钩子/等效)归设计阶段。
- 引擎协议下回合收口后重发同 `tool_use_id` 不发生;若发生,按新请求重新评估是可接受的治理默认(fail-closed 优先于缓存复用)。
- 子会话各自持有独立控制器状态,本变更不引入跨会话共享。

## Out of Scope

- P3 路由其余项:T5-① 16MB 行隔离、T12-⑤ watcher 复测、T12-⑥ SC-006 计数、T10-①②、T5-⑤ fsync、T8-①② 及廉价测试补强清单。
- 003 的 2 条 deferred minors(SC-001 合并闭环注记、fixture gate 形状断言)——另行廉价测试补强批次。
- 单飞机制的架构重构、进程全局 panic 恢复策略、审批 UI/交互变更。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->
