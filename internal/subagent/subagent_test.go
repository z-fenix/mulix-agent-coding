package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/event"
)

type stubHandle struct {
	id      string
	done    chan struct{}
	err     error
	summary string
	tokens  int64
	cents   int64
	mu      sync.Mutex
	cancels int
}

func (h *stubHandle) ID() string            { return h.id }
func (h *stubHandle) Done() <-chan struct{} { return h.done }
func (h *stubHandle) Err() error            { return h.err }
func (h *stubHandle) Summary() string       { return h.summary }
func (h *stubHandle) TotalTokens() int64    { return h.tokens }
func (h *stubHandle) CostUSDCents() int64   { return h.cents }
func (h *stubHandle) Cancel(ctx context.Context) error {
	h.mu.Lock()
	h.cancels++
	h.mu.Unlock()
	return nil
}
func (h *stubHandle) Close(ctx context.Context) error { return nil }

func newTestManager(t *testing.T, factory SessionFactory, publish func(any)) *Manager {
	t.Helper()
	m, err := NewManager(Options{Root: t.TempDir(), Factory: factory, Publish: publish})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	return m
}

func TestSpawnDepthLimit(t *testing.T) {
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return &stubHandle{id: "subX", done: make(chan struct{})}, nil
	}, func(any) {})
	_, err := m.Spawn(context.Background(), "p1", "task", 3) // depth==MaxDepth(默认3) → 拒绝
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth limit: %v", err)
	}
}

func TestSpawnConcurrentLimit(t *testing.T) {
	n := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		n++
		return &stubHandle{id: fmt.Sprintf("s%d", n), done: make(chan struct{})}, nil
	}, func(any) {})
	for i := 0; i < 4; i++ {
		if _, err := m.Spawn(context.Background(), "p", fmt.Sprintf("t%d", i), 0); err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
	}
	if _, err := m.Spawn(context.Background(), "p", "t5", 0); err == nil || !strings.Contains(err.Error(), "concurrent") {
		t.Fatalf("concurrency limit: %v", err)
	}
}

func TestPollRecordsCompletionAndPublishes(t *testing.T) {
	var mu sync.Mutex
	var pub []any
	h := &stubHandle{id: "sub1", done: make(chan struct{}), summary: "did it", tokens: 120, cents: 9}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) { mu.Lock(); pub = append(pub, ev); mu.Unlock() })
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if st, _ := m.Poll(context.Background(), "sub1"); st != StatusRunning {
		t.Fatalf("pre status = %v", st)
	}
	close(h.done) // 模拟子回合收口
	if st, err := m.Poll(context.Background(), "sub1"); err != nil || st != StatusCompleted {
		t.Fatalf("poll: %v %v", st, err)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range pub {
		if c, ok := ev.(event.DelegationCompletedEvent); ok && c.SubSessionID == "sub1" && c.TotalTokens == 120 {
			found = true
		}
	}
	if !found {
		t.Fatalf("DelegationCompleted not published: %v", pub)
	}
}

func TestCancelClosesSubcleanly(t *testing.T) {
	var mu sync.Mutex
	var pub []any
	h := &stubHandle{id: "sub1", done: make(chan struct{})}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) { mu.Lock(); pub = append(pub, ev); mu.Unlock() })
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := m.Cancel(context.Background(), "sub1", "user"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if st, _ := m.Poll(context.Background(), "sub1"); st != StatusCancelled {
		t.Fatalf("status = %v, want cancelled", st)
	}
	h.mu.Lock()
	cancels := h.cancels
	h.mu.Unlock()
	if cancels != 1 {
		t.Fatalf("handle cancels = %d", cancels)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range pub {
		if c, ok := ev.(event.DelegationCancelledEvent); ok && c.SubSessionID == "sub1" && c.Reason == "user" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DelegationCancelled not published: %v", pub)
	}
}

func TestCloseCancelsAllRunning(t *testing.T) {
	hs := []*stubHandle{
		{id: "s1", done: make(chan struct{})},
		{id: "s2", done: make(chan struct{})},
	}
	i := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		i++
		return hs[i-1], nil
	}, func(any) {})
	for k := 0; k < 2; k++ {
		if _, err := m.Spawn(context.Background(), "p", fmt.Sprintf("t%d", k), 0); err != nil {
			t.Fatalf("spawn %d: %v", k, err)
		}
	}
	m.Close(context.Background())
	for _, h := range hs {
		h.mu.Lock()
		c := h.cancels
		h.mu.Unlock()
		if c != 1 {
			t.Fatalf("handle %s cancels = %d, want 1", h.id, c)
		}
	}
	for _, id := range []string{"s1", "s2"} {
		if st, _ := m.Poll(context.Background(), id); st != StatusCancelled {
			t.Fatalf("%s status = %v", id, st)
		}
	}
}

