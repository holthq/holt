package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 搜索页：⇧⌘F 聚焦搜索框，敲字自动搜，点一处在预览标签里打开并选中它，回车固定；
// 三个开关、包含 / 排除、↓ 进结果、← → 收起展开。
func TestSearchPage(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	s := &a.Search
	// expect 等上次之后又搜完了一次、摘要里有 want（敲字后要等 150 ms 才搜，不能只看 Done）
	seen := 0
	expect := func(want string) {
		t.Helper()
		wait(want, func() bool {
			text, _ := a.SearchSummary()
			return s.Searches() > seen && !s.Running && strings.Contains(text, want)
		})
		seen = s.Searches()
		tt.Frame()
		mustText(t, tt, want)
	}

	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	if a.St.SidebarMode != workspace.ModeSearch || !tt.Focused("搜索文字") {
		t.Fatalf("⇧⌘F 后 mode=%q，搜索框没有焦点", a.St.SidebarMode)
	}
	mustText(t, tt, "在所有仓库里搜文字")
	tt.Type("改过")
	expect("1 处，1 个文件")
	mustText(t, tt, "结果文件 demo/a.txt")

	// 点那一处："第 20 行（改过）"里"改"在第 13 个字节
	mustClickRow(t, tt, "结果 demo/a.txt:20:14")
	d := a.CurrentDoc()
	if d == nil || d.Rel != "a.txt" || d.Pinned {
		t.Fatalf("没在预览标签里打开 a.txt：%+v", d)
	}
	wait("读 a.txt", func() bool { return d.Buf != nil })
	tt.Frame()
	if p, _ := d.Cursor(); p.Line != 19 || d.St.SelectedText() != "改过" {
		t.Fatalf("光标 %+v 选中 %q", p, d.St.SelectedText())
	}
	if !tt.Focused("结果 demo/a.txt:20:14") {
		t.Errorf("点结果后焦点应留在结果列表")
	}
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if !d.Pinned || a.CurrentDoc() != d {
		t.Errorf("回车应固定标签")
	}
	// ← 收起文件，→ 展开
	tt.Key(0, wui.KeyLeft)
	tt.Frame()
	if len(s.Rows) != 1 || s.Rows[0].Open || tt.HasText("结果 demo/a.txt:20:14") {
		t.Fatalf("← 没收起：%+v", s.Rows)
	}
	tt.Key(0, wui.KeyRight)
	tt.Frame()
	if len(s.Rows) != 2 {
		t.Fatalf("→ 没展开：%+v", s.Rows)
	}

	// 区分大小写 / 全字匹配 / 正则
	mustClick(t, tt, "搜索文字")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("# DEMO")
	expect("1 处，1 个文件")
	mustClick(t, tt, "区分大小写")
	expect("没有结果")
	mustClick(t, tt, "区分大小写")
	mustClick(t, tt, "正则表达式")
	mustClick(t, tt, "搜索文字")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type(`第 \d+ 行$`)
	expect("39 处，1 个文件")
	tt.Type("(")
	expect("正则写错了")
	mustClick(t, tt, "正则表达式")
	expect("没有结果")

	// 包含 / 排除
	mustClick(t, tt, "搜索文字")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("第")
	expect("41 处，2 个文件")
	mustClick(t, tt, "包含 / 排除的文件")
	mustClick(t, tt, "包含的文件")
	tt.Type("*.md")
	expect("1 处，1 个文件")
	mustText(t, tt, "结果文件 demo/README.md")
	mustClick(t, tt, "包含 / 排除的文件") // 收起就不再筛
	expect("41 处，2 个文件")

	// 搜索框里 ↓：选中第一处并打开它
	mustClick(t, tt, "搜索文字")
	tt.Key(0, wui.KeyDown)
	tt.Frame()
	if s.Sel != 1 || !tt.Focused("结果 demo/README.md:3:1") {
		t.Fatalf("↓ 后 sel=%d", s.Sel)
	}
	if c := a.CurrentDoc(); c == nil || c.Rel != "README.md" {
		t.Fatalf("↓ 没打开第一处：%+v", c)
	}
}

