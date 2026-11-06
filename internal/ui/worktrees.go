package ui

import (
	"path/filepath"
	"strconv"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// Git 栏仓库段里的"工作树"块：块头在段头下面、改动分组上面，展开后每个别的工作树一行。
// 每行是 Git 栏列表里能选中的一行：单击或 ↑↓ 停上去在右栏看它的分支历史，双击或回车在工作区中打开，
// 右键是锁定 / 移除 / 清理之类。

// worktreesHeader 是工作树组头（高 26）：箭头、树形图标（悬停"工作树"）、个数，不写"工作树"三个字；收起时右边放组里
// 最需要你的那一档的符号（等你 / 运行中 / 可合并，没有就不放），右边是新建和更多。
func worktreesHeader(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	c := row.Context
	t := c.Theme()
	r := cr.Repo
	open := r.WtShown()
	foldHeader(a, row, 26, cr.Title, true, strconv.Itoa(cr.Count), open, func() { a.ToggleWorktrees(r) }, func() {
		wui.Icon(c, iconWorktree).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0).Label(i18n.T("工作树")).Tooltip(i18n.T("工作树"))
	}, func() {
		if top, ok := a.WtGroupTop(r); ok && !open {
			wui.Box(c.Key("top")).Size(16, 16).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Children(func() {
				wtMark(c, top)
			})
		}
		if r.WtLoading {
			wui.Spinner(c)
		}
		squareButton(c, iconPlus, i18n.T("新建工作树…"), app.Tip(i18n.T("新建工作树…（git worktree add）"), "git.newWorktree"),
			toolSize, iconRow, r.Err != nil || !r.Loaded, func() { a.NewWorktree(r) })
		more := wui.ButtonBase(c).Label(i18n.T("工作树操作")).Size(toolSize, toolSize).Tooltip(i18n.T("更多工作树操作"))
		fg := toolStyle(c, more, false, false)
		more.Children(func() {
			wui.Icon(c, iconMore).FontSize(iconRow).TextColor(fg)
		})
		menuButton(a, c, more, menuOwner{"worktrees", r.Root}, func(m menu) { worktreesMenu(a, m, r) })
	})
}

// worktreesMenu 是工作树块头"更多"的菜单。
func worktreesMenu(a *app.App, m menu, r *app.Repo) {
	if m.Item(i18n.T("刷新工作树")).Disabled(r.Loading).Chosen() {
		a.RefreshWorktrees(r)
	}
	if m.Item(i18n.T("清理失效的工作树…")).Chosen() {
		a.PruneWorktrees(r)
	}
	m.Separator()
	label := i18n.T("展开工作树")
	if r.WtOpen {
		label = i18n.T("收起工作树")
	}
	if m.Item(label).Chosen() {
		a.ToggleWorktrees(r)
	}
}

