package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
)

// toasted 是 s 还在 a.Toast 里，或已经被界面拿去显示了。
func toasted(a *app.App, tt *wui.Tester, s string) bool { return a.Toast == s || tt.HasText(s) }

func syms(d *docs.Doc) int {
	if o := d.Outline(); o != nil {
		return len(o.Syms)
	}
	return 0
}

// 状态栏能点的语言、换行、缩进（临时仓库）：语言菜单换了高亮和大纲跟着、多一项"按文件名自动识别"；
// 换行换成 CRLF 算改过、换回来不算，保存按它写；缩进选 2 个空格、把缩进转成空格 / Tab（一步撤销）；
// 行列按字数，选区后面有"选中 N 字""N 处选中"。
func TestStatusBarMenus(t *testing.T) {
	repo := testutil.NewRepo(t, "look")
	repo.Write("a.go", "package a\n\nfunc f() {\n\tx := \"中文\"\n\t_ = x\n}\n")
	repo.Commit("base")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	d := a.OpenFile(r, "a.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()

	// 语言
	mustClick(t, tt, "语言 Go")
	menu := shownMenu(t, tt)
	if len(menu) < 4 || menu[0] != "纯文本" || !slices.Contains(menu, "Python") || slices.Contains(menu, "按文件名自动识别") {
		t.Fatalf("语言菜单 %q", menu)
	}
	if err := chooseMenu(t, tt, "Python"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if l := d.Buf.Language(); l == nil || l.Name != "Python" || d.Lang != "Python" {
		t.Fatalf("换成 Python：%v %q", l, d.Lang)
	}
	mustText(t, tt, "Python")
	mustClick(t, tt, "语言 Python")
	if menu := shownMenu(t, tt); menu[len(menu)-1] != "按文件名自动识别" {
		t.Fatalf("手动选过的语言菜单 %q", menu)
	}
	if err := chooseMenu(t, tt, "纯文本"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.Buf.Language() != nil || d.Lang != docs.PlainLang || syms(d) != 0 {
		t.Fatalf("纯文本：%v %q 大纲 %d", d.Buf.Language(), d.Lang, syms(d))
	}
	mustClick(t, tt, "语言 纯文本")
	if err := chooseMenu(t, tt, "按文件名自动识别"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if l := d.Buf.Language(); l == nil || l.Name != "Go" || d.Lang != "" || syms(d) == 0 {
		t.Fatalf("回到按文件名：%v %q", l, d.Lang)
	}

	// 行列：列按字数（Tab 算一个），选区
	at := strings.Index(d.Buf.String(), "\"中文\"") + len("\"中文")
	d.St.Select(at, at)
	tt.Frame()
	mustText(t, tt, "行 4，列 10")
	d.St.Select(at-len("中文"), at)
	tt.Frame()
	mustText(t, tt, "行 4，列 10（选中 2 字）")
	d.St.SetSelections(editor.Selection{Anchor: 0, Head: 0}, editor.Selection{Anchor: at, Head: at})
	tt.Frame()
	mustText(t, tt, "行 4，列 10（2 处选中）")

	// 换行
	mustClick(t, tt, "换行 LF")
	if err := chooseMenu(t, tt, "CRLF"); err != nil {
		t.Fatalf("换行菜单 %v：%q", err, shownMenu(t, tt))
	}
	tt.Frame()
	if d.Buf.LineEnding() != "\r\n" || !d.Modified() {
		t.Fatalf("换成 CRLF：%q 改过 %v", d.Buf.LineEnding(), d.Modified())
	}
	mustText(t, tt, "未保存 · ⌘S")
	mustClick(t, tt, "换行 CRLF")
	chooseMenu(t, tt, "LF")
	tt.Frame()
	if d.Modified() {
		t.Fatal("换回 LF 还算改过")
	}
	mustClick(t, tt, "换行 LF")
	chooseMenu(t, tt, "CRLF")
	a.Save(d)
	if got, _ := os.ReadFile(filepath.Join(repo.Root, "a.go")); !strings.HasPrefix(string(got), "package a\r\n\r\n") || d.Modified() {
		t.Fatalf("按 CRLF 存：%q 改过 %v", got, d.Modified())
	}
	tt.Frame()

	// 缩进：Go 按语言是 Tab；选 2 个空格只管这个文件；转成空格 / Tab 一步撤销
	mustClick(t, tt, "缩进 Tab 4")
	// Grove 菜单的 shownMenu 不列分隔线
	if menu := shownMenu(t, tt); !slices.Equal(menu, []string{"按语言默认", "Tab", "2 个空格", "4 个空格", "8 个空格", "把缩进转成空格", "把缩进转成 Tab"}) {
		t.Fatalf("缩进菜单 %q", menu)
	}
	if err := chooseMenu(t, tt, "2 个空格"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.Indent != "2" || d.St.Indent != editor.IndentSpaces || d.St.TabSize != 2 {
		t.Fatalf("选 2 个空格：%q %v %d", d.Indent, d.St.Indent, d.St.TabSize)
	}
	before := d.Buf.String()
	mustClick(t, tt, "缩进 空格 2")
	if err := chooseMenu(t, tt, "把缩进转成空格"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := d.Buf.String(); got != strings.ReplaceAll(before, "\t", "  ") || !toasted(a, tt, "2 行的缩进转成了空格") {
		t.Fatalf("转成空格：%q，提示 %q", got, a.Toast)
	}
	mustClick(t, tt, "缩进 空格 2")
	chooseMenu(t, tt, "把缩进转成 Tab")
	tt.Frame()
	if d.Buf.String() != before || d.Indent != "tab" || !toasted(a, tt, "2 行的缩进转成了 Tab") {
		t.Fatalf("转回 Tab：%q %q", d.Buf.String(), d.Indent)
	}
	if !d.Buf.Undo() || d.Buf.String() != strings.ReplaceAll(before, "\t", "  ") {
		t.Errorf("一步撤销：%q", d.Buf.String())
	}
	tt.Frame()
	mustClick(t, tt, "缩进 Tab 4") // 转成 Tab 之后宽度跟语言
	if err := chooseMenu(t, tt, "按语言默认"); err != nil {
		t.Fatalf("%v %q", err, shownMenu(t, tt))
	}
	tt.Frame()
	if d.Indent != "" || d.St.Indent != editor.IndentAuto {
		t.Errorf("按语言默认（设置也是按语言）不另记：%q", d.Indent)
	}
}
