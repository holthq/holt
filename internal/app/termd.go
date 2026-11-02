package app

import (
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/termd"
)

// 这个文件是终端后台进程（internal/termd）在应用这边：设置里开了"关掉窗口后终端继续在后台运行"，
// 新开的格的 shell 就跑在 termd 里，格里的终端只是它的镜像（terminal.Options.Conn）。
// 关窗口、⌘Q、换工作区是断开（会话留在 termd 里，会话记下 sid，再打开同一个工作区接回去）；
// 关格、关标签、移除仓库、重新启动是关掉会话（shell 收到 SIGHUP）。设置关掉只影响新开的格。
// 一个状态目录一个 termd（套接字 termd.SockPath(StateDir)），用 WORKBENCH_STATE 的实例有自己的。

var (
	// TermdLog 是 termd 的日志（main 里给 <日志目录>/termd.log；空则不写）。
	TermdLog string
	// TermdStart 在套接字 sock 上起一个 termd，等它能连上：默认是带 termd 参数再起一个自己（termd.Spawn），
	// 测试里换成进程内的 termd.Server。
	TermdStart = func(sock string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return termd.Spawn(exe, sock, TermdLog, nil)
	}
)

// termdSock 是这个窗口的 termd 的套接字；没有状态目录（测试）为空，终端都在进程里。
func (a *App) termdSock() string {
	if a.StateDir == "" {
		return ""
	}
	return termd.SockPath(a.StateDir)
}

// Daemon 是这一格的 shell 跑在终端后台进程里（接上了）。
func (p *TermPane) Daemon() bool { return p.conn != nil }

// Connecting 是这一格在连终端后台进程（界面上转圈）。
func (p *TermPane) Connecting() bool { return p.dial != nil }

// Lost 是这一格和终端后台进程的连接断了时的说明（会话没了、被别的窗口接走了），没断为空。
func (p *TermPane) Lost() string { return p.lost }

// termDial 是一格正在连 termd：连上之前格就关了（drop）的，连上的会话按关的方式断开或关掉。
type termDial struct {
	drop int // 0 还要；dropDetach 断开；dropKill 关掉
}

const (
	dropDetach = 1
	dropKill   = 2
)

// spawnMu 让同时起的几格（还原分屏的标签）只有一个去起 termd，别的等它起好。
var spawnMu sync.Mutex

// dialResult 是在后台连 termd 的结果。
type dialResult struct {
	conn  *termd.Conn
	fresh bool  // 新开的会话（不是接回来的）
	lost  bool  // 记着的会话不在了（termd 重新起过）
	err   error // 起不来 / 连不上：这次用普通终端
}

// dialSession 在后台连 sock 上的 termd：接回会话 sid，不在了（或 sid 为空）开新的；termd 没在跑时，
// spawn 为真先起一个，否则结果里 conn 为空（用普通终端）。
func dialSession(sock, sid string, req termd.NewReq, spawn bool, start func(string) error) dialResult {
	spawnMu.Lock()
	if termd.Ping(sock) != nil {
		if !spawn {
			spawnMu.Unlock()
			return dialResult{lost: sid != ""}
		}
		if err := start(sock); err != nil {
			spawnMu.Unlock()
			return dialResult{err: err}
		}
	}
	spawnMu.Unlock()
	var res dialResult
	if sid != "" {
		c, err := termd.Attach(sock, termd.AttachReq{Sid: sid})
		if err == nil {
			return dialResult{conn: c}
		}
		if !errors.Is(err, termd.ErrGone) {
			return dialResult{err: err}
		}
		res.lost = true
		if !spawn {
			return res
		}
	}
	c, err := termd.New(sock, req)
	if err != nil {
		res.err = err
		return res
	}
	res.conn, res.fresh = c, true
	return res
}

// termdReq 是给这一格在 termd 里开新会话的请求：设置里的 shell 和回滚大小；用终端配置的，换成配置的 shell、参数，
// 环境接上配置的（同名的配置的算）。
func (a *App) termdReq(r *Repo, p *TermPane, dir string) termd.NewReq {
	s := &a.St.Settings
	req := termd.NewReq{WS: a.WsPath, Repo: r.Root, Dir: dir, Env: os.Environ(), Shell: s.Shell}
	if pr := s.Profile(p.Profile); pr != nil {
		req.Shell, req.Args, req.Env = pr.Shell, pr.Args, append(req.Env, pr.Env...)
	}
	if s.Scrollback > 0 {
		req.Scrollback = s.Scrollback << 20
	}
	return req
}

