package app

import (
	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是右栏历史里的"比较两个提交"：在一个提交上右键"标记为比较起点"，再在同一个仓库的另一个提交上右键"与 … 比较"，
// "当前仓库"视图里"未提交的改动"下面多一段，列出两个提交之间改了的文件（只读，git diff-tree 起点 终点），点文件开 Diff。
// 起点和比较都记在仓库的历史上（换仓库不清），只有胶囊的 × 清掉比较。

// Compare 是正在比较的两个提交。
type Compare struct {
	From, To git.Commit   // 早的在前
	Files    []git.Change // InCommit、Commit 是 To、Parent 是 From（Range 为真）
	Loading  bool
	Open     bool // 文件行展开着（默认展开）
}

// MarkCompareFrom 把 cm 标成比较起点（提交右键"标记为比较起点"）。
func (a *App) MarkCompareFrom(r *Repo, cm git.Commit) {
	r.Hist.CompareFrom = &cm
	a.Toast = i18n.Tf("起点：%s，再在另一个提交上右键“与 %s 比较”", cm.Short, cm.Short)
}

// CompareCommits 比较同一个仓库的两个提交：时间早的当起点（同一时间的保持点的顺序），在后台读它们之间改了的文件。
// 右栏换到"当前仓库"视图（比较段只在那里），列表滚到比较段。
func (a *App) CompareCommits(r *Repo, from, to git.Commit) {
	if to.Date.Before(from.Date) {
		from, to = to, from
	}
	a.UseRepo(r)
	a.SetHistAll(false)
	h := &r.Hist
	cp := &Compare{From: from, To: to, Loading: true, Open: true}
	keepHistSel(r, func() { h.Compare = cp })
	a.HistList.ScrollTo(1, ui.Start)
	up := a.Update
	go func() {
		fs, err := git.DiffFiles(r.ctx, r.Root, from.Hash, to.Hash)
		up(func() {
			if h.Compare != cp || r.gone() { // 读的时候清掉了、又比了别的
				return
			}
			if err != nil {
				keepHistSel(r, func() { h.Compare = nil })
				a.Fail(i18n.T("比较提交失败"), err)
				return
			}
			keepHistSel(r, func() { cp.Files, cp.Loading = fs, false })
		})
	}()
}

// ToggleCompare 展开 / 收起比较段的文件（点它的标题、回车）。
func (a *App) ToggleCompare(r *Repo) {
	if cp := r.Hist.Compare; cp != nil {
		keepHistSel(r, func() { cp.Open = !cp.Open })
	}
}

// ClearCompare 不再比较（胶囊的 ×、比较段的右键）。起点留着，还能和别的提交比。
func (a *App) ClearCompare(r *Repo) {
	keepHistSel(r, func() { r.Hist.Compare = nil })
}

// keepHistSel 在 fn 改了比较段的行之后让选中还在原来那一行上：选的是提交或它的文件就跟过去；
// 选的是比较段里的文件而它没了（收起、换了一次比较）就落在比较段的标题上，比较段整个没了就不选。
func keepHistSel(r *Repo, fn func()) {
	old := r.Hist.Compare
	ci, file := histKey(r)
	fn()
	switch {
	case ci >= 0:
		setHistSel(r, ci, file)
	case ci == CompareRow && r.Hist.Compare == nil:
		r.Hist.Sel = -1
	case ci == CompareRow:
		if r.Hist.Compare != old {
			file = -1
		}
		setHistSel(r, ci, file)
	}
}
