package app

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/termd"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是底栏的终端：每个仓库一排终端标签，每个标签一格或分屏的几格，每格一个 shell。
// shell 第一次要显示时才启动（懒启动：底栏开着、当前仓库是它、是它当前的标签）；切仓库再切回来
// 还是同一个 shell，回滚都在。标签的名字、顺序、分屏的样子和每格 shell 所在的目录记进会话；关窗口时
// 在应用进程里的格的回滚写进 termhist（termhist.go），下次启动这一格时喂回去。
// 设置里开了"关掉窗口后终端继续在后台运行"时 shell 跑在终端后台进程里（termd.go），会话还记各格的 sid、接回来时按格还原。

// MaxPanes 是一个终端标签最多分的格数。
const MaxPanes = 8

// TermPane 是终端的一格：一个 shell。
type TermPane struct {
	ID       int    // 窗口里唯一，界面拿它当视图的 key
	Dir      string // 启动的目录；空为仓库根
	Term     *terminal.Terminal
	Err      error // 启动失败的原因
	Title    string
	Exited   bool
	ExitCode int
	Find     TermFind
	View     ui.Handle // 终端视图：给键盘
	Sid      string    // shell 在终端后台进程（termd）里的会话；空为 shell 在应用进程里（还原的格接上之前就有）
	Profile  string    // 用的终端配置（设置里"其他配置"的名字）；空为默认。重新启动沿用，分出来的格跟着

	gen   int         // 每次（重）启动递增，旧 shell 的回调据此作废；0 为还没启动过
	pid   int         // shell 的进程号，0 为还不知道（关之前用它看前台有没有命令在跑）
	cwd   string      // 最近一次用 lsof 读到的 shell 的目录（shell 不报告目录时会话里记它）
	conn  *termd.Conn // 接着的 termd 会话；nil 为 shell 在应用进程里（或还没连上）
	dial  *termDial   // 正在连 termd
	lost  string      // 和 termd 的连接断了的说明（会话没了、被别的窗口接走了）
	rdir  string      // termd 那边 shell 报的目录（OSC 7）：镜像帧里没有它，这时 Term.Dir() 是空的
	ready []func()    // 终端建好之后要做的（连 termd 时 RunInTerminal 排的）
	hist  string      // termhist 里这一格的回滚文件：还原的格启动前是上次的，关窗口时写的是这次的；空为没有
	feed  []byte      // 启动时读回来的上次的回滚（带分隔行），startLocal 建好终端马上喂给它

	// agent 的状态（agents.go）
	bellAt time.Time    // 响铃 / 要通知的时候（用户没在看这一格）；零为没在等你，切进这一格时清
	outNs  atomic.Int64 // 最近有输出的时候（UnixNano）：termd 的格读连接的 goroutine 上记，本地的看屏幕变没变
	runAt  time.Time    // 前台的 agent 这一段从什么时候起有输出；不在运行为零
	job    string       // 后台轮询最近读到的前台命令
}

// shellDir 是 shell 报告的目录（OSC 7），没报告为空。
func (p *TermPane) shellDir() string {
	if p.Term != nil {
		if d := p.Term.Dir(); d != "" {
			return d
		}
	}
	return p.rdir
}

// Started 是这一格的 shell 启动过（懒启动之前为假）。
func (p *TermPane) Started() bool { return p.gen > 0 }

// Gen 是这一格第几次启动：界面拿它和 ID 一起当终端视图的 key，重新启动就换一个视图。
func (p *TermPane) Gen() int { return p.gen }

// TermFind 是一格终端上的查找条。
type TermFind struct {
	Open  bool
	Text  string
	Focus bool // 下一帧把键盘给查找框
}

// TermNode 是一个终端标签里的分屏：叶子是一格（Pane），否则是左右（Row）或上下分的两半，A 占 Ratio。
type TermNode struct {
	Pane  *TermPane
	Row   bool
	Ratio float32
	A, B  *TermNode
}

// TermTab 是一个终端标签：在底栏（Repo.Terms），或开在编辑器区（Repo.EdTerms，InEditor；termeditor.go）。
type TermTab struct {
	Name     string // 用户起的名字；空为默认的（Repo.TermLabel）
	Num      int    // 这个仓库里第几个开的，默认名用；编辑器区的 docs.Term 标签拿它认
	Root     *TermNode
	Active   *TermPane // 最近有键盘的一格：分屏、清屏、查找、在终端运行都对它
	InEditor bool      // 开在编辑器区

	saved int // 还原的编辑器区标签在会话里记的号（会话里的 docs.Term 标签按它对上），对上了清零
}

// allTerms 是仓库所有的终端标签：底栏的和编辑器区的。
func (r *Repo) allTerms() []*TermTab { return append(slices.Clip(r.Terms), r.EdTerms...) }

// tabOf 是格 p 所在的终端标签（底栏的或编辑器区的）；不在这个仓库里为 nil。
func (r *Repo) tabOf(p *TermPane) *TermTab {
	for _, t := range r.allTerms() {
		if t.Root.leaf(p) != nil {
			return t
		}
	}
	return nil
}

