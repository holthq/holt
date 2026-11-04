package app

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/fuzzy"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是侧栏"文件"模式的那棵树：每个仓库是一个根节点，下面是它的文件树
// （git ls-files，展开时才读）。底部的筛选框筛所有仓库。

// TreeRow 是侧栏文件树的一行：仓库根、仓库里的目录 / 文件，或一行说明（如读失败）。
type TreeRow struct {
	Repo *Repo
	Root bool   // 仓库根
	Note string // 说明行的文字
	Edit bool   // 输入框：新建时插进来的一行（Path 是目标目录），或正在重命名的那行（见 App.Edit）
	files.Row
}

// treeCache 是按版本缓存的行：仓库列表、展开、筛选（ver）或任一仓库的文件树
// （Files.Version 只增不减，所以和也只增）变了才重算。
type treeCache struct {
	ver       int
	rows      []TreeRow
	key       [2]int
	Truncated bool
}

// TreeTruncated 是筛选结果超过 files.FilterCap，只显示了前面的。
func (a *App) TreeTruncated() bool { return a.tree.Truncated }

// TreeRows 是侧栏文件树现在显示的行。
func (a *App) TreeRows() []TreeRow {
	sum := 0
	for _, r := range a.Repos {
		if r.Expanded && !a.Filtering() {
			r.Files.ReadLazy(r.Root) // 刚展开的被忽略目录读一层（只读磁盘，一层很快）
			a.lazyEmpty(r)           // 大仓库刚展开的目录后台读一层找空目录
		}
		sum += r.Files.Version
	}
	key := [2]int{a.tree.ver, sum}
	if a.tree.rows != nil && a.tree.key == key {
		return a.tree.rows
	}
	a.tree.key = key
	rows := a.tree.rows[:0]
	a.tree.Truncated = false
	if pat := fuzzy.New(a.Filter); !pat.Empty() {
		a.tree.rows = a.filterRows(pat, rows)
		return a.tree.rows
	}
	var tmp []files.Row
	for _, r := range a.Repos {
		codes, marks := files.Marks(r.Status.Changes)
		from := len(rows)
		rows = append(rows, TreeRow{Repo: r, Root: true, Row: files.Row{Name: r.Name, Dir: true, Open: r.Expanded}})
		if !r.Expanded {
			continue
		}
		switch {
		case r.Files.Err != nil:
			rows = append(rows, TreeRow{Repo: r, Note: i18n.Tf("读文件列表失败：%s", r.Files.Err.Error()), Row: files.Row{Depth: 1}})
		case r.Files.Loaded && r.Files.Root != nil && len(r.Files.Root.Dirs)+len(r.Files.Root.Files) == 0:
			rows = append(rows, TreeRow{Repo: r, Note: i18n.T("仓库里没有文件"), Row: files.Row{Depth: 1}})
		default:
			if a.St.Settings.TreeNoCompact {
				tmp = r.Files.Rows(1, codes, marks, tmp[:0])
			} else {
				tmp = r.Files.CompactRows(1, keepDir(a.Edit, r), codes, marks, tmp[:0])
			}
			for _, fr := range tmp {
				if fr.More > 0 {
					rows = append(rows, TreeRow{Repo: r, Note: i18n.Tf("还有 %d 项没列", fr.More), Row: fr})
					continue
				}
				rows = append(rows, TreeRow{Repo: r, Row: fr})
			}
		}
		if e := a.Edit; e != nil && e.Repo == r {
			rows = withEdit(rows, from, e)
		}
	}
	a.tree.rows = rows
	return rows
}

