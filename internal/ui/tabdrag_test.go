package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// dragTo 按住 from 这个元素的中间，分几步挪到 to 的中间（左边 frac 处）再松开，像手拖一样。
func dragTo(t *testing.T, tt *wui.Tester, from, to string, frac float32) {
	t.Helper()
	f, ok := tt.Find(from)
	g, ok2 := tt.Find(to)
	if !ok || !ok2 {
		t.Fatalf("找不到 %q 或 %q；texts=%q", from, to, tt.Texts())
	}
	x0, y0 := f.X+f.W/2, f.Y+f.H/2
	x1, y1 := g.X+g.W*frac, g.Y+g.H/2
	tt.Press(x0, y0)
	for k := 1; k <= 4; k++ {
		s := float32(k) / 4
		tt.Move(x0+(x1-x0)*s, y0+(y1-y0)*s)
	}
	tt.Release(x1, y1)
	tt.Frame()
}

func titles(ds []*docs.Doc) []string {
	var s []string
	for _, d := range ds {
		s = append(s, d.Title)
	}
	return s
}

// 拖编辑器的标签换位置：当前标签不变，顺序记进这个工作区的会话，重开还是这个顺序。
// 关闭已保存的（右键菜单、⌥⌘W）只关没改过的，预览标签也关。
func TestTabDrag(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	state := t.TempDir()
	a, tt, wait := newUI(t, ws, state)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenFile(r, "README.md", true)
	a.OpenFile(r, "a.txt", true)
	u := a.OpenFile(r, "u.txt", false) // 预览标签
	wait("三个标签读好", func() bool {
		return len(a.Docs) == 3 && !slices.ContainsFunc(a.Docs, func(d *docs.Doc) bool { return d.Buf == nil })
	})
	a.SelectDoc(1)
	tt.Frame()
	cur := a.CurrentDoc()

	// 拖着时不动；松开在 README 上：u.txt 到最前面
	dragTo(t, tt, "标签 u.txt", "标签 README.md", 0.3)
	if got := titles(a.Docs); !slices.Equal(got, []string{"u.txt", "README.md", "a.txt"}) {
		t.Fatalf("拖到最前面后：%q", got)
	}
	if a.CurrentDoc() != cur {
		t.Errorf("拖标签换了当前标签：%q", a.CurrentDoc().Title)
	}
	// 往右拖：README 到 a.txt 的位置
	dragTo(t, tt, "标签 README.md", "标签 a.txt", 0.7)
	if got := titles(a.Docs); !slices.Equal(got, []string{"u.txt", "a.txt", "README.md"}) {
		t.Fatalf("往右拖后：%q", got)
	}
	// 按下不怎么动是点：选中，不换位置
	f, _ := tt.Find("标签 u.txt")
	tt.Press(f.X+f.W/2, f.Y+f.H/2)
	tt.Move(f.X+f.W/2+1, f.Y+f.H/2)
	tt.Release(f.X+f.W/2+1, f.Y+f.H/2)
	tt.Frame()
	if a.CurrentDoc() != u || titles(a.Docs)[0] != "u.txt" {
		t.Errorf("点一下：当前 %q，顺序 %q", a.CurrentDoc().Title, titles(a.Docs))
	}

	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	var saved []string
	if s := st.Sessions[a.WsPath]; s != nil {
		for _, tb := range s.Tabs {
			saved = append(saved, tb.Path[len(r.Root)+1:])
		}
	}
	if !slices.Equal(saved, []string{"u.txt", "a.txt", "README.md"}) {
		t.Errorf("会话里的标签顺序：%q", saved)
	}

	// 改一下 a.txt，右键"关闭已保存的"：只留 a.txt
	mustClick(t, tt, "标签 a.txt")
	mustClick(t, tt, "编辑 a.txt")
	ad := a.CurrentDoc()
	ad.St.Select(0, 0)
	tt.Type("y")
	tt.Frame()
	if !ad.Modified() {
		t.Fatal("输入后应是没保存的")
	}
	mustRightClick(t, tt, "标签 a.txt", "关闭已保存的")
	if got := titles(a.Docs); !slices.Equal(got, []string{"a.txt"}) || !tt.HasText("关了 2 个已保存的标签，1 个没保存的留着") {
		t.Fatalf("关闭已保存的：%q texts=%q", got, tt.Texts())
	}
	// ⌥⌘W：README 关掉，a.txt 留着
	a.OpenFile(r, "README.md", true)
	wait("README 读好", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	tt.Key(wui.Cmd|wui.Alt, wui.KeyW)
	tt.Frame()
	if got := titles(a.Docs); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("⌥⌘W 后：%q", got)
	}

	// 重开：顺序是拖过之后的
	a.OpenFile(r, "README.md", true)
	a.MoveDoc(1, 0)
	a.SaveState()
	a.Close()
	b, _, wait2 := newUI(t, ws, state)
	b.Start()
	wait2("标签放回去", func() bool { return len(b.Docs) == 2 })
	if got := titles(b.Docs); !slices.Equal(got, []string{"README.md", "a.txt"}) {
		t.Errorf("重开后的顺序：%q", got)
	}
}

// 拖终端标签换位置：只在这个仓库的终端标签之间；拖过去的那个成了当前标签，顺序记进会话。
func TestTermTabDrag(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	r := a.Repos[0]
	wait("git status", idle(r))
	a.NewTerminal(r, "")
	a.NewTerminal(r, "")
	tt.Frame()
	ts := a.TermTabs(r)
	if len(ts) != 3 {
		t.Fatalf("应有 3 个终端标签：%d", len(ts))
	}
	for i, name := range []string{"一", "二", "三"} {
		ts[i].Name = name
	}
	tt.Frame()

	dragTo(t, tt, "三", "一", 0.3)
	var got []string
	for _, x := range r.Terms {
		got = append(got, x.Name)
	}
	if !slices.Equal(got, []string{"三", "一", "二"}) || r.TermSel != 0 {
		t.Fatalf("拖到最前面后：%q，当前第 %d 个", got, r.TermSel)
	}
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	var saved []string
	if s := st.Sessions[a.WsPath]; s != nil && s.Terms[r.Root] != nil {
		for _, x := range s.Terms[r.Root].Tabs {
			saved = append(saved, x.Name)
		}
	}
	if !slices.Equal(saved, []string{"三", "一", "二"}) {
		t.Errorf("会话里的终端标签：%q", saved)
	}
}
