package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 从外面交进来的路径：命令行参数（`Grove 目录|文件`）、第二个实例转过来的参数（单实例）、
// `open -a Grove 路径` 和拖到 Dock 图标上的（mygo 的 OnOpenFile）。main 把它们交给 OpenArgs。

// Args 挑出命令行参数里的路径：去掉开关（-psn_… 之类）和工作区文件（main 自己用它定工作区）。
func Args(args []string) []string {
	var out []string
	for _, s := range args {
		if s == "" || strings.HasPrefix(s, "-") {
			continue
		}
		out = append(out, s)
	}
	return out
}

// OpenArgs 依次打开 paths（相对路径按 wd 算）。不在列表里的仓库攒起来一起问一次。
func (a *App) OpenArgs(paths []string, wd string) {
	var add []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(wd, p)
		}
		if root := a.openArg(filepath.Clean(p)); root != "" && !slices.Contains(add, root) {
			add = append(add, root)
		}
	}
	a.askAdd(add)
}

// OpenArg 打开一个绝对路径：文件开标签（仓库里的按仓库开，别的按外部文件开）；.code-workspace 换到那个工作区
// （先问没保存的，见 OpenWorkspace）；目录在 git 仓库里时，仓库已在列表里就跳到它那一段，不在就先问要不要
// 加进这个工作区（只记进 state.json，不改 .code-workspace）；不在仓库里的目录当普通文件夹，一样先问；
// 空工作区里目录直接当工作区打开。找不到的路径给个提示。
func (a *App) OpenArg(p string) { a.askAdd([]string{a.openArg(p)}) }

// openArg 是 OpenArg 除了问之外的部分；返回要问着加进来的仓库根，没有为 ""。
func (a *App) openArg(p string) string {
	info, err := os.Stat(p)
	switch {
	case err != nil:
		a.Toast = i18n.Tf("找不到 %s", p)
	case !info.IsDir() && filepath.Ext(p) == ".code-workspace":
		if p != a.WsPath {
			a.OpenWorkspace(p)
		}
	case !info.IsDir():
		a.OpenPath(p)
	case a.WsPath == "": // 空工作区没有可加进去的：目录直接当工作区打开
		a.OpenWorkspace(p)
	default:
		root, err := git.Toplevel(p)
		if err != nil && !git.NotRepo(p) {
			a.Toast = err.Error()
			return ""
		}
		if err != nil { // 普通文件夹；列表里普通文件夹的子目录就是跳到那个文件夹
			if r, _ := a.linkRepo(p); r != nil && r.Plain {
				a.JumpRepo(slices.Index(a.Repos, r))
				return ""
			}
			root = p
		}
		if r, rel := a.linkRepo(root); r != nil && rel == "" { // git 报的根是解开符号链接的（/var → /private/var）
			a.JumpRepo(slices.Index(a.Repos, r))
			return ""
		}
		return root
	}
	return ""
}

// askAdd 问要不要把 roots 这几个仓库加进这个工作区（一次问完，几个一起加）。
func (a *App) askAdd(roots []string) {
	roots = slices.DeleteFunc(roots, func(s string) bool { return s == "" })
	if len(roots) == 0 {
		return
	}
	title, msg := i18n.T("添加仓库"), i18n.Tf("把 %s 加进这个工作区？", roots[0])
	if len(roots) > 1 {
		title, msg = i18n.Tn("添加 %d 个仓库", len(roots)), i18n.Tn("把这 %d 个仓库加进这个工作区？\n%s", len(roots), strings.Join(roots, "\n"))
	}
	a.Ask(title, msg+"\n"+i18n.T("只记在 Grove 自己的设置里，不改工作区文件。"), i18n.T("添加"), func() {
		var errs []string
		for _, root := range roots {
			if err := a.AddRepoDir(root); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			a.Fail(i18n.T("添加仓库失败"), errors.New(strings.Join(errs, "\n")))
		}
	})
}

// Greet 是启动时状态栏的提示：上次崩了说一声（关于窗口里能打开崩溃日志）；否则换了版本（含第一次用）报一下版本号。
func (a *App) Greet(version string) {
	switch {
	case a.LastCrash != "":
		a.Toast = i18n.Tf("上次异常退出了，崩溃日志 %s 在\"关于 Grove\"里能打开", filepath.Base(a.LastCrash))
	case a.Toast != "": // 启动时已经有要说的（最近的工作区都打不开了）
	case a.St.AppVersion == "":
		a.Toast = i18n.Tf("欢迎使用 Grove %s", version)
	case a.St.AppVersion != version:
		a.Toast = i18n.Tf("已更新到 Grove %s", version)
	}
	if a.St.AppVersion != version {
		a.St.AppVersion = version
		a.SaveState()
	}
}
