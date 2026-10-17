# 引擎启动语义对齐 SDK（027）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task.

**Goal:** `Host.Start` 非阻塞化（spawn+泵就绪即返回）、init 事件流式消化 + 对账前置到到达时点（失败引擎自终止）、TUI 即现 + 就绪态徽章——消除 30s 黑屏，治理不弱化。

**Architecture:** 全部在既有文件内：claudehost.go（Start/route 重写）、controller.go（就绪态+兜底）、status.go/tui（徽章消费）。接口零变更（engine.Host/llm.Provider/FakeHost/adapter 不动，024 判定）。规格修正（FR-002 对账时点后移）已获用户批准（2026-10-17 设计审批）。

**Tech Stack:** 既有栈；零新增依赖。

**Spec:** `docs/specs/2026-10-17-go-engine-provider/spec.md`

**Design:** `.mulix/.runtime/2026-10-17-go-engine-provider/specs/2026-10-17-go-engine-provider-design.md`（Ruling-1..4；builder check 6 问已收编；§4.4 协议差异清单为「不需要/已对齐」结论的权威记录）

## Global Constraints

- 治理红线全数继承：镜像先行、围堵校验、审批独占、120s fail-closed、可慢不可丢、对账前输出不入镜像。
- 接口零变更：engine.Host/llm.Provider/FakeHost/adapter 方法集不动（builder Q2 裁定）。
- TDD；每任务 `go test ./... -count=1` 全绿 + `go vet ./...` + `gofmt -l $(git diff --name-only <base> HEAD 2>/dev/null)` 才 commit；conventional 前缀；只 add 本任务文件。
- 测试观测口径与 024/025 一致（FakeHost 注入 + 纯消息驱动）。

## Review Focus

- Start 返回后引擎秒退：exit 事件在无消费者窗口（tea.NewProgram 前）不丢（eventsOut 缓冲纪律 + RunTUI 接线次序）。
- 对账前 stash 不镜像：病态 CLI 的未验证输出不得进入 events.jsonl（围堵校验语义在新路径下不变）。
- 60s 兜底不起算于 spawn、不被 Noise/stderr 复位；init 到达解除。
- mulix run 错误码位移（2→1）如实记录（builder Q6，用户已批准设计含此项）。

---

## Task 1: Start 非阻塞化 + route 对账时点重置（engine 层）

**Files:**
- Modify: `internal/engine/claudehost.go`（Start 重写：删握手等待窗/provisional 分支/HandshakeTimeout；拒绝路径重安置到 init 事件处理时——合成 exit「refused: …」；route stash 语义重定义=对账前暂存不镜像不分发，init 通过 flush 释放、失败丢弃）、`internal/engine/claudehost_test.go`、`internal/engine/types_test.go`（握手相关测试重写）
- Test: 同上（重写既有测试为主）

**Interfaces:**
- Consumes: 既有 route/monitor/pump 结构、validateHandshake（原地重置时点到 init 事件）。
- Produces: `Start` 返回语义=spawn+泵运行；init 事件照常产出（InitInfo 载荷）；对账失败=合成 exit 事件 + eventsOut 关闭（现有 refused 形态搬迁）。

