package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decodeFixture 逐行解码 fixture 文件，返回事件切片（跳过 decoder 声明的
// 非产出行）与按类计数。
func decodeFixture(t *testing.T, rel string) ([]*Event, map[EventKind]int) {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	var events []*Event
	counts := map[EventKind]int{}
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("%s line %d: empty line in committed fixture", rel, i+1)
		}
		ev, err := DecodeLine([]byte(line))
		if err != nil {
			t.Fatalf("%s line %d: %v", rel, i+1, err)
		}
		if ev == nil {
			continue // decoder 声明的跳过类（未知 type 等）
		}
		counts[ev.Kind]++
		events = append(events, ev)
	}
	return events, counts
}

// TestDecodeHappyTurnFixture pins：M1 fixture 全量解码无错 + §7 自检表关键字段。
func TestDecodeHappyTurnFixture(t *testing.T) {
	events, counts := decodeFixture(t, filepath.Join("fixtures", "engine", "happy-turn.jsonl"))
	// 行计数：87 = 1 init + 2 hook + 79 noise(thinking_tokens) + 3 assistant + 1 tool_result + 1 result
	if counts[KindInit] != 1 || counts[KindHookStarted] != 1 || counts[KindHookResponse] != 1 {
		t.Fatalf("init/hook counts wrong: %+v", counts)
	}
	if counts[KindNoise] != 79 || counts[KindAssistant] != 3 || counts[KindToolResult] != 1 || counts[KindResult] != 1 {
		t.Fatalf("kind counts wrong: %+v", counts)
	}
	if got := counts[KindInit] + counts[KindHookStarted] + counts[KindHookResponse] +
		counts[KindNoise] + counts[KindAssistant] + counts[KindToolResult] + counts[KindResult]; got != 87 {
		t.Fatalf("total decoded = %d, want 87", got)
	}

	// 信封级：全部事件都带 session_id/uuid（Task 15 交接钉）
	for i, ev := range events {
		if ev.SessionID == "" || ev.UUID == "" {
			t.Fatalf("event %d (%s) missing envelope session/uuid", i, ev.Kind)
		}
		if ev.SessionID != "9ce73bff-164b-4488-a4e6-7d4e1fa4efc6" {
			t.Fatalf("event %d unexpected session_id %s", i, ev.SessionID)
		}
	}

	// init 关键字段（§7 row 1）
	init := events[2]
	if init.Kind != KindInit || init.Init == nil {
		t.Fatalf("third event must be init: %+v", init)
	}
	if init.Init.Model != "claude-opus-5[1M]" || init.Init.PermissionMode != "default" ||
		init.Init.CLIVersion != "2.1.291" || len(init.Init.Tools) != 28 || len(init.Init.MCPServers) != 4 {
		t.Fatalf("init fields wrong: %+v", init.Init)
	}

	// hook_response（§7 row 3）
	var hookResp *Event
	for _, ev := range events {
		if ev.Kind == KindHookResponse {
			hookResp = ev
		}
	}
	if hookResp == nil || hookResp.Hook == nil || hookResp.Hook.HookEvent != "SessionStart" ||
		hookResp.Hook.ExitCode == nil || *hookResp.Hook.ExitCode != 0 || hookResp.Hook.Outcome != "success" {
		t.Fatalf("hook_response wrong: %+v", hookResp)
	}

	// assistant 分片：thinking → tool_use → text（同逻辑消息流）
	var toolUse *ContentBlock
	for _, ev := range events {
		if ev.Kind != KindAssistant || ev.Assistant == nil {
			continue
		}
		for _, b := range ev.Assistant.Blocks {
			if b.Type == "tool_use" {
				toolUse = &b
			}
		}
	}
	if toolUse == nil || toolUse.ID != "call_87068bb5ade040bcab906bb4" || toolUse.Name != "Write" {
		t.Fatalf("tool_use block wrong: %+v", toolUse)
	}
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(toolUse.Input, &input); err != nil || input.FilePath == "" {
		t.Fatalf("tool_use.input not preserved: %v (%s)", err, toolUse.Input)
	}

	// tool_result：is_error 缺省=false（§7 row 9）
	var tr *Event
	for _, ev := range events {
		if ev.Kind == KindToolResult {
			tr = ev
		}
	}
	if tr == nil || tr.ToolResult == nil || len(tr.ToolResult.Blocks) != 1 {
		t.Fatalf("tool_result event wrong: %+v", tr)
	}
	block := tr.ToolResult.Blocks[0]
	if block.ToolUseID != toolUse.ID || block.IsError {
		t.Fatalf("tool_result block wrong: %+v", block)
	}
	if !strings.Contains(block.Content, "File created successfully") {
		t.Fatalf("tool_result content wrong: %q", block.Content)
	}
	if tr.ToolResult.RawToolUseResult == nil {
		t.Fatal("tool_use_result must be preserved (对象形态)")
	}

	// result（§7 row 10）
	var res *Event
	for _, ev := range events {
		if ev.Kind == KindResult {
			res = ev
		}
	}
	if res == nil || res.Result == nil {
		t.Fatal("result event missing")
	}
	r := res.Result
	if r.SubType != "success" || r.IsError || r.NumTurns != 2 {
		t.Fatalf("result fields wrong: %+v", r)
	}
	if r.Usage.InputTokens != 24805 || r.Usage.OutputTokens != 150 ||
		r.Usage.CacheWriteTokens != 0 || r.Usage.CacheReadTokens != 24512 {
		t.Fatalf("result usage wrong: %+v", r.Usage)
	}
	if r.TotalCostUSD == nil || math.Abs(*r.TotalCostUSD-0.140031) > 1e-9 {
		t.Fatalf("total_cost_usd = %v", r.TotalCostUSD)
	}
	if r.ResultIndex == nil || *r.ResultIndex != 0 {
		t.Fatalf("result_index = %v", r.ResultIndex)
	}
	if r.Denials == nil || len(r.Denials) != 0 {
		t.Fatalf("permission_denials empty array must decode to non-nil len-0: %+v", r.Denials)
	}
	if !strings.Contains(r.ResultText, "happy.txt") {
		t.Fatalf("result text wrong: %q", r.ResultText)
	}
}

