# Feature Specification: 020 总线 endpoint 会话收尾清理

**Change**: `020-bus-endpoint-session-teardown`

**Created**: 2026-10-13

**Status**: Draft

**Input**: 018 台账路由(「endpoint 残留:会话进程退出后文件留存(进程级单例总线无清理钩)——会话收尾关注点」);019 已交付 `Bus.Close()` 删 endpoint 能力,本变更核销最后一环:让收尾路径真正被调用。

## 背景(问题陈述)

019 后 `Bus.Close()` 已能删 endpoint,但 cli 的 `CloseSharedBus()`(session.go:770)**零调用点**——收尾钩是死代码:正常退出(run 完成/TUI 退出)后 endpoint 仍残留,观察面读到死端点 connect refused。异常退出(kill/crash)残留则无法靠收尾覆盖,需启动侧卫生。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 正常退出清理 (Priority: P1)

进程出口统一接线:`cmd/mulix/main.go` 在 `cmd.Execute()` 返回后调用 `cli.CloseSharedBus()`(run/TUI/doctor 等所有命令路径统一覆盖;serve 进程无桥=幂等 no-op)。正常退出后 `<ws>/.mulix/bus/endpoint` 不存在。

**Why this priority**: 死代码接活的最后一环;单点覆盖全部命令。

**Independent Test**: enable=true 跑 `mulix run` 完成 → endpoint 不存在(018 冒烟同场景对比)。

**Acceptance Scenarios**:

1. **Given** enable=true 会话完成, **When** 进程退出, **Then** endpoint 删除(幂等,无 bus 时 no-op)。

### User Story 2 - 残留卫生(异常退出覆盖) (Priority: P2)

kill/crash 残留无法靠收尾覆盖:openSession 在**门控关**时若发现 `<ws>/.mulix/bus/endpoint` 存在→删除(此时无进程持有总线,文件必为死残留);门控开时 OpenBus 原子覆盖写(018 既有,自动修复)。

**Why this priority**: 让「门控关=总线完全无痕」语义成立。

**Independent Test**: 手工预置残留 endpoint+门控关会话 → endpoint 被清。

**Acceptance Scenarios**:

1. **Given** 残留 endpoint+门控关, **When** openSession, **Then** endpoint 删除。
2. **Given** 残留 endpoint+门控开, **When** openSession, **Then** OpenBus 覆盖写新 endpoint(018 既有行为不变)。

## Requirements *(mandatory)*

- **FR-101**: main.go 进程出口接 `CloseSharedBus()`(导出已就绪;单点覆盖全命令;serve 幂等 no-op)。
- **FR-102**: openSession 门控关分支:残留 endpoint 删除(错误忽略——卫生面不阻断启动)。
- **FR-103**: 全量门禁绿;零第三方新增。

## Success Criteria

- SC-001: 正常退出后 endpoint 不存在(集成钉:enable=true 会话→CloseSharedBus→文件消失)。
- SC-002: 门控关+残留→清理(单元钉)。
- SC-003: 全量门禁绿。

## Out of Scope

- 观察面行为变化(018/019 fail-visible 已就绪)、JetStream 数据目录清理(Storage 归 010/012 形态)。

## Assumptions

- CloseSharedBus 导出已存在(session.go:770),main.go 可 import cli(main 已 import)。
- serve 进程:busByWS 空→CloseSharedBus 空转 no-op(015 裁定 serve 无桥——天然安全)。

## Clarifications

- clarify 跳过理由:范围即 018 路由项的死代码接线;两处修法均为最小形态(main 单点+门控关分支卫生删除),无新裁定。
- **边缘认领**(计划评审轮 2026-10-13):同工作区跨进程门控配置分歧时(gate-off 进程删掉 gate-on 进程活总线的 endpoint),观察面失去发现入口——低概率边缘(同工作区+配置分歧+观察中途),不代码防护,一句话认领。
