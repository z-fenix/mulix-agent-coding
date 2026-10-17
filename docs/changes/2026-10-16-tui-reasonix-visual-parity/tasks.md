# TUI 视觉完全对齐 Reasonix（025）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** mulix TUI 交谈 chrome 与状态栏版式逐元素对齐 DeepSeek-Reasonix 实拍基线（横幅/气泡/助手头+槽/思考标记/回执形态/模式徽章/主题观感）。

**Architecture:** 全部落在既有块渲染器（transcript.go）、思考块（thinking.go）、回执与状态栏（status.go）、模型接线（model.go）——零接缝改动、零新增依赖、治理面零改动。正常完成的 `✓ 回合完成` 行移除（回执即收口）。

**Tech Stack:** 既有栈；零新增依赖。

**Spec:** `docs/specs/2026-10-16-tui-reasonix-visual-parity/spec.md`

**Design:** approved in chat（2026-10-16）：chrome 逐元素按基线帧移植（baseline-reasonix-frames.log 为形态权威）；回执字段映射 UsageEvent 真实字段；观测宽一律 transcriptContentWidth（024 判别力教训）。

## Global Constraints

- 治理语义零改动；TUI→control 单向；零新增依赖；界面中文（横幅提示行中文，形态对照 reasonix）。
- TDD：先红后绿；每任务 `go test ./... -count=1` 全绿 + `go vet ./...` + `gofmt -l $(git ls-files '*.go')` 才 commit；conventional 前缀；只 add 本任务文件；`.mulix/.runtime/**` 不入提交。
- 测试观测宽一律 `transcriptContentWidth(m.width)`（024 判别力教训——宽度差触发全量重建洗现场）。
- 编译期 RED 须台账记 Ruling。

## Review Focus

- 2 空格槽不得破坏既有宽度预算（槽宽从 render 宽度扣除后正文换行，窄端不越界）。
- `✓ 回合完成` 移除后，覆盖矩阵 TurnDone 行、keys_test 相关钉同步更新；⏹/✗ 语义不变。
- 回执字段映射不得歪曲 UsageEvent 口径（WriteTokens=cached？否——cached=ReadTokens、new=WriteTokens，见 spec）。
- 横幅只在会话启动渲染一次；/new 换绑不重复。

---

## Task 1: 交谈 chrome——横幅/气泡/助手头+槽/思考标记

**Files:**
- Modify: `internal/tui/transcript.go`（userBlock/assistantBlock render 槽与前缀；bannerBlock/assistantHeader 块型）、`internal/tui/thinking.go`（折叠行 `▸ 思考了 N 秒 (Ctrl+O)`——tok 槽移除（数据仍在回执 reasoning 字段）；流式标记行）、`internal/tui/model.go`（横幅在 tui.Run 装配后 append 一次——A1：不放 newTuiModel，防全部块数钉 +1 漂移）、`internal/tui/toolcard.go` + `internal/tui/model.go` 委派卡（卡片入 2 空格槽）、`internal/tui/thinking_test.go`（TestThinkingCollapse (b)(c)(d) 按「tok 槽移除」重写；TestNoZeroTokenRender 自然通过）、`internal/tui/mapping_test.go`（`✓ 已思考` 钉更新）、`internal/tui/selection_test.go:213`（`已思考` contains 钉更新）
- Test: `internal/tui/chrome_test.go`（新）

**Interfaces:**
- Consumes: 既有 block 接口、wrapcache（槽宽计入 render 宽度预算——`block.render(width)` 收到的是含槽的整行宽，实现改为「槽前缀 + wrapText(text, width-2)」拼接）。
- Produces: `bannerBlock{model string}`（两行：`◆ mulix · <model>` + 提示行——横幅 model 来源=Run 入口取一次 Snapshot.Model，空则顺延首个 statusTick flush，A2）；userBlock render = `  › ` + 换行文本；assistantBlock render = 每行 2 空格槽 + renderMarkdown(text, width-2)；**助手头行**：每回合首个内容块提交时合成 `◆ mulix` 头行块（flushAnswer/commitAnswerBlock 首块时机，chrome_test 断言）；toolCard/委派卡 render 每行 2 空格槽；思考折叠行 `▸ 思考了 N 秒 (Ctrl+O)`；流式标记行 `▎ thinking…`。

