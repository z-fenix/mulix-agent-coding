# 019 观察面与桥健壮性 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 018 终审 5 项 deferred minor + 016 路由 keys_source 清偿:桥失败可见、观察面断连错退、rune 截断、endpoint 收尾清理、toml 文案指路、keys_source 双形态。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/019-eventbus-observation-robustness/specs/2026-10-13-observation-design.md`)①sharedEventBus swallow 前经 `o.stderr()`(root.go:42 既有缝)透一行;②ObserveMirrors 增 `nats.NoReconnect()`+ClosedHandler→错退(Ctrl-C 路径 nil 不变);③observe rune 截断/Bus 增 storeDir 字段+Close 删 endpoint/Enabled 文案指路;④kernel_inspect JSON 增 `keys_source`(`"env"`/`"degraded"`)。

**Tech Stack:** Go 标准库 + nats.go(既有)。

**Spec:** `docs/specs/019-eventbus-observation-robustness/spec.md`

**Design:** `.mulix/.runtime/019-eventbus-observation-robustness/specs/2026-10-13-observation-design.md`

## Global Constraints

- FR-105 / SC-005:每任务 `go test ./... -count=1` 全绿再 commit;vet/gofmt/lint layers 干净;零第三方新增。
- 018 语义不回退:Ctrl-C 干净退出(nil)、观察面纯订阅零写入、endpoint 原子写、缺省门控关——全部保持。
- 012 裁定不破:桥失败仍「桥关闭」(openSession 失败=桥关),只补可见性。

## Review Focus

- 断连钉真实性:观察中关闭对端 server→ObserveMirrors 返回非 nil(NoReconnect 生效——若自动重连仍开,钉会超时假绿)。
- Ctrl-C 路径回归:ctx cancel→nil 既有钉(TestObserveMirrors)不回退。
- Close 删 endpoint 幂等:Close 两次/endpoint 已不存在不报错;OpenBus 重启重写不受影响。
- rune 截断:多字节字符边界安全(utf8.ValidString)。
- stderr 透出行不污染 stdout 协议面(serve 进程 stdout 是 MCP 传输)。

---

## Task 1: eventbus 包四件——断连退出/rune 截断/endpoint 收尾/文案

**Files:**
- Modify: `internal/eventbus/observe.go`(NoReconnect+ClosedHandler;rune 截断)
- Modify: `internal/eventbus/eventbus.go`(Bus.storeDir 字段+Close 删 endpoint)
- Modify: `internal/config/config.go`(Enabled 文案)
- Test: `internal/eventbus/eventbus_test.go`(追加×3)
- Test: `internal/config/config_test.go`(文案断言追加)

**Interfaces:**
- Consumes: 018 ObserveMirrors/OpenBus/Enabled 现形态。
- Produces: ObserveMirrors 对端关闭→非 nil 错误;Bus.Close() 后 endpoint 不存在;截断 rune 安全;文案含 `enable = "true"`。

- [ ] **Step 1: 写失败测试**

`internal/eventbus/eventbus_test.go` 追加:

