package ui

import (
	"math"
	"strconv"
	"strings"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/outline"
)

// 粘性滚动（设置里默认开，view.stickyScroll）：滚动时把包着最上面那几行的函数 / 类 / 标题吸在编辑器顶上，
// 最多 outline.StickyMax 行，点一行跳过去。哪几个符号由 outline.Tree.Sticky 定（按大纲的 Line / End），这里只管画：
// 照 blame 装订线（blame.go）的写法，编辑器先建，再在它上面叠一层 Absolute 的 Box，按 LineTop - ScrollY 对齐；
// mygo 没有"装订线多宽"的接口，行号和正文的横坐标照 mygo editor 的 gutterWidth 估（stickyGutter）。

type (
	stickyKey    struct{ d *docs.Doc }
	stickyRowKey struct{ line int }
)

// stickyScrollbar 是编辑器右边滚动条的宽（mygo editor 的 scrollbarWidth）：叠层不盖它。
const stickyScrollbar = 10

// stickyOn：d 的编辑器要不要粘性滚动——设置开着、是文件标签、读好了、不是大文件和十六进制（Outline 再看语言和大小）。
func stickyOn(a *app.App, d *docs.Doc) bool {
	return !a.St.Settings.NoStickyScroll && d.Kind == docs.File && d.Buf != nil && !d.Big && !d.Hex
}

// stickyEditor 建带粘性滚动的编辑器（返回编辑器本身，好交键盘）：外面套一层 Row，编辑器撑满，叠层画在它上面。
func stickyEditor(a *app.App, c *wui.Context, d *docs.Doc, build func() wui.Element) wui.Element {
	bars := stickyBars(d)
	row := wui.Row(c.Key(stickyKey{d})).Grow(1).MinHeight(0).AlignItems(wui.Stretch) // 不撑满的话编辑器高是 0
	var ed wui.Element
	row.Children(func() {
		ed = build().Grow(1).MinWidth(0)
		peekCard(a, c, d, row, 0) // 吸着的行盖在卡上面，同文字
		stickyRows(a, c, d, row, 0, bars)
	})
	return ed
}

// stickyBars 让编辑器建查找条、跳行条时记下它们上一帧在哪（编辑器建的时候填）：叠层是编辑器之后建的，会盖住它们，
// 所以叠层的右边停在它们左边。
func stickyBars(d *docs.Doc) *[]wui.Rect {
	bars := new([]wui.Rect)
	d.St.RenderFindBar = func(c *wui.Context, bar wui.Element, f *editor.FindBar) {
		*bars = append(*bars, bar.Bounds())
		editor.DefaultFindBar(c, bar, f)
	}
	d.St.RenderGoToLine = func(c *wui.Context, bar wui.Element, g *editor.GoToLineBar) {
		*bars = append(*bars, bar.Bounds())
		editor.DefaultGoToLine(c, bar, g)
	}
	return bars
}

// stickyGeom 是编辑器横向的几个位置（从编辑器左边起，DIP）：行号右边、正文起点。
type stickyGeom struct{ numRight, textX float32 }

// stickyGutter 照 mygo editor 的 gutterWidth / layout / paintGutter 估装订线：行号位数（至少 3）× 字宽 + 1.5 字宽，
// 折叠箭头 0.8 行高，和 HEAD 比的改动条 6，再加 4；正文在装订线右边 4（没有装订线是 8）。
func stickyGutter(c *wui.Context, st *editor.State, lines int, lh float32) stickyGeom {
	w, _ := c.MeasureText(0, wui.Span{Text: "0000000000", Font: st.Font.Family, Size: st.Font.Size})
	cw := w / 10
	numbers := st.LineNumbers != editor.LineNumbersOff
	folding := !st.NoFolding
	gw := float32(0)
	if numbers {
		gw += float32(max(3, len(strconv.Itoa(lines))))*cw + cw*1.5
	}
	if folding {
		gw += lh * 0.8
	}
	if st.DiffBase != nil {
		gw += 6
	}
	if len(st.GutterMarks) > 0 && !numbers {
		gw += lh * 0.7
	}
	if gw > 0 {
		gw += 4
	}
	gw = float32(math.Ceil(float64(gw)))
	g := stickyGeom{numRight: gw - 4 - cw*0.5, textX: gw + 4}
	if gw == 0 {
		g.textX = 8
	}
	if folding {
		g.numRight -= lh * 0.8
	}
	if st.DiffBase != nil {
		g.numRight -= 6
	}
	if !numbers {
		g.numRight = 0
	}
	return g
}

