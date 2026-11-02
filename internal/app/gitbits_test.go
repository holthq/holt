package app

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// pickGit 像列表那样把 Git 栏的选中换成第 i 行（单击、方向键）：光标和选中都是它。
func pickGit(a *App, i int) {
	a.GitSel = i
	a.chooseGit(a.GitRows(), i)
}

// gitRow 是 Git 栏里第一个 match 的行，没有为 -1。
func gitRow(a *App, match func(ChangeRow) bool) int {
	return slices.IndexFunc(a.GitRows(), match)
}

// 贮藏展开看文件：展开时先是"读取中…"，读完一个文件一行（未跟踪的接在后面）；单击文件在预览 Diff 里看、
// 选中跟着它；键盘走到贮藏上只停住，回车展开 / 收起；空的贮藏写一句；这条贮藏没了（丢弃）就收起来。
func TestStashExpand(t *testing.T) {
	repo := testutil.NewRepo(t, "stash")
	repo.Write("a.txt", "1\n2\n")
	repo.Commit("base")
	repo.Write("a.txt", "1\n二\n")
	repo.Write("new.txt", "n\n")
	repo.Git("stash", "push", "-q", "-u", "-m", "两个文件")
	repo.Write("a.txt", "x\n")
	repo.Git("add", "a.txt")
	repo.Write("a.txt", "1\n2\n") // 暂存了改动、工作区又改回去：贮藏的工作区和 HEAD 一样
	repo.Git("stash", "push", "-q", "-m", "空的")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if len(r.Extra.Stashes) != 2 {
		t.Fatalf("贮藏 %+v", r.Extra.Stashes)
	}
	a.ToggleGitRepo(r)
	a.ToggleGitGroup(r, GroupStash)
	two := r.Extra.Stashes[1]
	stashRow := func(s git.Stash) int {
		return gitRow(a, func(cr ChangeRow) bool { return cr.Kind == RowStash && cr.Stash.Hash == s.Hash })
	}
	i := stashRow(two)
	if i < 0 {
		t.Fatalf("没有贮藏行: %+v", a.GitRows())
	}

	// 键盘走上来：只停住、不展开
	pickGit(a, i)
	a.GitHover = -1
	a.OnChangeSelected(false)
	if r.StashOpen != "" || a.GitSel != i {
		t.Fatalf("键盘走到贮藏上不该展开: open %q sel %d", r.StashOpen, a.GitSel)
	}
	a.syncChangeSel()
	if a.GitSel != i {
		t.Errorf("选中应停在贮藏上: %d", a.GitSel)
	}
	// 回车展开：先是"读取中…"
	a.OnChangeSelected(true)
	if r.StashOpen != two.Hash || !r.StashLoading {
		t.Fatalf("回车应展开: %q", r.StashOpen)
	}
	if rows := a.GitRows(); rows[i+1].Kind != RowNote || rows[i+1].Title != "读取中…" {
		t.Errorf("读的时候: %+v", rows[i+1])
	}
	wait("读贮藏的文件", func() bool { return r.StashFilesFor == two.Hash })
	rows := a.GitRows()
	var paths []string
	for _, cr := range rows[i+1:] {
		if cr.Kind != RowStashFile {
			break
		}
		paths = append(paths, cr.Change.Path)
	}
	if !slices.Equal(paths, []string{"a.txt", "new.txt"}) {
		t.Fatalf("贮藏下面的文件: %q", paths)
	}
	if a.GitSel != i {
		t.Errorf("展开后选中还应在贮藏上: %d", a.GitSel)
	}

	// 单击贮藏里的文件：预览 Diff（InCommit，和贮藏时的 HEAD 比），选中跟着它
	pickGit(a, i+2)
	a.GitHover = i + 2
	a.OnChangeSelected(false)
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || d.Change.Kind != git.InCommit || d.Change.Path != "new.txt" || d.Pinned {
		t.Fatalf("应在预览里看 new.txt: %+v", d)
	}
	a.syncChangeSel()
	if a.GitSel != i+2 {
		t.Errorf("选中应跟着贮藏里的文件: %d", a.GitSel)
	}

	// 空的贮藏：展开另一条（这条收起），写一句
	empty := r.Extra.Stashes[0]
	a.ToggleStash(r, empty)
	wait("读空的贮藏", func() bool { return r.StashFilesFor == empty.Hash })
	j := stashRow(empty)
	if rows := a.GitRows(); rows[j+1].Kind != RowNote || rows[j+1].Title != "这条贮藏是空的" || slices.ContainsFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowStashFile }) {
		t.Errorf("空的贮藏: %+v", rows[j+1])
	}
	// 键在一个仓库段里不重：两条说明（没有未提交的变更、这条贮藏是空的）
	keys := map[any]bool{}
	for _, cr := range a.GitRows() {
		if keys[cr.Key()] {
			t.Errorf("重复的键 %+v", cr.Key())
		}
		keys[cr.Key()] = true
	}

	// 丢掉展开着的那条：重读后收起
	repo.Git("stash", "drop", "-q", "stash@{0}")
	a.RefreshRepo(r, WhyManual)
	wait("重读", func() bool { return idle(r)() && len(r.Extra.Stashes) == 1 })
	if r.StashOpen != "" || r.StashFiles != nil {
		t.Errorf("丢掉后应收起: %q", r.StashOpen)
	}
	// 再展开剩下那条、再点一次收起
	a.ToggleStash(r, two)
	wait("读贮藏的文件", func() bool { return r.StashFilesFor == two.Hash })
	a.ToggleStash(r, two)
	if r.StashOpen != "" || gitRow(a, func(cr ChangeRow) bool { return cr.Kind == RowStashFile }) >= 0 {
		t.Error("再点应收起")
	}
}

