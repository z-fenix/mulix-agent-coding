// lock_test.go——跨进程会话锁钉子(2026-10-18-web-ui-mvp Task 5):
// 新鲜锁 fail-closed 拒绝(错误命名持有者 pid)、过期/损坏锁接管(fail-open
// 仅限 advisory artifact)、心跳按注入节拍刷新文件。测试注入包级 var 收缩
// 墙钟,绝不真实睡眠 30s/10s。
package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeLockFixture 手写一个锁文件(过期/占位场景的构造器)。
func writeLockFixture(t *testing.T, dir string, l webLock) {
	t.Helper()
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, lockFileName), raw, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

// readLockFixture 读回锁文件(心跳刷新断言面)。Windows 实测:心跳
// tmp+rename 原子替换的瞬间并发读会撞瞬态 sharing violation——短重试
// (与 readLock 同款容错;rename 原子性保证内容永不半写,解析失败仍 fatal)。
func readLockFixture(t *testing.T, dir string) webLock {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var l webLock
	for {
		raw, err := os.ReadFile(filepath.Join(dir, lockFileName))
		if err == nil {
			if jerr := json.Unmarshal(raw, &l); jerr != nil {
				t.Fatalf("parse lock: %v", jerr)
			}
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("read lock: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestLockAcquireAndRefuse 钉:首个 acquire 成功;持锁期间第二次 acquire
// fail-closed 拒绝且错误命名持有者 pid;release 删除锁文件且幂等,之后可再
// acquire。
func TestLockAcquireAndRefuse(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireSessionLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := acquireSessionLock(dir); err == nil {
		release()
		t.Fatal("second acquire = nil err, want fail-closed refusal")
	} else if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("refusal %q 未命名持有者 pid %d", err.Error(), os.Getpid())
	}
	release()
	release() // 幂等:重复 release 不得 panic
	if _, err := os.Stat(filepath.Join(dir, lockFileName)); !os.IsNotExist(err) {
		t.Fatalf("release 后锁文件仍存在(stat err = %v)", err)
	}
	release2, err := acquireSessionLock(dir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release2()
}

// TestLockStaleTakeover 钉:手写过期心跳(远早于 freshness 阈值)的锁文件
// → acquire 成功接管(锁是 advisory,持有者死透的痕迹不挡新会话)。
func TestLockStaleTakeover(t *testing.T) {
	dir := t.TempDir()
	writeLockFixture(t, dir, webLock{PID: 999999, HeartbeatUnix: time.Now().Add(-time.Hour).Unix()})
	release, err := acquireSessionLock(dir)
	if err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	defer release()
	if got := readLockFixture(t, dir).PID; got != os.Getpid() {
		t.Fatalf("takeover 后锁 pid = %d, want %d(接管者重写)", got, os.Getpid())
	}
}

// TestLockCorruptIsStale 钉:损坏(非 JSON)锁文件按过期处理——接管放行。
// fail-open 仅限损坏 artifact(见 lock.go 注释);新鲜合法锁仍 fail-closed
// (TestLockAcquireAndRefuse)。
func TestLockCorruptIsStale(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("write corrupt lock: %v", err)
	}
	release, err := acquireSessionLock(dir)
	if err != nil {
		t.Fatalf("corrupt takeover: %v", err)
	}
	defer release()
}

// TestLockHeartbeatRefresh 钉:acquire 后心跳 goroutine 按注入节拍(5ms)
// 刷新文件 heartbeat_unix;release 停心跳(文件不再更新)。
func TestLockHeartbeatRefresh(t *testing.T) {
	old := lockHeartbeatInterval
	lockHeartbeatInterval = 5 * time.Millisecond
	defer func() { lockHeartbeatInterval = old }()

	dir := t.TempDir()
	release, err := acquireSessionLock(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	first := readLockFixture(t, dir).HeartbeatUnix
	// unix 秒粒度:轮询窗口必须跨秒界(2s >> 1s 粒度)才能观测到刷新。
	deadline := time.Now().Add(2 * time.Second)
	refreshed := false
	for time.Now().Before(deadline) {
		if readLockFixture(t, dir).HeartbeatUnix > first {
			refreshed = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !refreshed {
		t.Fatalf("heartbeat 未刷新: first=%d(注入节拍 5ms)", first)
	}
}
