// sse.go——SSE 流(2026-10-18-web-ui-mvp Task 7)。
//
// 帧格式 = 镜像 wire record 同形 {"kind":"...","ev":{...}}(kind 用
// sessionx.EventKindOf;与 sessionx.Append 落盘记录逐字节同构——前端一套
// 解码器吃回放与实时流)。订阅复用 webSink 非阻塞 fan-out 红线:订阅者缓冲
// 满即被断开(close channel),绝不阻塞泵。
//
// 客户端断开(写失败 / ctx 取消)→ 退订 + release(clientID):非持有者
// no-op;持有者进入 10s 宽限(gracePeriod),期内重连 claim 取消离线计时,
// 到点无人认领 → gone → 审批桥 CancelAll fail-closed(Task 4/5 语义零改动)。
package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"mulix/internal/sessionx"
)

// sessionCookieName 是客户端身份 Cookie(GET / 下发;值即 clientID,
// claim/release/readonly 校验与 SSE 断开释放均消费它)。
const sessionCookieName = "mulix_session"

// sseHeartbeat 心跳节拍(注释行,保活 + 断连探测上界)。包级 var 是测试缝
// (gracePeriod 同惯例;生产不改写)。
var sseHeartbeat = 20 * time.Second

// wireRecordJSON 把类型化事件编成镜像 wire record 同形帧(逐字节同构
// sessionx.Append 的 {"kind","ev"} 记录——kind 常量是安全字符,免转义拼接)。
func wireRecordJSON(ev any) ([]byte, error) {
	kind, err := sessionx.EventKindOf(ev)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]json.RawMessage{
		"kind": json.RawMessage(`"` + kind + `"`),
		"ev":   json.RawMessage(payload),
	})
}

// handleStream GET /{id}/stream:SSE 订阅面。`: connected` 握手注释行 =
// 订阅就绪信号(前端重连与首个事件的确定性同步缝;测试同消费)。
func (a *webAPI) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h, ok := a.mgr.get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown session "+id)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	clientID := clientIDOf(r)
	// 订阅先于握手行(评审 I2):`: connected` 承诺「订阅已就绪」——通道就位
	// 后才 flush 握手,连接到订阅之间的发布不再有丢失窗(缓冲通道在 flush 前
	// 积累,与客户端对账的重叠帧由 (kind,seq) 去重吸收)。
	ch, unsub := h.sink.Subscribe()
	defer func() {
		unsub()
		if clientID != "" {
			h.release(clientID) // 非持有者 no-op;持有者进入宽限
		}
	}()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	hb := time.NewTicker(sseHeartbeat)
	defer hb.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return // 背压断开(webSink 满缓冲)或退订
			}
			frame, err := wireRecordJSON(ev)
			if err != nil { // 未知治理形态:断流优于静默缺帧(fail-closed)
				logf(a.opts.Stderr, "stream %s: %v(断流)", id, err)
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", frame); err != nil {
				return // 客户端断开
			}
			flusher.Flush()
		case <-hb.C:
			if _, err := io.WriteString(w, ": hb\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-a.opts.shutdown: // 优雅关停:断流退订(Shutdown 不取消请求 ctx)
			return
		case <-r.Context().Done():
			return
		}
	}
}
