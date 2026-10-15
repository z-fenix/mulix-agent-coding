# 009-t8-2-a verify 报告

**日期:** 2026-10-11
**范围:** 813d5a6..5e52ee9(4 任务 + 终审修复轮,7 提交;10 文件,+255/-23)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **19/19 包 ok**,exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| -race / lint | 环境不可用(历次裁定同源) | 如实记录 |
| 四组钉组 | 键集冻结/解析契约+fixture/双重故障+稳定性/lineNo 回归 `-count=1..5` | 全 PASS |

## 2. 需求逐条核对(FR-101..109 / SC-001..005)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 键集语义钉 | ✅ | TestWatcherKeySetFrozenAtStartup(新文件不进键集/无 EXTRA.md notice/既有键照常提醒,10.02s 双 5s 窗口);裁定注释入 startInstructionWatcher |
| FR-102 残留边缘修复 | ✅ | 段积累器重写 + FindAllStringSubmatch;3 新 fixture(008 终审失败形态/前后代码/整行块注释)+ 终审修复轮契约直测 + 相邻注释钉;008 的 6 fixture 零回归 |
| FR-103 双重故障防护 | ✅ | 内层 recover 包 Notice 发布;判别形态测试(sink 注入)修复前 RED(panic 逃出 defer → EOF)、修复后 GREEN 3 连跑 |
| FR-104 Close 稳定性直接断言 | ✅ | TestCloseMirrorEventCountStable(200ms 探测);allowlist helper 抽取 |
| FR-105 文档/注释卫生 | ✅ | ReadAll doc 两类损坏段;gateRuling.turn 注释;[006] t2 标注+原始行粘贴 |
| FR-106 memory lineNo | ✅ | `len(lines)+len(dmg.Oversized)+1`(jsonl.go:249/267 排除面核实);既有断言不变 |
| FR-107 测试卫生 | ✅ | 死代码+httptest import 删;fixture gate sub_session_id 断言(Type 过滤) |
| FR-108 门禁绿/分组提交 | ✅ | 5 提交与四组一一对应(含 style) |
| FR-109 三项维持路由标注 | ✅ | 本报告 §4 |
| SC-001..005 | ✅ | 对应上表(键集钉/残留转绿/防护落地/清偿 grep 可核/门禁绿+台账归零) |

## 3. 裁定复核(ledger 5 条 Ruling/Final 行)

1. **Task 1 语义钉形态**(无运行期 RED)。**成立**。
2. **Task 3 判别形态**(sink 注入,修复前 RED 实证)。**成立**——终审的 off-by-one 发现正是其判别力的旁证(修复轮 TDD 闭环)。
3. **[006] 原始行提取自全局镜像**(跨日存活实证)。**成立**。
4. **残留边缘描述 ledger 补正**(终审:原记录有误,真实残留已在本变更全覆盖)。**成立**。
5. **pre-existing start/Close 窗口豁免**。**成立**(FR-104 明示)。

## 4. Deferred minors(3 条新增,零阻塞)+ 路由维持

- 新增:noticePanicSink.events 死字段、200ms 探测窗口偏小、(lint 残留边缘已在本变更清偿,无新增)。
- 维持路由:SC-001 全 allow 委派补测(证据面)、start/Close 并发窗口(FR-104 豁免)、flake 观察。
- 累积:14 条中本变更清偿 11 条,维持路由 3 条——台账归零达成(SC-005)。

## 5. 结论

verify_result = **pass**。FR-101..109 逐条 ✅;SC-001..005 全部满足;5 条裁定全部成立;终审 1 Important(块注释跳过 off-by-one——实现偏离评审片段)已在修复轮 TDD 闭环(契约直测+相邻注释 fixture RED→GREEN);3 条 deferred 新增零阻塞,累积台账归零(除 3 条明示维持路由)。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
