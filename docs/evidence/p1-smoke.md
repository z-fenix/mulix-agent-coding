# P1 真实引擎冒烟证据（SC-001 / 权限回路端到端）

**Change:** 001-p1-core-loop-and-cache-foundation
**Date:** 2026-10-08
**引擎:** 真实 claude CLI 2.1.291（engine.lock pin），node v24.12.0，本机
（Windows；凭据经 claude 凭据文件）。冒烟工作区 = `%TEMP%\mulix-smoke-ws`
（独立 git 仓库：README.md / calc.py / mulix.toml / engine.lock 副本）。

## 冒烟 A（修复轮 e7e7fbd：权限桥 + Boot env 项目层配置）

- **任务文本**（`mulix run`，无人值守）：
  > Smoke round: 1) read calc.py; 2) edit calc.py to add function subtract(a, b)
  > returning a - b; 3) create test_calc.py with a simple assert for add and
  > subtract and run it with python; 4) git add -A and commit with message
  > "smoke: add subtract + tests". Be minimal. Do not invoke any skills.
- **结果：真实 commit `4b58fc7` "smoke: add subtract + tests"**——calc.py 增
  `subtract`；test_calc.py 断言经 python 执行通过；git 真实提交。
- **权限回路**：Read/Write/Bash 经 mulix.toml allow 清单放行（can_use_tool →
  `mcp__mulix__permission` → serve 子进程 permission 工具 → 前台回调裁决核 →
  behavior allow）；引擎试用的其它路径 fail-closed deny（`[审批] 工具 X 需要
  授权（无人值守将 fail-closed 拒绝）` 落镜像）。
- **收口**：引擎 graceful 退出；`mulix run` exit 0。

## 冒烟 B（终评审 Important-1 修复后复测：回调地址移出 mcp-config，经 Boot Env）

- **任务文本**：同构（multiply）。
- **结果：真实 commit `3487d01` "smoke: add multiply"**——calc.py 增
  `multiply`；test_multiply.py 输出 `ok`；git 真实提交；graceful 退出 exit 0。
- **要点**：证明引擎子进程把 `MULIX_PERMISSION_CALLBACK`（Boot Env）传播给
  `mulix mcp serve` 孙进程——回调地址不进 mcp-config/指纹（SC-003 字节稳定）。

## 冒烟失败记录（修复前，留痕）

- 冒烟 4（EnginePath 修复后）：真实引擎挂 12 分钟无事件——根因 control.start
  死等 initSeen（真实引擎 init 滞后于首条消息）→ 三方死锁。修复 = 宽限窗
  （`TestStartProvisionalWithoutInit` / `TestDeferredDriftRefusal`）。
- 冒烟 5：引擎报 `MCP tool mcp__mulix__permission not found`——registry 从未
  注册 permission 工具。修复 = mulixpermission 包 + 前台回调桥。
- 冒烟 6：Edit/Write/Bash 全被 deny——`DefaultPaths` 无项目层，mulix.toml
  从未加载。修复 = 项目层路径 + config 规则接线（`TestConfigAllowRulesGateRound`）。

## 诚实限制

- TUI 通道未做真实 TTY 验证（bubbletea 管道输入仅回显终端初始化序列）；
  run 与 TUI 共用 `buildComponents` 装配与 `Controller.Submit` 控制路径。
- 冒烟产物在 `%TEMP%`（非仓库）；本文件记录哈希与任务文本以供复现。
