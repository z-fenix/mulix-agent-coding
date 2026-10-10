# 002-p2 Verify Report

- 日期：2026-10-08（verify 阶段，控制器新鲜运行）
- 被验对象：HEAD `d9218cc`（P2 全链 c710523..d9218cc，22 提交；12 任务全部 `Task N: complete`，build-complete 防护已通过）
- 方法：`verification-before-completion` 纪律——本报告全部结果为 verify 阶段本会话实测输出，不引用 build 阶段旧结果；逐条需求对照 `docs/specs/002-p2/spec.md`（FR-101..113 / SC-001..006）与已批准设计（`.mulix/.runtime/002-p2/specs/2026-10-08-p2-ecosystem-design.md`，track=architectural approved）。

## 1. 命令与结果（本会话实测）

| 命令 | 结果 | exit |
|---|---|---|
| `go test ./... -count=1` | 21 包：19 ok + 2 无测试文件（cmd/mulix、internal/textutil）；零 FAIL | 0 |
| `go vet ./...` | 干净 | 0 |
| `gofmt -l internal cmd` | 空 | 0 |
| `go run ./cmd/mulix lint layers` | `分层检查通过：0 项违规` | 0 |
| `GOOS=windows GOARCH=amd64 go build ./...` | OK | 0 |
| `GOOS=linux GOARCH=amd64 go build ./...` | OK | 0 |
| `GOOS=darwin GOARCH=amd64 go build ./...` | OK | 0 |

全仓 -race 由 CI race job 承担（本机 CGO_ENABLED=0 无 cgo——build 阶段已如实记录，属环境限制非代码缺陷）。

## 2. 逐条需求核对（FR-101..113）

证据为终树真实测试（全部经 build 阶段评审 grep 核验存在 + 本会话全量套件通过；SC 映射另见 `docs/evidence/p2-acceptance.md`——57/57 引用测试名零伪造）。

| FR | 判定 | 证据（测试/钉） |
|---|---|---|
| FR-101 子代理委派、独立镜像、同一治理回路 | ✅ | TestSubagentWiringEndToEnd（depth 链走真桥/镜像/Manager 路径）；回调桥镜像 P1 permissionCallbackServer（controller.go:339-442 对照，T3 评审核实）；审批 fail-closed（无人值守 + TestConfigUnmatchedToolDeniesUnderUnattended） |
| FR-102 结果回收（最终消息/退出原因/usage + 委派链） | ✅ | e2e ①②（DelegationCompleted summary + usage>0；meta.parent_id 链）；Manager.Summary 访问器四分支测试；poll 带 summary（e2e 钉） |
| FR-103 深度上限 3 / 并发上限 4 / 明确错误 + 留痕 | ✅ | TestSpawnDepthLimit（第四层拒含 "depth"）；TestSpawnConcurrentLimit（屏障 10 并发 → 4+6，pending 预约修 TOCTOU）；TestSpawnRejectsSelfParentCycle；拒绝面留痕=引擎 transcript + 审批审计（终末评审裁定属已批准设计口径，见 §6-5） |
| FR-104 取消收口无孤儿回合、usage 仍入账 | ✅ | TestSubagentWiringEndToEnd 三路径（单层 cancel / 三层级联 / 父 Close reason=manager_closed）+ assertNoOrphanTurns（TurnStarted↔TurnDone 1:1、无交错、EngineExited、恢复门放行） |
| FR-105 记忆增删查 + 回合尾低权限注入块 | ✅ | memory 15 测（JSONL 损坏管线三态 + BM25 手工验算）；memory_save/list/delete 工具；TestMemoryRecallInjectedAtTurnTail（`[记忆召回]` 块逐字、TurnTail len==1、k=3/budget=2000、镜像 NoticeEvent）；withMemoryRecall 唯一调用点（controller.go:846，lint 钉） |
| FR-106 默认关、零开销、与 P1 一致；开启有预算 | ✅ | TestMemoryRecallDisabledZeroDelta（零注入 + boot face 逐字节 + 独立重算 cache.Compose 基线非同义反复）；字节护栏 6 测（TestBootComposeByteStable 等）；预算截断钉 |
| FR-107 外部 MCP 声明、命名空间、冻结、复用治理 | ✅ | Compose 矩阵 8 测（形状/保留名 TestComposeRejectsReservedMulixName/互重名/空 command fail-closed）；两次启动清单逐字节一致（冻结钉）；`mcp__server__tool` 命名空间；P1 权限/记账复用（e2e approval 面） |
| FR-108 超时默认 60s 可按 server 覆盖；崩溃隔离 | ✅ | TestComposeMCPToolTimeoutRespectsLargeDeclaredValues（120000 单 server 用声明值 + 90000+120000 组 min——haveTimeout 哨兵防静默钳制）；超时/崩溃降级测试（引擎原生 R1 + 透传钉）；冒烟 B echo stdio 往返成功 |
| FR-109 指令变更提醒（不重载不中断）+ 管理命令 | ✅ | watcher 矩阵 6 测（内容变更一次提醒/删除墓碑/touch 静默 TestWatchInstructionsTouchNoNotice（os.Chtimes 2h + 10 tick 全静默 + sha256 比较）/Close 停止/启动接线）；`mulix instructions` 3 测（sha256/摘要/预览） |
| FR-110 分层检查（导入规则/棘轮/注入点唯一/非零退出/CI） | ✅ | lint 15 测 + cli 4 测（exit 1 钉 TestLintLayersViolationExitsNonZero；棘轮增减矩阵；system-prompt-path 双 marker + turn-tail 引用扫描——design §6 两路扫描）；CI 步骤（ci.yml:40-41）；违规注入复现 RED（`t.go:3 [layer-import]` exit 1，任务 12 证据）+ 自食其力 0 违规 |
| FR-111 自定义钩子（时点 + 脚本、启动校验、stdin JSON、审计） | ✅ | guard hooks_custom 9 测（argv 逐字 re-exec 权威断言含全元字符清单 / JSON 应答 / 扇出）；非法声明 fail-closed 拒启（session.go:200-207）；构造期挂载先于 NewSession 冻结（controller.go:211-229） |
| FR-112 钩子超时 + 崩溃隔离、降级 + 告警 | ✅ | TestScriptConsumerTimeoutDegrades / CrashDegrades（双 errors.Is 分类、Windows kill→ExitError）；缺省 10000ms 双钉（值钉 + 80ms 收缩生效实测）；OnWarning→NoticeEvent 镜像先行（TestHookWiringTimeoutDegradesWithMirrorNotice 经 store.Replay() 钉） |
| FR-113 全部新事件遵循事件红线（先持久化后发布、1:1） | ✅ | codec 15 kinds 往返钉（replay_test.go:283-311）；委派四事件经 parent c.publish 镜像先行（controller.go:297）；钩子降级告警经 Replay 镜像面；崩溃恢复 e2e 三钉（TestE2ECrashRecoveryResume / TestE2EDenyFailClosed / TestE2EHappyTurnWithApproval）；冒烟 A2 真实子镜像 5 事件 1:1 收口 |

