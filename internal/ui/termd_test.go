package ui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/termd"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 终端后台进程（termd）的测试不起第二个进程：app.TermdStart 换成在测试进程里起一个 termd.Server，
// 套接字在状态目录（t.TempDir()）里，测试完 Stop 掉（挂断里面的 shell）。

// fakeTermd 记着测试里起过的 termd。
type fakeTermd struct {
	mu      sync.Mutex
	starts  int
	servers []*termd.Server
}

// inProcTermd 让应用要起 termd 时在测试进程里起一个。要在 termUI 之前调：它的 Cleanup 在关窗口之后跑。
func inProcTermd(t *testing.T) *fakeTermd {
	t.Helper()
	f := &fakeTermd{}
	old := app.TermdStart
	app.TermdStart = func(sock string) error {
		s := &termd.Server{Sock: sock, Idle: -1}
		if err := s.Listen(); err != nil {
			return err
		}
		go s.Serve()
		f.mu.Lock()
		f.starts++
		f.servers = append(f.servers, s)
		f.mu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		app.TermdStart = old
		f.stop()
	})
	return f
}

// stop 停掉起过的 termd（模拟 termd 退了、重新起过）。
func (f *fakeTermd) stop() {
	f.mu.Lock()
	ss := f.servers
	f.servers = nil
	f.mu.Unlock()
	for _, s := range ss {
		s.Stop()
		<-s.Done()
	}
}

func (f *fakeTermd) started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

// daemonState 是开着底栏、开了"关掉窗口后终端继续在后台运行"的状态目录。
func daemonState(t *testing.T) string {
	t.Helper()
	dir := termState(t, true)
	setDaemon(t, dir, true)
	return dir
}

func setDaemon(t *testing.T, dir string, on bool) {
	t.Helper()
	st, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Settings.TermDaemon = on
	if err := workspace.SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
}

// startedPane 画一帧（有键盘的那一格在这时才启动）、把键盘给它，等它建好终端（后台模式要先连上 termd）、出提示符。
func startedPane(t *testing.T, a *app.App, tt *wui.Tester, wait func(string, func() bool)) *app.TermPane {
	t.Helper()
	a.FocusTerm = true
	tt.Frame()
	_, _, p := a.ActivePane()
	if p == nil {
		t.Fatal("没有终端格")
	}
	wait("终端建好", func() bool { return p.Term != nil || p.Err != nil })
	if p.Err != nil {
		t.Fatal(p.Err)
	}
	testutil.WaitTermReady(t, tt, p.Term, 300*time.Millisecond, 15*time.Second)
	tt.Frame()
	return p
}

// sessions 是 termd 里现在的会话。
func sessions(t *testing.T, sock string) []termd.Info {
	t.Helper()
	infos, err := termd.List(sock)
	if err != nil {
		t.Fatalf("问 termd：%v", err)
	}
	return infos
}

