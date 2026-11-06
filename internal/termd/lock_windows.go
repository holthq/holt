//go:build windows

package termd

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx 的参数：独占、拿不到就马上返回错误。
const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

var procLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

// lockFile 非阻塞地独占 <sock>.lock：已经有人拿着时返回错误（调用方过一会儿再试）。
// 进程退出时句柄关掉，锁跟着放掉（不用 UnlockFileEx）。
func lockFile(f *os.File) error {
	var ol syscall.Overlapped
	r, _, err := procLockFileEx.Call(uintptr(f.Fd()), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return err
	}
	return nil
}
