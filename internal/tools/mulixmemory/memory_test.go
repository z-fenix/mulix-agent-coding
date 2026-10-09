package mulixmemory

// M13/T13.2 工具面测试：schema 契约 / 必填校验 fail-closed / source_session
// 溯源 / 直写文件与 store 重放一致（brief Step 1 ④）/ Open 失败不崩溃 /
// RegisterConditional 条件注册（Task 3 裁定：注册表断言仅 enabled 侧——
// 全局注册表 + 包级 Once 跨测试共享，disabled 反向断言不可靠）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mulix/internal/memory"
	"mulix/internal/registry"
)

// stubStore 是 store 窄缝的测试替身（记录调用、返回可编程结果）。
type stubStore struct {
	addText   string
	addSource string
	addID     string
	addErr    error

	deleted   []string
	deleteErr error

	entries []memory.Entry
	listErr error
}

func (s *stubStore) Add(text, sourceSession string) (string, error) {
	s.addText, s.addSource = text, sourceSession
	return s.addID, s.addErr
}

func (s *stubStore) Delete(id string) error {
	s.deleted = append(s.deleted, id)
	return s.deleteErr
}

func (s *stubStore) List() ([]memory.Entry, error) {
	return s.entries, s.listErr
}

// newStubTools 构造注入 stub store 的三件套（测试专用构造缝）。
func newStubTools(st store) map[string]registry.Tool {
	tools := newToolsWithStore(st)
	out := map[string]registry.Tool{}
	for _, t := range tools {
		out[t.Name()] = t
	}
	return out
}

// toolsByDir 构造生产形态（dir 惰性 Open）的三件套并按名索引。
func toolsByDir(t *testing.T, dir string) map[string]registry.Tool {
	t.Helper()
	out := map[string]registry.Tool{}
	for _, tl := range newToolsForDir(dir) {
		out[tl.Name()] = tl
	}
	return out
}

func execTool(t *testing.T, tool registry.Tool, args string) map[string]any {
	t.Helper()
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s execute: 协议层不得失败（fail-closed 走 JSON）: %v", tool.Name(), err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("%s response not json: %v (%s)", tool.Name(), err, out)
	}
	return m
}

// TestMemoryToolSchemas 钉三工具的注册契约：名字、schema 必填字段、ReadOnly 标注。
func TestMemoryToolSchemas(t *testing.T) {
	tools := newStubTools(&stubStore{})
	if len(tools) != 3 {
		t.Fatalf("registered tool count = %d, want 3", len(tools))
	}
	for _, name := range []string{"memory_save", "memory_list", "memory_delete"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("tool %q missing", name)
		}
	}
	var saveSchema map[string]any
	if err := json.Unmarshal(tools["memory_save"].Schema(), &saveSchema); err != nil {
		t.Fatalf("save schema: %v", err)
	}
	req, _ := saveSchema["required"].([]any)
	if len(req) != 1 || req[0] != "text" {
		t.Fatalf("save schema required = %v, want [text]", req)
	}
	var delSchema map[string]any
	if err := json.Unmarshal(tools["memory_delete"].Schema(), &delSchema); err != nil {
		t.Fatalf("delete schema: %v", err)
	}
	req, _ = delSchema["required"].([]any)
	if len(req) != 1 || req[0] != "id" {
		t.Fatalf("delete schema required = %v, want [id]", req)
	}
	var listSchema map[string]any
	if err := json.Unmarshal(tools["memory_list"].Schema(), &listSchema); err != nil {
		t.Fatalf("list schema: %v", err)
	}
	if typ, _ := listSchema["type"].(string); typ != "object" {
		t.Fatalf("list schema type = %v, want object", listSchema["type"])
	}
	if tools["memory_list"].ReadOnly() != true {
		t.Fatalf("memory_list must be ReadOnly")
	}
	if tools["memory_save"].ReadOnly() || tools["memory_delete"].ReadOnly() {
		t.Fatalf("memory_save/memory_delete must not be ReadOnly")
	}
}

