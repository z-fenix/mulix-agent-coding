package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mulix/internal/engine"
)

// doctor 自检（T9.3；design §10 隐私条）：node → claude CLI → engine.lock
// 一致性 → 凭据 → 工作区可写；输出结构化报告，--redact 脱敏导出。
// 检查项探测全部可注入（doctorProbes）——单测以假探针驱动故障面。

type doctorStatus string

const (
	doctorOK   doctorStatus = "ok"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
)

type doctorCheck struct {
	Name   string       `json:"name"`
	Status doctorStatus `json:"status"`
	Detail string       `json:"detail"`
}

type doctorReport struct {
	Workspace string        `json:"workspace"`
	Checks    []doctorCheck `json:"checks"`
	OK        bool          `json:"ok"`
}

// doctorProbes 是各检查项的探测函数（版本串/来源名，绝不返回凭据值）。
type doctorProbes struct {
	node        func() (string, error) // node --version
	claude      func() (string, error) // claude --version
	engineLock  func() (string, error) // engine.lock 的 pinned cli_version
	credentials func() (string, error) // 凭据来源名（ANTHROPIC_API_KEY|claude 凭据文件）
	workspace   func() error           // 工作区可写探针
}

// realDoctorProbes 生产探针（本机实际执行；错误即检查 fail）。
func realDoctorProbes(o Options) doctorProbes {
	run := func(name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		// WaitDelay（T29 评审 I-1）：cmd.exe shim 被 kill 后孙进程仍握着 stdout
		// 管道——不设 WaitDelay 则 Output() 等到 EOF 才返回，「超时」形同虚设
		//（评审实测 2s 死限拖到 29.3s）。设 2s：kill 后至多再等 2s 收尾。
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.CombinedOutput() // stderr 并入（M-5：CLI 版本可能走 stderr）
		return strings.TrimSpace(string(out)), err
	}
	return doctorProbes{
		node: func() (string, error) {
			if _, err := exec.LookPath("node"); err != nil {
				return "", fmt.Errorf("node 不在 PATH: %w", err)
			}
			return run("node", "--version")
		},
		claude: func() (string, error) {
			if _, err := exec.LookPath("claude"); err != nil {
				return "", fmt.Errorf("claude CLI 不在 PATH: %w", err)
			}
			return run("claude", "--version")
		},
		engineLock: func() (string, error) {
			lock, err := engine.LoadEngineLock(filepath.Join(o.Workspace, "engine.lock"))
			if err != nil {
				return "", err
			}
			return lock.CLIVersion, nil
		},
		credentials: func() (string, error) {
			if os.Getenv("ANTHROPIC_API_KEY") != "" {
				return "ANTHROPIC_API_KEY", nil
			}
			home, _ := os.UserHomeDir()
			for _, p := range []string{
				filepath.Join(home, ".claude", ".credentials.json"),
				filepath.Join(home, ".claude.json"),
			} {
				if _, err := os.Stat(p); err == nil {
					return "claude 凭据文件", nil
				}
			}
			return "", fmt.Errorf("未找到凭据（ANTHROPIC_API_KEY 或 ~/.claude 凭据文件）")
		},
		workspace: func() error {
			probe := filepath.Join(o.Workspace, ".mulix-doctor-probe")
			if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
				return err
			}
			return os.Remove(probe)
		},
	}
}

