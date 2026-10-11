package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mulix/internal/cache"
)

// ---- 测试基建 ----

// resetRegistry 清空全局注册表（同包白盒；测试串行）。
func resetRegistry(t *testing.T) {
	t.Helper()
	regMu.Lock()
	registry = map[string]registeredTool{}
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		registry = map[string]registeredTool{}
		regMu.Unlock()
	})
}

// stubTool 是测试用最小工具实现。
type stubTool struct {
	name, desc, schema string
	readOnly           bool
	fail               bool
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return s.desc }
func (s *stubTool) Schema() json.RawMessage {
	return json.RawMessage(s.schema)
}
func (s *stubTool) ReadOnly() bool { return s.readOnly }
func (s *stubTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	if s.fail {
		return "", errors.New("stub execute failure: " + string(args))
	}
	return `{"echo":` + string(args) + `}`, nil
}

// connectInMemory 起 server 并以内存传输连上客户端会话。
func connectInMemory(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	go func() { _ = s.mcpServer.Run(ctx, serverT) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "registry-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// ---- 用例 ----

// TestRegisterPins pins：重名注册 panic（注册期冲突是程序员错误，不静默
// 覆盖）；空名 panic；非 object schema panic（go-sdk AddTool 硬约束前置到
// 注册期）；正常注册后可列出。
func TestRegisterPins(t *testing.T) {
	resetRegistry(t)
	mustPanic := func(why string, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("%s: no panic", why)
			}
		}()
		fn()
	}
	mustPanic("empty name", func() { Register(&stubTool{name: "", schema: `{"type":"object"}`}) })
	mustPanic("non-object schema", func() {
		Register(&stubTool{name: "arr_tool", schema: `{"type":"array"}`})
	})
	mustPanic("malformed schema", func() {
		Register(&stubTool{name: "bad_tool", schema: `not-json`})
	})
	mustPanic("invalid charset name", func() {
		Register(&stubTool{name: "mulix usage", schema: `{"type":"object"}`})
	})
	Register(&stubTool{name: "dup_tool", schema: `{"type":"object"}`})
	mustPanic("duplicate", func() { Register(&stubTool{name: "dup_tool", schema: `{"type":"object"}`}) })
	if len(registry) != 1 {
		t.Fatalf("registry size = %d, want 1", len(registry))
	}
}

// TestCanonicalBytesDirect pins：canonicalize 产出与输入无关的确定字节形态
// （键排序、紧凑）——评审 Minor-1：客户端 marshal 往返会掩盖 canonicalize
// 回归，这里直测冻结字节。
func TestCanonicalBytesDirect(t *testing.T) {
	got := string(canonicalize(json.RawMessage(`{"type":"object","properties":{"z":{},"a":{}},"required":["z"]}`)))
	want := `{"properties":{"a":{},"z":{}},"required":["z"],"type":"object"}`
	if got != want {
		t.Fatalf("canonicalize = %s, want %s", got, want)
	}
}

// TestServerToolListStableOrder pins：工具列表按名字序、两次快照字节一致
// （C2 顺序稳定；SDK featureSet 排序 + registry 注册期冻结双保险）。
func TestServerToolListStableOrder(t *testing.T) {
	resetRegistry(t)
	Register(&stubTool{name: "zeta_tool", schema: `{"type":"object"}`})
	Register(&stubTool{name: "alpha_tool", schema: `{"type":"object"}`})
	Register(&stubTool{name: "mid_tool", schema: `{"type":"object"}`})
	s := NewServer(Options{Name: "mulix", Version: "test"})
	cs := connectInMemory(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r1, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools 1: %v", err)
	}
	r2, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools 2: %v", err)
	}
	b1, _ := json.Marshal(r1.Tools)
	b2, _ := json.Marshal(r2.Tools)
	if string(b1) != string(b2) {
		t.Fatalf("tool list not byte-stable:\n%s\n%s", b1, b2)
	}
	var names []string
	for _, tl := range r1.Tools {
		names = append(names, tl.Name)
	}
	want := `["alpha_tool","mid_tool","zeta_tool"]`
	if got, _ := json.Marshal(names); string(got) != want {
		t.Fatalf("tool order = %s, want %s", got, want)
	}
}

// TestServerSchemaFrozen pins：schema 注册期一次 canonicalize——注册乱序键
// 的 schema，列表中两次快照字节一致且为规范化形态（键排序、紧凑）。
func TestServerSchemaFrozen(t *testing.T) {
	resetRegistry(t)
	Register(&stubTool{name: "frozen_tool",
		schema: `{"type":"object","properties":{"z":{},"a":{}},"required":["z"]}`})
	s := NewServer(Options{Name: "mulix", Version: "test"})
	cs := connectInMemory(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r1, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list 1: %v", err)
	}
	r2, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	b1, _ := json.Marshal(r1.Tools[0].InputSchema)
	b2, _ := json.Marshal(r2.Tools[0].InputSchema)
	if string(b1) != string(b2) {
		t.Fatalf("schema not frozen:\n%s\n%s", b1, b2)
	}
	// canonicalize：map 重 marshal 键序稳定——第二次 Server 实例同字节
	resetRegistry(t)
	Register(&stubTool{name: "frozen_tool",
		schema: `{"required":["z"],"properties":{"a":{},"z":{}},"type":"object"}`})
	s2 := NewServer(Options{Name: "mulix", Version: "test"})
	cs2 := connectInMemory(t, s2)
	r3, err := cs2.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list 3: %v", err)
	}
	b3, _ := json.Marshal(r3.Tools[0].InputSchema)
	if string(b1) != string(b3) {
		t.Fatalf("canonicalize not deterministic:\n%s\n%s", b1, b3)
	}
}

