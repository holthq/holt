package ui

import (
	"slices"
	"strconv"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

var sidebarModes = []string{workspace.ModeFiles, workspace.ModeSearch, workspace.ModeGit, workspace.ModeOutline}

// sidebar 是左侧栏：顶部 文件 | 搜索 | Git | 大纲 四段切换，下面是对应的内容。
func sidebar(a *app.App, c *wui.Context) {
	// 侧栏不是卡片：直接画在窗口的材质（或"通透背景"关了时的纯色底）上
	col := wui.Column(c).Width(a.St.SidebarW).Shrink(0).MinHeight(0)
	tools := col.Hovered() || col.FocusWithin() // 文件树头部的按钮只在指针在侧栏里、或侧栏里有键盘焦点时画
	col.Children(func() {
		mode := 0
		for i, m := range sidebarModes {
			if m == a.St.SidebarMode {
				mode = i
			}
		}
		wui.Row(c).Height(headerHeight+4).Padding(0, 8).Gap(4).AlignItems(wui.Center).Shrink(0).Children(func() {
			seg := segTabs(c, &mode, seg{iconFiles, i18n.T("文件"), app.Tip(i18n.T("文件"), "view.files")},
				seg{iconSearch, i18n.T("搜索"), app.Tip(i18n.T("搜索"), "view.search")}, seg{iconBranch, "Git", app.Tip("Git", "view.git")},
				seg{iconOutline, i18n.T("大纲"), i18n.T("当前文件的大纲")}).Grow(1)
			if seg.Changed() {
				a.ShowSidebar(sidebarModes[mode])
			}
		})
		switch a.St.SidebarMode {
		case workspace.ModeSearch:
			searchView(a, c.Key("search"))
		case workspace.ModeGit:
			gitView(a, c.Key("git"))
		case workspace.ModeOutline:
			outlineView(a, c.Key("outline"))
		default:
			filesView(a, c.Key("files"), tools)
		}
	})
}

// noRepos 是列表里一个仓库都没有时侧栏的样子；空工作区（第一次用）是打开文件夹 / 工作区文件两个按钮。两种都能克隆。
func noRepos(a *app.App, c *wui.Context) {
	t := c.Theme()
	wui.Column(c).Grow(1).MinHeight(0).Justify(wui.Center).AlignItems(wui.Center).Gap(10).Padding(16).Children(func() {
		wui.Icon(c, iconFolder).FontSize(iconEmpty).TextColor(t.TextMuted.Alpha(0.6))
		if a.WsPath == "" { // 空工作区：第一次用，或最近的都打不开了
			wui.Text(c, i18n.T("没有打开的工作区")).FontSize(12).TextColor(t.TextMuted)
			if wui.Button(c, i18n.T("打开文件夹…")).Tooltip(app.Tip(i18n.T("打开文件夹"), "file.openFolder")).Clicked() {
				a.Exec("file.openFolder")
			}
			if wui.Button(c, i18n.T("打开工作区文件…")).Tooltip(i18n.T("打开 VS Code 的 .code-workspace")).Clicked() {
				a.Exec("file.openWsFile")
			}
			if wui.Button(c, i18n.T("克隆仓库…")).Tooltip(i18n.T("从地址克隆一个 Git 仓库，克隆完当工作区打开")).Clicked() {
				a.OpenClone()
			}
			return
		}
		wui.Text(c, i18n.T("列表里没有仓库")).FontSize(12).TextColor(t.TextMuted)
		if wui.Button(c, i18n.T("添加仓库…")).Tooltip(app.Tip(i18n.T("添加仓库"), "repo.add")).Clicked() {
			a.ChooseRepoDir()
		}
		if wui.Button(c, i18n.T("克隆仓库…")).Tooltip(i18n.T("从地址克隆一个 Git 仓库，克隆完加进列表")).Clicked() {
			a.OpenClone()
		}
		if len(a.St.Hidden) > 0 {
			if wui.Button(c, i18n.T("显示移除的工作区仓库")).Clicked() {
				a.ShowAllRepos()
			}
		}
	})
}

// filesView 是侧栏的文件模式：一棵树，每个仓库是根节点，下面是它的文件；底部筛选框筛所有仓库。
// 单击预览、双击 / 回车固定打开，改过的文件带状态字母，含改动的目录带个点。
// tools 是画不画头部那排按钮（VS Code 也是指针进了侧栏才出来）；不画时留一样高的空，行不跳；读着状态的转圈一直画。
func filesView(a *app.App, c *wui.Context, tools bool) {
	t := c.Theme()
	wui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if a.St.Settings.ShowOpenEditors {
			openEditors(a, c.Key("open"), tools)
		}
		wui.Row(c).Padding(0, 8, 2, 14).Gap(2).AlignItems(wui.Center).Shrink(0).Children(func() {
			wui.Text(c, i18n.T("仓库")).FontSize(sideSub).FontWeight(600).TextColor(t.TextMuted).Grow(1)
			if !tools {
				wui.Box(c).Size(0, toolSize).Shrink(0)
				if a.AnyLoading() {
					wui.Spinner(c)
				}
				return
			}
			if len(a.Repos) > 0 {
				iconButton(c, iconFilePlus, app.Tip(i18n.T("新建文件…"), "file.new"), func() { a.Exec("file.new") })
				iconButton(c, iconFolderPlus, app.Tip(i18n.T("新建文件夹…"), "file.newFolder"), func() { a.Exec("file.newFolder") })
			}
			if a.WsPath != "" { // 空工作区没有可以加进去的工作区文件
				iconButton(c, iconPlus, app.Tip(i18n.T("添加仓库…"), "repo.add"), a.ChooseRepoDir)
			}
			toolButton(c, iconLocate, app.Tip(i18n.T("定位当前文件"), "file.revealCurrent"), !a.CanRevealCurrent(), a.RevealCurrent)
			iconButton(c, iconCollapseAll, i18n.T("收起所有目录"), a.CollapseAll)
			if a.AnyLoading() {
				wui.Spinner(c)
			} else {
				iconButton(c, iconRefresh, app.Tip(i18n.T("重新读取所有仓库的 git 状态"), "git.refresh"), func() { a.Exec("git.refresh") })
			}
		})
		cloneRows(a, c)
		if len(a.Repos) == 0 {
			noRepos(a, c)
			return
		}
		rows := a.TreeRows()
		switch {
		case len(rows) == 0 && a.Filtering():
			message(c, iconSearch, i18n.T("没有匹配的文件"), "", t.TextMuted)
		default:
			a.FileList.Key = func(i int) any { return rows[i].Key() }
			a.FileList.Label = func(i int) string {
				row := rows[i]
				switch {
				case row.Edit && a.Edit != nil:
					return a.Edit.Label()
				case row.Root:
					return i18n.Tf("%s（仓库）", row.Repo.Name) // 名字在前：打字跳到仓库行
				case row.Note != "":
					return row.Note
				}
				return row.FindLabel() // 合并行念 "internal / ui"；去掉开头的点，打 git 跳到 .gitignore
			}
			a.FileList.Selected = &a.FileSel
			a.FileList.Selection = &a.FileChosen // ⌘点、⇧点、⌘A、拖着选多行
			a.FileList.DragSelect = true
			// 右键不改选中：列表替我们选中时和点击分不开，会把那个文件预览出来。没选中的那行的菜单只对它（MenuRows）
			a.FileList.MenuKeepsSelection = true
			a.FileHover = -1
			a.TreeDragging = false // 行在拖的话画的时候再设上（下一帧 FollowCurrent 看）
			if a.TreeSpans == nil {
				a.TreeSpans = map[int]wui.Rect{}
			}
			clear(a.TreeSpans)
			want, set := a.TakeTreeSel()
			var drag *app.TreeDrag // 选中的行拖起来带着的东西，这一帧算一次（每个选中的行都一样）
			dragOf := func(tr app.TreeRow) app.TreeDrag {
				if drag == nil {
					d := a.TreeDragOf(tr)
					drag = &d
				}
				return *drag
			}
			g := guidesFor(a)
			var list wui.Element
			box := wui.Column(c).Grow(1).MinHeight(0).Clip() // 吸顶栈叠在列表上面
			box.Children(func() {
				list = sideList(a, c, &a.FileList, len(rows), func(row wui.ListRow) { treeRow(a, row, rows[row.Index], dragOf, g) }).
					PopoverMenu(func(m *wui.Menu) { treeMenu(a, m) }). // 行下面的空白处
					HandleInput(treeCommands(a))
				stickyStack(a, c, rows, box.Bounds())
			})
			if a.FileHover < 0 {
				a.TreeTip.Leave() // 指针不在任何一行上：下回停在哪行都从头等
			}
			if a.EndTreeDrop() {
				c.AnimationFrame() // 拖着停够了展开的目录：指针不动也画出来
			}
			if set {
				a.FileSel = want // 列表按键把选中挪回了旧的那项时，以应用选的为准
			}
			if list.Clicked() {
				a.ClearTreeSel() // 点在行下面的空白处（行自己接点击，列表只接到空白处的）
			}
			if list.Changed() {
				a.TreeTip.Dismiss()              // 点了一行：它的提示收掉，指针离开前不再出
				a.TreeListChanged(c.Modifiers()) // ⇧点时锚点可能是应用记的
				// ⌥点在侧边打开。⌥点已经单独选中的那行列表不报 Changed，那时用 ⌥回车或右键
				if c.Modifiers()&(wui.Cmd|wui.Shift) == 0 && a.FileHover == a.FileSel {
					a.NoteTreeClick(a.FileSel, c.Now())
				}
				a.ClickTreeRow(false, c.Modifiers()&wui.Alt != 0)
			}
			a.ApplyTreeClick() // 选中的行自己接的点击（它可拖，列表接不到）
			if list.Submitted() {
				a.ClickTreeRow(true, false)
			}
			// 键从注册表读（keybindings.json 改过的跟着变）；⏎ 是列表自己的（Submitted），tree.open 改成别的键时两个都能用
			fl := &a.FileList.Handle
			onKey(c, fl, "tree.expand", a.TreeRight)
			onKey(c, fl, "tree.collapse", a.TreeLeft)
			if mods, key, ok := app.KeyOf("tree.open"); ok && (mods != 0 || key != wui.KeyEnter) {
				fl.OnShortcut(c, mods, key, func() { a.ClickTreeRow(true, false) })
			}
			onKey(c, fl, "tree.rename", a.RenameChosen)
			onKey(c, fl, "tree.trash", a.TrashChosen)
			onKey(c, fl, "tree.openSide", a.OpenSideChosen)
			if a.Edit == nil { // 输入名字时这几个键归输入框
				// 窗口里 ⌘C ⌘V ⌘Z 先被菜单栏"编辑"接走（treeCommands），⌘D 是菜单栏的终端分屏（RunKey 分派到 tree.duplicate）；
				// 这里接的是没有菜单栏的时候（测试）
				onKey(c, fl, "tree.copy", a.CopyChosen)
				onKey(c, fl, "tree.paste", a.PasteChosen)
				onKey(c, fl, "tree.duplicate", a.DuplicateChosen)
				onKey(c, fl, "tree.undoKey", a.UndoFile)
				if a.FileChosen.Len() > 1 {
					a.FileList.OnShortcut(c, 0, wui.KeyEscape, a.ClearTreeChosen)
				}
			}
			a.TreeFocused = a.FileList.Focused(c)
			if a.FocusTree && a.FileList.Focus(c) {
				a.FocusTree = false
			}
			if a.TreeTruncated() {
				wui.Textf(c, i18n.T("匹配太多，只显示前 %d 个"), files.FilterCap).FontSize(sideSub).TextColor(t.TextMuted).
					Padding(4, 12).Shrink(0)
			}
		}
		// 底下不留 padding：筛选框的底边和右边卡片的底边一条线（卡片下面的缝算在状态栏里）
		wui.Box(c).Padding(6, 8, 0, 8).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			field := wui.SearchField(c.Key("filter"), &a.Filter).Label(i18n.T("筛选文件")).
				Tooltip(i18n.Tf("按名字筛选所有仓库的文件；跨仓库模糊找文件用快速打开（%s）", app.Find("file.quickOpen").Keys())).FontSize(12).Fill()
			if field.Changed() {
				a.SetFilter(a.Filter)
			}
			if field.Submitted() {
				a.OpenFirstMatch()
			}
		})
	})
}

