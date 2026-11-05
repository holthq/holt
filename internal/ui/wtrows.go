package ui

import (
	"strconv"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// Git 栏里"工作树就是 agent 的工位"的界面：状态符号、展开的工作树（摘要、main...分支 的文件、终端）、"需要你"条。
// 只有琥珀点和绿勾带颜色，别的一律次要灰、更淡的灰。

// wtGreen 是"可合并"的绿勾、已暂存组头的勾选框、段头"干净"的勾。
func wtGreen(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.Hex("#5CC28A")
	}
	return wui.Hex("#2E8B57")
}

// amberText 是"需要你"条上等你的名字的琥珀字；条的底是 agentWaitColor 的 11%。
func amberText(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.Hex("#F2B04A")
	}
	return wui.Hex("#8A5000")
}

// accentSolid 是强调色实心按钮（合并到 main）的底。
func accentSolid(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.Hex("#5F7E9F")
	}
	return wui.Hex("#4E6E8E")
}

// wtStateName 是工作树状态的名字（悬停提示、读屏）。
func wtStateName(s app.WtState) string {
	switch s {
	case app.WtWaiting:
		return i18n.T("agent 在等你")
	case app.WtRunning:
		return i18n.T("agent 运行中")
	case app.WtMergeable:
		return i18n.T("可合并")
	case app.WtDirty:
		return i18n.T("有改动")
	case app.WtIdle:
		return i18n.T("agent 空闲")
	case app.WtMerged:
		return i18n.T("已合并")
	}
	return i18n.T("没有 agent")
}

// wtMark 画工作树状态 s 的符号（放在 16 见方的格子里）：等你是 8px 的琥珀点加 3px 的淡琥珀光圈，运行中是 agentMark 的转圈，
// 可合并是绿勾，有改动、没有 agent 是文件夹，空闲是灰色空心圆，已合并是合并图标。
func wtMark(c *wui.Context, s app.WtState) {
	t := c.Theme()
	name := wtStateName(s)
	switch s {
	case app.WtWaiting:
		amber := agentWaitColor(t)
		wui.Box(c.Key("wt-wait")).Size(14, 14).Radius(999).Background(amber.Alpha(0.25)).AlignItems(wui.Center).Justify(wui.Center).
			Shrink(0).Label(name).Tooltip(name).Children(func() {
			wui.Box(c).Size(8, 8).Radius(999).Background(amber)
		})
	case app.WtRunning:
		agentMark(c, app.AgentRunning)
	case app.WtMergeable:
		wui.Icon(c, iconCheck).FontSize(iconRow).TextColor(wtGreen(t)).Shrink(0).Label(name).Tooltip(name)
	case app.WtIdle:
		wui.Box(c).Size(10, 10).Radius(999).BorderWidth(1.5).BorderColor(t.TextMuted).Shrink(0).Label(name).Tooltip(name)
	case app.WtMerged:
		wui.Icon(c, iconMerge).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0).Label(name).Tooltip(name)
	default:
		wui.Icon(c, iconFolder).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0).Label(name).Tooltip(name)
	}
}

