package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/testutil"
)

// 常见失败认得出（用真的 git / ssh 原文），认不出的返回空。
func TestExplain(t *testing.T) {
	cases := []struct{ out, want string }{
		{"To github.com:o/r.git\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs to 'github.com:o/r.git'", "先拉取"},
		{" ! [rejected]        main -> main (non-fast-forward)", "先拉取"},
		{"remote: Invalid username or token. Password authentication is not supported for Git operations.\nfatal: Authentication failed for 'https://github.com/o/r.git/'", "访问令牌"},
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", "访问令牌"},
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", "SSH 密钥"},
		{"Host key verification failed.\nfatal: Could not read from remote repository.", "指纹"},
		{"remote: Repository not found.\nfatal: repository 'https://github.com/o/nope.git/' not found", "不存在"},
		{"fatal: '/tmp/x' does not appear to be a git repository\nfatal: Could not read from remote repository.", "不存在"},
		{"ssh: Could not resolve hostname githb.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.", "连不上"},
		{"fatal: unable to access 'https://github.com/o/r.git/': Could not resolve host: github.com", "连不上"},
		{"fatal: destination path 'r' already exists and is not an empty directory.", "不是空的"},
		{"fatal: Unable to create '/x/.git/index.lock': File exists.", "锁文件"},
		{"error: Your local changes to the following files would be overwritten by merge:\n\ta.txt", "会被覆盖"},
		{"Auto-merging a.txt\nCONFLICT (content): Merge conflict in a.txt", "有冲突"},
		{"fatal: not something we can merge", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := Explain(c.out)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%q → %q，应含 %q", c.out, got, c.want)
		}
	}
}

// slowAlias 是一个 git 别名，跑 dir 里的脚本 body（sh）。
func slowAlias(t *testing.T, body string) []string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"-c", "alias.slow=!" + script, "slow"}
}

// shortTimeout 把 opTimeout 改成 d，测试完改回来。
func shortTimeout(t *testing.T, d time.Duration) {
	old := opTimeout
	opTimeout = d
	t.Cleanup(func() { opTimeout = old })
}

// 超时钟：没有 Keep 时到点就停（返回超时的错误）；Hold 着的时候不算，Release 后重新算一整段。
func TestOpTimeoutKeep(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	shortTimeout(t, 400*time.Millisecond)
	ctx := context.Background()

	_, err := RunOpKeep(ctx, root, "", nil, nil, slowAlias(t, "sleep 3")...)
	if err == nil || !strings.Contains(err.Error(), "没结束，已终止") {
		t.Fatalf("没 Hold：%v，应超时", err)
	}

	k := &Keep{}
	k.Hold() // 开始前就 Hold：像凭据框一开始就弹出来
	go func() {
		time.Sleep(testutil.Budget(900 * time.Millisecond))
		k.Release()
	}()
	if out, err := RunOpKeep(ctx, root, "", nil, k, slowAlias(t, "sleep 1; echo done")...); err != nil || out != "done" {
		t.Fatalf("Hold 着：%q %v，应跑完", out, err)
	}

	// Touch 让它多活：每 150 ms 碰一下，跑 1 s 的命令不会在 400 ms 时被停
	k2 := &Keep{}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(150 * time.Millisecond):
				k2.Touch()
			}
		}
	}()
	out, err := RunOpKeep(ctx, root, "", nil, k2, slowAlias(t, "sleep 1; echo done")...)
	close(stop)
	if err != nil || out != "done" {
		t.Fatalf("Touch 着：%q %v，应跑完", out, err)
	}

	var nilKeep *Keep // 应用里没有凭据框的命令传 nil，Touch / Hold / Release 都得能调
	nilKeep.Touch()
	nilKeep.Hold()
	nilKeep.Release()
}

// RunStream：进度一段一段交出来（\r 切开），一直有进度就不超时，输出里 \r 刷新的只留最后一次；
// 一段时间没有新进度就停，返回没有进展的错误；取消返回 ErrCanceled。
func TestRunStream(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	shortTimeout(t, 400*time.Millisecond)
	ctx := context.Background()

	body := "echo out\nfor i in 10 20 30 40 50 60 70 80 90 100; do printf 'Receiving objects: %s%%\\r' $i >&2; sleep 0.1; done\necho 'Receiving objects: 100%, done.' >&2"
	var mu sync.Mutex
	var chunks []string
	out, err := RunStream(ctx, root, nil, nil, func(s string) {
		mu.Lock()
		chunks = append(chunks, s)
		mu.Unlock()
	}, slowAlias(t, body)...)
	if err != nil {
		t.Fatalf("有进度：%v", err)
	}
	if len(chunks) != 11 || chunks[0] != "Receiving objects: 10%" || chunks[10] != "Receiving objects: 100%, done." {
		t.Fatalf("进度段 %q", chunks)
	}
	if out != "out\nReceiving objects: 100%, done." {
		t.Fatalf("输出 %q", out)
	}

	_, err = RunStream(ctx, root, nil, nil, nil, slowAlias(t, "echo start >&2; sleep 3")...)
	if err == nil || !strings.Contains(err.Error(), "没有进展") {
		t.Fatalf("没进度：%v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	var once sync.Once
	go func() {
		<-started
		cancel()
	}()
	_, err = RunStream(cctx, root, nil, nil, func(string) { once.Do(func() { close(started) }) }, slowAlias(t, "echo start >&2; sleep 3")...)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("取消：%v", err)
	}

	_, err = RunStream(ctx, root, nil, nil, nil, slowAlias(t, "echo 'fatal: nope' >&2; exit 128")...)
	if err == nil || err.Error() != "fatal: nope" {
		t.Fatalf("失败：%v", err)
	}
}

// readChunks 按 \r、\n 切，空段不要；\r 结尾的那段被下一段盖掉；只留最后 maxStreamLines 行。
func TestReadChunks(t *testing.T) {
	var got []string
	lines := readChunks(strings.NewReader("a 1%\ra 2%\ra done\nline2\n\n  \nlast"), func(s string) { got = append(got, s) })
	if want := []string{"a 1%", "a 2%", "a done", "line2", "last"}; !slices.Equal(got, want) {
		t.Errorf("段 %q，应为 %q", got, want)
	}
	if want := []string{"a done", "line2", "last"}; !slices.Equal(lines, want) {
		t.Errorf("行 %q，应为 %q", lines, want)
	}
	var b strings.Builder
	for i := range maxStreamLines + 50 {
		fmt.Fprintf(&b, "l%d\n", i)
	}
	lines = readChunks(strings.NewReader(b.String()), func(string) {})
	if len(lines) != maxStreamLines || lines[0] != "l50" {
		t.Errorf("留了 %d 行，第一行 %q", len(lines), lines[0])
	}
}
