package cli

import (
	"bytes"
	"strings"
	"testing"

	"mulix/internal/event"
	"mulix/internal/sessionx"
)

// runCliCommand 经 cobra Execute 执行命令,返回合并输出与退出码(0=成功)。
func runCliCommand(t *testing.T, opts Options, args ...string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	opts.Stdout = &out
	opts.Stderr = &errOut
	cmd := NewRootCommand(opts)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return out.String() + errOut.String(), 1
	}
	return out.String() + errOut.String(), 0
}

// TestForkCommand 钉 013 FR-101 CLI 面:fork 命令产出新会话(镜像副本+
// parent 标注);atSeq 非法 → 非零退出+错误输出。
func TestForkCommand(t *testing.T) {
	root := t.TempDir()
	src, err := sessionx.Create(root, sessionx.Options{ID: "src", Workspace: "w"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := src.Append(event.AssistantTextEvent{Seq: int64(i + 1), Turn: 1, Text: "msg"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	src.Close()

	opts := Options{Workspace: root, SessionsRoot: root}
	out, code := runCliCommand(t, opts, "fork", "src", "2", "forked")
	if code != 0 {
		t.Fatalf("fork exit = %d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, "forked") {
		t.Fatalf("fork output must mention new session: %s", out)
	}
	// atSeq 非法 → 非零退出。
	_, code2 := runCliCommand(t, opts, "fork", "src", "0", "bad")
	if code2 == 0 {
		t.Fatal("fork atSeq=0 must fail-closed")
	}
}

// TestSearchCommand 钉 013 FR-103 CLI 面:search 命令输出命中(序位+摘录);
// 空结果不报错。
func TestSearchCommand(t *testing.T) {
	root := t.TempDir()
	st, err := sessionx.Create(root, sessionx.Options{ID: "search", Workspace: "w"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st.Append(event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "解决缓存穿透的方案"})
	st.Close()

	opts := Options{Workspace: root, SessionsRoot: root}
	out, code := runCliCommand(t, opts, "search", "缓存穿透")
	if code != 0 {
		t.Fatalf("search exit = %d; out=%s", code, out)
	}
	if !strings.Contains(out, "缓存穿透") {
		t.Fatalf("search output must contain hit: %s", out)
	}
	empty, code2 := runCliCommand(t, opts, "search", "不存在词汇xyz")
	if code2 != 0 || strings.Contains(empty, "缓存穿透") {
		t.Fatalf("empty search = (%s, %d), want clean empty", empty, code2)
	}
}
