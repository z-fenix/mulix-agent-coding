# CLAUDE.md — mulix 项目工作指令

这是 agent 在本仓库工作的持久上下文。 mulix 工作流的强制规则见 AGENTS.md（spec-driven 流程、mulix state 守卫），本文件不重复其条款，只做补充与沉淀。

关键原则：每行只保留 agent 无法自行从代码推断的信息。问自己：删掉这行会导致 agent 犯错误吗？如果不会，删掉。

## 1. 项目是什么

mulix 是一个 Go 实现的 coding agent 宿主，对齐 DeepSeek Harness（dsh）「一切皆插件」架构，用 Go 复刻。claude CLI 作为默认引擎实现（可配置）。

核心职责：拉起引擎子进程（stream-json + 控制协议），负责回合间会话权威镜像、权限治理、缓存治理三层。

产品公式：Model + Harness = Agent（Model 经引擎适配器接入，引擎为可替换插件）。

当前阶段：P4a 内核升级（change 010，进行中）。 历史分期与产品裁定见 AGENTS.md §历史阶段 与 .mulix/memory/history.md。

## 2. 关键红线（CRITICAL）

以下规则是硬约束，违反视为阻塞：

先持久化后发布：事件先写镜像再出 Sink；镜像单写者（泵）；1:1 不变量。

fail-closed：非法 JSON / 流损坏 / 未知治理形态 / 无人值守 ask → 一律拒绝收口，不静默。

kernel 不落盘：事件总线（010 后）为内存订阅层；镜像 events.jsonl 是唯一持久真相源。

双层注册：插件注册 effect 可逆；引擎可见清单回合开始冻结快照（P4b 落地）。

fail-closed 解析：jsonl 超限行/半行隔离为损坏段，不吞内容、不整体硬失败。

修订历史见各 spec Clarifications。

## 3. 架构

### 3.1 主流程（回合生命周期）

```
CLI(RunPrompt/openSession) → ctrl.NewSession → start(BootConfig → 引擎子进程
→ 泵启动 → watcher) → Submit(TurnStarted + 任务注入) → pump 逐行解码
→ 事件分流(onInit/onAssistant/onToolResult/onResult/onControlRequest)
→ 工具治理回路(evaluateCanUseTool 单飞 → Gate allow 清单/预设矩阵 →
审批桥/无人值守 fail-closed) → 回合收口(Meter 记账 → Usage/TurnDone →
cache.json 投影 → 放行下一次 Submit)
```

所有事件 → publish → Mirror（events.jsonl，1:1 审计）+ Sink（stdout/TUI）+ 钩子分发（4 时点）。

### 3.2 包分层矩阵（internal/lint/rules.go 为权威，violation 会红）

