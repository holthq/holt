package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// threeApp 读 testutil.ThreeRepos 的工作区，等三个仓库的状态都读完。
func threeApp(t *testing.T) (a *App, wait func(string, func() bool), alpha, beta, gamma *Repo, roots [3]*testutil.Repo) {
	t.Helper()
	ra, rb, rc, ws := testutil.ThreeRepos(t)
	a, wait = newTestApp(t, ws)
	if len(a.Repos) != 3 {
		t.Fatalf("有 %d 个仓库", len(a.Repos))
	}
	alpha, beta, gamma = a.Repos[0], a.Repos[1], a.Repos[2]
	for _, r := range a.Repos {
		a.RefreshRepo(r, WhyManual)
	}
	wait("三个仓库的状态", func() bool { return idle(alpha)() && idle(beta)() && idle(gamma)() })
	return a, wait, alpha, beta, gamma, [3]*testutil.Repo{ra, rb, rc}
}

// rowsSummary 把 Git 栏的行写成一串好比较的字："#仓库 / =分组 / 路径 / ~说明 / ✎提交框"。
func rowsSummary(rows []ChangeRow) string {
	var b []string
	for _, cr := range rows {
		switch cr.Kind {
		case RowRepo:
			b = append(b, "#"+cr.Repo.Name)
		case RowGroup:
			b = append(b, "="+cr.Title)
		case RowChange:
			b = append(b, cr.Change.Path)
		case RowNote:
			b = append(b, "~"+cr.Title)
		case RowCommit:
			b = append(b, "✎")
		}
	}
	return strings.Join(b, " ")
}

// Git 栏同时列出所有仓库：有改动的展开、干净的收成段头；折叠按仓库；键盘走过分组标题和提交框，
// 点它们不算选中；选中哪个仓库的变更，当前仓库就切到哪个。
func TestGitRowsAllRepos(t *testing.T) {
	a, _, alpha, beta, gamma, _ := threeApp(t)
	if a.Current() != alpha {
		t.Fatalf("当前仓库应是第一个: %s", a.Current().Name)
	}
	rows := a.GitRows()
	// 提交框在分组上面；未跟踪的 notes.md 接在未暂存的 alpha.txt 后面、同一组
	want := "#alpha ✎ =未暂存 alpha.txt notes.md #beta ✎ =未暂存 beta.go #gamma"
	if got := rowsSummary(rows); got != want {
		t.Fatalf("Git 栏的行:\n got %s\nwant %s", got, want)
	}
	if !alpha.GitOpen() || !beta.GitOpen() || gamma.GitOpen() {
		t.Errorf("默认折叠: alpha=%v beta=%v gamma=%v", alpha.GitOpen(), beta.GitOpen(), gamma.GitOpen())
	}
	if a.AllGitOpen() {
		t.Error("gamma 收着，AllGitOpen 应是 false")
	}

	// 折叠 / 展开按仓库，干净的展开后是一行说明
	a.ToggleGitRepo(alpha)
	a.ToggleGitRepo(gamma)
	want = "#alpha #beta ✎ =未暂存 beta.go #gamma" // 干净由段头的勾表示，没有说明行
	if got := rowsSummary(a.GitRows()); got != want {
		t.Fatalf("折叠后:\n got %s\nwant %s", got, want)
	}
	a.SetAllGitOpen(true)
	if !a.AllGitOpen() || len(a.GitRows()) != len(rows) { // 干净的 gamma 展开了也没有说明行
		t.Errorf("全部展开后 %s", rowsSummary(a.GitRows()))
	}
	a.SetAllGitOpen(false)
	if got := rowsSummary(a.GitRows()); got != "#alpha #beta #gamma" {
		t.Errorf("全部收起后 %s", got)
	}
	a.SetAllGitOpen(true)
	rows = a.GitRows()

	// 键盘从 notes.md 往下：跳过段头、提交框、分组标题，落到 beta.go，当前仓库切到 beta
	notes := rowIndex(t, rows, "notes.md")
	pickGit(a, notes)
	a.gitPrev, a.GitHover = notes, -1
	a.OnChangeSelected(false)
	pickGit(a, notes+1) // beta 的段头
	a.OnChangeSelected(false)
	if b := rowIndex(t, rows, "beta.go"); a.GitSel != b {
		t.Fatalf("往下应跳到 beta.go（%d），停在 %d", b, a.GitSel)
	}
	if a.Current() != beta || a.CurrentDoc() == nil || a.CurrentDoc().Root != beta.Root {
		t.Errorf("选 beta.go 后当前仓库 %s", a.Current().Name)
	}
	// 往上：跳回 notes.md，当前仓库回到 alpha
	pickGit(a, a.GitSel-1) // beta 的分组标题
	a.OnChangeSelected(false)
	if a.GitSel != notes || a.Current() != alpha {
		t.Errorf("往上应回到 notes.md（%d）: sel=%d 当前 %s", notes, a.GitSel, a.Current().Name)
	}
	// 鼠标点在分组标题上：选中退回原来那行，什么都不开
	docs := len(a.Docs)
	g := rowIndex(t, rows, "alpha.txt") - 1 // alpha 的"未暂存"标题
	if rows[g].Kind != RowGroup {
		t.Fatalf("alpha.txt 上面应是分组标题: %s", rowsSummary(rows))
	}
	pickGit(a, g)
	a.GitHover = g
	a.OnChangeSelected(false)
	if a.GitSel != notes || len(a.Docs) != docs {
		t.Errorf("点分组标题后 sel=%d（应退回 %d）", a.GitSel, notes)
	}
}

