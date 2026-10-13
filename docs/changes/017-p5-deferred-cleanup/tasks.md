# P5 deferred 清理 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 016 终审 3 项 deferred minor 清偿:env 解析加固、门控否决分支钉、RegisterSelfRefTools 双真源收敛。

**Architecture:**(bounded,设计文档已批准:`.mulix/.runtime/017-p5-deferred-cleanup/specs/2026-10-13-deferred-cleanup-design.md`)①`parseKernelKeys(string) []string` 小函数(TrimSpace+滤空段)替换 ServeMCP 内联 split;②`RegisterSelfRefTools` 删 `standard bool` 参数(签名收敛 `(profile, keys, tools, reg)`,函数内判定 standard);③TestBootEnvKernelKeys 表驱动加无 mulix 条目否决分支。

**Tech Stack:** Go 标准库(零第三方新增)。

**Spec:** `docs/specs/017-p5-deferred-cleanup/spec.md`

**Design:** `.mulix/.runtime/017-p5-deferred-cleanup/specs/2026-10-13-deferred-cleanup-design.md`

## Global Constraints

- FR-104 / SC-004:每任务 `go test ./... -count=1` 全绿再 commit;`go vet ./...`+`gofmt -l` 干净;`go run ./cmd/mulix lint layers` 0 违规。
- 零第三方新增;分层矩阵零变更(无新包/新边)。
- 生产行为面收窄:解析更严(脏值滤除)与签名收敛不改变 standard/minimal 注册语义——`TestRegisterSelfRefTools` 语义等价迁移。

## Review Focus

- parseKernelKeys 形态:`"a, b,,c,"` → `[a b c]`、空串 → nil(非空非 nil 纪律与 kernel.Keys 一致)。
- 签名收敛后 minimal 分支仍不注册(双真源退役不改变注册语义)。
- 否决分支钉的 Registry 必须真无 mulix 条目(`Name: "other"`)——门控回归可红。

---

## Task 1: parseKernelKeys + RegisterSelfRefTools 单真源

**Files:**
- Modify: `internal/cli/mcpserve.go`(ServeMCP 内联 split → parseKernelKeys;RegisterSelfRefTools 调用点签名同步)
- Modify: `internal/tools/mulixkernel/inspect.go`(RegisterSelfRefTools 删 standard bool)
- Test: `internal/tools/mulixkernel/mulixkernel_test.go`(TestParseKernelKeys 新增;TestRegisterSelfRefTools 语义等价迁移。cli 侧无测试改动——解析测试在 mulixkernel 包,消费点由既有 cli 包测试覆盖)

**Interfaces:**
- Produces: `func ParseKernelKeys(v string) []string`(mulixkernel 导出——cli 与包内测试同一实现;v 空/全空白 → nil;段 TrimSpace+滤空);`func RegisterSelfRefTools(profile config.ProfileMode, keys []string, tools func() map[string]registry.Tool, reg func(registry.Tool))`(profile==ProfileStandard 判定,standard bool 参数删除)。
- Consumes: 016 既有 NewKernelInspectTool/NewCordisRunTool(签名不变)。

- [ ] **Step 1: 写失败测试**

`internal/tools/mulixkernel/mulixkernel_test.go` 追加(并同步 TestRegisterSelfRefTools——旧签名编译失败即 RED 形态):

```go
// TestParseKernelKeys 钉 017 FR-101:env 进程边界脏值滤除——TrimSpace+
// 滤空段;空串 → nil(降级非阻断形态)。
func TestParseKernelKeys(t *testing.T) {
	got := ParseKernelKeys("a, b,,c,")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("ParseKernelKeys dirty = %v, want [a b c]", got)
	}
	if got := ParseKernelKeys("  "); got != nil {
		t.Fatalf("ParseKernelKeys blank = %v, want nil", got)
	}
	if got := ParseKernelKeys(""); got != nil {
		t.Fatalf("ParseKernelKeys empty = %v, want nil", got)
	}
	if got := ParseKernelKeys("profile"); len(got) != 1 || got[0] != "profile" {
		t.Fatalf("ParseKernelKeys single = %v, want [profile]", got)
	}
}
```

TestRegisterSelfRefTools 同步(替换旧实现——签名变化):

```go
// TestRegisterSelfRefTools 钉 017 FR-103/SC-003:profile 单真源——
// standard 注册 kernel_inspect/cordis_run;minimal 不注册(无 bool 矛盾面)。
func TestRegisterSelfRefTools(t *testing.T) {
	var registered []string
	reg := func(t registry.Tool) { registered = append(registered, t.Name()) }

	RegisterSelfRefTools(config.ProfileMinimal, nil, toolsFunc, reg)
	if len(registered) != 0 {
		t.Fatalf("minimal registered = %v, want empty", registered)
	}
	RegisterSelfRefTools(config.ProfileStandard, []string{"profile"}, toolsFunc, reg)
	if len(registered) != 2 || registered[0] != "kernel_inspect" || registered[1] != "cordis_run" {
		t.Fatalf("standard registered = %v, want [kernel_inspect cordis_run]", registered)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./internal/tools/mulixkernel/ -count=1`
Expected: FAIL(`ParseKernelKeys 未定义`+`RegisterSelfRefTools 参数不符`——编译期 RED,计划预期)。

