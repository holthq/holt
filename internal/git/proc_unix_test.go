//go:build !windows

package git

import "syscall"

// processAlive 是 pid 还在（测试用：信号 0 只查进程在不在）。
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
