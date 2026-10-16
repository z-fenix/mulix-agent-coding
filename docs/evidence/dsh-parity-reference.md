# dsh 等价总纲参考基线与证据边界

所属总纲：`2026-10-15-claude-cli-hosted-dsh-parity`。

本文件记录固定比较对象及有限源码核验，不是实际引擎、供应商、平台沙箱或全量 dsh 功能验收。总纲索引全部保持 `unverified`；静态线索不能自动变成逐项参考契约或产品通过证据。

入口：[总纲路线](../changes/2026-10-15-claude-cli-hosted-dsh-parity/roadmap.md)、[完整规格](../specs/2026-10-15-claude-cli-hosted-dsh-parity/spec.md)、[初始索引](../changes/2026-10-15-claude-cli-hosted-dsh-parity/capabilities.json)。

## 1. 不随 HEAD 移动的比较对象

| 项目 | 固定值 |
|---|---|
| dsh 本地参考目录 | `C:\Users\Administrator\workplace\project\tools\deepseek-harness` |
| dsh 完整提交 | `571902028a75c64c1d767f4ae5fd099485f0ddf0` |
| dsh 根包版本 | `0.2.1-alpha.3` |
| dsh 提交时间 | `2026-10-14T21:15:15+08:00` |
| 配对 mulix 产品源码提交 | `3b309100fdf7ac5170865c334b640b003e63749d` |
| 上述 mulix 提交的引擎锁 | Claude CLI `2.1.296` |

用户于本轮澄清提供参考目录，编排者采用首次核验时干净 HEAD 固化版本；具体 SHA 不是用户另行手输指定。首次元数据核验日期为 2026-10-15，dsh 的 `git status --short` 无输出；配对 mulix 的 `internal/`、`cmd/`、模块文件和引擎锁无工作区修改。mulix 当时另有既存流程资产改动与总纲草案，不能声称整个 mulix 工作区干净。

该工作区状态是首次核验的历史快照，不是对后续会话写入的实时承诺。2026-10-16 的补充源码核验使用以下固定提交的 `git show`，不依赖当前 HEAD 或未提交内容。参考 dsh 仓库未被修改，没有执行其安装、构建、应用启动或测试脚本。

## 2. 核验类型必须分开

| 类型 | 本文件中的范围 | 能证明什么 |
|---|---|---|
| 元数据/manifest | 提交、版本、历史状态、引擎锁 | 固定比较对象及配置声明 |
| 源码/架构说明 | 下述七个文件的有限读取 | 可见代码路径或文档所述架构，不是实际运行保证 |
| 计划评审中的内存样本 | 检查总纲验收脚本与数据约束 | 仅计划的可执行性，不算任务实际 RED/GREEN |
| 总纲初始文档契约检查 | 实施日志与阶段报告另行记录 | 路线/索引完整性，不是产品功能等价 |
| 实际引擎/供应商/OS 验证 | 本总纲未执行 | 不提供任何该类通过结论 |

本总纲任务会按计划执行 Go 构建、全量回归、静态和格式检查，其结果另见阶段报告；这类回归也不自动证明真实 CLI fork、多 Provider、压缩或平台隔离已通过。

## 3. 固定 dsh 提交的有限来源

以下路径均相对于固定 dsh 仓库，读取对象为 `571902028a75c64c1d767f4ae5fd099485f0ddf0`：

| 来源 | 本轮核对结论 | 明确限制 |
|---|---|---|
| `package.json:2–3` | 根包名和 `0.2.1-alpha.3` 版本声明 | 不代表发布产物或运行实例已验证 |
| `docs/architecture.md:13–25` | 命名 profile、bundle 和有序配置层的架构说明 | 未测试插件安装、组合或 HMR |
| `docs/architecture.md:59–65,79–103` | 默认 agent-loop driver 及 turn flow 的架构说明 | 不是模型—工具循环运行证据 |
| `docs/architecture.md:118–124` | 文档明确 session log 是模型上下文来源，由 `deriveMessages()` 派生 | 未逐项核对实现或验证上下文恢复 |

这两份文件不能支撑“全部 dsh 能力已重新核验”的结论。具体源码、默认开关、平台范围和用户契约须由 B0 及有关独立 change 继续冻结。

## 4. 固定 mulix 提交的有限来源

以下路径相对于本仓库，读取对象为 `3b309100fdf7ac5170865c334b640b003e63749d`：

