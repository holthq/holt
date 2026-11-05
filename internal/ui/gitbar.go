package ui

import (
	"path"
	"slices"
	"strconv"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// gitView 是侧栏的 Git 模式：工作区里所有仓库同时列出，一个仓库一段。段头是仓库名、分支、
// 各组的计数和（悬停时）"全部暂存"，滚动时贴在顶上；段里按冲突 / 已暂存 / 未暂存 / 未跟踪 / 贮藏分组，
// 每组点标题收起，段底是这个仓库的提交框。点一项在主区的预览 Diff 标签里看（当前仓库切到它），双击 / 回车固定。
func gitView(a *app.App, c *wui.Context) {
	t := c.Theme()
	if len(a.Repos) == 0 {
		noRepos(a, c)
		return
	}
	wui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		wui.Row(c).Padding(0, 8, 2, 14).Gap(2).AlignItems(wui.Center).Shrink(0).Children(func() {
			wui.Text(c, i18n.T("变更")).FontSize(sideSub).FontWeight(600).TextColor(t.TextMuted)
			if n := a.TotalChanges(); n > 0 {
				wui.Text(c, strconv.Itoa(n)).FontSize(sideSub).TextColor(t.TextMuted.Alpha(0.7)).Margin(0, 0, 0, 4)
			}
			wui.Spacer(c).Grow(1)
			if a.AllGitOpen() {
				iconButton(c, iconCollapseAll, i18n.T("收起所有仓库"), func() { a.SetAllGitOpen(false) })
			} else {
				iconButton(c, iconExpandAll, i18n.T("展开所有仓库"), func() { a.SetAllGitOpen(true) })
			}
			pull := strings.Join(app.PullArgs(a.St.Settings.PullMode), " ")
			squareButton(c, iconFetch, i18n.T("全部抓取"), app.Tip(i18n.T("全部抓取：每个仓库 git fetch --prune，一个跑完再下一个"), "git.fetchAll"),
				toolSize, iconTool, false, func() { a.Exec("git.fetchAll") })
			squareButton(c, iconPull, i18n.T("全部拉取"), app.Tip(i18n.Tf("全部拉取：每个有上游的仓库 git %s，一个跑完再下一个", pull), "git.pullAll"),
				toolSize, iconTool, false, func() { a.Exec("git.pullAll") })
			if a.AnyLoading() {
				wui.Spinner(c)
			} else {
				iconButton(c, iconRefresh, app.Tip(i18n.T("重新读取所有仓库的 git 状态"), "git.refresh"), func() { a.Exec("git.refresh") })
			}
		})
		a.SyncWtAuto()
		needYouBar(a, c)
		rows := a.GitRows()
		a.ChangeList.Key = func(i int) any { return rows[i].Key() }
		a.ChangeList.Header = func(i int) bool { return rows[i].Kind == app.RowRepo }
		a.ChangeList.Label = func(i int) string {
			cr := rows[i]
			switch cr.Kind {
			case app.RowRepo:
				return i18n.Tf("%s（仓库）", cr.Repo.Name)
			case app.RowChange:
				return cr.Change.Path
			case app.RowCommit:
				return i18n.Tf("%s 的提交框", cr.Repo.Name)
			case app.RowStash:
				return cr.Stash.Ref + " " + cr.Stash.Message
			case app.RowStashFile:
				return cr.Change.Path
			case app.RowWorktree:
				return i18n.Tf("工作树 %s", wtTitle(cr.Wt))
			case app.RowWtSummary:
				return i18n.Tf("%s 的摘要", wtTitle(cr.Wt))
			case app.RowWtFile:
				return cr.Change.Path
			case app.RowWtTerm:
				return i18n.Tf("%s 的终端", wtTitle(cr.Wt))
			}
			return cr.Title
		}
		a.ChangeList.Selected = &a.GitSel
		a.ChangeList.Selection = &a.GitChosen // ⌘点、⇧点、⌘A、拖着选多行
		a.ChangeList.DragSelect = true
		// 右键不改选中：列表替我们选中时和点击分不开，会把那一项预览出来。没选中的那行的菜单只对它（MenuChanges）
		a.ChangeList.MenuKeepsSelection = true
		a.GitHover = -1
		// 行内按钮的动作等列表建完再做（开文件会换当前标签、挪选中）
		var later func()
		g := gitFrame{do: func(fn func()) { later = fn }}
		list := sideList(a, c, &a.ChangeList, len(rows), func(row wui.ListRow) { gitRow(a, row, rows[row.Index], &g) })
		if later != nil {
			later()
		}
		if list.Changed() {
			a.OnChangeSelected(false)
			c.Invalidate() // 点在分组标题上时选中又退回去了，再画一帧
		}
		if list.Submitted() {
			a.OnChangeSelected(true)
		}
		// Esc 只在列表自己有焦点、选了好几行时收回多选：焦点在行里的提交框上时 Esc 归提交框（收起空的提交框）
		if a.GitChosen.Len() > 1 && a.ChangeList.Focused(c) {
			a.ChangeList.OnShortcut(c, 0, wui.KeyEscape, a.ClearGitChosen)
		}
		if a.ChangeList.Focused(c) { // ← → 收起 / 展开未跟踪目录；焦点在提交框上时归提交框
			a.ChangeList.OnShortcut(c, 0, wui.KeyLeft, func() { a.FoldGitDir(false) })
			a.ChangeList.OnShortcut(c, 0, wui.KeyRight, func() { a.FoldGitDir(true) })
			// 空格暂存 / 取消暂存、⌘⌫ 丢弃：作用于选中的变更（多选是全部），和行内按钮一样
			onKey(c, &a.ChangeList.Handle, "changes.toggleStage", func() { a.Exec("changes.toggleStage") })
			onKey(c, &a.ChangeList.Handle, "changes.discard", func() { a.Exec("changes.discard") })
		}
	})
}

