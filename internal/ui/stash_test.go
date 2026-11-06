package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 临时仓库上走一遍 Git 栏的贮藏列表：段头的条数、悬停的"应用"（贮藏留着）、右键"弹出贮藏"、
// "丢弃这条贮藏…"先问、问完发现序号挪了就不丢；还有 origin 在 GitHub 上时的"在 GitHub 打开"（仓库、文件、提交）。
func TestStashAndWeb(t *testing.T) {
	repo := testutil.NewRepo(t, "stash")
	repo.Write("a.txt", "原来\n")
	repo.Commit("初始提交")
	repo.Git("remote", "add", "origin", "git@github.com:me/stash-demo.git") // 假的地址：只用来推网页，不连网
	for _, s := range []string{"一", "二"} {
		repo.Write("a.txt", s+"\n")
		repo.Git("stash", "push", "-q", "-m", "第"+s+"个")
	}
	ws := testutil.Workspace(t, repo.Root)

	a, tt, wait := newUI(t, ws, "")
	showGroups(a)
	var opened []string
	a.OpenURL = func(u string) error { opened = append(opened, u); return nil }
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if n := len(r.Extra.Stashes); n != 2 || r.Extra.Stashes[0].Message != "On main: 第二个" || r.Extra.Web.Site() != "GitHub" {
		t.Fatalf("贮藏 / 网页没读对: %+v", r.Extra)
	}
	// 干净的仓库段默认收着，段头上有条数；展开后贮藏组也默认收着（标题上有条数），点开列出两条（只显示用户写的说明）
	if _, ok := tt.Find("2 条贮藏"); !ok {
		t.Fatalf("段头没有贮藏条数: %q", tt.Texts())
	}
	a.ToggleGitRepo(r)
	tt.Frame()
	if tt.HasText("第二个") || !shows(tt, "贮藏") {
		t.Fatalf("贮藏组应默认收着、标题上有条数: %q", tt.Texts())
	}
	mustClickRow(t, tt, "展开 贮藏")
	mustText(t, tt, "第二个")
	mustText(t, tt, "第一个")
	read := func() string { b, _ := os.ReadFile(filepath.Join(repo.Root, "a.txt")); return string(b) }
	hover := func(s string) {
		t.Helper()
		box, ok := tt.Find(s)
		if !ok {
			t.Fatalf("没有 %q: %q", s, tt.Texts())
		}
		tt.Move(box.X+box.W/2, box.Y+box.H/2)
		tt.Frame()
	}

	// 悬停的"应用"：改动放回工作区，贮藏还在
	hover("第一个")
	mustClickRow(t, tt, "应用改动")
	wait("应用贮藏", func() bool { return idle(r)() && r.Status.Count == 1 })
	if read() != "一\n" || len(r.Extra.Stashes) != 2 {
		t.Fatalf("应用后 a.txt=%q，贮藏 %d 条", read(), len(r.Extra.Stashes))
	}
	if !slices.Contains(outputTitles(a), "$ git stash apply -q stash@{1}") {
		t.Errorf("输出栏没记这条命令: %q", outputTitles(a))
	}
	repo.Git("checkout", "--", ".")

	// 右键"弹出贮藏"：放回后这条贮藏没了
	a.RefreshRepo(r, app.WhyManual)
	wait("干净", func() bool { return idle(r)() && r.Status.Count == 0 })
	mustRightClickRow(t, tt, "第二个", "弹出贮藏")
	wait("弹出贮藏", func() bool { return idle(r)() && len(r.Extra.Stashes) == 1 })
	if read() != "二\n" || r.Extra.Stashes[0].Message != "On main: 第一个" {
		t.Fatalf("弹出后 a.txt=%q，贮藏 %+v", read(), r.Extra.Stashes)
	}
	repo.Git("checkout", "--", ".")
	a.RefreshRepo(r, app.WhyManual)
	wait("干净", func() bool { return idle(r)() && r.Status.Count == 0 })

	// 丢弃先问，取消不动；问完 stash@{0} 已经不是它了就不丢
	hover("第一个")
	mustClickRow(t, tt, "丢弃这条贮藏…")
	mustText(t, tt, "丢弃贮藏 stash@{0}？")
	mustClickRow(t, tt, "取消")
	if n := len(strings.Fields(repo.Git("stash", "list", "--format=%H"))); n != 1 {
		t.Fatalf("取消后应还有 1 条贮藏，有 %d 条", n)
	}
	moved := r.Extra.Stashes[0]
	moved.Hash = strings.Repeat("0", 40)
	a.StashDrop(r, moved)
	tt.Frame()
	mustClickRow(t, tt, "丢弃")
	wait("核对", func() bool {
		return idle(r)() && a.AlertOpen && a.AlertMsg == "贮藏列表刚变过，看一眼再丢"
	})
	mustClickRow(t, tt, "好")
	if n := len(strings.Fields(repo.Git("stash", "list", "--format=%H"))); n != 1 {
		t.Fatalf("序号对不上时不该丢，还剩 %d 条", n)
	}
	mustRightClickRow(t, tt, "第一个", "丢弃贮藏…")
	mustClickRow(t, tt, "丢弃")
	wait("丢弃贮藏", func() bool { return idle(r)() && len(r.Extra.Stashes) == 0 })
	if out := repo.Git("stash", "list"); out != "" {
		t.Errorf("丢弃后还有贮藏: %s", out)
	}
	if !slices.Contains(outputTitles(a), "$ git stash drop -q stash@{0}") {
		t.Errorf("输出栏没记这条命令: %q", outputTitles(a))
	}

	// 在 GitHub 打开：仓库；HEAD 还没推送时文件看默认分支上的、提交不能开；"推送"后钉在 HEAD 上
	mustRightClickRow(t, tt, "stash（仓库）", "更多", "在 GitHub 打开")
	head := repo.Git("rev-parse", "HEAD")
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 1 })
	if err := rightClickRow(tt, "初始提交"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(t, tt, "在 GitHub 打开"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("没推送的提交不该能在 GitHub 打开: %v", err)
	}
	closeMenu(t, tt)
	repo.Git("update-ref", "refs/remotes/origin/main", "HEAD") // 当它推送过了（只改本地的远程分支引用）
	a.RefreshRepo(r, app.WhyManual)
	wait("推送后的状态", func() bool { return idle(r)() && r.Extra.HeadPushed() })
	a.ShowSidebar(workspace.ModeFiles)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("a.txt") })
	mustRightClickRow(t, tt, "a.txt", "在 GitHub 打开")
	mustClickRow(t, tt, "初始提交")
	wait("展开", func() bool { return r.Hist.Expanded != "" && !r.Hist.FilesLoading })
	mustClickRow(t, tt, "在 GitHub 打开")
	want := []string{"https://github.com/me/stash-demo", "https://github.com/me/stash-demo/blob/" + head + "/a.txt",
		"https://github.com/me/stash-demo/commit/" + head}
	if !slices.Equal(opened, want) {
		t.Errorf("打开的网页 %q，应为 %q", opened, want)
	}
}

