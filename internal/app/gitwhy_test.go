package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// Git 操作不能做的原因：每种操作几个临时仓库的状态，断言报的那一条（几条同时成立时只报最根本的）。
func TestGitWhy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo *testutil.Repo)
		why   func(r *Repo) string
		want  string
	}{
		{"推送：没有上游也没有 origin", nil, PushWhy, "没有上游，也没有 origin"},
		{"推送：HEAD 分离比没有 origin 根本", func(t *testing.T, repo *testutil.Repo) {
			repo.Git("switch", "-q", "--detach")
		}, PushWhy, "HEAD 不在分支上"},
		{"推送：没有上游、有 origin 时能做（推送并设置上游）", func(t *testing.T, repo *testutil.Repo) {
			repo.Git("remote", "add", "origin", filepath.Join(t.TempDir(), "x.git"))
		}, PushWhy, ""},
		{"推送：和 origin 一致", func(t *testing.T, repo *testutil.Repo) { bareRemote(t, repo) }, PushWhy, "和 origin 一致"},
		{"推送：领先时能做", func(t *testing.T, repo *testutil.Repo) {
			bareRemote(t, repo)
			repo.Write("n.txt", "n\n")
			repo.Commit("ahead")
		}, PushWhy, ""},
		{"推送：分叉", func(t *testing.T, repo *testutil.Repo) {
			remote := bareRemote(t, repo)
			other := repo.In(filepath.Join(t.TempDir(), "other"))
			repo.Git("clone", "-q", remote, other.Root)
			other.Git("config", "user.name", "测试")
			other.Git("config", "user.email", "t@example.com")
			other.Write("o.txt", "o\n")
			other.Git("add", "o.txt")
			other.Git("commit", "-q", "-m", "theirs")
			other.Git("push", "-q")
			repo.Write("n.txt", "n\n")
			repo.Commit("ours")
			repo.Git("fetch", "-q")
		}, PushWhy, "和 origin 分叉，先拉取或强制推送"},
		{"拉取：没有上游", nil, PullWhy, "没有上游"},
		{"拉取：进行中的合并比没有上游根本", mergeConflict, PullWhy, "先处理进行中的合并"},
		{"强制推送：没有上游", nil, PushForceWhy, "没有上游"},
		{"强制推送：和 origin 一致", func(t *testing.T, repo *testutil.Repo) { bareRemote(t, repo) }, PushForceWhy, "和 origin 一致"},
		{"同步：和 origin 一致", func(t *testing.T, repo *testutil.Repo) { bareRemote(t, repo) }, SyncWhy, "和 origin 一致"},
		{"抓取：没有远程", nil, FetchWhy, "没有远程"},
		{"暂存全部：没有改动", nil, StageAllWhy, "没有要暂存的改动"},
		{"取消全部暂存：没有已暂存的", nil, UnstageAllWhy, "没有已暂存的改动"},
		{"丢弃：没有未暂存的", nil, DiscardAllWhy, "没有未暂存的改动"},
		{"贮藏：没有改动", nil, StashWhy, "没有可贮藏的改动"},
		{"贮藏：有进行中的合并（比有没有改动根本）", mergeConflict, StashWhy, "先处理进行中的合并"},
		{"弹出贮藏：没有贮藏", nil, StashPopWhy, "没有贮藏"},
		{"合并子菜单：进行中的合并比 HEAD 分离根本", mergeConflict, RefWhy, "先处理进行中的合并"},
		{"重命名：HEAD 分离", func(t *testing.T, repo *testutil.Repo) {
			repo.Git("switch", "-q", "--detach")
		}, RenameBranchWhy, "HEAD 不在分支上"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := testutil.NewRepo(t, "why")
			repo.Write("f.txt", "1\n")
			repo.Commit("base")
			if c.setup != nil {
				c.setup(t, repo)
			}
			_, r, _ := dailyApp(t, repo)
			if got := c.why(r); got != c.want {
				t.Errorf("原因是 %q，要 %q", got, c.want)
			}
		})
	}
}

// mergeConflict 让仓库停在一个有冲突的合并上。
func mergeConflict(t *testing.T, repo *testutil.Repo) {
	repo.Git("switch", "-q", "-c", "side")
	repo.Write("f.txt", "side\n")
	repo.Commit("side")
	repo.Git("switch", "-q", "main")
	repo.Write("f.txt", "main\n")
	repo.Commit("main")
	repo.GitFails("merge", "side")
}

