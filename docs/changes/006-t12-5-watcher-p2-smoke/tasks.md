# T12-5 watcher 真实会话复测与 P2 记录修正 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在真实引擎会话(2.1.296,pin 一致)中,以「基线确立之后」的编辑时点复测 watcher 提醒,产出机制定论(H1 时序 / H2 接线缺陷 / H3 轮询未运行,三选一);按结论以追加式修正 `p2-smoke.md`;代码零改动。

**Architecture:**(bounded,设计于 chat 并已批准)证据任务。复测协议:独立 git 工作区 + `mulix run` 无人值守长回合(引擎经 allow 清单的 PowerShell 执行 sleep ~25s 制造存活窗口)→ 轮询镜像 `turn_started` 事件作为**基线锚**(turn_started 由泵在 Submit 发布,Submit 在 start() 完成后——即 watcher 基线装载之后)→ 锚后 +3s 编辑 CLAUDE.md(mtime=t1)→ 收集 stdout/镜像 Notice(t2)→ 时间线定论 → 证据落 `docs/evidence/p3-watcher-retest.md` → p2-smoke.md 追加式修正。

**Tech Stack:** bash 编排;真实 claude CLI 2.1.296;既有镜像查询方式(`mulix sessions`/events.jsonl 直读)。

**Spec:** `docs/specs/006-t12-5-watcher-p2-smoke/spec.md`

**Design:** approved in chat (bounded) —— 要点:①基线锚=镜像 turn_started(晚于 watcher 装载);②编辑锚后 +3s(≥1.5 个轮询周期);③三时刻时间线(init 等价锚/编辑/notice)从镜像与 mtime 如实提取;④三分支定论;⑤修正追加式、原文保留;⑥代码零改动是验收一部分(git diff 只含证据文档)。

## Global Constraints

- FR-101:pin 一致引擎(2.1.296)、独立工作区、无人值守通道,方法对齐 P1/P2 冒烟惯例。
- FR-102:编辑时点锚定基线装载之后;三时刻从镜像事件与文件时间戳如实提取,不得估算。
- FR-103:定论三选一,证据落 `docs/evidence/p3-watcher-retest.md`。
- FR-104:定论为缺陷 → 路由后续,本变更不修复、不冒充。
- FR-105:p2-smoke.md 修正追加式(原文保留),范围限冒烟 C watcher 段 + 诚实限制 #3 及直接相关条目。
- FR-106:不改 watcher/控制器代码;单测矩阵不新增。
- FR-107:全量门禁绿;`git status` 中除证据文档与 p2-smoke.md 外无代码文件变更。

## Review Focus

- 基线锚有效性:turn_started 时刻必须晚于 watcher 装载(代码事实:start 尾装载 → Submit 后发布)——若实测 turn_started 早于编辑但 notice 仍未出现且回合存活 ≥2 周期,才允许升级为缺陷定论。
- 时间线诚实性:三时刻全部来自镜像事件时间戳/文件 mtime,禁止「估算」;证据文档必须给出原始摘录。
- 追加式修正:p2-smoke.md 历史结论不被改写(git diff 应只见新增行/批注,原段落原样)。
- 定论纪律:证据不足时宁写「机制未定论 + 已尝试的证据」也不得强行归入三分支之一。

---

## Task 1: 真实会话复测执行 + 证据落档

**Files:**
- Create: `docs/evidence/p3-watcher-retest.md`
- (临时产物在 %TEMP% 工作区,不入仓)

**Interfaces:**
- Consumes: 003 重钉后的 engine.lock(2.1.296)、真实 claude CLI、`mulix run`、镜像查询(004 Task 4 确认:`~/.mulix/sessions/<id>/events.jsonl`)。
- Produces: 复测证据文档(时间线 + 定论 + 原始摘录);供 Task 2 消费其定论结论。

- [ ] **Step 1: 环境预检与工作区装配**

Run: `claude --version`
Expected: `2.1.296 (Claude Code)`(与 pin 一致;否则停止并按 FR-101 处理)

