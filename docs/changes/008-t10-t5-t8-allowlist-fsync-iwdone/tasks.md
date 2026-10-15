# T10/T5/T8 廉价加固合并包 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 四项独立廉价加固一次交付:①lint import 块解析器注释边缘(假闭合/假 import);②lint allowlist ⊆ P1 allowlist 交叉断言;③memory isolate rename 前 Sync;④控制器 Close 有界等待 watcher Done。

**Architecture:**(bounded,设计于 chat 并已批准)①`scanImports` in-block 循环先注释剥离(行注释至行尾、单行 `/*...*/`、多行块注释状态位)再判定——闭合=`TrimSpace(代码部分)==")"`、import 正则只作用于代码部分;②P1 `allowed` map 提取为 repolint_test.go 包级变量 + lint 新增导出访问器 `FlagAllowlistFor`;交叉断言测试放 `internal/guard`(同一测试二进制可见双方——Go 测试编译单元所限);③isolate tmp 写毕 rename 前 `f.Sync()`,错误走既有 fail-closed 路径;④Close 中 `iw.Close()` 后 `select { <-iw.Done(); <-time.After(2s) }` 有界等待,nil 句柄 no-op。

**Tech Stack:** Go 标准库;既有测试基建(lint fixture 文件、memory seedStore、watcher 矩阵)。

**Spec:** `docs/specs/008-t10-t5-t8-allowlist-fsync-iwdone/spec.md`

**Design:** approved in chat (bounded) —— 要点:①注释剥离先行、闭合判定用剥离后整行 Trim;②子集断言只覆盖共享 marker `--append-system-prompt`(AppendSystemPrompt/TurnTail 为 design-002 独有,无 P1 对应物);③Sync 错误 fail-closed;④等待上界 2s,超时 Close 照常返回。

## Global Constraints

- FR-106:四项各自携带针对性测试;既有测试零回归。
- FR-107:行为契约不变——分层违规判定规则不变、memory 隔离语义不变、watcher 提醒语义不变。
- 最小化:每项改动只触及本项文件;每任务结束 `go test ./... -count=1` 全绿再 commit。

## Review Focus

- 注释形态矩阵:`// )`、`/* ) */`、`import "x" // )` 行尾注释、块内注释含引号串(`// "internal/guard"`)、多行 `/* */` 跨行——全部不得假闭合/假 import。Task 1 fixture 钉。
- 交叉断言有判别力:人为注入越界条目(临时改动)必须红。Task 2 步骤钉。
- Sync 失败 fail-closed:错误不静默。Task 3 代码路径审查 + 既有隔离测试回归。
- Close 后无 publish 越过:镜像事件数在 Close 后短暂窗口内稳定。Task 4 断言钉。
- 既有矩阵零回归:watcher 6 测、单飞矩阵、lint 既有测试全绿(每任务全量门禁)。

---

## Task 1: lint import 块解析器注释边缘

**Files:**
- Modify: `internal/lint/lint.go:168-199`(`scanImports`)+ 新助手
- Test: `internal/lint/lint_test.go`(追加)

