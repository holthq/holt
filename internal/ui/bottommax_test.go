package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 最大化底栏（⇧⌘J）：底栏占掉编辑区那块（终端在下面是高度、在右边是宽度），主区和拖条不画；
// 标题行的按钮、双击空白处也能切；打开文件自动还原。
func TestBottomMaximize(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenFile(r, "a.txt", true)
	wait("a.txt", func() bool { return !a.CurrentDoc().Loading })
	tt.Frame()
	find := func(label string) wui.Rect {
		t.Helper()
		rc, ok := tt.Find(label)
		if !ok {
			t.Fatalf("找不到 %q", label)
		}
		return rc
	}
	maxTip, restoreTip := app.Tip("最大化底栏", "view.maximizeBottom"), app.Tip("还原底栏", "view.maximizeBottom")
	tab, out := find("标签 a.txt"), find("输出")
	h := a.St.BottomH

	tt.Key(wui.Cmd|wui.Shift, wui.KeyJ)
	tt.Frame()
	if !a.BottomMaxed() || a.St.BottomH != h {
		t.Fatalf("⇧⌘J 后应最大化、BottomH 不变: max=%v H=%v→%v", a.BottomMaxed(), h, a.St.BottomH)
	}
	for _, gone := range []string{"标签 a.txt", "调整底栏高度", maxTip} {
		if _, ok := tt.Find(gone); ok {
			t.Errorf("最大化后不该有 %q", gone)
		}
	}
	// 底栏的标题行顶到原来编辑区的顶上：底栏高 ≈ 原来编辑器 + 底栏
	if got := find("输出"); got.Y > tab.Y+16 || got.Y < tab.Y-30 || got.Y >= out.Y {
		t.Errorf("最大化后底栏标题行在 y=%.0f，原来编辑区顶 y=%.0f、底栏标题行 y=%.0f", got.Y, tab.Y, out.Y)
	}
	mustClick(t, tt, restoreTip)
	if a.BottomMaxed() || find("输出").Y != out.Y {
		t.Fatalf("点还原后应回到原来的高度")
	}

	// 双击标题行的空白处切换
	probs := find(problemsTabLabel(a))
	dbl := func() {
		tt.Advance(time.Second)
		x, y := probs.X+probs.W+60, probs.Y+probs.H/2
		tt.ClickAt(x, y)
		tt.ClickAt(x, y)
		tt.Frame()
	}
	dbl()
	if !a.BottomMaxed() {
		t.Fatal("双击标题行空白处应最大化")
	}
	// 打开文件（快速打开、文件树、搜索结果都走 SelectDoc）自动还原
	a.OpenFile(r, "README.md", true)
	tt.Frame()
	if a.BottomMaxed() {
		t.Fatal("打开文件后应还原")
	}

	// 关底栏取消最大化，下次打开是普通高度
	a.Exec("view.maximizeBottom")
	tt.Key(wui.Cmd, wui.KeyJ)
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyJ)
	tt.Frame()
	if a.BottomMaxed() || !a.St.BottomBar {
		t.Fatal("关掉再打开底栏应是普通高度")
	}
	// 底栏关着时执行：打开并直接最大化
	tt.Key(wui.Cmd, wui.KeyJ)
	tt.Frame()
	tt.Key(wui.Cmd|wui.Shift, wui.KeyJ)
	tt.Frame()
	if !a.St.BottomBar || !a.BottomMaxed() {
		t.Fatal("底栏关着时 ⇧⌘J 应打开并最大化")
	}
	a.Exec("view.maximizeBottom")

	// 终端在右边：最大化后底栏占满侧栏和右栏之间
	a.St.Settings.TermDock = "right"
	tt.Frame()
	tt.Frame()
	tab, out = find("标签 README.md"), find("输出")
	w := a.St.TermW
	a.Exec("view.maximizeBottom")
	tt.Frame()
	for _, gone := range []string{"标签 README.md", "调整终端宽度"} {
		if _, ok := tt.Find(gone); ok {
			t.Errorf("终端在右边最大化后不该有 %q", gone)
		}
	}
	if got := find("输出"); got.X >= out.X-200 || a.St.TermW != w {
		t.Errorf("终端在右边最大化后底栏应往左占掉主区: 输出 x=%.0f→%.0f（主区左边 %.0f），TermW %v→%v", out.X, got.X, tab.X, w, a.St.TermW)
	}

	// 截图：浅色 / 深色各一张
	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" {
		a.St.Settings.TermDock = ""
		tt.Frame()
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			name := "bottom-max-light.png"
			if dark {
				name = "bottom-max-dark.png"
			}
			savePNG(t, filepath.Join(dir, name), material(tt.Image(), dark))
		}
	}
}
