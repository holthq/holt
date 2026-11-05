package app

import (
	"io"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// fakeConn 是不接 shell 的终端连接：读一直等到关，写的扔掉。
type fakeConn struct{ r *io.PipeReader }

func (f fakeConn) Read(p []byte) (int, error)  { return f.r.Read(p) }
func (f fakeConn) Write(p []byte) (int, error) { return len(p), nil }
func (f fakeConn) Close() error                { return f.r.Close() }

// fakeTerm 给一格装上一个不起 shell 的终端（选项照常是 paneOptions 的：响铃、通知的回调都在），假装认领到进程号 pid。
func fakeTerm(t *testing.T, a *App, r *Repo, p *TermPane, pid int) {
	t.Helper()
	p.gen++
	opts := a.paneOptions(r, p, p.gen, r.Root)
	pr, pw := io.Pipe()
	opts.Conn, opts.Command, opts.Dir, opts.Env = fakeConn{pr}, nil, "", nil
	term, err := terminal.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pw.Close()
		term.Close()
	})
	p.Term, p.pid = term, pid
}

// 终端在工作树 B 的目录里响铃（BEL / OSC 9 / OSC 777）→ B 上是"等你"，主工作树上没有；用户正看着的那一格响铃不算；
// 切进那一格就不再等你。前台命令在名单里（默认的、设置里加的）时按最近 10 秒有没有输出分运行中 / 空闲，等你压过运行中。
// 钟和前台命令都是换的，不起 shell 也不起 agent。
func TestWtAgent(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	a.agents = &agentPoller{quit: true} // 后台轮询不起：前台命令由测试给
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读完状态", idle(r))
	clock := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	a.clock = func() time.Time { return clock }

	wtB := t.TempDir()
	main := Worktree{Worktree: git.Worktree{Path: r.Root, Main: true}}
	b := Worktree{Worktree: git.Worktree{Path: wtB}}

	_, tab1, p1 := a.ActivePane()
	a.NewTerminal(r, "")
	_, tab2, p2 := a.ActivePane()
	if tab1 == tab2 || p1 == p2 {
		t.Fatal("没开出第二个终端标签")
	}
	fakeTerm(t, a, r, p1, 900001)
	fakeTerm(t, a, r, p2, 900002)
	a.TermFocused = true // 键盘在第二个标签（p2）

	feed := func(p *TermPane, s string) {
		p.Term.Feed([]byte(s))
		wait("回调跑完", func() bool { return true })
	}
	feed(p1, "\x1b]7;file://localhost"+wtB+"\x07") // p1 cd 到了 B（OSC 7 的 BEL 是结束符，不算响铃）
	if s := a.WtAgent(r, b); s.State != AgentNone {
		t.Fatalf("OSC 7 的结束符当成了响铃：%+v", s)
	}
	for _, seq := range []string{"\a", "\x1b]9;等你\x07", "\x1b]777;notify;Claude;等你\x07"} {
		p1.bellAt = time.Time{}
		feed(p1, seq)
		s := a.WtAgent(r, b)
		if s.State != AgentWaiting || s.Pane != p1 || s.Tab != tab1 || s.Repo != r || !s.WaitSince.Equal(clock) {
			t.Fatalf("%q 之后 B：%+v", seq, s)
		}
		if m := a.WtAgent(r, main); m.State != AgentNone {
			t.Fatalf("%q 之后主工作树：%+v", seq, m)
		}
	}
	if a.TabAgent(tab1) != AgentWaiting || a.TabAgent(tab2) != AgentNone {
		t.Fatalf("标签：%v %v", a.TabAgent(tab1), a.TabAgent(tab2))
	}
	if s := a.WtAgent(r, b); s.Waited(clock.Add(time.Minute)) != time.Minute {
		t.Fatalf("等了 %v", s.Waited(clock.Add(time.Minute)))
	}

	feed(p2, "\a") // 正看着的那一格响铃不算
	if a.PaneAgent(p2) != AgentNone {
		t.Fatal("正看着的格响铃记成了等你")
	}

	a.GoToPane(r, tab1, p1) // 跳到那个终端：切过去，等你去掉
	if r.TermSel != 0 || a.CurTermTab(r) != tab1 || a.PaneAgent(p1) != AgentNone {
		t.Fatalf("跳过去之后 TermSel=%d，状态 %v", r.TermSel, a.PaneAgent(p1))
	}
	feed(p1, "\a")
	if a.PaneAgent(p1) != AgentNone {
		t.Fatal("跳过去之后用户正看着它，响铃不该算")
	}
	a.SelectTermTab(r, 1) // 换到 p2，p1 再响
	feed(p1, "\a")
	if a.WtAgent(r, b).State != AgentWaiting {
		t.Fatal("换走之后响铃没记上")
	}
	a.FocusPane(r, p1) // 用户点进 p1
	if s := a.WtAgent(r, b); s.State != AgentNone {
		t.Fatalf("聚焦之后还是 %+v", s)
	}

	// 运行中 / 空闲：前台是 claude，有输出的 10 秒内运行中，之后空闲
	note := func(p *TermPane, job string, run bool) {
		a.noteAgents([]agentJob{{p: p, pid: p.pid, job: job, run: run}})
	}
	p1.paneOutput(clock)
	note(p1, "claude", true)
	if s := a.WtAgent(r, b); s.State != AgentRunning || s.Pane != p1 || !s.RunSince.Equal(clock) {
		t.Fatalf("claude 在输出：%+v", s)
	}
	clock = clock.Add(9 * time.Second)
	if a.PaneAgent(p1) != AgentRunning {
		t.Fatal("9 秒还该是运行中")
	}
	clock = clock.Add(2 * time.Second)
	if s := a.WtAgent(r, b); s.State != AgentIdle {
		t.Fatalf("11 秒没输出：%+v", s)
	}
	// 不在名单里的不算；设置里加上就算
	note(p1, "goose", false)
	if a.WtAgent(r, b).State != AgentNone {
		t.Fatal("goose 不在名单里")
	}
	a.St.Settings.AgentNames = "amp, goose"
	if a.WtAgent(r, b).State != AgentIdle {
		t.Fatal("设置里加了 goose")
	}
	// 一个工作树几个终端：等你 > 运行中 > 空闲
	feed(p2, "\x1b]7;file://localhost"+wtB+"\x07")
	p2.paneOutput(clock)
	note(p2, "codex", true)
	if s := a.WtAgent(r, b); s.State != AgentRunning || s.Pane != p2 {
		t.Fatalf("运行中该压过空闲：%+v", s)
	}
	a.TermFocused = false
	feed(p1, "\a")
	if s := a.WtAgent(r, b); s.State != AgentWaiting || s.Pane != p1 {
		t.Fatalf("等你该压过运行中：%+v", s)
	}
}

// 前台命令的名字：路径去掉、登录 shell 的 - 去掉；node / bun / python 跑的脚本按脚本名。
func TestJobName(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"/opt/homebrew/bin/claude"}, "claude"},
		{[]string{"-zsh"}, "zsh"},
		{[]string{"node", "/usr/local/bin/gemini"}, "gemini"},
		{[]string{"node", "--no-warnings", "/x/opencode.js", "run"}, "opencode"},
		{[]string{"python3", "-m"}, "python3"},
		{nil, "?"},
	} {
		if got := jobName(c.args); got != c.want {
			t.Errorf("%q → %q，要 %q", c.args, got, c.want)
		}
	}
}
