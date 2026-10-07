package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// ---- 测试替身 ----

func okProbes() doctorProbes {
	return doctorProbes{
		node:        func() (string, error) { return "v22.14.0", nil },
		claude:      func() (string, error) { return "2.0.13 (Claude Code)", nil },
		engineLock:  func() (string, error) { return "2.0.13", nil },
		credentials: func() (string, error) { return "ANTHROPIC_API_KEY", nil },
		workspace:   func() error { return nil },
	}
}

// TestDoctorChecksAllOK pins：全绿时 6 检查项全 ok、报告 OK；顺序钉死
// （node → claude → lock → 一致性 → 凭据 → 工作区）。
func TestDoctorChecksAllOK(t *testing.T) {
	checks := doctorChecks(okProbes(), Options{})
	if len(checks) != 6 {
		t.Fatalf("checks = %d, want 6", len(checks))
	}
	wantOrder := []string{"node", "claude", "engine.lock", "consistency", "credentials", "workspace"}
	for i, c := range checks {
		if c.Name != wantOrder[i] {
			t.Fatalf("check[%d] = %s, want %s", i, c.Name, wantOrder[i])
		}
		if c.Status != doctorOK {
			t.Fatalf("check %s = %s (%s), want ok", c.Name, c.Status, c.Detail)
		}
	}
	if !reportOK(checks) {
		t.Fatal("report must be OK when all checks pass")
	}
}

// TestDoctorFaultInjection pins：各检查项可独立注入故障——单项 fail 传染
// 报告 OK=false，但不吞掉其余检查。
func TestDoctorFaultInjection(t *testing.T) {
	cases := []struct {
		name    string
		breaker func(p *doctorProbes)
		want    string // 期望 fail 的检查名
	}{
		{"node missing", func(p *doctorProbes) { p.node = func() (string, error) { return "", fs.ErrNotExist } }, "node"},
		{"claude missing", func(p *doctorProbes) { p.claude = func() (string, error) { return "", fs.ErrNotExist } }, "claude"},
		{"lock missing", func(p *doctorProbes) { p.engineLock = func() (string, error) { return "", fs.ErrNotExist } }, "engine.lock"},
		{"credentials missing", func(p *doctorProbes) { p.credentials = func() (string, error) { return "", fs.ErrPermission } }, "credentials"},
		{"workspace unwritable", func(p *doctorProbes) { p.workspace = func() error { return fs.ErrPermission } }, "workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := okProbes()
			tc.breaker(&p)
			checks := doctorChecks(p, Options{})
			var sawFail bool
			for _, c := range checks {
				if c.Name == tc.want && c.Status == doctorFail {
					sawFail = true
				} else if c.Name != tc.want && c.Name != "consistency" && c.Status == doctorFail {
					t.Fatalf("check %s unexpectedly failed: %s", c.Name, c.Detail)
				}
			}
			if !sawFail {
				t.Fatalf("check %s must fail", tc.want)
			}
			if reportOK(checks) {
				t.Fatal("report must not be OK with a failing check")
			}
		})
	}
}

// TestDoctorVersionDriftWarn pins：claude --version 与 engine.lock 不一致 →
// warn（可用 --allow-engine-drift 放行），不 fail（warn 不破坏报告 OK）。
func TestDoctorVersionDriftWarn(t *testing.T) {
	p := okProbes()
	p.claude = func() (string, error) { return "2.0.99 (Claude Code)", nil }
	checks := doctorChecks(p, Options{})
	var cons doctorCheck
	for _, c := range checks {
		if c.Name == "consistency" {
			cons = c
		}
	}
	if cons.Status != doctorWarn {
		t.Fatalf("consistency = %s (%s), want warn", cons.Status, cons.Detail)
	}
	if !strings.Contains(cons.Detail, "2.0.99") || !strings.Contains(cons.Detail, "2.0.13") {
		t.Fatalf("detail must name both versions: %s", cons.Detail)
	}
}

