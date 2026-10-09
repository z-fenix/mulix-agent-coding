package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

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
