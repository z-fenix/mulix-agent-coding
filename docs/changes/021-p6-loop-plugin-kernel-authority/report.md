# 021-p6-loop-plugin-kernel-authority verify 报告

**日期:** 2026-10-14
**范围:** 8dc3737..efed855(3 任务,3 提交;9 文件 +239/-8)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **26/26 包 ok**,exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l .` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(零新边) |
| 退役钉 | `grep -rn "opts\.Loop\|Options\.Loop" internal/ cmd/` | 零匹配(exit 1) |
| 负向四钉 | `go test ./internal/control -run TestNewControllerRequiresKernelLoop -v` | PASS(①nil ②缺键 ③类型不符 ④正控) |
| -race / golangci-lint | 环境不可用(CGO_ENABLED=0 / 工具链不匹配,历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..105 / SC-001..005)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 Plugin+装配 Mount | ✅ | agentloop.Plugin 实现 kernel.Plugin 接口;cli/session.go 裸 Provide 改 root.Mount(agentloop.NewPlugin())(终审核验签名匹配) |
| FR-102 权威解析 fail-closed | ✅ | NewController 三态(nil→kernel context required/缺键→服务键缺失/类型不符→类型不符);测试注入缝 8 调用点(证据 §3) |
| FR-103 Options.Loop 退役 | ✅ | 字段删除+pump 兜底退役;grep 零残留(终审独立复核含 `.Loop =` 形态) |
| FR-104 行为零变更 | ✅ | 26 包全矩阵绿(含 e2e happy-turn/delegation/PTC);终审独立复跑 |
| FR-105 全量门禁绿 | ✅ | 上表;go.mod 零触碰(零第三方) |
| SC-001..005 | ✅ | docs/evidence/021-loop-plugin-regression.md §4 逐项表 |

## 3. 终审复核(独立 subagent,Approved)

- FR-101..105 全经代码事实核验+审者独立复跑门禁自证;无 Critical/Important。
- 无静默回退路径:c.loop 写于 goroutine 启动前,无 Controller 字面量构造旁路;主/子会话两路均经 assembleKernelContext 单点回填。
- 三测试面 kernelCtxWithLoop 与生产装配同源(同一 agentloop.NewPlugin),非平行实现。

## 4. Deferred minors(2 项)

- controller.go:224 类型不符文案缺 %T 实际类型(仅诊断质量,测试钉自洽)。
- controller.go:216 三态文案中英文不一致(纯观感)。

路由:随下一 control 触碰变更顺手修,不单独开任务。

## 5. 收官

loop 权威解析收口:装配期 fail-closed(键即契约)+ Options.Loop 全退役;pump 消费 c.loop 单源。021 FR 全闭环,台账无新增未清项。
