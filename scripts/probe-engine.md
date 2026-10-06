# 引擎协议探针手册（scripts/probe-engine.md）

> mulix change 001-p1-core-loop-and-cache-foundation · Task 3（原 T1.1，M1）
> 引擎版本：claude CLI **2.1.291**（本机安装形态：npm wrapper `claude.cmd` → `node_modules/@anthropic-ai/claude-code/bin/claude.exe`）
> 探针日期：2026-10-06。原始事件流日志：`.mulix/.runtime/001-p1-core-loop-and-cache-foundation/sdd/tasks/probe-logs/`（git-ignored scratch，不入库；本手册引用的日志文件名均指该目录）。
> 与 design §7 假设冲突之处以本文实测为准（design §7.2 已预留此修订规则；本次探针触发的 §7/§9 修订见文末「差异与修订」）。

**通用环境说明**：

- 本机（Windows 11，pwsh 7.5）默认权限模式为 `auto`（用户全局设置）。mulix spawn 引擎时**必须显式传 `--permission-mode`**，否则审批回合不会发生、guard 不生效（见 ④b）。
- 绝不使用 `--dangerously-skip-permissions`（全 plan 红线）。以下所有命令均未使用它。
- `--output-format stream-json --verbose` 组合下，stdout 每行一个 JSON 事件；`\n` 分隔。
- 探针在临时目录运行；注意受限 shell 的 `%TEMP%` 与提权 shell 的 `%TEMP%` 可能不同（DSH 会话使用会话专用临时目录）。

---

## 验证点 ①：init 事件结构与版本字段

**命令**：

```powershell
claude -p "Reply with the single word ok" --output-format stream-json --verbose
```

**观察**（事件顺序；日志 `p1-stream.jsonl`）：

1. `{"type":"system","subtype":"hook_started",...}`（若配置了 SessionStart hook；无 hook 环境可能没有）
2. `{"type":"system","subtype":"hook_response",...}`
3. `{"type":"system","subtype":"init", cwd, session_id, tools[], mcp_servers[], model, permissionMode, slash_commands[], apiKeySource, claude_code_version, output_style, agents[], skills[], plugins[], capabilities[], uuid, memory_paths, powershell_path, ...}`
4. `{"type":"assistant","message":{ id, role, content:[{type,text}], model, stop_reason, usage{...} }, parent_tool_use_id, session_id, uuid, timestamp}`
5. （`system/informational` 等 meta 提示事件，可能穿插）
6. `{"type":"result", duration_ms, duration_api_ms, stop_reason, session_id, total_cost_usd, usage{ input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens, ...}, modelUsage{ <model>: {inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens, costUSD, ...} }, permission_denials[], num_turns, is_error, subtype:"success", result:"ok", ...}`

**结论**：

- 版本字段：`init.claude_code_version`（本机实测 `"2.1.291"`）——engine.lock 的 `protocol_probe.init_version_field` 即取此字段（design §7.3）。
- 会话标识：`session_id` 在**每个事件**上都有（顶层字段），result 里亦有；mulix 以 init 的 session_id 作为 engine session id。
- 权限模式回显：`init.permissionMode`（本机环境默认 `"auto"`——见 ④b 警示）。
- `mcp_servers[]` 每项 `{name, status: "connected"|"failed", source: "user"|"dynamic"|"sdk"|"local"|"project"}`——Task 25 registry 注入的 in-process 服务器应在此核对。
- usage 的缓存字段（`cache_read_input_tokens`/`cache_creation_input_tokens`）与 `modelUsage.*.costUSD` 是 Task 13 Meter 的数据源。

## 验证点 ②：assistant / user / result 事件字段

**命令**：同 ①；另让引擎触发一次工具调用并观察 `user` 事件（工具结果回传）：

```powershell
claude -p "Use the Write tool to create probe-5.txt in the current directory containing exactly: hooked write" --output-format stream-json --verbose --allowedTools "Write"
```

