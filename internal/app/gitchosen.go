package app

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是 Git 栏的多选：⌘点加减、⇧点一段、⌘A 全选、拖着选都是列表（ChangeList.Selection）做的，
// 选中按行的键（ChangeKey）记。选了好几行时点击不预览，右键其中一行一次暂存 / 取消暂存 / 丢弃选中的全部（跨仓库也行），
// 回车把它们都开成固定的 Diff。行里悬停出来的暂存 / 取消暂存 / 丢弃按钮（ActChanges）和列表有焦点时的空格 / ⌘⌫
// （KeyChanges）也一样：指针所在的行在多选里就作用于选中的全部，不然只管那一行。

// rowKey 是第 i 行的键，i 不在范围里是零值（不会在选中里）。
func rowKey(rows []ChangeRow, i int) ChangeKey {
	if i < 0 || i >= len(rows) {
		return ChangeKey{}
	}
	return rows[i].Key()
}

// chooseGit 把选中换成第 i 行（应用自己选的：跟着当前标签、键盘跳过分组标题之后）；-1 清掉。
func (a *App) chooseGit(rows []ChangeRow, i int) {
	a.GitChosen.Clear()
	if i >= 0 && i < len(rows) {
		a.GitChosen.Add(rows[i].Key())
	}
}

// shownChosen 是 rows 里选中的行数。
func (a *App) shownChosen(rows []ChangeRow) int {
	n := 0
	for _, cr := range rows {
		if a.GitChosen.Has(cr.Key()) {
			n++
		}
	}
	return n
}

// ChosenChanges 是 Git 栏里选中的变更行（RowChange），按显示的顺序；收起的组里、已经不在了的不算。
func (a *App) ChosenChanges() []ChangeRow {
	var out []ChangeRow
	if a.GitChosen.Len() == 0 {
		return out
	}
	for _, cr := range a.GitRows() {
		if cr.Kind == RowChange && a.GitChosen.Has(cr.Key()) {
			out = append(out, cr)
		}
	}
	return out
}

// MenuChanges 是右键第 i 行（一项变更）的菜单作用于哪些行：它在两行以上的选中里就是选中的全部，不然为 nil（只对它，右键不改选中）。
func (a *App) MenuChanges(cr ChangeRow) []ChangeRow {
	if !a.GitChosen.Has(cr.Key()) {
		return nil
	}
	if rows := a.ChosenChanges(); len(rows) > 1 {
		return rows
	}
	return nil
}

// ClearGitChosen 是 Git 栏里的 Esc：不再多选，选中回到当前标签那一行。
func (a *App) ClearGitChosen() {
	a.gitMulti = false
	a.syncChangeSel()
}

// OpenDiffs 把 rows 都开成固定的 Diff 标签（冲突开成编辑器），多于 OpenAsk 个先问；Git 栏的多选留着。
// 未跟踪目录那行跳过（开出来只是一句说明，里面的文件要展开了选）。
func (a *App) OpenDiffs(rows []ChangeRow) {
	rows = slices.DeleteFunc(slices.Clone(rows), func(cr ChangeRow) bool { return cr.Change.Dir })
	if len(rows) == 0 {
		return
	}
	open := func() {
		keys, cur := slices.Collect(a.GitChosen.All()), a.GitSel
		for _, cr := range rows {
			if cr.Change.Kind == git.Conflict {
				a.OpenConflict(cr.Repo, cr.Change, true)
			} else {
				a.OpenDiff(cr.Repo, cr.Change, true)
			}
		}
		a.UseRepo(rows[len(rows)-1].Repo)
		if len(keys) > 1 { // 开标签时选中跟着当前标签收成了一行：放回去
			a.GitChosen.Clear()
			for _, k := range keys {
				a.GitChosen.Add(k)
			}
			a.GitSel, a.gitPrev = cur, cur
			a.gitMulti, a.gitMultiDoc = true, a.CurrentDoc()
		}
	}
	if len(rows) > OpenAsk {
		a.Ask(i18n.Tn("打开 %d 个 Diff？", len(rows)), i18n.T("每个文件开一个标签。"), i18n.T("打开"), open)
		return
	}
	open()
}

