// Package mulixpermission 注册权限回路工具（design §7.2 实测修订：standalone
// CLI 的 can_use_tool 经 --permission-prompt-tool 以 MCP tools/call 进入
// mulix 自有 stdio MCP 权限工具）。
//
// 进程拓扑：引擎 spawn `mulix mcp serve` 子进程承载本工具；gate/审批桥在
// 前台进程。桥接 = 本工具 Execute 把 {tool_name, input, tool_use_id} POST
// 到前台回调端口（MULIX_PERMISSION_CALLBACK，经 mcp-config env 注入），
// 前台复用 evaluateCanUseTool 裁决核，应答 behavior JSON 原样透传给引擎。
//
// 终评冒烟发现（Critical-1 第三层）：registry 从未注册 "permission" 工具——
// 引擎侧报 "MCP tool mcp__mulix__permission not found"，权限回路整体不可达
// （FakeHost 的 stdout 控制请求传输掩盖了该缺口）。
package mulixpermission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"mulix/internal/registry"
)

func init() { registry.Register(&permissionTool{}) }

// callbackTimeout 是单次审批回调的客户端等待上限——与 Gate 的 120s
// fail-closed 审批窗口对齐并留余量（前台裁决核自带超时语义）。
const callbackTimeout = 125 * time.Second

type permissionTool struct{}

func (t *permissionTool) Name() string { return "permission" }

func (t *permissionTool) Description() string {
	return "mulix 权限回路回调（--permission-prompt-tool）：引擎对每次工具调用发起审批请求，返回 behavior JSON（allow/deny）。"
}

func (t *permissionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"tool_name":{"type":"string","description":"被审批的工具名"},"tool_use_id":{"type":"string","description":"引擎侧 tool_use id"},"input":{"type":"object","description":"工具调用入参"}},"required":["tool_name","tool_use_id"]}`)
}

func (t *permissionTool) ReadOnly() bool { return false }

// Execute 转发审批请求到前台回调端（fail-closed：无回调地址 / 回调不可达 /
// 非 200 / 应答非 behavior JSON → deny + 原因——引擎侧绝不悬挂）。
func (t *permissionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	endpoint := os.Getenv("MULIX_PERMISSION_CALLBACK")
	if endpoint == "" {
		return denyJSON("mulix permission callback not configured (MULIX_PERMISSION_CALLBACK missing)"), nil
	}
	var payload struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	if err := json.Unmarshal(args, &payload); err != nil {
		return denyJSON("malformed can_use_tool arguments: " + err.Error()), nil
	}
	if len(payload.Input) == 0 {
		payload.Input = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return denyJSON("marshal callback payload: " + err.Error()), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), callbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return denyJSON("callback request: " + err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return denyJSON("callback unreachable: " + err.Error()), nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return denyJSON("callback read: " + err.Error()), nil
	}
	if resp.StatusCode != http.StatusOK {
		return denyJSON(fmt.Sprintf("callback status %d", resp.StatusCode)), nil
	}
	var out struct {
		Behavior string `json:"behavior"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || (out.Behavior != "allow" && out.Behavior != "deny") {
		// 非 behavior JSON = 前台异常——fail-closed deny（不透传未知载荷）
		return denyJSON("callback returned invalid behavior"), nil
	}
	// 透传前台裁决核的完整 behavior JSON（allow 带 updatedInput；deny 带 message）
	return string(raw), nil
}

// denyJSON 构造 deny behavior（fail-closed 的统一出口）。
func denyJSON(msg string) string {
	b, _ := json.Marshal(map[string]any{"behavior": "deny", "message": msg})
	return string(b)
}
