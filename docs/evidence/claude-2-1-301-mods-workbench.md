# Claude Code 2.1.301 / mulix-workbench 验证证据

**记录日期**: 2026-10-17（版本锁实际更新日期为 2026-10-16）
**Change**: `2026-10-15-claude-code-2-1-300-mods-read-only-workbench`
**工作区**: 当前主仓库，master；BASE/HEAD `fc538044a5824a106238443bcb8612397c7461fd`，改动尚未提交
**状态**: 自动化功能检查及用户后续批准的主仓库限定格式门禁通过；真实交互/完整引擎验收仍待结果，**不是全量验收或归档证据**。下文原始格式失败是保留的历史结果，后续授权与复测见新增小节。

## 版本与工具来源

- 实际 CLI：2.1.301；根 engine.lock 从 2.1.296 更新到 2.1.301，识别字段与默认漂移拒绝实现未改。
- 0.953 是旧锁文件保留的历史样本，新增说明“历史参考，未作2.1.301缓存重测”；未冒称新版本缓存性能。
- SDK：由 2.1.301 生成的声明，类型检查所用 SHA-256 `3f7d179c698b0fb2652ffeca99e9220fb3abfb79410179900ce75069b6c436e3`。
- Go：go1.27.1 windows/amd64；Node：v24.19.0；npm：11.17.0。
- 用户明确批准的隔离 TypeScript 5.4.5 开发工具已安装，无生命周期脚本、全局或产品依赖修改。包源与完整性保留本 change 的私有执行台账。
- 静态检查使用 target es2022、lib es2023、strict/noUncheckedIndexedAccess/noEmit、无 DOM/Node ambient；这只是兼容 5.4.5 的不输出检查，真实运行转译由 2.1.301 承担。

## TDD 与自动化结果

全部原始输出位于本 change 的 `.mulix/.runtime/<change>/sdd/tasks/`，不是其他变更的复用日志。

| 检查 | 实际结果 | 说明 |
|---|---|---|
| 根版本锁回归 RED | 退出1：want 2.1.301, got 2.1.296 | 改锁前观察，task-1-red.log |
| 版本锁/立即与延迟漂移既有测试 | 退出0 | engine suite，未批量替换fixture版本 |
| 模型/查询 compile RED | 缺失相应被测模块，退出1 | 正确loader已就绪；Ruling说明形态 |
| 原生适配 RED | 注册预期两项、实际空列表 | no-op模块，既有9项仍通过 |
| 交付检查 RED | MODULE_NOT_FOUND | 实现 checker 前观察 |
| Node 交付测试 | 2 pass / 0 fail / 0 skip | 包含 Windows 文件链接与父目录 junction 拒绝 |
| 交付白名单/内容比较 | ok=true，problems=[] | 12个自有文件，不读/复制生成声明、设置或未知文件 |
| 原生 SDK 测试 | 17 pass / 0 fail | 终审修复后 final-fix-native.log；真实2.1.301测试宿主，不是物理终端绘制 |
| TypeScript | 退出0 | 精确版本声明 + 自有contract与源码/测试 |
| LIVE/REPO strict validate | 均退出0 | 未关闭strict；只增加非个人化项目作者归属 |
| go build ./... | 退出0 | task-5-build.log |
| go test ./... -count=1 | 27个测试包通过，cmd无测试 | task-5-go.log；每完成代码任务另有独立全量复跑 |
| go vet ./... | 退出0 | task-5-vet.log |
| 原始 gofmt -l . | 非空340条，检查门禁为失败 | 见下节，未当作通过 |

原生测试核验可见标签及实值：四类计数90/0/910/25、加权91%，原生上下文512/4096/12%刷新到768/4096/18%，观察窗口与主会话来源不混合；状态随c1/tasks→c2/verify→c3/design更新，失败标过期、换根丢旧数据。未知数据不伪装为0；定价覆盖未知不展示金额。外部命令不截获、非用户来源不打开，关闭后迟到结果不复活。

实际SDK差异与修正有台账：$辅助函数需文件顶层；测试不能用缓存别名冒调用状态API，改为观察真实已提交state读写；Text key没有进入描述树，使用可见标签查询而保留所有数据断言；自发关闭先退休本插件状态再调用宿主关闭。没有修改原生引擎或准入规则。

## 原始格式门禁的保留与边界

`task-5-fmt.log` 非空340条：170条位于另一个worktree，未继续读取/改动其源码；主工作区170条与HEAD内容在只消除CRLF后完全相同。没有为本变更批量重写这些文件，没有改变git配置。

额外只读审计 `main-tree-lf-format-audit.log` 在内存中向 gofmt 输入主仓库171个源码（含本次新测试）的LF等价文本：170个原始CRLF，LF等价未格式化数0、错误0。未创建/改写源码快照，也未把这项限定审计说成原始 `gofmt -l .` 全绿。

这是后续用户裁定前的原始门禁结果，不抹去、不冒称原命令已全绿。后续用户另行批准的范围与规范见下节；真实交互仍未通过，不能把整个变更标 verify pass。

## 用户后续格式裁定与复测

