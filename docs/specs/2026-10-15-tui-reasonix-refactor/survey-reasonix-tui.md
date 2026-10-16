# 调研：DeepSeek-Reasonix TUI 全貌（specify 阶段参考）

> 来源：2026-10-15 独立调研代理对 `D:\workplace\std\DeepSeek-Reasonix`（GitHub esengine/Reasonix）的只读勘察。本文是重构目标形态的权威参考。

## 1. 基本盘

- Go 1.26，CGO-free 单二进制；TUI 即 `reasonix` 无子命令的交互模式（`cmd/reasonix/main.go` → `internal/cli/cli.go:963` `chatREPL` → `tea.NewProgram`）。
- 框架：**Bubble Tea v2 + Bubbles v2 + Lipgloss v2**（charm.land，v2.0.10/v2.2.1/v2.0.6），辅以 `charmbracelet/x/ansi`、`colorprofile`、`mattn/go-runewidth`、`rivo/uniseg`、`yuin/goldmark`（Markdown）、`alecthomas/chroma/v2`（diff 语法高亮）、`aymanbagabas/go-udiff`、`atotto/clipboard`。
- TUI 不独立成包：`internal/cli` 共 331 文件（166 非测试）。

## 2. 渲染架构（混合保留/立即式）

- **全接管 alt-screen**：上方 bubbles `viewport` 转写区 + 下方「底部固定区」（模态面板 → 编辑器 → 两行状态栏）；`frame()`（chat_tui.go:3229）每帧重组底部区（立即式），转写为保留内容。
- **语义保留转写**：`m.transcript []string`（已换行）+ `m.transcriptSources []transcriptSource`（kind/原始 markdown/工具名参数/历史），缩放时由源重排（`reflowTranscript`，transcript.go:242）。
- **增量换行缓存**（wrap_cache.go）：流式更新只重排受影响块（`invalidateWrapFrom(i)` + `syncWrappedLines`）。
- **原地改写**：运行中的思考块、流式答案块、运行中工具尾用 `setTranscriptBlock(idx,...)`。
- **流式 markdown**：只 flush 完整段落（blank line 在围栏外才算，`flushableMarkdownPrefix` chat_tui.go:3078），半开围栏不提前渲染。
- **思考流**：4096 字节 / 12 行有界尾部窗口（`reasoningViewMax`），全文留给 verbose。
- **滚动**：viewport + 末列可拖拽滚动条、滚轮、PgUp/PgDn、Ctrl+Home/End、`scrollFollowMode` 尾随状态机（scroll_state.go）、缩放锚点保阅读位置。
- **事件节流**：不按帧率限流，靠事件排水——每次 Update 最多消费 512 条（`maxEventDrain`），channel 缓冲 1024。

## 3. 线程模型

- Bubble Tea 单 goroutine 持 Update/View；控制器在自身 goroutine 跑回合，经 `event.Sink` 接口写入 1024 缓冲 channel；TUI 用长驻 cmd `waitForAgentEvent` 拉取（每条事件 = 一个 msg）。
- 定时器 cmd：1s 回合计时、spinner tick、80ms 边缘滚动、500ms 鼠标重启用、1.5s 复制提示过期。
- 审批/ask 为同步阻塞点：控制器 goroutine 停在 `ctrl.Approve` 等按键决议。

## 4. 功能清单（重构对标面）

