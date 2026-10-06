# Mulix（mulix-agent-coding）设计规格

- 日期：2026-10-05（2026-10-06 迁移为 mulix change `001-p1-core-loop-and-cache-foundation` 的 design 文档）
- 状态：已批准 v1（原「草案 v1（待用户审查）」，2026-10-05 经评审批准；见同日 P1 实现计划头部「已批准」标注）
- 迁移出处：原 `docs/superpowers/specs/2026-10-05-mulix-agent-coding-design.md`（docs/superpowers 已随迁移删除，全文可在 git 历史 c4f88be 追溯）。除本元数据块外，正文与原文逐字一致。
- 与 spec.md 的分工：需求（WHAT/WHY，原 §1.1/§1.2/§1.3）已提取至 `docs/specs/001-p1-core-loop-and-cache-foundation/spec.md`，以其为需求权威；本文承载工程契约（HOW：架构、协议、缓存治理、权限、会话、配置与测试策略）。两者冲突时：用户需求以 spec.md 为准，工程细节以本文为准并回改 spec。
- 范围：本文覆盖项目 v1 整体契约；change `001-p1-core-loop-and-cache-foundation` 的 build 阶段执行其 P1 范围（§11 路线图）。
- 路径：基于 Claude Agent SDK 生态的 Go 实现 coding agent
- 工作代号：**Mulix**；仓库目录 `D:\workplace\project\mulix-agent-coding`；Go module 名 `mulix`；二进制名 `mulix`

> 本规格是工程契约：需要改变行为时，先更新本文件，再修改代码。英文不另设规范性版本（本项目面向中文团队），代码标识符、配置键、协议名保持英文原样。

---

## 1. 背景与目标

### 1.1 背景

Mulix 是一个全新的开源风格 coding agent，**引擎能力取自 Claude Code CLI（Claude Agent SDK 生态的同一引擎），宿主与产品层用 Go 实现**：Go 直接拉起 claude CLI 子进程，以 stream-json 与控制协议驱动它（与官方 Python SDK 的架构方式一致）；宿主自身不引入 Python/Node 运行时依赖（引擎 claude CLI 自身的 Node 依赖是用户部署前提，见 §14 风险对策）。

项目确立两条不可妥协的产品原则，均借鉴 DeepSeek-Reasonix 的工程实践：

1. **Prompt cache 命中率是产品行为，不是性能副产品。** 高命中率意味着更低的成本、更低的延迟和更稳定的响应质量；它必须像功能一样被构造、被度量、被守护、被展示。
2. **托管边界清晰。** 引擎管「回合内」（消息序列、上下文压缩、内置工具、模型采样），Go 管「回合间」（会话权威镜像、缓存治理、权限治理、多入口编排）。两侧职责不得互相越界。

### 1.2 目标与成功标准

| 目标 | 成功标准（可验证） |
| --- | --- |
| 可用的 CLI/TUI coding agent | `mulix`（交互式 TUI）与 `mulix run "<task>"`（单次模式）能完成真实编码任务：读改文件、跑测试、提交 git |
| 缓存命中率作为一等指标 | TUI 状态栏实时显示命中率与成本；`mulix cache` 输出会话级汇总；逐回合 JSONL 记账可审计 |
| 缓存构造有护栏 | 同工作区两次启动，我方发出的配置（system 追加段 + 工具面 + 权限模式）逐字节一致，有专项测试钉死 |
| 缓存回归有门禁 | PR 修改缓存敏感路径时 CI 拒绝缺少 `Cache-impact:` / `Cache-guard:` 元数据的提交 |
| 引擎升级可控 | `engine.lock` pin CLI 版本；升级必须跑缓存命中率基准对比并记录结论 |
| 安全默认 | 无授权者时写操作 fail-closed；权限规则 deny 永远优先；崩溃后可恢复不丢会话 |
| 架构可长青 | 分层规则由 repolint 类工具在 CI 机器强制；前端新增入口不需要复制编排逻辑 |

### 1.3 非目标（v1 明确不做）

