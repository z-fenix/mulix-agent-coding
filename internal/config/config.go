package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config 是 mulix 的最终生效配置（design §12 骨架字段）。
type Config struct {
	DefaultModel string            `toml:"default_model"`
	Cache        CacheConfig       `toml:"cache"`
	Permissions  PermissionsConfig `toml:"permissions"`
	Engine       EngineConfig      `toml:"engine"`
	Tools        ToolsConfig       `toml:"tools"`
}

// CacheConfig 是缓存三层可观测性相关配置。
type CacheConfig struct {
	HitRateFloor  float64 `toml:"hit_rate_floor"`  // 告警阈值（数值可项目覆盖）
	ColdAfterIdle string  `toml:"cold_after_idle"` // TTL 感知阈值
}

// PermissionsConfig 是权限预设与清单。
type PermissionsConfig struct {
	Preset string   `toml:"preset"` // default|acceptEdits|plan|danger-full（danger-full 为 user-global 准入）
	Deny   []string `toml:"deny"`
	Allow  []string `toml:"allow"`
}

// EngineConfig 是引擎相关策略。
type EngineConfig struct {
	AllowDrift bool `toml:"allow_drift"` // user-global；true 时版本漂移仅告警不拒绝
}

// ToolsConfig 是内置 Go 工具开关（注册期冻结）。
type ToolsConfig struct {
	MemorySearch bool `toml:"memory_search"` // P2
}

func defaultConfig() Config {
	return Config{
		DefaultModel: "claude-sonnet-4-5",
		Cache:        CacheConfig{HitRateFloor: 0.70, ColdAfterIdle: "4m"},
		Permissions:  PermissionsConfig{Preset: "default"},
		Engine:       EngineConfig{AllowDrift: false},
		Tools:        ToolsConfig{MemorySearch: false},
	}
}

// Flags 是命令行覆盖层（最高优先级；操作者显式动作，不受项目层锁定约束——
// 锁定防的是恶意 clone 的项目文件，不是操作者本人）。
type Flags struct {
	Model      *string // default_model
	Preset     *string // permissions.preset
	AllowDrift *bool   // engine.allow_drift
}

// Paths 是配置文件路径（测试注入；生产用 DefaultPaths）。
// 空字符串表示该层不存在。
type Paths struct {
	Project string // 项目 mulix.toml
	User    string // ~/.mulix/config.toml
}

// DefaultPaths 返回生产路径布局（用户层 ~/.mulix/config.toml）。
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("config: user home: %w", err)
	}
	return Paths{User: filepath.Join(home, ".mulix", "config.toml")}, nil
}

// keyLocks 是 user-global 锁定项（键级）：项目层不得设置这些键（防恶意 clone 提权）。
// P1 骨架仅 engine.allow_drift 实键；凭据来源、遥测开关、缓存告警的上报行为
// 在键位引入时经 RegisterLockedKey 纳入（机制可扩展，键位引入即锁定）。
var keyLocks = map[string]bool{
	"engine.allow_drift": true,
}

// RegisterLockedKey 注册一个点分路径为 user-global 锁定项（扩展机制）。
func RegisterLockedKey(path string) { keyLocks[path] = true }

// valueLocks 是 value 级锁定项：项目层不得把这些路径设为特权值；
// 用户层（或 flag）是唯一准入来源。
var valueLocks = map[string]map[string]bool{
	"permissions.preset": {"danger-full": true},
}

// Loader 负责分层加载并锚定 last-known-good（最近一次可用加载结果）。
type Loader struct {
	lastGood *Config
}

// NewLoader 创建加载器。
func NewLoader() *Loader { return &Loader{} }

// LastGood 返回最近一次可用加载的配置（含跳层加载），供恢复路径使用。
func (l *Loader) LastGood() *Config { return l.lastGood }

// Load 按 flag > project > user > default 分层合并（design §12）。
// 损坏层跳过并计入 loadWarnings（绝不改写原文件）；锁定项违规同样只告警不生效。
// 返回的配置始终可用（默认层兜底），error 保留给未来的硬失败场景。
func (l *Loader) Load(flags Flags, paths Paths) (*Config, []string, error) {
	merged, warnings, err := l.loadRaw(flags, paths)
	if err != nil {
		return nil, warnings, err
	}
	cfg := defaultConfig()
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(merged); err != nil {
		return nil, warnings, fmt.Errorf("config: encode merged: %w", err)
	}
	if _, err := toml.Decode(buf.String(), &cfg); err != nil {
		return nil, warnings, fmt.Errorf("config: decode merged: %w", err)
	}
	l.lastGood = &cfg
	return &cfg, warnings, nil
}

