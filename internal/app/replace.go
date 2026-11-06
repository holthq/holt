package app

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/search"
)

// 这个文件是搜索页的替换（⇧⌘H）：结果的预览里画出换成什么，可以排除某几处，逐处 / 逐个文件 / 全部替换。
// 开着的文件改编辑器的缓冲区（本来没改过的顺手存盘，改过没存的只改不存，⌘Z 一步撤销），没开的直接读盘、换好写回；
// 搜索的位置是解码后的文字里的（= 缓冲区里的），不是 UTF-8 的文件也在文字上换、按原来的编码写回。全部替换先问。每一处换之前都核对它还在原位（internal/search 的 Plan），搜完之后改过的不动。
// 换完在内存里重搜改过的文件，结果列表跟着变。

// ShowReplace 打开搜索页并显示替换那一行（⇧⌘H）：搜索框里有字就把焦点给替换框。
func (a *App) ShowReplace() {
	a.ShowSearch()
	s := &a.Search
	s.Replacing = true
	if s.Query.Text != "" {
		s.Focus, s.FocusRepl = false, true
	}
}

// CanReplace 是现在能不能替换：搜完了、没出错、不在搜。
func (s *SearchState) CanReplace() bool {
	return s.Done && !s.Running && s.Res.Err == nil && s.matcher != nil
}

// Preview 是第 i 处换成 Replace 之后的字（正则展开 $1，预览里对不上时原样给 Replace），换行画成 ⏎。
func (s *SearchState) Preview(i int) string {
	text := s.Replace
	if s.matcher != nil && i >= 0 && i < len(s.Res.Matches) {
		text, _ = s.matcher.Expand(s.Res.Matches[i], s.Replace)
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "⏎"), "\n", "⏎")
}

// ReplaceOne 换掉第 i 处（行上的按钮、⌘↩、右键菜单），不问。
func (a *App) ReplaceOne(i int) { a.ReplaceMatches([]int{i}) }

// ReplaceFile 换掉第 i 处所在的文件里没排除的全部，不问。
func (a *App) ReplaceFile(i int) {
	s := &a.Search
	if i < 0 || i >= len(s.Res.Matches) {
		return
	}
	lo, hi := s.fileRange(i)
	a.ReplaceMatches(s.kept(lo, hi))
}

// FileCount 是第 i 处所在的文件里没排除的有几处。
func (s *SearchState) FileCount(i int) int {
	if i < 0 || i >= len(s.Res.Matches) {
		return 0
	}
	return len(s.kept(s.fileRange(i)))
}

// kept 是 [lo, hi) 里没排除的那几处的下标。
func (s *SearchState) kept(lo, hi int) []int {
	var idx []int
	for k := lo; k < hi; k++ {
		if !s.dismissed[keyOf(s.Res.Matches[k])] {
			idx = append(idx, k)
		}
	}
	return idx
}

// ReplaceAll 把没排除的全部换掉（"全部替换"按钮、替换框里回车）：先问，说清几处、几个文件、几个仓库，没开着的直接写盘。
func (a *App) ReplaceAll() {
	s := &a.Search
	if !s.CanReplace() {
		return
	}
	idx := s.kept(0, len(s.Res.Matches))
	if len(idx) == 0 {
		return
	}
	files, repos := map[fileKey]bool{}, map[string]bool{}
	for _, i := range idx {
		m := s.Res.Matches[i]
		files[fileKey{m.Root, m.Path}], repos[m.Root] = true, true
	}
	title := i18n.Tn("把 %d 处换成 “%s”？", len(idx), strings.ReplaceAll(s.Replace, "\n", "⏎"))
	if s.Replace == "" {
		title = i18n.Tn("删掉这 %d 处？", len(idx))
	}
	ver := s.ver
	a.Ask(title, i18n.Tn("%d 个文件，%d 个仓库。没开着的文件直接写进磁盘：换完可以点摘要行的“撤销上次替换”写回，或用 Git 栏的 diff 看、不要了就丢弃。", len(files), len(repos)),
		i18n.T("全部替换"), func() {
			if a.Search.ver != ver {
				a.Toast = i18n.T("搜索结果变了，没有替换；再看一眼再换")
				return
			}
			a.ReplaceMatches(idx)
		})
}

