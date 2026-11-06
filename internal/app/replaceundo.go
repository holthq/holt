package app

import (
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/search"
)

// 这个文件是撤销上次替换：替换时没开着、直接写了盘的文件记下替换前的字节，"撤销上次替换"问过之后
// 把还是替换完那个样子的写回去（替换之后又被改过的不动）。开着的文件改的是缓冲区，在编辑器里 ⌘Z。
// 只记最近一次替换，在这个窗口里（不存进 state.json）。

// MaxUndoReplace 是一次替换最多记多少字节的原文件，再多这次就不能撤销。
const MaxUndoReplace = 20 << 20

// replaceUndo 是一次替换能撤销的部分。
type replaceUndo struct {
	at    time.Time
	files []undoFile
	open  int // 这次改的是编辑器缓冲区的文件数（撤销时提醒在编辑器里 ⌘Z）
}

// undoFile 是一个直接写了盘的文件：替换前的字节、写进去的字节的摘要、权限。
type undoFile struct {
	root, rel string
	before    []byte
	afterSum  [sha256.Size]byte
	mode      os.FileMode
}

// noteUndo 在一次替换之后记下能撤销的：换了东西的替换盖掉上一条（只改了缓冲区的清掉），什么都没换的不算一次。
// 原文件合计超过 MaxUndoReplace 的也清掉，big 为 true（报告里说）。
func (s *SearchState) noteUndo(results []replaced) (big bool) {
	u := &replaceUndo{at: time.Now()}
	done, size := 0, 0
	for _, r := range results {
		done += r.done
		switch {
		case r.undo != nil:
			u.files = append(u.files, *r.undo)
			size += len(r.undo.before)
		case r.buf && r.done > 0:
			u.open++
		}
	}
	switch {
	case done == 0:
	case len(u.files) == 0:
		s.lastReplace = nil
	case size > MaxUndoReplace:
		s.lastReplace = nil
		return true
	default:
		s.lastReplace = u
	}
	return false
}

// CanUndoReplace 是有能撤销的替换（摘要行的"撤销上次替换"、命令面板）。
func (s *SearchState) CanUndoReplace() bool { return s.lastReplace != nil }

// UndoReplaceTip 是"撤销上次替换"的提示：哪一次、几个文件。
func (s *SearchState) UndoReplaceTip() string {
	u := s.lastReplace
	if u == nil {
		return ""
	}
	return i18n.Tn("把 %s 那次替换直接写进磁盘的 %d 个文件写回替换前的内容（先问）", u.at.Format("15:04"), len(u.files))
}

// UndoReplace 问过之后撤销上次替换：直接写了盘的文件写回替换前的内容。
func (a *App) UndoReplace() {
	u := a.Search.lastReplace
	if u == nil {
		return
	}
	msg := i18n.Tn("把 %d 个文件写回替换前的内容", len(u.files))
	if u.open > 0 {
		msg += i18n.Tn("（开着的 %d 个文件请在编辑器里 ⌘Z）", u.open)
	}
	msg += i18n.T("。替换之后又改过的文件不动。")
	a.Ask(i18n.T("撤销上次替换？"), msg, i18n.T("撤销"), func() {
		if a.Search.lastReplace != u {
			a.Toast = i18n.T("问的时候又替换了一次，没有撤销")
			return
		}
		a.undoReplace(u)
	})
}

