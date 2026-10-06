# Mulix P1 核心闭环 + 缓存底座 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 mulix 的 P1 范围：`mulix`（TUI）/`mulix run` 核心闭环（引擎接入、权限治理、会话镜像与恢复）与缓存治理三层底座，达成 spec.md 的 SC-001..SC-007 全部成功标准。

**Architecture:** 方案 C「托管边界混合」（design §4/§5）：Go 宿主直接拉起 claude CLI 子进程，以 stream-json 与控制协议驱动——引擎管回合内（消息序列、压缩、采样、内置工具），Go 管回合间（会话权威镜像、权限治理、缓存治理三层、多入口编排）。分层：`cmd/mulix` → `internal/{cli,serve}` → `internal/control` → `{engine,sessionx,cache,guard,registry}` + leaves（`config,event,textutil,jsonl,i18n`），分层规则由 design §5.3 约束（P1 以 review 约定执行）。

**Tech Stack:** Go 1.26（module `mulix`，`GOTOOLCHAIN=auto`）；`github.com/spf13/cobra`（子命令）；`charm.land/bubbletea/v2` + `charm.land/lipgloss/v2`（TUI）；`github.com/BurntSushi/toml`（配置）；`github.com/modelcontextprotocol/go-sdk`（MCP server 框架）；引擎为用户自备 claude CLI（版本经 `engine.lock` pin，仓库不 vendor 引擎）。

**Spec:** `docs/specs/001-p1-core-loop-and-cache-foundation/spec.md`

**Design:** `.mulix/.runtime/001-p1-core-loop-and-cache-foundation/specs/2026-10-06-p1-architecture-design.md`（2026-10-06 用户批准；迁移自已批准的原设计规格）

> **迁移说明（保真度约定，用户 2026-10-06 明示选择）**：本计划由原《Mulix P1 实现计划：核心闭环 + 缓存底座》（2026-10-05，git 历史 ba8d709）忠实迁移：每个 `## Task N` 对应原计划一个任务（括注原编号与里程碑），保留其测试清单、验收命令与执行约定；里程碑顺序按原计划「顺序执行表」（M0 → M1 → M2 → M3+M5 → M4 → M6 → M7 → M8 → M9 → M10 → M11）。按该约定，任务步骤不预写完整测试/实现代码块——build 阶段执行每个任务时，先按任务「测试先行」清单以 TDD 写出测试（RED→验证 RED），再写最小实现（GREEN→验证 GREEN），证据记入构建台账。这是与 writing-plans「完整代码块」标准的已知、经用户批准的偏差。评审后的修订统一记录在文末「评审修订记录」。

## Global Constraints

（摘自原计划「依赖与构建基线」「执行约定」与 design 硬约束，逐条执行；每个任务的要求隐含包含本节。）

- Go：`go.mod` 写 `module mulix` + `go 1.26`（`GOTOOLCHAIN=auto` 跟随工具链）。
- P1 第三方依赖（全部纯 Go）：`github.com/spf13/cobra`、`charm.land/bubbletea/v2`、`charm.land/lipgloss/v2`、`github.com/BurntSushi/toml`、`github.com/modelcontextprotocol/go-sdk`；不引入规格之外的依赖。
- 引擎：用户环境自备 claude CLI（版本经 `engine.lock` pin）；仓库自身不 vendor 引擎。
- 执行约定：**测试先于实现**（每个任务先写测试再写实现）；每完成一个里程碑运行该里程碑的验收命令；不引入规格之外的依赖。
- 权限红线：绝不使用 `--dangerously-skip-permissions`；权限模式用 `--permission-mode` + canUseTool 控制协议回调（design §7.2）。
- 缓存构造红线（design §8.1 C1–C5）：追加 system prompt 生产者唯一（BootComposer）、会话内零变更；MCP 清单与工具面注册期冻结、顺序稳定；项目指令启动快照、变更仅提醒；动态内容只走 user message 尾部（turn tail）；恢复链显式选择。
- 分层红线（design §5.3）：只有 `internal/cli`、`internal/serve`（及 `cmd/`）可 import `internal/control`；`control` 不得 import 任何 frontends；leaves（`config,event,textutil,jsonl,i18n`——i18n 为 design 清单成员，P1 骨架未包含，引入时按同一 leaf 规则处理）禁止 import 任何 `internal/*` 内核包；内置工具子包经 `init()` 自注册到 `registry`，父包不反向 import 子包。
- 事件分发红线（design §6）：镜像写入是转发给前端的前置条件——先持久化后发布，顺序不可颠倒。
- 里程碑验收连续两轮不过：停下排查，不带病前进（原计划「风险触发器」）。

## Review Focus

