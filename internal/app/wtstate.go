package app

import (
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是 Git 栏里"工作树就是 agent 的工位"那一套：每个工作树算成七种状态之一（WtStateOf），组里按状态排，
// 收起的组头只放最需要你的那一档；点一个工作树展开看它比主分支多出的改动（main...分支），能审查、合并；
// 侧栏顶上的"需要你"条列出所有仓库里在等你、可合并的工作树，上一个 / 下一个在它们之间跳。

// WtState 是一个工作树在 Git 栏里的状态，值越小越需要你（组里就按这个顺序排）。
type WtState int

const (
	WtWaiting   WtState = iota // agent 在等你
	WtRunning                  // agent 在跑
	WtMergeable                // 没有 agent 在跑、工作区干净、比主分支领先 ≥ 1
	WtDirty                    // 有未提交的改动，没有 agent 在跑
	WtIdle                     // 前台是 agent，但闲着
	WtPlain                    // 别的情况（没有 agent）
	WtMerged                   // 分支已经合进主分支、主分支又往前走了（领先 0、落后 ≥ 1），目录没了的（可清理）也算；
	//                            刚建、还没提交的工作树（领先、落后都是 0）不算已合并，是 WtPlain
)

// WtStateOf 是工作树 w 的状态：ag 是它上面的 agent（WtAgent），info 是懒算的改动数和领先 / 落后（还没算为 nil）。
// 主工作树不会是可合并、已合并（它就是比较的基准）。
func WtStateOf(ag AgentKind, w Worktree, info *WtInfo) WtState {
	switch ag {
	case AgentWaiting:
		return WtWaiting
	case AgentRunning:
		return WtRunning
	}
	if w.Prunable && !w.Main {
		return WtMerged
	}
	known := info != nil && info.Err == nil && !w.Bare
	switch {
	case known && info.Dirty > 0:
		return WtDirty
	case known && !w.Main && info.Ahead > 0:
		return WtMergeable
	case ag == AgentIdle:
		return WtIdle
	case known && !w.Main && w.Branch != "" && w.Head != "" && info.Ahead == 0 && info.Behind > 0:
		return WtMerged
	}
	return WtPlain
}

// SortWts 把一个仓库的工作树按状态排（稳定：同一档里保持原来的顺序）：主工作树仍然在最前。
func SortWts(wts []Worktree, state func(Worktree) WtState) []Worktree {
	out := slices.Clone(wts)
	st := make(map[string]WtState, len(out))
	for _, w := range out {
		st[w.Path] = state(w)
	}
	slices.SortStableFunc(out, func(a, b Worktree) int {
		switch {
		case a.Main != b.Main:
			if a.Main {
				return -1
			}
			return 1
		}
		return int(st[a.Path]) - int(st[b.Path])
	})
	return out
}

// WtTop 是收起的工作树组头右边要放的那一档：组里最需要你的状态，只算等你、运行中、可合并；都没有 ok 为假。
func WtTop(states []WtState) (top WtState, ok bool) {
	top = WtMerged
	for _, s := range states {
		top = min(top, s)
	}
	return top, len(states) > 0 && top <= WtMergeable
}

// WtState 是仓库 r 里工作树 w 现在的状态。
func (a *App) WtState(r *Repo, w Worktree) WtState {
	return WtStateOf(a.WtAgent(r, w).State, w, r.WtInfo[w.Path])
}

// wtStates 是仓库 r 每个工作树的状态。
func (a *App) wtStates(r *Repo) []WtState {
	out := make([]WtState, len(r.Worktrees))
	for i, w := range r.Worktrees {
		out[i] = a.WtState(r, w)
	}
	return out
}

// WtGroupTop 是仓库 r 的工作树组收起时组头右边的那一档（WtTop）。
func (a *App) WtGroupTop(r *Repo) (WtState, bool) { return WtTop(a.wtStates(r)) }

// SortedWorktrees 是 Git 栏里仓库 r 的工作树，按状态排好（SortWts）。
func (a *App) SortedWorktrees(r *Repo) []Worktree {
	return SortWts(r.Worktrees, func(w Worktree) WtState { return a.WtState(r, w) })
}

// WtShown 是 Git 栏里仓库段的工作树组展开着：用户点开的，或者有工作树在等你、在跑、可合并（WtAuto）而用户这次没收起。
func (r *Repo) WtShown() bool { return r.WtOpen || r.wtAuto && !r.WtShut }

// SyncWtAuto 每帧画 Git 栏前对一次各仓库的"组默认展开"：有工作树在等你、在跑或可合并就展开；
// 刚变成展开时开始懒算各工作树的状态、盯着它们的目录（和点开一样）。
func (a *App) SyncWtAuto() {
	for _, r := range a.Repos {
		if len(r.Worktrees) == 0 {
			r.wtAuto = false
			continue
		}
		was := r.WtShown()
		_, r.wtAuto = a.WtGroupTop(r)
		if !was && r.WtShown() {
			a.loadWtInfo(r)
			a.syncWtWatch(r)
		}
	}
}

// WtExpand 是 Git 栏里展开着的那个工作树（同时只展开一个，不保存）：摘要、比主分支多出的改动、终端。
type WtExpand struct {
	Repo    *Repo
	Path    string
	Head    string       // 读的时候工作树的 HEAD（变了重读）
	Base    string       // 主分支的 HEAD（main...分支 的 main）
	Files   []git.Change // main...分支 改了的文件（Range，Parent 是 merge-base）
	Commits int          // merge-base..分支 的提交数
	Loading bool
	Err     error
	review  bool // 点了"审查"时还在读：读完开第一个文件
}

// WtExpanded 是 Git 栏里展开着的是仓库 r 的工作树 w。
func (a *App) WtExpanded(r *Repo, w Worktree) bool {
	return a.WtExp != nil && a.WtExp.Repo == r && a.WtExp.Path == w.Path
}

// ExpandWorktree 展开 Git 栏里的工作树 w（收起别的），在后台读它比主分支多出的改动（main...分支，只读）。
// 主工作树、bare、目录没了的、没有 HEAD 的只展开不读。
func (a *App) ExpandWorktree(r *Repo, w Worktree) {
	base := wtBase(r)
	if a.WtExpanded(r, w) && a.WtExp.Head == w.Head && a.WtExp.Base == base {
		return
	}
	x := &WtExpand{Repo: r, Path: w.Path, Head: w.Head, Base: base}
	a.WtExp = x
	if w.Main || w.Bare || w.Prunable || w.Head == "" || base == "" {
		return
	}
	x.Loading = true
	ctx, root, up := r.ctx, r.Root, a.Update
	go func() {
		_, fs, n, err := git.BranchFiles(ctx, root, base, w.Head)
		up(func() {
			if a.WtExp != x || r.gone() {
				return
			}
			x.Files, x.Commits, x.Err, x.Loading = fs, n, err, false
			if x.review && err == nil && len(fs) > 0 {
				a.OpenDiff(r, fs[0], false)
			}
		})
	}()
}

// ToggleWorktree 展开 / 收起工作树 w（回车、点已经展开的那行）。
func (a *App) ToggleWorktree(r *Repo, w Worktree) {
	if a.WtExpanded(r, w) {
		a.WtExp = nil
		return
	}
	a.ExpandWorktree(r, w)
}

// ReviewWorktree 是"审查"：展开工作树 w，在主区开它比主分支多出的第一个文件的 Diff（main...分支）；还在读就读完再开。
func (a *App) ReviewWorktree(r *Repo, w Worktree) {
	a.ExpandWorktree(r, w)
	x := a.WtExp
	if x.Loading {
		x.review = true
		return
	}
	if len(x.Files) > 0 {
		a.OpenDiff(r, x.Files[0], false)
	}
}

// MergeWtBranch 是工作树 w 的分支当 git.Branch 看（合并用）。
func MergeWtBranch(w Worktree) git.Branch {
	return git.Branch{Name: w.Branch, Hash: w.Head}
}

// MergeWorktreeWhy 是"合并到 main"不能做的原因：这一段自己是链接的工作树 → 分离 HEAD → 已经合并 → 有改动没提交 → MergeWhy。
func MergeWorktreeWhy(r *Repo, w Worktree) string {
	info := r.WtInfo[w.Path]
	switch {
	case w.Main:
		return i18n.T("主工作树")
	case r.Linked:
		return i18n.T("这一段是链接的工作树：到主仓库那一段合并")
	case w.Branch == "":
		return i18n.T("分离 HEAD，没有分支")
	case info != nil && info.Err == nil && info.Ahead == 0:
		return i18n.T("已经合并")
	case info != nil && info.Dirty > 0:
		return i18n.T("工作树里有没提交的改动")
	}
	return MergeWhy(r, MergeWtBranch(w))
}

// MergeWorktree 是"合并到 main…"：先问，点了再把工作树的分支合并进这一段的当前分支（App.Merge，git merge 分支）。
func (a *App) MergeWorktree(r *Repo, w Worktree) {
	if a.Blocked(MergeWorktreeWhy(r, w)) {
		return
	}
	into := r.Status.Branch
	a.Ask(i18n.Tf("把 %s 合并到 %s？", w.Branch, into),
		i18n.Tf("在 %s 里跑 git merge %s。停在冲突上时有横幅，可以中止。", r.Name, w.Branch),
		i18n.T("合并"), func() { a.Merge(r, MergeWtBranch(w)) })
}

// NeedYou 是"需要你"条上的一个工作树。
type NeedYou struct {
	Repo  *Repo
	Wt    Worktree
	State WtState // WtWaiting 或 WtMergeable
}

// NeedsYou 是"需要你"条上的工作树：所有仓库里先是等你的、再是可合并的，同一档按仓库、组里的顺序。
// 可合并要知道领先几个，只有算过状态的（组展开过）才认得出。
func (a *App) NeedsYou() []NeedYou {
	var wait, merge []NeedYou
	for _, r := range a.Repos {
		for _, w := range a.SortedWorktrees(r) {
			switch s := a.WtState(r, w); s {
			case WtWaiting:
				wait = append(wait, NeedYou{r, w, s})
			case WtMergeable:
				merge = append(merge, NeedYou{r, w, s})
			}
		}
	}
	return append(wait, merge...)
}

// StepNeedsYou 是"需要你"条的上一个（d = -1）/ 下一个（d = 1）：在这些工作树之间跳，展开它、选中、滚到它。
func (a *App) StepNeedsYou(d int) {
	ns := a.NeedsYou()
	if len(ns) == 0 {
		return
	}
	i := slices.IndexFunc(ns, func(n NeedYou) bool { return a.WtExp != nil && a.WtExp.Repo == n.Repo && a.WtExp.Path == n.Wt.Path })
	switch {
	case i < 0 && d < 0:
		i = len(ns) - 1
	case i < 0:
		i = 0
	default:
		i = (i + d + len(ns)) % len(ns)
	}
	a.GoToWorktree(ns[i].Repo, ns[i].Wt)
}

// GoToWorktree 在 Git 栏里跳到工作树 w：展开它的仓库段和工作树组，展开它、选中它、滚到它（当前仓库跟过去）。
func (a *App) GoToWorktree(r *Repo, w Worktree) {
	if !r.GitOpen() {
		r.GitFold = FoldOpen
	}
	if !r.WtShown() {
		a.ToggleWorktrees(r)
	}
	a.ExpandWorktree(r, w)
	rows := a.GitRows()
	i := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowWorktree && cr.Repo == r && cr.Wt.Path == w.Path })
	if i < 0 {
		return
	}
	a.GitSel, a.gitPrev = i, i
	a.chooseGit(rows, i)
	a.gitWt, a.gitWtDoc = w.Path, a.CurrentDoc()
	a.ChangeList.ScrollTo(i, ui.Center)
	a.UseRepo(r)
}

// AgentJob 是这一格前台的命令名（后台轮询记下的，不跑命令）；没认出来为 ""。
func (p *TermPane) AgentJob() string { return p.job }

// Since 是工作树上的 agent 这一状态从什么时候起（等你的从响铃起、运行中的从这一段有输出起），别的为零。
func (s AgentState) Since() time.Time {
	switch s.State {
	case AgentWaiting:
		return s.WaitSince
	case AgentRunning:
		return s.RunSince
	}
	return time.Time{}
}

// Now 是现在（界面算"等了多久"用；测试里是可拨的钟）。
func (a *App) Now() time.Time { return a.now() }
