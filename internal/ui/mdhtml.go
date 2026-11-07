package ui

import (
	"strconv"
	"strings"
	"sync"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件把 Markdown 预览里的 HTML 块（GitHub README 常见的 <div align="center">、<p>、<img> 徽章、
// <a> 链接）画成原生界面。mygo 的 Markdown 只认行内的 <b>、<i> 这些格式标签，块级 HTML 默认原样显示；
// 这里支持块级的 div / p / center / h1–h6 / ul / ol / li / pre / hr / br / details，行内的 a / img / strong /
// b / em / i / code / span，属性里用到的是 align、href、src、alt、width、height。
// 认不出来的标签递归画它的孩子，不认识的属性忽略；解析失败就照旧显示原文。

// htmlStyle 是行内文字现在带着的样式（嵌套的 strong / em / code 叠上去）。
type htmlStyle struct {
	size   float32
	weight int
	italic bool
	mono   bool
}

// mdHTMLBlock 画一块 HTML（markdown.Options.HTML 的钩子，由 markdownPreview 接上）。
func mdHTMLBlock(a *app.App, c *wui.Context, d *docs.Doc, o *markdown.Options, src string) {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{
		Type: html.ElementNode, Data: "div", DataAtom: atom.Div,
	})
	if err != nil {
		wui.Text(c, src).FontSize(12.5).TextColor(c.Theme().TextMuted)
		return
	}
	h := &htmlView{a: a, c: c, d: d, o: o, t: c.Theme()}
	wui.Column(c).Gap(8).AlignSelf(wui.Stretch).Children(func() {
		for _, n := range nodes {
			h.block(n)
		}
	})
}

type htmlView struct {
	a *app.App
	c *wui.Context
	d *docs.Doc
	o *markdown.Options
	t *wui.Theme
}

// block 画一个块级节点（或把文本节点当成一段）。
func (h *htmlView) block(n *html.Node) { h.blockAt(n, false) }

// blockCentered 画一个块级节点，外面（<div align=center>）让它居中。
func (h *htmlView) blockCentered(n *html.Node) { h.blockAt(n, true) }

func (h *htmlView) blockAt(n *html.Node, inheritCenter bool) {
	c := h.c
	switch n.Type {
	case html.TextNode:
		if strings.TrimSpace(n.Data) == "" {
			return
		}
		h.flow([]*html.Node{n}, htmlStyle{size: h.bodySize()}, inheritCenter)
		return
	case html.ElementNode:
	default:
		h.blockChildren(n)
		return
	}
	tag := strings.ToLower(n.Data)
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		h.flow(children(n), htmlStyle{size: h.headingSize(tag), weight: 600}, inheritCenter || h.center(n))
	case "p", "div", "center", "section", "article", "header", "footer", "main", "details", "summary", "blockquote":
		h.container(n, htmlStyle{size: h.bodySize()}, inheritCenter || h.center(n))
	case "ul", "ol":
		h.list(n, tag == "ol")
	case "hr":
		wui.Box(c).Height(1).AlignSelf(wui.Stretch).Margin(6, 0).Background(h.t.Border)
	case "br":
		wui.Box(c).Height(8)
	case "pre":
		wui.Text(c, textOf(n)).Font(mono()).FontSize(12).TextColor(h.t.Text).Selectable().
			Padding(8, 10).Radius(6).Background(h.t.Surface).AlignSelf(wui.Stretch)
	case "img":
		h.flow([]*html.Node{n}, htmlStyle{size: h.bodySize()}, false)
	default:
		// 不认识的块：孩子当行内内容画
		h.container(n, htmlStyle{size: h.bodySize()}, inheritCenter || h.center(n))
	}
}

// container 画一个容器：行内的一段一段画，块级的子节点（h1、p、ul……）各画各的。
func (h *htmlView) container(n *html.Node, st htmlStyle, center bool) {
	var run []*html.Node
	flush := func() {
		if len(run) > 0 {
			h.flow(run, st, center)
			run = nil
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && isBlockTag(ch.Data) {
			flush()
			if center {
				h.blockCentered(ch)
			} else {
				h.block(ch)
			}
			continue
		}
		run = append(run, ch)
	}
	flush()
}

// isBlockTag 报这个标签是块级的（container 里遇到它要另起一段）。
func isBlockTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "div", "p", "center", "section", "article", "header", "footer", "main", "details", "summary",
		"blockquote", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "pre", "hr", "table":
		return true
	}
	return false
}

// children 是节点的孩子。
func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		out = append(out, ch)
	}
	return out
}

func (h *htmlView) blockChildren(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		h.block(ch)
	}
}

