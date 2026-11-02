package app

import (
	"path"
	"path/filepath"
	"slices"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 打开的编辑器（gaps 1.4）：侧栏文件模式“仓库”头上面一段，所有组的标签各一行（ui/openeditors.go）。
// 默认关（设置 → 文件树“显示打开的编辑器”、视图菜单），折叠记在工作区的布局里（Panels.OpenEditorsFolded）。

// OpenEditorsMax 是这一段最多露出几行，再多在段里滚。
const OpenEditorsMax = 9

// OpenEditorRow 是打开的编辑器里的一行：拆分了时每组前面一个小标题（Doc 为 nil，N 是第几组，1 起），然后是这组的标签（I 是在组里的下标）。
type OpenEditorRow struct {
	G   *Group
	N   int
	Doc *docs.Doc
	I   int
}

// Head 是不是组的小标题。
func (r OpenEditorRow) Head() bool { return r.Doc == nil }

// OpenEditorRows 是打开的编辑器的行：组按从左到右、从上到下，组里按标签条的顺序；只有一组时不出小标题。
func (a *App) OpenEditorRows() []OpenEditorRow {
	gs := a.Groups()
	var out []OpenEditorRow
	for n, g := range gs {
		ds, _ := a.GroupDocs(g)
		if len(gs) > 1 {
			out = append(out, OpenEditorRow{G: g, N: n + 1})
		}
		for i, d := range ds {
			out = append(out, OpenEditorRow{G: g, N: n + 1, Doc: d, I: i})
		}
	}
	return out
}

// OpenEditorCurrent 是不是有键盘那组的当前标签（这一行画成选中的）；别的组的当前标签不另标。
func (a *App) OpenEditorCurrent(r OpenEditorRow) bool {
	return r.Doc != nil && r.G == a.Grp && r.I == a.DocSel
}

// PickOpenEditor 是点了打开的编辑器的一行：换到那组、那个标签，键盘给编辑器（这一段不拿键盘）。
func (a *App) PickOpenEditor(r OpenEditorRow) {
	if r.Doc == nil {
		return
	}
	ds, _ := a.GroupDocs(r.G)
	if r.I >= len(ds) || ds[r.I] != r.Doc {
		return // 这一帧里组变了（关掉、拖走），行是旧的
	}
	a.SelectIn(r.G, r.I)
	a.FocusDoc = true
}

// OpenEditorDetail 是一行右边的灰字：仓库名 / 所在目录（仓库根下的只有仓库名；提交里的带上短哈希），
// 仓库外的文件是 ~ 开头的目录；终端、搜索、设置这类没有。
func (a *App) OpenEditorDetail(d *docs.Doc) string {
	switch d.Kind {
	case docs.Term, docs.TermText, docs.SearchDoc, docs.SettingsDoc, docs.Rebase, docs.Compare:
		return ""
	}
	if d.Rel == "" {
		if d.Abs == "" {
			return ""
		}
		return tildePath(filepath.Dir(d.Abs))
	}
	s := ""
	if r := a.RepoOf(d); r != nil {
		s = r.Name
	}
	if dir := path.Dir(d.Rel); dir != "." {
		if s != "" {
			s += "/"
		}
		s += dir
	}
	if (d.Kind == docs.Version || d.Kind == docs.Diff) && d.Commit != "" {
		s += " @ " + git.Short(d.Commit)
	}
	return s
}

// CloseAllEditors 是打开的编辑器头上的“关闭全部”：所有组的标签照“关闭全部标签”的规矩关（那个命令 ⇧⌘W 只关有键盘的那组），
// 改过的、终端还在跑的留着，合起来提示一次。
func (a *App) CloseAllEditors() {
	left, busy := 0, 0
	for _, d := range slices.Clone(a.AllDocs()) {
		switch {
		case d.Modified():
			left++
		case a.termBusy(d):
			busy++
		default:
			a.RemoveDoc(d)
		}
	}
	msg := ""
	if left > 0 {
		msg = i18n.Tn("%d 个没保存的标签留着了", left)
	}
	if msg = busyToast(msg, busy); msg != "" {
		a.Toast = msg
	}
}

// ToggleOpenEditors 开关打开的编辑器（视图菜单、设置 → 文件树）；打开时侧栏切到文件，看得见它。
func (a *App) ToggleOpenEditors() {
	a.St.Settings.ShowOpenEditors = !a.St.Settings.ShowOpenEditors
	if a.St.Settings.ShowOpenEditors {
		a.ShowSidebar(workspace.ModeFiles)
	}
	a.SettingsChanged()
}

// ToggleOpenEditorsFolded 收起 / 展开这一段（头一行的三角），按工作区记。
func (a *App) ToggleOpenEditorsFolded() {
	a.St.OpenEditorsFolded = !a.St.OpenEditorsFolded
	a.SaveState()
}
