//go:build windows

package lsp

import "os/exec"

func setProcGroup(cmd *exec.Cmd) {}

func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
