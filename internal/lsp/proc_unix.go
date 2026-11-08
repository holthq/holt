//go:build unix

package lsp

import (
	"os/exec"
	"syscall"
)

// setProcGroup 让语言服务器和它起的子进程在同一个进程组里，关的时候一起杀。
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcess 杀掉语言服务器整个进程组。
func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
