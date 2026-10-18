// serve.go——web UI 服务(2026-10-18-web-ui-mvp Task 3 骨架;Task 7 接入
// hostManager 与 HTTP/SSE API):http.Server + 路由(健康探针/嵌入 dist/
// 会话面全表);ctx 取消优雅关停。
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"mulix/internal/config"
)

// Options 是 Run 参数。Config 携带 [web] 节完整校验结果(Task 1 fail-closed);
// Run 刻意迟钝:监听参数只认 opts.Bind/opts.Port(默认值来源由调用方裁决),
// 仅对零值兜底——Bind 空 → 回环,Port 0 → 3180。Workspace/SessionsRoot/
// NoOpen/AllowEngineDrift/Stdout/Stderr 是会话面(T5-T7)的消费接缝:
// SessionsRoot/Cfg/AllowEngineDrift/Stderr 经 assembleParams 直传装配
// (assemble.go),Workspace 兜底工作区清单,Stdout 不入装配(无消费面)。
type Options struct {
	Config           *config.Config
	Workspace        string
	SessionsRoot     string
	Bind             string
	Port             int
	NoOpen           bool
	AllowEngineDrift bool
	Stdout, Stderr   io.Writer

	// assemble 是测试缝:非 nil 时替换 HTTP 层构造 host 的装配闭包
	// (生产零值 → assembleReal;包内测试注入 stubAssemble)。
	assemble assembleFn

	// shutdown 是 SSE 生命线(Run 注入 ctx.Done;零值 = 永不触发):http.
	// Server.Shutdown 不取消请求 ctx,SSE 处理器若不感知关停会拖满优雅
	// 关停窗口——流选择面消费它,Shutdown 起点即断流退订。
	shutdown <-chan struct{}
}

// shutdownTimeout 优雅关停窗口:ctx 取消后给在途请求的收尾上限。
const shutdownTimeout = 5 * time.Second

// hostCloseTimeout 是逐 host 收尾上界:CancelAll 先 deny 全部待审(泵不被
// 审批窗口卡住),健康路径毫秒级;上界只兜异常泵(子代理在飞等)。
const hostCloseTimeout = 10 * time.Second

// stderr 缺省 io.Discard(逐 host 收尾失败留痕面)。
func (o Options) stderr() io.Writer {
	if o.Stderr == nil {
		return io.Discard
	}
	return o.Stderr
}

// Run 监听 bind:port 并服务,直至 ctx 取消(优雅 Shutdown)或监听出错。
// hostManager 在此构造(idle 取 Config.Web.IdleCloseMinutes;≤0/Config nil →
// newHostManager 兜底),进程生命周期与 server 同轨(Shutdown 后 Close)。
func Run(ctx context.Context, opts Options) error {
	bind := opts.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	port := opts.Port
	if port == 0 {
		port = 3180
	}
	var idle time.Duration
	if opts.Config != nil {
		idle = time.Duration(opts.Config.Web.IdleCloseMinutes) * time.Minute
	}
	mgr := newHostManager(idle)
	defer mgr.Close()
	runOpts := opts
	runOpts.shutdown = ctx.Done() // SSE 生命线(见 Options.shutdown)
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", bind, port),
		Handler:           newMux(mgr, runOpts),
		ReadHeaderTimeout: 10 * time.Second, // slowloris 防护:请求头读取限时
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		sherr := srv.Shutdown(shutCtx)
		// 逐 host 收尾(修复轮 1 裁定):mgr.Close 只停 idle 扫描——在册 host
		// 的 ctrl/meter/store/web.lock 必须显式收口,否则引擎子进程孤儿化。
		// 在 Shutdown 之后跑(在途 handler 已收完,无并发触 host);单个失败
		// 不阻断其余,错误 stderr 留痕(不吞——审计面不静默)。
		closeCtx, closeCancel := context.WithTimeout(context.Background(), hostCloseTimeout)
		defer closeCancel()
		for _, err := range mgr.closeAll(closeCtx) {
			_, _ = fmt.Fprintln(runOpts.stderr(), err)
		}
		return sherr
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// newMux 路由面:健康探针 + 嵌入 dist 静态(SPA,GET / 下发会话 Cookie)+
// 会话 HTTP/SSE API 全表(Task 7,路由即契约——见 http.go)。独立成函数供
// httptest 复用。
func newMux(mgr *hostManager, opts Options) http.Handler {
	api := &webAPI{mgr: mgr, opts: opts}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/workspaces", api.handleWorkspaces)
	mux.HandleFunc("GET /api/sessions", api.handleList)
	mux.HandleFunc("POST /api/sessions", api.mutating(api.handleCreate))
	mux.HandleFunc("GET /api/sessions/{id}/events", api.handleEvents)
	mux.HandleFunc("GET /api/sessions/{id}/stream", api.handleStream)
	mux.HandleFunc("GET /api/sessions/{id}/state", api.handleState)
	mux.HandleFunc("POST /api/sessions/{id}/open", api.mutating(api.handleOpen))
	mux.HandleFunc("POST /api/sessions/{id}/submit", api.mutating(api.handleSubmit))
	mux.HandleFunc("POST /api/sessions/{id}/cancel", api.mutating(api.handleCancel))
	mux.HandleFunc("POST /api/sessions/{id}/approval", api.mutating(api.handleApproval))
	mux.Handle("GET /", api.spa())
	return mux
}
