package app

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是 Git 操作"现在为什么不能做"的唯一出处：每种操作一个 XxxWhy，空串 = 能做，不然是一个短语。
// 按"最根本的先"的固定顺序查，只报第一条。界面上原生菜单把它括在标签后面（ui 的 withWhy），行内按钮、
// 标题栏按钮写进悬停提示，命令面板写在灰着的命令后面；点下去时动作自己再查一次（Blocked），
// 不为空就提示原因、不执行——菜单开着的时候状态可能已经变了（监听刷新、另一个 agent 刚推送）。

// Blocked 是"点时再查一次"：why 不空时提示原因、返回真（调用方别再往下做）。
func (a *App) Blocked(why string) bool {
	if why == "" {
		return false
	}
	a.Toast = why
	return true
}

// busyWhy 是拉取 / 推送 / 同步共同的第一条：同一个仓库里拉取或推送在跑或在排队。
func busyWhy(r *Repo) string {
	switch {
	case r.Pulling():
		return i18n.T("正在拉取")
	case r.Pushing():
		return i18n.T("正在推送")
	}
	return ""
}

// upstreamRemote 是上游的远程名（origin/main 的 origin），没有上游为空。
func upstreamRemote(r *Repo) string {
	up := Upstream(r)
	if up == "" {
		return ""
	}
	rm, _ := splitRemote(r, up)
	return rm
}

// FetchWhy 是"抓取"不能做的原因：正在抓取 → 没有远程。
func FetchWhy(r *Repo) string {
	switch {
	case r.Queued(i18n.T("抓取")):
		return i18n.T("正在抓取")
	case len(r.Extra.Remotes) == 0:
		return i18n.T("没有远程")
	}
	return ""
}

// PullWhy 是"拉取"不能做的原因：正在拉取 / 推送 → 有进行中的操作 → 没有上游 → 和上游一致（按上次抓取）。
func PullWhy(r *Repo) string {
	if why := busyWhy(r); why != "" {
		return why
	}
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.Tf("先处理进行中的%s", r.Extra.Ongoing.String())
	case Upstream(r) == "":
		return i18n.T("没有上游")
	}
	return ""
}

// PushWhy 是"推送"不能做的原因：正在拉取 / 推送 → HEAD 不在分支上 → 没有上游又没有 origin
// （有 origin 时不灰，变成"推送并设置上游"）→ 和远端一致 → 落后 / 分叉（先拉取，或强制推送）。
// 领先落后按上次抓取算。
func PushWhy(r *Repo) string {
	if why := busyWhy(r); why != "" {
		return why
	}
	if _, ok := CurrentBranch(r); !ok {
		return i18n.T("HEAD 不在分支上")
	}
	if Upstream(r) == "" {
		if !HasRemote(r, "origin") {
			return i18n.T("没有上游，也没有 origin")
		}
		return ""
	}
	switch st := r.Status; {
	case st.Ahead == 0 && st.Behind == 0:
		return i18n.Tf("和 %s 一致", upstreamRemote(r))
	case st.Ahead == 0:
		return i18n.Tf("落后 %s，没有要推的", strconv.Itoa(st.Behind))
	case st.Behind > 0:
		return i18n.Tf("和 %s 分叉，先拉取或强制推送", upstreamRemote(r))
	}
	return ""
}

// PushForceWhy 是"强制推送"不能做的原因：正在拉取 / 推送 → 没有上游 → 和远端一致。
func PushForceWhy(r *Repo) string {
	if why := busyWhy(r); why != "" {
		return why
	}
	switch {
	case Upstream(r) == "":
		return i18n.T("没有上游")
	case r.Status.Ahead == 0 && r.Status.Behind == 0:
		return i18n.Tf("和 %s 一致", upstreamRemote(r))
	}
	return ""
}

// SyncWhy 是"同步（拉取再推送）"不能做的原因：正在拉取 / 推送 → 有进行中的操作 → 没有上游时同推送
// → 和上游一致。
func SyncWhy(r *Repo) string {
	if why := busyWhy(r); why != "" {
		return why
	}
	if r.Extra.Ongoing != git.NoOp {
		return i18n.Tf("先处理进行中的%s", r.Extra.Ongoing.String())
	}
	if Upstream(r) == "" {
		return PushWhy(r)
	}
	if r.Status.Ahead == 0 && r.Status.Behind == 0 {
		return i18n.Tf("和 %s 一致", upstreamRemote(r))
	}
	return ""
}

// StageAllWhy 是"暂存全部"不能做的原因。
func StageAllWhy(r *Repo) string {
	if r.Status.Conflicts+r.Status.Unstaged+r.Status.Untracked == 0 {
		return i18n.T("没有要暂存的改动")
	}
	return ""
}

