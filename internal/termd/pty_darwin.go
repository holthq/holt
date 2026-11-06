//go:build darwin

package termd

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// macOS 上 grantpt / unlockpt / ptsname 用的 ioctl（sys/ttycom.h）。
const (
	ioctlPtyGrant  = 0x20007454
	ioctlPtyUnlock = 0x20007452
	ioctlPtyName   = 0x40807453
)

// openPty 开一个伪终端，返回主从两端（还没设大小）。
func openPty() (master, slave *os.File, err error) {
	master, err = openPtyFile()
	if err != nil {
		return nil, nil, err
	}
	var name [128]byte
	for _, req := range []struct {
		op  uintptr
		arg unsafe.Pointer
	}{{ioctlPtyGrant, nil}, {ioctlPtyUnlock, nil}, {ioctlPtyName, unsafe.Pointer(&name[0])}} {
		if err := ioctl(master, req.op, req.arg); err != nil {
			master.Close()
			return nil, nil, fmt.Errorf("pty: %w", err)
		}
	}
	n := bytes.IndexByte(name[:], 0)
	if n < 0 {
		n = len(name)
	}
	slave, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("pty: 打开 %s：%w", name[:n], err)
	}
	return master, slave, nil
}
