package ui

import (
	"fmt"
	"slices"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// cloneWidth 是克隆对话框的宽：比新建分支那种表单宽一档，地址和路径放得下。
const cloneWidth = 460

// cloneDialog 是"克隆仓库"（a.CloneDlg，app/clone.go）：地址、放到（+ 选择…）、目录名（跟着地址推，改过就不跟）、
// 含子模块；说明行是会克隆到哪，下面是会跑的命令。⏎ = 克隆。
func cloneDialog(a *app.App, c *wui.Context) {
	d := &a.CloneDlg
	d.Sync()
	parentErr, nameErr := a.CloneErrs()
	ok := a.CloneReady()
	submit := func() {
		if a.CloneReady() {
			a.StartClone()
		}
	}
	note := i18n.T("克隆下来加进仓库列表；私有仓库会弹登录框。")
	if dest := d.DestLabel(); dest != "" {
		note = i18n.Tf("克隆到 %s", dest)
	}
	o := dialogOpts{Title: i18n.T("克隆仓库"), Note: note, Width: cloneWidth, Hint: i18n.T("克隆")}
	dialogFrame(c, &d.Open, o, func() {
		dlgField(c, i18n.T("地址"), "", func() {
			if dlgInput(c, wui.TextInput(c, &d.URL).Placeholder("https://github.com/owner/repo.git").Label(i18n.T("仓库地址")).AutoFocus()).Submitted() {
				submit()
			}
		})
		dlgField(c, i18n.T("放到"), parentErr, func() {
			wui.Row(c).Gap(6).AlignItems(wui.Center).MinWidth(0).Children(func() {
				if dlgInput(c, wui.TextInput(c, &d.Parent).Label(i18n.T("放到哪个目录")).Grow(1).MinWidth(0)).Submitted() {
					submit()
				}
				if dlgButton(c, i18n.T("选择…"), btnPlain, false, false) {
					a.ChooseCloneParent()
				}
			})
		})
		dlgField(c, i18n.T("目录名"), nameErr, func() {
			if dlgInput(c, wui.TextInput(c, &d.Name).Placeholder(i18n.T("从地址推")).Label(i18n.T("目录名"))).Submitted() {
				submit()
			}
		})
		wui.Checkbox(c, &d.Sub, i18n.T("含子模块")).FontSize(12).Tooltip("--recurse-submodules")
		if ok {
			dlgCommand(c, d.Command())
		}
	}, func() {
		if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
			d.Open = false
		}
		if dlgButton(c, i18n.T("克隆"), btnPrimary, !ok, false) {
			submit()
		}
	})
	if !a.CloneDlg.Open {
		a.CloneDlg = app.CloneDialog{}
	}
}

// cloneRows 是侧栏文件页顶上正在跑的克隆，一个一行（长得像仓库行）：转圈、目录名、进度字、✕；底下一条 2 高的进度条（有百分比时）。
// 右键是"取消克隆"。不进文件树的虚拟列表（占位不是仓库，键盘、拖动、菜单都不该落到它上面）。
func cloneRows(a *app.App, c *wui.Context) {
	if len(a.Clones) == 0 {
		return
	}
	t := c.Theme()
	wui.Column(c).Shrink(0).Padding(0, 4, 2, 4).Children(func() {
		for _, cl := range a.Clones {
			kc := c.Key(cl)
			box := wui.Column(kc).Radius(5).Clip().Background(t.Text.Alpha(0.04)).Tooltip(cl.URL)
			ctxMenu(a, kc, box, menuOwner{"clone", fmt.Sprintf("%p", cl)}, func(m menu) {
				if !slices.Contains(a.Clones, cl) { // 菜单开着时克隆完了 / 取消了：关掉
					return
				}
				if m.Item(i18n.T("取消克隆")).Chosen() {
					a.CancelClone(cl)
				}
			})
			box.Children(func() {
				wui.Row(kc).Padding(5, 6, 5, 8).Gap(6).AlignItems(wui.Center).MinWidth(0).Children(func() {
					wui.Spinner(kc)
					wui.Text(kc, cl.Name).FontSize(sideFont).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
					wui.Text(kc, cl.Text).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
					wui.Spacer(kc).Grow(1)
					iconButton(kc, iconClose, i18n.Tf("取消克隆 %s", cl.Name), func() { a.CancelClone(cl) })
				})
				if cl.Pct >= 0 { // mygo 没有进度条：一条底色上画到百分比那么宽
					pct := float32(min(cl.Pct, 100)) / 100
					wui.Box(kc).Height(2).Shrink(0).Background(t.Accent.Alpha(0.15)).DrawOver(func(p *wui.Painter, b wui.Rect) {
						p.Fill(wui.Rect{X: b.X, Y: b.Y, W: b.W * pct, H: b.H}, t.Accent, 0)
					})
				}
			})
		}
	})
}
