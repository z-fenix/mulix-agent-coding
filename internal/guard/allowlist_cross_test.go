package guard

import (
	"testing"

	"mulix/internal/lint"
)

// TestLintAllowlistSubsetOfP1Repolint 钉 T10-②(008 FR-102):lint 的共享
// marker(--append-system-prompt)allowlist 必须是 P1 repolint 清单的子集
// ——双源清单单侧漂移在此即刻暴露。放 guard 测试:同一测试二进制可见
// repolintFlagAllowlist(测试变量)与 lint.FlagAllowlistFor(导出访问器)。
func TestLintAllowlistSubsetOfP1Repolint(t *testing.T) {
	allow := lint.FlagAllowlistFor("--append-system-prompt")
	if allow == nil {
		t.Fatal("lint must define the shared marker --append-system-prompt")
	}
	assertLintAllowSubset(t, allow)
}

// assertLintAllowSubset 断言 allow ⊆ repolintFlagAllowlist(子集检查与
// 越界报告;子集测试共用——009 FR-104 G2 helper 抽取)。
func assertLintAllowSubset(t *testing.T, allow map[string]bool) {
	t.Helper()
	for f := range allow {
		if !repolintFlagAllowlist[f] {
			t.Errorf("lint allowlist 越界: %q 不在 P1 repolint 清单(双源漂移——按既定程序同步两侧并过评审)", f)
		}
	}
}

// TestLintAllowlistDriftDetected 钉判别力(SC-002):注入越界条目必须失败
// (对访问器返回的副本注入,验证断言逻辑本身——不污染仓库)。
func TestLintAllowlistDriftDetected(t *testing.T) {
	allow := lint.FlagAllowlistFor("--append-system-prompt")
	injected := map[string]bool{"internal/nonexistent/drift.go": true}
	drifted := false
	for f := range injected {
		if !allow[f] {
			drifted = true
		}
	}
	if !drifted {
		t.Fatal("cross-assertion must detect injected drift (SC-002 判别力)")
	}
}
