package ui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/testutil"
)

// pxClass 把 4 倍图里的一行 / 一列像素分成：w 窗口的纯色底、c 卡片底、l 描边（比窗口底亮一截）、? 别的。
type pxClass struct {
	img       *image.RGBA
	win, card color.RGBA
}

func rgba(c wui.Color) color.RGBA { return color.RGBA{c.R, c.G, c.B, 255} }

func (p pxClass) at(x, y int) byte {
	c := p.img.RGBAAt(x, y)
	sum := func(c color.RGBA) int { return int(c.R) + int(c.G) + int(c.B) }
	switch {
	case c == p.win:
		return 'w'
	case c == p.card:
		return 'c'
	case sum(c) >= sum(p.win)+27:
		return 'l'
	}
	return '?'
}

// run 从像素 (x, y) 起往 (dx, dy) 方向读 n 个像素的分类。
func (p pxClass) run(x, y, dx, dy, n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteByte(p.at(x+i*dx, y+i*dy))
	}
	return b.String()
}

// 卡片的几何：4 倍渲染深色、窗口底是纯色的窗口（纯色底 #1F1F22、卡片 #202023、描边白 8%，深色没有阴影，像素干净；
// 柔和底的色斑会染缝，量不准），
// 逐像素量：卡片之间和外圈的缝 6 pt、终端分屏的缝 4 pt、描边 1 px（1 pt）、卡片圆角 8、当前标签和卡身之间没有线。
func TestCardGeometry(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	a.St.Settings.Backdrop = "flat"
	r := a.Repos[0]
	wait("git status", idle(r))
	a.OpenFile(r, "a.txt", true)
	wait("a.txt", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	readyPane(t, a, tt)
	a.SplitTerm(r, true)
	wait("左右分屏", func() bool { return len(a.CurTermTab(r).Panes()) == 2 })
	readyPane(t, a, tt)

	const s = 4
	tt.SetScale(s)
	tt.SetDark(true)
	tt.Frame()
	tt.Frame()
	dark := wui.DarkTheme()
	p := pxClass{img: tt.Image(), win: rgba(windowBg(dark)), card: rgba(cardBg(dark))}
	find := func(label string) wui.Rect {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("找不到 %q", label)
		}
		return r
	}
	side, right, bottom, split := find("调整侧栏宽度"), find("调整右栏宽度"), find("调整底栏高度"), find("拖动调整分屏大小")
	tab := find("标签 a.txt")
	for _, g := range []struct {
		name string
		got  float32
		want float32
	}{
		{"侧栏↔编辑器的缝", side.W, cardGap}, {"编辑器↔右栏的缝", right.W, cardGap}, {"编辑器↔底栏的缝", bottom.H, cardGap},
		{"终端分屏的缝", split.W, splitGap},
	} {
		if g.got != g.want {
			t.Errorf("%s %.1f pt，应为 %.0f", g.name, g.got, g.want)
		}
	}

	// 一条线上读像素：缝是窗口底（w），卡片的边是 4 个像素（1 pt）的描边（l），里面是卡片底（c）
	n := func(ch byte, k int) string { return strings.Repeat(string(ch), k) }
	midY := int((tab.Y + tab.H + bottom.Y) / 2 * s) // 编辑器卡片中间那一行
	edX := int((side.X + side.W + 40) * s)          // 编辑器卡片里面（躲开圆角）
	rcX := int((right.X + right.W + 40) * s)        // 右栏卡片里面
	H := float32(880)
	for _, c := range []struct {
		name, got, want string
	}{
		{"侧栏↔编辑器", p.run(int(side.X*s), midY, 1, 0, cardGap*s+s+1), n('w', cardGap*s) + n('l', s) + "c"},
		{"编辑器↔右栏", p.run(int(right.X*s)-s, midY, 1, 0, s+cardGap*s+s+1), n('l', s) + n('w', cardGap*s) + n('l', s) + "c"},
		{"右栏↔窗口右边", p.run(int((1440-cardGap)*s)-s-1, midY, 1, 0, 1+s+cardGap*s), "c" + n('l', s) + n('w', cardGap*s)},
		{"标题栏↔编辑器", p.run(edX, barHeight*s, 0, 1, cardGap*s+s), n('w', cardGap*s) + n('l', s)},
		{"编辑器↔底栏", p.run(edX, int(bottom.Y*s)-s, 0, 1, s+cardGap*s+s), n('l', s) + n('w', cardGap*s) + n('l', s)},
		{"右栏↔状态栏", p.run(rcX, int((H-statusHeight-cardGap)*s)-s-1, 0, 1, 1+s+cardGap*s), "c" + n('l', s) + n('w', cardGap*s)},
		// 分屏：两格各自铺卡片底，中间 4 pt 露出窗口底，格子没有描边
		{"终端分屏", p.run(int(split.X*s)-2, int((split.Y+split.H/2)*s), 1, 0, 2+splitGap*s+2), "cc" + n('w', splitGap*s) + "cc"},
	} {
		if c.got != c.want {
			t.Errorf("%s：\n  读到 %s\n  应为 %s", c.name, c.got, c.want)
		}
	}

	// 圆角 8：从编辑器卡片的外角沿 45° 往里走，碰到描边的距离是 r(1 − 1/√2)
	cx, cy := int((side.X+side.W)*s), int((barHeight+cardGap)*s)
	d := -1
	for i := range 6 * s {
		if p.at(cx+i, cy+i) == 'l' {
			d = i
			break
		}
	}
	radius := (float32(d) + 0.5) / s / 0.2929
	if d < 0 || radius < 7.5 || radius > 8.6 {
		t.Errorf("编辑器卡片的圆角量出来 %.2f pt（对角线上第 %d 个像素碰到描边），应为 %d", radius, d, cardRadius)
	}
	t.Logf("缝：侧栏 %.0f、右栏 %.0f、底栏 %.0f、分屏 %.0f pt；圆角 %.2f pt（对角线第 %d 像素）", side.W, right.W, bottom.H, split.W, radius, d)

	// 当前标签"长"在卡身上：标签底下那一行（卡片头的底线）是卡片底，标签外面同一行是描边；标签和卡身同色
	lineY := int((tab.Y+tab.H-1)*s) + 1
	if got := p.run(int((tab.X+tab.W/2)*s), lineY, 1, 0, 8); got != n('c', 8) {
		t.Errorf("当前标签底下应和卡身连成一片：%s", got)
	}
	if got := p.run(int((tab.X+tab.W+12)*s), lineY, 1, 0, 8); got != n('l', 8) {
		t.Errorf("当前标签右边的卡片头底下应有描边：%s", got)
	}
	if got := p.run(int(tab.X*s), int((tab.Y+tab.H/2)*s), 1, 0, s+2); got != n('l', s)+"cc" {
		t.Errorf("当前标签的左边应是 1 pt 的描边、里面是卡片底：%s", got)
	}

	// WORKBENCH_SHOT=目录 时把量的几处裁出来（4 倍）：编辑器卡片左上角、当前标签和标签条、终端分屏的缝
	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" {
		img := tt.Image()
		crop := func(name string, x, y, w, h float32) {
			r := image.Rect(int(x*s), int(y*s), int((x+w)*s), int((y+h)*s))
			savePNG(t, filepath.Join(dir, name), img.SubImage(r))
		}
		crop("card-corner-4x.png", side.X, barHeight, 40, 40)
		crop("card-tab-4x.png", tab.X-12, tab.Y-6, tab.W+60, tab.H+16)
		crop("card-split-4x.png", split.X-16, split.Y-4, splitGap+32, 40)
	}
}
