package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"

	"mulix/internal/cache"
	"mulix/internal/tools/mulixusage"
)

// newSessionID 生成 mulix 会话 id（16 字节 hex；无第三方依赖）。
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 熵源失败极罕见；时间兜底（可预测 id 只影响并列概率，不影响正确性）
		return fmt.Sprintf("s%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

// selfCommand 返回 mcp-config 条目的 mulix 自 spawn 命令（argv 全量）。
// 测试内嵌（go test 二进制）也可执行——统一走 os.Executable。
func selfCommand() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cli: resolve executable: %w", err)
	}
	if _, err := exec.LookPath(exe); err != nil {
		return nil, fmt.Errorf("cli: executable not runnable: %w", err)
	}
	return []string{exe, "mcp", "serve"}, nil
}

// setUsageSource 接线 mulix_usage 的记账口径（T26）：跨进程权威
// ReadUsageReport（含 resume 续号的完整历史）。
func setUsageSource(usageRoot, sessionID string) {
	mulixusage.SetSource(func() (cache.Report, error) {
		return cache.ReadUsageReport(usageRoot, sessionID)
	})
}