// gitFrame 是画一帧 Git 栏时各行共用的：do 把行内按钮的动作留到列表建完再做。
type gitFrame struct {
	do func(func())
}

// showActions 是这一行出不出行内按钮：指针在上面，或列表有键盘、它是选中的（键盘用户也看得到能做什么）。
// 列表的焦点要在建行时读（列表建完之前读不到这一帧的）。
func showActions(a *app.App, row wui.ListRow, hover bool) bool {
	return hover || row.Selected() && a.ChangeList.Focused(row.Context)
}

// gitRow 画 Git 栏的一行。
func gitRow(a *app.App, row wui.ListRow, cr app.ChangeRow, g *gitFrame) {
	switch cr.Kind {
	case app.RowRepo:
		repoHeader(a, row.Context, cr.Repo)
	case app.RowGroup:
		groupRow(a, row, cr)
	case app.RowChange:
		if cr.Change.Dir {
			dirRow(a, row, cr, g)
		} else {
			changeRow(a, row, cr, g)
		}
	case app.RowNote:
		noteRow(a, row, cr)
	case app.RowCommit:
		commitRow(a, row, cr.Repo)
	case app.RowStashes:
		stashGroupRow(a, row, cr)
	case app.RowStash:
		stashRow(a, row, cr.Repo, cr.Stash)
	case app.RowStashFile:
		stashFileRow(a, row, cr)
	case app.RowWorktrees:
		worktreesHeader(a, row, cr)
	case app.RowWorktree:
		worktreeRow(a, row, cr.Repo, cr.Wt, g)
	case app.RowWtSummary:
		wtSummaryRow(a, row, cr, g)
	case app.RowWtFile:
		wtFileRow(a, row, cr)
	case app.RowWtTerm:
		wtTermRow(a, row, cr, g)
	case app.RowOngoing:
		ongoingRow(a, row, cr)
	}
}

// kindColor 是一组变更的颜色：段头的计数、分组标题前的点用同一个。
func kindColor(t *wui.Theme, k git.Kind) wui.Color {
	switch k {
	case git.Conflict:
		return t.Danger
	case git.Staged:
		return t.Success
	case git.Unstaged:
		return t.Warning
	}
	return t.TextMuted
}

// repoHeader 是一个仓库段的段头：箭头、仓库名、分支和领先 / 落后、（收起时）贮藏条数，右边是各组的计数
// （干净的写"工作区干净"），指针在上面时还有"全部暂存"，在读状态或跑 git 操作时转圈。点它展开 / 收起并切成
// 当前仓库，右键是仓库菜单。它会贴在列表顶上，要有底色。
func repoHeader(a *app.App, c *wui.Context, r *app.Repo) {
	t := c.Theme()
	current := r == a.Current()
	open := r.GitOpen()
	// 段头贴在顶上，要不透明的底挡住滚上来的行：不通透时和窗口同色（柔和底再画上同一片色斑），开着材质时是一条浅色带
	bg := t.Surface
	if !c.Vibrancy() {
		bg = windowBg(t)
	}
	el := wui.Row(c).Height(34).Padding(0, 4, 0, 2).Gap(6).AlignItems(wui.Center).Background(bg).Cursor(wui.CursorPointer).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.6)).ContextMenu(func(m *wui.Menu) { repoMenu(a, nativeMenu(m), r, false) })
	underBackdrop(a, c, el)
	hover := el.Hovered()
	if el.Clicked() {
		a.ToggleGitRepo(r)
		a.UseRepo(r)
	}
	el.Children(func() {
		if r.Plain { // 普通文件夹只有段头，灰的、不能展开
			wui.Box(c).Width(16).Shrink(0)
		} else {
			chevron(c, iconRow, open, r.Name, t.TextMuted, func() { a.ToggleGitRepo(r) })
		}
		name := wui.Text(c, r.Name).FontSize(repoFont).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		switch {
		case r.Plain:
			name.TextColor(t.TextMuted)
		case !current:
			name.TextColor(t.Text.Alpha(0.85))
		}
		if r.Loaded && r.Err == nil && !r.Plain {
			wui.Row(c).Gap(3).AlignItems(wui.Center).MinWidth(0).Shrink(2).Children(func() {
				wui.Icon(c, iconBranch).FontSize(iconInline).TextColor(t.TextMuted)
				wui.Text(c, r.Status.Branch).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
			})
		}
		if r.Linked {
			linkedTag(c, r, false)
		}
		if op := r.Extra.Ongoing; op != git.NoOp {
			pill(c, i18n.Tf("%s中", op.String()), t.Warning, false).
				Tooltip(i18n.Tf("正在%s：看下面的横幅", op.String()))
		}
		if n := len(r.Extra.Stashes); n > 0 && !open {
			tip := i18n.Tf("%s 条贮藏", strconv.Itoa(n))
			wui.Row(c).Gap(2).AlignItems(wui.Center).Shrink(0).Tooltip(tip).Label(tip).Children(func() {
				wui.Icon(c, iconArchive).FontSize(iconInline).TextColor(t.TextMuted)
				wui.Text(c, strconv.Itoa(n)).FontSize(sideSub).TextColor(t.TextMuted).SingleLine()
			})
		}
		if n := len(r.Worktrees); n > 0 && !open {
			tip := i18n.Tf("%s 个工作树", strconv.Itoa(n))
			wui.Row(c).Gap(2).AlignItems(wui.Center).Shrink(0).Tooltip(tip).Label(tip).Children(func() {
				wui.Icon(c, iconWorktree).FontSize(iconInline).TextColor(t.TextMuted)
				wui.Text(c, strconv.Itoa(n)).FontSize(sideSub).TextColor(t.TextMuted).SingleLine()
			})
		}
		wui.Spacer(c).Grow(1)
		st := r.Status
		switch {
		case r.Running() != nil || len(r.Queue) > 0:
			queueButton(a, c, r)
		case r.Loading:
			wui.Spinner(c)
		case r.Err != nil:
			pill(c, "!", t.Danger, false).Tooltip(r.Err.Error())
		case !r.Loaded:
		case r.Plain:
			wui.Text(c, i18n.T("不是 Git 仓库")).FontSize(sideSub).TextColor(t.TextMuted).SingleLine()
		case st.Count == 0: // 干净：一个绿色圈中勾，不写字
			wui.Icon(c, iconCheck).FontSize(iconTool).TextColor(wtGreen(t)).Shrink(0).
				Label(i18n.T("工作区干净")).Tooltip(i18n.T("工作区干净"))
		default:
			counts(c, st)
		}
		if r.Loaded && r.Err == nil && !r.Plain {
			s := syncOf(a, r)
			if s.behind > 0 || s.ahead > 0 {
				ud := ""
				if s.behind > 0 {
					ud = "↓" + strconv.Itoa(s.behind)
				}
				if s.ahead > 0 {
					ud = strings.TrimSpace(ud + " ↑" + strconv.Itoa(s.ahead))
				}
				wui.Text(c, ud).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Shrink(0).FontFeatures("tnum").
					Tooltip(i18n.T("和上游比：↓ 落后、↑ 领先"))
			}
		}
		if hover && r.Loaded && st.Conflicts+st.Unstaged+st.Untracked > 0 {
			tip := app.Tip(i18n.T("全部暂存（git add -A）"), "git.stageAll")
			rowButton(c, iconPlus, tip, tip, func() { a.StageAll(r) })
		}
		if r.Loaded && r.Err == nil && !r.Plain {
			s := syncOf(a, r)
			syncButton(c, "pull", iconPull, i18n.Tf("拉取 %s", r.Name), s.pullTip, "↓", 0, s.pullOff, s.pulling, func() {
				a.UseRepo(r)
				a.Pull(r)
			})
			syncButton(c, "push", iconPush, i18n.Tf("推送 %s", r.Name), s.pushTip, "↑", 0, s.pushOff, s.pushing, func() {
				a.UseRepo(r)
				a.Push(r)
			}).ContextMenu(func(m *wui.Menu) { pushMenu(a, m, r) })
		}
	})
}

