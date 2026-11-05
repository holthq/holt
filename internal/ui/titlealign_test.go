package ui

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// inkRun 是一个元素里连在一起的一块墨迹（图标或一段文字）：横向从 x0 到 x1（像素），竖向墨迹的上下沿。
type inkRun struct {
	x0, x1, top, bottom int
}

// centre 是墨迹竖向的中心（DIP）。
func (r inkRun) centre(scale float32) float32 { return float32(r.top+r.bottom+1) / 2 / scale }

// inkDist 是两个像素 RGB 差的和。
func inkDist(a, b color.RGBA) int {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) + d(a.G, b.G) + d(a.B, b.B)
}

// inkRuns 逐列扫 rect（DIP）里和底色 bg 差得多的像素，横向隔开 gap DIP 以上的算两块。
func inkRuns(img *image.RGBA, rect wui.Rect, scale float32, bg color.RGBA, gap float32) []inkRun {
	return inkRunsOver(img, rect, scale, bg, gap, 90)
}

// inkRunsOver 是和底色差过 thr（inkDist）就算墨迹的 inkRuns：量淡的东西用（彩色底块和底色只差 30–80）。
func inkRunsOver(img *image.RGBA, rect wui.Rect, scale float32, bg color.RGBA, gap float32, thr int) []inkRun {
	x0, y0 := int(rect.X*scale), int(rect.Y*scale)
	x1, y1 := int((rect.X+rect.W)*scale), int((rect.Y+rect.H)*scale)
	ink := func(x, y int) bool { return inkDist(img.RGBAAt(x, y), bg) > thr }
	var runs []inkRun
	cur := (*inkRun)(nil)
	empty := 0
	for x := x0; x < x1; x++ {
		top, bottom := -1, -1
		for y := y0; y < y1; y++ {
			if ink(x, y) {
				if top < 0 {
					top = y
				}
				bottom = y
			}
		}
		if top < 0 {
			empty++
			if cur != nil && float32(empty) >= gap*scale {
				runs = append(runs, *cur)
				cur = nil
			}
			continue
		}
		empty = 0
		if cur == nil {
			cur = &inkRun{x0: x, x1: x, top: top, bottom: bottom}
			continue
		}
		cur.x1 = x
		cur.top, cur.bottom = min(cur.top, top), max(cur.bottom, bottom)
	}
	if cur != nil {
		runs = append(runs, *cur)
	}
	return runs
}

// rowRuns 是横条 strip（DIP）里的墨迹，每块的中心离 mid 多远；ok 为都不过 0.5 pt。
func rowRuns(img *image.RGBA, strip wui.Rect, scale float32, bg color.RGBA, mid, gap float32) (desc string, ok bool) {
	ok = true
	for _, run := range inkRuns(img, strip, scale, bg, gap) {
		if run.x1-run.x0 < int(2*scale) { // 点号之类：墨迹本来就不在字的中间
			continue
		}
		d := run.centre(scale) - mid
		desc += fmt.Sprintf(" [%.1f–%.1f %+.2f]", float32(run.x0)/scale, float32(run.x1+1)/scale, d)
		if d > 0.5 || d < -0.5 {
			ok = false
		}
	}
	return desc, ok
}

