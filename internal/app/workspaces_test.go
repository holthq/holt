package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// wsFile 在新的临时目录里写一个叫 name.code-workspace 的工作区文件。
func wsFile(t *testing.T, name string, roots ...string) string {
	t.Helper()
	folders := make([]string, len(roots))
	for i, r := range roots {
		folders[i] = `{"path":"` + r + `"}`
	}
	p := filepath.Join(t.TempDir(), name+".code-workspace")
	if err := os.WriteFile(p, []byte(`{"folders":[`+strings.Join(folders, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func realDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func repoRoots(a *App) []string {
	var roots []string
	for _, r := range a.Repos {
		roots = append(roots, r.Root)
	}
	return roots
}

// 换工作区：会话（标签、加进来的仓库）按工作区记、换回来放回去；开着的设置窗口留着；最近的工作区；文件夹当工作区；打不开的报错、不动现在的。
func TestSwitchWorkspace(t *testing.T) {
	ra, rb, rx := testutil.NewRepo(t, "a"), testutil.NewRepo(t, "b"), testutil.NewRepo(t, "x")
	for _, r := range []*testutil.Repo{ra, rb, rx} {
		r.Write("README.md", r.Root+"\n")
		r.Commit("一")
	}
	wsA, wsB := wsFile(t, "甲", ra.Root), wsFile(t, "乙", rb.Root)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, wsA, stateDir)
	a.Start()
	wait("甲的状态", func() bool { return idle(a.Repos[0])() })
	if err := a.AddRepoDir(rx.Root); err != nil {
		t.Fatal(err)
	}
	xr := realDir(t, rx.Root) // 加进来的仓库记的是 git 报的根（解开符号链接）
	a.OpenFile(a.Repos[0], "README.md", true)
	wait("README 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	if !slices.Equal(a.St.RecentWs, []string{wsA}) || a.RecentWorkspaces() != nil {
		t.Fatalf("最近的工作区：%v / %v", a.St.RecentWs, a.RecentWorkspaces())
	}

	// 没有要问的：直接换。设置窗口换工作区不关，导出 / 导入的面板还要挂在它上面（只比指针）
	settingsWin := new(mygo.Window)
	a.SettingsWin = settingsWin
	a.OpenWorkspace(wsB)
	if a.SettingsWin != settingsWin {
		t.Errorf("换工作区丢了设置窗口：%p", a.SettingsWin)
	}
	if a.Leave != nil || a.WsPath != wsB || a.WsName() != "乙" || !slices.Equal(repoRoots(a), []string{rb.Root}) || len(a.Docs) != 0 {
		t.Fatalf("换到乙：leave=%+v ws=%s repos=%v docs=%d", a.Leave, a.WsPath, repoRoots(a), len(a.Docs))
	}
	if a.Toast != "已打开工作区 乙" || len(a.St.Added) != 0 {
		t.Errorf("toast=%q added=%v", a.Toast, a.St.Added)
	}
	if !slices.Equal(a.St.RecentWs, []string{wsB, wsA}) || !slices.Equal(a.RecentWorkspaces(), []string{wsA}) {
		t.Errorf("最近的工作区：%v / %v", a.St.RecentWs, a.RecentWorkspaces())
	}
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if s := st.Sessions[wsA]; len(st.Added) != 0 || s == nil || !slices.Equal(s.Added, []string{xr}) || len(s.Tabs) != 1 {
		t.Errorf("甲的会话没按工作区存：顶层 added=%v 会话=%+v", st.Added, s)
	}
	wait("乙的状态", func() bool { return idle(a.Repos[0])() })

	// 换回甲：加进来的仓库和标签都回来
	a.OpenWorkspace(wsA)
	if !slices.Equal(repoRoots(a), []string{ra.Root, xr}) || !slices.Equal(a.St.Added, []string{xr}) {
		t.Fatalf("换回甲：repos=%v added=%v", repoRoots(a), a.St.Added)
	}
	wait("甲的标签", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	if d := a.CurrentDoc(); d.Abs != filepath.Join(ra.Root, "README.md") {
		t.Errorf("标签：%s", d.Abs)
	}
	a.OpenWorkspace(wsA)
	if a.Toast != "已经开着工作区 甲" {
		t.Errorf("再开同一个：%q", a.Toast)
	}

	// 文件夹：是仓库就是它自己；不是就找下面一层
	wait("甲的状态", func() bool { return idle(a.Repos[0])() && idle(a.Repos[1])() })
	a.OpenWorkspace(rb.Root)
	if a.WsName() != "b" || !slices.Equal(repoRoots(a), []string{realDir(t, rb.Root)}) {
		t.Errorf("仓库目录：%s %v", a.WsName(), repoRoots(a))
	}
	wait("b 的状态", func() bool { return idle(a.Repos[0])() })
	parent := filepath.Dir(rx.Root)
	os.MkdirAll(filepath.Join(parent, ".hidden", ".git"), 0o755)
	a.OpenWorkspace(parent)
	if !slices.Equal(repoRoots(a), []string{rx.Root}) {
		t.Errorf("下面一层的仓库：%v", repoRoots(a))
	}
	wait("x 的状态", func() bool { return idle(a.Repos[0])() })

	// 不是仓库、下面一层也没有仓库：当普通文件夹打开
	empty := t.TempDir()
	a.St.RecentWs = append(a.St.RecentWs, filepath.Join(empty, "没有了.code-workspace"))
	if slices.Contains(a.RecentWorkspaces(), filepath.Join(empty, "没有了.code-workspace")) {
		t.Error("磁盘上没有的也列出来了")
	}
	a.OpenWorkspace(empty)
	if a.AlertOpen || a.WsPath != empty || !slices.Equal(repoRoots(a), []string{empty}) {
		t.Fatalf("普通文件夹：alert=%v %q ws=%s repos=%v", a.AlertOpen, a.AlertMsg, a.WsPath, repoRoots(a))
	}
	wait("普通文件夹的状态", func() bool { return idle(a.Repos[0])() })
	if !a.Repos[0].Plain || a.Repos[0].Err != nil {
		t.Errorf("普通文件夹：plain=%v err=%v", a.Repos[0].Plain, a.Repos[0].Err)
	}

	// 打不开的：报错，还在原来的工作区；最近的里面有它就去掉
	gone := filepath.Join(empty, "没有了")
	a.St.RecentWs = append(a.St.RecentWs, gone)
	a.OpenWorkspace(gone)
	if !a.AlertOpen || a.WsPath != empty || slices.Contains(a.St.RecentWs, gone) {
		t.Errorf("没有的目录：alert=%v %q ws=%s recent=%v", a.AlertOpen, a.AlertMsg, a.WsPath, a.St.RecentWs)
	}
	a.AlertOpen = false

	a.ClearRecentWs()
	if len(a.St.RecentWs) != 0 || a.RecentWorkspaces() != nil {
		t.Errorf("清除后：%v", a.St.RecentWs)
	}
	if st, _ := workspace.LoadState(stateDir); len(st.RecentWs) != 0 {
		t.Errorf("清除后 state.json 里还有：%v", st.RecentWs)
	}

	// 没给工作区：开最近的
	a.OpenWorkspace(wsB)
	b, err := New("", stateDir)
	if err != nil || b.WsPath != wsB {
		t.Errorf("没给工作区应开最近的乙：%v %v", b, err)
	}
	b.Close()
}

// 老版本的 state.json：加进来、移除的仓库在顶层，不分工作区。归给第一个打开的工作区，存的时候挪进它的会话。
func TestLegacyAddedHidden(t *testing.T) {
	ra, rb, rx := testutil.NewRepo(t, "a"), testutil.NewRepo(t, "b"), testutil.NewRepo(t, "x")
	ws := wsFile(t, "甲", ra.Root, rb.Root)
	stateDir := t.TempDir()
	old := `{"version":3,"added":["` + rx.Root + `"],"hidden":["` + rb.Root + `"],"sessions":{"` + ws + `":{"active":-1,"repo":"` + ra.Root + `"}}}`
	if err := os.WriteFile(filepath.Join(stateDir, workspace.StateFile), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := newTestAppIn(t, ws, stateDir)
	if !slices.Equal(repoRoots(a), []string{ra.Root, rx.Root}) {
		t.Fatalf("旧的 added / hidden 没用上：%v", repoRoots(a))
	}
	a.SaveState()
	st, _ := workspace.LoadState(stateDir)
	if s := st.Sessions[ws]; len(st.Added)+len(st.Hidden) != 0 || !slices.Equal(s.Added, []string{rx.Root}) || !slices.Equal(s.Hidden, []string{rb.Root}) {
		t.Errorf("没挪进会话：顶层 %v %v，会话 %+v", st.Added, st.Hidden, s)
	}
}

// 退出和换工作区前问：没保存的文件（取消 / 不保存 / 全部保存）、在跑的 git 写命令（中断）。
func TestLeave(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	rb := testutil.NewRepo(t, "b")
	rb.Write("b.txt", "b\n")
	rb.Commit("b")
	wsB := wsFile(t, "乙", rb.Root)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.OpenFile(r, "a.txt", true)
	wait("a.txt 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	d := a.CurrentDoc()
	d.Buf.Set("改了\n")

	// 退出：取消
	a.RequestLeave("")
	title, msg, buttons := a.LeaveQuestion()
	if a.Leave == nil || !a.Leave.Open || a.Leave.Step != LeaveDirty || title != "有文件没保存" || msg != "a.txt还没保存，退出前要保存吗？" ||
		!slices.Equal(buttons, []string{"取消", "不保存退出", "全部保存并退出"}) {
		t.Fatalf("没保存的文件：%+v %q %q %q", a.Leave, title, msg, buttons)
	}
	a.LeaveAnswer(0)
	if a.Leave != nil || a.ForceClose {
		t.Fatalf("取消了还要退出：%+v %v", a.Leave, a.ForceClose)
	}

	// 换工作区：先问文件；全部保存了再问在跑的命令；中断了等它结束再换
	slowOp(a, r)
	a.RequestLeave(wsB)
	if _, _, buttons := a.LeaveQuestion(); !slices.Equal(buttons, []string{"取消", "不保存并切换", "全部保存并切换"}) {
		t.Fatalf("换工作区的按钮：%q", buttons)
	}
	a.LeaveAnswer(2)
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "改了\n" {
		t.Errorf("全部保存没存上：%q", got)
	}
	title, msg, buttons = a.LeaveQuestion()
	if a.Leave == nil || a.Leave.Step != LeaveWrites || title != "还有 git 命令在跑" || !strings.HasPrefix(msg, "demo：慢命令 还没跑完") ||
		!slices.Equal(buttons, []string{"取消", "中断并切换"}) {
		t.Fatalf("在跑的命令：%+v %q %q %q", a.Leave, title, msg, buttons)
	}
	a.LeaveAnswer(1)
	if a.Leave == nil || !a.Leave.Waiting || a.WsPath == wsB {
		t.Fatalf("应等命令结束：%+v", a.Leave)
	}
	a.RequestLeave("") // 等的时候再按退出：不打断
	wait("中断后换到乙", func() bool { return a.WsPath == wsB })
	if a.Leave != nil || len(a.Docs) != 0 || !strings.Contains(a.Output[len(a.Output)-1].Text, "已取消：慢命令") {
		t.Errorf("换过去之后：leave=%+v docs=%d", a.Leave, len(a.Docs))
	}

	// 什么都没有：直接退出
	a.RequestLeave("")
	if a.Leave != nil || !a.ForceClose {
		t.Errorf("没有要问的应直接退出：%+v %v", a.Leave, a.ForceClose)
	}
	wait("乙的状态", func() bool { return idle(a.Repos[0])() })
}

// 一次交进来好几个不在列表里的仓库：问一次，几个一起加。
func TestOpenArgsMany(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	r1, r2 := testutil.NewRepo(t, "one"), testutil.NewRepo(t, "two")
	for _, r := range []*testutil.Repo{r1, r2} {
		r.Write("f.txt", "f\n")
		r.Commit("f")
	}
	a, _ := newTestApp(t, ws)
	a.OpenArgs([]string{r1.Root, r2.Root, filepath.Join(r1.Root, "f.txt")}, "")
	if a.Confirm == nil || a.Confirm.Title != "添加 2 个仓库" || !strings.Contains(a.Confirm.Message, realDir(t, r1.Root)) || !strings.Contains(a.Confirm.Message, realDir(t, r2.Root)) {
		t.Fatalf("应一起问：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	if len(a.Repos) != 3 || len(a.St.Added) != 2 {
		t.Errorf("没都加进来：%v %v", repoRoots(a), a.St.Added)
	}
}

// 没有最近的工作区（第一次用）开空工作区：没有仓库、不记进最近的，终端在家目录（不在仓库列表里）、添加仓库是灰的；
// 老版本顶层加进来的仓库不进空工作区，留给下一个打开的。空工作区里打开目录就是换到那个工作区。
// 最近的都打不开也是空工作区，toast 说一声（Greet 不盖掉它）。
func TestStartEmpty(t *testing.T) {
	ra, rx := testutil.NewRepo(t, "a"), testutil.NewRepo(t, "x")
	stateDir := t.TempDir()
	if err := workspace.SaveState(stateDir, workspace.State{Added: []string{rx.Root}}); err != nil {
		t.Fatal(err)
	}
	a, wait := newTestAppIn(t, "", stateDir)
	t.Cleanup(a.Close)
	a.Start()
	if a.WsPath != "" || len(a.Repos) != 0 || a.Toast != "" {
		t.Fatalf("应是空工作区：ws=%q repos=%v toast=%q", a.WsPath, repoRoots(a), a.Toast)
	}
	home, _ := os.UserHomeDir()
	if r := a.TermRepo(); r.Root != home || r.Name != "~" || slices.Contains(a.Repos, r) || a.TermRepo() != r {
		t.Errorf("终端应在家目录、不在列表里：%+v", r)
	}
	if a.CanRun(Find("repo.add")) || !a.CanRun(Find("term.new")) || !a.CanRun(Find("file.openFolder")) {
		t.Error("空工作区：添加仓库应是灰的，新建终端、打开文件夹不是")
	}
	st, _ := workspace.LoadState(stateDir)
	if len(st.RecentWs) != 0 || len(st.Sessions) != 0 || !slices.Equal(st.Added, []string{rx.Root}) {
		t.Errorf("空工作区存的：recent=%v sessions=%v added=%v", st.RecentWs, st.Sessions, st.Added)
	}

	a.OpenArg(ra.Root)
	if a.Confirm != nil || a.WsPath != ra.Root || len(a.Repos) != 2 || !slices.Contains(repoRoots(a), rx.Root) {
		t.Fatalf("打开目录应换过去：confirm=%v ws=%q repos=%v", a.Confirm, a.WsPath, repoRoots(a))
	}
	wait("仓库读完", func() bool { return idle(a.Repos[0])() && idle(a.Repos[1])() })
	a.SaveState()
	st, _ = workspace.LoadState(stateDir)
	if s := st.Sessions[ra.Root]; len(st.Added) != 0 || s == nil || !slices.Equal(s.Added, []string{rx.Root}) || !slices.Equal(st.RecentWs, []string{ra.Root}) {
		t.Errorf("换过去以后：顶层 %v，会话 %+v，最近 %v", st.Added, s, st.RecentWs)
	}

	gone := filepath.Join(t.TempDir(), "没有了")
	stateDir = t.TempDir()
	if err := workspace.SaveState(stateDir, workspace.State{RecentWs: []string{gone}}); err != nil {
		t.Fatal(err)
	}
	b, _ := newTestAppIn(t, "", stateDir)
	t.Cleanup(b.Close)
	b.Greet("9.9.9")
	if b.WsPath != "" || len(b.Repos) != 0 || b.Toast != "最近的工作区都打不开了" {
		t.Errorf("最近的都打不开：ws=%q repos=%v toast=%q", b.WsPath, repoRoots(b), b.Toast)
	}
}

// 布局按工作区记：甲、乙各开各的三栏和宽度，切回来是自己的；没记过的工作区接着用最近的；
// 禅模式里换走记的是进之前的；存盘时会话里各是各的，顶层是最近的。
func TestPanelsPerWorkspace(t *testing.T) {
	ra, rb := testutil.NewRepo(t, "a"), testutil.NewRepo(t, "b")
	for _, r := range []*testutil.Repo{ra, rb} {
		r.Write("README.md", r.Root+"\n")
		r.Commit("一")
	}
	wsA, wsB := wsFile(t, "甲", ra.Root), wsFile(t, "乙", rb.Root)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, wsA, stateDir)
	a.Start()
	wait("甲的状态", func() bool { return idle(a.Repos[0])() })
	a.St.RightBar, a.St.SidebarW, a.St.Unified, a.St.SidebarMode = false, 400, true, workspace.ModeGit

	// 乙没记过：接着用甲的
	a.OpenWorkspace(wsB)
	if a.WsPath != wsB || a.St.RightBar || a.St.SidebarW != 400 || !a.St.Unified || a.St.SidebarMode != workspace.ModeGit {
		t.Fatalf("乙没接着用最近的：%+v", a.St.Panels())
	}
	wait("乙的状态", func() bool { return idle(a.Repos[0])() })
	a.St.RightBar, a.St.BottomBar, a.St.SidebarW, a.St.Unified, a.St.HistAll = true, false, 300, false, true
	a.St.SidebarMode, a.St.BottomH = workspace.ModeFiles, 500

	a.OpenWorkspace(wsA)
	if p := a.St.Panels(); p.RightBar || !p.BottomBar || p.SidebarW != 400 || !p.Unified || p.SidebarMode != workspace.ModeGit || p.HistAll || p.BottomH != 0 {
		t.Fatalf("回到甲不是甲的：%+v", p)
	}
	wait("甲的状态", func() bool { return idle(a.Repos[0])() })

	// 禅模式里换走：甲记的是进之前的三个面板
	a.ToggleZen()
	a.OpenWorkspace(wsB)
	if p := a.St.Panels(); a.Zen || !p.RightBar || p.BottomBar || !p.Sidebar || p.SidebarW != 300 || p.Unified || !p.HistAll || p.BottomH != 500 {
		t.Fatalf("回到乙不是乙的：zen=%v %+v", a.Zen, p)
	}
	wait("乙的状态", func() bool { return idle(a.Repos[0])() })
	a.SaveState()
	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	pa, pb := st.Sessions[wsA].Panels, st.Sessions[wsB].Panels
	if pa == nil || !pa.Sidebar || pa.RightBar || !pa.BottomBar || pa.SidebarW != 400 {
		t.Errorf("甲存的：%+v", pa)
	}
	if pb == nil || pb.SidebarW != 300 || !pb.HistAll || st.SidebarW != 300 || !st.HistAll || st.BottomH != 500 {
		t.Errorf("乙存的：%+v，顶层 %v %v", pb, st.SidebarW, st.HistAll)
	}

	// 不合理的值换回默认
	var bad workspace.State
	bad.SetPanels(&workspace.Panels{Sidebar: true, SidebarMode: "x", SidebarW: 5, RightW: 2000, TermW: 300})
	if bad.SidebarMode != workspace.ModeFiles || bad.SidebarW != 0 || bad.RightW != 0 || bad.TermW != 300 || !bad.Sidebar {
		t.Errorf("没换回默认：%+v", bad.Panels())
	}
}
