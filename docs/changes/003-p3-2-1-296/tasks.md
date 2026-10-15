# P3 引擎漂移修复——2.1.296 两形状容忍 + 重钉 + 真实冒烟 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** mulix 流解码器同时容忍工具结果内容的字符串(≤2.1.291)与文本块数组(≥2.1.296)两种形状;engine.lock 重钉 2.1.296;新形状真实样本入库;一次真实引擎冒烟关闭 P2 限制条目并回填缓存基准。

**Architecture:**(bounded,设计于 chat 并已批准)缺陷根因是 `ToolResultBlock.Content` 按 string 建模,2.1.296 把 `content` 发成数组时 `json.Unmarshal` 报错 → 既有「decode user 错误 → 流损坏 fail-closed」路径收口。解法:给 `ToolResultBlock` 加自定义 `UnmarshalJSON`,`Content` 保持底层 string(消费端零改动),数组形状按 `{type:"text"}` 白名单归一化拼接,未建模形状返回错误自然落入 fail-closed 红线。治理面(engine.lock 重钉 + 基准回填)与证据面(P2 transcript 提取 fixture + 真实冒烟)随代码面分任务交付。

**Tech Stack:** Go 1.x 标准库(encoding/json);Makefile 门禁(`make test`/`make lint`);scripts/check-engine-lock.sh;真实 claude CLI。

**Spec:** `docs/specs/003-p3-2-1-296/spec.md`

**Design:** approved in chat (bounded) —— 要点:①`ToolResultBlock` 自定义 `UnmarshalJSON` 做两形状容忍 + Q1 严格边界(数组元素只认 `{type:"text"}`,其他 → 流损坏);②engine.lock 重钉 2.1.296(Q2:先升本机 CLI 再改 pin,冒烟在其上跑);③缓存基准以冒烟会话缓存记账实测回填、注明测量条件,冒烟未跑成则标「尚未建立」(Q3);④fixture 优先从 P2 transcript 提取,缺字段场景冒烟补采(Q4)。

## Global Constraints

- FR-111 红线不放宽:未知顶层 type 跳过、非法 JSON fail-closed、先持久化后发布、镜像 1:1。
- FR-103/Q1 边界:数组元素只认 `{type:"text"}` 文本块;非文本块(image/结构化块)、畸形元素、原始类型数字/对象 → 流损坏 fail-closed,不静默丢弃。
- FR-102:`tool_use_id`、`is_error`(成功缺省=false)、`tool_use_result`(原样透传)语义不变。
- FR-109:版本治理语义不变——pin 一致不告警;不一致默认拒绝启动;显式放行留痕。不引入「静默接受任意版本」。
- FR-105 入库约定:fixture 样本 MUST 取自真实 CLI 输出(不得手工编造),无空行、逐行可解码。
- 缓存口径沿用 P1 meter(`internal/cache/meter.go`):逐回合 `hit = cache_read / (input + cache_creation + cache_read)`;会话 `session_hit` 加权 Σread/(Σuncached+Σwrite+Σread)。不新增度量面。
- 测试纪律:TDD(先红后绿);每个任务结束跑 `make test` 全绿再 commit。
- 依赖纪律:仅 Go 标准库,不新增第三方依赖。

## Review Focus

- 数组含**多个文本块**:按 `\n` 拼接保留全部文本,不得静默丢块 —— Task 1 单测 `数组多个文本块` 钉。
- 数组含**非文本块**(image/结构化):流损坏报错,不得跳过或伪装成文本 —— Task 1 单测 `数组含非文本块` 钉。
- `content` 为原始类型(数字/对象)或畸形元素(`[42]`/`[{}]`):流损坏报错;`null`/缺失才是空文本 —— Task 1 单测三例钉。
- 新形状下错误标记与标识:`is_error=true` + 数组形状组合、`tool_use_id` 保留 —— Task 1 组合断言 + Task 2 fixture 断言钉。
- 旧形状回归:2.1.291 样本(happy-turn/usage fixture、fake-* 假 CLI 脚本)行为一字不变 —— Task 1 步骤 4 全量门禁 + Task 2 既有 fixture 测试钉。

---

## Task 1: ToolResultBlock 两形状容忍(解码器核心)

**Files:**
- Modify: `internal/engine/events.go`(`ToolResultBlock` 定义处,约 :100-107)
- Modify: `internal/engine/decoder.go`(:92-103 user 分支注释)
- Test: `internal/engine/decoder_test.go`