// syncButton 是段头右边的拉取 / 推送按钮（rowToolSize 高）：图标，n 不为 0 时后面跟着"↓n" / "↑n"；
// busy（这个仓库的这条在跑或在排队）时图标换成转圈、点不了。label 带仓库名（每个仓库一组，读屏和测试按它找），
// tip 和标题栏的同一个按钮一样（syncOf）。带 key：指针移上段头时前面会多出"全部暂存"，不带 key 的话
// 按下和松开时不是同一个元素，点不着。
func syncButton(c *wui.Context, key string, ic *wui.SVG, label, tip, arrow string, n int, off, busy bool, fn func()) wui.Element {
	b := wui.Row(c.Key(key)).Height(syncSize).MinWidth(syncSize).Gap(2).Label(label)
	tooltip(c, b, tip)
	fg := toolStyle(c, b, false, off)
	if n > 0 {
		b.Padding(0, 4)
	}
	b.Children(func() {
		if busy {
			wui.Spinner(c).Size(iconInline, iconInline)
		} else {
			wui.Icon(c, ic).FontSize(iconRow).TextColor(fg)
		}
		if n > 0 {
			wui.Text(c, arrow+strconv.Itoa(n)).FontSize(sideSub).FontWeight(600).TextColor(fg).SingleLine().Margin(0, 0, textNudge, 0)
		}
	})
	if b.Clicked() && !off { // 灰着也要接住这一下，不然落到段头上，把这一段收起来了
		fn()
	}
	return b
}

// counts 是段头右边的改动数徽标：一个数（所有改动），橙色；有冲突时是红的。各组几条写在悬停提示里。
func counts(c *wui.Context, st git.Status) {
	t := c.Theme()
	sum := changeSummary(st)
	color := kindColor(t, git.Unstaged)
	if st.Conflicts > 0 {
		color = kindColor(t, git.Conflict)
	}
	pill(c, strconv.Itoa(st.Count), color, false).Tooltip(sum).Label(sum)
}

