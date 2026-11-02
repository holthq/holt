package app

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 替换都在临时仓库上测。replaceRepos 建两个仓库：one 里 dirty.go（会开着、改了没存）、clean.go（开着、没改）、
// disk.txt（没开、CRLF），two 里 t.txt；都有 foo。
func replaceRepos(t *testing.T) (one, two *testutil.Repo, ws string) {
	one = testutil.NewRepo(t, "one")
	one.Write("dirty.go", "foo 1\nfoo 2\n")
	one.Write("clean.go", "foo foo\n")
	one.Write("disk.txt", "x foo\r\ny foo\r\n")
	one.Commit("初始")
	two = testutil.NewRepo(t, "two")
	two.Write("t.txt", "foo two\n")
	two.Commit("初始")
	return one, two, testutil.Workspace(t, one.Root, two.Root)
}

// searchFor 按 q 搜，等搜完。
func searchFor(t *testing.T, a *App, wait func(string, func() bool), q search.Query) {
	t.Helper()
	a.Search.Query = q
	a.SearchNow()
	wait("搜索", func() bool { return a.Search.Done && !a.Search.Running })
}

// matchAt 是结果里 rel 第 line 行（从 1 起）第 col 列（从 0 起）那一处的下标。
func matchAt(t *testing.T, s *SearchState, rel string, line, col int) int {
	t.Helper()
	for i, m := range s.Res.Matches {
		if m.Path == rel && m.Line == line-1 && m.Col == col {
			return i
		}
	}
	t.Fatalf("结果里没有 %s:%d:%d：%+v", rel, line, col, s.Res.Matches)
	return -1
}

// rowsOf 是结果列表的样子：文件行 "path×N"，一处 "行:列"。
func rowsOf(s *SearchState) string {
	var out []string
	for _, r := range s.Rows {
		m := s.Res.Matches[r.Match]
		if r.File {
			out = append(out, m.Path+"×"+strconv.Itoa(r.Count))
		} else {
			out = append(out, strconv.Itoa(m.Line+1)+":"+strconv.Itoa(m.Col))
		}
	}
	return strings.Join(out, " ")
}

// openReplaceDocs 开着 dirty.go（改成多一行、没存）和 clean.go。
func openReplaceDocs(t *testing.T, a *App, wait func(string, func() bool), one *testutil.Repo) (dirty, clean *docs.Doc) {
	t.Helper()
	r := a.RepoFor(one.Root)
	dirty, clean = a.OpenFile(r, "dirty.go", true), a.OpenFile(r, "clean.go", true)
	wait("读文件", func() bool { return dirty.Buf != nil && clean.Buf != nil })
	dirty.Buf.Set("foo 1\nfoo 2\nfoo 3\n")
	return dirty, clean
}