- 不自研模型采样与上下文压缩（引擎域，见 §4 托管边界）。
- 不支持非 Anthropic 模型（引擎即 Claude；多模型是远期议题，本规格不设计）。
- 不做桌面端与移动端（P4 之后再议）。
- 不承诺兼容未 pin 版本的 CLI 协议细节。

## 2. 已确认的关键决策

| # | 决策 | 结论 | 来源 |
| --- | --- | --- | --- |
| D1 | 项目定位 | 全新独立项目，借鉴 Reasonix 理念，不受其 Go 技术栈之外的约束 | 用户确认 |
| D2 | 首发形态 | CLI/TUI 优先；后续扩展 serve / ACP / desktop | 用户确认 |
| D3 | 语言与接入 | 项目主体 Go 实现；采用 Python SDK 的方式——**Go 直编 claude CLI**（subprocess + stream-json 控制协议）；自定义工具以 Go 实现的 MCP server 暴露 | 用户确认 |
| D4 | 能力范围 | 全量对齐 Reasonix 级特性集，分阶段实现（§11 路线图）；本规格细节聚焦 P1 | 用户确认 |
| D5 | 架构方案 | 方案 C「托管边界混合」：引擎管回合内，Go 管回合间 | 用户确认 |
| D6 | 项目目录 | `D:\workplace\project\mulix-agent-coding`（module `mulix`，二进制 `mulix`） | 用户确认 |

## 3. 系统上下文与运行形态

```
用户 ──► mulix TUI / mulix run ──► Go 宿主（control 编排）
                                        │  spawn + stream-json + 控制协议
                                        ▼
                                  claude CLI 子进程（引擎，版本经 engine.lock pin）
                                        │  Anthropic API（模型采样、KV cache 在此发生）
                                        ▼
                                  本地文件系统（被操作的代码库）
```

- 引擎来源：npm 包 `@anthropic-ai/claude-code` 提供的 `claude` CLI；凭据支持 `ANTHROPIC_API_KEY` 或用户已有的 Claude 登录态，由 `mulix doctor` 检测。
- 一个 Go 宿主进程对应一个引擎子进程/会话；多会话（TUI 多 tab）为 P4 能力，P1 单会话单进程。
- P1 支持 Windows / macOS / Linux（Go 交叉编译）；引擎侧 Node 依赖由 doctor 引导安装。

## 4. 托管边界（方案 C 的合同）

| 归引擎管（回合内，Go 不重建、不重排） | 归 Go 管（回合间，Go 拥有权威） |
| --- | --- |
| 消息序列的组装、发送与 compaction（上下文压缩） | 会话权威镜像：所有事件的 JSONL 追加日志（审计/回放/检索的事实来源） |
| 模型采样、流式输出、工具循环的推进 | 权限策略与审批 UI（canUseTool 回调的裁决方） |
| 内置工具的执行（Read/Write/Edit/Bash/Glob/Grep/…） | 缓存治理三层（§8）：构造约束、指标、门禁 |
| 文件 checkpointing（rewind 的执行） | 多入口编排（Controller 抽象 transport） |
| KV cache 的实际命中（Anthropic 侧） | 子代理调度、记忆服务、指标与成本核算 |
| CLAUDE.md 等指令文件的读取时机 | 项目指令生命周期（启动快照、变更检测提醒） |

**边界推论**：
- Go 不重组消息序列、不重发历史——那会破坏引擎的缓存策略并与之打架。
- Go 发出的「会话级可变状态」一律走 user message 尾部（turn tail），永不进 system prefix。
- 引擎不可控的缓存行为（其内部注入的指令、microcompact 等）不属于回归对象，以版本 pin + 指标监控覆盖。

## 5. 架构总览与分层

### 5.1 分层图

```
┌──────────────────── 入口层 frontends ────────────────────┐
│  internal/cli (TUI + 子命令)     internal/serve (P4)     │
└────────────────────────────┬─────────────────────────────┘
                             │ 仅 frontends / cmd 可 import（repolint 强制）
                  internal/control   ← Controller：唯一的 turn 驱动器
                             │
   ┌──────────┬─────────────┼──────────────┬──────────────┐
   │ engine   │ sessionx    │ cache        │ guard        │ registry
   │ CLI 宿主 │ 权威镜像    │ 缓存治理三层 │ 权限/钩子    │ MCP 工具框架
   └────┬─────┴─────────────┴──────┬───────┴──────────────┘
        │ stream-json + 控制协议    │ 官方 MCP go-sdk
   claude CLI 子进程          in-process Go MCP servers
                             │
                  utility leaves（禁止导入任何内核包）
        config, event, textutil, jsonl, i18n, …
```

