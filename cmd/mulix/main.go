// Command mulix 是 mulix CLI 的入口：命令路由在 internal/cli（design §5.2）。
package main

import (
	"fmt"
	"os"

	"mulix/internal/cli"
	// 内置 MCP 工具经 init() 自注册（design §5.1.4）；父包不反向 import。
	_ "mulix/internal/tools/mulixusage"
)

func main() {
	opts := cli.Options{Workspace: cwd()}
	cmd := cli.NewRootCommand(opts)
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mulix:", err)
		os.Exit(1)
	}
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}
