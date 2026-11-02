package appicon

import (
	"bytes"
	"flag"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "重新生成 assets/icon.png")

// TestIconFile 保证 mygo build 用的 assets/icon.png 就是 Draw(1024) 画出来的：
// 改了图标的画法跑 `go test ./internal/appicon -update`。
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
	// 四角透明，中间不透明
	if _, _, _, a := img.At(4, 4).RGBA(); a != 0 {
		t.Errorf("左上角不透明：%d", a)
	}
	if _, _, _, a := img.At(512, 160).RGBA(); a != 0xffff {
		t.Errorf("主体不是不透明的：%d", a)
	}
	// 图案（100 单位的图标坐标，at 换成像素）：G 的笔画、横杠末端的提交点、提交环的心、G 里面的底
	for _, p := range []struct {
		what string
		u, v float64
		want [3]float64
	}{
		{"G 的笔画", 30, 50, inkG},
		{"提交点", 55, 50, dotFill},
		{"提交环的心", ringX, ringY, ringFill},
		{"G 里面的底", 40, 45, mix(bgTop, bgBottom, (at(45)-100)/824)},
	} {
		c := img.At(int(at(p.u)), int(at(p.v))).(color.NRGBA)
		got := [3]float64{float64(c.R), float64(c.G), float64(c.B)}
		for i := range got {
			if math.Abs(got[i]-p.want[i]) > 2 || c.A != 255 {
				t.Errorf("%s（%g, %g）是 %v，要 %v", p.what, p.u, p.v, c, p.want)
				break
			}
		}
	}
}