**Interfaces:**
- Consumes: 既有 `importBlockRe`/`quotedPathRe`/`singleImportRe`(lint.go:43-48)。
- Produces: `scanImports` 行为修正(签名不变);包内助手 `stripLineComment(line string) (code string, ok bool)`(ok=false 表示进入多行块注释状态,由调用方状态位处理)——语义:剥离 `//` 起的行尾注释(引号外首个 `//`)与单行 `/*...*/`;**多行块注释**由 `scanImports` 以 `inBlockComment bool` 状态位处理(见实现)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/lint/lint_test.go`(import 补所需;该文件已有 fixture 写法——沿用其 helpers,如缺则用 os.WriteFile 直写):

```go
// TestScanImportsCommentEdges 钉 T10-①(008 FR-101):import 块内注释行
// 既不假闭合也不假 import——扫描结果与剥离注释的等价文件完全一致。
func TestScanImportsCommentEdges(t *testing.T) {
	cases := []struct {
		name  string
		lines []string // 原始文件行
		want  []string // 期望 import 路径(按序)
	}{
		{
			name: "注释含右括号不假闭合",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"",
				"\t// internal/config) 这是注释", "\t\"internal/jsonl\"", ")",
			},
			want: []string{"internal/cache", "internal/jsonl"},
		},
		{
			name: "注释内引号串不假 import",
			lines: []string{
				"package p", "import (", "\t// 见 \"internal/guard\" 文档", "\t\"internal/cache\"", ")",
			},
			want: []string{"internal/cache"},
		},
		{
			name: "行尾注释不干扰",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\" // cache: 记账", ")",
			},
			want: []string{"internal/cache"},
		},
		{
			name: "右括号后行尾注释正常闭合",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"", ") // 分层矩阵收口", "var _ = 1",
			},
			want: []string{"internal/cache"},
		},
		{
			name: "单行块注释不假闭合",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"", "\t/* internal/config) 历史说明 */", "\t\"internal/jsonl\"", ")",
			},
			want: []string{"internal/cache", "internal/jsonl"},
		},
		{
			name: "多行块注释跨行不假闭合不假 import",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"", "\t/* 历史说明开始",
				"\t\"internal/guard\" 引用于旧版", "\t结束 */", "\t\"internal/jsonl\"", ")",
			},
			want: []string{"internal/cache", "internal/jsonl"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := scanImports(tc.lines)
			if err != nil {
				t.Fatalf("scanImports: %v", err)
			}
			var got []string
			for _, r := range refs {
				got = append(got, r.Path)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("imports = %v, want %v", got, tc.want)
			}
		})
	}
}
```

(注:块内含块外包路径如 `internal/guard` 出现在注释里,修正前会假 import 并可能在分层矩阵下产生假违规——这正是缺陷面;修正后注释行整体跳过。`reflect` 若未 import 则补。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/lint/ -run TestScanImportsCommentEdges -count=1 -v`
Expected: FAIL——「注释含右括号不假闭合」「注释内引号串不假 import」「多行块注释」用例的 imports 与 want 不符(现状:`strings.Contains(line, ")")` 假闭合、`quotedPathRe` 假 import);「行尾注释」「右括号后注释」用例可能现状即绿(等价行为)。

- [ ] **Step 3: 实现**

`internal/lint/lint.go` 的 `scanImports` 改为:

```go
// scanImports 提取文件全部 import 声明（单行 + 块内逐行），带行号。
// 注释剥离（T10-①，008 FR-101）：块内先剥离注释再判定——行注释（// 至
// 行尾，引号外首个 //）与块注释（/* */，含跨行）不参与闭合判定与 import
// 提取；闭合 = 剥离后整行 TrimSpace 为 ")"。扫描结果与「剥离注释的等价
// 文件」一致——注释既不假闭合也不假 import。
func scanImports(lines []string) ([]importRef, error) {
	var refs []importRef
	inBlock := false
	inBlockComment := false
	for i, line := range lines {
		if inBlockComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				line = line[idx+2:] // 块注释收尾:其余部分继续按代码处理
				inBlockComment = false
			} else {
				continue // 整行在块注释内
			}
		}
		if inBlock {
			code, comment := splitLineComment(line)
			if comment && strings.HasPrefix(strings.TrimSpace(line), "/*") {
				// 整行块注释开启(或 /* 无收尾——转多行状态)
				if !strings.Contains(line, "*/") {
					inBlockComment = true
				}
				continue
			}
			_ = comment
			if strings.TrimSpace(code) == ")" {
				inBlock = false
				continue
			}
			if m := quotedPathRe.FindStringSubmatch(code); m != nil {
				refs = append(refs, importRef{Path: m[1], Line: i + 1})
			}
			continue
		}
		if importBlockRe.MatchString(line) {
			inBlock = true
			continue
		}
		if m := singleImportRe.FindStringSubmatch(line); m != nil {
			refs = append(refs, importRef{Path: m[1], Line: i + 1})
		}
	}
	if inBlock {
		return nil, errors.New("import 块未闭合")
	}
	return refs, nil
}

// splitLineComment 把一行拆为代码部分与行尾注释:返回 code(引号外首个 //
// 之前的部分)与 ok(该行是否以 // 或 /* 起始——整行注释)。
// 引号内 // 不视为注释起始(如 import 路径含 // 的罕见形态)。
func splitLineComment(line string) (code string, wholeLineComment bool) {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case '/':
			if !inQuote && i+1 < len(line) && (line[i+1] == '/' || line[i+1] == '*') {
				trimmed := strings.TrimSpace(line[:i])
				if line[i+1] == '*' && trimmed == "" { // /* 起始的整行块注释(首非空白即 /*)
					// /* 起始的整行注释:code 为空,由调用方按块注释处理
					return "", true
				}
				if line[i+1] == '/' && trimmed == "" {
					return "", true // 整行行注释
				}
				return line[:i], false // 行尾注释:代码部分为其前缀
			}
		}
	}
	return line, false
}
```

