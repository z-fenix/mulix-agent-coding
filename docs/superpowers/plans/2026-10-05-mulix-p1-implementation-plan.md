# Mulix P1 实现计划：核心闭环 + 缓存底座

- 日期：2026-10-05
- 规格：[2026-10-05-mulix-agent-coding-design.md](../specs/2026-10-05-mulix-agent-coding-design.md)（已批准，本计划只覆盖其 P1 范围）
- 出口标准：规格 §1.2 的全部成功标准逐条可验证
- 执行约定：**测试先于实现**（每个任务先写测试再写实现）；每完成一个里程碑运行该里程碑的验收命令；不引入规格之外的依赖

## 依赖与构建基线

- Go：`go.mod` 写 `module mulix` + `go 1.26`（`GOTOOLCHAIN=auto` 跟随工具链）
- P1 第三方依赖（全部纯 Go）：`github.com/spf13/cobra`（子命令）、`charm.land/bubbletea/v2` + `charm.land/lipgloss/v2`（TUI）、`github.com/BurntSushi/toml`（配置）、`github.com/modelcontextprotocol/go-sdk`（MCP server 框架）
- 引擎：用户环境自备 claude CLI（版本经 `engine.lock` pin）；仓库自身不 vendor 引擎

## 里程碑总览与依赖

```
M0 地基 ──► M2 event/config ──┬─► M3 sessionx ──┐
                              ├─► M5 cache ─────┼─► M6 guard ──► M7 control ──► M8 registry ──► M9 cli/TUI ──► M10 CI ──► M11 e2e 收口
M1 协议探针 T0 ───────────────┴─► M4 engine ────┘
```

每个里程碑 = 一组任务（编号 T.n），任务内先列「测试先行」再列「实现」。

---

## M0 项目地基

- **T0.1 仓库骨架**：`go.mod`（module mulix）、`.gitignore`、`Makefile`（build/test/lint/fmt/clean，对齐规格 §13）、`README.md`（一句话定位 + 快速开始占位节，不写 TODO——直接写明「见 docs/specs」）、目录树 `cmd/mulix internal/{cli,control,engine,sessionx,cache,guard,registry,config,event,textutil,jsonl} scripts docs/{specs,evidence} fixtures/engine`
- **T0.2 go 工具链验证**：`go build ./...` 空骨架可编译；`gofmt` 干净
- 验收：`make build test` 绿（0 用例通过即绿）

## M1 协议探针 T0（spike，M4 的前置）

- **T1.1 探针脚本** `scripts/probe-engine.md`（操作手册而非代码）：在临时目录以 `--input-format stream-json --output-format stream-json` 手工驱动 claude CLI，验证并记录：① init 事件结构与版本字段；② assistant/user/result 事件字段；③ `--include-partial-messages` 的 delta 事件；④ 控制协议 canUseTool 回合（触发一次需审批的写操作，观察 control_request 及应答格式）；⑤ hook 回调格式；⑥ `--append-system-prompt`/`--mcp-config`/`--allowedTools`/`--resume`/`--fork-session`/`--setting-sources` 实际生效
- **T1.2 fixture 录制**：把一次「用户输入 → 工具审批 → 结果」完整会话录成 `fixtures/engine/happy-turn.jsonl`（脱敏）；再录一次含 usage 的多回合 `fixtures/engine/usage.jsonl`
- **T1.3 产出**：`docs/specs/engine-protocol.md`（事件类型/字段/控制协议清单，标注来源 CLI 版本）+ `engine.lock` 初值（cli_version、protocol_probe、pinned_at）
- 验收：engine-protocol.md 能让不了解 SDK 的人实现解码器；fixture 可被 M4 的解码器回放

## M2 event / config / jsonl leaves

- **T2.1 `internal/event`**：类型化事件（Kind 常量：TurnStarted/AssistantText/AssistantThinking/ToolUseRequested/ToolResult/ApprovalRequest/ApprovalResolved/Usage/Notice/TurnDone/EngineExited）；struct 字段全部可 JSON 往返；测试：每种事件 JSON round-trip
- **T2.2 `internal/jsonl`**：追加写（每行一 JSON，fsync 可选）+ 增量读 + 损坏尾部检测（返回已完整行数与 damaged 标记）；测试：追加/读回/尾部截断注入
- **T2.3 `internal/config`**：按规格 §12 实现 mulix.toml 分层加载（flag > project > user > default）、user-global 锁定项（engine.allow_drift、danger-full 准入、告警上报行为）、last-known-good 恢复 + loadWarnings；测试：优先级矩阵、损坏文件恢复、锁定项被项目覆盖时报 warning 且不生效
- 验收：`go test ./internal/config/ ./internal/event/ ./internal/jsonl/`

## M3 sessionx 权威镜像

