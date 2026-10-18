// web.go——`mulix web` 的生产接线（2026-10-18-web-ui-mvp Task 8）：生效配置
// → serve.Options → serve.Run（SIGINT/SIGTERM 经 NotifyContext 优雅关停）。
// 服务起来后探活 /api/healthz 成功即用系统浏览器打开（rundll32/open/
// xdg-open 平台探测）；探活超时与浏览器失败一律 stderr 留痕、非致命——
// 打开浏览器是便利面，绝不影响服务生命周期。serve 侧 per-workspace 总线
// 收口随 Run 返回（serve.CloseSharedBus——serve 组合根与 cli 组合根不互依，
// main.go 的 cli.CloseSharedBus 收不到 serve 的注册表，见 assemble.go）。
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"mulix/internal/serve"
)

// probeTimeout 是浏览器打开前的服务探活上限（端口被占等监听失败场景下
// 不无限等待——serve.Run 自身会以错误返回，两边独立收口）。
const probeTimeout = 10 * time.Second

// runWebServe 是 RunWeb 的生产实现：effectiveConfig → serve.Options →
// serve.Run。bind 由命令层解析后经 Options.webBind 传入（RunWeb 签名只携带
// port/noOpen）；AllowEngineDrift 沿根命令 --allow-engine-drift 指针
// （driftAllowed）透传——漂移拒绝/放行留痕语义在 engine 层，此处零改动。
// Stdout/Stderr 原样下传（serve 侧 eventbus 桥失败留痕面在 stderr）。
func runWebServe(ctx context.Context, o Options, port int, noOpen bool) error {
	cfg, err := effectiveConfig(o)
	if err != nil {
		return err
	}
	bind := o.webBind
	if bind == "" {
		bind = "127.0.0.1"
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer serve.CloseSharedBus() // serve 侧总线收口（卫生面；FileStorage 同步发布已持久）
	if !noOpen {
		go openWhenServing(ctx, fmt.Sprintf("http://%s:%d", bind, port), o.stderr())
	}
	return serve.Run(ctx, serve.Options{
		Config:           &cfg,
		Workspace:        o.Workspace,
		SessionsRoot:     o.SessionsRoot,
		Bind:             bind,
		Port:             port,
		NoOpen:           noOpen,
		AllowEngineDrift: o.driftAllowed(),
		Stdout:           o.Stdout,
		Stderr:           o.Stderr,
	})
}

// openWhenServing 轮询 /api/healthz 至可服务（上限 probeTimeout）后打开
// 系统浏览器；超时/取消/打开失败一律 stderr 一行留痕并返回——非致命。
func openWhenServing(ctx context.Context, url string, stderr io.Writer) {
	if stderr == nil {
		stderr = io.Discard
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(probeTimeout)
	for {
		resp, err := client.Get(url + "/api/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			_, _ = fmt.Fprintf(stderr, "mulix web: 服务探活超时，跳过浏览器打开（%s）\n", url)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	select {
	case <-ctx.Done():
		return // 关停已开始：不再拉起浏览器
	default:
	}
	if err := openBrowser(url); err != nil {
		_, _ = fmt.Fprintf(stderr, "mulix web: 打开浏览器失败（非致命）: %v\n", err)
	}
}

// openBrowser 用系统浏览器打开 url（平台探测：Windows rundll32 url.dll,
// FileProtocolHandler / darwin open / 其余 xdg-open）。Start 后台 Wait——
// 不阻塞探活 goroutine，也防子进程僵尸/句柄滞留。
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