### 5.2 包职责（一包一职）

| 包 | 职责 | 关键类型 |
| --- | --- | --- |
| `cmd/mulix` | 入口：blank import 注册内置 MCP 工具，交给 cli | `main` |
| `internal/cli` | TUI（bubbletea v2 + lipgloss）、子命令路由（run/doctor/cache/sessions/permissions） | `ChatUI`、`RunCmd` |
| `internal/serve` | HTTP/SSE 前端（P4，本规格只预留分层位置） | — |
| `internal/control` | Controller：接收前端命令（Submit/Cancel/Approve/NewSession/Resume…），驱动 engine，向单一 `event.Sink` 发类型化事件；turn 生命周期/取消/审批的唯一所有者 | `Controller`、`Options`、`Sink` |
| `internal/engine` | CLI 宿主层：子进程生命周期、stream-json 编解码、控制协议（canUseTool / hook / interrupt）、`Host` 接口抽象、`engine.lock` 加载与校验 | `Host`、`ClaudeHost`、`FakeHost`、`Event`、`ControlRequest` |
| `internal/sessionx` | 会话权威镜像：JSONL 追加日志、内存投影、崩溃恢复回放、session id ↔ 引擎 id 映射 | `Store`、`Ledger`、`Projection` |
| `internal/cache` | 缓存治理：`Boot`（配置组装与字节稳定）、`Meter`（usage 记账与命中率）、`Guard`（阈值告警、TTL 感知） | `BootComposer`、`Meter`、`Report` |
| `internal/guard` | 权限与钩子：纯函数 Policy（deny>ask>allow>fallback）、审批队列（超时 fail-closed）、钩子分发 | `Policy`、`Gate`、`Hooks` |
| `internal/registry` | in-process MCP server 框架：Go 工具以官方 go-sdk 暴露；schema 注册期冻结；顺序稳定 | `Server`、`Tool`、`Register` |
| `internal/config` | mulix.toml 分层加载（flag > project > user > defaults）、last-known-good 恢复、user-global 安全项锁定 | `Config`、`Load` |
| leaves | `event`（类型化事件）、`textutil`、`jsonl`、`i18n` 等 | — |

### 5.3 分层规则（repolint 机器强制，CI 阻断）

1. 只有 `internal/cli`、`internal/serve`（及 `cmd/`）可以 import `internal/control`。
2. `control` 不得 import 任何 frontends。
3. leaves 清单内的包禁止 import 任何 `internal/*` 内核包。
4. 内置工具子包经 `init()` 自注册到 `registry`；父包不反向 import 子包。
5. 文件职责棘轮：编辑不得增加既有文件的记录债务（照搬 Reasonix repolint 思路；P1 以 review 约定执行，P2 落地为 CI 工具，见 §11）。
6. `--append-system-prompt` 的生产者唯一（见 §8.1 护栏测试 `TestNoSecondSystemPromptPath`）。

## 6. 核心数据流：一次回合的生命周期

```
用户输入（TUI）
 → Controller.Submit（会话内回合互斥：已有活跃回合时返回 ErrTurnRunning，
    由前端提示等待；多回合排队调度属 P4 多入口议题）
 → engine.Host.Send(user message)                       [stream-json]
 → 引擎循环（引擎域）：
     assistant 事件（text/thinking delta）─┬─► sessionx 镜像（先持久化后发布）
                                           └─► 前端 Sink（流式渲染）
     tool_use 事件 ──► 控制协议 canUseTool 回调
                    ──► guard.Policy 裁决（deny>ask>allow）
                    ──► Ask → TUI 审批卡（单次/本会话/拒绝；超时 fail-closed）
                    ──► Allow → 引擎执行内置工具 → tool_result 回流
     usage 事件 ──► cache.Meter 记账（§8.2）
     钩子点（SessionStart/PreToolUse/PostToolUse/Stop）──► guard.Hooks 回调
 → result 事件（含最终 usage/成本）→ TurnDone
```

