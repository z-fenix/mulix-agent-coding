# 021 P6 loop 插件化 — 全矩阵回归证据（T3）

- change: 021-p6-loop-plugin-kernel-authority
- date: 2026-10-14
- 环境: Windows 11 / Go 1.27 / trunk master（T1=f2802fa，T2=21b91e4）
- 说明: `-race` 因 CGO_ENABLED=0 不可用，如实记录（FR-108 惯例）

## 1. 门禁命令与结果

| 命令 | 结果 | 判定 |
|---|---|---|
| `go test ./... -count=1` | 26 包 ok，exit 0，零 FAIL | PASS |
| `go vet ./...` | 干净 | PASS |
| `gofmt -l .` | 空输出 | PASS |
| `go run ./cmd/mulix lint layers` | 分层检查通过：0 项违规 | PASS |
| `grep -rn "opts\.Loop\|Options\.Loop" internal/ cmd/` | 无匹配（exit 1） | PASS |

26 包明细（ok 计时摘要）：agentloop 0.397s / cli 4.094s / control 13.038s / e2e 1.027s / kernel 1.504s / eventbus 2.756s / 其余 20 包均 ok。

## 2. 负向四钉（TestNewControllerRequiresKernelLoop，单跑 PASS）

`internal/control/lifecycle_test.go:471`，钉 021 FR-102「键即契约——缺 loop 的 controller 不该存在」：

| # | 状态 | 期望 fail-closed 文案 | 结果 |
|---|---|---|---|
| ① | Kernel nil | 含 "kernel context" | PASS |
| ② | ctx 缺 agentloop 键 | 含 "服务键缺失" | PASS |
| ③ | 键值类型不符（Provide 字符串） | 含 "类型不符" | PASS |
| ④ | 正确键（kernelCtxWithLoop） | 组装成功（err == nil） | PASS |

## 3. 8 调用点注入清单（021 FR-102 测试注入缝）

行号为提交 21b91e4 后现行行号（gofmt 重排后实测）：

| 包 | 文件:行 | 用途 |
|---|---|---|
| control | controller_test.go:92 | 会话/回合主路径 |
| control | controller_test.go:156 | 权限回路场景 |
| control | controller_test.go:200 | 权限回路场景 |
| control | controller_test.go:486 | resume/收口场景 |
| control | lifecycle_test.go:60 | 生命周期 + 负向钉 ④ 正控 |
| cli | subagent_wiring_test.go:502 | 子代理装配接线 |
| e2e | e2e_test.go:136 | 系统边界主构造点 |
| e2e | e2e_test.go:341 | 崩溃恢复场景 |

注入形态统一为 `Kernel: kernelCtxWithLoop()`；helper（kernel.NewContext(nil) + Mount(agentloop.NewPlugin())）在 control/cli/e2e 三测试面各自最小实现（包间无共享测试基建）。

## 4. SC 逐项判定

| SC | 内容 | 判定依据 | 判定 |
|---|---|---|---|
| SC-001 | Plugin Mount 语义正确（键就位/回滚） | T1 kernel_assembly_test + agentloop 包测试 | PASS |
| SC-002 | NewController 缺键/类型不符 fail-closed（双钉） | 负向四钉 ①②③（§2） | PASS |
| SC-003 | 行为零变更（全矩阵回归） | 26 包全绿含 e2e happy-turn/delegation/PTC 场景 | PASS |
| SC-004 | `Options.Loop` 零残留 | grep exit 1（§1） | PASS |
| SC-005 | 全量门禁绿 | §1 四门禁全 PASS | PASS |

## 5. 红线符合性

- 先持久化后发布 / fail-closed / 双层注册未触碰本任务改动面外行为；loop 权威解析走 kernel 服务键（双层注册纪律）。
- 退役方式：pump 兜底 `loop := c.loop`，无静默回退路径。
