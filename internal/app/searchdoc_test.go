package app

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/search"
	"github.com/holthq/holt/internal/testutil"
)

// 搜索编辑器：结果开成只读的纯文本标签，每处带上下文。

func TestSearchEditorText(t *testing.T) {
	files := map[string][]string{
		"a.go": {"l1", "l2 foo", "l3", "l4", "l5", "l6", "l7", "l8", "l9 foo", "l10 foo x foo"},
		"b.go": {"foo top", "b2"},
	}
	lines := func(root, rel string) []string { return files[rel] }
	ms := []search.Match{
		{Repo: "r", Root: "/r", Path: "a.go", Line: 1, Col: 3, Len: 3, Span: 3},
		{Repo: "r", Root: "/r", Path: "a.go", Line: 8, Col: 3, Len: 3, Span: 3},
		{Repo: "r", Root: "/r", Path: "a.go", Line: 9, Col: 4, Len: 3, Span: 3},
		{Repo: "r", Root: "/r", Path: "a.go", Line: 9, Col: 10, Len: 3, Span: 3},
		{Repo: "r", Root: "/r", Path: "b.go", Line: 0, Col: 0, Len: 3, Span: 3},
	}
	q := search.Query{Text: "foo", CaseSensitive: true, Regexp: true, Include: "*.go"}
	text, hits := SearchEditorText(search.Result{Matches: ms}, q, "r/sub", 1, 2, lines)
	want := strings.Join([]string{
		"# 5 处，2 个文件（排除了 1 处） · “foo” · 区分大小写 · 正则 · 包含 *.go · 范围 r/sub",
		"",
		"r/a.go:",
		"   1  l1",
		"   2: l2 foo",
		"   3  l3",
		"   4  l4",
		"  ⋯",
		"   7  l7",
		"   8  l8",
		"   9: l9 foo",
		"  10: l10 foo x foo",
		"",
		"r/b.go:",
		"  1: foo top",
		"  2  b2",
	}, "\n")
	if text != want {
		t.Fatalf("文字：\n%s\n该是\n%s", text, want)
	}
	if len(hits) != strings.Count(text, "\n")+1 {
		t.Fatalf("%d 行、%d 个 hit", strings.Count(text, "\n")+1, len(hits))
	}
	// 每一处标在文字里的位置正好是 foo；Col / Len 是原文里的
	var marked []string
	for _, h := range hits {
		for _, m := range h.Marks {
			marked = append(marked, text[m.Start:m.End])
			if m.Len != 3 {
				t.Errorf("Len %d", m.Len)
			}
		}
	}
	if !slices.Equal(marked, []string{"foo", "foo", "foo", "foo", "foo"}) {
		t.Errorf("标出来的 %q", marked)
	}
	if h := hits[11]; h.Path != "a.go" || h.Line != 9 || len(h.Marks) != 2 || h.Marks[1].Col != 10 {
		t.Errorf("第 10 行：%+v", h)
	}
	if h := hits[3]; h.Path != "a.go" || h.Line != 0 || h.Marks != nil {
		t.Errorf("上下文行：%+v", h)
	}
	if h := hits[2]; h.Path != "a.go" || h.Line != 1 { // 文件名那行指向第一处
		t.Errorf("文件名那行：%+v", h)
	}
	for _, i := range []int{0, 1, 7, 12} {
		if hits[i].Path != "" {
			t.Errorf("第 %d 行不该指向文件：%+v", i, hits[i])
		}
	}

	// 0 行上下文：挨着的两行之间没有 ⋯，隔开的有
	text, _ = SearchEditorText(search.Result{Matches: ms[:3]}, search.Query{Text: "foo"}, "", 0, 0, lines)
	if want := "# 3 处，1 个文件 · “foo”\n\nr/a.go:\n   2: l2 foo\n  ⋯\n   9: l9 foo\n  10: l10 foo x foo"; text != want {
		t.Errorf("0 行上下文：\n%s", text)
	}

	// 读不到文件：用结果里的预览，不带上下文；跨行的只标开始那一行
	pm := []search.Match{
		{Repo: "r", Root: "/r", Path: "gone.go", Line: 4, Col: 2, Len: 6, Span: 9, Preview: "a foo⏎bar", PreviewCol: 2},
		{Repo: "r", Root: "/r", Path: "gone.go", Line: 5, Col: 0, Len: 3, Span: 3, Preview: "foo", PreviewCol: 0},
	}
	text, hits = SearchEditorText(search.Result{Matches: pm, Truncated: true}, search.Query{Text: "foo", Limit: 2}, "", 0, 3, lines)
	if want := "# 只显示前 2 处，1 个文件 · “foo”\n\nr/gone.go:\n  5: a foo⏎bar\n  6: foo"; text != want {
		t.Errorf("没读到文件：\n%s", text)
	}
	if m := hits[3].Marks[0]; text[m.Start:m.End] != "foo⏎" || m.Len != 9 {
		t.Errorf("跨行的标成 %q（Len %d）", text[m.Start:m.End], m.Len)
	}

	// 跨行的匹配在读到的那一行上标到行尾
	files["m.go"] = []string{"x foo", "bar y"}
	text, hits = SearchEditorText(search.Result{Matches: []search.Match{{Repo: "r", Root: "/r", Path: "m.go", Line: 0, Col: 2, Len: 7, Span: 7}}},
		search.Query{Text: "foo\nbar"}, "", 0, 0, lines)
	if m := hits[3].Marks[0]; text[m.Start:m.End] != "foo" || m.Len != 7 || !strings.HasPrefix(text, "# 1 处，1 个文件 · “foo⏎bar”") {
		t.Errorf("跨行：%q %+v", text, m)
	}

	// 很长的一行截成匹配附近的一段
	long := strings.Repeat("a", 1000) + "foo" + strings.Repeat("b", 1000)
	files["long.js"] = []string{long}
	text, hits = SearchEditorText(search.Result{Matches: []search.Match{{Repo: "r", Root: "/r", Path: "long.js", Line: 0, Col: 1000, Len: 3, Span: 3}}},
		search.Query{Text: "foo"}, "", 0, 2, lines)
	row := strings.Split(text, "\n")[3]
	if m := hits[3].Marks[0]; text[m.Start:m.End] != "foo" || m.Col != 1000 || len(row) > searchLineMax+20 || !strings.HasSuffix(row, "…") || !strings.Contains(row, ": …a") {
		t.Errorf("长行：%d 字节，标 %q", len(row), text[m.Start:m.End])
	}

	// 出错的、没有结果的只有表头
	if text, _ := SearchEditorText(search.Result{Err: errors.New("正则写错了：x")}, search.Query{Text: "("}, "", 0, 2, lines); text != "# 正则写错了：x · “(”" {
		t.Errorf("出错：%q", text)
	}
	if text, _ := SearchEditorText(search.Result{}, search.Query{Text: "zz", WholeWord: true, Ignored: true, Big: true, Exclude: "vendor/"}, "", 0, 2, lines); text != "# 没有结果 · “zz” · 全字 · 排除 vendor/ · 含被忽略的文件 · 含大文件" {
		t.Errorf("没有结果：%q", text)
	}
}