- **取消**：Controller.Cancel → 控制协议 interrupt；引擎已发起的副作用按引擎语义处理（镜像如实记录）。
- **事件双路分发**：镜像写入是转发给前端的前置条件（先持久化后发布），保证崩溃后可恢复、可回放、可审计。
- **记忆召回**（P2）：每回合前 Go 侧 BM25 检索，命中以低权限文本块追加在 user message 尾部；不改 system prefix、不改工具面。

## 7. 引擎接入层（internal/engine）设计

### 7.1 Host 接口（Go 侧唯一依赖面）

```go
type Host interface {
    Start(ctx context.Context, cfg BootConfig) error          // spawn + 初始化握手
    Send(ctx context.Context, msg UserMessage) error          // 提交一回合输入
    Events() <-chan Event                                     // 引擎事件流（含 usage/tool_use/result）
    RespondControl(ctx context.Context, id string, payload ControlResponse) error // 审批/钩子应答
    Interrupt(ctx context.Context) error                      // 取消当前回合
    Close(ctx context.Context) error                          // 有界关闭（超时杀进程树）
    Info() EngineInfo                                         // 版本、session id、协议指纹
}
```

`ClaudeHost` 是真实实现；`FakeHost` 是脚本化实现（按 fixture 序列回放事件），e2e 用「假引擎 × 真实 control/cache/sessionx 栈」断言系统行为——组件正确不等于系统有效，边界才算数。

### 7.2 引擎拉起参数（P1 固定集）

- 输入输出：`--input-format stream-json --output-format stream-json`；流式增量按需开启 `--include-partial-messages`。
- 系统提示：`--append-system-prompt "<BootComposer 产物>"`（只追加，不替换引擎默认提示——保持引擎能力）。
- 工具注入：`--mcp-config <generated.json>`，内容为 Go 侧 in-process/stdio MCP 服务器清单（顺序稳定）；`--allowedTools`/`--disallowedTools` 由 BootComposer 依配置生成，注册期冻结。
- 权限模式：`--permission-mode`（default/acceptEdits/plan）+ canUseTool 控制协议回调；**绝不使用** `--dangerously-skip-permissions`。
- 会话：`--resume <engine-session-id>`（续链）、`--fork-session`（分叉）；P1 不使用 `--max-turns`（由 Go 侧预算控制，P3 完备）。
- 其它：`--model`、`--setting-sources`（限定加载来源，防止外部 settings 漂移缓存与行为）。

> 协议事实核查（P1 首任务 T0，0.5 天 spike）：以上 flag 与 stream-json/控制协议字段（init/assistant/user/result 事件、control_request 的 can_use_tool/hook_callback/interrupt）以**锁定版本的 CLI 实测 + 官方 TS/Python SDK 开源实现**核对为准，结论固化为 fixture 与 `docs/specs/engine-protocol.md`。若与本文冲突，以实测为准并修订本节。

### 7.3 engine.lock（版本 pin）

```jsonc
// engine.lock —— 提交进仓库；升级必须显式变更并附基准结论
{
  "cli_version": "2.x.y",            // claude --version 的精确输出
  "protocol_probe": { "init_version_field": "…" },  // 启动探针记录（T0 实测回填）
  "pinned_at": "2026-10-05",
  "baseline": { "cache_hit_rate": 0.0, "note": "P3 基准建立后回填" }
}
```

- 启动/doctor 时校验实际 CLI 版本与 lock 一致，不一致则告警（默认拒绝启动，`--allow-engine-drift` 显式放行并记录）。
- 协议兼容测试：fixture 回放（录制的事件流）随 CI 运行；CLI 升级 = 变更 lock + 重录 fixture + 基准对比。

### 7.4 进程与生命周期

- spawn 时注入干净环境（凭据经环境变量，不落日志）；stderr 脱敏后环形缓冲保留（诊断用）。
- 关闭：先优雅（引擎自然退出）→ 超时杀进程树；崩溃（非预期退出）→ 状态记 `recovery_required`，Go 侧由 sessionx 回放 + `--resume` 续链（§10）。

