# Feature Specification: TUI 全面再设计（dsh 设计语言与布局移植）

**Change**: `2026-10-17-dsh-tui-visual-redesign`

**Created**: 2026-10-17

**Status**: Draft

**Input**: User description: "按 \\wsl.localhost\Ubuntu-26.04\home\zhang\workplace\deepseek-harness TUI 重新设计本项目TUI"

> 背景：deepseek-harness（dsh）没有终端 TUI——其命令行入口（apps/cli）只做 profile/plugin 管理，交互面是 Web UI（apps/web + packages/client）。因此本变更的参考对象是 dsh Web UI 的设计系统：`packages/client/ui-theme/src/styles/design-platform.css` 的设计令牌（DeepSeek 蓝品牌色、neutral-bluish 中性层、状态色、diff 色、明暗双板）、`ui-tool`/`ui-chat` 的组件语言（工具行、气泡、卡片）、以及整体布局结构（侧栏 + 会话主列 + 信息面板）。用户裁定（Clarifications Q1）：**全面移植 dsh 布局**——不止视觉换装，布局级结构一并移植。现有 TUI（023 建成、025/027/2026-10-16 打磨）为过渡形态；治理回路、镜像、渲染管线（先持久化后发布、fail-closed、泵单写者）不在改动面内。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - dsh 视觉系统落地 (Priority: P1)

用户启动 mulix TUI：底色、正文层级、强调色、状态色呈现 dsh 设计语言——冷调中性暗底（源自 dsh dark 基底 rgb(21,21,23) 及其表面层）、DeepSeek 蓝（rgb(122,170,255) 族）为默认强调、文字主/次/三级层级、语义状态色。横幅/工具卡/状态栏/回执/通知全部换装；用户一眼认出「这是 DeepSeek 家的界面」。

**Why this priority**: 视觉身份是本变更的第一诉求，也是后续布局工作的视觉地基（面板复用同一令牌系统）。

**Independent Test**: 单测——SGR 钉点断言各渲染点槽位色值与 dsh 派生表同源；暗/亮两模式分别断言。

**Acceptance Scenarios**:

1. **Given** 真彩终端、暗色模式，**When** 启动 TUI 并完成一个含工具调用的回合，**Then** 横幅/头行/工具卡强调、状态栏、回执着色均来自 dsh 派生槽位（单测钉点），无 Reasonix 旧值残留。
2. **Given** 亮色模式（-light 变体），**When** 渲染同样内容，**Then** 各槽位取 dsh light 板派生值，可读性不劣于现状。

### User Story 2 - 治理与既有交互零回归 (Priority: P1)

用户在重设计后的 TUI 里做与之前完全相同的事：审批卡按 1/y/2/a/n/Esc 裁决、Esc 语义阶梯取消回合、流式段落渐显、思考折叠、粘贴折叠、补全、/resume、/theme、/agents 等全部照旧；审批 fail-closed 行为逐字节不变。用户感知到的差异只有观感与新增的面板能力。

**Why this priority**: 治理不变量（fail-closed、键位、独占态）是本仓红线；布局与面板改造不得触碰。

**Independent Test**: 既有全量测试套件（20 行事件渲染矩阵、治理、键位测试）以语义不变口径全绿。

**Acceptance Scenarios**:

1. **Given** 全量套件，**When** 重设计落地后执行 `go test ./... -count=1`，**Then** 全绿；治理/矩阵测试如有行更新仅限形态钉点（颜色/字面），行为断言不变。
2. **Given** 审批场景真机冒烟，**When** 按键裁决，**Then** 治理结果与镜像留痕与重设计前一致。

### User Story 3 - 多栏布局骨架 (Priority: P2)

用户看到 dsh 式整体布局：左侧会话栏 + 中央会话主列（转写 + 编辑器）+ 可开合的信息面板区；状态栏维持底部双行（含模式徽章）。宽端（≥120 列）三区并存；中端折叠右区；窄端（<80 列）降级为与现状等价的单列形态——布局是渐进增强，不是窄端惩罚。

**Why this priority**: 布局骨架是面板特性的承载体，必须先于面板内容落地。

**Independent Test**: 单测——按宽度档位断言 frame 分区构成（宽/中/窄三档）；窄端渲染输出与现状等价性断言。

