# TUI 重构（对齐 DeepSeek-Reasonix）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 把 mulix 的 TUI 从 `internal/cli/tui.go` 单文件 inline 形态重构为对齐 DeepSeek-Reasonix 的全屏接管 TUI（`internal/tui` 新包），治理三层语义零改动。

**Architecture:** 先登记 layerMatrix 并把现有 TUI 机械迁入 `internal/tui`（接缝：`tui.Sink` / `tui.SessionController` / `tui.SessionSwapper` / `tui.Options`），随后按关注点渐进重建：转写块模型 + viewport + 增量换行缓存 + 尾随状态机 → 流式 markdown/思考/工具卡 → 取消阶梯 → 输入/授权/状态升级 → 主题/diff/选择复制。组合根 `internal/cli` 只留装配与 SessionSwapper 实现。分三期（Task 1–12 = P1，13–17 = P2，18–22 = P3），每期独立可交付。

**Tech Stack:** Go 1.26 + Bubble Tea v2 + Lipgloss v2（既有）；新增 bubbles v2、goldmark、chroma/v2、go-udiff、clipboard（FR-017 裁定清单，呈现层专用）。

**Spec:** `docs/specs/2026-10-15-tui-reasonix-refactor/spec.md`

**Design:** `.mulix/.runtime/2026-10-15-tui-reasonix-refactor/specs/2026-10-15-tui-reasonix-design.md`（D1–D17 决策与 §7 移植契约为实现依据；参考仓 `D:\workplace\std\DeepSeek-Reasonix`（MIT）仅作移植源与对照）

## Global Constraints

- 治理语义零改动：审批独占冻结、120s 超时 fail-closed、先镜像后 Sink、Sink 不可重入（Publish 内禁回调 Submit/Resume/Approve）、「可慢不可丢」（1024 缓冲 + 阻塞 Publish，语义为延迟非丢失）。
- 分层矩阵（`internal/lint/rules.go` 权威）：`internal/tui` 必须登记为 `{"internal/*"}`；`control` 不得 import 前端；新包未登记即 lint 红。
- 第三方依赖（FR-017 用户裁定）：仅可新增 `charm.land/bubbles/v2`、`github.com/yuin/goldmark`、`github.com/alecthomas/chroma/v2`、`github.com/aymanbagabas/go-udiff`、`github.com/atotto/clipboard`；kernel 与治理层（control/guard/cache/mirror）零新增。
- 界面字符串一律中文（FR-015）；Windows 一等目标（ConPTY / Windows Terminal）。
- TDD 强制：先红后绿；每任务 `go test ./... -count=1` 全绿 + `gofmt -l .` 干净 + `go vet ./...` 才 commit；conventional 前缀（feat/test/docs/chore）；只 add 本任务文件；`.mulix/.runtime/**` 不入提交。
- 编译期 RED（迁移/重构类任务）须在台账记 Ruling 说明形态。
- 不做：@ 文件引用补全、多语言、/rewind 透传、引擎侧任何改动、config 包改动。

## Review Focus

- 事件洪峰（单 Update >512 条 / 积压逼近 1024）：必须全部送达且不丢——「延迟非丢失」。钉：`TestEventChannelBackpressure`（Task 2 迁移）+ `TestDrainCoalescing`（Task 3）。
- CJK 宽字符与未闭合代码围栏在流式提交与缩放重排中：不撕裂样式、不渲染半开围栏、宽度正确。钉：Task 5/7 的表驱动测试。
- 授权独占态的按键泄漏（授权卡期间编辑器/全局键仍生效）。钉：Task 16 迁移并扩展治理测试。
- 换绑窗口残余事件归属（close→drain→open 顺序被打破会把旧会话尾巴算到新会话头上）。钉：Task 2 的 swapper 顺序测试。
- ConPTY 输入吞键怪癖（5–15s 窗口）下 TUI 状态机不自愈（忙碌门闩卡死等）。钉：Task 12/22 真机冒烟断言。

---

## Task 1: layerMatrix 登记 + internal/tui 包骨架

**Files:**
- Modify: `internal/lint/rules.go:27-29`（组合根区块）
- Create: `internal/tui/doc.go`
- Test: `internal/lint/lint_test.go`（package lint——`allowFor`/`pkgAllowed` 是 lint 包内未导出符号，测试必须同包）

**Interfaces:**
- Consumes: 无。
- Produces: `internal/tui` 包（package tui）可编译且分层合法；后续所有任务在其上工作。

- [ ] **Step 1: 写失败测试**——断言 `internal/tui` 已登记为组合根级（允许 `internal/*`）。在 lint 测试中加：

```go
func TestLayerMatrixHasTUI(t *testing.T) {
    allow, ok := allowFor("internal/tui")
    if !ok {
        t.Fatal("internal/tui 未登记 layerMatrix（fail-closed leaf 缺省会静默禁止一切内部导入）")
    }
    if !pkgAllowed("internal/tui", "internal/control") {
        t.Fatal("internal/tui 应为组合根级（internal/*）")
    }
}
```

