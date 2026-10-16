// Package engine 是引擎宿主层：claude CLI 子进程与 stream-json 协议。
//
// 子代理流形态（2026-10-16 核验，引擎 2.1.30x）：
//   - 引擎原生 Task 工具在泵面上呈现为普通 tool_use→tool_result——父调用
//     即治理面（经 --permission-prompt-tool 审批回路），与 shell 工具同型。
//   - 子代理内部的 text/thinking/tool 事件不上流：引擎仅在显式
//     --forward-subagent-text（--print + stream-json）下才以带
//     parent_tool_use_id 的 assistant/user 消息转发——mulix 未接线该 flag，
//     泵面无子事件，L2 围堵（未裁决 tool_result 终止）无误杀面。
//   - 红线：未来任何接线引入 --forward-subagent-text 前，必须先做
//     parent_tool_use_id 治理建模（子事件直通泵会令 L2 围堵对未裁决子调用
//     响亮终止，或需显式白名单裁定）——不得静默开启。
package engine