// undoReplace 写回 u 里还是替换完那个样子的文件，刷新这些仓库，结果里重搜这些文件（不在结果里了的插回去）。
func (a *App) undoReplace(u *replaceUndo) {
	s := &a.Search
	s.lastReplace = nil
	type repoLog struct {
		n      int
		lines  []string
		failed bool
	}
	logs := map[*Repo]*repoLog{}
	var order []*Repo
	var restored, changed int
	var fails []string
	fresh := map[fileKey][]search.Match{}
	find := s.Done && s.matcher != nil && s.Res.Err == nil
	var buf []byte
	for _, f := range u.files {
		r := a.RepoFor(f.root)
		if r == nil {
			changed++
			continue
		}
		l := logs[r]
		if l == nil {
			l = &repoLog{}
			logs[r] = l
			order = append(order, r)
		}
		abs := filepath.Join(f.root, filepath.FromSlash(f.rel))
		cur, err := os.ReadFile(abs)
		switch {
		case err != nil || sha256.Sum256(cur) != f.afterSum:
			changed++
			l.lines = append(l.lines, i18n.Tf("%s：替换之后改过了，没动", f.rel))
			continue
		case a.openDocModified(f.root, f.rel):
			changed++
			l.lines = append(l.lines, i18n.Tf("%s：在编辑器里改了还没保存，没动", f.rel))
			continue
		}
		if err := os.WriteFile(abs, f.before, f.mode); err != nil {
			fails = append(fails, fmt.Sprintf("%s/%s：%v", r.Name, f.rel, err))
			l.lines, l.failed = append(l.lines, fmt.Sprintf("%s：%v", f.rel, err)), true
			continue
		}
		restored++
		l.n++
		l.lines = append(l.lines, i18n.Tf("%s：写回了", f.rel))
		if find {
			ms := s.matcher.FindFile(abs, s.resQ.MaxSize(), &buf)
			for i := range ms {
				ms[i].Repo, ms[i].Root, ms[i].Path = r.Name, f.root, f.rel
			}
			fresh[fileKey{f.root, f.rel}] = ms
		}
	}
	for _, r := range order {
		l := logs[r]
		if l.n > 0 {
			a.RefreshRepo(r, WhyFileOp)
		}
		a.Log(r, i18n.Tn("撤销替换：写回 %d 个文件", l.n), strings.Join(l.lines, "\n"), l.failed)
	}
	if len(fresh) > 0 {
		a.spliceBack(fresh)
	}
	switch {
	case restored > 0:
		a.Toast = i18n.Tn("撤销了替换，写回 %d 个文件", restored)
		if changed > 0 {
			a.Toast += i18n.Tn("；%d 个文件替换之后改过了，没动（见输出栏）", changed)
		}
	case changed > 0:
		a.Toast = i18n.Tn("这 %d 个文件替换之后都改过了，没动（见输出栏）", changed)
	}
	if len(fails) > 0 {
		a.Fail(i18n.T("撤销替换失败"), errors.New(strings.Join(fails, "\n")))
	}
}

// openDocModified 是这个文件开着、改了还没保存（撤销替换不写它的磁盘，不然保存时冲突）。
func (a *App) openDocModified(root, rel string) bool {
	d := a.openDocAt(root, rel)
	return d != nil && d.Modified()
}

// spliceBack 同 splice，结果里没有了的文件（替换之后没有配上的、撤销后又配上了）按仓库的顺序和路径插回去，
// 和搜出来的顺序一样；排除了的几处 (line, col) 配不上了就丢掉；超过上限的截掉。
func (a *App) spliceBack(fresh map[fileKey][]search.Match) {
	s := &a.Search
	in := map[fileKey]bool{}
	for _, m := range s.Res.Matches {
		in[fileKey{m.Root, m.Path}] = true
	}
	var add []search.Match
	for k, f := range fresh {
		if !in[k] {
			add = append(add, f...)
		}
	}
	for k := range s.dismissed {
		if f, ok := fresh[fileKey{k.root, k.path}]; ok && !slices.ContainsFunc(f, func(m search.Match) bool { return m.Line == k.line && m.Col == k.col }) {
			delete(s.dismissed, k)
		}
	}
	if len(add) > 0 {
		rank := map[string]int{}
		for i, r := range a.Repos {
			rank[r.Root] = i
		}
		ms := append(slices.Clip(s.Res.Matches), add...)
		// 同一个文件里的几处本来就是按顺序的：只按仓库、路径稳定排
		slices.SortStableFunc(ms, func(x, y search.Match) int {
			return cmp.Or(cmp.Compare(rank[x.Root], rank[y.Root]), strings.Compare(x.Path, y.Path))
		})
		s.Res.Matches = ms
	}
	s.splice(fresh)
	if c := s.resQ.MatchCap(); len(s.Res.Matches) > c {
		s.Res.Matches, s.Res.Truncated = s.Res.Matches[:c], true
		s.splice(nil)
	}
}