- **T3.1 Store**：`~/.mulix/sessions/<id>/events.jsonl` 追加 + `meta.json`（engine session id、模型、工作区、时间）+ `cache.json`（Meter 汇总投影，由 control 写）；**先持久化后发布**的 Append 接口（返回后才允许广播）
- **T3.2 回放与恢复**：`Replay()` 全量读回；`ScanSessions()` 列表；损坏尾部移 `.damaged` 并告警（对齐规格 §10）
- **T3.3 id 映射**：Go session id ↔ 引擎 session id 双向映射持久化在 meta.json
- 测试先行：追加即持久（模拟进程崩溃后重新打开可读全部事件）；damaged 尾部不丢好数据
- 验收：`go test ./internal/sessionx/`

## M4 engine 宿主层

- **T4.1 `Host` 接口 + 类型**（规格 §7.1 签名为准）：`BootConfig`（工作区、模型、权限模式、MCP 配置 JSON、追加 system prompt、resume id…）、`UserMessage`（含 turn tail 块）、`EngineEvent`、`ControlRequest`/`ControlResponse`、`EngineInfo`
- **T4.2 stream-json 解码器**：行 → EngineEvent；未知字段容忍（记录 raw）；fixture 回放测试（M1 的两个 fixture 全量解码无错、关键字段断言）
- **T4.3 `ClaudeHost`**：spawn（参数集见规格 §7.2，逐字对齐 T0 结论）、stdout 行泵 → Events()、控制协议请求路由（can_use_tool/hook_callback → 上层经 Channel 请求应答）、Interrupt、有界 Close（优雅→超时杀进程树）、stderr 脱敏环形缓冲、engine.lock 校验（漂移默认拒绝）
- **T4.4 `FakeHost`**：读 fixture 按脚本回放 + 可编程控制请求注入点（e2e 的引擎替身）
- 测试先行：解码器 fixture 回放；ClaudeHost 用「假 CLI」可执行文件（fixture 服务脚本）做进程级测试（spawn/事件/控制往返/关闭超时）
- 验收：`go test ./internal/engine/`；fixture 回放 e2e 贯通

## M5 cache 三层

- **T5.1 BootComposer**（规格 §8.1 C1/C2）：`Compose(cfg, workspace) BootArtifact{AppendSystemPrompt, MCPConfigJSON, AllowedTools, PermissionMode, Fingerprint}`；稳定序列化器（key 排序、列表显式顺序）；测试：`TestBootComposeByteStable`（两次 Compose 字节一致）、`TestBootComposeOrderInsensitive`（输入 map 顺序不影响输出）
- **T5.2 Meter**（§8.2）：Observe(usage) → Record{uncached, write, read, hit, cost, saving}；`Report()` 会话累计；JSONL 记账 `~/.mulix/usage/<session>.jsonl`；`MarkActivity(t)` 支撑 TTL：空闲 ≥ cold_after_idle 后首个回合标 `expected_cold`；测试：命中率/成本公式、TTL 边界（3m59s 热 Border / 4m01s 冷）、记账文件往返
- **T5.3 Guard**（§8.3）：滑动窗口（最近 10 个有效回合，排除 expected_cold）命中率 < floor → Notice；测试：窗口统计、expected_cold 排除、floor 边界
- 验收：`go test ./internal/cache/`；三个规格护栏测试全部在列

## M6 guard 权限与钩子

- **T6.1 Policy**：纯函数 `Decide(tool, input) Decision`；规则 `deny > ask > allow > fallback`；`Tool(specifier)` 匹配（`Bash(go test:*)`、`Edit(docs/**)` 字面 + glob）；预设 default/acceptEdits/plan/danger-full；测试：优先级矩阵、specifier 匹配边界、预设矩阵
- **T6.2 Gate**：Ask → 审批队列（Approver 接口；无人值守/超时 120s fail-closed Deny）；会话级授权（工具+规范化范围，内存 + 镜像事件）；测试：超时 fail-closed、会话授权命中、无人值守拒绝
- **T6.3 Hooks**：SessionStart/PreToolUse/PostToolUse/Stop 回调分发（P1 内置消费者：审计日志、Meter、notice）；测试：分发顺序、panic 隔离
- 验收：`go test ./internal/guard/`

## M7 control 编排

- **T7.1 Controller**：Options{HostFactory, Sink, Store, Meter, Gate, Config}；方法 Submit/Cancel/Approve/NewSession/Resume/Snapshot；`ErrTurnRunning`（会话内回合互斥，规格 §6）；事件双路分发（sessionx 先落、Sink 后发）
- **T7.2 控制请求路由**：can_use_tool → Gate → RespondControl；hook_callback → Hooks；usage → Meter；result → TurnDone（含最终 Report）
- **T7.3 生命周期**：NewSession（重新 Compose，快照 C1/C3）/Resume（engine.lock 校验 + resume 链事件入镜像）；崩溃恢复：启动时检测未闭合回合 → recovery_required → 引导 `mulix --resume`
- 测试先行（FakeHost × 真实栈）：happy 回合、审批 deny 链路、ErrTurnRunning、取消、崩溃恢复、Meter 记账断言
- 验收：`go test ./internal/control/`

