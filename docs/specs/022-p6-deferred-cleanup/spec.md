# Feature Specification: 022-p6-deferred-cleanup

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 台账 deferred 清零 (Priority: P1)

维护者打开任一近期变更（017/019/020/021）的 verify 报告「Deferred minors」节，
逐项核对：每一项要么已在代码/文档中清偿，要么其注释/文案已与现行行为一致。
台账不再存在路由未清偿项。

**Why**: deferred 项按纪律「随触碰清」，但若无清扫轮，低触碰频率文件上的
注释/文案偏差会长期滞留，形成「台账说有债、代码说不清」的漂移。

### User Story 2 - 证据可追溯 (Priority: P2)

阅读 `docs/evidence/*.md` 的评审者能在文档头部直接看到该证据基于的基线
commit hash，无需反推 git 历史。

### User Story 3 - 持久上下文可信 (Priority: P3)

新会话读取 CLAUDE.md 时，其中每一条指针（历史阶段、memory 文件）与状态
描述（当前阶段）都指向真实存在的内容或已被移除，不再有死引用误导 agent。

### Edge Cases

- 注释/文案修改不得改变任何运行时行为（纯注释改动的文件，行为测试零变化）。
- 错误文案统一化会触碰既有测试钉（断言子串）——钉与实现同变更内同步改，
  断言语义保持（仍验证 fail-closed 三态各自可达）。
- `.mulix/.runtime/*/state.yaml` 不入提交（既有惯例）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: `internal/control/controller.go` 三态 fail-closed 错误文案语言
  统一（当前 ①英文 "kernel context required" ②③中文），统一后三态各自
  fail-closed 语义不变，负向四钉（TestNewControllerRequiresKernelLoop）全绿。
- **FR-102**: `internal/control/controller.go` 类型不符错误文案补齐实际类型
  （当前仅有 want 形态，无 got 形态），排障时可读出注册了什么错类型。
- **FR-103**: `internal/cli/session.go` 桥失败注释更正为准确理由（019 Final
  行裁定原文：sharedEventBus 仅前台运行，stdout 是用户可见面非 MCP 传输）。
- **FR-104**: `internal/control/memory_injection_test.go` noMulixEntry 相关
  注释可读性修正（017 终审 Minor deferred 项）。
- **FR-105**: `internal/cli/teardown_test.go` 陈旧注释更正为与现行行为一致
  （020 deferred 项）。
- **FR-106**: evidence 文档头部标注基线 commit hash：022 自身 evidence 执行
  此惯例，并补注在案的 021 evidence 文档（020 路由项）。
- **FR-107**: CLAUDE.md 文档同步：提交工作区在案的 NATS 红线/环境限制两处
  更新（010 裁定沉淀）；移除死指针（AGENTS.md §历史阶段、
  .mulix/memory/history.md 引用不存在）与过时「当前阶段」描述。

### Key Entities *(include if feature involves data)*

不涉及数据实体（纯注释/文案/文档变更）。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 四个近期变更（017/019/020/021）report 的 deferred 项全部清偿，
  022 台账无新增未清项。
- **SC-002**: 全量门禁绿（26 包 ok / vet / gofmt / lint layers 零违规）。
- **SC-003**: 行为零变更——注释/文档任务外，唯一行为面改动是错误文案字符串
  （FR-101/102），由负向四钉 + 全矩阵承载。
- **SC-004**: docs/evidence/*.md（021 与 022）头部含基线 hash 标注。
- **SC-005**: CLAUDE.md 无死指针（引用的文件/章节真实存在）。

## Assumptions

- FR-101 文案统一方向默认**英文**（仓内 Go 错误串惯例，如 "create store"），
  且 "kernel context required" 串被大量证据文档/台账引用，保持不动、翻译
  ②③，diff 最小。设计阶段可推翻。
- FR-107 属流程/文档卫生，本属「剩余清单 §三」，经用户「开一轮 deferred
  清扫」裁定并入本变更（清扫意图涵盖文档残留）；若裁掉仅影响 Task 数。
- 各项均为注释/文案/文档级，无新依赖、无接口变更。

## Out of Scope

- 任何功能新增、重构、测试增强（非 deferred 项不动）。
- 「入口即插件（HTTP/SSE）」与 ctx.agents/ctx.sessions 插件化（未立项，等
  用户阶段裁定）。
- 021 Out of Scope 沿袭项：第二 loop 实现、`[loop]` 配置面、Source 接缝、
  loop 热替换。
- 早期（016 及以前）evidence 文档的基线 hash 回溯补注（路由仅指向近期）。

## Clarifications
