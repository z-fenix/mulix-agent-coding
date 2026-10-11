package kernel

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ① Provide/Service 注入与祖先回溯：子作用域读到父服务；同名子覆盖父（遮蔽，不改父）。
func TestProvideServiceAndAncestorLookup(t *testing.T) {
	root := NewContext(nil)
	if root.Parent() != nil {
		t.Fatalf("root parent = %v, want nil", root.Parent())
	}
	if err := root.Provide("db", "root-db"); err != nil {
		t.Fatalf("provide root: %v", err)
	}
	child := NewContext(root)
	if child.Parent() != root {
		t.Fatalf("child parent = %v, want root", child.Parent())
	}
	if v, ok := child.Service("db"); !ok || v != "root-db" {
		t.Fatalf("child service db = %v,%v; want root-db,true（祖先回溯）", v, ok)
	}
	// 同名子覆盖父：仅遮蔽，父作用域不受影响
	if err := child.Provide("db", "child-db"); err != nil {
		t.Fatalf("provide child: %v", err)
	}
	if v, ok := child.Service("db"); !ok || v != "child-db" {
		t.Fatalf("child service db = %v,%v; want child-db,true（子覆盖父）", v, ok)
	}
	if v, ok := root.Service("db"); !ok || v != "root-db" {
		t.Fatalf("root service db = %v,%v; want root-db,true（父不受子覆盖影响）", v, ok)
	}
	if _, ok := child.Service("missing"); ok {
		t.Fatal("unknown service resolved, want miss")
	}
}

// ② 同作用域重复注册报错。
func TestProvideDuplicateInSameScope(t *testing.T) {
	root := NewContext(nil)
	if err := root.Provide("svc", 1); err != nil {
		t.Fatalf("first provide: %v", err)
	}
	if err := root.Provide("svc", 2); err == nil {
		t.Fatal("duplicate provide in same scope: want error")
	}
}

// ③ Dispose 先子后父：children LIFO → 自身 disposables LIFO（记录调用序断言）。
func TestDisposeChildrenBeforeSelfInLIFOOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(tag string) { mu.Lock(); order = append(order, tag); mu.Unlock() }

	root := NewContext(nil)
	c1 := NewContext(root)
	c2 := NewContext(root)
	c1.OnDispose(func() { record("c1") })
	c1.OnDispose(func() { record("c1b") }) // 自身 LIFO：后注册先执行
	c2.OnDispose(func() { record("c2") })
	root.OnDispose(func() { record("root") })

	root.Dispose()
	want := []string{"c2", "c1b", "c1", "root"}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("dispose order = %v, want %v（children LIFO 先于自身）", order, want)
	}
}

// ④ Dispose 幂等：重复调用与父级联收口后再调，清理函数均不重复执行。
func TestDisposeIdempotent(t *testing.T) {
	root := NewContext(nil)
	calls := 0
	root.OnDispose(func() { calls++ })
	root.Dispose()
	root.Dispose()
	root.Dispose()
	if calls != 1 {
		t.Fatalf("root dispose ran %d times, want 1", calls)
	}

	root2 := NewContext(nil)
	child := NewContext(root2)
	ccalls := 0
	child.OnDispose(func() { ccalls++ })
	root2.Dispose()
	child.Dispose() // 已随父收口，再调不得重复执行
	if ccalls != 1 {
		t.Fatalf("child dispose ran %d times, want 1", ccalls)
	}
}

// stubPlugin 是可编程插件：mount 闭包决定挂载期行为。
type stubPlugin struct {
	name  string
	mount func(c *Context) error
}

func (p *stubPlugin) Name() string { return p.name }
func (p *stubPlugin) Mount(c *Context) error {
	if p.mount != nil {
		return p.mount(c)
	}
	return nil
}

