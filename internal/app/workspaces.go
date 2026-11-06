package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/workspace"
)

// 这个文件是工作区：打开文件夹（⌘O）和 .code-workspace、最近的工作区，以及退出和换工作区之前要问的
// （有文件没保存、有 git 写命令在跑；换工作区时还有终端里在跑的程序）。一个窗口一个工作区：换工作区时
// 先按工作区记下会话，关掉终端、所有标签和还在跑的 git 命令，再读新的工作区、放回它上次的会话。
// 要换去的工作区已经开在别的窗口里就只把那个窗口叫到前面（windows.go）；"在新窗口中打开"见 Windows.Open。
// 打开文件夹和打开工作区文件是两个面板：mygo 的 Dialog.Open 要么只能选文件、要么只能选目录。

// workspaceRoots 是工作区 p 里的仓库：.code-workspace 读它的 folders；目录在 Git 仓库里就是那个仓库，
// 不在就找下面一层的仓库（不进点开头的目录），也没有就把它当普通文件夹打开。
func workspaceRoots(p string) ([]string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return workspace.Load(p)
	}
	if root, err := git.Toplevel(p); err == nil {
		return []string{root}, nil
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(p, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			roots = append(roots, dir)
		}
	}
	if len(roots) == 0 {
		return []string{p}, nil
	}
	return roots, nil
}

// startWorkspace 是没指定工作区时打开的：最近一个还打得开的；一个都没有（第一次用）或都打不开就是空工作区
// （p 为 ""、没有仓库），都打不开时 failed。
func startWorkspace(st *workspace.State) (p string, roots []string, failed bool) {
	for _, p := range st.RecentWs {
		if roots, err := workspaceRoots(p); err == nil {
			return p, roots, false
		}
	}
	return "", nil, len(st.RecentWs) > 0
}

// wsName 是工作区的名字：工作区文件去掉 .code-workspace，目录就是目录名。
func wsName(p string) string { return strings.TrimSuffix(filepath.Base(p), ".code-workspace") }

// WsLabel 是菜单里最近工作区的一项：名字，后面是它所在的目录（~ 开头）。
func WsLabel(p string) string {
	dir := filepath.Dir(p)
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home+string(filepath.Separator)) {
		dir = "~" + dir[len(home):]
	}
	return wsName(p) + "  —  " + dir
}

