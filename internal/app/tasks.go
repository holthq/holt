package app

import (
	"slices"
	"strings"

	"github.com/holthq/holt/internal/fuzzy"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/tasks"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是任务：internal/tasks 从仓库根认出来的 npm scripts、make / just 目标、cargo / go 的常用命令（Repo.Tasks，读状态时后台扫）。
// 面板 "!"（⇧⌘B）列所有仓库的，回车在那个仓库里一个叫任务名的终端标签里跑；文件树里 package.json 这些的右键也能跑。

// MaxTreeTasks 是文件树右键 "运行 ▸" 里最多列几个任务。
const MaxTreeTasks = 30

// taskItems 是 "!"：所有仓库的任务（"仓库 · 命令"）模糊匹配 q，最近跑过的靠前（空查询时按跑的先后排在最前）。
func (a *App) taskItems(q string) ([]PaletteItem, string) {
	hint := i18n.T("找 package.json 的 scripts、Makefile / justfile 的目标、cargo / go 的常用命令")
	hist := a.St.TaskHistory
	rank := func(r *Repo, t tasks.Task) int {
		return slices.IndexFunc(hist, func(h workspace.TaskRun) bool { return h.Repo == r.Root && h.Cmd == t.Cmd })
	}
	pat := fuzzy.New(q)
	type hit struct {
		item  PaletteItem
		score int
		rank  int
	}
	var hs []hit
	total := 0
	for _, r := range a.Repos {
		for _, t := range r.Tasks {
			total++
			text := r.Name + " · " + t.Cmd
			sc, hits, ok := pat.Match(text, len(r.Name)+len(" · "))
			if !ok {
				continue
			}
			k := rank(r, t)
			if k >= 0 {
				sc += recentBonus * (len(hist) - k) / len(hist)
			}
			hs = append(hs, hit{PaletteItem{Kind: ItemTask, Text: text, Name: len(r.Name) + len(" · "), Hits: hits, Detail: t.Source,
				Recent: k >= 0, repo: r, task: t}, sc, k})
		}
	}
	switch {
	case total == 0:
		return nil, i18n.T("这些仓库里没认出任务") + " · " + hint
	case len(hs) == 0:
		return nil, i18n.T("没有匹配的任务")
	}
	if pat.Empty() {
		slices.SortStableFunc(hs, func(x, y hit) int {
			switch {
			case x.rank >= 0 && y.rank >= 0:
				return x.rank - y.rank
			case x.rank >= 0:
				return -1
			case y.rank >= 0:
				return 1
			}
			return 0
		})
	} else {
		slices.SortStableFunc(hs, func(x, y hit) int { return y.score - x.score })
	}
	items := make([]PaletteItem, 0, min(len(hs), maxPaletteItems))
	for _, h := range hs[:min(len(hs), maxPaletteItems)] {
		items = append(items, h.item)
	}
	if q == "" {
		return items, hint
	}
	return items, ""
}

// RunTask 在仓库 r 里跑任务 t：有叫任务名、前台没在跑命令的终端标签就用它，不然在最后新开一个（名字是任务名）；
// 在仓库根跑，切到这个仓库、打开底栏、键盘给它，记进跑过的任务。shell 没启动的先启动、出了提示符再敲。
func (a *App) RunTask(r *Repo, t tasks.Task) {
	if t.Cmd == "" {
		return
	}
	a.initTerms(r)
	at := -1
	for i, tab := range r.Terms {
		if tab.Name != t.Name {
			continue
		}
		if job := tab.Current().Job(); job == "" || job == "?" {
			at = i
			break
		}
	}
	if at < 0 {
		at = len(r.Terms)
		a.addTermTab(r, at, t.Name, "")
	}
	r.TermSel = at
	a.noteTask(r, t)
	a.termInput(r, t.Cmd, true, false)
	a.SaveState()
}

// noteTask 把跑过的任务记到最前面（去重，最多 workspace.MaxTaskHistory 条）。
func (a *App) noteTask(r *Repo, t tasks.Task) {
	run := workspace.TaskRun{Repo: r.Root, Cmd: t.Cmd}
	rest := slices.DeleteFunc(slices.Clone(a.St.TaskHistory), func(h workspace.TaskRun) bool { return h == run })
	a.St.TaskHistory = append([]workspace.TaskRun{run}, rest[:min(len(rest), workspace.MaxTaskHistory-1)]...)
}

// TasksOf 是仓库 r 里从文件 rel（仓库内路径）认出来的任务，最多 MaxTreeTasks 个（文件树右键 "运行 ▸"）；
// 不在仓库根、不是认任务的文件为空。
func (a *App) TasksOf(r *Repo, rel string) []tasks.Task {
	if r == nil || strings.Contains(rel, "/") {
		return nil
	}
	var out []tasks.Task
	for _, t := range r.Tasks {
		if t.Source == rel {
			out = append(out, t)
			if len(out) == MaxTreeTasks {
				break
			}
		}
	}
	return out
}
