// Package ptc 提供程序化工具调用(PTC)沙箱(015 FR-101..103):
// goja.Runtime 封装——注册工具暴露为 JS 全局函数;超时中断;安全白名单
// (无 require/fetch/fs/network 逃逸)。
package ptc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"mulix/internal/registry"
)

// Sandbox 封装 goja.Runtime:注册工具暴露为 JS 全局函数。
type Sandbox struct {
	tools   map[string]registry.Tool
	timeout time.Duration
}

// NewSandbox 创建沙箱(tools=暴露给脚本的工具子集;timeout=执行上限)。
func NewSandbox(tools map[string]registry.Tool, timeout time.Duration) *Sandbox {
	return &Sandbox{tools: tools, timeout: timeout}
}

// Execute 在沙箱中运行脚本;注册工具暴露为 JS 全局函数(名称→函数,参数=
// JS 对象→json→Tool args);超时经 Runtime.Interrupt 中断;安全白名单:
// 不注册 require/fetch/fs/network(模型不可信输入——沙箱是安全边界)。
// 注:Runtime.Interrupt 仅中断 JS 代码——慢工具 Execute 不受 timeout 管辖
// (goja 已知限制;超时守卫 runaway 脚本)。
func (s *Sandbox) Execute(script string) (string, error) {
	rt := goja.New()
	// 超时中断。
	timer := time.AfterFunc(s.timeout, func() {
		rt.Interrupt("timeout")
	})
	defer timer.Stop()

	// 注册工具为 JS 全局函数。
	for name, tool := range s.tools {
		t := tool // 闭包捕获
		fn := func(call goja.FunctionCall) goja.Value {
			argsJSON, _ := json.Marshal(call.Argument(0).Export())
			result, err := t.Execute(context.Background(), argsJSON)
			if err != nil {
				panic(rt.ToValue(err.Error()))
			}
			return rt.ToValue(result)
		}
		rt.Set(name, fn)
	}

	// 执行。
	result, err := rt.RunString(script)
	if err != nil {
		var ie *goja.InterruptedError
		if errors.As(err, &ie) {
			return "", fmt.Errorf("ptc: script timeout after %v", s.timeout)
		}
		return "", fmt.Errorf("ptc: script error: %w", err)
	}
	return result.String(), nil
}
