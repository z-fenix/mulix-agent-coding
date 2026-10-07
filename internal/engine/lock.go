package engine

import (
	"encoding/json"
	"fmt"
	"os"
)

// EngineLock 是 engine.lock 的解码形态（design §7.3；仓库根提交）。
type EngineLock struct {
	CLIVersion    string `json:"cli_version"`
	ProtocolProbe struct {
		InitVersionField string `json:"init_version_field"`
	} `json:"protocol_probe"`
	PinnedAt string `json:"pinned_at"`
	Baseline struct {
		CacheHitRate float64 `json:"cache_hit_rate"`
		Note         string  `json:"note"`
	} `json:"baseline"`
}

// LoadEngineLock 读取并解析 engine.lock（损坏/缺失 → 错误；不静默放行——
// 治理文件损坏等同未治理）。
func LoadEngineLock(path string) (EngineLock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return EngineLock{}, fmt.Errorf("engine: read engine.lock: %w", err)
	}
	var lock EngineLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		return EngineLock{}, fmt.Errorf("engine: parse engine.lock %s: %w", path, err)
	}
	if lock.CLIVersion == "" {
		return EngineLock{}, fmt.Errorf("engine: engine.lock %s missing cli_version", path)
	}
	return lock, nil
}