// dialPane 在后台连 termd 给这一格接回 / 开一个会话（StartPane 在后台模式下），连上了在主线程上建终端。
func (a *App) dialPane(r *Repo, p *TermPane, gen int, dir, sock string) {
	d := &termDial{}
	p.dial = d
	req := a.termdReq(r, p, dir)
	sid, spawn, start := p.Sid, a.St.Settings.TermDaemon, TermdStart
	up := a.Update
	go func() {
		res := dialSession(sock, sid, req, spawn, start)
		up(func() {
			if p.dial != d || p.gen != gen { // 连上之前关掉 / 重新启动了
				if c := res.conn; c != nil {
					if d.drop == dropDetach && !res.fresh {
						c.Detach()
					} else {
						c.Kill()
					}
				}
				return
			}
			p.dial = nil
			switch {
			case res.err != nil:
				log.Printf("终端后台进程：%v", res.err)
				a.Toast = i18n.T("终端后台进程没起来，这次用普通终端")
				p.Sid = ""
				a.startLocal(r, p, gen, dir)
			case res.conn == nil:
				p.Sid = ""
				a.startLocal(r, p, gen, dir)
			default:
				a.attachPane(r, p, gen, dir, res)
			}
			if res.lost && res.err == nil {
				a.Toast = i18n.T("上次的终端会话不在了，开了新的")
			}
			a.runReady(p)
		})
	}()
}

// attachPane 用连上的会话建这一格的终端：屏幕的大小跟着 termd 那边（ResizeByConn），⌘K 交给 termd 清，
// 进程号、退出了没有、shell 报的目录在 Hello 里。
func (a *App) attachPane(r *Repo, p *TermPane, gen int, dir string, res dialResult) {
	conn := res.conn
	up := a.Update
	opts := a.paneOptions(r, p, gen, dir)
	opts.Conn, opts.ResizeByConn = conn, true
	opts.OnClear = func(terminal.Clear) bool {
		conn.Clear()
		return true
	}
	// 读到头了：会话没了、termd 退了，或者被别的窗口接走了（OnGone 先到）
	opts.OnExit = func(int) {
		up(func() {
			if p.gen == gen && p.conn == conn && !p.Exited {
				p.Exited = true
				if p.lost == "" {
					p.lost = i18n.T("和终端后台进程的连接断了")
				}
			}
		})
	}
	term, err := terminal.New(opts)
	if err != nil {
		if res.fresh {
			conn.Kill()
		} else {
			conn.Detach()
		}
		p.Err = err
		return
	}
	h := conn.Hello
	p.Term, p.conn, p.Sid, p.pid = term, conn, h.Sid, h.Pid
	p.feed = nil // 上次的回滚：termd 那边会话本来就有自己的
	p.Exited, p.ExitCode, p.rdir = h.Exited, h.Code, h.Dir
	term.Resize(h.Cols, h.Rows)
	conn.OnResized = term.Resize
	conn.OnDir = func(d string) {
		up(func() {
			if p.gen == gen {
				p.rdir = d
			}
		})
	}
	conn.OnExited = func(code int) {
		up(func() {
			if p.gen == gen {
				p.Exited, p.ExitCode = true, code
			}
		})
	}
	conn.OnGone = func(kicked bool) {
		up(func() {
			if p.gen != gen {
				return
			}
			if kicked {
				p.lost = i18n.T("这个终端在别的窗口里接上了")
			} else {
				p.lost = i18n.T("后台的会话不在了")
			}
		})
	}
	conn.OnNoClear = func() {
		up(func() {
			if p.gen == gen {
				a.Toast = i18n.T("全屏程序里不清屏")
			}
		})
	}
	conn.Start()
	a.SaveState() // 会话里记下 sid
}

