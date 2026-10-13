// Command mulix 是 mulix CLI 的入口：命令路由在 internal/cli（design §5.2）。
package main

import (
	"fmt"
	"os"

	"mulix/internal/cli"
	// 内置 MCP 工具经 init() 自注册（design §5.1.4）；父包不反向 import。
	_ "mulix/internal/tools/mulixpermission" // 权限回路回调工具（终评 Critical-1）
	_ "mulix/internal/tools/mulixusage"
)

func main() {
	opts := cli.Options{Workspace: cwd()}
	cmd := cli.NewRootCommand(opts)
	execErr := cmd.Execute()
	// 总线收尾(020 FR-101):单点覆盖全命令——CloseSharedBus 逐 Bus Close
	// (019 已含 endpoint 删除);serve 无桥=busByWS 空→空转 no-op(015 裁定
	// 天然安全)。execErr 仍按原语义退出(收尾不改变退出语义)。
	cli.CloseSharedBus()
	if execErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mulix:", execErr)
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
