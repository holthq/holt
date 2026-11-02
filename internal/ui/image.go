package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
)

// 图片（二）：位图和 SVG 预览共用的图片视图——上面一条工具栏（适应、100%、缩小、放大、倍数），
// 下面两个方向都能滚的画布：拖着看、⌥ 滚轮 / 双指捏合 / = - 0 缩放，透明的地方垫棋盘格；
// 图片 diff 左右两张一起缩放；SVG 预览上面解析失败的红字；十六进制视图顶上的一条。

// imgPad 是图片四周留的空（适应时也算上）。
const imgPad = 16

type imgKey struct {
	d    *docs.Doc
	side int
}

// imgDrag 是正在拖着看的那块画布：按下时指针在画布里的位置和那时的滚动。
var imgDrag struct {
	sc           *wui.ScrollState
	x, y, sx, sy float32
}

// imgFocus 是刚按下、要拿键盘的那块画布（输入回调里不能 Focus，下一帧拿）。
var imgFocus *wui.ScrollState

// imageView 是一张图（位图，或 SVG 预览解析出来的）：工具栏和画布；focused 同 docContent，刚打开时画布拿键盘（= - 0 能用）。
func imageView(a *app.App, c *wui.Context, d *docs.Doc, src wui.ImageSource, focused bool) {
	imageBar(a, c, d, d.Bitmap != nil)
	box := imagePane(c, d, 0, src, i18n.Tf("图片 %s", d.Title))
	if focused && a.FocusDoc {
		a.FocusDoc = false
		box.Focus()
	}
}

// imageBar 是图片视图上面的一条：适应、100%、缩小、放大、现在的倍数；位图（hex）右边“十六进制”。
func imageBar(a *app.App, c *wui.Context, d *docs.Doc, hex bool) {
	t := c.Theme()
	v := &d.Img
	wui.Row(c).Height(28).Padding(0, 8).Gap(2).AlignItems(wui.Center).Shrink(0).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		// 点完键盘还给画布（接着按 = - 0）
		zoom := func(f func()) func() { return func() { f(); imgFocus = &v.Scroll } }
		textButtonCur(c, i18n.T("适应"), i18n.T("大图缩到放得下，小图照原大小（0）"), false, v.Zoom == 0, zoom(func() { fitImage(v) }))
		textButtonCur(c, "100%", i18n.T("按原大小显示"), false, v.Zoom == 1, zoom(func() { zoomAt(v, 1, 0, -1, -1) }))
		rowButton(c, iconMinus, i18n.T("缩小"), i18n.T("缩小（-，或按住 ⌥ 滚动、双指捏合）"), zoom(func() { zoomAt(v, docs.ZoomStep(v.Shown, false), 0, -1, -1) }))
		rowButton(c, iconPlus, i18n.T("放大"), i18n.T("放大（=，或按住 ⌥ 滚动、双指张开）"), zoom(func() { zoomAt(v, docs.ZoomStep(v.Shown, true), 0, -1, -1) }))
		wui.Text(c, zoomLabel(v.Shown)).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0).Margin(0, 0, 0, 6)
		wui.Spacer(c).Grow(1)
		if hex && a.CanHex(d) {
			textButton(c, i18n.T("十六进制"), app.Tip(i18n.T("以十六进制查看"), "view.hex"), func() { a.SetHex(d, true) })
		}
	})
}

// zoomLabel 是倍数的写法：125%。
func zoomLabel(z float32) string { return fmt.Sprintf("%d%%", int(math.Round(float64(z)*100))) }

