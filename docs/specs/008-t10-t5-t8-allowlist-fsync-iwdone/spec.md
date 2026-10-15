# Feature Specification: T10/T5/T8 廉价加固合并包(解析器边缘 / allowlist 断言 / fsync / iw.Done)

**Change**: `008-t10-t5-t8-allowlist-fsync-iwdone`

**Created**: 2026-10-11

**Status**: Draft

**Input**: User description: "P3 路由⑥⑦⑧合并:T10-① 解析器注释边缘、T10-② allowlist 漂移交叉断言、T5-⑤ fsync、T8-① 等 iw.Done"——四项独立小加固合并为一个变更集(体量相当、互不依赖、各有独立测试)。

## 背景(问题陈述)

P2 终末评审路由出四个 deferred minor,均为廉价加固(单点小改 + 各自测试),合并交付:

1. **T10-① 分层检查的 import 块解析边缘**(`internal/lint`):import 块内的**注释行**会被误解析——含 `)` 的注释行被当作块闭合(→ 后续真实 import 漏扫描,假阴性);注释内的引号字符串被当作 import(假阳性)。扫描结果直接影响分层纪律门禁的准确性。
2. **T10-② flag marker 双源 allowlist 漂移风险**(`internal/lint`):lint 自身的 allowlist 与 P1 repolint 的 allowlist 是两份独立清单——单侧增删会让另一侧漂移(交叉引用失效)。建议交叉断言:lint allowlist ⊆ P1 allowlist(测试钉)。
3. **T5-⑤ 记忆隔离转储无 fsync**(`internal/memory`):`isolate` 的 tmp+rename 两段式在 rename 前未 Sync——崩溃窗口内 rename 落盘的可能是未刷写数据(P1 模式一致,Sync 是廉价加固;005 的 sessionx 压实路径已有 Sync,本项把 memory 对齐)。
4. **T8-① Close 不等 iw.Done**(`internal/control`):控制器 Close 停止 watcher 轮询但不等 goroutine 退出——≤1 个在飞 publish 可晚于 Close 完成(Store 未关故 benign,但破坏「无 publish 越过 Close」的纯度,与泵的收口纪律不一致)。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 分层检查的解析器边缘加固 (Priority: P1)

T10-① + T10-②:分层检查扫描含注释边缘形态的 import 块时结论正确(注释行既不闭合块也不产生假 import);lint allowlist 与 P1 allowlist 的包含关系被测试钉死,单侧漂移在门禁层即刻暴露。

**Why this priority**: 两项同属 `internal/lint` 扫描准确性,合并交付;扫描结果是分层纪律门禁的判定依据。

**Independent Test**: 构造含注释行(含 `)` 的注释、注释内引号串)的 fixture 文件跑分层扫描——无假阴性/假阳性;漂移注入(lint allowlist 加入 P1 清单没有的条目)→ 交叉断言测试失败。

**Acceptance Scenarios**:

1. **Given** fixture 的 import 块内含「以注释形式出现的 `)` 行」与「注释内含引号串的行」, **When** 分层扫描, **Then** 块正确延续到真实 `)`,注释不产生任何 import 记录(与无注释的等价文件扫描结果一致)。
2. **Given** lint allowlist 与 P1 repolint allowlist 两份清单, **When** 跑交叉断言测试, **Then** lint allowlist 的每一项都存在于 P1 allowlist(子集关系);人为注入越界条目时测试失败。

### User Story 2 - 记忆隔离与 watcher 收口纪律 (Priority: P2)

T5-⑤ + T8-①:memory isolate 在 rename 前对 tmp 文件 Sync(崩溃窗口收紧);控制器 Close 等待 watcher goroutine 退出(有界),Close 之后不再有 watcher publish 越过。

**Why this priority**: 两项同为收口纪律加固,单测可钉;崩溃窗口与 publish 越界的实际风险低但修复廉价。

**Independent Test**: memory 隔离路径检查 rename 前 Sync 调用存在且隔离功能回归绿;watcher 测试断言 Close 返回后 Done 已关(或在有界时间内关闭)且无 publish 越过 Close。

**Acceptance Scenarios**:

1. **Given** 记忆库触发隔离重写, **When** tmp 写毕, **Then** rename 前执行 Sync(代码检查 + 既有隔离测试回归)。
2. **Given** watcher 在飞发布, **When** 控制器 Close, **Then** Close 在有界时间内等待 goroutine 退出(Done 关闭);Close 返回后不再有该 watcher 的 publish 到达。