// byRepo 把 rows 里 keep 的变更按仓库分开（仓库按第一次出现的顺序），每个仓库是它们的路径。
func byRepo(rows []ChangeRow, keep func(git.Change) bool) ([]*Repo, map[*Repo][]string) {
	var repos []*Repo
	paths := map[*Repo][]string{}
	for _, cr := range rows {
		if cr.Kind != RowChange || !keep(cr.Change) {
			continue
		}
		if _, ok := paths[cr.Repo]; !ok {
			repos = append(repos, cr.Repo)
		}
		paths[cr.Repo] = append(paths[cr.Repo], cr.Change.Path)
	}
	return repos, paths
}

// CanStage 是能暂存的：未暂存、未跟踪的（冲突不算：暂存它是"标记为已解决"）。
func CanStage(ch git.Change) bool { return ch.Kind == git.Unstaged || ch.Kind == git.Untracked }

// CanUnstage 是能取消暂存的：已暂存的。
func CanUnstage(ch git.Change) bool { return ch.Kind == git.Staged }

// CountChanges 是 rows 里 keep 的变更数。
func CountChanges(rows []ChangeRow, keep func(git.Change) bool) int {
	n := 0
	for _, cr := range rows {
		if cr.Kind == RowChange && keep(cr.Change) {
			n++
		}
	}
	return n
}

// StageChanges 暂存 rows 里未暂存、未跟踪的，一个仓库一条 git add。
func (a *App) StageChanges(rows []ChangeRow) {
	repos, paths := byRepo(rows, CanStage)
	for _, r := range repos {
		a.Stage(r, paths[r]...)
	}
}

// UnstageChanges 取消暂存 rows 里已暂存的，一个仓库一条 git restore --staged。
func (a *App) UnstageChanges(rows []ChangeRow) {
	repos, paths := byRepo(rows, CanUnstage)
	for _, r := range repos {
		a.Unstage(r, paths[r]...)
	}
}

// gitAfter 记着 Git 栏里对选中的行做暂存 / 取消暂存 / 丢弃时，它后面（没有就是前面）第一个不在选中里、能停的行：
// 状态重读后那行（连同多选的其余几行）挪到别的组或没了、选中跟不上当前标签时，选中停在 next 上（它也没了就 prev，
// 再没有就原来的行号附近），键盘可以接着按。
type gitAfter struct {
	set        bool
	next, prev ChangeKey
	at         int
}

// markGitAfter 在改动 r 里的 paths 之前记下 gitAfter；选中的行不是其中之一时不记。
func (a *App) markGitAfter(r *Repo, paths []string) {
	rows := a.GitRows()
	sel := a.GitSel
	if sel < 0 || sel >= len(rows) || rows[sel].Kind != RowChange || rows[sel].Repo != r || !slices.Contains(paths, rows[sel].Change.Path) {
		return
	}
	after := gitAfter{set: true, at: sel}
	near := func(d int) ChangeKey {
		for i := nextStop(rows, sel, d); i >= 0; i = nextStop(rows, i, d) {
			if k := rows[i].Key(); !a.GitChosen.Has(k) {
				return k
			}
		}
		return ChangeKey{}
	}
	after.next, after.prev = near(1), near(-1)
	a.gitAfter = after
}

// pick 是状态重读后选中落到的行，没有为 -1。
func (g gitAfter) pick(rows []ChangeRow) int {
	for _, k := range []ChangeKey{g.next, g.prev} {
		if k == (ChangeKey{}) {
			continue
		}
		if i := slices.IndexFunc(rows, func(cr ChangeRow) bool { return cr.Key() == k }); i >= 0 {
			return i
		}
	}
	at := min(g.at, len(rows)-1)
	if at < 0 {
		return -1
	}
	if canStop(rows[at].Kind) {
		return at
	}
	if i := nextStop(rows, at, 1); i >= 0 {
		return i
	}
	return nextStop(rows, at, -1)
}

