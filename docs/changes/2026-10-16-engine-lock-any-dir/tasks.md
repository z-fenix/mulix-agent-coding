# engine.lock 任意目录解析（026）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task.

**Goal:** 引擎锁解析链（工作区根 → exe 同目录 → 相对路径），mulix-cli 从任意目录可执行；校验语义零改动。

**Architecture:** `internal/cli/session.go` productionHostFactory 的 `LockPath: "engine.lock"` 换成 `resolveEngineLockPath(o.Workspace)`（cli 组合根新增解析函数：工作区根 → exe 同目录 → 相对路径，命中即止，全缺回退原相对路径保持既有错误形态）。engine/lock.go 校验零改动。

**Tech Stack:** 既有栈；零新增依赖。

**Spec:** `docs/specs/2026-10-16-engine-lock-any-dir/spec.md`

**Design:** approved in chat（2026-10-16）：解析链三态由用户裁定目标唯一确定；治理校验面零改动。

## Global Constraints

- 校验语义零改动（漂移拒绝/放行/损坏拒绝）；TDD；全量绿 + vet + gofmt（diff 文件）才 commit。

## Review Focus

- 解析链命中即止与全缺回退；os.Executable 失败（测试环境）安全退化；既有测试（fake host 不读锁）不受影响。

---

## Task 1: 解析链 + 真机验证

**Files:**
- Modify: `internal/cli/session.go`（resolveEngineLockPath + productionHostFactory 接线）
- Test: `internal/cli/engine_lock_resolve_test.go`（新）

**Interfaces:**
- Produces: `func resolveEngineLockPath(workspace string) string`——候选序：workspace/engine.lock → exe 同目录/engine.lock → "engine.lock"；`os.Stat` 命中即返。

- [ ] **Step 1: 写失败测试**——表驱动三态：工作区锁命中优先；工作区缺 + exe 同目录有（os.Executable 不可控——改测候选函数注入形态：`resolveEngineLockPathFrom(candidates []string)` 纯函数 + `resolveEngineLockPath` 组装候选）命中第二候选；全缺回退 "engine.lock"。
- [ ] **Step 2: 验红**。Run: `go test ./internal/cli/ -run TestResolveEngineLockPath -count=1`。Expected: FAIL（函数未定义）。
- [ ] **Step 3: 实现**——纯函数 + 组装；productionHostFactory 接线。
- [ ] **Step 4: 验绿 + 全量**。Run: `go test ./internal/cli/ -count=1 && go test ./... -count=1 && go vet ./... && gofmt -l $(git diff --name-only HEAD 2>/dev/null) 2>/dev/null; echo FMT`。
- [ ] **Step 5: 真机验证**——`go build -o C:/Users/Zhang/go/bin/mulix-cli.exe ./cmd/mulix-cli && cp engine.lock C:/Users/Zhang/go/bin/`，从 `C:\Users\Zhang` 驱动启动冒烟（ConPTY 或用户手跑）——TUI 就绪即锁解析生效。
- [ ] **Step 6: Commit**

```bash
git add internal/cli/session.go internal/cli/engine_lock_resolve_test.go
git commit -m "fix(cli): 引擎锁解析链——工作区根→exe 同目录→相对路径,任意目录可执行(026 FR-001)"
```
