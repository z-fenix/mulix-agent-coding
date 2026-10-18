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

// ProfileMode 是运行模式（standard=全工具集缺省；minimal=薄环境评测）。
type ProfileMode string

const (
	ProfileStandard ProfileMode = "standard"
	ProfileMinimal  ProfileMode = "minimal"
)

// ProfileConfig 是 [profile] 节（014 FR-101）。
type ProfileConfig struct {
	RawMode string `toml:"mode"`
}

// Mode 返回标准化后的运行模式（""→standard；非法在 Load 末尾 fail-closed）。
func (p ProfileConfig) Normalized() ProfileMode {
	if p.RawMode == string(ProfileMinimal) {
		return ProfileMinimal
	}
	return ProfileStandard
}

// EventbusConfig 是 [eventbus] 节(018 FR-101):镜像→总线桥生产接线门控。
// 缺省关(不写=行为与 017 逐字节一致);显式 true/false;非法值 Load 末尾
// fail-closed(014 profile 同款)。
type EventbusConfig struct {
	RawEnable string `toml:"enable"`
}

// Enabled 返回门控态;非法值报错(错误含原值)。
func (e EventbusConfig) Enabled() (bool, error) {
	switch e.RawEnable {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("config: eventbus.enable %q must be true or false (TOML 字符串形态: enable = \"true\")", e.RawEnable)
	}
}

// WebConfig 是 [web] 节(2026-10-18-web-ui-mvp Task 1):本地 web UI 服务参数。
// 默认仅监听本机回环(spec FR-001);WorkspaceRoots 空 = serve 时取 cwd;
// 非法值在 Load 末尾 fail-closed(port 界外/bind 显式空串/idle_close_minutes
// 非正),与 [eventbus] 门同款,拒绝的配置不得成为 last good。
type WebConfig struct {
	Port             int      `toml:"port"`
	Bind             string   `toml:"bind"`
	WorkspaceRoots   []string `toml:"workspace_roots"`
	IdleCloseMinutes int      `toml:"idle_close_minutes"`
}

// Config 是 mulix 的最终生效配置（design §12 骨架字段）。
type Config struct {
	DefaultModel string            `toml:"default_model"`
	Cache        CacheConfig       `toml:"cache"`
	Permissions  PermissionsConfig `toml:"permissions"`
	Engine       EngineConfig      `toml:"engine"`
	Tools        ToolsConfig       `toml:"tools"`
	Memory       MemoryConfig      `toml:"memory"`
	Subagent     SubagentConfig    `toml:"subagent"`
	MCP          MCPConfig         `toml:"mcp"`
	Profile      ProfileConfig     `toml:"profile"`
	Eventbus     EventbusConfig    `toml:"eventbus"`
	Web          WebConfig         `toml:"web"`
	Hooks        HooksConfig       `toml:"hooks"`
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
	// Path 是引擎可执行（PATH 解析或绝对路径；空值回退 "claude"——与 doctor
	// 的 LookPath 探测同一约定）。终评 Critical-1：composeBoot 缺 EnginePath
	// 接线时 ClaudeHost.Start fail-closed，真实引擎无法拉起。
	Path string `toml:"path"`
}

// ToolsConfig 是内置 Go 工具开关（注册期冻结）。
type ToolsConfig struct {
	MemorySearch bool `toml:"memory_search"` // P2
}

// SubagentConfig 是子代理编排限额（P2/M12；design 002 §3：默认深度 3、并发 4）。
type SubagentConfig struct {
	MaxDepth      int `toml:"max_depth"`      // 委派链最大深度
	MaxConcurrent int `toml:"max_concurrent"` // 同时运行的子代理上限
}

// MemoryConfig 是记忆召回参数（P2/M13；design 002 §4）。
type MemoryConfig struct {
	RecallTopK        int `toml:"recall_top_k"`        // 每回合召回条数上限（默认 3）
	RecallBudgetChars int `toml:"recall_budget_chars"` // 注入预算上限（rune 数；默认 2000——spec Assumptions）
}

// MCPConfig 是外部 MCP server 声明面（P2/M14；design 002 §5：配置透传——
// mulix 只声明，引擎原生托管外部 server 进程，mulix 不代理协议）。
type MCPConfig struct {
	Servers []MCPServerDecl `toml:"servers"`
}

