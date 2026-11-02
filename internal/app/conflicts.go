package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是三方合并：git 合并 / 变基 / 拣选留下的冲突标记，在编辑器里按块解决（界面是 ui 的 mergeBar、
// docMenu 和状态栏）。解决一块只改缓冲区（能 ⌘Z），"标记为已解决"才保存并 git add。

// Conflicted 是文件标签 d 要不要显示冲突条：blocks 是缓冲区里的冲突块，flagged 是所属仓库的状态把它列成冲突
// （仓库外的文件没有这一项，只看标记）。两样都没有就不显示。
func (a *App) Conflicted(d *docs.Doc) (blocks []docs.ConflictBlock, flagged bool) {
	if d.Kind != docs.File || d.Buf == nil {
		return nil, false
	}
	return d.ConflictBlocks(), a.repoConflict(a.RepoOf(d), d.Rel)
}

// repoConflict 是仓库 r 的状态里 rel 是不是冲突文件（冲突排在 Changes 最前面）。
func (a *App) repoConflict(r *Repo, rel string) bool {
	if r == nil || rel == "" {
		return false
	}
	for _, ch := range r.Status.Changes {
		if ch.Kind != git.Conflict {
			return false
		}
		if ch.Path == rel {
			return true
		}
	}
	return false
}

// CurrentConflict 是 d 的光标所在（或后面第一处）冲突块的序号，没有为 -1。
func (a *App) CurrentConflict(d *docs.Doc) int {
	return docs.CurrentConflict(d.ConflictBlocks(), d.St.CursorPos().Line)
}

// ConflictAtCursor 是光标所在的冲突块的序号（含标记行）；光标不在块里为 -1。
func (a *App) ConflictAtCursor(d *docs.Doc) int {
	blocks, line := d.ConflictBlocks(), d.St.CursorPos().Line
	if i := docs.CurrentConflict(blocks, line); i >= 0 && blocks[i].Contains(line) {
		return i
	}
	return -1
}

// GoToConflict 把光标放到 d 的下一处（dir 1）或上一处（dir -1）冲突的 <<<<<<< 行，走到头绕回另一头。
func (a *App) GoToConflict(d *docs.Doc, dir int) {
	blocks := d.ConflictBlocks()
	if i := docs.NextConflict(blocks, d.St.CursorPos().Line, dir); i >= 0 {
		d.JumpTo(docs.Jump{Line: blocks[i].Start})
	}
}

// ResolveConflict 把 d 的第 i 处冲突换成 how 那样（用我的 / 用他们的 / 两个都要），光标放到换上的第一行。
// 只改缓冲区，一次 ⌘Z 撤回；保存、git add 是"标记为已解决"的事。
func (a *App) ResolveConflict(d *docs.Doc, i int, how docs.Take) {
	blocks := d.ConflictBlocks()
	if i < 0 || i >= len(blocks) || !d.Editable() {
		return
	}
	start, end, repl := docs.Resolved(d.Buf.String(), blocks[i], how)
	d.Buf.Edit(editor.Edit{Start: start, End: end, Text: repl})
	d.JumpTo(docs.Jump{Line: blocks[i].Start})
}

// ResolveCurrent 解决光标所在（或后面第一处）的冲突：冲突条的按钮和 ⌃⌥1 / 2 / 3。
func (a *App) ResolveCurrent(d *docs.Doc, how docs.Take) {
	a.ResolveConflict(d, a.CurrentConflict(d), how)
}

// ResolveAll 先问，再把 d 的每处冲突都换成 how（全部用我的 / 他们的），一次 ⌘Z 撤回全部。
func (a *App) ResolveAll(d *docs.Doc, how docs.Take) {
	n := len(d.ConflictBlocks())
	if n == 0 || !d.Editable() {
		return
	}
	title, msg, button := i18n.Tn("%d 处冲突全部用我的？", n), i18n.T("他们那边的改动会丢掉；能 ⌘Z 撤销。"), i18n.T("全部用我的")
	if how == docs.TakeTheirs {
		title, msg, button = i18n.Tn("%d 处冲突全部用他们的？", n), i18n.T("我们这边的改动会丢掉；能 ⌘Z 撤销。"), i18n.T("全部用他们的")
	}
	a.Ask(title, msg, button, func() { a.resolveAll(d, how) })
}