func rowIndex(t *testing.T, rows []ChangeRow, path string) int {
	t.Helper()
	for i, cr := range rows {
		if cr.Kind == RowChange && cr.Change.Path == path {
			return i
		}
	}
	t.Fatalf("Git 栏里没有 %s: %s", path, rowsSummary(rows))
	return -1
}

// ⌘1–9 跳到第 n 段：设成当前仓库、收着的展开、文件树展开它的根。
func TestJumpRepo(t *testing.T) {
	a, _, alpha, _, gamma, _ := threeApp(t)
	a.Exec("repo.3")
	if a.Current() != gamma || !gamma.GitOpen() || !gamma.Expanded {
		t.Fatalf("⌘3 后当前 %s open=%v expanded=%v", a.Current().Name, gamma.GitOpen(), gamma.Expanded)
	}
	if got := rowsSummary(a.GitRows()); !strings.HasSuffix(got, "#gamma") {
		t.Errorf("跳到干净的 gamma 应展开它: %s", got)
	}
	a.Exec("repo.1")
	if a.Current() != alpha {
		t.Errorf("⌘1 后当前 %s", a.Current().Name)
	}
}

// 在第二个仓库暂存、提交只动它：第一个仓库的状态和历史不变；提交后它的提交框收起来。
func TestCommitInSecondRepo(t *testing.T) {
	a, wait, alpha, beta, _, roots := threeApp(t)
	alphaHead := roots[0].Git("rev-parse", "HEAD")
	if beta.CommitShown() {
		t.Fatal("没暂存时提交框应收着")
	}
	a.Stage(beta, "beta.go")
	wait("暂存 beta.go", func() bool { return idle(beta)() && beta.Status.Staged == 1 })
	if !beta.CommitShown() || alpha.CommitShown() {
		t.Errorf("暂存后 beta 的提交框应展开、alpha 的收着: beta=%v alpha=%v", beta.CommitShown(), alpha.CommitShown())
	}
	beta.CommitMsg = "beta 3"
	a.Commit(beta)
	wait("提交", func() bool { return idle(beta)() && beta.Status.Count == 0 })
	if got := roots[1].Git("log", "-1", "--format=%s"); got != "beta 3" {
		t.Errorf("beta 最新提交 %q", got)
	}
	if got := roots[0].Git("rev-parse", "HEAD"); got != alphaHead {
		t.Errorf("alpha 的 HEAD 变了: %s → %s", alphaHead, got)
	}
	a.RefreshRepo(alpha, WhyManual)
	wait("alpha 的状态", idle(alpha))
	if alpha.Status.Unstaged != 1 || alpha.Status.Untracked != 1 || alpha.Status.Staged != 0 {
		t.Errorf("alpha 的状态变了: %+v", alpha.Status)
	}
	if beta.CommitShown() || beta.CommitMsg != "" {
		t.Errorf("提交后提交框应收起: open=%v msg=%q", beta.CommitOpen, beta.CommitMsg)
	}
	if got := rowsSummary(a.GitRows()); !strings.Contains(got, "#beta #gamma") {
		t.Errorf("beta 干净了应收成段头: %s", got)
	}
}

