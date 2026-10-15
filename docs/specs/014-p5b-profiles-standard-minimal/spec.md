# Feature Specification: P5b profiles——standard/minimal 运行模式

**Change**: `014-p5b-profiles-standard-minimal`

**Created**: 2026-10-12

**Status**: Draft

**Input**: User description: "P5b:profiles——standard/minimal 运行模式(bundles 叠加;Minimal = bash + str_replace_editor 双工具)"——dsh profiles 形态对齐;工具注册 effect 可逆的消费者。

## 背景(问题陈述)

mulix 当前只有一种运行形态(全工具集)。dsh 的 profiles 形态允许按场景选择工具组合:

- **standard**(默认):既有全工具集——行为与现状逐字节一致;
- **minimal**:仅 bash/shell + str_replace_editor 双工具——薄环境评测(基准测试/最小面验证)。

profiles 是 P4a「注册 effect 可逆」的消费者:profile 决定工具注册清单,组合根按 profile 选择。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - profile 配置与解析 (Priority: P1)

`mulix.toml` 增 `[profile]` 节:`mode = "standard"(默认)|"minimal"`;解析后经组合根选择工具注册清单。未知 mode → fail-closed 拒绝启动。

**Why this priority**: 配置面先行(组合根消费的输入)。

**Independent Test**: mulix.toml 三态(缺省=standard/minimal/非法)→ 解析断言。

**Acceptance Scenarios**:

1. **Given** mulix.toml 无 [profile] 节, **When** 解析, **Then** mode=standard(缺省)。
2. **Given** mulix.toml [profile] mode="minimal", **When** 解析, **Then** mode=minimal。
3. **Given** mode="bogus", **When** 解析, **Then** fail-closed 拒绝启动(错误含 mode 值)。

### User Story 2 - minimal profile 工具注册面 (Priority: P2)

minimal profile 下组合根只注册 bash/shell + str_replace_editor 双工具(非 mulix 内置 MCP 工具——引擎原生工具);Boot 面相应变化(mcp-config 仅含 mulix stdio 条目——权限回路需要;不含内置工具注册条件面)。

**Why this priority**: profile 的核心价值=工具面差异化。

**Independent Test**: minimal profile 启动 → 注册面仅双工具(注册表 Snapshot 断言);standard → 全工具集(行为零变更)。

**Acceptance Scenarios**:

1. **Given** profile=minimal, **When** 组合根装配, **Then** 内置 MCP 工具注册面仅含双工具(注册表 Snapshot 断言)。
2. **Given** profile=standard(缺省), **When** 组合根装配, **Then** 行为与现状逐字节一致(零变更)。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: config 增 Profile(mode 字段;缺省 standard);解析 fail-closed(未知 mode 拒绝启动)。
- **FR-102**: minimal profile 下组合根只注册 bash/shell + str_replace_editor 双工具(引擎原生——非内置 MCP);Boot 面 mcp-config 仅含 mulix stdio 条目。
- **FR-103**: standard profile 行为与现状逐字节一致(零变更)。
- **FR-104**: profile 经 kernel 服务键 `ctx.profile` 就位(011 服务键模式)。
- **FR-105**: 全量门禁绿(既有测试零回归——standard 为缺省)。

## Key Entities *(include if feature involves data)*

- **Profile**: 运行模式配置(mode: standard|minimal)。
- **注册面差异**: minimal=双工具(引擎原生);standard=全内置 MCP 工具。

## Success Criteria *(mandatory)*

- **SC-001**: 三态解析测试(缺省/minimal/非法)全绿。
- **SC-002**: minimal 注册面 Snapshot 断言(仅双工具)。
- **SC-003**: standard 零变更(既有全矩阵)。
- **SC-004**: 全量门禁绿。

## Assumptions

- minimal 工具集固定(双工具)——不引入 per-tool 配置(P5d 可扩展)。
- profile 仅影响工具注册面;权限/缓存/镜像治理三层不变。

## Out of Scope

- P5c(PTC 模式)、P5d(tool-cordis 自指)。
- per-tool 配置、动态 profile 切换。

## Clarifications

<!-- clarify 阶段在此记录 NEEDS CLARIFICATION 的问答,或记录跳过理由。 -->
