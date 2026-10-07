package mulixusage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"mulix/internal/cache"
	"mulix/internal/registry"
)

// TestSelfRegistered pins：包导入即 init() 自注册（design §5.1.4）；名字/
// 只读元数据/无输入 schema 符合约定。
func TestSelfRegistered(t *testing.T) {
	found := false
	for _, name := range registry.Registered() {
		if name == "mulix_usage" {
			found = true
		}
	}
	if !found {
		t.Fatal("mulix_usage not self-registered")
	}
}

// TestExecuteUnwired pins：未接线（无会话）时 fail-closed 明确报错，不返回
// 空汇总假数据。
func TestExecuteUnwired(t *testing.T) {
	t.Cleanup(func() { SetSource(nil) })
	SetSource(nil)
	tool := &usageTool{}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("unwired execute = (%v), want not-wired error", err)
	}
}

// TestExecuteReturnsMeterSummary pins：接线后 Execute 返回 Meter 汇总 JSON
// （命中率/tokens/成本/节省字段齐全且值透传）。
func TestExecuteReturnsMeterSummary(t *testing.T) {
	t.Cleanup(func() { SetSource(nil) })
	SetSource(func() (cache.Report, error) {
		return cache.Report{
			Turns: 3, Uncached: 110, Write: 300, Read: 490, Output: 120,
			SessionHit: 0.7, Cost: 1.7, Saving: 1.2, ColdTurns: 1,
		}, nil
	})
	tool := &usageTool{}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rep cache.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output not Report JSON: %v (%s)", err, out)
	}
	if rep.Turns != 3 || rep.Uncached != 110 || rep.Read != 490 ||
		absF(rep.Cost-1.7) > 1e-9 || absF(rep.SessionHit-0.7) > 1e-9 {
		t.Fatalf("summary = %+v", rep)
	}
}

// TestExecuteProviderError pins：提供者错误 → error（IsError 结果由 registry
// 包装层产出）。
func TestExecuteProviderError(t *testing.T) {
	t.Cleanup(func() { SetSource(nil) })
	SetSource(func() (cache.Report, error) { return cache.Report{}, errors.New("disk boom") })
	tool := &usageTool{}
	if _, err := tool.Execute(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "disk boom") {
		t.Fatalf("provider error = %v", err)
	}
}

// TestToolMetadata pins：名字/只读/schema（无输入工具的 SDK 文档形态）。
func TestToolMetadata(t *testing.T) {
	tool := &usageTool{}
	if tool.Name() != "mulix_usage" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !tool.ReadOnly() {
		t.Fatal("usage tool must be read-only")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
