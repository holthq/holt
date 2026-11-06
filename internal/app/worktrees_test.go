package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// 工作树都建在 t.TempDir() 里：主仓库 proj 旁边一个分支工作树（经过符号链接加进工作区）、一个分离 HEAD 的，
// 仓库里面 .claude/worktrees/x 一个；另一个没有工作树的仓库一条工作树命令都不跑。
func TestRepoWorktrees(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.CommitAt("初始", t0)
	side := filepath.Dir(m.Root)
	feat, det := filepath.Join(side, "proj-feat"), filepath.Join(side, "proj-demo")
	nest := filepath.Join(m.Root, ".claude", "worktrees", "x")
	m.Git("worktree", "add", "-q", "-b", "wt/feat", feat)
	m.In(feat).Write("f.txt", "工作树\n")
	m.In(feat).CommitAt("工作树里的提交", t0.Add(2*time.Hour))
	m.Git("worktree", "add", "-q", "--detach", det)
	m.Git("worktree", "add", "-q", "-b", "nest", nest)
	if out := testutil.GitOut(t, m.Root, "ls-files", "-o", "--exclude-standard"); !strings.Contains(out, ".claude/worktrees/x/") {
		t.Fatalf("套在里面的工作树应在 ls-files 里：%q", out)
	}
	o := testutil.NewRepo(t, "other")
	o.Write("b.txt", "二\n")
	o.Commit("一")
	link := filepath.Join(t.TempDir(), "feat-link")
	if err := os.Symlink(feat, link); err != nil {
		t.Fatal(err)
	}

	// 没有 .git/worktrees 的仓库：ctx 取消了也不报错，说明一条命令都没跑；有的就要跑
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		root string
		runs bool
	}{{o.Root, false}, {m.Root, true}} {
		x, err := git.ReadExtra(context.Background(), c.root)
		if err != nil || x.CommonDir == "" || x.Linked {
			t.Fatalf("%s：%+v %v", c.root, x, err)
		}
		if l := readWorktrees(canceled, c.root, x); errors.Is(l.err, git.ErrCanceled) != c.runs || len(l.wts) != 0 {
			t.Errorf("%s 跑命令 = %v，要 %v（%+v）", c.root, l.err, c.runs, l.wts)
		}
	}

	stateDir := t.TempDir()
	ws := wsFile(t, "工作树", m.Root, o.Root, link)
	a, wait := newTestAppIn(t, ws, stateDir)
	showGroups(a)
	a.RefreshAll(WhyOpen)
	mr, or, lr := a.Repos[0], a.Repos[1], a.Repos[2]
	wait("三个读完", func() bool { return idle(mr)() && idle(or)() && idle(lr)() })

	// 主仓库：三个，不含自己，最新提交的在前
	if len(mr.Worktrees) != 3 || mr.Linked || mr.MainRoot != "" || mr.WtErr != nil {
		t.Fatalf("主仓库的工作树：%+v linked=%v %v", mr.Worktrees, mr.Linked, mr.WtErr)
	}
	for _, w := range mr.Worktrees {
		if w.Main || git.SamePath(w.Path, m.Root) {
			t.Errorf("列表里有自己：%+v", w)
		}
	}
	if w := mr.Worktrees[0]; w.Branch != "wt/feat" || !w.Time.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("第一个应是最新的 wt/feat：%+v", w)
	}
	if or.HasWorktrees() || or.Worktrees != nil {
		t.Errorf("没有工作树的仓库：%+v", or.Worktrees)
	}
	// 链接的工作树（经过符号链接加进来的）：主的在最前，MainRoot 是主仓库；主仓库列表里那条"已打开"认得出它
	if !lr.Linked || !git.SamePath(lr.MainRoot, m.Root) || len(lr.Worktrees) != 3 || !lr.Worktrees[0].Main {
		t.Fatalf("链接的工作树：linked=%v main=%q %+v", lr.Linked, lr.MainRoot, lr.Worktrees)
	}
	if a.WtRepo(mr.Worktrees[0]) != lr || a.WtRepo(lr.Worktrees[0]) != mr {
		t.Error("已打开：经过符号链接没认出来")
	}
	if wr := a.WtRepo(wtAt(t, mr, det)); wr != nil {
		t.Errorf("没打开的认成了 %s", wr.Root)
	}

	// 文件树里没有套在里面的工作树
	a.EnsureFiles(mr)
	wait("文件树", func() bool { return mr.Files.Loaded && !mr.Files.Loading })
	if slices.ContainsFunc(mr.Files.Paths, func(p string) bool { return strings.HasPrefix(p, ".claude") }) || !slices.Contains(mr.Files.Paths, "a.txt") {
		t.Errorf("文件树：%v", mr.Files.Paths)
	}

	// Git 栏：段头下面一行"工作树 3"，默认收着；干净的另一个仓库还是收成一行
	rows := a.GitRows()
	i := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Repo == mr && cr.Kind == RowWorktrees })
	if i != 1 || rows[i].Count != 3 || slices.ContainsFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowWorktree }) {
		t.Fatalf("工作树块：%d %+v", i, rows[max(i, 0)])
	}
	if or.GitOpen() {
		t.Error("干净、没有工作树的仓库应收着")
	}
	a.ToggleWorktrees(mr)
	if n := len(slices.DeleteFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Repo != mr || cr.Kind != RowWorktree })); n != 3 || !mr.WtLoading {
		t.Fatalf("展开后 %d 行、loading=%v", n, mr.WtLoading)
	}
	wait("懒算", func() bool { return mr.WtInfo != nil })
	if info := mr.WtInfo[wtAt(t, mr, feat).Path]; info == nil || info.Ahead != 1 || info.Behind != 0 || info.Dirty != 0 || info.Err != nil {
		t.Errorf("wt/feat：%+v", info)
	}
	if info := lr.WtInfo; info != nil {
		t.Error("收着的块不该算")
	}
	if err := os.WriteFile(filepath.Join(det, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.RefreshWorktrees(mr)
	wait("重算", func() bool {
		return idle(mr)() && !mr.WtLoading && mr.WtInfo[wtAt(t, mr, det).Path] != nil && mr.WtInfo[wtAt(t, mr, det).Path].Dirty == 1
	})
	if dirty, prunable, loaded := mr.WtSummary(); dirty != 1 || prunable != 0 || !loaded {
		t.Errorf("块头：%d 有改动、%d 可清理、%v", dirty, prunable, loaded)
	}

	// 右栏看工作树的历史：它那个分支往前，提交图在；在 Git 栏里选一项改动就回到自己的历史
	a.ShowWorktreeHistory(mr, mr.Worktrees[0])
	wait("工作树历史", func() bool { return mr.Hist.Loaded && !mr.Hist.Loading })
	h := &mr.Hist
	if h.RefName != "wt/feat" || len(h.Commits) != 2 || h.Commits[0].Subject != "工作树里的提交" || !h.HasGraph() || mr.RefHead() != h.Commits[0].Hash {
		t.Fatalf("工作树历史：%q %+v", h.RefName, h.Commits)
	}
	rows = a.GitRows()
	pickGit(a, slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowChange && cr.Repo == mr }))
	a.GitHover = -1
	a.OnChangeSelected(false)
	wait("回到自己的历史", func() bool { return mr.Hist.Loaded && !mr.Hist.Loading })
	if h.RefPath != "" || len(h.Commits) != 1 || h.Commits[0].Subject != "初始" {
		t.Errorf("点别处之后：%q %+v", h.RefPath, h.Commits)
	}

	// 目录没了：可清理
	if err := os.RemoveAll(det); err != nil {
		t.Fatal(err)
	}
	a.RefreshWorktrees(mr)
	wait("可清理", func() bool { return idle(mr)() && wtAt(t, mr, det).Prunable && !mr.WtLoading })
	if _, prunable, _ := mr.WtSummary(); prunable != 1 {
		t.Errorf("可清理 %d", prunable)
	}

	// 展开的块记在会话里，重开还展开着
	a.SaveState()
	b, wait2 := newTestAppIn(t, ws, stateDir)
	showGroups(b)
	if !b.Repos[0].WtOpen || b.Repos[1].WtOpen || b.Repos[2].WtOpen {
		t.Errorf("重开后 wtOpen：%v %v %v", b.Repos[0].WtOpen, b.Repos[1].WtOpen, b.Repos[2].WtOpen)
	}
	b.RefreshAll(WhyOpen)
	wait2("重开后懒算", func() bool { return idle(b.Repos[0])() && b.Repos[0].WtInfo != nil })
}