// 比较两个提交：起点标在晚的提交上也按时间早的在前（同一时间的照点的顺序）；比较段在"未提交的改动"下面，
// 读完一个文件一行（Commit 是终点、Parent 是起点）；点文件开 Diff，和提交自己的同一个文件是两个标签；收起时选中落回标题；× 清掉。
func TestCompareCommits(t *testing.T) {
	repo := testutil.NewRepo(t, "cmp")
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	repo.Write("a.txt", "1\n2\n")
	repo.Write("b.txt", "b\n")
	repo.CommitAt("A", t0)
	repo.Write("a.txt", "1\n二\n")
	repo.CommitAt("B", t0.Add(time.Hour))
	repo.Write("a.txt", "1\n二\n3\n")
	repo.Write("c.txt", "c\n")
	repo.Git("rm", "-q", "b.txt")
	repo.CommitAt("C", t0.Add(time.Hour)) // 和 B 同一时间
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.SetHistAll(true)
	a.LoadHistory(r, true)
	h := &r.Hist
	wait("历史", func() bool { return h.Loaded && !h.Loading })
	if len(h.Commits) != 3 {
		t.Fatalf("提交 %+v", h.Commits)
	}
	cC, cB, cA := h.Commits[0], h.Commits[1], h.Commits[2]

	a.MarkCompareFrom(r, cC)
	if h.CompareFrom == nil || h.CompareFrom.Hash != cC.Hash || !strings.Contains(a.Toast, cC.Short) {
		t.Fatalf("起点 %+v toast %q", h.CompareFrom, a.Toast)
	}
	h.Sel = 1 // 选着 C
	a.CompareCommits(r, cC, cA)
	if a.St.HistAll {
		t.Error("比较时应换到当前仓库视图")
	}
	cp := h.Compare
	if cp == nil || cp.From.Hash != cA.Hash || cp.To.Hash != cC.Hash || !cp.Loading || !cp.Open {
		t.Fatalf("比较 %+v", cp)
	}
	if rows := HistRows(r); rows[1] != (HistRow{CompareRow, -1}) || rows[h.Sel] != (HistRow{0, -1}) {
		t.Errorf("读的时候: 行 %v 选中 %d", rows, h.Sel)
	}
	wait("比较", func() bool { return !cp.Loading })
	var paths []string
	for _, f := range cp.Files {
		paths = append(paths, string(f.Code)+" "+f.Path)
		if f.Commit != cC.Hash || f.Parent != cA.Hash || !f.Range || f.Kind != git.InCommit {
			t.Errorf("比较的文件 %+v", f)
		}
	}
	if !slices.Equal(paths, []string{"M a.txt", "D b.txt", "A c.txt"}) {
		t.Fatalf("A → C 改了: %q", paths)
	}
	if f := cp.Files[0]; f.Add != 2 || f.Del != 1 {
		t.Errorf("a.txt 的行数 +%d −%d", f.Add, f.Del)
	}
	rows := HistRows(r)
	if len(rows) != 2+3+3 || rows[2] != (HistRow{CompareRow, 0}) || rows[5] != (HistRow{0, -1}) || rows[h.Sel] != (HistRow{0, -1}) {
		t.Fatalf("读完: 行 %v 选中 %d", rows, h.Sel)
	}

	// 点比较里的 a.txt：预览 Diff；再展开 C 点它自己的 a.txt：是另一个标签
	h.Sel = 2
	a.OnHistSelected(r)
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || !d.Change.Range || d.Key().Parent != cA.Hash || d.Where() != "a.txt @ "+cA.Short+" → "+cC.Short {
		t.Fatalf("比较的 Diff %+v", d)
	}
	a.OpenDiff(r, cp.Files[0], true)
	h.Sel = 5
	a.OnHistSelected(r)
	wait("C 的文件", func() bool { return h.FilesFor == cC.Hash })
	a.OpenDiff(r, h.Files[0], true)
	var keys []docs.Key
	for _, d := range a.AllDocs() {
		if d.Kind == docs.Diff {
			keys = append(keys, d.Key())
		}
	}
	if len(keys) != 2 || keys[0] == keys[1] || keys[1].Parent != cB.Hash {
		t.Errorf("比较的和提交自己的 a.txt 应是两个标签: %+v", keys)
	}

	// 选着比较里的文件时收起：落在标题上；标题只是选中，不切
	h.Sel = 3
	a.ToggleCompare(r)
	if cp.Open || h.Sel != 1 {
		t.Errorf("收起后 open %v 选中 %d", cp.Open, h.Sel)
	}
	a.OnHistSelected(r)
	if cp.Open {
		t.Error("方向键走到标题上不该展开")
	}
	a.ToggleCompare(r)

	// 同一时间的照点的顺序
	a.CompareCommits(r, cC, cB)
	if c := h.Compare; c.From.Hash != cC.Hash || c.To.Hash != cB.Hash {
		t.Errorf("同一时间应照点的顺序: %s → %s", c.From.Short, c.To.Short)
	}
	if h.Sel != 1 {
		t.Errorf("换一次比较，选着比较段的应落在标题上: %d", h.Sel)
	}
	wait("比较 C → B", func() bool { return !h.Compare.Loading })
	if fs := h.Compare.Files; len(fs) != 3 || fs[0].Path != "a.txt" || fs[0].Add != 0 || fs[0].Del != 1 || fs[1].Code != 'A' {
		t.Errorf("C → B: %+v", fs)
	}

	// × 清掉；起点留着
	h.Sel = 2
	a.ClearCompare(r)
	if h.Compare != nil || h.Sel != -1 || len(HistRows(r)) != 1+3+len(h.Files) || h.CompareFrom == nil {
		t.Errorf("清掉后 compare %v 选中 %d 行 %v", h.Compare, h.Sel, HistRows(r))
	}

	// 读不了：弹错、不留比较段
	a.CompareCommits(r, git.Commit{Hash: strings.Repeat("0", 40), Short: "0000000"}, cC)
	wait("比较失败", func() bool { return a.AlertOpen })
	if h.Compare != nil || !strings.Contains(a.AlertTitle, "比较提交失败") {
		t.Errorf("失败后 compare %v alert %q", h.Compare, a.AlertTitle)
	}
}

