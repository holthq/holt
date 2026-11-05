package termd

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// session 是 termd 里的一个 shell：伪终端加一个没有界面的 terminal.Terminal，shell 的输出喂给它，
// 接着的窗口看它的镜像帧（terminal.Mirror），一格一格和它一样；窗口的大小、敲的字经 termd 给 shell。
// 一个会话同时只给一个窗口接（新接的把旧的挤掉）。shell 退出了会话还在，直到窗口关掉它，
// 或者没有窗口接着过了 Server.Linger（见 lingerLocked）。
type session struct {
	srv  *Server
	info Info // Sid、WS、Repo、Started 不变，其余在 mu 下

	mu      sync.Mutex
	term    *terminal.Terminal
	proc    *proc
	pumped  chan struct{} // 输出读完了
	cols    int
	rows    int
	cellW   int
	cellH   int
	cl      *client
	framing bool
	dir     string // OSC 7 最近报的
	rang    bool   // 这一笔输出里响了铃 / 要了通知（Feed 里的回调写，output 读完清；都在 s.mu 下）
	bell    bool   // 响过铃但当时没有窗口接着：接上时补一帧
	exited  bool
	killed  bool
	linger  int // 每排一次拿掉（lingerLocked）、每接上一次加一：到点时对不上就是作废了
}

// headless 把 termd 这边的终端接到 shell：它什么也不读（termd 自己 Feed），
// 写的是终端对程序的问题的回答（光标在哪、颜色），交给伪终端。
type headless struct {
	proc   atomic.Pointer[proc]
	closed chan struct{}
	once   sync.Once
}

func (h *headless) Read([]byte) (int, error) {
	<-h.closed
	return 0, io.EOF
}

func (h *headless) Write(p []byte) (int, error) {
	if pr := h.proc.Load(); pr != nil {
		pr.Write(p)
	}
	return len(p), nil
}

func (h *headless) Close() error {
	h.once.Do(func() { close(h.closed) })
	return nil
}

func newSession(srv *Server, sid string, req NewReq) (*session, error) {
	cols, rows := req.Cols, req.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	h := &headless{closed: make(chan struct{})}
	var s *session
	// 响铃、通知的回调在 Feed 里同步调（output 拿着 s.mu），这里只记一笔，output 喂完再告诉窗口。
	// 终端自己分得清 OSC 里当结束符的 BEL 和真的响铃，不用自己扫输出。
	rang := func() {
		if s != nil {
			s.rang = true
		}
	}
	term, err := terminal.New(terminal.Options{
		Conn:       h,
		Scrollback: req.Scrollback,
		// 程序读剪贴板（OSC 52）交给接着的窗口去答，它照常问用户
		ClipboardRead: terminal.ClipboardForward,
		OnBell:        rang,
		OnNotify:      func(string, string) { rang() },
	})
	if err != nil {
		return nil, err
	}
	term.Resize(cols, rows)
	if req.CellW > 0 && req.CellH > 0 {
		term.ResizePixels(cols, rows, req.CellW, req.CellH)
	}
	dir := req.Dir
	if st, err := os.Stat(dir); dir == "" || err != nil || !st.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	env := req.Env
	if len(env) == 0 {
		env = os.Environ()
	}
	path, args := shellCommand(req.Shell, req.Args, env)
	pr, err := startProc(procSpec{
		Path: path, Args: args, Dir: dir, Env: shellEnv(env),
		Cols: cols, Rows: rows, CellW: req.CellW, CellH: req.CellH,
	})
	if err != nil {
		term.Close()
		return nil, err
	}
	h.proc.Store(pr)
	s = &session{
		srv:  srv,
		info: Info{Sid: sid, WS: req.WS, Repo: req.Repo, Dir: dir, Pid: pr.pid(), Started: time.Now()},
		term: term, proc: pr, pumped: make(chan struct{}),
		cols: cols, rows: rows, cellW: req.CellW, cellH: req.CellH,
	}
	go s.pump()
	go s.wait()
	return s, nil
}

// shellCommand 和终端插件一样：设置里的 shell 带 -l（终端配置有参数时是那些参数）；没设是 $SHELL（没有是 /bin/zsh），
// argv[0] 前面加 - 当登录 shell。不带 / 的 shell（配置里写 fish）按 env 里的 PATH 找。
func shellCommand(shell string, args, env []string) (string, []string) {
	if shell != "" {
		path := lookPath(shell, env)
		if len(args) > 0 {
			return path, append([]string{shell}, args...)
		}
		return path, []string{shell, "-l"}
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "SHELL="); ok && v != "" {
			shell = v
		}
	}
	if shell == "" {
		shell = "/bin/zsh"
	}
	return shell, []string{"-" + filepath.Base(shell)}
}

// lookPath 是 name 在 env 的 PATH 里找到的可执行文件；带 / 的、找不到的原样。
func lookPath(name string, env []string) string {
	if strings.Contains(name, "/") {
		return name
	}
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			for _, d := range filepath.SplitList(v) {
				p := filepath.Join(d, name)
				if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
					return p
				}
			}
			break
		}
	}
	return name
}