- [X] **Step 1: 写失败测试**——chrome_test.go：(a) 横幅块两行形态（A5：基线 `◆ reasonix  · deepseek-flash` 的 · 前双空格系其排版，本仓钉单空格 `◆ mulix · <model>`，拍帧仲裁）；(b) userBlock 首行 `  › 你好`；(c) assistantBlock 每行 2 空格开头；(d) toolCard render 每行 2 空格开头；(e) 思考折叠行 == `▸ 思考了 3 秒 (Ctrl+O)`（无 tok 槽）；(f) 思考流式首事件后块首行含 `▎ thinking…`；(g) 回合首个内容块前出现 `◆ mulix` 头行。
- [X] **Step 2: 验红**。Run: `go test ./internal/tui/ -run TestChrome -count=1`。Expected: FAIL（形态未实现）。
- [X] **Step 3: 实现**——按 Interfaces 落形态；横幅在 RunTUI 装配后作为首个块 append（仅一次；/new 换绑不重复——模型字段 pendingBanner 语义，换绑路径不置位）。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/tui/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l $(git ls-files '*.go')`。
- [X] **Step 5: Commit**

```bash
git add internal/tui/transcript.go internal/tui/thinking.go internal/tui/model.go internal/tui/toolcard.go internal/tui/thinking_test.go internal/tui/mapping_test.go internal/tui/selection_test.go internal/tui/chrome_test.go
git commit -m "feat(tui): 交谈 chrome 对齐 reasonix——横幅/气泡/助手头槽/思考标记(025 FR-001..004)"
```

## Task 2: 回执重构 + 完成行移除

**Files:**
- Modify: `internal/tui/status.go`（receipt 渲染——分隔线为 receipt 块 render 首行，A4 保持块拓扑）、`internal/tui/model.go`（TurnDone 分支：正常完成不再 appendLine 完成行）、`internal/tui/mapping_test.go`（TurnDone/Usage 行更新）、`internal/tui/keys_test.go`（:164 钉 `✓ 回合完成 t8`）、`internal/tui/markdown_test.go`（:153-155/:165-167 块数钉）、`internal/tui/thinking_test.go`（:99 块数钉）
- Test: `internal/tui/mapping_test.go`、`internal/tui/status_test.go`

**Interfaces:**
- Consumes: UsageEvent 字段（UncachedInputTokens/ReadTokens/WriteTokens/OutputTokens/ThinkingTokens/CostUSD）。
- Produces: 回执两块：分隔线块（`─────` 通栏，宽度=render 宽）+ 回执行 `本轮  N tok · in X · cached Y · new Z · out W · reasoning R · ≈$S`（S=CostUSD %.4f；零值字段照渲染）；TurnDone 正常完成零新增行；⏹/✗ 行保留原语义。

- [X] **Step 1: 写失败测试**——矩阵行更新：(a) Usage 行断言分隔线 + `本轮` 前缀 + `in/cached/new/out/reasoning` 字段次序（喂 UsageEvent 全字段）；(b) TurnDone 正常完成行 wantAbsent `回合完成`；(c) 取消行 ⏹ 与失败行 ✗ 语义保留（既有行不动）。status_test 回执单测同步改形态。
- [X] **Step 2: 验红**。Run: `go test ./internal/tui/ -run 'TestEventRenderMatrix|TestTurnReceipt' -count=1`。Expected: FAIL。
- [X] **Step 3: 实现**——receipt 渲染重构 + TurnDone 分支移除正常完成 appendLine（DurationMS 不再使用——折叠行已含 Ns 之外的时长信息由回执行不承载；DurationMS 字段留状态栏无需）。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/tui/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l $(git ls-files '*.go')`。
- [X] **Step 5: Commit**

```bash
git add internal/tui/status.go internal/tui/model.go internal/tui/mapping_test.go internal/tui/status_test.go internal/tui/keys_test.go internal/tui/markdown_test.go internal/tui/thinking_test.go
git commit -m "feat(tui): 回执对齐 reasonix 形态——分隔线+本轮字段序;完成行移除(025 FR-005)"
```

