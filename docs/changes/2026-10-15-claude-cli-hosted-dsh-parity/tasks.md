# Claude CLI 托管 dsh 等价总纲持久化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps start as `- [ ] **Step N: ...**`; mark only actually completed steps `- [X] **Step N: ...**`.

**Goal:** 发布可长期引用的总纲路线、16 项能力初始追踪索引和固定基线证据，不实现任何产品子能力。

**Architecture:** 将已批准设计中的拆分、依赖、审批和验收规则发布到持久文档，以 JSON 初始索引机械核对覆盖关系。总纲文档交付与产品等价状态分开：索引初始全部未验证，B0 等实际子 change 仍须独立启动和审批。

**Tech Stack:** Markdown、JSON、PowerShell 验收脚本；产品源码与依赖不变。

**Spec:** `docs/specs/2026-10-15-claude-cli-hosted-dsh-parity/spec.md`

**Design:** `.mulix/.runtime/2026-10-15-claude-cli-hosted-dsh-parity/specs/2026-10-15-hosted-dsh-parity-roadmap-design.md`（已获用户明确批准；批准记录日期 2026-10-16）

**Plan status:** Approved — 用户原文“批准这份任务计划”（记录日期：2026-10-16）；仅批准本总纲的文档/追踪交付，以下步骤均未执行，执行方式尚未选择。

**Independent plan review:** general-purpose 独立评审结论 `Approved`，无阻塞 Issue；已核对真实输入、路径、31 FR/16 方向/18 逻辑项及 PowerShell 语法与六个内存反例。这是计划评审证据，不是实际实施 RED/GREEN、产品回归或功能验收。

**Approval ruling:** 决定：当前任务计划获批，可以进入 build，但须先由用户明确选择执行方式；理由：真实用户已在收到独立评审结果后明确批准；错了代价：漏记会丢失阶段授权，扩大授权则会跳过执行方式及子 change 的独立裁定。

## Global Constraints

- 保持“Claude CLI 作为主驱动，mulix 在外围负责镜像与治理”；完整等价目标不缩水，多个独立 change 逐项交付。
- 固定 dsh `571902028a75c64c1d767f4ae5fd099485f0ddf0` / `0.2.1-alpha.3`，配对 mulix `3b309100fdf7ac5170865c334b640b003e63749d`，初始引擎锁 `2.1.296`；这是历史基线，不随其他 change 的升级改写。
- 本计划只交付总纲文档和追踪数据；不修改 `internal/`、`cmd/`、模块文件、`engine.lock`、mods、既有流程资产或参考 dsh 仓库，不新增依赖，不运行供应商请求。
- 先持久化后发布、镜像单写者、1:1 与 fail-closed 为后续子 change 的跨项约束，当前文档测试不证明这些产品行为已验收。
- 提供方、验证状态、逻辑交付项与真实 change 的生命周期分开；模拟测试、环境阻塞、源码存在和文档归档不得计为产品真实通过。
- 不将子 change 的 spec/design/tasks/归档批准从总纲批准或跨会话消息中推导；不自动创建子 change 或 worktree。
- 总纲 build/verify 只验收本文档交付；产品总目标须保持未验证，直到全部参考契约有对应证据。本文档不宣称 SC-002 已完成。
- 写入前 `mulix state show`，仅在本总纲为 active 且处于合法阶段时写本总纲产物；共享 active 须先交接，不抢切、不 `--force`、不借其他阶段白名单绕过。
- 本任务保留可复现的文档契约 RED/GREEN；如实标为文档验收，不冒充引擎功能 TDD。仍执行项目要求的 Go 回归、格式检查及 build 后独立终审。
- `.mulix/.runtime/` 只留本地运行态与日志，不强制入库；只暂存本计划明确列出的本 change 文件，不夹带工作区已有改动。

## Review Focus

