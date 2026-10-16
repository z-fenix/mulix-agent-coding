# Feature Specification: TUI 视觉完全对齐 DeepSeek-Reasonix（025）

**Change**: `2026-10-16-tui-reasonix-visual-parity`

**Created**: 2026-10-16

**Status**: Draft

**Input**: User description: "TUI 完全还原 DeepSeek-Reasonix，源码查看 D:\workplace\std\DeepSeek-Reasonix，视觉效果运行 reasonix 对比"

> 基线证据（本目录）：`baseline-reasonix-frames.log`（reasonix 实拍，ConPTY 100×30，问候回合全帧）、`baseline-mulix-frames.log`（mulix-cli 同交互实拍）。两者同驱动器、同序列、同尺寸——差异即本变更需求清单。参考源码：`D:\workplace\std\DeepSeek-Reasonix`（MIT）。

## 023/024 已对齐部分（本变更不重复）

结构层已达标：alt-screen 全接管、语义块+增量换行、流式段落、思考折叠、工具卡/diff 卡、多行编辑器+召回+粘贴折叠、补全+/resume 选择器、授权卡、双行状态栏骨架、主题系统（暗/亮×4 强调色）、滚动条右列、`❯` 编辑器提示符。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 交谈 chrome 全对齐（视觉主体） (Priority: P1)

同一句「你好」的完整回合，mulix 渲染与 reasonix 帧逐元素同构：

1. 启动横幅：`◆ mulix · <model>` + 提示行（对照 reasonix `◆ reasonix · deepseek-flash` + `对话上下文将跨轮保留。输入 'exit' 或按 Ctrl-D 退出。`）。
2. 用户气泡：`  › 你好`（2 空格缩进 + `›` 前缀）。
3. 助手头：`◆ mulix` 头行，正文整体 2 空格槽（含流式段落、工具卡、回执——reasonix 全部正文在槽内）。
4. 思考：折叠行 `▸ 思考了 N 秒 (Ctrl+O)`（对照 `▸ 思考了 0 秒 (Ctrl+O)`；替换现行 `✓ 已思考 0s`），流式期标记行 `▎ thinking…`。
5. 回执：上缘分隔线 `─────`（通栏），回执行 `本轮  N tok · in X · cached Y · new Z · out W · reasoning R · ≈$0.0153`（字段名/次序/分隔对齐 reasonix；数值取 mulix UsageEvent 真实字段映射：in=UncachedInputTokens、cached=ReadTokens、new=WriteTokens、out=OutputTokens、reasoning=ThinkingTokens；无「命中」字样——命中率只在状态栏）。
6. 收口：正常完成不再渲染 `✓ 回合完成 tN` 行（回执即收口，对照 reasonix）；`⏹ 回合已取消` 与 `✗ 回合失败 tN（subtype）` 保留（reasonix 对应错误/取消通知位）。

**Why this priority**: 这是「完全还原」的可见主体；六元素全部落在既有块渲染器与 status.go，不动接缝。

**Independent Test**: 同款 ConPTY 拍帧脚本跑 mulix，帧内逐元素断言（表驱动文本断言 + 人工比对帧）。

**Acceptance Scenarios**:

1. **Given** 空会话启动，**Then** 横幅两行形态与 reasonix 同构（◆ 行 + 提示行）。
2. **Given** 一句问候回合，**Then** 帧序列包含 `› 你好`、`◆ mulix`、2 空格槽正文、`▸ 思考了 N 秒 (Ctrl+O)`、分隔线+回执行，且无 `✓ 回合完成` 行。

### User Story 2 - 状态栏版式对齐 (Priority: P1)

双行状态栏版式对照 reasonix：

- 行 1 左：模式徽章（彩色 pill：normal/plan 等着色块，对照 reasonix `Ask ·` 位）；行 1 右：`模型 <model>   强度 <effort>` 形态（mulix 无 effort 字段则只 `模型 <model>`，不造数）。
- 行 2：`缓存 本次命中 X% · 平均 Y%  上下文 N tok (pct)  费用 $S` 形态；mulix 无余额/低峰/压缩阈值数据 → 省略（不造数纪律），字段次序与分隔风格对齐。
- 会话 id8 从行 1 右侧移除（reasonix 不在状态栏显示会话 id）。

