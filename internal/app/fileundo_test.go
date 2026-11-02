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

// 撤销文件操作都在临时仓库上测；废纸篓换成 fakeTrash。

// undoApp 是读好文件列表的临时仓库，废纸篓换成测试自己的目录。
func undoApp(t *testing.T) (string, *App, *Repo, func(string, func() bool), func() []string) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	trash, moved := fakeTrash(t)
	a.Trash = trash
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读文件列表", func() bool { return idle(r)() && hasFile(r, "a.txt") })
	return root, a, r, wait, moved
}

func TestUndoRename(t *testing.T) {
	root, a, r, wait, _ := undoApp(t)
	u := a.OpenFile(r, "u.txt", true)
	wait("读好", func() bool { return u.Buf != nil })
	if a.CanUndoFile() || Find("tree.undo").Enabled(a) {
		t.Fatal("还没操作就能撤销")
	}

	a.StartRename(r, "u.txt", false)
	a.Edit.Name = "v.txt"
	if !a.CommitEdit() {
		t.Fatalf("没改成：%q", a.Edit.Err)
	}
	if a.FileUndoLabel() != "重命名 u.txt → v.txt" || !Find("tree.undo").Enabled(a) {
		t.Fatalf("没记下：%q", a.FileUndoLabel())
	}
	// 输入名字时 ⌘Z 归输入框
	a.StartNew(r, "", false)
	if a.CanUndoFile() {
		t.Fatal("输入名字时也能撤销")
	}
	a.CancelEdit()

	a.UndoFile()
	if !exists(filepath.Join(root, "u.txt")) || exists(filepath.Join(root, "v.txt")) {
		t.Fatal("没改回去")
	}
	if u.Rel != "u.txt" || a.Toast != "已撤销：重命名 u.txt → v.txt" || len(a.FileUndo) != 0 {
		t.Fatalf("标签 %q，toast %q，栈 %d", u.Rel, a.Toast, len(a.FileUndo))
	}
	if e := a.Output[len(a.Output)-1]; e.Title != "重命名 v.txt → u.txt" || e.Failed {
		t.Errorf("输出栏：%+v", e)
	}
	wait("树里换回来", func() bool { return idle(r)() && hasFile(r, "u.txt") && !hasFile(r, "v.txt") })

	// 封顶 20 条，丢最老的；移除仓库时那个仓库的条目丢掉
	for i := range 25 {
		a.pushUndo(FileOp{Repo: r, Kind: OpCreate, To: []string{"x"}, Label: string(rune('a' + i))})
	}
	if len(a.FileUndo) != undoCap || a.FileUndo[0].Label != "f" || a.FileUndoLabel() != "y" {
		t.Fatalf("封顶：%d %q", len(a.FileUndo), a.FileUndo[0].Label)
	}
	a.removeRepo(r)
	if len(a.FileUndo) != 0 {
		t.Fatalf("移除仓库后还有 %d 条", len(a.FileUndo))
	}
}

func TestUndoMove(t *testing.T) {
	root, a, r, wait, _ := undoApp(t)
	os.MkdirAll(filepath.Join(root, "dst"), 0o755)
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "m.go"), []byte("package m\n"), 0o644)
	a.LoadFiles(r)
	m := a.OpenFile(r, "src/m.go", true)
	wait("读好", func() bool { return idle(r)() && hasFile(r, "src/m.go") && m.Buf != nil })

	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"u.txt", "src", "README.md"}}, r, "dst")
	a.Confirm.Fn()
	a.ConfirmOpen = false
	if !exists(filepath.Join(root, "dst", "src", "m.go")) || m.Rel != "dst/src/m.go" {
		t.Fatal("没移过去")
	}
	if a.FileUndoLabel() != "移动 3 项 → dst" {
		t.Fatalf("撤销说明：%q", a.FileUndoLabel())
	}

	// 命令面板里的灰字写要撤的是什么
	a.OpenPalette(">撤销上次文件")
	a.UpdatePalette()
	if it := a.Palette.Items; len(it) == 0 || it[0].Detail != "文件 · 移动 3 项 → dst" || it[0].Off {
		t.Fatalf("命令面板：%+v", it)
	}
	a.ClosePalette()

	a.UndoFile()
	for _, p := range []string{"u.txt", "src/m.go", "README.md"} {
		if !exists(filepath.Join(root, p)) {
			t.Errorf("%s 没回来", p)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(root, "dst")); len(ents) != 0 || m.Rel != "src/m.go" {
		t.Fatalf("dst 里还有 %d 项，标签 %q", len(ents), m.Rel)
	}
	if a.Toast != "已撤销：移动 3 项 → dst" {
		t.Errorf("toast %q", a.Toast)
	}

	// 一项的写名字；只拖了一个干净文件也记
	a.MoveTree(TreeDrag{Repo: r, Paths: []string{"u.txt"}}, r, "dst")
	if a.FileUndoLabel() != "移动 u.txt → dst" {
		t.Fatalf("一项：%q", a.FileUndoLabel())
	}
	a.UndoFile()
	if !exists(filepath.Join(root, "u.txt")) {
		t.Fatal("一项没回来")
	}
	wait("树里", func() bool { return idle(r)() && hasFile(r, "u.txt") && hasFile(r, "src/m.go") })
}

