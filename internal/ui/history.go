package ui

import (
	"slices"
	"strconv"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// rightBar 是右栏：提交历史，头部 当前仓库 | 所有仓库 两段切换（记进 state.json），下面一排是搜索框和
// "所有分支"开关，文件历史时再多一个写着文件名的胶囊。"当前仓库"第一行固定是"未提交的改动"（选中它侧栏
// 切到 Git 栏），然后一行一个提交；"所有仓库"先是有改动的仓库各一行，再是各仓库的提交按时间合并、每行带
// 仓库胶囊。选中的提交在下面展开详情和它改的文件，点文件在主区开 Diff 标签；到底了可以再读一页。
func rightBar(a *app.App, c *wui.Context) {
	r := a.Current()
	card(c, wui.Column(c).Width(a.St.RightW).Shrink(0), func() {
		t := c.Theme()
		// 头一行是标题和右对齐的摘要（读了几条），Claude 桌面端卡片头那样；当前仓库 | 所有仓库 在下面一行。
		cardHeader(c).Padding(0, 2, 0, 12).Gap(8).Children(func() {
			wui.Icon(c, iconHistory).FontSize(iconRow).TextColor(t.TextMuted)
			wui.Text(c, i18n.T("历史")).FontSize(12).FontWeight(600).Shrink(0)
			wui.Spacer(c).Grow(1)
			if a.St.HistAll && a.AllHistLoading() || !a.St.HistAll && r != nil && (r.Hist.Loading || r.Hist.FilesLoading) {
				wui.Spinner(c)
			}
			if n := histCount(a, r); n != "" {
				wui.Text(c, i18n.Tf("%s 条", n)).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0)
			}
			iconButton(c, iconClose, app.Tip(i18n.T("关闭右栏"), "view.right"), func() { a.Exec("view.right") })
		})
		wui.Row(c).Padding(6, 8, 0, 8).Shrink(0).Children(func() {
			mode := 0
			if a.St.HistAll {
				mode = 1
			}
			seg := segTabs(c, &mode, seg{nil, i18n.T("当前仓库"), i18n.T("只看当前仓库的历史")},
				seg{nil, i18n.T("所有仓库"), i18n.T("所有仓库的提交按时间合并")}).Grow(1)
			if seg.Changed() {
				a.SetHistAll(mode == 1)
			}
		})
		if r != nil && a.LocalHistPath == "" {
			histFilter(a, c, r)
		}
		switch {
		case a.LocalHistPath != "": // 本地历史的路径模式盖过下面几条提示
			localPathView(a, c)
		case r == nil && a.WsPath == "":
			message(c, iconHistory, i18n.T("没有打开的工作区"), "", t.TextMuted)
		case r == nil:
			message(c, iconHistory, i18n.T("没有仓库"), "", t.TextMuted)
		case r.Plain && !a.St.HistAll:
			message(c, iconFolder, i18n.T("这个文件夹不是 Git 仓库"), "", t.TextMuted)
		case a.St.HistAll:
			allHistory(a, c.Key("all"))
		default:
			historyInfo(c, r)
			history(a, c.Key("one"), r)
		}
	})
}