- [ ] **Step 3: 实现**

`internal/tools/mulixkernel/inspect.go`——RegisterSelfRefTools 替换:

```go
// RegisterSelfRefTools 是自指工具注册的 testable seam(015 RegisterPTCExecute
// 同款形态):profile 单真源(017 FR-103——bool 与 profile 双参数的矛盾面退役),
// standard 守卫 + 顺序注册 kernel_inspect→cordis_run(调用方保证在 NewServer
// 冻结前、ptc_execute 注册之后调用)。minimal 不注册。
func RegisterSelfRefTools(profile config.ProfileMode, keys []string, tools func() map[string]registry.Tool, reg func(registry.Tool)) {
	if profile != config.ProfileStandard {
		return
	}
	reg(NewKernelInspectTool(keys, profile, tools))
	reg(NewCordisRunTool(tools, 30*time.Second))
}

// parseKernelKeys → 导出形态(017 FR-101):env 是进程边界——脏值(空格/
// 尾逗号/空段)不得以幽灵键形态进入巡检输出。TrimSpace+滤空段;空串/全空白
// → nil(降级非阻断,与 NewKernelInspectTool nil-keys 形态一致)。
func ParseKernelKeys(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var keys []string
	for _, k := range strings.Split(v, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}
```

(import 补 `strings`。)

`internal/cli/mcpserve.go`——ServeMCP 内解析与注册调用点替换(原 `var kernelKeys []string; if v := os.Getenv(...); v != "" { strings.Split... }` 块整体替换):

```go
	kernelKeys := mulixkernel.ParseKernelKeys(os.Getenv("MULIX_KERNEL_KEYS"))
	mulixkernel.RegisterSelfRefTools(
		cfg.Profile.Normalized(),
		kernelKeys,
		func() map[string]registry.Tool { return registry.SnapshotToolMap() },
		registry.Register,
	)
```

(`strings` import 在 mcpserve.go 全文件仅此一处消费(评审已核验)——删除解析块后必须同步删除该 import,否则 unused import 编译失败。)

- [ ] **Step 4: 跑测试验证通过 + 零回归**

Run: `go test ./internal/tools/mulixkernel/ ./internal/cli/ -count=1 -v && go test ./... -count=1 && go vet ./... && gofmt -l internal/ && go run ./cmd/mulix lint layers`
Expected: PASS/全绿/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/tools/mulixkernel/ internal/cli/mcpserve.go
git commit -m "refactor(mulixkernel,cli): ParseKernelKeys 加固+RegisterSelfRefTools 单真源(017 FR-101/103;P5 清理 T1)"
```

---

## Task 2: 门控否决分支钉 + 全矩阵回归

**Files:**
- Test: `internal/control/memory_injection_test.go`(TestBootEnvKernelKeys 表驱动加否决分支)

**Interfaces:**
- Consumes: Task 1 全部产出;016 既有 TestBootEnvKernelKeys 表驱动骨架。
- Produces: 无生产面——纯测试加固(SC-002)。

- [ ] **Step 1: 写失败测试(扩表)**

TestBootEnvKernelKeys 表驱动加分支(现骨架 `internal/control/memory_injection_test.go`):

```go
		{name: "keys but no mulix entry = no env", keys: []string{"profile"}, wantEnvVar: false, noMulixEntry: true},
```

结构体加字段 `noMulixEntry bool`;t.Run 内:

```go
			c := newTestControllerCapturingBoot(t, fake, sink, &boot, func(o *Options) {
				o.KernelKeys = tc.keys
				if tc.noMulixEntry {
					o.Registry = registry.NewServer(registry.Options{Name: "other", Version: "test"})
				} else {
					o.Registry = registry.NewServer(registry.Options{Name: "mulix", Version: "test",
						SelfCommand: []string{"mulix.exe", "mcp", "serve"}})
				}
			})
```

- [ ] **Step 2: 跑测试验证通过性形态**

本分支是对既有生产门控(controller.go `len>0 && hasMulixRegistryEntry()`)的钉——生产门控已正确,新分支直接 PASS(测试加固形态,非 RED-first:构造自然 RED 须先破坏生产代码,与本变更生产零变更冲突;台账记 Ruling——以 Step 3 的 mutation 见证替代 RED,mutation FAIL 输出落文件留痕)。先跑:

Run: `go test ./internal/control/ -run TestBootEnvKernelKeys -count=1 -v`
Expected: 三分支全 PASS——**新分支直接 PASS = 预期形态,非误判**(生产门控已正确;本变更为纯测试加固)。

- [ ] **Step 3: 验证钉有效性(Ruling 记录)**

临时注释 `controller.go` 的 `&& c.hasMulixRegistryEntry()`(本地验证,不提交),复跑:

Run: `go test ./internal/control/ -run TestBootEnvKernelKeys -count=1`
Expected: FAIL(no mulix entry 分支红——钉有效)。**还原代码**,再复跑 PASS。

- [ ] **Step 4: 全矩阵回归(SC-004)**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l . && go run ./cmd/mulix lint layers`
Expected: 全绿/干净/0 违规

- [ ] **Step 5: Commit**

```bash
git add internal/control/memory_injection_test.go
git commit -m "test(control): 内核键桥否决分支钉——hasMulixRegistryEntry 单独路径(017 FR-102;P5 清理 T2)"
```
