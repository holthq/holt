package app

import (
	"slices"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 未跟踪目录：harness/ 折叠成一行，后台列出里面两个文件；选中目录行不开 Diff，回车、← → 展开 / 收起，
// 展开出两行（排好序）、选中留在目录行；点子行开出新文件的 Diff、内容对，状态重读后标签还在、选中跟着它，‹ › 走目录里的文件；
// 目录行进 OpenDiffs 被跳过；暂存子行后状态重读，harness/ 那项没了、换成 harness/a.md。临时仓库。
func TestUntrackedDirExpand(t *testing.T) {
	repo := testutil.NewRepo(t, "udir")
	repo.Write("a.txt", "a\n")
	repo.Commit("root")
	repo.Write("harness/sub/b.md", "b 的内容\n")
	repo.Write("harness/a.md", "a\n")
	repo.Write("c.md", "c\n")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态、列未跟踪目录", func() bool { return idle(r)() && r.Status.DirFiles["harness/"] != nil })
	shown := func() []string {
		var out []string
		for _, cr := range a.GitRows() {
			if cr.Kind == RowChange || cr.InDir != "" {
				out = append(out, cr.InDir+"|"+cr.Change.Path)
			}
		}
		return out
	}
	rowOf := func(p string) int {
		t.Helper()
		i := slices.IndexFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Kind == RowChange && cr.Change.Path == p })
		if i < 0 {
			t.Fatalf("Git 栏里没有 %s：%q", p, shown())
		}
		return i
	}
	pick := func(p string) {
		t.Helper()
		i := rowOf(p)
		a.GitSel = i
		a.chooseGit(a.GitRows(), i)
	}
	if got := shown(); !slices.Equal(got, []string{"|c.md", "|harness/"}) || r.Status.Untracked != 2 {
		t.Fatalf("收着的目录：%q（未跟踪 %d）", got, r.Status.Untracked)
	}

	a.GitHover = -1
	pick("harness/")
	a.OnChangeSelected(false)
	if len(a.Docs) != 0 || r.DirOpen["harness/"] {
		t.Fatalf("选中目录行不开 Diff、不展开：%d 个标签，展开 %v", len(a.Docs), r.DirOpen)
	}
	a.OnChangeSelected(true) // 回车
	want := []string{"|c.md", "|harness/", "harness/|harness/a.md", "harness/|harness/sub/b.md"}
	if got := shown(); !slices.Equal(got, want) || a.GitSel != rowOf("harness/") {
		t.Fatalf("回车展开：%q，选中 %d", got, a.GitSel)
	}
	a.FoldGitDir(false)
	if got := shown(); len(got) != 2 || a.GitSel != rowOf("harness/") {
		t.Fatalf("← 收起：%q，选中 %d", got, a.GitSel)
	}
	a.FoldGitDir(true)
	if got := shown(); !slices.Equal(got, want) {
		t.Fatalf("→ 展开：%q", got)
	}

	pick("harness/sub/b.md")
	a.OnChangeSelected(false)
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || d.Change.Kind != git.Untracked || d.Change.Path != "harness/sub/b.md" {
		t.Fatalf("点子行开的标签：%+v", d)
	}
	wait("新文件的 Diff", func() bool { return !d.Loading && d.DiffSt != nil })
	if string(d.Raw[0]) != "" || string(d.Raw[1]) != "b 的内容\n" {
		t.Errorf("Diff 两边：%q / %q", d.Raw[0], d.Raw[1])
	}
	a.RefreshRepo(r, WhyManual)
	wait("重读", idle(r))
	if d.Info == GoneNote || d.DiffSt == nil {
		t.Errorf("重读后目录里的文件的 Diff 不该算没了：%q", d.Info)
	}
	if a.GitSel != rowOf("harness/sub/b.md") {
		t.Errorf("重读后选中该跟着标签：%d", a.GitSel)
	}
	a.FoldGitDir(false) // 子行上 ← 回到目录行，不收起
	if a.GitSel != rowOf("harness/") || !r.DirOpen["harness/"] {
		t.Errorf("子行上 ←：选中 %d，展开 %v", a.GitSel, r.DirOpen)
	}
	if !a.StepFile(-1) || a.CurrentDoc().Change.Path != "harness/a.md" {
		t.Errorf("‹ 走到目录里的上一个文件：%+v", a.CurrentDoc().Change)
	}

	rows := a.GitRows()
	n := len(a.Docs)
	a.OpenDiffs([]ChangeRow{rows[rowOf("harness/")], rows[rowOf("c.md")]})
	if len(a.Docs) != n+1 || slices.ContainsFunc(a.Docs, func(d *docs.Doc) bool { return d.Change.Path == "harness/" }) {
		t.Errorf("OpenDiffs 该跳过目录行：%v", titlesOf(a.Docs))
	}

	a.Stage(r, "harness/sub/b.md")
	wait("暂存子行", func() bool { return idle(r)() && !r.Status.HasDirs() })
	got := shown()
	if !slices.Contains(got, "|harness/a.md") || slices.Contains(got, "|harness/") || slices.Contains(got, "harness/|harness/a.md") || r.Status.Untracked != 2 {
		t.Errorf("暂存了 harness/sub/b.md 以后：%q（未跟踪 %d）", got, r.Status.Untracked)
	}
}

// 列未跟踪目录比状态慢时：先贴状态，同一个目录先用上一次列的，没列过的没有键（画成“…”）。
func TestUntrackedDirLate(t *testing.T) {
	old := git.Status{DirFiles: map[string][]string{"x/": {"x/1"}, "gone/": {"gone/1"}}, DirMore: map[string]bool{"x/": true}}
	st := git.Status{Changes: []git.Change{{Kind: git.Untracked, Path: "x/", Dir: true}, {Kind: git.Untracked, Path: "y/", Dir: true}}}
	files, more := keepDirs(old, st)
	if len(files) != 1 || !slices.Equal(files["x/"], []string{"x/1"}) || !more["x/"] || len(more) != 1 {
		t.Errorf("keepDirs = %v %v", files, more)
	}
	if _, late := waitDirs(make(chan dirLists, 1)); late == nil {
		t.Error("没列完时 waitDirs 该返回 late")
	}
	ch := make(chan dirLists, 1)
	ch <- dirLists{files: map[string][]string{"y/": {}}}
	if dl, late := waitDirs(ch); late != nil || dl.files["y/"] == nil {
		t.Errorf("列完了：%v %v", dl, late)
	}
}
