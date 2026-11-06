package ui

import (
	"strings"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/tasks"
	"github.com/holthq/holt/internal/testutil"
)

// 任务：⇧⌘B（task.run）开面板 "!"，回车在仓库里新开一个叫任务名的终端标签跑（在最后、键盘给它）；
// 再跑同一个任务用回那个标签；那个标签前台有命令在跑就再开一个。任务是直接放进 Repo.Tasks 的（不靠机器上装没装 make / npm）。
func TestRunTask(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	r := a.Repos[0]
	wait("git status", idle(r))
	readyPane(t, a, tt)
	r.Tasks = []tasks.Task{
		{Name: "hello", Cmd: "echo task-$((6*7))", Source: "Makefile"},
		{Name: "nap", Cmd: "sleep 30", Source: "Makefile"},
	}
	ran := func(p *app.TermPane, want string, n int) func() bool {
		return func() bool {
			tt.Frame()
			return p.Term != nil && strings.Count(p.Term.Text(), want) >= n
		}
	}
	labels := func() []string {
		var out []string
		for _, tb := range a.TermTabs(r) {
			out = append(out, r.TermLabel(tb))
		}
		return out
	}

	a.Exec("task.run")
	a.UpdatePalette()
	if !a.Palette.Open || a.Palette.Text != "!" || len(a.Palette.Items) != 2 || a.Palette.Items[0].Text != "demo · echo task-$((6*7))" {
		t.Fatalf("⇧⌘B：%v %q %+v", a.Palette.Open, a.Palette.Text, a.Palette.Items)
	}
	tt.Frame()
	mustText(t, tt, "Makefile")
	a.AcceptPalette(0)
	tt.Frame()
	_, tab, p := a.ActivePane()
	if a.Palette.Open || tab.Name != "hello" || r.TermSel != 1 || len(a.TermTabs(r)) != 2 {
		t.Fatalf("回车后：面板 %v，标签 %q，选中 %d", a.Palette.Open, labels(), r.TermSel)
	}
	wait("任务跑了", ran(p, "\ntask-42", 1))
	if len(a.St.TaskHistory) != 1 || a.St.TaskHistory[0].Cmd != "echo task-$((6*7))" {
		t.Fatalf("任务历史：%+v", a.St.TaskHistory)
	}

	// 切回第一个标签再跑：用回 hello 那个标签
	a.SelectTermTab(r, 0)
	tt.Frame()
	a.RunTask(r, r.Tasks[0])
	tt.Frame()
	if _, _, q := a.ActivePane(); q != p || len(a.TermTabs(r)) != 2 {
		t.Fatalf("同一个任务应用回那个标签：%q，选中 %d", labels(), r.TermSel)
	}
	wait("又跑了一遍", ran(p, "\ntask-42", 2))

	// nap 在跑（前台有命令）时再跑 nap：再开一个
	a.RunTask(r, r.Tasks[1])
	tt.Frame()
	_, _, n1 := a.ActivePane()
	wait("sleep 在跑", func() bool { tt.Frame(); return n1.Job() == "sleep" })
	a.RunTask(r, r.Tasks[1])
	tt.Frame()
	if _, _, n2 := a.ActivePane(); n2 == n1 || len(a.TermTabs(r)) != 4 {
		t.Fatalf("前台有命令时应再开一个：%q", labels())
	}
	if got := labels(); got[1] != "hello" || got[2] != "nap" || got[3] != "nap" {
		t.Fatalf("标签名：%q", got)
	}
}