// groupRow 是段里的分组标题：箭头、颜色点、组名、条数，右边是这一组的暂存 / 取消暂存 / 中止合并之类的图标按钮。
// 点标题收起 / 展开这一组。
func groupRow(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	c := row.Context
	t := c.Theme()
	r := cr.Repo
	groupHeader(a, row, cr, func() {
		switch cr.Change.Kind {
		case git.Unstaged: // 橙色铅笔
			wui.Icon(c, iconPencil).FontSize(iconRow).TextColor(kindColor(t, git.Unstaged)).Shrink(0).Label(cr.Title)
		case git.Staged: // 绿色勾选框
			wui.Icon(c, iconCheckSquare).FontSize(iconRow).TextColor(wtGreen(t)).Shrink(0).Label(cr.Title)
		default:
			wui.Box(c).Size(6, 6).Radius(3).Background(kindColor(t, cr.Change.Kind)).Shrink(0)
		}
	}, func() {
		switch cr.Change.Kind {
		case git.Conflict:
			if op := r.Extra.Ongoing; op != git.NoOp {
				rowButton(c, iconCircleX, op.AbortLabel(), i18n.Tf("%s（git %s，会先问）", op.AbortLabel(), strings.Join(op.AbortArgs(), " ")),
					func() { a.AbortOngoing(r) })
			}
		case git.Staged:
			rowButton(c, iconMinus, i18n.T("全部取消暂存"), i18n.T("全部取消暂存（git reset）"), func() { a.UnstageAll(r) })
		case git.Unstaged:
			rowButton(c, iconUndo, i18n.T("丢弃所有未暂存的改动…"), i18n.T("丢弃所有未暂存的改动…（git restore .，会先问）"), func() { a.DiscardAll(r) })
			rowButton(c, iconPlus, i18n.T("暂存所有未暂存的改动"), i18n.T("暂存所有未暂存的改动（git add -u）"), func() { a.StageTracked(r) })
		case git.Untracked:
			rowButton(c, iconPlus, i18n.T("暂存所有未跟踪文件"), i18n.T("暂存所有未跟踪文件（git add）"), func() { a.StageUntracked(r) })
		}
	})
}

// stashGroupRow 是仓库段里贮藏的分组标题，也能收起。
func stashGroupRow(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	c := row.Context
	t := c.Theme()
	groupHeader(a, row, cr, func() {
		wui.Icon(c, iconArchive).FontSize(iconInline).TextColor(t.TextMuted).Shrink(0)
	}, nil)
}

// groupHeader 是分组标题的一行：箭头、mark（颜色点或图标）、组名、条数，actions 画在右边。点整行收起 / 展开。
// 未暂存、已暂存两组不写组名（图标代替，读屏和悬停提示照旧是组名）。
func groupHeader(a *app.App, row wui.ListRow, cr app.ChangeRow, mark, actions func()) {
	r := cr.Repo
	iconOnly := cr.Kind == app.RowGroup && (cr.Change.Kind == git.Staged || cr.Change.Kind == git.Unstaged)
	foldHeader(a, row, 26, cr.Title, iconOnly, strconv.Itoa(cr.Count), r.GroupOpen(cr.Group), func() { a.ToggleGitGroup(r, cr.Group) }, mark, actions)
}

// foldHeader 是能收起的标题行（分组标题、工作树块头）：h 高，箭头、mark、标题、count（条数，可以带说明），
// actions 画在右边。点整行收起 / 展开。iconOnly 时不写标题（mark 是图标），标题只给读屏和悬停提示。
func foldHeader(a *app.App, row wui.ListRow, h float32, title string, iconOnly bool, count string, open bool, toggle, mark, actions func()) {
	c := row.Context
	t := c.Theme()
	el := wui.Row(c).Height(h).Padding(0, 4, 0, 14).Gap(5).AlignItems(wui.Center).Cursor(wui.CursorPointer)
	if el.Hovered() {
		a.GitHover = row.Index
	}
	if el.Clicked() {
		toggle()
	}
	if iconOnly {
		el.Tooltip(title)
	}
	el.Children(func() {
		chevron(c, iconRow, open, title, t.TextMuted.Alpha(0.8), toggle)
		mark()
		if !iconOnly {
			wui.Text(c, title).FontSize(sideSub).FontWeight(600).TextColor(t.TextMuted).SingleLine().Shrink(0)
		}
		wui.Text(c, count).FontSize(sideSub).TextColor(t.TextMuted.Alpha(0.7)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		wui.Spacer(c).Grow(1)
		if actions != nil {
			actions()
		}
	})
}

// stashRow 是一条贮藏：左边的箭头、名字、说明、多久以前；指针在上面时右边是应用 / 弹出 / 丢弃，右键也有。
// 点这一行（或箭头）展开 / 收起它改了的文件；选中由列表管，键盘走上来只是停在上面。
func stashRow(a *app.App, row wui.ListRow, r *app.Repo, s git.Stash) {
	c := row.Context
	t := c.Theme()
	muted := t.TextMuted
	if onAccent(a, row.Selected()) {
		muted = t.AccentText.Alpha(0.75)
	}
	open := r.StashOpen == s.Hash
	toggle := func() { a.ToggleStash(r, s) }
	el := wui.Row(c).Height(26).Padding(0, 4, 0, 16).Gap(6).AlignItems(wui.Center).
		Tooltip(s.Ref + "  " + s.Message + "\n" + s.Date.Local().Format("2006-01-02 15:04")).
		ContextMenu(func(m *wui.Menu) { stashMenu(a, nativeMenu(m), r, s) })
	hover := el.Hovered()
	if hover {
		a.GitHover = row.Index
	}
	if el.Clicked() {
		toggle()
	}
	el.Children(func() {
		chevron(c, iconRow, open, s.Ref, muted, toggle)
		wui.Text(c, strings.TrimPrefix(s.Ref, "stash")).Font(mono()).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0)
		wui.Text(c, stashTitle(s.Message)).FontSize(sideFont).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if !hover {
			wui.Text(c, git.Ago(s.Date)).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0)
			return
		}
		rowButton(c, iconStashApply, i18n.T("应用改动"), i18n.Tf("应用改动（git stash apply %s，贮藏留着）", s.Ref), func() { a.StashApply(r, s) })
		rowButton(c, iconStashPop, i18n.T("弹出"), i18n.Tf("弹出（git stash pop %s，放回后删掉这条贮藏）", s.Ref), func() { a.StashPopRef(r, s) })
		rowButton(c, iconTrash, i18n.T("丢弃这条贮藏…"), i18n.T("丢弃这条贮藏…"), func() { a.StashDrop(r, s) })
	})
}

