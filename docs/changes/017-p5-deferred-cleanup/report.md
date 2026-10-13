# 017-p5-deferred-cleanup verify 报告

**日期:** 2026-10-13
**范围:** cb77111..633e13e(2 任务,2 提交;4 文件 +71/-21)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **26/26 包 ok**,exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(矩阵零变更) |
| -race / golangci-lint | 环境不可用(历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..104 / SC-001..004)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 env 解析加固 | ✅ | TestParseKernelKeys 四分支("a, b,,c,"→[a b c]/全空白 nil/空串 nil/单键);ParseKernelKeys 导出单一实现(cli 消费) |
| FR-102 否决分支钉 | ✅ | TestBootEnvKernelKeys 三分支全 PASS;mutation 见证(注释右合取项→分支 3 FAIL,task-2-mutation.log)→还原→PASS |
| FR-103 单真源收敛 | ✅ | RegisterSelfRefTools(profile, keys, tools, reg)——standard bool 删除;调用点 cfg.Profile.Normalized() 单源;TestRegisterSelfRefTools 语义等价迁移 |
| FR-104 全量门禁绿/零第三方 | ✅ | 上表;go.mod 零新增;分层矩阵零变更 |
| SC-001 脏 env 干净输出 | ✅ | TestParseKernelKeys |
| SC-002 双合取项均有钉 | ✅ | 左项(no keys 分支)+右项(noMulixEntry 分支) |
| SC-003 无矛盾组合面 | ✅ | 签名即文档——bool 参数不存在 |
| SC-004 全量门禁绿 | ✅ | 上表 |

## 3. 裁定复核(ledger Ruling/Final 行)

1. **T1 导出 deviation**(设计 unexported→导出 ParseKernelKeys,cli 消费者要求)。**成立**——计划评审已预告。
2. **T2 mutation 替代 RED**(生产门控已正确;构造自然 RED 须破坏生产代码)。**成立**——mutation FAIL 落 task-2-mutation.log,评审员独立复核在案。
3. **T2 附带修复**(原精确串断言对分支 3 假阴性——前缀判定+注入分支精确值校验)。**成立**——终审确认假阴性窗口真正关闭。
4. **终审 set-aside 4 项**(设计冻结不追溯/controller 侧 Join 不 Trim/不去重/TrimSpace unicode)——executor 逐项认可。**成立**。
5. **终审 1 Minor**(noMulixEntry 注释可读性)——**deferred 入账**,随下次触碰该测试落,不在本变更扩散(Minor 纪律)。

## 4. P5 清理收官

016 终审 3 项代码路由 deferred minor 全数清偿闭环;keys_source(P6+)按路由不在范围;台账无新增未清项(1 项注释可读性 Minor 新入账并路由)。
