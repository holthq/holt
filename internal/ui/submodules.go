package ui

import (
	"path/filepath"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 子模块的界面：文件树里那一行的提示和右键菜单、仓库菜单里的"子模块"、Git 栏里子模块那一项的 Diff。
// 操作都在 app/submodules.go。

// subTip 是文件树里初始化过的子模块那一行的提示：记的哪次提交、状态、双击做什么。
func subTip(r *app.Repo, p string) string {
	s, _ := r.Submodule(p)
	return i18n.Tf("子模块 · 记录的是 %s · %s\n双击添加为工作区里的仓库", git.Short(s.Head), s.StateText())
}

// submoduleMenu 是文件树里子模块那一行的右键菜单。
func submoduleMenu(a *app.App, m menu, r *app.Repo, p string) {
	s, _ := r.Submodule(p)
	inited := s.Inited()
	if m.Item(i18n.T("添加为仓库…")).Disabled(!inited).Chosen() {
		a.AddSubmoduleRepo(r, p)
	}
	if m.Item(i18n.T("在新窗口中打开")).Disabled(!inited).Chosen() {
		a.SubmoduleWindow(r, p)
	}
	m.Separator()
	if !inited {
		if m.Item(i18n.T("初始化并更新…")).Chosen() {
			a.InitSubmodule(r, p)
		}
	} else if m.Item(i18n.T("更新到父仓库记录的提交…")).Chosen() {
		a.UpdateSubmodule(r, p)
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.InFinder(filepath.Join(r.Root, filepath.FromSlash(p)))
	}
	if m.Item(i18n.T("复制 URL")).Disabled(s.URL == "").Chosen() {
		copyText(s.URL)
		a.Toast = i18n.Tf("已复制 %s 的地址", p)
	}
	if m.Item(i18n.T("复制仓库内路径")).Chosen() {
		copyText(p)
	}
}

// submoduleItems 是仓库菜单里"子模块"下面的：每个子模块一项（点了在文件树里选中它），最后全部初始化并更新。
func submoduleItems(a *app.App, m menu, r *app.Repo) {
	if len(r.Extra.Subs) == 0 {
		m.Item(i18n.T("没有子模块")).Disabled(true)
		return
	}
	for _, s := range r.Extra.Subs {
		if m.Item(s.Path + " · " + git.Short(s.Head) + " · " + s.StateText()).Chosen() {
			a.Reveal(r, s.Path)
		}
	}
	m.Separator()
	if m.Item(i18n.T("全部初始化并更新…")).Chosen() {
		a.InitAllSubmodules(r)
	}
}

// subDiff 是子模块那一项的 Diff 标签：两次提交（d.Info），下面添加为仓库 / 在新窗口中打开它。
func subDiff(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	r := a.RepoOf(d)
	wui.Column(c).Grow(1).MinHeight(0).AlignItems(wui.Center).Justify(wui.Center).Gap(6).Padding(16).Children(func() {
		wui.Icon(c, iconSubmodule).FontSize(iconEmpty).TextColor(t.TextMuted.Alpha(0.8))
		wui.Text(c, d.Info).FontSize(13).FontWeight(600).TextColor(t.TextMuted).Center()
		wui.Text(c, i18n.T("子模块是另一个仓库：父仓库只记着它的一次提交，里面的改动在它自己那边看")).FontSize(11).TextColor(t.TextMuted.Alpha(0.8)).Center()
		if r == nil {
			return
		}
		p := d.Change.Path
		wui.Row(c).Gap(8).Padding(6, 0, 0, 0).Children(func() {
			if wui.Button(c, i18n.T("添加为仓库…")).FontSize(11.5).Tooltip(i18n.T("把子模块添加为工作区里的仓库，和别的仓库一起看")).Clicked() {
				a.AddSubmoduleRepo(r, p)
			}
			if wui.Button(c, i18n.T("在新窗口中打开子模块")).FontSize(11.5).Tooltip(i18n.T("在新窗口里把子模块当工作区打开")).Clicked() {
				a.SubmoduleWindow(r, p)
			}
		})
	})
}