// imagePane 是一块画布（图片 diff 有左右两块，side 0 / 1）：比画布小的图居中，大的能滚、能拖；返回画布好交键盘。
func imagePane(c *wui.Context, d *docs.Doc, side int, src wui.ImageSource, label string) wui.Element {
	v := &d.Img
	sc := imgScroll(v, side)
	iw, ih := wui.ImageSize(src)
	box := wui.ScrollBoth(c.Key(imgKey{d, side})).Grow(1).MinWidth(0).MinHeight(0).TrackScroll(sc).Focusable().Label(label)
	view := box.Bounds()
	v.Pane[side] = docs.ImgPane{W: view.W, H: view.H, IW: iw, IH: ih}
	if view.W <= 0 || view.H <= 0 {
		c.Invalidate() // 第一帧还不知道画布多大，适应的倍数下一帧才对
	}
	z := paneZoom(v, v.Pane[side])
	v.Shown = z
	w, h := iw*z, ih*z
	cw, ch := max(view.W, w+2*imgPad), max(view.H, h+2*imgPad)
	if imgFocus == sc {
		imgFocus = nil
		box.Focus()
	}
	if w+2*imgPad > view.W || h+2*imgPad > view.H {
		box.Cursor(wui.CursorGrab)
		if imgDrag.sc == sc {
			box.Cursor(wui.CursorGrabbing)
		}
	}
	box.HandleInput(func(ev wui.InputEvent) bool { return imageInput(v, side, sc, ev) })
	if bm, ok := src.(*wui.Bitmap); ok && z >= docs.PixelZoom {
		src = v.Pixelated(side, bm) // 放得很大时一个像素一个方块，看得清
	}
	dark := c.Theme().Dark
	box.Children(func() {
		wui.Column(c).Size(cw, ch).Shrink(0).AlignItems(wui.Center).Justify(wui.Center).Children(func() {
			wui.Box(c).Size(w, h).Shrink(0).Draw(func(p *wui.Painter, r wui.Rect) {
				// 框里看得见的那块（按框的左上角算）：图放得很大时棋盘格只铺这一块
				vis := wui.Rect{X: sc.X - (cw-w)/2, Y: sc.Y - (ch-h)/2, W: view.W, H: view.H}
				checker(p, r, vis, dark)
				p.Image(src, r, wui.FillBox)
			})
		})
	})
	return box
}

func imgScroll(v *docs.ImgView, side int) *wui.ScrollState {
	if side == 1 {
		return &v.Scroll2
	}
	return &v.Scroll
}

// paneZoom 是画布 p 这一帧画的倍数：适应（Zoom 为 0）时大图缩到放得下、小图照原大小。
func paneZoom(v *docs.ImgView, p docs.ImgPane) float32 {
	if v.Zoom > 0 {
		return v.Zoom
	}
	if p.W <= 0 || p.H <= 0 || p.IW <= 0 || p.IH <= 0 {
		return 1
	}
	return max(0.01, min(1, (p.W-2*imgPad)/p.IW, (p.H-2*imgPad)/p.IH))
}

// fitImage 回到适应（工具栏的“适应”、0 键）。
func fitImage(v *docs.ImgView) {
	v.Zoom = 0
	v.Scroll.X, v.Scroll.Y, v.Scroll2.X, v.Scroll2.Y = 0, 0, 0, 0
}

// zoomAt 把 v 缩放到 z（夹在 ZoomSteps 两头之间）：第 side 块画布上 (ax, ay) 下面的那一点不动（ax < 0 是画布中心），
// 另一块（图片 diff）按它自己的中心。
func zoomAt(v *docs.ImgView, z float32, side int, ax, ay float32) {
	z = min(max(z, docs.ZoomSteps[0]), docs.ZoomSteps[len(docs.ZoomSteps)-1])
	for i, p := range v.Pane {
		if p.W <= 0 || p.H <= 0 || p.IW <= 0 || p.IH <= 0 {
			continue
		}
		x, y := ax, ay
		if i != side || ax < 0 {
			x, y = p.W/2, p.H/2
		}
		z0 := paneZoom(v, p)
		sc := imgScroll(v, i)
		sc.X = keepPoint(sc.X, x, p.W, p.IW, z0, z)
		sc.Y = keepPoint(sc.Y, y, p.H, p.IH, z0, z)
	}
	v.Zoom = z
}

// keepPoint 是一个方向上缩放后的滚动位置：画布长 view、图长 n，从 z0 缩放到 z1，画布上 at 处的那一点不动。
func keepPoint(s, at, view, n, z0, z1 float32) float32 {
	content := func(z float32) float32 { return max(view, n*z+2*imgPad) }
	off := func(z float32) float32 { return (content(z) - n*z) / 2 } // 图在内容里的左边（上边）
	img := (s + at - off(z0)) / z0                                   // 那一点在图里的位置（图的像素）
	return max(0, min(img*z1+off(z1)-at, content(z1)-view))
}

