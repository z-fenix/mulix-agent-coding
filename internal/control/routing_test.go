package control

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
)

// ---- can_use_tool（权限回路） ----

// canUseTool 构造引擎发来的 can_use_tool 控制请求信封（Task 3 Ruling：
// MCP tools/call{tool_name,input,tool_use_id}，经 control_request/mcp_message）。
func canUseTool(requestID, toolUseID, tool string, input string) engine.Event {
	args := map[string]any{"tool_name": tool, "tool_use_id": toolUseID}
	if input != "" {
		args["input"] = json.RawMessage(input)
	}
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID + "-mcp",
		"method":  "tools/call",
		"params":  map[string]any{"name": "permission", "arguments": args},
	})
	return engine.Event{Kind: engine.KindControlRequest, SessionID: "s", Control: &engine.ControlRequest{
		RequestID: requestID,
		Request: engine.ControlRequestBody{
			SubType:    "mcp_message",
			ServerName: "mulix",
			Message:    msg,
		},
	}}
}

// behaviorOf 从记录的应答里取 behavior JSON（MCP content[0].text）。
func behaviorOf(t *testing.T, resp engine.ControlResponse) map[string]any {
	t.Helper()
	var envelope struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Response, &envelope); err != nil {
		t.Fatalf("response not MCP envelope: %v (%s)", err, resp.Response)
	}
	if len(envelope.Result.Content) == 0 || envelope.Result.Content[0].Text == "" {
		t.Fatalf("empty MCP content: %s", resp.Response)
	}
	var behavior map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &behavior); err != nil {
		t.Fatalf("content text not behavior JSON: %v", err)
	}
	return behavior
}

// resolvedOf 从镜像找 tool_use_id 的 approval_resolved 事件。
func resolvedOf(t *testing.T, c *Controller, toolUseID string) event.ApprovalResolvedEvent {
	t.Helper()
	mirror, _ := c.Mirror().ReadAllEvents()
	for _, ev := range mirror {
		if e, ok := ev.(event.ApprovalResolvedEvent); ok && e.ToolUseID == toolUseID {
			return e
		}
	}
	t.Fatalf("no approval_resolved for %s", toolUseID)
	return event.ApprovalResolvedEvent{}
}

// requestOf 从镜像找 tool_use_id 的 approval_request 事件（权限回路审计起点）。
func requestOf(t *testing.T, c *Controller, toolUseID string) event.ApprovalRequestEvent {
	t.Helper()
	mirror, _ := c.Mirror().ReadAllEvents()
	for _, ev := range mirror {
		if e, ok := ev.(event.ApprovalRequestEvent); ok && e.ToolUseID == toolUseID {
			return e
		}
	}
	t.Fatalf("no approval_request for %s", toolUseID)
	return event.ApprovalRequestEvent{}
}

// rawControlRequest 构造任意控制信封（fail-closed 路由表测试用）。
func rawControlRequest(requestID, subType, server, message string) engine.Event {
	return engine.Event{Kind: engine.KindControlRequest, SessionID: "s", Control: &engine.ControlRequest{
		RequestID: requestID,
		Request: engine.ControlRequestBody{
			SubType: subType, ServerName: server, Message: json.RawMessage(message),
		},
	}}
}

