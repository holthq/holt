package ui

import (
	"slices"
	"strconv"

	"github.com/egoist/mygo/plugins/terminal"
	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
)

// bottomBar 是底栏：当前仓库的终端标签（每个仓库自己一组，切仓库就切过去；没有仓库时是家目录的）、输出（git 操作、>git 命令的日志）
// 和问题（检查命令报的，所有仓库一起，problems.go）。
// 终端标签里可以左右 / 上下分屏；shell 在那一格第一次画出来时才启动（app/terminals.go）。
// 底栏是一张卡片，标签条是它的头；分了屏的标签里卡身不铺底、每格自己铺，格与格之间露出 4 pt 的材质缝。
func bottomBar(a *app.App, c *wui.Context) {
	r := a.TermRepo() // 没有仓库时是家目录

	panel := wui.Column(c).Shrink(0)
	if a.BottomMaxed() { // 最大化：占掉编辑区那块（ui/view.go 的 center 不画主区和拖条）
		panel.Grow(1).MinWidth(0).MinHeight(0)
	} else if a.St.Settings.TermDock == "right" {
		panel.Width(a.St.TermW)
	} else {
		panel.Height(a.St.BottomH)
	}
	onTerm := a.BottomTab == app.BottomTerminal && r != nil
	var tab *app.TermTab
	var pane *app.TermPane
	if r != nil {
		tab = a.CurTermTab(r)
		if tab != nil {
			pane = tab.Active
		}
	}
	card(c, panel, func() {
		t := c.Theme()
		cardHeader(c).Padding(0, 6, 0, 0).Gap(2).Children(func() { // 左边 0：第一个标签贴着卡片左边
			// 终端标签多了横向滚动（同编辑器的标签条），标签少时「+」紧跟着最后一个；输出、问题固定在后面，标题行右边的按钮总在。
			if r != nil {
				scrollStrip(c.Key("tabs"), &r.TermStrip, false, func() {
					for i, tt := range slices.Clone(a.TermTabs(r)) { // 拷贝：右键菜单、拖标签在这一圈里改顺序
						termTab(a, c.Key(termTabKey{r.Root, tt.Num}), r, tt, onTerm && i == r.TermSel)
					}
				})
				newTermButton(a, c, r)
			}
			out := i18n.T("输出")
			if n := len(a.Output); n > 0 {
				out += " " + strconv.Itoa(n)
			}
			bottomTab(a, c, app.BottomOutput, iconOutput, out, len(a.Output), r != nil)
			_, errs, warns := a.ProblemList()
			bottomTab(a, c, app.BottomProblems, iconAlert, problemsTabLabel(a), errs+warns, false)
			// shell 的标题只拿标签和按钮剩下的地方（Basis 0）：长标题（user@host:很长的路径）先省略，标签不让
			// 双击标题行的空白处（标签和按钮以外）最大化 / 还原底栏
			var blank wui.Element
			if onTerm && pane != nil && pane.Title != "" && pane.Title != r.Name {
				blank = wui.Text(c.Key("blank"), pane.Title).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Basis(0).Grow(1).MinWidth(0).Margin(0, 0, 0, 6)
			} else {
				blank = wui.Box(c.Key("blank")).Grow(1).MinWidth(0).AlignSelf(wui.Stretch)
			}
			if blank.DoubleClicked() {
				a.Exec("view.maximizeBottom")
			}
			switch {
			case a.BottomTab == app.BottomProblems:
				problemsHeader(a, c)
			case a.BottomTab == app.BottomOutput:
				if len(a.Output) > 0 {
					iconButton(c, iconTrash, i18n.T("清空输出"), a.ClearOutput)
				}
			case !onTerm || pane == nil:
			default:
				switch {
				case pane.Err != nil:
					chip(c, iconAlert, i18n.T("启动失败"), t.Danger, t.Danger.Alpha(0.12))
				case pane.Exited:
					chip(c, nil, i18n.Tf("已退出 %d", pane.ExitCode), t.TextMuted, t.Text.Alpha(0.06))
				}
				if pane.Term != nil {
					iconButton(c, iconSearch, app.Tip(i18n.T("在终端里查找…"), "term.find"), func() { a.FocusPane(r, pane); a.FindInTerm() })
				}
				iconButton(c, iconColumns, app.Tip(i18n.T("左右分屏"), "term.splitRight"), func() { a.SplitTerm(r, true) })
				iconButton(c, iconRows, app.Tip(i18n.T("上下分屏"), "term.splitDown"), func() { a.SplitTerm(r, false) })
				if tab.Root.Pane == nil {
					iconButton(c, iconPaneClose, app.Tip(i18n.T("关闭有键盘的那一格分屏"), "term.closePane"), func() { a.ClosePane(r, tab, pane) })
				}
				iconButton(c, iconRefresh, i18n.T("重新启动终端"), func() { a.RestartPane(r, pane) })
			}
			if a.BottomMaxed() {
				iconButton(c, iconMinimize, app.Tip(i18n.T("还原底栏"), "view.maximizeBottom"), func() { a.Exec("view.maximizeBottom") })
			} else {
				iconButton(c, iconMaximize, app.Tip(i18n.T("最大化底栏"), "view.maximizeBottom"), func() { a.Exec("view.maximizeBottom") })
			}
			iconButton(c, iconClose, app.Tip(i18n.T("关闭底栏"), "view.bottom"), func() { a.Exec("view.bottom") })
		})
		// 标签条里的右键菜单、「+」这一帧可能换了标签（新开、关掉）：卡身按换过的画，不然旧标签那一格还有键盘、又把选中拉回去
		if r != nil {
			tab = a.CurTermTab(r)
		}
		wui.Box(c).Grow(1).MinHeight(0).Children(func() {
			switch {
			case a.BottomTab == app.BottomProblems:
				problems(a, c)
			case !onTerm:
				output(a, c)
			case tab == nil:
				noTerminal(a, c, r)
			default:
				termNode(a, c.Key(termTabKey{r.Root, tab.Num}), r, tab, tab.Root, "")
			}
		})
	})
	if onTerm && tab != nil && tab.Root.Pane == nil {
		panel.Background(wui.Transparent)
	}
}

