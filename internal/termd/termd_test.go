package termd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// 测试里的 termd 都在本进程里跑，套接字在 t.TempDir()（太长时 SockPath 换到临时目录，Cleanup 删掉）；
// shell 是 /bin/sh，HOME 指到临时目录，不读用户的配置。TestSpawnDetached 起的是测试二进制自己（WORKBENCH_TERMD_HELPER=1）。

func TestMain(m *testing.M) {
	if os.Getenv("WORKBENCH_TERMD_HELPER") == "1" && len(os.Args) > 1 && os.Args[1] == "termd" {
		os.Exit(Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func testSock(t *testing.T) string {
	t.Helper()
	sock := SockPath(t.TempDir())
	t.Cleanup(func() {
		os.Remove(sock)
		os.Remove(sock + ".lock")
	})
	return sock
}

func startServer(t *testing.T, idle time.Duration) (*Server, string) {
	t.Helper()
	sock := testSock(t)
	s := &Server{Sock: sock, Idle: idle}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() {
		s.Serve()
		close(served)
	}()
	t.Cleanup(func() {
		s.Stop()
		<-served
	})
	return s, sock
}

// shReq 起一个干净的 /bin/sh。HISTFILE 置空：shell 挂断时会把历史写进 $HOME/.bash_history，
// 而 Server.Stop 不等 shell 退，慢机器上这一笔会落在 t.TempDir 删目录的时候（"directory not empty"）。
func shReq(t *testing.T) NewReq {
	home := t.TempDir()
	return NewReq{
		Shell: "/bin/sh", Dir: home, Cols: 80, Rows: 24,
		Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PS1=$ ", "ENV=", "HISTFILE="},
	}
}

// view 是接着会话的窗口那一头：一个 ResizeByConn 的终端看 Conn 的帧。
type view struct {
	conn   *Conn
	term   *terminal.Terminal
	exited chan int
	gone   chan bool
	dirs   chan string
}

func show(t *testing.T, c *Conn) *view {
	t.Helper()
	v := &view{conn: c, exited: make(chan int, 4), gone: make(chan bool, 4), dirs: make(chan string, 16)}
	c.OnExited = func(code int) { v.exited <- code }
	c.OnGone = func(kicked bool) { v.gone <- kicked }
	c.OnDir = func(d string) { v.dirs <- d }
	term, err := terminal.New(terminal.Options{Conn: c, ResizeByConn: true})
	if err != nil {
		t.Fatal(err)
	}
	term.Resize(c.Hello.Cols, c.Hello.Rows)
	c.OnResized = term.Resize
	c.Start()
	v.term = term
	t.Cleanup(func() { term.Close() })
	return v
}

func (v *view) waitText(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(v.term.Text(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("屏幕上没有 %q：\n%s", want, v.term.Text())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (v *view) typeLine(t *testing.T, line string) {
	t.Helper()
	if _, err := v.conn.Write([]byte(line + "\r")); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("等不到：%s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustList(t *testing.T, sock string) []Info {
	t.Helper()
	l, err := List(sock)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// 伪终端：shell 看到的大小、它是一个 tty、退出码。
func TestPTY(t *testing.T) {
	p, err := startProc(procSpec{
		Path: "/bin/sh", Args: []string{"sh", "-c", "stty size; tty; exit 3"},
		Env: []string{"PATH=/usr/bin:/bin"}, Dir: t.TempDir(), Cols: 100, Rows: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := p.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	<-p.done
	if !strings.Contains(string(out), "30 100") || !strings.Contains(string(out), "/dev/ttys") {
		t.Errorf("输出 %q", out)
	}
	if p.code != 3 {
		t.Errorf("退出码 %d", p.code)
	}
}

// 开一个会话、敲命令、断开；list 里它还在、没人接着；再接回来，第一帧就有之前的输出。
func TestSessionNewAttach(t *testing.T) {
	_, sock := startServer(t, -1)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Hello.Sid == "" || c.Hello.Pid == 0 || c.Hello.Cols != 80 || c.Hello.Rows != 24 {
		t.Fatalf("hello %+v", c.Hello)
	}
	v := show(t, c)
	v.typeLine(t, "echo hello-$((40+2))")
	v.waitText(t, "hello-42")
	v.term.Close()
	waitFor(t, "断开后没人接着", func() bool {
		l := mustList(t, sock)
		return len(l) == 1 && !l[0].Attached
	})
	if err := syscall.Kill(c.Hello.Pid, 0); err != nil {
		t.Fatalf("断开后 shell 没了：%v", err)
	}

	c2, err := Attach(sock, AttachReq{Sid: c.Hello.Sid})
	if err != nil {
		t.Fatal(err)
	}
	if c2.Hello.Pid != c.Hello.Pid || c2.Hello.Exited {
		t.Fatalf("接回来 %+v", c2.Hello)
	}
	v2 := show(t, c2)
	v2.waitText(t, "hello-42")
	v2.typeLine(t, "echo again-$((1+1))")
	v2.waitText(t, "again-2")
	if !mustList(t, sock)[0].Attached {
		t.Error("接着的没算 attached")
	}

	if _, err := Attach(sock, AttachReq{Sid: "nope"}); !errors.Is(err, ErrGone) {
		t.Errorf("不认识的 sid：%v", err)
	}
}

// 窗口的大小经 ResizePixels 给 termd，termd 换了大小发 resized，窗口的终端跟着换；shell 看到新大小。
func TestResizeByConn(t *testing.T) {
	_, sock := startServer(t, -1)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "echo ready")
	v.waitText(t, "ready")
	if err := c.ResizePixels(100, 30, 8, 16); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "窗口的终端换成 100×30", func() bool {
		cols, rows := v.term.Size()
		return cols == 100 && rows == 30
	})
	v.typeLine(t, "stty size")
	v.waitText(t, "30 100")

	// 接的时候带大小：先换成它再发第一帧
	v.term.Close()
	c2, err := Attach(sock, AttachReq{Sid: c.Hello.Sid, Cols: 90, Rows: 20, CellW: 8, CellH: 16})
	if err != nil {
		t.Fatal(err)
	}
	if c2.Hello.Cols != 90 || c2.Hello.Rows != 20 {
		t.Fatalf("接的时候带的大小 %+v", c2.Hello)
	}
	v2 := show(t, c2)
	v2.typeLine(t, "stty size")
	v2.waitText(t, "20 90")
}

// shell 退出：窗口收到退出码，会话留着（接回来是已退出），关掉之后才没了（没人接着过 Linger 也没了，见下面两个）。
func TestExit(t *testing.T) {
	_, sock := startServer(t, -1)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "exit 7")
	select {
	case code := <-v.exited:
		if code != 7 {
			t.Fatalf("退出码 %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("没收到退出")
	}
	l := mustList(t, sock)
	if len(l) != 1 || !l[0].Exited || l[0].Code != 7 {
		t.Fatalf("list %+v", l)
	}
	v.term.Close()
	c2, err := Attach(sock, AttachReq{Sid: c.Hello.Sid})
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Hello.Exited || c2.Hello.Code != 7 {
		t.Fatalf("接回已退出的 %+v", c2.Hello)
	}
	if err := c2.Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "关掉后 list 空", func() bool { return len(mustList(t, sock)) == 0 })
}

// 退出了、没有窗口接着的会话过 Linger 拿掉：退出时就没人接的、接着退出之后断开的都是；只剩它们时 termd 照样闲下来退出。
func TestExitedSessionRemoved(t *testing.T) {
	s, sock := startServer(t, 200*time.Millisecond)
	s.Linger = 50 * time.Millisecond
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "exit 4")
	select {
	case <-v.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("没收到退出")
	}
	// 没人接着时退出：shell 在断开之后才读到 exit
	c2, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Write([]byte("sleep 0.2; exit\r")); err != nil {
		t.Fatal(err)
	}
	c2.Close()
	waitFor(t, "没人接的那个退出", func() bool {
		l := mustList(t, sock)
		return len(l) == 1 && l[0].Sid == c.Hello.Sid
	})
	if l := mustList(t, sock); !l[0].Exited || !l[0].Attached {
		t.Fatalf("接着的退出了的会话应留着：%+v", l)
	}
	v.term.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("只剩退出了的会话也该闲下来退出：%+v", mustList(t, sock))
	}
}

// 退出了的会话接着时一直留着（窗口显示“已退出”），断开（detach）之后过 Linger 才拿掉；期间接回去的不拿。
func TestExitedAttachedKeptUntilDetach(t *testing.T) {
	s, sock := startServer(t, -1)
	s.Linger = 150 * time.Millisecond
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "exit 5")
	select {
	case <-v.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("没收到退出")
	}
	time.Sleep(400 * time.Millisecond)
	if l := mustList(t, sock); len(l) != 1 || !l[0].Exited || !l[0].Attached || l[0].Code != 5 {
		t.Fatalf("接着时应留着：%+v", l)
	}
	if err := c.Detach(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "断开", func() bool { l := mustList(t, sock); return len(l) == 1 && !l[0].Attached })
	c2, err := Attach(sock, AttachReq{Sid: c.Hello.Sid}) // Linger 之内接回去
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Hello.Exited || c2.Hello.Code != 5 {
		t.Fatalf("接回已退出的 %+v", c2.Hello)
	}
	time.Sleep(400 * time.Millisecond)
	if l := mustList(t, sock); len(l) != 1 {
		t.Fatalf("接回去之后不该拿掉：%+v", l)
	}
	c2.Detach()
	waitFor(t, "断开之后拿掉", func() bool { return len(mustList(t, sock)) == 0 })
}

// 第二个接上的把第一个挤掉：第一个收到 gone（kicked）。
func TestSecondAttachKicksFirst(t *testing.T) {
	_, sock := startServer(t, -1)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "echo one")
	v.waitText(t, "one")
	c2, err := Attach(sock, AttachReq{Sid: c.Hello.Sid})
	if err != nil {
		t.Fatal(err)
	}
	v2 := show(t, c2)
	select {
	case kicked := <-v.gone:
		if !kicked {
			t.Error("gone 没说是被挤掉的")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("第一个没被挤掉")
	}
	v2.waitText(t, "one")
	l := mustList(t, sock)
	if len(l) != 1 || !l[0].Attached {
		t.Fatalf("list %+v", l)
	}
}

// 没有会话、没有连接 Idle 之后自己退出、删掉套接字；有会话时不退。
func TestIdleExit(t *testing.T) {
	s, sock := startServer(t, 150*time.Millisecond)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	time.Sleep(500 * time.Millisecond)
	select {
	case <-s.Done():
		t.Fatal("有会话时退了")
	default:
	}
	cl, err := Attach(sock, AttachReq{Sid: c.Hello.Sid})
	if err != nil {
		t.Fatal(err)
	}
	cl.Kill()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("闲了没退")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("套接字还在：%v", err)
	}
}

// 套接字只有自己能读写；同一个套接字上起第二个是 ErrRunning。
func TestSocketPerms(t *testing.T) {
	_, sock := startServer(t, -1)
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("权限 %v", st.Mode().Perm())
	}
	if err := (&Server{Sock: sock}).Listen(); !errors.Is(err, ErrRunning) {
		t.Errorf("第二个：%v", err)
	}
	long := filepath.Join(t.TempDir(), strings.Repeat("x", 80))
	if p := SockPath(long); len(p) >= 104 || !strings.HasPrefix(p, os.TempDir()) {
		t.Errorf("太长的状态目录：%s", p)
	}
}

// killall 挂断所有 shell：接着的窗口收到退出（SIGHUP 是 129），list 空了。
func TestKillall(t *testing.T) {
	_, sock := startServer(t, -1)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "echo up")
	v.waitText(t, "up")
	c2, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()
	n, err := Killall(sock)
	if err != nil || n != 2 {
		t.Fatalf("killall %d %v", n, err)
	}
	select {
	case code := <-v.exited:
		if code != 129 {
			t.Errorf("退出码 %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("接着的没收到退出")
	}
	if l := mustList(t, sock); len(l) != 0 {
		t.Fatalf("list %+v", l)
	}
	waitFor(t, "shell 都没了", func() bool {
		return syscall.Kill(c.Hello.Pid, 0) != nil && syscall.Kill(c2.Hello.Pid, 0) != nil
	})
}

// list 的 JSON：工作区、仓库、pid，前台在跑命令的是 busy。
func TestListJSON(t *testing.T) {
	_, sock := startServer(t, -1)
	req := shReq(t)
	req.WS, req.Repo = "/tmp/ws.code-workspace", "/tmp/repo"
	c, err := New(sock, req)
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.typeLine(t, "sleep 30")
	waitFor(t, "busy", func() bool {
		l := mustList(t, sock)
		return len(l) == 1 && l[0].Busy
	})
	l := mustList(t, sock)
	if l[0].WS != req.WS || l[0].Repo != req.Repo || l[0].Pid != c.Hello.Pid || l[0].Sid != c.Hello.Sid || !l[0].Attached {
		t.Fatalf("list %+v", l[0])
	}
	b, _ := json.Marshal(l[0])
	for _, k := range []string{`"sid"`, `"ws"`, `"repo"`, `"pid"`, `"busy":true`, `"attached":true`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("JSON 里没有 %s：%s", k, b)
		}
	}
	c.Write([]byte{3}) // ⌃C
	waitFor(t, "⌃C 后不 busy", func() bool { return !mustList(t, sock)[0].Busy })
}

// Spawn 起的 termd 是自己一个会话的后台进程，日志写进给的文件；Stop 让它退出、删掉套接字。
func TestSpawnDetached(t *testing.T) {
	sock := testSock(t)
	logPath := filepath.Join(t.TempDir(), "termd.log")
	env := append(os.Environ(), "WORKBENCH_TERMD_HELPER=1")
	if err := Spawn(os.Args[0], sock, logPath, env); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Stop(sock) })
	b, _ := os.ReadFile(logPath)
	m := regexp.MustCompile(`termd (\d+) 开始`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("日志 %q", b)
	}
	pid, _ := strconv.Atoi(string(m[1]))
	if sid, err := getsid(pid); err != nil || sid != pid {
		t.Errorf("termd %d 的会话 %d（%v）：不是自己一个会话", pid, sid, err)
	}
	if err := Stop(sock); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "termd 退出", func() bool { return syscall.Kill(pid, 0) != nil })
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("套接字还在：%v", err)
	}
}