设计符合性（要点）：design §5.3 分层基准表已编码于 internal/lint/rules.go 且经终末评审独立 grep 核实=实际 import 图；§6 注入点唯一两路扫描已编码；「功能未配置=零痕迹」经 6 项字节护栏双钉（disabled boot face 对独立 cache.Compose 基线）。

## 3. 成功标准核对（SC-001..006）

| SC | 判定 | 证据与边界 |
|---|---|---|
| SC-001 真实仓库一次委派完成多步任务、结果与 usage 回收、全程可审计 | ✅（自动化面完整；真实引擎面部分——诚实限制 §6-2） | 自动化：TestSubagentWiringEndToEnd 全链（读改测提交 + poll 回收 + 恢复门）。真实引擎冒烟 A2：mcp__mulix__mulix_spawn → policy allow → 真实子镜像（parent_id + delegation_started depth 0）→ 多步任务真实完成 git commit `9f71090`（python 断言过）→ 流断后级联收口无孤儿回合。披露：多步工作由主引擎会话内建 Agent 子代理完成，mulix spawn 桥子会话因 2.1.296 流断未跑完自身回合（p2-smoke.md A2 + 验收文档均已注明） |
| SC-002 开启时召回入回合尾且配置面逐字节不变；关闭与 P1 一致 | ✅ | 冒烟 B（真实引擎）：`[notice] 记忆召回: 2 条命中` + 引擎 transcript `[记忆召回]` turn-tail 逐字 + Boot 面不含该内容；护栏 6 测 + disabled 零增量钉 |
| SC-003 分层检查 CI 执行：注入违规复现非零退出与位置 | ✅ | CI 步骤入库（ci.yml:40-41；本地无 GH Actions，以本地等价命令 + 真实二进制注入复现代替：RED `t.go:3 [layer-import]` exit 1 / GREEN revert 0 违规——任务 12 报告 RED:/GREEN: 证据） |
| SC-004 外部工具回合内成功调用；超时/崩溃不悬挂 | ✅ | 冒烟 B：echo stdio 往返引擎侧成功（transcript 原文）→ 随后回合被 2.1.296 截断但 exit 1 非悬挂（「不悬挂」半边由真实行为本身验证）；超时/崩溃矩阵自动化钉死（FR-108 行） |
| SC-005 会话中修改 CLAUDE.md 产生提醒且继续用启动快照 | ✅（提醒事件面自动化钉；真实会话提醒为复测项 §6-3） | 冒烟 C：变更后引擎读盘回显 5 行全文、会话未中断、exit 0（快照语义与 P1 一致）；提醒矩阵 6 测钉死；真实会话 notice 未赶上窗口=T12-⑤ 复测项（机制未定论，见 §6-3） |
| SC-006 全部新事件类型镜像可审计且 1:1（崩溃恢复覆盖子代理） | ✅ | 委派链（冒烟 A2 真实子镜像 5 事件 + e2e 对账钉）；召回注入（冒烟 B 镜像 notice seq=2 + TestMemoryRecallInjectedAtTurnTail 镜像断言）；外部工具（冒烟 B approval_request/resolved + tool 原文入镜像）；自定义钩子（修复轮补齐：guard 5 测 + cli 3 测，镜像面经 Replay 钉——hook 事件复用 P1 notice kind，1:1 机制共享，event.go 无专用 kind 已诚实注记）；崩溃恢复 e2e 三钉 |

