# Verify Report — 2026-10-18-web-ui-mvp

**Date**: 2026-10-19 · **Verifier**: orchestrator（fresh run，证据日志 `.mulix/.runtime/2026-10-18-web-ui-mvp/sdd/verify-logs/`）

## 1. 构建与测试命令（本阶段全新执行）

| 命令 | 退出码 | 摘要 |
|---|---|---|
| `go build ./...` | 0 | 全模块构建通过 |
| `go vet ./...` | 0 | 静态检查干净 |
| `go test ./... -count=1` | 0 | 29 包全 ok（含 internal/serve 全套 + 5 场景 e2e） |
| `npm --prefix web run test` | 0 | vitest 54/54（6 文件） |
| `npm --prefix web run build` | 0 | tsc + vite 产物落 internal/serve/dist |
| `go test ./internal/serve/ -run 'TestEmbedDistServed\|TestE2E'` | 0 | embed 占位 + e2e 回归通过；dist 占位已复原 |
| `gofmt -l`（触碰文件域） | — | internal/serve、internal/config、internal/sessionx、internal/cli 本变更触碰文件全部干净；`internal/cli/hooks_wiring_test.go`、`internal/cli/session_test.go` 为 HEAD 既有 CRLF 文件（R3 裁定，非本变更触碰——git diff master..HEAD 核实） |

-race 因 CGO_ENABLED=0 不可用（环境限制，如实记录）。

## 2. 逐条需求核对（spec FR-001..FR-016）

| FR | 结果 | 证据 |
|---|---|---|
| FR-001 本地服务入口/回环/端口默认可配 | ✅ | `mulix web`（T8）；[web] port 默认 3180、bind 127.0.0.1（T1 config 测试钉）；wiring 测试 5 子测试 |
| FR-002 会话列表/新建/进入 | ✅ | http.go 路由全表 + 列表合并（本进程真实态 + ScanSessions 启发式）；interaction/Http 测试 |
| FR-003 镜像回放 + 损坏段隔离语义 | ✅ | ReadEventsRelaxed 非变异（字节级断言）+ /events warnings 头帧（T2/T7 测试）；好行照读不硬失败 |
| FR-004 流式 markdown + 代码高亮 | ✅ | transcript MarkdownText（marked + hljs + DOMPurify）；stream 对账/去重测试 |
| FR-005 工具卡片 + diff 渲染 | ✅* | ToolCard/DiffBlock 全实现；**耗时要素缺失**——事件面无工具级时戳（TurnDone.DurationMS 为回合级），TUI 同样不渲染；Ruling 入账（progress.md:94），折中方案留二期裁定 |
| FR-006 提交/回合未收口 409 明确提示 | ✅ | submit 202/409（消息含「回合」）；interaction 测试 |
| FR-007 中断回合 | ✅ | POST cancel → ctrl.Cancel；e2e TestE2ECancelViaHTTP 闭环 |
| FR-008 审批交互呈现 | ✅ | ApprovalBar（倒计时/三动作/y-a-n 键盘/IME 守卫/焦点陷阱）；e2e 审批 allow 闭环 |
| FR-009 fail-closed（120s 窗口保留/断开即拒） | ✅ | UserApprovalBridge 双槽 + watchInteractiveGone → CancelAll；e2e interactive_offline deny；修复后宽限内重连重 claim 不误拒（线级 e2e） |
| FR-010 先镜像后发布 1:1 | ✅ | e2e 三面对账（SSE=回放=store 逐位）+ 单包镜像先行断言 |
| FR-011 工作区选择前置 | ✅ | WorkspacePicker 门禁在提交处强制（interaction 测试：无 workspace 提交禁用） |
| FR-012 断线重连追平 | ✅ | stream 先开流后对账 + (kind,seq) 去重 + 缺口回归测试（final fix I2 双半闭合） |
| FR-013 漂移/拒绝如实呈现 | ✅ | TestAssembleEngineDriftRefused + StatusBar FAILED 映射（interaction 测试） |
| FR-014 浏览器关闭回合继续 | ✅ | 服务端持有 controller（架构保证）+ e2e 服务端生命周期测试（Run 关停逐 host 收尾） |
| FR-015 跨进程会话锁 | ✅ | web.lock pid+心跳；e2e 锁 409 呈 pid；过期/损坏接管（lock 测试） |
| FR-016 同源防护 | ✅ | SameSite=Strict Cookie + Origin 校验全 mutating；T7 钉测 + final fix 后 Cookie 门不受 ?client= 放大 |