// 冲突单独一组排在最前；它的 diff 是 HEAD ↔ 工作区；"全部暂存"先问。
func TestConflictGroup(t *testing.T) {
	r := testutil.NewRepo(t, "merge")
	r.Write("x.txt", "原来\n")
	r.Commit("初始提交")
	r.Git("switch", "-q", "-c", "other")
	r.Write("x.txt", "other 改的\n")
	r.Commit("other")
	r.Git("switch", "-q", "main")
	r.Write("x.txt", "main 改的\n")
	r.Write("y.txt", "新文件\n")
	r.Git("add", "x.txt", "y.txt")
	r.Git("commit", "-q", "-m", "main")
	r.Write("z.txt", "未跟踪\n")
	r.Write("y.txt", "新文件\n改了\n")
	if out, err := runGitIn(r.Root, "merge", "-q", "other"); err == nil {
		t.Fatalf("应该冲突: %s", out)
	}
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Current()
	a.RefreshRepo(repo, WhyManual)
	wait("状态", idle(repo))
	if repo.Status.Conflicts != 1 || repo.Status.Changes[0].Kind != git.Conflict || repo.Status.Changes[0].Path != "x.txt" {
		t.Fatalf("冲突没认出来: %+v", repo.Status)
	}
	want := "#merge ✎ =冲突 x.txt =未暂存 y.txt z.txt"
	if got := rowsSummary(a.GitRows()); got != want {
		t.Errorf("Git 栏:\n got %s\nwant %s", got, want)
	}
	if i := slices.IndexFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Kind == RowGroup && cr.Group == GroupUnstaged }); i < 0 ||
		a.GitRows()[i].Count != 2 {
		t.Errorf("未暂存一组应是 2 条（含未跟踪的）")
	}
	dc, err := git.LoadDiffContent(context.Background(), repo.Root, repo.Status.Changes[0])
	if err != nil || !strings.Contains(string(dc.Old), "main 改的") || !strings.Contains(string(dc.New), "<<<<<<<") {
		t.Errorf("冲突的 diff 应是 HEAD ↔ 带冲突标记的工作区: err=%v old=%q new=%q", err, dc.Old, dc.New)
	}
	d := a.OpenDiff(repo, repo.Status.Changes[0], false)
	wait("冲突的 diff", func() bool { return !d.Loading })
	if d.DiffSt == nil || d.Info != "" {
		t.Errorf("冲突的 Diff 标签没画出来: info=%q", d.Info)
	}
	// "未暂存"组头的 +：暂存改动和未跟踪的文件，不碰冲突的（暂存了就算已解决）
	a.StageUnstaged(repo)
	wait("暂存这一组", func() bool { return idle(repo)() && repo.Status.Unstaged+repo.Status.Untracked == 0 })
	if repo.Status.Conflicts != 1 || repo.Status.Staged != 2 {
		t.Fatalf("组头的 + 不该暂存冲突的文件: %+v", repo.Status)
	}
	a.StageAll(repo)
	if !a.ConfirmOpen || a.Confirm.Button != "全部暂存" || repo.Busy != "" {
		t.Fatalf("有冲突时全部暂存应先问: %+v busy=%q", a.Confirm, repo.Busy)
	}
	a.Confirm.Fn()
	wait("全部暂存", func() bool { return idle(repo)() && repo.Status.Conflicts == 0 })
	if repo.Status.Staged != 3 {
		t.Errorf("全部暂存后 %+v", repo.Status)
	}
}

