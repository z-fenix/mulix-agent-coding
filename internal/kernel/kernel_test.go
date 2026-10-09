package kernel

import (
	"errors"
	"fmt"
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

// ⑤b Mount 失败整体回滚：挂载期注册的服务与清理函数全部撤销，挂载前注入不受影响。
func TestMountFailureRollsBack(t *testing.T) {
	root := NewContext(nil)
	if err := root.Provide("pre", "keep"); err != nil {
		t.Fatalf("provide pre: %v", err)
	}
	boom := errors.New("boom")
	p := &stubPlugin{name: "bad", mount: func(c *Context) error {
		if err := c.Provide("a", 1); err != nil {
			return err
		}
		if err := c.Provide("b", 2); err != nil {
			return err
		}
		c.OnDispose(func() { t.Error("rolled-back disposable must not run") })
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
	root.Dispose() // 回滚应已撤销挂载期 OnDispose：不得触发 t.Error
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
