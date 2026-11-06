package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// mergeUI 在合并到一半、c.txt 有冲突的临时仓库上开界面，侧栏是 Git 栏，状态读好。
func mergeUI(t *testing.T, base, ours, theirs string) (*app.App, *app.Repo, *wui.Tester, func(string, func() bool)) {
	t.Helper()
	repo := testutil.ConflictRepo(t, "merge", "c.txt", base, ours, theirs)
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if r.Status.Conflicts != 1 {
		t.Fatalf("应有 1 个冲突: %+v", r.Status)
	}
	return a, r, tt, wait
}

// 冲突条：第几处、用我的之后剩一处、下一处挪光标、⌃⌥2 用他们的、全部用我的先问；都解决了换成"标记为已解决"，
// 点了保存并暂存，条就没了。
func TestMergeBar(t *testing.T) {
	a, r, tt, wait := mergeUI(t, "a\n2\n3\n4\n5\n6\n7\nz\n",
		"a\n我的二\n3\n4\n5\n6\n我的七\nz\n", "a\n他们的二\n3\n4\n5\n6\n他们的七\nz\n")
	d := a.OpenFile(r, "c.txt", true)
	wait("读文件", func() bool { return d.Buf != nil })
	tt.Frame()
	mustText(t, tt, "第 1 / 2 处冲突")
	mustText(t, tt, "2 处冲突") // 状态栏
	if len(d.St.LineBackgrounds) == 0 || len(d.St.GutterMarks) != 2 {
		t.Errorf("冲突块没染色: %d 段底色、%d 个槽里的点", len(d.St.LineBackgrounds), len(d.St.GutterMarks))
	}
	accessCheck(t, tt, "冲突条")

	mustClick(t, tt, "全部用我的")
	mustText(t, tt, "2 处冲突全部用我的？")
	mustClick(t, tt, "取消")
	if len(d.ConflictBlocks()) != 2 {
		t.Fatal("取消了还是改了")
	}

	mustClick(t, tt, "用我的")
	mustText(t, tt, "第 1 / 1 处冲突")
	if got := d.Buf.Line(1); got != "我的二" {
		t.Errorf("用我的之后第 2 行是 %q", got)
	}
	mustClick(t, tt, "下一处冲突")
	if left := d.ConflictBlocks(); len(left) != 1 || d.St.CursorPos().Line != left[0].Start {
		t.Errorf("下一处: 光标在第 %d 行，块 %+v", d.St.CursorPos().Line, left)
	}

	// 键盘在编辑器里：⌃⌥2 用他们的（编辑器的 OnInput 接 ⌃⌥ 的键）
	if !tt.Focused("编辑 c.txt") {
		mustClick(t, tt, "编辑 c.txt")
		a.GoToConflict(d, 1)
		tt.Frame()
	}
	tt.Key(wui.Ctrl|wui.Alt, wui.Key2)
	tt.Frame()
	if got := d.Buf.String(); got != "a\n我的二\n3\n4\n5\n6\n他们的七\nz\n" {
		t.Fatalf("⌃⌥2 之后 %q", got)
	}
	mustText(t, tt, "冲突标记都处理完了")
	if len(d.St.LineBackgrounds) != 0 || len(d.St.GutterMarks) != 0 {
		t.Error("没有块了底色还在")
	}
	accessCheck(t, tt, "冲突标记都处理完了")

	mustClick(t, tt, "标记为已解决")
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	tt.Frame()
	if d.Modified() || r.Status.Staged != 1 || shows(tt, "冲突标记都处理完了") {
		t.Errorf("标记后: 没保存 %v，%+v", d.Modified(), r.Status)
	}
}

// Git 栏单击冲突行开编辑器（带冲突条）；右键"查看 diff（HEAD ↔ 工作区）"、"两边的 diff"各开一个 Diff；
// 指针在行上时右边的 ✓ 是"标记为已解决"，还有标记就先问。
func TestConflictRowOpensEditor(t *testing.T) {
	a, r, tt, wait := mergeUI(t, "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")
	row, ok := tt.Find("c.txt")
	if !ok {
		t.Fatalf("Git 栏没有 c.txt: %q", tt.Texts())
	}
	x, y := row.X+row.W/2, row.Y+row.H/2

	mustClickRow(t, tt, "c.txt")
	wait("打开文件", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	tt.Frame()
	if d := a.CurrentDoc(); d.Kind != docs.File || d.Rel != "c.txt" {
		t.Fatalf("单击冲突行应开文件，开的是 %v %q", d.Kind, d.Title)
	}
	mustText(t, tt, "第 1 / 1 处冲突")
	if a.GitSel < 0 || a.GitRows()[a.GitSel].Change.Kind != git.Conflict {
		t.Errorf("Git 栏的冲突行应还选着: %d", a.GitSel)
	}

	tt.RightClickAt(x, y)
	tt.Frame()
	menu := shownMenu(t, tt)
	for _, want := range []string{"在编辑器中解决冲突", "查看 diff（HEAD ↔ 工作区）", "两边的 diff", "标记为已解决"} {
		if !slices.Contains(menu, want) {
			t.Errorf("冲突行的右键菜单里没有 %q: %q", want, menu)
		}
	}
	if err := chooseMenu(t, tt, "查看 diff（HEAD ↔ 工作区）"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d := a.CurrentDoc(); d.Kind != docs.Diff || d.Change.Code != 'U' {
		t.Fatalf("应开 HEAD ↔ 工作区的 Diff: %v %q", d.Kind, d.Title)
	}

	tt.RightClickAt(x, y)
	tt.Frame()
	if err := chooseMenu(t, tt, "两边的 diff"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s := a.CurrentDoc()
	if s.Kind != docs.Diff || s.Change.Code != 'S' || s.Title != "c.txt（两边）" {
		t.Fatalf("应开两边的 diff: %v %q", s.Kind, s.Title)
	}
	wait("读两边", func() bool { return !s.Loading })
	tt.Frame()
	mustText(t, tt, "我们的（:2）↔ 他们的（:3）")
	if n := len(a.AllDocs()); n != 3 {
		t.Errorf("应有文件、HEAD Diff、两边 diff 三个标签，有 %d 个", n)
	}

	tt.Move(x, y)
	tt.Frame()
	if !shows(tt, "标记为已解决") {
		t.Fatalf("指针在冲突行上应有 ✓: %q", tt.Texts())
	}
	accessCheck(t, tt, "冲突行上的 ✓")
	mustClick(t, tt, "标记为已解决")
	mustText(t, tt, "还有 1 处冲突标记，标成已解决？")
	mustClick(t, tt, "取消")
	if r.Status.Conflicts != 1 {
		t.Errorf("取消了还是暂存了: %+v", r.Status)
	}
}
