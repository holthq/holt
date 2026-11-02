package app

import (
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/globs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是 files.exclude：三处的模式并在一起——.code-workspace 的 settings、每个仓库的 .vscode/settings.json、
// 设置"文件树"页的"隐藏的文件"——按 VS Code 的规则从仓库根算起（globs.MatchRoot：不带 **/ 的只从根起，目录合了底下整棵都藏）。
// LoadFiles 在 Files.Done 之前去掉，增量刷新加进来的也去掉，所以文件树、⌘P、全文搜索、# 都看不到；Git 栏的改动照常列。

// ExcludeDelay 是设置里的"隐藏的文件"停手多久才重读文件树（打字时每个字都重读太费）。
var ExcludeDelay = 600 * time.Millisecond

// excludeBase 是这个窗口里每个仓库都用的模式：工作区文件的加上设置里的。
func (a *App) excludeBase() []string {
	return slices.Concat(a.wsExclude, globs.Split(a.St.Settings.Exclude))
}

// repoExclude 读仓库 root 的 .vscode/settings.json（读不了、写坏了当没有），并上 base，编成一组规则；
// key 是排好序、去了重的模式，比较规则变了没有。会碰磁盘，在后台调。
func repoExclude(root string, base []string) (globs.Set, string) {
	fs, _ := workspace.LoadFolderSettings(root)
	pats := slices.Concat(base, fs.Exclude)
	for i := range pats {
		pats[i] = strings.TrimSpace(pats[i])
	}
	pats = slices.DeleteFunc(pats, func(p string) bool { return p == "" })
	slices.Sort(pats)
	pats = slices.Compact(pats)
	set, _ := globs.CompileList(pats)
	return set, strings.Join(pats, "\x00")
}

// loadWsSettings 读工作区文件的 settings（打开的是文件夹、读不了的当没有）；改了返回 true。
func (a *App) loadWsSettings() bool {
	info, err := os.Stat(a.WsPath)
	if a.WsPath == "" || err != nil || info.IsDir() {
		changed := a.wsExclude != nil
		a.wsExclude, a.wsMod = nil, time.Time{}
		return changed
	}
	if info.ModTime().Equal(a.wsMod) {
		return false
	}
	a.wsMod = info.ModTime()
	s, _ := workspace.LoadSettings(a.WsPath)
	if slices.Equal(s.Exclude, a.wsExclude) {
		return false
	}
	a.wsExclude = s.Exclude
	return true
}

// checkWsSettings 看一眼工作区文件改了没有（它多半不在哪个仓库里，没有监听）：切回窗口、⌘R 时调，files.exclude 变了就重读文件树。
func (a *App) checkWsSettings() {
	if a.loadWsSettings() {
		a.reloadAllFiles()
	}
}

// reloadExclude 是设置改了：“隐藏的文件”变了的话停手 ExcludeDelay 之后重读文件树。
func (a *App) reloadExclude() {
	if a.excludeOn == a.St.Settings.Exclude {
		return
	}
	if a.excludeTimer != nil {
		a.excludeTimer.Stop()
	}
	up := a.Update
	a.excludeTimer = time.AfterFunc(ExcludeDelay, func() {
		up(func() {
			if a.excludeOn != a.St.Settings.Exclude {
				a.excludeOn = a.St.Settings.Exclude
				a.reloadAllFiles()
			}
		})
	})
}

// reloadAllFiles 重读所有读过（或正在读）的仓库的文件列表。
func (a *App) reloadAllFiles() {
	for _, r := range a.Repos {
		if r.Files.Loaded || r.Files.Loading {
			a.LoadFiles(r)
		}
	}
}

// RepoTip 是文件树仓库行的悬停提示：按 files.exclude 藏了几个；没藏为空。
func (a *App) RepoTip(r *Repo) string {
	if r.Excluded == 0 {
		return ""
	}
	return i18n.Tn("按 files.exclude 藏了 %d 个", r.Excluded)
}

// touchesSettings 是这次增量刷新要看的地方里有 .vscode/settings.json。
func (h fileHints) touchesSettings() bool {
	const p = ".vscode/settings.json"
	return slices.Contains(h.files, p) || slices.ContainsFunc(h.dirs, func(d string) bool { return files.Under(p, d) })
}
