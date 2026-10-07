# mulix 引擎协议规格（engine-protocol.md）

> mulix change 001-p1-core-loop-and-cache-foundation · Task 5（原 T1.3，M1）
> **来源 CLI 版本：claude Code 2.1.291**（`init.claude_code_version`，2026-10-06 实测 pin 于 `engine.lock`）。
> 事实来源：Task 3 探针手册（scripts/probe-engine.md，含原始日志索引）+ Task 4 fixture（fixtures/engine/happy-turn.jsonl、usage.jsonl）+ 官方 agent SDK 文档（仅用于未实测分支的标注）。
> 本文档的读者假设：**不了解 claude SDK**，仅凭本文即可实现 Task 16 的 stream-json 解码器与 Task 15 的 Host 类型。

## 0. 传输总则

- mulix 以子进程方式拉起引擎，固定参数（design §7.2，含实测修订）：
  `--input-format stream-json --output-format stream-json --verbose --permission-mode <config> --setting-sources <含 user 的清单> [--model …] [--mcp-config …] [--permission-prompt-tool …] [--append-system-prompt …]`
- **stdout**：每行一个独立 JSON 事件（UTF-8，`\n` 分隔）。**stdin**：每行一个 JSON 输入消息。
- 事件大类（顶层 `type` 字段）：`system`（再分 subtype）、`assistant`、`user`、`result`、`control_request`（P1 仅观察）、`stream_event`（P1 不开启 partial messages）。
- 解码器要求（Task 16）：逐行解析；未知 `type`/`subtype` **跳过不报错**（未知字段容忍红线）；`system/informational`、`system/status`、`system/thinking_tokens` 属于噪音类事件，必须容忍并可选透传。
- fixture 事实（Task 4）：两回合在同一进程内连续送达时 `session_id` 全程不变；**`system/init` 按回合重发**（每回合一条，session_id 不变——Task 16 必须幂等处理，以首条为准）；`usage.*` 为**本回合**口径，`modelUsage.*` 与 `total_cost_usd` 为**会话累计**口径（见 §2.5）。

## 1. 输入消息（stdin，mulix → 引擎）

| 消息 | JSON 形状 | 说明 |
|---|---|---|
| 用户回合 | `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<回合输入>"}]}}` | 每行一条；连续多条 = 同进程多回合（fixture usage.jsonl 实证） |
| 控制应答 | `{"type":"control_response","response":{"subtype":"success"\|"error","request_id":"<id>","response":{…}}}` | 应答 stdout 收到的 `control_request`（P1 不主动触发；SDK-MCP 信封见 §5） |
| 中断 | （interrupt 经控制协议，P1 由 FakeHost 演练、真实引擎 Task 24 联调） | `{"type":"control_request",…subtype:"interrupt"}` 为宿主→引擎方向（观察事实待联调，不阻塞 P1） |

## 2. 输出事件（stdout，引擎 → mulix）

### 2.1 `system/init`（会话启动；**按回合重发，session_id 不变**）

```jsonc
{
  "type":"system", "subtype":"init",
  "session_id":"<uuid>",            // engine session id；同进程多回合不变（实测）
  "cwd":"<启动目录>",
  "tools":["Task","Bash","Write",…],           // 引擎内置工具名清单
  "mcp_servers":[{"name":"…","status":"connected"|"failed","source":"user"|"project"|"local"|"dynamic"|"sdk"}],
  "model":"<init 侧模型名>",         // 注意：与 assistant.message.model 可能不同（网关映射），均按不透明字符串处理
  "permissionMode":"auto"|"default"|…,         // mulix 必须显式传入（实测：auto 会绕过审批）
  "apiKeySource":"none"|…,                     // 凭据来源；none + Not logged in 见 §4 警示
  "claude_code_version":"2.1.291",             // engine.lock 校验字段（§6）
  "slash_commands":[…], "agents":[…], "skills":[…], "plugins":[…],
  "capabilities":["interrupt_receipt_v1",…],
  // 未列全的其余键（output_style、terminal_slash_commands、view_mode、fast_mode_state、per_turn_effort_active、analytics_disabled、messaging_socket_path、product_feedback_disabled 等）一律容忍
  "uuid":"<事件 id>", "memory_paths":{…}, "powershell_path":"…"
}
```

### 2.2 `system/*` 其余子类型

| subtype | 载荷 | 处置（Task 16） |
|---|---|---|
| `hook_started` | `{hook_id, hook_name, hook_event, uuid, session_id}` | 透传事件总线（TUI 展示） |
| `hook_response` | `{hook_id, hook_name, hook_event, output, stdout?, stderr, exit_code, outcome, uuid, session_id}` | 同上；注意 output/stdout 是 JSON 字符串 |
| `permission_denied` | `{tool_name, tool_use_id, message, uuid, session_id}` | **fail-closed 路径①**：映射为权限拒绝事件（§4） |
| `thinking_tokens` | `{estimated_tokens, estimated_tokens_delta, uuid, session_id}` | 噪音/进度；可选聚合 |
| `informational` | `{content, level:"warning"|…, isMeta, uuid, session_id}` | 噪音；必须容忍（fixture 实证出现） |
| `status` | `{status:"requesting",…}`（轻量状态，字段按值容忍） | 噪音；必须容忍（p3 实测） |