- [X] **Step 1: 重写握手相关测试为期望形态（先红）**——须重写的既有四测试：TestStartRefusesVersionMismatch（:196 同步拒绝）、TestStartProvisionalWithoutInit（:312，引用 HandshakeTimeout 会编译红）、TestDeferredDriftRefusal（:343，同）、TestMalformedStdoutFailsPump（:477，改为经事件流观测）；新钉：(a) Start 在 spawn 后 N ms 内返回（init 未到）；(b) init 事件在首条 user 消息后到达且正常分发；(c) 版本不符 → 合成 exit「refused: …」+ 事件流关闭；(d) 对账前引擎输出不入镜像（route stash 单测）。**新增关键钉（评审 Issue 1）：pre-init 引擎秒退（monitor !ready 分支与 route pumpErr&&!ready 分支）必须产出 exit 事件（Code/Reason）后再关 eventsOut**——现状只 signalStart 无事件，非阻塞 Start 下原因不呈现。
- [X] **Step 2: 验红**。Run: `go test ./internal/engine/ -count=1`。Expected: FAIL（现行为阻塞 30s/直接放行）。
- [X] **Step 3: 实现**——Start 重写 + route 改造（stash 重定义 + validateHandshake 移至 init 事件处理 + refused 合成 exit 无条件化）；**失败路径补事件**：monitor !ready 分支与 route pumpErr&&!ready 分支改为产出 exit 事件（Code/Reason）后 finishEvents（替代 signalStart）；退役 startDone/startOnce/startErr 机制（评审 Issue 1）。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/engine/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l $(git diff --name-only HEAD 2>/dev/null) 2>/dev/null; echo FMT`。Expected: internal/engine 绿且**全量绿**（评审核实：control 测试全为 FakeHost/stub 驱动，T1 不产生 control 层红——中间态优于计划预期）。
- [X] **Step 5: Commit**

```bash
git add internal/engine/
git commit -m "feat(engine): Start 非阻塞化——spawn 即返回,init 流式消化,拒绝重安置(027 FR-001)"
```

## Task 2: control 就绪态 + 60s 兜底 + 消费面收口

**Files:**
- Modify: `internal/control/controller.go`（initSeen 宽限退役；就绪态 NOT_READY/READY/FAILED 进 Snapshot；首 Submit 起算 60s 兜底；对账失败 exit 消费呈现）、`internal/control/controller_test.go`、`internal/engine/events.go`（如需 Kind 不变——预计零改动）
- Test: `internal/control/controller_test.go`

**Interfaces:**
- Consumes: Task 1 的新 Start 语义 + refused exit 形态。
- Produces: `Snapshot.Ready string`（NOT_READY/READY/FAILED——TUI 消费）；Submit 不等 init；60s 兜底（首 Submit 起算、Noise/stderr 不复位、init 解除）。

- [X] **Step 1: 写失败测试**——(a) init 未到时 Submit 直写 stdin（Submit 现状本就不门控 init——评审 A4 核实；真正的退役目标是 start() 的 initSeen 宽限 select controller.go:427-448，红灯来自 Ready 迁移与 60s 兜底断言）；(b) Snapshot.Ready 迁移 NOT_READY→READY（init 到达+对账通过）→FAILED（refused exit）；(c) 60s 兜底（**新时钟注入缝：可注入 timer/after 字段**——现有 now func() 无法快进 time.After，评审 A3；首 Submit 起算，Noise 不复位）；(d) refused exit → ✗ 呈现 + 镜像留痕。
- [X] **Step 2: 验红**。Run: `go test ./internal/control/ -count=1`。Expected: FAIL（Snapshot 无 Ready 字段等）。
- [X] **Step 3: 实现**——就绪态机 + 兜底 + initSeen 退役 + refused 消费。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/control/ ./internal/tui/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l $(git diff --name-only HEAD 2>/dev/null) 2>/dev/null; echo FMT`。
- [X] **Step 5: Commit**

```bash
git add internal/control/
git commit -m "feat(control): 引擎就绪态+60s 兜底——Submit 不等 init,失败快速终止(027 FR-002/004/005)"
```

## Task 3: TUI 就绪徽章 + 拍帧验收

**Files:**
- Modify: `internal/tui/status.go`（徽章消费 Snapshot.Ready）、`internal/tui/status_test.go`
- Create: `docs/evidence/tui-startup-parity-2026-10-17.md`

**Interfaces:**
- Consumes: Task 2 的 Snapshot.Ready。
- Produces: 徽章三态（`引擎就绪中` dim / 模式徽章 / 红色 FAILED）；拍帧计时证据（≤3s 三次中位）。

- [X] **Step 1: 写失败测试**——Snapshot.Ready 三态断言（NOT_READY→「引擎就绪中」、READY→模式徽章、FAILED→红徽章）。
- [X] **Step 2: 验红**。Run: `go test ./internal/tui/ -run TestStatusBar -count=1`。Expected: FAIL。
- [X] **Step 3: 实现**。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./... -count=1 && go vet ./... && gofmt -l $(git diff --name-only HEAD 2>/dev/null) 2>/dev/null; echo FMT && go test ./internal/lint/ -count=1`。
- [X] **Step 5: 拍帧验收**——重建 go\bin exe + engine.lock 同目录确认在位；tui-visual 驱动拍帧（启动→你好→/quit）×3：启动到可交互 ≤3s 中位；非仓目录启动路径复验；证据落 docs/evidence/tui-startup-parity-2026-10-17.md（时间线+帧摘录+镜像 1:1）。
- [X] **Step 6: Commit**

```bash
git add internal/tui/ docs/evidence/
git commit -m "feat(tui): 引擎就绪徽章三态;拍帧验收≤3s(027 FR-003/SC-001)"
```

> 归档随录：spec FR-002 勘误文本（对账时点=init 到达时）随本变更归档记录提交（设计 §7 用户已批准）。

---

## 计划自审（writing-plans Self-Review）

- **Spec 覆盖**：FR-001→T1；FR-002→T1+T2（规格修正已批准：对账时点=init 到达时）；FR-003→T3；FR-004→T2；FR-005→T1/T2；FR-006→全任务约束；FR-007→设计 §4.4 清单已结论化（Clarifications 随归档更新）。无缺口。
- **Step 扫描**：实现步给机制，测试步给断言面；Step 4 的「engine 绿而全量红」为任务间显式中间态（T2 收口）——非漏项。
- **类型一致**：Snapshot.Ready 消费链（T2 产、T3 消）；refused exit 形态沿用既有。
- **Review Focus 钉点**：无消费者窗口不丢事件→T1 (a)(c)+pre-init 秒退钉；stash 不镜像→T1 (d)；兜底起算/复位→T2 (c)；错误码位移→台账。
- **比例**：紧凑。
