# 变更报告：引擎工具治理旁路修复（2026-10-16-engine-tool-governance-fix）

**日期**: 2026-10-16 | **结果**: verify PASS | **终审**: 独立终审 1 Important（已 TDD 修复 e7314c9）+ 1 Minor（deferred）

## 1. 门禁实录（verify 阶段全量重跑）

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过 |
| 全量测试 | `go test ./... -count=1` | 27 包 ok，零失败 |
| 静态检查 | `go vet ./...` | 干净 |
| 格式 | gofmt（LF 副本口径，Ruling-4）对本变更全部改动 .go 文件 | 零偏差 |
| -race | CGO_ENABLED=0 不可用（环境限制，如实记录） | — |

## 2. 需求逐项对账（spec FR/SC）

| 需求 | 落点 | 核验 |
|---|---|---|
| FR-201 Boot 面强制询问注入 | cache.Compose `composeShellAskRules`（字节 `{"permissions":{"ask":["Bash","PowerShell"]}}`）→ buildArgs `--settings` 物化 → composeBoot 接线 | TestComposeInjectsShellAskRules + TestBuildArgs 逐字 + TestRunUnattendedE2E boot 断言 |
| FR-202 指纹覆盖 | fingerprint canonical struct `settings_json` 字段 | TestFingerprintCoversSettingsJSON（字段覆盖探针） |
| FR-203 字节稳定冻结 | encoding/json map 键序 + 常量清单（非用户配置） | TestBootComposeByteStable 既有护栏延续 |
| FR-204 Gate 语义不变 | 审批回路原样；allow 清单经 policy（guard 补修 bcb155e：PowerShell 同 Bash 命令语义） | TestGovernanceAdjudicatedPass allow/deny + TestPowerShellSpecifier + 冒烟 S1/S2b |
| FR-205 围堵判定 | checkGovernedResults：运行态 + 未裁决 + 非白名单（config allow ∨ mcp__mulix__ ∨ Read/Glob/Grep） | 治理五例（violation/allow/adjudicated×2/readonly/orphan） |
| FR-206 留痕先于终止 + 输入摘要 | publish（镜像先行）→ 异步 host.Close；摘要：跟踪态 Input 截断、未跟踪态结果前缀（终审 Important 修复） | TestGovernanceViolationTerminatesSession + TestGovernanceViolationIncludesInputDigest（tracked/untracked） |
| FR-207 孤儿不误杀 | running 门控 | TestGovernanceOrphanAfterCancel（真实 running 周期 relay 形态） |
| FR-208 已裁决透传 | resolved 命中即 pass（deny 的 is_error 照常） | TestGovernanceAdjudicatedPass/deny |
| FR-209 Boot 面测试对齐 + lock 不动 | TestBuildArgs 新序列；engine.lock 零改动（git 对账） | 逐字测试 + `git diff 077741f HEAD -- engine.lock` 为空 |
| SC-001 echo 回合必现审批事件对 | 真机冒烟 S1/S2b | 镜像 a67038d7/69bb9fdd 逐字段对账 PASS（证据 docs/evidence/smoke-engine-governance-2026-10-16.md） |
| SC-002 allow 迁移结果不变 | 真机 S2b allow 实跑 + S1 未命中 fail-closed | PASS |
| SC-003 全量门禁 + 围堵测试 | 见 §1 + 治理六测试 | PASS |
| SC-004 指纹随注入面变化 | TestFingerprintCoversSettingsJSON | PASS |

## 3. 台账 Ruling 复核（Ruling-1..10 + Final 裁定）

- Ruling-1（inline 执行）：CLAUDE.md §7 授权成立，五任务全绿——**成立**。
- Ruling-2（计划评审闭环）：4 Important + 3 Advisory 全部落入计划修订——**成立**。
- Ruling-3/5（编译期 RED 如实入账）：与 spec FR 无冲突——**成立**。
- Ruling-4（gofmt CRLF 门禁口径）：LF 副本核验本变更零偏差——**成立**（环境性，非本变更引入）。
- Ruling-6（cleanupTemp 单测直设字段）：Start 接线由真机冒烟（真实 ClaudeHost）补覆盖——**成立**。
- Ruling-7（格式归一回写）：门禁口径内——**成立**。
- Ruling-8（RED 形态三次迭代）：最终 RED 为真实行为失败——**成立**。
- Ruling-9（fixture Write→Read 治理化）：旧形态在新不变量下本就非法；「Write 经审批」happy path 由 TestGovernanceAdjudicatedPass 补偿——**成立**（终审同裁可）。
- Ruling-10（guard PowerShell 语义补修）：SC-002 前提修复，词边界红线同款——**成立**。
- Final fixed（违规留痕输入摘要）：FR-206 追加覆盖——**成立**。

## 4. Deferred minors

- buildArgs 内 mcp 物化失败路径不清理已物化的 settingsTemp（$TMP 泄漏；需双物化 + TMP 异常同时发生；终审 Minor，不进修复轮；路由：后续清理类变更）。
- 引擎内部子代理（Task）流形态的 parent_tool_use_id 建模（终审 Declined-to-judge；子代理会话经完整 Controller 自动覆盖围堵，流形态区分留后续）。

## 5. 运营知会（归档前请用户知悉）

治理开启后，若引擎对其余客户端本地工具（TodoWrite/Task/WebSearch 等）存在自主放行类，真实会话的首次此类调用将以 `[governance_violation]` 响亮终止会话（fail-closed 设计姿态）；处置（白名单收编/维持终止）属运营裁定，按 FR-205 白名单面扩展即可。

## 6. 交付物

6 commits（077741f..e7314c9）：cache 注入面与指纹（e453505）→ engine --settings 物化（a4dc6c6）→ control 接线（0cc879e）→ 运行时围堵（e6536c1）→ guard PowerShell 语义（bcb155e）→ 证据（2c89c6b）→ 终审修复（e7314c9）。engine.lock 未动。镜像证据 `~/.mulix/sessions/{a67038d7,a8b4bc66,69bb9fdd}`；根因实验 harness `Temp\mulix-bypass-repro\`。