type termTabKey struct {
	root string
	num  int
}

type paneKey struct {
	id, gen int
}

// termTab 是标题行里的一个终端标签：点了切过去，双击改名，× 关掉（有命令在跑先问），右键有改名、挪位置、关闭。
// 分了屏的标签名字后面写格数。
func termTab(a *app.App, c *wui.Context, r *app.Repo, tt *app.TermTab, active bool) {
	t := c.Theme()
	if a.Rename.Tab == tt {
		renameTab(a, c, r, tt)
		return
	}
	label := r.TermLabel(tt)
	b := panelTab(c, active).Label(label).Tooltip(i18n.T("双击改名"))
	fg := panelTabColor(c, b, active)
	hover := b.Hovered()
	if active && r.TermShown != tt.Num { // 换了当前终端标签：滚到看得见（同编辑器的 g.TabShown）
		r.TermShown = tt.Num
		b.ScrollIntoView()
	}
	ctxMenu(a, c, b, menuOwner{"termTab", r.Root + "\x00" + strconv.Itoa(tt.Num)}, func(m menu) { termTabMenu(a, m, r, tt) })
	// 拖标签换位置（只在这个仓库的终端标签之间）
	ts := a.TermTabs(r)
	b.Drag(tt)
	if b.Dragging() {
		b.Opacity(0.5)
	}
	over, _ := wui.DragOver[*app.TermTab](b)
	if v, ok := wui.Drop[*app.TermTab](b); ok && v != tt {
		a.MoveTermTab(r, slices.Index(ts, v), slices.Index(ts, tt))
	}
	b.Children(func() {
		if j := slices.Index(ts, over); j >= 0 && over != tt {
			dropMark(c, j > slices.Index(ts, tt))
		}
		wui.Icon(c, iconTerminal).FontSize(iconRow).TextColor(fg)
		agentMark(c, a.TabAgent(tt))
		panelTabText(c, label, fg)
		if n := len(tt.Panes()); n > 1 {
			wui.Text(c, strconv.Itoa(n)).FontSize(10).TextColor(t.TextMuted).SingleLine().Padding(0, 4).Radius(999).
				Background(t.Text.Alpha(0.07)).Tooltip(i18n.Tf("分成 %d 格", n))
		}
		// × 占着位置，只在当前和悬停的标签上画出来，免得标签宽度跟着鼠标跳。
		x := wui.Box(c.Key("x")).Size(18, 18).AlignItems(wui.Center).Justify(wui.Center).Radius(5).Shrink(0).
			Label(i18n.T("关闭终端标签")).Tooltip(i18n.T("关闭终端标签"))
		xc := wui.Transparent
		if active || hover {
			xc = t.TextMuted
			x.Cursor(wui.CursorPointer)
			if x.Hovered() {
				x.Background(toolFill(t, 0.06))
			}
		}
		x.Children(func() { wui.Icon(c, iconClose).FontSize(iconRow).TextColor(xc) })
		if (active || hover) && x.Clicked() {
			a.CloseTermTab(r, tt)
		}
	})
	switch {
	case b.DoubleClicked():
		a.StartRenameTerm(r, tt)
	case b.Clicked():
		if i := slices.Index(a.TermTabs(r), tt); i >= 0 {
			a.SelectTermTab(r, i)
		}
	}
}

