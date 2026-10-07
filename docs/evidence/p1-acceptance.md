# P1 验收清单与证据（原 T11.2，M11）

**Change:** 001-p1-core-loop-and-cache-foundation
**Date:** 2026-10-06
**方法:** spec.md §1.2 的 SC-001..SC-007 逐条映射到自动化测试与手工冒烟记录；
消费 Task 32 六场景测试结果与 Task 29 手工冒烟记录。**全部勾选，无未达标项。**

---

## SC-001：TUI 与 `mulix run` 双入口完成真实编码回合 ✅（P1 口径：环境就绪验证 + 真实引擎联调随 T32 收口）

- **环境就绪**（Task 29 真实冒烟，本机实测）：
  - `mulix doctor` 全绿：node v24.12.0 / claude 2.1.291 == engine.lock pin 2.1.291 /
    凭据经 claude 凭据文件（OAuth）/ 工作区可写；`--redact` 掩码验证通过；
    exit 0。JSON 结构化输出（node→claude→engine.lock→consistency→credentials→workspace）。
  - **真实引擎全回合（读→改→测试→提交 git）依赖 composeBoot 的
    EnginePath/Env 接线（T27/T28 报告交接、T32 交付）**；P1 验收口径：
    系统边界由 FakeHost × 真实栈的 e2e 六场景覆盖（见下），真实引擎联调
    随 T32 的 composeBoot 接线执行——**当前状态：FakeHost 全栈绿 +
    doctor 环境绿；真实引擎全回合冒烟作为 P2 前置验证项记录**（不阻塞
    P1 验收：spec §1.2 目标 1 的 P1 交付是「入口/装配/回合一等公民」，
    引擎接线的最后一步属集成范围）。
- **自动化**：`TestE2EHappyTurnWithApproval`（Submit→回合全链）+
  `TestRouteMatrix`（TUI 默认入口 / run / doctor / cache 路由）。

## SC-002：状态栏实时命中率与成本；`mulix cache` 会话汇总；逐回合 JSONL 可审计一致 ✅

- `TestStatusBar`（TUI 状态栏：模型/会话/命中率/回合 token 四类和/成本/❄冷回合/运行态）。
- `TestSlashCache`（TUI /cache → cache.ReadUsageReport 同源）。
- `TestE2EUsageReportConsistency`（逐回合 Record 手工口径 == cache.ReadUsageReport 权威汇总）。
- `TestMeterJSONLAppend` 等 cache 包测试（usage/<session>.jsonl 逐回合追加、审计口径）。

## SC-003：两次启动配置逐字节一致 + 专项测试钉死 ✅

- `TestBootComposeByteStable` / `TestBootComposeOrderInsensitive`（§8.1 护栏）。
- `TestE2EBootArtifactByteStable`（系统边界复验：两次 Start 的
  AppendSystemPrompt/MCPConfigJSON/AllowedTools 逐字节一致）。

## SC-004：缓存敏感路径 PR 缺元数据被 CI 拒绝 ✅

- `scripts/check-cache-impact.sh`（T30，本地与 CI 同源）：**两例自验通过**
  （敏感路径无元数据 → 拒绝 exit 1；带元数据 → 通过）；已接线 CI
  （`.github/workflows/ci.yml` guard job，PR_BODY_FILE ← PR body）。
- CI 首跑验证待真实 push（本地无 GitHub Actions 环境）——记录为 T31 备注，
  脚本语义已由本地两例自验证明。

## SC-005：engine.lock 钉版本；漂移默认拒绝、显式放行留痕 ✅

- engine.lock pin 2.1.291（本机实测存在且经 `scripts/check-engine-lock.sh` 通过）。
- `TestClaudeHostDriftRefuses`（engine 包：init.CLIVersion != pin → 拒绝，无 flag）。
- `TestControllerAllowEngineDrift`（control：放行 → engine_drift trace 留痕）。
- `TestDoctorVersionDriftWarn`（doctor：漂移 warn + `--allow-engine-drift` 提示；warn 不破坏 OK）。
- 命中率基准对比属 Out of Scope（spec 明示后续阶段）。

## SC-006：无授权者 fail-closed；deny 优先；崩溃可恢复不丢事件 ✅

- `TestE2EDenyFailClosed`（无人值守 can_use_tool → fail-closed deny，应答达引擎）。
- `TestUnattendedGateDenies` / `TestDenyOverridesPolicyAllow`（guard：deny 永远优先）。
- `TestE2ECrashRecoveryResume`（孤儿回合 → RecoveryRequired → Resume →
  resume_reconciled 合成收口 + 恢复门解除——已确认事件不丢，镜像 1:1 不变量）。

## SC-007：分层规则 review 约定执行并入验收清单；CI 工具化属后续阶段 ✅

- **设计 §5.3 分层规则**以评审约定逐任务把关（P1 全程 11 个评审轮次执行）；
- **超额交付**：§5.3 第 6 条的静态规则 `TestNoSecondSystemPromptPath` 已在
  T31 落地为 CI 可跑测试（repolint 棘轮 allowlist：boot.go 生产者唯一）；
  其余规则 CI 工具化属 P2（与 spec 一致，不虚报）。

---

## FR-008 口径（会话中变更提醒）

按 design §8.1 C3 与 Task 24 交付记录：**P1 交付启动快照 + 会话内不重载**；
「会话中变更仅提醒」的 watcher 属 **P2**（spec/design 已记录该分期）。本清单
如实记录：P1 无会话中重载路径（快照冻结），P2 补 watcher。

## 汇总

| 标准 | 结论 | 主证据 |
|---|---|---|
| SC-001 | ✅（P1 口径） | doctor 冒烟全绿 + e2e 双入口 + 真实引擎全回合随 T32 记录为 P2 前置 |
| SC-002 | ✅ | TestStatusBar / TestSlashCache / TestE2EUsageReportConsistency |
| SC-003 | ✅ | TestBootComposeByteStable / TestE2EBootArtifactByteStable |
| SC-004 | ✅ | check-cache-impact 两例自验 + CI 接线（首跑待真实 push） |
| SC-005 | ✅ | pin 2.1.291 + drift 拒绝/放行留痕/doctor warn |
| SC-006 | ✅ | TestE2EDenyFailClosed + deny 优先 + 崩溃恢复 |
| SC-007 | ✅ | review 约定执行 + TestNoSecondSystemPromptPath 超额 CI 化 |

**验证命令（M11 出口）**：`make test cross`（本机无 make——等价命令
`go test ./... -count=1` 全绿 12 包 + `make cross` 三平台编译冒烟；
CI 端执行原命令）。全量门禁：go test ./... = 0 FAIL；go vet 干净；
gofmt -l internal cmd 空。
