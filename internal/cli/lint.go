package cli

// mulix lint layers（M17 / design 002 §6）：分层检查门禁子命令。
//
// 口径：repoRoot = Options.Workspace（命令在模块根运行——CI 于 checkout 根
// 执行 go run ./cmd/mulix-cli lint layers，自食其力）。棘轮基线默认在
// docs/lint/ratchet.json（入库文件；--fix 重生成，只紧不松）。违规 → 返回
// error（main.go 打印并 exit 1）；干净 → nil（exit 0）。

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"mulix/internal/lint"
)

// ratchetRelPath 是棘轮基线相对模块根的位置（正斜杠字面量——输出面确定性）。
const ratchetRelPath = "docs/lint/ratchet.json"

// newLintCommand 装配 mulix lint 子命令组（root.go 注册）。
func newLintCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "分层检查工具（design 002 §6：导入矩阵 / 注入点唯一 / 文件职责棘轮）",
	}
	var fix bool
	layers := &cobra.Command{
		Use:   "layers",
		Short: "分层规则检查（违规 → exit 1；--fix 重生成棘轮基线）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLintLayers(*opts, fix)
		},
	}
	layers.Flags().BoolVar(&fix, "fix", false, "重生成棘轮基线（max 合并只紧不松；人工检查后入库）")
	cmd.AddCommand(layers)
	return cmd
}

// runLintLayers 跑分层检查并输出报告；--fix 走基线重生成（显式人工动作，
// 不以违规数失败）。常规路径有违规返回 error（exit 1 语义）。
func runLintLayers(o Options, fix bool) error {
	root := o.Workspace
	ratchetPath := filepath.Join(root, filepath.FromSlash(ratchetRelPath))
	w := o.stdout()
	if fix {
		recorded, err := lint.GenerateRatchet(root, ratchetPath)
		if err != nil {
			return err
		}
		if err := printViolations(w, recorded); err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "棘轮基线已写入 %s（本次扫描 %d 项违规；max 合并只紧不松——人工检查后入库）\n",
			ratchetRelPath, len(recorded))
		return err
	}
	violations, err := lint.RunLayers(root, ratchetPath)
	if err != nil {
		return err
	}
	if err := printViolations(w, violations); err != nil {
		return err
	}
	if len(violations) > 0 {
		if _, err := fmt.Fprintf(w, "分层检查：%d 项违规\n", len(violations)); err != nil {
			return err
		}
		return fmt.Errorf("lint: %d 项分层违规", len(violations))
	}
	_, err = fmt.Fprintln(w, "分层检查通过：0 项违规")
	return err
}

// printViolations 逐条输出 file:line: [rule] detail。
func printViolations(w io.Writer, violations []lint.Violation) error {
	for _, v := range violations {
		if _, err := fmt.Fprintf(w, "%s:%d: [%s] %s\n", v.File, v.Line, v.Rule, v.Detail); err != nil {
			return err
		}
	}
	return nil
}
