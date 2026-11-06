//go:build linux

package termd

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// openPty 开一个伪终端：先解锁主端（TIOCSPTLCK），再问它的从端编号（TIOCGPTN），打开 /dev/pts/N。
// 返回主从两端（还没设大小）。和 macOS 上 grantpt / unlockpt / ptsname 三步对应。
func openPty() (master, slave *os.File, err error) {
	master, err = openPtyFile()
	if err != nil {
		return nil, nil, err
	}
	var unlock int32 // 0 是解锁
	if err := ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("pty: 解锁：%w", err)
	}
	var n uint32
	if err := ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("pty: 问从端编号：%w", err)
	}
	name := fmt.Sprintf("/dev/pts/%d", n)
	slave, err = os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("pty: 打开 %s：%w", name, err)
	}
	return master, slave, nil
}