// worktreeRow 是一个工作树（高 28，一个词都不写）：状态符号（16 的格子，wtMark）、名字、右边一个灰色的小数字
// （等了 / 跑了多久、↑N、改动数徽标、↑N ↓M）；分支、路径、主 / 已打开 / 已锁定写在悬停提示里，已打开、已锁定另有小图标。
// 已合并的整行半透明。指针在上面（或列表有键盘时选中它）右边出 22 见方的按钮：终端（有 agent 的终端）、
// 审查（可合并、有改动）、移除工作树（已合并）。单击选中并展开，双击在工作区中打开，右键是锁定 / 移除之类。
func worktreeRow(a *app.App, row wui.ListRow, r *app.Repo, w app.Worktree, g *gitFrame) {
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted := t.TextMuted
	if sel {
		muted = t.AccentText.Alpha(0.75)
	}
	ag := a.WtAgent(r, w)
	info := r.WtInfo[w.Path]
	st := app.WtStateOf(ag.State, w, info)
	el := wui.Row(c).Height(28).Padding(0, 6, 0, 26).Gap(6).AlignItems(wui.Center).Radius(6).
		Tooltip(wtTip(w, st))
	ctxMenuRow(a, c, row, el, menuOwner{"worktree", r.Root + "\x00" + w.Path}, func(m menu) { worktreeMenu(a, m, r, w) })
	hover := el.Hovered()
	if hover {
		a.GitHover = row.Index
	}
	if el.RowDoubleClicked() { // 单击由列表选中（OnChangeSelected，展开）
		a.OpenWorktree(w)
	}
	if st == app.WtMerged {
		el.Opacity(0.5)
	}
	if !sel && a.WtExpanded(r, w) && !w.Main { // 展开着：强调色 20% 的底加左边 3px 的竖条
		el.Background(t.Accent.Alpha(0.2)).BorderWidth(0, 0, 0, 3).BorderColor(t.Accent)
	} else if !sel && r.Hist.RefPath == w.Path {
		el.Background(t.Accent.Alpha(0.12))
	}
	el.Children(func() {
		wui.Box(c).Size(16, 16).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Children(func() {
			wtMark(c, st)
		})
		name := wui.Text(c, wtTitle(w)).FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		if st == app.WtWaiting {
			name.FontWeight(600)
		}
		if w.Main {
			wui.Icon(c, iconHome).FontSize(iconInline).TextColor(muted).Shrink(0).Label(i18n.T("主工作树")).Tooltip(i18n.T("主工作树"))
		}
		if a.WtRepo(w) != nil {
			tip := i18n.T("已经是工作区里的一段：双击跳过去")
			wui.Icon(c, iconLink).FontSize(iconInline).TextColor(muted).Shrink(0).Label(i18n.T("已打开")).Tooltip(tip)
		}
		if w.Locked {
			tip := i18n.T("已锁定")
			if w.LockReason != "" {
				tip = i18n.Tf("已锁定：%s", w.LockReason)
			}
			wui.Icon(c, iconLock).FontSize(iconInline).TextColor(muted).Shrink(0).Tooltip(tip).Label(tip)
		}
		wui.Spacer(c).Grow(1)
		if showActions(a, row, hover) && wtActions(a, c, r, w, st, ag, g.do) {
			return
		}
		wtTrailing(a, c, r, w, st, ag, info, sel, muted)
	})
}

// wtTrailing 是工作树一行右边灰色的小数字：等你 / 运行中是多久，可合并是 ↑N，有改动是改动数徽标，没有 agent 是 ↑N ↓M。
func wtTrailing(a *app.App, c *wui.Context, r *app.Repo, w app.Worktree, st app.WtState, ag app.AgentState, info *app.WtInfo, sel bool, muted wui.Color) {
	t := c.Theme()
	small := func(s, tip string) {
		wui.Text(c, s).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0).FontFeatures("tnum").Tooltip(tip)
	}
	switch {
	case w.Bare:
	case info != nil && info.Err != nil:
		pill(c, "!", t.Danger, sel).Tooltip(info.Err.Error())
		return
	case info == nil && r.WtLoading && st != app.WtWaiting && st != app.WtRunning:
		small("…", "")
		return
	}
	switch st {
	case app.WtWaiting:
		small(shortDur(a.Now().Sub(ag.Since())), i18n.T("等了多久"))
	case app.WtRunning:
		small(shortDur(a.Now().Sub(ag.Since())), i18n.T("跑了多久"))
	case app.WtMergeable:
		small("↑"+strconv.Itoa(info.Ahead), i18n.T("比主分支领先的提交数"))
	case app.WtDirty:
		n := strconv.Itoa(info.Dirty)
		pill(c, n, t.Warning, sel).Tooltip(i18n.Tf("%s 处未提交的改动", n)).Label(i18n.Tf("%s 处未提交的改动", n))
	case app.WtPlain:
		if info != nil {
			if s := wtUpDown(info); s != "" {
				small(s, i18n.T("和主工作树的 HEAD 比：↑ 领先、↓ 落后"))
			}
		}
	}
}

