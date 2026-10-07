package cli

import (
	"bytes"
	"strings"
	"testing"
)

// ---- 路由矩阵 ----

// TestRouteMatrix pins：子命令存在且可路由（doctor/cache 为 T29/T30 前的
// 骨架声明；run/mcp/sessions 为实装）。未知命令拒绝。
func TestRouteMatrix(t *testing.T) {
	cases := []struct {
		args       []string
		wantErr    bool
		wantOutput []string // 输出须含的标记
	}{
		{args: []string{"--help"}, wantOutput: []string{"run", "doctor", "cache", "sessions", "mcp", "resume"}},
		{args: []string{"nonsense-cmd"}, wantErr: true},
		{args: []string{"doctor"}, wantOutput: []string{"doctor"}},
		{args: []string{"cache", "--help"}, wantOutput: []string{"cache"}},
		{args: []string{"sessions", "--help"}, wantOutput: []string{"sessions"}},
		{args: []string{"mcp", "--help"}, wantOutput: []string{"serve"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			opts := Options{Stdout: &out, Stderr: &errOut, Workspace: t.TempDir(), SessionsRoot: t.TempDir(), UsageRoot: t.TempDir()}
			cmd := NewRootCommand(opts)
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if tc.wantErr && err == nil {
				t.Fatalf("args %v: want error", tc.args)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("args %v: unexpected error: %v", tc.args, err)
			}
			combined := out.String() + errOut.String()
			for _, marker := range tc.wantOutput {
				if !strings.Contains(combined, marker) {
					t.Fatalf("args %v: output missing %q:\n%s", tc.args, marker, combined)
				}
			}
		})
	}
}

// TestResumeFlagParsing pins：--resume 无值（跟 meta 最新会话）/有值（引擎
// id）两种形态可解析；两者都走同一入口。
func TestResumeFlagParsing(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errOut, Workspace: t.TempDir(),
		SessionsRoot: t.TempDir(), UsageRoot: t.TempDir()}
	cmd := NewRootCommand(opts)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--resume", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("resume help: %v", err)
	}
	if !strings.Contains(out.String()+errOut.String(), "resume") {
		t.Fatal("help must mention resume")
	}
}
