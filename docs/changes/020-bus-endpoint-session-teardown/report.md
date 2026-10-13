# 020-bus-endpoint-session-teardown verify 报告

**日期:** 2026-10-14
**范围:** 167a18c..5dccb6a(2 任务,2 提交;4 文件 +66/-2)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **26/26 包 ok**,exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(零新边) |
| 真机冒烟 | 接线前基线 vs 接线后(enable=true run) | 残留实证→endpoint 清痕(docs/evidence/020-teardown-smoke.md) |
| -race / golangci-lint | 环境不可用(历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..103 / SC-001..003)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 main 接线 | ✅ | cmd/mulix/main.go Execute 后 CloseSharedBus;execErr 语义逐字节保留(终审核验全命令经 Execute 单点覆盖) |
| FR-102 门控关残留卫生 | ✅ | TestStaleEndpointRemovedWhenGateOff(全链 openSession 驱动);else 侧严格(无双删竞态——终审核验) |
| FR-103 全量门禁绿/零第三方 | ✅ | 上表;go.mod 零新增 |
| SC-001 正常退出清理 | ✅ | 真机冒烟接线前后对比 |
| SC-002 门控关+残留→清理 | ✅ | 单元钉(行为 RED→GREEN 逐字一致) |
| SC-003 全量门禁绿 | ✅ | 上表 |

## 3. 裁定复核(ledger Ruling/Final 行)

1. **T1 冒烟替代 RED**(进程边界单元不可钉;基线+接线后对比落 evidence)。**成立**——017 T2 前例同款。
2. **T2 实现期插曲**(清理语句误插 if 前侧与门控开竞态——未提交前纠正 else 侧)。**成立**——终审核验 else 侧严格。
3. **终审 set-aside 3 项**(同进程配置分歧类边缘/硬杀兜底 by design/serve 幂等)——**认可**。

## 4. Deferred minors(2 项)

- teardown_test.go 注释陈旧(随触碰清)。
- evidence 基线出处标注(后续 evidence 惯例统一钉 base hash)。

## 5. 收官

018 路由「endpoint 残留会话收尾」核销闭环:正常退出(main 接线)+异常残留(门控关卫生)双面覆盖;019 Close 删 endpoint 能力生产可达;台账无新增未清项。