（spec 隐含但无单一任务测试覆盖、最可能咬到使用者的五类输入；每行由所属任务的钉死测试覆盖。）

- 启动参数遍历顺序：同一配置以不同 map 遍历顺序两次 Compose，输出必须逐字节一致 → Task 12 `TestBootComposeOrderInsensitive`。
- 缓存 TTL 边界：空闲 3m59s 的回合为热、4m01s 的回合标 expected_cold → Task 13 Meter TTL 边界用例。
- 镜像损坏尾部：进程中断留下的最后半行 JSON 不得导致好数据丢失，须返回已完整行数与 damaged 标记 → Task 7 尾部截断注入 + Task 10 damaged 尾部用例。
- 无人值守与审批超时：无 Approver 或等待超过 120s 必须 fail-closed 拒绝，不得放行或挂起 → Task 20 Gate 用例 + Task 32 e2e 场景 2。
- 引擎事件流未知字段：解码器必须容忍未知字段并记录 raw，不得报错丢弃 → Task 16 解码器容忍性用例。

---

## Task 1: 仓库骨架（原 T0.1，M0）

**Files:**
- Create: `go.mod`（module mulix、go 1.26）、`.gitignore`、`Makefile`（build/test/lint/fmt/clean，对齐 design §13）、`README.md`（一句话定位 + 快速开始占位节，不写 TODO——直接写明「见 docs/specs」）
- Create: `cmd/mulix/main.go`（最小入口占位）；`internal/{cli,control,engine,sessionx,cache,guard,registry,config,event,textutil,jsonl}/doc.go`（包声明 + 一行职责注释，对齐 design §5.2）
- Create: 目录树占位 `scripts/`、`docs/evidence/`、`fixtures/engine/`（`docs/specs/` 已由本 change 建立即跳过）

**Interfaces:**
- Consumes: —
- Produces: module `mulix` 与 design §5.1 分层目录结构；`make build|test|lint|fmt|clean` 目标名（后续任务与 CI 依赖）。

- [ ] **Step 1: 建立骨架**：写 `go.mod`、`.gitignore`、`Makefile`、`README.md`、`cmd/mulix/main.go` 与各 internal 包 `doc.go`，创建目录树占位。

- [ ] **Step 2: 验证骨架可编译（RED 判据）**

Run: `go build ./...`
Expected: 编译通过（空骨架）；`gofmt -l .` 无输出。

- [ ] **Step 3: 验收（M0）**

Run: `make build test`
Expected: 绿（0 用例通过即绿）。

- [ ] **Step 4: Commit**

```bash
git add go.mod .gitignore Makefile README.md cmd internal scripts docs fixtures
git commit -m "chore: P1 仓库骨架（M0/T0.1）"
```

## Task 2: go 工具链验证（原 T0.2，M0）

**Files:**
- Modify: `go.mod`（如工具链需要，补 `toolchain` 指令）

**Interfaces:**
- Consumes: Task 1 骨架。
- Produces: 确认 `go build ./...` 空骨架可编译、`gofmt` 干净的基线。

- [ ] **Step 1: 验证**

Run: `go build ./...; gofmt -l .`
Expected: 编译通过；gofmt 无输出。若失败，修复骨架后复跑。

- [ ] **Step 2: 验收（M0 复跑）**

Run: `make build test`
Expected: 绿。

- [ ] **Step 3: Commit**（仅当产生文件变更）

```bash
git add go.mod
git commit -m "chore: 固定 go 工具链（M0/T0.2）"
```

## Task 3: 探针脚本（原 T1.1，M1，spike 手册）

**Files:**
- Create: `scripts/probe-engine.md`（操作手册而非代码）

**Interfaces:**
- Consumes: 用户环境自备的 claude CLI。
- Produces: 协议事实记录（Task 4 fixture 录制与 Task 15/16 类型、解码器实现的依据）。

- [ ] **Step 1: 写手册框架**：固定六个验证点——① init 事件结构与版本字段；② assistant/user/result 事件字段；③ `--include-partial-messages` 的 delta 事件；④ 控制协议 canUseTool 回合（触发一次需审批的写操作，观察 control_request 及应答格式）；⑤ hook 回调格式；⑥ `--append-system-prompt`/`--mcp-config`/`--allowedTools`/`--resume`/`--fork-session`/`--setting-sources` 实际生效。

- [ ] **Step 2: 在临时目录以 `--input-format stream-json --output-format stream-json` 手工驱动 claude CLI**，逐项实测并把可复现命令、观察记录、结论写入 `scripts/probe-engine.md`；发现与 design §7 假设不符即停下修订 design（原计划风险触发器）。

