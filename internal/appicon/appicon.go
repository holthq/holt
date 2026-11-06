// Package appicon 是应用图标（Holt）：黄昏里的一片小树林——三棵冷杉站在坡上，
// 地平线上压着一轮琥珀色的落日，深青到暖橙的天，带纸纹。
//
// 原图（1024 × 1024，macOS 图标网格：主体 824、圆角 185.4）嵌在 icon.png 里，
// Draw 按需缩出各个尺寸：关于窗口要 256，mygo build 用的 assets/icon.png 要 1024
// （`go test ./internal/appicon -update` 从 Draw(1024) 重新生成）。
package appicon

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"
	"sync"
)

//go:embed icon.png
var iconPNG []byte

var (
	once sync.Once
	icon *image.NRGBA
)

// source 是内嵌的原图，只解一次；返回的是共享的，调用方别改。
func source() *image.NRGBA {
	once.Do(func() {
		img, err := png.Decode(bytes.NewReader(iconPNG))
		if err != nil {
			panic("appicon: 内嵌的 icon.png 解不开：" + err.Error())
		}
		if n, ok := img.(*image.NRGBA); ok {
			icon = n
			return
		}
		b := img.Bounds()
		icon = image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(icon, icon.Bounds(), img, b.Min, draw.Src)
	})
	return icon
}

// Draw 画 size × size 的图标。缩小按面积平均（预乘 alpha，边上不会发黑），
// 原尺寸返回一份拷贝，免得调用方改到内嵌的那张。
func Draw(size int) *image.NRGBA {
	src := source()
	if size <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, 0, 0))
	}
	if size == src.Bounds().Dx() && size == src.Bounds().Dy() {
		dst := image.NewNRGBA(src.Bounds())
		copy(dst.Pix, src.Pix)
		return dst
	}
	return scale(src, size)
}

// scale 把 src 缩成 size × size：每个目标像素取源图对应的矩形，
// 颜色按 alpha 加权平均（也就是在预乘空间里平均），再除回去。
func scale(src *image.NRGBA, size int) *image.NRGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		y0, y1 := y*sh/size, (y+1)*sh/size
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range size {
			x0, x1 := x*sw/size, (x+1)*sw/size
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sr, sg, sb, sa float64
			for sy := y0; sy < y1 && sy < sh; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1 && sx < sw; sx++ {
					p := row[sx*4:]
					a := float64(p[3])
					sr += float64(p[0]) * a
					sg += float64(p[1]) * a
					sb += float64(p[2]) * a
					sa += a
				}
			}
			n := float64((y1 - y0) * (x1 - x0))
			if n == 0 || sa == 0 {
				continue // 全透明的角落
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0] = clamp8(sr / sa)
			o[1] = clamp8(sg / sa)
			o[2] = clamp8(sb / sa)
			o[3] = clamp8(sa / n)
		}
	}
	return dst
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
