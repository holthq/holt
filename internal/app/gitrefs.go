package app

import (
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是分支和标签的 Git 日常操作：分支改名 / 删除 / 删远程分支 / 发布，标签新建 / 推送 / 删除，
// 远程添加 / 改名 / 删除。都排进仓库的队列；删东西的先问，问话里带数出来的提交数。

// RefDlgKind 是 RefDialog 在做哪一样。
type RefDlgKind int

const (
	DlgRenameBranch RefDlgKind = iota // 名字框，默认填当前分支名
	DlgNewTag                         // 名字 + 信息（空 = 轻量标签），打在 From 上
	DlgDeleteTag                      // 只有一句问话和"同时删除远程上的"勾选
	DlgRenameRemote                   // 名字框，默认填旧名
	DlgAddRemote                      // 名字 + 地址
)

// RefDialog 是分支改名、新建 / 删除标签、远程改名 / 添加共用的窗口内对话框（照"新建分支"那个做）。
type RefDialog struct {
	Open bool
	Kind RefDlgKind
	Repo *Repo
	Old  string // 改名前的名字；删标签时是标签名
	From string // 新建标签打在哪个提交（短哈希；空 = HEAD）
	Name string
	Text string // 标签信息 / 远程地址
	Also bool   // 删标签时同时删远程上的

	Prefilled bool // 添加远程时名字已经填了 origin（光标放地址框）
}

// TagRemote 是推送 / 删除标签用的远程：有 origin 用 origin，没有就用第一个；一个远程都没有为空。
func TagRemote(r *Repo) string {
	rs := r.Extra.Remotes
	if HasRemote(r, "origin") {
		return "origin"
	}
	if len(rs) > 0 {
		return rs[0].Name
	}
	return ""
}

// splitRemote 把 "origin/feature" 拆成远程名和分支名（远程名按已有的远程认，名字里带 / 的也对）。
func splitRemote(r *Repo, name string) (remote, branch string) {
	for _, x := range r.Extra.Remotes {
		if strings.HasPrefix(name, x.Name+"/") && len(x.Name) > len(remote) {
			remote = x.Name
		}
	}
	if remote == "" {
		remote, branch, _ = strings.Cut(name, "/")
		return remote, branch
	}
	return remote, name[len(remote)+1:]
}

// OpenRenameBranch 打开"重命名当前分支…"，名字框里是当前名。
func (a *App) OpenRenameBranch(r *Repo) {
	if a.Blocked(RenameBranchWhy(r)) {
		return
	}
	b, _ := CurrentBranch(r)
	a.RefDlg = RefDialog{Open: true, Kind: DlgRenameBranch, Repo: r, Old: b.Name, Name: b.Name}
}

// RenameBranch 把分支 old 改名为 name（git branch -m）。有上游时上游跟着取消：远程上的分支名没变，
// 留着的话 git push 会因为名字对不上而拒绝；取消后推送按钮走"推送并设置上游"，在远程上新建 name。
func (a *App) RenameBranch(r *Repo, old, name string) {
	name = strings.TrimSpace(name)
	if name == "" || name == old {
		return
	}
	var up string
	if i := slices.IndexFunc(r.Branches, func(b git.Branch) bool { return !b.Remote && b.Name == old }); i >= 0 {
		up = r.Branches[i].Upstream
	}
	a.RunOpThen(r, i18n.Tf("把分支 %s 改名为 %s", old, name), func(string) {
		if up == "" {
			return
		}
		rm, _ := splitRemote(r, up)
		a.RunOpThen(r, i18n.Tf("取消 %s 的上游", name), func(string) {
			a.Toast = i18n.Tf("远程分支名没变，推送时会新建 %s", rm+"/"+name)
		}, "branch", "--unset-upstream", name)
	}, "branch", "-m", old, name)
}

// DeleteBranch 删本地分支：先在后台数它上面不在 HEAD 里的提交、其中没推送过的，问过再 git branch -d；
// git 说没合并时再问一次强制删除（worktrees.go 的 deleteBranch）。
func (a *App) DeleteBranch(r *Repo, b git.Branch) {
	if b.Remote || a.Blocked(DeleteBranchWhy(r, b)) {
		return
	}
	ctx, root, up := r.ctx, r.Root, a.Update
	ref := "refs/heads/" + b.Name
	go func() {
		n, err := git.Count(ctx, root, ref, "--not", "HEAD")
		local := 0
		if err == nil {
			local, err = git.Count(ctx, root, ref, "--not", "HEAD", "--remotes")
		}
		up(func() {
			if r.gone() {
				return
			}
			if err != nil {
				a.Fail(i18n.T("数提交失败"), err)
				return
			}
			var msg string
			switch {
			case n == 0:
				msg = i18n.Tf("%s 上的提交都已经在当前分支里了。", b.Name)
			case local == 0:
				msg = i18n.Tn("%s 上有 %d 个提交不在当前分支里，都已经推送过。", b.Name, n)
			default:
				msg = i18n.Tn("%s 上有 %d 个提交不在当前分支里，其中 %d 个没推送过，删掉后只能从 reflog 找回。", b.Name, n, local)
			}
			if b.Upstream != "" {
				msg += i18n.Tf("远程上的 %s 不会删。", b.Upstream)
			}
			a.AskDanger(i18n.Tf("删除分支 %s？", b.Name), msg, i18n.T("删除分支"), func() { a.deleteBranch(r, b.Name, false) })
		})
	}()
}

// DeleteRemoteBranch 删远程分支（git push 远程 --delete 分支）：先数它上面不在 HEAD 里的提交，问过再删。
func (a *App) DeleteRemoteBranch(r *Repo, b git.Branch) {
	if !b.Remote {
		return
	}
	remote, branch := splitRemote(r, b.Name)
	ctx, root, up := r.ctx, r.Root, a.Update
	go func() {
		n, err := git.Count(ctx, root, "refs/remotes/"+b.Name, "--not", "HEAD")
		up(func() {
			if r.gone() {
				return
			}
			if err != nil {
				a.Fail(i18n.T("数提交失败"), err)
				return
			}
			msg := i18n.Tf("会在 %s 上删掉分支 %s，别人抓取后也就没了。", remote, branch)
			if n > 0 {
				msg += i18n.Tn("上面有 %d 个提交不在当前分支里。", n)
			}
			if i := slices.IndexFunc(r.Branches, func(x git.Branch) bool { return !x.Remote && x.Upstream == b.Name }); i >= 0 {
				msg += i18n.Tf("本地分支 %s 留着，只是没了上游。", r.Branches[i].Name)
			}
			a.AskDanger(i18n.Tf("删除远程分支 %s？", b.Name), msg, i18n.T("删除远程分支"), func() {
				a.RunOp(r, i18n.Tf("删除远程分支 %s", b.Name), "push", remote, "--delete", "refs/heads/"+branch)
			})
		})
	}()
}

// HasRemote 是仓库有叫 name 的远程（"发布到 origin"只在有 origin 时出现，添加远程时查重名）。
func HasRemote(r *Repo, name string) bool {
	return slices.ContainsFunc(r.Extra.Remotes, func(x git.Remote) bool { return x.Name == name })
}

// OpenNewTag 打开"新建标签…"；from 空打在 HEAD，否则是那个提交的短哈希。
func (a *App) OpenNewTag(r *Repo, from string) {
	a.RefDlg = RefDialog{Open: true, Kind: DlgNewTag, Repo: r, From: from}
}

// NewTag 打标签：有信息是附注标签（tag -a -m），没有是轻量的。
func (a *App) NewTag(r *Repo, name, msg, from string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	args := []string{"tag", name}
	if msg = strings.TrimSpace(msg); msg != "" {
		args = []string{"tag", "-a", name, "-m", msg}
	}
	if from != "" {
		args = append(args, from)
	}
	a.RunOp(r, i18n.Tf("新建标签 %s", name), args...)
}

// PushTag 把一个标签推到 TagRemote；name 空推全部（--tags）。
func (a *App) PushTag(r *Repo, name string) {
	if a.Blocked(PushTagWhy(r)) {
		return
	}
	remote := TagRemote(r)
	if name == "" {
		a.RunOp(r, i18n.Tf("推送全部标签到 %s", remote), "push", remote, "--tags")
		return
	}
	a.RunOp(r, i18n.Tf("推送标签 %s", name), "push", remote, "refs/tags/"+name)
}

// OpenDeleteTag 打开删标签的问话（带"同时删除远程上的"勾选，所以不是 Ask）。
func (a *App) OpenDeleteTag(r *Repo, t git.Tag) {
	a.RefDlg = RefDialog{Open: true, Kind: DlgDeleteTag, Repo: r, Old: t.Name}
}

// DeleteTag 删本地标签（tag -d）；remote 为真时成功后再排一条 push 远程 --delete。
func (a *App) DeleteTag(r *Repo, name string, remote bool) {
	rm := TagRemote(r)
	a.RunOpThen(r, i18n.Tf("删除标签 %s", name), func(string) {
		if remote && rm != "" {
			a.RunOp(r, i18n.Tf("删除 %s 上的标签 %s", rm, name), "push", rm, "--delete", "refs/tags/"+name)
		}
	}, "tag", "-d", name)
}

// OpenRenameRemote 打开远程"改名…"。
func (a *App) OpenRenameRemote(r *Repo, name string) {
	a.RefDlg = RefDialog{Open: true, Kind: DlgRenameRemote, Repo: r, Old: name, Name: name}
}

// RenameRemote 远程改名（git remote rename）：远程分支的本地引用和上游设置 git 一起改。
func (a *App) RenameRemote(r *Repo, old, name string) {
	name = strings.TrimSpace(name)
	if name == "" || name == old {
		return
	}
	a.RunOp(r, i18n.Tf("把远程 %s 改名为 %s", old, name), "remote", "rename", old, name)
}

// RemoteRefs 是远程 name 的远程分支在本地的引用有几个（删远程时一起没）。
func RemoteRefs(r *Repo, name string) int {
	n := 0
	for _, b := range r.Branches {
		if b.Remote {
			if rm, _ := splitRemote(r, b.Name); rm == name {
				n++
			}
		}
	}
	return n
}

// DeleteRemote 删远程（git remote remove）：先问，说一起删的远程分支引用有几个、当前分支的上游会不会没。
func (a *App) DeleteRemote(r *Repo, name string) {
	msg := i18n.Tn("远程仓库本身不受影响；本地的 %d 个远程分支引用（%s/…）会一起删。", RemoteRefs(r, name), name)
	if up := Upstream(r); up != "" {
		if rm, _ := splitRemote(r, up); rm == name {
			msg += i18n.Tf("当前分支的上游 %s 也会没有。", up)
		}
	}
	a.AskDanger(i18n.Tf("删除远程 %s？", name), msg, i18n.T("删除远程"), func() {
		a.RunOp(r, i18n.Tf("删除远程 %s", name), "remote", "remove", name)
	})
}

// OpenAddRemote 打开"添加远程…"：还没有 origin 时名字默认填 origin。
func (a *App) OpenAddRemote(r *Repo) {
	d := RefDialog{Open: true, Kind: DlgAddRemote, Repo: r}
	if !HasRemote(r, "origin") {
		d.Name, d.Prefilled = "origin", true
	}
	a.RefDlg = d
}

// AddRemote 添加远程（git remote add），成功后接着抓取它（fetch --prune 名字）。
func (a *App) AddRemote(r *Repo, name, url string) {
	name, url = strings.TrimSpace(name), strings.TrimSpace(url)
	if name == "" || url == "" {
		return
	}
	a.RunOpThen(r, i18n.Tf("添加远程 %s", name), func(string) { a.FetchRemote(r, name) }, "remote", "add", name, url)
}
