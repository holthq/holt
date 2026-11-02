package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是 Git 工作树（git worktree）：工作树归主仓库管，Git 栏里主仓库段下一个可折叠的"工作树"块列出它链接的
// 工作树（链接的工作树段里列出主的和别的链接的），默认收着。列表跟着状态一起读（没有 .git/worktrees 的仓库一条命令都不跑）；
// 每条的改动数和领先 / 落后只在块展开时懒算、并发 WtConcurrency 路；展开着时盯着各工作树的目录、变了只重算那一条（wtwatch.go）。
// 要在哪个工作树里干活就"在工作区中打开"，它成为普通的一段。新建、移除、清理、锁定只在用户点了之后经 RunOp 跑，丢数据的先问。

// WtConcurrency 是懒算工作树状态时同时跑的 git 命令数。
const WtConcurrency = 4

// Worktree 是仓库的一个别的工作树。
type Worktree struct {
	git.Worktree
	Real string    // 解开符号链接的路径（认"已打开"、套在仓库里面的目录用）
	Time time.Time // HEAD 的提交时间（排序用）；读不出来为零
}

// WtInfo 是一个工作树懒算的状态。
type WtInfo struct {
	Dirty         int // 未提交的改动数（git status --porcelain 的条数）
	Ahead, Behind int // 比主工作树的 HEAD 多 / 少几个提交
	Err           error
}

// wtList 是后台读到的工作树列表（RefreshRepo 里和状态一起读）。
type wtList struct {
	real   string     // 仓库根解开符号链接
	wts    []Worktree // 不含自己：主的在最前，别的按提交时间倒序
	nested []string   // 套在仓库根下面的别的工作树（仓库内路径，斜杠分隔），文件树不显示
	err    error
}

// readWorktrees 读仓库的别的工作树。普通仓库先看一眼 <公共目录>/worktrees：没有就是没有工作树，一条命令都不跑。
func readWorktrees(ctx context.Context, root string, x git.Extra) wtList {
	l := wtList{real: git.RealPath(root)}
	if x.CommonDir == "" {
		return l
	}
	if !x.Linked {
		if _, err := os.Stat(filepath.Join(x.CommonDir, "worktrees")); err != nil {
			return l
		}
	}
	all, err := git.Worktrees(ctx, root)
	if err != nil {
		l.err = err
		return l
	}
	heads := make([]string, 0, len(all))
	for _, w := range all {
		if w.Head != "" && !slices.Contains(heads, w.Head) {
			heads = append(heads, w.Head)
		}
	}
	times := git.CommitTimes(ctx, root, heads)
	for _, w := range all {
		rp := git.RealPath(w.Path)
		if rp == l.real || strings.EqualFold(rp, l.real) {
			continue
		}
		l.wts = append(l.wts, Worktree{Worktree: w, Real: rp, Time: times[w.Head]})
		if rel := git.Under(l.real, rp); rel != "" {
			l.nested = append(l.nested, rel)
		}
	}
	slices.SortStableFunc(l.wts, func(a, b Worktree) int {
		switch {
		case a.Main != b.Main:
			if a.Main {
				return -1
			}
			return 1
		case a.Time.After(b.Time):
			return -1
		case b.Time.After(a.Time):
			return 1
		}
		return 0
	})
	return l
}

