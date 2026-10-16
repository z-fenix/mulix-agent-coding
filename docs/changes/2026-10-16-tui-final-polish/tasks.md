# TUI 收尾打磨（023 路由项）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 清偿 023 终审路由的三项遗留：resize 阅读锚点、EngineExited 运行中工具卡清扫、三处死代码删除。

**Architecture:** 全部在既有 internal/tui 内做——锚点复用 wrapcache 的按块行结构（重排前后按「块下标+块内行偏移」对位）；清扫复用 toolCard 既有 failed 终态（不新增枚举）；死代码纯删除。接缝与治理面零改动。

**Tech Stack:** Go 1.26 + Bubble Tea v2（既有）；零新增依赖。

**Spec:** `docs/specs/2026-10-16-tui-final-polish/spec.md`

**Design:** approved in chat（2026-10-16）：锚点=视口顶行→(块下标,块内行) 快照→重排后按 wrapBlockLines 前缀和对位恢复 YOffset；清扫=EngineExited 分支遍历运行中卡→failed+详情；删除=clearWrapCache/useLegacyViewportScrollClear/lines 及其测试引用。

## Global Constraints

- 治理语义零改动；TUI→control 单向；零新增依赖。
- TDD：先红后绿；每任务 `go test ./... -count=1` 全绿 + `gofmt -l .` + `go vet ./...` 才 commit；conventional 前缀；只 add 本任务文件；`.mulix/.runtime/**` 不入提交。
- 编译期/机械删除类 RED 须在台账记 Ruling。
- 界面字符串中文。

## Review Focus

- 锚点恢复的 ±1 行容差不得把尾随态拉离底部（followTail 分支不经过锚点路径）。
- EngineExited 清扫不得触碰已 failed/done 卡片，且幂等（重复 EngineExited 不二次改写）。
- 死代码删除后零残留引用（含测试）；lines() 的测试观测面迁移后断言语义不变。

---

## Task 1: resize 阅读锚点（FR-001）

**Files:**
- Modify: `internal/tui/model.go`（WindowSizeMsg 宽度变化分支）、`internal/tui/transcript.go`（锚点辅助）
- Test: `internal/tui/anchor_test.go`（新）

**Interfaces:**
- Consumes: transcriptState 的 wrappedLines/wrapBlockLines（wrapcache 四元组，T5 契约）；scrollState（T6）。
- Produces: `func (ts *transcriptState) anchorForOffset(yoff int) (blockIdx, lineInBlock int)`；`func (ts *transcriptState) offsetForAnchor(blockIdx, lineInBlock int) int`（块前缀行数累加 + 截断到末行防越界）；WindowSizeMsg 宽度变化且非尾随时：旧宽锚点→全量重排→新宽恢复 YOffset。

