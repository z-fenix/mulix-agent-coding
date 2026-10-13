// Package mulixkernel 提供 P5d 自指工具(kernel_inspect/cordis_run):
// Agent 对运行时的只读巡检与 goja 沙箱执行。define/undefine/stop 三工具
// 不实现(016 FR-103 治理红线:Agent 不能修改自己的权限/工具面)。
package mulixkernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"mulix/internal/config"
	"mulix/internal/registry"
)

// KernelInspectTool 实现 registry.Tool:只读巡检(016 FR-101)。keys 为
// 前台内核服务键快照(MULIX_KERNEL_KEYS Boot env 桥注入,T3);tools
// 延迟取本进程(serve)注册面快照——ptc_registered 在 Execute 时判定。
type KernelInspectTool struct {
	keys    []string
	profile config.ProfileMode
	tools   func() map[string]registry.Tool
}

// NewKernelInspectTool 创建巡检工具(keys 可为 nil——env 缺席降级,非阻断)。
func NewKernelInspectTool(keys []string, profile config.ProfileMode, tools func() map[string]registry.Tool) *KernelInspectTool {
	return &KernelInspectTool{keys: keys, profile: profile, tools: tools}
}

func (t *KernelInspectTool) Name() string { return "kernel_inspect" }
func (t *KernelInspectTool) Description() string {
	return "巡检 mulix 运行时:服务键、profile、注册工具面"
}
func (t *KernelInspectTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *KernelInspectTool) ReadOnly() bool { return true }

func (t *KernelInspectTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc := append([]string{}, t.keys...)
	sort.Strings(svc)
	snapshot := t.tools()
	toolNames := make([]string, 0, len(snapshot))
	ptcRegistered := false
	for name := range snapshot {
		toolNames = append(toolNames, name)
		if name == "ptc_execute" {
			ptcRegistered = true
		}
	}
	sort.Strings(toolNames)
	// keys_source(019 FR-104;016 P6+ 路由核销):区分「真无服务键」与
	// 「桥缺席降级」——运维排障两态表象相同(services 恒空)。
	keysSource := "env"
	if len(t.keys) == 0 {
		keysSource = "degraded"
	}
	// eventbus_subscriptions:serve 进程无总线桥(前台指标不跨进程暴露,
	// 015 同款裁定)——如实恒 0,Ruling 入台账。
	rep := struct {
		Services              []string `json:"services"`
		Profile               string   `json:"profile"`
		Tools                 []string `json:"tools"`
		PtcRegistered         bool     `json:"ptc_registered"`
		EventbusSubscriptions int      `json:"eventbus_subscriptions"`
		KeysSource            string   `json:"keys_source"`
	}{svc, string(t.profile), toolNames, ptcRegistered, 0, keysSource}
	b, err := json.Marshal(rep)
	if err != nil {
		return "", fmt.Errorf("kernel_inspect: marshal: %w", err)
	}
	return string(b), nil
}

// RegisterSelfRefTools 是自指工具注册的 testable seam(015 RegisterPTCExecute
// 同款形态):profile 单真源(017 FR-103——bool 与 profile 双参数的矛盾面
// 退役),standard 守卫 + 顺序注册 kernel_inspect→cordis_run(调用方保证
// 在 NewServer 冻结前、ptc_execute 注册之后调用)。minimal 不注册。
func RegisterSelfRefTools(profile config.ProfileMode, keys []string, tools func() map[string]registry.Tool, reg func(registry.Tool)) {
	if profile != config.ProfileStandard {
		return
	}
	reg(NewKernelInspectTool(keys, profile, tools))
	reg(NewCordisRunTool(tools, 30*time.Second))
}

// ParseKernelKeys 解析 MULIX_KERNEL_KEYS(017 FR-101):env 是进程边界——
// 脏值(空格/尾逗号/空段)不得以幽灵键形态进入巡检输出。TrimSpace+滤空段;
// 空串/全空白 → nil(降级非阻断,与 NewKernelInspectTool nil-keys 形态一致)。
func ParseKernelKeys(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var keys []string
	for _, k := range strings.Split(v, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}