// ⑤a Mount 成功：插件贡献的服务经注入面可见。
func TestMountProvidesServices(t *testing.T) {
	root := NewContext(nil)
	p := &stubPlugin{name: "store", mount: func(c *Context) error {
		if err := c.Provide("store.root", "/sessions"); err != nil {
			return err
		}
		c.OnDispose(func() {})
		return nil
	}}
	if err := root.Mount(p); err != nil {
		t.Fatalf("mount: %v", err)
	}
	if v, ok := root.Service("store.root"); !ok || v != "/sessions" {
		t.Fatalf("service after mount = %v,%v; want /sessions,true", v, ok)
	}
}

// ⑤b Mount 失败整体回滚：挂载期注册的服务全部撤销；挂载期登记的清理函数
// （OnDispose/effect undo）按回滚补偿语义**逆序执行**（010 FR-104 行为细化
// ——注册是可逆副作用,回滚必须跑清理而非丢弃;原「不得触发」断言按新事实
// 修订,见台账 Task 10 Ruling）。
func TestMountFailureRollsBack(t *testing.T) {
	root := NewContext(nil)
	if err := root.Provide("pre", "keep"); err != nil {
		t.Fatalf("provide pre: %v", err)
	}
	boom := errors.New("boom")
	compensated := false
	p := &stubPlugin{name: "bad", mount: func(c *Context) error {
		if err := c.Provide("a", 1); err != nil {
			return err
		}
		if err := c.Provide("b", 2); err != nil {
			return err
		}
		c.OnDispose(func() { compensated = true })
		return boom
	}}
	err := root.Mount(p)
	if !errors.Is(err, boom) {
		t.Fatalf("mount err = %v, want boom", err)
	}
	for _, name := range []string{"a", "b"} {
		if _, ok := root.Service(name); ok {
			t.Fatalf("service %q still present after failed mount（回滚缺失）", name)
		}
	}
	if v, ok := root.Service("pre"); !ok || v != "keep" {
		t.Fatalf("pre-existing service = %v,%v; want keep,true", v, ok)
	}
	if !compensated {
		t.Fatal("rolled-back disposable must run as compensation (010 FR-104)")
	}
}

// 并发安全冒烟：Provide/Service/Mount/Dispose 混发（go test -race 下钉 mu 语义）。
func TestConcurrentAccess(t *testing.T) {
	root := NewContext(nil)
	child := NewContext(root)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := child.Provide(fmt.Sprintf("k%d", i), i); err != nil {
				t.Errorf("provide k%d: %v", i, err)
			}
			child.Service(fmt.Sprintf("k%d", i))
			child.Service("db")
		}(i)
	}
	wg.Wait()
	root.Dispose()
}

// TestServiceProvidesKeyedValue 钉 010 FR-102 前置(Service 即插件):
// NewService 插件 Mount 后,其键可经 Service 查得,值即 apply 产出。
func TestServiceProvidesKeyedValue(t *testing.T) {
	c := NewContext(nil)
	svc := NewService("llm", nil, "llm-adapter", func(c *Context, deps map[string]any) (any, error) {
		return "claude-cli-adapter", nil
	})
	if err := c.Mount(svc); err != nil {
		t.Fatalf("mount: %v", err)
	}
	v, ok := c.Service("llm")
	if !ok || v != "claude-cli-adapter" {
		t.Fatalf("service = (%v,%v), want (claude-cli-adapter,true)", v, ok)
	}
}

// plainPlugin 是测试用最小插件(声明序记序)。
type plainPlugin struct {
	name string
	run  func()
}

func (p *plainPlugin) Name() string           { return p.name }
func (p *plainPlugin) Mount(c *Context) error { p.run(); return nil }

// TestMountAllTopologicalOrder 钉 010 FR-103:乱序提交自动重排(消费先于
// 提供出现亦可);普通插件声明序最先。
func TestMountAllTopologicalOrder(t *testing.T) {
	c := NewContext(nil)
	order := []string{}
	plain := &plainPlugin{"plain", func() { order = append(order, "plain") }}
	llm := NewService("llm", nil, "llm", func(c *Context, _ map[string]any) (any, error) {
		order = append(order, "llm")
		return "adapter", nil
	})
	agent := NewService("agents", []string{"llm"}, "agents", func(c *Context, deps map[string]any) (any, error) {
		order = append(order, "agents")
		return deps["llm"], nil
	})
	if err := c.MountAll(agent, plain, llm); err != nil { // 乱序:agents 在 llm 前
		t.Fatalf("mount all: %v", err)
	}
	if len(order) != 3 || order[0] != "plain" || order[1] != "llm" || order[2] != "agents" {
		t.Fatalf("order = %v, want [plain llm agents]", order)
	}
}