## 4. 裁定复核（台账 Rulings 对照规格）

**Preflight 裁定：**

| 裁定 | 内容 | 对照规格判定 |
|---|---|---|
| R-P2-1 | 直接在 master 实施 | **成立**——22 提交 master-direct 无事故，全部评审门通过；规格无分支要求 |
| R-P2-2 | harness 子代理无 model 参数，以会话默认模型执行 | **成立**——评审层多次抓出真实缺陷（T2×2 Important、T7 Important-1、T12 SC-006 过度声明），质量代价未兑现 |
| R-P2-3（修订） | 同步派发废止 → 后台模式 | **成立**——两次取消均发生于 sync 模式；后台模式全程（T2 起）零取消零停滞 |
| R-P2-4 | T3 窄接口 + control 侧适配器 | **成立**——适配器经 T3/T4 评审核实；e2e depth 链走真桥证明接口缝正确 |
| R-P2-5 | 三个 bash 脚本可用 | **成立**——12 任务 brief/评审包/工作区脚本全部正常 |
| R-P2-6 | cordis 内核模式（用户指令：引擎宿主=provider 插件，claude code 只是项目下的一个插件） | **成立**——kernel 仅 stdlib 为 leaf 级、引擎为 HostFactory 插件、lint 矩阵编码分层；FR/SC 扫查无缺口，design §2.1/§3.0 修订（58d720b）已批准 |

**任务级关键裁定（全部维持，明细在台账各任务节）：**

- T3 三裁定（Summary 访问器路由 T4 / disabled 断言走 boot face / cancel reason 固定串）——**成立**：Summary 由 T4 交付且 e2e 钉死；boot face 断言成为 T6/T7 字节稳定护栏的标准形态；固定串 e2e 核实。
- T6 C1（serve 侧 subagent 注册路由修复轮）——**成立**：env 门控 + re-exec 双分支钉 + TestServeToolFaceComposition 全/缺席两面。C2（跨 serve 碰撞/召回新鲜度=会话起始）——**成立为 P2 限制**（规格只承诺后续会话；零依赖约束排除跨进程锁）。C3（计划缺陷 3a35008 修正）——**成立**：lint 矩阵=实际 import 图经终末评审独立核实。
- T7 R-T7-1'（无外部 server 时 EngineEnv=nil，P1 零增量）——**成立**：nil 逐字钉 + composeBoot 接线零增量钉。R-T7-4（command+args 分立形状）——**成立**：P1 形状保留。
- T8 R-T8-1..4（路径键 / watcher 基线去重 / 读错误双口径 / instructions fresh-load）——**成立**：单写点证明 + 删除重建恢复会提醒 + fresh-load 真实性核实。
- T9 R-1..4（超时/崩溃统一降级 / stdout 不做决策解码 / 构造期校验 / 10000ms 缺省）——**成立**：P1 失败模型对照核实；R-3 接线缺口经修复轮 debbbcb 收口且 re-review 核实 OnWarning 偏差 SATISFIES RULING INTENT。
- T10 R-T10-1..5（mulixusage 现况登记 / _test.go 不扫 / repolint allowlist +2 程序合规 / 未登记 fail-closed / 规则名映射）——**全部 VERIFIED**（T10 评审：allowlist↔marker 全仓 grep 双向精确；终末评审复核矩阵=import 图）。
- T11 N4 等价扩展（单飞 close 广播超出 brief 字面）——**成立（REQUIRED-BY-MATRIX）**：缓冲 send 语义使 brief 自带 20-goroutine 矩阵不可满足；终末评审确认新形状 exactly-once 且服务双入口路径。
- T12 Minor-5 留置（watcher 真实会话复测）——**成立**：时间戳矛盾持久记录；并入 P3 引擎重钉后复测。