- [X] **Step 1: 写失败测试**——溢出模型（80×24、约 60 行内容）：手势上翻至中部（markUserScrolled、YOffset>0），记录视口顶行文本；宽度 80→50 变更后断言视口顶行文本与锚定块内容一致（±1 行）；对照组：尾随态宽度变更后仍在底部。
- [X] **Step 2: 验红**。Run: `go test ./internal/tui/ -run TestResizeAnchor -count=1`。Expected: FAIL（重排后顶行跳变）。
- [X] **Step 3: 实现**——WindowSizeMsg 宽度变化分支：非尾随时先 anchorForOffset(当前 YOffset)，重排（sync 全量重建已有路径）后 offsetForAnchor 恢复，**并 feedViewportContent 重喂视口**（重排后 sync 返回 false，finalize/frame 不会代喂——不喂则视口持旧宽行直至下次内容变更）。尾随路径不变。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/tui/ -run TestResizeAnchor -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l .`。Expected: 全绿。
- [X] **Step 5: Commit**

```bash
git add internal/tui/model.go internal/tui/transcript.go internal/tui/anchor_test.go
git commit -m "feat(tui): resize 阅读锚点——重排后按块对位恢复阅读位置(024 FR-001)"
```

## Task 2: EngineExited 运行中工具卡清扫（FR-002）

**Files:**
- Modify: `internal/tui/model.go`（EngineExitedEvent 分支）、`internal/tui/mapping_test.go`（矩阵加行）
- Test: `internal/tui/mapping_test.go`

**Interfaces:**
- Consumes: toolCard 状态机（T9：toolRunning/toolDone/toolFailed）、transcriptState 遍历与 invalidateWrapFrom。
- Produces: EngineExited 分支新语义：所有 state==toolRunning 的卡 → toolFailed + failedDetail「引擎已退出，工具未返回」；幂等（重复 EngineExited 无运行中卡则 no-op）。

- [X] **Step 1: 写失败测试**——运行中卡 + EngineExited → 断言卡状态 failed、渲染含「引擎已退出，工具未返回」、无「· 运行中」残留；已 failed 卡不受影响；矩阵表加该行。
- [X] **Step 2: 验红**。Run: `go test ./internal/tui/ -run TestEventRenderMatrix -count=1`。Expected: FAIL（新行不满足——卡片仍「运行中」）。
- [X] **Step 3: 实现**——EngineExited 分支：closeThinking/sealAnswer 之后遍历 blocks，命中运行中卡改 failed+detail，逐块 invalidateWrapFrom（或最低位一次）。
- [X] **Step 4: 验绿 + 全量**。Run: `go test ./internal/tui/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l .`。
- [X] **Step 5: Commit**

```bash
git add internal/tui/model.go internal/tui/mapping_test.go
git commit -m "feat(tui): 引擎退出清扫运行中工具卡为中断终态(024 FR-002)"
```

## Task 3: 死代码清理（FR-003）

**Files:**
- Modify: `internal/tui/wrapcache.go`（删 clearWrapCache）、`internal/tui/scrollstate.go`（删 useLegacyViewportScrollClear）+ `internal/tui/scrollstate_test.go`（删其测试）、`internal/tui/transcript.go`（删 lines）+ 观测面迁移 5 个测试文件（lines() 共 11 处调用：`transcript_test.go`×3、`theme_test.go`×4、`status_test.go`×2、`markdown_test.go`×1、`mapping_test.go`×1——计划评审员逐处核实）

**Interfaces:**
- Consumes: 023 终审路由裁定（三符号无生产消费方）。
- Produces: 无（纯删除）；transcript_test 观测面改为测试内 helper。

- [X] **Step 1: 引用清点（删除前基线）**。Run: `grep -rn "clearWrapCache\|useLegacyViewportScrollClear\|\.lines(" internal/ --include=*.go | grep -v _test.go`。Expected: 仅定义处（wrapcache.go 契约头注释与 scrollstate.go 头注释的字符串命中为注释残留，非代码引用——勿误读为违规）。
- [X] **Step 2: 删除 + 测试观测面迁移**——删三符号；包级测试 helper（`transcript_test.go` 声明，对 transcriptState 调 syncWrappedLines 后按 wrapBlockLines 前缀展开，断言语义逐字不变）替换 **11 处** lines() 调用（5 个测试文件，清单见上）。
- [X] **Step 3: 验绿 + 全量**。Run: `go build ./... && go test ./... -count=1 && go vet ./... && gofmt -l . && go test ./internal/lint/ -count=1`。Expected: 全绿（RED 形态=机械删除，无行为测试先行——台账记 Ruling）。
- [X] **Step 4: Commit**

```bash
git add internal/tui/wrapcache.go internal/tui/scrollstate.go internal/tui/scrollstate_test.go internal/tui/transcript.go internal/tui/transcript_test.go internal/tui/theme_test.go internal/tui/status_test.go internal/tui/markdown_test.go internal/tui/mapping_test.go
git commit -m "chore(tui): 清理死代码——clearWrapCache/useLegacyViewportScrollClear/lines(024 FR-003)"
```

---

## 计划自审（writing-plans Self-Review）

- **Spec 覆盖**：FR-001→T1；FR-002→T2；FR-003→T3；SC-001/002 由三任务的验绿步骤与矩阵行覆盖。无缺口。
- **Step 扫描**：每步单一动作；实现步只给签名与机制一句，无函数体转写。
- **类型一致**：anchorForOffset/offsetForAnchor 消费 wrapcache 四元组（T5 契约字段名）；清扫复用 toolFailed/failedDetail（T9 字段）。
- **Review Focus 钉点**：锚点尾随不回归→T1 对照组；清扫幂等/不碰终态卡→T2 断言；零残留引用→T3 Step 1 清点。
- **比例**：计划短于 spec 的实现面，无转写。
