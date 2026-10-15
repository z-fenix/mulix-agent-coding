# Feature Specification: T8-2 裁定 A 落地与廉价测试补强批次

**Change**: `009-t8-2-a`

**Created**: 2026-10-11

**Status**: Draft

**Input**: User description: "P3 路由⑨:T8-② 产品裁定 A 落地 + 廉价测试补强批次(累积 14 条 deferred minors)"——P3 路由收口变更:产品裁定落地 + 台账 deferred 项的批次清理。

## 背景(问题陈述)

两部分:

1. **T8-② 产品裁定(用户 2026-10-11 裁定 A)**:指令文件 watcher 只监控**启动时已存在**的文件(键集在 `loadInstructionSnapshots` 固定);会话中新建的指令文件不被监控、不产生提醒。裁定:**维持现状并钉死**——与「快照保持,下会话生效」的会话冻结哲学同构,新文件属下个会话;补单测钉住该语义,防未来无意破坏。
2. **廉价测试补强批次**:P3 全程(003..008)累积 14 条 deferred minors + 1 条 flake 观察,按性质分四组:
   - **G1 lint 组(3 条)**:残留边缘(行内 `/*...*/` 终止后接代码的假阴性——008 终审逐字节证实)、判别力更强测试(watchInstructions + blocking publish 可产生真 RED)、镜像事件数稳定性断言直接钉。
   - **G2 control 组(2 条)**:recover 分支 Notice publish 双重故障防护(内层 recover)、allowlist 断言抽 helper 与判别力测试共享。
   - **G3 memory/jsonl 组(3 条)**:ReadAll doc 注释泛化、memory 半行 lineNo 计入超限段偏移、gateRuling.turn 注释修正。
   - **G4 测试卫生组(4 条)**:fixture gate 形状断言(已知真实标记)、leader_panic_test 死代码清除、判别力测试抽 helper(与 G2 合并)、[006] 证据文档 t2 标注「推断」+ 镜像原始事件行粘贴(全局镜像仍在档,可提取)。
   - **不在本批次(路由决定)**:[003] SC-001 全 allow 委派补测(真实冒烟面,非单测面,已有诚实注记);[004] start/Close 并发窗口(FR-104 明示豁免,终审确认超范围);flake 观察(TestSubagentWiringEndToEnd 时序竞争——保持观察,复现时单独路由)。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - T8-② 裁定 A 落地 (Priority: P1)

watcher 的「键集=启动快照」语义被单测钉死:会话启动后新建的指令文件不进入监控键集、不产生提醒;产品语义(新文件下会话生效)在代码注释与测试名中可检索。

**Why this priority**: 产品裁定的落地是 ⑨ 的第一项;防未来无意破坏。

**Independent Test**: 启动含 1 个指令文件的工作区 → 会话启动(基线确立)→ 新建第二个指令文件 → 轮询多个周期 → 无新键进入监控、无该文件相关 notice;既有键的变更仍正常提醒(无回归)。

**Acceptance Scenarios**:

1. **Given** 会话已启动(基线=文件 A), **When** 新建文件 B 并等待 ≥2 个轮询周期, **Then** watcher 键集仍为 {A}(白盒断言),无文件 B 相关 notice,文件 A 的变更提醒不受影响。
2. **Given** 语义钉测试存在, **When** 复核, **Then** 测试名/注释含「启动快照键集」语义关键词,可检索。

### User Story 2 - G1/G2 代码加固 (Priority: P2)

lint 残留边缘修复(行内 `/*...*/` 终止后接代码不再假阴性);watcher notice 发布的双重故障防护(内层 recover,原始 panic 源于 publish 自身时不再崩溃);Close 后镜像事件数稳定性断言直接钉。

**Why this priority**: 真实代码加固面;有明确失败形态。

**Independent Test**: 逐字节模拟 fixture(008 终审的失败形态)驱动 scanImports——行为修正;双重故障路径单测(注入 publish panic);Close 稳定性断言。

**Acceptance Scenarios**:

1. **Given** 行内 `/*...*/` 终止后接代码的行(`/* c */ "internal/x"`), **When** 扫描, **Then** 该行按代码解析(与剥离注释语义一致)。
2. **Given** 注入「publish 自身 panic」的测试钩子路径, **When** leader panic 收口触发 Notice 发布, **Then** 双重故障被防护(进程不崩溃,测试钉)。
3. **Given** Close 完成, **When** 短暂等待窗口后核对镜像, **Then** 事件计数稳定(无 publish 越过)。

### User Story 3 - G3/G4 文档与测试卫生 (Priority: P3)

注释/文档漂移清理(ReadAll、gateRuling.turn、t2 标注)、memory lineNo 计入超限段偏移、fixture gate 形状断言、死代码清除、[006] 证据文档补镜像原始行。

**Why this priority**: 纯卫生面,依赖前两组的同文件改动先行(避免冲突)。

