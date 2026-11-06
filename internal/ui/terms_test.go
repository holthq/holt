package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 终端测试都在 t.TempDir() 的临时仓库里，shell 用 /bin/sh（不读用户的 zsh 配置，起得快）。

// termState 是终端测试的状态目录：shell 用 /bin/sh，底栏开不开看 bar。写进 state.json，
// 窗口的第一帧（NewTester 里就画）就按它来，懒启动才量得准。
func termState(t *testing.T, bar bool) string {
	t.Helper()
	dir := t.TempDir()
	setBar(t, dir, bar)
	return dir
}

// setBar 改状态目录里的底栏开关（重开窗口前）。
func setBar(t *testing.T, dir string, bar bool) {
	t.Helper()
	st, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Settings.Shell, st.BottomBar = "/bin/sh", bar
	for _, s := range st.Sessions { // 会话也记布局，打开时用会话的
		if s.Panels != nil {
			s.Panels.BottomBar = bar
		}
	}
	if err := workspace.SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
}

// termUI 是终端测试的界面，在终端页；仓库都开始读状态。
func termUI(t *testing.T, ws, stateDir string) (*app.App, *wui.Tester, func(string, func() bool)) {
	t.Helper()
	a, tt, wait := newUI(t, ws, stateDir)
	a.BottomTab = app.BottomTerminal
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	return a, tt, wait
}

// readyPane 画一帧（有键盘的那一格在这时才启动 shell）、把键盘给它、等出提示符。
func readyPane(t *testing.T, a *app.App, tt *wui.Tester) *app.TermPane {
	t.Helper()
	a.FocusTerm = true
	tt.Frame()
	_, _, p := a.ActivePane()
	if p == nil || p.Term == nil {
		t.Fatalf("终端没起来：%+v", p)
	}
	testutil.WaitTermReady(t, tt, p.Term, 300*time.Millisecond, 15*time.Second)
	tt.Frame()
	return p
}

// typeLine 在有键盘的终端里敲一行、回车，等屏幕上出现 want。
func typeLine(t *testing.T, tt *wui.Tester, p *app.TermPane, line, want string) {
	t.Helper()
	tt.Type(line)
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, p.Term, want, 15*time.Second)
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// 懒启动：底栏关着一个 shell 都不起；打开底栏只起当前仓库的；切到别的仓库才起那个仓库的。
func TestTermLazyStart(t *testing.T) {
	alpha, beta, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := termUI(t, ws, termState(t, false))
	wait("读完状态", loaded(a))
	for range 5 {
		tt.Frame()
	}
	if n := a.StartedPanes(); n != 0 {
		t.Fatalf("底栏关着起了 %d 个 shell", n)
	}
	a.Exec("view.bottom")
	tt.Frame()
	if n := a.StartedPanes(); n != 1 || a.Current().Root != alpha.Root {
		t.Fatalf("打开底栏后起了 %d 个 shell（当前 %s），应只起 alpha 的", n, a.Current().Name)
	}
	a.UseRepo(a.RepoFor(beta.Root))
	tt.Frame()
	if n := a.StartedPanes(); n != 2 {
		t.Fatalf("切到 beta 后起了 %d 个 shell，应为 2", n)
	}
	mustText(t, tt, "终端 · beta")
}