// histFilter 是右栏头部下面的搜索框、"所有分支"开关，和看工作树 / 文件历史时的胶囊（× 去掉）。
func histFilter(a *app.App, c *wui.Context, r *app.Repo) {
	wui.Row(c).Padding(6, 8, 2, 8).Gap(6).AlignItems(wui.Center).Shrink(0).Children(func() {
		f := wui.SearchField(c.Key("histq"), &a.HistQuery).Label(i18n.T("搜索历史")).Placeholder(i18n.T("提交信息、作者或哈希；author: after: before: path:")).
			Tooltip(i18n.T("在历史里找提交信息或作者里有这些字的提交，或哈希以它开头的提交")).FontSize(12).Grow(1).MinWidth(0)
		if f.Changed() {
			a.HistQueryChanged()
		}
		if f.Submitted() {
			a.ReloadHistories()
		}
		wui.Icon(c, iconQuestion).FontSize(iconRow).TextColor(c.Theme().TextMuted).Shrink(0).Tooltip(histSyntaxTip())
		on := a.St.HistBranches
		if filterToggle(c, "branches", &on, i18n.T("所有分支"), i18n.T("所有分支：也列出其他本地分支、远程分支和标签上的提交（否则只当前分支）"),
			func(fg wui.Color) { wui.Icon(c, iconBranch).FontSize(iconRow).TextColor(fg) }).Changed() {
			a.ToggleHistBranches()
		}
		if r.Plain && !a.St.HistAll {
			return // 普通文件夹没有提交图
		}
		graphOn := !a.St.HistNoGraph
		if filterToggle(c, "graph", &graphOn, i18n.T("提交图"), i18n.T("提交图：在历史左边画分支和合并的连线（搜索、看文件历史时每个提交只画一个点，在完整历史里是父子的相邻两行才连起来）"),
			func(fg wui.Color) { wui.Icon(c, iconGraph).FontSize(iconRow).TextColor(fg) }).Changed() {
			a.ToggleHistGraph()
		}
	})
	if msg := a.HistQueryErr(); msg != "" {
		wui.Text(c, msg).FontSize(11).TextColor(c.Theme().Danger).Padding(0, 10, 2, 10).Shrink(0).SingleLine().Ellipsis("…").Tooltip(msg)
	}
	if (r.Hist.Path == "" && r.Hist.RefKind == app.RefNone && r.Hist.Compare == nil) || a.St.HistAll {
		return
	}
	wui.Row(c).Padding(2, 8, 2, 8).Gap(6).Shrink(0).Children(func() {
		switch h := &r.Hist; h.RefKind {
		case app.RefWorktree:
			filterPill(c, iconWorktree, i18n.Tf("工作树：%s", h.RefName), h.RefPath, i18n.T("回到这个仓库自己的历史"), func() { a.ClearRefHistory(r) })
		case app.RefTag:
			filterPill(c, iconTag, i18n.Tf("标签：%s", h.RefName), h.RefName+"  "+git.Short(h.Ref), i18n.T("回到这个仓库自己的历史"), func() { a.ClearRefHistory(r) })
		}
		if p := r.Hist.Path; p != "" {
			filterPill(c, iconFile, i18n.Tf("文件历史：%s", p), p, i18n.T("不再只看这个文件"), func() { a.ClearFileHistory(r) })
		}
		if cp := r.Hist.Compare; cp != nil {
			text := i18n.Tf("比较：%s → %s", cp.From.Short, cp.To.Short)
			if !cp.Loading {
				text = i18n.Tn("比较：%s → %s · %d 个文件", cp.From.Short, cp.To.Short, len(cp.Files))
			}
			filterPill(c, iconDiff, text, compareTip(cp), i18n.T("不再比较这两个提交"), func() { a.ClearCompare(r) })
		}
	})
}

// histSyntaxTip 是搜索框右边 ? 的悬停提示：查询语法。
func histSyntaxTip() string {
	return strings.Join([]string{
		i18n.T("裸词：提交信息或作者里有，或哈希开头"),
		i18n.T("“带 空格 的词”：引号里算一个词"),
		i18n.T("/正则/：提交信息或作者匹配这个正则"),
		i18n.T("author:名字（作者:）：作者名或邮箱里有，可写几个"),
		i18n.T("after:2026-10-01（之后:）：这天起；也认 2026-10、yesterday、“2 weeks ago”"),
		i18n.T("before:2026-10（之前:）：这天（这个月）之前"),
		i18n.T("path:internal/app（路径:）：动过这个目录或文件，可写几个"),
	}, "\n")
}

// compareTip 是比较的胶囊和比较段标题的悬停提示：起点和终点的短哈希、标题。
func compareTip(cp *app.Compare) string {
	return cp.From.Short + "  " + cp.From.Subject + "\n" + cp.To.Short + "  " + cp.To.Subject
}

// compareTitle 是比较段的标题："比较 abc1234 → def5678（3 个文件，+12 −4）"。
func compareTitle(cp *app.Compare) string {
	from, to := cp.From.Short, cp.To.Short
	switch add, del := lineTotals(cp.Files); {
	case cp.Loading:
		return i18n.Tf("比较 %s → %s（读取中…）", from, to)
	case len(cp.Files) == 0:
		return i18n.Tf("比较 %s → %s（没有不同）", from, to)
	case add+del == 0:
		return i18n.Tn("比较 %s → %s（%d 个文件）", from, to, len(cp.Files))
	default:
		return i18n.Tn("比较 %s → %s（%d 个文件，+%d −%d）", from, to, len(cp.Files), add, del)
	}
}