// wtAt 是仓库工作树列表里目录为 dir 的那条。
func wtAt(t *testing.T, r *Repo, dir string) Worktree {
	t.Helper()
	for _, w := range r.Worktrees {
		if git.SamePath(w.Path, dir) {
			return w
		}
	}
	t.Fatalf("%s 的工作树里没有 %s：%+v", r.Name, dir, r.Worktrees)
	return Worktree{}
}

// 写操作只在临时仓库上：新建（默认同级目录、已存在不让建）→ 自动打开；锁定；移除锁着又有改动的先被拒、
// 问过才强制，"同时删除分支"没合并的再问才 -D；检出已有分支；目录删了之后清理。
func TestWorktreeOps(t *testing.T) {
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.Commit("初始")
	m.Git("branch", "free")
	side := filepath.Dir(m.Root)
	a, wait := newTestApp(t, wsFile(t, "写", m.Root))
	showGroups(a)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyOpen)
	wait("读状态", idle(r))

	// 对话框：默认建新分支，目录跟着分支名走；目录已经有了不让建
	a.Exec("git.newWorktree")
	d := &a.WtDialog
	if !d.Open || d.Kind != WtNew || !d.NewBranch || !d.OpenAfter || d.Repo != r {
		t.Fatalf("对话框：%+v", d)
	}
	d.Name = "wt/fix-io"
	a.WtDirFollow()
	dir := filepath.Join(side, "proj-fix-io")
	if d.Dir != dir || !a.WtCanCreate() {
		t.Fatalf("默认目录 %q（要 %q）can=%v", d.Dir, dir, a.WtCanCreate())
	}
	os.Mkdir(dir, 0o755)
	d.dirFor = "" // 按目录缓存着：同一个目录不再去看磁盘
	if a.WtCanCreate() {
		t.Error("目录已经有了还能建")
	}
	os.Remove(dir)
	d.dirFor = ""
	if got := r.WtFreeBranches(); !slices.Equal(got, []string{"free"}) {
		t.Errorf("能检出的已有分支：%v", got)
	}

	a.CreateWorktree()
	wait("新建并打开", func() bool { return idle(r)() && len(a.Repos) == 2 && idle(a.Repos[1])() && len(r.Worktrees) == 1 })
	wr := a.Repos[1]
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || !wr.Linked || a.Current() != wr || a.WtRepo(r.Worktrees[0]) != wr {
		t.Fatalf("新建的工作树：%v linked=%v cur=%v", err, wr.Linked, a.Current().Name)
	}
	if got := testutil.GitOut(t, dir, "branch", "--show-current"); got != "wt/fix-io" {
		t.Errorf("新工作树的分支 %q", got)
	}

	// 锁定（带原因）
	a.LockWorktree(r, r.Worktrees[0])
	a.WtDialog.Reason = " 在 U 盘上 "
	a.DoLockWorktree()
	wait("锁定", func() bool { return idle(r)() && len(r.Worktrees) == 1 && r.Worktrees[0].Locked })
	if w := r.Worktrees[0]; w.LockReason != "在 U 盘上" {
		t.Errorf("锁定原因 %q", w.LockReason)
	}

	// 工作树里提交一次（分支就没合并）、再留一个未跟踪的文件；移除：先被拒 → 问 → 强制；再问删分支 → -D
	m.In(dir).Write("b.txt", "工作树\n")
	m.In(dir).Commit("工作树的提交")
	m.In(dir).Write("scratch.txt", "没提交\n")
	a.RemoveWorktree(r, r.Worktrees[0])
	if !a.WtDialog.Open || a.WtDialog.Kind != WtRemove || a.WtDialog.DeleteBranch {
		t.Fatalf("移除对话框：%+v", a.WtDialog)
	}
	a.WtDialog.DeleteBranch = true
	a.DoRemoveWorktree()
	wait("被拒", func() bool { return idle(r)() && a.ConfirmOpen })
	if a.Confirm.Button != "强制移除" {
		t.Fatalf("没问强制：%+v", a.Confirm)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("还没确认就删了")
	}
	a.ConfirmOpen = false
	a.Confirm.Fn()
	wait("强制移除、问删分支", func() bool { return idle(r)() && a.ConfirmOpen })
	if _, err := os.Stat(dir); !os.IsNotExist(err) || len(a.Repos) != 1 || len(r.Worktrees) != 0 {
		t.Fatalf("强制移除后：%v 段数 %d %+v", err, len(a.Repos), r.Worktrees)
	}
	if a.Confirm.Button != "强制删除" || !strings.Contains(testutil.GitOut(t, m.Root, "branch", "--list", "wt/fix-io"), "wt/fix-io") {
		t.Fatalf("没合并的分支应先问：%+v", a.Confirm)
	}
	a.ConfirmOpen = false
	a.Confirm.Fn()
	wait("删分支", idle(r))
	if got := testutil.GitOut(t, m.Root, "branch", "--list", "wt/fix-io"); got != "" {
		t.Errorf("分支还在：%q", got)
	}

	// 检出已有的分支，不打开
	a.NewWorktree(r)
	a.WtDialog.NewBranch, a.WtDialog.Existing, a.WtDialog.OpenAfter = false, "free", false
	a.WtDirFollow()
	freeDir := filepath.Join(side, "proj-free")
	if a.WtDialog.Dir != freeDir {
		t.Fatalf("已有分支的默认目录 %q", a.WtDialog.Dir)
	}
	a.CreateWorktree()
	wait("检出已有分支", func() bool { return idle(r)() && len(r.Worktrees) == 1 })
	if w := r.Worktrees[0]; w.Branch != "free" || len(a.Repos) != 1 {
		t.Fatalf("%+v 段数 %d", w, len(a.Repos))
	}

	// 没有失效的：不问；删掉目录后清理（先列出来、问过再清）
	a.PruneWorktrees(r)
	wait("没有失效的", func() bool { return a.Toast == "没有失效的工作树" })
	if err := os.RemoveAll(freeDir); err != nil {
		t.Fatal(err)
	}
	a.RefreshRepo(r, WhyManual)
	wait("可清理", func() bool { return idle(r)() && len(r.Worktrees) == 1 && r.Worktrees[0].Prunable })
	a.Exec("git.pruneWorktrees")
	wait("清理前问", func() bool { return a.ConfirmOpen })
	if !strings.Contains(a.Confirm.Message, "proj-free") || a.Confirm.Button != "清理" {
		t.Fatalf("清理的确认：%+v", a.Confirm)
	}
	a.ConfirmOpen = false
	a.Confirm.Fn()
	wait("清理", func() bool { return idle(r)() && len(r.Worktrees) == 0 })
	if r.HasWorktrees() || slices.ContainsFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Kind == RowWorktrees }) {
		t.Error("清理后还有工作树块")
	}
}

// 切到在别的工作树里检出的分支：不跑 git（它会拒绝），提示在哪检出。
func TestSwitchRefusedElsewhere(t *testing.T) {
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.Commit("初始")
	m.Git("worktree", "add", "-q", "-b", "feat", filepath.Join(filepath.Dir(m.Root), "proj-feat"))
	a, wait := newTestApp(t, wsFile(t, "切", m.Root))
	showGroups(a)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyOpen)
	wait("读状态", idle(r))
	i := slices.IndexFunc(r.Branches, func(b git.Branch) bool { return b.Name == "feat" })
	if i < 0 || !r.Branches[i].CheckedOutElsewhere(r.Root) {
		t.Fatalf("分支：%+v", r.Branches)
	}
	a.SwitchBranch(r, r.Branches[i])
	if a.Toast != "feat 已在 proj-feat 检出" || r.Busy != "" || len(r.Queue) != 0 || a.ConfirmOpen {
		t.Errorf("toast %q busy %q", a.Toast, r.Busy)
	}
	if got := testutil.GitOut(t, m.Root, "branch", "--show-current"); got != "main" {
		t.Errorf("当前分支 %q", got)
	}
}