1. 把总纲完成误报为产品等价：初始索引必须全部 `unverified`，无真实子 change、契约或运行证据；Step 1/4 覆盖伪造 passed 的反例。
2. 16 个方向或 FR-001..FR-031 被遗漏/重复冒名：Step 1/4 覆盖重复 capability 和缺失 FR-031。
3. 版本锁升级改写旧基线：Step 1/4 覆盖将历史引擎锁改成 2.1.300；新版本证据仅允许追加到后续交付记录。
4. 引用不存在的逻辑交付项或依赖循环：Step 1/4 覆盖 B18 引用和 B0/B1 循环。
5. 仅有被忽略设计稿、没有持久证据与交接规则：Step 1/4 检查三个持久产物及路线链接，Step 3 明确批准与来源强度，Step 5 人工核对证据措辞。

---

## Task 1: 发布可追踪的总纲路线与初始索引

**Files:**
- Create: `docs/changes/2026-10-15-claude-cli-hosted-dsh-parity/roadmap.md` — 持久化已批准拆分、依赖、批准边界和维护规则。
- Create: `docs/changes/2026-10-15-claude-cli-hosted-dsh-parity/capabilities.json` — 16 方向、31 FR 和 B0..B17 的初始规划索引，不是产品运行时服务。
- Create: `docs/evidence/dsh-parity-reference.md` — 固定基线、只读来源、已核验事实与未执行验证。
- Test（本地运行态）: `.mulix/.runtime/2026-10-15-claude-cli-hosted-dsh-parity/sdd/tasks/test-parity-initial.ps1`。
- Include in this change's commit: 已批准的 `docs/specs/2026-10-15-claude-cli-hosted-dsh-parity/spec.md` 和本 `tasks.md`，先确认均为本会话产物、没有他方未审改动。

**Interfaces:**
- Consumes: 已批准总纲 spec、已批准设计 §§1–10、31 个 FR、16 个方向与 B0..B17 逻辑项；无 Go 接口新增。
- Produces: `roadmap.md` 作为子 change 的持久入口，链接同目录 `capabilities.json`、`../../evidence/dsh-parity-reference.md` 和 `../../specs/2026-10-15-claude-cli-hosted-dsh-parity/spec.md`。
- Produces: JSON 根字段 `schema: "mulix.parity-roadmap.v1"`、`scope: "umbrella-tracking-only"`、`overall_status: "unverified"`、`baseline`、`cross_cutting_requirements: string[]`、`deliveries: Delivery[]`、`capabilities: Capability[]`。
- `baseline` 字段为 `dsh_commit`、`dsh_version`、`mulix_commit`、`engine_lock`，取 Global Constraints 的固定值。
- `Delivery` 字段为 `id: string`、`title: string`、`depends_on: string[]`、`conditional_dependencies: string[]`；ID 仅 B0..B17，均为逻辑项，不是实际 change ID。
- `Capability` 字段为 `id: string`、`requirements: string[]`、`deliveries: string[]`、`planned_provider: "host" | "engine" | "joint"`、`verification: "unverified"`、`child_changes: []`、`reference_contracts: []`、`evidence: []`。`planned_provider` 是设计责任建议，不是已验证提供方。

- [X] **Step 1: 写出针对初始文档交付的失败验收测试**

用 Write 将以下完整脚本保存到本任务的 Test 路径。脚本仅验收本次初始快照；后续子 change 更新状态时须维护其自己的验收，不能把所有能力永久锁成 unverified。此时不写三个交付产物。

