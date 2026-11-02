package ui

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// wsMenu 是标题栏工作区按钮的菜单。
// 换工作区的在上面：打开文件夹 / 工作区文件、最近的工作区（直接列出来，点了换过去）、清除最近工作区。
func wsMenu(a *app.App, m *wui.Menu) {
	if shortcut(m.Item(i18n.T("打开文件夹…")), "file.openFolder").Chosen() {
		a.ChooseFolder()
	}
	if m.Item(i18n.T("打开工作区文件…")).Chosen() {
		a.ChooseWsFile()
	}
	if shortcut(m.Item(i18n.T("在新窗口中打开…")), "file.openFolderNewWindow").Chosen() {
		a.ChooseFolderNewWindow()
	}
	m.Separator()
	m.Item(i18n.T("最近的工作区")).Disabled(true)
	for _, p := range a.RecentWorkspaces() {
		if m.Item("    " + app.WsLabel(p)).Chosen() {
			a.OpenWorkspace(p)
		}
	}
	if m.Item(i18n.T("清除最近工作区")).Disabled(len(a.St.RecentWs) == 0).Chosen() {
		a.ClearRecentWs()
	}
	m.Separator()
	none := a.WsPath == "" // 空工作区
	if shortcut(m.Item(i18n.T("添加仓库…")), "repo.add").Disabled(none).Chosen() {
		a.ChooseRepoDir()
	}
	if m.Item(i18n.T("克隆仓库…")).Chosen() { // 空工作区也能：克隆完当工作区打开
		a.OpenClone()
	}
	if m.Item(i18n.T("显示移除的工作区仓库")).Disabled(none || len(a.St.Hidden) == 0).Chosen() {
		a.ShowAllRepos()
	}
	if m.Item(i18n.T("刷新所有仓库")).Disabled(len(a.Repos) == 0).Chosen() {
		a.Exec("git.refresh")
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示工作区文件")).Disabled(none).Chosen() {
		a.InFinder(a.WsPath)
	}
	if m.Item(i18n.T("复制工作区文件路径")).Disabled(none).Chosen() {
		copyText(a.WsPath)
	}
}

// gitMenu 是标题栏"更多 git 操作"菜单，和菜单栏的 Git 菜单一样。
func gitMenu(a *app.App, m *wui.Menu, r *app.Repo) {
	if r.Running() != nil || len(r.Queue) > 0 {
		queueMenu(a, m, r)
		m.Separator()
	}
	if m.Item(i18n.T("抓取（fetch --prune）")).Chosen() {
		a.Fetch(r)
	}
	if m.Item(i18n.T("拉取")).Disabled(app.Upstream(r) == "").Chosen() {
		a.Pull(r)
	}
	if m.Item(i18n.T("推送")).Chosen() {
		a.Push(r)
	}
	if why := app.PushForceWhy(r); m.Item(withWhy(i18n.T("强制推送（--force-with-lease）…"), why)).Disabled(why != "").Chosen() {
		a.PushForce(r)
	}
	if m.Item(i18n.T("同步（拉取再推送）")).Chosen() {
		a.Sync(r)
	}
	m.Separator()
	if m.Item(i18n.T("暂存全部")).Disabled(r.Status.Conflicts+r.Status.Unstaged+r.Status.Untracked == 0).Chosen() {
		a.StageAll(r)
	}
	if m.Item(i18n.T("取消全部暂存")).Disabled(r.Status.Staged == 0).Chosen() {
		a.UnstageAll(r)
	}
	if m.Item(i18n.T("丢弃所有未暂存改动…")).Disabled(r.Status.Unstaged == 0).Chosen() {
		a.DiscardAll(r)
	}
	m.Separator()
	if m.Item(i18n.T("贮藏改动（stash）")).Disabled(r.Status.Staged+r.Status.Unstaged == 0).Chosen() {
		a.Stash(r)
	}
	if m.Item(i18n.T("弹出贮藏（stash pop）")).Disabled(len(r.Extra.Stashes) == 0).Chosen() {
		a.StashPop(r)
	}
	if w := a.WebOf(r); w.OK() {
		m.Separator()
		if m.Item(i18n.Tf("在 %s 打开仓库", w.Site())).Chosen() {
			a.OpenWeb(w.Base)
		}
	}
	m.Separator()
	if m.Item(i18n.T("新建分支…")).Chosen() {
		a.Branch = app.BranchDialog{Open: true}
	}
	if m.Item(i18n.T("运行 git 命令…")).Chosen() {
		a.OpenPalette(">git ")
	}
}

