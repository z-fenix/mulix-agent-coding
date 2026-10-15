# T5-1 超大行隔离为损坏段 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** JSONL 读取面把超过 16MB 的单行从「整文件硬失败」改为「隔离为损坏段」:好行照读、损坏如实计数、消费方既有损坏处置路径接管(恢复路径物理压实,记忆路径既有隔离重写)。

**Architecture:**(bounded,设计于 chat 并已批准)①`internal/jsonl` 用 `bufio.Reader` 有界逐行读取替换 `bufio.Scanner`(Scanner 遇超限行不可恢复):正常行照旧;超限行记入 `Damage.Oversized`(行序/总字节/64KiB 截断头),跳到行尾继续——不缓冲全行、不吞后续;签名 `(lines, damaged bool, err)` → `(lines, dmg Damage, err)`(单包内改,三消费方机械适配)。②`sessionx.Replay`:半行走既有截断修复;超限段走新增**原地压实**(保留全部完整行;恢复路径会话静止,独立 R/W 句柄与 O_APPEND 写句柄并存——Go/Windows 打开文件默认全共享)。③`memory`:超限段以有界头作 `damagedSegment.raw` 流入既有 `isolate`(隔离区+重写)。

**Tech Stack:** Go 标准库(bufio);既有测试基建(`seedSession`/`damageTail`、memory 隔离测试、jsonl 既有损坏尾测试)。

**Spec:** `docs/specs/005-t5-1/spec.md`

**Design:** approved in chat (bounded) —— 要点:①`ReadSlice` 分块行循环(保持 ScanLines 的剥 `\r` 语义,`MaxLineBytes` 16MB 不变、新增 `headLimit` 64KiB);②`Damage{HalfTail bool, Oversized []OversizedSegment{LineNo, TotalLen, Head}}` + `Any()`;③尾部半行与超限行不重复计数(尾部无换行且超限 → 只计超限);④压实用独立 `O_RDWR` 句柄 WriteAt+Truncate(唯一新增磁盘机制,恢复专用)。

## Global Constraints

- FR-101/102:超限行不整体报错、不吞后续内容。
- FR-103:损坏如实呈现(两类来源可区分),不静默丢弃。
- FR-104:三消费方既有损坏处置语义不变(恢复隔离/记忆隔离/权威加载硬错)。
- FR-105:无超限行文件行为逐字节一致(含 ScanLines 剥 `\r`、空行、尾部半行);上限 16MB 不变。
- FR-106:超限段磁盘足迹有界(隔离转储只写 64KiB 头+元信息)。
- FR-107:写入面、先持久化后发布、1:1 镜像不动。
- 测试纪律:TDD;每任务 `go test ./... -count=1` 全绿再 commit;17MB 级测试文件直接写盘(t.TempDir)。

## Review Focus

- 超限行**后跟正常行**:正常行必须可读(跳行不得吞内容)——Task 1 测试断言。
- 尾部超限行(无换行):只计超限、不重复计半行,且**不误丢**其前的完整行——Task 1 测试断言。
- 空 line/行尾 `\r` 等既有 Scanner 语义逐字节保持:既有 jsonl 测试零改动全绿——Task 1 全量门禁。
- 压实后文件可再次正常加载(恢复一次后不再反复触发)——Task 2 测试断言。
- 权威加载路径对损坏的既有硬错语义不变(消息从「damaged tail」泛化为「damaged segments」)——Task 1 机械适配钉。

---

## Task 1: jsonl 有界逐行读取 + Damage API + 消费方机械适配

**Files:**
- Modify: `internal/jsonl/jsonl.go:204-235`(`ReadAllWithStatus` 重写 + 新类型)
- Modify: `internal/sessionx/replay.go:38-41`、`internal/sessionx/sessionx.go:120-124`、`internal/memory/memory.go:238-242`(机械适配)
- Test: `internal/jsonl/jsonl_test.go`(追加)