```powershell
param([string]$RepoRoot = '.')
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$change = '2026-10-15-claude-cli-hosted-dsh-parity'
function Check([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function SameSet([object[]]$Actual, [object[]]$Expected, [string]$Message) {
    $a = @($Actual | Sort-Object -Unique)
    $e = @($Expected | Sort-Object -Unique)
    Check ($a.Count -eq $e.Count) $Message
    Check (@(Compare-Object $a $e).Count -eq 0) $Message
}
function Validate($r) {
    Check ($r.schema -eq 'mulix.parity-roadmap.v1') 'schema mismatch'
    Check ($r.scope -eq 'umbrella-tracking-only') 'scope mismatch'
    Check ($r.overall_status -eq 'unverified') 'unverified only'
    Check ($r.baseline.dsh_commit -eq '571902028a75c64c1d767f4ae5fd099485f0ddf0' -and
        $r.baseline.dsh_version -eq '0.2.1-alpha.3' -and
        $r.baseline.mulix_commit -eq '3b309100fdf7ac5170865c334b640b003e63749d' -and
        $r.baseline.engine_lock -eq '2.1.296') 'baseline mismatch'
    $names = 'loop,providers,plugins,profiles,tools-mcp,skills,compaction,context-authority,fork,subagents,ptc,middleware,sandbox,remote,search,replay'.Split(',')
    Check (@($r.capabilities).Count -eq 16) 'capability count'
    Check (@($r.capabilities.id | Sort-Object -Unique).Count -eq 16) 'duplicate capability'
    SameSet @($r.capabilities.id) $names 'capability coverage'
    $deliveryIds = @(0..17 | ForEach-Object { "B$_" })
    Check (@($r.deliveries).Count -eq 18) 'delivery count'
    Check (@($r.deliveries.id | Sort-Object -Unique).Count -eq 18) 'duplicate delivery'
    SameSet @($r.deliveries.id) $deliveryIds 'delivery coverage'
    $map = @{}
    foreach ($d in $r.deliveries) { $map[$d.id] = $d }
    foreach ($d in $r.deliveries) {
        Check (-not [string]::IsNullOrWhiteSpace($d.title)) 'missing delivery title'
        foreach ($id in $d.depends_on) { Check ($map.ContainsKey($id)) 'unknown dependency' }
        foreach ($id in $d.conditional_dependencies) { Check ($map.ContainsKey($id)) 'unknown dependency' }
    }
    $done = @{}
    $visiting = @{}
    function Visit([string]$Id) {
        Check (-not $visiting.ContainsKey($Id)) 'cyclic dependency'
        if ($done.ContainsKey($Id)) { return }
        $visiting[$Id] = $true
        foreach ($dep in $map[$Id].depends_on) { Visit $dep }
        $visiting.Remove($Id)
        $done[$Id] = $true
    }
    foreach ($id in $deliveryIds) { Visit $id }
    $requirements = @($r.cross_cutting_requirements)
    foreach ($c in $r.capabilities) {
        Check ($c.verification -eq 'unverified') 'unverified only'
        Check ($c.planned_provider -in @('host', 'engine', 'joint')) 'invalid planned provider'
        Check (@($c.requirements).Count -gt 0) 'missing capability requirements'
        Check (@($c.deliveries).Count -gt 0) 'missing capability delivery'
        Check (@($c.child_changes).Count -eq 0 -and @($c.reference_contracts).Count -eq 0 -and
            @($c.evidence).Count -eq 0) 'initial index cannot claim delivery evidence'
        foreach ($id in $c.deliveries) { Check ($map.ContainsKey($id)) 'unknown delivery' }
        $requirements += @($c.requirements)
    }
    SameSet $requirements @(1..31 | ForEach-Object { 'FR-{0:000}' -f $_ }) 'FR coverage'
}
function Reject($Original, [scriptblock]$Mutate, [string]$Expected) {
    $bad = $Original | ConvertTo-Json -Depth 30 | ConvertFrom-Json
    & $Mutate $bad
    try { Validate $bad }
    catch { Check ($_.Exception.Message -eq $Expected) "wrong rejection: $($_.Exception.Message)"; return }
    throw "accepted invalid record: $Expected"
}
$files = @(
    "docs/changes/$change/roadmap.md",
    "docs/changes/$change/capabilities.json",
    'docs/evidence/dsh-parity-reference.md'
)
foreach ($file in $files) {
    Check (Test-Path (Join-Path $RepoRoot $file) -PathType Leaf) "missing document: $file"
}
$r = Get-Content (Join-Path $RepoRoot $files[1]) -Raw | ConvertFrom-Json
Validate $r
$roadmap = Get-Content (Join-Path $RepoRoot $files[0]) -Raw
foreach ($link in @('capabilities.json', '../../evidence/dsh-parity-reference.md', "../../specs/$change/spec.md")) {
    Check ($roadmap.Contains($link)) "missing roadmap link: $link"
}
$reference = Get-Content (Join-Path $RepoRoot $files[2]) -Raw
foreach ($value in @('571902028a75c64c1d767f4ae5fd099485f0ddf0', '3b309100fdf7ac5170865c334b640b003e63749d', '0.2.1-alpha.3', '2.1.296')) {
    Check ($reference.Contains($value)) "missing reference value: $value"
}
Reject $r { param($b) $b.capabilities[1].id = $b.capabilities[0].id } 'duplicate capability'
Reject $r { param($b) $b.cross_cutting_requirements = @($b.cross_cutting_requirements | Where-Object { $_ -ne 'FR-031' }) } 'FR coverage'
Reject $r { param($b) $b.baseline.engine_lock = '2.1.300' } 'baseline mismatch'
Reject $r { param($b) $b.capabilities[0].deliveries = @('B18') } 'unknown delivery'
Reject $r { param($b) $b.capabilities[0].verification = 'passed' } 'unverified only'
Reject $r { param($b) ($b.deliveries | Where-Object id -eq 'B0').depends_on = @('B1') } 'cyclic dependency'
'PASS: 16 capabilities; 31 FR; 18 logical deliveries; 6 negative checks'
```

