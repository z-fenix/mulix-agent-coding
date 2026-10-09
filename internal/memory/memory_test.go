package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	textBigram  = "记住本项目用 pnpm"
	textEnglish = "english note about testing"
)

// seedStore 在临时目录建库并追加给定文本（模拟此前进程的落盘状态）。
func seedStore(t *testing.T, texts ...string) string {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, text := range texts {
		if _, err := s.Add(text, "sess-a"); err != nil {
			t.Fatalf("add %q: %v", text, err)
		}
	}
	return dir
}

// damageTail 向 entries.jsonl 追加半行 JSON（无换行），模拟写中途崩溃。
func damageTail(t *testing.T, dir, half string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, fileName), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for damage: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(half); err != nil {
		t.Fatalf("damage: %v", err)
	}
}

// damageMidFile 在首行之后插入一行垃圾（完整行，带换行），模拟文件中段损坏。
func damageMidFile(t *testing.T, dir, garbage string) {
	t.Helper()
	path := filepath.Join(dir, fileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read for damage: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("seed file has %d lines, want >= 2", len(lines))
	}
	corrupted := lines[0] + "\n" + garbage + "\n" + strings.Join(lines[1:], "\n") + "\n"
	if err := os.WriteFile(path, []byte(corrupted), 0o644); err != nil {
		t.Fatalf("write damage: %v", err)
	}
}

func TestAddListRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id1, err := s.Add(textBigram, "sess-a")
	if err != nil {
		t.Fatalf("add 1: %v", err)
	}
	id2, err := s.Add(textEnglish, "sess-b")
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if id1 != "m1" || id2 != "m2" {
		t.Fatalf("ids = %q, %q; want m1, m2 (monotonic per-store)", id1, id2)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("list len = %d, want 2", len(entries))
	}
	if entries[0].ID != "m1" || entries[0].Text != textBigram || entries[0].SourceSession != "sess-a" {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1].ID != "m2" || entries[1].Text != textEnglish || entries[1].SourceSession != "sess-b" {
		t.Errorf("entry[1] = %+v", entries[1])
	}
	if _, err := time.Parse(time.RFC3339, entries[0].CreatedAt); err != nil {
		t.Errorf("CreatedAt %q not RFC3339: %v", entries[0].CreatedAt, err)
	}
	if rep := s.IsolateReport(); len(rep) != 0 {
		t.Errorf("IsolateReport on clean store = %v, want empty", rep)
	}

	// 重开：状态由文件重放恢复，id 序列延续。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	id3, err := s2.Add("third entry", "sess-a")
	if err != nil {
		t.Fatalf("add after reopen: %v", err)
	}
	if id3 != "m3" {
		t.Errorf("id after reopen = %q, want m3 (id sequence survives reopen)", id3)
	}
}

