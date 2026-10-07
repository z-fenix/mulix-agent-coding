package engine

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClaudeHostOptions 是 NewClaudeHost 参数。
type ClaudeHostOptions struct {
	LockPath          string        // engine.lock 路径（必填；Start 校验）
	HandshakeTimeout  time.Duration // init 握手超时（默认 30s）
	GracefulCloseWait time.Duration // Close 无 ctx deadline 时的优雅窗口（默认 3s）
	StderrRingSize    int           // stderr 环形缓冲行数（默认 256）
}

// ClaudeHost 是 Host 的真实实现（claude CLI 子进程 + stream-json 泵；
// design §7）。单实例单会话；Start 一次。
type ClaudeHost struct {
	opts ClaudeHostOptions
	lock EngineLock
	cfg  BootConfig

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdinMu sync.Mutex
	stdinCl bool
	stdout  io.Reader // StdoutPipe 读端（pump 持有）
	stderr  io.Reader // StderrPipe 读端（pump 持有）
	mcpTemp string    // --mcp-config 物化文件（Close/失败路径清理）

	eventsOut chan Event
	done      chan struct{} // Close 关闭：让路由/监视协程退出投递阻塞
	doneOnce  sync.Once

	ring *stderrRing

	infoMu sync.RWMutex
	info   EngineInfo

	startOnce   sync.Once
	startErr    error
	startDone   chan struct{}
	ready       bool // init 握手完成（routeMu 保护）
	pending     []Event
	routeMu     sync.Mutex
	exited      chan struct{} // monitor Wait 完成后关闭
	killed      bool          // 由 Close 强杀置位（exit 事件 reason）
	monitorOnce sync.Once
	// provisional：真实引擎 init 后置协议适配（终评 Critical-1）——Start 在
	// 握手窗口内无 init 时临时放行；首个 init 到达时照常校验。
	provisional bool
	// exitClosed：eventsOut 已被 provisional 拒绝路径关闭（deliver 跳过，
	// 防止 route 循环残余事件 send-on-closed panic）。
	exitClosed bool

	routeDone  chan struct{} // 路由结束（stdout 泵停 + 事件投递完毕）——monitor 在 Wait 前等待
	stderrDone chan struct{} // stderr 泵结束
}

// NewClaudeHost 构造宿主（Start 时才 spawn）。
func NewClaudeHost(opts ClaudeHostOptions) *ClaudeHost {
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 30 * time.Second
	}
	if opts.GracefulCloseWait <= 0 {
		opts.GracefulCloseWait = 3 * time.Second
	}
	if opts.StderrRingSize <= 0 {
		opts.StderrRingSize = 256
	}
	return &ClaudeHost{
		opts:       opts,
		startDone:  make(chan struct{}),
		done:       make(chan struct{}),
		exited:     make(chan struct{}),
		routeDone:  make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
}

var _ Host = (*ClaudeHost)(nil)

// Events 返回引擎事件流（128 缓冲；Start 前注册的握手前事件也在此流出）。
func (h *ClaudeHost) Events() <-chan Event { return h.eventsOut }

// Info 返回握手后的引擎身份（Start 成功前为零值）。
func (h *ClaudeHost) Info() EngineInfo {
	h.infoMu.RLock()
	defer h.infoMu.RUnlock()
	return h.info
}

// StderrTail 返回 stderr 环形缓冲快照（旧→新；诊断用）。
func (h *ClaudeHost) StderrTail(n int) []string {
	if h.ring == nil {
		return nil
	}
	return h.ring.Snapshot(n)
}

