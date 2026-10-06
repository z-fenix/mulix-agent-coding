# Feature Specification: Mulix P1 — 核心闭环 + 缓存底座

**Change**: `001-p1-core-loop-and-cache-foundation`

**Created**: 2026-10-06

**Status**: Draft

**Input**: User description: "将已批准的 mulix 设计规格与 P1 实现计划（原 docs/superpowers，2026-10-05）迁移为 mulix change；用户确认：单个 change、tasks 忠实迁移、迁移后删除原文档、跳过 clarify。"

> 需求来源：已批准的《Mulix（mulix-agent-coding）设计规格》v1 与《Mulix P1 实现计划》（2026-10-05，见 git 历史）。本文提取其中属于本 change（P1 范围）的 WHAT/WHY；工程契约细节（架构、协议、包结构）由 design 文档承载。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 无人值守完成单次编码任务 (Priority: P1)

用户在任意代码仓库运行 `mulix run "<任务描述>"`，mulix 拉起引擎完成一个完整回合：阅读代码、修改文件、运行测试、提交 git；全程无需人工值守。当引擎请求需要授权的写操作而无人审批时，该请求按 fail-closed 拒绝并如实记录。

**Why this priority**: 最小价值闭环——不依赖 TUI 也能完成真实编码任务，同时验证引擎接入与安全底座；它是其余一切能力的交付形态底线。

**Independent Test**: 在示例仓库执行 `mulix run "把 README 首行标题改为 X 并提交"`，任务完成后仓库出现预期提交；同环境以无人值守方式触发写审批，得到拒绝记录。

**Acceptance Scenarios**:

1. **Given** 一个干净示例仓库与可用引擎凭据，**When** `mulix run "<编辑+测试类任务>"`，**Then** 回合正常结束、工作区出现预期文件修改与 git 提交、本次会话事件完整落盘可查。
2. **Given** 无人值守且权限预设为 default，**When** 引擎请求写操作审批，**Then** 该请求被自动拒绝，拒绝事件被持久化记录。

---

### User Story 2 - 交互式 TUI 会话 (Priority: P1)

用户运行 `mulix` 进入交互界面：流式查看回复与思考增量、在审批卡上做出「本次允许 / 本会话允许当前范围 / 拒绝」决策、在状态栏看到「模型 | 会话命中率 | 本回合 tokens | 成本」，并可使用 `/new`、`/resume`、`/cache`、`/doctor`、`/rewind` 等会话命令。

**Why this priority**: 日常主入口；审批与可观测性在此直接服务用户。

**Independent Test**: 启动 TUI 提交一次输入，观察流式输出与状态栏更新；触发一次写审批并对三种决策各验证一次；中断进程后用 `mulix --resume` 续链。

**Acceptance Scenarios**:

1. **Given** TUI 会话，**When** 提交一条任务输入，**Then** 回复与工具活动流式呈现，状态栏的命中率/tokens/成本随每个 API 回合更新。
2. **Given** 引擎请求需审批的写操作，**When** 审批卡弹出，**Then** 用户可选三种决策之一，选择被记录；超时未选按拒绝处理。
3. **Given** 上次会话因进程退出中断，**When** 用户 `mulix --resume` 选择该会话，**Then** 会话历史可回放并从引擎侧续链，不丢已确认事件。

---

### User Story 3 - 缓存命中率可见、可审计 (Priority: P1)

用户随时能看到缓存命中带来的成本与延迟收益：TUI 状态栏常驻命中率与成本；`mulix cache` 输出会话列表与汇总（累计命中率、tokens、成本、节省估算）；每个 API 回合的 usage 逐行记账为可审计的 JSONL 文件；会话空闲 ≥4 分钟后的首个回合标注为「冷启动（预期）」，不参与命中率回归告警；最近 10 个有效回合命中率低于阈值时产生提醒（不中断会话）。

**Why this priority**: 产品原则第一条——缓存命中率是产品行为，必须像功能一样被度量、被展示、被审计。

**Independent Test**: 完成一个多回合会话后运行 `mulix cache`，其汇总数字与该会话逐回合 JSONL 记账合计一致；构造空闲 ≥4 分钟的回合验证 expected_cold 标注与告警豁免。

**Acceptance Scenarios**:

1. **Given** 一个含多次 API 回合的会话，**When** 运行 `mulix cache`，**Then** 显示该会话的累计命中率/tokens/成本/节省估算，与逐回合记账一致。
2. **Given** 会话空闲 ≥4 分钟后的首个回合，**When** 该回合产生 usage，**Then** 被标注 expected_cold、状态栏显示冷启动（预期），且不触发命中率告警。
3. **Given** 最近 10 个有效回合（排除 expected_cold）命中率低于阈值，**When** 新回合完成，**Then** 出现提醒事件与 TUI 提示，会话继续不中断。

