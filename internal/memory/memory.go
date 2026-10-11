// Package memory 提供 P2 记忆服务第 1 半部（M13/T13.1）：项目级 JSONL 存储
// （追加式 + tombstone 删除 + 损坏段隔离）与进程内 BM25 检索。
//
// 存储布局：dir/entries.jsonl，每行一个记录：
//
//	{"kind":"add","id":"m1","text":"…","source_session":"…","created_at":"…"}
//	{"kind":"del","id":"m1"}          ← tombstone（追加，重放时收敛）
//
// 损坏段隔离沿用 P1 sessionx 先例（internal/sessionx/replay.go）：半行尾部与
// 中间垃圾行**原样转储进 entries.jsonl.damaged（累计追加）**，主文件收敛为
// 仅完整好行（临时文件 + rename 原子替换，中途失败原文件无损）；IsolateReport
// 逐段报告。好条目全保留，Add/Delete 随后照常可用——损坏不得阻塞打开。
//
// 写入策略：open-per-write——每次 Add/Delete 经 mulix/internal/jsonl 打开追加
// 句柄、写完即关（P1 jsonl.Writer 打开即探测损坏尾部并拒绝在其上追加，「不在
// 半行上熔接记录」红线不变）。不保留长命句柄：Windows 上 TempDir 清理无碍，
// Store 因此没有 Close。id 取 "m<N>"，N = 重放时见过的最大数字 id + 1——
// 随重开存活，且损坏段收敛压缩后不产生行号歧义、不与存量 id 碰撞。
package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"mulix/internal/jsonl"
)

// fileName 记忆库主文件名（隔离文件为其 + ".damaged"，沿 sessionx 命名式）。
const fileName = "entries.jsonl"

// Entry 是一条活记忆（List/Search 的投影，brief Produces 口径）。
type Entry struct {
	ID            string
	Text          string
	SourceSession string
	CreatedAt     string
}

