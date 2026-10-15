# T11-1 单飞裁决异常韧性与有界化 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 单飞 leader 评估期 panic 不再悬挂等待方(fail-closed 唤醒 + 在飞清理);裁决缓存表随回合收口有界回收;正常路径 exactly-once 语义零回归。

**Architecture:**(bounded,设计于 chat 并已批准)①`rulingFor` 的 leader 评估段(发布 request → SetAlias → Gate.Evaluate)抽入带 `defer recover` 的评估函数:panic 时写 fail-closed 兜底裁决(deny,`singleflight: leader panic` 留痕)→ 同临界区 close 广播 + delete 在飞 → 发布 warning Notice → leader 以兜底裁决正常返回(follower 从缓存读取,逻辑零改动);②`gateRuling` 增加所属回合号,`onResult` 收口时删除 `turn ≤ 收口回合` 的条目(收口即该回合审批全部裁决完毕,在飞表不动)。

**Tech Stack:** Go 标准库(sync/panic-recover);既有测试基建(`newTestControllerWithPolicyRules`、`postCallbackOnce`、`countApprovalEvents`)。

**Spec:** `docs/specs/004-t11-1-leader-panic-resolved/spec.md`

**Design:** approved in chat (bounded) —— 要点:①leader 评估段 `defer` 异常收口(兜底裁决 + 广播 + 清理 + Notice);②裁决条目关联回合号 + `onResult` 回收;③正常路径逐字节保持(语句顺序不变,仅换壳);④panic 注入经 `Options.EvaluateProbe` 测试钩子(`Options.Gate` 是具体类型,无接口可换;nil=无操作,生产零影响)。

## Global Constraints

- FR-104:正常路径逐字节保持——恰一次 approval_request、恰一次 approval_resolved(仅 leader 发布)、等待方复用同一裁决。
- FR-105:异常兜底裁决恒为 deny + 既有 timeout/兜底词表语义,异常路径镜像如实可见,不得静默吞异常或异常放行。
- FR-107:评估顺序、allow 清单、预设矩阵、审批人交互、别名配对不变量全部不动。
- spec Edge Cases:回合收口后重发同 ID → 重新评估(fail-closed 优先于缓存复用);异常路径的镜像可能只有 resolved 无对应 request,如实留痕不补发。
- 测试纪律:TDD(先红后绿);每任务结束 `go test ./... -count=1` 全绿再 commit。
- 依赖纪律:仅 Go 标准库;不把 Gate 改为接口、不重构单飞架构。

## Review Focus

- panic 发生在 approval_request **发布前**(EvaluateProbe 在 publish 前注入):等待方仍被唤醒、镜像可能只有 resolved 无 request——如实留痕,不补发不伪装。Task 1 子测试钉。
- 兜底裁决的一致性:所有等待方拿到**同一条**兜底裁决(从缓存读,不是各自生成)。Task 1 断言全员 behavior+message 一致。
- 清理不误伤进行中审批:pending 审批(回合未收口)条目在收口前必须可见。Task 2 断言收口前 len>0、收口后 len==0。
- 正常路径零回归:既有 `TestPermissionCallbackSingleflightConcurrent`(20 goroutine exactly-once)与 `TestPermissionCallbackClosedSessionDenies` 照旧全绿。Task 1/2 全量门禁钉。
- 兜底裁决不入缓存:panic 兜底裁决只经 leader 发布 resolved,不写缓存表(天然有界);同 ID 新请求走重新评估。Task 1 子测试(TestLeaderPanicThenFreshRequest)+ Task 2 turn-2 断言钉。

---

## Task 1: leader panic 韧性(defer 异常收口)

**Files:**
- Modify: `internal/control/controller.go:1474-1522`(`rulingFor` 评估段)、`internal/control/controller.go:56-80`(`Options` 增钩子字段)、`internal/control/controller.go:920-924`(`gateRuling` 增回合号字段——Task 2 消费,本任务仅加字段)
- Test: Create `internal/control/leader_panic_test.go`

