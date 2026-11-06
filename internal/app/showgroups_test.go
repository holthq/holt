package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
)

// hasRow 是 rows 里有 r 的 k 那种行。
func hasRow(rows []ChangeRow, r *Repo, k RowKind) bool {
	return slices.ContainsFunc(rows, func(cr ChangeRow) bool { return cr.Repo == r && cr.Kind == k })
}

// 设置里的"在 Git 栏显示工作树 / 贮藏"默认都关：没有工作树组、贮藏组、"需要你"条，干净但有工作树的仓库不自动展开，
// 也不算各工作树的状态、不盯目录；打开后都回来（会话里的 WtOpen 照旧恢复），再关掉停掉监听。全在临时仓库上。
func TestGitBarGroupsSetting(t *testing.T) {
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.Commit("初始")
	feat := filepath.Join(filepath.Dir(m.Root), "proj-feat")
	m.Git("worktree", "add", "-q", "-b", "wt/feat", feat)
	if err := os.WriteFile(filepath.Join(feat, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Git("-C", feat, "add", "b.txt")
	m.Git("-C", feat, "commit", "-q", "-m", "工作树里的提交") // 领先主分支 1：开着时是"可合并"
	m.Write("a.txt", "改\n")
	m.Git("stash", "-q") // 主仓库干净、有一条贮藏

	a, wait := newTestApp(t, wsFile(t, "开关", m.Root))
	r := a.Repos[0]
	a.RefreshAll(WhyOpen)
	wait("读完", func() bool { return idle(r)() && len(r.Worktrees) > 0 && len(r.Extra.Stashes) == 1 })
	a.StartWatching()
	defer a.Close()
	wait("监听开了", r.Watching)
	r.WtOpen = true // 会话里记着展开过：关着时不算，打开时恢复

	a.SyncWtAuto()
	rows := a.GitRows()
	switch {
	case r.GitOpen():
		t.Error("干净、只有工作树的仓库不该自动展开")
	case len(rows) != 1:
		t.Errorf("关着时只该有段头：%+v", rows)
	case r.WtShown() || r.WtTracked() || len(r.wtWatch) != 0 || r.WtInfo != nil || r.WtLoading:
		t.Errorf("关着时不该算、不该盯：shown=%v tracked=%v watch=%v info=%v", r.WtShown(), r.WtTracked(), r.wtWatch, r.WtInfo)
	case a.NeedsYou() != nil:
		t.Errorf("关着时不该有\"需要你\"：%+v", a.NeedsYou())
	}
	r.GitFold = FoldOpen
	if rows := a.GitRows(); hasRow(rows, r, RowWorktrees) || hasRow(rows, r, RowStashes) {
		t.Errorf("展开了也不该有工作树组、贮藏组：%+v", rows)
	}
	r.GitFold = FoldAuto
	a.GoToWorktree(r, r.Worktrees[0])
	if a.WtExp != nil || r.GitFold != FoldAuto || a.GitSel != -1 {
		t.Errorf("关着时 GoToWorktree 不该留下状态：exp=%v fold=%v sel=%d", a.WtExp, r.GitFold, a.GitSel)
	}

	showGroups(a)
	wait("打开后算状态、盯目录", func() bool { return r.WtInfo != nil && !r.WtLoading && r.WtWatching() == 1 })
	a.SyncWtAuto()
	rows = a.GitRows()
	if !r.GitOpen() || !hasRow(rows, r, RowWorktrees) || !hasRow(rows, r, RowWorktree) || !hasRow(rows, r, RowStashes) {
		t.Fatalf("打开后工作树组（按 WtOpen 展开着）、贮藏组都该回来：open=%v %+v", r.GitOpen(), rows)
	}
	if ns := a.NeedsYou(); len(ns) != 1 || ns[0].Wt.Path != wtAt(t, r, feat).Path {
		t.Errorf("打开后领先的工作树该在\"需要你\"里：%+v", ns)
	}
	a.GoToWorktree(r, wtAt(t, r, feat))
	if !a.WtExpanded(r, wtAt(t, r, feat)) || a.GitSel < 0 {
		t.Fatalf("打开后 GoToWorktree 该展开、选中：exp=%v sel=%d", a.WtExp, a.GitSel)
	}

	// 关掉：收起展开着的工作树、停掉监听、扔掉状态；WtOpen 不清
	a.St.Settings.ShowWorktrees = false
	a.SettingsChanged()
	if r.GitOpen() {
		t.Error("关掉后干净的仓库该收回去（自动展开不再算工作树）")
	}
	r.GitFold = FoldOpen
	switch rows := a.GitRows(); {
	case len(r.wtWatch) != 0 || r.WtInfo != nil:
		t.Errorf("关掉后还在盯 / 留着状态：%v %v", r.wtWatch, r.WtInfo)
	case a.WtExp != nil:
		t.Error("关掉后展开着的工作树该收起")
	case !r.WtOpen:
		t.Error("关掉不该清会话里的 WtOpen")
	case hasRow(rows, r, RowWorktrees) || !hasRow(rows, r, RowStashes):
		t.Errorf("关掉工作树后只剩贮藏组：%+v", rows)
	case a.GitSel >= 0 && a.GitSel < len(rows) && rows[a.GitSel].Kind == RowWorktree:
		t.Errorf("选中还落在工作树行上：%d", a.GitSel)
	}
}

// 贮藏组藏着时贮藏、弹出贮藏照常能用，r.Extra.Stashes 照常读。
func TestStashHiddenStillWorks(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("贮藏我\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.RefreshRepo(r, WhyManual)
	wait("有改动", func() bool { return idle(r)() && r.Status.Unstaged > 0 })
	n := len(r.Extra.Stashes)

	a.Exec("git.stash")
	wait("贮藏了", func() bool { return idle(r)() && len(r.Extra.Stashes) == n+1 && r.Status.Unstaged == 0 })
	if hasRow(a.GitRows(), r, RowStashes) {
		t.Error("关着时不该有贮藏组")
	}
	a.Exec("git.stashPop")
	wait("弹出了", func() bool { return idle(r)() && len(r.Extra.Stashes) == n && r.Status.Unstaged > 0 })
}