func TestSearchEditor(t *testing.T) {
	r := testutil.NewRepo(t, "se")
	r.Write("a.txt", "one\ntwo foo\nthree\nfour\nfive\nsix\nseven foo\n")
	r.Write("b.txt", "foo b\n")
	r.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	searchFor(t, a, wait, search.Query{Text: "foo"})
	a.DismissFile(matchAt(t, &a.Search, "b.txt", 1, 0))
	if !a.Search.CanOpenSearchEditor() {
		t.Fatal("开不了")
	}
	a.OpenSearchEditor()
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.SearchDoc || d.Title != "搜索: foo" || !d.Pinned || !a.FocusDoc {
		t.Fatalf("标签 %+v", d)
	}
	wait("生成", func() bool { return d.Buf != nil && !d.Loading })
	want := "# 2 处，1 个文件（排除了 1 处） · “foo”\n\nse/a.txt:\n  1  one\n  2: two foo\n  3  three\n  4  four\n  5  five\n  6  six\n  7: seven foo"
	if got := d.Buf.String(); got != want || !d.St.ReadOnly || d.SearchLines != 2 {
		t.Fatalf("文字：\n%q\n该是\n%q", got, want)
	}

	// ⌘点 / 双击 / 回车一行：匹配行选中那一处（预览标签），上下文行只跳
	off := strings.Index(d.Buf.String(), "seven")
	a.OpenSearchHit(d, off)
	f := a.CurrentDoc()
	wait("打开", func() bool { return f.Buf != nil && !f.Loading })
	if f.Kind != docs.File || f.Rel != "a.txt" || f.St.SelectedText() != "foo" || f.St.CursorPos().Line != 6 || f.Pinned {
		t.Fatalf("打开了 %s，选中 %q，光标 %+v", f.Rel, f.St.SelectedText(), f.St.CursorPos())
	}
	a.reuse(d.Key(), true)
	a.OpenSearchHit(d, strings.Index(d.Buf.String(), "four"))
	if f := a.CurrentDoc(); f.Rel != "a.txt" || f.St.SelectedText() != "" || f.St.CursorPos().Line != 3 {
		t.Errorf("上下文行：%s 选中 %q 光标 %+v", f.Rel, f.St.SelectedText(), f.St.CursorPos())
	}
	a.reuse(d.Key(), true)
	a.OpenSearchHit(d, 0) // 表头不指向文件
	if a.CurrentDoc() != d {
		t.Error("点表头打开了别的")
	}

	// 上下文 0 行：重写，记在窗口上
	a.SetSearchLines(d, 0)
	wait("重写", func() bool { return strings.Count(d.Buf.String(), "\n") == 5 })
	if a.Search.EditorLines != 0 || !strings.HasSuffix(d.Buf.String(), "  2: two foo\n  ⋯\n  7: seven foo") {
		t.Errorf("0 行：%q", d.Buf.String())
	}

	// 重新搜索：按标签记的条件（排除的不再算），磁盘上的新内容
	r.Write("a.txt", "one\ntwo foo\nthree\nfour\nfive\nsix\nseven foo\neight foo\n")
	a.ResearchSearchDoc(d)
	if !SearchDocRunning(d) {
		t.Error("没在搜")
	}
	wait("重搜", func() bool { return !SearchDocRunning(d) && strings.Contains(d.Buf.String(), "eight foo") })
	if !strings.HasPrefix(d.Buf.String(), "# 4 处，2 个文件 · “foo”") || !strings.Contains(d.Buf.String(), "se/b.txt:") {
		t.Errorf("重搜之后：%q", d.Buf.String())
	}

	// 同一个查询再开：重写那个标签，不多开
	n := len(a.AllDocs())
	searchFor(t, a, wait, search.Query{Text: "foo"})
	a.OpenSearchEditor()
	if a.CurrentDoc() != d || len(a.AllDocs()) != n {
		t.Errorf("又开了一个：%d → %d", n, len(a.AllDocs()))
	}

	// 关了不进 ⇧⌘T
	closed := len(a.closed)
	a.RemoveDoc(d)
	if len(a.closed) != closed {
		t.Error("搜索编辑器进了最近关闭")
	}
	if _, ok := sessionTab(d); ok {
		t.Error("搜索编辑器记进了会话")
	}
}

