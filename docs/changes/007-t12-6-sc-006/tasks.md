# T12-6 SC-006 计数修正 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正 `docs/evidence/p2-acceptance.md` SC-006 行的钩子测试计数笔误(guard 4 测 → 5 测),使两份台账一致并与裁定 A 口径一致(5+3=report.md 历史约定;当前 grep 事实 8+4 差异已入档路由)。

**Architecture:**(bounded,设计于 chat 并已批准;裁定 A)单点文档修正 + 口径说明一句(历史约定 5+3,差异 8+4 入档路由);核对依据入档;其余零变更。

**Tech Stack:** 无(grep 核对 + 文档编辑)。

**Spec:** `docs/specs/007-t12-6-sc-006/spec.md`

**Design:** approved in chat (bounded) —— 要点:①计数改 5+3;②口径说明一句(脚本消费核心测试 5 个;`TestHookHelperProcess` 为 helper-process 模式入口,属测试基础设施不计入);③核对依据入档;④最小化 diff。

## Global Constraints

- FR-103:仅该计数与口径说明,不动 SC-006 行其他断言。
- FR-104:全量门禁绿;除该文档外无文件变更。

## Review Focus

- 两份台账修正后一致(report.md 5+3 == p2-acceptance.md 5+3)。
- 计数与裁定 A 口径一致(差异 8+4 已入档路由,grep 清单名带入档)。
- diff 最小化(仅 p2-acceptance.md 一处)。

---

## Task 1: 计数修正 + 核对依据入档

**Files:**
- Modify: `docs/evidence/p2-acceptance.md`(SC-006 行,~:230)

**Interfaces:**
- Consumes: 仓库实际测试清单(guard 非helper 8 / cli 非 helper 4——grep 可复核)与 report.md 既有口径(5+3,裁定 A:历史约定)。
- Produces: 两份台账计数一致(5+3);无代码接口。

- [ ] **Step 1: 核对仓库事实与口径差异(裁定 A)**

Run: `grep -n "func Test" internal/guard/hooks_custom_test.go internal/cli/hooks_wiring_test.go`
Expected: guard 9 个测试函数(helper `TestHookHelperProcess` + 8 个非 helper:计划所列 5 个 + `TestScriptConsumerStdinOptionalNoRead`、`TestScriptConsumerDefaultTimeoutEffective`、`TestScriptConsumerFanOutWithBuiltin`);cli 5 个(helper `TestHookWiringHelperProcess` + 4 个非 helper)。输出原样摘录入档,并写明差异说明:**report.md 既有口径 5+3(历史约定)≠ 当前 grep 事实 8+4**——差异按裁定 A 如实入档并路由,不追溯改写历史记录。

- [ ] **Step 2: 修正计数**

`docs/evidence/p2-acceptance.md` SC-006 行(约 :230)中:

原锚文本(`~:230`,单行):`（guard 4 测 + cli 组合根 3 测，Replay 钉镜像面——见 SC-006 条目）`,替换为:

```markdown
（guard 5 测 + cli 组合根 3 测，Replay 钉镜像面——见 SC-006 条目；计数口径 P3 修正（007）：沿用 report.md 既有口径（历史约定，非 helper 测试脚本消费核心 5 个），与当前 grep 事实（guard 8 + cli 4 非 helper）存在差异，已按 007 裁定入档并路由）
```

(必须保持**单行**——该行是汇总表格行,多行粘贴会破坏表格;FR-103 最小化。)

- [ ] **Step 3: 一致性核对**

Run: `grep -n "guard 5 测\|guard 4 测" docs/changes/002-p2/report.md docs/evidence/p2-acceptance.md`
Expected: 两文件均为 5 测,无 4 测残留;差异说明(8+4)已入档。

Run: `git diff --stat`
Expected: 仅 `docs/evidence/p2-acceptance.md`(最小化,FR-103)。

- [ ] **Step 4: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ cmd/`
Expected: 全绿/干净(FR-104:代码零改动佐证)。

```bash
git add docs/evidence/p2-acceptance.md
git commit -m "docs(acceptance): SC-006 钩子测试计数修正 4+3→5+3+口径入档(007 FR-101..103)"
```
