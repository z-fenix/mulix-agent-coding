# mulix

mulix 是一个 Go 实现的 coding agent 宿主：直接拉起 claude CLI 子进程（stream-json + 控制协议驱动引擎），负责回合间的会话权威镜像、权限治理与缓存治理三层。

## 快速开始

mulix 尚在开发中（当前处于 P1 阶段）。设计规格与各 change 的需求文档见 [docs/specs](docs/specs)；P1 的实现计划见 [docs/changes](docs/changes)。

```bash
go build ./...   # 构建检查
go test ./...    # 测试
```
