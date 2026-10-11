package eventbus

import (
	"path/filepath"
	"strings"
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

// TestDeclareEventsAndFailClosed 钉 FR-101 类型化:声明幂等;未声明 subject
// 经 validateDeclared 拒绝(fail-closed)。
// 注:发布/订阅层的 fail-closed 接线属 Task 3(Subscribe/Publish 定义处);
// 本任务只钉声明表与校验原语。
func TestDeclareEventsAndFailClosed(t *testing.T) {
	b, err := OpenBus(BusOptions{StoreDir: filepath.Join(t.TempDir(), "js")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()
	if err := b.DeclareEvents("turn.started"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := b.DeclareEvents("turn.started"); err != nil { // 幂等
		t.Fatalf("declare idempotent: %v", err)
	}
	if err := b.validateDeclared("mulix.turn.started"); err != nil {
		t.Fatalf("declared subject must validate: %v", err)
	}
	if err := b.validateDeclared("mulix.undeclared.event"); err == nil {
		t.Fatal("undeclared subject must fail validateDeclared (fail-closed)")
	}
	if !strings.Contains(b.validateDeclared("mulix.undeclared.event").Error(), "not declared") {
		t.Fatal("error must name the fail-closed reason")
	}
}
