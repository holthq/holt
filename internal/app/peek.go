package app

import (
	"errors"
	"os"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是编辑器装订线上改动块的就地卡：点改动条（或改动块里的行号）、命令 diff.peekHunk，在这一块下面弹一张卡，
// 看 HEAD 里的旧行，还原（编辑器里直接变，⌘Z 能撤）、暂存（保存了才行，git apply --cached）、在 Diff 中打开。
// 块是 editor.DiffState{Old: d.Base, New: d.Buf} 现算的，和装订线上的改动条是同一种算法；卡画在 ui/peek.go。

// Peek 是开着的那张卡：哪个标签、第几块；Doc 为 nil 是没开。sel 是开卡 / 挪卡时编辑器的选区：
// 之后选区变了（点了编辑器别处、打字），光标停在某块开头（上 / 下一处改动）就把卡挪过去，不然关掉。
type Peek struct {
	Doc  *docs.Doc
	Hunk int
	sel  editor.Selection
}

// peekCache 记着上次算的块（两个缓冲区的版本没变就不重算）：卡开着时每帧都要。
type peekCache struct {
	d         *docs.Doc
	base, buf *editor.Buffer
	bv, nv    uint64
	hunks     []editor.DiffHunk
}

var lastPeek peekCache

// DocHunks 是文件标签 d 和 HEAD（历史版本：父提交）比的改动块；没有对比基准（新文件、仓库外、大文件、NoBase）是 nil。
func DocHunks(d *docs.Doc) []editor.DiffHunk {
	if d == nil || d.Buf == nil || d.Base == nil || d.St.DiffBase == nil || d.Big {
		return nil
	}
	c := &lastPeek
	if c.d == d && c.base == d.Base && c.buf == d.Buf && c.bv == d.Base.Version() && c.nv == d.Buf.Version() {
		return c.hunks
	}
	ds := editor.DiffState{Old: d.Base, New: d.Buf}
	*c = peekCache{d: d, base: d.Base, buf: d.Buf, bv: d.Base.Version(), nv: d.Buf.Version(), hunks: ds.Hunks()}
	return c.hunks
}

// HunkAt 是新文件第 line 行落在哪一块（-1 为不在）：块里的行，或纯删除的块标记的那一行（删在它上面；删在文件末尾的标在最后一行）。
func HunkAt(hunks []editor.DiffHunk, line, lines int) int {
	for i, h := range hunks {
		switch {
		case h.NewStart <= line && line < h.NewEnd:
			return i
		case h.NewStart == h.NewEnd && (h.NewStart == line || h.NewStart >= lines && line == lines-1):
			return i
		}
	}
	return -1
}

// hunkLine 是光标停到第 i 块时在的那一行（块的第一行；删在文件末尾的是最后一行）。
func hunkLine(h editor.DiffHunk, lines int) int {
	return max(0, min(h.NewStart, lines-1))
}

// TogglePeek 是点了装订线上第 line 行（或光标在那一行时的 diff.peekHunk）：落在一块里就开它的卡，那块的卡已经开着就关；
// 不在块里返回 false。
func (a *App) TogglePeek(d *docs.Doc, line int) bool {
	hs := DocHunks(d)
	i := HunkAt(hs, line, d.Buf.LineCount())
	if i < 0 {
		return false
	}
	if a.Peek.Doc == d && a.Peek.Hunk == i {
		a.ClosePeek()
		return true
	}
	a.Peek = Peek{Doc: d, Hunk: i, sel: d.St.Selection()}
	return true
}

// ClosePeek 关掉卡。
func (a *App) ClosePeek() { a.Peek = Peek{} }

// PeekHunk 是 d 上开着的卡的块和块数；没开（或开在别的标签上、块已经没了）ok 为 false。
func (a *App) PeekHunk(d *docs.Doc) (h editor.DiffHunk, i, n int, ok bool) {
	if a.Peek.Doc != d || d == nil {
		return h, 0, 0, false
	}
	hs := DocHunks(d)
	if len(hs) == 0 {
		return h, 0, 0, false
	}
	i = min(a.Peek.Hunk, len(hs)-1)
	return hs[i], i, len(hs), true
}

// SyncPeek 是界面每帧画 d 之前：块没了就关；选区变了，光标停在某块开头（上 / 下一处改动、卡上的 ↑ ↓）卡跟过去，不然关掉。
func (a *App) SyncPeek(d *docs.Doc) {
	if a.Peek.Doc != d {
		return
	}
	hs := DocHunks(d)
	if len(hs) == 0 {
		a.ClosePeek()
		return
	}
	a.Peek.Hunk = min(a.Peek.Hunk, len(hs)-1)
	sel := d.St.Selection()
	if sel == a.Peek.sel {
		return
	}
	a.Peek.sel = sel
	if sel.Anchor == sel.Head {
		p := d.Buf.Pos(sel.Head)
		n := d.Buf.LineCount()
		for i, h := range hs {
			if p.Col == 0 && p.Line == hunkLine(h, n) {
				a.Peek.Hunk = i
				return
			}
		}
	}
	a.ClosePeek()
}

// PeekStep 把卡挪到上一块（dir < 0）或下一块，光标到那块开头、滚到视图中间；到头了不动。
func (a *App) PeekStep(dir int) {
	d := a.Peek.Doc
	_, i, n, ok := a.PeekHunk(d)
	if !ok || i+dir < 0 || i+dir >= n {
		return
	}
	h := DocHunks(d)[i+dir]
	d.St.GoTo(editor.Pos{Line: hunkLine(h, d.Buf.LineCount())})
	a.Peek.Hunk, a.Peek.sel = i+dir, d.St.Selection()
}

// CanStepPeek 是卡上的 ↑（dir < 0）↓ 能不能点。
func (a *App) CanStepPeek(dir int) bool {
	_, i, n, ok := a.PeekHunk(a.Peek.Doc)
	return ok && i+dir >= 0 && i+dir < n
}

// PeekEditable 是卡上的“还原”能不能点：可编辑的文件标签。
func PeekEditable(d *docs.Doc) bool {
	return d != nil && d.Kind == docs.File && !d.St.ReadOnly
}

// PeekRevert 把卡上那块换回 HEAD 里的旧行（DiffState.Revert：编辑器里直接改，进撤销，不问）；
// 还剩别的块，卡留在原来的序号上（就是下面那块），没有了就关。
func (a *App) PeekRevert() {
	d := a.Peek.Doc
	_, i, _, ok := a.PeekHunk(d)
	if !ok || !PeekEditable(d) {
		return
	}
	ds := editor.DiffState{Old: d.Base, New: d.Buf}
	ds.Revert(i)
	a.Peek.sel = d.St.Selection()
	a.SyncPeek(d)
}

// PeekStage 暂存卡上那块：文件得先保存（磁盘上就是编辑器里这份）。补丁按 HEAD 里的原样字节和磁盘上的算（行号同编辑器里的块），
// 交给 git apply --cached；暂存区里这个文件已经有别的改动、和这一块挨着时 git 可能不接，报出来。
func (a *App) PeekStage() {
	d := a.Peek.Doc
	h, _, _, ok := a.PeekHunk(d)
	r := a.RepoOf(d)
	if !ok || r == nil || d.Kind != docs.File || d.Modified() || d.Rel == "" {
		return
	}
	label := i18n.T("暂存这一块")
	up := a.Update
	go func() {
		head := git.Blob(r.Root, "HEAD", d.Rel)
		disk, err := os.ReadFile(d.Abs)
		up(func() {
			if err != nil {
				a.Fail(label, err)
				return
			}
			to, ok := git.ReplaceLines(head, h.OldStart, h.OldEnd, disk, h.NewStart, h.NewEnd)
			patch := git.HunkPatch(d.Rel, head, to)
			if !ok || patch == "" {
				a.Fail(label, errors.New(i18n.T("这一块已经和磁盘上的对不上了，等 diff 重读后再试")))
				return
			}
			a.runOp(r, label, patch, i18n.Tf("（%s 第 %d 行起的一块）", d.Rel, h.NewStart+1), nil, "apply", "--cached", "-")
		})
	}()
}

// PeekOpenDiff 打开这个文件未提交改动的 Diff 标签（未暂存的优先），滚到卡上这块（新文件里同一行所在的那块：
// Diff 标签比的是暂存区，块的序号不一定一样）。
func (a *App) PeekOpenDiff() {
	d := a.Peek.Doc
	h, _, _, ok := a.PeekHunk(d)
	r := a.RepoOf(d)
	if !ok || r == nil || d.Rel == "" {
		return
	}
	var found *git.Change
	for i, ch := range r.Status.Changes {
		if ch.Path == d.Rel && (found == nil || ch.Kind == git.Unstaged) {
			found = &r.Status.Changes[i]
		}
	}
	if found == nil {
		a.Toast = i18n.Tf("%s 没有未提交的改动", d.Rel)
		return
	}
	a.ClosePeek()
	dd := a.OpenDiff(r, *found, true)
	dd.HunkLine, dd.ToHunk = h.NewStart+1, true
}

// HunkFromLine 是 Diff 里新文件第 line 行所在的那块，不在块里是它后面的第一块（都没有是最后一块）。
func HunkFromLine(st *editor.DiffState, line int) int {
	hs := st.Hunks()
	for i, h := range hs {
		if h.NewEnd > line || h.NewStart >= line {
			return i
		}
	}
	return max(len(hs)-1, 0)
}