用户在明确说明范围的询问中选择“限定并规范（推荐）”：门禁限定主仓库已跟踪及本任务新增Go源，不递归他人worktree；只规范已确认与HEAD的LF内容一致的CRLF，不改语义、Git配置或他人文件。用户另选择“我来实机验证（推荐）”只是未来意图，尚未提供实际结果。

获准后，执行前重新核对所有候选文件均为当前主仓库普通源、LF等价内容与HEAD一致、LF输入本身已符合gofmt；先完成全体检查再调用已安装的格式工具。实际171个主源中规范170个，0语义内容变化、0他人worktree触碰，逐文件前/后哈希在 `authorized-scoped-format.log` 保留。Git源码差异列表未产生既有Go文件语义改动；其LF/CRLF提示反映现有配置，不作Git配置修改。

随后 `verify-delivery-task.sh` 整体退出0（`task-5-approved-scope-green.log`）：Node2/2、原生17/17、类型、LIVE/REPO严格验证、Go build/27包test/vet均过，限定171源格式检查 `unformatted:[] / exit:0`。这履行新的格式门禁，不把旧递归340项结果改写成通过。

提交方式的用户答复为“主分支提交推送”；该请求已记录，但执行者受默认分支提交前须建分支约束且未获建分支许可，没有直接提交/推送master或委托他方绕过。

## 实际产品入口：doctor（已运行）

命令：`go run ./cmd/mulix-cli doctor --redact`，退出0。`product-doctor.log` 的结构化报告为 ok=true，node/claude/engine.lock/consistency/credentials/workspace 六项均ok；明确 `claude 2.1.301 == pinned 2.1.301`。路径已掩码，凭据只报来源、不含值。

这是实际产品命令及诊断探针，不是测试替代；它只证明安装/锁一致与诊断入口可用，**不证明完整引擎初始化、工具审批、stream回合、恢复或原生界面已通过**。doctor自己的临时可写探针不是mod运行时的操作。

## 原生交互启动：真实尝试但受阻

正常环境命令：`claude --plugin-dir ./mods/mulix-workbench < /dev/null`，退出1，`native-real-start.log`：

```text
Error: Input must be provided either through stdin or as a prompt argument when using --print
```

此执行环境没有可供该命令使用的交互TTY，宿主进入打印模式；未提供模型prompt，未继续盲写斜杠命令。没有清空嵌套会话标记、关闭信任/权限检查、改全局设置或绕过控制。该尝试不是原生面板运行成功。

环境中无tmux；有MSYS script/winpty，但项目历史证据只验证了Go Bubble Tea TUI及外部临时ConPTY驱动，不是本次原生2.1.301。仓库没有可复用的已验证原生驱动；历史记录还出现过斜杠输入丢失后意外提交模型任务。因此在没有额外模型费用授权、可靠终端驱动与可绘制表面的情况下，不做盲重试或安装新驱动依赖。

## 仍未通过的真实场景

- 真实2.1.301原生终端：两个命令打开、人工刷新、原生关闭标记、窄/宽实际布局与退出。
- 实际 /clear、worker热重载保持/去重、停用后行为。后续已收到宿主明确回执：用户启用本会话热加载，指定开发目录的 mods 已加载；这证明启用/加载，不证明面板已被实际打开、刷新或完整重载去重场景已通过。
- 经mulix产品入口的完整真实引擎/工具审批/stream回合；本轮没有为此自动发起新的付费模型请求。

这些场景按 FR-015/既有FR-108习惯记为 blocked/unverified，不能以17个SDK测试、模块加载回执、单独--version或doctor通过代替。README提供用户本地启动方式，不把提供命令当实际执行成功。

## 独立终审与唯一修复轮

独立最强模型对完整自有工作区/新文件完成只读终审，原分级无Critical/Important、2项Minor。执行者按用户影响将“未放置Pane之后被宿主放置，按钮仍永远不刷新”重新分为Important；按规范未重派终审，执行一轮TDD修复。

新增真实原生回归先得到16 pass/1 fail（预期1次查询，实际0）；修复仅在用户动作时重读ui.panes，确认当前代际仍一致才将placed状态同步并继续刷新，不在绘制中写状态，不增加轮询或自动open。随后17 pass/0 fail，类型/strict/Node/Go全量/build/vet全部重跑退出0，canonical副本检查与测试通过。原始格式阻塞仍保留。

另一项Minor（过期标签统一说读取失败，实际可能无active）留作后续独立文案变更，不在修复轮扩散。终审未能验证的宿主二进制搜索顺序等逐项裁定记录在本 change 的 final-review-report 与台账；不宣称操作系统沙箱或恶意PATH/cwd防护。

## 提交、归档与下一步

按用户要求在master实现，没有新建分支/worktree。用户允许本地提交，但执行者受默认分支提交前须建分支的更高约束；没有直接提交master、没有推送、没有用别的提交作为本次证据，提交步骤仍未执行。

独立终审与唯一TDD修复轮已完成；不把执行者测试闭环改称二次独立终审通过。原始格式与真实交互阻塞未解决前，不宣称全量验收通过、不设置verify pass、不归档。审查明确覆盖实际工作区与新文件，没有拿BASE=HEAD的空提交范围冒充差异。
