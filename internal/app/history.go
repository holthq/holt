package app

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/graph"
	"github.com/lemoeade/grove/internal/history"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是右栏历史的状态：分页读 git log，展开一个提交看它改的文件，
// 点文件在主区开 Diff 标签。"当前仓库"视图第一行固定是"未提交的改动"；"所有仓库"视图把
// 各仓库读到的提交按时间合并成一列，上面一段是有改动的仓库各一行。

// History 是一个仓库的提交历史。
type History struct {
	Commits []git.Commit
	Loaded  bool
	Loading bool
	Done    bool // 已经读到最早的提交
	Err     error
	gen     int // 每次重读递增，旧的结果作废

	Expanded     string       // 展开着的提交
	Files        []git.Change // 展开的提交改的文件
	FilesFor     string
	FilesLoading bool
	Sel, PrevSel int // 列表里选中的行（含第一行"未提交的改动"）

	Path    string // 文件历史：只看动过它的提交（仓库内路径），空为不筛
	PathDir bool   // Path 是目录（不跟重命名）

	// 别处的历史：单击 Git 栏"工作树"块里的一条，是从它的分支（分离 HEAD 时是哈希）往前读；
	// 分支菜单里选一个标签，是从那个标签的提交往前读。都代替 HEAD
	RefKind RefKind
	Ref     string // git log 的起点，空为不是
	RefName string // 标题里的名字：分支，或"分离 HEAD · 短哈希"，或标签名
	RefPath string // 那个工作树的目录（看标签时为空）

	query, path, ref string   // Commits 是按哪个搜索词、哪个文件、从哪读的（筛选变了才重读）
	all              bool     // Commits 是不是按所有分支读的
	BadDates         []string // 读的时候查询里 git 也认不出的日期（没算进条件）

	// Graph 和 Commits 一一对应，是提交图的每一行；读的时候有搜索词或是文件历史（只是一部分提交，连线没意义）就是空的。
	Graph  []graph.Row
	GraphW int         // Graph 里最宽的一行用到几条泳道
	lanes  graph.Lanes // 提交图算到哪了：读下一页接着算

	CompareFrom *git.Commit // 右键"标记为比较起点"标的提交
	Compare     *Compare    // 正在比较的两个提交（在"未提交的改动"下面一段），× 才清

	want     string // ShowInHistory 要选中的提交（blame 上点的）：读完一页还没有就接着读，最多再读 wantLeft 页
	wantLeft int

	// Local 是文件历史（Path 是文件）那个文件的本地历史（localhist.go），旧的在前；HistRows 把它和提交按时间混排
	Local    []history.Entry
	localFor string // Local 是哪个文件的（绝对路径），空为没有
}

// HasGraph 是读到的历史算了提交图（没有搜索词、不是文件历史）。
func (h *History) HasGraph() bool {
	return len(h.Graph) > 0 && len(h.Graph) == len(h.Commits)
}

// HeadIndex 是 HEAD 那个提交在 Commits 里的下标（提交图在它的泳道上接"未提交的改动"），没读到是 -1。
func (h *History) HeadIndex(head string) int {
	if head == "" {
		return -1
	}
	return slices.IndexFunc(h.Commits, func(c git.Commit) bool { return c.Hash == head })
}

// HistRow 是历史列表的一行："未提交的改动"（CI = -1）、比较段（CI = CompareRow）、提交、展开的提交下的一个文件，
// 或文件历史里的一条本地历史（CI = LocalRow）。
type HistRow struct {
	CI   int // Commits 下标；-1 为"未提交的改动"，CompareRow 为比较段，LocalRow 为本地历史
	File int // -1 为提交行（比较段的标题），否则 Files（比较段是 Compare.Files，本地历史是 Local）下标
}

// CompareRow 是 HistRow.CI 里比较段的行；LocalRow 是本地历史的行。
const (
	CompareRow = -2
	LocalRow   = -3
)

