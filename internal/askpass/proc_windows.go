//go:build windows

package askpass

import (
	"os"
)

// processAlive 是 pid 还在：能打开它的进程句柄就算在（退出了的进程句柄打不开）。
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
