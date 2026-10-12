// fork/search 子命令——会话能力 CLI 面(013 FR-101/103;P5a T5)。
package cli

import (
	"fmt"
	"strconv"

	"mulix/internal/sessionx"

	"github.com/spf13/cobra"
)

func newForkCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fork <src-id> <at-seq> <new-id>",
		Short: "从源会话分支新会话(镜像副本至指定事件点)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			atSeq, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("at-seq must be an integer: %w", err)
			}
			src, err := sessionx.OpenStore(sessionx.SessionDir(opts.SessionsRoot, args[0]))
			if err != nil {
				return fmt.Errorf("open source: %w", err)
			}
			defer src.Close()
			forked, err := sessionx.Fork(src, opts.SessionsRoot, args[2], atSeq)
			if err != nil {
				return err
			}
			defer forked.Close()
			cmd.Printf("forked %s (at seq %d) → %s\n", args[0], atSeq, args[2])
			return nil
		},
	}
	return cmd
}

func newSearchCommand(opts *Options) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "镜像事件全文检索(BM25)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sid := sessionID
			if sid == "" {
				// 缺省=最新会话(cache --session 先例——sessions 列表新在前)。
				sessions, _, serr := sessionx.ScanSessions(opts.SessionsRoot)
				if serr != nil {
					return serr
				}
				if len(sessions) == 0 {
					return fmt.Errorf("no sessions found")
				}
				sid = sessions[0].ID
			}
			st, err := sessionx.OpenStore(sessionx.SessionDir(opts.SessionsRoot, sid))
			if err != nil {
				return fmt.Errorf("open session: %w", err)
			}
			defer st.Close()
			hits, err := sessionx.SearchEvents(st, args[0], 20)
			if err != nil {
				return err
			}
			if len(hits) == 0 {
				cmd.Println("(no hits)")
				return nil
			}
			for _, h := range hits {
				cmd.Printf("[%d] %s: %s\n", h.LineNo, h.Kind, h.Excerpt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "会话 id(缺省=最新)")
	return cmd
}