// runReady 跑这一格等着终端建好的事（RunInTerminal 之类在连 termd 时排的）。
func (a *App) runReady(p *TermPane) {
	fns := p.ready
	p.ready = nil
	for _, fn := range fns {
		fn()
	}
}

// killSaved 关掉 termd 里一个这次还没接上的会话（还原了但没显示过的格、没建过终端的仓库，关标签、移除仓库时）。
func (a *App) killSaved(sid string) {
	sock := a.termdSock()
	if sid == "" || sock == "" {
		return
	}
	go func() {
		if termd.Ping(sock) != nil {
			return
		}
		if c, err := termd.Attach(sock, termd.AttachReq{Sid: sid}); err == nil {
			c.Kill()
		}
	}()
}

// TermdState 是终端后台进程现在的样子（设置页那一行、"停止终端后台进程…"灰不灰）：在跑没有、几个会话、
// 几个没在任何窗口里、几个前台在跑命令。查一次要连一下套接字，所以记下来，过 2 秒再问时在后台重新查，查完重画。
type TermdState struct {
	Running bool
	Total   int
	Loose   int // 没在任何窗口里（断开着）
	Busy    int
}

var termdSeen struct {
	sync.Mutex
	sock   string
	at     time.Time
	asking bool
	st     TermdState
}

// TermdStatus 是记下的 termd 的样子；旧了（2 秒）在后台重新查，查完变了就调 redraw（任意 goroutine 里；
// 设置窗口给它自己的 Invalidate），nil 为重画这个窗口。
func (a *App) TermdStatus(redraw func()) TermdState {
	sock := a.termdSock()
	if sock == "" {
		return TermdState{}
	}
	m := &termdSeen
	m.Lock()
	defer m.Unlock()
	if m.sock != sock {
		m.sock, m.at, m.st = sock, time.Time{}, TermdState{}
	}
	if redraw == nil && a.Update != nil {
		up := a.Update
		redraw = func() { up(func() {}) }
	}
	if time.Since(m.at) > 2*time.Second && !m.asking && redraw != nil {
		m.asking = true
		go func() {
			st := askTermd(sock)
			m.Lock()
			changed := st != m.st
			if m.sock == sock {
				m.st, m.at = st, time.Now()
			}
			m.asking = false
			m.Unlock()
			if changed {
				redraw()
			}
		}()
	}
	return m.st
}

// forgetTermd 让下一次 TermdStatus 重新查（开了、关了会话之后）。
func forgetTermd() {
	termdSeen.Lock()
	termdSeen.at = time.Time{}
	termdSeen.Unlock()
}

func askTermd(sock string) TermdState {
	infos, err := termd.List(sock)
	if err != nil {
		return TermdState{}
	}
	st := TermdState{Running: true, Total: len(infos)}
	for _, in := range infos {
		if !in.Attached {
			st.Loose++
		}
		if in.Busy {
			st.Busy++
		}
	}
	return st
}

// StopTermDaemon 是"停止终端后台进程…"：问过之后挂断 termd 里所有的 shell（SIGHUP）；
// 开着的格显示已退出，termd 没有会话、没有连接后自己退出。
func (a *App) StopTermDaemon() {
	sock := a.termdSock()
	st := askTermd(sock)
	if !st.Running || st.Total == 0 {
		a.Toast = i18n.T("终端后台进程里没有会话")
		return
	}
	up := a.Update
	a.Ask(i18n.T("停止终端后台进程？"),
		i18n.Tn("后台里的 %d 个 shell（%d 个还在跑命令）会被挂断（SIGHUP），开着的终端格会显示已退出。", st.Total, st.Busy),
		i18n.T("停止"), func() {
			go func() {
				n, err := termd.Killall(sock)
				forgetTermd()
				up(func() {
					if err != nil {
						a.Toast = i18n.Tf("停止失败：%v", err)
						return
					}
					a.Toast = i18n.Tn("挂断了 %d 个 shell", n)
				})
			}()
		})
}

// termdRunning 是"停止终端后台进程…"能不能用：termd 在跑而且有会话（记下的样子）。
func termdRunning(a *App) bool {
	st := a.TermdStatus(nil)
	return st.Running && st.Total > 0
}