**观察**：

- `assistant.message.content[]` 是**分片数组**：`{type:"text",text}` / `{type:"thinking",thinking,signature}` / `{type:"tool_use",id,name,input}`；同一 `message.id` 可能拆成多个 assistant 事件（thinking 与 text 分离，各自 `timestamp`）。
- `user` 事件（工具结果）：`{"type":"user","message":{"role":"user","content":[{type:"tool_result",tool_use_id,content,is_error?}]}, parent_tool_use_id, session_id, uuid, tool_use_result:{type:"create",filePath,content,...}}`——`tool_use_result` 带结构化结果摘要。
- `result` 事件字段全集见 ①；工具回合中 `num_turns>1`。

**结论**：decoder（Task 16）按 `content[].type` 分派；`tool_use.id` ↔ `tool_result.tool_use_id` 配对；user 事件承载工具结果（Task 15 Host 的 `Event` 需覆盖 text/thinking/tool_use/tool_result/result 五类载荷）。

## 验证点 ③：`--include-partial-messages` 的 delta 事件（实测）

**命令**：

```powershell
claude -p "Count from 1 to 5." --output-format stream-json --verbose --include-partial-messages
```

**观察**（日志 `p3-partial.jsonl`，事件顺序实测）：

1. `system/hook_started`、`system/hook_response`、`system/init`、`system/status`
2. `{"type":"stream_event","event":{"type":"message_start",...}}`
3. `{"type":"stream_event","event":{"type":"content_block_start",...}}`
4. 多条 `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":...}}}`（逐 token 增量；thinking 块对应 `thinking_delta`，工具参数对应 `input_json_delta`——与 Anthropic Messages 流式 API 同名同形）
5. 完整的 `assistant/` 整块事件**仍然送达**（delta 与整块并存）
6. `stream_event/content_block_stop` → `stream_event/message_delta` → `system/informational` → `stream_event/message_stop` → `result/success`

未开启该 flag 时（①②④⑤⑥ 的全部基线运行）：无 `stream_event`，thinking 增量以 `{"type":"system","subtype":"thinking_tokens",estimated_tokens,estimated_tokens_delta}` 聚合事件出现。

**结论**：P1 的 spawn 固定集**不开启** `--include-partial-messages`（design §7.2「按需开启」；TUI 打字机效果属 P2）。decoder P1 只需处理整块 assistant 分片 + `thinking_tokens` 聚合事件；若 P2 开启，增量事件包裹在顶层 `{"type":"stream_event","event":{...}}` 中，`event.type` 复用 Anthropic 流式块类型（`message_start`/`content_block_start`/`content_block_delta`(`text_delta`|`thinking_delta`|`input_json_delta`)/`content_block_stop`/`message_delta`/`message_stop`），且**整块事件仍并行送达**——decoder 可忽略 stream_event 而不丢内容。

## 验证点 ④：控制协议 canUseTool 回合

### ④a 基线：无权限回调 + `--permission-mode default` → 自动拒绝

**命令**（stream-json 输入；stdin 一次性送入 user 消息行）：

```powershell
# stdin: {"type":"user","message":{"role":"user","content":[{"type":"text","text":"Use the Write tool to create probe-4.txt in the current directory containing exactly the text: wrote by probe"}]}}
claude --input-format stream-json --output-format stream-json --verbose --permission-mode default
```

**观察**（日志 `p4-canusetool.jsonl`）：

- assistant 发出 `tool_use`（Write）后，**没有**任何 `control_request`；随即出现：
  `{"type":"system","subtype":"permission_denied","tool_name":"Write","tool_use_id":"call_...","message":"Claude requested permissions to write to ...probe-4.txt, but you haven't granted it yet.", session_id, uuid}`
