package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DecodeLine 把引擎 stdout 的一行 JSON 解码为归一化 Event（协议文档 §0/§7）。
//
// 红线与边界：
//   - 未知顶层 type / system 未知 subtype / stream_event（P1 关闭留位）→
//     (nil, nil)——跳过不报错（未知字段容忍红线的类型面）；
//   - 非法 JSON / 空行 / 缺顶层 type → 错误（流损坏，fail-closed；
//     与「未知 type 跳过」是两类故障）；
//   - 已知事件的未知字段一律容忍（encoding/json 默认行为）；
//   - 噪音类（informational/status/thinking_tokens）→ KindNoise，无结构化
//     载荷，Raw 保留原始行。
//
// envelope 的 session_id/uuid 按事件自带字段填充（几乎所有事件都有）。
func DecodeLine(line []byte) (*Event, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("engine: empty line")
	}
	var probe struct {
		Type    string `json:"type"`
		SubType string `json:"subtype"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, fmt.Errorf("engine: decode line: %w", err)
	}
	if probe.Type == "" {
		return nil, fmt.Errorf("engine: line missing top-level type")
	}

	// envelope（session_id/uuid 在各分支统一抽取）
	var env struct {
		SessionID string `json:"session_id"`
		UUID      string `json:"uuid"`
	}
	_ = json.Unmarshal(trimmed, &env) // 已知合法 JSON；字段缺失零值即可

	ev := &Event{Raw: append([]byte(nil), trimmed...), SessionID: env.SessionID, UUID: env.UUID}

	switch probe.Type {
	case "system":
		switch probe.SubType {
		case "init":
			var init InitInfo
			if err := json.Unmarshal(trimmed, &init); err != nil {
				return nil, fmt.Errorf("engine: decode init: %w", err)
			}
			ev.Kind, ev.Init = KindInit, &init
			return ev, nil
		case "hook_started", "hook_response":
			var hook HookInfo
			if err := json.Unmarshal(trimmed, &hook); err != nil {
				return nil, fmt.Errorf("engine: decode hook: %w", err)
			}
			ev.Hook = &hook
			if probe.SubType == "hook_started" {
				ev.Kind = KindHookStarted
			} else {
				ev.Kind = KindHookResponse
			}
			return ev, nil
		case "permission_denied":
			var denied PermissionDeniedInfo
			if err := json.Unmarshal(trimmed, &denied); err != nil {
				return nil, fmt.Errorf("engine: decode permission_denied: %w", err)
			}
			ev.Kind, ev.Denied = KindPermissionDenied, &denied
			return ev, nil
		case "thinking_tokens", "informational", "status":
			ev.Kind = KindNoise
			return ev, nil
		default:
			// 未知 subtype：跳过不报错（§0 红线）
			return nil, nil
		}
	case "assistant":
		var wrapper struct {
			Message AssistantMsg `json:"message"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			return nil, fmt.Errorf("engine: decode assistant: %w", err)
		}
		msg := wrapper.Message
		ev.Kind, ev.Assistant = KindAssistant, &msg
		return ev, nil
	case "user":
		var wrapper struct {
			Message       ToolResultMsg   `json:"message"`
			ToolUseResult json.RawMessage `json:"tool_use_result"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			return nil, fmt.Errorf("engine: decode user: %w", err)
		}
		msg := wrapper.Message
		msg.RawToolUseResult = wrapper.ToolUseResult // 容忍缺失（nil）
		ev.Kind, ev.ToolResult = KindToolResult, &msg
		return ev, nil
	case "result":
		var result ResultInfo
		if err := json.Unmarshal(trimmed, &result); err != nil {
			return nil, fmt.Errorf("engine: decode result: %w", err)
		}
		ev.Kind, ev.Result = KindResult, &result
		return ev, nil
	case "control_request":
		var cr ControlRequest
		if err := json.Unmarshal(trimmed, &cr); err != nil {
			return nil, fmt.Errorf("engine: decode control_request: %w", err)
		}
		ev.Kind, ev.Control = KindControlRequest, &cr
		return ev, nil
	case "stream_event":
		// P1 关闭留位（§7 row 11）：跳过不报错
		return nil, nil
	default:
		// 未知顶层 type：跳过不报错（§0 红线）
		return nil, nil
	}
}
