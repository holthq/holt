// Package appicon 画应用图标（Grove）：浅色圆角方块上一个钢蓝粗笔画的 G——一条弯回来的 git 分支，
// 上端一个空心的提交环，横杠末端一颗琥珀色的提交点。都用有向距离场画，任意大小都抗锯齿。
//
// mygo build 用的 1024 × 1024 图标 assets/icon.png 由它生成
// （`go test ./internal/appicon -update`），关于窗口里的小图标也是它现画的。
package appicon

import (
	"image"
	"image/color"
	"math"
)

// 坐标都按 1024 × 1024 的画布算（macOS 图标网格：主体 824，四周各留 100）。
// 图案按 100 单位的图标坐标定（方块占 9..91），u 换成画布坐标是 at(u)，长度乘 unit。
const (
	grid = 1024.0
	unit = 824.0 / 82 // 一个图标单位是多少画布单位
)

func at(u float64) float64 { return 100 + (u-9)*unit }

// 颜色（E3 浅底）：底的渐变上 → 下、G、提交环的心、提交点、G 的投影。
var (
	bgTop    = rgb(0xFFFFFF)
	bgBottom = rgb(0xE7ECF2)
	inkG     = rgb(0x3F5D7B)
	ringFill = rgb(0xF3F6F9)
	dotFill  = rgb(0xE5B567)
	shadowG  = rgb(0x35536F)
)

// Draw 画 size × size 的图标。
func Draw(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	px := grid / float64(size) // 一个像素是多少个网格单位，抗锯齿的宽度
	for y := range size {
		for x := range size {
			ux, uy := (float64(x)+0.5)*px, (float64(y)+0.5)*px
			img.SetNRGBA(x, y, pixel(ux, uy, px))
		}
	}
	return img
}

func pixel(x, y, px float64) color.NRGBA {
	body := roundRect(x, y, 512, 512, 412, 412, 185.4)
	a := cover(body, px)
	if a == 0 {
		return color.NRGBA{} // 方块外全透明，投影由 Dock / 访达自己加
	}
	c := mix(bgTop, bgBottom, clamp((y-100)/824))
	// 边上两圈各一个单位：外圈黑 8%（浅色背景上靠它和背景分开），里面一圈白 90%
	c = mix(c, rgb(0x000000), 0.08*cover(math.Abs(body+unit/2)-unit/2, px))
	c = mix(c, rgb(0xFFFFFF), 0.90*cover(math.Abs(body+unit*3/2)-unit/2, px))
	// G 的投影：往下 1.6 个单位、糊开 1.3 个单位
	if sh := gShape(x, y-1.6*unit); sh < 3*unit {
		c = mix(c, shadowG, 0.22*(1-smooth(-1.3*unit, 1.3*unit, sh)))
	}
	c = mix(c, inkG, cover(gShape(x, y), px))
	c = mix(c, ringFill, cover(circle(x, y, at(ringX), at(ringY), 4.2*unit), px))
	c = mix(c, dotFill, cover(circle(x, y, at(55), at(50), 4.6*unit), px))
	return color.NRGBA{R: uint8(c[0] + 0.5), G: uint8(c[1] + 0.5), B: uint8(c[2] + 0.5), A: uint8(a*255 + 0.5)}
}

// G 的弧：圆心 (50,50)、半径 20，从右上 −45° 逆时针走 315° 到右边 0°；提交环在弧的起点。
var ringX, ringY = 50 + 20*math.Cos(-math.Pi/4), 50 + 20*math.Sin(-math.Pi/4)

// gShape 是 G 整个（弧、横杠、提交环的外圈）的有向距离，笔画宽 8 个单位、两端圆头。
func gShape(x, y float64) float64 {
	cx, cy, r, hw := at(50), at(50), 20*unit, 4*unit
	var arc float64
	if th := math.Atan2(y-cy, x-cx); th > -math.Pi/4 && th < 0 { // 右上那 45° 是开口：只算两个端点
		arc = math.Min(circle(x, y, at(ringX), at(ringY), hw), circle(x, y, at(70), at(50), hw))
	} else {
		arc = math.Abs(math.Hypot(x-cx, y-cy)-r) - hw
	}
	bar := segment(x, y, at(70), at(50), at(55), at(50)) - hw
	ring := circle(x, y, at(ringX), at(ringY), 7.8*unit)
	return math.Min(math.Min(arc, bar), ring)
}

func rgb(v uint32) [3]float64 {
	return [3]float64{float64(v >> 16), float64(v >> 8 & 0xff), float64(v & 0xff)}
}

// mix 是 a 往 b 走 t（0..1）。
func mix(a, b [3]float64, t float64) [3]float64 {
	for i := range a {
		a[i] += (b[i] - a[i]) * t
	}
	return a
}

// cover 把有向距离（里面为负）变成覆盖率，边上过渡一个像素。
func cover(d, px float64) float64 { return clamp(0.5 - d/px) }

func clamp(v float64) float64 { return math.Min(math.Max(v, 0), 1) }

// smooth 是 a 到 b 之间的平滑过渡（smoothstep）。
func smooth(a, b, v float64) float64 {
	t := clamp((v - a) / (b - a))
	return t * t * (3 - 2*t)
}

// roundRect 是点到圆角矩形（中心 cx, cy，半宽 hw，半高 hh，圆角 r）的有向距离。
func roundRect(x, y, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(x-cx) - (hw - r)
	qy := math.Abs(y-cy) - (hh - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

// segment 是点到线段 ab 的距离。
func segment(x, y, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	h := clamp(((x-ax)*dx + (y-ay)*dy) / (dx*dx + dy*dy))
	return math.Hypot(x-ax-dx*h, y-ay-dy*h)
}

func circle(x, y, cx, cy, r float64) float64 { return math.Hypot(x-cx, y-cy) - r }
