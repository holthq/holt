package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// refsRepo 是 base → main two（已推送到临时裸远程，有上游），feature 从 base 分出、有一个自己的提交。
func refsRepo(t *testing.T) (*testutil.Repo, string) {
	t.Helper()
	repo := testutil.NewRepo(t, "refs")
	repo.Write("a.txt", "a\n")
	repo.Commit("base")
	repo.Git("switch", "-q", "-c", "feature")
	repo.Write("f.txt", "f\n")
	repo.Commit("feature one")
	repo.Git("switch", "-q", "main")
	repo.Write("b.txt", "b\n")
	repo.Commit("main two")
	return repo, bareRemote(t, repo)
}

// refsIn 是裸仓库里 prefix 下的引用名（refs/heads/main 之类），一行一个。
func refsIn(t *testing.T, bare, prefix string) []string {
	t.Helper()
	return strings.Fields(testutil.GitOut(t, bare, "for-each-ref", "--format=%(refname)", prefix))
}

func TestSplitRemote(t *testing.T) {
	r := &Repo{}
	r.Extra.Remotes = []git.Remote{{Name: "origin"}, {Name: "me/fork"}}
	for _, tc := range []struct{ in, remote, branch string }{
		{"origin/feat/y", "origin", "feat/y"},
		{"me/fork/x", "me/fork", "x"},
		{"z/w", "z", "w"},
	} {
		if rm, b := splitRemote(r, tc.in); rm != tc.remote || b != tc.branch {
			t.Errorf("splitRemote(%q) = %q %q", tc.in, rm, b)
		}
	}
	if TagRemote(r) != "origin" || TagRemote(&Repo{}) != "" {
		t.Error("TagRemote")
	}
}

// 改名：有上游时上游跟着取消，推送就在远程上新建新名字（旧的留着）。
func TestRenameBranch(t *testing.T) {
	repo, bare := refsRepo(t)
	a, r, wait := dailyApp(t, repo)
	a.OpenRenameBranch(r)
	if d := a.RefDlg; !d.Open || d.Kind != DlgRenameBranch || d.Old != "main" || d.Name != "main" {
		t.Fatalf("改名对话框: %+v", d)
	}
	a.RenameBranch(r, "main", " trunk ")
	wait("改名", func() bool { return idle(r)() && strings.Contains(a.Toast, "推送时会新建 origin/trunk") })
	b, ok := CurrentBranch(r)
	if !ok || b.Name != "trunk" || b.Upstream != "" {
		t.Fatalf("改名后当前分支 %+v", b)
	}
	a.Push(r)
	wait("推送", func() bool { return idle(r)() && Upstream(r) == "origin/trunk" })
	if got := refsIn(t, bare, "refs/heads"); !slices.Equal(got, []string{"refs/heads/main", "refs/heads/trunk"}) {
		t.Errorf("远程分支 %q", got)
	}
	// 没上游的分支改名不多跑一步
	a.Toast = ""
	a.RenameBranch(r, "feature", "feat")
	wait("改名 feature", func() bool {
		return idle(r)() && slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "feat" })
	})
	if strings.Contains(a.Toast, "推送时会新建") {
		t.Errorf("没上游也提示了: %q", a.Toast)
	}
}

// 删本地分支：问话里带数出来的提交数；没合并的 -d 被拒后再问强制删除；推送过的 -d 直接删；检出着的灰着。
func TestDeleteBranch(t *testing.T) {
	repo, _ := refsRepo(t)
	repo.Git("branch", "done")
	repo.Git("switch", "-q", "-c", "pushed")
	repo.Write("p.txt", "p\n")
	repo.Commit("pushed one")
	repo.Git("push", "-q", "-u", "origin", "pushed")
	repo.Git("switch", "-q", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	repo.Git("worktree", "add", "-q", "-b", "side", wt)
	a, r, wait := dailyApp(t, repo)

	if why := DeleteBranchWhy(r, branchNamed(t, r, "main")); why != "当前分支" {
		t.Errorf("当前分支的原因 %q", why)
	}
	if why := DeleteBranchWhy(r, branchNamed(t, r, "side")); why != "在 wt 检出" {
		t.Errorf("别的工作树检出着的原因 %q", why)
	}

	a.DeleteBranch(r, branchNamed(t, r, "done"))
	c := confirm(t, a, wait, "删除分支 done？")
	if !strings.Contains(c.Message, "done 上的提交都已经在当前分支里了") {
		t.Errorf("done 的问话: %q", c.Message)
	}
	wait("删 done", func() bool {
		return idle(r)() && !slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "done" })
	})

	a.DeleteBranch(r, branchNamed(t, r, "pushed"))
	c = confirm(t, a, wait, "删除分支 pushed？")
	for _, want := range []string{"有 1 个提交不在当前分支里，都已经推送过", "远程上的 origin/pushed 不会删"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("pushed 的问话没有 %q: %q", want, c.Message)
		}
	}
	wait("删 pushed", func() bool {
		return idle(r)() && !slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "pushed" })
	})
	if a.ConfirmOpen {
		t.Fatal("推送过的不该再问强制删除")
	}

	a.DeleteBranch(r, branchNamed(t, r, "feature"))
	c = confirm(t, a, wait, "删除分支 feature？")
	if !strings.Contains(c.Message, "其中 1 个没推送过") {
		t.Errorf("feature 的问话: %q", c.Message)
	}
	confirm(t, a, wait, "强制删除分支 feature？")
	wait("删 feature", func() bool {
		return idle(r)() && !slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "feature" })
	})
}