// absPath 是 p 的绝对路径（取不到就原样）。
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// RecentWorkspaces 是最近打开过的别的工作区，最近的在前；磁盘上已经没有的不列。
func (a *App) RecentWorkspaces() []string {
	var out []string
	for _, p := range a.St.RecentWs {
		if p == a.WsPath {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// noteWs 把现在的工作区记成最近打开的（放到最前），系统的"最近使用"也记一笔（OnRecent）。
func (a *App) noteWs() {
	if p := a.WsPath; p != "" { // 空工作区不记
		a.St.RecentWs = slices.Insert(slices.DeleteFunc(a.St.RecentWs, func(s string) bool { return s == p }), 0, p)
		if len(a.St.RecentWs) > workspace.MaxRecentWs {
			a.St.RecentWs = a.St.RecentWs[:workspace.MaxRecentWs]
		}
		if a.OnRecent != nil {
			a.OnRecent(p)
		}
	}
	a.SaveState()
}

// ClearRecentWs 清空最近的工作区（菜单里的"清除最近工作区"），系统的"最近使用"也清掉。
func (a *App) ClearRecentWs() {
	a.St.RecentWs = nil
	if a.OnClearRecent != nil {
		a.OnClearRecent()
	}
	a.SaveState()
	a.Toast = i18n.T("已清除最近工作区")
}

// ChooseFolder 用系统的打开面板选一个文件夹当工作区（⌘O）。
func (a *App) ChooseFolder() {
	a.chooseWorkspace(mygo.OpenDialogOptions{
		Title:     i18n.T("打开文件夹"),
		Message:   i18n.T("选一个文件夹：Git 仓库、下面一层有仓库的文件夹，或普通文件夹；会换掉现在的工作区"),
		Directory: true,
	}, false)
}

// ChooseWsFile 用系统的打开面板选一个 .code-workspace。
func (a *App) ChooseWsFile() {
	a.chooseWorkspace(mygo.OpenDialogOptions{
		Title:   i18n.T("打开工作区文件"),
		Message: i18n.T("选一个 VS Code 的 .code-workspace；会换掉现在的工作区"),
		Filters: []mygo.FileFilter{{Name: i18n.T("VS Code 工作区"), Extensions: []string{"code-workspace"}}},
	}, false)
}

// ChooseFolderNewWindow 选一个文件夹，在新窗口里打开（⌥⌘O）。
func (a *App) ChooseFolderNewWindow() {
	a.chooseWorkspace(mygo.OpenDialogOptions{
		Title:     i18n.T("在新窗口中打开文件夹"),
		Message:   i18n.T("选一个文件夹：Git 仓库、下面一层有仓库的文件夹，或普通文件夹；在新窗口里打开"),
		Directory: true,
	}, true)
}

// ChooseWsFileNewWindow 选一个 .code-workspace，在新窗口里打开。
func (a *App) ChooseWsFileNewWindow() {
	a.chooseWorkspace(mygo.OpenDialogOptions{
		Title:   i18n.T("在新窗口中打开工作区文件"),
		Message: i18n.T("选一个 VS Code 的 .code-workspace；在新窗口里打开"),
		Filters: []mygo.FileFilter{{Name: i18n.T("VS Code 工作区"), Extensions: []string{"code-workspace"}}},
	}, true)
}

// chooseWorkspace 弹打开面板，选好了换到那个工作区（newWin 时在新窗口里开）。
func (a *App) chooseWorkspace(opts mygo.OpenDialogOptions, newWin bool) {
	opts.Parent, opts.ButtonLabel, opts.DefaultPath = a.Win, i18n.T("打开"), filepath.Dir(a.WsPath)
	if a.WsPath == "" {
		opts.DefaultPath, _ = os.UserHomeDir()
	}
	up, open := a.Update, a.OpenDialog
	go func() {
		paths, err := open(opts)
		if err != nil || len(paths) == 0 {
			return
		}
		up(func() {
			if newWin {
				a.openNewWindow(paths[0])
			} else {
				a.OpenWorkspace(paths[0])
			}
		})
	}()
}

// OpenWorkspace 换到工作区 p（.code-workspace 或目录）：先问该问的（RequestLeave），再关掉现在的、打开 p。
// p 已经开在别的窗口里就把那个窗口叫到前面，这个窗口不换。
func (a *App) OpenWorkspace(p string) {
	p = absPath(p)
	if p == a.WsPath {
		a.Toast = i18n.Tf("已经开着工作区 %s", a.WsName())
		return
	}
	if a.Windows != nil && a.Windows.Find(p) != nil {
		a.Windows.Open(p, a)
		return
	}
	a.RequestLeave(p)
}

// Leave 是退出或换工作区之前一步一步问的（LeaveQuestion、LeaveAnswer）：没保存的文件、
// 在跑的 git 写命令，换工作区时还有终端里在跑的程序。不用问的步骤直接跳过。
type Leave struct {
	To      string // 要换去的工作区；空为退出
	Step    int    // LeaveDirty / LeaveWrites / LeaveJobs
	Open    bool   // 界面上开着这一步的对话框
	Waiting bool   // 选了中断，等在跑的 git 命令结束
	roots   []string
	jobs    []string
}

// Leave 的步骤。
const (
	LeaveDirty  = iota // 有文件没保存
	LeaveWrites        // 有 git 写命令在跑或排队
	LeaveJobs          // 换工作区时终端里有程序在跑
	leaveDone
)

// RequestLeave 开始退出（to 为空）或换到工作区 to：问完了才真的关窗口或换工作区。
// 工作区打不开就报错，现在的工作区不动。
func (a *App) RequestLeave(to string) {
	if l := a.Leave; l != nil && l.Waiting {
		a.Toast = i18n.T("正在中断 git 命令…")
		return
	}
	l := &Leave{To: to}
	if to != "" {
		roots, err := workspaceRoots(to)
		if err != nil {
			a.St.RecentWs = slices.DeleteFunc(a.St.RecentWs, func(s string) bool { return s == to })
			a.Fail(i18n.T("打不开工作区"), err)
			return
		}
		l.roots = roots
	}
	a.Leave = l
	a.leaveNext()
}

// leaveNext 从现在这一步往下找要问的；都问完了就退出或换工作区。
func (a *App) leaveNext() {
	l := a.Leave
	for ; l.Step < leaveDone; l.Step++ {
		if a.leaveNeeds(l) {
			l.Open = true
			return
		}
	}
	a.Leave = nil
	if l.To == "" {
		a.ForceClose = true
		if a.Win != nil {
			a.Win.Close()
		}
		return
	}
	a.switchTo(l.To, l.roots)
}

func (a *App) leaveNeeds(l *Leave) bool {
	switch l.Step {
	case LeaveDirty:
		return a.DirtyDocs() > 0
	case LeaveWrites:
		return len(a.WritesRunning()) > 0
	case LeaveJobs:
		if l.To == "" { // 退出时终端照旧直接关
			return false
		}
		l.jobs = nil
		a.eachPane(func(_ *Repo, p *TermPane) {
			if p.Daemon() { // 后台的会话只断开，命令照跑
				return
			}
			if j := p.Job(); j != "" {
				l.jobs = append(l.jobs, j)
			}
		})
		return len(l.jobs) > 0
	}
	return false
}

// LeaveQuestion 是这一步对话框的标题、正文和按钮（第一个是取消，最后一个是默认）。
func (a *App) LeaveQuestion() (title, msg string, buttons []string) {
	l := a.Leave
	if l == nil {
		return "", "", nil
	}
	quit := l.To == ""
	pick := func(q, s string) string {
		if quit {
			return i18n.T(q)
		}
		return i18n.T(s)
	}
	switch l.Step {
	case LeaveDirty:
		var names []string
		for _, d := range a.AllDocs() {
			if d.Modified() && d.Copy == 0 && len(names) < 3 {
				names = append(names, d.Title)
			}
		}
		list := i18n.List(names)
		if n := a.DirtyDocs(); n > len(names) {
			list += i18n.Tn(" 等 %d 个文件", n)
		}
		if quit {
			msg = i18n.Tf("%s还没保存，退出前要保存吗？", list)
		} else {
			msg = i18n.Tf("%s还没保存，换工作区前要保存吗？", list)
		}
		return i18n.T("有文件没保存"), msg, []string{i18n.T("取消"), pick("不保存退出", "不保存并切换"), pick("全部保存并退出", "全部保存并切换")}
	case LeaveWrites:
		ops := a.WritesRunning()
		list := i18n.List(ops[:min(len(ops), 3)])
		if len(ops) > 3 {
			list += i18n.Tf(" 等 %d 条", len(ops))
		}
		if quit {
			msg = i18n.Tf("%s 还没跑完。退出会中断正在跑的、不跑排着的。", list)
		} else {
			msg = i18n.Tf("%s 还没跑完。换工作区会中断正在跑的、不跑排着的。", list)
		}
		return i18n.T("还有 git 命令在跑"), msg, []string{i18n.T("取消"), pick("中断并退出", "中断并切换")}
	case LeaveJobs:
		return i18n.T("关掉终端？"), jobMessage(l.jobs), []string{i18n.T("取消"), i18n.T("结束并切换")}
	}
	return "", "", nil
}

// LeaveAnswer 是这一步对话框选了第 i 个按钮（0 和 Esc 是取消）。
func (a *App) LeaveAnswer(i int) {
	l := a.Leave
	if l == nil {
		return
	}
	l.Open = false
	if i <= 0 {
		a.Leave = nil
		a.leaveDropped()
		return
	}
	switch l.Step {
	case LeaveDirty:
		if i == 2 {
			a.SaveAll()
			if a.DirtyDocs() > 0 { // 有没存上的：WriteDoc 已经报了错，留下来
				a.Leave = nil
				a.leaveDropped()
				return
			}
		}
	case LeaveWrites:
		a.stopWrites()
		if len(a.WritesRunning()) > 0 {
			l.Waiting = true
			a.Toast = i18n.T("正在中断 git 命令…")
			return // writesStopped 接着走
		}
	}
	l.Step++
	a.leaveNext()
}

// stopWrites 停掉全部抓取 / 拉取，清空每个仓库的队列，取消正在跑的写命令和克隆。
func (a *App) stopWrites() {
	a.StopBatch()
	for _, cl := range a.Clones {
		a.CancelClone(cl)
	}
	for _, r := range a.Repos {
		for len(r.Queue) > 0 {
			a.Dequeue(r, 0)
		}
		a.CancelOp(r)
	}
}

// writesStopped 在一条写命令跑完后调（opDone）：在等中断的退出 / 换工作区，命令都结束了就接着走。
// 中断后留下了锁文件（opDone 弹了提示）就不走了，先让人看到提示。
func (a *App) writesStopped() {
	l := a.Leave
	if l == nil || !l.Waiting || len(a.WritesRunning()) > 0 {
		return
	}
	l.Waiting = false
	if a.AlertOpen {
		a.Leave = nil
		a.leaveDropped()
		return
	}
	l.Step++
	a.leaveNext()
}

// switchTo 换到工作区 p（问完之后）：存下现在的会话，关掉终端、文件监听、还在跑的 git 命令和所有标签，
// 按 p 和它上次的会话重新建应用的状态。跟着窗口和进程走的（窗口、设置、输出栏、命令面板的历史）留着。
func (a *App) switchTo(p string, roots []string) {
	a.NoteTermDirs()
	a.SaveState()
	a.Close()
	for _, d := range a.AllDocs() {
		d.Gen++ // 还在后台读的结果作废
	}
	if a.histTimer != nil {
		a.histTimer.Stop()
	}
	st := a.St
	st.Sessions = a.store().State().Sessions // 别的窗口存过的会话也在里面
	// 新的应用不在禅模式：没记过布局的工作区接着用进禅模式之前的三个面板
	if a.Zen {
		st.Sidebar, st.RightBar, st.BottomBar = a.zenWas[0], a.zenWas[1], a.zenWas[2]
	}
	if a.WsPath != "" {
		st.Added, st.Hidden = nil, nil // 现在这个工作区的已经存进它的会话（空工作区没有会话，老版本顶层的归给下一个）
	}
	n := newApp(p, roots, st)
	n.Win, n.Update, n.HasMenu, n.StateDir = a.Win, a.Update, a.HasMenu, a.StateDir
	n.Rend, n.RenderOpen, n.SettingsWin = a.Rend, a.RenderOpen, a.SettingsWin // 设置窗口跟着进程走、换工作区不关：导出 / 导入的面板还挂在它上面
	n.Windows, n.Slot, n.LastBar, n.ownStore = a.Windows, a.Slot, a.LastBar, a.ownStore
	n.Trash, n.OpenURL, n.ShowPath, n.InFinder, n.OpenDialog, n.SaveDialog = a.Trash, a.OpenURL, a.ShowPath, a.InFinder, a.OpenDialog, a.SaveDialog
	n.OnSettings, n.OpenSettings, n.OpenAbout, n.OnRecent, n.OnClearRecent = a.OnSettings, a.OpenSettings, a.OpenAbout, a.OnRecent, a.OnClearRecent
	n.LogDir, n.LastCrash = a.LogDir, a.LastCrash
	n.Output, n.OutList, n.BottomTab = a.Output, a.OutList, a.BottomTab
	n.Palette.History = a.Palette.History
	n.AlertOpen, n.AlertErr, n.AlertTitle, n.AlertMsg, n.AlertDetail = a.AlertOpen, a.AlertErr, a.AlertTitle, a.AlertMsg, a.AlertDetail // 取消后留下锁文件的提示
	// 旧的后台回调靠这几个计数认出自己过期了
	n.Search.gen, n.Search.tick, n.histTick, n.paneSeq = a.Search.gen+1, a.Search.tick+1, a.histTick+1, a.paneSeq
	*a = *n
	for _, r := range a.Repos {
		r.set = &a.St.Settings // newRepo 给的是 n 的设置，n 拷进 a 之后要指到 a 的
	}
	if a.Win != nil {
		a.Win.SetTitle(a.Title())
	}
	a.Start()
	a.Toast = i18n.Tf("已打开工作区 %s", a.WsName())
}
