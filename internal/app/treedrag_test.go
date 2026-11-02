package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
)

// 文件树里的拖放都在临时仓库上测：移动、从外面复制进来、拖进窗口打开。

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// treeRow 是文件树里路径为 p 的行。
func treeRow(t *testing.T, a *App, p string) (int, TreeRow) {
	t.Helper()
	for i, row := range a.TreeRows() {
		if !row.Root && row.Note == "" && row.Path == p {
			return i, row
		}
	}
	t.Fatalf("树里没有 %s", p)
	return -1, TreeRow{}
}

func TestMoveTree(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	for _, p := range []string{"src/m.go", "lib/x.txt", "docs/n.md", "dst/a.txt"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(p+"\n"), 0o644)
	}
	a.LoadFiles(r)
	m := a.OpenFile(r, "src/m.go", true)
	n := a.OpenFile(r, "docs/n.md", true)
	wait("读好", func() bool { return idle(r)() && hasFile(r, "dst/a.txt") && m.Buf != nil && n.Buf != nil })
	r.Files.SetOpen("src", true)

	// 一个干净的文件：直接移；目标目录展开、文件列表重读好了选中它
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"u.txt"}}, r, "lib")
	if a.ConfirmOpen || !exists(filepath.Join(root, "lib", "u.txt")) || exists(filepath.Join(root, "u.txt")) || !r.Files.Open["lib"] {
		t.Fatalf("没直接移过去：confirm=%v open=%v", a.ConfirmOpen, r.Files.Open)
	}
	wait("树里换到 lib 下", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return idle(r)() && hasFile(r, "lib/u.txt") && !hasFile(r, "u.txt") && ok && row.Path == "lib/u.txt"
	})
	if e := a.Output[len(a.Output)-1]; e.Title != "移动 u.txt → lib/u.txt" || e.Failed {
		t.Errorf("输出栏：%+v", e)
	}

	// 本来就在这个目录、拖进它自己：都不动
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"lib/u.txt"}}, r, "lib")
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"lib"}}, r, "lib")
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"lib"}}, r, "lib/sub")
	if a.ConfirmOpen || !exists(filepath.Join(root, "lib", "u.txt")) || !exists(filepath.Join(root, "lib", "x.txt")) {
		t.Fatal("空操作也动了")
	}

	// 别的仓库的：提示，不动
	a.MoveTree(TreeDrag{Paths: []string{"lib/x.txt"}}, r, "")
	if a.Toast != "只能在同一个仓库里移动" || !exists(filepath.Join(root, "lib", "x.txt")) {
		t.Fatalf("跨仓库：%q", a.Toast)
	}

	// 目录：先问；里面打开着的标签和展开状态跟着
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"src"}}, r, "lib")
	if !a.ConfirmOpen || a.Confirm.Title != "把 src 移到 lib？" || a.Confirm.Button != "移动" || !exists(filepath.Join(root, "src")) {
		t.Fatalf("目录没先问：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if m.Rel != "lib/src/m.go" || m.Abs != filepath.Join(root, "lib", "src", "m.go") || !r.Files.Open["lib/src"] || r.Files.Open["src"] {
		t.Fatalf("目录移过去后：%q open=%v", m.Rel, r.Files.Open)
	}

	// 有改了没保存的标签：问的时候说，移过去改动留着
	n.Buf.Set("改了\n")
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"docs/n.md"}}, r, "")
	if !a.ConfirmOpen || a.Confirm.Title != "把 n.md 移到 仓库根目录？" || !strings.Contains(a.Confirm.Message, "有 1 个打开的标签改了没保存") {
		t.Fatalf("没保存的：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if n.Rel != "n.md" || !n.Modified() || n.Buf.String() != "改了\n" {
		t.Fatalf("标签没跟上：%q modified=%v", n.Rel, n.Modified())
	}

	// 多选：目标里已经有同名的，停在那一个、报错，前面的移好了、后面的不动
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"README.md", "a.txt", "n.md"}}, r, "dst")
	if !a.ConfirmOpen || a.Confirm.Title != "把这 3 项移到 dst？" {
		t.Fatalf("多选：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if !exists(filepath.Join(root, "dst", "README.md")) || !exists(filepath.Join(root, "a.txt")) || !exists(filepath.Join(root, "n.md")) ||
		mustRead(t, filepath.Join(root, "dst", "a.txt")) != "dst/a.txt\n" {
		t.Fatal("多选移动的结果不对")
	}
	if !a.AlertOpen || a.AlertTitle != "没能移动" || a.AlertMsg != "已经有 a.txt 了" {
		t.Fatalf("同名没报错：%v %q %q", a.AlertOpen, a.AlertTitle, a.AlertMsg)
	}
	a.AlertOpen = false
	wait("树里", func() bool { return idle(r)() && hasFile(r, "dst/README.md") && hasFile(r, "lib/src/m.go") })

	// 拖起来带着的：选中的行只带最外面的（目录里面的不重复算），没选中的只带它自己
	r.Files.SetOpen("dst", true)
	_, dst := treeRow(t, a, "dst")
	_, inDst := treeRow(t, a, "dst/a.txt")
	_, lib := treeRow(t, a, "lib")
	a.FileChosen.Clear()
	a.FileChosen.Add(dst.Key())
	a.FileChosen.Add(inDst.Key())
	if d := a.TreeDragOf(inDst); d.Repo != r || !slices.Equal(d.Paths, []string{"dst"}) {
		t.Errorf("选中的拖起来：%+v", d)
	}
	if d := a.TreeDragOf(lib); !slices.Equal(d.Paths, []string{"lib"}) {
		t.Errorf("没选中的拖起来：%+v", d)
	}
}

func TestDropFiles(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	os.MkdirAll(filepath.Join(root, "in"), 0o755)
	os.WriteFile(filepath.Join(root, "in", ".keep"), nil, 0o644)
	wait("读好", func() bool { return idle(r)() && hasFile(r, "a.txt") })

	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "x.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(out, "z.txt"), []byte("z\n"), 0o644)
	os.MkdirAll(filepath.Join(out, "pkg", "sub"), 0o755)
	os.WriteFile(filepath.Join(out, "pkg", "sub", "y.txt"), []byte("y\n"), 0o644)

	// 仓库外的：先问，点了才复制；文件保留权限位，目录整个复制
	a.DropFiles([]string{filepath.Join(out, "x.sh"), filepath.Join(out, "pkg")}, r, "in")
	if !a.ConfirmOpen || a.Confirm.Title != "把 2 项复制到 in？" || a.Confirm.Button != "复制" ||
		!strings.Contains(a.Confirm.Message, "x.sh") || !strings.Contains(a.Confirm.Message, "pkg") {
		t.Fatalf("没先问：%+v", a.Confirm)
	}
	if exists(filepath.Join(root, "in", "x.sh")) {
		t.Fatal("问之前就复制了")
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("复制好", func() bool { return idle(r)() && hasFile(r, "in/x.sh") && hasFile(r, "in/pkg/sub/y.txt") })
	if fi, err := os.Stat(filepath.Join(root, "in", "x.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("权限位：%v %v", fi, err)
	}
	if mustRead(t, filepath.Join(root, "in", "pkg", "sub", "y.txt")) != "y\n" || !r.Files.Open["in"] {
		t.Errorf("目录没整个复制过来，或 in 没展开：%v", r.Files.Open)
	}
	if row, ok := a.TreeRowAt(a.FileSel); !ok || row.Path != "in/x.sh" {
		t.Errorf("没选中复制进来的第一个：%+v", row)
	}

	// 同名的已经有了：停在那一个报错，前面的复制好了
	a.DropFiles([]string{filepath.Join(out, "z.txt"), filepath.Join(out, "x.sh")}, r, "in")
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("报错", func() bool { return a.AlertOpen })
	if a.AlertTitle != "没能复制" || a.AlertMsg != "x.sh：已经有 x.sh 了" || !exists(filepath.Join(root, "in", "z.txt")) {
		t.Fatalf("同名：%q %q", a.AlertTitle, a.AlertMsg)
	}
	a.AlertOpen = false
	if e := a.Output[len(a.Output)-1]; !e.Failed || !strings.Contains(e.Title, "x.sh") {
		t.Errorf("输出栏：%+v", e)
	}

	// 把包着仓库的目录拖进仓库：不复制进它自己里面
	a.DropFiles([]string{filepath.Dir(root)}, r, "in")
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("报错", func() bool { return a.AlertOpen })
	if a.AlertMsg != filepath.Base(filepath.Dir(root))+"：不能复制到它自己里面" || exists(filepath.Join(root, "in", filepath.Base(filepath.Dir(root)))) {
		t.Fatalf("复制进自己里面：%q", a.AlertMsg)
	}
	a.AlertOpen = false

	// 本来就在这个仓库里的（访达里拖的是仓库里的文件）：当移动，一个干净的文件不问
	wait("空闲", idle(r))
	a.DropFiles([]string{filepath.Join(root, "u.txt")}, r, "in")
	if a.ConfirmOpen || !exists(filepath.Join(root, "in", "u.txt")) || exists(filepath.Join(root, "u.txt")) {
		t.Fatalf("同仓库的没当移动：confirm=%+v", a.Confirm)
	}
	wait("树里", func() bool { return idle(r)() && hasFile(r, "in/u.txt") })
}

// 拖进窗口、没落在文件树目录行上的（main.go 的 OnFileDrop）照命令行参数开：文件开标签，没加过的仓库先问。
func TestOpenArgsDrop(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	other := testutil.NewRepo(t, "other")
	other.Write("f.txt", "f\n")
	other.Commit("f")
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.OpenArgs([]string{filepath.Join(root, "a.txt"), other.Root}, "")
	d := a.CurrentDoc()
	if d == nil || d.Rel != "a.txt" || a.RepoOf(d) != r {
		t.Fatalf("文件没开标签：%+v", d)
	}
	if !a.ConfirmOpen || a.Confirm.Title != "添加仓库" || !strings.Contains(a.Confirm.Message, realDir(t, other.Root)) {
		t.Fatalf("目录没问：%+v", a.Confirm)
	}
	wait("读好", func() bool { return d.Buf != nil })
	if len(a.Repos) != 1 {
		t.Fatal("问之前就加进来了")
	}
}

// 跨仓库拖 = 复制：别的仓库来的先问（说是复制），点了才复制，原来的不动，同名的加"副本"；
// 同一个仓库里按着 ⌥ 是复制（不问；就放回原来的目录也行，成了副本），不按是移动；拖进自己里面、选中跨了仓库的不动。
func TestDropTreeCopy(t *testing.T) {
	proj := testutil.NewRepo(t, "proj")
	for _, p := range []string{"u.txt", "lib/x.txt", "pkg/sub/y.txt"} {
		proj.Write(p, p+"\n")
	}
	proj.Commit("init")
	other := testutil.NewRepo(t, "other")
	other.Write("u.txt", "old\n")
	other.Commit("init")
	a, wait := newTestApp(t, testutil.Workspace(t, proj.Root, other.Root))
	pr, or := a.Repos[0], a.Repos[1]
	for _, r := range a.Repos {
		a.RefreshRepo(r, WhyManual)
		a.EnsureFiles(r)
	}
	wait("读好", func() bool { return idle(pr)() && idle(or)() && hasFile(pr, "pkg/sub/y.txt") && hasFile(or, "u.txt") })
	in := func(r *testutil.Repo, p string) string { return filepath.Join(r.Root, filepath.FromSlash(p)) }

	// 别的仓库来的：先问，点了才复制；同名的 u.txt 成了"u 副本.txt"，目录整个复制
	a.DropTree(TreeDrag{Repo: pr, Paths: []string{"u.txt", "pkg"}}, or, "", false)
	if !a.ConfirmOpen || a.Confirm.Title != "把 2 项复制到 other？" || a.Confirm.Button != "复制" || !strings.Contains(a.Confirm.Message, "proj 里原来的不动") {
		t.Fatalf("跨仓库没先问复制：%+v", a.Confirm)
	}
	if exists(in(other, "pkg")) {
		t.Fatal("问之前就复制了")
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("复制到 other", func() bool { return idle(or)() && hasFile(or, "u 副本.txt") && hasFile(or, "pkg/sub/y.txt") })
	if mustRead(t, in(other, "u 副本.txt")) != "u.txt\n" || mustRead(t, in(other, "u.txt")) != "old\n" || !exists(in(proj, "u.txt")) || !exists(in(proj, "pkg/sub/y.txt")) {
		t.Fatal("跨仓库复制的结果不对")
	}
	if row, ok := a.TreeRowAt(a.FileSel); !ok || row.Repo != or || row.Path != "u 副本.txt" {
		t.Errorf("没选中复制过去的第一个：%+v", row)
	}
	a.DropTree(TreeDrag{Repo: pr, Paths: []string{"lib/x.txt"}}, or, "pkg", false)
	if !a.ConfirmOpen || a.Confirm.Title != "把 x.txt 复制到 other/pkg？" {
		t.Fatalf("复制到别的仓库的目录：%+v", a.Confirm)
	}
	a.ConfirmOpen = false

	// 同一个仓库里按着 ⌥：不问，复制；放回原来的目录成了副本
	a.DropTree(TreeDrag{Repo: pr, Paths: []string{"u.txt"}}, pr, "lib", true)
	a.DropTree(TreeDrag{Repo: pr, Paths: []string{"lib/x.txt"}}, pr, "lib", true)
	if a.ConfirmOpen {
		t.Fatalf("⌥ 拖在同一个仓库里问了：%+v", a.Confirm)
	}
	wait("⌥ 复制", func() bool { return idle(pr)() && hasFile(pr, "lib/u.txt") && hasFile(pr, "lib/x 副本.txt") })
	if !exists(in(proj, "u.txt")) || !exists(in(proj, "lib/x.txt")) {
		t.Fatal("⌥ 复制把原来的拿走了")
	}

	// 不按 ⌥：移动
	a.DropTree(TreeDrag{Repo: pr, Paths: []string{"lib/x.txt"}}, pr, "", false)
	if a.ConfirmOpen || !exists(in(proj, "x.txt")) || exists(in(proj, "lib/x.txt")) {
		t.Fatalf("不按 ⌥ 没移动：confirm=%v", a.ConfirmOpen)
	}

	// 拖进自己里面（复制也不行）、选中跨了仓库的：都不动
	a.DropTree(TreeDrag{Repo: pr, Paths: []string{"pkg"}}, pr, "pkg/sub", true)
	if a.ConfirmOpen || exists(in(proj, "pkg/sub/pkg")) {
		t.Fatal("复制进自己里面了")
	}
	a.DropTree(TreeDrag{Paths: []string{"u.txt"}}, or, "", false)
	if a.ConfirmOpen || a.Toast != "选中的在好几个仓库里，不能一起拖" {
		t.Fatalf("选中跨了仓库：%q", a.Toast)
	}
	wait("刷新完", func() bool { return idle(pr)() && idle(or)() })
}

// 复制保留链接：链接复制成一样的链接（相对的目标原样，断了的也照写，指向目录的不复制里面），
// 目录自己一层层走、里面的链接也是链接，目录和文件的权限位留着；管道之类不复制、报错。
func TestCopyInLinks(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	at := func(root string, p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	os.MkdirAll(at(src, "d/sub"), 0o755)
	os.WriteFile(at(src, "d/run.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(at(src, "d/sub/x.txt"), []byte("x\n"), 0o644)
	links := map[string]string{"d/rel": "sub/x.txt", "d/bad": "../nope", "d/dl": "sub", "top": "d/run.sh"}
	for p, target := range links {
		if err := os.Symlink(target, at(src, p)); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(at(src, "d/sub"), 0o750)
	if isDir, err := copyIn(at(src, "d"), at(dst, "d")); !isDir || err != nil {
		t.Fatalf("复制目录：%v %v", isDir, err)
	}
	if isDir, err := copyIn(at(src, "top"), at(dst, "top")); isDir || err != nil {
		t.Fatalf("复制链接：%v %v", isDir, err)
	}
	if isDir, err := copyIn(at(src, "d/dl"), at(dst, "dl2")); isDir || err != nil {
		t.Fatalf("复制指向目录的链接：%v %v", isDir, err)
	}
	links["dl2"] = "sub"
	for p, target := range links {
		if got, err := os.Readlink(at(dst, p)); err != nil || got != target {
			t.Errorf("%s：%q %v，要链接 %q", p, got, err, target)
		}
	}
	for p, mode := range map[string]os.FileMode{"d/run.sh": 0o755, "d/sub": 0o750, "d/sub/x.txt": 0o644} {
		if fi, err := os.Lstat(at(dst, p)); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s 权限 %v %v，要 %v", p, fi.Mode().Perm(), err, mode)
		}
	}
	if mustRead(t, at(dst, "d/sub/x.txt")) != "x\n" {
		t.Error("目录里的文件内容不对")
	}
	os.MkdirAll(at(src, "p"), 0o755)
	if err := syscall.Mkfifo(at(src, "p/fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := copyIn(at(src, "p"), at(dst, "p")); err == nil || !strings.Contains(err.Error(), "不是普通文件") {
		t.Errorf("管道：%v", err)
	}
}

// 文件树的行拖到编辑器的一组上：里面的文件按顺序在那组开成固定标签（开在别组的搬过来），目录不算，全是目录什么都不做。
func TestOpenDropped(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "m.go"), []byte("package m\n"), 0o644)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	a.LoadFiles(r)
	readme := a.OpenFile(r, "README.md", true)
	at := a.OpenFile(r, "a.txt", true)
	u := a.OpenFile(r, "u.txt", false)
	wait("读完", func() bool {
		return idle(r)() && hasFile(r, "src/m.go") && !readme.Loading && !at.Loading && !u.Loading
	})
	a.Grp.W, a.Grp.H = 1200, 800
	a.SplitEditor(true)
	left, right := a.Groups()[0], a.Groups()[1]
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[1], []string{"u.txt"}) {
		t.Fatalf("分屏：%q", got)
	}
	a.FocusGroup(left)
	r.Files.SetOpen("src", true)

	_, dir := treeRow(t, a, "src")
	_, ar := treeRow(t, a, "a.txt")
	_, mr := treeRow(t, a, "src/m.go")
	a.OpenDropped(TreeDrag{Repo: r, Paths: []string{"src"}, Rows: []TreeRow{dir}}, right)
	if got := groupTitles(a); !slices.Equal(got[0], []string{"README.md", "a.txt"}) || !slices.Equal(got[1], []string{"u.txt"}) || a.Grp != left {
		t.Fatalf("只有目录也动了：%q", got)
	}
	a.OpenDropped(TreeDrag{Repo: r, Paths: []string{"src", "a.txt", "src/m.go"}, Rows: []TreeRow{dir, ar, mr}}, right)
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"u.txt", "a.txt", "m.go"}) {
		t.Fatalf("拖到右边那组：%q", got)
	}
	if d := a.CurrentDoc(); a.Grp != right || d == nil || d.Rel != "src/m.go" || !d.Pinned || !at.Pinned || !a.FocusDoc {
		t.Fatalf("最后一个当前、都是固定标签：组对 %v 当前 %v", a.Grp == right, d)
	}
}

// 拖到终端上贴的：绝对路径按 shell 的规矩引号、空格连起来、末尾一个空格。
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/tmp/a.txt":            "/tmp/a.txt",
		"/tmp/a-b_c@1+2=3:4,5%": "/tmp/a-b_c@1+2=3:4,5%",
		"/tmp/a b":              "'/tmp/a b'",
		"/tmp/$HOME":            "'/tmp/$HOME'",
		"/tmp/it's":             `'/tmp/it'\''s'`,
		"/tmp/\"q\"":            `'/tmp/"q"'`,
		"/tmp/中文":               "'/tmp/中文'",
		"/tmp/*?[x]":            "'/tmp/*?[x]'",
		"":                      "''",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s，要 %s", in, got, want)
		}
	}
	r := &Repo{Root: "/w/my repo"}
	d := TreeDrag{Rows: []TreeRow{{Repo: r}, {Repo: r}}}
	d.Rows[0].Path, d.Rows[1].Path = "src", "a.txt"
	if got, want := d.ShellText(), "'/w/my repo/src' '/w/my repo/a.txt' "; got != want {
		t.Fatalf("ShellText = %q，要 %q", got, want)
	}
}