// chooseGitRows 像列表那样选中 Git 栏里这些路径的变更行（⌘点）：光标停在最后一个上。
func chooseGitRows(t *testing.T, a *App, paths ...string) {
	t.Helper()
	rows := a.GitRows()
	a.GitChosen.Clear()
	for _, p := range paths {
		i := rowIndex(t, rows, p)
		a.GitChosen.Add(rows[i].Key())
		a.GitSel = i
	}
}

func changePaths(rows []ChangeRow) []string {
	out := make([]string, len(rows))
	for i, cr := range rows {
		out[i] = cr.Repo.Name + ":" + cr.Change.Path
	}
	return out
}

// Git 栏多选：选中的变更按显示顺序、只算变更行；选了好几行时点击不预览，
// 重读状态不打断多选，换了标签、Esc 就收回到当前标签那一行；右键选中里的一行作用于全部，没选中的只对它。
func TestChosenChanges(t *testing.T) {
	a, wait, alpha, beta, _, _ := threeApp(t)
	rows := a.GitRows()
	note := rowIndex(t, rows, "notes.md")
	a.OnChangeSelected(false) // 先单击 notes.md：预览，选中就是它
	a.GitSel = note
	a.chooseGit(rows, note)
	a.OnChangeSelected(false)
	if len(a.Docs) != 1 || a.GitChosen.Len() != 1 || !a.GitChosen.Has(rows[note].Key()) {
		t.Fatalf("单击后 docs %d chosen %d", len(a.Docs), a.GitChosen.Len())
	}
	preview := a.CurrentDoc()

	chooseGitRows(t, a, "beta.go", "alpha.txt", "notes.md")
	group := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowGroup })
	a.GitChosen.Add(rows[group].Key()) // ⇧点一段会把分组标题也选上
	a.OnChangeSelected(false)
	if len(a.Docs) != 1 || a.CurrentDoc() != preview {
		t.Fatalf("多选时点击预览了: %d 个标签", len(a.Docs))
	}
	if got := changePaths(a.ChosenChanges()); !slices.Equal(got, []string{"alpha:alpha.txt", "alpha:notes.md", "beta:beta.go"}) {
		t.Fatalf("ChosenChanges %v", got)
	}
	if got := a.MenuChanges(rows[rowIndex(t, rows, "alpha.txt")]); len(got) != 3 {
		t.Errorf("右键选中里的一行作用于 %d 项", len(got))
	}
	a.RefreshRepo(alpha, WhyManual)
	a.RefreshRepo(beta, WhyManual)
	wait("重读", func() bool { return idle(alpha)() && idle(beta)() })
	if a.GitChosen.Len() != 4 || len(a.ChosenChanges()) != 3 {
		t.Fatalf("重读状态后多选只剩 %d", a.GitChosen.Len())
	}
	a.ClearGitChosen()
	if a.GitChosen.Len() != 1 || !a.GitChosen.Has(rows[note].Key()) || a.GitSel != note {
		t.Fatalf("Esc 后 chosen %d sel %d（应回到 %d）", a.GitChosen.Len(), a.GitSel, note)
	}

	// ⌘点把光标那行取消了：不预览
	chooseGitRows(t, a, "alpha.txt")
	a.GitSel = rowIndex(t, rows, "beta.go")
	a.OnChangeSelected(false)
	if a.CurrentDoc() != preview || !a.gitMulti {
		t.Errorf("取消光标那行后预览了")
	}
	if a.MenuChanges(rows[rowIndex(t, rows, "alpha.txt")]) != nil {
		t.Errorf("只选了一行时右键应只对它")
	}
	// 换了标签：多选收回到当前标签那一行
	chooseGitRows(t, a, "alpha.txt", "beta.go")
	a.OnChangeSelected(false)
	a.OpenFile(alpha, "alpha.txt", true)
	wait("文件", func() bool { return !a.CurrentDoc().Loading })
	if a.GitChosen.Len() != 0 || a.GitSel != -1 {
		t.Errorf("换了标签后还选着 %d 行（sel %d）", a.GitChosen.Len(), a.GitSel)
	}

	// 键盘走到分组标题上跳到下一项：选中跟过去
	g := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowGroup && cr.Repo == beta })
	a.gitPrev, a.GitSel = note, g
	a.chooseGit(rows, g)
	a.OnChangeSelected(false)
	if b := rowIndex(t, rows, "beta.go"); a.GitSel != b || !a.GitChosen.Has(rows[b].Key()) || a.GitChosen.Len() != 1 {
		t.Errorf("键盘跳过分组标题后 sel %d chosen %d", a.GitSel, a.GitChosen.Len())
	}
}