// TestMemorySaveRequiresText 钉必填校验：缺 text → ok=false（fail-closed）。
func TestMemorySaveRequiresText(t *testing.T) {
	st := &stubStore{}
	tools := newStubTools(st)
	for _, args := range []string{`{}`, `{"text":"   "}`, `not-json`} {
		r := execTool(t, tools["memory_save"], args)
		if r["ok"] != false || !strings.Contains(anyString(r["error"]), "text") {
			t.Fatalf("save(%s) resp = %v, want ok=false with text error", args, r)
		}
	}
	if st.addText != "" {
		t.Fatalf("store must not be touched on invalid args (got add %q)", st.addText)
	}
}

// TestMemorySaveStampsSourceSession 钉溯源：memory_save 以
// MULIX_MEMORY_SESSION（control Boot Env 注入的本会话 id）为 source_session。
func TestMemorySaveStampsSourceSession(t *testing.T) {
	t.Setenv("MULIX_MEMORY_SESSION", "s-session-9")
	st := &stubStore{addID: "m4"}
	tools := newStubTools(st)
	r := execTool(t, tools["memory_save"], `{"text":"记住本项目用 pnpm"}`)
	if r["ok"] != true || r["id"] != "m4" {
		t.Fatalf("save resp = %v, want ok=true id=m4", r)
	}
	if st.addText != "记住本项目用 pnpm" || st.addSource != "s-session-9" {
		t.Fatalf("Add(text=%q, source=%q), want source stamped from env", st.addText, st.addSource)
	}
}