// 删远程分支：git push origin --delete，本地同名分支留着。
func TestDeleteRemoteBranch(t *testing.T) {
	repo, bare := refsRepo(t)
	repo.Git("push", "-q", "-u", "origin", "feature")
	a, r, wait := dailyApp(t, repo)
	a.DeleteRemoteBranch(r, branchNamed(t, r, "origin/feature"))
	c := confirm(t, a, wait, "删除远程分支 origin/feature？")
	for _, want := range []string{"会在 origin 上删掉分支 feature", "上面有 1 个提交不在当前分支里", "本地分支 feature 留着"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("问话没有 %q: %q", want, c.Message)
		}
	}
	wait("删远程分支", func() bool {
		return idle(r)() && !slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "origin/feature" })
	})
	if got := refsIn(t, bare, "refs/heads"); !slices.Equal(got, []string{"refs/heads/main"}) {
		t.Errorf("远程分支 %q", got)
	}
	branchNamed(t, r, "feature")
}

// 标签：轻量 / 附注（打在别的提交上）、推送一个 / 全部、删本地（可连远程一起）。
func TestTags(t *testing.T) {
	repo, bare := refsRepo(t)
	a, r, wait := dailyApp(t, repo)
	base := commitNamed(t, repo.Root, "base")
	hasTag := func(name string) bool {
		return slices.ContainsFunc(r.Tags, func(x git.Tag) bool { return x.Name == name })
	}

	a.OpenNewTag(r, base.Short)
	if d := a.RefDlg; d.Kind != DlgNewTag || d.From != base.Short {
		t.Fatalf("新建标签对话框: %+v", d)
	}
	a.NewTag(r, "v1", "  ", "")
	a.NewTag(r, "v2", "release two", base.Short)
	wait("打标签", func() bool { return idle(r)() && hasTag("v1") && hasTag("v2") })
	if typ := repo.Git("cat-file", "-t", "v1"); typ != "commit" {
		t.Errorf("v1 应是轻量标签: %s", typ)
	}
	if typ := repo.Git("cat-file", "-t", "v2"); typ != "tag" {
		t.Errorf("v2 应是附注标签: %s", typ)
	}
	if got := repo.Git("rev-parse", "v2^{commit}"); got != base.Hash {
		t.Errorf("v2 打在 %s，不是 base %s", got, base.Hash)
	}

	a.PushTag(r, "v1")
	wait("推送 v1", idle(r))
	if got := refsIn(t, bare, "refs/tags"); !slices.Equal(got, []string{"refs/tags/v1"}) {
		t.Fatalf("推一个后远程标签 %q", got)
	}
	a.PushTag(r, "")
	wait("推送全部", idle(r))
	if got := refsIn(t, bare, "refs/tags"); !slices.Equal(got, []string{"refs/tags/v1", "refs/tags/v2"}) {
		t.Fatalf("推全部后远程标签 %q", got)
	}

	a.DeleteTag(r, "v1", true)
	a.DeleteTag(r, "v2", false)
	wait("删标签", func() bool { return idle(r)() && !hasTag("v1") && !hasTag("v2") })
	if got := refsIn(t, bare, "refs/tags"); !slices.Equal(got, []string{"refs/tags/v2"}) {
		t.Errorf("删后远程标签 %q（v1 连远程删，v2 只删本地）", got)
	}
}

// 远程：添加后接着抓取、改名带着远程分支引用走、删除先问（说引用数和当前分支的上游）；没有 origin 时添加默认填 origin。
func TestRemotes(t *testing.T) {
	repo, _ := refsRepo(t)
	other := filepath.Join(t.TempDir(), "other.git")
	repo.Git("init", "-q", "--bare", "-b", "main", other)
	repo.Git("push", "-q", other, "feature:x")
	a, r, wait := dailyApp(t, repo)
	a.OpenAddRemote(r)
	if d := a.RefDlg; d.Kind != DlgAddRemote || d.Name != "" || d.Prefilled {
		t.Fatalf("有 origin 时添加远程: %+v", d)
	}

	a.AddRemote(r, "up", other)
	wait("添加并抓取", func() bool {
		return idle(r)() && HasRemote(r, "up") && slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "up/x" })
	})
	a.RenameRemote(r, "up", "upstream")
	wait("远程改名", func() bool {
		return idle(r)() && HasRemote(r, "upstream") && slices.ContainsFunc(r.Branches, func(b git.Branch) bool { return b.Name == "upstream/x" })
	})
	if n := RemoteRefs(r, "upstream"); n != 1 {
		t.Errorf("upstream 的远程分支引用 %d 个", n)
	}

	a.DeleteRemote(r, "upstream")
	c := confirm(t, a, wait, "删除远程 upstream？")
	if !strings.Contains(c.Message, "本地的 1 个远程分支引用（upstream/…）会一起删") || strings.Contains(c.Message, "上游") {
		t.Errorf("删 upstream 的问话: %q", c.Message)
	}
	wait("删 upstream", func() bool { return idle(r)() && !HasRemote(r, "upstream") })

	a.DeleteRemote(r, "origin")
	c = confirm(t, a, wait, "删除远程 origin？")
	if !strings.Contains(c.Message, "当前分支的上游 origin/main 也会没有") {
		t.Errorf("删 origin 的问话: %q", c.Message)
	}
	wait("删 origin", func() bool { return idle(r)() && len(r.Extra.Remotes) == 0 })
	a.OpenAddRemote(r)
	if d := a.RefDlg; d.Name != "origin" || !d.Prefilled {
		t.Errorf("没有 origin 时添加远程: %+v", d)
	}
}
