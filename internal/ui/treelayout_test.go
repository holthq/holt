package ui

import (
	"fmt"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 吸顶栈的几何：一个仓库下面四层目录 d1/d2/d3/d4（各带一个文件，不合并），d4 里 40 个文件；
// 一个像素一个像素往下滚，每一帧照上一帧的位置算栈：第一层是仓库、整个栈不过三行高（过渡时被盖着的、从仓库那层后面露出来的一截
// 加起来可以是四层）；钉着的行真的那行不能还露在栈底下（两份都看得见）；前后两帧都钉着的同一行挪不过 1.5（不跳，进去、出来都是）；
// 滚到 d4 的文件里是 [仓库, d3, d4]；d4 的最后一个文件上去以后 d4 那层不在了；到了仓库里的 x 只剩仓库那层。
func TestStickyLayersGeometry(t *testing.T) {
	r := &app.Repo{Name: "deep"}
	rows := []app.TreeRow{{Repo: r, Root: true, Row: files.Row{Open: true}}}
	dir := ""
	for d := 1; d <= 4; d++ {
		dir += fmt.Sprintf("/d%d", d)
		rows = append(rows, app.TreeRow{Repo: r, Row: files.Row{Path: dir[1:], Name: fmt.Sprintf("d%d", d), Depth: d, Dir: true, Open: true}})
	}
	for i := range 40 {
		rows = append(rows, app.TreeRow{Repo: r, Row: files.Row{Path: fmt.Sprintf("%s/g%02d", dir[1:], i), Depth: 5}})
	}
	for d := 4; d >= 1; d-- { // d1/d2/d3/x … x：仓库和 d1…d3 里各一个文件，排在子目录后面
		dir = dir[:len(dir)-3]
		rows = append(rows, app.TreeRow{Repo: r, Row: files.Row{Path: strings.TrimPrefix(dir+"/x", "/"), Depth: d}})
	}
	const rootH, rowH = 26, 22
	box := wui.Rect{X: 0, Y: 100, W: 270, H: 300}
	a := &app.App{TreeRootH: rootH, TreeRowH: rowH, TreeSpans: map[int]wui.Rect{}}
	name := func(i int) string {
		if rows[i].Root {
			return "deep"
		}
		return rows[i].Path
	}
	prev := map[int]float32{}
	sawCap := false
	for s := float32(0); s < 1100; s++ {
		clear(a.TreeSpans)
		first, last, y := -1, -1, box.Y+2-s
		for i, row := range rows {
			h := float32(rowH)
			if row.Root {
				h = rootH
			}
			if y+h > box.Y && y < box.Y+box.H {
				if first < 0 {
					first = i
				}
				last = i
				a.TreeSpans[i] = wui.Rect{X: 4, Y: y, W: 262, H: h}
			}
			y += h
		}
		if first < 0 {
			break
		}
		layers := stickyLayers(a, rows, box, first, last)
		desc := ""
		for _, l := range layers {
			desc += fmt.Sprintf(" %s@%.0f", name(l.i), l.y)
		}
		if len(layers) > maxSticky+1 || len(layers) > 0 && !rows[layers[0].i].Root {
			t.Fatalf("滚了 %.0f：栈是%s", s, desc)
		}
		for _, l := range layers {
			if l.y+l.h > rootH+2*rowH+0.5 {
				t.Fatalf("滚了 %.0f：栈比三行高：%s", s, desc)
			}
		}
		if len(layers) == 0 {
			if s > 30 && s < 900 {
				t.Fatalf("滚了 %.0f 栈是空的", s)
			}
			prev = map[int]float32{}
			continue
		}
		end := box.Y + layers[len(layers)-1].y + layers[len(layers)-1].h
		now := map[int]float32{}
		for _, l := range layers {
			if b, ok := a.TreeSpans[l.i]; ok && b.Y+b.H > end+0.5 {
				t.Errorf("滚了 %.0f：%s 钉着，真的那行还露在栈底下（%.0f–%.0f，栈底 %.0f）：%s", s, name(l.i), b.Y, b.Y+b.H, end, desc)
			}
			if p, ok := prev[l.i]; ok && (l.y-p > 1.5 || p-l.y > 1.5) {
				t.Errorf("滚了 %.0f：%s 那层从 %.1f 跳到 %.1f：%s", s, name(l.i), p, l.y, desc)
			}
			now[l.i] = l.y
		}
		prev = now
		if len(layers) == 3 && name(layers[1].i) == "d1/d2/d3" && name(layers[2].i) == "d1/d2/d3/d4" && layers[2].y == rootH+rowH {
			sawCap = true
		}
		// 滚到 d4 的最后一个文件也上去了：d4 那层不在了
		if b, ok := a.TreeSpans[5+39]; ok && b.Y+b.H < box.Y+rootH+2*rowH-rowH {
			for _, l := range layers {
				if name(l.i) == "d1/d2/d3/d4" {
					t.Fatalf("滚了 %.0f：d4 的文件都上去了还钉着 d4：%s", s, desc)
				}
			}
		}
	}
	if !sawCap {
		t.Error("滚到 d4 的文件里，栈没出现过 [仓库, d3, d4]")
	}
}

// 文件树的尺寸（照 VS Code 量的）：行距 22、每级缩进 10、深 4 级的文件名从 104 起、
// 行里的类型徽章 14 宽（标签上的还是 11.5）、离名字 6。名字都是 n 开头，墨迹的左边一样。
func TestTreeSizes(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("a/n1.go", "package a\n")
	repo.Write("a/b/n2.go", "package b\n")
	repo.Write("a/b/c/n3.go", "package c\n")
	repo.Write("n0.go", "package main\n")
	repo.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Current()
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded })
	r.Files.Reveal("a/b/c/n3.go")
	tt.Move(700, 400)
	wait("深的文件", func() bool { return tt.HasText("n3.go") })
	const scale = 2
	tt.SetScale(scale)
	tt.Frame()
	img := tt.Image()
	// ink 是那一行（Find 到的是整行）里的徽章和名字两块墨迹（DIP）
	ink := func(name string) (row wui.Rect, badge, text [2]float32) {
		t.Helper()
		row, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q：%q", name, tt.Texts())
		}
		bg := img.RGBAAt(int((row.X+row.W-20)*scale), int((row.Y+row.H/2)*scale))
		runs := inkRuns(img, wui.Rect{X: row.X + 1, Y: row.Y + 1, W: row.W - 30, H: row.H - 2}, scale, bg, 3)
		if len(runs) != 2 {
			t.Fatalf("%s 那行的墨迹不是徽章、名字两块：%v", name, runs)
		}
		span := func(r inkRun) [2]float32 { return [2]float32{float32(r.x0) / scale, float32(r.x1+1) / scale} }
		// 彩色底块（默认）的块和底色只差 30–80：名字前面那一截按差过 20 量（不扫参考线）
		if !a.St.Settings.TreeIconsPlain {
			text := span(runs[1])
			tile := inkRunsOver(img, wui.Rect{X: text[0] - 28, Y: row.Y + 1, W: 27, H: row.H - 2}, scale, bg, 1, 20)
			if len(tile) != 1 {
				t.Fatalf("%s 名字前面不是一块底块：%v", name, tile)
			}
			return row, span(tile[0]), text
		}
		return row, span(runs[0]), span(runs[1])
	}
	// 彩色底块：16 见方的块贴着格子，离名字 Gap 6（加上字形左边那一点）
	if _, b, n := ink("n3.go"); b[1]-b[0] < 15.5 || b[1]-b[0] > 16.5 || n[0]-b[1] < 6 || n[0]-b[1] > 7 {
		t.Errorf("彩色底块 %.1f–%.1f、名字从 %.1f 起：要 16 宽、离名字 6", b[0], b[1], n[0])
	}
	// 下面量的是线形（徽章）
	a.St.Settings.TreeIconsPlain = true
	a.SettingsChanged()
	tt.Frame()
	tt.Frame()
	img = tt.Image()
	r0, _, n0 := ink("n0.go")
	_, _, n1 := ink("n1.go")
	r2, _, n2 := ink("n2.go")
	r3, b3, n3 := ink("n3.go")
	if n3[0] < 104 || n3[0] > 105.5 {
		t.Errorf("深 4 级的文件名从 %.1f 起，应该是 104（加上字形左边那一点）", n3[0])
	}
	for _, d := range []float32{n1[0] - n0[0], n3[0] - n2[0]} {
		if d < 9.5 || d > 10.5 {
			t.Errorf("每级缩进 %.1f，应该是 10", d)
		}
	}
	if d := r2.Y - r3.Y; d != 22 || r0.H != 22 {
		t.Errorf("行距 %.1f、行高 %.1f，应该都是 22", d, r0.H)
	}
	if w := b3[1] - b3[0]; w < 13.5 || w > 15 {
		t.Errorf("徽章宽 %.1f，应该是 14", w)
	}
	if gap := n3[0] - b3[1]; gap < 6 || gap > 8 {
		t.Errorf("徽章离名字 %.1f，应该是 7（徽章在 16 的格子里居中，离右边 1，再加 Gap 6）", gap)
	}
}