// 多选暂存：跨仓库，每个仓库一条 git add，未跟踪的也加。
func TestStageChosen(t *testing.T) {
	a, wait, alpha, beta, _, _ := threeApp(t)
	chooseGitRows(t, a, "alpha.txt", "notes.md", "beta.go")
	a.StageChanges(a.ChosenChanges())
	wait("暂存", func() bool { return idle(alpha)() && idle(beta)() })
	if alpha.Status.Staged != 2 || alpha.Status.Unstaged+alpha.Status.Untracked != 0 || beta.Status.Staged != 1 {
		t.Fatalf("暂存后 alpha %+v beta %+v", alpha.Status, beta.Status)
	}
	if got := outputsFor(a, "alpha"); len(got) != 1 || got[0] != "$ git add -A -- alpha.txt notes.md" {
		t.Errorf("alpha 的输出 %q", got)
	}
}

// 多选取消暂存：已暂存的一个仓库一条 git restore --staged；没暂存的不动。
func TestUnstageChosen(t *testing.T) {
	a, wait, alpha, beta, _, roots := threeApp(t)
	roots[0].Git("add", "alpha.txt")
	roots[1].Git("add", "beta.go")
	a.RefreshRepo(alpha, WhyManual)
	a.RefreshRepo(beta, WhyManual)
	wait("重读", func() bool { return idle(alpha)() && idle(beta)() })
	chooseGitRows(t, a, "alpha.txt", "notes.md", "beta.go")
	rows := a.ChosenChanges()
	if n := CountChanges(rows, CanUnstage); n != 2 {
		t.Fatalf("能取消暂存的 %d 项", n)
	}
	a.UnstageChanges(rows)
	wait("取消暂存", func() bool { return idle(alpha)() && idle(beta)() })
	if alpha.Status.Staged+beta.Status.Staged != 0 || alpha.Status.Unstaged != 1 || alpha.Status.Untracked != 1 || beta.Status.Unstaged != 1 {
		t.Fatalf("取消暂存后 alpha %+v beta %+v", alpha.Status, beta.Status)
	}
}