// renameTab 是改名中的终端标签：回车或点到别处算数，Esc 不改。
func renameTab(a *app.App, c *wui.Context, r *app.Repo, tt *app.TermTab) {
	t := c.Theme()
	e := &a.Rename
	panelTab(c, true).Padding(0, 6).Children(func() {
		wui.Icon(c, iconTerminal).FontSize(iconRow).TextColor(t.Text)
		in := wui.TextInput(c.Key("name"), &e.Text).Label(i18n.T("终端标签的名字")).Placeholder(r.TermLabel(&app.TermTab{Num: tt.Num})).
			FontSize(12).TextColor(t.Text).Width(140).
			OnTextKey(func(k wui.TextKey) bool {
				if k.Key != wui.KeyEscape || k.Mods != 0 {
					return false
				}
				a.CancelRenameTerm()
				return true
			})
		if !e.Started {
			e.Started = true
			in.Focus().SetTextSelection(0, len([]rune(e.Text)))
		}
		switch {
		case in.Submitted():
			a.RenameTerm(r, tt, e.Text)
			a.FocusTermTab(tt)
		case in.Focused():
			e.Focused = true
		case e.Focused:
			a.RenameTerm(r, tt, e.Text) // 点到别处去了
		}
	})
}

// termTabMenu 是终端标签的右键菜单。
func termTabMenu(a *app.App, m menu, r *app.Repo, tt *app.TermTab) {
	ts := a.TermTabs(r)
	i := slices.Index(ts, tt)
	if m.Item(i18n.T("重命名…")).Chosen() {
		a.StartRenameTerm(r, tt)
	}
	m.Separator()
	if m.Item(i18n.T("向左移")).Disabled(i <= 0).Chosen() {
		a.MoveTermTab(r, i, i-1)
	}
	if m.Item(i18n.T("向右移")).Disabled(i < 0 || i >= len(ts)-1).Chosen() {
		a.MoveTermTab(r, i, i+1)
	}
	m.Separator()
	// 开成标签、复制全部都对这个标签里有键盘的那一格（格自己的右键菜单是终端插件的，加不进东西）
	p := tt.Current()
	if m.Item(i18n.T("把终端内容开成标签")).Disabled(p.Term == nil).Chosen() {
		a.OpenPaneText(r, tt, p)
	}
	if m.Item(i18n.T("复制全部输出")).Disabled(p.Term == nil).Chosen() {
		a.CopyPaneOutput(p)
	}
	m.Separator()
	if m.Item(i18n.T("挪到编辑器区")).Chosen() {
		a.MoveTermToEditor(r, tt)
	}
	if m.Item(i18n.T("关闭终端标签")).Chosen() {
		a.CloseTermTab(r, tt)
	}
}

