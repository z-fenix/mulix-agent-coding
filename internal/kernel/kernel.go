// Package kernel 提供 Go 版 cordis 模式内核：层级 Context 作用域、服务注入、
// 插件挂载生命周期与 dispose 级联（先子后父）。
//
// 红线（design 002 §3.0）：leaf 级包——不 import 任何 internal 包、零第三方依赖；
// 不做事件发布（事件面仍走 sessionx 先持久化后发布的 P1 红线，kernel 不设第二条通道）。
package kernel

import (
	"fmt"
	"sync"
)

// Context 是层级作用域：子作用域继承父注入（Service 查找沿祖先链），Dispose 先子后父。
type Context struct {
	mu          sync.Mutex
	parent      *Context // 构造期定值，只读
	services    map[string]any
	disposables []func()
	children    []*Context
	disposed    bool
}

// NewContext 创建作用域；根作用域 = NewContext(nil)。parent 非 nil 时登记为其子
// 作用域，父 Dispose 时级联收口。
func NewContext(parent *Context) *Context {
	c := &Context{services: map[string]any{}}
	if parent != nil {
		parent.mu.Lock()
		parent.children = append(parent.children, c)
		parent.mu.Unlock()
		c.parent = parent
	}
	return c
}

// Parent 返回父作用域（根为 nil）。
func (c *Context) Parent() *Context { return c.parent }

// Provide 在本作用域注册服务；同作用域重复注册报错（子作用域同名注册是覆盖父的
// 遮蔽，不属于重复）。
func (c *Context) Provide(name string, svc any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.services[name]; dup {
		return fmt.Errorf("kernel: service %q already provided in this scope", name)
	}
	c.services[name] = svc
	return nil
}

// Service 沿自身 → 祖先链查找服务。
func (c *Context) Service(name string) (any, bool) {
	c.mu.Lock()
	svc, ok := c.services[name]
	c.mu.Unlock()
	if ok {
		return svc, true
	}
	if c.parent != nil {
		return c.parent.Service(name)
	}
	return nil, false
}

// OnDispose 注册清理函数（LIFO：后注册先执行）。
func (c *Context) OnDispose(fn func()) {
	c.mu.Lock()
	c.disposables = append(c.disposables, fn)
	c.mu.Unlock()
}

// Plugin 是可挂载单元：挂载时向作用域贡献服务与清理函数（c.Provide / c.OnDispose）。
type Plugin interface {
	Name() string
	Mount(c *Context) error
}

// Mount 挂载插件：p.Mount(c)；失败即整体回滚——撤销本次挂载期间注册的服务与
// 清理函数（挂载前已存在的注入不受影响）。
func (c *Context) Mount(p Plugin) error {
	c.mu.Lock()
	existing := make(map[string]struct{}, len(c.services))
	for name := range c.services {
		existing[name] = struct{}{}
	}
	disposablesBefore := len(c.disposables)
	c.mu.Unlock()

	if err := p.Mount(c); err != nil {
		c.mu.Lock()
		for name := range c.services {
			if _, was := existing[name]; !was {
				delete(c.services, name)
			}
		}
		c.disposables = c.disposables[:disposablesBefore]
		c.mu.Unlock()
		return fmt.Errorf("kernel: mount plugin %q: %w", p.Name(), err)
	}
	return nil
}

// Dispose 幂等收口：先全部子作用域（LIFO），再执行自身 disposables（LIFO）。
// 清理函数在锁外执行，允许其内部再调用本作用域的方法。
func (c *Context) Dispose() {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return
	}
	c.disposed = true
	children := c.children
	c.children = nil
	disposables := c.disposables
	c.disposables = nil
	c.mu.Unlock()

	for i := len(children) - 1; i >= 0; i-- {
		children[i].Dispose()
	}
	for i := len(disposables) - 1; i >= 0; i-- {
		disposables[i]()
	}
}
