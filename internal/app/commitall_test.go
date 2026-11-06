package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
)

// 什么都没暂存时提交全部：未跟踪的也进提交，信息清掉。
func TestCommitAll(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if !r.CommitsAll() || r.Status.Untracked == 0 {
		t.Fatalf("该提交全部：%+v", r.Status)
	}
	r.CommitMsg = "全部"
	if why := CommitWhy(r); why != "" {
		t.Fatalf("不该拦：%q", why)
	}
	a.Commit(r)
	wait("提交完", func() bool { return idle(r)() && r.Status.Count == 0 })
	if a.AlertOpen {
		t.Fatalf("提交全部报错：%q", a.AlertMsg)
	}
	if r.CommitMsg != "" {
		t.Errorf("提交后框里还有 %q", r.CommitMsg)
	}
	if got := testutil.GitOut(t, root, "log", "-1", "--format=%s"); got != "全部" {
		t.Errorf("最新提交 %q", got)
	}
}

// 暂存失败（index.lock 被占着）就不提交：信息留着，HEAD 不动。
func TestCommitAllStageFails(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	head := testutil.GitOut(t, root, "rev-parse", "HEAD")
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r.CommitMsg = "不该提交"
	a.Commit(r)
	wait("暂存失败", func() bool { return r.Busy == "" && len(r.Queue) == 0 && a.AlertOpen })
	os.Remove(lock)
	wait("读完状态", idle(r))
	if got := testutil.GitOut(t, root, "rev-parse", "HEAD"); got != head {
		t.Errorf("暂存失败还提交了：%s", got)
	}
	if r.CommitMsg != "不该提交" {
		t.Errorf("信息丢了：%q", r.CommitMsg)
	}
}

// 有 git 命令在跑、状态在读时不提交全部：那时候的改动数不算数。
func TestCommitAllBlocked(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	head := testutil.GitOut(t, root, "rev-parse", "HEAD")
	r.CommitMsg = "等等"
	r.Loading = true
	a.Commit(r)
	r.Loading = false
	if a.Toast != i18n.T("正在读状态，稍后再提交") {
		t.Errorf("读状态时的提示：%q", a.Toast)
	}
	r.running = &Op{Label: "别的命令"}
	if why := CommitWhy(r); why != i18n.T("有 git 命令在跑") {
		t.Errorf("跑命令时的原因：%q", why)
	}
	a.Commit(r)
	r.running = nil
	if a.Toast != i18n.T("有 git 命令在跑") {
		t.Errorf("跑命令时的提示：%q", a.Toast)
	}
	if got := testutil.GitOut(t, root, "rev-parse", "HEAD"); got != head || len(r.Queue) != 0 {
		t.Errorf("被拦的提交跑了：%s 队列 %d", got, len(r.Queue))
	}
}

// 有冲突、修改上一次提交、合并停在一半时都不走提交全部。
func TestCommitAllRefused(t *testing.T) {
	r := &Repo{}
	r.Status.Count, r.Status.Unstaged = 2, 2
	r.CommitMsg = "信息"
	if !r.CommitsAll() {
		t.Fatal("只有未暂存的改动该提交全部")
	}
	r.Amend = true
	if r.CommitsAll() {
		t.Error("修改上一次提交不暂存全部")
	}
	r.Amend = false
	r.Status.Conflicts = 1
	if r.CommitsAll() {
		t.Error("有冲突不暂存全部")
	}
	if why := CommitWhy(r); why != i18n.T("有冲突的文件，先解决") {
		t.Errorf("有冲突的原因：%q", why)
	}
	r.Status.Conflicts = 0
	r.Extra.Ongoing = git.Rebasing
	if r.CommitsAll() {
		t.Error("变基停在一半不暂存全部")
	}
}

// 提交并推送：自动推送关着也推；不能推时写原因，不提交。
func TestCommitPush(t *testing.T) {
	repo := testutil.NewRepo(t, "push")
	repo.Write("a.txt", "a\n")
	repo.Commit("第一次")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "-u", "origin", "main")
	repo.Write("a.txt", "b\n")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	a.St.Settings.AutoPush = false
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))

	if why := CommitPushWhy(r); why != i18n.T("还没写提交信息") {
		t.Errorf("没写信息的原因：%q", why)
	}
	r.CommitMsg = "推上去"
	r.Amend = true
	if why := CommitPushWhy(r); why != i18n.T("修改后的提交要强推") {
		t.Errorf("修改提交的原因：%q", why)
	}
	r.Amend = false
	behind := r.Status.Behind
	r.Status.Behind = 2
	if why := CommitPushWhy(r); !strings.Contains(why, "2") {
		t.Errorf("落后时的原因：%q", why)
	}
	r.Status.Behind = behind
	if why := CommitPushWhy(r); why != "" {
		t.Fatalf("不该拦：%q", why)
	}
	a.CommitPush(r)
	wait("推完", func() bool {
		return idle(r)() && r.Status.Count == 0 && repo.Git("rev-parse", "origin/main") == repo.Git("rev-parse", "HEAD")
	})
	if got := repo.Git("--git-dir", bare, "log", "-1", "--format=%s", "main"); got != "推上去" {
		t.Errorf("远程最新提交 %q", got)
	}
}
