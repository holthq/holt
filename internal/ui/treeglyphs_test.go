package ui

import (
	"image/color"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 彩色底块：glyphOf 在浅色、深色主题，13 和 16 两档下的底块大小、圆角、淡色和图形——
// 代码文件两个、三个字母，线性图标的几类，不认识的和点文件是更淡的灰底块；实心选中块上是 AccentText；灰显的行是灰的。
// 文件树里画出来：彩色底块是 16 见方的淡色块（和底色差得不多，不过饱和），线形没有块。
func TestFileGlyph(t *testing.T) {
	for _, th := range []*wui.Theme{wui.LightTheme(), wui.DarkTheme()} {
		alpha := float32(tileAlpha)
		if th.Dark {
			alpha = tileAlphaDark
		}
		for _, size := range []float32{iconRow, iconTool} {
			for _, c := range []struct {
				name  string
				ink   wui.Color
				alpha float32
				badge string
				font  float32
			}{
				{"main.go", tintCyan.color(th), alpha, "Go", size * tileFont2},
				{"a.cpp", tintSlate.color(th), alpha, "C++", size * tileFont3},
				{"run.sh", tintGreen.color(th), alpha, "", 0},
				{"a.json", tintKhaki.color(th), alpha, "", 0},
				{"logo.png", tintPink.color(th), alpha, "", 0},
				{"README.md", tintDoc.color(th), alpha, "", 0},
				{"a.xyz", th.TextMuted, tileFaint, "", 0},
				{".gitignore", th.TextMuted, tileFaint, "", 0},
			} {
				g := glyphOf(th, c.name, false, false, size)
				where := c.name + map[bool]string{false: "（浅色）", true: "（深色）"}[th.Dark]
				if g.size != size || g.radius != size*tileRadius {
					t.Errorf("%s %v：底块 %v 见方、圆角 %v", where, size, g.size, g.radius)
				}
				if g.ink != c.ink || g.tile != c.ink.Alpha(c.alpha) {
					t.Errorf("%s %v：图形 %v、底块 %v，要 %v 和它的 %v", where, size, g.ink, g.tile, c.ink, c.alpha)
				}
				if g.kind.badge != c.badge {
					t.Errorf("%s：字母 %q，要 %q", where, g.kind.badge, c.badge)
				}
				if c.badge != "" && g.font != c.font {
					t.Errorf("%s %v：字号 %v，要 %v", where, size, g.font, c.font)
				}
				// 线性图标每边空 2（取整），2 倍屏上落在整像素
				if c.badge == "" && size-g.icon != 4 {
					t.Errorf("%s %v：图标 %v，要 %v", where, size, g.icon, size-4)
				}
			}
			on := glyphOf(th, "main.go", true, false, size)
			if on.ink != th.AccentText || on.tile != th.AccentText.Alpha(tileOnAccent) {
				t.Errorf("实心选中块上：图形 %v、底块 %v", on.ink, on.tile)
			}
			dim := glyphOf(th, "main.go", false, true, size)
			if dim.ink != th.TextMuted || dim.tile != th.TextMuted.Alpha(tileFaint) {
				t.Errorf("灰显的行：图形 %v、底块 %v", dim.ink, dim.tile)
			}
			if d := glyphOf(th, "main.go", true, true, size); d.ink != th.AccentText.Alpha(0.75) {
				t.Errorf("实心选中的灰显行：图形 %v", d.ink)
			}
		}
	}

	repo := testutil.NewRepo(t, "proj")
	repo.Write("main.go", "package main\n")
	repo.Write("run.sh", "echo\n")
	repo.Write("a.xyz", "?\n")
	repo.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Current()
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("a.xyz") })
	tt.Move(700, 400)
	const scale = 2
	tt.SetScale(scale)
	// box 是 name 那行名字前面的图标格子（和底色差过 20 的那块，DIP）；mid 是格子左边 1 pt、竖着正中那个像素离底色多远
	box := func(name string) (w, h float32, mid int) {
		t.Helper()
		row, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q：%q", name, tt.Texts())
		}
		img := tt.Image()
		bg := img.RGBAAt(int((row.X+row.W-20)*scale), int((row.Y+row.H/2)*scale))
		runs := inkRuns(img, wui.Rect{X: row.X + 1, Y: row.Y + 1, W: row.W - 30, H: row.H - 2}, scale, bg, 3)
		if len(runs) != 2 {
			t.Fatalf("%s 那行的墨迹不是图标、名字两块：%v", name, runs)
		}
		text := float32(runs[1].x0) / scale
		cell := inkRunsOver(img, wui.Rect{X: text - 28, Y: row.Y + 1, W: 27, H: row.H - 2}, scale, bg, 1, 20)
		if len(cell) != 1 {
			t.Fatalf("%s 名字前面不是一块图标：%v", name, cell)
		}
		b := cell[0]
		w, h = float32(b.x1+1-b.x0)/scale, float32(b.bottom+1-b.top)/scale
		return w, h, inkDist(img.RGBAAt(b.x0+scale, (b.top+b.bottom)/2), bg)
	}
	for _, name := range []string{"main.go", "run.sh", "a.xyz"} {
		w, h, mid := box(name)
		if w < 15.5 || w > 16.5 || h < 15.5 || h > 16.5 {
			t.Errorf("%s 的底块 %.1f × %.1f，要 16 见方", name, w, h)
		}
		// 底块是淡色：看得出来，但比字淡得多（字和底色差 90 以上）
		if mid < 20 || mid > 85 {
			t.Errorf("%s 的底块和底色差 %d，要在 20–85 之间", name, mid)
		}
	}
	a.St.Settings.TreeIconsPlain = true
	a.SettingsChanged()
	tt.Frame()
	tt.Frame()
	for _, name := range []string{"main.go", "run.sh"} {
		// 线形：字母徽章 14 宽（描边的方块），线性图标没有块，格子左边 1 pt 是底色
		if w, _, mid := box(name); w > 15 || (name == "run.sh" && mid > 20) {
			t.Errorf("线形的 %s 还画着底块：宽 %.1f、左边离底色 %d", name, w, mid)
		}
	}
}

