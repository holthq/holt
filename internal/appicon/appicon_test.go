package appicon

import (
	"bytes"
	"flag"
	"image/color"
	"image/png"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "重新生成 assets/icon.png")

// TestIconFile 保证 mygo build 用的 assets/icon.png 就是 Draw(1024) 编出来的：
// 换了 icon.png 跑 `go test ./internal/appicon -update`。
func TestIconFile(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, Draw(1024)); err != nil {
		t.Fatal(err)
	}
	const path = "../../assets/icon.png"
	if *update {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, b.Bytes()) {
		t.Fatalf("%s 和 Draw(1024) 不一样，跑 go test ./internal/appicon -update", path)
	}
	img, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if s := img.Bounds().Size(); s.X != 1024 || s.Y != 1024 {
		t.Fatalf("图标是 %v，要 1024 × 1024", s)
	}
}

// TestDraw 查画出来的各个尺寸：圆角外透明、内容没画歪、缩小后还是那张图。
func TestDraw(t *testing.T) {
	big := Draw(1024)
	for _, p := range []struct {
		name string
		x, y int
	}{
		{"左上角", 4, 4}, {"右上角", 1019, 4}, {"左下角", 4, 1019}, {"右下角", 1019, 1019},
	} {
		if _, _, _, a := big.At(p.x, p.y).RGBA(); a != 0 {
			t.Errorf("%s不透明：%d", p.name, a)
		}
	}
	// 中间是落日的暖橙，天是深青，地是暗绿——画歪了这几处就对不上。
	for _, c := range []struct {
		what string
		x, y int
		ok   func(r, g, b uint8) bool
	}{
		{"落日", 512, 512, func(r, g, b uint8) bool { return r > 220 && g > 110 && g < 190 && b < 110 }},
		{"天", 512, 180, func(r, g, b uint8) bool { return r < 60 && g > 45 && b > 45 && g > r }},
		{"地", 512, 920, func(r, g, b uint8) bool { return r < 70 && g < 70 && b < 70 }},
	} {
		c0 := color.NRGBAModel.Convert(big.At(c.x, c.y)).(color.NRGBA)
		if c0.A != 255 || !c.ok(c0.R, c0.G, c0.B) {
			t.Errorf("%s（%d, %d）是 %v，不像", c.what, c.x, c.y, c0)
		}
	}

	// 缩小：角落还是透明，中间还是落日，边长对。
	for _, size := range []int{256, 64, 16} {
		img := Draw(size)
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("Draw(%d) 是 %v", size, b.Size())
		}
		if _, _, _, a := img.At(1, 1).RGBA(); a != 0 {
			t.Errorf("Draw(%d) 的角不透明：%d", size, a)
		}
		c := color.NRGBAModel.Convert(img.At(size/2, size/2)).(color.NRGBA)
		if c.A != 255 || c.R < 180 || c.R <= c.B {
			t.Errorf("Draw(%d) 的中心是 %v，不像落日", size, c)
		}
	}
}

// TestDrawCopy：改返回值不能改到内嵌的原图。
func TestDrawCopy(t *testing.T) {
	a := Draw(1024)
	a.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	if _, _, _, al := Draw(1024).At(0, 0).RGBA(); al != 0 {
		t.Fatal("Draw(1024) 返回的是共享的原图")
	}
}