// loadRaw 解析各层、执行锁定规则并返回合并后的原始视图（白盒测试用）。
func (l *Loader) loadRaw(flags Flags, paths Paths) (map[string]any, []string, error) {
	var warnings []string
	user, w := parseLayer(paths.User, "user")
	if w != "" {
		warnings = append(warnings, w)
	}
	project, w := parseLayer(paths.Project, "project")
	if w != "" {
		warnings = append(warnings, w)
	}
	warnings = append(warnings, applyLockedRules(project)...)

	merged := map[string]any{}
	mergeInto(merged, user)
	mergeInto(merged, project)
	mergeInto(merged, flagsMap(flags))
	return merged, warnings, nil
}

// parseLayer 解析单层文件；路径为空或不存在返回 (nil, "")（正常缺省）。
// 文件存在但不可读/损坏/类型不符返回警告（跳过该层——恢复语义，绝不改写原文件）。
func parseLayer(path, layer string) (map[string]any, string) {
	if path == "" {
		return nil, ""
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, "" // 缺省层不是警告
		}
		return nil, fmt.Sprintf("config: %s layer %s unreadable, ignored (%v)", layer, path, err)
	}
	var m map[string]any
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, fmt.Sprintf("config: %s layer %s corrupted, ignored (%v)", layer, path, err)
	}
	// 语法有效但类型不符（如 hit_rate_floor = "0.9"）同样按损坏层处理（skip+warn），
	// 保证恢复契约完整：Load 永远返回可用配置，最终解码不会硬失败。
	if w := validateLayer(m, layer, path); w != "" {
		return nil, w
	}
	return m, ""
}

// validateLayer 校验层的值类型与 Config 模式相容（解码探针）。
// 返回空串表示相容；否则返回损坏警告。
func validateLayer(m map[string]any, layer, path string) string {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return fmt.Sprintf("config: %s layer %s corrupted, ignored (%v)", layer, path, err)
	}
	probe := Config{}
	if _, err := toml.Decode(buf.String(), &probe); err != nil {
		return fmt.Sprintf("config: %s layer %s corrupted, ignored (%v)", layer, path, err)
	}
	return ""
}

// applyLockedRules 对项目层执行锁定规则（就地删除违规键），返回警告。
// 仅作用于项目层：用户层是 user-global 锁定项的权威来源，flag 是操作者动作。
func applyLockedRules(project map[string]any) []string {
	var warnings []string
	for _, path := range sortedLockPaths(keyLocks) {
		if _, present := getDotted(project, path); present {
			deleteDotted(project, path)
			warnings = append(warnings, fmt.Sprintf(
				"config: project layer may not override user-global key %s (ignored)", path))
		}
	}
	for _, path := range sortedLockPaths(valueLocks) {
		v, present := getDotted(project, path)
		if !present {
			continue
		}
		if s, ok := v.(string); ok && valueLocks[path][s] {
			deleteDotted(project, path)
			warnings = append(warnings, fmt.Sprintf(
				"config: project layer may not set %s to privileged value %q (ignored)", path, s))
		}
	}
	return warnings
}

func sortedLockPaths[V any](locks map[string]V) []string {
	paths := make([]string, 0, len(locks))
	for p := range locks {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// flagsMap 把 flag 层转成合并视图（仅显式设置的键）。
func flagsMap(f Flags) map[string]any {
	m := map[string]any{}
	if f.Model != nil {
		m["default_model"] = *f.Model
	}
	if f.Preset != nil {
		m["permissions"] = map[string]any{"preset": *f.Preset}
	}
	if f.AllowDrift != nil {
		m["engine"] = map[string]any{"allow_drift": *f.AllowDrift}
	}
	return m
}

// mergeInto 把 src 深合并进 dst：子表递归，标量/数组整键替换。
func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if dm, ok := dst[k].(map[string]any); ok {
			if sm, ok := v.(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// getDotted 按点分路径取值。
// 局限（已记录的形状依赖）：TOML 带引号的字面量键（"engine.allow_drift" = true）
// 按整键存储、不匹配点分路径，锁定规则不命中——但该键也从不映射进 Config
// （惰性无效），不构成提权路径。
func getDotted(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = m
	for i, p := range parts {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, present := mm[p]
		if !present {
			return nil, false
		}
		if i == len(parts)-1 {
			return v, true
		}
		cur = v
	}
	return nil, false
}

// deleteDotted 按点分路径删除键（并向上裁剪空子表）。
func deleteDotted(m map[string]any, path string) {
	parts := strings.Split(path, ".")
	stack := make([]map[string]any, 0, len(parts))
	cur := m
	for i, p := range parts {
		next, ok := cur[p].(map[string]any)
		if !ok {
			if i == len(parts)-1 {
				delete(cur, p)
			}
			return
		}
		if i == len(parts)-1 {
			delete(cur, p)
			// 向上裁剪空子表
			for j := len(stack) - 1; j >= 0; j-- {
				parent := stack[j]
				child := parts[j]
				if len(parent[child].(map[string]any)) == 0 {
					delete(parent, child)
					continue
				}
				break
			}
			return
		}
		stack = append(stack, cur)
		cur = next
	}
}
