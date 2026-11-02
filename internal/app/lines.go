package app

import (
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 行级暂存：Diff 标签里点行号选几行（⇧ 点扩大到那一行），只暂存 / 取消暂存 / 丢弃选中的那几行。能做的和每一块上的
// 一样（HunkOps）。一块改动里旧的第 i 行和新的第 i 行配成一对（i 小于两边行数少的那个），补丁由 git.LinesPatch 算：
//
//   - 暂存（改旧的一边，git apply --cached）：选中新的一行，配了对的换掉对面那行，没配对的插在这一块旧的末尾；
//     选中旧的一行，删掉它。
//   - 取消暂存（改新的一边，git apply --cached）和丢弃（同样改新的一边，git apply，先问）：选中新的一行，配了对的
//     换回对面那行，没配对的删掉；选中旧的一行，配了对的插回它对面那行前面，没配对的插在这一块新的末尾。
//
// 取消暂存和丢弃不像每一块那样反着打（-R），而是在新的一边上正着打：补丁一样能对上，也不用再算一份"去掉选中的行"的全文。

// LinesOps 是 d 这个 Diff 标签里选中的行上能做什么：选着行时和每一块上的一样。
func LinesOps(d *docs.Doc) (stage, unstage, discard bool) {
	if d == nil || d.DiffSt == nil {
		return false, false, false
	}
	if _, ok := d.DiffSt.SelectedLines(); !ok {
		return false, false, false
	}
	return HunkOps(d)
}

// SelectedLineCount 是选了几行，没选为 0。
func SelectedLineCount(d *docs.Doc) int {
	if d == nil || d.DiffSt == nil {
		return 0
	}
	sel, ok := d.DiffSt.SelectedLines()
	if !ok {
		return 0
	}
	return sel.End - sel.Start + 1
}

// ClearSelectedLines 取消选中的行（Esc、行级操作做完以后）。
func ClearSelectedLines(d *docs.Doc) {
	if d != nil && d.DiffSt != nil {
		d.DiffSt.SetSelectedLines(editor.DiffLineRange{}, false)
	}
}

// LinesEdits 是选中的行 sel 要套的改动：toOld 为真时改旧的一边（暂存），否则改新的一边（取消暂存、丢弃）。
// old、nw 是两边原样的内容，hunks 是 Diff 视图里的每一块（行号和 git.Lines 分的一样，只是视图在以换行结尾的文件
// 后面多一行空的，那一行不算）。选中的行里不在任何一块里的（没改的上下文）什么也不做。
func LinesEdits(old, nw []byte, hunks []editor.DiffHunk, sel editor.DiffLineRange, toOld bool) []git.LineEdit {
	a, b := git.Lines(old), git.Lines(nw)
	var es []git.LineEdit
	for _, h := range hunks {
		o0, o1 := min(h.OldStart, len(a)), min(h.OldEnd, len(a))
		n0, n1 := min(h.NewStart, len(b)), min(h.NewEnd, len(b))
		pairs := min(o1-o0, n1-n0)
		lo, hi := o0, o1
		if sel.Side == editor.DiffNew {
			lo, hi = n0, n1
		}
		for l := max(lo, sel.Start); l < min(hi, sel.End+1); l++ {
			i := l - lo
			var e git.LineEdit
			switch {
			case toOld && sel.Side == editor.DiffNew && i < pairs:
				e = git.LineEdit{Start: o0 + i, End: o0 + i + 1, With: []string{b[l]}}
			case toOld && sel.Side == editor.DiffNew:
				e = git.LineEdit{Start: o1, End: o1, With: []string{b[l]}}
			case toOld:
				e = git.LineEdit{Start: l, End: l + 1}
			case sel.Side == editor.DiffNew && i < pairs:
				e = git.LineEdit{Start: l, End: l + 1, With: []string{a[o0+i]}}
			case sel.Side == editor.DiffNew:
				e = git.LineEdit{Start: l, End: l + 1}
			case i < pairs:
				e = git.LineEdit{Start: n0 + i, End: n0 + i, With: []string{a[l]}}
			default:
				e = git.LineEdit{Start: n1, End: n1, With: []string{a[l]}}
			}
			es = append(es, e)
		}
	}
	return es
}

// linesPatch 是 d 里选中的行的补丁和选了几行；补丁空了（选中的都是没改的行）时 toast 说一声。
func (a *App) linesPatch(d *docs.Doc, toOld bool) (patch string, n int) {
	sel, ok := d.DiffSt.SelectedLines()
	if !ok {
		return "", 0
	}
	base := d.Raw[1]
	if toOld {
		base = d.Raw[0]
	}
	patch = git.LinesPatch(d.Change.Path, base, LinesEdits(d.Raw[0], d.Raw[1], d.DiffSt.Hunks(), sel, toOld))
	if patch == "" {
		a.Toast = i18n.T("选中的行里没有改动")
	}
	return patch, sel.End - sel.Start + 1
}

// linesNote 是输出栏里那条 git apply 后面的说明。
func linesNote(d *docs.Doc, n int) string {
	sel, _ := d.DiffSt.SelectedLines()
	return i18n.Tn("（%s 第 %d 行起选中的 %d 行）", d.Change.Path, sel.Start+1, n)
}

// applyLines 把选中的行的补丁交给 git apply，做完取消选择。
func (a *App) applyLines(d *docs.Doc, label, patch, note string, args ...string) {
	r := a.RepoOf(d)
	if r == nil {
		return
	}
	a.runOp(r, label, patch, note, func(string) { ClearSelectedLines(d) }, append(args, "-")...)
}

// StageLines 只暂存选中的行（git apply --cached）。
func (a *App) StageLines(d *docs.Doc) {
	if stage, _, _ := LinesOps(d); !stage {
		return
	}
	if patch, n := a.linesPatch(d, true); patch != "" {
		a.applyLines(d, i18n.T("暂存选中的行"), patch, linesNote(d, n), "apply", "--cached")
	}
}

// UnstageLines 把选中的行移出暂存区，工作区不动（在索引那一边上正着打，git apply --cached）。
func (a *App) UnstageLines(d *docs.Doc) {
	if _, unstage, _ := LinesOps(d); !unstage {
		return
	}
	if patch, n := a.linesPatch(d, false); patch != "" {
		a.applyLines(d, i18n.T("取消暂存选中的行"), patch, linesNote(d, n), "apply", "--cached")
	}
}

// DiscardLines 把工作区里选中的行恢复成暂存区的样子（git apply，改工作区里的文件）；先问。
func (a *App) DiscardLines(d *docs.Doc) {
	if _, _, discard := LinesOps(d); !discard {
		return
	}
	patch, n := a.linesPatch(d, false)
	if patch == "" {
		return
	}
	note := linesNote(d, n)
	a.AskDanger(i18n.Tn("丢弃 %s 里选中的 %d 行？", d.Change.Path, n),
		i18n.T("工作区里这几行会恢复成暂存区的样子，不能撤销。"), i18n.T("丢弃"), func() {
			a.applyLines(d, i18n.T("丢弃选中的行"), patch, note, "apply")
		})
}
