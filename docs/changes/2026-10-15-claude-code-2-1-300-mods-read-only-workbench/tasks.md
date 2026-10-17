# Claude Code 2.1.301 Read-only Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking; only verified executed steps become `- [X]`.

**Goal:** 将项目锁定目标升级为 2.1.301，交付原生 Claude Code 内按需打开的只读阶段/用量工作台，且不转移 Go 宿主权威。
**Architecture:** 原生 mod 读取工作流 CLI 的只读状态及当前原生主会话用量；纯状态归约与工作流查询独立于原生事件/绘制适配。live 开发目录遵循 plugin-authoring，repo 保存同内容自有文件；两者不包含生成声明或用户配置。
**Tech Stack:** 既有 Go；TypeScript >=5.4；Claude Code 2.1.301 intrinsic `claude-code` / `claude-code/testing`；原生 Box/Text/Button；交付检查仅用 Node 内建模块。无新增产品运行时第三方依赖。
**Spec:** `docs/specs/2026-10-15-claude-code-2-1-300-mods-read-only-workbench/spec.md`
**Normative target amendment:** `.mulix/.runtime/2026-10-15-claude-code-2-1-300-mods-read-only-workbench/specs/2026-10-16-target-2-1-301-amendment.md`
**Design:** `.mulix/.runtime/2026-10-15-claude-code-2-1-300-mods-read-only-workbench/specs/2026-10-16-mods-read-only-workbench-design.md`
**Status:** User-approved tasks (2026-10-16 /goal); independent review Approved; execution not yet started at approval recording. User requests master/no new branch; direct default-branch commits are deferred under the executor's higher-priority branching constraint.

## Global Constraints

- 有效目标为用户明确更新的 **2.1.301**；目录名中的 2-1-300 是历史 ID，不重命名。主 spec 与规范性版本修订共同消费；不把旧版 API/基线冒称新版验证。
- A：仅当前原生 Claude Code **主会话**用量。不查产品账本，不从 Pane 当前 agent 视图切换统计来源。
- 原生工作台不是 Go TUI 的嵌入界面。不得改 Go 引擎算法、TUI、权限、memory、agent、NATS、注册表或镜像权威；镜像先持久化/单写/1:1、默认版本漂移拒绝不变。
- `/mulix-status`、`/mulix-cache` 仅用户明确调用才打开同一 Pane；无常驻带、周期轮询、自动弹窗/完成通知。外国命令不截获；自己的面板命令结果无 text/context。
- 运行时不写产品/流程文件或配置，不批准/推进阶段，不提交任务，不调用模型、额外计数、HTTP、工具、agent、压缩或凭据；仅本插件 typed state 可变。
- 费用无可靠覆盖标志时必须不可得，cost 缺失/0/正金额均不生成费用数值。token 的已知 0 与未知分开；命中率按输入 token 加权，主回合去重，观察上限 4096。
- 实施只在 build 后。用户最新 /goal 明确要求不建分支、在 master 上执行，覆盖此前设计的实施分支安排；本轮在 master 实现与验证，不新建分支/worktree、不重置既有改动。执行者另受默认分支提交前须建分支的更高优先级约束，所以不直接 commit master、不擅自建分支；提交步骤保持未执行并真实记录，不能用占位哈希、其他会话或 plumbing 绕过。HEAD/源接口变化先核对并记录。
- 前置：再次验证 CLI 精确 2.1.301；用户已单独授权在任务计划获批、进入 build 后隔离安装仅开发验证用的 TypeScript（>=5.4、固定版本、禁安装脚本、不改全局或产品依赖），尚未实际安装。缺编译器/安装被拒/网络受阻是 blocked，不是 feature RED，不静默跳过类型检查，不借其他工具/会话绕过。
- 本计划批准本身不等于安装、热加载或付费模型冒烟授权；上述隔离安装依据单独的真实答复，仍限 build 后。用户独立回答原生热加载询问；未启用不写成已加载。发布、push、归档另行审批。本地 commit 步骤也只在用户明确授权逐任务本地提交后执行，否则在检查点停下询问。
- 每个任务亲眼核对 RED/GREEN 与退出码，编译期 RED 要在 ledger 写 Ruling。每任务重跑 `go test ./... -count=1`；最终 build/vet/原始 gofmt 与真实证据分开。NO_COLOR/CRLF 等原始失败保留，不擅自修上游或归一化后冒称原环境全绿。
- 只用受守卫写工具改允许文件。live/repo 写入若拒绝就停止；不使用 shell copy/MCP 绕过。提交只列本任务文件，不暂存既有修改；runtime、生成声明、临时编译配置/工具及日志不入提交。

### 固定执行输入与命令约定

每次 Bash 调用重新定义下面的路径，不依赖跨调用 shell 状态。TSC 固定为满足声明最低版本的 **5.4.5**（编排者的兼容性选择，不称为最新版本）；尚未安装，实际可达性、版本与来源必须在获批 build 的 preflight 核实，未满足前禁止进入任务代码写入。

```bash
CHANGE=2026-10-15-claude-code-2-1-300-mods-read-only-workbench
ROOT='C:/Users/Administrator/workplace/project/tools/mulix-agent-coding'
RUNTIME="$ROOT/.mulix/.runtime/$CHANGE"
LIVE='C:/Users/Administrator/.claude/dev-mods/ba06d172-c58e-45c4-af01-0b3b3cb6bc53/mulix-workbench'
REPO="$ROOT/mods/mulix-workbench"
CLI='C:/Users/Administrator/AppData/Roaming/npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe'
SDK='C:/Users/ADMINI~1/AppData/Local/Temp/claude/bundled-skills/2.1.301/ed21ed00e157301ed737ca707d00a29f/plugin-authoring/types/claude-code.d.ts'
TSC_DIR="$RUNTIME/tooling/typescript-5.4.5"
TSC_JS="$TSC_DIR/node_modules/typescript/lib/tsc.js"
```

`CLI --version` 必须为 2.1.301；SDK 首行为 Written by Claude Code 2.1.301，已核对 SHA-256 `3f7d179c698b0fb2652ffeca99e9220fb3abfb79410179900ce75069b6c436e3`。临时路径若消失，重新取得同版本声明并核对，不使用 2.1.300 或更高版本代替。

仅在任务获批、进入 build 且执行方式已选定后，preflight 才执行隔离工具安装；不在 tasks 阶段运行下面的命令。TSC_DIR 首次使用必须是新建空目录；已有目录只读核验，版本或来源不符就停止，不删除/覆盖、不转用全局安装或其他镜像。安装从仓库根执行，不把 cwd 放在下载目录，不运行未知 setup 脚本：

```bash
npm install --prefix "$TSC_DIR" --ignore-scripts --no-audit --no-fund --save-exact typescript@5.4.5
node "$TSC_JS" --version
```

Expected: 编译器报告 `Version 5.4.5`；记录 npm 实际 registry、package-lock 的 version/resolved/integrity 与实际安装路径。未拿到正确包、权限/下载被阻或版本不符则 blocked，不把环境问题当 RED，不自动改源或换 latest，不绕过此前网络策略限制。

build 前在本 runtime 形成临时 `workbench-tsconfig.json`（绝不加入插件/提交）。使用 SDK 声明头给出的 target/lib es2023、types=[]、module esnext、moduleResolution bundler、strict/noUncheckedIndexedAccess/noEmit/skipLibCheck、jsx react / h / Fragment；include 是 SDK 文件和 LIVE 的 hooks/types/tests 的**绝对路径**。不引入 DOM/Node ambient types到 mod。

命令缩写，下文 Run 均先使用上述独立调用前缀：