**Interfaces:**
- Consumes: 既有 `OpenReader`/`Reader.f`;既有消费者 `damaged bool` 用法(3 处)。
- Produces: ①`type Damage struct { HalfTail bool; Oversized []OversizedSegment }` + `func (d Damage) Any() bool`;②`type OversizedSegment struct { LineNo int; TotalLen int64; Head []byte }`;③`const MaxLineBytes = 16 * 1024 * 1024`(由内联字面量提升)与 `headLimit = 64 * 1024`;④`ReadAllWithStatus() (lines [][]byte, dmg Damage, err error)`——`dmg.Any()` 等价于旧 `damaged`,半行旧语义原样在 `HalfTail`。

- [ ] **Step 1: 写失败测试**

追加到 `internal/jsonl/jsonl_test.go`(import 补 `bytes`——该文件现未 import):

```go
// writeLines 原样写字节行(测试用;不做 JSON 校验)。
func writeLines(t *testing.T, path string, parts ...[]byte) {
	t.Helper()
	var buf bytes.Buffer
	for _, p := range parts {
		buf.Write(p)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestOversizedLineIsolated 钉 FR-101/102/103:中部超限行被隔离为损坏段,
// 前后正常行照读,损坏如实呈现(总长+有界头),无整体错误。
func TestOversizedLineIsolated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize.jsonl")
	big := bytes.Repeat([]byte("x"), MaxLineBytes+1024) // 明确超限
	writeLines(t, path,
		[]byte("{\"a\":1}\n"),
		append(append([]byte(nil), big...), '\n'),
		[]byte("{\"b\":2}\n"))

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("oversized line must not hard-fail (FR-101): %v", err)
	}
	if len(lines) != 2 || string(lines[0]) != `{"a":1}` || string(lines[1]) != `{"b":2}` {
		t.Fatalf("lines = %d (want 2, both normal lines readable FR-102)", len(lines))
	}
	if !dmg.Any() || len(dmg.Oversized) != 1 {
		t.Fatalf("oversized segments = %d, want 1 (FR-103: damage visible)", len(dmg.Oversized))
	}
	seg := dmg.Oversized[0]
	if seg.LineNo != 2 {
		t.Fatalf("segment line no = %d, want 2", seg.LineNo)
	}
	if seg.TotalLen != int64(len(big)) {
		t.Fatalf("segment total = %d, want %d", seg.TotalLen, len(big))
	}
	if len(seg.Head) != headLimit || !bytes.Equal(seg.Head, big[:headLimit]) {
		t.Fatalf("segment head len = %d, want %d (bounded dump FR-106)", len(seg.Head), headLimit)
	}
	if dmg.HalfTail {
		t.Fatal("half-tail must not be set for mid-file oversized line")
	}
}

// TestOversizedTailNoDoubleCount 钉 Edge:尾部超限行(无换行)只计超限一次,
// 不重复计半行,且其前的完整行不误丢。
func TestOversizedTailNoDoubleCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize-tail.jsonl")
	big := bytes.Repeat([]byte("y"), MaxLineBytes+2048)
	writeLines(t, path, []byte("{\"a\":1}\n"), big) // 尾行无换行

	r, _ := OpenReader(path)
	defer r.Close()
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 1 || string(lines[0]) != `{"a":1}` {
		t.Fatalf("lines = %d, want 1 (complete line before oversized tail must survive)", len(lines))
	}
	if len(dmg.Oversized) != 1 || dmg.HalfTail {
		t.Fatalf("oversized=%d halfTail=%v, want 1/false (no double count)", len(dmg.Oversized), dmg.HalfTail)
	}
}

// TestOversizedBoundaryExactCap 钉边界:恰为 MaxLineBytes 的行视为正常
// (>max 才超限;与旧 Scanner 的 ErrTooLong 边界差异是有意裁定,不静默截断)。
func TestOversizedBoundaryExactCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exact-cap.jsonl")
	writeLines(t, path, []byte("{\"a\":1}\n"), append(append([]byte(nil), bytes.Repeat([]byte("b"), MaxLineBytes)...), '\n'))
	r, _ := OpenReader(path)
	defer r.Close()
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 1 || len(lines[0]) != MaxLineBytes {
		t.Fatalf("exact-cap line must be a normal line: lines=%d len=%d", len(lines), len(lines[0]))
	}
	if dmg.Any() {
		t.Fatalf("no damage at exact cap: %+v", dmg)
	}
}

// TestMultipleOversizedLines 钉 SC-004:多条超限行各自隔离、计数累加,
// 后续正常行不受影响。
func TestMultipleOversizedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize-multi.jsonl")
	big1 := bytes.Repeat([]byte("p"), MaxLineBytes+128)
	big2 := bytes.Repeat([]byte("q"), MaxLineBytes+256)
	writeLines(t, path,
		[]byte("{\"a\":1}\n"), append(append([]byte(nil), big1...), '\n'),
		[]byte("{\"b\":2}\n"), append(append([]byte(nil), big2...), '\n'),
		[]byte("{\"c\":3}\n"))

	r, _ := OpenReader(path)
	defer r.Close()
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3 (all normal lines survive)", len(lines))
	}
	if len(dmg.Oversized) != 2 {
		t.Fatalf("oversized = %d, want 2 (count accumulates)", len(dmg.Oversized))
	}
}

// TestLargeNormalLineIntact 钉 FR-105(终审修正回归钉):64KiB~16MB 之间的
// 正常大行必须逐字节完整返回(headLimit 只服务超限段转储,不截断正常行)。
func TestLargeNormalLineIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large-normal.jsonl")
	big := bytes.Repeat([]byte("aB9"), 100*1024) // 300KB 正常行(< 16MB)
	writeLines(t, path, []byte("{\"a\":1}\n"), append(append([]byte(nil), big...), '\n'), []byte("{\"b\":2}\n"))

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	if !bytes.Equal(lines[1], big) {
		t.Fatalf("large normal line corrupted: got %d bytes, want %d byte-for-byte", len(lines[1]), len(big))
	}
	if dmg.Any() {
		t.Fatalf("no damage expected: %+v", dmg)
	}
}

// TestNoOversizedBehaviorUnchanged 钉 FR-105:无超限行文件(含尾部半行、
// 空行、CRLF 剥离)行为与现状一致。
func TestNoOversizedBehaviorUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "normal.jsonl")
	writeLines(t, path, []byte("{\"a\":1}\r\n"), []byte("\n"), []byte("{\"b\":2}\n"), []byte("{\"half\":"))

	r, _ := OpenReader(path)
	defer r.Close()
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// ScanLines 语义保持:\r 被剥离;空行保留;尾半行不计入且标记。
	if len(lines) != 3 || string(lines[0]) != `{"a":1}` || len(lines[1]) != 0 || string(lines[2]) != `{"b":2}` {
		t.Fatalf("lines = %q, want [{a:1} empty {b:2}] (ScanLines semantics preserved)", lines)
	}
	if !dmg.HalfTail {
		t.Fatalf("half-tail = %v, want true (existing tail semantics)", dmg.HalfTail)
	}
	if len(dmg.Oversized) != 0 {
		t.Fatalf("oversized = %d, want 0", len(dmg.Oversized))
	}
}
```

