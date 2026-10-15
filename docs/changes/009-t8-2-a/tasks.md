# T8-2 裁定 A 落地与廉价测试补强批次 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** T8-② 产品裁定 A 落地(键集=启动快照语义钉死)+ P3 累积 14 条 deferred minors 的批次清偿(4 组分组提交),台账归零(除明示路由保留项)。

**Architecture:**(bounded,设计于 chat 并已批准)四任务按组组织:①T8-② 键集语义钉(新测试 + 裁定注释);②G1 lint 残留边缘修复(`splitLineComment` 行中 `/*...*/` 跳到收尾后继续收集代码);③G2 control 双重故障防护(Notice 发布内层 recover)+ Close 稳定性断言 + allowlist 断言抽共享 helper;④G3/G4 卫生组(注释/lineNo/死代码/fixture gate 增强/[006] 证据补全)。

**Tech Stack:** Go 标准库;既有测试基建(watcher 矩阵、lint fixture、guard 交叉断言)。

**Spec:** `docs/specs/009-t8-2-a/spec.md`

**Design:** approved in chat (bounded) —— 要点:①键集测试白盒断言 `c.instructionSnaps` + notice 双面;②残留边缘修法 = 行中 `/*` 分支跳到 `*/` 后**继续收集代码**(当前返回前缀丢后缀=假阴性根因);③双重故障 = 内层 `recover` 包 Notice 发布;④[006] 原始行从全局镜像(`~/.mulix/sessions/90ca64b1.../events.jsonl`,跨日存活已实证)提取粘贴。

## Global Constraints

- FR-107:行为契约不变(仅解析准确性/收口纪律/注释卫生);各组既有测试零回归。
- FR-108:全量门禁绿;分组提交,diff 范围与分组一一对应。
- FR-109:三项维持路由(SC-001 全 allow 委派补测、start/Close 并发窗口、flake 观察)在 report 中标注,不在本变更实施。
- 最小化:每项改动只触及本项文件;每任务 `go test ./... -count=1` 全绿再 commit。

## Review Focus

- 键集冻结测试的时序确定性:文件 B 创建后必须等 ≥2 个轮询周期(≥4s)再断言——过短可能假绿。
- 残留边缘修法与多行状态位交互:`/* c */ code` 行内终止后接代码必须按代码解析;未终止 `/*` 仍转多行状态(008 行为保持)。
- 双重故障防护不吞真 panic:内层 recover 只包 Notice 发布,外层逻辑(兜底裁决/在飞清理)不受影响。
- lineNo 修正不破坏既有断言:半行场景无超限段(偏移 0)。
- 死代码清除后测试仍编译(gofmt/vet)。

---

## Task 1: T8-② 键集语义钉(裁定 A 落地)

**Files:**
- Modify: `internal/control/controller.go`(`startInstructionWatcher` 注释)
- Test: `internal/control/watcher_test.go`(追加)

**Interfaces:**
- Consumes: 既有 `writeInstructionFile`/`newTestControllerCapturingBoot`/`engine.NewFakeHost`(watcher_test.go:26,241-262);`c.instructionSnaps`(controller.go:162)。
- Produces: `TestWatcherKeySetFrozenAtStartup`(键集冻结钉);`startInstructionWatcher` 裁定 A 注释。零生产行为变更。

- [ ] **Step 1: 写测试(语义钉——现状即满足,无运行期 RED;台账记 Ruling)**

追加到 `internal/control/watcher_test.go`:

```go
// TestWatcherKeySetFrozenAtStartup 钉 T8-② 裁定 A(009 FR-101):监控键集
// = 启动时快照——会话中新建的指令文件不进入键集、不产生提醒;既有键变更
// 照常提醒(快照保持,下会话生效——新文件属下个会话)。
func TestWatcherKeySetFrozenAtStartup(t *testing.T) {
	ws := t.TempDir()
	writeInstructionFile(t, ws, "CLAUDE.md", "# rules")
	fake := engine.NewFakeHost(engine.FakeScript{})
	c := newTestControllerCapturingBoot(t, fake, &recordingSink{}, &engine.BootConfig{}, func(o *Options) {
		o.Workspace = ws
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	c.mu.Lock()
	snaps, iw := c.instructionSnaps, c.instrWatch
	c.mu.Unlock()
	if iw == nil || len(snaps) != 1 {
		t.Fatalf("baseline: snaps=%v iw=%v, want 1 key + watcher", snaps, iw)
	}
	// 会话中新建文件 B(不在基线键集)——等待 ≥2 个轮询周期。
	writeInstructionFile(t, ws, "EXTRA.md", "# new file mid-session")
	time.Sleep(5 * time.Second)
	c.mu.Lock()
	snaps2 := c.instructionSnaps
	c.mu.Unlock()
	if len(snaps2) != 1 {
		t.Fatalf("key set must stay frozen at startup snapshot (裁定 A): %v", snaps2)
	}
	// 镜像无文件 B 相关 notice。
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	for _, ev := range mirror {
		if nEv, ok := ev.(event.NoticeEvent); ok && strings.Contains(nEv.Content, "EXTRA.md") {
			t.Fatalf("new file must not be monitored (裁定 A): %s", nEv.Content)
		}
	}
	// 既有键变更照常提醒(无回归)。
	writeInstructionFile(t, ws, "CLAUDE.md", "# rules edited")
	time.Sleep(5 * time.Second)
	mirror2, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	sawEdit := false
	for _, ev := range mirror2 {
		if nEv, ok := ev.(event.NoticeEvent); ok && strings.Contains(nEv.Content, "CLAUDE.md") {
			sawEdit = true
		}
	}
	if !sawEdit {
		t.Fatal("existing key change must still be noticed (no regression)")
	}
}
```

(注:两个 5s 等待使测试 ~10s——与 watcher 矩阵的 tick 时窗一致;镜像读取路径经 `c.Mirror()`,与 004/008 测试同款。)

- [ ] **Step 2: 跑测试验证通过(语义钉形态)**

Run: `go test ./internal/control/ -run TestWatcherKeySetFrozenAtStartup -count=1 -v`
Expected: PASS(现状即满足裁定 A;本测试为语义固化——台账记 Ruling「无运行期 RED,固化形态」)。

- [ ] **Step 3: 裁定注释**

`startInstructionWatcher` 函数注释前加:

```go
// 产品裁定(009 T8-②,裁定 A):监控键集 = 启动时快照——会话中新建的
// 指令文件不纳入监控、不产生提醒(新文件下会话生效,与「快照保持」
// 会话冻结哲学同构)。键集冻结语义由 TestWatcherKeySetFrozenAtStartup 钉。
```

- [ ] **Step 4: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净

```bash
git add internal/control/controller.go internal/control/watcher_test.go
git commit -m "test(control): watcher 键集冻结语义钉——裁定 A 落地(009 FR-101;T8-②)"
```

---

## Task 2: G1 lint 残留边缘修复

**Files:**
- Modify: `internal/lint/lint.go`(`splitLineComment` 重写为段积累器 + `scanImports` 改 `FindAllStringSubmatch`)
- Test: `internal/lint/lint_test.go`(追加 3 子用例)

**Interfaces:**
- Consumes: 008 的 `splitLineComment`/`scanImports`(008 终审修正后形态)。
- Produces: 行内 `/*...*/` 终止后接代码按代码解析(008 终审失败形态转绿);整行块注释(含含引号串者)返回 wholeComment=true(008 不变量保持);块内多引号串行全部提取(`FindAllStringSubmatch`)。008 既有 6 fixture 零回归。

- [ ] **Step 1: 写失败测试**

在 `TestScanImportsCommentEdges` 的 `cases` 切片末尾追加 3 用例:

```go
		{
			name: "行内块注释终止后接代码(008 终审失败形态)",
			lines: []string{
				"package p", "import (", "	/* c */ \"internal/x\"", ")", "var _ = 1",
			},
			want: []string{"internal/x"},
		},
		{
			name: "行内块注释前后均有代码",
			lines: []string{
				"package p", "import (", "	\"internal/cache\" /* 历史注 */ \"internal/y\"", ")", "var _ = 2",
			},
			want: []string{"internal/cache", "internal/y"},
		},
		{
			name: "整行块注释含引号串不假 import",
			lines: []string{
				"package p", "import (", "	/* 见 \"internal/guard\" 文档 */", "	\"internal/cache\"", ")",
			},
			want: []string{"internal/cache"},
		},
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/lint/ -run TestScanImportsCommentEdges -count=1 -v`
Expected: FAIL——「行内块注释终止后接代码」与「前后均有代码」现状返回 code=前缀(丢 `*/` 后代码)→ imports 缺失;「整行块注释含引号串」现状把原始行当代码 → 假 import `internal/guard`。三处与 008 不变量(「注释既不假闭合也不假 import」)矛盾。

