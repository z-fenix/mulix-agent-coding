// Package kernel 提供 Go 版 cordis 模式内核：层级 Context 作用域、服务注入
// (inject + Service 即插件)、插件挂载生命周期(MountAll 拓扑解析、Mount
// 回滚补偿)、effect 可逆注册与类型化事件总线小接口。
//
// 红线(修订来源:用户裁定 2026-10-11,变更 010——按 dsh 实现逻辑修订红线):
//   - leaf 级包——不 import 任何 internal 包;除经批准的 NATS 例外
//     (经 EventPublisher/EventSubscriber 小接口隔离,kernel 本体不 import NATS);
//   - 事件总线为内存订阅+JetStream 持久骨干(实现 internal/eventbus);
//     镜像 events.jsonl 是唯一持久审计真相源——持久化审计语义不经总线;
//   - 双层注册:插件注册 effect 可逆(本包 Effect);引擎可见清单回合开始
//     冻结快照(机制属 P4b);
//   - dispose 级联(先子后父)与 Mount 回滚补偿(已执行 effect undo 逆序)。
package kernel

import (
	"fmt"
	"sort"
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

// Keys 返回已注册服务键的只读快照:沿自身→祖先链收集,同键去重(子遮蔽
// 父,与 Service 查找语义一致),排序返回。巡检读面(016 FR-101 前置)——
// 仅读,不扩写面(治理红线:自指工具不得修改运行时)。
func (c *Context) Keys() []string {
	c.mu.Lock()
	keys := make([]string, 0, len(c.services))
	for name := range c.services {
		keys = append(keys, name)
	}
	c.mu.Unlock()
	if c.parent != nil {
		for _, k := range c.parent.Keys() {
			dup := false
			for _, have := range keys {
				if have == k {
					dup = true
					break
				}
			}
			if !dup {
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
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
		added := append([]func(){}, c.disposables[disposablesBefore:]...)
		c.disposables = c.disposables[:disposablesBefore]
		c.mu.Unlock()
		// 回滚补偿(009 FR-104 细化,010 FR-104 同语义):本次挂载期间登记的
		// 清理(含 effect undo)逆序执行——注册是可逆副作用,回滚必须跑清理
		// 而非丢弃;补偿先于服务删除(可能读取挂载期状态)。
		for i := len(added) - 1; i >= 0; i-- {
			added[i]()
		}
		c.mu.Lock()
		for name := range c.services {
			if _, was := existing[name]; !was {
				delete(c.services, name)
			}
		}
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

// Service 是「提供服务的插件」：键即 ctx.<key>；Mount 完成即服务就绪
// （cordis 忠实模型——服务本身就是插件；键即契约，消费方按 key 断言类型）。
type Service interface {
	Plugin
	ServiceKey() string
	Inject() []string
}

// NewService 是 Service 的语法糖实现：注入 deps 中的服务值，apply 产出服务
// 值并自动 Provide（key 即 ServiceKey）。deps 缺失时 Mount 返回错误
// （指名缺键；与 MountAll 拓扑解析配合，乱序提交由解析器重排）。
func NewService(key string, deps []string, name string,
	apply func(c *Context, deps map[string]any) (any, error)) Plugin {
	return &servicePlugin{name: name, key: key, deps: deps, apply: apply}
}

type servicePlugin struct {
	name  string
	key   string
	deps  []string
	apply func(c *Context, deps map[string]any) (any, error)
}

func (p *servicePlugin) Name() string       { return p.name }
func (p *servicePlugin) ServiceKey() string { return p.key }
func (p *servicePlugin) Inject() []string   { return p.deps }

func (p *servicePlugin) Mount(c *Context) error {
	deps := map[string]any{}
	for _, k := range p.deps {
		v, ok := c.Service(k)
		if !ok {
			return fmt.Errorf("kernel: service %q missing dep %q", p.key, k)
		}
		deps[k] = v
	}
	v, err := p.apply(c, deps)
	if err != nil {
		return err
	}
	return c.Provide(p.key, v)
}

// MountAll 批量挂载：普通 Plugin（未实现 Inject）按声明序最先；Service 按
// Inject 键拓扑排序（Kahn；提供键=ServiceKey）；缺键/环/自环/重复键
// fail-closed。单个 Mount 保持既有语义（无解析——调用方自理顺序）。FR-103。
func (c *Context) MountAll(plugins ...Plugin) error {
	var plain []Plugin
	var svcs []Service
	provided := map[string]Service{} // ServiceKey -> Service(重复键检测)
	for _, p := range plugins {
		svc, ok := p.(Service)
		if !ok {
			plain = append(plain, p)
			continue
		}
		if _, dup := provided[svc.ServiceKey()]; dup {
			return fmt.Errorf("kernel: duplicate ServiceKey %q", svc.ServiceKey())
		}
		provided[svc.ServiceKey()] = svc
		svcs = append(svcs, svc)
	}
	for _, p := range plain { // 普通插件声明序最先挂载
		if err := c.Mount(p); err != nil {
			return err
		}
	}
	// Kahn:入边 = Service.Inject 中被其他 Service 提供的键;已就绪键(作用域
	// 已有服务)视为无入边;缺键(无人提供且不在作用域)→ 拒挂。
	existing := func(key string) bool {
		_, ok := c.Service(key)
		return ok
	}
	degree := map[string]int{}
	dependents := map[string][]Service{}
	for _, s := range svcs {
		for _, k := range s.Inject() {
			if k == s.ServiceKey() {
				return fmt.Errorf("kernel: dependency cycle (self-loop): %s -> %s", s.ServiceKey(), s.ServiceKey())
			}
			if _, ok := provided[k]; ok {
				degree[s.ServiceKey()]++
				dependents[k] = append(dependents[k], s)
			} else if !existing(k) {
				return fmt.Errorf("kernel: mount %q missing dep %q", s.ServiceKey(), k)
			}
		}
	}
	var queue []Service
	for _, s := range svcs {
		if degree[s.ServiceKey()] == 0 {
			queue = append(queue, s)
		}
	}
	mounted := 0
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if err := c.Mount(s); err != nil {
			return err
		}
		mounted++
		for _, dep := range dependents[s.ServiceKey()] {
			degree[dep.ServiceKey()]--
			if degree[dep.ServiceKey()] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	if mounted != len(svcs) { // 剩余即环
		var names []string
		for _, s := range svcs {
			if degree[s.ServiceKey()] > 0 {
				names = append(names, s.ServiceKey())
			}
		}
		return fmt.Errorf("kernel: dependency cycle among %v", names)
	}
	return nil
}

// Effect 登记可逆注册：fn 成功执行后，undo 进入 disposables（LIFO——Dispose
// 逆序执行，FR-104）；fn 返回错误 → 原样上抛（Mount 期由回滚补偿机制处理）。
func (c *Context) Effect(fn func() (undo func(), err error)) error {
	undo, err := fn()
	if err != nil {
		return err
	}
	if undo != nil {
		c.OnDispose(undo)
	}
	return nil
}

// EventPublisher/EventSubscriber 是事件骨干的 kernel 侧小接口：kernel 及其
// 插件经此消费事件能力而不依赖 NATS（实现=internal/eventbus；组合根 Provide
// "events" 服务键）。FR-107。
type EventPublisher interface {
	DeclareEvents(names ...string) error
	Publish(name string, payload []byte) error
}

type EventSubscriber interface {
	Subscribe(name string, fn func(payload []byte)) (func(), error)
}