// treeCommands 接键盘在文件树上时菜单栏"编辑"的拷贝 / 粘贴 / 全选 / 撤销：窗口里 ⌘C ⌘V ⌘A ⌘Z 被这几项接走，
// 不当按键送来，只送给有焦点的元素的 HandleInput。输入名字时输入框有焦点，送不到这里。
func treeCommands(a *app.App) func(wui.InputEvent) bool {
	return func(ev wui.InputEvent) bool {
		if ev.Kind != wui.InputCommand || a.Edit != nil {
			return false
		}
		// 菜单栏"编辑"的 ⌘C ⌘V ⌘Z：对应的命令还是这个键时才归文件树（keybindings.json 改走了就不接）
		std := func(id string, key wui.Key) bool {
			mods, k, ok := app.KeyOf(id)
			return ok && mods == wui.Cmd && k == key
		}
		switch {
		case ev.Text == "copy" && std("tree.copy", wui.KeyC):
			a.CopyChosen()
		case ev.Text == "paste" && std("tree.paste", wui.KeyV):
			a.PasteChosen()
		case ev.Text == "selectAll":
			a.ChooseAllTree()
		case ev.Text == "undo" && std("tree.undoKey", wui.KeyZ):
			a.UndoFile()
		default:
			return false
		}
		return true
	}
}

