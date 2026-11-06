//go:build darwin || linux

package termd

import (
	"os"
	"syscall"
)

// lockFile 非阻塞地独占 <sock>.lock：已经有人拿着时返回错误（调用方过一会儿再试）。
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
