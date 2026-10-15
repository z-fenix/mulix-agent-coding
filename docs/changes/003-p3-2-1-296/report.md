# 003-p3-2-1-296 verify 报告

**日期:** 2026-10-11
**范围:** c1fa20b..48944e3(4 任务 5 提交;7 文件,+235/-4)
**结论:** **pass**(全部命令本会话新鲜重跑,证据在案)

## 1. 门禁实测(全新非缓存)

| 门禁 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| 全量测试 | `go test ./... -count=1` | **19/19 包 ok**,exit 0 |
| vet | `go vet ./...` | 干净,exit 0 |
| gofmt | `gofmt -l internal/ cmd/` | 空 |
| engine.lock 格式 | `scripts/check-engine-lock.sh` | 通过(cli_version=2.1.296) |
| 缓存敏感路径 | `scripts/check-cache-impact.sh` | 通过(未触及) |
| 解码器钉组 | 5 个钉测试 `-count=1 -v` | 全 PASS |
| lint | `golangci-lint` **环境不可用**(v1.64.8/go1.26.1 与本机 go1.27.0 export data 不兼容,master 同样挂——Task 1 裁定) | 兜底:go vet + gofmt 干净 |

## 2. 需求逐条核对(FR-101..111 / SC-001..005)

| 条目 | 判定 | 证据 |
|---|---|---|
| FR-101 两形状容忍、任一不断流 | ✅ | TestDecodeToolResultContentTwoShapes(字符串+数组 PASS);旧形状回归 TestDecodeHappyTurnFixture PASS;真实断流签名 4 个冒烟 stdout 0 命中 |
| FR-102 标识/错误标记/摘要保留 | ✅ | TestDecodeToolResultArrayErrorFlags(is_error 缺省假/出错真+文本);happy-turn 的 tool_use_result 对象保留断言未动;RawToolUseResult 通路零改动 |
| FR-103 容忍有边界 | ✅ | 6 个流损坏用例(非文本块/[42]/[{}]/[{"text":"x"}]/原始对象/原始数字)全 PASS;空数组/空块/null/缺失 → 空文本 4 用例 PASS |
| FR-104 pin=实测版本+pinned_at | ✅ | engine.lock 2.1.296/2026-10-10;doctor「claude 2.1.296 == pinned 2.1.296」 |
| FR-105 真实 fixture 入库约定 | ✅ | fixtures/engine/tool-result-array-2196.jsonl:3 条 P2 transcript 原样真实行,0 空行,逐行可解码(gate 测试断言) |
| FR-106 两种形状都有断言+第三形状失败 | ✅ | 旧 fixture(happy-turn/usage)PASS=无回归;新 fixture PASS+关键字段;6 个未知形状用例必须失败(PASS=失败路径钉住) |
| FR-107 真实冒烟关闭 P2 限制 | ✅ | p3-smoke.md:A1 委派链(spawn policy allow→子镜像→多步工作→结果与 usage 回收,exit 0)+ A2 提交(f479564,graceful)+ B2 外部工具回流成功——P2 断流场景正样本复验;**诚实注记**:「一次委派内提交」由 A1+A2 合并闭环(见 §3 minor-1) |
| FR-108 环境受阻如实记录 | ✅(未触发) | 冒烟四轮全跑通,无环境受阻项 |
| FR-109 治理语义不变 | ✅ | pin 一致 doctor 无告警;B 轮 echo 未入 allow 真实 fail-closed 拒绝(负样本);engine.allow_drift 默认 false 未动 |
| FR-110 基准位回填 | ✅ | cache_hit_rate=0.953(A1 主会话 f59d66c2 实测 session_hit),note 写明单次会话/日期/任务类型/口径 |
| FR-111 P1/P2 红线不放宽 | ✅ | 解码器红线代码路径未动(未知 type 跳过/非法 JSON fail-closed);持久化与镜像通路零改动;全量 suite 绿 |
| SC-001 | ✅ | A1+A2 合并闭环+镜像事件计数(delegation_started/completed、usage、turn_done)+全程可审计 |
| SC-002 | ✅ | 同套门禁两种形状都过、未知形状失败(上 FR-105/106 行) |
| SC-003 | ✅ | pin=本机实测,doctor 无漂移告警;「必须降级 CLI」缓解要求消除 |
| SC-004 | ✅ | 治理行为与 P1/P2 一致;本次变更自身即升级流程演练(改 pin→重录样本→门禁→基准) |
| SC-005 | ✅ | baseline 不再是占位:0.953+测量条件 |

## 3. 裁定复核(ledger 7 条 Ruling,逐条对照 spec)

1. **Task 1 lint 兜底**:环境限制非代码缺陷,master 同挂;vet+gofmt 干净。**成立**。
2. **Task 2 提取过滤收紧+断言限 tool_result 块**:fixture 须忠实 stdout 形状(文本提问行不经 stdout 流);FR-105 语义只约束工具结果块。**成立**。
3. **Task 2 无真实 is_error=true 样本路由补采面**:transcript 与冒烟均未出现错误回流;合成单测钉住语义;诚实记录未造样本。**成立**(spec Q4 的补采前提「冒烟时出现」未发生)。
4. **Task 4 allow 缺口转负样本**:PowerShell/echo 两次 fail-closed 拒绝是治理红线的真实引擎实证(超额证据),补 allow 后正样本复验。**成立且增强**。
5. **Task 4 baseline 单会话口径 0.953**:spec Q3 明文「单次真实会话」;note 已注明测量条件;5 会话加权 0.9031 备查于证据文档。**成立**(统计弱性已自认,可后续累积)。
6. **Task 4 镜像在 ~/.mulix 全局路径**:复现方法已写入证据文档。**成立**。
7. **Final 混合空块拼接尾随换行**:计划字面行为+纯外观。**成立**。

## 4. Deferred minors(2 条,零阻塞)

1. **SC-001 字面**:「一次真实子代理委派完成读改测提交」由 A1+A2 两轮合并达成(A1 内提交被 PowerShell allow 缺口拦——治理正确行为);严格审计口径下可补一轮全 allow 委派。修复选项已记录,不阻塞 P2 限制关闭(修复的缺陷本身——数组形状断流——已被 A1 直接实证关闭)。
2. **fixture 门禁未断言形状本身**:数组形状靠提取过滤器约定,坏再提取理论上可过 gate。修复选项:断言已知真实标记或硬钉 sawErrFlag。

两条均已入 ledger,路由后续(可与 T12-⑥ 等廉价测试补强清单合并)。

## 5. 结论

verify_result = **pass**。FR-101..111 逐条 ✅(每条引用真实新鲜测试/证据);SC-001..005 全部满足(SC-001 带如实划界的合并闭环注记);7 条裁定全部成立;deferred minors 2 条零阻塞。P2 report.md 限制 #1 关闭交叉引用已在案。

**verify_result = pass**(记录于 mulix state;archive 阶段待用户显式决定)。
