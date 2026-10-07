package jsonl

import (
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
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("complete lines = %d, want 2", len(got))
	}
	if !damaged {
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
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || damaged {
		t.Fatalf("got %d lines damaged=%v, want 2 false", len(got), damaged)
	}
}

func TestEmptyFile(t *testing.T) {
	path := mustTemp(t)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r, _ := OpenReader(path)
	defer r.Close()
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 || damaged {
		t.Fatalf("got %d lines damaged=%v, want 0 false", len(got), damaged)
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
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || damaged {
		t.Fatalf("after repair: %d lines damaged=%v, want 2 false (b was the damaged tail)", len(got), damaged)
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
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || damaged {
		t.Fatalf("got %d lines damaged=%v, want 1 false", len(got), damaged)
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
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || damaged {
		t.Fatalf("got %d lines damaged=%v, want 1 false", len(got), damaged)
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
	got, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || damaged {
		t.Fatalf("got %d lines damaged=%v, want 1 false", len(got), damaged)
	}
}
