package ui

import (
	"os"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// issue #48：文件树的右键菜单是 Grove 自己画的。按住拖、延迟、三角这些在 app/popmenu_test.go 里直接喂事件测；
// 这里在真界面上（三个仓库）测接线：右键开、描边、点外面关掉且不落到底下的行、灰项悬停出原因、子菜单停一会儿才开、
// Esc / 失去焦点 / 滚动文件树关掉。设了 WORKBENCH_SHOT 时顺便截图（浅色、深色）。
func TestTreeGroveMenu(t *testing.T) {
	alpha, beta, gamma, _ := testutil.ThreeRepos(t)
	gamma.Write("menu.txt", "m\n")
	gamma.Commit("加 menu.txt")
	k := newTreeKit(t, alpha.Root, beta.Root, gamma.Root)
	tt, a := k.tt, k.a
	shot := os.Getenv("WORKBENCH_SHOT")

	open := func(name string) {
		t.Helper()
		closeMenu(t, tt)
		if err := tt.RightClick(name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if ms := popMenus(t, tt); len(ms) != 1 {
			t.Fatalf("右键 %s 开了 %d 层画出来的菜单（系统菜单 %q）", name, len(ms), tt.Menu())
		}
	}
	item := func(lv int, label string) accNode {
		t.Helper()
		ms := popMenus(t, tt)
		if len(ms) <= lv {
			t.Fatalf("第 %d 层没开", lv)
		}
		for _, it := range ms[lv].Items {
			if it.Label == label {
				return it
			}
		}
		t.Fatalf("第 %d 层没有 %q：%q", lv, label, shownMenu(t, tt))
		return accNode{}
	}

	// 右键开、对着的那一行有描边（Owner 是那一行），选中不变
	before := a.FileSel
	open("menu.txt")
	if !menuOnRow(a, rowKey(t, k, "menu.txt")) {
		t.Fatalf("菜单该对着 menu.txt：%+v", a.TreeMenu.Owner)
	}
	if a.FileSel != before {
		t.Fatal("右键不该改选中")
	}
	if shot != "" {
		writeShot(t, tt, shot, "tree-menu.png")
	}

	// 灰项停 0.5 s 出原因
	d := item(0, "查看 diff")
	if d.States&accessDisabled == 0 {
		t.Fatal("没改动的文件，查看 diff 该灰着")
	}
	tt.Move(float32(d.X+d.W/2), float32(d.Y+d.H/2))
	tt.Frame()
	if tt.HasText("没有改动") {
		t.Fatal("刚指上就出了原因")
	}
	tt.Advance(app.MenuTipDelay + 50*time.Millisecond)
	tt.Frame()
	if !tt.HasText("没有改动") {
		t.Fatal("停够了该出灰项的原因")
	}
	if shot != "" {
		writeShot(t, tt, shot, "tree-menu-why.png")
		tt.SetDark(true)
		writeShot(t, tt, shot, "tree-menu-why-dark.png")
		tt.SetDark(false)
		tt.Frame()
	}

	// 点外面：关菜单，这一下不落到底下的行上（不选中、不打开）
	panel := popMenus(t, tt)[0].Panel
	b, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("找不到 alpha")
	}
	x, y := b.X+b.W/2, b.Y+b.H/2
	if float64(y) >= panel.Y && float64(y) < panel.Y+panel.H && float64(x) >= panel.X && float64(x) < panel.X+panel.W {
		t.Fatal("测不到：alpha 在菜单底下")
	}
	open1 := a.Repos[0].Expanded
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Frame()
	if a.TreeMenu.IsOpen() || len(popMenus(t, tt)) != 0 {
		t.Fatal("点外面该关菜单")
	}
	if a.FileSel != before || a.Repos[0].Expanded != open1 || len(a.Docs) != 0 {
		t.Fatalf("点外面的那一下落到了底下：选中 %d→%d，展开 %v→%v，%d 个标签", before, a.FileSel, open1, a.Repos[0].Expanded, len(a.Docs))
	}

	// 子菜单：停一会儿才开
	open("gamma")
	r := item(0, "远程")
	tt.Move(float32(r.X+r.W/2), float32(r.Y+r.H/2))
	tt.Frame()
	if len(popMenus(t, tt)) != 1 {
		t.Fatal("刚指上子菜单就开了")
	}
	tt.Advance(app.MenuSubDelay + 50*time.Millisecond)
	tt.Frame()
	if len(popMenus(t, tt)) != 2 {
		t.Fatal("停够了子菜单该开")
	}
	if shot != "" {
		writeShot(t, tt, shot, "tree-menu-sub.png")
		tt.SetDark(true)
		writeShot(t, tt, shot, "tree-menu-sub-dark.png")
		tt.SetDark(false)
		tt.Frame()
	}

	// Esc
	open("menu.txt")
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if a.TreeMenu.IsOpen() {
		t.Fatal("Esc 该关")
	}
	// 失去焦点
	open("menu.txt")
	tt.Frame()
	tt.Frame()
	tt.SetFocused(false)
	tt.Frame()
	tt.Frame()
	if a.TreeMenu.IsOpen() {
		t.Fatal("窗口失去焦点该关")
	}
	tt.SetFocused(true)
	tt.Frame()
	// 滚文件树
	open("menu.txt")
	tt.Scroll(x, y, 0, 30)
	tt.Frame()
	if a.TreeMenu.IsOpen() {
		t.Fatal("滚文件树该关")
	}
	// 键盘：↓ 回车选第一项（打开）
	open("menu.txt")
	tt.Key(0, wui.KeyDown)
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if dd := a.CurrentDoc(); dd == nil || dd.Rel != "menu.txt" {
		t.Fatalf("↓ 回车该选“打开”：%v", dd)
	}
}
