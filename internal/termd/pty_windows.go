//go:build windows

package termd

import (
	"errors"
	"os"
	"os/exec"
)

// ErrNoPTY 是 Windows 上还没有后台终端：termd 的伪终端要 ConPTY（CreatePseudoConsole），还没实现。
// 应用收到这个错误就当 termd 不可用，退回窗口里的本地终端。
var ErrNoPTY = errors.New("termd: Windows 上暂不支持后台终端")

// proc 是 pty_unix.go 里那一个的桩：Windows 上 startProc 直接失败，这些方法不会被调到。
type proc struct {
	master *os.File
	cmd    *exec.Cmd
	done   chan struct{}
	code   int
}

type procSpec struct {
	Path         string
	Args, Env    []string
	Dir          string
	Cols, Rows   int
	CellW, CellH int
}

func startProc(procSpec) (*proc, error) { return nil, ErrNoPTY }

func (p *proc) Read([]byte) (int, error) { return 0, ErrNoPTY }

func (p *proc) Write(b []byte) (int, error) { return 0, ErrNoPTY }

func (p *proc) pid() int { return 0 }

func (p *proc) resize(int, int, int, int) error { return ErrNoPTY }

func (p *proc) busy() bool { return false }

func (p *proc) hangup() {}
