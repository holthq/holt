package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 临时仓库上走一遍文件树里的文件操作：⌘N 新建文件（输入框在树里、回车建、打开、焦点给编辑器）、
// 名字不行时红字提示、Esc 取消、右键新建文件夹、F2 重命名（先选中扩展名之前那段）、
// ⌘⌫ 先确认再移到废纸篓（废纸篓换成测试目录）。
func TestTreeFileOps(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	trashDir := t.TempDir()
	var trashed []string
	a.Trash = func(p string) error {
		trashed = append(trashed, p)
		return os.Rename(p, filepath.Join(trashDir, filepath.Base(p)))
	}
	r := a.Repos[0]
	a.Exec("view.files")
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return idle(r)() && r.Files.Loaded && tt.HasText("a.txt") })

	// ⌘N：树里出来一个输入框，焦点在它上面
	tt.Key(wui.Cmd, wui.KeyN)
	wait("输入框", func() bool { return tt.Focused("新文件名") })
	if a.Edit == nil || a.Edit.Dir != "" {
		t.Fatalf("新建的位置 %+v", a.Edit)
	}
	tt.Type("a.txt")
	tt.Key(0, wui.KeyEnter)
	wait("重名提示", func() bool { return tt.HasText("已经有 a.txt 了") })
	if !tt.Focused("新文件名") {
		t.Fatal("名字不行时输入框不该丢焦点")
	}
	tt.Key(0, wui.KeyEscape)
	wait("Esc 取消", func() bool { return a.Edit == nil && !tt.HasText("已经有 a.txt 了") })
	if _, ok := tt.Find("新文件名"); ok {
		t.Fatal("取消后输入框还在")
	}

	tt.Key(wui.Cmd, wui.KeyN)
	wait("输入框", func() bool { return tt.Focused("新文件名") })
	tt.Type("notes/todo.md")
	tt.Key(0, wui.KeyEnter)
	wait("建好并打开", func() bool {
		d := a.CurrentDoc()
		return d != nil && d.Rel == "notes/todo.md" && d.Buf != nil && tt.Focused("编辑 todo.md") && tt.HasText("todo.md")
	})
	if _, err := os.Stat(filepath.Join(root, "notes", "todo.md")); err != nil {
		t.Fatal(err)
	}

	// 右键 notes → 新建文件夹…
	wait("树里有 notes", func() bool { return !r.Files.Loading && tt.HasText("notes") })
	mustRightClickRow(t, tt, "notes", "新建文件夹…")
	wait("文件夹输入框", func() bool { return tt.Focused("新文件夹名") })
	tt.Type("drafts")
	tt.Key(0, wui.KeyEnter)
	wait("文件夹建好、在树里选中", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return ok && row.Path == "notes/drafts" && tt.HasText("drafts")
	})
	if fi, err := os.Stat(filepath.Join(root, "notes", "drafts")); err != nil || !fi.IsDir() {
		t.Fatal("文件夹没建", err)
	}

	// 选中 u.txt，F2：输入框里先选中 "u"，打字换掉它
	mustClickRow(t, tt, "u.txt")
	wait("选中 u.txt", func() bool { row, ok := a.TreeRowAt(a.FileSel); return ok && row.Path == "u.txt" })
	tt.Key(0, wui.KeyF2)
	wait("重命名输入框", func() bool { return tt.Focused("新名字") })
	tt.Type("v")
	tt.Key(0, wui.KeyEnter)
	wait("改名", func() bool { return a.Edit == nil && hasFile(r, "v.txt") && !hasFile(r, "u.txt") })
	if _, err := os.Stat(filepath.Join(root, "v.txt")); err != nil {
		t.Fatal(err)
	}
	wait("树里选中 v.txt、焦点回到树", func() bool {
		row, ok := a.TreeRowAt(a.FileSel)
		return ok && row.Path == "v.txt" && a.TreeFocused
	})

	// ⌘⌫：先确认，点了"移到废纸篓"才挪
	tt.Key(wui.Cmd, wui.KeyBackspace)
	wait("确认框", func() bool { return tt.HasText("把文件“v.txt”移到废纸篓？") })
	if len(trashed) != 0 {
		t.Fatal("没确认就挪了")
	}
	mustClickRow(t, tt, "移到废纸篓")
	wait("挪走", func() bool { return len(trashed) == 1 && !hasFile(r, "v.txt") })
	if !slices.Contains(trashed, filepath.Join(root, "v.txt")) {
		t.Fatalf("挪的是 %v", trashed)
	}
}

func hasFile(r *app.Repo, rel string) bool {
	return r.Files.Loaded && !r.Files.Loading && r.Files.Has(rel)
}
