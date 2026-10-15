# 007-t12-6-sc-006 verify 报告

**日期:** 2026-10-11
**范围:** ee7a024..b3d7a45(1 任务,1 提交;1 文件 1 行原位替换,代码零改动)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **19/19 包 ok**,exit 0(终审轮曾观测 1 次 `TestSubagentWiringEndToEnd` 时序 flake,复跑 3 次全过——与本变更无关,零 Go 文件变更,路由为已知 flake 观察项) |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 最小化 diff | `git diff ee7a024..HEAD --stat` | 仅 p2-acceptance.md,1+/1-(单表格行原位替换,FR-103) |

## 2. 需求逐条核对(FR-101..104 / SC-001..002)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 计数修正 4→5 | ✅ | p2-acceptance.md:230 现读「guard 5 测」;两文件无「4 测」残留 |
| FR-102 核对依据入档 | ✅ | 口径说明入 SC-006 行(历史约定 5+3 + 差异路由);**实际测试函数清单**(树内入档,下 §4) |
| FR-103 最小化 | ✅ | 单行原位替换;该行其他断言(event codec/Replay/TestE2ECrashRecoveryResume/真实冒烟审计)逐字符未动 |
| FR-104 门禁绿 | ✅ | 上表 |
| SC-001 两台账一致且与裁定 A 口径一致 | ✅ | report.md:52 与 p2-acceptance.md:230 均 5+3;差异(8+4)入档路由 |
| SC-002 修正最小化 | ✅ | 1 文件 1 行 |

## 3. 裁定复核(ledger 1 条 Ruling)

1. **口径=裁定 A**(历史约定 5+3;当前 grep 事实 guard 8 + cli 4 非 helper 的差异入档路由;不追溯改写 P2 时点记录)。**成立**——终审确认反「过度声明」关切解除(口径声明与差异披露同句并存)。

## 4. 核对依据入档(实际测试函数清单,树内)

**guard 非helper(8)**:`TestNewScriptConsumerValidation`、`TestScriptConsumerExecArgvVerbatim`、`TestScriptConsumerJSONResponseAccepted`、`TestScriptConsumerTimeoutDegrades`、`TestScriptConsumerCrashDegrades`、`TestScriptConsumerStdinOptionalNoRead`、`TestScriptConsumerDefaultTimeoutEffective`、`TestScriptConsumerFanOutWithBuiltin`(helper `TestHookHelperProcess` 不计入)。

**cli 非 helper(4)**:`TestHookWiringMountsConsumerAndTriggers`、`TestHookWiringTimeoutDegradesWithMirrorNotice`、`TestHookWiringInvalidRulesRefuseStart`、`TestHookWiringEmptyRulesZeroDelta`(helper `TestHookWiringHelperProcess` 不计入)。

**口径说明**:report.md 既有口径 5+3(历史约定,脚本消费核心 5 个)为台账记录值;与当前 grep 事实 8+4 的差异按裁定 A 入档路由。

## 5. Deferred minors / 路由

- `TestSubagentWiringEndToEnd` 单次时序 flake(TurnDone/temp-dir 竞争)——非本变更引入,复跑全过;复现时路由(与廉价测试补强批次合并考虑)。
- 累积 deferred minors 9 条维持既有路由。

## 6. 结论

verify_result = **pass**。FR-101..104 逐条 ✅;SC-001..002 满足;裁定 A 复核成立;终审 **Clean**;核对依据树内入档(评审员 advisory 已采纳)。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
