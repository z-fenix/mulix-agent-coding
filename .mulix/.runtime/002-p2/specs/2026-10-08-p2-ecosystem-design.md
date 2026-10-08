# P2 生态设计——子代理编排、记忆服务、外部 MCP、钩子与分层工具

**Change**: 002-p2
**Date**: 2026-10-08
**Track**: architectural（设计阶段记录）
**输入**: docs/specs/002-p2/spec.md（FR-101..113，SC-001..006）+ design 001 §11 路线图 P2 行 + 用户三处分叉点裁决（本文 §2）

---

## 0. 设计原则（继承 001 的裁决）

- **D5 托管边界不变**：引擎管回合内，Go 管回合间。子代理调度、记忆索引、外部 server 声明都在 Go 侧；外部 server 进程本身由引擎托管（透传）。
- **P1 红线全量继承**：事件先持久化后发布、镜像 1:1 不变量、注册期冻结、缓存构造护栏（C1–C5）、权限治理唯一入口（Gate）、动态内容只走 turn tail（C5）。
- **C5 注入点唯一化**：记忆召回、子代理状态块一律追加在回合 user message 尾部——由 repolint 静态规则强制（§6）。

## 1. 组件与分层（新增/扩展）

| 组件 | 层 | 职责 | 新增/扩展 |
|---|---|---|---|
| `internal/subagent` | control 之下 | 子代理编排器：委派生命周期（spawn/poll/cancel/收口）、委派链（DAG，深度≤3、并发≤4）、与 control 的事件对接 | 新增 |
| `internal/memory` | utility 层之上、cache 之旁 | BM25 索引 + 条目存储（JSONL）+ 检索/注入预算 | 新增 |
| `internal/tools/mulixsubagent` | tools 子包 | `mulix_spawn` / `mulix_poll` / `mulix_cancel` 三个 MCP 工具（init 自注册；HTTP 回调桥模式与 mulixpermission 相同——前台编排器持有编排状态） | 新增 |
| `internal/tools/mulixmemory` | tools 子包 | `memory_save` / `memory_list` / `memory_delete`（registry 注册；检索注入不走工具，走 control 的回合组装） | 新增 |
| `internal/control` | — | 回合组装扩展：记忆召回注入（turn tail）；子代理事件镜像；CLAUDE.md watcher（轮询哈希） | 扩展 |
| `internal/cache`（Compose） | — | `[mcp.servers]` 外部条目透传进 mcp-config（注册期冻结语义不变；外部条目 env 不做运行期注入——终评 Important-1 教训） | 扩展 |
| `internal/config` | — | `[mcp.servers]` / `[hooks.custom]` / `[tools] memory_search` / 子代理上限 / 召回预算 解析与 user-global 锁定项 | 扩展 |
| `internal/guard`（Hooks） | — | 用户自定义钩子消费者：exec 脚本 + stdin 事件 JSON + 超时/崩溃隔离；P1 内置消费者不变 | 扩展 |
| `internal/lint` + `mulix lint` 子命令 | cli | 分层检查工具：分层导入规则、文件职责棘轮、turn-tail 注入点唯一——行级扫描（P1 repolint 测试先例），零新依赖 | 新增 |
| TUI `/agents` 面板 | cli | 运行中/已完成子代理列表、委派链、取消动作 | 扩展 |

分层约束：`internal/subagent` 可 import control/engine/sessionx/cache/guard/memory；tools 子包不反向 import 内核包（沿 P1 规则，回调桥经 registry HTTP 模式解耦）。

## 2. 用户裁决记录（2026-10-08，ask_user_question）

1. **子代理编排 = MCP 工具驱动**：主代理经 `mulix_spawn`/`mulix_poll`/`mulix_cancel` 自主委派；TUI `/agents` 面板只读查看 + 取消。
2. **外部 MCP server = 配置透传**：mulix 只声明、引擎原生托管；mulix 不代理外部 server 协议。
3. **记忆库 = 项目级**：工作区 `.mulix/memory/`，随仓库可选入库。

## 3. 子代理编排（internal/subagent）

### 3.1 生命周期与状态机

```
spawn ──→ running ──→ completed
             │  └──→ failed（引擎错误/收口异常）
             ├─────→ cancelled（用户或主代理取消）
             └─────→ timeout（可选整体上限，默认无；FR-103 只限深度/并发）
```

- `Spawn(parentSession, task, opts)`：为子代理创建独立 sessionx Store（`.mulix/sessions/` 同池，meta 记 `parent_id` 与 `delegation_task`）、构造子 Controller（复用 `buildComponents` 的组装序：store → meter → policy/gate → controller，attended 语义 = 继承父会话的审批桥）、`NewSession` 后 Submit(task)。
- 子代理引擎进程与主代理引擎进程**相互独立**（各自 claude 子进程、各自 mcp-config 副本——BootComposer 面一致，Fingerprint 一致性由字节稳定护栏保证）。
- **结果回收**：子代理回合收口（TurnDone）后，`mulix_poll` 返回最终 assistant 文本 + 退出原因 + usage（复用 `cache.ReadUsageReport` 权威口径）；主会话镜像记一条 `DelegationCompleted` 事件（含子会话 id、任务摘要、usage 摘要）。