// 文件树里的右键菜单（空白处 treeMenu、行 rowMenu、仓库行、钉在顶上的行、压缩目录的每一段）都用 PopoverMenu
// （mygo 画在窗口里），不用 ContextMenu（系统的 NSMenu）：mygo 弹系统菜单用的是
// popUpMenuPositioningItem:nil，左上角钉在点的地方，下面放不下时 AppKit 不往上翻，而是把菜单折起来、
// 底下出滚动箭头（issue #6）。PopoverMenu 下面放不下就往上开，窗口本身都放不下才滚。见 docs/mygo-pitfalls.md。

// treeMenu 是文件树空白处的右键菜单。
func treeMenu(a *app.App, m *wui.Menu) {
	if m.Item(i18n.T("显示被忽略的文件")).Checked(a.St.Settings.ShowIgnored).Chosen() {
		a.ToggleShowIgnored()
	}
	if m.Item(i18n.T("收起所有目录")).Chosen() {
		a.CollapseAll()
	}
	if m.Item(i18n.T("展开所有仓库有改动的目录")).Disabled(!slices.ContainsFunc(a.Repos, app.HasChanges)).Chosen() {
		a.ExpandAllChanged()
	}
	m.Separator()
	undo := i18n.T("撤销上次文件操作")
	if l := a.FileUndoLabel(); l != "" {
		undo = i18n.Tf("撤销：%s", l)
	}
	if shortcut(m.Item(undo), "tree.undoKey").Disabled(!a.CanUndoFile()).Chosen() {
		a.UndoFile()
	}
}

// treeGuides 是这一帧画缩进参考线要的：画不画（设置开着、没在筛选），选中那行的上一级（那一级的线用淡字色）。
type treeGuides struct {
	on     bool
	repo   *app.Repo
	parent string // 选中行的上一级目录（仓库内路径，"" 是仓库根）
	level  int    // 上一级的深度（0 是仓库行），-1 为没有选中行
}

func guidesFor(a *app.App) treeGuides {
	g := treeGuides{on: !a.St.Settings.TreeNoGuides && !a.Filtering(), level: -1}
	if sr, ok := a.TreeRowAt(a.FileSel); ok && !sr.Root && sr.Note == "" && !sr.Edit {
		top := sr.Path
		if len(sr.Chain) > 0 {
			top = sr.Chain[0]
		}
		g.repo, g.parent, g.level = sr.Repo, files.DirOf(top), sr.Depth-1
	}
	return g
}

// 侧栏（文件树、Git、搜索、大纲、打开的编辑器）的字号：sideFont 是正文（文件名、仓库名、变更、结果），
// sideSub 是次要的（分支、计数、目录、分组标题、说明），sideMono 是搜索结果里的等宽代码行。
// 次要的只比正文小两号，层级还在、又不至于小得费眼（#5：原来是 13 / 10.5–11）。
const (
	sideFont = 14
	sideSub  = 12
	sideMono = 12.5
)

// 文件树每级缩进、图标到名字的间距（照 VS Code 8 / 6，缩进取 10，参考线不挤），
// 目录 / 文件行的高（14 号字一行 16.5，上下各 3 再凑成整数，行和吸顶层都按它对齐）。
const (
	treeRowH = 24
	treeStep = 10
	treeGap  = 6
)

// treeIndent 是第 depth 级（1 是仓库下面第一级）的行左边的缩进，箭头（16 宽）从这里开始。
func treeIndent(depth int) float32 { return float32(6 + (depth+1)*treeStep) }

// guideX 是第 k 级（0 是仓库行）的缩进参考线离行左边多远：那一级的箭头正中。
func guideX(k int) float32 {
	if k == 0 {
		return 6 + 8 // 仓库行左 padding 6
	}
	return treeIndent(k) + 8
}

// drawGuides 在 el（第 tr.Depth 级的一行）上画它上面各级的竖线：1 px 描边色，选中行的上一级那条用淡字色。
func drawGuides(el wui.Element, tr app.TreeRow, g treeGuides) {
	if !g.on || tr.Depth < 1 {
		return
	}
	depth, hl := tr.Depth, -1
	if g.repo == tr.Repo && (g.parent == "" || files.Under(tr.Path, g.parent) && tr.Path != g.parent) {
		hl = g.level
	}
	el.Draw(func(p *wui.Painter, r wui.Rect) {
		t := p.Theme()
		for k := 0; k < depth; k++ {
			col := t.Border
			if k == hl {
				col = t.TextMuted
			}
			p.Fill(wui.Rect{X: r.X + guideX(k) - 0.5, Y: r.Y, W: 1, H: r.H}, col, 0)
		}
	})
}

