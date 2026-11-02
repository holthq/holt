package ui

import (
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是设置里的强调色和"选中行用淡底"。mygo 的主题跟着系统的强调色；选了别的强调色时，每帧开头
// （root、设置窗口、关于窗口）在这一帧的主题上换掉 Accent 那几个颜色（applyAccent）。列表的选中行默认是淡底：
// 强调色淡淡的一层、字不换色、左边一道强调色的竖条（listLook）；关掉淡底是 mygo 原来的实心块、行上的字换成 AccentText。
//
// 少画：强调色只留给“这里是当前 / 焦点 / 可以放”——焦点环、主按钮、勾选和开关、当前标签组和底栏标签的 2 pt 条、
// 选中行的底和竖条、拖放的落点、搜索命中的高亮、对话框的信息 / 提问图标、几处按类别分的色板（泳道、ours / theirs、R / C）、进度条。
// 别的角标、胶囊、横幅、文字按钮、图标都是灰的（quietFill 的底、TextMuted 或正文色的字）。

// accentColors 是能选的强调色（跟随系统的 "system" 不在这里）：浅色、深色主题各一个；
// darkText 是深色主题里强调色上的字，零值是白色（石墨的深色太浅，白字看不清）。
var accentColors = map[string]struct{ light, dark, darkText wui.Color }{
	"steel":    {wui.Hex("#4E6E8E"), wui.Hex("#8FA9C6"), wui.Hex("#18181B")}, // 默认：白字 5.3:1；深色的太亮，白字只有 2.4:1，换深字
	"ink":      {wui.Hex("#3B5B84"), wui.Hex("#7A9CC6"), wui.Color{}},
	"graphite": {wui.Hex("#6E7075"), wui.Hex("#9A9CA3"), wui.Hex("#18181B")},
	"teal":     {wui.Hex("#2E8B78"), wui.Hex("#4FB8A2"), wui.Color{}},
	"indigo":   {wui.Hex("#5E5CE6"), wui.Hex("#7D7AFF"), wui.Color{}},
}

// applyAccent 把设置里的强调色套到这一帧的主题 t 上（mygo 每帧给一份新的，可以直接改）。跟随系统时不动。
// 悬停 / 按下浅色主题里深 8% / 16%，深色主题里亮 8% / 16%；选中的字和焦点环的透明度和 mygo 跟系统时一样，
// 高对比度时焦点环照旧不透明。
func applyAccent(a *app.App, t *wui.Theme) {
	k, ok := accentColors[workspace.AccentOf(a.St.Settings.Accent)]
	if !ok {
		return
	}
	black, white := wui.Hex("#000000"), wui.Hex("#ffffff")
	solidFocus := t.Focus.A == 255
	c, to, text := k.light, black, white
	sel, focus := float32(0.25), float32(0.55)
	if t.Dark {
		c, to, sel, focus = k.dark, white, 0.4, 0.6
		if k.darkText.A > 0 {
			text = k.darkText
		}
	}
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = c, c.Mix(to, 0.08), c.Mix(to, 0.16), text
	t.Selection, t.Focus = c.Alpha(sel), c.Alpha(focus)
	if solidFocus {
		t.Focus.A = 255
	}
}

// onAccent 是这一行要不要按"在强调色上"画（字、图标换成 AccentText）：选中了、选中行又是实心的。
func onAccent(a *app.App, sel bool) bool { return sel && a.St.Settings.SolidSelection }

// listLook 是一个列表的选中行怎么画。mygo 的列表拿建列表时的主题画选中块（Accent 的底、AccentText 的字），
// 行也在这份主题下建；淡底时建列表用一份 Accent 换成淡色、AccentText 换成正文色的主题，
// 行里的东西（按钮、勾选框、胶囊）还在真的主题下建，选中行再加左边的竖条。
// 实心、圆角又没改时 list 就是 c.Theme()，什么都不换。
type listLook struct {
	soft bool
	list *wui.Theme // 建列表用的
	real *wui.Theme // 建行用的（淡底时）：真的主题，圆角和列表一样
	bg   wui.Color  // 窗口根的底色：行可能在布局时才建，SetTheme 会把它改成主题的底色，建完换回来
}

// newListLook 照设置给列表一个 listLook；radius > 0 时选中块的圆角换成它（侧栏那样内缩的圆角行）。
func newListLook(a *app.App, c *wui.Context, radius float32) *listLook {
	t := c.Theme()
	l := &listLook{soft: !a.St.Settings.SolidSelection, list: t, real: t, bg: rootBg(c, t)}
	if radius > 0 && radius != t.Radius {
		rt := *t
		rt.Radius = radius
		l.list, l.real = &rt, &rt
	}
	if l.soft {
		lt := *l.real
		lt.Accent, lt.AccentText = t.Accent.Alpha(softAlpha(t)), t.Text
		l.list = &lt
	}
	return l
}

// softAlpha 是淡底的选中行上强调色的透明度。
func softAlpha(t *wui.Theme) float32 {
	if t.Dark {
		return 0.22
	}
	return 0.14
}

// build 在列表的主题下建 fn 里的列表。
func (l *listLook) build(c *wui.Context, fn func()) {
	if l.list == c.Theme() {
		fn()
		return
	}
	withTheme(c, l.list, fn)
}

// rows 包一层列表的行函数（见 row）。
func (l *listLook) rows(fn func(wui.ListRow)) func(wui.ListRow) {
	if !l.soft {
		return fn
	}
	return func(row wui.ListRow) { l.row(row.Context, row.Selected(), row.ListFocused(), func() { fn(row) }) }
}

// row 建一行：淡底时换回真的主题建，选中的行左边加一道强调色的竖条（列表有键盘时粗一点）。
func (l *listLook) row(c *wui.Context, sel, focused bool, fn func()) {
	if !l.soft {
		fn()
		return
	}
	withTheme(c, l.real, func() {
		fn()
		if sel {
			w := float32(2)
			if focused {
				w = 3
			}
			wui.Box(c).Absolute().Left(0).Top(softBarInset).Bottom(softBarInset).Width(w).Radius(1).Background(l.real.Accent)
		}
	})
	c.Root().Background(l.bg)
}

// softBarInset 是淡底选中行左边竖条上下离行边的距离。
const softBarInset = 4

// quietFill 是少画以后灰胶囊、灰角标的底：浅色字色 7%，深色 12%。
func quietFill(t *wui.Theme) wui.Color {
	if t.Dark {
		return t.Text.Alpha(0.12)
	}
	return t.Text.Alpha(0.07)
}

// bannerFill 是标签上面那些横幅（终端快照、设置文档、搜索编辑器、替换预览、选中几行、变基）的底：浅色字色 5%，深色 8%。
func bannerFill(t *wui.Theme) wui.Color {
	if t.Dark {
		return t.Text.Alpha(0.08)
	}
	return t.Text.Alpha(0.05)
}
