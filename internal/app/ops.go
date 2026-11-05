package app

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是界面上的 git 操作：每一个都对应用户点的一个按钮、菜单项或快捷键。
// 会丢数据的（丢弃改动、删文件）先弹确认；失败弹窗口内的提示；成功出 toast 并重读状态。
// 每条命令和它的输出都记进底栏的"输出"。

// RunOp 在后台跑一条会改仓库的 git 命令：排进仓库的队列（queue.go），同一个仓库一条跑完再跑下一条。
func (a *App) RunOp(r *Repo, label string, args ...string) {
	a.RunOpThen(r, label, nil, args...)
}

// RunOpThen 同 RunOp，成功后在主线程上再跑 then（拿到命令的输出）。
func (a *App) RunOpThen(r *Repo, label string, then func(out string), args ...string) {
	a.runOp(r, label, "", "", then, args...)
}

// logText 是输出栏里一条命令的正文：输出，出错时（输出里没有的话）再加上错误。
func logText(out string, err error) string {
	out = strings.TrimRight(out, "\n")
	if err != nil && !strings.Contains(out, err.Error()) {
		if out != "" {
			out += "\n"
		}
		out += err.Error()
	}
	return out
}

// Stage 暂存（git add -A -- paths）。
func (a *App) Stage(r *Repo, paths ...string) {
	a.RunOp(r, i18n.T("暂存"), append([]string{"add", "-A", "--"}, paths...)...)
}

// Unstage 取消暂存（git restore --staged -- paths）。
func (a *App) Unstage(r *Repo, paths ...string) {
	a.RunOp(r, i18n.T("取消暂存"), append([]string{"restore", "--staged", "--"}, paths...)...)
}

// StageAll 暂存全部，包括未跟踪的。有冲突的文件时先问：暂存会把它们（连同冲突标记）标成已解决。
func (a *App) StageAll(r *Repo) {
	if a.Blocked(StageAllWhy(r)) {
		return
	}
	do := func() { a.RunOp(r, i18n.T("暂存全部"), "add", "-A") }
	if n := r.Status.Conflicts; n > 0 {
		a.Ask(i18n.Tn("暂存全部，包括 %d 个有冲突的文件？", n), i18n.T("有冲突的文件暂存后就算已解决；里面的冲突标记（<<<<<<< 等）还在的话会原样进提交。"), i18n.T("全部暂存"), do)
		return
	}
	do()
}

// StageTracked 只暂存已跟踪文件的改动（Git 栏"未暂存"分组的"全部暂存"）。
func (a *App) StageTracked(r *Repo) { a.RunOp(r, i18n.T("暂存全部"), "add", "-u") }

// UnstageAll 取消全部暂存。
func (a *App) UnstageAll(r *Repo) {
	if a.Blocked(UnstageAllWhy(r)) {
		return
	}
	a.RunOp(r, i18n.T("取消全部暂存"), "reset", "-q")
}

// StageUntracked 暂存所有未跟踪的文件（"未跟踪"分组的"全部暂存"）。
func (a *App) StageUntracked(r *Repo) {
	var paths []string
	for _, ch := range r.Status.Changes {
		if ch.Kind == git.Untracked {
			paths = append(paths, ch.Path)
		}
	}
	if len(paths) > 0 {
		a.Stage(r, paths...)
	}
}

// Discard 丢掉一项未暂存的改动（恢复成索引里的版本）或删掉一个未跟踪的文件 / 目录；先问。
func (a *App) Discard(r *Repo, ch git.Change) {
	switch {
	case ch.Kind == git.Unstaged && ch.Sub != "":
		a.discardSub(r, ch)
	case ch.Kind == git.Unstaged:
		a.AskDanger(i18n.Tf("丢弃 %s 的改动？", ch.Path), i18n.T("工作区里对它的修改会恢复成暂存区 / 上次提交的样子，不能撤销。"), i18n.T("丢弃"), func() {
			a.RunOp(r, i18n.T("丢弃改动"), "restore", "--", ch.Path)
		})
	case ch.Kind == git.Untracked:
		a.AskDanger(i18n.Tf("删除未跟踪的 %s？", ch.Path), i18n.T("它还没进过 Git，删了就找不回来。"), i18n.T("删除"), func() {
			a.RunOp(r, i18n.T("删除"), "clean", "-fdq", "--", ch.Path)
		})
	}
}

// discardSub 是子模块那一项的丢弃：git restore 管不到子模块里面，换成 git submodule update，把子模块检出回
// 父仓库记录的那次提交；子模块里没提交的改动和未跟踪的文件不动（要丢它们得在子模块自己里，把它添加为仓库）。先问。
func (a *App) discardSub(r *Repo, ch git.Change) {
	if !strings.Contains(ch.Sub, "C") {
		a.Toast = i18n.Tf("子模块 %s 检出的还是记录的提交；里面的改动要在子模块自己里丢弃（把它添加为仓库）", ch.Path)
		return
	}
	s, _ := r.Submodule(ch.Path)
	a.Ask(i18n.Tf("把子模块 %s 检出回记录的提交？", ch.Path),
		i18n.Tf("会在子模块里检出父仓库记录的 %s（git submodule update），子模块的 HEAD 变成分离的；里面没提交的改动和未跟踪的文件不动。", git.Short(s.Head)),
		i18n.T("检出"), func() {
			a.RunOp(r, i18n.Tf("更新子模块 %s", ch.Path), "submodule", "update", "--", ch.Path)
		})
}