- [X] **Step 2: 亲眼运行 RED 并保存真实输出**

Run: `& ".mulix/.runtime/2026-10-15-claude-cli-hosted-dsh-parity/sdd/tasks/test-parity-initial.ps1" -RepoRoot "."`

Expected: 非零退出，包含 `missing document: docs/changes/2026-10-15-claude-cli-hosted-dsh-parity/roadmap.md`。若产物已被另一人创建，先核对来源并调整本任务，不删除他方文件制造 RED。解析错误、权限错误或脚本异常不能冒充预期 RED。

在任务报告/ledger 明确记录：RED 是初始文档契约缺失，不是产品功能失败；输出落本 change 的 `sdd/tasks/`，不伪造运行证据。

- [X] **Step 3: 写出最小的三个持久交付产物**

`roadmap.md` 应持久化批准后的设计 §§1–10 的决定，而不是链接被忽略设计文件作为唯一内容。包含固定基线、16 方向归属、B0..B17 的标题/依赖/条件、完整目标保留、首条功能主线 B1、B12/B11 尽早可行性核验、跨项回归、共享 active 交接及每个子 change 独立审批。解释本文档完成只表示路线与索引交付，不能算 B0 产品查询服务或任一能力已通过。记录总纲 spec/design 的真实用户批准原文与记录日期，不补写任务或归档批准。

`capabilities.json` 按 Interfaces 创建；初始 `overall_status` 与每项 `verification` 均为 `unverified`，三个证据/子 change 数组均为空。跨项需求为 `FR-002, FR-003, FR-004, FR-006, FR-007, FR-028, FR-029, FR-030, FR-031`。能力行使用下表，planned_provider 仅表达计划责任：

| id | requirements | deliveries | planned_provider |
|---|---|---|---|
| loop | FR-001, FR-005 | B0 | engine |
| providers | FR-027 | B12 | joint |
| plugins | FR-013, FR-015 | B4, B5 | host |
| profiles | FR-014 | B6 | host |
| tools-mcp | FR-016, FR-017 | B4, B7 | joint |
| skills | FR-018 | B8 | joint |
| compaction | FR-011 | B2 | joint |
| context-authority | FR-008 | B0, B1 | joint |
| fork | FR-009, FR-010 | B1 | joint |
| subagents | FR-012 | B3 | joint |
| ptc | FR-019 | B9, B10, B11 | joint |
| middleware | FR-020 | B9 | joint |
| sandbox | FR-021, FR-022 | B11 | joint |
| remote | FR-023, FR-024 | B13, B14, B15 | host |
| search | FR-025 | B16 | host |
| replay | FR-026 | B17 | host |

逻辑交付项标题沿用批准设计，硬依赖与条件依赖如下；条件的适用说明必须同时写入 roadmap，不将条件依赖擅自升格为所有场景的硬依赖。

