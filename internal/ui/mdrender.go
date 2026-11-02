package ui

import (
	"fmt"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/render"
)

// 这个文件是 Markdown 预览里的 Mermaid 图和公式：给 markdown.Options 挂上 Code["mermaid"]、Math、InlineMath，
// 从窗口的渲染器（app.Renderer，internal/render）取位图画。没好的先显示代码块 / TeX，坏了的代码块上面一行红字。

// mdRender 给 o 挂上（或摘掉）钩子，返回行内公式的代数（预览拿它当 key：有了新的行内结果，段落重排）。
// 渲染器没有（设置里关了、测试里没给）时摘掉，$…$ 也不认（照旧是字）。
func mdRender(a *app.App, c *wui.Context, o *markdown.Options) uint64 {
	r := a.Renderer()
	if r == nil {
		o.Code, o.Math, o.InlineMath, o.Extensions = nil, nil, nil, 0
		return 0
	}
	t := c.Theme()
	dark, color := t.Dark, hexColor(t.Text)
	// 一个 DIP 几个像素：显示器的（画的时候记下的 Painter.Scale，还没画过当 Retina）乘界面缩放（Painter.Scale 不含它）
	zoom := a.ZoomFactor()
	scale := r.Scale(2) * zoom
	size := t.FontSize
	if size == 0 {
		size = 14
	}
	o.Extensions = parser.Default | parser.ExtDollarMath
	o.Code = map[string]func(*wui.Context, *markdown.CodeBlockView){
		"mermaid": func(c *wui.Context, b *markdown.CodeBlockView) {
			q := render.Request{Kind: render.Mermaid, Src: b.Code(), Dark: dark, Scale: scale}
			renderedBlock(c, r, q, zoom, i18n.T("Mermaid 图"), func() { markdown.DefaultCodeBlock(c, b) })
		},
	}
	o.Math = func(c *wui.Context, tex string) {
		q := render.Request{Kind: render.Math, Src: tex, Dark: dark, Scale: scale, Size: size, Color: color}
		renderedBlock(c, r, q, zoom, tex, func() {
			th := c.Theme()
			wui.ScrollHorizontal(c).AlignSelf(wui.Stretch).Children(func() {
				wui.Text(c, tex).Font("monospace").FontSize(size*0.875).TextColor(th.TextMuted).NoWrap().Margin(0, wui.Auto)
			})
		})
	}
	o.InlineMath = func(tex string, size float32) (markdown.InlineObject, bool) {
		q := render.Request{Kind: render.Inline, Src: tex, Dark: dark, Scale: scale, Size: size, Color: color}
		res, ok := r.Get(q)
		if !ok || res.Bitmap == nil {
			return markdown.InlineObject{}, false // 先按 TeX 显示；好了 Gen 变，段落重排
		}
		return markdown.InlineObject{Width: res.W, Height: res.H - res.Depth, Depth: res.Depth, Draw: func(p *wui.Painter, rc wui.Rect) {
			drawRendered(p, r, q, res, rc, zoom)
		}}, true
	}
	return r.Gen()
}

// renderedBlock 是块级的一张：好了居中画位图（超宽时按比例缩到预览宽）；在渲染时先显示 fallback（代码块 / TeX）和一行淡字；
// 坏了显示 fallback，上面一行红字（错误信息）。
func renderedBlock(c *wui.Context, r *render.Renderer, q render.Request, zoom float32, label string, fallback func()) {
	t := c.Theme()
	res, ok := r.Get(q)
	switch {
	case ok && res.Bitmap != nil && res.W > 0 && res.H > 0:
		wui.Box(c).AlignSelf(wui.Center).FillWidth().MaxWidth(res.W).AspectRatio(res.W / res.H).Shrink(0).
			Role(wui.RoleImage).Label(label).Draw(func(p *wui.Painter, rc wui.Rect) { drawRendered(p, r, q, res, rc, zoom) })
	case ok && res.Err != "":
		wui.Column(c).AlignSelf(wui.Stretch).Gap(6).Children(func() {
			wui.Text(c, i18n.Tf("画不出来：%s", res.Err)).FontSize(12).TextColor(t.Danger).Selectable()
			fallback()
		})
	default:
		wui.Column(c).AlignSelf(wui.Stretch).Gap(6).Children(func() {
			fallback()
			wui.Text(c, i18n.T("渲染中…")).FontSize(11.5).TextColor(t.TextMuted.Alpha(0.8))
		})
	}
}

// drawRendered 画一张的位图：按这一帧的主题和像素密度再取一次（换了主题、挪到别的显示器时换成对的那张，没好之前画手里的）。
func drawRendered(p *wui.Painter, r *render.Renderer, q render.Request, res render.Result, rc wui.Rect, zoom float32) {
	th := p.Theme()
	q.Scale, q.Dark = p.Scale()*zoom, th.Dark
	if q.Kind != render.Mermaid {
		q.Color = hexColor(th.Text)
	}
	r.SeenScale(p.Scale())
	if cur, ok := r.Get(q); ok && cur.Bitmap != nil {
		res = cur
	}
	if res.Bitmap != nil {
		p.Image(res.Bitmap, rc, wui.FillBox)
	}
}

// hexColor 是 c 的 #rrggbb（给 MathJax 的 currentColor）。
func hexColor(c wui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