**Interfaces:**
- Consumes: 既有 `newTestControllerWithPolicyRules` / `postCallbackOnce` / `countApprovalEvents`(singleflight_test.go);`Options` 构造(controller_test.go:116-152)。
- Produces: ①`Options.EvaluateProbe func(tool string, input json.RawMessage)`——leader 评估前调用,nil=无操作(测试注入点;panic 从中传播即被收口);②`gateRuling{decision, source, resolvedBy, turn int64}`(turn 字段本任务只加不消费);③`rulingFor` 对外签名与语义不变。

- [ ] **Step 1: 写失败测试**

创建 `internal/control/leader_panic_test.go`(import:`bytes/context/encoding/json/net/http/httptest/sync/testing/time` + `mulix/internal/event`):

```go
package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLeaderPanicWakeFollowers 钉 FR-101/102/105:单飞 leader 评估期 panic
// 时,等待方在有限时间内被唤醒且拿到一致的 fail-closed 裁决;在飞条目被
// 清理;异常路径镜像如实留痕(Notice);resolved 恰一次(leader 兜底发布)。
func TestLeaderPanicWakeFollowers(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		panic("gate exploded (test injection)")
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()

	const n = 20
	const toolUseID = "tu-panic-fanout"
	answers := make([]map[string]any, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			answers[i], errs[i] = postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, toolUseID)
		}(i)
	}
	close(start)
	wg.Wait()

	// 全员在超时内拿到 deny(现状缺陷:leader panic 后等待方悬挂到 ctx 超时)。
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: callback POST failed (panic must not hang waiters): %v", i, errs[i])
		}
		if answers[i]["behavior"] != "deny" {
			t.Fatalf("goroutine %d behavior = %v, want deny (fail-closed fallback)", i, answers[i]["behavior"])
		}
	}
	// 裁决一致性(决策层,FR-101):全员 deny;message 恰好 1 条 leader 兜底
	// (singleflight: leader panic——leader 自己的应答)与 n-1 条等待方防御
	// (singleflight: ruling missing)——两个来源都是 fail-closed,无放行。
	leaderMsg, waiterMsg := 0, 0
	for i := range n {
		m, _ := answers[i]["message"].(string)
		switch {
		case strings.Contains(m, "singleflight: leader panic"):
			leaderMsg++
		case strings.Contains(m, "singleflight: ruling missing"):
			waiterMsg++
		default:
			t.Fatalf("goroutine %d unexpected message %q", i, m)
		}
	}
	if leaderMsg != 1 || waiterMsg != n-1 {
		t.Fatalf("messages: leader=%d waiter=%d, want 1/%d (fallback annotate + defensive deny)", leaderMsg, waiterMsg, n-1)
	}
	// resolved 恰一次(leader 兜底发布);request 可能缺(panic 在发布前)——如实。
	_, resolved := countApprovalEvents(t, c, toolUseID)
	if resolved != 1 {
		t.Fatalf("approval_resolved count = %d, want 1 (leader fallback publishes once)", resolved)
	}
	// 在飞条目清理(FR-102)。
	c.mu.Lock()
	inflightLeft := len(c.inflight)
	c.mu.Unlock()
	if inflightLeft != 0 {
		t.Fatalf("inflight left = %d, want 0 (panic must clean up)", inflightLeft)
	}
	// 异常路径镜像留痕(FR-105):Notice 提及 leader panic。
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	sawNotice := false
	for _, ev := range mirror {
		if nEv, ok := ev.(event.NoticeEvent); ok && strings.Contains(nEv.Content, "leader panic") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("mirror must record a Notice mentioning leader panic (FR-105)")
	}
}
```

(文件 import:`context/encoding/json/net/http/httptest/strings/sync/testing/time` + `mulix/internal/event` + `mulix/internal/guard`(去掉模板里未用的 `bytes`)。注:本测试不断言 approval_request 计数——注入点在 publish 之前,镜像可能只有 resolved 无对应 request,spec Edge Case 如实留痕即可。)

再补一个子测试(同文件):