// waitSessions 等 termd 里的会话满足 ok（断开、关掉是 termd 那边异步做的）。
func waitSessions(t *testing.T, sock, what string, ok func([]termd.Info) bool) []termd.Info {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		infos := sessions(t, sock)
		if ok(infos) {
			return infos
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 termd 里 %s 超时：%+v", what, infos)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 后台会话：开了设置，格里的 shell 跑在 termd 里（第一次先起它，界面上转圈"启动中…"）；关窗口只断开，
// 会话留在 termd 里、会话里记着 sid；再打开同一个工作区接回同一个 shell，之前的输出还在，不再起 termd；
// 关掉终端标签才关掉会话。
func TestDaemonPane(t *testing.T) {
	f := inProcTermd(t)
	_, ws := testutil.TempRepo(t)
	state := daemonState(t)
	sock := termd.SockPath(state)
	a, tt, wait := termUI(t, ws, state)
	// 第一帧就起了这一格；连 termd 的回调排在队列里，没跑之前一直是"启动中…"
	tt.Frame()
	if _, _, p := a.ActivePane(); p == nil || !p.Connecting() {
		t.Fatalf("第一帧之后应在连 termd：%+v", p)
	}
	mustText(t, tt, "启动中…")
	accessCheck(t, tt, "连 termd")
	r := a.Repos[0]
	wait("git status", idle(r))
	p := startedPane(t, a, tt, wait)
	if !p.Daemon() || f.started() != 1 || p.Sid == "" {
		t.Fatalf("没接到后台进程：daemon=%v 起了 %d 次 sid=%q", p.Daemon(), f.started(), p.Sid)
	}
	typeLine(t, tt, p, "echo 后台$((40+2))", "后台42")
	infos := sessions(t, sock)
	if len(infos) != 1 || infos[0].Sid != p.Sid || !infos[0].Attached || infos[0].Repo != r.Root || infos[0].WS != a.WsPath {
		t.Fatalf("termd 里的会话：%+v", infos)
	}
	sid, wsPath, root := p.Sid, a.WsPath, r.Root

	a.NoteTermDirs()
	a.SaveState()
	a.Close()
	waitSessions(t, sock, "会话断开", func(in []termd.Info) bool { return len(in) == 1 && !in[0].Attached && !in[0].Exited })
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	if s := st.Sessions[wsPath]; s == nil || s.Terms[root] == nil || len(s.Terms[root].Tabs) != 1 ||
		fmt.Sprint(s.Terms[root].Tabs[0].Sids) != fmt.Sprint([]string{sid}) {
		t.Fatalf("会话里没记 sid：%+v", s)
	}

	a, tt, wait = termUI(t, ws, state)
	r = a.Repos[0]
	wait("git status", idle(r))
	p = startedPane(t, a, tt, wait)
	testutil.WaitTerm(t, tt, p.Term, "后台42", 10*time.Second)
	if !p.Daemon() || p.Sid != sid || f.started() != 1 {
		t.Fatalf("没接回原来的会话：sid %q（原来 %q），起了 %d 次 termd", p.Sid, sid, f.started())
	}
	typeLine(t, tt, p, "echo 接回$((1+1))", "接回2")
	waitSessions(t, sock, "接回", func(in []termd.Info) bool { return len(in) == 1 && in[0].Attached })

	a.CloseTermTab(r, r.Terms[0])
	waitSessions(t, sock, "会话关掉", func(in []termd.Info) bool { return len(in) == 0 })
}

// shell 退出了的后台格：关窗口时关掉它的会话（不是断开留着），会话里也不记它的 sid，重开是新的 shell、不提示“会话不在了”。
func TestCloseKillsExited(t *testing.T) {
	inProcTermd(t)
	_, ws := testutil.TempRepo(t)
	state := daemonState(t)
	sock := termd.SockPath(state)
	a, tt, wait := termUI(t, ws, state)
	wait("git status", idle(a.Repos[0]))
	p := startedPane(t, a, tt, wait)
	sid := p.Sid
	tt.Type("exit 3")
	tt.Key(0, wui.KeyEnter)
	wait("shell 退出", func() bool { return p.Exited })
	if p.ExitCode != 3 || p.Lost() != "" {
		t.Fatalf("退出码 %d，lost %q", p.ExitCode, p.Lost())
	}
	waitSessions(t, sock, "接着的退出了的会话还在", func(in []termd.Info) bool { return len(in) == 1 && in[0].Exited && in[0].Attached })
	a.NoteTermDirs()
	a.SaveState()
	a.Close()
	// termd 的 Linger 是默认的 10 秒：3 秒内没了就是关掉的、不是断开的
	deadline := time.Now().Add(3 * time.Second)
	for len(sessions(t, sock)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("关窗口没关掉退出了的会话：%+v", sessions(t, sock))
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	if s := st.Sessions[a.WsPath]; s != nil && s.Terms != nil {
		for _, ts := range s.Terms {
			for _, tab := range ts.Tabs {
				if len(tab.Sids) > 0 {
					t.Fatalf("会话里记了退出了的 sid（原来 %s）：%+v", sid, tab)
				}
			}
		}
	}

	a, tt, wait = termUI(t, ws, state)
	wait("git status", idle(a.Repos[0]))
	p = startedPane(t, a, tt, wait)
	if p.Sid == sid || p.Exited {
		t.Fatalf("重开应是新的 shell：sid %q（原来 %q）exited=%v", p.Sid, sid, p.Exited)
	}
	if tt.HasText("上次的终端会话不在了，开了新的") {
		t.Fatal("退出过的格不该提示会话不在了")
	}
}

// termd 起不来：这次用普通终端，提示一下。
func TestDaemonFallback(t *testing.T) {
	old := app.TermdStart
	app.TermdStart = func(string) error { return errors.New("起不来") }
	t.Cleanup(func() { app.TermdStart = old })
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, daemonState(t))
	wait("git status", idle(a.Repos[0]))
	p := startedPane(t, a, tt, wait)
	if p.Daemon() {
		t.Fatal("termd 起不来还接上了")
	}
	mustText(t, tt, "终端后台进程没起来，这次用普通终端")
	typeLine(t, tt, p, "echo 本地$((2+3))", "本地5")
}

// 会话里记着的 sid 在 termd 里不在了（termd 退过）：开着设置就在新起的 termd 里开个新的，关了设置就用普通终端，
// 两种都提示一下；关了设置之后记着 sid 的格也不会去起 termd。
func TestDaemonLost(t *testing.T) {
	f := inProcTermd(t)
	_, ws := testutil.TempRepo(t)
	state := daemonState(t)
	sock := termd.SockPath(state)
	a, tt, wait := termUI(t, ws, state)
	wait("git status", idle(a.Repos[0]))
	p := startedPane(t, a, tt, wait)
	old := p.Sid
	a.SaveState()
	a.Close()
	f.stop()

	a, tt, wait = termUI(t, ws, state)
	wait("git status", idle(a.Repos[0]))
	p = startedPane(t, a, tt, wait)
	mustText(t, tt, "上次的终端会话不在了，开了新的")
	if !p.Daemon() || p.Sid == old || p.Sid == "" || f.started() != 2 {
		t.Fatalf("应在新起的 termd 里开新会话：daemon=%v sid %q（原来 %q），起了 %d 次", p.Daemon(), p.Sid, old, f.started())
	}
	a.SaveState()
	a.Close()
	f.stop()

	setDaemon(t, state, false)
	a, tt, wait = termUI(t, ws, state)
	wait("git status", idle(a.Repos[0]))
	p = startedPane(t, a, tt, wait)
	mustText(t, tt, "上次的终端会话不在了，开了新的")
	if p.Daemon() || f.started() != 2 {
		t.Fatalf("关了设置应用普通终端、不起 termd：daemon=%v 起了 %d 次", p.Daemon(), f.started())
	}
	if termd.Ping(sock) == nil {
		t.Fatal("关了设置还起了 termd")
	}
}

// 换工作区：后台的会话只断开，前台在跑命令也不问，命令照跑。
func TestLeaveSkipsTerms(t *testing.T) {
	inProcTermd(t)
	ra, rb := testutil.NewRepo(t, "a"), testutil.NewRepo(t, "b")
	for _, r := range []*testutil.Repo{ra, rb} {
		r.Write("README.md", "# 一\n")
		r.Commit("一")
	}
	wsA, wsB := namedWs(t, "甲", ra.Root), namedWs(t, "乙", rb.Root)
	state := daemonState(t)
	sock := termd.SockPath(state)
	a, tt, wait := termUI(t, wsA, state)
	wait("甲的状态", idle(a.Repos[0]))
	p := startedPane(t, a, tt, wait)
	tt.Type("sleep 30")
	tt.Key(0, wui.KeyEnter)
	wait("sleep 在前台", func() bool { return p.Job() == "sleep" })
	a.OpenWorkspace(wsB)
	if a.WsPath != wsB || a.Leave != nil {
		t.Fatalf("后台的终端在跑命令也不该问：%s %+v；texts=%q", a.WsPath, a.Leave, tt.Texts())
	}
	waitSessions(t, sock, "断开、命令照跑", func(in []termd.Info) bool {
		return len(in) == 1 && !in[0].Attached && in[0].Busy && in[0].WS == wsA
	})
}

// ⇧⌘C（键盘在终端里）把这一格的屏幕和回滚开成主区里一个只读标签：横幅写着几行、几点取的；不记进会话，关了 ⇧⌘T 也不回来；
// "复制全部"写剪贴板、提示几行；终端里又有了输出点"刷新"重新取；终端关了刷新变灰。
// 底栏标签的右键菜单和"终端"菜单里的"复制全部输出"复制有键盘的那一格。
func TestTermTextTab(t *testing.T) {
	var copied []string
	oldCopy := app.CopyText
	app.CopyText = func(s string) { copied = append(copied, s) }
	t.Cleanup(func() { app.CopyText = oldCopy })
	_, ws := testutil.TempRepo(t)
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	r := a.Repos[0]
	wait("git status", idle(r))
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "seq 1 30 | sed 's/^/行/'", "\n行30")

	// 终端里的局部快捷键排到下一帧前跑
	tt.Key(wui.Cmd|wui.Shift, wui.KeyC)
	wait("⇧⌘C", func() bool { return a.CurrentDoc() != nil })
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.TermText || d.Title != "终端 · demo" || !d.St.ReadOnly {
		t.Fatalf("⇧⌘C 没开成只读标签：%+v", d)
	}
	text := d.Buf.String()
	if !strings.Contains(text, "行1\n行2\n") || !strings.Contains(text, "行30") || strings.HasSuffix(text, "\n") {
		t.Fatalf("标签内容：%q", text)
	}
	if d.Lines != docs.CountLines(text) || d.Lines < 31 {
		t.Fatalf("行数 %d", d.Lines)
	}
	mustText(t, tt, fmt.Sprintf("终端输出的快照（%d 行，", d.Lines))
	mustText(t, tt, "只读 · 终端输出")
	accessCheck(t, tt, "终端输出标签")

	mustClick(t, tt, "复制全部")
	if len(copied) != 1 || copied[0] != text {
		t.Fatalf("复制全部：%q", copied)
	}
	mustText(t, tt, fmt.Sprintf("已复制 %d 行", d.Lines))

	// 再按一次是同一个标签
	n := len(a.Docs)
	a.FocusTerm = true
	tt.Frame()
	typeLine(t, tt, p, "echo 又一行", "\n又一行")
	tt.Key(wui.Cmd|wui.Shift, wui.KeyC)
	wait("再按 ⇧⌘C", func() bool { return strings.Contains(d.Buf.String(), "\n又一行") })
	if len(a.Docs) != n || a.CurrentDoc() != d || !strings.Contains(d.Buf.String(), "\n又一行") {
		t.Fatalf("同一格再按 ⇧⌘C 应刷新原来的标签：%d 个标签", len(a.Docs))
	}
	a.FocusTerm = true // ⇧⌘C 把键盘给了标签
	tt.Frame()
	typeLine(t, tt, p, "echo 第三次", "\n第三次")
	mustClick(t, tt, "刷新")
	if !strings.Contains(d.Buf.String(), "\n第三次") {
		t.Fatalf("刷新没取到新输出：%q", d.Buf.String())
	}

	// "终端"菜单里的复制全部输出：有键盘的那一格
	a.FocusTerm = true
	tt.Frame()
	a.Exec("term.copyAll")
	if len(copied) != 2 || !strings.Contains(copied[1], "第三次") {
		t.Fatalf("复制全部输出：%q", copied)
	}

	// 不记进会话
	a.SaveState()
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	if s := st.Sessions[a.WsPath]; s != nil && len(s.Tabs) > 0 {
		t.Fatalf("终端输出标签记进了会话：%+v", s.Tabs)
	}

	// 终端关了：刷新变灰，标签里留着关之前的
	a.CloseTermTab(r, r.Terms[0])
	tt.Frame()
	if a.CanRefreshTermText(d) {
		t.Fatal("终端关了还能刷新")
	}
	a.RefreshTermText(d)
	tt.Frame()
	mustText(t, tt, "这个终端已经关了，标签里是关之前的内容")
	if !strings.Contains(d.Buf.String(), "第三次") {
		t.Fatal("关了终端标签里的内容没了")
	}

	// 关了 ⇧⌘T 不回来
	a.CloseDoc(d)
	a.ReopenClosed()
	for _, x := range a.Docs {
		if x.Kind == docs.TermText {
			t.Fatal("⇧⌘T 把终端输出标签开回来了")
		}
	}
}

// 底栏终端标签的右键菜单：把终端内容开成标签、复制全部输出。
func TestTermTabMenuText(t *testing.T) {
	var copied []string
	oldCopy := app.CopyText
	app.CopyText = func(s string) { copied = append(copied, s) }
	t.Cleanup(func() { app.CopyText = oldCopy })
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	wait("git status", idle(a.Repos[0]))
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "echo 菜单$((3*3))", "菜单9")
	mustRightClick(t, tt, "终端 · demo", "复制全部输出")
	if len(copied) != 1 || !strings.Contains(copied[0], "菜单9") {
		t.Fatalf("右键复制全部输出：%q", copied)
	}
	mustRightClick(t, tt, "终端 · demo", "把终端内容开成标签")
	if d := a.CurrentDoc(); d == nil || d.Kind != docs.TermText || !strings.Contains(d.Buf.String(), "菜单9") {
		t.Fatalf("右键开成标签：%+v", d)
	}
}