### 3.2 委派链与限额

- 委派链 = 有向无环：每个子会话 meta 记 `parent_id`；spawn 时沿链回溯判环，深度 >3 → `mulix_spawn` 返回错误应答（引擎侧收错误，不悬挂）；并发 >4 → 排队拒绝（立即错误，明确文案）。
- 取消：`mulix_cancel` 或 TUI `/agents` → 子 Controller.Cancel()（P1 语义：CancelAll + Interrupt）→ 镜像无孤儿回合（resume_reconciled 同款收口保障）→ usage 已发生的部分照常入账。
- 主会话关闭时：所有运行中子代理级联取消（Close 序：先子后父，防悬挂进程）。

### 3.3 权限治理与审批路由

- 子代理的 Gate 构造继承父会话的 Approver（attended：同一 TUI 审批桥，审批卡标注子代理来源；无人值守：同样 fail-closed）。
- 子代理的 `--permission-prompt-tool` 回调地址独立（每会话一个监听器，P1 permissionCallbackServer 已支持多实例）。
- 审计：审批事件落**子会话镜像**（谁裁决的不变语义），主镜像保留委派链摘要。

### 3.4 事件与镜像

新增事件类型（event 包，全部走先持久化后发布红线）：
- `DelegationStarted`（父镜像：子会话 id、任务、深度）
- `DelegationCompleted` / `DelegationCancelled` / `DelegationFailed`（父镜像：结果摘要、usage、退出原因）
- 子会话内部事件与 P1 完全同构（TurnStarted/AssistantText/…），天然可审计。

## 4. 记忆服务（internal/memory）

- **存储**：`<workspace>/.mulix/memory/entries.jsonl`——追加式，每行 `{id, text, source_session, created_at, tags?}`；删除 = 追加 tombstone（与 sessionx 损坏隔离同思路，重放时收敛）。损坏尾部隔离复用 P1 jsonl 能力。
- **索引**：进程内 BM25（中文按 rune biggram + 英文按词——mulix 用户面中英混合；Go 标准库实现，零新依赖），会话启动全量构建（万级条目毫秒级）；条目变更增量更新。
- **召回注入**：control 回合组装时（Submit 路径、C5 turn tail 唯一注入点）检索 top-k（默认 k=3），预算 2000 字符内按相关性截断，格式化为低权限文本块追加在 user message 尾部。两次启动的 Boot 面不变（注入在回合级，不在系统级——护栏测试钉死）。
- **工具面**：`memory_save`（显式记忆）、`memory_list`、`memory_delete`；引擎侧的隐式召回不走工具（走 control 注入），避免工具面膨胀与缓存抖动。
- **开关**：`[tools] memory_search`（默认 false）。关闭时 control 不做检索、工具不注册（进程启动期决定，注册期冻结语义保持）。
- **user-global 锁定**：无（记忆是项目数据，不涉安全项）。

## 5. 外部 MCP server（配置透传）

- `[mcp.servers]` 数组：`name`（命名空间）、`command`（argv）、`env`（可选，静态）、`timeout_ms`（可选覆盖，默认 60000）。
- cache.Compose 把外部条目与内置 mulix 条目**合并**生成 mcp-config（名称冲突 → Compose 报错拒绝；内置名 `mulix` 保留）。顺序稳定（内置先、外部按声明序），两次启动逐字节一致——SC-003 护栏测试扩展覆盖外部条目。
- 超时：透传为引擎 env `MCP_TOOL_TIMEOUT`（claude 原生读取；mulix 不代理协议）。崩溃隔离：引擎原生（server 进程死 → 工具错误应答，回合不悬挂）。
- 治理：引擎对外部工具调用同样经 `--permission-prompt-tool` 回调（P1 冒烟实证），权限/审计自动覆盖；外部工具 usage 由引擎计入回合 usage（四类 token 口径不变）。
- 启动期校验：name 保留名冲突、command 空、env 值非字符串 → 配置错误拒绝启动（fail-closed）。

## 6. 分层检查工具（internal/lint + `mulix lint`）

- 三个规则集（行级扫描，零新依赖——P1 `TestNoSecondSystemPromptPath` 的 regex 先例规模化）：
  1. **分层导入**：每包声明允许的 import 前缀白名单（内置于工具，与 §5.3 一致）；utility leaves 引用内核包、tools 子包反向 import 内核包等违规 → 报告 `file:line` 与规则名。
  2. **文件职责棘轮**：`docs/lint/ratchet.json`（初始生成）记录每文件允许的"记录债务"计数；编辑使计数增加 → 违规。首轮以当前树生成基线（棘轮只紧不松）。
  3. **注入点唯一**：`--append-system-prompt` 引用 allowlist（P1 已有）+ 新增「user message 组装只经 control 的 turn-tail 函数」的引用扫描。
