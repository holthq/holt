package app

import (
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/i18n"
)

// 文件树的撤销：重命名 / 移动改回去，新建 / 复制进来的移到废纸篓。移到废纸篓的不记（放回要靠访达），
// 没有重做。换工作区是新的 App，栈跟着没了；移除仓库丢掉那个仓库的条目。

// FileOpKind 是 FileOp 记的哪种操作。
type FileOpKind int

const (
	OpRename FileOpKind = iota // From[i] 改成了 To[i]（重命名、拖动移动）
	OpCreate                   // To 是新建 / 复制进来的（新建、粘贴、创建副本、从访达或按着 ⌥ 拖进来复制的）
)

// FileOp 是 FileUndo 里的一条。
type FileOp struct {
	Repo     *Repo
	Kind     FileOpKind
	From, To []string // 仓库内路径；OpRename 一一对应，OpCreate 只用 To（新建时缺的上级目录一起建的，记最上面那个）
	Label    string   // 说给用户听的："重命名 a → b"、"新建文件 notes/todo.md"
}

// undoCap 是 FileUndo 最多记几条，多了丢最老的。
const undoCap = 20

// pushUndo 记一条能撤销的文件操作（To 是空的不记：一个都没做成）。
func (a *App) pushUndo(op FileOp) {
	if len(op.To) == 0 {
		return
	}
	a.FileUndo = append(a.FileUndo, op)
	if n := len(a.FileUndo) - undoCap; n > 0 {
		a.FileUndo = slices.Delete(a.FileUndo, 0, n)
	}
}

// dropUndo 丢掉仓库 r 的条目（移除仓库时）。
func (a *App) dropUndo(r *Repo) {
	a.FileUndo = slices.DeleteFunc(a.FileUndo, func(op FileOp) bool { return op.Repo == r })
}

// CanUndoFile 是有能撤销的、也没在输入名字（输入名字时 ⌘Z 归输入框）。
func (a *App) CanUndoFile() bool { return a.Edit == nil && len(a.FileUndo) > 0 }

// FileUndoLabel 是下一次撤销要撤的（命令面板的灰字、右键菜单），没有为空。
func (a *App) FileUndoLabel() string {
	if len(a.FileUndo) == 0 {
		return ""
	}
	return a.FileUndo[len(a.FileUndo)-1].Label
}

// UndoFile 撤销最近一次文件操作。先看磁盘还是不是做完时的样子，不是就说一声、这条丢掉；
// 重命名 / 移动直接改回去，新建的空文件 / 空目录直接移到废纸篓，不空的（写过字、复制来的）先问。
func (a *App) UndoFile() {
	if !a.CanUndoFile() {
		return
	}
	op := a.FileUndo[len(a.FileUndo)-1]
	pop := func() { a.FileUndo = a.FileUndo[:len(a.FileUndo)-1] }
	if p := a.undoStale(op); p != "" {
		pop()
		a.Toast = i18n.Tf("%s 已经变了，不能撤销", path.Base(p))
		return
	}
	r := op.Repo
	switch op.Kind {
	case OpRename:
		pop()
		var back []string
		for i := len(op.To) - 1; i >= 0; i-- { // 倒着改回去
			if err := a.rename(r, op.To[i], op.From[i]); err != nil {
				a.Log(r, i18n.Tf("撤销：%s", op.Label), err.Error(), true)
				a.Fail(i18n.T("没能撤销"), err)
				break
			}
			back = append(back, op.From[i])
		}
		slices.Reverse(back)
		if len(back) == len(op.To) {
			a.Toast = i18n.Tf("已撤销：%s", op.Label)
		}
		if len(back) > 0 {
			a.landed(r, files.DirOf(back[0]), back)
		}
	case OpCreate:
		items := make([]trashItem, len(op.To))
		empty := true
		for i, p := range op.To {
			items[i] = trashItem{r, p}
			empty = empty && a.dirtyUnder(r, p) == 0 && emptyTree(filepath.Join(r.Root, filepath.FromSlash(p)))
		}
		run := func() {
			pop()
			a.trashAll(items)
			a.Toast = i18n.Tf("已撤销：%s", op.Label)
		}
		if empty {
			run()
			return
		}
		names := make([]string, 0, 6)
		for i, p := range op.To {
			if i == 5 {
				names = append(names, i18n.Tf("…还有 %d 个", len(op.To)-5))
				break
			}
			names = append(names, path.Base(p))
		}
		a.Ask(i18n.Tf("撤销“%s”？", op.Label), i18n.Tf("撤销新建会把 %s 移到废纸篓（可以在废纸篓找回）", i18n.List(names)), i18n.T("移到废纸篓"), run)
	}
}

// undoStale 看 op 做完之后磁盘有没有变：重命名的 To 都还在、From 都不在（只改大小写的是同一个），新建的都还在。
// 变了的返回第一个对不上的路径，没变返回空。
func (a *App) undoStale(op FileOp) string {
	root := op.Repo.Root
	abs := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	for i, to := range op.To {
		fi, err := os.Lstat(abs(to))
		if err != nil {
			return to
		}
		if op.Kind == OpRename {
			if old, err := os.Lstat(abs(op.From[i])); err == nil && !os.SameFile(old, fi) {
				return op.From[i]
			}
		}
	}
	return ""
}

// emptyTree 是 p 是空文件、空目录，或者目录里只有空文件和空目录（最多看 1000 项，多了算不空）。
func emptyTree(p string) bool {
	n := 0
	var walk func(string) bool
	walk = func(p string) bool {
		if n++; n > 1000 {
			return false
		}
		fi, err := os.Lstat(p)
		switch {
		case err != nil:
			return false
		case fi.Mode().IsRegular():
			return fi.Size() == 0
		case !fi.IsDir():
			return false // 符号链接等
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			return false
		}
		for _, e := range ents {
			if !walk(filepath.Join(p, e.Name())) {
				return false
			}
		}
		return true
	}
	return walk(p)
}

// topNew 是新建 rel 时会建出来的最上面一层：rel 的上级目录里最上面一个还不在的，都在就是 rel 自己。
func topNew(root, rel string) string {
	top := rel
	for d := files.DirOf(rel); d != ""; d = files.DirOf(d) {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(d))); err == nil {
			break
		}
		top = d
	}
	return top
}

// moveLabel / copyLabel 是移动 / 复制进 dir 的撤销说明：一项写名字，多项写几项。
func moveLabel(srcs []string, dir string) string {
	if len(srcs) == 1 {
		return i18n.Tf("移动 %s → %s", path.Base(srcs[0]), placeName(dir))
	}
	return i18n.Tn("移动 %d 项 → %s", len(srcs), placeName(dir))
}

func copyLabel(srcs []string, dir string) string {
	if len(srcs) == 1 {
		return i18n.Tf("复制 %s → %s", filepath.Base(srcs[0]), placeName(dir))
	}
	return i18n.Tn("复制 %d 项 → %s", len(srcs), placeName(dir))
}
