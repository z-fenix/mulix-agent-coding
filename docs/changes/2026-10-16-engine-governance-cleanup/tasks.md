# 引擎治理收尾 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 清偿上变更两项 deferred——buildArgs settingsTemp 失败泄漏与 Task 流形态核验落档。

**Architecture:** ① buildArgs mcp 段失败分支补清理（可测性经包级 `createTempFile` seam）；② engine doc.go 协议小节固化「Task 父调用即治理面/子事件不上流/forwarding 红线」三项事实。

**Tech Stack:** Go 1.27；零新增依赖。

**Spec:** `docs/specs/2026-10-16-engine-governance-cleanup/spec.md`

**Design:** approved（bounded）：`.mulix/.runtime/2026-10-16-engine-governance-cleanup/specs/2026-10-16-cleanup-design.md`

## Global Constraints

- TDD 先红后绿；每任务 `go test ./... -count=1` 全绿 + `go vet ./...` 干净 + 改动 .go 文件 gofmt（LF 副本口径）零偏差才 commit；conventional 前缀；只 add 本任务文件。
- engine.lock 与既有治理语义零改动（FR-304）。
- 文档为中文（仓内惯例）。

## Review Focus

- seam 变量不得改变生产行为（默认值 = os.CreateTemp；测试外零覆写）。
- 失败清理必须覆盖 mcp 段全部三个失败 return（CreateTemp/Write/Close）。
- doc.go 红线表述不得弱化为「建议」——是后续接线的硬前置。

---

 buildArgs 失败路径临时文件清理

## Task 1: buildArgs 失败路径临时文件清理

**Files:**
- Modify: `internal/engine/claudehost.go`（createTempFile seam + mcp 段失败分支清理）
- Test: `internal/engine/claudehost_test.go`

**Interfaces:**
- Consumes: 既有 buildArgs 4 返回值签名与物化顺序（settings 先、mcp 后）。
- Produces: 失败路径零残留保证（新测试钉住）；`createTempFile` 包级 seam（测试覆写用）。

- [X] **Step 1: 写失败测试**——`internal/engine/claudehost_test.go` 追加：

```go
// TestBuildArgsCleansSettingsTempOnMcpFailure pins：mcp 段物化失败时，
// 本调用已落盘的 settings 临时文件必须被清理（FR-301 失败路径零残留）。
func TestBuildArgsCleansSettingsTempOnMcpFailure(t *testing.T) {
	h := NewClaudeHost(ClaudeHostOptions{LockPath: testLockPath(t)})
	cfg := BootConfig{
		PermissionMode: "default",
		SettingSources: []string{"user"},
		SettingsJSON:   `{"permissions":{"ask":["Bash"]}}`,
		MCPConfigJSON:  `{"mcpServers":{}}`,
	}
	// seam：第一次调用（settings 物化）成功；第二次（mcp 物化）失败。
	var settingsPath string
	calls := 0
	restore := createTempFile
	createTempFile = func(dir, pattern string) (*os.File, error) {
		calls++
		if calls == 1 {
			f, err := os.CreateTemp(dir, pattern)
			if err == nil {
				settingsPath = f.Name()
			}
			return f, err
		}
		return nil, errors.New("inject: mcp create failure")
	}
	t.Cleanup(func() { createTempFile = restore })
	_, _, _, err := h.buildArgs(cfg)
	if err == nil {
		t.Fatal("mcp failure must surface as error")
	}
	if _, statErr := os.Stat(settingsPath); !os.IsNotExist(statErr) {
		t.Fatalf("settings temp leaked on mcp failure: %v", statErr)
	}
}
```

（imports 补 `errors`。）

- [X] **Step 2: 跑测试看它失败**

Run: `go test ./internal/engine/ -run TestBuildArgsCleansSettingsTempOnMcpFailure -count=1`
Expected: FAIL——`createTempFile undefined`（编译期，seam 未实现）。

- [X] **Step 3: 最小实现**——`internal/engine/claudehost.go`：

```go
// createTempFile 是临时文件物化 seam（测试注入失败用；生产 = os.CreateTemp）。
var createTempFile = os.CreateTemp
```

buildArgs 内两处 `os.CreateTemp(` 改为 `createTempFile(`；mcp 段三个失败 return 改为：

```go
		if err != nil {
			_ = os.Remove(settingsTemp) // 失败路径零残留（FR-301）
			settingsTemp = ""
			return nil, "", "", fmt.Errorf("engine: materialize mcp-config: %w", err)
		}
```

（Write/Close 两分支保留既有 `f.Close()`/`os.Remove(f.Name())` 与各自错误文案（"write/close mcp-config"），仅在其 return 前追加 `_ = os.Remove(settingsTemp); settingsTemp = ""` 两行——mcp 自身临时文件清除不得丢失。）

- [X] **Step 4: 跑测试看它通过**

Run: `go test ./internal/engine/ -count=1`
Expected: PASS（新测试 + 引擎包全绿）。

- [X] **Step 5: 全量 + 提交**

Run: `go test ./... -count=1 && go vet ./...`
Expected: 全绿/干净。

```bash
git add internal/engine/claudehost.go internal/engine/claudehost_test.go
git commit -m "fix(engine): buildArgs 失败路径清理已物化临时文件——零 $TMP 残留(上变更终审 Minor)"
```

---

## Task 2: engine 协议文档——Task 流形态结论与 forwarding 红线

**Files:**
- Modify: `internal/engine/doc.go`

**Interfaces:**
- Produces: 协议事实与红线的 durable 落档（FR-303）；零代码行为。

- [X] **Step 1: 扩写 doc.go**——包注释下追加「子代理流形态（2026-10-16 核验）」小节：

```go
// 子代理流形态（2026-10-16 核验，引擎 2.1.30x）：
//   - 引擎原生 Task 工具在泵面上呈现为普通 tool_use→tool_result——父调用
//     即治理面（经 --permission-prompt-tool 审批回路），与 shell 工具同型。
//   - 子代理内部的 text/thinking/tool 事件不上流：引擎仅在显式
//     --forward-subagent-text（--print + stream-json）下才以带
//     parent_tool_use_id 的 assistant/user 消息转发——mulix 未接线该 flag，
//     泵面无子事件，L2 围堵无误杀面。
//   - 红线：未来任何接线引入 --forward-subagent-text 前，必须先做
//     parent_tool_use_id 治理建模（子事件直通泵会令 L2 围堵对未裁决子调用
//     响亮终止，或需显式白名单裁定）——不得静默开启。
```

- [X] **Step 2: 核验收口**——

Run: `go build ./... && go test ./... -count=1 && go vet ./... && grep -rn "forward-subagent-text" internal/ cmd/ --include="*.go" | grep -v _test`
Expected: 全绿；grep 输出仅含 internal/engine/doc.go 命中行（红线文档本身），exit 0。

- [X] **Step 3: 提交**

```bash
git add internal/engine/doc.go
git commit -m "docs(engine): Task 流形态结论与 forwarding 治理红线落档(上变更终审 Declined-to-judge 清偿)"
```
