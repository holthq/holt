package ui

import (
	"fmt"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 状态栏两头和上面那一列的内容对齐（不跟着侧栏、右栏的显隐变）：
//   - 左边第一个字和侧栏头部的字（"仓库"、"大纲"、Git 栏的标题，Padding 左 14）一条竖线；
//   - 右边最后一个字和右栏卡片里分段控件、搜索框、开关的右缘一条竖线：卡片外的缝、1 px 描边、卡片里的 8。
const (
	statusPadL = 14
	statusPadR = cardGap + 1 + 8
)

// statusBar 是窗口最底下一行。左：当前仓库、分支图标和分支 ↑↓、变更计数、刷新状态；
// 右：当前标签的行列、语言、换行、大小和状态。高 statusHeight 加上卡片底下那条缝（center 底下不留 padding）：
// 卡片底边到窗口底边整条带子是同色的，字在这一整条里居中（只在下面 22 里居中的话，上面空 11、下面空 6.5，看着偏下）。
func statusBar(a *app.App, c *wui.Context) {
	t := c.Theme()
	muted := t.TextMuted
	wui.Row(c).Height(statusHeight+cardGap).Padding(0, statusPadR, 0, statusPadL).Gap(12).AlignItems(wui.Center).Shrink(0).Children(func() {
		r := a.Current()
		wui.Row(c).Gap(12).AlignItems(wui.Center).Grow(1).MinWidth(0).Children(func() {
			if b := a.Batch; b != nil {
				batchStatus(a, c, b)
			}
			if r == nil {
				wui.Text(c, i18n.T("没有仓库")).FontSize(10.5).TextColor(muted).SingleLine()
				return
			}
			// 仓库名、分支图标、分支 ↑↓：分支图标用 iconBranch（"⎇" 是回退字体画的，比旁边的字低、细）
			wui.Row(c).Gap(4).AlignItems(wui.Center).MinWidth(0).Shrink(1).Label(i18n.Tf("当前仓库 %s", r.Name)).Tooltip(r.Root).Children(func() {
				wui.Text(c, r.Name).FontSize(10.5).FontWeight(500).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if !r.Loaded || r.Err != nil || r.Plain {
					return
				}
				branch := r.Status.Branch
				if r.Linked {
					branch += " · " + i18n.T("工作树")
				}
				wui.Icon(c, iconBranch).FontSize(iconInline).TextColor(muted).Shrink(0)
				wui.Text(c, branch).FontSize(10.5).FontWeight(500).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
				if s := app.UpDown(r.Status); s != "" { // 领先 / 落后：点了同步
					x := wui.Text(c, s).FontSize(10.5).FontWeight(500).TextColor(muted).SingleLine().Shrink(0).
						Label(i18n.Tf("同步 %s", s)).Tooltip(app.Tip(i18n.T("同步：拉取再推送"), "git.sync"))
					if x.Hovered() {
						x.TextColor(t.Text)
					}
					if x.Clicked() {
						a.Sync(r)
					}
				}
			})
			switch {
			case r.Err != nil:
				wui.Text(c, i18n.T("git status 失败")).FontSize(10.5).TextColor(t.Danger).SingleLine().Shrink(0)
			case r.Loaded && !r.Plain:
				wui.Text(c, changeSummary(r.Status)).FontSize(10.5).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2).
					Tooltip(i18n.Tn("%d 个仓库 · 共 %d 项变更", len(a.Repos), a.TotalChanges()))
			}
			problemStatus(a, c) // 检查报的问题（所有仓库一起）
			state := ""
			switch {
			case r.Busy != "":
				state = i18n.Tf("正在%s…", r.Busy)
			case r.Loading:
				state = i18n.T("正在刷新…")
			case r.Loaded:
				state = i18n.Tf("%s刷新", git.Ago(r.LoadedAt))
			}
			if state != "" {
				wui.Text(c, state).FontSize(10.5).TextColor(muted.Alpha(0.8)).SingleLine().Shrink(0).Tooltip(app.Tip(i18n.T("刷新"), "git.refresh"))
			}
		})
		if d := a.CurrentDoc(); d != nil {
			docStatus(a, c, d)
		}
		if a.Zoomed() {
			zoomStatus(a, c)
		}
	})
}

// zoomKeys 是放大 / 缩小 / 实际大小现在的键各一句（“⌘= 放大”），没绑键的那个不列。
func zoomKeys() []string {
	var keys []string
	for _, z := range []struct{ id, what string }{{"view.zoomIn", "%s 放大"}, {"view.zoomOut", "%s 缩小"}, {"view.zoomReset", "%s 回到 100%%"}} {
		if k := app.KeyText(z.id); k != "" {
			keys = append(keys, i18n.Tf(z.what, k))
		}
	}
	return keys
}

// zoomTip 是界面缩放的工具提示。
func zoomTip() string {
	if keys := zoomKeys(); len(keys) > 0 {
		return i18n.Tf("界面缩放：点开换一档；%s", i18n.List(keys))
	}
	return i18n.T("界面缩放：点开换一档")
}

// zoomStatus 是状态栏最右边的界面缩放（不是 100% 时才有）：点开是缩放的几档和“实际大小”。
func zoomStatus(a *app.App, c *wui.Context) {
	t := c.Theme()
	z := a.St.Settings.ZoomPercent()
	x := wui.Text(c, fmt.Sprintf("%d%%", z)).FontSize(10.5).FontWeight(500).TextColor(t.TextMuted).SingleLine().Shrink(0).
		Label(i18n.Tf("界面缩放 %d%%", z)).Tooltip(zoomTip())
	if x.Hovered() {
		x.TextColor(t.Text)
	}
	x.Menu(func(m *wui.Menu) {
		for _, s := range workspace.ZoomSteps {
			if m.Item(fmt.Sprintf("%d%%", s)).Checked(s == z).Chosen() {
				a.SetZoom(s)
			}
		}
		m.Separator()
		if shortcut(m.Item(i18n.T("实际大小")), "view.zoomReset").Chosen() {
			a.ZoomReset()
		}
	})
}

// changeSummary 是 "冲突 1 · 已暂存 2 · 未暂存 4"（未暂存含未跟踪的，和 Git 栏的分组一样），没有改动时是 "工作区干净"。
func changeSummary(st git.Status) string {
	var parts []string
	for _, p := range []struct {
		name string
		n    int
	}{{i18n.T("冲突"), st.Conflicts}, {i18n.T("已暂存"), st.Staged}, {i18n.T("未暂存"), st.Unstaged + st.Untracked}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", p.name, p.n))
		}
	}
	if len(parts) == 0 {
		return i18n.T("工作区干净")
	}
	return strings.Join(parts, " · ")
}
