package app

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是 Diff 标签里一块改动（hunk）上的按钮：未暂存的 diff（索引 ↔ 工作区）能暂存、丢弃这一块，
// 已暂存的（HEAD ↔ 索引）能取消暂存这一块。补丁由 git.HunkPatch 按这个 Diff 标签读到的两边原样内容算，
// 交给 git apply：暂存是 --cached，取消暂存是 --cached -R，丢弃是 -R（改工作区，先问）。

// HunkOps 是 d 这个 Diff 标签的每一块上能点什么。未跟踪、有冲突、提交里的，以及新加 / 删掉 / 改了类型的
// 整个文件没有：那些在 Git 栏按整个文件做。
func HunkOps(d *docs.Doc) (stage, unstage, discard bool) {
	if d == nil || d.Kind != docs.Diff || d.DiffSt == nil {
		return false, false, false
	}
	ch := d.Change
	switch {
	case ch.Kind == git.Unstaged && ch.Code == 'M':
		return true, false, true
	case ch.Kind == git.Staged && (ch.Code == 'M' || ch.Code == 'R'):
		return false, true, false
	}
	return false, false, false
}

// hunkPatch 是 d 里第 h 块的补丁：stage 为真时从旧的一边套上这一块（git apply 正着打），否则从新的一边
// 去掉这一块（反着打）。ok 为 false 时这一块已经对不上（diff 刚重读过），什么也不做。
func hunkPatch(d *docs.Doc, h editor.DiffHunk, stage bool) (string, bool) {
	old, nw := d.Raw[0], d.Raw[1]
	if stage {
		to, ok := git.ReplaceLines(old, h.OldStart, h.OldEnd, nw, h.NewStart, h.NewEnd)
		return git.HunkPatch(d.Change.Path, old, to), ok
	}
	from, ok := git.ReplaceLines(nw, h.NewStart, h.NewEnd, old, h.OldStart, h.OldEnd)
	return git.HunkPatch(d.Change.Path, from, nw), ok
}

// hunkNote 是输出栏里那条 git apply 后面的说明：哪个文件、哪几行。
func hunkNote(d *docs.Doc, h editor.DiffHunk) string {
	return i18n.Tf("（%s 第 %d 行起的一块）", d.Change.Path, h.NewStart+1)
}

// applyHunk 把补丁交给 git apply；补丁空了（两边已经一样）就不跑。
func (a *App) applyHunk(d *docs.Doc, h editor.DiffHunk, label string, stage bool, args ...string) {
	r := a.RepoOf(d)
	if r == nil {
		return
	}
	patch, ok := hunkPatch(d, h, stage)
	if !ok || patch == "" {
		a.Fail(label, errors.New(i18n.T("这一块已经和磁盘上的对不上了，等 diff 重读后再试")))
		return
	}
	a.runOp(r, label, patch, hunkNote(d, h), nil, append(args, "-")...)
}

// StageHunk 暂存未暂存 diff 里的一块（git apply --cached）。
func (a *App) StageHunk(d *docs.Doc, h editor.DiffHunk) {
	a.applyHunk(d, h, i18n.T("暂存这一块"), true, "apply", "--cached")
}

// UnstageHunk 取消暂存已暂存 diff 里的一块（git apply --cached -R）。
func (a *App) UnstageHunk(d *docs.Doc, h editor.DiffHunk) {
	a.applyHunk(d, h, i18n.T("取消暂存这一块"), false, "apply", "--cached", "-R")
}

// DiscardHunk 丢弃未暂存 diff 里的一块（git apply -R，改工作区里的文件）；先问。
func (a *App) DiscardHunk(d *docs.Doc, h editor.DiffHunk) {
	n := max(h.NewEnd-h.NewStart, h.OldEnd-h.OldStart)
	lines := strings.TrimSpace(i18n.Tn("第 %d 行起的 %d 行", h.NewStart+1, n))
	a.AskDanger(i18n.Tf("丢弃 %s 里的这一块改动？", d.Change.Path),
		i18n.Tf("工作区里%s会恢复成暂存区的样子，不能撤销。", lines), i18n.T("丢弃"), func() {
			a.applyHunk(d, h, i18n.T("丢弃这一块"), false, "apply", "-R")
		})
}

// diffFiles 是 d 这个 Diff 标签所在的那串文件和它在里面的位置：未提交的改动是所有仓库的
// （按 Git 栏的顺序，收起的段也算），提交里的是那次提交改的文件（历史里展开着它时）。
func (a *App) diffFiles(d *docs.Doc) (r []*Repo, chs []git.Change, at int) {
	at = -1
	if d.Change.Kind == git.InCommit {
		x := a.RepoOf(d)
		if x == nil || x.Hist.FilesFor != d.Change.Commit {
			return nil, nil, -1
		}
		for i, ch := range x.Hist.Files {
			r, chs = append(r, x), append(chs, ch)
			if ch.Path == d.Change.Path {
				at = i
			}
		}
		return r, chs, at
	}
	add := func(x *Repo, ch git.Change) {
		if x.Root == d.Root && ch.Kind == d.Change.Kind && ch.Path == d.Change.Path {
			at = len(chs)
		}
		r, chs = append(r, x), append(chs, ch)
	}
	for _, x := range a.Repos {
		for _, ch := range x.Status.Changes {
			fs, ok := x.Status.DirFiles[ch.Path]
			if !ch.Dir || !ok {
				add(x, ch)
				continue
			}
			for _, p := range fs { // 未跟踪目录走里面列出来的文件
				add(x, git.Change{Kind: git.Untracked, Path: p, Code: '?'})
			}
		}
	}
	return r, chs, at
}

// StepFile 打开那串文件里的下一个（dir 1）或上一个（-1）的 Diff，到头了就停在原处；
// 当前文件已经不在那串里（刚提交了）时从头 / 从尾开始。返回有没有换。
func (a *App) StepFile(dir int) bool {
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff { // 比较、替换预览没有"下一个文件"
		return false
	}
	rs, chs, at := a.diffFiles(d)
	if len(chs) == 0 {
		return false
	}
	next := at + dir
	if at < 0 {
		next = 0
		if dir < 0 {
			next = len(chs) - 1
		}
	}
	if next < 0 || next >= len(chs) {
		a.Toast = i18n.T("已经是最后一个文件")
		if dir < 0 {
			a.Toast = i18n.T("已经是第一个文件")
		}
		return false
	}
	a.UseRepo(rs[next])
	a.OpenDiff(rs[next], chs[next], false) // 和单击一样是预览标签：一路按下去不会堆一排标签
	return true
}