## 8. 缓存治理体系（产品行为的落地，本项目灵魂）

### 8.1 构造层（Boot：启动时冻结，会话内字节不变）

| # | 约束 | 实现点 |
| --- | --- | --- |
| C1 | `--append-system-prompt` 追加段为静态模板 + 启动参数快照；会话内零变更 | `cache.BootComposer.Compose()`；模板变更只发生在新会话 |
| C2 | 工具面冻结：MCP 服务器清单与顺序固定；schema 注册期一次 canonicalize；运行期不增减工具 | `registry`（注册期冻结）+ `--mcp-config` 顺序稳定序列化 |
| C3 | 项目指令快照：CLAUDE.md/AGENTS.md 在会话启动时读取并快照；会话中文件变更只提醒（notice）不重载 | `control` 启动序列 + watcher（P2） |
| C4 | 恢复链策略：同任务续用 resume；新会话必须显式（用户动作或 `/new`）；恢复链选择事件入镜像 | `control` + `sessionx` |
| C5 | 动态内容只走 turn tail：记忆召回、运行时状态块一律追加在 user message 尾部 | `control` 注入点唯一化（repolint 静态规则强制，见 §5.3 第 6 条） |

**护栏测试（构造层契约，CI 阻断）**：
- `TestBootComposeByteStable`：同配置两次 `Compose()`，追加段 + mcp-config JSON + allowedTools 序列逐字节一致。
- `TestBootComposeOrderInsensitive`：输入参数遍历顺序不影响输出字节（map 序列化必须走稳定序列化器）。
- `TestNoSecondSystemPromptPath`：静态检查（repolint 规则）确保 `--append-system-prompt` 只有 BootComposer 一个生产者。

### 8.2 监控层（Meter：每个 API 回合记账）

引擎每个 API 回合的 usage（`input_tokens`、`cache_creation_input_tokens`、`cache_read_input_tokens`、`output_tokens`）进入 Meter：

```
uncached(r) = input_tokens
write(r)    = cache_creation_input_tokens
read(r)     = cache_read_input_tokens
hit(r)      = read / (uncached + write + read)            // 分母>0 时
session_hit = Σread / Σ(uncached + write + read)         // 会话累计
cost(r)     = uncached·P_in + write·1.25·P_in + read·0.1·P_in + output·P_out   // 定价可配置
saving(r)   = read · (P_in − 0.1·P_in)                   // 相对全未命中的节省估算
```

产品面：
- TUI 状态栏常驻：`模型 | 会话命中率 | 本回合 tokens | 成本`。
- `mulix cache`：会话列表 + 汇总（累计命中率、tokens、成本、节省估算）。
- 逐回合 JSONL 记账（`~/.mulix/usage/<session>.jsonl`）：审计与基准数据源。
- **TTL 感知**：会话空闲 ≥ 4 分钟后的首个回合标注 `expected_cold=true`（Anthropic 默认 5 分钟缓存 TTL，预留 1 分钟余量），该回合不参与命中率回归告警；状态栏显示「冷启动（预期）」。

### 8.3 治理层（Guard + CI）

1. **命中率地板告警**：滑动窗口（最近 10 个有效回合，排除 expected_cold）命中率 < `cache.hit_rate_floor`（默认 0.70）→ notice 事件 + TUI 提示 + 记账标记；不中断会话。
2. **CI 门禁（照搬 Reasonix 机制）**：PR diff 触及缓存敏感路径（`internal/cache/`、`internal/engine/`、`internal/control/`、`internal/registry/`、`docs/` 的 prompt 模板）时，PR body 必须含：
   - `Cache-impact: <none|low|medium|high> - <原因>`
   - `Cache-guard: <本次新增/依赖的护栏测试或命令>`
   脚本 `scripts/check-cache-impact.sh` 本地与 CI 同源。
3. **engine.lock 治理**：CLI 升级 = 修改 lock + 重录协议 fixture + 运行缓存基准（P3 起）并附对比结论；基准数据源即 §8.2 的 JSONL 记账。
4. **升级演练记录**：`docs/evidence/` 存放每次引擎升级前后的命中率/成本对比，作为升级准入证据。

