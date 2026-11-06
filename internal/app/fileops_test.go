package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 文件操作都在临时仓库上测；废纸篓换成测试自己的目录。

// fakeTrash 把东西挪进测试的临时目录，记下挪了什么。挪是在后台 goroutine 里，测试那边读记录要加锁：
// 文件监视先看到东西没了、wait 先读，和这边 append 之间没有 Go 能认的先后（-race 抓到过）。
func fakeTrash(t *testing.T) (func(string) error, func() []string) {
	dir := t.TempDir()
	var mu sync.Mutex
	var got []string
	return func(p string) error {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
			return os.Rename(p, filepath.Join(dir, filepath.Base(p)))
		}, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(got)
		}
}

func TestNewFileAndFolder(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读文件列表", func() bool { return idle(r)() && hasFile(r, "a.txt") })

	// 名字不行：输入框留着，下面说为什么，什么都没建
	a.StartNew(r, "", false)
	if a.Edit == nil || a.Edit.Kind != EditNewFile {
		t.Fatalf("没开输入框：%+v", a.Edit)
	}
	rows := a.TreeRows()
	if a.FileSel < 0 || !rows[a.FileSel].Edit || rows[a.FileSel-1].Repo != r || !rows[a.FileSel-1].Root {
		t.Fatalf("输入框不在仓库行下面：sel=%d", a.FileSel)
	}
	for name, want := range map[string]string{
		"":          "要有名字",
		"  ":        "要有名字",
		"../x.txt":  "路径里不能有空的、. 或 .. 这样的一段",
		"a//b":      "路径里不能有空的、. 或 .. 这样的一段",
		".git/x":    "不能用 .git 这个名字",
		"a.txt":     "已经有 a.txt 了",
		"README.md": "已经有 README.md 了",
		`a\b`:       `名字里不能有 \ 或 NUL`,
	} {
		a.Edit.Name, a.Edit.Err = name, ""
		if a.CommitEdit() || a.Edit == nil || a.Edit.Err != want {
			t.Errorf("名字 %q：Err = %q，应为 %q", name, a.Edit.Err, want)
		}
	}
	a.CancelEdit()
	if a.Edit != nil || slicesHasEdit(a.TreeRows()) {
		t.Fatal("取消后输入框还在")
	}
	if a.FileSel < 0 || !a.TreeRows()[a.FileSel].Root {
		t.Fatalf("取消后没选回仓库行：%d", a.FileSel)
	}

	// 新建文件，带子目录：上级目录一起建，文件打开在标签里，树里选中它
	a.StartNew(r, "", false)
	a.Edit.Name = "notes/todo.md"
	if !a.CommitEdit() {
		t.Fatalf("没建成：%q", a.Edit.Err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "notes", "todo.md")); err != nil || len(b) != 0 {
		t.Fatalf("新文件 %q %v", b, err)
	}
	d := a.CurrentDoc()
	if d == nil || d.Rel != "notes/todo.md" || !d.Pinned || !a.FocusDoc {
		t.Fatalf("新文件没打开：%+v", d)
	}
	wait("树里有新文件并选中", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return idle(r)() && hasFile(r, "notes/todo.md") && ok && row.Path == "notes/todo.md"
	})
	if r.Status.Untracked != 2 {
		t.Fatalf("未跟踪 %d", r.Status.Untracked)
	}
	if e := a.Output[len(a.Output)-1]; e.Title != "新建文件 notes/todo.md" || e.Failed {
		t.Fatalf("输出栏 %+v", e)
	}

	// 新建文件夹（空的，git ls-files 列不出来）：在选中的目录下面建，树里看得到、展开着
	a.StartNew(r, "notes", true)
	rows = a.TreeRows()
	if !rows[a.FileSel].Edit || rows[a.FileSel-1].Path != "notes" || rows[a.FileSel].Depth != rows[a.FileSel-1].Depth+1 {
		t.Fatalf("输入框不在 notes 下面：%+v", rows[a.FileSel])
	}
	a.Edit.Name = "drafts"
	if !a.CommitEdit() {
		t.Fatalf("没建成：%q", a.Edit.Err)
	}
	if fi, err := os.Stat(filepath.Join(root, "notes", "drafts")); err != nil || !fi.IsDir() {
		t.Fatalf("文件夹 %v", err)
	}
	wait("树里有空文件夹", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return !r.Files.Loading && r.Files.Dir("notes/drafts") != nil && ok && row.Dir && row.Path == "notes/drafts"
	})
	// 外面删了它，再读一遍就不在树里了
	os.Remove(filepath.Join(root, "notes", "drafts"))
	a.LoadFiles(r)
	wait("重读文件列表", func() bool { return !r.Files.Loading })
	if r.Files.Dir("notes/drafts") != nil || r.Files.Extra["notes/drafts"] {
		t.Fatal("删掉的空文件夹还在树里")
	}
}

