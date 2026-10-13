package eventbus

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// ObserveMirrors 是外部观察面(018 FR-103):连接 url、订阅 mulix.mirror.*
// (总线 subject 真身——Emit 统一加 "mulix." 前缀,桥声明 "mirror.<kind>",
// 评审 Critical 修正)、行打印 `<RFC3339> <kind> <payload>`。纯订阅——零写入
// 任何文件(镜像 events.jsonl 1:1 审计面不受影响)。ctx 取消→Unsubscribe+
// Drain+Close 后返回 nil(Drain 异步——Close 截断在途尾行,观察面退出语义
// 不关心尾行,评审 Advisory 认可)。
func ObserveMirrors(ctx context.Context, url string, w io.Writer) error {
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second), nats.NoReconnect())
	if err != nil {
		return fmt.Errorf("events: connect %s: %w", url, err)
	}
	defer nc.Close()
	// 断连可见(019 FR-102):对端(单会话进程)死亡→ClosedHandler 关 channel
	// →错退 fail-visible;用户 Ctrl-C(ctx)路径保持 nil(018 语义不变)。
	// nats.go 签名:ConnHandler = func(*Conn)(无 err 参数、无返回值——评审修正)。
	closed := make(chan struct{})
	nc.SetClosedHandler(func(_ *nats.Conn) {
		close(closed) // nats.go isClosed 守卫——每连接恰好触发一次,无双调用
	})
	sub, err := nc.Subscribe("mulix.mirror.*", func(m *nats.Msg) {
		kind := strings.TrimPrefix(m.Subject, "mulix.mirror.")
		summary := strings.ReplaceAll(string(m.Data), "\n", " ")
		if len(summary) > 200 {
			summary = truncateRunes(summary, 200) // rune 边界安全(019 FR-103)
		}
		_, _ = fmt.Fprintf(w, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339), kind, summary)
	})
	if err != nil {
		return fmt.Errorf("events: subscribe: %w", err)
	}
	nc.Flush() // 订阅注册先行——观察面启动即收新行(无 Flush 存在注册竞态窗口)
	defer func() {
		_ = sub.Unsubscribe()
		_ = nc.Drain()
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-closed:
		return fmt.Errorf("events: 连接已关闭(对端总线不可达——会话进程可能已退出)")
	}
}

// truncateRunes 按字节上限截断但落在 rune 边界上(019 FR-103)。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	out := 0
	i := 0
	for ; i < len(r); i++ {
		out += len(string(r[i]))
		if out > max {
			break
		}
	}
	return string(r[:i])
}
