package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
)

// 选中里画出来的空白符号（制表符的箭头）比 mygo 默认的淡，浅色、深色都是。
func TestWhitespaceFaint(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "tabs.go"), []byte("func a() {\n\tif x {\n\t\treturn\n\t}\n}\n"), 0o644)
	a, tt, wait := newUI(t, ws, "")
	d := a.OpenFile(a.Repos[0], "tabs.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	d.St.Select(0, d.Buf.Len())
	var cols []wui.Color
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame()
		if d.St.Theme == nil {
			t.Fatalf("dark=%v 没套 Holt 的代码颜色", dark)
		}
		// mygo 默认是 TextMuted 的 0.45（不透明的 TextMuted 时 A≈115），这里要淡到 0.3 以下
		got := d.St.Theme.Whitespace
		if got.A == 0 || float32(got.A) > 0.3*255 {
			t.Errorf("dark=%v 空白符号的颜色 %+v，应比默认的淡", dark, got)
		}
		cols = append(cols, got)
	}
	if cols[0] == cols[1] {
		t.Errorf("浅色、深色的空白符号一个颜色 %+v", cols[0])
	}
}

// occurColor 是 mygo 给"其他出现"的底色：只看深浅色。
func occurColor(dark bool) wui.Color {
	return editor.DefaultTheme(&wui.Theme{Dark: dark}).Occurrence
}

// 光标停在标识符上：停稳 OccurRest 后同名的其他出现（整词、区分大小写）被高亮，光标下那一处不标；
// 打字时清掉、不标；挪到空白处清掉；有选区时不管（交给 mygo）；同一个词的另一处换一下哪处不标。
func TestOccurrenceHighlight(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	src := "foo := 1\nbar(foo)\nfoofoo foo_x Foo\nfoo\n"
	os.WriteFile(filepath.Join(root, "occ.go"), []byte(src), 0o644)
	a, tt, wait := newUI(t, ws, "")
	d := a.OpenFile(a.Repos[0], "occ.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	st := &d.St
	starts := func() []int {
		var out []int
		for _, b := range st.TextBackgrounds {
			out = append(out, b.Start)
		}
		return out
	}
	eq := func(got, want []int) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	foo1, foo3 := strings.Index(src, "(foo")+1, strings.LastIndex(src, "foo")

	st.GoTo(editor.Pos{Line: 0, Col: 1})
	tt.Frame()
	if len(st.TextBackgrounds) != 0 {
		t.Fatalf("刚停到词上就标了: %v", starts())
	}
	tt.Advance(OccurRest)
	tt.Frame()
	if got := starts(); !eq(got, []int{foo1, foo3}) {
		t.Fatalf("停稳后标的 %v，应为 %v", got, []int{foo1, foo3})
	}
	if col := occurColor(false); st.TextBackgrounds[0].Color != col || st.TextBackgrounds[0].End != foo1+3 {
		t.Errorf("底色 %+v，应是 Occurrence %+v、整个词", st.TextBackgrounds[0], col)
	}

	// 词里挪一格：还是这个词，照旧标着
	st.GoTo(editor.Pos{Line: 0, Col: 3})
	tt.Frame()
	if got := starts(); !eq(got, []int{foo1, foo3}) {
		t.Errorf("同一个词里挪光标后 %v", got)
	}

	// 打字：马上清掉，停手后按新词重算（fooz 没有别处）
	st.Insert("z")
	tt.Frame()
	if len(st.TextBackgrounds) != 0 {
		t.Fatalf("打字时还标着: %v", starts())
	}
	tt.Advance(OccurRest)
	tt.Frame()
	if len(st.TextBackgrounds) != 0 {
		t.Fatalf("fooz 没有别处，却标了 %v", starts())
	}
	st.Run(editor.Undo)
	st.GoTo(editor.Pos{Line: 0, Col: 0})
	tt.Frame()
	tt.Advance(OccurRest)
	tt.Frame()
	if got := starts(); !eq(got, []int{foo1, foo3}) {
		t.Fatalf("撤销后停稳 %v", got)
	}

	// 挪到空白处：清掉
	st.GoTo(editor.Pos{Line: 0, Col: 4})
	tt.Frame()
	if len(st.TextBackgrounds) != 0 {
		t.Errorf("光标在空白处还标着 %v", starts())
	}

	// 同一个词的另一处：停稳后标的是另外两处
	st.GoTo(editor.Pos{Line: 3, Col: 1})
	tt.Frame()
	tt.Advance(OccurRest)
	tt.Frame()
	if got := starts(); !eq(got, []int{0, foo1}) {
		t.Fatalf("在最后一行的 foo 上标的 %v", got)
	}
	st.GoTo(editor.Pos{Line: 1, Col: 5})
	tt.Frame()
	if got := starts(); !eq(got, []int{0, foo3}) {
		t.Errorf("换到同一个词的另一处 %v", got)
	}

	// 有选区：不标
	st.Select(0, 3)
	tt.Frame()
	tt.Advance(OccurRest)
	tt.Frame()
	if len(st.TextBackgrounds) != 0 {
		t.Errorf("有选区时还标着 %v", starts())
	}

	// 深色：换成深色的 Occurrence
	st.GoTo(editor.Pos{Line: 0, Col: 1})
	tt.Frame()
	tt.Advance(OccurRest)
	tt.Frame()
	tt.SetDark(true)
	tt.Frame()
	if col := occurColor(true); len(st.TextBackgrounds) != 2 || st.TextBackgrounds[0].Color != col {
		t.Errorf("深色下的底色 %+v，应是 %+v", st.TextBackgrounds, col)
	}
}

// 大于 StickyQuick 的文件只标看得见的那几行，滚了重标。
func TestOccurrenceBigFile(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	line := "foo bar baz " + strings.Repeat("x", 60) + "\n"
	n := docs.StickyQuick/len(line) + 100
	os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat(line, n)), 0o644)
	a, tt, wait := newUI(t, ws, "")
	d := a.OpenFile(a.Repos[0], "big.txt", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	st := &d.St
	st.GoTo(editor.Pos{Line: 0, Col: 1})
	tt.Frame()
	tt.Advance(OccurRest)
	tt.Frame()
	first, last := st.VisibleLines()
	got := st.TextBackgrounds
	if len(got) == 0 || len(got) > last-first+1 || len(got) >= n-1 {
		t.Fatalf("大文件标了 %d 处，看得见的是第 %d–%d 行", len(got), first, last)
	}
	snap := d.Buf.Snapshot()
	for _, b := range got {
		if l := snap.LineOf(b.Start); l < first || l > last {
			t.Fatalf("标到了看不见的第 %d 行（看得见 %d–%d）", l, first, last)
		}
	}
	st.ScrollTo(n / 2)
	tt.Frame()
	tt.Frame()
	first, last = st.VisibleLines()
	if len(st.TextBackgrounds) == 0 || snap.LineOf(st.TextBackgrounds[0].Start) < first {
		t.Errorf("滚到第 %d 行后没重标：%d 处", first, len(st.TextBackgrounds))
	}
}
