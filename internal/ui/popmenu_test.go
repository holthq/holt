package ui

import (
	"fmt"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/testutil"
)

// 右键菜单有两种：系统菜单（ContextMenu，Tester 的 Menu / ChooseMenuItem / CloseMenu 管）和 mygo 画在窗口里的
// 弹出菜单（PopoverMenu，文件树用它，见 sidebar.go treeMenu 上面的说明）。Tester 不认后一种，这里从无障碍树里找：
// 菜单是 RoleMenu，项是 RoleMenuItem / 复选 / 单选。下面几个函数两种都认，测试里统一用它们。

// mygo internal/platform 里 AccessRole / AccessStates 的编号（a11y_test.go 那份接着往下数）。
const (
	roleMenu          = 38
	roleMenuItem      = 40
	roleMenuItemCheck = 41
	roleMenuItemRadio = 42
	accessDisabled    = 1 << 1
)

// popPanel 是画出来的一层菜单：面板和里面的项（按顺序，不含分隔线）。
type popPanel struct {
	Panel accNode
	Items []accNode
}

// popMenus 返回画在窗口里的菜单，一层一层（子菜单在后）；没开着就是空的。
func popMenus(t *testing.T, tt *wui.Tester) []popPanel {
	t.Helper()
	nodes := accessTree(t, tt)
	var out []popPanel
	at := map[int]int{} // 菜单节点 → out 里的下标
	for i, n := range nodes {
		if n.Role == roleMenu {
			at[i] = len(out)
			out = append(out, popPanel{Panel: n})
		}
	}
	for _, n := range nodes {
		if n.Role != roleMenuItem && n.Role != roleMenuItemCheck && n.Role != roleMenuItemRadio {
			continue
		}
		for p := n.Parent; p >= 0; p = nodes[p].Parent {
			if k, ok := at[p]; ok {
				out[k].Items = append(out[k].Items, n)
				break
			}
		}
	}
	return out
}

// shownMenu 是现在显示的右键菜单的项：系统菜单照 tt.Menu（分隔线是 "-"），画出来的是最上面一层的项（没有分隔线）。
func shownMenu(t *testing.T, tt *wui.Tester) []string {
	t.Helper()
	if m := tt.Menu(); m != nil {
		return m
	}
	ms := popMenus(t, tt)
	if len(ms) == 0 {
		return nil
	}
	out := []string{}
	for _, it := range ms[len(ms)-1].Items {
		out = append(out, it.Label)
	}
	return out
}

// chooseMenu 选现在显示的右键菜单里的一项，path 前面的是一层层子菜单，和 tt.ChooseMenuItem 一样；
// 灰着的项报 "disabled"。
func chooseMenu(t *testing.T, tt *wui.Tester, path ...string) error {
	t.Helper()
	if tt.Menu() != nil {
		return tt.ChooseMenuItem(path...)
	}
	if len(path) == 0 {
		return fmt.Errorf("没说选哪一项")
	}
	for i, label := range path {
		ms := popMenus(t, tt)
		if len(ms) == 0 {
			return fmt.Errorf("ui: no context menu is shown")
		}
		if len(ms) <= i {
			return fmt.Errorf("%q 的子菜单没开", strings.Join(path[:i], " > "))
		}
		var found *accNode
		for _, it := range ms[i].Items {
			if it.Label == label {
				found = &it
				break
			}
		}
		switch {
		case found == nil:
			return fmt.Errorf("ui: the context menu has no item %q", strings.Join(path[:i+1], " > "))
		case found.States&accessDisabled != 0:
			return fmt.Errorf("ui: the item %q is disabled", strings.Join(path[:i+1], " > "))
		}
		x, y := float32(found.X+found.W/2), float32(found.Y+found.H/2)
		tt.Move(x, y)
		tt.ClickAt(x, y)
		tt.Frame()
	}
	return nil
}

// closeMenu 不选、关掉现在显示的右键菜单（画出来的按 Esc，一层层关）。
func closeMenu(t *testing.T, tt *wui.Tester) {
	t.Helper()
	if tt.Menu() != nil {
		tt.CloseMenu()
		return
	}
	for range 5 {
		if len(popMenus(t, tt)) == 0 {
			return
		}
		tt.Key(0, wui.KeyEscape)
		tt.Frame()
	}
	t.Fatal("右键菜单按 Esc 关不掉")
}

// issue #6：窗口底下右键文件树，菜单整份显示在窗口里、往上开，不折叠、不滚；三个仓库，点的是最后一个仓库底下的文件。
// 先在高窗口里右键同一个文件量出整份菜单，再把窗口压矮、让那个文件落到最底下一行再右键，两次的项要一样多、一样高。
func TestTreeMenuFitsAtBottom(t *testing.T) {
	alpha, beta, gamma, _ := testutil.ThreeRepos(t)
	for i := range 30 {
		gamma.Write(fmt.Sprintf("g%02d.txt", i), "g\n")
	}
	gamma.Commit("多些文件")
	k := newTreeKit(t, alpha.Root, beta.Root, gamma.Root)
	tt := k.tt
	const name = "g29.txt"
	open := func(h int) (popPanel, float32) {
		t.Helper()
		tt.SetSize(1000, h)
		tt.Frame()
		k.a.Exec("file.closeAll")
		tt.Frame()
		k.a.FileList.ScrollToEnd()
		tt.Frame()
		tt.Frame()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("窗口高 %d：找不到 %s", h, name)
		}
		x, y := b.X+b.W/2, b.Y+b.H/2
		tt.RightClickAt(x, y)
		tt.Frame()
		ms := popMenus(t, tt)
		if len(ms) != 1 {
			t.Fatalf("窗口高 %d：右键 %s 开了 %d 层菜单（系统菜单 %q）", h, name, len(ms), tt.Menu())
		}
		return ms[0], y
	}
	full, _ := open(1800)
	closeMenu(t, tt)
	if len(full.Items) < 10 {
		t.Fatalf("高窗口里 %s 的菜单只有 %d 项", name, len(full.Items))
	}

	const h = 700
	m, y := open(h)
	defer closeMenu(t, tt)
	p := m.Panel
	t.Logf("点在 y=%.0f；菜单 %.0f..%.0f、%d 项（整份高 %.0f）；窗口高 %d", y, p.Y, p.Y+p.H, len(m.Items), full.Panel.H, h)
	if room := h - float64(y); room >= full.Panel.H {
		t.Fatalf("%s 下面还放得下整份菜单（y=%.0f，窗口高 %d，菜单高 %.0f），测不到", name, y, h, full.Panel.H)
	}
	if p.Y < 0 || p.Y+p.H > h+0.5 {
		t.Fatalf("菜单出了窗口：%.0f..%.0f，窗口高 %d", p.Y, p.Y+p.H, h)
	}
	if float32(p.Y) >= y {
		t.Fatalf("下面放不下时菜单该往上开：菜单顶 %.0f，点在 %.0f", p.Y, y)
	}
	if len(m.Items) != len(full.Items) || p.H < full.Panel.H-0.5 {
		t.Fatalf("菜单折起来了：%d 项、高 %.0f，整份是 %d 项、高 %.0f", len(m.Items), p.H, len(full.Items), full.Panel.H)
	}
	for _, it := range m.Items {
		if it.Y < p.Y-0.5 || it.Y+it.H > p.Y+p.H+0.5 || it.H < full.Items[0].H-0.5 {
			t.Fatalf("菜单项 %q 没整个露出来：%.0f..%.0f，菜单 %.0f..%.0f", it.Label, it.Y, it.Y+it.H, p.Y, p.Y+p.H)
		}
	}
}