// Start 拉起引擎子进程并等待初始化握手（首个 system/init）。
// fail-closed 校验（任一失败 → 杀进程 + 返回错误）：
//   - --permission-mode 必须显式（auto 绕过审批——实测）；
//   - --setting-sources 必须含 user（否则凭据不加载——实测）；
//   - init.claude_code_version 与 engine.lock 不一致默认拒绝（FR-003），
//     AllowEngineDrift 显式放行并产出 engine_drift trace 事件（留痕）；
//   - init.permissionMode=="auto" 一律拒绝（审批回路被绕过，安全边界失效）。
func (h *ClaudeHost) Start(ctx context.Context, cfg BootConfig) error {
	if cfg.EnginePath == "" {
		return fmt.Errorf("engine: engine path required")
	}
	if cfg.PermissionMode == "" {
		return fmt.Errorf("engine: --permission-mode must be explicit (auto bypasses approvals)")
	}
	if !containsString(cfg.SettingSources, "user") {
		return fmt.Errorf("engine: --setting-sources must include user scope (credentials not loaded otherwise)")
	}
	lock, err := LoadEngineLock(h.opts.LockPath)
	if err != nil {
		return err
	}
	h.lock = lock
	h.cfg = cfg
	h.ring = newStderrRing(h.opts.StderrRingSize, buildRedactors(cfg.Env))

	args, mcpTemp, err := h.buildArgs(cfg)
	if err != nil {
		return err
	}
	h.mcpTemp = mcpTemp

	cmd := exec.Command(cfg.EnginePath, args...)
	cmd.Dir = cfg.Workspace
	cmd.Env = append(os.Environ(), cfg.Env...)
	// spawn 阶段任一步失败 → 清理已物化的 mcp 临时文件（Minor-1 修复）
	spawnErr := func() error {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("engine: stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("engine: stdout pipe: %w", err)
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("engine: stderr pipe: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("engine: spawn: %w", err)
		}
		h.stdin = stdin
		h.stderr = stderr
		h.stdout = stdout
		return nil
	}()
	if spawnErr != nil {
		h.cleanupTemp()
		return spawnErr
	}
	h.cmd = cmd
	h.eventsOut = make(chan Event, 128)

	// stderr 泵（脱敏后入环形缓冲；结束信号供 monitor 的 Wait 前汇合）
	go func() {
		defer close(h.stderrDone)
		sc := newLargeScanner(h.stderr)
		for sc.Scan() {
			h.ring.Add(sc.Text())
		}
	}()

	// stdout 行泵：行 → DecodeLine → 路由；解码错误 = 流损坏（fail-closed）。
	pumpCh := make(chan Event, 16)
	pumpDone := make(chan error, 1)
	go func() {
		defer close(pumpCh)
		sc := newLargeScanner(h.stdout)
		for sc.Scan() {
			decoded, err := DecodeLine(sc.Bytes())
			if err != nil {
				pumpDone <- fmt.Errorf("engine: stdout stream corrupt: %w", err)
				return
			}
			if decoded == nil {
				continue // 未知 type/subtype 跳过（协议红线）
			}
			pumpCh <- *decoded
		}
		// scanner 终止但非 EOF（I/O 错误 / 超过 16MB 行上限）→ 流损坏路径
		if err := sc.Err(); err != nil {
			pumpDone <- fmt.Errorf("engine: stdout stream corrupt: %w", err)
			return
		}
		pumpDone <- nil
	}()

	// 事件路由：握手前 stash（保持顺序），init 后直通 eventsOut。
	// routeDone：路由彻底结束（泵停 + 投递完毕）后关闭——monitor 在
	// cmd.Wait 前等待它（StdoutPipe 契约：Wait 不得与未完成的读并发；
	// 同时保证 eventsOut 关闭前再无发送者——关闭通道 panic 窗口消除）。
	go func() {
		h.route(pumpCh, pumpDone)
		close(h.routeDone)
	}()

	// 进程监视：stdout/stderr 汇合后 Wait；退出 → exit 事件 → 关闭 eventsOut。
	go h.monitor()

	// 握手等待（有界）
	timeout := h.opts.HandshakeTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d < timeout {
			timeout = d
		}
	}
	select {
	case <-h.startDone:
		if h.startErr != nil {
			h.killTree()
			return h.startErr
		}
		return nil
	case <-time.After(timeout):
		// 协议适配（终评 Critical-1 实测）：真实 claude 2.1.291 的 init 在
		// 首条 user message 之后才发出——窗口内无 init 且进程仍存活 → 临时
		// 放行（provisional start）；首个 init 到达时照常校验（失败 → 终止
		// 引擎 + 拒绝原因合成 exit 事件，fail-closed 保留）。
		select {
		case <-h.exited:
			// 进程在窗口内退出（monitor 已 signalStart「exited during
			// handshake」）——按启动失败收口。
			h.killTree()
			return h.startErr
		default:
		}
		h.routeMu.Lock()
		h.provisional = true
		h.routeMu.Unlock()
		h.signalStart(nil)
		return nil
	case <-ctx.Done():
		h.signalStart(fmt.Errorf("engine: start cancelled: %w", ctx.Err()))
		h.killTree()
		return h.startErr
	}
}

