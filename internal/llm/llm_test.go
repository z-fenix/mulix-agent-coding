package llm

import "testing"

// TestProviderInterfaceExists 钉 011 FR-102:接缝存在(编译期形态钉)。
func TestProviderInterfaceExists(t *testing.T) {
	var _ Provider = (Provider)(nil)
}

// TestFakeProviderImplementsProvider 钉测试装配路径(FakeProvider 同为
// seam-only 形态,Name()="fake";定义见 T2 Step 3 的 llm.go 追加段)。
func TestFakeProviderImplementsProvider(t *testing.T) {
	var _ Provider = NewFakeProvider()
}
