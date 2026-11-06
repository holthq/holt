// Package crash 把崩溃和后台出的错记到日志目录（打包的应用是 ~/Library/Logs/Holt/）：
//
//   - 崩溃：runtime/debug.SetCrashOutput 让没接住的 panic 和 fatal error（任何 goroutine 里的，带所有 goroutine 的栈）
//     除了 stderr 也写进 crash-pending.log。这个文件每次启动先清空、写好版本和启动时间；下次启动时它比开头长，
//     就是上次崩了，改名成 crash-日期-时间.log 留下（最多留 MaxCrashLogs 份），Setup 把它的路径交给界面去提示。
//   - 错误：标准库 log 的输出同时写进 workbench.log（每次启动先写一行版本和时间），超过 MaxLogSize 时
//     旧的改名成 workbench.old.log。后台读状态失败、git 操作失败、state.json 读写失败都经 log 记在这里。
package crash

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

const (
	PendingName  = "crash-pending.log"
	LogName      = "workbench.log"
	OldLogName   = "workbench.old.log"
	MaxLogSize   = 1 << 20
	MaxCrashLogs = 10
	sep          = "----\n" // 开头和崩溃输出之间的分隔
)

// Setup 在 dir 里开好崩溃日志、接上 SetCrashOutput，返回错误日志的 writer（调用方 log.SetOutput 它和 stderr）。
// header 是写在两个文件开头的那行（版本）。last 是上次崩溃留下的日志，没有为空。
func Setup(dir, header string) (last string, logw io.WriteCloser, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	pending := filepath.Join(dir, PendingName)
	if b, err := os.ReadFile(pending); err == nil {
		if _, rest, ok := bytes.Cut(b, []byte(sep)); ok && len(bytes.TrimSpace(rest)) > 0 {
			when := time.Now()
			if info, err := os.Stat(pending); err == nil {
				when = info.ModTime() // 崩溃输出写进去的时候
			}
			last = filepath.Join(dir, "crash-"+when.Format("20060102-150405")+".log")
			if err := os.Rename(pending, last); err != nil {
				last = ""
			}
			prune(dir)
		}
	}
	start := header + "\nstarted " + time.Now().Format(time.RFC3339) + "\n"
	f, err := os.OpenFile(pending, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return last, nil, err
	}
	_, err = f.WriteString(start + sep)
	if err == nil {
		err = debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	f.Close() // SetCrashOutput 复制了文件描述符
	if err != nil {
		return last, nil, err
	}

	lp := filepath.Join(dir, LogName)
	if info, err := os.Stat(lp); err == nil && info.Size() > MaxLogSize {
		os.Rename(lp, filepath.Join(dir, OldLogName))
	}
	lf, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return last, nil, err
	}
	lf.WriteString("\n" + start)
	return last, lf, nil
}

// prune 只留最新的 MaxCrashLogs 份崩溃日志。
func prune(dir string) {
	logs, _ := filepath.Glob(filepath.Join(dir, "crash-2*.log"))
	slices.SortFunc(logs, func(a, b string) int { return strings.Compare(b, a) }) // 名字里是时间，新的在前
	for _, p := range logs[min(len(logs), MaxCrashLogs):] {
		os.Remove(p)
	}
}
