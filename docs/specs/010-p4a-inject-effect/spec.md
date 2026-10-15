# Feature Specification: P4a 内核升级——事件总线、inject 依赖声明与 effect 可逆注册

**Change**: `010-p4a-inject-effect`

**Created**: 2026-10-11

**Status**: Draft

**Input**: User description: "P4 = Go 复刻 DeepSeek Harness(dsh)功能;按 dsh 实现逻辑修订红线;分期保持推荐(P4=a 内核升级 + b 核心插件化 + c 入口与配置;P5=能力面)。本变更为 P4a。"——内核升级是 P4 的地基。

## 背景(问题陈述)

用户裁定(2026-10-11):mulix 调整架构,对齐 **DeepSeek Harness(dsh)** 的「一切皆插件」架构,用 **Go 复刻其功能**;红线按 dsh 实现逻辑修订;引擎保持可配置(本阶段默认 claude CLI 适配器,后续插件替换)。

P4 全程序分三期:**P4a 内核升级**(本变更)→ **P4b 核心插件化**(agent loop/llm 适配器/镜像与治理插件化 + 镜像流→总线订阅桥)→ **P4c 入口与配置**(HTTP/SSE 入口插件、profiles)→ **P5 能力面**(fork/replay/search、PTC、tool-cordis 自指)。

现状:`internal/kernel`(P2 交付的 Go 版 cordis 内核)仅有层级 Context 作用域、Provide/Service 注入、Mount 回滚、Dispose 级联——**缺少 dsh 的三大核心机制**:①类型化事件总线(5 种分发模式);②inject 声明式依赖(加载顺序由服务依赖表达);③effect 可逆注册(注册即副作用,卸载自动撤销)。同时两条 P1 红线与 dsh 模式冲突,需按裁定修订(见 FR-106/107)。

dsh 参照(2026-08-13 开源,MIT,TypeScript):Cordis 五概念=插件/上下文(服务容器 `ctx.<key>`)/inject 声明依赖/类型化事件(emit、waterfall(环绕中间件,不调 next() 即短路)、parallel、serial、bail(停在首个返回值))/注册是可逆副作用;核心包=`core/session`(append-only 日志)、`core/tools`、`core/agent`+`core/agent-loop`(loop 本身是插件)、`llm/llm`(适配器接缝)、profiles/bundles 配置层。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 类型化事件总线 (Priority: P1)

插件(或核心组件)经 `ctx.Events()` 获得类型化事件总线,按五种分发模式发布/订阅:emit(独立广播)、waterfall(环绕中间件,短路语义)、parallel(并发)、serial(顺序)、bail(首个非零返回停)。订阅是可逆副作用(经 effect 注册,卸载自动注销)。发布是同步内存语义(**不落盘**——持久化真相源仍是 sessionx 镜像,P4b 的订阅桥负责镜像流→总线;本变更不引入第二条持久化通道)。

**Why this priority**: 事件总线是 P4b 一切插件协作的通信基座,先于一切插件化。

**Independent Test**: 总线单测矩阵——5 种分发模式各钉语义(waterfall 短路:中间件不调 next 则后续不执行;parallel:并发全跑;serial:按序;bail:首个非零返回停);effect 卸载后订阅自动注销(再发布不再送达);类型安全(未注册事件名编译期/运行期拒绝)。

**Acceptance Scenarios**:

1. **Given** 订阅了 waterfall 事件的两个监听器(第一个不调 next), **When** 发布, **Then** 仅第一个监听器执行且其返回值成为最终值。
2. **Given** 经 effect 注册的 serial 订阅, **When** 所在插件作用域 Dispose, **When** 再发布, **Then** 该订阅不再收到事件,其余订阅不受影响。
3. **Given** bail 模式多订阅者, **When** 发布且第二个订阅者返回非零, **Then** 第三个订阅者不执行。
4. **Given** 事件总线上的一切注册, **When** 作用域 Dispose, **Then** 注册全部撤销、总线可安全继续服务其他作用域。

### User Story 2 - inject 声明式依赖 (Priority: P2)

插件以 `Inject() []string` 声明其依赖的服务键;挂载顺序由依赖图解析(拓扑序),缺依赖的插件按策略处理(fail-closed 拒挂 + 明确错误,与 mulix 治理气质一致);循环依赖检出并报错。

