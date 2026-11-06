package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// writePNG 在 root 下写一张 w×h、一种颜色的 PNG。
func writePNG(t *testing.T, root, name string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	if err := os.WriteFile(filepath.Join(root, name), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hasPrefixText 是界面上有以 p 开头的字。
func hasPrefixText(tt *wui.Tester, p string) bool {
	return slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, p) })
}

// 图片视图：大图适应着放下、小图照原大小；画布拿着键盘，= - 0 缩放；“100%”居中；⌥ 滚轮一档、双指捏合按倍数；
// 放大后拖着看；状态栏是宽 × 高 · 倍数。
func TestImageZoom(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	writePNG(t, root, "big.png", 2000, 1000, color.RGBA{0, 128, 255, 200})
	writePNG(t, root, "small.png", 20, 10, color.RGBA{255, 0, 0, 255})
	a, tt, wait := newUI(t, ws, termState(t, false))
	r := a.Repos[0]
	d := a.OpenFile(r, "big.png", true)
	a.FocusDoc = true // 像在文件树里点开的
	wait("读好", func() bool { return d.Bitmap != nil && !d.Loading })
	tt.Frame()
	tt.Frame()
	v := &d.Img
	p := v.Pane[0]
	fit := min(1, (p.W-2*imgPad)/2000, (p.H-2*imgPad)/1000)
	if p.W <= 0 || v.Zoom != 0 || v.Shown != fit || fit >= 1 {
		t.Fatalf("适应：画布 %+v、倍数 %v（应是 %v）", p, v.Shown, fit)
	}
	mustText(t, tt, "2000 × 1000 · "+zoomLabel(fit))
	if !tt.Focused(i18n.T("图片 big.png")) {
		t.Fatal("打开图片后画布没拿键盘")
	}
	tt.Key(0, wui.KeyEqual)
	tt.Frame()
	if want := docs.ZoomStep(fit, true); v.Zoom != want {
		t.Fatalf("= 后 %v，应是 %v", v.Zoom, want)
	}
	tt.Key(0, wui.KeyMinus)
	tt.Key(0, wui.KeyMinus)
	tt.Frame()
	if v.Zoom >= fit {
		t.Fatalf("两下 - 后 %v，应比适应（%v）小", v.Zoom, fit)
	}
	tt.Key(0, wui.Key0)
	tt.Frame()
	if v.Zoom != 0 || v.Shown != fit {
		t.Fatalf("0 后没回到适应：%v", v.Zoom)
	}

	mustClick(t, tt, "100%")
	tt.Frame()
	if v.Zoom != 1 || v.Scroll.MaxX <= 0 {
		t.Fatalf("100%%：%v，能横着滚 %v", v.Zoom, v.Scroll.MaxX)
	}
	if want := (2000 + 2*imgPad - p.W) / 2; v.Scroll.X < want-1 || v.Scroll.X > want+1 {
		t.Errorf("100%% 应停在中间：滚到 %v，应 %v", v.Scroll.X, want)
	}
	mustText(t, tt, "2000 × 1000 · 100%")
	if !tt.Focused(i18n.T("图片 big.png")) {
		t.Error("点了工具栏，键盘没回到画布")
	}

	box, _ := tt.Find(i18n.T("图片 big.png"))
	cx, cy := box.X+box.W/2, box.Y+box.H/2
	tt.Wheel(cx, cy, 0, 40) // 不按 ⌥ 是滚动
	tt.Frame()
	if v.Zoom != 1 {
		t.Fatalf("没按 ⌥ 的滚轮也缩放了：%v", v.Zoom)
	}
	// Tester 的滚轮带不了修饰键（mygo 按事件自己的修饰键），直接交给画布的输入回调
	if !imageInput(v, 0, &v.Scroll, wui.InputEvent{Kind: wui.InputScroll, Mods: wui.Alt, DY: -40, X: box.W / 2, Y: box.H / 2}) {
		t.Fatal("⌥ 滚轮没接")
	}
	tt.Frame()
	if v.Zoom != 1.5 {
		t.Fatalf("⌥ 滚轮一格后 %v，应是 150%%", v.Zoom)
	}
	tt.Pinch(cx, cy, 2)
	tt.Frame()
	if v.Zoom != 3 {
		t.Fatalf("捏合 2 倍后 %v", v.Zoom)
	}
	x0, y0 := v.Scroll.X, v.Scroll.Y
	tt.Press(cx, cy)
	tt.Move(cx-100, cy-50)
	tt.Frame()
	if tt.Cursor() != wui.CursorGrabbing {
		t.Errorf("拖着时指针是 %v", tt.Cursor())
	}
	tt.Release(cx-100, cy-50)
	tt.Frame()
	if v.Scroll.X != x0+100 || v.Scroll.Y != y0+50 {
		t.Errorf("拖了 (-100, -50)：滚动从 (%v, %v) 到 (%v, %v)", x0, y0, v.Scroll.X, v.Scroll.Y)
	}

	s := a.OpenFile(r, "small.png", true)
	wait("读好小图", func() bool { return s.Bitmap != nil && !s.Loading })
	tt.Frame()
	tt.Frame()
	if s.Img.Shown != 1 {
		t.Errorf("小图适应时不该放大：%v", s.Img.Shown)
	}
	mustText(t, tt, "20 × 10 · 100%")
	if !tt.HasText(i18n.T("十六进制")) {
		t.Error("位图的工具栏右边应有“十六进制”")
	}
}

