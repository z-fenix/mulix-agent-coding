package cli

// Task 8（2026-10-18-web-ui-mvp）——`mulix web` 命令接线测试。
//
// 测试形态：纯注入捕获（RunWeb 替身），无 helper-process——serve 无子进程面。
// 端口/地址的「config 默认流转、flag 显式覆盖」经 Options.Config 注入钉死
// （effectiveConfig 注入优先），不依赖宿主机 ~/.mulix 配置——内置默认值
// （port=3180 / bind=127.0.0.1）由 internal/config/web_test.go
// TestWebConfigDefaults 另行钉死，本测试只钉流转矩阵。

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"mulix/internal/config"
)

// TestWebCommandWiring pins `mulix web` 接线矩阵（RunWeb 注入捕获）：
//   - 端口：内置默认 3180 透传 / --port 覆盖 / config [web].port 流转 / flag 胜 config；
//   - 地址：config [web].bind 流转 / --bind 覆盖（经 Options.webBind 传入 RunWeb）；
//   - --no-open 透传；
//   - Stdout 打印服务 URL（启动横幅）。
func TestWebCommandWiring(t *testing.T) {
	type captured struct {
		port   int
		noOpen bool
		bind   string
	}
	cases := []struct {
		name string
		args []string
		cfg  *config.Config
		want captured
		url  string // Stdout 须含的服务 URL
	}{
		{
			name: "内置默认端口 3180 透传",
			args: []string{"web"},
			cfg:  &config.Config{Web: config.WebConfig{Port: 3180, Bind: "127.0.0.1"}},
			want: captured{port: 3180, bind: "127.0.0.1"},
			url:  "http://127.0.0.1:3180",
		},
		{
			name: "flag 覆盖端口 + no-open 透传",
			args: []string{"web", "--port", "4000", "--no-open"},
			cfg:  &config.Config{Web: config.WebConfig{Port: 3180, Bind: "127.0.0.1"}},
			want: captured{port: 4000, noOpen: true, bind: "127.0.0.1"},
			url:  "http://127.0.0.1:4000",
		},
		{
			name: "config [web].port 默认流转（无 flag）",
			args: []string{"web"},
			cfg:  &config.Config{Web: config.WebConfig{Port: 4001, Bind: "127.0.0.1"}},
			want: captured{port: 4001, bind: "127.0.0.1"},
			url:  "http://127.0.0.1:4001",
		},
		{
			name: "flag 胜 config（端口与地址同测）",
			args: []string{"web", "--port", "4020", "--bind", "127.0.0.2"},
			cfg:  &config.Config{Web: config.WebConfig{Port: 4001, Bind: "192.168.1.5"}},
			want: captured{port: 4020, bind: "127.0.0.2"},
			url:  "http://127.0.0.2:4020",
		},
		{
			name: "config [web].bind 流转（无 flag）",
			args: []string{"web"},
			cfg:  &config.Config{Web: config.WebConfig{Port: 3180, Bind: "127.0.0.2"}},
			want: captured{port: 3180, bind: "127.0.0.2"},
			url:  "http://127.0.0.2:3180",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			var got captured
			called := false
			opts := Options{
				Stdout: &out, Stderr: &errOut,
				Workspace: t.TempDir(), SessionsRoot: t.TempDir(), UsageRoot: t.TempDir(),
				Config: tc.cfg,
				RunWeb: func(_ context.Context, o Options, port int, noOpen bool) error {
					called = true
					got = captured{port: port, noOpen: noOpen, bind: o.webBind}
					return nil
				},
			}
			cmd := NewRootCommand(opts)
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute %v: %v", tc.args, err)
			}
			if !called {
				t.Fatal("RunWeb 未被调用")
			}
			if got != tc.want {
				t.Fatalf("RunWeb 捕获 = %+v, want %+v", got, tc.want)
			}
			if !strings.Contains(out.String(), tc.url) {
				t.Fatalf("stdout 缺服务 URL %q:\n%s", tc.url, out.String())
			}
		})
	}
}

// TestWebCommandHelpRegistered pins web 子命令注册：root --help 命令表可见。
func TestWebCommandHelpRegistered(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errOut, Workspace: t.TempDir(),
		SessionsRoot: t.TempDir(), UsageRoot: t.TempDir()}
	cmd := NewRootCommand(opts)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help: %v", err)
	}
	if !strings.Contains(out.String()+errOut.String(), "web") {
		t.Fatalf("root --help 未含 web 命令:\n%s", out.String()+errOut.String())
	}
}
