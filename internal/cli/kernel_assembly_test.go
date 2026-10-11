package cli

import (
	"testing"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/guard"
	"mulix/internal/sessionx"
)

// testAssemblySink 是最小 Sink(Publish no-op——装配测试不消费事件)。
type testAssemblySink struct{}

func (testAssemblySink) Publish(any) {}

// TestAssembleKernelContextServiceKeys 钉 011 FR-101(SC-001):装配产出的
// 依赖以 kernel 服务键登记——键就位且与 Options 字段同源(同值)。
func TestAssembleKernelContextServiceKeys(t *testing.T) {
	store := &sessionx.Store{}
	meter := &cache.Meter{}
	gate := &guard.Gate{}
	sink := testAssemblySink{}
	cfg := config.Config{}
	o := control.Options{
		HostFactory: func(boot engine.BootConfig) (engine.Host, error) { return nil, nil },
		Sink:        sink,
		Store:       store,
		Meter:       meter,
		Gate:        gate,
		Config:      cfg,
	}
	root, opts, err := assembleKernelContext(o)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, k := range []string{"sink", "store", "meter", "gate", "config"} {
		if _, ok := root.Service(k); !ok {
			t.Fatalf("key %q missing (SC-001)", k)
		}
	}
	if v, ok := root.Service("sink"); !ok || v.(control.Sink) != sink {
		t.Fatalf("sink same-source check failed")
	}
	if v, ok := root.Service("store"); !ok || v.(*sessionx.Store) != store {
		t.Fatalf("store same-source check failed")
	}
	if v, ok := root.Service("meter"); !ok || v.(*cache.Meter) != meter {
		t.Fatalf("meter same-source check failed")
	}
	if v, ok := root.Service("gate"); !ok || v.(*guard.Gate) != gate {
		t.Fatalf("gate same-source check failed")
	}
	if opts.Store != store || opts.Meter != meter || opts.Gate != gate {
		t.Fatalf("back-filled Options mismatch")
	}
	if _, ok := root.Service("approvals"); ok {
		t.Fatal("approvals key must be absent when nil (nil=功能关闭)")
	}
}
