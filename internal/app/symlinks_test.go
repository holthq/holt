package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/watch"
)

// 文件树里的符号链接：整棵读时已跟踪的看索引（mode 120000）、未跟踪的看磁盘，指向目录的在树里也是文件；
// 增量刷新新加的链接也认出来、删了的去掉，普通文件和链接互换也认得出，都不整棵读；悬停提示第一段是"→ 目标"。
func TestTreeLinks(t *testing.T) {
	r0 := testutil.NewRepo(t, "links")
	r0.Write("a.txt", "a\n")
	r0.Write("src/b.go", "package src\n")
	link := func(target, p string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(r0.Root, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	link("a.txt", "l")
	link("src", "dl")
	link("nope", "bad")
	r0.Commit("初始")
	link("../a.txt", "src/ul") // 未跟踪
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Repos[0]
	var calls atomic.Int32
	old := lsFiles
	lsFiles = func(ctx context.Context, root string) (git.Listing, error) { calls.Add(1); return old(ctx, root) }
	t.Cleanup(func() { lsFiles = old })
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读文件树", func() bool { return idle(r)() && hasFile(r, "src/ul") })
	want := map[string]files.Link{"l": {Target: "a.txt"}, "dl": {Target: "src", Dir: true}, "bad": {Target: "nope", Broken: true}, "src/ul": {Target: "../a.txt"}}
	if len(r.Files.Links) != len(want) {
		t.Fatalf("Links %v，要 %v", r.Files.Links, want)
	}
	for p, l := range want {
		if r.Files.Links[p] != l {
			t.Errorf("%s：%v，要 %v", p, r.Files.Links[p], l)
		}
	}
	if !r.Files.Has("dl") || r.Files.Dir("dl") != nil {
		t.Error("指向目录的链接在树里该是一个文件")
	}
	rowFor := func(p string) TreeRow {
		for _, tr := range a.TreeRows() {
			if tr.Path == p {
				return tr
			}
		}
		t.Fatalf("树里没有 %s", p)
		return TreeRow{}
	}
	if tip := a.RowTip(rowFor("bad")); tip != "→ nope（目标不存在） · 没有改动" { // 断了的 stat 不到，没有大小和时间
		t.Errorf("断了的链接提示 %q", tip)
	}
	if tip := a.RowTip(rowFor("l")); !strings.HasPrefix(tip, "→ a.txt · ") {
		t.Errorf("链接提示 %q", tip)
	}

	n := calls.Load()
	src := watch.Change{Files: true, Dirs: []string{"src"}}
	link("b.go", "src/nl")
	a.watchChanged(r, src)
	wait("新建链接", func() bool { return idle(r)() && !r.Files.Loading && hasFile(r, "src/nl") })
	if r.Files.Links["src/nl"] != (files.Link{Target: "b.go"}) {
		t.Errorf("增量加的链接 %v", r.Files.Links)
	}
	os.Remove(filepath.Join(r0.Root, "src/ul"))
	a.watchChanged(r, src)
	wait("删链接", func() bool { return idle(r)() && !r.Files.Loading && !r.Files.Has("src/ul") })
	if _, ok := r.Files.Links["src/ul"]; ok {
		t.Error("删了的链接还在 Links 里")
	}
	// 已跟踪的普通文件删掉、同名换成链接：增量刷新认出来，有链接图标；再换回普通文件，图标没了
	os.Remove(filepath.Join(r0.Root, "src/b.go"))
	link("../a.txt", "src/b.go")
	a.watchChanged(r, src)
	wait("文件换成链接", func() bool {
		return idle(r)() && !r.Files.Loading && r.Files.Links["src/b.go"] == (files.Link{Target: "../a.txt"})
	})
	if !r.Files.Has("src/b.go") {
		t.Error("换成链接的文件不在树里了")
	}
	os.Remove(filepath.Join(r0.Root, "src/b.go"))
	os.WriteFile(filepath.Join(r0.Root, "src/b.go"), []byte("package src\n"), 0o644)
	a.watchChanged(r, src)
	wait("链接换回文件", func() bool { _, ok := r.Files.Links["src/b.go"]; return idle(r)() && !r.Files.Loading && !ok })
	if !r.Files.Has("src/b.go") || r.Files.Links["src/nl"] != (files.Link{Target: "b.go"}) {
		t.Errorf("换回文件后：在树里 %v，Links %v", r.Files.Has("src/b.go"), r.Files.Links)
	}
	if got := calls.Load() - n; got != 0 {
		t.Errorf("增量刷新整棵读了 %d 次", got)
	}
}

// 指向目录的链接：画成目录行，展开看到里面（Linked、没有状态字母），打开用穿过链接的路径；悬停说"（目录）"，
// 指向上层的是文件行；里面的不能重命名、删、创建副本（说一声），粘贴 / 新建放到链接外面；移动不接，按着 ⌥ 复制出来可以。
func TestDirLinkRows(t *testing.T) {
	r0 := testutil.NewRepo(t, "dirlinks")
	r0.Write("a.txt", "a\n")
	r0.Write("src/b.go", "package src\n")
	for p, target := range map[string]string{"dl": "src", "src/self": "."} {
		if err := os.Symlink(target, filepath.Join(r0.Root, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	r0.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读文件树", func() bool { return idle(r)() && hasFile(r, "dl") && r.Files.Links["dl"].Dir })
	_, dl := treeRow(t, a, "dl")
	if !dl.Dir || !dl.DirLink() || dl.Linked || dl.Open {
		t.Fatalf("链接目录那行 %+v", dl)
	}
	if tip := a.RowTip(dl); tip != "→ src（目录） · 没有改动" {
		t.Errorf("链接目录提示 %q", tip)
	}
	a.ToggleDir(r, "src")
	if _, self := treeRow(t, a, "src/self"); self.Dir || a.RowTip(self) != "→ .（指向上层目录，不展开） · 没有改动" {
		t.Errorf("指向上层的 %+v，提示 %q", self, a.RowTip(self))
	}
	a.ToggleDir(r, "dl")
	_, in := treeRow(t, a, "dl/b.go")
	if !in.Linked || in.Code != 0 || in.Dir {
		t.Fatalf("链接里面的 %+v", in)
	}
	if _, self := treeRow(t, a, "dl/self"); !self.Linked || self.Dir {
		t.Errorf("链接里面指向上层的 %+v", self)
	}
	d := a.OpenFile(r, "dl/b.go", true)
	wait("打开链接里面的", func() bool { return d.Buf != nil && !d.Loading })
	if d.Rel != "dl/b.go" || d.Buf.String() != "package src\n" {
		t.Errorf("打开的 %q：%q", d.Rel, d.Buf.String())
	}
	a.TrashRows([]TreeRow{in})
	if a.ConfirmOpen || a.Toast != "链接目录里面的只能打开、复制路径、在访达中显示" {
		t.Errorf("删链接里面的：confirm=%v toast=%q", a.ConfirmOpen, a.Toast)
	}
	a.Toast = ""
	a.DuplicateRows([]TreeRow{in})
	if a.Toast == "" {
		t.Error("创建副本没说一声")
	}
	if PasteDir(in) != "" || PasteDir(dl) != "" || CanIgnore([]TreeRow{in}) {
		t.Errorf("粘贴到 %q / %q，能忽略 %v", PasteDir(in), PasteDir(dl), CanIgnore([]TreeRow{in}))
	}
	drag := a.TreeDragOf(in)
	if !drag.Linked() || len(drag.FileRows()) != 1 {
		t.Fatalf("拖着的 %+v", drag)
	}
	a.Toast = ""
	a.DropTree(drag, r, "", false)
	if a.Toast != "链接目录里面的不能移动，按着 ⌥ 拖是复制" || !exists(filepath.Join(r0.Root, "src", "b.go")) {
		t.Fatalf("移动链接里面的：toast=%q", a.Toast)
	}
	a.DropTree(drag, r, "", true)
	wait("⌥ 复制出来", func() bool { return idle(r)() && hasFile(r, "b.go") })
	if !exists(filepath.Join(r0.Root, "src", "b.go")) {
		t.Error("复制出来把原来的拿走了")
	}
}
