package termd

import (
	"bufio"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// SockPath 是状态目录 stateDir 的 termd 用的套接字：<stateDir>/termd.sock；
// 太长（macOS 的 sun_path 只有 104 字节）时改用临时目录里按 stateDir 的哈希起名的一个。
func SockPath(stateDir string) string {
	p := filepath.Join(stateDir, "termd.sock")
	if len(p) < 100 {
		return p
	}
	sum := sha1.Sum([]byte(stateDir))
	return filepath.Join(os.TempDir(), fmt.Sprintf("grove-termd-%x.sock", sum[:6]))
}

func dial(sock string) (net.Conn, error) {
	return net.DialTimeout("unix", sock, time.Second)
}

// roundTrip 发一个请求帧、读一个回答帧。
func roundTrip(sock string, typ byte, p []byte) (byte, []byte, error) {
	c, err := dial(sock)
	if err != nil {
		return 0, nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(c, typ, p); err != nil {
		return 0, nil, err
	}
	return readFrame(bufio.NewReader(c))
}

// Ping 是 sock 上有没有一个答话的 termd。
func Ping(sock string) error {
	typ, _, err := roundTrip(sock, framePing, nil)
	if err == nil && typ != frameOK {
		err = fmt.Errorf("termd: ping 回了 %q", typ)
	}
	return err
}

// List 列出 termd 里的会话，开的先后排。
func List(sock string) ([]Info, error) {
	typ, p, err := roundTrip(sock, frameList, nil)
	if err != nil {
		return nil, err
	}
	if typ != frameListed {
		return nil, fmt.Errorf("termd: list 回了 %q", typ)
	}
	var out []Info
	return out, json.Unmarshal(p, &out)
}

// Killall 关掉所有会话（shell 收到 SIGHUP），返回关了几个。没有会话、没有连接的 termd 过一会儿自己退出。
func Killall(sock string) (int, error) {
	typ, p, err := roundTrip(sock, frameKillall, nil)
	if err != nil {
		return 0, err
	}
	v, ok := parseUint32s(p, 1)
	if typ != frameOK || !ok {
		return 0, fmt.Errorf("termd: killall 回了 %q", typ)
	}
	return v[0], nil
}

// Stop 让 termd 挂断所有 shell 后马上退出。
func Stop(sock string) error {
	_, _, err := roundTrip(sock, frameStop, nil)
	return err
}

// Conn 是一个会话的终端流，给 terminal.Options.Conn（配 ResizeByConn）：读到的是 termd 那边终端的镜像帧，
// 写的是敲的字；终端插件用 ResizePixels 告诉它视图的大小、UseTheme 告诉它颜色，都转给 termd。
//
// Read 在 Start 之前一直等着：应用先建好终端、按 Hello 的大小 Resize，再放第一帧进来。
// 回调都在终端读的那个 goroutine 里、在它后面的帧之前调用：Start 之前设好。
type Conn struct {
	Hello Hello

	// OnResized 是 termd 那边的屏幕换了大小：调 Terminal.Resize。
	OnResized func(cols, rows int)
	// OnDir 是 shell 报的目录（OSC 7，镜像帧里没有）。
	OnDir func(dir string)
	// OnExited 是 shell 退出了；会话还在，直到 Kill。
	OnExited func(code int)
	// OnGone 是会话没了；kicked 是被别的连接接走了。之后 Read 返回 io.EOF。
	OnGone func(kicked bool)
	// OnNoClear 是全屏程序里没清屏。
	OnNoClear func()

	c         net.Conn
	r         *bufio.Reader
	wmu       sync.Mutex
	buf       []byte
	ready     chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

// New 在 termd 里开一个新会话。
func New(sock string, req NewReq) (*Conn, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return open(sock, frameNew, b)
}

// Attach 接回会话 req.Sid；不在了是 ErrGone。
func Attach(sock string, req AttachReq) (*Conn, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return open(sock, frameAttach, b)
}

func open(sock string, typ byte, req []byte) (*Conn, error) {
	c, err := dial(sock)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(c, typ, req); err != nil {
		c.Close()
		return nil, err
	}
	r := bufio.NewReaderSize(c, 64<<10)
	t, p, err := readFrame(r)
	if err != nil {
		c.Close()
		return nil, err
	}
	switch t {
	case frameHello:
	case frameGone:
		c.Close()
		return nil, ErrGone
	case frameError:
		c.Close()
		return nil, fmt.Errorf("termd: %s", p)
	default:
		c.Close()
		return nil, fmt.Errorf("termd: 回了 %q", t)
	}
	c.SetDeadline(time.Time{})
	conn := &Conn{c: c, r: r, ready: make(chan struct{})}
	if err := json.Unmarshal(p, &conn.Hello); err != nil {
		c.Close()
		return nil, err
	}
	return conn, nil
}

// Start 放帧进来。
func (c *Conn) Start() { c.startOnce.Do(func() { close(c.ready) }) }

func (c *Conn) Read(p []byte) (int, error) {
	<-c.ready
	for len(c.buf) == 0 {
		typ, payload, err := readFrame(c.r)
		if err != nil {
			return 0, io.EOF
		}
		switch typ {
		case frameOutput:
			c.buf = payload
		case frameResized:
			if v, ok := parseUint32s(payload, 2); ok && c.OnResized != nil {
				c.OnResized(v[0], v[1])
			}
		case frameDir:
			if c.OnDir != nil {
				c.OnDir(string(payload))
			}
		case frameExited:
			if len(payload) == 4 && c.OnExited != nil {
				c.OnExited(int(int32(binary.BigEndian.Uint32(payload))))
			}
		case frameNoClear:
			if c.OnNoClear != nil {
				c.OnNoClear()
			}
		case frameGone:
			if c.OnGone != nil {
				c.OnGone(string(payload) == "kicked")
			}
			return 0, io.EOF
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *Conn) send(typ byte, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.c, typ, p)
}

func (c *Conn) Write(p []byte) (int, error) {
	if err := c.send(frameInput, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ResizePixels 告诉 termd 视图的大小（格子的像素大小给程序问的时候答）。
func (c *Conn) ResizePixels(cols, rows, cellW, cellH int) error {
	return c.send(frameResize, uint32s(cols, rows, cellW, cellH))
}

// UseTheme 告诉 termd 视图的颜色，程序问前景 / 背景色时它答这个。
func (c *Conn) UseTheme(th *terminal.Theme) error {
	if th == nil {
		return nil
	}
	b, err := json.Marshal(th)
	if err != nil {
		return err
	}
	return c.send(frameTheme, b)
}

// Clear 让 termd 那边清屏（⌘K），下一帧就是清过的。
func (c *Conn) Clear() error { return c.send(frameClear, nil) }

// Kill 关掉会话：shell 收到 SIGHUP。之后关连接。
func (c *Conn) Kill() error {
	err := c.send(frameKill, nil)
	c.Close()
	return err
}

// Detach 断开，会话留在 termd 里。
func (c *Conn) Detach() error {
	err := c.send(frameDetach, nil)
	c.Close()
	return err
}

// Close 断开（和 Detach 一样，会话留着）。
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.Start() // 还在等 Start 的 Read 也放出来
		err = c.c.Close()
	})
	return err
}