// route 把泵事件投递到 eventsOut（握手前 stash；init 完成校验与留痕）。
// 返回时 pumpCh 已耗尽且 pumpDone 已消费；eventsOut 的发送全部完成后才返回
// （routeDone 由调用方在返回后关闭）。
func (h *ClaudeHost) route(pumpCh <-chan Event, pumpDone <-chan error) {
	for ev := range pumpCh {
		if ev.Kind != KindInit {
			h.routeMu.Lock()
			ready := h.ready
			if !ready {
				h.pending = append(h.pending, ev)
			}
			h.routeMu.Unlock()
			if ready {
				h.deliver(ev)
			}
			continue
		}
		// init：校验 → 接受（置 ready）→ 按序 flush
		if err := h.validateHandshake(ev); err != nil {
			h.signalStart(err)
			// provisional start（Start 已返回）下的校验失败：Start 的错误
			// 通道无人等待——终止引擎并以拒绝原因合成 exit 事件（先于
			// monitor 的 killed exit 投递；monitorOnce 保证 close 互斥）。
			h.routeMu.Lock()
			prov := h.provisional
			if prov {
				h.killed = true
			}
			h.routeMu.Unlock()
			if prov {
				h.killTree()
				h.monitorOnce.Do(func() {
					h.deliver(Event{Kind: KindExit, Exit: &ExitInfo{Code: -1,
						Reason: "refused: " + err.Error()}})
					h.routeMu.Lock()
					h.exitClosed = true
					h.routeMu.Unlock()
					close(h.eventsOut)
				})
			}
			continue
		}
		flush := h.acceptHandshake(ev)
		for _, p := range flush {
			h.deliver(p)
		}
		h.deliver(ev)
	}
	// stdout EOF / 泵失败：pumpDone 在 pumpCh 关闭前必有值（泵先发后关）
	pumpErr := <-pumpDone
	h.routeMu.Lock()
	ready := h.ready
	h.routeMu.Unlock()
	switch {
	case pumpErr != nil && ready:
		// 流损坏：以 exit 事件收尾（进程可能仍活着——Task 22 消费方语义）
		h.deliver(Event{Kind: KindExit, Exit: &ExitInfo{Code: -1, Reason: pumpErr.Error()}})
		h.finishEvents()
	case pumpErr != nil && !ready:
		h.signalStart(pumpErr)
	}
}

// validateHandshake 在首个 init 上做版本/权限模式校验。
func (h *ClaudeHost) validateHandshake(ev Event) error {
	init := ev.Init
	if init == nil {
		return fmt.Errorf("engine: init event without payload")
	}
	if init.PermissionMode == "auto" {
		return fmt.Errorf("engine: engine reports permissionMode=auto (approvals bypassed); refusing to start")
	}
	if init.CLIVersion != h.lock.CLIVersion {
		if !h.cfg.AllowEngineDrift {
			return fmt.Errorf("engine: version drift: lock %s, actual %s (refusing; allow with --allow-engine-drift)", h.lock.CLIVersion, init.CLIVersion)
		}
	}
	return nil
}

