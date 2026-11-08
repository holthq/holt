package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/holthq/holt/internal/i18n"
)

// RunState 是底栏输出里跑着的那一条命令（和终端分开，一次一条）：命令面板 "$ 命令" 跑的命令。
type RunState struct {
	Running bool
	Repo    *Repo
	Cmd     string
	Started time.Time
	cancel  context.CancelFunc
}

// runOutputMax 是一条命令最多留多少输出：再长的只留前面这么多（编译错误一般在前面的几行）。
const runOutputMax = 512 << 10

// RunShell 在 r 的根目录、login shell 里跑 cmd；输出、退出码和解析出来的编译错误在跑完后进底栏：
// 输出记进"输出"，`路径:行:列: 信息` 这样的行进问题面板（和检查报的合在一起）。
// 只在用户从命令面板选了 "$ 运行命令" 之后跑，同一时间只跑一条。
func (a *App) RunShell(r *Repo, cmd string) {
	cmd = strings.TrimSpace(cmd)
	if r == nil || r.gone() || cmd == "" {
		return
	}
	if a.Run.Running {
		a.Toast = i18n.T("已经有一条命令在跑，先停掉它")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.Run = RunState{Running: true, Repo: r, Cmd: cmd, Started: time.Now(), cancel: cancel}
	up := a.Update
	shell := a.shell()
	go func() {
		var buf limitedBuffer
		buf.max = runOutputMax
		c := exec.CommandContext(ctx, shell, "-lc", cmd)
		c.Dir = r.Root
		c.Env = os.Environ()
		c.Stdout, c.Stderr = &buf, &buf
		err := c.Run()
		code, stopped := 0, ctx.Err() != nil
		if err != nil {
			code = -1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		out, truncated := buf.String(), buf.truncated
		up(func() { a.finishRun(r, cmd, out, truncated, code, stopped) })
	}()
}

// StopRun 停掉正在跑的那条命令（SIGKILL，同 exec.CommandContext 的取消）。
func (a *App) StopRun() {
	if !a.Run.Running || a.Run.cancel == nil {
		return
	}
	a.Run.cancel()
	a.Toast = i18n.T("正在停止…")
}

// RunRunning 报有没有命令在跑。
func (a *App) RunRunning() bool { return a.Run.Running }

// finishRun 是命令跑完之后的回话（主线程上）：输出进底栏，失败时把编译错误解析进问题面板。
func (a *App) finishRun(r *Repo, cmd, out string, truncated bool, code int, stopped bool) {
	a.Run = RunState{}
	text := strings.TrimRight(out, "\n")
	if truncated {
		if text != "" {
			text += "\n"
		}
		text += i18n.Tf("（输出太长，只留了前 %d KB）", runOutputMax>>10)
	}
	title := "$ " + cmd
	if stopped {
		a.Log(r, title, text, false)
		a.Toast = i18n.T("已停止")
		return
	}
	ps := ParseProblems(out, r.Root)
	a.setProblems(lintKey{root: r.Root, lang: "build", scope: scopeRepo}, ps, lintSnap{})
	if code != 0 {
		a.Log(r, title, text, true)
		if len(ps) > 0 {
			a.Toast = i18n.Tn("运行失败：退出码 %d，%d 个问题", code, len(ps))
		} else {
			a.Toast = i18n.Tf("运行失败：退出码 %d", code)
		}
		return
	}
	a.Log(r, title, text, false)
	a.Toast = i18n.T("运行完成")
}

// limitedBuffer 是最多留 max 字节的输出（后面来的丢掉，记下 truncated）。
type limitedBuffer struct {
	b         bytes.Buffer
	max       int
	truncated bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.max <= 0 {
		w.max = runOutputMax
	}
	room := w.max - w.b.Len()
	if room <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		w.b.Write(p[:room])
		w.truncated = true
		return len(p), nil
	}
	return w.b.Write(p)
}

func (w *limitedBuffer) String() string { return w.b.String() }
