# 020 总线 endpoint 会话收尾清理 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** endpoint 残留收口:main 进程出口接线 `CloseSharedBus()`(死代码接活)+ openSession 门控关残留卫生删除。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/020-bus-endpoint-session-teardown/specs/2026-10-13-teardown-design.md`)①`cmd/mulix/main.go` Execute 返回后调 `cli.CloseSharedBus()`(退出码语义不变;serve 无桥 no-op);②openSession `eventbusOn` else 侧删 `<ws>/.mulix/bus/endpoint`(错误忽略;与 018 门控开原子覆盖互补)。

**Tech Stack:** Go 标准库。

**Spec:** `docs/specs/020-bus-endpoint-session-teardown/spec.md`

**Design:** `.mulix/.runtime/020-bus-endpoint-session-teardown/specs/2026-10-13-teardown-design.md`

## Global Constraints

- FR-103 / SC-003:每任务 `go test ./... -count=1` 全绿再 commit;vet/gofmt/lint layers 干净;零第三方新增。
- 退出码语义不变;018/019 语义不回退(门控开原子覆盖/Close 删 endpoint/Ctrl-C nil)。
- serve 进程安全:busByWS 空→CloseSharedBus 空转 no-op(015 裁定)。

## Review Focus

- main 接线不吞错误:execErr 路径原样保留(收尾不改变退出语义)。
- 门控关删除仅在 else 侧(门控开走 OpenBus 原子覆盖——不双删竞态)。
- 残留删除失败静默(卫生面不阻断启动)——与「如实记录」边界:删除失败可容忍,不 stderr(高频无害路径)。

---

## Task 1: main 接线(进程边界——冒烟替代 RED)

**Files:**
- Modify: `cmd/mulix/main.go`(Execute 后接 CloseSharedBus)

**Interfaces:**
- Consumes: `cli.CloseSharedBus()`(session.go:770 导出,现零调用);019 Close 删 endpoint。
- Produces: 全命令正常退出后 endpoint 清理(真机冒烟证据)。

- [ ] **Step 1: 写失败测试**

进程边界单元不可钉(设计已裁定 Ruling 形态):本任务验证=真机冒烟(Step 4)+ 既有钉回归(TestBusCloseRemovesEndpoint 已钉 Close 删 endpoint 语义)。无新测试文件。

- [ ] **Step 2: 冒烟基线(接线前;忠实对照 018 冒烟场景——enable=true + 完整 run)**

Run: `go build -o /tmp/mulix020a.exe ./cmd/mulix && WS=$(mktemp -d) && cp engine.lock $WS/ && printf '[eventbus]\nenable = "true"\n' > $WS/mulix.toml && cd $WS && timeout 90 ./mulix020a.exe run "回复:好" > run.out 2>&1; ls .mulix/bus/`
Expected(基线): `endpoint` 仍在(接线前残留实证——注意本基线须在 T2 落地前执行,否则门控关路径无关;此场景门控开,与 T2 无耦合)。留档进证据文档。

- [ ] **Step 3: 实现**

`cmd/mulix/main.go`:

```go
func main() {
	opts := cli.Options{Workspace: cwd()}
	cmd := cli.NewRootCommand(opts)
	execErr := cmd.Execute()
	// 总线收尾(020 FR-101):单点覆盖全命令——CloseSharedBus 逐 Bus Close
	// (019 已含 endpoint 删除);serve 无桥=busByWS 空→空转 no-op(015 裁定
	// 天然安全)。execErr 仍按原语义退出(收尾不改变退出语义)。
	cli.CloseSharedBus()
	if execErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mulix:", execErr)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: 真机冒烟(接线后)**

Run: `go build -o /tmp/mulix020b.exe ./cmd/mulix && cd <tmpws> && cp engine.lock . && printf '[eventbus]\nenable = "true"\n' > mulix.toml && ./mulix020b.exe run "回复:好" > run.out 2>&1; ls .mulix/bus/`
Expected: 目录无 endpoint(018 同场景冒烟是「endpoint 残留」——本任务翻转);run.out 正常收口。证据落 `docs/evidence/020-teardown-smoke.md`(接线前后对比+退出码)。

Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers`
Expected: 全绿/干净/0 违规。

- [ ] **Step 5: Commit**

```bash
git add cmd/mulix/main.go docs/evidence/020-teardown-smoke.md
git commit -m "fix(cli): main 进程出口接线 CloseSharedBus——endpoint 收尾清痕(020 FR-101;T1)"
```

---

## Task 2: 门控关残留卫生 + 回归

**Files:**
- Modify: `internal/cli/session.go`(eventbusOn else 侧)
- Test: `internal/cli/bridge_warn_test.go` 或新 `teardown_test.go`(残留清理钉)

**Interfaces:**
- Consumes: Task 1 无强依赖。
- Produces: 门控关+残留→endpoint 删除(错误忽略)。

- [ ] **Step 1: 写失败测试**

`internal/cli/teardown_test.go`(Create):

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStaleEndpointRemovedWhenGateOff 钉 020 FR-102:门控关=无进程持有
// 总线——endpoint 必为死残留(kill/crash 收尾无法覆盖),openSession 侧
// 删除清痕;删除失败不阻断启动。
func TestStaleEndpointRemovedWhenGateOff(t *testing.T) {
	ws := t.TempDir()
	busDir := filepath.Join(ws, ".mulix", "bus")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(busDir, "endpoint")
	if err := os.WriteFile(stale, []byte("nats://127.0.0.1:1"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 门控关会话(cfg 无 [eventbus])——经 assembleController 不可轻启,
	// 残留清理落 sharedEventBus 邻位:直调形态以 openSession 同函数核验。
	// 实现者以最小可达锚核验(见 Step 3 注)。
	h := mustOpenSession(t, Options{Workspace: ws, SessionsRoot: ws, UsageRoot: ws,
		Config: testConfig(), HostFactory: fakeHostFactory(t)}, "", nil)
	h.close() // close() 无返回值(session.go:616)——语句式调用(评审 Minor 1)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale endpoint after gate-off session = %v, want removed", err)
	}
}
```

(注:mustOpenSession 走完整 openSession→assembleController——门控关分支在 assembleController 内,锚点可达;清理点实现于 eventbusOn else 侧,测试经全链驱动。)

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/cli/ -run TestStaleEndpointRemovedWhenGateOff -count=1`
Expected: FAIL(stale endpoint after gate-off session = <nil>, want removed——现码不删)。

- [ ] **Step 3: 实现**

`internal/cli/session.go` assembleController 门控块 else 侧:

```go
	if eventbusOn {
		…(018 既有,不动)
	} else {
		// 残留卫生(020 FR-102):门控关=无进程持有总线——endpoint 必为
		// 死残留(kill/crash 收尾无法覆盖),删除清痕;错误忽略(卫生面
		// 不阻断启动)。
		_ = os.Remove(filepath.Join(o.Workspace, ".mulix", "bus", "endpoint"))
	}
```

- [ ] **Step 4: 跑测试验证通过 + 全矩阵回归**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers`
Expected: 全绿/干净/0 违规。

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ 
git commit -m "fix(cli): 门控关残留 endpoint 卫生删除——kill/crash 残留清痕(020 FR-102;T2)"
```
