package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// selPath 是文件树光标那行的仓库和路径（没有为 nil, ""）。
func selPath(a *App) (*Repo, string) {
	row, ok := a.TreeRowAt(a.FileSel)
	if !ok {
		return nil, ""
	}
	return row.Repo, row.Path
}

func TestFollowCurrent(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	other := testutil.NewRepo(t, "other")
	other.Write("docs/guide.md", "# guide\n")
	other.Write("a.txt", "other a\n")
	other.Commit("init")
	a, wait := newTestApp(t, testutil.Workspace(t, root, other.Root))
	r, o := a.Repos[0], a.Repos[1]
	a.EnsureFiles(r)
	wait("读文件列表", func() bool { return r.Files.Loaded })
	frame := func() bool { return a.FollowCurrent() }
	docIdx := func(rr *Repo, rel string) int {
		return slices.IndexFunc(a.Docs, func(d *docs.Doc) bool { return d.Root == rr.Root && d.Rel == rel })
	}

	// 从别处打开另一个仓库的文件：树展开那个仓库（还没读，读到再选）；当前仓库、侧栏模式都不变
	a.OpenFile(o, "docs/guide.md", true)
	if !frame() {
		t.Fatal("换了当前标签，树应该跟")
	}
	wait("读第二个仓库并选中", func() bool { rr, p := selPath(a); return rr == o && p == "docs/guide.md" })
	if a.Current() != r || a.St.SidebarMode != workspace.ModeFiles || !o.Expanded {
		t.Fatalf("跟随不该换当前仓库 %v / 模式 %s / 展开 %v", a.Current().Name, a.St.SidebarMode, o.Expanded)
	}
	// 同一个标签不再跟：收起目录后不会又被展开
	a.ToggleDir(o, "docs")
	if frame() || o.Files.Open["docs"] {
		t.Fatal("同一个标签又跟了一遍")
	}

	// 文件树自己打开的：光标已经在那行，不再动
	i := slices.IndexFunc(a.TreeRows(), func(row TreeRow) bool { return row.Repo == r && row.Path == "u.txt" })
	a.FileSel, a.FileHover = i, i
	a.chooseOnly(i)
	a.ClickTreeRow(false, false)
	if d := a.CurrentDoc(); d == nil || d.Rel != "u.txt" {
		t.Fatalf("点了 u.txt 打开的是 %+v", d)
	}
	ver := r.Files.Version
	if frame() || a.FileSel != i || r.Files.Version != ver {
		t.Fatal("树自己打开的文件不该再跟")
	}

	// 筛选时不跟，清掉筛选后跟一次
	a.SetFilter("guide")
	a.SelectDoc(docIdx(o, "docs/guide.md"))
	if frame() {
		t.Fatal("筛选时不该跟")
	}
	a.SetFilter("")
	if !frame() {
		t.Fatal("清掉筛选后应该跟一次")
	}
	if rr, p := selPath(a); rr != o || p != "docs/guide.md" || !o.Files.Open["docs"] {
		t.Fatalf("清掉筛选后选中 %v %q", rr, p)
	}

	// 侧栏在 Git 模式时不跟，切回文件模式跟一次
	a.ShowSidebar(workspace.ModeGit)
	a.SelectDoc(docIdx(r, "a.txt"))
	if a.CurrentDoc() == nil || a.CurrentDoc().Rel != "a.txt" {
		a.OpenFile(r, "a.txt", true)
	}
	if frame() {
		t.Fatal("Git 模式时不该跟")
	}
	a.ShowSidebar(workspace.ModeFiles)
	if !frame() {
		t.Fatal("切回文件模式应该跟一次")
	}
	if rr, p := selPath(a); rr != r || p != "a.txt" {
		t.Fatalf("切回文件模式后选中 %v %q", rr, p)
	}

	// 设置关掉：不跟；正在输入名字、正在拖也不跟
	a.St.Settings.TreeNoFollow = true
	a.SelectDoc(docIdx(o, "docs/guide.md"))
	if frame() {
		t.Fatal("关了跟随还在跟")
	}
	a.St.Settings.TreeNoFollow = false
	a.TreeDragging = true
	if frame() {
		t.Fatal("拖的时候不该跟")
	}
	a.TreeDragging = false
	if !frame() {
		t.Fatal("打开跟随、不拖了，应该跟上当前标签")
	}

	// 定位当前文件：不管设置，清掉筛选
	a.St.Settings.TreeNoFollow = true
	a.SelectDoc(docIdx(r, "a.txt"))
	a.SetFilter("nothing")
	a.RevealCurrent()
	if rr, p := selPath(a); a.Filter != "" || rr != r || p != "a.txt" {
		t.Fatalf("定位当前文件后筛选 %q，选中 %v %q", a.Filter, rr, p)
	}
	a.St.Settings.TreeNoFollow = false
	if frame() {
		t.Fatal("定位过的标签不该再跟")
	}
}

