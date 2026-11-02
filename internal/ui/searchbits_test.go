package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 搜索小功能的界面：替换框和行上的按钮、最近搜索、"在此搜索"和范围胶囊。都在临时仓库上测。

// replaceRepo 建一个仓库 demo：a.txt 三处 foo，docs/b.md 一处。
func replaceRepo(t *testing.T) (*testutil.Repo, string) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.txt", "foo one\nfoo two\nfoo three\n")
	r.Write("docs/b.md", "# foo b\n")
	r.Commit("初始")
	return r, testutil.Workspace(t, r.Root)
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// hoverOn 把指针挪到 label 那一行上，画一帧。
func hoverOn(t *testing.T, tt *wui.Tester, label string) {
	t.Helper()
	b, ok := tt.Find(label)
	if !ok {
		t.Fatalf("没有 %q；texts=%q", label, tt.Texts())
	}
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
}

// ⇧⌘H 打开替换框：结果里有换成什么，行上的按钮逐处 / 逐个文件替换、排除，⌘↩ / ⌫，全部替换先问。
func TestReplaceRow(t *testing.T) {
	repo, ws := replaceRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	s := &a.Search
	// 等搜完、结果里有 n 处 q（输入后要等去抖的定时器，Done 可能还是上一次的）
	searched := func(q string, n int) {
		t.Helper()
		wait("搜 "+q, func() bool {
			ms := s.Res.Matches
			return s.Done && !s.Running && s.Res.Err == nil && len(ms) == n && strings.Contains(ms[0].Preview, q)
		})
		tt.Frame()
	}

	tt.Key(wui.Cmd|wui.Shift, wui.KeyH)
	tt.Frame()
	if a.St.SidebarMode != workspace.ModeSearch || !s.Replacing || !tt.Focused("搜索文字") {
		t.Fatalf("⇧⌘H：mode=%q replacing=%v，搜索框没有焦点", a.St.SidebarMode, s.Replacing)
	}
	mustText(t, tt, "全部替换（0 处）")
	tt.Type("foo")
	searched("foo", 4)
	mustClick(t, tt, "替换为")
	tt.Type("bar")
	tt.Frame()
	mustText(t, tt, "全部替换（4 处）")
	if s.Preview(0) != "bar" {
		t.Errorf("预览 %q", s.Preview(0))
	}
	accessCheck(t, tt, "搜索并替换")

	// 指针在一处上：替换这一处 / 排除
	hoverOn(t, tt, "结果 demo/a.txt:1:1")
	mustClick(t, tt, "替换这一处")
	if got := mustRead(t, filepath.Join(repo.Root, "a.txt")); got != "bar one\nfoo two\nfoo three\n" {
		t.Fatalf("替换这一处：%q", got)
	}
	if tt.HasText("结果 demo/a.txt:1:1") || !tt.HasText("替换了 1 处，1 个文件") {
		t.Fatalf("换掉的那处还在，或没有提示；texts=%q", tt.Texts())
	}
	hoverOn(t, tt, "结果 demo/a.txt:2:1")
	mustClick(t, tt, "排除")
	mustText(t, tt, "全部替换（2 处）")

	// 键盘：点一处（开了预览标签、焦点在列表上），⌘↩ 换它，开着的标签跟着变；⌫ 排除
	mustClick(t, tt, "结果 demo/a.txt:3:1")
	d := a.CurrentDoc()
	wait("读 a.txt", func() bool { return d != nil && d.Buf != nil })
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyEnter)
	tt.Frame()
	if d.Buf.String() != "bar one\nfoo two\nbar three\n" || d.Modified() || mustRead(t, filepath.Join(repo.Root, "a.txt")) != d.Buf.String() {
		t.Fatalf("⌘↩：缓冲区 %q modified=%v", d.Buf.String(), d.Modified())
	}
	mustText(t, tt, "全部替换（1 处）")

	// 全部替换：先问，取消不换；点了换完、Git 栏有 M
	mustClick(t, tt, "全部替换（1 处）")
	if !a.ConfirmOpen || !tt.HasText("把 1 处换成 “bar”？") {
		t.Fatalf("没先问；texts=%q", tt.Texts())
	}
	mustClick(t, tt, "取消")
	if mustRead(t, filepath.Join(repo.Root, "docs", "b.md")) != "# foo b\n" {
		t.Fatal("取消了还换")
	}
	tt.Key(0, wui.KeyUp) // 列表还有焦点：选回 b.md 那一处
	tt.Frame()
	mustClick(t, tt, "替换为")
	tt.Key(0, wui.KeyEnter) // 替换框里回车也是全部替换（问）
	tt.Frame()
	mustClick(t, tt, "全部替换")
	if got := mustRead(t, filepath.Join(repo.Root, "docs", "b.md")); got != "# bar b\n" {
		t.Fatalf("全部替换：%q", got)
	}
	if mustRead(t, filepath.Join(repo.Root, "a.txt")) != "bar one\nfoo two\nbar three\n" {
		t.Error("排除的那处也换了")
	}
	wait("Git 状态", func() bool { return idle(r)() && r.Status.Unstaged == 2 })

	// 收起替换框：结果里只有排除，没有替换
	mustClick(t, tt, "隐藏替换")
	if s.Replacing {
		t.Fatal("没收起")
	}
	mustClick(t, tt, "搜索文字")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("bar")
	searched("bar", 3)
	hoverOn(t, tt, "结果 demo/a.txt:1:1")
	if _, ok := tt.Find("替换这一处"); ok {
		t.Error("收起替换框了还有替换这一处")
	}
	mustClick(t, tt, "排除")
	// 文件行：右键排除这个文件；⌫ 在列表上排除选中的
	mustRightClick(t, tt, "结果文件 demo/docs/b.md", "排除这个文件")
	if tt.HasText("结果文件 demo/docs/b.md") {
		t.Error("排除的文件还在")
	}
	mustClick(t, tt, "结果 demo/a.txt:3:1")
	tt.Key(0, wui.KeyBackspace)
	tt.Frame()
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "0 处，0 个文件（排除了 3 处）") {
		t.Errorf("摘要 %q", text)
	}
}

