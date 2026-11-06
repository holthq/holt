//go:build !windows

package app

import (
	"os"
	"os/exec"
	"syscall"
)

// setToolProcGroup 让外部命令自己一个进程组：超时、取消时连同它起的子进程一起杀。
func setToolProcGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTool 杀掉 p 的整个进程组（负的进程号）。
func killTool(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGKILL) }
