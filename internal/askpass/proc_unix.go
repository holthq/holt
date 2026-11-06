//go:build !windows

package askpass

import (
	"errors"
	"syscall"
)

// processAlive 是 pid 还在（信号 0 只查进程在不在，不真的发信号）。
// 权限不够查（EPERM）说明进程在，只是不是自己的。
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
