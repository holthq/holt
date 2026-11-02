package app

import (
	"errors"
	"strings"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是进行到一半的合并 / 变基 / 拣选 / 还原：Git 栏仓库段头下面的横幅（ui 的 ongoingRow）、
// Git 菜单和仓库右键上的继续 / 跳过 / 中止。停在哪一步是 git.ReadProgress 读的（Extra.Progress）。

// OngoingTitle 是横幅的第一行："变基中（1 / 2）：topic one"、"合并中：Merge branch 'feature'"；没有进行中为空。
func OngoingTitle(r *Repo) string {
	p := r.Extra.Progress
	what := p.Subject
	if what == "" {
		what = git.Short(p.Head)
	}
	switch r.Extra.Ongoing {
	case git.Merging:
		return i18n.Tf("合并中：%s", what)
	case git.Rebasing:
		if p.Total > 0 {
			return i18n.Tf("变基中（%d / %d）：%s", p.Step, p.Total, what)
		}
		return i18n.Tf("变基中：%s", what)
	case git.CherryPicking:
		return i18n.Tf("拣选中：%s", what)
	case git.Reverting:
		return i18n.Tf("还原中：%s", what)
	}
	return ""
}

// OngoingNote 是横幅第一行下面的小字：还有几个文件有冲突，或者可以继续了；拣选 / 还原好几个时后面还排着几个。
func OngoingNote(r *Repo) string {
	var s string
	switch n := r.Status.Conflicts; {
	case n > 0:
		s = i18n.Tn("还有 %d 个文件有冲突：在编辑器里解决，再点“标记为已解决”", n)
	case r.Extra.Ongoing == git.Merging: // 提交框也能提交这次合并（MergeReady）
		s = i18n.T("冲突都解决了：点继续，或在提交框里写信息提交")
	default:
		s = i18n.T("冲突都解决了，可以继续")
	}
	if n := r.Extra.Progress.Left; n > 0 {
		s += i18n.Tn("；后面还排着 %d 个提交", n)
	}
	return s
}

// ContinueOngoing 接着做停下来的操作（git rebase --continue 之类，用原来的提交信息，不开编辑器）。
// 还有冲突时不跑，只提示一声。
func (a *App) ContinueOngoing(r *Repo) {
	op := r.Extra.Ongoing
	if op == git.NoOp {
		return
	}
	if n := r.Status.Conflicts; n > 0 {
		a.Toast = i18n.Tn("还有 %d 个文件有冲突", n)
		return
	}
	a.runStep(r, op.ContinueLabel(), op.ContinueArgs())
}

// SkipOngoing 跳过停在的这个提交（git rebase --skip 之类），先问：它的改动不会进结果。合并没有跳过。
func (a *App) SkipOngoing(r *Repo) {
	op := r.Extra.Ongoing
	args := op.SkipArgs()
	if args == nil {
		return
	}
	what := r.Extra.Progress.Subject
	if what == "" {
		what = git.Short(r.Extra.Progress.Head)
	}
	label := op.SkipLabel()
	a.Ask(i18n.Tf("跳过 %s 停在的这个提交？", r.Name), i18n.Tf("它（%s）的改动不会进结果；想找回要翻 reflog。", what),
		label, func() { a.runStep(r, label, args) })
}

// AbortOngoing 中止进行到一半的合并 / 变基 / 拣选 / 还原（git merge --abort 之类），先问一声：
// 解决到一半的冲突会丢掉。
func (a *App) AbortOngoing(r *Repo) {
	op := r.Extra.Ongoing
	if op == git.NoOp {
		return
	}
	label := op.AbortLabel()
	a.AskDanger(i18n.Tf("中止 %s 里进行到一半的%s？", r.Name, op.String()),
		i18n.Tf("仓库会回到开始%s之前的样子，已经解决的冲突和这中间改的文件都会丢掉，不能撤销。", op.String()),
		i18n.T("中止"), func() { a.RunOp(r, label, op.AbortArgs()...) })
}

// runStep 排继续 / 跳过：变基、拣选好几个时 git 做完这一步又停在下一个冲突上会返回非零，
// 那不算失败，输出栏照记、提示"又停在下一处"；真失败了照常弹，git 说这个提交没有改动了就补一句用"跳过"。
func (a *App) runStep(r *Repo, label string, args []string) {
	o := &Op{Label: label, Cmd: "$ git " + strings.Join(args, " "), args: args}
	gitDir := r.Extra.GitDir
	o.done = func(out string, err error) {
		switch {
		case errors.Is(err, git.ErrCanceled):
			a.Log(r, o.Cmd, canceledText(out, label, nil), true)
			a.Toast = i18n.Tf("已取消：%s", label)
		case err != nil && strings.Contains(out, "CONFLICT ("):
			a.Log(r, o.Cmd, logText(out, nil), false)
			a.Toast = i18n.T("继续了，又停在下一处冲突")
		case err != nil:
			a.Log(r, o.Cmd, logText(out, err), true)
			a.Fail(i18n.Tf("%s失败", label), skipHint(args, err))
		default:
			a.Log(r, o.Cmd, logText(out, nil), false)
			a.Toast = i18n.Tf("%s完成", label)
			if gitDir != "" && git.ReadOngoing(gitDir) != git.NoOp { // 停在了不是冲突的地方（edit、exec）；只看几个文件在不在
				a.Toast = i18n.T("继续了，又停在下一处")
			}
		}
	}
	a.enqueue(r, o)
}

// skipHint 给"继续"失败的提示补一句：git 说这个提交解决完没有改动了（和那边一样），该点的是"跳过"。合并没有跳过，不补。
func skipHint(args []string, err error) error {
	if len(args) < 2 || args[1] != "--continue" || args[0] == "merge" {
		return err
	}
	s := err.Error()
	if strings.Contains(s, "No changes") || strings.Contains(s, "nothing to commit") || strings.Contains(s, "is now empty") {
		return errors.New(s + "\n\n" + i18n.T("这个提交的改动已经都在了的话，用“跳过”。"))
	}
	return err
}