// SVG：按文本开，⇧⌘V 预览——画布拿键盘、能缩放；里面有 text 的淡淡说一句；改坏了上面红字、画布留着上一张；
// 标签栏的按钮叫“SVG 预览”。
func TestSVGPreviewUI(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	src := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect width="200" height="100" fill="#3b82f6"/><circle cx="50" cy="50" r="30" fill="none" stroke="white" stroke-width="6"/><text x="100" y="60">Hi</text></svg>`
	os.WriteFile(filepath.Join(root, "logo.svg"), []byte(src), 0o644)
	a, tt, wait := newUI(t, ws, termState(t, false))
	d := a.OpenFile(a.Repos[0], "logo.svg", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()
	if _, ok := tt.Find(app.Tip(i18n.T("SVG 预览"), "view.markdownPreview")); !ok {
		t.Errorf("标签栏没有“SVG 预览”按钮：%q", tt.Texts())
	}
	a.Exec("view.markdownPreview")
	tt.Frame()
	tt.Frame()
	if !d.Preview || !tt.Focused(i18n.T("图片 logo.svg")) {
		t.Fatalf("预览 %v，画布拿键盘 %v", d.Preview, tt.Focused(i18n.T("图片 logo.svg")))
	}
	mustText(t, tt, i18n.Tf("预览画不出里面的%s，和浏览器里看到的不一样", i18n.T("文字")))
	if d.Img.Shown != 1 {
		t.Errorf("200×100 的 SVG 适应时是 %v", d.Img.Shown)
	}
	tt.Key(0, wui.KeyEqual)
	tt.Frame()
	if d.Img.Zoom != 1.5 {
		t.Errorf("= 后 %v", d.Img.Zoom)
	}
	d.Buf.Set(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect`)
	tt.Frame()
	if !hasPrefixText(tt, i18n.Tf("SVG 解析失败：%s", "")) {
		t.Fatalf("改坏了没有红字：%q", tt.Texts())
	}
	if _, ok := tt.Find(i18n.T("图片 logo.svg")); !ok {
		t.Error("改坏了画布应留着上一张")
	}
}