// flow 把一段行内内容画成几条会折行的 Row，<br> 换一条。
func (h *htmlView) flow(nodes []*html.Node, st htmlStyle, center bool) {
	c := h.c
	col := wui.Column(c).Gap(2).AlignSelf(wui.Stretch)
	if center {
		col.AlignItems(wui.Center)
	}
	col.Children(func() {
		var seg []*html.Node
		flush := func() {
			if len(seg) == 0 {
				return
			}
			row := wui.Row(c).Wrap().Gap(3).AlignItems(wui.Center)
			if center {
				row.Justify(wui.Center)
			} else {
				row.AlignSelf(wui.Stretch)
			}
			row.Children(func() {
				for _, n := range seg {
					h.inline(n, st)
				}
			})
			seg = nil
		}
		for _, n := range nodes {
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "br") {
				flush()
				continue
			}
			seg = append(seg, n)
		}
		flush()
	})
}

// inline 把一个行内节点加进当前的 Row。
func (h *htmlView) inline(n *html.Node, st htmlStyle) {
	c := h.c
	switch n.Type {
	case html.TextNode:
		for _, w := range splitInline(n.Data) {
			if w == " " {
				continue
			}
			txt := wui.Text(c, w).FontSize(st.size).TextColor(h.t.Text).MaxWidthPercent(100).Selectable()
			if st.weight > 0 {
				txt.FontWeight(st.weight)
			}
			if st.italic {
				txt.Italic()
			}
			if st.mono {
				txt.Font(mono()).FontSize(st.size-0.5).Background(h.t.Surface).Radius(3).Padding(1, 3)
			}
		}
		return
	case html.ElementNode:
	default:
		return
	}
	tag := strings.ToLower(n.Data)
	switch tag {
	case "a":
		h.link(n, st)
	case "img":
		h.image(n)
	case "strong", "b":
		if st.weight < 600 {
			st.weight = 600
		}
		h.inlines(children(n), st)
	case "em", "i":
		st.italic = true
		h.inlines(children(n), st)
	case "code", "kbd", "samp":
		st.mono = true
		h.inlines(children(n), st)
	case "br":
		wui.Text(c, " ").FontSize(st.size)
	default:
		// span / font / small / big 之类：样式不管，孩子照画
		h.inlines(children(n), st)
	}
}

func (h *htmlView) inlines(nodes []*html.Node, st htmlStyle) {
	for _, n := range nodes {
		h.inline(n, st)
	}
}

// link 是行内链接：强调色，悬停加下划线，点了走 MarkdownLink（和 Markdown 的链接一样）。
// 链接里包着图片（徽章）时，图片本身可点。
func (h *htmlView) link(n *html.Node, st htmlStyle) {
	href := attr(n, "href")
	label := strings.TrimSpace(plainText(n))
	img := firstElement(n, "img")
	switch {
	case img != nil && label == "":
		h.imageLink(img, href)
	case label != "":
		c := h.c
		// 用 mygo 的 Link：预览整页是可选中文字，自己拼的按钮会被选字那层挡住点击。
		// 它走系统浏览器，和 Markdown 链接里的 http(s) 一样；不带协议的地址打不开，先这样。
		link := wui.Link(c, label, href).FontSize(st.size).Shrink(0)
		if st.weight > 0 {
			link.FontWeight(st.weight)
		}
		if st.italic {
			link.Italic()
		}
	default:
		h.inlines(children(n), st)
	}
}

// imageLink 是包着图片的链接（徽章）：图片照画，链接先不接点击（和上面一样，选字那层会挡）。
func (h *htmlView) imageLink(img *html.Node, href string) {
	h.image(img)
}

// image 是行内图片：按 width / height 属性排，没有就按位图原来的大小、最宽不超过这一段。
// 还没下好时显示 alt 的一小块占位。
func (h *htmlView) image(n *html.Node) {
	c := h.c
	alt := attr(n, "alt")
	src := h.imageSrc(attr(n, "src"))
	if src == nil {
		label := alt
		if label == "" {
			label = i18n.T("图片")
		}
		ph := wui.Row(c).Padding(3, 8).Radius(4).Background(h.t.Surface).Label(alt)
		ph.Children(func() {
			wui.Text(c, label).FontSize(11).TextColor(h.t.TextMuted).SingleLine()
		})
		return
	}
	img := wui.Image(c, src).Radius(3).MaxWidthPercent(100).Label(alt)
	if v, pct := lengthAttr(n, "width"); v > 0 {
		if pct {
			img.MaxWidthPercent(v)
		} else {
			img.Width(v)
		}
	}
	if v, pct := lengthAttr(n, "height"); v > 0 && !pct {
		img.Height(v)
	}
}