// TestDoctorCredentialDetailNeverSecrets pins（§10 红线，T29 评审 I-3 实化）：
// 生产探针拿到凭据值后仍只返回来源名——detail 结构上不可能泄值。
func TestDoctorCredentialDetailNeverSecrets(t *testing.T) {
	secret := "sk-ant-super-secret-value"
	t.Setenv("ANTHROPIC_API_KEY", secret) // 生产探针会摸到值
	probes := realDoctorProbes(Options{Workspace: t.TempDir()})
	src, err := probes.credentials()
	if err != nil {
		t.Fatalf("credentials probe: %v", err)
	}
	if src == secret || strings.Contains(src, "sk-ant") {
		t.Fatalf("credential probe leaked the secret value: %q", src)
	}
	if src != "ANTHROPIC_API_KEY" {
		t.Fatalf("probe must report the SOURCE name, got %q", src)
	}
	checks := doctorChecks(probes, Options{Workspace: t.TempDir()})
	for _, c := range checks {
		if strings.Contains(c.Detail, secret) {
			t.Fatalf("check %s leaks secret: %s", c.Name, c.Detail)
		}
	}
}

// TestDoctorVersionPrefixCollision pins（T29 评审 I-4）：pin 2.1.2 vs 安装
// 2.1.291 → 必须 warn（子串匹配会误报 ok——恰是要抓的滞留 pin 漂移）。
func TestDoctorVersionPrefixCollision(t *testing.T) {
	p := okProbes()
	p.engineLock = func() (string, error) { return "2.1.2", nil }
	p.claude = func() (string, error) { return "2.1.291 (Claude Code)", nil }
	checks := doctorChecks(p, Options{})
	var cons doctorCheck
	for _, c := range checks {
		if c.Name == "consistency" {
			cons = c
		}
	}
	if cons.Status != doctorWarn {
		t.Fatalf("prefix collision must warn, got %s (%s)", cons.Status, cons.Detail)
	}
}

// TestDoctorRedactWorkspaceUnderHome pins（T29 评审 I-2）：workspace 在 home
// 之下（常见态）时 <workspace> 掩码必须生效（先 ws 后 home 的次序钉）。
func TestDoctorRedactWorkspaceUnderHome(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "proj")
	p := okProbes()
	p.workspace = func() error { return fmt.Errorf("open %s: denied", filepath.Join(ws, "engine.lock")) }
	checks := doctorChecks(p, Options{Workspace: ws})
	redacted := redactChecks(checks, Options{Workspace: ws}, home)
	var wsCheck doctorCheck
	for _, c := range redacted {
		if c.Name == "workspace" {
			wsCheck = c
		}
	}
	if strings.Contains(wsCheck.Detail, home) {
		t.Fatalf("redacted detail leaks home: %s", wsCheck.Detail)
	}
	if !strings.Contains(wsCheck.Detail, "<workspace>") {
		t.Fatalf("workspace mask must apply under home: %s", wsCheck.Detail)
	}
}

// TestDoctorRedactMasksPaths pins：--redact 脱敏导出——home/workspace 路径
// 掩码化（design §10 隐私条）；非 redact 保留原样。
func TestDoctorRedactMasksPaths(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "proj")
	p := okProbes()
	p.node = func() (string, error) { return "v22.14.0 (" + filepath.Join(home, "bin", "node.exe") + ")", nil }
	o := Options{Workspace: ws}
	plain := doctorChecks(p, o)
	redacted := redactChecks(plain, o, home)
	if !strings.Contains(plain[0].Detail, home) {
		t.Fatalf("plain detail must contain path: %s", plain[0].Detail)
	}
	if strings.Contains(redacted[0].Detail, home) {
		t.Fatalf("redacted detail leaks home: %s", redacted[0].Detail)
	}
	if !strings.Contains(redacted[0].Detail, "<home>") {
		t.Fatalf("redacted detail must carry mask: %s", redacted[0].Detail)
	}
}

// TestDoctorReportJSON pins：报告结构化（JSON 可解析；ok 字段与检查一致）。
func TestDoctorReportJSON(t *testing.T) {
	checks := doctorChecks(okProbes(), Options{Workspace: "w"})
	data, err := json.Marshal(doctorReport{Workspace: "w", Checks: checks, OK: reportOK(checks)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back doctorReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.OK || len(back.Checks) != 6 || back.Workspace != "w" {
		t.Fatalf("roundtrip mismatch: %+v", back)
	}
}
