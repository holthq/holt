package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 终端里 ⌘点击：仓库里的 path:行 固定打开并跳行、相对终端目录、~/、file://、目录在文件树里显示；
// 仓库外的文件也能开；网址和找不到的路径交回给终端。
func TestOpenLink(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	outside := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(outside, []byte("一\n二\n三\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("# 指南\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, wait := newTestApp(t, ws)
	r := a.Repos[0]
	loaded := func(d *docs.Doc) func() bool { return func() bool { return !d.Loading && d.Buf != nil } }
	line := func(d *docs.Doc) int { p, _ := d.Cursor(); return p.Line }

	for _, link := range []string{"https://example.org", "mailto:a@b.c", "nope.txt:3", "/no/such/file"} {
		if a.OpenLink(root, link) {
			t.Errorf("%q 不该由应用打开", link)
		}
	}
	if len(a.Docs) != 0 {
		t.Fatalf("没打开东西却多了标签：%d", len(a.Docs))
	}

	if !a.OpenLink(root, "a.txt:20") || !a.FocusDoc {
		t.Fatal("a.txt:20 没打开")
	}
	d := a.CurrentDoc()
	if d.Rel != "a.txt" || !d.Pinned || d.Kind != docs.File {
		t.Fatalf("a.txt:20 打开的是 %+v", d)
	}
	wait("读 a.txt", loaded(d))
	if line(d) != 19 {
		t.Fatalf("a.txt:20 光标在第 %d 行", line(d)+1)
	}

	// 相对终端现在的目录：在 docs 里 ⌘点 ../a.txt:5:3
	if !a.OpenLink(filepath.Join(root, "docs"), "../a.txt:5:3") {
		t.Fatal("../a.txt:5:3 没打开")
	}
	if c := a.CurrentDoc(); c != d || line(d) != 4 {
		t.Fatalf("../a.txt:5:3 应在同一个标签里跳到第 5 行：%v 第 %d 行", c == d, line(d)+1)
	}
	if !a.OpenLink(filepath.Join(root, "docs"), "guide.md") || a.CurrentDoc().Rel != "docs/guide.md" {
		t.Fatalf("docs 里的 guide.md 没打开：%+v", a.CurrentDoc())
	}

	// file:// 和 ~/ 都能开仓库外的文件
	if !a.OpenLink(root, "file://"+root+"/README.md") || a.CurrentDoc().Rel != "README.md" {
		t.Fatalf("file:// 没打开 README.md：%+v", a.CurrentDoc())
	}
	if !a.OpenLink(root, "~/notes.txt:2") {
		t.Fatal("~/notes.txt:2 没打开")
	}
	o := a.CurrentDoc()
	if o.Abs != outside || o.Kind != docs.File {
		t.Fatalf("~/notes.txt 打开的是 %+v", o)
	}
	wait("读 notes.txt", loaded(o))
	if line(o) != 1 {
		t.Fatalf("~/notes.txt:2 光标在第 %d 行", line(o)+1)
	}

	// 仓库里的目录：在文件树里显示；仓库外的目录交回给终端
	n := len(a.Docs)
	a.St.Sidebar = false
	if !a.OpenLink(root, "./docs") || !a.St.Sidebar || a.St.SidebarMode != workspace.ModeFiles {
		t.Fatal("./docs 没在文件树里显示")
	}
	wait("文件列表", func() bool { return r.Files.Loaded && a.FileSel >= 0 })
	if row := a.TreeRows()[a.FileSel]; !row.Dir || row.Path != "docs" {
		t.Fatalf("选中的是 %+v", row)
	}
	if a.OpenLink(root, home) || len(a.Docs) != n {
		t.Fatal("仓库外的目录不该由应用打开")
	}
}
