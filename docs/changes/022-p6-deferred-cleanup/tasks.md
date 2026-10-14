# 022-p6-deferred-cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps are plain bold step headings — no checkbox markers.

**Goal:** 清偿 017/019/020/021 四变更台账在账的 deferred minors（3 项注释 + 2 项错误文案 + 1 项 evidence 惯例）并同步 CLAUDE.md 文档，零行为面变更（错误文案字符串除外）。

**Architecture:** approved in chat (bounded)。方向裁定：FR-101 文案统一为英文——① `"kernel context required (021 FR-102: loop 权威真源)"` 逐字节不动（大量证据/台账引用），②③ 翻译，测试钉同变更内同步改；FR-102 补 `got %T`。注释项按各 Final 行原文更正；文档项按 FR-107 清单执行。

**Tech Stack:** Go 1.27；零第三方（NATS 唯一例外）；go vet + gofmt 兜底（golangci-lint/-race 环境不可用）。

**Spec:** `docs/specs/022-p6-deferred-cleanup/spec.md`

**Design:** approved in chat (bounded) — 方向裁定与文件锚点表见上方 Architecture；台账 Final 行原文为注释项的更正依据。

## Global Constraints

- `go test ./... -count=1` 全绿 + `go vet ./...` + `gofmt -l .` 干净 + `go run ./cmd/mulix lint layers` 0 违规，是每任务提交前置。
- TDD 先红后绿；注释/文档任务无 RED 形态——台账 Ruling 如实声明（021 前例：`task-N-report.md` 需**行首** `RED:`/`GREEN:` 纯行，build-complete guard 实证）。
- 提交：conventional 前缀；只 add 本任务文件；`.mulix/.runtime/*/state.yaml` 不入提交；commit + ledger 同调用完成。
- FR-101 的 ① 文案字符串逐字节不变；仅 ②③ 与测试钉改。
- 注释项（FR-103/104/105）零行为面——不触碰任何代码语句。
- 各 Final 行原文（更正依据，不得凭记忆转述）：
  - 019：session.go 桥失败注释理由不准确——sharedEventBus 仅前台运行，stdout 是用户可见面非 MCP 传输。
  - 017：memory_injection_test.go noMulixEntry 分支 `Name:"other"` 不承载语义（真机制=缺 SelfCommand→MCPServers 空面；Name 比对路径未执行）——注释澄清。
  - 017 Ruling：TestParseKernelKeys 补 `"a, ,b"` 纯空白段例——随下次触碰落（本变更即触碰）。

## Review Focus

1. ① 文案被 e2e/证据文档大量引用——改 ②③ 时若连带动了 ①，SC-003 行为零变更即破。钉：Task 1 GREEN 步含全包+e2e 重跑。
2. 新钉子串必须三态互斥（"service key missing" / "type mismatch" / "kernel context" 不互相误匹配）。钉：Task 1 负向四钉单跑。
3. 注释项若误改代码语句，26 包行为即变。钉：Task 2 后全量门禁零 FAIL。
4. evidence 基线 hash 必须真实存在。钉：Task 3 步内 `git cat-file -t <hash>` 返回 commit。
5. CLAUDE.md 清理后引用的路径必须真实存在。钉：Task 3 步内对每个保留引用 ls/grep 核验。

---

## Task 1: control 错误文案统一 + got %T（FR-101/102）

**Files:**
- Modify: `internal/control/controller.go:214-222`
- Test: `internal/control/lifecycle_test.go:498-515`（TestNewControllerRequiresKernelLoop ②③ 钉）

**Interfaces:**
- Consumes: 现行三态文案（controller.go:214/218/222）；现行钉子串（lifecycle_test.go:505 `"服务键缺失"` / :513 `"类型不符"`）。
- Produces: ② `"control: kernel service key missing: agentloop"`；③ `"control: kernel service key type mismatch: agentloop (got %T, want agentloop.Loop)"`；① 不变。lifecycle_test 钉子串同步为新值。

**Step 1: 先改测试钉（RED 源）**

lifecycle_test.go ②（:505）改为：