// TestServerCallTool pins：Execute 输出 → text content；Execute 错误 →
// IsError 结果（协议错误不 panic、消息透出）。
func TestServerCallTool(t *testing.T) {
	resetRegistry(t)
	Register(&stubTool{name: "echo_tool", schema: `{"type":"object"}`})
	Register(&stubTool{name: "bad_tool", schema: `{"type":"object"}`, fail: true})
	s := NewServer(Options{Name: "mulix", Version: "test"})
	cs := connectInMemory(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "echo_tool", Arguments: map[string]any{"x": float64(1)},
	})
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if res.IsError {
		t.Fatalf("echo result IsError: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatal("echo result empty content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, `"x":1`) {
		t.Fatalf("echo content = %+v", res.Content[0])
	}
	res2, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "bad_tool"})
	if err != nil {
		t.Fatalf("call bad (protocol level must not fail): %v", err)
	}
	if !res2.IsError {
		t.Fatalf("bad tool result must be IsError: %+v", res2)
	}
	if len(res2.Content) == 0 || !strings.Contains(res2.Content[0].(*mcp.TextContent).Text, "stub execute failure") {
		t.Fatalf("bad tool content = %+v", res2.Content)
	}
}

// TestReadOnlyAnnotation pins：ReadOnly 元数据 → ToolAnnotations.ReadOnlyHint。
func TestReadOnlyAnnotation(t *testing.T) {
	resetRegistry(t)
	Register(&stubTool{name: "ro_tool", schema: `{"type":"object"}`, readOnly: true})
	Register(&stubTool{name: "rw_tool", schema: `{"type":"object"}`, readOnly: false})
	s := NewServer(Options{Name: "mulix", Version: "test"})
	cs := connectInMemory(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range r.Tools {
		byName[tl.Name] = tl
	}
	if byName["ro_tool"].Annotations == nil || !byName["ro_tool"].Annotations.ReadOnlyHint {
		t.Fatalf("ro_tool annotations = %+v, want ReadOnlyHint=true", byName["ro_tool"].Annotations)
	}
	if byName["rw_tool"].Annotations != nil && byName["rw_tool"].Annotations.ReadOnlyHint {
		t.Fatalf("rw_tool annotations = %+v, want ReadOnlyHint=false", byName["rw_tool"].Annotations)
	}
}

// TestLookup pins（Task 4）：按名取回已注册工具（只读快照——Execute 可直调，
// cli e2e 经它驱动 mulix_spawn 的工具面）；未注册名 → (nil, false)。
func TestLookup(t *testing.T) {
	resetRegistry(t)
	if _, ok := Lookup("ghost_tool"); ok {
		t.Fatal("unknown name must miss")
	}
	st := &stubTool{name: "named_tool", schema: `{"type":"object"}`}
	Register(st)
	got, ok := Lookup("named_tool")
	if !ok || got == nil {
		t.Fatalf("Lookup = (%v, %v), want tool", got, ok)
	}
	if got.Name() != "named_tool" {
		t.Fatalf("Lookup name = %q", got.Name())
	}
	// 快照可执行（与注册表内同源）
	out, err := got.Execute(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil || !strings.Contains(out, `"x":1`) {
		t.Fatalf("Lookup execute = (%q, %v)", out, err)
	}
}

// TestMCPServersSelfCommand pins：cache.Registry 对齐——SelfCommand 配置时
// 返回 mulix stdio 自 spawn 条目（Boot mcp-config 生成对接）；未配置 → nil。
// 编译期钉接口实现。
func TestMCPServersSelfCommand(t *testing.T) {
	var _ cache.Registry = (*Server)(nil) // 接口对齐编译期钉
	resetRegistry(t)
	Register(&stubTool{name: "t", schema: `{"type":"object"}`})
	s := NewServer(Options{Name: "mulix", Version: "test"})
	if got := s.MCPServers(); got != nil {
		t.Fatalf("no self command: MCPServers = %+v, want nil", got)
	}
	s2 := NewServer(Options{Name: "mulix", Version: "test",
		SelfCommand: []string{"C:\\bin\\mulix.exe", "mcp", "serve"}})
	got := s2.MCPServers()
	if len(got) != 1 || got[0].Name != "mulix" ||
		strings.Join(got[0].Command, " ") != `C:\bin\mulix.exe mcp serve` {
		t.Fatalf("MCPServers = %+v", got)
	}
	// 顺序稳定：两次调用等价
	if got2 := s2.MCPServers(); len(got2) != 1 || got2[0].Name != got[0].Name {
		t.Fatalf("MCPServers unstable: %+v vs %+v", got, got2)
	}
}

// TestSnapshotFreezesAtCallTime 钉 011 FR-103:Snapshot 返回调用时刻副本——
// 调用后的注册不影响副本(冻结语义)。
func TestSnapshotFreezesAtCallTime(t *testing.T) {
	resetRegistry(t)
	Register(&stubTool{name: "snap-a", desc: "a", schema: `{"type":"object"}`})
	snap1 := Snapshot()
	Register(&stubTool{name: "snap-b", desc: "b", schema: `{"type":"object"}`})
	if len(snap1) != 1 {
		t.Fatalf("snapshot 1 = %d tools, want 1 (副本不可变)", len(snap1))
	}
	snap2 := Snapshot()
	if len(snap2) != 2 {
		t.Fatalf("snapshot 2 = %d tools, want 2 (当前态)", len(snap2))
	}
}
