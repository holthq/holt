package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/search"
)

// 替换预览：替换之前，把一个文件按当前的搜索结果和替换词换好的样子和现在的文字放进一个只读的 diff 标签
// （docs.ReplacePreview），不写盘、不记会话。标签记着按哪一版结果生成的：重搜、排除了几处、改了替换词之后顶栏说它旧了，
// 替换按钮灰掉，点“重新生成”再看。

// PreviewReplace 打开第 i 处所在文件的替换预览（结果右键“预览替换…”）；at 时滚到这一处所在的那块改动。
func (a *App) PreviewReplace(i int, at bool) {
	s := &a.Search
	if !s.CanReplace() || i < 0 || i >= len(s.Res.Matches) {
		return
	}
	m := s.Res.Matches[i]
	if a.RepoFor(m.Root) == nil {
		return
	}
	d := docs.NewReplacePreview(m.Root, m.Path)
	if old := a.reuse(d.Key(), true); old != nil {
		d = old
	} else {
		a.addDoc(d, true)
	}
	a.fillPreview(d)
	if at && d.DiffSt != nil {
		d.ReplaceHunk = hunkAt(d.DiffSt, m.Line) + 1
	}
	a.FocusDoc = true
}

// PreviewReplaceFirst 是替换行“全部替换”右边的“预览”：开第一个有（没排除的）结果的文件的预览。
func (a *App) PreviewReplaceFirst() {
	s := &a.Search
	if !s.CanReplace() {
		return
	}
	if idx := s.kept(0, len(s.Res.Matches)); len(idx) > 0 {
		a.PreviewReplace(idx[0], false)
	}
}

// RegenPreview 是替换预览顶栏的“重新生成”：按现在的结果和替换词再生成一遍。
func (a *App) RegenPreview(d *docs.Doc) {
	if a.Search.CanReplace() {
		a.fillPreview(d)
	}
}

// PreviewStale 是替换预览 d 不是按现在的搜索结果和替换词生成的（或者正在重搜）：顶栏说它旧了，不能从它替换。
func (a *App) PreviewStale(d *docs.Doc) bool {
	s := &a.Search
	return !s.CanReplace() || d.ReplaceVer != s.ver || d.ReplaceText != s.Replace
}

// PreviewCount 是替换预览 d 那个文件里现在没排除的有几处。
func (a *App) PreviewCount(d *docs.Doc) int { return len(a.Search.fileKept(d.Root, d.Rel)) }

// ReplaceFromPreview 是替换预览顶栏的“替换这个文件（N 处）”：和结果右键一样换掉这个文件里没排除的全部（不问），关掉预览。
// 预览旧了不换。
func (a *App) ReplaceFromPreview(d *docs.Doc) {
	if a.PreviewStale(d) {
		return
	}
	idx := a.Search.fileKept(d.Root, d.Rel)
	if len(idx) == 0 {
		return
	}
	a.ReplaceMatches(idx)
	a.RemoveDoc(d)
}

// fillPreview 按现在的结果和替换词（重新）生成 d 的两边：旧的一边是现在的文字（和 replaceIn 认的一样：开着的用缓冲区，
// 没开的、或磁盘上被别处改过的读盘解码），新的一边是 Plan 核对过还在原位的几处换好的。读不了的在标签里写一句。
func (a *App) fillPreview(d *docs.Doc) {
	s := &a.Search
	old, err := a.textNow(d.Root, d.Rel)
	if err != nil {
		d.Gen++
		d.Loading, d.Info, d.DiffSt = false, err.Error(), nil
		d.ReplaceVer, d.ReplaceText = s.ver, s.Replace
		return
	}
	var ms []search.Match
	for _, i := range s.fileKept(d.Root, d.Rel) {
		ms = append(ms, s.Res.Matches[i])
	}
	edits, _ := s.matcher.Plan(old, ms, s.Replace)
	d.SetReplacePreview(old, search.Apply(old, edits), s.ver, s.Replace, a.St.FullDiff)
}

// textNow 是仓库 root 里 rel 现在的文字（替换按它换）：开着、编辑器里是最新的用缓冲区，否则读盘按认出的编码解码。
func (a *App) textNow(root, rel string) ([]byte, error) {
	if d := a.openDocAt(root, rel); d != nil && (d.Modified() || !d.NeedsOverwrite()) {
		return []byte(d.Buf.String()), nil
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, errors.New(i18n.Tf("读取失败：%s", err.Error()))
	}
	text, enc, _ := docs.Decode(raw)
	if enc == docs.Binary {
		return nil, errors.New(i18n.T("二进制文件，不显示 diff"))
	}
	return []byte(text), nil
}

// fileKept 是结果里 root/rel 这个文件没排除的那几处的下标。
func (s *SearchState) fileKept(root, rel string) []int {
	for i, m := range s.Res.Matches {
		if m.Root == root && m.Path == rel {
			return s.kept(s.fileRange(i))
		}
	}
	return nil
}

// hunkAt 是 diff 里旧的一边第 line 行（从 0 起）所在、或它后面最近的那块改动；都在它前面就是最后一块。
func hunkAt(st *editor.DiffState, line int) int {
	hs := st.Hunks()
	for i, h := range hs {
		if h.OldEnd > line || h.OldStart >= line {
			return i
		}
	}
	return max(len(hs)-1, 0)
}
