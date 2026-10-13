// Package mulixkernel 提供 P5d 自指工具(kernel_inspect/cordis_run):
// Agent 对运行时的只读巡检与 goja 沙箱执行。cordis_define/undefine/stop
// 不实现(016 FR-103 治理红线:Agent 不能修改自己的权限/工具面)。
package mulixkernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	// eventbus_subscriptions:serve 进程无总线桥(前台指标不跨进程暴露,
	// 015 同款裁定)——如实恒 0,Ruling 入台账。
	rep := struct {
		Services              []string `json:"services"`
		Profile               string   `json:"profile"`
		Tools                 []string `json:"tools"`
		PtcRegistered         bool     `json:"ptc_registered"`
		EventbusSubscriptions int      `json:"eventbus_subscriptions"`
	}{svc, string(t.profile), toolNames, ptcRegistered, 0}
	b, err := json.Marshal(rep)
	if err != nil {
		return "", fmt.Errorf("kernel_inspect: marshal: %w", err)
	}
	return string(b), nil
}

// RegisterSelfRefTools 是自指工具注册的 testable seam(015 RegisterPTCExecute
// 同款形态):standard 守卫 + 顺序注册 kernel_inspect→cordis_run(调用方保证
// 在 NewServer 冻结前、ptc_execute 注册之后调用)。minimal 不注册。
func RegisterSelfRefTools(standard bool, keys []string, profile config.ProfileMode, tools func() map[string]registry.Tool, reg func(registry.Tool)) {
	if !standard {
		return
	}
	reg(NewKernelInspectTool(keys, profile, tools))
	reg(NewCordisRunTool(tools, 30*time.Second))
}