// stickyRows 是叠层：row 是编辑器外面那一层（量高、对齐查找条用），left 是编辑器左边离 row 左边多远（开着 blame 时是装订线的宽）。
// 编辑器画的时候还会滚，画到叠层时核对一下用的滚动和行高，变了就再建一帧。
func stickyRows(a *app.App, c *wui.Context, d *docs.Doc, row wui.Element, left float32, bars *[]wui.Rect) {
	tree, wait := d.StickyOutline(c.Now())
	if wait > 0 {
		c.After(wait)
	}
	st := &d.St
	n := d.Buf.LineCount()
	// 光标动了才看要不要露出来（滚轮不碰）：不管这一帧有没有叠层都记下，不然光标停着、滚轮把它滚到叠层底下时会被拽回去
	moved := st.Cursor() != d.StickyCur
	d.StickyCur = st.Cursor()
	if tree == nil || len(tree.Flat) == 0 || n < 2 {
		return
	}
	first, last := st.VisibleLines()
	y0 := st.ScrollY()
	// 一行多高：看得见的行里最矮的（软换行折了的行更高）
	lh := 0.0
	for i := first; i < min(last+1, first+64, n); i++ {
		if h := st.LineTop(i+1) - st.LineTop(i); h > 0.5 && (lh == 0 || h < lh) {
			lh = h
		}
	}
	if lh == 0 {
		return // 编辑器还没排过版
	}
	total := st.LineTop(n)
	svc := c.Services()
	check := func() {
		if st.ScrollY() != y0 || st.LineTop(n) != total {
			svc.Invalidate()
		}
	}
	// 吸几行：编辑器矮的时候少吸，最多占三分之一
	rows := outline.StickyMax
	if h := float64(row.Bounds().H); h > 0 {
		rows = min(rows, int(h/lh/3))
	}
	if rows < 1 || y0 <= 0 {
		wui.Box(c).Absolute().Width(0).Height(0).Draw(func(*wui.Painter, wui.Rect) { check() })
		return
	}
	// 从上往下第 k 行显示的是文件的哪一行：LineTop 不超过那一行上沿的最后一行（藏着的行高 0，落到它下面那行）
	rowLine := func(k int) int {
		y := y0 + float64(k)*lh + 0.5
		lo, hi := 0, n-1
		for lo < hi {
			m := (lo + hi + 1) / 2
			if st.LineTop(m) <= y {
				lo = m
			} else {
				hi = m - 1
			}
		}
		return lo
	}
	folded := map[int]bool{}
	for _, l := range st.Folded() {
		folded[l] = true
	}
	chain := tree.Sticky(rowLine, rows, folded)
	if len(chain) == 0 {
		wui.Box(c).Absolute().Width(0).Height(0).Draw(func(*wui.Painter, wui.Rect) { check() })
		return
	}
	// 推挤：一个符号到头了（下一行就出了它），它那一行连同里面的往上推，下一个符号头顶上来
	tops := make([]float64, len(chain))
	height := 0.0
	for i, idx := range chain {
		f := &tree.Flat[idx]
		end := min(f.End, n)
		if folded[f.Line] {
			end = f.Line + 1
		}
		tops[i] = float64(i)*lh + min(0, st.LineTop(end)-y0-float64(i+1)*lh)
		if i > 0 {
			tops[i] = min(tops[i], tops[i-1]+lh) // 里层的不跑到外层下面去
		}
		height = math.Max(height, tops[i]+lh)
	}
	if height <= 0 {
		return
	}
	// 光标那一行被吸着的行盖住（或在上面看不见）：滚到它正好在叠层下面。编辑器这一帧排版时光标已经看得见，不会再自己滚；
	// 往上滚后吸着的行可能变，下一帧再看一遍（每次只往上滚，到顶没有叠层，总会停）
	if moved {
		if top := st.LineTop(st.CursorPos().Line); top-y0 < height {
			st.SetScrollY(max(0, top-height))
			if st.ScrollY() != y0 {
				d.StickyCur = -1
			}
		}
	}

	t := c.Theme()
	ct := editor.DefaultTheme(t)
	g := stickyGutter(c, st, n, float32(lh))
	right := float32(stickyScrollbar)
	if rb := row.Bounds(); rb.W > 0 {
		for _, b := range *bars {
			if b.W > 0 && b.X > rb.X {
				right = max(right, rb.X+rb.W-b.X+6)
			}
		}
	}
	var lang *editor.Language
	if l := d.Buf.Language(); l != nil && l.Tokenizer != nil {
		lang = l
	}
	box := wui.Box(c).Absolute().Top(0).Left(left).Right(right).Height(float32(height)).Clip().
		Background(ct.Background).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
		Shadow(0, 2, 6, 0, wui.RGBA(0, 0, 0, 0.12)).Label(i18n.T("粘性滚动")).
		Draw(func(*wui.Painter, wui.Rect) { check() }).
		HandleInput(func(ev wui.InputEvent) bool { // 滚轮透给编辑器
			if ev.Kind == wui.InputScroll && ev.DY != 0 {
				before := st.ScrollY()
				st.SetScrollY(before + float64(ev.DY))
				return st.ScrollY() != before
			}
			return false
		})
	box.Children(func() {
		// 从里往外画：外层的盖在被推上去的里层上面
		for i := len(chain) - 1; i >= 0; i-- {
			stickyRow(c, d, &tree.Flat[chain[i]], i, float32(tops[i]), float32(lh), g, ct, lang)
		}
	})
}

