package app

import (
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是侧栏的搜索页：在列表里所有仓库的文件里搜文字（internal/search，只读），
// 结果按文件分组。敲字停 150 ms 后自动搜，回车立刻搜；新的一次取消还没搜完的上一次。
// 点一处结果就在预览标签里打开那个文件并选中那一处，双击 / 回车固定标签。
// 可以只搜一个仓库 / 目录（Scope）、排除某几处，↑ ↓ 翻最近搜索；替换在 replace.go。

// SearchDebounce 是停止敲字后多久开始搜。
const SearchDebounce = 150 * time.Millisecond

// SearchState 是搜索页的状态。
type SearchState struct {
	Query search.Query
	Field ui.Handle // 搜索框，⇧⌘F 把焦点给它
	Focus bool      // 下一帧把焦点给搜索框
	Globs bool      // 显示"包含 / 排除"两个框

	Replace   string    // 替换成的文字（空的就是删掉）
	Replacing bool      // 显示替换那一行（⇧⌘H、搜索框左边的箭头）
	ReplField ui.Handle // 替换框
	FocusRepl bool      // 下一帧把焦点给替换框
	MultiQ    bool      // 搜索框是多行的：⇧⏎ 换过行，清空之前一直是
	MultiR    bool      // 替换框是多行的
	CaretQ    int       // 换成多行的框那一帧把搜索框的光标放在第几个字后面，加 1（0 是不放）
	CaretR    int       // 替换框的，同上
	Scope     *SearchScope

	Running bool
	Done    bool          // Res 是一次搜完的结果
	Res     search.Result // 最近一次搜完的结果
	Rows    []SearchRow   // 结果列表的行：文件，展开时跟着它的每一处（排除了的不算）
	List    ui.ListState
	Sel     int // 选中的行，-1 为无
	Hover   int // 这一帧指针所在的行，用来区分点击和键盘移动

	resQ      search.Query      // Res 是按它搜出来的：替换按它核对（框里可能已经改了、还没搜）
	matcher   *search.Matcher   // resQ 编译好的，替换和预览里展开 $1 用
	dismissed map[matchKey]bool // 排除了的几处：不显示、不替换；新搜一次就清掉
	ver       int               // Res 或排除的变了就递增（问"全部替换？"时记下，点了再核对）
	histPos   int               // ↑ ↓ 翻最近搜索翻到第几条（从 1 起），0 是没在翻
	draft     search.Query      // 开始翻之前框里的字和开关，翻回最近那条之后再 ↓ 回到它
	draftGl   bool              // 开始翻之前包含 / 排除展没展开
	collapsed map[fileKey]bool
	gen       int // 每次开始搜递增，旧的结果作废
	tick      int // 每次改查询递增，过期的定时器不再搜
	timer     *time.Timer
	cancel    context.CancelFunc

	lastReplace *replaceUndo     // 上次替换直接写了盘的文件，"撤销上次替换"用（replaceundo.go）；换了搜索词也留着
	follow      map[fileKey]bool // 结果里变了、等着重搜的文件（searchfollow.go）
	followTimer *time.Timer
	following   bool // 在后台重搜，回来之前不再起定时器

	EditorLines int // 搜索编辑器每处前后几行上下文（顶栏的菜单，默认 2；只在这个窗口里，不存设置）
	toEditor    int // 第几次搜索（gen）搜完就开成搜索编辑器（⌥⏎），0 是不开
}

// SearchScope 是只在一个仓库（Dir 空）或它的一个目录里搜（文件树右键"在此搜索"）。只在这个窗口里，不存进 state.json。
type SearchScope struct {
	Root string // 仓库根
	Repo string // 仓库名（显示用）
	Dir  string // 仓库内的目录，空是整个仓库
}

// Label 是范围胶囊上的字：仓库名，或 仓库/目录。
func (sc *SearchScope) Label() string {
	if sc.Dir == "" {
		return sc.Repo
	}
	return sc.Repo + "/" + sc.Dir
}

// MaxSearches 是最近搜索最多记几条（state.json 的 "searchHistory"，所有窗口共用，最近的在前）。
const MaxSearches = 20

// SearchRow 是结果列表的一行：File 时是一个文件（Match 是它的第一处、Count 是处数），否则是第 Match 处。
type SearchRow struct {
	File  bool
	Match int
	Count int
	Open  bool
}

type fileKey struct{ root, path string }

// matchKey 是一处结果在哪：排除按它记。
type matchKey struct {
	root, path string
	line, col  int
}

func keyOf(m search.Match) matchKey { return matchKey{m.Root, m.Path, m.Line, m.Col} }

// ShowSearch 打开侧栏的搜索页并把焦点给搜索框（⇧⌘F）；编辑器里选着一行内的文字就拿它来搜。
func (a *App) ShowSearch() {
	a.ShowSidebar(workspace.ModeSearch)
	a.Search.Focus = true
	if d := a.CurrentDoc(); d != nil && d.Buf != nil {
		if s := d.St.SelectedText(); s != "" && !strings.ContainsAny(s, "\r\n") && s != a.Search.Query.Text {
			a.Search.Query.Text = s
			a.SearchNow()
		}
	}
}

// SearchChanged 在查询（文字、开关、包含 / 排除）变了之后调：停 SearchDebounce 再搜；清空了就清掉结果。
func (a *App) SearchChanged() {
	s := &a.Search
	s.tick++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.Query.Text == "" {
		a.SearchNow()
		return
	}
	tick := s.tick
	up := a.Update
	s.timer = time.AfterFunc(SearchDebounce, func() {
		up(func() {
			if a.Search.tick == tick {
				a.SearchNow()
			}
		})
	})
}

// SearchNow 立刻搜（回车、停止敲字之后），取消还没搜完的上一次；"再读"加上去的上限回到 search.MaxMatches。
func (a *App) SearchNow() { a.search(0, false) }

// MoreStep 是"再读"一次多收几处。
const MoreStep = search.MaxMatches

// CanSearchMore 是结果到了上限、还能"再读"（没到 search.MaxLimit）。
func (s *SearchState) CanSearchMore() bool {
	return s.Done && !s.Running && s.Res.Err == nil && s.Res.Truncated && s.resQ.MatchCap() < search.MaxLimit
}

// AtMaxLimit 是结果到了 search.MaxLimit，不能再读了。
func (s *SearchState) AtMaxLimit() bool {
	return s.Done && s.Res.Err == nil && s.Res.Truncated && s.resQ.MatchCap() >= search.MaxLimit
}

// SearchMore 是摘要后面的"再读 5000"：照同样的条件多收 MoreStep 处重搜（从头搜，前面的结果不变），
// 收起的文件、排除的几处、滚动和选中都留着。框里的条件已经改了（还没搜）就是普通的重搜。
func (a *App) SearchMore() {
	s := &a.Search
	if !s.CanSearchMore() {
		return
	}
	if EntryOf(s.Query) != EntryOf(s.resQ) {
		a.SearchNow()
		return
	}
	a.search(min(s.resQ.MatchCap()+MoreStep, search.MaxLimit), true)
}

// Research 是摘要那一行的"重新搜索"：按搜出这些结果的条件、同样的处数再搜一遍（结果跟着磁盘变只重读结果里有的文件，
// 外面新配上的要靠它），收起的、排除的、选中的留着。框里的条件已经改了（还没搜）就是普通的重搜。
func (a *App) Research() {
	s := &a.Search
	if !s.Done || s.Running {
		return
	}
	if EntryOf(s.Query) != EntryOf(s.resQ) {
		a.SearchNow()
		return
	}
	a.search(s.resQ.Limit, true)
}

// search 按框里的条件、最多 limit 处（0 是默认）搜；more 时是"再读"，搜完不清收起、排除，不回到顶上。
func (a *App) search(limit int, more bool) {
	s := &a.Search
	s.Query.Limit = limit
	s.tick++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.stop()
	s.stopFollow()
	s.gen++
	if s.Query.Text == "" {
		s.Running, s.Done, s.Res, s.Rows, s.Sel = false, false, search.Result{}, nil, -1
		s.ver++
		return
	}
	q, roots, gen := s.Query, a.searchRoots(), s.gen
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.Running = cancel, true
	up := a.Update
	go func() {
		res := search.Search(ctx, roots, q)
		up(func() {
			if a.Search.gen != gen {
				return
			}
			a.Search.stop()
			a.Search.Running = false
			if more && res.Err == nil {
				a.Search.showMore(res, q)
			} else {
				a.Search.show(res, q)
			}
			if a.Search.toEditor == gen {
				a.Search.toEditor = 0
				a.OpenSearchEditor()
			}
		})
		// 搜一次要读上万个文件，读完的内容都成了垃圾；应用闲下来就没有分配去触发 GC，要等 Go 每 2 分钟一次的
		// 强制 GC 才收，这期间几十 MB 一直算在 footprint 里（量"全开"时多出约 50 MB）。搜完了就收一次、还给系统。
		if ctx.Err() == nil {
			debug.FreeOSMemory()
		}
	}()
}

// Searches 是开始过几次搜索（测试用它等"又搜了一次"）。
func (s *SearchState) Searches() int { return s.gen }

// stop 取消正在搜的那一次。
func (s *SearchState) stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// searchRoots 是要搜的仓库（rootsIn 搜索页的范围）；范围那个仓库已经不在列表里了就去掉范围、搜全部。
func (a *App) searchRoots() []search.Root {
	roots, gone := a.rootsIn(a.Search.Scope)
	if gone {
		a.Toast = i18n.Tf("%s 已经不在列表里了，改成搜全部", a.Search.Scope.Repo)
		a.Search.Scope = nil
	}
	return roots
}

// rootsIn 是在范围 sc（nil 是全部）里要搜的仓库：文件树读过的直接用它的文件列表，没读过的由搜索自己跑 git ls-files。
// 有范围时只搜那一个仓库（的那个目录）；那个仓库已经不在列表里了 gone 为真，给的是全部。
func (a *App) rootsIn(sc *SearchScope) (roots []search.Root, gone bool) {
	repos := a.Repos
	if sc != nil {
		if r := a.RepoFor(sc.Root); r != nil {
			repos = []*Repo{r}
		} else {
			sc, gone = nil, true
		}
	}
	roots = make([]search.Root, 0, len(repos))
	for _, r := range repos {
		root := search.Root{Name: r.Name, Dir: r.Root, Plain: r.Plain}
		if sc != nil {
			root.Sub = sc.Dir
		}
		if r.Files.Loaded && r.Files.Err == nil {
			root.Files = r.Files.Paths // 已经去掉了 files.exclude 的
			root.Exclude = r.exclude   // 被忽略的另列，也要去掉
		} else {
			root.Exclude, _ = repoExclude(r.Root, a.excludeBase()) // 读一个小文件
		}
		roots = append(roots, root)
	}
	return roots, gone
}

// show 放进一次搜完的结果（按 q 搜的）：文件都展开，排除的、选中清掉，列表回到顶上。
func (s *SearchState) show(res search.Result, q search.Query) {
	s.Res, s.Done, s.Sel = res, true, -1
	s.resQ, s.matcher = q, nil
	if res.Err == nil {
		s.matcher, _ = search.Compile(q)
	}
	s.collapsed, s.dismissed = nil, nil
	s.rebuild()
	s.List.ScrollTo(0, ui.Start)
}

// showMore 放进"再读"搜完的结果：前面的几处和原来一样，收起的、排除的、选中的、滚动都留着。
func (s *SearchState) showMore(res search.Result, q search.Query) {
	s.Res, s.Done = res, true
	s.resQ = q
	s.matcher, _ = search.Compile(q)
	s.rebuild()
}

// rebuild 按结果、收起的文件和排除的几处重算列表的行：全排除了的文件不显示。
func (s *SearchState) rebuild() {
	s.ver++
	s.Rows = s.Rows[:0]
	ms := s.Res.Matches
	for i := 0; i < len(ms); {
		j := i + 1
		for j < len(ms) && ms[j].Root == ms[i].Root && ms[j].Path == ms[i].Path {
			j++
		}
		var keep []int
		for k := i; k < j; k++ {
			if !s.dismissed[keyOf(ms[k])] {
				keep = append(keep, k)
			}
		}
		if len(keep) > 0 {
			open := !s.collapsed[fileKey{ms[i].Root, ms[i].Path}]
			s.Rows = append(s.Rows, SearchRow{File: true, Match: keep[0], Count: len(keep), Open: open})
			if open {
				for _, k := range keep {
					s.Rows = append(s.Rows, SearchRow{Match: k})
				}
			}
		}
		i = j
	}
	if s.Sel >= len(s.Rows) {
		s.Sel = len(s.Rows) - 1
	}
}

// Count 是没排除的结果有几处、几个文件。
func (s *SearchState) Count() (matches, files int) {
	ms := s.Res.Matches
	for i := 0; i < len(ms); {
		_, j := s.fileRange(i)
		n := 0
		for k := i; k < j; k++ {
			if !s.dismissed[keyOf(ms[k])] {
				n++
			}
		}
		if n > 0 {
			files++
		}
		matches += n
		i = j
	}
	return matches, files
}

// fileRange 是第 i 处所在的文件在 Res.Matches 里的下标范围 [lo, hi)。
func (s *SearchState) fileRange(i int) (lo, hi int) {
	ms := s.Res.Matches
	lo, hi = i, i+1
	for lo > 0 && ms[lo-1].Root == ms[i].Root && ms[lo-1].Path == ms[i].Path {
		lo--
	}
	for hi < len(ms) && ms[hi].Root == ms[i].Root && ms[hi].Path == ms[i].Path {
		hi++
	}
	return lo, hi
}

// DismissMatch 把第 i 处排除：不显示、全部替换时跳过（结果列表的 ×、⌫）。
func (a *App) DismissMatch(i int) {
	s := &a.Search
	if i < 0 || i >= len(s.Res.Matches) {
		return
	}
	s.dismiss(i, i+1)
}

// DismissFile 把第 i 处所在的文件整个排除。
func (a *App) DismissFile(i int) {
	s := &a.Search
	if i < 0 || i >= len(s.Res.Matches) {
		return
	}
	s.dismiss(s.fileRange(i))
}

func (s *SearchState) dismiss(lo, hi int) {
	if s.dismissed == nil {
		s.dismissed = map[matchKey]bool{}
	}
	for k := lo; k < hi; k++ {
		s.dismissed[keyOf(s.Res.Matches[k])] = true
	}
	s.rebuild()
}

// Dismissed 是排除了几处。
func (s *SearchState) Dismissed() int {
	n := 0
	for _, m := range s.Res.Matches {
		if s.dismissed[keyOf(m)] {
			n++
		}
	}
	return n
}

// ToggleSearchFile 收起 / 展开第 row 行（文件行，或某一处所在的文件），选中留在那个文件上。
func (a *App) ToggleSearchFile(row int) {
	s := &a.Search
	if row < 0 || row >= len(s.Rows) {
		return
	}
	m := s.Res.Matches[s.Rows[row].Match]
	k := fileKey{m.Root, m.Path}
	if s.collapsed == nil {
		s.collapsed = map[fileKey]bool{}
	}
	s.collapsed[k] = !s.collapsed[k]
	s.rebuild()
	for i, r := range s.Rows {
		if r.File && s.Res.Matches[r.Match].Root == m.Root && s.Res.Matches[r.Match].Path == m.Path {
			if s.Sel >= 0 {
				s.Sel = i
			}
			return
		}
	}
}

// FoldSearchFile 是结果列表里的 ← →：← 收起选中行所在的文件，→ 展开选中的文件行。
func (a *App) FoldSearchFile(open bool) {
	s := &a.Search
	if s.Sel < 0 || s.Sel >= len(s.Rows) {
		return
	}
	row := s.Rows[s.Sel]
	isOpen := !row.File || row.Open
	if open != isOpen {
		a.ToggleSearchFile(s.Sel)
	}
}

// OnSearchRow 处理结果列表的选择：点文件行收起 / 展开它；选中一处就在预览标签里打开并选中那一处，
// pin（双击、回车）时固定标签。clicked 是鼠标点的（不是方向键走过来的）。
func (a *App) OnSearchRow(clicked, pin bool) {
	s := &a.Search
	if s.Sel < 0 || s.Sel >= len(s.Rows) {
		return
	}
	row := s.Rows[s.Sel]
	switch {
	case row.File && (clicked || pin):
		a.ToggleSearchFile(s.Sel)
	case !row.File:
		a.OpenSearchMatch(row.Match, pin)
	}
}

// OpenSearchMatch 打开第 i 处结果所在的文件并选中它。
func (a *App) OpenSearchMatch(i int, pin bool) {
	if i < 0 || i >= len(a.Search.Res.Matches) {
		return
	}
	m := a.Search.Res.Matches[i]
	r := a.RepoFor(m.Root)
	if r == nil {
		a.Toast = i18n.Tf("%s 已经不在列表里了", m.Repo)
		return
	}
	a.noteSearch(a.Search.resQ)
	a.OpenAt(r, m.Path, docs.Jump{Line: m.Line, Col: m.Col, Len: m.BufLen()}, pin)
}

// OpenSearchMatchSide 在侧边的组里打开第 i 处所在的文件并选中它（结果的右键菜单）。
func (a *App) OpenSearchMatchSide(i int) {
	if i < 0 || i >= len(a.Search.Res.Matches) {
		return
	}
	m := a.Search.Res.Matches[i]
	r := a.RepoFor(m.Root)
	if r == nil {
		a.Toast = i18n.Tf("%s 已经不在列表里了", m.Repo)
		return
	}
	a.noteSearch(a.Search.resQ)
	if d := a.OpenFileSide(r, m.Path); d != nil {
		a.jumpIn(d, docs.Jump{Line: m.Line, Col: m.Col, Len: m.BufLen()}, false)
	}
}

// SearchIn 只在仓库 r 的目录 dir（空是整个仓库）里搜：打开搜索页、显示范围胶囊，框里有字就马上搜（树右键"在此搜索"）。
func (a *App) SearchIn(r *Repo, dir string) {
	a.Search.Scope = &SearchScope{Root: r.Root, Repo: r.Name, Dir: dir}
	a.ShowSidebar(workspace.ModeSearch)
	a.Search.Focus = true
	a.SearchNow()
}

// ClearScope 去掉范围，重新搜全部（胶囊上的 ×）。
func (a *App) ClearScope() {
	if a.Search.Scope == nil {
		return
	}
	a.Search.Scope = nil
	a.SearchNow()
}

// EntryOf 是 q 记进最近搜索的那一条（开关、包含 / 排除都记，"再读 5000"的上限不记）。
func EntryOf(q search.Query) workspace.SearchEntry {
	return workspace.SearchEntry{Text: q.Text, Case: q.CaseSensitive, Word: q.WholeWord, Regexp: q.Regexp,
		Include: q.Include, Exclude: q.Exclude, Ignored: q.Ignored, Big: q.Big}
}

// queryOf 是最近搜索里的一条要搜的条件。
func queryOf(e workspace.SearchEntry) search.Query {
	return search.Query{Text: e.Text, CaseSensitive: e.Case, WholeWord: e.Word, Regexp: e.Regexp,
		Include: e.Include, Exclude: e.Exclude, Ignored: e.Ignored, Big: e.Big}
}

// noteSearch 把 q 记到最近搜索最前面（字、开关、包含 / 排除都一样才算重复，最多 MaxSearches 条）：搜索框里回车、打开了一处结果时。
func (a *App) noteSearch(q search.Query) {
	e := EntryOf(q)
	hist := a.St.SearchHistory
	if e.Text == "" || len(hist) > 0 && hist[0] == e {
		return
	}
	rest := slices.DeleteFunc(slices.Clone(hist), func(h workspace.SearchEntry) bool { return h == e })
	a.St.SearchHistory = append([]workspace.SearchEntry{e}, rest[:min(len(rest), MaxSearches-1)]...)
	a.SaveState()
}

// useQuery 把框里换成 q（字和开关、包含 / 排除）；q 有包含 / 排除或两个"也搜"就展开那一块，好看见。
func (a *App) useQuery(q search.Query) {
	s := &a.Search
	s.Query = q
	if q.Include != "" || q.Exclude != "" || q.Ignored || q.Big {
		s.Globs = true
	}
}

// SearchSubmitted 是搜索框里的回车：立刻搜，记进最近搜索。
func (a *App) SearchSubmitted() {
	a.Search.histPos = 0
	a.noteSearch(a.Search.Query)
	a.SearchNow()
}

// SearchEdited 是用户在搜索框里改了字：不再是翻最近搜索。
func (a *App) SearchEdited() {
	a.Search.histPos = 0
	a.SearchChanged()
}

// StepSearch 是搜索框里的 ↑（dir 1，更早的）/ ↓（dir -1，更近的）：翻最近搜索（连开关、包含 / 排除一起换），每翻一条就搜。
// 翻过最近那条再按 ↓ 回到开始翻之前框里的字和开关；没在翻时 ↓ 返回 false（交给"进结果列表"）。
func (a *App) StepSearch(dir int) bool {
	s := &a.Search
	hist := a.St.SearchHistory
	if s.histPos == 0 {
		if dir < 0 || len(hist) == 0 {
			return false
		}
		s.draft, s.draftGl = s.Query, s.Globs
	}
	next := s.histPos + dir
	if next == 1 && dir > 0 && hist[0] == EntryOf(s.draft) && len(hist) > 1 {
		next = 2 // 框里就是最近那条：↑ 直接到上一条
	}
	if next > len(hist) {
		return true
	}
	s.histPos = next
	if next == 0 {
		s.Query, s.Globs = s.draft, s.draftGl
	} else {
		a.useQuery(queryOf(hist[next-1]))
	}
	a.SearchNow()
	return true
}

// UseSearch 用最近搜索里的第 i 条搜（搜索框空着时下面那张表）：开关、包含 / 排除也换成它的。
func (a *App) UseSearch(i int) {
	if i < 0 || i >= len(a.St.SearchHistory) {
		return
	}
	a.useQuery(queryOf(a.St.SearchHistory[i]))
	a.Search.histPos = 0
	a.Search.Focus = true
	a.noteSearch(a.Search.Query)
	a.SearchNow()
}

// ForgetSearch 从最近搜索里去掉第 i 条（行上的 ×）。
func (a *App) ForgetSearch(i int) {
	if i < 0 || i >= len(a.St.SearchHistory) {
		return
	}
	a.St.SearchHistory = slices.Delete(slices.Clone(a.St.SearchHistory), i, i+1)
	a.SaveState()
}

// ClearSearches 清掉最近搜索（表头的"清除"）。
func (a *App) ClearSearches() {
	a.St.SearchHistory = nil
	a.SaveState()
}

// SearchSummary 是结果列表上面的一行：搜索中 / 出错 / 没有结果 / N 处、M 个文件。
func (a *App) SearchSummary() (text string, failed bool) {
	s := &a.Search
	switch {
	case s.Running && !s.Done:
		return i18n.T("搜索中…"), false
	case !s.Done:
		return "", false
	case s.Res.Err != nil:
		return i18n.T(s.Res.Err.Error()), true
	}
	n, files := s.Count()
	switch {
	case len(s.Res.Matches) == 0:
		text = i18n.T("没有结果")
	case s.Res.Truncated:
		text = i18n.Tn("只显示前 %d 处，%d 个文件", s.resQ.MatchCap(), files)
	default:
		text = i18n.Tn("%d 处，%d 个文件", n, files)
	}
	if d := s.Dismissed(); d > 0 {
		text += i18n.Tf("（排除了 %d 处）", d)
	}
	if s.Scope != nil {
		text += i18n.Tf(" · 范围 %s", s.Scope.Label())
	}
	if s.Res.IgnoredCut {
		text += i18n.Tf(" · 被忽略的文件太多，每个仓库只搜了前 %d 个", git.MaxIgnoredFiles)
	}
	if s.Res.TimedOut > 0 {
		text += i18n.Tn(" · %d 个文件正则超时", s.Res.TimedOut)
	}
	if len(s.Res.Matches) > 0 {
		text += fmt.Sprintf(" · %d ms", s.Res.Elapsed.Milliseconds())
	}
	if s.Res.Advanced { // 这次的正则走的 regexp2（反向引用、环视）
		text += i18n.T(" · 高级正则")
	}
	return text, false
}
