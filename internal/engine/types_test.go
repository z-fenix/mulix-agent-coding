package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// hostShapeProbe 是接口形状的编译期断言替身（Task 18 FakeHost 才是真实现；
// 这里只钉方法集——漏方法/签名漂移在编译期失败）。
type hostShapeProbe struct{}

func (hostShapeProbe) Start(ctx context.Context, cfg BootConfig) error { return nil }
func (hostShapeProbe) Send(ctx context.Context, msg UserMessage) error { return nil }
func (hostShapeProbe) Events() <-chan Event                            { return nil }
func (hostShapeProbe) RespondControl(ctx context.Context, id string, p ControlResponse) error {
	return nil
}
func (hostShapeProbe) Interrupt(ctx context.Context) error { return nil }
func (hostShapeProbe) Close(ctx context.Context) error     { return nil }
func (hostShapeProbe) Info() EngineInfo                    { return EngineInfo{} }

var _ Host = hostShapeProbe{}

// TestHostInterfaceShape pins：Host 七方法编译期成立（design §7.1 签名）。
func TestHostInterfaceShape(t *testing.T) {
	var h Host = hostShapeProbe{}
	if h == nil {
		t.Fatal("unreachable")
	}
}

// TestEventKindsDistinct pins：事件类常量唯一（decoder 归一化的语义键）。
func TestEventKindsDistinct(t *testing.T) {
	seen := map[EventKind]bool{}
	for _, k := range []EventKind{
		KindInit, KindHookStarted, KindHookResponse, KindPermissionDenied,
		KindAssistant, KindToolResult, KindResult, KindControlRequest,
		KindNoise, KindUnknown,
	} {
		if seen[k] {
			t.Fatalf("duplicate event kind %q", k)
		}
		seen[k] = true
	}
}

// TestInitInfoJSONRoundTrip pins：system/init 载荷字段名（含 camelCase 的
// permissionMode 与 snake 的 claude_code_version）+ 未知字段容忍。
func TestInitInfoJSONRoundTrip(t *testing.T) {
	in := `{
      "type":"system","subtype":"init",
      "session_id":"f8dcb93b-1a2b-3c4d-5e6f-7a8b9c0d1e2f",
      "cwd":"D:\\ws\\demo",
      "tools":["Task","Bash","Write"],
      "mcp_servers":[{"name":"mulix","status":"connected","source":"user"}],
      "model":"claude-opus-5[1M]",
      "permissionMode":"default",
      "apiKeySource":"none",
      "claude_code_version":"2.1.291",
      "uuid":"u1",
      "some_future_field":{"nested":true}
    }`
	var init InitInfo
	if err := json.Unmarshal([]byte(in), &init); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	// 信封级 session_id/uuid 的解码断言在 decoder（Task 16）；此处钉载荷。
	if init.Model != "claude-opus-5[1M]" || init.PermissionMode != "default" {
		t.Fatalf("init fields wrong: %+v", init)
	}
	if init.CLIVersion != "2.1.291" {
		t.Fatalf("cli version = %q", init.CLIVersion)
	}
	if len(init.Tools) != 3 || init.Tools[1] != "Bash" {
		t.Fatalf("tools wrong: %v", init.Tools)
	}
	if len(init.MCPServers) != 1 || init.MCPServers[0].Name != "mulix" ||
		init.MCPServers[0].Status != "connected" || init.MCPServers[0].Source != "user" {
		t.Fatalf("mcp_servers wrong: %+v", init.MCPServers)
	}
	// 往返：marshal → unmarshal → 相等
	raw, err := json.Marshal(init)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back InitInfo
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !reflect.DeepEqual(init, back) {
		t.Fatalf("round-trip mismatch:\n%+v\n%+v", init, back)
	}
}

