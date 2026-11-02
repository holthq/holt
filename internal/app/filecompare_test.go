package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
)

// compareLoaded 是比较标签读完了（DiffSt 或一句说明）。
func compareLoaded(d *docs.Doc) func() bool {
	return func() bool { return !d.Loading && (d.DiffSt != nil || d.Info != "") }
}

// TestCompareFiles：选一个再和另一个比较开成只读的 Compare 标签（标题、左边的仓库、改动处数），同样两边再开切到同一个；
// 同名的带上一级目录；仓库外的文件也能比；二进制、目录只给一句说明；磁盘上改了跟着重读；没有"下一个文件"、
// 关了不进 ⇧⌘T、不记进会话；"在编辑器中打开"开左边的文件。
func TestCompareFiles(t *testing.T) {
	r0 := testutil.NewRepo(t, "cmp")
	r0.Write("a.go", "package a\n\nfunc A() int { return 1 }\n")
	r0.Write("b.go", "package a\n\nfunc A() int { return 2 }\n\nfunc B() {}\n")
	r0.Write("one/x.go", "package one\n")
	r0.Write("two/x.go", "package two\n")
	r0.Write("bin.dat", "a\x00b")
	r0.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Repos[0]

	a.SelectForCompare(r, "a.go")
	if a.CanCompareWithSel(r, "a.go") || !a.CanCompareWithSel(r, "b.go") || a.CompareSelName() != "a.go" {
		t.Fatalf("选了 a.go：和自己 %v、和 b.go %v、名字 %q", a.CanCompareWithSel(r, "a.go"), a.CanCompareWithSel(r, "b.go"), a.CompareSelName())
	}
	d := a.CompareWithSel(r, "b.go")
	if a.CompareSel != nil {
		t.Error("比较完已选的应该清掉")
	}
	if d == nil || d.Kind != docs.Compare || d.Title != "a.go ↔ b.go" || d.Root != r.Root || d.Rel != "a.go" || !d.Pinned {
		t.Fatalf("比较标签 %+v", d)
	}
	wait("读两边", compareLoaded(d))
	if d.DiffSt == nil {
		t.Fatalf("没读出 diff：%q", d.Info)
	}
	if added, deleted := d.DiffSt.Stats(); added != 3 || deleted != 1 {
		t.Errorf("+%d −%d，想要 +3 −1", added, deleted)
	}
	if _, n, _ := HunkPos(d); n == 0 || !onDiffTab(a) || a.StepFile(1) {
		t.Errorf("比较标签：%d 处改动、onDiffTab %v、StepFile 不该动", n, onDiffTab(a))
	}
	if again := a.CompareFiles(filepath.Join(r.Root, "a.go"), filepath.Join(r.Root, "b.go")); again != d || len(a.Docs) != 1 {
		t.Errorf("同样两边应该切到开着的那个：%p / %p，%d 个标签", again, d, len(a.Docs))
	}
	if _, ok := sessionTab(d); ok {
		t.Error("比较标签不该记进会话")
	}

	// 磁盘上改了右边：syncDocs 重读
	r0.Write("b.go", "package a\n\nfunc A() int { return 1 }\n")
	a.syncDocs(r)
	wait("重读", func() bool {
		if d.DiffSt == nil {
			return false
		}
		added, deleted := d.DiffSt.Stats()
		return added == 0 && deleted == 0
	})

	same := a.CompareFiles(filepath.Join(r.Root, "one/x.go"), filepath.Join(r.Root, "two/x.go"))
	if same.Title != "one/x.go ↔ two/x.go" {
		t.Errorf("同名的标题 %q", same.Title)
	}
	out := filepath.Join(t.TempDir(), "outside.go")
	os.WriteFile(out, []byte("package a\n"), 0o644)
	ext := a.CompareFiles(out, filepath.Join(r.Root, "a.go"))
	wait("仓库外的", compareLoaded(ext))
	if ext.Root != "" || ext.DiffSt == nil || !compareTouches(ext, r) {
		t.Errorf("仓库外的左边：Root %q、DiffSt %v", ext.Root, ext.DiffSt != nil)
	}
	bin := a.CompareFiles(filepath.Join(r.Root, "a.go"), filepath.Join(r.Root, "bin.dat"))
	wait("二进制", compareLoaded(bin))
	if bin.Info != "二进制文件，不显示 diff" {
		t.Errorf("二进制：%q", bin.Info)
	}
	dir := a.CompareFiles(filepath.Join(r.Root, "a.go"), filepath.Join(r.Root, "one"))
	wait("目录", compareLoaded(dir))
	if !strings.Contains(dir.Info, "是目录") {
		t.Errorf("目录：%q", dir.Info)
	}

	n := len(a.closed)
	a.RemoveDoc(dir)
	if len(a.closed) != n {
		t.Error("关掉的比较标签不该进 ⇧⌘T")
	}
	a.SelectDoc(slices.Index(a.Docs, d))
	if f := a.CompareLeft(d); f == nil || f.Kind != docs.File || f.Rel != "a.go" {
		t.Errorf("在编辑器中打开左边：%+v", f)
	}
	a.SelectDoc(slices.Index(a.Docs, ext))
	if f := a.CompareLeft(ext); f == nil || f.Abs != out {
		t.Errorf("仓库外的左边：%+v", f)
	}
}

