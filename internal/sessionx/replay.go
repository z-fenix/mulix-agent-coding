package sessionx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"mulix/internal/jsonl"
)

// SessionInfo 是 ScanSessions 返回的会话摘要（meta.json 投影）。
type SessionInfo struct {
	ID        string
	EngineID  string
	Model     string
	Workspace string
	CreatedAt string
}

// Replay 全量读回镜像事件（恢复路径，design §10）：
// 损坏尾部**移 `.damaged` 并告警，不阻塞会话**——好数据照常返回，半行字节
// 原样转储到 events.jsonl.damaged（累计追加，保留隔离历史），主日志截断到
// 最后一个完整行，之后可继续 Append/Replay。
// warnings 非空表示发生过隔离（恢复链选择事件由调用方记入镜像）。
func (s *Store) Replay() ([]any, []string, error) {
	var warnings []string
	path := filepath.Join(s.dir, "events.jsonl")
	quarantine := path + ".damaged"

	r, err := jsonl.OpenReader(path)
	if err != nil {
		return nil, warnings, err
	}
	lines, damaged, err := r.ReadAllWithStatus()
	r.Close()
	if err != nil {
		return nil, warnings, fmt.Errorf("sessionx: read: %w", err)
	}

	if damaged {
		// 1) 半行字节转储（原样 + 换行；累计追加）
		half, err := extractHalfLine(path)
		if err != nil {
			return nil, warnings, fmt.Errorf("sessionx: extract damaged tail: %w", err)
		}
		f, err := os.OpenFile(quarantine, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, warnings, fmt.Errorf("sessionx: open %s: %w", quarantine, err)
		}
		if _, err := f.Write(append(half, '\n')); err != nil {
			f.Close()
			return nil, warnings, fmt.Errorf("sessionx: quarantine: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, warnings, fmt.Errorf("sessionx: quarantine: %w", err)
		}
		// 2) 主日志截断到最后一个完整行（jsonl.Writer.Repair 幂等，且
		//    Store.writer 已知 damagedTail 状态——修复后恢复可追加）
		if err := s.repairLocked(); err != nil {
			return nil, warnings, fmt.Errorf("sessionx: repair: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf(
			"sessionx: damaged tail isolated to %s; %d complete events kept", quarantine, len(lines)))
	}

	events := make([]any, 0, len(lines))
	for i, line := range lines {
		var record struct {
			Kind string          `json:"kind"`
			Ev   json.RawMessage `json:"ev"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, warnings, fmt.Errorf("sessionx: record %d: %w", i, err)
		}
		ev, err := eventFromKind(record.Kind, record.Ev)
		if err != nil {
			return nil, warnings, fmt.Errorf("sessionx: record %d: %w", i, err)
		}
		events = append(events, ev)
	}
	return events, warnings, nil
}

// repairLocked 在 Store 的写入句柄上执行截断修复（恢复路径专用；镜像仍是单写者）。
func (s *Store) repairLocked() error {
	if s.writer == nil {
		return fmt.Errorf("sessionx: store closed")
	}
	return s.writer.Repair()
}

// extractHalfLine 返回文件末尾最后一个换行之后的半行字节（损坏尾部原样）。
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

// ScanSessions 扫描会话根目录（Task 29 doctor / TUI 会话列表用）。
// 含可解析 meta.json 的子目录才算会话；损坏 meta 记警告（不静默丢弃）。
func ScanSessions(root string) ([]SessionInfo, []string, error) {
	var warnings []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, warnings, fmt.Errorf("sessionx: scan %s: %w", root, err)
	}
	var sessions []SessionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if os.IsNotExist(err) {
			continue // 无 meta.json 的目录不是会话
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("sessionx: %s meta unreadable, skipped (%v)", e.Name(), err))
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal(raw, &meta); err != nil {
			warnings = append(warnings, fmt.Sprintf("sessionx: %s meta corrupted, skipped (%v)", e.Name(), err))
			continue
		}
		sessions = append(sessions, SessionInfo{
			ID:        metaString(meta, "id", e.Name()),
			EngineID:  metaString(meta, "engine_session_id", ""),
			Model:     metaString(meta, "model", ""),
			Workspace: metaString(meta, "workspace", ""),
			CreatedAt: metaString(meta, "created_at", ""),
		})
	}
	return sessions, warnings, nil
}

func metaString(meta map[string]any, key, fallback string) string {
	if v, ok := meta[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

// EngineID 返回当前映射的引擎 session id（mulix → engine 方向；--resume 续链用）。
// 引擎 init 到达前为空串。
func (s *Store) EngineID() (string, error) {
	meta, err := s.ReadMeta()
	if err != nil {
		return "", err
	}
	return metaString(meta, "engine_session_id", ""), nil
}

// FindByEngineID 按引擎 session id 反查会话（engine → mulix 方向；恢复链
// --resume <engine-id> 的定位入口）。未命中返回 (零值, false, nil)。
// 空 engineID 直接未命中（防静默错配——空串会匹配到尚未回填的会话）。
// 注意：ScanSessions 的损坏 meta 警告在此被丢弃——Resume 编排（Task 24）如需
// 呈现损坏信号应直接调用 ScanSessions。多会话映射同一引擎 id 属异常
// （返回首个；doctor 经 ScanSessions 检测）。
func FindByEngineID(root, engineID string) (SessionInfo, bool, error) {
	if engineID == "" {
		return SessionInfo{}, false, nil
	}
	sessions, _, err := ScanSessions(root)
	if err != nil {
		return SessionInfo{}, false, err
	}
	for _, s := range sessions {
		if s.EngineID == engineID {
			return s, true, nil
		}
	}
	return SessionInfo{}, false, nil
}