### 2.3 `assistant`（模型输出，分片流）

```jsonc
{
  "type":"assistant",
  "message":{
    "id":"<消息 id>",             // 同一逻辑消息可能拆成多条 assistant 事件，id 相同
    "role":"assistant",
    "content":[                    // 分片数组；P1 逐事件即整块（未开 partial）
      {"type":"text","text":"…"},
      {"type":"thinking","thinking":"…","signature":""},
      {"type":"tool_use","id":"call_<id>","name":"<工具名>","input":{…}}
    ],
    "model":"<assistant 侧模型名>","stop_reason":null,
    "usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,…} // 分片级 usage 恒 0，忽略
  },
  "parent_tool_use_id":null,       // 子代理嵌套时非空（P1 不产生）
  "session_id":"<uuid>","uuid":"<事件 id>","timestamp":"<RFC3339>"
}
```

### 2.4 `user`（工具结果回流）

```jsonc
{
  "type":"user",
  "message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"<配对 tool_use.id>","content":"<文本结果>","is_error":false}]}, // is_error 条件字段：成功时缺省（=false），被拒/出错时 true——Task 16 不得假设必有
  "tool_use_result":{"type":"create","filePath":"…","content":"…",…}, // 结构化摘要：成功时为对象、出错时为纯字符串（"按工具而异"且"按成败而异"）；容忍缺失
  "session_id":"<uuid>","uuid":"…"
}
```

### 2.5 `result`（回合终止；**同进程多回合 = 多个 result**；`usage.*` 本回合、`modelUsage/total_cost_usd` 会话累计）

```jsonc
{
  "type":"result","subtype":"success"|"error…"|"rejected",
  "is_error":false,"num_turns":1,"result":"<最终文本>",
  "session_id":"<uuid>","uuid":"…",
  "duration_ms":6575,"duration_api_ms":6510,"ttft_ms":6566,   // 示例字面量（非 fixture 实值；fixture 见 usage.jsonl）
  "usage":{                          // ⭐ Meter（Task 13）数据源；usage.* 为本回合值（modelUsage/total_cost_usd 才是会话累计）——逐 result 累加
    "input_tokens":22060,"output_tokens":43,
    "cache_creation_input_tokens":0,"cache_read_input_tokens":0,
    "output_tokens_details":{"thinking_tokens":0},
    "server_tool_use":{…},"service_tier":"standard"
  },
  "modelUsage":{"<model>":{"inputTokens":…,"outputTokens":…,"cacheReadInputTokens":…,"cacheCreationInputTokens":…,"costUSD":0.109,…}},
  "total_cost_usd":0.109,            // 示例字面量（fixture 实值见 usage.jsonl：0.111375 / 0.222565）
  "permission_denials":[             // **fail-closed 路径②**：被拒工具全记录（含 tool_input）
    {"tool_name":"Write","tool_use_id":"call_…","tool_input":{…}}
  ],
  "result_index":0                   // 同进程第几个回合（0 起）；usage fixture 实证 0→1
}
```

**Meter 口径（Task 13）**：`usage.*` 为**本回合**值（`input_tokens`、`output_tokens`（write）、`cache_read_input_tokens`（read）、`output_tokens_details.thinking_tokens`（output 内细目））；`modelUsage.*`（`inputTokens/outputTokens/cacheReadInputTokens/…/costUSD`）与 `total_cost_usd` 为**会话累计**值（fixture 实证：result#2 的 usage.in=22138 本回合 vs modelUsage.in=44198=22060+22138 累计）。**Task 13 逐 result 累加 `usage.*` 得会话计数；成本仅用末条 result 的 `total_cost_usd`（或 `modelUsage.*.costUSD`），不得对多条 result 求和（会双计）**。四类 token = uncached_input（≈input−cache_read−cache_creation，缺省回退 input）、output（write）、cache_read（read）、thinking（output 细目，单列统计）。

## 3. 权限回路（P1 契约，实测修订）

- 引擎拉起时必须**显式** `--permission-mode default`（auto 会绕过审批）+ `--permission-prompt-tool mcp__<mulix-server>__<tool>`，权限工具由 mulix 自有 stdio MCP 服务器（Task 25 registry）提供。
- 审批请求（引擎 → 权限工具，MCP `tools/call`）：

```jsonc
{"method":"tools/call","params":{
  "name":"<permission-tool 名>",
  "arguments":{"tool_name":"Write","input":{"file_path":"…","content":"…"},"tool_use_id":"call_…"},
  "_meta":{"claudecode/toolUseId":"call_…","progressToken":2}
}}
```

- 审批应答（权限工具 → 引擎，MCP content 内嵌 JSON）：

```jsonc
{"content":[{"type":"text","text":"{\"behavior\":\"allow\",\"updatedInput\":{…}}"}]}
// deny: {"behavior":"deny","message":"…"}   —— deny 分支文档来源，Task 20 联调补测
```

