# Feature Specification: P5 deferred 清理——016 终审遗留加固

**Change**: `017-p5-deferred-cleanup`

**Created**: 2026-10-13

**Status**: Draft

**Input**: 016 台账 deferred minors(4 项中 3 项代码项路由「下变更顺手清偿」);`keys_source` 排障字段路由 P6+,不在本变更。

## 背景(问题陈述)

016 终审交付了 1 Important 修复与 4 项 deferred minor。其中 3 项为代码加固项,已在台账明示路由到下一变更;本变更即该清偿,防止「顺手项」无限期悬置。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - env 解析加固 (Priority: P1)

`ServeMCP` 的 `MULIX_KERNEL_KEYS` 解析:TrimSpace + 滤空段——env 是进程边界,脏值(尾逗号/空格)不得以幽灵键形态进入 kernel_inspect.services。

**Why this priority**: 巡检输出失真是用户可见错误。

**Independent Test**: env=`"a, b,,c,"` → services=[a,b,c]。

**Acceptance Scenarios**: 1. Given 脏 env,When 解析,Then 无空段/无首尾空格键。

### User Story 2 - 注入门控否决分支钉 (Priority: P2)

`TestBootEnvKernelKeys` 补「keys 非空 + 注册面无 mulix 条目」分支——`len>0 && hasMulixRegistryEntry()` 的第二个合取项单独否决路径无钉,回归删门控测试不红。

**Why this priority**: 门控是 SC-003 字节稳定防线的组成部分。

**Independent Test**: keys 非空 + Registry 无 mulix 条目 → env 不注入。

### User Story 3 - 双真源收敛 (Priority: P2)

`RegisterSelfRefTools(standard bool, …, profile, …)` 允许 standard=true + profile=minimal 矛盾组合(当前调用点不可达);收敛为单一 `config.ProfileMode` 参数,函数内判定 `profile == config.ProfileStandard`。

**Why this priority**: 消除矛盾面;签名即文档。

**Independent Test**: ProfileStandard → 注册两工具;ProfileMinimal → 不注册;无 bool 参数。

## Requirements *(mandatory)*

- **FR-101**: env 解析 TrimSpace+滤空段(mcpserve.go;测试钉)。
- **FR-102**: TestBootEnvKernelKeys 补否决分支(测试加固,无生产变更)。
- **FR-103**: RegisterSelfRefTools 单参数化(profile 唯一真源);mcpserve.go 调用点同步;既有测试同步(016 形态退役)。
- **FR-104**: 全量门禁绿;零第三方新增。

## Success Criteria *(mandatory)*

- SC-001: 脏 env 解析产出干净 services。
- SC-002: 门控双合取项均有钉。
- SC-003: RegisterSelfRefTools 无矛盾组合面。
- SC-004: 全量门禁绿。

## Out of Scope

- kernel_inspect `keys_source` 字段(P6+)、任何功能新增。

## Clarifications

- 纯清偿变更:范围即 016 台账路由原文,无新增裁定。