// shellEnv 是给 shell 的环境：应用的，加上终端插件设的那几个和 TERM_PROGRAM=Grove。
func shellEnv(base []string) []string {
	env := append([]string(nil), base...)
	set := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		for i, e := range env {
			if strings.HasPrefix(e, k+"=") {
				env[i] = kv
				return
			}
		}
		env = append(env, kv)
	}
	has := func(k string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, k+"=") {
				return true
			}
		}
		return false
	}
	set("TERM=xterm-256color")
	set("COLORTERM=truecolor")
	set("TERM_PROGRAM=Grove")
	if !has("LANG") && !has("LC_ALL") && !has("LC_CTYPE") {
		set("LANG=en_US.UTF-8")
	}
	return env
}

// pump 读 shell 的输出直到读完。
func (s *session) pump() {
	defer close(s.pumped)
	buf := make([]byte, 64<<10)
	for {
		n, err := s.proc.Read(buf)
		if n > 0 {
			s.output(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *session) output(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.killed {
		return
	}
	s.term.Feed(p)
	if d := s.term.Dir(); d != "" && d != s.dir {
		// 镜像帧里没有 OSC 7：目录单独告诉窗口
		s.dir = d
		if s.cl != nil {
			s.cl.send(frameDir, []byte(d))
		}
	}
	if s.rang {
		s.rang = false
		if s.cl != nil {
			s.cl.send(frameBell, nil)
		} else {
			s.bell = true
		}
	}
	s.frameSoon()
}

// wait 等 shell 退出：等输出读完（后台还有进程开着伪终端的话最多 200 毫秒，之后挂断它），
// 发最后一帧和退出码。被关掉的会话之后就没了。
func (s *session) wait() {
	<-s.proc.done
	select {
	case <-s.pumped:
	case <-time.After(200 * time.Millisecond):
		s.proc.master.Close()
		<-s.pumped
	}
	s.mu.Lock()
	s.exited = true
	s.info.Exited, s.info.Code = true, s.proc.code
	s.frame()
	cl := s.cl
	if cl != nil {
		cl.send(frameExited, binary.BigEndian.AppendUint32(nil, uint32(int32(s.proc.code))))
	}
	killed := s.killed
	if killed {
		s.cl = nil
		if cl != nil {
			cl.mirror.Close()
			cl.end()
		}
	}
	s.lingerLocked()
	s.mu.Unlock()
	s.srv.logf("会话 %s：shell %d 退出，退出码 %d", s.info.Sid, s.info.Pid, s.proc.code)
	if killed {
		s.term.Close()
	}
}

// kill 关掉会话：从 termd 里拿掉，shell 挂断（收到 SIGHUP）。
func (s *session) kill() {
	s.mu.Lock()
	if s.killed {
		s.mu.Unlock()
		return
	}
	s.killed = true
	exited := s.exited
	cl := s.cl
	if exited {
		s.cl = nil
	}
	s.mu.Unlock()
	s.srv.remove(s)
	if exited {
		// shell 早退了，wait 已经走完：这里收尾
		if cl != nil {
			cl.mirror.Close()
			cl.end()
		}
		s.term.Close()
		return
	}
	s.proc.hangup()
}

// attach 让 c 接上会话：之前接着的被挤掉（收到 gone "kicked"）；先换成 c 的大小，
// 然后 hello、从头来的第一帧，之后跟着输出发帧。会话已经关了返回 false。
func (s *session) attach(c *client, cols, rows, cellW, cellH int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.killed {
		return false
	}
	if old := s.cl; old != nil {
		s.cl = nil
		old.mirror.Close()
		old.send(frameGone, []byte("kicked"))
		old.end()
	}
	s.linger++
	s.resizeLocked(cols, rows, cellW, cellH)
	m, err := s.term.NewMirror()
	if err != nil {
		return false
	}
	first, _ := m.Frame()
	c.mirror = m
	s.cl = c
	hello, _ := json.Marshal(Hello{Sid: s.info.Sid, Pid: s.info.Pid, Cols: s.cols, Rows: s.rows, Exited: s.exited, Code: s.info.Code, Dir: s.dir})
	c.send(frameHello, hello)
	if len(first) > 0 {
		c.send(frameOutput, first)
	}
	if s.bell {
		s.bell = false
		c.send(frameBell, nil)
	}
	return true
}

func (s *session) detach(c *client) {
	s.mu.Lock()
	if s.cl == c {
		s.cl = nil
		c.mirror.Close()
		s.lingerLocked()
	}
	s.mu.Unlock()
}

// lingerLocked 在 shell 退出了、没有窗口接着时排一次拿掉：过 Server.Linger 还是没人接（期间接上过就作废）就关掉会话。
// 退出了的会话留着没用，留着 termd 就永远闲不下来。s.mu 拿着。
func (s *session) lingerLocked() {
	if !s.exited || s.killed || s.cl != nil {
		return
	}
	s.linger++
	n := s.linger
	time.AfterFunc(s.srv.linger(), func() {
		s.mu.Lock()
		stale := n != s.linger || s.cl != nil || s.killed
		s.mu.Unlock()
		if !stale {
			s.srv.logf("会话 %s：退出了、没有窗口接着，拿掉", s.info.Sid)
			s.kill()
		}
	})
}

func (s *session) input(p []byte) {
	s.mu.Lock()
	dead := s.exited || s.killed
	s.mu.Unlock()
	if !dead {
		s.proc.Write(p)
	}
}

func (s *session) resize(cols, rows, cellW, cellH int) {
	s.mu.Lock()
	s.resizeLocked(cols, rows, cellW, cellH)
	s.mu.Unlock()
}

// resizeLocked 换大小：只是格子的像素变了就只告诉终端和 shell；列 / 行变了先按旧大小发完帧，
// 再换、告诉窗口（resized，窗口的终端在这之后的帧前面跟着换），再发新大小的帧。s.mu 拿着。
func (s *session) resizeLocked(cols, rows, cellW, cellH int) {
	if cols <= 0 || rows <= 0 || s.killed {
		return
	}
	if cellW > 0 && cellH > 0 && (cellW != s.cellW || cellH != s.cellH) {
		s.cellW, s.cellH = cellW, cellH
		s.term.ResizePixels(s.cols, s.rows, cellW, cellH)
		if !s.exited {
			s.proc.resize(s.cols, s.rows, cellW, cellH)
		}
	}
	if cols == s.cols && rows == s.rows {
		return
	}
	s.frame()
	s.cols, s.rows = cols, rows
	s.term.Resize(cols, rows)
	if !s.exited {
		s.proc.resize(cols, rows, s.cellW, s.cellH)
	}
	if s.cl != nil {
		s.cl.send(frameResized, uint32s(cols, rows))
	}
	s.frame()
}

// clear 是窗口里的 ⌘K：清掉回滚和光标那行上面的，提示符挪到顶上；全屏程序里不清，告诉窗口。
func (s *session) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.killed {
		return
	}
	if !s.term.ClearScreen() {
		if s.cl != nil {
			s.cl.send(frameNoClear, nil)
		}
		return
	}
	s.frameSoon()
}

func (s *session) useTheme(p []byte) {
	var th terminal.Theme
	if json.Unmarshal(p, &th) != nil {
		return
	}
	s.mu.Lock()
	if !s.killed {
		s.term.UseTheme(&th)
	}
	s.mu.Unlock()
}

// frameDelay 是输出之后等多久再发帧：程序重画是一块一块来的。
const frameDelay = 4 * time.Millisecond

// frameSoon 过一会儿给窗口发一帧。s.mu 拿着。
func (s *session) frameSoon() {
	if s.framing || s.cl == nil {
		return
	}
	s.framing = true
	time.AfterFunc(frameDelay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.framing = false
		s.frame()
	})
}

// frame 发给窗口让它的终端和这边一样的那一帧；程序在同步更新里（模式 2026）时这一帧要晚点，50 毫秒后再来。s.mu 拿着。
func (s *session) frame() {
	if s.cl == nil {
		return
	}
	f, due := s.cl.mirror.Frame()
	if len(f) > 0 {
		s.cl.send(frameOutput, f)
	}
	if due && !s.framing {
		s.framing = true
		time.AfterFunc(50*time.Millisecond, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.framing = false
			s.frame()
		})
	}
}

