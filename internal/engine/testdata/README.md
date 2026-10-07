# engine 假 CLI 测试脚本（Task 17）

本目录存放 ClaudeHost 进程级测试用的「假 CLI」脚本（JSON）。
执行器不是外部脚本，而是 Go 测试二进制自身（`TestMain` 检测
`MULIX_FAKE_CLI_MODE=1` 后进入 `runFakeCLI`，按本目录的脚本文件行动）——
这是 Go 官方的 helper-process 惯用法，避免构建/分发外部可执行文件。

脚本字段（fakeScript，见 claudehost_test.go）：

| 字段 | 说明 |
|---|---|
| `session_id` / `version` / `model` / `permission_mode` | init 事件字段（version 用于 engine.lock 漂移测试） |
| `before_input` | init 之后、首个输入之前逐行透传的原始 JSONL |
| `per_input` | 每收到一条 `{"type":"user",…}` stdin 行后逐行透传的原始 JSONL |
| `stderr_lines` | 启动时写入 stderr 的行（脱敏环形缓冲测试） |
| `garbage_line` | 在 init 之前写入的非法行（解码失败测试） |
| `echo_stdin_to_stderr` | 把收到的每行 stdin 以 `STDIN:` 前缀回显到 stderr（wire 形状断言） |
| `hang_on_eof` | stdin EOF 后挂起不退出（有界关闭/杀进程树测试） |