// branchMenu 是分支按钮的菜单：本地分支（最多 30 个）、远程分支子菜单（最多 60 个）、标签子菜单（最多 60 个）、
// 合并 / 变基到、新建分支、改名 / 发布当前分支、删除分支 / 远程分支。
func branchMenu(a *app.App, m *wui.Menu, r *app.Repo) {
	local, remote := 0, 0
	for _, b := range r.Branches {
		if b.Remote {
			remote++
			continue
		}
		local++
		if local > 30 {
			continue
		}
		label := b.Name
		if b.Upstream != "" {
			label += "  → " + b.Upstream
		}
		elsewhere := b.CheckedOutElsewhere(r.Root)
		if elsewhere { // 在别的工作树里检出着，git switch 会拒绝
			label = b.Name + " · " + i18n.Tf("在 %s 检出", filepath.Base(b.Worktree))
		}
		if m.Item(label).Checked(b.Current).Disabled(b.Current || elsewhere).Chosen() {
			a.SwitchBranch(r, b)
		}
	}
	if local > 30 {
		m.Item(app.KeyHint("git.command", "…还有 %s 个本地分支（%s 运行 git branch 看全部）", "…还有 %s 个本地分支（运行 git branch 看全部）",
			strconv.Itoa(local-30))).Disabled(true)
	}
	if local == 0 {
		m.Item(i18n.T("没有本地分支")).Disabled(true)
	}
	if remote > 0 {
		m.Separator()
		m.Submenu(i18n.T("远程分支"), func(m *wui.Menu) {
			n := 0
			for _, b := range r.Branches {
				if !b.Remote {
					continue
				}
				n++
				if n > 60 {
					m.Item(i18n.Tf("…还有 %s 个", strconv.Itoa(remote-60))).Disabled(true)
					break
				}
				if m.Item(b.Name).Chosen() {
					a.SwitchBranch(r, b)
				}
			}
		})
	}
	if remote == 0 {
		m.Separator()
	}
	m.Submenu(i18n.T("标签"), func(m *wui.Menu) { tagItems(a, m, r) })
	m.Separator()
	why := app.RefWhy(r)
	m.Submenu(withWhy(i18n.T("合并到当前分支"), why), func(m *wui.Menu) {
		refItems(m, r, func(b git.Branch) { a.Merge(r, b) })
	}).Disabled(why != "")
	m.Submenu(withWhy(i18n.T("把当前分支变基到"), why), func(m *wui.Menu) {
		refItems(m, r, func(b git.Branch) { a.RebaseOnto(r, b) })
	}).Disabled(why != "")
	m.Separator()
	if m.Item(i18n.T("新建分支…")).Chosen() {
		a.Branch = app.BranchDialog{Open: true}
	}
	cur, onBranch := app.CurrentBranch(r)
	renameWhy := ""
	if !onBranch {
		renameWhy = i18n.T("HEAD 不在分支上")
	}
	if m.Item(withWhy(i18n.T("重命名当前分支…"), renameWhy)).Disabled(!onBranch).Chosen() {
		a.OpenRenameBranch(r)
	}
	if onBranch && cur.Upstream == "" && app.HasRemote(r, "origin") && m.Item(i18n.T("发布到 origin（推送并设置上游）")).Chosen() {
		a.Push(r)
	}
	m.Submenu(i18n.T("删除分支"), func(m *wui.Menu) { deleteBranchItems(a, m, r) })
	if remote > 0 {
		m.Submenu(i18n.T("删除远程分支"), func(m *wui.Menu) {
			n := 0
			for _, b := range r.Branches {
				if !b.Remote {
					continue
				}
				if n++; n > 60 {
					m.Item(i18n.Tf("…还有 %s 个", strconv.Itoa(remote-60))).Disabled(true)
					break
				}
				if m.Item(b.Name).Chosen() {
					a.DeleteRemoteBranch(r, b) // 它先问
				}
			}
		})
	}
}

// deleteBranchItems 是分支菜单"删除分支"子菜单的内容：当前分支以外的本地分支（最多 30 个），
// 在别的工作树里检出着的灰着带原因；选了先问（app.DeleteBranch 数过提交再问）。
func deleteBranchItems(a *app.App, m *wui.Menu, r *app.Repo) {
	n := 0
	for _, b := range r.Branches {
		if b.Remote || b.Current {
			continue
		}
		if n++; n > 30 {
			continue
		}
		why := app.DeleteBranchWhy(r, b)
		if m.Item(withWhy(b.Name, why)).Disabled(why != "").Chosen() {
			a.DeleteBranch(r, b)
		}
	}
	if n > 30 {
		m.Item(i18n.Tf("…还有 %s 个", strconv.Itoa(n-30))).Disabled(true)
	}
	if n == 0 {
		m.Item(i18n.T("没有别的本地分支")).Disabled(true)
	}
}

// withWhy 把灰着的原因括在菜单标签后面（原生菜单项没有悬停说明）；why 为空原样。
func withWhy(label, why string) string {
	if why == "" {
		return label
	}
	label = strings.TrimSuffix(label, "…")
	if i18n.Lang() == "en" {
		return label + " (" + why + ")"
	}
	return label + "（" + why + "）"
}

// refItems 是"合并到当前分支 / 把当前分支变基到"子菜单的内容：别的本地分支（最多 30 个）、分隔、远程分支
// （最多 60 个），当前分支不列；选了一个就 pick 它。
func refItems(m *wui.Menu, r *app.Repo, pick func(b git.Branch)) {
	local, remote := 0, 0
	for _, b := range r.Branches {
		if b.Current {
			continue
		}
		if b.Remote {
			remote++
			continue
		}
		local++
		if local <= 30 && m.Item(b.Name).Chosen() {
			pick(b)
		}
	}
	if local > 30 {
		m.Item(i18n.Tf("…还有 %s 个", strconv.Itoa(local-30))).Disabled(true)
	}
	if local == 0 {
		m.Item(i18n.T("没有别的本地分支")).Disabled(true)
	}
	if remote == 0 {
		return
	}
	m.Separator()
	n := 0
	for _, b := range r.Branches {
		if !b.Remote {
			continue
		}
		if n++; n > 60 {
			m.Item(i18n.Tf("…还有 %s 个", strconv.Itoa(remote-60))).Disabled(true)
			break
		}
		if m.Item(b.Name).Chosen() {
			pick(b)
		}
	}
}