func (a *App) resolveAll(d *docs.Doc, how docs.Take) {
	blocks := d.ConflictBlocks()
	if len(blocks) == 0 {
		return
	}
	text := d.Buf.String()
	edits := make([]editor.Edit, len(blocks))
	for i, b := range blocks { // 偏移都按改之前的文字算，Edit 一次改完
		start, end, repl := docs.Resolved(text, b, how)
		edits[i] = editor.Edit{Start: start, End: end, Text: repl}
	}
	d.Buf.Edit(edits...)
	d.JumpTo(docs.Jump{Line: blocks[0].Start})
}

// MarkResolved 是冲突条和右键的"标记为已解决"：没保存就先保存（失败就停），再 git add。
// 缓冲区里还有冲突标记就先问一声，标记会原样进提交。
func (a *App) MarkResolved(d *docs.Doc) {
	r := a.RepoOf(d)
	if r == nil || d.Rel == "" || d.Kind != docs.File || d.Buf == nil {
		return
	}
	if d.NeedsOverwrite() {
		a.Toast = i18n.T("这个文件在磁盘上被改过了：先在上面那条里选重新读取或保留我的")
		return
	}
	mark := func() {
		if d.Modified() && a.WriteDoc(d, true) != nil {
			return
		}
		a.Stage(r, d.Rel)
	}
	if n := len(d.ConflictBlocks()); n > 0 {
		a.askMarkers(n, mark)
		return
	}
	mark()
}

// MarkResolvedPath 是 Git 栏冲突行的"标记为已解决"（行上的 ✓、右键）：文件开着就照它的标签来（先保存）；
// 没开着就看磁盘上的文件还有没有冲突标记，有就先问。
func (a *App) MarkResolvedPath(r *Repo, rel string) {
	for _, d := range a.AllDocs() {
		if d.Kind == docs.File && d.Root == r.Root && d.Rel == rel && d.Buf != nil {
			a.MarkResolved(d)
			return
		}
	}
	mark := func() { a.Stage(r, rel) }
	b, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(rel)))
	switch {
	case errors.Is(err, os.ErrNotExist): // 一边删了：git add -A 按删掉记
	case err != nil:
		a.Fail(i18n.T("标记为已解决"), err)
		return
	default:
		if n := len(docs.ParseConflicts(string(b))); n > 0 {
			a.askMarkers(n, mark)
			return
		}
	}
	mark()
}

func (a *App) askMarkers(n int, fn func()) {
	a.Ask(i18n.Tn("还有 %d 处冲突标记，标成已解决？", n), i18n.T("标记（<<<<<<< 等）会原样进提交。"), i18n.T("仍然标记"), fn)
}

// OpenConflict 是 Git 栏单击冲突行：在编辑器里打开那个文件（冲突条在上面）。工作区里没有这个文件
// （我们这边删了）时还是开 HEAD ↔ 工作区的 Diff。
func (a *App) OpenConflict(r *Repo, ch git.Change, pin bool) *docs.Doc {
	if _, err := os.Stat(filepath.Join(r.Root, filepath.FromSlash(ch.Path))); err != nil {
		return a.OpenDiff(r, ch, pin)
	}
	return a.OpenFile(r, ch.Path, pin)
}

// OpenStagesDiff 开冲突文件"两边的 diff"：索引里的 :2（我们的）↔ :3（他们的），只读。
func (a *App) OpenStagesDiff(r *Repo, rel string) *docs.Doc {
	return a.OpenDiff(r, git.Change{Kind: git.Conflict, Path: rel, Code: 'S'}, true)
}