## 9. 权限与钩子（internal/guard）

- **Policy**：纯函数，`Decide(toolName, input) → Allow|Ask|Deny`；规则 `deny > ask > allow > fallback`；规则形态 v1 为 `Tool` 与 `Tool(specifier)`（如 `Bash(go test:*)`、`Edit(docs/**)`），specifier 语义对齐内置工具参数约定。
- **预设**：`default`（写操作 Ask）、`acceptEdits`（文件编辑 Allow，Bash 仍 Ask）、`plan`（只读面）、`danger-full`（全 Allow，显式 deny 仍优先）。映射引擎 `--permission-mode` + Go 侧规则叠加。
- **审批**：Ask 进入审批队列（TUI 卡片：本次允许 / 本会话允许当前范围 / 拒绝）；无人值守 `mulix run` 无审批者 → fail-closed 拒绝；队列超时（默认 120s）fail-closed。
- **会话授权**：本会话允许绑定「工具 + 规范化范围」（如同一命令前缀/同一目录），仅存内存与镜像，不写项目配置。
- **钩子**：SessionStart / PreToolUse / PostToolUse / Stop 以 Go 回调接入（P1 内置消费者：审计日志、Meter、越界提醒）；P2 开放用户自定义钩子（见 §11）。

## 10. 会话持久化与恢复（internal/sessionx）

- **权威镜像**：`~/.mulix/sessions/<id>/events.jsonl` 追加日志（唯一事实来源）+ 索引 sidecar（`meta.json`：引擎 session id、模型、时间、工作区；`cache.json`：Meter 汇总投影）。
- **先持久化后发布**：事件先落镜像再广播前端；镜像损坏尾部移 `.damaged` 并告警，不阻塞会话。
- **恢复**：崩溃/重启后，`mulix --resume` 从镜像列表选择 → 校验 engine.lock → `--resume <engine-id>` 续链；镜像与引擎侧不一致时以引擎为准、镜像告警（引擎是上下文权威，镜像只承诺审计完整性）。
- **checkpoint**：P1 直接采用引擎文件 checkpointing（rewind 能力透传到 TUI `/rewind`）；Go 侧 pre-image 快照扩展为 P5 议题。
- **隐私**：镜像默认本地明文（用户机器）；`doctor` 提供脱敏导出（`--redact`：token/密钥/邮箱 regex 化）。

## 11. 分阶段路线图（全量对齐的落地顺序）

| 阶段 | 内容 | 出口标准 |
| --- | --- | --- |
| **P1 核心闭环 + 缓存底座**（本规格细节范围） | T0 协议探针；engine 宿主（含 FakeHost/fixture）；control；TUI（会话/run/审批/状态栏）；guard 权限与钩子；sessionx 镜像与恢复；cache 三层（Boot/Meter/Guard）；config；doctor；engine.lock；CI 门禁脚本 | §1.2 全部成功标准达成 |
| **P2 生态** | 自定义工具框架完善（多 server、命名空间、超时）、子代理编排（agents + 委派 + 结果回收）、skills、记忆服务（BM25 召回 + memory_* MCP 工具）、CLAUDE.md 管理命令、分层检查工具落地（repolint 思路：分层/棘轮/第二 system prompt 路径）、用户自定义钩子开放 | 子代理任务在真实仓库完成多步编码任务；记忆召回可开关且不破坏缓存护栏；分层违规 CI 阻断 |
| **P3 治理完备** | 真实 CLI e2e 基准（命中率/token/成本/时延，含 expected_cold 分离）、预算控制（token/成本上限）、门禁完善、多工作区支持 | 基准有噪声底说明与重复次数约定；预算超限 fail-closed 可恢复 |
| **P4 多入口** | serve（HTTP/SSE + 会话租约交接）、ACP（编辑器协议）、桌面壳选型；多回合排队调度 | 同一 Controller 多入口行为一致（共享 e2e 断言） |
| **P5 高级** | Go 侧 pre-image checkpoint 扩展、fleet 并行（小依赖图 + write_paths 互斥）、worktree 隔离 | 并行写入互斥有测试证明；rewind 两阶段可撤销 |

