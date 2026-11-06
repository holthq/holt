package app

import (
	"path/filepath"
	"slices"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是子模块：文件树里是一行（不展开），双击把它添加为工作区里的仓库（照"所有仓库同时可见"，不在树里另开一棵），
// 没初始化的双击问要不要初始化。初始化 / 更新都是用户点了以后才跑，先问（要联网的说一声）。

// Submodule 是 r 里路径为 p 的子模块。
func (r *Repo) Submodule(p string) (git.Submodule, bool) {
	if i := slices.IndexFunc(r.Extra.Subs, func(s git.Submodule) bool { return s.Path == p }); i >= 0 {
		return r.Extra.Subs[i], true
	}
	return git.Submodule{}, false
}

// subStates 是文件树要的子模块路径 → 状态（files.Tree.SetSubs），没有子模块为 nil。
func subStates(subs []git.Submodule) map[string]byte {
	if len(subs) == 0 {
		return nil
	}
	m := make(map[string]byte, len(subs))
	for _, s := range subs {
		m[s.Path] = s.State
	}
	return m
}

// subDir 是子模块在磁盘上的目录。
func subDir(r *Repo, p string) string { return filepath.Join(r.Root, filepath.FromSlash(p)) }

// OpenSubmodule 是文件树里子模块那一行的双击 / 回车：初始化了的问要不要添加为仓库，没初始化的问要不要初始化并更新。
func (a *App) OpenSubmodule(r *Repo, p string) {
	if s, ok := r.Submodule(p); ok && !s.Inited() {
		a.InitSubmodule(r, p)
		return
	}
	a.AddSubmoduleRepo(r, p)
}

// AddSubmoduleRepo 把子模块添加为工作区里的仓库（只改应用自己的仓库列表）；已经在列表里的直接跳过去。先问。
func (a *App) AddSubmoduleRepo(r *Repo, p string) {
	dir := subDir(r, p)
	// 加进来的仓库根是 git rev-parse 解开了符号链接的（/var → /private/var），按同一个目录比
	if i := slices.IndexFunc(a.Repos, func(x *Repo) bool { return git.SamePath(x.Root, dir) }); i >= 0 {
		a.JumpRepo(i)
		return
	}
	a.Ask(i18n.Tf("把子模块 %s 添加为工作区里的仓库？", p),
		i18n.T("添加以后它和别的仓库一样出现在文件树和 Git 栏里，可以看它里面的文件、改动和历史。"), i18n.T("添加"), func() {
			if err := a.AddRepoDir(dir); err != nil {
				a.Fail(i18n.T("添加子模块失败"), err)
				return
			}
			if a.Current() != nil {
				a.JumpRepo(a.Selected)
			}
		})
}

// SubmoduleWindow 在新窗口里打开子模块（目录当工作区）。
func (a *App) SubmoduleWindow(r *Repo, p string) {
	if s, ok := r.Submodule(p); ok && !s.Inited() {
		a.Toast = i18n.Tf("子模块 %s 还没初始化", p)
		return
	}
	a.openNewWindow(subDir(r, p))
}

// InitSubmodule 初始化并更新一个子模块（git submodule update --init -- p）：从它的地址克隆，检出父仓库记录的那次提交。先问。
func (a *App) InitSubmodule(r *Repo, p string) {
	a.Ask(i18n.Tf("初始化并更新子模块 %s？", p),
		i18n.T("会从 .gitmodules 里的地址克隆它（要联网，可能要一会儿），然后检出父仓库记录的那次提交。"), i18n.T("初始化并更新"), func() {
			a.RunOp(r, i18n.Tf("初始化子模块 %s", p), "submodule", "update", "--init", "--", p)
		})
}

// UpdateSubmodule 把初始化过的子模块检出到父仓库记录的提交（git submodule update -- p）。先问。
func (a *App) UpdateSubmodule(r *Repo, p string) {
	s, _ := r.Submodule(p)
	a.Ask(i18n.Tf("把子模块 %s 更新到父仓库记录的提交？", p),
		i18n.Tf("会在子模块里检出 %s。里面没提交的改动不会丢（挡着检出时 git 会停下），但子模块的 HEAD 会变成分离的。", git.Short(s.Head)),
		i18n.T("更新"), func() {
			a.RunOp(r, i18n.Tf("更新子模块 %s", p), "submodule", "update", "--", p)
		})
}

// InitAllSubmodules 初始化并更新全部子模块，子模块里的子模块也是（git submodule update --init --recursive）。先问。
func (a *App) InitAllSubmodules(r *Repo) {
	if len(r.Extra.Subs) == 0 {
		a.Toast = i18n.T("没有子模块")
		return
	}
	a.Ask(i18n.Tf("初始化并更新 %s 的全部子模块？", r.Name),
		i18n.T("没初始化的会从各自的地址克隆（要联网，可能要一会儿）；全都检出父仓库记录的提交，HEAD 变成分离的，子模块里的子模块也一样。"),
		i18n.T("全部初始化并更新"), func() {
			a.RunOp(r, i18n.T("初始化并更新全部子模块"), "submodule", "update", "--init", "--recursive")
		})
}
