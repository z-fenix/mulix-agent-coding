# 016-p5d-tool-cordis 回归证据(P5d T4,FR-105/FR-106/SC-004)

**日期**: 2026-10-13 | **分支**: master(trunk convention) | **范围**: 65397f8..HEAD(T1..T3 提交)

## 门禁结果(SC-004 / FR-106)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 全量测试 | `go test ./... -count=1` | exit 0,26 包全 ok(task-4-tests.log) |
| 静态检查 | `go vet ./...` | exit 0 |
| 格式 | `gofmt -l .` | 干净(无输出) |
| 分层矩阵 | `go run ./cmd/mulix lint layers` | 0 项违规(mulixkernel 已登记:config/ptc/registry) |
| SC-003 | `grep -rn "cordis_define\|cordis_undefine\|cordis_stop" internal/ cmd/` | 零命中 PASS |

## SC 判据逐项

- **SC-001**(kernel_inspect 返回正确状态):`TestKernelInspectTool` PASS——services 排序/profile/工具面/ptc_registered(Execute 时快照)/env 缺席降级非阻断。
- **SC-002**(cordis_run 沙箱执行+逃逸拒绝):`TestCordisRunTool` PASS——JS 纯计算(2,4,6)/工具暴露/4 条逃逸码 fail-closed(require/fetch/process.exit/import)/超时 fail-closed。
- **SC-003**(cordis_define/stop 不存在):注册面 seam 钉 `TestRegisterSelfRefTools` PASS(standard 恰 [kernel_inspect cordis_run],minimal 空)+ 源码 grep 零命中。
- **SC-004**(全量门禁绿):见上表。

## TDD RED/GREEN 记录

| 任务 | RED 见证 | GREEN |
|---|---|---|
| T1 kernel.Keys() | `c.Keys undefined`(kernel_test.go:391) | TestContextKeys PASS |
| T2 mulixkernel | 新包编译期失败(`undefined: NewCordisRunTool` 等,计划预期形态) | 3 测试 PASS(0.918s) |
| T3 serve 接线 | `o.KernelKeys undefined`(memory_injection_test.go:366) | TestBootEnvKernelKeys 双分支 PASS |
| T4 台账扫描 | —(只读核销) | 门禁表全绿 |

## FR-105 deferred 清偿(013/014/015 台账逐项)

- **013-p5a**:T6 deferred batch 已在 013 Task 6 complete 清偿(89ab1f1);「台账归零[除 3 条明示路由]」——3 条路由项即 T6 批次内容,已闭环。→ **无未清项**。
- **014-p5b**:终审 Minor(lastGood 顺序)已在 014 修复轮闭环(261d9ef);set-aside 三项(engine 侧过滤/-race 不可用/归档 010-012)为 standing 裁定,非债务。→ **无未清项**。
- **015-p5c**:终审 Critical/Important 已修复轮闭环(a2a54e0);set-aside 三项(双持久面 P4a 已裁定/goja VM-escape 深度审计超范围/慢工具 Execute 不受 timeout 管辖)为 standing 裁定。→ **无未清项**。

**结论:FR-105 累积 deferred 清偿 = 无未清项,逐项核销完毕(预读基线一致,未发现新未清项)。**

## P5d 收官判据

P5a(fork/replay/search)+ P5b(profiles)+ P5c(PTC)+ P5d(自指工具)全链落地;工具面新增 kernel_inspect/cordis_run(standard only);零第三方新增;治理红线(先持久化后发布/1:1/双层注册)零触碰——自指工具均为只读或 fail-closed 沙箱,不扩内核写面。
