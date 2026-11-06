package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/testutil"
)

// symLocs 是列表里每项的 "名字 在哪"。
func symLocs(items []PaletteItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Text+" "+it.Loc)
	}
	return out
}

// 工作区符号的索引（⌘T / "#"）：两个仓库的 go / py / md 都进索引，跨仓库查得到、按名字匹配；
// 标脏的目录只重新 stat 那里的文件、变了才重新解析；文件列表里没了的去掉；开着的文件用标签的大纲；封顶了不再加文件。
func TestSymbolIndex(t *testing.T) {
	one := testutil.NewRepo(t, "one")
	one.Write("main.go", "package main\n\nfunc main() {}\n\nfunc helper() {}\n")
	one.Write("pkg/lib.go", "package pkg\n\nfunc Lib() {}\n")
	one.Write("notes.md", "# Title\n\n## Usage\n")
	one.Write("readme.txt", "main\n")
	one.Commit("初始")
	two := testutil.NewRepo(t, "two")
	two.Write("tool.py", "def main():\n    pass\n\nclass Runner:\n    def run(self):\n        pass\n")
	two.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, one.Root, two.Root))
	t.Cleanup(a.Close)
	p := &a.Palette
	ix := &a.syms
	find := func(q string) []string {
		t.Helper()
		p.Text = "#" + q
		wait("建索引", func() bool {
			a.UpdatePalette()
			return a.Repos[0].Files.Loaded && a.Repos[1].Files.Loaded && ix.files != nil && !ix.building
		})
		a.UpdatePalette()
		return symLocs(p.Items)
	}

	a.OpenPalette("#")
	if got := find("main"); !slices.Equal(got, []string{"main one/main.go:3", "main two/tool.py:1"}) {
		t.Fatalf("#main：%q（%q）", got, p.Note)
	}
	if len(ix.files) != 4 || ix.count != 8 {
		t.Errorf("索引 %d 个文件 %d 个符号，应为 4 个文件（txt 不算）8 个符号", len(ix.files), ix.count)
	}
	if got := find("usage"); !slices.Equal(got, []string{"Usage one/notes.md:3"}) {
		t.Errorf("#usage：%q", got)
	}
	if got := find("run"); !slices.Equal(got, []string{"run two/tool.py:5", "Runner two/tool.py:4"}) {
		t.Errorf("#run（名字短的靠前）：%q", got)
	}
	// 只匹配名字，不匹配路径
	if got := find("tool"); len(got) != 0 || p.Note != "没有匹配的符号" {
		t.Errorf("#tool 不该按路径匹配：%q %q", got, p.Note)
	}

	// 改了 pkg/lib.go，watch 报 pkg：只重新 stat 那一个
	later := time.Now().Add(time.Hour)
	os.WriteFile(filepath.Join(one.Root, "pkg/lib.go"), []byte("package pkg\n\nfunc Lib2() {}\n"), 0o644)
	os.Chtimes(filepath.Join(one.Root, "pkg/lib.go"), later, later)
	a.symFilesChanged(a.Repos[0], []string{"pkg"}, false)
	if got := find("lib"); !slices.Equal(got, []string{"Lib2 one/pkg/lib.go:3"}) {
		t.Errorf("改了之后 #lib：%q", got)
	}
	if ix.total != 1 {
		t.Errorf("标脏 pkg 重新读了 %d 个文件，应只有 1 个", ix.total)
	}
	// 说不清哪里变了（根下的文件）：整个仓库重新 stat，没变的不重新解析
	os.WriteFile(filepath.Join(one.Root, "main.go"), []byte("package main\n\nfunc main() {}\n\nfunc renamed() {}\n"), 0o644)
	os.Chtimes(filepath.Join(one.Root, "main.go"), later, later)
	before := ix.files[filepath.Join(one.Root, "notes.md")]
	a.symFilesChanged(a.Repos[0], nil, true)
	if got := find("helper"); len(got) != 0 {
		t.Errorf("删掉的 helper 还在：%q", got)
	}
	if got := find("renamed"); !slices.Equal(got, []string{"renamed one/main.go:5"}) {
		t.Errorf("新加的 renamed：%q", got)
	}
	if ix.files[filepath.Join(one.Root, "notes.md")] != before {
		t.Error("没变的 notes.md 重新解析了")
	}

	// 文件列表里没了：去掉
	os.Remove(filepath.Join(two.Root, "tool.py"))
	a.LoadFiles(a.Repos[1])
	wait("重读文件列表", func() bool { return a.Repos[1].Files.Loaded && !slices.Contains(a.Repos[1].Files.Paths, "tool.py") })
	if got := find("runner"); len(got) != 0 {
		t.Errorf("删了的 tool.py 还在：%q", got)
	}

	// 开着的文件：用标签的大纲（改了没存的也算），不重复列
	d := a.OpenFile(a.Repos[0], "main.go", true)
	wait("读 main.go", func() bool { return d.Buf != nil && !d.Loading })
	d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: "\nfunc unsaved() {}\n"})
	if got := find("unsaved"); !slices.Equal(got, []string{"unsaved one/main.go:7"}) {
		t.Errorf("开着的文件没存的符号：%q", got)
	}
	if got := find("main"); !slices.Equal(got, []string{"main one/main.go:3"}) {
		t.Errorf("开着的文件重复列了：%q", got)
	}

	// 回车打开文件跳到那一行，记进最近跳过的
	find("lib2")
	a.AcceptPalette(0)
	if cur := a.CurrentDoc(); cur == nil || cur.Rel != "pkg/lib.go" || p.Open {
		t.Fatalf("回车没打开 pkg/lib.go：%+v", cur)
	}
	cur := a.CurrentDoc()
	wait("读 lib.go", func() bool { return cur.Buf != nil })
	if pos, _ := cur.Cursor(); pos.Line != 2 {
		t.Errorf("光标在 %+v，应在 Lib2 那行", pos)
	}
	a.OpenPalette("#")
	if got := find(""); !slices.Equal(got, []string{"Lib2 one/pkg/lib.go:3"}) || !strings.Contains(p.Note, "名字@符号") {
		t.Errorf("空查询应列最近跳过的符号：%q %q", got, p.Note)
	}

	// 封顶：符号到了就不再加文件
	old := maxIndexSymbols
	maxIndexSymbols = 3 // 这时盘上一共 5 个符号
	t.Cleanup(func() { maxIndexSymbols = old })
	a2, wait2 := newTestApp(t, testutil.Workspace(t, one.Root, two.Root))
	t.Cleanup(a2.Close)
	a2.OpenPalette("#")
	wait2("建索引", func() bool {
		a2.UpdatePalette()
		return a2.Repos[0].Files.Loaded && a2.Repos[1].Files.Loaded && a2.syms.files != nil && !a2.syms.building
	})
	a2.UpdatePalette()
	if ix2 := &a2.syms; !ix2.capped || ix2.count > 3 || !strings.HasPrefix(a2.Palette.Note, "索引太大，只建了前 ") {
		t.Errorf("封顶：capped %v，%d 个符号，提示 %q", ix2.capped, ix2.count, a2.Palette.Note)
	}
}
