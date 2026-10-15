# Feature Specification: P5a 会话能力——fork/replay/search 与补强批次

**Change**: `013-p5a-fork-replay-search`

**Created**: 2026-10-12

**Status**: Draft

**Input**: User description: "P5 全量(A-D),分期 P5a=会话能力三件套(fork/replay/search)+补强批次"——dsh「一切从事件流」哲学的核心兑现:Resume/fork/search/replay 共用同一事件流。

## 背景(问题陈述)

P4 程序(010-012)铺齐了地基:镜像 events.jsonl(1:1 审计)、事件总线(NATS/JetStream)、agentloop(可替换 loop)。但 dsh 的三大会话能力缺席:

1. **fork**:无法从已有会话的任意事件点派生新会话(现在只有崩溃恢复=全量续链,没有「从中间开始」的分支语义);
2. **replay**:无法将历史事件流重放经 agentloop(P4b-2 的 loop 抽取使这成为可能——事件源从引擎换成镜像文件);
3. **search**:镜像事件无全文检索(memory 的 BM25 只覆盖记忆条目,不覆盖会话事件)。

同时累积 deferred 9+ 条的补强批次随本变更清偿。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 会话 fork (Priority: P1)

从已有会话的指定事件点(默认=末尾)派生新会话:新会话获得父会话至分支点的事件副本(镜像复制),携带 parentSession 元数据;dsh 形态 `ctx.agents.create({parentSession, seedLength})` 的 Go 对齐。

**Why this priority**: fork 是 dsh 会话能力的核心原语(replay/分支工作流的地基)。

**Independent Test**: 源会话 N 事件 → fork(seq=N/2)→ 新会话含前 N/2 事件副本 + parentSession 指针 + 分支点标注;源会话不受影响;fork 后两会话独立追加互不干扰。

**Acceptance Scenarios**:

1. **Given** 源会话与 fork 点, **When** fork, **Then** 新会话镜像含事件副本(至分支点)+ meta 标注 parent_id/seed_length。
2. **Given** fork 产物, **When** 源会话继续追加, **Then** 两会话镜像独立(无交叉污染)。
3. **Given** 分支点越界/非法, **When** fork, **Then** fail-closed 报错。

### User Story 2 - 事件流 replay 经 agentloop (Priority: P2)

将镜像事件流作为 agentloop 的事件源重放:治理回调按历史事件依序触发(不改写镜像——重放是只读观测面,不产生新镜像写入)。

**Why this priority**: agentloop(P4b-2)的首个非引擎事件源——loop 可替换的价值实证。

**Independent Test**: 种子镜像(N 事件)→ replay 经 recordCallbacks → 路由序列与镜像事件序列一致;replay 不追加镜像(ReadAllEvents 前后计数不变)。

**Acceptance Scenarios**:

1. **Given** 含全 kind 事件的镜像, **When** replay 经 Default 驱动, **Then** 回调路由序列与事件序列逐一对齐。
2. **Given** replay 完成, **When** 核对镜像, **Then** 零追加(只读语义)。

### User Story 3 - 镜像事件全文检索 (Priority: P3)

镜像事件的文本面(assistant text/tool result content/notice content)可全文检索:返回按相关性排序的命中列表(事件序位+摘录)。复用 memory 的 BM25 索引模式。

**Why this priority**: 会话可追溯性的查询面;P5a 收尾项。

**Independent Test**: 种子镜像含可检索文本 → search 命中且排序合理;无命中查询返回空。

**Acceptance Scenarios**:

1. **Given** 镜像含 assistant_text "解决缓存穿透", **When** search "缓存穿透", **Then** 命中该事件(序位+摘录)。
2. **Given** 无命中查询, **When** search, **Then** 空结果(不报错)。

### User Story 4 - 廉价补强批次清偿 (Priority: P3)

累积 deferred(003..012)按既有路由清偿:[003] fixture gate 标记断言、[006] 证据 t2 标注+原始行(已清偿确认)、[010] MountAll 重复键测试、[011] Bail 静默注释+MountAll 非事务边界文档、[012] 回填注释对齐、flake 观察。不明 narrate,逐项核销入账。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: `sessionx.Fork(src *Store, atSeq int64, newID string) (*Store, error)`——从源镜像复制事件(至 atSeq)到新会话;meta 标注 parent_id/seed_length;源会话只读不受影响。
- **FR-102**: `agentloop.Replay(ctx, events []engine.Event, cb)`——**类型化事件切片**经 Callbacks 重放;只读语义(零镜像追加);镜像→engine.Event 的适配由调用方(cli/control,可双 import)完成。
- **FR-103**: `sessionx.SearchEvents(store, query)`——镜像事件文本面全文检索(BM25 模式复用);返回序位+摘录+kind。
- **FR-104**: 补强批次清偿(累积 deferred 逐项核销;清单见设计文档)。
- **FR-105**: 全量门禁绿;零第三方新增(NATS/已有除外)。

## Key Entities *(include if feature involves data)*

- **fork 产物**: 新 Store(镜像副本+meta{parentSession, seedLength})。
- **Replay**: 只读事件流重放(镜像→Callbacks)。
- **事件检索**: BM25 模式复用的镜像文本查询。

## Success Criteria *(mandatory)*

- **SC-001**: fork 三场景(副本/独立/fail-closed)全绿。
- **SC-002**: replay 路由序列对齐 + 零追加。
- **SC-003**: search 命中/空结果两态。
- **SC-004**: 补强批次清偿(台账归零,除明示路由)。
- **SC-005**: 全量门禁绿。

## Assumptions

- fork 的分支点=seq(镜像行序位);事件副本原样(不改写)。
- replay 不产生新镜像写入(只读观测;写面 replay 属 P5d+)。
- search 复用 memory BM25 索引模式(不同数据源同算法)。

## Out of Scope

- P5b(profiles)、P5c(PTC)、P5d(tool-cordis)。
- 写面 replay(重放产生新镜像写入)、跨会话事件合并。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->