// filterRows 是筛选时的行：所有仓库里模糊匹配 pat 的文件放在一起比分，取最好的 files.FilterCap 个
// （分一样时路径短的在前，再按仓库的顺序、树的顺序），然后照仓库的顺序分组（各组前面是仓库行），组里按分。
// 一边匹配一边只留着最好的那些（topHits），不把几万个匹配都存下来排（5 万个文件敲一个字母时要几 MB）。
func (a *App) filterRows(pat fuzzy.Pattern, rows []TreeRow) []TreeRow {
	var top topHits
	for i, r := range a.Repos {
		r.Files.Match(pat, func(s files.Scored) { top.offer(repoHit{s, int32(i), top.seen}) })
	}
	all := top.h
	a.tree.Truncated = int(top.seen) > len(all)
	slices.SortFunc(all, func(x, y repoHit) int {
		if x.repo != y.repo {
			return int(x.repo - y.repo)
		}
		return cmp.Compare(y.rank(), x.rank()) // 组里按分（先后各不相同，不用稳定排序）
	})
	var codes map[string]byte
	for i, h := range all {
		r := a.Repos[h.repo]
		if i == 0 || all[i-1].repo != h.repo {
			rows = append(rows, TreeRow{Repo: r, Root: true, Row: files.Row{Name: r.Name, Dir: true, Open: true}})
			codes, _ = files.Marks(r.Status.Changes)
		}
		rows = append(rows, TreeRow{Repo: r, Row: r.Files.HitRow(pat, h.Scored, 1, codes)})
	}
	return rows
}

// repoHit 是筛选匹配到的一个文件、它在 a.Repos 里的下标和匹配到的先后（仓库的顺序、树的顺序）。
type repoHit struct {
	files.Scored
	repo, seq int32
}

// rank 是 h 排在哪：越大越靠前（分高的、一样时路径短的、再一样时先匹配到的）。路径不到 1024 字节、
// 文件不到 1600 万个，三段不会叠在一起。
func (h repoHit) rank() int64 {
	return int64(h.Score)<<40 - int64(len(h.Path))<<24 - int64(h.seq)
}

// topHits 是一边匹配一边留着的最好的 files.FilterCap 个：h 是堆，最差的那个在 h[0]，比它好的才换进来；seen 是匹配到了几个。
type topHits struct {
	h    []repoHit
	seen int32
}

func (t *topHits) offer(x repoHit) {
	t.seen++
	h := t.h
	if len(h) < files.FilterCap {
		h = append(h, x)
		for i := len(h) - 1; i > 0; { // 往上浮：比父节点差就换上去
			p := (i - 1) / 2
			if h[p].rank() <= h[i].rank() {
				break
			}
			h[p], h[i] = h[i], h[p]
			i = p
		}
		t.h = h
		return
	}
	if x.rank() <= h[0].rank() {
		return
	}
	h[0] = x
	for i := 0; ; { // 往下沉：换到更差的那个子节点的位置
		m, l := i, 2*i+1
		if l < len(h) && h[l].rank() < h[m].rank() {
			m = l
		}
		if r := l + 1; r < len(h) && h[r].rank() < h[m].rank() {
			m = r
		}
		if m == i {
			break
		}
		h[i], h[m] = h[m], h[i]
		i = m
	}
}

// filtering 是筛选框里有字（只有空格不算，和 fuzzy.New 去掉的一样）。
func filtering(q string) bool { return strings.TrimSpace(q) != "" }

// Filtering 是文件树在筛选：筛选框里有字（只有空格不算，照常显示树）。
func (a *App) Filtering() bool { return filtering(a.Filter) }

// keepDir 是仓库 r 里不和上下合成一行的目录：输入框对着的那个（新建放在它下面、重命名的就是它），没有为空。
func keepDir(e *TreeEdit, r *Repo) string {
	switch {
	case e == nil || e.Repo != r:
		return ""
	case e.Kind == EditRename:
		return e.Path
	}
	return e.Dir
}

// rowOf 是 rows 里仓库 r 的 rel 那一行：空 rel 是仓库行，目录（dir）也认合并行里的一段；没有为 -1。
func rowOf(rows []TreeRow, r *Repo, rel string, dir bool) int {
	return slices.IndexFunc(rows, func(row TreeRow) bool {
		switch {
		case row.Repo != r || row.Note != "" || row.Edit:
			return false
		case rel == "":
			return row.Root
		case dir:
			return !row.Root && row.Dir && row.Holds(rel)
		}
		return !row.Root && !row.Dir && row.Path == rel
	})
}

