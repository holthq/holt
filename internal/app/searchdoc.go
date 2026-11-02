package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/search"
)

// 这个文件是搜索编辑器：把一次搜索的结果开成一个只读的纯文本标签（docs.SearchDoc），每处带前后几行上下文，
// 能整页滚着看、用编辑器自己的 ⌘F 再找。双击 / 回车 / ⌘点一行打开那个文件到那一行；顶栏"重新搜索"按标签记的条件和范围重搜、重写。
// 上下文行是开的时候照搜索读文件的规矩重读磁盘取的（search.ReadText），只在后台读，读完就丢。

// SearchContexts 是顶栏"上下文 N 行"菜单的几档。
var SearchContexts = []int{0, 1, 2, 3, 5}

// searchLineMax 是搜索编辑器里一行最多多少字节：再长的（压缩过的 JS）截成匹配附近的一段，前后加 …。
const searchLineMax = 400

// searchSrc 是搜索编辑器标签记着的：按什么条件、在哪搜的，结果（只有没排除的几处）。
type searchSrc struct {
	q         search.Query
	scope     *SearchScope // nil 是全部仓库
	res       search.Result
	dismissed int // 开的时候在搜索页排除了几处（表头写上；重搜之后清零）
	running   bool
	cancel    context.CancelFunc
	gen       int
}

func (src *searchSrc) stop() {
	if src.cancel != nil {
		src.cancel()
		src.cancel = nil
	}
	src.gen++
	src.running = false
}

// searchSrcOf 是搜索编辑器标签记着的条件；不是搜索编辑器的为 nil。
func searchSrcOf(d *docs.Doc) *searchSrc {
	if d == nil || d.Kind != docs.SearchDoc {
		return nil
	}
	src, _ := d.SearchSrc.(*searchSrc)
	return src
}

// SearchDocRunning 是搜索编辑器标签在重搜。
func SearchDocRunning(d *docs.Doc) bool {
	src := searchSrcOf(d)
	return src != nil && src.running
}

// CanOpenSearchEditor 是搜索页有能开成搜索编辑器的结果（或者正在搜，搜完就开）。
func (s *SearchState) CanOpenSearchEditor() bool {
	return s.Running && !s.Done || s.Done && s.Res.Err == nil && len(s.Rows) > 0
}

// OpenSearchEditor 把搜索页现在显示的结果（没排除的几处）开成搜索编辑器（摘要行的图标、表头右键、search.openInEditor）：
// 同一个查询的标签已经开着就重写它。还没有结果、正在搜就等搜完再开。
func (a *App) OpenSearchEditor() {
	s := &a.Search
	switch {
	case s.Running && !s.Done:
		s.toEditor = s.gen
	case !s.CanOpenSearchEditor():
		a.Toast = i18n.T("没有能打开的搜索结果")
	default:
		a.openSearchEditor()
	}
}

// SearchToEditor 是搜索框里的 ⌥⏎：按框里的条件搜（框里的就是显示着的结果就不重搜），搜完开成搜索编辑器。
func (a *App) SearchToEditor() {
	s := &a.Search
	switch {
	case s.Query.Text == "":
		return
	case s.Running && s.timer == nil: // 正在搜的就是框里的
		s.toEditor = s.gen
	case s.timer != nil || !s.Done || EntryOf(s.Query) != EntryOf(s.resQ):
		a.SearchSubmitted()
		s.toEditor = s.gen
	default:
		a.noteSearch(s.Query)
		a.OpenSearchEditor()
	}
}

// openSearchEditor 照搜索页的结果开（或重写）搜索编辑器标签，键盘给它。
func (a *App) openSearchEditor() {
	s := &a.Search
	if s.Res.Err != nil || len(s.Rows) == 0 {
		a.Toast = i18n.T("没有能打开的搜索结果")
		return
	}
	res := s.Res
	res.Matches = make([]search.Match, 0, len(s.Res.Matches))
	for _, m := range s.Res.Matches {
		if !s.dismissed[keyOf(m)] {
			res.Matches = append(res.Matches, m)
		}
	}
	src := &searchSrc{q: s.resQ, res: res, dismissed: s.Dismissed()}
	if sc := s.Scope; sc != nil {
		cp := *sc
		src.scope = &cp
	}
	d := docs.NewSearchDoc(searchDocTitle(src.q))
	if old := a.reuse(d.Key(), true); old != nil {
		d = old
		if o := searchSrcOf(d); o != nil {
			o.stop()
		}
	} else {
		a.addDoc(d, true)
	}
	d.SearchSrc, d.SearchLines = src, a.Search.EditorLines
	a.fillSearchDoc(d)
	a.FocusDoc = true
}

