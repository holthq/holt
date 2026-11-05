package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// dailyApp 在仓库上开应用、读好状态。
func dailyApp(t *testing.T, repo *testutil.Repo) (*App, *Repo, func(string, func() bool)) {
	t.Helper()
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	return a, r, wait
}

// refresh 重读状态（测试里在应用外面动过仓库之后）。
func refresh(a *App, r *Repo, wait func(string, func() bool)) {
	a.RefreshRepo(r, WhyManual)
	wait("重读状态", idle(r))
}

// branchNamed 是仓库里叫 name 的分支（本地或 origin/x）。
func branchNamed(t *testing.T, r *Repo, name string) git.Branch {
	t.Helper()
	for _, b := range r.Branches {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("没有分支 %s: %+v", name, r.Branches)
	return git.Branch{}
}

// commitNamed 是所有分支里标题是 subject 的提交（带 Parents）。
func commitNamed(t *testing.T, root, subject string) git.Commit {
	t.Helper()
	cs, err := git.Log(context.Background(), root, 0, 100, git.LogQuery{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Subject == subject {
			return c
		}
	}
	t.Fatalf("没有提交 %q", subject)
	return git.Commit{}
}

// confirm 等确认框出来（问话可能在后台数完提交才问），检查标题，点确认。
func confirm(t *testing.T, a *App, wait func(string, func() bool), title string) *ConfirmReq {
	t.Helper()
	wait("确认框", func() bool { return a.ConfirmOpen })
	c := a.Confirm
	if c.Title != title {
		t.Fatalf("问的是 %q，不是 %q（%s）", c.Title, title, c.Message)
	}
	a.ConfirmOpen, a.Confirm = false, nil
	c.Fn()
	return c
}

// bareRemote 建一个裸仓库当 origin，repo 推上去并设置上游。
func bareRemote(t *testing.T, repo *testutil.Repo) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", remote)
	repo.Git("remote", "add", "origin", remote)
	repo.Git("push", "-q", "-u", "origin", "main")
	return remote
}

// 合并：能快进的、两边都有提交的（生成合并提交）、冲突的（停在 Merging，交给横幅，不弹失败）；
// HEAD 分离、有进行中的操作时两个子菜单灰着。
func TestMergeBranch(t *testing.T) {
	repo := testutil.NewRepo(t, "merge")
	repo.Write("f.txt", "1\n2\n3\n")
	repo.Commit("base")
	repo.Git("switch", "-q", "-c", "ff")
	repo.Write("ff.txt", "ff\n")
	repo.Commit("ff one")
	repo.Git("switch", "-q", "-c", "side", "main")
	repo.Write("side.txt", "side\n")
	repo.Commit("side one")
	repo.Git("switch", "-q", "-c", "clash", "main")
	repo.Write("f.txt", "1\nC\n3\n")
	repo.Commit("clash one")
	repo.Git("switch", "-q", "main")
	a, r, wait := dailyApp(t, repo)
	if why := RefWhy(r); why != "" {
		t.Fatalf("在 main 上应能合并：%q", why)
	}

	a.Merge(r, branchNamed(t, r, "ff"))
	wait("合并 ff", idle(r))
	if a.Toast != "合并 ff完成" || a.AlertOpen {
		t.Fatalf("toast %q alert %v %q", a.Toast, a.AlertOpen, a.AlertMsg)
	}
	repo.Git("merge-base", "--is-ancestor", "ff", "HEAD")

	repo.Write("f.txt", "1\nM\n3\n")
	repo.Commit("main change")
	refresh(a, r, wait)
	a.Merge(r, branchNamed(t, r, "side"))
	wait("合并 side", idle(r))
	if parents := strings.Fields(repo.Git("log", "-1", "--format=%P")); len(parents) != 2 {
		t.Fatalf("两边都有提交应生成合并提交：父提交 %v", parents)
	}

	a.Merge(r, branchNamed(t, r, "clash"))
	wait("合并 clash", func() bool { return idle(r)() && r.Extra.Ongoing == git.Merging })
	if a.AlertOpen || !strings.Contains(a.Toast, "停在了冲突上") {
		t.Fatalf("冲突应交给横幅：toast %q alert %v %q", a.Toast, a.AlertOpen, a.AlertMsg)
	}
	if r.Status.Conflicts != 1 || OngoingTitle(r) == "" {
		t.Fatalf("冲突 %d 横幅 %q", r.Status.Conflicts, OngoingTitle(r))
	}
	if why := RefWhy(r); why != "先处理进行中的合并" {
		t.Fatalf("合并中应灰：%q", why)
	}
	repo.Git("merge", "--abort")
	repo.Git("switch", "-q", "--detach", "HEAD")
	refresh(a, r, wait)
	if why := RefWhy(r); why != "HEAD 不在分支上" {
		t.Fatalf("HEAD 分离应灰：%q", why)
	}
}

// 变基到：先在后台数提交、问（N 个重写，M 个推送过），带着 --autostash，未提交的改动做完放回；
// 冲突时停在 Rebasing，交给横幅。
func TestRebaseOnto(t *testing.T) {
	repo := testutil.NewRepo(t, "rebase")
	repo.Write("f.txt", "1\n2\n3\n")
	repo.Write("g.txt", "g\n")
	repo.Commit("base")
	bareRemote(t, repo)
	repo.Git("switch", "-q", "-c", "topic")
	repo.Write("t1.txt", "t1\n")
	repo.Commit("topic one")
	repo.Git("push", "-q", "-u", "origin", "topic")
	repo.Write("t2.txt", "t2\n")
	repo.Commit("topic two")
	repo.Git("switch", "-q", "main")
	repo.Write("m.txt", "m\n")
	repo.Commit("main change")
	repo.Git("switch", "-q", "topic")
	repo.Write("g.txt", "改了没提交\n")
	a, r, wait := dailyApp(t, repo)

	a.RebaseOnto(r, branchNamed(t, r, "main"))
	c := confirm(t, a, wait, "把 topic 变基到 main？")
	for _, s := range []string{"当前分支的 2 个提交会重写。", "其中 1 个已经推送过", "--autostash"} {
		if !strings.Contains(c.Message, s) {
			t.Errorf("问话里没有 %q：%s", s, c.Message)
		}
	}
	wait("变基到 main", idle(r))
	if a.Toast != "变基到 main完成" || a.AlertOpen {
		t.Fatalf("toast %q alert %v %q", a.Toast, a.AlertOpen, a.AlertMsg)
	}
	if log := repo.Git("log", "--format=%s"); log != "topic two\ntopic one\nmain change\nbase" {
		t.Fatalf("变基后的历史:\n%s", log)
	}
	if data, _ := os.ReadFile(filepath.Join(repo.Root, "g.txt")); string(data) != "改了没提交\n" {
		t.Fatalf("自动贮藏的改动没放回：%q", data)
	}
	if s := repo.Git("stash", "list"); s != "" {
		t.Fatalf("贮藏应已放回：%s", s)
	}

	// 已经在 main 上面了：不问、不跑，只提示
	a.RebaseOnto(r, branchNamed(t, r, "main"))
	wait("数提交", func() bool { return a.Toast == "topic 已经在 main 上面了，不用变基" })
	if a.ConfirmOpen || r.Busy != "" {
		t.Fatal("不用变基时不该问、不该跑")
	}
	// 当前分支没有自己的提交：问的是快进
	repo.Git("switch", "-q", "-c", "late", "main~1")
	refresh(a, r, wait)
	a.RebaseOnto(r, branchNamed(t, r, "main"))
	c = confirm(t, a, wait, "把 late 变基到 main？")
	if !strings.Contains(c.Message, "快进") {
		t.Errorf("没有要重写的提交：%s", c.Message)
	}
	wait("变基到 main", func() bool { return idle(r)() && r.Extra.Head == repo.Git("rev-parse", "main") })
	repo.Git("switch", "-q", "topic")

	repo.Git("switch", "-q", "main")
	repo.Write("f.txt", "1\nM\n3\n")
	repo.Commit("main clash")
	repo.Git("switch", "-q", "topic")
	repo.Write("f.txt", "1\nT\n3\n")
	repo.Commit("topic clash")
	refresh(a, r, wait)
	a.RebaseOnto(r, branchNamed(t, r, "main"))
	confirm(t, a, wait, "把 topic 变基到 main？")
	wait("变基停下", func() bool { return idle(r)() && r.Extra.Ongoing == git.Rebasing })
	if a.AlertOpen || !strings.Contains(a.Toast, "停在了冲突上") {
		t.Fatalf("冲突应交给横幅：toast %q alert %v %q", a.Toast, a.AlertOpen, a.AlertMsg)
	}
}

// 拣选不问；HEAD、合并提交灰着。还原先问，生成反过来的提交；合并提交灰着。
func TestCherryPickRevert(t *testing.T) {
	repo := testutil.NewRepo(t, "pick")
	repo.Write("a.txt", "a\n")
	repo.Commit("base")
	repo.Git("switch", "-q", "-c", "other")
	repo.Write("x.txt", "x\n")
	repo.Commit("add x")
	repo.Git("switch", "-q", "main")
	repo.Write("b.txt", "b\n")
	repo.Commit("add b")
	repo.Git("switch", "-q", "-c", "merged")
	repo.Git("merge", "-q", "--no-ff", "-m", "merge other", "other")
	repo.Git("switch", "-q", "main")
	a, r, wait := dailyApp(t, repo)

	head := commitNamed(t, repo.Root, "add b")
	if why := PickWhy(r, head); why != "就是 HEAD" {
		t.Errorf("HEAD 的拣选应灰：%q", why)
	}
	mc := commitNamed(t, repo.Root, "merge other")
	if why := PickWhy(r, mc); why != "合并提交" {
		t.Errorf("合并提交的拣选应灰：%q", why)
	}
	if why := RevertWhy(r, mc); why != "合并提交，到终端里 revert -m" {
		t.Errorf("合并提交的还原应灰：%q", why)
	}

	x := commitNamed(t, repo.Root, "add x")
	// 右栏读着所有分支的历史：HEAD 往前的灰着，别的分支上的能拣
	cs, err := git.Log(context.Background(), repo.Root, 0, 100, git.LogQuery{All: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Hist.Commits = cs
	if why := PickWhy(r, commitNamed(t, repo.Root, "base")); why != "HEAD 里已经有了" {
		t.Errorf("HEAD 往前的提交拣选应灰：%q", why)
	}
	if why := PickWhy(r, x); why != "" {
		t.Errorf("别的分支上的提交应能拣：%q", why)
	}
	a.CherryPick(r, x)
	if a.ConfirmOpen {
		t.Fatal("拣选不该问")
	}
	wait("拣选", idle(r))
	if a.Toast != "拣选 "+x.Short+"完成" {
		t.Fatalf("toast %q alert %q", a.Toast, a.AlertMsg)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "x.txt")); err != nil {
		t.Fatal("拣选后应有 x.txt")
	}

	b := commitNamed(t, repo.Root, "add b")
	a.Revert(r, b)
	if r.Busy != "" || len(r.Queue) != 0 {
		t.Fatal("还原没先问就跑了")
	}
	c := confirm(t, a, wait, "还原 "+b.Short+"？")
	if !strings.Contains(c.Message, "add b") {
		t.Errorf("问话 %q", c.Message)
	}
	wait("还原", idle(r))
	if got := repo.Git("log", "-1", "--format=%s"); got != `Revert "add b"` {
		t.Fatalf("还原的提交 %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("还原后 b.txt 应没了")
	}
}

// 重置三档都先问，问话带离开的提交数；硬重置带工作区里会丢的改动数（未跟踪的不算，也不动）、按钮换成"重置并丢弃改动"。
func TestResetModes(t *testing.T) {
	repo := testutil.NewRepo(t, "reset")
	repo.Write("a.txt", "1\n")
	repo.Commit("c1")
	repo.Write("a.txt", "2\n")
	repo.Commit("c2")
	repo.Write("a.txt", "3\n")
	repo.Commit("c3")
	tip := repo.Git("rev-parse", "HEAD")
	a, r, wait := dailyApp(t, repo)
	c1 := commitNamed(t, repo.Root, "c1")
	if why := ResetWhy(r, commitNamed(t, repo.Root, "c3")); why != "就是 HEAD" {
		t.Errorf("HEAD 的重置应灰：%q", why)
	}

	for _, tc := range []struct {
		mode             ResetMode
		name             string
		staged, unstaged int
	}{{ResetSoft, "软", 1, 0}, {ResetMixed, "混合", 0, 1}} {
		a.ResetTo(r, c1, tc.mode)
		c := confirm(t, a, wait, tc.name+"重置到 "+c1.Short+"？")
		if want := "HEAD 和分支 main 会退到 " + c1.Short + "，中间的 2 个提交只能从 reflog 找回。"; c.Message != want || c.Button != "重置" || c.Danger {
			t.Errorf("%v 问话 %q 按钮 %q 危险 %v", tc.mode, c.Message, c.Button, c.Danger)
		}
		wait("重置", idle(r))
		if r.Extra.Head != c1.Hash || r.Status.Staged != tc.staged || r.Status.Unstaged != tc.unstaged {
			t.Errorf("%v 后 HEAD %s 暂存 %d 未暂存 %d", tc.mode, git.Short(r.Extra.Head), r.Status.Staged, r.Status.Unstaged)
		}
		repo.Git("reset", "-q", "--hard", tip)
		refresh(a, r, wait)
	}

	repo.Write("a.txt", "乱改\n")
	repo.Write("new.txt", "未跟踪\n")
	refresh(a, r, wait)
	a.ResetTo(r, c1, ResetHard)
	c := confirm(t, a, wait, "硬重置到 "+c1.Short+"？")
	if !strings.Contains(c.Message, "工作区里 1 处未提交的改动也会丢，不能撤销。") || c.Button != "重置并丢弃改动" || !c.Danger {
		t.Errorf("硬重置问话 %q 按钮 %q 危险 %v", c.Message, c.Button, c.Danger)
	}
	wait("硬重置", idle(r))
	if data, _ := os.ReadFile(filepath.Join(repo.Root, "a.txt")); string(data) != "1\n" {
		t.Errorf("硬重置后 a.txt %q", data)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "new.txt")); err != nil {
		t.Error("未跟踪的文件不该动")
	}
}

// 撤销上次提交：没推送过不问；改动回到暂存区，信息回到空的提交框；框里有字时不覆盖、放进提交信息历史；
// 第一个提交灰着；推送过的先问。
func TestUndoCommit(t *testing.T) {
	repo := testutil.NewRepo(t, "undo")
	repo.Write("a.txt", "1\n")
	repo.Commit("first")
	a, r, wait := dailyApp(t, repo)
	if why := UndoWhy(r); why != "第一个提交，没有上一个" {
		t.Fatalf("第一个提交应灰：%q", why)
	}

	repo.Write("a.txt", "2\n")
	repo.Git("add", "a.txt")
	repo.Git("commit", "-q", "-m", "second\n\nbody line")
	refresh(a, r, wait)
	a.UndoCommit(r)
	if a.ConfirmOpen {
		t.Fatal("没推送过不该问")
	}
	wait("撤销", func() bool { return idle(r)() && r.CommitMsg != "" })
	if r.CommitMsg != "second\n\nbody line" || r.Status.Staged != 1 || !r.CommitOpen {
		t.Fatalf("信息 %q 暂存 %d 框开着 %v", r.CommitMsg, r.Status.Staged, r.CommitOpen)
	}
	if got := repo.Git("log", "--format=%s"); got != "first" {
		t.Fatalf("撤销后的历史 %q", got)
	}

	repo.Git("commit", "-q", "-m", "again")
	r.CommitMsg = "写了一半"
	refresh(a, r, wait)
	a.UndoCommit(r)
	wait("撤销", func() bool { return idle(r)() && r.Extra.HeadRoot })
	if r.CommitMsg != "写了一半" || a.St.CommitMsgs[0] != "again" || a.Toast != "信息已复制到提交信息历史（↑）" {
		t.Fatalf("有字时：框 %q 历史 %q toast %q", r.CommitMsg, a.St.CommitMsgs, a.Toast)
	}

	repo.Git("commit", "-q", "-m", "pushed")
	bareRemote(t, repo)
	refresh(a, r, wait)
	a.UndoCommit(r)
	confirm(t, a, wait, "撤销已经推送过的提交？")
	wait("撤销", func() bool { return idle(r)() && r.Extra.HeadRoot })
}

// 强制推送用 --force-with-lease：别人推过、本地没抓取时被拒绝（弹失败）；抓取之后再推成功。没上游灰着。
func TestPushForceLease(t *testing.T) {
	repo := testutil.NewRepo(t, "force")
	repo.Write("a.txt", "1\n")
	repo.Commit("base")
	if why := PushForceWhy(&Repo{}); why != "没有上游" {
		t.Fatalf("没上游应灰：%q", why)
	}
	remote := bareRemote(t, repo)
	other := testutil.NewRepo(t, "other")
	other.Git("remote", "add", "origin", remote)
	other.Git("pull", "-q", "origin", "main")
	other.Write("b.txt", "别人的\n")
	other.Commit("theirs")
	other.Git("push", "-q", "origin", "main")

	repo.Write("a.txt", "改写\n")
	repo.Git("commit", "-q", "-a", "--amend", "-m", "base rewritten")
	a, r, wait := dailyApp(t, repo)
	if why := PushForceWhy(r); why != "" {
		t.Fatalf("有上游应能强制推送：%q", why)
	}
	a.PushForce(r)
	c := confirm(t, a, wait, "强制推送 main → origin/main？")
	if !strings.Contains(c.Message, "--force-with-lease") {
		t.Errorf("问话 %q", c.Message)
	}
	wait("强制推送", idle(r))
	if !a.AlertOpen || a.AlertTitle != "强制推送失败" {
		t.Fatalf("没抓取时应被拒绝：alert %v %q", a.AlertOpen, a.AlertTitle)
	}
	if got := testutil.GitOut(t, remote, "log", "-1", "--format=%s", "main"); got != "theirs" {
		t.Fatalf("被拒绝了远程不该变：%q", got)
	}
	a.AlertOpen = false

	repo.Git("fetch", "-q", "origin")
	refresh(a, r, wait)
	a.PushForce(r)
	c = confirm(t, a, wait, "强制推送 main → origin/main？")
	if !strings.Contains(c.Message, "本地领先 1、落后 2") {
		t.Errorf("问话 %q", c.Message)
	}
	wait("强制推送", idle(r))
	if a.AlertOpen || a.Toast != "强制推送完成" {
		t.Fatalf("抓取后应成功：alert %q toast %q", a.AlertMsg, a.Toast)
	}
	if got := testutil.GitOut(t, remote, "rev-parse", "main"); got != repo.Git("rev-parse", "HEAD") {
		t.Fatal("远程应是改写后的提交")
	}
}

// 同步：拉取成功再推送；拉取失败（不能快进）就停，不推；领先落后都是 0 只提示；没上游和推送一样。
func TestSync(t *testing.T) {
	repo := testutil.NewRepo(t, "sync")
	repo.Write("a.txt", "1\n")
	repo.Commit("base")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare) // 有 origin、没上游：同步就是推送并设置上游
	a, r, wait := dailyApp(t, repo)
	a.Sync(r)
	if r.Busy != "推送（设置上游 origin）" && !strings.Contains(strings.Join(outputsFor(a, "sync"), "\n"), "push -u origin HEAD") {
		t.Fatalf("没上游时应和推送一样：busy %q", r.Busy)
	}
	wait("推送", idle(r))
	a.AlertOpen = false

	remote := bare
	refresh(a, r, wait)
	a.Sync(r)
	if a.Toast != "和 origin 一致" || r.Busy != "" || len(r.Queue) != 0 {
		t.Fatalf("一致时只提示：toast %q busy %q", a.Toast, r.Busy)
	}

	repo.Write("a.txt", "2\n")
	repo.Commit("mine")
	refresh(a, r, wait)
	a.Sync(r)
	wait("同步", func() bool { return idle(r)() && r.Status.Ahead == 0 })
	if got := testutil.GitOut(t, remote, "rev-parse", "main"); got != repo.Git("rev-parse", "HEAD") {
		t.Fatal("同步后远程应是本地的提交")
	}

	other := testutil.NewRepo(t, "other")
	other.Git("remote", "add", "origin", remote)
	other.Git("pull", "-q", "origin", "main")
	other.Write("b.txt", "别人的\n")
	other.Commit("theirs")
	other.Git("push", "-q", "origin", "main")
	repo.Write("a.txt", "3\n")
	repo.Commit("mine again")
	repo.Git("fetch", "-q", "origin")
	refresh(a, r, wait)
	before := len(a.Output)
	a.St.Settings.PullMode = "" // 只快进：两边都有提交时拉取失败
	a.Sync(r)
	wait("同步", idle(r))
	if !a.AlertOpen || a.AlertTitle != "拉取失败" {
		t.Fatalf("拉取应失败：alert %v %q", a.AlertOpen, a.AlertTitle)
	}
	for _, e := range a.Output[before:] {
		if strings.Contains(e.Title, "push") {
			t.Fatalf("拉取失败了不该推：%s", e.Title)
		}
	}
	if got := testutil.GitOut(t, remote, "log", "-1", "--format=%s", "main"); got != "theirs" {
		t.Fatalf("远程不该变：%q", got)
	}
}
