package ui

import (
	"strconv"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// 问题：检查命令（app/lint.go）报的问题。底栏第三页列所有仓库的，按 仓库 → 文件 → 行 排；
// 状态栏左边一个计数，点开这一页；编辑器里的波浪线、悬停、滚动条标记是 mygo 编辑器自带的诊断（app.SyncProblems 交过去）。

// problemsTabLabel 是底栏标签上的字：“问题 N”，N 是错误 + 警告（提示不算）；没有为“问题”。
func problemsTabLabel(a *app.App) string {
	_, errs, warns := a.ProblemList()
	if n := errs + warns; n > 0 {
		return i18n.Tf("问题 %d", n)
	}
	return i18n.T("问题")
}

// problemsHeader 是在问题页时标题行右边的：在跑时一句“正在检查 go…”，重新检查当前文件、清空。
func problemsHeader(a *app.App, c *wui.Context) {
	t := c.Theme()
	if run := a.LintRunning(); len(run) > 0 {
		wui.Text(c, i18n.Tf("正在检查 %s…", i18n.List(run))).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").
			MinWidth(0).Shrink(1).Margin(0, 6, 0, 0)
	}
	cur := a.CurrentDoc()
	toolButton(c, iconRefresh, app.Tip(i18n.T("重新检查当前文件"), "lint.run"), !app.CanLint(cur) || a.Linting(cur), func() { a.Exec("lint.run") })
	if a.HasProblems() {
		iconButton(c, iconTrash, i18n.T("清空"), a.ClearProblems)
	}
}

// sevIcon 是问题严重程度的图标和颜色：错误红叉、警告黄三角（圆里的感叹号）、提示灰 i。
func sevIcon(t *wui.Theme, s editor.Severity) (*wui.SVG, wui.Color) {
	switch s {
	case editor.SeverityError:
		return iconCircleX, t.Danger
	case editor.SeverityWarning:
		return iconAlert, t.Warning
	}
	return iconInfo, t.TextMuted
}

// problems 是底栏的问题页：一条一行，图标、位置（灰）、信息、命令名（右边，灰）。单击开到那一行（预览标签），双击 / 回车固定。
func problems(a *app.App, c *wui.Context) {
	t := c.Theme()
	list, _, _ := a.ProblemList()
	n := len(list)
	if n == 0 {
		sub := app.KeyHint("lint.run", "开着“保存时检查”时保存后列在这里，或按 %s 检查当前文件", "开着“保存时检查”（设置 → 格式化）时保存后列在这里，或右键编辑器“检查”")
		message(c, iconCheck, i18n.T("没有问题"), sub, t.TextMuted)
		return
	}
	a.ProbList.Key = func(i int) any { return list[i] }
	a.ProbList.Label = func(i int) string { return a.ProblemPlace(list[i]) + " " + list[i].Msg }
	a.ProbList.Selected = &a.ProbSel
	l := newListLook(a, c, 0)
	var el wui.Element
	l.build(c, func() {
		el = wui.List(c.Key("problems"), &a.ProbList, n).Fill().PaddingX(4).PaddingY(4).Rows(l.rows(func(row wui.ListRow) {
			q := list[row.Index]
			c := row.Context
			sel := onAccent(a, row.Selected())
			muted, fg := c.Theme().TextMuted, c.Theme().Text
			if sel {
				muted, fg = c.Theme().AccentText.Alpha(0.75), c.Theme().AccentText
			}
			ic, col := sevIcon(t, q.Sev)
			if sel {
				col = fg
			}
			r := wui.Row(c).Padding(3, 8).Gap(8).AlignItems(wui.Center).Tooltip(q.Msg)
			if r.RowDoubleClicked() {
				a.OpenProblem(q, true)
			}
			r.Children(func() {
				wui.Icon(c, ic).FontSize(iconRow).TextColor(col).Shrink(0)
				wui.Text(c, a.ProblemPlace(q)).FontSize(11).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(0).MaxWidth(360)
				wui.Text(c, q.Msg).FontSize(12).TextColor(fg).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).Basis(0)
				if q.Source != "" {
					wui.Text(c, q.Source).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0)
				}
			})
		}))
	})
	if i := a.ProbSel; i >= 0 && i < n && (el.Changed() || el.Submitted()) {
		a.OpenProblem(list[i], el.Submitted())
	}
}

// problemStatus 是状态栏左边的问题计数（有错误或警告才有）：错误时红、只有警告时黄，点开问题面板。
func problemStatus(a *app.App, c *wui.Context) {
	_, errs, warns := a.ProblemList()
	n := errs + warns
	if n == 0 {
		return
	}
	t := c.Theme()
	col := t.Warning
	if errs > 0 {
		col = t.Danger
	}
	tip := app.Tip(i18n.Tn("%d 个问题 · 点开问题面板", n), "view.problems")
	x := wui.Row(c).Gap(3).AlignItems(wui.Center).Shrink(0).Label(i18n.Tn("%d 个问题", n)).Tooltip(tip)
	x.Children(func() {
		wui.Icon(c, iconAlert).FontSize(iconInline).TextColor(col)
		wui.Text(c, strconv.Itoa(n)).FontSize(10.5).FontWeight(500).TextColor(col).SingleLine()
	})
	if x.Hovered() {
		x.Opacity(0.8)
	}
	if x.Clicked() {
		a.Exec("view.problems")
	}
}