// searchDocTitle 是搜索编辑器标签的名字：搜索: foo（换行画成 ⏎，太长截到 40 个字）。
func searchDocTitle(q search.Query) string {
	return i18n.Tf("搜索: %s", shortQuery(q.Text, 40))
}

// shortQuery 是表头、标签名里显示的搜索词：换行画成 ⏎，超过 n 个字截掉、加 …。
func shortQuery(text string, n int) string {
	text = strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\t", " ").Replace(text)
	if utf8.RuneCountInString(text) > n {
		text = string([]rune(text)[:n]) + "…"
	}
	return text
}

// fillSearchDoc 在后台按标签记着的结果和上下文行数重读文件、生成文字，回来放进标签（第一次之前是"读取中"）。
func (a *App) fillSearchDoc(d *docs.Doc) {
	src := searchSrcOf(d)
	if src == nil {
		return
	}
	if d.Buf == nil {
		d.Loading = true
	}
	d.Gen++
	gen, res, q, dismissed, n := d.Gen, src.res, src.q, src.dismissed, d.SearchLines
	scope := ""
	if src.scope != nil {
		scope = src.scope.Label()
	}
	up := a.Update
	go func() {
		text, hits := SearchEditorText(res, q, scope, dismissed, n, diskLines(q))
		up(func() {
			if d.Gen == gen {
				d.SetSearchText(text, hits)
			}
		})
	}()
}

// diskLines 照搜索读文件的规矩（大小上限按 q）读一个文件、切成行（不带换行，末尾的换行后面不算一行）：读不了、二进制的为 nil。
func diskLines(q search.Query) func(root, rel string) []string {
	return func(root, rel string) []string {
		data, ok := search.ReadText(filepath.Join(root, filepath.FromSlash(rel)), q.MaxSize())
		if !ok {
			return nil
		}
		ls := strings.Split(string(data), "\n")
		if len(ls) > 1 && ls[len(ls)-1] == "" { // 末尾的换行后面不算一行
			ls = ls[:len(ls)-1]
		}
		for i, l := range ls {
			ls[i] = strings.TrimSuffix(l, "\r")
		}
		return ls
	}
}