// 包含 / 排除下面两个"也搜"勾上就重搜，被忽略的、3 MB 的文件进结果；收起包含 / 排除时一起清掉。
func TestSearchAlsoIgnoredBig(t *testing.T) {
	repo := testutil.NewRepo(t, "demo")
	repo.Write("a.txt", "第一\n")
	repo.Write(".gitignore", "out/\n")
	repo.Commit("初始")
	repo.Write("out/x.log", "第二\n")
	repo.Write("big.txt", strings.Repeat("0123456789\n", 3<<20/11)+"第三\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	s := &a.Search
	seen := 0
	expect := func(want string) {
		t.Helper()
		wait(want, func() bool {
			text, _ := a.SearchSummary()
			return s.Searches() > seen && !s.Running && strings.Contains(text, want)
		})
		seen = s.Searches()
		tt.Frame()
		mustText(t, tt, want)
	}
	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	tt.Type("第")
	expect("1 处，1 个文件")
	if tt.HasText("也搜被忽略的文件（.gitignore）") {
		t.Fatal("包含 / 排除收着也显示了勾选框")
	}
	// "重新搜索"：新配上的文件进来
	repo.Write("c.txt", "第四\n")
	mustClick(t, tt, "重新搜索")
	expect("2 处，2 个文件")
	if err := os.Remove(filepath.Join(repo.Root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, "重新搜索")
	expect("1 处，1 个文件")
	mustClick(t, tt, "包含 / 排除的文件")
	mustClick(t, tt, "也搜被忽略的文件（.gitignore）")
	expect("2 处，2 个文件")
	mustText(t, tt, "结果文件 demo/out/x.log")
	mustClick(t, tt, "也搜 2 MB 以上的大文件")
	expect("3 处，3 个文件")
	mustText(t, tt, "结果文件 demo/big.txt")
	mustClick(t, tt, "搜索文字")
	tt.Key(0, wui.KeyEnter) // 记进最近搜索，连两个"也搜"
	expect("3 处，3 个文件")
	mustClick(t, tt, "包含 / 排除的文件")
	expect("1 处，1 个文件")
	if s.Query.Ignored || s.Query.Big {
		t.Errorf("收起没清：%+v", s.Query)
	}

	// 最近搜索那一行带灰字，点了开关跟着回来、包含 / 排除展开
	mustClick(t, tt, "搜索文字")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Key(0, wui.KeyBackspace)
	tt.Frame()
	mustText(t, tt, "最近搜索 第  忽略 大")
	mustClick(t, tt, "最近搜索 第  忽略 大")
	expect("3 处，3 个文件")
	if !s.Globs || !s.Query.Ignored || !s.Query.Big || !tt.HasText("也搜被忽略的文件（.gitignore）") {
		t.Errorf("点了最近搜索：globs=%v %+v", s.Globs, s.Query)
	}
}

// 多行搜索：⇧⏎ 在光标处换行、框变成多行的，LF 和 CRLF 的文件都搜到，预览里换行是 ⏎；替换框同样，全部替换写回各自的换行；清空回到一行。
func TestSearchMultiLine(t *testing.T) {
	repo := testutil.NewRepo(t, "demo")
	repo.Write("a.txt", "foo\nbar\n")
	repo.Write("b.txt", "foo\r\nbar\r\n")
	repo.Write("c.txt", "foo bar\n")
	repo.Commit("初始")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	s := &a.Search
	tt.Key(wui.Cmd|wui.Shift, wui.KeyH)
	tt.Frame()
	one, _ := tt.Find("搜索文字")
	tt.Type("foo")
	tt.Key(wui.Shift, wui.KeyEnter)
	wait("换成多行的框", func() bool { return s.MultiQ })
	tt.Frame()
	tt.Frame()
	if s.Query.Text != "foo\n" || !tt.Focused("搜索文字") {
		t.Fatalf("⇧⏎：%q，焦点在搜索框 %v", s.Query.Text, tt.Focused("搜索文字"))
	}
	tt.Type("bar") // 光标在换行后面
	tt.Frame()
	if s.Query.Text != "foo\nbar" {
		t.Fatalf("多行的框里打字：%q", s.Query.Text)
	}
	if two, _ := tt.Find("搜索文字"); two.H <= one.H {
		t.Errorf("多行的框没变高：%v → %v", one, two)
	}
	wait("搜 foo⏎bar", func() bool {
		ms := s.Res.Matches
		return s.Done && !s.Running && len(ms) == 2 && ms[0].Preview == "foo⏎bar"
	})
	tt.Frame()
	mustText(t, tt, "2 处，2 个文件")
	if !tt.HasText("foo⏎bar") || tt.HasText("结果文件 demo/c.txt") {
		t.Fatalf("预览没有 ⏎，或者搜到了一行里的；texts=%q", tt.Texts())
	}

	// 打开 CRLF 的那处：选中整个匹配（缓冲区里没有 \r）
	mustClickRow(t, tt, "结果 demo/b.txt:1:1")
	d := a.CurrentDoc()
	wait("读 b.txt", func() bool { return d != nil && d.Buf != nil && d.St.SelectedText() != "" })
	if got := d.St.SelectedText(); got != "foo\nbar" {
		t.Errorf("选中 %q", got)
	}

	// 替换框：⇧⏎ 换行，预览里也是 ⏎；回车全部替换（先问）
	mustClick(t, tt, "替换为")
	tt.Type("xy")
	tt.Key(0, wui.KeyLeft)
	tt.Key(wui.Shift, wui.KeyEnter) // 替换框是普通的输入框：在光标处换行
	wait("替换框换成多行的", func() bool { return s.MultiR })
	tt.Frame()
	tt.Frame()
	if s.Replace != "x\ny" || s.Preview(0) != "x⏎y" {
		t.Fatalf("替换框：%q 预览 %q", s.Replace, s.Preview(0))
	}
	tt.Key(0, wui.KeyEnter)
	wait("问全部替换", func() bool { return a.ConfirmOpen })
	tt.Frame()
	mustText(t, tt, "把 2 处换成 “x⏎y”？")
	mustClick(t, tt, "全部替换")
	if got := mustRead(t, filepath.Join(repo.Root, "a.txt")); got != "x\ny\n" {
		t.Errorf("a.txt：%q", got)
	}
	if got := d.Buf.String(); got != "x\ny\n" || d.Modified() || mustRead(t, filepath.Join(repo.Root, "b.txt")) != "x\r\ny\r\n" {
		t.Errorf("开着的 b.txt：%q modified=%v 盘上 %q", got, d.Modified(), mustRead(t, filepath.Join(repo.Root, "b.txt")))
	}

	// 回车搜索记进最近搜索（⏎ 画出来），清空回到一行、焦点还在
	mustClick(t, tt, "搜索文字")
	tt.Key(0, wui.KeyEnter)
	wait("记进最近搜索", func() bool { return len(a.St.SearchHistory) > 0 })
	if got := a.St.SearchHistory[0].Text; got != "foo\nbar" {
		t.Errorf("最近搜索 %q", got)
	}
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Key(0, wui.KeyBackspace)
	tt.Frame()
	tt.Frame()
	if s.MultiQ || s.Query.Text != "" || !tt.Focused("搜索文字") {
		t.Fatalf("清空：multi=%v %q 焦点 %v", s.MultiQ, s.Query.Text, tt.Focused("搜索文字"))
	}
	if back, _ := tt.Find("搜索文字"); back.H != one.H {
		t.Errorf("回到一行的高 %v，原来 %v", back.H, one.H)
	}
	mustText(t, tt, "最近搜索 foo⏎bar")

	// 撤销上次替换：清空了搜索框也在；写盘的 a.txt 写回去，开着的 b.txt 留给 ⌘Z
	mustClick(t, tt, "撤销上次替换")
	if !a.ConfirmOpen || a.Confirm.Title != "撤销上次替换？" || !strings.Contains(a.Confirm.Message, "把 1 个文件写回替换前的内容（开着的 1 个文件请在编辑器里 ⌘Z）") {
		t.Fatalf("问话 %+v", a.Confirm)
	}
	mustClick(t, tt, "撤销")
	tt.Frame()
	if got := mustRead(t, filepath.Join(repo.Root, "a.txt")); got != "foo\nbar\n" || tt.HasText("撤销上次替换") {
		t.Errorf("撤销后 a.txt %q，链接还在 %v", got, tt.HasText("撤销上次替换"))
	}
}

// TestSearchBoxCaret：一行的搜索框（searchBox）⇧⏎ 在光标处换行；清空按钮和 Esc 清空，键盘留在框里。
func TestSearchBoxCaret(t *testing.T) {
	repo := testutil.NewRepo(t, "demo")
	repo.Write("a.txt", "foo\nbar\n")
	repo.Commit("初始")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	s := &a.Search
	tt.Key(wui.Cmd|wui.Shift, wui.KeyF)
	tt.Frame()
	tt.Type("abc")
	tt.Frame()
	mustClick(t, tt, "清除")
	tt.Frame()
	if s.Query.Text != "" || !tt.Focused("搜索文字") || tt.HasText("清除") {
		t.Fatalf("点清空：%q，焦点在框里 %v", s.Query.Text, tt.Focused("搜索文字"))
	}
	tt.Type("abc")
	tt.Frame()
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if s.Query.Text != "" || !tt.Focused("搜索文字") {
		t.Fatalf("Esc：%q，焦点在框里 %v", s.Query.Text, tt.Focused("搜索文字"))
	}
	tt.Type("foobar")
	for range 3 {
		tt.Key(0, wui.KeyLeft)
	}
	tt.Key(wui.Shift, wui.KeyEnter)
	wait("换成多行的框", func() bool { return s.MultiQ })
	tt.Frame()
	tt.Frame()
	if s.Query.Text != "foo\nbar" || !tt.Focused("搜索文字") {
		t.Fatalf("⇧⏎ 没插在光标处：%q，焦点在框里 %v", s.Query.Text, tt.Focused("搜索文字"))
	}
	wait("搜 foo⏎bar", func() bool { return s.Done && !s.Running && len(s.Res.Matches) == 1 })
}
