package ui

import (
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
)

// 搜索编辑器：搜索框里 ⌥⏎ 搜完开成只读的纯文本标签，顶上一行是怎么打开、上下文行数、重新搜索；
// 每处标底色，光标放在匹配行上回车打开那个文件、选中那一处；摘要行的图标也开它。
func TestSearchEditorUI(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))

	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	tt.Type("改过")
	tt.Key(wui.Alt, wui.KeyEnter)
	wait("开成搜索编辑器", func() bool {
		d := a.CurrentDoc()
		return d != nil && d.Kind == docs.SearchDoc && d.Buf != nil && !d.Loading
	})
	tt.Frame()
	d := a.CurrentDoc()
	if d.Title != "搜索: 改过" || !strings.Contains(d.Buf.String(), "demo/a.txt:\n") || !strings.Contains(d.Buf.String(), "20: 第 20 行（改过）") {
		t.Fatalf("%s：%q", d.Title, d.Buf.String())
	}
	mustText(t, tt, "双击、回车或 ⌘点一行，打开那个文件到那一行")
	mustText(t, tt, "上下文 2 行")
	mustText(t, tt, "只读 · 搜索结果")
	if len(d.St.TextBackgrounds) != 1 {
		t.Errorf("底色 %+v", d.St.TextBackgrounds)
	}
	accessCheck(t, tt, "搜索编辑器")

	// 光标放到匹配行上回车：打开 a.txt、选中那一处
	line := -1
	for i, h := range d.Hits {
		if len(h.Marks) > 0 {
			line = i
		}
	}
	d.JumpTo(docs.Jump{Line: line})
	a.FocusDoc = true
	tt.Frame()
	tt.Frame()
	tt.Key(0, wui.KeyEnter)
	wait("打开 a.txt", func() bool { f := a.CurrentDoc(); return f.Kind == docs.File && f.Buf != nil })
	tt.Frame()
	if f := a.CurrentDoc(); f.Rel != "a.txt" || f.St.SelectedText() != "改过" || f.St.CursorPos().Line != 19 {
		t.Fatalf("回车打开了 %s，选中 %q", f.Rel, f.St.SelectedText())
	}

	// 摘要行的图标：开回同一个标签
	n := len(a.AllDocs())
	mustClick(t, tt, "在编辑器里打开")
	wait("切回搜索编辑器", func() bool { return a.CurrentDoc() == d && !d.Loading })
	if len(a.AllDocs()) != n {
		t.Errorf("又开了一个标签：%d → %d", n, len(a.AllDocs()))
	}
}
