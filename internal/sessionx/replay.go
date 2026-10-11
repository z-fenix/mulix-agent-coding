package sessionx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"mulix/internal/jsonl"
)

// SessionInfo 是 ScanSessions 返回的会话摘要（meta.json 投影）。
type SessionInfo struct {
	ID         string
	EngineID   string
	Model      string
	Workspace  string
	Unattended bool
	CreatedAt  string
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
	lines, dmg, err := r.ReadAllWithStatus()
	r.Close()
	if err != nil {
		return nil, warnings, fmt.Errorf("sessionx: read: %w", err)
	}

	if dmg.Any() {
		// 1) 损坏段转储（累计追加，保留隔离历史）：半行原样（既有）；
		//    超限段有界头+元信息（全行不落盘，FR-106）。
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
			// 元信息以 NUL 结尾（appendQuarantine 自动补换行）——隔离区是
			// 字节转储历史（无解析契约），NUL 防止头字节与元信息粘连歧义。
			if err := appendQuarantine(quarantine, append([]byte(meta), 0)); err != nil {
				return nil, warnings, err
			}
			if err := appendQuarantine(quarantine, seg.Head); err != nil {
				return nil, warnings, err
			}
		}
		// 2) 物理处置：半行 → 截断修复（既有）；超限 → 原地压实（保留全部
		//    完整行；恢复路径会话静止，独立 R/W 句柄合法）。压实同时移除
		//    尾部半行（lines 已排除全部损坏段）。
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

// appendQuarantine 向隔离区文件追加字节块（无换行结尾时自动补换行；累计
// 追加保留隔离历史——T5-① 复用既有约定）。
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

// compactEventsInPlace 原地压实镜像日志：仅保留 keepLines（顺序不变），
// 中部超限段被物理移除——压实后文件干净，不再反复触发恢复。恢复路径专用
// （会话静止）；Go/Windows 打开文件默认全共享，独立 R/W 句柄与 Store 的
// O_APPEND 写句柄可并存；写毕 Truncate 到新长度并 Sync。
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
		if os.IsNotExist(err) {
			return nil, nil, nil // 根目录不存在 = 尚无会话（缺省布局的首次运行）
		}
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
			ID:         metaString(meta, "id", e.Name()),
			EngineID:   metaString(meta, "engine_session_id", ""),
			Model:      metaString(meta, "model", ""),
			Workspace:  metaString(meta, "workspace", ""),
			Unattended: metaBool(meta, "unattended"),
			CreatedAt:  metaString(meta, "created_at", ""),
		})
	}
	// 新会话在前（列表/恢复引导都是「最近优先」）；created_at RFC3339 字典
	// 序 = 时间序，同秒按目录名稳定序（meta 生成即目录名）。
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt > sessions[j].CreatedAt
	})
	return sessions, warnings, nil
}

func metaBool(meta map[string]any, key string) bool {
	v, _ := meta[key].(bool)
	return v
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