// setWorktrees 放进读到的工作树列表（主线程，RefreshRepo 读完状态时）：套在里面的目录变了就重读文件树；
// 块展开着就重新懒算（算完之前还显示上一次的数）、收着就把算过的扔掉（下次展开再算）；右栏在看的那个工作树没了就不看了。
func (a *App) setWorktrees(r *Repo, x git.Extra, l wtList) {
	if l.err != nil && r.ctx.Err() == nil {
		r.WtErr = l.err
		return // 读失败了：留着上一次的列表
	}
	r.WtErr = nil
	r.real = l.real
	r.Linked, r.MainRoot = x.Linked, ""
	if x.Linked {
		if i := slices.IndexFunc(l.wts, func(w Worktree) bool { return w.Main }); i >= 0 && !l.wts[i].Bare {
			r.MainRoot = l.wts[i].Path
		}
	}
	r.Worktrees = l.wts
	if !slices.Equal(r.nested, l.nested) {
		r.nested = l.nested
		a.tree.ver++
		if r.Files.Loaded || r.Files.Loading {
			a.LoadFiles(r)
		}
	}
	if h := &r.Hist; h.RefKind == RefWorktree {
		i := slices.IndexFunc(r.Worktrees, func(w Worktree) bool { return w.Path == h.RefPath })
		switch {
		case i < 0 || r.Worktrees[i].Bare:
			a.ClearRefHistory(r)
		case wtRev(r.Worktrees[i].Worktree) != h.Ref: // 分离 HEAD 动了，或换了分支
			h.Ref, h.RefName = wtRev(r.Worktrees[i].Worktree), wtName(r.Worktrees[i].Worktree)
			a.LoadHistory(r, true)
		}
	}
	if r.WtOpen && len(r.Worktrees) > 0 {
		a.loadWtInfo(r)
	} else {
		r.WtInfo = nil
		r.wtGen++
	}
	a.syncWtWatch(r)
}

// clearWorktrees 是仓库成了普通文件夹或读状态失败：没有工作树了。
func (a *App) clearWorktrees(r *Repo) {
	r.Worktrees, r.WtInfo, r.Linked, r.MainRoot, r.WtErr = nil, nil, false, "", nil
	r.wtGen++
	if r.nested != nil {
		r.nested = nil
		a.tree.ver++
		if r.Files.Loaded || r.Files.Loading {
			a.LoadFiles(r)
		}
	}
	if r.Hist.RefKind == RefWorktree {
		a.ClearRefHistory(r)
	}
	a.syncWtWatch(r)
}

// HasWorktrees 是 Git 栏里这个仓库段有"工作树"块：有别的工作树，或者自己就是链接的工作树。
func (r *Repo) HasWorktrees() bool { return len(r.Worktrees) > 0 || r.Linked }

// loadWtInfo 在后台算每个工作树的改动数和领先 / 落后（并发 WtConcurrency 路），全算完一起放进 WtInfo。
func (a *App) loadWtInfo(r *Repo) {
	r.wtGen++
	gen := r.wtGen
	wts := slices.Clone(r.Worktrees)
	base := wtBase(r)
	r.WtLoading = true
	up := a.Update
	ctx := r.ctx
	go func() {
		res := make(map[string]*WtInfo, len(wts))
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, WtConcurrency)
		for _, w := range wts {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				info := wtInfo(ctx, r.Root, base, w.Worktree)
				mu.Lock()
				res[w.Path] = info
				mu.Unlock()
			}()
		}
		wg.Wait()
		up(func() {
			if r.wtGen != gen || r.gone() {
				return
			}
			r.WtInfo, r.WtLoading = res, false
		})
		// 和搜完一样：几十个工作树一轮是近百条 git 命令，读输出的缓冲都成了垃圾，应用闲下来不会再触发 GC，
		// 这几 MB 一直算在 footprint 里（atlas 的 45 个约 4 MB）。算完收一次、还给系统。
		if ctx.Err() == nil {
			debug.FreeOSMemory()
		}
	}()
}

// wtBase 是算领先 / 落后时比的基准：主工作树的 HEAD；自己是链接的就用列表里主的那条。
func wtBase(r *Repo) string {
	if !r.Linked {
		return r.Extra.Head
	}
	if i := slices.IndexFunc(r.Worktrees, func(w Worktree) bool { return w.Main }); i >= 0 {
		return r.Worktrees[i].Head
	}
	return ""
}

// wtInfo 算一个工作树的状态。可清理的（目录没了）和 bare 的没有状态；主的那条不算领先落后（它就是比较的基准）。
func wtInfo(ctx context.Context, root, base string, w git.Worktree) *WtInfo {
	info := &WtInfo{}
	if w.Prunable || w.Bare {
		return info
	}
	info.Dirty, info.Err = git.WorktreeDirty(ctx, w.Path)
	if !w.Main && base != "" && w.Head != "" && w.Head != base {
		var err error
		info.Ahead, info.Behind, err = git.AheadBehind(ctx, root, base, w.Head)
		if info.Err == nil {
			info.Err = err
		}
	}
	return info
}