- `NATIVE`: `"$CLI" plugin test "$LIVE"`
- `TYPECHECK`: `node "$TSC_JS" -p "$RUNTIME/workbench-tsconfig.json"`
- `VALIDATE`: `"$CLI" plugin validate --strict "$LIVE"`
- `GOALL`: `go test ./... -count=1`

长输出落本 change 的 `sdd/tasks/task-N-{red,green,go}.log`；执行者用 Read/Grep 核对关键失败/通过及退出码，不以文件存在作证据。不在 tasks 阶段创建这些实现/测试产物。

### File structure and dependency boundaries

| 文件 | 职责 / 首次负责任务 |
|---|---|
| `engine.lock`、`internal/engine/repository_lock_test.go`、主 spec 的当前目标同步 | 版本与历史保护 / T1 |
| `mods/mulix-workbench/.claude-plugin/plugin.json`、`hooks/hooks.json` | 插件加载元数据 / T2 |
| `types/index.d.ts`、`hooks/model.ts`、`tests/model.test.ts` | 唯一 JSON 状态契约与纯归约 / T2 |
| `hooks/workflow.ts`、`tests/workflow.test.ts` | 只读协议解析、受限查询 / T3 |
| `hooks/register.tsx`、`hooks/view.tsx`、`tests/host-fixture.ts`、`tests/register.test.tsx` | 原生适配/绘制及隔离测试端口 / T4 |
| `README.md`、`scripts/check-mod-delivery.cjs`、`scripts/check-mod-delivery.test.cjs`、本变更 evidence | 白名单副本校验、使用与验收 / T5 |

表中 `.claude-plugin`、hooks/types/tests 及 README 的 mod 相对路径同时适用于 LIVE 与 REPO。`scripts/check-mod-delivery*.cjs` 始终位于仓库根的 `scripts/`，不是 mod 内路径；engine.lock/internal/docs 也均为仓库根路径。先按 authoring 规范直接写 LIVE，GREEN 后按自有清单使用受守卫写工具同步 REPO。纯 workflow 从 model 分离为一文件只是任务分解，不改变已批准模块职责/调用面。

## Review Focus

1. `/clear`/resume 不再次触发 session.start：T2 退休旧 epoch，T4 验证旧 completion 和查询不会进入新会话。
2. stdout 含重复字段、未知枚举、终端控制符/秘密样式未知字段：T3 拒绝非法协议，只投影白名单，不直出 stderr。
3. 同会话再次初始化/重载的数据保留与去重、completion 无 start、观察上限：T2/T4 验证统计不被重复或伪补；真实热重载另见 T5。
4. 外国同名命令（含运行中更换提供方）、非用户来源、关闭后迟到结果、Pane 切到 agent：T4 不能截获/复活或改变统计来源。
5. LIVE/REPO 不一致、生成声明/额外文件/链接误入交付：T5 白名单与哈希检查；运行态或敏感内容不成为交付输入。

---

## Task 1: Repin 2.1.301 and reconcile effective specification

**Files:** Create/Test `internal/engine/repository_lock_test.go`; Modify `engine.lock`; Modify current-target portions of `docs/specs/2026-10-15-claude-code-2-1-300-mods-read-only-workbench/spec.md`; retain the already-authored review record `docs/changes/2026-10-15-claude-code-2-1-300-mods-read-only-workbench/plan-review.md`; track this task in this plan.
**Interfaces:** Consumes existing `LoadEngineLock(path string) (EngineLock, error)` from `internal/engine/lock.go`; Produces repository pin 2.1.301 with unchanged `protocol_probe.init_version_field`, historical baseline attribution and a spec consistent with the approved amendment. No new production Go API.

- [X] **Step 1: Add the root-pin regression test**

```go
package engine

import (
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestRepositoryLock2301(t *testing.T) {
    lock, err := LoadEngineLock(filepath.Join("..", "..", "engine.lock"))
    if err != nil { t.Fatal(err) }
    if lock.CLIVersion != "2.1.301" { t.Fatalf("want 2.1.301, got %q", lock.CLIVersion) }
    if _, err := time.Parse("2006-01-02", lock.PinnedAt); err != nil { t.Fatal(err) }
    if lock.PinnedAt == "2026-10-10" { t.Fatal("repin must record its actual new date") }
    if lock.ProtocolProbe.InitVersionField != "claude_code_version" { t.Fatal("probe contract changed") }
    if lock.Baseline.CacheHitRate != 0.953 { t.Fatal("historical sample changed") }
    for _, part := range []string{"2026-10-10 真实冒烟实测", "历史参考，未作2.1.301缓存重测"} {
        if !strings.Contains(lock.Baseline.Note, part) { t.Fatalf("missing provenance %q", part) }
    }
}
```

- [X] **Step 2: Verify RED**

Run: `go test ./internal/engine -run '^TestRepositoryLock2301$' -count=1`.
Expected: nonzero; `want 2.1.301, got "2.1.296"`, not a syntax/toolchain failure.

- [X] **Step 3: Apply only the approved version and document amendment**

Set root cli_version to 2.1.301, pinned_at to actual execution date; preserve old note and append exact `；历史参考，未作2.1.301缓存重测`. Keep probe and 0.953 unchanged. Synchronize main spec title/current target/FR-001/SC-001 and assumptions from the normative amendment; append the exact latest user ruling and design approval, retaining old quotes, ID, API hashes and history. Do not globally replace fixture versions, evidence or total-roadmap baseline.

- [X] **Step 4: Verify GREEN and existing drift behavior**

Run: `gofmt -w internal/engine/repository_lock_test.go`; `go test ./internal/engine -count=1`; `bash scripts/check-engine-lock.sh`; `GOALL`.
Expected: all pass, including existing immediate/deferred drift rejection and explicit-drift trace. Raw environment failure is recorded and investigated without widening scope.

- [ ] **Step 5: Local commit checkpoint**

After explicit local-commit authorization and clean formatting gate: add only engine.lock, the new test, own spec, own tasks.md and own plan-review.md; commit `chore(engine): pin Claude CLI 2.1.301 with historical baseline`. No push; record actual evidence/commit in the task ledger with normal task-done workflow.

## Task 2: Typed state and exact main-turn usage model

**Files:** Create LIVE/REPO `.claude-plugin/plugin.json`, `hooks/hooks.json`, `types/index.d.ts`, `hooks/model.ts`, `tests/model.test.ts`; initial no-op `hooks/register.tsx` is only a loader fixture until T4.
**Interfaces:** Consumes no other new module. Export from contract:

```ts
export type MulixWorkbenchIdentity = { sessionId: string; startedAt: number; root: string; version: string };
export type MulixWorkbenchCounts = { input: number; write: number; read: number; output: number };
export type MulixWorkbenchWorkflowFields = { change: string; phase: 'specify'|'clarify'|'design'|'tasks'|'build'|'verify'|'archive'; designApproved: boolean; verify: 'pending'|'pass'|'fail' };
export type MulixWorkbenchWorkflowSnapshot = { root: string; observedAt: number; fields: MulixWorkbenchWorkflowFields|null; reason: string|null; stale: boolean };
export type MulixWorkbenchNativeView = { sessionId: string; startedAt: number; observedAt: number; tokens: number|null; window: number|null; percent: number|null; cost: { kind: 'unavailable'; reason: 'pricing-coverage-unknown'|'host-cost-unavailable' } };
export type MulixWorkbenchObservation = { epoch: number; since: number|null; until: number|null; pendingTurnId: string|null; seenTurnIds: string[]; validTurns: number; missingTurns: number; invalidTurns: number; counts: MulixWorkbenchCounts; models: string[]; frozen: boolean };
export type MulixWorkbenchState = { schemaVersion: 1; identity: MulixWorkbenchIdentity|null; observation: MulixWorkbenchObservation; workflow: MulixWorkbenchWorkflowSnapshot|null; nativeUsage: MulixWorkbenchNativeView|null; warning: string|null; ui: { view: 'workflow'|'usage'; open: boolean; placed: boolean; busy: boolean; generation: number; error: string|null } };
```

