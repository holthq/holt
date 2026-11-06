//go:build windows

package termd

import (
	"os/exec"
	"syscall"
)

// detachProc 让 termd 自己一个进程组，不跟着应用的控制台。
func detachProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// daemonSupported 是这台机器上能不能跑 termd：Windows 上还没有伪终端，别起一个什么都开不了的 daemon。
func daemonSupported() error { return ErrNoPTY }