// TestCompareClipboard：剪贴板空着只提示；和剪贴板比的标题是“a.go ↔ 剪贴板”，同一个文件再比换成新的文字；
// 命令"比较当前文件与已选项目"只在选过别的文件、当前是文件标签时能用，已选的在左。
func TestCompareClipboard(t *testing.T) {
	r0 := testutil.NewRepo(t, "clip")
	r0.Write("a.go", "one\ntwo\n")
	r0.Write("b.go", "uno\n")
	r0.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Repos[0]
	clip := ""
	old := ReadClipboard
	ReadClipboard = func() string { return clip }
	t.Cleanup(func() { ReadClipboard = old })

	if d := a.CompareClipboard(r, "a.go"); d != nil || a.Toast != "剪贴板是空的" || len(a.Docs) != 0 {
		t.Fatalf("剪贴板空着：%v、%q", d, a.Toast)
	}
	clip = "one\nthree\n"
	d := a.CompareClipboard(r, "a.go")
	if d == nil || d.Title != "a.go ↔ 剪贴板" || d.Right != "" {
		t.Fatalf("和剪贴板比 %+v", d)
	}
	wait("读", compareLoaded(d))
	if d.DiffSt == nil || d.DiffSt.New.String() != clip {
		t.Fatalf("右边应是剪贴板：%v %q", d.DiffSt != nil, d.Info)
	}
	clip = "one\ntwo\n"
	if again := a.CompareClipboard(r, "a.go"); again != d || len(a.Docs) != 1 {
		t.Fatalf("同一个文件再和剪贴板比应是同一个标签")
	}
	wait("换成新的剪贴板", func() bool { return d.DiffSt != nil && d.DiffSt.New.String() == clip })

	f := a.OpenFile(r, "a.go", true)
	wait("读 a.go", func() bool { return f.Buf != nil })
	if a.CanCompareCurrent() || a.CanRun(Find("diff.compareWithSel")) {
		t.Error("没选过不该能比较当前文件")
	}
	a.SelectForCompare(r, "a.go")
	if a.CanCompareCurrent() {
		t.Error("选的就是当前文件，不该能比")
	}
	a.SelectForCompare(r, "b.go")
	a.Exec("diff.compareWithSel")
	cd := a.CurrentDoc()
	if cd.Kind != docs.Compare || cd.Title != "b.go ↔ a.go" || a.CompareSel != nil {
		t.Fatalf("比较当前文件与已选项目：%q、已选 %v", cd.Title, a.CompareSel)
	}
}
