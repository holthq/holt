package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 五块落点：离哪条边不到 1/4 就是那边，两边都近取更近的，中间是搬进这组，外面不算；预览盖的是新组会占的那一半。
func TestDropSide(t *testing.T) {
	for _, c := range []struct {
		x, y float32
		want Side
	}{
		{200, 150, SideCenter}, {101, 150, SideCenter}, {99, 150, SideLeft}, {350, 150, SideRight},
		{200, 10, SideTop}, {200, 290, SideBottom}, {10, 20, SideLeft}, {20, 10, SideTop},
		{390, 295, SideBottom}, {-1, 150, SideNone}, {200, 301, SideNone},
	} {
		if got := DropSide(c.x, c.y, 400, 300); got != c.want {
			t.Errorf("(%v, %v) 落在 %v，要 %v", c.x, c.y, got, c.want)
		}
	}
	if DropSide(1, 1, 0, 0) != SideNone {
		t.Error("没排过的组不该有落点")
	}
	for s, want := range map[Side][4]float32{
		SideCenter: {0, 0, 400, 300}, SideLeft: {0, 0, 200, 300}, SideRight: {200, 0, 200, 300},
		SideTop: {0, 0, 400, 150}, SideBottom: {0, 150, 400, 150},
	} {
		if x, y, w, h := DropRect(s, 400, 300); [4]float32{x, y, w, h} != want {
			t.Errorf("%v 的预览 %v，要 %v", s, [4]float32{x, y, w, h}, want)
		}
	}
}

func groupTitles(a *App) [][]string {
	var out [][]string
	for _, g := range a.Groups() {
		ds, _ := a.GroupDocs(g)
		var ts []string
		for _, d := range ds {
			ts = append(ts, d.Title)
		}
		out = append(out, ts)
	}
	return out
}