// tagItems 是分支菜单"标签"子菜单的内容：新的在前（最多 60 个），"v0.9.0 · 3 天前"，选一个右栏看它往前的历史；
// 底下新建标签、推送标签、删除标签。
func tagItems(a *app.App, m *wui.Menu, r *app.Repo) {
	for i, t := range r.Tags {
		if i == 60 {
			more := strconv.Itoa(len(r.Tags) - 60)
			if len(r.Tags) == git.MaxTags { // 只读了最新的 MaxTags 个
				more += "+"
			}
			m.Item(i18n.Tf("…还有 %s 个", more)).Disabled(true)
			break
		}
		label := t.Name
		if ago := git.Ago(t.Date); ago != "" {
			label += " · " + ago
		}
		if m.Item(label).Checked(r.ViewingTag(t.Name)).Chosen() {
			a.ShowTagHistory(r, t)
		}
	}
	if len(r.Tags) == 0 {
		m.Item(i18n.T("没有标签")).Disabled(true)
	}
	m.Separator()
	if m.Item(i18n.T("新建标签…")).Disabled(r.Extra.Head == "").Chosen() {
		a.OpenNewTag(r, "")
	}
	rm := app.TagRemote(r)
	pushWhy := ""
	switch {
	case rm == "":
		pushWhy = i18n.T("没有远程")
	case len(r.Tags) == 0:
		pushWhy = i18n.T("没有标签")
	}
	push := i18n.T("推送标签")
	if rm != "" {
		push = i18n.Tf("推送标签到 %s", rm)
	}
	m.Submenu(withWhy(push, pushWhy), func(m *wui.Menu) {
		tagList(m, r, func(t git.Tag) { a.PushTag(r, t.Name) })
		m.Separator()
		if m.Item(i18n.T("全部（--tags）")).Chosen() {
			a.PushTag(r, "")
		}
	}).Disabled(pushWhy != "")
	m.Submenu(i18n.T("删除标签"), func(m *wui.Menu) {
		tagList(m, r, func(t git.Tag) { a.OpenDeleteTag(r, t) })
	}).Disabled(len(r.Tags) == 0)
}

// tagList 是"推送标签 / 删除标签"子菜单里的标签名（新的在前，最多 60 个），选了一个就 pick 它。
func tagList(m *wui.Menu, r *app.Repo, pick func(t git.Tag)) {
	for i, t := range r.Tags {
		if i == 60 {
			m.Item(i18n.Tf("…还有 %s 个", strconv.Itoa(len(r.Tags)-60))).Disabled(true)
			break
		}
		if m.Item(t.Name).Chosen() {
			pick(t)
		}
	}
}

// remoteItems 是仓库菜单"远程"子菜单的内容：每个远程一段，"origin  地址"（复制地址）、"抓取 origin"，
// 推送地址不一样时再一行灰的"推送：地址"，再是改名 / 删除；最底下"添加远程…"。
func remoteItems(a *app.App, m *wui.Menu, r *app.Repo) {
	for i, rm := range r.Extra.Remotes {
		if i > 0 {
			m.Separator()
		}
		if m.Item(rm.Name + "  " + rm.URL).Chosen() {
			copyText(rm.URL)
			a.Toast = i18n.Tf("已复制 %s 的地址", rm.Name)
		}
		if m.Item(i18n.Tf("抓取 %s", rm.Name)).Chosen() {
			a.FetchRemote(r, rm.Name)
		}
		if rm.Push != "" {
			m.Item(i18n.Tf("推送：%s", rm.Push)).Disabled(true)
		}
		if m.Item(i18n.Tf("把 %s 改名…", rm.Name)).Chosen() {
			a.OpenRenameRemote(r, rm.Name)
		}
		if m.Item(i18n.Tf("删除 %s…", rm.Name)).Chosen() {
			a.DeleteRemote(r, rm.Name) // 它先问
		}
	}
	if len(r.Extra.Remotes) == 0 {
		m.Item(i18n.T("没有远程")).Disabled(true)
	}
	m.Separator()
	if m.Item(i18n.T("添加远程…")).Chosen() {
		a.OpenAddRemote(r)
	}
}

// repoMenu 是仓库行的右键菜单（文件树、Git 栏、历史）。tree 是文件树里的：多了展开 / 收起那组和粘贴。
func repoMenu(a *app.App, m *wui.Menu, r *app.Repo, tree bool) {
	refresh := i18n.T("刷新 Git 状态")
	if r.Plain {
		refresh = i18n.T("刷新")
	}
	if m.Item(refresh).Disabled(r.Loading).Chosen() {
		a.RefreshRepo(r, app.WhyManual)
	}
	if m.Item(i18n.T("在此打开终端")).Chosen() {
		a.NewTerminal(r, r.Root)
	}
	if m.Item(i18n.T("在此仓库里搜索")).Chosen() {
		a.SearchIn(r, "")
	}
	if m.Item(i18n.T("重新启动终端")).Chosen() {
		a.RestartTerm(r)
	}
	if r.Extra.Head != "" && m.Item(i18n.T("修改上一次提交…")).Checked(r.Amend).Chosen() {
		a.SetAmend(r, !r.Amend)
		a.ShowCommit(r)
	}
	if r.Extra.Head != "" {
		if why := app.UndoWhy(r); m.Item(withWhy(i18n.T("撤销上次提交"), why)).Disabled(why != "").Chosen() {
			a.UndoCommit(r)
		}
	}
	if op := r.Extra.Ongoing; op != git.NoOp {
		if m.Item(op.ContinueLabel()).Disabled(r.Status.Conflicts > 0).Chosen() {
			a.ContinueOngoing(r)
		}
		if op.SkipArgs() != nil && m.Item(op.SkipLabel()+"…").Chosen() {
			a.SkipOngoing(r)
		}
		if m.Item(op.AbortLabel() + "…").Chosen() {
			a.AbortOngoing(r)
		}
	}
	m.Separator()
	if tree {
		if m.Item(i18n.T("展开有改动的目录")).Disabled(!app.HasChanges(r)).Chosen() {
			a.ExpandChanged(r)
		}
		if m.Item(i18n.T("收起其他仓库")).Disabled(len(a.Repos) < 2).Chosen() {
			a.CollapseOtherRepos(r)
		}
	}
	// 和拖仓库行一样（MoveRepo）：只在同一组里挪，到头了灰着；⇧F10 打开这个菜单就是键盘走法
	i := slices.Index(a.Repos, r)
	if m.Item(i18n.T("上移")).Disabled(!a.CanMoveRepo(r, i-1)).Chosen() {
		a.MoveReq = &app.RepoMove{Repo: r, To: i - 1}
	}
	if m.Item(i18n.T("下移")).Disabled(!a.CanMoveRepo(r, i+1)).Chosen() {
		a.MoveReq = &app.RepoMove{Repo: r, To: i + 1}
	}
	m.Separator()
	newItems(a, m, r, "")
	if !r.Plain {
		if m.Item(i18n.T("新建工作树…")).Disabled(!r.Loaded || r.Err != nil).Chosen() {
			a.NewWorktree(r)
		}
		if r.Linked && m.Item(i18n.T("打开主仓库")).Disabled(r.MainRoot == "").Chosen() {
			a.OpenMainRepo(r)
		}
		m.Submenu(i18n.T("远程"), func(m *wui.Menu) { remoteItems(a, m, r) })
		m.Submenu(i18n.T("子模块"), func(m *wui.Menu) { submoduleItems(a, m, r) })
		if m.Item(i18n.T("显示被忽略的文件")).Checked(a.St.Settings.ShowIgnored).Chosen() {
			a.ToggleShowIgnored()
		}
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.InFinder(r.Root)
	}
	if w := a.WebOf(r); w.OK() && m.Item(i18n.Tf("在 %s 打开", w.Site())).Chosen() {
		a.OpenWeb(w.Base)
	}
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(r.Root)
	}
	if tree && shortcut(m.Item(i18n.T("粘贴")), "tree.paste").Disabled(len(a.TreeClip) == 0).Chosen() {
		a.PasteInto(r, "")
	}
	m.Separator()
	if m.Item(i18n.T("从列表移除")).Chosen() {
		a.RemoveReq = r
	}
}

