package ui

import (
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 编辑器的查找 / 替换 / 跳行（mygo 的 FindBar、GoToLineBar，中文文案）：编辑器有焦点时它自己接
// ⌘F / ⌥⌘F / ⌃G；焦点在别处（筛选框）时窗口接到同一个键；命令面板里也有。⌥⌘F 在文件标签里是替换，
// 在 Diff 标签里是全文 diff。
func TestFindReplaceGoToLine(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenFile(r, "a.txt", true)
	wait("a.txt", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	d := a.CurrentDoc()
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	if !d.St.Focused() {
		t.Fatal("编辑器没拿到焦点")
	}

	// 编辑器有焦点：⌘F 打开查找条，焦点在查找框里，敲字就找
	tt.Key(wui.Cmd, wui.KeyF)
	tt.Frame()
	if !tt.Focused("查找") || !tt.HasText("区分大小写") {
		t.Fatal("⌘F 没打开查找条或焦点不在查找框")
	}
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("第 2")
	tt.Frame()
	if n, _ := d.St.FindMatches(); n != 11 { // 第 2 行、第 20–29 行
		t.Fatalf("“第 2” 找到 %d 处", n)
	}
	mustText(t, tt, "共 11 项")
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if tt.HasText("区分大小写") {
		t.Fatal("Esc 没关掉查找条")
	}

	// ⌥⌘F：替换。Tab 走到替换框（在"关闭"按钮后面），敲替换文字，点"全部替换"
	tt.Key(wui.Cmd|wui.Alt, wui.KeyF)
	tt.Frame()
	mustText(t, tt, "全部替换")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("（改过）")
	tt.Frame()
	pastClose := false
	for range 20 {
		tt.Key(0, wui.KeyTab)
		tt.Frame()
		if tt.Focused("关闭") {
			pastClose = true
		} else if pastClose && tt.Focused("替换") {
			break
		}
	}
	if !pastClose || !tt.Focused("替换") {
		t.Fatal("Tab 走不到替换框")
	}
	tt.Type("（替换了）")
	mustClick(t, tt, "全部替换")
	tt.Frame()
	if s := d.Buf.String(); !strings.Contains(s, "第 20 行（替换了）\n") || !d.Modified() {
		t.Fatalf("全部替换没改到：modified=%v", d.Modified())
	}
	d.St.Run(editor.Undo)
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if d.Modified() {
		t.Fatal("撤销后应回到没改的样子")
	}

	// ⌃G：跳到行
	d.St.GoTo(editor.Pos{})
	a.FocusDoc = true
	tt.Frame()
	tt.Key(wui.Ctrl, wui.KeyG)
	tt.Frame()
	if !tt.Focused("转到行") {
		t.Fatal("⌃G 没打开跳行条")
	}
	tt.Type("30")
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if pos, _ := d.Cursor(); pos.Line != 29 || tt.HasText("第 1 行，共 41 行") {
		t.Fatalf("⌃G 30 后在第 %d 行", pos.Line+1)
	}

	// 焦点在别处（侧栏的筛选框）：窗口接到 ⌘F，开的还是当前标签的查找条
	mustClick(t, tt, "筛选文件")
	tt.Key(wui.Cmd, wui.KeyF)
	tt.Frame()
	if !tt.Focused("查找") {
		t.Fatal("焦点在筛选框时 ⌘F 没打开编辑器的查找条")
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()

	// 命令面板：">跳到行" 回车打开跳行条
	tt.Key(wui.Cmd|wui.Shift, wui.KeyP)
	tt.Type("跳到行")
	tt.Frame()
	mustText(t, tt, "命令 跳到行…")
	mustText(t, tt, app.Find("edit.goToLine").Keys())
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if a.Palette.Open || !tt.Focused("转到行") {
		t.Fatalf("面板里的跳到行没打开跳行条：open=%v", a.Palette.Open)
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()

	// ⌥⌘F 在 Diff 标签里是全文 diff（同一个键按当前标签分派）
	a.OpenDiff(r, r.Status.Changes[0], false)
	tt.Frame()
	full := a.St.FullDiff
	tt.Key(wui.Cmd|wui.Alt, wui.KeyF)
	tt.Frame()
	if a.St.FullDiff == full {
		t.Fatal("Diff 标签里 ⌥⌘F 没切全文 diff")
	}
	if tt.HasText("全部替换") {
		t.Fatal("Diff 标签里 ⌥⌘F 不该开替换")
	}
	// 没有编辑器的标签上查找类命令不能用
	if a.CanRun(app.Find("edit.find")) {
		t.Error("Diff 标签上查找应不能用")
	}
}