// ⌥⏎：框里的还没搜（停手的定时器还没到）就立刻搜，搜完开；没有结果的不开。
func TestSearchToEditor(t *testing.T) {
	r := testutil.NewRepo(t, "st")
	r.Write("a.txt", "alpha\nbeta\n")
	r.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	a.Search.Query.Text = "beta"
	a.SearchEdited()
	a.SearchToEditor()
	wait("开", func() bool { d := a.CurrentDoc(); return d != nil && d.Kind == docs.SearchDoc && d.Buf != nil })
	if d := a.CurrentDoc(); d.Title != "搜索: beta" || !strings.Contains(d.Buf.String(), "  2: beta") {
		t.Fatalf("%s：%q", d.Title, d.Buf.String())
	}
	if len(a.St.SearchHistory) == 0 || a.St.SearchHistory[0].Text != "beta" {
		t.Error("没记进最近搜索")
	}
	a.Search.Query.Text, a.Toast = "nothing-here", ""
	a.SearchToEditor()
	wait("搜完", func() bool { return a.Search.Done && !a.Search.Running && a.Search.resQ.Text == "nothing-here" })
	if a.Toast == "" || a.CurrentDoc().Title != "搜索: beta" {
		t.Errorf("没有结果还开了：%q %s", a.Toast, a.CurrentDoc().Title)
	}
}