**Why this priority**: 插件化后手动启动编排不可维护,依赖声明是插件组合的秩序来源。

**Independent Test**: 三插件 A(无依赖)、B(inject A)、C(inject B)乱序 Mount——挂载顺序自动 A→B→C;C 依赖缺失键 → 拒挂且错误指名缺键;A↔B 循环 → 报循环依赖错误。

**Acceptance Scenarios**:

1. **Given** 三个插件乱序提交 Mount, **When** 依赖解析, **Then** 按拓扑序依次 apply,后者的 ctx 中可获得前者提供的服务。
2. **Given** 插件声明依赖 "llm" 而无人提供, **When** Mount, **Then** 拒挂,错误信息含缺失键名(不静默跳过)。
3. **Given** 两个插件互相 inject 对方, **When** Mount, **Then** 报循环依赖错误(指名环)。

### User Story 3 - effect 可逆注册 (Priority: P2)

`ctx.Effect(fn func() (undo func(), err error))` 把「注册」建模为可逆副作用:apply 内的注册(未来:工具注册、事件订阅、入口监听)一律经 effect 登记,作用域 Dispose 时逆序执行 undo。内核自身的事件订阅即首个 effect 消费者。

**Why this priority**: 「注册是可逆副作用」是 dsh「一切皆插件」的机制保障;P4b 全部插件化改动都依赖它先行。

**Independent Test**: 多个 effect 注册 → Dispose → undo 按注册逆序执行;某个 effect 的 fn 返回错误 → Mount 整体回滚(已执行的 undo 逆序补偿)。

**Acceptance Scenarios**:

1. **Given** 三个 effect 依次注册(A/B/C), **When** Dispose, **Then** undo 执行顺序为 C/B/A(逆序)。
2. **Given** 第二个 effect 的 fn 返回错误, **When** Mount, **Then** 挂载失败,第一个 effect 的 undo 已被逆序补偿,作用域恢复挂载前状态。

### User Story 4 - 红线修订与宪法落档 (Priority: P2)

两条 P1 红线按 dsh 实现逻辑修订并入档(可 grep 可核):①kernel「不做事件发布/不设第二通道」→「事件总线为内存订阅层;镜像 events.jsonl 仍是唯一持久真相源,持久化不经总线」;②「工具注册期冻结」→「插件注册 effect 可逆;引擎可见清单在回合开始时冻结快照」(快照机制 P4b 落地,本变更只修订红线文本与 kernel 能力)。

**Why this priority**: 红线是后续全部 P4b/P4c 工作的依据;不落档则插件化与红线互相矛盾。

**Independent Test**: grep kernel 包 doc 与宪法/文档中的红线文本,新语义在案;旧禁令文本无残留。

**Acceptance Scenarios**:

1. **Given** kernel 包 doc 与 docs/ 中的红线陈述, **When** grep「不做事件发布」「注册期冻结」, **Then** 旧禁令已替换为修订后语义(标注修订来源:009→010,用户裁定 2026-10-11)。

## Edge Cases

