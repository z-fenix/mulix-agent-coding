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
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("events: connect %s: %w", url, err)
	}
	defer nc.Close()
	sub, err := nc.Subscribe("mulix.mirror.*", func(m *nats.Msg) {
		kind := strings.TrimPrefix(m.Subject, "mulix.mirror.")
		summary := strings.ReplaceAll(string(m.Data), "\n", " ")
		if len(summary) > 200 {
			summary = summary[:200]
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
	<-ctx.Done()
	return nil
}