// newItems 是右键菜单里的"新建文件… / 新建文件夹…"，建在仓库的 dir 目录下。
func newItems(a *app.App, m *wui.Menu, r *app.Repo, dir string) {
	if shortcut(m.Item(i18n.T("新建文件…")), "file.new").Chosen() {
		a.StartNew(r, dir, false)
	}
	if shortcut(m.Item(i18n.T("新建文件夹…")), "file.newFolder").Chosen() {
		a.StartNew(r, dir, true)
	}
}

// fileMenu 是文件树里文件和目录的右键菜单。分组：打开（目录是展开 / 收起、终端、搜索、历史）| 新建 | Git |
// 访达、默认应用、网页 | 路径 | 复制粘贴 | 重命名、废纸篓。指向目录的链接能展开，别的当文件（重命名、删掉的是链接）；
// 链接里面的行见 linkedMenu。
func fileMenu(a *app.App, m *wui.Menu, r *app.Repo, row files.Row) {
	if row.Sub != 0 {
		submoduleMenu(a, m, r, row.Path)
		return
	}
	if row.Linked {
		linkedMenu(a, m, r, row)
		return
	}
	tr := app.TreeRow{Repo: r, Row: row}
	full := filepath.Join(r.Root, filepath.FromSlash(row.Path))
	isLink := tr.DirLink()
	asDir := row.Dir && !isLink
	if row.Dir {
		label := i18n.T("展开")
		if row.Open {
			label = i18n.T("收起")
		}
		if m.Item(label).Chosen() {
			a.ToggleDir(r, row.Path)
		}
		if m.Item(i18n.T("收起所有目录")).Chosen() {
			a.CollapseAll()
		}
		if isLink {
			if m.Item(i18n.T("查看文件历史")).Disabled(row.Code == '?' || r.Plain).Chosen() {
				a.ShowFileHistory(r, row.Path, false)
			}
		} else if m.Item(i18n.T("查看目录历史")).Disabled(r.Plain).Chosen() {
			a.ShowFileHistory(r, row.Path, true)
		}
		if m.Item(i18n.T("在此打开终端")).Chosen() {
			a.NewTerminal(r, full)
		}
		if !isLink {
			if m.Item(i18n.T("在此搜索")).Chosen() {
				a.SearchIn(r, row.Path)
			}
			m.Separator()
			newItems(a, m, r, row.Path)
		}
	} else {
		if m.Item(i18n.T("打开")).Chosen() {
			a.OpenFile(r, row.Path, true)
		}
		if shortcut(m.Item(i18n.T("在侧边打开")), "tree.openSide").Chosen() {
			a.OpenFileSide(r, row.Path)
		}
		if m.Item(i18n.T("查看 diff")).Disabled(row.Code == 0).Chosen() {
			a.ShowDiffFor(r, row.Path)
		}
		if m.Item(i18n.T("查看文件历史")).Disabled(row.Code == '?' || r.Plain).Chosen() {
			a.ShowFileHistory(r, row.Path, false)
		}
		localHistItem(a, m, filepath.Join(r.Root, filepath.FromSlash(row.Path)))
		if tks := a.TasksOf(r, row.Path); len(tks) > 0 { // 仓库根的 package.json / Makefile / justfile……：挑一个在终端里跑
			m.Submenu(i18n.T("运行"), func(m *wui.Menu) {
				for _, tk := range tks {
					if m.Item(tk.Cmd).Chosen() {
						a.RunTask(r, tk)
					}
				}
			})
		}
		m.Separator()
		compareItems(a, m, r, row.Path)
		m.Separator()
		newItems(a, m, r, files.DirOf(row.Path))
	}
	if !r.Plain {
		m.Separator()
		if !asDir {
			for _, ch := range r.Status.Changes {
				if ch.Path == row.Path && ch.Kind != git.InCommit {
					stageItems(a, m, r, ch, true)
				}
			}
		}
		if m.Item(i18n.T("添加到 .gitignore")).Disabled(!app.CanIgnore([]app.TreeRow{tr})).Chosen() {
			a.IgnoreRows([]app.TreeRow{tr})
		}
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.InFinder(full)
	}
	if !row.Dir && m.Item(i18n.T("用默认应用打开")).Chosen() {
		openWithDefault(a, full)
	}
	if w := a.WebOf(r); w.OK() && m.Item(i18n.Tf("在 %s 打开", w.Site())).Disabled(row.Code == '?' || row.Code == 'A').Chosen() {
		a.OpenWeb(w.File(r.WebRef(), row.Path, asDir))
	}
	m.Separator()
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(full)
	}
	if m.Item(i18n.T("复制仓库内路径")).Chosen() {
		copyText(row.Path)
	}
	m.Separator()
	clipItems(a, m, tr, []app.TreeRow{tr})
	m.Separator()
	if shortcut(m.Item(i18n.T("重命名…")), "tree.rename").Chosen() {
		a.StartRename(r, row.Path, asDir)
	}
	if shortcut(m.Item(i18n.T("移到废纸篓…")), "tree.trash").Chosen() {
		a.TrashPath(r, row.Path, asDir)
	}
}