- [ ] **Step 3: 实现**

(a) `splitLineComment` 重写为**段积累器**(跨 `/*...*/` 收集代码段;全注释行返回 wholeComment=true):

```go
// splitLineComment 把一行拆为代码部分与注释:代码段 = 引号外的非注释区间
// 之拼接(行注释 // 至行尾终止;块注释 /* 至 */ 跳过后继续收集)。
// 整行均为注释(代码段拼空)→ wholeComment=true。引号内 // 与 /* 不视为
// 注释起始。跨行块注释不在本函数职责内(调用方状态位处理 /* 开启且本行
// 无收尾的形态——返回 wholeComment=true,原始行由调用方弃用)。
func splitLineComment(line string) (code string, wholeComment bool) {
	var b []byte
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '"':
			inQuote = !inQuote
			b = append(b, line[i])
		case !inQuote && line[i] == '/' && i+1 < len(line) && line[i+1] == '/':
			i = len(line) // 行注释:至行尾
		case !inQuote && line[i] == '/' && i+1 < len(line) && line[i+1] == '*':
			end := strings.Index(line[i+2:], "*/")
			if end < 0 {
				return "", true // /* 开启且本行无收尾:调用方转多行状态
			}
			i = i + 2 + end + 1 // 越过 */;for 的 i++ 指向下一字节
		default:
			b = append(b, line[i])
		}
	}
	code = string(b)
	if strings.TrimSpace(code) == "" {
		return "", true // 全注释行(含「注释+空白」)
	}
	return code, false
}
```

(b) `scanImports` 的块内 import 提取改 `FindAllStringSubmatch`(同行多引号串全提取;008 既有 fixture 每代码行 ≤1 引号串,行为兼容):

```go
			for _, m := range quotedPathRe.FindAllStringSubmatch(code, -1) {
				refs = append(refs, importRef{Path: m[1], Line: i + 1})
			}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/lint/ -count=1 -v`
Expected: PASS(新 3 用例 + 008 的 6 用例 + lint 既有测试全绿)

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净

- [ ] **Step 5: Commit**

```bash
git add internal/lint/lint.go internal/lint/lint_test.go
git commit -m "fix(lint): 行内块注释终止后接代码——残留边缘修复(009 FR-102;G1)"
```

## Task 3: G2 control 组(双重故障防护 + Close 稳定性 + allowlist helper)

**Files:**
- Modify: `internal/control/controller.go`(`evaluateRuling` defer 段)、`internal/guard/allowlist_cross_test.go`(helper 抽取)
- Test: `internal/control/leader_panic_test.go`(追加双重故障测试)、`internal/control/watcher_test.go`(追加稳定性断言)

**Interfaces:**
- Consumes: 004 的 `evaluateRuling`(defer 段 Notice 发布);008 的交叉断言测试。
- Produces: ①双重故障防护(内层 recover);②`assertCloseMirrorStable(t, c, c2)` 或同型稳定性断言;③guard 测试 helper `assertLintAllowSubset(t, allow map[string]bool)`(子集断言与判别力共用)。

- [ ] **Step 1: 写失败测试(双重故障 + 稳定性)**

(a) 追加到 `internal/control/leader_panic_test.go`:

```go
// TestLeaderPanicDoubleFaultProtected 钉 009 FR-103(G2):原始 panic 源于
// publish 自身时,收口路径的 Notice 发布(第二次 publish)被内层防护捕获
// ——双重故障不得使 panic 逃出 defer(逃出 = goroutine 崩溃 = 测试二进制
// 失败,RED)。判别形态:Sink 包装注入——Publish 在 NoticeEvent 含
// "leader panic" 时 panic(首 panic 即此形态:兜底 Notice 是首次 Notice
// 发布);修复前 panic 逃出 defer → RED;修复后内层 recover 捕获 → 绿。
func TestLeaderPanicDoubleFaultProtected(t *testing.T) {
	base := &recordingSink{}
	sink := &noticePanicSink{Sink: base}
	c := newTestControllerWithPolicyRules(t, nil, sink,
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		panic("gate exploded (double-fault probe)")
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ans, err := postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-double-fault")
	if err != nil {
		t.Fatalf("double-fault path must answer (panic must not escape): %v", err)
	}
	if ans["behavior"] != "deny" {
		t.Fatalf("behavior = %v, want deny", ans["behavior"])
	}
	if len(base.events) == 0 {
		t.Fatal("sink must record pre-panic events (panicking notice intentionally never forwarded)")
	}
}

// noticePanicSink 包装 Sink:NoticeEvent 含 "leader panic" 时 Publish panic
// (模拟「publish 自身 panic」的双重故障源——判别注入)。
type noticePanicSink struct {
	Sink
	events []any
}

func (s *noticePanicSink) Publish(ev any) {
	s.events = append(s.events, ev)
	if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "leader panic") {
		panic("publish exploded (double-fault injection)")
	}
	s.Sink.Publish(ev)
}
```

(判别形态说明:Sink 注入经 `recordingSink` 同款接口——检查 `recordingSink` 是否实现 `Sink` 全接口,包装结构体以嵌入复用;若 `Publish` 签名不同以实际为准。事件字段 `base.events` 若 recordingSink 已有事件存储,直接复用其读取方法替代自有切片。)

(b) 追加到 `internal/control/watcher_test.go`:

```go
// TestCloseMirrorEventCountStable 钉 009 FR-104(G2):Close 后镜像事件
// 计数稳定——无 publish 越过 Close(直接断言,008 时为传递承载)。
func TestCloseMirrorEventCountStable(t *testing.T) {
	ws := t.TempDir()
	writeInstructionFile(t, ws, "CLAUDE.md", "# rules")
	fake := engine.NewFakeHost(engine.FakeScript{})
	c := newTestControllerCapturingBoot(t, fake, &recordingSink{}, &engine.BootConfig{}, func(o *Options) {
		o.Workspace = ws
	})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	n1 := len(mirror)
	time.Sleep(200 * time.Millisecond) // 越过窗口探测
	mirror2, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if len(mirror2) != n1 {
		t.Fatalf("mirror grew after Close: %d -> %d (publish crossed Close)", n1, len(mirror2))
	}
}
```

- [ ] **Step 2: 跑测试验证**

Run: `go test ./internal/control/ -run 'TestLeaderPanicDoubleFaultProtected|TestCloseMirrorEventCountStable' -count=3 -v`
Expected: **`TestLeaderPanicDoubleFaultProtected` FAIL(RED)**——修复前 panic 逃出 defer(goroutine 崩溃/回调无应答),失败形态如实记台账;`TestCloseMirrorEventCountStable` PASS(现状传递承载,直接断言为显式钉)。全量绿移至 Step 4 门禁。

- [ ] **Step 3: 实现**

(a) `evaluateRuling` defer 段的 Notice 发布包内层防护:

```go
			func() {
				defer func() { _ = recover() }() // 双重故障防护(009 FR-103):
				c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", // publish 自身
					Content: fmt.Sprintf("审批裁决 leader panic(fail-closed 兜底): %v", r)}) // panic 时不再升级
			}()
```

(b) `internal/guard/allowlist_cross_test.go` 抽 helper(两个测试共用;判别力子测试改用 helper 的注入变体):

```go
// assertLintAllowSubset 断言 allow ⊆ repolintFlagAllowlist(子集检查与
// 越界报告;子集测试与判别力测试共用——009 FR-104 G2 helper 抽取)。
func assertLintAllowSubset(t *testing.T, allow map[string]bool) {
	t.Helper()
	for f := range allow {
		if !repolintFlagAllowlist[f] {
			t.Errorf("lint allowlist 越界: %q 不在 P1 repolint 清单(双源漂移——按既定程序同步两侧并过评审)", f)
		}
	}
}
```

(子集测试体改调 `assertLintAllowSubset(t, allow)`;判别力测试保持注入副本形态——helper 抽取限于消除重复的子集循环。)

- [ ] **Step 4: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净(既有 panic 矩阵/watcher 矩阵零回归)

```bash
git add internal/control/controller.go internal/control/leader_panic_test.go internal/control/watcher_test.go internal/guard/allowlist_cross_test.go
git commit -m "test(control,guard): 双重故障防护+Close 稳定性断言+allowlist helper(009 FR-103/104;G2)"
```

---