// filterPill 是右栏历史筛选条下面的一个条件胶囊（文件历史、工作树）：图标、文字（悬停 tip），× 是 clear（读屏念 closeLabel）。
// 灰胶囊、正文色的字，× 次级色。
func filterPill(c *wui.Context, ic *wui.SVG, text, tip, closeLabel string, clear func()) {
	t := c.Theme()
	wui.Row(c).Gap(4).AlignItems(wui.Center).Padding(1, 2, 1, 8).Radius(999).Background(quietFill(t)).MinWidth(0).
		Shrink(1).Children(func() {
		wui.Icon(c, ic).FontSize(iconInline).TextColor(t.Text)
		wui.Text(c, text).FontSize(11).TextColor(t.Text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(tip)
		b := wui.Box(c).Padding(2).Radius(999).Cursor(wui.CursorPointer).Label(closeLabel).Tooltip(closeLabel).Shrink(0)
		if b.Hovered() {
			b.Background(toolFill(t, 0.1))
		}
		b.Children(func() { wui.Icon(c, iconClose).FontSize(iconInline).TextColor(t.TextMuted) })
		if b.Clicked() {
			clear()
		}
	})
}

// historyInfo 是"当前仓库"视图搜索框下面那一行：哪个仓库、哪个分支（读了几条在卡片头上）。
func historyInfo(c *wui.Context, r *app.Repo) {
	t := c.Theme()
	wui.Row(c).Padding(5, 12, 3, 12).Gap(6).AlignItems(wui.Center).Shrink(0).Children(func() {
		wui.Text(c, r.Name).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		switch {
		case r.Hist.RefKind == app.RefWorktree: // 看着一个工作树的历史："仓库 · 分支（工作树）"
			wui.Icon(c, iconWorktree).FontSize(iconInline).TextColor(t.TextMuted)
			wui.Text(c, i18n.Tf("%s（工作树）", r.Hist.RefName)).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").
				MinWidth(0).Shrink(2).Tooltip(r.Hist.RefPath)
		case r.Hist.RefKind == app.RefTag: // 看着一个标签的历史："仓库 · v0.9.0（标签）"
			wui.Icon(c, iconTag).FontSize(iconInline).TextColor(t.TextMuted)
			wui.Text(c, i18n.Tf("%s（标签）", r.Hist.RefName)).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").
				MinWidth(0).Shrink(2)
		case r.Loaded && r.Err == nil:
			wui.Icon(c, iconBranch).FontSize(iconInline).TextColor(t.TextMuted)
			wui.Text(c, r.Status.Branch).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
		}
	})
}

// histCount 是右栏卡片头上的条数："200"，还能再读就是"200+"；还没读到（或读失败）是空的。
func histCount(a *app.App, r *app.Repo) string {
	if a.LocalHistPath != "" {
		return strconv.Itoa(len(a.LocalRows))
	}
	if a.St.HistAll {
		if len(a.Repos) == 0 {
			return ""
		}
		n := strconv.Itoa(app.AllHistShown(a.AllHistRows()))
		if !a.AllHistDone() {
			n += "+"
		}
		return n
	}
	if r == nil || !r.Hist.Loaded || r.Hist.Err != nil {
		return ""
	}
	n := strconv.Itoa(len(r.Hist.Commits))
	if !r.Hist.Done {
		n += "+"
	}
	return n
}

// history 是右栏的"当前仓库"视图。
func history(a *app.App, c *wui.Context, r *app.Repo) {
	t := c.Theme()
	h := &r.Hist
	if r.Loaded {
		a.EnsureHistory(r)
	}
	switch {
	case h.Err != nil:
		message(c, iconAlert, i18n.T("读历史失败"), h.Err.Error(), t.Danger)
		return
	case !h.Loaded:
		wui.Spacer(c).Grow(1)
		return
	}
	rows, cp := app.HistRows(r), h.Compare // 列表在帧之间也可能调 Key：用这一帧的比较段，别读 h.Compare
	a.HistList.Key = func(i int) any {
		row := rows[i]
		switch {
		case row.CI == app.CompareRow && row.File < 0:
			return "compare"
		case row.CI == app.CompareRow:
			return "compare:" + cp.Files[row.File].Path
		case row.CI == app.LocalRow:
			return h.Local[row.File].T
		case row.CI < 0:
			return "uncommitted"
		case row.File < 0:
			return h.Commits[row.CI].Hash
		}
		return h.Commits[row.CI].Hash + ":" + h.Files[row.File].Path
	}
	a.HistList.Label = func(i int) string {
		row := rows[i]
		switch {
		case row.CI == app.CompareRow && row.File < 0:
			return compareTitle(cp)
		case row.CI == app.CompareRow:
			return cp.Files[row.File].Path
		case row.CI == app.LocalRow:
			return app.LocalTitle(h.Local[row.File]) + " · " + app.LocalMeta(h.Local[row.File])
		case row.CI < 0:
			return i18n.T("未提交的改动")
		case row.File < 0:
			return h.Commits[row.CI].Subject
		}
		return h.Files[row.File].Path
	}
	a.HistList.Selected = &h.Sel
	titleHover := false // 鼠标在比较段的标题上：回车 / 双击带来的 Submitted 不再切（点它时已经切过了）
	l := newListLook(a, c, 0)
	var list wui.Element
	l.build(c, func() {
		list = wui.List(c, &a.HistList, len(rows)).Grow(1).MinHeight(0).PaddingX(6).PaddingY(2).
			Rows(l.rows(func(row wui.ListRow) {
				if hr := rows[row.Index]; hr.CI == app.CompareRow {
					titleHover = compareRow(a, row, r, cp, hr) || titleHover
					return
				} else if hr.CI == app.LocalRow {
					localHistRow(a, row, r, rows, row.Index)
					return
				}
				histRow(a, row, r, rows[row.Index])
			}))
	})
	if list.Changed() {
		a.OnHistSelected(r)
	}
	if list.Submitted() {
		// 回车固定选中文件的 Diff 标签（Changed 可能刚展开了提交，行要重算）；比较段的标题展开 / 收起
		if rows := app.HistRows(r); h.Sel >= 0 && h.Sel < len(rows) {
			switch hr := rows[h.Sel]; {
			case hr.CI == app.CompareRow && hr.File < 0:
				if !titleHover {
					a.ToggleCompare(r)
				}
			case hr.CI == app.CompareRow:
				a.OpenDiff(r, h.Compare.Files[hr.File], true)
			case hr.CI == app.LocalRow:
				a.OpenLocalCompare(a.LocalAbs(r), h.Local[hr.File], true)
			case hr.File >= 0:
				a.OpenDiff(r, h.Files[hr.File], true)
			}
		}
	}
	switch {
	case len(h.Commits) == 0 && a.HistFiltered(r):
		wui.Text(c, i18n.T("没有对得上的提交")).FontSize(11).TextColor(t.TextMuted).Padding(6, 14).Shrink(0)
	case len(h.Commits) == 0:
		wui.Text(c, i18n.T("还没有提交")).FontSize(11).TextColor(t.TextMuted).Padding(6, 14).Shrink(0)
	}
	if !h.Done {
		wui.Row(c).Padding(6, 10).Gap(8).AlignItems(wui.Center).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			wui.Textf(c, i18n.T("已显示 %d 条"), len(h.Commits)).FontSize(11).TextColor(t.TextMuted).Grow(1)
			if wui.Button(c, i18n.Tf("再读 %s 条", strconv.Itoa(git.LogPage))).Disabled(h.Loading).Clicked() {
				a.LoadHistory(r, false)
			}
		})
	}
}