// 多个终端标签：⌃⇧` 新建、点标签切换、⇧⌘] / ⇧⌘[ 轮换、双击改名（Esc 不改、回车改）；名字、顺序和目录记进会话，
// 重开窗口时标签按原样回来，shell 还是到显示时才起。
func TestTermTabs(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	r := a.Repos[0]
	wait("git status", idle(r))
	p1 := readyPane(t, a, tt)
	mustText(t, tt, "终端 · demo")

	tt.Key(wui.Ctrl|wui.Shift, wui.KeyBackquote)
	tt.Frame()
	if len(r.Terms) != 2 || r.TermSel != 1 {
		t.Fatalf("⌃⇧` 之后 %d 个标签、当前第 %d 个", len(r.Terms), r.TermSel)
	}
	mustText(t, tt, "终端 2")
	p2 := readyPane(t, a, tt)
	if p2 == p1 {
		t.Fatal("新标签用的还是第一个终端")
	}
	typeLine(t, tt, p2, "cd docs && echo 二号", "\n二号")
	if strings.Contains(p1.Term.Text(), "二号") {
		t.Fatal("敲进了第一个标签")
	}

	mustClick(t, tt, "终端 · demo")
	if r.TermSel != 0 {
		t.Fatalf("点第一个标签后当前是第 %d 个", r.TermSel)
	}
	readyPane(t, a, tt)
	tt.Key(wui.Cmd|wui.Shift, wui.KeyBracketRight) // 终端视图的 OnKey 交给命令
	wait("下一个终端标签", func() bool { return r.TermSel == 1 })
	tt.Frame()
	tt.Key(wui.Cmd|wui.Shift, wui.KeyBracketLeft)
	wait("上一个终端标签", func() bool { return r.TermSel == 0 })

	// 双击"终端 2"改名：Esc 不改
	at, ok := tt.Find("终端 2")
	if !ok {
		t.Fatalf("找不到终端 2；texts=%q", tt.Texts())
	}
	tt.ClickAt(at.X+at.W/2, at.Y+at.H/2)
	tt.ClickAt(at.X+at.W/2, at.Y+at.H/2)
	tt.Frame()
	if a.Rename.Tab != r.Terms[1] || !tt.Focused("终端标签的名字") {
		t.Fatalf("双击没进改名：rename=%+v", a.Rename)
	}
	tt.Type("不要")
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if a.Rename.Tab != nil || r.Terms[1].Name != "" {
		t.Fatalf("Esc 之后名字是 %q", r.Terms[1].Name)
	}
	// 右键 → 重命名…，回车改
	if err := tt.RightClick("终端 2"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("重命名…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("构建")
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if r.Terms[1].Name != "构建" || !tt.HasText("构建") {
		t.Fatalf("改名后是 %q", r.Terms[1].Name)
	}
	// 右键 → 向左移：顺序换了，当前跟着走
	mustRightClick(t, tt, "构建", "向左移")
	if r.TermLabel(r.Terms[0]) != "构建" || r.TermSel != 0 {
		t.Fatalf("左移后顺序：%q %q，当前 %d", r.TermLabel(r.Terms[0]), r.TermLabel(r.Terms[1]), r.TermSel)
	}

	// 关窗口：会话里记名字、顺序和 shell 现在的目录（lsof 读的），不记内容
	a.NoteTermDirs()
	a.SaveState()
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[ws]
	if s == nil || s.Terms[r.Root] == nil {
		t.Fatalf("会话里没记终端：%+v", s)
	}
	ts := s.Terms[r.Root]
	if len(ts.Tabs) != 2 || ts.Tabs[0].Name != "构建" || ts.Tabs[1].Name != "" || ts.Active != 0 {
		t.Fatalf("记下的终端标签：%+v", ts)
	}
	if realPath(t, ts.Tabs[0].Dir) != realPath(t, filepath.Join(root, "docs")) || ts.Tabs[1].Dir != "" {
		t.Fatalf("记下的目录：%q %q", ts.Tabs[0].Dir, ts.Tabs[1].Dir)
	}
	a.Close()

	setBar(t, state, false)
	b, tt2, wait2 := termUI(t, ws, state)
	r2 := b.Repos[0]
	wait2("git status", idle(r2))
	tt2.Frame()
	if got := []string{r2.TermLabel(b.TermTabs(r2)[0]), r2.TermLabel(b.TermTabs(r2)[1])}; !slices.Equal(got, []string{"构建", "终端 2"}) { // 没起名的按重开后的先后编号
		t.Fatalf("重开后的标签：%q", got)
	}
	if n := b.StartedPanes(); n != 0 {
		t.Fatalf("底栏关着，重开后起了 %d 个 shell", n)
	}
	b.St.BottomBar = true
	p := readyPane(t, b, tt2)
	typeLine(t, tt2, p, "pwd -P", "\n"+realPath(t, filepath.Join(root, "docs")))
	if n := b.StartedPanes(); n != 1 {
		t.Fatalf("只显示了一个标签，却起了 %d 个 shell", n)
	}
}

// 关闭：前台没命令直接关；有命令在跑（sleep）先问，取消不关，确认才关。⌘W 键盘在终端里时关终端、不关编辑器标签。
func TestTermCloseConfirm(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	r := a.Repos[0]
	wait("git status", idle(r))
	a.OpenFile(r, "README.md", true)
	wait("README 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	readyPane(t, a, tt)
	docs := len(a.Docs)

	tt.Key(wui.Ctrl|wui.Shift, wui.KeyBackquote)
	p := readyPane(t, a, tt)
	wait("认领到 shell 的进程号", func() bool { return p.Job() == "" })
	tt.Key(wui.Cmd, wui.KeyW)
	tt.Frame()
	if a.Confirm != nil || len(r.Terms) != 1 {
		t.Fatalf("没命令在跑也问了，或没关：confirm=%v tabs=%d", a.Confirm, len(r.Terms))
	}
	if len(a.Docs) != docs {
		t.Fatal("键盘在终端里，⌘W 关了编辑器的标签")
	}

	p = readyPane(t, a, tt)
	tt.Type("sleep 30")
	tt.Key(0, wui.KeyEnter)
	wait("sleep 在前台", func() bool { return p.Job() == "sleep" })
	tt.Key(wui.Cmd, wui.KeyW)
	tt.Frame()
	if a.Confirm == nil || !tt.HasText("关闭终端标签？") && !tt.HasText("关闭终端？") || !tt.HasText("“sleep”还在运行，关掉终端会结束它。") {
		t.Fatalf("没问：confirm=%+v texts=%q", a.Confirm, tt.Texts())
	}
	mustClick(t, tt, "取消")
	if len(r.Terms) != 1 || p.Term == nil {
		t.Fatal("取消了还是关了")
	}
	// 标签上的 × 也问
	mustClick(t, tt, "关闭终端标签")
	if a.Confirm == nil {
		t.Fatal("点 × 没问")
	}
	mustClick(t, tt, "关闭")
	tt.Frame()
	if len(r.Terms) != 0 || p.Term != nil {
		t.Fatalf("确认后没关：tabs=%d", len(r.Terms))
	}
	mustText(t, tt, "demo 没有打开的终端")
	mustClick(t, tt, "新建终端")
	if len(r.Terms) != 1 {
		t.Fatal("空的底栏里点新建终端没开")
	}
	readyPane(t, a, tt)
}

// 分屏：⌘D 左右、⇧⌘D 上下，新的一格从原来那格 shell 现在的目录起；拖缝改比例、双击回到一半；⌘W 一格一格关，回到单格。
func TestTermSplit(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := termUI(t, ws, termState(t, true))
	r := a.Repos[0]
	wait("git status", idle(r))
	p1 := readyPane(t, a, tt)
	wait("认领到 shell 的进程号", func() bool { return p1.Job() == "" })
	typeLine(t, tt, p1, "cd sub && echo 到了", "\n到了")
	tab := a.CurTermTab(r)

	tt.Key(wui.Cmd, wui.KeyD)
	wait("左右分屏", func() bool { return len(tab.Panes()) == 2 })
	if tab.Root.Pane != nil || !tab.Root.Row || tab.Active == p1 {
		t.Fatalf("分屏的树不对：%+v", tab.Root)
	}
	p2 := readyPane(t, a, tt)
	typeLine(t, tt, p2, "pwd -P", "\n"+realPath(t, filepath.Join(root, "sub")))
	if !tt.HasText("拖动调整分屏大小") {
		t.Fatal("没有分屏的缝")
	}

	// 拖缝：往右 100 点，左边那格变宽
	div, _ := tt.Find("拖动调整分屏大小")
	tt.Press(div.X+div.W/2, div.Y+div.H/2)
	tt.Move(div.X+div.W/2+50, div.Y+div.H/2)
	tt.Move(div.X+div.W/2+100, div.Y+div.H/2)
	tt.Release(div.X+div.W/2+100, div.Y+div.H/2)
	tt.Frame()
	if tab.Root.Ratio <= 0.55 {
		t.Fatalf("拖了缝比例还是 %.2f", tab.Root.Ratio)
	}
	if !tt.Focused("Terminal") {
		t.Error("拖缝把键盘从终端拿走了")
	}
	tt.ClickAt(div.X+div.W/2+100, div.Y+div.H/2)
	tt.ClickAt(div.X+div.W/2+100, div.Y+div.H/2)
	tt.Frame()
	if tab.Root.Ratio != 0.5 {
		t.Fatalf("双击缝后比例 %.2f", tab.Root.Ratio)
	}

	readyPane(t, a, tt)
	tt.Key(wui.Cmd|wui.Shift, wui.KeyD)
	wait("上下分屏", func() bool { return len(tab.Panes()) == 3 })
	if tab.Root.B.Pane != nil || tab.Root.B.Row {
		t.Fatalf("右边那格该上下分开：%+v", tab.Root.B)
	}
	p3 := readyPane(t, a, tt)

	// 分了屏：标题行有"关闭有键盘的那一格"，终端菜单里"关闭这一格分屏"能点；指针所在那格右上角有 ×
	closeActive := app.Tip(i18n.T("关闭有键盘的那一格分屏"), "term.closePane")
	if !tt.HasText(closeActive) || !a.CanRun(app.Find("term.closeSplit")) {
		t.Fatal("分屏时没有关一格的按钮 / 菜单")
	}
	if tt.HasText("关闭这一格分屏") {
		t.Fatal("指针不在格上时不该有 ×")
	}
	// 最下面那格（右边那半的下半）：点它右上角的 ×
	wait("认领到 shell 的进程号", func() bool { return p3.Job() == "" })
	div, _ = tt.Find("拖动调整分屏大小")
	tt.Move(div.X+80, div.Y+div.H-30)
	tt.Frame()
	mustClick(t, tt, "关闭这一格分屏")
	tt.Frame()
	if len(tab.Panes()) != 2 || p3.Term != nil {
		t.Fatalf("点 × 之后 %d 格", len(tab.Panes()))
	}

	// ⌘W：关掉有键盘的那格，回到单格（还是一开始那个 shell）；单格时没有关一格的按钮
	p2 = readyPane(t, a, tt)
	if p2 == p1 {
		t.Fatal("关掉一格后键盘该在右边那格")
	}
	wait("认领到 shell 的进程号", func() bool { return p2.Job() == "" })
	tt.Key(wui.Cmd, wui.KeyW)
	tt.Frame()
	if tab.Root.Pane != p1 || len(r.Terms) != 1 {
		t.Fatalf("没回到单格：%+v", tab.Root)
	}
	if tt.HasText("拖动调整分屏大小") || tt.HasText(closeActive) || tt.HasText("关闭这一格分屏") || a.CanRun(app.Find("term.closeSplit")) {
		t.Fatal("单格了还有缝或关一格的按钮")
	}
}

// 在此打开终端：文件树里目录的右键、面包屑的目录段，都新开一个终端标签、shell 在那个目录。
func TestTermOpenHere(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "n.md"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := termUI(t, ws, termState(t, false))
	r := a.Repos[0]
	a.ShowSidebar(workspace.ModeFiles)
	a.EnsureFiles(r)
	wait("树里有 notes", func() bool { return idle(r)() && r.Files.Loaded && tt.HasText("notes") })
	if n := a.StartedPanes(); n != 0 {
		t.Fatalf("底栏关着起了 %d 个 shell", n)
	}
	mustRightClickRow(t, tt, "notes", "在此打开终端")
	if !a.St.BottomBar || len(r.Terms) != 2 || r.TermSel != 1 {
		t.Fatalf("没开新终端：bar=%v tabs=%d sel=%d", a.St.BottomBar, len(r.Terms), r.TermSel)
	}
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "pwd -P", "\n"+realPath(t, filepath.Join(root, "notes")))
	if n := a.StartedPanes(); n != 1 {
		t.Fatalf("默认的终端标签没显示过，却起了 %d 个 shell", n)
	}

	// 面包屑：打开 notes/n.md，点 notes 段 → 在此打开终端
	a.OpenFile(r, "notes/n.md", true)
	wait("n.md 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	tt.Frame()
	mustClick(t, tt, "位置 demo/notes")
	if err := tt.ChooseMenuItem("在此打开终端"); err != nil {
		t.Fatalf("%v；菜单 %q", err, tt.Menu())
	}
	tt.Frame()
	if len(r.Terms) != 3 || r.TermSel != 2 {
		t.Fatalf("面包屑没开新终端：tabs=%d sel=%d", len(r.Terms), r.TermSel)
	}
	p = readyPane(t, a, tt)
	typeLine(t, tt, p, "pwd -P", "\n"+realPath(t, filepath.Join(root, "notes")))
}

// ⌘K：键盘在终端里清屏（回看也清），不在终端里还是带着 >git 打开命令面板。⌘F 在终端里查找：数匹配、Esc 关掉。
// 切到别的仓库再切回来，回看里的输出还在。
func TestTermClearFindScrollback(t *testing.T) {
	alpha, beta, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	wait("读完状态", loaded(a))
	p := readyPane(t, a, tt)
	typeLine(t, tt, p, "for i in $(seq 1 300); do echo 行$i; done", "\n行300")

	a.UseRepo(a.RepoFor(beta.Root))
	readyPane(t, a, tt)
	a.UseRepo(a.RepoFor(alpha.Root))
	if q := readyPane(t, a, tt); q != p {
		t.Fatal("切回 alpha 换了一个终端")
	}
	// 找回看里早就滚出屏幕的那几行
	tt.Key(wui.Cmd, wui.KeyF)
	tt.Frame()
	if !p.Find.Open || !tt.Focused("在终端里查找") {
		t.Fatalf("⌘F 没开查找条：%+v", p.Find)
	}
	tt.Type("行10")
	tt.Frame()
	wait("找到 行10", func() bool { st := p.Term.SearchStatus(); return !st.Searching && st.Total > 0 })
	if st := p.Term.SearchStatus(); st.Total < 11 { // 行10、行100–109，加上命令那一行里没有
		t.Fatalf("回看里只找到 %d 个 行10：%+v", st.Total, st)
	}
	mustText(t, tt, "1 / ")
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if p.Find.Open {
		t.Fatal("Esc 没关查找条")
	}

	readyPane(t, a, tt)
	tt.Key(wui.Cmd, wui.KeyK)
	tt.Frame()
	testutil.WaitTermGone(t, tt, p.Term, "行300", 15*time.Second)
	if a.Palette.Open {
		t.Fatal("键盘在终端里，⌘K 开了命令面板")
	}
	p.Term.Search("行1")
	wait("清屏后找 行1", func() bool { return !p.Term.SearchStatus().Searching })
	if st := p.Term.SearchStatus(); st.Total > 1 { // 只剩命令那一行
		t.Fatalf("清屏后回看里还有 %d 个 行1", st.Total)
	}
	p.Term.EndSearch()

	// 把键盘挪到右栏的历史搜索框，⌘K 照旧带着 >git 开命令面板
	mustClick(t, tt, "搜索历史")
	tt.Frame()
	if a.TermFocused || !tt.Focused("搜索提交") {
		t.Fatalf("键盘没离开终端：term=%v", a.TermFocused)
	}
	tt.Key(wui.Cmd, wui.KeyK)
	tt.Frame()
	if !a.Palette.Open || a.Palette.Text != ">git " {
		t.Fatalf("键盘不在终端里，⌘K 应打开 >git：open=%v text=%q", a.Palette.Open, a.Palette.Text)
	}
}

// 命令面板 > 后敲的不是命令：最后一项"在终端运行"，选了把它放进当前仓库的终端，不回车。
func TestPaletteRunInTerminal(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, false))
	r := a.Repos[0]
	wait("git status", idle(r))
	a.OpenPalette(">echo 面板来的")
	a.UpdatePalette()
	items := a.Palette.Items
	if len(items) == 0 || items[len(items)-1].Kind != app.ItemTerm || items[len(items)-1].Text != "在终端运行：echo 面板来的" {
		t.Fatalf("没有在终端运行：%+v", items)
	}
	a.AcceptPalette(len(items) - 1)
	tt.Frame()
	if !a.St.BottomBar || a.Palette.Open {
		t.Fatalf("底栏没开或面板没关：bar=%v", a.St.BottomBar)
	}
	_, _, p := a.ActivePane()
	wait("命令进了输入行", func() bool { tt.Frame(); return p.Term != nil && strings.Contains(p.Term.Text(), "echo 面板来的") })
	for range 20 {
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
	if strings.Contains(p.Term.Text(), "\n面板来的") {
		t.Fatalf("自己回车了：\n%s", p.Term.Text())
	}
	a.FocusTerm = true
	tt.Frame()
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, p.Term, "\n面板来的", 15*time.Second)
}

// >git 里要人输入的命令：提示行先说会去终端；回车后不进队列，在当前仓库的终端里敲好、回车跑，
// 命令历史照记。当前那一格前台有命令在跑、shell cd 出了仓库就新开一个标签。用 bisect log：转终端，但不开编辑器。
func TestRunCommandToTerminal(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, false))
	r := a.Repos[0]
	wait("git status", idle(r))
	a.OpenPalette(">git bisect log")
	a.UpdatePalette()
	if it := a.Palette.Items; a.Palette.Note != "这条会在终端里跑：要交互" || len(it) == 0 || !strings.HasSuffix(it[0].Detail, "的终端里运行") {
		t.Fatalf("提示：%q %+v", a.Palette.Note, it)
	}
	out := len(a.Output)
	a.AcceptPalette(0)
	if a.Toast != "git bisect log：要交互，已在终端里运行" || !a.St.BottomBar || a.BottomTab != app.BottomTerminal || len(r.Queue) != 0 {
		t.Fatalf("toast %q，底栏 %v，队列 %d", a.Toast, a.St.BottomBar, len(r.Queue))
	}
	// 敲的字在输入行里、回了车（后面又出了提示符）
	ran := func(p *app.TermPane, cmd string) func() bool {
		return func() bool {
			tt.Frame()
			if p.Term == nil {
				return false
			}
			text := p.Term.Text()
			i := strings.LastIndex(text, cmd)
			return i >= 0 && strings.Contains(text[i+len(cmd):], "$")
		}
	}
	_, _, p := a.ActivePane()
	wait("终端里跑了", ran(p, "git bisect log"))
	if len(a.Output) != out || len(r.Queue) != 0 || !slices.Contains(a.Palette.History, "bisect log") {
		t.Fatalf("输出栏 %d → %d，历史 %v", out, len(a.Output), a.Palette.History)
	}

	// 前台在跑命令：不往里敲，新开一个标签
	p.Term.Paste("sleep 30")
	p.Term.Send([]byte("\r"))
	wait("sleep 在跑", func() bool { tt.Frame(); return p.Job() == "sleep" })
	a.RunCommand(r, "bisect log")
	if n := len(a.TermTabs(r)); n != 2 {
		t.Fatalf("前台有命令时 %d 个标签", n)
	}
	q := a.CurTermTab(r).Current()
	wait("新标签里跑了", ran(q, "git bisect log"))

	// shell cd 出了仓库：也新开（/bin/sh 不报目录，按进程号用 lsof 读）
	wait("认领到进程号", func() bool { tt.Frame(); return q.Job() != "?" })
	q.Term.Paste("cd /")
	q.Term.Send([]byte("\r"))
	wait("cd 走了", ran(q, "cd /"))
	a.RunCommand(r, "bisect log")
	if n := len(a.TermTabs(r)); n != 3 {
		t.Fatalf("cd 走了 %d 个标签", n)
	}
	wait("又一个新标签里跑了", ran(a.CurTermTab(r).Current(), "git bisect log"))
}
