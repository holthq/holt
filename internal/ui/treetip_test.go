package ui

import (
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
)

// 文件树行的悬停提示（#20）：停 0.6 s 还没有（mygo 全局的延迟），停满 1 s 才出，画在行的下方、不盖上一行；
// 挪到下一行重新等（没有"热身"立刻弹）；点了那行就收掉、指针不离开不再出；名字被省略号截断时提示里有全名。
// 三个仓库都有这些行，提示按各自仓库算。
func TestTreeRowTip(t *testing.T) {
	long := "a-really-long-file-name-that-will-not-fit-in-the-sidebar.txt"
	var roots []string
	for _, name := range []string{"proj", "other", "third"} {
		r := testutil.NewRepo(t, name)
		for _, p := range []string{"b.txt", "c.txt", long} {
			r.Write(p, "hi\n")
		}
		r.Commit("init")
		roots = append(roots, r.Root)
	}
	k := newTreeKit(t, roots...)
	a, tt := k.a, k.tt
	a.Exec("file.closeAll")
	tt.Frame()
	other := k.repo("other")
	rowOf := func(path string) app.TreeRow {
		t.Helper()
		for _, row := range a.TreeRows() {
			if row.Repo == other && !row.Root && row.Path == path {
				return row
			}
		}
		t.Fatalf("树里没有 other/%s", path)
		return app.TreeRow{}
	}
	// 找 other 里那一行：三个仓库都有同名的文件，按 TreeSpans 里那一行的位置
	box := func(path string) wui.Rect {
		t.Helper()
		key := rowOf(path).Key()
		for i, b := range a.TreeSpans {
			if row, ok := a.TreeRowAt(i); ok && row.Key() == key {
				return b
			}
		}
		t.Fatalf("other/%s 没画出来", path)
		return wui.Rect{}
	}
	hover := func(path string) wui.Rect {
		t.Helper()
		b := box(path)
		tt.Move(b.X+b.W/2, b.Y+b.H/2)
		tt.Frame()
		tt.Frame()
		return b
	}
	tipOf := func(path string) string { return a.RowTip(rowOf(path)) }
	shown := func(path string) (wui.Rect, bool) { return tt.Find(tipOf(path)) }

	row := hover("b.txt")
	if tipOf("b.txt") == "" {
		t.Fatal("b.txt 没有提示")
	}
	tt.Advance(300 * time.Millisecond)
	if _, ok := shown("b.txt"); ok {
		t.Fatal("停了 0.3 s 树行的提示就出了，应该等 0.5 s")
	}
	tt.Advance(300 * time.Millisecond)
	tip, ok := shown("b.txt")
	if !ok {
		t.Fatalf("停了 0.6 s 树行的提示还没出：%q", tt.Texts())
	}
	if tip.Y < row.Y+row.H {
		t.Errorf("提示 %v 不在行 %v 的下方，盖住了上一行", tip, row)
	}

	// 挪到下一行：暖启动，提示直接换到新行、不再等
	crow := hover("c.txt")
	if tip, ok := shown("c.txt"); !ok {
		t.Error("从有提示的行挪到下一行，提示没直接出（暖启动）")
	} else if tip.Y < crow.Y+crow.H {
		t.Errorf("提示 %v 还在 b.txt 下面，没换到 c.txt（%v）", tip, crow)
	}

	// 点了就收掉，指针不动不再出
	b := box("c.txt")
	tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
	tt.Advance(2 * time.Second)
	if _, ok := shown("c.txt"); ok {
		t.Error("点了 c.txt，提示还在")
	}

	// 提示盖在下一行上：⌘点下一行照样加选上它（提示连里面的字都不接指针）。这段用 proj 的行（Find 先找到它的）
	k.click(0, "b.txt")
	at := func(name string) {
		t.Helper()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q", name)
		}
		tt.Move(b.X+b.W/2, b.Y+b.H/2)
		tt.Frame()
	}
	at("c.txt") // 点了提示收掉：挪开再回来
	at("b.txt")
	tt.Advance(1100 * time.Millisecond)
	if _, ok := shown("b.txt"); !ok {
		t.Fatal("挪开再回到 b.txt，停了 1.1 s 没出提示")
	}
	if err := clickRowWith(tt, wui.Cmd, "c.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.FileChosen.Len() != 2 {
		t.Errorf("b.txt 的提示盖着 c.txt 时 ⌘点 c.txt，没加选上：选中 %d 项", a.FileChosen.Len())
	}

	// 截断的名字：提示第一行是全名（名字本身也叫这个，所以比出提示前后多了几处）
	a.St.SidebarW = 200
	tt.Frame()
	tt.Frame()
	hover(long)
	n0 := countText(tt, long)
	tt.Advance(1100 * time.Millisecond)
	if _, ok := shown(long); !ok {
		t.Fatalf("截断的行没出提示：%q", tt.Texts())
	}
	if n := countText(tt, long); n != n0+1 {
		t.Errorf("截断的名字提示里没有全名（%q 出提示前 %d 处、后 %d 处）", long, n0, n)
	}
	// 没截断的名字提示里不重复名字
	hover("b.txt")
	n0 = countText(tt, "b.txt")
	tt.Advance(1100 * time.Millisecond)
	if _, ok := shown("b.txt"); !ok {
		t.Fatal("b.txt 没出提示")
	}
	if n := countText(tt, "b.txt"); n != n0 {
		t.Errorf("没截断的 b.txt 提示里也带了名字（%d → %d 处）", n0, n)
	}
}