// HistRows 是历史列表现在的行。
func HistRows(r *Repo) []HistRow {
	h := &r.Hist
	rows := make([]HistRow, 0, 2+len(h.Commits)+len(h.Files))
	rows = append(rows, HistRow{-1, -1})
	if cp := h.Compare; cp != nil {
		rows = append(rows, HistRow{CompareRow, -1})
		if cp.Open {
			for j := range cp.Files {
				rows = append(rows, HistRow{CompareRow, j})
			}
		}
	}
	// 本地历史倒着（新的在前）插在比它早的提交前面；提交还没读完时比最后一个读到的提交还早的先不列（下一页可能更新）
	lj := len(h.Local) - 1
	local := func(before time.Time) {
		for ; lj >= 0 && (before.IsZero() || h.Local[lj].Time().After(before)); lj-- {
			rows = append(rows, HistRow{LocalRow, lj})
		}
	}
	for i, c := range h.Commits {
		local(c.Date)
		rows = append(rows, HistRow{i, -1})
		if c.Hash == h.Expanded && h.FilesFor == c.Hash {
			for j := range h.Files {
				rows = append(rows, HistRow{i, j})
			}
		}
	}
	if h.Done || len(h.Commits) == 0 {
		local(time.Time{})
	}
	return rows
}

// LocalPass 是第 i 行（本地历史）在圆点模式下要不要一条竖线穿过去：上下挨着的两个提交连着（DotLink）。
func LocalPass(r *Repo, rows []HistRow, i int) bool {
	h := &r.Hist
	above, below := -1, -1
	for j := i - 1; j >= 0 && above < 0; j-- {
		if rows[j].CI >= 0 && rows[j].File < 0 {
			above = rows[j].CI
		}
	}
	for j := i + 1; j < len(rows) && below < 0; j++ {
		if rows[j].CI >= 0 && rows[j].File < 0 {
			below = rows[j].CI
		}
	}
	return above >= 0 && below >= 0 && DotLink(h.Commits[above], h.Commits[below])
}

// EnsureHistory 读第一页历史，已经读过或正在读就什么都不做。
func (a *App) EnsureHistory(r *Repo) {
	if !r.Hist.Loaded && !r.Hist.Loading {
		a.LoadHistory(r, true)
	}
}

// LoadHistory 在后台读第一页（reset）或下一页历史。普通文件夹没有历史，什么都不做。
func (a *App) LoadHistory(r *Repo, reset bool) {
	h := &r.Hist
	if r.Plain {
		return
	}
	if h.Loading && !reset {
		return
	}
	h.gen++
	gen := h.gen
	h.Loading = true
	skip := 0
	if !reset {
		skip = len(h.Commits)
	}
	ci, file := histKey(r)
	var selHash, selPath string
	var selLocal int64 // 选着的本地历史那条的 T
	if ci == LocalRow && file >= 0 && file < len(h.Local) {
		selLocal = h.Local[file].T
	}
	if ci >= 0 {
		selHash = h.Commits[ci].Hash
		if file >= 0 && file < len(h.Files) {
			selPath = h.Files[file].Path
		}
	}
	q, hq := a.logQuery(r)
	raw := strings.TrimSpace(a.HistQuery)
	up := a.Update
	a.readyAsk(r, readyHist)
	t0 := time.Now()
	go func() {
		var head string // 开着"所有分支"时提交图把泳道 0 留给它（看工作树的历史时是那个工作树的 HEAD）
		if reset && q.All {
			head = git.RevHash(r.ctx, r.Root, cmp.Or(q.Rev, "HEAD"))
		}
		bad := hq.resolveDates(r.ctx, r.Root, &q)
		cs, err := git.Log(r.ctx, r.Root, skip, git.LogPage, q)
		took := time.Since(t0)
		up(func() {
			if h.gen != gen || r.gone() {
				return
			}
			a.readyStep(r, readyHist, took)
			h.Loading, h.Loaded, h.Err = false, true, err
			h.query, h.path, h.all, h.ref, h.BadDates = raw, q.Path, q.All, q.Rev, bad
			if err != nil {
				h.want = ""
				return
			}
			if reset {
				h.Commits = cs
				h.Graph, h.GraphW, h.lanes = nil, 0, graph.Lanes{}
				h.lanes.Reserve(head)
				a.readLocal(r)
			} else {
				h.Commits = append(h.Commits, cs...)
			}
			if !q.Narrowed() && q.Path == "" && len(h.Graph) == len(h.Commits)-len(cs) {
				// 上一页的 Lanes 接着算；汇入时会改上一页最后一行的 Edges（Graph 里存的就是那一份）
				for _, c := range cs {
					row := h.lanes.Next(c.Hash, c.Parents)
					h.Graph = append(h.Graph, row)
					h.GraphW = max(h.GraphW, row.Width())
				}
			}
			h.Done = len(cs) < git.LogPage
			if h.Expanded != "" {
				i := slices.IndexFunc(h.Commits, func(c git.Commit) bool { return c.Hash == h.Expanded })
				switch {
				case i < 0:
					h.Expanded, h.Files, h.FilesFor = "", nil, ""
				case reset:
					a.loadCommitFiles(r, h.Commits[i])
				}
			}
			// 选中的提交 / 文件还在就还选着它
			if reset && selHash != "" {
				ci := slices.IndexFunc(h.Commits, func(c git.Commit) bool { return c.Hash == selHash })
				file := -1
				if selPath != "" && h.FilesFor == selHash {
					file = slices.IndexFunc(h.Files, func(c git.Change) bool { return c.Path == selPath })
				}
				setHistSel(r, ci, file)
			}
			if reset && selLocal != 0 {
				setHistSel(r, LocalRow, slices.IndexFunc(h.Local, func(e history.Entry) bool { return e.T == selLocal }))
			}
			a.findWanted(r)
		})
	}()
}

