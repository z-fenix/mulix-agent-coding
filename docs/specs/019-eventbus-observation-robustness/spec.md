# Feature Specification: 019 观察面与桥健壮性——018/016 deferred 清偿

**Change**: `019-eventbus-observation-robustness`

**Created**: 2026-10-13

**Status**: Draft

**Input**: 018 终审 5 项 deferred minor(台账路由「下变更」/「观察面健壮性」)+ 016 路由 P6+ 的 keys_source 字段;全部为台账明示项,无新增裁定。

## 背景(问题陈述)

018 交付的事件总线生产接线留下五个观察面/桥健壮性缺口:桥失败静默、观察面在会话进程死亡后静默冻结、UTF-8 摘要截断 mojibake、endpoint 残留、toml 布尔报错不友好。本变更逐项清偿;另核销 016 路由的 keys_source 排障字段。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 桥失败可见 (Priority: P1)

`sharedEventBus` 的 OpenBus 失败 swallow(012 既有裁定:桥关闭如实记录)在丢弃错误前向 stderr 透出一行——用户显式 `enable=true` 却静默拿到桥关的窗口关闭。

**Why this priority**: 静默失败与「如实记录」注释不符,是诊断黑洞。

**Independent Test**: OpenBus 注入失败(非法 StoreDir)→ stderr 含「eventbus: 桥关闭」+原因。

### User Story 2 - 观察面断连可见退出 (Priority: P1)

`mulix events` 在会话进程死亡(对端 server 关闭)后不再静默冻结:禁用自动重连,断连即报错退出(fail-visible)——对端是单会话进程,死了观察面就无物可观。

**Why this priority**: 当前形态用户只能停摆到最后一次 Ctrl-C,且无任何提示。

**Independent Test**: 观察面订阅中关闭 Bus → ObserveMirrors 返回错误(非 nil)。

### User Story 3 - 摘要与字段修缮 (Priority: P2)

①200 字节截断按 rune(中文 thinking/text 摘要不 mojibake);②`Bus.Close()` 删除 endpoint 文件(进程收尾清理残留——观察面不再读到死端点);③`Enabled()` 错误信息附「TOML 需引号串形态 `enable = "true"`」(自然布尔 `enable = true` 是 toml 解码器类型错,提示指路);④kernel_inspect JSON 增 `keys_source` 字段(`"env"`=桥送达 / `"degraded"`=env 缺席降级——016 路由 P6+ 核销,运维可区分「真无服务键」与「桥缺席」)。

**Why this priority**: 观感与运维诊断项,合包清偿。

**Independent Test**: 逐项钉(截断含完整尾字符/Close 后 endpoint 不存在/错误文案/降级形态 keys_source)。

## Requirements *(mandatory)*

- **FR-101**: sharedEventBus 失败 swallow 前向 stderr 透出一行(含错误链)。
- **FR-102**: ObserveMirrors 禁用自动重连(`nats.NoReconnect()`);连接关闭/断开→返回错误(fail-visible)。
- **FR-103**: 摘要截断按 rune;`Bus.Close()` 删 `<StoreDir>/endpoint`;`Enabled()` 非法值错误文案附引号串指路。
- **FR-104**: kernel_inspect JSON 增 `keys_source`(`"env"`/`"degraded"`;016 路由核销)。
- **FR-105**: 全量门禁绿;零第三方新增。

## Success Criteria

- SC-001: 桥失败 stderr 可见。
- SC-002: 对端关闭→观察面报错退出。
- SC-003: 截断 rune 安全+endpoint 收尾清理+错误文案指路。
- SC-004: keys_source 双形态钉。
- SC-005: 全量门禁绿。

## Out of Scope

- JetStream 回放/补发、无鉴权暴露面(设计裁定)、观察面 UI 形态。

## Clarifications

- clarify 跳过理由:范围即 018 终审 deferred 台账原文 + 016 路由项;FR-102 的「NoReconnect+错退」为 018 终审建议修法(评审员明示),无新裁定。
