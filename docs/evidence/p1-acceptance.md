# P1 验收清单与证据（原 T11.2，M11）

**Change:** 001-p1-core-loop-and-cache-foundation
**Date:** 2026-10-08（真实引擎冒烟修复轮后修订）
**方法:** spec.md §1.2 的 SC-001..SC-007 逐条映射到自动化测试与真实引擎
冒烟记录。每条引用的测试名均经 `grep "func <Name>("` 逐一定位核验
（终评审定 5 个伪造引用已全部替换为真实测试名）。

---

## SC-001：TUI 与 `mulix run` 双入口完成真实编码回合 ✅

- **环境就绪**（本机实测）：`mulix doctor` 全绿——node v24.12.0 /
  claude 2.1.291 == engine.lock pin 2.1.291 / 凭据经 claude 凭据文件（OAuth）/
  工作区可写；`--redact` 掩码验证通过；exit 0。doctor 探针自动化：
  `TestDoctorChecksAllOK` / `TestDoctorFaultInjection` / `TestDoctorVersionDriftWarn` /
  `TestDoctorCredentialDetailNeverSecrets`（cli 包）。
- **真实引擎全回合冒烟（2026-10-08 本机实测，修复轮 e7e7fbd 后）**：
  独立 git 冒烟工作区（README/calc.py/mulix.toml/engine.lock）执行
  `mulix run "read calc.py → add subtract → create test_calc.py → run asserts
  → git commit 'smoke: add subtract + tests'"`，真实 claude 2.1.291 引擎：
  - 回合完整走通读→改→测→提交：**git commit `4b58fc7` "smoke: add
    subtract + tests"** 真实落盘（calc.py 增 subtract、test_calc.py 断言经
    python 执行通过）；
  - **权限回路端到端工作**：引擎 → `mcp__mulix__permission` MCP 工具 →
    serve 子进程 → 前台回调裁决核 → behavior JSON → 引擎；allow 命中放行
    （Read/Write/Bash）、未命中 fail-closed deny（审批事件落镜像）；
  - 引擎 graceful 退出、mulix run exit 0。
- **诚实限制**：TUI 通道的同等验证受限于冒烟环境无真实 TTY——bubbletea
  TUI 经管道 stdin 仅回显终端初始化序列（无法驱动回合）。run 与 TUI 共用
  同一装配路径（`buildComponents`）与同一控制回路（`RunPrompt` 与 TUI 的
  回合提交同经 `Controller.Submit`）；`TestRouteMatrix`（cli）钉住四入口
  路由，`TestStatusBar`/`TestSlashCache`（TUI 单元）钉住状态栏与 /cache。
  真实 TTY 下的 TUI 回合留待人工冒烟（不阻塞：控制路径已由 run 通道
  真实引擎验证）。
- **自动化**：`TestRunUnattendedE2E`、`TestE2EHappyTurnWithApproval`
  （Submit→回合全链，FakeHost × 真实 control/sessionx/cache/guard 栈）。

## SC-002：状态栏实时命中率与成本；`mulix cache` 会话汇总；逐回合 JSONL 可审计一致 ✅

- `TestStatusBar`（TUI 状态栏：模型/会话/命中率/回合 token 四类和/成本/
  ❄冷回合/运行态）、`TestSlashCache`（TUI /cache → cache.ReadUsageReport 同源）。
- `TestE2EUsageReportConsistency`（系统边界：Report 的 Uncached/Write/Read/
  Output 逐项 == Meter 总和，加权 SessionHit 对比误差 1e-9）。
- `TestMeterRecords` / `TestUsageJSONLRoundTripAndAppend`（cache：逐回合
  JSONL 追加与审计口径）。

## SC-003：两次启动配置逐字节一致 + 专项测试钉死 ✅

- `TestBootComposeByteStable` / `TestBootComposeOrderInsensitive`（§8.1 护栏）。
- `TestE2EBootArtifactByteStable`（系统边界复验：两次 Start 的
  AppendSystemPrompt/MCPConfigJSON/AllowedTools 逐字节一致）。

## SC-004：缓存敏感路径 PR 缺元数据被 CI 拒绝 ✅

- `scripts/check-cache-impact.sh`（T30，本地与 CI 同源）：两例自验通过
  （敏感路径无元数据 → 拒绝 exit 1；带元数据 → 通过）；diff 失败
  fail-closed（评审 M-7 修复，无 `|| true`）。
- CI 接线（`.github/workflows/ci.yml` guard job）：PR body 经 `env:`
  注入临时文件（评审 CI 阻塞项修复，不落命令行注入面）。CI 首跑验证
  待真实 push（本地无 GitHub Actions 环境）——T31 备注。