// 分屏的模型：⌘\ 把当前标签搬到右边新的一组、键盘跟过去；拖到中间换组、拖到边上再分（可以嵌套）；
// 关组里最后一个标签组就收起，兄弟占满、键盘到兄弟；只有一个标签、太小时不分；合并按组的顺序接起来；
// ‹ › 和打开已经开着的文件都跳到它所在的组。
func TestEditorGroups(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.Repos[0]
	readme := a.OpenFile(r, "README.md", true)
	at := a.OpenFile(r, "a.txt", true)
	wait("读完", func() bool { return !readme.Loading && !at.Loading })

	if a.Split() || len(a.Groups()) != 1 || !slices.Equal(a.AllDocs(), a.Docs) {
		t.Fatal("一开始只有一组")
	}
	// ⌘\：a.txt 搬到右边
	first := a.Grp
	a.SplitEditor(true)
	if got := groupTitles(a); !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"a.txt"}) || len(got) != 2 {
		t.Fatalf("⌘\\ 之后：%q", got)
	}
	if !a.Ed.Row || a.Ed.Ratio != 0.5 || a.Grp == first || a.CurrentDoc() != at || !a.FocusDoc {
		t.Fatalf("右边的新组：row=%v ratio=%v 当前 %v", a.Ed.Row, a.Ed.Ratio, a.CurrentDoc().Title)
	}
	// 只有一个标签：分不出来
	a.SplitEditor(false)
	if len(a.Groups()) != 2 || a.Toast != "这一组只有一个标签，分不出新的一组" {
		t.Fatalf("一个标签也分了：%q %q", groupTitles(a), a.Toast)
	}

	// 在左边那组开 u.txt（键盘先回左边），⌥⌘\ 搬到它下面：左边上下两组 + 右边一组
	a.FocusGroup(first)
	if a.CurrentDoc() != readme {
		t.Fatalf("回到左边：当前 %v", a.CurrentDoc().Title)
	}
	u := a.OpenFile(r, "u.txt", true)
	a.Grp.H = 2*MinGroupH - 1
	a.SplitEditor(false)
	if a.Toast != "这一组太小，分不开了" || len(a.Groups()) != 2 {
		t.Fatalf("太小也分了：%q", groupTitles(a))
	}
	a.Grp.H = 600
	a.SplitEditor(false)
	if got := groupTitles(a); len(got) != 3 || !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"u.txt"}) ||
		!slices.Equal(got[2], []string{"a.txt"}) || a.Ed.A.Row || a.Ed.A.G != nil {
		t.Fatalf("嵌套分屏：%q", got)
	}
	if !slices.Equal(titlesOf(a.AllDocs()), []string{"README.md", "u.txt", "a.txt"}) {
		t.Fatalf("所有标签：%q", titlesOf(a.AllDocs()))
	}

	// 打开已经在右边的 a.txt：键盘跳过去；‹ 回到 u.txt 那组
	a.OpenFile(r, "a.txt", true)
	right := a.Grp
	if a.CurrentDoc() != at || slices.Contains(a.Groups()[:2], right) {
		t.Fatalf("打开开着的文件：当前 %v", a.CurrentDoc().Title)
	}
	a.Back()
	if a.CurrentDoc() != u {
		t.Fatalf("‹ 之后当前 %v", a.CurrentDoc().Title)
	}

	// 拖 README 到右边那组中间：左上那组空了收起，u.txt 那组占满左边
	if a.CanDrop(readme, a.GroupOf(readme), SideCenter) || !a.CanDrop(readme, right, SideCenter) {
		t.Fatal("拖回自己那组中间不该动，拖到别的组中间要动")
	}
	if a.CanDrop(u, a.GroupOf(u), SideLeft) {
		t.Fatal("自己那组只有它一个时不该能分")
	}
	a.DropDoc(readme, right, SideCenter)
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"u.txt"}) || !slices.Equal(got[1], []string{"a.txt", "README.md"}) {
		t.Fatalf("拖到中间：%q", got)
	}
	if a.Grp != right || a.CurrentDoc() != readme || !a.Ed.Row {
		t.Fatalf("键盘跟着标签：当前 %v", a.CurrentDoc().Title)
	}
	// 拖 a.txt 到左边那组的上边：左边又上下分开，a.txt 在上
	left := a.GroupOf(u)
	a.DropDoc(at, left, SideTop)
	if got := groupTitles(a); len(got) != 3 || !slices.Equal(got[0], []string{"a.txt"}) || !slices.Equal(got[1], []string{"u.txt"}) ||
		!slices.Equal(got[2], []string{"README.md"}) {
		t.Fatalf("拖到上边：%q", got)
	}
	// 拖到别的组的标签上：插在它前面
	a.DropOnTab(readme, u)
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[1], []string{"README.md", "u.txt"}) {
		t.Fatalf("拖到标签上：%q", got)
	}

	// 关组里最后一个标签：组收起，兄弟占满，键盘到兄弟
	a.FocusGroup(a.GroupOf(at))
	a.RemoveDoc(at)
	if a.Split() || a.CurrentDoc() == nil || !slices.Equal(titlesOf(a.Docs), []string{"README.md", "u.txt"}) {
		t.Fatalf("关了最后一个：%q，当前 %v", groupTitles(a), a.CurrentDoc())
	}

	// 再分开，关掉别的组里的标签（不是有键盘的那组）：那组收起，键盘不动
	a.SelectDoc(0)
	a.SplitEditor(true)
	cur := a.CurrentDoc()
	other := a.Groups()[0]
	ds, _ := a.GroupDocs(other)
	a.RemoveDoc(ds[0])
	if a.Split() || a.CurrentDoc() != cur {
		t.Fatalf("关别的组的最后一个：%q，当前 %v", groupTitles(a), a.CurrentDoc())
	}

	// 合并：按组的顺序接起来，当前标签不变
	a.OpenFile(r, "a.txt", true)
	a.SplitEditor(true)
	a.SplitEditor(true) // 新组只有一个标签了：不动
	a.FocusGroup(a.Groups()[0])
	cur = a.CurrentDoc()
	a.MergeGroups()
	if a.Split() || !slices.Equal(titlesOf(a.Docs), []string{"README.md", "a.txt"}) || a.CurrentDoc() != cur {
		t.Fatalf("合并：%q，当前 %v", titlesOf(a.Docs), a.CurrentDoc().Title)
	}
	_ = root
}

func titlesOf(ds []*docs.Doc) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Title)
	}
	return out
}

