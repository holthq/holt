package app

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 同一个文件在两组里开（镜像）：⇧⌘\ 向右分出一组、开一份共用缓冲区的，光标各自的；再按切到已经有的那份；
// 改一边另一边跟着、脏点两边亮、没保存的只算一个；从镜像保存后两边都算存了；外部改了两边一起重读，
// 改着没存时两边都冲突，“保留我的”点哪边都算；换编码（新的缓冲区）两边跟着；打开文件找的是正本；
// 关正本不问、镜像提成正本，关最后一份才问；Diff、图片、十六进制开不了镜像，开着几份时不让切十六进制。
func TestMirrorTabs(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("git status", idle(r))

	d := a.OpenFile(r, "a.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	d.St.GoTo(editor.Pos{Line: 5})
	if !a.CanMirror(d) {
		t.Fatal("读好的文本应能再开一份")
	}
	a.Exec("view.openInOtherGroup")
	m := a.CurrentDoc()
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"a.txt"}) || !slices.Equal(got[1], []string{"a.txt"}) {
		t.Fatalf("分出来的组：%q", got)
	}
	if m == d || m.Copy != 1 || d.Copy != 0 || m.Buf != d.Buf || m.St.Buffer != d.Buf || m.Base != d.Base || m.Key() == d.Key() || !m.Pinned {
		t.Fatalf("镜像：copy %d，共用缓冲区 %v、基准 %v", m.Copy, m.Buf == d.Buf, m.Base == d.Base)
	}
	if m.St.CursorPos().Line != 5 || a.GroupOf(m) != a.Grp || !a.FocusDoc {
		t.Fatalf("镜像的光标 %+v，键盘在镜像那组 %v", m.St.CursorPos(), a.GroupOf(m) == a.Grp)
	}
	m.St.GoTo(editor.Pos{Line: 30})
	if d.St.CursorPos().Line != 5 {
		t.Errorf("光标该各自的：正本 %+v", d.St.CursorPos())
	}
	// 再按一次（从正本那组）：切到右边已有的那份，不再开第三份
	a.FocusGroup(a.GroupOf(d))
	if x := a.OpenMirror(d); x != m || len(a.AllDocs()) != 2 || a.CurrentDoc() != m {
		t.Fatalf("再开一份：%v，共 %d 个标签", x == m, len(a.AllDocs()))
	}
	if got := a.siblings(m); len(got) != 2 || !a.hasCopy(d) {
		t.Fatalf("兄弟：%d", len(got))
	}

	// 改一边：另一边跟着、两边都算改过，没保存的只算一个
	m.Buf.Edit(editor.Edit{Text: "镜像里写的\n"})
	if !d.Modified() || !m.Modified() || !strings.HasPrefix(d.Buf.String(), "镜像里写的\n") || a.DirtyDocs() != 1 {
		t.Fatalf("改动：正本 %v 镜像 %v，没保存的 %d", d.Modified(), m.Modified(), a.DirtyDocs())
	}
	// 从镜像保存：磁盘上是新的，两边都存了、时间一样
	a.Save(m)
	wait("保存", func() bool { return !m.Modified() })
	if got, _ := os.ReadFile(d.Abs); !strings.HasPrefix(string(got), "镜像里写的\n") {
		t.Fatalf("磁盘上：%q", string(got)[:20])
	}
	if d.Modified() || !d.Mtime.Equal(m.Mtime) || d.Size != m.Size {
		t.Fatalf("保存后正本：改过 %v，时间 %v / %v", d.Modified(), d.Mtime, m.Mtime)
	}

	// 外部改了（没改着）：正本重读，镜像跟着（同一个缓冲区）
	later := time.Now().Add(2 * time.Second)
	os.WriteFile(d.Abs, []byte("外面改的\n"), 0o644)
	os.Chtimes(d.Abs, later, later)
	a.RefreshRepo(r, WhyManual)
	wait("重读", func() bool { return d.Buf.String() == "外面改的\n" })
	if m.Buf != d.Buf || m.Buf.String() != "外面改的\n" || !m.Mtime.Equal(d.Mtime) || m.Conflict || d.Conflict {
		t.Fatalf("外部改了：镜像 %q，共用 %v", m.Buf.String(), m.Buf == d.Buf)
	}

	// 改着没存时外部又改了：两边都冲突；镜像上点“保留我的”，正本也不冲突了
	d.Buf.Edit(editor.Edit{Text: "x"})
	later = later.Add(2 * time.Second)
	os.WriteFile(d.Abs, []byte("外面又改了\n"), 0o644)
	os.Chtimes(d.Abs, later, later)
	a.RefreshRepo(r, WhyManual)
	wait("冲突", func() bool { return d.Conflict })
	if !m.Conflict {
		t.Fatal("镜像上没冲突条")
	}
	a.KeepMine(m)
	if d.Conflict || m.Conflict || d.NeedsOverwrite() {
		t.Fatalf("保留我的：正本冲突 %v", d.Conflict)
	}
	// 关正本：不问（改动还在镜像里），镜像提成正本；关最后一份才问
	a.CloseDoc(d)
	if a.CloseReq != nil || a.GroupOf(d) != nil || m.Copy != 0 || a.Split() || len(a.closed) != 0 {
		t.Fatalf("关正本：问了 %v，镜像 copy %d，分屏 %v，⇧⌘T 记了 %d", a.CloseReq != nil, m.Copy, a.Split(), len(a.closed))
	}
	if x := a.OpenFile(r, "a.txt", true); x != m {
		t.Fatal("打开文件没找提上来的那份")
	}
	a.CloseDoc(m)
	if a.CloseReq != m {
		t.Fatal("关最后一份没问")
	}
	a.CloseReq, a.CloseOpen = nil, false
	m.Buf.Undo()
	a.KeepMine(m)

	// 换编码（后台建新的缓冲区）：两边一起换
	n := a.OpenMirror(m)
	a.ReopenWithEncoding(m, docs.Encoding("GB18030"))
	wait("换编码", func() bool { return m.ForcedEnc == "GB18030" && n.ForcedEnc == "GB18030" && !m.Loading })
	if n.Buf != m.Buf || n.St.Buffer != m.Buf {
		t.Fatal("换编码后镜像没跟上新的缓冲区")
	}
	if a.CanHex(m) || a.CanHex(n) {
		t.Error("开着两份时不该能切十六进制")
	}

	// Diff、图片标签开不了镜像
	diff := a.OpenDiff(r, git.Change{Kind: git.Unstaged, Path: "a.txt"}, true)
	if a.CanMirror(diff) || a.CanRun(Find("view.openInOtherGroup")) {
		t.Error("Diff 标签能再开一份")
	}
	os.WriteFile(filepath.Join(root, "p.png"), pngBytes(2, 2, color.RGBA{1, 2, 3, 255}), 0o644)
	p := a.OpenFile(r, "p.png", true)
	wait("图片", func() bool { return p.Bitmap != nil })
	if a.CanMirror(p) {
		t.Error("图片能再开一份")
	}
}

