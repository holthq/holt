package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
)

func groupTitles(a *app.App) [][]string {
	var out [][]string
	for _, g := range a.Groups() {
		ds, _ := a.GroupDocs(g)
		out = append(out, titles(ds))
	}
	return out
}

// 拖标签分屏：拖着到内容区右边缘，右半边铺一层强调色，松开分成左右两组；再拖回左边那组中间，右边那组收起。
// ⌘\ 把当前标签搬到右边新的一组、键盘跟过去（打字进的是它）；点左边那组的编辑器键盘回左边；
// 一个标签的组 ⌥⌘\ 只给提示；⌥⌘\ 往下分；关掉组里最后一个标签，那组收起、键盘到兄弟那组。
func TestEditorSplit(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	readme := a.OpenFile(r, "README.md", true)
	at := a.OpenFile(r, "a.txt", true)
	wait("读好", func() bool { return readme.Buf != nil && at.Buf != nil })
	tt.Frame()

	// 拖着时预览铺在右半边，松开才分
	f, _ := tt.Find("标签 a.txt")
	g, ok := tt.Find("编辑 a.txt")
	if !ok {
		t.Fatalf("找不到 a.txt 的编辑器；texts=%q", tt.Texts())
	}
	x0, y0 := f.X+f.W/2, f.Y+f.H/2
	x1, y1 := g.X+g.W*0.95, g.Y+g.H/2
	before := tt.Image().RGBAAt(int(x1), int(y1))
	tt.Press(x0, y0)
	for k := 1; k <= 4; k++ {
		s := float32(k) / 4
		tt.Move(x0+(x1-x0)*s, y0+(y1-y0)*s)
	}
	tt.Frame()
	if got := tt.Image().RGBAAt(int(x1), int(y1)); got == before || a.Split() {
		t.Fatalf("拖着时：右半边 %v（拖之前 %v），分屏 %v", got, before, a.Split())
	}
	tt.Release(x1, y1)
	tt.Frame()
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"a.txt"}) {
		t.Fatalf("拖到右边缘：%q", got)
	}
	left, ok1 := tt.Find("编辑 README.md")
	right, ok2 := tt.Find("编辑 a.txt")
	if !ok1 || !ok2 || left.X+left.W > right.X || a.CurrentDoc() != at {
		t.Fatalf("左右两个编辑器：%v %v，当前 %v", left, right, a.CurrentDoc().Title)
	}
	accessCheck(t, tt, "左右两组")

	// 拖回左边那组中间：一组
	dragTo(t, tt, "标签 a.txt", "编辑 README.md", 0.5)
	if a.Split() || !slices.Equal(titles(a.Docs), []string{"README.md", "a.txt"}) || a.CurrentDoc() != at {
		t.Fatalf("拖回中间：%q", groupTitles(a))
	}

	// ⌘\：a.txt 到右边，打字进的是它
	tt.Key(wui.Cmd, wui.KeyBackslash)
	tt.Frame()
	tt.Frame()
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[1], []string{"a.txt"}) {
		t.Fatalf("⌘\\：%q", got)
	}
	tt.Type("Z")
	tt.Frame()
	if !at.Modified() || readme.Modified() {
		t.Fatalf("⌘\\ 后打字：a.txt 改了 %v，README 改了 %v", at.Modified(), readme.Modified())
	}
	tt.Key(wui.Cmd, wui.KeyZ)
	tt.Frame()
	// 点左边的编辑器：键盘回左边
	mustClick(t, tt, "编辑 README.md")
	tt.Type("Q")
	tt.Frame()
	if a.CurrentDoc() != readme || !readme.Modified() || at.Modified() {
		t.Fatalf("点左边以后打字：当前 %v，README 改了 %v，a.txt 改了 %v", a.CurrentDoc().Title, readme.Modified(), at.Modified())
	}
	tt.Key(wui.Cmd, wui.KeyZ)
	tt.Frame()

	// 一个标签分不出来；开 u.txt 再 ⌥⌘\：左边上下两组
	tt.Key(wui.Cmd|wui.Alt, wui.KeyBackslash)
	tt.Frame()
	mustText(t, tt, "这一组只有一个标签，分不出新的一组")
	u := a.OpenFile(r, "u.txt", true)
	wait("u.txt 读好", func() bool { return u.Buf != nil })
	tt.Key(wui.Cmd|wui.Alt, wui.KeyBackslash)
	tt.Frame()
	tt.Frame()
	if got := groupTitles(a); len(got) != 3 || !slices.Equal(got[1], []string{"u.txt"}) || a.Ed.A.G != nil || a.Ed.A.Row {
		t.Fatalf("⌥⌘\\：%q", got)
	}
	top, _ := tt.Find("编辑 README.md")
	bottom, _ := tt.Find("编辑 u.txt")
	if top.Y+top.H > bottom.Y || a.CurrentDoc() != u {
		t.Fatalf("上下两个编辑器：%v %v", top, bottom)
	}
	accessCheck(t, tt, "三组")

	// ⌘W 关掉 u.txt：那组收起，键盘到 README 那组
	tt.Key(wui.Cmd, wui.KeyW)
	tt.Frame()
	tt.Frame()
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"README.md"}) || a.CurrentDoc() != readme {
		t.Fatalf("关了 u.txt：%q，当前 %v", got, a.CurrentDoc())
	}
}
