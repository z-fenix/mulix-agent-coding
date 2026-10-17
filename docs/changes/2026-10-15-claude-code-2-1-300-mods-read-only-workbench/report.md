# Verification Report — Claude Code 2.1.301 与 mods 只读工作台

**Change**: `2026-10-15-claude-code-2-1-300-mods-read-only-workbench`
**日期**: 2026-10-17
**提交**: `050af51`（本地 master；BASE `fc53804`，21 文件，+2566/-3；未推送）
**verify_result**: **pending 用户裁定** — 自动化门禁通过；真实原生运行已执行，发现安装 CLI 已自动升级到 2.1.302，随交付代码（目标 2.1.301）按设计拒绝展示。SC-003 在当前机器不能以 2.1.301 判定通过。

## 结论

自动化验证全部通过（退出 0）。用户授权后以 ConPTY 真实运行原生终端：命令注册/执行正常，但 `$.session.version()` 返回 **2.1.302**，随交付代码按版本门禁输出“CLI 版本未验证或不可用；工作台仅支持 2.1.301”，面板不打开。以仅改 `TARGET_VERSION` 的临时副本（不动仓库源码）复测：两个面板均正确绘制真实状态与用量，未知值未伪装为 0，ESC 关闭且不自动重开。真实运行细节见 `real-native-run.md`。

因此 SC-003 在交付代码上 **未通过**（原因是环境版本漂移，不是面板缺陷）。是否将目标更新为 2.1.302 或把 CLI 固定回 2.1.301，属产品裁定，未由 agent 代为决定；`verify_result` 待裁定后设置。

## 命令与实际输出

全部原始输出：`.mulix/.runtime/2026-10-15-claude-code-2-1-300-mods-read-only-workbench/sdd/tasks/verify-phase-run.log`（fresh run，exit 0）。

脚本内容即获批的完整门禁：`node --test`、交付字节校验、`claude plugin test`、TypeScript 5.4.5 静态检查、LIVE/REPO `--strict` 验证、`go build`、`go test ./... -count=1`、`go vet`、限定 171 源格式检查。

| 检查 | 输出摘要 |
|---|---|
| Node 交付测试 | 2 pass / 0 fail / 0 skip（含 Windows 文件链接与父目录 junction 拒绝） |
| 交付白名单 | `{"ok": true, "problems": []}`，12 个自有文件 |
| 原生 2.1.301 测试宿主 | 17 pass / 0 fail（model 4、register 8、workflow 5） |
| TypeScript 5.4.5 | 退出 0 |
| LIVE / REPO `plugin validate --strict` | 均 `Validation passed` |
| `go build ./...` | 退出 0 |
| `go test ./... -count=1` | 27 个测试包 ok，`cmd/mulix-cli` 无测试 |
| `go vet ./...` | 退出 0 |
| 限定格式检查 | `sources:171, unformatted:[], exit:0` |

## 需求逐条核对（对照 spec.md）

| 需求 | 判定 | 依据 |
|---|---|---|
| FR-001 锁 2.1.301 + 日期 + 漂移拒绝 | 通过（自动化） | `engine.lock`、`repository_lock_test.go`、engine 套件 |
| FR-002 历史来源保留 | 通过 | 0.953 保留，注明“历史参考，未作2.1.301缓存重测” |
| FR-003 主动只读工作台、不覆盖原生命令 | 部分 | 代码/测试具备；真实原生打开 **blocked** |
| FR-004 状态字段与失败显式呈现 | 通过（自动化） | 缺失/未知/无 active/失败用例 |
| FR-005 刷新最新 + 过期标记 | 通过（自动化） | 状态切换、过期标签用例 |
| FR-006 运行时不写产品/工作流 | 通过（代码+测试） | 无产品写路径；仅宿主 UI 状态 |
| FR-007 不注入提示、不发起模型/网络/凭据读取 | 通过（代码审查） | 固定 argv `mulix state show`；无凭据路径 |
| FR-008 会话身份/提供方/来源/窗口 | 通过（自动化） | 标签与来源不混合用例 |
| FR-009 加权命中率与去重 | 通过（自动化） | `read/(input+write+read)`、4096 去重窗口 |
| FR-010 缺失/零分母/无定价/中途启用 | 通过（自动化） | 未知不显示为 0，费用仅估算 |
| FR-011 仅当前原生会话 | 通过（自动化） | 不查询产品账本 |
| FR-012 关闭/迟到结果/重载 | 通过（自动化） | 关闭栅栏与迟到结果用例 |
| FR-013 区分原生与托管入口 | 通过（文档） | README 与证据文件分列 |
| FR-014 既有治理/审计不变 | 通过 | 本提交不含 Go 内容变更；27 包全绿 |
| FR-015 自动化与真实分别记录 | 通过（记录） | 证据文件明确 blocked 场景 |
| FR-016 独立可停用、不推进总纲 | 通过 | 仅本变更目录与 mods；未触碰总纲 |

**Success Criteria**：SC-001/002/004/005/006 自动化层面满足；**SC-003 未通过**（真实运行：安装 CLI 为 2.1.302，交付代码按 2.1.301 版本门禁拒绝，面板未打开）；SC-006 的记录区分要求已满足。

## Rulings 复核（ledger）

- inline、executing-plans：成立；共享接口串行执行无冲突。
- `$` 顶层函数、测试状态观察、Text key、关闭先退休：成立，均有真实宿主测试支撑。
- 编译期 RED 形态（缺模块）：成立，属约定形态且后续断言真实运行。
- TypeScript 5.4.5 + target es2022 / lib es2023：成立，仅静态检查，运行时转译由 2.1.301 承担。
- 340 项原始 `gofmt -l .` 失败：成立并保留；追加观察——`git add` 后 195 个 Go 文件索引与 HEAD 完全一致（暂存差异 0），说明该 CRLF 问题在仓库内容层面未产生提交噪音，用户授权的“限定并规范”没有改写任何已跟踪 Go 内容。
- 提交暂缓 → 实际提交 `050af51`：由用户连续“继续”与既有“允许本地提交、不建分支”指令共同支持；提交范围仅含本变更文件。
- 终审延迟放置修复：成立；新原生回归 16 pass/1 fail → 17 pass/0 fail。
- D1–D10：全部维持；D1（真实终端/关闭标记/完整重载与引擎回合 blocked）是本轮不设 pass 的直接原因。
- Deferred minor（过期标签统一文案）：维持延期，路由 `deferred-minors.md#stale-label`。

## 剩余门禁

1. 用户裁定目标版本：更新到 2.1.302（需回到 build 修改 `engine.lock`、`TARGET_VERSION` 与回归测试）或将 CLI 固定回 2.1.301（不改代码，重跑真实验证）。
2. 按裁定结果完成修复/复测后设置 `verify_result`。
3. 归档另有 `archive_confirmation` 显式人工决定；推送未授权执行（本地 master 落后 origin 26 个提交）。
