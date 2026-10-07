package guard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestSourceConstants pins：Source 类型化常量（Task 6 交接）——与
// event.ApprovalResolvedEvent 的 source 词表一致（policy|session|user|timeout）。
// timeout 语义 = 一切非人为 fail-closed（审批超时 / 审批者错误）。
func TestSourceConstants(t *testing.T) {
	if SourcePolicy != "policy" || SourceSession != "session" ||
		SourceUser != "user" || SourceTimeout != "timeout" {
		t.Fatalf("source constants drifted: %q %q %q %q",
			SourcePolicy, SourceSession, SourceUser, SourceTimeout)
	}
}

// approverStub 是脚本化审批者。
type approverStub struct {
	verdict ApprovalVerdict
	err     error
	delay   time.Duration

	calls int
	last  ApprovalRequest
}

func (a *approverStub) ApproveRequest(ctx context.Context, req ApprovalRequest) (ApprovalVerdict, error) {
	a.calls++
	a.last = req
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-ctx.Done():
			return ApprovalVerdict{}, ctx.Err()
		}
	}
	if a.err != nil {
		return ApprovalVerdict{}, a.err
	}
	return a.verdict, nil
}

func gateForTest(t *testing.T, preset Preset, ap Approver, timeout time.Duration) *Gate {
	t.Helper()
	p, err := NewPolicy(preset, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGate(GateOptions{Policy: p, Approver: ap, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestGatePolicyPassThrough pins：policy 的 allow/deny 直接放行/拒绝，
// source=policy，不经审批者。
func TestGatePolicyPassThrough(t *testing.T) {
	ap := &approverStub{}
	p, _ := NewPolicy(PresetDefault, mustRules(t, ruleIn{raw: "Bash(go test:*)", effect: DecisionAllow}))
	g, err := NewGate(GateOptions{Policy: p, Approver: ap, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("go test ./..."))
	if err != nil || d.Decision != DecisionAllow || d.Source != SourcePolicy {
		t.Fatalf("policy allow: %+v %v", d, err)
	}
	d, err = g.Evaluate(context.Background(), "Bash", bashInput("rm -rf /"))
	// default 预设下 rm 是 Ask → Gate 送审（stub 零值 = 拒绝）；
	// Gate 从不返回 ask——ask 在 Gate 内被解决（Decision 词表只有 allow/deny）
	if err != nil || d.Decision != DecisionDeny || d.Source != SourceUser {
		t.Fatalf("ask must resolve via approver: %+v %v", d, err)
	}
	// deny 规则 → 直接拒绝
	p2, _ := NewPolicy(PresetDangerFull, mustRules(t, ruleIn{raw: "Bash(rm:*)", effect: DecisionDeny}))
	g2, _ := NewGate(GateOptions{Policy: p2, Approver: ap})
	d, err = g2.Evaluate(context.Background(), "Bash", bashInput("rm -rf /"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourcePolicy {
		t.Fatalf("policy deny: %+v %v", d, err)
	}
	if ap.calls != 1 {
		// 仅 rm 的 Ask 走了一次审批者；policy allow/deny 直裁不经审批者
		t.Fatalf("approver calls = %d, want 1 (only the ask case)", ap.calls)
	}
}

// TestGateAskGoesToApprover pins：Ask → 审批队列；裁决与 resolvedBy 透传
// （source=user）。
func TestGateAskGoesToApprover(t *testing.T) {
	ap := &approverStub{verdict: ApprovalVerdict{Allow: true, ResolvedBy: "user:allow-once"}}
	g := gateForTest(t, PresetDefault, ap, time.Second)
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("npm publish"))
	if err != nil || d.Decision != DecisionAllow || d.Source != SourceUser || d.ResolvedBy != "user:allow-once" {
		t.Fatalf("approve flow: %+v %v", d, err)
	}
	if ap.last.Tool != "Bash" || ap.last.ToolUseID == "" {
		t.Fatalf("approval request shape: %+v", ap.last)
	}

	ap2 := &approverStub{verdict: ApprovalVerdict{Allow: false, ResolvedBy: "user:deny"}}
	g2 := gateForTest(t, PresetDefault, ap2, time.Second)
	d, err = g2.Evaluate(context.Background(), "Bash", bashInput("npm publish"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourceUser || d.ResolvedBy != "user:deny" {
		t.Fatalf("deny flow: %+v %v", d, err)
	}
}

// TestGateApprovalRequestShape pins：送审请求携带规范化范围与超时秒数
// （TUI 卡片渲染需要）。
func TestGateApprovalRequestShape(t *testing.T) {
	ap := &approverStub{verdict: ApprovalVerdict{Allow: false}}
	g := gateForTest(t, PresetDefault, ap, 3*time.Second)
	_, _ = g.Evaluate(context.Background(), "Edit", pathInput("docs/a.md"))
	if ap.last.Scope != "dir:docs" {
		t.Fatalf("scope = %q, want dir:docs", ap.last.Scope)
	}
	if ap.last.Timeout != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", ap.last.Timeout)
	}
	if !strings.Contains(string(ap.last.Input), "docs/a.md") {
		t.Fatalf("input not carried: %s", ap.last.Input)
	}
}

// TestGateTimeoutFailClosed pins：审批者超时 → fail-closed Deny（source=timeout）。
func TestGateTimeoutFailClosed(t *testing.T) {
	ap := &approverStub{delay: 300 * time.Millisecond} // 超过 50ms 窗口
	g := gateForTest(t, PresetDefault, ap, 50*time.Millisecond)
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("slow thing"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourceTimeout {
		t.Fatalf("timeout must fail closed: %+v %v", d, err)
	}
}

// lateApprover 模拟不守约的审批者：窗口耗尽后仍返回 Allow。
type lateApprover struct{}

func (lateApprover) ApproveRequest(ctx context.Context, req ApprovalRequest) (ApprovalVerdict, error) {
	<-ctx.Done() // 等 Gate 的窗口关闭
	time.Sleep(5 * time.Millisecond)
	return ApprovalVerdict{Allow: true, ResolvedBy: "late"}, nil // 迟到的 allow
}

// TestGateLateVerdictFailClosed pins：迟到裁决必须 fail-closed（FR-005：
// 审批窗口属于 Gate——返回 (Allow, nil) 但窗口已耗尽 → deny(timeout)，
// 不依赖审批者自觉遵守 ctx）。
func TestGateLateVerdictFailClosed(t *testing.T) {
	g := gateForTest(t, PresetDefault, lateApprover{}, 30*time.Millisecond)
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("anything"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourceTimeout {
		t.Fatalf("late verdict must fail closed: %+v %v", d, err)
	}
}

// TestGateApproverErrorFailsClosed pins：审批者错误 = 非人为 fail-closed
// （source=timeout，语义「无有效人类裁决」）。
func TestGateApproverErrorFailsClosed(t *testing.T) {
	ap := &approverStub{err: errors.New("ui crashed")}
	g := gateForTest(t, PresetDefault, ap, time.Second)
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("x"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourceTimeout {
		t.Fatalf("approver error must fail closed: %+v %v", d, err)
	}
	if !strings.Contains(d.ResolvedBy, "ui crashed") {
		t.Fatalf("resolvedBy should carry error: %q", d.ResolvedBy)
	}
}

// TestGateUnattendedFailClosed pins：无人值守（无审批者）→ fail-closed 拒绝
// （source=policy，resolvedBy=unattended——配置驱动的基线，FR-009）。
func TestGateUnattendedFailClosed(t *testing.T) {
	g := gateForTest(t, PresetDefault, nil, time.Second)
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("anything"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourcePolicy {
		t.Fatalf("unattended must fail closed: %+v %v", d, err)
	}
	if !strings.Contains(d.ResolvedBy, "unattended") {
		t.Fatalf("resolvedBy should mark unattended: %q", d.ResolvedBy)
	}
}

// TestGateSessionAuthHit pins：会话级授权命中（工具+规范化范围）→ allow
// （source=session），不再问审批者；范围不同则照常送审。
func TestGateSessionAuthHit(t *testing.T) {
	ap := &approverStub{verdict: ApprovalVerdict{Allow: true, ResolvedBy: "user:session-scope"}}
	g := gateForTest(t, PresetDefault, ap, time.Second)
	// 送审一次并授予「本会话允许当前范围」
	d, _ := g.Evaluate(context.Background(), "Bash", bashInput("go test ./..."))
	if d.Decision != DecisionAllow {
		t.Fatalf("first ask must go to approver: %+v", d)
	}
	g.GrantSession("Bash", ap.last.Scope)
	if ap.last.Scope != "go test" {
		t.Fatalf("bash scope = %q, want 'go test'", ap.last.Scope)
	}
	// 同前缀范围内再调用 → 会话授权命中
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("go test -run X ./..."))
	if err != nil || d.Decision != DecisionAllow || d.Source != SourceSession {
		t.Fatalf("session grant must hit: %+v %v", d, err)
	}
	// 范围外 → 再次送审
	d, err = g.Evaluate(context.Background(), "Bash", bashInput("npm publish"))
	if err != nil || d.Source != SourceUser {
		t.Fatalf("outside scope must go to approver: %+v %v", d, err)
	}
	if ap.calls != 2 {
		t.Fatalf("approver calls = %d, want 2", ap.calls)
	}
}

// TestGateSessionGrantWholeTool pins：空范围授权 = 整工具授权（任意输入命中）。
func TestGateSessionGrantWholeTool(t *testing.T) {
	ap := &approverStub{verdict: ApprovalVerdict{Allow: true}}
	g := gateForTest(t, PresetDefault, ap, time.Second)
	_, _ = g.Evaluate(context.Background(), "Bash", bashInput("go test ./..."))
	g.GrantSession("Bash", "*")
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("curl evil.example"))
	if err != nil || d.Decision != DecisionAllow || d.Source != SourceSession {
		t.Fatalf("whole-tool grant must hit: %+v %v", d, err)
	}
}

// TestGatePolicyDenyNotOverridable pins：policy deny 不被会话授权覆盖
// （deny 是安全基线；会话授权只覆盖 Ask）。
func TestGatePolicyDenyNotOverridable(t *testing.T) {
	p, _ := NewPolicy(PresetDefault, mustRules(t, ruleIn{raw: "Bash(rm:*)", effect: DecisionDeny}))
	g, _ := NewGate(GateOptions{Policy: p})
	g.GrantSession("Bash", "rm")
	d, err := g.Evaluate(context.Background(), "Bash", bashInput("rm -rf /"))
	if err != nil || d.Decision != DecisionDeny || d.Source != SourcePolicy {
		t.Fatalf("policy deny must not be overridable: %+v %v", d, err)
	}
}

// TestNormalizeScope pins：范围规范化——Bash 取前两词（同一命令前缀）、
// 文件族取目录（同一目录；反斜杠规范化）、缺输入 → 空（仅整工具授权命中）。
func TestNormalizeScope(t *testing.T) {
	cases := []struct {
		tool  string
		input json.RawMessage
		want  string
	}{
		{"Bash", bashInput("go test ./..."), "go test"},
		{"Bash", bashInput("go"), "go"},
		{"Bash", bashInput("npm run build --silent"), "npm run"},
		{"Bash", bashInput("  "), ""},
		{"Bash", bashInput("go test && rm -rf /"), ""}, // 复合命令不获前缀背书（仅整工具授权可命中）
		{"Bash", bashInput("go test; rm -rf /"), ""},   // 分号同上
		{"Bash", bashInput("go test | tee x"), ""},     // 管道同上
		{"Bash", bashInput("go test $(evil)"), ""},     // 命令替换同上
		{"Bash", bashInput("go testx"), "go testx"},    // 词面不同 → scope 不同 → 不命中 "go test" 授权
		{"Edit", pathInput(`docs\sub\a.md`), "dir:docs/sub"},
		{"Edit", pathInput("a.md"), "dir:."},
		{"Write", pathInput("/abs/x.md"), "dir:/abs"},
		{"Read", pathInput("x.md"), "dir:."},
		{"Task", json.RawMessage(`{}`), ""},
		{"Bash", json.RawMessage(`{}`), ""},
	}
	for _, c := range cases {
		if got := NormalizeScope(c.tool, c.input); got != c.want {
			t.Fatalf("NormalizeScope(%s, %s) = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

// TestGateValidation pins：无 Policy 构造失败；0 超时取默认 120s。
func TestGateValidation(t *testing.T) {
	if _, err := NewGate(GateOptions{}); err == nil {
		t.Fatal("gate without policy must fail")
	}
	g, err := NewGate(GateOptions{Policy: mustPolicy(t, PresetDefault)})
	if err != nil {
		t.Fatal(err)
	}
	if g.timeout != 120*time.Second {
		t.Fatalf("default timeout = %v, want 120s", g.timeout)
	}
}

func mustPolicy(t *testing.T, preset Preset) *Policy {
	t.Helper()
	p, err := NewPolicy(preset, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