---

### User Story 4 - 安全默认与权限治理 (Priority: P1)

权限裁决始终遵循 deny > ask > allow > fallback；提供 default / acceptEdits / plan / danger-full 预设；规则支持 `Tool` 与 `Tool(specifier)` 两类形态；需审批操作进入审批队列（无人值守或超时 120 秒均 fail-closed）；会话级授权仅存活于当前会话；SessionStart / PreToolUse / PostToolUse / Stop 四个钩子时点可被内部消费者使用（审计日志、计量记账、越界提醒）。

**Why this priority**: 无安全默认的 coding agent 不可交付；fail-closed 与 deny 优先是不可妥协的产品原则。

**Independent Test**: 以规则矩阵验证裁决优先级与 specifier 匹配边界；端到端验证无人值守拒绝、超时拒绝与会话级授权命中。

**Acceptance Scenarios**:

1. **Given** deny 与 allow 规则同时命中某操作，**When** 裁决，**Then** deny 生效。
2. **Given** 无人值守会话遇到需审批操作，**When** 无审批者，**Then** 该操作被拒绝且事件落盘。
3. **Given** 审批队列等待超过 120 秒，**When** 无人应答，**Then** 按拒绝处理。
4. **Given** 用户对同一命令前缀选择「本会话允许当前范围」，**When** 会话内再次出现同范围请求，**Then** 不再询问；新会话不继承该授权。

---

### User Story 5 - 会话不丢：镜像与崩溃恢复 (Priority: P1)

每个会话的全部事件先持久化到本地镜像（追加式 JSONL，含会话元数据与缓存汇总旁车文件），再呈现给用户；镜像尾部损坏时自动隔离损坏段并告警，不丢已完整事件；崩溃/重启后 `mulix --resume` 可从镜像列表选择会话、校验引擎版本一致性后从引擎侧续链。

**Why this priority**: 长任务的可信底座；镜像是审计、回放与恢复的唯一事实来源，「先持久化后发布」不可颠倒。

**Independent Test**: 写入若干事件后模拟进程中断，重新打开能读回全部完整事件；注入损坏尾行后好数据不丢且得到损坏标记。

**Acceptance Scenarios**:

1. **Given** 一个进行中的会话，**When** 宿主进程意外退出，**Then** 重启后镜像可完整回放已确认事件，并引导用户续链。
2. **Given** 镜像尾部存在损坏行，**When** 读取镜像，**Then** 完整部分照常可用，损坏尾部被移出原文件并告警。

---

### User Story 6 - 缓存构造护栏与回归门禁 (Priority: P2)

同工作区两次启动，mulix 发出的启动配置（追加系统提示段、工具清单、权限模式）逐字节一致，由专项护栏测试钉死；PR 修改缓存敏感路径时，CI 拒绝缺少 `Cache-impact:` / `Cache-guard:` 元数据的提交（本地与 CI 使用同一脚本）；引擎 CLI 版本以 lock 文件钉住，漂移默认拒绝启动、显式放行需留痕。

**Why this priority**: 防止缓存命中率无声退化的保护性设施；不阻塞 MVP 主路径，但在本 change 出口标准内。

**Independent Test**: 两次组合启动配置逐字节比较一致；对含/不含元数据的模拟 PR 运行门禁脚本，分别得到通过/拒绝。

**Acceptance Scenarios**:

1. **Given** 同一配置两次启动组合，**When** 比较输出，**Then** 追加系统提示段、工具清单 JSON、allowed/disallowed 工具序列逐字节一致，且输入参数遍历顺序不影响输出。
2. **Given** PR diff 触及缓存敏感路径且缺少元数据，**When** 运行门禁检查，**Then** 检查失败并给出补充指引；元数据齐全则通过。

---

### Edge Cases

