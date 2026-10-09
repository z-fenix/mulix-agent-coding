package cli

// M15：mulix instructions 输出来源文件与内容摘要。口径见 instructions.go
// 注：CLI 进程无运行中控制器句柄（控制器活在 TUI/run 进程内，启动快照不落
// 镜像）——按 control.LoadInstructionSnapshots 对 workspace 现读现报。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runInstructionsCmd 执行 `mulix instructions` 并返回 stdout。
func runInstructionsCmd(t *testing.T, ws string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errOut, Workspace: ws,
		SessionsRoot: t.TempDir(), UsageRoot: t.TempDir()}
	cmd := NewRootCommand(opts)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"instructions"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instructions: %v", err)
	}
	return out.String()
}

// TestInstructionsShowsSourcesAndSummary 钉：有指令文件 → 输出来源文件（
// 全路径）、sha256 与内容摘要（含真实内容行）。
func TestInstructionsShowsSourcesAndSummary(t *testing.T) {
	ws := t.TempDir()
	content := "# proj rules\n- rule a\n"
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	out := runInstructionsCmd(t, ws)
	if !strings.Contains(out, "CLAUDE.md") {
		t.Fatalf("output must name the source file:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(ws, "CLAUDE.md")) {
		t.Fatalf("output must show the source path:\n%s", out)
	}
	sum := sha256.Sum256([]byte(content))
	if !strings.Contains(out, hex.EncodeToString(sum[:])) {
		t.Fatalf("output must show the content sha256:\n%s", out)
	}
	if !strings.Contains(out, "- rule a") {
		t.Fatalf("output must include a content summary (real line):\n%s", out)
	}
}

// TestInstructionsEmptyWorkspace 钉：无指令文件 → 提示两个检查对象，退出
// 码 0（信息面命令：缺席不是错误）。
func TestInstructionsEmptyWorkspace(t *testing.T) {
	out := runInstructionsCmd(t, t.TempDir())
	if !strings.Contains(out, "CLAUDE.md") || !strings.Contains(out, "AGENTS.md") {
		t.Fatalf("empty workspace must name the checked files:\n%s", out)
	}
}

// TestInstructionsLongContentTruncated 钉：摘要超长截断并标注总行数（不
// 全文倾倒）。
func TestInstructionsLongContentTruncated(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"),
		[]byte(strings.Repeat("line\n", 30)), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	out := runInstructionsCmd(t, ws)
	if !strings.Contains(out, "截断") {
		t.Fatalf("long content must be marked truncated:\n%s", out)
	}
}