```go
// TestLeaderPanicThenFreshRequest 钉 FR-102/FR-103(Edge):panic 后同 ID 的
// 新请求不被悬挂状态阻塞——关闭钩子后重新评估并得到新裁决。
func TestLeaderPanicThenFreshRequest(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	probeOn := true
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		if probeOn {
			panic("gate exploded (test injection)")
		}
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ans, err := postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-panic-1")
	if err != nil {
		t.Fatalf("first (panicking) POST must still answer: %v", err)
	}
	if ans["behavior"] != "deny" {
		t.Fatalf("panicked ruling = %v, want deny", ans["behavior"])
	}
	probeOn = false // 新评估不再 panic → allow 规则生效
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	ans2, err := postCallbackOnce(ctx2, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-panic-1")
	if err != nil {
		t.Fatalf("fresh POST after panic must re-evaluate (not hang): %v", err)
	}
	if ans2["behavior"] != "allow" {
		t.Fatalf("fresh ruling = %v, want allow (re-evaluation)", ans2["behavior"])
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run 'TestLeaderPanic' -count=1 -v`
Expected: FAIL——`TestLeaderPanicWakeFollowers` 有 goroutine 报 `callback POST failed`(leader 自身连接被重置/EOF——net/http 恢复 handler panic;follower 悬挂到 `context deadline exceeded`)或 `inflight left = 1, want 0`;`TestLeaderPanicThenFreshRequest` 在第二次 POST 超时。若编译错误(字段不存在),先只加 `Options.EvaluateProbe` 空字段与 `gateRuling.turn` 字段使其编译,再验证运行期 RED。

- [ ] **Step 3: 最小实现**

`internal/control/controller.go` 三处:

(a) `Options` 结构体(`Gate` 字段后)加:

```go
	// EvaluateProbe 是 leader 评估前的观察钩子(nil=无操作):测试注入点
	// (T11-① FR-106)——从中 panic 传播即模拟 leader 评估期异常;生产恒 nil。
	EvaluateProbe func(tool string, input json.RawMessage)
```

(b) `gateRuling` 增字段(Task 2 消费):

```go
	turn int64 // 裁决所属回合(收口回收口径;0=异常兜底未关联回合)
```

(c) `rulingFor` 的 leader 评估段(controller.go:1496-1511 的 publish+approveMu+Evaluate 块)抽为方法并加 defer 收口;leader 段改为:

```go
	ch := make(chan struct{})
	c.inflight[toolUseID] = ch
	c.mu.Unlock()
	ruling, panicked := c.evaluateRuling(toolUseID, tool, input)
	c.mu.Lock()
	if !panicked {
		// panic 收口不写缓存:等待方经「ruling missing」防御路径拿到一致
		// deny;同 ID 新请求缓存未命中 → 新 leader 重新评估(FR-102 字面)。
		c.resolved[toolUseID] = ruling
	}
	if c.inflight[toolUseID] == ch {
		close(ch) // 广播:close 唤醒全部等待方(send 只能喂饱一个——P1 复审 N5)
		delete(c.inflight, toolUseID)
	}
	c.mu.Unlock()
	return ruling, true
```

新增方法(controller.go,紧邻 rulingFor):

