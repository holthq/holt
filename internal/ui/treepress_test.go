package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
)

// 文件树的鼠标手感（issue #46）：按下（还没松开）那一帧选中就挪过去、行画成"按下"的底色，打开文件等松开；
// 按下拖过几行是拖着选，松开不打开；右键的那一行画描边、原来的选中不动，菜单关了描边就没了。
func TestTreePressFeel(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	for _, p := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		repo.Write(p, p+"\n")
	}
	repo.Commit("init")
	k := newTreeKit(t, repo.Root)
	a, tt := k.a, k.tt
	k.click(0, "a.txt")
	a.Exec("file.closeAll")
	tt.Frame()
	tt.Advance(time.Second)

	center := func(name string) (float32, float32) {
		t.Helper()
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q", name)
		}
		return b.X + b.W/2, b.Y + b.H/2
	}
	x, y := center("b.txt")
	tt.Move(x, y)
	tt.Frame()
	hover := tt.Image().At(int(x)+40, int(y)) // 悬停、没选中
	tt.Press(x, y)
	tt.Frame()
	if k.sel() != "b.txt" {
		t.Fatalf("按下 b.txt 选中没挪过去：%s", k.sel())
	}
	if a.CurrentDoc() != nil {
		t.Fatal("按下就打开了文件，应该等松开")
	}
	if key, ok := a.TreePressed(); !ok || key.Path != "b.txt" {
		t.Fatalf("没记下按下的行：%+v %v", key, ok)
	}
	pressed := tt.Image().At(int(x)+40, int(y))
	tt.Release(x, y)
	tt.Frame()
	tt.Frame()
	if d := a.CurrentDoc(); d == nil || d.Rel != "b.txt" {
		t.Fatalf("松开没打开 b.txt：%+v", d)
	}
	released := tt.Image().At(int(x)+40, int(y))
	if pressed != released || pressed == hover {
		t.Errorf("按下就该是选中的底色，松开不再变（不然点一下闪一下）：悬停 %v 按下 %v 松开 %v", hover, pressed, released)
	}
	// 按在已经选中的行上：底色一点不变
	tt.Move(x, y)
	tt.Frame()
	tt.Press(x, y)
	tt.Frame()
	if again := tt.Image().At(int(x)+40, int(y)); again != released {
		t.Errorf("按在选中的行上底色变了：%v -> %v", released, again)
	}
	tt.Release(x, y)
	tt.Frame()
	tt.Frame()

	// 按下拖过几行：拖着选，松开不打开
	a.Exec("file.closeAll")
	tt.Frame()
	tt.Advance(time.Second)
	x, y = center("c.txt")
	tt.Move(x, y)
	tt.Press(x, y)
	tt.Frame()
	x2, y2 := center("d.txt")
	for i := 1; i <= 4; i++ {
		s := float32(i) / 4
		tt.Move(x+(x2-x)*s, y+(y2-y)*s)
	}
	tt.Frame()
	tt.Release(x2, y2)
	tt.Frame()
	if a.CurrentDoc() != nil {
		t.Fatalf("拖着选了几行，松开打开了 %q", a.CurrentDoc().Rel)
	}
	if n := a.FileChosen.Len(); n != 2 {
		t.Fatalf("拖着选应该选中 2 行：%d", n)
	}

	// 右键没选中的行：描边，选中不变；菜单关了描边没了
	tt.Advance(time.Second)
	x, y = center("a.txt")
	tt.Move(x, y)
	tt.Frame()
	before := tt.Image()
	if err := rightClickRow(tt, "a.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(shownMenu(t, tt)) == 0 {
		t.Fatal("右键没出菜单")
	}
	if n := a.FileChosen.Len(); n != 2 || a.FileChosen.Has(rowKey(t, k, "a.txt")) {
		t.Fatalf("右键没选中的行改了选中：%d 行", n)
	}
	// 描边画在行的上边上：开着菜单时那一点和右键前、菜单关了以后都不一样
	r := a.TreeSpans[rowIndex(t, k, "a.txt")]
	px, py := int(r.X+r.W/2), int(r.Y)
	open := tt.Image().At(px, py)
	if open == before.At(px, py) {
		t.Errorf("右键的那一行没画描边：%v", open)
	}
	closeMenu(t, tt)
	tt.Frame()
	if got := tt.Image().At(px, py); got == open {
		t.Errorf("菜单关了描边还在：%v", got)
	}

	// 截图：浅色 / 深色各一套，悬停（b.txt）、按下（b.txt，选中已挪过去）、右键描边加菜单（c.txt，选中还在 b.txt）
	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" {
		for _, dark := range []bool{false, true} {
			mode := map[bool]string{false: "light", true: "dark"}[dark]
			tt.SetDark(dark)
			k.click(0, "a.txt")
			a.Exec("file.closeAll")
			tt.Frame()
			tt.Advance(time.Second)
			x, y := center("b.txt")
			tt.Move(x, y)
			tt.Frame()
			tt.Frame()
			savePNG(t, filepath.Join(dir, "tree-hover-"+mode+".png"), material(tt.Image(), dark))
			tt.Press(x, y)
			tt.Frame()
			savePNG(t, filepath.Join(dir, "tree-press-"+mode+".png"), material(tt.Image(), dark))
			tt.Release(x, y)
			tt.Frame()
			a.Exec("file.closeAll")
			tt.Frame()
			if err := rightClickRow(tt, "c.txt"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Frame()
			savePNG(t, filepath.Join(dir, "tree-menu-"+mode+".png"), material(tt.Image(), dark))
			closeMenu(t, tt)
		}
	}
}