// loadCommitFiles 在后台读一次提交改的文件。
func (a *App) loadCommitFiles(r *Repo, c git.Commit) {
	h := &r.Hist
	h.FilesLoading = true
	ci, file := histKey(r)
	up := a.Update
	go func() {
		fs, err := git.CommitFiles(r.ctx, r.Root, c)
		up(func() {
			if h.Expanded != c.Hash || r.gone() {
				return
			}
			h.FilesLoading = false
			if err != nil {
				a.Fail(i18n.T("读提交的文件失败"), err)
				return
			}
			h.Files, h.FilesFor = fs, c.Hash
			if ci >= 0 {
				setHistSel(r, ci, file)
			}
		})
	}()
}

// histKey 是选中的行对应的 (提交下标, 文件下标)；没选或选的是"未提交的改动"为 (-1, -1)，比较段的 CI 是 CompareRow。
func histKey(r *Repo) (ci, file int) {
	rows := HistRows(r)
	sel := r.Hist.Sel
	if sel < 0 || sel >= len(rows) {
		return -1, -1
	}
	return rows[sel].CI, rows[sel].File
}

// setHistSel 让选中落在 (ci, file) 上；行号变了（展开、折叠、重读）之后用。
// 文件行还没有（没展开或没读完）就落在它的提交行上（比较段的就落在它的标题上）。
func setHistSel(r *Repo, ci, file int) {
	h := &r.Hist
	h.Sel = -1
	if ci == -1 {
		return
	}
	rows := HistRows(r)
	for i, row := range rows {
		if row.CI == ci && row.File == file {
			h.Sel = i
			return
		}
	}
	for i, row := range rows {
		if row.CI == ci && row.File < 0 {
			h.Sel = i
			return
		}
	}
}

