package ptc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// TestSandboxToolCall 钉 015 FR-102:注册工具暴露为 JS 函数——脚本调用
// 工具并获得结果。
func TestSandboxToolCall(t *testing.T) {
	tools := map[string]registry.Tool{
		"echo": &stubTool{name: "echo", result: "hello from echo"},
	}
	s := NewSandbox(tools, 5*time.Second)
	result, err := s.Execute(`echo({message: "hello"})`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(result, "hello from echo") {
		t.Fatalf("result = %q, want echo result", result)
	}
}

// TestSandboxEscapeRejected 钉 015 FR-103:沙箱安全——require/fetch/fs/
// network 逃逸拒绝(fail-closed)。
func TestSandboxEscapeRejected(t *testing.T) {
	s := NewSandbox(nil, 5*time.Second)
	escapes := []string{
		`require("fs")`,
		`fetch("http://evil.com")`,
		`globalThis.process.exit(0)`,
		`import("fs")`,
	}
	for _, script := range escapes {
		_, err := s.Execute(script)
		if err == nil {
			t.Fatalf("escape %q must fail-closed", script)
		}
	}
}

// TestSandboxTimeout 钉 015 FR-104:超时 fail-closed(30s 缺省;测试用短超时)。
func TestSandboxTimeout(t *testing.T) {
	s := NewSandbox(nil, 100*time.Millisecond)
	_, err := s.Execute(`while(true) {}`)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout err = %v, want timeout", err)
	}
}