// TestToolResultIsErrorDefault pins §2.4：is_error 是条件字段——
// 成功时缺省（=false），被拒/出错时 true；decoder 不得假设必有。
func TestToolResultIsErrorDefault(t *testing.T) {
	okLine := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"done"}]}`
	var okMsg ToolResultMsg
	if err := json.Unmarshal([]byte(okLine), &okMsg); err != nil {
		t.Fatalf("unmarshal ok: %v", err)
	}
	if len(okMsg.Blocks) != 1 || okMsg.Blocks[0].ToolUseID != "call_1" || okMsg.Blocks[0].IsError {
		t.Fatalf("absent is_error must decode false: %+v", okMsg.Blocks)
	}
	errLine := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_2","content":"denied","is_error":true}]}`
	var errMsg ToolResultMsg
	if err := json.Unmarshal([]byte(errLine), &errMsg); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if !errMsg.Blocks[0].IsError {
		t.Fatal("explicit is_error:true must decode true")
	}
}

// TestResultUsageRoundTrip pins §2.5：usage（本回合）四类 token + thinking 细目 +
// permission_denials + total_cost_usd/result_index（指针区分缺失与 0）。
func TestResultUsageRoundTrip(t *testing.T) {
	in := `{
      "type":"result","subtype":"success","is_error":false,"num_turns":1,
      "result":"最终文本",
      "session_id":"s1","uuid":"u9",
      "duration_ms":6575,
      "usage":{"input_tokens":22060,"output_tokens":43,
               "cache_creation_input_tokens":0,"cache_read_input_tokens":0,
               "output_tokens_details":{"thinking_tokens":7}},
      "modelUsage":{"claude-opus-5[1M]":{"inputTokens":22060,"costUSD":0.109}},
      "total_cost_usd":0.111375,
      "permission_denials":[{"tool_name":"Write","tool_use_id":"call_w","tool_input":{"file_path":"x"}}],
      "result_index":0
    }`
	var r ResultInfo
	if err := json.Unmarshal([]byte(in), &r); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if r.SubType != "success" || r.IsError || r.NumTurns != 1 || r.ResultText != "最终文本" {
		t.Fatalf("result fields wrong: %+v", r)
	}
	if r.Usage.InputTokens != 22060 || r.Usage.OutputTokens != 43 ||
		r.Usage.CacheWriteTokens != 0 || r.Usage.CacheReadTokens != 0 {
		t.Fatalf("usage wrong: %+v", r.Usage)
	}
	if r.Usage.Details.ThinkingTokens != 7 {
		t.Fatalf("thinking tokens = %d, want 7", r.Usage.Details.ThinkingTokens)
	}
	if r.TotalCostUSD == nil || *r.TotalCostUSD != 0.111375 {
		t.Fatalf("total_cost_usd = %v", r.TotalCostUSD)
	}
	if r.ResultIndex == nil || *r.ResultIndex != 0 {
		t.Fatalf("result_index = %v", r.ResultIndex)
	}
	if len(r.Denials) != 1 || r.Denials[0].ToolName != "Write" || r.Denials[0].ToolUseID != "call_w" {
		t.Fatalf("denials wrong: %+v", r.Denials)
	}
	// 缺省可选字段 → nil（Task 13 口径：缺失 ≠ 0）
	var bare ResultInfo
	if err := json.Unmarshal([]byte(`{"subtype":"success","is_error":false}`), &bare); err != nil {
		t.Fatalf("unmarshal bare: %v", err)
	}
	if bare.TotalCostUSD != nil || bare.ResultIndex != nil || bare.Denials != nil {
		t.Fatalf("absent optionals must be nil: %+v", bare)
	}
	// 往返
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ResultInfo
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !reflect.DeepEqual(r, back) {
		t.Fatalf("round-trip mismatch:\n%+v\n%+v", r, back)
	}
}