// treeRow 画文件树的一行：仓库根、目录、文件或说明。选中的文件 / 目录行可以拖（dragOf 是带着的东西），
// 拖到目录行或仓库行上移动；目录行和仓库行也接从访达拖进来的文件。筛选时不拖不放。g 是缩进参考线。
func treeRow(a *app.App, row wui.ListRow, tr app.TreeRow, dragOf func(app.TreeRow) app.TreeDrag, g treeGuides) {
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted, folder := t.TextMuted, t.TextMuted // 目录图标、改名框前的图标：不上强调色
	if sel {
		muted, folder = t.AccentText.Alpha(0.75), t.AccentText
	}
	filtering := a.Filtering()
	r := tr.Repo
	if tr.Edit {
		span(a, row, editRow(a, c, tr, folder, sel))
		return
	}
	if tr.Note != "" {
		span(a, row, wui.Row(c).MinHeight(treeRowH).Padding(3, 8, 3, 30).AlignItems(wui.Center).Children(func() {
			wui.Text(c, tr.Note).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		}))
		return
	}
	if tr.Root {
		repoRow(a, c, row, tr, filtering)
		return
	}
	indent := treeIndent(tr.Depth)
	if filtering {
		indent = 22
	}
	el := wui.Row(c).MinHeight(treeRowH).Padding(3, 8, 3, indent).Gap(treeGap).AlignItems(wui.Center).PopoverMenu(func(m *wui.Menu) { rowMenu(a, m, row.Index, tr, tr.Row) })
	drawGuides(el, tr, g)
	if b := span(a, row, el); b.H > 0 {
		a.TreeRowH = b.H
	}
	hov := el.Hovered()
	if hov {
		a.FileHover = row.Index
	}
	if !filtering && a.FileChosen.Has(tr.Key()) {
		// 按下归这一行了（列表接不到点击），点击记下来等列表建好再照列表的规矩做（ApplyTreeClick）
		el.Drag(dragOf(tr))
		if el.Clicked() || el.DoubleClicked() {
			a.TreeRowClicked(row.Index, el.ClickModifiers(), el.DoubleClicked(), c.Now())
			a.TreeTip.Dismiss()
		}
		if el.Dragging() {
			el.Opacity(0.5)
			a.TreeDragging = true
			switch {
			case a.TreeCopying:
				el.Cursor(wui.CursorCopy) // 拖着时光标归按下的这一行
			case a.TreeNoDrop:
				el.Cursor(wui.CursorNotAllowed)
			}
		}
	} else if tr.Sub != 0 && el.RowDoubleClicked() {
		a.OpenSubmodule(r, tr.Path)
		a.TreeTip.Dismiss()
	} else if !tr.Dir && el.RowDoubleClicked() {
		a.OpenFile(r, tr.Path, true)
		a.TreeTip.Dismiss()
	}
	if tr.Dir && !filtering && !tr.Linked && !tr.DirLink() { // 不往链接指的地方放
		treeDropTarget(a, c, el, tr)
	}
	var nameEl wui.Element // 名字（截断了的话提示里带全名）；合并行没有（hasName 为假）
	hasName := false
	var inner []wui.Element // 行里自己带提示的小图标：指针在它们上面时不出行的提示
	defer func() {
		if !hov {
			return
		}
		var tip string
		switch {
		case tr.Sub == '-':
			tip = i18n.T("子模块还没初始化：双击初始化并更新")
		case tr.Sub != 0:
			tip = subTip(r, tr.Path)
		default: // 文件要 stat 一次：只在悬停时算（RowTip 记着）
			tip = a.RowTip(tr)
		}
		full := ""
		if hasName {
			full = cutName(c, nameEl, tr.Name, sideFont)
		}
		skip := a.TreeDragging || slices.ContainsFunc(inner, wui.Element.Hovered)
		treeTip(a, c, el, tr.Key(), full, tip, indent, skip)
	}()
	el.Children(func() {
		if tr.Dir {
			chevron(c, iconTool, tr.Open, tr.Label(), muted, func() { a.ToggleDir(r, tr.Path) })
			folderGlyph(a, c, dirsOf(tr.Row), tr.Open, tr.Ignored, sel)
			nameCol := dirNameColor(a, t, tr, muted, sel)
			if len(tr.Chain) > 0 {
				chainName(c, tr, muted, nameCol, func(m *wui.Menu, seg files.Row) { rowMenu(a, m, row.Index, tr, seg) })
			} else {
				nameEl, hasName = wui.Text(c, tr.Name).FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0), true
				if nameCol != nil {
					nameEl.TextColor(*nameCol)
				}
			}
			if tr.Mark {
				inner = append(inner, wui.Box(c).Size(6, 6).Radius(3).Background(codeColor(t, 'M')).Shrink(0).Tooltip(i18n.T("里面有未提交的改动")))
			}
			if l, ok := r.Files.Link(tr.Path); ok { // 指向目录的链接：展开看里面，git 状态照文件那样跟在后面
				inner = append(inner, wui.Icon(c, iconLink).FontSize(iconRow).TextColor(muted).Shrink(0).Tooltip(app.LinkTip(l)))
				if tr.Code != 0 {
					wui.Spacer(c).Grow(1)
					codeLetter(c, t, tr.Code, sel)
				}
			}
			return
		}
		if !filtering {
			wui.Box(c).Width(16).Shrink(0)
		}
		dim := tr.Ignored || tr.Sub == '-' // 被 .gitignore 忽略的、没初始化的子模块灰显
		// 子模块：仓库图标，强调色（两种图标风格一样）
		if tr.Sub != 0 {
			col := folder
			if dim {
				col = muted
			}
			wui.Icon(c, iconSubmodule).FontSize(iconTool).TextColor(col)
		} else {
			fileGlyphSize(a, c, tr.Name, sel, dim, iconTool)
		}
		// 筛选的行：名字和后面淡字的目录里命中的字加底色（和快速打开一样）
		hl := t.Accent.Alpha(0.22)
		if sel {
			hl = t.AccentText.Alpha(0.28)
		}
		var name wui.Element
		if filtering && tr.Hits != nil {
			name = hitText(c, tr.Name, tr.Hits, len(tr.Path)-len(tr.Name), hl)
		} else {
			name = wui.Text(c, tr.Name)
		}
		name.FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		nameEl, hasName = name, true
		if dim {
			name.TextColor(muted)
		} else if tr.Code != 0 && !a.St.Settings.TreeNoStatusColors {
			statusName(t, name, tr.Code, sel)
		}
		if l, ok := r.Files.Link(tr.Path); ok { // 符号链接：名字后面一个链接图标，悬停看指向哪（双击打开的还是链接本身）
			inner = append(inner, wui.Icon(c, iconLink).FontSize(iconRow).TextColor(muted).Shrink(0).Tooltip(app.LinkTip(l)))
		}
		if dir := files.DirOf(tr.Path); filtering && dir != "" {
			hitText(c, dir, tr.Hits, 0, hl).FontSize(sideSub).TextColor(muted).MinWidth(0).Shrink(2)
		}
		if tr.Sub != 0 || tr.Code != 0 {
			wui.Spacer(c).Grow(1)
		}
		if tr.Sub != 0 {
			label := i18n.T("子模块")
			if tr.Sub == '-' {
				label = i18n.T("未初始化")
			}
			wui.Text(c, label).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0)
		}
		if tr.Code != 0 {
			codeLetter(c, t, tr.Code, sel)
		}
	})
}

// codeLetter 是文件树行末的 git 状态字母（M / A / ? …），实心选中块上（sel）是白的。
func codeLetter(c *wui.Context, t *wui.Theme, code byte, sel bool) {
	col := codeColor(t, code)
	if sel {
		col = t.AccentText
	}
	wui.Text(c, string(code)).Font(mono()).FontSize(sideSub).FontWeight(700).TextColor(col).Shrink(0)
}

// rowMenu 是文件树里文件 / 目录行的右键菜单：选了好几行、右键的是其中一行时对选中的全部（chosenMenu），
// 否则对 row（合并行里右键的那一段，或整行）。右键没选中的行（第 i 行）先只选中它（MenuSelect，不预览）。
func rowMenu(a *app.App, m *wui.Menu, i int, tr app.TreeRow, row files.Row) {
	a.MenuSelect(i)
	if rows := a.MenuRows(tr); len(rows) > 1 {
		at := tr
		at.Row = row
		chosenMenu(a, m, at, rows)
	} else {
		fileMenu(a, m, tr.Repo, row)
	}
}

