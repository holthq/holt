package ui

import (
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// 文件树的右键菜单是 Grove 自己画的（issue #48），状态机是 app.PopMenu（App.TreeMenu），这里只管喂事件和画：
//
//   - 文件树的行（文件 / 目录行、仓库行、钉在顶上的行、压缩目录的每一段）和列表的空白处在右键按下时打开它
//     （treeMenuPress）；按下归了那一行，按着拖的移动和松开都还送到那一行，转给菜单（按住拖到项上松开选它）。
//   - 开着时窗口上盖一层透明的接收层（gmenuView）：点外面关掉菜单、这一下被吞掉（不落到底下的行上），
//     在外面滚动（滚文件树）、Esc、窗口失去焦点都关；键盘的 ↑↓→←、回车、打字跳项也是它接。
//   - 建菜单的函数和系统菜单共用（menus.go 用 menu / menuItem 接口），别处的 ContextMenu 不变。
//
// 为什么不用 mygo 的 PopoverMenu：它不能按住右键拖到项上松开，子菜单没有悬停延迟和安全三角，灰着的项说不了原因，
// 点外面的那一下还会落到底下。见 docs/mygo-pitfalls.md。

// menu / menuItem 是建菜单的函数用的接口：系统菜单经 nativeMenu 适配，Grove 画的菜单是 app.PopMenu 自己的。
type (
	menu     = app.MenuSink
	menuItem = app.MenuItem
)

// nativeMenu 把 mygo 的 *wui.Menu（ContextMenu / Menu / PopoverMenu 给的）包成 menu。
func nativeMenu(m *wui.Menu) menu { return wuiMenu{m} }

type wuiMenu struct{ m *wui.Menu }

type wuiItem struct {
	it    *wui.MenuItem
	m     *wui.Menu
	label string
}

func (w wuiMenu) Item(label string) menuItem { return wuiItem{w.m.Item(label), w.m, label} }
func (w wuiMenu) Separator()                 { w.m.Separator() }
func (w wuiMenu) Submenu(label string, build func(menu)) menuItem {
	return wuiItem{w.m.Submenu(label, func(m *wui.Menu) { build(wuiMenu{m}) }), w.m, label}
}

func (w wuiItem) Disabled(d bool) menuItem { w.it.Disabled(d); return w }
func (w wuiItem) Checked(on bool) menuItem { w.it.Checked(on); return w }
func (w wuiItem) Shortcut(mods wui.Modifiers, key wui.Key) menuItem {
	w.it.Shortcut(mods, key)
	return w
}

// Why 在系统菜单里没有悬停说明：灰着时什么也不加（标签要带原因的，建菜单时用 whyItem / whySub 拼好）。
func (w wuiItem) Why(why string) menuItem { w.it.Disabled(why != ""); return w }
func (w wuiItem) Chosen() bool            { return w.it.Chosen() }

// whyItem 加一项灰着说原因的（why 为空就是普通的一项）：系统菜单把原因括在标签后面（withWhy），
// Grove 画的菜单悬停出提示。
func whyItem(m menu, label, why string) menuItem {
	if _, ok := m.(wuiMenu); ok {
		label = withWhy(label, why)
	}
	return m.Item(label).Why(why)
}

// whySub 是 whyItem 的子菜单版：why 不空时子菜单灰着、打不开。
func whySub(m menu, label, why string, build func(menu)) menuItem {
	if _, ok := m.(wuiMenu); ok {
		label = withWhy(label, why)
	}
	return m.Submenu(label, build).Why(why)
}

// 菜单的样子：项的字号同侧栏，快捷键淡色右对齐；高亮是强调色。
const (
	gmenuRadius  = 6
	gmenuItemRad = 4
)

// treeMenuPress 给文件树里的元素装上右键菜单：右键按下打开 build 建的菜单（owner 是它对着的东西，那一行画描边），
// 按着时的移动和松开转给菜单。el 是装它的元素（建的时候取它上一帧的位置，事件里换算成窗口坐标）；other 是这个元素原来的 HandleInput（可以为空），
// 不是右键的事件照旧交给它。
func treeMenuPress(a *app.App, c *wui.Context, el wui.Element, owner any, build func(menu), other func(wui.InputEvent) bool) {
	held := false    // 这次右键按下是这个元素接的
	b := el.Bounds() // 上一帧的位置（事件里不能再用元素）
	el.HandleInput(func(ev wui.InputEvent) bool {
		m := &a.TreeMenu
		x, y := b.X+ev.X, b.Y+ev.Y
		switch ev.Kind {
		case wui.InputPointerDown:
			if ev.Button == 1 || ev.Button == 0 && ev.Mods&wui.Ctrl != 0 {
				a.TreeTip.Dismiss()
				root := c.Root().Bounds()
				gmenuMeasure(a, c)
				m.Open(owner, build, x, y, root.W, root.H, true, ev.Time)
				held = true
				return true
			}
		case wui.InputPointerMove:
			if held && m.IsOpen() {
				m.Move(x, y, ev.Time)
				return true
			}
		case wui.InputPointerUp:
			if held {
				held = false
				m.Up(x, y, ev.Button, ev.Time)
				return true
			}
		}
		if other != nil {
			return other(ev)
		}
		return false
	})
}

// gmenuMeasure 设好量字的函数（按菜单的字号）。
func gmenuMeasure(a *app.App, c *wui.Context) {
	a.TreeMenu.Measure = func(s string) float32 {
		w, _ := c.MeasureText(0, wui.Span{Text: s, Size: sideFont})
		return w
	}
}

// menuOwned 报文件树里对着 owner 的菜单开着（那一行画描边）。
func menuOwned(a *app.App, owner any) bool {
	return a.TreeMenu.IsOpen() && a.TreeMenu.Owner == owner
}

// gmenuView 画开着的菜单（在 root 里，所有东西之后）；刚选了一项的话先执行它。
func gmenuView(a *app.App, c *wui.Context) {
	m := &a.TreeMenu
	if m.Chosen() {
		m.RunChoice()
		c.Invalidate()
	}
	if !m.IsOpen() {
		a.MenuShown = 0
		return
	}
	root := c.Root().Bounds()
	gmenuMeasure(a, c)
	m.Refresh(root.W, root.H)
	now := c.Now()
	if wait := m.Tick(now); wait > 0 {
		c.After(wait)
	}
	t := c.Theme()
	wui.Overlay(c, func() {
		// 自己的 key：不然关掉以后同一处建的下一个 Overlay（比如选了一项弹出的出错框的底）会接着这一层的点击，当成点了它
		catcher := wui.Column(c.Key("treeMenu")).Absolute().Left(0).Top(0).Width(root.W).Height(root.H).Focusable().FocusRing(false)
		catcher.Label(i18n.T("右键菜单"))
		catcher.Focus()
		a.MenuShown++
		if a.MenuShown > 2 && !catcher.Focused() { // 窗口失去焦点（或者焦点被别处拿走）
			m.Close()
			c.Invalidate()
		}
		catcher.HandleInput(func(ev wui.InputEvent) bool {
			switch ev.Kind {
			case wui.InputPointerDown:
				return m.Down(ev.X, ev.Y, ev.Button, ev.Time)
			case wui.InputPointerUp:
				m.Up(ev.X, ev.Y, ev.Button, ev.Time)
			case wui.InputPointerMove:
				m.Move(ev.X, ev.Y, ev.Time)
			case wui.InputScroll:
				m.Scroll(ev.X, ev.Y, ev.DY)
			case wui.InputKeyDown:
				switch ev.Key {
				case wui.KeyEscape, wui.KeyUp, wui.KeyDown, wui.KeyLeft, wui.KeyRight, wui.KeyEnter, wui.KeySpace:
					return m.Key(ev.Key, "", ev.Time)
				}
				return ev.Mods&(wui.Cmd|wui.Ctrl) == 0 // 菜单开着时别的键不落到底下（⌘ 的快捷键照旧）
			case wui.InputText:
				return m.Key(0, ev.Text, ev.Time)
			default:
				return false
			}
			return true
		})
		catcher.Children(func() {
			path := m.OpenPath()
			tipLv, tipIdx, why, tip := m.Tip(now)
			for lv, l := range m.Levels() {
				open := -1
				if lv < len(path) {
					open = path[lv]
				}
				gmenuPanel(c, t, l, open)
				if tip && lv == tipLv {
					gmenuTip(c, t, l, tipIdx, why, root.W)
				}
			}
		})
	})
}

// gmenuBg 是菜单面板的底色：浅色白、深色比卡片亮一档。
func gmenuBg(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.RGBA(44, 44, 48, 1)
	}
	return wui.RGBA(255, 255, 255, 1)
}