(17MB 级测试文件直接写 `t.TempDir()`,磁盘占用约 52MB、用后即弃;断言含边界、多条、正常大行、既有语义四面。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/jsonl/ -run 'TestOversized|TestNoOversized' -count=1 -v`
Expected: FAIL——`TestOversizedLineIsolated` 报 `oversized line must not hard-fail (FR-101): jsonl: scan: bufio.Scanner: token too long`(现状硬失败);其余新测试同理或编译期因 `MaxLineBytes`/`headLimit` 未定义先报编译错(先加 Task 1 Step 3 的类型与常量声明空壳再验证运行期 RED 亦可,断言不变)。

- [ ] **Step 3: 实现**

`internal/jsonl/jsonl.go`:

(a) 常量与类型(`Reader` 定义前):

```go
// MaxLineBytes 是单行字节上限(防护性设计:单事件协议面不应达到;超限行
// 按损坏段隔离,不整体报错——T5-①)。数值与既有 Scanner 上限一致(16MB)。
const MaxLineBytes = 16 * 1024 * 1024

// headLimit 是超限行截断头的保留字节数(有界转储 FR-106;全行不缓冲)。
const headLimit = 64 * 1024

// OversizedSegment 是一条超限行的有界元信息(T5-①)。
type OversizedSegment struct {
	LineNo   int    // 1 起的行序(按文件物理行计)
	TotalLen int64  // 原始总字节数(不含行尾换行)
	Head     []byte // 前 headLimit 字节截断头(隔离转储用)
}

// Damage 是一次读取发现的损坏段汇总(两类来源可区分,FR-103)。
type Damage struct {
	HalfTail  bool               // 既有:尾部半行(崩溃残留,不计入完整行)
	Oversized []OversizedSegment // T5-①:超限行(隔离,不吞后续)
}

// Any 报告是否存在任何损坏(与旧 damaged bool 语义等价)。
func (d Damage) Any() bool { return d.HalfTail || len(d.Oversized) > 0 }
```

(b) `ReadAllWithStatus` 重写(签名变更;`ReadAll` 的 `damaged` 判断改 `dmg.Any()`):

```go
// ReadAllWithStatus 返回当前全部完整行与损坏段汇总:
// HalfTail=尾部半行(崩溃残留,既有语义);Oversized=超限行(隔离,不吞
// 后续——T5-①)。损坏段不计入完整行、不被静默丢弃——由调用方决定修复或告警。
func (r *Reader) ReadAllWithStatus() (lines [][]byte, dmg Damage, err error) {
	if _, err := r.f.Seek(0, 0); err != nil {
		return nil, Damage{}, fmt.Errorf("jsonl: seek: %w", err)
	}
	br := bufio.NewReaderSize(r.f, 64*1024)
	lineNo := 0
	tailOversized := false // 最后一行无换行且超限(已计超限,不重复计半行)
	for {
		line, total, tooLong, hadNewline, rerr := readBoundedLine(br, MaxLineBytes)
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, Damage{}, fmt.Errorf("jsonl: read: %w", rerr)
		}
		lineNo++
		if tooLong {
			dmg.Oversized = append(dmg.Oversized,
				OversizedSegment{LineNo: lineNo, TotalLen: total, Head: line})
			tailOversized = !hadNewline
			continue
		}
		tailOversized = false
		lines = append(lines, line)
	}
	// 尾部判定(既有):文件非空且最后字节非换行 → 最后一行是半行;
	// 若该尾行已按超限计(无换行超限),不重复计数、不误丢完整行。
	info, err := r.f.Stat()
	if err != nil {
		return nil, Damage{}, fmt.Errorf("jsonl: stat: %w", err)
	}
	if info.Size() > 0 && !tailOversized {
		buf := make([]byte, 1)
		if _, err := r.f.ReadAt(buf, info.Size()-1); err != nil {
			return nil, Damage{}, fmt.Errorf("jsonl: read last byte: %w", err)
		}
		if buf[0] != '\n' && len(lines) > 0 {
			dmg.HalfTail = true
			lines = lines[:len(lines)-1] // 半行不计入完整行
		}
	}
	return lines, dmg, nil
}
```

(c) 行读取助手(包内):

```go
// readBoundedLine 读取一行(不含行尾换行):字节总数 ≤ max 时正常返回;
// 超过 max 时 tooLong=true 且 line 为前 headLimit 字节截断头(全行不缓冲),
// 继续消费到行尾/EOF——后续行不受影响(不吞内容)。ScanLines 语义保持:
// 剥离行尾 \r(仅完整行);空行返回空切片。EOF 且无任何字节 → io.EOF。
func readBoundedLine(br *bufio.Reader, max int) (line []byte, total int64, tooLong bool, hadNewline bool, err error) {
	// 双缓冲(终审修正):buf 承载 ≤ max 的完整行(正常大行——大工具输出
	// 现实存在——必须逐字节返回,不得被 headLimit 截断);head 只服务超限段
	// 的有界转储。一旦累计超过 max(over),buf 丢弃、仅 head 继续。
	var buf []byte
	var head []byte
	over := false
	absorb := func(chunk []byte) {
		total += int64(len(chunk))
		if !over {
			if int64(len(buf))+int64(len(chunk)) > int64(max) {
				over = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if len(head) < headLimit {
			take := headLimit - len(head)
			if take > len(chunk) {
				take = len(chunk)
			}
			head = append(head, chunk[:take]...)
		}
	}
	for {
		chunk, ferr := br.ReadSlice('\n')
		if ferr == bufio.ErrBufferFull { // 缓冲满且未见换行:行继续
			absorb(chunk)
			continue
		}
		if ferr != nil {
			if ferr == io.EOF {
				if total == 0 && len(chunk) == 0 {
					return nil, 0, false, false, io.EOF
				}
				absorb(chunk)
				if !over && len(buf) > 0 && buf[len(buf)-1] == '\r' {
					buf = buf[:len(buf)-1] // ScanLines:EOF 终止 token 同样剥 
				}
				return headOr(buf, head, over), total, over, false, nil
			}
			return nil, 0, false, false, ferr
		}
		body := chunk[:len(chunk)-1] // 去 

		absorb(body)
		if !over && len(buf) > 0 && buf[len(buf)-1] == '\r' {
			buf = buf[:len(buf)-1] // ScanLines:剥离行尾 
		}
		return headOr(buf, head, over), total, over, true, nil
	}
}

// headOr 返回行字节:正常行=完整缓冲;超限行=截断头。
func headOr(buf, head []byte, over bool) []byte {
	if over {
		return head
	}
	return buf
}
```

注:`io` 需加入 import。`ReadAll`(jsonl.go:190-199)的 `if damaged {` 改为 `if dmg.Any() {`,错误消息同步泛化:`"jsonl: damaged tail detected"` → `"jsonl: damaged segments detected"`。

(d) 三消费方机械适配(语义不变,仅换判定与命名):

`internal/sessionx/replay.go:38-41`:

```go
	lines, dmg, err := r.ReadAllWithStatus()
	r.Close()
	if err != nil {
		return nil, warnings, fmt.Errorf("sessionx: read: %w", err)
	}

	if dmg.Any() {
```

(块内 `damaged` 无其他引用;Task 2 将把半行/超限分流——本任务保持既有整块行为。)

`internal/sessionx/sessionx.go:120-124`:

```go
	lines, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		return nil, fmt.Errorf("sessionx: read: %w", err)
	}
	if dmg.Any() {
		return nil, fmt.Errorf("sessionx: damaged segments in %s (recovery path handles repair)", s.dir)
	}
```

`internal/memory/memory.go:238-242`:

```go
	lines, dmg, err := r.ReadAllWithStatus()
	r.Close()
	if err != nil {
		return fmt.Errorf("memory: read %s: %w", s.path, err)
	}
```

块内 `if damagedTail {` 改 `if dmg.HalfTail {`(半行段追加原样);其后、`if len(segments) == 0` 之前加:

```go
	for _, seg := range dmg.Oversized {
		segments = append(segments, damagedSegment{lineNo: seg.LineNo, raw: seg.Head,
			reason: fmt.Sprintf("oversized line (%d bytes), head truncated to %d", seg.TotalLen, len(seg.Head))})
	}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/jsonl/ -count=1 -v`
Expected: PASS(新 6 测试 + 既有 9 测试零改动全绿)

Run: `go test ./... -count=1`
Expected: 全绿(sessionx/memory/consumer 全链)

Run: `go vet ./... && gofmt -l internal/`
Expected: 干净

- [ ] **Step 5: Commit**

```bash
git add internal/jsonl/jsonl.go internal/jsonl/jsonl_test.go internal/sessionx/replay.go internal/sessionx/sessionx.go internal/memory/memory.go
git commit -m "fix(jsonl): 超限行隔离为损坏段——有界逐行读取+Damage API(005 FR-101..103/105/106)"
```

---

## Task 2: sessionx 恢复路径——超限段转储 + 原地压实

**Files:**
- Modify: `internal/sessionx/replay.go:41-79`(`Replay` 损坏分流 + 新压实助手)
- Test: `internal/sessionx/replay_test.go`(追加)

**Interfaces:**
- Consumes: Task 1 的 `Damage{HalfTail, Oversized}` 与 `lines`(已排除全部损坏段);既有 `extractHalfLine`/`s.repairLocked()`/隔离区追加约定。
- Produces: `compactEventsInPlace(path string, keep [][]byte) error`(包内;原地左移压实+Truncate+Sync);`Replay` 语义:半行 → 既有截断修复;超限 → 有界头转储 + 原地压实(恢复后文件干净,不再反复触发)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/sessionx/replay_test.go`(import 补 `bytes` 与 `mulix/internal/jsonl`(MaxLineBytes);`os/filepath/strings/testing` 已有):

```go
// TestReplayOversizedSegmentCompacted 钉 FR-104/106 + SC-001/003:中部超限行
// 被隔离转储(有界头+元信息),主日志原地压实(好行全保留、超限段物理移除),
// 压实后文件可再次正常加载(不反复触发恢复);恢复语义与既有半行路径同构。
func TestReplayOversizedSegmentCompacted(t *testing.T) {
	root := t.TempDir()
	want := []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
		event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "before"},
		event.TurnDoneEvent{Seq: 3, Turn: 1, Subtype: "success"},
	}
	dir := seedSession(t, root, "s-oversize", want)
	// 中部注入超限行(其后另有完整事件——压实不得吞它)。
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("z"), MaxLineBytes+512)); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatalf("inject nl: %v", err)
	}
	f.Close()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Append(event.TurnStartedEvent{Seq: 4, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"}); err != nil {
		t.Fatalf("append after damage: %v", err)
	}
	s.Close()

	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	events, warnings, err := s2.Replay()
	if err != nil {
		t.Fatalf("replay must not hard-fail on oversized segment: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (all good events survive)", len(events))
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "oversized") {
		t.Fatalf("warnings must record oversized isolation, got: %s", joined)
	}
	// 隔离区有界转储:元信息 + 截断头(不写全量 17MB)。
	q, err := os.ReadFile(filepath.Join(dir, "events.jsonl.damaged"))
	if err != nil || !strings.Contains(string(q), `"damaged":"oversized"`) {
		t.Fatalf("quarantine must contain oversized meta (FR-103/106): %v", err)
	}
	if len(q) > MaxLineBytes/16 {
		t.Fatalf("quarantine size = %d, must be bounded (FR-106)", len(q))
	}
	// 压实后再次加载无损坏(不反复触发);事件计数不变。
	events2, err := s2.ReadAllEvents()
	if err != nil {
		t.Fatalf("post-compaction load must be clean: %v", err)
	}
	if len(events2) != len(events) {
		t.Fatalf("post-compaction events = %d, want %d", len(events2), len(events))
	}
}
```

(注:若 `OpenStore` 后 `Append` 因镜像单写者约定不可用,以 `seedSession` 同款 Create/Append/Close 构造即可——保持「超限行注入后、恢复前再无合法追加」亦可,断言不变:恢复后 events==4 依赖注入点之后的合法事件;若构造器约束不允许,把第 4 个事件并入 seedSession 的 want,events 期望改 3,断言语义不变:好行全保留+压实干净。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/sessionx/ -run TestReplayOversizedSegmentCompacted -count=1 -v`
Expected: FAIL——现状(Task 1 机械适配后)超限段走半行路径:`extractHalfLine` 只取尾部半行(此处无半行)→ warnings 无 "oversized" → 断言 `warnings must record oversized isolation` FAIL;或压实缺失导致二次加载报 `damaged segments`。

- [ ] **Step 3: 实现**

`internal/sessionx/replay.go` 的 `Replay` 损坏块(Task 1 的 `if dmg.Any() {`)替换为分流:

```go
	if dmg.Any() {
		// 1) 损坏段转储(累计追加,保留隔离历史):半行原样(既有);
		//    超限段有界头+元信息(全行不落盘,FR-106)。
		if dmg.HalfTail {
			half, err := extractHalfLine(path)
			if err != nil {
				return nil, warnings, fmt.Errorf("sessionx: extract damaged tail: %w", err)
			}
			if err := appendQuarantine(quarantine, half); err != nil {
				return nil, warnings, err
			}
		}
		for _, seg := range dmg.Oversized {
			meta := fmt.Sprintf(`{"damaged":"oversized","line_no":%d,"total_bytes":%d,"head_bytes":%d}`,
				seg.LineNo, seg.TotalLen, len(seg.Head))
			if err := appendQuarantine(quarantine, append(append([]byte(nil), []byte(meta)...), '\n', byte(0))); err != nil {
				return nil, warnings, err
			}
			if err := appendQuarantine(quarantine, seg.Head); err != nil {
				return nil, warnings, err
			}
		}
		// 2) 物理处置:半行 → 截断修复(既有);超限 → 原地压实(保留全部
		//    完整行;恢复路径会话静止,独立 R/W 句柄合法)。
		if dmg.HalfTail {
			if err := s.repairLocked(); err != nil {
				return nil, warnings, fmt.Errorf("sessionx: repair: %w", err)
			}
		}
		if len(dmg.Oversized) > 0 {
			if err := compactEventsInPlace(path, lines); err != nil {
				return nil, warnings, fmt.Errorf("sessionx: compact: %w", err)
			}
		}
		warnings = append(warnings, fmt.Sprintf(
			"sessionx: damaged segments isolated to %s (half_tail=%v oversized=%d); %d complete events kept",
			quarantine, dmg.HalfTail, len(dmg.Oversized), len(lines)))
	}
```

新增两个包内助手(`extractHalfLine` 之后):

```go
// appendQuarantine 向隔离区文件追加一行(累计追加;T5-① 复用既有约定)。
func appendQuarantine(quarantine string, data []byte) error {
	f, err := os.OpenFile(quarantine, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("sessionx: open %s: %w", quarantine, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("sessionx: quarantine: %w", err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		if _, err := f.Write([]byte("\n")); err != nil {
			f.Close()
			return fmt.Errorf("sessionx: quarantine: %w", err)
		}
	}
	return f.Close()
}

// compactEventsInPlace 原地压实镜像日志:仅保留 keepLines(顺序不变),
// 中部超限段被物理移除——压实后文件干净,不再反复触发恢复。恢复路径专用
// (会话静止);Go/Windows 打开文件默认全共享,独立 R/W 句柄与 O_APPEND
// 写句柄可并存;写毕 Truncate 到新长度并 Sync。
func compactEventsInPlace(path string, keep [][]byte) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("sessionx: compact open: %w", err)
	}
	defer f.Close()
	off := int64(0)
	for _, l := range keep {
		buf := make([]byte, 0, len(l)+1)
		buf = append(buf, l...)
		buf = append(buf, '\n')
		n, err := f.WriteAt(buf, off)
		if err != nil {
			return fmt.Errorf("sessionx: compact write: %w", err)
		}
		off += int64(n)
	}
	if err := f.Truncate(off); err != nil {
		return fmt.Errorf("sessionx: compact truncate: %w", err)
	}
	return f.Sync()
}
```

注:①超限转储的元信息行以 NUL 字节结尾、头字节独立一行——隔离区是字节转储历史(无解析契约),NUL 防止头内容与元信息粘连歧义;②`bytes` 已在 replay.go import;③压实在 `repairLocked` 之外独立进行(半行与超限并存时:压实先移除全部损坏,repairLocked 的截断对已压实文件为无害收敛,writer 的 damagedTail 状态被 repairLocked 复位)。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/sessionx/ -count=1 -v`
Expected: PASS(新测试 + 既有 Replay/恢复矩阵零改动全绿)

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净

- [ ] **Step 5: Commit**

```bash
git add internal/sessionx/replay.go internal/sessionx/replay_test.go
git commit -m "fix(sessionx): 恢复路径超限段隔离转储+原地压实(005 FR-104/106;SC-001)"
```

---

## Task 3: memory 超限段隔离验证(既有 isolate 管线)

**Files:**
- Modify: `internal/memory/memory.go:248-253`(超限段入管线——Task 1 已加;本任务补 reason 文案核对与损坏段来源可区分性)
- Test: `internal/memory/memory_test.go`(追加;文件名以既有为准)

**Interfaces:**
- Consumes: Task 1 的 `dmg.Oversized` → `damagedSegment{raw: Head}` 流入既有 `isolate`(隔离区+原子重写)。
- Produces: 记忆库对含超限行文件:好记录照常应用、超限段隔离转储有界、主文件收敛为好行(既有两段式);无新接口。

- [ ] **Step 1: 写失败测试**(构造复用既有 `seedStore`/`damageMidFile`/`Open`/`IsolateReport` 助手,同 `TestOpenIsolatesHalfLineTail` 断言风格):

```go
// TestOpenIsolatesOversizedLine 钉 FR-104/FR-106(SC-003/SC-004):记忆库
// 文件中的超限行被隔离(有界头+reason 元信息),好记录照常应用,主文件
// 收敛为好行,Add 继续可用,隔离区大小有界。构造复用既有
// seedStore/damageMidFile 助手。
func TestOpenIsolatesOversizedLine(t *testing.T) {
	dir := seedStore(t, "good one", "good two")
	damageMidFile(t, dir, strings.Repeat("x", MaxLineBytes+256))

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open on oversized line must not fail: %v", err)
	}
	rep := s.IsolateReport()
	if len(rep) == 0 || !strings.Contains(strings.Join(rep, "\n"), "oversized line") {
		t.Fatalf("report %v should mention oversized line", rep)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 || entries[0].Text != "good one" || entries[1].Text != "good two" {
		t.Fatalf("good entries kept = %+v", entries)
	}
	// Add 继续可用:隔离重写已把主文件收敛,追加不被拒。
	id, err := s.Add("after isolation", "sess-a")
	if err != nil {
		t.Fatalf("add after isolation: %v", err)
	}
	if id != "m3" {
		t.Errorf("id = %q, want m3", id)
	}
	// 隔离历史:reason 文案 + 有界截断头(全量 17MB 不落盘,FR-106)。
	qraw, err := os.ReadFile(filepath.Join(dir, fileName+".damaged"))
	if err != nil {
		t.Fatalf("read damaged: %v", err)
	}
	if len(qraw) > MaxLineBytes/16 {
		t.Errorf("quarantine size = %d, must be bounded (FR-106)", len(qraw))
	}
	if !bytes.Contains(qraw, bytes.Repeat([]byte("x"), 128)) {
		t.Errorf("quarantine missing oversized head bytes")
	}
	// 主文件已收敛:重开无新报告,三条俱全。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if rep2 := s2.IsolateReport(); len(rep2) != 0 {
		t.Errorf("reopen after isolation still reports: %v", rep2)
	}
	entries2, err := s2.List()
	if err != nil {
		t.Fatalf("list after reopen: %v", err)
	}
	if len(entries2) != 3 {
		t.Errorf("entries after reopen = %d, want 3", len(entries2))
	}
}
```

(注意:超限段的 reason 进 `IsolateReport`,隔离区文件只落有界头字节——两者分开断言;import 补 `bytes` 与 `mulix/internal/jsonl`(MaxLineBytes)。)


- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/memory/ -run TestOpenIsolatesOversizedLine -count=1 -v`
Expected: FAIL(Task 1 已把超限段接入 isolate——若该步已使测试绿,则本任务降级为「钉住既有行为的固化测试」,RED 记录改为「已绿,作为回归固化」并在台账记 Ruling;不允许删断言)

- [ ] **Step 3: 核对/补实现**

Task 1 的 reason 文案与 raw=Head 即本任务的实现面;若 Step 2 已绿,核对三点并补缺:①reason 含 "oversized";②隔离区只含头+reason(非全量);③isolate 重写后主文件无超限字节。

- [ ] **Step 4: 跑测试验证通过**

Run: `go test ./internal/memory/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "test(memory): 超限行隔离固化测试——既有 isolate 管线承接(005 FR-104/106)"
```