Contract augments `PluginState` with literal `"mulix-workbench": { workbench: MulixWorkbenchState }`; no contract imports/references. `model.ts` exports `TARGET_VERSION='2.1.301'`, `MAX_SEEN_TURNS=4096`, and exact signatures:

```ts
freshState(): MulixWorkbenchState;
restoreState(raw: unknown): MulixWorkbenchState;
bindIdentity(s: MulixWorkbenchState, id: MulixWorkbenchIdentity): MulixWorkbenchState;
beginTurn(s: MulixWorkbenchState, turnId: string, now: number): MulixWorkbenchState;
finishTurn(s: MulixWorkbenchState, turnId: string, usage: unknown, now: number): MulixWorkbenchState;
retireSession(s: MulixWorkbenchState): MulixWorkbenchState;
cacheHit(s: MulixWorkbenchState): number|null;
projectNativeUsage(id: MulixWorkbenchIdentity, raw: unknown, now: number): MulixWorkbenchNativeView;
```

- [X] **Step 1: Write the model tests before model implementation**

`tests/model.test.ts` imports `test/expect` from `claude-code/testing` and the functions above; type aliases may be imported from the contract after GREEN. Essential assertions:

```ts
import { test, expect } from 'claude-code/testing';
import { freshState, restoreState, bindIdentity, beginTurn, finishTurn, retireSession, cacheHit, projectNativeUsage, MAX_SEEN_TURNS } from '../hooks/model';
const id = { sessionId:'native-1', startedAt:1, root:'C:/repo', version:'2.1.301' };
const u = (input: number, read: number, output: number) => ({ input_tokens:input, cache_creation_input_tokens:0, cache_read_input_tokens:read, output_tokens:output, model:'fixture-model' });
test('weighted input and exact turn ownership', () => {
  let s = bindIdentity(freshState(), id);
  s = finishTurn(s, 'unseen', u(1, 9, 1), 1);
  expect(s.observation.validTurns).toBe(0);
  s = finishTurn(beginTurn(s,'t1',10),'t1',u(90,10,5),11);
  s = finishTurn(beginTurn(s,'t2',20),'t2',{...u(0,900,20),model:'second-model'},21);
  expect(s.observation.models).toEqual(['fixture-model','second-model']);
  expect(s.observation.counts).toEqual({ input:90, write:0, read:910, output:25 });
  expect(cacheHit(s)).toBe(0.91);
  expect(finishTurn(s,'t2',u(0,900,20),22)).toEqual(s);
  expect(bindIdentity(s,{...id,root:'C:/other'}).observation.counts).toEqual(s.observation.counts);
  expect(bindIdentity(s,{...id,sessionId:'native-2'}).observation.validTurns).toBe(0);
  expect(retireSession(s).observation.pendingTurnId).toBe(null);
  expect(restoreState(JSON.parse(JSON.stringify(s))).observation.counts).toEqual(s.observation.counts);
  expect(restoreState({schemaVersion:0}).warning).not.toBe(null);
});
test('unknown is not zero and cost has no amount', () => {
  let s = bindIdentity(freshState(),id);
  expect(cacheHit(s)).toBe(null);
  s = finishTurn(beginTurn(s,'zero',1),'zero',u(0,0,0),2);
  expect(cacheHit(s)).toBe(null);
  s = finishTurn(beginTurn(s,'missing',3),'missing',undefined,4);
  expect(s.observation.missingTurns).toBe(1);
  for (const bad of [-1, NaN, Infinity, Number.MAX_SAFE_INTEGER+1]) {
    s = finishTurn(beginTurn(s,`bad-${String(bad)}`,5),`bad-${String(bad)}`,u(bad,0,0),6);
  }
  expect(s.observation.invalidTurns).toBe(4);
  for (const cost of [undefined,{usd:0},{usd:12.5}]) {
    const v = projectNativeUsage(id,{startedAt:1,context:{tokens:0,window:1000,percent:0},rateLimits:[],cost},9);
    expect(v.tokens).toBe(0);
    expect(v.cost.kind).toBe('unavailable');
    expect(Object.hasOwn(v.cost,'usd')).toBe(false);
    expect(JSON.stringify(v).includes('"usd"')).toBe(false);
    expect(JSON.stringify(v).includes('12.5')).toBe(false);
    expect(v.cost.reason).toBe(cost === undefined ? 'host-cost-unavailable' : 'pricing-coverage-unknown');
  }
});
test('bounded window stops rather than evicts dedupe', {timeoutMs:30000}, () => {
  let s = bindIdentity(freshState(),id);
  for (let n=0;n<MAX_SEEN_TURNS;n++) s=finishTurn(beginTurn(s,`t-${n}`,n),`t-${n}`,u(1,0,0),n);
  const counts=s.observation.counts;
  s=finishTurn(beginTurn(s,'beyond',5000),'beyond',u(10,0,0),5001);
  expect(s.observation.frozen).toBe(true);
  expect(s.observation.counts).toEqual(counts);
  expect(finishTurn(s,'t-0',u(100,0,0),5002).observation.counts).toEqual(counts);
});
```

```ts
test('overflow and retired pending turn never alter valid totals', () => {
  let s=bindIdentity(freshState(),id);
  s=finishTurn(beginTurn(s,'max',1),'max',u(Number.MAX_SAFE_INTEGER,0,0),2);
  s=finishTurn(beginTurn(s,'overflow',3),'overflow',u(1,0,0),4);
  expect(s.observation.counts.input).toBe(Number.MAX_SAFE_INTEGER);
  expect(s.observation.invalidTurns).toBe(1);
  const waiting=beginTurn(s,'old',5);
  const retired=bindIdentity(retireSession(waiting),{...id,sessionId:'native-2',startedAt:2});
  expect(finishTurn(retired,'old',u(1,0,0),6)).toEqual(retired);
  const moved=bindIdentity(s,{...id,root:'C:/other'});
  expect(moved.workflow).toBe(null);
  expect(moved.ui.generation).toBeGreaterThan(s.ui.generation);
});
```

- [X] **Step 2: Add only loader fixtures required to reach meaningful RED**

Create the three authoring files directly under LIVE: manifest name mulix-workbench/version 0.1.0/description (initially no `types` entry); modules `["./register.tsx"]`; register exports a typed no-op `Register`. No hooks or business behavior. This test harness setup is not model implementation; native hot-load consent is not inferred.

- [X] **Step 3: Verify compilation RED**

Run `NATIVE`. Expected: nonzero due to missing `../hooks/model` import, not missing manifest/unsupported engine. Record this compile-RED shape as a Ruling; tool/setup failures must be corrected before accepting RED.

- [X] **Step 4: Implement contract and pure model**

Add manifest `types:"./types/index.d.ts"`, the exact state contract and functions. Identity key is sessionId + startedAt; root-only changes invalidate workflow/query generation, not native conversation token ownership. Start must be seen before finish; no output in denominator; sums use nonnegative safe integers; missing/invalid turns are deduped; overflow does not alter valid totals. Retire invalidates pending/requests; same-identity restore preserves observed data. Invalid persisted schema resets with an explicit new-window warning. Freeze at 4096 retained IDs rather than evicting. No host I/O, raw cost storage, timer or prompt calls.