// ToggleWorktrees 展开 / 收起 Git 栏里仓库段的"工作树"块（记进会话的 wtOpen）；展开时重算各条的状态、开始盯着各工作树的目录。
func (a *App) ToggleWorktrees(r *Repo) {
	r.WtOpen = !r.WtOpen
	if r.WtOpen && len(r.Worktrees) > 0 {
		a.loadWtInfo(r) // 收着时没盯着，上次算的可能过时了
	}
	a.syncWtWatch(r)
	a.SaveState()
}

// RefreshWorktrees 重读仓库的工作树列表和（块展开着时）每条的状态："刷新工作树"。
func (a *App) RefreshWorktrees(r *Repo) {
	if r.Plain {
		return
	}
	a.RefreshRepo(r, WhyManual)
}

// WtSummary 是"工作树"块头的数：几条有改动（还没算出来时 loaded 为假）、几条可清理。
func (r *Repo) WtSummary() (dirty, prunable int, loaded bool) {
	for _, w := range r.Worktrees {
		if w.Prunable {
			prunable++
		}
		if info := r.WtInfo[w.Path]; info != nil && info.Dirty > 0 {
			dirty++
		}
	}
	return dirty, prunable, r.WtInfo != nil
}

// WtRepo 是工作区里就是这个工作树的那一段（"已打开"），没有为 nil。路径解开符号链接比。
func (a *App) WtRepo(w Worktree) *Repo {
	for _, r := range a.Repos {
		if r.Root == w.Path || r.real != "" && (r.real == w.Real || strings.EqualFold(r.real, w.Real)) {
			return r
		}
	}
	return nil
}

// OpenWorktree 是"在工作区中打开"：已经是工作区里的一段就跳过去，否则加进来（和添加仓库一样记进 added）。
func (a *App) OpenWorktree(w Worktree) {
	if r := a.WtRepo(w); r != nil {
		a.JumpRepo(slices.Index(a.Repos, r))
		return
	}
	if w.Prunable {
		a.Toast = i18n.Tf("%s 的目录已经不在了", w.Name())
		return
	}
	if err := a.AddRepoDir(w.Path); err != nil {
		a.Fail(i18n.T("打开工作树失败"), err)
		return
	}
	if r := a.Current(); r != nil {
		a.JumpRepo(a.Selected)
	}
}

// OpenMainRepo 是链接工作树的"打开主仓库"。
func (a *App) OpenMainRepo(r *Repo) {
	if r.MainRoot == "" {
		return
	}
	if i := slices.IndexFunc(r.Worktrees, func(w Worktree) bool { return w.Main }); i >= 0 {
		a.OpenWorktree(r.Worktrees[i])
	}
}

// wtRev 是右栏看一个工作树的历史时从哪读：有分支就是那个分支（工作树里提交了也跟着），分离 HEAD 是它的哈希。
func wtRev(w git.Worktree) string {
	if w.Branch != "" {
		return "refs/heads/" + w.Branch
	}
	return w.Head
}

// wtName 是工作树在右栏标题里的名字：分支，分离 HEAD 写"分离 HEAD · 短哈希"。
func wtName(w git.Worktree) string {
	if w.Branch != "" {
		return w.Branch
	}
	return WtDetachedName(w.Head)
}

// WtDetachedName 是分离 HEAD 的工作树显示的名字。
func WtDetachedName(head string) string {
	return i18n.Tf("分离 HEAD · %s", git.Short(head))
}

// ShowWorktreeHistory 是右键菜单的"查看分支历史"：右栏历史看工作树的那个分支（或分离的 HEAD）往前的提交，
// 提交图照画；已经在看就回到这个仓库自己的历史。在 Git 栏里选一项改动、或者点右栏那个胶囊上的 × 也回去。
func (a *App) ShowWorktreeHistory(r *Repo, w Worktree) {
	if w.Bare || w.Head == "" {
		return
	}
	if r.Hist.RefPath == w.Path {
		a.UseRepo(r)
		a.ClearRefHistory(r)
		return
	}
	a.viewWorktree(r, w)
}