// 终端里查找：没有匹配的写红色的"没有结果"，有的写"第几个 / 共几个"。
func TestFindCount(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	wait("git status", idle(a.Repos[0]))
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "echo 甲乙 甲乙", "\n甲乙 甲乙")
	tt.Key(wui.Cmd, wui.KeyF)
	tt.Frame()
	tt.Type("没有这个词")
	wait("没有结果", func() bool { return tt.HasText("没有结果") })
	for range len([]rune("没有这个词")) {
		tt.Key(0, wui.KeyBackspace)
	}
	tt.Type("甲乙")
	wait("计数", func() bool { return tt.HasText(" / ") && !tt.HasText("没有结果") })
}

// WORKBENCH_TERM_CMD：第一个仓库的第一个终端出了提示符就敲进去回车。
func TestTermCmdEnv(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, _, wait := termUI(t, ws, termState(t, true))
	wait("git status", idle(a.Repos[0]))
	a.TypeInTerminal("echo 调试$((6*7))")
	_, _, p := a.ActivePane()
	wait("终端建好", func() bool { return p != nil && p.Term != nil })
	wait("敲进去", func() bool { return strings.Contains(p.Term.Text(), "\n调试42") })
}

// 设置页终端那几个开关：⌥ 作为 Meta 键、选中即复制、关掉窗口后终端继续在后台运行，都存进 state.json；
// 选中即复制马上交给开着的终端；后台那一行没在运行。
func TestSettingsTerminalDaemon(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	_, ws := testutil.TempRepo(t)
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	wait("git status", idle(a.Repos[0]))
	readyPane(t, a, tt)
	st := wui.NewTester(SettingsView(a), 760, 1000)
	st.SetLocale(i18n.Locale())
	st.Frame()
	mustClick(t, st, "终端")
	saved := func() workspace.Settings {
		t.Helper()
		s, err := workspace.LoadState(state)
		if err != nil {
			t.Fatal(err)
		}
		return s.Settings
	}
	mustText(t, st, "没在运行")
	accessCheck(t, st, "设置 · 终端")
	for _, name := range []string{"⌥ 作为 Meta 键", "选中即复制", "关掉窗口后终端继续在后台运行"} {
		mustClick(t, st, name)
	}
	s := saved()
	if !s.TermOptionAsAlt || !s.TermCopyOnSelect || !s.TermDaemon || !reflect.DeepEqual(a.St.Settings, s) {
		t.Fatalf("开关没存上：%+v", s)
	}
	tt.Frame()
	mustText(t, tt, "已经开着的终端重新启动后才会在后台运行")
	mustClick(t, st, "关掉窗口后终端继续在后台运行")
	if saved().TermDaemon {
		t.Fatal("关掉后台运行没存上")
	}
}