// stickyRow 是叠层里的一行：装订线的位置写行号，正文是那一行源码去掉首尾空白、按第几行缩进两格，配色同编辑器。
// 悬停换底色，点一下光标到那一行行首、那一行滚到外层吸着的行下面（最外层的滚到顶）。
func stickyRow(c *wui.Context, d *docs.Doc, f *outline.Flat, depth int, top, lh float32, g stickyGeom, ct *editor.Theme, lang *editor.Language) {
	t := c.Theme()
	st := &d.St
	font := st.Font
	src := ""
	if f.Line < d.Buf.LineCount() {
		src = strings.TrimSpace(d.Buf.Line(f.Line))
	}
	if len(src) > 400 {
		src = src[:400]
	}
	el := wui.Box(c.Key(stickyRowKey{f.Line})).Absolute().Left(0).Right(0).Top(top).Height(lh).
		Background(ct.Background).Cursor(wui.CursorPointer).Label(src)
	if el.Hovered() {
		el.Background(t.Text.Alpha(0.06))
	}
	el.Children(func() {
		if g.numRight > 0 {
			wui.Text(c, strconv.Itoa(f.Line+1)).Font(font.Family).FontSize(font.Size).TextColor(ct.LineNumber).SingleLine().
				Absolute().Left(0).Width(g.numRight).Top(0).Height(lh).TextAlign(wui.End).AlignContent(wui.Center)
		}
		var spans []wui.Span
		code := strings.Repeat("  ", depth) + src
		if lang != nil {
			toks, _ := lang.Tokenizer.Tokenize(src, 0, nil)
			spans = append(spans, wui.Span{Text: code[:len(code)-len(src)], Font: font.Family, Size: font.Size, Color: ct.Text})
			at := 0
			for _, tk := range toks {
				if tk.End <= at || tk.End > len(src) {
					continue
				}
				spans = append(spans, wui.Span{Text: src[at:tk.End], Font: font.Family, Size: font.Size, Color: ct.TokenColor(tk.Kind)})
				at = tk.End
			}
			if at < len(src) {
				spans = append(spans, wui.Span{Text: src[at:], Font: font.Family, Size: font.Size, Color: ct.Text})
			}
		} else {
			spans = []wui.Span{{Text: code, Font: font.Family, Size: font.Size, Color: ct.Text}}
		}
		wui.RichText(c, spans...).SingleLine().Absolute().Left(g.textX).Right(0).Top(0).Height(lh).AlignContent(wui.Center)
	})
	if el.Clicked() { // 那一行滚到外面几层吸着的行下面（第 0 层就是滚到顶）
		st.GoTo(editor.Pos{Line: f.Line})
		st.SetScrollY(max(0, st.LineTop(f.Line)-float64(depth)*float64(lh)))
	}
}
