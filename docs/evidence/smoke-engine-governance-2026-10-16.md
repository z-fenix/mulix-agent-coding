# 引擎工具治理旁路修复真机冒烟证据（2026-10-16，SC-001/SC-002）

**变更**: 2026-10-16-engine-tool-governance-fix | **平台**: Windows 11 / Git Bash | **基线**: worktree cool-panini-5e34b2（077741f + 本变更 5 commit）
**二进制**: `Temp\mulix-bypass-repro\mulix-cli-gov.exe`（出仓构建自 ./cmd/mulix-cli @ bcb155e；仓根镜像进程禁写 C:\Users\Zhang 管控不变——运行 cwd 为 Temp 工作区）
**引擎**: claude CLI 2.1.301 ≠ engine.lock pinned 2.1.296 → `--allow-engine-drift` 放行（FR-016，留痕入镜像）
**环境剥离**: 驱动侧剥离 CLAUDE*/ANTHROPIC* 环境变量（P1 驱动器惯例）；凭据经 user settings env 块（--setting-sources user）

## 根因实验（修复前，harness 留存 `Temp\mulix-bypass-repro\`）

| 实验 | 构造 | 结果 | cost |
|---|---|---|---|
| r0 | `--permission-prompt-tool` stub + `echo repro-r0` | PowerShell 执行，stub **零咨询**（旁路复现=冒烟 run1 t4 同型） | $0.1096 |
| r0b | `Set-Content probe-r0b.txt` | stub 被咨询（tool_name/input/tool_use_id），deny 生效、文件未落盘 | $0.1283 |
| r1 | +`--settings '{"permissions":{"ask":["PowerShell"]}}'` + echo | **强制咨询**（ask 压制引擎只读自动放行） | $0.1122 |
| r3 | r1 + `--allowedTools "PowerShell(echo:*)"` | 仍咨询（ask > allow，引擎短路被 Gate 取代） | $0.1114 |
| r2 | `CLAUDE_CODE_USE_POWERSHELL_TOOL=0` | Git Bash 缺失 → 引擎拒启（不可作修复手段） | $0 |

小计 $0.4616。引擎侧自主放行类枚举（二进制字符串证据）：`POWERSHELL_READ_ONLY_ALLOW_REASON` / `READ_ONLY_AUTO_ALLOW_REASON` / `SANDBOX_AUTO_ALLOW_REASON`。

## 修复后真机冒烟（mulix-cli run 无人值守，引擎=claude 2.1.301）

| 会话 | 配置 | 结果 | 镜像证据（events.jsonl） | usage |
|---|---|---|---|---|
| a67038d7（S1） | 无 allow | **审批回路介入** → 无人值守 fail-closed 拒绝，echo 未执行 | `approval_request{tool:PowerShell,command:"echo gov-smoke-1"}` → `approval_resolved{deny,policy,unattended}` → `tool_result{is_error:true,"denied by policy: unattended"}` | $0.1156 |
| a8b4bc66（S2a） | allow=["PowerShell(echo:*)"]（修复前二进制） | 拒绝——**暴露 guard 既有缺口**：ruleMatches 只给 Bash 命令语义，PowerShell 落 default 分支永不匹配 | 同上 deny 形态 | $0.1164 |
| 69bb9fdd（S2b） | 同上（guard 补修后 bcb155e） | **allow 命中 → Gate 放行 → 命令实跑**（stdout `gov-smoke-2`） | `approval_request{...command:"echo gov-smoke-2"}` → `approval_resolved{allow,policy}` → `tool_result{content:"gov-smoke-2",is_error:false}` | $0.0221 |

小计 $0.2541（3 真实回合；镜像 `~/.mulix/sessions/{a67038d7,a8b4bc66,69bb9fdd}` 供复测）。

## 断言对账

- **SC-001**（echo 回合必现审批事件对）：PASS——修复前同型调用零审批事件（r0），修复后 approval_request/approval_resolved 逐字段在镜像（S1/S2b）。
- **SC-002**（allow 迁移结果不变性）：PASS——`PowerShell(echo:*)` 命中 → resolved{allow,policy} → 命令实跑（S2b）；未命中 → unattended fail-closed 拒绝（S1）。
- 引擎侧只读自动放行已被 ask 规则压制：r1/r3 实验 + S1/S2b（引擎对只读 echo 发起审批咨询）双重印证。
- 补修项（bcb155e）：`guard.ruleMatches` 增加 PowerShell 同 Bash 命令语义（词边界红线同款：`echo;rm` 不放行，TestPowerShellSpecifier 钉住）。

## 残留与如实记录

- -race 因 CGO_ENABLED=0 不可用（环境限制，如实记录）；gofmt 门禁在本 worktree 因 core.autocrlf=true 对全仓误报行尾——以 LF 副本 gofmt -d 为空 + go vet 兜底（Ruling-4）。
- S2a 会话是修复中间态（guard 补修前）的真实记录，保留作缺口证据。
- 引擎工具清单中其余潜在自主放行类（Read/Glob/Grep 之外）由 L2 运行时围堵兜底：未裁决回流 → `[governance_violation]` + 终止（自动化测试五例钉住）。