// chainName 是合并行的名字 "internal / ui"：每一段自己接右键（菜单对那一段的目录），分隔的 "/" 是淡色、两边各空 3
// （用 Gap，不用 " / "：文字末尾的空格量宽时不算，画出来是 "internal /ui"）。col 不空时只给最后一段
// （这一行代表的那个目录：Mark、Ignored 说的都是它）上这个颜色。menu 是一段的右键菜单。
func chainName(c *wui.Context, tr app.TreeRow, muted wui.Color, col *wui.Color, menu func(*wui.Menu, files.Row)) {
	wui.Row(c).AlignItems(wui.Center).Gap(3).MinWidth(0).Shrink(1).Children(func() {
		for i, p := range tr.Chain {
			if i > 0 {
				wui.Text(c, "/").FontSize(sideFont).TextColor(muted).SingleLine().Shrink(0)
			}
			seg := tr.Row
			seg.Path, seg.Name, seg.Chain = p, files.BaseName(p), nil
			name := wui.Text(c.Key(i), seg.Name).FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).
				PopoverMenu(func(m *wui.Menu) { menu(m, seg) })
			if col != nil && i == len(tr.Chain)-1 {
				name.TextColor(*col)
			}
		}
	})
}

// dirsOf 是目录行 r 的每一段（合并行的 Chain，不合并就是它自己）：folderGlyph 按最深的、名字在表里的那段着色。
func dirsOf(r files.Row) []string {
	if len(r.Chain) > 0 {
		return r.Chain
	}
	return []string{r.Path}
}

// dirNameColor 是目录行名字的颜色，nil 为正文色：被忽略的淡色（muted，选中块上是淡白）；按状态着色开着时
// 里面有改动的黄（和圆点一样），实心选中块上（sel）照旧白。
func dirNameColor(a *app.App, t *wui.Theme, tr app.TreeRow, muted wui.Color, sel bool) *wui.Color {
	var col wui.Color
	switch {
	case tr.Ignored:
		col = muted
	case sel:
		return nil
	case tr.Mark && !a.St.Settings.TreeNoStatusColors:
		col = codeColor(t, 'M')
	default:
		return nil
	}
	return &col
}

// statusName 按 git 状态给文件名 name 上色（设置"按 Git 状态给文件名着色"）：删掉的灰色加删除线，别的和名字后面的字母同色；
// 实心选中块上（sel）字还是白的，只留删除线。
func statusName(t *wui.Theme, name wui.Element, code byte, sel bool) {
	col := codeColor(t, code)
	if code == 'D' {
		name.Strikethrough()
		col = t.TextMuted
	}
	if !sel {
		name.TextColor(col)
	}
}

// treeDropTarget 让 el（目录行或仓库行 tr，仓库行是仓库根目录）接住拖过来的：文件树里拖的移过来、
// 别的仓库来的或按着 ⌥ 的复制过来（DropTree），访达拖进来的复制 / 移过来（DropFiles）。拖在上面时整行淡淡的强调色，
// 停在收起的行上半秒展开它（DwellTree），松手会复制时拖着的行画复制光标（TreeDropCopy，见 treeRow）。
func treeDropTarget(a *app.App, c *wui.Context, el wui.Element, tr app.TreeRow) {
	r, dir := tr.Repo, tr.Path
	if tr.Root {
		dir = ""
	}
	over, ok := wui.DragOver[app.TreeDrag](el)
	if ok {
		alt := c.Modifiers()&wui.Alt != 0
		ok = over.Repo != nil && !app.DropInside(over, r, dir) && // 不是拖进它自己里面
			(!over.Linked() || app.DropCopies(over, r, alt)) // 链接目录里面的只能复制出来
		if ok && app.DropCopies(over, r, alt) {
			a.TreeDropCopy = true
		}
	}
	if ok || el.FileDragOver() {
		el.Background(c.Theme().Accent.Alpha(0.12))
		if wait := a.DwellTree(tr, c.Now()); wait > 0 {
			c.After(wait)
		}
	}
	if d, ok := wui.Drop[app.TreeDrag](el); ok {
		a.DropTree(d, r, dir, c.Modifiers()&wui.Alt != 0)
	}
	if paths := el.DroppedFiles(); len(paths) > 0 {
		a.DropFiles(paths, r, dir)
	}
}

// editRow 是文件树里的输入框那一行：新建文件 / 文件夹的名字，或重命名。回车提交，Esc 取消
// （焦点回到文件树），焦点离开也取消；名字不行就在下面用红字说为什么。这一行可能是选中行（重命名，sel 为画在实心选中块上），
// 输入框的字明确用正文色，不跟着选中行变白。图标跟着敲的名字换（彩色底块）；线形风格是 icon 色的线框。
func editRow(a *app.App, c *wui.Context, tr app.TreeRow, icon wui.Color, sel bool) wui.Element {
	e := a.Edit
	if e == nil {
		return wui.Box(c)
	}
	t := c.Theme()
	return wui.Column(c).Padding(2, 8, 2, treeIndent(tr.Depth)).Gap(2).Children(func() {
		wui.Row(c).Gap(treeGap).AlignItems(wui.Center).Children(func() {
			wui.Box(c).Width(16).Shrink(0)
			switch {
			case e.IsDir:
				folderGlyph(a, c, []string{e.Name}, false, false, sel)
			case a.St.Settings.TreeIconsPlain:
				wui.Icon(c, iconFile).FontSize(iconTool).TextColor(icon)
			default:
				fileGlyphSize(a, c, e.Name, sel, false, iconTool)
			}
			in := wui.TextInput(c, &e.Name).Label(e.Label()).Placeholder(e.Placeholder()).FontSize(sideFont).TextColor(t.Text).Grow(1).MinWidth(0).
				OnTextKey(func(k wui.TextKey) bool {
					if k.Key != wui.KeyEscape || k.Mods != 0 {
						return false
					}
					a.CancelEdit()
					a.FocusTree = true
					return true
				})
			if e.Err != "" {
				in.BorderColor(t.Danger)
			}
			if !e.Started {
				e.Started = true
				in.Focus()
				if e.Kind == app.EditRename {
					in.SetTextSelection(0, e.Stem())
				}
			}
			switch {
			case in.Submitted():
				a.CommitEdit()
			case in.Focused():
				e.Focused = true
			case e.Focused:
				a.CancelEdit() // 点到别处去了
				return
			}
			if in.Changed() {
				e.Err = ""
			}
		})
		if e.Err != "" {
			wui.Text(c, e.Err).FontSize(sideSub).TextColor(t.Danger).Padding(0, 0, 0, 16+treeGap) // 和图标左边齐
		}
	})
}