| 层级 | 包 |
|---|---|
| leaf（禁 import 内部包） | kernel、engine、event、jsonl、textutil、config、lint |
| 中间层 | memory→jsonl；sessionx→event,jsonl；guard→config；cache→config,jsonl；registry→cache |
| 编排 | subagent→event,kernel,engine,sessionx,cache,guard,memory；control→cache,config,engine,event,guard,memory,sessionx,subagent |
| 组合根 | cli/serve/cmd→internal/* |

### 3.3 kernel（cordis 模式，Go 版）

P2 按用户指令交付（internal/kernel）：层级 Context 作用域（继承注入）、Provide/Service、Mount 回滚、Dispose 级联（先子后父）。leaf 级、零第三方依赖。当前消费方：subagent。

P4a（010）升级：类型化事件总线（emit/waterfall/parallel/serial/bail）、inject 声明式依赖（拓扑序/缺键拒挂/循环报错）、effect 可逆注册（Dispose 逆序 undo/Mount 回滚）。

P4b 待办：loop 插件化（ctx.agentLoop）——当前 kernel 定位尚未覆盖，见 §3.4。

### 3.4 P4 目标架构（dsh 对齐，Go 复刻）

ctx.llm（Provider 接缝，默认 claude CLI 适配器插件）/ ctx.tools（effect 可逆注册表）/ ctx.agents（子代理）/ ctx.agentLoop（loop 本身是插件）/ ctx.sessions（镜像）/ profiles（standard/minimal）/ 入口即插件（HTTP/SSE）。治理三层保留为 mulix 差异化插件。

目标态 vs 过渡态：本节描述 P4 完成后的目标态；P4 完成前为过渡态，§3.3 的 kernel 即过渡形态。

## 4. 技术方案

命令

```bash
go build ./...              # 构建
go test ./... -count=1      # 全量测试（每任务必须全绿）
go vet ./...                # 兜底静态检查
gofmt -l .                  # 格式检查（提交前置，必须干净）
make test                   # 等价于 go test
make lint                   # golangci-lint（本机工具链不匹配，不可用）
make cross                  # 三平台交叉编译
```

环境限制：Go 1.27；零第三方运行时依赖（治理红线）；golangci-lint 因工具链不匹配在本机不可用——以 go vet ./... + gofmt -l 兜底；-race 因 CGO_ENABLED=0 不可用，如实记录。

引擎治理

engine.lock（cli_version/pinned_at/baseline）；漂移默认拒绝启动，放行留痕；scripts/check-engine-lock.sh 门禁。

测试纪律

TDD 强制（先红后绿）；每任务 go test ./... -count=1 全绿再 commit。fixture 约定：jsonl 无空行逐行可解码；超限/半行隔离为损坏段。

镜像与恢复

events.jsonl append-only；损坏段（半行/超限行）隔离转储（.damaged），好行照读；sessionx 压实原地重写；memory isolate tmp+rename（Sync 先行）。

## 5. 上下文压缩规范（长会话纪律）

保留（压缩后必须可重建）：

mulix state show 输出（change/phase/track/verify_result）与当前 tasks.md 任务序号

SDD ledger（.mulix/.runtime/<change>/sdd/tasks/progress.md）的 Ruling 行与 Task N: complete 行——trust the ledger and git log over your own recollection

本文件 §2 红线清单与 §3.2 分层矩阵

当前任务的 Interfaces 块（Produces/Consumes 签名）与未决 Review Focus 项

用户的最新裁定原文（产品决定不可凭记忆转述）

清除/不驻留（日志与搜索内容）：

冒烟/复测的完整 stdout、测试长输出——落文件（工作区/evidence 文档），上下文只留 tail 与关键行

WebSearch 的搜索过程与来源列表——结论入 spec/报告，来源只在最终答复引用一次

已 GREEN 任务的逐步骤输出（task-done 已落日志文件）

多轮评审往返的历史——评审结论以「Status + Issue 列表」一行摘要入 ledger

临时探针/probe 文件——用完即删

压缩实践：长输出一律重定向到 .mulix/.runtime/<change>/sdd/tasks/*.log 后读 tail；证据写 docs/evidence/*.md（文件是记忆，上下文是工作集）；跨任务恢复时先 mulix state show + 读 ledger，再 git log --oneline 对账。

## 6. 开发规范

mulix 流程强制（详见 AGENTS.md）：任何工作前 mulix state show；阶段技能先加载；守卫失败是阻塞指令，不得绕过/不用 --force；产物只写当前阶段允许路径。

TDD：先写失败测试并亲眼看它失败（编译期 RED 须在台账记 Ruling 说明形态）；最小实现；每任务以 task-done 落 RED/GREEN 证据。

计划评审：tasks.md 必须经独立评审员（general-purpose，高级模型）对照真实代码核验后方可请用户审批；评审 Issue 全部修复闭环。

终审：每个变更 build 结束后必 dispatch 独立终审（不因「改动小」跳过）；Critical/Important → 一轮 TDD 修复；Minor → ledger deferred，不扩散。

提交：conventional 前缀（fix/test/docs/chore/style）；只 add 本任务文件；gofmt 干净是提交前置；.mulix/.runtime/*/state.yaml 不入提交。

真实冒烟：证据落 docs/evidence/*.md（引擎版本/工作区/时间线/退出码/摘录）；环境受阻按 FR-108 惯例如实记录，不得以自动化测试冒充。

台账纪律：每个 Ruling 记「决定 — 理由 — 错了代价」；deferred minor 记一行并指明路由；终审 set-aside 项由 executor 逐条裁定入账。

## 7. 角色与职责

| 角色 | 职责 | 纪律 |
|---|---|---|
| 用户（产品/裁定权） | 阶段范围与产品裁定（如 T8-② 裁定 A、计数口径）；每个变更的 spec/tasks/归档显式审批；archive 选项（A/B/C）决定推送 | 裁定一经给出即为 spec 事实；agent 不得代为裁定 |
| 编排者（orchestrator，主会话 agent） | 走 mulix 全流程（阶段技能先加载）；写 spec/tasks；执行 inline 或派发 subagent；分组/判定冲突；维护 ledger 与台账 | 设计呈现后等用户批准再记 approved；deviation 必须 Ruling 入账，不秘密决定；四类停机问题（不可逆/安全/副作用越界/计划全坏）才停 |
| 计划评审员（plan reviewer，独立 subagent） | 对 tasks.md 对照真实代码核验（锚点/签名/RED 预期/可编译性）；只报会导致实现真实问题的 Issue | Issue 全部修复后才可 Approved；建议 advisory 不阻塞但由编排者斟酌采纳 |
| 实现者（implementer） | 按 brief 逐步 TDD；每个 Expected 与真实输出对比；偏差记 Ruling；commit + ledger 同调用完成 | 不看计划自作主张；测试没看失败不算数 |
| 终审员（final reviewer，独立 subagent，最强模型） | 全分支对照 spec 的正确性审查；按 effect 分级；Declined-to-judge 清单逐条交回 executor | 只读；不重审已由覆盖测试回答的项；Minor 不进修复轮 |
| 修复者（fix executor） | 终审 Critical/Important 的一轮修复（每项 RED→GREEN+全量） | Minor 不进修复轮；无失败测试的修复不算修复 |
| 归档决策（用户） | archive_confirmation（A=归档+push / B=仅归档 / C=保持） | guard 拦截即补正，不 --force |

## 附：历史阶段（指针）

历史分期、归档记录与产品裁定（2026-10-11）的原文，见 AGENTS.md §历史阶段 与 .mulix/memory/history.md。产品裁定不可凭记忆转述，须读原文。