// replaced 是一个文件替换的结果。
type replaced struct {
	repo        *Repo
	path        string
	done, stale int
	fresh       []search.Match // 换完重搜的结果；nil 且 keep 时留着原来的
	keep        bool           // 没读到内容（只读、出错）：结果列表里照旧
	dirty       bool           // 只改了编辑器里的缓冲区（本来就改过没存）
	readonly    bool
	enc         docs.Encoding // readonly 是因为编码（只读的编码、编不回原样的老编码）：报告里说是哪种
	lossy       bool          // enc 是编不回原样（Doc.Lossy）
	wrote       bool          // 写了磁盘
	buf         bool          // 改的是编辑器的缓冲区（撤销是编辑器里的 ⌘Z）
	undo        *undoFile     // 没开着、直接写了盘：撤销上次替换按它写回
	err         error
}

// ReplaceMatches 把 Res.Matches 里下标为 idx 的几处（排除了的跳过）换成 Replace，按文件一个个来，一个出错接着换别的。
func (a *App) ReplaceMatches(idx []int) {
	s := &a.Search
	if !s.CanReplace() {
		return
	}
	type group struct {
		root, path string
		ms         []search.Match
	}
	var groups []*group
	byFile := map[fileKey]*group{}
	for _, i := range idx {
		if i < 0 || i >= len(s.Res.Matches) || s.dismissed[keyOf(s.Res.Matches[i])] {
			continue
		}
		m := s.Res.Matches[i]
		k := fileKey{m.Root, m.Path}
		g := byFile[k]
		if g == nil {
			g = &group{root: m.Root, path: m.Path}
			byFile[k] = g
			groups = append(groups, g)
		}
		g.ms = append(g.ms, m)
	}
	if len(groups) == 0 {
		return
	}
	var results []replaced
	fresh := map[fileKey][]search.Match{}
	for _, g := range groups {
		res := a.replaceIn(g.root, g.path, g.ms)
		results = append(results, res)
		if !res.keep {
			fresh[fileKey{g.root, g.path}] = res.fresh
		}
	}
	s.splice(fresh)
	big := s.noteUndo(results)
	a.reportReplace(results)
	if big {
		a.Toast += i18n.Tf("；这次太大（原文件合计超过 %d MB），不能撤销", MaxUndoReplace>>20)
	}
}