```bash
go build -o /tmp/mulix-bin/mulix.exe ./cmd/mulix && echo build_ok
WS=$(mktemp -d)/mulix-watcher-retest && mkdir -p "$WS" && cd "$WS" && git init -q
printf '# watcher retest baseline\nline 2\nline 3\n' > CLAUDE.md
cp /d/workplace/project/mulix-agent-coding/engine.lock .
cat > mulix.toml <<'EOF'
[permissions]
preset = "acceptEdits"
allow = ["PowerShell"]
EOF
git add . && git commit -qm init && echo "WS=$WS"
```

Expected: 工作区就绪;engine.lock 为 2.1.296 版。

Run: `/tmp/mulix-bin/mulix.exe doctor`
Expected: 全绿、无漂移告警。

- [ ] **Step 2: 长回合复测执行(编辑锚定基线后)**

在仓库根执行编排脚本(后台发起 run,轮询镜像 turn_started 作锚,锚后 +3s 编辑):

```bash
cd "$WS"
# 镜像全局共享:记录既有最新会话 id,轮询「新 id」作本轮锚——绝不删除历史镜像
BEFORE=$(ls -t ~/.mulix/sessions | head -1)
/tmp/mulix-bin/mulix.exe run "用 PowerShell 工具执行命令 Start-Sleep -Seconds 25,完成后读取 CLAUDE.md 并原样汇报全部内容" > /tmp/watcher-retest-stdout.txt 2>&1 &
RUNPID=$!
# 轮询新会话镜像出现且含 turn_started(基线锚)
for i in $(seq 1 60); do
  NEW=$(ls -t ~/.mulix/sessions | head -1)
  if [ "$NEW" != "$BEFORE" ] && grep -q "turn_started" ~/.mulix/sessions/$NEW/events.jsonl 2>/dev/null; then break; fi
  sleep 1
done
if [ "$NEW" = "$BEFORE" ]; then echo "no new session — branch C (evidence insufficient)"; kill $RUNPID 2>/dev/null; exit 1; fi
# 镜像事件无时间戳字段(仅 seq/kind/ev):锚取 events.jsonl 的 mtime——
# turn_started 发布即文件追加,mtime ≈ 发布时刻(文件来源,非墙钟估算)
T0=$(stat -c '%y' ~/.mulix/sessions/$NEW/events.jsonl)
echo "anchor session=$NEW turn_started(events.jsonl mtime)=$T0"
sleep 3   # 基线后 +3s(≥1.5 个轮询周期)
printf 'Watcher retest edit line B\n' >> CLAUDE.md
T1=$(date -Iseconds)
echo "edit at $T1"
wait $RUNPID
echo "run_exit=$?"
T2_NOTE=$(grep -c "快照保持" /tmp/watcher-retest-stdout.txt || true)
echo "stdout notice hits=$T2_NOTE"
```

注:①**镜像事件不带时间戳**(记录=kind/seq/ev 三键;meta.json 的 created_at 是会话创建时刻、早于基线装载,不可作锚)——锚改用 events.jsonl 的 **mtime**(turn_started 发布即追加,mtime≈发布时刻;证据文档必须如实记录这一锚替换:spec FR-102 原文「镜像 init 事件时刻」在此替换为「turn_started 发布时刻(文件 mtime)」,理由:init 事件晚于 turn_started,且 2s init 宽限路径下 start() 不等 init);②清理 `~/.mulix/sessions` 的做法改为「记录 BEFORE 最新会话 id、轮询新 id」(如上),不删历史镜像。

Expected(定论分支,如实记录实际观察):
- 分支 A(无缺陷):stdout 或镜像出现含「快照保持,下会话生效」的 info Notice,且编辑后回合存活 ≥2 个轮询周期 → 机制健康(有效判别式=存活窗口内 notice 出现与否;P2 原解释按证据修正,H1 排除)。
- 分支 B(缺陷):编辑晚于 turn_started、回合存活 ≥2 周期(≥4s 后仍在运行)、stdout 与镜像均无 notice → H2/H3 定论,路由修复。
- 分支 C(证据不足):回合提前结束/时间线无法闭合 → 如实记录「机制未定论」与已尝试证据,不强行归类。

- [ ] **Step 3: 证据收集**

Run(补齐镜像面观测):

