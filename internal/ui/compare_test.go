package ui

import (
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestCompareMenu：文件行右键"选择以进行比较"，别的文件右键多出"与 a.go 比较"（同一个文件上没有），选了开成比较标签、
// 标签条有上 / 下一处和"在编辑器中打开左边的文件"，底栏写“文件 ↔ 文件”、状态栏“只读 · 比较”；剪贴板空着"与剪贴板比较"说一声，
// 有字时开成“文件 ↔ 剪贴板”；多选正好两个文件有"比较这两个文件"，三个没有。
func TestCompareMenu(t *testing.T) {
	r0 := testutil.NewRepo(t, "cmpui")
	r0.Write("a.go", "package a\n\nvar x = 1\n")
	r0.Write("b.go", "package a\n\nvar x = 2\n")
	r0.Write("c.go", "package a\n")
	r0.Commit("初始")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	clip := ""
	old := app.ReadClipboard
	app.ReadClipboard = func() string { return clip }
	t.Cleanup(func() { app.ReadClipboard = old })
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Repos[0]
	if !r.Expanded {
		a.ToggleRepo(r)
	}
	a.RefreshRepo(r, app.WhyManual)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return loaded(a)() && r.Files.Loaded && tt.HasText("c.go") })

	menu := func(name string) []string {
		t.Helper()
		if err := tt.RightClick(name); err != nil {
			t.Fatalf("右键 %q: %v", name, err)
		}
		tt.Frame()
		m := tt.Menu()
		tt.CloseMenu()
		tt.Frame()
		return m
	}
	if m := menu("a.go"); !slices.Contains(m, "选择以进行比较") || !slices.Contains(m, "与剪贴板比较") || slices.ContainsFunc(m, func(s string) bool { return strings.HasPrefix(s, "与 ") && s != "与剪贴板比较" }) {
		t.Fatalf("没选过时的菜单 %q", m)
	}
	mustRightClick(t, tt, "a.go", "与剪贴板比较")
	if !tt.HasText("剪贴板是空的") || len(a.Docs) != 0 {
		t.Errorf("剪贴板空着：toast %q，标签 %d", a.Toast, len(a.Docs))
	}
	mustRightClick(t, tt, "a.go", "选择以进行比较")
	if a.CompareSelName() != "a.go" {
		t.Fatalf("已选 %q", a.CompareSelName())
	}
	if m := menu("a.go"); slices.Contains(m, "与 a.go 比较") {
		t.Errorf("同一个文件上不该有与 a.go 比较：%q", m)
	}
	mustRightClick(t, tt, "b.go", "与 a.go 比较")
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Compare || d.Title != "a.go ↔ b.go" {
		t.Fatalf("当前标签 %+v", d)
	}
	wait("比较标签", func() bool { return d.DiffSt != nil && tt.HasText("文件 ↔ 文件") })
	mustText(t, tt, "只读 · 比较")
	mustText(t, tt, "a.go ↔ b.go")
	for _, tip := range []string{app.Tip("上一处改动", "diff.prevHunk"), app.Tip("下一处改动", "diff.nextHunk"), "在编辑器中打开左边的文件"} {
		if _, ok := tt.Find(tip); !ok {
			t.Errorf("标签条上没有 %q", tip)
		}
	}

	clip = "package a\n\nvar x = 3\n"
	a.Exec("file.closeAll")
	tt.Frame()
	mustRightClick(t, tt, "c.go", "与剪贴板比较")
	wait("和剪贴板比", func() bool { return tt.HasText("文件 ↔ 剪贴板") && tt.HasText("c.go ↔ 剪贴板") })

	a.Exec("file.closeAll")
	tt.Frame()
	tt.Click("a.go")
	tt.Frame()
	tt.ClickWith(wui.Cmd, "c.go")
	tt.Frame()
	mustRightClick(t, tt, "c.go", "比较这两个文件")
	if d := a.CurrentDoc(); d == nil || d.Title != "a.go ↔ c.go" {
		t.Fatalf("比较这两个文件：%+v", d)
	}
	a.Exec("file.closeAll")
	tt.Frame()
	tt.ClickWith(wui.Cmd, "b.go")
	tt.Frame()
	if m := menu("b.go"); slices.Contains(m, "比较这两个文件") {
		t.Errorf("选了三个不该有比较这两个文件：%q", m)
	}
}
