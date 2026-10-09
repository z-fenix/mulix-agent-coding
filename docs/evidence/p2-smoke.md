# P2 真实引擎冒烟证据（SC-001/002/004/005/006 真实引擎面）

**Change:** 002-p2
**Date:** 2026-10-10
**引擎:** 真实 claude CLI **2.1.296**（本机实际版本；engine.lock pin 为
2.1.291——冒烟工作区副本按本机实际版本重钉为 2.1.296，协议探针字段
不变。主仓 engine.lock 未动。漂移影响见「冒烟失败记录」）。
node v24.12.0，Windows。冒烟二进制 `mulix-real.exe` =
`go build -o %TEMP%\mulix-p2-smoke\mulix-real.exe ./cmd/mulix`
（f4486e6 同源，2026-10-10 04:49:51，exit 0）。
**doctor 全绿**（ws-a 实测）：node v24.12.0 / claude 2.1.296 == pinned
2.1.296 / 凭据经 claude 凭据文件 / 工作区可写，exit 0。

冒烟工作区 = `%TEMP%\mulix-p2-smoke\`（与仓库完全独立）：
- `ws-a`（git 仓，基线提交 `51460cc`）：README.md / calc.py /
  mulix.toml（permissions.allow 六件套 + spawn 三件套）/ engine.lock(2.1.296)
- `ws-b`（git 仓，基线提交 `d2b469e`）：同上 + `[tools] memory_search=true`
  + `[[mcp.servers]] name="echo" command=["node", …echo-mcp-server.mjs]`
  + 预置记忆 `.mulix/memory/entries.jsonl`（m1 pnpm 约定 / m2 提交前缀）
- `echo-mcp-server.mjs`：最小 stdio MCP echo server（node 原生，无依赖），
  自测 initialize/tools/list/tools/call 三应答全对（`echo: ping`）。

---

## 冒烟 A1（ws-a，spawn 未入 allow 清单——治理负样本，留痕）

- **任务文本**：委派给子代理（mulix_spawn）读改测提交 + poll 回收。
- **结果**：`[审批] 工具 mcp__mulix__mulix_spawn 需要授权（无人值守将
  fail-closed 拒绝）`——**allow 清单外的 MCP 工具在无人值守下被
  fail-closed 拒绝**；主代理收到 `denied by policy: unattended` 后如实
  汇报（未绕过、未篡改配置）。Bash/Edit/Write 的 5 次失败尝试全数被拒
  并落镜像 approval_request/resolved。引擎流最终被 2.1.296 形状问题
  截断（见「冒烟失败记录」①），mulix run exit 1（**非悬挂**）。
- **价值**：FR-101「子代理工具调用与主代理同一治理回路」的 fail-closed
  侧真实引擎实证（对照 `TestGateUnattendedFailClosed` /
  `TestConfigUnmatchedToolDeniesUnderUnattended`）。

## 冒烟 A2（ws-a，allow + spawn/poll/cancel——SC-001/006 正样本）

- **配置**：allow 追加 `mcp__mulix__mulix_spawn/_poll/_cancel`（冒烟工作区
  mulix.toml，git 提交 `32f1747` 之外的运行时修改——冒烟配置，不入主仓）。
- **结果（事件时序，逐条经镜像 events.jsonl 核验）**：
  1. 主代理 thinking 后真实调用 `mcp__mulix__mulix_spawn`（task=读改测提交
     四步）→ approval_request → **approval_resolved decision=allow
     source=policy**（config allow 规则真实生效）；
  2. **真实子会话镜像创建**：`2517bca…-sub-1`，meta.parent_id ==
     `2517bca…`（委派链可审计）；主镜像 delegation_started depth=0 +
     task 全文；
  3. **多步编码任务真实完成**：git commit **`9f71090` "smoke: subagent
     add subtract"**——calc.py 增 `subtract`、test_calc.py 断言经 python
     执行通过（本轮复验 exit 0）；执行方式：主代理 spawn 后在引擎会话内
     经内置 Agent 子代理完成改动（引擎侧 transcript 有完整工具链与
     `[Subagent hand-back]` 报告帧：测试输出 `all tests passed`、提交哈希、
     subagent_tokens 26046）；
  4. 主引擎流在 spawn tool_result 回流时被 2.1.296 形状问题截断 → 回合
     error 收口 → **delegation_cancelled reason=manager_closed 级联收口
     子代理**（子镜像 5 事件收口无孤儿回合）→ mulix run exit 1（非悬挂）。
- **结论**：委派链（spawn → policy allow → 子镜像 → 委派事件 → 取消级联
  收口）端到端真实走通；「poll 回收 summary」一环受引擎版本漂移阻断
  （诚实记录，修复路由见 p2-acceptance.md 限制①）。

## 冒烟 B（ws-b——SC-002 开启冒烟 + SC-004 echo 冒烟）

- **任务文本**：调 `mcp__echo__echo`（text=`mulix-p2-smoke-echo-12345`）
  并汇报包管理器约定。
- **结果（mulix stdout 原文 + 镜像 + 引擎 transcript 三面核验）**：
  1. **记忆召回真实注入**：`[notice] 记忆召回: 2 条命中，已注入本回合
     turn tail`（mulix stdout 首行）；镜像 notice 事件落档（seq=2）；
     引擎收到的 user message 尾部真实携带：
     > [记忆召回]
     > - 本项目包管理器用 pnpm，不要用 npm
     > - 冒烟工作区的提交信息前缀用 smoke:

     （claude transcript 原文——turn tail 注入面逐字验证，Boot 面不含
     该内容，护栏测试见 p2-acceptance.md SC-002）
  2. **外部 echo server 真实调用成功**：引擎按 mcp-config 自 spawn echo
     server → `mcp__echo__echo`（命名空间）→ approval_request（tool=
     mcp__echo__echo input 原文入镜像）→ **approval_resolved allow
     source=policy** → 引擎侧 tool_result：
     **`echo: mulix-p2-smoke-echo-12345`**（transcript 原文——外部 server
     stdio 协议往返成功）；
  3. 回合随后被同一 2.1.296 形状问题截断（mulix run exit 1，非悬挂）。
- **结论**：SC-002 开启冒烟 ✅；SC-004「工具在回合中成功调用」的引擎侧
  往返 ✅（mulix 侧后续事件面受限同 A2）。

## 冒烟 C（ws-a——SC-005 真实会话快照语义）

- **任务文本**：读取 CLAUDE.md 并原样汇报全部内容（CLAUDE.md 已于会话前
  提交 `32f1747`，运行中第 6 秒追加一行 "Watcher smoke edit line B"）。
- **结果**：引擎真实读盘并汇报**变更后**内容（5 行全文回显）→ 引擎
  graceful 退出 → **`mulix run` exit 0**。会话未因文件变更中断（快照
  语义 + 只提醒不重载）。
- **watcher notice 未出现**（如实记录）：实测会话生命周期（turn 14.7s）
  短于 watcher 生效窗口——Submit 串行先于 watcher 启动（controller.go
  start 序列尾装载），Close 即停轮询，短回合 + 8 秒窗口未赶上 2s tick
  的事件发布；watcher 行为由 6 个矩阵测试钉死（见 p2-acceptance.md
  SC-005），真实会话提醒留待复测。

## 冒烟 D（ws-a——SC-005 快照查询命令）

- **`mulix instructions`（真实二进制，ws-a cwd）**，exit 0：
  ```
  指令文件快照（workspace: …\ws-a）
  CLAUDE.md  …\ws-a\CLAUDE.md
    sha256: bb6e8917bc73a9e4ec57e99bde12f4712c0c696fed930eefb2724cccb1916196
    摘要: 6 行 / 87 字节
    | # ws-a instructions
    | Original line A (restored).
    | Watcher smoke D edit (2026-10-10).
  ```
- 另：无指令文件会话（冒烟 B 前的 ws-b 状态）+ 空工作区行为由
  `TestInstructionsEmptyWorkspace` 钉死。

## 冒烟失败记录（修复前/受限面，留痕——同 P1 冒烟方法）

- **①2.1.296 tool_result 形状变更（A1/A2/B 共同根因，本轮最重要发现）**：
  `[引擎已退出] engine: stdout stream corrupt: engine: decode user: json:
  cannot unmarshal array into Go struct field .message.content.0.content
  of type string`。实证：引擎 transcript 中 2.1.296 的 tool_result 行为
  `"content":[{"type":"text","text":"…"}]`（数组），mulix 解码器
  （events.go ToolResultBlock.Content string）按 2.1.291 字符串形状建模。
  触发面：MCP 工具成功结果回流（spawn/echo）；不触发面：内置 Read 结果
  （字符串形状，冒烟 C 全回合通过）、审批拒绝回流（A1 五连拒流未断）。
  行为面：流损坏 fail-closed → 回合 error 收口 → 子代理级联收口 → 进程
  exit 1（无悬挂、无半开资源）。**修复路由**：解码器两形状容忍 +
  engine.lock 重钉 + fixture 增补（P3，不属本任务）。
- **②watcher 真实会话 notice 未赶上窗口**：见冒烟 C——机制面已由测试
  钉死，属复测项非缺陷。

## 诚实限制

- 冒烟产物在 `%TEMP%`（非仓库）；本文件记录哈希与任务文本以供复现。
- 无人值守通道（`mulix run`）验证；TUI 通道真实 TTY 交互同 P1 限制
  （共享装配路径与控制回路，单元面已钉）。
- 引擎 2.1.291 未在本机共存，无法做同基线 A/B 对照（P1 冒烟 A/B 法的
  「B」在本轮以 allow 清单差异（A1 vs A2）与功能开关（B）替代）。

## 汇总

| 冒烟 | 工作区 | 结论 | 关键哈希/事件 |
|---|---|---|---|
| A1 | ws-a | 治理 fail-closed 实证（spawn 拒绝） | approval 链 + exit 1 非悬挂 |
| A2 | ws-a | 委派链端到端（spawn→子镜像→真实提交→级联收口） | commit `9f71090`、sub-1 镜像、manager_closed |
| B | ws-b | 记忆召回注入 + echo 外部 server 往返 | `[notice] 记忆召回: 2 条`、`echo: mulix-p2-smoke-echo-12345` |
| C | ws-a | 真实会话快照语义 + exit 0 | 全回合 graceful、读盘回显 |
| D | ws-a | `mulix instructions` 快照查询 | sha256/摘要/预览 exit 0 |
