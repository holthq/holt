package termd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// macOS 上 grantpt / unlockpt / ptsname 用的 ioctl（sys/ttycom.h）。
const (
	ioctlPtyGrant  = 0x20007454
	ioctlPtyUnlock = 0x20007452
	ioctlPtyName   = 0x40807453
)

// proc 是跑在自己那个伪终端里的 shell：自己一个会话，伪终端是它的控制终端，
// 所以 ⌃C 之类的信号给前台进程组，和 termd 不沾边。
type proc struct {
	master *os.File
	cmd    *exec.Cmd
	done   chan struct{} // shell 退出时关上，之后 code 可读
	code   int
}

type procSpec struct {
	Path         string
	Args, Env    []string
	Dir          string
	Cols, Rows   int
	CellW, CellH int
}

func startProc(o procSpec) (*proc, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("pty: %w", err)
	}
	var name [128]byte
	for _, req := range []struct {
		op  uintptr
		arg unsafe.Pointer
	}{{ioctlPtyGrant, nil}, {ioctlPtyUnlock, nil}, {ioctlPtyName, unsafe.Pointer(&name[0])}} {
		if err := ioctl(master, req.op, req.arg); err != nil {
			master.Close()
			return nil, fmt.Errorf("pty: %w", err)
		}
	}
	n := bytes.IndexByte(name[:], 0)
	if n < 0 {
		n = len(name)
	}
	slave, err := os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: 打开 %s：%w", name[:n], err)
	}
	defer slave.Close() // shell 有它自己的一份
	if err := setSize(master, o.Cols, o.Rows, o.CellW, o.CellH); err != nil {
		master.Close()
		return nil, err
	}
	cmd := &exec.Cmd{Path: o.Path, Args: o.Args, Dir: o.Dir, Env: o.Env, Stdin: slave, Stdout: slave, Stderr: slave}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	p := &proc{master: master, cmd: cmd, done: make(chan struct{})}
	go func() {
		p.code = exitCode(cmd.Wait())
		close(p.done)
	}()
	return p, nil
}

// exitCode 是 shell 的退出码；被信号杀掉的是 128 + 信号，和 shell 里 $? 一样。
func exitCode(err error) int {
	var e *exec.ExitError
	if !errors.As(err, &e) {
		return 0
	}
	if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return e.ExitCode()
}

// Read 读 shell 的输出；没人再开着伪终端时是 io.EOF。
func (p *proc) Read(b []byte) (int, error) {
	n, err := p.master.Read(b)
	if err != nil && n == 0 && (errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)) {
		err = io.EOF
	}
	return n, err
}

func (p *proc) Write(b []byte) (int, error) { return p.master.Write(b) }

func (p *proc) pid() int { return p.cmd.Process.Pid }

func (p *proc) resize(cols, rows, cellW, cellH int) error {
	return setSize(p.master, cols, rows, cellW, cellH)
}

// busy 是前台有 shell 以外的进程组（在跑命令）。
func (p *proc) busy() bool {
	var pgid int32
	if ioctl(p.master, syscall.TIOCGPGRP, unsafe.Pointer(&pgid)) != nil {
		return false
	}
	return pgid > 0 && int(pgid) != p.pid()
}

// hangup 挂断：进程组收到 SIGHUP、伪终端关掉；2 秒还没退的 SIGKILL。
func (p *proc) hangup() {
	syscall.Kill(-p.pid(), syscall.SIGHUP)
	p.master.Close()
	go func() {
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			if syscall.Kill(-p.pid(), syscall.SIGKILL) != nil {
				p.cmd.Process.Kill()
			}
		}
	}()
}

func setSize(f *os.File, cols, rows, cellW, cellH int) error {
	ws := struct{ row, col, xpixel, ypixel uint16 }{clamp16(rows), clamp16(cols), pixels(cols, cellW), pixels(rows, cellH)}
	return ioctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
}

func clamp16(v int) uint16 { return uint16(max(1, min(v, 0xFFFF))) }

// pixels 是 n 格占多少像素，不知道格子多大时是 0。
func pixels(n, cell int) uint16 { return uint16(max(0, min(n*cell, 0xFFFF))) }

func ioctl(f *os.File, op uintptr, arg unsafe.Pointer) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := c.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, op, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