func TestDeleteTombstoneAndReplay(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id1, err := s.Add("first", "sess-a")
	if err != nil {
		t.Fatalf("add 1: %v", err)
	}
	id2, err := s.Add("second", "sess-a")
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if err := s.Delete(id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(id1); err == nil {
		t.Errorf("re-delete of tombstoned id: want error, got nil")
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id2 {
		t.Fatalf("after delete list = %+v, want only %s", entries, id2)
	}

	// 重放：tombstone 收敛，被删条目不复活。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	entries2, err := s2.List()
	if err != nil {
		t.Fatalf("list after replay: %v", err)
	}
	if len(entries2) != 1 || entries2[0].ID != id2 {
		t.Fatalf("after replay list = %+v, want only %s", entries2, id2)
	}
	if err := s2.Delete(id1); err == nil {
		t.Errorf("delete of tombstoned id after replay: want error, got nil")
	}
	// tombstone 不释放 id：新追加延续单调序。
	id3, err := s2.Add("third", "sess-a")
	if err != nil {
		t.Fatalf("add after replay: %v", err)
	}
	if id3 != "m3" {
		t.Errorf("id after replay = %q, want m3 (deleted ids are not reused)", id3)
	}
}

// Review-Focus：损坏尾部（半行截断）不得使 Open 失败——隔离 + 报告 + 好条目保留 + Add 可用。
func TestOpenIsolatesHalfLineTail(t *testing.T) {
	dir := seedStore(t, "good one", "good two")
	damageTail(t, dir, `{"kind":"add","id":"m3","text":"half`)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open on damaged tail: %v", err)
	}
	rep := s.IsolateReport()
	if len(rep) == 0 {
		t.Fatalf("IsolateReport empty, want damaged-tail entry")
	}
	if !strings.Contains(strings.Join(rep, "\n"), "tail") {
		t.Errorf("report %v should mention damaged tail", rep)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 || entries[0].Text != "good one" || entries[1].Text != "good two" {
		t.Fatalf("good entries kept = %+v", entries)
	}
	// Add 继续可用：隔离已把主文件收敛为干净尾，追加不被拒。
	id, err := s.Add("after repair", "sess-a")
	if err != nil {
		t.Fatalf("add after isolation: %v", err)
	}
	if id != "m3" {
		t.Errorf("id after isolation = %q, want m3", id)
	}
	// 隔离历史在 .damaged（半行原样）。
	qraw, err := os.ReadFile(filepath.Join(dir, fileName+".damaged"))
	if err != nil {
		t.Fatalf("read damaged: %v", err)
	}
	if !strings.Contains(string(qraw), `"text":"half`) {
		t.Errorf("quarantine missing half-line bytes: %q", qraw)
	}
	// 主文件已收敛：重开无新报告，三条俱全。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after isolation: %v", err)
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

// Review-Focus：文件中段垃圾行不得使 Open 失败——隔离 + 报告 + 好条目保留 + Add 可用。
func TestOpenIsolatesGarbageMidFileLine(t *testing.T) {
	dir := seedStore(t, "good one", "good two")
	damageMidFile(t, dir, `GARBAGE{{{`)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open on mid-file garbage: %v", err)
	}
	rep := s.IsolateReport()
	if len(rep) == 0 {
		t.Fatalf("IsolateReport empty, want garbage-line entry")
	}
	if !strings.Contains(strings.Join(rep, "\n"), "line 2") {
		t.Errorf("report %v should mention line 2", rep)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "m1" || entries[1].ID != "m2" {
		t.Fatalf("good entries kept (in order) = %+v", entries)
	}
	id, err := s.Add("after repair", "sess-a")
	if err != nil {
		t.Fatalf("add after isolation: %v", err)
	}
	if id != "m3" {
		t.Errorf("id after isolation = %q, want m3", id)
	}
	qraw, err := os.ReadFile(filepath.Join(dir, fileName+".damaged"))
	if err != nil {
		t.Fatalf("read damaged: %v", err)
	}
	if !strings.Contains(string(qraw), "GARBAGE{{{") {
		t.Errorf("quarantine missing garbage bytes: %q", qraw)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after isolation: %v", err)
	}
	if rep2 := s2.IsolateReport(); len(rep2) != 0 {
		t.Errorf("reopen after isolation still reports: %v", rep2)
	}
}

// Review-Focus：半行截断 + 中间垃圾行同时存在——两段都隔离，好条目全保留。
func TestOpenIsolatesHalfLineAndGarbageMidFile(t *testing.T) {
	dir := seedStore(t, "good one", "good two", "good three")
	damageMidFile(t, dir, `GARBAGE{{{`)
	damageTail(t, dir, `{"kind":"a`)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open on combined damage: %v", err)
	}
	rep := s.IsolateReport()
	if len(rep) != 2 {
		t.Fatalf("IsolateReport = %v, want 2 segments (garbage line + tail)", rep)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("good entries kept = %d, want 3", len(entries))
	}
	if _, err := s.Add("after repair", "sess-a"); err != nil {
		t.Fatalf("add after isolation: %v", err)
	}
}

func TestSearchChineseBigramHit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Add(textBigram, "sess-a"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.Add("完全无关的一条记忆", "sess-a"); err != nil {
		t.Fatalf("add: %v", err)
	}
	hits := s.Search("项目", 5, 2000)
	if len(hits) != 1 || hits[0].ID != "m1" || hits[0].Text != textBigram {
		t.Fatalf("search 项目 = %+v, want only m1 (%s)", hits, textBigram)
	}
}

func TestSearchLatinWordHit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Add(textBigram, "sess-a"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// 与 textBigram 同长（6 词）：同 tf 同 dl → BM25 精确平分 → 稳定序钉插入序。
	if _, err := s.Add("keep pnpm pinned at the root", "sess-a"); err != nil {
		t.Fatalf("add: %v", err)
	}
	hits := s.Search("pnpm", 5, 2000)
	if len(hits) != 2 {
		t.Fatalf("search pnpm len = %d, want 2 (case-insensitive word match)", len(hits))
	}
	if hits[0].ID != "m1" || hits[1].ID != "m2" {
		t.Errorf("search pnpm order = %s,%s; want m1,m2 (exact score tie → insertion order)", hits[0].ID, hits[1].ID)
	}
}

func TestSearchTopKOrdering(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Add("alpha alpha pie", "sess-a"); err != nil { // tf=2
		t.Fatalf("add: %v", err)
	}
	if _, err := s.Add("alpha tart cup", "sess-a"); err != nil { // tf=1，同长
		t.Fatalf("add: %v", err)
	}
	if _, err := s.Add("banana bread", "sess-a"); err != nil { // 不含查询词
		t.Fatalf("add: %v", err)
	}
	hits := s.Search("alpha", 1, 2000)
	if len(hits) != 1 || hits[0].ID != "m1" {
		t.Fatalf("k=1 = %+v, want only m1 (higher tf first)", hits)
	}
	hits = s.Search("alpha", 5, 2000)
	if len(hits) != 2 || hits[0].ID != "m1" || hits[1].ID != "m2" {
		t.Fatalf("k=5 = %+v, want m1 then m2 (non-matching excluded)", hits)
	}
}

func TestSearchBudgetTruncation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Add("alpha alpha alpha", "sess-a"); err != nil { // 17 chars
		t.Fatalf("add: %v", err)
	}
	if _, err := s.Add("alpha beta", "sess-a"); err != nil { // 10 chars
		t.Fatalf("add: %v", err)
	}
	// 预算充足：两条都回，按分排序。
	hits := s.Search("alpha", 5, 100)
	if len(hits) != 2 || hits[0].ID != "m1" || hits[1].ID != "m2" {
		t.Fatalf("budget 100 = %+v, want m1 then m2", hits)
	}
	// 预算=17：第一条恰好放下，第二条会超 → 停（放不下即截断）。
	hits = s.Search("alpha", 5, 17)
	if len(hits) != 1 || hits[0].ID != "m1" {
		t.Fatalf("budget 17 = %+v, want only m1", hits)
	}
	// 预算小于最优先条目：一条都放不下 → 空。
	hits = s.Search("alpha", 5, 5)
	if len(hits) != 0 {
		t.Fatalf("budget 5 = %+v, want empty", hits)
	}
	// k 上限独立于预算生效。
	hits = s.Search("alpha", 1, 100)
	if len(hits) != 1 || hits[0].ID != "m1" {
		t.Fatalf("k=1 = %+v, want only m1", hits)
	}
}

// 预算按字符（rune）计，不是字节：6 rune 的中文条目在 budget=10 下放得下。
func TestSearchBudgetCountsRunesNotBytes(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Add("五字中文条目", "sess-a"); err != nil { // 6 rune / 18 byte
		t.Fatalf("add: %v", err)
	}
	if hits := s.Search("中文", 5, 10); len(hits) != 1 {
		t.Fatalf("budget 10 chars = %+v, want the 6-rune entry (rune counting)", hits)
	}
	if hits := s.Search("中文", 5, 5); len(hits) != 0 {
		t.Fatalf("budget 5 chars = %+v, want empty (6 runes > 5)", hits)
	}
}