func histRow(a *app.App, row wui.ListRow, r *app.Repo, hr app.HistRow) {
	c := row.Context
	t := c.Theme()
	h := &r.Hist
	sel := onAccent(a, row.Selected())
	muted, mark := t.TextMuted, t.TextMuted // mark：短哈希、计数、diff 图标（不上强调色）
	if sel {
		muted, mark = t.AccentText.Alpha(0.75), t.AccentText
	}
	// 有提交图时每行是"左边图格 + 原来的内容"，图格撑满行高；内容左边的 padding 少一点，图格右边已经留了空
	g := a.HistGraph(r)
	dots := !g && a.HistDots(r) // 搜索结果、文件历史：每个提交一个点
	var dirty [2]int
	if g {
		dirty[0], dirty[1] = dirtyLine(r)
	}
	switch {
	case hr.CI < 0:
		uncommittedRow(c, r, sel, muted, mark, g, dirty)
		return
	case hr.File >= 0:
		ch := h.Files[hr.File]
		el := wui.Row(c).ContextMenu(func(m *wui.Menu) {
			changeMenu(a, m, r, ch)
		})
		if el.RowDoubleClicked() {
			a.OpenDiff(r, ch, true)
		}
		if !g && !dots {
			el.Padding(3, 8, 3, 28).Gap(8).AlignItems(wui.Center).Children(func() { commitFileLabel(c, ch, sel, muted) })
			return
		}
		el.AlignItems(wui.Stretch).Children(func() {
			if dots {
				_, down := h.DotLinks(hr.CI)
				dotPass(c, down, graphWidth(1), sel)
			} else {
				passGraph(c, h, hr.CI, dirty, sel, graphWidth(h.GraphW))
			}
			wui.Row(c).Padding(3, 8, 3, 24).Gap(8).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(func() {
				commitFileLabel(c, ch, sel, muted)
			})
		})
		return
	}
	cm := h.Commits[hr.CI]
	expanded := cm.Hash == h.Expanded
	menu := func(m *wui.Menu) { commitMenu(a, m, r, cm) }
	body := func() {
		wui.Row(c).Gap(6).AlignItems(wui.Center).Children(func() {
			wui.Text(c, cm.Subject).FontSize(12.5).FontWeight(500).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			for i, ref := range cm.Refs {
				if i >= 3 {
					wui.Textf(c, "+%d", len(cm.Refs)-3).FontSize(10).TextColor(muted).Shrink(0)
					break
				}
				refChip(c, ref, sel)
			}
		})
		commitMeta(c, cm, r.Extra.Unpushed[cm.Hash], sel, muted, mark)
		if expanded {
			commitDetail(a, c, r, cm, sel, muted)
		}
	}
	if !g && !dots {
		wui.Column(c).Padding(6, 8).Gap(3).ContextMenu(menu).Children(body)
		return
	}
	wui.Row(c).AlignItems(wui.Stretch).ContextMenu(menu).Children(func() {
		head, _ := histHead(r)
		if dots {
			up, down := h.DotLinks(hr.CI)
			dotGraph(c, cm, head, up, down, graphWidth(1), sel)
		} else {
			commitGraph(c, h, hr.CI, head, dirty, sel)
		}
		wui.Column(c).Padding(6, 8, 6, 4).Gap(3).Grow(1).MinWidth(0).Children(body)
	})
}