// replaceIn 换掉一个文件里的 ms：开着、能编辑的改缓冲区，否则读盘、换好写回；换完在内存里重搜这个文件。
func (a *App) replaceIn(root, rel string, ms []search.Match) replaced {
	s := &a.Search
	res := replaced{repo: a.RepoFor(root), path: rel}
	if res.repo == nil {
		res.keep, res.err = true, errors.New(i18n.Tf("%s 已经不在列表里了", ms[0].Repo))
		return res
	}
	var (
		data, out []byte
		edits     []search.Edit
	)
	d := a.openDocAt(root, rel)
	switch {
	case d != nil && !d.Editable():
		res.keep, res.readonly = true, true
		if !d.Enc.Writable() || d.Lossy || d.Broken {
			res.enc, res.lossy = d.Enc, d.Lossy
		}
		return res
	case d != nil && docs.Unencodable(s.Replace, d.Enc) > 0:
		res.keep, res.err = true, errors.New(i18n.Tn("替换成的文字有 %d 个字 %s 写不了", docs.Unencodable(s.Replace, d.Enc), d.Enc.Label()))
		return res
	case d != nil && (d.Modified() || !d.NeedsOverwrite()):
		// 编辑器里的就是最新的：改缓冲区（一步撤销）；本来没改过的顺手存盘（Write 按 d.Enc 编回），改过没存的只改不存
		data = []byte(d.Buf.String())
		edits, res.stale = s.matcher.Plan(data, ms, s.Replace)
		if len(edits) > 0 {
			clean := !d.Modified()
			res.buf = true
			d.Buf.Edit(bufEdits(edits)...)
			if clean {
				res.err = d.Write()
				res.wrote = res.err == nil
			}
			res.dirty = !clean || res.err != nil
		}
	default:
		// 没开着（或开着没改、但磁盘上已经被别的程序改了，编辑器里那份是旧的）：直接改磁盘
		abs := filepath.Join(root, filepath.FromSlash(rel))
		fi, err := os.Stat(abs)
		if err != nil {
			res.keep, res.err = true, err
			return res
		}
		if !fi.Mode().IsRegular() || fi.Size() > s.resQ.MaxSize() {
			res.stale = len(ms)
			return res
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			res.keep, res.err = true, err
			return res
		}
		// 和打开时一样认编码（老编码编回去比对过）：在解出来的文字上换，按原来的编码写回（BOM 补回）；写不回的编码不动
		text, enc, editable := docs.Decode(raw)
		if enc == docs.Binary {
			res.stale = len(ms)
			return res
		}
		if !editable {
			res.keep, res.readonly, res.enc, res.lossy = true, true, enc, enc.Writable() // 能写的编码还不能编辑 = 编不回原样
			return res
		}
		if n := docs.Unencodable(s.Replace, enc); n > 0 {
			res.keep, res.err = true, errors.New(i18n.Tn("替换成的文字有 %d 个字 %s 写不了", n, enc.Label()))
			return res
		}
		data = []byte(text)
		edits, res.stale = s.matcher.Plan(data, ms, s.Replace)
		if len(edits) > 0 {
			out = search.Apply(data, edits)
			b := docs.Encode(string(out), enc)
			if b == nil {
				res.keep, res.err = true, errors.New(i18n.Tf("编不回 %s", enc.Label()))
				return res
			}
			if err := os.WriteFile(abs, b, fi.Mode().Perm()); err != nil {
				res.keep, res.err = true, err
				return res
			}
			res.wrote = true
			res.undo = &undoFile{root: root, rel: rel, before: raw, afterSum: sha256.Sum256(b), mode: fi.Mode().Perm()}
		}
	}
	res.done = len(edits)
	if out == nil {
		out = search.Apply(data, edits)
	}
	s.moveDismissed(root, rel, data, out, edits)
	res.fresh = s.matcher.Find(out)
	for k := range res.fresh {
		res.fresh[k].Repo, res.fresh[k].Root, res.fresh[k].Path = res.repo.Name, root, rel
	}
	return res
}

// openDocAt 是所有组里开着的、读好了的这个文件的标签。
func (a *App) openDocAt(root, rel string) *docs.Doc {
	for _, d := range a.AllDocs() {
		if d.Kind == docs.File && d.Root == root && d.Rel == rel && d.Buf != nil {
			return d
		}
	}
	return nil
}

func bufEdits(edits []search.Edit) []editor.Edit {
	out := make([]editor.Edit, len(edits))
	for i, e := range edits {
		out[i] = editor.Edit{Start: e.Start, End: e.End, Text: e.Text}
	}
	return out
}

// moveDismissed 让这个文件里排除了的几处跟着改动挪位置（同一行前面换长了、多了行），重搜之后还是排除着。
func (s *SearchState) moveDismissed(root, rel string, data, out []byte, edits []search.Edit) {
	if len(s.dismissed) == 0 || len(edits) == 0 {
		return
	}
	var moved []matchKey
	for k := range s.dismissed {
		if k.root != root || k.path != rel {
			continue
		}
		delete(s.dismissed, k)
		if line, col, ok := search.Shift(data, out, edits, k.line, k.col); ok {
			moved = append(moved, matchKey{root, rel, line, col})
		}
	}
	for _, k := range moved {
		s.dismissed[k] = true
	}
}