- [X] **Step 5: Verify GREEN, types and baseline**

Run `NATIVE`, `TYPECHECK`, `VALIDATE`, `GOALL`. Expected: zero exits and all model assertions pass. Synchronize only this task's owned files to REPO with guarded writes; do not copy generated types/config/logs.

- [ ] **Step 6: Local commit checkpoint**

After authorization/format gate, add only this task's canonical manifest/hooks/contract/model/test plus own plan; commit `chore(mods): add typed read-only usage model`. No runtime/live files in git.

## Task 3: Bounded workflow protocol and query

**Files:** Create LIVE/REPO `hooks/workflow.ts`, `tests/workflow.test.ts`.
**Interfaces:** Consumes T2 workflow types; Produces:

```ts
parseWorkflowOutput(text: string): MulixWorkbenchWorkflowFields|null;
queryWorkflow(io: MulixWorkbenchWorkflowIO): Promise<MulixWorkbenchWorkflowSnapshot>;
export type MulixWorkbenchWorkflowIO = {
  root(): Promise<string>;
  stat(path: string): Promise<{kind:'file'|'dir'|'other'}>;
  run(argv: readonly string[], init:{cwd:string;timeoutMs:number}): Promise<{exitCode:number;stdout:string;stderr:string;isStdoutTruncated:boolean;isStderrTruncated:boolean}>;
  now(): Promise<number>;
};
```

- [X] **Step 1: Write parser/query tests**

```ts
import { test, expect } from 'claude-code/testing';
import { parseWorkflowOutput, queryWorkflow } from '../hooks/workflow';
const good='change: c1\nphase: tasks\ndesign: track=architectural approved=true path=x\nverify_result: pending (failures: 0)\n';
test('workflow protocol never guesses approval', () => {
  expect(parseWorkflowOutput(good)).toEqual({change:'c1',phase:'tasks',designApproved:true,verify:'pending'});
  for (const bad of [good+'phase: build\n', good.replace('tasks','future'),good.replace('pending','ok'),good.replace('approved=true','approved='),good.replace('change: c1\n',''),good+'\x1b[31m']) expect(parseWorkflowOutput(bad)).toBe(null);
  const projected=parseWorkflowOutput(good+'credential: DO_NOT_RENDER\n');
  expect(JSON.stringify(projected).includes('DO_NOT_RENDER')).toBe(false);
});
test('fixed argv, root check and bounded I/O', async () => {
  const calls: unknown[]=[]; let root='C:/repo';
  const io={root:async()=>root,stat:async()=>({kind:'dir' as const}),now:async()=>7,
    run:async(argv:readonly string[],init:{cwd:string;timeoutMs:number})=>{calls.push({argv,init});return {exitCode:0,stdout:good,stderr:'',isStdoutTruncated:false,isStderrTruncated:false};}};
  expect((await queryWorkflow(io)).fields?.phase).toBe('tasks');
  expect(calls).toEqual([{argv:['mulix','state','show'],init:{cwd:'C:/repo',timeoutMs:2000}}]);
  expect((await queryWorkflow({...io,stat:async()=>({kind:'file' as const})})).fields).toBe(null);
  expect(calls.length).toBe(1);
  expect((await queryWorkflow({...io,run:async()=>{throw new Error('SECRET');}})).reason).not.toBe(null);
  const failed=await queryWorkflow({...io,run:async()=>({exitCode:1,stdout:'',stderr:'SECRET',isStdoutTruncated:false,isStderrTruncated:false})});
  expect(JSON.stringify(failed).includes('SECRET')).toBe(false);
  const cut=await queryWorkflow({...io,run:async()=>({exitCode:0,stdout:good,stderr:'',isStdoutTruncated:true,isStderrTruncated:false})});
  expect(cut.fields).toBe(null);
  const moved=await queryWorkflow({...io,run:async()=>{root='C:/other';return {exitCode:0,stdout:good,stderr:'',isStdoutTruncated:false,isStderrTruncated:false};}});
  expect(moved.fields).toBe(null);
});
```

```ts
test('CRLF and exact design grammar are bounded', () => {
  expect(parseWorkflowOutput(good.replace(/\n/g,'\r\n'))?.change).toBe('c1');
  for(const bad of ['',good+'x'.repeat(8193),good.replace('architectural','future'),good.replace('approved=true','approved=true approved=false'),good.replace('verify_result: pending (failures: 0)\n','')]) {
    expect(parseWorkflowOutput(bad)).toBe(null);
  }
});
test('stderr truncation is also unavailable', async () => {
  const r=await queryWorkflow({root:async()=>'C:/repo',stat:async()=>({kind:'dir' as const}),now:async()=>7,
    run:async()=>({exitCode:0,stdout:good,stderr:'HIDDEN',isStdoutTruncated:false,isStderrTruncated:true})});
  expect(r.fields).toBe(null);
  expect(JSON.stringify(r).includes('HIDDEN')).toBe(false);
});
```

Unknown failures use safe categories, not arbitrary exception/stderr text. The design line validates track as empty/bounded/architectural and an exact single approved boolean, while only projecting the approved boolean; extra unknown fields are never retained.

- [X] **Step 2: Verify RED**

Run `NATIVE`. Expected: missing workflow module/exports compile RED while prior model tests remain valid; record shape, not an environmental failure.

- [X] **Step 3: Implement read-only workflow functions**

Use anchored, unique canonical fields and phase/verify allow-lists; accept extra fields only by omitting them from the projection. Reject forbidden terminal controls (CRLF/tab separators are allowed), missing/duplicate/unknown required values and oversize/truncation. Verify `.mulix` kind `dir`, fixed argv/cwd/2000ms only; re-read root before accepting a result. All failures return fields=null with a safe reason and observation time; no raw stderr/unknown field retention. No YAML reader, network, dynamic shell or mutation.

- [X] **Step 4: Verify GREEN and synchronize own files**

Run `NATIVE`, `TYPECHECK`, `VALIDATE`, `GOALL`. Expected: zero exits; zero process calls for unmanaged root, exactly one fixed command for valid query, no unsafe text in returned snapshot.

- [ ] **Step 5: Local commit checkpoint**

After authorization/format gate, add only canonical workflow.ts/workflow.test.ts plus own plan; commit `chore(mods): add bounded workflow status query`.

## Task 4: Native command, lifecycle and Pane adapter

**Files:** Replace no-op LIVE/REPO `hooks/register.tsx`; Create `hooks/view.tsx`, `tests/host-fixture.ts`, `tests/register.test.tsx`.
**Interfaces:** Consumes all T2/T3 interfaces. Produces `export const register: Register`; `hooks/view.tsx` exports `type MulixWorkbenchActions = {refresh():Promise<void>;select(view:'workflow'|'usage'):Promise<void>;close():Promise<void>}` and `renderWorkbench($: EngineInterface, e: RenderInput<'Pane'>, state: MulixWorkbenchState, actions: MulixWorkbenchActions): RenderElement`. Import EngineInterface/RenderInput/RenderElement as types from `claude-code`, state from `../types`; register imports the view/action types. Actions are UI-only, not JSON state or contract fields, and never write captured-old-state values.

- [X] **Step 1: Create the native test fixture, not feature behavior**

