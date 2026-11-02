package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/testutil"
)

// "再读 5000"：6000 处先收 5000、到顶；再读一次收全，前面的 5000 处不变，
// 收起的文件、排除的一处、选中都留着；改了框里的条件再点就是普通的重搜（上限回到 5000）。
func TestContinueSearch(t *testing.T) {
	r := testutil.NewRepo(t, "many")
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		r.Write(name, strings.Repeat("hit\n", 2000))
	}
	r.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	t.Cleanup(a.Close)
	s := &a.Search
	done := func() bool { return s.Done && !s.Running }
	s.Query.Text = "hit"
	a.SearchNow()
	wait("搜", done)
	if len(s.Res.Matches) != search.MaxMatches || !s.Res.Truncated || !s.CanSearchMore() || s.AtMaxLimit() {
		t.Fatalf("第一次：%d 处 truncated=%v", len(s.Res.Matches), s.Res.Truncated)
	}
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "只显示前 5000 处，3 个文件") {
		t.Errorf("摘要 %q", text)
	}
	first := s.Res.Matches[:search.MaxMatches:search.MaxMatches]
	a.ToggleSearchFile(0) // 收起 a.txt
	a.DismissMatch(2000)  // b.txt 第 1 行
	s.Sel = 2
	gen := s.Searches()
	a.SearchMore()
	wait("再读", func() bool { return s.Searches() > gen && done() })
	if len(s.Res.Matches) != 6000 || s.Res.Truncated || s.CanSearchMore() || s.Query.Limit != 10000 {
		t.Fatalf("再读：%d 处 truncated=%v limit=%d", len(s.Res.Matches), s.Res.Truncated, s.Query.Limit)
	}
	for i, m := range first {
		if s.Res.Matches[i] != m {
			t.Fatalf("前面的第 %d 处变了：%+v → %+v", i, m, s.Res.Matches[i])
		}
	}
	if s.Rows[0].Open || s.Sel != 2 || s.Dismissed() != 1 {
		t.Errorf("收起 %v、选中 %d、排除 %d 没留住", s.Rows[0].Open, s.Sel, s.Dismissed())
	}
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "5999 处，3 个文件（排除了 1 处）") {
		t.Errorf("摘要 %q", text)
	}
	if s.Res.Files != 3 || len(s.Rows) != 1+2*(1+1999)+1 {
		t.Errorf("行 %d", len(s.Rows))
	}

	// 改了条件：重搜，上限回到默认
	s.Query.Text = "hi"
	a.SearchNow()
	wait("重搜", done)
	if s.Query.Limit != 0 || len(s.Res.Matches) != search.MaxMatches || s.Dismissed() != 0 || !s.Rows[0].Open {
		t.Errorf("重搜：limit=%d %d 处", s.Query.Limit, len(s.Res.Matches))
	}
}

