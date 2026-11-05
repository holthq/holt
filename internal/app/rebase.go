package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是交互式变基向导：右栏历史里右键一个提交"从这里开始交互式变基…"，从 HEAD 到它的提交开成一个标签，
// 排顺序、选每一条做什么（pick / reword / squash / fixup / drop / edit）、改信息，点"开始变基"先问一次再跑。
// 全程不开编辑器：todo 写进临时文件、GIT_SEQUENCE_EDITOR 换成 cp 它；改信息（reword、squash 合在一起的）
// 写成 pick / fixup 加一行 exec git commit --amend -F <信息文件>。停下来（冲突、edit、exec 失败）交给 Git 栏的横幅。

// RebaseLimit 是一次最多变基几个提交。
const RebaseLimit = 100

// RebaseView 是 r 的右栏历史现在是当前分支的完整历史：不筛、不是所有分支、不是别的工作树 / 标签、
// 不是"所有仓库"视图。交互式变基只从这里开（别的视图里右键菜单没有那一项）。
func (a *App) RebaseView(r *Repo) bool {
	h := &r.Hist
	return !a.St.HistAll && h.RefKind == RefNone && h.Ref == "" && h.ref == "" && !h.all && h.Path == "" && h.path == "" && h.query == ""
}

// RebaseRange 是从 HEAD 到 cm（含）的提交，最新的在前（历史列表里的顺序）；why 不空时不能从这里开始
// （菜单项灰着、后面括着它）：范围是一条线、没有合并提交，最多 RebaseLimit 个，仓库里没有进行到一半的操作。
func (a *App) RebaseRange(r *Repo, cm git.Commit) (commits []git.Commit, why string) {
	h := &r.Hist
	switch {
	case !a.RebaseView(r):
		return nil, i18n.T("只能在当前分支的历史里用（不筛选、不看所有分支）")
	case r.Extra.Ongoing != git.NoOp:
		return nil, i18n.T("有进行中的操作")
	case r.Extra.Head == "" || len(h.Commits) == 0 || h.Commits[0].Hash != r.Extra.Head:
		return nil, i18n.T("历史还没读到当前的 HEAD")
	}
	k := -1
	for i, c := range h.Commits {
		if len(c.Parents) > 1 {
			return nil, i18n.T("范围里有合并提交")
		}
		if c.Hash == cm.Hash {
			k = i
			break
		}
		if i+1 >= RebaseLimit {
			return nil, i18n.Tf("一次最多 %d 个提交", RebaseLimit)
		}
		if len(c.Parents) == 0 || i+1 >= len(h.Commits) || c.Parents[0] != h.Commits[i+1].Hash {
			return nil, i18n.T("这个提交不在当前分支上")
		}
	}
	if k < 0 {
		return nil, i18n.T("这个提交不在当前分支上")
	}
	return h.Commits[:k+1], ""
}

// RebaseTitle 是向导标签的名字。
func RebaseTitle(r *Repo, n int) string {
	return i18n.Tn("交互式变基 · %s · %d 个提交", r.Name, n)
}

// OpenRebase 开交互式变基向导：从 HEAD 到 cm 的提交排成计划（最老的在上面，全是 pick），开成一个标签、键盘给它。
// 什么都不跑，直到点"开始变基"。这个仓库已经开着向导的：没开始的换成新的范围，跑着 / 停着的只切过去。
func (a *App) OpenRebase(r *Repo, cm git.Commit) {
	commits, why := a.RebaseRange(r, cm)
	if why != "" {
		a.Toast = why
		return
	}
	plan := &docs.RebasePlan{Head: r.Extra.Head}
	if len(cm.Parents) > 0 {
		plan.Base = cm.Parents[0]
	}
	for i := range commits {
		c := commits[len(commits)-1-i]
		plan.Steps = append(plan.Steps, docs.RebaseStep{Commit: c, Pushed: r.Extra.Pushed(c.Hash), Orig: i})
	}
	d := docs.NewRebase(r.Root, RebaseTitle(r, len(commits)), plan)
	if old := a.reuse(d.Key(), true); old != nil {
		if p := old.Rebase; p.Running || p.Stopped && !p.Ended {
			a.Toast = i18n.T("这个仓库的变基还没结束")
			a.FocusDoc = true
			return
		}
		a.dropRebaseFiles(old.Rebase)
		old.Rebase, old.Title = plan, d.Title
		d = old
	} else {
		a.addDoc(d, true)
	}
	plan.Defaults()
	a.FocusDoc = true
}