// --- 以下为任务说明钉死语义的补充测试（NewManager 必填校验 / unknown id / failed
// --- 分支 / 首个终态获胜 / List 值拷贝 / Seq 共享计数器从 1 起）。

func TestNewManagerRequiresOptions(t *testing.T) {
	factory := func(ctx context.Context, task string) (SessionHandle, error) { return nil, nil }
	if _, err := NewManager(Options{Factory: factory, Publish: func(any) {}}); err == nil {
		t.Fatal("Root required: want error")
	}
	if _, err := NewManager(Options{Root: "r", Publish: func(any) {}}); err == nil {
		t.Fatal("Factory required: want error")
	}
	if _, err := NewManager(Options{Root: "r", Factory: factory}); err == nil {
		t.Fatal("Publish required: want error")
	}
}

func TestPollAndCancelUnknownID(t *testing.T) {
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return &stubHandle{id: "subX", done: make(chan struct{})}, nil
	}, func(any) {})
	if _, err := m.Poll(context.Background(), "ghost"); err == nil {
		t.Fatal("poll unknown id: want error")
	}
	if err := m.Cancel(context.Background(), "ghost", "r"); err == nil {
		t.Fatal("cancel unknown id: want error")
	}
}

func TestCancelOnlyRunning(t *testing.T) {
	h := &stubHandle{id: "sub1", done: make(chan struct{})}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(any) {})
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	close(h.done)
	if st, err := m.Poll(context.Background(), "sub1"); err != nil || st != StatusCompleted {
		t.Fatalf("poll: %v %v", st, err)
	}
	if err := m.Cancel(context.Background(), "sub1", "late"); err == nil {
		t.Fatal("cancel on completed sub: want error（Cancel running-only）")
	}
}

func TestPollRecordsFailureAndPublishes(t *testing.T) {
	var mu sync.Mutex
	var pub []any
	h := &stubHandle{id: "sub1", done: make(chan struct{}), err: fmt.Errorf("engine boom")}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) { mu.Lock(); pub = append(pub, ev); mu.Unlock() })
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	close(h.done)
	if st, err := m.Poll(context.Background(), "sub1"); err != nil || st != StatusFailed {
		t.Fatalf("poll: %v %v", st, err)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range pub {
		if f, ok := ev.(event.DelegationFailedEvent); ok && f.SubSessionID == "sub1" && f.Reason == "engine boom" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DelegationFailed not published: %v", pub)
	}
}

