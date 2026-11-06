package ui

import (
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
)

// 段头的"全部暂存"不用悬停也在：有可暂存的改动就画，暂存完就没了（#56）。三个仓库各一段，各管各的。
func TestStageAllShown(t *testing.T) {
	alpha, _, gamma, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("读状态", func() bool { return loaded(a)() && idle(a.Repos[0])() && idle(a.Repos[1])() && idle(a.Repos[2])() })
	tip := app.Tip(i18n.T("全部暂存（git add -A）"), "git.stageAll")
	count := func() int {
		tt.Frame()
		n := 0
		for _, nd := range accessTree(t, tt) {
			if nd.Label == tip {
				n++
			}
		}
		return n
	}
	repoOf := func(root string) *app.Repo {
		for _, r := range a.Repos {
			if r.Root == root {
				return r
			}
		}
		t.Fatalf("没有仓库 %s", root)
		return nil
	}
	// alpha、beta 有改动，gamma 干净：指针不在段头上也有两个
	if n := count(); n != 2 {
		t.Fatalf("全部暂存有 %d 个，应为 2（alpha、beta）", n)
	}
	gamma.Write("new.txt", "x\n")
	g := repoOf(gamma.Root)
	a.RefreshRepo(g, app.WhyManual)
	wait("gamma 读到未跟踪的文件", func() bool { return idle(g)() && g.Status.Untracked == 1 })
	if n := count(); n != 3 {
		t.Fatalf("gamma 有了未跟踪的文件：全部暂存有 %d 个，应为 3", n)
	}
	r := repoOf(alpha.Root)
	a.StageAll(r)
	wait("alpha 全部暂存", func() bool { return idle(r)() && r.Status.Unstaged+r.Status.Untracked == 0 })
	if n := count(); n != 2 {
		t.Errorf("alpha 暂存完了：全部暂存有 %d 个，应为 2", n)
	}
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

	name := i18n.T("修改上一次提交")
	mustClick(t, tt, name)
	if !r.Amend {
		t.Fatal("点了开关该开着")
	}
	tt.Frame()
	mustClick(t, tt, name)
	if r.Amend {
		t.Fatal("再点一下该关了")
	}

	// 没写信息：提交按钮灰着，点了不提交
	head := repo.Git("rev-parse", "HEAD")
	repo.Write("a.txt", "b\n")
	repo.Git("add", "a.txt")
	a.RefreshRepo(r, app.WhyManual)
	wait("暂存", func() bool { return idle(r)() && r.Status.Staged == 1 })
	r.CommitMsg = ""
	tt.Frame()
	mustClick(t, tt, i18n.Tf("提交 %s 个文件", "1"))
	if len(r.Queue) != 0 || r.Busy != "" || repo.Git("rev-parse", "HEAD") != head {
		t.Fatal("没写信息时灰着的提交按钮被点了")
	}

	// 前面排着一条慢命令，提交排在它后面：提交中，开关灰着、点了不切
	slowOp(a, r)
	r.CommitMsg = "改一下"
	a.Commit(r)
	tt.Frame()
	if !r.Committing() {
		t.Fatal("提交该在排队")
	}
	if _, ok := tt.Find(name); !ok {
		t.Fatalf("提交中开关也该在：%q", tt.Texts())
	}
	mustClick(t, tt, name)
	if r.Amend {
		t.Error("提交中点开关不该切到修改上一次提交")
	}
	a.CancelOp(r) // 慢命令取消了，排着的提交接着跑完
	wait("队列跑完", idle(r))
}