// acceptHandshake 完成握手：置 Info、drift 留痕、取走 pending。
// 返回待按序 flush 的事件（pending + 可能的 drift trace）；ready/pending 的
// 变更在 routeMu 内完成（monitor 读取 ready 的同步点），投递在锁外按序执行。
func (h *ClaudeHost) acceptHandshake(ev Event) []Event {
	init := ev.Init
	h.infoMu.Lock()
	h.info = EngineInfo{
		CLIVersion:  init.CLIVersion,
		SessionID:   ev.SessionID,
		Model:       init.Model,
		Fingerprint: h.lock.ProtocolProbe.InitVersionField,
	}
	h.infoMu.Unlock()

	h.routeMu.Lock()
	if init.CLIVersion != h.lock.CLIVersion {
		// AllowEngineDrift=true（validateHandshake 已确认）→ 留痕事件（FR-003/SC-005）
		details, _ := json.Marshal(struct {
			LockVersion   string `json:"lock_version"`
			ActualVersion string `json:"actual_version"`
		}{h.lock.CLIVersion, init.CLIVersion})
		h.pending = append(h.pending, Event{
			Kind:      KindTrace,
			SessionID: ev.SessionID,
			UUID:      ev.UUID,
			Trace:     &TraceInfo{Code: "engine_drift", Message: "engine version drift allowed by explicit flag", Details: details},
		})
	}
	flush := h.pending
	h.pending = nil
	h.ready = true
	h.routeMu.Unlock()
	h.signalStart(nil)
	return flush
}

// deliver 投递事件（Close 后丢弃；eventsOut 关闭后跳过——provisional 拒绝
// 路径关闭事件流后，route 循环里残余的 pump 事件安全丢弃）。
func (h *ClaudeHost) deliver(ev Event) {
	h.routeMu.Lock()
	closed := h.exitClosed
	h.routeMu.Unlock()
	if closed {
		return
	}
	select {
	case h.eventsOut <- ev:
	case <-h.done:
	}
}

// monitor 等待进程退出并产出 exit 事件（eventsOut 的唯一关闭者）。
// 汇合顺序（Critical-1/2 修复）：routeDone（stdout 泵停 + 事件投递完毕）与
// stderrDone 先于 cmd.Wait——StdoutPipe 契约要求 Wait 不与未完成的读并发，
// 同时保证 close(eventsOut) 时再无发送者（消除 send-on-closed panic 窗口）。
func (h *ClaudeHost) monitor() {
	<-h.routeDone
	<-h.stderrDone
	err := h.cmd.Wait()
	close(h.exited)
	code := 0
	reason := "graceful"
	if h.cmd.ProcessState != nil {
		code = h.cmd.ProcessState.ExitCode()
	}
	h.routeMu.Lock()
	ready := h.ready
	killed := h.killed
	h.routeMu.Unlock()
	switch {
	case killed:
		reason = "killed"
	case code != 0 || err != nil:
		reason = "crashed"
	}
	h.monitorOnce.Do(func() {
		exitEv := Event{Kind: KindExit, Exit: &ExitInfo{Code: code, Reason: reason}}
		if !ready {
			h.signalStart(fmt.Errorf("engine: exited during handshake (code %d, reason %s)", code, reason))
		} else {
			// 先非阻塞投递（此刻 done 尚未关闭——markDone 只在 Close 观测到
			// exited 之后发生）：缓冲有位时事件必达，消除「Close 抢先 markDone
			// → deliver 走 done 分支丢事件」的竞态窗口；缓冲满时走下方
			// done-guarded 重试（Close 后允许丢弃——契约已文档化）。
			select {
			case h.eventsOut <- exitEv:
				exitEv.Exit = nil // 已投递
			default:
			}
			if exitEv.Exit != nil {
				h.deliver(exitEv)
			}
		}
		close(h.eventsOut)
	})
}

// finishEvents 供流损坏路径收尾（eventsOut 关闭者之一；monitorOnce 保证与
// monitor 互斥——先到者关，后到者跳过）。
func (h *ClaudeHost) finishEvents() {
	h.monitorOnce.Do(func() {
		close(h.eventsOut)
	})
}

