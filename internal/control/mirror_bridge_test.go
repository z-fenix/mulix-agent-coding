package control

import (
	"encoding/json"
	"strings"
	"testing"

	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/kernel"
)

// 承重断言:recordBus 实现 kernel.EventPublisher(桥消费面)。
var _ kernel.EventPublisher = (*recordBus)(nil)

// recordBus 是 kernel.EventPublisher 的记录实现(桥观测面)。
type recordBus struct {
	declared  []string
	published []struct {
		name    string
		payload []byte
	}
	failOn string // 注入:该事件名发布失败
}

func (b *recordBus) DeclareEvents(names ...string) error {
	b.declared = append(b.declared, names...)
	return nil
}

func (b *recordBus) Publish(name string, payload []byte) error {
	if b.failOn != "" && name == b.failOn {
		return errBusExplode
	}
	b.published = append(b.published, struct {
		name    string
		payload []byte
	}{name, payload})
	return nil
}

var errBusExplode = errString("bus explode (injected)")

type errString string

func (e errString) Error() string { return string(e) }

// TestMirrorBridgeOrderAndPayload 钉 012 FR-104:镜像先行后总线 Emit
// (事件名=mirror.<kind>;payload=镜像行同源序列化);顺序可证。
// (终审 Important 修复:计划 T4 Step 1 的覆盖缺口补齐——桥代码 012 已
// 实现,本测试为语义钉形态,无运行期 RED;ledger 如实记录。)
func TestMirrorBridgeOrderAndPayload(t *testing.T) {
	bus := &recordBus{}
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.mu.Lock()
	c.evPub = bus // NewController 已从 opts.EventPublisher 复制;此处注入测试观测面
	c.mu.Unlock()
	c.publish(event.TurnStartedEvent{Seq: 1, SessionID: "s-test"})
	if len(bus.published) != 1 {
		t.Fatalf("bus published = %d, want 1", len(bus.published))
	}
	if bus.published[0].name != "mirror.turn_started" {
		t.Fatalf("event name = %q, want mirror.turn_started", bus.published[0].name)
	}
	var probe map[string]any
	if err := json.Unmarshal(bus.published[0].payload, &probe); err != nil {
		t.Fatalf("payload must be the mirror line (json): %v", err)
	}
	if probe["kind"] != "turn_started" {
		t.Fatalf("payload kind = %v", probe["kind"])
	}
}

// TestMirrorBridgeFailureFailVisible 钉 012 FR-105:总线失败 → 镜像已写
// (不回滚)+ fail-visible 留痕(直接 Store.Append+Sink.Publish——pubMu
// 不可重入,禁走 c.publish)+ 桥自禁(后续 publish 不再尝试总线)。
func TestMirrorBridgeFailureFailVisible(t *testing.T) {
	bus := &recordBus{failOn: "mirror.turn_started"}
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.mu.Lock()
	c.evPub = bus
	c.mu.Unlock()
	c.publish(event.TurnStartedEvent{Seq: 1, SessionID: "s-test"})
	// 镜像已有该事件(不回滚)。
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	found := false
	for _, ev := range mirror {
		if _, ok := ev.(event.TurnStartedEvent); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("mirror must keep the event (桥失败不阻断镜像)")
	}
	// fail-visible:直接 Append 的 warning Notice 落镜像(不经 c.publish——
	// pubMu 不可重入;不经桥——防递归)。
	warnFound := false
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "eventbus") {
			warnFound = true
		}
	}
	if !warnFound {
		t.Fatal("bridge failure must leave a fail-visible Notice (FR-105)")
	}
	// 桥自禁:第二次 publish 不再尝试总线(bus.published 不增长)。
	n := len(bus.published)
	c.publish(event.TurnStartedEvent{Seq: 2, SessionID: "s-test"})
	if len(bus.published) != n {
		t.Fatal("bridge must self-disable after failure (防递归)")
	}
}
