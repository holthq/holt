// Package termd 是终端的后台进程：shell 跑在它的伪终端里，关窗口、退出 Grove 都不断，
// 再打开同一个工作区时窗口接回去（设置"关掉窗口后终端继续在后台运行"）。
//
// 它就是 Grove 这个可执行文件带 termd 参数（`Grove termd <sock> <log>`）：main 第一行就分出来，
// 不起 mygo.App。每个会话一个没有界面的 terminal.Terminal，shell 的输出喂给它；窗口的终端
// （Options.Conn = Conn，ResizeByConn）看它的镜像帧，屏幕和回滚一格一格和它一样。
//
// shell 退出了的会话：有窗口接着时留着（窗口显示"已退出"），没人接着（退出时就没有，或者之后断开了）
// 再过 Linger（10 秒）拿掉，所以只剩退出了的会话时它照样会闲下来退出。
//
// 应用和它之间是 unix 套接字（状态目录里的 termd.sock，0600，不做别的认证：只有这个用户能连），
// 一个请求一条连接，帧见 proto.go。没有会话、也没有连接 60 秒后它自己退出、删掉套接字；
// SIGTERM / SIGINT 挂断所有 shell 再退出。
package termd

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// DefaultIdle 是没有会话、没有连接多久后 termd 自己退出。
const DefaultIdle = 60 * time.Second

// DefaultLinger 是 shell 退出了、没有窗口接着的会话再留多久（给窗口重连一次的机会，比如换工作区），之后拿掉。
const DefaultLinger = 10 * time.Second

// MaxLogSize 是 termd.log 多大时换成 termd.old.log 重新写。
const MaxLogSize = 1 << 20

// ErrRunning 是这个套接字上已经有一个 termd 了。
var ErrRunning = errors.New("termd: 已经在运行")

// Server 是一个 termd。
type Server struct {
	Sock string
	// Idle 是没有会话、没有连接多久后退出：0 是 DefaultIdle，小于 0 不退。
	Idle time.Duration
	// Linger 是退出了的会话没有窗口接着时再留多久：0 是 DefaultLinger，小于 0 马上拿掉。
	Linger time.Duration
	Log    *log.Logger // nil 不记

	mu       sync.Mutex
	sessions map[string]*session
	order    []string // 开的先后，list 按它排
	conns    int
	quiet    time.Time // 最近一次变成没有会话、没有连接的时候
	ln       net.Listener
	lock     *os.File
	closing  bool
	done     chan struct{}
	stopOnce sync.Once
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

// Listen 占住套接字：上面已经有一个答话的 termd 是 ErrRunning；同时起了两个的，
// 靠 <sock>.lock 的 flock 只留一个（前一个正在退出时等它最多 1.5 秒）。
func (s *Server) Listen() error {
	if Ping(s.Sock) == nil {
		return ErrRunning
	}
	lf, err := os.OpenFile(s.Sock+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	for i := 0; ; i++ {
		err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if i >= 30 {
			lf.Close()
			return ErrRunning
		}
		time.Sleep(50 * time.Millisecond)
	}
	if Ping(s.Sock) == nil {
		lf.Close()
		return ErrRunning
	}
	os.Remove(s.Sock) // 上一个没收拾的
	ln, err := net.Listen("unix", s.Sock)
	if err != nil {
		lf.Close()
		return err
	}
	if err := os.Chmod(s.Sock, 0o600); err != nil {
		ln.Close()
		lf.Close()
		return err
	}
	s.mu.Lock()
	s.ln, s.lock = ln, lf
	s.sessions = map[string]*session{}
	s.quiet = time.Now()
	s.done = make(chan struct{})
	s.mu.Unlock()
	return nil
}

// Serve 接连接，直到 Stop 或者闲够了 Idle。之前要 Listen。
func (s *Server) Serve() error {
	go s.idleLoop()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			s.Stop()
			return err
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go s.handle(c)
	}
}

// Stop 挂断所有 shell、关掉套接字。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		ln, lf := s.ln, s.lock
		var all []*session
		for _, id := range s.order {
			all = append(all, s.sessions[id])
		}
		s.mu.Unlock()
		if ln != nil {
			ln.Close()
			os.Remove(s.Sock)
		}
		for _, se := range all {
			se.kill()
		}
		if lf != nil {
			os.Remove(s.Sock + ".lock")
			lf.Close()
		}
		close(s.done)
	})
}

// Done 在 Stop 做完（套接字删了、shell 都挂断了）之后关上。
func (s *Server) Done() <-chan struct{} { return s.done }

func (s *Server) idleLoop() {
	idle := s.Idle
	if idle == 0 {
		idle = DefaultIdle
	}
	if idle < 0 {
		return
	}
	t := time.NewTicker(min(max(idle/4, 10*time.Millisecond), time.Second))
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		}
		s.mu.Lock()
		quiet := len(s.sessions) == 0 && s.conns == 0 && time.Since(s.quiet) >= idle
		s.mu.Unlock()
		if quiet {
			s.logf("没有会话、没有连接 %v，退出", idle)
			s.Stop()
			return
		}
	}
}

// linger 是退出了的会话没人接着时再留多久。
func (s *Server) linger() time.Duration {
	switch {
	case s.Linger < 0:
		return 0
	case s.Linger == 0:
		return DefaultLinger
	}
	return s.Linger
}

