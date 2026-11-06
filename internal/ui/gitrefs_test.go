package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 分支和标签的 Git 日常操作在界面上的入口（临时仓库 + 临时裸远程）：分支下拉底下的改名 / 发布 / 删除分支 / 删除远程分支、
// 标签子菜单底下的新建 / 推送 / 删除标签、历史右键"在这里打标签…"、仓库右键"远程"里的改名 / 删除 / 添加。
func TestGitRefsMenus(t *testing.T) {
	repo := testutil.NewRepo(t, "refs")
	repo.Write("a.txt", "a\n")
	repo.Commit("base")
	repo.Git("branch", "feature")
	repo.Write("b.txt", "b\n")
	repo.Commit("main two")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "-u", "origin", "main")
	repo.Git("push", "-q", "origin", "feature")
	other := filepath.Join(t.TempDir(), "other.git")
	repo.Git("init", "-q", "--bare", "-b", "main", other)
	repo.Git("push", "-q", other, "feature:x")
	remoteRefs := func(prefix string) []string {
		return strings.Fields(testutil.GitOut(t, bare, "for-each-ref", "--format=%(refname)", prefix))
	}

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.UseRepo(r)
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("状态", idle(r))
	tt.Frame()
	hasTag := func(name string) bool {
		return slices.ContainsFunc(r.Tags, func(x git.Tag) bool { return x.Name == name })
	}
	cur := func() string { b, _ := app.CurrentBranch(r); return b.Name }

	// 分支下拉底下：新建分支、改名、删除分支、删除远程分支（有上游，没有"发布"）
	mustClickRow(t, tt, "分支 main")
	menu := shownMenu(t, tt)
	i := slices.Index(menu, "新建分支…")
	if i < 0 || !slices.Equal(menu[i:], []string{"新建分支…", "重命名当前分支…", "删除分支", "删除远程分支"}) {
		t.Fatalf("分支菜单: %q", menu)
	}
	if err := chooseMenu(t, tt, "删除分支", "main"); err == nil {
		t.Error("删除分支里不该有当前分支")
	}
	if err := chooseMenu(t, tt, "删除分支", "feature"); err != nil {
		t.Fatal(err)
	}
	wait("删分支的问话", func() bool { return a.ConfirmOpen })
	if c := a.Confirm; c.Title != "删除分支 feature？" || !strings.Contains(c.Message, "都已经在当前分支里了") {
		t.Fatalf("删分支的问话: %+v", c)
	}
	mustClickRow(t, tt, "删除分支")
	wait("删 feature", func() bool {
		return idle(r)() && !slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "feature" })
	})

	// 新建标签（打在 HEAD）→ 推送 → 删除时勾"同时删除 origin 上的"
	mustClickRow(t, tt, "分支 main")
	if err := chooseMenu(t, tt, "标签", "新建标签…"); err != nil {
		t.Fatalf("新建标签: %v；菜单=%q", err, shownMenu(t, tt))
	}
	tt.Frame()
	if !tt.HasText("打在 main 的 HEAD 上；写了信息是附注标签，不写是轻量标签。") || !tt.Focused("标签名") {
		t.Fatalf("新建标签对话框: %q", tt.Texts())
	}
	tt.Type("v2")
	tt.Frame()
	if !tt.HasText("git tag v2") {
		t.Errorf("没有命令预览：%q", tt.Texts())
	}
	mustClickRow(t, tt, "新建")
	wait("打标签", func() bool { return idle(r)() && hasTag("v2") })
	mustClickRow(t, tt, "分支 main")
	if err := chooseMenu(t, tt, "标签", "推送标签到 origin", "v2"); err != nil {
		t.Fatalf("推送标签: %v；菜单=%q", err, shownMenu(t, tt))
	}
	wait("推送标签", func() bool { return idle(r)() && slices.Equal(remoteRefs("refs/tags"), []string{"refs/tags/v2"}) })
	mustClickRow(t, tt, "分支 main")
	if err := chooseMenu(t, tt, "标签", "删除标签", "v2"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustClickRow(t, tt, "同时删除 origin 上的")
	if !a.RefDlg.Also || !tt.HasText("git tag -d v2 && git push origin --delete refs/tags/v2") {
		t.Fatalf("删标签对话框: %+v %q", a.RefDlg, tt.Texts())
	}
	mustClickRow(t, tt, "删除标签")
	wait("删标签", func() bool { return idle(r)() && !hasTag("v2") && len(remoteRefs("refs/tags")) == 0 })

	// 改名：上游取消，菜单里多了"发布到 origin"，点了在远程上新建 trunk
	mustClickRow(t, tt, "分支 main")
	if err := chooseMenu(t, tt, "重命名当前分支…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("trunk")
	tt.Key(0, wui.KeyEnter)
	wait("改名", func() bool { return idle(r)() && cur() == "trunk" && app.Upstream(r) == "" })
	tt.Frame()
	mustClickRow(t, tt, "分支 trunk")
	if err := chooseMenu(t, tt, "发布到 origin（推送并设置上游）"); err != nil {
		t.Fatalf("发布: %v；菜单=%q", err, shownMenu(t, tt))
	}
	wait("发布", func() bool { return idle(r)() && app.Upstream(r) == "origin/trunk" })
	if got := remoteRefs("refs/heads"); !slices.Equal(got, []string{"refs/heads/feature", "refs/heads/main", "refs/heads/trunk"}) {
		t.Errorf("远程分支 %q", got)
	}

	// 删除远程分支 origin/feature（本地的 feature 前面删了）；origin/main 是裸远程的 HEAD，git 会拒绝删
	mustClickRow(t, tt, "分支 trunk")
	if err := chooseMenu(t, tt, "删除远程分支", "origin/feature"); err != nil {
		t.Fatal(err)
	}
	wait("删远程分支的问话", func() bool { return a.ConfirmOpen })
	mustClickRow(t, tt, "删除远程分支")
	wait("删远程分支", func() bool {
		return idle(r)() && slices.Equal(remoteRefs("refs/heads"), []string{"refs/heads/main", "refs/heads/trunk"})
	})

	// 历史右键"在这里打标签…"：打在那个提交上
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	a.LoadHistory(r, true)
	h := &r.Hist
	wait("历史", func() bool { return h.Loaded && !h.Loading && len(h.Commits) == 2 })
	tt.Frame()
	base := h.Commits[1]
	mustRightClickRow(t, tt, "base", "在这里打标签…")
	if !tt.HasText("打在 提交 " + base.Short + " 上；写了信息是附注标签，不写是轻量标签。") {
		t.Fatalf("历史里打标签: %q", tt.Texts())
	}
	tt.Type("v0")
	tt.Key(0, wui.KeyEnter)
	wait("打在 base 上", func() bool { return idle(r)() && hasTag("v0") })
	if got := repo.Git("rev-parse", "v0"); got != base.Hash {
		t.Errorf("v0 打在 %s，不是 %s", got, base.Hash)
	}

	// 仓库右键"远程"：添加（接着抓取）→ 删除
	mustRightClickRow(t, tt, "refs（仓库）", "更多", "远程", "添加远程…")
	if !tt.Focused("远程名") {
		t.Fatalf("添加远程的焦点不在名字框：%q", tt.Texts())
	}
	tt.Type("up")
	tt.Key(0, wui.KeyTab)
	tt.Type(other)
	tt.Key(0, wui.KeyEnter)
	wait("添加并抓取", func() bool {
		return idle(r)() && app.HasRemote(r, "up") && slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "up/x" })
	})
	mustRightClickRow(t, tt, "refs（仓库）", "更多", "远程", "删除 up…")
	wait("删远程的问话", func() bool { return a.ConfirmOpen })
	if c := a.Confirm; c.Title != "删除远程 up？" || !strings.Contains(c.Message, "本地的 1 个远程分支引用（up/…）会一起删") {
		t.Fatalf("删远程的问话: %+v", c)
	}
	mustClickRow(t, tt, "删除远程")
	wait("删远程", func() bool { return idle(r)() && !app.HasRemote(r, "up") })
}
