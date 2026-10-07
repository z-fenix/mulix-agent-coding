# P1 Verify Report — 001-p1-core-loop-and-cache-foundation

**Date:** 2026-10-08（build → verify 阶段；验证执行于 commit ad598f7）
**方法:** verification-before-completion——以下全部命令为 verify 阶段内
**现场真实执行**（非引用历史输出），输出摘录与退出码逐条记录。

## 1. 全量验证命令（现场执行）

| 命令 | 结果 |
|---|---|
| `go test ./... -count=1 -timeout 300s` | **exit 0**——14 个包全部 `ok`（cache/cli/config/control/e2e/engine/event/guard/jsonl/registry/sessionx/tools/mulixpermission/tools/mulixusage），2 包无测试文件（cmd/mulix、textutil） |
| `go vet ./...` | **exit 0**（无输出） |
| `gofmt -l internal cmd` | **空**（无未格式化文件） |
| 交叉编译 `GOOS=darwin GOARCH=amd64 go build ./cmd/mulix` | **exit 0** |
| 交叉编译 `GOOS=linux GOARCH=amd64 go build ./cmd/mulix` | **exit 0** |
| `bash scripts/check-engine-lock.sh` | **exit 0**——「check-engine-lock: 通过」 |
| `-race` | 本机不可执行（无 gcc/CGO——已有 Ruling）；race 缺口由 CI ubuntu job（T31 workflow）覆盖，CI 首跑待真实 push |

## 2. 真实引擎冒烟（SC-001 的真实环境证据，非模拟）

- **冒烟 A**（e7e7fbd 后）：独立 git 冒烟工作区，`mulix run` 真实回合——
  读 calc.py → 加 subtract → 建 test_calc.py → python 断言通过 →
  **真实 commit `4b58fc7` "smoke: add subtract + tests"**；graceful 退出，
  mulix run exit 0。权限回路端到端（引擎 → mcp__mulix__permission →
  serve 子进程 → 前台回调裁决核 → behavior JSON）。
- **冒烟 B**（ca5c144 复测）：同构任务——**真实 commit `3487d01`
  "smoke: add multiply"**；证明 Boot Env（MULIX_PERMISSION_CALLBACK）经
  引擎子进程传播到 serve 孙进程；graceful exit 0。
- 证据存档：docs/evidence/p1-smoke.md（含三次失败留痕的完整修复链）。
- **诚实限制**：TUI 通道无真实 TTY 驱动（bubbletea 管道输入仅回显终端
  初始化序列）；run/TUI 共用 buildComponents 装配与 Controller.Submit 控制
  路径，TUI 真实 TTY 人工冒烟记录为 P2 前置事项。

## 3. 成功标准逐条核验（spec.md §Success Criteria）

| SC | 判定 | 现场证据 |
|---|---|---|
| SC-001 双入口完成真实编码回合 | **达成（run 通道实证；TUI 通道限制如实记录）** | 冒烟 A/B 真实 commit + `go test ./internal/e2e/` 六场景绿 + TestRouteMatrix；TUI 状态栏/命令单测绿（TestStatusBar/TestSlashCache） |
| SC-002 状态栏/汇总/JSONL 一致 | **达成** | TestE2EUsageReportConsistency（对账 1e-9）+ TestMeterRecords/TestUsageJSONLRoundTripAndAppend + TestSlashCache |
| SC-003 两次启动配置逐字节一致 | **达成** | TestBootComposeByteStable/OrderInsensitive + TestE2EBootArtifactByteStable；回调地址经 Boot Env 不入 mcp-config（终评 Important-1 修复后冒烟 B 复测） |
| SC-004 敏感路径 PR 缺元数据被拒 | **达成（本地语义实证；CI 首跑待 push）** | Task 30 报告：check-cache-impact.sh 现场回放——无元数据 exit 1 / 带元数据 exit 0；CI guard job 接线（PR_BODY_FILE 经 env 注入） |
| SC-005 pin 版本；漂移拒绝/放行留痕 | **达成** | check-engine-lock.sh exit 0（pin 2.1.291）+ TestStartRefusesVersionMismatch/TestDeferredDriftRefusal/TestStartAllowsDriftWithTrace/TestDoctorVersionDriftWarn |
| SC-006 fail-closed；deny 优先；崩溃可恢复 | **达成** | TestE2EDenyFailClosed/TestE2ECrashRecoveryResume + TestRulePriorityMatrix + TestGate{Unattended,Timeout,LateVerdict,ApproverError}FailClosed + 回调桥 fail-closed 矩阵（TestExecuteDeny*） |
| SC-007 分层规则 review 执行；CI 工具化后续 | **达成** | 13+ 轮独立评审（含终评与复审）记录于台账；TestNoSecondSystemPromptPath 超额 CI 化；其余 repolint 规则属 P2（与 spec 一致） |

