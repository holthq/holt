package app

import (
	"errors"
	"strings"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是 Git 日常操作：分支下拉的"合并到当前分支 / 把当前分支变基到"，历史右键的拣选 / 还原 / 重置，
// 撤销上次提交、强制推送、同步。都排进仓库的队列；合并、变基、拣选、还原停在冲突上的交给 Git 栏的横幅
// （ongoing.go 的继续 / 跳过 / 中止），不另做界面。

// CurrentBranch 是检出着的本地分支；HEAD 分离、还没有提交时 ok 为假。
func CurrentBranch(r *Repo) (b git.Branch, ok bool) {
	for _, b := range r.Branches {
		if b.Current && !b.Remote {
			return b, true
		}
	}
	return git.Branch{}, false
}

// inHead 是 hash 在 HEAD 往前的历史里：照右栏读进来的历史走父提交，读到的范围外的算不在。
// 只看当前分支的历史时每一行都在；拣选这样的提交只会停在空提交上。
func inHead(r *Repo, hash string) bool {
	h := &r.Hist
	if r.Extra.Head == "" || len(h.Commits) == 0 {
		return false
	}
	at := make(map[string]int, len(h.Commits))
	for i, c := range h.Commits {
		at[c.Hash] = i
	}
	seen := map[string]bool{}
	for todo := []string{r.Extra.Head}; len(todo) > 0; {
		x := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if x == hash {
			return true
		}
		if i, ok := at[x]; ok && !seen[x] {
			seen[x] = true
			todo = append(todo, h.Commits[i].Parents...)
		}
	}
	return false
}

// Merge 把分支 b 合并进当前分支（git merge b），不问：停在冲突上有横幅，合并的方式照用户的 git 配置。
func (a *App) Merge(r *Repo, b git.Branch) {
	if a.Blocked(MergeWhy(r, b)) {
		return
	}
	a.stopOp(r, i18n.Tf("合并 %s", b.Name), "merge", b.Name)
}

// RebaseOnto 把当前分支变基到 b 上（git rebase --autostash b）。先在后台数当前分支上有几个提交会重写、
// 其中几个推送过，再问；没有未提交的改动也带着 --autostash（有的话 git 先贮藏、做完放回）。
func (a *App) RebaseOnto(r *Repo, b git.Branch) {
	cur, ok := CurrentBranch(r)
	if a.Blocked(RebaseOntoWhy(r, b)) || !ok {
		return
	}
	ctx, root, up := r.ctx, r.Root, a.Update
	go func() {
		n, pushed, err := git.RangeCount(ctx, root, b.Name)
		behind := 0
		if err == nil {
			behind, err = git.Count(ctx, root, "HEAD.."+b.Name)
		}
		up(func() {
			if r.gone() {
				return
			}
			if err != nil {
				a.Fail(i18n.T("数提交失败"), err)
				return
			}
			if behind == 0 { // 目标已经在当前分支里了：git 什么也不会做，不问
				a.Toast = i18n.Tf("%s 已经在 %s 上面了，不用变基", cur.Name, b.Name)
				return
			}
			var msg string
			if n == 0 {
				msg = i18n.Tf("当前分支没有 %s 上没有的提交，会直接快进过去。", b.Name)
			} else {
				msg = i18n.Tn("当前分支的 %d 个提交会重写。", n)
				if pushed > 0 {
					msg += i18n.Tn("其中 %d 个已经推送过，做完要强制推送（Git 菜单 → 强制推送）。", pushed)
				}
			}
			msg += i18n.T("有未提交的改动会先自动贮藏、做完再放回（--autostash）。")
			label := i18n.Tf("变基到 %s", b.Name)
			a.Ask(i18n.Tf("把 %s 变基到 %s？", cur.Name, b.Name), msg, i18n.T("开始变基"), func() {
				a.stopOp(r, label, "rebase", "--autostash", b.Name)
			})
		})
	}()
}

// CherryPick 把这个提交的改动拣到当前分支上（git cherry-pick），不问。
func (a *App) CherryPick(r *Repo, cm git.Commit) {
	if a.Blocked(PickWhy(r, cm)) {
		return
	}
	a.stopOp(r, i18n.Tf("拣选 %s", cm.Short), "cherry-pick", cm.Hash)
}

// Revert 生成一个把这个提交的改动反过来的新提交（git revert --no-edit），先问。
func (a *App) Revert(r *Repo, cm git.Commit) {
	if a.Blocked(RevertWhy(r, cm)) {
		return
	}
	a.Ask(i18n.Tf("还原 %s？", cm.Short), i18n.Tf("会生成一个新的提交把 %s 的改动反过来（“%s”）。", cm.Short, cm.Subject),
		i18n.T("还原提交"), func() { a.stopOp(r, i18n.Tf("还原 %s", cm.Short), "revert", "--no-edit", cm.Hash) })
}

// ResetMode 是重置的三档。
type ResetMode int

const (
	ResetSoft  ResetMode = iota // 保留暂存：退掉的提交的改动都在暂存区里
	ResetMixed                  // 保留改动：在工作区里，没暂存
	ResetHard                   // 丢掉改动：工作区和暂存区都回到那个提交
)

// Flag 是 git reset 的参数。
func (m ResetMode) Flag() string { return [...]string{"--soft", "--mixed", "--hard"}[m] }

// Label 是菜单里的名字。
func (m ResetMode) Label() string {
	return i18n.T([...]string{"软（保留暂存）", "混合（保留改动）", "硬（丢掉改动）…"}[m])
}

// ask 是问话的标题：菜单名带括号，套进标题会括号套括号，所以三档各一句。
func (m ResetMode) ask(short string) string {
	return i18n.Tf([...]string{"软重置到 %s？", "混合重置到 %s？", "硬重置到 %s？"}[m], short)
}

// ResetTo 把 HEAD（和当前分支）重置到这个提交，三档都先问：先在后台数 HEAD 上会离开的提交和其中推送过的，
// 硬重置再加上工作区里会丢的改动（已跟踪的文件；未跟踪的 git reset --hard 不动）。
func (a *App) ResetTo(r *Repo, cm git.Commit, mode ResetMode) {
	if a.Blocked(ResetWhy(r, cm)) {
		return
	}
	ctx, root, up := r.ctx, r.Root, a.Update
	go func() {
		n, pushed, err := git.RangeCount(ctx, root, cm.Hash)
		up(func() {
			if r.gone() {
				return
			}
			if err != nil {
				a.Fail(i18n.T("数提交失败"), err)
				return
			}
			b, onBranch := CurrentBranch(r)
			var msg string
			switch {
			case n == 0:
				msg = i18n.Tf("HEAD 会移到 %s，不会有提交离开。", cm.Short)
			case onBranch:
				msg = i18n.Tn("HEAD 和分支 %s 会退到 %s，中间的 %d 个提交只能从 reflog 找回。", b.Name, cm.Short, n)
			default:
				msg = i18n.Tn("HEAD 会退到 %s，中间的 %d 个提交只能从 reflog 找回。", cm.Short, n)
			}
			if pushed > 0 {
				msg += i18n.Tn("其中 %d 个已经推送过，做完要强制推送（Git 菜单 → 强制推送）。", pushed)
			}
			button := i18n.T("重置")
			if mode == ResetHard {
				if k := r.Status.Count - r.Status.Untracked; k > 0 {
					msg += i18n.Tn("工作区里 %d 处未提交的改动也会丢，不能撤销。", k)
				}
				button = i18n.T("重置并丢弃改动")
			}
			label := i18n.Tf("重置到 %s", cm.Short)
			ask := a.Ask
			if mode == ResetHard {
				ask = a.AskDanger
			}
			ask(mode.ask(cm.Short), msg, button, func() {
				a.RunOp(r, label, "reset", "-q", mode.Flag(), cm.Hash)
			})
		})
	}()
}

// UndoCommit 撤销上一次提交（git reset --soft HEAD~1）：改动留在暂存区，提交信息回到提交框——框里已经有字的
// 不覆盖，放进提交信息历史（空框里按 ↑ 取）。只在这个提交已经推送过时先问。
func (a *App) UndoCommit(r *Repo) {
	if a.Blocked(UndoWhy(r)) {
		return
	}
	run := func() {
		root, up := r.Root, a.Update
		go func() {
			msg := git.LastMessage(root)
			up(func() {
				if r.gone() {
					return
				}
				a.RunOpThen(r, i18n.T("撤销上次提交"), func(string) { a.refillCommit(r, msg) }, "reset", "-q", "--soft", "HEAD~1")
			})
		}()
	}
	if r.Extra.HeadPushed() {
		a.AskDanger(i18n.T("撤销已经推送过的提交？"), i18n.T("这个提交已经推送过，撤销后要强制推送。改动留在暂存区，提交信息回到提交框。"),
			i18n.T("撤销提交"), run)
		return
	}
	run()
}

// refillCommit 把撤销掉的提交的信息放回提交框；框里有字时不覆盖，放进提交信息历史。
func (a *App) refillCommit(r *Repo, msg string) {
	if msg == "" {
		return
	}
	if r.Amend {
		a.SetAmend(r, false)
	}
	r.CommitOpen = true
	if strings.TrimSpace(r.CommitMsg) == "" {
		r.CommitMsg = msg
		return
	}
	a.noteCommitMsg(msg)
	a.Toast = i18n.T("信息已复制到提交信息历史（↑）")
}

// PushForce 强制推送当前分支（git push --force-with-lease），先问：远程分支自从上次抓取后被别人推过就会被拒绝。
func (a *App) PushForce(r *Repo) {
	if a.Blocked(PushForceWhy(r)) {
		return
	}
	upstream := Upstream(r)
	b, _ := CurrentBranch(r)
	a.AskDanger(i18n.Tf("强制推送 %s → %s？", b.Name, upstream),
		i18n.Tf("用 --force-with-lease：远程分支自从上次抓取后被别人改过就会被拒绝。本地领先 %d、落后 %d。", r.Status.Ahead, r.Status.Behind),
		i18n.T("强制推送"), func() { a.RunOp(r, i18n.T("强制推送"), "push", "--force-with-lease") })
}

// Sync 拉取再推送：按设置的拉取方式拉，成功了再推；拉取失败（冲突、不能快进）就停在那。
// 没有上游时和推送一样（推到 origin 并设置上游）；领先落后都是 0（按上次抓取）时只提示。
func (a *App) Sync(r *Repo) {
	switch {
	case a.Blocked(SyncWhy(r)):
	case Upstream(r) == "":
		a.pushNow(r)
	default:
		a.RunOpThen(r, i18n.T("拉取"), func(string) { a.pushNow(r) }, PullArgs(a.St.Settings.PullMode)...)
	}
}

// stopOp 排一条可能停在冲突上的命令（合并、变基到、拣选、还原）：停下了（输出里有 CONFLICT，或 git 目录里
// 有了进行中的记号）不算失败，输出栏照记，提示到横幅里接着做；真失败照常弹。
func (a *App) stopOp(r *Repo, label string, args ...string) {
	o := &Op{Label: label, Cmd: "$ git " + strings.Join(args, " "), args: args}
	gitDir := r.Extra.GitDir
	o.done = func(out string, err error) {
		switch {
		case errors.Is(err, git.ErrCanceled):
			a.Log(r, o.Cmd, canceledText(out, label, nil), true)
			a.Toast = i18n.Tf("已取消：%s", label)
		case err != nil && strings.Contains(out, "CONFLICT ("):
			a.Log(r, o.Cmd, logText(out, nil), false)
			a.Toast = i18n.Tf("%s停在了冲突上：在 Git 栏的横幅里解决后继续，或中止", label)
		case err != nil && gitDir != "" && git.ReadOngoing(gitDir) != git.NoOp:
			a.Log(r, o.Cmd, logText(out, nil), false)
			a.Toast = i18n.Tf("%s停下了：输出栏里有原因，在 Git 栏的横幅里继续、跳过或中止", label)
		case err != nil:
			a.Log(r, o.Cmd, logText(out, err), true)
			a.Fail(i18n.Tf("%s失败", label), err)
		default:
			a.Log(r, o.Cmd, logText(out, nil), false)
			a.Toast = i18n.Tf("%s完成", label)
		}
	}
	a.enqueue(r, o)
}
