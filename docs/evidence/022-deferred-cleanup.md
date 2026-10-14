# 022 P6 deferred 清扫 — 清偿证据（T3）

- change: 022-p6-deferred-cleanup
- date: 2026-10-14
- 环境: Windows 11 / Go 1.27 / trunk master（基线 hash 惯例首发执行——020 路由项）
- 基线: 7fe59e4（021 归档提交；022 变更 diff 起点）
- 说明: `-race` 因 CGO_ENABLED=0 不可用，如实记录（FR-108 惯例）

## 1. 门禁命令与结果

| 命令 | 结果 | 判定 |
|---|---|---|
| `go test ./... -count=1`（T1/T2 各自后复跑） | 26 包 ok，exit 0，零 FAIL | PASS |
| `go vet ./...` | 干净 | PASS |
| `gofmt -l .` | 空输出 | PASS |
| 负向四钉 `TestNewControllerRequiresKernelLoop` | T1 RED 亲证→GREEN | PASS |

## 2. 六项清偿逐条对照

| 来源 Final 行 | 落点 | 提交 |
|---|---|---|
| 021 Minor：类型不符文案缺 %T | controller.go:222 → `got %T`(传 loopAny) | 421d63d |
| 021 Minor：三态文案中英不一致 | controller.go:218/222 英文化（① 逐字节未动），钉同步 | 421d63d |
| 019 Minor：session.go 桥失败注释理由 | session.go:261-263 按 Final 原文更正 | 86f96f8 |
| 017 Minor：noMulixEntry 注释可读性 | memory_injection_test.go 补语义澄清行（Final 原文） | 86f96f8 |
| 020 Minor：teardown_test 注释陈旧 | 删「见 Step 3 注」死引用 | 86f96f8 |
| 020 Minor：evidence 基线出处标注 | 本文档首发 + 021 evidence 补基线行 | 本提交 |

017 rider：TestParseKernelKeys 补 `"a, ,b"` 内空白段例（Final Ruling「随下次触碰落」路由，本变更触碰即落）——PASS（覆盖钉，TrimSpace+滤空段 017 已实现）。

## 3. 行为零变更承载

唯一行为面改动 = 错误文案字符串（022 FR-101/102）：T1 RED（旧文案不含新钉）→GREEN（新钉三态互斥）；全矩阵 26 包绿含 e2e（① 文案依赖方零牵连）。注释/文档任务零代码语句触碰。