// gmenuPanel 画一层：open 是这一层里开着子菜单的那项（高亮着）。
func gmenuPanel(c *wui.Context, t *wui.Theme, l app.MenuLevel, open int) wui.Element {
	r := l.Rect
	shadow := wui.RGBA(0, 0, 0, 0.18)
	if t.Dark {
		shadow = wui.RGBA(0, 0, 0, 0.45)
	}
	panel := wui.Column(c).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).Radius(gmenuRadius).
		Background(gmenuBg(t)).Border(1, t.Border.Alpha(0.9)).Shadow(0, 6, 18, 0, shadow).Clip().Role(wui.RoleMenu)
	panel.Children(func() {
		col := wui.Column(c).Absolute().Left(0).Top(-l.Scroll).Width(r.W).Padding(app.MenuPad)
		col.Children(func() {
			for i, it := range l.Items {
				if it.Sep {
					wui.Column(c).Height(app.MenuSepH).Shrink(0).Justify(wui.Center).Padding(0, 6).Children(func() {
						wui.Column(c).Height(1).Background(t.Border.Alpha(0.8))
					})
					continue
				}
				hot := l.Hot == i || open == i
				gmenuItem(c, t, it, hot)
			}
		})
	})
	return panel
}

func gmenuItem(c *wui.Context, t *wui.Theme, it app.MenuEntry, hot bool) {
	fg, muted := t.Text, t.TextMuted
	row := wui.Row(c).Height(app.MenuItemH).Shrink(0).AlignItems(wui.Center).Radius(gmenuItemRad).
		Padding(0, app.MenuTextR, 0, 0).Label(it.Label)
	switch {
	case it.Disabled:
		fg, muted = t.TextMuted.Alpha(0.6), t.TextMuted.Alpha(0.45)
		row.Disabled(true)
	case hot:
		row.Background(t.Accent)
		fg, muted = t.AccentText, t.AccentText.Alpha(0.75)
	}
	switch {
	case it.HasSub:
		row.Role(wui.RoleMenuItem)
	case it.Checked:
		row.Role(wui.RoleMenuItemCheckBox).Checked(true)
	default:
		row.Role(wui.RoleMenuItem)
	}
	row.Children(func() {
		wui.Row(c).Width(app.MenuTextL).Shrink(0).Justify(wui.Center).Children(func() {
			if it.Checked {
				wui.Icon(c, iconCheck).FontSize(iconInline).TextColor(fg)
			}
		})
		wui.Text(c, it.Label).FontSize(sideFont).TextColor(fg).SingleLine().Grow(1).MinWidth(0)
		if it.Keys != "" {
			wui.Text(c, it.Keys).FontSize(sideFont).TextColor(muted).SingleLine().Shrink(0).Margin(0, 0, 0, app.MenuKeysGap)
		}
		if it.HasSub {
			wui.Row(c).Width(app.MenuArrowW).Shrink(0).Justify(wui.End).Children(func() {
				wui.Icon(c, iconChevronRight).FontSize(iconInline).TextColor(muted)
			})
		}
	})
}

// gmenuTip 是灰着的项的原因：画在那项右边（右边放不下画在左边），和树行提示一样的反色小块。
func gmenuTip(c *wui.Context, t *wui.Theme, l app.MenuLevel, i int, why string, winW float32) {
	fill, text := t.Inverse, t.InverseText
	if fill == wui.Transparent {
		fill = t.Text
	}
	if text == wui.Transparent {
		text = t.Background
	}
	r := l.Rect
	y := r.Y + l.ItemY(i) - l.Scroll
	w := min(t.Space(80), winW/2)
	x := r.X + r.W + 4
	tip := wui.Column(c).Absolute().Top(y).MaxWidth(w).PassThrough().Role(wui.RoleTooltip)
	if x+w > winW && r.X-w-4 >= 0 {
		tip.Right(winW - r.X + 4)
	} else {
		tip.Left(x)
	}
	tip.Padding(t.Space(1.25), t.Space(2)).Radius(t.Space(1.25)).Background(fill).TextColor(text).FontSize(t.FontSize-1).
		Shadow(0, 2, 8, 0, wui.RGBA(0, 0, 0, 0.2))
	tip.Children(func() { wui.Text(c, why).PassThrough() })
}
