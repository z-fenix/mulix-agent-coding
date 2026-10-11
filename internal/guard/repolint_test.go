package guard

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoSecondSystemPromptPath pins（design §8.1 护栏 / §5.3 第 6 条；T10.2
// 落地为 CI 可跑的静态规则——repolint 棘轮语义）：
// "--append-system-prompt" 的生产者唯一 = BootComposer（internal/cache/boot.go）。
// 现有引用逐文件钉死（下表 allowlist）：
//   - internal/cache/boot.go        唯一生产者（组合 prompt 值）
//   - internal/engine/host.go       BootConfig 契约字段文档
//   - internal/engine/claudehost.go 唯一 argv 写入方（消费 cfg 字段，不自产内容）
//   - internal/engine/claudehost_test.go 透传钉
//
// 任何**新增**文件出现该字符串 = 出现第二条 system prompt 注入路径 → 红线。

// repolintFlagAllowlist 是 --append-system-prompt 引用面的 P1 allowlist
// (原 TestNoSecondSystemPromptPath 局部变量;T10-② 提取为包级供交叉断言
// ——仅测试文件,零生产面)。P3 008:追加第 8 键 allowlist_cross_test.go
// (交叉断言测试文件自身含 marker 字面量——P1 walk 扫 _test.go;P1 侧是
// 超集,子集断言不受影响)。
var repolintFlagAllowlist = map[string]bool{
	"internal/cache/boot.go":                 true,
	"internal/engine/host.go":                true,
	"internal/engine/claudehost.go":          true,
	"internal/engine/claudehost_test.go":     true,
	"internal/guard/repolint_test.go":        true,
	"internal/guard/allowlist_cross_test.go": true,
	// M17（Task 10）：本规则并入 internal/lint（design 002 §6）——规则定义处
	// 与其测试必然包含标记字面量（本注释既定程序：「如属合法消费面请先更新
	// 本规则的 allowlist 并过评审」）。生产扫描仍由本测试与本工具双保险。
	"internal/lint/rules.go":     true,
	"internal/lint/lint_test.go": true,
}

func TestNoSecondSystemPromptPath(t *testing.T) {
	const marker = "--append-system-prompt"
	allowed := repolintFlagAllowlist

	var offenders []string
	root := "../.." // 测试进程 cwd = internal/guard → 仓库根
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "bin" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if allowed[rel] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), marker) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("--append-system-prompt 出现第二生产者（design §8.1 红线；"+
			"如属合法消费面请先更新本规则的 allowlist 并过评审）: %v", offenders)
	}
}

// TestEngineLockPinMatchesDoctorContract pins：engine.lock 的契约键与
// check-engine-lock.sh（T10.2）的键校验不能漂移。
func TestEngineLockPinMatchesDoctorContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "engine.lock"))
	if err != nil {
		t.Skipf("engine.lock 不在仓库根（跳过）: %v", err)
	}
	for _, key := range []string{"cli_version", "pinned_at", "init_version_field"} {
		if !regexp.MustCompile(`"` + key + `"`).Match(raw) {
			t.Fatalf("engine.lock 缺少契约键 %q（check-engine-lock.sh 会拒绝）", key)
		}
	}
}