// chevron 是目录 / 仓库行前面可以点的箭头，16 见方，图标 size（文件树 iconTool，别处 iconRow）；
// name 是目录或仓库的名字，读屏念"展开 name" / "收起 name"。
func chevron(c *wui.Context, size float32, open bool, name string, color wui.Color, toggle func()) {
	label := i18n.Tf("展开 %s", name)
	if open {
		label = i18n.Tf("收起 %s", name)
	}
	b := wui.Box(c).Size(16, 16).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Cursor(wui.CursorPointer).Label(label)
	b.Children(func() {
		ic := wui.Icon(c, iconChevronRight).FontSize(size).TextColor(color)
		if open {
			ic.Rotate(90)
		}
	})
	if b.Clicked() {
		toggle()
	}
}

// repoRow 是文件树里仓库的根节点：名字、分支、变更数（或 ✓ / ! / 转圈），右键是仓库菜单。
func repoRow(a *app.App, c *wui.Context, row wui.ListRow, tr app.TreeRow, filtering bool) {
	r := tr.Repo
	var el wui.Element
	// 外面这层接拖着的仓库行（调顺序）：一个元素只接一种拖的东西，里面那行接文件（TreeDrag）
	slot := wui.Column(c)
	slot.Children(func() {
		el = repoLine(a, c, tr, onAccent(a, row.Selected()), filtering, func(m *wui.Menu) {
			a.MenuSelect(row.Index)
			if rows := a.MenuRows(tr); len(rows) > 1 {
				chosenMenu(a, m, tr, rows)
			} else {
				repoMenu(a, m, r, true)
			}
		})
	})
	if b := span(a, row, el); b.H > 0 {
		a.TreeRootH = b.H
	}
	if el.Hovered() {
		a.FileHover = row.Index
		treeTip(a, c, el, tr.Key(), "", a.RepoTip(r), treeIndent(0), a.TreeDragging)
	}
	if !filtering && a.FileChosen.Has(tr.Key()) {
		el.Drag(app.RepoDrag{Repo: r}) // 和文件行一样：按下归这一行，点击转给 ApplyTreeClick
		if el.Clicked() || el.DoubleClicked() {
			a.TreeRowClicked(row.Index, el.ClickModifiers(), el.DoubleClicked(), c.Now())
			a.TreeTip.Dismiss()
		}
		if el.Dragging() {
			el.Opacity(0.5)
			a.TreeDragging = true
		}
	}
	if !filtering {
		treeDropTarget(a, c, el, tr)
		repoDropTarget(a, c, slot, r)
	}
}

// repoDropTarget 让仓库行 el 接拖着的别的仓库行：停在上半 / 下半画一条插入线（插到它前面 / 后面），松手才挪（MoveReq，列表画完再挪）。
// 挪不了的（同一个位置、跨了工作区文件和"添加仓库"两组）不画线，松手不动；跨组的给个提示。
func repoDropTarget(a *app.App, c *wui.Context, el wui.Element, r *app.Repo) {
	after := func() bool {
		_, y, _ := el.PointerPosition()
		return y > el.Bounds().H/2
	}
	if d, ok := wui.DragOver[app.RepoDrag](el); ok && a.CanMoveRepoNear(d.Repo, r, after()) {
		below, col := after(), c.Theme().Accent
		el.DrawOver(func(p *wui.Painter, b wui.Rect) {
			y := b.Y - 1
			if below {
				y = b.Y + b.H - 1
			}
			p.Fill(wui.Rect{X: b.X + 4, Y: y, W: b.W - 8, H: 2}, col, 1)
		})
	}
	if d, ok := wui.Drop[app.RepoDrag](el); ok && d.Repo != r {
		if below := after(); a.CanMoveRepoNear(d.Repo, r, below) {
			a.MoveReq = &app.RepoMove{Repo: d.Repo, To: a.RepoNear(d.Repo, r, below)}
		} else if a.InWsFile(d.Repo) != a.InWsFile(r) {
			a.Toast = i18n.T("工作区文件里的仓库和后来添加的各排各的")
		}
	}
}

// span 记下这一行上一帧画在哪（TreeSpans，吸顶栈看），返回它；上一帧没画的不记（H 为 0）。
func span(a *app.App, row wui.ListRow, el wui.Element) wui.Rect {
	b := el.Bounds()
	if b.H > 0 {
		a.TreeSpans[row.Index] = b
	}
	return b
}

// stickyLayer 是吸顶栈的一层：第 i 行（仓库行或展开的目录行），画在列表顶往下 y 处，高 h。
type stickyLayer struct {
	i    int
	y, h float32
}

// maxSticky 是吸顶栈最多几层：仓库 + 两级目录，再深只留最近的两级。
const maxSticky = 3

// stickyStack 是列表顶上叠着的吸顶栈（box 是列表那一块）：最上面那行看得见的行的祖先（仓库行、展开的目录行，合并行算一层），
// 滚过去了的从上往下叠着，每层一行高；点一层滚到那一行，右键是它的菜单，箭头收起它。一个仓库 / 目录的最后一行上来时把它那层
// 顶出去。筛选时不钉；不在键盘的走法里（键盘走列表里真的行），光标那行被哪层盖住时那层和下面的都不画。
// 用的是上一帧的位置（Visible、TreeSpans），滚得快时晚一帧跟上。
func stickyStack(a *app.App, c *wui.Context, rows []app.TreeRow, box wui.Rect) {
	first, last := a.FileList.Visible()
	if a.Filtering() || first < 0 || last < first || a.TreeRootH <= 0 {
		return
	}
	layers := stickyLayers(a, rows, box, first, min(last, len(rows)-1))
	if len(layers) == 0 {
		return
	}
	// 光标那行（↑ 走上去滚到顶的）在哪层底下：那层和下面的先不画，让光标那行看得见
	if sel, ok := a.TreeSpans[a.FileSel]; ok {
		for k, l := range layers {
			if a.FileSel > l.i && sel.Y < box.Y+l.y+l.h-0.5 {
				layers = layers[:k]
				break
			}
		}
	}
	t := c.Theme()
	low := 0 // 底边最低的那层（一样低的取外面的，它画在上面）往下投阴影
	for k, l := range layers {
		if l.y+l.h > layers[low].y+layers[low].h {
			low = k
		}
	}
	for k := len(layers) - 1; k >= 0; k-- { // 深的先画：被顶出去的那层滑到上一层底下
		l := layers[k]
		tr := rows[l.i]
		pin := wui.Box(c.Key(tr.Key())).Absolute().Top(l.y).Left(0).Right(0).PaddingX(4).Background(cardBg(t))
		if k == low {
			// 和编辑器的粘性滚动一样是柔和的阴影、不画硬边线：被顶住一半的那行读起来是从栈底下滚过去的。
			// 往上、往两边的那点被上面的层和列表的 Clip 盖住
			x, y, blur, col := stickyShadow(t)
			pin.Shadow(x, y, blur, 0, col)
		}
		pin.Children(func() {
			var el wui.Element
			if tr.Root {
				el = repoLine(a, c, tr, false, false, func(m *wui.Menu) { repoMenu(a, m, tr.Repo, true) }).
					Label(i18n.Tf("%s（仓库，钉在顶上）", tr.Repo.Name))
			} else {
				el = stickyDir(a, c, tr)
			}
			el.Cursor(wui.CursorPointer)
			if el.Clicked() {
				a.FileList.ScrollTo(stickyTarget(a, rows, l), wui.Start)
			}
		})
	}
}

