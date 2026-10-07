// Package mulixusage 是内置工具 mulix_usage：返回当前会话的缓存记账汇总
// （命中率/tokens/成本/节省）——产品功能（模型可自查缓存经济性）兼 MCP
// 全链路验证。经 init() 自注册到 registry（design §5.1.4；cli 空白导入
// 本包即完成注册）。
//
// 接线：SetSource 由 cli 在会话启动时注入（跨进程权威口径
// cache.ReadUsageReport）；未接线时 Execute fail-closed 明确报错。
package mulixusage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"mulix/internal/cache"
	"mulix/internal/registry"
)

var (
	srcMu  sync.Mutex
	source func() (cache.Report, error)
)

// SetSource 注入记账汇总提供者（cli 会话启动时调用；传 nil 恢复未接线态）。
// 写侧约定在会话生命周期边界（无并发重入）；读侧（Execute）受锁保护。
func SetSource(fn func() (cache.Report, error)) {
	srcMu.Lock()
	defer srcMu.Unlock()
	source = fn
}

func init() { registry.Register(&usageTool{}) }

// usageTool 是 mulix_usage 的 registry.Tool 实现（无输入；只读）。
type usageTool struct{}

func (t *usageTool) Name() string { return "mulix_usage" }

func (t *usageTool) Description() string {
	return ("查询当前 mulix 会话的缓存记账汇总（JSON）：命中率（session_hit，" +
		"加权口径 read/(uncached+write+read)）、四类 token 计数、成本（cost，" +
		"§8.2 公式）、节省（saving，相对全未命中）、冷回合数。记账口径为已收口" +
		"回合；turns 与镜像 turn_started 计数在 send_failed 回合存在时可能略有" +
		"差异（Meter 只记到达 usage 的回合）。")
}

// Schema 无输入工具：{"type":"object","properties":{}}（go-sdk 文档形态）。
func (t *usageTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t *usageTool) ReadOnly() bool { return true }

// Execute 返回会话记账汇总 JSON（跨进程权威口径；进程内实时口径差异见
// Description——模型侧看到的 turns/费用以持久记账文件对账为准）。
func (t *usageTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	srcMu.Lock()
	fn := source
	srcMu.Unlock()
	if fn == nil {
		return "", fmt.Errorf("mulix_usage not wired: no session meter source (unattended?)")
	}
	rep, err := fn()
	if err != nil {
		return "", fmt.Errorf("mulix_usage: %w", err)
	}
	payload, err := json.Marshal(rep)
	if err != nil {
		return "", fmt.Errorf("mulix_usage: marshal report: %w", err)
	}
	return string(payload), nil
}