- 回合继续：assistant 解释无法写文件，`result.subtype="success"`、`is_error=false`；**且** `result.permission_denials` 收敛为一条完整记录：
  `[{"tool_name":"Write","tool_use_id":"call_cb55c99b4560418a9038f0e8","tool_input":{"file_path":"...\\probe-4.txt","content":"wrote by probe"}}]`

**结论（fail-closed 语义）**：无权限回调机制时，未授予的工具调用被引擎**立即自动拒绝**（不是挂起等待）；拒绝以两条路径同时呈现——即时 `system/permission_denied` 事件 + `result.permission_denials[]` 汇总（含 tool_input）。mulix 的审批队列不在线时引擎不会卡死，符合 FR-009 的 fail-closed 底线；decoder（Task 16）两条路径都要解析。

### ④b `--permission-prompt-tool` + stdio MCP 权限工具（canUseTool 等价契约，P1 采用）

**命令**：

```powershell
# mcp-permit.json: {"mcpServers":{"probe":{"command":"node","args":["permit-server.js"],"env":{"PERMIT_LOG":"permit-calls.log"}}}}
claude -p "Use the Write tool to create probe-4.txt in the current directory containing exactly: wrote by probe" `
  --output-format stream-json --verbose `
  --permission-mode default `
  --mcp-config mcp-permit.json `
  --permission-prompt-tool "mcp__probe__permit"
```

**观察**（日志 `p4a-stream.jsonl` / `p4c-stream.jsonl`；权限工具侧原始请求-应答日志 `p4b-permit-calls.log`）：

- CLI 先对 MCP 服务器完整握手：`initialize`（clientInfo=claude-code 2.1.291）→ `notifications/initialized` → `tools/list`。
- 工具调用触发权限回合，CLI 向 `mcp__probe__permit` 发 MCP `tools/call`（`p4b-permit-calls.log` 实录）：
  ```json
  {"method":"tools/call","id":2,"params":{"name":"permit","arguments":{"tool_name":"Write","input":{"file_path":"...\\probe-4.txt","content":"wrote by probe"},"tool_use_id":"call_194664a4d0eb4433b9b52277"},"_meta":{"claudecode/toolUseId":"call_194664a4d0eb4433b9b52277","progressToken":2}}}
  ```
- 权限**应答契约**：工具以 MCP content 返回 JSON 文本（同日志 `direction:"response"` 行）：
  ```json
  {"content":[{"type":"text","text":"{\"behavior\":\"allow\",\"updatedInput\":{\"file_path\":\"...\",\"content\":\"wrote by probe\"}}"}]}
  ```
- 应答 allow 后：文件真实写入（`probe-4.txt` 出现），`result/subtype=success, is_error=false`，`permission_denials=[]`。
- 环境警示（复测确认）：不加 `--permission-mode default` 时本机默认 `auto` 模式**直接放行、不咨询权限工具**（`p4a-stream.jsonl` 运行中 `permit-calls.log` 无 tools/call 行）——mulix 必须显式传权限模式。
- deny 分支未实测（本探针未触发 deny 应答）；按官方文档，deny 应答为 `{"behavior":"deny","message":"..."}`——**标注：文档来源，Task 20 联调时补测**。

### ④c 控制协议变体（SDK 型 MCP，观察事实，P1 不依赖）

**命令**：`--mcp-config`（`{"mcpServers":{"stdio":{"type":"sdk","name":"stdio"}}}`）+ `--permission-prompt-tool mcp__stdio__permit`，宿主在 stdin/stdout 上应答控制信封（日志 `p4d-control.txt`、`p4d-stream.jsonl`；中途一次未应答的运行在 `p4b-stream.jsonl`/`p4b-control.txt`，仅事件类型摘要）。

**观察**：

- SDK 型服务器通过**控制协议**握手：stdout 出现顶层事件（注意：**不是** `system` 子类型）
  `{"type":"control_request","request_id":"<uuid>","request":{"subtype":"mcp_message","server_name":"stdio","message":{"method":"initialize","params":{...,"jsonrpc":"2.0","id":0}}}}`
