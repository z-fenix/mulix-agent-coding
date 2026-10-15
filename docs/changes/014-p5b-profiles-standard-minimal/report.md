# 014-p5b-profiles-standard-minimal verify 报告

**日期:** 2026-10-12
**范围:** 7bff768..2a4458a(5 任务 + 终审修复轮,8 提交;27 文件 +1426/-213,含 012 交叉)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **24/24 包 ok**,exit 0 |
| vet / gofmt | `go vet ./...` / `gofmt -l internal cmd` | 干净 |
| 分层自检 | `go run ./cmd/mulix lint layers` | 0 违规(llm/llm-claude/eventbus/agentloop 登记在案) |
| 行为回归 | 441 子测试(单飞/watcher/收口/e2e/Boot 护栏/serve_wiring mulix_usage 基线) | 全 PASS |
| -race / golangci-lint | 环境不可用(历次裁定同源) | 如实记录 |

## 2. 需求逐条核对(FR-101..105 / SC-001..004)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 Profile 解析 3 态 | ✅ | TestProfileModeParsing(缺省 standard/minimal/非法 fail-closed 含 mode 值) |
| FR-102 minimal 注册面 | ✅ | TestNewServerToolFilter(InMemory ListTools 协议级:全暴露 2 工具 vs 过滤后 1 工具);**终审 Critical 修复**(ServeMCP ToolFilter 接线) |
| FR-103 standard 零变更 | ✅ | 既有全矩阵零回归;serve_wiring mulix_usage 基线保持;nil ToolFilter 字节不变 |
| FR-104 ctx.profile 键 | ✅ | TestProfileServiceKey(standard 断言;终审 Important 修复补齐) |
| FR-105 门禁绿 | ✅ | 上表 |
| SC-001..004 | ✅ | 三态/minimal 面/零变更/profile 键——对应上表 |

## 3. 裁定复核(ledger 8 条 Ruling/Final 行)

1. **Task 1 字段/方法名冲突**(Mode 字段 vs Mode 方法→RawMode+Normalized)。**成立**。
2. **Task 2 ToolFilter 机制**(NewServer 冻结层过滤——评审员确认正确接缝)。**成立**。
3. **Task 3 newRegistryServer 增参数**(既有调用方零破坏)。**成立**。
4. **Task 4 e2e 面已由 T2 钉**(InMemory ListTools)。**成立——但终审发现 serve 子进程面遗漏**(Critical 修复)。
5. **Task 5/6 回归+清偿**。**成立**。
6. **Task 6 [009] events 字段还原**(判断有误→修正 ledger)。**成立**。
7. **Task 4 helper subprocess 具体路径**(评审员建议:构造 Server 而非裸注册表)。**成立**。

## 4. Deferred minors(2 条,零阻塞)

1. lastGood 顺序已修(校验移到赋值前——终审 Minor 折叠)。
2. serve_wiring mulix_usage 基线钉:standard 下不受 ToolFilter 影响(nil filter)已确认——终审 Review Focus 3 PASS。
(累积 deferred 路由不变:SC-001 补测/并发窗口/flake/.AssemblyEventBus toml/CloseSharedBus 挂点。)

## 5. 结论

verify_result = **pass**。FR-101..105 逐条 ✅;SC-001..004 全部满足;8 条裁定全部成立;终审 1 Critical(ServeMCP ToolFilter——生产拓扑工具面过滤)+ 1 Important(profile 键断言)已修复轮 TDD 闭环;2 条 Minor 零阻塞。P5b 交付:profiles(standard/minimal)运行模式 + NewServer ToolFilter + ctx.profile 服务键——行为零变更(standard 缺省)。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
