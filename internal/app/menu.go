package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件把命令注册表变成菜单栏。菜单项一律不置灰（原生菜单的可用状态要每帧同步，
// 不值得）：点了不能用的命令什么都不做。勾选状态和仓库名变了才重建整个菜单（SyncMenu 每帧比对）。
// 菜单栏是整个应用的：几个窗口时只有前台的那个同步它（换到前台时 Windows.Focus 让它重建），
// 点了交给 Click 带来的那个窗口（target），不是建菜单的那个。

// menuSignature 是决定菜单长什么样的状态：勾选项、仓库列表、快捷键。
func (a *App) menuSignature() string {
	var b strings.Builder
	for _, c := range Commands {
		if c.Checked != nil && c.Checked(a) {
			b.WriteString(c.ID)
			b.WriteByte(';')
		}
	}
	for _, r := range a.Repos {
		b.WriteString(r.Root)
		b.WriteByte(';')
	}
	b.WriteByte('|')
	for _, p := range a.St.RecentWs {
		b.WriteString(p)
		b.WriteByte(';')
	}
	b.WriteByte('|')
	for _, p := range a.St.Settings.Profiles() {
		b.WriteString(p.Name)
		b.WriteByte(';')
	}
	b.WriteString(i18n.Lang())
	fmt.Fprintf(&b, "|%d|%t", keyRev, KeyRecording) // 改了快捷键、正在录键（keybind.go）
	return b.String()
}

// SyncMenu 在菜单需要变的时候重建它；每帧在 View 里调。没有菜单栏（测试）或不是前台窗口时什么都不做。
func (a *App) SyncMenu() {
	if !a.HasMenu || a.Windows != nil && a.Windows.Focused() != a {
		return
	}
	if sig := a.menuSignature(); sig != a.menuSig {
		a.menuSig = sig
		mygo.App.SetMenu(a.BuildMenu())
	}
}

// BuildMenu 是整个菜单栏。
func (a *App) BuildMenu() *mygo.Menu {
	top := []*mygo.MenuItem{a.appMenu()}
	for _, group := range Menus {
		var items []*mygo.MenuItem
		for _, c := range Commands {
			if c.Group != group {
				continue
			}
			if c.ID == "file.clearRecent" { // 在"最近的工作区"子菜单里
				continue
			}
			if c.ID == "term.newProfile" { // 换成子菜单：默认 + 每个配置；没有配置不放
				if m := a.profileMenu(); m != nil {
					items = append(items, m)
				}
				continue
			}
			if c.Sep && len(items) > 0 {
				items = append(items, mygo.Separator())
			}
			if item := a.menuItem(c); item != nil {
				items = append(items, item)
			}
			if c.ID == "file.openWsFile" {
				items = append(items, a.recentMenu())
			}
			if c.ID == "repo.9" { // 第 10 个起的仓库没有快捷键
				for i := 9; i < len(a.Repos); i++ {
					items = append(items, &mygo.MenuItem{Label: a.Repos[i].Name, ToolTip: a.Repos[i].Root,
						Click: func(_ *mygo.MenuItem, win *mygo.Window) { t := a.target(win); t.Update(func() { t.JumpRepo(i) }) }})
				}
			}
		}
		top = append(top, &mygo.MenuItem{Label: i18n.T(group), Submenu: items})
		if group == "文件" {
			top = append(top, &mygo.MenuItem{Role: mygo.RoleEditMenu})
		}
	}
	return mygo.NewMenu(append(top, &mygo.MenuItem{Role: mygo.RoleWindowMenu}))
}

// recentMenu 是文件菜单里的"最近的工作区"：点了换过去，最后是"清除最近工作区"。
func (a *App) recentMenu() *mygo.MenuItem {
	var items []*mygo.MenuItem
	for _, p := range a.RecentWorkspaces() {
		items = append(items, &mygo.MenuItem{Label: WsLabel(p), ToolTip: p,
			Click: func(_ *mygo.MenuItem, win *mygo.Window) { t := a.target(win); t.Update(func() { t.OpenWorkspace(p) }) }})
	}
	if len(items) > 0 {
		items = append(items, mygo.Separator())
	}
	items = append(items, a.menuItem(Find("file.clearRecent")))
	return &mygo.MenuItem{Label: i18n.T("最近的工作区"), Submenu: items}
}

// profileMenu 是终端菜单里的"用配置新建终端"：默认的 shell，再是设置里的每个终端配置，点了在当前仓库开一个；
// 一个配置都没有为 nil。
func (a *App) profileMenu() *mygo.MenuItem {
	profs := a.St.Settings.Profiles()
	if len(profs) == 0 {
		return nil
	}
	open := func(name string) func(*mygo.MenuItem, *mygo.Window) {
		return func(_ *mygo.MenuItem, win *mygo.Window) {
			t := a.target(win)
			t.Update(func() { t.NewTerminalProfile(t.TermRepo(), "", name) })
		}
	}
	items := []*mygo.MenuItem{{Label: i18n.T("默认"), Click: open("")}, mygo.Separator()}
	for _, p := range profs {
		items = append(items, &mygo.MenuItem{Label: p.Name, ToolTip: strings.Join(p.Command(), " "), Click: open(p.Name)})
	}
	return &mygo.MenuItem{Label: i18n.T("用配置新建终端"), Submenu: items}
}

// appMenu 是菜单栏最左边的应用菜单：应用分组的命令（关于 Grove、设置…），加上系统的服务、隐藏、退出。
// "关于"是自己的窗口（版本、日志目录、许可），不用系统的关于面板。
func (a *App) appMenu() *mygo.MenuItem {
	var items []*mygo.MenuItem
	for _, c := range Commands {
		if c.Group == AppGroup {
			if c.Sep && len(items) > 0 {
				items = append(items, mygo.Separator())
			}
			items = append(items, a.menuItem(c))
		}
	}
	items = append(items, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleServices}, mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleHide}, &mygo.MenuItem{Role: mygo.RoleHideOthers}, &mygo.MenuItem{Role: mygo.RoleUnhide},
		mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})
	return &mygo.MenuItem{Label: mygo.App.Name(), Submenu: items}
}

func (a *App) menuItem(c *Command) *mygo.MenuItem {
	accel := c.Accel()
	if KeyRecording {
		accel = ""
	}
	item := &mygo.MenuItem{ID: c.ID, Label: c.Label(), Accelerator: accel,
		Click: func(_ *mygo.MenuItem, win *mygo.Window) { t := a.target(win); t.Update(func() { t.menuRun(c) }) }}
	if c.Checked != nil {
		item.Type, item.Checked = mygo.MenuItemCheckbox, c.Checked(a)
	}
	if n, _ := strings.CutPrefix(c.ID, "repo."); len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
		i := int(n[0] - '1')
		if i >= len(a.Repos) {
			return nil
		}
		item.Label, item.ToolTip = a.Repos[i].Name, a.Repos[i].Root
	}
	return item
}

// menuRun 执行菜单项：有快捷键的按键分派（菜单的快捷键先于焦点里的编辑器接到按键，
// ⌥⌘F 在文件标签里要落到编辑器的替换上）。
func (a *App) menuRun(c *Command) {
	if c.Key == 0 || !a.RunKey(c.Mods, c.Key) {
		a.run(c)
	}
}
