package app

import (
	"slices"
	"strings"
	"time"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是右栏历史的筛选：搜索框（提交信息、作者、哈希，查询语法见 histquery.go）、"所有分支"和"提交图"开关（记进 state.json）、
// 文件历史（右键文件"查看文件历史"，只看动过它的提交，按仓库记）。筛选变了就按新的条件重读。

// HistDebounce 是历史搜索框停止敲字后多久重读。
const HistDebounce = 250 * time.Millisecond

// logQuery 是读这个仓库的历史时用的筛选，日期还没认（hq.resolveDates 在后台填）。
func (a *App) logQuery(r *Repo) (git.LogQuery, HistQuery) {
	h := &r.Hist
	q := git.LogQuery{All: a.St.HistBranches, Path: h.Path, Follow: h.Path != "" && !h.PathDir, Rev: h.Ref}
	hq := ParseHistQuery(a.HistQuery)
	hq.apply(&q)
	return q, hq
}

// HistFiltered 是这个仓库的历史现在有筛选（搜索或文件历史；所有分支不算，它是另一种看法）。
func (a *App) HistFiltered(r *Repo) bool {
	return strings.TrimSpace(a.HistQuery) != "" || r.Hist.Path != ""
}

// HistQueryErr 是搜索框下面那行小字：查询里看不懂的日期（写错的 yyyy-mm-dd 马上有；git 也认不出的读完才有），没有为空。
func (a *App) HistQueryErr() string {
	raw := strings.TrimSpace(a.HistQuery)
	if raw == "" {
		return ""
	}
	bad := ParseHistQuery(raw).Bad
	for _, r := range a.Repos {
		if h := &r.Hist; h.query == raw {
			for _, d := range h.BadDates {
				if !slices.Contains(bad, d) {
					bad = append(bad, d)
				}
			}
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return i18n.Tf("日期看不懂：%s", i18n.List(bad))
}

// HistQueryChanged 在历史搜索框的字变了之后调：停 HistDebounce 再按新的字重读读过的历史。
func (a *App) HistQueryChanged() {
	a.histTick++
	if a.histTimer != nil {
		a.histTimer.Stop()
	}
	tick := a.histTick
	up := a.Update
	a.histTimer = time.AfterFunc(HistDebounce, func() {
		up(func() {
			if a.histTick == tick {
				a.ReloadHistories()
			}
		})
	})
}

// ReloadHistories 按现在的筛选重读每个读过（或正在读）历史的仓库；回车、开关变了时直接调。
func (a *App) ReloadHistories() {
	a.histTick++
	if a.histTimer != nil {
		a.histTimer.Stop()
		a.histTimer = nil
	}
	q := strings.TrimSpace(a.HistQuery)
	for _, r := range a.Repos {
		if h := &r.Hist; (h.Loaded || h.Loading) && (h.query != q || h.all != a.St.HistBranches || h.path != h.Path || h.ref != h.Ref) {
			a.LoadHistory(r, true)
		}
	}
}

// ToggleHistBranches 切换右栏历史的"所有分支"（本地、远程分支和标签），记进 state.json。
func (a *App) ToggleHistBranches() {
	a.St.HistBranches = !a.St.HistBranches
	a.SaveState()
	a.ReloadHistories()
}

// ToggleHistGraph 开关右栏历史的提交图，记进 state.json。图是读历史时就算好的，不用重读。
func (a *App) ToggleHistGraph() {
	a.St.HistNoGraph = !a.St.HistNoGraph
	a.SaveState()
}

// HistGraph 是这个仓库的历史这会儿画不画提交图：开关开着，读的时候没有搜索词、也不是文件历史。
func (a *App) HistGraph(r *Repo) bool {
	return !a.St.HistNoGraph && r.Hist.HasGraph()
}

// HistDots 是这个仓库的历史画圆点代替提交图：开关开着，但读的时候有搜索词或是文件历史——提交不连续，泳道没意义，
// 每个提交左边一个点，相邻两行在完整历史里是父子（DotLink）才连一条短线。
func (a *App) HistDots(r *Repo) bool {
	return !a.St.HistNoGraph && !r.Plain && len(r.Hist.Commits) > 0 && !r.Hist.HasGraph()
}

// DotLink 是搜出来挨着的两个提交（above 在上）在完整历史里是父子：above 的 Parents 里有 below。
func DotLink(above, below git.Commit) bool { return slices.Contains(above.Parents, below.Hash) }

// DotLinks 是右栏历史（当前仓库）第 ci 个提交的圆点和上 / 下一个提交连不连（HistDots 时用）。
func (h *History) DotLinks(ci int) (up, down bool) {
	cs := h.Commits
	up = ci > 0 && DotLink(cs[ci-1], cs[ci])
	down = ci+1 < len(cs) && DotLink(cs[ci], cs[ci+1])
	return up, down
}

// AllHistLinks 是"所有仓库"视图第 i 行（AllCommit；AllFile 算它那个提交）和上 / 下一个提交行连不连：得是同一个仓库，
// 画提交图时是它历史里挨着的两个提交（CI 差 1；别的仓库的行插在中间就断开，段与段之间不连），画圆点时是父子（DotLink）。
// 中间隔着的展开文件行不算，隔着段头就不连。
func (a *App) AllHistLinks(rows []AllHistRow, i int) (up, down bool) {
	row := rows[i]
	link := func(above, below AllHistRow) bool {
		if above.Repo != below.Repo {
			return false
		}
		if a.HistGraph(above.Repo) {
			return below.CI == above.CI+1
		}
		return DotLink(above.Commit(), below.Commit())
	}
	near := func(from, step int) (AllHistRow, bool) {
		for j := from; j >= 0 && j < len(rows) && (rows[j].Kind == AllCommit || rows[j].Kind == AllFile); j += step {
			if rows[j].Kind == AllCommit {
				return rows[j], true
			}
		}
		return AllHistRow{}, false
	}
	if row.Kind == AllCommit {
		if above, ok := near(i-1, -1); ok {
			up = link(above, row)
		}
	}
	if below, ok := near(i+1, 1); ok {
		down = link(row, below)
	}
	return up, down
}

// ShowFileHistory 在右栏只看这个仓库里动过 rel 的提交（文件跟着重命名往前找），切到"当前仓库"视图。
func (a *App) ShowFileHistory(r *Repo, rel string, dir bool) {
	a.ClearLocalPath()
	a.UseRepo(r)
	r.Hist.Path, r.Hist.PathDir = rel, dir
	a.St.HistAll, a.St.RightBar = false, true
	a.SaveState()
	a.LoadHistory(r, true)
}

// ClearFileHistory 去掉文件历史的筛选（右栏里那个胶囊上的 ×）。
func (a *App) ClearFileHistory(r *Repo) {
	if r.Hist.Path == "" {
		return
	}
	r.Hist.Path, r.Hist.PathDir = "", false
	if r.Hist.Loaded || r.Hist.Loading {
		a.LoadHistory(r, true)
	}
}