// wtActions 是指针在工作树一行上时右边的按钮（22 见方，图标加提示）：有 agent 的终端时是终端，可合并、有改动时是审查，
// 已合并时是移除工作树。画了按钮返回真（就不画右边的数字）。
func wtActions(a *app.App, c *wui.Context, r *app.Repo, w app.Worktree, st app.WtState, ag app.AgentState, do func(func())) bool {
	any := false
	if ag.Pane != nil {
		any = true
		squareButton(c.Key("term"), iconTerminal, i18n.T("跳到终端"), i18n.T("跳到这个工作树上的 agent 终端"), wtBtnSize, iconRow, false,
			func() { do(func() { a.GoToPane(ag.Repo, ag.Tab, ag.Pane) }) })
	}
	if st == app.WtMergeable || st == app.WtDirty {
		any = true
		squareButton(c.Key("review"), iconEye, i18n.T("审查"), i18n.T("审查：在主区看它比主分支多出的改动（main...分支）"), wtBtnSize, iconRow, false,
			func() { do(func() { a.ReviewWorktree(r, w) }) })
	}
	if st == app.WtMerged && !w.Main {
		any = true
		why := app.RemoveWorktreeWhy(r, w)
		tip := i18n.T("移除工作树…（分支会留着）")
		if why != "" {
			tip = i18n.Tf("移除工作树：%s", why)
		}
		squareButton(c.Key("remove"), iconTrash, i18n.T("移除工作树…"), tip, wtBtnSize, iconRow, why != "",
			func() { do(func() { a.RemoveWorktree(r, w) }) })
	}
	return any
}

// wtBtnSize 是工作树一行、摘要行上的图标按钮见方的边长。
const wtBtnSize = 22

// worktreeMenu 是一个工作树的右键菜单；主工作树只有打开、终端、访达、复制路径。
func worktreeMenu(a *app.App, m menu, r *app.Repo, w app.Worktree) {
	open := i18n.T("在工作区中打开")
	if a.WtRepo(w) != nil {
		open = i18n.T("跳到区块")
	}
	if m.Item(open).Disabled(w.Bare || w.Prunable && a.WtRepo(w) == nil).Chosen() {
		a.OpenWorktree(w)
	}
	if !w.Main && m.Item(i18n.T("查看分支历史")).Checked(r.Hist.RefPath == w.Path).Disabled(w.Head == "").Chosen() {
		a.ShowWorktreeHistory(r, w)
	}
	m.Separator()
	if m.Item(i18n.T("在此打开终端")).Disabled(w.Prunable).Chosen() {
		a.NewTerminal(r, w.Path)
	}
	if m.Item(i18n.T("在 Finder 中显示")).Disabled(w.Prunable).Chosen() {
		a.InFinder(w.Path)
	}
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(w.Path)
	}
	if w.Main {
		return
	}
	m.Separator()
	if w.Locked {
		if m.Item(i18n.T("解锁")).Chosen() {
			a.UnlockWorktree(r, w)
		}
	} else if m.Item(i18n.T("锁定…")).Chosen() {
		a.LockWorktree(r, w)
	}
	if whyItem(m, i18n.T("移除工作树…"), app.RemoveWorktreeWhy(r, w)).Danger().Chosen() {
		a.RemoveWorktree(r, w)
	}
	if w.Prunable && m.Item(i18n.T("清理")).Chosen() {
		a.PruneWorktrees(r)
	}
}

// wtTitle 是工作树一行的名字：分支，分离 HEAD 是"分离 HEAD · 短哈希"，bare 是目录名。
func wtTitle(w app.Worktree) string {
	switch {
	case w.Branch != "":
		return w.Branch
	case w.Head != "":
		return app.WtDetachedName(w.Head)
	}
	return filepath.Base(w.Path)
}

// wtUpDown 是"↑3 ↓1"：只写不为 0 的。
func wtUpDown(info *app.WtInfo) string {
	var parts []string
	if info.Ahead > 0 {
		parts = append(parts, "↑"+strconv.Itoa(info.Ahead))
	}
	if info.Behind > 0 {
		parts = append(parts, "↓"+strconv.Itoa(info.Behind))
	}
	return strings.Join(parts, " ")
}

// wtTip 是工作树一行的悬停说明：状态、分支（分离 HEAD 写短哈希）、路径、主工作树、锁定和可清理的原因。
func wtTip(w app.Worktree, st app.WtState) string {
	lines := []string{wtStateName(st)}
	if w.Branch != "" {
		lines = append(lines, i18n.Tf("分支 %s", w.Branch))
	}
	if w.Main {
		lines = append(lines, i18n.T("主工作树"))
	}
	lines = append(lines, w.Path)
	if w.Head != "" {
		lines = append(lines, "HEAD "+git.Short(w.Head))
	}
	if w.Locked {
		if w.LockReason != "" {
			lines = append(lines, i18n.Tf("已锁定：%s", w.LockReason))
		} else {
			lines = append(lines, i18n.T("已锁定"))
		}
	}
	if w.Prunable {
		lines = append(lines, i18n.Tf("可清理：%s", w.PruneReason))
	}
	return strings.Join(lines, "\n")
}