## 5. Deferred minors 处置

台账 ~55 条 deferred minor 经终末全分支评审三分类（明细见台账 Final whole-branch review 节）：

- **(a) must-fix-now：零**——无阻塞代码缺陷。
- **(b) P3 路由**（优先序）：①2.1.296 解码器两形状容忍 + engine.lock 重钉 + fixture + 真实复冒烟 ②T11-① 单飞 leader panic 悬挂 + resolved 无界增长（defer 式清理）③T5-① >16MB 行 Open 硬失败（隔离为损坏段）④T12-⑤ watcher 复测 + p2-smoke.md 限制 #3 修正 ⑤T12-⑥ SC-006 计数 4+3→5+3 ⑥T10-① 解析器注释边缘 ⑦T10-② allowlist 漂移交叉断言 ⑧T5-⑤ fsync ⑧T8-① 等 iw.Done ⑨T8-② 启动后新建文件监控（FR-109 后半产品裁定）及廉价测试补强清单。
- **(c) 接受现状**：T2-③④⑤⑥⑦⑧ / T3-②④⑤ / T4-②③④⑤ / T5-⑦ / T6-⑤⑥ / T7-envContains / T8-③④⑤⑥⑦ / T9-②④⑥⑦ / T10-③④ / T11-②③（理由均在台账）。
- **在飞已解决**：T3-①（T4 交付 depth 来源）、T6-③（TestServeToolFaceComposition 缺席分支观测）。

## 6. 诚实限制

1. **引擎 2.1.296 漂移（P3 第一项）**：本机 claude CLI 2.1.296 ≠ 主仓 engine.lock pin 2.1.291；2.1.296 的 tool_result user message `.content` 由字符串改为块数组 → mulix 解码器（events.go ToolResultBlock.Content string）整流 fail-closed。触发=MCP 工具成功结果回流（spawn 确认截断；echo 往返引擎侧成功后回合截断）；不触发=内置 Read 回流、审批拒绝回流。行为面 fail-closed 正确且冒烟实况验证（回合 error 收口 → 子代理级联 manager_closed → exit 1 非悬挂，1:1 完好）。**verify 判定不受阻**：全部门禁在 pin 代码上绿；主仓 pin 2.1.291 使 doctor 先行挡漂移启动（第二层 fail-closed）；临时缓解=本机 CLI 降回 2.1.291。
2. **SC-001 真实引擎面部分**：多步工作由主引擎会话内建 Agent 子代理完成；mulix spawn 桥子会话因流断未跑完自身回合——回收/恢复链由自动化 TestSubagentWiringEndToEnd 完整钉死。
3. **SC-005 真实会话提醒复测项（T12-⑤）**：watcher 真实会话 notice 未被观测到；机制未定论（时间戳复检显示原解释与 D 轮时间线矛盾——编辑时刻早于引擎收消息 ~30s，watcher 本应命中）；矩阵 6 测已钉，复测并入 P3①重钉后执行，随后修正 p2-smoke.md 限制 #3。
4. `/agents` 真实 TTY 面板交互同 P1 限制（自动化钉 TUI 构造面）。
5. FR-103 拒绝留痕落在引擎 transcript + 审批审计而非专用镜像事件——终末评审裁定属已批准设计口径（非缺口）。
6. CI 面本地无 GH Actions——lint CI 步骤已入库，本地等价命令 + 注入复现代替验证。

## 7. 结论

全量门禁（测试/vet/gofmt/分层 lint/三平台交叉编译）本会话实测全绿；FR-101..113 逐条核对全部 ✅（每条引用终树真实测试）；SC-001..006 全部满足（SC-001/SC-005 带如实划界的真实引擎面限制，均已路由 P3 且不阻塞）；全部裁定经复核成立；deferred minors 零阻塞。

**verify_result = pass**（记录于 mulix state；archive 阶段待用户显式决定）。
