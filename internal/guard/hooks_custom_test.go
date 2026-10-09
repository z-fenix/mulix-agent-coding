package guard

// M16/FR-111/FR-112 用户自定义钩子消费者测试。执行语义按 P1 既有矩阵：钩子
// 消费者是观察面（FR-017「单个消费者异常不得影响回合」）——脚本超时/崩溃
// 一律「降级 + 告警 + 主流程继续」（design 002 §9 对两种失败模式同款单行
// 裁定，brief ⑤「同上」）；引擎协议的 exit-2 阻断语义不适用（mulix 消费者
// 无引擎反馈通道——P1 hook_response 的 output/stdout 在 control 侧只透传
// 不解释，控制器的 Dispatch 也没有错误应答面）。
//
// 测试形态（自重执行 helper process，TestServeHelperProcess 同款）：脚本
// stub 由测试二进制自重执行承担——Windows 的 .bat/cmd echo 对元字符转义
// 不可靠，而 Go helper 的 os.Args 是 CreateProcess 引号往返后的权威 argv，
// 断言「argv 原样到达」最硬（评审 Focus：argv 注入——command 数组元素里的
// 分号/管道/&/$/% 等元字符不得被任何 shell 解释）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"mulix/internal/config"
)

const hookHelperEnv = "MULIX_HOOK_HELPER"

// hookObservation 是 helper 进程的观察记录（argv 原样 + stdin 原文）。
type hookObservation struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

// TestHookHelperProcess 是自重执行的钩子脚本 stub：父进程以
// MULIX_HOOK_HELPER=1 驱动、-test.run 过滤使子进程只跑本测。行为由环境变量
// 参数化：MULIX_HOOK_HELPER_SLEEP_MS 先睡眠（超时用例）、STDOUT 打印指定
// 文本（正常 JSON 输出用例）、OUT 落观察记录（argv/stdin 断言面）、EXIT 以
// 指定码退出（崩溃用例——写在 OUT 之后，stdin 送达在崩溃路径仍可断言）。
func TestHookHelperProcess(t *testing.T) {
	if os.Getenv(hookHelperEnv) != "1" {
		return
	}
	if ms := os.Getenv("MULIX_HOOK_HELPER_SLEEP_MS"); ms != "" {
		n, err := strconv.Atoi(ms)
		if err != nil {
			t.Fatalf("sleep ms: %v", err)
		}
		time.Sleep(time.Duration(n) * time.Millisecond)
	}
	if s := os.Getenv("MULIX_HOOK_HELPER_STDOUT"); s != "" {
		// 直接写 os.Stdout：子进程默认非 verbose，t.Logf 不出流。
		fmt.Fprintln(os.Stdout, s)
	}
	if out := os.Getenv("MULIX_HOOK_HELPER_OUT"); out != "" {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			t.Fatalf("read stdin: %v", err)
		}
		obs := hookObservation{Args: os.Args, Stdin: string(stdin)}
		b, err := json.Marshal(obs)
		if err != nil {
			t.Fatalf("marshal observation: %v", err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatalf("write observation: %v", err)
		}
	}
	if ec := os.Getenv("MULIX_HOOK_HELPER_EXIT"); ec != "" {
		n, err := strconv.Atoi(ec)
		if err != nil {
			t.Fatalf("exit code: %v", err)
		}
		os.Exit(n)
	}
}

// hookHelperCommand 构造指向本测试二进制 helper 模式的规则 command（argv
// 直传；extra 为尾部标记参数——argv 逐字断言的观测面）。
func hookHelperCommand(t *testing.T, extra ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	cmd := []string{exe, "-test.run=^TestHookHelperProcess$", "--"}
	return append(cmd, extra...)
}

// hookWarning 是 OnWarning 的观察记录（事件、规则声明序、细节文案）。
type hookWarning struct {
	ev     HookEvent
	index  int
	detail string
}