// OnHistSelected 处理历史列表里的点击 / 方向键："未提交的改动"把侧栏切到 Git 栏、滚到这个仓库那一段；
// 点到提交就展开它（收起上一个）；点到文件就在主区的预览 Diff 标签里显示。比较段的标题只是选中
// （点它、回车才展开 / 收起，界面上接；方向键走过它不动）。
func (a *App) OnHistSelected(r *Repo) {
	h := &r.Hist
	rows := HistRows(r)
	if h.Sel < 0 || h.Sel >= len(rows) {
		return
	}
	row := rows[h.Sel]
	switch {
	case row.CI == CompareRow && row.File < 0:
	case row.CI == CompareRow:
		a.OpenDiff(r, h.Compare.Files[row.File], false)
	case row.CI == LocalRow:
		a.OpenLocalCompare(h.localFor, h.Local[row.File], false)
	case row.CI < 0:
		a.ShowSidebar(workspace.ModeGit)
		a.JumpRepo(slices.Index(a.Repos, r))
	case row.File < 0:
		c := h.Commits[row.CI]
		if h.Expanded != c.Hash {
			h.Expanded, h.Files, h.FilesFor = c.Hash, nil, ""
			setHistSel(r, row.CI, -1)
			a.loadCommitFiles(r, c)
		}
	default:
		a.OpenDiff(r, h.Files[row.File], false)
	}
}

// SetHistAll 切换右栏的"当前仓库 | 所有仓库"，记进 state.json。
func (a *App) SetHistAll(all bool) {
	if a.St.HistAll == all {
		return
	}
	a.St.HistAll = all
	a.ClearLocalPath()
	a.SaveState()
	if all { // "所有仓库"不按文件筛
		for _, r := range a.Repos {
			a.ClearFileHistory(r)
		}
	}
}

// 右栏"所有仓库"视图的行的种类。
const (
	AllHeader = iota // 段标题："未提交的改动" / "提交"（列表的 Header）
	AllDirty         // 一个有未提交改动的仓库
	AllCommit        // 一个提交
	AllFile          // 展开的提交改的一个文件
)

// AllHistRow 是右栏"所有仓库"视图的一行。
type AllHistRow struct {
	Kind  int
	Repo  *Repo
	CI    int    // AllCommit / AllFile：Repo.Hist.Commits 下标
	File  int    // AllFile：Repo.Hist.Files 下标
	Title string // AllHeader
}

// allHistKey 是"所有仓库"视图一行的身份（列表的 Key）。
type allHistKey struct {
	kind       int
	root, hash string
	path       string
}

// Key 是这一行在列表里的身份。
func (row AllHistRow) Key() any {
	k := allHistKey{kind: row.Kind, path: row.Title}
	if row.Repo != nil {
		k.root = row.Repo.Root
	}
	if row.Kind == AllCommit || row.Kind == AllFile {
		k.hash = row.Repo.Hist.Commits[row.CI].Hash
	}
	if row.Kind == AllFile {
		k.path = row.Repo.Hist.Files[row.File].Path
	}
	return k
}

// Commit 是这一行的提交（AllCommit / AllFile）。
func (row AllHistRow) Commit() git.Commit { return row.Repo.Hist.Commits[row.CI] }

// AllHistRows 是"所有仓库"视图现在的行：先一段"未提交的改动"（有改动的仓库各一行，都干净就没有这段），
// 再是各仓库读到的提交按时间合并（每个仓库自己的顺序不变）。有仓库读到的那页用完了、后面还有没读的，
// 就停在那里：再往下的提交可能比它没读的那些早，等"再读"读了下一页再接着排，免得顺序错。
func (a *App) AllHistRows() []AllHistRow {
	var rows []AllHistRow
	for _, r := range a.Repos {
		if r.Loaded && r.Err == nil && r.Status.Count > 0 {
			if len(rows) == 0 {
				rows = append(rows, AllHistRow{Kind: AllHeader, Title: i18n.T("未提交的改动")})
			}
			rows = append(rows, AllHistRow{Kind: AllDirty, Repo: r})
		}
	}
	if len(rows) > 0 {
		rows = append(rows, AllHistRow{Kind: AllHeader, Title: i18n.T("提交")})
	}
	next := make([]int, len(a.Repos))
	for {
		best := -1
		for i, r := range a.Repos {
			h := &r.Hist
			if r.Plain || r.Err != nil || h.Err != nil {
				continue // 普通文件夹没有历史，读不了的仓库不挡别的
			}
			if next[i] >= len(h.Commits) {
				if !h.Loaded || !h.Done {
					return rows // 它后面的还没读到
				}
				continue
			}
			if best < 0 || h.Commits[next[i]].Date.After(a.Repos[best].Hist.Commits[next[best]].Date) {
				best = i
			}
		}
		if best < 0 {
			return rows
		}
		r, ci := a.Repos[best], next[best]
		next[best]++
		rows = append(rows, AllHistRow{Kind: AllCommit, Repo: r, CI: ci, File: -1})
		if h := &r.Hist; h.Commits[ci].Hash == h.Expanded && h.FilesFor == h.Expanded {
			for j := range h.Files {
				rows = append(rows, AllHistRow{Kind: AllFile, Repo: r, CI: ci, File: j})
			}
		}
	}
}

