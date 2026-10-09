package cli

// mulix instructions（M15/FR-109）：输出来源文件与内容摘要。
//
// 口径裁定：CLI 是一次性进程，没有运行中控制器句柄（控制器活在 TUI/run
// 进程内，启动快照不落镜像）——brief「读控制器快照」在此落为按控制器的
// 同一装载函数（control.LoadInstructionSnapshots）对 workspace 现读现报：
// 展示当前指令文件的来源路径、sha256 与内容摘要。启动快照的消费面（watcher
// 与本命令）绝不改 Boot 面（SC-003——system prompt/mcp-config 与 P1 逐字节
// 一致）。

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"mulix/internal/control"
)

// instructionPreviewLines 是内容摘要的最大行数（超出截断并标注总行数——
// 摘要面不全文倾倒）。
const instructionPreviewLines = 20

// newInstructionsCommand 装配 mulix instructions 子命令（root.go 注册）。
func newInstructionsCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "instructions",
		Short: "显示指令文件快照（来源文件、sha256 与内容摘要）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstructions(*opts)
		},
	}
}

// runInstructions 输出指令文件快照（M15/FR-109）：来源文件与内容摘要。
// 无指令文件时提示两个检查对象并正常退出（信息面命令：缺席不是错误）。
func runInstructions(o Options) error {
	w := o.stdout()
	snaps := control.LoadInstructionSnapshots(o.Workspace)
	if len(snaps) == 0 {
		_, err := fmt.Fprintln(w, "工作区无指令文件（CLAUDE.md / AGENTS.md 均不存在）")
		return err
	}
	if _, err := fmt.Fprintf(w, "指令文件快照（workspace: %s）\n", o.Workspace); err != nil {
		return err
	}
	paths := make([]string, 0, len(snaps))
	for p := range snaps {
		paths = append(paths, p)
	}
	sort.Strings(paths) // 确定性输出（路径序 = AGENTS.md 先于 CLAUDE.md）
	for _, p := range paths {
		snap := snaps[p]
		if _, err := fmt.Fprintf(w, "\n%s  %s\n  sha256: %s\n  摘要: %d 行 / %d 字节\n",
			filepath.Base(p), p, snap.SHA256, countContentLines(snap.Content), len(snap.Content)); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, summarizeInstructionContent(snap.Content)); err != nil {
			return err
		}
	}
	return nil
}

// countContentLines 数文本行数（末尾有无换行都按文本行计；空内容 = 0 行）。
func countContentLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// summarizeInstructionContent 组内容摘要块：逐行竖线前缀，超过
// instructionPreviewLines 行截断并标注总行数；空文件显式标注。
func summarizeInstructionContent(content string) string {
	if content == "" {
		return "  （空文件）\n"
	}
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" && strings.HasSuffix(content, "\n") {
		lines = lines[:n-1] // 末尾换行产生的空尾元素不算一行
	}
	shown := lines
	if len(shown) > instructionPreviewLines {
		shown = shown[:instructionPreviewLines]
	}
	var b strings.Builder
	for _, ln := range shown {
		b.WriteString("  | ")
		b.WriteString(ln)
		b.WriteString("\n")
	}
	if len(shown) < len(lines) {
		fmt.Fprintf(&b, "  …（截断，共 %d 行）\n", len(lines))
	}
	return b.String()
}
