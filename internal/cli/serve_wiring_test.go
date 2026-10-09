package cli

// 评审修复轮 1（F1）：serve 进程的组合根工具面——真实 serve 子进程此前只注册
// memory_*，mulix_spawn/poll/cancel 永不进引擎 tools/list。修复 = ServeMCP 在
// 注册面冻结前调 registerServeTools：
//   - 子代理三件套：MULIX_SUBAGENT_CALLBACK 已注入 = control 已接线回调桥
//     （Boot Env 通道随引擎进程树到达 serve 子进程）→ 注册；缺席 = fail-closed
//     （与工具自身 Execute 的 fail-closed 矩阵同向）。
//   - 记忆三件套：[tools] memory_search=true（同源分层配置，cwd=工作区）。
//
// 测试形态（自重执行 helper process）：注册面/Once/全局注册表均进程内共享，
// 「缺席」分支在宿主测试进程内不可钉（Task 3 裁定的根源）——子进程以干净
// 注册表从零走真组合根（effectiveConfig 磁盘加载 + registerServeTools），
// 两分支都可确定性断言。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mulix/internal/registry"
)

// serveFaceWant 是本修复拥有的条件工具面（六件套；registry.Registered() 名序）。
// 注：cli 包经 support.go import mulixusage（setUsageSource 接线）→ 其 init()
// 无条件注册 mulix_usage——生产 mulix 二进制里 main.go 另有 mulixpermission
// 的空白导入（无条件基线层，非本修复的判定面），helper 进程只承载 cli 包的
// 传递导入，故基线只含 mulix_usage。
var serveFaceWant = []string{
	"memory_delete", "memory_list", "memory_save",
	"mulix_cancel", "mulix_poll", "mulix_spawn",
}

// TestServeHelperProcess 是自重执行的 serve 注册面子进程：父进程以
// MULIX_SERVE_HELPER=1 驱动、-test.run 过滤使子进程只跑本测；工作区经
// MULIX_SERVE_HELPER_WS 注入。流程与 ServeMCP 逐行同源（effectiveConfig →
// registerServeTools），仅以「打印冻结前面」替代 NewServer+ServeStdio。
func TestServeHelperProcess(t *testing.T) {
	if os.Getenv("MULIX_SERVE_HELPER") != "1" {
		return
	}
	o := withDefaults(Options{Workspace: os.Getenv("MULIX_SERVE_HELPER_WS")})
	cfg, err := effectiveConfig(o)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	registerServeTools(o, cfg)
	// fmt.Println（非 t.Log）：子进程默认非 verbose，t.Logf 不出流。
	fmt.Println("FACE:" + strings.Join(registry.Registered(), ","))
}

// TestServeToolFaceComposition 钉 serve 组合根的工具面判定：
//   - 回调 env + memory_search=true → 六件套齐（子代理三件套修复在案）；
//   - 干净环境（无回调 env + memory_search=false）→ 六件套全缺（缺席分支，
//     含评审 add-on：memory_search=false 时 memory_* 不注册）。
func TestServeToolFaceComposition(t *testing.T) {
	run := func(t *testing.T, callback bool, projectTOML string) string {
		t.Helper()
		exe, err := os.Executable()
		if err != nil {
			t.Fatalf("executable: %v", err)
		}
		ws := t.TempDir()
		if projectTOML != "" {
			if err := os.WriteFile(filepath.Join(ws, "mulix.toml"), []byte(projectTOML), 0o644); err != nil {
				t.Fatalf("seed project config: %v", err)
			}
		}
		// 隔离环境：滤掉宿主 MULIX_* 残留；user 配置层指到空临时 home
		//（os.UserHomeDir 在 Windows 读 USERPROFILE，POSIX 读 HOME）。
		env := []string{}
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, "MULIX_") {
				continue
			}
			env = append(env, e)
		}
		home := t.TempDir()
		env = append(env, "HOME="+home, "USERPROFILE="+home,
			"MULIX_SERVE_HELPER=1", "MULIX_SERVE_HELPER_WS="+ws)
		if callback {
			env = append(env, "MULIX_SUBAGENT_CALLBACK=http://127.0.0.1:9/never")
		}
		cmd := exec.Command(exe, "-test.run=^TestServeHelperProcess$")
		cmd.Dir = ws
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("helper process: %v\n%s", err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "FACE:") {
				if i := strings.Index(line, "FACE:"); i >= 0 {
					return strings.TrimSpace(line[i+len("FACE:"):])
				}
			}
		}
		t.Fatalf("helper produced no FACE line:\n%s", out)
		return ""
	}

	t.Run("callback + memory_search=true = full face", func(t *testing.T) {
		face := run(t, true, "[tools]\nmemory_search = true\n")
		want := append(append([]string{}, serveFaceWant...), "mulix_usage")
		if got := strings.Split(face, ","); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("serve face = %q, want %q", face, strings.Join(want, ","))
		}
	})
	t.Run("clean env = none of the six", func(t *testing.T) {
		face := run(t, false, "")
		if !strings.Contains(face, "mulix_usage") {
			t.Fatalf("unconditional baseline mulix_usage missing from face %q（import 图基线）", face)
		}
		names := strings.Split(face, ",")
		for _, want := range serveFaceWant {
			for _, got := range names {
				if got == want {
					t.Fatalf("tool %q must be absent from clean serve face (%q)", want, face)
				}
			}
		}
	})
}