// linkedMenu 是指向目录的链接里面的行的右键菜单：只能展开 / 打开、在访达中显示、复制路径
// （它们在链接指向的地方：不新建、不重命名、不删，也没有 git 状态）。
func linkedMenu(a *app.App, m *wui.Menu, r *app.Repo, row files.Row) {
	full := filepath.Join(r.Root, filepath.FromSlash(row.Path))
	if row.Dir {
		label := i18n.T("展开")
		if row.Open {
			label = i18n.T("收起")
		}
		if m.Item(label).Chosen() {
			a.ToggleDir(r, row.Path)
		}
	} else {
		if m.Item(i18n.T("打开")).Chosen() {
			a.OpenFile(r, row.Path, true)
		}
		if shortcut(m.Item(i18n.T("在侧边打开")), "tree.openSide").Chosen() {
			a.OpenFileSide(r, row.Path)
		}
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.InFinder(full)
	}
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(full)
	}
}

// clipItems 是复制 / 粘贴 / 创建副本：复制、创建副本对 rows，粘贴到 at 那行（PasteDir），⌘C 记下的没有时灰着。
func clipItems(a *app.App, m *wui.Menu, at app.TreeRow, rows []app.TreeRow) {
	copyLabel, dupLabel := i18n.T("复制"), i18n.T("创建副本")
	if len(rows) > 1 {
		copyLabel, dupLabel = i18n.Tn("复制 %d 项", len(rows)), i18n.Tn("创建 %d 个副本", len(rows))
	}
	if shortcut(m.Item(copyLabel), "tree.copy").Chosen() {
		a.CopyRows(rows)
	}
	if shortcut(m.Item(i18n.T("粘贴")), "tree.paste").Disabled(len(a.TreeClip) == 0).Chosen() {
		a.PasteInto(at.Repo, app.PasteDir(at))
	}
	if shortcut(m.Item(dupLabel), "tree.duplicate").Chosen() {
		a.DuplicateRows(rows)
	}
}

// compareItems 是文件行右键里比较的三项：选择以进行比较、与已选的比较（选过别的文件才有）、与剪贴板比较
// （建菜单时不读剪贴板：读系统剪贴板要回主线程，空着的点了再说）。
func compareItems(a *app.App, m *wui.Menu, r *app.Repo, rel string) {
	if m.Item(i18n.T("选择以进行比较")).Chosen() {
		a.SelectForCompare(r, rel)
	}
	if a.CanCompareWithSel(r, rel) && m.Item(i18n.Tf("与 %s 比较", a.CompareSelName())).Chosen() {
		a.CompareWithSel(r, rel)
	}
	if m.Item(i18n.T("与剪贴板比较")).Chosen() {
		a.CompareClipboard(r, rel)
	}
}

// chosenMenu 是文件树里选了多行、右键其中一行（at）时的菜单：作用于选中的全部（可以跨仓库），粘贴到 at 那行。
// 看历史、重命名只对一项，灰着；选中里有仓库行时不能移到废纸篓。选中里有链接目录里面的行时只能打开、
// 在访达中显示、复制路径（同 linkedMenu）。
func chosenMenu(a *app.App, m *wui.Menu, at app.TreeRow, rows []app.TreeRow) {
	n, nf, nc := len(rows), app.Files(rows), app.Changed(rows)
	if m.Item(i18n.Tn("打开 %d 个文件", nf)).Disabled(nf == 0).Chosen() {
		a.OpenRows(rows)
	}
	if shortcut(m.Item(i18n.Tn("在侧边打开 %d 个文件", nf)), "tree.openSide").Disabled(nf == 0).Chosen() {
		a.OpenRowsSide(rows)
	}
	if app.CanCompareRows(rows) && m.Item(i18n.T("比较这两个文件")).Chosen() {
		a.CompareRows(rows)
	}
	if app.HasLinked(rows) {
		m.Separator()
		if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
			a.RevealRows(rows)
		}
		if m.Item(i18n.Tn("复制 %d 个路径", n)).Chosen() {
			copyText(app.RowPaths(rows, false))
		}
		return
	}
	if m.Item(i18n.Tn("查看 %d 个文件的 diff", nc)).Disabled(nc == 0).Chosen() {
		a.DiffRows(rows)
	}
	m.Item(i18n.T("查看文件历史")).Disabled(true)
	m.Separator()
	if m.Item(i18n.T("添加到 .gitignore")).Disabled(!app.CanIgnore(rows)).Chosen() {
		a.IgnoreRows(rows)
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.RevealRows(rows)
	}
	m.Separator()
	if m.Item(i18n.Tn("复制 %d 个路径", n)).Chosen() {
		copyText(app.RowPaths(rows, false))
	}
	if m.Item(i18n.Tn("复制 %d 个仓库内路径", n)).Chosen() {
		copyText(app.RowPaths(rows, true))
	}
	m.Separator()
	clipItems(a, m, at, rows)
	m.Separator()
	shortcut(m.Item(i18n.T("重命名…")), "tree.rename").Disabled(true)
	if shortcut(m.Item(i18n.Tn("把 %d 项移到废纸篓…", n)), "tree.trash").Disabled(app.HasRoot(rows)).Chosen() {
		a.TrashRows(rows)
	}
}

