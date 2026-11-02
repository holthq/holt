package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 面包屑和后退 / 前进：仓库段的菜单列出顶层、从子菜单打开文件；目录段"在文件树里显示"；
// 文件段点了在树里选中它；⌃- / ⌃⇧- 和标题栏的 ‹ › 在打开过的位置间来回。
func TestBreadcrumbAndNav(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("# 指南\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.OpenFile(r, "README.md", true)
	wait("README 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	a.EnsureFiles(r)
	wait("文件列表", func() bool { return r.Files.Loaded })
	tt.Frame()

	// 仓库段：菜单里是"在文件树里显示""在此打开终端"、子目录（子菜单）和顶层文件
	mustClick(t, tt, "位置 demo")
	menu := tt.Menu()
	if want := []string{"在文件树里显示", "在此打开终端", "-", "docs", "a.txt", "README.md", "u.txt"}; !slices.Equal(menu, want) {
		t.Fatalf("仓库段的菜单 %q，要 %q", menu, want)
	}
	if err := tt.ChooseMenuItem("docs", "guide.md"); err != nil {
		t.Fatal(err)
	}
	wait("guide.md 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == "docs/guide.md" && d.Buf != nil })
	if !a.CurrentDoc().Pinned {
		t.Error("从面包屑菜单打开应固定")
	}
	tt.Frame()

	// 目录段：在文件树里显示 → 侧栏切到文件，选中并展开 docs
	a.St.Sidebar = false
	mustClick(t, tt, "位置 demo/docs")
	if err := tt.ChooseMenuItem("在文件树里显示"); err != nil {
		t.Fatalf("%v；菜单=%q", err, tt.Menu())
	}
	tt.Frame()
	if row, ok := a.TreeRowAt(a.FileSel); !ok || !row.Dir || row.Path != "docs" || !row.Open || !a.St.Sidebar {
		t.Fatalf("目录段没在树里显示 docs：%+v sidebar=%v", row, a.St.Sidebar)
	}
	// 文件段：点了在树里选中这个文件
	mustClick(t, tt, "位置 demo/docs/guide.md")
	if row, _ := a.TreeRowAt(a.FileSel); row.Path != "docs/guide.md" || row.Dir {
		t.Fatalf("文件段没在树里选中它：%+v", row)
	}

	// 后退 / 前进：快捷键和标题栏按钮
	back, fwd := app.Tip("后退", "nav.back"), app.Tip("前进", "nav.forward")
	at := func(rel string) {
		t.Helper()
		tt.Frame()
		if d := a.CurrentDoc(); d == nil || d.Rel != rel {
			t.Fatalf("应在 %s，现在 %+v", rel, d)
		}
	}
	tt.Key(wui.Ctrl, wui.KeyMinus)
	at("README.md")
	tt.Key(wui.Ctrl|wui.Shift, wui.KeyMinus)
	at("docs/guide.md")
	mustClick(t, tt, back)
	at("README.md")
	mustClick(t, tt, fwd)
	at("docs/guide.md")
	if a.CanForward() {
		t.Error("走到头了还能前进")
	}

	// 目录多于两层：中间的收成"…"，它的菜单列出收起的目录
	deep := filepath.Join(root, "x", "y", "z", "w")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "deep.md"), []byte("深\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.OpenPath(filepath.Join(deep, "deep.md"))
	tt.Frame()
	for _, s := range []string{"位置 demo/x", "位置 省略的目录", "位置 demo/x/y/z/w", "位置 demo/x/y/z/w/deep.md"} {
		mustText(t, tt, s)
	}
	if hasLabel(tt, "位置 demo/x/y") || hasLabel(tt, "位置 demo/x/y/z") {
		t.Fatalf("中间的目录没收起来：%q", tt.Texts())
	}
	a.LoadFiles(a.Repos[0])
	wait("文件列表", func() bool { return a.Repos[0].Files.Dir("x/y/z") != nil })
	mustClick(t, tt, "位置 省略的目录")
	if menu := tt.Menu(); !slices.Equal(menu, []string{"y", "z"}) {
		t.Fatalf("… 的菜单 %q", menu)
	}
	if err := tt.ChooseMenuItem("z", "在文件树里显示"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if row, _ := a.TreeRowAt(a.FileSel); !row.Holds("x/y/z") || !row.Dir { // x/y/z/w 合并成一行，选中那一行
		t.Fatalf("… → z → 在文件树里显示 选中的是 %+v", row)
	}
}

// hasLabel 是界面上有没有正好是 s 的文字或标签。
func hasLabel(tt *wui.Tester, s string) bool { return slices.Contains(tt.Texts(), s) }
