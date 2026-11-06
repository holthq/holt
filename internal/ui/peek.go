package ui

import (
	"strconv"
	"unicode"
	"unicode/utf8"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
)

// 编辑器装订线上改动块的就地卡（app/peek.go）：点改动条或改动块里的行号，在这一块下面弹一张卡。
// 照粘性滚动（sticky.go）的写法叠在编辑器上：编辑器先建，卡是 Absolute，按 LineTop - ScrollY 跟着文字滚；
// 左边从装订线右缘起，右到编辑器右缘留 8，最高编辑器高的三分之一，旧行多了在卡里滚。

type (
	peekKey     struct{ d *docs.Doc }
	peekWrapKey struct{ d *docs.Doc }
	peekClipKey struct{ d *docs.Doc }
)

// peekSetup 在建编辑器之前：卡跟上光标（app.SyncPeek），有改动块时接管装订线的点击——点在块里开 / 关卡，
// 块外的行号照旧选中那一行（mygo 的 OnGutterClick 一设就是整条装订线，拖着选几行只在没有改动块的文件里有）。
func peekSetup(a *app.App, d *docs.Doc) {
	a.SyncPeek(d)
	d.St.OnGutterClick = nil
	if len(app.DocHunks(d)) == 0 {
		return
	}
	d.St.OnGutterClick = func(line int) {
		if a.TogglePeek(d, line) {
			return
		}
		if line < 0 || line >= d.Buf.LineCount() {
			return
		}
		start := d.Buf.Offset(editor.Pos{Line: line})
		end := d.Buf.Len()
		if line+1 < d.Buf.LineCount() {
			end = d.Buf.Offset(editor.Pos{Line: line + 1})
		}
		d.St.Select(start, end)
	}
}

// peekEditor 是没有 blame、没有粘性滚动时的编辑器：同样套一层 Row，卡叠在上面。
func peekEditor(a *app.App, c *wui.Context, d *docs.Doc, build func() wui.Element) wui.Element {
	row := wui.Row(c.Key(peekWrapKey{d})).Grow(1).MinHeight(0).AlignItems(wui.Stretch)
	var ed wui.Element
	row.Children(func() {
		ed = build().Grow(1).MinWidth(0)
		peekCard(a, c, d, row, 0)
	})
	return ed
}

// peekCard 是卡（d 上没开就什么也不画）：row 是编辑器外面那一层，left 是编辑器左边离 row 左边多远（开着 blame 时是那一列的宽）。
// 外面一层撑满 row 的透明框只管裁掉滚出编辑器的部分（PassThrough，点击照旧落到编辑器上）。
func peekCard(a *app.App, c *wui.Context, d *docs.Doc, row wui.Element, left float32) {
	h, i, n, ok := a.PeekHunk(d)
	if !ok {
		return
	}
	st := &d.St
	lines := d.Buf.LineCount()
	y0 := st.ScrollY()
	lh := float32(st.LineTop(1) - st.LineTop(0))
	if lh <= 0.5 {
		c.Invalidate() // 编辑器还没排过版
		return
	}
	total := st.LineTop(lines)
	top := float32(st.LineTop(min(h.NewEnd, lines)) - y0)
	svc := c.Services()
	check := func(*wui.Painter, wui.Rect) {
		if st.ScrollY() != y0 || st.LineTop(lines) != total {
			svc.Invalidate()
		}
	}
	rb := row.Bounds()
	maxH := float32(240)
	if rb.H > 0 {
		maxH = max(rb.H/3, 90)
	}
	g := stickyGutter(c, st, lines, lh)
	t := c.Theme()
	ct := editor.DefaultTheme(t)
	clip := wui.Box(c.Key(peekClipKey{d})).Absolute().Top(0).Bottom(0).Left(left + g.textX - 4).Right(8).Clip().PassThrough().Draw(check)
	clip.Children(func() {
		card := wui.Column(c.Key(peekKey{d})).Absolute().Top(top).Left(0).Right(0).MaxHeight(maxH).Clip().
			Background(ct.Background).Border(1, t.Border).Radius(6).Shadow(0, 4, 12, 0, wui.RGBA(0, 0, 0, 0.16)).
			Label(i18n.T("这处改动"))
		if card.OverlayShortcut(0, wui.KeyEscape) {
			a.ClosePeek()
		}
		card.Children(func() {
			peekHead(a, c, d, h, i, n)
			peekBody(c, d, h, lh, ct)
		})
	})
}