// viewWorktree 是 Git 栏的选中停在一个工作树上（单击、↑↓）：当前仓库切到它的仓库，右栏看它的历史；已经在看就不动。
func (a *App) viewWorktree(r *Repo, w Worktree) {
	a.UseRepo(r)
	if w.Bare || w.Head == "" || r.Hist.RefPath == w.Path {
		return
	}
	a.ShowRefHistory(r, RefWorktree, wtRev(w.Worktree), wtName(w.Worktree))
	r.Hist.RefPath = w.Path
}

// RefKind 是右栏历史从哪读。
type RefKind int

const (
	RefNone     RefKind = iota // 仓库自己的 HEAD
	RefWorktree                // 一个工作树（RefPath 是它的目录）
	RefTag                     // 一个标签
)

// ShowRefHistory 让右栏（切到"当前仓库"视图）看 r 从 rev 往前的历史，标题写 name；r 成为当前仓库。
func (a *App) ShowRefHistory(r *Repo, kind RefKind, rev, name string) {
	a.UseRepo(r)
	h := &r.Hist
	h.RefKind, h.Ref, h.RefName, h.RefPath = kind, rev, name, ""
	h.Sel, h.PrevSel = -1, -1
	a.St.HistAll, a.St.RightBar = false, true
	a.SaveState()
	a.LoadHistory(r, true)
}

// ShowTagHistory 是分支菜单里"标签"下的一项：右栏看那个标签往前的历史；已经在看就回到这个仓库自己的历史。
func (a *App) ShowTagHistory(r *Repo, t git.Tag) {
	if r.ViewingTag(t.Name) {
		a.UseRepo(r)
		a.ClearRefHistory(r)
		return
	}
	a.ShowRefHistory(r, RefTag, t.Hash, t.Name)
}

// ViewingTag 是右栏正看着标签 name 的历史。
func (r *Repo) ViewingTag(name string) bool {
	return r.Hist.RefKind == RefTag && r.Hist.RefName == name
}

// syncTagHistory 是读完了标签：右栏在看的标签删了就回到自己的历史，挪到了别的提交就跟着重读。
func (a *App) syncTagHistory(r *Repo) {
	h := &r.Hist
	if h.RefKind != RefTag {
		return
	}
	i := slices.IndexFunc(r.Tags, func(t git.Tag) bool { return t.Name == h.RefName })
	switch {
	case i < 0 && len(r.Tags) < git.MaxTags: // 只读最新的 MaxTags 个：读满了还没有的不算删了
		a.ClearRefHistory(r)
	case i >= 0 && r.Tags[i].Hash != h.Ref:
		h.Ref = r.Tags[i].Hash
		a.LoadHistory(r, true)
	}
}

// ClearRefHistory 回到仓库自己的历史。Git 栏的选中停在那个工作树上的话也不再停（再点它就又看它的历史）。
func (a *App) ClearRefHistory(r *Repo) {
	h := &r.Hist
	if h.RefKind == RefNone {
		return
	}
	wt := h.RefPath
	h.RefKind, h.Ref, h.RefName, h.RefPath = RefNone, "", "", ""
	if h.Loaded || h.Loading {
		a.LoadHistory(r, true)
	}
	if wt != "" && a.gitWt == wt {
		a.gitWt = ""
		a.syncChangeSel()
	}
}

// clearRefHistories 是在 Git 栏里选了别的（"点别处"）：除了 keep，看着工作树历史的都回到自己的历史（看标签的不动）。
func (a *App) clearRefHistories(keep *Repo) {
	for _, r := range a.Repos {
		if r != keep && r.Hist.RefKind == RefWorktree {
			a.ClearRefHistory(r)
		}
	}
}