// 没有冲突时，"未暂存"组头的 + 是一条不带路径的 git add -A（上万个文件按路径传会超出命令行长度限制）：
// 已跟踪的改动、删掉的、未跟踪的都暂存，已暂存的照旧。
func TestStageUnstagedNoConflict(t *testing.T) {
	r := testutil.NewRepo(t, "stage")
	r.Write("a.txt", "a\n")
	r.Write("gone.txt", "g\n")
	r.Write("s.txt", "s\n")
	r.Commit("初始提交")
	r.Write("a.txt", "a 改了\n")
	r.Write("s.txt", "s 改了\n")
	r.Git("add", "s.txt")
	if err := os.Remove(filepath.Join(r.Root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	r.Write("new/n.txt", "未跟踪\n")
	r.Write("u.txt", "未跟踪\n")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Current()
	a.RefreshRepo(repo, WhyManual)
	wait("状态", idle(repo))
	if repo.Status.Staged != 1 || repo.Status.Unstaged != 2 || repo.Status.Untracked != 2 || repo.Status.Conflicts != 0 {
		t.Fatalf("起始状态不对: %+v", repo.Status)
	}
	a.StageUnstaged(repo)
	wait("暂存这一组", func() bool { return idle(repo)() && repo.Status.Unstaged+repo.Status.Untracked == 0 })
	if repo.Status.Staged != 5 {
		t.Errorf("应全部暂存（含未跟踪的）: %+v", repo.Status)
	}
	if n := len(a.Output); n == 0 || a.Output[n-1].Title != "$ git add -A" {
		t.Errorf("没有冲突时应是不带路径的 git add -A: %+v", a.Output)
	}
}

// runGitIn 在 dir 里跑 git（测试用，不 Fatal）。
func runGitIn(dir string, args ...string) (string, error) {
	out, err := git.Run(dir, args...)
	return string(out), err
}

// "所有仓库"的历史：各仓库的提交按时间交错，置顶有改动的仓库；有仓库那一页读完了、后面还有，
// 就停在那里；选中提交展开它（别的仓库展开的收起），当前仓库切到它；有改动的仓库那行跳到 Git 栏。
func TestAllHistory(t *testing.T) {
	a, wait, alpha, beta, gamma, _ := threeApp(t)
	a.EnsureAllHistory()
	wait("三个仓库的历史", func() bool { return !a.AllHistLoading() && a.AllHistDone() })
	rows := a.AllHistRows()
	var got []string
	for _, row := range rows {
		switch row.Kind {
		case AllHeader:
			got = append(got, "#"+row.Title)
		case AllDirty:
			got = append(got, "*"+row.Repo.Name)
		case AllCommit:
			if !strings.HasPrefix(row.Commit().Subject, row.Repo.Name+" ") {
				t.Errorf("%q 的仓库是 %s", row.Commit().Subject, row.Repo.Name)
			}
			got = append(got, row.Commit().Subject)
		}
	}
	want := "#未提交的改动 *alpha *beta #提交 gamma 1 alpha 3 beta 2 alpha 2 beta 1 alpha 1"
	if s := strings.Join(got, " "); s != want {
		t.Fatalf("合并的历史:\n got %s\nwant %s", s, want)
	}
	if n := AllHistShown(rows); n != 6 {
		t.Errorf("排出 %d 条", n)
	}

	// beta 那页"还没读到头"：beta 1 之后的 alpha 1 可能比 beta 没读的更早，先不排
	beta.Hist.Done = false
	if a.AllHistDone() || AllHistShown(a.AllHistRows()) != 5 {
		t.Errorf("beta 没读完时应排到 beta 1 为止: %d 条", AllHistShown(a.AllHistRows()))
	}
	beta.Hist.Done = true

	// 选中 beta 2：当前仓库切到 beta，展开它的文件
	pick := func(subject string) {
		t.Helper()
		for i, row := range a.AllHistRows() {
			if row.Kind == AllCommit && row.Commit().Subject == subject {
				a.HistAllSel = i
				a.OnAllHistSelected()
				return
			}
		}
		t.Fatalf("没有 %s", subject)
	}
	pick("beta 2")
	if a.Current() != beta {
		t.Fatalf("选 beta 2 后当前 %s", a.Current().Name)
	}
	wait("beta 2 的文件", func() bool { return beta.Hist.FilesFor != "" && !beta.Hist.FilesLoading })
	rows = a.AllHistRows()
	if row := rows[a.HistAllSel+1]; row.Kind != AllFile || row.Repo != beta || beta.Hist.Files[row.File].Path != "beta.go" {
		t.Errorf("beta 2 下面应是它的 beta.go: %+v", row)
	}
	pick("alpha 2")
	if a.Current() != alpha || beta.Hist.Expanded != "" || alpha.Hist.Expanded == "" {
		t.Errorf("选 alpha 2 后当前 %s，beta 展开 %q", a.Current().Name, beta.Hist.Expanded)
	}
	if row := a.AllHistRows()[a.HistAllSel]; row.Commit().Subject != "alpha 2" {
		t.Errorf("选中行跟丢了: %s", row.Commit().Subject)
	}

	// 有改动的仓库那行：侧栏切到 Git 栏，当前仓库切过去
	a.St.SidebarMode = workspace.ModeFiles
	a.HistAllSel = 2 // *beta
	a.OnAllHistSelected()
	if a.St.SidebarMode != workspace.ModeGit || a.Current() != beta {
		t.Errorf("点 beta 的未提交改动后 mode=%q 当前 %s", a.St.SidebarMode, a.Current().Name)
	}
	_ = gamma
}

// 右栏的"当前仓库 | 所有仓库"记进 state.json；旧文件没有这一项时是"当前仓库"。
func TestHistAllPersists(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	dir := t.TempDir()
	a, _ := newTestAppIn(t, ws, dir)
	if a.St.HistAll {
		t.Fatal("默认应是当前仓库")
	}
	a.SetHistAll(true)
	b, _ := newTestAppIn(t, ws, dir)
	if !b.St.HistAll {
		t.Error("重开后没记住所有仓库")
	}
}

// Git 栏里点过箭头的仓库段记进 state.json，重开还是那样；没点过的照旧按有没有改动，旧文件没有这一项。
func TestGitFoldPersists(t *testing.T) {
	ra, _, rc, ws := testutil.ThreeRepos(t)
	dir := t.TempDir()
	open := func() (*App, *Repo, *Repo) {
		a, wait := newTestAppIn(t, ws, dir)
		for _, r := range a.Repos {
			a.RefreshRepo(r, WhyManual)
		}
		wait("状态", func() bool { return idle(a.Repos[0])() && idle(a.Repos[2])() })
		return a, a.Repos[0], a.Repos[2]
	}
	a, alpha, gamma := open()
	if !alpha.GitOpen() || gamma.GitOpen() {
		t.Fatal("默认应是有改动的展开、没改动的收起")
	}
	a.ToggleGitRepo(alpha)
	a.ToggleGitRepo(gamma)
	a.Close()
	st, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Sessions[ws].Git; len(got) != 2 || got[ra.Root] != "closed" || got[rc.Root] != "open" {
		t.Fatalf("存的是 %v", got)
	}
	b, alpha, gamma := open()
	if alpha.GitOpen() || !gamma.GitOpen() {
		t.Error("重开后折叠没放回去")
	}
	for _, r := range b.Repos {
		r.GitFold = FoldAuto
	}
	b.SaveState()
	st, _ = workspace.LoadState(dir)
	if got := st.Sessions[ws].Git; got != nil {
		t.Errorf("都没点过时不该记: %v", got)
	}
}

// Git 栏里点分组标题收起这一组：标题和条数还在，下面的文件不列；收起的组记进 state.json，重开还是收着；
// 文件里认不得的分组键读的时候不要，什么都没收起时不记。
func TestGitGroupFold(t *testing.T) {
	ra, _, _, ws := testutil.ThreeRepos(t)
	dir := t.TempDir()
	open := func() (*App, *Repo) {
		a, wait := newTestAppIn(t, ws, dir)
		a.RefreshRepo(a.Repos[0], WhyManual)
		wait("状态", idle(a.Repos[0]))
		return a, a.Repos[0]
	}
	// rowsOf 是 alpha 段里每组的 [标题行的条数, 列出来的文件数]。
	rowsOf := func(a *App) map[string][2]int {
		m := map[string][2]int{}
		group := ""
		for _, cr := range a.GitRows() {
			if cr.Repo.Root != ra.Root {
				continue
			}
			switch cr.Kind {
			case RowGroup:
				group = cr.Group
				m[group] = [2]int{cr.Count, 0}
			case RowChange:
				n := m[group]
				n[1]++
				m[group] = n
			}
		}
		return m
	}
	a, alpha := open()
	// 未暂存的改动和未跟踪的文件同在一组
	if got := rowsOf(a); len(got) != 1 || got[GroupUnstaged] != [2]int{2, 2} {
		t.Fatalf("默认应都展开: %v", got)
	}
	a.ToggleGitGroup(alpha, GroupUnstaged)
	if got := rowsOf(a); got[GroupUnstaged] != [2]int{2, 0} {
		t.Fatalf("收起未暂存后: %v", got)
	}
	a.Close()
	st, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Sessions[ws].Groups[ra.Root]; !slices.Equal(got, []string{GroupUnstaged}) {
		t.Fatalf("存的是 %v", st.Sessions[ws].Groups)
	}
	s := st.Sessions[ws]
	s.Groups[ra.Root] = append(s.Groups[ra.Root], "bogus", "untracked") // untracked 是以前单独一组时的键：忽略
	st.Sessions[ws] = s
	if err := workspace.SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
	b, alpha := open()
	if alpha.GroupOpen(GroupUnstaged) || len(alpha.GroupToggled) != 1 {
		t.Fatalf("重开后应只收着未暂存: %v", alpha.GroupToggled)
	}
	if got := rowsOf(b); got[GroupUnstaged] != [2]int{2, 0} {
		t.Fatalf("重开后: %v", got)
	}
	b.ToggleGitGroup(alpha, GroupUnstaged)
	b.Close()
	st, _ = workspace.LoadState(dir)
	if got := st.Sessions[ws].Groups; got != nil {
		t.Errorf("都展开时不该记: %v", got)
	}
}

// 贮藏组默认收着（标题和条数还在），点开了才列出每条；点开的记进 state.json（"stash"），重开还开着，收回去就不记。
func TestStashGroupDefault(t *testing.T) {
	repo := testutil.NewRepo(t, "stashes")
	repo.Write("a.txt", "原来\n")
	repo.Commit("初始提交")
	for _, s := range []string{"一", "二"} {
		repo.Write("a.txt", s+"\n")
		repo.Git("stash", "push", "-q", "-m", "第"+s+"个")
	}
	ws := testutil.Workspace(t, repo.Root)
	dir := t.TempDir()
	open := func() (*App, *Repo) {
		a, wait := newTestAppIn(t, ws, dir)
		r := a.Repos[0]
		r.GitFold = FoldOpen // 干净的仓库段默认收着：打开它才看得到分组
		a.RefreshRepo(r, WhyManual)
		wait("贮藏", func() bool { return idle(r)() && len(r.Extra.Stashes) == 2 })
		return a, r
	}
	// stashRows 是贮藏标题行的条数和列出来的贮藏数。
	stashRows := func(a *App) (count, listed int) {
		for _, cr := range a.GitRows() {
			switch cr.Kind {
			case RowStashes:
				count = cr.Count
			case RowStash:
				listed++
			}
		}
		return
	}
	a, r := open()
	if count, listed := stashRows(a); count != 2 || listed != 0 || r.GroupOpen(GroupStash) {
		t.Fatalf("贮藏默认应收着、标题上有 2 条: 条数 %d 列了 %d", count, listed)
	}
	a.ToggleGitGroup(r, GroupStash)
	if _, listed := stashRows(a); listed != 2 {
		t.Fatalf("点开后应列出 2 条，列了 %d", listed)
	}
	a.Close()
	st, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Sessions[ws].Groups[repo.Root]; !slices.Equal(got, []string{GroupStash}) {
		t.Fatalf("存的是 %v", st.Sessions[ws].Groups)
	}
	b, r := open()
	if _, listed := stashRows(b); listed != 2 {
		t.Fatalf("重开后贮藏应还开着，列了 %d", listed)
	}
	b.ToggleGitGroup(r, GroupStash)
	b.Close()
	if st, _ = workspace.LoadState(dir); st.Sessions[ws].Groups != nil {
		t.Errorf("收回默认后不该记: %v", st.Sessions[ws].Groups)
	}
}
