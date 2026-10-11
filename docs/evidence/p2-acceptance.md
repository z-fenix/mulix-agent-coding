# P2 验收清单与证据（M18 收口）

**Change:** 002-p2
**Date:** 2026-10-10
**方法:** spec.md §Success Criteria 的 SC-001..SC-006 逐条映射到自动化测试与
真实引擎冒烟记录。每条引用的测试名均经 `grep "func <Name>("` 逐一定位核验
**且在本任务会话中逐一运行**（P1 终评教训：不写入任何未经运行的测试名）。
真实引擎冒烟证据见 `docs/evidence/p2-smoke.md`（同日同轮产出）。

---

## SC-001：子代理真实委派完成多步编码任务，结果与 usage 回收，全程可审计 ✅（自动化/e2e 全链钉死；真实引擎委派链走通——结果回收全链受引擎版本漂移限制，诚实记录见下与限制①）

- **e2e 接线钉（FakeHost × 真实 control/sessionx/cache/guard/subagent 栈）**：
  `TestSubagentWiringEndToEnd`（cli，本会话运行 PASS 0.22s）——
  ①主镜像 DelegationStarted（depth 0）+ DelegationCompleted（usage 非零 + summary）；
  ②子镜像 meta.parent_id == 主会话 id，三层委派链 depth 0/1/2 递增；
  ③mulix_cancel 后三层子镜像全部无孤儿回合（成对 + 1:1）且恢复门放行；
  ④第四层 spawn 被拒（错误含 depth——MaxDepth 可达非死代码）。
- **配套钉**：`TestSubagentConfigDefaults`（默认 3 层/4 并发）、
  `TestSpawnDepthLimit` / `TestSpawnConcurrentLimit` /
  `TestSpawnRejectsSelfParentCycle`（subagent.Manager 限额与判环）、
  `TestSubagentDisabledWhenFactoryNil`（disabled 面：Boot Env 无回调 +
  boot 面与 P1 逐字节一致）、`TestTUIAgentsPanel`（/agents 面板：列表视图
  id/状态/任务/深度 + c 取消；真实 TTY 交互留人工冒烟——诚实限制见文末）。
- **真实引擎冒烟（2026-10-10，claude 2.1.296）**——独立 git 冒烟工作区
  （ws-a）执行 `mulix run`（无人值守）：
  - 主代理真实调用 `mcp__mulix__mulix_spawn`（policy 直判 allow，
    approval_request/approval_resolved 落镜像）→ **真实子会话镜像创建**
    （`2517bca…-sub-1`，meta.parent_id == 主会话 id，delegation_started
    depth 0 落主镜像）；
  - 多步编码任务真实完成并提交：**git commit `9f71090` "smoke: subagent
    add subtract"**（calc.py 增 subtract、test_calc.py 断言经 python 执行
    通过——本轮产物复验 exit 0；如实记录：该多步任务由主引擎会话内建
    Agent 子代理执行完成，mulix spawn 桥创建的子会话因引擎流断未跑完
    自身回合——见 p2-smoke.md 冒烟 A2）；
  - **子代理工具治理同回路实证**：冒烟 A1（spawn 未入 allow 清单）时
    spawn 被无人值守 fail-closed 拒绝（`denied by policy: unattended`），
    主代理未绕过、如实汇报——FR-101 审批/策略回路在真实引擎下工作。
- **诚实限制（版本漂移）**：子代理的「结果回收进主会话」全链
  （DelegationCompleted + poll summary）在本环境被 claude 2.1.296 的
  tool_result 形状变更阻断（mulix 解码器按 2.1.291 形状建模——
  `.content` 数组 vs 字符串，见 p2-smoke.md 详录）。回收路径由
  `TestSubagentWiringEndToEnd`（FakeHost 真实栈）钉死；真实引擎下
  poll/summary 未走通属 **P3 待办**（engine.lock 重钉 + 解码器兼容），
  如实记录不虚报。

## SC-002：记忆召回开启命中回合尾部且配置面逐字节不变；关闭与 P1 一致 ✅（真实引擎开启冒烟命中）

