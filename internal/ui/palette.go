package ui

import (
	"strings"
	"unicode/utf8"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
)

// paletteRows 是面板列表最多同时显示几行，多了滚动。
const (
	paletteRows = 12
	paletteRowH = 28
)

// palette 是快速打开 / 命令面板（⌘P、⇧⌘P、⌘K）：窗口上方居中的浮层，输入框加列表。
// ↑ ↓ 选，回车或点一项执行，Esc 或点外面关掉。按输入的开头分哪几种模式、每项做什么见 app/palette.go。
func palette(a *app.App, c *wui.Context) {
	p := &a.Palette
	if !p.Open {
		return
	}
	a.UpdatePalette()
	t := c.Theme()
	wui.DialogBase(c, &p.Open, func(back, panel wui.Element) {
		back.Justify(wui.Start).Padding(56, 16, 16, 16)
		panel.Width(640).Padding(6).Gap(4).Radius(dlgRadius).Background(t.Background).Border(1, t.Border).
			Shadow(0, 12, 32, 0, wui.RGBA(0, 0, 0, 0.22)) // 和 dialogFrame 一样的圆角、阴影
		wui.Row(c).Padding(2, 8).Gap(8).AlignItems(wui.Center).Children(func() {
			icon := iconSearch
			if strings.HasPrefix(p.Text, ">") {
				icon = iconChevronRight
			}
			wui.Icon(c, icon).FontSize(iconTool).TextColor(t.TextMuted)
			in := wui.TextInputBase(c.Key("in"), &p.Text).Label(i18n.T("快速打开或命令")).
				Placeholder(i18n.T("按名字找文件；> 命令，>git 运行 git 命令，:行号 跳行")).FontSize(14).Grow(1).MinWidth(0).Height(30).AutoFocus().
				OnTextKey(func(k wui.TextKey) bool {
					switch {
					case k.Mods == wui.Alt && k.Key == wui.KeyEnter:
						a.AcceptPaletteSide(p.Sel) // 文件项在侧边打开
					case k.Mods != 0:
						return false
					case k.Key == wui.KeyDown:
						a.MovePalette(1)
					case k.Key == wui.KeyUp:
						a.MovePalette(-1)
					default:
						return false
					}
					return true
				})
			if in.Submitted() {
				a.AcceptPalette(p.Sel)
			}
		})
		if p.Note != "" {
			wui.Text(c, p.Note).FontSize(11).TextColor(t.TextMuted).Padding(0, 10, 2, 10).SingleLine().Ellipsis("…").Tooltip(p.Note)
		}
		if len(p.Items) == 0 {
			return
		}
		wui.Divider(c)
		paletteList(a, c)
	})
	if !p.Open {
		a.ClosePalette()
	}
}

// paletteList 是面板的列表：文件（名字、灰的仓库和目录，命中的字高亮）、命令（名字、分组、快捷键）、git 命令。
func paletteList(a *app.App, c *wui.Context) {
	p := &a.Palette
	p.List.Selected = &p.Sel
	p.List.Label = func(i int) string {
		it := p.Items[i]
		switch it.Kind {
		case app.ItemFile:
			return i18n.Tf("打开 %s", it.Text)
		case app.ItemCommand:
			return i18n.Tf("命令 %s", it.Text)
		case app.ItemProfile:
			return i18n.Tf("用 %s 新建终端", it.Text)
		case app.ItemTask:
			return i18n.Tf("运行 %s", it.Text)
		case app.ItemSymbol:
			if it.Loc != "" {
				return i18n.Tf("%s，在 %s", it.Text, it.Loc)
			}
		}
		return it.Text
	}
	clicked := -1
	h := float32(min(len(p.Items), paletteRows) * paletteRowH)
	l := newListLook(a, c, 0)
	l.active = true // 键盘在输入框里，上下键移的是这个列表
	l.build(c, func() {
		wui.List(c, &p.List, len(p.Items)).Height(h).Shrink(0).Rows(l.rows(func(row wui.ListRow) {
			if paletteRow(a, row, p.Items[row.Index]) {
				clicked = row.Index
			}
		}))
	})
	if clicked >= 0 {
		a.AcceptPalette(clicked)
	}
}