## 12. 配置体系（internal/config）

- 文件：项目 `mulix.toml`；用户 `~/.mulix/config.toml`；密钥 `~/.mulix/.env`（仅 `ANTHROPIC_API_KEY` 等凭据，配置文件只写键名）。
- 优先级：`flag > 项目 mulix.toml > 用户 config.toml > 内置默认`。
- **user-global 锁定**（项目配置不可覆盖，防恶意 clone 提权）：引擎版本策略（`[engine]`）、凭据来源、遥测开关、缓存告警的上报行为（阈值数值可项目覆盖）、`danger-full` 预设准入。
- 配置损坏：last-known-good 内存恢复 + `loadWarnings`，绝不改写原文件。
- 骨架（P1 全量字段）：

```toml
default_model = "claude-sonnet-4-5"        # 示例别名；实际以 mulix doctor 探测的引擎可用模型为准

[cache]
hit_rate_floor = 0.70        # 告警阈值（可项目覆盖）；告警是否上报为 user-global 锁定项
cold_after_idle = "4m"       # TTL 感知阈值

[permissions]
preset = "default"           # default|acceptEdits|plan|danger-full(user-global 准入)
deny  = ["Bash(rm -rf*)", "Bash(git push*)"]
allow = ["Bash(go test:*)"]

[engine]
allow_drift = false          # user-global；true 时版本漂移仅告警不拒绝

[mcp]                        # 外部 MCP server（P2）；内置工具走 registry
# [[mcp.servers]]
# name = "browser"
# command = ["npx", "-y", "@browser/mcp"]

[tools]                      # 内置 Go 工具开关（注册期冻结，会话内不可变）
memory_search = false        # P2
```

## 13. 测试策略

| 层 | 手段 |
| --- | --- |
| 单元 | 各包 `go test`；Policy/BootComposer/Meter 为纯函数重点覆盖 |
| 协议兼容 | fixture 回放（真实 CLI 录制）× FakeHost；升级 = 重录 |
| 系统边界 | 假引擎 × 真实 control/cache/sessionx 栈的 e2e：回合流程、审批、恢复、命中率记账、TTL 标注 |
| 构造护栏 | §8.1 三个专项测试 + repolint 静态规则 |
| 真实引擎 | P3 起的 nightly 基准（真实 CLI + 真实任务集），分离 expected_cold |
| CI 门禁 | golangci-lint + repolint + cache-impact 脚本 + 构造护栏测试 |

## 14. 风险与对策

| 风险 | 对策 |
| --- | --- |
| stream-json/控制协议非官方稳定契约，CLI 升级可能破坏 | `engine.lock` pin + fixture 兼容测试 + `engine.Host` 隔离层 + `--allow-engine-drift` 显式放行流程 |
| 引擎内部行为（指令注入、microcompact、CLAUDE.md 读取时机）影响命中率，且不可控 | 划入「引擎域」（§4）：不作为回归对象；版本 pin 保证可重现；基准对比捕捉升级影响 |
| 用户环境缺 Node/claude CLI | `mulix doctor` 自检（node/claude/凭据/版本一致性）+ 安装引导文档；Go 二进制本身零依赖不变 |
| TUI 流式渲染与审批并发复杂度 | 事件单一 `event.Sink` 通道 + TUI 状态机（渲染态/审批态互斥）；审批队列有界 |
| Windows shell 语义差异 | 内置 Bash/PowerShell 语义由引擎工具面承担（引擎域）；Go 侧不重复实现 shell 解析 |

## 15. 术语表

- **引擎域 / Go 域**：§4 托管边界两侧。
- **回合（turn）**：一次用户输入触发的完整「采样→工具循环→终答」过程；一个回合含 1..N 个 API 调用。
- **API 回合**：引擎发起的一次 Anthropic API 请求/响应，是 usage 与命中率的基本计量单位。
- **turn tail**：追加在 user message 尾部的会话级可变内容；system prefix 的对立面。
- **expected_cold**：因缓存 TTL 过期而预期的低命中率回合，不参与回归告警。
- **镜像**：sessionx 对引擎事件的追加式持久化副本；审计与恢复的事实来源，不是上下文权威。
