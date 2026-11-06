package app

import (
	"slices"
	"strconv"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/workspace"
)

// 状态栏里能点的语言、换行、缩进：都只管这一个标签。语言、缩进记进会话（Tab.Lang / Tab.Indent），
// 换行是文件内容，保存了就在。界面在 ui/tabs.go 的 docStatus。

// restoreLook 把会话、⇧⌘T 记着的语言和缩进放回文件标签上。
func restoreLook(d *docs.Doc, lang, indent string) {
	if d == nil || d.Kind != docs.File {
		return
	}
	if lang != "" {
		d.SetLang(lang)
	}
	if indent == docs.IndentAuto || slices.Contains(workspace.Indents, indent) {
		d.Indent = indent
	}
}

// DocIndent 是 d 现在用的缩进，写法同设置里的（"" 按语言、"tab"、"2"、"4"、"8"）：这个文件选过的，没选跟设置。
func (a *App) DocIndent(d *docs.Doc) string {
	switch d.Indent {
	case "":
		return a.St.Settings.Indent
	case docs.IndentAuto:
		return ""
	}
	return d.Indent
}

// SetDocLang 是状态栏语言菜单里选的（"" 为按文件名认，docs.PlainLang 为纯文本）。
func (a *App) SetDocLang(d *docs.Doc, name string) {
	d.SetLang(name)
	if d.Kind == docs.File {
		a.relink(d)
		a.SaveState()
	}
}

// SetDocIndent 是状态栏缩进菜单里选的这个文件的缩进（写法同设置里的，"" 为按语言）：和设置一样的不另记。
func (a *App) SetDocIndent(d *docs.Doc, indent string) {
	switch {
	case indent == a.St.Settings.Indent:
		d.Indent = ""
	case indent == "":
		d.Indent = docs.IndentAuto
	default:
		d.Indent = indent
	}
	a.relink(d)
	a.SaveState()
}

// ConvertIndent 把 d 每行开头的缩进按现在的宽度换成空格或 Tab（一步撤销），之后这个文件的缩进也跟着换。
func (a *App) ConvertIndent(d *docs.Doc, toTabs bool) {
	if !d.Editable() {
		return
	}
	size, _ := a.IndentOf(d)
	n := d.ConvertIndent(size, toTabs)
	if toTabs {
		a.SetDocIndent(d, "tab")
	} else {
		a.SetDocIndent(d, strconv.Itoa(size))
	}
	switch {
	case n == 0:
		a.Toast = i18n.T("没有要转的缩进")
	case toTabs:
		a.Toast = i18n.Tn("%d 行的缩进转成了 Tab", n)
	default:
		a.Toast = i18n.Tn("%d 行的缩进转成了空格", n)
	}
}
