package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// Source 是审批裁决来源词表（与 event.ApprovalResolvedEvent.source 一致）。
// timeout 语义 = 一切非人为 fail-closed（审批超时 / 审批者错误）。
type Source string

const (
	SourcePolicy  Source = "policy"  // 策略直裁（allow/deny 规则、预设、无人值守基线）
	SourceSession Source = "session" // 会话级授权命中
	SourceUser    Source = "user"    // 审批者人工裁决
	SourceTimeout Source = "timeout" // 非人为 fail-closed（超时/审批者错误）
)

// ApprovalRequest 是送审请求（TUI 卡片渲染所需的最小集）。
type ApprovalRequest struct {
	ToolUseID string          // gate 侧审批序号 id；与引擎 tool_use id 的关联由 control 负责
	Tool      string          // 工具名
	Input     json.RawMessage // 原始输入（卡片展示参数）
	Scope     string          // 规范化范围（「本会话允许当前范围」的绑定键）
	Timeout   time.Duration   // 审批窗口（Gate 计时，FR-005；卡片可显示倒计时）
}

// ApprovalVerdict 是审批者的人类裁决。
type ApprovalVerdict struct {
	Allow      bool   // 本次允许
	ResolvedBy string // 人读说明（如 "user:allow-once"、"user:session-scope"）
}

// Approver 是审批者抽象（TUI 卡片 / mulix run 显式无审批者）。
// 实现必须尊重 ctx：Gate 以 ctx 超时/取消为 fail-closed 依据。
type Approver interface {
	ApproveRequest(ctx context.Context, req ApprovalRequest) (ApprovalVerdict, error)
}

// GateOptions 是 NewGate 参数。
type GateOptions struct {
	Policy   *Policy       // 必填：策略裁决
	Approver Approver      // nil = 无人值守（Ask 一律 fail-closed 拒绝——FR-009）
	Timeout  time.Duration // 审批窗口（默认 120s；§9 队列超时 fail-closed）
}

// GateDecision 是一次工具调用的最终裁决（ask 已被解决为 allow/deny）。
type GateDecision struct {
	Decision   Decision // allow|deny
	Source     Source   // policy|session|user|timeout
	ResolvedBy string   // 人读说明（审计/镜像事件）
}

// Gate 是单会话审批门（control 持有；P1 串行调用 Evaluate）。
type Gate struct {
	policy   *Policy
	approver Approver
	timeout  time.Duration

	mu     sync.Mutex
	grants map[string]bool // key: tool + "\x00" + scope（"*" = 整工具）
}

// NewGate 构造审批门（fail-closed：无 Policy 拒绝构造）。
func NewGate(opts GateOptions) (*Gate, error) {
	if opts.Policy == nil {
		return nil, fmt.Errorf("guard: gate requires policy")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 120 * time.Second
	}
	return &Gate{
		policy:   opts.Policy,
		approver: opts.Approver,
		timeout:  opts.Timeout,
		grants:   map[string]bool{},
	}, nil
}