func TestFirstTerminalStatusWins(t *testing.T) {
	var mu sync.Mutex
	var pub []any
	h := &stubHandle{id: "sub1", done: make(chan struct{})}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) { mu.Lock(); pub = append(pub, ev); mu.Unlock() })
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := m.Cancel(context.Background(), "sub1", "user"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(h.done) // 终态 goroutine 迟到：首个终态（cancelled）不得被覆盖
	if st, _ := m.Poll(context.Background(), "sub1"); st != StatusCancelled {
		t.Fatalf("status = %v, want cancelled（首个终态获胜）", st)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range pub {
		if _, ok := ev.(event.DelegationCompletedEvent); ok {
			t.Fatalf("DelegationCompleted published after cancel: %v", pub)
		}
	}
}

func TestListReturnsSafeCopies(t *testing.T) {
	hs := []*stubHandle{
		{id: "s1", done: make(chan struct{})},
		{id: "s2", done: make(chan struct{})},
	}
	i := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		i++
		return hs[i-1], nil
	}, func(any) {})
	if _, err := m.Spawn(context.Background(), "p1", "t0", 0); err != nil {
		t.Fatalf("spawn 0: %v", err)
	}
	if _, err := m.Spawn(context.Background(), "p2", "t1", 1); err != nil {
		t.Fatalf("spawn 1: %v", err)
	}
	infos := m.List()
	if len(infos) != 2 {
		t.Fatalf("len = %d, want 2", len(infos))
	}
	want := []Info{
		{SubSessionID: "s1", ParentID: "p1", Task: "t0", Status: StatusRunning, Depth: 0},
		{SubSessionID: "s2", ParentID: "p2", Task: "t1", Status: StatusRunning, Depth: 1},
	}
	for k, w := range want {
		if infos[k] != w {
			t.Fatalf("info[%d] = %+v, want %+v", k, infos[k], w)
		}
	}
	infos[0].Status = "hacked" // 修改返回切片不得影响 Manager 内部状态
	if again := m.List(); again[0].Status != StatusRunning {
		t.Fatalf("manager state mutated via List copy: %+v", again[0])
	}
}

func TestDelegationEventsShareSeqStartingAtOne(t *testing.T) {
	var mu sync.Mutex
	var seqs []int64
	h := &stubHandle{id: "sub1", done: make(chan struct{})}
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		return h, nil
	}, func(ev any) {
		mu.Lock()
		defer mu.Unlock()
		switch e := ev.(type) {
		case event.DelegationStartedEvent:
			seqs = append(seqs, e.Seq)
		case event.DelegationCompletedEvent:
			seqs = append(seqs, e.Seq)
		}
	})
	if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	close(h.done)
	if _, err := m.Poll(context.Background(), "sub1"); err != nil { // 同步收口
		t.Fatalf("poll: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Fatalf("seqs = %v, want [1 2]（内部计数器从 1 起、全部委派事件共享）", seqs)
	}
}

// 并发 Spawn 压力（评审修复轮 1）：10 个 goroutine 同时 Spawn（MaxConcurrent=默认 4）。
// 工厂内设 barrier——全部 10 个涌入工厂或 500ms 超时才放行：旧实现（检查后即释放锁、
// 无名额预约）在放行前不可能有登记完成，故全部 10 个都通过检查 → 确定性超发；
// 正确实现必须恰好 4 成功 + 6 个含 "concurrent" 的错误，List 不超过 4 个 running。
func TestSpawnConcurrentLimitUnderParallelSpawns(t *testing.T) {
	const workers = 10
	var (
		fmu       sync.Mutex
		inFactory int
		release   = make(chan struct{})
		idmu      sync.Mutex
		n         int
	)
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		fmu.Lock()
		inFactory++
		if inFactory == workers {
			close(release) // 全部涌入才放行（名额预约正确时到不了这个数，走超时分支）
		}
		fmu.Unlock()
		select {
		case <-release:
		case <-time.After(500 * time.Millisecond):
		}
		idmu.Lock()
		n++
		h := &stubHandle{id: fmt.Sprintf("s%d", n), done: make(chan struct{})}
		idmu.Unlock()
		return h, nil
	}, func(any) {})

	var (
		wg             sync.WaitGroup
		resmu          sync.Mutex
		successes      int
		concurrentErrs int
		otherErrs      int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Spawn(context.Background(), "p", "task", 0)
			resmu.Lock()
			defer resmu.Unlock()
			switch {
			case err == nil:
				successes++
			case strings.Contains(err.Error(), "concurrent"):
				concurrentErrs++
			default:
				otherErrs++
			}
		}()
	}
	wg.Wait()
	if successes != 4 {
		t.Fatalf("successes = %d, want 4（检查与登记之间的窗口不得超发）", successes)
	}
	if concurrentErrs != workers-4 || otherErrs != 0 {
		t.Fatalf("concurrent errs = %d, want %d; other errs = %d, want 0", concurrentErrs, workers-4, otherErrs)
	}
	running := 0
	for _, info := range m.List() {
		if info.Status == StatusRunning {
			running++
		}
	}
	if running != 4 {
		t.Fatalf("running = %d, want 4（List 不得显示超过 MaxConcurrent）", running)
	}
}

