# 隔离工作区真实冒烟证据（FR-108 惯例）

**日期:** 2026-10-14 | **平台:** Windows 11 / Git Bash | **变更:** 无 active change（022 归档后用户指令「隔离启动，完成实际测试」）
**二进制:** `go build -o /tmp/mulix-smoke/mulix.exe ./cmd/mulix`（基线 0015c81=022 收官提交）

## 1. 环境自检（mulix doctor，隔离工作区）

- 工作区: `C:\Users\Zhang\AppData\Local\Temp\mulix-smoke\ws`（仅 engine.lock，无其他配置——缺省面）
- node: v24.12.0 ok | claude: **2.1.296 (Claude Code)** ok = engine.lock 钉定版本（零漂移）| engine.lock: pinned 2.1.296 ok

## 2. 首跑偏离与清理（如实记录）

首跑因 Bash `cd` 未跨调用持久，`mulix run` 落在**仓库工作区**执行（会话 1e46adc6…，workspace=D:\workplace\project\mulix-agent-coding，15:45:47Z）——隔离要求被破坏。处置：删除仓库根误落 doctor.json/run.log + 删除误跑会话镜像目录后纯净重跑。误跑本身亦为一次真实引擎回合（文本回复，exit graceful），未触碰仓库文件。

## 3. 隔离重跑（同一调用内 cd && run）

```
$ cd /tmp/mulix-smoke/ws && mulix.exe run "回复:好"
RUN_EXIT: 0
（stdout 尾）好 — 有什么需要我做的吗？
[引擎已退出] graceful
```

- sessions 首行: `fdb04c20… | 2026-10-14T15:48:18Z | workspace=C:\Users\Zhang\AppData\Local\Temp\mulix-smoke\ws` ——**隔离达成**
- `.mulix/bus` 不存在——事件总线缺省关，无 endpoint/jetstream 残留（020 清痕语义复验）

## 4. 镜像 1:1 核验（~/.mulix/sessions/fdb04c20…/）

events.jsonl 6 行，全 kind 覆盖单回合生命周期：`turn_started / assistant_thinking / assistant_text / usage / turn_done / engine_exited`（各 1）；伴随 cache.json + meta.json。先镜像后发布、1:1 不变量真机路径成立。

## 5. 结论

021/022 归档后的真机冒烟通过：引擎子进程拉起→stream-json 回合→镜像 1:1→graceful 收口全链真实可达；engine.lock 零漂移；无人值守 run（ask fail-closed 面）缺省配置即用。二进制为 022 收官基线构建，无未提交代码面。
