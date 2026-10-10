# P3 冒烟证据(003-p3-2-1-296)

**日期:** 2026-10-10(P3 复冒烟;A1/A2/B/B2 四轮)
**引擎:** 真实 claude CLI **2.1.296 (Claude Code)**(`claude --version` 原文;engine.lock pin 2.1.296,2026-10-10 重钉)
**doctor(ws 实测):** node / claude 2.1.296 == pinned 2.1.296 / 凭据经 claude 凭据文件 / 工作区可写——全绿,**无漂移告警**(FR-109 治理语义一致;SC-003 实证)。
**方法:** 对齐 `p2-smoke.md`(无人值守通道 `mulix run`;真实引擎;事件经 mulix stdout + 用户全局镜像 `~/.mulix/sessions/` + 引擎 transcript 三面核验)。
**工作区:** `%TEMP%\mulix-p3-smoke-a\`(与仓库完全独立的 git 仓,基线 `8ed5c3b`);配置 `mulix.toml`(preset=acceptEdits + allow:Bash/PowerShell/mulix_spawn/poll/cancel/echo)+ `engine.lock`(2.1.296)+ `echo-mcp-server.mjs`(最小 stdio MCP echo server,node 原生)。冒烟产物在临时区;本文件记录哈希/会话 id/摘录以供复现。

## 冒烟 A1(委派链——SC-001 主实证)

- **任务文本:** 用 mulix_spawn 委派子代理:新建 notes.md(内容 `P3-SMOKE-OK`)并 git 提交。
- **结果(exit=0,会话 `f59d66c2`,子会话 `f59d66c2-sub-1`):**
  1. `mcp__mulix__mulix_spawn` → **approval_resolved decision=allow source=policy**(config allow 真实生效);
  2. **真实子会话镜像创建**(meta.parent_id 链,`mulix sessions` 双行可见)+ 主镜像 `delegation_started`;
  3. **多步工作真实完成**:子代理创建 `notes.md`(内容确认为 `P3-SMOKE-OK`,本仓复验属实)——**spawn/poll 的工具结果回流(2.1.296 数组形状)全部正确解读,无一处断流**;
  4. `git` 提交被拒——**Windows 引擎把 shell 暴露为 `PowerShell` 工具**(非 `Bash`),allow 未含 → **approval_resolved decision=deny source=policy resolved_by=unattended**(治理红线真实实证,与 P2 A1 同型);主会话诚实报告「提交被环境策略拦截」;
  5. **结果与 usage 回收进主会话**,`mulix run` exit 0,**graceful 收口**。
- **镜像事件计数:** tool_use_requested 22 / approval 对 21(spawn allow×1、poll allow×19、PowerShell deny×1)/ delegation_started 1 / delegation_completed 1 / usage 1 / turn_done 1。
- **跨轮统一负样本核验:四轮全部 runs(4 个 stdout 文件)grep `stream corrupt` / `cannot unmarshal array` = 0 命中**(P2 断流签名消失)。

## 冒烟 A2(提交收口)

- **配置:** allow 补 `PowerShell`(冒烟工作区提交 `71a914e`)。
- **结果(exit=0,会话 `9e08218f`):** 主回合直接完成——状态核实 → `notes.md` 内容确认 → 提交 → **git commit `f479564 "smoke: notes"`**(本仓复验在案),工作区干净,`[引擎已退出] graceful`。
- A1+A2 合并 = 读改提交闭环(诚实注记:提交在第二轮完成,首轮缺口是工具名 allow 缺口所致的治理拦截,非断流)。

## 冒烟 B(echo 未入 allow——治理负样本,留痕)

- **任务文本:** 调 `mcp__echo__echo`(text=`mulix-p3-smoke-echo-67890`)并复述。
- **结果(exit=0,会话 `a0118b37`):** `mcp__echo__echo` 未入 allow → 无人值守 fail-closed 拒绝,主代理如实报告未执行——与 P2 A1 同型,治理语义未因本次修复放宽。

## 冒烟 B2(外部工具回合——P2 断流场景的正样本复验)

- **配置:** allow 补 `mcp__echo__echo`(冒烟工作区提交)。
- **结果(exit=0,会话 `0b28c203`):** stdout 原文第 6 行 **`echo: mulix-p3-smoke-echo-67890`**——外部 MCP 工具结果回流被正确解读(2.1.296 数组形状),`[引擎已退出] graceful`。**这正是 P2 冒烟 B 断流的同一类回合。**

## 缓存记账(mulix cache 跨进程权威口径,P1 meter 公式)

| 会话 | 轮次 | 命中率 | 未命中/读取 | 输出 | 成本 | 节省 |
|---|---|---|---|---|---|---|
| `f59d66c2`(A1 委派主回合) | 1 | **95.3%** | 39869/817152 | 2095 | $0.3962 | $2.2063 |
| `f59d66c2-sub-1`(子会话) | 1 | 82.6% | 37830/179584 | 3003 | $0.2124 | $0.4849 |
| `9e08218f`(A2 提交收口) | 1 | 96.8% | 3399/104000 | 339 | $0.0465 | $0.2808 |
| `a0118b37`(B echo 拒) | 1 | 49.9% | 35797/35584 | 231 | $0.1215 | $0.0961 |
| `0b28c203`(B2 echo 成功) | 1 | 84.2% | 11254/60160 | 144 | $0.0540 | $0.1624 |

**基准回填依据:** `baseline.cache_hit_rate = 0.953`(冒烟 A1 委派主会话 `f59d66c2` 实测 session_hit;单次真实会话,2026-10-10,委派任务,加权口径 Σread/(Σuncached+Σwrite+Σread))。

## 引擎 transcript

5 份引擎会话 transcript(1 主 + 1 子 + 3 后续回合)留存于
`~/.claude/projects/C--Users-Zhang-AppData-Local-Temp-mulix-p3-smoke-a/`
(引擎自留档;tool_result content 为 `[{"type":"text",…}]` 数组形状)。

## 结论

FR-107 关闭:P2 report.md「诚实限制」#1(真实委派链断流)在真实引擎 2.1.296 上复验通过——委派链(spawn→policy allow→子镜像→多步工作→结果与 usage 回收)与外部工具回合(echo 结果回流)全部正常收口,断流签名零命中。FR-108 未触发(无环境受阻项)。