```bash
grep -n "Notice\|notice" ~/.mulix/sessions/$NEW/events.jsonl | head -5
python -I -c "
import json,sys
for l in open(sys.argv[1], encoding='utf-8'):
    e=json.loads(l)
    if e.get('kind')=='notice':
        print(e['ev'].get('seq'), e['ev'].get('level'), e['ev'].get('content','')[:80])
" ~/.mulix/sessions/$NEW/events.jsonl
stat -c '%y' "$WS/CLAUDE.md"
tail -5 /tmp/watcher-retest-stdout.txt
```

Expected: 三时刻齐备(t0=events.jsonl mtime;t1=CLAUDE.md mtime;t2=**无独立时间戳**——镜像 notice 仅 seq 有序,t2 以「stdout 观测时刻/检测时 events.jsonl mtime」记录,证据文档如实注明);引擎汇报的是编辑后 CLAUDE.md 内容(快照语义不重载——与 P2 冒烟 C 一致)。

- [ ] **Step 4: 证据文档落盘**

写 `docs/evidence/p3-watcher-retest.md`,结构对齐 p2-smoke.md,必含:引擎版本与 pin、日期、独立工作区说明、任务文本、mulix.toml 摘要、**镜像无时间戳的锚替换说明**(mtime 锚 + turn_started 对 spec init 锚的替换理由)、**时间线三时刻**(原始摘录形式:镜像首事件行、stat 输出、notice 事件行或「无」)、stdout 关键摘录、镜像 notice 计数、**定论**(分支 A/B/C 三选一 + 依据)、与 P2 原解释的对照结论、(若分支 B)缺陷描述与路由条目。

- [ ] **Step 5: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ cmd/`
Expected: 全绿/干净(代码零改动佐证)

Run: `git status --short`
Expected: 仅 `docs/evidence/p3-watcher-retest.md` 新增(代码文件零变更,FR-107)

```bash
git add docs/evidence/p3-watcher-retest.md
git commit -m "docs(evidence): watcher 真实会话复测——时间线定论(006 FR-101..103;SC-001)"
```

---

## Task 2: p2-smoke.md 追加式修正

**Files:**
- Modify: `docs/evidence/p2-smoke.md`(冒烟 C 追记 + 诚实限制 #3 更新)

**Interfaces:**
- Consumes: Task 1 的定论结论与证据文档(`docs/evidence/p3-watcher-retest.md`)。
- Produces: p2-smoke.md 修正(追加式);P2 台账路由项 ④ 的关闭依据。

- [ ] **Step 1: 冒烟 C 段落追记**

在 `docs/evidence/p2-smoke.md` 冒烟 C 的「watcher notice 未出现(如实记录)」段末尾追加(不改原文):

```markdown
  **P3 复测定论(006-t12-5-watcher-p2-smoke,2026-10-11)**:原解释不成立/成立
  (按 Task 1 定论如实填写:三分支之一 + 一句依据),编辑时点判定标准与
  时间线见 `docs/evidence/p3-watcher-retest.md`。
```

(内容按实际定论填写;分支 B 时追加「缺陷路由:见台账」一句。)

- [ ] **Step 2: 诚实限制 #3 更新**

在诚实限制清单的 #3 条目(`引擎 2.1.291 未在本机共存,无法做同基线 A/B 对照…`)末尾追加一行(原文保留):

```markdown
  > P3 事实更新(006):003-p3-2-1-296 已将 pin 重钉 2.1.296(=本机实测),
  > 「pin≠本机」的前提解除;同基线 A/B 对照的可行性随事实重估,原限制条目
  > 保留作历史记录。
```

- [ ] **Step 3: 核对追加式纪律**

Run: `git diff docs/evidence/p2-smoke.md`
Expected: diff 只含**新增行**(以 `>` 或缩进续行形式),无原行删改(Review Focus:历史结论不被改写)。

- [ ] **Step 4: 全量门禁 + Commit**

Run: `go test ./... -count=1 && go vet ./... && gofmt -l internal/ cmd/ && git status --short`
Expected: 全绿;`git status` 无代码文件变更(FR-107)

```bash
git add docs/evidence/p2-smoke.md
git commit -m "docs(evidence): p2-smoke 冒烟 C 追记复测定论+限制 #3 事实更新(006 FR-105;SC-002)"
```