// 多选丢弃：只问一次，然后每个仓库一条 restore、一条 clean；已暂存的不动。
func TestDiscardChosen(t *testing.T) {
	a, wait, alpha, beta, _, roots := threeApp(t)
	roots[1].Write("staged.go", "package beta\n")
	roots[1].Git("add", "staged.go")
	a.RefreshRepo(beta, WhyManual)
	wait("重读", idle(beta))
	chooseGitRows(t, a, "alpha.txt", "notes.md", "beta.go", "staged.go")
	a.DiscardChanges(a.ChosenChanges())
	if !a.ConfirmOpen || a.Confirm.Title != "丢弃 2 项未暂存的改动、删除 1 个未跟踪的文件？" {
		t.Fatalf("问 %+v", a.Confirm)
	}
	if want := "  alpha.txt\n  notes.md\n  beta.go\n"; !strings.HasPrefix(a.Confirm.Message, want) {
		t.Errorf("问的内容 %q", a.Confirm.Message)
	}
	ask := a.Confirm
	a.ConfirmOpen = false
	ask.Fn()
	wait("丢弃", func() bool { return idle(alpha)() && idle(beta)() })
	if a.Confirm != ask {
		t.Errorf("又问了一次: %+v", a.Confirm)
	}
	if alpha.Status.Count != 0 || beta.Status.Count != 1 || beta.Status.Staged != 1 {
		t.Fatalf("丢弃后 alpha %+v beta %+v", alpha.Status, beta.Status)
	}
	if got := mustRead(t, roots[0].Root+"/alpha.txt"); got != "一\n二\n三\n" {
		t.Errorf("alpha.txt 没恢复: %q", got)
	}
	if got := outputsFor(a, "alpha"); !slices.Equal(got, []string{"$ git restore -- alpha.txt", "$ git clean -fdq -- notes.md"}) {
		t.Errorf("alpha 的输出 %q", got)
	}
}