// chosenChangeMenu 是 Git 栏里选了好几项变更、右键其中一项时的菜单：作用于选中的全部（可以跨仓库）；冲突不暂存、不丢弃。
func chosenChangeMenu(a *app.App, m *wui.Menu, rows []app.ChangeRow) {
	n := len(rows)
	ns, nu := app.CountChanges(rows, app.CanStage), app.CountChanges(rows, app.CanUnstage)
	if m.Item(i18n.Tn("查看 %d 个 Diff", n)).Shortcut(0, wui.KeyEnter).Chosen() {
		a.OpenDiffs(rows)
	}
	m.Separator()
	if m.Item(i18n.Tn("暂存 %d 项", ns)).Disabled(ns == 0).Chosen() {
		a.StageChanges(rows)
	}
	if m.Item(i18n.Tn("取消暂存 %d 项", nu)).Disabled(nu == 0).Chosen() {
		a.UnstageChanges(rows)
	}
	if m.Item(i18n.Tn("丢弃 %d 项改动…", ns)).Disabled(ns == 0).Chosen() {
		a.DiscardChanges(rows)
	}
	m.Separator()
	if m.Item(i18n.T("在 Finder 中显示")).Disabled(n > 10).Chosen() { // 每个目录一个访达窗口，多了只是一串窗口
		a.RevealChanges(rows)
	}
	m.Separator()
	if m.Item(i18n.Tn("复制 %d 个路径", n)).Chosen() {
		copyText(app.ChangePaths(rows, false))
	}
	if m.Item(i18n.Tn("复制 %d 个仓库内路径", n)).Chosen() {
		copyText(app.ChangePaths(rows, true))
	}
}

// stageItems 是一项变更的暂存那几项：Git 栏的变更行和文件树的文件行共用（一样的命令、一样的确认）。
// tree 是文件树里的：没跟踪的文件只有暂存，删掉它用"移到废纸篓"。
func stageItems(a *app.App, m *wui.Menu, r *app.Repo, ch git.Change, tree bool) {
	switch ch.Kind {
	case git.Staged:
		if m.Item(i18n.T("取消暂存")).Chosen() {
			a.Unstage(r, ch.Path)
		}
	case git.Unstaged:
		if m.Item(i18n.T("暂存")).Chosen() {
			a.Stage(r, ch.Path)
		}
		if m.Item(i18n.T("丢弃改动…")).Chosen() {
			a.Discard(r, ch)
		}
	case git.Untracked:
		if m.Item(i18n.T("暂存（git add）")).Chosen() {
			a.Stage(r, ch.Path)
		}
		if !tree && m.Item(i18n.T("删除文件…")).Chosen() {
			a.Discard(r, ch)
		}
	case git.Conflict:
		if m.Item(i18n.T("标记为已解决")).Chosen() {
			a.MarkResolvedPath(r, ch.Path)
		}
	}
}

// changeMenu 是 Git 栏变更行、右栏提交文件行的右键菜单。
func changeMenu(a *app.App, m *wui.Menu, r *app.Repo, ch git.Change) {
	full := filepath.Join(r.Root, filepath.FromSlash(ch.Path))
	if ch.Kind == git.InCommit {
		if m.Item(i18n.T("查看此版本")).Disabled(ch.Code == 'D').Chosen() {
			a.OpenVersion(r, ch)
		}
		if m.Item(i18n.T("在编辑器中打开当前文件")).Chosen() {
			a.OpenFile(r, ch.Path, true)
		}
	} else if ch.Kind == git.Conflict {
		if m.Item(i18n.T("在编辑器中解决冲突")).Chosen() {
			a.OpenConflict(r, ch, true)
		}
		if m.Item(i18n.T("查看 Diff（HEAD ↔ 工作区）")).Chosen() {
			a.OpenDiff(r, ch, true)
		}
		if m.Item(i18n.T("两边的 diff")).Chosen() {
			a.OpenStagesDiff(r, ch.Path)
		}
	} else if m.Item(i18n.T("在编辑器中打开")).Disabled(ch.Code == 'D').Chosen() {
		a.OpenFile(r, ch.Path, true)
	}
	if m.Item(i18n.T("查看文件历史")).Disabled(ch.Kind == git.Untracked).Chosen() {
		a.ShowFileHistory(r, ch.Path, false)
	}
	m.Separator()
	stageItems(a, m, r, ch, false)
	if ch.Kind != git.InCommit {
		m.Separator()
	}
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.InFinder(full)
	}
	if m.Item(i18n.T("用默认应用打开")).Disabled(ch.Code == 'D').Chosen() {
		openWithDefault(a, full)
	}
	if w := a.WebOf(r); w.OK() {
		u, ok := w.File(r.WebRef(), ch.Path, false), ch.Kind != git.Untracked && ch.Code != 'A'
		if ch.Kind == git.InCommit { // 这次提交里的那个版本（删掉的看父提交里的）
			ref := ch.Commit
			if ch.Code == 'D' {
				ref = ch.Parent
			}
			u, ok = w.File(ref, ch.Path, false), ref != "" && r.Extra.Pushed(ch.Commit) && !r.InStash(ch)
		}
		if m.Item(i18n.Tf("在 %s 打开", w.Site())).Disabled(!ok).Chosen() {
			a.OpenWeb(u)
		}
	}
	m.Separator()
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(full)
	}
	if m.Item(i18n.T("复制仓库内路径")).Chosen() {
		copyText(ch.Path)
	}
}