// ActChanges 是变更行上行内按钮作用于哪些行：cr 在两行以上的多选里时是选中的全部（同右键菜单），不然只有它。
func (a *App) ActChanges(cr ChangeRow) []ChangeRow {
	if rows := a.MenuChanges(cr); rows != nil {
		return rows
	}
	return []ChangeRow{cr}
}

// KeyChanges 是 Git 栏键盘上的暂存 / 丢弃键（命令面板里的同一条）作用于哪些行：选中的变更；
// 选中里没有变更时是光标那一行（是变更的话）。都没有为 nil。
func (a *App) KeyChanges() []ChangeRow {
	if rows := a.ChosenChanges(); len(rows) > 0 {
		return rows
	}
	rows := a.GitRows()
	if a.GitSel >= 0 && a.GitSel < len(rows) && rows[a.GitSel].Kind == RowChange {
		return rows[a.GitSel : a.GitSel+1 : a.GitSel+1]
	}
	return nil
}

// CanToggleStage 是 rows 里有能暂存或能取消暂存的（全是冲突、或是空的就不能）。
func (a *App) CanToggleStage(rows []ChangeRow) bool {
	return CountChanges(rows, CanStage)+CountChanges(rows, CanUnstage) > 0
}

// ToggleStageRows 是暂存键：rows 里有能暂存的（未暂存、未跟踪）就暂存它们，没有就取消暂存已暂存的。冲突不动。
func (a *App) ToggleStageRows(rows []ChangeRow) {
	if CountChanges(rows, CanStage) > 0 {
		a.StageChanges(rows)
		return
	}
	a.UnstageChanges(rows)
}

// DiscardRows 是丢弃键 / 行内的丢弃按钮：一项时和右键菜单的"丢弃改动…" / "删除文件…"一样（Discard），
// 好几项时是"丢弃 N 项改动…"（DiscardChanges）。都先问。
func (a *App) DiscardRows(rows []ChangeRow) {
	if len(rows) == 1 {
		a.Discard(rows[0].Repo, rows[0].Change)
		return
	}
	a.DiscardChanges(rows)
}

// ToggleStageChosen 是 changes.toggleStage：对 KeyChanges 暂存 / 取消暂存。
func (a *App) ToggleStageChosen() { a.ToggleStageRows(a.KeyChanges()) }

// DiscardChosen 是 changes.discard：丢弃 KeyChanges 里未暂存的改动、删掉未跟踪的（先问）。
func (a *App) DiscardChosen() { a.DiscardRows(a.KeyChanges()) }