// withEdit 把输入框放进仓库 e.Repo 的那段行（rows[from] 是仓库行）：新建时插在目标目录那行下面第一行，
// 重命名时标出那一行。目标还没读到（文件列表在读）就先不放，读到后行会重算。
func withEdit(rows []TreeRow, from int, e *TreeEdit) []TreeRow {
	if e.Kind == EditRename {
		for i := from + 1; i < len(rows); i++ {
			if rows[i].Note == "" && rows[i].Path == e.Path {
				rows[i].Edit = true
				break
			}
		}
		return rows
	}
	at, depth := from, 1
	if e.Dir != "" {
		at = slices.IndexFunc(rows[from+1:], func(row TreeRow) bool { return row.Dir && row.Note == "" && row.Path == e.Dir })
		if at < 0 {
			return rows
		}
		at += from + 1
		depth = rows[at].Depth + 1
	}
	return slices.Insert(rows, at+1, TreeRow{Repo: e.Repo, Edit: true, Row: files.Row{Path: e.Dir, Depth: depth, Dir: e.IsDir}})
}

// TreeRowAt 是第 i 行，越界为 false。
func (a *App) TreeRowAt(i int) (TreeRow, bool) {
	rows := a.TreeRows()
	if i < 0 || i >= len(rows) {
		return TreeRow{}, false
	}
	return rows[i], true
}

// LoadFiles 在后台（重）读仓库的文件列表，并上应用里新建的空目录和被忽略的文件（Files.Extra）。
// 普通文件夹（还没读状态的也是：ls-files 失败了再看）直接列目录。
func (a *App) LoadFiles(r *Repo) {
	gen := r.Files.Start()
	extra := maps.Clone(r.Files.Extra)
	plain := r.Plain
	nested := r.nested
	showIgnored := a.St.Settings.ShowIgnored
	a.ignoredOn = showIgnored
	base := a.excludeBase()
	up := a.Update
	// 包级开关在主线程上读好，后台 goroutine 只用局部的：测试会换 lsFiles、改 files.MaxEmptyScan，
	// 上一个测试的 goroutine 可能还在跑
	ls, maxScan := lsFiles, files.MaxEmptyScan
	a.readyAsk(r, readyTree)
	t0 := time.Now()
	go func() {
		var paths, dirs, ignored, cands []string
		var err error
		ex, exKey := repoExclude(r.Root, base)
		hide := ex.Matcher()
		hidden := 0
		if !plain {
			var l git.Listing
			l, err = ls(r.ctx, r.Root)
			paths, cands = l.Paths, slices.Concat(l.Links, l.Others) // 符号链接：已跟踪的看索引，未跟踪的才看磁盘
			plain = err != nil && r.ctx.Err() == nil && git.NotRepo(r.Root)
			if err == nil && showIgnored {
				ignored, _ = git.LsIgnored(r.ctx, r.Root) // 读不到只是不显示
				ignored = dropNested(ignored, nested)
			}
		}
		if plain {
			paths, dirs, cands, err = files.Walk(r.ctx, r.Root)
		}
		var links map[string]files.Link
		if err == nil {
			var more []string
			paths, more, extra = files.WithExtra(r.Root, paths, extra)
			dirs = append(dirs, more...)
			paths, dirs = dropNested(paths, nested), dropNested(dirs, nested)
			var n1, n2, n3 int // files.exclude（exclude.go）：目录合了底下整棵去掉
			paths, n1 = hide.Filter(paths)
			dirs, n2 = hide.Filter(dirs)
			ignored, n3 = hide.Filter(ignored)
			cands, _ = hide.Filter(cands)
			hidden = n1 + n2 + n3
			links = files.ReadLinks(r.Root, dropNested(cands, nested))
		}
		took := time.Since(t0)
		scan := err == nil && !plain && len(paths) <= maxScan
		up(func() {
			if r.Files.Done(gen, paths, err, ignored, dirs...) {
				defer a.readyTreeDone(r, took, r.Files.Count, hidden, scan)
				if err == nil {
					r.Files.Extra, r.Files.Links = extra, links
					r.exclude, r.excludeKey, r.Excluded = ex, exKey, hidden
				}
				if a.Edit != nil && a.Edit.Repo == r {
					a.selectEditRow() // 输入框是在文件列表读到之前开的
				}
				if q := a.revealLater; q != nil && q.repo == r {
					a.revealLater = nil
					switch {
					case !q.follow:
						a.Reveal(r, q.rel)
					case a.treeShown() && a.Edit == nil && r.Files.Has(q.rel):
						a.revealIn(r, q.rel, true)
					}
				}
			}
		})
		if err != nil || plain || len(paths) > maxScan {
			return // 普通文件夹的空目录 Walk 已经列了
		}
		t1 := time.Now()
		empty, _ := hide.Filter(emptyDirs(r.ctx, r.Root, nested, showIgnored)) // 树先画出来，空目录随后补上
		// 另起一个变量：上面那个闭包可能还没在界面线程上跑，它读的 took 不能再改
		emptyTook := time.Since(t1)
		up(func() {
			r.Files.SetEmpty(gen, empty)
			a.readyStep(r, readyEmpty, emptyTook)
		})
	}()
}