- 宿主应答 `{"type":"control_response","response":{"subtype":"success","request_id":"<同 id>","response":{...initialize result...}}}` 后 CLI 停止重发（未应答时同一 initialize 重发两次）。
- 探针宿主答了两次 initialize（CLI 重发），未继续收到 `tools/list` 请求；Write 最终报错：
  `Error: MCP tool mcp__stdio__permit (passed via --permission-prompt-tool) not found.`（`init.mcp_servers` 中该服务器 `{"name":"stdio","status":"failed","source":"sdk"}`）

**结论**：

- **P1 权限回路 = ④b**：guard（Task 20）的审批队列通过 mulix 自有 stdio MCP 权限工具对外暴露；引擎侧契约 = `tools/call{tool_name,input,tool_use_id}` → content JSON `{behavior:"allow",updatedInput}`。
- 控制协议信封（顶层 `control_request`/`control_response`、`request_id`、`mcp_message` 子类型、未应答重发）为实测事实，记录进 Task 5 协议文档；SDK 型 in-process MCP 的完整生命周期（tools/list 之后的会话）**留作开放观察项**，Task 5 文档标注 P1 不依赖。
- stream-json 模式下 canUseTool 的 fail-closed 语义由 ④a 兜底：无权限工具时未授予的工具调用被引擎直接拒绝（不是挂起等待）——mulix 的 120s 审批超时（FR-009）与之叠加。

## 验证点 ⑤：hook 回调格式（实测归档）