// 图片 diff：左右两张、下面一行“旧 · 名字 · 宽 × 高 · 大小”；新加的旧的那边写“没有”；一起缩放。
func TestImageDiffUI(t *testing.T) {
	r := testutil.NewRepo(t, "pics")
	writePNG(t, r.Root, "logo.png", 40, 40, color.RGBA{0, 0, 255, 255})
	r.Commit("加 logo")
	writePNG(t, r.Root, "logo.png", 80, 60, color.RGBA{0, 200, 0, 160})
	writePNG(t, r.Root, "new.png", 16, 16, color.RGBA{255, 200, 0, 255})
	a, tt, wait := newUI(t, testutil.Workspace(t, r.Root), termState(t, false))
	repo := a.Repos[0]
	a.RefreshAll(app.WhyOpen)
	wait("读状态", loaded(a))
	a.ShowDiffFor(repo, "logo.png")
	d := a.CurrentDoc()
	wait("读 diff", func() bool { return d != nil && !d.Loading && d.ImgDiff != nil })
	tt.Frame()
	tt.Frame()
	if !hasPrefixText(tt, i18n.T("旧")+" · logo.png · 40 × 40 · ") || !hasPrefixText(tt, i18n.T("新")+" · logo.png · 80 × 60 · ") {
		t.Fatalf("两边的说明：%q", tt.Texts())
	}
	if d.Img.Pane[0].W <= 0 || d.Img.Pane[1].W <= 0 {
		t.Fatalf("两块画布：%+v", d.Img.Pane)
	}
	mustClick(t, tt, "100%")
	if d.Img.Zoom != 1 {
		t.Errorf("100%% 后 %v", d.Img.Zoom)
	}
	a.ShowDiffFor(repo, "new.png")
	n := a.CurrentDoc()
	wait("读新文件的 diff", func() bool { return n != d && !n.Loading && n.ImgDiff != nil })
	tt.Frame()
	mustText(t, tt, i18n.T("旧")+" · "+i18n.T("没有"))
	if !hasPrefixText(tt, i18n.T("新")+" · new.png · 16 × 16 · ") {
		t.Errorf("新加的：%q", tt.Texts())
	}
}

// 十六进制视图：二进制文件直接是只读的 dump，上面一条字节数和“用默认应用打开”，状态栏是光标那个字节的偏移；
// 文本 view.hex 切过去，“按文本打开”切回来。
func TestHexViewUI(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	data := make([]byte, 40)
	for i := range data {
		data[i] = byte(i * 7)
	}
	os.WriteFile(filepath.Join(root, "blob.bin"), data, 0o644)
	a, tt, wait := newUI(t, ws, termState(t, false))
	r := a.Repos[0]
	b := a.OpenFile(r, "blob.bin", true)
	wait("读好", func() bool { return b.Buf != nil && !b.Loading })
	tt.Frame()
	tt.Frame()
	mustText(t, tt, i18n.Tf("十六进制 · %s 字节", "40"))
	mustText(t, tt, i18n.Tf("偏移 0x%X（%s）", 0, "0"))
	if tt.HasText(i18n.T("按文本打开")) || tt.HasText("UTF-8") {
		t.Errorf("二进制不该有“按文本打开”、编码：%q", tt.Texts())
	}
	b.St.GoTo(editor.Pos{Line: 1, Col: 13})
	tt.Frame()
	mustText(t, tt, i18n.Tf("偏移 0x%X（%s）", 17, "17"))
	var shown []string
	a.ShowPath = func(p string) error { shown = append(shown, p); return nil }
	mustClick(t, tt, i18n.T("用默认应用打开"))
	if len(shown) != 1 || filepath.Base(shown[0]) != "blob.bin" {
		t.Errorf("用默认应用打开：%q", shown)
	}

	d := a.OpenFile(r, "README.md", true)
	wait("读好文本", func() bool { return d.Buf != nil && !d.Loading })
	a.Exec("view.hex")
	wait("十六进制", func() bool { return d.Hex && d.Buf != nil && !d.Loading })
	tt.Frame()
	mustText(t, tt, i18n.T("按文本打开"))
	mustClick(t, tt, i18n.T("按文本打开"))
	wait("切回文本", func() bool { return !d.Hex && d.Buf != nil && !d.Loading })
	if d.Buf.String() != "# demo\n\n第一行\n" {
		t.Errorf("切回来 %q", d.Buf.String())
	}
}
