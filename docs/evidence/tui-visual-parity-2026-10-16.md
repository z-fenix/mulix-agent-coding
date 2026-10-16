# TUI 视觉对齐拍帧对比证据（025 SC-001）

**日期**: 2026-10-16 · **方法**: 出仓 ConPTY 驱动器（C:\Users\Zhang\go\src\tui-visual，100×30，同序列：启动→你好→静默→/quit），reasonix（npm 全局，deepseek-flash）与 mulix-cli（go\bin，025 构建含 023/024/025）各拍一轮 · **成本**: mulix 回合 ≈$0.14 + reasonix ≈¥0.007

## 逐元素比对（FINAL 帧，基线=baseline-reasonix-frames.log）

| 元素 | reasonix 基线 | mulix 实拍 | 结论 |
|---|---|---|---|
| 启动横幅 | `◆ reasonix · deepseek-flash` + 提示行 | `◆ mulix · claude-sonnet-4-5` + 提示行（`输入 /quit 或按两次 Ctrl+C 退出`——内容按 mulix 实际退出方式） | 同构 |
| 用户气泡 | `  › 你好` | `  › 你好` | 同构 |
| 思考折叠 | `  ▸ 思考了 0 秒 (Ctrl+O)` | `  ▸ 思考了 0 秒 (Ctrl+O)` | 同构 |
| 助手头 | `  ◆ Reasonix` | `  ◆ mulix` | 同构 |
| 正文槽 | 全部内容 2 空格缩进 | 全部内容 2 空格缩进（段落/回执/分隔线） | 同构 |
| 回执 | `  ────…` + `  本轮  9.6K tok · in 8.1K · cached 7.6K · new 541 · reasoning 1.2K · ≈¥0.0066 · 低峰` | `  ────…` + `  本轮 46.6K tok · in 46.5K · cached 0 · new 0 · out 81 · reasoning 0 · ≈$0.1408` | 同构（人性化计数/字段序/分隔线一致；币种 $=mulix 口径；无「低峰」——mulix 无该数据） |
| 完成行 | 无「回合完成」行 | 无（025 FR-005 移除） | 同构 |
| 状态行 1 | `模型 deepseek-flash   强度 max` | ` 空闲 ` 徽章（accent 反白）+ `模型 claude-sonnet-4-5`（mulix 增模式徽章——reasonix 基线帧该处空置系其默认模式不显标） | 同构+徽章 |
| 状态行 2 | `缓存 本次命中 93.32% · 平均 64.88%  上下文 9.7K (0%)  压缩 80%  余额 ¥2.20  费用 ≈¥0.0161 · 低峰` | `缓存 本次命中 0.0% · 平均 0.0% · 费用 $0.1408` | 版式同构；上下文/压缩/余额无数据源省略（D7，024 已钉） |
| 滚动条 | 溢出时右列 █ | 同（本帧内容未溢出无滚动条） | 同构 |

## mulix FINAL 帧摘录（原样）

```
◆ mulix · claude-sonnet-4-5
对话上下文将跨轮保留。输入 /quit 或按两次 Ctrl+C 退出。
  › 你好
  ▸ 思考了 0 秒 (Ctrl+O)
◆ mulix
  你好。有什么需要我做的？当前分支 master,工作区有几个未跟踪文件(docs/changes、docs/specs 的 025
  spec 目录、driver.log、frames.log、raw.bin)。
  ─────────────────────────────────────────────────────────────────────────────────────────────────
  本轮 46.6K tok · in 46.5K · cached 0 · new 0 · out 81 · reasoning 0 · ≈$0.1408
❯
 空闲                                                                         模型 claude-sonnet-4-5
缓存 本次命中 0.0% · 平均 0.0% · 费用 $0.1408
```

## 环境与怪癖

- 驱动器出仓构建（tui-visual，hinshun/vt10x + UserExistsError/conpty 同源）；workdir 必须为仓根（engine.lock 按 CWD 读取）——驱动器输出文件因此落仓根，拍完即移出（引擎在其回合内观察到未跟踪文件属预期瞬态，已如实记录于其回复）。
- ConPTY 吞键怪癖依旧（10s 重发穿透）；MSYS 参数路径转换需 MSYS2_ARG_CONV_EXCL="*"（/quit 曾被转成 Git 安装路径提示词——首轮 reasonix 拍帧因此触发其 ask 卡，重拍规避）。
- 币种 $（mulix 口径）；reasonix 的「低峰/余额/上下文/压缩」字段 mulix 无数据源，按 D7 省略。

## 结论

SC-001 六元素逐项同构（横幅/气泡/思考标记/助手头+槽/回执/状态栏），视觉对齐达成。全程 mulix 代码零测试外改动；治理面零触碰。