| id | depends_on | conditional_dependencies 与适用条件 |
|---|---|---|
| B0 | 空 | 空 |
| B1 | B0 | 空 |
| B2 | B0 | B1：重启续接时 |
| B3 | B1 | 空 |
| B4 | B0 | B1：重启续接时 |
| B5 | B4 | 空 |
| B6 | B5 | 空 |
| B7 | B4 | 空 |
| B8 | B0, B4 | B6：动态组合集成时 |
| B9 | B4 | 空 |
| B10 | B9 | B11：声明对应 OS 效果约束时 |
| B11 | B0 | 空；B9/B10 集成是后续交叉验收，不是平台基础交付的前置依赖 |
| B12 | B0 | 空 |
| B13 | B0, B1 | B4, B9：扩大工具面时 |
| B14 | B13 | 空 |
| B15 | B13 | 空 |
| B16 | B0 | 空 |
| B17 | B0 | B1：分支回放集成时 |

B11 是按平台独立交付的系列，B15 是按实际参考协议独立交付的系列；记录逻辑项不自动创建任何实际 change。后续实际 change 关联前，核对其真实 ID、批准范围和证据，不从其他会话的范围介绍推断产品验收。

`docs/evidence/dsh-parity-reference.md` 记录固定 SHA、版本、参考本地路径、元数据核验时工作区干净、配对 mulix 提交、历史引擎锁，以及已知代码现状的来源路径。将“源码/元数据核对”“模拟验证”“真实引擎/供应商/平台验证”分开；本总纲未运行后两类验证则明确写未执行。保留原始核验事实，不以当前其他 change 的工作区状态或引擎锁覆盖历史记录；必要时用 `git show <固定SHA>:<路径>` 读取该基线。后续版本只追加记录。

- [X] **Step 4: 验证 GREEN 及六个反例**

Run: `& ".mulix/.runtime/2026-10-15-claude-cli-hosted-dsh-parity/sdd/tasks/test-parity-initial.ps1" -RepoRoot "."`

Expected: 退出 0，末行精确包含 `PASS: 16 capabilities; 31 FR; 18 logical deliveries; 6 negative checks`。所有反例只在内存副本变更，不修改真实索引或删除交付文件。

- [X] **Step 5: 执行完整回归与人工证据核对**

分别运行 `go build ./...`、`go test ./... -count=1`、`go vet ./...`、`gofmt -l .`；长输出落本 change 的运行态日志，逐条核对退出码。Expected: 前三者退出 0，gofmt 无输出；既存无关失败先记录并报告，不改他方文件或声称已通过。

人工核对持久 roadmap 能脱离运行态设计稿阅读、31 FR 均有归属或跨项约束、证据文档没有把源码核对写成实测、没有隐含子 change 批准。检查 `git diff --check`，并核对只暂存下述本 change 文件。独立终审、verify 报告及 archive 仍由阶段技能另行执行，不在此处预先打勾或批准。

- [ ] **Step 6: 提交本 change 文件并落任务证据**

只有本任务 RED/GREEN 与完整回归均达到真实预期后，才执行 task-done/ledger 记录与 commit；按 build 技能要求在同一收口调用完成，记录实际提交 SHA。不得跳过 hooks、绕过签名或强行添加运行态。

Run: `git add docs/specs/2026-10-15-claude-cli-hosted-dsh-parity/spec.md docs/changes/2026-10-15-claude-cli-hosted-dsh-parity/tasks.md docs/changes/2026-10-15-claude-cli-hosted-dsh-parity/roadmap.md docs/changes/2026-10-15-claude-cli-hosted-dsh-parity/capabilities.json docs/evidence/dsh-parity-reference.md`

Run: `git diff --cached --check`

Expected: 退出 0；暂存内容没有其他会话或用户文件。

Run: `git commit -m "docs: publish hosted dsh parity roadmap and initial tracker"`

Expected: 提交成功；真实记录只表示总纲文档交付，不表示产品全量等价、真实冒烟或子 change 已获批准。
