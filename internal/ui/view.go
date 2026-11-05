package ui

import (
	wui "github.com/egoist/mygo/ui"

	_ "github.com/egoist/mygo/locales/zh-Hans" // mygo 自己的文案（编辑器的查找条、跳行条，系统菜单）的简体中文

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// View 是窗口的内容：每帧从 App 的状态画出整个界面。
//
//	标题栏
//	侧栏 ┆ [主区（标签 + 编辑器 / Diff）] ┆ [右栏（历史）]
//	     ┆ [底栏（终端 / 输出）]          ┆
//	状态栏
//
// [ ] 是卡片，┆ 是露出材质的缝（拖着改大小）。
func View(a *app.App) func(c *wui.Context) {
	return func(c *wui.Context) { root(a, c) }
}

func root(a *app.App, c *wui.Context) {
	t := c.Theme()
	applyAccent(a, t)
	c.SetZoom(a.ZoomFactor()) // 界面缩放（⌘= ⌘- ⌘0）：按窗口的，每个窗口自己设
	a.BindShortcuts(c)
	a.SyncMenu()
	a.AutoSaveTick()
	a.TreeFocused = false // 文件树画出来时再写
	a.TermFocused = false // 终端那一格画出来时再写（bottom.go 的 termPane，底栏和编辑器区的）
	wui.Column(c).Fill().Children(func() {
		titleBar(a, c.Key("title"))
		wui.Row(c).Grow(1).MinHeight(0).AlignItems(wui.Stretch).Children(func() {
			if a.St.Sidebar {
				if a.St.SidebarW == 0 {
					a.St.SidebarW = defaultSidebarW
				}
				sidebar(a, c.Key("sidebar"))
				resizer(c.Key("sideResize"), i18n.T("调整侧栏宽度"), true, true, &a.St.SidebarW, 160, 800, a.SaveState)
			}
			center(a, c.Key("center"))
		})
		statusBar(a, c.Key("status"))
	})
	gmenuView(a, c) // 文件树的右键菜单：选了的项在这里执行，所以在 pending 之前
	dialogs(a, c)
	pending(a, c)
	if a.Toast != "" {
		c.Toast(a.Toast)
		a.Toast = ""
	}
	// 标题栏、侧栏、状态栏直接画在窗口底上，编辑器组、底栏、右栏是放在上面的卡片。窗口底默认是柔和底（底色上几团
	// 淡淡的颜色），可以是纯色，或者通透（整个窗口是一块材质，Vibrancy；系统不给材质时是纯色）。
	// 卡片换过主题会重设根的底色，所以放在最后（布局时才建的淡底列表行也会，见 listLook.row）。
	switch {
	case softBackdrop(a, c):
		c.Root().Background(rootBg(c, t)).Draw(func(p *wui.Painter, r wui.Rect) { paintBackdrop(p, r, t) })
	default:
		c.Root().Background(rootBg(c, t)).Draw(nil)
	}
}

// rootBg 是窗口根的底色：通透时透明，不然是 windowBg。
func rootBg(c *wui.Context, t *wui.Theme) wui.Color {
	if c.Vibrancy() {
		return wui.Transparent
	}
	return windowBg(t)
}

// center 是侧栏右边的卡片：编辑器组和它下面的底栏，最右是右栏；卡片之间、卡片和窗口边之间是 6 pt 的缝，
// 侧栏和卡片之间的缝是 root 里那个拖条；卡片底下的缝算在状态栏里（statusBar），底栏文字在卡片底边和窗口底边中间。
func center(a *app.App, c *wui.Context) {
	left := float32(0)
	if !a.St.Sidebar {
		left = cardGap
	}
	row := wui.Row(c).Grow(1).MinWidth(0).AlignItems(wui.Stretch).Padding(cardGap, cardGap, 0, left)
	// 禅模式：窗口够宽时编辑器组最宽 ZenWidth、放在中间，两边露出窗口底；面板都关着时，连按两下没人接的 Esc 退出
	// （登记在叠层上：编辑器的取消、查找条、对话框先拿 Esc，见 app.ZenEsc）
	zen := a.Zen && c.Root().Bounds().W >= app.ZenWidth+2*app.ZenMargin
	if zen {
		row.Justify(wui.Center)
	}
	if a.Zen && !a.St.Sidebar && !a.St.RightBar && !a.St.BottomBar && row.OverlayShortcut(0, wui.KeyEscape) {
		a.ZenEsc(c.Now())
	}
	if !a.St.BottomBar {
		a.BottomMax = false // 底栏不管怎么关的（换工作区之类），下次打开都是普通高度
	}
	row.Children(func() {
		right := a.St.Settings.TermDock == "right"
		maxed := a.BottomMaxed() // 底栏最大化：不画主区和它旁边的拖条，底栏占掉那块地方
		if !(maxed && right) {
			col := wui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch)
			if zen {
				col.MaxWidth(app.ZenWidth)
			}
			col.Children(func() {
				if !maxed {
					mainArea(a, c.Key("main"))
				}
				if a.St.BottomBar && !right {
					if a.St.BottomH == 0 {
						a.St.BottomH = defaultBottomH
					}
					if !maxed {
						resizer(c.Key("bottomResize"), i18n.T("调整底栏高度"), false, false, &a.St.BottomH, 80, 1200, a.SaveState)
					}
					bottomBar(a, c.Key("bottom"))
				}
			})
		}
		if a.St.BottomBar && right { // 设置里把终端放在右边：底栏整个挪到主区右边
			if a.St.TermW == 0 {
				a.St.TermW = defaultTermW
			}
			if !maxed {
				resizer(c.Key("termResize"), i18n.T("调整终端宽度"), true, false, &a.St.TermW, 240, 1200, a.SaveState)
			}
			bottomBar(a, c.Key("bottom"))
		}
		if a.St.RightBar {
			if a.St.RightW == 0 {
				a.St.RightW = defaultRightW
			}
			resizer(c.Key("rightResize"), i18n.T("调整右栏宽度"), true, false, &a.St.RightW, 200, 900, a.SaveState)
			rightBar(a, c.Key("right"))
		}
	})
}

// pending 处理菜单里选的、要等列表画完才能做的事（免得在列表的行循环里改列表）。
func pending(a *app.App, c *wui.Context) {
	if r := a.RemoveReq; r != nil {
		a.RemoveReq = nil
		a.RemoveRepo(r)
		c.Invalidate()
	}
	if m := a.MoveReq; m != nil {
		a.MoveReq = nil
		a.MoveRepo(m.Repo, m.To)
		c.Invalidate()
	}
	if d := a.RevealReq; d != nil {
		a.RevealReq = nil
		if r := a.RepoOf(d); r != nil {
			for i, x := range a.Repos {
				if x == r {
					a.SelectRepo(i)
				}
			}
			a.Reveal(r, d.Rel)
			c.Invalidate()
		}
	}
	if a.FollowCurrent() {
		c.Invalidate()
	}
}
