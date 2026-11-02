package ui

import (
	"path/filepath"
	"strconv"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	lhist "github.com/lemoeade/grove/internal/history" // ui 里已经有 history（右栏）
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是右栏里的本地历史（app/localhist.go）：文件历史里和提交混排的本地行，和仓库外、未跟踪的文件
// "查看本地历史"时的路径模式（只列本地行）。本地行长得像提交行：左边时钟代替圆点，标题是"保存""保存前磁盘上的"……，
// 第二行时间和大小，右端一个淡的"本地"小胶囊代替哈希。单击开只读比较（预览标签），回车 / 双击固定。

// localHistRow 是文件历史里的一条本地历史（rows[i]）。圆点模式下左边一格画时钟，上下两个提交连着就一条竖线穿过去。
func localHistRow(a *app.App, row wui.ListRow, r *app.Repo, rows []app.HistRow, i int) {
	c := row.Context
	h := &r.Hist
	e := h.Local[rows[i].File]
	abs := a.LocalAbs(r)
	sel := onAccent(a, row.Selected())
	muted := c.Theme().TextMuted
	if sel {
		muted = c.Theme().AccentText.Alpha(0.75)
	}
	el := wui.Row(c).ContextMenu(func(m *wui.Menu) { localMenu(a, m, abs, e) })
	if el.RowDoubleClicked() {
		a.OpenLocalCompare(abs, e, true)
	}
	if !a.HistDots(r) {
		el.Padding(6, 8).Gap(8).AlignItems(wui.Center).Children(func() { localBody(c, e, sel, muted, true) })
		return
	}
	el.AlignItems(wui.Stretch).Children(func() {
		localGraph(c, app.LocalPass(r, rows, i), graphWidth(1), sel)
		wui.Row(c).Padding(6, 8, 6, 4).Gap(8).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(func() {
			localBody(c, e, sel, muted, false)
		})
	})
}

// localBody 是本地行的内容：（icon 时）时钟、标题和第二行"5 分钟前 · 1.2 KB"、右端"本地"胶囊。
func localBody(c *wui.Context, e lhist.Entry, sel bool, muted wui.Color, icon bool) {
	if icon {
		wui.Icon(c, iconClock).FontSize(iconRow).TextColor(muted).Shrink(0)
	}
	wui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
		wui.Text(c, app.LocalTitle(e)).FontSize(12.5).FontWeight(500).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(e.From)
		wui.Text(c, app.LocalMeta(e)).FontSize(10.5).TextColor(muted).SingleLine().
			Tooltip(e.Time().Local().Format("2006-01-02 15:04:05"))
	})
	pill(c, i18n.T("本地"), c.Theme().TextMuted, sel).Tooltip(i18n.T("本地历史：保存时留的一份，不是提交"))
}

// localGraph 是圆点模式下本地行的图格：一个小时钟（圈 + 两根针）代替圆点；pass 时上下各一截竖线接到挨着的提交。
func localGraph(c *wui.Context, pass bool, w float32, sel bool) {
	wui.Box(c).Width(w).Shrink(0).Draw(func(p *wui.Painter, r wui.Rect) {
		col := laneColor(p.Theme(), 0, sel)
		x, y := r.X+graphX(0), r.Y+graphNodeY
		const rad = graphRingR - graphStroke/2
		p.Clip(r, 0, func() {
			if pass {
				p.Line(x, r.Y, x, y-graphRingR, graphStroke, col)
				p.Line(x, y+graphRingR, x, r.Y+r.H, graphStroke, col)
			}
			var ring wui.Path
			ring.Circle(x, y, rad)
			p.StrokePath(&ring, graphStroke, col)
			p.Line(x, y, x, y-rad*0.6, graphStroke, col)
			p.Line(x, y, x+rad*0.5, y+rad*0.3, graphStroke, col)
		})
	})
}

// localMenu 是本地行的右键菜单。
func localMenu(a *app.App, m *wui.Menu, abs string, e lhist.Entry) {
	if m.Item(i18n.T("恢复到这一份")).Chosen() {
		a.RestoreLocal(abs, e)
	}
	if m.Item(i18n.T("与当前比较")).Chosen() {
		a.OpenLocalCompare(abs, e, true)
	}
	if m.Item(i18n.T("在编辑器中打开")).Chosen() {
		a.OpenLocalFile(abs, e)
	}
	m.Separator()
	if m.Item(i18n.T("删除这一条")).Chosen() {
		a.DeleteLocal(abs, e)
	}
	if m.Item(i18n.T("清除这个文件的本地历史…")).Chosen() {
		a.ClearLocal(abs)
	}
}

// localPathView 是右栏的本地历史路径模式：胶囊"本地历史 · a.go ×"，下面只有本地行（新的在前）。
func localPathView(a *app.App, c *wui.Context) {
	t := c.Theme()
	abs := a.LocalHistPath
	wui.Row(c).Padding(6, 8, 2, 8).Gap(6).Shrink(0).Children(func() {
		filterPill(c, iconClock, i18n.Tf("本地历史 · %s", filepath.Base(abs)), abs, i18n.T("不再看本地历史"), a.ClearLocalPath)
	})
	es := a.LocalRows
	n := len(es)
	if n == 0 {
		message(c, iconClock, i18n.T("还没有本地历史"), i18n.T("保存过的文件每次保存都会留一份"), t.TextMuted)
		return
	}
	at := func(i int) lhist.Entry { return es[n-1-i] }
	a.LocalList.Key = func(i int) any { return at(i).T }
	a.LocalList.Label = func(i int) string { return app.LocalTitle(at(i)) + " · " + app.LocalMeta(at(i)) }
	a.LocalList.Selected = &a.LocalSel
	l := newListLook(a, c, 0)
	var list wui.Element
	l.build(c, func() {
		list = wui.List(c.Key("local"), &a.LocalList, n).Grow(1).MinHeight(0).PaddingX(6).PaddingY(2).
			Rows(l.rows(func(row wui.ListRow) {
				c := row.Context
				e := at(row.Index)
				sel := onAccent(a, row.Selected())
				muted := c.Theme().TextMuted
				if sel {
					muted = c.Theme().AccentText.Alpha(0.75)
				}
				el := wui.Row(c).Padding(6, 8).Gap(8).AlignItems(wui.Center).ContextMenu(func(m *wui.Menu) { localMenu(a, m, abs, e) })
				if el.RowDoubleClicked() {
					a.OpenLocalCompare(abs, e, true)
				}
				el.Children(func() { localBody(c, e, sel, muted, true) })
			}))
	})
	if i := a.LocalSel; i >= 0 && i < n && (list.Changed() || list.Submitted()) {
		a.OpenLocalCompare(abs, at(i), list.Submitted())
	}
	wui.Text(c, i18n.Tn("%d 条，留 %s 天", n, strconv.Itoa(a.St.Settings.LocalHistoryDays()))).FontSize(11).TextColor(t.TextMuted).
		Padding(6, 14).Shrink(0)
}