// stashFileRow 是展开的贮藏下面的一个文件：比贮藏行多缩进一级，右边是行数；单击在预览 Diff 里看（和贮藏时的 HEAD 比），
// 双击 / 回车固定。右键先是“只应用这个文件”（选了这条贮藏里的好几个文件时是“应用选中的 N 个文件”），下面和历史里提交的文件一样。
func stashFileRow(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	r, ch := cr.Repo, cr.Change
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted := t.TextMuted
	if sel {
		muted = t.AccentText.Alpha(0.75)
	}
	el := wui.Row(c).Padding(3, 8, 3, 44).Gap(8).AlignItems(wui.Center).ContextMenu(func(m *wui.Menu) {
		files := a.StashFileRows(cr)
		label := i18n.T("只应用这个文件")
		if len(files) > 1 {
			label = i18n.Tn("应用选中的 %d 个文件", len(files))
		}
		if m.Item(label).Disabled(!slices.ContainsFunc(files, func(f git.Change) bool { return f.Code != 'D' })).Chosen() {
			a.ApplyStashFiles(r, files)
		}
		m.Separator()
		changeMenu(a, nativeMenu(m), r, ch)
	})
	if el.Hovered() {
		a.GitHover = row.Index
	}
	if el.RowDoubleClicked() {
		a.OnChangeSelected(true)
	}
	el.Children(func() {
		changeLabel(c, ch, sel, muted, sideFont)
		wui.Spacer(c).Grow(1)
		lineCounts(c, ch, sel, muted)
	})
}

// stashTitle 是贮藏一行里显示的说明：去掉 git 加的"WIP on main: 1a2b3c4 " / "On main: "，只留用户写的或那个提交的标题。
func stashTitle(msg string) string {
	for _, p := range []string{"WIP on ", "On "} {
		if rest, ok := strings.CutPrefix(msg, p); ok {
			if _, after, ok := strings.Cut(rest, ": "); ok {
				return after
			}
		}
	}
	return msg
}

// changeRow 画一项变更：状态字母、文件名、目录；右键是暂存 / 取消暂存 / 丢弃之类。展开的未跟踪目录里的文件多缩进一级，
// 目录写相对那个目录的（harness/sub/b.md 写成 b.md 和灰的 sub）。指针在上面（或列表有键盘时选中它）右边出行内按钮
// （changeActions），名字先省略号截断、按钮贴在最右边；不出按钮时放一个同高的占位，行不跳。
func changeRow(a *app.App, row wui.ListRow, cr app.ChangeRow, g *gitFrame) {
	r, ch := cr.Repo, cr.Change
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted := t.TextMuted
	if sel {
		muted = t.AccentText.Alpha(0.75)
	}
	left, shown := float32(30), ch
	if cr.InDir != "" {
		left, shown.Path = 44, strings.TrimPrefix(ch.Path, cr.InDir)
	}
	el := wui.Row(c).Padding(rowPadY, 8, rowPadY, left).Gap(8).AlignItems(wui.Center).ContextMenu(func(m *wui.Menu) {
		if rows := a.MenuChanges(cr); rows != nil {
			chosenChangeMenu(a, nativeMenu(m), rows)
			return
		}
		changeMenu(a, nativeMenu(m), r, ch)
	})
	hover := el.Hovered()
	if hover {
		a.GitHover = row.Index
	}
	if el.RowDoubleClicked() {
		a.OnChangeSelected(true)
	}
	el.Children(func() {
		changeLabel(c, shown, sel, muted, sideFont)
		if !showActions(a, row, hover) {
			wui.Box(c).Size(0, rowToolSize).Shrink(0) // 和出按钮时一样高，行不跳
			return
		}
		wui.Spacer(c).Grow(1)
		changeActions(a, c, cr, g.do)
	})
}

// rowPadY 是 Git 栏变更行、未跟踪目录行上下的留白：加上 rowToolSize 高的按钮（或同高的占位）是一行的高度。
const rowPadY = 3

// changeActions 是变更行右边的行内按钮，从左到右和 VS Code 一样：未暂存的是打开文件（删掉的不出）· 丢弃改动 · 暂存，
// 未跟踪的是打开文件 · 删除文件 · 暂存，已暂存的是打开文件 · 取消暂存，冲突是标记为已解决。指针所在的行在多选里时
// 暂存 / 取消暂存 / 丢弃作用于选中的全部（ActChanges，同右键菜单），提示里写数量；打开文件只开这一行。丢弃、删除照旧先问。
func changeActions(a *app.App, c *wui.Context, cr app.ChangeRow, do func(func())) {
	r, ch := cr.Repo, cr.Change
	if ch.Kind == git.Conflict {
		rowButton(c, iconCheck, i18n.T("标记为已解决"), i18n.T("标记为已解决（git add）"), func() { do(func() { a.MarkResolvedPath(r, ch.Path) }) })
		return
	}
	if ch.Code != 'D' && !ch.Dir {
		rowButton(c, iconFile, i18n.T("在编辑器中打开"), i18n.T("在编辑器中打开"), func() { do(func() { a.OpenFile(r, ch.Path, true) }) })
	}
	stageActions(a, c, cr, do)
}

