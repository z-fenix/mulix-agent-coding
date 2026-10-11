package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mulix/internal/cache"
)

// Tool 是 mulix 内置 MCP 工具的抽象（design §5.2：Go 工具以官方 go-sdk 暴露；
// §8.1 C2：schema 注册期冻结、运行期不增减）。内置工具子包经 init() 调
// Register 自注册；父包不反向 import 子包（design §5.1.4）。
type Tool interface {
	// Name 是 MCP 工具名（注册表键；全局唯一）。
	Name() string
	// Description 是给模型看的人类描述（tools/list 透传）。
	Description() string
	// Schema 是输入 JSON Schema（2020-12 draft，必须 type=object——引擎侧
	// 工具调用参数形态约束；无输入工具用 {"type":"object","properties":{}}）。
	// 注册期做一次 canonicalize（解析+键排序重序列化）并冻结——此后每次
	// 列出/比较都是同一字节形态。
	Schema() json.RawMessage
	// ReadOnly 报告工具是否不改环境（→ ToolAnnotations.ReadOnlyHint；
	// 权限策略/审计消费）。
	ReadOnly() bool
	// Execute 执行工具。args 是引擎发来的原始参数。返回 text content 载荷
	//（P1 内置工具统一返回 JSON 文本）；返回 error → IsError 结果（消息透出，
	// 协议层不失败）。
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// registeredTool 是注册表条目：工具 + 注册期冻结的 canonical schema。
type registeredTool struct {
	Tool
	frozen json.RawMessage
}

var (
	regMu    sync.RWMutex
	registry = map[string]registeredTool{}
)

// Snapshot 返回当前已注册工具的副本(011 FR-103 读原语:effect 可逆注册的
// 未来写面消费;副本不可变——调用后的注册/注销不影响其内容)。
func Snapshot() []Tool {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Tool, 0, len(registry))
	for name, rt := range registry {
		_ = name
		out = append(out, rt.Tool)
	}
	return out
}

// Register 全局注册一个工具（重名/空名/非法 schema panic——注册期冲突是
// 程序员错误，不静默覆盖；C2 冻结语义：进程生命周期内不增减，schema 于此
// 时一次 canonicalize）。
func Register(t Tool) {
	if t == nil {
		panic("registry: register nil tool")
	}
	if t.Name() == "" {
		panic("registry: tool with empty name")
	}
	if !validToolName(t.Name()) {
		panic(fmt.Sprintf("registry: tool name %q contains characters outside [A-Za-z0-9_-] (MCP spec charset)", t.Name()))
	}
	frozen := canonicalize(t.Schema())
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[t.Name()]; dup {
		panic(fmt.Sprintf("registry: duplicate tool %q", t.Name()))
	}
	registry[t.Name()] = registeredTool{Tool: t, frozen: frozen}
}

// validToolName 钉 MCP 工具名 charset（^[A-Za-z0-9_-]{1,128}$）：SDK 侧仅
// 告警不拒绝——非法名要到引擎侧才失败，注册期 fail-fast（评审 Minor-4）。
func validToolName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// registeredTools 返回按名字排序的已注册条目快照（顺序稳定的第一道）。
func registeredTools() []registeredTool {
	regMu.RLock()
	defer regMu.RUnlock()
	entries := slices.Collect(maps.Values(registry))
	slices.SortFunc(entries, func(a, b registeredTool) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries
}

// Registered 返回当前注册的工具名快照（名字序）——诊断/测试/doctor 用。
func Registered() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Lookup 按名取回一个已注册工具（只读快照——调用方可直接 Execute；未注册
// → (nil, false)）。组合根 e2e 与诊断消费（M12/T12.4：经工具面驱动子代理
// 回调桥，钉 RegisterConditional 的组合根接线）。
func Lookup(name string) (Tool, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	t, ok := registry[name]
	if !ok {
		return nil, false
	}
	return t, true
}

// Server 是进程内 MCP 服务器框架（官方 go-sdk 承载）：启动时从全局注册表
// 冻结工具面。cache.Registry 对齐——SelfCommand 配置后，BootComposer 的
// --mcp-config 生成 mulix stdio 自 spawn 条目（design §7.2：引擎经 stdio
// 直连 mulix 工具；权限回路的 can_use_tool 即由此进入 mulix）。
type Server struct {
	mcpServer *mcp.Server
	name      string
	selfCmd   []string
}

// Options 是 NewServer 参数。
type Options struct {
	// Name/Version 是 server implementation 元数据（initialize 应答）。
	Name, Version string
	// SelfCommand 是 mcp-config 条目的 mulix 自 spawn 命令（argv 全量）。
	// 为空 = 不暴露 stdio 条目（纯进程内用途）。
	SelfCommand []string
}

// NewServer 冻结当前注册表并构建服务器（C2：此后注册的新工具不影响本
// Server；注册期在进程启动时完成，正常无迟到注册）。SelfCommand 与 Name
// 必须同给（否则 Compose 才发现空名——wiring 期 fail-fast，评审 Minor-5）。
func NewServer(opts Options) *Server {
	if len(opts.SelfCommand) > 0 && opts.Name == "" {
		panic("registry: server Name required when SelfCommand is set")
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: opts.Name, Version: opts.Version}, nil)
	for _, t := range registeredTools() {
		ms.AddTool(&mcp.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: json.RawMessage(t.frozen),
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: t.ReadOnly()},
		}, handlerFor(t.Tool))
	}
	return &Server{mcpServer: ms, name: opts.Name, selfCmd: slices.Clone(opts.SelfCommand)}
}

// handlerFor 包装 mulix Tool 为 go-sdk ToolHandler（Execute 错误 → IsError
// 结果透出消息；协议层不失败——引擎侧工具错误走正常错误呈现）。
func handlerFor(t Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, err := t.Execute(ctx, req.Params.Arguments)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: out}},
		}, nil
	}
}

// canonicalize 注册期一次规范化：解析（兼校验）→ 键排序重序列化 → 冻结
// 字节。同一 schema 语义每次注册产出同一字节（C2「注册期一次 canonicalize」）。
// 附加校验：type 必须显式为 "object"（go-sdk AddTool 的硬约束，前置到注册期
// 给出可读错误，而非构建 Server 时 panic 难排查）。
func canonicalize(raw json.RawMessage) json.RawMessage {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		panic(fmt.Sprintf("registry: invalid tool schema %s: %v", string(raw), err))
	}
	if typ, _ := v["type"].(string); typ != "object" {
		panic(fmt.Sprintf("registry: tool schema must have type \"object\" (got %v)", v["type"]))
	}
	out, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("registry: canonicalize tool schema %s: %v", string(raw), err))
	}
	return out
}

// MCPServers 实现 cache.Registry（编译期钉见测试）：SelfCommand 非空时返回
// 唯一的 mulix stdio 条目（Command 显式顺序；cache 侧再做排序归一——双保险）。
func (s *Server) MCPServers() []cache.MCPServer {
	if len(s.selfCmd) == 0 {
		return nil
	}
	// 注意（终评审 Important-1）：条目 env 必须保持为空——运行期寻址信息
	//（权限回调地址）经 Boot Env 传递，不入 mcp-config（SC-003 字节稳定 +
	// 设计 §8.1 C2 注册面冻结）。新增 env 时必须有冻结面豁免裁决。
	return []cache.MCPServer{{Name: s.name, Command: slices.Clone(s.selfCmd)}}
}

// ServeStdio 以 stdio 传输运行服务器（阻塞至 ctx 取消或对端 EOF）——
// `mulix mcp serve` 子命令的主体（Task 27 接线）。
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}