// commitMenu 是右栏提交行的右键菜单。
func commitMenu(a *app.App, m *wui.Menu, r *app.Repo, cm git.Commit) {
	// 菜单里的操作都落在这个提交的仓库上，并把当前仓库切到它（"所有仓库"视图里它不一定是当前的）
	chosen := func(it *wui.MenuItem) bool {
		if !it.Chosen() {
			return false
		}
		a.UseRepo(r)
		return true
	}
	if chosen(m.Item(i18n.T("复制提交哈希"))) {
		copyText(cm.Hash)
	}
	if chosen(m.Item(i18n.Tf("复制短哈希 %s", cm.Short))) {
		copyText(cm.Short)
	}
	if w := a.WebOf(r); w.OK() && chosen(m.Item(i18n.Tf("在 %s 打开", w.Site())).Disabled(!r.Extra.Pushed(cm.Hash))) {
		a.OpenWeb(w.Commit(cm.Hash))
	}
	if chosen(m.Item(i18n.T("复制提交信息"))) {
		msg := cm.Subject
		if cm.Body != "" {
			msg += "\n\n" + cm.Body
		}
		copyText(msg)
	}
	m.Separator()
	if chosen(m.Item(i18n.T("从这里新建分支…"))) {
		a.Branch = app.BranchDialog{Open: true, From: cm.Short, Repo: r}
	}
	if chosen(m.Item(i18n.T("在这里打标签…"))) {
		a.OpenNewTag(r, cm.Short)
	}
	if chosen(m.Item(i18n.T("检出这个提交（分离 HEAD）…"))) {
		a.Checkout(r, cm) // 它先问
	}
	if why := app.PickWhy(r, cm); chosen(m.Item(withWhy(i18n.T("拣选这个提交"), why)).Disabled(why != "")) {
		a.CherryPick(r, cm)
	}
	if why := app.RevertWhy(r, cm); chosen(m.Item(withWhy(i18n.T("还原这个提交…"), why)).Disabled(why != "")) {
		a.Revert(r, cm) // 它先问
	}
	why := app.ResetWhy(r, cm)
	m.Submenu(withWhy(i18n.T("重置到这个提交"), why), func(m *wui.Menu) {
		for _, mode := range []app.ResetMode{app.ResetSoft, app.ResetMixed, app.ResetHard} {
			if chosen(m.Item(mode.Label())) {
				a.ResetTo(r, cm, mode) // 三档都先问
			}
		}
	}).Disabled(why != "")
	if a.RebaseView(r) { // 只在当前分支的历史里（不筛、不是所有分支 / 所有仓库）
		m.Separator()
		_, why := a.RebaseRange(r, cm)
		label := i18n.T("从这里开始交互式变基…")
		if why != "" {
			label = i18n.Tf("从这里开始交互式变基（%s）", why) // 原生菜单项没有悬停说明，原因括在后面
		}
		if chosen(m.Item(label).Disabled(why != "")) {
			a.OpenRebase(r, cm)
		}
	}
	m.Separator()
	from := r.Hist.CompareFrom
	if chosen(m.Item(i18n.T("标记为比较起点")).Checked(from != nil && from.Hash == cm.Hash)) {
		a.MarkCompareFrom(r, cm)
	}
	if from != nil && from.Hash != cm.Hash && chosen(m.Item(i18n.Tf("与 %s 比较", from.Short))) {
		a.CompareCommits(r, *from, cm)
	}
}

// stashMenu 是 Git 栏里一条贮藏的右键菜单。
func stashMenu(a *app.App, m *wui.Menu, r *app.Repo, s git.Stash) {
	look := i18n.T("查看改动")
	if r.StashOpen == s.Hash {
		look = i18n.T("收起")
	}
	if m.Item(look).Chosen() {
		a.ToggleStash(r, s)
	}
	m.Separator()
	if m.Item(i18n.T("应用贮藏")).Chosen() {
		a.StashApply(r, s)
	}
	if m.Item(i18n.T("弹出贮藏")).Chosen() {
		a.StashPopRef(r, s)
	}
	m.Separator()
	if m.Item(i18n.T("丢弃贮藏…")).Chosen() {
		a.StashDrop(r, s)
	}
	m.Separator()
	if m.Item(i18n.Tf("复制名字 %s", s.Ref)).Chosen() {
		copyText(s.Ref)
	}
}

// tabMenu 是标签的右键菜单。
func tabMenu(a *app.App, m *wui.Menu, d *docs.Doc) {
	g := a.GroupOf(d)
	ds, _ := a.GroupDocs(g)
	if m.Item(i18n.T("关闭")).Chosen() {
		a.CloseDoc(d)
	}
	// 关其他 / 已保存的 / 全部都只在这个标签那一组里：先把键盘换过去
	if m.Item(i18n.T("关闭其他")).Disabled(len(ds) < 2).Chosen() {
		a.FocusGroup(g)
		a.CloseOthers(d)
	}
	if shortcut(m.Item(i18n.T("关闭已保存的")), "file.closeSaved").Chosen() {
		a.FocusGroup(g)
		a.CloseSaved()
	}
	if m.Item(i18n.T("关闭全部")).Chosen() {
		a.FocusGroup(g)
		a.CloseOthers(nil)
	}
	if r, tt := a.TermTabOf(d); tt != nil { // 编辑器区的终端：同底栏终端标签的那几项，加挪回底栏
		m.Separator()
		if m.Item(i18n.T("重命名…")).Chosen() {
			a.StartRenameTerm(r, tt)
		}
		if m.Item(i18n.T("挪回底栏")).Chosen() {
			a.MoveTermToBottom(d)
		}
		p := tt.Current()
		if m.Item(i18n.T("把终端内容开成标签")).Disabled(p.Term == nil).Chosen() {
			a.OpenPaneText(r, tt, p)
		}
		if m.Item(i18n.T("复制全部输出")).Disabled(p.Term == nil).Chosen() {
			a.CopyPaneOutput(p)
		}
	}
	m.Separator()
	splitItems(a, m, d)
	if m.Item(i18n.T("固定标签")).Disabled(d.Pinned).Chosen() {
		d.Pinned = true
	}
	if d.Kind == docs.File && a.RepoOf(d) != nil {
		if m.Item(i18n.T("在文件树里显示")).Chosen() {
			a.RevealReq = d
		}
	}
	if d.Kind == docs.File && d.Snapshot == 0 {
		localHistItem(a, m, d.Abs)
	}
	pathItems(a, m, d)
}