// Panes 是标签里的格，从左到右、从上到下。
func (t *TermTab) Panes() []*TermPane {
	var out []*TermPane
	var walk func(n *TermNode)
	walk = func(n *TermNode) {
		if n.Pane != nil {
			out = append(out, n.Pane)
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(t.Root)
	return out
}

// leaf 是 p 所在的叶子，没有为 nil。
func (n *TermNode) leaf(p *TermPane) *TermNode {
	if n.Pane != nil {
		if n.Pane == p {
			return n
		}
		return nil
	}
	if l := n.A.leaf(p); l != nil {
		return l
	}
	return n.B.leaf(p)
}

// cut 把 p 从分屏里拿掉，另一半占满两半的位置，返回占过来的那一半；p 是唯一的一格时返回 nil。
func (n *TermNode) cut(p *TermPane) *TermNode {
	if n.Pane != nil {
		return nil
	}
	for _, kid := range [2]struct{ me, other *TermNode }{{n.A, n.B}, {n.B, n.A}} {
		if kid.me.Pane == p {
			*n = *kid.other
			return n
		}
	}
	if s := n.A.cut(p); s != nil {
		return s
	}
	return n.B.cut(p)
}

// first 是这一半里第一格。
func (n *TermNode) first() *TermPane {
	for n.Pane == nil {
		n = n.A
	}
	return n.Pane
}

// TermLabel 是终端标签显示的名字：用户起的，或者第一个"终端 · 仓库名"、往后"终端 n"；
// 第一格用的是终端配置的，"终端"换成配置名（"fish · 仓库名"、"fish 2"）。
func (r *Repo) TermLabel(t *TermTab) string {
	prof := ""
	if t.Root != nil {
		prof = t.Root.first().Profile
	}
	switch {
	case t.Name != "":
		return t.Name
	case prof != "" && t.Num <= 1:
		return prof + " · " + r.Name
	case prof != "":
		return prof + " " + strconv.Itoa(t.Num)
	case t.Num <= 1:
		return i18n.Tf("终端 · %s", r.Name)
	}
	return i18n.Tf("终端 %d", t.Num)
}

// TermTabs 是仓库的终端标签；第一次问时按会话建好（不启动 shell）。
func (a *App) TermTabs(r *Repo) []*TermTab {
	a.initTerms(r)
	return r.Terms
}

// CurTermTab 是仓库当前的终端标签；一个都没有（全关了）为 nil。
func (a *App) CurTermTab(r *Repo) *TermTab {
	a.initTerms(r)
	if len(r.Terms) == 0 {
		return nil
	}
	r.TermSel = min(max(r.TermSel, 0), len(r.Terms)-1)
	return r.Terms[r.TermSel]
}

// TermRepo 是底栏的终端属于哪个仓库：当前仓库；一个仓库都没有（空工作区）时是家目录 ~，
// 它不在仓库列表里，终端也不记进会话。
func (a *App) TermRepo() *Repo {
	if r := a.Current(); r != nil {
		return r
	}
	if a.home == nil {
		dir, err := os.UserHomeDir()
		if err != nil {
			dir = "/"
		}
		a.home = NewRepo(dir)
		a.home.Name, a.home.Plain = "~", true
	}
	return a.home
}

// ActivePane 是当前仓库（TermRepo）当前终端标签里有键盘的那一格；没有为 nil。
// 键盘在编辑器区的终端里时（TermFocused）是那个标签的：终端的快捷键、菜单都对它。
func (a *App) ActivePane() (*Repo, *TermTab, *TermPane) {
	if t, er := a.edTab, a.edRepo; a.TermFocused && t != nil && er != nil && slices.Contains(er.EdTerms, t) {
		return er, t, t.Current()
	}
	r := a.TermRepo()
	tab := a.CurTermTab(r)
	if tab == nil {
		return r, nil, nil
	}
	return r, tab, tab.Current()
}

// Current 是标签里有键盘的那一格（Active；它已经关了就是第一格）。
func (t *TermTab) Current() *TermPane {
	if t.Active == nil || t.Root.leaf(t.Active) == nil {
		t.Active = t.Root.first()
	}
	return t.Active
}

// initTerms 建仓库的终端标签：会话里记着的，没有就一个在仓库根的。不启动 shell。
func (a *App) initTerms(r *Repo) {
	if r.termsInit {
		return
	}
	r.termsInit = true
	if s := r.savedTerms; s != nil && len(s.Tabs) > 0 {
		var gone []string // 会话里记的、设置里已经没有了的终端配置
		for _, t := range s.Tabs {
			var tab *TermTab
			if t.InEditor { // 编辑器区的：restoreTabs 按 saved 对上会话里的 docs.Term 标签，对不上的放回底栏
				tab = a.newTermTab(r, t.Name, t.Dir)
				tab.InEditor, tab.saved = true, max(t.Num, 1)
				r.EdTerms = append(r.EdTerms, tab)
			} else {
				tab = a.addTermTab(r, len(r.Terms), t.Name, t.Dir)
			}
			if t.Split != nil {
				gone = append(gone, a.restoreSplit(r, tab, t.Split)...)
			} else {
				a.restoreSids(r, tab, t)
			}
		}
		if len(r.Terms) == 0 { // 底栏的全挪到编辑器区了：底栏照旧有一个
			a.addTermTab(r, 0, "", "")
		}
		r.TermSel = s.Active
		if len(gone) > 0 {
			a.Toast = i18n.Tf("终端配置“%s”没有了，用默认的 shell", gone[0])
		}
	} else {
		a.addTermTab(r, 0, "", "")
	}
	r.savedTerms = nil
}

func (a *App) addTermTab(r *Repo, at int, name, dir string) *TermTab {
	t := a.newTermTab(r, name, dir)
	r.Terms = slices.Insert(r.Terms, at, t)
	return t
}

// newTermTab 是仓库里新的一个终端标签（一格，还没启动），不放进哪一边。
func (a *App) newTermTab(r *Repo, name, dir string) *TermTab {
	r.termNum++
	p := a.newPane(r, dir)
	return &TermTab{Name: name, Num: r.termNum, Root: &TermNode{Pane: p}, Active: p}
}

// restoreSids 给还原的标签接上会话里记的 termd 会话：一个 sid 一格，几格从左到右等分（分屏的样子不记）。
// 接回去在各格第一次显示时（StartPane）。
func (a *App) restoreSids(r *Repo, tab *TermTab, t workspace.TermTab) {
	sids := t.Sids
	if len(sids) == 0 {
		return
	}
	sids = sids[:min(len(sids), MaxPanes)]
	panes := []*TermPane{tab.Root.Pane}
	panes[0].Sid = sids[0]
	for _, sid := range sids[1:] {
		q := a.newPane(r, t.Dir)
		q.Sid = sid
		panes = append(panes, q)
	}
	tab.Root, tab.Active = chainPanes(panes), panes[0]
}

// restoreSplit 按会话里记的分屏树建标签的格：每格自己的目录、终端配置、termd 会话和上次的回滚，
// 有键盘的是第一格。都不启动。返回设置里已经没有了的配置（那几格用默认的）。
func (a *App) restoreSplit(r *Repo, tab *TermTab, s *workspace.TermSplit) (gone []string) {
	var build func(s *workspace.TermSplit) *TermNode
	build = func(s *workspace.TermSplit) *TermNode {
		if s.A == nil {
			p := a.newPane(r, s.Dir)
			p.Sid, p.hist, p.Profile = s.Sid, s.Hist, s.Profile
			if p.Profile != "" && a.St.Settings.Profile(p.Profile) == nil {
				gone, p.Profile = append(gone, p.Profile), ""
			}
			return &TermNode{Pane: p}
		}
		return &TermNode{Row: s.Row, Ratio: s.Ratio, A: build(s.A), B: build(s.B)}
	}
	tab.Root = build(s)
	tab.Active = tab.Root.first()
	return gone
}

// splitOf 是分屏树 n 在会话里的样子。
func (a *App) splitOf(r *Repo, n *TermNode) *workspace.TermSplit {
	if p := n.Pane; p != nil {
		dir := a.paneDir(p, false)
		if atRoot(r, dir) {
			dir = ""
		}
		s := &workspace.TermSplit{Dir: dir, Hist: p.hist, Profile: p.Profile}
		if p.Sid != "" && p.lost == "" && !p.Exited {
			s.Sid = p.Sid
		}
		return s
	}
	return &workspace.TermSplit{Row: n.Row, Ratio: n.Ratio, A: a.splitOf(r, n.A), B: a.splitOf(r, n.B)}
}

// chainPanes 是从左到右等分的几格。
func chainPanes(ps []*TermPane) *TermNode {
	if len(ps) == 1 {
		return &TermNode{Pane: ps[0]}
	}
	return &TermNode{Row: true, Ratio: 1 / float32(len(ps)), A: &TermNode{Pane: ps[0]}, B: chainPanes(ps[1:])}
}

func (a *App) newPane(r *Repo, dir string) *TermPane {
	if atRoot(r, dir) {
		dir = ""
	}
	a.paneSeq++
	return &TermPane{ID: a.paneSeq, Dir: dir}
}

// EnsurePane 在这一格第一次要显示时启动它的 shell（懒启动）；启动过的（退出了、失败了也算）不再自己启动。
func (a *App) EnsurePane(r *Repo, p *TermPane) {
	if !p.Started() {
		a.StartPane(r, p)
	}
}

// StartPane 启动（或重新启动）一格的 shell：在它的目录（没有了就仓库根）里，用设置里的 shell、字体和配色。
// 后台模式（设置开着，或者这一格是还原的 termd 会话）在后台连 termd，连上之前格里转圈；重新启动的关掉原来的会话、开个新的。
func (a *App) StartPane(r *Repo, p *TermPane) {
	if p.Term != nil || p.dial != nil {
		closeTerm(p, true)
		p.Sid = ""
	}
	p.gen++
	gen := p.gen
	p.Term, p.Err, p.Title, p.Exited, p.ExitCode, p.pid, p.Find = nil, nil, "", false, 0, 0, TermFind{}
	p.lost, p.rdir, p.ready = "", "", nil
	p.bellAt, p.runAt, p.job = time.Time{}, time.Time{}, ""
	p.outNs.Store(0)
	p.feed = a.takeHist(p) // 接到 termd 上的用不着（attachPane 丢掉），连不上退回本地的照样喂
	dir := r.Root
	if p.Dir != "" {
		if fi, err := os.Stat(p.Dir); err == nil && fi.IsDir() {
			dir = p.Dir
		}
	}
	if sock := a.termdSock(); sock != "" && (p.Sid != "" || a.St.Settings.TermDaemon) {
		a.dialPane(r, p, gen, dir, sock)
		return
	}
	a.startLocal(r, p, gen, dir)
}

// startLocal 在应用进程里起这一格的 shell。
func (a *App) startLocal(r *Repo, p *TermPane, gen int, dir string) {
	term, err := terminal.New(a.paneOptions(r, p, gen, dir))
	if err != nil {
		p.Err = err
		return
	}
	if f := p.feed; f != nil {
		// 上次的回滚赶在 shell 的第一笔输出（要读 rc，几十毫秒）之前：History 让光标落在它下面，提示符接着出
		p.feed = nil
		start := time.Now()
		term.Feed(f)
		log.Printf("终端回滚：喂回 %d 字节，%v", len(f), time.Since(start).Round(time.Microsecond))
	}
	p.Term = term
	if !slices.Contains(a.pidWait, p) {
		a.pidWait = append(a.pidWait, p)
	}
	a.pollPids()
}

// paneOptions 是一格终端的选项（两种模式都用）：链接、标题、退出、终端的局部快捷键和设置里的字体配色。
func (a *App) paneOptions(r *Repo, p *TermPane, gen int, dir string) terminal.Options {
	up := a.Update // 下面几个回调在终端自己的 goroutine 上调
	opts := terminal.Options{
		Dir:          dir,
		Padding:      terminal.Padding{X: 12, Y: 8},
		PaddingColor: terminal.PaddingExtend,
		// ⌘点击 的路径相对 shell 现在的目录（cd 过就不是启动的目录了）
		OpenLink: func(link string) bool {
			d := dir
			if s := p.shellDir(); s != "" {
				d = s
			}
			return a.OpenLink(d, link)
		},
		OnTitle: func(title string) {
			up(func() {
				if p.gen == gen {
					p.Title = title
				}
			})
		},
		OnExit: func(code int) {
			up(func() {
				if p.gen == gen {
					p.Exited, p.ExitCode = true, code
				}
			})
		},
		// 响铃（BEL，OSC 里当结束符的不算）和通知（OSC 9 / 777）：用户没在看这一格就是 agent 在等你
		OnBell:   func() { up(func() { a.paneBell(p, gen) }) },
		OnNotify: func(string, string) { up(func() { a.paneBell(p, gen) }) },
		// 窗口没登记的键（⌘D 分屏、⇧⌘[ ⇧⌘] 切标签）先到这里；编辑器自己要 ⌘D，所以不放到窗口上
		OnKey: func(mods ui.Modifiers, key ui.Key) bool { return a.termKey(r, p, mods, key) },
	}
	a.termOptions(&opts, p.Profile)
	return opts
}

// termKey 是终端视图的 OnKey（主线程，按键事件里）：终端的局部快捷键交给命令执行，别的键照常给 shell。
// 终端自带的字号键（⌘= ⇧⌘= ⌘- ⌘0）让给界面缩放：菜单和窗口快捷键本来就先接到，这里接住剩下的 ⇧⌘=。
// 自定义的终端键可以不带 ⌘，所以每个键都查一遍注册表。
func (a *App) termKey(r *Repo, p *TermPane, mods ui.Modifiers, key ui.Key) bool {
	if zoom := a.termZoomKeyFor(mods, key); mods&ui.Super != 0 && zoom != nil {
		a.Update(zoom)
		return true
	}
	for _, c := range Commands {
		if c.Context == Terminal && c.Mods == mods && c.Key == key && c.Run != nil {
			a.Update(func() { // 放到下一帧前跑：改了底栏要重画
				a.focusPane(r, p)
				a.TermFocused = true
				c.Run(a)
			})
			return true
		}
	}
	return false
}

// termZoomKeyFor 是终端里按到的界面缩放键要跑的；不是为 nil。
func (a *App) termZoomKeyFor(mods ui.Modifiers, key ui.Key) func() {
	switch {
	case key == ui.KeyEqual && (mods == ui.Super || mods == ui.Super|ui.Shift):
		return a.ZoomIn
	case key == ui.KeyMinus && mods == ui.Super:
		return a.ZoomOut
	case key == ui.Key0 && mods == ui.Super:
		return a.ZoomReset
	}
	return nil
}

// FocusPane 在界面上记下键盘在哪一格（终端视图有焦点的那一帧）：它所在的标签当前、这一格是标签里有键盘的。
func (a *App) FocusPane(r *Repo, p *TermPane) {
	a.TermFocused = true
	a.focusPane(r, p)
	p.seen()
}

func (a *App) focusPane(r *Repo, p *TermPane) {
	for i, t := range r.Terms {
		if t.Root.leaf(p) != nil {
			r.TermSel, t.Active = i, p
			a.edTab, a.edRepo = nil, nil
			return
		}
	}
	for _, t := range r.EdTerms {
		if t.Root.leaf(p) != nil {
			t.Active = p
			a.edTab, a.edRepo = t, r
			return
		}
	}
}

// FocusTermTab 下一帧把键盘给终端标签 tab 里有键盘的那一格（底栏的或编辑器区的；改名、关查找条、关了一格之后）。
func (a *App) FocusTermTab(tab *TermTab) { a.FocusTerm, a.FocusTab = true, tab }

// focusBottom 下一帧把键盘给底栏当前的终端标签。
func (a *App) focusBottom() { a.FocusTerm, a.FocusTab = true, nil }

// showTerm 切到仓库、打开底栏的终端页，下一帧把键盘给它当前的那一格。
func (a *App) showTerm(r *Repo) {
	a.UseRepo(r)
	a.St.BottomBar = true
	a.BottomTab = BottomTerminal
	a.focusBottom()
}

// NewTerminal 在仓库里开一个新的终端标签（排在当前标签后面），shell 在 dir 里启动（空为仓库根）；
// 切到这个仓库、打开底栏、把键盘给它（⌘T、"在此打开终端"）。
func (a *App) NewTerminal(r *Repo, dir string) { a.NewTerminalProfile(r, dir, "") }

// NewTerminalProfile 和 NewTerminal 一样，shell 按终端配置 profile 开（"" 或设置里没有了是默认）。
func (a *App) NewTerminalProfile(r *Repo, dir, profile string) {
	a.initTerms(r)
	at := 0
	if len(r.Terms) > 0 {
		at = min(r.TermSel+1, len(r.Terms))
	}
	if a.St.Settings.Profile(profile) == nil {
		profile = ""
	}
	a.addTermTab(r, at, "", dir).Root.Pane.Profile = profile
	r.TermSel = at
	a.showTerm(r)
	a.SaveState()
}

// SelectTermTab 切到仓库的第 i 个终端标签。
func (a *App) SelectTermTab(r *Repo, i int) {
	if i < 0 || i >= len(r.Terms) {
		return
	}
	r.TermSel = i
	a.BottomTab = BottomTerminal
	a.focusBottom()
	a.SaveState()
}

// StepTermTab 切到当前仓库的下一个（d = 1）/ 上一个（d = -1）终端标签，到头了绕回来。
func (a *App) StepTermTab(d int) {
	r := a.TermRepo()
	if len(r.Terms) < 2 {
		return
	}
	n := len(r.Terms)
	a.SelectTermTab(r, ((r.TermSel+d)%n+n)%n)
}

// MoveTermTab 把终端标签从 from 挪到 to（拖标签）。
func (a *App) MoveTermTab(r *Repo, from, to int) {
	if from == to || from < 0 || to < 0 || from >= len(r.Terms) || to >= len(r.Terms) {
		return
	}
	t := r.Terms[from]
	r.Terms = slices.Insert(slices.Delete(r.Terms, from, from+1), to, t)
	r.TermSel = to
	a.SaveState()
}

// SplitTerm 把当前终端标签里有键盘的那一格分成两半（row 为左右，否则上下），新的一格在右 / 下，
// 从那一格 shell 现在的目录启动、拿到键盘。键盘在编辑器区的终端里时分的是那个标签。
func (a *App) SplitTerm(r *Repo, row bool) {
	if er, tab, p := a.ActivePane(); tab != nil && tab.InEditor {
		a.SplitPane(er, tab, p, row)
		return
	}
	tab := a.CurTermTab(r)
	if tab == nil {
		a.NewTerminal(r, "")
		return
	}
	if r != a.TermRepo() {
		a.UseRepo(r)
	}
	a.SplitPane(r, tab, tab.Current(), row)
}

// SplitPane 把终端标签 tab 里的一格 p 分成两半（编辑器区标签的工具栏、SplitTerm）。
func (a *App) SplitPane(r *Repo, tab *TermTab, p *TermPane, row bool) {
	leaf := tab.Root.leaf(p)
	if leaf == nil {
		return
	}
	if len(tab.Panes()) >= MaxPanes {
		a.Toast = i18n.Tf("一个终端标签最多分 %d 格", MaxPanes)
		return
	}
	q := a.newPane(r, a.paneDir(p, true))
	q.Profile = p.Profile
	old := *leaf
	*leaf = TermNode{Row: row, Ratio: 0.5, A: &old, B: &TermNode{Pane: q}}
	tab.Active = q
	if tab.InEditor {
		a.FocusTermTab(tab)
		return
	}
	a.showTerm(r)
}

// paneDir 是 shell 现在的目录：shell 报告的（OSC 7），或 lsof 读的（live 为真时现读，否则用上次读的），
// 都没有就是启动的目录；在仓库根为 ""。
func (a *App) paneDir(p *TermPane, live bool) string {
	d := ""
	if p.Term != nil && !p.Exited {
		d = p.shellDir()
		if d == "" && live && p.pid != 0 {
			d = procCwd(p.pid)
		}
	}
	if d == "" {
		d = p.cwd
	}
	if d == "" {
		d = p.Dir
	}
	return d
}

// ClosePane 关掉一格（⌘W、退出条上的"关闭"）：前台有命令在跑先问。分屏里关一格另一半占满，标签里最后一格关了标签也关。
func (a *App) ClosePane(r *Repo, tab *TermTab, p *TermPane) {
	do := func() { a.dropPane(r, tab, p) }
	if job := p.Job(); job != "" {
		a.Ask(i18n.T("关闭终端？"), jobMessage([]string{job}), i18n.T("关闭"), do)
		return
	}
	do()
}

// CloseActivePane 是 ⌘W（焦点在终端里）：关掉有键盘的那一格（编辑器区的终端里是那个标签的）。
func (a *App) CloseActivePane(*Repo) {
	if r, tab, p := a.ActivePane(); p != nil {
		a.ClosePane(r, tab, p)
	}
}

// CloseTermTab 关掉一个终端标签（标签上的 ×、菜单）：它的格里前台有命令在跑先问。
func (a *App) CloseTermTab(r *Repo, tab *TermTab) {
	var jobs []string
	for _, p := range tab.Panes() {
		if j := p.Job(); j != "" {
			jobs = append(jobs, j)
		}
	}
	do := func() { a.closeTab(r, tab) }
	if len(jobs) > 0 {
		a.Ask(i18n.T("关闭终端标签？"), jobMessage(jobs), i18n.T("关闭"), do)
		return
	}
	do()
}

// jobMessage 是关闭 / 重新启动终端前问的那句话。
func jobMessage(jobs []string) string {
	named := slices.DeleteFunc(slices.Clone(jobs), func(j string) bool { return j == "?" })
	if len(named) == 0 {
		return i18n.T("终端里可能还有命令在运行，关掉会结束它。")
	}
	return i18n.Tf("“%s”还在运行，关掉终端会结束它。", strings.Join(slices.Compact(named), "”、“"))
}

func (a *App) dropPane(r *Repo, tab *TermTab, p *TermPane) {
	if r.tabOf(p) != tab {
		return
	}
	rest := tab.Root.cut(p)
	if rest == nil {
		a.closeTab(r, tab)
		return
	}
	a.killPane(p)
	if tab.Active == p {
		tab.Active = rest.first()
	}
	a.syncTermDoc(r, tab) // 默认名跟着第一格的终端配置
	a.FocusTermTab(tab)
	a.SaveState()
}

// closeTab 关掉终端标签（不问）：底栏的 dropTab，编辑器区的连它的标签一起关。
func (a *App) closeTab(r *Repo, tab *TermTab) {
	if i := slices.Index(r.Terms, tab); i >= 0 {
		a.dropTab(r, i)
	} else if tab.InEditor {
		a.closeEdTab(r, tab)
	}
}

func (a *App) dropTab(r *Repo, i int) {
	for _, p := range r.Terms[i].Panes() {
		a.killPane(p)
	}
	r.Terms = slices.Delete(r.Terms, i, i+1)
	if r.TermSel > i || r.TermSel >= len(r.Terms) {
		r.TermSel = max(r.TermSel-1, 0)
	}
	if a.Rename.Tab != nil && !slices.Contains(r.allTerms(), a.Rename.Tab) {
		a.Rename = TermRename{}
	}
	a.FocusTerm, a.FocusTab = len(r.Terms) > 0, nil
	a.SaveState()
}

// killPane 关掉一格（关格、关标签、移除仓库）：shell 结束，后台的会话也关掉（还没接上的按 sid 关）。
func (a *App) killPane(p *TermPane) {
	if p.conn == nil && p.dial == nil && p.Sid != "" {
		a.killSaved(p.Sid)
	}
	p.Sid = ""
	a.dropHist(p.hist)
	p.hist, p.feed = "", nil
	closePane(p, true)
}

// closePane 关掉一格的 shell：kill 为假（关窗口、换工作区）时后台的会话只断开，留在 termd 里。
func closePane(p *TermPane, kill bool) {
	p.gen++ // 旧 shell 的回调作废
	closeTerm(p, kill)
}

// closeTerm 关掉一格的终端。在应用进程里、还没认领到进程号的，等它的 shell 退干净（最多 300 ms，一般几毫秒）：
// 不然它还挂在应用的子进程里，会被下一个排队的格按顺序认走。后台的会话 kill 为真时关掉，否则断开；还在连的，连上了照办。
// shell 已经退出了的会话也关掉（留着没用，会话里也不记它）；被别的窗口接走的（lost）只断开。
func closeTerm(p *TermPane, kill bool) {
	kill = kill || p.Exited && p.lost == ""
	if d := p.dial; d != nil {
		d.drop = dropDetach
		if kill {
			d.drop = dropKill
		}
		p.dial = nil
	}
	if c := p.conn; c != nil {
		if kill {
			c.Kill()
		} else {
			c.Detach()
		}
		p.conn = nil
		forgetTermd()
	}
	t := p.Term
	if t == nil {
		return
	}
	p.Term = nil
	t.Close()
	if p.pid == 0 {
		select {
		case <-t.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// RestartPane 重新启动一格的 shell（在它启动的目录）：前台有命令在跑先问。
func (a *App) RestartPane(r *Repo, p *TermPane) {
	do := func() {
		a.StartPane(r, p)
		a.FocusTermTab(r.tabOf(p))
	}
	if job := p.Job(); job != "" {
		a.Ask(i18n.T("重新启动终端？"), jobMessage([]string{job}), i18n.T("重新启动"), do)
		return
	}
	do()
}

// RestartTerm 重新启动当前终端标签里有键盘的那一格（菜单、仓库的右键菜单）；一个标签都没有就新开一个。
func (a *App) RestartTerm(r *Repo) {
	if r != a.TermRepo() {
		a.UseRepo(r)
	}
	_, _, p := a.ActivePane()
	if p == nil {
		a.NewTerminal(r, "")
		return
	}
	a.showTerm(r)
	a.RestartPane(r, p)
}

// TermRename 是正在改名的终端标签（双击标签）。
type TermRename struct {
	Tab     *TermTab
	Text    string
	Started bool // 输入框建过了（第一帧给它键盘、全选）
	Focused bool // 输入框拿到过键盘（之后丢了键盘就是点到别处去了，按回车算）
}

// CancelRenameTerm 是改名时按 Esc：名字不变，键盘还给终端。
func (a *App) CancelRenameTerm() {
	a.FocusTermTab(a.Rename.Tab)
	a.Rename = TermRename{}
}

// StartRenameTerm 开始给终端标签改名：标签变成输入框，里面是现在的名字。
func (a *App) StartRenameTerm(r *Repo, tab *TermTab) {
	a.Rename = TermRename{Tab: tab, Text: r.TermLabel(tab)}
}

// RenameTerm 给终端标签改名；空的、和默认名一样的回到默认名。
func (a *App) RenameTerm(r *Repo, tab *TermTab, name string) {
	a.Rename = TermRename{}
	name = strings.TrimSpace(name)
	tab.Name = ""
	if name != r.TermLabel(tab) {
		tab.Name = name
	}
	a.syncTermDoc(r, tab)
	a.SaveState()
}

// ClearTerm 是 ⌘K（焦点在终端里）：清掉有键盘的那一格的屏幕和回滚，提示符留着；全屏程序里不清。
// 后台的会话交给 termd 清（下一帧就是清过的；全屏程序里它回一声，OnNoClear 提示）。
func (a *App) ClearTerm() {
	_, _, p := a.ActivePane()
	switch {
	case p == nil || p.Term == nil:
	case p.conn != nil:
		p.conn.Clear()
	case !p.Term.ClearScreen():
		a.Toast = i18n.T("全屏程序里不清屏")
	}
}

// FindInTerm 是 ⌘F（焦点在终端里）：在有键盘的那一格上打开查找条、把键盘给它。
func (a *App) FindInTerm() {
	if _, _, p := a.ActivePane(); p != nil {
		a.FindInPane(p)
	}
}

// FindInPane 在一格上打开查找条、把键盘给它（编辑器区终端标签卡片头上的放大镜）。
func (a *App) FindInPane(p *TermPane) {
	if p.Term != nil {
		p.Find.Open, p.Find.Focus = true, true
	}
}

// SearchTerm 在一格终端的屏幕和回滚里找 text（查找条里敲字）；空的结束查找。
func (a *App) SearchTerm(p *TermPane, text string) {
	p.Find.Text = text
	if p.Term != nil {
		p.Term.Search(text)
	}
}

// StepTermFind 是 ⌘G / ⇧⌘G：选下一个（往旧的输出找）/ 上一个匹配；查找条没开先打开。
func (a *App) StepTermFind(d int) {
	_, _, p := a.ActivePane()
	if p == nil || p.Term == nil {
		return
	}
	if !p.Find.Open || p.Find.Text == "" {
		a.FindInTerm()
		return
	}
	if d > 0 {
		p.Term.SearchNext()
	} else {
		p.Term.SearchPrevious()
	}
}

// EndTermFind 关掉一格的查找条（Esc、×），去掉高亮，键盘还给终端。
func (a *App) EndTermFind(p *TermPane) {
	if p.Term != nil {
		p.Term.EndSearch()
	}
	p.Find = TermFind{}
	a.FocusTermTab(a.tabOfPane(p))
}

// RunInTerminal 把命令放进当前仓库终端（有键盘的那一格）的输入行，不回车（命令面板里"在终端运行"）。
// shell 还没启动就先启动，等它出了提示符再放；退出了的重新启动。
func (a *App) RunInTerminal(line string) { a.termInput(a.TermRepo(), line, false, false) }

// termInput 把 line 放进仓库 r 当前终端标签有键盘的那一格的输入行（一个标签都没有、newTab 为真时新开一个），
// enter 为真时再回车；切到这个仓库、打开底栏、键盘给终端。还没启动、已经退出的那一格先（重新）启动，出了提示符再放。
func (a *App) termInput(r *Repo, line string, enter, newTab bool) {
	if line == "" {
		return
	}
	if newTab || a.CurTermTab(r) == nil {
		a.NewTerminal(r, "")
	}
	p := a.CurTermTab(r).Current()
	a.showTerm(r)
	fresh := !p.Started() || p.Exited
	if fresh {
		a.StartPane(r, p)
	}
	if p.Term == nil && p.dial != nil { // 在连 termd：接上了再放
		p.ready = append(p.ready, func() {
			if p.Term != nil {
				a.typeLater(p, p.Term, line, enter)
			}
		})
		return
	}
	t := p.Term
	if t == nil {
		a.Toast = i18n.T("终端启动失败")
		return
	}
	if !fresh {
		t.Paste(line)
		if enter {
			t.Send([]byte("\r"))
		}
		return
	}
	a.typeLater(p, t, line, enter)
}

// runGitInTerm 把要人输入的 git 命令（line 是 git 后面的原样参数）送进仓库 r 的终端回车跑（>git）。
// 当前那一格前台有命令在跑、shell 不在这个仓库里（cd 走了、在套着的别的仓库里）就新开一个标签：免得敲进别的程序、跑错仓库。
func (a *App) runGitInTerm(r *Repo, line string) {
	reuse := false
	if tab := a.CurTermTab(r); tab != nil {
		p := tab.Current()
		job := p.Job()
		reuse = (job == "" || job == "?") && r.hasDir(a.paneDir(p, true))
	}
	a.termInput(r, "git "+line, true, !reuse)
}

// hasDir 是目录 d（绝对路径，"" 为仓库根）在这个仓库里、不在套着的别的工作树里。
func (r *Repo) hasDir(d string) bool {
	if d == "" {
		return true
	}
	rel, err := filepath.Rel(r.Root, d)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || !slices.ContainsFunc(r.nested, func(n string) bool { return files.Under(rel, n) })
}

// typeLater 等终端 t 出了提示符（屏幕上有字，最多 5 秒）再把 line 放进输入行，enter 为真时再回车。
func (a *App) typeLater(p *TermPane, t *terminal.Terminal, line string, enter bool) {
	up := a.Update
	go func() {
		for range 100 {
			if strings.TrimSpace(t.Text()) != "" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		up(func() {
			if p.Term == t {
				t.Paste(line)
				if enter {
					t.Send([]byte("\r"))
				}
			}
		})
	}()
}

// TypeInTerminal 在第一个仓库（没有仓库是 ~）的第一个终端标签里敲 line 再回车，等它出了提示符
// （调试的 WORKBENCH_TERM_CMD：量后台进程的内存、手测接回去）。
func (a *App) TypeInTerminal(line string) {
	r := a.TermRepo()
	if len(a.Repos) > 0 {
		r = a.Repos[0]
	}
	ts := a.TermTabs(r)
	if len(ts) == 0 || line == "" {
		return
	}
	p := ts[0].Root.first()
	a.EnsurePane(r, p)
	switch {
	case p.Term != nil:
		a.typeLater(p, p.Term, line, true)
	case p.dial != nil:
		p.ready = append(p.ready, func() {
			if p.Term != nil {
				a.typeLater(p, p.Term, line, true)
			}
		})
	}
}

// StartAllTerms 启动所有仓库所有终端标签的 shell（量内存的 WORKBENCH_TERMS=all）。
func (a *App) StartAllTerms() {
	for _, r := range a.Repos {
		for _, t := range a.TermTabs(r) {
			for _, p := range t.Panes() {
				a.EnsurePane(r, p)
			}
		}
	}
}

// StartedPanes 是启动过、还开着的 shell 数（量内存、测试用）。
func (a *App) StartedPanes() int {
	n := 0
	a.eachPane(func(_ *Repo, p *TermPane) {
		if p.Term != nil && !p.Exited {
			n++
		}
	})
	return n
}

func (a *App) eachPane(fn func(r *Repo, p *TermPane)) {
	rs := a.Repos
	if a.home != nil {
		rs = append(slices.Clip(rs), a.home)
	}
	for _, r := range rs {
		for _, t := range r.allTerms() {
			for _, p := range t.Panes() {
				fn(r, p)
			}
		}
	}
}

// tabOfPane 是格 p 所在的终端标签（哪个仓库的都行）；关掉了为 nil。
func (a *App) tabOfPane(p *TermPane) *TermTab {
	rs := a.Repos
	if a.home != nil {
		rs = append(slices.Clip(rs), a.home)
	}
	for _, r := range rs {
		if t := r.tabOf(p); t != nil {
			return t
		}
	}
	return nil
}

// closeTerms 关掉仓库所有的 shell（关窗口、换工作区）：后台的会话只断开。
func (r *Repo) closeTerms() {
	for _, t := range r.allTerms() {
		for _, p := range t.Panes() {
			closePane(p, false)
		}
	}
}

// killTerms 关掉仓库所有的 shell 和它在 termd 里的会话（移除仓库），还没建过终端的仓库按会话里记的 sid 关。
func (a *App) killTerms(r *Repo) {
	for _, t := range r.allTerms() {
		for _, p := range t.Panes() {
			a.killPane(p)
		}
	}
	if s := r.savedTerms; s != nil {
		for _, t := range s.Tabs {
			sids := t.Sids
			if t.Split != nil {
				sids = nil
				for _, l := range t.Split.Leaves() {
					if l.Sid != "" {
						sids = append(sids, l.Sid)
					}
				}
			}
			for _, sid := range sids {
				a.killSaved(sid)
			}
		}
		for _, h := range splitHists(s) {
			a.dropHist(h)
		}
	}
}

// NoteTermDirs 用 lsof 读每个开着的 shell 现在的目录，记下来给会话用；在应用进程里的格的回滚写进 termhist
// （关窗口、换工作区前，同步）。
func (a *App) NoteTermDirs() {
	a.eachPane(func(_ *Repo, p *TermPane) {
		if p.Term != nil && !p.Exited && p.pid != 0 && p.shellDir() == "" {
			if d := procCwd(p.pid); d != "" {
				p.cwd = d
			}
		}
	})
	a.saveTermHist()
}

// noteTermDirsLater 是后台的 NoteTermDirs，读完有变化再存一次（窗口切走时）。
func (a *App) noteTermDirsLater() {
	type job struct {
		p   *TermPane
		pid int
	}
	var jobs []job
	a.eachPane(func(_ *Repo, p *TermPane) {
		if p.Term != nil && !p.Exited && p.pid != 0 && p.shellDir() == "" {
			jobs = append(jobs, job{p, p.pid})
		}
	})
	if len(jobs) == 0 || a.Update == nil {
		return
	}
	up := a.Update
	go func() {
		dirs := make([]string, len(jobs))
		for i, j := range jobs {
			dirs[i] = procCwd(j.pid)
		}
		up(func() {
			changed := false
			for i, j := range jobs {
				if d := dirs[i]; d != "" && j.p.pid == j.pid && j.p.cwd != d {
					j.p.cwd, changed = d, true
				}
			}
			if changed {
				a.SaveState()
			}
		})
	}()
}

// sessionTerms 是仓库的终端标签在会话里的样子：名字、顺序、shell 的目录和 termd 的会话，分了屏或者有回滚的
// 再记分屏树（Split）。没建过的照搬上次记的；只有一个没改名、在仓库根、不在后台、没有回滚的标签时为 nil（不记）。
// 编辑器区的排在底栏的后面（Active 还是底栏的下标），记 InEditor 和号（会话里的 docs.Term 标签按号对上）。
func (a *App) sessionTerms(r *Repo) *workspace.Terms {
	if !r.termsInit {
		return r.savedTerms
	}
	ts := &workspace.Terms{Active: r.TermSel}
	for _, t := range r.allTerms() {
		if len(ts.Tabs) == workspace.MaxTermTabs {
			break
		}
		p := t.Active
		if p == nil || t.Root.leaf(p) == nil {
			p = t.Root.first()
		}
		dir := a.paneDir(p, false)
		if atRoot(r, dir) {
			dir = ""
		}
		var sids []string
		for _, q := range t.Panes() {
			if q.Sid != "" && q.lost == "" && !q.Exited {
				sids = append(sids, q.Sid)
			}
		}
		tt := workspace.TermTab{Name: t.Name, Dir: dir, Sids: sids}
		if t.InEditor {
			tt.InEditor, tt.Num = true, t.Num
		}
		if p := t.Root.Pane; p == nil || p.hist != "" || p.Profile != "" {
			tt.Split = a.splitOf(r, t.Root)
		}
		ts.Tabs = append(ts.Tabs, tt)
	}
	if len(ts.Tabs) == 0 || len(ts.Tabs) == 1 && ts.Tabs[0].Name == "" && ts.Tabs[0].Dir == "" && len(ts.Tabs[0].Sids) == 0 && ts.Tabs[0].Split == nil && !ts.Tabs[0].InEditor {
		return nil
	}
	ts.Active = min(ts.Active, len(ts.Tabs)-1)
	return ts
}

// atRoot 是 dir 就是仓库根（lsof 读到的是解开链接的路径，/var 读成 /private/var）。
func atRoot(r *Repo, dir string) bool {
	if dir == "" || dir == r.Root {
		return true
	}
	real, err := filepath.EvalSymlinks(r.Root)
	return err == nil && dir == real
}

// 认领 shell 的进程号：mygo 的终端不给进程号，只好看应用的子进程里有终端（tty）的那些
// （git 子进程没有终端），按进程号从小到大分给按启动先后排队的格。认领过的进程号整个进程里不再给别的格
// （测试里一个进程有好几个 App）。还没认领到就关掉的格，closeTerm 等它的 shell 退干净，免得被下一个格认走。
// （试过把记号放进 shell 的环境再用 ps -E 对，本机的 ps 看不到别的进程的环境。）
var (
	claimMu sync.Mutex
	claimed = map[int]bool{}
)

// pollPids 在后台找排队的格的 shell 的进程号，最多找 3 秒。
func (a *App) pollPids() {
	if a.pidPolling || a.Update == nil {
		return
	}
	a.pidPolling = true
	var done atomic.Bool
	up := a.Update
	go func() {
		for i := 0; i < 20 && !done.Load(); i++ {
			time.Sleep(150 * time.Millisecond)
			kids := ttyChildren()
			last := i == 19
			up(func() {
				if a.claimPids(kids) || last {
					done.Store(true)
					a.pidPolling = false
				}
			})
		}
	}()
}

// claimPids 把 kids 里没认领过的分给排队的格，返回还有没有格在等。
func (a *App) claimPids(kids []int) bool {
	a.pidWait = slices.DeleteFunc(a.pidWait, func(p *TermPane) bool { return p.Term == nil || p.pid != 0 })
	claimMu.Lock()
	for _, k := range kids {
		if len(a.pidWait) == 0 {
			break
		}
		if !claimed[k] {
			claimed[k] = true
			a.pidWait[0].pid = k
			a.pidWait = a.pidWait[1:]
		}
	}
	claimMu.Unlock()
	a.syncAgents()
	return len(a.pidWait) == 0
}

// ttyChildren 是应用有终端（tty）的子进程，按进程号排好。
func ttyChildren() []int {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,tty=").Output()
	if err != nil {
		return nil
	}
	me := os.Getpid()
	var kids []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[2] == "??" || f[2] == "-" {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		if ppid == me && pid > 0 {
			kids = append(kids, pid)
		}
	}
	slices.Sort(kids)
	return kids
}

// Job 是这一格前台在跑的命令的名字：没在跑为 ""，不知道 shell 的进程号（还没认领到）为 "?"。
func (p *TermPane) Job() string {
	if p.Term == nil || p.Exited {
		return ""
	}
	if p.pid == 0 {
		return "?"
	}
	return foregroundJob(p.pid)
}

// foregroundJob 是 shell（进程号 pid）前台在跑的命令：shell 的进程组不是它终端的前台进程组时，
// 前台那一组的组长的名字（叫不出名字为 "?"）；在提示符那里等着、只有后台任务（&）的为 ""。
func foregroundJob(pid int) string {
	out, err := exec.Command("ps", "-o", "pgid=,tpgid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "" // shell 已经没了
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return ""
	}
	pgid, _ := strconv.Atoi(f[0])
	tpgid, _ := strconv.Atoi(f[1])
	if tpgid <= 0 || tpgid == pgid {
		return ""
	}
	name, _ := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(tpgid)).Output()
	if n := strings.TrimSpace(string(name)); n != "" {
		return strings.TrimPrefix(filepath.Base(n), "-")
	}
	return "?"
}

// procCwd 是进程现在的工作目录（lsof），读不到为 ""。
func procCwd(pid int) string {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if d, ok := strings.CutPrefix(line, "n"); ok && filepath.IsAbs(d) {
			return d
		}
	}
	return ""
}