- [ ] **Step 2: 跑测试确认红**。Run: `go test ./internal/lint/ -run TestLayerMatrixHasTUI -count=1`。Expected: FAIL（未登记）。
- [ ] **Step 3: 登记**——`internal/lint/rules.go` 组合根区块加一行（带注释）：`"internal/tui": {"internal/*"},  // TUI 前端（023 重构：与 cli 同级组合根，cli 只留装配）`。同目录建 `internal/tui/doc.go`：包文档（职责一句话）+ MIT 归属声明「本包部分内容改编自 DeepSeek-Reasonix (MIT License, © 2026 Reasonix Contributors, https://github.com/esengine/DeepSeek-Reasonix)」。
- [ ] **Step 4: 跑测试确认绿**。Run: `go test ./internal/lint/ -count=1 && go build ./...`。Expected: PASS。
- [ ] **Step 5: Commit**

```bash
git add internal/lint/rules.go internal/tui/doc.go
git commit -m "feat(tui): 登记 internal/tui 组合根级分层并建包骨架(023)"
```

## Task 2: 现有 TUI 机械迁移至 internal/tui（行为零变化）

**Files:**
- Create: `internal/tui/sink.go`、`internal/tui/model.go`（由 `internal/cli/tui.go` 平移）、`internal/tui/swapper.go`（接口）、`internal/tui/tui_test.go`（由 `internal/cli/tui_test.go` 平移改写）、`internal/tui/agents_test.go`（`internal/cli/subagent_wiring_test.go:561-619` 的 TUI 面板测试平移）
- Modify: `internal/cli/tui.go`（缩为组合根入口 `RunTUI` + `cliSwapper`）、`internal/cli/subagent_wiring_test.go`（移除 TUI 面板测试）、`internal/cli/session.go`（不动装配，仅接 swapper）
- 锚点：`sessionController`（tui.go:25-33）、`capTuiEventCh`（:41）、`tuiSink`（:46-58）、`waitForEvent`（:60-64）、`tuiModel`（:73-93）、`newTuiModel`（:100）、`RunTUI`（:564-579）、换绑序列（:478-499，顺序 close→drain→open 不得变）、`sessionHandle`（session.go:618）、`openSession`（session.go:646）

**Interfaces:**
- Consumes: Task 1 的 `internal/tui` 包。
- Produces（后续全部任务依赖的接缝，签名钉死）:

```go
package tui
// sink.go——control.Sink 实现（原 tuiSink 平移改名导出）
type tuiEvent struct{ ev any }
type Sink struct{ ch chan tuiEvent }
func NewSink() *Sink
func (s *Sink) Publish(ev any)            // 满则阻塞（可慢不可丢）
func (s *Sink) Events() <-chan tuiEvent

// model.go——原 tuiModel/appendLine/handleEvent/View/键位原样平移
// swapper.go——会话换绑接缝（cli 实现；TUI 只见接口）
type SessionController interface {
    Submit(ctx context.Context, text string) error
    Approve(toolUseID string, allow bool, resolvedBy string) error
    ApproveSession(toolUseID, resolvedBy string) error
    Snapshot() control.Snapshot
    Subagent() *subagent.Manager
}
type SessionSwapper interface {
    CloseCurrent()                                                        // 关当前会话（收尾 publish 入 sink）
    Open(resumeID string, sink control.Sink) (SessionController, string, error) // 同 sink 开新/续链会话
}
type Options struct{ /* 窄函数字段：迁移时枚举 m.opts 实际使用点逐一映射（doctor 探针等），禁止搬整个 cli.Options */ }
func Run(o Options, ctrl SessionController, selfID string, swapper SessionSwapper, events <-chan tuiEvent) error
```

- `internal/cli` 侧：`cliSwapper` 实现 `SessionSwapper`（持 `Options`+当前 `*sessionHandle`；`Open` 内部走 `openSession(o, resumeID, true, sink)`）；`RunTUI(ctx, o, resumeID)`（root.go:122 的注入缝签名不变）：`withDefaults` → `tui.NewSink()` → `openSession(o, resumeID, true, sink)` → 构造 `cliSwapper` → `tui.Run(...)` → `defer` 收口**当前** handle（tui.go:575-577 语义：换绑后必须收口新 handle）。

- [ ] **Step 1: 编译期 RED**——先在 cli 侧把 `tui.go` 内容剪切为 `internal/tui/model.go`+`sink.go`（package tui，导出接缝），`internal/cli/tui.go` 重写为入口+`cliSwapper`。此任务 RED 为编译失败形态（旧测试引用已移动符号），台账记 Ruling：「编译期 RED——机械迁移，行为零变化」。
- [ ] **Step 2: 平移并改写测试**——`tui_test.go` 移入 `internal/tui`（`stubController` 实现 `tui.SessionController`；`TestSlashNew`/`TestSlashResume` 拆两半：tui 侧改为注入假 `SessionSwapper` 断言 `/new` `/resume` 触发 `CloseCurrent`+`Open` 且重绑 ctrl/selfID；cli 侧新增 `TestCliSwapper`（真装配 + `engine.NewFakeHost` + `fixtures/engine/happy-turn.jsonl`）断言 `Swap` 顺序 close→（通道归 tui 排空）→open 且旧 handle 已收口）。**`TestSlashCache` 不能机械平移**：其种子经 `mustOpenSession`→cli 未导出 `openSession`（tui 不得 import cli——循环依赖），改写为直连 `cache.NewMeter(MeterOptions{Root: root, SessionID: id})` + `Observe` + `Close` 播种（不需要会话）；`mustOpenSession`/`seedUsageSession`/`seedSession`/`discardSink` 留在 cli 服务 `TestCliSwapper`。`TestTUIAgentsPanel`（subagent_wiring_test.go:561-619）移入 `internal/tui/agents_test.go`。迁移要点：模型持 `sink *Sink` 字段替代 `sendCh`/裸通道（`SessionSwapper.Open` 需要 tui 供给 `control.Sink`，drain 读同一通道）；`handle` 字段移除，改为持 `swapper SessionSwapper` + `swapping` 忙门闩；换绑 drain（排空通道残余）留在 tui 侧、顺序不变；doctor 能力经 `tui.Options` 的**预合成回调**由 cli 注入（`doctorProbes`/`doctorChecks` 均为 cli 未导出，禁止整体搬迁 doctor.go）。
- [ ] **Step 3: 验证迁移等价**。Run: `go build ./... && go test ./internal/tui/ ./internal/cli/ -count=1`。Expected: 全 PASS（迁移前后测试集合等价，新增 2 个 swapper 测试）。
- [ ] **Step 4: 全量 + lint**。Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix-cli lint layers`（若 lint 以子命令暴露；否则 `go test ./internal/lint/`）。Expected: 全绿、gofmt 无输出、分层干净。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/ internal/cli/tui.go internal/cli/tui_test.go internal/cli/subagent_wiring_test.go internal/cli/session.go
git commit -m "refactor(tui): 现有 TUI 机械迁移 internal/tui——接缝 Sink/SessionController/SessionSwapper(023)"
```

## Task 3: 事件排水与根模型关注点骨架（D3/D4）

**Files:**
- Modify: `internal/tui/model.go`
- Test: `internal/tui/drain_test.go`

**Interfaces:**
- Consumes: Task 2 的 `tuiModel`、`tuiEvent`、`testMode`。
- Produces: `func (m *tuiModel) drainEvents(first tuiEvent)`（一次 Update 内非阻塞排水至多 `maxEventDrain = 512` 条后统一 `finalize()`）；关注点子结构占位字段（`transcriptState`、`scrollState`、`composerState`、`approvalState`、`statusState`、`themeState`——本任务先建结构，字段随后续任务迁入）。

- [ ] **Step 1: 写失败测试**——

```go
func TestDrainCoalescing(t *testing.T) {
    m := newTestModel(t) // testMode：不回挂阻塞 cmd
    const n = 700        // > maxEventDrain(512)，分两次 Update 消费完
    for i := 0; i < n; i++ {
        m.sinkPub(NoticeEvent{Seq: int64(i), Content: "x"}) // 测试辅助：直塞通道
    }
    m.update(tuiEvent{ev: <-m.events}) // 第一条触发排水
    got := m.transcriptLineCount()
    if got != 512 { t.Fatalf("第一次 Update 应消费 512 条, got %d", got) }
    m.update(m.drainOne(t)) // 继续消费（测试循环驱动）
    if m.transcriptLineCount() != n { t.Fatalf("应全部消费不丢, got %d", n) }
}
```

- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestDrainCoalescing -count=1`。Expected: FAIL（现逐条消费）。
- [ ] **Step 3: 实现**——`handleEvent` 入口改为 `drainEvents`：首条处理后在 `for` 中 `select { case ev := <-m.events: ...; default: }` 计数至 512；循环后调用一次 `finalize()`（本任务先空实现，Task 5 起放 wrapcache sync）。`maxEventDrain = 512` 常量（对照参考 turn_lifecycle.go:147）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./internal/tui/ -run TestDrainCoalescing -count=1 && go test ./... -count=1`。Expected: PASS。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/model.go internal/tui/drain_test.go
git commit -m "feat(tui): 一次 Update 排水至多 512 条事件——洪峰合并消费(023 D4)"
```

## Task 4: alt-screen 布局 + 转写块模型 + viewport（FR-001）

**Files:**
- Create: `internal/tui/transcript.go`、`internal/tui/layout.go`
- Modify: `internal/tui/model.go`（View 改为布局合成；`tea.NewProgram` 加 `tea.WithAltScreen()`）
- Test: `internal/tui/transcript_test.go`、`internal/tui/layout_test.go`
- 依赖入库：`go get charm.land/bubbles/v2`

**Interfaces:**
- Consumes: Task 2/3 的模型与排水。
- Produces:

```go
// transcript.go——语义保留转写块
type block interface{ render(width int) []string } // 语义源按宽渲染（含样式）
type transcriptState struct {
    blocks []block
    vp     viewport.Model // bubbles；喂给用克隆行切片
}
func (ts *transcriptState) append(b block)            // 追加（触发后缀重渲染）
func (ts *transcriptState) rewriteLast(b block)       // 原地改写运行中块（流式/思考/工具尾）
func (ts *transcriptState) lines(width int) []string  // 全量行（Task 5 起走 wrapcache）
// layout.go——帧合成：转写视口(高 = H-底部固定区行数) + 底部固定区
func (m *tuiModel) frame() string // 底部固定区 = 模态面板(有则) → 编辑器/授权卡 → 状态行
```

- [ ] **Step 1: 写失败测试**——(a) `TestBlockAppendRewrite`：append 3 块 + rewriteLast 后 `lines(80)` 反映改写且块数不变；(b) `TestFrameLayout`：给定 H=24，视口行数 = 24 − 底部固定区行数；底部区含编辑器行与状态行；(c) `TestViewportCloneFeed`：喂给后改内部缓存不影响 viewport 已持有内容（克隆契约，对照参考 wrap_cache.go:80-88）。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestBlock|TestFrame|TestViewportClone' -count=1`。Expected: FAIL（符号未定义）。
- [ ] **Step 3: 实现**——`block` 三实现起步：`plainBlock`（notice/turn 行，本任务承载旧行为迁移：现有 `m.lines` 的行包装为 plainBlock）、`userBlock`、`assistantBlock`（本任务先按纯文本渲染，Task 7 换 markdown）。View 改走 `frame()`；`tea.NewProgram(m, tea.WithAltScreen())`。
- [ ] **Step 4: 确认绿 + 全量**（现有迁移测试必须仍绿——旧行为在 alt-screen 下等价呈现）。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/ go.mod go.sum
git commit -m "feat(tui): alt-screen 全接管布局+语义保留转写块+bubbles viewport(023 FR-001)"
```

## Task 5: 增量换行缓存（D6 移植，契约=设计 §7.1）

**Files:**
- Create: `internal/tui/wrapcache.go`
- Modify: `internal/tui/transcript.go`（`lines(width)` 走缓存）、`internal/tui/model.go`（`finalize()` 放 sync）
- Test: `internal/tui/wrapcache_test.go`
- 移植源：`D:\workplace\std\DeepSeek-Reasonix\internal\cli\wrap_cache.go`（138 行；文件头注 MIT 改编来源）

**Interfaces:**
- Produces: `wrapCache{ wrappedLines []string; wrapWidth int; wrapBlockCount int; wrapBlockLines [][]string }`；方法签名与参考一致（`clearWrapCache/syncWrappedLines(contentW, forceFull)/rebuildWrappedLinesFull/feedViewportContent/wrapBlockLines/flattenBlockWraps/invalidateWrapFrom`），块渲染走 `block.render(width)`。

- [ ] **Step 1: 写失败测试**（表驱动，覆盖契约四角）——suffix 路径（append 后只重排后缀：用计数探针统计 `render` 调用次数，append 第 4 块时前 3 块 render 不再被调）；`invalidateWrapFrom` 后缀丢弃；宽度变更触发全量重建；`flattenBlockWraps` 与 join-then-wrap 等价性（契约前提，参考 wrap_cache.go:99-101）。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestWrapCache -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——按参考契约逐函数移植，`wrapBlockLines` 改为调 `block.render`；**换行用 lipgloss 宽度渲染**（对照参考 transcript.go:306-311 `wrapTranscript`——其注释明确否定 x/ansi 的 Hardwrap：跨行断点的样式会敞开渗入填充与下一行，正是 Review Focus「不撕裂样式」钉点）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./internal/tui/ -run TestWrapCache -count=1 && go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/wrapcache.go internal/tui/wrapcache_test.go internal/tui/transcript.go internal/tui/model.go
git commit -m "feat(tui): 按块增量换行缓存——流式只重排后缀(023 D6, MIT 改编)"
```

## Task 6: 尾随状态机与滚动（D5 移植，契约=设计 §7.2；FR-002）

**Files:**
- Create: `internal/tui/scrollstate.go`
- Modify: `internal/tui/model.go`（键位）、`internal/tui/layout.go`（末列滚动条——静态呈现即可，拖拽在 Task 21）
- Test: `internal/tui/scrollstate_test.go`
- 移植源：`D:\workplace\std\DeepSeek-Reasonix\internal\cli\scroll_state.go`（87 行；文件头注 MIT 改编来源）

**Interfaces:**
- Produces: `scrollFollowMode`（`scrollFollowTail|scrollUserScrolled`）、`markUserScrolled/markFollowTail/shouldFollowTail/modalOpen/syncScrollModeAfterGesture`（语义逐条对照参考注释；`modalOpen` 的豁免集 = 授权卡 + agents 面板 + 补全菜单 + 会话选择器，随 Task 15/16 扩）；`useLegacyViewportScrollClear` Windows=false 裁定一并移植。键位：PgUp/PgDn、滚轮、Ctrl+Home/End、上翻不拉回、回底恢复跟随、`forceGotoBottom` 单帧豁免。

- [ ] **Step 1: 写失败测试**——(a) 手势状态机表驱动（滚上→userScrolled；滚到底→followTail；forceGotoBottom 单帧）；(b) `TestModalOpenNoUnfollow`：授权卡开合（底部区高度变化）不翻模式——这是参考 #6430/#6978 的回归钉；(c) 上翻后新事件到达视口不动、Ctrl+End 后恢复尾随。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestScroll|TestModalOpen' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——逐函数移植 + 接入键位与 `finalize()`（跟随态下 feed 后 GotoBottom）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./internal/tui/ -count=1 && go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/scrollstate.go internal/tui/scrollstate_test.go internal/tui/model.go internal/tui/layout.go
git commit -m "feat(tui): 显式尾随状态机+滚动键——模态开合不打断跟随(023 D5, MIT 改编)"
```

## Task 7: markdown 基础渲染 + 流式段落提交（D13/D17；FR-003；CLAUDE.md §4 勘误）

**Files:**
- Create: `internal/tui/markdown.go`
- Modify: `internal/tui/transcript.go`（assistantBlock 走 markdown 渲染）、`internal/tui/model.go`（AssistantTextEvent → pending 缓冲 + 段落提交）
- Test: `internal/tui/markdown_test.go`
- 依赖入库：`go get github.com/yuin/goldmark`（+ `charmbracelet/x/ansi` 转直接已在 Task 5）
- 移植源：参考 `internal/cli/md.go` 的渲染思路与 `chat_tui.go:3078` `flushableMarkdownPrefix` 语义

**Interfaces:**
- Produces:

```go
// markdown.go
func renderMarkdown(src string, width int, th *theme) []string // goldmark→ANSI（本任务：标题/加粗/行内代码/围栏着色/列表；CJK 强调修正必带）
func splitFlushablePrefix(md string) (stable, tail string)     // 「围栏外空行」判定完整段落；半开围栏整体留在 tail
```

- [ ] **Step 1: 写失败测试**——(a) `TestSplitFlushable`：完整段落出 stable；`"```go\nx"` 半开围栏整体留 tail；围栏内的空行不算段落界；(b) `TestRenderMarkdownBasics`：标题/加粗/行内代码/围栏渲染出对应 ANSI（断言含 SGR 序列与内容）；(c) `TestCJKEmphasis`：中文强调不误判（对照参考 md.go:107 fixCJKEmphasis）；(d) `TestStreamingParagraphCommit`：依次喂消息级 AssistantTextEvent 分片，断言只有完整段落成为块、TurnDone 时冲刷 tail 残留（D17：消息级粒度）。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestSplit|TestRender|TestCJK|TestStreaming' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——goldmark 自定义 renderer（AST→带样式的行）；`splitFlushablePrefix` 移植；`handleEvent` 的 AssistantTextEvent 改走 pending/flush。
- [ ] **Step 4: 勘误 CLAUDE.md §4**——「第三方依赖：NATS 为唯一例外」改为：「第三方依赖按清单治理（见各 change spec 裁定；现状：cobra/bubbletea/lipgloss/toml/goja/NATS/MCP SDK/bubbles/goldmark/chroma/go-udiff/clipboard）；『零第三方』的准确范围是 kernel leaf 级」。这是 FR-017 裁定的文档义务。
- [ ] **Step 5: 确认绿 + 全量**。Run: `go test ./... -count=1 && gofmt -l .`。
- [ ] **Step 6: Commit**

```bash
git add internal/tui/ go.mod go.sum CLAUDE.md
git commit -m "feat(tui): goldmark markdown 基础渲染+流式段落提交;勘误 CLAUDE.md 依赖条款(023 D13/FR-017)"
```

## Task 8: 思考块——有界尾窗与折叠（D14；FR-004）

**Files:**
- Create: `internal/tui/thinking.go`
- Modify: `internal/tui/model.go`（AssistantThinkingEvent/TurnStarted/TurnDone 接线）
- Test: `internal/tui/thinking_test.go`

**Interfaces:**
- Consumes: Task 4 `rewriteLast`。
- Produces: `type thinkingBlock struct{ tail string; startedAt, endedAt time.Time; tokens int64; collapsed bool }`；`func utf8SafeTail(s string, maxBytes int, maxLines int) string`（4096/12，参考 reasoningViewMax 语义）；折叠行格式 `✓ 已思考 Ns · N tok`（Ns = 事件到达时刻差；tok = 回合末 UsageEvent.ThinkingTokens——**禁用 AssistantThinkingEvent.EstimatedTokens**，恒零字段）。

- [ ] **Step 1: 写失败测试**——(a) `TestTailWindowBound`：超长/超行输入截到 4096 字节（UTF-8 不截半字符）/12 行；(b) `TestThinkingCollapse`：思考流 rewrite 同块 → 下一非思考事件或回合收口折叠为单行「✓ 已思考 3s · 45 tok」；(c) `TestNoZeroTokenRender`：EstimatedTokens 恒零不渲染任何「0 tok」。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestTail|TestThinking|TestNoZero -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——有界窗 + 折叠 + 模型接线（思考开始/结束本地计时戳）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/thinking.go internal/tui/thinking_test.go internal/tui/model.go
git commit -m "feat(tui): 思考块有界尾窗+折叠——token 从回合末 Usage 取(023 D14)"
```

## Task 9: 工具卡（FR-005）

**Files:**
- Create: `internal/tui/toolcard.go`
- Modify: `internal/tui/model.go`（ToolUseRequestedEvent/ToolResultEvent 接线）
- Test: `internal/tui/toolcard_test.go`
- 移植源：参考 `internal/cli/toolcard.go`（242 行）的卡片版式与动词映射

**Interfaces:**
- Produces:

```go
type toolState uint8 // toolRunning | toolDone | toolFailed
type toolCard struct {
    toolUseID, tool, arg string
    state               toolState
    outputTail          []string // ≤8 行有界尾（来自 ToolResult.Content）
    failedDetail        string   // IsError 时的失败详情行
}
func toolVerb(tool string) string  // Read→「读」Write→「写」Edit→「改」Bash→「运行」Glob/Grep→「搜」mcp__*→短名
func toolArgKey(tool string, input json.RawMessage) string // 主参数（path/cmd/query…缺省 json 摘要截断）
func (c *toolCard) render(width int) []string // ⏺ 动词(主参) · 状态 · ⎿ 输出尾；失败红卡 ⊘
```

- [ ] **Step 1: 写失败测试**——(a) 事件序列 `ToolUseRequested(Bash, cmd)→ToolResult(content, IsError=false)` 渲染出运行中→完成的卡片与 ≤8 行输出尾；(b) IsError=true 渲染红卡含详情；(c) 超长输出截尾；(d) 主参数提取表驱动（path/cmd/缺省）。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestToolCard -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——卡片结构 + 动词/主参映射 + 事件接线（同 ToolUseID upsert）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/toolcard.go internal/tui/toolcard_test.go internal/tui/model.go
git commit -m "feat(tui): 工具卡——动词/主参/状态/有界输出尾/失败红卡(023 FR-005)"
```

## Task 10: 取消语义阶梯（D10；FR-011）

**Files:**
- Modify: `internal/tui/swapper.go`（`SessionController` 增 `Cancel(ctx context.Context) error`）、`internal/tui/model.go`（Esc/Ctrl+C 阶梯、气泡上屏时机、取消意图）、`internal/tui/keys.go`（新文件，集中键位语义）、`internal/tui/tui_test.go`（`stubController` 增 Cancel 记录——接口扩成员后否则编译红）、`internal/cli/tui.go`（cliSwapper 实现 Cancel 转发 `ctrl.Cancel`——controller.go:968）
- Test: `internal/tui/keys_test.go`

**Interfaces:**
- Consumes: Task 2 的 SessionSwapper。
- Produces: `SessionController.Cancel(ctx context.Context) error`（签名=control.Controller.Cancel）；模型字段 `cancelIntent bool`、`pendingSubmit *pendingSubmit{ text string; gen uint64 }`；语义（逐条钉测试）：用户气泡在 **TurnStartedEvent 到达时**上屏（Enter 只置 pendingSubmit）；忙碌时 Esc = 取消意图：气泡未上屏 → 丢弃在途提交（代际标志使提交收据作废）+ 还原草稿；回合进行中 → `ctrl.Cancel`；空闲 Esc = 清空输入。取消后收口行 `⏹ 回合已取消 tN`（凭本地 cancelIntent，**不看** TurnDone.Subtype/IsError）。Ctrl+C：有选区复制（P3 前恒走清空）→ 清空输入 → 1.5s 双击退出；**授权卡独占态单击 Ctrl+C 直接退出**（Minor-4 逃生口保留）。

- [ ] **Step 1: 写失败测试**——(a) `TestBubbleCommitsOnTurnStarted`：Enter 后 TurnStarted 前转写无用户气泡；(b) `TestEscBeforeTurnStartedDiscards`：Esc 后草稿还原、submit 收据作废、无气泡；(c) `TestEscRunningCancels`：stub 断言 Cancel 恰一次；(d) `TestCancelledTurnRendersPausedLine`：cancelIntent=true 且 TurnDone 到达 → `⏹ 回合已取消`（非「回合失败」）；(e) `TestApprovalCtrlCQuits`：授权态 Ctrl+C → quit msg（迁自现语义）。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestBubble|TestEsc|TestCancelled|TestApprovalCtrlC' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——键位阶梯 + pendingSubmit 代际 + cliSwapper.Cancel 转发。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/swapper.go internal/tui/model.go internal/tui/keys.go internal/tui/keys_test.go internal/tui/tui_test.go internal/cli/tui.go
git commit -m "feat(tui): Esc 取消回合+语义阶梯——气泡回合开始才上屏,取消行凭本地意图(023 D10/FR-011)"
```

## Task 11: 事件渲染覆盖矩阵（FR-016/SC-004）

**Files:**
- Modify: `internal/tui/model.go`（补齐映射表残留项：Notice Level 分级、Delegation 四事件卡、EngineExited 行为保持）
- Create: `internal/tui/mapping_test.go`

**Interfaces:**
- Produces: 覆盖矩阵测试——spec 设计 §6 映射表逐行断言（15 种事件 × 输入序列 → 转写块序列/状态变化）。

- [ ] **Step 1: 写失败测试**——`TestEventRenderMatrix`：表驱动，每行 `{name, events []any, wantBlocks []string 子串断言, wantState func(m) bool}`；必须覆盖全部 15 种（TurnStarted/AssistantText/AssistantThinking/ToolUseRequested/ToolResult/ApprovalRequest/ApprovalResolved/Usage/Notice/TurnDone/EngineExited/DelegationStarted/Completed/Cancelled/Failed）。Missing 项红灯：Notice 的 warning 级 `! ` 前缀、Delegation 卡 `⏺ 子代理 <id8>` 与完成收口（token/成本）、EngineExited 后编辑器可用。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestEventRenderMatrix -count=1`。Expected: FAIL（列出不满足行）。
- [ ] **Step 3: 实现**——补齐缺失渲染分支（委派卡复用 toolCard 版式可简化为 dedicated delegationBlock）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/model.go internal/tui/mapping_test.go
git commit -m "feat(tui): 15 种事件渲染覆盖矩阵全绿(023 FR-016)"
```

## Task 12: P1 出口——治理回归 + 真机冒烟一

**Files:**
- Create: `docs/evidence/smoke-tui-p1-2026-10-XX.md`（实际日期）
- Modify: 无生产代码（修复冒烟暴露问题除外——按 TDD 补测再修）

**Interfaces:**
- Consumes: Task 1–11 全部。

- [ ] **Step 1: 治理回归确认**——Run: `go test ./internal/tui/ ./internal/cli/ -run 'TestApproval|TestSubmitFlow|TestBusyGate|TestSlashCache|TestEventChannelBackpressure|TestCliSwapper' -count=1`。Expected: 全 PASS（键位仍为迁移期 a/s/d；授权键位升级在 Task 16；TestCliSwapper 在 package cli，两包都要点名否则 -run 无匹配静默通过）。
- [ ] **Step 2: ConPTY 真机冒烟一**——按 `docs/evidence/smoke-tui-2026-10-15.md` 的驱动器方法（出仓构建驱动器），驱动 `mulix-cli` 完成：启动 → 一回合含工具调用任务 → 期间 PgUp 上翻（新输出不拉回）→ Ctrl+End 回底 → 窗口 resize → Esc 取消第二回合 → 正常退出。断言：工具卡出现且状态流转、流式段落上屏、思考块折叠、滚动/重排/取消行为、镜像 events.jsonl 1:1。
- [ ] **Step 3: 证据落盘**——`docs/evidence/smoke-tui-p1-*.md`：引擎版本/工作区/时间线/退出码/关键帧摘录；ConPTY 吞键怪癖如实记录。任何断言失败 → `systematic-debugging` 流程修复后重跑。
- [ ] **Step 4: 全量收口**。Run: `go test ./... -count=1 && go vet ./... && gofmt -l .`。Expected: 干净。
- [ ] **Step 5: Commit**

```bash
git add docs/evidence/
git commit -m "docs(evidence): TUI P1 出口真机冒烟——alt-screen/滚动/重排/取消通过(023)"
```

## Task 13: 多行编辑器 + 历史召回（D12；FR-007）

**Files:**
- Create: `internal/tui/composer.go`
- Modify: `internal/tui/model.go`（composerState 迁入 textarea）、`internal/tui/layout.go`（编辑器动态高度 1→8；`❯` 仅首行）
- Test: `internal/tui/composer_test.go`
- 依赖入库：`charm.land/bubbles/v2` 已入（Task 4），textarea 组件随取

**Interfaces:**
- Produces: `type composerState struct { ta textarea.Model; history []string; histIdx int; draft string }`；键位：Enter 提交 / Alt+Enter、Ctrl+J 换行 / ↑↓ 空闲时历史召回（保草稿、↓ 返回草稿）/ Alt|Ctrl+方向键词移动 / Ctrl+Backspace 删词 / CharLimit 16384 / 真实终端光标（`SetVirtualCursor(false)`，IME 锚定）。

- [ ] **Step 1: 写失败测试**——(a) `TestHistoryRecallKeepsDraft`：输入草稿 → 提交 → 输新草稿 → ↑ 两次召回 → ↓ 到底还原草稿；(b) `TestNewlineKeys`：Alt+Enter/Ctrl+J 换行而 Enter 提交；(c) `TestComposerDynamicHeight`：1 行文本 → 1 行高，9 行文本 → 8 行高截断；(d) `TestWordOps`：Ctrl+Backspace 删词。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestHistory|TestNewline|TestComposer|TestWordOps' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——textarea 接入与键位绑定（对照参考 chat_tui.go:700 configureChatTextarea）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/composer.go internal/tui/composer_test.go internal/tui/model.go internal/tui/layout.go
git commit -m "feat(tui): 多行编辑器+历史召回——动态高度/换行键/词级编辑(023 FR-007)"
```

## Task 14: 粘贴折叠（FR-008）

**Files:**
- Modify: `internal/tui/composer.go`
- Test: `internal/tui/paste_test.go`

**Interfaces:**
- Consumes: Task 13 composerState。
- Produces: `type pasteFold struct { id int; lines int; body string }`；`composerState.folds []pasteFold`；阈值 `≥3 行 或 ≥200 字符`（builder 可调常量 `pasteFoldThresholdLines/Chars`）；折叠记号 `[粘贴 #N · M 行]` 可整体删除；提交时还原全文（含记号位置替换）。

- [ ] **Step 1: 写失败测试**——(a) `TestPasteFolds`：bracketed paste 10 行 → 记号入编辑器、folds 记录原文；(b) `TestFoldDeleteRemovesAll`：删除记号（含相邻字符）后原文本体不残留；(c) `TestSubmitExpandsFolds`：提交文本含原文全文而非记号；(d) 低于阈值不折叠。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestPaste|TestFold|TestSubmit -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——`tea.PasteMsg` 拦截 + 记号管理 + 提交展开。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/composer.go internal/tui/paste_test.go
git commit -m "feat(tui): 大粘贴折叠为可删记号——提交还原(023 FR-008)"
```

## Task 15: 斜杠补全菜单 + /resume 会话选择器（FR-009）

**Files:**
- Create: `internal/tui/complete.go`、`internal/tui/resume_picker.go`
- Modify: `internal/tui/model.go`、`internal/tui/swapper.go`（`tui.Options` 增 `ListSessions func() []SessionSummary`）、`internal/cli/tui.go`（cli 侧实现：`sessionx.ScanSessions` + `SessionInfo{ID, EngineID, Model, Workspace, Unattended, CreatedAt}` → `SessionSummary`）
- Test: `internal/tui/complete_test.go`、`internal/tui/resume_picker_test.go`

**Interfaces:**
- Consumes: Task 2 SessionSwapper；Task 13 编辑器。
- Produces:

```go
type SessionSummary struct{ ID, EngineID, Model, Workspace, CreatedAt string; Unattended bool }
// complete.go——命令目录（/new /resume /cache /doctor /agents /quit…；/theme 于 Task 18 落地时入目录）+
// 菜单：≤8 行窗口、↑↓ 选择、Tab/Enter 补全、Esc 关闭、输入过滤
type completeMenu struct{ items []string; sel int; query string }
// resume_picker.go——列表态模态：↑↓ 选择、Enter 换绑（走 SessionSwapper）、Esc 关闭
```

- [ ] **Step 1: 写失败测试**——(a) `TestCompleteNav`：`/` 弹菜单 → 过滤 → ↑↓/Enter/Esc 语义；(b) `TestCompleteAlreadyPresentSubmits`：命中唯一项且已完整 → Enter 直接提交（参考语义）；(c) `TestResumePickerListsAndSwaps`：注入 3 个 SessionSummary → 选择第 2 → 断言 swapper 收到对应 EngineID；(d) `TestResumePickerEsc`。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestComplete|TestResumePicker' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——菜单 + 选择器 + `modalOpen()` 豁免集扩充（Task 6 留的口）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/ internal/cli/tui.go
git commit -m "feat(tui): 斜杠补全菜单+/resume 交互式会话选择器(023 FR-009)"
```

## Task 16: 授权卡升级（D11；FR-010）

**Files:**
- Create: `internal/tui/approval.go`（从 model.go 拆出授权卡）
- Modify: `internal/tui/model.go`
- Test: `internal/tui/approval_test.go`（迁 `TestApprovalExclusiveState`/`TestApprovalDenyAndSession` 并改键位）

**Interfaces:**
- Consumes: Task 2/3 模型与治理语义（独占冻结、120s fail-closed 上游不动）。
- Produces: 键位 `1/y/Enter`→Approve（允许一次）、`2/a`→ApproveSession、`n/Esc`→拒绝；卡片显示 `工具 <tool>（id8）` + 参数摘要（Input JSON 平铺截断 ≤2 行，`func inputSummary(raw json.RawMessage, w int) string`）；`ApprovalResolvedEvent` 裁决行保留 `[裁决] id → decision/source`；Ctrl+C 单击退出保留。

- [ ] **Step 1: 迁移并改写测试**——先按新键位改写两个治理测试 + 新增 `TestApprovalInputSummary`（摘要截断）、`TestApprovalKeysFull`（1/y/2/a/n/Esc 全映射）。Run: `go test ./internal/tui/ -run 'TestApproval' -count=1`。Expected: FAIL（新键位未实现）。
- [ ] **Step 2: 实现**——键位映射 + 参数摘要；独占/超时语义一行不动。
- [ ] **Step 3: 确认绿 + 全量**。Run: `go test ./internal/tui/ -run TestApproval -count=1 && go test ./... -count=1`。Expected: PASS。
- [ ] **Step 4: Commit**

```bash
git add internal/tui/approval.go internal/tui/approval_test.go internal/tui/model.go
git commit -m "feat(tui): 授权卡升级——参考键位+参数摘要,治理语义不变(023 FR-010)"
```

## Task 17: 回合回执 + 双行状态栏（D16；FR-012）

**Files:**
- Create: `internal/tui/status.go`
- Modify: `internal/tui/model.go`（UsageEvent 接线回执；statusState 迁入）
- Test: `internal/tui/status_test.go`

**Interfaces:**
- Consumes: Task 4 块模型；`control.Snapshot`（controller.go:1042）。
- Produces: `type receipt struct { turn int64; usage UsageEvent }` → 块 `✓ 回合 tN · 入N/写N/读N/出N/思N tok · $0.xxxx · 命中N%`（UsageEvent 驱动，D16）；状态行 1：`<模式> · <状态>` 左 + `模型 · 会话id8` 右；行 2：`<工作区> · <Snapshot 可得字段>`：命中率/回合累计/成本/冷回合；上下文仪表：Snapshot 有上下文 token 字段则渲染 `tokens (pct)`，无则显示可得子集（D7 兜底，禁造数据）。

- [ ] **Step 1: 写失败测试**——(a) `TestTurnReceipt`：喂 UsageEvent 断言回试行字段齐全（对 Task 13 文档口径：UsageEvent 字段 UncachedInputTokens/WriteTokens/ReadTokens/OutputTokens/ThinkingTokens/CostUSD/HitRate）；(b) `TestStatusBarTwoRows`：行数=2、左模式右模型、命中率来自 Snapshot；(c) `TestContextGaugeFallback`：Snapshot 无上下文字段时显示可得子集不 panic。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestTurnReceipt|TestStatusBar|TestContextGauge' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——回执块 + 双行状态渲染（照参考 status_footer.go 版式，文案中文）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/status.go internal/tui/status_test.go internal/tui/model.go
git commit -m "feat(tui): 回合用量回执+双行状态栏(023 FR-012/D16)"
```

## Task 18: 主题系统（D8；FR-014）

**Files:**
- Create: `internal/tui/theme.go`
- Modify: `internal/tui/model.go`（themeState 接线；`/theme` 命令）、全部渲染点改走主题槽
- Test: `internal/tui/theme_test.go`
- 移植源：参考 `internal/cli/theme.go`（555 行）的结构思路（语义槽 + 256 手选回退 + colorprofile 退避）

**Interfaces:**
- Produces: `type theme struct { name string; dark bool; accent, dim, red, green, yellow, cyan, magenta, fg, bg lipgloss.Color /*…语义槽*/ }`；预设 4 强调色：`graphite/ember/aurora/midnight` × 暗/亮两底；`func themeByName(name string) (theme, bool)`；`/theme <name>` 实时切换（会话级内存态，**不写 config**）；`NO_COLOR`/无色终端经 colorprofile 自动退避。

- [ ] **Step 1: 写失败测试**——(a) `TestThemePresets`：4×2 预设齐全、`themeByName` 未知名 false；(b) `TestThemeSwitch`：/theme ember 后渲染输出含 ember accent 的 SGR；(c) `TestNoColorFallback`：设 `NO_COLOR=1`（t.Setenv）渲染无真彩 SGR。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestTheme|TestNoColor -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**——槽位定义 + 预设 + 接线；把现有硬编码 `styleStatus/styleCard` 收编入主题。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/theme.go internal/tui/theme_test.go internal/tui/
git commit -m "feat(tui): 主题系统——暗亮×4强调色,NO_COLOR 退避,会话级(023 FR-014/D8)"
```

## Task 19: markdown 高保真——表格 + chroma 围栏高亮（FR-014/Story4）

**Files:**
- Modify: `internal/tui/markdown.go`
- Test: `internal/tui/markdown_test.go`（追加用例）
- 依赖入库：`go get github.com/alecthomas/chroma/v2`

**Interfaces:**
- Consumes: Task 7 renderMarkdown。
- Produces: 围栏块经 chroma 按语言高亮（主题随暗/亮切换）；goldmark 表格 AST → 对齐表格渲染（CJK 宽度感知，`runewidth` 已在模块图）。

- [ ] **Step 1: 写失败测试**——(a) `TestFenceChromaHighlight`：```go 围栏渲染含关键字色 SGR；(b) `TestTableRender`：3×2 表格对齐渲染、CJK 单元格不错位；(c) 未知语言围栏降级纯色。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestFence|TestTable' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/markdown.go internal/tui/markdown_test.go go.mod go.sum
git commit -m "feat(tui): 表格保真+chroma 代码围栏高亮(023 Story4)"
```

## Task 20: diff 卡（D9；FR-005/Story4）

**Files:**
- Create: `internal/tui/diffview.go`
- Modify: `internal/tui/toolcard.go`（写类工具结果挂 diff 视图）
- Test: `internal/tui/diffview_test.go`
- 依赖入库：`go get github.com/aymanbagabas/go-udiff`

**Interfaces:**
- Consumes: Task 9 toolCard；chroma（Task 19）。
- Produces: `func diffFromInput(tool string, input json.RawMessage) (diffInput, bool)`——从工具 Input 结构化字段提取可 diff 对（Edit 类 `old_string/new_string`、Write 类整文件；字段名以 claude CLI 工具 schema 实况为准，实现者对照真实 Input 样例）；`func renderDiff(d diffInput, width int, th theme) []string`——`✎ 改 <path> +A -B` 头 + 行号 hunk + 增删行色；`diffFromInput` 返回 false → 普通工具卡（降级路径）。

- [ ] **Step 1: 写失败测试**——(a) `TestDiffFromEditInput`：Edit Input → diff 行含 `-old`/`+new` 与行号；(b) `TestDiffHeaderCounts`：`+A -B` 计数正确；(c) `TestDiffFallback`：无结构字段的 Input → diffFromInput=false → 卡片无 diff 视图。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run TestDiff -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**（go-udiff 计算 + chroma 着色；折叠上限随 Ctrl+B 在 Task 21）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/diffview.go internal/tui/diffview_test.go internal/tui/toolcard.go go.mod go.sum
git commit -m "feat(tui): diff 卡——工具 Input 结构化字段计算,降级路径(023 D9)"
```

## Task 21: 选择复制 + Ctrl+O/B 展开 + 滚动条拖拽（Story4）

**Files:**
- Modify: `internal/tui/transcript.go`（选择态 + 滚动条拖拽）、`internal/tui/thinking.go`（Ctrl+O 展开）、`internal/tui/toolcard.go`（Ctrl+B 展开至 200 行）、`internal/tui/keys.go`
- Test: `internal/tui/selection_test.go`
- 依赖入库：`go get github.com/atotto/clipboard`

**Interfaces:**
- Produces: 转写左键拖选（行内区间 + 高亮）→ 松开复制（clipboard；**OSC 52 仅留接口不实现**——相对设计 §8 的降范围由 executor 在台账记 Ruling：SSH 专项非目标）；`Ctrl+O` 折叠思考块 ↔ 全文；`Ctrl+B` 输出尾 ↔ 展开至 200 行上限；末列滚动条拖拽跳转。

- [ ] **Step 1: 写失败测试**——(a) `TestSelectCopy`：拖选区间 → clipboard 内容 = 选中文本；(b) `TestCtrlOVerbose`：折叠块展开显示全文、再按折叠；(c) `TestCtrlBExpand`：输出尾展开 ≤200 行截断；(d) `TestScrollbarDrag`：拖拽映射视口偏移。
- [ ] **Step 2: 确认红**。Run: `go test ./internal/tui/ -run 'TestSelect|TestCtrlO|TestCtrlB|TestScrollbar' -count=1`。Expected: FAIL。
- [ ] **Step 3: 实现**（鼠标事件接线对照参考 transcript.go 选择态与 scrollbarThumb）。
- [ ] **Step 4: 确认绿 + 全量**。Run: `go test ./... -count=1`。
- [ ] **Step 5: Commit**

```bash
git add internal/tui/ go.mod go.sum
git commit -m "feat(tui): 文本选择复制+Ctrl+O/B 展开+滚动条拖拽(023 Story4)"
```

## Task 22: P3 出口——真机冒烟二 + 全量收尾

**Files:**
- Create: `docs/evidence/smoke-tui-p3-2026-10-XX.md`
- Modify: 冒烟暴露问题的修复（TDD）

**Interfaces:**
- Consumes: Task 1–21 全部。

- [ ] **Step 1: ConPTY 真机冒烟二（SC-001 全清单）**——驱动 `mulix-cli` 完成整回合（含工具调用与 diff 类工具结果）：工具卡+diff 卡、流式段落、思考折叠+Ctrl+O、回合回执、双行状态栏、主题切换 + NO_COLOR 启动、粘贴折叠 + 历史召回 + 斜杠补全、授权卡新键位真实裁决（含 120s 超时 fail-closed 路径——上游计时观察）、Esc 取消、镜像 1:1。
- [ ] **Step 2: 证据落盘**——`docs/evidence/smoke-tui-p3-*.md` 全要素摘录；失败项走 systematic-debugging 修复重跑。
- [ ] **Step 3: 全量收口**。Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go test ./internal/lint/ -count=1`。Expected: 全绿干净。
- [ ] **Step 4: Commit**

```bash
git add docs/evidence/
git commit -m "docs(evidence): TUI P3 出口真机冒烟——SC-001 全清单通过(023)"
```

---

## 计划自审记录（writing-plans Self-Review）

- **Spec 覆盖**：FR-001→T4；FR-002→T6；FR-003→T7；FR-004→T8；FR-005→T9/T20；FR-006→T5（语义重排）；FR-007→T13；FR-008→T14；FR-009→T15；FR-010→T16；FR-011→T10；FR-012→T17；FR-013→T2/T3（背压+排水）；FR-014→T18/T19；FR-015→Global（中文文案贯穿）；FR-016→T11；FR-017→T4/T7/T19/T20/T21 依赖入库 + T7 勘误；FR-018→三期即任务序；FR-019→T2（/agents 迁移）/T15（/cache//doctor 随命令目录保留）/T22（/rewind 占位验证）。无缺口。
- **Step 扫描**：实现步均为「签名+方案一句」，无函数体转写；测试步给断言要点与命令。
- **类型一致性**：`SessionController/SessionSwapper/Sink/tuiEvent/block.render(width)/toolCard/renderMarkdown/splitFlushablePrefix/theme` 各任务引用同名同形（T2 定接口，后续只消费）。
- **Review Focus 钉点**：洪峰→T2/T3；CJK/围栏→T5/T7/T19；授权泄漏→T16；换绑残余→T2；ConPTY 自愈→T12/T22。
- **比例**：计划以签名/断言/命令为主，未抄参考实现代码体。
