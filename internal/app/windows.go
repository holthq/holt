package app

import (
	"cmp"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是多窗口（VS Code 的样子）：一个窗口一个工作区，可以同时开几个窗口，同一个工作区只开在一个窗口里
// （再开就把那个窗口叫到前面）。Windows 是开着的窗口的注册表，每个窗口一个 App；槽位定窗口记位置用的 StateKey
// （1 是 main，2 是 main-2……，关掉的槽位新窗口先用）。几个窗口共用一个 workspace.Store（SaveState）。
// 同一个仓库开在两个窗口里就是两份文件监听、两套终端，互相不知道。
//
// 菜单栏是整个应用的（mygo.App.SetMenu），只有前台窗口（Focused）同步它；菜单项的 Click 按传进来的窗口找 App（For）。
//
// 退出（⌘Q、程序坞、关机）：mygo 先发 OnBeforeQuit，没拦住就一个个关窗口，中间有一个拦住了，前面关掉的也回不来了。
// 所以要问的（有没保存的文件、有 git 写命令在跑）在 OnBeforeQuit 里就拦下来（RequestQuit），一个窗口一个窗口地问
// （RequestLeave("")）：问完的窗口关掉后再问下一个，都关完了再退一次；中途取消就停在那里，已经关掉的不再开回来。
//
// 启动时开回几个窗口：每个窗口刚开时要几块整窗的帧内存，一起开峰值就叠在一起（实测两个窗口 327 MiB），
// 所以排成队一个一个开：前一个画完第一帧（Ready，main 里接 mygo 的 OnReadyToShow）再隔 RestoreGap 开下一个。
// 只开一个窗口时没有队列、不等。还原中关掉已开的窗口队列照走、不退出；⌘Q 时没开的也记进 windows（paths）。

// Windows 是开着的工作台窗口（设置、关于窗口不算）。
type Windows struct {
	Apps      []*App                           // 开着的窗口，按槽位
	NewWindow func(a, from *App)               // 给 a 建窗口：设好 a.Win、a.Update 和窗口的回调，from 不为 nil 时错开它一点放（main 里给；测试里是桩）
	Setup     func(a *App)                     // 新的 App 建窗口之前：设好跟着进程走的（设置、关于、最近使用、日志目录……；main 里给）
	Quit      func()                           // 退出整个应用（main 里给：关掉设置、关于窗口再 mygo.App.Quit；测试里是桩）
	Later     func(d time.Duration, fn func()) // 过 d 之后在主线程上跑 fn（还原时错开开窗口；main 里给，测试里是桩）；nil 时还原的窗口一次开完
	Restored  func()                           // 还原的窗口都开完了（main 里把这期间攒着的路径派出去）

	focused  *App     // 最后到前台的窗口
	quitting *App     // 退出时正在问的窗口；nil 为不在问
	ending   bool     // 问完了、要退出了：不再拦，最后一个窗口关掉时不再退一次
	keep     []string // 退出（或关最后一个窗口）时开着的窗口，存进 state.json 的 windows；nil 为按现在开着的算
	store    *workspace.Store

	queue   []restoring // 还原时还没开的窗口，按 state.json 的顺序
	waiting *App        // 还原时等它画完第一帧（Ready）再开下一个
	due     bool        // 已经约了 Later 开下一个
	front   string      // 还原完叫到前面的工作区（命令行给的、本来就在 windows 里）
	failed  error       // 命令行给的工作区打不开：还原完报在前台窗口上
}

// restoring 是还原时排着的一个窗口。
type restoring struct {
	ws      string
	roots   []string
	cascade bool // 命令行给的：错开前台窗口放
	gone    bool // 最近的工作区都打不开了，开的是空工作区：提示一声
}

// RestoreGap 是还原时一个窗口画完第一帧之后隔多久开下一个（调试：WORKBENCH_RESTORE_GAP）。
// 前一个窗口启动时多拿的 drawable 第一帧后约 2.2–2.5 s 才还，比这短两个窗口的启动峰值照样叠在一起：实测间隔 2 s
// 两个窗口峰值 322，2.5 s 259，一个窗口 ~217–246。要改先重新量（TestRestoreGapDefault 守着）。
var RestoreGap = 2500 * time.Millisecond

// NewWindows 读 stateDir 里的 state.json（stateDir 为空就不保存）。窗口由 Restore、Open 开。
func NewWindows(stateDir string) *Windows {
	st := workspace.Default()
	if stateDir != "" {
		var err error
		if st, err = workspace.LoadState(stateDir); err != nil {
			log.Printf("读 %s: %v", workspace.StateFile, err)
		}
	}
	return &Windows{store: workspace.NewStore(stateDir, st)}
}

// Settings 是 state.json 里的设置（启动时建窗口之前先套上外观和语言）。
func (w *Windows) Settings() workspace.Settings { return w.store.State().Settings }

// Restore 是启动：state.json 里记着的窗口一个个开回来（打不开的跳过，同一个工作区只开一次），再开 ws（命令行或
// WORKBENCH_WORKSPACE 给的，已经开着就只叫到前面）；一个都没开成就开最近的工作区（都打不开是空工作区，提示一声）。
// ws 打不开、又没有别的窗口可开时返回错误。第一个窗口当场开，别的排队（next），都开完调 Restored。
func (w *Windows) Restore(ws string) error {
	st := w.store.State()
	var todo []restoring
	has := func(p string) bool { return slices.ContainsFunc(todo, func(r restoring) bool { return r.ws == p }) }
	for _, p := range st.Windows {
		if p != "" && has(p) {
			continue
		}
		roots, err := workspaceRoots(p)
		if p != "" && err != nil {
			log.Printf("开回窗口 %s: %v", p, err)
			continue
		}
		todo = append(todo, restoring{ws: p, roots: roots})
	}
	if ws != "" {
		ws = absPath(ws)
		roots, err := workspaceRoots(ws)
		switch {
		case err != nil && len(todo) == 0:
			return err
		case err != nil:
			w.failed = err
		case has(ws):
			w.front = ws
		default:
			todo = append(todo, restoring{ws: ws, roots: roots, cascade: true})
		}
	}
	if len(todo) == 0 {
		p, roots, failed := startWorkspace(&st)
		todo = append(todo, restoring{ws: p, roots: roots, gone: failed})
	}
	w.queue = todo
	w.next()
	return nil
}

// Restoring 是还原的窗口还没开完（main 的 deliver 这期间攒着外面交进来的路径）。
func (w *Windows) Restoring() bool { return len(w.queue) > 0 }

// next 开队列里的下一个窗口，有 Later 时开一个就停下等它画完第一帧（Ready）；队列空了收尾：
// 命令行给的、本来就开着的叫到前面，打不开的报在前台窗口上，调 Restored。在退出（问着、要退了）就停着。
func (w *Windows) next() {
	w.due = false
	for len(w.queue) > 0 {
		if w.quitting != nil || w.ending {
			return
		}
		r := w.queue[0]
		w.queue = w.queue[1:]
		if r.ws != "" && w.Find(r.ws) != nil { // 这期间从别处开了
			continue
		}
		var from *App
		if r.cascade {
			from = w.Focused()
		}
		a := w.open(r.ws, r.roots, from)
		if r.gone {
			a.Toast = i18n.T("最近的工作区都打不开了")
		}
		if len(w.queue) > 0 && w.Later != nil {
			w.waiting = a
			return
		}
	}
	w.waiting = nil
	if o := w.Find(w.front); o != nil {
		w.Show(o)
	}
	if f := w.Focused(); f != nil && w.failed != nil {
		f.Fail(i18n.T("打不开工作区"), w.failed)
	}
	w.front, w.failed = "", nil
	if w.Restored != nil {
		w.Restored()
	}
}

// Ready 在窗口 a 画完第一帧时调（main 里接 OnReadyToShow，只来一次）：还原时等的是它，就隔 RestoreGap 开下一个。
func (w *Windows) Ready(a *App) {
	if a == nil || a != w.waiting {
		return
	}
	w.waiting = nil
	w.after()
}

// after 约 RestoreGap 之后开队列里的下一个（已经约了就不再约）。
func (w *Windows) after() {
	if len(w.queue) > 0 && !w.due && w.Later != nil {
		w.due = true
		w.Later(RestoreGap, w.next)
	}
}

// Open 在新窗口里打开工作区 ws（.code-workspace 或目录，"" 是空工作区），新窗口错开 from（为 nil 时是前台窗口）放。
// ws 已经开在哪个窗口里就把那个窗口叫到前面、在那里说一声，返回它；打不开的报在 from（或前台窗口）上，返回 nil。
func (w *Windows) Open(ws string, from *App) *App {
	from = cmp.Or(from, w.Focused())
	if ws == "" {
		return w.open("", nil, from)
	}
	ws = absPath(ws)
	if o := w.Find(ws); o != nil {
		w.Show(o)
		o.Toast = i18n.Tf("已经开着工作区 %s", o.WsName())
		o.Update(func() {})
		return o
	}
	roots, err := workspaceRoots(ws)
	if err != nil {
		if from != nil {
			from.St.RecentWs = slices.DeleteFunc(from.St.RecentWs, func(s string) bool { return s == ws })
			from.Fail(i18n.T("打不开工作区"), err)
		}
		return nil
	}
	return w.open(ws, roots, from)
}

// open 建工作区 ws（仓库 roots）的 App，占最小的空槽位，建窗口，再启动（终端、状态读取要等窗口建好）。
func (w *Windows) open(ws string, roots []string, from *App) *App {
	a := newApp(ws, roots, w.store.State())
	a.StateDir, a.Windows = w.store.Dir(), w
	a.defaults()
	if w.Setup != nil {
		w.Setup(a)
	}
	a.Slot = 1
	for w.slotUsed(a.Slot) {
		a.Slot++
	}
	i, _ := slices.BinarySearchFunc(w.Apps, a.Slot, func(o *App, s int) int { return o.Slot - s })
	w.Apps = slices.Insert(w.Apps, i, a)
	if w.NewWindow != nil {
		w.NewWindow(a, from)
	}
	w.focused = a
	a.Start()
	return a
}

func (w *Windows) slotUsed(slot int) bool {
	return slices.ContainsFunc(w.Apps, func(a *App) bool { return a.Slot == slot })
}

// StateKey 是窗口记位置和大小用的键：第一个槽位是 main（和单窗口时一样），往后是 main-2、main-3……
func (a *App) StateKey() string {
	if a.Slot <= 1 {
		return "main"
	}
	return "main-" + strconv.Itoa(a.Slot)
}

// Title 是窗口的标题（窗口菜单、调度中心里看得到）：工作区名 — Holt，空工作区就是 Holt。
func (a *App) Title() string {
	if a.WsPath == "" {
		return "Holt"
	}
	return a.WsName() + " — Holt"
}

// Find 是开着工作区 ws（绝对路径）的窗口，没有为 nil。
func (w *Windows) Find(ws string) *App {
	for _, a := range w.Apps {
		if a.WsPath != "" && a.WsPath == ws {
			return a
		}
	}
	return nil
}

// Focused 是前台的工作台窗口：最后到前台的那个，没有就是第一个；一个窗口都没有时是 nil。
func (w *Windows) Focused() *App {
	if w.focused != nil && slices.Contains(w.Apps, w.focused) {
		return w.focused
	}
	if len(w.Apps) > 0 {
		return w.Apps[0]
	}
	return nil
}

// For 是窗口 win 的 App（菜单项的 Click 带着前台窗口）；win 不是工作台窗口（设置、关于在前面）或为 nil 时是前台的工作台窗口。
func (w *Windows) For(win *mygo.Window) *App {
	for _, a := range w.Apps {
		if win != nil && a.Win == win {
			return a
		}
	}
	return w.Focused()
}

// Route 打开外面交进来的路径（命令行、第二个实例、open -a、拖到 Dock；绝对路径。OnOpenFile 分不出是哪一种，一起走这里）：
//   - 工作区文件开新窗口（开着就叫到前面）；
//   - 目录见 routeDir：开着的工作区或仓库叫到前面，前台是空工作区就在它里面开，不然一个目录开一个新窗口；
//   - 别的（文件、找不到的）交给前台窗口（OpenArgs），前台窗口叫到前面。
//
// 拖进窗口里的（OnFileDrop）不走这里，照旧是 OpenArgs：目录问要不要加进当前工作区。
func (w *Windows) Route(paths []string) {
	var rest []string
	for _, p := range paths {
		info, err := os.Stat(p)
		switch {
		case err == nil && !info.IsDir() && filepath.Ext(p) == ".code-workspace":
			w.Open(p, nil)
		case err == nil && info.IsDir():
			w.routeDir(filepath.Clean(p))
		default:
			rest = append(rest, p)
		}
	}
	if len(rest) == 0 {
		return
	}
	if t := w.Focused(); t != nil {
		w.Show(t)
		t.Update(func() { t.OpenArgs(rest, "") })
	}
}

// routeDir 打开外面交进来的目录 p：
//  1. 它就是哪个窗口的工作区：叫到前面（Open）；
//  2. 它（或它的 git 根）是哪个开着的窗口里的仓库：叫到前面、跳到那个仓库（同一个仓库不开第二个窗口）；
//  3. 前台窗口是空工作区：就在它里面开；
//  4. 别的开新窗口。
func (w *Windows) routeDir(p string) {
	p = absPath(p)
	if w.Find(p) != nil {
		w.Open(p, nil)
		return
	}
	roots := []string{p}
	if root, err := git.Toplevel(p); err == nil && root != p {
		roots = append(roots, root)
	}
	for _, a := range w.Apps {
		for _, root := range roots {
			if r, rel := a.linkRepo(root); r != nil && rel == "" {
				w.Show(a)
				a.Update(func() { a.JumpRepo(slices.Index(a.Repos, r)) })
				return
			}
		}
	}
	if t := w.Focused(); t != nil && t.WsPath == "" {
		w.Show(t)
		t.Update(func() { t.OpenWorkspace(p) })
		return
	}
	w.Open(p, nil)
}

// Focus 在窗口 a 到前台时调（OnFocus）：菜单栏换成它的。
func (w *Windows) Focus(a *App) {
	w.focused = a
	a.menuSig = ""
}

// Show 把窗口 a 叫到前面。
func (w *Windows) Show(a *App) {
	w.focused = a
	a.menuSig = ""
	if a.Win != nil && !a.Win.IsDestroyed() {
		mygo.App.Focus()
		a.Win.Show()
		a.Win.Focus()
	}
}

// list 是存进 state.json 的 windows：退出时冻结的，或者现在开着的（按槽位）。只有一个窗口、开的是最近的工作区
// （或空工作区）时不记——下次启动本来就开最近的，和以前的文件一样。
func (w *Windows) list(recent []string) []string {
	l := w.keep
	if l == nil {
		l = w.paths()
	}
	if len(l) == 1 && (l[0] == "" || len(recent) > 0 && l[0] == recent[0]) {
		return nil
	}
	return slices.Clone(l)
}

// paths 是开着的窗口（按槽位），还原时后面再跟着还没开的（按 state.json 的顺序）：还原到一半退出，下次照样都回来。
func (w *Windows) paths() []string {
	l := make([]string, 0, len(w.Apps)+len(w.queue))
	for _, a := range w.Apps {
		l = append(l, a.WsPath)
	}
	for _, r := range w.queue {
		if r.ws == "" || !slices.Contains(l, r.ws) {
			l = append(l, r.ws)
		}
	}
	return l
}

// freeze 记下现在开着的窗口（退出、关最后一个窗口时；还原时连同还没开的），之后一个个关掉也照这个存。
func (w *Windows) freeze() {
	if w.keep == nil {
		w.keep = w.paths()
	}
}

// syncShared 在 from 存完之后把跟着进程走的（最近的工作区、最近打开的文件、提交信息历史、最近搜索、跑过的任务、设置、版本）抄给别的窗口：
// 每个窗口手里总是最新的，谁存都不会把别人刚加的盖掉，清除、删掉的也一起没了。
func (w *Windows) syncShared(from *App) {
	for _, a := range w.Apps {
		if a == from {
			continue
		}
		a.St.RecentWs, a.St.Recent, a.St.CommitMsgs = slices.Clone(from.St.RecentWs), slices.Clone(from.St.Recent), slices.Clone(from.St.CommitMsgs)
		a.St.SearchHistory, a.St.TaskHistory = slices.Clone(from.St.SearchHistory), slices.Clone(from.St.TaskHistory)
		a.St.Settings, a.St.AppVersion = from.St.Settings, from.St.AppVersion
	}
}

// Closed 在窗口 a 关掉之后调（OnClosed）：记下终端在哪、存会话、关掉终端和文件监听，让出槽位，
// 把关掉的窗口占的内存还给系统。关的是最后一个窗口就退出（下次启动开回它）；退出时一个个问的，接着问下一个。
// 还原中（还有没开的）关掉的不算最后一个：队列照走，不退出，下次启动不开回它。
func (w *Windows) Closed(a *App) {
	i := slices.Index(w.Apps, a)
	if i < 0 {
		return
	}
	restoring := w.Restoring()
	if len(w.Apps) == 1 && !restoring || w.quitting != nil || w.ending {
		w.freeze()
	}
	w.Apps = slices.Delete(w.Apps, i, i+1)
	if w.focused == a {
		w.focused = nil
	}
	a.NoteTermDirs() // shell 不报告目录时用 lsof 读，会话里记下每个终端标签在哪
	a.SaveState()
	a.Close()
	if a.Rend != nil { // 渲染器跟着窗口走（换工作区的 Close 不关它）
		a.Rend.Close()
		a.Rend = nil
	}
	debug.FreeOSMemory()
	if w.waiting == a { // 没画完第一帧就关了：不等它
		w.Ready(a)
	}
	switch {
	case w.quitting == a:
		w.quitting = nil
		if !w.ask() {
			w.ending = true
			w.Quit()
		}
	case w.quitting == nil && len(w.Apps) == 0 && !w.ending && !restoring:
		w.ending = true
		w.Quit()
	}
}

// CanClose 在窗口 a 要关时调（OnClose）：没什么要问的、或已经问完了就让它关；否则拦下来先问（RequestLeave("")）。
// 退出时关到它才发现有要问的（刚改的），就接着一个个问。
func (w *Windows) CanClose(a *App) bool {
	if a.ForceClose || !a.mustAsk() {
		return true
	}
	if w.ending {
		w.ending, w.quitting = false, a
	}
	a.Update(func() { a.RequestLeave("") })
	return false
}

// RequestQuit 在要退出时调（OnBeforeQuit）：没有要问的窗口就放行（true）；有就拦下来，从第一个要问的窗口问起。
func (w *Windows) RequestQuit() bool {
	if w.ending {
		return true
	}
	if w.quitting != nil { // 已经在问了：把它叫到前面
		w.Show(w.quitting)
		return false
	}
	w.freeze()
	if !w.ask() {
		w.ending = true
		return true
	}
	return false
}

// ask 问下一个要问的窗口；没有了返回 false。
func (w *Windows) ask() bool {
	for _, a := range w.Apps {
		if a.mustAsk() {
			w.quitting = a
			w.Show(a)
			a.Update(func() { a.RequestLeave("") })
			return true
		}
	}
	return false
}

// leaveDropped 在退出或换工作区没走完就放弃时调（取消、文件存不上、中断后留下了锁文件）：退出时一个个问的就停在这里，
// 已经关掉的窗口不再开回来，state.json 的 windows 按还开着的记。还原到一半时取消退出就当没按过 ⌘Q：
// 还没开的接着开（不然它们只在 windows 里、下次启动才回来，这次要自己一个个再开）。
func (a *App) leaveDropped() {
	if w := a.Windows; w != nil && w.quitting == a {
		w.quitting, w.keep = nil, nil
		a.SaveState()
		if w.waiting == nil {
			w.after()
		}
	}
}

// mustAsk 是关窗口（退出）前要不要问：有没保存的文件，或有 git 写命令在跑。
func (a *App) mustAsk() bool { return a.DirtyDocs() > 0 || len(a.WritesRunning()) > 0 }

// peers 是所有开着的窗口（没有注册表时只有自己）。
func (a *App) peers() []*App {
	if a.Windows == nil {
		return []*App{a}
	}
	return a.Windows.Apps
}

// target 是菜单项该交给的 App：Click 带来的窗口是哪个工作台窗口就是哪个，否则是前台的工作台窗口（a 建的菜单）。
func (a *App) target(win *mygo.Window) *App {
	if a.Windows == nil {
		return a
	}
	return cmp.Or(a.Windows.For(win), a)
}

// NewWindow 开一个空工作区的新窗口（⌥⌘N）。
func (a *App) NewWindow() {
	if a.Windows != nil {
		a.Windows.Open("", a)
	}
}

// openNewWindow 在新窗口里打开工作区 p（已经开着就叫到前面）；没有注册表（测试）时在这个窗口里换。
func (a *App) openNewWindow(p string) {
	if a.Windows == nil {
		a.OpenWorkspace(p)
		return
	}
	a.Windows.Open(p, a)
}
