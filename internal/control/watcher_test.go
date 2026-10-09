package control

// M15/FR-109 前半：指令文件（CLAUDE.md/AGENTS.md）启动快照加载 + 变更轮询
// watcher + 提醒事件。测试矩阵 = brief Task 8 Step 1 ①–⑤，外加 start 序列
// 接线的结构钉（快照入控制器 / watcher 随 start 启动 / Close 收口）。
//
// Review Focus（计划 §Review Focus 第五条）钉在
// TestWatchInstructionsTouchNoNotice：mtime 变化而内容不变（touch）不得产生
// 提醒——比较依据是 sha256 而非 mtime。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mulix/internal/engine"
	"mulix/internal/event"
)

// writeInstructionFile 是测试工作区的指令文件写入 helper（返回完整路径）。
func writeInstructionFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// collectNotices 是 watcher 发布面的收集器（并发安全；缓冲防丢）。
type collectNotices struct {
	ch chan event.NoticeEvent
}

func newCollectNotices() *collectNotices {
	return &collectNotices{ch: make(chan event.NoticeEvent, 64)}
}

func (c *collectNotices) publish(ev event.NoticeEvent) { c.ch <- ev }

// next 等待下一条提醒（超时 fatal）。
func (c *collectNotices) next(t *testing.T, within time.Duration) event.NoticeEvent {
	t.Helper()
	select {
	case ev := <-c.ch:
		return ev
	case <-time.After(within):
		t.Fatalf("no notice within %v", within)
		return event.NoticeEvent{} // 不可达
	}
}

// quiet 断言静默窗内无任何提醒（同键重复提醒/误报即失败）。
func (c *collectNotices) quiet(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case ev := <-c.ch:
		t.Fatalf("unexpected notice in quiet window: %+v", ev)
	case <-time.After(window):
	}
}

// ---- ① 快照加载 ----

// TestLoadInstructionSnapshots 钉：workspace 放 CLAUDE.md/AGENTS.md → map 含
// 内容与 sha256（键 = workspace 下的文件路径——watcher 以键重读、通知取
// base 名的约定）；空目录 → 空 map；文件缺席 → 键缺席。
func TestLoadInstructionSnapshots(t *testing.T) {
	t.Run("claude md present = content and sha256", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructionFile(t, dir, "CLAUDE.md", "# hello\n")
		snaps := loadInstructionSnapshots(dir)
		if len(snaps) != 1 {
			t.Fatalf("snapshots = %d entries, want 1 (%v)", len(snaps), snaps)
		}
		key := filepath.Join(dir, "CLAUDE.md")
		snap, ok := snaps[key]
		if !ok {
			t.Fatalf("key %q absent (keys=%v)", key, snaps)
		}
		if snap.Content != "# hello\n" {
			t.Fatalf("content = %q, want %q", snap.Content, "# hello\n")
		}
		sum := sha256.Sum256([]byte("# hello\n"))
		if want := hex.EncodeToString(sum[:]); snap.SHA256 != want {
			t.Fatalf("sha256 = %q, want %q", snap.SHA256, want)
		}
	})
	t.Run("empty dir = empty map", func(t *testing.T) {
		snaps := loadInstructionSnapshots(t.TempDir())
		if len(snaps) != 0 {
			t.Fatalf("empty workspace must yield empty map, got %v", snaps)
		}
	})
	t.Run("agents md only = claude key absent", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructionFile(t, dir, "AGENTS.md", "agents rules")
		snaps := loadInstructionSnapshots(dir)
		if len(snaps) != 1 {
			t.Fatalf("snapshots = %d entries, want 1 (%v)", len(snaps), snaps)
		}
		if snap, ok := snaps[filepath.Join(dir, "AGENTS.md")]; !ok || snap.Content != "agents rules" {
			t.Fatalf("AGENTS.md snapshot missing or wrong: %v", snaps)
		}
		if _, ok := snaps[filepath.Join(dir, "CLAUDE.md")]; ok {
			t.Fatal("absent CLAUDE.md must not appear as a key（文件不存在则键缺席）")
		}
	})
	t.Run("both files = two keys", func(t *testing.T) {
		dir := t.TempDir()
		writeInstructionFile(t, dir, "CLAUDE.md", "c")
		writeInstructionFile(t, dir, "AGENTS.md", "a")
		snaps := loadInstructionSnapshots(dir)
		if len(snaps) != 2 {
			t.Fatalf("snapshots = %d entries, want 2 (%v)", len(snaps), snaps)
		}
	})
}

// ---- ② 内容变化 → 恰好一条 Notice ----