**Interfaces:**
- Consumes: 既有 `DecodeLine(line []byte) (*Event, error)`(decoder.go);既有 `decodeFixture` 测试助手(decoder_test.go:~30)。
- Produces: `ToolResultBlock.Content` 仍为 `string`(类型与字段名不变,`controller.go onToolResult` 与既有测试零改动);新增方法 `(*ToolResultBlock).UnmarshalJSON(data []byte) error`——数组形状归一化为文本(多块按 `\n` 拼接,全空块 → 空文本),未建模形状返回非 nil error(DecodeLine 原样上抛 → 流损坏 fail-closed)。

- [ ] **Step 1: 写失败测试(两形状 + 全边界)**

在 `internal/engine/decoder_test.go` 末尾追加(`encoding/json`、`strings`、`fmt` 已在该文件 import 则复用;`fmt` 若缺则补):

```go
// TestDecodeToolResultContentTwoShapes 钉 FR-101/FR-103:content 两形状
// 等价解读 + Q1 严格边界(非文本块/畸形元素 → 流损坏)。
func TestDecodeToolResultContentTwoShapes(t *testing.T) {
	cases := []struct {
		name    string
		content string // message.content[0].content 的 JSON 值;"" 表示键缺失
		want    string
		wantErr bool
	}{
		{name: "字符串形状(2.1.291)", content: `"hello world"`, want: "hello world"},
		{name: "数组单文本块(2.1.296)", content: `[{"type":"text","text":"hello world"}]`, want: "hello world"},
		{name: "数组多个文本块按换行拼接", content: `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, want: "a\nb"},
		{name: "空数组解读为空文本", content: `[]`, want: ""},
		{name: "单空文本块解读为空文本", content: `[{"type":"text","text":""}]`, want: ""},
		{name: "全空文本块解读为空文本", content: `[{"type":"text","text":""},{"type":"text","text":""}]`, want: ""},
		{name: "null解读为空文本", content: `null`, want: ""},
		{name: "content键缺失解读为空文本", content: "", want: ""},
		{name: "数组含非文本块判流损坏", content: `[{"type":"image","source":{"x":1}}]`, wantErr: true},
		{name: "数组含畸形元素判流损坏", content: `[42]`, wantErr: true},
		{name: "数组含空对象元素判流损坏", content: `[{}]`, wantErr: true},
		{name: "content为原始对象判流损坏", content: `{"text":"x"}`, wantErr: true},
		{name: "数组含无type元素判流损坏", content: `[{"text":"x"}]`, wantErr: true},
		{name: "content为原始数字判流损坏", content: `3`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var blockJSON string
			if tc.content == "" {
				blockJSON = `{"type":"tool_result","tool_use_id":"call_1"}`
			} else {
				blockJSON = fmt.Sprintf(`{"type":"tool_result","tool_use_id":"call_1","content":%s}`, tc.content)
			}
			line := fmt.Sprintf(`{"type":"user","session_id":"s1","uuid":"u1","message":{"id":"m1","content":[%s]}}`, blockJSON)
			ev, err := DecodeLine([]byte(line))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want stream-corruption error, got event %+v", ev)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeLine: %v", err)
			}
			if ev.Kind != KindToolResult || ev.ToolResult == nil || len(ev.ToolResult.Blocks) != 1 {
				t.Fatalf("tool_result event wrong: %+v", ev)
			}
			b := ev.ToolResult.Blocks[0]
			if b.ToolUseID != "call_1" {
				t.Fatalf("tool_use_id wrong: %q", b.ToolUseID)
			}
			if b.Content != tc.want {
				t.Fatalf("content = %q, want %q", b.Content, tc.want)
			}
		})
	}
}

