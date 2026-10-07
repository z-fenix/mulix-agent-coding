package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"mulix/internal/config"
)

// MCPServer 是 Boot 组合所需的最小注册面条目（Task 25 registry 对齐完整接口；
// 实现方必须在注册期冻结内容——运行期不增减工具，design §8.1 C2）。
type MCPServer struct {
	Name    string
	Command []string          // argv[0] = 可执行文件，其余为参数
	Env     map[string]string // 可为 nil
}

// Registry 是 cache 依赖的注册面（Task 25 对齐；实现必须稳定：同注册内容
// 每次调用返回等价清单——BootComposer 对输出再做排序归一，双保险）。
type Registry interface {
	MCPServers() []MCPServer
}

// BootArtifact 是 Compose 的产物：引擎拉起参数的全部 Boot 面（design §7.2/§8.1）。
// 会话内字节不变（C1/C2）；Fingerprint 是启动面摘要（可观测性）。
// 维护注意：新增字段必须同步加入 fingerprint() 的 canonical 结构体，否则新字段
// 不进摘要（指纹漂移）。
type BootArtifact struct {
	AppendSystemPrompt string   // --append-system-prompt 参数（静态模板 + 启动参数快照）
	MCPConfigJSON      string   // --mcp-config 文件内容（键排序、列表显式顺序）
	AllowedTools       []string // --allowedTools（config allow 清单，显式顺序去重）
	DisallowedTools    []string // --disallowedTools（config deny 清单，显式顺序去重）
	PermissionMode     string   // --permission-mode（引擎侧映射值）
	Fingerprint        string   // 启动面 sha256 前 16 hex（同输入同指纹）
}

// systemPromptTemplate 是 C1 静态模板：%s 依次为 workspace、model、
// permission preset（用户面语义；引擎实际 mode 是映射后的值，二者刻意不同——
// Task 20/21 勿把提示词里的 mode 行当引擎模式）。只追加不替换引擎默认提示。
const systemPromptTemplate = `You are running inside mulix, a coding-agent harness.

Startup snapshot (frozen for this session):
- Workspace: %s
- Engine model: %s
- Permission mode: %s

Rules:
- Dynamic context (memory recall, runtime state) is appended by mulix at the tail of user messages; it never appears in this prompt.
- Tool approvals flow through mulix's permission queue with a fail-closed timeout; unapproved tools are denied by the engine.
- Keep the first response turn identical in structure: greets only when greeted.`

// permissionModeMapping 把 mulix 预设映射为引擎 --permission-mode（design §9）：
// 引擎侧始终保持严格审批；danger-full 的放行由 Go 策略层在权限工具里叠加，
// 引擎 mode 不放宽（fail-closed 姿态）。
var permissionModeMapping = map[string]string{
	"default":     "default",
	"acceptEdits": "acceptEdits",
	"plan":        "plan",
	"danger-full": "default",
}

// Compose 组装 Boot 面（纯函数；同输入同字节，design §8.1 护栏）。
// workspace/模型/权限模式进入追加段快照；registry 清单排序归一后序列化；
// allow/deny 清单保持配置显式顺序（去重保首现）。
func Compose(cfg config.Config, workspace string, reg Registry) (BootArtifact, error) {
	mode, ok := permissionModeMapping[cfg.Permissions.Preset]
	if !ok {
		return BootArtifact{}, fmt.Errorf("cache: unknown permission preset %q", cfg.Permissions.Preset)
	}

	prompt := fmt.Sprintf(systemPromptTemplate, workspace, cfg.DefaultModel, cfg.Permissions.Preset)

	mcpJSON, err := composeMCPConfig(reg)
	if err != nil {
		return BootArtifact{}, err
	}

	artifact := BootArtifact{
		AppendSystemPrompt: prompt,
		MCPConfigJSON:      mcpJSON,
		AllowedTools:       dedupePreserveOrder(cfg.Permissions.Allow),
		DisallowedTools:    dedupePreserveOrder(cfg.Permissions.Deny),
		PermissionMode:     mode,
	}
	artifact.Fingerprint = fingerprint(artifact)
	return artifact, nil
}

// composeMCPConfig 稳定序列化 --mcp-config 内容：
// 服务器按名字排序（sort.Slice 归一是第一道；最终字节序由 encoding/json 的
// map 键排序保证——文档化行为，两层都在时改名/重排不会破坏稳定性）、
// env 键由 encoding/json 排序、args 保持显式顺序。
func composeMCPConfig(reg Registry) (string, error) {
	servers := map[string]any{}
	if reg != nil {
		normalized := make([]MCPServer, 0)
		normalized = append(normalized, reg.MCPServers()...)
		sort.Slice(normalized, func(i, j int) bool { return normalized[i].Name < normalized[j].Name })
		for _, s := range normalized {
			if s.Name == "" {
				return "", fmt.Errorf("cache: mcp server with empty name")
			}
			if len(s.Command) == 0 {
				return "", fmt.Errorf("cache: mcp server %q has empty command", s.Name)
			}
			if _, dup := servers[s.Name]; dup {
				// 重名会让「哪个定义存活」依赖输入顺序——破坏 order-insensitivity，
				// 且被覆盖的工具从冻结面静默消失。boot 拒绝，不静默覆盖。
				return "", fmt.Errorf("cache: duplicate mcp server name %q", s.Name)
			}
			def := map[string]any{
				"type":    "stdio",
				"command": s.Command[0],
			}
			if len(s.Command) > 1 {
				def["args"] = s.Command[1:]
			}
			if len(s.Env) > 0 {
				def["env"] = s.Env
			}
			servers[s.Name] = def
		}
	}
	payload, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", fmt.Errorf("cache: marshal mcp-config: %w", err)
	}
	return string(payload), nil
}

// dedupePreserveOrder 去重保首现（清单显式顺序，design §8.1「列表显式顺序」）。
func dedupePreserveOrder(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// fingerprint 计算启动面摘要（sha256 前 16 hex；规范化 JSON 保证字段序稳定）。
func fingerprint(a BootArtifact) string {
	canonical, err := json.Marshal(struct {
		AppendSystemPrompt string   `json:"append_system_prompt"`
		MCPConfigJSON      string   `json:"mcp_config_json"`
		AllowedTools       []string `json:"allowed_tools"`
		DisallowedTools    []string `json:"disallowed_tools"`
		PermissionMode     string   `json:"permission_mode"`
	}{
		AppendSystemPrompt: a.AppendSystemPrompt,
		MCPConfigJSON:      a.MCPConfigJSON,
		AllowedTools:       a.AllowedTools,
		DisallowedTools:    a.DisallowedTools,
		PermissionMode:     a.PermissionMode,
	})
	if err != nil {
		// 结构体 marshal 不会失败；防御性兜底
		return "unfingerprintable"
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])[:16]
}
