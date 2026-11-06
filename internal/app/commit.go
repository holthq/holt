package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是提交框的几样：修改上一次提交（amend）、↑ / ↓ 翻以前写过的提交信息，和中止进行到一半的
// 合并 / 变基。

// MaxCommitMsgs 是提交信息历史最多记几条（state.json 的 "commitMsgs"，所有仓库共用，最近的在前）。
const MaxCommitMsgs = 30

// SetAmend 勾上 / 取消提交框的"修改上一次提交"。勾上时在后台读上一次提交的信息（只读的 git log），
// 框里空着或还是勾上前那句时换成它；取消时框里还是那句就放回勾上前写的。
func (a *App) SetAmend(r *Repo, on bool) {
	r.Amend = on
	if !on {
		if r.CommitMsg == r.amendMsg {
			r.CommitMsg = r.amendKeep
		}
		r.amendKeep, r.amendMsg = "", ""
		return
	}
	r.amendKeep, r.CommitOpen = r.CommitMsg, true
	up := a.Update
	go func() {
		msg := git.LastMessage(r.Root)
		up(func() {
			if !r.Amend || r.CommitMsg != r.amendKeep {
				return
			}
			r.amendMsg, r.CommitMsg = msg, msg
		})
	}()
}

// ShowCommit 打开侧栏的 Git、展开这个仓库段、滚到它的提交框，下一帧焦点给它（菜单里的"修改上一次提交"）。
// 提交框在段头下面（中间可能隔着横幅、工作树组头），所以把段头滚到顶上：贴顶的段头不会挡住提交框。
func (a *App) ShowCommit(r *Repo) {
	a.ShowSidebar(workspace.ModeGit)
	a.UseRepo(r)
	if !r.GitOpen() {
		r.GitFold = FoldOpen
	}
	r.CommitOpen, r.FocusCommit = true, true
	if row := slices.IndexFunc(a.GitRows(), func(cr ChangeRow) bool { return cr.Kind == RowRepo && cr.Repo == r }); row >= 0 {
		a.ChangeList.ScrollTo(row, ui.Start)
	}
}

// Committing 是这个仓库有一条提交（或修改上一次提交）在跑或排队。
func (r *Repo) Committing() bool {
	return r.Queued(i18n.T("提交")) || r.Queued(i18n.T("修改上一次提交"))
}

// CanCommit 是提交按钮能不能点：有信息，有已暂存的改动（修改上一次提交时可以没有，只改信息；
// 合并的冲突都解决了时也可以没有，提交的是这次合并），没有一条提交已经在跑或排队
// （连点两下不排进两条；前面排着暂存的话，提交排在它后面）。不能点的原因见 CommitWhy。
func (r *Repo) CanCommit() bool { return CommitWhy(r) == "" }

// MergeReady 是合并停在一半、冲突都标成已解决了：没有已暂存的改动也能提交（比如冲突都用了我的，
// git add 之后和 HEAD 一样）。拣选、变基没有这一说，git commit 会说没有可提交的。
func (r *Repo) MergeReady() bool {
	return !r.Amend && r.Extra.Ongoing == git.Merging && r.Status.Conflicts == 0
}

// CommitsAll 是这次提交要先把所有改动暂存（git add -A，含未跟踪的文件）：什么都没暂存、工作区有改动、
// 没有冲突，也不是合并、变基停在一半（那时候暂存什么要用户自己定）。按钮上写着"提交全部 N 个文件"。
func (r *Repo) CommitsAll() bool {
	return !r.Amend && r.Status.Staged == 0 && r.Status.Count > 0 && r.Status.Conflicts == 0 && r.Extra.Ongoing == git.NoOp
}

// Commit 提交这个仓库已暂存的改动（git commit -m），勾着"修改上一次提交"时加 --amend；什么都没暂存时
// 先暂存全部（CommitsAll）。上一次提交已经推送过的先问一声。成功后信息记进提交信息历史；开着自动推送时接着推送。
func (a *App) Commit(r *Repo) { a.commit(r, a.St.Settings.AutoPush) }

