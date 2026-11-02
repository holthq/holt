package app

import (
	"log"
	"slices"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/tasks"
	"github.com/lemoeade/grove/internal/watch"
)

// 这个文件是文件监听和重读仓库状态的唯一入口 RefreshRepo：监听到的变化（合并 300 ms）、⌘R、
// 切回窗口（只管监听停了的仓库）、应用里跑完的 git 写命令、保存文件都走它。

// 重读仓库状态的原因（RefreshRepo 的 why，记在 Repo.LastWhy）。
const (
	WhyManual     = "手动刷新"   // ⌘R、Git 栏的刷新按钮、仓库菜单
	WhyFocus      = "切回窗口"   // 监听停了的仓库的兜底
	WhyOpen       = "打开仓库"   // 启动、添加仓库、切到一个没在监听又好久没读的仓库
	WhyOp         = "git 操作" // 应用里跑完的 git 写命令
	WhySave       = "保存"
	WhyFileOp     = "文件操作"    // 应用里新建、重命名、移到废纸篓
	WhyWatchGit   = ".git 变了" // 监听：提交、暂存、切分支、拉取……
	WhyWatchFiles = "文件变了"    // 监听：只是工作区里的文件
)

// filesOnly 是只有工作区文件变了的原因：不用重读历史。
func filesOnly(why string) bool { return why == WhyWatchFiles || why == WhySave || why == WhyFileOp }

// RefreshAll 在后台重新读所有仓库的状态（⌘R、启动）。
func (a *App) RefreshAll(why string) {
	a.LastRefresh = time.Now()
	a.checkWsSettings()
	for _, r := range a.Repos {
		a.RefreshRepo(r, why)
	}
}

// RefreshOnFocus 是切回窗口：监听停了（或没开）的仓库重读一次，在监听的不用。
func (a *App) RefreshOnFocus() {
	if time.Since(a.LastRefresh) < 3*time.Second {
		return
	}
	a.LastRefresh = time.Now()
	a.checkWsSettings()
	for _, r := range a.Repos {
		if r.watcher == nil {
			a.RefreshRepo(r, WhyFocus)
		}
	}
}

// RefreshRepo 在后台重读仓库的状态和分支；文件树和（不是只有文件变了时）历史已经读过的也重读；
// 跟着更新这个仓库的文件标签和 Diff 标签。正在读的时候又来一次，就等这次读完再读一遍，变化不会丢。
// 文件树：只有监听报的工作区变化和保存时按 r.hints 只看变了的地方（treediff.go），别的原因整棵读。
func (a *App) RefreshRepo(r *Repo, why string) {
	r.LastWhy = why
	if why != WhyWatchFiles && why != WhySave {
		r.hints.full = true
	}
	if r.Loading {
		if r.again == "" || filesOnly(r.again) {
			r.again = why
		}
		return
	}
	r.Loading = true
	extra := !filesOnly(why) || !r.Loaded // 只有工作区的文件变了时 HEAD、合并没变
	wasPlain := r.Plain
	var inc *treeDiff
	if r.Files.Loaded || r.Files.Loading {
		inc = a.startTreeDiff(r, r.hints)
	}
	r.hints = fileHints{}
	up := a.Update
	oldStamp := r.taskStamp
	t0 := time.Now()
	go func() {
		done := make(chan struct{})
		if inc != nil {
			go func() { inc.run(r.ctx, r.Root); close(done) }()
		} else {
			close(done)
		}
		// 普通文件夹每次先看一眼是不是 git init 了；仓库读状态失败再看是不是 .git 没了（或本来就不是仓库）
		plain := wasPlain && git.NotRepo(r.Root)
		var st git.Status
		var err error
		if !plain {
			st, err = git.ReadStatus(r.ctx, r.Root)
			if err != nil && r.ctx.Err() == nil && git.NotRepo(r.Root) {
				plain, err = true, nil
			}
		}
		var dirs chan dirLists // 有未跟踪目录时另外列里面的文件，和下面几样一起跑
		if err == nil && !plain && st.HasDirs() {
			dirs = make(chan dirLists, 1)
			go func(st git.Status) { dirs <- listDirs(r, st) }(st)
		}
		var bs []git.Branch
		var tags []git.Tag
		var x git.Extra
		var wts wtList
		if err == nil && !plain {
			bs, _ = git.Branches(r.ctx, r.Root)
			tags, _ = git.ReadTags(r.ctx, r.Root)
			if extra {
				x, _ = git.ReadExtra(r.ctx, r.Root)
				wts = readWorktrees(r.ctx, r.Root, x) // 没有 .git/worktrees 的仓库不跑命令
			}
		}
		stamp := tasks.Stamp(r.Root) // 任务：那几个文件变了才重扫
		var tks []tasks.Task
		if stamp != oldStamp {
			var terr error
			if tks, terr = tasks.Scan(r.Root); terr != nil {
				log.Printf("认 %s 的任务: %v", r.Root, terr)
			}
		}
		<-done
		dl, late := waitDirs(dirs)
		up(func() {
			if r.gone() { // 读的时候移除了仓库、切了工作区
				r.Loading = false
				return
			}
			if stamp != oldStamp {
				r.Tasks, r.taskStamp = tks, stamp
				a.taskVer++
			}
			if err != nil && (r.Err == nil || r.Err.Error() != err.Error()) {
				log.Printf("读 %s 的状态: %v", r.Root, err) // 同一个错只记一次
			}
			first := !r.Loaded
			took := time.Since(t0)
			r.Loading, r.Loaded, r.LoadedAt = false, true, time.Now()
			if late != nil {
				st.DirFiles, st.DirMore = keepDirs(r.Status, st)
				a.lateDirs(r, late)
			} else {
				st.DirFiles, st.DirMore = dl.files, dl.more
			}
			r.Status, r.Err, r.Branches, r.Tags = st, err, bs, tags
			if extra || err != nil || plain {
				r.Extra = x
				r.syncStashOpen()
				r.Files.SetSubs(subStates(x.Subs))
			}
			if extra && err == nil {
				a.followRebase(r)
			}
			if err != nil {
				r.Status = git.Status{}
			}
			if plain != r.Plain {
				a.setPlain(r, plain, first)
			}
			switch {
			case err != nil || plain:
				a.clearWorktrees(r)
			case extra:
				a.setWorktrees(r, x, wts)
			}
			a.syncTagHistory(r)
			if inc != nil {
				a.applyTreeDiff(r, inc)
			}
			r.Files.Version++ // 文件树上的改动标记跟着状态变
			a.syncDocs(r)
			a.syncChangeSel()
			if again := r.again; again != "" {
				r.again = ""
				a.RefreshRepo(r, again)
			}
			if first {
				a.readyStatus(r, took)
			}
		})
	}()
	if !filesOnly(why) && (r.Hist.Loaded || r.Hist.Loading) {
		a.LoadHistory(r, true)
	}
	if inc == nil && (r.Files.Loaded || r.Files.Loading) {
		a.LoadFiles(r)
	}
}