// imageInput 是画布收到的输入：= - 0（⇧= 也是放大）、⌥ 滚轮和双指捏合缩放，左键按下拖着看。别的照常（滚轮滚动、Tab）。
func imageInput(v *docs.ImgView, side int, sc *wui.ScrollState, ev wui.InputEvent) bool {
	switch ev.Kind {
	case wui.InputKeyDown:
		if ev.Mods&^wui.Shift != 0 {
			return false
		}
		switch ev.Key {
		case wui.KeyEqual:
			zoomAt(v, docs.ZoomStep(v.Shown, true), side, -1, -1)
		case wui.KeyMinus:
			zoomAt(v, docs.ZoomStep(v.Shown, false), side, -1, -1)
		case wui.Key0:
			fitImage(v)
		default:
			return false
		}
		return true
	case wui.InputScroll:
		if ev.Mods&wui.Alt == 0 || ev.DY == 0 {
			return false
		}
		z := docs.ZoomStep(v.Shown, ev.DY < 0) // 鼠标滚轮一格一档
		if ev.Precise {                        // 触控板一点点地滚
			z = v.Shown * float32(math.Exp(float64(-ev.DY)/200))
		}
		zoomAt(v, z, side, ev.X, ev.Y)
		return true
	case wui.InputZoom:
		if ev.Scale <= 0 {
			return false
		}
		zoomAt(v, v.Shown*ev.Scale, side, ev.X, ev.Y)
		return true
	case wui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		imgFocus = sc
		imgDrag.sc, imgDrag.x, imgDrag.y, imgDrag.sx, imgDrag.sy = sc, ev.X, ev.Y, sc.X, sc.Y
		return true
	case wui.InputPointerMove:
		if imgDrag.sc != sc {
			return false
		}
		sc.X, sc.Y = max(0, imgDrag.sx-(ev.X-imgDrag.x)), max(0, imgDrag.sy-(ev.Y-imgDrag.y))
		return true
	case wui.InputPointerUp:
		if imgDrag.sc == sc {
			imgDrag.sc = nil
			return true
		}
	}
	return false
}

// checkerDIP 是棋盘格一块贴图画多大：128 像素见方、16 像素一格的贴图画成 64 DIP（一格 8 DIP）。
const checkerDIP = 64

// checkerTiles 是棋盘格的贴图，浅色、深色各一份，用到才做（各 64 KB）。
var checkerTiles [2]*wui.Bitmap

func checkerTile(dark bool) *wui.Bitmap {
	i, a, b := 0, color.RGBA{0xff, 0xff, 0xff, 0xff}, color.RGBA{0xe4, 0xe4, 0xe7, 0xff}
	if dark {
		i, a, b = 1, color.RGBA{0x2a, 0x2a, 0x2d, 0xff}, color.RGBA{0x3a, 0x3a, 0x3e, 0xff}
	}
	if checkerTiles[i] == nil {
		img := image.NewRGBA(image.Rect(0, 0, 128, 128))
		for y := range 128 {
			for x := range 128 {
				c := a
				if (x/16+y/16)%2 == 1 {
					c = b
				}
				img.SetRGBA(x, y, c)
			}
		}
		checkerTiles[i] = wui.NewBitmap(img).Pixelated()
	}
	return checkerTiles[i]
}

// checker 在图的框 r 里垫棋盘格（透明的地方看得出来），只铺 vis（框里看得见的那块，按框的左上角算）。
func checker(p *wui.Painter, r, vis wui.Rect, dark bool) {
	if vis.W <= 0 || vis.H <= 0 {
		return
	}
	tile := checkerTile(dark)
	x0 := float32(math.Floor(float64(max(0, vis.X)/checkerDIP))) * checkerDIP
	y0 := float32(math.Floor(float64(max(0, vis.Y)/checkerDIP))) * checkerDIP
	x1, y1 := min(r.W, vis.X+vis.W), min(r.H, vis.Y+vis.H)
	p.Clip(r, 0, func() {
		for y := y0; y < y1; y += checkerDIP {
			for x := x0; x < x1; x += checkerDIP {
				p.ImageRegion(tile, wui.Rect{}, wui.Rect{X: r.X + x, Y: r.Y + y, W: checkerDIP, H: checkerDIP}, wui.ImageOptions{})
			}
		}
	})
}

