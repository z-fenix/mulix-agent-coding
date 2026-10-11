package jsonl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mustTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "log.jsonl")
}

func TestAppendAndReadBack(t *testing.T) {
	path := mustTemp(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	type rec struct {
		Kind string `json:"kind"`
		N    int    `json:"n"`
	}
	for i, want := range []rec{{"a", 1}, {"b", 2}, {"c", 3}} {
		b, _ := json.Marshal(want)
		if err := w.Append(b); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer r.Close()
	got, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3", len(got))
	}
	var first rec
	if err := json.Unmarshal(got[0], &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if first.Kind != "a" || first.N != 1 {
		t.Fatalf("first record wrong: %+v", first)
	}
}

// TestWriterSyncExplicit pins 显式 Sync 入口（per-kind fsync 供调用方使用）：
// Append（无 fsync 选项）后显式 Sync——落盘确认 + 内容完好；连续 Sync 幂等。
func TestWriterSyncExplicit(t *testing.T) {
	path := mustTemp(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if err := w.Append([]byte(`{"kind":"approval"}`)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("idempotent sync: %v", err)
	}
	// 落盘后内容完好（只读句柄验证，不经 Close）
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "{\"kind\":\"approval\"}\n" {
		t.Fatalf("content = %q", b)
	}
}

func TestAppendFsyncOptional(t *testing.T) {
	path := mustTemp(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := w.Append([]byte(`{"kind":"x"}`)); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Default writer: fsync off; with Fsync enabled: flushes to disk per append.
	w2, err := OpenWriterWithOptions(path, Options{Fsync: true})
	if err != nil {
		t.Fatalf("open with options: %v", err)
	}
	if err := w2.Append([]byte(`{"kind":"y"}`)); err != nil {
		t.Fatalf("append fsync: %v", err)
	}
	w.Close()
	w2.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("file empty")
	}
}

func TestDamagedTailDetection(t *testing.T) {
	path := mustTemp(t)
	// Two complete lines + one damaged (half) line, no trailing newline.
	content := "{\"kind\":\"a\",\"n\":1}\n{\"kind\":\"b\",\"n\":2}\n{\"kind\":\"c\""
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("complete lines = %d, want 2", len(got))
	}
	if !dmg.HalfTail {
		t.Fatal("damaged tail not detected")
	}
	var second struct {
		Kind string `json:"kind"`
		N    int    `json:"n"`
	}
	if err := json.Unmarshal(got[1], &second); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if second.Kind != "b" || second.N != 2 {
		t.Fatalf("second record wrong: %+v", second)
	}
}

func TestCleanTailNotDamaged(t *testing.T) {
	path := mustTemp(t)
	content := "{\"kind\":\"a\"}\n{\"kind\":\"b\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || dmg.Any() {
		t.Fatalf("got %d lines damaged=%v, want 2 false", len(got), dmg.Any())
	}
}

func TestEmptyFile(t *testing.T) {
	path := mustTemp(t)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 || dmg.Any() {
		t.Fatalf("got %d lines damaged=%v, want 0 false", len(got), dmg.Any())
	}
}

func TestAppendAfterCrashRecoversFile(t *testing.T) {
	path := mustTemp(t)
	// Simulate a crash-damaged tail, then append a new complete line: the
	// writer must refuse to append onto a damaged tail without repair.
	content := "{\"kind\":\"a\"}\n{\"kind\":\"b\""
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if err := w.Append([]byte(`{"kind":"c"}`)); err == nil {
		t.Fatal("append onto damaged tail must fail without repair")
	}
	// Repair (truncate the damaged tail), then append succeeds.
	if err := w.Repair(); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if err := w.Append([]byte(`{"kind":"c"}`)); err != nil {
		t.Fatalf("append after repair: %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || dmg.Any() {
		t.Fatalf("after repair: %d lines damaged=%v, want 2 false (b was the damaged tail)", len(got), dmg.Any())
	}
}

func TestReadIncremental(t *testing.T) {
	path := mustTemp(t)
	w, _ := OpenWriter(path)
	w.Append([]byte(`{"n":1}`))
	w.Close()
	r, _ := OpenReader(path)
	defer r.Close()
	got, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// Append more, then read again: incremental reads see new lines.
	w, _ = OpenWriter(path)
	w.Append([]byte(`{"n":2}`))
	w.Close()
	got, err = r.ReadAll()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
}

// TestReadAllErrorsOnDamagedTail pins the red line: ReadAll (the strict
// variant) must error on a damaged tail.
func TestReadAllErrorsOnDamagedTail(t *testing.T) {
	path := mustTemp(t)
	content := "{\"kind\":\"a\"}\n{\"kind\":\"b\""
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	got, err := r.ReadAll()
	if err == nil {
		t.Fatal("ReadAll must error on damaged tail")
	}
	if len(got) != 0 {
		t.Fatalf("ReadAll on damaged tail must not return lines, got %d", len(got))
	}
}

// TestRepairNoNewlineAtAll pins: a file of one half line with no newline
// truncates to empty (zero complete lines survive), then appends work.
func TestRepairNoNewlineAtAll(t *testing.T) {
	path := mustTemp(t)
	if err := os.WriteFile(path, []byte(`{"kind":"half`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if err := w.Append([]byte(`{"kind":"a"}`)); err == nil {
		t.Fatal("append must refuse on damaged tail")
	}
	if err := w.Repair(); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if err := w.Append([]byte(`{"kind":"a"}`)); err != nil {
		t.Fatalf("append after repair: %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || dmg.Any() {
		t.Fatalf("got %d lines damaged=%v, want 1 false", len(got), dmg.Any())
	}
}

// TestRepairDamagedTailLongerThanChunk pins the multi-chunk backward scan:
// a half line longer than the 4096-byte scan chunk is still repaired to the
// last complete line.
func TestRepairDamagedTailLongerThanChunk(t *testing.T) {
	path := mustTemp(t)
	half := `{"kind":"half","pad":"` + string(make([]byte, 10_000)) + `"`
	content := "{\"kind\":\"a\"}\n" + half
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if err := w.Repair(); err != nil {
		t.Fatalf("repair: %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || dmg.Any() {
		t.Fatalf("got %d lines damaged=%v, want 1 false", len(got), dmg.Any())
	}
	var a struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(got[0], &a); err != nil || a.Kind != "a" {
		t.Fatalf("surviving record wrong: %s (%v)", got[0], err)
	}
}

// TestRepairIdempotent pins: Repair twice is a no-op the second time.
func TestRepairIdempotent(t *testing.T) {
	path := mustTemp(t)
	content := "{\"kind\":\"a\"}\n{\"kind\":\"b\""
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if err := w.Repair(); err != nil {
		t.Fatalf("repair 1: %v", err)
	}
	if err := w.Repair(); err != nil {
		t.Fatalf("repair 2 (idempotent): %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, dmg, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || dmg.Any() {
		t.Fatalf("got %d lines damaged=%v, want 1 false", len(got), dmg.Any())
	}
}

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
	if len(lines) != 2 || len(lines[1]) != MaxLineBytes || !bytes.Equal(lines[1], bytes.Repeat([]byte("b"), MaxLineBytes)) {
		t.Fatalf("exact-cap line must be a normal line: lines=%d len(lines[1])=%d", len(lines), len(lines[1]))
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