// 多选回车：都开成固定的 Diff（多于 OpenAsk 个先问），之后多选还在。
func TestOpenDiffsChosen(t *testing.T) {
	a, wait, alpha, _, _, roots := threeApp(t)
	chooseGitRows(t, a, "alpha.txt", "notes.md", "beta.go")
	a.OnChangeSelected(true)
	if len(a.Docs) != 3 || slices.ContainsFunc(a.Docs, func(d *docs.Doc) bool { return d.Kind != docs.Diff || !d.Pinned }) {
		t.Fatalf("回车后的标签 %v", titlesOf(a.Docs))
	}
	if a.CurrentDoc().Change.Path != "beta.go" || a.Current().Name != "beta" {
		t.Errorf("当前标签 %s，当前仓库 %s", a.CurrentDoc().Change.Path, a.Current().Name)
	}
	if a.GitChosen.Len() != 3 || !a.gitMulti {
		t.Errorf("开完后多选只剩 %d", a.GitChosen.Len())
	}
	a.syncChangeSel()
	if a.GitChosen.Len() != 3 {
		t.Errorf("再对一次后多选只剩 %d", a.GitChosen.Len())
	}

	for i := range OpenAsk + 1 { // 放在仓库根上：未跟踪的目录在 git status 里只是一行
		roots[0].Write("many-"+string(rune('a'+i))+".txt", "x\n")
	}
	a.RefreshRepo(alpha, WhyManual)
	wait("重读", idle(alpha))
	a.GitChosen.Clear()
	for _, cr := range a.GitRows() {
		if cr.Kind == RowChange && cr.Repo == alpha {
			a.GitChosen.Add(cr.Key())
		}
	}
	a.OnChangeSelected(true)
	if !a.ConfirmOpen || a.Confirm.Title != "打开 23 个 Diff？" {
		t.Fatalf("多于 %d 个时问 %+v", OpenAsk, a.Confirm)
	}
}

// 右栏看标签的历史：从"所有仓库"切到"当前仓库"、右栏打开，从标签的提交往前读，提交图照画；
// HEAD 是标签的提交、改动数不算；在 Git 栏里点一项改动不回去（那是工作树历史的规矩）；
// 标签挪了跟着重读，再选一次同一个标签、× 或标签删了都回到自己的历史。
func TestRefHistoryTag(t *testing.T) {
	repo := testutil.NewRepo(t, "tagged")
	repo.Write("a.txt", "1\n")
	repo.Commit("c1")
	c1 := repo.Git("rev-parse", "HEAD")
	repo.Git("tag", "v1")
	repo.Write("a.txt", "2\n")
	repo.Commit("c2")
	c2 := repo.Git("rev-parse", "HEAD")
	repo.Write("a.txt", "3\n")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if len(r.Tags) != 1 || r.Tags[0].Name != "v1" {
		t.Fatalf("标签 %+v", r.Tags)
	}
	a.St.HistAll, a.St.RightBar = true, false
	h := &r.Hist
	loaded := func() bool { return h.Loaded && !h.Loading }

	a.ShowTagHistory(r, r.Tags[0])
	if a.St.HistAll || !a.St.RightBar || h.RefKind != RefTag || h.RefName != "v1" || h.Ref != c1 || h.RefPath != "" {
		t.Fatalf("选标签后：all=%v right=%v %v %q %q %q", a.St.HistAll, a.St.RightBar, h.RefKind, h.RefName, h.Ref, h.RefPath)
	}
	wait("标签的历史", loaded)
	if len(h.Commits) != 1 || h.Commits[0].Subject != "c1" || !h.HasGraph() || r.RefHead() != c1 || r.RefDirty() != -1 || !r.ViewingTag("v1") {
		t.Fatalf("标签的历史：%+v head=%q dirty=%d", h.Commits, r.RefHead(), r.RefDirty())
	}

	pickGit(a, gitRow(a, func(cr ChangeRow) bool { return cr.Kind == RowChange }))
	a.GitHover = -1
	a.OnChangeSelected(false)
	if h.RefKind != RefTag {
		t.Errorf("点一项改动不该回到自己的历史：%v", h.RefKind)
	}

	repo.Git("tag", "-f", "v1", c2)
	a.RefreshRepo(r, WhyManual)
	wait("标签挪了", func() bool { return idle(r)() && loaded() && h.Ref == c2 && len(h.Commits) == 2 })

	a.ShowTagHistory(r, r.Tags[0])
	wait("再选一次回去", loaded)
	if h.RefKind != RefNone || h.Ref != "" || h.RefName != "" || len(h.Commits) != 2 || r.ViewingTag("v1") {
		t.Errorf("再选一次之后：%v %q %+v", h.RefKind, h.Ref, h.Commits)
	}

	a.ShowTagHistory(r, r.Tags[0])
	a.ClearRefHistory(r)
	if h.RefKind != RefNone || h.Ref != "" {
		t.Errorf("× 之后：%v %q", h.RefKind, h.Ref)
	}

	a.ShowTagHistory(r, r.Tags[0])
	repo.Git("tag", "-d", "v1")
	a.RefreshRepo(r, WhyManual)
	wait("标签删了", func() bool { return idle(r)() && loaded() && h.RefKind == RefNone && len(h.Commits) == 2 })
}