// ResearchSearchDoc 是搜索编辑器顶栏的"重新搜索"：按标签记的条件和范围在后台重搜，搜完重写标签（光标留在原来那行）。
// 范围那个仓库已经不在列表里了就改成搜全部。
func (a *App) ResearchSearchDoc(d *docs.Doc) {
	src := searchSrcOf(d)
	if src == nil || src.running {
		return
	}
	roots, gone := a.rootsIn(src.scope)
	if gone {
		a.Toast = i18n.Tf("%s 已经不在列表里了，改成搜全部", src.scope.Repo)
		src.scope = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	src.gen++
	src.running, src.cancel = true, cancel
	gen, q, n := src.gen, src.q, d.SearchLines
	scope := ""
	if src.scope != nil {
		scope = src.scope.Label()
	}
	up := a.Update
	go func() {
		res := search.Search(ctx, roots, q)
		var text string
		var hits []docs.SearchHit
		if ctx.Err() == nil {
			text, hits = SearchEditorText(res, q, scope, 0, n, diskLines(q))
		}
		up(func() {
			if src.gen != gen {
				return
			}
			src.running, src.cancel = false, nil
			src.res, src.dismissed = res, 0
			d.SetSearchText(text, hits)
		})
		if ctx.Err() == nil {
			debug.FreeOSMemory()
		}
	}()
}

// SetSearchLines 是顶栏"上下文 N 行"菜单：记在窗口上（之后开的搜索编辑器也用它，不存设置），重写这个标签。
func (a *App) SetSearchLines(d *docs.Doc, n int) {
	a.Search.EditorLines = n
	if d.SearchLines == n {
		return
	}
	d.SearchLines = n
	a.fillSearchDoc(d)
}

// OpenSearchHit 打开搜索编辑器里 offset 所在那一行指向的文件（预览标签）：匹配行选中那一处，上下文行、文件名那行只跳到那一行。
func (a *App) OpenSearchHit(d *docs.Doc, offset int) {
	h, m, ok := d.HitAt(offset)
	if !ok {
		return
	}
	r := a.RepoFor(h.Root)
	if r == nil {
		a.Toast = i18n.Tf("%s 已经不在列表里了", filepath.Base(h.Root))
		return
	}
	j := docs.Jump{Line: h.Line}
	if m != nil {
		j.Col, j.Len = m.Col, m.Len
	}
	a.OpenAt(r, h.Path, j, false)
}

// SearchEditorText 生成搜索编辑器的文字和每一行指向的地方（下标是行号）。res 里只有要显示的几处（按仓库、路径、行排好），
// scope 是范围的名字（空是全部），dismissed 是排除了几处，ctx 是每处前后几行上下文，lines 取一个文件的所有行（取不到为 nil，
// 那就只用结果里的预览、不带上下文）：
//
//	# 12 处，3 个文件 · “foo” · 区分大小写 · 正则 · 包含 *.go · 范围 adv/internal
//
//	adv/internal/a.go:
//	   9  上下文
//	  10: 匹配的一行
//	  ⋯
//	  42: 又一处
//
// 重叠、挨着的上下文合并；跨行的匹配只标它开始的那一行（到行尾）；行号的宽度每个文件按它最大的那个。
func SearchEditorText(res search.Result, q search.Query, scope string, dismissed, ctx int, lines func(root, rel string) []string) (string, []docs.SearchHit) {
	w := &searchWriter{}
	w.line(searchHeader(res, q, scope, dismissed), docs.SearchHit{})
	ms := res.Matches
	for i := 0; i < len(ms); {
		j := i + 1
		for j < len(ms) && ms[j].Root == ms[i].Root && ms[j].Path == ms[i].Path {
			j++
		}
		w.line("", docs.SearchHit{})
		w.file(ms[i:j], lines(ms[i].Root, ms[i].Path), ctx)
		i = j
	}
	return strings.Join(w.out, "\n"), w.hits
}

// searchHeader 是搜索编辑器的第一行：处数、文件数，搜索词和开关、包含 / 排除、范围。
func searchHeader(res search.Result, q search.Query, scope string, dismissed int) string {
	var b strings.Builder
	b.WriteString("# ")
	files := 0
	for i, m := range res.Matches {
		if i == 0 || m.Root != res.Matches[i-1].Root || m.Path != res.Matches[i-1].Path {
			files++
		}
	}
	switch {
	case res.Err != nil:
		b.WriteString(i18n.T(res.Err.Error()))
	case len(res.Matches) == 0:
		b.WriteString(i18n.T("没有结果"))
	case res.Truncated:
		b.WriteString(i18n.Tn("只显示前 %d 处，%d 个文件", q.MatchCap(), files))
	default:
		b.WriteString(i18n.Tn("%d 处，%d 个文件", len(res.Matches), files))
	}
	if dismissed > 0 {
		b.WriteString(i18n.Tf("（排除了 %d 处）", dismissed))
	}
	parts := []string{"“" + shortQuery(q.Text, 200) + "”"}
	if q.CaseSensitive {
		parts = append(parts, i18n.T("区分大小写"))
	}
	if q.WholeWord {
		parts = append(parts, i18n.T("全字"))
	}
	if q.Regexp {
		parts = append(parts, i18n.T("正则"))
	}
	if q.Include != "" {
		parts = append(parts, i18n.Tf("包含 %s", q.Include))
	}
	if q.Exclude != "" {
		parts = append(parts, i18n.Tf("排除 %s", q.Exclude))
	}
	if q.Ignored {
		parts = append(parts, i18n.T("含被忽略的文件"))
	}
	if q.Big {
		parts = append(parts, i18n.T("含大文件"))
	}
	if scope != "" {
		parts = append(parts, i18n.Tf("范围 %s", scope))
	}
	b.WriteString(" · " + strings.Join(parts, " · "))
	if res.TimedOut > 0 {
		b.WriteString(i18n.Tn(" · %d 个文件正则超时", res.TimedOut))
	}
	if res.Advanced {
		b.WriteString(i18n.T(" · 高级正则"))
	}
	return b.String()
}

// searchWriter 一行一行攒搜索编辑器的文字，记着每行的起点（标出匹配的位置要用）。
type searchWriter struct {
	out  []string
	hits []docs.SearchHit
	off  int // 下一行在文字里的起点
}

func (w *searchWriter) line(s string, h docs.SearchHit) {
	w.out = append(w.out, s)
	w.hits = append(w.hits, h)
	w.off += len(s) + 1
}

// file 写一个文件：文件名那行，下面每段连着的行（匹配行和上下文），段之间一行 ⋯。ms 是这个文件的几处，ls 是它的所有行（可以为 nil）。
func (w *searchWriter) file(ms []search.Match, ls []string, ctx int) {
	m0 := ms[0]
	w.line(m0.Repo+"/"+m0.Path+":", docs.SearchHit{Root: m0.Root, Path: m0.Path, Line: m0.Line})
	type span struct{ lo, hi int }
	var spans []span
	for _, m := range ms {
		lo, hi := m.Line, m.Line
		if m.Line < len(ls) {
			lo, hi = max(0, m.Line-ctx), min(len(ls)-1, m.Line+ctx)
		}
		if n := len(spans); n > 0 && lo <= spans[n-1].hi+1 {
			spans[n-1].hi = max(spans[n-1].hi, hi)
			continue
		}
		spans = append(spans, span{lo, hi})
	}
	width := len(strconv.Itoa(spans[len(spans)-1].hi + 1))
	k := 0 // ms 里下一处
	for si, sp := range spans {
		if si > 0 {
			w.line("  ⋯", docs.SearchHit{})
		}
		for ln := sp.lo; ln <= sp.hi; ln++ {
			for k < len(ms) && ms[k].Line < ln {
				k++
			}
			e := k
			for e < len(ms) && ms[e].Line == ln {
				e++
			}
			h := docs.SearchHit{Root: m0.Root, Path: m0.Path, Line: ln}
			if e == k { // 上下文行
				l, _ := lineAt(ls, ln)
				text, _ := clipLine(l, 0)
				w.line(fmt.Sprintf("  %*d  %s", width, ln+1, text), h)
				continue
			}
			prefix := fmt.Sprintf("  %*d: ", width, ln+1)
			l, ok := lineAt(ls, ln)
			text, marks := matchLine(ms[k:e], l, ok)
			for i := range marks {
				marks[i].Start += w.off + len(prefix)
				marks[i].End += w.off + len(prefix)
			}
			h.Marks = marks
			w.line(prefix+text, h)
		}
	}
}

func lineAt(ls []string, i int) (string, bool) {
	if i < len(ls) {
		return ls[i], true
	}
	return "", false
}

// matchLine 是有匹配的那一行显示成什么、每一处标在哪（相对这一行的文字）：读到了那一行就用它，
// 没读到（文件变了、读不了）或者对不上就用第一处的预览。
func matchLine(ms []search.Match, line string, ok bool) (string, []docs.SearchMark) {
	valid := ok && utf8.ValidString(line)
	for _, m := range ms {
		if m.Col > len(line) {
			valid = false
		}
	}
	marks := make([]docs.SearchMark, len(ms))
	if !valid {
		m := ms[0]
		text, from := clipLine(m.Preview, m.PreviewCol)
		s := min(len(text), max(0, m.PreviewCol-from))
		for i, m := range ms {
			marks[i] = docs.SearchMark{Start: s, End: min(len(text), s+m.Len), Col: m.Col, Len: m.BufLen()}
		}
		return text, marks
	}
	text, from := clipLine(line, ms[0].Col)
	for i, m := range ms {
		s := min(len(text), max(0, m.Col-from))
		e := min(len(text), max(0, min(m.Col+m.Span, len(line))-from))
		marks[i] = docs.SearchMark{Start: s, End: e, Col: m.Col, Len: m.BufLen()}
	}
	return text, marks
}

// clipLine 把太长的一行截成 searchLineMax 字节左右：从 col 前 60 字节起（在字符边界上），截掉的地方画 …。
// from 是截出来的这段在原来那行里的起点减去开头 … 的长度（原来的位置减 from 就是在截出来的文字里的位置）。
func clipLine(line string, col int) (text string, from int) {
	line = strings.ToValidUTF8(line, "�")
	if len(line) <= searchLineMax {
		return line, 0
	}
	lo := max(0, min(col, len(line))-60)
	for lo > 0 && !utf8.RuneStart(line[lo]) {
		lo--
	}
	hi := min(len(line), lo+searchLineMax)
	for hi < len(line) && !utf8.RuneStart(line[hi]) {
		hi++
	}
	text, from = line[lo:hi], lo
	if lo > 0 {
		text, from = "…"+text, lo-len("…")
	}
	if hi < len(line) {
		text += "…"
	}
	return text, from
}
