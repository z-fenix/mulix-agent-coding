package sessionx

import (
	"encoding/json"
	"fmt"

	"mulix/internal/event"
)

// eventKindOf 返回事件的 kind 标注（镜像记录的 kind 字段）。
func eventKindOf(ev any) (event.Kind, error) {
	switch ev.(type) {
	case event.TurnStartedEvent:
		return event.TurnStarted, nil
	case event.AssistantTextEvent:
		return event.AssistantText, nil
	case event.AssistantThinkingEvent:
		return event.AssistantThinking, nil
	case event.ToolUseRequestedEvent:
		return event.ToolUseRequested, nil
	case event.ToolResultEvent:
		return event.ToolResult, nil
	case event.ApprovalRequestEvent:
		return event.ApprovalRequest, nil
	case event.ApprovalResolvedEvent:
		return event.ApprovalResolved, nil
	case event.UsageEvent:
		return event.Usage, nil
	case event.NoticeEvent:
		return event.Notice, nil
	case event.TurnDoneEvent:
		return event.TurnDone, nil
	case event.EngineExitedEvent:
		return event.EngineExited, nil
	default:
		return "", fmt.Errorf("sessionx: unknown event type %T", ev)
	}
}

// eventFromKind 按 kind 标注还原类型化事件（值语义，便于消费方类型切换）。
// 未知 kind 报错——镜像记录只可能是本包写入的 11 种；出现未知 kind 说明
// 镜像被外部改动，交由恢复路径告警。
func eventFromKind(kind string, raw json.RawMessage) (any, error) {
	switch event.Kind(kind) {
	case event.TurnStarted:
		var ev event.TurnStartedEvent
		return ev, unmarshalInto(&ev, raw)
	case event.AssistantText:
		var ev event.AssistantTextEvent
		return ev, unmarshalInto(&ev, raw)
	case event.AssistantThinking:
		var ev event.AssistantThinkingEvent
		return ev, unmarshalInto(&ev, raw)
	case event.ToolUseRequested:
		var ev event.ToolUseRequestedEvent
		return ev, unmarshalInto(&ev, raw)
	case event.ToolResult:
		var ev event.ToolResultEvent
		return ev, unmarshalInto(&ev, raw)
	case event.ApprovalRequest:
		var ev event.ApprovalRequestEvent
		return ev, unmarshalInto(&ev, raw)
	case event.ApprovalResolved:
		var ev event.ApprovalResolvedEvent
		return ev, unmarshalInto(&ev, raw)
	case event.Usage:
		var ev event.UsageEvent
		return ev, unmarshalInto(&ev, raw)
	case event.Notice:
		var ev event.NoticeEvent
		return ev, unmarshalInto(&ev, raw)
	case event.TurnDone:
		var ev event.TurnDoneEvent
		return ev, unmarshalInto(&ev, raw)
	case event.EngineExited:
		var ev event.EngineExitedEvent
		return ev, unmarshalInto(&ev, raw)
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
}

func unmarshalInto(ev any, raw json.RawMessage) error {
	if err := json.Unmarshal(raw, ev); err != nil {
		return fmt.Errorf("decode %T: %w", ev, err)
	}
	return nil
}