func TestUndoCreateEmpty(t *testing.T) {
	root, a, r, wait, moved := undoApp(t)

	// 带子目录新建：撤销时缺的上级目录一起拿走，不问
	a.StartNew(r, "", false)
	a.Edit.Name = "notes/deep/todo.md"
	if !a.CommitEdit() {
		t.Fatalf("没建成：%q", a.Edit.Err)
	}
	if a.FileUndoLabel() != "新建文件 notes/deep/todo.md" || !slices.Equal(a.FileUndo[0].To, []string{"notes"}) {
		t.Fatalf("记的：%+v", a.FileUndo)
	}
	// 新建文件夹
	a.StartNew(r, "", true)
	a.Edit.Name = "empty"
	if !a.CommitEdit() {
		t.Fatalf("文件夹没建成：%q", a.Edit.Err)
	}

	a.UndoFile()
	if a.ConfirmOpen {
		t.Fatalf("空目录也问了：%+v", a.Confirm)
	}
	wait("empty 进废纸篓", func() bool { return len(moved()) == 1 })
	if got := moved(); got[0] != filepath.Join(root, "empty") || a.Toast != "已撤销：新建文件夹 empty" {
		t.Fatalf("废纸篓 %v，toast %q", got, a.Toast)
	}

	a.UndoFile()
	if a.ConfirmOpen {
		t.Fatalf("空文件也问了：%+v", a.Confirm)
	}
	wait("notes 进废纸篓", func() bool { return len(moved()) == 2 && !exists(filepath.Join(root, "notes")) })
	wait("树里没了", func() bool { return idle(r)() && !hasFile(r, "notes/deep/todo.md") })
	if slices.ContainsFunc(a.Docs, func(d *docs.Doc) bool { return d.Rel == "notes/deep/todo.md" }) {
		t.Error("没改过的标签还开着")
	}
}

func TestUndoCreateDirty(t *testing.T) {
	root, a, r, wait, moved := undoApp(t)

	// 复制进来的本来就不空：先问，取消就什么都不动、条目留着
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "z.txt"), []byte("z\n"), 0o644)
	os.WriteFile(filepath.Join(out, "y.txt"), nil, 0o644)
	a.DropFiles([]string{filepath.Join(out, "z.txt"), filepath.Join(out, "y.txt")}, r, "")
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("复制好", func() bool { return idle(r)() && hasFile(r, "z.txt") && len(a.FileUndo) == 1 })
	if a.FileUndoLabel() != "复制 2 项 → 仓库根目录" {
		t.Fatalf("撤销说明：%q", a.FileUndoLabel())
	}
	a.UndoFile()
	if !a.ConfirmOpen || a.Confirm.Title != "撤销“复制 2 项 → 仓库根目录”？" || a.Confirm.Button != "移到废纸篓" ||
		a.Confirm.Message != "撤销新建会把 z.txt、y.txt 移到废纸篓（可以在废纸篓找回）" {
		t.Fatalf("没先问：%+v", a.Confirm)
	}
	a.ConfirmOpen, a.Confirm = false, nil
	if len(a.FileUndo) != 1 || len(moved()) != 0 {
		t.Fatal("取消了还是动了")
	}
	a.UndoFile()
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("进废纸篓", func() bool { return len(moved()) == 2 })
	if exists(filepath.Join(root, "z.txt")) || exists(filepath.Join(root, "y.txt")) || len(a.FileUndo) != 0 {
		t.Fatal("没拿走")
	}

	// 新建后在编辑器里写了没保存：也问（磁盘上是空的，但标签里有字）
	a.StartNew(r, "", false)
	a.Edit.Name = "w.md"
	a.CommitEdit()
	w := a.OpenFile(r, "w.md", true)
	wait("读好", func() bool { return w.Buf != nil })
	w.Buf.Set("写了\n")
	a.UndoFile()
	if !a.ConfirmOpen || !strings.Contains(a.Confirm.Message, "w.md") {
		t.Fatalf("写了没保存的没问：%+v", a.Confirm)
	}
}

func TestUndoStale(t *testing.T) {
	root, a, r, wait, moved := undoApp(t)

	// 重命名之后又有了原来的名字：不撤，说一声，条目丢掉
	a.StartRename(r, "u.txt", false)
	a.Edit.Name = "v.txt"
	a.CommitEdit()
	os.WriteFile(filepath.Join(root, "u.txt"), []byte("新的\n"), 0o644)
	a.UndoFile()
	if a.Toast != "u.txt 已经变了，不能撤销" || len(a.FileUndo) != 0 || !exists(filepath.Join(root, "v.txt")) {
		t.Fatalf("toast %q，栈 %d", a.Toast, len(a.FileUndo))
	}

	// 新建的已经不在了
	a.StartNew(r, "", false)
	a.Edit.Name = "gone.txt"
	a.CommitEdit()
	os.Remove(filepath.Join(root, "gone.txt"))
	a.UndoFile()
	if a.Toast != "gone.txt 已经变了，不能撤销" || len(a.FileUndo) != 0 || a.ConfirmOpen || len(moved()) != 0 {
		t.Fatalf("toast %q，栈 %d", a.Toast, len(a.FileUndo))
	}
	wait("树里", func() bool { return idle(r)() && hasFile(r, "v.txt") })
}