// shortDur 是"2m""12m""3h""2d"：等了 / 跑了多久，不到一分钟写秒。
func shortDur(d time.Duration) string {
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// wtSummaryRow 是展开的工作树下面的摘要行（高 36）：左边提交数、文件数和 +增 −删（main...分支），右边是审查（灰底眼睛）
// 和合并到 main（强调色实心、白色合并图标；灰着时提示里写原因，点了先问）。
func wtSummaryRow(a *app.App, row wui.ListRow, cr app.ChangeRow, g *gitFrame) {
	c := row.Context
	t := c.Theme()
	r, w, x := cr.Repo, cr.Wt, a.WtExp
	el := wui.Row(c).Height(36).Padding(0, 6, 0, 48).Gap(8).AlignItems(wui.Center)
	if el.Hovered() {
		a.GitHover = row.Index
	}
	el.Children(func() {
		muted := t.TextMuted
		switch {
		case x == nil:
		case x.Loading:
			wui.Spinner(c)
		case x.Err != nil:
			wui.Text(c, x.Err.Error()).FontSize(sideSub).TextColor(t.Danger).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		default:
			stat := func(ic *wui.SVG, n int, tip string) {
				wui.Row(c).Gap(3).AlignItems(wui.Center).Shrink(0).Tooltip(tip).Label(tip).Children(func() {
					wui.Icon(c, ic).FontSize(iconInline).TextColor(muted)
					wui.Text(c, strconv.Itoa(n)).FontSize(sideSub).TextColor(muted).SingleLine().FontFeatures("tnum")
				})
			}
			stat(iconCommit, x.Commits, i18n.Tf("%s 个提交", strconv.Itoa(x.Commits)))
			stat(iconFile, len(x.Files), i18n.Tf("%s 个文件", strconv.Itoa(len(x.Files))))
			add, del := lineTotals(x.Files)
			if add+del > 0 {
				wui.Row(c).Gap(4).AlignItems(wui.Center).Shrink(0).Children(func() {
					wui.Text(c, "+"+strconv.Itoa(add)).FontSize(sideSub).TextColor(wtGreen(t)).SingleLine().FontFeatures("tnum")
					wui.Text(c, "−"+strconv.Itoa(del)).FontSize(sideSub).TextColor(t.Danger).SingleLine().FontFeatures("tnum")
				})
			}
		}
		wui.Spacer(c).Grow(1)
		review := squareButton(c.Key("review"), iconEye, i18n.T("审查"), i18n.T("审查：在主区看它比主分支多出的改动（main...分支）"),
			wtBtnSize+2, iconRow, false, func() { g.do(func() { a.ReviewWorktree(r, w) }) })
		review.Background(t.Text.Alpha(0.07))
		into := r.Status.Branch
		if into == "" {
			into = "main"
		}
		why := app.MergeWorktreeWhy(r, w)
		tip := i18n.Tf("合并到 %s…", into)
		if why != "" {
			tip = i18n.Tf("合并到 %s：%s", into, why)
		}
		m := wui.Box(c.Key("merge")).Size(wtBtnSize+2, wtBtnSize+2).Radius(6).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).
			Label(i18n.Tf("合并到 %s…", into)).Cursor(wui.CursorPointer)
		tooltip(c, m, tip)
		bg := accentSolid(t)
		switch {
		case why != "":
			m.Opacity(0.4)
		case m.Pressed():
			bg = bg.Mix(wui.Hex("#000000"), 0.12)
		case m.Hovered():
			bg = bg.Mix(wui.Hex("#FFFFFF"), 0.1)
		}
		m.Background(bg).Children(func() {
			wui.Icon(c, iconMerge).FontSize(iconRow).TextColor(wui.Hex("#FFFFFF"))
		})
		if m.Clicked() && why == "" {
			g.do(func() { a.MergeWorktree(r, w) })
		}
	})
}

// wtFileRow 是展开的工作树比主分支多出的一个文件（高 26）：状态徽标、文件名、灰色的目录、+/− 计数。选中开它的 Diff。
func wtFileRow(a *app.App, row wui.ListRow, cr app.ChangeRow) {
	c := row.Context
	t := c.Theme()
	sel := onAccent(a, row.Selected())
	muted := t.TextMuted
	if sel {
		muted = t.AccentText.Alpha(0.75)
	}
	el := wui.Row(c).Height(26).Padding(0, 8, 0, 48).Gap(8).AlignItems(wui.Center).Tooltip(cr.Change.Path)
	if el.Hovered() {
		a.GitHover = row.Index
	}
	if el.RowDoubleClicked() {
		a.OnChangeSelected(true)
	}
	el.Children(func() {
		commitFileLabel(c, cr.Change, sel, muted)
	})
}