// stickyShadow 是吸顶栈底下的阴影（x、y 偏移、模糊、颜色）：浅色同编辑器的粘性滚动（sticky.go）；
// 深色底本来就暗，黑 12% 看不出来，加深到 45%。
func stickyShadow(t *wui.Theme) (x, y, blur float32, col wui.Color) {
	if t.Dark {
		return 0, 2, 6, wui.RGBA(0, 0, 0, 0.45)
	}
	return 0, 2, 6, wui.RGBA(0, 0, 0, 0.12)
}

// stickyLayers 算吸顶栈。栈满（仓库 + 两级目录）时见 stickyFull；没满时和 VS Code 的一样：列表顶往下、栈底下那条线上的那行，
// 它的祖先里比栈的最后一层深一级的那个叠上去，再看新的栈底下那行，直到那行不在栈的最后一层里面；那一行本身是展开的目录、
// 顶已经滑到线上面了也叠上去（它停在那儿）。每层被它那棵子树后面的第一行往上顶；整层被顶进列表上面的留白、或滑到上一层底下了就到此为止。
func stickyLayers(a *app.App, rows []app.TreeRow, box wui.Rect, first, last int) []stickyLayer {
	if full := stickyFull(a, rows, box, first, last); full != nil {
		return full
	}
	const pad = 2 // 列表上下的 PaddingY
	rowH := a.TreeRowH
	if rowH <= 0 {
		rowH = treeRowH
	}
	var layers []stickyLayer
	bottom := func() float32 {
		if len(layers) == 0 {
			return 0
		}
		l := layers[len(layers)-1]
		return l.y + l.h
	}
	// push 把第 i 行叠到栈底下，它的子树后面那行上来时顶着它；整层被顶进列表上面的留白、或滑到上一层底下了就不叠，报 false
	push := func(i int) bool {
		l, floor := stickyLayer{i: i, y: bottom(), h: rowH}, float32(pad)
		if rows[i].Root {
			l.h = a.TreeRootH
		}
		if len(layers) > 0 {
			floor = bottom()
		}
		if end := subtreeEnd(rows, i); end <= last {
			if b, ok := a.TreeSpans[end]; ok {
				l.y = min(l.y, b.Y-box.Y-l.h)
			}
		}
		if l.y+l.h <= floor+0.5 {
			return false
		}
		layers = append(layers, l)
		return true
	}
	for len(layers) < maxSticky { // 满了的 stickyFull 已经接走
		line := box.Y + bottom()
		j := firstBelow(a, first, last, line)
		if j < 0 {
			break
		}
		chain := ancestors(rows, j)
		if tr := rows[j]; (tr.Root || tr.Dir) && tr.Open && a.TreeSpans[j].Y < line-0.5 {
			chain = append(chain, j)
		}
		at := 0
		if n := len(layers); n > 0 {
			if at = slices.Index(chain, layers[n-1].i) + 1; at == 0 {
				break // 那行不在栈的最后一层里面了（最后一层正被顶着往外走）
			}
		}
		if at >= len(chain) || !push(chain[at]) {
			break
		}
	}
	return layers
}

// firstBelow 是 first..last 里第一行下沿过了 line（窗口坐标）的。
func firstBelow(a *app.App, first, last int, line float32) int {
	for i := first; i <= last; i++ {
		if b, ok := a.TreeSpans[i]; ok && b.Y+b.H > line+0.5 {
			return i
		}
	}
	return -1
}

// stickyFull 是栈满时的吸顶栈（不满、或仓库行还没滚上去报 nil）。栈满时底在 L（仓库那层 + 两行），看两条线：L 往上一行那条线上的行 p、
// L 那条线上的行 r。停稳的栈是 p 的上下文（它的祖先，p 是展开的目录就连它自己）截成仓库 + 最近的两级，再往外的目录接着往仓库那层后面排；
// r 从 L 往上滑一行的这段里，从 p 的上下文的样子按比例变成 r 的上下文的样子：两边都有的几层挪到新位置（往里走一级时整列往上，
// 退出来时整列往下、退回来的祖先从仓库那层后面滑出来）；退出去的几层收到"两边都有的最深那层"底下（还在最深那格的原地被盖住，
// 别的和 VS Code 一样被 r 顶上去）；r 是展开的目录时先不钉，真的那行看得见，滑到位了下一帧它就是 p、钉在那儿。
// 一行接一行都是这样接上的，所以哪一帧都不跳；过渡时被盖着的那层、从仓库那层后面露出来的一截加起来可能有四层，但整个栈不过三行高。
func stickyFull(a *app.App, rows []app.TreeRow, box wui.Rect, first, last int) []stickyLayer {
	rowH, top := a.TreeRowH, a.TreeRootH
	if rowH <= 0 || top <= 0 {
		return nil
	}
	L := top + 2*rowH
	// ctx 是第 i 行的上下文：它的祖先；它自己是展开的、有子行的目录（仓库）而且顶过了 line 也算上
	ctx := func(i int, line float32) []int {
		c := ancestors(rows, i)
		if tr := rows[i]; (tr.Root || tr.Dir) && tr.Open && subtreeEnd(rows, i) > i+1 && a.TreeSpans[i].Y < box.Y+line+0.5 {
			c = append(c, i)
		}
		return c
	}
	p := firstBelow(a, first, last, box.Y+L-rowH)
	if p < 0 {
		return nil
	}
	f := ctx(p, L-rowH)
	if len(f) < maxSticky || !rows[f[0]].Root {
		return nil
	}
	if b, ok := a.TreeSpans[f[0]]; ok && b.Y >= box.Y-0.5 {
		return nil // 仓库行还没滚上去：一层都不钉
	}
	// slots 是停稳的栈里每层的 y：仓库在 0，最近的两级目录在仓库底下两格，再往外的接着往上排（被仓库那层盖着）
	slots := func(c []int) map[int]float32 {
		ys := map[int]float32{c[0]: 0}
		n := len(c) - 1
		for k := 1; k <= n; k++ {
			ys[c[k]] = top + float32(k-max(1, n-1))*rowH
		}
		return ys
	}
	to, t := f, float32(0)
	if r := firstBelow(a, first, last, box.Y+L); r >= 0 && r != p {
		to = ctx(r, L)
		t = min(max((box.Y+L-a.TreeSpans[r].Y)/(rowH-0.5), 0), 1)
	}
	common := 0
	for common < len(f) && common < len(to) && f[common] == to[common] {
		common++
	}
	if common < 2 { // 退到只剩仓库（或到了别的仓库）：接着的不满的栈里还留着最外那级目录，顶在仓库底下
		to, common = f[:2], 2
	}
	yf, yt := slots(f), slots(to)
	floor := max(yt[f[common-1]], top)
	out := []stickyLayer{{i: f[0], y: 0, h: top}}
	for k := 1; k < len(f); k++ {
		from, end := yf[f[k]], min(yf[f[k]], floor) // 退出去的收到两边都有的最深那层底下
		if k < common {
			end = yt[f[k]]
		}
		if y := from + (end-from)*t; y+rowH > top+0.5 { // 整个在仓库那层后面的不画
			out = append(out, stickyLayer{i: f[k], y: y, h: rowH})
		}
	}
	return out
}

