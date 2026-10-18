// serve.go——web UI 服务骨架(2026-10-18-web-ui-mvp Task 3):http.Server +
// 路由(本任务仅 GET /api/healthz 与嵌入 dist 静态面);ctx 取消优雅关停。
// 后续任务在此装配 webSink/审批桥/会话面(T5-T7),本文件只留最小可编译面。
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
// NoOpen/AllowEngineDrift/Stdout/Stderr 是后续任务(T5-T7)的消费接缝,
// 本任务不读。
type Options struct {
	Config           *config.Config
	Workspace        string
	SessionsRoot     string
	Bind             string
	Port             int
	NoOpen           bool
	AllowEngineDrift bool
	Stdout, Stderr   io.Writer
}

// shutdownTimeout 优雅关停窗口:ctx 取消后给在途请求的收尾上限。
const shutdownTimeout = 5 * time.Second

// Run 监听 bind:port 并服务,直至 ctx 取消(优雅 Shutdown)或监听出错。
func Run(ctx context.Context, opts Options) error {
	bind := opts.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	port := opts.Port
	if port == 0 {
		port = 3180
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", bind, port),
		Handler:           newMux(),
		ReadHeaderTimeout: 10 * time.Second, // slowloris 防护:请求头读取限时
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// newMux 路由面:GET /api/healthz 健康探针;/ 为嵌入 dist 静态(占位页,
// Task 9 构建产物经 .gitignore 白名单原地替换)。独立成函数供 httptest 复用。
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	mux.Handle("/", http.FileServerFS(distFS))
	return mux
}
