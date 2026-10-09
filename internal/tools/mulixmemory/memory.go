// Package mulixmemory 注册记忆服务的三个 MCP 工具（M13/Task 6）：
// memory_save / memory_list / memory_delete。
//
// 进程拓扑：工具在 `mulix mcp serve` 子进程内执行，直接打开工作区
// `.mulix/memory/` 的 *memory.Store 读写（design 002 §4「serve 子进程直写
// 工作区文件」）——不经前台回调桥，与 mulixsubagent 的 HTTP 桥刻意不同。
// workspace 由组合根经 RegisterConditional 传入（serve 进程 cwd = 引擎
// cmd.Dir = 工作区，cwd 语义；前台进程同样注册——注册面冻结语义，工具在
// 前台不执行）。
//
// 每次工具执行惰性 Open、用完即弃（不留长命实例）：Add 的 id 序取当前文件
// 重放——多会话树共享同一记忆库目录时（主会话与各子代理各有自己的 serve
// 进程），任一写者携带的 id 游标都不会跨进程陈旧，双写并发下的 id 碰撞窗口
// 收敛到毫秒级「重放→追加」间隙（Review Focus「记忆名称冲突」的工具侧防线；
// 进程内互斥与追加原子性由 memory.Store 的 open-per-write + jsonl 红线承载，
// 单实例-per-dir 契约由每次操作独占实例满足）。
//
// source_session 溯源：memory_save 以 MULIX_MEMORY_SESSION（control 在 Boot
// Env 注入的本会话 id——绝不入 mcp-config，SC-003 字节稳定，与
// MULIX_SUBAGENT_CALLBACK 同一裁定）为戳；env 缺失（独立/测试运行）时空戳，
// 不阻塞保存（溯源是元数据，不是写入前置条件）。
//
// fail-closed 矩阵（同 mulixsubagent）：参数缺失 / Open 失败 / store 操作
// 失败 → {"ok":false,"error":...}，协议层不失败（返回 nil error——引擎侧
// 工具错误走正常错误呈现，回合不悬挂）。
//
// 分层（Task 10 规则）：本包可 import internal/memory（明确允许边）；不
// import 其他 internal 内核包。
//
// 注册不走 init() 自注册（mulixsubagent 同款修订）：记忆工具按
// [tools] memory_search 条件注册——导出 RegisterConditional，由 cli 组合根
// 在配置加载后、registry.NewServer 冻结前调用（前台 assembleController 与
// serve 进程 ServeMCP 各自为政，进程内恰好一次）。
package mulixmemory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mulix/internal/memory"
	"mulix/internal/registry"
)

// sessionEnvVar 是 control 注入本会话 id 的 Boot Env 键（memory_save 的
// source_session 溯源戳）。
const sessionEnvVar = "MULIX_MEMORY_SESSION"

// store 是工具面消费记忆库的窄缝（Task 3 mulixsubagent 的 subagentOps 同款
// 内部缝）：*memory.Store 天然满足；测试以 stub 注入。检索（Search）刻意
// 不在缝内——隐式召回走 control 的 turn-tail 注入，不走工具（design 002
// R2：避免工具面膨胀与每回合工具面字节抖动）。
type store interface {
	Add(text, sourceSession string) (string, error)
	Delete(id string) error
	List() ([]memory.Entry, error)
}

// registerOnce 钉 RegisterConditional 的恰好一次语义（registry.Register 对
// 重复名 panic；Once 兜底防多次调用——enabled=false 先到即消耗，后续调用
// 无论 enabled 都不再注册，组合根每进程只调一次）。
var registerOnce sync.Once

// RegisterConditional 条件注册三个记忆工具：enabled=true 注册
// memory_save/memory_list/memory_delete（workspace = 工作区根，工具经
// <workspace>/.mulix/memory 直写）；enabled=false 整体跳过且永不注册。
// srv 目前未用（注册进全局注册表；参数保留以钉「必须在 registry.NewServer
// 冻结前调用」的时序契约——冻结后注册的工具不进工具面）。
func RegisterConditional(_ *registry.Server, workspace string, enabled bool) {
	registerOnce.Do(func() {
		if enabled {
			for _, t := range newToolsForDir(Dir(workspace)) {
				registry.Register(t)
			}
		}
	})
}

// Dir 返回 workspace 的记忆库目录（design 002 §2 裁决 3：项目级记忆库 =
// <workspace>/.mulix/memory）。cli 组装根（control 侧开库）与工具侧共用
// 本口径，杜绝两处拼装漂移。
func Dir(workspace string) string {
	return filepath.Join(workspace, ".mulix", "memory")
}