// TestMountAllMissingKeyAndCycle 钉 010 FR-103:缺键拒挂含键名;环报错含
// 路径;自环按循环依赖报错(Edge Case 裁定)。
func TestMountAllMissingKeyAndCycle(t *testing.T) {
	c := NewContext(nil)
	missing := NewService("x", []string{"not-provided"}, "x", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	if err := c.MountAll(missing); err == nil || !strings.Contains(err.Error(), "not-provided") {
		t.Fatalf("missing key err = %v, want mention not-provided", err)
	}
	a := NewService("a", []string{"b"}, "a", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	bCycle := NewService("b", []string{"a"}, "b", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	if err := c.MountAll(a, bCycle); err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("cycle err = %v, want cycle path mentioning a", err)
	}
	self := NewService("self", []string{"self"}, "self", func(c *Context, _ map[string]any) (any, error) { return nil, nil })
	if err := c.MountAll(self); err == nil || !strings.Contains(err.Error(), "self -> self") {
		t.Fatalf("self-loop err = %v, want self -> self", err)
	}
}

// effectfulPlugin 是测试助手:Mount 内对目标 Context 逐个调用 c.Effect。
type effectfulPlugin struct {
	name    string
	target  *Context
	effects []func() (func(), error)
}

func (p *effectfulPlugin) Name() string { return p.name }
func (p *effectfulPlugin) Mount(c *Context) error {
	for _, fn := range p.effects {
		if err := p.target.Effect(fn); err != nil {
			return err
		}
	}
	return nil
}

// TestEffectRollbackCompensation 钉 010 FR-104:第二个 effect 的 fn 返回错误
// → Mount 失败,第一个 effect 的 undo 已逆序补偿执行(非静默截断)。
func TestEffectRollbackCompensation(t *testing.T) {
	c := NewContext(nil)
	var compensations []string
	p := &plainPlugin{"with-effects", func() {}}
	ef := &effectfulPlugin{"ef", c, []func() (func(), error){
		func() (func(), error) {
			return func() { compensations = append(compensations, "undo-1") }, nil
		},
		func() (func(), error) {
			return nil, fmt.Errorf("effect-2 failed")
		},
	}}
	if err := c.MountAll(p, ef); err == nil {
		t.Fatal("mount must fail on effect error")
	}
	if len(compensations) != 1 || compensations[0] != "undo-1" {
		t.Fatalf("compensations = %v, want [undo-1] (逆序补偿)", compensations)
	}
}

// TestEffectDisposeLIFO 钉 010 FR-104:成功路径下 effect undo 在 Dispose
// 时 LIFO 逆序执行。
func TestEffectDisposeLIFO(t *testing.T) {
	c := NewContext(nil)
	var order []string
	ef := &effectfulPlugin{"ef", c, []func() (func(), error){
		func() (func(), error) { return func() { order = append(order, "undo-1") }, nil },
		func() (func(), error) { return func() { order = append(order, "undo-2") }, nil },
	}}
	if err := c.MountAll(ef); err != nil {
		t.Fatalf("mount: %v", err)
	}
	c.Dispose()
	if len(order) != 2 || order[0] != "undo-2" || order[1] != "undo-1" {
		t.Fatalf("order = %v, want [undo-2 undo-1] (LIFO)", order)
	}
}

// TestEventInterfacesContract 钉 010 FR-107:kernel 侧事件小接口存在且契约
// 形态正确(实现=internal/eventbus;承重断言 var _ kernel.Event* = *Bus 落
// eventbus_test.go)。
func TestEventInterfacesContract(t *testing.T) {
	var _ EventPublisher = (EventPublisher)(nil)
	var _ EventSubscriber = (EventSubscriber)(nil)
}
