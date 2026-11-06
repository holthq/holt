package ui

import (
	wui "github.com/egoist/mygo/ui"
	"strconv"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是交互式变基向导标签（app/rebase.go 跑它）：每个提交一行，最老的在上面；行上是动作按钮、哈希、标题、
// 推送过的胶囊、上移 / 下移，reword / squash 下面一个信息框。底下一栏是摘要和"开始变基 / 重置 / 关闭"，
// 跑着、停下时上面一栏说现在怎样（停下以后交给 Git 栏的横幅）。

// rebaseKeys 是向导列表有键盘时设动作的字母键（和 git 的 todo 里一样的首字母）。
var rebaseKeys = []struct {
	key wui.Key
	x   docs.RebaseAction
}{
	{wui.KeyP, docs.RebasePick}, {wui.KeyR, docs.RebaseReword}, {wui.KeyS, docs.RebaseSquash},
	{wui.KeyF, docs.RebaseFixup}, {wui.KeyD, docs.RebaseDrop}, {wui.KeyE, docs.RebaseEdit},
}

// rebaseKeyNames 是菜单里每个动作后面括的键。
var rebaseKeyNames = [...]string{"P", "R", "S", "F", "D", "E"}

// rebaseContent 是交互式变基向导标签的内容；focused 是它在有键盘的那组。
func rebaseContent(a *app.App, c *wui.Context, d *docs.Doc, focused bool) {
	p := d.Rebase
	p.Defaults()
	locked := p.Running || p.Stopped
	rebaseBar(a, c, d)
	p.List.Selected = &p.Sel
	p.List.Key = func(i int) any { return p.Steps[i].Commit.Hash } // 挪行时信息框跟着它的提交
	l := newListLook(a, c, 0)
	l.build(c, func() {
		wui.List(c, &p.List, len(p.Steps)).Label(i18n.T("交互式变基的提交")).Grow(1).MinHeight(0).PaddingX(6).PaddingY(4).Rows(l.rows(func(row wui.ListRow) {
			rebaseRow(a, row, p, row.Index, locked)
		}))
	})
	// 只在列表自己有键盘时登记：信息框里打字时 P、S 是字
	if !locked && p.List.Focused(c) {
		p.List.OnShortcut(c, wui.Alt, wui.KeyUp, func() { a.MoveRebaseStep(p, p.Sel, -1) })
		p.List.OnShortcut(c, wui.Alt, wui.KeyDown, func() { a.MoveRebaseStep(p, p.Sel, 1) })
		for _, k := range rebaseKeys {
			p.List.OnShortcut(c, 0, k.key, func() { a.SetRebaseAction(p, p.Sel, k.x) })
		}
	}
	if focused && a.FocusDoc {
		a.FocusDoc = false
		p.List.Focus(c)
	}
	rebaseFooter(a, c, d)
}

// rebaseRow 是计划里的一步。
func rebaseRow(a *app.App, row wui.ListRow, p *docs.RebasePlan, i int, locked bool) {
	c := row.Context
	t := c.Theme()
	s := &p.Steps[i]
	sel := onAccent(a, row.Selected())
	muted, mark := t.TextMuted, t.TextMuted // mark：短哈希（不上强调色）
	if sel {
		muted, mark = t.AccentText.Alpha(0.75), t.AccentText
	}
	wui.Column(c).Padding(4, 6, 4, 8).Gap(4).Children(func() {
		wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
			wui.Textf(c, "%d", i+1).Font(mono()).FontSize(11).TextColor(muted).Width(20).Shrink(0)
			// 动作选择：点开是 Holt 菜单（当前的打勾，不能合并的灰着）
			act := wui.ButtonBase(c).Width(84).Height(22).Shrink(0).Padding(0, 4, 0, 8).Gap(4).AlignItems(wui.Center).
				Radius(5).Border(1, t.Border).Label(i18n.Tf("第 %d 行做什么：%s", i+1, s.Action)).Tooltip(s.Action.Text()).Disabled(locked)
			if act.Hovered() && !locked {
				act.Background(toolFill(t, 0.06))
			}
			act.Children(func() {
				wui.Text(c, s.Action.String()).Font(mono()).FontSize(11.5).SingleLine().Grow(1).MinWidth(0)
				wui.Icon(c, iconChevron).FontSize(iconInline).TextColor(t.TextMuted)
			})
			step, hash := i, s.Commit.Hash
			menuButton(a, c, act, menuOwner{"rebase", strconv.Itoa(step)}, func(m menu) {
				// 每帧重建：菜单开着时计划变了（行少了、换了顺序）就什么也不加，菜单关掉
				if step >= len(p.Steps) || p.Steps[step].Commit.Hash != hash {
					return
				}
				for k, x := range docs.RebaseActions {
					it := m.Item(i18n.Tf("%s — %s（按 %s）", x, x.Text(), rebaseKeyNames[k])).Checked(p.Steps[step].Action == x).
						Disabled(x.Melds() && !app.CanMeld(p, step))
					if it.Chosen() {
						a.SetRebaseAction(p, step, x)
					}
				}
			})
			wui.Text(c, s.Commit.Short).Font(mono()).FontSize(11).TextColor(mark).SingleLine().Shrink(0)
			subject := wui.Text(c, s.Commit.Subject).FontSize(12.5).FontWeight(500).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if s.Action == docs.RebaseDrop {
				subject.Strikethrough().TextColor(muted)
			}
			if s.Pushed {
				pill(c, i18n.T("已推送"), t.Warning, sel).Tooltip(i18n.T("已经推送到远程：改写以后要强制推送"))
			}
			wui.Text(c, s.Commit.Author).FontSize(10.5).TextColor(muted).SingleLine().Ellipsis("…").MaxWidth(120)
			wui.Text(c, git.Ago(s.Commit.Date)).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0).
				Tooltip(s.Commit.Date.Local().Format("2006-01-02 15:04"))
			squareButton(c, iconChevronUp, i18n.Tf("把第 %d 行上移", i+1), i18n.T("上移一行（⌥↑）"), rowToolSize, iconRow,
				locked || i == 0, func() { a.MoveRebaseStep(p, i, -1) })
			squareButton(c, iconChevron, i18n.Tf("把第 %d 行下移", i+1), i18n.T("下移一行（⌥↓）"), rowToolSize, iconRow,
				locked || i == len(p.Steps)-1, func() { a.MoveRebaseStep(p, i, 1) })
		})
		switch {
		case s.Action.HasMsg():
			label := i18n.Tf("第 %d 行的新提交信息", i+1)
			if s.Action == docs.RebaseSquash {
				label = i18n.Tf("第 %d 行合在一起的提交信息", i+1)
			}
			wui.TextArea(c.Key("msg"), &s.Msg).Lines(2, 8).Label(label).Placeholder(i18n.T("提交信息")).
				Font(mono()).FontSize(12).ReadOnly(locked).Margin(0, 0, 0, 28)
		case s.Action == docs.RebaseEdit:
			wui.Text(c, i18n.T("会停在这里；改完用 Git 栏的横幅继续")).FontSize(11).TextColor(muted).Margin(0, 0, 0, 28)
		}
	})
}

