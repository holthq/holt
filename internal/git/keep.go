package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/holthq/holt/internal/i18n"
)

// Keep 是一条写命令的超时钟：默认 opTimeout 到点就结束命令（和以前一样）；
// Touch 重新算（克隆每收到一段进度），Hold 停住、Release 接着重新算（凭据框开着时等用户输入的时间不算）。
// 可以从任何 goroutine 调；命令还没开始或已经结束时调也没事。
type Keep struct {
	mu    sync.Mutex
	timer *time.Timer
	d     time.Duration
	held  int
}

// start 开始计时：d 之后（期间没有 Touch、没有 Hold 着）调 fire。
func (k *Keep) start(d time.Duration, fire func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.d = d
	k.timer = time.AfterFunc(d, fire)
	if k.held > 0 {
		k.timer.Stop()
	}
}

// stop 是命令结束了。
func (k *Keep) stop() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
}

// Touch 重新算：再过一整个 opTimeout 才超时（Hold 着时不动）。
func (k *Keep) Touch() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.timer != nil && k.held == 0 {
		k.timer.Reset(k.d)
	}
}

// Hold 停住超时钟，直到配对的 Release。
func (k *Keep) Hold() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.held++
	if k.timer != nil {
		k.timer.Stop()
	}
}

// Release 解除一次 Hold；都解除了就重新算一整个 opTimeout。
func (k *Keep) Release() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.held > 0 {
		k.held--
	}
	if k.timer != nil && k.held == 0 {
		k.timer.Reset(k.d)
	}
}

// maxStreamLines 是 RunStream 留着的输出最后多少行（出错时给人看的）。
const maxStreamLines = 200

// RunStream 跑一条长的 git 写命令（克隆），在 dir 里、环境同 RunOpEnv。stderr 一段一段交给 onChunk（按 \r 和 \n 切，
// 空的不给；在读的 goroutine 上调）；每段都 Touch 一下 keep（nil 就自己建一个），所以 opTimeout 没有新的输出才停，
// 返回"2 分钟没有进展，已停止"。取消同 RunOp（ErrCanceled）。返回的输出是 stdout 加上 stderr 的最后 200 行，
// 用 \r 刷新的进度只留最后一次。
func RunStream(ctx context.Context, dir string, env []string, keep *Keep, onChunk func(string), args ...string) (string, error) {
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	if keep == nil {
		keep = &Keep{}
	}
	keep.start(opTimeout, func() {
		stalled.Store(true)
		cancel()
	})
	defer keep.stop()
	cmd, reap := newOp(tctx, dir, env, args)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	pipe, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	lines := readChunks(pipe, func(s string) {
		keep.Touch()
		if onChunk != nil {
			onChunk(s)
		}
	})
	err = cmd.Wait()
	reap()
	text := strings.TrimRight(strings.TrimRight(stdout.String(), "\n")+"\n"+strings.Join(lines, "\n"), "\n")
	text = strings.TrimLeft(text, "\n")
	switch {
	case ctx.Err() != nil:
		return text, ErrCanceled
	case stalled.Load():
		return text, errors.New(i18n.T("2 分钟没有进展，已停止"))
	case err != nil:
		if text == "" {
			text = err.Error()
		}
		return text, errors.New(text)
	}
	return text, nil
}

// readChunks 读到 EOF：按 \r、\n 切段交给 fn，返回最后 maxStreamLines 行（\r 结尾的那段会被下一段盖掉，像终端里那样）。
func readChunks(r io.Reader, fn func(string)) []string {
	var lines []string
	over := false // 上一段是 \r 结尾的：下一段盖掉它
	br := bufio.NewReader(r)
	var cur []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			break
		}
		if b != '\r' && b != '\n' {
			cur = append(cur, b)
			continue
		}
		s := strings.TrimSpace(string(cur))
		cur = cur[:0]
		if s == "" {
			continue
		}
		fn(s)
		if over && len(lines) > 0 {
			lines[len(lines)-1] = s
		} else {
			lines = append(lines, s)
		}
		over = b == '\r'
		if len(lines) > maxStreamLines {
			lines = lines[len(lines)-maxStreamLines:]
		}
	}
	if s := strings.TrimSpace(string(cur)); s != "" {
		fn(s)
		if over && len(lines) > 0 {
			lines[len(lines)-1] = s
		} else {
			lines = append(lines, s)
		}
	}
	return lines
}
