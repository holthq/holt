package app

import (
	"slices"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是侧栏 Git 模式的变更列表：工作区里所有仓库同时列出，一个仓库一段（段头贴在顶上），
// 段里按冲突 / 已暂存 / 未暂存 / 未跟踪分组，再是贮藏，段底是这个仓库的提交框。选中一项就在主区的
// 预览 Diff 标签里看它（"diff 跟着选择走"），并把当前仓库切到它所在的仓库。

// RowKind 是 Git 栏一行的种类。
type RowKind int

const (
	RowRepo      RowKind = iota // 仓库的段头（列表的 Header：滚动时贴在顶上，不会被选中）
	RowGroup                    // 段里的分组标题：冲突 / 已暂存 / 未暂存 / 未跟踪
	RowChange                   // 一项变更
	RowNote                     // 说明：没有未提交的变更、git status 失败
	RowCommit                   // 段底的提交框
	RowStashes                  // 贮藏的分组标题
	RowStash                    // 一条贮藏
	RowWorktrees                // "工作树"块的标题（worktrees.go）
	RowWorktree                 // 一个工作树
	RowOngoing                  // 段头下面的横幅：合并 / 变基 / 拣选 / 还原停在一半（ongoing.go），继续 / 跳过 / 中止
	RowStashFile                // 展开的贮藏下面的一个文件（Stash 是那条贮藏，Change 是 InCommit 的文件）
	RowWtSummary                // 展开的工作树下面的摘要行：提交数、文件数、行数，审查、合并到 main
	RowWtFile                   // 展开的工作树比主分支多出的一个文件（main...分支，Change 是 Range 的）
	RowWtTerm                   // 展开的工作树上的终端（有 agent 时）：前台进程、多久以前，点它跳过去
)

// 分组在 GroupToggled 和 state.json 里的键。
const (
	GroupConflict  = "conflict"
	GroupStaged    = "staged"
	GroupUnstaged  = "unstaged"
	GroupUntracked = "untracked"
	GroupStash     = "stash"
)

// GroupKey 是一组变更的键。
func GroupKey(k git.Kind) string {
	switch k {
	case git.Conflict:
		return GroupConflict
	case git.Staged:
		return GroupStaged
	case git.Unstaged:
		return GroupUnstaged
	}
	return GroupUntracked
}

// GroupOpen 是仓库段里的这一组展开着：默认贮藏收着（很少要看，一条条列出来占地方）、别的展开，点过分组标题就反过来。
func (r *Repo) GroupOpen(key string) bool { return groupOpenByDefault(key) != r.GroupToggled[key] }

func groupOpenByDefault(key string) bool { return key != GroupStash }

// ToggleGitGroup 收起 / 展开仓库段里的一组（点分组标题），记进 state.json。
func (a *App) ToggleGitGroup(r *Repo, key string) {
	if r.GroupToggled == nil {
		r.GroupToggled = map[string]bool{}
	}
	if r.GroupToggled[key] {
		delete(r.GroupToggled, key)
	} else {
		r.GroupToggled[key] = true
	}
	a.syncChangeSel() // 选中的那项收进去了就不选
	a.SaveState()
}

// ChangeRow 是 Git 栏的一行。
type ChangeRow struct {
	Kind   RowKind
	Repo   *Repo
	Title  string     // 分组标题、说明
	Count  int        // 分组的条数
	Group  string     // RowGroup / RowStashes：分组的键（GroupKey）
	Change git.Change // RowChange：那项变更；RowGroup：只有 Kind 有意义；RowStashFile：贮藏里的文件
	Stash  git.Stash  // RowStash：那条贮藏；RowStashFile 和展开的贮藏下面的说明：它所在的贮藏
	Wt     Worktree   // RowWorktree：那个工作树
	InDir  string     // 展开的未跟踪目录下面的行（RowChange 是里面的一个文件，RowNote 是“读取中…”“还有更多”）：那个目录的 Path
}

// ChangeKey 是 Git 栏一行的身份（列表的 Key、多选的 GitChosen）：仓库、种类、分组和路径。
type ChangeKey struct {
	root string
	kind RowKind
	ck   git.Kind
	path string
}

// Key 是这一行在列表里的身份。
func (cr ChangeRow) Key() ChangeKey {
	switch cr.Kind {
	case RowStash:
		return ChangeKey{cr.Repo.Root, cr.Kind, 0, cr.Stash.Hash}
	case RowStashFile:
		return ChangeKey{cr.Repo.Root, cr.Kind, 0, cr.Stash.Hash + ":" + cr.Change.Path}
	case RowWorktree, RowWtSummary, RowWtTerm:
		return ChangeKey{cr.Repo.Root, cr.Kind, 0, cr.Wt.Path}
	case RowWtFile:
		return ChangeKey{cr.Repo.Root, cr.Kind, 0, cr.Wt.Path + ":" + cr.Change.Path}
	case RowNote: // 一个仓库段里可以有好几条说明（没有改动、展开的贮藏在读、展开的未跟踪目录还有更多）
		return ChangeKey{cr.Repo.Root, cr.Kind, 0, cr.InDir + cr.Title}
	}
	return ChangeKey{cr.Repo.Root, cr.Kind, cr.Change.Kind, cr.Change.Path}
}

// 仓库段的折叠：默认按有没有改动（有改动展开、干净的收成一行；有工作树、合并 / 变基停在一半的也展开，让"工作树"块和横幅看得见），
// 点过箭头就按用户的。
const (
	FoldAuto = iota
	FoldOpen
	FoldClosed
)

// GitOpen 是这个仓库在 Git 栏里展开着。
func (r *Repo) GitOpen() bool {
	if r.Plain {
		return false // 普通文件夹只有段头
	}
	switch r.GitFold {
	case FoldOpen:
		return true
	case FoldClosed:
		return false
	}
	return r.Err != nil || r.Status.Count > 0 || r.HasWorktrees() || r.Extra.Ongoing != git.NoOp // 合并都用了我的、干净了也要看得见横幅
}

// ToggleGitRepo 展开 / 收起 Git 栏里的一个仓库段，记进 state.json。
func (a *App) ToggleGitRepo(r *Repo) {
	if r.Plain {
		return
	}
	if r.GitOpen() {
		r.GitFold = FoldClosed
	} else {
		r.GitFold = FoldOpen
	}
	a.SaveState()
}

// SetAllGitOpen 展开 / 收起 Git 栏里所有仓库段（段头那排的按钮），记进 state.json。普通文件夹不算。
func (a *App) SetAllGitOpen(open bool) {
	for _, r := range a.Repos {
		if r.Plain {
			continue
		}
		r.GitFold = FoldClosed
		if open {
			r.GitFold = FoldOpen
		}
	}
	a.SaveState()
}

// AllGitOpen 是 Git 栏里所有仓库段都展开着（普通文件夹不算）。
func (a *App) AllGitOpen() bool {
	return !slices.ContainsFunc(a.Repos, func(r *Repo) bool { return !r.Plain && !r.GitOpen() })
}

// CommitShown 是提交框展开着：有已暂存的改动、写了信息、合并的冲突都解决了，或用户点开了"提交…"。
func (r *Repo) CommitShown() bool {
	return r.CommitOpen || r.CommitMsg != "" || r.Status.Staged > 0 || r.Amend || r.MergeReady()
}

// OpenCommit 点开仓库段底的"提交…"，下一帧焦点给提交信息框。
func (a *App) OpenCommit(r *Repo) {
	r.CommitOpen, r.FocusCommit = true, true
	if !r.GitOpen() {
		r.GitFold = FoldOpen
	}
	a.UseRepo(r)
}

// GitRows 是 Git 栏现在的行：每个仓库一段，按列表里仓库的顺序。
func (a *App) GitRows() []ChangeRow {
	n := 0
	for _, r := range a.Repos {
		n += 1 + len(r.Status.Changes) + 8 + len(r.Extra.Stashes) + len(r.StashFiles)
		if r.WtShown() {
			n += len(r.Worktrees)
		}
	}
	rows := make([]ChangeRow, 0, n)
	for _, r := range a.Repos {
		rows = append(rows, ChangeRow{Kind: RowRepo, Repo: r})
		if !r.GitOpen() {
			continue
		}
		if r.Err == nil && r.Loaded && r.Extra.Ongoing != git.NoOp {
			rows = append(rows, ChangeRow{Kind: RowOngoing, Repo: r, Title: OngoingTitle(r)})
		}
		if r.Err == nil && r.Loaded && r.HasWorktrees() {
			rows = a.appendWorktrees(rows, r)
		}
		switch {
		case r.Err != nil:
			rows = append(rows, ChangeRow{Kind: RowNote, Repo: r, Title: r.Err.Error()})
		case !r.Loaded:
			// 段头在转圈
		case r.Status.Count == 0: // 干净由段头的勾表示，不再写一行"没有未提交的变更"
			rows = appendStashes(rows, r)
			if r.CommitShown() { // 干净的仓库也能修改上一次提交（只改信息）
				rows = append(rows, ChangeRow{Kind: RowCommit, Repo: r})
			}
		default:
			rows = appendChanges(rows, r)
			rows = appendStashes(rows, r)
			rows = append(rows, ChangeRow{Kind: RowCommit, Repo: r})
		}
	}
	return rows
}

// appendWorktrees 加上仓库的"工作树"块：一行组头，展开时下面每个工作树一行（按状态排，SortedWorktrees）；
// 展开着的那个工作树下面是摘要、比主分支多出的文件和（有 agent 时）终端。
func (a *App) appendWorktrees(rows []ChangeRow, r *Repo) []ChangeRow {
	rows = append(rows, ChangeRow{Kind: RowWorktrees, Repo: r, Title: i18n.T("工作树"), Count: len(r.Worktrees)})
	if !r.WtShown() {
		return rows
	}
	for _, w := range a.SortedWorktrees(r) {
		rows = append(rows, ChangeRow{Kind: RowWorktree, Repo: r, Wt: w})
		if !a.WtExpanded(r, w) || w.Main {
			continue
		}
		x := a.WtExp
		rows = append(rows, ChangeRow{Kind: RowWtSummary, Repo: r, Wt: w})
		for _, f := range x.Files {
			rows = append(rows, ChangeRow{Kind: RowWtFile, Repo: r, Wt: w, Change: f})
		}
		if ag := a.WtAgent(r, w); ag.Pane != nil {
			rows = append(rows, ChangeRow{Kind: RowWtTerm, Repo: r, Wt: w})
		}
	}
	return rows
}

// appendStashes 加上仓库的贮藏（有的话）：一个分组标题，下面每条一行；展开的那条下面是它的文件。
func appendStashes(rows []ChangeRow, r *Repo) []ChangeRow {
	if len(r.Extra.Stashes) == 0 {
		return rows
	}
	rows = append(rows, ChangeRow{Kind: RowStashes, Repo: r, Title: i18n.T("贮藏"), Count: len(r.Extra.Stashes), Group: GroupStash})
	if !r.GroupOpen(GroupStash) {
		return rows
	}
	for _, s := range r.Extra.Stashes {
		rows = append(rows, ChangeRow{Kind: RowStash, Repo: r, Stash: s})
		if s.Hash != r.StashOpen {
			continue
		}
		switch {
		case r.StashFilesFor != s.Hash:
			rows = append(rows, ChangeRow{Kind: RowNote, Repo: r, Stash: s, Title: i18n.T("读取中…")})
		case len(r.StashFiles) == 0:
			rows = append(rows, ChangeRow{Kind: RowNote, Repo: r, Stash: s, Title: i18n.T("这条贮藏是空的")})
		}
		for _, ch := range r.StashFiles {
			rows = append(rows, ChangeRow{Kind: RowStashFile, Repo: r, Stash: s, Change: ch})
		}
	}
	return rows
}

// appendChanges 加上一个仓库按种类分好组的变更（Status.Changes 已经按组排好）；收起的组只有标题。
func appendChanges(rows []ChangeRow, r *Repo) []ChangeRow {
	last, open := git.Kind(-1), true
	for _, ch := range r.Status.Changes {
		if ch.Kind != last {
			last = ch.Kind
			key := GroupKey(ch.Kind)
			open = r.GroupOpen(key)
			rows = append(rows, ChangeRow{Kind: RowGroup, Repo: r, Title: ch.Kind.String(), Count: groupCount(r.Status, ch.Kind),
				Change: git.Change{Kind: ch.Kind}, Group: key})
		}
		if open {
			rows = append(rows, ChangeRow{Kind: RowChange, Repo: r, Change: ch})
			if ch.Dir && r.DirOpen[ch.Path] {
				rows = appendDirFiles(rows, r, ch.Path)
			}
		}
	}
	return rows
}

// groupCount 是仓库里某一组变更的条数。
func groupCount(st git.Status, k git.Kind) int {
	switch k {
	case git.Conflict:
		return st.Conflicts
	case git.Staged:
		return st.Staged
	case git.Unstaged:
		return st.Unstaged
	}
	return st.Untracked
}

// OnChangeSelected 处理 Git 栏里的点击 / 方向键：选中落在一项变更、一个工作树、一条贮藏或贮藏里的文件上。
// 键盘走到段头、分组标题、说明或提交框时按方向跳到下一个能停的行（那边没有了就留在原处）；点在它们上面不算选中。
// 选中一项变更（或贮藏里的文件）就把当前仓库切到它的仓库，在预览 Diff 标签里显示；pin（双击、回车）时固定这个标签。
// 选中一个工作树是展开它、右栏看它的分支历史，开着的 Diff 不动；回车展开 / 收起它（双击在工作区中打开由那一行管）。
// 选中一条贮藏只是停在上面（点它展开 / 收起由那一行自己管），回车展开 / 收起。
// 选了好几行（⌘点、⇧点、⌘A，见 gitchosen.go）时点击不预览，回车把选中的改动都开成固定的 Diff。
func (a *App) OnChangeSelected(pin bool) {
	rows := a.GitRows()
	switch n := a.GitChosen.Len(); {
	case n > 1, n == 1 && !a.GitChosen.Has(rowKey(rows, a.GitSel)):
		// 选了好几行，或 ⌘点把光标那行取消了（光标还在它上面）
		a.gitMulti, a.gitMultiDoc = true, a.CurrentDoc()
		if pin {
			a.OpenDiffs(a.ChosenChanges())
		}
		return
	case n == 0 && !pin:
		return // ⌘点把唯一选中的那行取消了
	}
	a.gitMulti = false
	a.gitAfter = gitAfter{} // 用户自己挪了选中
	sel := a.GitSel
	if sel >= 0 && sel < len(rows) && !canStop(rows[sel].Kind) {
		if a.GitHover == sel {
			a.syncChangeSel()
			return
		}
		d := 1
		if sel < a.gitPrev {
			d = -1
		}
		sel = nextStop(rows, sel, d)
		if sel < 0 && a.gitPrev >= 0 && a.gitPrev < len(rows) && canStop(rows[a.gitPrev].Kind) {
			sel = a.gitPrev
		}
	}
	a.GitSel, a.gitPrev = sel, sel
	a.chooseGit(rows, sel)
	a.gitStash, a.gitDir = "", ChangeKey{}
	if sel < 0 || sel >= len(rows) {
		a.gitWt = ""
		return
	}
	cr := rows[sel]
	if cr.Kind == RowStash {
		a.gitWt = ""
		a.gitStash, a.gitStashDoc = cr.Stash.Hash, a.CurrentDoc()
		a.clearRefHistories(nil)
		a.UseRepo(cr.Repo)
		if pin && a.GitHover != sel { // 回车；鼠标的单击、双击是那一行自己的
			a.ToggleStash(cr.Repo, cr.Stash)
		}
		return
	}
	if cr.Kind == RowWorktree { // 选中就展开（同时只展开一个）；回车展开 / 收起，双击在工作区中打开（那一行自己管）
		a.gitWt, a.gitWtDoc = cr.Wt.Path, a.CurrentDoc()
		if pin {
			if a.GitHover != sel {
				a.ToggleWorktree(cr.Repo, cr.Wt)
			}
			return
		}
		a.ExpandWorktree(cr.Repo, cr.Wt)
		// 上面展开着的那个收起来了，行号会变：选中跟着这个工作树走
		rows = a.GitRows()
		if i := slices.IndexFunc(rows, func(r ChangeRow) bool { return r.Kind == RowWorktree && r.Key() == cr.Key() }); i >= 0 && i != sel {
			a.GitSel, a.gitPrev = i, i
			a.chooseGit(rows, i)
		}
		a.clearRefHistories(cr.Repo)
		a.viewWorktree(cr.Repo, cr.Wt)
		return
	}
	if cr.Kind == RowWtFile { // main...分支 的一个文件：开它的 Diff，选中跟着那个 Diff 标签（docChangeRow）
		a.gitWt = ""
		a.OpenDiff(cr.Repo, cr.Change, pin)
		return
	}
	a.gitWt = ""
	a.clearRefHistories(nil) // "点别处"：右栏不再看工作树的历史
	a.UseRepo(cr.Repo)
	if cr.Change.Dir { // 未跟踪目录：选中停在上面，不开 Diff（点它展开 / 收起由那一行自己管），回车展开 / 收起
		a.gitDir, a.gitDirDoc = cr.Key(), a.CurrentDoc()
		if pin && a.GitHover != sel {
			a.ToggleUntrackedDir(cr.Repo, cr.Change.Path)
		}
		return
	}
	if cr.Change.Kind == git.Conflict { // 冲突在编辑器里解决；HEAD ↔ 工作区的 Diff 在右键里
		a.OpenConflict(cr.Repo, cr.Change, pin)
		return
	}
	a.OpenDiff(cr.Repo, cr.Change, pin)
}

// canStop 是 Git 栏的选中能停在这种行上：一项变更、一个工作树、一条贮藏或贮藏里的文件。
func canStop(k RowKind) bool {
	return k == RowChange || k == RowWorktree || k == RowStash || k == RowStashFile || k == RowWtFile
}

// nextStop 是从 i 往 d 方向（1 下、-1 上）第一个能停的行，没有为 -1。
func nextStop(rows []ChangeRow, i, d int) int {
	for i += d; i >= 0 && i < len(rows); i += d {
		if canStop(rows[i].Kind) {
			return i
		}
	}
	return -1
}

// syncChangeSel 让 Git 栏的选中行跟着当前标签：当前标签是某项未提交改动的 Diff、展开的贮藏里一个文件的 Diff、
// 或一个冲突文件就选中那一行（不管它是从 Git 栏、文件树、标签条还是 ‹ › 切过来的），否则哪行都不选。
// 状态重读后行号会变，也再对一次。选中停在一个工作树或一条贮藏上时，只要当前标签还是那时的、那一行还在，
// 就一直停在它上面。选着好几行时，只要当前标签没换、选中的还有两行以上看得见，就都留着（重读状态不打断多选）。
func (a *App) syncChangeSel() {
	rows := a.GitRows()
	if a.gitMulti && a.CurrentDoc() == a.gitMultiDoc && a.shownChosen(rows) >= 2 {
		return
	}
	a.gitMulti = false
	sel := a.docChangeRow(rows)
	if sel < 0 && a.gitAfter.set { // 暂存 / 丢弃了选中的那行：停在原来位置的下一行，不丢选中
		sel = a.gitAfter.pick(rows)
		a.gitAfter = gitAfter{}
	}
	a.GitSel, a.gitPrev = sel, sel
	a.chooseGit(rows, sel)
}

// docChangeRow 是 syncChangeSel 要选中的行（见它的说明），没有为 -1。
func (a *App) docChangeRow(rows []ChangeRow) int {
	d := a.CurrentDoc()
	if a.gitWt != "" {
		if d == a.gitWtDoc {
			if i := slices.IndexFunc(rows, func(cr ChangeRow) bool {
				return cr.Kind == RowWorktree && cr.Wt.Path == a.gitWt
			}); i >= 0 {
				return i
			}
		}
		a.gitWt = ""
	}
	if a.gitStash != "" {
		if d == a.gitStashDoc {
			if i := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Kind == RowStash && cr.Stash.Hash == a.gitStash }); i >= 0 {
				return i
			}
		}
		a.gitStash = ""
	}
	if a.gitDir != (ChangeKey{}) {
		if d == a.gitDirDoc {
			if i := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Key() == a.gitDir }); i >= 0 {
				return i
			}
		}
		a.gitDir = ChangeKey{}
	}
	switch {
	case d == nil:
	case d.Kind == docs.Diff && d.Change.Kind != git.InCommit:
		return slices.IndexFunc(rows, func(cr ChangeRow) bool {
			return cr.Kind == RowChange && cr.Repo.Root == d.Root && cr.Change.Kind == d.Change.Kind && cr.Change.Path == d.Change.Path
		})
	case d.Kind == docs.Diff:
		return slices.IndexFunc(rows, func(cr ChangeRow) bool {
			return (cr.Kind == RowStashFile || cr.Kind == RowWtFile) && cr.Repo.Root == d.Root &&
				cr.Change.Commit == d.Change.Commit && cr.Change.Parent == d.Change.Parent && cr.Change.Path == d.Change.Path
		})
	case d.Kind == docs.File && d.Rel != "":
		return slices.IndexFunc(rows, func(cr ChangeRow) bool {
			return cr.Kind == RowChange && cr.Repo.Root == d.Root && cr.Change.Kind == git.Conflict && cr.Change.Path == d.Rel
		})
	}
	return -1
}

// GitRowOf 是 Git 栏里仓库 r 的段头所在的行，没有为 -1。
func (a *App) GitRowOf(r *Repo) int {
	return slices.IndexFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Kind == RowRepo && cr.Repo == r })
}

// ClearOutput 清空输出栏。
func (a *App) ClearOutput() { a.Output = nil }