// 分屏随会话存进 state.json（Tabs 按组的顺序排，叶子记自己的标签在 Tabs 里的下标、当前第几个、键盘在哪组）、重开放回去；
// 没有 Layout 的老会话照旧一组；记的标签磁盘上没了，那组就不要了。只有一组时不写 layout。
func TestEditorLayoutRoundTrip(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	state := t.TempDir()
	a, wait := newTestAppIn(t, ws, state)
	r := a.Repos[0]
	a.OpenFile(r, "README.md", true)
	at := a.OpenFile(r, "a.txt", true)
	u := a.OpenFile(r, "u.txt", true)
	wait("读完", func() bool { return !at.Loading && !u.Loading })
	a.SplitEditor(true) // 右边 u.txt
	diff := a.OpenDiff(r, git.Change{Kind: git.Unstaged, Path: "a.txt"}, true)
	a.DropDoc(at, a.Grp, SideBottom) // 右边上下：u.txt + Diff / a.txt
	a.Ed.Ratio = 0.3
	if got := groupTitles(a); len(got) != 3 || !slices.Equal(got[2], []string{"a.txt"}) {
		t.Fatalf("分好的：%q", got)
	}
	a.FocusGroup(a.Groups()[1])
	a.SaveState()
	a.Close()

	// Diff 标签也记，是右上那组的第二个、当前的
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[ws]
	l := s.Layout
	if l == nil || !l.Row || l.Ratio != 0.3 || l.A == nil || !slices.Equal(l.A.Tabs, []int{0}) || l.B.Row ||
		!slices.Equal(l.B.A.Tabs, []int{1, 2}) || !l.B.A.Focus || l.B.A.Sel != 1 || !slices.Equal(l.B.B.Tabs, []int{3}) ||
		s.Tabs[2].Kind != workspace.TabDiff {
		j, _ := json.Marshal(l)
		t.Fatalf("存的布局：%s，标签 %v", j, s.Tabs)
	}

	b, wait := newTestAppIn(t, ws, state)
	b.restoreTabs()
	t.Cleanup(b.Close)
	wait("重开的读完", func() bool { return !slices.ContainsFunc(b.AllDocs(), func(d *docs.Doc) bool { return d.Loading }) })
	if got := groupTitles(b); len(got) != 3 || !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"u.txt", diff.Title}) ||
		!slices.Equal(got[2], []string{"a.txt"}) {
		t.Fatalf("重开的组：%q", got)
	}
	if !b.Ed.Row || b.Ed.Ratio != 0.3 || b.Ed.B.Row || b.Grp != b.Groups()[1] || b.CurrentDoc().Kind != docs.Diff {
		t.Fatalf("重开的树：row=%v ratio=%v 当前 %v", b.Ed.Row, b.Ed.Ratio, b.CurrentDoc().Title)
	}

	// u.txt 没了：右上那组不要，右边只剩 a.txt 那组（先关掉 Diff，那组只剩 u.txt）
	b.RemoveDoc(b.CurrentDoc())
	b.SaveState()
	b.Close()
	testutil.GitOut(t, root, "clean", "-fq", "u.txt")
	c, wait := newTestAppIn(t, ws, state)
	c.restoreTabs()
	t.Cleanup(c.Close)
	wait("读完", func() bool { return !slices.ContainsFunc(c.AllDocs(), func(d *docs.Doc) bool { return d.Loading }) })
	if got := groupTitles(c); len(got) != 2 || !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"a.txt"}) {
		t.Fatalf("少了一个文件后：%q", got)
	}

	// 合并成一组再存：不写 layout；老会话没有 layout：一组
	c.MergeGroups()
	c.SaveState()
	if st := c.session(); st.Layout != nil {
		t.Fatalf("一组也写了 layout：%+v", st.Layout)
	}
	old := &workspace.Session{Tabs: []workspace.Tab{{Path: at.Abs}}, Active: 0}
	d, _ := newTestApp(t, ws)
	t.Cleanup(d.Close)
	d.St.Sessions = map[string]*workspace.Session{d.sessionKey(): old}
	d.restoreTabs()
	if d.Split() || len(d.Docs) != 1 {
		t.Fatalf("老会话：%q", groupTitles(d))
	}
}

