// SearchEvents 镜像事件文本面 BM25 检索(013 FR-103):文本面提取
// (assistant_text/tool_result/notice 的内容字段)→ BM25 索引 → top-k。
package sessionx

import (
	"fmt"

	"mulix/internal/event"
	"sort"
	"strconv"

	"mulix/internal/textutil"
)

// EventHit 是一次检索命中(行序位/kind/文本摘录/评分)。
type EventHit struct {
	LineNo  int64
	Kind    string
	Excerpt string
	Score   float64
}

// SearchEvents 镜像事件文本面 BM25 检索:返回命中(行序位/kind/文本摘录,
// 截 80 字符);空结果不报错。FR-103。
func SearchEvents(store *Store, query string, k int) ([]EventHit, error) {
	events, err := store.ReadAllEvents()
	if err != nil {
		return nil, fmt.Errorf("sessionx: search read: %w", err)
	}
	ix := textutil.NewBM25Index()
	texts := make([]string, len(events))
	for i, ev := range events {
		texts[i] = eventTextOf(ev)
		ix.Add(fmt.Sprintf("%d", i+1), texts[i])
	}
	scores := ix.Score(query)
	var hits []EventHit
	for id, score := range scores {
		ln, _ := strconv.ParseInt(id, 10, 64)
		excerpt := texts[ln-1]
		if len(excerpt) > 80 {
			excerpt = excerpt[:80] + "…"
		}
		kind, _ := EventKindOf(events[ln-1])
		hits = append(hits, EventHit{LineNo: ln, Kind: string(kind), Excerpt: excerpt, Score: score})
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].Score > hits[b].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// eventTextOf 提取事件文本面(kind 分支——assistant_text/tool_result/notice)。
func eventTextOf(ev any) string {
	switch e := ev.(type) {
	case event.AssistantTextEvent:
		return e.Text
	case event.ToolResultEvent:
		return e.Content
	case event.NoticeEvent:
		return e.Content
	default:
		return ""
	}
}