**Independent Test**: grep 断言(注释文案)、既有测试回归、死代码消失、证据文档含原始 JSONL 行。

**Acceptance Scenarios**:

1. **Given** 三处注释/文档漂移, **When** 清理, **Then** 注释与实现语义一致(grep 可核)。
2. **Given** fixture gate 测试, **When** 增加已知真实标记断言, **Then** 形状错误可被捕获。
3. **Given** [006] 证据文档, **When** 从全局镜像提取原始事件行, **Then** seq=1/seq=2 原样粘贴 + t2 标注「推断」。

## Edge Cases

- G1 残留边缘修复与多行块注释状态机交互时怎么办?——`/*` 状态位优先;行内 `/*...*/` 剥离只对「本行内有收尾」生效,未终止的仍转多行状态(008 行为保持,仅补「终止后接代码」分支)。
- 双重故障防护的 recover 套 recover 时怎么办?——内层 recover 仅包 Notice 发布;若内层也 panic(理论不可能——recover 后再次 recover 无源),按 Go 语义终止,测试只钉「publish panic 被内层捕获」形态。
- lineNo 修正影响既有测试断言时怎么办?——修正后核对 memory 既有测试(半行 lineNo 断言)——半行场景无超限段,偏移为零,断言应不变;若有变动按新事实改断言并记录。
- [006] 镜像会话已清理时怎么办?——`~/.mulix/sessions` 为全局持久档(005 Task 4 实证跨日存活);已清理则从证据文档既有字段级摘录重建,如实标注「原始行不可复得」。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: watcher 键集语义钉:`internal/control` 新增测试断言「会话启动后新建的指令文件不进入监控键集、不产生提醒;既有键变更不受影响」;`startInstructionWatcher` 补注释(键集=启动时快照,新文件下会话生效——产品裁定 A,009)。
- **FR-102**: lint 残留边缘修复:`scanImports` 的行内 `/*...*/` 终止后接代码的行按代码解析;008 已覆盖形态(整行注释/行尾注释/多行块注释)零回归。
- **FR-103**: watcher Notice 发布加内层防护:双重故障(publish 自身 panic)不得使进程崩溃;失败形态如实留痕(测试钉)。
- **FR-104**: Close 后镜像事件数稳定性直接断言(新增测试);allowlist 子集断言抽共享 helper(子集测试与判别力测试共用)。
- **FR-105**: 文档/注释卫生:①`jsonl.ReadAll` doc 改「损坏段(两类)」;②`gateRuling.turn` 注释改「0=未关联(防御兜底,不入缓存)」;③[006] 证据文档 t2 标注「推断」+ 镜像原始行粘贴。
- **FR-106**: memory 半行段 lineNo 计入超限段偏移(`+len(dmg.Oversized)`);既有 memory 测试回归核对。
- **FR-107**: 测试卫生:leader_panic_test.go 死代码(`var _ = httptest.NewRecorder`)清除;fixture gate 增加已知真实标记断言(sub_session_id)。
- **FR-108**: 全量门禁绿;各组分提交,diff 范围与分组一一对应。
- **FR-109**: 路由决定如实记录:SC-001 全 allow 委派补测(证据面,不实施)、start/Close 并发窗口(FR-104 豁免)、flake 观察(保持观察)——三者在本变更 report 中标注「维持路由」。

## Key Entities *(include if feature involves data)*

- **监控键集**: watcher 的启动快照文件集合(裁定 A:仅启动时存在者)。
- **残留边缘**: lint 解析器已知未覆盖形态(行内块注释终止后接代码)。
- **deferred 台账**: 003..008 各变更 verify 报告的 deferred 清单;本变更为其批次清偿。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: T8-② 键集语义钉测试存在且 PASS(新建文件不进键集/无提醒,既有键不受影响)。
- **SC-002**: G1 残留边缘 fixture(008 终审失败形态)转绿;判别力测试经共享 helper 保持可红。
- **SC-003**: G2 双重故障防护测试落地(注入 publish panic 不崩溃)。
- **SC-004**: G3/G4 全部清偿(grep 可核注释、死代码消失、fixture gate 增强、[006] 证据补全)。
- **SC-005**: 全量门禁绿;分组提交;台账 deferred 清单归零(除明示路由保留项)。

## Assumptions

- 裁定 A 为最终产品决定(用户 2026-10-11 明示);未来如需动态纳管属新变更。
- 各 deferred 项均为独立小改,批次内无跨依赖;分组仅为提交组织。
- [003] SC-001 全 allow 委派补测属真实冒烟面——下一次真实冒烟变更时顺路执行,本批次不实施(文档注记已满足诚实性)。

## Out of Scope

- watcher 动态纳管新文件(裁定 A 排除)。
- start/Close 并发窗口修复(FR-104 豁免,超范围)。
- TestSubagentWiringEndToEnd flake 修复(保持观察)。
- 任何新功能、策略调整、依赖引入。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->
