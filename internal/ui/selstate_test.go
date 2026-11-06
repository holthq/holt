package ui

import (
	"image/color"
	"os"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 文件树（淡底，默认）的三种行底色在浅色、深色主题里都分得开：悬停 < 非活动选中（焦点在编辑器，中性灰）< 活动选中
// （树有键盘，强调色）；非活动选中是灰的（R、G、B 差不多），活动选中带强调色的色相。
// WORKBENCH_SHOT=目录 时顺便出截图：tree-selection-active / tree-selection-inactive，各 -light / -dark。
func TestSelectedRowStates(t *testing.T) {
	r0 := testutil.NewRepo(t, "states")
	for _, name := range []string{"go.mod", "main.go", "util.go", "zz.txt"} {
		r0.Write(name, "package main\n")
	}
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	const scale = 2
	tt.SetScale(scale)
	a.St.BottomBar, a.St.RightBar = false, false
	a.St.Settings.Backdrop = "flat"
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	a.ShowSidebar(workspace.ModeFiles)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return idle(r)() && r.Files.Loaded && tt.HasText("zz.txt") })
	d := a.OpenFile(r, "main.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	a.Reveal(r, "main.go")
	wait("选中 main.go", func() bool { row, ok := a.TreeRowAt(a.FileSel); return ok && row.Path == "main.go" })

	// 行底色取行右边往里 20 pt：选中的 main.go、悬停的 util.go、什么都不是的 zz.txt
	type px struct{ sel, hov, bg color.RGBA }
	sample := func() px {
		t.Helper()
		tt.Frame()
		tt.Frame()
		at := func(name string) color.RGBA {
			t.Helper()
			b, ok := tt.Find(name)
			if !ok {
				t.Fatalf("找不到 %s：%q", name, tt.Texts())
			}
			return tt.Image().RGBAAt(int((b.X+b.W-20)*scale), int((b.Y+b.H/2)*scale))
		}
		return px{at("main.go"), at("util.go"), at("zz.txt")}
	}
	dist := func(x, y color.RGBA) int {
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		return d(x.R, y.R) + d(x.G, y.G) + d(x.B, y.B)
	}
	chroma := func(c color.RGBA) int {
		return max(int(c.R), int(c.G), int(c.B)) - min(int(c.R), int(c.G), int(c.B))
	}
	dir := os.Getenv("WORKBENCH_SHOT")

	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		tt.SetDark(dark)
		b, _ := tt.Find("util.go")
		tt.Move(b.X+b.W-20, b.Y+b.H/2)

		a.FocusDoc = true // 点了编辑器
		tt.Frame()
		in := sample()
		if dir != "" {
			writeShot(t, tt, dir, "tree-selection-inactive-"+theme+".png")
		}
		a.FocusTree = true // 点回树
		tt.Frame()
		act := sample()
		if dir != "" {
			writeShot(t, tt, dir, "tree-selection-active-"+theme+".png")
		}

		if dist(in.sel, act.sel) < 12 {
			t.Errorf("%s：树有键盘 %v 和编辑器有键盘 %v 时选中行的底色该不一样", theme, act.sel, in.sel)
		}
		if dist(in.hov, in.bg) < 6 || dist(in.hov, in.sel) < 6 || dist(act.hov, act.sel) < 12 {
			t.Errorf("%s：悬停 %v 该和底 %v、非活动选中 %v、活动选中 %v 都分得开", theme, in.hov, in.bg, in.sel, act.sel)
		}
		// 悬停比非活动选中淡：离底色更近
		if dist(in.hov, in.bg) >= dist(in.sel, in.bg) {
			t.Errorf("%s：悬停 %v 该比非活动选中 %v 淡（底 %v）", theme, in.hov, in.sel, in.bg)
		}
		if chroma(in.sel) > chroma(in.bg)+4 || chroma(act.sel) <= chroma(act.bg)+4 {
			t.Errorf("%s：非活动选中 %v 该是灰的，活动选中 %v 该带强调色（底 %v）", theme, in.sel, act.sel, in.bg)
		}
	}
}
