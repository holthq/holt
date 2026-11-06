package ui

import (
	"math"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
)

// 文件树的右键菜单是 Grove 自己画的（issue #48），状态机是 app.PopMenu（App.Menu），这里只管喂事件和画：
//
//   - 文件树的行（文件 / 目录行、仓库行、钉在顶上的行、压缩目录的每一段）和列表的空白处在右键按下时打开它
//     （menuPress）；按下归了那一行，按着拖的移动和松开都还送到那一行，转给菜单（按住拖到项上松开选它）。
//   - 开着时窗口上盖一层透明的接收层（gmenuView）：点外面关掉菜单、这一下被吞掉（不落到底下的行上），
//     在外面滚动（滚文件树）、Esc、窗口失去焦点都关；键盘的 ↑↓→←、回车、打字跳项也是它接。
//   - 开着时在文件树别处右键（或 ⌃点）：关掉这份、在那里开一份新的（menuPress 每帧把能开菜单的地方登记给
//     PopMenu.AddTarget）；左键和别的键点外面只关、被吞掉。
//   - 文件树有键盘时 ⇧F10 / 菜单键在光标那一行下面打开它（openTreeMenuByKey），第一项高亮着。
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

// 菜单的样子：字号 13，快捷键、子菜单箭头淡色右对齐；悬停是淡淡的一层填充（字色不变），不是实心的强调色块。
const (
	gmenuRadius  = 8
	gmenuItemRad = 5
	gmenuFont    = 13
)