// compareRow 是比较段的一行：标题（箭头、点它展开 / 收起，右键能清掉）或两个提交之间改了的一个文件。
// 有提交图时左边留一格同宽的空白，只让"未提交的改动"连到 HEAD 的虚线穿过去。返回鼠标在标题上。
func compareRow(a *app.App, row wui.ListRow, r *app.Repo, cp *app.Compare, hr app.HistRow) bool {
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted, mark := t.TextMuted, t.TextMuted // mark：短哈希、计数、diff 图标（不上强调色）
	if sel {
		muted, mark = t.AccentText.Alpha(0.75), t.AccentText
	}
	g := a.HistGraph(r)
	var dirty [2]int
	if g {
		dirty[0], dirty[1] = dirtyLine(r)
	}
	line := func(el wui.Element, pad [4]float32, body func()) {
		if !g {
			el.Padding(pad[0], pad[1], pad[2], pad[3]).Gap(8).AlignItems(wui.Center).Children(body)
			return
		}
		el.AlignItems(wui.Stretch).Children(func() {
			gapGraph(c, &r.Hist, dirty, sel)
			wui.Row(c).Padding(pad[0], pad[1], pad[2], pad[3]-4).Gap(8).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(body)
		})
	}
	if hr.File >= 0 {
		ch := cp.Files[hr.File]
		el := wui.Row(c).ContextMenu(func(m *wui.Menu) { changeMenu(a, m, r, ch) })
		if el.RowDoubleClicked() {
			a.OpenDiff(r, ch, true)
		}
		line(el, [4]float32{3, 8, 3, 28}, func() { commitFileLabel(c, ch, sel, muted) })
		return false
	}
	toggle := func() { a.ToggleCompare(r) }
	el := wui.Row(c).Tooltip(compareTip(cp)).ContextMenu(func(m *wui.Menu) {
		look := i18n.T("收起")
		if !cp.Open {
			look = i18n.T("展开")
		}
		if m.Item(look).Chosen() {
			toggle()
		}
		if m.Item(i18n.T("不再比较")).Chosen() {
			a.ClearCompare(r)
		}
	})
	if el.Clicked() {
		toggle()
	}
	line(el, [4]float32{6, 8, 6, 8}, func() {
		chevron(c, iconRow, cp.Open, cp.From.Short+" → "+cp.To.Short, muted, toggle)
		wui.Icon(c, iconDiff).FontSize(iconRow).TextColor(mark)
		wui.Text(c, compareTitle(cp)).FontSize(12.5).FontWeight(500).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
	return el.Hovered()
}

// commitMeta 是提交行的第二行：短哈希、还没推送的标记、作者、多久以前、是不是合并。
func commitMeta(c *wui.Context, cm git.Commit, unpushed, sel bool, muted, mark wui.Color) {
	wui.Row(c).Gap(6).AlignItems(wui.Center).Children(func() {
		wui.Text(c, cm.Short).Font(mono()).FontSize(10.5).TextColor(mark).SingleLine().Shrink(0)
		if unpushed {
			color := c.Theme().Warning
			if sel {
				color = muted
			}
			wui.Row(c).Gap(2).AlignItems(wui.Center).Shrink(0).Tooltip(i18n.T("还没推送到任何远程分支")).Children(func() {
				wui.Icon(c, iconArrowUp).FontSize(iconInline).TextColor(color)
				wui.Text(c, i18n.T("未推送")).FontSize(10).FontWeight(500).TextColor(color).SingleLine()
			})
		}
		wui.Text(c, cm.Author).FontSize(10.5).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		wui.Text(c, "·").FontSize(10.5).TextColor(muted).Shrink(0)
		wui.Text(c, git.Ago(cm.Date)).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0).
			Tooltip(cm.Date.Local().Format("2006-01-02 15:04"))
		if len(cm.Parents) > 1 {
			wui.Text(c, i18n.T("· 合并")).FontSize(10.5).TextColor(muted).Shrink(0)
		}
	})
}

// commitDetail 是展开的提交行下面的详情：正文、完整哈希（点了复制）、作者和邮箱、具体时间、父提交、
// 在网页打开（推送过的；GitHub、GitLab、Bitbucket、Gitea 或主机名），然后是"N 个文件"。
func commitDetail(a *app.App, c *wui.Context, r *app.Repo, cm git.Commit, sel bool, muted wui.Color) {
	h := &r.Hist
	if cm.Body != "" {
		wui.Text(c, cm.Body).FontSize(11).TextColor(muted).Margin(2, 0, 2, 0)
	}
	wui.Column(c).Gap(2).Margin(2, 0, 2, 0).Children(func() {
		hash := wui.Box(c).Cursor(wui.CursorPointer).Label(i18n.T("复制提交哈希")).Tooltip(i18n.T("点一下复制完整哈希"))
		hash.Children(func() {
			wui.Text(c, cm.Hash).Font(mono()).FontSize(10).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		})
		if hash.Clicked() {
			copyText(cm.Hash)
			a.Toast = i18n.T("已复制提交哈希")
		}
		who := cm.Author
		if cm.Email != "" {
			who += " <" + cm.Email + ">"
		}
		wui.Text(c, who).FontSize(10.5).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		wui.Text(c, cm.Date.Local().Format("2006-01-02 15:04:05 -0700")).FontSize(10.5).TextColor(muted).SingleLine()
		if len(cm.Parents) > 0 {
			ps := make([]string, len(cm.Parents))
			for i, p := range cm.Parents {
				ps[i] = git.Short(p)
			}
			wui.Text(c, i18n.Tf("父提交 %s", strings.Join(ps, " "))).Font(mono()).FontSize(10).TextColor(muted).SingleLine()
		}
		if w := a.WebOf(r); w.OK() && r.Extra.Pushed(cm.Hash) {
			u := w.Commit(cm.Hash)
			wui.Row(c).Children(func() {
				textButtonOn(c, i18n.Tf("在 %s 打开", w.Site()), u, sel, func() { a.OpenWeb(u) })
			})
		}
	})
	switch {
	case h.FilesLoading:
		wui.Text(c, i18n.T("读取文件…")).FontSize(10.5).TextColor(muted)
	case h.FilesFor == cm.Hash && len(h.Files) == 0:
		wui.Text(c, i18n.T("没有改动文件")).FontSize(10.5).TextColor(muted)
	case h.FilesFor == cm.Hash:
		wui.Text(c, fileCount(h.Files)).FontSize(10.5).TextColor(muted)
	}
}

// fileCount 是"N 个文件 · +A −D"，都没有行数（只有二进制）时只写个数。
func fileCount(files []git.Change) string {
	add, del := lineTotals(files)
	if add == 0 && del == 0 {
		return i18n.Tn("%d 个文件", len(files))
	}
	return i18n.Tn("%d 个文件 · +%d −%d", len(files), add, del)
}

// uncommittedRow 是历史列表的第一行：工作区里还没提交的改动。
// g 是有提交图：左边多一个图格；dirty 是 dirtyLine 的结果。
func uncommittedRow(c *wui.Context, r *app.Repo, sel bool, muted, mark wui.Color, g bool, dirty [2]int) {
	el := wui.Row(c).Tooltip(app.Tip(i18n.T("在侧栏的 Git 栏里看"), "view.git"))
	if g {
		el.AlignItems(wui.Stretch).Children(func() {
			uncommittedGraph(c, r, dirty, sel)
			wui.Row(c).Padding(7, 8, 7, 4).Gap(8).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(func() {
				uncommittedBody(c, r, sel, muted, mark)
			})
		})
		return
	}
	el.Padding(7, 8).Gap(8).AlignItems(wui.Center).Children(func() { uncommittedBody(c, r, sel, muted, mark) })
}

// uncommittedBody 是"未提交的改动"那一行的内容：颜色点（有改动黄、干净绿）、标题、领先落后、改动数。
// 看着一个工作树的历史时是那个工作树的改动数（没算出来时灰点、不写数），看着一个标签时灰点、不写数；都不写领先落后。
func uncommittedBody(c *wui.Context, r *app.Repo, sel bool, muted, mark wui.Color) {
	t := c.Theme()
	_, n := histHead(r)
	ref := r.Hist.RefKind != app.RefNone
	dot := t.Warning
	switch {
	case n < 0:
		dot = t.TextMuted.Alpha(0.5)
	case n == 0:
		dot = t.Success
	}
	if sel {
		dot = t.AccentText
	}
	wui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
	wui.Text(c, i18n.T("未提交的改动")).FontSize(12.5).FontWeight(500).SingleLine().Grow(1).MinWidth(0)
	if s := app.UpDown(r.Status); s != "" && !ref {
		wui.Text(c, s).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0).Tooltip(i18n.T("领先 / 落后上游的提交数"))
	}
	switch {
	case !r.Loaded || n < 0:
	case n > 0:
		pill(c, strconv.Itoa(n), mark, sel)
	default:
		wui.Text(c, i18n.T("无")).FontSize(10.5).TextColor(muted).Shrink(0)
	}
}