// rebaseBar 是向导上面说现在怎样的一栏：跑着、停下了（第几步、为什么）、停下以后结束了。没开始时没有。
func rebaseBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	p := d.Rebase
	text, bg := "", bannerFill(t)
	switch {
	case p.Running:
		text = i18n.T("正在变基…（底栏的输出里有 git 说的）")
	case p.Stopped && p.Ended:
		text = i18n.T("这次变基已经结束了，可以关掉这个标签")
	case p.Stopped:
		bg = t.Warning.Alpha(0.1)
		text = i18n.Tf("停下了：%s，解决后用 Git 栏的横幅继续", p.Reason)
		if r := a.RepoOf(d); r != nil && r.Extra.Progress.Total > 0 {
			pr := r.Extra.Progress
			text = i18n.Tf("停在第 %d / %d 步：%s，解决后用 Git 栏的横幅继续", pr.Step, pr.Total, p.Reason)
		}
	default:
		return
	}
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(bg).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconRebase).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, text).FontSize(11.5).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Tooltip(text)
	})
}

// rebaseFooter 是向导底下一栏：几个提交变几个、推送过几个、要不要自动贮藏，"开始变基 / 重置 / 关闭"。
func rebaseFooter(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	p := d.Rebase
	r := a.RepoOf(d)
	wui.Row(c).Padding(6, 10).Gap(8).AlignItems(wui.Center).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		wui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
			wui.Row(c).Gap(10).AlignItems(wui.Center).Children(func() {
				wui.Text(c, i18n.Tn("%d 个提交 → %d 个", len(p.Steps), app.RebaseResult(p))).FontSize(12).FontWeight(600).SingleLine().Shrink(0)
				pushed := app.RebasePushed(p)
				color := t.TextMuted
				if pushed > 0 {
					color = t.Warning
				}
				wui.Text(c, i18n.Tf("%d 个已推送，完成后要强制推送（Git 菜单 → 强制推送）", pushed)).FontSize(11).TextColor(color).SingleLine().Ellipsis("…").MinWidth(0)
			})
			if r != nil && r.Status.Staged+r.Status.Unstaged > 0 && !p.Stopped {
				wui.Text(c, i18n.T("有未提交的改动：先自动贮藏，做完恢复（--autostash）")).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
			}
		})
		if !p.Stopped {
			why := app.RebaseProblem(p)
			if iconAction(c.Key("start"), iconPlay, i18n.T("开始变基"), i18n.T("先问一次，再跑 git rebase -i --autostash"), why, t.Accent) {
				a.StartRebase(d)
			}
			resetWhy := ""
			switch {
			case p.Running:
				resetWhy = i18n.T("正在变基")
			case !p.Changed():
				resetWhy = i18n.T("还没改过")
			}
			if iconAction(c.Key("reset"), iconUndo, i18n.T("重置"), i18n.T("换回全是 pick、原来的顺序"), resetWhy, wui.Transparent) {
				a.ResetRebase(p)
			}
		}
		closeWhy := ""
		if p.Running {
			closeWhy = i18n.T("正在变基")
		}
		if iconAction(c.Key("close"), iconClose, i18n.T("关闭"), i18n.T("关掉向导（什么都不改）"), closeWhy, wui.Transparent) {
			a.CloseDoc(d)
		}
	})
}