// emptyDirs 是整棵找到的空目录（files.FindEmptySkip）：被忽略的目录（git ls-files --directory 列出来的 node_modules/ 那样的）
// 不进去找；去掉嵌套仓库里的；不显示被忽略的文件时再去掉被忽略的。
// 子模块、被忽略的目录里的由 Files.SetEmpty 去掉。
func emptyDirs(ctx context.Context, root string, nested []string, showIgnored bool) []string {
	var skip []string
	if ig, err := git.LsIgnored(ctx, root); err == nil { // 列不了就整棵找
		skip = slices.DeleteFunc(ig, func(p string) bool { return !strings.HasSuffix(p, "/") })
	}
	dirs := dropNested(files.FindEmptySkip(ctx, root, skip), nested)
	if showIgnored {
		return dirs
	}
	return notIgnored(ctx, root, dirs)
}

// notIgnored 是 dirs 里没被 .gitignore 忽略的（一个 git check-ignore 进程）；问不了时一个都不要。
func notIgnored(ctx context.Context, root string, dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	ig, err := git.CheckIgnore(ctx, root, dirs)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(dirs, func(d string) bool { return ig[d] })
}

// ToggleShowIgnored 开关"显示被忽略的文件"：所有窗口的文件树都重读（SettingsChanged 里）。
func (a *App) ToggleShowIgnored() {
	a.St.Settings.ShowIgnored = !a.St.Settings.ShowIgnored
	a.SettingsChanged()
}

// reloadIgnored 在"显示被忽略的文件"和文件树读的时候不一样时重读所有仓库的文件列表（关掉时被忽略的行和读到的一层都丢掉）。
func (a *App) reloadIgnored() {
	if a.ignoredOn == a.St.Settings.ShowIgnored {
		return
	}
	a.ignoredOn = a.St.Settings.ShowIgnored
	a.reloadAllFiles()
}

// EnsureFiles 读仓库的文件列表，已经读过或正在读就什么都不做。
func (a *App) EnsureFiles(r *Repo) {
	if !r.Files.Loaded && !r.Files.Loading {
		a.LoadFiles(r)
	}
}

// ToggleRepo 展开或收起文件树里的一个仓库。
func (a *App) ToggleRepo(r *Repo) {
	r.Expanded = !r.Expanded
	a.tree.ver++
	if r.Expanded {
		a.EnsureFiles(r)
	}
}

