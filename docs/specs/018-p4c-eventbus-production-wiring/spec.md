# Feature Specification: P4c 事件总线生产接线

**Change**: `018-p4c-eventbus-production-wiring`

**Created**: 2026-10-13

**Status**: Draft

**Input**: 012/015 台账三次 Ruling 明示路由——「EnableEventBus 生产接线=配置门控(`[eventbus] enable` 默认关),生产默认翻转随 P4c 首消费者」。用户裁定(2026-10-13):P4c 为下一变更方向。

## 背景(问题陈述)

010 内嵌 NATS 总线落地(emit/waterfall/parallel/serial/bail),012 镜像→总线桥落地(先持久化后发布,镜像 events.jsonl 仍为 1:1 审计真相源)——但接线开关 `EnableEventBus` 仅为 CLI flag(默认关,零消费者):生产形态「总线仅持久无消费」。

P4c 翻转:配置门控成为唯一真源,并交付首个真实订阅消费者——`mulix events`(外部观察面,订阅 `mirror.*` 实时打印),总线从「可用基建」变成「在用能力」。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 配置门控(三态 fail-closed) (Priority: P1)

config 增 `[eventbus]` 节:`enable` 缺省=关;显式 `true`=开;显式 `false`=关。非法值(非 bool)Load 末尾 fail-closed(014 Profile 同款形态)。openSession 经 config 消费——CLI flag `EnableEventBus` 退役(单真源)。

**Why this priority**: 门控是翻转的前提;单真源消除双开关矛盾面(017 同款纪律)。

**Independent Test**: 配置缺省 → 桥关;`[eventbus] enable=true` → 桥开(boot 后 events 键就位+桥发布);非法值 → 拒绝启动。

**Acceptance Scenarios**:

1. **Given** 缺省配置, **When** openSession, **Then** 桥关(无 events 键,行为与 017 逐字节一致)。
2. **Given** `[eventbus] enable=true`, **When** openSession, **Then** 桥开(sharedEventBus 就位,events 键注册,镜像先写后桥发)。
3. **Given** `[eventbus] enable="yes"`(非法), **When** Load, **Then** fail-closed 拒绝启动。

### User Story 2 - mulix events 观察面(首个订阅消费者) (Priority: P2)

新子命令 `mulix events [--workspace W]`:连接(或按配置自启)内嵌 NATS,订阅 `mirror.*`,实时打印 kind+payload 摘要行;Ctrl-C 退出。镜像先持久化红线不变——观察面是纯订阅侧,绝不写。

**Why this priority**: 「翻转」的意义在于有消费者;外部观察面最小且不触碰 TUI。

**Independent Test**: enable=true 会话跑一轮 → `mulix events` 收到 turn_started/usage/turn_done 等行;enable=false → 收不到。

**Acceptance Scenarios**:

1. **Given** 桥开会话, **When** `mulix events` + 独立回合, **Then** 订阅行实时到达(含 kind 前缀)。
2. **Given** 观察面进程, **When** Ctrl-C, **Then** 干净退出(订阅取消+连接关闭)。
3. **Given** 观察面, **When** 镜像写入与桥发布, **Then** 观察面零写入(纯订阅——审计面 1:1 不受影响)。

## Requirements *(mandatory)*

- **FR-101**: config 增 `EventbusConfig{RawEnable string/bool 形态随设计}` `[eventbus]` 节;缺省关;非法值 fail-closed(014 三态同款)。
- **FR-102**: openSession 桥接线改经 config 单真源;CLI flag `EnableEventBus` 退役(Options 字段与接线点同步清理);既有测试迁移。
- **FR-103**: `mulix events` 子命令:订阅 `mirror.*` 实时打印;纯订阅零写入;Ctrl-C 干净退出。
- **FR-104**: 先持久化后发布红线不变(桥已是镜像 Append 成功后才 Emit——本变更零触碰发布序)。
- **FR-105**: 全量门禁绿;零第三方新增(nats-server/nats.go 既有)。

## Key Entities

- **EventbusConfig**: [eventbus] 节,enable 三态。
- **mulix events**: 外部观察面命令(纯订阅)。

## Success Criteria

- **SC-001**: 缺省关——017 行为零变更。
- **SC-002**: enable=true 桥开——events 键就位+订阅可达。
- **SC-003**: mulix events 实时收到 mirror.* 行;纯订阅零写入。
- **SC-004**: 全量门禁绿。

## Assumptions

- 内嵌 NATS 单例(sharedEventBus 按工作区键)继续复用;观察面进程按同 StoreDir 连接同一内嵌 server 的可行性属设计期核验点(内嵌 server 不监听 TCP——跨进程连接形态需设计期裁定:InProcess 仅限同进程,观察面或需 StoreDir 直读回放/或开本地监听,设计期裁定)。
- TUI 不在本变更消费总线(Sink 已是正装出口)。

## Out of Scope

- TUI 总线消费、waterfall/serial/parallel 业务订阅、P6 loop 插件化。

## Clarifications

- clarify 跳过理由:范围即 012/015 台账 Ruling 原文;跨进程连接形态(内嵌 server 无 TCP)标记为设计期裁定(NEEDS CLARIFICATION → design),不阻塞 spec。
- **subject 命名事实修正**(计划评审轮 2026-10-13):本 spec 与 design 所写「订阅 mirror.*」为笔误级事实错误——总线实际 subject 为 `mulix.mirror.<kind>`(Emit 统一加 `mulix.` 前缀,桥声明 `mirror.<kind>`);观察面订阅 `mulix.mirror.*`,Ruling 入台账。
