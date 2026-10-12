package claude

import (
	"context"
	"strings"
	"testing"

	"mulix/internal/engine"
	"mulix/internal/llm"
)

// TestClaudeAdapterImplementsProvider 钉 011 FR-102/SC-002:适配器实现
// llm.Provider(编译期承重断言)。
func TestClaudeAdapterImplementsProvider(t *testing.T) {
	var _ llm.Provider = NewClaudeAdapter()
}

// TestClaudeAdapterSeamOnly 钉 seam-only 语义:回合方法 fail-closed 返回
// ErrNotBound(不静默);Events 返回 nil 流。
func TestClaudeAdapterSeamOnly(t *testing.T) {
	a := NewClaudeAdapter()
	if a.Name() != "claude-cli" {
		t.Fatalf("name = %q, want claude-cli", a.Name())
	}
	if err := a.Start(context.Background(), engine.BootConfig{}); err != llm.ErrNotBound {
		t.Fatalf("start err = %v, want ErrNotBound", err)
	}
	if a.Events() != nil {
		t.Fatal("events must be nil pre-bind")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("close (no resources) = %v, want nil", err)
	}
}

// TestBindLifecycle 钉 012 FR-101a:Bind 前 ErrNotBound/nil;Bind 后委托;
// 重复 Bind 报错(already bound)。
func TestBindLifecycle(t *testing.T) {
	a := NewClaudeAdapter()
	fake := engine.NewFakeHost(engine.FakeScript{})
	// Bind 前:seam-only 语义(既有断言面)。
	if err := a.Start(context.Background(), engine.BootConfig{}); err != llm.ErrNotBound {
		t.Fatalf("pre-bind start = %v, want ErrNotBound", err)
	}
	// Bind。
	if err := a.Bind(fake); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if a.Host() == nil {
		t.Fatal("bound host must be exposed via Host()")
	}
	if err := a.Start(context.Background(), engine.BootConfig{}); err != nil {
		t.Fatalf("post-bind start must delegate: %v", err)
	}
	// 重复 Bind 报错。
	if err := a.Bind(engine.NewFakeHost(engine.FakeScript{})); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("double bind err = %v, want already bound", err)
	}
}
