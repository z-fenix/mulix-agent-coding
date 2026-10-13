# P4c 事件总线生产接线 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 事件总线从「仅持久无消费」翻转为生产接线:`[eventbus]` 配置门控单真源(CLI flag 退役)+ 本地监听 endpoint 握手 + `mulix events` 首个订阅消费者。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/018-p4c-eventbus-production-wiring/specs/2026-10-13-p4c-design.md`)①config 三态门控(014 Profile 同款);②openSession 经 config 单真源(双真源矛盾面退役——017 纪律);③内嵌 NATS 开 127.0.0.1 随机端口监听,ClientURL 原子写 `<ws>/.mulix/bus/endpoint`(tmp+rename);④`mulix events` 读 endpoint→订阅 `mulix.mirror.*` 实时打印(纯订阅零写入;总线 subject 真身含 mulix. 前缀——评审 Critical 修正)。

**Tech Stack:** Go 标准库 + nats-server/nats.go(既有批准依赖,零新增)。

**Spec:** `docs/specs/018-p4c-eventbus-production-wiring/spec.md`

**Design:** `.mulix/.runtime/018-p4c-eventbus-production-wiring/specs/2026-10-13-p4c-design.md`

## Global Constraints

- FR-104 红线:先持久化后发布发布序**零触碰**(本变更只翻接线/加订阅侧,桥 Emit 路径不动);镜像 events.jsonl 1:1 不变。
- FR-105:每任务 `go test ./... -count=1` 全绿再 commit;`go vet ./...`+`gofmt -l` 干净;`go run ./cmd/mulix lint layers` 0 违规;零第三方新增。
- 缺省行为不变(SC-001):不写 `[eventbus]` 的用户,启动/回合/桥状态与 017 逐字节一致。
- 监听暴露面:仅 127.0.0.1+随机端口,且仅门控开启时存在。

## Review Focus

- 三态语义:`""`→关、`"true"`→开、`"false"`→关、其他→fail-closed(错误含原值)——校验在 Load 末尾、lastGood 赋值**之前**(014 同款位置)。
- flag 退役完整性:`Options.EnableEventBus`/`assembleSessionSpec.EnableEventBus` 双字段删除后全仓零残留(grep 钉);既有测试经 config helper 迁移,无旁路。
- endpoint 握手:原子写(tmp+rename);写失败 OpenBus 报错(fail-closed——开了没 endpoint 等于没开)。
- 观察面纯订阅:`mulix events` 零写入任何文件;Ctrl-C 干净退出(Unsubscribe+Drain+Close)。
- 跨进程连通:非 InProcess 客户端经 endpoint URL 真实连接+收行(测试钉)。

---

## Task 1: EventbusConfig 三态门控

**Files:**
- Modify: `internal/config/config.go`(EventbusConfig 类型+Config.Eventbus 字段+Load 校验)
- Test: `internal/config/config_test.go`(追加)

**Interfaces:**
- Produces: `type EventbusConfig struct { RawEnable string \`toml:"enable"\` }` + `func (e EventbusConfig) Enabled() (bool, error)`(""→false;"true"→true;"false"→false;其他→error 含原值);`Config.Eventbus EventbusConfig`(`toml:"eventbus"`)。T2 消费。

- [ ] **Step 1: 写失败测试**

`internal/config/config_test.go` 追加(沿用 014 profile 三态测试形态):

```go
// TestEventbusEnabled 钉 018 FR-101:[eventbus] 三态——缺省关/true/false/
// 非法 fail-closed(错误含原值)。
func TestEventbusEnabled(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		want   bool
		wantEr bool
	}{
		{raw: "", want: false},
		{raw: "false", want: false},
		{raw: "true", want: true},
		{raw: "yes", wantEr: true},
		{raw: "TRUE", wantEr: true},
	} {
		got, err := EventbusConfig{RawEnable: tc.raw}.Enabled()
		if tc.wantEr {
			if err == nil || !strings.Contains(err.Error(), tc.raw) {
				t.Fatalf("raw %q err = %v, want fail-closed 含原值", tc.raw, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("raw %q = (%v,%v), want (%v,nil)", tc.raw, got, err, tc.want)
		}
	}
}
```

