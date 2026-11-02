package app

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 比较两个文件：文件行右键"选择以进行比较"记下一个，再在别的文件上"与 a.go 比较"，
// 或多选正好两个文件"比较这两个文件"、"与剪贴板比较"。开成只读的 Compare 标签（docs.Compare），
// 读法同 Diff 标签；不记进会话，哪边在仓库里变了跟着重读（syncDocs）。

// CompareItem 是"选择以进行比较"记下的文件。
type CompareItem struct {
	Repo     *Repo
	Rel, Abs string
}

// SelectForCompare 记下 r 里的 rel，等下一次"与已选项目比较"。
func (a *App) SelectForCompare(r *Repo, rel string) {
	a.CompareSel = &CompareItem{Repo: r, Rel: rel, Abs: filepath.Join(r.Root, filepath.FromSlash(rel))}
	a.Toast = i18n.Tf("已选择 %s，在另一个文件上右键“与 %s 比较”", filepath.Base(rel), filepath.Base(rel))
}

// CanCompareWithSel 是 r 里的 rel 能和已选的比较：选过、不是同一个文件。
func (a *App) CanCompareWithSel(r *Repo, rel string) bool {
	s := a.CompareSel
	return s != nil && s.Abs != filepath.Join(r.Root, filepath.FromSlash(rel))
}

// CompareSelName 是已选的文件名（菜单的"与 a.go 比较"），没选为空。
func (a *App) CompareSelName() string {
	if a.CompareSel == nil {
		return ""
	}
	return filepath.Base(a.CompareSel.Abs)
}

// CompareWithSel 打开已选的（左）和 r 里的 rel（右）的比较，清掉已选的。
func (a *App) CompareWithSel(r *Repo, rel string) *docs.Doc {
	if !a.CanCompareWithSel(r, rel) {
		return nil
	}
	left := a.CompareSel.Abs
	a.CompareSel = nil
	return a.CompareFiles(left, filepath.Join(r.Root, filepath.FromSlash(rel)))
}

// CanCompareCurrent 是当前标签是磁盘上的文件、能和已选的比较（命令 diff.compareWithSel）。
func (a *App) CanCompareCurrent() bool {
	d := a.CurrentDoc()
	return a.CompareSel != nil && d != nil && d.Kind == docs.File && d.Abs != "" && d.Abs != a.CompareSel.Abs
}

// CompareCurrentWithSel 打开已选的（左）和当前文件（右）的比较，清掉已选的。
func (a *App) CompareCurrentWithSel() {
	if !a.CanCompareCurrent() {
		return
	}
	left := a.CompareSel.Abs
	a.CompareSel = nil
	a.CompareFiles(left, a.CurrentDoc().Abs)
}

// CanCompareRows 是文件树里选中的正好是两个文件（多选菜单的"比较这两个文件"）。
func CanCompareRows(rows []TreeRow) bool {
	return len(rows) == 2 && !slices.ContainsFunc(rows, func(row TreeRow) bool {
		return row.Root || row.Dir || row.Note != "" || row.Sub != 0
	})
}

// CompareRows 打开选中的两个文件的比较：先选的在左（rows 按树里的顺序，上面的在左）。
func (a *App) CompareRows(rows []TreeRow) *docs.Doc {
	if !CanCompareRows(rows) {
		return nil
	}
	abs := func(row TreeRow) string { return filepath.Join(row.Repo.Root, filepath.FromSlash(row.Path)) }
	return a.CompareFiles(abs(rows[0]), abs(rows[1]))
}

// CompareFiles 打开磁盘上 left ↔ right 的比较标签（固定的）；同样两边的开着就切过去。
func (a *App) CompareFiles(left, right string) *docs.Doc {
	return a.openCompare(docs.NewCompare(filepath.Clean(left), filepath.Clean(right), nil))
}

// CompareClipboard 打开 r 里的 rel ↔ 剪贴板的文字的比较；剪贴板是空的就只提示。
// 已经开着的同一个文件的剪贴板比较换成这次的文字重读。
func (a *App) CompareClipboard(r *Repo, rel string) *docs.Doc {
	clip := ReadClipboard()
	if clip == "" {
		a.Toast = i18n.T("剪贴板是空的")
		return nil
	}
	d := docs.NewCompare(filepath.Join(r.Root, filepath.FromSlash(rel)), "", []byte(clip))
	if old := a.reuse(a.compareKey(d), true); old != nil {
		old.RightText = d.RightText
		a.loadDoc(old)
		return old
	}
	return a.openCompare(d)
}

// openCompare 把比较标签填上左边所在的仓库，开过的切过去，没开过的加上读。
func (a *App) openCompare(d *docs.Doc) *docs.Doc { return a.openCompareAs(d, true) }

// openCompareAs 同 openCompare；pin 为 false 时是预览标签（本地历史的行单击）。
func (a *App) openCompareAs(d *docs.Doc, pin bool) *docs.Doc {
	if old := a.reuse(a.compareKey(d), pin); old != nil {
		return old
	}
	a.addDoc(d, pin)
	a.loadDoc(d)
	return d
}

// compareKey 填好 d 的 Root、Rel（左边在列表里某个仓库下时），返回它的 Key。
func (a *App) compareKey(d *docs.Doc) docs.Key {
	if r, rel := a.repoOfPath(d.Abs); r != nil {
		d.Root, d.Rel = r.Root, rel
	}
	return d.Key()
}

// compareTouches 是比较标签 d 的某一边在仓库 r 下。
func compareTouches(d *docs.Doc, r *Repo) bool {
	for _, p := range []string{d.Abs, d.Right} {
		if rel, err := filepath.Rel(r.Root, p); p != "" && err == nil && !isUp(rel) {
			return true
		}
	}
	return false
}

// CompareLeft 打开比较标签左边的文件（标签栏的"在编辑器中打开"）。
func (a *App) CompareLeft(d *docs.Doc) *docs.Doc {
	if r := a.RepoFor(d.Root); r != nil && d.Rel != "" {
		return a.OpenFile(r, d.Rel, true)
	}
	return a.OpenPath(d.Abs)
}

// isUp 是相对路径 rel 跑到了上一级外面（filepath.Rel 的结果）。
func isUp(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