// TestManagerSummaryAccessor 钉 Summary 访问器（Task 3 裁定：poll 应答
// summary 的来源，FR-102 最终消息回收）：completed/failed 终态 →
// (handle.Summary(), true)；running → ("", false)；unknown id → ("", false)。
func TestManagerSummaryAccessor(t *testing.T) {
	h := &stubHandle{id: "sub-ok", done: make(chan struct{}), summary: "最终结论"}
	fh := &stubHandle{id: "sub-fail", done: make(chan struct{}), summary: "半程产出", err: fmt.Errorf("boom")}
	i := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		i++
		if i == 1 {
			return h, nil
		}
		return fh, nil
	}, func(any) {})
	for _, id := range []string{"sub-ok", "sub-fail"} {
		if _, err := m.Spawn(context.Background(), "p", "task", 0); err != nil {
			t.Fatalf("spawn %s: %v", id, err)
		}
	}
	// running → ("", false)
	if s, ok := m.Summary("sub-ok"); ok || s != "" {
		t.Fatalf("running summary = (%q, %v), want (\"\", false)", s, ok)
	}
	// unknown id → ("", false)
	if s, ok := m.Summary("ghost"); ok || s != "" {
		t.Fatalf("unknown summary = (%q, %v), want (\"\", false)", s, ok)
	}
	// completed → (summary, true)
	close(h.done)
	if st, err := m.Poll(context.Background(), "sub-ok"); err != nil || st != StatusCompleted {
		t.Fatalf("poll completed: %v %v", st, err)
	}
	if s, ok := m.Summary("sub-ok"); !ok || s != "最终结论" {
		t.Fatalf("completed summary = (%q, %v), want (最终结论, true)", s, ok)
	}
	// failed → (summary, true)——终态同样回收最终消息
	close(fh.done)
	if st, err := m.Poll(context.Background(), "sub-fail"); err != nil || st != StatusFailed {
		t.Fatalf("poll failed: %v %v", st, err)
	}
	if s, ok := m.Summary("sub-fail"); !ok || s != "半程产出" {
		t.Fatalf("failed summary = (%q, %v), want (半程产出, true)", s, ok)
	}
}

// 自环拒绝（评审修复轮 1，可达判环路径）：工厂返回的句柄 id == parentID 即委派环，
// 必须在登记前拒绝；被拒的 spawn 不得留下登记项、不得泄漏并发名额（随后仍可
// 正常 Spawn 到默认限额 4，第 5 个被 "concurrent" 拒绝）。
func TestSpawnRejectsSelfParentCycle(t *testing.T) {
	n := 0
	m := newTestManager(t, func(ctx context.Context, task string) (SessionHandle, error) {
		n++
		if n == 1 {
			return &stubHandle{id: "sub1", done: make(chan struct{})}, nil // id == parentID：自环
		}
		return &stubHandle{id: fmt.Sprintf("s%d", n), done: make(chan struct{})}, nil
	}, func(any) {})

	if _, err := m.Spawn(context.Background(), "sub1", "task", 0); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("self-parent spawn err = %v, want cycle error", err)
	}
	if infos := m.List(); len(infos) != 0 {
		t.Fatalf("rejected spawn left registry entries: %+v", infos)
	}
	for i := 0; i < 4; i++ {
		if _, err := m.Spawn(context.Background(), "p", fmt.Sprintf("t%d", i), 0); err != nil {
			t.Fatalf("spawn %d after self-loop rejection: %v", i, err)
		}
	}
	if _, err := m.Spawn(context.Background(), "p", "t5", 0); err == nil || !strings.Contains(err.Error(), "concurrent") {
		t.Fatalf("5th spawn should hit concurrent limit: %v", err)
	}
}