// peekHead 是卡的头一行：第 k / n 处改动 · −a +b，右边 ↑ ↓、还原、暂存、在 Diff 中打开、✕。
func peekHead(a *app.App, c *wui.Context, d *docs.Doc, h editor.DiffHunk, i, n int) {
	t := c.Theme()
	ct := editor.DefaultTheme(t)
	wui.Row(c).Padding(4, 6, 4, 10).Gap(4).AlignItems(wui.Center).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Text(c, i18n.Tf("第 %d / %d 处改动", i+1, n)).FontSize(11.5).FontWeight(500).SingleLine().Shrink(0)
		wui.Text(c, "·").FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
		wui.Text(c, "−"+strconv.Itoa(h.OldEnd-h.OldStart)).FontSize(11.5).TextColor(ct.Deleted).SingleLine().Shrink(0)
		wui.Text(c, "+"+strconv.Itoa(h.NewEnd-h.NewStart)).FontSize(11.5).TextColor(ct.Added).SingleLine().Shrink(0)
		wui.Spacer(c).Grow(1)
		toolButton(c, iconChevronUp, i18n.T("上一处改动"), !a.CanStepPeek(-1), func() { a.PeekStep(-1) })
		toolButton(c, iconChevron, i18n.T("下一处改动"), !a.CanStepPeek(1), func() { a.PeekStep(1) })
		if app.PeekEditable(d) {
			if peekButton(c, upperFirst(i18n.T("还原")), i18n.T("换回 HEAD 里的样子（⌘Z 能撤）"), false) {
				a.PeekRevert()
			}
		}
		if r := a.RepoOf(d); r != nil && d.Kind == docs.File && d.Rel != "" {
			tip := i18n.T("把这一块加进暂存区")
			if d.Modified() {
				tip = i18n.T("先保存")
			}
			if peekButton(c, i18n.T("暂存"), tip, d.Modified()) {
				a.PeekStage()
			}
			if peekButton(c, i18n.T("在 Diff 中打开"), i18n.T("打开这个文件的 Diff，停在这一块"), false) {
				a.PeekOpenDiff()
			}
		}
		toolButton(c, iconClose, i18n.T("关闭"), false, a.ClosePeek)
	})
}

// peekBody 是这一块在 HEAD 里的旧行：删除底色、等宽、行号用旧行号；纯新增的块只写一句。
func peekBody(c *wui.Context, d *docs.Doc, h editor.DiffHunk, lh float32, ct *editor.Theme) {
	t := c.Theme()
	if h.OldEnd <= h.OldStart {
		wui.Text(c, i18n.Tn("新增 %d 行，HEAD 里没有", h.NewEnd-h.NewStart)).FontSize(11.5).TextColor(t.TextMuted).Padding(6, 10).Shrink(0)
		return
	}
	font := d.St.Font
	bg := ct.Deleted.Alpha(0.12)
	if t.Dark {
		bg = ct.Deleted.Alpha(0.2)
	}
	numW := float32(len(strconv.Itoa(h.OldEnd)))*font.Size*0.62 + 12
	wui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		wui.Column(c).Padding(4, 0).Children(func() {
			for l := h.OldStart; l < h.OldEnd && l < d.Base.LineCount(); l++ {
				wui.Row(c).Height(lh).Background(bg).AlignItems(wui.Center).Children(func() {
					wui.Text(c, strconv.Itoa(l+1)).Font(font.Family).FontSize(font.Size).TextColor(ct.LineNumber).SingleLine().
						Width(numW).TextAlign(wui.End).Shrink(0)
					wui.Text(c, expandTabs(d.Base.Line(l), d.St.TabSize)).Font(font.Family).FontSize(font.Size).TextColor(ct.Text).
						SingleLine().Ellipsis("…").Padding(0, 10).MinWidth(0)
				})
			}
		})
	})
}

// peekButton 是卡头上的字按钮（还原、暂存、在 Diff 中打开）：禁用时灰掉、点了不算，提示照样有。
func peekButton(c *wui.Context, label, tip string, disabled bool) bool {
	t := c.Theme()
	b := wui.Box(c).Padding(2, 7).Radius(5).Shrink(0)
	tooltip(c, b, tip)
	fg := t.TextMuted
	switch {
	case disabled:
		fg = t.TextMuted.Alpha(0.5)
	case b.Hovered():
		fg = t.Text
		b.Background(toolFill(t, 0.06)).Cursor(wui.CursorPointer)
	default:
		b.Cursor(wui.CursorPointer)
	}
	b.Children(func() { wui.Text(c, label).FontSize(11).FontWeight(500).TextColor(fg).SingleLine() })
	return !disabled && b.Clicked()
}

// upperFirst 把头一个字母换成大写：英文表里的"还原"是 revert（git.Ongoing 的名字，嵌在句子里所以小写），卡上当按钮用。中文不变。
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// expandTabs 把行首和行里的 Tab 换成空格（按 size，0 当 4），卡里的旧行和编辑器对得上。
func expandTabs(s string, size int) string {
	if size <= 0 {
		size = 4
	}
	out := make([]rune, 0, len(s))
	col := 0
	for _, r := range s {
		if r == '\t' {
			for k := size - col%size; k > 0; k-- {
				out = append(out, ' ')
				col++
			}
			continue
		}
		out = append(out, r)
		col++
	}
	return string(out)
}