func (s *Server) connDone() {
	s.mu.Lock()
	s.conns--
	if s.conns == 0 && len(s.sessions) == 0 {
		s.quiet = time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) add(se *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.sessions[se.info.Sid] = se
	s.order = append(s.order, se.info.Sid)
	return true
}

func (s *Server) remove(se *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[se.info.Sid] != se {
		return
	}
	delete(s.sessions, se.info.Sid)
	for i, id := range s.order {
		if id == se.info.Sid {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if s.conns == 0 && len(s.sessions) == 0 {
		s.quiet = time.Now()
	}
}

func (s *Server) get(sid string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sid]
}

func (s *Server) list() []Info {
	s.mu.Lock()
	var all []*session
	for _, id := range s.order {
		all = append(all, s.sessions[id])
	}
	s.mu.Unlock()
	out := []Info{}
	for _, se := range all {
		out = append(out, se.snapshot())
	}
	return out
}

func newSid() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) handle(c net.Conn) {
	defer s.connDone()
	r := bufio.NewReaderSize(c, 64<<10)
	typ, p, err := readFrame(r)
	if err != nil {
		c.Close()
		return
	}
	switch typ {
	case framePing:
		writeFrame(c, frameOK, nil)
	case frameList:
		writeJSON(c, frameListed, s.list())
	case frameKillall:
		all := s.list()
		for _, in := range all {
			if se := s.get(in.Sid); se != nil {
				se.kill()
			}
		}
		s.logf("killall：%d 个会话", len(all))
		writeFrame(c, frameOK, uint32s(len(all)))
	case frameStop:
		writeFrame(c, frameOK, nil)
		c.Close()
		s.Stop()
		return
	case frameNew:
		var req NewReq
		if err := json.Unmarshal(p, &req); err != nil {
			writeFrame(c, frameError, []byte(err.Error()))
			break
		}
		se, err := newSession(s, newSid(), req)
		if err != nil {
			s.logf("开会话：%v", err)
			writeFrame(c, frameError, []byte(err.Error()))
			break
		}
		if !s.add(se) {
			se.kill()
			writeFrame(c, frameError, []byte("termd 正在退出"))
			break
		}
		s.logf("会话 %s：shell %d 在 %s（%s）", se.info.Sid, se.info.Pid, se.info.Dir, se.info.Repo)
		s.stream(c, r, se, 0, 0, 0, 0)
		return
	case frameAttach:
		var req AttachReq
		if err := json.Unmarshal(p, &req); err != nil {
			writeFrame(c, frameError, []byte(err.Error()))
			break
		}
		se := s.get(req.Sid)
		if se == nil {
			writeFrame(c, frameGone, nil)
			break
		}
		s.stream(c, r, se, req.Cols, req.Rows, req.CellW, req.CellH)
		return
	default:
		writeFrame(c, frameError, fmt.Appendf(nil, "不认识的请求 %q", typ))
	}
	c.Close()
}

// stream 是接着会话的那条连接：先接上，再读窗口发来的帧，直到它断开、detach 或者关掉会话。
func (s *Server) stream(c net.Conn, r *bufio.Reader, se *session, cols, rows, cellW, cellH int) {
	cl := newClient(c)
	if !se.attach(cl, cols, rows, cellW, cellH) {
		cl.send(frameGone, nil)
		cl.end()
		return
	}
	defer func() {
		se.detach(cl)
		cl.end()
	}()
	for {
		typ, p, err := readFrame(r)
		if err != nil {
			return
		}
		switch typ {
		case frameInput:
			se.input(p)
		case frameResize:
			if v, ok := parseUint32s(p, 4); ok {
				se.resize(v[0], v[1], v[2], v[3])
			}
		case frameTheme:
			se.useTheme(p)
		case frameClear:
			se.clear()
		case frameKill:
			se.detach(cl)
			se.kill()
			return
		case frameDetach:
			return
		}
	}
}

// Main 是 `Grove termd <sock> <log>`：返回退出码。
func Main(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "用法：Grove termd <套接字> [日志]")
		return 2
	}
	sock := args[0]
	var lg *log.Logger
	if len(args) > 1 && args[1] != "" {
		if f, err := openLog(args[1]); err == nil {
			defer f.Close()
			lg = log.New(f, "", log.LstdFlags)
			debug.SetCrashOutput(f, debug.CrashOptions{})
		}
	}
	logf := func(format string, a ...any) {
		if lg != nil {
			lg.Printf(format, a...)
		}
	}
	if err := terminal.Load(); err != nil {
		logf("终端库：%v", err)
		return 1
	}
	signal.Ignore(syscall.SIGHUP)
	srv := &Server{Sock: sock, Log: lg}
	if err := srv.Listen(); err != nil {
		if errors.Is(err, ErrRunning) {
			logf("%s 上已经有一个 termd", sock)
			return 0
		}
		logf("监听 %s：%v", sock, err)
		return 1
	}
	logf("termd %d 开始，套接字 %s", os.Getpid(), sock)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case v := <-sig:
			logf("收到 %v：挂断所有 shell", v)
			srv.Stop()
		case <-srv.Done():
		}
	}()
	err := srv.Serve()
	// 挂断的 shell 收到 SIGHUP 就退；给它们一点时间，别让 termd 先没了留下孤儿
	time.Sleep(100 * time.Millisecond)
	if err != nil {
		logf("termd 退出：%v", err)
		return 1
	}
	logf("termd %d 退出", os.Getpid())
	return 0
}

// openLog 打开追加写的日志；超过 MaxLogSize 的先改名成 .old。
func openLog(path string) (*os.File, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > MaxLogSize {
		os.Rename(path, oldLog(path))
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
}

func oldLog(path string) string {
	const ext = ".log"
	if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
		return path[:len(path)-len(ext)] + ".old" + ext
	}
	return path + ".old"
}