**SC 核对**：SC-003（重连一致性）e2e 断言成立；SC-004（无人应答拒绝率 100%）由 e2e interactive_offline + 120s timeout 语义覆盖。**SC-001/002/005 需真实引擎/真实浏览器在线计时——本机宿主认证受阻，待用户实测**（见 §4）。

**Edge cases**：引擎中途崩溃（EngineExited 扫尾卡失败，reducer 测试）；多标签并发（final fix I1 每标签页身份后开只读——e2e/Http 测试）；超长 transcript（对账幂等；reducer O(n) 为 verify 观测点）；回合未收口提交（409）；损坏段（隔离呈现不硬失败）。

## 3. 裁定与 deferred minor 复核（progress.md 全量）

| 裁定/项 | 复核结论 |
|---|---|
| R1 不建 worktree（hook 锚定主仓） | 成立，全程无踩坑 |
| R2 spec 回填降级 | 未触发——T1 实际回填成功（hook 放行） |
| R3 gofmt 门按触碰文件域 | 成立，HEAD 既有 CRLF 与本变更无关（本节 §1 已核） |
| T5 gone-after-fired claim 语义 | 成立（one-shot 闸 + 钉子测试）；终审后线级 e2e 补齐重连重 claim 通路 |
| T6 子代理工厂不接线（二期） | 成立：spawn 请求 MCP 面清晰失败，泵/镜像/收口不受影响 |
| T6 新会话工作区恢复门 | 成立：T7 create 端点已实现 cli 对齐门（handleCreate workspaceRecoveryBlocked） |
| T6 RegisterConditional 单工作区 memory 约束 | 成立：默认 memory_search 关；单工作区约束在案 |
| T7/T8 Run 关停逐 host 收尾 | 成立：closeAll + web.lock 消失传递断言（shutdown 测试）；终审孤儿化缺口已闭合 |
| T9 4 个机械 devDeps + audit 延期 | 成立：dev 工具链不入生产 bundle；升级留后续变更 |
| T13 变异核验 RED 形态 | 成立：栈预存场景的合理 RED 形态，brief 明示 |
| 终审 I3 FR-005 耗时 Ruling | 在账（progress.md:94）；建议随归档同步 spec Clarifications |
| 终审 C1/I1/I2/M1/M3 修复 | 复审 6/6 ADDRESSED，无新增阻塞项（review-final-fix） |
| Deferred minors（T2 警告序/T5 TOCTOU×2/T7 竞窗/T8 双载/T11 观感等） | 维持 deferred 合理：危害有界或有留痕，终审逐条 triage 通过 |

## 4. 阻塞与待用户事项

- **真实引擎冒烟受阻（FR-108 如实记录）**：本机 claude 2.1.302 与 engine.lock 一致，但宿主托管认证不可用（`ANTHROPIC_AUTH_TOKEN` 不在环境，`claude -p` 独立复现 "Not logged in"）。服务全链路（Cookie/create/open/submit/事件流/turn 生命周期）已实测工作；模型真实作答证据待用户 `claude /login` 后补齐（证据文档 docs/evidence/2026-10-18-web-ui-mvp.md 已留补录位）。
- **浏览器体验实测**：SC-001（1 分钟上手）、SC-002（≤1s 延迟体感）、SC-005（数千事件 5s 回放）需用户在真实浏览器验收；reducer O(n)/帧为性能观测点。
- verify 补录：本报告由 verify 阶段落盘 docs/changes/2026-10-18-web-ui-mvp/report.md（终审预告的占位完成）。

## 5. 结论

自动化面全绿（Go 29 包 + 前端 54 测试 + e2e 5 场景 + 构建链）；16 条 FR 中 15 条 ✅、FR-005 带 Ruling 的要素缺失（耗时）；fail-closed 红线经终审与修复轮确认闭合。**verify_result: pass**（附真实引擎/浏览器两项用户待验事项，不阻塞门禁——受阻事实已按 FR-108 记录）。