**Independent Test**: statusView 表驱动断言（行 1/行 2 字段与次序）。

**Acceptance Scenarios**:

1. **Given** 空闲态，**Then** 行 1 含彩色模式徽章与 `模型 <model>`；行 2 以 `缓存 本次命中` 开头、含上下文仪表。
2. **Given** 会话 id，**Then** 不出现在状态栏。

### User Story 3 - 默认主题观感调至 reasonix 档 (Priority: P2)

默认主题（暗色 graphite）的槽位取值对照 reasonix 实际观感调优：强调色用于 ◆ 头行/glyph/徽章，dim 用于状态与辅助，红仅错误。以实拍帧人工比对为准（ANSI 色不进文本帧——以 raw.bin SGR 与屏幕观感双重核对）。

**Independent Test**: 主题槽断言（各元素用哪槽）+ 真机帧人工比对记录。

**Acceptance Scenarios**:

1. **Given** 默认暗色主题，**Then** ◆ 头行/glyph/模式徽章为强调色、状态行为 dim、错误为红——与 reasonix 观感一致。

### Edge Cases

- 宽度 50 以下窄端：槽内正文换行不得破坏 2 空格槽对齐（槽宽计入 render 宽度预算）。
- 回执字段为零值（cached=0 等）：仍按字段次序渲染 `cached 0`（reasonix 同风格显示 0，不省略）。
- 多回合：横幅只在会话启动渲染一次；/new 换绑后不重复横幅（reasonix 无对应场景——按 mulix 现语义保留换绑留痕行）。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: 启动横幅 MUST 渲染 `◆ mulix · <model>` + 提示行，仅会话启动一次。
- **FR-002**: 用户输入 MUST 渲染为 `  › <text>` 气泡（2 空格缩进 + › 前缀）。
- **FR-003**: 助手头行 `◆ mulix` MUST 出现在每个助手回合正文前，正文（段落/工具卡/回执）整体 2 空格槽。
- **FR-004**: 思考折叠行 MUST 为 `▸ 思考了 N 秒 (Ctrl+O)`；流式期 MUST 有 `▎ thinking…` 标记行。
- **FR-005**: 回执 MUST 为「上缘分隔线 + `本轮 N tok · in/cached/new/out/reasoning · ≈$S`」形态；正常完成 MUST NOT 渲染 `✓ 回合完成 tN` 行（⏹/✗ 保留）。
- **FR-006**: 状态栏行 1 MUST 有彩色模式徽章 + `模型 <model>`（右）；行 2 MUST 为 `缓存 本次命中 X% · 平均 Y%  上下文 N tok (pct)  费用 $S` 版式；会话 id8 MUST NOT 出现在状态栏。
- **FR-007**: 默认主题槽位观感 MUST 对照 reasonix 实拍调优（◆/glyph/徽章=accent；状态/辅助=dim；错误=red）。
- **FR-008**: 治理语义零改动（授权卡键位/独占/120s fail-closed/镜像先行/可慢不可丢——023 红线全数继承）；事件渲染覆盖矩阵同步更新（TurnDone 行为变化、新增横幅/槽断言）。

### Key Entities

无新实体；用户气泡/助手头/思考标记为既有块渲染的形态变更；回执块重构字段映射；模式徽章为 statusState 渲染形态。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 同款 ConPTY 驱动器拍帧对比：mulix 问候回合帧与 reasonix 基线帧逐元素同构（本 spec 六元素清单逐项断言），证据落 docs/evidence/。
- **SC-002**: 覆盖矩阵与治理测试全绿语义不变；新形态有表驱动断言；全量 27 包绿 + vet + gofmt（跟踪文件）+ 分层 lint。

## Assumptions

- 币种符号保持 mulix 的 $（reasonix 的 ¥ 是其 currency 配置）；无余额/低峰/压缩阈值数据 → 省略不造数（023 D7 纪律）。
- reasonix 的 `● ask` 选择卡形态不在本变更（mulix 的 ask=授权卡，023 已对齐键位；卡片视觉差异若用户后续提出另立变更）。

## Out of Scope

- 引擎侧改动；ask/授权卡视觉重排；reasonix 独有数据字段（余额/低峰）；多语言。

## Clarifications

<!-- clarify 阶段填充 -->