// list 是 ul / ol：一项一行，前面是 • 或 1.。
func (h *htmlView) list(n *html.Node, ordered bool) {
	c := h.c
	i := 0
	wui.Column(c).Gap(3).AlignSelf(wui.Stretch).Children(func() {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode || !strings.EqualFold(ch.Data, "li") {
				continue
			}
			i++
			mark := "•"
			if ordered {
				mark = strconv.Itoa(i) + "."
			}
			row := wui.Row(c).Wrap().Gap(3).AlignItems(wui.Center).AlignSelf(wui.Stretch).Padding(0, 0, 0, 12)
			row.Children(func() {
				wui.Text(c, mark).FontSize(h.bodySize()).TextColor(h.t.TextMuted).Shrink(0).Margin(0, 6, 0, 0)
				for ch2 := ch.FirstChild; ch2 != nil; ch2 = ch2.NextSibling {
					h.inline(ch2, htmlStyle{size: h.bodySize()})
				}
			})
		}
	})
}

// htmlImageEntry 是一张正在下 / 下好的图片（和 mygo 的 Markdown 一样，在 goroutine 里下，好了叫窗口重画）。
type htmlImageEntry struct {
	mu   sync.Mutex
	src  wui.ImageSource
	err  error
	done bool
}

var htmlImages sync.Map // "文档目录\x00url" -> *htmlImageEntry

// imageSrc 取一张图片，第一次遇到时开始下（o.LoadImage，本地相对路径和网络图片都走它）。
func (h *htmlView) imageSrc(url string) wui.ImageSource {
	if h.o == nil || h.o.LoadImage == nil || url == "" {
		return nil
	}
	key := h.d.Dir() + "\x00" + url
	v, loaded := htmlImages.LoadOrStore(key, &htmlImageEntry{})
	e := v.(*htmlImageEntry)
	if !loaded {
		load, services := h.o.LoadImage, h.c.Services()
		go func() {
			src, err := load(url)
			e.mu.Lock()
			e.src, e.err, e.done = src, err, true
			e.mu.Unlock()
			services.Invalidate()
		}()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.src
}

// bodySize 是正文大小：跟预览的主题，没有就用 13.5。
func (h *htmlView) bodySize() float32 {
	if s := h.t.FontSize; s > 0 {
		return s
	}
	return 13.5
}

// headingSize 是 h1–h6 的大小。
func (h *htmlView) headingSize(tag string) float32 {
	base := h.bodySize()
	switch tag {
	case "h1":
		return base * 2
	case "h2":
		return base * 1.6
	case "h3":
		return base * 1.3
	case "h4":
		return base * 1.15
	case "h5":
		return base * 1.05
	}
	return base
}

// center 报这个块要居中：<center> 或 align="center"。
func (h *htmlView) center(n *html.Node) bool {
	return strings.EqualFold(n.Data, "center") || strings.EqualFold(attr(n, "align"), "center")
}

// attr 取节点的属性（名字不分大小写）。
func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

// lengthAttr 取 width / height：数字是 DIP，"50%" 当百分比（pct 为 true）。
func lengthAttr(n *html.Node, name string) (v float32, pct bool) {
	s := strings.TrimSpace(attr(n, name))
	if s == "" {
		return 0, false
	}
	if p, ok := strings.CutSuffix(s, "%"); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(p), 32); err == nil && f > 0 {
			return float32(f), true
		}
		return 0, false
	}
	if f, err := strconv.ParseFloat(s, 32); err == nil && f > 0 {
		return float32(f), false
	}
	return 0, false
}

// plainText 是节点里所有文字（递归；给链接的标签用）。
func plainText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch x.Type {
		case html.TextNode:
			b.WriteString(x.Data)
		case html.ElementNode:
			for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
				walk(ch)
			}
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch)
	}
	return b.String()
}

// textOf 是 pre 里的原文（换行留着）。
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch x.Type {
		case html.TextNode:
			b.WriteString(x.Data)
		case html.ElementNode:
			for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
				walk(ch)
			}
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		walk(ch)
	}
	return b.String()
}

// firstElement 找第一个叫 tag 的后代元素。
func firstElement(n *html.Node, tag string) *html.Node {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && strings.EqualFold(ch.Data, tag) {
			return ch
		}
		if ch.Type == html.ElementNode {
			if got := firstElement(ch, tag); got != nil {
				return got
			}
		}
	}
	return nil
}

// splitInline 把一段文字切成能折行的几块：空白单独一块，CJK 一个字一块，别的按词。
func splitInline(s string) []string {
	var out []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u00a0':
			flush()
			if n := len(out); n > 0 && out[n-1] == " " {
				continue
			}
			out = append(out, " ")
		default:
			word.WriteRune(r)
		}
	}
	flush()
	return out
}