// RefDirty 是右栏在看的工作树有几处未提交的改动（"未提交的改动"那行用）；没算出来、看的是标签是 -1。
func (r *Repo) RefDirty() int {
	if r.Hist.RefKind != RefWorktree {
		return -1
	}
	if info := r.WtInfo[r.Hist.RefPath]; info != nil && info.Err == nil {
		return info.Dirty
	}
	return -1
}

// RefHead 是右栏在看的工作树的 HEAD 哈希（提交图在它的泳道上接"未提交的改动"）；看的是标签就是标签的提交。
func (r *Repo) RefHead() string {
	if r.Hist.RefKind == RefTag {
		return r.Hist.Ref
	}
	if i := slices.IndexFunc(r.Worktrees, func(w Worktree) bool { return w.Path == r.Hist.RefPath }); i >= 0 {
		return r.Worktrees[i].Head
	}
	return ""
}

// ---- 写操作 ----

// 工作树对话框的种类。
const (
	WtNew    = iota // 新建工作树
	WtLock          // 锁定（填原因）
	WtRemove        // 移除（勾"同时删除分支"）
)

// WorktreeDialog 是新建 / 锁定 / 移除工作树的对话框。
type WorktreeDialog struct {
	Open bool
	Kind int
	Repo *Repo
	Wt   Worktree // 锁定、移除的那个

	NewBranch bool   // 新建：建一个新分支（否则检出一个已有的本地分支）
	Name      string // 新分支的名字
	Existing  string // 已有的本地分支
	Base      string // 新分支的起点：空为当前 HEAD，或一个分支名
	Dir       string // 工作树的目录
	DirEdited bool   // 用户改过目录：分支名再变就不跟着改
	OpenAfter bool   // 建好后在工作区中打开

	Reason       string // 锁定的原因（可空）
	DeleteBranch bool   // 移除后同时删除它的分支

	dirFor    string // dirExists 是对哪个目录查的
	dirExists bool
}

// NewWorktree 打开"新建工作树"对话框：默认建新分支、从当前 HEAD、目录是仓库旁边的 <仓库名>-<分支最后一段>。
func (a *App) NewWorktree(r *Repo) {
	if r == nil || r.Plain || !r.Loaded || r.Err != nil {
		return
	}
	a.WtDialog = WorktreeDialog{Open: true, Kind: WtNew, Repo: r, NewBranch: true, OpenAfter: true}
	a.WtDirFollow()
}

// WtFreeBranches 是新建工作树时可以检出的已有本地分支：没在任何工作树里检出的。
func (r *Repo) WtFreeBranches() []string {
	var names []string
	for _, b := range r.Branches {
		if !b.Remote && b.Worktree == "" {
			names = append(names, b.Name)
		}
	}
	return names
}

// WtBranch 是对话框里要检出的分支：新建的名字或选的已有分支。
func (d *WorktreeDialog) WtBranch() string {
	if d.NewBranch {
		return strings.TrimSpace(d.Name)
	}
	return d.Existing
}

// WtDirFollow 在分支变了之后（用户没改过目录时）把目录换成默认的。
func (a *App) WtDirFollow() {
	d := &a.WtDialog
	if d.DirEdited || d.Repo == nil {
		return
	}
	d.Dir = WtDefaultDir(d.Repo, d.WtBranch())
}

// WtDefaultDir 是新工作树的默认目录：主仓库旁边的 <主仓库名>-<分支最后一段>（照 atlas / atlas-fix-io 的习惯）。
func WtDefaultDir(r *Repo, branch string) string {
	main := r.Root
	if r.Linked && r.MainRoot != "" {
		main = r.MainRoot
	}
	seg := branch[strings.LastIndexByte(branch, '/')+1:]
	if seg == "" {
		seg = "worktree"
	}
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"-"+seg)
}

// WtDirExists 是对话框里的目录已经有东西了（不让建）。按目录缓存，不是每帧都去看磁盘。
func (a *App) WtDirExists() bool {
	d := &a.WtDialog
	dir := strings.TrimSpace(d.Dir)
	if dir != d.dirFor {
		d.dirFor = dir
		_, err := os.Lstat(dir)
		d.dirExists = dir != "" && err == nil
	}
	return d.dirExists
}

