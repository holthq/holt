package ui

import (
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// 段头只有名字、分支、拉取和推送：改动数只在收起时出现（展开时分组标题上有），没有"全部暂存"。
func TestRepoHeaderSlim(t *testing.T) {
	alpha, _, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("读状态", func() bool { return loaded(a)() && idle(a.Repos[0])() && idle(a.Repos[1])() && idle(a.Repos[2])() })
	var r *app.Repo
	for _, x := range a.Repos {
		if x.Root == alpha.Root {
			r = x
		}
	}
	if !r.GitOpen() {
		a.ToggleGitRepo(r)
	}
	tt.Frame()
	sum := changeSummary(r.Status) // 状态栏上也写着这句，所以比收起前后多没多
	before := countText(tt, sum)
	a.ToggleGitRepo(r)
	tt.Frame()
	if after := countText(tt, sum); after <= before {
		t.Errorf("收起时段头该多出改动数 %q：%d → %d", sum, before, after)
	}
}

// 分组标题上的按钮指针移上去才有，移开就没了；右键是同样的动作。
func TestGroupActionsOnHover(t *testing.T) {
	repo := testutil.NewRepo(t, "group")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	repo.Write("a.txt", "b\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	tt.Move(1200, 800)
	tt.Frame()
	tt.Frame()
	stage, discard := i18n.T("暂存所有未暂存的改动"), i18n.T("丢弃所有未暂存的改动…")
	if tt.HasText(stage) || tt.HasText(discard) {
		t.Fatalf("指针不在分组标题上时不该有按钮: %q", tt.Texts())
	}
	hoverOn(t, tt, "收起 改动")
	mustText(t, tt, stage)
	mustText(t, tt, discard)
	tt.Move(1200, 800)
	tt.Frame()
	tt.Frame()
	if tt.HasText(stage) {
		t.Fatal("指针移开了按钮还在")
	}
	mustRightClickRow(t, tt, "收起 改动", stage)
	wait("暂存", func() bool { return idle(r)() && r.Status.Staged == 1 })
	mustRightClickRow(t, tt, "收起 已暂存", i18n.T("全部取消暂存"))
	wait("取消暂存", func() bool { return idle(r)() && r.Status.Staged == 0 })
}

// 提交框底下的"修改上一次提交"是图标开关：点了开、再点关；有一条提交排着时画灰、点了不切，提示还在。
// 提交按钮灰着（没写信息）点了也不提交。
func TestAmendToggle(t *testing.T) {
	repo := testutil.NewRepo(t, "amend")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", idle(r))
	a.OpenCommit(r)
	tt.Frame()

	name := i18n.T("修改上一次提交…")
	chooseCommitMenu(t, tt, name)
	if !r.Amend {
		t.Fatal("选了该开着")
	}
	tt.Frame()
	chooseCommitMenu(t, tt, name)
	if r.Amend {
		t.Fatal("再选一下该关了")
	}

	// 没写信息：框里没有提交按钮
	repo.Write("a.txt", "b\n")
	repo.Git("add", "a.txt")
	a.RefreshRepo(r, app.WhyManual)
	wait("暂存", func() bool { return idle(r)() && r.Status.Staged == 1 })
	r.CommitMsg = ""
	tt.Frame()
	if tt.HasText(i18n.Tf("提交 %s 个文件", "1")) {
		t.Fatal("没写信息时不该有提交按钮")
	}

	// 前面排着一条慢命令，提交排在它后面：提交中，▾ 里那一项灰着
	slowOp(a, r)
	r.CommitMsg = "改一下"
	a.Commit(r)
	tt.Frame()
	if !r.Committing() {
		t.Fatal("提交该在排队")
	}
	openCommitMenu(t, tt)
	if !menuDisabled(t, tt, name) {
		t.Errorf("提交中修改上一次提交该灰着：%q", shownMenu(t, tt))
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	a.CancelOp(r) // 慢命令取消了，排着的提交接着跑完
	wait("队列跑完", idle(r))
}