// DiscardAll 丢掉所有未暂存的改动；先问。
func (a *App) DiscardAll(r *Repo) {
	if a.Blocked(DiscardAllWhy(r)) {
		return
	}
	a.AskDanger(i18n.Tn("丢弃 %d 项未暂存的改动？", r.Status.Unstaged), i18n.T("已暂存和未跟踪的不动；工作区里其余修改会恢复成暂存区的样子，不能撤销。"), i18n.T("丢弃"), func() {
		a.RunOp(r, i18n.T("丢弃改动"), "restore", "--", ".")
	})
}

// SwitchBranch 切到一个本地分支，或由远程分支建一个同名的跟踪分支再切过去。
// 有未提交的改动时先问一声（git switch 会试着把改动带过去，带不过去就失败）。
func (a *App) SwitchBranch(r *Repo, b git.Branch) {
	args := []string{"switch", b.Name}
	target := b.Name
	if b.Remote {
		target = b.Name[strings.IndexByte(b.Name, '/')+1:]
		if i := slices.IndexFunc(r.Branches, func(x git.Branch) bool { return !x.Remote && x.Name == target }); i >= 0 {
			b = r.Branches[i]
			args = []string{"switch", target}
		} else {
			args = []string{"switch", "-c", target, "--track", b.Name}
		}
	}
	if b.CheckedOutElsewhere(r.Root) { // git 会拒绝：一个分支只能在一个工作树里检出（SwitchWhy 的第二条，这里说全一点）
		a.Toast = i18n.Tf("%s 已在 %s 检出", b.Name, filepath.Base(b.Worktree))
		return
	}
	if a.Blocked(SwitchWhy(r, b)) {
		return
	}
	label := i18n.Tf("切换到 %s", target)
	do := func() { a.RunOp(r, label, args...) }
	if n := r.Status.Conflicts + r.Status.Staged + r.Status.Unstaged; n > 0 {
		a.Ask(i18n.Tf("%s？", label), i18n.Tn("有 %d 项未提交的改动。git switch 会试着把它们带到那个分支，带不过去时会失败并保持原样。", n), i18n.T("切换"), do)
		return
	}
	do()
}

// CreateBranch 新建分支并切过去；from 空就从当前 HEAD，否则从那个提交。
func (a *App) CreateBranch(r *Repo, name, from string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	args := []string{"switch", "-c", name}
	if from != "" {
		args = append(args, from)
	}
	a.RunOp(r, i18n.Tf("新建分支 %s", name), args...)
}

// Checkout 检出一个提交（分离 HEAD）；先问。
func (a *App) Checkout(r *Repo, c git.Commit) {
	a.Ask(i18n.Tf("检出 %s？", c.Short), i18n.T("HEAD 会离开分支（detached）。之后要回来就在分支菜单里切回去。"), i18n.T("检出"), func() {
		a.RunOp(r, i18n.Tf("检出 %s", c.Short), "switch", "--detach", c.Hash)
	})
}

// Fetch 抓取（git fetch --prune）。
func (a *App) Fetch(r *Repo) {
	if a.Blocked(FetchWhy(r)) {
		return
	}
	a.RunOp(r, i18n.T("抓取"), "fetch", "--prune")
}

// FetchRemote 只抓取一个远程（仓库菜单"远程"里的"抓取 origin"）。
func (a *App) FetchRemote(r *Repo, name string) {
	a.RunOp(r, i18n.Tf("抓取 %s", name), "fetch", "--prune", name)
}

// Pull 按设置里的拉取方式拉取（默认 git pull --ff-only）。
func (a *App) Pull(r *Repo) {
	if a.Blocked(PullWhy(r)) {
		return
	}
	a.RunOp(r, i18n.T("拉取"), PullArgs(a.St.Settings.PullMode)...)
}

// Push 推送当前分支；没有上游时推到 origin 并设置上游。
// 点下去时先再查一次 PushWhy（菜单开着时状态可能变了）。
func (a *App) Push(r *Repo) {
	if a.Blocked(PushWhy(r)) {
		return
	}
	a.pushNow(r)
}

// pushNow 是不查 PushWhy 的推送：提交后推送、同步拉完再推时状态还没刷新（领先数是旧的），由调用方保证该推。
func (a *App) pushNow(r *Repo) {
	if Upstream(r) == "" {
		a.RunOp(r, i18n.T("推送（设置上游 origin）"), "push", "-u", "origin", "HEAD")
		return
	}
	a.RunOp(r, i18n.T("推送"), "push")
}

// Pulling 是仓库里有拉取在跑或在排队（Pull、同步的前一半、全部拉取排进来的）：按钮转圈、不能再点。
func (r *Repo) Pulling() bool { return r.Queued(i18n.T("拉取")) }

// Pushing 是仓库里有推送（含"推送并设置上游"）在跑或在排队。
func (r *Repo) Pushing() bool {
	return r.Queued(i18n.T("推送")) || r.Queued(i18n.T("推送（设置上游 origin）"))
}
