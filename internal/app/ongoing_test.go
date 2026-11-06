package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// ongoingApp 在仓库上开应用、读好状态，应停在 op 上。
func ongoingApp(t *testing.T, repo *testutil.Repo, op git.Ongoing) (*App, *Repo, func(string, func() bool)) {
	t.Helper()
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if r.Extra.Ongoing != op {
		t.Fatalf("应停在%v: %v", op, r.Extra.Ongoing)
	}
	return a, r, wait
}

// 变基停在第 1 / 2 步：有冲突时继续只提示、不排命令；解决、暂存后继续，变基做完，两个提交都在 main 上面。
func TestContinueRebase(t *testing.T) {
	repo := testutil.RebaseRepo(t, "rebase")
	a, r, wait := ongoingApp(t, repo, git.Rebasing)
	if got := OngoingTitle(r); got != "变基中（1 / 2）：topic one" {
		t.Errorf("标题 %q", got)
	}
	if got := OngoingNote(r); !strings.HasPrefix(got, "还有 1 个文件有冲突") {
		t.Errorf("小字 %q", got)
	}

	a.ContinueOngoing(r)
	if a.Toast != "还有 1 个文件有冲突" || r.Busy != "" || len(r.Queue) != 0 {
		t.Fatalf("有冲突时不该跑: toast %q busy %q queue %d", a.Toast, r.Busy, len(r.Queue))
	}

	repo.Write("f.txt", "1\nTM\n3\n")
	repo.Git("add", "f.txt")
	a.RefreshRepo(r, WhyManual)
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	if got := OngoingNote(r); got != "冲突都解决了，可以继续" {
		t.Errorf("解决后小字 %q", got)
	}

	a.ContinueOngoing(r)
	if r.Busy != "继续变基" {
		t.Fatalf("Busy = %q", r.Busy)
	}
	wait("继续变基", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	if a.Toast != "继续变基完成" {
		t.Errorf("toast %q", a.Toast)
	}
	if log := repo.Git("log", "--format=%s"); log != "topic two\ntopic one\nmain change\nbase" {
		t.Errorf("变基后的历史:\n%s", log)
	}
	if OngoingTitle(r) != "" {
		t.Errorf("做完了还有标题 %q", OngoingTitle(r))
	}
}

// 变基两步都冲突：继续第一步之后又停在第二步，提示换一句，横幅变成第 2 / 2 步。
func TestContinueStopsAgain(t *testing.T) {
	repo := testutil.NewRepo(t, "again")
	repo.Write("f.txt", "1\n2\n3\n")
	repo.Commit("base")
	repo.Git("switch", "-q", "-c", "topic")
	repo.Write("f.txt", "1\nT\n3\n")
	repo.Commit("topic one")
	repo.Write("f.txt", "1\nT2\n3\n")
	repo.Commit("topic two")
	repo.Git("switch", "-q", "main")
	repo.Write("f.txt", "1\nM\n3\n")
	repo.Commit("main change")
	repo.Git("switch", "-q", "topic")
	repo.GitFails("rebase", "main")
	a, r, wait := ongoingApp(t, repo, git.Rebasing)

	repo.Write("f.txt", "1\nMT\n3\n") // 和 topic one 不一样，第二步改 T 那行就又冲突
	repo.Git("add", "f.txt")
	a.RefreshRepo(r, WhyManual)
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	a.ContinueOngoing(r)
	wait("继续变基", func() bool { return idle(r)() && r.Status.Conflicts == 1 })
	if a.Toast != "继续了，又停在下一处冲突" || r.Extra.Ongoing != git.Rebasing {
		t.Errorf("toast %q，%v", a.Toast, r.Extra.Ongoing)
	}
	if got := OngoingTitle(r); got != "变基中（2 / 2）：topic two" {
		t.Errorf("标题 %q", got)
	}
}

// 跳过先问；确认后变基跳过 topic one（它的改动不进结果），做完 topic two。
func TestSkipRebase(t *testing.T) {
	repo := testutil.RebaseRepo(t, "skip")
	a, r, wait := ongoingApp(t, repo, git.Rebasing)

	a.SkipOngoing(r)
	if !a.ConfirmOpen || a.Confirm == nil || !strings.Contains(a.Confirm.Title, "跳过") ||
		!strings.Contains(a.Confirm.Message, "topic one") || a.Confirm.Button != "跳过这个提交" {
		t.Fatalf("跳过应先问: %+v", a.Confirm)
	}
	if r.Busy != "" || len(r.Queue) != 0 {
		t.Fatal("问的时候就跑了")
	}
	a.Confirm.Fn()
	wait("跳过", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	if a.Toast != "跳过这个提交完成" {
		t.Errorf("toast %q", a.Toast)
	}
	if log := repo.Git("log", "--format=%s"); log != "topic two\nmain change\nbase" {
		t.Errorf("跳过后的历史:\n%s", log)
	}
	if f := repo.Git("show", "HEAD:f.txt"); f != "1\nM\n3" {
		t.Errorf("f.txt 应是 main 的: %q", f)
	}
}

// 合并：解决、暂存后继续，出一个两个父提交的合并提交，用的是 MERGE_MSG；合并没有跳过。
func TestContinueMerge(t *testing.T) {
	repo := testutil.ConflictRepo(t, "merge", "c.txt", "1\n2\n", "1\n我的\n", "1\n他们的\n")
	a, r, wait := ongoingApp(t, repo, git.Merging)
	if got := OngoingTitle(r); got != "合并中：Merge branch 'feature'" {
		t.Errorf("标题 %q", got)
	}
	a.SkipOngoing(r)
	if a.ConfirmOpen {
		t.Fatal("合并不该有跳过")
	}

	repo.Write("c.txt", "1\n我的\n")
	repo.Git("add", "c.txt")
	a.RefreshRepo(r, WhyManual)
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	if got := OngoingNote(r); got != "冲突都解决了：点继续，或在提交框里写信息提交" {
		t.Errorf("小字 %q", got)
	}
	a.ContinueOngoing(r)
	wait("继续合并", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	if parents := strings.Fields(repo.Git("rev-list", "--parents", "-n1", "HEAD")); len(parents) != 3 {
		t.Errorf("应有两个父提交: %v", parents)
	}
	if subj := repo.Git("log", "-1", "--format=%s"); subj != "Merge branch 'feature'" {
		t.Errorf("合并提交信息 %q", subj)
	}
}

// "继续"失败、git 说这个提交没有改动了：补一句用"跳过"；合并和别的失败不补。
func TestSkipHint(t *testing.T) {
	empty := errors.New("The previous cherry-pick is now empty, possibly due to conflict resolution.")
	if err := skipHint([]string{"cherry-pick", "--continue"}, empty); !strings.Contains(err.Error(), "用“跳过”") {
		t.Errorf("拣选空了: %v", err)
	}
	if err := skipHint([]string{"rebase", "--continue"}, errors.New("No changes - did you forget to use 'git add'?")); !strings.Contains(err.Error(), "用“跳过”") {
		t.Errorf("变基空了: %v", err)
	}
	if err := skipHint([]string{"merge", "--continue"}, errors.New("nothing to commit")); err.Error() != "nothing to commit" {
		t.Errorf("合并不该补: %v", err)
	}
	if err := skipHint([]string{"rebase", "--continue"}, errors.New("error: could not lock")); err.Error() != "error: could not lock" {
		t.Errorf("别的失败不该补: %v", err)
	}
	if err := skipHint([]string{"push"}, empty); err != empty {
		t.Errorf("别的命令不该补: %v", err)
	}
}