// TestAssistantBlocksRoundTrip pins §2.3：content 分片 text/thinking/tool_use
// （tool_use.input 不透明透传）。
func TestAssistantBlocksRoundTrip(t *testing.T) {
	in := `{"id":"msg_1","role":"assistant","model":"gw-claude","stop_reason":null,
      "content":[
        {"type":"text","text":"你好"},
        {"type":"thinking","thinking":"…","signature":""},
        {"type":"tool_use","id":"call_9","name":"Bash","input":{"command":"go test"}}
      ]}`
	var m AssistantMsg
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatalf("unmarshal assistant: %v", err)
	}
	if m.ID != "msg_1" || m.Model != "gw-claude" || len(m.Blocks) != 3 {
		t.Fatalf("assistant fields wrong: %+v", m)
	}
	if m.Blocks[0].Type != "text" || m.Blocks[0].Text != "你好" {
		t.Fatalf("text block wrong: %+v", m.Blocks[0])
	}
	if m.Blocks[1].Type != "thinking" || m.Blocks[1].Thinking == "" {
		t.Fatalf("thinking block wrong: %+v", m.Blocks[1])
	}
	if m.Blocks[2].Type != "tool_use" || m.Blocks[2].ID != "call_9" ||
		m.Blocks[2].Name != "Bash" || string(m.Blocks[2].Input) != `{"command":"go test"}` {
		t.Fatalf("tool_use block wrong: %+v", m.Blocks[2])
	}
	if m.StopReason != nil {
		t.Fatalf("null stop_reason must decode nil, got %v", *m.StopReason)
	}
}

// TestControlEnvelope pins §5：控制信封 request_id + 原始 request 透传；
// 应答信封字段名（subtype/request_id/response）。
func TestControlEnvelope(t *testing.T) {
	in := `{"type":"control_request","request_id":"r-1",
      "request":{"subtype":"mcp_message","server_name":"stdio",
                 "message":{"method":"initialize","params":{"jsonrpc":"2.0","id":0}}}}`
	var cr ControlRequest
	if err := json.Unmarshal([]byte(in), &cr); err != nil {
		t.Fatalf("unmarshal control_request: %v", err)
	}
	if cr.RequestID != "r-1" || cr.Request.SubType != "mcp_message" {
		t.Fatalf("control request fields wrong: %+v", cr)
	}
	if cr.Request.ServerName != "stdio" || len(cr.Request.Message) == 0 {
		t.Fatalf("request body wrong: %+v", cr.Request)
	}
	// 应答信封 marshal 形状
	resp := ControlResponse{SubType: "success", RequestID: "r-1",
		Response: json.RawMessage(`{"jsonrpc":"2.0","id":0,"result":{}}`)}
	raw, err := json.Marshal(struct {
		Type     string          `json:"type"`
		Response ControlResponse `json:"response"`
	}{"control_response", resp})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var back struct {
		Type     string `json:"type"`
		Response struct {
			SubType   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("re-unmarshal response: %v", err)
	}
	if back.Type != "control_response" || back.Response.SubType != "success" ||
		back.Response.RequestID != "r-1" || len(back.Response.Response) == 0 {
		t.Fatalf("response envelope wrong: %s", raw)
	}
}

// TestPermissionDeniedAndHook pins §2.2：permission_denied（fail-closed 路径①）
// 与 hook_started/hook_response 载荷字段。
func TestPermissionDeniedAndHook(t *testing.T) {
	denied := `{"tool_name":"Write","tool_use_id":"call_x","message":"未授权"}`
	var d PermissionDeniedInfo
	if err := json.Unmarshal([]byte(denied), &d); err != nil {
		t.Fatalf("unmarshal denied: %v", err)
	}
	if d.ToolName != "Write" || d.ToolUseID != "call_x" || d.Message != "未授权" {
		t.Fatalf("denied wrong: %+v", d)
	}
	hook := `{"hook_id":"h1","hook_name":"audit","hook_event":"PreToolUse",
      "output":"{}","stdout":"[]","stderr":"","exit_code":0,"outcome":"success"}`
	var h HookInfo
	if err := json.Unmarshal([]byte(hook), &h); err != nil {
		t.Fatalf("unmarshal hook: %v", err)
	}
	if h.HookID != "h1" || h.HookEvent != "PreToolUse" || h.ExitCode == nil || *h.ExitCode != 0 {
		t.Fatalf("hook wrong: %+v", h)
	}
}