`installHost(on: On)` returns `{registered:string[];opens:PaneOpenArgs[];processCalls:unknown[];messages:string[];advance(ms:number):Promise<void>;state():Promise<MulixWorkbenchState>;setIdentity(p:Partial<MulixWorkbenchIdentity>):void;setCommands(v:CommandInfo[]):void;setPlaced(v:UiOpenResult):void;setUsage(v:SessionUsage):void;deferProcess():{entered:Promise<void>;finish(r:ProcessRunResult):void}}`. Initial identity is native-1/startedAt=1/root C:/repo/version 2.1.301; usage is startedAt=1/context.window=1000/rateLimits=[]/cost.usd=0. Mock clock starts at 1000; advance delegates to its real `advance(ms)`. Identity changes update matching usage.startedAt; default process output is the valid T3 c1/tasks fixture.

Use real `claude-code/testing` mock.clock/mock.session and native default state. Bottom test hooks: session.start → `{cwd:e.cwd}` (also capture hook's EngineInterface for read-only state getter); turn.start → `{turnId:e.turnId}`; turn.complete → `{text:e.answer,usage:e.usage}`; session.end → `{sessionId:e.sessionId}`. Op hooks MUST return `{value:...}`: session.id/root/usage, command.list/register, fs.stat (`kind:'dir'`), ui.open (`isPlaced:true`), ui.close/log (`undefined`), ui.panes (`[]`), process.run. `session.version` is explicitly `{value:{version:identity.version}}`, not a string. command.register updates the fixture registry with source plugin/name mulix-workbench; deferred process returns a held promise. Capture only non-debug `ui.log` calls' `e.text` in messages so a debug-only entry cannot satisfy user-visible failure feedback; do not invent a tool_use_id to call ui.notice from a slash command. The state getter uses captured CoreEngineInterface.state.get with the literal owned reference, falling back to T2 `freshState()` only when value is undefined; test Engine.state does not exist.

All unlisted effectful calls install throwing test hooks: fs.write/store/http/model/tool/prompt/config/agent/session append/send/compact. A throw in a test hook must fail the test, not silently become feature GREEN. A foreign command fallback is explicitly registered only in its collision test.

- [X] **Step 2: Write adapter behavior tests**

Each required datum is a native `Text` row with a stable key (spacing/number grouping may vary, not its source or value). Usage keys: `usage-provider`, `usage-session`, `usage-context`, `usage-window`, `usage-valid-turns`, `usage-input`, `usage-write`, `usage-read`, `usage-output`, `usage-hit`, `usage-cost`. Workflow keys: `workflow-source`, `workflow-root`, `workflow-change`, `workflow-phase`, `workflow-design`, `workflow-verify`, `workflow-observed-at`, `workflow-stale`. Times use UTC ISO text for deterministic source/window disclosure. The cost row is the approved unavailable message, never a monetary number. These are view/test contracts, not new effects.

```tsx
import { test, expect } from 'claude-code/testing';
import type { MountTarget, Mounted } from 'claude-code/testing';
import { installHost } from './host-fixture';
const command=(name:string)=>({command:name,args:'',origin:{kind:'composer' as const},presentation:{isFullscreen:false,columns:100}});
const pane=(width:number):MountTarget<'terminal','Pane'>=>({plugin:'mulix-workbench',surface:'terminal' as const,component:'Pane' as const,requestId:'mulix-workbench',props:{title:'mulix',isFocused:true,bodyColumns:width,placement:'inline' as const,scroll:{offset:0,bodyRows:20},view:{}},viewport:{columns:width,rows:30,isFullscreen:false}});
const row=async(ui:Mounted<'terminal','Pane'>,key:string)=>{const found=await ui.find({key});expect(found).toBeDefined();return found?.text ?? '';};
test('user commands show only the native main-session view', async ($,on)=>{
  const h=installHost(on);
  h.setUsage({startedAt:1,context:{tokens:512,window:4096,percent:12},rateLimits:[],cost:{usd:12.5}});
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  expect(h.registered).toEqual(['mulix-status','mulix-cache']);
  expect(h.opens.length).toBe(0); expect(h.processCalls.length).toBe(0);
  const result=await $.command.run(command('mulix-cache'));
  expect(result.text).toBe(undefined); expect(result.context).toBe(undefined);
  for (const width of [60,120]) {
    const ui=await $.ui.mount(pane(width));
    expect(await row(ui,'usage-cost')).toMatch(/费用不可得/);
    expect(await row(ui,'usage-provider')).toMatch(/Claude Code.*原生宿主/);
    expect(await row(ui,'usage-session')).toMatch(/原生主会话.*native-1/);
    expect(await row(ui,'usage-context')).toMatch(/上下文.*512.*4,?096.*12\s*%/);
    expect(await row(ui,'usage-window')).toMatch(/观察窗口/);
    for(const key of ['usage-input','usage-write','usage-read','usage-output']) expect(await row(ui,key)).toMatch(/尚未观察|未提供|不可得/);
    expect(await row(ui,'usage-hit')).toMatch(/不可计算|尚未观察/);
    await ui.unmount();
  }
  await h.advance(1); await $.turn.start({text:'fixture',turnId:'t1'});
  await h.advance(1); await $.turn.complete({turnId:'t1',answer:'',durationMs:1,isAborted:false,reason:'answer',usage:{model:'fixture-model',input_tokens:90,cache_creation_input_tokens:0,cache_read_input_tokens:10,output_tokens:5}});
  await h.advance(1); await $.turn.start({text:'fixture',turnId:'t2'});
  await h.advance(1); await $.turn.complete({turnId:'t2',answer:'',durationMs:1,isAborted:false,reason:'answer',usage:{model:'fixture-model',input_tokens:0,cache_creation_input_tokens:0,cache_read_input_tokens:900,output_tokens:20}});
  const usage=await $.ui.mount(pane(100));
  expect(await row(usage,'usage-input')).toMatch(/未缓存输入.*\b90\b/);
  expect(await row(usage,'usage-write')).toMatch(/缓存写入.*\b0\b/);
  expect(await row(usage,'usage-read')).toMatch(/缓存读取.*\b910\b/);
  expect(await row(usage,'usage-output')).toMatch(/输出.*\b25\b/);
  expect(await row(usage,'usage-hit')).toMatch(/命中率.*91(?:\.0+)?\s*%/);
  expect(await row(usage,'usage-valid-turns')).toMatch(/有效用量回合.*\b2\b/);
  const observed=(await h.state()).observation;
  expect(await row(usage,'usage-window')).toContain(new Date(observed.since!).toISOString());
  expect(await row(usage,'usage-window')).toContain(new Date(observed.until!).toISOString());
  h.setUsage({startedAt:1,context:{tokens:768,window:4096,percent:18},rateLimits:[],cost:{usd:12.5}});
  await usage.press({key:'refresh'});
  expect(await row(usage,'usage-context')).toMatch(/上下文.*768.*4,?096.*18\s*%/);
  expect(await row(usage,'usage-hit')).toMatch(/91(?:\.0+)?\s*%/);
  expect(await row(usage,'usage-read')).toMatch(/\b910\b/);
  expect(await row(usage,'usage-cost')).not.toMatch(/12\.5|\$|USD/);
  expect(h.processCalls.length).toBe(0);
  await usage.unmount();
  const before=await h.state();
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  expect((await h.state()).observation.counts).toEqual(before.observation.counts);
  await $.session.end({reason:'clear',sessionId:'native-1',resume:{id:'native-1'}});
  h.setIdentity({sessionId:'native-2',startedAt:2});
  await $.turn.complete({turnId:'t1',answer:'',durationMs:1,isAborted:false,reason:'answer'});
  expect((await h.state()).observation.validTurns).toBe(0);
  expect((await h.state()).identity).toBe(null);
  await $.turn.start({text:'new main',turnId:'new-main'});
  await $.turn.start({text:'child',turnId:'child-turn',agentId:'child-1'});
  await $.turn.complete({turnId:'child-turn',agentId:'child-1',answer:'',durationMs:1,isAborted:false,reason:'answer',usage:{model:'fixture-model',input_tokens:999,cache_creation_input_tokens:0,cache_read_input_tokens:0,output_tokens:1}});
  expect((await h.state()).observation.validTurns).toBe(0);
  expect((await h.state()).identity?.sessionId).toBe('native-2');
  expect((await h.state()).observation.pendingTurnId).toBe('new-main');
});
test('close fences late query and repeated refresh', async ($,on)=>{
  const h=installHost(on);
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  const held=h.deferProcess(); const opening=$.command.run(command('mulix-status'));
  await held.entered;
  const ui=await $.ui.mount(pane(100));
  await ui.press({key:'refresh'});
  expect(h.processCalls.length).toBe(1);
  await ui.press({key:'close'});
  held.finish({exitCode:0,stdout:'change: late\nphase: build\ndesign: track=architectural approved=true path=x\nverify_result: pass (failures: 0)\n',stderr:'',isStdoutTruncated:false,isStderrTruncated:false});
  await opening;
  expect((await h.state()).ui.open).toBe(false);
  expect((await h.state()).workflow).toBe(null);
  expect(h.opens.length).toBe(1);
  await ui.unmount();
});
```

```tsx
test('foreign names and late takeover pass through', async ($,on)=>{
  const h=installHost(on);
  h.setCommands([{name:'mulix-cache',description:'other',source:'plugin',plugin:'other'}]);
  on('command.run',{command:'mulix-cache'},()=>({text:'foreign-cache'}));
  on('command.run',{command:'mulix-status'},()=>({text:'foreign-status'}));
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  expect(h.registered).toEqual(['mulix-status']);
  expect((await $.command.run(command('mulix-cache'))).text).toBe('foreign-cache');
  h.setCommands([{name:'mulix-status',description:'user-owned',source:'user'}]);
  expect((await $.command.run(command('mulix-status'))).text).toBe('foreign-status');
  expect(h.opens.length).toBe(0);
});
test('programmatic commands, args and wrong versions do not open', async ($,on)=>{
  const h=installHost(on);
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  await $.command.run({...command('mulix-cache'),origin:{kind:'plugin',name:'other'}});
  await $.command.run({...command('mulix-status'),args:'--force; DO_NOT_EXECUTE'});
  for(const version of ['2.1.300','2.1.302','']) {
    h.setIdentity({version});
    await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
    const beforeMessages=h.messages.length;
    await $.command.run(command('mulix-cache'));
    expect(h.messages.length).toBeGreaterThan(beforeMessages);
    expect(h.messages.slice(beforeMessages).join('\n')).toMatch(/版本|version|CLI|不可用/);
  }
  expect(h.opens.length).toBe(0); expect(h.processCalls.length).toBe(0);
});
test('unplaced commands report unavailable and never query workflow', async ($,on)=>{
  const h=installHost(on); h.setPlaced({isPlaced:false,reason:'no-surface'});
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  const beforeMessages=h.messages.length;
  for(const name of ['mulix-status','mulix-cache']) {
    const result=await $.command.run(command(name));
    expect(result.text).toBe(undefined); expect(result.context).toBe(undefined);
  }
  expect((await h.state()).ui.placed).toBe(false);
  expect(h.messages.length).toBeGreaterThan(beforeMessages);
  expect(h.messages.slice(beforeMessages).join('\n')).toMatch(/不可用|未放置|无法显示|no-surface/);
  expect(h.processCalls.length).toBe(0);
});
test('missing usage is not zero; agent view is not source scope', async ($,on)=>{
  const h=installHost(on);
  on('ui.render',{component:'Pane'},(host,e)=>{const {Text}=host.ui.resolve(e);return <Text>native fallback</Text>;});
  h.setUsage({startedAt:1,context:{window:1000},rateLimits:[]});
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  await $.command.run(command('mulix-cache'));
  const s=await h.state();
  expect(s.nativeUsage?.tokens).toBe(null);
  expect(s.nativeUsage?.cost.reason).toBe('host-cost-unavailable');
  const agentPane=pane(100); agentPane.props.view={agentId:'child-1'};
  const ui=await $.ui.mount(agentPane);
  expect(await row(ui,'usage-session')).toMatch(/原生主会话.*native-1/);
  expect(await row(ui,'usage-context')).toMatch(/未提供|不可得/);
  expect(await row(ui,'usage-cost')).toMatch(/费用不可得.*宿主未提供费用/);
  await ui.unmount();
  const desktop=await $.ui.mount({...pane(100),surface:'desktop' as const});
  expect(await desktop.find({type:'Text',text:'native fallback'})).toBeDefined();
  await desktop.unmount();
});
test('root change drops old data; status rendering follows fresh and stale data', async ($,on)=>{
  const h=installHost(on);
  await $.session.start({cwd:'C:/repo',surface:'terminal',isInteractive:true});
  await $.command.run(command('mulix-status'));
  const ui=await $.ui.mount(pane(100));
  expect(await row(ui,'workflow-source')).toMatch(/mulix.*工作流.*CLI/);
  expect(await row(ui,'workflow-root')).toContain('C:/repo');
  expect(await row(ui,'workflow-change')).toContain('c1');
  expect(await row(ui,'workflow-phase')).toContain('tasks');
  expect(await row(ui,'workflow-design')).toMatch(/设计.*true/);
  expect(await row(ui,'workflow-verify')).toMatch(/验证.*pending/);
  expect(await row(ui,'workflow-stale')).toMatch(/当前/);
  const before=(await h.state()).workflow;
  expect(await row(ui,'workflow-observed-at')).toContain(new Date(before!.observedAt).toISOString());
  await h.advance(1);
  const failed=h.deferProcess(); const second=$.command.run(command('mulix-status'));
  await failed.entered;
  failed.finish({exitCode:1,stdout:'',stderr:'SECRET',isStdoutTruncated:false,isStderrTruncated:false}); await second;
  const stale=(await h.state()).workflow;
  expect(stale?.stale).toBe(true); expect(stale?.fields).toEqual(before?.fields);
  expect(stale?.observedAt).toBe(before?.observedAt);
  expect(await row(ui,'workflow-stale')).toMatch(/过期/);
  expect(await row(ui,'workflow-observed-at')).toContain(new Date(before!.observedAt).toISOString());
  for(const next of [{change:'c2',phase:'verify',approved:true,verify:'fail'},{change:'c3',phase:'design',approved:false,verify:'pending'}]) {
    await h.advance(1);
    const update=h.deferProcess(); const reading=$.command.run(command('mulix-status')); await update.entered;
    update.finish({exitCode:0,stdout:`change: ${next.change}\nphase: ${next.phase}\ndesign: track=architectural approved=${next.approved} path=x\nverify_result: ${next.verify} (failures: ${next.verify==='fail'?1:0})\n`,stderr:'',isStdoutTruncated:false,isStderrTruncated:false}); await reading;
    expect(await row(ui,'workflow-change')).toContain(next.change);
    expect(await row(ui,'workflow-phase')).toContain(next.phase);
    expect(await row(ui,'workflow-design')).toContain(String(next.approved));
    expect(await row(ui,'workflow-verify')).toContain(next.verify);
    expect(await row(ui,'workflow-stale')).toMatch(/当前/);
  }
  const moved=h.deferProcess(); const third=$.command.run(command('mulix-status'));
  await moved.entered; h.setIdentity({root:'C:/other'});
  moved.finish({exitCode:0,stdout:'change: old\nphase: build\ndesign: track=architectural approved=true path=x\nverify_result: pass (failures: 0)\n',stderr:'',isStdoutTruncated:false,isStderrTruncated:false}); await third;
  const s=await h.state(); expect(s.identity?.root).toBe('C:/other');
  expect(s.workflow?.fields ?? null).toBe(null); expect(s.ui.busy).toBe(false);
  expect(await row(ui,'workflow-root')).toContain('C:/other');
  expect(await row(ui,'workflow-change')).toMatch(/不可得|未提供|未知/);
  expect(await row(ui,'workflow-change')).not.toMatch(/c1|c2|c3|old/);
  expect(JSON.stringify(s).includes('SECRET')).toBe(false);
  expect(h.messages.join('\n').includes('SECRET')).toBe(false);
  await ui.unmount();
});
```

Type `pane(width)` as a `MountTarget<'terminal','Pane'>` so its view accepts a SiteView agentId (import the types from the target declarations); it is not the TypeScript empty-object type. All test hooks register **before the first call on `$`**; mutable fixture setters are used afterward. Keep fixtures typed, never use `as any` to hide a wrong signature. Native close-mark and actual worker hot-reload behavior remain true-UI evidence, not claims from pressing the plugin's own close button or repeating session.start.

- [X] **Step 3: Verify meaningful RED**

Run `NATIVE`. Expected: the registration assertion (empty list versus two commands) fails with the no-op module; earlier T2/T3 tests pass. Bad mock shape/missing bottom hook is a test setup defect, not accepted RED.

- [X] **Step 4: Implement adapter and pure drawing**

Use literal typed atom ref `{plugin:'mulix-workbench',key:'workbench'}`. Register only free/own names; before every command handling re-check current registry owner against the actual plugin identity. If foreign/unknown, return next unchanged, so skipping registration does not still hijack its command. Non-composer/programmatic source never opens. Own commands with nonempty trimmed args only report unsupported arguments through the UI, without opening/querying; arguments never reach the process. Valid own commands open once, set selected view, query only on explicit actions and return `{}` even for own UI errors. If open returns isPlaced=false, mark unavailable, emit a user-visible non-debug ui.log and stop before querying workflow; no phantom tool_use_id/ui.notice. Unsupported/unknown versions similarly return a visible unavailable log on the user's command, without opening or querying.

Bind native identity/version via id/root/version/usage, keep per-event epoch before awaits, consume only main turn events, return lifecycle next results unchanged. No workflow process on start/end/turn-complete. Single module I/O slot + host generation/root/session recheck prevent duplicate refresh, close/root/session-late updates; timeout is the native 2000ms contract, not invented abort support. Same native initialization retains observations; clear/resume retire without assuming a later start. ui.close hooks must handle the native close path as well as own button; no auto-open after result/reload. Render atom read-only, bind every keyed data row to its specified actual fields/values and ISO observation times (not label-only placeholders), display an explicit stale marker and no previous-root data after a root change, preserve normal close controls/focus and no holdToasts. Invalidation must also leave UI busy false without allowing an old request to clear a newer request's busy state; keep module I/O-slot ownership separate from whether its result may still apply.

- [X] **Step 5: Verify GREEN, readonly effects and canonical copy**

Run `NATIVE`, `TYPECHECK`, `VALIDATE`, `GOALL`. Expected: all pass with no unexpected bottom operations or refused/skipped test hooks. UI kit verifies element descriptions, not terminal paint or true worker hot reload; those claims remain for T5. Sync only owned source/test files with guarded writes.

- [ ] **Step 6: Local commit checkpoint**

After authorization/format gate, add only canonical adapter/view/fixture/tests plus own plan; commit `chore(mods): wire native readonly workbench panes`.

## Task 5: Reproducible delivery, instructions and honest integration evidence

**Files:** Create `scripts/check-mod-delivery.cjs`, `scripts/check-mod-delivery.test.cjs`, LIVE/REPO `README.md`, `docs/evidence/claude-2-1-301-mods-workbench.md`; update own plan only.
**Interfaces:** Consumes all earlier tasks. Produces host-only `checkDelivery(repoDir:string, liveDir:string):{ok:boolean,problems:string[]}` and exact exported `OWNED_FILES` list below. CLI `node scripts/check-mod-delivery.cjs "$REPO" "$LIVE"` exits 0 iff owned ordinary files exist and bytes match; it never writes/synchronizes or executes either tree, and problems name paths not contents.

Owned whitelist: `.claude-plugin/plugin.json`, `hooks/hooks.json`, `hooks/register.tsx`, `hooks/model.ts`, `hooks/workflow.ts`, `hooks/view.tsx`, `types/index.d.ts`, `tests/model.test.ts`, `tests/workflow.test.ts`, `tests/host-fixture.ts`, `tests/register.test.tsx`, `README.md`. Exclude generated `.claude-plugin/types/**` and generated root tsconfig from comparison without reading them; runtime/tooling/logs, settings, tokens and marketplace assets never become delivery inputs. Reject unexpected entries in either dedicated mod tree (other than those two documented generated locations), and reject a symlink at either root or any owned path component before reading bytes. Do not traverse unknown/link directories or print file contents.

- [X] **Step 1: Write the host-only delivery checker test**

```js
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'); const os=require('node:os'); const path=require('node:path');
const {checkDelivery,OWNED_FILES}=require('./check-mod-delivery.cjs');
const expected=['.claude-plugin/plugin.json','hooks/hooks.json','hooks/register.tsx','hooks/model.ts','hooks/workflow.ts','hooks/view.tsx','types/index.d.ts','tests/model.test.ts','tests/workflow.test.ts','tests/host-fixture.ts','tests/register.test.tsx','README.md'];
test('only ordinary owned bytes are delivery inputs',()=>{
  assert.deepEqual(OWNED_FILES,expected);
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'mulix-delivery-'));
  try {
    const repo=path.join(dir,'repo'),live=path.join(dir,'live');
    for(const root of [repo,live]) for(const f of expected){const p=path.join(root,f);fs.mkdirSync(path.dirname(p),{recursive:true});fs.writeFileSync(p,'owned');}
    assert.equal(checkDelivery(repo,live).ok,true);
    fs.writeFileSync(path.join(live,'hooks/model.ts'),'PRIVATE_DIFFERENT_BYTES');
    const mismatch=checkDelivery(repo,live); assert.equal(mismatch.ok,false);
    assert.equal(JSON.stringify(mismatch).includes('PRIVATE_DIFFERENT_BYTES'),false);
    fs.writeFileSync(path.join(live,'hooks/model.ts'),'owned');
    const generated=path.join(live,'.claude-plugin/types/claude-code/index.d.ts');
    fs.mkdirSync(path.dirname(generated),{recursive:true});fs.writeFileSync(generated,'DO_NOT_COPY');
    assert.equal(checkDelivery(repo,live).ok,true);
    const extra=path.join(live,'hooks/extra.ts'); fs.writeFileSync(extra,'DO_NOT_EXECUTE');
    assert.equal(checkDelivery(repo,live).ok,false); fs.unlinkSync(extra);
    fs.unlinkSync(path.join(live,'README.md'));
    assert.equal(checkDelivery(repo,live).ok,false);
    fs.symlinkSync(path.join(repo,'README.md'),path.join(live,'README.md'),'file');
    assert.equal(checkDelivery(repo,live).ok,false);
  } finally {fs.rmSync(dir,{recursive:true,force:true});}
});
```

```js
test('linked owned parent is rejected without following it',()=>{
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'mulix-delivery-parent-'));
  try {
    const repo=path.join(dir,'repo'),live=path.join(dir,'live');
    for(const root of [repo,live]) for(const f of expected){const p=path.join(root,f);fs.mkdirSync(path.dirname(p),{recursive:true});fs.writeFileSync(p,'owned');}
    const saved=path.join(dir,'saved-hooks'); fs.renameSync(path.join(live,'hooks'),saved);
    fs.symlinkSync(saved,path.join(live,'hooks'),process.platform==='win32'?'junction':'dir');
    assert.equal(checkDelivery(repo,live).ok,false);
  } finally {fs.rmSync(dir,{recursive:true,force:true});}
});
```

Both link fixtures use only test-owned targets. If the platform cannot create them, record an environmental block rather than claiming those cases passed. Do not inspect linked contents or clean a directory not created by these tests.

- [X] **Step 2: Verify RED**

Run `node --test scripts/check-mod-delivery.test.cjs`.
Expected: nonzero missing check-mod-delivery.cjs module (document compile/load RED), not missing Node or filesystem permission.

- [X] **Step 3: Implement checker and local-use documentation**

Implement fixed whitelist, ordinary-file/no-symlink checks, safe hash comparison and missing/mismatch paths; Node builtins only, no recursive copying. README states target 2.1.301, A scope, no product ledger/Governance authority, fee unavailability, commands, manual local `claude --plugin-dir ./mods/mulix-workbench`, no auto install/config changes, unplaced/headless limitations, and exact type/native test commands. Capture new generated type locations as runtime inputs, not committed declarations.

- [X] **Step 4: Verify GREEN and full build gates**

Run Node test, delivery checker, `NATIVE`, `TYPECHECK`, `VALIDATE` for LIVE and `"$CLI" plugin validate --strict "$REPO"`, `go build ./...`, `GOALL`, `go vet ./...`, raw `gofmt -l .`.
Expected: all code/unit/type/format gates actually pass; any original failure/nonempty formatting output remains visible and blocking as required by project policy, not silently relabeled. Do not use another change's green logs or rewrite unrelated CRLF/NO_COLOR sources.

- [X] **Step 5: Record real smoke or explicit blocked evidence**

Load `run` skill before real app driving. Evidence records date, HEAD/workspace, exact native/installed versions, source/type hashes, command timeline, exit codes, UI scope and hot-load answer. Separately cover (a) mulix product-entry version/approval/stream lifecycle, not just `claude --version`, and (b) a real 2.1.301 interactive host opening both views, manual refresh/close, no prompt injection, /clear and actual hot reload/dedup, stop/unload without changing Go behavior. Native close-mark and real paint are not claimed from UI kit.

Current authoring session may still run the older engine despite PATH 2.1.301; determine `session.version` in the tested host. A new native host/manual session or any new paid model request requires the corresponding actual authorization; no implicit nested-session/environment bypass. If hot loading is not enabled, native host unavailable, paid smoke not authorized or any gate is blocked, write the exact limitation, not a fake pass. Artifact creation may be GREEN, but whole-change verify_result must not be pass until its required real scenarios and gates are satisfied or the user explicitly amends acceptance.

- [ ] **Step 6: Local commit checkpoint and final handoff**

After authorization and genuine formatting/test gates, add only checker/test, canonical README, own evidence/plan; commit `chore(mods): document and verify readonly workbench delivery`. Do not mark build complete, verify, archive or push merely because this plan exists or a smoke is blocked.

All-task build exit follows the build skill: dispatch an independent final review, fix only its Critical/Important findings via one TDD round, route Minor to ledger, then seek normal verification/archival gates. This is a subsequent orchestration gate, not a second action hidden in the commit step.

## Plan decisions, coverage and approvals

- DR-1: isolate pure workflow query in workflow.ts — reason: focused independently testable component; wrong cost: unclear I/O ownership. No behavioral architecture change; native adapter still owns host effects.
- DR-2: re-check command ownership on each call, not only registration — reason: handlers match names even when registration was skipped or later replaced; wrong cost: foreign command hijack. T4 covers both collision timings.
- DR-3: target-version amendment is an explicit user ruling, not inferred from installed version. T1 reconciles main spec only once build paths permit; all tasks consume the normative amendment now.
- FR-001/002→T1; FR-003–007→T3/T4/T5; FR-008–011→T2/T4; FR-012→T2/T3/T4; FR-013–016→T1/T4/T5 and unchanged host regressions.
- All checkboxes are unexecuted. Header is Draft until independent plan review closes its real Issues and the human approves tasks. Approval of design does not set execution_method or authorize commits by itself; separate tooling permission is recorded below and only applies after the build gate.

### 用户工具授权（2026-10-16）

- Q: 当前未找到 TypeScript 编译器，是否授权在任务计划获批、进入 build 后隔离安装仅用于开发验证的编译器？ → A: 用户选择“授权隔离安装（推荐）”。选项明确限定 TypeScript >=5.4、固定版本、禁安装脚本、不改全局环境/产品依赖，下载被策略拦截仍停止，不批准任务或实现。
- **Ruling — 决定**: 未来获批 build 可按上述 preflight 隔离安装；当前没有安装。固定 5.4.5 是编排者在已授权范围内的工程选择，不说成用户另行指定的版本，也不声称下载/类型检查已通过。
- **理由**: 精确目标声明要求至少 TypeScript 5.4，需要固定、可复查且不污染产品/全局环境的开发工具。
- **错了代价**: 若现在执行安装或把此选择当任务/执行方式批准，会越过阶段门禁；若改用 floating latest、全局安装或绕过网络拒绝，会破坏隔离与可重复验证。

### 独立计划评审闭环

初审仅有 1 个 Issue：T4 的原生面板显示数据未被断言，只有两个固定文字即可通过。修订补齐了字段实值、两套统计窗口、动态刷新/过期、来源与不可用用户反馈的 UI 断言；4 项 advisory 也已应用（fixture 形状/空状态、仓库脚本路径、错误版本提示、跨模型标签）。同一独立评审员已完成聚焦复核，明确确认 Issue 1 closed、无新增阻塞项，返回 **Status: Approved**。复核记录见同目录 `plan-review.md`。该独立结论只批准计划质量，不是编译/测试通过或实现完成；后续真实用户批准另记如下。

### 用户任务批准与执行工作区裁定（2026-10-16）

- **用户 /goal 原文**: `批准任务并允许本地提交，不用开分支，在主分支上执行`。用户同时要求立即推进，不停下来询问要做什么。
- **Ruling — 决定**: 任务计划获明确批准，本地提交获授权意图；执行工作区按最新要求使用现有 master，不创建分支或 worktree。常规任务提交动作因执行者更高优先级的默认分支约束暂缓，不实际提交 master、不绕过、不伪造提交证据；实现/验证继续推进，提交步骤不勾选。
- **理由**: 新指令明确替代此前分支建议；用户允许提交不使执行者获得违反更高优先级规则的权限。用户要求不再停问时采用 inline 作为编排者默认执行方式，不伪称用户逐字选择了 inline。
- **错了代价**: 若新建分支会违背用户工作区裁定；若直接提交 master 或委托他方绕过，会违反执行约束；若把待提交说成已提交或把本目标解释为推送/归档授权，会造成错误交付记录。
- **边界**: 通过正常 tasks-complete 转 build 并先加载 build 技能；保持 TDD、权限与阶段守卫。若正常 task-done/阶段守卫必须有真实提交而无法满足，记录实际阻塞，不用 --force 或假哈希收口。安装权限限此前已批准的隔离开发工具，不包含推送、归档、自动热加载或新增付费模型试验。