```go
	if _, err := NewController(o); err == nil || !strings.Contains(err.Error(), "service key missing") {
		t.Fatalf("missing key err = %v", err)
	}
```

③（:513）改为（补 got 断言——`Provide("agentloop", "not-a-loop")` 的值类型是 string）：

```go
	if _, err := NewController(o); err == nil || !strings.Contains(err.Error(), "type mismatch") ||
		!strings.Contains(err.Error(), "got string") {
		t.Fatalf("wrong type err = %v", err)
	}
```

**Step 2: 跑测试亲眼看它失败**

Run: `go test ./internal/control -run TestNewControllerRequiresKernelLoop -count=1`
Expected: FAIL——② 报 `missing key err = control: kernel 服务键缺失: agentloop`，③ 报 `wrong type err = control: kernel 服务键类型不符: ...`（旧中文文案不含新钉子串）。

**Step 3: 最小实现**

controller.go:218 改：

```go
		return nil, fmt.Errorf("control: kernel service key missing: agentloop")
```

controller.go:222 改：

```go
		return nil, fmt.Errorf("control: kernel service key type mismatch: agentloop (got %T, want agentloop.Loop)")
```

:214（①）不动。

**Step 4: 跑测试确认通过**

Run: `go test ./internal/control -run TestNewControllerRequiresKernelLoop -count=1 && go test ./internal/control ./internal/e2e -count=1`
Expected: 全 ok（e2e 一并跑——其失败路径依赖 ① 文案，确认未受牵连）。

**Step 5: 全量门禁 + commit**

Run: `go build ./... && go test ./... -count=1 && go vet ./... && gofmt -l .`
Expected: 全绿/干净。
Commit: `fix(control): loop 服务键错误文案英文化+补 got %T——三态钉同步(022 FR-101/102;T1)`

---

## Task 2: 注释清偿三处 + ParseKernelKeys 空白段例（FR-103/104/105 + 017 rider）

**Files:**
- Modify: `internal/cli/session.go:261-262`（FR-103）
- Modify: `internal/control/memory_injection_test.go:350-352 与 362-364 注释`（FR-104）
- Modify: `internal/cli/teardown_test.go:22-24`（FR-105）
- Test: `internal/tools/mulixkernel/mulixkernel_test.go:168 前`（017 rider）

**Interfaces:**
- Consumes: Global Constraints 中的三条 Final 行原文。
- Produces: 无接口变更（纯注释 + 一个测试段例）。

**Step 1: FR-103 session.go 桥失败注释**

session.go:261-262 现文：

```go
	// 镜像→总线桥(018 FR-102:单真源=cfg.Eventbus——CLI flag 退役):
	// 进程级单例(按工作区键);openSession 失败=桥关闭(如实记录)。
```

改为（按 019 Final 行更正理由）：

```go
	// 镜像→总线桥(018 FR-102:单真源=cfg.Eventbus——CLI flag 退役):
	// 进程级单例(按工作区键);桥不可用不阻断会话——sharedEventBus 仅前台
	// 运行,stdout 是用户可见面而非 MCP 传输(019 Final 裁定)。
```

**Step 2: FR-104 noMulixEntry 注释澄清**

memory_injection_test.go:362-364 现注释：

```go
		// 017 FR-102:第二合取项单独否决路径——keys 非空但注册面无 mulix
		// 条目(无 serve 消费者)→ env 不注入(hasMulixRegistryEntry 门控钉)。
```

在其后补一行（按 017 Final 行澄清 Name 不承载语义）：

```go
		// 注:本分支 Name:"other" 不承载语义——真机制=缺 SelfCommand→
		// MCPServers 空面;Name 比对路径未执行(017 Final 裁定)。
```

**Step 3: FR-105 teardown_test 陈旧注释**

teardown_test.go:22-24 现文：

```go
	// 门控关会话(cfg 无 [eventbus])——经 assembleController 不可轻启,
	// 残留清理落 sharedEventBus 邻位:直调形态以 openSession 同函数核验。
	// 实现者以最小可达锚核验(见 Step 3 注)。
```

