# 调研：mulix 现有 TUI 全貌（specify 阶段参考）

> 来源：2026-10-15 独立调研代理对 mulix 仓的只读勘察。记录现状、约束与缺口，是重构的起点基线。

## 0. 关键事实修正

- TUI **不是**手写 ANSI：已是 **Bubble Tea v2 + Lipgloss v2**（go.mod：`charm.land/bubbletea/v2 v2.0.10`、`charm.land/lipgloss/v2 v2.0.6`）。CLAUDE.md §4「第三方依赖 NATS 唯一例外」与现实不符——P1 计划（docs/changes/001-p1-core-loop-and-cache-foundation/tasks.md:9）明确批准 cobra、bubbletea、lipgloss、toml、MCP SDK；「零第三方」的准确范围是 kernel 包（CLAUDE.md §3.3「leaf 级、零第三方依赖」）。
- 真机驱动证据：docs/evidence/smoke-tui-2026-10-15.md（ConPTY 驱动器出仓外构建，完成整回合生命周期；已知怪癖：ConPTY 输入写 5–15s 吞键，每 10s 重发可穿透）。

## 1. 位置与接线

- **全部 TUI 在一个文件**：`internal/cli/tui.go`（约 580 行，包 `cli`，组合根）；测试 `tui_test.go`（纯消息驱动状态机）+ `subagent_wiring_test.go:536+`（/agents 面板）。
- 入口：`mulix-cli` 无子命令 → root RunE → `RunTUI`（tui.go:564）：`openSession(o, resumeID, true, sink)` → `tea.NewProgram(m).Run()`（**零选项 = inline 渲染**）；退出时关闭「换绑后」的 handle（防 mcp-config 临时文件泄漏）。
- TUI 是**直连 `control.Sink`**（不是镜像/总线消费者）：`tuiSink` 把事件推进 1024 缓冲 channel；`waitForEvent(ch)` 为 tea.Cmd，逐事件 re-arm。状态数据**轮询**：500ms `statusTick` → `ctrl.Snapshot()`。
- 背压：`Publish` 满则阻塞——「前端可慢不可丢」（tui.go:37-41），天然限流引擎事件流。已知 Minor-C：换绑时积压 >cap 使旧泵收尾 publish 延迟（延迟而非丢失）。
- **Sink 不可重入红线**（control/controller.go:50-52）：Publish 实现内不得回调 Submit/Resume/Approve；TUI 全部控制器调用都在 Update/tea.Cmd 中。

## 2. 现状功能矩阵

| 功能 | 状态 | 说明 |
|---|---|---|
| 转写区 | **partial** | 纯 `[]string` 尾部截断（`height-3`），无视口/滚动/鼠标/alt-screen |
| 流式助手文本 | 已实现 | 每事件一行 `appendLine`（未按段落合并） |
| 工具调用/结果渲染 | **缺失** | `ToolUseRequestedEvent`/`ToolResultEvent` 在 handleEvent 无 case |
| 思考渲染 | **缺失** | `AssistantThinkingEvent` 未处理 |
| 编辑器 | partial | 单行 `"> " + input`；仅 Backspace/追加，无光标移动/多行/括号粘贴 |
| 历史召回 | **缺失** | 无 ↑/↓ |
| 键位 | partial | Ctrl+C 退出、Enter 提交、审批 a/s/d/Esc、/agents ↑↓c/esc/q |
| 授权卡 | 已实现 | `[a]本次允许 [s]本会话允许 [d]拒绝`；独占冻结编辑器；上游 120s fail-closed；Input JSON 未展示 |
| 状态栏 | 已实现 | `模型｜会话id8｜命中率%｜回合tN Ntok｜成本$X ❄冷回合N｜空闲/运行中`（500ms 轮询） |
| 主题 | **缺失** | 两个硬编码 lipgloss style |
| 会话恢复 | 已实现 | `/resume <engine-id>`、`/new` 异步换绑（drain 旧残的事件→重绑 channelSink）；无交互选择器 |
| 错误显示 | partial | 括号中文行（[提交失败]/[回合失败]/[引擎已退出]…），无分级样式 |
| 取消运行中回合 | **缺失** | `sessionController` 接口无 Cancel；Ctrl=C 只退出程序（`control.Controller.Cancel` 已存在，subagent 已用） |
| 斜杠命令 | 已实现 | /new /resume /cache /doctor /agents /quit；**/rewind 占位**（等 T32 引擎 checkpoint） |
| /agents 面板 | 已实现 | 列表/选择/取消运行中子代理 |

## 3. 事件→UI 现状（handleEvent，tui.go:202-227）

TurnStarted 不渲染；AssistantText → 转写；Notice → 行；ApprovalRequest/Resolved → 卡片+行；TurnDone/EngineExited → 行；Usage 忽略（状态栏轮询代替）；**ToolUseRequested/ToolResult/AssistantThinking/Delegation* 不渲染**（子代理可见性仅 /agents 面板）。ControlRequest 是引擎层信封，在 control 内部消化，不进 TUI。

## 4. 硬约束（重构必须遵守）

- 分层矩阵（internal/lint/rules.go 权威）：`internal/cli → internal/*`；**control 禁 import 前端**（TUI→control 单向）；TUI 经窄接口 `sessionController`（tui.go:25-33）消费 control 以便测试注入。若 TUI 拆新包（如 internal/tui）**必须先登记 layerMatrix**，否则 lint 红。
- 镜像先行（Controller 保证）→ Sink；1:1 不变量与 TUI 无关但不可破坏。
- 测试模式：纯消息驱动（`testMode` 不 re-arm 阻塞 cmd；测试 helper 同步执行 tea.Cmd 并回灌 msg）——新 TUI 须保持此可测形态；治理语义测试（TestApprovalExclusiveState 等）语义不变。
- 已修过的 bug 类（注释即维修地图）：Update 内同步 Submit/Close 冻结循环致授权卡不可达（tui.go:280-284、469-477）；换绑须关「新」handle（tui.go:575-577）。

## 5. 重构入口文件

1. `internal/cli/tui.go` — 现有 TUI 全部
2. `internal/cli/tui_test.go` — 状态机测试骨架（stub + testMode 模式）
3. `internal/cli/session.go` — openSession/sessionHandle/assembleController（换绑必经）
4. `internal/cli/root.go` — 命令路由与 Options.RunTUI 注入缝
5. `internal/control/controller.go` — Sink（:53）与 Snapshot（:1042）契约、不可重入红线（:50）
6. `internal/event/event.go` — TUI 必须渲染的事件词汇（含未渲染的 Tool/Thinking/Delegation）
7. `internal/lint/rules.go` — 新包登记处
8. `docs/evidence/smoke-tui-2026-10-15.md` — 真机驱动方法与 ConPTY 怪癖