func getsid(pid int) (int, error) {
	r, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}

// 终端配置：NewReq.Args 有了就是 [shell] + Args（没有照旧 -l）；不带 / 的 shell 按 Env 的 PATH 找；
// 配置的环境接在后面，同名的后面的算。
func TestNewReqArgs(t *testing.T) {
	path := "PATH=/usr/bin:/bin"
	if p, args := shellCommand("/bin/sh", nil, nil); p != "/bin/sh" || !slices.Equal(args, []string{"/bin/sh", "-l"}) {
		t.Fatalf("没参数：%q %q", p, args)
	}
	if p, args := shellCommand("sh", []string{"-i"}, []string{path}); p != "/bin/sh" || !slices.Equal(args, []string{"sh", "-i"}) {
		t.Fatalf("有参数：%q %q", p, args)
	}

	_, sock := startServer(t, -1)
	req := shReq(t)
	req.Shell, req.Args = "sh", []string{"-c", "echo args-$((2*3)); echo env-$PROF_X; exec cat"}
	req.Env = append(req.Env, "PROF_X=one", "PROF_X=two")
	c, err := New(sock, req)
	if err != nil {
		t.Fatal(err)
	}
	v := show(t, c)
	v.waitText(t, "args-6")
	v.waitText(t, "env-two")
}

