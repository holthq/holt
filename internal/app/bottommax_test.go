package app

import (
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// 最大化底栏：命令切 BottomMax、BottomH 不动；关底栏、打开文件、切组、进禅模式都还原；底栏关着时打开并最大化；不存进 state。
func TestBottomMax(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, _ := newTestApp(t, ws)
	r := a.Repos[0]
	a.St.BottomBar, a.St.BottomH = true, 333

	a.Exec("view.maximizeBottom")
	if !a.BottomMax || !a.BottomMaxed() || a.St.BottomH != 333 {
		t.Fatalf("执行后应最大化、BottomH 不变: max=%v H=%v", a.BottomMax, a.St.BottomH)
	}
	if c := Find("view.maximizeBottom"); c == nil || !c.Checked(a) {
		t.Error("菜单里应打勾")
	}
	a.Exec("view.maximizeBottom")
	if a.BottomMax || a.St.BottomH != 333 {
		t.Fatalf("再执行应还原: max=%v H=%v", a.BottomMax, a.St.BottomH)
	}

	// 关底栏清掉
	a.Exec("view.maximizeBottom")
	a.Exec("view.bottom")
	if a.BottomMax || a.St.BottomBar {
		t.Fatal("关底栏应取消最大化")
	}
	// 底栏关着时执行：打开并直接最大化
	a.Exec("view.maximizeBottom")
	if !a.St.BottomBar || !a.BottomMaxed() {
		t.Fatal("底栏关着时执行应打开并最大化")
	}

	// 打开文件后清掉
	a.OpenFile(r, "a.txt", true)
	if a.BottomMax {
		t.Fatal("打开文件后应还原")
	}
	// 切到已开着的标签也还原
	a.OpenFile(r, "README.md", true)
	a.Exec("view.maximizeBottom")
	a.SelectDoc(0)
	if a.BottomMax {
		t.Fatal("切标签后应还原")
	}
	// 问题面板关底栏也清掉
	a.Exec("view.maximizeBottom")
	a.BottomTab = BottomProblems
	a.ToggleProblems()
	if a.BottomMax || a.St.BottomBar {
		t.Fatal("⇧⌘M 关底栏应取消最大化")
	}
	// 禅模式进来时还原
	a.Exec("view.maximizeBottom")
	a.ToggleZen()
	if a.BottomMax {
		t.Fatal("进禅模式应还原")
	}
	a.ToggleZen()
	if a.St.BottomH != 333 {
		t.Errorf("BottomH 被改了: %v", a.St.BottomH)
	}
}