func slicesHasEdit(rows []TreeRow) bool {
	for _, row := range rows {
		if row.Edit {
			return true
		}
	}
	return false
}

// ⌘N 建在哪：文件树有焦点时按选中的行，否则当前标签的文件所在目录，再不然仓库根。
func TestFileOpTarget(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.EnsureFiles(r)
	r.Expanded = true
	a.tree.ver++
	wait("读文件列表", func() bool { return hasFile(r, "a.txt") })
	if got, dir := a.FileOpTarget(); got != r || dir != "" {
		t.Fatalf("什么都没选：%v %q", got, dir)
	}
	os.MkdirAll(filepath.Join(r.Root, "src", "x"), 0o755)
	os.WriteFile(filepath.Join(r.Root, "src", "x", "m.go"), []byte("package x\n"), 0o644)
	a.LoadFiles(r)
	wait("重读", func() bool { return hasFile(r, "src/x/m.go") })
	a.OpenFile(r, "src/x/m.go", true)
	if _, dir := a.FileOpTarget(); dir != "src/x" {
		t.Fatalf("当前标签的目录：%q", dir)
	}
	a.Reveal(r, "src")
	a.TreeFocused = true
	if _, dir := a.FileOpTarget(); dir != "src/x" {
		t.Fatalf("树里选中的合并行 src / x（键盘按最深那段）：%q", dir)
	}
	a.St.Settings.TreeNoCompact = true
	a.SettingsChanged()
	a.Reveal(r, "src")
	if _, dir := a.FileOpTarget(); dir != "src" {
		t.Fatalf("树里选中的目录：%q", dir)
	}
	a.Reveal(r, "README.md")
	if _, dir := a.FileOpTarget(); dir != "" {
		t.Fatalf("树里选中的顶层文件：%q", dir)
	}
	a.TreeFocused = false
	if _, dir := a.FileOpTarget(); dir != "src/x" {
		t.Fatalf("树没焦点时用当前标签：%q", dir)
	}
}

func TestRename(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "m.go"), []byte("package m\n"), 0o644)
	a.LoadFiles(r)
	d := a.OpenFile(r, "a.txt", true)
	m := a.OpenFile(r, "src/m.go", true)
	wait("读好", func() bool { return idle(r)() && hasFile(r, "src/m.go") && d.Buf != nil && m.Buf != nil })
	d.Buf.Set("没保存的\n" + d.Buf.String())
	r.Files.SetOpen("src", true)

	// 文件：一开始选中扩展名之前那段；改了名，打开着的标签跟着换，没保存的改动留着
	a.StartRename(r, "a.txt", false)
	if a.Edit.Name != "a.txt" || a.Edit.Stem() != 1 {
		t.Fatalf("输入框 %q stem=%d", a.Edit.Name, a.Edit.Stem())
	}
	rows := a.TreeRows()
	if row := rows[a.FileSel]; !row.Edit || row.Path != "a.txt" {
		t.Fatalf("重命名的行 %+v", row)
	}
	a.Edit.Name = "README.md"
	if a.CommitEdit() || a.Edit.Err != "已经有 README.md 了" {
		t.Fatalf("改成已有的名字：%q", a.Edit.Err)
	}
	a.Edit.Name = "sub/b.txt"
	if a.CommitEdit() || a.Edit.Err != "名字里不能有 /" {
		t.Fatalf("名字里带 /：%q", a.Edit.Err)
	}
	a.Edit.Name = "b.md"
	if !a.CommitEdit() {
		t.Fatalf("没改成：%q", a.Edit.Err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("旧文件还在")
	}
	if d.Rel != "b.md" || d.Title != "b.md" || d.Abs != filepath.Join(root, "b.md") || !d.Modified() {
		t.Fatalf("标签没跟上：%+v modified=%v", d.Key(), d.Modified())
	}
	if err := a.WriteDoc(d, true); err != nil || !strings.HasPrefix(mustRead(t, filepath.Join(root, "b.md")), "没保存的\n") {
		t.Fatalf("存到新路径：%v", err)
	}
	wait("树里换成新名字", func() bool { return idle(r)() && hasFile(r, "b.md") && !hasFile(r, "a.txt") })

	// 只改大小写（同一个文件）可以
	a.StartRename(r, "b.md", false)
	a.Edit.Name = "B.md"
	if !a.CommitEdit() {
		t.Fatalf("只改大小写：%q", a.Edit.Err)
	}
	// 没改名字就是取消
	a.StartRename(r, "B.md", false)
	if a.CommitEdit() || a.Edit != nil {
		t.Fatal("没改名字也做了")
	}

	// 目录：里面打开着的标签和展开状态都跟着换
	a.StartRename(r, "src", true)
	if a.Edit.Stem() != 3 {
		t.Fatalf("目录一开始全选：%d", a.Edit.Stem())
	}
	a.Edit.Name = "lib"
	if !a.CommitEdit() {
		t.Fatalf("目录没改成：%q", a.Edit.Err)
	}
	if m.Rel != "lib/m.go" || m.Abs != filepath.Join(root, "lib", "m.go") || !r.Files.Open["lib"] || r.Files.Open["src"] {
		t.Fatalf("目录改名后：%q open=%v", m.Rel, r.Files.Open)
	}
	wait("树里换成 lib", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return idle(r)() && hasFile(r, "lib/m.go") && ok && row.Dir && row.Path == "lib"
	})
}

