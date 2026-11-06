package ui

import (
	"image"
	"image/color"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 强调色：跟随系统时主题不动，默认（存成 ""）是钢蓝；选了的那个浅色、深色主题各换一套，悬停 / 按下往黑（深色往白）调，
// 石墨在深色主题里的字是深的，高对比度时焦点环还是不透明。
func TestApplyAccent(t *testing.T) {
	a := &app.App{}
	base := func(dark bool) wui.Theme {
		th := wui.Theme{Dark: dark, Accent: wui.Hex("#0a84ff"), AccentText: wui.Hex("#ffffff"), Focus: wui.Hex("#0a84ff").Alpha(0.55)}
		return th
	}
	for _, v := range []string{"system", "nope"} {
		a.St.Settings.Accent = v
		th := base(false)
		applyAccent(a, &th)
		if th != base(false) {
			t.Errorf("强调色 %q 不该改主题：%+v", v, th)
		}
	}
	a.St.Settings.Accent = ""
	def := base(false)
	applyAccent(a, &def)
	a.St.Settings.Accent = workspace.DefaultAccent
	th := base(false)
	applyAccent(a, &th)
	if def.Accent != wui.Hex("#4E6E8E") || th != def {
		t.Errorf("默认的强调色该是钢蓝：%v，选了钢蓝 %v", def.Accent, th.Accent)
	}
	a.St.Settings.Accent = ""
	def = base(true)
	applyAccent(a, &def)
	if def.Accent != wui.Hex("#8FA9C6") || def.AccentText != wui.Hex("#18181B") {
		t.Errorf("深色钢蓝：%v，字 %v", def.Accent, def.AccentText)
	}

	a.St.Settings.Accent = "teal"
	th = base(false)
	applyAccent(a, &th)
	teal := wui.Hex("#2E8B78")
	if th.Accent != teal || th.AccentText != wui.Hex("#ffffff") {
		t.Errorf("浅色青绿：%v / %v", th.Accent, th.AccentText)
	}
	if want := teal.Mix(wui.Hex("#000000"), 0.08); th.AccentHover != want {
		t.Errorf("浅色悬停 %v，应是 %v", th.AccentHover, want)
	}
	if want := teal.Mix(wui.Hex("#000000"), 0.16); th.AccentPressed != want {
		t.Errorf("浅色按下 %v，应是 %v", th.AccentPressed, want)
	}
	if th.Selection != teal.Alpha(0.25) || th.Focus != teal.Alpha(0.55) {
		t.Errorf("浅色选中的字 %v、焦点环 %v", th.Selection, th.Focus)
	}

	th = base(true)
	applyAccent(a, &th)
	if th.Accent != wui.Hex("#4FB8A2") || th.AccentHover != wui.Hex("#4FB8A2").Mix(wui.Hex("#ffffff"), 0.08) || th.Selection != wui.Hex("#4FB8A2").Alpha(0.4) {
		t.Errorf("深色青绿：%v / %v / %v", th.Accent, th.AccentHover, th.Selection)
	}

	a.St.Settings.Accent = "graphite"
	th = base(true)
	applyAccent(a, &th)
	if th.Accent != wui.Hex("#9A9CA3") || th.AccentText != wui.Hex("#18181B") {
		t.Errorf("深色石墨：%v，字 %v", th.Accent, th.AccentText)
	}

	th = base(false)
	th.Focus.A = 255
	applyAccent(a, &th)
	if th.Focus.A != 255 {
		t.Errorf("高对比度时焦点环该不透明：%v", th.Focus)
	}
}

// 文件树里选中的行：实心时跟随系统是系统的强调色、默认是钢蓝、选了青绿是青绿的实心块、字是白的；默认的淡底是青绿淡淡的一层
// （侧栏底色上叠 14%），字还是正文的深色，左边一道青绿的竖条（列表有键盘时粗一点）。
func TestSelectedRowLook(t *testing.T) {
	r0 := testutil.NewRepo(t, "look")
	for _, name := range []string{"go.mod", "main.go", "zz.txt"} {
		r0.Write(name, "x\n")
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
	a.Reveal(r, "main.go")
	wait("选中 main.go", func() bool { row, ok := a.TreeRowAt(a.FileSel); return ok && row.Path == "main.go" })

	type shot struct {
		img         *image.RGBA
		name, other wui.Rect // 选中的 main.go、没选中的 zz.txt 那一行
	}
	frame := func() shot {
		t.Helper()
		tt.Frame()
		tt.Frame()
		name, ok := tt.Find("main.go")
		other, ok2 := tt.Find("zz.txt")
		if !ok || !ok2 {
			t.Fatalf("找不到文件树的行：%q", tt.Texts())
		}
		return shot{tt.Image(), name, other}
	}
	px := func(s shot, x, y float32) color.RGBA { return s.img.RGBAAt(int(x*scale), int(y*scale)) }
	// 行底色取行右边往里 20 pt 的地方，没选中的那行同一列是侧栏的底色
	rowBg := func(s shot) (sel, bg color.RGBA) {
		x := s.name.X + s.name.W - 20
		return px(s, x, s.name.Y+s.name.H/2), px(s, x, s.other.Y+s.other.H/2)
	}
	near := func(c color.RGBA, w wui.Color, tol int) bool {
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		return d(c.R, w.R) <= tol && d(c.G, w.G) <= tol && d(c.B, w.B) <= tol
	}
	// 文件名的字在行里 70–130 pt（缩进一层、图标后面）
	darkest := func(s shot, row wui.Rect) int {
		r := wui.Rect{X: row.X + 70, Y: row.Y + 2, W: 60, H: row.H - 4}
		m := 3 * 255
		for y := int(r.Y * scale); y < int((r.Y+r.H)*scale); y++ {
			for x := int(r.X * scale); x < int((r.X+r.W)*scale); x++ {
				c := s.img.RGBAAt(x, y)
				m = min(m, int(c.R)+int(c.G)+int(c.B))
			}
		}
		return m
	}

	// 实心、跟随系统：系统的强调色；默认（存成 ""）是钢蓝，不管系统
	a.St.Settings.SolidSelection = true
	pink := wui.Hex("#D9366B")
	tt.SetPreferences(wui.Preferences{Accent: pink})
	a.St.Settings.Accent = "system"
	if sel, _ := rowBg(frame()); !near(sel, pink, 3) {
		t.Errorf("跟随系统时选中行是 %v，系统的强调色 %v", sel, pink)
	}
	a.St.Settings.Accent = ""
	if sel, _ := rowBg(frame()); !near(sel, wui.Hex("#4E6E8E"), 3) {
		t.Errorf("默认的选中行是 %v，应是钢蓝", sel)
	}

	// 青绿实心：字是白的（最深的像素也就是青绿本身）
	teal := wui.Hex("#2E8B78")
	a.St.Settings.Accent = "teal"
	s := frame()
	if sel, _ := rowBg(s); !near(sel, teal, 3) {
		t.Errorf("青绿实心的选中行是 %v", sel)
	}
	if m := darkest(s, s.name); m < 250 {
		t.Errorf("实心时选中行的字该是白的，最深的像素 %d", m)
	}

	// 默认的淡底，列表没有键盘（焦点在编辑器）：非活动选中，侧栏底色上叠一层中性的灰（字色 8%），字是深的，没有竖条
	a.St.Settings.SolidSelection = false
	a.FocusTree = false
	s = frame()
	sel, bg := rowBg(s)
	opaque := func(c color.RGBA) wui.Color { return wui.Color{R: c.R, G: c.G, B: c.B, A: 255} }
	if want := opaque(bg).Mix(lightTheme().Text, 0.08); !near(sel, want, 3) {
		t.Errorf("非活动选中的行是 %v，侧栏底色 %v，应是 %v", sel, bg, want)
	}
	if m := darkest(s, s.name); m > 200 {
		t.Errorf("淡底时选中行的字该是正文的深色，最深的像素 %d", m)
	}
	bar := func(s shot) float32 {
		t.Helper()
		y := int((s.name.Y + s.name.H/2) * scale)
		n := 0
		for x := 0; x < int((s.name.X+20)*scale); x++ { // 图标在 60 pt 往右
			if near(s.img.RGBAAt(x, y), teal, 24) {
				n++
			}
		}
		return float32(n) / scale
	}
	if w := bar(s); w != 0 {
		t.Errorf("非活动选中不该有强调色的竖条：%.1f pt", w)
	}
	inactive := sel

	// 列表有键盘：活动选中，侧栏底色上叠 20% 的青绿，左边一道 3 pt 的竖条
	a.FocusTree = true
	tt.Frame()
	s = frame()
	sel, bg = rowBg(s)
	if want := opaque(bg).Mix(teal, softAlpha(lightTheme())); !near(sel, want, 3) || near(sel, opaque(inactive), 3) {
		t.Errorf("活动选中的行是 %v，侧栏底色 %v，应是 %v（非活动 %v）", sel, bg, want, inactive)
	}
	if w := bar(s); w < 2 || w > 4 {
		t.Errorf("活动选中的竖条 %.1f pt", w)
	}

	// 悬停的行：比非活动选中还淡，和三者都分得开
	tt.Move(s.other.X+s.other.W-20, s.other.Y+s.other.H/2)
	s = frame()
	_, hov := rowBg(s)
	if want := opaque(bg).Mix(lightTheme().Text, 0.04); !near(hov, want, 3) || near(hov, opaque(inactive), 3) || near(hov, opaque(sel), 3) || near(hov, opaque(bg), 2) {
		t.Errorf("悬停的行是 %v，应是 %v（非活动选中 %v、活动选中 %v、底 %v）", hov, want, inactive, sel, bg)
	}

	// 点进编辑器：树上的选中变灰；焦点回到树：又是强调色
	active := sel
	d := a.OpenFile(r, "main.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	a.FocusDoc = true
	tt.Frame()
	if got, _ := rowBg(frame()); !near(got, opaque(inactive), 3) {
		t.Errorf("编辑器有键盘时选中行是 %v，应是非活动选中 %v", got, inactive)
	}
	a.FocusTree = true
	tt.Frame()
	if got, _ := rowBg(frame()); !near(got, opaque(active), 3) {
		t.Errorf("焦点回到树时选中行是 %v，应是活动选中 %v", got, active)
	}
}

// lightTheme 是默认设置下应用实际画的浅色主题：mygo 的浅色主题套上默认的强调色（钢蓝，不是 mygo 跟的系统色）。
func lightTheme() *wui.Theme {
	th := wui.LightTheme()
	applyAccent(&app.App{}, th)
	return th
}