// noTerminal 是仓库的终端标签都关了时底栏的样子。
func noTerminal(a *app.App, c *wui.Context, r *app.Repo) {
	t := c.Theme()
	wui.Column(c).Fill().AlignItems(wui.Center).Justify(wui.Center).Gap(8).Children(func() {
		wui.Icon(c, iconTerminal).FontSize(iconEmpty).TextColor(t.TextMuted.Alpha(0.8))
		wui.Text(c, i18n.Tf("%s 没有打开的终端", r.Name)).FontSize(13).FontWeight(600).TextColor(t.Text)
		if wui.Button(c, i18n.T("新建终端")).Tooltip(app.Tip(i18n.T("新建终端"), "term.new")).Clicked() {
			a.NewTerminal(r, "")
		}
	})
}

// termNode 画终端标签里的一棵分屏树：叶子是一格终端，别的是左右（Row）/ 上下排开的两半，中间一条能拖的缝。
func termNode(a *app.App, c *wui.Context, r *app.Repo, tab *app.TermTab, n *app.TermNode, path string) {
	if n.Pane != nil {
		termPane(a, c.Key(paneKey{n.Pane.ID, 0}), r, tab, n.Pane)
		return
	}
	if n.Ratio <= 0 || n.Ratio >= 1 {
		n.Ratio = 0.5
	}
	// Row 和 Column 各建一个元素，只建用到的那个：同一个 key 下建了又换掉的会留成一个空的兄弟。
	key := "split-" + path
	var box wui.Element
	if n.Row {
		box = wui.Row(c.Key(key))
	} else {
		box = wui.Column(c.Key(key))
	}
	box.Fill().Grow(1).Basis(0).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch)
	box.Children(func() {
		wui.Column(c.Key("a")).Grow(n.Ratio).Basis(0).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch).Children(func() {
			termNode(a, c, r, tab, n.A, path+"a")
		})
		splitDivider(c, n.Row, &n.Ratio, box)
		wui.Column(c.Key("b")).Grow(1 - n.Ratio).Basis(0).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch).Children(func() {
			termNode(a, c, r, tab, n.B, path+"b")
		})
	})
}

// splitDivider 是分屏两半之间 4 pt 的材质缝（终端的格、编辑器组）：拖了改 A 占的比例（12%–88%），双击回到一半一半。
// 拖它不抢键盘。
func splitDivider(c *wui.Context, row bool, ratio *float32, split wui.Element) {
	var d wui.Element
	if row {
		d = wui.Row(c.Key("div")).Width(splitGap).Cursor(wui.CursorResizeColumn)
	} else {
		d = wui.Column(c.Key("div")).Height(splitGap).Cursor(wui.CursorResizeRow)
	}
	d.Shrink(0).Justify(wui.Center).AlignItems(wui.Stretch).KeepFocus().Label(i18n.T("拖动调整分屏大小"))
	gapLine(c, d, row)
	if dx, dy, ok := d.Dragged(); ok {
		b := split.Bounds()
		size, delta := b.W-splitGap, dx
		if !row {
			size, delta = b.H-splitGap, dy
		}
		if size > 0 {
			*ratio = min(max(*ratio+delta/size, 0.12), 0.88)
		}
	}
	if d.DoubleClicked() {
		*ratio = 0.5
	}
}