// ancestors 是第 i 行的祖先（仓库行、目录行），从外到里；前面那行深一级的就是上一级（行是先序排的）。
// 中间碰上不是目录的（说明行之类，不该有）就当没有。
func ancestors(rows []app.TreeRow, i int) []int {
	var out []int
	for d := rows[i].Depth; !rows[i].Root && d > 0; {
		j := i - 1
		for j >= 0 && rows[j].Repo == rows[i].Repo && rows[j].Depth >= d {
			j--
		}
		if j < 0 || rows[j].Repo != rows[i].Repo || rows[j].Depth != d-1 || !rows[j].Root && !rows[j].Dir {
			return nil
		}
		out = append(out, j)
		i, d = j, d-1
	}
	slices.Reverse(out)
	return out
}

// subtreeEnd 是第 i 行（仓库或目录）那棵子树后面的第一行：深度不比它深的、或别的仓库的；一直到底是 len(rows)。
func subtreeEnd(rows []app.TreeRow, i int) int {
	j := i + 1
	for j < len(rows) && rows[j].Repo == rows[i].Repo && !rows[j].Root && rows[j].Depth > rows[i].Depth {
		j++
	}
	return j
}

// stickyTarget 是点吸顶栈的第 l 层时滚到顶上的那行：仓库行就是它自己（滚到顶上它就不钉了）；
// 目录行往前数上面几层那么高的行，让它落在上面几层底下、正好露出来（行高按上一帧的，落得低一点也不要紧）。
func stickyTarget(a *app.App, rows []app.TreeRow, l stickyLayer) int {
	i, above := l.i, float32(0)
	for i > 0 && above < l.y-0.5 {
		i--
		if rows[i].Root {
			above += a.TreeRootH
		} else {
			above += max(a.TreeRowH, 1)
		}
	}
	return i
}

// stickyDir 画吸顶栈里一个目录那层：和列表里的目录行一样的缩进、箭头（收起它）、图标、名字和圆点，不画参考线；右键是它的菜单。
func stickyDir(a *app.App, c *wui.Context, tr app.TreeRow) wui.Element {
	t := c.Theme()
	muted := t.TextMuted
	el := wui.Row(c).MinHeight(treeRowH).Padding(3, 8, 3, treeIndent(tr.Depth)).Gap(treeGap).AlignItems(wui.Center).
		PopoverMenu(func(m *wui.Menu) { fileMenu(a, m, tr.Repo, tr.Row) }).Label(i18n.Tf("%s（钉在顶上）", tr.Label()))
	el.Children(func() {
		chevron(c, iconTool, true, tr.Label(), muted, func() { a.ToggleDir(tr.Repo, tr.Path) })
		folderGlyph(a, c, dirsOf(tr.Row), true, tr.Ignored, false)
		col := dirNameColor(a, t, tr, muted, false)
		if len(tr.Chain) > 0 {
			chainName(c, tr, muted, col, func(m *wui.Menu, seg files.Row) { fileMenu(a, m, tr.Repo, seg) })
		} else if name := wui.Text(c, tr.Name).FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0); col != nil {
			name.TextColor(*col)
		}
		if tr.Mark {
			wui.Box(c).Size(6, 6).Radius(3).Background(codeColor(t, 'M')).Shrink(0)
		}
	})
	return el
}

// repoLine 画仓库行的内容（列表里的和钉在顶上的共用）：箭头、名字、分支、变更数（或 ✓ / ! / 转圈）。sel 是画在实心选中块上。
func repoLine(a *app.App, c *wui.Context, tr app.TreeRow, sel, filtering bool, menu func(*wui.Menu)) wui.Element {
	t := c.Theme()
	r, open := tr.Repo, tr.Open
	current := r == a.Current()
	muted, mark, ok := t.TextMuted, t.TextMuted, t.Success // mark：变更数的胶囊（灰）
	if sel {
		muted, mark, ok = t.AccentText.Alpha(0.75), t.AccentText, t.AccentText
	}
	el := wui.Row(c).Padding(5, 8, 5, 6).Gap(6).AlignItems(wui.Center).PopoverMenu(menu)
	el.Children(func() {
		if filtering {
			wui.Box(c).Width(16).Shrink(0)
		} else {
			chevron(c, iconTool, open, r.Name, muted, func() { a.ToggleRepo(r) })
		}
		name := wui.Text(c, r.Name).FontSize(sideFont).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		if !current && !sel {
			name.TextColor(t.Text.Alpha(0.85))
		}
		switch {
		case r.Err != nil:
			wui.Text(c, i18n.T("git status 失败")).FontSize(sideSub).TextColor(muted).SingleLine().MinWidth(0)
		case r.Plain:
			wui.Text(c, i18n.T("不是 Git 仓库")).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		case r.Loaded:
			wui.Row(c).Gap(3).AlignItems(wui.Center).MinWidth(0).Shrink(2).Children(func() {
				wui.Icon(c, iconBranch).FontSize(iconInline).TextColor(muted)
				b := r.Status.Branch
				if s := app.UpDown(r.Status); s != "" {
					b += " " + s
				}
				wui.Text(c, b).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
			})
		}
		if r.Linked {
			linkedTag(c, r, sel)
		}
		wui.Spacer(c).Grow(1)
		switch {
		case r.Busy != "" || (r.Loading && !r.Loaded):
			wui.Spinner(c)
		case r.Err != nil:
			pill(c, "!", t.Danger, sel).Tooltip(r.Err.Error())
		case r.Status.Count > 0:
			pill(c, strconv.Itoa(r.Status.Count), mark, sel).Tooltip(i18n.T("与 git status --porcelain 的行数一致"))
		case r.Loaded && !r.Plain:
			wui.Icon(c, iconCheck).FontSize(iconRow).TextColor(ok.Alpha(0.7)).Tooltip(i18n.T("工作区干净"))
		}
	})
	return el
}