// localHistItem 是标签、编辑器、文件树文件行右键里的"查看本地历史"：仓库外、未跟踪的也能用。
func localHistItem(a *app.App, m *wui.Menu, abs string) {
	if abs != "" && m.Item(i18n.T("查看本地历史")).Disabled(a.LocalHist() == nil).Chosen() {
		a.ShowLocalHistory(abs)
	}
}

// docMenu 是编辑器右键菜单里追加的几项。
func docMenu(a *app.App, m *wui.Menu, d *docs.Doc) {
	mergeMenu(a, m, d)
	m.Separator()
	if m.Item(i18n.T("保存")).Disabled(!d.Modified()).Chosen() {
		a.Save(d)
	}
	if d.Kind == docs.File && d.Editable() { // 这种语言没有命令时照样能点，提示里说为什么
		it := m.Item(i18n.T("格式化文档")).Disabled(!app.CanFormat(d) || d.Formatting)
		if a.CurrentDoc() == d {
			shortcut(it, "editor.format")
		}
		if it.Chosen() {
			a.Format(d)
		}
	}
	if d.Kind == docs.File && d.Abs != "" { // 检查：没有命令时照样能点，提示里说
		it := m.Item(i18n.T("检查")).Disabled(!app.CanLint(d) || a.Linting(d))
		if a.CurrentDoc() == d {
			shortcut(it, "lint.run")
		}
		if it.Chosen() {
			a.Lint(d)
		}
	}
	if a.CanBlame(d) { // 两种 blame 并排；装订线按标签，行内的所有文件标签一起（设置 Git 页那一项）
		m.Separator()
		it := m.Item(i18n.T("显示 blame 装订线")).Checked(d.ShowBlame)
		if a.CurrentDoc() == d {
			shortcut(it, "view.blame")
		}
		if it.Chosen() {
			a.ToggleDocBlame(d)
		}
		if d.Kind == docs.File && m.Item(i18n.T("显示行内 blame")).Checked(a.St.Settings.InlineBlame).Chosen() {
			a.ToggleInlineBlame()
		}
	}
	if d.Kind == docs.File && d.Snapshot == 0 {
		localHistItem(a, m, d.Abs)
	}
	splitItems(a, m, d)
	pathItems(a, m, d)
}

// splitItems 是标签和编辑器右键菜单里的"拆分"子菜单：把 d 搬进它那组某一边新分出的一组，键盘跟过去。
// 放不下的方向灰；那组只有它一个（哪边都分不了）整项灰。⌘\ / ⌥⌘\（改过的键照改后的）只在 d 是键盘所在组的当前标签时标出来，
// 不然按了分的是别的标签。文件标签后面跟一项“在另一组里再开一份”（镜像，⇧⌘\）。
func splitItems(a *app.App, m *wui.Menu, d *docs.Doc) {
	cur := a.CurrentDoc() == d
	m.Submenu(i18n.T("拆分"), func(m *wui.Menu) {
		item := func(label string, s app.Side, id string) {
			it := m.Item(label).Disabled(!a.CanSplit(d, s))
			if cur && id != "" {
				shortcut(it, id)
			}
			if it.Chosen() {
				a.SplitDoc(d, s)
			}
		}
		item(i18n.T("向右拆分"), app.SideRight, "view.splitRight")
		item(i18n.T("向下拆分"), app.SideBottom, "view.splitDown")
		item(i18n.T("向左拆分"), app.SideLeft, "")
		item(i18n.T("向上拆分"), app.SideTop, "")
	}).Disabled(!a.CanSplit(d, app.SideRight) && !a.CanSplit(d, app.SideBottom))
	if d.Kind == docs.File { // 镜像：同一个文件在另一组里再开一份
		it := m.Item(i18n.T("在另一组里再开一份")).Disabled(!a.CanMirror(d))
		if cur {
			shortcut(it, "view.openInOtherGroup")
		}
		if it.Chosen() {
			a.OpenMirror(d)
		}
	}
}

func pathItems(a *app.App, m *wui.Menu, d *docs.Doc) {
	if d.Abs == "" {
		return
	}
	if m.Item(i18n.T("在 Finder 中显示")).Chosen() {
		a.InFinder(d.Abs)
	}
	if u, ok := a.WebLines(d); u != "" && m.Item(i18n.Tf("在 %s 打开这几行", a.WebOf(a.RepoOf(d)).Site())).Disabled(!ok).Chosen() {
		a.OpenWeb(u)
	}
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(d.Abs)
	}
}

// openWithDefault 用默认应用打开 path：经 a.ShowPath（测试里换掉），打不开的弹应用自己的提示。
func openWithDefault(a *app.App, path string) {
	if err := a.ShowPath(path); err != nil {
		a.Fail(i18n.T("打不开文件"), err)
	}
}
