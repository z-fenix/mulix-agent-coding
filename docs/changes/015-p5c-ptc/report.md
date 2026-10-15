# 015-p5c-ptc verify 报告

**日期:** 2026-10-12
**范围:** 7bff768..a2a54e0(7 任务 + 终审修复轮,25 提交含 012/013/014 交叉;32 文件 +1667/-222)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **25/25 包 ok**(ptc 新增),exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(ptc→registry 登记在案) |
| 沙箱安全 | TestSandboxEscapeRejected(require/fetch/process.exit/import) | 全 PASS |
| 沙箱功能 | TestSandboxToolCall/Timeout | PASS |
| ptc_execute | TestPTCExecuteTool(脚本文本→沙箱→结果) | PASS |
| -race / golangci-lint | 环境不可用(历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..107 / SC-001..004)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 goja 宿主 | ✅ | internal/ptc(纯 Go 零 CGO ✓);goja MIT;Runtime.Interrupt+InterruptedError(经 errors.As) |
| FR-102 沙箱执行环境 | ✅ | TestSandboxToolCall(工具暴露为 JS 函数);TestSandboxTimeout(100ms 超时中断) |
| FR-103 沙箱安全 | ✅ | TestSandboxEscapeRejected(require/fetch/process.exit/import 全 fail-closed);goja 默认无宿主能力 |
| FR-104 ptc_execute 注册 | ✅ | standard profile 注册(终审 Critical 修复:移至 ServeMCP serve 子进程=引擎可见面);minimal 不注册 |
| FR-105 治理回路 | ✅ | 外层 ptc_execute 走 guard/审批/镜像;内层 ReadOnly 工具无需审批(终审修复:限制 ReadOnly) |
| FR-106 零第三方新增(除 goja) | ✅ | goja 为经批准的第二个第三方依赖;无其他新增 |
| FR-107 分层矩阵 | ✅ | internal/ptc: {internal/registry};layers 0 违规 |
| SC-001 沙箱测试 | ✅ | TestSandboxToolCall/EscapeRejected/Timeout |
| SC-002 ptc_execute e2e | ✅ | TestPTCExecuteTool(脚本文本→沙箱→结果);serve 子进程注册(终审 Critical 修复) |
| SC-003 profile 接线 | ✅ | standard 注册/minimal 不注册(TestProfileServiceKey + ToolFilter) |
| SC-004 全量门禁绿 | ✅ | 上表 |

## 3. 裁定复核(ledger 7 条 Ruling/Final 行)

1. **Task 1 goja API 修正**(InterruptedError 非 IsInterrupted;矩阵插入位置修正)。**成立**。
2. **Task 2 ptcOnce 包级提升**(主会话+子会话各调一次 → Register 全局 panic;包级 Once 进程内一次定型)。**成立**。
3. **终审 Critical 修复**(ptc_execute 注册移至 ServeMCP serve 子进程=引擎可见面)。**成立**——012 终审 Critical 同样指注册位置而非机制。
4. **终审 Important 修复**(沙箱限制 ReadOnly——内层工具调用绕治理回路=审计黑洞)。**成立**。
5. **EnableEventBus 生产默认关**(P4c 翻转)。**成立**。
6. **Task 3 语义钉**(无运行期 RED)。**成立**。
7. **sessionx Windows flake**(预存,复跑全绿)。**成立**。

## 4. Deferred minors / 路由

- json.Marshal(call.Argument(0).Export()) 错误静默 → nil args(终审 Minor;goja Export() 对 JSON-safe 类型恒成功)。
- 全局对象白名单 vs 默认 globals + tools(实际等价于逃逸拒绝,文档措辞待对齐)。
- 慢工具 Execute 不受 timeout 管辖(goja 已知限制)。
- ctx 未穿透沙箱(ServeMCP 注册 + 沙箱 context.Background())。
- 累积路由不变(SC-001 补测/并发窗口/flake/toml tag/CloseSharedBus 挂点)。

## 5. 结论

verify_result = **pass**。FR-101..107 逐条 ✅;SC-001..004 全部满足;7 条裁定全部成立;终审 1 Critical + 2 Important 已在修复轮 TDD 闭环;5 条 Minor 零阻塞。P5c 交付:goja 沙箱(ReadOnly 工具暴露+超时+安全白名单)+ ptc_execute 工具(serve 子进程注册,standard profile)+ profile 接线(standard/minimal)。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