// doctorChecks 依序执行检查（顺序稳定：node → claude → engine.lock →
// 一致性 → 凭据 → 工作区）。
func doctorChecks(p doctorProbes, o Options) []doctorCheck {
	var checks []doctorCheck

	// node
	nodeV, nodeErr := p.node()
	checks = append(checks, checkOf("node", nodeErr, nodeV))

	// claude
	claudeV, claudeErr := p.claude()
	checks = append(checks, checkOf("claude", claudeErr, claudeV))

	// engine.lock
	lockV, lockErr := p.engineLock()
	detail := fmt.Sprintf("pinned %s", lockV)
	if lockErr != nil {
		detail = lockErr.Error()
	}
	checks = append(checks, doctorCheck{Name: "engine.lock", Status: statusOf(lockErr), Detail: detail})

	// 一致性：claude --version 与 pin 比对。取版本输出的**首个空白 token**
	// 严格相等（T29 评审 I-4：子串匹配会让 pin 2.1.2 误配 2.1.291——恰是
	// doctor 要抓的滞留 pin 漂移场景）。
	cons := doctorCheck{Name: "consistency", Status: doctorOK}
	claudeVer := strings.Fields(claudeV)
	switch {
	case claudeErr != nil || lockErr != nil:
		cons.Status = doctorFail
		cons.Detail = "前置检查未通过，无法比对版本"
	case len(claudeVer) > 0 && claudeVer[0] == lockV:
		cons.Status = doctorOK
		cons.Detail = fmt.Sprintf("claude %s == pinned %s", claudeVer[0], lockV)
	default:
		cons.Status = doctorWarn // 漂移可经 --allow-engine-drift 放行（FR-016）
		cons.Detail = fmt.Sprintf("漂移: claude %s ≠ pinned %s（可用 --allow-engine-drift 放行）", claudeV, lockV)
	}
	checks = append(checks, cons)

	// 凭据（只报来源，绝不报值——设计 §10）
	credSrc, credErr := p.credentials()
	checks = append(checks, checkOf("credentials", credErr, credSrc))

	// 工作区可写
	wsErr := p.workspace()
	checks = append(checks, checkOf("workspace", wsErr, fmt.Sprintf("可写 %s", o.Workspace)))

	return checks
}

func checkOf(name string, err error, okDetail string) doctorCheck {
	if err != nil {
		return doctorCheck{Name: name, Status: doctorFail, Detail: err.Error()}
	}
	return doctorCheck{Name: name, Status: doctorOK, Detail: okDetail}
}

func statusOf(err error) doctorStatus {
	if err != nil {
		return doctorFail
	}
	return doctorOK
}

// reportOK：无 fail 即 OK（warn——如版本漂移——不破坏可用性判定）。
func reportOK(checks []doctorCheck) bool {
	for _, c := range checks {
		if c.Status == doctorFail {
			return false
		}
	}
	return true
}

// redactChecks 脱敏导出（design §10）：home/workspace 绝对路径掩码化。
// 次序（T29 评审 I-2）：**先 workspace 后 home**——workspace 通常在 home 之下，
// 先掩 home 会把 workspace 子串一并吃掉，<workspace> 永不生效。
// 检查项 detail 本就不含凭据值；此处针对路径类信息。
func redactChecks(checks []doctorCheck, o Options, home string) []doctorCheck {
	out := make([]doctorCheck, len(checks))
	home = filepath.Clean(home)
	ws := filepath.Clean(o.Workspace)
	for i, c := range checks {
		d := c.Detail
		if ws != "" && ws != "." {
			d = strings.ReplaceAll(d, ws, "<workspace>")
		}
		if home != "" && home != "." {
			d = strings.ReplaceAll(d, home, "<home>")
		}
		c.Detail = d
		out[i] = c
	}
	return out
}

// runDoctor 执行自检并输出结构化报告；redact 为真时报告掩码化。
// 返回报告与可用性（任一 fail → false，命令层据此退出码 1）。
func runDoctor(o Options, redact bool) (doctorReport, error) {
	o = withDefaults(o)
	probes := realDoctorProbes(o)
	if o.doctorProbes != nil { // 测试注入（确定性）
		probes = *o.doctorProbes
	}
	checks := doctorChecks(probes, o)
	home, _ := os.UserHomeDir()
	report := doctorReport{Workspace: o.Workspace, Checks: checks, OK: reportOK(checks)}
	if redact {
		report.Checks = redactChecks(checks, o, home)
		report.Workspace = "<workspace>"
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	_, perr := fmt.Fprintln(o.stdout(), string(data))
	if perr != nil {
		return report, perr
	}
	return report, nil
}
