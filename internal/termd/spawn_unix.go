//go:build darwin || linux

package termd

import (
	"os/exec"
	"syscall"
)

// detachProc 让 termd 自己一个会话：应用退出、关窗口都不带走它。
func detachProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// daemonSupported 是这台机器上能不能跑 termd。
func daemonSupported() error { return nil }