// refChip 是提交行右边的分支 / 标签名：本地分支正文色、远程次级色（都是灰胶囊），标签黄。
func refChip(c *wui.Context, ref string, onAccent bool) {
	t := c.Theme()
	bg, fg, ic := quietFill(t), t.Text, iconBranch
	switch {
	case strings.HasPrefix(ref, "tag: "):
		ref, bg, fg, ic = strings.TrimPrefix(ref, "tag: "), t.Warning.Alpha(0.14), t.Warning, iconTag
	case strings.Contains(ref, "/"):
		fg = t.TextMuted
	}
	if onAccent {
		bg, fg = t.AccentText.Alpha(0.22), t.AccentText
	}
	wui.Row(c).Gap(3).AlignItems(wui.Center).Padding(1, 6).Radius(999).Background(bg).Shrink(0).Children(func() {
		wui.Icon(c, ic).FontSize(iconInline).TextColor(fg)
		wui.Text(c, ref).FontSize(10).FontWeight(500).TextColor(fg).SingleLine().Ellipsis("…").MaxWidth(120)
	})
}

// allHistory 是右栏的"所有仓库"视图。
func allHistory(a *app.App, c *wui.Context) {
	t := c.Theme()
	a.EnsureAllHistory()
	rows := a.AllHistRows()
	shown := app.AllHistShown(rows)
	wui.Row(c).Padding(5, 12, 3, 12).Gap(6).AlignItems(wui.Center).Shrink(0).Children(func() {
		wui.Text(c, i18n.Tf("%s 个仓库", strconv.Itoa(len(a.Repos)))).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
	})
	if len(rows) == 0 {
		switch {
		case a.AllHistLoading() || a.AnyLoading():
			wui.Spacer(c).Grow(1)
		case strings.TrimSpace(a.HistQuery) != "":
			message(c, iconHistory, i18n.T("没有对得上的提交"), "", t.TextMuted)
		default:
			message(c, iconHistory, i18n.T("还没有提交"), "", t.TextMuted)
		}
		return
	}
	a.AllHist.Key = func(i int) any { return rows[i].Key() }
	a.AllHist.Header = func(i int) bool { return rows[i].Kind == app.AllHeader }
	a.AllHist.Label = func(i int) string {
		row := rows[i]
		switch row.Kind {
		case app.AllDirty:
			return i18n.Tf("%s 未提交的改动", row.Repo.Name)
		case app.AllCommit:
			return row.Repo.Name + " " + row.Commit().Subject
		case app.AllFile:
			return row.Repo.Hist.Files[row.File].Path
		}
		return row.Title
	}
	a.AllHist.Selected = &a.HistAllSel
	l := newListLook(a, c, 0)
	w := allGraphW(a)
	var list wui.Element
	l.build(c, func() {
		list = wui.List(c, &a.AllHist, len(rows)).Grow(1).MinHeight(0).PaddingX(6).PaddingY(2).
			Rows(l.rows(func(row wui.ListRow) { allHistRow(a, row, rows, w) }))
	})
	if list.Changed() {
		a.OnAllHistSelected()
	}
	if list.Submitted() {
		if rows := a.AllHistRows(); a.HistAllSel >= 0 && a.HistAllSel < len(rows) && rows[a.HistAllSel].Kind == app.AllFile {
			row := rows[a.HistAllSel]
			a.OpenDiff(row.Repo, row.Repo.Hist.Files[row.File], true)
		}
	}
	if !a.AllHistDone() {
		wui.Row(c).Padding(6, 10).Gap(8).AlignItems(wui.Center).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			wui.Textf(c, i18n.T("已显示 %d 条"), shown).FontSize(11).TextColor(t.TextMuted).Grow(1).
				Tooltip(i18n.T("每个仓库各读一页再按时间合并；有仓库那一页读完了，后面的等再读一页才排得准"))
			if wui.Button(c, i18n.Tf("再读 %s 条", strconv.Itoa(git.LogPage))).Disabled(a.AllHistLoading()).
				Tooltip(i18n.T("每个还没读到头的仓库各读一页")).Clicked() {
				a.LoadMoreAllHistory()
			}
		})
	}
}