- CI：`.github/workflows/ci.yml` 增加 lint job 步骤（`go run ./cmd/mulix lint layers`）；本地同源可跑。
- 出口：分层违规 CI 阻断（design §11 P2 出口标准 3）。

## 7. 用户自定义钩子（guard.Hooks 扩展）

- `[hooks.custom]`：`[[hooks.custom.rules]] event = "PreToolUse" command = ["python", "scripts/hook.py"] timeout_ms = 5000`。
- 启动期校验：event 必须是 P1 四时点之一、command 非空、timeout 上限 30s——非法拒绝启动。
- 执行：go exec，事件 JSON 经 stdin 传入；stdout JSON 响应按 P1 钩子语义解析（与内置 hook_response 同构）；超时/崩溃 → 按该时点既定 fail-open/fail-closed 语义降级 + NoticeEvent 留痕。
- P1 内置消费者（审计/Meter/越界提醒）保持不变，用户钩子是追加消费者（一条事件分发给全部消费者）。

## 8. CLAUDE.md/AGENTS.md watcher（FR-008 P2 半部）

- control 启动序列读快照（P1 已有）→ 启动 watcher goroutine：2s 轮询，对快照源文件做 sha256 比较。
- 变更 → `NoticeEvent{level=info, content="指令文件变更: <file>（快照保持，下会话生效）"}` + 镜像留痕；不重载、不中断。
- `mulix instructions` 子命令：输出当前快照来源与摘要。
- 无文件/读取失败 → watcher 静默跳过（P1 快照语义：无文件即无快照）。

## 9. 错误处理与降级汇总

| 场景 | 行为 |
|---|---|
| spawn 深度超限/并发超限 | 工具错误应答（明确文案），引擎侧不悬挂 |
| 子代理引擎崩溃 | 子会话 engine_closed 收口（P1 语义）→ DelegationFailed 回收 |
| 记忆文件损坏尾部 | 隔离 + 告警（复用 jsonl 能力），好数据不丢 |
| 外部 server 畸形输出 | 工具错误应答（引擎原生），回合继续 |
| 自定义钩子超时/崩溃 | 既定语义降级 + NoticeEvent，主流程继续 |
| 指令文件删除 | 同修改：提醒事件，快照保持 |

## 10. 测试策略

| 层 | 手段 |
|---|---|
| 单元 | subagent 状态机/限额判环、memory BM25/预算截断、config 解析、lint 规则矩阵——FakeHost/纯函数重点 |
| 系统边界 | e2e：spawn→委派任务→poll 回收（FakeHost 子引擎）；治理路由（子代理审批到父桥）；取消收口；记忆开关 A/B；外部条目字节稳定；钩子超时降级 |
| 构造护栏 | 外部条目入 mcp-config 的字节稳定扩展测试；turn-tail 注入点唯一（repolint 自食其力） |
| 真实引擎 | SC-001 冒烟：真实仓库委派一次多步任务（P1 冒烟 A/B 同法）；记忆召回开关 A/B 冒烟 |
| CI | 新 lint job 步骤 + 现有门禁全保留 |

## 11. 里程碑切分（tasks 阶段的输入，非任务清单）

- M12 子代理编排（subagent 包 + 3 工具 + 事件 + e2e + /agents 面板）
- M13 记忆服务（memory 包 + 3 工具 + 注入 + 开关 + 护栏）
- M14 外部 MCP 透传（config/Compose 扩展 + 校验 + 字节稳定护栏）
- M15 指令 watcher + `mulix instructions`（FR-109）
- M16 自定义钩子（Hooks 扩展 + 校验 + 降级矩阵）
- M17 分层检查工具（lint 包 + 子命令 + CI 步骤 + 棘轮基线）
- M18 P2 验收（SC-001..006 映射 + 真实引擎冒烟 + P1 复审 N2/N4/N5 随手修）

## 12. 决议记录（Rulings）

- R1: 外部 server 的工具超时依赖引擎 env（MCP_TOOL_TIMEOUT）——mulix 不代理协议（用户裁决「配置透传」的推论）；FR-108 验收口径按此校准（测试钉 env 透传与默认值，不测 mulix 侧行为）。
- R2: 记忆隐式召回不走 MCP 工具（走 control 注入）——避免工具面膨胀与每回合工具面字节抖动。
- R3: 子代理会话与主会话共用 sessionx 池（同目录、meta 区分父子）——恢复/审计统一入口；不新建存储域。
- R4: lint 规则先行 regex 行级扫描——go/packages 类型图重（依赖+构建缓存成本），收益不匹配 P2；棘轮机制保证后续可升级。
- R5: P1 复审 N2/N4/N5 随 M18 一起修（不单独立 change）。
