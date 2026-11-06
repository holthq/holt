package app

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是文件树的多选：⌘点加减、⇧点一段、⌘A 全选、拖着选都是列表（FileList.Selection）做的，
// 选中按行的键（FileKey）记，行号变了也跟着项走。动作作用于选中的全部行，跨仓库也行（所有仓库同时可见）；
// 光标（FileSel）是最后点的那行，新建文件放在它的目录。

// FileKey 是文件树一行的键：仓库根、仓库内路径，说明行是它的文字、输入框是 "\x00edit"（新的一行，重新开始）。
type FileKey struct{ Root, Path, Kind string }

// Key 是这一行的键。
func (row TreeRow) Key() FileKey {
	if row.Edit {
		return FileKey{row.Repo.Root, row.Path, "\x00edit"}
	}
	return FileKey{row.Repo.Root, row.Path, row.Note}
}

// TreeYield 记吸顶栈该不该给光标那行让位：光标换到 Sel 那行、那行落进列表（Landed）之后列表滚动过（Scrolled）就不让了。
type TreeYield struct {
	Sel      FileKey
	Y        float32 // 落进列表时列表的偏移
	Landed   bool
	Scrolled bool
}

// OpenAsk 是一次打开这么多个文件以上先问。
const OpenAsk = 20

// ChosenRows 是文件树里选中的行，按显示的顺序；只算看得见的（收起的目录里、筛掉的、移走的不算），
// 说明行和输入框不算。一行都没选（还没点过）就是光标那一行。
func (a *App) ChosenRows() []TreeRow {
	var out []TreeRow
	if a.FileChosen.Len() == 0 {
		if row, ok := a.TreeRowAt(a.FileSel); ok && row.Note == "" && !row.Edit {
			out = append(out, row)
		}
		return out
	}
	for _, row := range a.TreeRows() {
		if row.Note == "" && !row.Edit && a.FileChosen.Has(row.Key()) {
			out = append(out, row)
		}
	}
	return out
}

// MenuRows 是右键 row 弹的菜单作用于哪些行：row 在选中里就是选中的全部，不然只是它（右键不改选中）。
func (a *App) MenuRows(row TreeRow) []TreeRow {
	if a.FileChosen.Has(row.Key()) {
		return a.ChosenRows()
	}
	return []TreeRow{row}
}

// ClearTreeSel 是点了文件树行下面的空白处：一行也不选（光标也去掉），键盘留在树上。
func (a *App) ClearTreeSel() {
	a.FileSel = -1
	a.FileChosen.Clear()
	a.FocusTree = true
	a.treeClick.anchor, a.treeClick.own = FileKey{}, true // 之后的 ⇧点只选那一行
}

// ClearTreeChosen 是文件树里的 Esc：不再多选，只留光标那行。
func (a *App) ClearTreeChosen() {
	if a.FileChosen.Len() > 1 {
		a.chooseOnly(a.FileSel)
	}
}

// chooseOnly 把选中换成第 i 行（应用自己选的：显示文件、新建、重命名之后，键盘 ← →）；-1 清掉。⇧点从它选起。
func (a *App) chooseOnly(i int) {
	a.FileChosen.Clear()
	a.treeClick.anchor, a.treeClick.own = FileKey{}, true
	if row, ok := a.TreeRowAt(i); ok {
		a.FileChosen.Add(row.Key())
		a.treeClick.anchor = row.Key()
	}
}

// OpenRows 把 rows 里的文件都开成固定标签（目录、仓库行跳过），多于 OpenAsk 个先问。
func (a *App) OpenRows(rows []TreeRow) {
	rows = slices.DeleteFunc(slices.Clone(rows), func(row TreeRow) bool { return row.Dir || row.Sub != 0 }) // 子模块不是文件
	open := func() {
		for _, row := range rows {
			a.OpenFile(row.Repo, row.Path, true)
		}
		if len(rows) > 0 {
			a.UseRepo(rows[len(rows)-1].Repo)
		}
	}
	if len(rows) > OpenAsk {
		a.Ask(i18n.Tn("打开 %d 个文件？", len(rows)), i18n.T("每个文件开一个标签。"), i18n.T("打开"), open)
		return
	}
	open()
}

// OpenRowsSide 把 rows 里的文件按顺序都在侧边打开（OpenFileSide）：进同一个侧边的组，最后一个是当前标签；
// 一个标签都没有时就是 OpenRows。多于 OpenAsk 个先问。
func (a *App) OpenRowsSide(rows []TreeRow) {
	rows = slices.DeleteFunc(slices.Clone(rows), func(row TreeRow) bool { return row.Dir || row.Sub != 0 }) // 子模块不是文件
	if len(a.AllDocs()) == 0 {
		a.OpenRows(rows)
		return
	}
	open := func() {
		from, side := a.Grp, a.sideOf(a.Grp)
		for _, row := range rows {
			_, side = a.openSide(row.Repo, row.Path, from, side)
		}
		if len(rows) > 0 {
			a.UseRepo(rows[len(rows)-1].Repo)
		}
	}
	if len(rows) > OpenAsk {
		a.Ask(i18n.Tn("打开 %d 个文件？", len(rows)), i18n.T("每个文件开一个标签。"), i18n.T("打开"), open)
		return
	}
	open()
}

// DiffRows 打开 rows 里有改动的文件的 Diff。
func (a *App) DiffRows(rows []TreeRow) {
	for _, row := range rows {
		if !row.Dir && row.Code != 0 {
			a.ShowDiffFor(row.Repo, row.Path)
		}
	}
}

