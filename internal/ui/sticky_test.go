package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/testutil"
)

// stickyGo 是一个长 Go 文件：Long 从第 3 行（下标 2）到 205 行，后面跟一个 Other。
func stickyGo() string {
	var b strings.Builder
	b.WriteString("package main\n\nfunc Long() {\n")
	for range 200 {
		b.WriteString("\tprintln(1)\n")
	}
	b.WriteString("}\n\nfunc Other() {\n\tprintln(2)\n}\n")
	return b.String()
}

// 粘性滚动：滚到函数中间，编辑器顶上吸着一行，字是函数头；点它光标回到函数头、那一行滚到顶（就不吸了）；
// 查找条开着时叠层不盖它；设置关掉就没有；两层的点里层，它滚到外层下面。
func TestStickyOverlay(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "long.go"), []byte(stickyGo()), 0o644)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	d := a.OpenFile(r, "long.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	tt.Frame()
	tt.Frame()
	st := &d.St
	// 吸着的行数：每行有标签和正文两处 "func Long() {"（编辑器自己的文字不进 Texts）
	head := func() int {
		n := 0
		for _, s := range tt.Texts() {
			if s == "func Long() {" {
				n++
			}
		}
		return n / 2
	}
	if head() != 0 {
		t.Fatalf("没滚动就吸了：%q", tt.Texts())
	}

	st.SetScrollY(st.LineTop(50))
	tt.Frame()
	tt.Frame()
	if head() != 1 {
		t.Fatalf("滚到函数中间应吸一行 func Long() {：%q", tt.Texts())
	}
	if slices.Contains(tt.Texts(), "func Other() {") {
		t.Error("不该吸 Other")
	}
	accessCheck(t, tt, "粘性滚动")

	// 查找条：开着时叠层停在它左边，点得到它的关闭按钮
	a.FocusDoc = true
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyF)
	tt.Frame()
	tt.Type("println")
	tt.Frame()
	tt.Frame()
	if n, _ := st.FindMatches(); n == 0 {
		t.Fatal("查找条没开")
	}
	st.SetScrollY(st.LineTop(50))
	tt.Frame()
	tt.Frame()
	if head() != 1 {
		t.Fatalf("查找条开着时吸的行：%q", tt.Texts())
	}
	mustClick(t, tt, "✕")
	tt.Frame()
	if n, _ := st.FindMatches(); n != 0 {
		t.Fatal("叠层盖住了查找条：点不到关闭")
	}

	// 点吸着的那一行：光标到函数头，那一行滚到顶
	st.SetScrollY(st.LineTop(50))
	tt.Frame()
	tt.Frame()
	mustClick(t, tt, "func Long() {")
	tt.Frame()
	tt.Frame()
	if p := st.CursorPos(); p.Line != 2 || p.Col != 0 {
		t.Errorf("点了光标在 %+v，应在第 3 行行首", p)
	}
	if y := st.ScrollY(); y != st.LineTop(2) {
		t.Errorf("滚动 %v，应把第 3 行滚到顶（%v）", y, st.LineTop(2))
	}
	if head() != 0 {
		t.Errorf("函数头在顶上了还吸着：%q", tt.Texts())
	}

	// 滚轮落在叠层上照样滚编辑器
	st.SetScrollY(st.LineTop(50))
	tt.Frame()
	tt.Frame()
	if rc, ok := tt.Find("func Long() {"); ok {
		before := st.ScrollY()
		tt.Wheel(rc.X+rc.W/2, rc.Y+rc.H/2, 0, 60)
		tt.Frame()
		if st.ScrollY() == before {
			t.Error("叠层吃了滚轮")
		}
	}

	// 设置关掉：没有叠层
	a.ToggleStickyScroll()
	tt.Frame()
	tt.Frame()
	if head() != 0 {
		t.Errorf("关了还吸着：%q", tt.Texts())
	}
	if a.Exec("view.stickyScroll"); a.St.Settings.NoStickyScroll {
		t.Error("命令没把它开回来")
	}

	// 两层：类 › 方法；点里层那一行，它滚到外层那一行下面（看得见），只剩外层吸着
	var py strings.Builder
	py.WriteString("class Shop:\n")
	for range 30 {
		py.WriteString("    x = 1\n")
	}
	py.WriteString("    def checkout(self):\n")
	for range 80 {
		py.WriteString("        y = 2\n")
	}
	os.WriteFile(filepath.Join(root, "shop.py"), []byte(py.String()), 0o644)
	p := a.OpenFile(r, "shop.py", true)
	wait("读好 shop.py", func() bool { return p.Buf != nil && !p.Loading })
	tt.Frame()
	tt.Frame()
	ps := &p.St
	ps.SetScrollY(ps.LineTop(70))
	tt.Frame()
	tt.Frame()
	outer, ok1 := tt.Find("class Shop:")
	inner, ok2 := tt.Find("def checkout(self):")
	if !ok1 || !ok2 || inner.Y <= outer.Y {
		t.Fatalf("应吸两行 class Shop: 和 def checkout：%v %v %q", ok1, ok2, tt.Texts())
	}
	mustClick(t, tt, "def checkout(self):")
	tt.Frame()
	tt.Frame()
	if c := ps.CursorPos(); c.Line != 31 {
		t.Errorf("点了光标在 %+v，应在第 32 行", c)
	}
	if y, want := ps.ScrollY(), ps.LineTop(30); y != want {
		t.Errorf("滚动 %v，应把第 32 行滚到吸着的 class 下面（%v）", y, want)
	}
	if _, ok := tt.Find("def checkout(self):"); ok {
		t.Errorf("方法头露出来了还吸着：%q", tt.Texts())
	}
	if _, ok := tt.Find("class Shop:"); !ok {
		t.Errorf("外层的 class 不该掉：%q", tt.Texts())
	}
}
