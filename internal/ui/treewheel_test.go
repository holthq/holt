package ui

import (
	"fmt"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"
	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 文件树的鼠标滚轮（issue #58 第 5 节）：一格走一个行高、每帧的位移不超过两个行高、连续几格之间不停、
// 到头反向立刻动、吸顶栈读的行位置和这一帧画的一样（不晚一帧）。
func TestTreeWheel(t *testing.T) {
	r := testutil.NewRepo(t, "grove")
	for i := range 200 {
		r.Write(fmt.Sprintf("internal/ui/f%03d.go", i), "package ui\n")
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
	repo.Files.Reveal("internal/ui/f000.go")
	tt.Frame()
	a.FileSel = -1
	tt.Frame()
	s := a.SideScrolls[&a.FileList]
	if s == nil {
		t.Fatal("文件树没有 SideScroll")
	}
	if s.S.MaxY <= 0 {
		t.Fatal("文件树滚不动")
	}
	rowH := a.TreeRowH
	lb, ok := tt.Find("f010.go")
	if !ok {
		t.Fatalf("找不到 f010.go：%q", tt.Texts())
	}
	x, y := lb.X+lb.W/2, lb.Y+lb.H/2
	const tick = 17 * time.Millisecond

	// 三格，每格隔 40 ms（比滑完一格的 160 ms 快）：每帧都在动、每帧不超过两行，最后正好三行
	var ys []float32
	for range 3 {
		tt.Wheel(x, y, 0, app.WheelLine)
		for range 5 {
			tt.Advance(tick)
			ys = append(ys, s.S.Y)
		}
	}
	for range 60 {
		tt.Advance(tick)
		ys = append(ys, s.S.Y)
	}
	prev := float32(0)
	for i, v := range ys {
		if d := v - prev; d > 2*rowH {
			t.Errorf("第 %d 帧一下走了 %.1f（行高 %.0f）", i, d, rowH)
		} else if i < 15 && d <= 0.01 {
			t.Errorf("第 %d 帧没动（连续几格之间停了）：%v", i, ys[:15])
		}
		prev = v
	}
	if got := ys[len(ys)-1]; abs32(got-3*rowH) > 0.5 {
		t.Errorf("三格走了 %.1f，要 %.0f（三行）", got, 3*rowH)
	}

	// 加速后的一大格：3 行走 3 行，再大也不超过 6 行
	before := s.S.Y
	tt.Wheel(x, y, 0, 3*app.WheelLine)
	tt.Advance(time.Second)
	if got, want := s.S.Y-before, 3*rowH; abs32(got-want) > 0.5 {
		t.Errorf("3 行的一格走了 %.1f，要 %.1f", got, want)
	}
	if app.WheelRows(100*app.WheelLine) != app.WheelMaxRows {
		t.Errorf("100 行的一格走 %.1f 行，要封顶 %d", app.WheelRows(100*app.WheelLine), app.WheelMaxRows)
	}

	// 滑的时候吸顶栈看的行位置（挪过的 TreeSpans）和这一帧行真画的地方一样（Find 给的是这一帧的布局）。
	// 顶上几行会被吸顶栈里同名的层盖住，只看往下的
	tt.Wheel(x, y, 0, 3*app.WheelLine)
	moved := 0
	for range 10 {
		tt.Advance(tick)
		rows := a.TreeRows()
		for i, b := range a.TreeSpans {
			if rows[i].Root || i%7 != 0 || b.Y < lb.Y+4*rowH {
				continue
			}
			got, ok := tt.Find(rows[i].Name)
			if !ok || got.H <= 0 {
				continue
			}
			if d := got.Y - b.Y; abs32(d) > 0.5 {
				t.Fatalf("第 %d 行（%s）：吸顶栈看的在 %.1f，这一帧画在 %.1f（晚了一帧）", i, rows[i].Name, b.Y, got.Y)
			}
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("滑的时候一行也没量到")
	}

	// ListState 的 ScrollTo 在 TrackScroll 下照常（定位当前文件、吸顶栈点击跳转都走 ListState）；
	// 到底了再往下滚不动，反过来一格，下一帧就往上走
	a.FileList.ScrollTo(len(a.TreeRows())-1, wui.End)
	for range 4 {
		tt.Frame()
	}
	tt.Wheel(x, y, 0, app.WheelLine)
	tt.Advance(time.Second)
	end := s.S.Y
	if abs32(end-s.S.MaxY) > 0.5 {
		t.Fatalf("没滚到底：%.1f / %.1f", end, s.S.MaxY)
	}
	tt.Wheel(x, y, 0, app.WheelLine)
	tt.Wheel(x, y, 0, -app.WheelLine)
	tt.Advance(tick)
	if s.S.Y >= end-0.01 {
		t.Errorf("到底后反向一格，下一帧没动：%.1f", s.S.Y)
	}

	// 触控板（precise）的照旧交给 mygo：跟手、不滑
	tt.Advance(time.Second)
	before = s.S.Y
	tt.Scroll(x, y, 0, -10)
	tt.Frame()
	if abs32(s.S.Y-(before-10)) > 0.5 {
		t.Errorf("触控板滚 10 走了 %.1f", before-s.S.Y)
	}
}

// wheelTree 是四个仓库的文件树（仓库行比别的行高，列表估的行高和量到的不一样），报滚动状态和滚轮的落点。
func wheelTree(t *testing.T) (*app.App, *wui.Tester, *app.SideScroll, float32, float32) {
	t.Helper()
	var roots []string
	for k := range 4 {
		r := testutil.NewRepo(t, fmt.Sprintf("r%d", k))
		for i := range 60 {
			r.Write(fmt.Sprintf("d%d/f%03d.go", i%3, i), "x\n")
		}
		r.Commit("init")
		roots = append(roots, r.Root)
	}
	a, tt, wait := newUI(t, testutil.Workspace(t, roots...), "")
	a.ShowSidebar(workspace.ModeFiles)
	for _, r := range a.Repos {
		if !r.Expanded {
			a.ToggleRepo(r)
		}
		a.EnsureFiles(r)
	}
	wait("文件列表", func() bool {
		for _, r := range a.Repos {
			if !r.Files.Loaded {
				return false
			}
		}
		return true
	})
	for _, r := range a.Repos {
		for d := range 3 {
			r.Files.Reveal(fmt.Sprintf("d%d/f%03d.go", d, d))
		}
	}
	tt.Frame()
	tt.Frame()
	lb, ok := tt.Find(a.TreeRows()[6].Name)
	if !ok {
		t.Fatalf("找不到第 6 行：%q", tt.Texts())
	}
	return a, tt, a.SideScrolls[&a.FileList], lb.X + lb.W/2, lb.Y + lb.H/2
}

// 到底后往上滚：滚轮事件之后的第一帧来得多早都走得动（第一帧走不到 0.5 DIP 时列表把它拉回底上，这一格原来就丢了）。
func TestTreeWheelLeavesEnd(t *testing.T) {
	a, tt, s, x, y := wheelTree(t)
	a.FileList.ScrollTo(len(a.TreeRows())-1, wui.End)
	for range 4 {
		tt.Frame()
	}
	for _, first := range []time.Duration{0, 200 * time.Microsecond, time.Millisecond, 3 * time.Millisecond} {
		a.FileList.ScrollToEnd()
		for range 4 {
			tt.Frame()
		}
		end := s.S.Y
		if abs32(end-s.S.MaxY) > 0.5 {
			t.Fatalf("没到底：%.1f / %.1f", end, s.S.MaxY)
		}
		tt.Wheel(x, y, 0, -app.WheelLine)
		tt.Advance(first)
		tt.Frame()
		tt.Advance(time.Second)
		if got := end - s.S.Y; abs32(got-a.TreeRowH) > 1 {
			t.Errorf("第一帧隔 %v：到底后往上一格走了 %.2f，要一行 %.0f", first, got, a.TreeRowH)
		}
	}
}

// 跳到底（上面的行还没量过）往上滚：列表量到新行时修正偏移，滑动不断，一格走满一行。
func TestTreeWheelFullRowWhileMeasuring(t *testing.T) {
	a, tt, s, x, y := wheelTree(t)
	a.FileList.ScrollTo(len(a.TreeRows())-1, wui.End)
	for range 4 {
		tt.Frame()
	}
	const tick = 17 * time.Millisecond
	start := s.S.Y
	for i := range 100 {
		before := s.S.Y
		tt.Wheel(x, y, 0, -app.WheelLine)
		tt.Advance(3 * time.Millisecond)
		tt.Frame()
		for range 15 {
			tt.Advance(tick)
		}
		// 量到仓库行时偏移还会修正几 DIP，看的是没有哪格只走了一点点
		if got := before - s.S.Y; got < a.TreeRowH/2 {
			t.Fatalf("第 %d 格只走了 %.2f（%.1f -> %.1f）", i, got, before, s.S.Y)
		}
	}
	if s.S.Y >= start-90*a.TreeRowH {
		t.Fatalf("100 格只走了 %.1f", start-s.S.Y)
	}
}

// 滑的时候列表自己滚到一行（差一行以上）：滑动让给它。
func TestTreeWheelYieldsToScrollTo(t *testing.T) {
	a, tt, s, x, y := wheelTree(t)
	tt.Wheel(x, y, 0, 3*app.WheelLine)
	tt.Advance(17 * time.Millisecond)
	if !s.Gliding() {
		t.Fatal("应该在滑")
	}
	a.FileList.ScrollTo(100, wui.Start)
	tt.Advance(time.Second)
	if first, _ := a.FileList.Visible(); first < 99 || first > 101 {
		t.Fatalf("滚到第 100 行后最上面是第 %d 行", first)
	}
}
