# P5a 内核升级 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** BM25 核心抽取到 textutil(memory/sessionx 共用)+ sessionx.Fork + agentloop.Replay + sessionx.SearchEvents + CLI 接线 + 补强批次清偿。

**Architecture:**(architectural,设计文档已批准:`.mulix/.runtime/013-p5a-fork-replay-search/specs/2026-10-12-p5a-design.md`)①BM25 核心(tokenize/index/score)从 memory/bm25.go 抽取到 textutil/bm25.go(memory 依赖 textutil ✓ 矩阵;memory 的 index 改为 textutil.BM25Index 包装);②sessionx.Fork(src, root, newID, atSeq)——事件副本原样至 atSeq + meta{parent_id, seed_length};③agentloop.Replay(ctx, events, cb)——类型化事件切片经 Callbacks 重放(只读);④sessionx.SearchEvents(store, query, k)——BM25 检索镜像事件文本面;⑤CLI fork/search 命令接线;⑥补强批次清偿。

**Tech Stack:** Go 标准库;既有测试基建。

**Spec:** `docs/specs/013-p5a-fork-replay-search/spec.md`

**Design:** `.mulix/.runtime/013-p5a-fork-replay-search/specs/2026-10-12-p5a-design.md`

## Global Constraints

- FR-105 零第三方新增;行为零变更(既有 memory/sessionx 测试零回归——抽取重构的同构保证)。
- fork fail-closed(atSeq 非法);replay 只读;search 空结果不报错。
- 每任务 `go test ./... -count=1` 全绿再 commit。

## Review Focus

- BM25 抽取同构:memory 既有测试零改动全绿(tokenize/bm25 语义逐字节)。
- fork 副本一致性:事件字节原样(jsonl 行原样复制);atSeq 边界(1/末尾/越界)。
- replay 只读:ReadAllEvents 前后计数不变。
- search 空结果:不报错。

---

## Task 1: BM25 核心抽取到 textutil

**Files:**
- Create: `internal/textutil/bm25.go`
- Modify: `internal/memory/bm25.go`(改为 textutil 包装)
- Modify: `internal/lint/rules.go`(memory 矩阵增 `internal/textutil`——textutil 仍 leaf)
- Test: `internal/textutil/bm25_test.go`(Create——从 memory 测试迁移核心用例)

**Interfaces:**
- Produces: `textutil.BM25Index`(exported:type + NewBM25Index/Add/Remove/Score/Tokenize——exported 供 sessionx/memory 共用)。

- [ ] **Step 1: 写失败测试**

Create `internal/textutil/bm25_test.go`:

```go
package textutil

import (
	"reflect"
	"testing"
)

// TestTokenize 钉分词语义(CJK bigram/拉丁词/混合)——从 memory 测试迁移。
func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"记住本项目用 pnpm", []string{"记住", "住本", "本项", "项目", "目用", "pnpm"}},
		{"hello world", []string{"hello", "world"}},
		{"单字", []string{"单字"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := Tokenize(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Tokenize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestBM25IndexScore 钉索引语义:命中正分;不命中不在结果。
func TestBM25IndexScore(t *testing.T) {
	ix := NewBM25Index()
	ix.Add("d1", "缓存穿透问题")
	ix.Add("d2", "权限治理")
	scores := ix.Score("缓存")
	if len(scores) != 1 {
		t.Fatalf("scores = %v, want 1 hit", scores)
	}
	if _, ok := scores["d1"]; !ok {
		t.Fatal("d1 must score")
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/textutil/ -count=1`
Expected: FAIL(包无 BM25——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/textutil/bm25.go`:将 memory/bm25.go 的 tokenize/termSet/isCJK/index 全量迁移,符号导出(Tokenize/termSet→小写保持、BM25Index/NewBM25Index/Add/Remove/Score),参数常量同值。`internal/memory/bm25.go` 改为 textutil **wrapper**(memory 保留 unexported index/newIndex/add/remove/score/tokenize shim 委托 textutil——既有测试直调 unexported 符号,alias 会破坏测试)。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/textutil/ ./internal/memory/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿(memory 既有测试零改动全绿=抽取同构;lint layers 0 违规=矩阵已登记)

- [ ] **Step 5: Commit**

```bash
git add internal/textutil/ internal/memory/ internal/lint/rules.go
git commit -m "refactor(textutil): BM25 核心抽取——memory/sessionx 共用(013 FR-104 前置;P5a T1)"
```

---

## Task 2: sessionx.Fork

**Files:**
- Modify: `internal/sessionx/sessionx.go`(Fork 函数)
- Test: `internal/sessionx/sessionx_test.go`(追加)

**Interfaces:**
- Consumes: 既有 Create/ReadAllEvents/Store.Open/SetParentID。
- Produces: `func Fork(src *Store, root string, newID string, atSeq int64) (*Store, error)`。

- [ ] **Step 1: 写失败测试**

追加到 `internal/sessionx/sessionx_test.go`:

```go
// TestForkCopiesEventsAndMeta 钉 013 FR-101:fork 副本至 atSeq+meta 标注+
// 源不受影响+独立追加。
func TestForkCopiesEventsAndMeta(t *testing.T) {
	root := t.TempDir()
	src, err := Create(root, Options{ID: "src", Workspace: "w"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := src.Append(TurnStartedEvent{Seq: int64(i + 1), SessionID: "src"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	src.Close()

	forked, err := Fork(src, root, "forked", 2)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	// fork 产物:2 事件副本。
	events, err := forked.ReadAllEvents()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("forked events = %d, want 2", len(events))
	}
	// meta 标注(经 ReadMeta——Store 无 parentID 字段)。
	fmeta, _ := forked.ReadMeta()
	if fmeta["parent_id"] != "src" {
		t.Fatalf("parent_id = %v, want src", fmeta["parent_id"])
	}
	if fmeta["seed_length"] == nil {
		t.Fatal("seed_length missing in meta")
	}
	// 源不受影响。
	events2, _ := src.ReadAllEvents()
	if len(events2) != 4 {
		t.Fatalf("src events changed: %d", len(events2))
	}
	// 独立追加互不干扰。
	if err := forked.Append(TurnStartedEvent{Seq: 5, SessionID: "forked"}); err != nil {
		t.Fatalf("append forked: %v", err)
	}
	events3, _ := forked.ReadAllEvents()
	events4, _ := src.ReadAllEvents()
	if len(events3) != 3 || len(events4) != 4 {
		t.Fatalf("independent append failed: forked=%d src=%d", len(events3), len(events4))
	}
}

// TestForkFailClosed 钉 013 FR-101:atSeq 非法 fail-closed。
func TestForkFailClosed(t *testing.T) {
	root := t.TempDir()
	src, _ := Create(root, Options{ID: "src2", Workspace: "w"})
	src.Append(TurnStartedEvent{Seq: 1, SessionID: "src2"})
	src.Close()
	if _, err := Fork(src, root, "f1", 0); err == nil {
		t.Fatal("atSeq=0 must fail-closed")
	}
	if _, err := Fork(src, root, "f2", 99); err == nil {
		t.Fatal("atSeq beyond end must fail-closed")
	}
}
```

(注:Store 字段访问以实际落位为准;parentID/meta 断言路径以 sessionx 既有 meta 机制为准。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/sessionx/ -run TestFork -count=1`
Expected: FAIL(Fork 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/sessionx/sessionx.go` 追加:

```go
// Fork 从源镜像复制事件(至 atSeq,1 起含)到新会话;meta 标注 parent_id/
// seed_length。源会话只读不受影响。atSeq 非法(≤0 或超末尾)→ fail-closed。
// engine_session_id 不继承(新会话新引擎实例)。
func Fork(src *Store, root string, newID string, atSeq int64) (*Store, error) {
	events, err := src.ReadAllEvents()
	if err != nil {
		return nil, fmt.Errorf("sessionx: fork read: %w", err)
	}
	if atSeq < 1 || int(atSeq) > len(events) {
		return nil, fmt.Errorf("sessionx: fork atSeq %d out of range [1, %d]", atSeq, len(events))
	}
	srcMeta, _ := src.ReadMeta()
	forked, err := Create(root, Options{
		ID:           newID,
		Workspace:    src.workspace,
		Model:        src.meta["model"].(string),
		PinnedModel:  src.meta["pinned_model"].(string),
	})
	if err != nil {
		return nil, fmt.Errorf("sessionx: fork create: %w", err)
	}
	for i := int64(0); i < atSeq; i++ {
		if err := forked.writer.Append(<事件原始行——见注>); err != nil {
			return nil, fmt.Errorf("sessionx: fork append: %w", err)
		}
	}
	forked.SetParentID(src.id)
	forked.meta["seed_length"] = atSeq
	_ = srcMeta
	return forked, nil
}
```

(注:①事件**原样行复制**需要源 jsonl 原始行——ReadAllEvents 返回类型化事件(丢失原始字节);实现形态:**白盒直读源镜像文件行**(os.ReadFile + strings.Split,取前 atSeq 行原样 append 到 forked.writer——文件是镜像同格式)——原样行保真优于类型化往返;②src.workspace/meta 字段访问以实际落位为准;③seed_length 落 meta。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/sessionx/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/sessionx/
git commit -m "feat(sessionx): Fork——事件副本+meta 标注(013 FR-101;P5a T2)"
```

---

## Task 3: agentloop.Replay

**Files:**
- Modify: `internal/agentloop/agentloop.go`(Replay 函数)
- Test: `internal/agentloop/agentloop_test.go`(追加)

**Interfaces:**
- Consumes: sessionx.Store.ReadAllEvents;既有 Callbacks。
- Produces: `func Replay(ctx context.Context, events []engine.Event, cb Callbacks) error`(包级函数——事件流由调用方从镜像读出;不 import sessionx——依赖约束)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentloop/agentloop_test.go`:

```go
// TestReplayRoutesMirrorEvents 钉 013 FR-102:镜像事件流经 Callbacks 重放
// (路由序列对齐);replay 零镜像追加(只读语义——由调用方保证,本测试核
// 对 replay 函数自身无写面)。
func TestReplayRoutesMirrorEvents(t *testing.T) {
	cb := &recordCallbacks{}
	events := []engine.Event{
		{Kind: engine.KindInit},
		{Kind: engine.KindAssistant},
		{Kind: engine.KindResult},
	}
	if err := Replay(context.Background(), events, cb); err != nil {
		t.Fatalf("replay: %v", err)
	}
	want := []string{"init", "assistant", "result"}
	if len(cb.route) != len(want) {
		t.Fatalf("route = %v, want %v", cb.route, want)
	}
	for i := range want {
		if cb.route[i] != want[i] {
			t.Fatalf("route[%d] = %q, want %q", i, cb.route[i], want[i])
		}
	}
	// 只读语义:replay 不调 OnEngineClosed(非引擎关闭——历史重放)。
	if cb.closed {
		t.Fatal("replay must not fire OnEngineClosed (只读重放)")
	}
}
```

(注:replay 不触发 OnEngineClosed——它只在引擎流关闭时触发,历史重放没有"关闭"语义;FR-102 的只读语义由调用方保证——agentloop 包零写面。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/agentloop/ -run TestReplay -count=1`
Expected: FAIL(Replay 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/agentloop/agentloop.go` 追加:

```go
// Replay 将历史事件流经 Callbacks 重放(只读——零镜像追加;观测面)。
// 事件序列经同一 dispatch 路由(与引擎流语义一致);OnEngineClosed 不触发
// (非引擎关闭——历史重放无关闭语义)。FR-102。
func Replay(ctx context.Context, events []engine.Event, cb Callbacks) error {
	for _, ev := range events {
		switch ev.Kind {
		case engine.KindInit:
			cb.OnInit(nil, ev) // 历史重放无宿主——host 收 nil
		case engine.KindAssistant:
			cb.OnAssistant(ev)
		case engine.KindToolResult:
			cb.OnToolResult(ev)
		case engine.KindResult:
			cb.OnResult(ev)
		case engine.KindTrace:
			cb.OnTrace(ev)
		case engine.KindPermissionDenied:
			cb.OnPermissionDenied(ev)
		case engine.KindExit:
			cb.OnExit(ev)
		case engine.KindControlRequest:
			cb.OnControlRequest(nil, ev)
		case engine.KindHookResponse:
			cb.OnHookResponse(ev)
		default:
			// 未知 kind:跳过(与引擎流 dispatch default 一致)。
		}
	}
	return nil
}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/agentloop/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/agentloop/
git commit -m "feat(agentloop): Replay——镜像事件流经 Callbacks 重放(013 FR-102;P5a T3)"
```

---

## Task 4: sessionx.SearchEvents

**Files:**
- Modify: `internal/sessionx/search.go`(Create)
- Modify: `internal/lint/rules.go`(sessionx 矩阵增 textutil)
- Test: `internal/sessionx/search_test.go`(Create)

**Interfaces:**
- Consumes: textutil.BM25Index/Tokenize;Store.ReadAllEvents。
- Produces: `type EventHit struct { LineNo int64; Kind string; Excerpt string }` + `func SearchEvents(store *Store, query string, k int) ([]EventHit, error)`。

- [ ] **Step 1: 写失败测试**

Create `internal/sessionx/search_test.go`:

```go
package sessionx

import (
	"strings"
	"testing"
)

// TestSearchEventsHitsAndEmpty 钉 013 FR-103:文本面检索命中(序位+摘录);
// 空结果不报错。
func TestSearchEventsHitsAndEmpty(t *testing.T) {
	root := t.TempDir()
	st, err := Create(root, Options{ID: "search", Workspace: "w"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st.Append(AssistantTextEvent{Seq: 1, Text: "解决缓存穿透的方案"})
	st.Append(NoticeEvent{Seq: 2, Content: "无关内容"})
	st.Append(AssistantTextEvent{Seq: 3, Text: "另一段缓存雪崩"})
	hits, err := SearchEvents(st, "缓存穿透", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("must hit 缓存穿透")
	}
	if !strings.Contains(hits[0].Excerpt, "缓存穿透") {
		t.Fatalf("excerpt = %q", hits[0].Excerpt)
	}
	empty, err := SearchEvents(st, "不存在词汇xyz", 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty search = %v, %v; want empty no error", empty, err)
	}
}
```

(注:事件类型名/字段以 internal/event 实际定义为准;事件文本面提取(assistant_text.Text/tool_result.Content/notice.Content)按 kind 分支。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/sessionx/ -run TestSearchEvents -count=1`
Expected: FAIL(SearchEvents 未定义——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/sessionx/search.go`:

```go
// SearchEvents 镜像事件文本面 BM25 检索(013 FR-103):文本面提取
// (assistant_text/tool_result/notice 的内容字段)→ BM25 索引 → top-k。
// 返回命中(行序位/kind/文本摘录,截 80 字符);空结果不报错。FR-103。
func SearchEvents(store *Store, query string, k int) ([]EventHit, error) {
	events, err := store.ReadAllEvents()
	if err != nil {
		return nil, fmt.Errorf("sessionx: read: %w", err)
	}
	ix := textutil.NewBM25Index()
	texts := make([]string, len(events))
	for i, ev := range events {
		texts[i] = eventTextOf(ev)
		ix.Add(fmt.Sprintf("%d", i+1), texts[i])
	}
	scores := ix.Score(query)
	var hits []EventHit
	for id, score := range scores {
		ln, _ := strconv.ParseInt(id, 10, 64)
		excerpt := texts[ln-1]
		if len(excerpt) > 80 {
			excerpt = excerpt[:80] + "…"
		}
		kind, _ := EventKindOf(events[ln-1])
		hits = append(hits, EventHit{LineNo: ln, Kind: string(kind), Excerpt: excerpt, Score: score})
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].Score > hits[b].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// eventTextOf 提取事件文本面(kind 分支——assistant_text/tool_result/notice)。
func eventTextOf(ev any) string { ... }
```

(注:①EventHit 增 Score 字段(排序依据,导出诚实);②kind 标注经 sessionx.EventKindOf;③eventTextOf 的 kind 分支实现按实际事件类型落位。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/sessionx/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/sessionx/
git commit -m "feat(sessionx): SearchEvents——镜像事件 BM25 检索(013 FR-103;P5a T4)"
```

---

## Task 5: CLI 接线(fork/search 命令)

**Files:**
- Modify: `internal/cli/root.go`(fork/search 子命令)
- Modify: `internal/lint/rules.go`(sessionx 矩阵增 `internal/textutil`)
- Test: `internal/cli/commands_test.go`(Create 或追加既有)

**Interfaces:**
- Consumes: sessionx.Fork/SearchEvents。
- Produces: `mulix fork <src-id> <at-seq> <new-id>`、`mulix search <query> [--session <id>]`(stdout 命中列表;--session 缺省=最新会话——按 cache --session 先例)。

- [ ] **Step 1: 写失败测试**

Create/追加命令测试(按既有 cli 命令测试同款构造——openSession/会话创建助手):

```go
// TestForkCommand 钉 013 FR-101 CLI 面:fork 命令产出新会话(镜像副本+
// parent 标注);atSeq 非法 → 非零退出+错误输出。
func TestForkCommand(t *testing.T) { ... }

// TestSearchCommand 钉 013 FR-103 CLI 面:search 命令输出命中(序位+摘录)。
func TestSearchCommand(t *testing.T) { ... }
```

(最终代码按既有 cli 命令测试构造落位—— 断言不变:fork 产出/错误、search 命中/空。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/cli/ -run 'TestForkCommand|TestSearchCommand' -count=1`
Expected: FAIL(命令未注册——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/cli/root.go` 增两子命令(cobra 同款;fork 调 sessionx.Fork,search 调 sessionx.SearchEvents + stdout 格式化输出)。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): fork/search 子命令——会话能力 CLI 面(013 FR-101/103;P5a T5)"
```

---

## Task 6: 补强批次清偿

**Files:**
- Modify: `internal/engine/decoder_test.go`(fixture gate 标记断言——009 已做,核对)、`internal/kernel/kernel_test.go`(MountAll 重复键测试)、各注释
- Test: 核对/追加

**Interfaces:**
- Consumes: 累积 deferred 清单(003..012)。
- Produces: 台账归零(除明示路由)。

- [ ] **Step 1: 逐项核对/补齐**

累积清单逐项核对(已清偿项确认、未清偿项补齐):[003]fixture gate(009 已做→确认)、[006]t2 标注+原始行(011 已做→确认)、[010]MountAll 重复键(补测试)、[011]Bail 注释+非事务边界文档(补)、[012]回填注释/EnableEventBus 设置位置/hooks 键(补)、[009]死字段(删)、[012]泵尾部重复(012 T3 已删→确认)、[009]200ms 探测窗口偏小(明示路由/或直接修)、[010]「注册期冻结」doc 文本待适配(补)。

- [ ] **Step 2: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿

```bash
git add internal/
git commit -m "test: 补强批次清偿——累积 deferred 核销(013 FR-104;P5a T6)"
```

---

## Task 7: 全矩阵回归收口

**Files:**
- (无新代码——收口核验)

- [ ] **Step 1: 全矩阵**

Run: `go test ./... -count=1 -v && go vet ./... && gofmt -l internal/ cmd/ && go run ./cmd/mulix lint layers`
Expected: 20+ 包全绿/0 违规;补强批次台账归零。

- [ ] **Step 2: 台账**

ledger 记录回归结论;P5a 收官判据(fork/replay/search 全绿+台账归零)写入 task 报告。