// rowKey 是文件树里路径为 rel 的那一行的键。
func rowKey(t *testing.T, k *treeKit, rel string) app.FileKey {
	t.Helper()
	for _, row := range k.a.TreeRows() {
		if !row.Root && row.Path == rel {
			return row.Key()
		}
	}
	t.Fatalf("树里没有 %s", rel)
	return app.FileKey{}
}

// rowIndex 是文件树里路径为 rel 的那一行是第几行。
func rowIndex(t *testing.T, k *treeKit, rel string) int {
	t.Helper()
	for i, row := range k.a.TreeRows() {
		if !row.Root && row.Path == rel {
			return i
		}
	}
	t.Fatalf("树里没有 %s", rel)
	return -1
}

// 连点同一个目录（issue #50）：每一下都切（开 / 关 / 开 / 关 / 开），不当成双击吞掉；按住很久再松开只切一次。
// 文件双击照旧固定打开（TestTreeClicks）。
func TestTreeRapidDirClicks(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("a.txt", "a\n")
	repo.Write("b.txt", "b\n")
	repo.Write("sub/x.txt", "x\n")
	repo.Commit("init")
	k := newTreeKit(t, repo.Root)
	a, tt := k.a, k.tt
	r := k.repo("proj")
	a.Exec("file.closeAll")
	tt.Frame()
	tt.Advance(time.Second)
	b, ok := tt.Find("sub")
	if !ok {
		t.Fatal("找不到 sub")
	}
	x, y := b.X+b.W/2, b.Y+b.H/2
	tt.Move(x, y)
	tt.Frame()
	click := func(hold, gap time.Duration) {
		tt.Press(x, y)
		tt.Frame()
		tt.Advance(hold)
		tt.Release(x, y)
		tt.Frame()
		tt.Frame()
		tt.Advance(gap)
	}
	want := false
	for i := 1; i <= 5; i++ {
		click(60*time.Millisecond, 180*time.Millisecond)
		want = !want
		if got := r.Files.Open["sub"]; got != want {
			t.Fatalf("连点第 %d 下：展开 %v，应该 %v", i, got, want)
		}
	}
	// 按住 700 ms（过了认双击的间隔）再松开：只切一次
	tt.Advance(time.Second)
	for i := range 2 {
		click(700*time.Millisecond, time.Second)
		want = !want
		if got := r.Files.Open["sub"]; got != want {
			t.Fatalf("按住 700 ms 松开（第 %d 次，选中着=%v）：展开 %v，应该 %v", i+1, i > 0, got, want)
		}
	}
	// 再换到没选中时按住
	k.click(0, "a.txt")
	b, _ = tt.Find("sub")
	x, y = b.X+b.W/2, b.Y+b.H/2
	tt.Move(x, y)
	tt.Frame()
	click(700*time.Millisecond, time.Second)
	want = !want
	if got := r.Files.Open["sub"]; got != want {
		t.Fatalf("按住没选中的目录 700 ms 松开：展开 %v，应该 %v", got, want)
	}
}

// 点文件不闪（issue #50）：小文件松开后的第一帧就有内容（不是 Loading、编辑区不空一帧），预览换文件也一样。
func TestTreeClickNoBlank(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("a.txt", "alpha\n")
	repo.Write("b.txt", "beta\n")
	repo.Commit("init")
	k := newTreeKit(t, repo.Root)
	a, tt := k.a, k.tt
	a.Exec("file.closeAll")
	tt.Frame()
	tt.Advance(time.Second)
	for _, name := range []string{"a.txt", "b.txt"} {
		b, ok := tt.Find(name)
		if !ok {
			t.Fatalf("找不到 %q", name)
		}
		x, y := b.X+b.W/2, b.Y+b.H/2
		tt.Move(x, y)
		tt.Press(x, y)
		tt.Frame()
		tt.Advance(60 * time.Millisecond)
		tt.Release(x, y)
		tt.Frame()
		d := a.CurrentDoc()
		if d == nil || d.Rel != name {
			t.Fatalf("松开没打开 %s：%+v", name, d)
		}
		if d.Loading || d.Buf == nil || d.Buf.String() == "" {
			t.Fatalf("%s 松开后的第一帧还在读（Loading=%v）", name, d.Loading)
		}
		tt.Advance(time.Second)
	}
}