注:①行尾注释形态(`code, ok=false`)由 inBlock 分支直接用 `code` 判定闭合与 import——`/* xxx */` 单行内联在行中的形态经 `splitLineComment` 不剥离(首个 `//` 才触发),但其后接 `//` 或行首 `/*` 已覆盖计划 fixture;真正的残留边缘仅剩「行中 `/*` 开启且本行无 `*/` 的未终止多行块注释」(其前缀代码之后、收尾之前的行体会被按代码解析)——单行内联 `/*...*/` 已被完整剥离(含含 `)` 者),超出部分如实记录;②`errors` 已在 lint.go import;③既有 `scanImports` 调用方(lint.go:159)签名不变。

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/lint/ -count=1 -v`
Expected: PASS(新测试 + lint 既有测试全绿)

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净

- [ ] **Step 5: Commit**

```bash
git add internal/lint/lint.go internal/lint/lint_test.go
git commit -m "fix(lint): import 块解析注释边缘——假闭合/假 import 修复(008 FR-101;T10-①)"
```

---

## Task 2: allowlist 交叉断言(lint ⊆ P1)

**Files:**
- Modify: `internal/guard/repolint_test.go:24-40`(`allowed` 局部 map 提取为包级变量)
- Modify: `internal/lint/rules.go`(新增导出访问器,~5 行——生产面最小新增,评审员方案 a)
- Test: Create `internal/guard/allowlist_cross_test.go`(交叉测试放 guard:同一测试二进制可见双方——lint 的测试文件无法引用 guard 测试文件内的变量,Go 测试编译单元所限,评审员 Issue 1)

**Interfaces:**
- Consumes: lint `refRules`(rules.go:80);P1 allowed 清单(repolint_test.go)。
- Produces: ①`guard.repolintFlagAllowlist`(包级测试变量);②`lint.FlagAllowlistFor(marker string) map[string]bool`(导出访问器:返回该 marker 的 Allow 集合副本;未知 marker 返回 nil);③交叉断言测试 `TestLintAllowlistSubsetOfP1Repolint` + 判别力测试。

- [ ] **Step 1: 写失败测试**

(a) `internal/guard/repolint_test.go`:把 `TestNoSecondSystemPromptPath` 函数内的 `allowed := map[string]bool{...}` 提取为包级变量(函数内改为引用;**键值逐字不变**):

```go
// repolintFlagAllowlist 是 --append-system-prompt 引用面的 P1 allowlist
// (原 TestNoSecondSystemPromptPath 局部变量;T10-② 提取为包级供交叉断言
// ——仅测试文件,零生产面)。
var repolintFlagAllowlist = map[string]bool{
	"internal/cache/boot.go":             true,
	"internal/engine/host.go":            true,
	"internal/engine/claudehost.go":      true,
	"internal/engine/claudehost_test.go": true,
	"internal/guard/repolint_test.go":    true,
	"internal/lint/rules.go":             true,
	"internal/lint/lint_test.go":         true,
	// P3 008:交叉断言测试文件自身含 marker 字面量——P1 walk 扫 _test.go,
	// 不加键则 TestNoSecondSystemPromptPath 红键(P1 侧是超集,子集断言不受影响)。
	"internal/guard/allowlist_cross_test.go": true,
}
```

(b) `internal/lint/rules.go` 末尾新增访问器:

```go
// FlagAllowlistFor 返回指定 marker 的 allowlist 集合副本(T10-② 交叉断言
// 消费:guard 侧测试经此读取 lint 侧清单,双源单侧漂移在断言层暴露)。
// 未知 marker 返回 nil。
func FlagAllowlistFor(marker string) map[string]bool {
	for _, rr := range refRules {
		if rr.Marker == marker {
			out := make(map[string]bool, len(rr.Allow))
			for k, v := range rr.Allow {
				out[k] = v
			}
			return out
		}
	}
	return nil
}
```

(c) 创建 `internal/guard/allowlist_cross_test.go`:

```go
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
```

- [ ] **Step 2: 跑测试验证通过**

Run: `go test ./internal/guard/ ./internal/lint/ -run 'TestLintAllowlist|TestNoSecondSystemPromptPath' -count=1 -v`
Expected: PASS(当前无漂移——lint Allow ⊆ P1 清单成立;判别力测试按构造通过)。

注:本任务无运行期 RED(现状即满足子集关系)——测试价值在**钉住**与**判别力**;判别力子测试以注入副本验证断言逻辑可红(构造性证明,不真实引入漂移污染仓库)。台账记录此形态。

- [ ] **Step 3: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净

```bash
git add internal/guard/repolint_test.go internal/guard/allowlist_cross_test.go internal/lint/rules.go
git commit -m "test(lint): allowlist ⊆ P1 repolint 交叉断言——双源漂移钉(008 FR-102;T10-②)"
```

---

## Task 3: memory isolate rename 前 Sync

**Files:**
- Modify: `internal/memory/memory.go:304-310`(isolate tmp 写毕段)
- Test: 既有 `TestOpenIsolatesHalfLineTail`/`TestOpenIsolatesGarbageMidFileLine`/`TestOpenIsolatesOversizedLine` 回归(无新测试——行为不可观差异,Sync 为持久化加固;代码路径审查 + 回归钉)

**Interfaces:**
- Consumes: 既有 isolate tmp+rename 段(memory.go:298-312)。
- Produces: rename 前 `f.Sync()`;Sync 错误走既有错误包装 fail-closed。

- [ ] **Step 1: 实现**

`internal/memory/memory.go` 的 tmp 写毕段:

```go
	if err := f.Sync(); err != nil { // T5-⑤(008 FR-103):rename 前刷盘——
		f.Close()                   // 崩溃窗口内 rename 不得落到未刷写数据
		return fmt.Errorf("memory: rewrite sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("memory: rewrite close: %w", err)
	}
```

(插在 `for _, line := range good { ... }` 循环之后、`f.Close()` 之前。)

- [ ] **Step 2: 核对其余 rename 路径(spec Assumption 复核)**

Run: `grep -rn "os.Rename" internal/ --include="*.go" | grep -v _test`
Expected: 逐个核对——memory isolate(本次补)、sessionx 压实(`compactEventsInPlace` 已有 `f.Sync()`)、jsonl Repair(**无 rename**——原地 `os.Truncate`,无 Sync 对象,核对结论「无 action」;内部仅 memory.go:309 与 sessionx.go:242 两处 rename)。Sync 失败路径遗留 `.tmp` 文件无害(下次 isolate 以 O_TRUNC 重开),与 sessionx 的 os.Remove 对称性不做(保持最小化)。

- [ ] **Step 3: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净(既有隔离/记忆测试零回归——SC-003 回归钉)

```bash
git add internal/memory/memory.go
git commit -m "fix(memory): isolate rename 前 Sync——崩溃窗口收紧(008 FR-103;T5-⑤)"
```

---

## Task 4: Close 有界等待 iw.Done

**Files:**
- Modify: `internal/control/controller.go`(`Close` 的 watcher 段,~1013-1030)
- Test: `internal/control/watcher_test.go`(追加)

**Interfaces:**
- Consumes: 既有 `iw.Close()`/`iw.Done()`(controller.go:1706-1719);`Close` 的 `iw := c.instrWatch` 快照段。
- Produces: `Close` 返回后 watcher goroutine 已退出(Done 关闭)或有界超时(2s)留痕;nil 句柄 no-op 不变。

- [ ] **Step 1: 写失败测试**

追加到 `internal/control/watcher_test.go`(沿用其测试基建;构造见既有 `TestWatchWiredToStartSequence`):

```go
// TestCloseWaitsForWatcherDone 钉 T8-①(008 FR-104/105):控制器 Close
// 有界等待 watcher goroutine 退出——Close 返回后 Done 已关闭(无在飞
// publish 越过 Close 的窗口)。
func TestCloseWaitsForWatcherDone(t *testing.T) {
	c := newTestControllerCapturingBoot(t, engine.NewFakeHost(engine.FakeScript{}), &recordingSink{}, &engine.BootConfig{})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	c.mu.Lock()
	iw := c.instrWatch
	c.mu.Unlock()
	if iw == nil {
		t.Fatal("watcher must be started")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-iw.Done():
	default:
		t.Fatal("Close returned but watcher goroutine still running (must wait for Done, bounded)")
	}
}
```

(构造对齐既有 `TestWatchWiredToStartSequence`(watcher_test.go:241-262):`newTestControllerCapturingBoot` + FakeHost + `NewSession`——`start(ctx, boot)` 签名与 nil 宿主 panic 面经评审员核实,该装配是唯一可编译形态;断言不变:Close 后 Done 必须已关。import 补 `mulix/internal/engine`。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/control/ -run TestCloseWaitsForWatcherDone -count=1 -v`
Expected: FAIL with "Close returned but watcher goroutine still running"(现状:Close 不等 Done——goroutine 退出有调度延迟,select default 大概率命中;若偶发绿则连跑 `-count=5` 取红,仍不红则该测试形态不可靠,改用「Close 后 50ms 内 Done 必关」的轮询断言并在台账记 Ruling)。

- [ ] **Step 3: 实现**

`Close` 的 watcher 段(controller.go,`iw := c.instrWatch` 快照后):

```go
	if iw != nil {
		iw.Close() // 停止指令文件轮询（M15）——closed 先置位再收口（回调桥同纪律）
		// T8-①(008 FR-104/105):有界等待 goroutine 退出——Close 返回后
		// 不再有 watcher publish 越过(与泵「无 publish 越过 Close」纯度
		// 一致)。nil/未启动句柄经上方判空 no-op;轮询周期工作量小(文件
		// hash),2s 上界仅防御性。
		select {
		case <-iw.Done():
		case <-time.After(2 * time.Second):
			// 超时不悬挂 Close;留痕:fmt.Fprintln(os.Stderr,
			// "mulix: watcher goroutine did not exit within 2s")。
			fmt.Fprintln(os.Stderr, "mulix: watcher goroutine did not exit within 2s")
		}
	}
```

(`time`/`os`/`fmt` 若 controller.go 未 import 则补。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/control/ -run 'TestCloseWaitsForWatcherDone|TestWatch|TestPermissionCallbackSingleflight' -count=5 -v`
Expected: PASS(含 5 连跑防偶发)

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/`
Expected: 全绿/干净(既有 watcher 矩阵 6 测、单飞矩阵、e2e 零回归)

- [ ] **Step 5: Commit**

```bash
git add internal/control/controller.go internal/control/watcher_test.go
git commit -m "fix(control): Close 有界等待 watcher Done——收口纪律对齐(008 FR-104/105;T8-①)"
```