// WtCanCreate 是新建对话框里填的能建：有分支、目录是绝对路径且还不存在。
func (a *App) WtCanCreate() bool {
	d := &a.WtDialog
	dir := strings.TrimSpace(d.Dir)
	return d.WtBranch() != "" && filepath.IsAbs(dir) && !a.WtDirExists()
}

// CreateWorktree 按对话框建工作树（git worktree add），建好后（勾着的话）在工作区中打开。
func (a *App) CreateWorktree() {
	d := a.WtDialog
	r := d.Repo
	if r == nil || !a.WtCanCreate() {
		return
	}
	a.WtDialog.Open = false
	dir := filepath.Clean(strings.TrimSpace(d.Dir))
	branch := d.WtBranch()
	args := []string{"worktree", "add"}
	if d.NewBranch {
		args = append(args, "-b", branch, dir)
		if d.Base != "" {
			args = append(args, d.Base)
		}
	} else {
		args = append(args, dir, branch)
	}
	a.RunOpThen(r, i18n.Tf("新建工作树 %s", branch), func(string) {
		if d.OpenAfter {
			if err := a.AddRepoDir(dir); err != nil {
				a.Fail(i18n.T("打开工作树失败"), err)
				return
			}
			a.JumpRepo(a.Selected)
		}
	}, args...)
}

// LockWorktree 打开"锁定工作树"对话框（填原因，可空）。
func (a *App) LockWorktree(r *Repo, w Worktree) {
	a.WtDialog = WorktreeDialog{Open: true, Kind: WtLock, Repo: r, Wt: w}
}

// DoLockWorktree 按对话框锁定（git worktree lock）：锁着的工作树 prune 不清、remove 要强制两次。
func (a *App) DoLockWorktree() {
	d := a.WtDialog
	a.WtDialog.Open = false
	args := []string{"worktree", "lock"}
	if reason := strings.TrimSpace(d.Reason); reason != "" {
		args = append(args, "--reason", reason)
	}
	a.RunOp(d.Repo, i18n.Tf("锁定工作树 %s", d.Wt.Name()), append(args, d.Wt.Path)...)
}

// UnlockWorktree 解锁（git worktree unlock）。
func (a *App) UnlockWorktree(r *Repo, w Worktree) {
	a.RunOp(r, i18n.Tf("解锁工作树 %s", w.Name()), "worktree", "unlock", w.Path)
}

// RemoveWorktree 打开"移除工作树"对话框：写着路径、分支、有几处未提交的改动，一个"同时删除分支"的勾（默认不勾）。
func (a *App) RemoveWorktree(r *Repo, w Worktree) {
	if w.Main {
		return
	}
	a.WtDialog = WorktreeDialog{Open: true, Kind: WtRemove, Repo: r, Wt: w}
}

// DoRemoveWorktree 按对话框移除（git worktree remove）。git 因为有改动或锁着不肯时再问一次，点了才 --force；
// 移除了的工作树是工作区里的一段就把那段拿掉、关掉它没改过的文件标签；勾了"同时删除分支"再删分支。
func (a *App) DoRemoveWorktree() {
	d := a.WtDialog
	a.WtDialog.Open = false
	a.removeWorktree(d.Repo, d.Wt, d.DeleteBranch, false)
}