- [ ] **Step 3: 复核**：六个验证点每条都有「命令 + 观察 + 结论」三要素。

- [ ] **Step 4: Commit**

```bash
git add scripts/probe-engine.md
git commit -m "docs: 引擎协议探针手册（M1/T1.1）"
```

## Task 4: fixture 录制（原 T1.2，M1）

**Files:**
- Create: `fixtures/engine/happy-turn.jsonl`（一次「用户输入 → 工具审批 → 结果」完整会话，脱敏）、`fixtures/engine/usage.jsonl`（一次含 usage 的多回合会话，脱敏）

**Interfaces:**
- Consumes: Task 3 的手册（录制环境复用 T0 探针环境）。
- Produces: Task 16 解码器回放测试、Task 18 FakeHost 回放、Task 32 e2e 依赖的两个 fixture。

- [ ] **Step 1: 按 Task 3 手册录制两段会话**，落盘为上述两个文件。

- [ ] **Step 2: 验证**：两文件均可逐行解析为 JSON；含审批回合与 usage 字段；脱敏检查通过（无 token/密钥/邮箱明文）。

- [ ] **Step 3: Commit**

```bash
git add fixtures/engine/happy-turn.jsonl fixtures/engine/usage.jsonl
git commit -m "test: 录制引擎协议 fixture（M1/T1.2）"
```

## Task 5: 协议文档与 engine.lock 初值（原 T1.3，M1）

**Files:**
- Create: `docs/specs/engine-protocol.md`（事件类型/字段/控制协议清单，标注来源 CLI 版本）、`engine.lock`（cli_version、protocol_probe、pinned_at；baseline 占位按 design §7.3）

**Interfaces:**
- Consumes: Task 3 记录、Task 4 fixture。
- Produces: `engine.lock`（Task 17 校验用）；协议字段清单（Task 15 类型、Task 16 解码器用）。

- [ ] **Step 1: 固化 `docs/specs/engine-protocol.md` 与 `engine.lock` 初值。**

- [ ] **Step 2: 验证（M1 出口）**：engine-protocol.md 能让不了解 SDK 的人实现解码器（自检：仅凭该文档列出全部事件类型的字段清单）；fixture 与文档字段一一对应。

- [ ] **Step 3: Commit**

```bash
git add docs/specs/engine-protocol.md engine.lock
git commit -m "docs: 引擎协议规格与 engine.lock 初值（M1/T1.3）"
```

## Task 6: internal/event 类型化事件（原 T2.1，M2）

**Files:**
- Create: `internal/event/*.go`
- Test: `internal/event/*_test.go`

**Interfaces:**
- Consumes: —
- Produces: 事件 Kind 常量 `TurnStarted/AssistantText/AssistantThinking/ToolUseRequested/ToolResult/ApprovalRequest/ApprovalResolved/Usage/Notice/TurnDone/EngineExited` 与对应结构体；字段全部可 JSON 往返（sessionx/control/cli 共用）。

- [ ] **Step 1: 写失败测试**：每种事件的 JSON round-trip 用例（按 Produces 清单逐 Kind 覆盖）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/event/`
Expected: FAIL（类型未实现）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/event/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/event
git commit -m "feat(event): 类型化事件与 JSON 往返（M2/T2.1）"
```

## Task 7: internal/jsonl 追加日志（原 T2.2，M2）

**Files:**
- Create: `internal/jsonl/*.go`
- Test: `internal/jsonl/*_test.go`

**Interfaces:**
- Consumes: —
- Produces: 追加写（每行一 JSON，fsync 可选）+ 增量读 + 损坏尾部检测（返回已完整行数与 damaged 标记）；sessionx 直接依赖。

