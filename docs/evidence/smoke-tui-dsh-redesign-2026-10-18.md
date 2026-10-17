# mulix TUI dsh 重设计真机冒烟证据（2026-10-17-dsh-tui-visual-redesign Task 19，FR-108 惯例）

**日期:** 2026-10-18 | **平台:** Windows 11 企业版 / ConPTY | **基线:** 分支 2026-10-17-dsh-tui-visual-redesign @ 9b0e8cc（T1–T18 全部落地，工作树仅含未提交 docs 与既有 go.mod/go.sum 既有改动）
**二进制:** `C:/Users/Zhang/go/bin/mulix-cli-smoke.exe`（出仓构建自 `./cmd/mulix-cli` @9b0e8cc；写入管控：go/bin 镜像不受限）
**引擎:** claude CLI **2.1.301（实装）≠ engine.lock pinned 2.1.302（漂移）**——FR-016 `--allow-engine-drift` 放行（留痕入会话镜像）；漂移后果见 §3（治理 fail-closed 实证）
**工作区:** `C:\Users\Zhang\AppData\Local\Temp\mulix-smoke-dsh\ws`（独立临时目录，engine.lock 副本 + a.md/b.md）
**驱动器:** `C:\Users\Zhang\AppData\Local\Temp\tui-driver2\smoke-dsh.exe`（仓外 throwaway：conpty + hinshun/vt10x 帧重建，400ms 变帧捕获；mulix 仓零第三方红线不受影响）
**产物:** `…\mulix-smoke-dsh\out\`（rawA/rawB/rawC.bin 原始字节流、frames*.log 逐帧、driver*.log 时间线、SUMMARY.txt）

## 1. 驱动方法

沿用 023 冒烟惯例：ConPTY 140x36 启动 TUI，vt10x 仿真重建全屏帧；写键带屏幕谓词重发（sendUntil）。ConPTY 吞键怪癖复现：「一次有效写入后约 30–36s 内后续写入被吞」——v3 运行中 A3 回合提交（13s 生效）后的 F6 五连（18–48s）全部落入吞键窗，48.5s 起 F2/F3/F4 恢复可达，与 023 量化（30–36s）一致。

## 2. 场景结果（SUMMARY.txt 摘录，v3 运行）

| # | 断言 | 结果 |
|---|---|---|
| C1 | 三栏启动帧（侧栏品牌 ◆ mulix + 「会话」目；面板 变更/代理/诊断/缓存 四页签；主列横幅+编辑器+状态栏） | PASS |
| A2 | 读回合（漂移引擎）：工具卡 `读(…)` → ✓ 完成 → 回空闲（12.5s） | PASS |
| A3 | 写回合（Edit a.md）：**未经审批回路即回流结果，终止会话（fail-closed）**——治理红线实证（见 §3） | PASS（治理实证） |
| C2 | F6 聚焦侧栏 + 状态栏「焦点 侧栏」/「焦点 面板」/Esc 取焦 | **v3 FAIL（吞键窗）**；v2-lite 独立运行三连 PASS（C2-F6 聚焦侧栏+焦点名 / 聚焦面板 / Esc 取焦——SUMMARY 12/13 那轮）——两轮合证 |
| C3 | F2 关面板（页签退场、主列加宽）→ F2 恢复 | PASS |
| C4 | F4 页签轮换 变更→代理 | PASS |
| C5 | F3 关侧栏 → 开侧栏 | PASS |
| C6 | `/theme deepseek-light` → **亮底 48;5;231 铺底在场**（rawC.bin 28 处）；切回 deepseek 暗底 | PASS |
| C7 | 引擎态徽章（v3 引擎重启后自动就绪——漂移放行会话引擎正常起；v2 早前轮「引擎异常」徽章在场） | PASS |
| C9 | Ctrl+C 双击（1.2s 窗内）退出 code=0 | PASS |
| B1 | NO_COLOR：三栏结构保留（侧栏/页签/编辑器全在）+ **主题层 SGR 空集**（rawB.bin 无 38;5;/48;5;/38;2;/48;2;） | PASS |

**暗色铺底/强调 SGR 计数（rawC.bin）:** `48;5;234`（满帧铺底）×141、`38;5;111`（deepseek 蓝 accent）×176、`48;5;231`（亮底切换）×28——dsh 令牌真机在场。

## 3. 环境阻塞与治理实证（FR-108 如实记录）

**引擎漂移与治理 fail-closed**：实装 claude CLI 2.1.301 ≠ lock pinned 2.1.302。`--allow-engine-drift` 放行后：读类工具回合正常（A2）；**写类工具（Edit）回合，引擎控制流将工具结果未经审批回路直接回流 → mulix fail-closed 判定「未经审批回路即回流结果，终止会话」并终止会话**（framesC.log 原文摘录）。该行为是本仓治理红线（先持久化后发布 / 无人值守 ask 一律拒绝收口）的实证：漂移引擎的审批控制流不兼容时，mulix 宁可终止不静默放行。

**受阻场景**：审批卡真实裁决（1/y/2/a/n 键）与 diff 卡 bg 槽（48;5;22/52）真机行使**未能完成**——需 pin 版引擎（2.1.302）方可走通审批流。补偿面：审批卡键位/治理语义由单元与矩阵测试逐字节钉死（approval_test、mapping_test 治理行），diff bg 由 TestDiffRowBGSlots（SGR 字面）与矩阵行钉死；真机部分以 fail-closed 终止行的红色呈现（语义色在场）间接可见。

**后续治理建议**：engine.lock 重钉（2.1.302 → 2.1.301）或宿主升级回 2.1.302——属引擎治理决策，超出本 TUI 变更面，**提请用户裁定**（仓 precedent：1fca68e 宿主自动升级后的重钉提交）。

## 4. 驱动器侧 quirk（非 mulix 缺陷）

- ConPTY 吞键窗（30–36s）导致 v3 的 C2-F6 未行使；v2-lite 以 F6 为首键的独立运行三连 PASS 交叉证实焦点模型真机可用。
- 大写字母丢失（023 已实证，conhost VT 输入层）：本批提示词全小写规避；F 键为转义序列不受影响。
- vt10x 对 CJK 按 1 列计（真终端双列），帧右缘偶见残影——重建器伪影，非 TUI 输出；帧宽断言以 ±2 列容差通过。

## 5. 结论

TUI dsh 重设计的真机可验面全部通过：三栏布局、面板/侧栏交互、焦点可见性、明暗主题切换、NO_COLOR 退化、满帧铺底与 accent 着色的 SGR 实证；治理红线（fail-closed）在漂移引擎下实证生效。受阻面（审批裁决/diff bg 真机行使）已如实记录并给出单测补偿面与治理后续（lock 重钉提请裁定）。