// 合并到当前分支：就是当前分支 → 已经合并 → 有进行中的操作，按这个顺序只报第一条。
func TestMergeWhy(t *testing.T) {
	repo := testutil.NewRepo(t, "mergewhy")
	repo.Write("f.txt", "1\n")
	repo.Commit("base")
	repo.Git("branch", "old") // 指着 base，已经在 main 里
	repo.Git("switch", "-q", "-c", "side")
	repo.Write("s.txt", "s\n")
	repo.Commit("side")
	repo.Git("switch", "-q", "main")
	repo.Write("m.txt", "m\n")
	repo.Commit("main")
	a, r, wait := dailyApp(t, repo)
	cs, err := git.Log(context.Background(), repo.Root, 0, 100, git.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	r.Hist.Commits = cs
	if got := MergeWhy(r, branchNamed(t, r, "main")); got != "就是当前分支" {
		t.Errorf("当前分支：%q", got)
	}
	if got := MergeWhy(r, branchNamed(t, r, "old")); got != "已经合并" {
		t.Errorf("已合并的分支：%q", got)
	}
	if got := MergeWhy(r, branchNamed(t, r, "side")); got != "" {
		t.Errorf("没合并的分支应能合并：%q", got)
	}
	repo.Write("s.txt", "冲突\n")
	repo.Git("add", "s.txt")
	repo.Commit("conflict")
	repo.GitFails("merge", "side")
	refresh(a, r, wait)
	if cs, err = git.Log(context.Background(), repo.Root, 0, 100, git.LogQuery{}); err != nil {
		t.Fatal(err)
	}
	r.Hist.Commits = cs
	if got := MergeWhy(r, branchNamed(t, r, "old")); got != "已经合并" {
		t.Errorf("已经合并比进行中的合并根本：%q", got)
	}
	if got := MergeWhy(r, branchNamed(t, r, "side")); got != "先处理进行中的合并" {
		t.Errorf("进行中的合并：%q", got)
	}
}

// 删除工作树：主工作树 → 目录没了 → 有 git 命令在跑。
func TestRemoveWorktreeWhy(t *testing.T) {
	r := &Repo{}
	for _, c := range []struct {
		w    git.Worktree
		want string
	}{
		{git.Worktree{Main: true, Prunable: true, Locked: true}, "主工作树不能删"},
		{git.Worktree{Prunable: true}, "目录已经没了，用“清理”"},
		{git.Worktree{Locked: true}, ""}, // 锁着的不灰：git 不肯时再问要不要强制
	} {
		if got := RemoveWorktreeWhy(r, Worktree{Worktree: c.w}); got != c.want {
			t.Errorf("%+v：%q，要 %q", c.w, got, c.want)
		}
	}
	r.running = &Op{Label: "抓取"}
	if got := RemoveWorktreeWhy(r, Worktree{}); got != "有 git 命令在跑" {
		t.Errorf("有命令在跑：%q", got)
	}
}

// 点时再查：原因成立时动作不执行、提示原因；命令面板 / 菜单栏走 CanRun、WhyNot。
func TestBlockedAtClick(t *testing.T) {
	repo := testutil.NewRepo(t, "blocked")
	repo.Write("f.txt", "1\n")
	repo.Commit("base")
	bareRemote(t, repo)
	a, r, _ := dailyApp(t, repo)
	a.Push(r)
	if len(r.Queue) > 0 || r.Running() != nil {
		t.Fatal("和 origin 一致时推送不该排进队列")
	}
	if a.Toast != "和 origin 一致" {
		t.Errorf("提示：%q", a.Toast)
	}
	c := Find("git.push")
	if a.CanRun(c) || a.WhyNot(c) != "和 origin 一致" {
		t.Errorf("命令 git.push：能用 %v，原因 %q", a.CanRun(c), a.WhyNot(c))
	}
	a.Toast = ""
	a.run(c)
	if a.Toast != "和 origin 一致" {
		t.Errorf("菜单栏点了不能做的命令应提示原因：%q", a.Toast)
	}
}