| 来源 | 本轮核对结论 | 明确限制 |
|---|---|---|
| `engine.lock:2` | `cli_version` 固定为 `2.1.296` | 不证明本机已安装或正在运行此版本 |
| `internal/cli/fork_search.go:13–35` | 产品 fork 命令打开源镜像并调用 `sessionx.Fork(..., atSeq)`；此命令体没有调用引擎 fork 路径 | 本次有限核验未读取 helper 内部，不能以本条单独证明具体前缀复制、lineage 或有效上下文继承 |
| `internal/engine/claudehost.go:667–672` | 参数构造器支持 `--resume`，并按 `cfg.ForkSession` 条件加入 `--fork-session` | 参数存在不等于产品 fork 已接通，也不证明任意切点或连续对话语义 |
| `internal/registry/registry.go:159–178` | `NewServer` 取构造时工具快照；之后注册不会改变该 server 的清单 | 直接证明的是每个 server 的构造快照，不是进程全局禁止注册，也不单独证明会话寿命或逐回合刷新 |
| `internal/registry/registry.go:63–82,102–109` | 注册时规范化 schema，并提供排序后的工具集合 | 不存在仅凭该文件就可推断的构造后全局注册禁令 |
| `internal/ptc/ptc.go:28–55` | 使用 Goja，过滤非 `ReadOnly()` 工具，JS 中断计时，handler 使用 `Execute(context.Background(), argsJSON)` | JS 中断不能终止正在执行的慢 Go 工具；没有将沙箱 timeout/deadline 传给该 Execute 调用，不是硬执行时限或 OS 隔离证明 |

上述为五份 mulix 文件，连同两份 dsh 文件合计七份有限固定对象核验。没有借此对其他未读取 helper、全部治理路径或生产装配作全量结论。

可复查的固定对象读取示例：

```bash
git -C "C:\Users\Administrator\workplace\project\tools\deepseek-harness" show 571902028a75c64c1d767f4ae5fd099485f0ddf0:package.json
```

```bash
git show 3b309100fdf7ac5170865c334b640b003e63749d:engine.lock
```

```bash
git show 3b309100fdf7ac5170865c334b640b003e63749d:internal/registry/registry.go
```

## 5. 尚未执行的产品验证与后续责任

- 未启动真实 Claude CLI 会话，没有验证恢复、原生 fork、自动压缩或子代理继承。
- 未发出真实供应商请求，没有多协议、流式工具或多轮继续的供应商通过证据。
- 未运行 OS 文件/网络效果探针，没有任何平台的执行沙箱通过证据。
- 未运行参考 dsh 应用或其测试，不把架构文档、manifest 和固定对象读取当作该产品已可运行的证明。
- 本总纲文档交付与初始 JSON 验收均不实现 B0 的产品能力查询服务，也不完成 B1 等子能力。

B0 与后续独立 change 应将可观察契约、默认配置、平台限制、受测提交/引擎版本、证据类型及用户批准范围逐项关联。必要的真实冒烟受环境或授权阻塞时如实记录，不以自动化模拟替代。

后续升级引擎或参考版本应追加新记录并说明哪些场景重新验证，不能覆盖这里的历史 SHA/`2.1.296`，不能将旧证据自动迁移为新版本通过。仅某个 change 已创建、获批或归档，不构成功能等价通过。

## 6. 本次总纲文档交付的验证环境

2026-10-16 使用 go1.27.1、Windows/amd64、CGO_ENABLED=0。文档契约先因路线文件缺失退出 1，三个产物创建后输出 `PASS: 16 capabilities; 31 FR; 18 logical deliveries; 6 negative checks`。Go 构建、全量 `go test ./... -count=1`（26 个测试包）和 vet 退出 0；这些结果不升级任何产品能力状态。

格式检查存在明确的环境适配：原始 `gofmt -l .` 非空，列出 130 个文件；Git 记录均为 LF、Windows 工作区均为 CRLF，用户配置 core.autocrlf=true。本轮没有批量格式化产品源码或修改配置。核对所有 Go 文件相对固定 mulix 提交无内容差异、没有未跟踪 Go 文件、且全部位于 cmd/internal 后，以单次 `git -c core.autocrlf=false archive` 导出固定对象到运行态快照进行完整 gofmt 复核，结果无输出。默认 git archive 也曾套用 CRLF，保留了该首次快照失败证据，再用上述单次参数重建；不宣称原始工作区命令无输出。

详细命令、原始非空结果、成功复核及风险决定保留在本 change 的本地任务 ledger/log；长期结论见该 change 的阶段报告。未运行 race（CGO_ENABLED=0），未以这些回归或格式复核冒充真实引擎、供应商、OS 沙箱或 dsh 运行验收。