```go
// TestObserveMirrorsPeerClosed 钉 019 FR-102:对端(会话进程)关闭→观察面
// 报错退出(NoReconnect——禁自动重连,静默冻结窗口关闭)。
func TestObserveMirrorsPeerClosed(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- ObserveMirrors(context.Background(), strings.TrimSpace(string(raw)), io.Discard) }()
	time.Sleep(200 * time.Millisecond) // 订阅就绪
	b.Close()                          // 对端死亡
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("peer closed must fail-visible (非 nil), 不允许静默冻结")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("observe frozen after peer close (NoReconnect 未生效?)")
	}
}

// TestBusCloseRemovesEndpoint 钉 019 FR-103:进程收尾清理——Close 后
// endpoint 不存在(观察面不再读到死端点);幂等(Close 二次不报错)。
func TestBusCloseRemovesEndpoint(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBus(BusOptions{StoreDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "endpoint")); err != nil {
		t.Fatalf("endpoint must exist before close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "endpoint")); !os.IsNotExist(err) {
		t.Fatalf("endpoint after close = %v, want NotExist", err)
	}
	if err := b.Close(); err != nil { // 幂等
		t.Fatalf("second close: %v", err)
	}
}

// TestObserveSummaryRuneSafe 钉 019 FR-103:多字节字符截断不 mojibake
// (rune 边界——200 字节处劈开中文不再产生非法 UTF-8)。
func TestObserveSummaryRuneSafe(t *testing.T) {
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
	var out syncBuffer
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- ObserveMirrors(ctx, strings.TrimSpace(string(raw)), &out) }()
	time.Sleep(200 * time.Millisecond)
	long := strings.Repeat("好", 300) // 900 字节——触发 200 字节截断
	if err := b.DeclareEvents("mirror.t"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := b.Publish("mirror.t", []byte(long)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !utf8.ValidString(lastLine(out.String())) {
		time.Sleep(20 * time.Millisecond)
	}
	line := lastLine(out.String())
	if !utf8.ValidString(line) {
		t.Fatalf("truncated line not valid UTF-8 (字节劈开多字节字符): %q", line)
	}
	// 截断本身也钉(评审 Advisory):行 payload 部分不得超 200 字节上限
	// (66 个完整「好」=198 字节——「截断含完整尾字符」)。
	if !strings.Contains(line, strings.Repeat("好", 66)) || strings.Contains(line, strings.Repeat("好", 67)) {
		t.Fatalf("truncation boundary wrong: %q", line)
	}
	stop()
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
```

(import 追加 `unicode/utf8`。)

`internal/config/config_test.go` TestEventbusEnabled 的 wantEr 分支断言追加文案指路:

```go
			if err == nil || !strings.Contains(err.Error(), tc.raw) || !strings.Contains(err.Error(), `enable = "true"`) {
```

- [ ] **Step 2: 跑测试验证失败**

Run(逐包分开跑——`&&` 会短路掉第二个 RED,TDD 要求亲眼看每个失败):

Run: `go test ./internal/eventbus/ -run "TestObserveMirrorsPeerClosed|TestBusCloseRemovesEndpoint|TestObserveSummaryRuneSafe" -count=1`
Expected: PeerClosed FAIL(3s 超时——静默冻结实证)/CloseRemovesEndpoint FAIL(endpoint 仍在)/RuneSafe FAIL(300×「好」=900 字节,现 `[:200]` 劈第 67 字中间→非法 UTF-8)。

Run: `go test ./internal/config/ -run TestEventbusEnabled -count=1`
Expected: FAIL(文案缺指路)。

- [ ] **Step 3: 实现**

`observe.go`:

```go
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second), nats.NoReconnect())
	if err != nil {
		return fmt.Errorf("events: connect %s: %w", url, err)
	}
	defer nc.Close()
	// 断连可见(019 FR-102):对端(单会话进程)死亡→ClosedHandler 关 channel
	// →错退 fail-visible;用户 Ctrl-C(ctx)路径保持 nil(018 语义不变)。
	// nats.go 签名:ConnHandler = func(*Conn)(无 err 参数、无返回值——评审修正)。
	closed := make(chan struct{})
	nc.SetClosedHandler(func(_ *nats.Conn) {
		close(closed) // nats.go isClosed 守卫——每连接恰好触发一次,无双调用
	})
```

截断改 rune 边界:

```go
		summary := strings.ReplaceAll(string(m.Data), "\n", " ")
		if len(summary) > 200 {
			summary = truncateRunes(summary, 200) // rune 边界安全(019 FR-103)
		}
```

```go
// truncateRunes 按字节上限截断但落在 rune 边界上(019 FR-103)。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	out := 0
	i := 0
	for ; i < len(r); i++ {
		out += len(string(r[i]))
		if out > max {
			break
		}
	}
	return string(r[:i])
}
```

closed 等待(select 扩展):

```go
	select {
	case <-ctx.Done():
		return nil
	case <-closed:
		return fmt.Errorf("events: 连接已关闭(对端总线不可达——会话进程可能已退出)")
	}
```

`eventbus.go`:Bus 增字段 `storeDir string`(OpenBus 赋 `storeDir: opts.StoreDir`);Close() 追加:

```go
	// endpoint 收尾清理(019 FR-103):进程收尾删握手文件——观察面不再读到
	// 死端点;幂等(不存在=成功),失败忽略(收尾清理不得阻塞关停)。
	_ = os.Remove(filepath.Join(b.storeDir, "endpoint"))
```

`config.go` Enabled() default 分支:

```go
		return false, fmt.Errorf("config: eventbus.enable %q must be true or false (TOML 字符串形态: enable = \"true\")", e.RawEnable)
```

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/eventbus/ ./internal/config/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: PASS/全绿(既有 TestObserveMirrors Ctrl-C nil 路径不回退)。

- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/ internal/config/
git commit -m "fix(eventbus,config): 观察面断连错退+rune 截断+endpoint 收尾+文案指路(019 FR-102/103;T1)"
```

---

## Task 2: keys_source + 桥失败可见 + 回归

**Files:**
- Modify: `internal/tools/mulixkernel/inspect.go`(keys_source 字段)
- Modify: `internal/cli/session.go`(sharedEventBus stderr 透出)
- Test: `internal/tools/mulixkernel/mulixkernel_test.go`(双形态断言)
- Test: `internal/cli/*_test.go`(桥失败 stderr 钉——注入点核验后落位)

**Interfaces:**
- Consumes: Task 1 无强依赖(并行安全,聚合提交)。
- Produces: kernel_inspect JSON 增 `keys_source`;sharedEventBus 失败→stderr 一行。

- [ ] **Step 1: 写失败测试**

`internal/tools/mulixkernel/mulixkernel_test.go` TestKernelInspectTool 追加断言(rep struct 增 `KeysSource string \`json:"keys_source"\``):

```go
	if rep.KeysSource != "env" {
		t.Fatalf("keys_source = %q, want env", rep.KeysSource)
	}
```

降级分支(keys=nil)断言:

```go
	// env 缺席形态:keys_source=degraded(016 P6+ 路由核销——区分真无键与桥缺席)。
	out, err = empty.Execute(context.Background(), json.RawMessage(`{}`))
	// …解包后断言 rep.KeysSource == "degraded"
```

`internal/cli` 侧:构造 `Options{Stderr: &buf, Workspace: <快速失败注入路径>}` 同包直调 sharedEventBus。**快速注入形态(评审修正,毫秒级)**:先建 `<ws>/.mulix/bus/endpoint` 为**目录**(JetStream 用 `bus/jetstream` 子目录不冲突)→ server 正常起 → OpenBus 的 `os.Rename(endpoint.tmp, endpoint)` 对目录即败 → OpenBus 报错(每跑 10s 的文件路径注入形态弃用)→断言 buf 含「桥关闭」。

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/tools/mulixkernel/ ./internal/cli/ -count=1`
Expected: FAIL(keys_source 字段空/sharedEventBus 无 stderr 输出)。

- [ ] **Step 3: 实现**

`inspect.go` Execute:keys_source 判定(nil→degraded):

```go
	// keys_source(019 FR-104;016 P6+ 路由核销):区分「真无服务键」与
	// 「桥缺席降级」——运维排障两态表象相同(services 恒空)。
	keysSource := "env"
	if len(t.keys) == 0 {
		keysSource = "degraded"
	}
```

(JSON struct 增 `KeysSource string \`json:"keys_source"\``;**注意**:rep 现为位置值复合字面量 `{svc, string(t.profile), toolNames, ptcRegistered, 0}`——必须同步加第六值,或顺手改 keyed 字段(评审 Minor,编译器会响亮拦截)。)

`session.go` sharedEventBus 失败分支:

```go
	if err != nil {
		// 019 FR-101:swallow 前透出一行(012「桥关闭如实记录」字面落实;
		// stdout 是 serve/MCP 协议面——只写 stderr 不污染)。
		_, _ = fmt.Fprintf(o.stderr(), "eventbus: 桥关闭(OpenBus 失败): %v\n", err)
		return nil
	}
```

- [ ] **Step 4: 跑测试验证通过 + 全矩阵回归**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers`
Expected: 全绿/干净/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/tools/mulixkernel/ internal/cli/
git commit -m "fix(mulixkernel,cli): keys_source 双形态+桥失败 stderr 可见(019 FR-101/104;T2)"
```
