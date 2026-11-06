package app

import (
	"os"
	"slices"
	"strings"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/fuzzy"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/outline"
)

// 这个文件是快速打开里的"名字@符号"：文件按名字打分取第一名（Sublime 的做法），第一行是那个文件，
// 下面是它大纲里模糊匹配"符号"的项，回车打开文件跳到那个符号。开着的文件用标签的大纲；没开的在后台读盘、
// 解析，结果按 abs + 大小 + 修改时间记在 olCache（最近用过的 maxOutlineCache 个）。

// maxOutlineCache 是记住几个没开着的文件的大纲。
const maxOutlineCache = 32

// olKey 认出磁盘上一个文件的一个版本：路径、大小、修改时间（纳秒）。
type olKey struct {
	abs   string
	size  int64
	mtime int64
}

// olCache 是没开着的文件的大纲（"名字@符号"读的），最近用过的在 keys 末尾；pending 是正在后台读的。
type olCache struct {
	keys    []olKey
	trees   map[olKey]*outline.Tree
	pending map[olKey]bool
}

// get 是 k 的大纲，有的话挪到最近用过。
func (c *olCache) get(k olKey) (*outline.Tree, bool) {
	t, ok := c.trees[k]
	if ok {
		i := slices.Index(c.keys, k)
		c.keys = append(slices.Delete(c.keys, i, i+1), k)
	}
	return t, ok
}

// put 记下 k 的大纲，多了丢掉最久没用的。
func (c *olCache) put(k olKey, t *outline.Tree) {
	if c.trees == nil {
		c.trees = map[olKey]*outline.Tree{}
	}
	if _, ok := c.trees[k]; ok {
		i := slices.Index(c.keys, k)
		c.keys = slices.Delete(c.keys, i, i+1)
	}
	c.trees[k] = t
	c.keys = append(c.keys, k)
	if len(c.keys) > maxOutlineCache {
		delete(c.trees, c.keys[0])
		c.keys = slices.Delete(c.keys, 0, 1)
	}
}

// splitAt 拆开"名字@符号"（按最后一个 @）；开头就是 @ 的（当前文件的符号）、没有 @ 的 ok 为假。
func splitAt(s string) (name, sym string, ok bool) {
	i := strings.LastIndexByte(s, '@')
	if i <= 0 {
		return s, "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}

// fileSymbolItems 是"名字@符号"（text 是整段输入）：名字打分第一的文件和它大纲里匹配 sym 的符号。
// 第一名没有大纲（语言不认）时整段当文件名找（名字里本来就带 @ 的，如 logo@2x.png）；有大纲、一个符号都不匹配时
// 提示一句，下面也是整段当文件名找的结果。
func (a *App) fileSymbolItems(text, name, sym string) ([]PaletteItem, string) {
	whole := func() ([]PaletteItem, string) {
		n, j, at := splitLine(text)
		return a.fileItems(n, j, at)
	}
	files, note := a.fileItems(name, docs.Jump{}, false)
	if len(files) == 0 {
		if items, n := whole(); len(items) > 0 {
			return items, n
		}
		return nil, note
	}
	top := files[0]
	top.Recent = false
	title := top.Text[top.Name:]
	if !outline.Supported(top.path) {
		return whole()
	}
	tree, loading := a.outlineOf(top.repo, top.path)
	head := []PaletteItem{top}
	switch {
	case loading:
		return head, i18n.T("正在读大纲…")
	case tree == nil:
		return head, i18n.Tf("%s 没有大纲", title)
	case len(tree.Flat) == 0:
		return head, i18n.Tf("%s 里没有符号", title)
	}
	syms := symbolsOf(tree.Flat, sym)
	if len(syms) == 0 {
		items, _ := whole()
		return items, i18n.Tf("%s 里没有匹配 %s 的符号", title, sym)
	}
	for i := range syms {
		syms[i].repo, syms[i].path, syms[i].Under = top.repo, top.path, true
	}
	if len(syms) > maxPaletteItems {
		syms = syms[:maxPaletteItems]
	}
	return append(head, syms...), ""
}

// symbolsOf 是大纲 flat 里模糊匹配 q 的符号（匹配"父 › 子"，名字里命中的靠前），q 为空时按文件里的顺序；
// "@"（当前文件）和"名字@符号"共用。
func symbolsOf(flat []outline.Flat, q string) []PaletteItem {
	pat := fuzzy.New(q)
	type hit struct {
		item  PaletteItem
		score int
	}
	var hs []hit
	for _, f := range flat {
		name := len(f.Path) - len(f.Name)
		sc, hits, ok := pat.Match(f.Path, name)
		if !ok {
			continue
		}
		hs = append(hs, hit{PaletteItem{Kind: ItemSymbol, Text: f.Path, Name: name, Hits: hits, Detail: i18n.Tf("第 %d 行", f.Line+1), Sym: f.Kind,
			jump: docs.Jump{Line: f.Line}, at: true}, sc})
	}
	if !pat.Empty() {
		slices.SortStableFunc(hs, func(x, y hit) int { return y.score - x.score })
	}
	items := make([]PaletteItem, len(hs))
	for i, h := range hs {
		items[i] = h.item
	}
	return items
}

// outlineOf 是仓库 r 里 rel 这个文件的大纲：开着、读好了的用标签的（改了没存的也算）；没开的看 olCache，
// 没有就在后台读盘、解析（loading 为真，读完让面板重算）。超过 outline.MaxBytes、大文件、读不了的为 nil。
func (a *App) outlineOf(r *Repo, rel string) (tree *outline.Tree, loading bool) {
	for _, d := range a.AllDocs() {
		if d.Kind == docs.File && d.Root == r.Root && d.Rel == rel && d.Buf != nil {
			return d.Outline(), false
		}
	}
	abs := docs.NewFile(r.Root, rel).Abs
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() || fi.Size() > outline.MaxBytes {
		return nil, false
	}
	k := olKey{abs, fi.Size(), fi.ModTime().UnixNano()}
	c := &a.olCache
	if t, ok := c.get(k); ok {
		return t, false
	}
	if c.pending[k] {
		return nil, true
	}
	if c.pending == nil {
		c.pending = map[olKey]bool{}
	}
	c.pending[k] = true
	up := a.Update
	go func() {
		var t *outline.Tree
		if data, err := os.ReadFile(abs); err == nil && len(data) <= outline.MaxBytes {
			if text, enc, _ := docs.Decode(data); enc != docs.Binary {
				t = outline.Build(outline.Parse(rel, text))
			}
		}
		up(func() {
			delete(c.pending, k)
			c.put(k, t)
			if a.Palette.Open {
				a.Palette.sig = "\x00" // 下一帧重算
			}
		})
	}()
	return nil, true
}