- 事件监听器自身 panic 时怎么办?——emit/waterfall/serial/bail:panic 沿发布调用栈传播(发布方责任,与 Go 惯例一致);parallel:panic 被 recover 并作为该订阅的错误结果返回(并发面不能炸整辆总线)。设计期细化。
- 同一事件同一监听器重复订阅时怎么办?——允许(多次注册=多次送达),由 effect 注销配对;不做去重(语义留给插件)。
- inject 依赖声明引用自身时怎么办?——按循环依赖处理(自环报错)。
- 同名服务重复 Provide 时怎么办?——覆盖并留痕(返回旧实例/报错,二选一归设计;fail-closed 倾向报错)。
- 多行/嵌套作用域的事件可见性时怎么办?——事件总线按作用域树继承(子作用域可见父订阅),与 Service 查找同构;细节归设计。
- 既有 subagent 对 kernel 的使用(仅 Context/Provide/Dispose)时怎么办?——纯增量升级,既有 API 不破坏(编译期保证);subagent 迁移到新机制属 P4b。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-101**: kernel MUST 提供类型化事件总线:五种分发模式 emit/waterfall/parallel/serial/bail,语义对齐 dsh(waterfall 不调 next 即短路;bail 首个非零返回停);事件名类型化(未声明事件名拒绝注册/发布)。
- **FR-102**: 事件订阅 MUST 为可逆副作用(effect 注册),作用域 Dispose 自动注销。
- **FR-103**: 插件 MUST 支持 `Inject() []string` 声明式依赖;挂载按依赖拓扑序解析;缺依赖拒挂(错误含键名);循环依赖报错(含环路径)。
- **FR-104**: `ctx.Effect` MUST 建模可逆注册:Dispose 逆序 undo;effect fn 错误 → Mount 整体回滚(已执行 undo 逆序补偿)。
- **FR-105**: 既有 kernel API(Context/Provide/Service/Mount/Dispose)MUST 零破坏(既有 subagent 消费方编译期不破)。
- **FR-106**: 红线修订落档:①kernel 事件通道禁令 → 「总线=内存订阅层,镜像为唯一持久真相源,持久化不经总线」;②「注册期冻结」→ 「effect 可逆 + 回合开始冻结快照(机制 P4b)」;修订标注来源(用户裁定 2026-10-11,变更 010)。
- **FR-107**: 零第三方依赖(leaf 级红线保持——dsh 的 cordis.js 不可引入,Go 复刻);分层矩阵登记更新(kernel 仍为 leaf)。
- **FR-108**: 事件总线不落盘:持久化唯一真相源仍是 sessionx 镜像(本变更不建立镜像流→总线桥——属 P4b)。
- **FR-109**: 全量门禁绿;新机制单测矩阵覆盖(5 分发模式语义×边界、inject 拓扑/缺键/循环、effect 逆序/回滚)。

## Key Entities *(include if feature involves data)*

- **事件总线(Event Bus)**: 类型化发布/订阅;5 分发模式;订阅可逆;作用域树继承。
- **inject 声明**: 插件的依赖键列表;拓扑序挂载解析器。
- **effect**: 可逆注册原语(fn→undo);Dispose 逆序补偿;Mount 回滚语义。
- **修订后红线**: 总线=内存层;镜像=持久真相源;可逆注册+回合冻结快照(双层)。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 5 分发模式单测矩阵全绿(语义逐模式钉死,含短路/并发/顺序/停止边界)。
- **SC-002**: inject 三场景测试全绿(拓扑序/缺键拒挂/循环报错)。
- **SC-003**: effect 两场景测试全绿(逆序 undo/Mount 回滚补偿)。
- **SC-004**: 红线修订落档且旧禁令文本无残留(grep 可核)。
- **SC-005**: 全量门禁绿;既有 subagent/kernel 消费方零回归;零第三方依赖。

## Assumptions

- 「对齐 dsh」= 架构模式对齐(Go 复刻),非代码/依赖移植(cordis.js 为 TS,不引入——leaf 级零依赖红线保持)。
- 事件总线为同步内存语义;异步分发(parallel 内部 goroutine)的生命周期由作用域 Dispose 收口。
- 5 分发模式的精确语义以本 spec 验收场景 + 设计文档钉死为准(设计阶段对齐 dsh 参照语义,如 waterfall 的 next 契约)。
- 红线修订为用户 2026-10-11 明示裁定(「按 dsh 实现逻辑修订」),范围限上列两条;其余 P1/P2 红线(先持久化后发布、1:1 镜像、fail-closed)不动。

## Out of Scope

- P4b 全部内容:agent loop 抽取与插件化、llm.Provider 接缝与 claude CLI 适配器、镜像流→总线订阅桥、引擎可见清单冻结快照机制、主流程插件化接线、工具注册改造。
- P4c:HTTP/SSE 入口插件、profiles/bundles/运行模式。
- P5:fork/replay/search、PTC、tool-cordis 自指工具。
- watcher/治理三层的任何行为变更(它们的插件化属 P4b)。

## Clarifications

### Session 2026-10-11

- 跳过理由:四故事边界清晰,潜在歧义已归设计或在 Edge Cases 裁定(用户同意跳过)。
- Q(设计阶段裁定):事件总线形态? → A: 用户裁定**直接嵌入 NATS 含持久化**——自研内存总线方案作废;依赖红线修订(NATS 为经批准例外);kernel 经小接口消费保持零依赖;JetStream 持久化与镜像 1:1 审计面职责分立(§2.5)。