// refreshSaved 是保存了 d（写回了磁盘）：重读它仓库的状态，文件树只看这一个文件。
func (a *App) refreshSaved(d *docs.Doc) {
	a.relink(d) // 镜像：时间、大小、换行、编码抄给别的几份
	if r := a.RepoOf(d); r != nil {
		r.hints.noteFile(d.Rel)
		a.RefreshRepo(r, WhySave)
		a.SearchFilesChanged(r, []string{d.Rel}, false)
		a.symFilesChanged(r, []string{d.Rel}, false)
	}
	a.lintSaved(d) // 保存时检查：开着才跑
}

// setPlain 是读状态时发现仓库成了普通文件夹（本来就不是仓库，或 .git 被删了），或普通文件夹 git init 了。
// 第一次读状态就发现的只记下来（文件树、监听自己会按普通文件夹读和开）；读过之后变的，文件树换种方式重读、
// 历史清掉（重新要的时候再读）、监听按新的样子重开。
func (a *App) setPlain(r *Repo, plain, first bool) {
	r.Plain = plain
	a.tree.ver++
	if first {
		return
	}
	if r.Files.Loaded || r.Files.Loading {
		a.LoadFiles(r)
	}
	r.Hist = History{Sel: -1, PrevSel: -1, Path: r.Hist.Path, PathDir: r.Hist.PathDir, gen: r.Hist.gen + 1}
	if r.watching {
		a.unwatch(r)
		a.watchRepo(r)
	}
}

// StartWatching 给每个仓库开文件监听，之后加进来的仓库也开（Start 里调；测试按需调）。
func (a *App) StartWatching() {
	a.watching = true
	for _, r := range a.Repos {
		a.watchRepo(r)
		a.syncWtWatch(r)
	}
}

// Watching 是仓库的监听在跑。
func (r *Repo) Watching() bool { return r.watcher != nil }

// watchRepo 在后台给仓库开监听（rev-parse、读忽略的目录要几十毫秒，不卡界面）。
func (a *App) watchRepo(r *Repo) {
	if !a.watching || r.watching {
		return
	}
	r.watching, r.WatchErr = true, nil
	up := a.Update
	go func() {
		onChange := func(c watch.Change) {
			up(func() {
				if r.watcher != nil {
					a.watchChanged(r, c)
				}
			})
		}
		onStop := func(err error) {
			up(func() { a.watchStopped(r, err) })
		}
		w, err := watch.Start(r.Root, onChange, onStop)
		if err != nil && git.NotRepo(r.Root) {
			w, err = watch.StartPlain(r.Root, onChange, onStop)
		}
		up(func() {
			switch {
			case err != nil:
				a.watchStopped(r, err)
			case !a.watching || !r.watching || !slices.Contains(a.Repos, r): // 等的时候关了窗口或移除了仓库
				w.Close()
			default:
				r.watcher = w
			}
		})
	}()
}

// watchChanged 是监听报来一批合并过的变化：记下文件树要看的地方，重读。
func (a *App) watchChanged(r *Repo, c watch.Change) {
	why := WhyWatchFiles
	if c.Git {
		why = WhyWatchGit
	}
	r.hints.note(c)
	a.RefreshRepo(r, why)
	if c.Files {
		a.SearchFilesChanged(r, c.Dirs, c.Full)
		a.symFilesChanged(r, c.Dirs, c.Full)
		a.wtTouched(r)
	}
}

// watchStopped 是仓库的监听停了或没开起来：记进输出栏，之后靠切回窗口和 ⌘R 刷新。
func (a *App) watchStopped(r *Repo, err error) {
	if r.watcher != nil {
		go r.watcher.Close() // 等它的 goroutine 退出，别在主线程上等
	}
	r.watcher, r.watching, r.WatchErr = nil, false, err
	if slices.Contains(a.Repos, r) {
		a.Log(r, i18n.T("文件监听停了"), KeyHint("git.refresh", "%s\n之后切回窗口或按 %s 刷新。", "%s\n之后切回窗口或运行“刷新 Git 状态”。", err.Error()), true)
	}
}

// unwatch 停掉一个仓库的监听（移除仓库）。
func (a *App) unwatch(r *Repo) {
	if r.watcher != nil {
		r.watcher.Close()
	}
	r.watcher, r.watching = nil, false
	r.stopWtWatch()
}

// stopWatching 停掉所有监听（关窗口）。
func (a *App) stopWatching() {
	a.watching = false
	for _, r := range a.Repos {
		a.unwatch(r)
	}
}