// 镜像记进会话：同一个路径的第二个标签重开时挂成第一个的镜像（共用缓冲区，还在读的读完跟上），各自的光标和组照旧。
func TestMirrorSession(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	r := a.Repos[0]
	d := a.OpenFile(r, "a.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	d.St.GoTo(editor.Pos{Line: 3})
	m := a.OpenMirror(d)
	m.St.GoTo(editor.Pos{Line: 40})
	a.SaveState()
	a.Close()

	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[ws]
	if len(s.Tabs) != 2 || s.Tabs[0].Copy != 0 || s.Tabs[1].Copy != 1 || s.Tabs[0].Path != s.Tabs[1].Path || s.Layout == nil {
		t.Fatalf("存的：%+v，布局 %v", s.Tabs, s.Layout != nil)
	}
	b, wait := newTestAppIn(t, ws, stateDir)
	b.restoreTabs()
	t.Cleanup(b.Close)
	wait("重开读完", func() bool {
		return !slices.ContainsFunc(b.AllDocs(), func(d *docs.Doc) bool { return d.Loading || d.Buf == nil })
	})
	if got := groupTitles(b); len(got) != 2 || len(got[0]) != 1 || len(got[1]) != 1 {
		t.Fatalf("重开的组：%q", got)
	}
	x, y := b.Groups()[0], b.Groups()[1]
	bd, _ := b.GroupDocs(x)
	bm, _ := b.GroupDocs(y)
	if bd[0].Copy != 0 || bm[0].Copy != 1 || bd[0].Buf != bm[0].Buf {
		t.Fatalf("重开的：copy %d / %d，共用 %v", bd[0].Copy, bm[0].Copy, bd[0].Buf == bm[0].Buf)
	}
	if bd[0].St.CursorPos().Line != 3 || bm[0].St.CursorPos().Line != 40 {
		t.Fatalf("光标：%+v / %+v", bd[0].St.CursorPos(), bm[0].St.CursorPos())
	}
}