// TestMemoryListShape 钉 list 应答形状：小写字段名（design §4 存储字段口径）、
// 空库为空数组（非 null）。
func TestMemoryListShape(t *testing.T) {
	st := &stubStore{entries: []memory.Entry{
		{ID: "m1", Text: "第一条", SourceSession: "s1", CreatedAt: "2026-10-08T00:00:00Z"},
	}}
	tools := newStubTools(st)
	raw, err := tools["memory_list"].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list execute: %v", err)
	}
	var resp struct {
		OK      bool `json:"ok"`
		Entries []struct {
			ID            string `json:"id"`
			Text          string `json:"text"`
			SourceSession string `json:"source_session"`
			CreatedAt     string `json:"created_at"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("list resp: %v (%s)", err, raw)
	}
	if !resp.OK || len(resp.Entries) != 1 || resp.Entries[0].ID != "m1" ||
		resp.Entries[0].Text != "第一条" || resp.Entries[0].SourceSession != "s1" {
		t.Fatalf("list resp = %s", raw)
	}
	// 空库 → entries: []（不是 null）
	st2 := &stubStore{entries: []memory.Entry{}}
	tools2 := newStubTools(st2)
	raw2, _ := tools2["memory_list"].Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(raw2, `"entries":[]`) {
		t.Fatalf("empty list resp = %s, want entries:[]", raw2)
	}
}

// TestMemoryDeleteRequiresID 钉 delete 校验与错误透传。
func TestMemoryDeleteRequiresID(t *testing.T) {
	st := &stubStore{}
	tools := newStubTools(st)
	r := execTool(t, tools["memory_delete"], `{}`)
	if r["ok"] != false || !strings.Contains(anyString(r["error"]), "id") {
		t.Fatalf("delete({}) resp = %v, want ok=false with id error", r)
	}
	r = execTool(t, tools["memory_delete"], `{"id":"m1"}`)
	if r["ok"] != true {
		t.Fatalf("delete(m1) resp = %v", r)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "m1" {
		t.Fatalf("store deletes = %v", st.deleted)
	}
	// store 报错（未知 id）→ ok=false 原样透传
	st2 := &stubStore{deleteErr: errorString("memory: unknown id \"mX\"")}
	tools2 := newStubTools(st2)
	r = execTool(t, tools2["memory_delete"], `{"id":"mX"}`)
	if r["ok"] != false || !strings.Contains(anyString(r["error"]), "unknown id") {
		t.Fatalf("delete(mX) resp = %v, want ok=false unknown id", r)
	}
}

// TestMemoryToolsRoundtripRealStore 集成（brief Step 1 ④）：save/list/delete
// 直写工作区文件，重放（重新 Open）一致——save 落盘可检索、delete 后检索不再命中。
func TestMemoryToolsRoundtripRealStore(t *testing.T) {
	t.Setenv("MULIX_MEMORY_SESSION", "s-real")
	dir := newToolsDir(t)
	tools := toolsByDir(t, dir)

	sr := execTool(t, tools["memory_save"], `{"text":"记住本项目用 pnpm 管理依赖"}`)
	if sr["ok"] != true || sr["id"] != "m1" {
		t.Fatalf("save resp = %v, want ok=true id=m1", sr)
	}
	// 直写文件：entries.jsonl 落了记录（cwd 语义下的工作区直写面）
	raw, err := os.ReadFile(filepath.Join(dir, "entries.jsonl"))
	if err != nil {
		t.Fatalf("entries.jsonl: %v", err)
	}
	if !strings.Contains(string(raw), "记住本项目用 pnpm 管理依赖") {
		t.Fatalf("entries.jsonl missing saved text: %s", raw)
	}

	// 重放一致：重新 Open 后 List 命中、Search 可召回
	replayed, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	entries, err := replayed.List()
	if err != nil || len(entries) != 1 || entries[0].SourceSession != "s-real" {
		t.Fatalf("replayed list = %v err=%v, want 1 entry stamped s-real", entries, err)
	}
	if hits := replayed.Search("pnpm", 3, 2000); len(hits) != 1 {
		t.Fatalf("search after save = %v, want 1 hit", hits)
	}

	// list 工具读到同一条
	lr := execTool(t, tools["memory_list"], `{}`)
	if !strings.Contains(anyString2(lr), "m1") {
		t.Fatalf("list resp missing m1: %v", lr)
	}

	// delete → 重放不再命中（tombstone 收敛）
	dr := execTool(t, tools["memory_delete"], `{"id":"m1"}`)
	if dr["ok"] != true {
		t.Fatalf("delete resp = %v", dr)
	}
	replayed2, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("reopen2: %v", err)
	}
	if hits := replayed2.Search("pnpm", 3, 2000); len(hits) != 0 {
		t.Fatalf("search after delete = %v, want 0 hits", hits)
	}
	if entries2, _ := replayed2.List(); len(entries2) != 0 {
		t.Fatalf("list after delete = %v, want empty", entries2)
	}
}

// TestMemoryToolsOpenFailureFailsClosed 钉 Open 失败 → 工具错误应答（不崩溃）。
func TestMemoryToolsOpenFailureFailsClosed(t *testing.T) {
	// 记忆目录位置被一个普通文件占据 → MkdirAll/Open 必败
	ws := t.TempDir()
	blocker := filepath.Join(ws, ".mulix", "memory")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	tools := toolsByDir(t, blocker) // dir 即该文件路径 → Open 失败
	for _, name := range []string{"memory_save", "memory_list", "memory_delete"} {
		args := `{}`
		if name == "memory_save" {
			args = `{"text":"x"}`
		}
		if name == "memory_delete" {
			args = `{"id":"m1"}`
		}
		r := execTool(t, tools[name], args)
		if r["ok"] != false || anyString(r["error"]) == "" {
			t.Fatalf("%s on broken dir resp = %v, want ok=false with error", name, r)
		}
	}
}

// TestRegisterConditionalRegistersOnce 钉条件注册（enabled 侧；Task 3 裁定：
// 注册表全局态 + 包级 Once 跨测试共享，disabled 反向断言不可靠——disabled
// 走组合根 boot face / resolveMemoryStore 断言）。
func TestRegisterConditionalRegistersOnce(t *testing.T) {
	RegisterConditional(nil, newToolsDir(t), true) // 进程内首次调用（本包测试仅此一处）
	RegisterConditional(nil, newToolsDir(t), true) // 重复调用——Once 拦截，不得 panic
	names := registry.Registered()
	for _, want := range []string{"memory_save", "memory_list", "memory_delete"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("RegisterConditional(true) missing %q in %v", want, names)
		}
	}
}

// TestDir 钉目录口径：<workspace>/.mulix/memory（design 002 §2 裁决 3）。
func TestDir(t *testing.T) {
	got := Dir("w")
	if want := filepath.Join("w", ".mulix", "memory"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

// ---- 小工具 ----

// newToolsDir 返回一个空的临时目录（既可直接作 memory dir 传 newToolsForDir，
// 也可作 workspace 传 RegisterConditional——后者在其下拼 .mulix/memory）。
func newToolsDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

type errorString string

func (e errorString) Error() string { return string(e) }

func anyString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func anyString2(m map[string]any) string {
	raw, _ := json.Marshal(m)
	return string(raw)
}