// wtTermRow 是展开的工作树上 agent 的终端：终端图标、前台进程名，做完了（空闲）是绿勾，加多久以前；点它跳到那个终端。
func wtTermRow(a *app.App, row wui.ListRow, cr app.ChangeRow, g *gitFrame) {
	c := row.Context
	t := c.Theme()
	ag := a.WtAgent(cr.Repo, cr.Wt)
	el := wui.Row(c).Height(26).Padding(0, 8, 0, 48).Gap(6).AlignItems(wui.Center).Cursor(wui.CursorPointer).Radius(6).
		Label(i18n.T("跳到终端")).Tooltip(i18n.T("跳到这个工作树上的 agent 终端"))
	if el.Hovered() {
		a.GitHover = row.Index
		el.Background(toolFill(t, 0.06))
	}
	if el.Clicked() && ag.Pane != nil {
		g.do(func() { a.GoToPane(ag.Repo, ag.Tab, ag.Pane) })
	}
	el.Children(func() {
		wui.Icon(c, iconTerminal).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0)
		job := ""
		if ag.Pane != nil {
			job = ag.Pane.AgentJob()
		}
		wui.Text(c, job).FontSize(sideFont).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		switch ag.State {
		case app.AgentIdle:
			wui.Icon(c, iconCheck).FontSize(iconInline).TextColor(wtGreen(t)).Shrink(0).Label(i18n.T("做完了")).Tooltip(i18n.T("做完了"))
		default:
			agentMark(c, ag.State)
		}
		wui.Spacer(c).Grow(1)
		if s := ag.Since(); !s.IsZero() {
			wui.Text(c, shortDur(a.Now().Sub(s))).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().FontFeatures("tnum")
		}
	})
}

// needYouBar 是 Git 栏顶上、所有仓库前面的"需要你"条（高 32，圆角 8，琥珀色 11% 的底）：只在有工作树等你或可合并时出现。
// 内容只有工作树名：琥珀点加等你的（半粗琥珀字），绿勾加可合并的（次要色），放不下的写"+N"；右边是上一个 / 下一个。
func needYouBar(a *app.App, c *wui.Context) {
	ns := a.NeedsYou()
	if len(ns) == 0 {
		return
	}
	t := c.Theme()
	const maxShown = 3
	wui.Row(c.Key("needyou")).Height(32).Margin(0, 8, 4, 8).Padding(0, 2, 0, 10).Gap(8).Radius(8).AlignItems(wui.Center).
		Shrink(0).Background(agentWaitColor(t).Alpha(0.11)).Label(i18n.T("需要你")).Children(func() {
		wui.Row(c).Gap(10).AlignItems(wui.Center).MinWidth(0).Shrink(1).Children(func() {
			for i, n := range ns {
				if i == maxShown {
					break
				}
				wui.Row(c.Key(n.Repo.Root + "\x00" + n.Wt.Path)).Gap(5).AlignItems(wui.Center).MinWidth(0).Shrink(1).
					Tooltip(n.Repo.Name + " · " + wtStateName(n.State)).Children(func() {
					waiting := n.State == app.WtWaiting
					if waiting {
						wui.Box(c).Size(8, 8).Radius(999).Background(agentWaitColor(t)).Shrink(0)
					} else {
						wui.Icon(c, iconCheck).FontSize(iconInline).TextColor(wtGreen(t)).Shrink(0)
					}
					name := wui.Text(c, wtTitle(n.Wt)).FontSize(sideSub).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
					if waiting {
						name.FontWeight(600).TextColor(amberText(t))
					} else {
						name.TextColor(t.TextMuted)
					}
				})
			}
			if len(ns) > maxShown {
				wui.Text(c, "+"+strconv.Itoa(len(ns)-maxShown)).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Shrink(0)
			}
		})
		wui.Spacer(c).Grow(1)
		squareButton(c.Key("prev"), iconChevronUp, i18n.T("上一个需要你的工作树"), i18n.T("上一个需要你的工作树"), wtBtnSize, iconRow, false,
			func() { a.StepNeedsYou(-1) })
		squareButton(c.Key("next"), iconChevron, i18n.T("下一个需要你的工作树"), i18n.T("下一个需要你的工作树"), wtBtnSize, iconRow, false,
			func() { a.StepNeedsYou(1) })
	})
}