## M8 registry（in-process MCP 框架）

- **T8.1 Server 框架**：官方 go-sdk 起 in-process/stdio server；`Tool` 接口（Name/Description/Schema/Execute + ReadOnly 元数据）；`Register` 全局注册（重名 panic）；`--mcp-config` 生成（顺序稳定，接 BootComposer）
- **T8.2 首个内置工具 `mulix_usage`**：返回当前会话 Meter 汇总（命中率/tokens/成本/节省）——既是产品功能又验证 MCP 全链路
- 测试：server 起停、工具列表顺序稳定、schema 冻结（两次快照字节一致）
- 验收：`go test ./internal/registry/`

## M9 cli 与 TUI

- **T9.1 cobra 骨架**：`mulix`（默认进 TUI）、`run`（无人值守，无审批者 fail-closed）、`doctor`、`cache`、`sessions`、`--resume`、`--allow-engine-drift`
- **T9.2 TUI（bubbletea v2）**：transcript 视口（流式追加）、composer、状态栏（模型 | 会话命中率 | 本回合 tokens | 成本；expected_cold 标注）、审批卡（本次允许/本会话允许/拒绝）、`/` 命令（/new /resume /cache /doctor /rewind 透传引擎 checkpoint）
- **T9.3 doctor**：node → claude CLI → engine.lock 一致性 → 凭据 → 工作区可写；输出结构化报告 + `--redact` 脱敏导出
- 测试先行：tea model 状态机单测（审批态/渲染态互斥）；doctor 各检查项可注入故障
- 验收：`go test ./internal/cli/`；真实 CLI 手工冒烟（T0 环境复用）：完成一次真实「读文件→改文件→跑测试」回合

## M10 CI 与门禁

- **T10.1 `scripts/check-cache-impact.sh`**：diff 触及缓存敏感路径（internal/cache、engine、control、registry、docs prompt 模板）→ 要求 PR body `Cache-impact:` + `Cache-guard:`（本地与 CI 同源）
- **T10.2 `.github/workflows/ci.yml`**：golangci-lint（版本 pin 文件）+ go test ./... + 交叉编译矩阵（三平台）+ cache-impact 检查 + engine.lock 格式校验
- 验收：本地跑 check 脚本两例（命中路径缺元数据 → 拒绝；带元数据 → 通过）

## M11 e2e 收口（出口标准验收）

- **T11.1 系统级 e2e**（FakeHost × 真实 control/cache/sessionx/cli 栈，`internal/e2e/`）：
  1. happy 回合（含审批 Allow）→ 事件全落镜像、Sink 顺序正确
  2. 审批 Deny / 无人值守 fail-closed
  3. usage → Meter → `mulix cache` 报表数字一致
  4. 空闲 TTL → expected_cold 标注且不触发告警；命中率跌破 floor → Notice
  5. 崩溃恢复：杀 FakeHost 中途 → recovery → Resume 续链
  6. 两次 Start 的 BootArtifact 字节一致（构造护栏在系统边界复验）
- **T11.2 验收清单**：规格 §1.2 七条成功标准逐条映射到上述测试与手工冒烟记录，写入 `docs/evidence/p1-acceptance.md`
- 验收：`make test cross` 全绿；验收清单全部勾选

---

## 里程碑顺序执行表

| 顺序 | 里程碑 | 前置 | 预估规模 |
| --- | --- | --- | --- |
| 1 | M0 地基 | — | 小 |
| 2 | M1 协议探针 T0 | M0 | 中（含人工操作） |
| 3 | M2 leaves | M0 | 小 |
| 4 | M3 sessionx、M5 cache（可并行） | M2 | 中 |
| 5 | M4 engine | M1, M3 | 大 |
| 6 | M6 guard | M2 | 中 |
| 7 | M7 control | M3–M6 | 大 |
| 8 | M8 registry | M5 | 小 |
| 9 | M9 cli/TUI | M7, M8 | 大 |
| 10 | M10 CI | M9 | 小 |
| 11 | M11 e2e 收口 | 全部 | 中 |

## 风险触发器（执行中升级路径）

- T0 探针发现控制协议与规格 §7 假设不符 → 停，修订规格 §7 再继续（规格是契约）
- T4 fixture 回放无法覆盖某事件类型 → 补录 fixture，不手写伪造字段
- 任何里程碑验收命令连续两轮不过 → 停下排查，不带病前进