// TestControlRequestFailClosedRouting pins：不可路由的控制信封一律得到
// subtype=error 应答且按原 request_id 回给引擎（fail-closed 红线：引擎不得
// 悬挂——评审 Important-1）。
func TestControlRequestFailClosedRouting(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	cases := []struct{ id, name, subType, server, message string }{
		{"req-e1", "unsupported-subtype", "can_use_tool", "mulix", `{}`},
		{"req-e2", "foreign-server", "mcp_message", "other", `{"method":"tools/call"}`},
		{"req-e3", "malformed-message", "mcp_message", "mulix", `not-json`},
		{"req-e4", "unsupported-method", "mcp_message", "mulix",
			`{"jsonrpc":"2.0","method":"resources/list"}`},
		{"req-e5", "missing-args", "mcp_message", "mulix",
			`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"permission","arguments":{}}}`},
	}
	for _, tc := range cases {
		if err := fake.InjectControlRequest(*rawControlRequest(tc.id, tc.subType, tc.server, tc.message).Control); err != nil {
			t.Fatalf("%s: inject: %v", tc.name, err)
		}
	}
	waitResponses(t, fake, len(cases))
	seen := map[string]engine.FakeControlResponse{}
	for _, r := range fake.Responses() {
		seen[r.ID] = r
	}
	for _, tc := range cases {
		r, ok := seen[tc.id]
		if !ok {
			t.Fatalf("%s: no response (engine would hang)", tc.name)
		}
		if r.Payload.SubType != "error" {
			t.Fatalf("%s: subtype = %q, want error (fail-closed)", tc.name, r.Payload.SubType)
		}
		var errObj map[string]any
		if err := json.Unmarshal(r.Payload.Response, &errObj); err != nil {
			t.Fatalf("%s: error payload = %s (%v)", tc.name, r.Payload.Response, err)
		}
		if msg, _ := errObj["error"].(string); msg == "" {
			t.Fatalf("%s: error message missing/empty in %s", tc.name, r.Payload.Response)
		}
	}
}

