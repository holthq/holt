//go:build windows

package git

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcGroup 让 git 自己一个进程组。Windows 上应用是 GUI 程序、没有控制台，
// GenerateConsoleCtrlEvent 发不到，取消时用 taskkill 结束整棵进程树。
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// termGroup 结束 git 和它起的子进程。Windows 没有 SIGTERM，直接 taskkill /F。
func termGroup(pid int) error { return taskkill(pid) }

// killGroup 同 termGroup：taskkill /F 本来就是强杀。
func killGroup(pid int) error { return taskkill(pid) }

// taskkill 结束 pid 和它起的进程（/T 整棵树，/F 强杀）。进程已经没了时 taskkill 报错，
// 调用方都是忽略错误。不要弹窗。
func taskkill(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
