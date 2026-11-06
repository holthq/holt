package ui

import (
	"os"
	"slices"
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
		if err := rightClickRow(tt, name); err != nil {
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
		t.Fatalf("菜单该对着 menu.txt：%+v", a.Menu.Owner)
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
	if a.Menu.IsOpen() || len(popMenus(t, tt)) != 0 {
		t.Fatal("点外面该关菜单")
	}
	if a.FileSel != before || a.Repos[0].Expanded != open1 || len(a.Docs) != 0 {
		t.Fatalf("点外面的那一下落到了底下：选中 %d→%d，展开 %v→%v，%d 个标签", before, a.FileSel, open1, a.Repos[0].Expanded, len(a.Docs))
	}

	// 子菜单：停一会儿才开
	open("gamma")
	r := item(0, "更多")
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
	if a.Menu.IsOpen() {
		t.Fatal("Esc 该关")
	}
	// 失去焦点
	open("menu.txt")
	tt.Frame()
	tt.Frame()
	tt.SetFocused(false)
	tt.Frame()
	tt.Frame()
	if a.Menu.IsOpen() {
		t.Fatal("窗口失去焦点该关")
	}
	tt.SetFocused(true)
	tt.Frame()
	// 滚文件树
	open("menu.txt")
	tt.Scroll(x, y, 0, 30)
	tt.Frame()
	if a.Menu.IsOpen() {
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

// ⇧F10 / 菜单键：文件树有键盘时在光标那一行左边、紧挨着它下面打开它的菜单，第一项高亮，↓ 回车马上能用；
// Esc 关了键盘回到文件树。
func TestTreeGroveMenuByKey(t *testing.T) {
	alpha, beta, gamma, _ := testutil.ThreeRepos(t)
	k := newTreeKit(t, alpha.Root, beta.Root, gamma.Root)
	tt, a := k.tt, k.a

	for _, key := range []struct {
		mods wui.Modifiers
		key  wui.Key
	}{{wui.Shift, wui.KeyF10}, {0, wui.KeyContextMenu}} {
		a.FileSel = rowIndex(t, k, "beta.go")
		a.FocusTree = true
		tt.Frame()
		tt.Frame()
		if !a.TreeFocused {
			t.Fatal("文件树该有键盘")
		}
		tt.Key(key.mods, key.key)
		tt.Frame()
		if !a.Menu.IsOpen() || !menuOnRow(a, rowKey(t, k, "beta.go")) {
			t.Fatalf("%v：该开 beta.go 的菜单：open=%v owner=%+v", key, a.Menu.IsOpen(), a.Menu.Owner)
		}
		row := a.TreeSpans[a.FileSel]
		ms := popMenus(t, tt)
		if len(ms) != 1 {
			t.Fatalf("开了 %d 层", len(ms))
		}
		p := ms[0].Panel
		if abs(p.X-float64(max(row.X, app.MenuMargin))) > 1 || abs(p.Y-float64(row.Y+row.H)) > 1 { // 离窗口边至少 MenuMargin
			t.Fatalf("菜单该在那一行左边、紧挨着下面：面板 (%.0f, %.0f)，行 %+v", p.X, p.Y, row)
		}
		if h := a.Menu.Levels()[0].Hot; h != 0 {
			t.Fatalf("第一项该高亮：%d", h)
		}
		tt.Key(0, wui.KeyEscape)
		tt.Frame()
		tt.Frame()
		if a.Menu.IsOpen() {
			t.Fatal("Esc 该关")
		}
		if !a.TreeFocused {
			t.Fatal("键盘开的菜单关了，键盘该回到文件树")
		}
	}

	// 回车选第一项（打开）
	a.FileSel = rowIndex(t, k, "beta.go")
	tt.Frame()
	tt.Key(wui.Shift, wui.KeyF10)
	tt.Frame()
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if d := a.CurrentDoc(); d == nil || d.Rel != "beta.go" {
		t.Fatalf("⇧F10 回车该打开 beta.go：%v", d)
	}
}

// 菜单开着时在文件树别处右键：关掉这份、在右键的那一行（或空白处）、指针处开一份新的；右键多选外面的一行照旧：
// 那一行描边、选中不清。左键点外面只关、被吞掉（TestTreeGroveMenu 测了）。
func TestTreeGroveMenuRightClickElsewhere(t *testing.T) {
	alpha, beta, gamma, _ := testutil.ThreeRepos(t)
	k := newTreeKit(t, alpha.Root, beta.Root, gamma.Root)
	tt, a := k.tt, k.a

	clickRow(tt, "alpha.txt")
	tt.Frame()
	if err := clickRowWith(tt, wui.Cmd, "notes.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.FileChosen.Len() != 2 {
		t.Fatalf("该选着两行：%d", a.FileChosen.Len())
	}
	sel := a.FileSel
	if err := rightClickRow(tt, "notes.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !menuOnRow(a, rowKey(t, k, "notes.md")) {
		t.Fatalf("菜单该对着 notes.md：%+v", a.Menu.Owner)
	}

	// 右键选中外面的 beta.go：菜单换成它的，开在指针处
	b, ok := tt.Find("beta.go")
	if !ok {
		t.Fatal("找不到 beta.go")
	}
	x, y := b.X+2, b.Y+b.H/2 // 名字的左头：菜单开在 notes.md 名字的中间，盖不到这里
	if p := popMenus(t, tt)[0].Panel; float64(x) >= p.X && float64(x) < p.X+p.W && float64(y) >= p.Y && float64(y) < p.Y+p.H {
		t.Fatal("测不到：beta.go 在菜单底下")
	}
	tt.RightClickAt(x, y)
	tt.Frame()
	if !a.Menu.IsOpen() || !menuOnRow(a, rowKey(t, k, "beta.go")) {
		t.Fatalf("右键别的行该开它的菜单：open=%v owner=%+v", a.Menu.IsOpen(), a.Menu.Owner)
	}
	ms := popMenus(t, tt)
	if len(ms) != 1 {
		t.Fatalf("该只有一份菜单：%d 层", len(ms))
	}
	if p := ms[0].Panel; abs(p.X-float64(x)) > 1 && abs(p.X+p.W-float64(x)) > 1 {
		t.Fatalf("新菜单该开在指针处：面板 %+v，指针 (%.0f, %.0f)", p, x, y)
	}
	if a.FileChosen.Len() != 2 || a.FileSel != sel {
		t.Fatalf("右键多选外面的行不该改选中：%d 行，光标 %d→%d", a.FileChosen.Len(), sel, a.FileSel)
	}
	if len(a.Docs) > 1 {
		t.Fatalf("右键不该打开文件：%d 个标签", len(a.Docs))
	}

	// 右键行下面的空白处：空白处的菜单
	last := a.TreeSpans[len(a.TreeRows())-1]
	p := popMenus(t, tt)[0].Panel
	by := max(float64(last.Y+last.H+30), p.Y+p.H+20) // 别落在开着的菜单上
	tt.RightClickAt(last.X+20, float32(by))
	tt.Frame()
	if o, ok := a.Menu.Owner.(treeOwner); !a.Menu.IsOpen() || !ok || !o.Blank {
		t.Fatalf("右键空白处该开空白处的菜单：open=%v owner=%+v", a.Menu.IsOpen(), a.Menu.Owner)
	}
	if !slices.Contains(shownMenu(t, tt), "显示被忽略的文件") {
		t.Fatalf("空白处的菜单：%q", shownMenu(t, tt))
	}
	closeMenu(t, tt)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// 高亮项是淡填充（字色的 8%–10%，不超过 12%）、字色不变；叠在面板底上之后字和底至少 4.5:1，浅色深色都量。
func TestGroveMenuHotContrast(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th := wui.LightTheme()
		if dark {
			th = wui.DarkTheme()
		}
		bg, fg := gmenuHot(th)
		if fg != th.Text {
			t.Errorf("dark=%v：高亮项的字该不变色：%v", dark, fg)
		}
		if bg.A == 0 || bg.A > 31 {
			t.Errorf("dark=%v：高亮项该是淡填充：%v", dark, bg)
		}
		// 叠在面板底上之后，字照样够 4.5:1
		panel := gmenuBg(th)
		hot := panel.Mix(wui.RGBA(bg.R, bg.G, bg.B, 1), float32(bg.A)/255)
		if r := contrast(fg, hot); r < 4.5 {
			t.Errorf("dark=%v：高亮项 %v 上的字 %v 只有 %.2f:1", dark, hot, fg, r)
		}
	}
}