### Edge Cases

- 注释行形态多样(`// )`、`/* ) */`、行尾注释 `import "x" // )`)时怎么办?——解析器按「剥离注释后判定」的单一规则处理;具体剥离策略(整行注释 vs 行尾注释)归设计,行为以「与无注释等价文件扫描结果一致」为验收。
- lint allowlist 出现 P1 清单没有的**合法新条目**(未来真实新增)时怎么办?——交叉断言失败即门禁红,按既定程序同步两侧(与 P1 repolint 的 allowlist 扩容程序一致)——失败是有用的信号而非误报。
- Sync 失败时怎么办?——Sync 错误沿既有 isolate 错误路径返回(fail-closed,不静默)。
- Close 等待超时(goroutine 卡在发布)时怎么办?——有界等待(设计定界,秒级);超时后 Close 照常返回并在既有日志/观测面留痕,不悬挂 Close。
- watcher 从未启动(nil 句柄)时怎么办?——等待逻辑对 nil/未启动句柄为 no-op(既有 Close 幂等语义保持)。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: `internal/lint` 的 import 块解析 MUST 忽略块内注释行:注释行不得被判定为块闭合,注释内容不得被解析为 import(扫描结果与「剥离注释的等价文件」一致)。
- **FR-102**: 新增交叉断言测试 MUST 钉死 lint allowlist ⊆ P1 repolint allowlist 的子集关系;任一侧漂移使断言失败。
- **FR-103**: `internal/memory` 的 isolate 路径 MUST 在 rename 前对 tmp 文件 Sync;Sync 错误按既有错误路径 fail-closed 返回。
- **FR-104**: 控制器 Close MUST 有界等待 watcher goroutine 退出(Done 关闭);等待对 nil/未启动句柄 no-op;超时不悬挂 Close 且留痕。
- **FR-105**: Close 返回后 MUST 不再有 watcher 的 publish 越过(在飞发布在等待窗口内完成或随 goroutine 退出终止)。
- **FR-106**: 四项加固 MUST 各自携带针对性测试(解析器 fixture、交叉断言、isolate 回归、Close/Done 断言);既有测试零回归。
- **FR-107**: 行为面 MUST 保持既有契约:分层检查的违规判定规则不变(仅解析准确性修复);memory 隔离语义不变(仅持久化加固);watcher 提醒语义不变(仅收口纪律)。

## Key Entities *(include if feature involves data)*

- **import 块解析器**:`internal/lint` 的分层扫描行状态机;本变更修复其注释处理。
- **双源 allowlist**:lint 自身 allowlist 与 P1 repolint allowlist;本变更以子集断言钉死包含关系。
- **隔离两段式**:memory isolate 的 tmp+rename;本变更补 rename 前 Sync。
- **watcher 收口**:InstructionWatcher 的 Close/Done;本变更补有界等待。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 注释边缘 fixture 扫描结果与无注释等价文件一致(无假阴性/假阳性)。
- **SC-002**: 交叉断言测试存在且对注入漂移失败(有判别力)。
- **SC-003**: memory isolate rename 前 Sync 落地,既有隔离/记忆测试零回归。
- **SC-004**: Close/Done 有界等待断言落地,既有 watcher 矩阵(6 测)与单飞矩阵零回归。
- **SC-005**: 全量门禁绿;四项各自提交,git diff 范围与四项一一对应。

## Assumptions

- 四项均无跨依赖,任务可独立交付与评审;合并仅为路由效率。
- 解析器只处理 `internal/lint` 自身的行扫描(不做通用 Go 解析——`go/parser` 引入属重构,超出廉价加固定位)。
- T8-① 的有界等待上秒级即可(轮询 goroutine 每周期工作量为文件 hash,退出迅速)。
- memory isolate 为唯一无 Sync 的 rename 路径(sessionx 压实与 jsonl Repair 已有 Sync——执行时复核,如有遗漏一并补齐并在报告注明)。

## Out of Scope

- T8-② 启动后新建文件监控(产品裁定)、watcher 策略调整。
- P3 路由 ⑨ 其余项与累积 deferred minors 批次(9 条 + flake 观察)。
- lint 解析器向 `go/parser` 的重构、allowlist 机制重设计。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->