// 逐处替换：开着没改的改缓冲区并存盘，没开的直接改磁盘（CRLF 留着），开着改了没存的只改缓冲区；换完的那处从结果里没了。
func TestReplaceOne(t *testing.T) {
	one, _, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	dirty, clean := openReplaceDocs(t, a, wait, one)
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "bar"
	if want := "clean.go×2 1:0 1:4 dirty.go×2 1:0 2:0 disk.txt×2 1:2 2:2 t.txt×1 1:0"; rowsOf(s) != want {
		t.Fatalf("结果 %q，想要 %q", rowsOf(s), want)
	}
	if got := s.Preview(matchAt(t, s, "clean.go", 1, 4)); got != "bar" {
		t.Errorf("预览 %q", got)
	}

	// 开着、没改过：改缓冲区，顺手存盘；选中留在原位（同一个文件剩下的那处）
	s.Sel = 2
	a.ReplaceOne(matchAt(t, s, "clean.go", 1, 4))
	if clean.Buf.String() != "foo bar\n" || clean.Modified() || mustRead(t, filepath.Join(one.Root, "clean.go")) != "foo bar\n" {
		t.Fatalf("clean.go：缓冲区 %q modified=%v", clean.Buf.String(), clean.Modified())
	}
	if want := "clean.go×1 1:0 dirty.go×2 1:0 2:0 disk.txt×2 1:2 2:2 t.txt×1 1:0"; rowsOf(s) != want || s.Sel != 2 {
		t.Fatalf("替换后结果 %q sel=%d", rowsOf(s), s.Sel)
	}
	if a.Toast != "替换了 1 处，1 个文件" {
		t.Errorf("提示 %q", a.Toast)
	}
	if e := a.Output[len(a.Output)-1]; e.Title != "替换 “foo” → “bar”：1 处 / 1 个文件" || e.Text != "clean.go：1 处" || e.Failed {
		t.Errorf("输出栏：%+v", e)
	}

	// 没开着的：读盘、换好写回，CRLF 不变
	a.ReplaceOne(matchAt(t, s, "disk.txt", 2, 2))
	if got := mustRead(t, filepath.Join(one.Root, "disk.txt")); got != "x foo\r\ny bar\r\n" {
		t.Fatalf("disk.txt：%q", got)
	}

	// 开着、改了没存：只改缓冲区，磁盘不动、还是改过的
	a.ReplaceOne(matchAt(t, s, "dirty.go", 1, 0))
	if dirty.Buf.String() != "bar 1\nfoo 2\nfoo 3\n" || !dirty.Modified() || mustRead(t, filepath.Join(one.Root, "dirty.go")) != "foo 1\nfoo 2\n" {
		t.Fatalf("dirty.go：%q modified=%v", dirty.Buf.String(), dirty.Modified())
	}
	if a.Toast != "替换了 1 处，1 个文件；1 个文件在编辑器里改了，还没保存" {
		t.Errorf("提示 %q", a.Toast)
	}
	// 重搜的是缓冲区：多出来的第 3 行也在结果里了
	if want := "clean.go×1 1:0 dirty.go×2 2:0 3:0 disk.txt×1 1:2 t.txt×1 1:0"; rowsOf(s) != want {
		t.Fatalf("结果 %q", rowsOf(s))
	}

	// 搜完之后改过的：不动，结果跟着现在的内容
	os.WriteFile(filepath.Join(one.Root, "disk.txt"), []byte("x fox\r\ny bar\r\n"), 0o644)
	a.ReplaceOne(matchAt(t, s, "disk.txt", 1, 2))
	if got := mustRead(t, filepath.Join(one.Root, "disk.txt")); got != "x fox\r\ny bar\r\n" || a.Toast != "这 1 处在搜完后变了，没动" {
		t.Fatalf("变了的也换了：%q %q", got, a.Toast)
	}
	if strings.Contains(rowsOf(s), "disk.txt") {
		t.Errorf("没有了的文件还在结果里：%q", rowsOf(s))
	}
}

// 逐个文件替换：文件里没排除的全换，一步撤销。
func TestReplaceFile(t *testing.T) {
	one, _, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	dirty, clean := openReplaceDocs(t, a, wait, one)
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "quux"

	a.ReplaceFile(matchAt(t, s, "clean.go", 1, 0))
	if clean.Buf.String() != "quux quux\n" || mustRead(t, filepath.Join(one.Root, "clean.go")) != "quux quux\n" {
		t.Fatalf("clean.go：%q", clean.Buf.String())
	}
	a.ReplaceFile(matchAt(t, s, "disk.txt", 1, 2))
	if got := mustRead(t, filepath.Join(one.Root, "disk.txt")); got != "x quux\r\ny quux\r\n" {
		t.Fatalf("disk.txt：%q", got)
	}
	a.ReplaceFile(matchAt(t, s, "dirty.go", 2, 0))
	if dirty.Buf.String() != "quux 1\nquux 2\nfoo 3\n" || !dirty.Modified() {
		t.Fatalf("dirty.go：%q", dirty.Buf.String())
	}
	if want := "dirty.go×1 3:0 t.txt×1 1:0"; rowsOf(s) != want || s.Res.Files != 2 {
		t.Fatalf("结果 %q files=%d", rowsOf(s), s.Res.Files)
	}
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "2 处，2 个文件") {
		t.Errorf("摘要 %q", text)
	}
	// 文件行上的处数 = FileCount
	if n := s.FileCount(matchAt(t, s, "t.txt", 1, 0)); n != 1 {
		t.Errorf("FileCount %d", n)
	}
}