// TestDecodeToolResultArrayErrorFlags 钉 FR-102:新形状下 is_error 条件
// 字段语义不变(成功缺省=false,出错=true)。
func TestDecodeToolResultArrayErrorFlags(t *testing.T) {
	line := `{"type":"user","session_id":"s1","uuid":"u1","message":{"id":"m1","content":[` +
		`{"type":"tool_result","tool_use_id":"call_ok","content":[{"type":"text","text":"ok"}]},` +
		`{"type":"tool_result","tool_use_id":"call_err","is_error":true,"content":[{"type":"text","text":"boom"}]}]}}`
	ev, err := DecodeLine([]byte(line))
	if err != nil {
		t.Fatalf("DecodeLine: %v", err)
	}
	if ev.Kind != KindToolResult || ev.ToolResult == nil || len(ev.ToolResult.Blocks) != 2 {
		t.Fatalf("tool_result event wrong: %+v", ev)
	}
	if ev.ToolResult.Blocks[0].IsError {
		t.Fatalf("成功块 is_error 应缺省为假: %+v", ev.ToolResult.Blocks[0])
	}
	if !ev.ToolResult.Blocks[1].IsError || ev.ToolResult.Blocks[1].Content != "boom" {
		t.Fatalf("错误块标记/文本错误: %+v", ev.ToolResult.Blocks[1])
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/engine/ -run 'TestDecodeToolResultContentTwoShapes|TestDecodeToolResultArrayErrorFlags' -v`
Expected: FAIL——数组形状用例报 `DecodeLine: engine: decode user: json: cannot unmarshal array into Go struct field ...content of type string`;字符串/null/缺失用例可能已绿(现状行为)。

- [ ] **Step 3: 最小实现(events.go 加 UnmarshalJSON)**

在 `internal/engine/events.go` 的 `ToolResultBlock` 定义(`Content string` 字段保留)之后新增(文件需补 import `bytes`、`fmt`、`strings`):

```go
// UnmarshalJSON 解码 tool_result 块:content 接受两种形状(spec 003
// FR-101/FR-103)——单字符串(2.1.291 及之前)与文本块数组(2.1.296 及
// 之后),等价解读为同一段文本;null/缺失 → 空文本。数组元素只认
// {type:"text"} 文本块(多块按 \n 拼接,全空块 → 空文本);出现未建模的
// 非文本块、畸形元素或原始类型 → 返回错误(流损坏 fail-closed,与
// 「非法 JSON」同类——容忍的是已建模的两种形状,不是任意数据)。
func (b *ToolResultBlock) UnmarshalJSON(data []byte) error {
	type alias struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	b.Type, b.ToolUseID, b.IsError = a.Type, a.ToolUseID, a.IsError
	raw := bytes.TrimSpace(a.Content)
	switch {
	case len(raw) == 0 || bytes.Equal(raw, []byte("null")):
		b.Content = ""
	case raw[0] == '"':
		return json.Unmarshal(raw, &b.Content)
	case raw[0] == '[':
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return fmt.Errorf("engine: tool_result content array: %w", err)
		}
		parts := make([]string, 0, len(blocks))
		allEmpty := true
		for i, blk := range blocks {
			if blk.Type != "text" {
				return fmt.Errorf("engine: tool_result content[%d]: unknown block type %q (fail-closed)", i, blk.Type)
			}
			if blk.Text != "" {
				allEmpty = false
			}
			parts = append(parts, blk.Text)
		}
		if allEmpty {
			b.Content = ""
		} else {
			b.Content = strings.Join(parts, "\n")
		}
	default:
		return fmt.Errorf("engine: tool_result content: unknown shape (fail-closed)")
	}
	return nil
}
```

并在 `internal/engine/decoder.go` user 分支(case "user":)上方注释补一行(不改逻辑):

```go
	// content 形状容忍见 ToolResultBlock.UnmarshalJSON(字符串/文本块数组两形状;003 FR-101)。
```

- [ ] **Step 4: 跑测试验证通过 + 旧形状回归全绿**

Run: `go test ./internal/engine/ -run 'TestDecodeToolResultContentTwoShapes|TestDecodeToolResultArrayErrorFlags' -v`
Expected: PASS(15 个子用例 + 1 个标记用例)

Run: `go test ./...`
Expected: PASS——含 `TestDecodeHappyTurnFixture`(旧形状字符串样本)、`TestDecodeMalformed`、controller/sessionx/e2e 全链;`Content` 类型未变,消费端零改动。

Run: `make lint`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/events.go internal/engine/decoder.go internal/engine/decoder_test.go
git commit -m "feat(engine): tool_result content 两形状容忍(字符串/文本块数组;003 FR-101..103)"
```

---

## Task 2: 新形状 fixture 入库 + fixture 门禁测试

**Files:**
- Create: `fixtures/engine/tool-result-array-2196.jsonl`(真实样本,本任务提取)
- Test: `internal/engine/decoder_test.go`(追加 fixture 门禁测试)

**Interfaces:**
- Consumes: Task 1 的两形状容忍(数组形状样本可解码);既有 `decodeFixture` 助手;P2 冒烟引擎 transcript(真实 2.1.296 CLI 产物):`~/.claude/projects/C--Users-Zhang-AppData-Local-Temp-mulix-p2-smoke-ws-a/*.jsonl` 与 `...-p2-smoke-ws-b/*.jsonl`。
- Produces: `fixtures/engine/tool-result-array-2196.jsonl`——无空行、逐行可解码、全为新形状(数组)tool_result 的真实 user 事件;门禁测试 `TestDecodeToolResultArrayFixture`。

- [ ] **Step 1: 写失败测试(fixture 尚不存在)**

在 `internal/engine/decoder_test.go` 末尾追加:

```go
// TestDecodeToolResultArrayFixture 钉 FR-105/FR-106:2.1.296 真实样本
// (P2 冒烟引擎 transcript 提取)逐行解码无错,关键字段被断言;旧形状
// fixture(happy-turn/usage)继续通过=无回归。
func TestDecodeToolResultArrayFixture(t *testing.T) {
	events, _ := decodeFixture(t, filepath.Join("fixtures", "engine", "tool-result-array-2196.jsonl"))
	if len(events) == 0 {
		t.Fatal("fixture 无可解码事件")
	}
	sawArray := false
	sawErrFlag := false
	for i, ev := range events {
		if ev.Kind != KindToolResult || ev.ToolResult == nil {
			continue
		}
		for _, b := range ev.ToolResult.Blocks {
			if b.ToolUseID == "" {
				t.Fatalf("event %d: tool_result 缺 tool_use_id: %q", i, b.Content)
			}
			if b.IsError {
				sawErrFlag = true // 真实错误样本出现时,标记必须完整保留(FR-102)
			}
			sawArray = true // 提取过滤器保证入库样本全为数组形状
		}
	}
	if !sawArray {
		t.Fatal("fixture 中无数组形状 tool_result 样本")
	}
	t.Logf("数组形状样本已断言;真实 is_error=true 样本出现=%v(未出现时由 Task 4 冒烟补采面覆盖)", sawErrFlag)
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/engine/ -run TestDecodeToolResultArrayFixture -v`
Expected: FAIL with "open fixtures/engine/tool-result-array-2196.jsonl"(文件不存在)

- [ ] **Step 3: 从 P2 transcript 提取真实样本**

在仓库根执行(只读 transcript、原样输出整行,不改动事件内容;transcript 是外部会话数据,`-I` 隔离运行):

```bash
python -I - <<'EOF'
import json, pathlib
srcs = [
    pathlib.Path.home() / ".claude/projects/C--Users-Zhang-AppData-Local-Temp-mulix-p2-smoke-ws-a",
    pathlib.Path.home() / ".claude/projects/C--Users-Zhang-AppData-Local-Temp-mulix-p2-smoke-ws-b",
]
out = pathlib.Path("fixtures/engine/tool-result-array-2196.jsonl")
seen, kept, skipped = set(), [], 0
for d in srcs:
    for f in sorted(d.glob("*.jsonl")):
        for line in f.read_text(encoding="utf-8", errors="replace").splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue
            if obj.get("type") != "user":
                continue
            msg = obj.get("message") or {}
            content = msg.get("content")
            if not isinstance(content, list):
                continue
            ok = True
            for b in content:
                if not (isinstance(b, dict) and b.get("type") == "tool_result"):
                    continue
                inner = b.get("content")
                if not isinstance(inner, list):
                    ok = False  # 字符串形状非本 fixture 目标
                    break
                if not all(isinstance(e, dict) and e.get("type") == "text" for e in inner):
                    ok = False  # 含未建模块/畸形元素:属流损坏负例,不入正样本
                    break
            if not ok:
                skipped += 1
                continue
            if line in seen:  # 原样留存 transcript 整行,不重序列化(忠实真实 CLI 输出)
                continue
            seen.add(line)
            kept.append(line)
out.write_text("\n".join(kept) + ("\n" if kept else ""), encoding="utf-8")
print(f"kept={len(kept)} skipped={skipped} -> {out}")
EOF
```

验收:`kept >= 1`(P2 transcript 实测含 ≥3 条新形状 tool_result 行);文件无空行、每行是合法 JSON、`type=="user"` 且 tool_result 的 content 均为数组形状。若 `kept == 0`(transcript 已被清理),如实停止本步并在任务报告中记录,fixture 补采转入 Task 4 冒烟时从 mulix stdout 直接留存(方法:冒烟命令重定向 stdout 副本,过滤 user 行)——不得手工编造样本顶替。

- [ ] **Step 4: 跑测试验证通过**

Run: `go test ./internal/engine/ -run 'TestDecodeToolResultArrayFixture|TestDecodeHappyTurnFixture|TestDecodeUsageFixture' -v`
Expected: PASS(新样本逐行可解码 + 两个旧形状 fixture 无回归)

Run: `go test ./... && make lint`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add fixtures/engine/tool-result-array-2196.jsonl internal/engine/decoder_test.go
git commit -m "test(engine): 2.1.296 新形状真实 fixture 入库+门禁(003 FR-105/106)"
```

---

## Task 3: engine.lock 重钉 2.1.296

**Files:**
- Modify: `engine.lock`(仓库根治理文件)

**Interfaces:**
- Consumes: 本机 claude CLI(Task 4 前置:须先升回 2.1.296,见 spec Clarifications Q2);`scripts/check-engine-lock.sh` 格式门禁;`internal/engine.LoadEngineLock` 读取契约(`cli_version` 非空)。
- Produces: `engine.lock` `cli_version="2.1.296"`、`pinned_at=执行当日`;baseline 字段留待 Task 4 回填(本任务不改 baseline,避免中间态谎称已治理——report 中如实注明)。

- [ ] **Step 1: 确认本机 CLI 已升到 2.1.296**

Run: `claude --version`
Expected: 输出包含 `2.1.296`。若仍是 2.1.291,先用与本机安装方式一致的通道升级(npm 全局或 native installer),升级后复跑确认。升级失败(网络/权限)→ 停止本任务,按 spec FR-108 在任务报告如实记录,路由后续处理。

- [ ] **Step 2: 更新 engine.lock**

将 `engine.lock` 内容改为(cli_version 用 `claude --version` 实测版本号,`pinned_at` 用执行当日日期):

```json
{
  "cli_version": "2.1.296",
  "protocol_probe": {
    "init_version_field": "claude_code_version"
  },
  "pinned_at": "2026-10-10",
  "baseline": {
    "cache_hit_rate": 0.0,
    "note": "P3 基准建立后回填"
  }
}
```

(baseline 两键本步原样保留,Task 4 按实测回填或标注尚未建立。)

- [ ] **Step 3: 治理门禁验证**

Run: `bash scripts/check-engine-lock.sh`
Expected: `cli_version = 2.1.296`、`pinned_at` 非空、末行 `check-engine-lock: 通过`

Run: `go test ./internal/engine/ -run TestLoadEngineLock -v && go test ./internal/cli/ -run TestDoctor -v`
Expected: PASS(lock 解析契约与 doctor 严格相等匹配均不受影响;doctor 与 pin 的一致性在 Task 4 真实环境复核)

- [ ] **Step 4: Commit**

```bash
git add engine.lock
git commit -m "chore(governance): engine.lock 重钉 2.1.296(003 FR-104;baseline 待冒烟回填)"
```

---

## Task 4: 真实引擎冒烟 + 缓存基准回填 + P2 限制关闭

**Files:**
- Create: `docs/evidence/p3-smoke.md`(冒烟证据,独立于自动化门禁归档)
- Modify: `engine.lock`(baseline 回填)
- Modify: `docs/changes/002-p2/report.md`(限制 #1 关闭交叉引用;仅追加,不改写原文)

**Interfaces:**
- Consumes: Task 1 解码器容忍(数组形状回合可跑通);Task 3 重钉后的 pin(真实 CLI 2.1.296 与 pin 一致 → doctor 无漂移告警,FR-109);`mulix run`(无人值守单回合)、`mulix doctor`、`mulix cache`(跨进程权威记账);P2 冒烟方法基线 `docs/evidence/p2-smoke.md`。
- Produces: `docs/evidence/p3-smoke.md`——引擎版本、独立工作区、委派链与外部工具回合的关键输出摘录、退出码、缓存记账;`engine.lock` `baseline.cache_hit_rate` = 冒烟会话实测值(备注测量条件)或明确标注「尚未建立」及原因(FR-110/SC-005);P2 report.md 限制 #1 关闭引用。

- [ ] **Step 1: 环境预检**

Run: `claude --version`
Expected: `2.1.296`(与 pin 一致;不一致则回 Task 3)

Run: `go build -o /tmp/mulix-bin/mulix ./cmd/mulix && /tmp/mulix-bin/mulix doctor`
Expected: doctor 无 engine 漂移告警(node/claude/凭据绿)。凭据或网络不可用 → 本任务走 FR-108 分支(见 Step 5)。

- [ ] **Step 2: 独立工作区 + 真实委派回合**

建一次性 git 工作区(冒烟产物不进本仓库;方法对齐 `docs/evidence/p2-smoke.md` A2):

```bash
WS=$(mktemp -d)/mulix-p3-smoke-ws && mkdir -p "$WS" && cd "$WS" && git init -q
printf '# smoke\n' > README.md && git add . && git commit -qm init
/tmp/mulix-bin/mulix run "用 mulix_spawn 委派一个子代理任务:在当前仓库新建 notes.md,内容为冒烟标记 P3-SMOKE-OK,git commit 后报告完成" 2>&1 | tee /tmp/mulix-p3-smoke-run-a.txt
echo "exit=${PIPESTATUS[0]}"
```

Expected: 回合跑完、退出码 0(或该通道正常收口码;`PIPESTATUS[0]` 取 mulix 真实退出码而非 tee 的);`/tmp/mulix-p3-smoke-run-a.txt` 中可见子代理创建、真实多步工作(文件写入 + commit)、结果与 usage 回收进主会话——**不再出现** `engine: stdout stream corrupt: engine: decode user: ... cannot unmarshal array`(P2 断流签名)。

- [ ] **Step 3: 真实外部工具回合**

```bash
cd "$WS" && /tmp/mulix-bin/mulix run "调用外部 MCP 工具 echo,参数 message=mulix-p3-smoke-echo-67890,并复述返回内容" 2>&1 | tee /tmp/mulix-p3-smoke-run-b.txt
echo "exit=${PIPESTATUS[0]}"
```

Expected: 输出含 `mulix-p3-smoke-echo-67890`(工具结果回流被正确解读——2.1.296 数组形状),回合正常收口,退出码正常。

- [ ] **Step 4: 缓存记账 + 证据落盘**

Run: `cd "$WS" && /tmp/mulix-bin/mulix cache 2>&1 | tee /tmp/mulix-p3-smoke-cache.txt`
Expected: 输出会话累计口径(session_hit = Σread/(Σuncached+Σwrite+Σread),P1 meter 权威口径)。

将证据写入 `docs/evidence/p3-smoke.md`,结构对齐 p1/p2-smoke.md,必含:引擎版本(`claude --version` 原文)、日期、独立工作区路径与说明(产物在临时区,文件记录关键摘录)、两次回合的关键输出摘录(委派链建立、子代理完成标记 P3-SMOKE-OK、echo 回显)、退出码、`mulix cache` 记账原文、`baseline.cache_hit_rate` 依据(session_hit 实测值)。若 Task 2 的 fixture 中没有 `is_error=true` 的数组形状真实样本而冒烟中出现了错误回流,把该行按入库约定补采进 `fixtures/engine/tool-result-array-2196.jsonl` 并在证据中注明(spec FR-105 Q4 补采约定)。

- [ ] **Step 5: baseline 回填(或 FR-108 分支)+ P2 限制关闭**

主路径:将 `engine.lock` 的 baseline 改为(数值用 Step 4 实测 session_hit,保留 4 位小数;日期/任务类型如实):

```json
  "baseline": {
    "cache_hit_rate": 0.0000,
    "note": "2026-10-10 真实冒烟会话实测(单次会话,session_hit 加权口径,委派+外部工具两回合)"
  }
```

FR-108 分支(仅当 Step 1-3 因凭据/网络/安装约束无法完成):`engine.lock` baseline note 改为 `"尚未建立:<具体环境原因>"`,cache_hit_rate 保持 0.0,并在 p3-smoke.md 如实记录已完成部分与未完成原因——不得以自动化测试冒充真实冒烟。

在 `docs/changes/002-p2/report.md` 限制条目 #1 处追加一行(不改原文):`> P3 已关闭:003-p3-2-1-296 两形状容忍 + 重钉 + 复冒烟,见 docs/evidence/p3-smoke.md(冒烟未跑成时改为:复冒烟仍受阻,见 p3-smoke.md FR-108 记录,本限制保持开放)`。

- [ ] **Step 6: 全量门禁 + Commit**

Run: `bash scripts/check-engine-lock.sh && go test ./... && make lint`
Expected: PASS

```bash
git add engine.lock docs/evidence/p3-smoke.md docs/changes/002-p2/report.md
git commit -m "docs(evidence): P3 真实冒烟+基准回填+P2 限制关闭(003 FR-107/108/110;SC-001/003/005)"
```