// ToggleDir 展开或收起仓库里的一个目录。p 在合并行里时按那一行来（ToggleRow：收起只收最深那段，展开每一层都展开）。
func (a *App) ToggleDir(r *Repo, p string) {
	if !a.Filtering() {
		rows := a.TreeRows()
		if i := rowOf(rows, r, p, true); i >= 0 && p != "" {
			r.Files.ToggleRow(rows[i].Row)
			return
		}
	}
	r.Files.Toggle(p)
}

// CollapseAll 收起所有仓库的所有目录，只留当前仓库展开。
func (a *App) CollapseAll() {
	cur := a.Current()
	for _, r := range a.Repos {
		r.Files.CollapseAll()
		r.Expanded = r == cur
	}
	a.tree.ver++
}

// SetFilter 改筛选词；有筛选时把还没读的仓库的文件列表都读上。选中的留着，筛掉的那些不算（见 ChosenRows）。
func (a *App) SetFilter(q string) {
	a.Filter = q
	a.FileSel = -1
	a.tree.ver++
	if filtering(q) {
		for _, r := range a.Repos {
			a.EnsureFiles(r)
		}
	}
}

// ClickTreeRow 处理文件树里选中变了（点击或键盘）和回车：仓库行切到这个仓库并展开 / 收起，
// 目录展开收起，文件打开（当前仓库也切到它的仓库）。键盘走到一行只选中不打开；点到的那行（指针在它上面）才打开预览。
// 选了多行（⌘点、⇧点、⌘A）时点击不预览，回车把选中的文件都打开。alt 是按着 ⌥ 点的：文件不预览，在侧边打开（OpenFileSide）。
func (a *App) ClickTreeRow(submit, alt bool) {
	row, ok := a.TreeRowAt(a.FileSel)
	if !ok || row.Note != "" || row.Edit {
		return
	}
	switch n := a.FileChosen.Len(); {
	case n > 1, n == 1 && !a.FileChosen.Has(row.Key()):
		// 选了多行，或 ⌘点把光标那行取消了（光标还在它上面）
		if submit {
			a.OpenRows(a.ChosenRows())
		}
		return
	case n == 0 && !submit:
		return // ⌘点把唯一选中的那行取消了
	}
	click := a.FileHover == a.FileSel
	r := row.Repo
	a.treeClick.toggled = false
	switch {
	case row.Root && (submit || click):
		i := slices.Index(a.Repos, r)
		if a.Current() != r {
			a.SelectRepo(i)
		} else if !a.Filtering() {
			a.ToggleRepo(r)
			a.treeClick.toggled = !submit
		}
	case row.Dir && (submit || click):
		r.Files.ToggleRow(row.Row)
		a.treeClick.toggled = !submit
	case row.Sub != 0 && submit:
		a.UseRepo(r)
		a.OpenSubmodule(r, row.Path)
	case row.Sub != 0 && click:
		a.UseRepo(r) // 单击只选中：子模块不是文件，没有预览
	case !row.Dir && submit:
		a.UseRepo(r)
		a.OpenFile(r, row.Path, true)
	case !row.Dir && click && alt:
		a.UseRepo(r)
		a.OpenFileSide(r, row.Path)
	case !row.Dir && click:
		a.UseRepo(r)
		a.OpenFile(r, row.Path, false)
	}
}

// OpenSideChosen 是文件树里的 ⌥回车：选中的文件都在侧边打开（OpenRowsSide），没选就是光标那行；
// 只有一个目录 / 仓库行时和回车一样展开 / 收起。
func (a *App) OpenSideChosen() {
	if rows := a.ChosenRows(); len(rows) == 1 && rows[0].Dir {
		a.ClickTreeRow(true, false)
	} else {
		a.OpenRowsSide(rows)
	}
}

// TreeRight 是文件树里按 →：展开选中的仓库 / 目录，已经展开就走到第一个子项。
func (a *App) TreeRight() {
	row, ok := a.TreeRowAt(a.FileSel)
	if !ok || !row.Dir {
		return
	}
	switch {
	case row.Root && !row.Open:
		a.ToggleRepo(row.Repo)
	case !row.Root && !row.Open:
		row.Repo.Files.ToggleRow(row.Row) // 合并行每一层都展开
	case a.FileSel+1 < len(a.TreeRows()):
		a.FileSel++
		a.chooseOnly(a.FileSel)
	}
}

