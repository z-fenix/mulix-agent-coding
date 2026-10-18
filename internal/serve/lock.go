// lock.go——跨进程会话锁(2026-10-18-web-ui-mvp Task 5)。
//
// <sessionDir>/web.lock 承载 {pid, heartbeat_unix}:持锁进程以心跳节拍
// (默认 10s)原子重写;acquire 遇「新鲜」锁(心跳距今 < 30s)fail-closed
// 拒绝并命名持有者 pid——绝不静默接管别人活着的会话(红线)。
//
// fail-open 裁定(刻意):锁文件**损坏/过期**时接管放行。锁是 advisory
// artifact——持有进程死透(心跳过期)或文件残损(半写/非 JSON)后,拒绝
// 接管只会让会话永久不可用,而误接管的代价(两个 web 进程同写一个镜像)
// 有镜像单写者之外的锁头与人工介入兜底;损坏判定不区分错误种类。
// 新鲜且合法的锁仍一律拒绝(TestLockAcquireAndRefuse 钉死)。
package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// lockFileName 是会话目录内的锁文件名(与会话镜像同目录——锁随会话生命周期)。
const lockFileName = "web.lock"

// lockFreshness 是心跳新鲜阈值:heartbeat_unix 距今小于该值 → 持有进程
// 视为存活,acquire fail-closed 拒绝。包级 var 是测试缝(guard
// defaultHookTimeoutMS 同惯例;生产不改写)。
var lockFreshness = 30 * time.Second

// lockHeartbeatInterval 是持锁进程的心跳节拍(生产 10s;包级 var 测试缝)。
var lockHeartbeatInterval = 10 * time.Second

// webLock 是 web.lock 的 JSON 形状(heartbeat_unix 秒粒度——新鲜判定
// 天然有 ≤1s 误差,远小于 30s 阈值,可接受)。
type webLock struct {
	PID           int   `json:"pid"`
	HeartbeatUnix int64 `json:"heartbeat_unix"`
}

// acquireLock 是 HTTP 层取锁的接缝(M1:create 路径锁在装配前——注入失败可
// 钉「锁失败不留孤儿镜像」清理面;生产直连 acquireSessionLock)。
var acquireLock = acquireSessionLock

// acquireSessionLock 获取会话的跨进程锁:新鲜锁拒绝(错误命名 pid);
// 过期/损坏/缺失 → 原子写锁文件(tmp+rename)并启动心跳 goroutine 刷新。
// release 幂等:停心跳(等待退出)后删锁文件。
func acquireSessionLock(sessionDir string) (release func(), err error) {
	path := filepath.Join(sessionDir, lockFileName)
	if cur, rerr := readLock(path); rerr == nil {
		// 既有锁:新鲜 → fail-closed。未来时间戳(时钟偏差/畸形值)同样
		// 落入 age < lockFreshness → 拒绝(fail-closed 方向:存疑不接管)。
		if age := time.Since(time.Unix(cur.HeartbeatUnix, 0)); age < lockFreshness {
			return nil, fmt.Errorf("serve: session locked by another process (pid %d, heartbeat %v ago)",
				cur.PID, age.Round(time.Second))
		}
	}
	if err := writeLock(path, os.Getpid()); err != nil {
		return nil, fmt.Errorf("serve: write lock: %w", err)
	}
	interval := lockHeartbeatInterval // 同步读一次;goroutine 不再触碰包级 var
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// 瞬态写失败:下个 tick 重试;持续失败 → 心跳停更 → 锁
				// 自然过期被他人接管(advisory 语义内的自愈,不 panic)。
				_ = writeLock(path, os.Getpid())
			}
		}
	}()
	var released bool
	return func() {
		if released { // 幂等:重复 release 无害(关闭的 chan/已删文件均不重做)
			return
		}
		released = true
		close(done)
		<-stopped
		_ = os.Remove(path) // 不存在(被接管者重写)亦无妨——advisory
	}, nil
}

// readLock 读并解析锁文件(不存在/损坏 → error → 调用方按过期接管)。
// Windows 陷阱(实测):心跳 tmp+rename 原子替换的瞬间,并发读可能撞上
// 瞬态 sharing violation——短重试一次再放行,避免把「读撞写」误判成
// 「持有者死透」而立即接管。
func readLock(path string) (webLock, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		time.Sleep(5 * time.Millisecond)
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return webLock{}, err
	}
	var l webLock
	if err := json.Unmarshal(raw, &l); err != nil {
		return webLock{}, err
	}
	return l, nil
}

// writeLock 原子写锁文件:temp + fsync + rename——崩溃半写只留 .tmp 残骸,
// 正名文件永远完整可解析。Windows 红线(sessionx writeJSONFile 同款教训):
// rename 前必须 Close 写句柄。本地实现,不 import sessionx(锁是 serve 自有
// artifact,依赖方向 sessionx→serve 反转无必要)。
func writeLock(path string, pid int) error {
	payload, err := json.Marshal(webLock{PID: pid, HeartbeatUnix: time.Now().Unix()})
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