// paletteRow 画一项，返回它是不是被点了。
func paletteRow(a *app.App, row wui.ListRow, it app.PaletteItem) bool {
	c := row.Context
	t := c.Theme()
	// 选中的行由列表画成强调色，上面的字跟着换成强调色上的字（淡底时不换）
	sel := onAccent(a, row.Selected())
	text, muted, hl := t.Text, t.TextMuted, t.Accent.Alpha(0.22)
	if sel {
		text, muted, hl = t.AccentText, t.AccentText.Alpha(0.75), t.AccentText.Alpha(0.28)
	}
	if it.Off {
		text = muted.Alpha(0.7)
	}
	el := wui.Row(c).Height(paletteRowH).Padding(0, 10).Gap(8).AlignItems(wui.Center).Cursor(wui.CursorPointer)
	if it.Under {
		el.Padding(0, 10, 0, 30) // "名字@符号"：符号缩在文件下面
	}
	el.Children(func() {
		switch it.Kind {
		case app.ItemFile:
			name, dir := it.Text[it.Name:], strings.TrimSuffix(it.Text[:it.Name], "/")
			fileGlyph(a, c, name, sel).Shrink(0)
			hitSpans(c, name, it.Hits, it.Name, 13, text, hl).Shrink(0).MaxWidth(360)
			hitSpans(c, dir, it.Hits, 0, 11.5, muted, hl).MinWidth(0).Shrink(1)
			wui.Spacer(c).Grow(1)
			if it.Recent {
				wui.Text(c, i18n.T("最近打开")).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0)
			}
		case app.ItemCommand:
			hitSpans(c, it.Text, it.Hits, 0, 13, text, hl).MinWidth(0).Shrink(1)
			wui.Text(c, it.Detail).FontSize(11).TextColor(muted).SingleLine().Shrink(0)
			wui.Spacer(c).Grow(1)
			if it.Keys != "" {
				wui.Text(c, it.Keys).FontSize(11.5).TextColor(muted).SingleLine().Shrink(0)
			}
		case app.ItemGit, app.ItemGitHistory, app.ItemTerm:
			ic := iconTerminal
			if it.Kind == app.ItemGitHistory {
				ic = iconHistory
			}
			wui.Icon(c, ic).FontSize(iconRow).TextColor(muted).Shrink(0)
			wui.Text(c, it.Text).Font(mono()).FontSize(12.5).TextColor(text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			wui.Spacer(c).Grow(1)
			wui.Text(c, it.Detail).FontSize(11).TextColor(muted).SingleLine().Shrink(0)
		case app.ItemSymbol:
			wui.Text(c, symbolGlyph(it.Sym)).FontSize(12).FontWeight(600).TextColor(muted).Width(14).Shrink(0)
			if it.Loc != "" { // "#"：名字、签名，右边是在哪
				hitSpans(c, it.Text, it.Hits, 0, 13, text, hl).Shrink(0).MaxWidth(360)
				wui.Text(c, it.Detail).FontSize(11.5).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				wui.Spacer(c).Grow(1)
				wui.Text(c, it.Loc).FontSize(11).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).MaxWidth(420)
				break
			}
			hitSpans(c, it.Text[it.Name:], it.Hits, it.Name, 13, text, hl).Shrink(0).MaxWidth(360)
			hitSpans(c, strings.TrimSuffix(it.Text[:it.Name], " › "), it.Hits, 0, 11.5, muted, hl).MinWidth(0).Shrink(1)
			wui.Spacer(c).Grow(1)
			wui.Text(c, it.Detail).FontSize(11).TextColor(muted).SingleLine().Shrink(0)
		case app.ItemProfile:
			wui.Icon(c, iconTerminal).FontSize(iconRow).TextColor(muted).Shrink(0)
			hitSpans(c, it.Text, it.Hits, 0, 13, text, hl).Shrink(0).MaxWidth(260)
			wui.Spacer(c).Grow(1)
			wui.Text(c, it.Detail).Font(mono()).FontSize(11.5).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		case app.ItemTask: // 命令（等宽，命中高亮）在前、仓库灰字在后，右边是哪个文件认出来的
			wui.Icon(c, iconPlay).FontSize(iconRow).TextColor(muted).Shrink(0)
			hitSpans(c, it.Text[it.Name:], it.Hits, it.Name, 12.5, text, hl).Font(mono()).MinWidth(0).Shrink(1).MaxWidth(420)
			hitSpans(c, strings.TrimSuffix(it.Text[:it.Name], " · "), it.Hits, 0, 11.5, muted, hl).Shrink(0).MaxWidth(160)
			wui.Spacer(c).Grow(1)
			if it.Recent {
				wui.Text(c, i18n.T("最近跑过")).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0)
			}
			wui.Text(c, it.Detail).FontSize(11).TextColor(muted).SingleLine().Shrink(0)
		case app.ItemLine:
			wui.Icon(c, iconChevronRight).FontSize(iconRow).TextColor(muted).Shrink(0)
			wui.Text(c, it.Text).FontSize(13).TextColor(text).SingleLine().Shrink(0)
			wui.Spacer(c).Grow(1)
			wui.Text(c, it.Detail).FontSize(11).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		}
	})
	return el.Clicked()
}

// hitSpans 画 s（在原文里从 off 开始），hits 里落在它上面的字加底色。
func hitSpans(c *wui.Context, s string, hits []int, off int, size float32, color, hl wui.Color) wui.Element {
	return hitText(c, s, hits, off, hl).FontSize(size).TextColor(color)
}

// hitText 和 hitSpans 一样，只是字号、颜色、粗细、删除线都跟着元素（调用方在返回的元素上设，文件树筛选的行用）。
func hitText(c *wui.Context, s string, hits []int, off int, hl wui.Color) wui.Element {
	var spans []wui.Span
	plain := func(from, to int) {
		if from < to {
			spans = append(spans, wui.Span{Text: s[from:to]})
		}
	}
	at := 0
	for _, h := range hits {
		i := h - off
		if i < at || i >= len(s) {
			continue
		}
		plain(at, i)
		n := 1
		for i+n < len(s) && !utf8.RuneStart(s[i+n]) {
			n++
		}
		spans = append(spans, wui.Span{Text: s[i : i+n], Background: hl, Radius: 2})
		at = i + n
	}
	plain(at, len(s))
	if len(spans) == 0 {
		spans = append(spans, wui.Span{})
	}
	return wui.RichText(c, spans...).SingleLine().Ellipsis("…")
}