// TreeLeft 是文件树里按 ←：收起选中的仓库 / 目录，已经收起就走到上一级。
func (a *App) TreeLeft() {
	row, ok := a.TreeRowAt(a.FileSel)
	if !ok {
		return
	}
	switch {
	case row.Root && row.Open && !a.Filtering():
		a.ToggleRepo(row.Repo)
		return
	case row.Dir && !row.Root && row.Open:
		row.Repo.Files.ToggleRow(row.Row) // 合并行只收最深那段
		return
	case row.Root:
		return
	}
	rows := a.TreeRows()
	for i := a.FileSel - 1; i >= 0; i-- {
		if rows[i].Dir && (rows[i].Root || rows[i].Depth < row.Depth) {
			a.FileSel = i
			a.chooseOnly(i)
			a.FileList.ScrollIntoView(i)
			return
		}
	}
}

// OpenFirstMatch 是在筛选框里按回车：打开选中的文件，没选就打开第一个匹配的文件。
func (a *App) OpenFirstMatch() {
	if row, ok := a.TreeRowAt(a.FileSel); ok && !row.Dir && row.Note == "" {
		a.OpenFile(row.Repo, row.Path, true)
		return
	}
	for _, row := range a.TreeRows() {
		if !row.Dir && row.Note == "" {
			a.OpenFile(row.Repo, row.Path, true)
			return
		}
	}
}

// Reveal 在文件树里展开到某个文件或目录并选中它（标签菜单和面包屑的"在文件树里显示"）；
// 目录也展开，空 rel 是仓库那一行。清掉筛选、侧栏切到文件模式。
func (a *App) Reveal(r *Repo, rel string) {
	if a.Filter != "" {
		a.SetFilter("")
	}
	a.revealIn(r, rel, false)
	a.ShowSidebar(workspace.ModeFiles)
	a.followed() // 刚露出来的树不再跟当前文件，不然会盖掉这次要显示的
}

// revealIn 展开到 rel（文件或目录，空 rel 是仓库那一行）、选中、滚到它那里。不清筛选、不切侧栏、不动焦点和当前仓库
// （Reveal 和跟随当前文件共用）。文件列表还没读到就等读到再选；follow 是跟随发起的（读到时再看一次能不能跟）。
func (a *App) revealIn(r *Repo, rel string, follow bool) {
	if !r.Expanded {
		r.Expanded = true
		a.tree.ver++
	}
	a.EnsureFiles(r)
	dir := rel == "" || r.Files.Dir(rel) != nil || r.Files.DirLink(rel)
	r.Files.Reveal(rel)
	if dir && rel != "" {
		r.Files.SetOpen(rel, true)
	}
	a.setTreeSel(rowOf(a.TreeRows(), r, rel, dir)) // 合并行里的一段选中那一行
	if a.FileSel >= 0 {
		a.FileList.ScrollIntoView(a.FileSel)
	}
	if a.FileSel < 0 && !r.Files.Loaded {
		a.revealLater = &revealReq{r, rel, follow} // 文件列表读到之后再选中
	}
}

// follow 是跟随当前文件记下的：跟过的那个标签、上一帧文件树露没露着。
type follow struct {
	key   docs.Key
	shown bool
}

// treeShown 是文件树露着：侧栏开着、在文件模式、没在筛选。
func (a *App) treeShown() bool {
	return a.St.Sidebar && a.St.SidebarMode == workspace.ModeFiles && !a.Filtering()
}

// followed 记下当前标签已经跟过了（文件树也算露着）。
func (a *App) followed() {
	a.follow.shown = a.treeShown()
	if d := a.CurrentDoc(); d != nil {
		a.follow.key = d.Key()
	}
}