// 开着没改过的文件换完存了盘，⌘Z 一步回到换之前（变成改了没存）。
func TestReplaceUndo(t *testing.T) {
	one, _, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	_, clean := openReplaceDocs(t, a, wait, one)
	searchFor(t, a, wait, search.Query{Text: "foo", Include: "clean.go"})
	s.Replace = "bar"
	a.ReplaceFile(0)
	if clean.Buf.String() != "bar bar\n" || clean.Modified() {
		t.Fatalf("换完：%q", clean.Buf.String())
	}
	if !clean.Buf.Undo() || clean.Buf.String() != "foo foo\n" || !clean.Modified() {
		t.Fatalf("一步撤销：%q modified=%v", clean.Buf.String(), clean.Modified())
	}
	if clean.Buf.Undo() {
		t.Errorf("撤销了两步才回去")
	}
}

// 全部替换先问：说清几处、几个文件、几个仓库；点了才换，结果在问的时候变了就不换；换完 Git 状态跟着刷新。
func TestReplaceAllAsks(t *testing.T) {
	one, two, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	dirty, _ := openReplaceDocs(t, a, wait, one)
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "bar"

	a.ReplaceAll()
	if !a.ConfirmOpen || a.Confirm.Title != "把 7 处换成 “bar”？" || a.Confirm.Button != "全部替换" ||
		!strings.HasPrefix(a.Confirm.Message, "4 个文件，2 个仓库。") {
		t.Fatalf("没先问：%+v", a.Confirm)
	}
	if mustRead(t, filepath.Join(two.Root, "t.txt")) != "foo two\n" {
		t.Fatal("问之前就换了")
	}
	// 问着的时候排除了一处：点了也不换
	fn := a.Confirm.Fn
	a.ConfirmOpen = false
	a.DismissMatch(matchAt(t, s, "t.txt", 1, 0))
	fn()
	if a.Toast != "搜索结果变了，没有替换；再看一眼再换" || mustRead(t, filepath.Join(one.Root, "disk.txt")) != "x foo\r\ny foo\r\n" {
		t.Fatalf("结果变了还换了：%q", a.Toast)
	}

	a.ReplaceAll()
	if a.Confirm.Title != "把 6 处换成 “bar”？" || !strings.HasPrefix(a.Confirm.Message, "3 个文件，1 个仓库。") {
		t.Fatalf("排除之后：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if a.Toast != "替换了 6 处，3 个文件；1 个文件在编辑器里改了，还没保存" {
		t.Errorf("提示 %q", a.Toast)
	}
	for p, want := range map[string]string{"clean.go": "bar bar\n", "disk.txt": "x bar\r\ny bar\r\n", "dirty.go": "foo 1\nfoo 2\n"} {
		if got := mustRead(t, filepath.Join(one.Root, p)); got != want {
			t.Errorf("%s：%q", p, got)
		}
	}
	if dirty.Buf.String() != "bar 1\nbar 2\nfoo 3\n" || mustRead(t, filepath.Join(two.Root, "t.txt")) != "foo two\n" {
		t.Errorf("dirty.go %q，排除的 t.txt 也换了？", dirty.Buf.String())
	}
	r := a.RepoFor(one.Root)
	wait("Git 状态", func() bool { return idle(r)() && r.Status.Unstaged == 2 })

	// 替换成空的：问"删掉"
	searchFor(t, a, wait, search.Query{Text: "two"})
	s.Replace = ""
	a.ReplaceAll()
	if a.Confirm.Title != "删掉这 1 处？" {
		t.Fatalf("删掉：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if got := mustRead(t, filepath.Join(two.Root, "t.txt")); got != "foo \n" {
		t.Errorf("删掉后 %q", got)
	}
	// 正则写错了不能换
	searchFor(t, a, wait, search.Query{Text: "(", Regexp: true})
	if s.CanReplace() {
		t.Error("正则错了还能换")
	}
}

// 排除：不显示、全部替换时跳过；文件整个排除；换完别处后排除的那处挪了位置也还排除着。
func TestDismiss(t *testing.T) {
	one, _, ws := replaceRepos(t)
	one.Write("three.txt", "foo foo foo\n")
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "bazz"

	a.DismissMatch(matchAt(t, s, "three.txt", 1, 4))
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "9 处，5 个文件（排除了 1 处）") {
		t.Errorf("摘要 %q", text)
	}
	if !strings.Contains(rowsOf(s), "three.txt×2 1:0 1:8") {
		t.Fatalf("排除的还在：%q", rowsOf(s))
	}
	a.DismissFile(matchAt(t, s, "clean.go", 1, 0))
	if strings.Contains(rowsOf(s), "clean.go") || s.Dismissed() != 3 {
		t.Fatalf("排除文件：%q %d", rowsOf(s), s.Dismissed())
	}
	a.ReplaceFile(matchAt(t, s, "three.txt", 1, 0))
	if got := mustRead(t, filepath.Join(one.Root, "three.txt")); got != "bazz foo bazz\n" {
		t.Fatalf("three.txt：%q", got)
	}
	// 中间那处挪到了第 5 列，还是排除着；这个文件没有剩下的了
	if strings.Contains(rowsOf(s), "three.txt") || s.Dismissed() != 3 {
		t.Fatalf("挪过的排除：%q %d", rowsOf(s), s.Dismissed())
	}
	a.ReplaceAll()
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if mustRead(t, filepath.Join(one.Root, "clean.go")) != "foo foo\n" {
		t.Error("排除的文件也换了")
	}
	// 新搜一次排除就清掉了
	searchFor(t, a, wait, search.Query{Text: "foo"})
	if s.Dismissed() != 0 || !strings.Contains(rowsOf(s), "clean.go×2") {
		t.Errorf("新搜之后：%q", rowsOf(s))
	}
}

// 最近搜索：回车、打开一处时记下（字、开关、包含 / 排除都一样才算重复，最近的在前、最多 MaxSearches 条）；
// ↑ ↓ 翻（开关、包含 / 排除跟着换，有包含 / 排除就展开），翻过头回到原来框里的字和开关。
func TestRecentSearches(t *testing.T) {
	_, _, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	texts := func() []string {
		var out []string
		for _, e := range a.St.SearchHistory {
			out = append(out, e.Text)
		}
		return out
	}
	for _, q := range []string{"foo", "two", "foo"} {
		s.Query.Text = q
		a.SearchSubmitted()
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
	if !slices.Equal(texts(), []string{"foo", "two"}) {
		t.Fatalf("最近搜索 %q", texts())
	}
	s.Query = search.Query{Text: "foo", CaseSensitive: true} // 字一样、开关不一样：另记一条
	a.SearchSubmitted()
	if !slices.Equal(texts(), []string{"foo", "foo", "two"}) || !a.St.SearchHistory[0].Case || a.St.SearchHistory[1].Case {
		t.Fatalf("开关不一样：%+v", a.St.SearchHistory)
	}
	a.ForgetSearch(0)
	s.Query = search.Query{Text: "x"}
	a.SearchSubmitted()
	wait("搜索", func() bool { return s.Done && !s.Running })
	a.OpenSearchMatch(0, false) // 打开一处也记（已经在最前就不动）
	for i := range MaxSearches + 5 {
		a.noteSearch(search.Query{Text: "q" + strconv.Itoa(i)})
	}
	if len(a.St.SearchHistory) != MaxSearches || a.St.SearchHistory[0].Text != "q24" {
		t.Fatalf("上限：%d %q", len(a.St.SearchHistory), texts()[0])
	}

	// ↑ ↓：开关、包含 / 排除跟着换
	glob := workspace.SearchEntry{Text: "two", Word: true, Include: "*.go", Ignored: true}
	a.St.SearchHistory = []workspace.SearchEntry{{Text: "foo"}, glob, {Text: "x", Regexp: true}}
	s.Query = search.Query{Text: "draft", CaseSensitive: true}
	s.Globs = false
	a.SearchEdited()
	if a.StepSearch(-1) {
		t.Fatal("没在翻时 ↓ 应该交给结果列表")
	}
	for _, want := range []string{"foo", "two", "x", "x"} {
		if !a.StepSearch(1) || s.Query.Text != want {
			t.Fatalf("↑ 想要 %q，是 %q", want, s.Query.Text)
		}
		switch want {
		case "foo":
			if s.Query != (search.Query{Text: "foo"}) || s.Globs {
				t.Fatalf("↑ 到 foo：%+v globs=%v", s.Query, s.Globs)
			}
		case "two":
			if EntryOf(s.Query) != glob || !s.Globs {
				t.Fatalf("↑ 到 two：%+v globs=%v", s.Query, s.Globs)
			}
		case "x":
			if !s.Query.Regexp || s.Query.Include != "" || s.Query.Ignored {
				t.Fatalf("↑ 到 x：%+v", s.Query)
			}
		}
	}
	for _, want := range []string{"two", "foo", "draft"} {
		if !a.StepSearch(-1) || s.Query.Text != want {
			t.Fatalf("↓ 想要 %q，是 %q", want, s.Query.Text)
		}
	}
	if s.Query != (search.Query{Text: "draft", CaseSensitive: true}) || s.Globs {
		t.Fatalf("回到原来的：%+v globs=%v", s.Query, s.Globs)
	}
	if a.StepSearch(-1) {
		t.Fatal("回到原来的字之后 ↓ 应该交给结果列表")
	}
	// 框里就是最近那条：↑ 直接到上一条；开关不一样就不算
	s.Query = search.Query{Text: "foo"}
	a.SearchEdited()
	if a.StepSearch(1); s.Query.Text != "two" {
		t.Fatalf("框里是最近那条时 ↑：%q", s.Query.Text)
	}
	s.Query = search.Query{Text: "foo", Regexp: true}
	a.SearchEdited()
	if a.StepSearch(1); s.Query != (search.Query{Text: "foo"}) {
		t.Fatalf("开关不一样时 ↑：%+v", s.Query)
	}

	// 点一条（开关跟着换）、去掉一条、全清
	s.Globs = false
	a.UseSearch(1)
	if EntryOf(s.Query) != glob || !s.Globs || a.St.SearchHistory[0] != glob || !s.Focus {
		t.Fatalf("点了一条：%+v globs=%v %+v", s.Query, s.Globs, a.St.SearchHistory)
	}
	a.ForgetSearch(0)
	if !slices.Equal(texts(), []string{"foo", "x"}) {
		t.Fatalf("去掉一条：%q", texts())
	}
	a.ClearSearches()
	if len(a.St.SearchHistory) != 0 {
		t.Fatalf("全清：%q", texts())
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
}

// 范围：只搜一个仓库 / 它的一个目录，摘要里写着；× 去掉范围重搜全部；仓库移出列表就改成搜全部。
func TestSearchScope(t *testing.T) {
	one, _, ws := replaceRepos(t)
	one.Write("sub/a.txt", "foo\n")
	one.Write("sub/deep/b.txt", "foo\n")
	one.Write("subway.txt", "foo\n")
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	r := a.RepoFor(one.Root)
	s.Query.Text = "foo"
	a.SearchIn(r, "sub")
	if a.St.SidebarMode != "search" || !s.Focus || s.Scope == nil || s.Scope.Label() != "one/sub" {
		t.Fatalf("在此搜索：mode=%q scope=%+v", a.St.SidebarMode, s.Scope)
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
	if want := "sub/a.txt×1 1:0 sub/deep/b.txt×1 1:0"; rowsOf(s) != want {
		t.Fatalf("只搜 sub：%q", rowsOf(s))
	}
	if text, _ := a.SearchSummary(); !strings.Contains(text, "2 处，2 个文件 · 范围 one/sub") {
		t.Errorf("摘要 %q", text)
	}

	a.SearchIn(r, "")
	wait("搜索", func() bool { return s.Done && !s.Running })
	if s.Scope.Label() != "one" || strings.Contains(rowsOf(s), "t.txt×") || !strings.Contains(rowsOf(s), "subway.txt") {
		t.Fatalf("整个仓库：%q", rowsOf(s))
	}

	a.ClearScope()
	wait("搜索", func() bool { return s.Done && !s.Running })
	if s.Scope != nil || !strings.Contains(rowsOf(s), "t.txt×1") {
		t.Fatalf("去掉范围：%q", rowsOf(s))
	}

	// 范围的仓库不在列表里了
	a.SearchIn(r, "sub")
	wait("搜索", func() bool { return s.Done && !s.Running })
	a.Repos = a.Repos[1:]
	a.SearchNow()
	if s.Scope != nil || a.Toast != "one 已经不在列表里了，改成搜全部" {
		t.Fatalf("仓库没了：%+v %q", s.Scope, a.Toast)
	}
	wait("搜索", func() bool { return s.Done && !s.Running })
}