## Task 4: G3/G4 卫生组(注释/lineNo/死代码/fixture gate/[006] 证据)

**Files:**
- Modify: `internal/jsonl/jsonl.go`(ReadAll doc)、`internal/control/controller.go`(gateRuling.turn 注释)、`internal/control/leader_panic_test.go`(死代码)、`internal/lint/lint_test.go`(fixture gate 标记断言——005 的 fixture gate 在 decoder_test.go,此处指 lint fixture?核对:003 deferred 指 decoder_test.go 的 fixture gate)、`internal/engine/decoder_test.go`(fixture gate 标记)、`internal/memory/memory.go`(lineNo)、`docs/evidence/p3-watcher-retest.md`(t2 标注 + 原始行)
- Test: `internal/engine/decoder_test.go`(fixture gate 增强)

**Interfaces:**
- Consumes: 各前序变更的既有形态;[006] 全局镜像 `~/.mulix/sessions/90ca64b1.../events.jsonl`(跨日存活已实证)。
- Produces: 注释/文档与实现一致;死代码消失;fixture gate 形状增强;[006] 证据补全。

- [ ] **Step 1: 注释与文档修正(3 处)**

①`internal/jsonl/jsonl.go` `ReadAll` doc:「检测到损坏尾部时返回错误」→「检测到损坏段(尾部半行/超限行)时返回错误」。
②`internal/control/controller.go` `gateRuling.turn` 注释:「0=异常兜底未关联回合」→「0=未关联(防御兜底,不入缓存)」。
③`docs/evidence/p3-watcher-retest.md` t2 行:「**t1 后首个 2s tick 内(≤13:36:56)**」→「**t1 后首个 2s tick 内(≤13:36:56,推断值——由 notice 事件序位 seq=2 先于引擎首事件推导)**」。

- [ ] **Step 2: lineNo 修正**

`internal/memory/memory.go` 半行段:

```go
		segments = append(segments, damagedSegment{
			lineNo: len(lines) + len(dmg.Oversized) + 1, raw: half, reason: "damaged tail (half line)",
		})
```

Run: `go test ./internal/memory/ -count=1`
Expected: PASS(半行场景无超限段,偏移 0,既有断言不变——若变动按新事实改断言并记 Ruling)

- [ ] **Step 3: 测试卫生(2 处)**

①删除 `internal/control/leader_panic_test.go` 末尾的 `var _ = httptest.NewRecorder ...` 死代码及 `httptest` import(若无他用)。
②`internal/engine/decoder_test.go` 的 `TestDecodeToolResultArrayFixture` 增加已知真实标记断言(sub_session_id 来自 P2 冒烟真实子会话):

```go
	sawMarker := false
	for _, ev := range events {
		if ev.Kind == KindToolResult && ev.ToolResult != nil {
			for _, b := range ev.ToolResult.Blocks {
				if b.Type == "tool_result" && strings.Contains(b.Content, "sub_session_id") {
					sawMarker = true
				}
			}
		}
	}
	if !sawMarker {
		t.Fatal("fixture must contain the known real marker sub_session_id (shape-gate hardening, 009 FR-107)")
	}
```

Run: `go test ./internal/engine/ -run TestDecodeToolResultArrayFixture -count=1 -v`
Expected: PASS(现有样本含 sub_session_id——grep 已核)

- [ ] **Step 4: [006] 证据补全**

Run: `grep -n "turn_started\|notice" ~/.mulix/sessions/90ca64b107f93d8df10508a224385364/events.jsonl | head -2`
(以 `~/.mulix/sessions/90ca64b1*/events.jsonl` 实际路径为准;会话若已清理则按 spec Edge Case 如实标注。)

将 seq=1(turn_started)与 seq=2(notice)两行原始 JSONL 粘贴到 `docs/evidence/p3-watcher-retest.md` 时间线表之后,新增小节「原始事件行(粘贴)」;t2 标注见 Step 1③。

- [ ] **Step 5: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ cmd/`
Expected: 全绿/干净

```bash
git add internal/jsonl/jsonl.go internal/control/controller.go internal/control/leader_panic_test.go internal/engine/decoder_test.go internal/memory/memory.go docs/evidence/p3-watcher-retest.md
git commit -m "chore: 廉价补强批次 G3/G4——注释/lineNo/死代码/fixture gate/证据补全(009 FR-105..107)"
```