// termPane 是一格终端：shell 第一次画出来时才启动。分屏时每格自己铺卡片的底，有键盘的一格顶上一条强调色。
// shell 活着时接文件树拖来的行（app.PastePaths）和别的应用拖来的文字、文件（app.DropOnTerm，访达的文件和文件树的一样
// 单引号写法），拖着停在上面整格一层淡强调色加一圈边；shell 退出了的格子是终端视图自己接访达的文件（mygo 反斜杠转义）。
func termPane(a *app.App, c *wui.Context, r *app.Repo, tab *app.TermTab, p *app.TermPane) {
	t := c.Theme()
	a.EnsurePane(r, p)
	multi := tab.Root.Pane == nil
	box := wui.Box(c).Fill().Grow(1).Basis(0).MinWidth(0).MinHeight(0)
	if multi {
		box.Background(t.Background)
	}
	if box.FocusWithin() {
		a.FocusPane(r, p)
	}
	hover := box.Hovered()
	over := false
	if p.Term != nil && !p.Exited { // 文件树的行拖到这一格：贴引号过的绝对路径；shell 没了不接
		if v, ok := wui.Drop[app.TreeDrag](box); ok {
			a.PastePaths(r, p, v)
		}
		_, over = wui.DragOver[app.TreeDrag](box)
		if drop, ok := wui.DropData(box, app.DataDrop); ok {
			a.DropOnTerm(r, p, drop)
		}
		if _, ok := wui.DataDragOver(box, app.DataDrop); ok {
			over = true
		}
	} else if p.Term != nil { // 退出了：访达拖来的文件打开（不然落到终端视图自己那儿，写进没有 shell 的终端）
		if drop, ok := wui.DropData(box, app.ExitedDrop); ok {
			a.DropOnTerm(r, p, drop)
		}
	}
	box.Children(func() {
		switch {
		case p.Err != nil:
			message(c, iconAlert, i18n.T("终端启动失败"), p.Err.Error(), t.Danger)
		case p.Connecting(): // 在连终端后台进程（第一次要先起它）
			wui.Row(c.Key("connecting")).Padding(12, 14).Gap(8).AlignItems(wui.Center).Children(func() {
				wui.Spinner(c)
				wui.Text(c, i18n.T("启动中…")).FontSize(12).TextColor(t.TextMuted)
			})
		case p.Term != nil:
			tv := terminal.View(c.Key(paneKey{p.ID, p.Gen()}), p.Term).Fill().Bind(&p.View)
			if a.FocusTerm && tab.Active == p && !p.Find.Open && a.Rename.Tab == nil && (a.FocusTab == tab || a.FocusTab == nil && !tab.InEditor) {
				a.FocusTerm, a.FocusTab = false, nil
				tv.Focus()
			}
			if p.Exited {
				exitedBar(a, c, r, tab, p)
			}
			if p.Find.Open {
				termFind(a, c, p)
			}
		}
		if multi && tab.Active == p {
			focusBar(c)
		}
		if multi && hover && !p.Find.Open {
			paneClose(a, c, r, tab, p)
		}
		if over {
			wui.Box(c.Key("drop")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(t.Accent.Alpha(0.08)).Border(2, t.Accent.Alpha(0.6))
		}
	})
}

// paneClose 是分屏时指针所在那一格右上角的 ×：关掉这一格（前台有命令在跑先问），别的格占回它的地方。
func paneClose(a *app.App, c *wui.Context, r *app.Repo, tab *app.TermTab, p *app.TermPane) {
	t := c.Theme()
	wui.Box(c.Key("close")).Absolute().Top(5).Right(5).Padding(1).Radius(7).Background(t.Background).Border(1, t.Border).
		Shadow(0, 2, 6, 0, wui.RGBA(0, 0, 0, 0.12)).Children(func() {
		rowButton(c, iconClose, i18n.T("关闭这一格分屏"), app.Tip(i18n.T("关闭这一格分屏"), "term.closePane"), func() { a.ClosePane(r, tab, p) })
	})
}

// exitedBar 是 shell 退出后压在那一格底部的一条：退出码、重新启动、关闭。回看里的输出还在上面。
// 后台模式下和终端后台进程的连接断了（会话没了、被别的窗口接走了）时说的是断的原因；
// shell 退出了而会话还在后台进程里时补一句，重新启动是关掉它开一个新的。
func exitedBar(a *app.App, c *wui.Context, r *app.Repo, tab *app.TermTab, p *app.TermPane) {
	t := c.Theme()
	msg := i18n.Tf("shell 已退出，退出码 %d", p.ExitCode)
	switch {
	case p.Lost() != "":
		msg = p.Lost()
	case p.Daemon():
		msg = i18n.Tf("shell 已退出，退出码 %d；会话还在后台进程里，重新启动会换一个", p.ExitCode)
	}
	wui.Row(c.Key("exited")).Absolute().Left(8).Right(8).Bottom(8).Padding(6, 6, 6, 10).Gap(4).AlignItems(wui.Center).Radius(8).
		Background(t.Background).Border(1, t.Border).Shadow(0, 4, 12, 0, wui.RGBA(0, 0, 0, 0.18)).Children(func() {
		wui.Icon(c, iconTerminal).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, msg).FontSize(12).TextColor(t.Text).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(msg)
		wui.Spacer(c).Grow(1)
		if iconAction(c.Key("restart"), iconRefresh, i18n.T("重新启动"), i18n.T("重新启动终端"), "", wui.Transparent) {
			a.RestartPane(r, p)
		}
		if iconAction(c.Key("close"), iconClose, i18n.T("关闭"), i18n.T("关闭终端"), "", wui.Transparent) {
			a.ClosePane(r, tab, p)
		}
	})
}

