package ui

import (
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 快速打开 / 命令面板：⌘P 敲名字回车固定打开、焦点给编辑器；:行号 跳行；点标题栏的按钮和列表里的一项；
// ⇧⌘P 找命令、↓ 选、回车执行；Esc 关掉。
func TestPalette(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	p := &a.Palette
	input := "快速打开或命令"

	tt.Key(wui.Cmd, wui.KeyP)
	tt.Frame()
	if !p.Open || !tt.Focused(input) {
		t.Fatalf("⌘P 后面板没开或输入框没焦点：open=%v", p.Open)
	}
	wait("文件列表", func() bool { return r.Files.Loaded })
	tt.Frame()
	mustText(t, tt, "敲文件名在所有仓库里找")
	tt.Type("a.tx")
	tt.Frame()
	mustText(t, tt, "打开 demo/a.txt")
	if len(p.Items) != 1 {
		t.Fatalf("a.tx 应只匹配 a.txt：%d 项", len(p.Items))
	}
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	d := a.CurrentDoc()
	if p.Open || d == nil || d.Rel != "a.txt" || !d.Pinned {
		t.Fatalf("回车没固定打开 a.txt：open=%v doc=%+v", p.Open, d)
	}
	wait("读 a.txt", func() bool { return d.Buf != nil })
	tt.Frame()
	tt.Frame()
	tt.Type("新")
	tt.Frame()
	if !d.Modified() {
		t.Fatal("打开后焦点应在编辑器里（敲的字没进去）")
	}
	d.St.Run(editor.Undo)
	tt.Frame()

	// :行号 在当前文件里跳
	tt.Key(wui.Cmd, wui.KeyP)
	tt.Frame()
	tt.Type(":20")
	tt.Frame()
	mustText(t, tt, "跳到第 20 行")
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if pos, _ := d.Cursor(); pos.Line != 19 || p.Open {
		t.Fatalf("光标在 %+v，open=%v", pos, p.Open)
	}

	// 标题栏的"快速打开"：空着时列最近打开的以外的……只有 a.txt 打开过，所以只有提示；
	// 敲 readme 点那一项
	mustClick(t, tt, "快速打开")
	tt.Frame()
	if !p.Open || !tt.Focused(input) {
		t.Fatal("点标题栏的快速打开没打开面板")
	}
	tt.Type("readme")
	tt.Frame()
	mustClick(t, tt, "打开 demo/README.md")
	tt.Frame()
	if c := a.CurrentDoc(); p.Open || c == nil || c.Rel != "README.md" {
		t.Fatalf("点一项没打开 README.md：open=%v", p.Open)
	}
	// 现在空着打开列出最近的 a.txt
	tt.Key(wui.Cmd, wui.KeyP)
	tt.Frame()
	mustText(t, tt, "最近打开")
	mustText(t, tt, "打开 demo/a.txt")
	// Esc 关掉
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if p.Open || tt.HasText("最近打开") {
		t.Fatal("Esc 没关掉面板")
	}

	// ⇧⌘P：命令，显示分组和快捷键；↓↓ 选第三项，回车执行
	tt.Key(wui.Cmd|wui.Shift, wui.KeyP)
	tt.Frame()
	if !p.Open || p.Text != ">" || !tt.Focused(input) {
		t.Fatalf("⇧⌘P：open=%v text=%q", p.Open, p.Text)
	}
	mustText(t, tt, "命令 打开文件…")
	tt.Type("栏")
	tt.Frame()
	mustText(t, tt, "命令 侧栏")
	mustText(t, tt, "⌘B")
	if len(p.Items) != 5 || p.Items[0].Text != "侧栏" || p.Items[2].Text != "右栏（历史）" || p.Items[4].Kind != app.ItemTerm {
		t.Fatalf(">栏：%+v", p.Items)
	}
	right := a.St.RightBar
	tt.Key(0, wui.KeyDown)
	tt.Key(0, wui.KeyDown)
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if p.Open || a.St.RightBar == right {
		t.Fatalf("回车没执行第三项：open=%v right=%v", p.Open, a.St.RightBar)
	}
}
