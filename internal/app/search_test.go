package app

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/search"
	"github.com/holthq/holt/internal/testutil"
)

// searchRepos 建两个临时仓库：one 里 a.go 有三处 needle（第 2、4 行和第 30 行），b.md 一处；two 里 c.txt 一处。
func searchRepos(t *testing.T) (one, two *testutil.Repo, ws string) {
	one = testutil.NewRepo(t, "one")
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "filler"
	}
	lines[1], lines[3], lines[29] = "x := needle", "\tneedle()", "// Needle at 30"
	one.Write("a.go", strings.Join(lines, "\n")+"\n")
	one.Write("docs/b.md", "# 说明\n用 needle 找\n")
	one.Commit("初始")
	two = testutil.NewRepo(t, "two")
	two.Write("c.txt", "needle in two\n")
	two.Commit("初始")
	return one, two, testutil.Workspace(t, one.Root, two.Root)
}

func TestSearchFlow(t *testing.T) {
	one, two, ws := searchRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search

	// 敲字：停 150 ms 才搜，中间再敲只搜最后一次
	s.Query.Text = "nee"
	a.SearchChanged()
	start := time.Now()
	s.Query.Text = "needle"
	a.SearchChanged()
	if s.Running || s.Done {
		t.Fatalf("还在等敲字停下就开始搜了")
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
	if time.Since(start) < SearchDebounce {
		t.Errorf("没等 %v 就搜了", SearchDebounce)
	}
	if n := len(s.Res.Matches); n != 5 || s.Res.Files != 3 || s.Res.Truncated {
		t.Fatalf("结果 %d 处 %d 个文件 %+v", n, s.Res.Files, s.Res.Matches)
	}
	if text, failed := a.SearchSummary(); failed || !strings.HasPrefix(text, "5 处，3 个文件 · ") {
		t.Errorf("摘要 %q %v", text, failed)
	}
	// 行：文件行后面跟着它的每一处，仓库按列表顺序、文件按路径
	var got []string
	for _, r := range s.Rows {
		m := s.Res.Matches[r.Match]
		if r.File {
			got = append(got, m.Repo+"/"+m.Path+"×"+strconv.Itoa(r.Count))
		} else {
			got = append(got, strconv.Itoa(m.Line+1))
		}
	}
	if want := "one/a.go×3 2 4 30 one/docs/b.md×1 2 two/c.txt×1 1"; strings.Join(got, " ") != want {
		t.Fatalf("行 %q，想要 %q", strings.Join(got, " "), want)
	}

	// 点第 4 行那一处：预览标签打开 a.go，读完后选中那个 needle
	s.Sel = 2
	a.OnSearchRow(true, false)
	d := a.CurrentDoc()
	if d == nil || d.Rel != "a.go" || d.Pinned {
		t.Fatalf("没在预览标签里打开 a.go: %+v", d)
	}
	wait("读 a.go", func() bool { return d.Buf != nil })
	if p, _ := d.Cursor(); p.Line != 3 || d.St.SelectedText() != "needle" {
		t.Fatalf("光标 %+v 选中 %q", p, d.St.SelectedText())
	}
	// 同一个文件里再跳到第 30 行，‹ 回到第 4 行，› 又到第 30 行
	s.Sel = 3
	a.OnSearchRow(true, false)
	if p, _ := d.Cursor(); p.Line != 29 || d.St.SelectedText() != "Needle" {
		t.Fatalf("跳到第 30 行后光标 %+v 选中 %q", p, d.St.SelectedText())
	}
	if !a.CanBack() {
		t.Fatal("同一个文件里跳了一次，应该能后退")
	}
	a.Back()
	if p, _ := d.Cursor(); a.CurrentDoc() != d || p.Line != 3 {
		t.Fatalf("后退后在 %+v 第 %d 行", a.CurrentDoc(), p.Line+1)
	}
	a.Forward()
	if p, _ := d.Cursor(); p.Line != 29 {
		t.Fatalf("前进后在第 %d 行", p.Line+1)
	}

	// 另一个仓库的结果：回车固定标签，‹ 回到 a.go 第 30 行
	s.Sel = len(s.Rows) - 1
	a.OnSearchRow(false, true)
	c := a.CurrentDoc()
	if c == nil || c.Root != two.Root || c.Rel != "c.txt" || !c.Pinned {
		t.Fatalf("没固定打开 two/c.txt: %+v", c)
	}
	wait("读 c.txt", func() bool { return c.Buf != nil })
	if p, _ := c.Cursor(); p.Line != 0 || c.St.SelectedText() != "needle" {
		t.Fatalf("c.txt 光标 %+v 选中 %q", p, c.St.SelectedText())
	}
	a.Back()
	if p, _ := d.Cursor(); a.CurrentDoc() != d || p.Line != 29 {
		t.Fatalf("后退应回到 a.go 第 30 行：%v 第 %d 行", a.CurrentDoc().Title, p.Line+1)
	}

	// 方向键走到文件行什么都不做；点文件行收起它，选中留在它上面
	s.Sel = 0
	a.OnSearchRow(false, false)
	if !s.Rows[0].Open || len(s.Rows) != 8 {
		t.Fatalf("键盘走到文件行不该收起")
	}
	s.Sel = 2
	a.ToggleSearchFile(2)
	if len(s.Rows) != 5 || s.Rows[0].Open || s.Sel != 0 {
		t.Fatalf("收起 a.go 后 %d 行 open=%v sel=%d", len(s.Rows), s.Rows[0].Open, s.Sel)
	}
	a.OnSearchRow(true, false)
	if len(s.Rows) != 8 || !s.Rows[0].Open {
		t.Fatalf("再点一次应展开")
	}

	// 区分大小写：只剩小写的四处；正则写错了显示错误
	s.Query.CaseSensitive = true
	a.SearchNow()
	wait("区分大小写", func() bool { return s.Done && !s.Running })
	if len(s.Res.Matches) != 4 {
		t.Errorf("区分大小写 %d 处", len(s.Res.Matches))
	}
	s.Query.Regexp, s.Query.Text = true, "needle("
	a.SearchNow()
	wait("错的正则", func() bool { return s.Done && !s.Running })
	if text, failed := a.SearchSummary(); !failed || !strings.Contains(text, "正则写错了") {
		t.Errorf("错的正则：%q %v", text, failed)
	}
	// 包含 / 排除
	s.Query = docsOnly()
	a.SearchNow()
	wait("只搜 md", func() bool { return s.Done && !s.Running })
	if len(s.Res.Matches) != 1 || s.Res.Matches[0].Path != "docs/b.md" {
		t.Errorf("只搜 *.md：%+v", s.Res.Matches)
	}

	// 清空：结果没了，正在等的也不搜了
	s.Query.Text = "needle"
	a.SearchChanged()
	s.Query.Text = ""
	a.SearchChanged()
	if s.Done || s.Running || len(s.Rows) != 0 {
		t.Fatalf("清空后还有结果")
	}
	time.Sleep(2 * SearchDebounce)
	wait("过期的定时器", func() bool { return true })
	if s.Done || s.Running {
		t.Fatalf("清空后过期的定时器又搜了")
	}

	// 仓库移出列表后，旧结果点不开，给一句提示
	s.Query = search.Query{Text: "needle"}
	a.SearchNow()
	wait("再搜", func() bool { return s.Done && !s.Running })
	a.Repos = a.Repos[:1]
	a.OpenSearchMatch(len(s.Res.Matches)-1, false)
	if !strings.Contains(a.Toast, "two 已经不在列表里了") {
		t.Errorf("toast %q", a.Toast)
	}
	_ = one
}

func docsOnly() search.Query { return search.Query{Text: "needle", Include: "*.md"} }

// 新的一次搜索开始时取消上一次：慢的旧结果回来也不会盖掉新的。
func TestSearchCancelsPrevious(t *testing.T) {
	_, _, ws := searchRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	s.Query.Text = "filler"
	a.SearchNow()
	first := s.cancel
	s.Query.Text = "needle"
	a.SearchNow()
	if first == nil || s.cancel == nil {
		t.Fatal("没有取消函数")
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
	wait("旧的回调", func() bool { return true })
	if len(s.Res.Matches) != 5 || s.Res.Matches[0].Preview == "filler" {
		t.Fatalf("结果是旧查询的：%d 处", len(s.Res.Matches))
	}
}

// ⇧⌘F 打开搜索页、焦点给搜索框；编辑器里选着一行内的文字就拿它来搜。
func TestShowSearchSeedsSelection(t *testing.T) {
	one, _, ws := searchRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.RepoFor(one.Root)
	d := a.OpenAt(r, "a.go", docs.Jump{Line: 1, Col: 5, Len: 6}, true)
	wait("读 a.go", func() bool { return d.Buf != nil })
	a.Exec("view.search")
	if a.St.SidebarMode != "search" || !a.Search.Focus || a.Search.Query.Text != "needle" || !a.Search.Running {
		t.Fatalf("⇧⌘F：mode=%q focus=%v 查询 %q running=%v", a.St.SidebarMode, a.Search.Focus, a.Search.Query.Text, a.Search.Running)
	}
	wait("搜索", func() bool { return a.Search.Done && !a.Search.Running })
	// 选着跨行的文字不拿来搜
	a.Search.Query.Text = "keep"
	d.St.Select(0, d.Buf.Len())
	a.Exec("view.search")
	if a.Search.Query.Text != "keep" {
		t.Errorf("跨行的选择也拿来搜了：%q", a.Search.Query.Text)
	}
}
