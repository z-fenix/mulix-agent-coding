package llm

import "testing"

// TestProviderInterfaceExists 钉 011 FR-102:接缝存在(编译期形态钉)。
func TestProviderInterfaceExists(t *testing.T) {
	var _ Provider = (Provider)(nil)
}