// TestDecodeUsageFixture pins：双回合同进程（usage 口径 + init 重发 + result_index）。
func TestDecodeUsageFixture(t *testing.T) {
	events, counts := decodeFixture(t, filepath.Join("fixtures", "engine", "usage.jsonl"))
	// 行计数：65 = 2 init + 2 hook + 55 noise(54 thinking_tokens + 1 informational) + 4 assistant + 2 result
	if counts[KindInit] != 2 || counts[KindHookStarted] != 1 || counts[KindHookResponse] != 1 {
		t.Fatalf("init/hook counts wrong: %+v", counts)
	}
	if counts[KindNoise] != 55 || counts[KindAssistant] != 4 || counts[KindResult] != 2 {
		t.Fatalf("kind counts wrong: %+v", counts)
	}
	if got := counts[KindInit] + counts[KindHookStarted] + counts[KindHookResponse] +
		counts[KindNoise] + counts[KindAssistant] + counts[KindResult]; got != 65 {
		t.Fatalf("total decoded = %d, want 65", got)
	}

	// 全程 session_id 不变（fixture 事实：同进程多回合）+ uuid 非空
	for i, ev := range events {
		if ev.SessionID != "f8dcb93b-dc7a-4263-b62d-770caf42308e" {
			t.Fatalf("event %d session drift: %s", i, ev.SessionID)
		}
		if ev.UUID == "" {
			t.Fatalf("event %d missing uuid", i)
		}
	}

	// init 按回合重发（2 条）——decoder 如实产出；幂等消费归 control（Task 22）
	inits := 0
	for _, ev := range events {
		if ev.Kind == KindInit {
			inits++
			if ev.Init == nil || ev.Init.CLIVersion != "2.1.291" || len(ev.Init.Tools) != 25 {
				t.Fatalf("init fields wrong: %+v", ev.Init)
			}
		}
	}
	if inits != 2 {
		t.Fatalf("init count = %d, want 2 (per-turn re-emit)", inits)
	}

	// assistant 文本 "391" / "130.33"
	var texts []string
	for _, ev := range events {
		if ev.Kind != KindAssistant || ev.Assistant == nil {
			continue
		}
		for _, b := range ev.Assistant.Blocks {
			if b.Type == "text" {
				texts = append(texts, b.Text)
			}
		}
	}
	if len(texts) != 2 || !strings.Contains(texts[0], "391") || !strings.Contains(texts[1], "130.33") {
		t.Fatalf("assistant texts wrong: %q", texts)
	}

	// 两个 result：本回合 usage + 会话累计 cost + result_index 0→1（§2.5）
	var results []*ResultInfo
	for _, ev := range events {
		if ev.Kind == KindResult {
			results = append(results, ev.Result)
		}
	}
	if len(results) != 2 {
		t.Fatal("missing results")
	}
	r1, r2 := results[0], results[1]
	if r1.Usage.InputTokens != 22060 || r1.Usage.OutputTokens != 43 ||
		r1.Usage.CacheWriteTokens != 0 || r1.Usage.CacheReadTokens != 0 {
		t.Fatalf("result#1 usage wrong: %+v", r1.Usage)
	}
	if r1.ResultIndex == nil || *r1.ResultIndex != 0 || r1.NumTurns != 1 {
		t.Fatalf("result#1 idx/num_turns wrong: %+v", r1)
	}
	if r1.TotalCostUSD == nil || math.Abs(*r1.TotalCostUSD-0.111375) > 1e-9 {
		t.Fatalf("result#1 cost = %v", r1.TotalCostUSD)
	}
	if r2.Usage.InputTokens != 22138 || r2.Usage.OutputTokens != 20 {
		t.Fatalf("result#2 usage wrong: %+v", r2.Usage)
	}
	if r2.ResultIndex == nil || *r2.ResultIndex != 1 {
		t.Fatalf("result#2 index = %v, want 1", r2.ResultIndex)
	}
	if r2.TotalCostUSD == nil || math.Abs(*r2.TotalCostUSD-0.222565) > 1e-9 {
		t.Fatalf("result#2 cost = %v", r2.TotalCostUSD)
	}
	// 本回合 vs 会话累计口径的 fixture 实证：in 22060 + 22138 = 44198 = modelUsage 累计
	// （decoder 不做加法——口径纪律由 Meter/消费方负责，此处只钉解码值）
}