// RebaseCommit 是命令"交互式变基…"用的提交：当前仓库右栏历史里选中的那条。
func (a *App) RebaseCommit() (*Repo, git.Commit, bool) {
	r := a.Current()
	if r == nil || a.St.HistAll {
		return nil, git.Commit{}, false
	}
	rows := HistRows(r)
	h := &r.Hist
	if h.Sel < 0 || h.Sel >= len(rows) {
		return nil, git.Commit{}, false
	}
	if row := rows[h.Sel]; row.CI >= 0 && row.CI < len(h.Commits) {
		return r, h.Commits[row.CI], true
	}
	return nil, git.Commit{}, false
}

// canRebaseHere 是命令"交互式变基…"能不能用。
func canRebaseHere(a *App) bool {
	r, cm, ok := a.RebaseCommit()
	if !ok {
		return false
	}
	return a.RebaseWhy(r, cm) == ""
}

// RebaseHere 是命令"交互式变基…"：对右栏历史里选中的提交开向导。
func (a *App) RebaseHere() {
	if r, cm, ok := a.RebaseCommit(); ok {
		a.OpenRebase(r, cm)
		return
	}
	a.Toast = i18n.T("先在右栏的历史里选一个提交")
}

// RebaseResult 是做完以后剩几个提交（drop、squash、fixup 都不算）。
func RebaseResult(p *docs.RebasePlan) int {
	n := 0
	for _, s := range p.Steps {
		if s.Action != docs.RebaseDrop && !s.Action.Melds() {
			n++
		}
	}
	return n
}

// RebasePushed 是计划里推送过的提交有几个（做完都得强制推送）。
func RebasePushed(p *docs.RebasePlan) int {
	n := 0
	for _, s := range p.Steps {
		if s.Pushed {
			n++
		}
	}
	return n
}

// RebaseCounts 是动作的摘要，如"pick 3、squash 1、drop 1"（输出栏那一行）。
func RebaseCounts(p *docs.RebasePlan) string {
	var parts []string
	for _, x := range docs.RebaseActions {
		n := 0
		for _, s := range p.Steps {
			if s.Action == x {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", x, n))
		}
	}
	return i18n.List(parts)
}

// CanMeld 是第 i 步能不能设成 squash / fixup：前面得有一条留下来的（不是 drop）。
func CanMeld(p *docs.RebasePlan, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if p.Steps[j].Action != docs.RebaseDrop {
			return true
		}
	}
	return false
}

// RebaseProblem 是"开始变基"为什么灰着；能开始为空。
func RebaseProblem(p *docs.RebasePlan) string {
	if p.Running || p.Stopped {
		return i18n.T("这次变基已经开始了")
	}
	for i, s := range p.Steps {
		if s.Action.Melds() && !CanMeld(p, i) {
			return i18n.Tf("第 %d 行前面没有能并入的提交", i+1)
		}
		if s.Action.HasMsg() && strings.TrimSpace(s.Msg) == "" {
			return i18n.Tf("第 %d 行的提交信息是空的", i+1)
		}
	}
	if !p.Changed() {
		return i18n.T("还没改什么")
	}
	return ""
}

// SetRebaseAction 把第 i 步设成 x（第一条之类前面没有能并入的，不能 squash / fixup）。
func (a *App) SetRebaseAction(p *docs.RebasePlan, i int, x docs.RebaseAction) {
	if p.Running || p.Stopped || i < 0 || i >= len(p.Steps) {
		return
	}
	if x.Melds() && !CanMeld(p, i) {
		a.Toast = i18n.T("前面没有能并入的提交")
		return
	}
	s := &p.Steps[i]
	if s.Action != x {
		s.Action, s.Msg, s.Def, s.Edited = x, "", "", false
	}
	p.Sel = i
	p.Defaults()
}

// MoveRebaseStep 把第 i 步挪 by 格（-1 往上一格）。
func (a *App) MoveRebaseStep(p *docs.RebasePlan, i, by int) {
	if p.Running || p.Stopped {
		return
	}
	if p.Move(i, by) {
		p.Defaults()
	}
}

