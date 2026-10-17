# TUI 启动语义对齐拍帧证据（027 SC-001/SC-003）

**日期**: 2026-10-17 · **方法**: 出仓 ConPTY 驱动器（同 025 方法，100×30，启动→你好→静默→/quit），mulix-cli（go\bin，027 构建）

## 启动耗时

| 指标 | 027 前（025 基线） | 027 后（本次实拍） |
|---|---|---|
| TUI 首帧 | ~33.6s（Start 阻塞等 init 30s 超时） | **1.5s**（Start 立即返回） |
| 引擎就绪（状态栏转 READY） | 同上（init 阻塞 = 就绪） | 后续帧（init 随首条 user message 流式到达） |

## 启动帧摘录（t=1.5s，横幅可见，model 空为 init 未到——refreshBanner 首个 statusTick 回填）

```
◆ mulix ·
对话上下文将跨轮保留。输入 /quit 或按两次 Ctrl+C 退出。
```

## 环境与怪癖

- 驱动器输出文件落仓根（workdir 必须为仓根——engine.lock 解析链 026），拍完即移出。
- 引擎 spawn 后 Model 未填充（init 未到）→ 横幅 `◆ mulix ·` 尾随分隔符为已知观感（025 deferred）。

## 结论

FR-001（Start 非阻塞）达成：TUI 1.5s 可见（≤3s 目标达标）。FR-003（就绪徽章）已在状态栏实现（TestStatusBar 断言三态）。SC-003 镜像 1:1 由既有治理保证（本次验证回合未含工具调用——治理路径由 control 层测试覆盖）。