// stageActions 是行内的丢弃 / 删除、暂存、取消暂存按钮（变更行和未跟踪目录行共用），文案同右键菜单，提示带 git 命令和快捷键。
func stageActions(a *app.App, c *wui.Context, cr app.ChangeRow, do func(func())) {
	rows := a.ActChanges(cr)
	multi := len(rows) > 1
	switch cr.Change.Kind {
	case git.Unstaged, git.Untracked:
		label, tip := i18n.T("丢弃改动…"), i18n.T("丢弃改动…（git restore，会先问）")
		if cr.Change.Kind == git.Untracked {
			label, tip = i18n.T("删除文件…"), i18n.T("删除文件…（git clean，会先问）")
		}
		if multi {
			n := app.CountChanges(rows, app.CanStage)
			label = i18n.Tn("丢弃 %d 项改动…", n)
			tip = i18n.Tn("丢弃 %d 项改动…（未暂存的 git restore，未跟踪的 git clean，会先问）", n)
		}
		rowButton(c, iconUndo, label, app.Tip(tip, "changes.discard"), func() { do(func() { a.DiscardRows(rows) }) })
		label, tip = i18n.T("暂存"), i18n.T("暂存（git add）")
		if multi {
			n := app.CountChanges(rows, app.CanStage)
			label, tip = i18n.Tn("暂存 %d 项", n), i18n.Tn("暂存 %d 项（git add）", n)
		}
		rowButton(c, iconPlus, label, app.Tip(tip, "changes.toggleStage"), func() { do(func() { a.StageChanges(rows) }) })
	case git.Staged:
		label, tip := i18n.T("取消暂存"), i18n.T("取消暂存（git restore --staged）")
		if multi {
			n := app.CountChanges(rows, app.CanUnstage)
			label, tip = i18n.Tn("取消暂存 %d 项", n), i18n.Tn("取消暂存 %d 项（git restore --staged）", n)
		}
		rowButton(c, iconMinus, label, app.Tip(tip, "changes.toggleStage"), func() { do(func() { a.UnstageChanges(rows) }) })
	}
}

// dirRow 是一个整个未跟踪的目录（git status 折叠成的一项）：箭头、文件夹、“harness/”和上级目录，右边灰字是
// 里面几个文件（还没列到是“…”，超了上限是“2000+ 个文件”）。点它展开 / 收起（不开 Diff）；右键是未跟踪的那套
// （暂存、删除都是整个目录）再加展开 / 收起；指针在上面（或列表有键盘时选中它）右边的个数换成删除 · 暂存两个按钮。
func dirRow(a *app.App, row wui.ListRow, cr app.ChangeRow, g *gitFrame) {
	r, ch := cr.Repo, cr.Change
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted := t.TextMuted
	if sel {
		muted = t.AccentText.Alpha(0.75)
	}
	open := r.DirOpen[ch.Path]
	toggle := func() { a.ToggleUntrackedDir(r, ch.Path) }
	el := wui.Row(c).Padding(rowPadY, 8, rowPadY, 12).AlignItems(wui.Center).ContextMenu(func(m *wui.Menu) {
		if rows := a.MenuChanges(cr); rows != nil {
			chosenChangeMenu(a, nativeMenu(m), rows)
			return
		}
		label := i18n.T("展开")
		if open {
			label = i18n.T("收起")
		}
		if m.Item(label).Chosen() {
			toggle()
		}
		m.Separator()
		changeMenu(a, nativeMenu(m), r, ch)
	})
	hover := el.Hovered()
	if hover {
		a.GitHover = row.Index
	}
	if el.Clicked() {
		toggle()
	}
	el.Children(func() {
		chevron(c, iconRow, open, ch.Path, muted, toggle)
		wui.Row(c).Padding(0, 0, 0, 2).Gap(8).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(func() {
			col := codeColor(t, ch.Code)
			bg, fg := col.Alpha(0.15), col
			if sel {
				bg, fg = t.AccentText.Alpha(0.22), t.AccentText
			}
			wui.Box(c).Size(18, 18).Radius(4).Background(bg).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Children(func() {
				wui.Icon(c, iconFolder).FontSize(iconInline).TextColor(fg)
			})
			pathLabel(c, ch, sel, muted, sideFont)
			wui.Spacer(c).Grow(1)
			if !showActions(a, row, hover) {
				wui.Text(c, dirCount(r, ch.Path)).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0)
				wui.Box(c).Size(0, rowToolSize).Shrink(0) // 和出按钮时一样高，行不跳
				return
			}
			stageActions(a, c, cr, g.do) // 删除 · 暂存，都是整个目录
		})
	})
}

// dirCount 是未跟踪目录那行右边的“12 个文件”：还没列到是“…”，超了上限是“2000+ 个文件”。
func dirCount(r *app.Repo, dir string) string {
	fs, ok := r.Status.DirFiles[dir]
	switch {
	case !ok:
		return "…"
	case r.Status.DirMore[dir]:
		return i18n.Tf("%d+ 个文件", len(fs))
	}
	return i18n.Tn("%d 个文件", len(fs))
}

// noteRow 是段里的一行说明：干净的仓库被展开时、git status 失败时、展开的贮藏在读或是空的。
func noteRow(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	c := row.Context
	t := c.Theme()
	color := t.TextMuted
	if cr.Repo.Err != nil {
		color = t.Danger
	}
	if onAccent(a, row.Selected()) {
		color = t.AccentText
	}
	left := float32(30)
	if cr.Stash.Hash != "" || cr.InDir != "" { // 展开的贮藏、未跟踪目录下面：和里面的文件一样缩进
		left = 44
	}
	el := wui.Row(c).Padding(4, 8, 6, left)
	if el.Hovered() {
		a.GitHover = row.Index
	}
	el.Children(func() {
		wui.Text(c, cr.Title).FontSize(sideSub).TextColor(color).MinWidth(0)
	})
}