// Send 提交一回合输入（§1 用户回合 wire 形状；TurnTail 追加为后续 text 块）。
func (h *ClaudeHost) Send(_ context.Context, msg UserMessage) error {
	blocks := make([]wireTextBlock, 0, 1+len(msg.TurnTail))
	blocks = append(blocks, wireTextBlock{Type: "text", Text: msg.Text})
	for _, tail := range msg.TurnTail {
		blocks = append(blocks, wireTextBlock{Type: "text", Text: tail})
	}
	wire := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content []wireTextBlock `json:"content"`
		} `json:"message"`
	}{Type: "user"}
	wire.Message.Role = "user"
	wire.Message.Content = blocks
	payload, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("engine: marshal user message: %w", err)
	}
	return h.writeStdin(payload)
}

// RespondControl 应答引擎控制请求（§1/§5 信封；request_id 以参数覆写——
// 重发携带新 id，逐信封应答）。
func (h *ClaudeHost) RespondControl(_ context.Context, id string, payload ControlResponse) error {
	payload.RequestID = id // 组包权威：信封参数（Task 15 交接钉）
	wire := struct {
		Type     string          `json:"type"`
		Response ControlResponse `json:"response"`
	}{"control_response", payload}
	data, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("engine: marshal control response: %w", err)
	}
	return h.writeStdin(data)
}

// Interrupt 发送控制协议 interrupt（§1 row 3；host→引擎方向为观察事实，
// Task 24 联调确认——P1 仅实现信封写入）。
func (h *ClaudeHost) Interrupt(_ context.Context) error {
	wire := struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			SubType string `json:"subtype"`
		} `json:"request"`
	}{Type: "control_request", RequestID: newRequestID()}
	wire.Request.SubType = "interrupt"
	data, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("engine: marshal interrupt: %w", err)
	}
	return h.writeStdin(data)
}

// Close 有界关闭：优雅（stdin EOF → 引擎自然退出）→ 超时杀进程树（§7.4）。
// ctx 无 deadline 时用 opts.GracefulCloseWait 兜底。
func (h *ClaudeHost) Close(ctx context.Context) error {
	if h.cmd == nil {
		h.markDone()
		return nil
	}
	// EOF 信号
	h.stdinMu.Lock()
	if !h.stdinCl {
		h.stdinCl = true
		_ = h.stdin.Close()
	}
	h.stdinMu.Unlock()

	bound := h.opts.GracefulCloseWait
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d < bound {
			bound = d
		}
	}
	select {
	case <-h.exited:
		h.markDone()
		h.cleanupTemp()
		return nil
	case <-time.After(bound):
		// 超时 → 杀进程树
		h.routeMu.Lock()
		h.killed = true
		h.routeMu.Unlock()
		h.killTree()
		select {
		case <-h.exited:
		case <-time.After(5 * time.Second):
		}
		h.markDone()
		h.cleanupTemp()
		return nil
	}
}

// writeStdin 串行写一行（互斥；未启动/已关/流断 → 错误）。
func (h *ClaudeHost) writeStdin(payload []byte) error {
	h.stdinMu.Lock()
	defer h.stdinMu.Unlock()
	if h.stdin == nil {
		return fmt.Errorf("engine: not started")
	}
	if h.stdinCl {
		return fmt.Errorf("engine: stdin closed")
	}
	if _, err := h.stdin.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("engine: write stdin: %w", err)
	}
	return nil
}