// UnstageAllWhy 是"取消全部暂存"不能做的原因。
func UnstageAllWhy(r *Repo) string {
	if r.Status.Staged == 0 {
		return i18n.T("没有已暂存的改动")
	}
	return ""
}

// DiscardAllWhy 是"丢弃所有未暂存改动"不能做的原因。
func DiscardAllWhy(r *Repo) string {
	if r.Status.Unstaged == 0 {
		return i18n.T("没有未暂存的改动")
	}
	return ""
}

// StashWhy 是"贮藏改动"不能做的原因：有进行中的操作 → 没有改动。
func StashWhy(r *Repo) string {
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.Tf("先处理进行中的%s", r.Extra.Ongoing.String())
	case r.Status.Staged+r.Status.Unstaged == 0:
		return i18n.T("没有可贮藏的改动")
	}
	return ""
}

// StashPopWhy 是"弹出贮藏"不能做的原因：有进行中的操作 → 没有贮藏。
func StashPopWhy(r *Repo) string {
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.Tf("先处理进行中的%s", r.Extra.Ongoing.String())
	case len(r.Extra.Stashes) == 0:
		return i18n.T("没有贮藏")
	}
	return ""
}

// RefWhy 是"合并到当前分支 / 把当前分支变基到"整个子菜单不能做的原因：有进行中的操作 → 有冲突 → HEAD 不在分支上。
func RefWhy(r *Repo) string {
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.Tf("先处理进行中的%s", r.Extra.Ongoing.String())
	case r.Status.Conflicts > 0:
		return i18n.Tn("有 %d 处冲突", r.Status.Conflicts)
	}
	if _, ok := CurrentBranch(r); !ok {
		return i18n.T("HEAD 不在分支上")
	}
	return ""
}

// MergeWhy 是把分支 b 合并到当前分支不能做的原因：就是当前分支 → 已经合并 → RefWhy。
// "已经合并"照右栏读进来的历史判断，读到的范围外的算没合并（git merge 会说 Already up to date）。
func MergeWhy(r *Repo, b git.Branch) string {
	switch {
	case b.Current:
		return i18n.T("就是当前分支")
	case b.Hash != "" && headHasPrefix(r, b.Hash):
		return i18n.T("已经合并")
	}
	return RefWhy(r)
}

// RebaseOntoWhy 是把当前分支变基到 b 上不能做的原因：就是当前分支 → RefWhy。
func RebaseOntoWhy(r *Repo, b git.Branch) string {
	if b.Current {
		return i18n.T("就是当前分支")
	}
	return RefWhy(r)
}

// headHasPrefix 是 HEAD 往前的历史里有哈希以 short 开头的提交（分支列表里是短哈希）。
func headHasPrefix(r *Repo, short string) bool {
	for _, c := range r.Hist.Commits {
		if strings.HasPrefix(c.Hash, short) {
			return inHead(r, c.Hash)
		}
	}
	return false
}

// SwitchWhy 是切到分支 b 不能做的原因：就是当前分支 → 在别的工作树里检出着（git switch 会拒绝）。
func SwitchWhy(r *Repo, b git.Branch) string {
	switch {
	case b.Current && !b.Remote:
		return i18n.T("当前分支")
	case b.CheckedOutElsewhere(r.Root):
		return i18n.Tf("在 %s 检出", filepath.Base(b.Worktree))
	}
	return ""
}

// RenameBranchWhy 是"重命名当前分支"不能做的原因。
func RenameBranchWhy(r *Repo) string {
	if _, ok := CurrentBranch(r); !ok {
		return i18n.T("HEAD 不在分支上")
	}
	return ""
}

// DeleteBranchWhy 是"删除分支"子菜单里这个分支不能删的原因：当前分支 → 在别的工作树里检出着。
func DeleteBranchWhy(r *Repo, b git.Branch) string {
	switch {
	case b.Current:
		return i18n.T("当前分支")
	case b.CheckedOutElsewhere(r.Root):
		return i18n.Tf("在 %s 检出", filepath.Base(b.Worktree))
	}
	return ""
}

// PushTagWhy 是"推送标签"不能做的原因：没有远程 → 没有标签。
func PushTagWhy(r *Repo) string {
	switch {
	case TagRemote(r) == "":
		return i18n.T("没有远程")
	case len(r.Tags) == 0:
		return i18n.T("没有标签")
	}
	return ""
}