- **fail-closed 双路径**（无权限回调或回调不可用时，`--permission-mode default`）：引擎立即自动拒绝 → 事件流出现 `system/permission_denied`（即时）+ `result.permission_denials[]`（汇总，含 tool_input）；回合继续，`result.subtype="success"` 且 `is_error=false`。mulix 的 120s 审批超时（FR-009）叠加其上。
- fixture 对应：happy-turn.jsonl 的 tool_use→tool_result 链即一次真实 allow 回合。

## 4. 认证与漂移警示（实测）

- `--setting-sources` **必须含 user 作用域**：仅 project 时凭据不加载（实测该场景：`apiKeySource:"none"` → assistant 文本 "Not logged in · Please run /login"，`result.is_error=true`，`error:"authentication_failed"`）。注意 `apiKeySource:"none"` 本身不是充分判据（happy-turn 在用户环境凭据经网关时也报 none）——**Task 17/24 的认证失败检测按失败形态（is_error + error 字段/文本）判定，不按 apiKeySource 单字段**。decoder 需把该形态识别为认证失败（recovery_required 输入之一）。
- 环境默认 `permissionMode:"auto"`（用户全局设置）：mulix spawn 必须显式传权限模式，否则审批不发生。
- 模型命名双轨（init/modelUsage 用 claude-opus-5[1M]，assistant.message.model 用网关映射名）：一律不透明字符串，Task 28 状态栏显示 init 侧 `model`。

## 5. 控制协议信封（SDK-MCP，观察事实；P1 不依赖）

```jsonc
// 引擎 → 宿主（stdout 顶层事件，非 system 子类型）
{"type":"control_request","request_id":"<uuid>",
 "request":{"subtype":"mcp_message","server_name":"stdio","message":{"method":"initialize","params":{…,"jsonrpc":"2.0","id":0}}}}
// 宿主 → 引擎（stdin）
{"type":"control_response","response":{"subtype":"success","request_id":"<同 id>","response":{…MCP result…}}}
// 未应答时同一逻辑请求重发（实测 2 次）——**每次重发携带新的 request_id**（内部 message 相同），宿主必须按收到的信封逐条应答其 request_id；应答成功后停止。
// 开放观察项：tools/list 之后的完整 SDK-MCP 生命周期（P1 不依赖，Task 20 仅按 §3 契约实现）
```

## 6. engine.lock（版本 pin，design §7.3）

```jsonc
{
  "cli_version": "2.1.291",                    // claude --version 精确输出
  "protocol_probe": { "init_version_field": "claude_code_version" },  // Task 3 实测回填
  "pinned_at": "2026-10-06",
  "baseline": { "cache_hit_rate": 0.0, "note": "P3 基准建立后回填" }
}
```

- 启动/doctor（Task 17/29）校验 `init.claude_code_version == lock.cli_version`；不一致默认拒绝启动，`--allow-engine-drift` 显式放行并记录 trace 事件（FR-016）。
- 升级流程 = 变更 lock + 重录 fixture（Task 4 脚本复用）+ 基准对比（P3 起）。

## 7. 事件类型 → 字段清单（Task 16 自检表）

| # | 事件（type[/subtype]） | 必有字段 | 出处 |
|---|---|---|---|
| 1 | system/init | session_id,cwd,tools[],mcp_servers[],model,permissionMode,claude_code_version | ①/fixture |
| 2 | system/hook_started | hook_id,hook_name,hook_event | ⑤/fixture |
| 3 | system/hook_response | hook_id,hook_name,hook_event,output,stdout?,stderr,exit_code,outcome | ⑤/fixture |
| 4 | system/permission_denied | tool_name,tool_use_id,message | ④a/探针 |
| 5 | system/thinking_tokens | estimated_tokens,estimated_tokens_delta | ①②/fixture |
| 6 | system/informational | content,level,isMeta | ①②/fixture |
| 7 | system/status | status（"requesting" 等，容忍） | p3 实测 |
| 8 | assistant | message.id,message.content[text\|thinking\|tool_use],parent_tool_use_id | ②/fixture |
| 9 | user | message.content[tool_result{tool_use_id,content,is_error?}]（is_error 缺省=false，被拒/出错 true）,tool_use_result? | ②/fixture |
| 10 | result | subtype,is_error,num_turns,result,usage,modelUsage,total_cost_usd,permission_denials,result_index | ②/fixture |
| 11 | stream_event（P1 关闭） | event.type∈message_start/content_block_start/content_block_delta/content_block_stop/message_delta/message_stop | ③ 实测（留位） |
| 12 | control_request（观察） | request_id,request.subtype,request.message | ④c 实测（留位） |

**fixture ↔ 文档一致性**：happy-turn.jsonl 覆盖 #1#2#3#5#6#8(tool_use)#9#10 + §3 审批链；usage.jsonl 覆盖 #1#5#6#8(text)#10×2（含累计口径的 modelUsage/total_cost_usd 与本回合口径的 usage、result_index）。#4 来自探针 p4（fixture 未含 deny 路径——Task 20 补测）；#7 来自探针 p3；#11 来自探针 p3；#12 来自探针 ④c。