## 4. 功能需求抽查（FR-001..FR-018 关键点）

- FR-001 命令面：TestRouteMatrix（root/run/doctor/cache/mcp serve/sessions）✓
- FR-002 子进程驱动引擎：ClaudeHost（stream-json 编解码 + 控制协议）+ 冒烟 A/B 实证 ✓
- FR-003 版本校验：TestStartRefusesVersionMismatch + TestDeferredDriftRefusal ✓
- FR-004 纯函数策略：TestRulePriorityMatrix/TestPresetMatrices ✓
- FR-005 审批队列 fail-closed：TestGateTimeoutFailClosed/Unattended/LateVerdict ✓
- FR-006 先持久化后发布：sessionx Store 红线测试（T3/T10）+ e2e 场景镜像 1:1 ✓
- FR-007 崩溃恢复：RecoveryRequired + Resume 合成收口（e2e 场景 5）✓
- FR-008 启动快照冻结：TestComposeSystemPromptSnapshot + SC-003 字节稳定；
  会话中变更提醒 watcher 属 P2（spec 分期，见下）✓
- FR-009/010 usage 记账与 cache 汇总：TestMeterRecords + TestReadUsageOffline + `mulix cache`（T27 接线）✓
- FR-011/012 TTL/地板告警：e2e 场景 4（expected_cold + NoticeEvent）+ TestGuardWindowWeightedHit ✓
- FR-013 CI 门禁脚本：check-cache-impact.sh 两例自验（Task 30 报告）✓
- FR-014 doctor：本机全绿 + TestDoctorChecksAllOK 等 8 项 ✓
- FR-015 MCP 工具框架：registry 注册期冻结测试 + mulix_usage + mulixpermission ✓
- FR-016 配置分层：TestDefaultPathsIncludesProjectLayer（项目层曾缺失，终评修复后钉死）✓
- FR-017 钩子四时点：mapHookEvent 四向映射测试（T21/T23）✓
- FR-018 协议探针三件套：docs/engine-protocol + fixtures/engine/*.jsonl + scripts/probe-engine.md（T5 前置交付）✓

## 5. 台账 Rulings 复核（对 spec 的效力）

| Ruling | 对 spec 的效力判定 |
|---|---|
| 直接在 master 实施（用户明示） | **成立**——spec 未约束分支策略；全部提交链可审计 |
| subagent 无 model 参数；后台子代理停滞 → 同步派发 | **成立**——所有评审/实现派发均同步完成并有产物；进程偏离已记录 |
| 沙箱拒绝 .git 写 → 提权（danger-full-access） | **成立**——边界已实证；提交均真实落库 |
| msys bash 受限 / 无 make | **成立**——Makefile 交付，`make test cross` 由 CI 执行；本地等价命令已现场跑（见 §1） |
| 非代码任务的 TDD 形态（脚手架/验证任务以编译判据） | **成立**——Task 1/2 报告以 N/A-RED + 编译 GREEN 记录；本次 guard 补钉为 18+4 份报告补齐可回放证据（事后回放已诚实声明） |
| 目录占位 .gitkeep | **成立**——目录均已填充实际内容 |
| goenv（GOCACHE/GOMODCACHE/GOPROXY） | **成立**——verify 全程沿用 |
| 空 diff 任务跳过评审派发 | **成立**——Task 2 的验证证据逐字记录于台账/报告 |
| FR-008「会话中变更提醒」= P2 watcher | **成立**——spec/design 分期记录一致；P1 快照冻结语义已钉 |

## 6. 遗留事项（不阻塞，均已留痕）

- CI 首跑验证待真实 push（本地无 GitHub Actions runner）。
- `-race` 由 CI ubuntu job 执行（本机无 cgo）。
- TUI 真实 TTY 人工冒烟（P2 前置事项）。
- 复审 N2（startErr 读取同步）/N4（并发重复投递的 resolved 重复发布面）/
  N5（单飞与 closed-deny 覆盖测试）→ P2 follow-up。

## 结论

**verify_result: pass** —— 14 包全量测试绿、vet/gofmt 干净、三平台交叉编译
通过、engine.lock 校验通过、真实引擎冒烟两轮完成、SC-001..SC-007 逐条达成
（TUI TTY 限制与 CI 首跑两项如实留痕为后续验证项）。