// splice 把重搜的几个文件的结果换进 Res.Matches（顺序不变，没有结果了的文件去掉），重算文件数和列表的行。
// 选中留在原来的位置（换掉一处后就是同一个文件的下一处，文件换完了是下一个文件）。
func (s *SearchState) splice(fresh map[fileKey][]search.Match) {
	ms := s.Res.Matches
	out := make([]search.Match, 0, len(ms))
	files := 0
	for i := 0; i < len(ms); {
		_, j := s.fileRange(i)
		group := ms[i:j]
		if f, ok := fresh[fileKey{ms[i].Root, ms[i].Path}]; ok {
			group = f
		}
		if len(group) > 0 {
			out = append(out, group...)
			files++
		}
		i = j
	}
	s.Res.Matches, s.Res.Files = out, files
	s.rebuild()
}

// reportReplace 说换了多少（提示），记进输出栏（每个仓库一条），刷新写过盘的仓库的 Git 状态；有出错的弹出来。
func (a *App) reportReplace(results []replaced) {
	s := &a.Search
	var done, files, stale, dirty, readonly int
	var fails, encs []string // encs：因为编码只读的文件各是哪种（去重，按出现的顺序）
	encRO := 0
	type repoLog struct {
		n, files int
		lines    []string
		failed   bool
	}
	logs := map[*Repo]*repoLog{}
	var order []*Repo
	for _, r := range results {
		done, stale = done+r.done, stale+r.stale
		if r.done > 0 {
			files++
		}
		if r.dirty && r.err == nil {
			dirty++
		}
		switch {
		case r.readonly && r.enc != "":
			encRO++
			l := r.enc.Label()
			if r.lossy {
				l = i18n.Tf("%s 编不回原样", l)
			}
			if !slices.Contains(encs, l) {
				encs = append(encs, l)
			}
		case r.readonly:
			readonly++
		}
		if r.repo == nil {
			fails = append(fails, fmt.Sprintf("%s：%v", r.path, r.err))
			continue
		}
		l := logs[r.repo]
		if l == nil {
			l = &repoLog{}
			logs[r.repo] = l
			order = append(order, r.repo)
		}
		switch {
		case r.err != nil:
			fails = append(fails, fmt.Sprintf("%s/%s：%v", r.repo.Name, r.path, r.err))
			l.lines, l.failed = append(l.lines, fmt.Sprintf("%s：%v", r.path, r.err)), true
		case r.done > 0:
			l.n, l.files = l.n+r.done, l.files+1
			l.lines = append(l.lines, i18n.Tf("%s：%d 处", r.path, r.done))
		}
		if r.wrote {
			a.RefreshRepo(r.repo, WhyFileOp)
		}
	}
	for _, r := range order {
		if l := logs[r]; len(l.lines) > 0 {
			a.Log(r, i18n.Tn("替换 “%s” → “%s”：%d 处 / %d 个文件", s.resQ.Text, s.Replace, l.n, l.files), strings.Join(l.lines, "\n"), l.failed)
		}
	}
	switch {
	case done > 0:
		a.Toast = i18n.Tn("替换了 %d 处，%d 个文件", done, files)
	case stale > 0:
		a.Toast = i18n.Tn("这 %d 处在搜完后变了，没动", stale)
		stale = 0
	case encRO > 0:
		a.Toast = i18n.Tn("%d 个文件因为编码只读（%s），没换", encRO, i18n.List(encs))
		encRO = 0
	case readonly > 0:
		a.Toast = i18n.T("只读的文件没动")
		readonly = 0
	default:
		a.Toast = ""
	}
	if stale > 0 {
		a.Toast += i18n.Tn("；%d 处在搜完后变了，没动", stale)
	}
	if encRO > 0 {
		a.Toast += i18n.Tn("；%d 个文件因为编码只读（%s），没换", encRO, i18n.List(encs))
	}
	if readonly > 0 {
		a.Toast += i18n.Tn("；%d 个文件只读，没动", readonly)
	}
	if dirty > 0 {
		a.Toast += i18n.Tn("；%d 个文件在编辑器里改了，还没保存", dirty)
	}
	if len(fails) > 0 {
		slices.Sort(fails)
		a.Fail(i18n.T("替换失败"), errors.New(strings.Join(fails, "\n")))
	}
}
