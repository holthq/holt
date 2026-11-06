//go:build windows

package app

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setToolProcGroup 让外部命令自己一个进程组。Windows 上应用是 GUI 程序、没有控制台，
// GenerateConsoleCtrlEvent 发不到，杀的时候用 taskkill 结束整棵进程树。
func setToolProcGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killTool 结束 p 和它起的子进程（taskkill /T /F）。
func killTool(p *os.Process) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
