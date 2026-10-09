package cli

// lint_test.go——mulix lint layers 子命令面（M17）：违规 → Execute 返回
// error（main → exit 1）+ stdout 违规报告；干净树 → nil error；--fix 重生成
// 棘轮基线后原违规被基线覆盖（减/平 → 通过）。
//
// fixture 不含注入点标记字面量（规则族见 internal/lint/rules.go）——本文件
// 保持 P1 repolint 扫描面干净，违规面用 layer-import。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLintFixture 在 dir 下落 mulix 形状的最小 fixture 树。
func writeLintFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// runLintCommand 以给定参数装配并执行根命令，返回 stdout 与 Execute 错误。
func runLintCommand(t *testing.T, workspace string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	opts := Options{Workspace: workspace, Stdout: &buf}
	cmd := NewRootCommand(opts)
	cmd.SetArgs(args)
	cmd.SetOut(&buf)
	err := cmd.Execute()
	return buf.String(), err
}

// 违规树 → Execute 返回 error（exit 1 语义）+ stdout 报告含 file:line 与规则名。
func TestLintLayersViolationExitsNonZero(t *testing.T) {
	root := t.TempDir()
	writeLintFixture(t, root, map[string]string{
		"go.mod":                       "module mulix\n",
		"internal/kernel/k.go":         "package kernel\n",
		"internal/tools/faketool/t.go": "package faketool\n\nimport _ \"mulix/internal/kernel\"\n",
	})
	out, err := runLintCommand(t, root, "lint", "layers")
	if err == nil {
		t.Fatalf("Execute = nil error, want failure (exit 1 语义); stdout=%s", out)
	}
	if !strings.Contains(err.Error(), "1 项分层违规") {
		t.Fatalf("err = %v, want 违规计数 1", err)
	}
	if !strings.Contains(out, "internal/tools/faketool/t.go:3") ||
		!strings.Contains(out, "layer-import") {
		t.Fatalf("stdout = %s, want file:line 与规则名", out)
	}
}

// 干净树 → nil error（exit 0 语义）。
func TestLintLayersCleanTreePasses(t *testing.T) {
	root := t.TempDir()
	writeLintFixture(t, root, map[string]string{
		"go.mod":               "module mulix\n",
		"internal/cli/stub.go": "package cli\n",
	})
	out, err := runLintCommand(t, root, "lint", "layers")
	if err != nil {
		t.Fatalf("Execute = %v, want nil (干净树 exit 0); stdout=%s", err, out)
	}
	if !strings.Contains(out, "0 项违规") {
		t.Fatalf("stdout = %s, want 零违规摘要", out)
	}
}

// --fix 重生成棘轮基线（入库文件）；再跑同一棵树 → 基线内通过（减/平 → 通过）。
func TestLintLayersFixRegeneratesRatchet(t *testing.T) {
	root := t.TempDir()
	writeLintFixture(t, root, map[string]string{
		"go.mod":                       "module mulix\n",
		"internal/kernel/k.go":         "package kernel\n",
		"internal/tools/faketool/t.go": "package faketool\n\nimport _ \"mulix/internal/kernel\"\n",
	})
	out, err := runLintCommand(t, root, "lint", "layers", "--fix")
	if err != nil {
		t.Fatalf("Execute --fix = %v, want nil; stdout=%s", err, out)
	}
	ratchet := filepath.Join(root, "docs", "lint", "ratchet.json")
	data, readErr := os.ReadFile(ratchet)
	if readErr != nil {
		t.Fatalf("read ratchet.json: %v (--fix 未生成基线)", readErr)
	}
	if !strings.Contains(string(data), "internal/tools/faketool/t.go") {
		t.Fatalf("ratchet.json = %s, want faketool 条目", string(data))
	}
	if !strings.Contains(out, "棘轮基线已写入") {
		t.Fatalf("stdout = %s, want 基线写入提示", out)
	}

	// 第二跑：基线内既有债务 → 通过。
	out, err = runLintCommand(t, root, "lint", "layers")
	if err != nil {
		t.Fatalf("Execute after --fix = %v, want nil (基线内); stdout=%s", err, out)
	}
}

// 非模块根（缺 go.mod）→ error（提示在模块根运行）。
func TestLintLayersRequiresGoMod(t *testing.T) {
	root := t.TempDir()
	if _, err := runLintCommand(t, root, "lint", "layers"); err == nil {
		t.Fatal("Execute without go.mod = nil error, want error")
	}
}