// Changed 是 rows 里有改动的文件数。
func Changed(rows []TreeRow) int {
	n := 0
	for _, row := range rows {
		if !row.Dir && row.Code != 0 {
			n++
		}
	}
	return n
}

// Files 是 rows 里的文件数（目录、仓库行不算）。
func Files(rows []TreeRow) int {
	n := 0
	for _, row := range rows {
		if !row.Dir {
			n++
		}
	}
	return n
}

// HasRoot 是 rows 里有仓库行。
func HasRoot(rows []TreeRow) bool {
	return slices.ContainsFunc(rows, func(row TreeRow) bool { return row.Root })
}

// DirLink 是这一行是指向目录、画成目录行的符号链接（files.Tree.DirLink）：它自己是个文件（重命名、删掉、
// 复制的都是链接），里面的行 Linked。
func (row TreeRow) DirLink() bool {
	return row.Dir && !row.Root && row.Repo != nil && row.Repo.Files.DirLink(row.Path)
}

// HasLinked 是 rows 里有没有在链接目录里面的行（files.Row.Linked）：它们只能打开、复制路径、在访达中显示。
func HasLinked(rows []TreeRow) bool {
	return slices.ContainsFunc(rows, func(row TreeRow) bool { return row.Linked })
}

// linkedToast 是对链接目录里面的行做了写磁盘的事（重命名、删掉、创建副本…）时说的。
func (a *App) linkedToast() {
	a.Toast = i18n.T("链接目录里面的只能打开、复制路径、在访达中显示")
}

// FullPath 是这一行在磁盘上的路径（仓库行是仓库根）。
func (row TreeRow) FullPath() string {
	return filepath.Join(row.Repo.Root, filepath.FromSlash(row.Path))
}

// RevealRows 在访达里显示 rows：每个目录只显示其中第一项（每显示一次访达就换一次选中，多了只是一串窗口）。
func (a *App) RevealRows(rows []TreeRow) {
	seen := map[string]bool{}
	for _, row := range rows {
		p := row.FullPath()
		if dir := filepath.Dir(p); !seen[dir] {
			seen[dir] = true
			a.InFinder(p)
		}
	}
}

// RowPaths 是 rows 的路径，一行一个：完整路径，或仓库内路径（rel；仓库行是 "."）。
func RowPaths(rows []TreeRow, rel bool) string {
	lines := make([]string, len(rows))
	for i, row := range rows {
		switch {
		case !rel:
			lines[i] = row.FullPath()
		case row.Root:
			lines[i] = "."
		default:
			lines[i] = row.Path
		}
	}
	return strings.Join(lines, "\n")
}

// RenameChosen 是 F2：恰好选了一个文件或目录时重命名它。
func (a *App) RenameChosen() {
	rows := a.ChosenRows()
	switch {
	case len(rows) != 1 || a.Edit != nil || rows[0].Root:
	case rows[0].Linked:
		a.linkedToast()
	default:
		a.StartRename(rows[0].Repo, rows[0].Path, rows[0].Dir && !rows[0].DirLink())
	}
}

// TrashChosen 是 ⌘⌫：把选中的都移到废纸篓（先确认）；选中里有仓库行就不做。
func (a *App) TrashChosen() {
	if a.Edit == nil {
		a.TrashRows(a.ChosenRows())
	}
}

// TrashRows 把 rows 移到废纸篓：一项时照旧问"把文件“x”移到废纸篓？"，多项时只问一次、列出前 5 个名字。
// 选了目录又选了它里面的东西，只移目录。有仓库行就不做（菜单里是灰的）；有链接目录里面的行也不做（说一声）。
// 指向目录的链接移走的是链接自己。
func (a *App) TrashRows(rows []TreeRow) {
	if len(rows) == 0 || HasRoot(rows) {
		return
	}
	if HasLinked(rows) {
		a.linkedToast()
		return
	}
	if len(rows) == 1 {
		a.TrashPath(rows[0].Repo, rows[0].Path, rows[0].Dir && !rows[0].DirLink())
		return
	}
	rows = outermost(rows)
	names, dirty := make([]string, 0, 6), 0
	for i, row := range rows {
		if i < 5 {
			names = append(names, "  "+path.Base(row.Path))
		}
		dirty += a.dirtyUnder(row.Repo, row.Path)
	}
	if len(rows) > 5 {
		names = append(names, "  "+i18n.Tf("…还有 %d 项", len(rows)-5))
	}
	msg := strings.Join(names, "\n") + "\n" + i18n.T("可以在废纸篓里找回。")
	if dirty > 0 {
		msg += i18n.Tn("\n有 %d 个打开的标签改了没保存，它们会留着。", dirty)
	}
	a.Ask(i18n.Tn("把 %d 项移到废纸篓？", len(rows)), msg, i18n.T("移到废纸篓"), func() {
		items := make([]trashItem, len(rows))
		for i, row := range rows {
			items[i] = trashItem{row.Repo, row.Path}
		}
		a.trashAll(items)
	})
}

// outermost 去掉在别的选中目录里面的行（同一个仓库），顺序不变。
func outermost(rows []TreeRow) []TreeRow {
	return slices.DeleteFunc(slices.Clone(rows), func(row TreeRow) bool {
		return slices.ContainsFunc(rows, func(d TreeRow) bool {
			return d.Dir && d.Repo == row.Repo && d.Path != row.Path && files.Under(row.Path, d.Path)
		})
	})
}