// ongoingRow 是段头下面的进行中横幅：停在哪一步、还有没有冲突，继续 / 跳过 / 放弃（都在 app/ongoing.go）。
// 这一行不能选中；按钮的点击归按钮。
func ongoingRow(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	c := row.Context
	t := c.Theme()
	r := cr.Repo
	op, p := r.Extra.Ongoing, r.Extra.Progress
	indent := float32(iconRow + 6)
	el := wui.Column(c).Padding(6, 10).Gap(4).Background(t.Warning.Alpha(0.12)).BorderWidth(1, 0, 1, 0).BorderColor(t.Border)
	if el.Hovered() {
		a.GitHover = row.Index
	}
	el.Children(func() {
		wui.Row(c).Gap(6).AlignItems(wui.Center).Children(func() {
			wui.Icon(c, iconAlert).FontSize(iconRow).TextColor(t.Warning)
			wui.Text(c, cr.Title).FontSize(sideFont).FontWeight(600).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		})
		wui.Row(c).Gap(6).Wrap().Padding(0, 0, 0, indent).Children(func() {
			if op == git.Rebasing && p.Branch != "" && p.Onto != "" {
				wui.Text(c, p.Branch+" → "+p.Onto).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Shrink(0)
			}
			wui.Text(c, app.OngoingNote(r)).FontSize(sideSub).TextColor(t.TextMuted).MinWidth(0)
		})
		wui.Row(c).Gap(6).Padding(2, 0, 0, indent).AlignItems(wui.Center).Children(func() {
			n := r.Status.Conflicts
			tip := i18n.Tf("%s：git %s，用原来的提交信息", op.ContinueLabel(), strings.Join(op.ContinueArgs(), " "))
			if n > 0 {
				tip = i18n.Tn("还有 %d 个文件有冲突：先解决、标记为已解决，才能继续", n)
			}
			if wui.PrimaryButton(c, i18n.T("继续")).FontSize(11.5).Label(op.ContinueLabel()).Tooltip(tip).Disabled(n > 0).Clicked() {
				a.ContinueOngoing(r)
			}
			if args := op.SkipArgs(); args != nil {
				if wui.Button(c, i18n.T("跳过")).FontSize(11.5).
					Tooltip(i18n.Tf("跳过停在的这个提交（git %s，会先问）", strings.Join(args, " "))).Clicked() {
					a.SkipOngoing(r)
				}
			}
			if wui.Button(c, i18n.T("放弃")).FontSize(11.5).
				Tooltip(i18n.Tf("%s（git %s，会先问）", op.AbortLabel(), strings.Join(op.AbortArgs(), " "))).Clicked() {
				a.AbortOngoing(r)
			}
		})
	})
}

// changeLabel 是一项变更的状态字母、文件名和目录（或重命名前的路径）。
func changeLabel(c *wui.Context, ch git.Change, sel bool, muted wui.Color, size float32) {
	codeChip(c, ch.Code, sel)
	pathLabel(c, ch, sel, muted, size)
}

// pathLabel 是 changeLabel 去掉状态字母：文件名、子模块标记、目录（或重命名前的路径）。整个未跟踪的目录
// （git status 给的是 "scripts/"）名字写成 "scripts/"，后面是它的上级目录。
func pathLabel(c *wui.Context, ch git.Change, sel bool, muted wui.Color, size float32) {
	dir, base := path.Split(strings.TrimSuffix(ch.Path, "/"))
	if strings.HasSuffix(ch.Path, "/") {
		base += "/"
	}
	wui.Text(c, base).FontSize(size).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
	if ch.Sub != "" {
		tip := i18n.T("子模块：父仓库里记的是它的一次提交")
		if s := git.SubText(ch.Sub); s != "" {
			tip = i18n.Tf("子模块：%s", s)
		}
		pill(c, i18n.T("子模块"), c.Theme().TextMuted, sel).Tooltip(tip)
	}
	switch {
	case ch.Orig != "":
		wui.Text(c, "← "+ch.Orig).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
	case dir != "":
		wui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
	}
}

// lineCounts 是提交里的文件行右边的 +12 −3（diff-tree --numstat），二进制是灰的"二进制"，是 0 的一边不画。
func lineCounts(c *wui.Context, ch git.Change, onAccent bool, muted wui.Color) {
	t := c.Theme()
	if ch.Binary {
		wui.Text(c, i18n.T("二进制")).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0)
		return
	}
	add, del := t.Success, t.Danger
	if onAccent {
		add, del = t.AccentText, t.AccentText
	}
	if ch.Add > 0 {
		wui.Textf(c, "+%d", ch.Add).Font(mono()).FontSize(sideSub).FontWeight(600).TextColor(add).SingleLine().Shrink(0)
	}
	if ch.Del > 0 {
		wui.Textf(c, "−%d", ch.Del).Font(mono()).FontSize(sideSub).FontWeight(600).TextColor(del).SingleLine().Shrink(0)
	}
}

// commitFileLabel 是提交里的一个文件：changeLabel，右边是行数。
func commitFileLabel(c *wui.Context, ch git.Change, sel bool, muted wui.Color) {
	changeLabel(c, ch, sel, muted, sideFont)
	wui.Spacer(c).Grow(1)
	lineCounts(c, ch, sel, muted)
}

// lineTotals 把几个文件的行数加起来。
func lineTotals(files []git.Change) (add, del int) {
	for _, f := range files {
		add += f.Add
		del += f.Del
	}
	return add, del
}

