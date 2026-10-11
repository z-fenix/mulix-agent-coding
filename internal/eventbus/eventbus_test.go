package eventbus

import (
	"path/filepath"
	"testing"
)

// TestOpenCloseBus 钉 FR-107/FR-108 骨架:内嵌 server + JetStream 可启停,
// InProcess 连接可用,Close 后资源收口(不监听 TCP——仅内嵌连接)。
func TestOpenCloseBus(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if b.Conn() == nil || b.JS() == nil {
		t.Fatal("in-process conn and JetStream context must be available")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