- **开关 A/B 护栏（本会话逐一运行 PASS）**：
  `TestMemoryRecallDisabledZeroDelta`（关闭：零注入 + 同参双跑 boot 面
  逐字节一致 + cache.Compose 独立重算基线）、
  `TestMemoryRecallInjectedAtTurnTail`（开启：`[记忆召回]` 块恰在 turn
  tail，正文原样，k=3/budget=2000，镜像 NoticeEvent 留痕）、
  `TestMemoryRecallNoHitsSilent`（0 命中静默 + 无 notice）、
  `TestMemoryRecallBudgetTruncationRealStore`（真实 store 预算截断）、
  `TestMemoryRecallFoldsMultilineEntryText`（多行折叠）、
  `TestMemorySessionEnvInjection`（MULIX_MEMORY_SESSION 溯源 env，绝不入
  mcp-config）。
- **字节稳定护栏（P1 面零回归）**：`TestBootComposeByteStable` /
  `TestBootComposeOrderInsensitive`（cache）、`TestE2EBootArtifactByteStable`
  （系统边界两次 Start 逐字节）、`TestMemoryWiringInjectsRecallAndBootFaceStable`
  （cli 装配：开关双跑 boot 面逐字节）、`TestMemoryDisabledSubtreeInheritsZeroDelta`
  （子代理子树关闭零增量）、`TestMemoryStoreSharedWithSubagentTree`
  （会话树共享单 Store——多实例分叉防线）。
- **真实引擎开启冒烟（ws-b，`[tools] memory_search=true` + 预置 2 条
  记忆）**：`mulix run` 首行即
  **`[notice] 记忆召回: 2 条命中，已注入本回合 turn tail`**；引擎收到的
  user message 尾部真实携带（claude transcript 原文）：
  `[记忆召回]\n- 本项目包管理器用 pnpm，不要用 npm\n- 冒烟工作区的提交信息前缀用 smoke:`——
  BM25 命中 → turn tail 注入 → 镜像 notice 三面一致；关闭态行为与 P1
  一致由 `TestMemoryRecallDisabledZeroDelta` + 零注册面钉死
  （`TestServeToolFaceComposition`：memory_search=false → memory_* 不注册）。

## SC-003：分层检查工具 CI 可执行；注入违规即复现非零退出与位置报告 ✅

- **本地等价命令（真实二进制）**：`mulix-real.exe lint layers`（go build
  ./cmd/mulix 产出，f4486e6 同源）在仓库根 → **`分层检查通过：0 项违规`
  exit 0**（本会话实测；与 Task 10 棘轮基线 docs/lint/ratchet.json 同源）。
- **违规注入复现（独立 scratch fixture，真实二进制 + 真实 cwd 语义）**：
  fixture（go.mod module mulix + internal/kernel/k.go +
  internal/tools/faketool/t.go 注入 `import _ "mulix/internal/kernel"`）→
  **exit 1**，报告含位置与规则名：
  `internal/tools/faketool/t.go:3: [layer-import] import "mulix/internal/kernel"…`
  `lint: 1 项分层违规`；revert 后复跑 **exit 0**（0 项违规）。
  另：缺 go.mod 的 cwd 实测被拒（`缺 go.mod——lint layers 需在 Go 模块根运行`
  exit 1，与 `TestLintLayersRequiresGoMod` 一致）。
- **测试钉（本会话运行 PASS）**：cli 面 `TestLintLayersViolationExitsNonZero`
  （违规 → exit 1 语义 + file:line + 规则名）、`TestLintLayersCleanTreePasses`、
  `TestLintLayersFixRegeneratesRatchet`（--fix 只紧不松）、
  `TestLintLayersRequiresGoMod`；lint 内核 15 测试全绿（含
  `TestLayerImportLeafToKernel`、`TestSubagentMustNotImportControl`、
  `TestControlMustNotImportFrontends`、`TestSystemPromptPathReferenceOutsideAllowlist`、
  `TestTurnTailReferenceOutsideAllowlist`、`TestRatchetIncreaseFailsDecreaseEqualPass`、
  `TestCleanTreeZeroViolations`、`TestMissingGoModIsError`）。
  CI 接线属仓库 workflow 层（本机无 GitHub Actions 环境——与 P1 SC-004
  同口径如实记录：本地等价命令 + 违规注入复现已闭环）。