// 头部那排按钮：指针在编辑器里时不画，只有"仓库"两个字；指针进侧栏画出来，行高不变、下面的行不挪；
// 指针回到编辑器又没了；列表有键盘焦点时（Tab 进来）指针不在也画。
func TestTreeHeaderHover(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Current()
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("a.txt") })
	newFile := app.Tip(i18n.T("新建文件…"), "file.new")
	at := func(x, y float32) (head, row wui.Rect, shown bool) {
		t.Helper()
		tt.Move(x, y)
		tt.Frame()
		tt.Frame()
		head, ok1 := tt.Find("仓库")
		row, ok2 := tt.Find("a.txt")
		if !ok1 || !ok2 {
			t.Fatalf("找不到头部或 a.txt：%q", tt.Texts())
		}
		_, shown = tt.Find(newFile)
		return head, row, shown
	}
	h0, r0, shown := at(700, 400)
	if shown {
		t.Fatal("指针在编辑器里，头部还画着按钮")
	}
	for _, label := range []string{app.Tip(i18n.T("添加仓库…"), "repo.add"), i18n.T("收起所有目录")} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("指针在编辑器里，头部还画着 %q", label)
		}
	}
	h1, r1, shown := at(100, 300)
	if !shown {
		t.Fatal("指针在侧栏里，头部没画按钮")
	}
	for _, label := range []string{app.Tip(i18n.T("新建文件夹…"), "file.newFolder"), app.Tip(i18n.T("添加仓库…"), "repo.add"),
		app.Tip(i18n.T("定位当前文件"), "file.revealCurrent"), i18n.T("收起所有目录"), app.Tip(i18n.T("重新读取所有仓库的 git 状态"), "git.refresh")} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("指针在侧栏里，头部没有 %q", label)
		}
	}
	if h0.X != h1.X || h0.Y != h1.Y || h0.H != h1.H || r0 != r1 { // "仓库"那段是 Grow(1)：宽跟着按钮变
		t.Errorf("按钮出来时头部 %v → %v、a.txt %v → %v，跳了", h0, h1, r0, r1)
	}
	if _, _, shown := at(700, 400); shown {
		t.Error("指针回到编辑器，按钮还在")
	}
	a.FocusTree = true
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find(newFile); !ok {
		t.Error("列表有键盘焦点时头部没画按钮")
	}
}