// TestNewScriptConsumerValidation pins ①（启动拒绝矩阵，FR-111 fail-fast）：
// 合法声明构建成功（四时点各一；timeout 上限边界 30000 与缺省 0 均合法；
// 空规则集 = no-op 消费者；空串参数位（index>0）合法——只有 argv[0] 空
// 串必拒，否则该规则每次执行都启动失败→循环降级+告警）；event 不在 P1
// 四时点、command 空/argv[0] 空串、timeout 越界（>30000 或负数）→
// NewScriptConsumer 报错拒绝，错误点名违规字段。
func TestNewScriptConsumerValidation(t *testing.T) {
	legal := []config.HookRule{
		{Event: "SessionStart", Command: []string{"true"}},
		{Event: "PreToolUse", Command: []string{"python", "-c", "print(1)"}, TimeoutMS: 5000},
		{Event: "PostToolUse", Command: []string{"x"}, TimeoutMS: 30000},
		{Event: "Stop", Command: []string{"y"}, TimeoutMS: 0},
		// 评审修复轮 1 F2 反向钉：argv[0] 之后的空串参数合法（字面量传参）
		{Event: "PreToolUse", Command: []string{"printf", "%s", ""}},
	}
	if _, err := NewScriptConsumer(legal); err != nil {
		t.Fatalf("legal declarations must build: %v", err)
	}
	if _, err := NewScriptConsumer(nil); err != nil {
		t.Fatalf("empty rule set must build (no-op consumer): %v", err)
	}
	for _, tc := range []struct {
		name  string
		rules []config.HookRule
		want  string
	}{
		{"unknown event", []config.HookRule{{Event: "Foo", Command: []string{"x"}}}, "event"},
		{"empty event", []config.HookRule{{Event: "", Command: []string{"x"}}}, "event"},
		{"empty command", []config.HookRule{{Event: "PreToolUse", Command: []string{}}}, "command"},
		{"nil command", []config.HookRule{{Event: "PreToolUse"}}, "command"},
		{"argv0 empty string", []config.HookRule{{Event: "PreToolUse", Command: []string{""}}}, "command"},
		{"timeout over cap", []config.HookRule{{Event: "PreToolUse", Command: []string{"x"}, TimeoutMS: 30001}}, "timeout"},
		{"negative timeout", []config.HookRule{{Event: "PreToolUse", Command: []string{"x"}, TimeoutMS: -1}}, "timeout"},
	} {
		_, err := NewScriptConsumer(tc.rules)
		if err == nil {
			t.Fatalf("%s: must be rejected at startup (fail-fast)", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q must mention %q", tc.name, err, tc.want)
		}
	}
}

// TestScriptConsumerExecArgvVerbatim pins ②（评审 Focus：argv 注入）：command
// 数组经 exec 直传（不经 shell）——元字符（分号、管道、&、$、%、引号、
// 空格）必须作为参数字面量到达子进程；stdin 收到 payload 的完整事件 JSON
// （字段形状钉死：snake_case、空字段在位——脚本作者的可依赖契约）。
func TestScriptConsumerExecArgvVerbatim(t *testing.T) {
	out := filepath.Join(t.TempDir(), "obs.json")
	t.Setenv(hookHelperEnv, "1")
	t.Setenv("MULIX_HOOK_HELPER_OUT", out)
	markers := []string{"print(1); print(args)", "a b", "a|b", "a&c", "$X", "%PATH%", `q"q`}
	c, err := NewScriptConsumer([]config.HookRule{
		{Event: "PreToolUse", Command: hookHelperCommand(t, markers...), TimeoutMS: 10000},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	payload := HookPayload{
		Event: HookPreToolUse, HookID: "h-1", HookName: "audit",
		Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`),
	}
	if err := c.Handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("helper observation missing: %v", err)
	}
	var obs hookObservation
	if err := json.Unmarshal(b, &obs); err != nil {
		t.Fatalf("observation: %v", err)
	}
	// os.Args 前缀 = [exe, -test.run, --]；尾段必须与 markers 逐字相等。
	if len(obs.Args) < len(markers)+3 {
		t.Fatalf("helper argv too short: %q", obs.Args)
	}
	gotTail := obs.Args[len(obs.Args)-len(markers):]
	if !reflect.DeepEqual(gotTail, markers) {
		t.Fatalf("argv tail = %q, want %q (metacharacters must arrive verbatim)", gotTail, markers)
	}
	wantStdin := `{"event":"PreToolUse","hook_id":"h-1","hook_name":"audit","tool":"Bash","input":{"command":"go test ./..."},"output":"","outcome":""}`
	if obs.Stdin != wantStdin {
		t.Fatalf("stdin = %s, want %s", obs.Stdin, wantStdin)
	}
}

// TestScriptConsumerJSONResponseAccepted pins ③：脚本正常输出 JSON（exit 0）
// → 按语义生效 = 响应被接受：Handle 无错误、无降级告警。P1 钩子语义里
// stdout 是信息面（control 透传不解释、无引擎反馈通道）——exit 码是成功的
// 权威，正常 JSON 输出不触发降级路径。
func TestScriptConsumerJSONResponseAccepted(t *testing.T) {
	t.Setenv(hookHelperEnv, "1")
	t.Setenv("MULIX_HOOK_HELPER_STDOUT", `{"output":"ok","outcome":"success"}`)
	built, err := NewScriptConsumer([]config.HookRule{
		{Event: "PostToolUse", Command: hookHelperCommand(t), TimeoutMS: 10000},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	c := built.(*ScriptConsumer) // OnWarning 挂在具体类型上（接口契约只有 Handle）
	var warns []hookWarning
	c.OnWarning = func(ev HookEvent, index int, detail string) {
		warns = append(warns, hookWarning{ev, index, detail})
	}
	err = c.Handle(context.Background(), HookPayload{Event: HookPostToolUse, HookID: "h-2", HookName: "notify"})
	if err != nil {
		t.Fatalf("exit-0 JSON response must be accepted: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("successful script must not warn: %+v", warns)
	}
}

// TestScriptConsumerTimeoutDegrades pins ④（FR-112）：脚本超时 → 降级 +
// 告警：OnWarning 以事件、规则声明序、超时细节上报；Handle 返回错误供
// 编程式调用者感知（HookFunc 适配层丢弃即得「主流程继续」的既定 fail-open）。
// 主流程不悬挂由测试自身不阻塞收口证明（30000ms 睡眠 vs 150ms 规则超时）。
func TestScriptConsumerTimeoutDegrades(t *testing.T) {
	t.Setenv(hookHelperEnv, "1")
	t.Setenv("MULIX_HOOK_HELPER_SLEEP_MS", "30000") // 远超规则超时——依赖 kill 收口
	built, err := NewScriptConsumer([]config.HookRule{
		{Event: "PreToolUse", Command: hookHelperCommand(t), TimeoutMS: 150},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	c := built.(*ScriptConsumer) // OnWarning 挂在具体类型上（接口契约只有 Handle）
	var warns []hookWarning
	c.OnWarning = func(ev HookEvent, index int, detail string) {
		warns = append(warns, hookWarning{ev, index, detail})
	}
	err = c.Handle(context.Background(), HookPayload{Event: HookPreToolUse, HookID: "h-3"})
	if err == nil {
		t.Fatal("timeout must surface an error to the programmatic caller")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout error must name the failure mode: %v", err)
	}
	if len(warns) != 1 || warns[0].ev != HookPreToolUse || warns[0].index != 0 ||
		!strings.Contains(warns[0].detail, "timeout") {
		t.Fatalf("timeout warning = %+v", warns)
	}
}

// TestScriptConsumerCrashDegrades pins ⑤：脚本崩溃（exit 3）→ 与超时同款
// 降级 + 告警（brief ⑤「同上」；design 002 §9 单行裁定）；错误与告警带退出
// 码线索；崩溃前 stdin 仍完整送达（helper 先落观察再退出）。
func TestScriptConsumerCrashDegrades(t *testing.T) {
	out := filepath.Join(t.TempDir(), "obs.json")
	t.Setenv(hookHelperEnv, "1")
	t.Setenv("MULIX_HOOK_HELPER_OUT", out)
	t.Setenv("MULIX_HOOK_HELPER_EXIT", "3")
	built, err := NewScriptConsumer([]config.HookRule{
		{Event: "Stop", Command: hookHelperCommand(t), TimeoutMS: 10000},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	c := built.(*ScriptConsumer) // OnWarning 挂在具体类型上（接口契约只有 Handle）
	var warns []hookWarning
	c.OnWarning = func(ev HookEvent, index int, detail string) {
		warns = append(warns, hookWarning{ev, index, detail})
	}
	err = c.Handle(context.Background(), HookPayload{Event: HookStop, HookID: "h-4"})
	if err == nil {
		t.Fatal("crash must surface an error to the programmatic caller")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("crash error must carry the exit code: %v", err)
	}
	if len(warns) != 1 || warns[0].ev != HookStop || warns[0].index != 0 ||
		!strings.Contains(warns[0].detail, "exit status 3") {
		t.Fatalf("crash warning = %+v", warns)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("helper observation missing: %v", err)
	}
	var obs hookObservation
	if err := json.Unmarshal(b, &obs); err != nil {
		t.Fatalf("observation: %v", err)
	}
	if !strings.Contains(obs.Stdin, `"event":"Stop"`) {
		t.Fatalf("stdin must carry the event JSON even on crash: %q", obs.Stdin)
	}
}

// TestScriptConsumerStdinOptionalNoRead pins（特征钉：外部契约）：脚本不读
// stdin 直接退出（exit 0）不误报失败——os/exec 对 stdin 拷贝的
// EPIPE/BROKEN_PIPE 按 exit 码豁免（Go Issue 20445）。「stdin 收事件 JSON」
// 是提供面而非要求面：忽略 stdin 的钩子脚本同样合法。
func TestScriptConsumerStdinOptionalNoRead(t *testing.T) {
	t.Setenv(hookHelperEnv, "1") // 无 OUT/STDOUT/SLEEP/EXIT：helper 立即返回，全程不读 stdin
	c, err := NewScriptConsumer([]config.HookRule{
		{Event: "PreToolUse", Command: hookHelperCommand(t), TimeoutMS: 10000},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := c.Handle(context.Background(), HookPayload{Event: HookPreToolUse, HookID: "h-6"}); err != nil {
		t.Fatalf("exit-0 script that never reads stdin must not fail: %v", err)
	}
}

// TestScriptConsumerDefaultTimeoutEffective pins（评审修复轮 1 F3）：缺省
// timeout_ms=0 的生效值是 defaultHookTimeoutMS（包级 var 缝——10s 墙钟不可
// 测，测试收缩后走生效默认路径实测）。双钉：①缺省值本身不被顺手改动；
// ②TimeoutMS=0 的规则按缺省值生效（收缩为 80ms + 睡眠 helper → 超时错误
// 带收缩值——证明 0 映射到缺省值而非「无限时」）。
func TestScriptConsumerDefaultTimeoutEffective(t *testing.T) {
	if defaultHookTimeoutMS != 10000 {
		t.Fatalf("default timeout drift: %d, want 10000", defaultHookTimeoutMS)
	}
	t.Setenv(hookHelperEnv, "1")
	t.Setenv("MULIX_HOOK_HELPER_SLEEP_MS", "30000")
	defaultHookTimeoutMS = 80 // 收缩缺省值（defer 恢复）
	defer func() { defaultHookTimeoutMS = 10000 }()
	c, err := NewScriptConsumer([]config.HookRule{
		{Event: "PreToolUse", Command: hookHelperCommand(t)}, // TimeoutMS 缺省 0
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	err = c.Handle(context.Background(), HookPayload{Event: HookPreToolUse, HookID: "h-7"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("rule without explicit timeout must hit the effective default: %v", err)
	}
	if !strings.Contains(err.Error(), "80ms") {
		t.Fatalf("timeout error must carry the effective default duration: %v", err)
	}
}

// TestScriptConsumerFanOutWithBuiltin pins ⑥：自定义钩子是追加消费者——
// 一条事件同时到达 P1 内置消费者（HookFunc 直注册）与脚本消费者，互不挤占
// （design 002 §7「一条事件分发给全部消费者」；适配层与未来 control/cli
// 接线同构：HookFunc 内调 Handle 并丢弃错误——既定 fail-open）。
func TestScriptConsumerFanOutWithBuiltin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "obs.json")
	t.Setenv(hookHelperEnv, "1")
	t.Setenv("MULIX_HOOK_HELPER_OUT", out)
	c, err := NewScriptConsumer([]config.HookRule{
		{Event: "SessionStart", Command: hookHelperCommand(t), TimeoutMS: 10000},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	hooks := NewHooks()
	builtin := 0
	hooks.On(HookSessionStart, func(ctx context.Context, p HookPayload) { builtin++ })
	hooks.On(HookSessionStart, func(ctx context.Context, p HookPayload) { _ = c.Handle(ctx, p) })
	hooks.Dispatch(context.Background(), HookPayload{Event: HookSessionStart, HookID: "h-5"})
	if builtin != 1 {
		t.Fatalf("builtin consumer calls = %d, want 1", builtin)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("script consumer must also receive the event: %v", err)
	}
}