func (a *App) removeWorktree(r *Repo, w Worktree, deleteBranch, force bool) {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
		if w.Locked {
			args = append(args, "--force") // 锁着的要两个
		}
	}
	args = append(args, w.Path)
	label := i18n.Tf("移除工作树 %s", w.Name())
	cmd := "$ git " + strings.Join(args, " ")
	a.enqueue(r, &Op{Label: label, Cmd: cmd, args: args, done: func(out string, err error) {
		switch {
		case errors.Is(err, git.ErrCanceled):
			a.Log(r, cmd, canceledText(out, label, nil), true)
		case err != nil && !force && wtRefused(err):
			a.Log(r, cmd, logText(out, err), true)
			if r.gone() {
				return
			}
			a.AskDanger(i18n.Tf("强制移除工作树 %s？", w.Name()),
				i18n.Tf("git 不肯移除：%s\n\n强制移除会丢掉它里面未提交的改动和未跟踪的文件，不能撤销。", err.Error()),
				i18n.T("强制移除"), func() { a.removeWorktree(r, w, deleteBranch, true) })
		case err != nil:
			a.Log(r, cmd, logText(out, err), true)
			if !r.gone() {
				a.Fail(i18n.Tf("%s失败", label), err)
			}
		default:
			a.Log(r, cmd, logText(out, nil), false)
			if r.gone() {
				return
			}
			a.Toast = i18n.Tf("%s完成", label)
			a.dropWorktreeRepo(w)
			if deleteBranch && w.Branch != "" {
				a.deleteBranch(r, w.Branch, false)
			}
		}
	}})
}

// wtRefused 是 git worktree remove 因为有改动、未跟踪的文件或锁着而不肯（加 --force 就行）。
func wtRefused(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "--force") || strings.Contains(msg, "-f -f") || strings.Contains(msg, "locked") ||
		strings.Contains(msg, "modified or untracked")
}

// dropWorktreeRepo 是移除了的工作树在工作区里的那一段：拿掉，关掉它没改过的文件标签（改过的留着，保存会报错）。
func (a *App) dropWorktreeRepo(w Worktree) {
	wr := a.WtRepo(w)
	if wr == nil {
		return
	}
	for _, d := range slices.Clone(a.AllDocs()) {
		if d.Kind == docs.File && d.Root == wr.Root && !d.Modified() {
			a.RemoveDoc(d)
		}
	}
	a.removeRepo(wr) // 移除前它的写命令已经排不进去了：目录没了
}

// deleteBranch 删掉分支（git branch -d）；没合并的再问一次，点了才 -D。
func (a *App) deleteBranch(r *Repo, branch string, force bool) {
	flag := "-d"
	if force {
		flag = "-D"
	}
	args := []string{"branch", flag, branch}
	label := i18n.Tf("删除分支 %s", branch)
	cmd := "$ git " + strings.Join(args, " ")
	a.enqueue(r, &Op{Label: label, Cmd: cmd, args: args, done: func(out string, err error) {
		a.Log(r, cmd, logText(out, err), err != nil)
		switch {
		case r.gone() || errors.Is(err, git.ErrCanceled):
		case err != nil && !force && strings.Contains(err.Error(), "not fully merged"):
			a.AskDanger(i18n.Tf("强制删除分支 %s？", branch), i18n.Tf("%s 还没合并进当前分支或它的上游，删掉后它独有的提交只能从 reflog 找回。", branch),
				i18n.T("强制删除"), func() { a.deleteBranch(r, branch, true) })
		case err != nil:
			a.Fail(i18n.Tf("%s失败", label), err)
		default:
			a.Toast = i18n.Tf("%s完成", label)
		}
	}})
}

// PruneWorktrees 清理失效的工作树（目录已经没了的那些的记录）：先 git worktree prune --dry-run 列出会清掉哪些，问过再清。
func (a *App) PruneWorktrees(r *Repo) {
	if r == nil || r.Plain {
		return
	}
	up := a.Update
	go func() {
		list, err := git.PruneList(r.ctx, r.Root)
		up(func() {
			if r.gone() {
				return
			}
			if err != nil {
				a.Fail(i18n.T("清理工作树失败"), err)
				return
			}
			if len(list) == 0 {
				a.Toast = i18n.T("没有失效的工作树")
				return
			}
			a.Ask(i18n.Tn("清理 %d 个失效的工作树？", len(list)), i18n.Tf("它们的目录已经不在了，清掉的只是 .git/worktrees 里的记录：\n\n%s", strings.Join(list, "\n")),
				i18n.T("清理"), func() { a.RunOp(r, i18n.T("清理工作树"), "worktree", "prune", "-v") })
		})
	}()
}