```go
// evaluateRuling 是单飞 leader 的评估段(approval_request 发布 → 别名配对 →
// Gate.Evaluate),带 defer 异常收口(T11-① FR-101/102/105):评估 panic 时
// 以 fail-closed 兜底裁决返回并置 panicked=true,发布 Notice 留痕;缓存的
// 写入与在飞广播由 rulingFor 尾段统一完成(唯一 close 位点)——panicked 时
// 跳过缓存写,等待方经「ruling missing」防御 deny 唤醒,同 ID 后续请求按新
// 裁决重新评估。进程不悬挂、不崩溃,无「未经评估的放行」。正常路径语句
// 顺序与拆分前逐字一致(FR-104)。
func (c *Controller) evaluateRuling(toolUseID, tool string, input json.RawMessage) (ruling gateRuling, panicked bool) {
	turn := c.currentTurn()
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			ruling = gateRuling{decision: string(guard.DecisionDeny),
				source: string(guard.SourceTimeout), resolvedBy: "singleflight: leader panic", turn: turn}
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
				Content: fmt.Sprintf("审批裁决 leader panic(fail-closed 兜底): %v", r)})
		}
	}()
	if c.opts.EvaluateProbe != nil {
		c.opts.EvaluateProbe(tool, input) // 测试注入点:publish 之前(覆盖「镜像只有 resolved 无 request」的异常路径)
	}
	c.publish(event.ApprovalRequestEvent{Seq: c.nextSeq(), Turn: c.currentTurn(),
		ToolUseID: toolUseID, Tool: tool, Input: input})
	// 别名配对不变量:本函数是 Gate.Evaluate 的唯一生产调用点,且每次
	// Evaluate 前必须 SetAlias(终评审 Important-3c,语义原样)。
	c.approveMu.Lock()
	if c.opts.Approvals != nil {
		c.opts.Approvals.SetAlias(toolUseID)
	}
	decision, err := c.opts.Gate.Evaluate(context.Background(), tool, input)
	c.approveMu.Unlock()
	if err != nil { // 防御:Gate 约定 err 恒空,仍 fail-closed 兜底
		decision = guard.GateDecision{Decision: guard.DecisionDeny,
			Source: guard.SourceTimeout, ResolvedBy: "gate: " + err.Error()}
	}
	return gateRuling{decision: string(decision.Decision),
		source: string(decision.Source), resolvedBy: decision.ResolvedBy, turn: turn}
}
```