// treeNames 是仓库 r 那段行的名字（合并行用 " / " 连起来），带深度。
func treeNames(a *App, r *Repo) []string {
	var out []string
	for _, row := range a.TreeRows() {
		if row.Repo != r || row.Root || row.Note != "" {
			continue
		}
		name := row.Label()
		if row.Edit {
			name = "[输入框]"
		}
		out = append(out, strings.Repeat("  ", row.Depth-1)+name)
	}
	return out
}

func TestCompactTree(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("internal/ui/view.go", "package ui\n")
	repo.Write("lib/one/two/three.go", "package two\n")
	repo.Write("docs/x.md", "x\n")
	repo.Write("docs/sub/y.md", "y\n")
	repo.Commit("init")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Current()
	a.EnsureFiles(r)
	wait("读文件列表", func() bool { return hasFile(r, "docs/x.md") })
	want := func(what string, names ...string) {
		t.Helper()
		if got := treeNames(a, r); !slices.Equal(got, names) {
			t.Fatalf("%s：%q", what, got)
		}
	}
	want("合并只有一个子目录的目录", "docs", "internal / ui", "lib / one / two")

	// 点中间那段的箭头：整行展开（每一层都展开）
	a.ToggleDir(r, "internal")
	want("展开合并行", "docs", "internal / ui", "  view.go", "lib / one / two")
	if !r.Files.Open["internal"] || !r.Files.Open["internal/ui"] {
		t.Fatal("展开合并行应该每一层都展开")
	}
	// 点这一行：收起只收最深那段
	i := slices.IndexFunc(a.TreeRows(), func(row TreeRow) bool { return row.Path == "internal/ui" })
	a.FileSel, a.FileHover = i, i
	a.chooseOnly(i)
	a.ClickTreeRow(false, false)
	if !r.Files.Open["internal"] || r.Files.Open["internal/ui"] {
		t.Fatalf("收起合并行：%v", r.Files.Open)
	}
	// → 展开、← 收起
	a.TreeRight()
	if !r.Files.Open["internal/ui"] {
		t.Fatal("→ 没展开合并行")
	}
	a.TreeLeft()
	if r.Files.Open["internal/ui"] {
		t.Fatal("← 没收起合并行")
	}

	// 显示中间那段：选中合并行
	a.Reveal(r, "lib/one")
	if row, _ := a.TreeRowAt(a.FileSel); row.Path != "lib/one/two" || len(row.Chain) != 3 {
		t.Fatalf("显示 lib/one 选中 %+v", row.Row)
	}

	// 在中间那段新建：输入框对着的目录自己一行；取消后合回去、选中合并行
	a.StartNew(r, "lib/one", false)
	want("在 lib/one 新建", "docs", "internal / ui", "lib", "  one", "    [输入框]", "    two")
	a.CancelEdit()
	if row, _ := a.TreeRowAt(a.FileSel); row.Path != "lib/one/two" {
		t.Fatalf("取消新建后选中 %+v", row.Row)
	}

	// 中间那层多了文件：拆开
	repo.Write("lib/one/readme.md", "r\n")
	a.LoadFiles(r)
	wait("重读", func() bool { return hasFile(r, "lib/one/readme.md") })
	r.Files.SetOpen("lib/one/two", false)
	want("lib/one 有了文件", "docs", "internal / ui", "lib / one", "  two", "  readme.md")

	// 设置关掉：一层一行
	a.St.Settings.TreeNoCompact = true
	a.SettingsChanged()
	r.Files.CollapseAll()
	want("关掉合并", "docs", "internal", "lib")
}

// 空目录：整棵读之后后台找空目录放进树，被 .gitignore 忽略的不放；目录里有了文件再整棵读，
// 就不再算找到的空目录。增量刷新时冒出来的见 TestTreeIncremental。全在临时仓库上。
func TestEmptyDirsInTree(t *testing.T) {
	r0 := testutil.NewRepo(t, "empty")
	r0.Write(".gitignore", "build/\n*.tmp\n")
	r0.Write("src/a.go", "package src\n")
	r0.Commit("初始")
	for _, d := range []string{"empty/dir", "build/x", "src/cache.tmp", "src/keep"} {
		if err := os.MkdirAll(filepath.Join(r0.Root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("空目录放进树", func() bool {
		return idle(r)() && !r.Files.Loading && r.Files.Dir("empty/dir") != nil && r.Files.Dir("src/keep") != nil
	})
	for _, d := range []string{"build/x", "src/cache.tmp"} {
		if r.Files.Extra[d] {
			t.Errorf("被忽略的 %s 当成空目录放进来了", d)
		}
	}
	r0.Write("empty/dir/f.txt", "f\n")
	a.RefreshRepo(r, WhyManual)
	wait("不空了", func() bool { return idle(r)() && hasFile(r, "empty/dir/f.txt") && !r.Files.Extra["empty/dir"] })
	if !r.Files.Extra["src/keep"] {
		t.Error("还空着的 src/keep 掉了")
	}
}
