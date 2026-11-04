package ui

import (
	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"

	wui "github.com/egoist/mygo/ui"
)

// openEditors 是侧栏文件模式“仓库”头上面的“打开的编辑器”（设置 → 文件树、视图菜单开关，默认关）：
// 头一行和“仓库”同款，左边三角收起（按工作区记），右边全部保存、关闭全部（所有组，app.CloseAllEditors）；下面所有组的标签按标签条的顺序各一行，
// 拆分了每组前面一个“第 N 组”。最多露 app.OpenEditorsMax 行，再多在这一段里滚。不拿键盘、不拖。
func openEditors(a *app.App, c *wui.Context, tools bool) {
	t := c.Theme()
	open := !a.St.OpenEditorsFolded
	rows := a.OpenEditorRows()
	name := i18n.T("打开的编辑器")
	wui.Row(c.Key("head")).Padding(0, 8, 2, 2).Gap(2).AlignItems(wui.Center).Shrink(0).Children(func() {
		// 三角 + 名字一起是收起的开关（三角窄一点，名字和下面“仓库”的左边齐）
		label := i18n.Tf("展开 %s", name)
		if open {
			label = i18n.Tf("收起 %s", name)
		}
		h := wui.Row(c.Key("fold")).Grow(1).MinWidth(0).AlignItems(wui.Center).Cursor(wui.CursorPointer).Label(label)
		h.Children(func() {
			wui.Box(c).Size(12, 16).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Children(func() {
				ic := wui.Icon(c, iconChevronRight).FontSize(iconInline).TextColor(t.TextMuted)
				if open {
					ic.Rotate(90)
				}
			})
			wui.Text(c, name).FontSize(sideSub).FontWeight(600).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
		})
		if h.Clicked() {
			a.ToggleOpenEditorsFolded()
		}
		if !tools {
			wui.Box(c).Size(0, toolSize).Shrink(0)
			return
		}
		toolButton(c, iconSave, app.Tip(i18n.T("全部保存"), "file.saveAll"), a.DirtyDocs() == 0, func() { a.Exec("file.saveAll") })
		toolButton(c, iconCloseAll, i18n.T("关闭全部标签（所有组）"), len(rows) == 0, a.CloseAllEditors)
	})
	if !open || len(rows) == 0 {
		return
	}
	rowH := a.TreeRowH // 和文件树的行一样高（树还没画过的第一帧用估计的）
	if rowH <= 0 {
		rowH = treeRowH
	}
	indent := treeIndent(0)
	if rows[0].Head() { // 拆分了：标签比“第 N 组”再缩一级
		indent = treeIndent(1)
	}
	wui.Scroll(c.Key("rows")).Shrink(0).MaxHeight(rowH*app.OpenEditorsMax+4).PaddingX(4).PaddingY(2).Margin(0, 0, 6, 0).Children(func() {
		for i, r := range rows {
			if r.Head() {
				wui.Row(c.Key(i)).Height(rowH).Padding(0, 8, 0, treeIndent(0)).AlignItems(wui.Center).Children(func() {
					wui.Text(c, i18n.Tf("第 %d 组", r.N)).FontSize(sideSub).TextColor(t.TextMuted).SingleLine()
				})
				continue
			}
			openEditorRow(a, c.Key(r.Doc), r, indent)
		}
	})
}

// openEditorRow 是打开的编辑器里的一个标签：标签同款图标和标题（没固定的斜体），右边灰的仓库 / 目录，
// 再右边和标签一样的一格（悬停 ×，不然有改动是一个点）。有键盘那组的当前标签是选中行（和侧栏列表一样的淡底 + 竖条或实心）。
func openEditorRow(a *app.App, c *wui.Context, r app.OpenEditorRow, indent float32) {
	t := c.Theme()
	d := r.Doc
	cur := a.OpenEditorCurrent(r)
	on := onAccent(a, cur)
	el := wui.Row(c).Padding(3, 4, 3, indent).Gap(treeGap).Radius(rowRadius).AlignItems(wui.Center).
		Cursor(wui.CursorPointer).Label(i18n.Tf("打开的编辑器 %s", d.Title)).ContextMenu(func(m *wui.Menu) { tabMenu(a, m, d) })
	tooltip(c, el, d.Where())
	hover := el.Hovered()
	switch {
	case on:
		el.Background(t.Accent)
	case cur:
		el.Background(t.Accent.Alpha(softAlpha(t)))
	case hover:
		el.Background(toolFill(t, 0.06))
	}
	if el.Clicked() {
		a.PickOpenEditor(r)
	}
	if el.DoubleClicked() {
		d.Pinned = true // 和标签条一样：双击固定
	}
	el.Children(func() {
		if cur && !on {
			wui.Box(c.Key("bar")).Absolute().Left(0).Top(softBarInset).Bottom(softBarInset).Width(2).Radius(1).Background(t.Accent)
		}
		docGlyph(a, c, d, on)
		title := wui.Text(c, d.Title).FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		if !d.Pinned {
			title.Italic()
		}
		if on {
			title.TextColor(t.AccentText)
		}
		if s := a.OpenEditorDetail(d); s != "" {
			muted := t.TextMuted
			if on {
				muted = t.AccentText.Alpha(0.75)
			}
			// 地方不够先缩它
			wui.Text(c, s).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(100).Margin(0, 0, textNudge, 0)
		}
		wui.Spacer(c).Grow(1)
		tabClose(a, c, d, false, hover)
	})
}
