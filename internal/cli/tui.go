package cli

import (
	"context"
	"fmt"

	"mulix/internal/control"
	"mulix/internal/sessionx"
	"mulix/internal/tui"
)

// cliSwapper 是 tui.SessionSwapper 的组合根实现：持装配参数与当前会话句柄，
// Open 复用 openSession 单一装配路径（TUI /new /resume 与启动同源——防接线
// 分叉）。事件通道归 tui 所有（换绑排空在 tui 侧）；tui 只见接口。
type cliSwapper struct {
	o   Options
	cur *sessionHandle // 当前会话（nil = 已收口且不可恢复）
}

// CloseCurrent 关当前会话（收尾 publish 入 tui 的 sink 通道；nil 安全）。
func (s *cliSwapper) CloseCurrent() { s.cur.close() }

// Open 以同 sink 开新/续链会话并返回新控制面与 mulix 会话 id。失败时旧会话
// 已不可恢复（CloseCurrent 先行）——cur 置 nil。
func (s *cliSwapper) Open(resumeID string, sink control.Sink) (tui.SessionController, string, error) {
	h, _, err := openSession(s.o, resumeID, true, sink)
	if err != nil {
		s.cur = nil
		return nil, "", err
	}
	s.cur = h
	return h.ctrl, h.id, nil
}

// tuiDoctorLines 预合成 TUI /doctor 回调（doctorProbes/doctorChecks 均为 cli
// 未导出——禁止整体搬迁 doctor.go，经闭包把「探针注入 + 检查执行 + 行格式化」
// 打包注入 tui.Options.Doctor；与 CLI doctor 同一检查清单与注入面）。
func tuiDoctorLines(o Options) func() []string {
	return func() []string {
		probes := realDoctorProbes(o)
		if o.doctorProbes != nil { // 与 CLI 同一注入面（T29 评审 M-2）
			probes = *o.doctorProbes
		}
		checks := doctorChecks(probes, o)
		lines := make([]string, len(checks))
		for i, c := range checks {
			lines[i] = fmt.Sprintf("[doctor] %s: %s — %s", c.Name, c.Status, c.Detail)
		}
		return lines
	}
}

// tuiListSessions 预合成 TUI /resume 选择器回调（023 FR-009；tuiDoctorLines
// 同款注入形态——sessionx 未导出面收敛在组合根）：sessionx.ScanSessions 摘要
// 投影 tui.SessionSummary。口径与 resolveResume 的 "@" 缺省一致——只列当前
// 工作区且引擎 id 已就位的会话（无链会话不可续——Resume("") 会静默新开；跨
// 工作区续链需回原工作区，resolveResume 同语义）；排序沿用 ScanSessions（新
// 在前）。Scan 失败降级为空表（选择器是便利视图——错误面在 /resume <id> 直
// 连表单与启动恢复门全量呈现；签名无 error 回程，不静默改写语义）。
func tuiListSessions(o Options) func() []tui.SessionSummary {
	return func() []tui.SessionSummary {
		infos, _, err := sessionx.ScanSessions(o.SessionsRoot)
		if err != nil {
			return nil
		}
		out := make([]tui.SessionSummary, 0, len(infos))
		for _, in := range infos {
			if in.EngineID == "" {
				continue // 引擎 init 未到即退出——无链可续
			}
			if o.Workspace != "" && in.Workspace != o.Workspace {
				continue // 续链需回原工作区（resolveResume 同语义）
			}
			out = append(out, tui.SessionSummary{
				ID: in.ID, EngineID: in.EngineID, Model: in.Model,
				Workspace: in.Workspace, CreatedAt: in.CreatedAt, Unattended: in.Unattended,
			})
		}
		return out
	}
}

// RunTUI 生产 TUI 入口（root 默认命令；attended 会话）。装配顺序：withDefaults
// → NewSink → openSession（初始会话绑定 sink）→ cliSwapper → tui.Run。
func RunTUI(ctx context.Context, o Options, resumeID string) error {
	o = withDefaults(o)
	sink := tui.NewSink()
	h, _, err := openSession(o, resumeID, true, sink)
	if err != nil {
		return err
	}
	sw := &cliSwapper{o: o, cur: h}
	// 收口「当前」会话而非启动时的 h：/new //resume 换绑后 sw.cur 已指向
	// 新控制器——不收口则宿主不 Close（mcp-config 临时文件泄漏）。闭包延迟
	// 取 cur（defer 语句的接收者求值陷阱：直接 defer sw.cur.close() 会钉死
	// 启动句柄）。
	defer func() { sw.cur.close() }()
	return tui.Run(tui.Options{
		UsageRoot:    o.UsageRoot,
		Doctor:       tuiDoctorLines(o),
		Sink:         sink,
		ListSessions: tuiListSessions(o),
	}, h.ctrl, h.id, sw, sink.Events())
}