// where 是结果里每一处的 "路径:行:列"（排除了的带 "x"）。
func where(s *SearchState) string {
	var out []string
	for _, m := range s.Res.Matches {
		w := fmt.Sprintf("%s:%d:%d", m.Path, m.Line, m.Col)
		if s.dismissed[keyOf(m)] {
			w += "x"
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// followIdle 是没有等着、也没有在重搜结果里的文件。
func followIdle(s *SearchState) bool {
	return s.followTimer == nil && !s.following && len(s.follow) == 0
}

// 结果跟着磁盘变：开着监听，外面改了结果里的文件、删了一个，结果跟着变（不重搜全部）；
// 排除了的 (line, col) 还配得上就留着，配不上了就丢掉；结果外面新配上的不进来，"重新搜索"才进来，排除的留着。
// 重读一样的不动 ver（问着的"全部替换？"不作废）；重读的时候排除了一处就按新的再读一次。
func TestSearchFollowsDisk(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.txt", "foo\nbar foo\n")
	r.Write("b.txt", "foo\n")
	r.Write("c.txt", "nothing\n")
	r.Write("sub/d.txt", "foo foo\n")
	r.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	t.Cleanup(a.Close)
	repo := a.Current()
	a.RefreshRepo(repo, WhyManual)
	wait("读状态", idle(repo))
	a.StartWatching()
	wait("监听开了", repo.Watching)
	s := &a.Search
	s.Query.Text = "foo"
	a.SearchNow()
	wait("搜", func() bool { return s.Done && !s.Running })
	if got := where(s); got != "a.txt:0:0 a.txt:1:4 b.txt:0:0 sub/d.txt:0:0 sub/d.txt:0:4" {
		t.Fatalf("搜出来 %s", got)
	}
	a.DismissMatch(1) // a.txt:1:4
	a.DismissMatch(4) // sub/d.txt:0:4
	gen := s.Searches()

	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(r.Root, filepath.FromSlash(name)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "x foo\nbar foo\n")
	write("sub/d.txt", "foo\n")
	write("c.txt", "foo now\n")
	if err := os.Remove(filepath.Join(r.Root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	want := "a.txt:0:2 a.txt:1:4x sub/d.txt:0:0"
	wait("结果跟上", func() bool { return where(s) == want && followIdle(s) })
	if s.Searches() != gen {
		t.Errorf("重搜了全部（%d → %d）", gen, s.Searches())
	}
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "2 处，2 个文件（排除了 1 处）") {
		t.Errorf("摘要 %q", text)
	}
	if len(s.dismissed) != 1 {
		t.Errorf("配不上的排除没丢：%v", s.dismissed)
	}

	// 重读一样的：ver 不变
	ver := s.ver
	a.SearchFilesChanged(repo, []string{"a.txt"}, false)
	a.SearchFilesChanged(repo, nil, true)
	if !s.follow[fileKey{r.Root, "a.txt"}] || !s.follow[fileKey{r.Root, "sub/d.txt"}] || s.followTimer == nil {
		t.Fatalf("没记下要重搜的 %v", s.follow)
	}
	wait("重读一样的", func() bool { return followIdle(s) })
	if s.ver != ver || where(s) != want {
		t.Errorf("没变也动了：ver %d → %d，%s", ver, s.ver, where(s))
	}

	// 重读的时候排除了一处：回来的作废，按现在的再读一次
	write("a.txt", "x foo\nbar foo\nfoo\n")
	s.follow = map[fileKey]bool{{r.Root, "a.txt"}: true}
	a.followSearch(s.gen)
	if !s.following {
		t.Fatal("没在后台重搜")
	}
	a.DismissMatch(0) // a.txt:0:2
	wait("再读一次", func() bool { return where(s) == "a.txt:0:2x a.txt:1:4x a.txt:2:0 sub/d.txt:0:0" && followIdle(s) })

	// 重新搜索：c.txt 进来，排除的留着
	a.Research()
	wait("重新搜索", func() bool { return s.Searches() > gen && s.Done && !s.Running })
	if got := where(s); got != "a.txt:0:2x a.txt:1:4x a.txt:2:0 c.txt:0:0 sub/d.txt:0:0" {
		t.Errorf("重新搜索 %s", got)
	}

	// 新搜的时候不跟（等着的丢掉）
	s.follow = map[fileKey]bool{{r.Root, "a.txt"}: true}
	s.Query.Text = "bar"
	a.SearchNow()
	if !followIdle(s) {
		t.Error("新搜了还等着重搜")
	}
	wait("搜 bar", func() bool { return s.Done && !s.Running })
	if got := where(s); got != "a.txt:1:0" {
		t.Errorf("搜 bar %s", got)
	}
}

// 保存开着的文件（⌘S、自动保存）也让结果跟着变；没监听时也是。没存的改动不算（结果是磁盘上的）。
func TestSearchFollowsSave(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.txt", "foo\n")
	r.Write("b.txt", "foo\nfoo\n")
	r.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	t.Cleanup(a.Close)
	repo := a.Current()
	s := &a.Search
	s.Query.Text = "foo"
	a.SearchNow()
	wait("搜", func() bool { return s.Done && !s.Running })
	d := a.OpenFile(repo, "b.txt", true)
	wait("读文件", func() bool { return !d.Loading && d.Buf != nil })

	d.Buf.Set("foo\nfoo foo\n")
	a.SearchFilesChanged(repo, []string{"a.txt"}, false) // 别的文件变了：b.txt 不重读
	wait("没存不变", func() bool { return followIdle(s) })
	if got := where(s); got != "a.txt:0:0 b.txt:0:0 b.txt:1:0" {
		t.Fatalf("没存就变了 %s", got)
	}
	a.Save(d)
	wait("⌘S 之后", func() bool { return where(s) == "a.txt:0:0 b.txt:0:0 b.txt:1:0 b.txt:1:4" && followIdle(s) })

	a.St.Settings.AutoSave = "blur"
	d.Buf.Set("bar\nfoo\n")
	a.OnBlur()
	wait("自动保存之后", func() bool { return where(s) == "a.txt:0:0 b.txt:1:0" && followIdle(s) })
	if text, _ := a.SearchSummary(); !strings.HasPrefix(text, "2 处，2 个文件") {
		t.Errorf("摘要 %q", text)
	}
}

// lastLog 是输出栏里最后一条标题是 title 的。
func lastLog(a *App, title string) (LogEntry, bool) {
	for i := len(a.Output) - 1; i >= 0; i-- {
		if a.Output[i].Title == title {
			return a.Output[i], true
		}
	}
	return LogEntry{}, false
}

// 撤销上次替换：没开着、直接写了盘的（CRLF、Shift_JIS）问过之后逐字节写回，开着的留给编辑器 ⌘Z（问话里说）；
// 替换之后没有配上而掉出结果的文件插回原来的位置；Git 状态跟着刷新；撤销完链接没了。
func TestUndoReplaceOnDisk(t *testing.T) {
	one, two, ws := replaceRepos(t)
	ja := encodeAs(t, japanese.ShiftJIS, "日本語 foo\n")
	if err := os.WriteFile(filepath.Join(one.Root, "ja.txt"), ja, 0o600); err != nil {
		t.Fatal(err)
	}
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	dirty, clean := openReplaceDocs(t, a, wait, one)
	searchFor(t, a, wait, search.Query{Text: "foo"})
	before := where(s)
	if before != "clean.go:0:0 clean.go:0:4 dirty.go:0:0 dirty.go:1:0 disk.txt:0:2 disk.txt:1:2 ja.txt:0:10 t.txt:0:0" {
		t.Fatalf("搜出来 %s", before)
	}
	disk := func(r *testutil.Repo, rel string) []byte {
		b, _ := os.ReadFile(filepath.Join(r.Root, rel))
		return b
	}
	s.Replacing, s.Replace = true, "bar"
	if s.CanUndoReplace() {
		t.Fatal("还没替换就能撤销")
	}
	a.ReplaceAll()
	confirm(t, a, wait, "把 8 处换成 “bar”？")
	if got := string(disk(one, "disk.txt")); got != "x bar\r\ny bar\r\n" || !s.CanUndoReplace() {
		t.Fatalf("替换完 %q，能撤销 %v", got, s.CanUndoReplace())
	}
	if got := where(s); got != "dirty.go:2:0" { // 缓冲区里多的那行
		t.Fatalf("替换完的结果 %s", got)
	}
	if u := s.lastReplace; len(u.files) != 3 || u.open != 2 || !strings.Contains(s.UndoReplaceTip(), "3 个文件") {
		t.Fatalf("记下的 %d 个文件、开着的 %d 个，提示 %q", len(u.files), u.open, s.UndoReplaceTip())
	}
	if c := Find("search.undoReplace"); c == nil || !a.CanRun(c) {
		t.Fatal("命令面板没有撤销上次替换")
	}

	// 换了搜索词也还在
	searchFor(t, a, wait, search.Query{Text: "x"})
	a.UndoReplace()
	if a.Confirm.Message != "把 3 个文件写回替换前的内容（开着的 2 个文件请在编辑器里 ⌘Z）。替换之后又改过的文件不动。" || a.Confirm.Button != "撤销" {
		t.Fatalf("问话 %+v", a.Confirm)
	}
	a.ConfirmOpen = false
	searchFor(t, a, wait, search.Query{Text: "foo"})
	if got := where(s); got != "dirty.go:0:0 dirty.go:1:0" { // 磁盘上的 dirty.go 还是没存的
		t.Fatalf("再搜 %s", got)
	}
	a.UndoReplace()
	confirm(t, a, wait, "撤销上次替换？")
	for _, c := range []struct {
		r    *testutil.Repo
		rel  string
		want []byte
	}{{one, "disk.txt", []byte("x foo\r\ny foo\r\n")}, {one, "ja.txt", ja}, {two, "t.txt", []byte("foo two\n")}} {
		if got := disk(c.r, c.rel); !bytes.Equal(got, c.want) {
			t.Errorf("%s 没写回：%q", c.rel, got)
		}
	}
	if fi, _ := os.Stat(filepath.Join(one.Root, "ja.txt")); fi.Mode().Perm() != 0o600 {
		t.Errorf("权限 %v", fi.Mode().Perm())
	}
	if got := where(s); got != "dirty.go:0:0 dirty.go:1:0 disk.txt:0:2 disk.txt:1:2 ja.txt:0:10 t.txt:0:0" {
		t.Errorf("撤销后的结果 %s", got)
	}
	if s.CanUndoReplace() || a.Toast != "撤销了替换，写回 3 个文件" {
		t.Errorf("撤销后：能撤销 %v，提示 %q", s.CanUndoReplace(), a.Toast)
	}
	if clean.Buf.String() != "bar bar\n" || dirty.Buf.String() != "bar 1\nbar 2\nfoo 3\n" {
		t.Errorf("开着的也动了：%q %q", clean.Buf.String(), dirty.Buf.String())
	}
	if e, ok := lastLog(a, "撤销替换：写回 2 个文件"); !ok || e.Repo != "one" || e.Text != "disk.txt：写回了\nja.txt：写回了" {
		t.Errorf("输出栏 %+v", e)
	}
	r1 := a.RepoFor(one.Root)
	wait("刷新 Git 状态", func() bool { return idle(r1)() && r1.Status.Untracked == 1 && r1.Status.Unstaged == 1 }) // ja.txt 未跟踪、clean.go 改了
}

// 撤销只写回还是替换完那个样子的：外面又改过的、开着改了没存的不动，输出栏列出来；
// 只改了缓冲区的替换、什么都没换的、原文件合计太大的不能撤销；问着的时候又替换了一次就不撤销。
func TestUndoReplaceSkipsChanged(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		r.Write(name, "foo\n")
	}
	r.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	t.Cleanup(a.Close)
	repo := a.Current()
	s := &a.Search
	read := func(rel string) string { b, _ := os.ReadFile(filepath.Join(r.Root, rel)); return string(b) }
	searchFor(t, a, wait, search.Query{Text: "foo", Exclude: "d.txt"})
	s.Replace = "bar"
	a.ReplaceAll()
	confirm(t, a, wait, "把 3 处换成 “bar”？")
	r.Write("b.txt", "bar!\n")
	c := a.OpenFile(repo, "c.txt", true)
	wait("读文件", func() bool { return c.Buf != nil })
	c.Buf.Set("bar?\n")
	a.UndoReplace()
	confirm(t, a, wait, "撤销上次替换？")
	if read("a.txt") != "foo\n" || read("b.txt") != "bar!\n" || read("c.txt") != "bar\n" || c.Buf.String() != "bar?\n" {
		t.Errorf("a %q b %q c %q / %q", read("a.txt"), read("b.txt"), read("c.txt"), c.Buf.String())
	}
	if a.Toast != "撤销了替换，写回 1 个文件；2 个文件替换之后改过了，没动（见输出栏）" {
		t.Errorf("提示 %q", a.Toast)
	}
	if e, ok := lastLog(a, "撤销替换：写回 1 个文件"); !ok || e.Text != "a.txt：写回了\nb.txt：替换之后改过了，没动\nc.txt：在编辑器里改了还没保存，没动" {
		t.Errorf("输出栏 %q", e.Text)
	}
	if where(s) != "a.txt:0:0" {
		t.Errorf("结果 %s", where(s))
	}

	// 全都改过了
	c.Buf.Set("bar\n")
	searchFor(t, a, wait, search.Query{Text: "foo", Include: "a.txt"})
	a.ReplaceFile(0)
	r.Write("a.txt", "改了\n")
	a.UndoReplace()
	confirm(t, a, wait, "撤销上次替换？")
	if a.Toast != "这 1 个文件替换之后都改过了，没动（见输出栏）" || read("a.txt") != "改了\n" {
		t.Errorf("全改过了：%q %q", a.Toast, read("a.txt"))
	}

	// 问着的时候又替换了一次：不撤销
	searchFor(t, a, wait, search.Query{Text: "foo"})
	a.ReplaceFile(matchAt(t, s, "d.txt", 1, 0))
	a.UndoReplace()
	fn := a.Confirm.Fn
	a.ConfirmOpen = false
	r.Write("e.txt", "foo\n")
	searchFor(t, a, wait, search.Query{Text: "foo"})
	a.ReplaceFile(0)
	fn()
	if a.Toast != "问的时候又替换了一次，没有撤销" || read("d.txt") != "bar\n" {
		t.Errorf("又替换了一次：%q d %q", a.Toast, read("d.txt"))
	}

	// 只改了缓冲区：清掉，不能撤销上一次的
	if !s.CanUndoReplace() {
		t.Fatal("e.txt 那次该能撤销")
	}
	r.Write("f.txt", "foo\n")
	f := a.OpenFile(repo, "f.txt", true)
	wait("读文件", func() bool { return f.Buf != nil })
	searchFor(t, a, wait, search.Query{Text: "foo", Include: "f.txt"})
	a.ReplaceFile(0)
	if f.Buf.String() != "bar\n" || s.CanUndoReplace() {
		t.Errorf("只改了缓冲区：%q 能撤销 %v", f.Buf.String(), s.CanUndoReplace())
	}

	// 什么都没换（搜完之后变了）：留着上一次的
	r.Write("g.txt", "foo\n")
	searchFor(t, a, wait, search.Query{Text: "foo", Include: "g.txt"})
	a.ReplaceFile(0)
	r.Write("h.txt", "foo\n")
	searchFor(t, a, wait, search.Query{Text: "foo", Include: "h.txt"})
	r.Write("h.txt", "xx foo\n")
	a.ReplaceFile(0)
	if !s.CanUndoReplace() || !strings.Contains(a.Toast, "变了") {
		t.Errorf("没换成也清了：%v %q", s.CanUndoReplace(), a.Toast)
	}

	// 原文件合计超过 20 MB：不记，报告里说
	r.Write("big.txt", strings.Repeat("0123456789\n", 21<<20/11)+"foo\n")
	searchFor(t, a, wait, search.Query{Text: "foo", Include: "big.txt", Big: true})
	a.ReplaceFile(0)
	if s.CanUndoReplace() || !strings.HasSuffix(a.Toast, "；这次太大（原文件合计超过 20 MB），不能撤销") {
		t.Errorf("太大：%v %q", s.CanUndoReplace(), a.Toast)
	}
}