- 引擎子进程在中途崩溃或被杀：状态标记为需要恢复，镜像保持完整，下次启动引导 resume，不自动重发历史消息。
- 引擎 CLI 版本与 lock 不一致：默认拒绝启动并给出提示；`--allow-engine-drift` 显式放行且留痕于会话记录。
- 环境缺 Node / 缺 claude CLI / 缺凭据：`mulix doctor` 给出结构化诊断与安装引导，不产生半启动会话。
- 配置文件损坏：以最近可用配置恢复并给出加载警告，绝不改写原文件。
- 镜像与引擎侧会话不一致：以引擎为上下文权威，镜像保留告警（镜像只承诺审计完整性）。
- 用户在引擎执行副作用期间取消回合：按引擎语义处理已发生的副作用，镜像如实记录。
- 项目指令文件在会话中变更：仅提醒，不重载，不破坏已冻结的启动配置。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: mulix MUST 提供命令面：`mulix`（默认进入 TUI）、`mulix run "<task>"`（无人值守单次模式）、`mulix doctor`、`mulix cache`、`mulix sessions`，以及全局 `--resume` 与 `--allow-engine-drift`。
- **FR-002**: mulix MUST 以子进程方式驱动 claude CLI 引擎（stream-json 输入输出与控制协议）；回合内的消息序列组装、上下文压缩、模型采样与内置工具执行归引擎负责，mulix 不重建、不重排。
- **FR-003**: mulix MUST 在启动时校验引擎 CLI 版本与 engine.lock 的一致性：不一致默认拒绝启动；显式放行必须留痕。
- **FR-004**: 权限裁决 MUST 为纯函数策略，优先级 deny > ask > allow > fallback；支持 default / acceptEdits / plan / danger-full 预设与 `Tool`、`Tool(specifier)` 两类规则形态；绝不提供绕过权限的运行方式。
- **FR-005**: 需审批操作 MUST 进入审批队列：TUI 提供「本次允许 / 本会话允许当前范围 / 拒绝」；队列超时（120 秒）与无人值守场景一律 fail-closed 拒绝；会话级授权仅存于当前会话，不写入项目配置。
- **FR-006**: mulix MUST 维护会话权威镜像：全部引擎事件先持久化（追加式，含会话元数据与缓存汇总旁车文件）再发布给前端；镜像尾部损坏时隔离损坏段并告警，不丢好数据。
- **FR-007**: mulix MUST 支持崩溃恢复：从镜像列表选择历史会话、校验引擎版本一致性、以引擎侧会话标识续链；镜像与引擎不一致时以引擎为上下文权威并保留镜像告警。
- **FR-008**: 启动配置 MUST 满足缓存构造约束：追加系统提示段会话内零变更；工具清单与顺序在注册期冻结、运行期不增减；项目指令文件启动时快照、会话中变更仅提醒不重载；会话级可变内容一律追加在用户消息尾部，禁止进入系统前缀。
- **FR-009**: mulix MUST 为每个 API 回合记录 usage（未缓存输入、缓存写入、缓存读取、输出四类 token 计数），据此计算命中率、成本与相对全未命中的节省估算，并逐回合落盘为可审计的 JSONL 记账文件。
- **FR-010**: mulix MUST 提供 `mulix cache`：会话列表与汇总（累计命中率、tokens、成本、节省估算），数字与逐回合记账一致。
- **FR-011**: mulix MUST 实现 TTL 感知：会话空闲 ≥4 分钟后的首个回合标注 expected_cold，状态栏显示冷启动（预期），且不参与命中率回归告警。
- **FR-012**: mulix MUST 实现命中率地板告警：最近 10 个有效回合（排除 expected_cold）命中率低于阈值（默认 0.70，可项目覆盖）时产生提醒事件并在 TUI 呈现；不中断会话。
- **FR-013**: CI 门禁脚本 MUST 在 PR diff 触及缓存敏感路径时要求 PR 描述包含 `Cache-impact:` 与 `Cache-guard:` 元数据，缺失即拒绝；本地与 CI 使用同一脚本。
- **FR-014**: mulix MUST 提供 `mulix doctor`：依次检查 Node、claude CLI、engine.lock 一致性、凭据、工作区可写，输出结构化报告；支持脱敏导出（对 token/密钥/邮箱等做正则遮蔽）。
- **FR-015**: mulix MUST 内置进程内 MCP 工具框架：工具在注册期冻结（名称、schema、顺序），以稳定顺序生成引擎工具清单；P1 内置首个工具 `mulix_usage`（返回当前会话缓存计量汇总）。
- **FR-016**: 配置 MUST 按「命令行 flag > 项目 mulix.toml > 用户级 config.toml > 内置默认」分层生效；用户级锁定项（引擎版本策略、凭据来源、遥测开关、告警上报行为、danger-full 准入）不可被项目配置覆盖；配置损坏时以最近可用配置恢复并给出加载警告，绝不改写原文件。
- **FR-017**: 钩子分发 MUST 覆盖 SessionStart / PreToolUse / PostToolUse / Stop 四个时点；P1 内置消费者为审计日志、计量记账与越界提醒；单个消费者异常不得影响回合。
- **FR-018**: 引擎接入实现前 MUST 完成协议探针，产出三件套：引擎协议说明文档（可让不了解该 SDK 的人实现解码器）、可回放的事件流 fixture（至少一次「输入→审批→结果」完整会话与一次含 usage 的多回合会话，脱敏）、engine.lock 初值。

