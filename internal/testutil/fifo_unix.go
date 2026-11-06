//go:build !windows

package testutil

import (
	"os"
	"syscall"
)

// MakeFIFO 建一个命名管道（FIFO），给"不是普通文件"的测试用（拷贝跳过、打开不卡住之类）。
// Windows 上没有 FIFO，那里的实现返回 ErrNoFIFO，调用的人把这一点跳过去。
func MakeFIFO(path string, perm os.FileMode) error { return syscall.Mkfifo(path, uint32(perm)) }
