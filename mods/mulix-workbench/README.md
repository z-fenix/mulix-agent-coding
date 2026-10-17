# mulix-workbench

Claude Code **2.1.302** 的原生只读伴随工作台。它不替换 mulix-cli 的 Go TUI、执行循环、权限治理、审计镜像或产品会话账本。

## 本地使用

在本仓库根目录运行：

```bash
claude --version
claude --plugin-dir ./mods/mulix-workbench
```

在该原生交互会话中主动输入：

- `/mulix-status`：查看当前项目根目录的 mulix 工作流状态。
- `/mulix-cache`：查看**当前原生主会话**上下文与本插件实际观察窗口的用量。

两条命令打开同一个 Pane。面板中的工作流/原生用量按钮切换视图，刷新只读，关闭按钮或原生关闭操作离开面板。默认不自动弹出、不常驻状态带、不轮询、不发送完成通知。命令不接收参数，不从其他插件的程序式调用自动打开；正在进行的回合可由宿主排队后处理查看命令。

仅支持精确 2.1.302；既有进程可能仍是升级前版本，PATH 的版本输出不代表当前原生宿主。工作台读取 session.version 判断，不支持/未知版本只提示不可用，不放宽产品漂移治理。

开发目录的热加载询问必须由使用者独立选择；写好文件、权限模式或任务批准均不表示热加载已启用。也可使用上述显式本地 --plugin-dir，不需要插件市场、修改全局配置或安装产品 npm 依赖。

## 数字的含义

- **原生上下文**：宿主最近响应所报告的当前窗口输入/窗口大小/百分比，不是观察回合的累计 token。
- **观察窗口**：只统计本插件确实听到 start 与 complete 的原生主回合；无 start 的历史不补算，子代理不并入。来源标为 Claude Code 原生宿主，不从模型名猜测真实供应商。
- 未缓存输入、缓存写入、缓存读取、输出分别累计；命中率为缓存读取总量除以三类输入总量，输出不进入分母，不对各回合百分比取平均。
- 没有数据、缺失用量、非法计数、总输入为零分别披露；不是用零值伪装已测量。
- 同一 turnId 去重。同会话热重载由宿主持有的状态保留；新会话、/clear、resume 退休旧观察。记住最多 4096 个完成主回合 ID，达到上限后冻结窗口并显示截止，不淘汰旧 ID 后重新计数。
- **费用不可得**：目标 API 的 cost.usd 只累计已定价响应，缺少定价覆盖证明。即使金额为零或正数，也不显示为全会话估算/窗口费用或供应商账单；不额外读取定价来源或发起计数请求。
- 工作流数据来自固定 `mulix state show` 只读调用，cwd 为当前 session.root，需本根目录有 .mulix，限时 2 秒。缺少工具/active/字段、未知状态、截断输出等显示不可得；同根上次快照可保留但标明过期，换根立即丢弃旧字段。

## 只读边界

不切换 active、不推进/批准阶段、不创建或取消任务、不修改权限/配置/提示词、不发模型/HTTP/额外 token-count 请求、不读取产品账本或凭据。只修改本插件的宿主管理状态。

自己的面板命令不返回供模型读取的 text/context；宿主自身记录用户键入命令的行为仍由宿主负责。外部同名命令不覆盖，执行前再次核对所有权；工作台不是安全沙箱，也不代替 Go 宿主的 fail-closed 或先持久化后发布保证。

首批真实界面支持范围为 Windows 原生终端。无 surface/headless 或宿主无法放置 Pane 时提示不可用，不把模块加载或单元测试当作界面已显示。Pane 旁侧切到 agent 视图，不会将这里的主会话统计切换成 agent 统计。

## 验证

```bash
claude plugin validate --strict ./mods/mulix-workbench
claude plugin test ./mods/mulix-workbench
node --test scripts/check-mod-delivery.test.cjs
node scripts/check-mod-delivery.cjs ./mods/mulix-workbench "<LIVE_DIR>"
```

需要类型检查时使用已授权的 TypeScript >=5.4；开发工具不作为产品依赖。载入目标宿主后使用其生成的声明，或通过 plugin-authoring 获取精确 2.1.302 的声明，在 mod 外创建临时 tsconfig，include 该声明及 hooks/types/tests。使用 strict、noUncheckedIndexedAccess、noEmit、无 DOM/Node ambient、moduleResolution bundler、jsxFactory h / jsxFragmentFactory Fragment。TypeScript 5.4.5 的静态 target 使用 es2022、lib es2023（没有输出转译），不要直接使用它不支持的 target es2023。

`claude-code/testing` 验证钩子与绘制描述，不证明终端物理绘制或真实热重载。真实场景与阻塞证据见本仓库 `docs/evidence/claude-2-1-302-mods-workbench.md`；未通过的场景不能用 mock 通过替代。

交付检查只读取固定白名单文件及元数据，拒绝链接/未知条目和不一致字节，不复制或执行两个输入目录。宿主生成的 .claude-plugin/types 与根 tsconfig 不作为交付输入；不要把设置、日志、工具、凭据或市场文件放入此包。

## 停用

关闭 Pane 不会删除会话、清缓存或更改 engine.lock。要停止此 mod 的后续观察，使用原生宿主的插件管理能力停用，或结束此原生会话、下次启动不传该 --plugin-dir；热加载选择由使用者管理。本包不自动更改安装状态或全局设置。停用不改变既有 mulix Go 入口和产品数据。
