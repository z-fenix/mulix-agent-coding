package claude

import (
	"context"
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