## Task 3: 状态栏版式 + 模式徽章 + 主题槽调优

**Files:**
- Modify: `internal/tui/status.go`（statusView 两行版式）、`internal/tui/theme.go`（徽章 pill 样式——accent 底色/反色；各元素槽位核对）
- Test: `internal/tui/status_test.go`

**Interfaces:**
- Consumes: Snapshot（Model/MeterReport.SessionHit=平均；TurnTokens 推本次命中=CacheRead/(Input+Write+Read)，A3）、activeTheme 槽（accent/dim/red）。
- Produces: 行 1：彩色徽章（accent 反白 pill，对照 reasonix `Ask ·` 位）+ 右侧 `模型 <model>`；行 2：`缓存 本次命中 X% · 平均 Y%  费用 $S`（上下文/压缩/余额段省略——D7）；会话 id8 移除。

- [X] **Step 1: 写失败测试**——status_test：(a) 行 1 含徽章 SGR（accent 反白）与 `模型 ` 前缀、不含会话 id；(b) 行 2 以 `缓存 本次命中` 开头、**不含 `上下文`**（D7 缺席断言）、含 `费用 $`；(c) TurnTokens 为零时本次命中显示 0.0%（不造数）。
- [X] **Step 2: 验红**。Run: `go test ./internal/tui/ -run TestStatusBar -count=1`。Expected: FAIL。
- [X] **Step 3: 实现**——statusView 版式重构 + theme.go 徽章样式（accent 背景/前景反色 lipgloss）。随本任务入台账 Ruling：spec FR-006「上下文 N tok (pct)」字样勘误（无数据源，D7 省略）——spec 文本随归档记录提交。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/tui/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l $(git ls-files '*.go')`。
- [X] **Step 5: Commit**

```bash
git add internal/tui/status.go internal/tui/theme.go internal/tui/status_test.go
git commit -m "feat(tui): 状态栏版式+模式徽章对齐 reasonix;默认主题槽位调优(025 FR-006/007)"
```

## Task 4: 拍帧对比验收（SC-001）

**Files:**
- Create: `docs/evidence/tui-visual-parity-2026-10-16.md`
- Modify: 前三任务暴露问题的修复（TDD）

**Interfaces:**
- Consumes: 出仓拍帧驱动器（C:\Users\Zhang\go\src\tui-visual）+ reasonix 基线（docs/specs/<change>/baseline-reasonix-frames.log）。

- [X] **Step 1: 重拍 mulix**——同款驱动、同序列（启动→你好→静默→/quit），重建 mulix-cli.exe 到 go/bin（仓根构建拷贝——写入管控）。
- [X] **Step 2: 逐元素比对**——六元素清单（横幅/气泡/助手头槽/思考标记/回执/状态栏）逐项对照 reasonix 基线帧，帧摘录入 evidence。
- [X] **Step 3: 全量收口**。Run: `go test ./... -count=1 && go vet ./... && gofmt -l $(git ls-files '*.go') && go test ./internal/lint/ -count=1`。
- [X] **Step 4: Commit**

```bash
git add docs/evidence/
git commit -m "docs(evidence): TUI 视觉对齐拍帧对比——六元素逐项同构(025 SC-001)"
```

---

## 计划自审（writing-plans Self-Review）

- **Spec 覆盖**：FR-001..004→T1；FR-005→T2；FR-006/007→T3；SC-001→T4；FR-008→各任务矩阵/治理零改动约束。无缺口。
- **Step 扫描**：实现步只给形态与机制；测试步给断言面。
- **类型一致**：bannerBlock/anchorPoint 式新块型走既有 block 接口；回执/状态栏消费既有 UsageEvent/Snapshot 字段。
- **Review Focus 钉点**：槽宽预算→T1 Step1(c)；完成行移除的矩阵/钉更新→T2 Step1；字段映射口径→T2 Step1(a)；横幅一次性→T1 Step3。
- **比例**：紧凑，无转写。
