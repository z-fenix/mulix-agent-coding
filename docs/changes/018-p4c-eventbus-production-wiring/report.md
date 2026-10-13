# 018-p4c-eventbus-production-wiring verify 报告

**日期:** 2026-10-13
**范围:** cc0a684..796059e(4 任务 + 终审修复轮,6 提交;10 文件 +380/-30)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **26/26 包 ok**,exit 0(×多轮;含 T3 后并发压测 6×4 包场景) |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(零新边) |
| flag 退役钉 | `grep -rn "EnableEventBus" internal/ cmd/` | 零命中 |
| -race / golangci-lint | 环境不可用(历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..105 / SC-001..004)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 三态门控 | ✅ | TestEventbusEnabled(方法级 5 分支)+ TestEventbusGateLoadFailClosed(Load 级+lastGood 顺序 mutation 见证) |
| FR-102 单真源接线 | ✅ | openSession 经 cfg.Eventbus;双字段退役零残留 grep;子代理路径同闸(session.go:473);既有测试 config helper 迁移 |
| FR-103 mulix events | ✅ | TestObserveMirrors(实时收行/干净退出/连接 fail-closed)+ 真机冒烟 6 事件全链 |
| FR-104 发布序零触碰 | ✅ | 桥 Emit 路径 diff 零触碰(终审核验「仅接线与订阅侧」) |
| FR-105 全量门禁绿/零第三方 | ✅ | 上表;go.mod 零新增 |
| SC-001 缺省关 | ✅ | 代码路径+真机实证(全新工作区无 [eventbus]→无 .mulix/bus/) |
| SC-002 enable=true 桥开 | ✅ | 真机冒烟(events 键就位+订阅可达) |
| SC-003 观察面实时收行 | ✅ | 真机 6 行(turn_started→engine_exited);纯订阅零写入 |
| SC-004 全量门禁绿 | ✅ | 上表 |

## 3. 裁定复核(ledger Ruling/Final 行)

1. **T3 Port 语义**(nats-server 0=默认 4222/-1=随机;010「-1=不监听」误记修正;监听面收窄至 127.0.0.1)。**成立**——systematic-debugging 探针坐实,修复后并发场景 6×4 包全绿。
2. **T3 nc.Flush()**(订阅注册先于发布)。**成立**。
3. **T4 endpoint 残留 fail-visible**(会话收尾关注点路由)。**认可**。
4. **终审 Minor 1 修复**(Load 级钉+mutation 见证)。**成立**——乱序 mutation 实测红(LastGood 含被拒配置)。
5. **终审 Minor 2-6 deferred**(toml 自然布尔提示/swallow 无日志/观察面重连冻结/UTF-8 截断/endpoint.tmp)——路由明示。**认可**。
6. **终审 set-aside 5 项**(无鉴权回环暴露面设计裁定/live-only/残留/无上限保留/发布序)——**认可**,与 010/012 裁定同源。

## 4. Deferred minors(5 项,路由明示)

见台账 Final 段;keys_source 等 P6+ 路由不变。

## 5. P4c 收官

事件总线从「仅持久无消费」翻转为生产接线:配置门控单真源 + `mulix events` 首个订阅消费者真机全链实证;红线全数守住;012/015 三次「P4c 翻转」Ruling 清偿闭环。