// RemoveWorktreeWhy 是"移除工作树"不能做的原因：主工作树 → 目录已经没了（用"清理"）→ 仓库里有 git 命令在跑。
// 有未提交的改动、锁着的不灰：对话框里写着有几处改动，git 不肯时再问要不要强制移除。
func RemoveWorktreeWhy(r *Repo, w Worktree) string {
	switch {
	case w.Main:
		return i18n.T("主工作树不能删")
	case w.Prunable:
		return i18n.T("目录已经没了，用“清理”")
	case r.Running() != nil:
		return i18n.T("有 git 命令在跑")
	}
	return ""
}

// UndoWhy 是"撤销上次提交"不能做的原因。
func UndoWhy(r *Repo) string {
	switch {
	case r.Extra.Head == "":
		return i18n.T("还没有提交")
	case r.Extra.HeadRoot:
		return i18n.T("第一个提交，没有上一个")
	case r.Extra.Ongoing != git.NoOp:
		return i18n.T("有进行中的操作")
	case r.Committing():
		return i18n.T("提交在队列里")
	}
	return ""
}

// CommitWhy 是提交框里的提交按钮不能点的原因（CanCommit 是假时），能点是 ""。灰着时写在按钮左边和悬停提示里。
// 什么都没暂存时按钮是"提交全部"（CommitsAll），能不能点看工作区。
func CommitWhy(r *Repo) string {
	st := r.Status
	switch {
	case r.Committing():
		return i18n.T("正在提交")
	case strings.TrimSpace(r.CommitMsg) == "":
		return i18n.T("还没写提交信息")
	case st.Staged > 0 || r.Amend && r.Extra.Head != "" || r.MergeReady():
		return ""
	case st.Count == 0:
		return i18n.T("没有可提交的改动")
	case st.Conflicts > 0:
		return i18n.T("有冲突的文件，先解决")
	case !r.CommitsAll():
		return i18n.T("没有已暂存的改动")
	case r.Running() != nil || len(r.Queue) > 0:
		return i18n.T("有 git 命令在跑")
	}
	return ""
}

// CommitPushWhy 是"提交并推送"不能做的原因：先是不能提交的（CommitWhy），再是提交完推不上去的：
// 修改提交要强推、HEAD 不在分支上、没有地方推、已经落后（领先数要等提交完才算，不看）。
func CommitPushWhy(r *Repo) string {
	if why := CommitWhy(r); why != "" {
		return why
	}
	if why := busyWhy(r); why != "" {
		return why
	}
	if r.Amend {
		return i18n.T("修改后的提交要强推")
	}
	if _, ok := CurrentBranch(r); !ok {
		return i18n.T("HEAD 不在分支上")
	}
	if Upstream(r) == "" && !HasRemote(r, "origin") {
		return i18n.T("没有上游，也没有 origin")
	}
	if n := r.Status.Behind; n > 0 {
		return i18n.Tf("落后 %s，先拉取", strconv.Itoa(n))
	}
	return ""
}

// PickWhy 是历史右键"拣选这个提交"不能做的原因。
func PickWhy(r *Repo, cm git.Commit) string {
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.T("有进行中的操作")
	case cm.Hash == r.Extra.Head:
		return i18n.T("就是 HEAD")
	case len(cm.Parents) > 1:
		return i18n.T("合并提交")
	case inHead(r, cm.Hash):
		return i18n.T("HEAD 里已经有了")
	}
	return ""
}

// RevertWhy 是"还原这个提交…"不能做的原因。合并提交要选按哪一边还原（-m），留给终端。
func RevertWhy(r *Repo, cm git.Commit) string {
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.T("有进行中的操作")
	case len(cm.Parents) > 1:
		return i18n.T("合并提交，到终端里 revert -m")
	}
	return ""
}

// ResetWhy 是"重置到这个提交"整个子菜单不能做的原因。
func ResetWhy(r *Repo, cm git.Commit) string {
	switch {
	case r.Extra.Ongoing != git.NoOp:
		return i18n.T("有进行中的操作")
	case cm.Hash == r.Extra.Head:
		return i18n.T("就是 HEAD")
	}
	return ""
}

// RebaseWhy 是从 cm 开始交互式变基不能做的原因（RebaseRange 的 why）。
func (a *App) RebaseWhy(r *Repo, cm git.Commit) string {
	_, why := a.RebaseRange(r, cm)
	return why
}

// repoWhy 把对当前仓库的 XxxWhy 包成命令的 Why（没有当前仓库时空：Enabled 已经管了）。
func repoWhy(fn func(r *Repo) string) func(a *App) string {
	return func(a *App) string {
		if r := a.Current(); r != nil {
			return fn(r)
		}
		return ""
	}
}