// TestCancelDeniesPendingApprovals pins：Cancel 中断时待审请求一并
// fail-closed 拒绝（解阻塞泵）——引擎收 deny(message 含 cancelled)，镜像
// resolved=deny/user/cancelled:interrupt，host.Interrupt 恰一次。
func TestCancelDeniesPendingApprovals(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerAttended(t, fake, sink, 2*time.Second)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := fake.InjectControlRequest(*canUseTool("req-c1", "tu-c1", "Bash", `{"command":"npm publish"}`).Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitPending(t, c, "tu-c1")
	if err := c.Cancel(context.Background()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitResponses(t, fake, 1)
	behavior := behaviorOf(t, fake.Responses()[0].Payload)
	if behavior["behavior"] != "deny" {
		t.Fatalf("behavior = %v, want deny", behavior["behavior"])
	}
	if msg, _ := behavior["message"].(string); !strings.Contains(msg, "cancelled") {
		t.Fatalf("deny message = %v, want cancelled reason", behavior["message"])
	}
	resolved := resolvedOf(t, c, "tu-c1")
	if resolved.Decision != "deny" || resolved.Source != "user" ||
		resolved.ResolvedBy != "cancelled:interrupt" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if got := fake.Interrupts(); got != 1 {
		t.Fatalf("interrupts = %d, want 1", got)
	}
}

// TestCanUseToolAllowViaPolicy pins：policy allow 的 can_use_tool → 应答
// behavior=allow + updatedInput 回显；镜像落 approval_request/approval_resolved
// （source=policy）；应答信封 id = 控制信封 request_id（逐信封应答）。
func TestCanUseToolAllowViaPolicy(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerWithPolicyRules(t, fake, sink,
		policyRuleIn{raw: "Bash(go test:*)", effect: guard.DecisionAllow})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	cr := canUseTool("req-1", "tu-1", "Bash", `{"command":"go test ./..."}`)
	if err := fake.InjectControlRequest(*cr.Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for len(fake.Responses()) == 0 {
		select {
		case <-deadline:
			t.Fatal("no response to can_use_tool")
		case <-time.After(5 * time.Millisecond):
		}
	}
	resps := fake.Responses()
	if len(resps) != 1 {
		t.Fatalf("responses = %d, want 1", len(resps))
	}
	if resps[0].ID != "req-1" {
		t.Fatalf("response id = %q, want req-1 (逐信封应答)", resps[0].ID)
	}
	if resps[0].Payload.SubType != "success" {
		t.Fatalf("payload subtype = %q", resps[0].Payload.SubType)
	}
	behavior := behaviorOf(t, resps[0].Payload)
	if behavior["behavior"] != "allow" {
		t.Fatalf("behavior = %v, want allow", behavior["behavior"])
	}
	upd, _ := behavior["updatedInput"].(map[string]any)
	if upd["command"] != "go test ./..." {
		t.Fatalf("updatedInput = %v", behavior["updatedInput"])
	}
	// MCP id 回显（评审 fix-round Minor-1）：应答信封 echo 请求的 mcp id。
	var envelope map[string]any
	if err := json.Unmarshal(resps[0].Payload.Response, &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if envelope["id"] != "req-1-mcp" {
		t.Fatalf("mcp id echo = %v, want req-1-mcp", envelope["id"])
	}
	resolved := resolvedOf(t, c, "tu-1")
	if resolved.Decision != "allow" || resolved.Source != "policy" {
		t.Fatalf("resolved = %+v", resolved)
	}
	requestOf(t, c, "tu-1") // 权限回路审计起点必须在镜像
}

// TestCanUseToolDenyViaPolicy pins：policy deny → behavior=deny + message；
// 镜像 approval_resolved{deny, policy}（Task 3 Ruling：deny 形状文档来源）。
func TestCanUseToolDenyViaPolicy(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerWithPolicyRules(t, fake, sink,
		policyRuleIn{raw: "Bash(rm:*)", effect: guard.DecisionDeny})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	cr := canUseTool("req-2", "tu-2", "Bash", `{"command":"rm -rf /"}`)
	if err := fake.InjectControlRequest(*cr.Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitResponses(t, fake, 1)
	behavior := behaviorOf(t, fake.Responses()[0].Payload)
	if behavior["behavior"] != "deny" {
		t.Fatalf("behavior = %v, want deny", behavior["behavior"])
	}
	if msg, _ := behavior["message"].(string); msg == "" {
		t.Fatalf("deny message missing: %v", behavior)
	}
	resolved := resolvedOf(t, c, "tu-2")
	if resolved.Decision != "deny" || resolved.Source != "policy" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

// TestCanUseToolUnattendedDeny pins：无人值守 + policy Ask → fail-closed 拒绝
// （FR-009；resolvedBy=unattended）。
func TestCanUseToolUnattendedDeny(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerWithPolicyRules(t, fake, sink) // default 预设，无审批者
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	cr := canUseTool("req-3", "tu-3", "Bash", `{"command":"npm publish"}`)
	if err := fake.InjectControlRequest(*cr.Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitResponses(t, fake, 1)
	behavior := behaviorOf(t, fake.Responses()[0].Payload)
	if behavior["behavior"] != "deny" {
		t.Fatalf("unattended must fail closed, got %v", behavior["behavior"])
	}
	resolved := resolvedOf(t, c, "tu-3")
	if resolved.Decision != "deny" || resolved.Source != "policy" ||
		!strings.Contains(resolved.ResolvedBy, "unattended") {
		t.Fatalf("resolved = %+v", resolved)
	}
}

// TestCanUseToolUserApproveDeny pins： attended 审批链路——can_use_tool(Ask)
// → 审批桥登记 pending → Approve 裁决 → 应答透传；deny 后重复 Approve 得
// ErrNoPendingApproval。
func TestCanUseToolUserApproveDeny(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerAttended(t, fake, sink, 2*time.Second)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	cr := canUseTool("req-4", "tu-4", "Bash", `{"command":"npm publish"}`)
	if err := fake.InjectControlRequest(*cr.Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitPending(t, c, "tu-4")
	if err := c.Approve("tu-4", false, "user:deny"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitResponses(t, fake, 1)
	behavior := behaviorOf(t, fake.Responses()[0].Payload)
	if behavior["behavior"] != "deny" {
		t.Fatalf("behavior = %v, want deny", behavior["behavior"])
	}
	resolved := resolvedOf(t, c, "tu-4")
	if resolved.Decision != "deny" || resolved.Source != "user" || resolved.ResolvedBy != "user:deny" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if err := c.Approve("tu-4", true, "user:again"); err == nil {
		t.Fatal("second approve must fail (no pending)")
	}
}

// TestCanUseToolTimeoutFailClosed pins：审批窗口耗尽 → deny(timeout) 应答 +
// 镜像 decision=timeout/source=timeout（Task 20 交接的集成面）。
func TestCanUseToolTimeoutFailClosed(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerAttended(t, fake, sink, 60*time.Millisecond)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	cr := canUseTool("req-5", "tu-5", "Bash", `{"command":"slow thing"}`)
	if err := fake.InjectControlRequest(*cr.Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitResponses(t, fake, 1)
	behavior := behaviorOf(t, fake.Responses()[0].Payload)
	if behavior["behavior"] != "deny" {
		t.Fatalf("timeout must fail closed, got %v", behavior["behavior"])
	}
	resolved := resolvedOf(t, c, "tu-5")
	if resolved.Decision != "timeout" || resolved.Source != "timeout" {
		t.Fatalf("resolved = %+v, want timeout/timeout", resolved)
	}
}

// TestCanUseToolSessionGrant pins：「本会话允许当前范围」——ApproveSession
// 授予后同范围第二次 can_use_tool 直接 allow(session)，不再问人。
func TestCanUseToolSessionGrant(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerAttended(t, fake, sink, 2*time.Second)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := fake.InjectControlRequest(*canUseTool("req-6", "tu-6", "Bash", `{"command":"go test ./..."}`).Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitPending(t, c, "tu-6")
	if err := c.ApproveSession("tu-6", "user:session-scope"); err != nil {
		t.Fatalf("approve session: %v", err)
	}
	waitResponses(t, fake, 1)
	if behaviorOf(t, fake.Responses()[0].Payload)["behavior"] != "allow" {
		t.Fatal("first call must be allowed")
	}
	// 同范围（go test 前缀）第二次：session 授权命中
	if err := fake.InjectControlRequest(*canUseTool("req-7", "tu-7", "Bash", `{"command":"go test -run X ./..."}`).Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitResponses(t, fake, 2)
	behavior := behaviorOf(t, fake.Responses()[1].Payload)
	if behavior["behavior"] != "allow" {
		t.Fatalf("session grant must allow, got %v", behavior["behavior"])
	}
	resolved := resolvedOf(t, c, "tu-7")
	if resolved.Decision != "allow" || resolved.Source != "session" {
		t.Fatalf("resolved = %+v, want allow/session", resolved)
	}
}

// TestControlRequestResendAnsweredByEachID pins：同一逻辑请求重发携带新
// request_id → 每个信封都得到应答（Task 20/协议 §5 交接；裁决只做一次）。
func TestControlRequestResendAnsweredByEachID(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{})
	sink := &recordingSink{}
	c := newTestControllerAttended(t, fake, sink, 2*time.Second)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := fake.InjectControlRequest(*canUseTool("req-A", "tu-8", "Bash", `{"command":"npm publish"}`).Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitPending(t, c, "tu-8")
	// 重发（新信封 id；同一 tool_use_id）
	if err := fake.InjectControlRequest(*canUseTool("req-B", "tu-8", "Bash", `{"command":"npm publish"}`).Control); err != nil {
		t.Fatalf("inject resend: %v", err)
	}
	if err := c.Approve("tu-8", true, "user:allow-once"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitResponses(t, fake, 2)
	ids := map[string]bool{}
	for _, r := range fake.Responses() {
		ids[r.ID] = true
		if behaviorOf(t, r.Payload)["behavior"] != "allow" {
			t.Fatalf("envelope %s: behavior = %v", r.ID, behaviorOf(t, r.Payload)["behavior"])
		}
	}
	if !ids["req-A"] || !ids["req-B"] {
		t.Fatalf("both envelopes must be answered, got %v", ids)
	}
}

// ---- 钩子分发 ----

// TestHookDispatchFourWay pins：四种 hook_event 经 control 显式映射进
// guard.Hooks（Task 21 交接：映射钉只能在 control 侧）；hook_response 携带
// Output/Outcome；未知 hook_event 静默忽略。
func TestHookDispatchFourWay(t *testing.T) {
	var turn []engine.Event
	for i, name := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop", "Mystery"} {
		turn = append(turn, engine.Event{Kind: engine.KindHookResponse, SessionID: "s", Hook: &engine.HookInfo{
			HookID: string(rune('a' + i)), HookName: "audit", HookEvent: name,
			Output: "out-" + name, Outcome: "success",
		}})
	}
	turn = append(turn, engine.Event{Kind: engine.KindResult, SessionID: "s",
		Result: &engine.ResultInfo{SubType: "success"}})
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{turn}})
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	hooks := guard.NewHooks()
	c.opts.Hooks = hooks
	var mu sync.Mutex
	var got []guard.HookPayload
	hooks.OnPanic = func(guard.HookEvent, int, any) { t.Error("hook panic") }
	for _, he := range []guard.HookEvent{guard.HookSessionStart, guard.HookPreToolUse, guard.HookPostToolUse, guard.HookStop} {
		hooks.On(he, func(_ context.Context, p guard.HookPayload) {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
		})
	}
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("hook dispatches = %d, want 4 (unknown event ignored)", len(got))
	}
	seen := map[string]guard.HookPayload{}
	for _, p := range got {
		seen[string(p.Event)] = p
	}
	for _, name := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop"} {
		p, ok := seen[name]
		if !ok {
			t.Fatalf("hook %s not dispatched", name)
		}
		if p.HookName != "audit" || p.Output != "out-"+name || p.Outcome != "success" {
			t.Fatalf("hook %s payload = %+v", name, p)
		}
	}
}

// TestCanUseToolWithTurnAccounting pins：权限回路与记账同回合共存——
// can_use_tool(allow) + 工具回合的 usage/TurnDone/cache.json 全部落位。
func TestCanUseToolWithTurnAccounting(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{
		{engine.Event{Kind: engine.KindResult, SessionID: "s", Result: &engine.ResultInfo{
			SubType: "success",
			Usage: engine.Usage{InputTokens: 100, OutputTokens: 20,
				CacheWriteTokens: 500, CacheReadTokens: 200},
		}}},
	}})
	sink := &recordingSink{}
	c := newTestControllerWithPolicyRules(t, fake, sink,
		policyRuleIn{raw: "Bash(go:*)", effect: guard.DecisionAllow})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := fake.InjectControlRequest(*canUseTool("req-9", "tu-9", "Bash", `{"command":"go version"}`).Control); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if err := c.Submit(context.Background(), "run it"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	resolved := resolvedOf(t, c, "tu-9")
	if resolved.Decision != "allow" {
		t.Fatalf("resolved = %+v", resolved)
	}
	proj, err := c.Mirror().ReadCache()
	if err != nil || proj == nil {
		t.Fatalf("cache.json: %v %v", proj, err)
	}
	if proj["turns"] != float64(1) {
		t.Fatalf("projection turns = %v", proj["turns"])
	}
}

// ---- 助手（本文件） ----

func waitResponses(t *testing.T, fake *engine.FakeHost, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for len(fake.Responses()) < n {
		select {
		case <-deadline:
			t.Fatalf("responses = %d, want %d", len(fake.Responses()), n)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitPending(t *testing.T, c *Controller, toolUseID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !c.HasPendingApproval(toolUseID) {
		select {
		case <-deadline:
			t.Fatalf("approval %s never registered", toolUseID)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