**Acceptance Scenarios**:

1. **Given** 140 列宽端，**When** 启动，**Then** 左栏 + 主列 + 面板区并存，主列宽度仍满足 80 列阅读线。
2. **Given** 70 列窄端，**When** 启动，**Then** 呈现单列形态（与现状等价），无折行错位。
3. **Given** 中端开合面板，**When** 键位切换面板，**Then** 主列宽度重排、阅读锚点不丢（复用 2026-10-16 锚点机制）。

### User Story 4 - 左栏会话列表 (Priority: P2)

用户在左栏直接看到本仓会话清单（复用 resume 选择器的数据源）：当前会话高亮，其余按最近活跃排列，选中即恢复对应会话——不再必须走 /resume 模态。空态（无其他会话）给出行动指引文案而非空白。

**Why this priority**: 会话切换是 dsh 布局里侧栏的核心职责，也是 mulix 现有数据（sessionx 镜像清单）能诚实支撑的特性。

**Independent Test**: 单测——清单渲染/高亮/选中断言（数据源与 resolveResume 同谓词）；真机冒烟切换一次。

**Acceptance Scenarios**:

1. **Given** 镜像目录含 3 个历史会话，**When** 左栏渲染，**Then** 清单含全部 3 项 + 当前项高亮。
2. **Given** 用户在左栏选中另一会话，**When** 确认，**Then** 恢复会话流程与 /resume 选择器等价（同一数据路径）。

### User Story 5 - 右侧信息面板 (Priority: P2)

用户在可开合的右侧面板获取会话上下文：本回合变更文件汇总（从事件流真实聚合，点开可看 diff 预览）+ 既有模态面板（agents/doctor/cache）收编为面板页签。数据全部来自会话镜像与事件流的既有真实数据——没有数据源的面板（如交互式终端）不进面板。

**Why this priority**: 面板是 dsh 布局的差异点，但价值依赖真实数据；变更文件汇总对 coding agent 场景价值最高，先行。

**Independent Test**: 单测——面板页签渲染/聚合口径断言（变更文件聚合与 diff 卡数据同源）；真机冒烟含一次 diff 预览。

**Acceptance Scenarios**:

1. **Given** 回合内发生过 2 次写文件，**When** 打开变更面板，**Then** 两个文件各一条目，diff 预览与对应工具卡数据同源。
2. **Given** agents 面板键位，**When** 按下，**Then** agents 内容呈现在右侧面板页签（非模态卡），信息不丢。

### User Story 6 - 组件形态与符号系统 (Priority: P2)

用户看到的界面件换用 dsh 组件语言的终端形态：工具行/工具卡三态标记（进行中/成功/失败）、用户消息气泡感、提示符与编辑器形态、通知行、回合回执、模式徽章。glyph 词汇（当前 ◆ ❯ ▸ ⏹ ⊘ ·）按 dsh 组件语言重审，形成单一符号表——同一语义全界面同一符号。

**Why this priority**: 形态与色彩共同构成观感；与布局并行推进、逐组件可独立验收。

**Independent Test**: 单测按组件断言渲染形态（glyph/结构/着色）；真机冒烟对照 dsh 界面观感评审。

**Acceptance Scenarios**:

1. **Given** 含读/写/失败工具调用的回合，**When** 渲染工具卡，**Then** 三态标记与标题/详情层级一致可辨（单测钉点）。
2. **Given** 启动帧、输入态、审批态、面板态，**When** 渲染，**Then** glyph 全表一致，无废弃符号残留。

### User Story 7 - 主题机制与退化路径延续 (Priority: P3)

用户 `/theme` 换肤照常可用；预设清单重推导自 dsh 色板（DeepSeek 蓝为新默认）；256 色回退仍逐槽手选、NO_COLOR/TERM=dumb 仍全无色（装饰属性保留）。diff 卡首次消费预留槽，加/删配色取 dsh diff 令牌（暗板 added 底 rgb(31,49,36)/标记 rgb(65,201,119)，deleted 底 rgb(60,31,27)/标记 rgb(250,66,62)）。

**Why this priority**: 主题机制是 023 FR-014 的资产，重设计在其上换值而非推翻。

**Independent Test**: 单测——预设表与 dsh 派生值同源断言；三层色彩退化 SGR 集合断言沿用 023 T19 口径。