// 悬停高亮：淡底、实底两种选中样式下，指针下面那行（没选中）都有看得见的底色。
func TestTreeHoverBg(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Current()
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("a.txt") && tt.HasText("u.txt") })
	for _, solid := range []bool{false, true} {
		a.St.Settings.SolidSelection = solid
		a.SettingsChanged()
		a.FileSel = -1
		tt.Move(700, 400)
		tt.Frame()
		tt.Frame()
		hov, ok1 := tt.Find("a.txt")
		ref, ok2 := tt.Find("u.txt")
		if !ok1 || !ok2 {
			t.Fatalf("找不到 a.txt / u.txt：%q", tt.Texts())
		}
		x := hov.X + hov.W - 40 // 行右边空着的地方（状态字母更靠右）
		before := tt.Image().RGBAAt(int(x), int(hov.Y+hov.H/2))
		tt.Move(x, hov.Y+hov.H/2)
		tt.Frame()
		tt.Frame()
		img := tt.Image()
		after, other := img.RGBAAt(int(x), int(hov.Y+hov.H/2)), img.RGBAAt(int(x), int(ref.Y+ref.H/2))
		if d := inkDist(before, after); d < 8 {
			t.Errorf("实底=%v：悬停的行底色 %v，没悬停时 %v，差 %d 看不出来", solid, after, before, d)
		}
		if d := inkDist(after, other); d < 8 {
			t.Errorf("实底=%v：悬停的行 %v 和别的行 %v 差 %d", solid, after, other, d)
		}
	}
}
