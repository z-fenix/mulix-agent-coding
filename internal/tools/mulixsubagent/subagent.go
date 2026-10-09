// Package mulixsubagent 注册子代理编排的三个 MCP 工具（M12/Task 3）：
// mulix_spawn / mulix_poll / mulix_cancel。
//
// 进程拓扑同 mulixpermission：引擎 spawn `mulix mcp serve` 子进程承载本工具；
// Manager 与编排状态在前台进程。桥接 = 本工具 Execute 把 {op, ...} POST 到
// 前台回调端口（MULIX_SUBAGENT_CALLBACK，经 Boot Env 注入——绝不入
// mcp-config，SC-003 字节稳定），应答 JSON 原样透传给引擎。
//
// fail-closed 矩阵（同 mulixpermission）：env 缺失 / 回调不可达 / 非 200 /
// 应答非 JSON → {"ok":false,"error":...}——引擎侧拿到明确错误，绝不悬挂；
// 协议层不失败（返回 nil error，错误走 JSON 文本）。
//
// 分层红线（design 001 §5.3、002 lint 面）：本包不 import 任何内核 internal
// 包——回调应答是 opaque JSON，子代理状态以字符串流转，无需 internal/subagent。
//
// 注册不走 init() 自注册（对 P1 规则 4 的修订）：子代理工具必须按
// SubagentFactory 是否为 nil 条件注册——导出 RegisterConditional，由 cli
// 组合根在配置加载后、registry.NewServer 冻结前调用。
package mulixsubagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"mulix/internal/registry"
)

// callbackTimeout 是单次子代理回调的客户端等待上限。spawn 含子会话组装
// （镜像 store + 引擎进程拉起），比权限裁决慢一个量级；引擎侧 MCP 工具超时
// （MCP_TOOL_TIMEOUT，默认 60s）通常先到——本上限是兜底，不早于引擎放弃。
const callbackTimeout = 120 * time.Second

// registerOnce 钉 RegisterConditional 的恰好一次语义（registry.Register 对
// 重复名 panic；Once 兜底防多次调用——enabled=false 先到即消耗，后续调用
// 无论 enabled 都不再注册，组合根每进程只调一次）。
var registerOnce sync.Once

// RegisterConditional 条件注册三个子代理工具（对 P1 规则 4 的修订）：
// enabled=true 注册 mulix_spawn/mulix_poll/mulix_cancel；enabled=false 整体
// 跳过且永不注册。srv 目前未用（注册进全局注册表；参数保留以钉「必须在
// registry.NewServer 冻结前调用」的时序契约——冻结后注册的工具不进工具面）。
func RegisterConditional(_ *registry.Server, enabled bool) {
	registerOnce.Do(func() {
		if enabled {
			registry.Register(&spawnTool{})
			registry.Register(&pollTool{})
			registry.Register(&cancelTool{})
		}
	})
}

// spawnTool 是 mulix_spawn：委派一个子代理任务（受深度/并发限额约束），
// 返回 sub_session_id。
type spawnTool struct{}

func (t *spawnTool) Name() string { return "mulix_spawn" }

func (t *spawnTool) Description() string {
	return "委派子代理：为独立任务启动一个子会话（深度/并发限额内），返回 sub_session_id；用 mulix_poll 查询状态、mulix_cancel 取消。"
}

func (t *spawnTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"委派给子代理的自包含任务描述"}},"required":["task"]}`)
}

func (t *spawnTool) ReadOnly() bool { return false }

// Execute 转发 spawn 到前台编排器（parent_id/depth 由前台按主会话与调用层
// 判定——工具 schema 只有 task）。
func (t *spawnTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(args, &payload); err != nil || payload.Task == "" {
		return opError("malformed mulix_spawn arguments: task is required"), nil
	}
	return postCallback(map[string]any{"op": "spawn", "task": payload.Task})
}

// pollTool 是 mulix_poll：查询子代理生命周期状态。
type pollTool struct{}

func (t *pollTool) Name() string { return "mulix_poll" }

func (t *pollTool) Description() string {
	return "查询子代理状态：返回 sub_session_id 的生命周期状态（running/completed/cancelled/failed）。"
}

func (t *pollTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"sub_session_id":{"type":"string","description":"mulix_spawn 返回的子会话 id"}},"required":["sub_session_id"]}`)
}

func (t *pollTool) ReadOnly() bool { return true }

func (t *pollTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		SubID string `json:"sub_session_id"`
	}
	if err := json.Unmarshal(args, &payload); err != nil || payload.SubID == "" {
		return opError("malformed mulix_poll arguments: sub_session_id is required"), nil
	}
	return postCallback(map[string]any{"op": "poll", "sub_session_id": payload.SubID})
}

// cancelTool 是 mulix_cancel：取消一个运行中的子代理。
type cancelTool struct{}

func (t *cancelTool) Name() string { return "mulix_cancel" }

func (t *cancelTool) Description() string {
	return "取消子代理：终止 sub_session_id 对应的运行中子会话（级联收口，镜像无孤儿回合）。"
}

func (t *cancelTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"sub_session_id":{"type":"string","description":"mulix_spawn 返回的子会话 id"}},"required":["sub_session_id"]}`)
}

func (t *cancelTool) ReadOnly() bool { return false }

func (t *cancelTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		SubID string `json:"sub_session_id"`
	}
	if err := json.Unmarshal(args, &payload); err != nil || payload.SubID == "" {
		return opError("malformed mulix_cancel arguments: sub_session_id is required"), nil
	}
	return postCallback(map[string]any{"op": "cancel", "sub_session_id": payload.SubID})
}

// postCallback 把一次子代理操作 POST 到前台回调端并透传应答 JSON
// （fail-closed 矩阵：env 缺失 / 不可达 / 非 200 / 应答非 JSON → ok=false）。
func postCallback(body map[string]any) (string, error) {
	endpoint := os.Getenv("MULIX_SUBAGENT_CALLBACK")
	if endpoint == "" {
		return opError("mulix subagent callback not configured (MULIX_SUBAGENT_CALLBACK missing)"), nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return opError("marshal callback payload: " + err.Error()), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), callbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return opError("callback request: " + err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return opError("callback unreachable: " + err.Error()), nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return opError("callback read: " + err.Error()), nil
	}
	if resp.StatusCode != http.StatusOK {
		return opError(fmt.Sprintf("callback status %d", resp.StatusCode)), nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// 非 JSON = 前台异常——fail-closed（不透传未知载荷）
		return opError("callback returned invalid JSON"), nil
	}
	// 透传前台编排器的完整应答 JSON（{ok, sub_session_id?/status?/error?}）
	return string(raw), nil
}

// opError 构造操作失败的应答（fail-closed 的统一出口——与前台回调应答同形：
// {ok:false, error}）。
func opError(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	return string(b)
}