// DiscardChanges 丢掉 rows 里未暂存的改动、删掉未跟踪的文件：只问一次（列出前 5 个），
// 然后每个仓库一条 git restore、一条 git clean。已暂存的、冲突不动。
// 子模块 git restore 管不到：检出的提交变了的（Sub 里有 C）检出回记录的提交（同 Discard），里面的改动不动；
// 只有里面的改动 / 未跟踪文件的不算进 N 项、单说一句（这里丢不了，要到子模块自己的仓库里），选中的全是这种时不问、只提示。
func (a *App) DiscardChanges(rows []ChangeRow) {
	isSub := func(ch git.Change) bool { return ch.Kind == git.Unstaged && ch.Sub != "" }
	isInner := func(ch git.Change) bool { return isSub(ch) && !strings.Contains(ch.Sub, "C") }
	isSubMoved := func(ch git.Change) bool { return isSub(ch) && strings.Contains(ch.Sub, "C") }
	isUnstaged := func(ch git.Change) bool { return ch.Kind == git.Unstaged && !isInner(ch) }
	isUntracked := func(ch git.Change) bool { return ch.Kind == git.Untracked }
	nu, nn, ni := CountChanges(rows, isUnstaged), CountChanges(rows, isUntracked), CountChanges(rows, isInner)
	inner := i18n.Tn("其中 %d 个子模块只有里面的改动，这里不动（到子模块自己的仓库里丢弃）", ni)
	var title string
	switch {
	case nu == 0 && nn == 0:
		if ni > 0 {
			a.Toast = inner
		}
		return
	case nn == 0:
		title = i18n.Tn("丢弃 %d 项未暂存的改动？", nu)
	case nu == 0:
		title = i18n.Tn("删除 %d 个未跟踪的文件？", nn)
	default:
		title = i18n.Tn("丢弃 %d 项未暂存的改动、删除 %d 个未跟踪的文件？", nu, nn)
	}
	names := make([]string, 0, 6)
	for _, cr := range rows {
		if cr.Kind != RowChange || !CanStage(cr.Change) || isInner(cr.Change) {
			continue
		}
		if len(names) == 5 {
			names = append(names, "  "+i18n.Tf("…还有 %d 项", nu+nn-5))
			break
		}
		names = append(names, "  "+path.Base(strings.TrimSuffix(cr.Change.Path, "/")))
	}
	msg := strings.Join(names, "\n") + "\n" + i18n.T("未暂存的改动会恢复成暂存区 / 上次提交的样子，未跟踪的文件删了就找不回来；不能撤销。")
	if ns := CountChanges(rows, isSubMoved); ns > 0 {
		msg += "\n" + i18n.Tn("其中 %d 个是子模块：检出回父仓库记录的提交（git submodule update），子模块里没提交的改动不动。", ns)
	}
	if ni > 0 {
		msg += "\n" + inner
	}
	a.AskDanger(title, msg, i18n.T("丢弃"), func() {
		_, restore := byRepo(rows, func(ch git.Change) bool { return ch.Kind == git.Unstaged && ch.Sub == "" })
		_, subs := byRepo(rows, isSubMoved)
		_, clean := byRepo(rows, isUntracked)
		for _, r := range a.Repos { // 仓库按列表的顺序；问的时候移除了的跳过
			a.markGitAfter(r, slices.Concat(restore[r], subs[r], clean[r]))
			if ps := restore[r]; len(ps) > 0 {
				a.RunOp(r, i18n.T("丢弃改动"), append([]string{"restore", "--"}, ps...)...)
			}
			if ps := subs[r]; len(ps) > 0 {
				a.RunOp(r, i18n.T("更新子模块"), append([]string{"submodule", "update", "--"}, ps...)...)
			}
			if ps := clean[r]; len(ps) > 0 {
				a.RunOp(r, i18n.T("删除"), append([]string{"clean", "-fdq", "--"}, ps...)...)
			}
		}
	})
}

// fullPath 是这一项变更在磁盘上的路径。
func (cr ChangeRow) fullPath() string {
	return filepath.Join(cr.Repo.Root, filepath.FromSlash(cr.Change.Path))
}

// RevealChanges 在访达里显示 rows：每个目录只显示其中第一项（同 RevealRows）。
func (a *App) RevealChanges(rows []ChangeRow) {
	seen := map[string]bool{}
	for _, cr := range rows {
		p := cr.fullPath()
		if dir := filepath.Dir(p); !seen[dir] {
			seen[dir] = true
			a.InFinder(p)
		}
	}
}

// ChangePaths 是 rows 的路径，一行一个：完整路径，或仓库内路径（rel）。
func ChangePaths(rows []ChangeRow, rel bool) string {
	lines := make([]string, len(rows))
	for i, cr := range rows {
		if rel {
			lines[i] = cr.Change.Path
		} else {
			lines[i] = cr.fullPath()
		}
	}
	return strings.Join(lines, "\n")
}