// TestDecodeUnknownTolerance pins：未知 type/subtype 跳过不报错（协议红线）；
// 已知事件的未知字段容忍。
func TestDecodeUnknownTolerance(t *testing.T) {
	// 未知顶层 type → (nil, nil)
	ev, err := DecodeLine([]byte(`{"type":"brand_new_thing","payload":{"x":1}}`))
	if err != nil || ev != nil {
		t.Fatalf("unknown type must skip without error, got (%v, %v)", ev, err)
	}
	// system 未知 subtype → (nil, nil)
	ev, err = DecodeLine([]byte(`{"type":"system","subtype":"from_the_future","session_id":"s","uuid":"u"}`))
	if err != nil || ev != nil {
		t.Fatalf("unknown subtype must skip without error, got (%v, %v)", ev, err)
	}
	// stream_event（P1 关闭留位）→ (nil, nil)
	ev, err = DecodeLine([]byte(`{"type":"stream_event","event":{"type":"message_start"}}`))
	if err != nil || ev != nil {
		t.Fatalf("stream_event must skip without error, got (%v, %v)", ev, err)
	}
	// 已知事件 + 未知字段 → 正常解码
	ev, err = DecodeLine([]byte(`{"type":"assistant","session_id":"s","uuid":"u","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"m","brand_new_field":[1,2]}}`))
	if err != nil || ev == nil || ev.Kind != KindAssistant || ev.Assistant == nil || len(ev.Assistant.Blocks) != 1 {
		t.Fatalf("unknown fields must be tolerated: (%+v, %v)", ev, err)
	}
	// 噪音类（informational）→ KindNoise 且无结构化载荷、Raw 保留
	ev, err = DecodeLine([]byte(`{"type":"system","subtype":"informational","content":"note","level":"warning","isMeta":false,"session_id":"s","uuid":"u"}`))
	if err != nil || ev == nil || ev.Kind != KindNoise || len(ev.Raw) == 0 {
		t.Fatalf("informational must decode as noise with raw: (%+v, %v)", ev, err)
	}
	// 噪音类（status，§7 row 7）→ KindNoise（必须容忍）
	ev, err = DecodeLine([]byte(`{"type":"system","subtype":"status","status":"requesting","session_id":"s","uuid":"u"}`))
	if err != nil || ev == nil || ev.Kind != KindNoise {
		t.Fatalf("status must decode as noise: (%+v, %v)", ev, err)
	}
	// permission_denials: null → nil（与 [] 的 non-nil len-0 区分）
	var nullResult ResultInfo
	if err := json.Unmarshal([]byte(`{"subtype":"success","is_error":false,"permission_denials":null}`), &nullResult); err != nil {
		t.Fatalf("null denials unmarshal: %v", err)
	}
	if nullResult.Denials != nil {
		t.Fatalf("null denials must decode nil, got %+v", nullResult.Denials)
	}
	// user 事件缺 tool_use_result → RawToolUseResult nil（容忍缺失）
	var bareTool ToolResultMsg
	if err := json.Unmarshal([]byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"x"}]}`), &bareTool); err != nil {
		t.Fatalf("bare tool_result unmarshal: %v", err)
	}
	if bareTool.RawToolUseResult != nil {
		t.Fatalf("absent tool_use_result must be nil, got %s", bareTool.RawToolUseResult)
	}
}

// TestDecodeMalformed pins：非法 JSON → 错误（fail-closed；与「未知 type 跳过」区分）。
func TestDecodeMalformed(t *testing.T) {
	if _, err := DecodeLine([]byte(`{"type":"assistant",broken`)); err == nil {
		t.Fatal("malformed JSON must error")
	}
	if _, err := DecodeLine([]byte(``)); err == nil {
		t.Fatal("empty line must error")
	}
	if _, err := DecodeLine([]byte(`   `)); err == nil {
		t.Fatal("whitespace-only line must error")
	}
	// JSON 合法但缺 type → 错误（协议要求顶层 type）
	if _, err := DecodeLine([]byte(`{"session_id":"s"}`)); err == nil {
		t.Fatal("missing type must error")
	}
}

// TestDecodeControlRequestEnvelope pins：控制信封解码（request_id + 嵌套 request）。
func TestDecodeControlRequestEnvelope(t *testing.T) {
	line := `{"type":"control_request","request_id":"r-7",
      "request":{"subtype":"mcp_message","server_name":"stdio",
                 "message":{"method":"initialize","params":{"jsonrpc":"2.0","id":0}}}}`
	ev, err := DecodeLine([]byte(line))
	if err != nil || ev == nil || ev.Kind != KindControlRequest || ev.Control == nil {
		t.Fatalf("control_request decode: (%+v, %v)", ev, err)
	}
	if ev.Control.RequestID != "r-7" || ev.Control.Request.SubType != "mcp_message" ||
		ev.Control.Request.ServerName != "stdio" || len(ev.Control.Request.Message) == 0 {
		t.Fatalf("control request fields wrong: %+v", ev.Control)
	}
}

// TestDecodePermissionDenied pins：system/permission_denied（fail-closed ①）解码。
func TestDecodePermissionDenied(t *testing.T) {
	line := `{"type":"system","subtype":"permission_denied","tool_name":"Bash",
      "tool_use_id":"call_d1","message":"需要审批","session_id":"s","uuid":"u"}`
	ev, err := DecodeLine([]byte(line))
	if err != nil || ev == nil || ev.Kind != KindPermissionDenied || ev.Denied == nil {
		t.Fatalf("permission_denied decode: (%+v, %v)", ev, err)
	}
	if ev.Denied.ToolName != "Bash" || ev.Denied.ToolUseID != "call_d1" || ev.Denied.Message != "需要审批" {
		t.Fatalf("denied fields wrong: %+v", ev.Denied)
	}
}