// MCPServerDecl 是单个外部 MCP server 的声明（[[mcp.servers]] 数组元素）。
// Command 是 argv 全量（argv[0] = 可执行文件）；Env 静态透传进 mcp-config
// （不做运行期注入——终评 Important-1 教训）；TimeoutMS 缺省 0 = Compose 侧
// 解释为默认 60000（R1：以引擎 env MCP_TOOL_TIMEOUT 透传，全组取最小值）。
// 名称校验（保留名/重名/空名）与空 command 校验在 cache.Compose 收口。
type MCPServerDecl struct {
	Name      string            `toml:"name"`
	Command   []string          `toml:"command"`
	Env       map[string]string `toml:"env"`
	TimeoutMS int               `toml:"timeout_ms"`
}

// HooksConfig 是用户自定义钩子声明面（P2/M16；design 002 §7）。
type HooksConfig struct {
	Custom HooksCustomConfig `toml:"custom"`
}

// HooksCustomConfig 是 [hooks.custom] 组。
type HooksCustomConfig struct {
	Rules []HookRule `toml:"rules"`
}

// HookRule 是单个自定义钩子规则（[[hooks.custom.rules]] 数组元素）。取值
// 校验（event ∈ P1 四时点、command 非空、0 ≤ timeout_ms ≤ 30000）在
// guard.NewScriptConsumer 构造期收口（启动拒绝 fail-fast，FR-111）——config
// 只负责分层解析，与 [mcp.servers] 的「Compose 侧校验」同款分工；TimeoutMS
// 缺省 0 = 消费侧解释为默认超时（M14 缺省语义同款）。
type HookRule struct {
	Event     string   `toml:"event"`
	Command   []string `toml:"command"`
	TimeoutMS int      `toml:"timeout_ms"`
}

func defaultConfig() Config {
	return Config{
		DefaultModel: "claude-sonnet-4-5",
		Cache:        CacheConfig{HitRateFloor: 0.70, ColdAfterIdle: "4m"},
		Permissions:  PermissionsConfig{Preset: "default"},
		Engine:       EngineConfig{AllowDrift: false, Path: "claude"},
		Tools:        ToolsConfig{MemorySearch: false},
		Memory:       MemoryConfig{RecallTopK: 3, RecallBudgetChars: 2000},
		Subagent:     SubagentConfig{MaxDepth: 3, MaxConcurrent: 4},
		Web:          WebConfig{Port: 3180, Bind: "127.0.0.1", IdleCloseMinutes: 10},
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
	// 项目层（终评冒烟发现：此前缺失——项目 mulix.toml 从未被加载，FR-016
	// 的 flag > project > user > default 分层退化成 user > default）。
	// 工作区根 = 进程 cwd（cli 注入 cwd() 为 Workspace 同一口径）。
	return Paths{
		Project: "mulix.toml",
		User:    filepath.Join(home, ".mulix", "config.toml"),
	}, nil
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
	// profile mode 校验（014 FR-101：fail-closed，错误含 mode 值）——
	// 在 lastGood 赋值之前(拒绝的配置不得成为 last good)。
	switch cfg.Profile.RawMode {
	case "", string(ProfileStandard), string(ProfileMinimal):
	default:
		return nil, warnings, fmt.Errorf("config: profile.mode %q must be standard or minimal", cfg.Profile.RawMode)
	}
	// eventbus.enable 校验(018 FR-101:三态 fail-closed,错误含原值)——
	// 在 lastGood 赋值之前(拒绝的配置不得成为 last good)。
	if _, eberr := cfg.Eventbus.Enabled(); eberr != nil {
		return nil, warnings, eberr
	}
	// [web] 校验(2026-10-18-web-ui-mvp Task 1:非法值 fail-closed,错误含 "web")
	// ——同在 lastGood 赋值之前;bind 空串只可能是显式配置(缺省 127.0.0.1)。
	if cfg.Web.Port <= 0 || cfg.Web.Port > 65535 {
		return nil, warnings, fmt.Errorf("config: web.port %d must be in [1, 65535]", cfg.Web.Port)
	}
	if cfg.Web.Bind == "" {
		return nil, warnings, fmt.Errorf("config: web.bind %q must not be empty (显式空串拒绝;缺省 127.0.0.1)", cfg.Web.Bind)
	}
	if cfg.Web.IdleCloseMinutes <= 0 {
		return nil, warnings, fmt.Errorf("config: web.idle_close_minutes %d must be positive", cfg.Web.IdleCloseMinutes)
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