// TestWatchInstructionsContentChangeNoticedOnce 钉：内容变化 → 恰一条 info
// 提醒（文案逐字）；同一轮询窗内多次变化只报一次；基线推进后同内容不再
// 刷屏。间隔取 50ms（非 10ms）：两次连续写必在首个 tick 前完成——多变化
// 单提醒的判定因此确定（首个 tick 只见终态），不被写间隙的 tick 撞上。
func TestWatchInstructionsContentChangeNoticedOnce(t *testing.T) {
	dir := t.TempDir()
	path := writeInstructionFile(t, dir, "CLAUDE.md", "v1")
	snaps := loadInstructionSnapshots(dir)
	notices := newCollectNotices()
	w := watchInstructions(context.Background(), snaps, notices.publish, 50*time.Millisecond)
	defer w.Close()

	// 同一窗口内连续两次变化（v2→v3，均先于首个 tick）。
	if err := os.WriteFile(path, []byte("v2"), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if err := os.WriteFile(path, []byte("v3"), 0o644); err != nil {
		t.Fatalf("write v3: %v", err)
	}
	ev := notices.next(t, 2*time.Second)
	if ev.Level != "info" {
		t.Fatalf("notice level = %q, want info", ev.Level)
	}
	if want := "指令文件变更: CLAUDE.md（快照保持，下会话生效）"; ev.Content != want {
		t.Fatalf("notice content = %q, want %q", ev.Content, want)
	}
	// 基线推进：终态内容不再重复提醒（4 个 tick 的静默窗）。
	notices.quiet(t, 200*time.Millisecond)
}

// ---- ③ touch（mtime 变内容同）→ 无 Notice（Review Focus 钉） ----

// TestWatchInstructionsTouchNoNotice 钉：mtime 前移而内容不变不得提醒——
// 比较依据是 sha256 而非 mtime（计划 §Review Focus 第五条）。
func TestWatchInstructionsTouchNoNotice(t *testing.T) {
	dir := t.TempDir()
	path := writeInstructionFile(t, dir, "CLAUDE.md", "stable content")
	snaps := loadInstructionSnapshots(dir)
	notices := newCollectNotices()
	w := watchInstructions(context.Background(), snaps, notices.publish, 10*time.Millisecond)
	defer w.Close()

	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	notices.quiet(t, 100*time.Millisecond) // 10ms × ~10 tick 全静默
}

// ---- ④ 删除文件 → 提醒事件（同修改语义） ----

// TestWatchInstructionsDeleteNoticed 钉：被监控文件删除 → 同修改语义的提醒
// （文案一致），且只报一次（不随后续 tick 刷屏）。
func TestWatchInstructionsDeleteNoticed(t *testing.T) {
	dir := t.TempDir()
	path := writeInstructionFile(t, dir, "AGENTS.md", "delete me")
	snaps := loadInstructionSnapshots(dir)
	notices := newCollectNotices()
	w := watchInstructions(context.Background(), snaps, notices.publish, 10*time.Millisecond)
	defer w.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	ev := notices.next(t, 2*time.Second)
	if ev.Level != "info" {
		t.Fatalf("notice level = %q, want info（删除同修改语义）", ev.Level)
	}
	if want := "指令文件变更: AGENTS.md（快照保持，下会话生效）"; ev.Content != want {
		t.Fatalf("notice content = %q, want %q", ev.Content, want)
	}
	notices.quiet(t, 100*time.Millisecond) // 删除只报一次
}

// ---- ⑤ Close 后 goroutine 退出（channel 等待退出信号，无泄漏） ----

// TestWatchInstructionsCloseStopsGoroutine 钉：Close 停 ticker——goroutine
// 经退出信号通道收口（Done 可等待，无泄漏）；Close 幂等。
func TestWatchInstructionsCloseStopsGoroutine(t *testing.T) {
	dir := t.TempDir()
	writeInstructionFile(t, dir, "CLAUDE.md", "x")
	snaps := loadInstructionSnapshots(dir)
	w := watchInstructions(context.Background(), snaps, func(event.NoticeEvent) {}, 10*time.Millisecond)
	w.Close()
	select {
	case <-w.Done(): // goroutine 退出信号——无泄漏
	case <-time.After(2 * time.Second):
		t.Fatal("watcher goroutine did not exit after Close")
	}
	w.Close() // 幂等：重复 Close 不得 panic
}

// TestWatchContextCancelStopsGoroutine 钉 ctx 参数语义：取消同样收口
// goroutine（Close 之外的第二个停止面）。
func TestWatchContextCancelStopsGoroutine(t *testing.T) {
	dir := t.TempDir()
	writeInstructionFile(t, dir, "CLAUDE.md", "x")
	snaps := loadInstructionSnapshots(dir)
	ctx, cancel := context.WithCancel(context.Background())
	w := watchInstructions(ctx, snaps, func(event.NoticeEvent) {}, 10*time.Millisecond)
	cancel()
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("watcher goroutine did not exit after ctx cancel")
	}
}

// ---- start 序列接线（结构钉） ----