(若该文件未 import `strings` 则补;`EventbusConfig` 未定义 → 编译期 RED。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/config/ -run TestEventbusEnabled -count=1`
Expected: FAIL(`undefined: EventbusConfig`——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/config/config.go`:

```go
// EventbusConfig 是 [eventbus] 节(018 FR-101):镜像→总线桥生产接线门控。
// 缺省关(不写=行为与 017 逐字节一致);显式 true/false;非法值 Load 末尾
// fail-closed(014 profile 同款)。
type EventbusConfig struct {
	RawEnable string `toml:"enable"`
}

// Enabled 返回门控态;非法值报错(错误含原值)。
func (e EventbusConfig) Enabled() (bool, error) {
	switch e.RawEnable {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("config: eventbus.enable %q must be true or false", e.RawEnable)
	}
}
```

`Config` 结构体增字段(与既有节并排):

```go
	Eventbus     EventbusConfig    `toml:"eventbus"`
```

`Load` 的 profile 校验块之后、`l.lastGood = &cfg` 之前(lastGood 纪律同 014):

```go
	// eventbus.enable 校验(018 FR-101:三态 fail-closed,错误含原值)——
	// 在 lastGood 赋值之前(拒绝的配置不得成为 last good)。
	if _, eberr := cfg.Eventbus.Enabled(); eberr != nil {
		return nil, warnings, eberr
	}
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/config/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): [eventbus] 三态门控——缺省关 fail-closed(018 FR-101;P4c T1)"
```

---

## Task 2: openSession 单真源接线 + flag 退役

**Files:**
- Modify: `internal/cli/root.go:41`(Options.EnableEventBus 字段删除)
- Modify: `internal/cli/session.go`(262 行门控改 config;431-432 死字段删除)
- Test: `internal/cli/kernel_assembly_test.go`(TestKernelKeysIncludeEventsKey 迁移至 config 门控)

**Interfaces:**
- Consumes: Task 1 `EventbusConfig.Enabled()`。
- Produces: 桥接线唯一真源=cfg.Eventbus;`Options`/`assembleSessionSpec` 零 EnableEventBus 残留。

- [ ] **Step 1: 写失败测试(迁移既有钉)**

`internal/cli/kernel_assembly_test.go` 内追加 helper(`testConfig` 定义在 tui_test.go 343 行——同包直用),再把 `TestKernelKeysIncludeEventsKey` 的 `Options{... EnableEventBus: true, ...}` 迁移至 config 门控:

```go
// withEventbusEnabled 返回开总线门控的配置副本(018:T2 起 flag 退役——
// 单真源=cfg.Eventbus)。
func withEventbusEnabled() *config.Config {
	cfg := testConfig()
	cfg.Eventbus.RawEnable = "true"
	return cfg
}
```

`TestKernelKeysIncludeEventsKey` 的 Options 行 `Config: testConfig(), EnableEventBus: true,` 改为 `Config: withEventbusEnabled(),`。

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/cli/ -run TestKernelKeysIncludeEventsKey -count=1`
Expected: FAIL(env 缺 MULIX_KERNEL_KEYS/events——门控仍读已废弃 flag;`Options` 删字段前先经历行为 RED;若编译期红「EnableEventBus 已删」亦可,按实际形态入台账)。

- [ ] **Step 3: 实现**

`internal/cli/session.go` 262 行门控块替换(assembleController 内,cfg 为入参):

```go
	// 镜像→总线桥(018 FR-102:单真源=cfg.Eventbus——CLI flag 退役):
	// 进程级单例(按工作区键);openSession 失败=桥关闭(如实记录)。
	eventbusOn, eberr := cfg.Eventbus.Enabled()
	if eberr != nil {
		return nil, nil, fmt.Errorf("eventbus: gate: %w", eberr)
	}
	if eventbusOn {
```

(原 `if o.EnableEventBus {` 整行替换;块体不动。)

`internal/cli/root.go` 41 行 `EnableEventBus bool` 字段删除;`internal/cli/session.go` 431-432 `assembleSessionSpec.EnableEventBus` 死字段删除(全仓 grep 零残留验证在 Step 4)。

- [ ] **Step 4: 跑测试验证通过 + 零回归 + 退役钉**

Run: `go test ./internal/cli/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿

退役钉(逐字跑,期望零命中):

```bash
grep -rn "EnableEventBus" internal/ cmd/ || echo "T2 PASS: flag 零残留"
```

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "refactor(cli): 桥接线单真源 cfg.Eventbus——EnableEventBus flag 退役(018 FR-102;P4c T2)"
```

---

## Task 3: 内嵌监听 + endpoint 原子写

**Files:**
- Modify: `internal/eventbus/eventbus.go`(OpenBus 监听+endpoint)
- Test: `internal/eventbus/eventbus_test.go`(追加)

**Interfaces:**
- Produces: OpenBus 后 `<StoreDir>/endpoint` 文件存在,内容=`nc.ClientURL()`(nats://127.0.0.1:<随机端口>);非 InProcess 客户端可经该 URL 连接;原子写(tmp+rename),写失败 OpenBus 报错。

- [ ] **Step 1: 写失败测试**

`internal/eventbus/eventbus_test.go` 追加:

```go
// TestOpenBusEndpointHandshake 钉 018 FR-103 前置:监听+endpoint 握手——
// endpoint 文件存在且非 InProcess 客户端可经它连接收发。
func TestOpenBusEndpointHandshake(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer b.Close()

	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	url := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(url, "nats://127.0.0.1:") {
		t.Fatalf("endpoint = %q, want nats://127.0.0.1:<port>", url)
	}
	// 独立连接(不经 InProcess 选项)——跨进程形态核验。
	// subject 真身:桥声明 "mirror.test" → Emit 加 "mulix." 前缀 →
	// 总线实际 subject = mulix.mirror.test(评审 Critical 修正)。
	if err := b.DeclareEvents("mirror.test"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatalf("external connect: %v", err)
	}
	defer nc.Close()
	got := make(chan string, 1)
	sub, err := nc.Subscribe("mulix.mirror.test", func(m *nats.Msg) { got <- string(m.Data) })
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	nc.Flush() // SUB 协议先于 PUB 到达服务器(订阅注册竞态——实现期探针坐实)
	if err := b.Publish("mirror.test", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-got:
		if msg != "hello" {
			t.Fatalf("msg = %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("external subscriber got nothing")
	}
}
```

(该测试文件现有 import 若缺 `os/filepath/strings/time/nats` 按需补——以现状为准。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestOpenBusEndpointHandshake -count=1`
Expected: FAIL(endpoint: open …——文件不存在;监听未开)。

- [ ] **Step 3: 实现**

`internal/eventbus/eventbus.go` OpenBus 内,`server.Options` 改(监听 127.0.0.1 随机端口——仅门控开启时 OpenBus 被调,默认关=零暴露):

```go
	srv, err := server.NewServer(&server.Options{
		JetStream: true,
		StoreDir:  opts.StoreDir,
		NoLog:     true,
		Host:      "127.0.0.1", // 仅本地(018 设计裁定——观察面跨进程握手)
		Port:      -1,          // 随机端口(018 Ruling:nats-server 语义 0=默认 4222、-1=随机)
	})
```

`ReadyForConnections` 通过后、`return` 前原子写 endpoint:

```go
	// endpoint 握手(018 FR-103):观察面经此发现总线;原子写(tmp+rename,
	// memory 同款纪律);写失败=OpenBus 报错——开了没 endpoint 等于没开。
	// 插入点:nc 连接与 JetStream 上下文建立成功之后、return 之前
	// (评审 Important 修正——nc 在 Connect 成功后才存在)。
	endpoint := filepath.Join(opts.StoreDir, "endpoint")
	tmp := endpoint + ".tmp"
	if werr := os.WriteFile(tmp, []byte(nc.ClientURL()), 0o600); werr != nil {
		nc.Close()
		srv.Shutdown()
		return nil, fmt.Errorf("eventbus: write endpoint tmp: %w", werr)
	}
	if rerr := os.Rename(tmp, endpoint); rerr != nil {
		nc.Close()
		srv.Shutdown()
		return nil, fmt.Errorf("eventbus: rename endpoint: %w", rerr)
	}
```

(import 补 `os`、`path/filepath`。)InProcess 连接保持不变(`nats.InProcessServer(srv)` 与 TCP 监听共存)。

(注:endpoint 文件落 StoreDir 根(与 JetStream `$JS` 结构并列)——恢复期对顶层非目录文件的兼容性由本任务测试即刻暴露;若报错,备选=JetStream 用子目录或 endpoint 上移一级(评审 Advisory,实现者现场核验)。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿(既有 eventbus 测试经 InProcess 连接不受监听影响)。

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): 本地监听+endpoint 原子握手(018 FR-103 前置;P4c T3)"
```

---

## Task 4: mulix events 观察面 + 全矩阵回归

**Files:**
- Create: `internal/eventbus/observe.go`
- Test: `internal/eventbus/eventbus_test.go`(追加 observe 钉)
- Modify: `internal/cli/root.go`(newEventsCommand)
- Create: `docs/evidence/018-events-smoke.md`(真机冒烟,FR-108 惯例)

**Interfaces:**
- Consumes: Task 3 endpoint;既有 Bus.Publish/镜像桥(发布序零触碰)。
- Produces: `func ObserveMirrors(ctx context.Context, url string, w io.Writer) error`(连接+订阅 `mulix.mirror.*`+行打印 `<RFC3339> <kind> <payload>`;ctx 取消→Unsubscribe+Drain+Close 后返回 nil);CLI `mulix events [--workspace W]`(endpoint 缺席→fail-closed 报「总线未启用」)。

- [ ] **Step 1: 写失败测试**

`internal/eventbus/eventbus_test.go` 追加:

```go
// TestObserveMirrors 钉 018 FR-103:观察面——订阅 mulix.mirror.* 实时收行;
// ctx 取消干净退出;连接失败 fail-closed。
// syncBuffer:回调 goroutine 写 out 与主 goroutine 轮询并发——mutex 化
// (评审 Minor 修正:-race 不可用不等于无竞态)。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestObserveMirrors(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}

	var out syncBuffer
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ObserveMirrors(ctx, strings.TrimSpace(string(raw)), &out) }()
	time.Sleep(200 * time.Millisecond) // 订阅就绪
	// subject 真身:声明 "mirror.turn_started" → Emit 加前缀 → mulix.mirror.turn_started。
	if err := b.DeclareEvents("mirror.turn_started"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := b.Publish("mirror.turn_started", []byte(`{"seq":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "turn_started") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "turn_started") {
		t.Fatalf("observe out = %q, want kind 行", out.String())
	}
	stop() // 干净退出
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("observe exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observe did not exit after ctx cancel")
	}
	// 连接失败 fail-closed。
	if err := ObserveMirrors(context.Background(), "nats://127.0.0.1:1", io.Discard); err == nil {
		t.Fatal("bad url must fail-closed")
	}
}
```

(import 以该文件现状为准补 `bytes/io/sync`。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/eventbus/ -run TestObserveMirrors -count=1`
Expected: FAIL(`undefined: ObserveMirrors`——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

Create `internal/eventbus/observe.go`:

```go
package eventbus

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// ObserveMirrors 是外部观察面(018 FR-103):连接 url、订阅 mulix.mirror.*
// (总线 subject 真身——Emit 统一加 "mulix." 前缀,桥声明 "mirror.<kind>",
// 评审 Critical 修正)、行打印 `<RFC3339> <kind> <payload>`。纯订阅——零写入
// 任何文件(镜像 events.jsonl 1:1 审计面不受影响)。ctx 取消→Unsubscribe+
// Drain+Close 后返回 nil(Drain 异步——Close 截断在途尾行,观察面退出语义
// 不关心尾行,评审 Advisory 认可)。
func ObserveMirrors(ctx context.Context, url string, w io.Writer) error {
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("events: connect %s: %w", url, err)
	}
	defer nc.Close()
	sub, err := nc.Subscribe("mulix.mirror.*", func(m *nats.Msg) {
		kind := strings.TrimPrefix(m.Subject, "mulix.mirror.")
		summary := strings.ReplaceAll(string(m.Data), "\n", " ")
		if len(summary) > 200 {
			summary = summary[:200]
		}
		_, _ = fmt.Fprintf(w, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339), kind, summary)
	})
	if err != nil {
		return fmt.Errorf("events: subscribe: %w", err)
	}
	nc.Flush() // 订阅注册先行——观察面启动即收新行(无 Flush 存在注册竞态窗口)
	defer func() {
		_ = sub.Unsubscribe()
		_ = nc.Drain()
	}()
	<-ctx.Done()
	return nil
}
```

`internal/cli/root.go` 加子命令(与既有命令并排;读 endpoint 失败=fail-closed 报「总线未启用」):

```go
func newEventsCommand(opts *Options) *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "events",
		Short: "订阅 mulix.mirror.* 实时打印(外部观察面;需 [eventbus] enable=true)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := opts.Workspace
			if workspace != "" {
				ws = workspace
			}
			raw, err := os.ReadFile(filepath.Join(ws, ".mulix", "bus", "endpoint"))
			if err != nil {
				return fmt.Errorf("events: 总线未启用(endpoint 缺席——会话侧需 [eventbus] enable=true): %w", err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return eventbus.ObserveMirrors(ctx, strings.TrimSpace(string(raw)), os.Stdout)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "工作区(缺省=当前目录)")
	return cmd
}
```

(root.AddCommand(newEventsCommand(opts)) 与既有命令同处登记;import 以现状为准补 `os/filepath/signal/syscall/eventbus`——注意 root.go 现有 import 名冲突时用别名。endpoint 路径与 sharedEventBus 的 StoreDir 一致性由实现者对照 `<ws>/.mulix/bus` 核验。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ ./internal/cli/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: 真机冒烟(FR-108 惯例)**

工作区临时启用 `[eventbus] enable=true`,跑一轮 + `mulix events` 观察收行,Ctrl-C 干净退出;证据(引擎版本/工作区/时间线/退出码/摘录)落 `docs/evidence/018-events-smoke.md`。环境受阻如实记录,不得以自动化测试冒充。

- [ ] **Step 6: Commit**

```bash
git add internal/eventbus/ internal/cli/root.go docs/evidence/018-events-smoke.md
git commit -m "feat(cli,eventbus): mulix events 观察面——首个总线订阅消费者(018 FR-103;P4c T4)"
```