// allGraphW 是"所有仓库"视图的图格宽：画提交图的仓库里最宽的那个，都画圆点是一条泳道宽，都不画是 0。
func allGraphW(a *app.App) float32 {
	w := float32(0)
	for _, r := range a.Repos {
		switch {
		case a.HistGraph(r):
			w = max(w, graphWidth(r.Hist.GraphW))
		case a.HistDots(r):
			w = max(w, graphWidth(1))
		}
	}
	return w
}

// allHistRow 画"所有仓库"视图的一行（rows[row.Index]）。w 是图格宽（allGraphW）：每个仓库那一段用它自己的提交图，
// 段头不画，和上 / 下一个提交行不是这个仓库里挨着的提交（app.AllHistLinks）那半截渐隐；搜索时画圆点。
func allHistRow(a *app.App, row wui.ListRow, rows []app.AllHistRow, w float32) {
	hr := rows[row.Index]
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted, mark := t.TextMuted, t.TextMuted // mark：短哈希、计数、diff 图标（不上强调色）
	if sel {
		muted, mark = t.AccentText.Alpha(0.75), t.AccentText
	}
	r := hr.Repo
	switch hr.Kind {
	case app.AllHeader:
		wui.Row(c).Padding(7, 8, 3, 8).Background(t.Background).Children(func() {
			wui.Text(c, hr.Title).FontSize(11).FontWeight(600).TextColor(t.TextMuted)
		})
	case app.AllDirty:
		wui.Row(c).Padding(6, 8).Gap(8).AlignItems(wui.Center).Tooltip(i18n.T("在侧栏的 Git 栏里看这个仓库")).
			ContextMenu(func(m *wui.Menu) { repoMenu(a, m, r, false) }).Children(func() {
			dot := t.Warning
			if sel {
				dot = t.AccentText
			}
			wui.Box(c).Size(8, 8).Radius(4).Background(dot).Shrink(0)
			repoChip(a, c, r, sel)
			wui.Text(c, changeSummary(r.Status)).FontSize(11).TextColor(muted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			pill(c, strconv.Itoa(r.Status.Count), mark, sel)
		})
	case app.AllFile:
		ch := r.Hist.Files[hr.File]
		el := wui.Row(c).ContextMenu(func(m *wui.Menu) {
			changeMenu(a, m, r, ch)
		})
		if el.RowDoubleClicked() {
			a.OpenDiff(r, ch, true)
		}
		g, dots := a.HistGraph(r), a.HistDots(r)
		if !g && !dots {
			el.Padding(3, 8, 3, 28).Gap(8).AlignItems(wui.Center).Children(func() { commitFileLabel(c, ch, sel, muted) })
			return
		}
		_, down := a.AllHistLinks(rows, row.Index)
		el.AlignItems(wui.Stretch).Children(func() {
			switch {
			case g && down:
				passGraph(c, &r.Hist, hr.CI, [2]int{-1, -1}, sel, w)
			case g:
				wui.Box(c).Width(w).Shrink(0)
			default:
				dotPass(c, down, w, sel)
			}
			wui.Row(c).Padding(3, 8, 3, 24).Gap(8).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(func() {
				commitFileLabel(c, ch, sel, muted)
			})
		})
	default:
		cm := hr.Commit()
		h := &r.Hist
		menu := func(m *wui.Menu) { commitMenu(a, m, r, cm) }
		body := func() {
			wui.Row(c).Gap(6).AlignItems(wui.Center).Children(func() {
				repoChip(a, c, r, sel)
				wui.Text(c, cm.Subject).FontSize(12.5).FontWeight(500).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				for i, ref := range cm.Refs {
					if i >= 2 {
						wui.Textf(c, "+%d", len(cm.Refs)-2).FontSize(10).TextColor(muted).Shrink(0)
						break
					}
					refChip(c, ref, sel)
				}
			})
			commitMeta(c, cm, r.Extra.Unpushed[cm.Hash], sel, muted, mark)
			if cm.Hash == h.Expanded {
				commitDetail(a, c, r, cm, sel, muted)
			}
		}
		g, dots := a.HistGraph(r), a.HistDots(r)
		if !g && !dots {
			wui.Column(c).Padding(6, 8).Gap(3).ContextMenu(menu).Children(body)
			return
		}
		up, down := a.AllHistLinks(rows, row.Index)
		wui.Row(c).AlignItems(wui.Stretch).ContextMenu(menu).Children(func() {
			if g {
				cutGraph(c, h, hr.CI, r.Extra.Head, [2]int{-1, -1}, sel, graphCut{w: w, fadeUp: !up, fadeDown: !down})
			} else {
				dotGraph(c, cm, r.Extra.Head, up, down, w, sel)
			}
			wui.Column(c).Padding(6, 8, 6, 4).Gap(3).Grow(1).MinWidth(0).Children(body)
		})
	}
}

// repoChip 是"所有仓库"视图里每行前面的仓库名胶囊；颜色按仓库在列表里的位置轮换，好一眼分开。
// 不用红色：红色在这里是冲突和出错。紫、青两个是固定色，浅色和深色主题上都看得清。
func repoChip(a *app.App, c *wui.Context, r *app.Repo, onAccent bool) {
	t := c.Theme()
	palette := []wui.Color{t.Accent, t.Success, t.Warning, wui.RGB(0x8b, 0x5c, 0xf6), wui.RGB(0x14, 0xa3, 0x9a), t.TextMuted}
	color := palette[max(0, slices.Index(a.Repos, r))%len(palette)]
	bg, fg := color.Alpha(0.14), color
	if onAccent {
		bg, fg = t.AccentText.Alpha(0.22), t.AccentText
	}
	wui.Box(c).Padding(1, 6).Radius(999).Background(bg).Shrink(0).Tooltip(r.Root).Children(func() {
		wui.Text(c, r.Name).FontSize(10).FontWeight(600).TextColor(fg).SingleLine().Ellipsis("…").MaxWidth(110)
	})
}
