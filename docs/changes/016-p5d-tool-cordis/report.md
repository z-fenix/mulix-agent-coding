# 016-p5d-tool-cordis verify 报告

**日期:** 2026-10-13
**范围:** a2a54e0..36f6674(4 任务 + 终审修复轮,5 提交;12 文件 +480)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **26/26 包 ok**(mulixkernel 新增),exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(mulixkernel:config/ptc/registry 登记在案) |
| SC-003 | `grep -rn "cordis_define\|cordis_undefine\|cordis_stop" internal/ cmd/` | 零命中 PASS |
| -race / golangci-lint | 环境不可用(历次裁定同源,CGO_ENABLED=0/工具链不匹配) | 如实记录 |

## 2. 需求逐条核对(FR-101..106 / SC-001..004)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 kernel_inspect 注册 | ✅ | TestKernelInspectTool(services 排序/profile/工具面/ptc_registered Execute 时快照;env 缺席降级非阻断);serve 进程 NewServer 冻结前注册(mcpserve.go) |
| FR-102 cordis_run 注册 | ✅ | TestCordisRunTool(JS 纯计算 2,4,6/工具暴露/30s 缺省;复用 ptc.Sandbox) |
| FR-103 define/undefine/stop 不实现 | ✅ | 注册面 seam 钉 TestRegisterSelfRefTools + 源码 grep 零命中(双钉) |
| FR-104 既有治理回路 | ✅ | 注册经 registry.Register(外层工具调用走 guard/审批/镜像);kernel_inspect ReadOnly=true,cordis_run ReadOnly=false;内核写面零扩动(Keys 纯读面) |
| FR-105 deferred 清偿 | ✅ | 013/014/015 台账逐项核销=无未清项(docs/evidence/016-p5d-regression.md §FR-105) |
| FR-106 全量门禁绿/零第三方 | ✅ | 上表;go.mod 零新增 |
| SC-001 巡检正确状态 | ✅ | TestKernelInspectTool + TestKernelKeysIncludeEventsKey(终审修复:总线会话 events 键就位) |
| SC-002 沙箱执行+逃逸拒绝 | ✅ | TestCordisRunTool(require/fetch/process.exit/import fail-closed + timeout) |
| SC-003 define/stop 不存在 | ✅ | 双钉全 PASS |
| SC-004 全量门禁绿 | ✅ | 上表 |

## 3. 裁定复核(ledger Ruling/Final 行)

1. **T1 RED 形态**(编译期失败=新读面 TDD 形态)。**成立**。
2. **T3 SC-003 注释改写**(包注释红线说明改「define/undefine/stop 三工具」保持字面零命中,门禁可机械执行)。**成立**——非注册面内容,不构成规避。
3. **终审 Important 修复**(KernelKeys 快照移至 assembleController 装配末尾;TestKernelKeysIncludeEventsKey RED(env 缺 events)→GREEN)。**成立**——openSession+EnableEventBus 真装配路径实测。
4. **终审 set-aside 3 项**(goja 深度逃逸面 015 standing/Boot env→serve 真机传播先例证据覆盖/沙箱 ReadOnly 集完备性 015 机制裁定)。**认可**——与前两变更裁定同源,不重开。
5. **FR-105 无未清项**(013 T6 批次已清偿/014 lastGood 修复轮闭环/015 standing 裁定)。**成立**——与预读基线一致。

## 4. Deferred minors(4 项,路由明示)

- mcpserve env 解析不 trim/不滤空段(P6+ 或下变更顺手)
- TestBootEnvKernelKeys 缺 hasMulixRegistryEntry 单独否决分支钉(下变更补)
- RegisterSelfRefTools standard+profile 双真源(下变更收敛)
- kernel_inspect keys_source 字段(P6+)

## 5. P5 收官

P5a(fork/replay/search)+P5b(profiles)+P5c(PTC)+P5d(自指工具)全链交付;治理红线零触碰;零第三方新增。
