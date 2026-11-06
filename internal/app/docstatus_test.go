package app

import (
	"testing"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 状态栏选的语言、缩进记进会话（Tab.Lang / Tab.Indent），重开窗口和 ⇧⌘T 都放回；和设置一样的缩进不另记，
// 设置不是"按语言"时选"按语言默认"记成 auto。
func TestDocLookSession(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	a.St.Settings.Indent = "4"
	d := a.OpenFile(a.Repos[0], "a.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	a.SetDocLang(d, "Python")
	a.SetDocIndent(d, "4")
	if d.Lang != "Python" || d.Indent != "" || d.Buf.Language() == nil || d.Buf.Language().Name != "Python" {
		t.Fatalf("选 Python、4 个空格：%q %q", d.Lang, d.Indent)
	}
	a.SetDocIndent(d, "")
	if d.Indent != docs.IndentAuto || a.DocIndent(d) != "" {
		t.Fatalf("按语言默认：%q", d.Indent)
	}
	a.SetDocIndent(d, "2")
	if tab, _ := sessionTab(d); tab.Lang != "Python" || tab.Indent != "2" {
		t.Fatalf("会话里记成 %+v", tab)
	}
	a.SaveState()
	a.Close()
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if s := st.Sessions[ws]; s == nil || len(s.Tabs) != 1 || s.Tabs[0].Lang != "Python" || s.Tabs[0].Indent != "2" {
		t.Fatalf("存的是 %+v", s)
	}

	b, wait := newTestAppIn(t, ws, stateDir)
	b.restoreTabs()
	t.Cleanup(b.Close)
	bd := b.Docs[0]
	wait("重开读完", func() bool { return bd.Buf != nil && !bd.Loading })
	if l := bd.Buf.Language(); l == nil || l.Name != "Python" || bd.Indent != "2" || b.IndentLabel(bd) != "空格 2" {
		t.Fatalf("重开：%v %q %q", l, bd.Indent, b.IndentLabel(bd))
	}
	b.CloseDoc(bd)
	b.ReopenClosed()
	again := b.CurrentDoc()
	wait("⇧⌘T 读完", func() bool { return again.Buf != nil && !again.Loading })
	if l := again.Buf.Language(); l == nil || l.Name != "Python" || again.Indent != "2" {
		t.Fatalf("⇧⌘T：%v %q", l, again.Indent)
	}
	b.SetDocLang(again, "")
	if l := again.Buf.Language(); l != nil || again.Lang != "" { // a.txt 按文件名是纯文本
		t.Fatalf("回到按文件名：%v %q", l, again.Lang)
	}
	if tab, _ := sessionTab(again); tab.Lang != "" {
		t.Errorf("回到按文件名还记着 %q", tab.Lang)
	}
}