// 标题栏上的图标和文字竖向对齐：4 倍渲染，逐列扫出每个按钮里一块块的墨迹（图标、一段字），
// 每块的中心和标题栏的中线（= 红绿灯的中心）差不过 0.5 pt；面包屑里类型图标离文件名 3–4 pt。
// 标签条、Git 栏的仓库段头和分组标题、状态栏也一样（它们没有红绿灯，按自己那一行的中线）。
func TestTitleBarAlign(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenFile(r, "a.txt", true)
	wait("a.txt", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	a.Exec("view.git")
	const scale = 4
	tt.SetScale(scale)
	tt.SetTitleBar(shotBar)
	tt.Frame()
	tt.Frame()
	img := tt.Image()
	bg := img.RGBAAt(40*scale, 4*scale) // 红绿灯那块空着的地方
	mid := float32(TrafficLights.Y) + trafficLightH/2.0
	if mid != barHeight/2.0 {
		t.Errorf("红绿灯的中心 %.1f 不在标题栏的中线 %.1f 上", mid, barHeight/2.0)
	}
	for _, label := range []string{"侧栏（⌘B）", "工作区 demo", "后退（⌃-）", "前进（⌃⇧-）", "位置 demo/a.txt", "快速打开",
		"分支 main", "抓取（没有远程）", "Git 操作", "右栏（历史）（⌥⌘B）", "底栏（终端 / 输出）（⌘J）"} {
		rect, ok := tt.Find(label)
		if !ok {
			t.Errorf("找不到 %q", label)
			continue
		}
		in := wui.Rect{X: rect.X + 1.5, Y: 1.5, W: rect.W - 3, H: barHeight - 3} // 不扫边框
		if desc, ok := rowRuns(img, in, scale, bg, mid, 2.5); !ok || desc == "" {
			t.Errorf("%s 没对齐中线 %.1f：%s", label, mid, desc)
		}
	}

	// 类型图标挨着文件名：彩色底块（默认）量块的边，线形量图标的墨迹，都离文件名 3–4 pt
	file, _ := tt.Find("位置 demo/a.txt")
	crumbGap := func(img *image.RGBA, thr int) {
		t.Helper()
		bg := img.RGBAAt(int((file.X-8)*scale), 3*scale) // 图标正上方
		var icon, name *inkRun
		for _, run := range inkRunsOver(img, wui.Rect{X: 76, Y: 1.5, W: file.X + file.W - 76, H: barHeight - 3}, scale, bg, 1.5, thr) {
			if float32(run.x0)/scale < file.X {
				icon = &run
			} else if name == nil {
				name = &run
			}
		}
		if icon == nil || name == nil {
			t.Fatalf("面包屑里找不到类型图标和文件名（线形 %v）", a.St.Settings.TreeIconsPlain)
		}
		if gap := float32(name.x0-icon.x1-1) / scale; gap < 3 || gap > 4 {
			t.Errorf("类型图标离文件名 %.2f pt（线形 %v）", gap, a.St.Settings.TreeIconsPlain)
		}
	}
	crumbGap(img, 20)
	defer func() {
		a.St.Settings.TreeIconsPlain = true
		a.SettingsChanged()
		tt.Frame()
		tt.Frame()
		crumbGap(tt.Image(), 90)
	}()

	// 别的横条：没有红绿灯，按自己的中线
	tab, _ := tt.Find("标签 a.txt")
	grp, _ := tt.Find("收起 未暂存")
	hdr, _ := tt.Find("收起 demo")
	h := float32(880) // newUI 的 Tester 是 1440×880
	for _, row := range []struct {
		name  string
		strip wui.Rect
		mid   float32
	}{
		{"标签条", wui.Rect{X: tab.X + 1, Y: tab.Y + 3, W: tab.W - 2, H: tab.H - 4}, tab.Y + tab.H/2}, // 不扫上面的强调色条
		{"Git 栏仓库段头", wui.Rect{X: 1, Y: hdr.Y + hdr.H/2 - 13, W: grp.X + 200, H: 26}, hdr.Y + hdr.H/2},
		{"Git 栏分组标题", wui.Rect{X: 1, Y: grp.Y + grp.H/2 - 10, W: grp.X + 120, H: 20}, grp.Y + grp.H/2},
		{"状态栏", wui.Rect{X: 1, Y: h - statusHeight - cardGap + 1, W: 600, H: statusHeight + cardGap - 2}, h - (statusHeight+cardGap)/2},
	} {
		bg := img.RGBAAt(int(row.strip.X*scale)+1, int((row.strip.Y+1)*scale))
		if desc, ok := rowRuns(img, row.strip, scale, bg, row.mid, 2.5); !ok || desc == "" {
			t.Errorf("%s 没对齐中线 %.1f：%s", row.name, row.mid, desc)
		}
	}
}

// 状态栏两头和上面那一列的内容齐：左边第一个字和侧栏头部的"仓库"一条竖线，右边最后一个字的右缘和右栏卡片里
// "提交图"开关的右缘一条竖线；侧栏、右栏关掉也不跳；字在卡片底边和窗口底边中间，侧栏的筛选框底边和卡片底边齐。
func TestStatusBarEdges(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenFile(r, "a.txt", true)
	wait("a.txt", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	a.St.Sidebar, a.St.RightBar = true, true
	tt.Frame()
	find := func(s string) wui.Rect {
		t.Helper()
		rc, ok := tt.Find(s)
		if !ok {
			t.Fatalf("找不到 %q：%q", s, tt.Texts())
		}
		return rc
	}
	const w, h = 1440, 880 // newUI 的 Tester 是 1440×880
	left, right := find("当前仓库 demo"), find("有未提交的改动")
	// 字在卡片底边和窗口底边中间（只在下面 22 里居中的话看着偏下 3 pt）
	bottom := float32(h - statusHeight - cardGap) // 卡片底边（TestCardGeometry 量过）
	if mid := left.Y + left.H/2; mid > (bottom+h)/2+0.5 || mid < (bottom+h)/2-0.5 {
		t.Errorf("状态栏的字中线在 %.1f，卡片底边 %.0f 和窗口底边 %d 的中点是 %.1f", mid, bottom, h, (bottom+h)/2)
	}
	// 侧栏底下的筛选框和卡片底边一条线
	a.ShowSidebar(workspace.ModeFiles)
	tt.Frame()
	if f := find("筛选文件"); f.Y+f.H != bottom {
		t.Errorf("筛选框的底边在 %.1f，卡片底边在 %.0f", f.Y+f.H, bottom)
	}
	if head := find("仓库"); left.X != head.X {
		t.Errorf("状态栏左边从 %.1f 起，侧栏头部的字从 %.1f 起", left.X, head.X)
	}
	if graph := find(app.Tip("关闭右栏", "view.right")); right.X+right.W != graph.X+graph.W || graph.X+graph.W != w-statusPadR {
		t.Errorf("状态栏右边到 %.1f，右栏里的关闭按钮到 %.1f", right.X+right.W, graph.X+graph.W)
	}
	a.St.Sidebar, a.St.RightBar = false, false
	tt.Frame()
	if l, r := find("当前仓库 demo"), find("有未提交的改动"); l.X != left.X || r.X+r.W != right.X+right.W {
		t.Errorf("侧栏、右栏关掉以后状态栏两头跳了：%.1f–%.1f，原来 %.1f–%.1f", l.X, r.X+r.W, left.X, right.X+right.W)
	}
}