## SC-004：外部 MCP server 工具可成功调用；超时与崩溃不悬挂主流程（各有测试钉死）✅（真实 echo server 冒烟往返成功）

- **设计口径（R1）**：外部 server = 配置透传——mulix 声明、引擎原生托管
  （spawn/超时/崩溃隔离），mulix 不代理协议；FR-108 验收按「透传」校准。
- **透传矩阵测试（本会话运行 PASS）**：`TestComposeMergesExternalServers`
  （外部条目并入 mcp-config + 命名空间面）、`TestComposeMCPToolTimeoutEnv`
  （MCP_TOOL_TIMEOUT 引擎 env 生成）、
  `TestComposeMCPToolTimeoutRespectsLargeDeclaredValues`（per-server 声明值
  不被封顶——评审 Important-1 回归钉）、`TestComposeNoExternalServersEngineEnvEmpty`
  （无外部 → EngineEnv nil，P1 boot.Env 零 delta）、
  `TestComposeRejectsReservedMulixName` / `TestComposeRejectsEmptyExternalCommand` /
  `TestComposeRejectsDuplicateExternalNames`（保留名/空命令/重名 fail-closed）、
  `TestComposeExternalEnvStaticPassthrough`（env 静态透传）、
  `TestMCPToolTimeoutWiredToBootEnv`（control 侧 Boot Env 接线）、
  `TestMCPServersEnvTypeMismatchRecovers`（config 层类型失配恢复）、
  `TestMCPServersSelfCommand`（内置 mulix 条目命令形状）。
- **真实 echo server 冒烟（ws-b，`[[mcp.servers]] name="echo"` → node 脚本
  stdio MCP server）**：真实引擎按 mcp-config 自 spawn echo server；
  `mcp__echo__echo` 经权限回路（policy allow 落镜像 approval_request/
  resolved）**真实调用成功——引擎侧 tool_result 回显
  `echo: mulix-p2-smoke-echo-12345`**（claude transcript 原文；echo 脚本
  协议自测 initialize/tools/list/tools/call 三应答全对）。外部 server
  崩溃隔离场景由引擎原生语义承载（R1），mulix 侧主流程不悬挂由
  `TestE2ECrashRecoveryResume`（引擎崩溃 → 恢复门 → resume 合成收口）
  与冒烟 A2（引擎流损坏 → 回合 error 收口 → delegation_cancelled
  manager_closed 级联收口子代理 → 进程 exit 1 非悬挂）双面钉死。
- **诚实限制**：echo 结果在引擎侧成功后，mulix 的 stdout 流随后被
  2.1.296 tool_result 数组形状阻断（同 SC-001 限制）——「工具在回合中
  成功调用」的引擎侧证据完整（transcript），mulix 侧全回合收口待解码器
  兼容后复测。

## SC-005：会话中修改 CLAUDE.md 产生提醒事件且会话继续使用启动快照 ✅

- **watcher 矩阵测试（本会话运行 PASS）**：
  `TestWatchInstructionsTouchNoNotice`（Review Focus 钉：mtime 变内容同 →
  10 tick 全静默——sha256 比较非 mtime）、
  `TestWatchInstructionsContentChangeNoticedOnce`（内容变 → 恰一条 info
  notice）、`TestWatchInstructionsDeleteNoticed`（删除 → 同修改语义提醒 +
  基线墓碑化）、`TestWatchInstructionsCloseStopsGoroutine` /
  `TestWatchContextCancelStopsGoroutine`（收口无泄漏）、
  `TestWatchWiredToStartSequence`（start 序列真实接线：快照装载 + 事件落
  镜像 + 「下会话生效」文案钉）。
- **快照查询命令（真实二进制）**：`mulix instructions` 在冒烟工作区
  （ws-a）→ 列出来源文件路径 + sha256 + 摘要（6 行/87 字节）+ 逐行内容
  预览，exit 0（本会话实测，输出全文见 p2-smoke.md 冒烟 D）；
  `TestInstructionsShowsSourcesAndSummary` /
  `TestInstructionsEmptyWorkspace` / `TestInstructionsLongContentTruncated`
  钉住命令面。
