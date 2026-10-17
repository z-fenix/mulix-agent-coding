# 变更报告：引擎治理收尾（2026-10-16-engine-governance-cleanup）

**日期**: 2026-10-16 | **结果**: verify PASS | **范围**: 上变更（2026-10-16-engine-tool-governance-fix）两项 deferred 的清偿

## 1. 门禁实录（verify 阶段全量重跑）

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过 |
| 全量测试 | `go test ./... -count=1` | 27 包 ok，零失败 |
| 静态检查 | `go vet ./...` | 干净 |
| 格式 | gofmt（LF 副本口径）对本变更改动 .go 文件 | 零偏差 |
| engine.lock | `git diff 7ecdef5 HEAD -- engine.lock` | 零改动（FR-304） |

## 2. 需求逐项对账（spec FR/SC）

| 需求 | 落点 | 核验 |
|---|---|---|
| FR-301 失败路径零残留 | buildArgs mcp 段三个失败分支补 `os.Remove(settingsTemp)`；settings/mcp 物化改经 `createTempFile` seam | TestBuildArgsCleansSettingsTempOnMcpFailure（seam 注入 mcp 失败 → 断言 settings 临时文件已删 + err 上抛） |
| FR-302 失败清理测试覆盖 | 同上（先红后绿：编译期 RED → GREEN） | task-1-report.md |
| FR-303 Task 流形态结论落档 | internal/engine/doc.go「子代理流形态（2026-10-16 核验）」三事实 + forwarding 红线 | grep 核验：`--forward-subagent-text` 全仓仅 doc.go 命中（红线文档本身），零接线 |
| FR-304 治理语义零改动 | engine.lock 零 diff；围堵/审批面未触碰 | git 对账 + 全量测试 |
| SC-101 门禁 | §1 全绿 | PASS |
| SC-102 先红后绿 + 文档可复证 | task-1 RED（createTempFile undefined）→ GREEN；grep 复证 | PASS |

## 3. 台账 Ruling 复核

- Ruling-1（inline 执行）：2 任务机械性强，全绿——**成立**。
- Ruling-2（计划评审 1 Important + 1 Advisory 修入计划）：grep 核验命令改为可观测形态；Write/Close 分支既有 mcp 清理保留——**成立**。

## 4. 交付物与残留

2 commits（7ecdef5..81f4bfd）：失败路径临时文件零残留（2d43d84）+ Task 流形态结论与 forwarding 红线落档（81f4bfd）。

**核验结论（落档于 internal/engine/doc.go，durable）**：引擎原生 Task 在 mulix 泵面 = 普通 tool_use→tool_result（父调用即治理面）；子代理内部事件不上流（`--forward-subagent-text` 为引擎 opt-in flag，mulix 未接线）；启用 forwarding 前必须先做 parent_tool_use_id 治理建模（红线，不得静默开启）。

无新增 deferred。
