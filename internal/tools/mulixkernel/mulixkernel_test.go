package mulixkernel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mulix/internal/config"
	"mulix/internal/registry"
)

type stubTool struct {
	name, result string
}

func (s *stubTool) Name() string            { return s.name }
func (s *stubTool) Description() string     { return "stub" }
func (s *stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) ReadOnly() bool          { return true }
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return s.result, nil
}

func toolsFunc() map[string]registry.Tool {
	return map[string]registry.Tool{
		"ptc_execute": &stubTool{name: "ptc_execute", result: "x"},
		"echo":        &stubTool{name: "echo", result: "y"},
	}
}

// TestKernelInspectTool 钉 016 FR-101/SC-001:巡检 JSON——服务键、profile、
// 工具面、ptc_registered(快照活在 Execute 时)。
func TestKernelInspectTool(t *testing.T) {
	tool := NewKernelInspectTool(
		[]string{"profile", "registry", "meter"},
		config.ProfileStandard,
		toolsFunc,
	)
	if tool.Name() != "kernel_inspect" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !tool.ReadOnly() {
		t.Fatal("kernel_inspect 必须只读(015 终审先例:只读工具无审批需求)")
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rep struct {
		Services              []string `json:"services"`
		Profile               string   `json:"profile"`
		Tools                 []string `json:"tools"`
		PtcRegistered         bool     `json:"ptc_registered"`
		EventbusSubscriptions int      `json:"eventbus_subscriptions"`
		KeysSource            string   `json:"keys_source"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v (out=%s)", err, out)
	}
	if len(rep.Services) != 3 || rep.Services[0] != "meter" {
		t.Fatalf("services = %v", rep.Services)
	}
	if rep.Profile != "standard" {
		t.Fatalf("profile = %q", rep.Profile)
	}
	if !rep.PtcRegistered {
		t.Fatal("ptc_registered = false, want true")
	}
	if rep.EventbusSubscriptions != 0 {
		t.Fatalf("eventbus_subscriptions = %d, want 0(serve 进程无桥——前台键不跨进程)", rep.EventbusSubscriptions)
	}
	if rep.KeysSource != "env" {
		t.Fatalf("keys_source = %q, want env", rep.KeysSource)
	}
	// env 缺席形态(独立运行):keys 空——降级非阻断;keys_source=degraded
	// (019 FR-104;016 P6+ 路由核销——区分「真无服务键」与「桥缺席」,
	// 运维排障两态表象相同)。
	empty := NewKernelInspectTool(nil, config.ProfileStandard, toolsFunc)
	out, err = empty.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, `"services":[]`) {
		t.Fatalf("degraded out = %q err = %v, want services=[] 非阻断", out, err)
	}
	var drep struct {
		KeysSource string `json:"keys_source"`
	}
	if err := json.Unmarshal([]byte(out), &drep); err != nil {
		t.Fatalf("degraded json: %v", err)
	}
	if drep.KeysSource != "degraded" {
		t.Fatalf("degraded keys_source = %q, want degraded", drep.KeysSource)
	}
}

// TestCordisRunTool 钉 016 FR-102/SC-002:JS 全量执行+工具暴露+逃逸拒绝
// +超时 fail-closed(安全矩阵复用 P5c)。
func TestCordisRunTool(t *testing.T) {
	tool := NewCordisRunTool(toolsFunc, 5*time.Second)
	if tool.Name() != "cordis_run" {
		t.Fatalf("name = %q", tool.Name())
	}
	// JS 全量:纯计算(不限工具调用)。
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"code": "[1,2,3].map(x => x * 2).join(',')"}`))
	if err != nil || !strings.Contains(out, "2,4,6") {
		t.Fatalf("pure js out = %q err = %v", out, err)
	}
	// 工具暴露(同 P5c 沙箱路径)。
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"code": "echo({})"}`))
	if err != nil || !strings.Contains(out, "y") {
		t.Fatalf("tool call out = %q err = %v", out, err)
	}
	// 逃逸 fail-closed(复用 P5c 矩阵)。
	for _, code := range []string{
		`require("fs")`,
		`fetch("http://evil.com")`,
		`globalThis.process.exit(0)`,
		`import("fs")`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"code":`+quote(code)+`}`)); err == nil {
			t.Fatalf("escape %q must fail-closed", code)
		}
	}
	// 超时 fail-closed(测试用短超时;toolsFunc 非 nil——func 求值纪律)。
	tiny := NewCordisRunTool(toolsFunc, 100*time.Millisecond)
	if _, err := tiny.Execute(context.Background(), json.RawMessage(`{"code": "while(true) {}"}`)); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout err = %v, want timeout", err)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestRegisterSelfRefTools 钉 017 FR-103/SC-003:profile 单真源——
// standard 注册 kernel_inspect/cordis_run;minimal 不注册(无 bool 矛盾面)。
func TestRegisterSelfRefTools(t *testing.T) {
	var registered []string
	reg := func(t registry.Tool) { registered = append(registered, t.Name()) }

	RegisterSelfRefTools(config.ProfileMinimal, nil, toolsFunc, reg)
	if len(registered) != 0 {
		t.Fatalf("minimal registered = %v, want empty", registered)
	}
	RegisterSelfRefTools(config.ProfileStandard, []string{"profile"}, toolsFunc, reg)
	if len(registered) != 2 || registered[0] != "kernel_inspect" || registered[1] != "cordis_run" {
		t.Fatalf("standard registered = %v, want [kernel_inspect cordis_run]", registered)
	}
}

// TestParseKernelKeys 钉 017 FR-101:env 进程边界脏值滤除——TrimSpace+
// 滤空段;空串 → nil(降级非阻断形态)。
func TestParseKernelKeys(t *testing.T) {
	got := ParseKernelKeys("a, b,,c,")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("ParseKernelKeys dirty = %v, want [a b c]", got)
	}
	if got := ParseKernelKeys("  "); got != nil {
		t.Fatalf("ParseKernelKeys blank = %v, want nil", got)
	}
	if got := ParseKernelKeys(""); got != nil {
		t.Fatalf("ParseKernelKeys empty = %v, want nil", got)
	}
	if got := ParseKernelKeys("profile"); len(got) != 1 || got[0] != "profile" {
		t.Fatalf("ParseKernelKeys single = %v, want [profile]", got)
	}
}