// 多选丢弃里的子模块：检出的提交变了的照旧检出回记录的提交；只有里面改动 / 未跟踪文件的不算进 N 项、
// 单说一句、什么都不动；选中的全是这种时不问，只提示。
func TestDiscardChosenSubs(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	lib := testutil.NewRepo(t, "lib")
	lib.Write("a.txt", "a\n")
	lib.Commit("a")
	lib.Write("a.txt", "b\n")
	lib.Commit("b")
	m := testutil.NewRepo(t, "main")
	m.Write("README.md", "main\n")
	m.Commit("初始")
	for _, p := range []string{"s/c", "s/m", "s/u"} {
		m.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", lib.Root, p)
	}
	m.Commit("加子模块")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = filepath.Join(m.Root, dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("s/c", "checkout", "-q", "HEAD~1")
	m.Write("s/m/a.txt", "改了\n")
	m.Write("s/u/new.txt", "新\n")
	m.Write("README.md", "改了\n")
	a, wait := newTestApp(t, testutil.Workspace(t, m.Root))
	t.Cleanup(a.Close)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("git status", idle(r))

	inner := "其中 2 个子模块只有里面的改动，这里不动（到子模块自己的仓库里丢弃）"
	chooseGitRows(t, a, "README.md", "s/c", "s/m", "s/u")
	a.DiscardChanges(a.ChosenChanges())
	if !a.ConfirmOpen || a.Confirm.Title != "丢弃 2 项未暂存的改动？" {
		t.Fatalf("问 %+v", a.Confirm)
	}
	if msg := a.Confirm.Message; !strings.HasPrefix(msg, "  README.md\n  c\n") || !strings.Contains(msg, "其中 1 个是子模块") || !strings.HasSuffix(msg, "\n"+inner) {
		t.Errorf("问的内容 %q", msg)
	}
	ask := a.Confirm
	a.ConfirmOpen = false
	ask.Fn()
	wait("丢弃", func() bool { return idle(r)() && len(outputsFor(a, "main")) == 2 })
	if got := outputsFor(a, "main"); !slices.Equal(got, []string{"$ git restore -- README.md", "$ git submodule update -- s/c"}) {
		t.Errorf("输出 %q", got)
	}
	a.RefreshRepo(r, WhyManual)
	wait("重读", idle(r))
	subs := map[string]string{}
	for _, ch := range r.Status.Changes {
		subs[ch.Path] = ch.Sub
	}
	if len(subs) != 2 || subs["s/m"] != ".M." || subs["s/u"] != "..U" {
		t.Fatalf("丢弃后还剩 %q", subs)
	}

	a.Toast = ""
	chooseGitRows(t, a, "s/m", "s/u")
	a.DiscardChanges(a.ChosenChanges())
	if a.ConfirmOpen || a.Toast != inner {
		t.Fatalf("全是里面改动的子模块：confirm=%v toast=%q", a.ConfirmOpen, a.Toast)
	}
}