// commitRow 是仓库段底的提交框。平时收成一行"提交…"，点开、有已暂存的改动或写了信息时展开：
// 一行到四行的信息，⌘↩ 或按钮提交这个仓库已暂存的改动；信息空着按 Esc 收起来，空着按 ↑ 翻以前写过的。
// 下面一排是"修改上一次提交"（amend）的勾选和提交按钮。
func commitRow(a *app.App, row wui.ListRow, r *app.Repo) {
	c := row.Context
	t := c.Theme()
	if !r.CommitShown() { // 刚提交完多半是收着的：能撤销时右边也放"撤销上次提交"
		wui.Row(c).AlignItems(wui.Center).Padding(0, 8, 0, 0).Children(func() {
			b := wui.Row(c).Grow(1).Padding(4, 8, 6, 24).Gap(6).AlignItems(wui.Center).Cursor(wui.CursorPointer).
				Label(i18n.Tf("提交 %s 的改动…", r.Name)).Tooltip(i18n.T("写提交信息；先暂存要提交的文件"))
			if b.Hovered() {
				a.GitHover = row.Index
			}
			if b.Clicked() {
				a.OpenCommit(r)
			}
			b.Children(func() {
				wui.Icon(c, iconPencil).FontSize(iconInline).TextColor(t.TextMuted)
				wui.Text(c, i18n.T("提交…")).FontSize(sideSub).TextColor(t.TextMuted).SingleLine()
			})
			if r.Extra.Head != "" && app.UndoWhy(r) == "" {
				undoButton(a, c, r, "")
			}
		})
		return
	}
	col := wui.Column(c).Padding(4, 8, 10, 22).Gap(6)
	if col.Hovered() {
		a.GitHover = row.Index
	}
	col.Children(func() {
		// TextArea 默认至少 80 高，MinHeight(0) 之后才真的从一行起、随内容长到四行。
		ph := i18n.T("提交信息")
		if k := app.Find("commit").Keys(); k != "" {
			ph = i18n.Tf("提交信息（%s 提交）", k)
		}
		ta := wui.TextArea(c.Key("msg"), &r.CommitMsg).Lines(1, 4).MinHeight(0).Placeholder(ph).
			Label(i18n.T("提交信息")).FontSize(sideFont).TextColor(t.Text).OnTextKey(func(k wui.TextKey) bool {
			switch {
			case (k.Key == wui.KeyUp || k.Key == wui.KeyDown) && k.Mods == 0:
				dir := 1
				if k.Key == wui.KeyDown {
					dir = -1
				}
				if msg, ok := a.StepCommitMsg(r, k.Text(), dir); ok {
					k.SetText(msg)
					return true
				}
				return false
			case keyIs(wui.InputEvent{Mods: k.Mods, Key: k.Key}, app.Find("commit")):
				msg := k.Text()
				a.Update(func() {
					r.CommitMsg = msg
					a.Commit(r)
				})
				return true
			case k.Key == wui.KeyEscape && k.Mods == 0 && k.Text() == "":
				a.Update(func() { r.CommitOpen = false })
				return true
			}
			return false
		})
		if r.FocusCommit {
			r.FocusCommit = false
			ta.Focus()
		}
		if ta.Focused() {
			a.UseRepo(r)
		}
		hint := ""
		switch {
		case r.Busy != "":
			hint = r.Busy + "…"
		case r.Amend && r.Status.Staged == 0:
			hint = i18n.T("只改上一次提交的信息")
		case r.Amend:
			hint = i18n.Tf("已暂存的 %s 个文件并进上一次提交", strconv.Itoa(r.Status.Staged))
		case r.MergeReady() && r.Status.Staged == 0:
			hint = i18n.T("冲突都解决了：提交这次合并")
		case r.Status.Staged == 0 && r.Status.Count > 0:
			hint = i18n.T("没有已暂存的改动：点分组上的 + 或右键文件暂存")
		case r.Status.Staged == 0:
			hint = i18n.T("没有可提交的改动")
		}
		if hint != "" {
			wui.Text(c, hint).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
		}
		wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
			if r.Extra.Head != "" {
				amend := r.Amend
				cb := wui.Checkbox(c, &amend, i18n.T("修改上一次提交")).FontSize(sideSub).Disabled(r.Committing()).
					Tooltip(app.Tip(i18n.T("git commit --amend：信息换成上一次的，可以改；已暂存的改动并进去"), "git.amend"))
				if cb.Changed() {
					a.SetAmend(r, amend)
				}
				undoButton(a, c, r, app.UndoWhy(r))
			}
			wui.Spacer(c).Grow(1)
			label := i18n.T("提交")
			switch {
			case r.Amend:
				label = i18n.T("修改提交")
			case r.Status.Staged > 0:
				label = i18n.Tf("提交 %s 个文件", strconv.Itoa(r.Status.Staged))
			}
			tip := app.Tip("git commit", "commit")
			if r.Amend {
				tip = app.Tip("git commit --amend", "commit")
			}
			if wui.PrimaryButton(c, label).Disabled(!r.CanCommit()).Tooltip(tip).Clicked() {
				a.UseRepo(r)
				a.Commit(r)
			}
		})
	})
}

// undoButton 是提交框里的"撤销上次提交"（git reset --soft HEAD~1）；why 不空时灰着、提示里带原因。
func undoButton(a *app.App, c *wui.Context, r *app.Repo, why string) {
	tip := i18n.T("撤销上次提交：git reset --soft HEAD~1，改动回到暂存区，信息回到提交框")
	if why != "" {
		tip = withWhy(tip, why)
	}
	squareButton(c, iconUndo, i18n.T("撤销上次提交"), tip, rowToolSize, iconRow, why != "", func() { a.UndoCommit(r) })
}