// AllHistShown 是"所有仓库"视图里排出来的提交数。
func AllHistShown(rows []AllHistRow) int {
	n := 0
	for _, row := range rows {
		if row.Kind == AllCommit {
			n++
		}
	}
	return n
}

// EnsureAllHistory 读每个仓库的第一页历史（"所有仓库"视图要用；读过、在读或状态还没读到的不动）。
func (a *App) EnsureAllHistory() {
	for _, r := range a.Repos {
		if r.Loaded && r.Err == nil && !r.Plain {
			a.EnsureHistory(r)
		}
	}
}

// AllHistLoading 是有仓库的历史或提交文件正在读。
func (a *App) AllHistLoading() bool {
	return slices.ContainsFunc(a.Repos, func(r *Repo) bool { return r.Hist.Loading || r.Hist.FilesLoading })
}

// AllHistDone 是每个仓库的历史都读到头了。
func (a *App) AllHistDone() bool {
	return !slices.ContainsFunc(a.Repos, func(r *Repo) bool { return !r.Plain && r.Err == nil && r.Hist.Err == nil && !r.Hist.Done })
}

// LoadMoreAllHistory 是"所有仓库"视图的"再读 200 条"：每个还没读到头的仓库各读一页。
func (a *App) LoadMoreAllHistory() {
	for _, r := range a.Repos {
		if h := &r.Hist; !r.Plain && r.Err == nil && h.Loaded && h.Err == nil && !h.Done {
			a.LoadHistory(r, false)
		}
	}
}

// OnAllHistSelected 处理"所有仓库"视图里的点击 / 方向键：有改动的仓库把侧栏切到 Git 栏、滚到它那一段；
// 提交展开它（别的仓库展开着的收起来）；文件在预览 Diff 标签里显示。点到提交或文件时当前仓库切到它的仓库。
func (a *App) OnAllHistSelected() {
	rows := a.AllHistRows()
	if a.HistAllSel < 0 || a.HistAllSel >= len(rows) {
		return
	}
	row := rows[a.HistAllSel]
	r := row.Repo
	switch row.Kind {
	case AllDirty:
		a.ShowSidebar(workspace.ModeGit)
		a.JumpRepo(slices.Index(a.Repos, r))
	case AllCommit:
		a.UseRepo(r)
		c := row.Commit()
		for _, x := range a.Repos {
			if x != r && x.Hist.Expanded != "" {
				x.Hist.Expanded, x.Hist.Files, x.Hist.FilesFor = "", nil, ""
			}
		}
		if h := &r.Hist; h.Expanded != c.Hash {
			h.Expanded, h.Files, h.FilesFor = c.Hash, nil, ""
			setHistSel(r, row.CI, -1)
			a.loadCommitFiles(r, c)
		}
		// 收起别的仓库展开的文件后这一行可能往上挪了
		a.HistAllSel = slices.IndexFunc(a.AllHistRows(), func(x AllHistRow) bool {
			return x.Kind == AllCommit && x.Repo == r && x.CI == row.CI
		})
	case AllFile:
		a.UseRepo(r)
		a.OpenDiff(r, r.Hist.Files[row.File], false)
	}
}