// 响铃和通知：BEL、OSC 9、OSC 777 各告诉窗口一次；OSC 0 / 2 / 7 / 52 里当结束符的 BEL 不算。
func TestBell(t *testing.T) {
	_, sock := startServer(t, -1)
	c, err := New(sock, shReq(t))
	if err != nil {
		t.Fatal(err)
	}
	var bells atomic.Int32
	c.OnBell = func() { bells.Add(1) }
	v := show(t, c)
	cases := []struct {
		seq  string
		want int32
	}{
		{`\033]0;标题\007`, 0},
		{`\033]2;title\007`, 0},
		{`\033]7;file://localhost/tmp\007`, 0},
		{`\033]52;p;aGk=\007`, 0}, // primary：终端不写剪贴板（测试不碰开发机的剪贴板）
		{`\007`, 1},
		{`\033]9;agent 等你\007`, 1},
		{`\033]777;notify;Claude;等你\007`, 1},
	}
	for i, tc := range cases {
		before := bells.Load()
		v.typeLine(t, fmt.Sprintf("printf '%s'; echo mark-$((%d+1000))", tc.seq, i))
		v.waitText(t, fmt.Sprintf("mark-%d", i+1000))
		if got := bells.Load() - before; got != tc.want {
			t.Errorf("%s：响了 %d 次，要 %d 次", tc.seq, got, tc.want)
		}
	}
}