- [ ] **Step 1: 写失败测试**：追加/读回/尾部截断注入（半行 JSON → 已完整行数 + damaged 标记）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/jsonl/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/jsonl/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/jsonl
git commit -m "feat(jsonl): 追加写与损坏尾部检测（M2/T2.2）"
```

## Task 8: internal/config 分层加载（原 T2.3，M2）

**Files:**
- Create: `internal/config/*.go`
- Test: `internal/config/*_test.go`

**Interfaces:**
- Consumes: —
- Produces: mulix.toml 分层加载（flag > project > user > default，design §12）；user-global 锁定项五类——引擎版本策略（`engine.allow_drift`）、凭据来源、遥测开关、告警上报行为、danger-full 准入（P1 配置骨架暂无凭据来源/遥测开关的独立键，锁定机制按可扩展实现，键位引入即纳入锁定）；last-known-good 恢复 + `loadWarnings`。

- [ ] **Step 1: 写失败测试**：优先级矩阵、损坏文件恢复、锁定矩阵覆盖上列各项（无键项测扩展机制本身）：锁定项被项目覆盖时报 warning 且不生效。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/config/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M2 验收**

Run: `go test ./internal/config/ ./internal/event/ ./internal/jsonl/`
Expected: PASS（M2 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): mulix.toml 分层加载与锁定项（M2/T2.3）"
```

## Task 9: sessionx Store（原 T3.1，M3）

**Files:**
- Create: `internal/sessionx/*.go`
- Test: `internal/sessionx/*_test.go`

**Interfaces:**
- Consumes: `internal/jsonl`（Task 7）。
- Produces: `~/.mulix/sessions/<id>/events.jsonl` 追加 + `meta.json`（engine session id、模型、工作区、时间）+ `cache.json`（Meter 汇总投影，由 control 写）；**先持久化后发布**的 Append 接口（返回后才允许广播）。

- [ ] **Step 1: 写失败测试**：追加即持久（模拟进程崩溃后重新打开可读全部事件）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/sessionx/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/sessionx/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/sessionx
git commit -m "feat(sessionx): 权威镜像 Store 与先持久化后发布（M3/T3.1）"
```

## Task 10: sessionx 回放与恢复（原 T3.2，M3）

**Files:**
- Modify: `internal/sessionx/`
- Test: `internal/sessionx/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 9 Store。
- Produces: `Replay()` 全量读回；`ScanSessions()` 列表；损坏尾部移 `.damaged` 并告警（对齐 design §10）。

- [ ] **Step 1: 写失败测试**：Replay/ScanSessions 往返；damaged 尾部不丢好数据。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/sessionx/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/sessionx/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/sessionx
git commit -m "feat(sessionx): 回放、扫描与损坏尾部隔离（M3/T3.2）"
```

## Task 11: sessionx id 映射（原 T3.3，M3）

**Files:**
- Modify: `internal/sessionx/`
- Test: `internal/sessionx/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 9 meta.json。
- Produces: Go session id ↔ 引擎 session id 双向映射持久化在 meta.json（control/Resume 依赖）。

- [ ] **Step 1: 写失败测试**：双向映射的写入与查回。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/sessionx/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M3 验收**

Run: `go test ./internal/sessionx/`
Expected: PASS（M3 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/sessionx
git commit -m "feat(sessionx): 会话 id 双向映射（M3/T3.3）"
```

## Task 12: cache BootComposer（原 T5.1，M5）

**Files:**
- Create: `internal/cache/`（Boot 部分）
- Test: `internal/cache/*_test.go`

**Interfaces:**
- Consumes: `internal/config`（Task 8）、`internal/registry` 注册面（本任务先以注入顺序的最小接口承接，Task 25 对齐）。
- Produces: `Compose(cfg, workspace) BootArtifact{AppendSystemPrompt, MCPConfigJSON, AllowedTools, PermissionMode, Fingerprint}`；稳定序列化器（key 排序、列表显式顺序）；实现 design §8.1 C1/C2。

- [ ] **Step 1: 写失败测试（规格护栏）**：`TestBootComposeByteStable`（同配置两次 Compose，追加段 + mcp-config JSON + allowedTools 序列逐字节一致）、`TestBootComposeOrderInsensitive`（输入参数遍历顺序不影响输出字节）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/cache/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/cache/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cache
git commit -m "feat(cache): BootComposer 字节稳定组合（M5/T5.1）"
```

## Task 13: cache Meter（原 T5.2，M5）

**Files:**
- Create: `internal/cache/`（Meter 部分）
- Test: `internal/cache/*_test.go`（新增用例）

**Interfaces:**
- Consumes: —
- Produces: `Observe(usage) → Record{uncached, write, read, output, hit, cost, saving}`（记账 JSONL 落全部四类 token 计数，FR-009）；`Report()` 会话累计；JSONL 记账 `~/.mulix/usage/<session>.jsonl`；`MarkActivity(t)` 支撑 TTL：空闲 ≥ `cold_after_idle` 后首个回合标 `expected_cold`；公式按 design §8.2。

- [ ] **Step 1: 写失败测试**：命中率/成本公式；TTL 边界（3m59s 热 / 4m01s 冷）；记账文件往返。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/cache/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/cache/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cache
git commit -m "feat(cache): Meter 记账与 TTL 感知（M5/T5.2）"
```

## Task 14: cache Guard（原 T5.3，M5）

**Files:**
- Create: `internal/cache/`（Guard 部分）
- Test: `internal/cache/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 13 Record 流。
- Produces: 滑动窗口（最近 10 个有效回合，排除 expected_cold）命中率 < floor（默认 0.70，可配置）→ Notice（design §8.3）。

- [ ] **Step 1: 写失败测试**：窗口统计、expected_cold 排除、floor 边界。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/cache/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M5 验收**

Run: `go test ./internal/cache/`
Expected: PASS（M5 验收；design §8.1 三个护栏中两个在本里程碑在列，`TestNoSecondSystemPromptPath` 为静态规则，P1 以 review 约定执行，见 design §5.3 第 6 条与 §11）。

- [ ] **Step 5: Commit**

```bash
git add internal/cache
git commit -m "feat(cache): 命中率地板告警 Guard（M5/T5.3）"
```

## Task 15: engine Host 接口 + 类型（原 T4.1，M4）

**Files:**
- Create: `internal/engine/*.go`
- Test: `internal/engine/*_test.go`（类型 JSON 往返等基础用例）

**Interfaces:**
- Consumes: —
- Produces（以 design §7.1 签名为准）: `Host` 接口（`Start/Send/Events/RespondControl/Interrupt/Close/Info`）；`BootConfig`（工作区、模型、权限模式、MCP 配置 JSON、追加 system prompt、resume id…）；`UserMessage`（含 turn tail 块）；`EngineEvent`；`ControlRequest`/`ControlResponse`；`EngineInfo`。

- [ ] **Step 1: 写失败测试**：类型与接口的编译期断言 + 关键类型 JSON 往返。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/engine/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/engine/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/engine
git commit -m "feat(engine): Host 接口与核心类型（M4/T4.1）"
```

## Task 16: stream-json 解码器（原 T4.2，M4）

**Files:**
- Create: `internal/engine/`（解码器）
- Test: `internal/engine/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 15 类型、Task 5 协议文档、Task 4 fixtures。
- Produces: 行 → `EngineEvent`；未知字段容忍（记录 raw）。

- [ ] **Step 1: 写失败测试**：fixture 回放（M1 两个 fixture 全量解码无错、关键字段断言）+ 未知字段容忍用例。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/engine/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/engine/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/engine
git commit -m "feat(engine): stream-json 解码器与 fixture 回放（M4/T4.2）"
```

## Task 17: ClaudeHost 真实实现（原 T4.3，M4）

**Files:**
- Create: `internal/engine/`（ClaudeHost）
- Create: `internal/engine/testdata/`（假 CLI fixture 服务脚本）
- Test: `internal/engine/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 15/16；`engine.lock`（Task 5）。
- Produces: spawn（参数集见 design §7.2，逐字对齐 T0 结论）、stdout 行泵 → `Events()`、控制协议请求路由（can_use_tool/hook_callback → 上层经 Channel 请求应答）、`Interrupt`、有界 `Close`（优雅 → 超时杀进程树）、stderr 脱敏环形缓冲、engine.lock 校验（漂移默认拒绝；`--allow-engine-drift` 显式放行时向会话镜像写入留痕事件——FR-003/SC-005）。

- [ ] **Step 1: 写失败测试**：用「假 CLI」可执行文件（`internal/engine/testdata/` 服务脚本）做进程级测试（spawn/事件/控制往返/关闭超时、版本不匹配拒绝启动、漂移放行留痕的镜像事件断言）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/engine/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/engine/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/engine
git commit -m "feat(engine): ClaudeHost 子进程宿主（M4/T4.3）"
```

## Task 18: FakeHost（原 T4.4，M4）

**Files:**
- Create: `internal/engine/`（FakeHost）
- Test: `internal/engine/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 15 接口、Task 4 fixtures。
- Produces: 读 fixture 按脚本回放 + 可编程控制请求注入点（e2e 的引擎替身，Task 22/32 依赖）。

- [ ] **Step 1: 写失败测试**：fixture 脚本回放与控制请求注入。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/engine/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M4 验收**

Run: `go test ./internal/engine/`
Expected: PASS；fixture 回放 e2e 贯通（M4 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/engine
git commit -m "feat(engine): FakeHost 脚本回放（M4/T4.4）"
```

## Task 19: guard Policy（原 T6.1，M6）

**Files:**
- Create: `internal/guard/*.go`
- Test: `internal/guard/*_test.go`

**Interfaces:**
- Consumes: —
- Produces: 纯函数 `Decide(tool, input) Decision`；规则 `deny > ask > allow > fallback`；`Tool(specifier)` 匹配（`Bash(go test:*)`、`Edit(docs/**)` 字面 + glob）；预设 default/acceptEdits/plan/danger-full（design §9）。

- [ ] **Step 1: 写失败测试**：优先级矩阵、specifier 匹配边界、预设矩阵。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/guard/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/guard/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): 权限策略纯函数（M6/T6.1）"
```

## Task 20: guard Gate 审批队列（原 T6.2，M6）

**Files:**
- Create: `internal/guard/`（Gate）
- Test: `internal/guard/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 19 Decision。
- Produces: Ask → 审批队列（`Approver` 接口；无人值守/超时 120s fail-closed Deny）；会话级授权（工具+规范化范围，内存 + 镜像事件）。

- [ ] **Step 1: 写失败测试**：超时 fail-closed、会话授权命中、无人值守拒绝。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/guard/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/guard/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): 审批队列与会话级授权（M6/T6.2）"
```

## Task 21: guard Hooks（原 T6.3，M6）

**Files:**
- Create: `internal/guard/`（Hooks）
- Test: `internal/guard/*_test.go`（新增用例）

**Interfaces:**
- Consumes: —
- Produces: SessionStart/PreToolUse/PostToolUse/Stop 回调分发（P1 内置消费者：审计日志、Meter、notice，design §9）。

- [ ] **Step 1: 写失败测试**：分发顺序、panic 隔离。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/guard/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M6 验收**

Run: `go test ./internal/guard/`
Expected: PASS（M6 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/guard
git commit -m "feat(guard): 钩子分发（M6/T6.3）"
```

## Task 22: control Controller（原 T7.1，M7）

**Files:**
- Create: `internal/control/*.go`
- Test: `internal/control/*_test.go`

**Interfaces:**
- Consumes: `engine.Host`（Task 15/18）、`sessionx.Store`（Task 9–11）、`cache.Meter`（Task 13）、`guard.Gate`（Task 20）。
- Produces: `Options{HostFactory, Sink, Store, Meter, Gate, Config}`；方法 `Submit/Cancel/Approve/NewSession/Resume/Snapshot`；`ErrTurnRunning`（会话内回合互斥，design §6）；事件双路分发（sessionx 先落、Sink 后发）。

- [ ] **Step 1: 写失败测试**（FakeHost × 真实栈）：happy 回合事件顺序、`ErrTurnRunning`、cache.json 投影由 control 写入的断言。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/control/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/control/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/control
git commit -m "feat(control): Controller 与回合互斥（M7/T7.1）"
```

## Task 23: control 控制请求路由（原 T7.2，M7）

**Files:**
- Modify: `internal/control/`
- Test: `internal/control/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 22 Controller、Task 20 Gate、Task 13 Meter、Task 21 Hooks。
- Produces: can_use_tool → Gate → `RespondControl`；hook_callback → Hooks；usage → Meter；result → TurnDone（含最终 Report）。

- [ ] **Step 1: 写失败测试**（FakeHost × 真实栈）：审批 deny 链路、Meter 记账断言。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/control/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/control/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/control
git commit -m "feat(control): 控制请求路由（M7/T7.2）"
```

## Task 24: control 生命周期（原 T7.3，M7）

**Files:**
- Modify: `internal/control/`
- Test: `internal/control/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 22/23。
- Produces: NewSession（重新 Compose，快照 C1/C3；C3 的会话中变更提醒 watcher 属 P2——P1 交付启动快照与会话内不重载）/Resume（engine.lock 校验 + resume 链事件入镜像；漂移放行留痕在 Resume 链路同样生效；镜像与引擎不一致时以引擎为上下文权威并保留镜像告警，design §10）；崩溃恢复：启动时检测未闭合回合 → recovery_required → 引导 `mulix --resume`。

- [ ] **Step 1: 写失败测试**（FakeHost × 真实栈）：取消、崩溃恢复、Resume 续链、镜像与引擎不一致告警。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/control/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M7 验收**

Run: `go test ./internal/control/`
Expected: PASS（M7 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/control
git commit -m "feat(control): 会话生命周期与崩溃恢复（M7/T7.3）"
```

## Task 25: registry Server 框架（原 T8.1，M8）

**Files:**
- Create: `internal/registry/*.go`
- Test: `internal/registry/*_test.go`

**Interfaces:**
- Consumes: `github.com/modelcontextprotocol/go-sdk`；Task 12 BootComposer（mcp-config 生成对接）。
- Produces: 官方 go-sdk 起 in-process/stdio server；`Tool` 接口（Name/Description/Schema/Execute + ReadOnly 元数据）；`Register` 全局注册（重名 panic）；`--mcp-config` 生成（顺序稳定）。

- [ ] **Step 1: 写失败测试**：server 起停、工具列表顺序稳定、schema 冻结（两次快照字节一致）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/registry/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/registry/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/registry
git commit -m "feat(registry): in-process MCP server 框架（M8/T8.1）"
```

## Task 26: mulix_usage 内置工具（原 T8.2，M8）

**Files:**
- Create: `internal/registry/`（mulix_usage 工具子包，`init()` 自注册）
- Test: `internal/registry/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 25 框架、Task 13 Meter。
- Produces: 首个内置工具 `mulix_usage`：返回当前会话 Meter 汇总（命中率/tokens/成本/节省）——既是产品功能又验证 MCP 全链路。

- [ ] **Step 1: 写失败测试**：工具注册、执行返回 Meter 汇总。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/registry/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M8 验收**

Run: `go test ./internal/registry/`
Expected: PASS（M8 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/registry
git commit -m "feat(registry): mulix_usage 工具（M8/T8.2）"
```

## Task 27: cli cobra 骨架（原 T9.1，M9）

**Files:**
- Create: `internal/cli/`（命令路由）
- Modify: `cmd/mulix/main.go`（blank import 注册内置 MCP 工具，交给 cli）
- Test: `internal/cli/*_test.go`

**Interfaces:**
- Consumes: `internal/control`（唯一可 import control 的 frontends 之一）、Task 26 注册。
- Produces: `mulix`（默认进 TUI）、`run`（无人值守，无审批者 fail-closed）、`doctor`、`cache`、`sessions`、`--resume`、`--allow-engine-drift`（放行时留痕入会话镜像，接 Task 17 行为）。

- [ ] **Step 1: 写失败测试**：子命令路由与 flag 解析矩阵。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/cli/`
Expected: FAIL。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/cli/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cli cmd/mulix
git commit -m "feat(cli): cobra 命令骨架（M9/T9.1）"
```

## Task 28: TUI（原 T9.2，M9）

**Files:**
- Create: `internal/cli/`（TUI）
- Test: `internal/cli/*_test.go`（新增用例）

**Interfaces:**
- Consumes: Task 22 Controller 事件流、Task 20 审批队列。
- Produces: bubbletea v2 TUI：transcript 视口（流式追加）、composer、状态栏（模型 | 会话命中率 | 本回合 tokens | 成本；expected_cold 标注）、审批卡（本次允许/本会话允许/拒绝）、`/` 命令（/new /resume /cache /doctor /rewind 透传引擎 checkpoint）。

- [ ] **Step 1: 写失败测试**：tea model 状态机单测（审批态/渲染态互斥）。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/cli/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN**

Run: `go test ./internal/cli/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): TUI 会话界面（M9/T9.2）"
```

## Task 29: doctor（原 T9.3，M9）

**Files:**
- Create: `internal/cli/`（doctor）
- Test: `internal/cli/*_test.go`（新增用例）

**Interfaces:**
- Consumes: `engine.lock`（Task 5）。
- Produces: node → claude CLI → engine.lock 一致性 → 凭据 → 工作区可写；输出结构化报告 + `--redact` 脱敏导出（design §10 隐私条）。

- [ ] **Step 1: 写失败测试**：doctor 各检查项可注入故障。

- [ ] **Step 2: 验证 RED**

Run: `go test ./internal/cli/`
Expected: FAIL（新用例）。

- [ ] **Step 3: 写最小实现（GREEN）**

- [ ] **Step 4: 验证 GREEN 与 M9 验收**

Run: `go test ./internal/cli/`
Expected: PASS；另做真实 CLI 手工冒烟（T0 环境复用）：分别以 `mulix run` 与 TUI 各完成一次真实「读文件→改文件→跑测试→提交 git」回合，覆盖 SC-001 的两个入口（M9 验收）。

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): doctor 自检（M9/T9.3）"
```

## Task 30: check-cache-impact 脚本（原 T10.1，M10）

**Files:**
- Create: `scripts/check-cache-impact.sh`

**Interfaces:**
- Consumes: —
- Produces: diff 触及缓存敏感路径（internal/cache、engine、control、registry、docs prompt 模板）→ 要求 PR body 含 `Cache-impact:` + `Cache-guard:`（本地与 CI 同源，design §8.3）。

- [ ] **Step 1: 实现脚本与两例自验**：命中路径缺元数据 → 拒绝；带元数据 → 通过。

- [ ] **Step 2: 验证**

Run: `bash scripts/check-cache-impact.sh`（两例分别构造）
Expected: 一例拒绝、一例通过（M10 验收口径）。

- [ ] **Step 3: Commit**

```bash
git add scripts/check-cache-impact.sh
git commit -m "ci: 缓存影响门禁脚本（M10/T10.1）"
```

## Task 31: CI workflow（原 T10.2，M10）

**Files:**
- Create: `.github/workflows/ci.yml`、golangci-lint 版本 pin 文件、`scripts/check-engine-lock.sh`（engine.lock 格式校验，本地与 CI 同源）
- Modify: `Makefile`（新增 `cross` 目标：三平台交叉编译测试冒烟，供 M11 出口 `make test cross` 与 CI 矩阵使用）

**Interfaces:**
- Consumes: Task 30 脚本。
- Produces: golangci-lint（版本 pin）+ `go test ./...` + 交叉编译矩阵（Windows/macOS/Linux 三平台，含 `make test cross` 目标）+ cache-impact 检查 + engine.lock 格式校验（`scripts/check-engine-lock.sh`）。

- [ ] **Step 1: 写 workflow 与 lint 配置。**

- [ ] **Step 2: 验证**：本地复跑 cache-impact 两例（同 Task 30）；engine.lock 格式校验脚本对当前 lock 通过。

- [ ] **Step 3: Commit**

```bash
git add .github
git commit -m "ci: CI 工作流与门禁接线（M10/T10.2）"
```

## Task 32: 系统级 e2e（原 T11.1，M11）

**Files:**
- Create: `internal/e2e/*_test.go`
- Test: `internal/e2e/*_test.go`（即交付物）

**Interfaces:**
- Consumes: FakeHost（Task 18）× 真实 control/cache/sessionx/cli 栈。
- Produces: 六个系统边界场景的常驻回归测试：
  1. happy 回合（含审批 Allow）→ 事件全落镜像、Sink 顺序正确；
  2. 审批 Deny / 无人值守 fail-closed；
  3. usage → Meter → `mulix cache` 报表数字一致；
  4. 空闲 TTL → expected_cold 标注且不触发告警；命中率跌破 floor → Notice；
  5. 崩溃恢复：杀 FakeHost 中途 → recovery → Resume 续链；
  6. 两次 Start 的 BootArtifact 字节一致（构造护栏在系统边界复验）。

- [ ] **Step 1: 写六场景测试（此任务交付物即测试）。**

- [ ] **Step 2: 验证 RED→GREEN**：先确认测试因缺口失败（如有），补齐后全绿。

Run: `go test ./internal/e2e/`
Expected: PASS（六场景全绿）。

- [ ] **Step 3: Commit**

```bash
git add internal/e2e
git commit -m "test: 系统级 e2e 六场景（M11/T11.1）"
```

## Task 33: P1 验收清单（原 T11.2，M11）

**Files:**
- Create: `docs/evidence/p1-acceptance.md`

**Interfaces:**
- Consumes: Task 32 测试结果、Task 29 手工冒烟记录。
- Produces: spec.md 的 SC-001..SC-007 七条成功标准逐条映射到上述测试与手工冒烟记录（原规格 §1.2 的验收清单）；FR-008 中「会话中变更提醒」按 design §8.1 C3 口径记录为 P2 watcher（P1 交付启动快照与会话内不重载，见 Task 24）。

- [ ] **Step 1: 逐条映射并勾选**，写入 `docs/evidence/p1-acceptance.md`；有未达标项即回补对应任务，不带病前进。

- [ ] **Step 2: 验证（M11 出口）**

Run: `make test cross`
Expected: 全绿；验收清单全部勾选。

- [ ] **Step 3: Commit**

```bash
git add docs/evidence/p1-acceptance.md
git commit -m "docs: P1 验收清单与证据（M11/T11.2）"
```

---

## 评审修订记录（2026-10-06，plan reviewer 一轮）

按 `.dsh/skills/writing-plans/plan-document-reviewer-prompt.md` 评审（plan = tasks.md，spec = spec.md）后的一次性修订：**4 项 Issue 全部修复，7 项建议全部并入**。以下修订相对原《Mulix P1 实现计划》文本属评审驱动的补强，其余内容保持忠实迁移：

- [Issue 1] Task 17/24/27：补 `--allow-engine-drift` 显式放行留痕（镜像事件）与「版本不匹配拒绝启动」命名测试（FR-003/SC-005）。
- [Issue 2] Task 31：Makefile 新增 `cross` 目标 + `scripts/check-engine-lock.sh`，M11 出口 `make test cross` 可达。
- [Issue 3] Task 29：手工冒烟明确覆盖 `mulix run` 与 TUI 双入口、含 git 提交（SC-001 全口径）。
- [Issue 4] Task 8：user-global 锁定项补全为五类，并记录 P1 无键项的扩展机制口径（FR-016）。
- [建议] Task 24 补镜像不一致告警；Task 22 补 cache.json 写入断言；Task 13 Record 增 `output` 并注明四类计数落账；Task 17 假 CLI 脚本落位 `internal/engine/testdata/`；Task 31 校验脚本路径；Task 24/33 注明 C3 watcher 属 P2；Global Constraints 注明 i18n leaf 口径。