// TestWatchWiredToStartSequence 钉：start 成功后装载快照入控制器并启动
// watcher；变更提醒经 c.publish 到 Sink 且镜像留痕（先持久化后发布红线由
// 既有 publish 路径保证，此处端到端证明接线）；Close 停 watcher；无指令
// 文件 → 不起 watcher（P1「无文件即无快照」语义）。
func TestWatchWiredToStartSequence(t *testing.T) {
	t.Run("start loads snapshot and launches watcher", func(t *testing.T) {
		ws := t.TempDir()
		writeInstructionFile(t, ws, "CLAUDE.md", "# rules")
		fake := engine.NewFakeHost(engine.FakeScript{})
		sink := &recordingSink{}
		c := newTestControllerCapturingBoot(t, fake, sink, &engine.BootConfig{}, func(o *Options) {
			o.Workspace = ws
		})
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		c.mu.Lock()
		snaps, watch := c.instructionSnaps, c.instrWatch
		c.mu.Unlock()
		key := filepath.Join(ws, "CLAUDE.md")
		if snap, ok := snaps[key]; !ok || snap.Content != "# rules" {
			t.Fatalf("start must load instruction snapshot into controller (snaps=%v)", snaps)
		}
		if watch == nil {
			t.Fatal("start must launch instruction watcher")
		}
	})
	t.Run("change notice reaches sink and mirror", func(t *testing.T) {
		// 短间隔注入（包内变量；同包测试串行执行——恢复由 defer 保证）。
		old := instructionWatchInterval
		instructionWatchInterval = 20 * time.Millisecond
		defer func() { instructionWatchInterval = old }()
		ws := t.TempDir()
		path := writeInstructionFile(t, ws, "CLAUDE.md", "v1")
		fake := engine.NewFakeHost(engine.FakeScript{})
		sink := &recordingSink{}
		c := newTestControllerCapturingBoot(t, fake, sink, &engine.BootConfig{}, func(o *Options) {
			o.Workspace = ws
		})
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		if err := os.WriteFile(path, []byte("v2"), 0o644); err != nil {
			t.Fatalf("write v2: %v", err)
		}
		// Sink 面出现提醒（Seq 由控制器发布路径分配）。
		deadline := time.Now().Add(3 * time.Second)
		found := false
		for !found {
			for _, raw := range sink.all() {
				n, ok := raw.(event.NoticeEvent)
				if !ok || !strings.Contains(n.Content, "指令文件变更: CLAUDE.md") {
					continue
				}
				found = true
				if n.Level != "info" {
					t.Fatalf("notice level = %q, want info", n.Level)
				}
				if n.Seq == 0 {
					t.Fatal("notice seq must be assigned by controller publish path")
				}
			}
			if !found {
				if time.Now().After(deadline) {
					t.Fatalf("no instruction notice at sink within 3s (%d events)", len(sink.all()))
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		// 镜像留痕（design 002 §8：变更 → NoticeEvent + 镜像留痕）。
		mirror, err := c.Mirror().ReadAllEvents()
		if err != nil {
			t.Fatalf("read mirror: %v", err)
		}
		mirrored := false
		for _, raw := range mirror {
			if n, ok := raw.(event.NoticeEvent); ok && strings.Contains(n.Content, "指令文件变更: CLAUDE.md") {
				mirrored = true
			}
		}
		if !mirrored {
			t.Fatalf("instruction change notice must be mirrored (%d events)", len(mirror))
		}
	})
	t.Run("close stops watcher", func(t *testing.T) {
		ws := t.TempDir()
		writeInstructionFile(t, ws, "AGENTS.md", "rules")
		fake := engine.NewFakeHost(engine.FakeScript{})
		sink := &recordingSink{}
		c := newTestControllerCapturingBoot(t, fake, sink, &engine.BootConfig{}, func(o *Options) {
			o.Workspace = ws
		})
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		c.mu.Lock()
		watch := c.instrWatch
		c.mu.Unlock()
		if watch == nil {
			t.Fatal("watcher must be running after start")
		}
		if err := c.Close(context.Background()); err != nil {
			t.Fatalf("close: %v", err)
		}
		select {
		case <-watch.Done(): // Close 停 ticker——goroutine 无泄漏退出
		case <-time.After(2 * time.Second):
			t.Fatal("watcher goroutine did not exit after controller Close")
		}
	})
	t.Run("no instruction files = no watcher", func(t *testing.T) {
		fake := engine.NewFakeHost(engine.FakeScript{})
		sink := &recordingSink{}
		c := newTestControllerCapturingBoot(t, fake, sink, &engine.BootConfig{}, func(o *Options) {
			o.Workspace = t.TempDir()
		})
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		c.mu.Lock()
		snaps, watch := c.instructionSnaps, c.instrWatch
		c.mu.Unlock()
		if len(snaps) != 0 {
			t.Fatalf("empty workspace must yield empty snapshot map (got %v)", snaps)
		}
		if watch != nil {
			t.Fatal("no instruction files = no watcher（P1 无文件即无快照语义）")
		}
	})
}