// 最近搜索：搜索框空着时列出来，点一条拿它来搜，× 去掉一条，清除全清；搜索框里 ↑ 翻。
func TestRecentList(t *testing.T) {
	_, ws := replaceRepo(t)
	a, tt, wait := newUI(t, ws, "")
	s := &a.Search
	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	mustText(t, tt, "在所有仓库里搜文字") // 没有最近搜索：原来的说明
	for _, q := range []string{"foo", "one", "two"} {
		tt.Key(wui.Cmd, wui.KeyA)
		tt.Type(q)
		tt.Key(0, wui.KeyEnter)
		tt.Frame()
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Key(0, wui.KeyBackspace)
	tt.Frame()
	mustText(t, tt, "最近搜索")
	for _, q := range []string{"two", "one", "foo"} {
		mustText(t, tt, "最近搜索 "+q)
	}
	accessCheck(t, tt, "最近搜索")

	// ↑ 翻到最近那条，再 ↑ 是上一条，↓ 回来
	tt.Key(0, wui.KeyUp)
	tt.Frame()
	if s.Query.Text != "two" {
		t.Fatalf("↑：%q", s.Query.Text)
	}
	tt.Key(0, wui.KeyUp)
	tt.Key(0, wui.KeyDown)
	tt.Key(0, wui.KeyDown)
	tt.Frame()
	if s.Query.Text != "" || tt.HasText("最近搜索 two") == false {
		t.Fatalf("↓ 回到空的：%q", s.Query.Text)
	}

	mustClick(t, tt, "最近搜索 one")
	wait("搜 one", func() bool { return s.Done && !s.Running && s.Query.Text == "one" })
	if a.St.SearchHistory[0].Text != "one" || !tt.Focused("搜索文字") {
		t.Fatalf("点了一条：%+v", a.St.SearchHistory)
	}
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Key(0, wui.KeyBackspace)
	tt.Frame()
	hoverOn(t, tt, "最近搜索 foo")
	mustClick(t, tt, "从最近搜索里去掉 foo")
	if tt.HasText("最近搜索 foo") || len(a.St.SearchHistory) != 2 {
		t.Fatalf("去掉一条：%+v", a.St.SearchHistory)
	}
	mustClick(t, tt, "清除最近搜索")
	if len(a.St.SearchHistory) != 0 {
		t.Fatalf("清除：%+v", a.St.SearchHistory)
	}
	mustText(t, tt, "在所有仓库里搜文字")
}

// 文件树右键目录"在此搜索"、仓库"在此仓库里搜索"、结果文件"只在这个目录里搜"：范围胶囊，× 搜全部。
func TestSearchInMenu(t *testing.T) {
	_, ws := replaceRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	s := &a.Search
	a.ShowSidebar(workspace.ModeFiles)
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("树里有 docs", func() bool { return !r.Files.Loading && tt.HasText("docs") })
	s.Query.Text = "foo"

	mustRightClick(t, tt, "docs", "在此搜索")
	wait("搜索", func() bool { return s.Done && !s.Running })
	tt.Frame()
	// 胶囊上的字：HasText 是包含，结果行的 Label 里也有 demo/docs，所以用 Find 整段比
	if _, chip := tt.Find("demo/docs"); a.St.SidebarMode != workspace.ModeSearch || s.Scope == nil || !chip {
		t.Fatalf("在此搜索：mode=%q scope=%+v", a.St.SidebarMode, s.Scope)
	}
	if !tt.HasText("结果文件 demo/docs/b.md") || tt.HasText("结果文件 demo/a.txt") {
		t.Fatalf("范围外的也搜了；texts=%q", tt.Texts())
	}
	accessCheck(t, tt, "范围胶囊")
	mustClick(t, tt, "清除范围")
	wait("搜全部", func() bool { return s.Done && !s.Running && len(s.Res.Matches) == 4 })
	tt.Frame()
	if _, chip := tt.Find("demo/docs"); s.Scope != nil || chip {
		t.Fatal("× 没去掉范围")
	}

	// 结果文件行右键：只在这个目录里搜
	mustRightClick(t, tt, "结果文件 demo/docs/b.md", "只在这个目录里搜")
	wait("搜索", func() bool { return s.Done && !s.Running })
	if s.Scope == nil || s.Scope.Label() != "demo/docs" || len(s.Res.Matches) != 1 {
		t.Fatalf("只在这个目录里搜：%+v %d", s.Scope, len(s.Res.Matches))
	}

	// 仓库行右键：在此仓库里搜索
	a.ShowSidebar(workspace.ModeFiles)
	tt.Frame()
	mustRightClick(t, tt, "demo（仓库）", "在此仓库里搜索")
	wait("搜索", func() bool { return s.Done && !s.Running && len(s.Res.Matches) == 4 })
	if s.Scope == nil || s.Scope.Label() != "demo" {
		t.Fatalf("在此仓库里搜索：%+v", s.Scope)
	}
}
