# Claude Code 2.1.302 / mulix-workbench 验证证据

**记录日期**: 2026-10-17
**Change**: `2026-10-15-claude-code-2-1-300-mods-read-only-workbench`
**工作区**: 主仓库 master；BASE `050af51`，修复轮改动尚未提交
**状态**: 自动化门禁与真实原生运行均通过；本条目标修订由用户裁定，前序 2.1.301 证据保留在 `claude-2-1-301-mods-workbench.md`。

## 目标修订

- 2026-10-17 真实原生运行发现安装 CLI 已自动升级为 **2.1.302**（`claude --version`、exe、package.json 一致，构建时间 2026-10-16T15:52:41Z），而 `engine.lock` 仍为 2.1.301。
- 随交付代码按版本门禁拒绝展示（`CLI 版本未验证或不可用；工作台仅支持 2.1.301`），面板不打开——fail-closed 行为符合设计。
- 用户裁定：**更新目标到 2.1.302**（记录于本 change 台账与 spec Clarifications）。
- 修复范围：`engine.lock`（cli_version/pinned_at/历史注记）、`TARGET_VERSION`、版本提示文案、插件描述/README、夹具与测试期望。协议字段、0.953 历史基线、只读边界与门禁结构均未改。

## TDD 记录

| 检查 | RED（实现前） | GREEN（实现后） |
|---|---|---|
| 根锁回归 | 退出1：`want 2.1.302, got "2.1.301"` | `ok mulix/internal/engine` |
| 原生宿主测试 | 7 pass / 10 fail（夹具 2.1.302 与旧目标不符，命令不打开、观察不计数） | 17 pass / 0 fail |
| 全量门禁 | — | `verify-delivery-task.sh` 退出0 |

原始日志：`retarget-lock-red.log`、`retarget-native-red.log`、`retarget-native-green.log`、`retarget-verify.log`。

## 真实原生运行（ConPTY，140×45）

入口：`claude.exe --plugin-dir ./mods/mulix-workbench`（用户授权验证；隔离 venv 内 pywinpty 3.1.0 驱动）。

- `/mulix-status`：面板打开，先显示“正在读取工作流 / 数据状态：不可得”，随后刷新为 `阶段：build`、`验证结果：pending`、`数据状态：当前`。未再出现版本拒绝。
- `/mulix-cache`：面板打开，显示原生主会话 ID（标注“不是产品会话账本”）、`命中率：不可计算（尚无可计算输入）`、`费用不可得：原生接口未披露定价覆盖`，未知计数显示“尚未观察有效用量”。
- ESC 关闭面板且不自动重开；Ctrl-C 退出正常。
- 工作流值与当时 `mulix state show` 一致（build/pending）。

## 环境限制与未覆盖

- 伪终端由脚本驱动，不覆盖真实人工键鼠、窄/宽布局切换、`/clear` 与 worker 热重载去重、完整引擎工具审批回合。
- 静态类型检查仍使用已安装的 2.1.301 SDK 声明（2.1.302 尚未生成 bundled 声明）；运行时转译与钩子执行由真实 2.1.302 宿主完成。
- 启动画面另有一条与本 mod 无关的用户级插件钩子错误（需要 Git Bash），不影响本工作台。
