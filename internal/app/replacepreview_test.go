package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/search"
	"github.com/holthq/holt/internal/testutil"
)

// 替换预览都在临时仓库上测：不写盘，旧的一边是现在的文字（开着改了没存的用缓冲区），新的一边是换好的。

func TestReplacePreviewText(t *testing.T) {
	one, _, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	dirty, _ := openReplaceDocs(t, a, wait, one)
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "bar"

	// 没开着的：读盘解码，CRLF 原样留在 Raw 里；磁盘不动
	a.PreviewReplace(matchAt(t, s, "disk.txt", 2, 2), false)
	d := a.CurrentDoc()
	if d.Kind != docs.ReplacePreview || d.Title != "替换预览: disk.txt" || d.DiffSt == nil || d.DiffSt.Editable {
		t.Fatalf("标签 kind=%v title=%q diff=%v", d.Kind, d.Title, d.DiffSt)
	}
	if string(d.Raw[0]) != "x foo\r\ny foo\r\n" || string(d.Raw[1]) != "x bar\r\ny bar\r\n" {
		t.Fatalf("两边 %q → %q", d.Raw[0], d.Raw[1])
	}
	if d.DiffSt.Old.String() != "x foo\ny foo\n" || d.DiffSt.New.String() != "x bar\ny bar\n" {
		t.Errorf("diff 视图 %q → %q", d.DiffSt.Old.String(), d.DiffSt.New.String())
	}
	if got := mustRead(t, filepath.Join(one.Root, "disk.txt")); got != "x foo\r\ny foo\r\n" {
		t.Fatalf("预览写了盘：%q", got)
	}
	if _, ok := sessionTab(d); ok {
		t.Error("替换预览记进了会话")
	}

	// 开着、改了没存：旧的一边是缓冲区（多出来的第 3 行不在结果里，不换），缓冲区不动
	a.PreviewReplace(matchAt(t, s, "dirty.go", 1, 0), false)
	d = a.CurrentDoc()
	if string(d.Raw[0]) != "foo 1\nfoo 2\nfoo 3\n" || string(d.Raw[1]) != "bar 1\nbar 2\nfoo 3\n" {
		t.Fatalf("dirty.go 两边 %q → %q", d.Raw[0], d.Raw[1])
	}
	if dirty.Buf.String() != "foo 1\nfoo 2\nfoo 3\n" || !dirty.Modified() {
		t.Errorf("缓冲区被改了：%q", dirty.Buf.String())
	}

	// 同一个文件再预览一次是同一个标签；关掉不进 ⇧⌘T
	n := len(a.AllDocs())
	a.PreviewReplaceFirst() // 第一个有结果的是 clean.go
	if d = a.CurrentDoc(); d.Rel != "clean.go" || string(d.Raw[1]) != "bar bar\n" {
		t.Fatalf("“预览”开了 %s：%q", d.Rel, d.Raw[1])
	}
	a.PreviewReplace(matchAt(t, s, "clean.go", 1, 4), true)
	if len(a.AllDocs()) != n+1 || a.CurrentDoc() != d {
		t.Errorf("又开了一个标签：%d → %d", n, len(a.AllDocs()))
	}
	a.RemoveDoc(d)
	if a.CanReopen() {
		t.Error("关掉的替换预览进了 ⇧⌘T")
	}
}

// 正则的 $1 照搜索页一样展开；从一处进来记下它那块改动（界面滚过去）。
func TestReplacePreviewRegexp(t *testing.T) {
	p := testutil.NewRepo(t, "p")
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line"
	}
	lines[1], lines[25] = "a foo b", "c fooo d"
	p.Write("far.txt", strings.Join(lines, "\n")+"\n")
	p.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, p.Root))
	t.Cleanup(a.Close)
	s := &a.Search
	searchFor(t, a, wait, search.Query{Text: `f(o+)`, Regexp: true})
	s.Replace = "<$1>"
	a.PreviewReplace(matchAt(t, s, "far.txt", 26, 2), true)
	d := a.CurrentDoc()
	want := strings.Replace(strings.Replace(string(d.Raw[0]), "a foo b", "a <oo> b", 1), "c fooo d", "c <ooo> d", 1)
	if string(d.Raw[1]) != want {
		t.Fatalf("新的一边 %q", d.Raw[1])
	}
	if hs := d.DiffSt.Hunks(); len(hs) != 2 || d.ReplaceHunk != 2 {
		t.Errorf("%d 块改动，要滚到第 %d 块", len(hs), d.ReplaceHunk)
	}
}

// 结果变了（改了替换词、排除了几处、重搜）预览就旧了：不能从它替换，重新生成之后再换；换完关掉预览。
func TestReplacePreviewStale(t *testing.T) {
	one, _, ws := replaceRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	s := &a.Search
	searchFor(t, a, wait, search.Query{Text: "foo"})
	s.Replace = "bar"
	a.PreviewReplace(matchAt(t, s, "disk.txt", 1, 2), true)
	d := a.CurrentDoc()
	disk := filepath.Join(one.Root, "disk.txt")
	if a.PreviewStale(d) || a.PreviewCount(d) != 2 {
		t.Fatalf("刚生成的：stale=%v count=%d", a.PreviewStale(d), a.PreviewCount(d))
	}

	// 改了替换词：旧了，点替换不动
	s.Replace = "baz"
	if !a.PreviewStale(d) {
		t.Fatal("改了替换词还不算旧")
	}
	a.ReplaceFromPreview(d)
	if got := mustRead(t, disk); got != "x foo\r\ny foo\r\n" {
		t.Fatalf("旧的预览换了：%q", got)
	}
	a.RegenPreview(d)
	if a.PreviewStale(d) || string(d.Raw[1]) != "x baz\r\ny baz\r\n" {
		t.Fatalf("重新生成：stale=%v %q", a.PreviewStale(d), d.Raw[1])
	}

	// 排除了一处：旧了；重新生成之后只换剩下那处
	a.DismissMatch(matchAt(t, s, "disk.txt", 1, 2))
	if !a.PreviewStale(d) {
		t.Fatal("排除了还不算旧")
	}
	a.RegenPreview(d)
	if a.PreviewCount(d) != 1 || string(d.Raw[1]) != "x foo\r\ny baz\r\n" {
		t.Fatalf("排除后：%d 处 %q", a.PreviewCount(d), d.Raw[1])
	}

	// 从预览替换：写盘、关掉预览
	a.ReplaceFromPreview(d)
	if got := mustRead(t, disk); got != "x foo\r\ny baz\r\n" {
		t.Fatalf("换完：%q", got)
	}
	for _, x := range a.AllDocs() {
		if x == d {
			t.Fatal("换完预览还开着")
		}
	}

	// 重搜：别的文件的预览也旧了
	a.PreviewReplace(matchAt(t, s, "clean.go", 1, 0), false)
	d = a.CurrentDoc()
	searchFor(t, a, wait, search.Query{Text: "foo"})
	if !a.PreviewStale(d) {
		t.Error("重搜之后还不算旧")
	}
}
