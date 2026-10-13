# 018-p4c-eventbus-production-wiring 真机冒烟证据(FR-108 惯例)

**日期:** 2026-10-13 | **平台:** Windows 11 / Git Bash | **引擎:** claude-sonnet-4-5(经 engine.lock 钉定)
**构建:** `go build -o mulix.exe ./cmd/mulix`(f65053c 之后含 T4)

## 冒烟 1:enable=true 完整回合 + mulix events 实时观察(全链)

- 工作区临时目录;`mulix.toml` 仅含 `[eventbus] enable = "true"`。
- 终端 A:`mulix run "只回复两个词:你好"`(真实引擎回合)。
- 终端 B:`mulix events`(外部观察面,独立进程)。
- **观察面实时收到 6 行全链事件**(time.utc kind payload 摘录):

```
2026-10-13T09:23:08Z turn_started {"ev":{"seq":1,"session_id":"093f89b0...","pinned_model":"claude-sonnet-4-5",...}
2026-10-13T09:23:15Z assistant_thinking {"ev":{"seq":2,"turn":1,"thinking":"The user is asking me to reply...
2026-10-13T09:23:15Z assistant_text {"ev":{"seq":3,"turn":1,"text":"你好"},"kind":"assistant_text"}
2026-10-13T09:23:15Z usage {"ev":{"seq":4,...,"cost_usd":0.0123312,"hit_rate":0.99966...},"kind":"usage"}
2026-10-13T09:23:15Z turn_done {"ev":{"seq":5,"turn":1,"subtype":"success","is_error":false,"duration_ms":7038,"result_text":"你好"}}
2026-10-13T09:23:16Z engine_exited {"ev":{"seq":6,"code":0,"reason":"graceful"},"kind":"engine_exited"}
```

- 终端 A 回合结果:`你好` + `[引擎已退出] graceful`;exit 0。
- 观察面退出:timeout 发 SIGTERM → NotifyContext → 干净退出(无 panic/悬挂)。

## 冒烟 2:SC-001 缺省关(全新工作区,无 [eventbus] 配置)

- 全新工作区 + engine.lock,`mulix run` 正常回合(exit 0)。
- `.mulix/bus/` **不存在**(endpoint 缺席)——SC-001 PASS:门控默认关,行为与 017 逐字节一致。

## 已知行为(如实记录)

- **endpoint 残留**:会话进程退出后 `<ws>/.mulix/bus/endpoint` 文件留存(进程级单例总线无清理钩)。观察面读残留 endpoint → connect refused → fail-closed 报错(冒烟 1 前置尝试实证:`events: connect ... actively refused`)。fail-visible,不静默;残留清理属会话收尾关注点,不在本变更。

## 红线核验

- 观察面纯订阅:冒烟全程零写入(events.out 为 shell 重定向,非观察面行为);镜像 events.jsonl 1:1 不受影响。
- 先持久化后发布:桥 Emit 路径零触碰(本变更只翻接线/加订阅侧)。