// record 是 entries.jsonl 的行结构。kind 区分追加（add）与 tombstone（del）；
// 设计 §4 的 {id, text, source_session, created_at} 字段原样保留。
type record struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Text          string `json:"text,omitempty"`
	SourceSession string `json:"source_session,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// Store 是项目级记忆库。并发安全（互斥锁覆盖全部公开方法）。
type Store struct {
	mu      sync.Mutex
	path    string         // entries.jsonl 全路径（隔离/临时文件由其派生）
	entries []Entry        // 追加序槽位（含被删槽位；List/Search 按 liveIdx 过滤）
	liveIdx map[string]int // 活跃 id → entries 下标（tombstone 收敛即摘除）
	ix      *index         // BM25 索引（Open 全量构建，Add/Delete 增量）
	report  []string       // 损坏段隔离报告（IsolateReport 投影）
	nextID  int            // 见过的最大数字 id；新 id = "m" + (nextID+1)
}

// Open 打开（或创建）dir 下的记忆库并全量重放。
// 损坏段（半行尾部 / 中间垃圾行 / 字段缺失或未知 kind 的行）不阻塞打开：
// 逐段隔离（见包注释），返回的 Store 报告经 IsolateReport 可查。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("memory: mkdir %s: %w", dir, err)
	}
	s := &Store{
		path:    filepath.Join(dir, fileName),
		liveIdx: map[string]int{},
		ix:      newIndex(),
	}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}

// Add 追加一条记忆并返回 id（"m<N>"，单调递增；tombstone 不释放 id）。
// 空白文本拒绝（无检索价值的空条目会让 avgdl 失真）。
func (s *Store) Add(text, sourceSession string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("memory: text is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := record{
		Kind:          "add",
		ID:            "m" + strconv.Itoa(s.nextID+1),
		Text:          text,
		SourceSession: sourceSession,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.appendRecord(rec); err != nil {
		return "", err // 落盘失败：id 未消费（applyRecord 未执行），下标不前进
	}
	s.applyRecord(&rec)
	return rec.ID, nil
}

// Delete 追加 tombstone 并在内存面摘除该条目。未知 id 报错（不静默吞）。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.liveIdx[id]; !ok {
		return fmt.Errorf("memory: unknown id %q", id)
	}
	rec := record{Kind: "del", ID: id}
	if err := s.appendRecord(rec); err != nil {
		return err
	}
	s.applyRecord(&rec)
	return nil
}

// List 返回全部活跃条目（追加序，即创建序）。活跃态由重放收敛而来。
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.liveIdx))
	for i, e := range s.entries {
		// 缺失键的零值是 0，不能直接 ==i：必须带 ok 判存活（index 0 的
		// tombstone 条目否则会穿透过滤）。
		if idx, ok := s.liveIdx[e.ID]; !ok || idx != i {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Search BM25 检索 top-k：相关性降序（平分按插入序稳定裁决），预算按
// Text 的 rune 数累进——下一条放不下即停（预算内按分截断，不做逐条截半）。
// k<=0 或 budgetChars<=0 返回空。
func (s *Store) Search(query string, k int, budgetChars int) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k <= 0 || budgetChars <= 0 {
		return []Entry{}
	}
	scores := s.ix.score(query)
	if len(scores) == 0 {
		return []Entry{}
	}
	type hit struct {
		entry Entry
		score float64
	}
	var hits []hit
	for i, e := range s.entries {
		// 同 List：缺失键零值 0 == 0 会放行 index 0 的已删条目，须带 ok。
		if idx, ok := s.liveIdx[e.ID]; !ok || idx != i {
			continue
		}
		if sc := scores[e.ID]; sc > 0 {
			hits = append(hits, hit{entry: e, score: sc})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]Entry, 0, min(k, len(hits)))
	used := 0
	for _, h := range hits {
		if len(out) == k {
			break
		}
		need := utf8.RuneCountInString(h.entry.Text)
		if used+need > budgetChars {
			break
		}
		out = append(out, h.entry)
		used += need
	}
	return out
}

// IsolateReport 返回本次 Open 隔离的损坏段报告（逐段一行；无损坏为空）。
// 测试/告警用——损坏不静默，好数据不丢。
func (s *Store) IsolateReport() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.report))
	copy(out, s.report)
	return out
}

// applyRecord 把一条记录收敛进内存面（重放与运行时共用；调用方持锁）。
// add：id 已活跃 → 原位替换（异常重复行的后到为准）；否则新槽位。
// del：摘除活跃标记（槽位保留，过滤路径跳过）；未知 id 为 no-op（重放容错）。
func (s *Store) applyRecord(rec *record) {
	if n, ok := numericSuffix(rec.ID); ok && n > s.nextID {
		s.nextID = n
	}
	switch rec.Kind {
	case "add":
		e := Entry{ID: rec.ID, Text: rec.Text, SourceSession: rec.SourceSession, CreatedAt: rec.CreatedAt}
		if i, ok := s.liveIdx[rec.ID]; ok {
			s.entries[i] = e
		} else {
			s.liveIdx[rec.ID] = len(s.entries)
			s.entries = append(s.entries, e)
		}
		s.ix.add(rec.ID, rec.Text) // 替换语义：重复 add 不叠加词频
	case "del":
		if _, ok := s.liveIdx[rec.ID]; ok {
			delete(s.liveIdx, rec.ID)
			s.ix.remove(rec.ID)
		}
	}
}

// damagedSegment 是一段待隔离的损坏内容（原样字节 + 定位信息）。
type damagedSegment struct {
	lineNo int    // 原文件行号（报告定位用；尾部半行记其后一行）
	raw    []byte // 损坏字节（原样，进隔离文件）
	reason string // 损坏原因（进报告）
}

// replay 全量读回主文件：逐行解析收敛，损坏段收集后统一隔离。
func (s *Store) replay() error {
	r, err := jsonl.OpenReader(s.path)
	if err != nil {
		// jsonl.OpenReader 包装了 os.Open 的错误：必须用 errors.Is 解包判定
		// 首次运行（无文件 = 空库）；os.IsNotExist 不解包 wrapError，会漏判。
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("memory: open %s: %w", s.path, err)
	}
	lines, dmg, err := r.ReadAllWithStatus()
	r.Close()
	if err != nil {
		return fmt.Errorf("memory: read %s: %w", s.path, err)
	}

	good := make([][]byte, 0, len(lines))
	var segments []damagedSegment
	for i, line := range lines {
		rec, perr := parseRecord(line)
		if perr != nil {
			segments = append(segments, damagedSegment{lineNo: i + 1, raw: line, reason: perr.Error()})
			continue
		}
		s.applyRecord(rec)
		good = append(good, line)
	}
	if dmg.HalfTail {
		half, err := extractHalfLine(s.path)
		if err != nil {
			return fmt.Errorf("memory: extract damaged tail: %w", err)
		}
		segments = append(segments, damagedSegment{
			lineNo: len(lines) + 1, raw: half, reason: "damaged tail (half line)",
		})
	}
	for _, seg := range dmg.Oversized {
		segments = append(segments, damagedSegment{lineNo: seg.LineNo, raw: seg.Head,
			reason: fmt.Sprintf("oversized line (%d bytes), head truncated to %d", seg.TotalLen, len(seg.Head))})
	}
	if len(segments) == 0 {
		return nil
	}
	return s.isolate(good, segments)
}

// isolate 损坏段隔离（P1 sessionx 同款语义的两段式）：
//  1. 原样转储进 entries.jsonl.damaged（累计追加，保留隔离历史）；
//  2. 主文件收敛为仅完整好行（临时文件 + rename 原子替换——中途失败原文件无损）。
func (s *Store) isolate(good [][]byte, segments []damagedSegment) error {
	quarantine := s.path + ".damaged"
	q, err := os.OpenFile(quarantine, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("memory: open quarantine %s: %w", quarantine, err)
	}
	for _, seg := range segments {
		if _, err := q.Write(append(seg.raw, '\n')); err != nil {
			q.Close()
			return fmt.Errorf("memory: quarantine write: %w", err)
		}
		s.report = append(s.report, fmt.Sprintf(
			"memory: line %d %s, isolated to %s", seg.lineNo, seg.reason, filepath.Base(quarantine)))
	}
	if err := q.Close(); err != nil {
		return fmt.Errorf("memory: quarantine close: %w", err)
	}

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("memory: rewrite open %s: %w", tmp, err)
	}
	for _, line := range good {
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return fmt.Errorf("memory: rewrite write: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("memory: rewrite close: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("memory: rewrite rename: %w", err)
	}
	return nil
}

// appendRecord 追加一行记录（open-per-write：句柄即开即关，不留长命句柄）。
// jsonl.OpenWriter 打开即探测损坏尾部并拒绝在其上追加——文件被带外写坏时
// fail-closed，重开 Open 走隔离路径恢复。
func (s *Store) appendRecord(rec record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("memory: marshal: %w", err)
	}
	w, err := jsonl.OpenWriter(s.path)
	if err != nil {
		return fmt.Errorf("memory: append: %w", err)
	}
	if err := w.Append(line); err != nil {
		w.Close()
		return fmt.Errorf("memory: append: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("memory: append close: %w", err)
	}
	return nil
}

// parseRecord 解析并校验一行记录；不合规（坏 JSON / 缺关键字段 / 未知 kind）
// 返回错误——由 replay 计入损坏段，不静默丢弃。
func parseRecord(line []byte) (*record, error) {
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil, fmt.Errorf("invalid json (%v)", err)
	}
	switch rec.Kind {
	case "add":
		if rec.ID == "" {
			return nil, fmt.Errorf("add record missing id")
		}
		if rec.Text == "" {
			return nil, fmt.Errorf("add record missing text")
		}
	case "del":
		if rec.ID == "" {
			return nil, fmt.Errorf("del record missing id")
		}
	default:
		return nil, fmt.Errorf("unknown kind %q", rec.Kind)
	}
	return &rec, nil
}

// numericSuffix 提取 "m<N>" 的 N；非此式的 id 返回 ok=false（不推进 nextID）。
func numericSuffix(id string) (int, bool) {
	if !strings.HasPrefix(id, "m") {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// extractHalfLine 返回文件末尾最后一个换行之后的半行字节（损坏尾部原样；
// P1 sessionx 同款实现）。
func extractHalfLine(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	idx := bytes.LastIndexByte(raw, '\n')
	if idx < 0 {
		return raw, nil // 整个文件都是半行
	}
	return raw[idx+1:], nil
}
