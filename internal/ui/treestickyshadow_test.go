package ui

import (
	"fmt"
	"image/color"
	"os"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 文件树的吸顶栈底下和编辑器的粘性滚动一样投柔和的阴影、不画硬边线（#18）：树滚到 internal / ui 的文件中间、往下再滚半行，
// 钉着 [grove, internal / ui] 两层；栈底下紧挨着的像素比再往下一截（阴影外面）的暗，浅色、深色都是。
// WORKBENCH_SHOT=目录 时顺便出 tree-sticky-light.png / tree-sticky-dark.png。
func TestTreeStickyShadow(t *testing.T) {
	r := testutil.NewRepo(t, "grove")
	for i := range 60 {
		r.Write(fmt.Sprintf("internal/ui/f%02d_test.go", i), "package ui\n")
	}
	r.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, r.Root), "")
	a.ShowSidebar(workspace.ModeFiles)
	repo := a.Repos[0]
	if !repo.Expanded {
		a.ToggleRepo(repo)
	}
	a.EnsureFiles(repo)
	wait("文件列表", func() bool { return repo.Files.Loaded })
	repo.Files.Reveal("internal/ui/f00_test.go")
	tt.Frame()
	a.FileSel = -1
	rows := a.TreeRows()
	target := -1
	for i, row := range rows {
		if !row.Root && row.Path == "internal/ui/f30_test.go" {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("树里没有 internal/ui/f30_test.go")
	}
	a.FileList.ScrollTo(target, wui.Start)
	for range 4 {
		tt.Frame()
	}
	if b, ok := a.TreeSpans[target]; ok { // 再滚半行：栈底下那行被顶住一半
		tt.Wheel(b.X+b.W/2, b.Y+b.H/2, 0, b.H/2)
	}
	for range 4 {
		tt.Frame()
	}
	repoPin, internal := "grove（仓库，钉在顶上）", "internal / ui（钉在顶上）"
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		for range 3 {
			tt.Frame()
		}
		if _, ok := tt.Find(repoPin); !ok {
			t.Fatalf("（深色 %v）仓库行没钉着：%q", dark, tt.Texts())
		}
		b, ok := tt.Find(internal)
		if !ok {
			t.Fatalf("（深色 %v）internal / ui 没钉着", dark)
		}
		img := tt.Image()
		s := float32(img.Bounds().Dx()) / 1440
		x := int((b.X + b.W*0.8) * s)
		bottom := b.Y + b.H
		near := pixLum(img.At(x, int((bottom+1)*s)))
		far := pixLum(img.At(x, int((bottom+16)*s)))
		if near >= far-2 {
			t.Errorf("（深色 %v）吸顶栈底下没有阴影：紧挨着的亮度 %.1f，外面 %.1f", dark, near, far)
		}
		if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" {
			name := "tree-sticky-light.png"
			if dark {
				name = "tree-sticky-dark.png"
			}
			writeShot(t, tt, dir, name)
		}
	}
}

// pixLum 是像素的亮度（0–255）。
func pixLum(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 257
}