**命令**（项目 `.claude/settings.json`，实测所用原文见 `probe-logs` 旁 `probe-run4b.ps1` 输出）：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Write",
        "hooks": [
          { "type": "command", "command": "node -e \"...read stdin, append to HOOK_LOG...\"" }
        ]
      }
    ]
  }
}
```

```powershell
claude -p "Use the Write tool to create probe-5.txt ..." --output-format stream-json --verbose --allowedTools "Write"
```

**观察**（hook stdin 原始捕获 `p5-hook-calls.log`；流事件 `p5-hook-stream.jsonl`）：

- hook 进程 stdin 收到**一行 JSON**（实测原文）：
  `{"session_id":"5a50e447-...","transcript_path":"C:\\Users\\Zhang\\.claude\\projects\\...\\5a50e447-....jsonl","cwd":"...\\mulix-probe5","prompt_id":"cce395d9-...","permission_mode":"auto","effort":{"level":"high"},"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"...\\probe-5.txt","content":"hooked write"},"tool_use_id":"call_8f64802c2a68477284360c3d"}`
- hook stdout 可返回裁决/上下文 JSON（如 `hookSpecificOutput.additionalContext`——SessionStart hook 实测注入了整段上下文，见 `p5-hook-stream.jsonl` 的 hook_response.output）；无输出 = 放行（配合 permissions）。
- 事件流伴随 `system/hook_started`（hook_id/hook_name/hook_event）与 `system/hook_response`（output/stderr/exit_code/outcome）；**实测注记：该事件对仅在 SessionStart 观察到（p5-hook-stream.jsonl L1–2），静默的 PreToolUse hook 执行未产生流事件（同文件 L36–41）——Task 21 勿假定每次钩子执行必有流事件对**。

**结论**：guard Hooks（Task 21）按此契约分发：stdin 注入（含 `permission_mode`、`tool_input`、`tool_use_id`、`prompt_id`）、stdout 裁决/上下文、`matcher` 按工具名过滤；engine 事件流中的 hook_started/hook_response 须进事件总线（TUI 可显示钩子活动）。

## 验证点 ⑥：CLI flag 实际生效性

| flag | 实测命令要点 | 观察 | 结论 |
|---|---|---|---|
| `--append-system-prompt` | `-p "天气一句话" --append-system-prompt "…end with PROBE_MARKER_42"`（`p6a-*.txt`） | 输出以 `PROBE_MARKER_42` 结尾 | 追加生效、不改默认提示；BootComposer（Task 12）产物经此注入 |
| `--resume <sid>` | 基线回合取得 `session_id`，再 `--resume $sid` 问口令（`p6c-resume.json`） | 实测答出前轮 `BANANA42`；**resume 回合的 session_id 与基线相同**（480a30d9 = 480a30d9；基线回合输出未存档，基线 id 由执行者记录并经 fork 对照佐证） | 续链生效且**不换** session id；sessionx（Task 10/11）以 init.session_id 记账 |
| `--fork-session` | `--resume $sid --fork-session`（`p6d-fork.json`） | 实测新 `session_id ≠ 原 id`（fa29e130 ≠ 480a30d9） | 分叉生效且**生成新** session id；镜像恢复（US5）据此安全续链 |
| `--mcp-config` | 注入 failing server（`cmd /c exit 1`）（`p6e-mcp-config.txt`） | init 的 `mcp_servers[]` 列出 `{name:"probe_fail",status:"failed",source:"dynamic"}`；健康服务器 `connected` | flag 生效；registry 冻结顺序 = 传入清单顺序（Task 25） |
| `--allowedTools` | `--allowedTools "Write"`（`p5-hook-run.txt` 等） | 工具直接执行、无权限回合（结合 ④b 对照） | 预授名单生效；BootComposer 依 config 生成 allow/deny 清单（§9） |
| `--setting-sources` | `--setting-sources project`（`p6f-setting-sources.txt`） | **凭据不加载**（`apiKeySource:"none"` → "Not logged in"），模型/插件/slash_commands 集合明显缩小 | **mulix 传 `--setting-sources` 必须包含 user 作用域**（如 `user,project,local`），否则用户级凭据与设置丢失；该 flag 同时用于隔离外部 settings 漂移（§7.2） |

resume 基线会话获取（`--output-format json` 输出为**单行 JSON 数组**，取元素 `session_id`/`result`）：

```powershell
$raw = claude -p "..." --output-format json --verbose
$arr = ($raw | Where-Object { $_.Trim().StartsWith('[') }) | ConvertFrom-Json
$sid = $arr[0].session_id
```

**复现陷阱注记**：`--resume` 需要有效的会话 id——早期一次探针以空 id 调用得到 CLI 用法错误（`p6c-resume.txt`/`p6d-fork.txt` 为该失败尝试的存档，正确证据在 `p6c-resume.json`/`p6d-fork.json`）；`--resume` 查找会话按项目目录（cwd 派生）定位，跨 cwd resume 需在原 cwd 下执行。

---

## 与 design §7 的差异与修订（实测为准，已回写 design 文档）

1. **canUseTool 回路**：§7.2/§5.2 原文「canUseTool 控制协议回调」；实测 P1 可行契约是 `--permission-prompt-tool` + stdio MCP 权限工具（④b），控制协议（④c）为 SDK 内部机制。**design §5.2/§7.2 已按实测修订**（§9 权限裁决逻辑与其兼容、未改动）；Task 20 审批队列按 ④b 契约实现；fail-closed 语义（④a 自动拒绝 + mulix 120s 超时）不变。
2. **`--permission-mode` 必须显式**：环境默认 `auto` 会绕过审批；spawn 固定集默认 `default`（unattended `mulix run`）或按 config。
3. **`--setting-sources` 必须含 user**：否则凭据丢失（实测 Not logged in）。
4. **P1 不使用 `--max-turns`**：design §7.2 已定（Go 侧预算控制）；探针为控制成本用了单回合命令，不属于 spawn 固定集。
5. **`--output-format json` 为单行数组**（非 stream-json 的逐行事件）——doctor/诊断辅助命令可用，decoder 不依赖。
6. **delta 事件形态实测**（③）：`stream_event` 包裹 + 整块并存；P1 不开启，Task 16 留位。
