package ui

import (
	"fmt"
	"image/color"
	"math"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 窗口底（设置 → 外观）：柔和是窗口底色上几团淡淡的颜色——状态栏中段（粉）、侧栏和编辑器之间的缝（蓝）不是纯底色，
// 偏向那团颜色，浅色深色都是；Git 栏贴在顶上的段头（列表滚下去以后）画的是同一片色斑，和旁边露出的窗口底一个颜色。
// 纯色一色到底；通透时窗口底透明（透出系统材质）。
func TestBackdrop(t *testing.T) {
	alpha := testutil.NewRepo(t, "alpha")
	alpha.Write("a.txt", "a\n")
	alpha.Commit("初始提交")
	for i := range 60 {
		alpha.Write(fmt.Sprintf("new%02d.txt", i), "x\n")
	}
	beta := testutil.NewRepo(t, "beta")
	beta.Write("b.txt", "b\n")
	beta.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, alpha.Root, beta.Root), "")
	a.St.BottomBar = false
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("两个仓库的状态", loaded(a))
	a.ShowSidebar(workspace.ModeGit)
	wait("alpha 的未跟踪文件", func() bool { return tt.HasText("new00.txt") })

	// 列表往下滚，alpha 的段头贴在顶上，下面是滚上来的行
	side, ok := tt.Find("调整侧栏宽度")
	if !ok {
		t.Fatal("找不到侧栏的拖条")
	}
	tt.Move(140, 400)
	tt.Scroll(140, 400, 0, 300)
	tt.Move(side.X+side.W+300, 400) // 指针挪开，段头不带悬停的底
	tt.Frame()
	head, ok := tt.Find("alpha（仓库）")
	if !ok || tt.HasText("new00.txt") {
		t.Fatalf("滚下去以后 alpha 的段头应贴在顶上、第一行滚走了：%v %q", ok, tt.Texts())
	}

	img := tt.Image()
	W, H := img.Bounds().Dx(), img.Bounds().Dy()
	px := func(x, y int) color.RGBA { return img.RGBAAt(x, y) }
	status := func() color.RGBA { return px(int(0.55*float32(W)), H-10) }       // 状态栏中段：粉的那团
	gap := func() color.RGBA { return px(int(side.X)+3, int(0.18*float32(H))) } // 侧栏↔编辑器的缝：蓝的那团
	headPx := func() (color.RGBA, color.RGBA) {                                 // 段头右沿里外各 2 px（外面是露出来的窗口底），同一行、躲开上沿的线
		x, y := int(head.X+head.W), int(head.Y)+3
		return px(x-2, y), px(x+2, y)
	}

	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		img = tt.Image()
		th := wui.LightTheme()
		blobs := lightBlobs
		if dark {
			th, blobs = wui.DarkTheme(), darkBlobs
		}
		bg := rgba(windowBg(th))
		// 偏向那团颜色多少（0–255）：和底色的差在"底色→那团颜色"方向上的长度
		lean := func(got color.RGBA, b blob) float64 {
			c := rgba(b.c)
			dx, dy, dz := float64(got.R)-float64(bg.R), float64(got.G)-float64(bg.G), float64(got.B)-float64(bg.B)
			ex, ey, ez := float64(c.R)-float64(bg.R), float64(c.G)-float64(bg.G), float64(c.B)-float64(bg.B)
			return (dx*ex + dy*ey + dz*ez) / math.Sqrt(ex*ex+ey*ey+ez*ez)
		}
		pink, blue := blobs[len(blobs)-3], blobs[len(blobs)-1] // 浅色四团、深色三团，末三团都是粉、桃、蓝
		if l := lean(status(), pink); status() == bg || l < 6 {
			t.Errorf("dark=%v 柔和：状态栏中段 %v，底色 %v，偏向粉 %.1f（要 ≥ 6）", dark, status(), bg, l)
		}
		if l := lean(gap(), blue); gap() == bg || l < 6 {
			t.Errorf("dark=%v 柔和：侧栏旁的缝 %v，底色 %v，偏向蓝 %.1f（要 ≥ 6）", dark, gap(), bg, l)
		}
		h, g := headPx()
		if h == bg || colorDiff(h, g) > 2 {
			t.Errorf("dark=%v 柔和：贴顶的段头 %v 和旁边露出的窗口底 %v 应一个颜色（不是底色 %v）", dark, h, g, bg)
		}

		a.St.Settings.Backdrop = "flat"
		tt.Frame()
		img = tt.Image()
		h, g = headPx()
		if status() != bg || gap() != bg || h != bg || g != bg {
			t.Errorf("dark=%v 纯色：状态栏 %v、缝 %v、段头 %v / %v 都应是底色 %v", dark, status(), gap(), h, g, bg)
		}
		a.St.Settings.Backdrop = ""
	}

	tt.SetDark(false)
	tt.SetVibrancy(true)
	a.St.Settings.Backdrop = "vibrancy"
	tt.Frame()
	img = tt.Image()
	if s, g := status(), gap(); s.A != 0 || g.A != 0 {
		t.Errorf("通透：状态栏 %v、缝 %v 应透明", s, g)
	}
}

// colorDiff 是两个颜色各通道差的最大值。
func colorDiff(a, b color.RGBA) int {
	d := 0
	for _, p := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}, {a.A, b.A}} {
		d = max(d, int(math.Abs(float64(p[0])-float64(p[1]))))
	}
	return d
}
