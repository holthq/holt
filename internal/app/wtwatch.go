package app

import (
	"slices"

	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/watch"
)

// 这个文件是 Git 栏"工作树"块算着状态时（WtTracked：块展开着，或收着但有工作树在等你、在跑、可合并）对链接工作树目录的监听：
// 算着状态，就给 r.Worktrees 里的每个工作树
// （最多 WtWatchMax 个；已经在工作区里打开的那个有它自己的监听，不另开）开一个只看工作区的监听（watch.StartTree：
// 一个 FSEvents 流，被 git 忽略的目录不看），里面的文件变了只重算那一条的 WtInfo。不算状态就不监听。
// HEAD、index、refs 变了（在工作树里提交、暂存、切分支）不用这里管：链接工作树的 HEAD、index 在主仓库的
// .git/worktrees/<名字> 下，refs 在主仓库的 .git 里，都在主仓库自己的监听范围里，报 Git 变化、整块重读。
// 不用再算（块收起了、也没有要你管的）、列表里没了、仓库移除、关窗口就停。

// WtWatchMax 是一个仓库最多同时监听几个工作树目录（每个一个 FSEvents 流）。
const WtWatchMax = 16

// wtWatcher 是一个工作树目录的监听：w 在后台开起来之前是 nil；stop 是已经不要了（开起来了也马上关）。
type wtWatcher struct {
	w    *watch.Watcher
	stop bool
}

// syncWtWatch 让 r 的工作树监听和现在该看的对上（主线程）：展开状态、列表、工作区里开着哪些仓库变了之后调。
func (a *App) syncWtWatch(r *Repo) {
	want := map[string]bool{}
	if a.watching && r.WtTracked() && !r.gone() && slices.Contains(a.Repos, r) {
		for _, w := range r.Worktrees {
			if len(want) >= WtWatchMax {
				break
			}
			if !w.Prunable && !w.Bare && a.WtRepo(w) == nil {
				want[w.Path] = true
			}
		}
	}
	for p, e := range r.wtWatch {
		if !want[p] {
			e.close()
			delete(r.wtWatch, p)
		}
	}
	for p := range want {
		if _, ok := r.wtWatch[p]; ok {
			continue
		}
		if r.wtWatch == nil {
			r.wtWatch = map[string]*wtWatcher{}
		}
		e := &wtWatcher{}
		r.wtWatch[p] = e
		up := a.Update
		go func() {
			onChange := func(watch.Change) {
				up(func() {
					if !e.stop {
						a.reloadWtInfo(r, p)
					}
				})
			}
			onStop := func(err error) {
				up(func() {
					if !e.stop { // 目录没了之类：留着这一条（不再重试），列表重读后它不在了自然清掉
						a.Log(r, i18n.T("工作树的监听停了"), p+"\n"+err.Error(), false)
					}
				})
			}
			w, err := watch.StartTree(p, onChange, onStop)
			up(func() {
				switch {
				case err != nil:
					if !e.stop {
						a.Log(r, i18n.T("工作树的监听停了"), p+"\n"+err.Error(), false)
					}
				case e.stop:
					go w.Close()
				default:
					e.w = w
				}
			})
		}()
	}
}

// wtReposChanged 是工作区里开着的仓库变了（加了、移除了）：各仓库"工作树"块里哪些要自己盯跟着变。
func (a *App) wtReposChanged() {
	for _, r := range a.Repos {
		a.syncWtWatch(r)
	}
}

// close 停掉这一条（主线程）：Close 要等监听的 goroutine 退出，放到后台。
func (e *wtWatcher) close() {
	e.stop = true
	if e.w != nil {
		go e.w.Close()
		e.w = nil
	}
}

// stopWtWatch 停掉 r 的所有工作树监听（仓库移除、关窗口）。
func (r *Repo) stopWtWatch() {
	for _, e := range r.wtWatch {
		e.close()
	}
	r.wtWatch = nil
}

// WtWatching 是 r 现在在看的工作树目录数（开起来了的）。
func (r *Repo) WtWatching() int {
	n := 0
	for _, e := range r.wtWatch {
		if e.w != nil {
			n++
		}
	}
	return n
}

// reloadWtInfo 重算一个工作树（目录 path）的状态：它的目录里变了。只算这一条、算完换进 WtInfo；
// 不再算状态了、整块重算了（wtGen 变了）就扔掉。整块还在算（可能是变之前开始的）就整块重来。
func (a *App) reloadWtInfo(r *Repo, path string) {
	i := slices.IndexFunc(r.Worktrees, func(w Worktree) bool { return w.Path == path })
	if !r.WtTracked() || r.gone() || i < 0 {
		return
	}
	if r.WtLoading || r.WtInfo == nil {
		a.loadWtInfo(r)
		return
	}
	w, gen, base, ctx, up := r.Worktrees[i], r.wtGen, wtBase(r), r.ctx, a.Update
	go func() {
		info := wtInfo(ctx, r.Root, base, w.Worktree)
		up(func() {
			if r.wtGen == gen && !r.gone() && r.WtInfo != nil {
				r.WtInfo[w.Path] = info
			}
		})
	}()
}

// wtTouched 是工作区里打开着的仓库 o 的监听报了文件变化：o 要是别的仓库"工作树"块（算着状态）里的一条，
// 那一条的状态跟着重算（这种工作树不另开监听）。
func (a *App) wtTouched(o *Repo) {
	for _, r := range a.Repos {
		if r == o || !r.WtTracked() || len(r.Worktrees) == 0 {
			continue
		}
		for _, w := range r.Worktrees {
			if a.WtRepo(w) == o {
				a.reloadWtInfo(r, w.Path)
			}
		}
	}
}