### Key Entities *(include if feature involves data)*

- **Session（会话）**: 一次用户与引擎的连续协作；含本地唯一会话标识、引擎侧会话标识（双向映射）、模型、工作区、时间；其权威镜像是事件追加日志。
- **Turn（回合）**: 一次用户输入触发的完整「采样→工具循环→终答」过程；含 1..N 个 API 回合；会话内回合互斥。
- **UsageRecord（计量记录）**: 一次 API 回合的四类 token 计数与派生值（命中率、成本、节省估算、expected_cold 标注）。
- **PermissionRule / Decision（权限规则与裁决）**: 规则形态 `Tool` / `Tool(specifier)` 与裁决结果 Allow / Ask / Deny。
- **EngineLock（引擎版本锁）**: 引擎 CLI 精确版本、启动探针记录、pin 时间与基准占位（基准数据后续阶段回填）。
- **Config（配置）**: 分层合并后的生效配置；含用户级锁定项与可覆盖的告警阈值。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 在真实代码仓库中，`mulix`（TUI）与 `mulix run "<task>"` 均能完成一次真实编码任务：读改文件、跑测试、提交 git（原规格 §1.2 目标 1）。
- **SC-002**: TUI 状态栏实时显示命中率与成本；`mulix cache` 输出会话级汇总；逐回合 JSONL 记账可审计且与汇总一致（§1.2 目标 2）。
- **SC-003**: 同工作区两次启动，mulix 发出的配置（追加系统提示段 + 工具面 + 权限模式）逐字节一致，并有专项测试钉死（§1.2 目标 3）。
- **SC-004**: PR 修改缓存敏感路径时，CI 拒绝缺少 `Cache-impact:` / `Cache-guard:` 元数据的提交（§1.2 目标 4）。
- **SC-005**: engine.lock 钉住 CLI 版本；版本漂移默认拒绝启动、显式放行留痕（命中率基准对比属后续阶段，见 Out of Scope）（§1.2 目标 5 的 P1 部分）。
- **SC-006**: 无授权者时写操作 fail-closed；权限规则 deny 永远优先；崩溃后可恢复且不丢已确认会话事件（§1.2 目标 6）。
- **SC-007**: 分层规则以 review 约定执行并入验收清单；CI 工具化（repolint 类）属后续阶段（§1.2 目标 7 的 P1 部分）。

## Assumptions

- 用户环境自备 Node 运行时与 claude CLI（doctor 提供检测与安装引导）；凭据经环境变量或用户已有登录态提供。
- P1 为单会话单进程；多会话（TUI 多 tab）与多入口属后续阶段。
- 引擎即 Claude（Anthropic API）；多模型支持不在本 change 范围。
- 会话镜像默认以本地明文存储于用户机器；doctor 提供脱敏导出。
- 缓存 TTL 以引擎服务端默认 5 分钟为基准，预留 1 分钟余量（即 4 分钟阈值）。
- P1 支持 Windows / macOS / Linux 三平台（Go 交叉编译）；本 change 的开发与验收环境为 Windows。
- 「命中率地板告警不中断会话」按行业常规选择为提醒而非阻断。

## Out of Scope

- 自研模型采样与上下文压缩；Go 侧重组或重发历史消息。
- 非 Anthropic 模型支持。
- 桌面端与移动端。
- 兼容未 pin 版本的 CLI 协议细节。
- serve（HTTP/SSE）、ACP、桌面壳与多回合排队调度。
- 子代理编排、skills、记忆服务、用户自定义钩子、分层检查的 CI 工具化（repolint）。
- 真实 CLI e2e 基准与预算控制（token/成本上限）。
- Go 侧 pre-image 快照、fleet 并行与 worktree 隔离。
- 引擎侧 `--max-turns` 预算使用（Go 侧预算控制在后续阶段完备）。
- 引擎升级演练的命中率基准对比数据（本 change 只交付 lock 与护栏，不建立基准）。

## Clarifications

### Session 2026-10-06

- Q: 澄清（clarify）阶段是否可以跳过？ → A: 跳过。本 change 的需求全部提取自已批准的设计规格（原 `docs/superpowers/specs/2026-10-05-mulix-agent-coding-design.md`，其 P1 实现计划头部标注「已批准」），迁移时未发现遗留的 NEEDS CLARIFICATION 项；用户于 2026-10-06 明确同意跳过（已知悉跳过会提高下游返工风险）。原文档删除后可在 git 历史中追溯。