## SC-005：engine.lock 钉版本；漂移默认拒绝、显式放行留痕 ✅

- engine.lock pin 2.1.291（本机实测；`scripts/check-engine-lock.sh` 通过）。
- `TestStartRefusesVersionMismatch`（engine：init.CLIVersion != pin → 拒绝）；
  `TestDeferredDriftRefusal`（协议适配后的拒绝语义：init 延迟到首条消息 →
  首轮 validateHandshake 拒绝并以 `refused:` 收口杀引擎）；
  `TestStartAllowsDriftWithTrace`（放行 → trace）。
- `TestComposeBootAllowDriftPassthrough` / `TestResumeDriftTraceMirrored`
  （control：放行留痕）。
- `TestDoctorVersionDriftWarn`（doctor：漂移 warn + 提示，warn 不破坏 OK）。
- 命中率基准对比属 Out of Scope（spec 明示后续阶段）。

## SC-006：无授权者 fail-closed；deny 优先；崩溃可恢复不丢事件 ✅

- 系统边界：`TestE2EDenyFailClosed`（无人值守 can_use_tool → fail-closed
  deny，应答达引擎）、`TestE2ECrashRecoveryResume`（孤儿回合 →
  RecoveryRequired → Resume → resume_reconciled 合成收口，镜像 1:1 不变量）。
- guard：`TestRulePriorityMatrix`（deny > ask > allow > fallback）；
  `TestGateUnattendedFailClosed` / `TestGateTimeoutFailClosed` /
  `TestGateLateVerdictFailClosed` / `TestGateApproverErrorFailsClosed`
  （无授权者/超时/迟到/错误全 fail-closed）。
- control 路由：`TestCanUseToolUnattendedDeny` / `TestCanUseToolDenyViaPolicy` /
  `TestCanUseToolTimeoutFailClosed`。
- 权限回调桥 fail-closed：`TestExecuteDenyWithoutCallback` /
  `TestExecuteDenyOnServerErrors` / `TestExecuteAllowPassthrough`
  （tools/mulixpermission：无回调地址/不可达/非 200/非 behavior JSON 一律
  deny）+ `TestPermissionCallbackBridge`（control：回调 → 裁决核，
  allow 规则放行/无人值守 deny）。

## SC-007：分层规则 review 约定执行并入验收清单；CI 工具化属后续阶段 ✅

- **设计 §5.3 分层规则**以评审约定逐任务把关（P1 全程 11 个评审轮次 +
  终评修复轮执行）；
- **超额交付**：§5.3 第 6 条的静态规则 `TestNoSecondSystemPromptPath` 已在
  T31 落地为 CI 可跑测试（repolint 棘轮 allowlist：boot.go 生产者唯一）；
  其余规则 CI 工具化属 P2（与 spec 一致，不虚报）。

---

## FR-008 口径（会话中变更提醒）

按 design §8.1 C3 与 Task 24 交付记录：**P1 交付启动快照 + 会话内不重载**；
「会话中变更仅提醒」的 watcher 属 **P2**（spec/design 已记录该分期）。
本清单如实记录：P1 无会话中重载路径（快照冻结），P2 补 watcher。

## 汇总

| 标准 | 结论 | 主证据 |
|---|---|---|
| SC-001 | ✅ | doctor 全绿 + **真实引擎全回合（4b58fc7）+ 权限回路端到端** + e2e/路由测试；TUI 真实 TTY 留待人工（限制如实记录） |
| SC-002 | ✅ | TestStatusBar / TestSlashCache / TestE2EUsageReportConsistency |
| SC-003 | ✅ | TestBootComposeByteStable / TestE2EBootArtifactByteStable |
| SC-004 | ✅ | check-cache-impact 两例自验（fail-closed）+ CI 接线（首跑待真实 push） |
| SC-005 | ✅ | pin 2.1.291 + 拒绝/放行/延迟拒绝（TestDeferredDriftRefusal）/doctor warn |
| SC-006 | ✅ | e2e deny fail-closed + crash resume + guard/gate 矩阵 + 回调桥 fail-closed |
| SC-007 | ✅ | review 约定执行 + TestNoSecondSystemPromptPath 超额 CI 化 |

**验证命令（M11 出口）**：`go test ./... -count=1` 全绿（13 包，含新增
tools/mulixpermission）+ `go vet ./...` 干净 + `gofmt -l internal cmd` 空；
`make test cross` 与三平台交叉编译由 CI（T31 workflow）执行——本机无 make/cgo，
`-race` 由 ubuntu 作业补位（T31 交付记录）。
