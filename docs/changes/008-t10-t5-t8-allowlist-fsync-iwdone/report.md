# 008-t10-t5-t8-allowlist-fsync-iwdone verify 报告

**日期:** 2026-10-11
**范围:** b3d7a45..813d5a6(4 任务,5 提交含 style;8 文件,+233/-14)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **19/19 包 ok**,exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| -race / lint | 环境不可用(历次裁定同源) | 如实记录 |
| 四项钉组 | lint(6 fixture 子测+既有)、guard 交叉 2 测、memory 回归、control watcher+单飞 `-count=5` | 全 PASS |

## 2. 需求逐条核对(FR-101..107 / SC-001..005)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 解析器忽略块内注释 | ✅ | TestScanImportsCommentEdges 6 子用例(注释含 `)`/引号串/行尾/单行/多行块注释)全 PASS;扫描与无注释等价文件一致 |
| FR-102 交叉断言子集关系 | ✅ | TestLintAllowlistSubsetOfP1Repolint(经 FlagAllowlistFor 访问器);TestNoSecondSystemPromptPath 8 键清单 PASS;判别力测试(构造性) |
| FR-103 rename 前 Sync fail-closed | ✅ | memory.go:309 前插入;Sync 错误走既有包装路径;rename 全仓审计(仅 2 处,另一处已有 Sync) |
| FR-104 Close 有界等待 | ✅ | 2s select;TestCloseWaitsForWatcherDone(-count=5)PASS;nil no-op 保持 |
| FR-105 Close 后无 publish 越过 | ✅(传递) | publish 仅源自 watcher goroutine(publish 闭包唯一捕获点),Done 关 ⇒ 无后续 publish |
| FR-106 四项各携测试+零回归 | ✅ | 每任务 RED/GREEN 报告;既有矩阵(lint/guard/memory/control)全绿 |
| FR-107 行为契约不变 | ✅ | 违规判定规则/隔离语义/提醒语义未动;diff 范围与四项一一对应 |
| SC-001 | ✅ | 注释边缘 fixture 全 PASS |
| SC-002 | ✅ | 交叉断言+判别力(构造性)落地 |
| SC-003 | ✅ | Sync 落地+回归绿 |
| SC-004 | ✅ | Close/Done 断言落地+矩阵零回归 |
| SC-005 | ✅ | 四项各自提交(5 commits 一一对应);门禁绿 |

## 3. 裁定复核(ledger 5 条 Ruling/Final 行)

1. **Task 1 残留边缘(终审补正)**:原记录「未终止 /* 跨行」有误(代码已正确处理);真实残留=行内 `/*...*/` 终止后接代码的假阴性(罕见形态,失败面 fail-closed 或静默漏扫)——已路由 deferred 批次。**成立**(终审逐字节模拟证实)。
2. **Task 2 口径=裁定 A**(005 沿用;本任务第 8 键 + 判别力构造性证明)。**成立**。
3. **Task 4 RED 不可得**(5 连跑全绿;强化后置条件落地)。**成立**(终审复现:基线 10/10 绿,确认非回归)。
4. **Task 4 判别力更强形态路由**。**成立**。
5. **pre-existing start/Close 并发窗口**:FR-104 明示豁免,超出范围。**成立**。

## 4. Deferred minors(4 条新增,零阻塞)

1. lint 残留边缘:行内 `/*...*/` 终止后接代码的假阴性(扩 stripping 处理 code-after-*/)。
2. Task 4 判别力更强测试(watchInstructions + blocking publish,可产生真 RED)。
3. allowlist 断言抽 helper 与判别力测试共享。
4. 镜像事件数稳定性断言直接钉(现传递承载)。
(累积路由:此前 9 条 + 本批 4 条 + flake 观察 1 条 → 廉价测试补强批次 14 条。)

## 5. 结论

verify_result = **pass**。FR-101..107 逐条 ✅;SC-001..005 全部满足;5 条裁定全部成立(含 1 条 ledger 补正);终审零 Critical/Important(评审员独立复跑全绿、残留边缘逐字节模拟证实)。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