// 目录按名字着色：表里的名字不分大小写、合并行看最深的那个在表里的段；node_modules 那组和不在表里的一样灰；
// 被忽略的更淡，实心选中块上是 AccentText。文件树里实心夹的正中是那个颜色，线形是空心的（正中是底色）。
func TestFolderTint(t *testing.T) {
	light, dark := wui.LightTheme(), wui.DarkTheme()
	for _, c := range []struct {
		dirs []string
		want *tint // nil：不在表里
	}{
		{[]string{"src"}, &tintBlue}, {[]string{"SRC"}, &tintBlue}, {[]string{"Docs"}, &tintKhaki},
		{[]string{"tests"}, &tintGreen}, {[]string{"a/b/tests"}, &tintGreen}, {[]string{"scripts"}, &tintGreen},
		{[]string{".github"}, &tintSlate}, {[]string{".vscode"}, &tintSlate}, {[]string{"dist"}, &tintOrange},
		{[]string{"public"}, &tintPink}, {[]string{"locales"}, &tintPurple}, {[]string{"components"}, &tintCyan},
		{[]string{"migrations"}, &tintKhaki}, {[]string{"api"}, &tintBlue},
		{[]string{"internal", "internal/ui"}, &tintCyan},          // 最深的一段在表里
		{[]string{"src", "src/main", "src/main/java"}, &tintBlue}, // 最深的两段不在表里，往上找到 src
		{[]string{"node_modules"}, nil}, {[]string{"vendor"}, nil}, {[]string{"foo"}, nil}, {[]string{"foo", "foo/bar"}, nil},
	} {
		k, ok := folderTint(c.dirs)
		if ok != (c.want != nil) || ok && k.color(light) != c.want.color(light) {
			t.Errorf("%q：%v %v，要 %v", c.dirs, k.color(light), ok, c.want)
		}
	}
	for _, th := range []*wui.Theme{light, dark} {
		grey := th.TextMuted.Alpha(0.8)
		for _, c := range []struct {
			dirs        []string
			ignored, on bool
			want        wui.Color
		}{
			{[]string{"src"}, false, false, tintBlue.color(th)},
			{[]string{"foo"}, false, false, grey},
			{[]string{"node_modules"}, false, false, grey},
			{[]string{"src"}, true, false, th.TextMuted.Alpha(0.5)},
			{[]string{"src"}, false, true, th.AccentText},
			{[]string{"foo"}, true, true, th.AccentText.Alpha(0.75)},
		} {
			if got := folderColor(th, c.dirs, c.ignored, c.on); got != c.want {
				t.Errorf("%q 忽略 %v 选中 %v（深色 %v）：%v，要 %v", c.dirs, c.ignored, c.on, th.Dark, got, c.want)
			}
		}
	}

	repo := testutil.NewRepo(t, "proj")
	for _, f := range []string{"src/a.go", "docs/a.md", "foo/a.txt", "node_modules/a.js", "internal/ui/view.go"} {
		repo.Write(f, "x\n")
	}
	repo.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Current()
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("node_modules") })
	tt.Move(700, 400)
	const scale = 2
	tt.SetScale(scale)
	// fill 是 dir 那行目录图标正中的像素和那行的底色
	fill := func(dir string) (px, bg color.RGBA) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		img := tt.Image()
		for i, row := range a.TreeRows() {
			if row.Repo != r || row.Root || row.Path != dir {
				continue
			}
			sp, ok := a.TreeSpans[i]
			if !ok {
				t.Fatalf("%s 那行没画出来", dir)
			}
			bg = img.RGBAAt(int((sp.X+sp.W-20)*scale), int((sp.Y+sp.H/2)*scale))
			runs := inkRuns(img, wui.Rect{X: sp.X + 1, Y: sp.Y + 1, W: sp.W - 30, H: sp.H - 2}, scale, bg, 3)
			if len(runs) < 3 {
				t.Fatalf("%s 那行的墨迹不是箭头、目录、名字：%v", dir, runs)
			}
			ic := runs[1]
			return img.RGBAAt((ic.x0+ic.x1)/2, (ic.top+ic.bottom)/2), bg
		}
		t.Fatalf("没有 %s 那行", dir)
		return
	}
	rgba := func(c wui.Color) color.RGBA { return color.RGBA{c.R, c.G, c.B, 255} }
	th := wui.LightTheme()
	for _, c := range []struct {
		dir  string
		want wui.Color
	}{{"src", tintBlue.color(th)}, {"docs", tintKhaki.color(th)}, {"internal/ui", tintCyan.color(th)}} {
		if px, _ := fill(c.dir); inkDist(px, rgba(c.want)) > 30 {
			t.Errorf("%s 的实心夹正中是 %v，要 %v", c.dir, px, c.want)
		}
	}
	foo, _ := fill("foo")
	nm, _ := fill("node_modules")
	src, _ := fill("src")
	if inkDist(foo, nm) > 12 || inkDist(foo, src) < 60 {
		t.Errorf("不在表里的 foo（%v）和 node_modules（%v）应该一样灰，和 src（%v）不一样", foo, nm, src)
	}
	a.St.Settings.TreeIconsPlain = true
	a.SettingsChanged()
	if px, bg := fill("src"); inkDist(px, bg) > 25 {
		t.Errorf("线形的目录正中是 %v，底色 %v：应该是空心的", px, bg)
	}
}