// CommitPush 是提交框 ▾ 菜单里的"提交并推送"：不看自动推送的设置，提交成功后推送。
func (a *App) CommitPush(r *Repo) {
	if a.Blocked(CommitPushWhy(r)) {
		return
	}
	a.commit(r, true)
}

func (a *App) commit(r *Repo, push bool) {
	msg := strings.TrimSpace(r.CommitMsg)
	label := i18n.T("提交")
	if r.Amend {
		label = i18n.T("修改上一次提交")
	}
	switch {
	case r.Committing():
		a.Toast = i18n.Tf("%s已经在队列里", label)
		return
	case msg == "":
		a.Fail(label, errors.New(i18n.T("先写一句提交信息")))
		return
	case r.Amend && r.Extra.Head == "":
		a.Fail(label, errors.New(i18n.T("还没有提交，没有可以修改的")))
		return
	case !r.Amend && r.Status.Staged == 0 && !r.MergeReady() && !r.CommitsAll():
		a.Fail(label, errors.New(i18n.T("没有已暂存的改动；先把要提交的文件暂存")))
		return
	}
	all := r.CommitsAll()
	switch { // 前面排着的可能是暂存，刚跑完的暂存状态也还没读回来：等一下，不然把别的文件一起提交了
	case all && (r.Running() != nil || len(r.Queue) > 0):
		a.Toast = i18n.T("有 git 命令在跑")
		return
	case all && r.Loading:
		a.Toast = i18n.T("正在读状态，稍后再提交")
		return
	}
	args := a.commitArgs(msg)
	amend := r.Amend
	if amend {
		args = append(args, "--amend")
	}
	commit := func() {
		a.RunOpThen(r, label, func(string) {
			a.noteCommitMsg(msg)
			r.CommitMsg, r.CommitOpen, r.msgPos = "", false, 0
			if amend {
				r.Amend, r.amendKeep, r.amendMsg = false, "", ""
			}
			if push && !amend {
				a.pushNow(r) // 刚提交完状态还没刷新，领先数是旧的：不查 PushWhy
			}
		}, args...)
	}
	run := commit
	if all { // 暂存失败就不提交（then 只在成功后跑）
		run = func() { a.RunOpThen(r, i18n.T("暂存全部"), func(string) { commit() }, "add", "-A") }
	}
	if amend && r.Extra.HeadPushed() {
		a.AskDanger(i18n.T("修改已经推送过的提交？"),
			i18n.Tf("上一次提交 %s 已经在远程分支里了。修改后它会变成新的提交，要强推才能推上去，已经拉过它的人那边会对不上。",
				git.Short(r.Extra.Head)), i18n.T("修改"), run)
		return
	}
	run()
}

// noteCommitMsg 把刚提交成功的信息记到提交信息历史最前面（去重，最多 MaxCommitMsgs 条）。
func (a *App) noteCommitMsg(msg string) {
	msgs := slices.DeleteFunc(slices.Clone(a.St.CommitMsgs), func(m string) bool { return m == msg })
	a.St.CommitMsgs = append([]string{msg}, msgs[:min(len(msgs), MaxCommitMsgs-1)]...)
	a.SaveState()
}

// StepCommitMsg 是提交框里按 ↑（dir 1，更早的）/ ↓（dir -1，更近的）翻到的信息：框里空着、或还是
// 上次翻到的那条时才翻（写了别的就不动，↑ ↓ 照常移光标）。翻过最近那条再按 ↓ 回到空。
// text 是框里现在的字；ok 为假时不翻。
func (a *App) StepCommitMsg(r *Repo, text string, dir int) (msg string, ok bool) {
	hist := a.St.CommitMsgs
	browsing := r.msgPos > 0 && r.msgPos <= len(hist) && text == hist[r.msgPos-1]
	if !browsing && (text != "" || dir < 0) {
		return "", false
	}
	if !browsing {
		r.msgPos = 0
	}
	next := r.msgPos + dir
	if next > len(hist) || next < 0 {
		return "", false
	}
	r.msgPos = next
	if next == 0 {
		return "", true
	}
	return hist[next-1], true
}