// termFind 是一格终端右上角的查找条：敲字就找（屏幕和回看），↩ 往旧的输出找下一个，⇧↩ 上一个，Esc 关掉。
func termFind(a *app.App, c *wui.Context, p *app.TermPane) {
	t := c.Theme()
	f := &p.Find
	// 重新启动过的终端要重新找。
	if f.Text != "" && p.Term.SearchStatus().Text != f.Text {
		p.Term.Search(f.Text)
	}
	wui.Row(c.Key("find")).Absolute().Top(6).Right(10).Height(32).Padding(0, 4, 0, 10).Gap(4).AlignItems(wui.Center).Radius(8).
		Background(t.Background).Border(1, t.Border).Shadow(0, 4, 12, 0, wui.RGBA(0, 0, 0, 0.18)).Children(func() {
		wui.Icon(c, iconSearch).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0)
		in := wui.TextInputBase(c.Key("q"), &f.Text).Label(i18n.T("在终端里查找")).Placeholder(i18n.T("查找")).FontSize(12.5).Width(170).
			OnTextKey(func(k wui.TextKey) bool {
				switch {
				case k.Key == wui.KeyEscape && k.Mods == 0:
					a.EndTermFind(p)
				case k.Key == wui.KeyEnter && k.Mods == wui.Shift:
					p.Term.SearchPrevious()
				default:
					return false
				}
				return true
			})
		if f.Focus {
			f.Focus = false
			in.Focus().SetTextSelection(0, len([]rune(f.Text)))
		}
		if in.Changed() {
			a.SearchTerm(p, f.Text)
		}
		if in.Submitted() {
			p.Term.SearchNext()
		}
		st := p.Term.SearchStatus()
		count, col := "", t.TextMuted
		switch {
		case f.Text == "":
		case st.Total == 0 && !st.Searching:
			count, col = i18n.T("没有结果"), t.Danger
		case st.Total > 0:
			count = i18n.Tf("%d / %d", st.Selected, st.Total)
		}
		wui.Text(c, count).FontSize(11).TextColor(col).SingleLine().MinWidth(52).TextAlign(wui.End).Margin(0, 4, 0, 0)
		iconButton(c, iconChevronUp, app.Tip(i18n.T("查找下一个（更早的输出）"), "term.findNext"), p.Term.SearchNext)
		iconButton(c, iconChevron, app.Tip(i18n.T("查找上一个"), "term.findPrev"), p.Term.SearchPrevious)
		iconButton(c, iconClose, i18n.T("关闭查找"), func() { a.EndTermFind(p) })
	})
}