// menuPress 给文件树里的元素装上右键菜单：右键按下打开 build 建的菜单（owner 是它对着的东西，那一行画描边），
// 按着时的移动和松开转给菜单。el 是装它的元素（建的时候取它上一帧的位置，事件里换算成窗口坐标）；other 是这个元素原来的 HandleInput（可以为空），
// 不是右键的事件照旧交给它。
func menuPress(a *app.App, c *wui.Context, el wui.Element, owner any, build func(menu), other func(wui.InputEvent) bool) {
	held := false    // 这次右键按下是这个元素接的
	b := el.Bounds() // 上一帧的位置（事件里不能再用元素）
	// 登记成能开菜单的地方：菜单开着时右键这里，关掉那份、在这里开新的（接收层盖着，事件到不了这个元素）
	a.Menu.AddTarget(app.MenuRect{X: b.X, Y: b.Y, W: b.W, H: b.H}, menuZ(owner), owner, build)
	el.HandleInput(func(ev wui.InputEvent) bool {
		m := &a.Menu
		x, y := b.X+ev.X, b.Y+ev.Y
		switch ev.Kind {
		case wui.InputPointerDown:
			if ev.Button == 1 || ev.Button == 0 && ev.Mods&wui.Ctrl != 0 {
				a.TreeTip.Dismiss()
				root := c.Root().Bounds()
				gmenuMeasure(a, c)
				m.OpenRow(owner, build, x, y, app.MenuRect{X: b.X, Y: b.Y, W: b.W, H: b.H}, root.W, root.H, true, ev.Time)
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

// menuOwner 是文件树以外的菜单对着的东西（比较用，要可比较）：哪一类 + 哪个仓库 / 哪一项。
type menuOwner struct{ Kind, Key string }

// ctxMenu 给 el 装上 Grove 画的右键菜单（文件树以外的地方用它代替 ContextMenu）：和文件树的行一样，
// 右键按下打开、按住拖到项上松开选它，菜单开着时在别处右键换成那里的。el 不能再挂别的 HandleInput。
//
// el 有键盘（或键盘在它里面）时 ⇧F10 / 菜单键在它左下角下面开同一份菜单，第一项高亮（menuKeyTarget）。
func ctxMenu(a *app.App, c *wui.Context, el wui.Element, owner menuOwner, build func(menu)) {
	menuPress(a, c, el, owner, build, nil)
	if el.FocusWithin() {
		menuKeyTarget(a, el, owner, build)
	}
}

// ctxMenuRow 是列表行上的 ctxMenu：键盘在列表上、这一行是选中的那行时 ⇧F10 / 菜单键开它的菜单
// （行自己不拿键盘，键盘在列表上）。
func ctxMenuRow(a *app.App, c *wui.Context, row wui.ListRow, el wui.Element, owner menuOwner, build func(menu)) {
	menuPress(a, c, el, owner, build, nil)
	if row.Selected() && row.ListFocused() {
		menuKeyTarget(a, el, owner, build)
	}
}

// menuDo 给建菜单的函数里"等列表建完再做"的动作（搜索结果的 do）用：Grove 菜单选中的项是在整棵树建完之后、
// gmenuView 里再跑一遍建菜单的函数时执行的，列表早建完了，直接做。建菜单时捕获的 do 是打开菜单那一帧的，
// 那一帧的列表早就过去了，交给它的动作没人执行。
func menuDo(fn func()) { fn() }

// menuKeyTarget 登记这一帧键盘能开菜单的地方（后登记的是里面的，盖掉外面的）；帧末 gmenuView 认 ⇧F10 / 菜单键。
func menuKeyTarget(a *app.App, el wui.Element, owner menuOwner, build func(menu)) {
	b := el.Bounds()
	a.MenuKeyAt = &app.MenuKeyTarget{Owner: owner, Build: build, X: b.X, Y: b.Y + b.H}
}

// openMenuBelow 在按钮 b（上一帧的位置）左下角下面打开 build 建的 Grove 菜单（按钮的菜单用它代替 Menu）。
// 下面放不下往上翻、窗口都放不下就滚（PopMenu.layout）。
func openMenuBelow(a *app.App, c *wui.Context, b wui.Rect, owner menuOwner, build func(menu)) {
	root := c.Root().Bounds()
	gmenuMeasure(a, c)
	a.TreeTip.Dismiss()
	a.Menu.Open(owner, build, b.X, b.Y+b.H+2, root.W, root.H, false, c.Now())
	c.Invalidate()
}

// menuZ 是 owner 那块盖在谁上面：空白处（列表）0、行 1、合并行的一段 2，钉在顶上的再加 2（盖着底下滚过去的行）。
func menuZ(owner any) int {
	o, ok := owner.(treeOwner)
	if !ok { // 文件树以外的（Git 栏、标签……）：不受文件树列表的裁剪
		return app.MenuZFree
	}
	if o.Blank {
		return 0
	}
	z := 1
	if o.Seg != "" {
		z = 2
	}
	if o.Pin {
		z += 2
	}
	return z
}

// isMenuKey 报 ev 是键盘开右键菜单的键：菜单键，或者 ⇧F10（和 mygo 给 ContextMenu / PopoverMenu 的一样）。
func isMenuKey(ev wui.InputEvent) bool {
	return ev.Kind == wui.InputKeyDown && (ev.Key == wui.KeyContextMenu && ev.Mods == 0 || ev.Key == wui.KeyF10 && ev.Mods == wui.Shift)
}

// openTreeMenuByKey 是文件树有键盘时按了 ⇧F10 / 菜单键：在光标那一行左边、紧挨着它下面打开那一行的菜单
// （和右键它的一样，多选里的一行对选中的全部），第一个能选的项高亮着；没有光标行（或它不在视野里）时开空白处的菜单，
// 在列表左上角。关菜单时没选什么（Esc、点外面）键盘回到文件树（gmenuView）。list 是列表上一帧的位置。
func openTreeMenuByKey(a *app.App, c *wui.Context, rows []app.TreeRow, list wui.Rect, now time.Time) {
	a.TreeTip.Dismiss()
	root := c.Root().Bounds()
	gmenuMeasure(a, c)
	owner, build := any(treeOwner{Blank: true}), func(m menu) { treeMenu(a, m) }
	x, y := list.X+8, list.Y+4
	if i := a.FileSel; i >= 0 && i < len(rows) {
		if r, ok := a.TreeSpans[i]; ok && r.H > 0 && r.Y+r.H > list.Y && r.Y < list.Y+list.H {
			tr := rows[i]
			owner, build = treeOwner{Key: tr.Key()}, treeRowBuild(a, tr)
			x, y = r.X, min(r.Y+r.H, list.Y+list.H)
		}
	}
	a.Menu.OpenByKey(owner, build, x, y, root.W, root.H, now)
	a.MenuByKey = true
}

// gmenuMeasure 设好量字的函数（按菜单的字号）。
func gmenuMeasure(a *app.App, c *wui.Context) {
	a.Menu.Measure = func(s string) float32 {
		w, _ := c.MeasureText(0, wui.Span{Text: s, Size: gmenuFont})
		return w
	}
}

// menuOwned 报文件树里对着 owner 的菜单开着（那一行画描边）。
func menuOwned(a *app.App, owner any) bool {
	return a.Menu.IsOpen() && a.Menu.Owner == owner
}

// gmenuView 画开着的菜单（在 root 里，所有东西之后）；刚选了一项的话先执行它。
func gmenuView(a *app.App, c *wui.Context) {
	m := &a.Menu
	m.EndFrame() // 这一帧的树已经建完，能开菜单的地方都登记了
	if k := a.MenuKeyAt; k != nil {
		a.MenuKeyAt = nil
		// 只在有这样的元素时登记这两个键：别处照旧归 mygo（文本框的系统菜单）和文件树自己（openTreeMenuByKey）
		if !m.IsOpen() && (c.Shortcut(wui.Shift, wui.KeyF10) || c.Shortcut(0, wui.KeyContextMenu)) {
			root := c.Root().Bounds()
			gmenuMeasure(a, c)
			a.TreeTip.Dismiss()
			m.OpenByKey(k.Owner, k.Build, k.X, k.Y, root.W, root.H, c.Now())
			c.Invalidate()
		}
	}
	chose := m.Chosen()
	if chose {
		m.RunChoice()
		c.Invalidate()
	}
	if !m.IsOpen() {
		if a.MenuByKey && a.MenuShown > 0 && !chose {
			a.FocusTree = true // 键盘开的菜单没选就关了：键盘回到文件树
		}
		a.MenuShown, a.MenuByKey = 0, false
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
		a.MenuShown++
		// 第一帧不拿键盘：mygo 在 Overlay 出现的那一帧（fn 跑完以后）记下当时有键盘的元素，接收层消失时把键盘还给它
		// （restoreFocus）。这一帧就 Focus 的话记下的是接收层自己，关了菜单键盘就没了着落。下一帧马上补上。
		if a.MenuShown > 1 {
			catcher.Focus()
		} else {
			c.Invalidate()
		}
		if a.MenuShown > 2 && !catcher.Focused() { // 窗口失去焦点（或者焦点被别处拿走）
			m.Close()
			c.Invalidate()
		}
		catcher.HandleInput(func(ev wui.InputEvent) bool {
			switch ev.Kind {
			case wui.InputPointerDown:
				b := ev.Button
				if b == 0 && ev.Mods&wui.Ctrl != 0 {
					b = 1 // ⌃点和右键一样
				}
				if b == 1 && !m.Contains(ev.X, ev.Y) {
					a.TreeTip.Dismiss()
					a.MenuByKey = false // 右键在别处开的新菜单是鼠标开的
				}
				return m.Down(ev.X, ev.Y, b, ev.Time)
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

// gmenuHot 是高亮项（悬停、键盘挪到、开着子菜单的）的底和字：底是字色 8% 左右的淡填充，字色不变
// （实心的强调色块太重，侧栏里的行也是淡填充）。深色主题里同样的比例看着更淡，提一点。
func gmenuHot(t *wui.Theme) (bg, fg wui.Color) {
	if t.Dark {
		return t.Text.Alpha(0.1), t.Text
	}
	return t.Text.Alpha(0.08), t.Text
}

// contrast 是两个颜色的对比度（WCAG 的算法，1–21），不看透明度。
func contrast(a, b wui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c wui.Color) float64 {
	ch := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
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
						wui.Column(c).Height(1).Background(t.Border.Alpha(0.5))
					})
					continue
				}
				hot := l.Hot == i || open == i
				gmenuItem(c, t, it, hot, l.TextL())
			}
		})
	})
	return panel
}

// gmenuItem 画一项；textL 是字左边留的（这一层有勾选项时留出勾的位置，见 MenuLevel.TextL）。
func gmenuItem(c *wui.Context, t *wui.Theme, it app.MenuEntry, hot bool, textL float32) {
	fg, muted := t.Text, t.TextMuted
	row := wui.Row(c).Height(app.MenuItemH).Shrink(0).AlignItems(wui.Center).Radius(gmenuItemRad).
		Padding(0, app.MenuTextR, 0, 0).Label(it.Label)
	switch {
	case it.Disabled:
		fg, muted = t.TextMuted.Alpha(0.6), t.TextMuted.Alpha(0.45)
		row.Disabled(true)
	case hot:
		bg, text := gmenuHot(t)
		row.Background(bg)
		fg = text
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
		wui.Row(c).Width(textL).Shrink(0).Justify(wui.Center).Children(func() {
			if it.Checked {
				wui.Icon(c, iconCheck).FontSize(iconInline).TextColor(fg)
			}
		})
		wui.Text(c, it.Label).FontSize(gmenuFont).TextColor(fg).SingleLine().Grow(1).MinWidth(0)
		if it.Keys != "" {
			wui.Text(c, it.Keys).FontSize(gmenuFont).TextColor(muted).SingleLine().Shrink(0).Margin(0, 0, 0, app.MenuKeysGap)
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
