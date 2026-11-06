//go:build !windows

package git

import (
	"os/exec"
	"syscall"
)

// setProcGroup 让 git 自己一个进程组：取消时整组收信号，ssh、远程助手这些子进程一起停。
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// termGroup 给整个进程组发 SIGTERM（让 git 自己收尾）。
func termGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }

// killGroup 给整个进程组发 SIGKILL（收尾时间过了，连子进程一起）。
func killGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }
