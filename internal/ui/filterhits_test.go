package ui

import (
	"image/color"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 筛选的行标出命中的字：名字里命中的（notes/docs.txt 的 docs）和后面淡字目录里命中的（docs/api.md 的 docs）
// 都垫一块淡强调色（和快速打开一样）；名字里的在左边、目录里的在名字后面。不筛选的树没有。
func TestTreeFilterHits(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("notes/docs.txt", "n\n")
	repo.Write("docs/api.md", "a\n")
	repo.Write("main.go", "package main\n")
	repo.Commit("init")
	a, tt, wait := filesUI(t, testutil.Workspace(t, repo.Root), "main.go")
	tt.Move(700, 400)
	const scale = 2
	tt.SetScale(scale)
	th := lightTheme()
	// hl 量 name 那行垫着命中底色的像素：个数和最左的 x（DIP）
	hl := func(name string) (n int, left float32) {
		t.Helper()
		row, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q：%q", name, tt.Texts())
		}
		img := tt.Image()
		bg := img.RGBAAt(int((row.X+row.W-6)*scale), int((row.Y+row.H/2)*scale))
		k := float32(th.Accent.A) / 255 * 0.22
		mix := func(b, f uint8) uint8 { return uint8(float32(b)*(1-k) + float32(f)*k + 0.5) }
		want := color.RGBA{mix(bg.R, th.Accent.R), mix(bg.G, th.Accent.G), mix(bg.B, th.Accent.B), 255}
		x0, x1 := int(row.X*scale), int((row.X+row.W)*scale)
		minX := x1
		for y := int(row.Y * scale); y < int((row.Y+row.H)*scale); y++ {
			for x := x0; x < x1; x++ {
				if inkDist(img.RGBAAt(x, y), want) <= 9 {
					n++
					minX = min(minX, x)
				}
			}
		}
		return n, float32(minX) / scale
	}

	a.SetFilter("docs")
	wait("筛选", func() bool { return tt.HasText("docs.txt") && tt.HasText("api.md") && !tt.HasText("main.go") })
	tt.Frame()
	n1, left1 := hl("docs.txt")
	n2, left2 := hl("api.md")
	if n1 < 60 || n2 < 60 {
		t.Fatalf("命中的字没垫底色：docs.txt %d 个像素、api.md %d 个", n1, n2)
	}
	if left2-left1 < 20 {
		t.Errorf("api.md 那行的底色从 %.1f 开始、docs.txt 那行从 %.1f：目录里的命中该在名字后面", left2, left1)
	}

	a.SetFilter("")
	wait("树", func() bool { return tt.HasText("main.go") })
	a.Reveal(a.Repos[0], "notes/docs.txt")
	tt.Frame()
	tt.Frame()
	if n, _ := hl("docs.txt"); n > 6 {
		t.Errorf("不筛选的树也垫了底色：%d 个像素", n)
	}
}
