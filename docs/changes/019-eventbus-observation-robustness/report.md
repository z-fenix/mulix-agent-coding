# 019-eventbus-observation-robustness verify 报告

**日期:** 2026-10-13
**范围:** 517920b..1e53e8e(2 任务 + 终审修复轮,3 提交;9 文件 +197/-11)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **26/26 包 ok**,exit 0 |
| Flake 压测 | `go test ./internal/eventbus/ -run TestObserveSummaryRuneSafe -count=20` | ok(5.6s) |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(零新边) |
| -race / golangci-lint | 环境不可用(历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..105 / SC-001..005)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 桥失败可见 | ✅ | TestSharedEventBusWarnsOnFailure(endpoint-as-directory 毫秒级注入→stderr 含「桥关闭」);`o.stderr()` 既有缝(root.go:42) |
| FR-102 断连错退 | ✅ | TestObserveMirrorsPeerClosed(NoReconnect——钉为真钉非超时假绿,评审 nats.go 源码核验);Ctrl-C 路径 nil 不回退(TestObserveMirrors) |
| FR-103 三件修缮 | ✅ | TestObserveSummaryRuneSafe(utf8.ValidString+66 字符边界+轮询非空门);TestBusCloseRemovesEndpoint(幂等);TestEventbusEnabled 文案含 `enable = "true"` |
| FR-104 keys_source | ✅ | TestKernelInspectTool 双形态(env/degraded) |
| FR-105 门禁绿/零第三方 | ✅ | 上表;go.mod 零新增;分层零变更 |
| SC-001..005 | ✅ | 逐项对应上表 |

## 3. 裁定复核(ledger Ruling/Final 行)

1. **终审 Minor 1 重定级修复**(RuneSafe 空串假红→CI 门禁可靠性)——测试自身逻辑缺陷无自然 RED 形态;修复+`-count=20` 实证。**成立**。
2. **终审 Minor 2 deferred**(注释理由措辞)。**认可**。
3. **SetClosedHandler 上移建议不动**(Subscribe 兜底无实际缺口,避免无钉重构)。**认可**。
4. **终审 set-aside 4 项**(env 空串生产不可达/短摘要透传 018 既有/ClosedCB 即时派发/Close 恒 nil 前瞻)——**认可**。

## 4. Deferred minors(1 项)

- session.go 桥失败注释措辞(随下次触碰落)。

## 5. 收官

018 终审 5 项 deferred minor + 016 路由 keys_source 全数清偿;018 语义零回退(缺省关/原子写/纯订阅/Ctrl-C nil);台账无新增未清项(1 项注释措辞新入账)。