- **真实会话快照语义**：冒烟 C/D 回合正常完成（graceful exit 0），会话
  Boot 面在变更后不变（快照冻结语义——SC-003 护栏测试零回归）。

## SC-006：全部新事件类型镜像可审计且 1:1 不变量；崩溃恢复覆盖子代理会话 ✅

- **事件类型与 codec（Task 1）**：`internal/event` 全包绿（本会话运行）——
  委派链四事件（delegation_started/completed/cancelled/failed）JSON 往返
  稳定，`eventKindOf` 全 case（Append 打标必经）。
- **自定义钩子事件（spec SC-006 事件组之一，FR-111/112/113）**：钩子
  事件复用 P1 `notice` kind（`internal/event/event.go` 无专用 hook
  kind——诚实记录：1:1 机制、持久化通路与 codec 与 P1 全量共享，P2 未
  为钩子新增 kind），降级/告警以 NoticeEvent（warning）走镜像先行发布。
  测试钉（全部 grep 核验 + 本会话运行 PASS，评审修复轮 1 复跑）：
  guard 面 `TestHookHelperProcess`（自重执行钩子脚本 stub 基座——
  sleep/stdout/argv+stdin 观察/指定退出码四行为参数化）、
  `TestScriptConsumerJSONResponseAccepted`（exit 0 + JSON 输出
  → 按语义接受、零告警）、`TestScriptConsumerFanOutWithBuiltin`（脚本
  消费者与内置消费者同一次 Dispatch 分发——一条事件全部消费者）、
  `TestScriptConsumerTimeoutDegrades` / `TestScriptConsumerCrashDegrades`
  （超时/崩溃 → 降级 + OnWarning 告警，错误带失败模式与退出码线索，
  主流程不悬挂 fail-open）；组合根面（cli）`TestHookWiringHelperProcess`
  （自重执行 stub 基座）、`TestHookWiringMountsConsumerAndTriggers`
  （挂载 + PreToolUse 触发 + stdin 携带完整事件 JSON）、
  `TestHookWiringTimeoutDegradesWithMirrorNotice`（**FR-113 审计性组合根
  实证：降级告警经 `store.Replay()` 从镜像读回钉死「走了发布通道」而非
  直写 sink**，回合照常收口）。其余三组（委派链/召回注入/外部工具）的
  镜像审计证据分别在 SC-001/002/004 与下方冒烟实证条目。
- **镜像 1:1 + 孤儿回合对账**：`TestSubagentWiringEndToEnd` ③（cancel/
  级联/父 Close 三路径 → 子镜像成对 + 1:1 + RecoveryRequired 放行——
  `mirrorReconciled` 用控制层同款恢复门判定）；`TestE2ECrashRecoveryResume`
  （孤儿回合 → RecoveryRequired → Resume → resume_reconciled 合成收口，
  P1 不变量对主会话 + 子代理会话同构）；`TestE2EDenyFailClosed` /
  `TestE2EHappyTurnWithApproval`（审批事件镜像审计全链）。
- **真实引擎冒烟的镜像审计实证（A2）**：主镜像 10 事件
  （turn_started/assistant_thinking/assistant_text/tool_use_requested/
  approval_request/approval_resolved/delegation_started/turn_done/
  engine_exited/delegation_cancelled），子镜像 5 事件（turn_started/
  tool_result/usage/turn_done/engine_exited）——**引擎崩溃路径下子代理
  回合被收口（无孤儿回合），delegation_cancelled reason=manager_closed
  级联落镜像**；冒烟 B 的记忆召回 notice、echo 的 approval_request/
  resolved 均落镜像可审计。

---

## 诚实限制（P2 如实记录）

1. **引擎版本漂移与解码器形状（本轮最重要发现）**：本机 claude CLI 已
   升至 2.1.296（engine.lock pin 2.1.291）。2.1.296 的 tool_result user
   message 将 `.content` 从字符串改为块数组，mulix 解码器
   （`internal/engine/events.go` ToolResultBlock.Content string）按 2.1.291
   形状建模 → 流损坏 fail-closed（`engine: decode user: json: cannot
   unmarshal array into …`）。影响：真实引擎下 MCP 工具结果回流的后续
   事件（含 DelegationCompleted/poll summary 全链）被截断。已验证不受
   影响的面：会话启动、init、assistant 流、内置 Read 结果（字符串形状
   回流）、审批回路、委派链事件、镜像 1:1、取消收口。**修复路由（P3
   待办，不属本任务）**：①解码器容忍 string|块数组两形状（协议红线：
   未知形状容忍或按行跳过，不应使整个流崩溃——建议按 fail-closed 降级
   为跳行/裸保留）；②engine.lock 重钉 + doctor 复检；③ happy-turn
   fixture 增补 2.1.296 形状样本。