**Acceptance Scenarios**:

1. **Given** `/theme` 无参，**When** 执行，**Then** 清单列出重推导后的预设（含新默认），切换即时生效。
2. **Given** NO_COLOR=1，**When** 渲染全部事件矩阵，**Then** SGR 输出为空集。
3. **Given** diff 工具卡，**When** 渲染，**Then** 加/删行着色取 dsh diff 令牌，预留槽消费有钉点。

### User Story 8 - 真机冒烟证据 (Priority: P3)

用户完成一次真实会话（含工具调用、审批、diff、取消、面板开合、/theme 切换），证据落 `docs/evidence/`：引擎版本、工作区、时间线、退出码、SGR 摘录，明暗两模式各一。

**Why this priority**: FR-108 惯例；依赖前七个故事完成。

**Independent Test**: docs/evidence 冒烟文档存在且含清单各项。

**Acceptance Scenarios**:

1. **Given** 全部任务完成，**When** 真机冒烟执行，**Then** 证据文档落盘，暗/亮/NO_COLOR 三形态各有摘录。

## Edge Cases

- 窄端（<80 列）降级单列后，任何面板不得撑破布局；面板打开态在窄端自动转模态（或禁用并提示）。
- 宽端三区并存时主列宽度不足 80 列（如 100 列终端）→ 依优先级收窄左栏/面板区，主列阅读线优先。
- 无色层下新 glyph、布局分区、面板结构全部保留（仅去色），不因无色丢信息。
- 中文正文 + 粗体层级的混排不得破坏 CJK 宽度计量（wrapcache 契约延伸到面板）。
- 左栏会话清单过长 → 视口内滚动，不挤压主列行数契约。
- 面板数据聚合空态（无变更文件/无 agents）给出指引文案，不渲染空白框。
- 引擎就绪中/异常徽章、审批独占态在新配色与布局下仍一眼可辨（语义色不因品牌化弱化；审批独占态优先级高于任何面板态）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: TUI MUST 以 dsh 设计令牌（design-platform.css）为色彩唯一来源：暗板基底 rgb(21,21,23)、表面层 875/850/800（rgb(35,35,36)/rgb(44,44,46)/rgb(53,54,56)）、边框白 alpha 6%–20% 映射为终端可用的层级手段；亮板以 dsh light 别名板为源。设计阶段产出「令牌 → 终端 SGR 槽」映射表并留痕。
- **FR-002**: TUI MUST 以 DeepSeek 蓝族（dark 板 rgb(122,170,255)/rgb(86,134,254)，light 板 rgb(65,118,230)）为默认强调色；出厂默认主题呈现 dsh 身份（现默认 graphite 陶土色退位）。
- **FR-003**: 状态语义色 MUST 取 dsh 状态令牌：成功 rgb(34,197,94)/rgb(78,209,126)，告警 rgb(245,158,11)/rgb(247,173,49)，错误 rgb(242,90,90)（dark）；正文层级取 dsh label 层级（主/次/三级/说明）。
- **FR-004**: diff 卡 MUST 消费预留槽并采用 dsh diff 令牌（US7 场景 3 数值）；工具卡类别色（读/写/proc 等）由设计阶段按 dsh 工具行语言映射到预留槽。
- **FR-005**: TUI MUST 采用 dsh 式多栏布局：左栏会话列表 + 中央会话主列 + 可开合信息面板区；宽度响应式三档（宽/中/窄），窄端降级为与现状等价的单列；主列阅读线（≥80 列或可用宽度优先）受保护。
- **FR-006**: 左栏 MUST 展示会话清单（与 resume 选择器同一数据源与谓词），支持当前会话高亮与选中恢复；空态有指引文案。
- **FR-007**: 右侧信息面板 MUST 支持页签形态：变更文件汇总（事件流真实聚合，与 diff 卡数据同源）+ 既有 agents/doctor/cache 面板收编；无真实数据源的特性不进面板。
- **FR-008**: 面板与布局的导航键位 MUST 新增且不与既有键位冲突；审批独占态与治理回路的优先级、语义、键位不变；面板打开态不得截获审批键。
- **FR-009**: 组件 chrome（横幅/助手头行/工具卡/委派卡/授权卡/回执/通知/状态栏/模式徽章/编辑器/选择器）MUST 按 dsh 组件语言重新审定形态与 glyph 词汇，形成单一符号表（同一语义全界面同一符号）；符号表在设计文档留痕。
- **FR-010**: 交互语义 MUST 零回归：既有键位、治理回路（审批裁决/fail-closed/独占态）、流式、思考与粘贴折叠、补全、/命令、事件渲染矩阵行为断言全部不变；本变更触及呈现层与布局容器，不触及治理与镜像管线。
- **FR-011**: 主题机制 MUST 保留：/theme 换肤、256 层逐槽手选回退（不做距离降采样）、无色层行为不变；预设清单重推导自 dsh 色板，Reasonix 预设去留在设计阶段裁定入台账。
- **FR-012**: 事件渲染覆盖矩阵 MUST 更新至重设计后形态（新增 diff 卡、面板、布局档位行），行为语义行不删减。
- **FR-013**: 界面文案 MUST 保持中文（023 FR-015 资产）；dsh 的英文文案不移植，仅视觉与结构语言移植。