// 在侧边打开：没有标签时就是打开；只有一组时向右分出一组、键盘跟过去；键盘在最后一组时接着开进它，不再分；
// 开在别的组的跳过去；开在这组的搬到侧边的组，这组只有它一个就只提示；太小就提示、开在这组；
// 多选都进同一个侧边的组、最后一个当前。
func TestOpenSide(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	for _, n := range []string{"b.txt", "c.txt", "d.txt", "e.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.Repos[0]
	t.Cleanup(func() {
		wait("都读完", func() bool { return !slices.ContainsFunc(a.AllDocs(), func(d *docs.Doc) bool { return d.Loading }) })
	})

	readme := a.OpenFileSide(r, "README.md")
	if a.Split() || a.CurrentDoc() != readme || !readme.Pinned {
		t.Fatalf("没有标签时：%q", groupTitles(a))
	}
	left := a.Grp
	at := a.OpenFileSide(r, "a.txt")
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"README.md"}) || !slices.Equal(got[1], []string{"a.txt"}) {
		t.Fatalf("在侧边开 a.txt：%q", got)
	}
	right := a.Grp
	if !a.Ed.Row || a.Ed.A.G != left || a.Ed.B.G != right || a.CurrentDoc() != at || !at.Pinned || !a.FocusDoc {
		t.Fatalf("右边的新组：row=%v 当前 %v", a.Ed.Row, a.CurrentDoc().Title)
	}
	// 键盘在右边（最后一组）：进现有的右组
	u := a.OpenFileSide(r, "u.txt")
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[1], []string{"a.txt", "u.txt"}) || a.Grp != right || a.CurrentDoc() != u {
		t.Fatalf("再在侧边开 u.txt：%q", got)
	}

	// 左组两个标签时在侧边开其中一个：搬到右组
	a.FocusGroup(left)
	b := a.OpenFile(r, "b.txt", true)
	a.OpenFileSide(r, "README.md")
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"b.txt"}) || !slices.Equal(got[1], []string{"a.txt", "u.txt", "README.md"}) ||
		a.Grp != right || a.CurrentDoc() != readme {
		t.Fatalf("搬到右组：%q", got)
	}
	// 左组只剩它一个：只提示
	a.FocusGroup(left)
	a.Toast = ""
	a.OpenFileSide(r, "b.txt")
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[0], []string{"b.txt"}) || a.Grp != left || a.CurrentDoc() != b ||
		a.Toast != "这一组只有一个标签，分不出新的一组" {
		t.Fatalf("只有一个标签：%q %q", got, a.Toast)
	}
	// 开在右组的：跳过去选中它
	a.OpenFileSide(r, "a.txt")
	if got := groupTitles(a); len(got) != 2 || len(got[1]) != 3 || a.Grp != right || a.CurrentDoc() != at {
		t.Fatalf("开在右组的：%q，当前 %v", got, a.CurrentDoc().Title)
	}

	// 一组、太小：提示，开在这组
	a.MergeGroups()
	a.Grp.W = 2*MinGroupW - 1
	a.Toast = ""
	c := a.OpenFileSide(r, "c.txt")
	if a.Split() || a.CurrentDoc() != c || !c.Pinned || a.Toast != "这一组太小，分不开了" {
		t.Fatalf("太小：%q %q", groupTitles(a), a.Toast)
	}

	// 多选两个：都进新分出的右组，最后一个当前
	a.Grp.W = 800
	n := len(a.Docs)
	a.OpenRowsSide([]TreeRow{{Repo: r, Row: files.Row{Path: "d.txt"}}, {Repo: r, Row: files.Row{Path: "docs", Dir: true}}, {Repo: r, Row: files.Row{Path: "e.txt"}}})
	if got := groupTitles(a); len(got) != 2 || len(got[0]) != n || !slices.Equal(got[1], []string{"d.txt", "e.txt"}) ||
		a.CurrentDoc().Title != "e.txt" || a.Grp != a.Ed.B.G {
		t.Fatalf("多选在侧边打开：%q，当前 %v", got, a.CurrentDoc().Title)
	}
}

// 标签的"拆分"子菜单：四个方向都把 d 搬进它那组那一边新分出的一组（左 / 上的新组在前），键盘跟过去，
// 不在键盘那组的标签也行；那组只有它一个、那个方向太小时分不了（菜单里灰）。
func TestSplitDoc(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	r := a.Repos[0]
	readme := a.OpenFile(r, "README.md", true)
	at := a.OpenFile(r, "a.txt", true)
	wait("读完", func() bool { return !readme.Loading && !at.Loading })

	for _, c := range []struct {
		s          Side
		row, first bool
	}{{SideRight, true, false}, {SideLeft, true, true}, {SideBottom, false, false}, {SideTop, false, true}} {
		old := a.Grp
		if !a.CanSplit(at, c.s) {
			t.Fatalf("%v：不能分", c.s)
		}
		a.SplitDoc(at, c.s)
		nn := a.GroupOf(at)
		want := [2]*Group{old, nn}
		if c.first {
			want = [2]*Group{nn, old}
		}
		if a.Ed.G != nil || a.Ed.Row != c.row || [2]*Group{a.Ed.A.G, a.Ed.B.G} != want || a.Grp != nn || a.CurrentDoc() != at || !a.FocusDoc {
			t.Fatalf("%v：row=%v，%q，当前 %v", c.s, a.Ed.Row, groupTitles(a), a.CurrentDoc().Title)
		}
		if a.CanSplit(at, SideRight) || a.CanSplit(at, SideBottom) {
			t.Fatalf("%v：只有它一个的组还能分", c.s)
		}
		a.MergeGroups()
	}

	// 不在键盘那组的：分它自己那组
	a.SelectDoc(0)
	a.SplitDoc(at, SideBottom)
	if got := groupTitles(a); len(got) != 2 || !slices.Equal(got[1], []string{"a.txt"}) || a.Ed.Row || a.CurrentDoc() != at {
		t.Fatalf("分当前标签以外的：%q", got)
	}
	a.MergeGroups()

	// 太小：那个方向灰，另一个方向能分；不是四个方向之一的不算
	a.Grp.W, a.Grp.H = 2*MinGroupW-1, 600
	if a.CanSplit(at, SideLeft) || a.CanSplit(at, SideRight) || !a.CanSplit(at, SideTop) || a.CanSplit(at, SideCenter) || a.CanSplit(at, SideNone) {
		t.Fatal("太窄时：左右该灰、上下能分")
	}
	a.SplitDoc(at, SideRight)
	if a.Split() {
		t.Fatal("太窄也分了")
	}
}