// 设置里的"在 Git 栏显示工作树 / 贮藏"默认都关：段头没有贮藏数、工作树数，展开后没有贮藏组、工作树组，
// 侧栏顶上没有"需要你"条；干净但有工作树的仓库不自动展开。打开开关后都回来。全在临时仓库上。
func TestGitGroupsHiddenByDefault(t *testing.T) {
	repo := testutil.NewRepo(t, "proj")
	repo.Write("a.txt", "原来\n")
	repo.Commit("初始提交")
	feat := filepath.Join(filepath.Dir(repo.Root), "proj-feat")
	repo.Git("worktree", "add", "-q", "-b", "wt/feat", feat)
	repo.Git("-C", feat, "commit", "-q", "--allow-empty", "-m", "工作树里的提交") // 领先 1：开着时是"可合并"、进"需要你"条
	repo.Write("a.txt", "改\n")
	repo.Git("stash", "push", "-q", "-m", "一条")

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", func() bool { return idle(r)() && len(r.Worktrees) == 1 && len(r.Extra.Stashes) == 1 })
	tt.Frame()
	if r.GitOpen() {
		t.Error("干净、只有工作树的仓库不该自动展开")
	}
	for _, s := range []string{"1 条贮藏", "1 个工作树", "需要你"} {
		if shows(tt, s) {
			t.Errorf("关着时不该有 %q", s)
		}
	}
	a.ToggleGitRepo(r)
	tt.Frame()
	for _, s := range []string{"贮藏", "工作树"} {
		if shows(tt, s) {
			t.Errorf("展开后关着时也不该有 %q 组：%q", s, tt.Texts())
		}
	}
	r.GitFold = app.FoldAuto // 回到自动：打开开关后有工作树就自动展开

	showGroups(a)
	wait("贮藏组、工作树组回来", func() bool { return r.GitOpen() && shows(tt, "贮藏") && shows(tt, "工作树") })
}