// svgPreview 是 SVG 的预览（⇧⌘V）：按缓冲区现在的文字画，改了跟着变；解析失败上面一条红字、画布上留着上一张；
// 里面有 mygo 画不出来的元素（文字、内嵌 HTML、内嵌图）淡淡地说一句。
func svgPreview(a *app.App, c *wui.Context, d *docs.Doc, focused bool) {
	t := c.Theme()
	img, errText, missing := d.SVGPreview()
	if errText != "" {
		wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(t.Danger.Alpha(0.10)).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			wui.Icon(c, iconAlert).FontSize(iconRow).TextColor(t.Danger)
			wui.Text(c, i18n.Tf("SVG 解析失败：%s", errText)).FontSize(11.5).TextColor(t.Danger).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		})
	}
	if len(missing) > 0 {
		names := make([]string, len(missing))
		for i, m := range missing {
			switch m {
			case "text":
				names[i] = i18n.T("文字")
			case "foreignObject":
				names[i] = i18n.T("内嵌 HTML")
			default:
				names[i] = i18n.T("内嵌图")
			}
		}
		wui.Text(c, i18n.Tf("预览画不出里面的%s，和浏览器里看到的不一样", i18n.List(names))).FontSize(11).TextColor(t.TextMuted).
			SingleLine().Ellipsis("…").Padding(3, 10).Shrink(0)
	}
	if img == nil {
		if focused {
			a.FocusDoc = false
		}
		message(c, iconImage, i18n.T("SVG 画不出来"), app.KeyHint("view.markdownPreview", "改好源码（%s 回去）后这里跟着变", "改好源码后这里跟着变"), t.TextMuted)
		return
	}
	imageView(a, c, d, img, focused)
}

// imageDiff 是图片的 diff（工作区、暂存区、提交里的改动）：左边旧的、右边新的，一起缩放、各自滚动；
// 每张下面一行“旧 · 名字 · 宽 × 高 · 大小”，没有的那边（新加、删掉的）写“没有”，解不出来的写为什么。
func imageDiff(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	s := d.ImgDiff
	imageBar(a, c, d, false)
	wui.Row(c).Grow(1).MinHeight(0).AlignItems(wui.Stretch).Children(func() {
		for i := range s {
			if i == 1 {
				wui.Box(c).Width(1).Shrink(0).Background(t.Border)
			}
			side := &s[i]
			which := i18n.T("旧")
			if i == 1 {
				which = i18n.T("新")
			}
			wui.Column(c.Key(i)).Grow(1).Basis(0).MinWidth(0).Children(func() {
				switch {
				case side.Bitmap != nil:
					imagePane(c, d, i, side.Bitmap, i18n.Tf("%s：%s", which, side.Name))
				case side.Err != "":
					message(c, iconAlert, side.Err, "", t.TextMuted)
				default:
					d.Img.Pane[i] = docs.ImgPane{}
					message(c, iconImage, i18n.T("没有"), "", t.TextMuted)
				}
				wui.Text(c, imgCaption(which, side)).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").
					Padding(4, 10).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			})
		}
	})
}

// imgCaption 是图片 diff 一边下面的那行。
func imgCaption(which string, s *docs.ImgSide) string {
	if s.Bitmap == nil && s.Err == "" {
		return which + " · " + i18n.T("没有")
	}
	out := which + " · " + s.Name
	if s.Bitmap != nil {
		w, h := s.Bitmap.Size()
		out += fmt.Sprintf(" · %d × %d", w, h)
	}
	return out + " · " + docs.HumanSize(int64(s.Size))
}

// hexBar 是十六进制视图上面的一条：多少字节（只显示了前 1 MB 的说一声），右边用默认应用打开、按文本 / 图片打开（切回去）。
func hexBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	note := d.HexNote()
	if d.HexTotal > docs.HexMax {
		note = i18n.Tf("十六进制 · 只显示前 1 MB，共 %s 字节", docs.Grouped(d.HexTotal))
	}
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(t.TextMuted.Alpha(0.08)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconFile).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, note).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if d.Kind == docs.File && d.Abs != "" && linkButton(c, i18n.T("用默认应用打开"), i18n.T("交给系统里打开这种文件的应用")).Clicked() {
			a.OpenDefault(d)
		}
		switch {
		case !a.CanHex(d):
		case docs.IsImage(d.Name()):
			if linkButton(c, i18n.T("按图片打开"), app.Tip(i18n.T("切回图片"), "view.hex")).Clicked() {
				a.SetHex(d, false)
			}
		default:
			if linkButton(c, i18n.T("按文本打开"), app.Tip(i18n.T("切回文本"), "view.hex")).Clicked() {
				a.SetHex(d, false)
			}
		}
	})
}
