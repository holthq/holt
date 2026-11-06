package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// TestPanelShots 在 WORKBENCH_SHOT=目录 时把 #56 改过的几处截下来（临时仓库，不读真实工作区）：
// 通栏的编辑器标签（多个标签、分屏）、底栏的终端标签（多个标签、输出、问题）、合并冲突条、进行中横幅、
// 提交框的修改上一次提交开关（关 / 开）。没设 WORKBENCH_SHOT 就跳过。
func TestPanelShots(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("没设 WORKBENCH_SHOT")
	}
	merge := testutil.ConflictRepo(t, "merge", "c.txt", "1\n2\n", "1\n我的\n", "1\n他们的\n")
	two := testutil.NewRepo(t, "two")
	for _, f := range []string{"main.go", "一个名字很长很长很长很长很长很长很长很长的文件.md", "util.go"} {
		two.Write(f, "package two\n")
	}
	two.Commit("初始提交")
	two.Write("main.go", "package two\n\nfunc main() {}\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, merge.Root, two.Root), "")
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("读状态", func() bool { return loaded(a)() && idle(a.Repos[0])() && idle(a.Repos[1])() })
	m, w := a.Repos[0], a.Repos[1]
	if m.Root != merge.Root {
		m, w = w, m
	}
	for _, f := range []string{"main.go", "一个名字很长很长很长很长很长很长很长很长的文件.md", "util.go"} {
		a.OpenFile(w, f, true)
	}
	a.OpenFile(m, "c.txt", true)
	a.SplitEditor(true)
	a.OpenFile(w, "main.go", true)

	// 底栏：三个终端标签，输出里有一条
	a.UseRepo(m)
	if !a.St.BottomBar {
		a.Exec("view.bottom")
	}
	for range 3 {
		a.NewTerminal(m, "")
	}
	a.StageAll(w)
	wait("暂存", idle(w))
	a.OpenCommit(m)
	shot := func(name string) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		savePNG(t, filepath.Join(dir, name), tt.Image())
	}
	shot("panels-term.png")
	a.BottomTab = app.BottomOutput
	shot("panels-output.png")
	a.BottomTab = app.BottomProblems
	shot("panels-problems.png")
	a.BottomTab = app.BottomTerminal
	if m.Extra.Head != "" {
		a.SetAmend(m, true)
		shot("panels-amend-on.png")
		a.SetAmend(m, false)
	}
	tt.SetDark(true)
	shot("panels-dark.png")
}
