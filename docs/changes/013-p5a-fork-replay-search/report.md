# 013-p5a-fork-replay-search verify 报告

**日期:** 2026-10-12
**范围:** 110d3bf..89ab1f1(7 任务 + 终审修复轮 + P4b-2 交叉,32 提交;31 文件 +2609/-218)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **24/24 包 ok**(agentloop/textutil 新增),exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(agentloop/llm/llm-claude/eventbus 登记在案) |
| 子测试 | 439 个 `-v` 全 PASS | 行为零变更 + 新能力 |

## 2. 需求逐条核对(FR-101..105 / SC-001..005)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 sessionx.Fork | ✅ | TestForkCopiesEventsAndMeta(副本/独立/meta)+ TestForkFailClosed(atSeq≤0/超末尾);Windows 句柄兼容(Close+OpenStore) |
| FR-102 agentloop.Replay | ✅ | TestReplayRoutesMirrorEvents(路由对齐+零 OnEngineClosed);类型化事件切片(规范 FR-102 同步) |
| FR-103 SearchEvents | ✅ | TestSearchEventsHitsAndEmpty(命中/空结果);EventHit{LineNo,Kind,Excerpt,Score};rune-safe 截断;k≤0 fail-closed |
| FR-104 补强批次 | ✅ | [010]MountAll 重复键测试、[011]Bail 注释、[012]EnableEventBus 位置/hooks 键/回填注释、[009]死字段修正还原、[010]registry doc 更新 |
| FR-105 零第三方新增 | ✅ | 仅 NATS(P4a 已批准) |
| SC-001 fork 三场景 | ✅ | 副本/独立/fail-closed |
| SC-002 replay 路由+零追加 | ✅ | 路由对齐+零 OnEngineClosed |
| SC-003 search 命中/空 | ✅ | 两态钉 |
| SC-004 补强清偿 | ✅ | 台账归零(除明示路由 3 条) |
| SC-005 门禁绿 | ✅ | 上表 |

## 3. 裁定复核(ledger 7 条 Ruling/Final 行)

1. **Task 1 wrapper 两轮修正**(alias 不可定义方法→wrapper struct;TermSet/IsCJK 导出)。**成立**。
2. **Task 2 Windows 句柄兼容**(Fork 返回 Close+OpenStore)+fixture 修正×3。**成立**。
3. **Task 3 Replay 无 OnEngineClosed**(历史重放无关闭语义;nil EventSource 处理)。**成立**。
4. **Task 4 st.Close(TempDir 兼容)+eventTextOf 类型分支**。**成立**。
5. **Task 5 cobra Execute 模式**。**成立**。
6. **Task 6 [009] events 字段还原**(009 死字段判断有误——Publish 内实际消费;ledger 修正)。**成立**。
7. **Task 7 全矩阵**。**成立**。

## 4. Deferred minors / 路由

- 终审 2 Minor 修复轮折叠:rune-safe 截断、k≤0 fail-closed、fork 失败清理(已折入)。
- 维持路由:SC-001 全 allow 委派补测(证据面)、start/Close 并发窗口(FR-104 豁免)、flake 观察、[010]EnableEventBus toml(非 toml 面)。
- 累积 deferred 清零(除明示路由 4 条)。

## 5. 结论

verify_result = **pass**。FR-101..105 逐条 ✅;SC-001..005 全部满足;7 条裁定全部成立;终审 2 Important 已在修复轮闭环(k≤0 fail-closed/rune-safe 截断折叠、Fork meta 错误传播);4 条 deferred 零阻塞。P5a 交付:fork/replay/search 三件套 + BM25 共用抽取 + 补强批次清偿。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