// ResetRebase 把计划换回开的时候（全是 pick、原来的顺序）。
func (a *App) ResetRebase(p *docs.RebasePlan) {
	if p.Running || p.Stopped {
		return
	}
	p.Reset()
	p.Defaults()
}

// shellQuote 用单引号包起来给 sh（GIT_SEQUENCE_EDITOR、todo 里的 exec 行），里面的单引号写成：结束引号、反斜杠转义的单引号、再开引号。
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// RebaseTodo 把计划写成 git rebase -i 的 todo。msgFile(text) 把一段信息写进文件、返回路径：
// reword 写成 pick 加 exec commit --amend -F；squash 写成 fixup，这一组（留下的那条和并进来的）最后一条之后
// exec commit --amend -F 合在一起的信息；信息和原来一样的不加 exec。
func RebaseTodo(p *docs.RebasePlan, msgFile func(text string) (string, error)) (string, error) {
	p.Defaults()
	var b strings.Builder
	amend := func(msg string) error {
		path, err := msgFile(msg)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "exec git -c core.editor=true commit --amend -q -F %s\n", shellQuote(path))
		return nil
	}
	// 一组：一条留下来的（pick / reword / edit）和后面并进来的（squash / fixup，中间的 drop 不算）
	msg, changed := "", false
	flush := func() error {
		if changed {
			if err := amend(msg); err != nil {
				return err
			}
		}
		msg, changed = "", false
		return nil
	}
	for _, s := range p.Steps {
		h := s.Commit.Hash
		switch s.Action {
		case docs.RebaseDrop:
			fmt.Fprintf(&b, "drop %s\n", h)
			continue
		case docs.RebaseSquash, docs.RebaseFixup:
			fmt.Fprintf(&b, "fixup %s\n", h)
			if s.Action == docs.RebaseSquash {
				msg, changed = s.Msg, true
			}
			continue
		}
		if err := flush(); err != nil {
			return "", err
		}
		switch s.Action {
		case docs.RebaseEdit:
			fmt.Fprintf(&b, "edit %s\n", h)
		default:
			fmt.Fprintf(&b, "pick %s\n", h)
		}
		if s.Action == docs.RebaseReword && s.Msg != docs.Message(s.Commit) {
			msg, changed = s.Msg, true
		}
	}
	if err := flush(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeRebaseFiles 把 todo 和信息写进临时文件（workbench-rebase-*），返回 todo 的路径和写了的全部文件。
func writeRebaseFiles(p *docs.RebasePlan) (todo string, files []string, err error) {
	write := func(pattern, text string) (string, error) {
		f, err := os.CreateTemp("", pattern)
		if err != nil {
			return "", err
		}
		files = append(files, f.Name())
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return f.Name(), err
	}
	text, err := RebaseTodo(p, func(msg string) (string, error) {
		return write("workbench-rebase-*.msg", strings.TrimRight(msg, "\n")+"\n")
	})
	if err == nil {
		todo, err = write("workbench-rebase-*.todo", text)
	}
	if err != nil {
		removeFiles(files)
		return "", nil, err
	}
	return todo, files, nil
}

func removeFiles(files []string) {
	for _, f := range files {
		_ = os.Remove(f)
	}
}

// dropRebaseFiles 删掉计划写过的临时文件。
func (a *App) dropRebaseFiles(p *docs.RebasePlan) {
	removeFiles(p.Files)
	p.Files = nil
}

// StartRebase 是向导上的"开始变基"：先问一次（历史会重写、推送过的要强制推送、停下时交给横幅），确认后跑。
func (a *App) StartRebase(d *docs.Doc) {
	r, p := a.RepoOf(d), d.Rebase
	if r == nil || p == nil {
		return
	}
	p.Defaults()
	if why := RebaseProblem(p); why != "" {
		a.Toast = why
		return
	}
	switch {
	case r.Extra.Ongoing != git.NoOp:
		a.Toast = i18n.T("有进行中的操作")
		return
	case r.Extra.Head != p.Head:
		a.Toast = i18n.T("仓库的 HEAD 变了，从历史里重新开一次向导")
		return
	}
	n := len(p.Steps)
	msg := i18n.Tn("这 %d 个提交会换成新的 %d 个（新的哈希）。", n, RebaseResult(p))
	if k := RebasePushed(p); k > 0 {
		msg += i18n.Tn("其中 %d 个已经推送过，做完要强制推送（Git 菜单 → 强制推送）。", k)
	}
	if r.Status.Staged+r.Status.Unstaged > 0 {
		msg += i18n.T("没提交的改动先自动贮藏，做完再恢复。")
	}
	msg += i18n.T("有冲突或停在 edit 时会停下来，在 Git 栏的横幅里继续、跳过或放弃。")
	a.Ask(i18n.Tn("改写 %s 的 %d 个提交？", r.Name, n), msg, i18n.T("开始变基"), func() { a.runRebase(r, d) })
}

// runRebase 写好 todo、排进仓库的队列：git rebase -i --autostash <Base>（根提交是 --root）。
func (a *App) runRebase(r *Repo, d *docs.Doc) {
	p := d.Rebase
	todo, files, err := writeRebaseFiles(p)
	if err != nil {
		a.Fail(i18n.T("写变基的计划失败"), err)
		return
	}
	args, from := []string{"rebase", "-i", "--autostash"}, "--root"
	if p.Base != "" {
		args, from = append(args, p.Base), git.Short(p.Base)
	} else {
		args = append(args, "--root")
	}
	n, m := len(p.Steps), RebaseResult(p)
	label := i18n.T("交互式变基")
	o := &Op{Label: label, Cmd: i18n.Tn("$ git rebase -i %s（%d 个提交：%s）", from, n, RebaseCounts(p)), args: args,
		env: []string{"GIT_SEQUENCE_EDITOR=cp " + shellQuote(todo)}}
	p.Files, p.Running, p.Stopped, p.Ended, p.Reason = files, true, false, false, ""
	r.rebase = p
	gitDir := r.Extra.GitDir
	o.done = func(out string, err error) {
		p.Running = false
		stopped := gitDir != "" && git.ReadOngoing(gitDir) == git.Rebasing
		if !stopped {
			r.rebase = nil
		}
		switch {
		case stopped: // 冲突、edit、exec 失败：横幅接手，临时文件留到变基结束（后面的 exec 还要读）
			a.Log(r, o.Cmd, logText(out, nil), false)
			p.Stopped, p.Reason = true, rebaseReason(out)
			a.Toast = i18n.Tf("变基停下了：%s", p.Reason)
		case errors.Is(err, git.ErrCanceled):
			a.Log(r, o.Cmd, canceledText(out, label, nil), true)
			a.Toast = i18n.Tf("已取消：%s", label)
			a.dropRebaseFiles(p)
		case err != nil:
			a.Log(r, o.Cmd, logText(out, err), true)
			a.Fail(i18n.Tf("%s失败", label), err)
			a.dropRebaseFiles(p)
		default:
			a.Log(r, o.Cmd, logText(out, nil), false)
			a.dropRebaseFiles(p)
			a.Toast = i18n.Tn("变基完成：%d 个提交 → %d 个", n, m)
			if a.GroupOf(d) != nil {
				a.RemoveDoc(d)
			}
		}
	}
	a.enqueue(r, o)
}

// rebaseReason 从 git 的输出里认出为什么停下。
func rebaseReason(out string) string {
	switch {
	case strings.Contains(out, "CONFLICT ("), strings.Contains(out, "could not apply"):
		return i18n.T("有冲突")
	case strings.Contains(out, "Execution failed"):
		return i18n.T("改提交信息的那一步没成功")
	case strings.Contains(out, "Stopped at"), strings.Contains(out, "You can amend"):
		return i18n.T("停在 edit 这一步")
	}
	return i18n.T("git 停下了")
}

// followRebase 在状态重读后跟上仓库：停下的变基在横幅里继续完了或放弃了（不再是变基中），向导标成结束、删临时文件
// （向导关了也一样，计划记在 r.rebase 上）。
func (a *App) followRebase(r *Repo) {
	p := r.rebase
	if p == nil || p.Running || r.Extra.Ongoing == git.Rebasing {
		return
	}
	if dir := r.Extra.GitDir; dir == "" || git.ReadOngoing(dir) == git.Rebasing { // 读之前就开始的那次重读可能是旧的：再看一眼
		return
	}
	r.rebase = nil
	p.Ended = true
	a.dropRebaseFiles(p)
}