// snapshot 是 list 里的这一条。
func (s *session) snapshot() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.info
	if s.dir != "" {
		in.Dir = s.dir
	}
	in.Attached = s.cl != nil
	in.Busy = !s.exited && s.proc.busy()
	return in
}

// client 是接着一个会话的那条连接。帧先进 out，由 writeLoop 写出去：会话的锁里不等网络。
type client struct {
	conn   net.Conn
	out    chan []byte // nil 是"写完前面的就关"
	done   chan struct{}
	once   sync.Once
	mirror *terminal.Mirror
}

func newClient(conn net.Conn) *client {
	c := &client{conn: conn, out: make(chan []byte, 4096), done: make(chan struct{})}
	go c.writeLoop()
	return c
}

// send 不等：窗口几千帧没读（卡住了）就断开它，它再接上时从头来一帧。
func (c *client) send(typ byte, p []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.out <- encodeFrame(typ, p):
	default:
		c.close()
	}
}

// end 让 writeLoop 写完已经排着的帧再关连接。
func (c *client) end() {
	select {
	case c.out <- nil:
	case <-c.done:
	default:
		c.close()
	}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

func (c *client) writeLoop() {
	for {
		select {
		case b := <-c.out:
			if b == nil {
				c.close()
				return
			}
			if _, err := c.conn.Write(b); err != nil {
				c.close()
				return
			}
		case <-c.done:
			return
		}
	}
}