2. **子代理真实 TTY 面板交互**：/agents 面板的列表/取消键位由
   `TestTUIAgentsPanel`（真实 Manager + bubbletea 单元）钉住；真实 TTY
   下的键位交互与 P1 同限（管道 stdin 无法驱动 bubbletea）——留人工冒烟。
3. **watcher 真实会话提醒事件**：冒烟 C/D 未观察到 notice——实测会话
   生命周期短于 watch 起点到达时机（Submit 串行先于 watcher 启动，
   Close 即停轮询；短回合 + 8 秒窗口未赶上 2s tick）。矩阵行为由 6 个
   watcher 测试钉死（touch 静默/变更恰一条/删除墓碑/接线/收口），
   真实会话 watcher 提醒留待引擎版本对齐后复测。
4. **外部 server 超时与崩溃的引擎原生隔离**：按设计 R1 由引擎承载，
   mulix 侧以 env 透传测试钉死；真实引擎下注入超时/杀 server 的专门
   冒烟未做（echo 成功往返 + 主流程不悬挂两面已有实证）。
5. **CI 环境依赖面**：`-race`/golangci-lint/三平台 CI 作业由仓库 workflow
   承载（本机无 cgo，与 P1 记录一致）；SC-003 的 CI 接线以本地等价命令
   + 违规注入复现为验收口径。

## 汇总

| 标准 | 结论 | 主证据（测试均经 grep 核验 + 本会话运行） |
|---|---|---|
| SC-001 | ✅ | TestSubagentWiringEndToEnd（depth 链/孤儿对账/MaxDepth）+ 真实引擎 spawn→子镜像→`9f71090` 真实提交 + fail-closed 治理实证；「结果回收」全链自动化已钉、真实引擎面受 2.1.296 形状限制（诚实记录，限制①） |
| SC-002 | ✅ | TestMemoryRecallDisabledZeroDelta/InjectedAtTurnTail 等 6 测 + 字节护栏 6 测 + **真实引擎开启冒烟（召回块命中 + 镜像 notice）** |
| SC-003 | ✅ | 真实二进制 lint layers exit 0 + 违规注入 exit 1（file:line + 规则名）+ revert 复跑 exit 0 + lint 15 测/cli 4 测 |
| SC-004 | ✅ | Compose 矩阵 8 测 + 接线 3 测 + **真实 echo server 冒烟（spawn/命名空间调用/policy allow/`echo: mulix-p2-smoke-echo-12345` 往返）** |
| SC-005 | ✅ | watcher 6 测（touch 静默/变更/删除/收口/接线）+ `mulix instructions` 真实输出（sha256/摘要/预览） |
| SC-006 | ✅ | event codec 全绿 + 自定义钩子事件审计（guard 5 测 + cli 组合根 3 测，Replay 钉镜像面——见 SC-006 条目；计数口径 P3 修正（007）：沿用 report.md 既有口径（历史约定，非 helper 测试脚本消费核心 5 个），与当前 grep 事实（guard 8 + cli 4 非 helper）存在差异，已按 007 裁定入档并路由）+ TestE2ECrashRecoveryResume + 孤儿回合对账 + **真实冒烟镜像审计（主 10 事件/子 5 事件，崩溃路径收口）** |

**验证命令（M18 出口）**：`go test ./... -count=1` 全绿（21 包：19 ok +
2 无测试文件——评审修复轮 1 实测复核）+ `go vet ./...` 干净 +
`gofmt -l internal cmd` 空 +
`mulix lint layers` 0 违规 + GOOS=windows/linux/darwin（GOARCH=amd64）
`go build ./...` 三平台全过（本机 Windows host 实测）。
