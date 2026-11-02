package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// stashFiles 准备一个有一条贮藏的仓库（a.txt、b.txt 改了，-u 时带上没跟踪的 new.txt），展开它，返回应用和文件行。
func stashFiles(t *testing.T, untracked bool) (*App, *Repo, *testutil.Repo, func(string, func() bool)) {
	t.Helper()
	repo := testutil.NewRepo(t, "stash")
	repo.Write("a.txt", "1\n")
	repo.Write("b.txt", "1\n")
	repo.Commit("base")
	repo.Write("a.txt", "a 改了\n")
	repo.Write("b.txt", "b 改了\n")
	args := []string{"stash", "push", "-q", "-m", "两个文件"}
	if untracked {
		repo.Write("new.txt", "新的\n")
		args = append(args, "-u")
	}
	repo.Git(args...)
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.ToggleGitRepo(r)
	a.ToggleGitGroup(r, GroupStash)
	a.ToggleStash(r, r.Extra.Stashes[0])
	wait("读贮藏的文件", func() bool { return r.StashFilesFor == r.Extra.Stashes[0].Hash })
	return a, r, repo, wait
}

// stashFileRow 是展开的贮藏里路径为 p 的那行。
func stashFileRow(t *testing.T, a *App, p string) ChangeRow {
	t.Helper()
	i := gitRow(a, func(cr ChangeRow) bool { return cr.Kind == RowStashFile && cr.Change.Path == p })
	if i < 0 {
		t.Fatalf("没有 %s 这行：%+v", p, a.GitRows())
	}
	return a.GitRows()[i]
}

func readFile(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// 只应用贮藏里的一个文件：它回到工作区，另一个不动，暂存区不动，贮藏还在；多选两个一次应用。
func TestStashApplyFiles(t *testing.T) {
	a, r, repo, wait := stashFiles(t, false)
	cr := stashFileRow(t, a, "a.txt")
	files := a.StashFileRows(cr)
	if len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("没多选时只管它：%+v", files)
	}
	a.ApplyStashFiles(r, files)
	if a.ConfirmOpen {
		t.Fatalf("工作区干净不该问：%+v", a.Confirm)
	}
	wait("应用", idle(r))
	if got := readFile(t, repo.Root, "a.txt"); got != "a 改了\n" {
		t.Errorf("a.txt = %q", got)
	}
	if got := readFile(t, repo.Root, "b.txt"); got != "1\n" {
		t.Errorf("b.txt 不该动：%q", got)
	}
	if a.Toast != "已应用 1 个文件" {
		t.Errorf("Toast %q", a.Toast)
	}
	if out := repo.Git("diff", "--cached", "--name-only"); strings.TrimSpace(out) != "" {
		t.Errorf("暂存区不该动：%q", out)
	}
	if len(r.Extra.Stashes) != 1 {
		t.Errorf("贮藏该留着：%+v", r.Extra.Stashes)
	}

	// 多选同一条贮藏里的两个文件：右键其中一个，两个都管；b.txt 干净，a.txt 刚才改过 → 先问
	repo.Git("checkout", "--", "a.txt")
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	ka, kb := stashFileRow(t, a, "a.txt"), stashFileRow(t, a, "b.txt")
	a.GitChosen.Clear()
	a.GitChosen.Add(ka.Key())
	a.GitChosen.Add(kb.Key())
	files = a.StashFileRows(kb)
	if len(files) != 2 {
		t.Fatalf("多选两个：%+v", files)
	}
	a.ApplyStashFiles(r, files)
	wait("应用", idle(r))
	if readFile(t, repo.Root, "a.txt") != "a 改了\n" || readFile(t, repo.Root, "b.txt") != "b 改了\n" {
		t.Errorf("两个都该回来：%q %q", readFile(t, repo.Root, "a.txt"), readFile(t, repo.Root, "b.txt"))
	}
	if a.Toast != "已应用 2 个文件" {
		t.Errorf("Toast %q", a.Toast)
	}
	// 不在选中里的行：只管它自己
	a.GitChosen.Clear()
	a.GitChosen.Add(ka.Key())
	if files := a.StashFileRows(kb); len(files) != 1 || files[0].Path != "b.txt" {
		t.Errorf("右键没选中的行：%+v", files)
	}
}

// 工作区里这个文件已经有改动：先红色确认“会覆盖工作区里 a.txt 的改动”，取消就不动，确认才覆盖。
func TestStashApplyFileAsks(t *testing.T) {
	a, r, repo, wait := stashFiles(t, false)
	repo.Write("a.txt", "我手上的\n")
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.ApplyStashFiles(r, []git.Change{stashFileRow(t, a, "a.txt").Change})
	if !a.ConfirmOpen || a.Confirm == nil || !a.Confirm.Danger || !strings.Contains(a.Confirm.Message, "会覆盖工作区里 a.txt 的改动") {
		t.Fatalf("该先问：%v %+v", a.ConfirmOpen, a.Confirm)
	}
	if r.Busy != "" || readFile(t, repo.Root, "a.txt") != "我手上的\n" {
		t.Fatalf("问的时候不该动：busy %q", r.Busy)
	}
	a.Confirm.Fn()
	wait("应用", idle(r))
	if got := readFile(t, repo.Root, "a.txt"); got != "a 改了\n" {
		t.Errorf("确认后 a.txt = %q", got)
	}
}

// 没跟踪的文件（stash -u）从贮藏的第三个父提交取，放回来还是没跟踪的；贮藏里删掉的文件菜单里灰掉，什么也不做。
func TestStashApplyUntracked(t *testing.T) {
	a, r, repo, wait := stashFiles(t, true)
	if _, err := os.Stat(filepath.Join(repo.Root, "new.txt")); err == nil {
		t.Fatal("new.txt 该在贮藏里")
	}
	cr := stashFileRow(t, a, "new.txt")
	if cr.Change.Commit == r.Extra.Stashes[0].Hash {
		t.Fatalf("没跟踪的该从第三个父提交取：%+v", cr.Change)
	}
	a.ApplyStashFiles(r, a.StashFileRows(cr))
	wait("应用", idle(r))
	if got := readFile(t, repo.Root, "new.txt"); got != "新的\n" {
		t.Errorf("new.txt = %q", got)
	}
	if out := repo.Git("status", "--porcelain", "--", "new.txt"); strings.TrimSpace(out) != "?? new.txt" {
		t.Errorf("该还是没跟踪的：%q", out)
	}

	busy := len(r.Queue)
	a.ApplyStashFiles(r, []git.Change{{Kind: git.InCommit, Code: 'D', Path: "gone.txt", Commit: r.Extra.Stashes[0].Hash}})
	if a.ConfirmOpen || len(r.Queue) != busy || r.Busy != "" {
		t.Errorf("删掉的文件不该跑命令")
	}
}
