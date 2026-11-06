package termd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ErrNoDaemon 是起 termd 失败。
var ErrNoDaemon = errors.New("termd: 后台进程没起来")

// StartTimeout 是起 termd 后等它能连上多久。
const StartTimeout = 2 * time.Second

// Spawn 在后台起一个 termd：exe termd sock logPath，自己一个会话（Setsid，退出 Holt 不带走它），
// 标准输入输出接 /dev/null，环境是 env（nil 是这个进程的）。等到 sock 上能 Ping 通，最多 StartTimeout。
func Spawn(exe, sock, logPath string, env []string) error {
	if err := daemonSupported(); err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	cmd := exec.Command(exe, "termd", sock, logPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	cmd.Env = env
	detachProc(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w：%v", ErrNoDaemon, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }() // 应用还开着时 termd 退了也不留僵尸
	deadline := time.Now().Add(StartTimeout)
	for {
		if Ping(sock) == nil {
			return nil
		}
		select {
		case err := <-exited:
			// 一起起的另一个占住了套接字的，这个直接退了：那个能连上也算
			if Ping(sock) == nil {
				return nil
			}
			return fmt.Errorf("%w：退出了（%v）", ErrNoDaemon, err)
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w：%v 内连不上", ErrNoDaemon, StartTimeout)
		}
	}
}
