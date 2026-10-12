package sessionx

import (
	"strings"
	"testing"

	"mulix/internal/event"
)

// TestSearchEventsHitsAndEmpty 钉 013 FR-103:文本面检索命中(序位+摘录);
// 空结果不报错。
func TestSearchEventsHitsAndEmpty(t *testing.T) {
	root := t.TempDir()
	st, err := Create(root, Options{ID: "search", Workspace: "w"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st.Append(event.AssistantTextEvent{Seq: 1, Text: "解决缓存穿透的方案"})
	st.Append(event.NoticeEvent{Seq: 2, Content: "无关内容"})
	st.Append(event.AssistantTextEvent{Seq: 3, Text: "另一段缓存雪崩"})
	defer st.Close()
	hits, err := SearchEvents(st, "缓存穿透", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("must hit 缓存穿透")
	}
	if !strings.Contains(hits[0].Excerpt, "缓存穿透") {
		t.Fatalf("excerpt = %q", hits[0].Excerpt)
	}
	empty, err := SearchEvents(st, "不存在词汇xyz", 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty search = %v, %v; want empty no error", empty, err)
	}
}