// FollowCurrent 是"文件树跟随当前文件"：当前标签换了（切标签、换组、从 ⌘P / 搜索 / 链接打开、关掉标签后落到别的、
// 会话恢复……）就把文件树展开到它、选中、滚到它那里。界面每帧画之前调一次，所以换当前标签的各条路只有这一个出口。
// 只动树：不换当前仓库、不切侧栏模式、不抢键盘。文件树没露着、正在输入名字、正在拖、设置关着都不跟；
// 文件树重新露出来（切回文件模式、清掉筛选）时跟一次；文件树自己打开的（光标就在它上面，或在多选里）不再跟。返回树变了没有。
func (a *App) FollowCurrent() bool {
	shown := a.treeShown()
	if shown && !a.follow.shown {
		a.follow.key = docs.Key{} // 刚露出来：跟一次
	}
	a.follow.shown = shown
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.File || d.Root == "" || d.Commit != "" ||
		!shown || a.St.Settings.TreeNoFollow || a.Edit != nil || a.TreeDragging {
		return false
	}
	k := d.Key()
	if k == a.follow.key {
		return false
	}
	a.follow.key = k
	r := a.RepoOf(d)
	if r == nil {
		return false
	}
	if row, ok := a.TreeRowAt(a.FileSel); ok && row.Repo == r && !row.Dir && row.Note == "" && row.Path == d.Rel {
		return false // 文件树自己打开的，或已经选着它
	}
	if a.FileChosen.Len() > 1 && a.FileChosen.Has(FileKey{Root: r.Root, Path: d.Rel}) {
		return false // 选了几行一起打开的（回车、⌥回车）：多选留着
	}
	if r.Files.Loaded && !r.Files.Has(d.Rel) {
		return false // 不在树里（被忽略的、刚删掉的……）
	}
	a.revealIn(r, d.Rel, true)
	return true
}

// RevealCurrent 是文件树头上的"定位当前文件"和命令 file.revealCurrent：不管跟随开没开，清掉筛选、显示到当前文件。
func (a *App) RevealCurrent() {
	if a.CanRevealCurrent() {
		d := a.CurrentDoc()
		a.Reveal(a.RepoOf(d), d.Rel)
	}
}

// CanRevealCurrent 是当前标签是仓库里的文件（"定位当前文件"能用）。
func (a *App) CanRevealCurrent() bool {
	d := a.CurrentDoc()
	return d != nil && d.Kind == docs.File && d.Root != "" && a.RepoOf(d) != nil
}

// setTreeSel 是应用自己选中文件树的第 i 行（显示、新建、重命名之后），多选换成只选它。列表会让选中项跟着它的键走：
// 行变了而新选的行号碰巧和上一帧一样时，它会把选中挪回旧的那项；界面画完列表后用 TakeTreeSel 再设一次。
func (a *App) setTreeSel(i int) {
	a.FileSel, a.treeSel = i, &i
	a.chooseOnly(i)
}

// TakeTreeSel 是这一帧之前应用自己选的行（见 setTreeSel），取一次就清掉。
func (a *App) TakeTreeSel() (int, bool) {
	p := a.treeSel
	a.treeSel = nil
	if p == nil {
		return -1, false
	}
	return *p, true
}

// revealReq 是等文件列表读到之后要在树里选中的文件或目录。
type revealReq struct {
	repo   *Repo
	rel    string
	follow bool // 跟随当前文件发起的（读到时不切侧栏）
}

// dropNested 去掉落在套在仓库里面的工作树（如 .claude/worktrees/x）下的路径：那是另一份检出，不是这个仓库的文件。
// 未跟踪的它在 ls-files 里是一条 "x/"。
func dropNested(paths, nested []string) []string {
	if len(nested) == 0 {
		return paths
	}
	return slices.DeleteFunc(paths, func(p string) bool {
		p = strings.TrimSuffix(p, "/")
		return slices.ContainsFunc(nested, func(n string) bool { return files.Under(p, n) })
	})
}