// linkedTag 是链接工作树的段头 / 根行上的"工作树"标签，悬停说主仓库在哪。
func linkedTag(c *wui.Context, r *app.Repo, onAccent bool) {
	tip := i18n.T("这是一个链接的工作树")
	if r.MainRoot != "" {
		tip = i18n.Tf("主仓库 %s", r.MainRoot)
	}
	pill(c, i18n.T("工作树"), c.Theme().TextMuted, onAccent).Tooltip(tip).Label(i18n.T("链接的工作树"))
}

// worktreeDialog 是新建 / 锁定 / 移除工作树的对话框（app.WtDialog，dialogFrame 420 宽）。
func worktreeDialog(a *app.App, c *wui.Context) {
	d := &a.WtDialog
	r := d.Repo
	if r == nil || a.RepoFor(r.Root) != r {
		a.WtDialog = app.WorktreeDialog{}
		return
	}
	var o dialogOpts
	var body, footer func()
	switch d.Kind {
	case app.WtNew:
		o, body, footer = newWorktreeDlg(a, c, r)
	case app.WtLock:
		o, body, footer = lockWorktreeDlg(a, c)
	case app.WtRemove:
		o, body, footer = removeWorktreeDlg(a, c, r)
	}
	if len(a.Repos) > 1 && d.Kind != app.WtNew { // 新建的说明里有仓库名
		o.Repo = r.Name
	}
	o.Width = 420
	dialogFrame(c, &d.Open, o, body, footer)
	if !a.WtDialog.Open {
		a.WtDialog = app.WorktreeDialog{}
	}
}

// newWorktreeDlg 是"新建工作树"：新分支（名字、起点）或已有的本地分支，目录（默认跟着分支名），建好后打开的勾。
// 名字、目录框里 ⏎ 是新建（以前只能点按钮）。
func newWorktreeDlg(a *app.App, c *wui.Context, r *app.Repo) (dialogOpts, func(), func()) {
	t := c.Theme()
	d := &a.WtDialog
	o := dialogOpts{Title: i18n.T("新建工作树"), Note: i18n.Tf("给 %s 再检出一份，在另一个目录里同时改另一个分支", r.Name), Hint: i18n.T("新建")}
	submitted := func(in wui.Element) {
		if in.Submitted() && a.WtCanCreate() {
			a.CreateWorktree()
		}
	}
	body := func() {
		mode := 1
		if d.NewBranch {
			mode = 0
		}
		if segTabs(c, &mode, seg{label: i18n.T("新分支")}, seg{label: i18n.T("已有分支")}).Label(i18n.T("分支从哪来")).Changed() {
			d.NewBranch = mode == 0
			if free := r.WtFreeBranches(); !d.NewBranch && d.Existing == "" && len(free) > 0 {
				d.Existing = free[0]
			}
			a.WtDirFollow()
		}
		if d.NewBranch {
			dlgField(c, i18n.T("分支名"), "", func() {
				in := dlgInput(c, wui.TextInput(c, &d.Name).Placeholder(i18n.T("如 wt/fix-io")).Label(i18n.T("分支名")).AutoFocus())
				if in.Changed() {
					a.WtDirFollow()
				}
				submitted(in)
			})
			head := i18n.Tf("当前 HEAD（%s）", r.Status.Branch)
			opts := []string{head}
			for _, b := range r.Branches {
				if !b.Remote {
					opts = append(opts, b.Name)
				}
			}
			sel := d.Base
			if sel == "" {
				sel = head
			}
			dlgField(c, i18n.T("起点"), "", func() {
				if wui.Select(c, &sel, opts).Label(i18n.T("起点")).FontSize(12.5).TextColor(t.Text).Changed() {
					d.Base = ""
					if sel != head {
						d.Base = sel
					}
				}
			})
		} else {
			free := r.WtFreeBranches()
			dlgField(c, i18n.T("分支"), "", func() {
				if len(free) == 0 {
					wui.Text(c, i18n.T("没有空着的本地分支：都在别的工作树里检出了")).FontSize(11).TextColor(t.TextMuted)
					return
				}
				if wui.Select(c, &d.Existing, free).Label(i18n.T("分支")).FontSize(12.5).TextColor(t.Text).Changed() {
					a.WtDirFollow()
				}
			})
		}
		dirErr := ""
		switch dir := strings.TrimSpace(d.Dir); {
		case dir == "":
		case !filepath.IsAbs(dir):
			dirErr = i18n.T("要写绝对路径")
		case a.WtDirExists():
			dirErr = i18n.T("这个目录已经有了")
		}
		dlgField(c, i18n.T("目录"), dirErr, func() {
			in := dlgInput(c, wui.TextInput(c, &d.Dir).Label(i18n.T("目录")))
			if in.Changed() {
				d.DirEdited = true
			}
			submitted(in)
		})
		wui.Checkbox(c, &d.OpenAfter, i18n.T("建好后在工作区中打开")).FontSize(12)
		if a.WtCanCreate() {
			dlgCommand(c, newWorktreeCmd(d))
		}
	}
	footer := func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			d.Open = false
		}
		if dlgButton(c, i18n.T("新建"), btnPrimary, !a.WtCanCreate(), false) {
			a.CreateWorktree()
		}
	}
	return o, body, footer
}

