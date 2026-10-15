package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

// ---- /resume 会话选择器（023 FR-009；设计 §7.5）----

// pickerTestModel 构造带 3 条会话摘要替身与换绑接缝替身的模型（新在前——
// cli 侧 ScanSessions 排序口径）。
func pickerTestModel(t *testing.T) (*tuiModel, *stubController, *fakeSwapper) {
	t.Helper()
	m, stub, _ := newTestModel(t)
	sw := &fakeSwapper{sink: m.sink, ctrl: &stubController{}, id: "s-picked"}
	m.swapper = sw
	m.opts.ListSessions = func() []SessionSummary {
		return []SessionSummary{
			{ID: "sess-a", EngineID: "eng-a", Model: "m1", Workspace: "w1", CreatedAt: "2026-10-15T10:00:00Z"},
			{ID: "sess-b", EngineID: "eng-b", Model: "m2", Workspace: "w2", CreatedAt: "2026-10-15T11:00:00Z", Unattended: true},
			{ID: "sess-c", EngineID: "eng-c", Model: "m3", Workspace: "w3", CreatedAt: "2026-10-15T12:00:00Z"},
		}
	}
	return m, stub, sw
}

// openPicker 经无参 /resume 打开选择器（键序 = 真实用户路径）。
func openPicker(t *testing.T, m *tuiModel) *tuiModel {
	t.Helper()
	m = update(t, m, typedInput("/resume"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

// TestResumePickerListsAndSwaps pins：无参 /resume 弹选择器（3 条摘要全列、
// 视图含 id/引擎 id/无人值守标注）→ ↓ 选中第 2 项 → Enter 关面板并换绑——
// swapper 收到该条的 EngineID（/resume <id> 同一接缝），ctrl/selfID 重绑、
// swapping 门禁随回执清除。
func TestResumePickerListsAndSwaps(t *testing.T) {
	m, _, sw := pickerTestModel(t)
	m = openPicker(t, m)
	if m.picker == nil {
		t.Fatal("bare /resume must open the picker")
	}
	if len(m.picker.items) != 3 || m.picker.sel != 0 {
		t.Fatalf("picker = %d items sel=%d, want 3 items sel=0", len(m.picker.items), m.picker.sel)
	}
	view := m.View().Content
	for _, want := range []string{"sess-a", "eng-a", "sess-b", "无人值守", "sess-c"} {
		if !strings.Contains(view, want) {
			t.Fatalf("picker view missing %q:\n%s", want, view)
		}
	}
	// 选择第 2 项并确认
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.picker == nil || m.picker.sel != 1 {
		t.Fatalf("sel = %v, want 1", m.picker)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.picker != nil {
		t.Fatal("picker must close on accept")
	}
	if len(sw.opens) != 1 || sw.opens[0] != "eng-b" {
		t.Fatalf("swapper opens = %v, want [eng-b]", sw.opens)
	}
	if m.selfID != "s-picked" {
		t.Fatalf("selfID = %q, want s-picked（换绑回执重绑）", m.selfID)
	}
	if m.swapping {
		t.Fatal("swap receipt must clear the busy latch")
	}
}

// TestResumePickerEsc pins：Esc 关闭不动换绑接缝（CloseCurrent/Open 零调用）；
// 关闭后可重入；未注入 ListSessions / 空表 → 留痕不进面板态；忙门禁（回合
// 进行中 /resume 不进选择器）；空链条目 Enter fail-closed 拒绝（不静默退化为
// /new 语义）。
func TestResumePickerEsc(t *testing.T) {
	m, _, sw := pickerTestModel(t)
	m = openPicker(t, m)
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.picker != nil {
		t.Fatal("esc must close the picker")
	}
	if sw.closed != 0 || len(sw.opens) != 0 {
		t.Fatalf("esc must not touch the swapper: closed=%d opens=%v", sw.closed, sw.opens)
	}
	// 重入：再次无参 /resume 可重开（选择态复位首项）
	m = openPicker(t, m)
	if m.picker == nil || m.picker.sel != 0 {
		t.Fatal("picker must be re-openable with fresh selection")
	}

	// 未注入回调 → 留痕不进面板态（防御性，生产组合根必注入）
	m2, _, _ := newTestModel(t)
	m2 = openPicker(t, m2)
	if m2.picker != nil || !strings.Contains(m2.View().Content, "[resume]") {
		t.Fatal("nil ListSessions must append a notice, not open the picker")
	}

	// 空表 → 留痕无可续链会话
	m3, _, _ := newTestModel(t)
	m3.opts.ListSessions = func() []SessionSummary { return nil }
	m3 = openPicker(t, m3)
	if m3.picker != nil || !strings.Contains(m3.View().Content, "没有可续链") {
		t.Fatal("empty list must append a notice, not open the picker")
	}

	// 忙门禁：回合进行中 Enter 被门禁，不进选择器（composer 邻接键序与提交
	// 同门禁——门禁在清空之前，文本保留）
	m4, _, _ := pickerTestModel(t)
	m4 = update(t, m4, typedInput("/resume"))
	m4.running = true
	m4 = update(t, m4, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m4.picker != nil {
		t.Fatal("busy gate must keep the picker closed")
	}
	if m4.composerValue() != "/resume" {
		t.Fatalf("busy gate must not swallow text: %q", m4.composerValue())
	}

	// 空链条目 fail-closed：EngineID 为空的选中项 Enter 拒绝换绑（Open("")
	// 会静默新开引擎会话——实测陷阱）
	m5, _, sw5 := pickerTestModel(t)
	m5.opts.ListSessions = func() []SessionSummary {
		return []SessionSummary{{ID: "sess-x", EngineID: "", Model: "m", Workspace: "w", CreatedAt: "2026-10-15T09:00:00Z"}}
	}
	m5 = openPicker(t, m5)
	m5 = update(t, m5, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m5.picker != nil {
		t.Fatal("picker must close even on the reject path")
	}
	if sw5.closed != 0 || len(sw5.opens) != 0 {
		t.Fatalf("empty engine id must not swap: closed=%d opens=%v", sw5.closed, sw5.opens)
	}
	if !strings.Contains(m5.View().Content, "无法续链") {
		t.Fatal("empty engine id must append a reject notice")
	}
}