// Evaluate 裁决一次工具调用：
//
//	policy allow            → allow(policy)
//	policy deny             → deny(policy)   [不可被会话授权覆盖]
//	policy ask：
//	  会话授权命中          → allow(session)
//	  无人值守              → deny(policy, unattended)
//	  送审：允许/拒绝       → allow/deny(user)
//	        超时/审批者错误 → deny(timeout)
func (g *Gate) Evaluate(ctx context.Context, tool string, input json.RawMessage) (GateDecision, error) {
	switch g.policy.Decide(tool, input) {
	case DecisionAllow:
		return GateDecision{Decision: DecisionAllow, Source: SourcePolicy, ResolvedBy: "policy"}, nil
	case DecisionDeny:
		return GateDecision{Decision: DecisionDeny, Source: SourcePolicy, ResolvedBy: "policy"}, nil
	}

	scope := NormalizeScope(tool, input)
	if g.sessionGranted(tool, scope) {
		return GateDecision{Decision: DecisionAllow, Source: SourceSession, ResolvedBy: "session:" + scope}, nil
	}
	if g.approver == nil {
		return GateDecision{Decision: DecisionDeny, Source: SourcePolicy, ResolvedBy: "unattended"}, nil
	}

	req := ApprovalRequest{
		ToolUseID: newApprovalID(),
		Tool:      tool,
		Input:     input,
		Scope:     scope,
		Timeout:   g.timeout,
	}
	actx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	verdict, err := g.approver.ApproveRequest(actx, req)
	if err != nil {
		// 非人为 fail-closed（审批窗口耗尽 / 审批者故障）——词表归 timeout
		return GateDecision{Decision: DecisionDeny, Source: SourceTimeout,
			ResolvedBy: "timeout: " + err.Error()}, nil
	}
	// 迟到裁决 fail-closed（FR-005 红线）：审批窗口属于 Gate——即便审批者
	// 返回 (Allow, nil)，只要窗口已耗尽（或父 ctx 已取消），一律 deny(timeout)。
	// 不依赖审批者自觉遵守 ctx。
	if actx.Err() != nil {
		return GateDecision{Decision: DecisionDeny, Source: SourceTimeout,
			ResolvedBy: "timeout: approval window closed before verdict"}, nil
	}
	if verdict.Allow {
		return GateDecision{Decision: DecisionAllow, Source: SourceUser, ResolvedBy: verdict.ResolvedBy}, nil
	}
	return GateDecision{Decision: DecisionDeny, Source: SourceUser, ResolvedBy: verdict.ResolvedBy}, nil
}

// GrantSession 授予会话级授权（工具+规范化范围；内存态，control 负责镜像
// 留痕事件）。scope 传 "*" 或空串 = 整工具授权（任意输入命中）。
func (g *Gate) GrantSession(tool, scope string) {
	if scope == "" {
		scope = "*"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[tool+"\x00"+scope] = true
}

func (g *Gate) sessionGranted(tool, scope string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.grants[tool+"\x00"+"*"] {
		return true
	}
	if scope == "" {
		return false
	}
	return g.grants[tool+"\x00"+scope]
}

// NormalizeScope 推导会话授权的规范化范围（§9「同一命令前缀/同一目录」）：
//   - Bash：command 的前两个空白分隔词（"go test ./..." → "go test"）；
//     **含 shell 元字符（;|&`$()><\n 等）的命令一律派生 ""**——前两词前缀
//     不能为复合命令背书（"go test && rm -rf /" 不得因 "go test" 授权而
//     免审；与 Task 19 词边界红线同一威胁面，fail-closed）；
//   - 文件族：路径所在目录（反斜杠规范化；"dir:" 前缀防跨工具碰撞）；
//   - 其余/缺输入：""（仅整工具授权可命中）。
func NormalizeScope(tool string, input json.RawMessage) string {
	switch tool {
	case "Bash":
		cmd := inputString(input, "command")
		if containsShellMeta(cmd) {
			return ""
		}
		fields := strings.Fields(cmd)
		switch len(fields) {
		case 0:
			return ""
		case 1:
			return fields[0]
		default:
			return fields[0] + " " + fields[1]
		}
	case "Edit", "Write", "Read", "NotebookEdit", "MultiEdit":
		p := inputString(input, "file_path")
		if p == "" {
			p = inputString(input, "notebook_path")
		}
		if p == "" {
			return ""
		}
		return "dir:" + path.Dir(strings.ReplaceAll(p, "\\", "/"))
	default:
		return ""
	}
}

// shellMetaChars 是 scope 派生的复合命令哨兵：出现即拒绝前缀背书。
// （空格本身是词分隔符不在此列；`<`/`>`/`|`/`&`/`;`/反引号/`$`/括号/换行
// 均可改变命令语义。）
const shellMetaChars = ";|&`$()<>\n\r"

func containsShellMeta(cmd string) bool {
	return strings.ContainsAny(cmd, shellMetaChars)
}

// newApprovalID 生成送审请求 id（非引擎 tool_use id 的 gate 侧序号）。
func newApprovalID() string {
	return fmt.Sprintf("apr-%d", time.Now().UnixNano())
}
