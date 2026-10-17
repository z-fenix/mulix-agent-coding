# Verification Report — Claude Code 2.1.302 与 mods 只读工作台

**Change**: `2026-10-15-claude-code-2-1-300-mods-read-only-workbench`
**日期**: 2026-10-17
**提交**: `050af51`（初始交付）、`3130979`（首轮验证记录）、`1fca68e`（2.1.302 重定向修复）；本地 master，未推送
**verify_result**: **pass** — 自动化门禁与真实原生运行均通过。

## 结论

第 1 轮真实验证发现安装 CLI 已自动升级为 2.1.302，交付代码按 2.1.301 门禁 fail-closed 拒绝，SC-003 不通过；按 `verify-fail` 回到 build。用户裁定“更新到 2.1.302”后完成 TDD 修复。第 2 轮全部自动化门禁退出 0，真实原生终端中两个面板均打开、刷新并显示当前数据，ESC 正常关闭。第 1 轮的失败记录保留，不抹去。

## 命令与实际输出

原始日志：`.mulix/.runtime/<change>/sdd/tasks/` 下 `retarget-lock-red.log`、`retarget-native-red.log`、`retarget-native-green.log`、`retarget-verify.log`、`verify-phase-run-2.log`。

| 检查 | 第 2 轮结果 |
|---|---|
| Node 交付测试 | 2 pass / 0 fail |
| 交付白名单（REPO ↔ LIVE 12 文件） | `{"ok": true, "problems": []}` |
| 原生 2.1.302 测试宿主 | 17 pass / 0 fail（model 4、register 8、workflow 5） |
| TypeScript 5.4.5 静态检查 | 退出 0（仍用已安装的 2.1.301 声明；运行时由 2.1.302 转译） |
| LIVE / REPO `plugin validate --strict` | 均 `Validation passed` |
| `go build ./...` | 退出 0 |
| `go test ./... -count=1` | 27 个测试包 ok |
| `go vet ./...` | 退出 0 |
| 限定格式检查 | `sources:171, unformatted:[], exit:0` |

TDD：锁回归 RED `want 2.1.302, got "2.1.301"`；原生套件 RED 7 pass/10 fail（夹具先行改为 2.1.302，命令不打开、观察不计数）；实现后 GREEN 17/17。

## 真实原生运行（ConPTY，140×45）

入口：`claude.exe --plugin-dir ./mods/mulix-workbench`，用户授权后由 ConPTY（隔离 venv 内 pywinpty）驱动。详细记录见 `real-native-run.md` 与 `docs/evidence/claude-2-1-302-mods-workbench.md`。

- `/mulix-status`：面板打开，先显示“正在读取工作流 / 不可得”，随后刷新为 `阶段：build`、`验证结果：pending`、`数据状态：当前`。
- `/mulix-cache`：面板打开，标注提供方与原生会话来源（“不是产品会话账本”）、观察窗口限制；未知计数显示“尚未观察”，命中率“不可计算”，费用“不可得”。
- ESC 关闭且不自动重开；Ctrl-C 退出正常。
- 随交付代码未再出现版本拒绝。

## 需求逐条核对（对照 spec.md）

| 需求 | 判定 | 依据 |
|---|---|---|
| FR-001 锁 2.1.302 + 日期 + 漂移拒绝 | 通过 | `engine.lock`、`repository_lock_test.go`、engine 套件 |
| FR-002 历史来源保留 | 通过 | 0.953 保留，注明“历史参考，未作2.1.302缓存重测” |
| FR-003 主动只读工作台、不覆盖原生命令 | 通过 | 真实原生打开/刷新/关闭 + 17 项宿主测试 |
| FR-004 状态字段与失败显式呈现 | 通过 | 真实运行 + 缺失/未知/无 active 用例 |
| FR-005 刷新最新 + 过期标记 | 通过 | 真实“不可得 → 当前”刷新 + 用例 |
| FR-006 运行时不写产品/工作流 | 通过 | 无产品写路径；仅宿主 UI 状态；真实运行后状态未变 |
| FR-007 不注入提示、不发起模型/网络/凭据读取 | 通过 | 固定 argv `mulix state show`；无凭据路径 |
| FR-008 会话身份/提供方/来源/窗口 | 通过 | 真实面板标签 + 用例 |
| FR-009 加权命中率与去重 | 通过 | `read/(input+write+read)`、4096 去重窗口 |
| FR-010 缺失/零分母/无定价/中途启用 | 通过 | 未知不显示为 0，费用仅估算 |
| FR-011 仅当前原生会话 | 通过 | 不查询产品账本 |
| FR-012 关闭/迟到结果/重载 | 通过 | 关闭栅栏用例 + 真实 ESC 关闭 |
| FR-013 区分原生与托管入口 | 通过 | README 与证据文件分列 |
| FR-014 既有治理/审计不变 | 通过 | 仅 engine.lock 与 mods/文档变更；27 包全绿 |
| FR-015 自动化与真实分别记录 | 通过 | 两轮证据分别记录，含未覆盖场景 |
| FR-016 独立可停用、不推进总纲 | 通过 | 仅本变更目录与 mods；未触碰总纲 |

**Success Criteria**：SC-001/002/003/004/005/006 均通过。SC-003 由真实原生运行直接证明。

## Rulings 复核（ledger）

- inline、executing-plans：成立；共享接口串行执行无冲突。
- `$` 顶层函数、测试状态观察、Text key、关闭先退休：成立，均有真实宿主测试支撑。
- 编译期 RED 形态（缺模块）：成立。
- TypeScript 5.4.5 + target es2022 / lib es2023：成立，仅静态检查。
- 340 项原始 `gofmt -l .` 失败：成立并保留；`git add` 后 Go 文件索引与 HEAD 一致，未产生 EOL 提交噪音。
- 提交暂缓 → 实际提交：`050af51`、`3130979`、`1fca68e`，均只含本变更文件。
- 终审延迟放置修复：成立。
- D1–D10：维持；D1 的真实终端场景已由本轮 ConPTY 运行部分覆盖（面板打开/刷新/关闭），窄宽布局、`/clear`、worker 热重载去重与完整引擎回合仍未覆盖，如实记录。
- 2.1.302 重定向：用户明确裁定后实施，旧目标与旧证据保留。
- Deferred minor（过期标签统一文案）：维持延期，路由 `deferred-minors.md#stale-label`。

## 仍未覆盖

- 窄/宽实际布局切换、`/clear`、worker 热重载保持/去重、停用后行为。
- 经 mulix 产品入口的完整引擎工具审批/stream 回合（本次未发起新的付费模型请求）。
- 这些不作为 pass 的替代证据，按 FR-015 记录为未覆盖。

## 下一步

归档需要 `archive_confirmation` 显式人工决定；推送未执行（本地 master 落后 origin 26 个提交）。