func (h *ClaudeHost) killTree() {
	if h.cmd == nil || h.cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		// 进程树终止（引擎可能拉起子进程）
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(h.cmd.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = h.cmd.Process.Kill()
}

func (h *ClaudeHost) signalStart(err error) {
	h.startOnce.Do(func() {
		h.startErr = err
		close(h.startDone)
	})
}

func (h *ClaudeHost) markDone() {
	h.doneOnce.Do(func() { close(h.done) })
}

func (h *ClaudeHost) cleanupTemp() {
	if h.mcpTemp != "" {
		_ = os.Remove(h.mcpTemp)
		h.mcpTemp = ""
	}
}

// buildArgs 构造 §7.2 参数集（顺序固定——字节稳定 Boot 面的可预测投影）。
// MCPConfigJSON 物化为临时文件（Close/失败路径清理）。返回 (args, mcpTempPath, err)。
func (h *ClaudeHost) buildArgs(cfg BootConfig) ([]string, string, error) {
	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", cfg.PermissionMode,
		"--setting-sources", strings.Join(cfg.SettingSources, ","),
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	mcpTemp := ""
	if cfg.MCPConfigJSON != "" {
		f, err := os.CreateTemp("", "mulix-mcp-*.json")
		if err != nil {
			return nil, "", fmt.Errorf("engine: materialize mcp-config: %w", err)
		}
		if _, err := f.WriteString(cfg.MCPConfigJSON); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, "", fmt.Errorf("engine: write mcp-config: %w", err)
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return nil, "", fmt.Errorf("engine: close mcp-config: %w", err)
		}
		mcpTemp = f.Name()
		args = append(args, "--mcp-config", mcpTemp)
	}
	if cfg.PermissionPromptTool != "" {
		args = append(args, "--permission-prompt-tool", cfg.PermissionPromptTool)
	}
	if cfg.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.AppendSystemPrompt)
	}
	if len(cfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(cfg.AllowedTools, ","))
	}
	if len(cfg.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(cfg.DisallowedTools, ","))
	}
	if cfg.ResumeEngineSessionID != "" {
		args = append(args, "--resume", cfg.ResumeEngineSessionID)
	}
	if cfg.ForkSession {
		args = append(args, "--fork-session")
	}
	return args, mcpTemp, nil
}

// ---- stderr 脱敏环形缓冲（§7.4） ----

type stderrRing struct {
	mu        sync.Mutex
	lines     []string
	cap       int
	head      int
	count     int
	redactors []string // 字面量替换表（注入凭据值）
}

func newStderrRing(size int, redactors []string) *stderrRing {
	return &stderrRing{lines: make([]string, size), cap: size, redactors: redactors}
}

func (r *stderrRing) Add(line string) {
	line = redactStderrLine(line, r.redactors)
	r.mu.Lock()
	tail := (r.head + r.count) % r.cap
	if r.count < r.cap {
		r.lines[tail] = line
		r.count++
	} else {
		r.lines[r.head] = line
		r.head = (r.head + 1) % r.cap
	}
	r.mu.Unlock()
}

// Snapshot 返回最近 n 行（旧→新）。
func (r *stderrRing) Snapshot(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > r.count {
		n = r.count
	}
	out := make([]string, 0, n)
	start := (r.head + r.count - n) % r.cap
	for i := 0; i < n; i++ {
		out = append(out, r.lines[(start+i)%r.cap])
	}
	return out
}

var (
	secretKeyPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
	bearerPattern    = regexp.MustCompile(`Bearer\s+[A-Za-z0-9._\-]+`)
)

// redactStderrLine 脱敏：注入凭据值（字面量替换表）+ 已知密钥形态
// （sk-…/Bearer …）→ [REDACTED]。
func redactStderrLine(line string, literals []string) string {
	for _, secret := range literals {
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[REDACTED]")
		}
	}
	line = secretKeyPattern.ReplaceAllString(line, "[REDACTED]")
	line = bearerPattern.ReplaceAllString(line, "Bearer [REDACTED]")
	return line
}

// buildRedactors 从注入环境提取凭据值（len≥8 才算——避免误伤短值）。
func buildRedactors(env []string) []string {
	var out []string
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			if v := kv[i+1:]; len(v) >= 8 {
				out = append(out, v)
			}
		}
	}
	return out
}

// ---- 小工具 ----

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// newLargeScanner 行扫描器（上限 16MB——与 jsonl 口径一致，防 64KB 默认截断）。
func newLargeScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	return sc
}

// newRequestID 生成中断等宿主发起控制请求的 id（随机 hex）。
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type wireTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