func TestTrashAsksFirst(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	trash, moved := fakeTrash(t)
	a.Trash = trash
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "x.md"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(root, "docs", "y.md"), []byte("y\n"), 0o644)
	a.LoadFiles(r)
	x := a.OpenFile(r, "docs/x.md", true)
	y := a.OpenFile(r, "docs/y.md", true)
	u := a.OpenFile(r, "u.txt", true)
	wait("读好", func() bool {
		return idle(r)() && hasFile(r, "docs/x.md") && x.Buf != nil && y.Buf != nil && u.Buf != nil
	})
	y.Buf.Set("改了\n" + y.Buf.String())

	// 先问；取消就什么都不做
	a.TrashPath(r, "u.txt", false)
	if !a.ConfirmOpen || a.Confirm.Title != "把文件“u.txt”移到废纸篓？" || a.Confirm.Button != "移到废纸篓" {
		t.Fatalf("没先确认：%+v", a.Confirm)
	}
	a.ConfirmOpen, a.Confirm = false, nil
	if len(moved()) != 0 {
		t.Fatal("没确认就挪了")
	}

	a.TrashPath(r, "u.txt", false)
	a.Confirm.Fn()
	wait("挪走 u.txt", func() bool { return idle(r)() && !hasFile(r, "u.txt") && len(moved()) == 1 })
	if got := moved(); got[0] != filepath.Join(root, "u.txt") || slices.Contains(a.Docs, u) {
		t.Fatalf("废纸篓 %v，标签还在=%v", got, slices.Contains(a.Docs, u))
	}

	// 目录：说明里提到没保存的标签；没改过的关掉，改了的留着
	a.TrashPath(r, "docs", true)
	if !strings.Contains(a.Confirm.Message, "有 1 个打开的标签改了没保存") || a.Confirm.Title != "把文件夹“docs”移到废纸篓？" {
		t.Fatalf("目录的确认：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	wait("挪走 docs", func() bool { return idle(r)() && !hasFile(r, "docs/x.md") && len(moved()) == 2 })
	if slices.Contains(a.Docs, x) || !slices.Contains(a.Docs, y) {
		t.Fatalf("标签：x 在=%v y 在=%v", slices.Contains(a.Docs, x), slices.Contains(a.Docs, y))
	}
	if r.Status.Untracked != 0 {
		t.Fatalf("未跟踪 %d", r.Status.Untracked)
	}

	// 挪不动：弹错误、输出栏记一条失败
	a.Trash = func(string) error { return os.ErrPermission }
	a.TrashPath(r, "README.md", false)
	a.Confirm.Fn()
	wait("失败提示", func() bool { return a.AlertOpen })
	if e := a.Output[len(a.Output)-1]; !e.Failed || e.Title != "移到废纸篓 README.md" {
		t.Fatalf("输出栏 %+v", e)
	}
}