| 域 | 要点 |
|---|---|
| 转写 | 横幅、用户气泡 `› …`、助手 markdown（`◆ Reasonix` 头 + 2 空格槽）、思考块 `▎ thinking…` →「thought for Ns」折叠、工具卡、回合回执、压缩卡、通知、恢复回放包 |
| 工具卡 | `⏺ 动词(主参)` 按类别着色圆点（读/写/Shell/进程），`⎿` 连接槽；写工具出 **diff 卡**（`✎ path +A -B`、行号、chroma 高亮、绿/红底条、40 行折叠 /diff-fold）；输出 8 行有界尾、Ctrl+B 展开（≤200 行）；失败红卡 `⊘ error` 含退出码 |
| 编辑器 | bubbles textarea 多行 1→8 行自适应、`❯ ` 仅首行、真实终端光标（IME 锚定）、16384 字符上限；Enter 提交、Alt+Enter/Ctrl+J/Shift+Enter 换行；Alt/Ctrl+方向键词移动；Ctrl+Backspace 删词 |
| 历史/粘贴 | ↑/↓ 召回提交历史（保草稿）；回合中 ↑/↓ 浏览持久收件箱队列；大粘贴折叠 `[Pasted text #N · M lines]`、提交还原；图片粘贴；`# note` 记忆、`! cmd` 直跑 Shell；回合中 Enter 排队、Ctrl+Enter 插话 |
| 补全 | 斜杠命令菜单（注册表缓存，非每键扫描）、参数级下钻、@ 文件引用（有界目录遍历+记忆化搜索）、↑/↓/Tab/Enter/Esc 语义、8 行窗口、NBSP 填充防 mintty 幽灵格 |
| 审批 | 横幅在编辑器上方；`1/y/Enter` 允许一次、`2/a` 本会话、`n/Esc` 拒绝、数字直选、↑/↓ 选择、Ctrl+C 取消整回合；plan 审批变体 |
| 模态 | rewind/resume/quick-picker/skill/mcp/复制/连接设置/`/clear` 确认等全部模态接管按键 |
| 模式 | **Shift+Tab** 循环 权限模式（只读→工作区写→YOLO→Plan）；**Ctrl+Y** YOLO 开关 |
| 状态栏 | 两行：行1 模式徽章+状态提示（左）、模型+effort（右）；行2 git 分支（异步刷新）、缓存命中率（回合/会话）、上下文仪表 `tokens (pct) · N% to compact`、jobs、成本 |
| 回执 | 每回合完成：`≈N tok · in/cached/new/out/reasoning · ≈$0.0000 · band` |
| 工作行 | `⣾ thinking · 12s · ↓1.2k · ✎ 2 in inbox`（阶段标签、重试、取消中） |
| 主题 | 暗亮双底色 × 8 强调色、18 语义槽（hex + 手选 256 色回退）、`/theme` 实时切换 + 扫场动画、OSC 11 底色探测、NO_COLOR |
| 选择/复制 | 左键拖选（松开即复制）、右键复制/粘贴、边缘自动滚动、编辑器内拖选、OSC 52 SSH 回退 |
| 会话 | `--resume` 选择器、`--continue`、/resume /rename /new /clear /export 等；回放以 replay bundle 整体提交 |
| 子代理 | 每子代理固定转写槽位（阶段+耗时+有界尾），`Tool.ParentID` 嵌套；后台任务进状态栏 jobs |
| 退出/看护 | 单一退出路径、SIGTERM 快照、双击 Ctrl+C 确认退出、10s 卡死看门狗（cancel→grace→kill）、诊断日志 |
| Windows/CJK | conhost 开 VT；ConPTY 鼠标自愈；glyphFit 量宽替换（Windows Terminal 下 no-op）；CJK markdown 修正、CJK 宽度换行、IME 真光标；i18n en/zh/zh-TW |

## 5. 引擎接缝（口径对照用）

- 出向：`event.Sink`（单方法 `Emit(Event)`）→ 1024 channel；33 种事件（TurnStarted/Reasoning/Text/ToolDispatch/ToolResult/ToolProgress/Usage/Notice/ApprovalRequest/TurnDone/Compaction*/Retrying/Steering…，全表见 `internal/event/event.go`）。
- 入向：`control.SessionAPI` 组合端口（13 子接口：Lifecycle/TurnControl/Approvals/History/Status/Settings/Inbox…，`internal/control/port.go:310`）；审批同步阻塞。
- 运行时重建：/model 等经 builder 原子换控制器，旧控制器延迟到 `p.Run()` 返回再关。

## 6. 移植优先 Top 文件

1. `internal/cli/chat_tui.go`（模型/Update/View/键/流式/斜杠分发）
2. `internal/event/event.go`（UI 事件词汇）
3. `internal/control/port.go`（引擎驱动端口）
4. `internal/cli/chat_tui_events.go`（事件→UI 摄取语义）
5. `internal/cli/transcript.go`（transcriptSource/滚动条/选择/回放）
6. `internal/cli/wrap_cache.go` + `scroll_state.go`（增量换行 + 尾随状态机，最微妙两处）
7. `internal/cli/md.go`（markdown→ANSI，CJK 修正）
8. `internal/cli/toolcard.go` + `diffview.go`
9. `internal/cli/cli.go:963`（装配/关闭/控制器切换）
10. `internal/cli/theme.go` + `status_footer.go`