注意:①正常路径里原 `rulingFor` 中的 publish/approveMu/Evaluate 三段**原样搬入**,顺序不变(除新增的 probe 观察、nil=无操作外无任何语句增删);②panic 收口**不写缓存**:等待方经既有防御路径(「ruling missing」→ deny)一致 fail-closed,同 ID 新请求重新评估(FR-102 字面);③Notice 经 publish 落镜像(与既有 notice 同通路);④probe 在 publish 之前——panic 注入时镜像可能只有 resolved 无 request,与 spec Edge Case 一致;⑤缓存的写入与 close/delete 广播统一在 `rulingFor` 尾段(唯一 close 位点,panicked 时跳过缓存写)。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/control/ -run 'TestLeaderPanic' -count=1 -v`
Expected: PASS(2 个测试)

Run: `go test ./... -count=1`
Expected: 全绿(含 `TestPermissionCallbackSingleflightConcurrent` exactly-once、`TestPermissionCallbackClosedSessionDenies`、e2e)

Run: `go vet ./... && gofmt -l internal/`
Expected: 干净

- [ ] **Step 5: Commit**

```bash
git add internal/control/controller.go internal/control/leader_panic_test.go
git commit -m "fix(control): 单飞 leader panic 收口——等待方 fail-closed 唤醒+在飞清理(004 FR-101/102/105)"
```

---

## Task 2: resolved 裁决表有界化(回合收口回收)

**Files:**
- Modify: `internal/control/controller.go:1210-1260`(`onResult` 收口段)
- Test: `internal/control/leader_panic_test.go`(追加)

**Interfaces:**
- Consumes: Task 1 的 `gateRuling.turn` 字段与 `evaluateRuling`(兜底裁决已带 turn);既有 `onResult` 回合收口路径(此时 `c.turn` 即收口回合)。
- Produces: `onResult` 在持锁段清理 `resolved` 中 `turn ≤ 收口回合` 的条目;`inflight` 不动;对外无新接口。

- [ ] **Step 1: 写失败测试**

追加到 `internal/control/leader_panic_test.go`(直测 `evaluateCanUseTool` 与 `onResult`,同包白盒):

```go
// TestResolvedBoundedAcrossTurns 钉 FR-103:裁决缓存表随回合收口回收——
// 收口后条目数回落常数,不随历史工具调用数增长;进行中审批(收口前)不受影响。
func TestResolvedBoundedAcrossTurns(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	// 白盒前置:回合 1 进行中(onResult 只消费 running 会话的 result)。
	c.mu.Lock()
	c.running = true
	c.turn = 1
	c.mu.Unlock()

	// 回合 1:三次裁决(allow 规则命中,走正常 leader 路径)。
	c.evaluateCanUseTool("r1", "tu-b1", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`1`))
	c.evaluateCanUseTool("r2", "tu-b2", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`2`))
	c.evaluateCanUseTool("r3", "tu-b3", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`3`))

	c.mu.Lock()
	sizeAfterTurn1 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterTurn1 != 3 {
		t.Fatalf("resolved size after 3 rulings = %d, want 3 (in-flight turn entries present)", sizeAfterTurn1)
	}

	// 回合 1 收口 → 条目回收。
	c.onResult(engine.Event{Kind: engine.KindResult,
		Result: &engine.ResultInfo{SubType: "success", IsError: false, NumTurns: 1}})
	c.mu.Lock()
	sizeAfterClose1 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterClose1 != 0 {
		t.Fatalf("resolved size after turn close = %d, want 0 (bounded per FR-103)", sizeAfterClose1)
	}

	// 回合 2:panic 兜底裁决不入缓存(Task 1 设计)——只有正常裁决 tu-b4 在表;
	// 收口后 0 条(兜底天然有界,无豁免问题)。
	c.mu.Lock()
	c.running = true // onResult 首轮已置 false;不置位则回合 2 收口被 running guard 忽略
	c.turn = 2
	c.mu.Unlock()
	probeOn := true
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		if probeOn {
			panic("gate exploded (test injection)")
		}
	}
	c.evaluateCanUseTool("r4", "tu-p1", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`4`))
	probeOn = false
	c.evaluateCanUseTool("r5", "tu-b4", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`5`))
	c.mu.Lock()
	sizeAfterTurn2 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterTurn2 != 1 {
		t.Fatalf("resolved size in turn 2 = %d, want 1 (panic fallback never cached; normal entry present)", sizeAfterTurn2)
	}
	c.onResult(engine.Event{Kind: engine.KindResult,
		Result: &engine.ResultInfo{SubType: "success", IsError: false, NumTurns: 2}})
	c.mu.Lock()
	sizeAfterClose2 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterClose2 != 0 {
		t.Fatalf("resolved size after turn 2 close = %d, want 0 (fallback never cached, normal entry cleaned)", sizeAfterClose2)
	}
}
```

(文件 import 需补:`mulix/internal/engine`、`mulix/internal/guard`。`c.running`/`c.turn` 为同包白盒直写——锁内赋值,模拟回合进行/推进;若与既有回合推进基建冲突,以能驱动 `onResult` 为准,断言不变。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run TestResolvedBoundedAcrossTurns -count=1 -v`
Expected: FAIL with `resolved size after turn close = 3, want 0`(现状:resolved 只进不出)

- [ ] **Step 3: 最小实现**

`onResult` 的第一个持锁段(controller.go:1214-1223)内、`turn := c.turn` 之后加:

```go
		// 裁决缓存表有界化(T11-① FR-103):本回合全部审批在收口时已裁决
		// (引擎等许可后才执行工具),回合 ≤ 收口回合的条目不再被重发复用
		// 需要——回收。inflight 不动(pending 唤醒语义独立于缓存回收);
		// 收口后同 ID 重发按新请求重新评估(spec 假设:协议下不发生)。
		for id, r := range c.resolved {
			if r.turn <= turn {
				delete(c.resolved, id)
			}
		}
```

(同一 `c.mu` 临界区内完成,无新增锁序;`turn := c.turn` 已在锁内。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/control/ -run 'TestResolvedBoundedAcrossTurns|TestLeaderPanic' -count=1 -v`
Expected: PASS

Run: `go test ./... -count=1`
Expected: 全绿(exactly-once 矩阵、e2e、subagent 全链)

Run: `go vet ./... && gofmt -l internal/`
Expected: 干净

- [ ] **Step 5: Commit**

```bash
git add internal/control/controller.go internal/control/leader_panic_test.go
git commit -m "fix(control): resolved 裁决表回合收口回收——长会话内存有界(004 FR-103)"
```