// newToolsForDir 以「每次执行惰性 Open」的 opener 构造三件套（生产路径）。
func newToolsForDir(dir string) []registry.Tool {
	return newTools(func() (store, error) { return memory.Open(dir) })
}

// newToolsWithStore 以固定 store 实例构造三件套（测试注入缝）。
func newToolsWithStore(st store) []registry.Tool {
	return newTools(func() (store, error) { return st, nil })
}

func newTools(open func() (store, error)) []registry.Tool {
	return []registry.Tool{&saveTool{open: open}, &listTool{open: open}, &deleteTool{open: open}}
}

// ---- memory_save ----

// saveTool 是 memory_save：显式保存一条记忆（source_session = 本会话 id）。
type saveTool struct {
	open func() (store, error)
}

func (t *saveTool) Name() string { return "memory_save" }

func (t *saveTool) Description() string {
	return "保存一条项目记忆（约定/决策/事实），后续会话按相关性召回；返回条目 id。"
}

func (t *saveTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"要记住的内容（自包含，勿依赖上下文）"}},"required":["text"]}`)
}

func (t *saveTool) ReadOnly() bool { return false }

func (t *saveTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &payload); err != nil || strings.TrimSpace(payload.Text) == "" {
		return opError("malformed memory_save arguments: text is required"), nil
	}
	st, err := t.open()
	if err != nil {
		return opError("memory store unavailable: " + err.Error()), nil
	}
	id, err := st.Add(payload.Text, os.Getenv(sessionEnvVar))
	if err != nil {
		return opError("memory save failed: " + err.Error()), nil
	}
	return mustJSON(map[string]any{"ok": true, "id": id}), nil
}

// ---- memory_list ----

// listTool 是 memory_list：列出全部活跃记忆。
type listTool struct {
	open func() (store, error)
}

func (t *listTool) Name() string { return "memory_list" }

func (t *listTool) Description() string {
	return "列出项目记忆库中的全部活跃条目（id/内容/来源会话/创建时间）。"
}

func (t *listTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t *listTool) ReadOnly() bool { return true }

func (t *listTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	st, err := t.open()
	if err != nil {
		return opError("memory store unavailable: " + err.Error()), nil
	}
	entries, err := st.List()
	if err != nil {
		return opError("memory list failed: " + err.Error()), nil
	}
	out := make([]entryJSON, 0, len(entries)) // 空库序列化为 []（非 null）
	for _, e := range entries {
		out = append(out, entryJSON{ID: e.ID, Text: e.Text, SourceSession: e.SourceSession, CreatedAt: e.CreatedAt})
	}
	return mustJSON(map[string]any{"ok": true, "entries": out}), nil
}

// entryJSON 是 list 应答的 wire 形状（小写字段 = design §4 存储字段口径；
// memory.Entry 无 json tag，不直 marshaling——wire 契约不随结构体漂移）。
type entryJSON struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	SourceSession string `json:"source_session"`
	CreatedAt     string `json:"created_at"`
}

// ---- memory_delete ----

// deleteTool 是 memory_delete：按 id 删除一条记忆（tombstone）。
type deleteTool struct {
	open func() (store, error)
}

func (t *deleteTool) Name() string { return "memory_delete" }

func (t *deleteTool) Description() string {
	return "删除一条项目记忆（按 memory_save 返回或 memory_list 列出的 id）。"
}

func (t *deleteTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"要删除的记忆条目 id"}},"required":["id"]}`)
}

func (t *deleteTool) ReadOnly() bool { return false }

func (t *deleteTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &payload); err != nil || payload.ID == "" {
		return opError("malformed memory_delete arguments: id is required"), nil
	}
	st, err := t.open()
	if err != nil {
		return opError("memory store unavailable: " + err.Error()), nil
	}
	if err := st.Delete(payload.ID); err != nil {
		return opError("memory delete failed: " + err.Error()), nil
	}
	return mustJSON(map[string]any{"ok": true, "id": payload.ID}), nil
}

// opError 构造操作失败的应答（fail-closed 的统一出口：{"ok":false,"error"}）。
func opError(msg string) string {
	return mustJSON(map[string]any{"ok": false, "error": msg})
}

// mustJSON 序列化应答（map[string]any 字面量不会失败；失败即程序员错误）。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"response marshal failed"}`
	}
	return string(b)
}
