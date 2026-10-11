// Package eventbus 承载 mulix 的事件骨干(P4a 010):内嵌 nats-server
// (JetStream,FileStorage)提供 append-only 事件持久流;进程内 InProcess
// 连接零网络跃迁;五分发语义层见后续任务。
// 依赖红线修订:NATS 为经批准的首个第三方运行时依赖(用户裁定 2026-10-11)。
package eventbus

import (
	"fmt"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Bus 是内嵌 NATS 事件骨干的句柄:server + 进程内连接 + JetStream 上下文。
type Bus struct {
	srv *server.Server
	nc  *nats.Conn
	js  nats.JetStreamContext
}

// BusOptions 是 OpenBus 的选项:StoreDir 为 JetStream FileStorage 目录。
type BusOptions struct {
	StoreDir string
}

// OpenBus 启动内嵌 server(不监听 TCP——仅 InProcess 连接)、启用 JetStream、
// 建立进程内连接。
func OpenBus(opts BusOptions) (*Bus, error) {
	srv, err := server.NewServer(&server.Options{
		JetStream: true,
		StoreDir:  opts.StoreDir,
		NoLog:     true,
		Port:      -1, // 不监听 TCP(nats 默认 4222——内嵌形态仅 InProcess 连接)
	})
	if err != nil {
		return nil, fmt.Errorf("eventbus: new server: %w", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * 1e9) {
		return nil, fmt.Errorf("eventbus: server not ready within 10s")
	}
	nc, err := nats.Connect(srv.ClientURL(), nats.InProcessServer(srv))
	if err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("eventbus: connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		srv.Shutdown()
		return nil, fmt.Errorf("eventbus: jetstream: %w", err)
	}
	return &Bus{srv: srv, nc: nc, js: js}, nil
}

// Conn 返回进程内连接(后续任务:发布与订阅)。
func (b *Bus) Conn() *nats.Conn { return b.nc }

// JS 返回 JetStream 上下文(后续任务:Stream 管理)。
func (b *Bus) JS() nats.JetStreamContext { return b.js }

// Close 收口:关闭连接与内嵌 server(Drain 为异步——直接 Close 避免
// Shutdown 截断 Drain;持久化由 JetStream 写路径保证,发布调用已同步返回)。
func (b *Bus) Close() error {
	if b.nc != nil {
		b.nc.Close()
	}
	if b.srv != nil {
		b.srv.Shutdown()
	}
	return nil
}