改为（删除对已不存在计划步骤的引用）：

```go
	// 门控关会话(cfg 无 [eventbus])——经 assembleController 不可轻启,
	// 残留清理落 sharedEventBus 邻位:直调形态以 openSession 同函数核验。
```

**Step 4: 017 rider——ParseKernelKeys 补纯空白段例**

mulixkernel_test.go:168 前（`}` 闭合前）补：

```go
	if got := ParseKernelKeys("a, ,b"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ParseKernelKeys inner-blank = %v, want [a b]", got)
	}
```

Run: `go test ./internal/tools/mulixkernel -run TestParseKernelKeys -count=1`
Expected: PASS（TrimSpace+滤空段 017 已实现——本段例为覆盖钉非缺陷修复，RED 不适用，台账如实记录实际观察；若意外 FAIL 则为真实缺陷发现，立即 Ruling 入账并转修）。

**Step 5: 全量门禁 + commit**

Run: `go build ./... && go test ./... -count=1 && go vet ./... && gofmt -l .`
Expected: 全绿/干净（注释零行为面实证）。
Commit: `docs(control,cli): deferred 注释清偿——桥失败理由/noMulixEntry 语义/teardown 陈旧引用+空白段例(022 FR-103/104/105;T2)`

---

## Task 3: 文档面——evidence 基线 hash + CLAUDE.md 同步（FR-106/107）

**Files:**
- Modify: `docs/evidence/021-loop-plugin-regression.md`（头部补基线 hash）
- Create: `docs/evidence/022-deferred-cleanup.md`
- Modify: `CLAUDE.md`（FR-107）

**Interfaces:**
- Consumes: Task 1/2 的提交 hash（022 evidence 内容引用）。
- Produces: SC-004/SC-005 的证据载体。

**Step 1: 021 evidence 头部补基线**

`docs/evidence/021-loop-plugin-regression.md` 环境节补一行（hash 须经 `git cat-file -t` 验证存在）：

```markdown
- 基线: 8dc3737（021 变更 diff 起点；020 收官提交）
```

补注一行（评审员建议采纳：与「① 逐字节不动」同口径——021 证据文档负向四钉表中 ②③ 期望文案已于本变更英文化，时点记录仍为真）：

```markdown
- 注:负向四钉表 ②③ 期望文案（服务键缺失/类型不符）已于 022 T1 英文化，本表为 21b91e4 时点记录。
```

**Step 2: 022 evidence 落盘**

创建 `docs/evidence/022-deferred-cleanup.md`：头部环境节含基线 hash（Task 1 开始前一提交，`git rev-parse --short HEAD` 实测）；正文=门禁命令+退出码表、六项清偿逐条对照（台账 Final 行 → 落点 → 提交 hash）。

**Step 3: CLAUDE.md 同步**

1. 工作区在案两处 NATS 更新（kernel 不落盘→内嵌 NATS JetStream；零第三方→NATS 唯一例外）随本提交入库（010 裁定沉淀，不另改内容）。
2. §1「当前阶段：P4a 内核升级（change 010，进行中）」更新为实际状态（P1→P6 全弧收官：021 loop 插件化 kernel 权威解析归档；无 active change）。
3. 删死指针：所有「见 AGENTS.md §历史阶段」「.mulix/memory/history.md」引用移除或改为真实存在路径（§附：历史阶段 节内两处 + §1 一处——以 grep 实测为准，逐处核验后再删）。

Run: `grep -n "history.md\|§历史阶段" CLAUDE.md ; ls AGENTS.md .mulix/memory/`
Expected: 清理后 grep 零命中或仅余指向真实存在内容的引用；AGENTS.md 实为 20 行流程文件、.mulix/memory/ 仅 constitution.md（未填模板）——引用一律删除。

**Step 4: 门禁 + commit**

Run: `go build ./... && go test ./... -count=1 && gofmt -l .`
Expected: 全绿/干净（文档变更零行为面）。
Commit: `docs: 022 deferred 清扫收官——evidence 基线 hash 惯例+CLAUDE.md 死指针清理(022 FR-106/107;T3)`