// bottomTab 是底栏标题行里的一个只有图标的紧凑标签（输出、问题）：有条数时图标后面跟着数字。读屏名和提示是
// "输出 3" / "问题 2" 这样的全名。left：前面是「+」，左边也画一条分隔线。
func bottomTab(a *app.App, c *wui.Context, which int, ic *wui.SVG, label string, n int, left bool) {
	active := a.BottomTab == which
	b := panelTab(c, active).Padding(0, 10).Gap(4).Label(label)
	if left {
		b.BorderWidth(0, 1, 0, 1)
	}
	tooltip(c, b, label)
	fg := panelTabColor(c, b, active)
	b.Children(func() {
		wui.Icon(c, ic).FontSize(iconRow).TextColor(fg)
		if n > 0 {
			wui.Text(c, strconv.Itoa(n)).FontSize(12).FontWeight(500).TextColor(fg).SingleLine().FontFeatures("tnum").Margin(0, 0, textNudge, 0)
		}
	})
	if b.Clicked() {
		a.BottomTab = which
	}
}

// output 是输出栏：每条一个时间、仓库、命令（或操作）和它的输出，最新的在最下面。
func output(a *app.App, c *wui.Context) {
	t := c.Theme()
	if len(a.Output) == 0 {
		message(c, iconOutput, i18n.T("还没有输出"), app.KeyHint("git.command", "git 操作和 %s 命令的输出会记在这里", "git 操作和 git 命令的输出会记在这里"), t.TextMuted)
		return
	}
	a.OutList.Label = func(i int) string { return a.Output[i].Title }
	wui.List(c, &a.OutList, len(a.Output)).Fill().PaddingX(4).PaddingY(4).Rows(func(row wui.ListRow) {
		e := a.Output[row.Index]
		c := row.Context
		wui.Column(c).Padding(4, 8).Gap(2).Selectable().Children(func() {
			wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
				wui.Text(c, e.Time.Format("15:04:05")).Font(mono()).FontSize(10.5).TextColor(t.TextMuted).Shrink(0)
				if e.Repo != "" {
					wui.Text(c, e.Repo).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
				}
				col := t.Text
				if e.Failed {
					col = t.Danger
				}
				wui.Text(c, e.Title).Font(mono()).FontSize(11.5).FontWeight(600).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0)
			})
			if e.Text != "" {
				col := t.TextMuted
				if e.Failed {
					col = t.Danger.Alpha(0.85)
				}
				wui.Text(c, e.Text).Font(mono()).FontSize(11).TextColor(col)
			}
		})
	})
}

// newTermButton 是终端标签后面的「+」：点了用默认的 shell 开一个；右键是菜单（默认 + 每个终端配置），
// ⌥点开命令面板的 ">term "（自己的元素弹不了原生菜单）。
func newTermButton(a *app.App, c *wui.Context, r *app.Repo) {
	label := app.Tip(i18n.T("新建终端"), "term.new")
	tip, profs := label, a.St.Settings.Profiles()
	if len(profs) > 0 {
		tip += "\n" + i18n.T("右键或 ⌥点：用配置新建")
	}
	b := squareButton(c, iconPlus, label, tip, toolSize, iconTool, false, func() {})
	if b.Clicked() {
		if b.ClickModifiers()&wui.Alt != 0 && len(profs) > 0 {
			a.OpenPalette(">term ")
		} else {
			a.NewTerminal(r, "")
		}
	}
	ctxMenu(a, c, b, menuOwner{"termProfiles", r.Root}, func(m menu) { profileItems(a, m, r) })
}

// profileItems 是「+」右键菜单的几项：默认的 shell，再是设置里的每个终端配置；没有配置时最后一行灰着说去哪加。
func profileItems(a *app.App, m menu, r *app.Repo) {
	if m.Item(i18n.T("默认")).Chosen() {
		a.NewTerminal(r, "")
	}
	profs := a.St.Settings.Profiles()
	m.Separator()
	for _, p := range profs {
		if m.Item(p.Name).Chosen() {
			a.NewTerminalProfile(r, "", p.Name)
		}
	}
	if len(profs) == 0 {
		m.Item(i18n.T("在设置 → 终端 → 其他配置里加")).Disabled(true)
	}
}
