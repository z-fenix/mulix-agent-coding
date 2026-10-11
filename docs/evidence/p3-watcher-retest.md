# P3 watcher 真实会话复测证据(006-t12-5-watcher-p2-smoke)

**日期:** 2026-10-11
**引擎:** 真实 claude CLI **2.1.296 (Claude Code)**(`claude --version` 原文;doctor 实测 pinned 2.1.296 == 本机,无漂移告警)
**通道:** `mulix run` 无人值守(方法对齐 P2 冒烟 C;独立 git 工作区)
**工作区:** `%TEMP%\tmp.a6HXTZXqdc\mulix-watcher-retest\`(临时区,不入仓;本文件记录任务文本/时间线/摘录以供复现)

## 复测设计(对 spec FR-102)

- **任务文本**:「用 PowerShell 工具执行命令 Start-Sleep -Seconds 25,完成后读取 CLAUDE.md 并原样汇报全部内容」——长回合保证存活窗口覆盖编辑后 ≥2 个轮询周期。
- **基线锚**:镜像 `~/.mulix/sessions/<id>/events.jsonl` 的 **mtime**。锚替换说明(spec 原文为「镜像 init 事件时刻」):**镜像事件不带时间戳**(记录=kind/seq/ev 三键);`meta.json` 的 created_at 是会话创建时刻(早于基线装载),不可作锚;`turn_started` 由泵在 Submit 发布、`start()` 完成(=watcher 基线装载)之后,发布即文件追加——故 **events.jsonl mtime ≈ 基线装载后首事件时刻**,是唯一文件来源的诚实锚。
- **编辑时点**:轮询到新会话 turn_started 后 +3s,`printf >> CLAUDE.md` 追加一行;编辑落盘时刻取文件 mtime。
- **配置**:mulix.toml(preset=acceptEdits + allow PowerShell——003 冒烟同款);engine.lock(2.1.296)。

## 时间线(三时刻,原始来源)

| 时刻 | 值 | 来源 |
|---|---|---|
| t0 基线锚(turn_started 发布) | **2026-10-11 13:36:50.606** | `stat -c '%y' events.jsonl`(轮询命中时取) |
| t1 编辑落盘 | **2026-10-11 13:36:54.043** | `stat -c '%y' CLAUDE.md` |
| t2 notice 观测 | **t1 后首个 2s tick 内(≤13:36:56)** | 镜像事件序位:notice 为 **seq=2**(先于引擎首个 assistant_thinking);stdout 第 1 行即该 notice(观测于 run 结束时收集) |

编辑时点锚验证:t1 − t0 ≈ **+3.4s**(编辑确在基线装载之后,≥1.5 个轮询周期)。

## 观测结果

- **stdout 原文首行**:`[notice] 指令文件变更: CLAUDE.md（快照保持，下会话生效）`——watcher 提醒真实出现。
- **镜像**:`events.jsonl` seq=2、kind=notice、level=info,内容同上(事件先于引擎 tool 链,证明在编辑后首个轮询周期即发布)。
- **快照语义复验**(与 P2 冒烟 C 一致):引擎读取并汇报**编辑后**内容(4 行有效),会话未中断,`mulix run` **exit 0**,graceful。

## 定论(三分支之一:**A —— 机制健康**)

1. 编辑在基线装载之后(+3.4s)、回合存活覆盖 ≥2 周期、notice 在编辑后首个 tick 出现——**watcher 在真实装配路径上工作正常**(H2 接线缺陷、H3 轮询未运行均排除)。
2. **P2 原解释不成立**:P2 冒烟 C 的 turn 14.7s 配 2s 轮询有约 7 个 tick,「窗口短未赶上」不能解释 notice 缺席。真实机制是 **H1(基线时序)**:P2 的编辑发生在 run 第 6 秒,而引擎启动耗时约 30 秒——**编辑落在 watcher 基线装载之前**,快照捕获的已是编辑后内容,无 delta 可报。这是 P1 快照语义(基线=启动时点观测)的正确行为,**不是缺陷**。
3. **有效复测的编辑时点判定标准**(供后续冒烟复用):编辑落盘时刻必须晚于镜像 turn_started 事件(=基线装载)至少 1 个轮询周期。

无缺陷路由(FR-104 未触发);单测矩阵无缺口需补(基线后编辑场景已被本复测在真实装配面覆盖)。

## 门禁

`go test ./... -count=1` 全 19 包 ok;`go vet ./...` / `gofmt -l` 干净;`git status` 仅本文件新增(代码零改动,FR-107)。