### Key Entities

- **主题（槽位 × 预设 × 色彩层）**: 023 三层机制不变；槽位值来源换为 dsh 派生，槽位按需扩充（diff 底/标记、工具类别色、气泡底、面板层级）。
- **布局框架**: 宽/中/窄三档的分区构成与降级规则（新增）。
- **面板页签**: 变更文件 / agents / doctor / cache（收编既有模态面板；数据源约束见 FR-007）。
- **符号表**: glyph 与组件形态的单一权威清单（新增，设计文档承载）。
- **渲染块**: transcript 块接口的视觉参数换装；块拓扑（024 A4）不变。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 全量门禁绿：`go build ./...`、`go test ./... -count=1`、`go vet ./...`、`gofmt -l` 空、`internal/lint` 分层绿。
- **SC-002**: 治理与交互零回归：既有键位/治理/独占态测试断言语义不变全绿；事件渲染矩阵行为行全绿。
- **SC-003**: 视觉钉点完备：暗/亮两模式下每个渲染点的槽位色值有单测断言且与 dsh 派生表同源；无色层 SGR 空集断言保持。
- **SC-004**: 布局档位可验证：宽/中/窄三档 frame 分区与窄端等价性有单测；面板开合不破坏阅读锚点。
- **SC-005**: 真机冒烟证据落 `docs/evidence/`（暗/亮/NO_COLOR 三形态 + 审批/diff/取消/面板场景），符合 FR-108 惯例。

## Assumptions

- 参考对象为 dsh Web UI 设计系统（dsh 无终端 TUI，事实认定见背景）；「按 dsh TUI 重新设计」即「按 dsh 的 UI 设计语言与布局结构重设计本项目 TUI」。
- 用户裁定（2026-10-17）：目标态为全面移植 dsh 布局（左栏 + 主列 + 面板）。本变更 spec 一次覆盖完整目标态；交付按 User Story 优先级分波推进，每波全量绿、可独立冒烟。
- 交互式终端面板不入面板（mulix 引擎无 PTY 数据源，不造数——D7 纪律延伸）；若后续引擎具备该能力另行变更。
- 暗色为主形态，亮色为调校变体（沿用现有机制）；终端无法控制字体，dsh 的 Montserrat 品牌字体不移植，以字重与符号层级表达。
- 默认强调色换为 DeepSeek 蓝属用户可预期结果；/theme 可随时切回其他预设。
- stdout Sink（非 TUI 模式）不在本变更范围；主题与布局消费面限于 internal/tui。

## Out of Scope

- 交互式终端（PTY）面板、文档/浏览器预览面板（无引擎数据源）。
- 主题持久化到用户 config（023 D8 裁定维持）。
- TUI 以外的呈现面（stdout Sink、日志格式、Web/HTTP 入口）。
- 引擎侧（engine/治理/镜像）任何改动。

## Clarifications

### Session 2026-10-17

- Q: 「按 dsh 重新设计本项目 TUI」的范围口径——仅视觉换装、换装 + 会话侧栏，还是全面移植 dsh 布局（多栏 + 面板）？ → A: **全面移植 dsh 布局**（用户明示选择；布局级结构一并移植，不止换装）。