// newWorktreeCmd 是对话框底下灰字写的、点"新建"会跑的命令。
func newWorktreeCmd(d *app.WorktreeDialog) string {
	dir := filepath.Clean(strings.TrimSpace(d.Dir))
	if !d.NewBranch {
		return "git worktree add " + dir + " " + d.Existing
	}
	cmd := "git worktree add -b " + d.WtBranch() + " " + dir
	if d.Base != "" {
		cmd += " " + d.Base
	}
	return cmd
}

// lockWorktreeDlg 是"锁定工作树"：原因可空。
func lockWorktreeDlg(a *app.App, c *wui.Context) (dialogOpts, func(), func()) {
	d := &a.WtDialog
	o := dialogOpts{Title: i18n.Tf("锁定工作树 %s", wtTitle(d.Wt)), Hint: i18n.T("锁定"),
		Note: i18n.T("锁着的工作树清理时不会被清掉（比如它在拔掉的 U 盘上），移除时要多确认一次。")}
	body := func() {
		if dlgInput(c, wui.TextInput(c, &d.Reason).Placeholder(i18n.T("原因（可不填）")).Label(i18n.T("锁定的原因")).AutoFocus()).Submitted() {
			a.DoLockWorktree()
		}
	}
	footer := func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			d.Open = false
		}
		if dlgButton(c, i18n.T("锁定"), btnPrimary, false, false) {
			a.DoLockWorktree()
		}
	}
	return o, body, footer
}

// removeWorktreeDlg 是"移除工作树"：路径、分支、有几处改动，"同时删除分支"默认不勾；红的，⏎ 移除。
func removeWorktreeDlg(a *app.App, c *wui.Context, r *app.Repo) (dialogOpts, func(), func()) {
	t := c.Theme()
	d := &a.WtDialog
	w := d.Wt
	note := i18n.T("会删掉这个目录（git worktree remove）；分支和提交还在。")
	if info := r.WtInfo[w.Path]; info != nil && info.Dirty > 0 {
		note = i18n.Tf("这个工作树有 %s 处未提交的改动：git 会先拒绝，再问一次才强制移除，改动就丢了。", strconv.Itoa(info.Dirty))
	}
	remove := func() {
		if d.Open { // ⏎ 和点按钮同一帧只跑一次
			a.DoRemoveWorktree()
		}
	}
	o := dialogOpts{Kind: dlgDanger, Title: i18n.Tf("移除工作树 %s？", wtTitle(w)), Note: note, Hint: i18n.T("移除"), Enter: remove}
	body := func() {
		wui.Text(c, w.Path).Font(mono()).FontSize(11).TextColor(t.TextMuted).MinWidth(0)
		if w.Locked {
			wui.Text(c, i18n.T("它锁着：也要再确认一次。")).FontSize(11.5).TextColor(t.Warning)
		}
		if w.Branch != "" {
			wui.Checkbox(c, &d.DeleteBranch, i18n.Tf("同时删除分支 %s", w.Branch)).FontSize(12).
				Tooltip(i18n.T("git branch -d：没合并的分支会再问一次"))
		}
	}
	footer := func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			d.Open = false
		}
		if dlgButton(c, i18n.T("移除"), btnDanger, false, true) {
			remove()
		}
	}
	return o, body, footer
}
