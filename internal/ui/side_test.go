package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 在侧边打开和"拆分"子菜单：文件行右键"在侧边打开"分成左右两组、新文件在右、键盘在右；⌥点在侧边打开（固定），
// 进现有的右组；⌘点多选不受影响；⌥回车把选中的都在侧边打开（不当回车）。标签右键"拆分"在"固定标签"上面，
// 四个方向都能分，放不下的方向灰，组里只有它一个时整项灰；编辑器右键也有。
func TestOpenToSide(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	for _, n := range []string{"b.txt", "c.txt", "d.txt", "e.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.St.Settings.TreeNoFollow = true // 下面点左边的编辑器会换当前标签，跟随会把树的选中换成 README.md
	a.ShowSidebar(workspace.ModeFiles)
	if !r.Expanded {
		a.ToggleRepo(r)
	}
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return loaded(a)() && r.Files.Loaded && tt.HasText("e.txt") })
	readme := a.OpenFile(r, "README.md", true)
	wait("README 读好", func() bool { return readme.Buf != nil })
	tt.Frame()

	doc := func(rel string) *docs.Doc {
		for _, d := range a.AllDocs() {
			if d.Rel == rel {
				return d
			}
		}
		return nil
	}
	expect := func(what string, want ...[]string) {
		t.Helper()
		got := groupTitles(a)
		if !slices.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("%s：%q，想要 %q", what, got, want)
		}
	}

	// 右键"在侧边打开"：左右两组，新文件在右，键盘在右
	mustRightClickRow(t, tt, "a.txt", "在侧边打开")
	expect("右键在侧边打开", []string{"README.md"}, []string{"a.txt"})
	if d := a.CurrentDoc(); d == nil || d.Rel != "a.txt" || !d.Pinned || a.Grp != a.Ed.B.G {
		t.Fatalf("在侧边打开后当前 %+v", d)
	}
	// ⌥点：不预览，固定标签进现有的右组
	if err := clickRowWith(tt, wui.Alt, "b.txt"); err != nil {
		t.Fatalf("⌥点 b.txt: %v", err)
	}
	tt.Frame()
	tt.Frame()
	expect("⌥点", []string{"README.md"}, []string{"a.txt", "b.txt"})
	if d := doc("b.txt"); d == nil || !d.Pinned || a.CurrentDoc() != d {
		t.Fatalf("⌥点后 b.txt：%+v", d)
	}
	// 点左边的编辑器把键盘拿回左组，再⌘点：照旧是多选，不开文件（文件树拿到键盘）
	mustClickRow(t, tt, "编辑 README.md")
	tt.Frame()
	if a.Grp != a.Ed.A.G {
		t.Fatalf("点左边的编辑器后键盘不在左组")
	}
	for _, n := range []string{"c.txt", "d.txt"} {
		if err := clickRowWith(tt, wui.Cmd, n); err != nil {
			t.Fatalf("⌘点 %s: %v", n, err)
		}
		tt.Frame()
	}
	var chosen []string
	for _, row := range a.ChosenRows() {
		chosen = append(chosen, row.Path)
	}
	if !slices.Equal(chosen, []string{"b.txt", "c.txt", "d.txt"}) || len(a.AllDocs()) != 3 || a.Grp != a.Ed.A.G {
		t.Fatalf("⌘点：选中 %q，标签 %q", chosen, groupTitles(a))
	}
	// ⌥回车：选中的都进左组旁边的右组，开着的 b.txt 只是选中，最后一个当前
	tt.Key(wui.Alt, wui.KeyEnter)
	tt.Frame()
	expect("⌥回车", []string{"README.md"}, []string{"a.txt", "b.txt", "c.txt", "d.txt"})
	if d := a.CurrentDoc(); d == nil || d.Rel != "d.txt" || a.Grp != a.Ed.B.G {
		t.Fatalf("⌥回车后当前 %+v", d)
	}
	wait("都读好", func() bool { return !slices.ContainsFunc(a.AllDocs(), func(d *docs.Doc) bool { return d.Buf == nil }) })
	tt.Frame()

	// 编辑器右键：d.txt 向右分出去
	mustRightClickRow(t, tt, "编辑 d.txt", "拆分", "向右拆分")
	expect("编辑器右键向右拆分", []string{"README.md"}, []string{"a.txt", "b.txt", "c.txt"}, []string{"d.txt"})
	// 标签右键："拆分"和"在另一组里再开一份"在"固定标签"上面；这组太窄，向左灰；向下分
	if err := rightClickRow(tt, "标签 c.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m := shownMenu(t, tt); !slices.Contains(m, "拆分") || slices.Index(m, "在另一组里再开一份") != slices.Index(m, "拆分")+1 || slices.Index(m, "固定标签") != slices.Index(m, "拆分")+2 {
		t.Fatalf("标签菜单：%q", m)
	}
	if err := chooseMenu(t, tt, "拆分", "向左拆分"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("太窄时向左拆分：%v", err)
	}
	if err := chooseMenu(t, tt, "拆分", "向下拆分"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	expect("向下拆分", []string{"README.md"}, []string{"a.txt", "b.txt"}, []string{"c.txt"}, []string{"d.txt"})
	mustRightClickRow(t, tt, "标签 b.txt", "拆分", "向上拆分")
	expect("向上拆分", []string{"README.md"}, []string{"b.txt"}, []string{"a.txt"}, []string{"c.txt"}, []string{"d.txt"})
	if a.CurrentDoc() != doc("b.txt") {
		t.Fatalf("向上拆分后当前 %v", a.CurrentDoc().Title)
	}
	// 组里只有它一个：整项灰
	if err := rightClickRow(tt, "标签 a.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(t, tt, "拆分", "向右拆分"); err == nil || !strings.Contains(err.Error(), `"拆分" is disabled`) {
		t.Fatalf("一个标签时拆分：%v", err)
	}
	closeMenu(t, tt)
	// 向左：左边那组再开一个，把它分到左边
	a.FocusGroup(a.GroupOf(readme))
	e := a.OpenFile(r, "e.txt", true)
	wait("e.txt 读好", func() bool { return e.Buf != nil })
	tt.Frame()
	mustRightClickRow(t, tt, "标签 e.txt", "拆分", "向左拆分")
	expect("向左拆分", []string{"e.txt"}, []string{"README.md"}, []string{"b.txt"}, []string{"a.txt"}, []string{"c.txt"}, []string{"d.txt"})
	if a.CurrentDoc() != e {
		t.Fatalf("向左拆分后当前 %v", a.CurrentDoc().Title)
	}
	accessCheck(t, tt, "六组")
}
