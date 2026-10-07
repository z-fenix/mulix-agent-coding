package sessionx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mulix/internal/jsonl"
)

// Store 是会话的权威镜像（design §10）：~/.mulix/sessions/<id>/events.jsonl
// 追加日志（唯一事实来源）+ 索引 sidecar（meta.json 会话元数据；cache.json 由
// control 写的 Meter 汇总投影）。
// 红线：先持久化后发布——Append 成功返回后事件必须已在磁盘上，调用方才能广播。
// 并发约定：Store 非并发安全（单写者；每会话一个 goroutine 持有）。P4 多入口
// 引入租约/互斥后再放开。
type Store struct {
	dir    string
	writer *jsonl.Writer
}

// Options 是 Create 的会话参数。
type Options struct {
	ID          string // 会话 id（mulix 侧）
	Workspace   string // 工作区路径
	EngineID    string // 引擎 session id（--resume 续链用；可为空，Attach 时回填）
	Model       string // init 侧模型名
	PinnedModel string // 固定模型（未固定则与 Model 相同）
	Unattended  bool   // 无人值守模式
}

// SessionDir 返回会话目录（root/<id>）。
func SessionDir(root, id string) string { return filepath.Join(root, id) }

// Create 新建会话镜像（目录 + 空 events.jsonl + meta.json）。
func Create(root string, opts Options) (*Store, error) {
	if opts.ID == "" {
		return nil, fmt.Errorf("sessionx: session id required")
	}
	dir := SessionDir(root, opts.ID)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("sessionx: session %q already exists", opts.ID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionx: mkdir: %w", err)
	}
	meta := map[string]any{
		"id":                opts.ID,
		"engine_session_id": opts.EngineID,
		"model":             opts.Model,
		"pinned_model":      opts.PinnedModel,
		"workspace":         opts.Workspace,
		"unattended":        opts.Unattended,
		"created_at":        time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSONFile(filepath.Join(dir, "meta.json"), meta); err != nil {
		return nil, err
	}
	w, err := jsonl.OpenWriter(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("sessionx: open events: %w", err)
	}
	return &Store{dir: dir, writer: w}, nil
}

// OpenStore 打开既有会话镜像（追加/读取；恢复链行为属 Task 10 回放）。
// dir 直接传会话目录（root/<id>）。
func OpenStore(dir string) (*Store, error) {
	if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err != nil {
		return nil, fmt.Errorf("sessionx: not a session dir %q (%v)", dir, err)
	}
	w, err := jsonl.OpenWriter(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("sessionx: open events: %w", err)
	}
	return &Store{dir: dir, writer: w}, nil
}

// Append 先持久化一个事件到镜像（kind 标注 + JSON 行）。返回后调用方才可广播。
func (s *Store) Append(ev any) error {
	kind, err := eventKindOf(ev)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("sessionx: marshal %T: %w", ev, err)
	}
	record, err := json.Marshal(map[string]json.RawMessage{
		"kind": json.RawMessage(`"` + kind + `"`),
		"ev":   json.RawMessage(payload),
	})
	if err != nil {
		return fmt.Errorf("sessionx: marshal record: %w", err)
	}
	return s.writer.Append(record)
}

// ReadAllEvents 读取镜像中的全部事件（解 kind 标注还原为类型化事件）。
// 损坏尾部不静默丢弃：jsonl.Reader 已不把半行计入完整行；这里在损坏时返回错误。
func (s *Store) ReadAllEvents() ([]any, error) {
	r, err := jsonl.OpenReader(filepath.Join(s.dir, "events.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("sessionx: open reader: %w", err)
	}
	defer r.Close()
	lines, damaged, err := r.ReadAllWithStatus()
	if err != nil {
		return nil, fmt.Errorf("sessionx: read: %w", err)
	}
	if damaged {
		return nil, fmt.Errorf("sessionx: damaged tail in %s (recovery path handles repair)", s.dir)
	}
	events := make([]any, 0, len(lines))
	for i, line := range lines {
		var record struct {
			Kind string          `json:"kind"`
			Ev   json.RawMessage `json:"ev"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("sessionx: record %d: %w", i, err)
		}
		ev, err := eventFromKind(record.Kind, record.Ev)
		if err != nil {
			return nil, fmt.Errorf("sessionx: record %d: %w", i, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// WriteCache 覆写 cache.json（Meter 汇总投影；由 control 调用）。
func (s *Store) WriteCache(projection map[string]any) error {
	return writeJSONFile(filepath.Join(s.dir, "cache.json"), projection)
}

// ReadCache 读回 cache.json（不存在返回 nil, nil）。
func (s *Store) ReadCache() (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, "cache.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sessionx: read cache: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("sessionx: parse cache: %w", err)
	}
	return m, nil
}

// ReadMeta 读回 meta.json。
func (s *Store) ReadMeta() (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("sessionx: read meta: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("sessionx: parse meta: %w", err)
	}
	return m, nil
}

// SetEngineID 回填引擎 session id（Create 后首次 init 到达时调用）并更新 meta.json。
func (s *Store) SetEngineID(engineID string) error {
	meta, err := s.ReadMeta()
	if err != nil {
		return err
	}
	meta["engine_session_id"] = engineID
	return writeJSONFile(filepath.Join(s.dir, "meta.json"), meta)
}

// Dir 返回会话目录路径。
func (s *Store) Dir() string { return s.dir }

// Close 关闭底层写入句柄（幂等）。
func (s *Store) Close() error {
	if s.writer == nil {
		return nil
	}
	err := s.writer.Close()
	s.writer = nil
	return err
}

func writeJSONFile(path string, v any) error {
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("sessionx: marshal %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("sessionx: write %s: %w", filepath.Base(path), err)
	}
	return nil
}
