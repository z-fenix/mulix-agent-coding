# Verify 报告 — 2026-10-15-tui-reasonix-refactor

**Date**: 2026-10-16 · **Range**: 3b30910..40134ab（23+1 commits，22 任务 + 终审修复波） · **Result**: PASS

## 1. 全量门禁（本阶段新鲜执行，非引用历史）

| 命令 | 结果 |
|---|---|
| `go build ./...` | OK（exit 0） |
| `go test ./... -count=1` | **27 包全部 ok**（含 internal/tui 0.631s、internal/cli 4.665s、internal/lint 0.851s；无 FAIL 无 SKIP 异常） |
| `go vet ./...` | 干净（exit 0） |
| `gofmt -l .` | 空输出（exit 0） |
| `go test ./internal/lint/ -count=1`（分层） | ok（internal/tui 登记 + 全包 import 边界） |

## 2. 逐条需求核验（spec FR-001..019 / SC-001..005）

| 需求 | 结论 | 证据锚 |
|---|---|---|
| FR-001 alt-screen 全接管 | ✅ | T4；冒烟启动帧 |
| FR-002 滚动+尾随 | ✅ | T5/T6；TestModalOpenNoUnfollow（#6430 钉）；P1 冒烟 run7 视口签名冻结表 |
| FR-003 流式段落提交 | ✅ | T7（splitFlushablePrefix 算法对照参考逐行核实）；引擎消息级粒度事实= D17 记录 |
| FR-004 思考有界窗+折叠 | ✅ | T8（TestTailWindowBound/Collapse/NoZeroTokenRender）；P3 冒烟 #8-10 |
| FR-005 工具卡 | ✅ | T9/T20；P1 冒烟 6×Read 卡流转；P3 冒烟 diff 卡 ✎ 写/改 与盘上内容对账 |
| FR-006 缩放语义重排 | ✅（尾项见裁定） | T5 wrapcache（渲染计数探针）；P1 冒烟 88x30 重排帧连贯；**「阅读位置尽量保持」未实现——终审 Important-2 裁定为 spec 软承诺，路由新变更（FR-006 尾项）** |
| FR-007 多行编辑器 | ✅ | T13（含评审修复：忙碌召回门禁） |
| FR-008 粘贴折叠 | ✅ | T14；P3 冒烟 26 PASS + 1 项未真机行使（诚实标注，单测覆盖） |
| FR-009 补全+/resume 选择器 | ✅ | T15（ListSessions 谓词与 resolveResume 逐字等价核实） |
| FR-010 授权卡升级+治理不变 | ✅ | T16（键位 1/y/2/a/n/Esc；治理不变量逐字节）；P3 冒烟 1/2 键真实裁决 + **120.1s fail-closed 精准触发且目标文件未落盘**（三重独立确认） |
| FR-011 Esc 取消+语义阶梯 | ✅ | T10；P1/P3 冒烟 ⏹ 行 + 引擎 error_during_execution 免疫 |
| FR-012 回执+双行状态栏 | ✅（仪表见裁定） | T17；回执与镜像用量逐字段对账；上下文仪表省略=D7（Snapshot 实无字段，终审判定合规非砍scope） |
| FR-013 可慢不可丢 | ✅ | T2/T3；TestEventChannelBackpressure + TestDrainCoalescing 迁移后仍绿；镜像两次冒烟 seq 连续无损坏段 |
| FR-014 主题+NO_COLOR | ✅ | T18/T19；P3 冒烟 SGR 前后集合**严格不相交**（按计数）+ NO_COLOR 0 色 |
| FR-015 中文界面 | ✅ | 全程 |
| FR-016 事件渲染覆盖矩阵 | ✅ | T11（20 行覆盖 15 种 + 取消三分支） |
| FR-017 依赖裁定+勘误 | ✅ | go.mod 终态 = 裁定五家族 + x/ansi 直用；CLAUDE.md §4 勘误落地（终审修复波补 x/ansi） |
| FR-018 P1–P3 全做 | ✅ | T1–T22 |
| FR-019 mulix 面板保留 | ✅ | T2/T15（/agents /doctor /cache 迁移；/rewind 占位保留） |
| SC-001 真机冒烟全清单 | ✅ | P1 12/12 + P3 27 项（26 PASS + 1 未真机行使）——docs/evidence/smoke-tui-p1-2026-10-15.md、smoke-tui-p3-2026-10-16.md |
| SC-002 治理测试语义不变 | ✅ | TestApprovalExclusiveState/DenyAndSession 等迁移绿（键位升级语义同形）；新增行为全钉 |
| SC-003 全量绿+gofmt+分层 | ✅ | §1 门禁 |
| SC-004 渲染覆盖矩阵 | ✅ | T11 |
| SC-005 滚动/重排真机 | ✅ | P1 冒烟 run7（上翻不拉回、回底跟随、重排不丢） |

## 3. 裁定复核

终审（fable，全分支）已对台账 **38 条 Ruling 逐条对照实现复核：全部属实，代价陈述无一虚报**（抽查含 T4 AltScreen v2 形态、T10 接口直满足、T15 谓词等价、T16 裸键收紧、T17 仪表省略、T18 colorprofile 不 import、T21 OSC 52 留位）。终审修复波 3 项（appendLine 纪律归一 / mapping_test 去括号 / CLAUDE.md 补 x/ansi）经 scoped re-review 全部 ADDRESSED、无新破坏（commit 40134ab，回归 TestBusyGateDoesNotSplitThinking 先红后绿）。

**holds-up 例外记录**：FR-006 尾项「阅读位置尽量保持」——终审判定为 spec 软承诺（重排/内容完整性达标，两轮冒烟 resize 断言通过；阅读锚点缺失为已知 UX 缺口），裁定路由新变更。本报告将其记为**部分达标 + 已裁定路由**，不构成 fail。

## 4. 终审遗留路由（不在本变更内）

(a) resize 阅读锚点；(b) 死代码清理三件；(c) EngineExited 工具卡终态清扫；(d) **引擎 PowerShell 治理旁路（claude CLI 2.1.300）——已另立任务芯片上报用户**；另有台账 deferred minors 若干（终审全部 triage 为可带病合并，见 progress.md）。

## 5. 结论

PASS——`mulix state set verify_result pass` 后推进 verify-pass，进入归档确认（archive_confirmation=pending，等用户裁定 A/B/C）。
