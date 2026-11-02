package app

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// winHarness 是测试里的多窗口：没有真窗口（Win 为 nil），所有窗口的回调排进一个队列；
// 关窗口、退出照 mygo 的顺序走（OnClose 能拦、拦不住才 OnClosed；Quit 先发 OnBeforeQuit，放行了再一个个关窗口，有一个拦住就停）。
type winHarness struct {
	t     *testing.T
	w     *Windows
	q     testutil.Queue
	from  map[*App]*App // 新窗口错开的是哪个窗口（nil 为按记着的位置）
	quits int           // Windows.Quit 调了几次
	later []func()      // stagger 之后 Windows.Later 约的（tick 时跑）
	gaps  []time.Duration
}

// stagger 让还原错开开窗口：Windows.Later 只记下来，tick 时才跑（当作计时器到点了）。
func (h *winHarness) stagger() {
	h.w.Later = func(d time.Duration, fn func()) {
		h.gaps = append(h.gaps, d)
		h.later = append(h.later, fn)
	}
}

// tick 是约好的计时器都到点了。
func (h *winHarness) tick() {
	fns := h.later
	h.later = nil
	for _, fn := range fns {
		fn()
	}
	h.q.Drain()
}

func newWinHarness(t *testing.T, stateDir string) *winHarness {
	t.Helper()
	h := &winHarness{t: t, w: NewWindows(stateDir), q: testutil.NewQueue(), from: map[*App]*App{}}
	h.w.NewWindow = func(a, from *App) {
		a.Update = h.q.Post
		h.from[a] = from
	}
	h.w.Quit = func() {
		h.quits++
		h.quit()
	}
	t.Cleanup(func() {
		for _, a := range h.w.Apps {
			a.Close()
		}
	})
	return h
}

// wait 跑掉排着的回调，直到 cond 成立。
func (h *winHarness) wait(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		h.q.Drain()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("等 %s 超时", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// idle 等所有窗口的仓库都读完状态。
func (h *winHarness) idle() {
	h.t.Helper()
	h.wait("所有窗口的状态", func() bool {
		for _, a := range h.w.Apps {
			for _, r := range a.Repos {
				if !idle(r)() {
					return false
				}
			}
		}
		return true
	})
}

// close 是点了窗口 a 的关闭按钮（或 ⌘W 关到没有标签）。
func (h *winHarness) close(a *App) {
	if h.w.CanClose(a) {
		h.w.Closed(a)
	}
	h.q.Drain()
}

// answer 是在窗口 a 的退出对话框里选第 i 个按钮；问完了窗口自己关掉（leaveNext 的 a.Win.Close）。
func (h *winHarness) answer(a *App, i int) {
	a.LeaveAnswer(i)
	if a.ForceClose && slices.Contains(h.w.Apps, a) {
		h.close(a)
	}
	h.q.Drain()
}

// quit 是 ⌘Q（mygo.App.Quit）。
func (h *winHarness) quit() {
	if !h.w.RequestQuit() {
		h.q.Drain()
		return
	}
	for _, a := range slices.Clone(h.w.Apps) {
		if !slices.Contains(h.w.Apps, a) {
			continue
		}
		if !h.w.CanClose(a) {
			h.q.Drain()
			return
		}
		h.w.Closed(a)
	}
}

// open 打开 a 第一个仓库里的 README.md，等它读进来。
func (h *winHarness) open(a *App) *docs.Doc {
	h.t.Helper()
	d := a.OpenFile(a.Repos[0], "README.md", true)
	h.wait("README 打开", func() bool { return d.Buf != nil })
	return d
}

// winWorkspaces 给每个名字建一个有一个提交的仓库和只有它的工作区文件，返回工作区文件和仓库根。
func winWorkspaces(t *testing.T, names ...string) (ws, roots []string) {
	t.Helper()
	for _, n := range names {
		r := testutil.NewRepo(t, n)
		r.Write("README.md", n+"\n")
		r.Commit("一")
		ws, roots = append(ws, wsFile(t, n, r.Root)), append(roots, r.Root)
	}
	return ws, roots
}

func winPaths(w *Windows) []string { return w.paths() }

// openPaths 是真开着的窗口（winPaths 在还原中还带着没开的）。
func openPaths(w *Windows) []string {
	var l []string
	for _, a := range w.Apps {
		l = append(l, a.WsPath)
	}
	return l
}

// saveWindows 把 state.json 的 windows 设成 ws（下次启动要开回的窗口）。
func saveWindows(t *testing.T, dir string, ws ...string) {
	t.Helper()
	st, err := workspace.LoadState(dir)
	if err != nil {
		st = workspace.Default()
	}
	st.Windows = ws
	if err := workspace.SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
}

func loadWinState(t *testing.T, dir string) workspace.State {
	t.Helper()
	st, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// 新窗口占最小的空槽位（main、main-2……）、错开前台窗口放；同一个工作区只开一次，从哪里再开都是叫到前面、在那里说一声；
// 空工作区的窗口可以开好几个，里面打开工作区就在这个窗口里换；打不开的报在发起的窗口上。
func TestWindowsOpenFocus(t *testing.T) {
	ws, _ := winWorkspaces(t, "甲", "乙", "丙")
	h := newWinHarness(t, t.TempDir())
	if err := h.w.Restore(ws[0]); err != nil {
		t.Fatal(err)
	}
	a := h.w.Apps[0]
	if len(h.w.Apps) != 1 || a.WsPath != ws[0] || a.Slot != 1 || a.StateKey() != "main" || a.Title() != "甲 — Grove" || h.from[a] != nil {
		t.Fatalf("第一个窗口：%v slot=%d key=%s title=%q from=%v", winPaths(h.w), a.Slot, a.StateKey(), a.Title(), h.from[a])
	}

	b := h.w.Open(ws[1], nil)
	if b == nil || !slices.Equal(winPaths(h.w), ws[:2]) || b.Slot != 2 || b.StateKey() != "main-2" || h.from[b] != a || h.w.Focused() != b {
		t.Fatalf("新窗口里打开乙：%v slot=%d from=%v", winPaths(h.w), b.Slot, h.from[b])
	}
	if b.Title() != "乙 — Grove" || a.WsPath != ws[0] {
		t.Errorf("标题 %q，甲成了 %s", b.Title(), a.WsPath)
	}
	h.idle()

	// 已经开着的：叫到前面、在那个窗口里说一声
	if o := h.w.Open(ws[0], b); o != a || len(h.w.Apps) != 2 || h.w.Focused() != a || a.Toast != "已经开着工作区 甲" {
		t.Fatalf("再开甲：%v focused=%v toast=%q", winPaths(h.w), h.w.Focused().WsPath, a.Toast)
	}
	a.Toast = ""
	b.openNewWindow(ws[0]) // 在新窗口中打开…
	if len(h.w.Apps) != 2 || h.w.Focused() != a || a.Toast != "已经开着工作区 甲" {
		t.Errorf("新窗口里再开甲：%v toast=%q", winPaths(h.w), a.Toast)
	}
	a.OpenWorkspace(ws[1]) // 换工作区、最近的工作区：换到别的窗口开着的也是叫过去，这个窗口不换
	if a.WsPath != ws[0] || a.Leave != nil || h.w.Focused() != b || b.Toast != "已经开着工作区 乙" || len(h.w.Apps) != 2 {
		t.Errorf("在甲里换到乙：甲=%s leave=%+v toast=%q", a.WsPath, a.Leave, b.Toast)
	}

	// 新建窗口：空工作区，可以开好几个
	a.NewWindow()
	e := h.w.Focused()
	if len(h.w.Apps) != 3 || e.WsPath != "" || e.Slot != 3 || e.Title() != "Grove" || h.from[e] != a {
		t.Fatalf("新建窗口：%v slot=%d title=%q", winPaths(h.w), e.Slot, e.Title())
	}
	e.NewWindow()
	e2 := h.w.Focused()
	if len(h.w.Apps) != 4 || e2 == e || e2.Slot != 4 || h.from[e2] != e {
		t.Fatalf("第二个空窗口：%v", winPaths(h.w))
	}
	h.close(e2)

	// 关掉乙：槽位让出来，下一个新窗口先用它；窗口按槽位排
	h.close(b)
	if !slices.Equal(winPaths(h.w), []string{ws[0], ""}) || h.quits != 0 {
		t.Fatalf("关掉乙：%v quits=%d", winPaths(h.w), h.quits)
	}
	c := h.w.Open(ws[1], e)
	if c.Slot != 2 || !slices.Equal(winPaths(h.w), []string{ws[0], ws[1], ""}) || h.from[c] != e {
		t.Fatalf("再开乙：slot=%d %v", c.Slot, winPaths(h.w))
	}

	// 空窗口里打开工作区：就在这个窗口里换
	e.OpenWorkspace(ws[2])
	if e.WsPath != ws[2] || h.w.Find(ws[2]) != e || len(h.w.Apps) != 3 || e.Title() != "丙 — Grove" {
		t.Errorf("空窗口里打开丙：%s %v", e.WsPath, winPaths(h.w))
	}
	h.idle()

	// 打不开的：报在发起的窗口上，从最近的工作区里去掉，不开窗口
	gone := filepath.Join(t.TempDir(), "没有了.code-workspace")
	a.St.RecentWs = append(a.St.RecentWs, gone)
	if o := h.w.Open(gone, a); o != nil || !a.AlertOpen || slices.Contains(a.St.RecentWs, gone) || len(h.w.Apps) != 3 {
		t.Errorf("打不开的：%v alert=%v recent=%v", winPaths(h.w), a.AlertOpen, a.St.RecentWs)
	}
}

// 启动：上次开着的窗口按槽位一个个开回来，标签跟着各自的窗口；打不开的跳过、重复的只开一次；命令行给的工作区开着就叫到前面，
// 没开就再开一个；一个都没开成就开最近的（都打不开是空工作区，提示一声）。
func TestWindowsRestore(t *testing.T) {
	ws, roots := winWorkspaces(t, "甲", "乙", "丙")
	dir := t.TempDir()
	h := newWinHarness(t, dir)
	if err := h.w.Restore(ws[0]); err != nil {
		t.Fatal(err)
	}
	a := h.w.Apps[0]
	b := h.w.Open(ws[1], a)
	h.w.Open("", b)
	h.idle()
	h.open(a)
	h.open(b)
	h.quit()
	if len(h.w.Apps) != 0 {
		t.Fatalf("退出后还开着：%v", winPaths(h.w))
	}
	st := loadWinState(t, dir)
	if !slices.Equal(st.Windows, []string{ws[0], ws[1], ""}) {
		t.Fatalf("windows = %q", st.Windows)
	}
	for i := range 2 {
		if s := st.Sessions[ws[i]]; s == nil || len(s.Tabs) != 1 || s.Tabs[0].Path != filepath.Join(roots[i], "README.md") {
			t.Errorf("%s 的会话：%+v", ws[i], s)
		}
	}

	h = newWinHarness(t, dir)
	if err := h.w.Restore(""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(winPaths(h.w), []string{ws[0], ws[1], ""}) {
		t.Fatalf("开回来的：%v", winPaths(h.w))
	}
	h.wait("标签放回去", func() bool {
		for _, a := range h.w.Apps[:2] {
			if d := a.CurrentDoc(); d == nil || d.Buf == nil {
				return false
			}
		}
		return true
	})
	for i, a := range h.w.Apps {
		if a.Slot != i+1 || h.from[a] != nil {
			t.Errorf("第 %d 个窗口：slot=%d from=%v", i, a.Slot, h.from[a])
		}
		var tabs []string
		for _, d := range a.AllDocs() {
			tabs = append(tabs, d.Abs)
		}
		if i < 2 && !slices.Equal(tabs, []string{filepath.Join(roots[i], "README.md")}) || i == 2 && len(tabs) != 0 {
			t.Errorf("%q 的标签：%q", a.WsPath, tabs)
		}
	}
	h.idle()
	h.quit()

	// 命令行给的：没开的再开一个（错开前台窗口），开着的叫到前面
	h = newWinHarness(t, dir)
	h.w.Restore(ws[2])
	if c := h.w.Focused(); !slices.Equal(winPaths(h.w), []string{ws[0], ws[1], "", ws[2]}) || c.WsPath != ws[2] || h.from[c] != h.w.Apps[2] {
		t.Errorf("命令行给丙：%v", winPaths(h.w))
	}
	h.idle()
	h.quit()
	h = newWinHarness(t, dir)
	h.w.Restore(ws[0])
	if len(h.w.Apps) != 4 || h.w.Focused() != h.w.Apps[0] {
		t.Errorf("命令行给甲：%v focused=%q", winPaths(h.w), h.w.Focused().WsPath)
	}
	h.idle()
	h.quit()

	// 打不开的跳过，重复的只开一次
	gone := filepath.Join(t.TempDir(), "没有了")
	st = loadWinState(t, dir)
	st.Windows = []string{ws[1], gone, ws[1], ""}
	workspace.SaveState(dir, st)
	h = newWinHarness(t, dir)
	h.w.Restore("")
	if !slices.Equal(winPaths(h.w), []string{ws[1], ""}) {
		t.Errorf("跳过打不开的、重复的：%v", winPaths(h.w))
	}
	h.idle()
	h.quit()

	// 命令行给的打不开：有别的窗口就报在前台窗口上，没有就是错误
	h = newWinHarness(t, dir)
	h.w.Restore(gone)
	if f := h.w.Focused(); len(h.w.Apps) != 2 || !f.AlertOpen {
		t.Errorf("命令行给的打不开：%v alert=%v", winPaths(h.w), f.AlertOpen)
	}
	h.quit()
	st = loadWinState(t, dir)
	st.Windows, st.RecentWs = []string{gone}, []string{gone}
	workspace.SaveState(dir, st)
	if err := newWinHarness(t, dir).w.Restore(gone); err == nil {
		t.Error("命令行给的打不开、又没有窗口可开：应报错")
	}
	h = newWinHarness(t, dir)
	h.w.Restore("")
	if len(h.w.Apps) != 1 || h.w.Apps[0].WsPath != "" || h.w.Apps[0].Toast != "最近的工作区都打不开了" {
		t.Errorf("都打不开：%v toast=%q", winPaths(h.w), h.w.Apps[0].Toast)
	}
}

// ⌘Q：有没保存的窗口一个个问（问完的关掉再问下一个），中途取消就停在那里、已经关掉的不再开回来；
// 都问完了退出，没要问的窗口一起关掉；退出放行之后才发现有没保存的（关到它时），接着问它。
func TestWindowsQuitSequence(t *testing.T) {
	ws, roots := winWorkspaces(t, "甲", "乙", "丙")
	dir := t.TempDir()
	h := newWinHarness(t, dir)
	h.w.Restore(ws[0])
	a := h.w.Apps[0]
	b := h.w.Open(ws[1], a)
	c := h.w.Open(ws[2], b)
	h.idle()
	h.open(a).Buf.Set("甲改了\n")
	h.open(b).Buf.Set("乙改了\n")

	h.quit()
	if len(h.w.Apps) != 3 || a.Leave == nil || !a.Leave.Open || b.Leave != nil || h.w.Focused() != a {
		t.Fatalf("先问甲：%v leave=%+v/%+v focused=%q", winPaths(h.w), a.Leave, b.Leave, h.w.Focused().WsPath)
	}
	if _, msg, _ := a.LeaveQuestion(); msg != "README.md还没保存，退出前要保存吗？" {
		t.Errorf("问的：%q", msg)
	}
	h.w.Focus(c)
	h.quit() // 问的时候再按 ⌘Q：还是问甲，叫回前面
	if b.Leave != nil || h.w.Focused() != a {
		t.Errorf("再按 ⌘Q：leave 乙=%+v focused=%q", b.Leave, h.w.Focused().WsPath)
	}

	h.answer(a, 1) // 甲不保存退出：关掉，接着问乙
	if !slices.Equal(winPaths(h.w), ws[1:]) || b.Leave == nil || !b.Leave.Open || h.w.Focused() != b {
		t.Fatalf("问完甲：%v leave=%+v", winPaths(h.w), b.Leave)
	}
	if got, _ := os.ReadFile(filepath.Join(roots[0], "README.md")); string(got) != "甲\n" {
		t.Errorf("不保存的存上了：%q", got)
	}
	h.answer(b, 0) // 乙取消：停在这里，甲不开回来，丙照旧开着
	if !slices.Equal(winPaths(h.w), ws[1:]) || b.Leave != nil || h.quits != 0 {
		t.Fatalf("取消：%v leave=%+v quits=%d", winPaths(h.w), b.Leave, h.quits)
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws[1:]) {
		t.Errorf("取消后 windows = %q", st.Windows)
	}

	h.quit() // 再退：问乙，全部保存并退出；没有要问的了，丙一起关掉
	if b.Leave == nil || !b.Leave.Open {
		t.Fatalf("再退：leave=%+v", b.Leave)
	}
	h.answer(b, 2)
	if len(h.w.Apps) != 0 || h.quits != 1 {
		t.Fatalf("问完了：%v quits=%d", winPaths(h.w), h.quits)
	}
	if got, _ := os.ReadFile(filepath.Join(roots[1], "README.md")); string(got) != "乙改了\n" {
		t.Errorf("全部保存没存上：%q", got)
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws[1:]) || st.Sessions[ws[1]] == nil || st.Sessions[ws[2]] == nil {
		t.Errorf("退出后 windows = %q，会话 %v", st.Windows, st.Sessions)
	}

	// 放行之后关到丙才发现有没保存的：拦下来问丙，问完再退一次
	h = newWinHarness(t, dir)
	h.w.Restore("")
	b, c = h.w.Apps[0], h.w.Apps[1]
	h.idle()
	if !h.w.RequestQuit() {
		t.Fatal("没有要问的也拦了")
	}
	h.open(c).Buf.Set("丙改了\n")
	if !h.w.CanClose(b) {
		t.Fatal("乙没有要问的也拦了")
	}
	h.w.Closed(b)
	if h.w.CanClose(c) {
		t.Fatal("丙有没保存的没拦")
	}
	h.q.Drain()
	if c.Leave == nil || !c.Leave.Open || h.quits != 0 {
		t.Fatalf("应问丙：leave=%+v quits=%d", c.Leave, h.quits)
	}
	h.answer(c, 1)
	if len(h.w.Apps) != 0 || h.quits != 1 {
		t.Errorf("问完丙：%v quits=%d", winPaths(h.w), h.quits)
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws[1:]) {
		t.Errorf("windows = %q", st.Windows)
	}
}

// 关一个窗口应用照旧开着；关最后一个就退出（有没保存的先问，取消就不关），下次启动开回它；
// 只剩一个空窗口、或开的是最近的工作区时不记 windows（和单窗口时的文件一样）。
func TestWindowsLastClosed(t *testing.T) {
	ws, _ := winWorkspaces(t, "甲", "乙")
	dir := t.TempDir()
	h := newWinHarness(t, dir)
	h.w.Restore(ws[0])
	a := h.w.Apps[0]
	b := h.w.Open(ws[1], a)
	h.idle()
	h.close(b)
	if !slices.Equal(winPaths(h.w), ws[:1]) || h.quits != 0 {
		t.Fatalf("关乙：%v quits=%d", winPaths(h.w), h.quits)
	}
	st := loadWinState(t, dir)
	if !slices.Equal(st.RecentWs, []string{ws[1], ws[0]}) || !slices.Equal(st.Windows, ws[:1]) {
		t.Errorf("关乙后：recent=%q windows=%q", st.RecentWs, st.Windows)
	}

	h.open(a).Buf.Set("改了\n")
	h.close(a)
	if len(h.w.Apps) != 1 || a.Leave == nil || !a.Leave.Open {
		t.Fatalf("关最后一个、有没保存的：%v leave=%+v", winPaths(h.w), a.Leave)
	}
	h.answer(a, 0)
	if len(h.w.Apps) != 1 || h.quits != 0 {
		t.Fatalf("取消了还关：%v quits=%d", winPaths(h.w), h.quits)
	}
	h.close(a)
	h.answer(a, 1)
	if len(h.w.Apps) != 0 || h.quits != 1 {
		t.Fatalf("关最后一个：%v quits=%d", winPaths(h.w), h.quits)
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws[:1]) {
		t.Errorf("windows = %q", st.Windows)
	}
	h = newWinHarness(t, dir)
	h.w.Restore("")
	if !slices.Equal(winPaths(h.w), ws[:1]) {
		t.Errorf("下次启动开的：%v（应是最后关的甲，不是最近的乙）", winPaths(h.w))
	}
	h.idle()
	st = loadWinState(t, dir)
	if st.Windows != nil || st.RecentWs[0] != ws[0] {
		t.Errorf("一个窗口、开的是最近的：windows = %q", st.Windows)
	}

	// 只剩一个空窗口：不记 windows，下次开最近的
	h.w.Open("", nil)
	h.close(h.w.Apps[0])
	h.close(h.w.Apps[0])
	if len(h.w.Apps) != 0 || h.quits != 1 {
		t.Fatalf("关空窗口：%v quits=%d", winPaths(h.w), h.quits)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, workspace.StateFile)); bytes.Contains(b, []byte(`"windows"`)) {
		t.Errorf("只有一个空窗口也记了 windows：\n%s", b)
	}
}

// 菜单项交给 Click 带来的窗口（不是建菜单栏的前台窗口）；带来的不是工作台窗口就给前台的；
// 外面交进来的工作区文件开新窗口、别的给前台窗口；设置改了每个窗口都换；共用的表一个窗口改了别的跟着。
func TestWindowsRouting(t *testing.T) {
	ws, roots := winWorkspaces(t, "甲", "乙", "丙")
	h := newWinHarness(t, t.TempDir())
	h.w.Restore(ws[0])
	a := h.w.Apps[0]
	b := h.w.Open(ws[1], a)
	h.idle()

	a.Win, b.Win = new(mygo.Window), new(mygo.Window) // 只拿来比指针
	var item *mygo.MenuItem
	for _, top := range b.BuildMenu().Items() {
		for _, it := range top.Submenu {
			if it.ID == "view.sidebar" {
				item = it
			}
		}
	}
	if item == nil {
		t.Fatal("菜单里没有 view.sidebar")
	}
	sa, sb := a.St.Sidebar, b.St.Sidebar
	item.Click(item, a.Win)
	h.q.Drain()
	if a.St.Sidebar == sa || b.St.Sidebar != sb {
		t.Errorf("乙建的菜单在甲的窗口点：甲 %v→%v，乙 %v→%v", sa, a.St.Sidebar, sb, b.St.Sidebar)
	}
	h.w.Focus(b)
	item.Click(item, nil)              // 设置、关于在前面时 mygo 给的是它们（或 nil）
	item.Click(item, new(mygo.Window)) // 再点一次，换回来
	h.q.Drain()
	if b.St.Sidebar != sb || a.St.Sidebar == sa {
		t.Errorf("不是工作台窗口：应交给前台的乙")
	}
	b.menuSig = "旧的"
	h.w.Focus(a)
	h.w.Focus(b)
	if h.w.Focused() != b || b.menuSig != "" {
		t.Errorf("到前台的窗口要重建菜单栏：focused=%q sig=%q", h.w.Focused().WsPath, b.menuSig)
	}
	a.Win, b.Win = nil, nil

	// 外面交进来的
	h.w.Route([]string{ws[2]})
	c := h.w.Focused()
	if !slices.Equal(winPaths(h.w), ws) || c.WsPath != ws[2] {
		t.Fatalf("交进来丙：%v", winPaths(h.w))
	}
	h.w.Route([]string{ws[0]})
	if len(h.w.Apps) != 3 || h.w.Focused() != a || a.Toast != "已经开着工作区 甲" {
		t.Errorf("交进来开着的甲：%v toast=%q", winPaths(h.w), a.Toast)
	}
	h.idle()
	h.w.Focus(c)
	h.w.Route([]string{filepath.Join(roots[2], "README.md")})
	h.wait("丙里打开 README", func() bool { d := c.CurrentDoc(); return d != nil && d.Buf != nil })
	if len(a.AllDocs())+len(b.AllDocs()) != 0 {
		t.Errorf("文件开到别的窗口里了")
	}
	x := testutil.NewRepo(t, "丁")
	e := h.w.Open("", nil)
	h.w.Route([]string{x.Root}) // 空工作区的窗口：目录当工作区打开
	h.q.Drain()
	if e.WsPath != x.Root || len(h.w.Apps) != 4 {
		t.Errorf("空窗口里交进来目录：%q %v", e.WsPath, winPaths(h.w))
	}
	h.idle()

	// 设置：改了每个窗口都换（外观、语言在 main 的 OnSettings 里套到每个窗口上）
	a.St.Settings.SoftWrap = !a.St.Settings.SoftWrap
	a.SettingsChanged()
	for _, o := range h.w.Apps {
		if !reflect.DeepEqual(o.St.Settings, a.St.Settings) {
			t.Errorf("%q 的设置没跟着换", o.WsPath)
		}
	}

	// 共用的表：甲清掉最近的工作区，别的窗口也清掉，谁再存都带不回来
	a.ClearRecentWs()
	b.SaveState()
	for _, o := range h.w.Apps {
		if len(o.St.RecentWs) != 0 {
			t.Errorf("%q 的最近的工作区：%q", o.WsPath, o.St.RecentWs)
		}
	}
	if st := loadWinState(t, h.w.store.Dir()); len(st.RecentWs) != 0 {
		t.Errorf("state.json 里还有：%q", st.RecentWs)
	}
}

// 两个窗口共用一个 state.json：谁先存都一样——两个工作区的会话都在、最近的工作区 / 文件和提交信息历史两边加的都在、windows 按槽位；
// 只有一个窗口时写出来的和没有注册表的 App 一字不差。
func TestStoreTwoWindows(t *testing.T) {
	ws, roots := winWorkspaces(t, "甲", "乙")
	for _, order := range []string{"甲乙", "乙甲"} {
		dir := t.TempDir()
		h := newWinHarness(t, dir)
		h.w.Restore(ws[0])
		a := h.w.Apps[0]
		b := h.w.Open(ws[1], a)
		h.idle()
		h.open(a)
		h.open(b)
		a.noteCommitMsg("甲的提交")
		b.noteCommitMsg("乙的提交")
		a.noteSearch(search.Query{Text: "甲搜"})
		b.noteSearch(search.Query{Text: "乙搜"})
		a.St.Sidebar, b.St.Sidebar = true, true
		first, second := a, b
		if order == "乙甲" {
			first, second = b, a
		}
		first.SaveState()
		second.SaveState()
		st := loadWinState(t, dir)
		for i := range 2 {
			if s := st.Sessions[ws[i]]; s == nil || len(s.Tabs) != 1 || s.Tabs[0].Path != filepath.Join(roots[i], "README.md") {
				t.Errorf("%s：%s 的会话 %+v", order, ws[i], s)
			}
		}
		recent := []string{filepath.Join(roots[1], "README.md"), filepath.Join(roots[0], "README.md")}
		if !slices.Equal(st.RecentWs, []string{ws[1], ws[0]}) || !slices.Equal(st.Recent, recent) ||
			!slices.Equal(st.CommitMsgs, []string{"乙的提交", "甲的提交"}) || !slices.Equal(st.Windows, ws) {
			t.Errorf("%s：recentWs=%q recent=%q msgs=%q windows=%q", order, st.RecentWs, st.Recent, st.CommitMsgs, st.Windows)
		}
		if want := []workspace.SearchEntry{{Text: "乙搜"}, {Text: "甲搜"}}; !slices.Equal(st.SearchHistory, want) || !slices.Equal(a.St.SearchHistory, b.St.SearchHistory) {
			t.Errorf("%s：最近搜索 %+v，两个窗口 %+v / %+v", order, st.SearchHistory, a.St.SearchHistory, b.St.SearchHistory)
		}
		if !slices.Equal(a.St.CommitMsgs, b.St.CommitMsgs) || !slices.Equal(a.St.Recent, b.St.Recent) {
			t.Errorf("%s：两个窗口手里的不一样：%q / %q", order, a.St.CommitMsgs, b.St.CommitMsgs)
		}
	}

	// 一个窗口：和没有注册表的 App 写出来的一样
	old, dir := t.TempDir(), t.TempDir()
	o, wait := newTestAppIn(t, ws[0], old)
	o.Start()
	wait("状态", func() bool { return idle(o.Repos[0])() })
	o.SaveState()
	o.Close()
	h := newWinHarness(t, dir)
	h.w.Restore(ws[0])
	h.idle()
	h.w.Apps[0].SaveState()
	want, _ := os.ReadFile(filepath.Join(old, workspace.StateFile))
	got, _ := os.ReadFile(filepath.Join(dir, workspace.StateFile))
	if len(want) == 0 || !bytes.Equal(got, want) {
		t.Errorf("一个窗口的 state.json 和以前不一样：\n%s\n以前：\n%s", got, want)
	}
}

// 启动时开回几个窗口：第一个当场开，别的等前一个画完第一帧（Ready）再隔 RestoreGap 开；命令行给的排在最后、
// 错开前台窗口放，开完它在前面；本来就在 windows 里的，开完叫到前面，不然前面是最后一个；打不开的、重复的跳过，不占一次计时；
// 还原中从别处开了的不再开；都开完才调 Restored（main 在这之前攒着外面交进来的路径）。
func TestWindowsRestoreStaggered(t *testing.T) {
	ws, _ := winWorkspaces(t, "甲", "乙", "丙")
	dir := t.TempDir()
	saveWindows(t, dir, ws[0], ws[1])
	h := newWinHarness(t, dir)
	h.stagger()
	restored := 0
	h.w.Restored = func() { restored++ }
	if err := h.w.Restore(ws[2]); err != nil {
		t.Fatal(err)
	}
	a := h.w.Apps[0]
	if !slices.Equal(openPaths(h.w), ws[:1]) || len(h.later) != 0 || !h.w.Restoring() || restored != 0 || h.w.Focused() != a {
		t.Fatalf("Restore 之后只开甲：%v later=%d restored=%d", openPaths(h.w), len(h.later), restored)
	}
	if !slices.Equal(winPaths(h.w), ws) {
		t.Errorf("还原中要记的 windows（开着的 + 没开的）：%v", winPaths(h.w))
	}
	h.tick()
	h.w.Ready(nil)
	if len(h.w.Apps) != 1 || len(h.later) != 0 {
		t.Fatalf("没画完第一帧就开了下一个：%v", openPaths(h.w))
	}
	h.w.Ready(a)
	h.w.Ready(a)
	if len(h.later) != 1 || h.gaps[0] != RestoreGap || len(h.w.Apps) != 1 {
		t.Fatalf("甲画完第一帧：later=%d gaps=%v %v", len(h.later), h.gaps, openPaths(h.w))
	}
	h.tick()
	b := h.w.Focused()
	if !slices.Equal(openPaths(h.w), ws[:2]) || b.WsPath != ws[1] || b.Slot != 2 || h.from[b] != nil || restored != 0 {
		t.Fatalf("隔一会儿开乙：%v from=%v restored=%d", openPaths(h.w), h.from[b], restored)
	}
	h.w.Ready(a) // 早就画过的：不再约
	if len(h.later) != 0 {
		t.Fatal("甲的 Ready 又约了一次")
	}
	h.w.Ready(b)
	h.tick()
	c := h.w.Focused()
	if !slices.Equal(openPaths(h.w), ws) || c.WsPath != ws[2] || h.from[c] != b || h.w.Restoring() || restored != 1 {
		t.Fatalf("命令行给的丙最后开、错开乙：%v from=%v restored=%d", openPaths(h.w), h.from[c], restored)
	}
	h.w.Ready(c)
	if len(h.later) != 0 || restored != 1 {
		t.Errorf("开完了还约：later=%d restored=%d", len(h.later), restored)
	}
	h.idle()
	h.quit()

	// 命令行给的本来就在 windows 里：开完叫到前面；没给：前面是最后一个
	for _, arg := range []string{ws[0], ""} {
		h = newWinHarness(t, dir)
		h.stagger()
		h.w.Restore(arg)
		for len(h.w.Apps) < 3 {
			h.w.Ready(h.w.Focused())
			if len(h.later) != 1 {
				t.Fatalf("命令行给 %q：开了 %v 就停了", arg, openPaths(h.w))
			}
			h.tick()
		}
		want := h.w.Apps[2]
		if arg != "" {
			want = h.w.Apps[0]
		}
		if !slices.Equal(openPaths(h.w), ws) || h.w.Focused() != want || h.w.Restoring() {
			t.Errorf("命令行给 %q：%v focused=%q", arg, openPaths(h.w), h.w.Focused().WsPath)
		}
		h.idle()
		h.quit()
	}

	// 打不开的、重复的跳过，不占一次计时；还原中从别处开了的不再开
	gone := filepath.Join(t.TempDir(), "没有了")
	saveWindows(t, dir, ws[0], gone, ws[0], ws[1], ws[2])
	h = newWinHarness(t, dir)
	h.stagger()
	h.w.Restore("")
	h.w.Open(ws[2], nil)
	if !slices.Equal(openPaths(h.w), []string{ws[0], ws[2]}) || !slices.Equal(winPaths(h.w), []string{ws[0], ws[2], ws[1]}) {
		t.Fatalf("还原中开丙：%v / %v", openPaths(h.w), winPaths(h.w))
	}
	h.w.Ready(h.w.Apps[0])
	h.tick()
	h.w.Ready(h.w.Find(ws[1]))
	h.tick()
	if !slices.Equal(openPaths(h.w), []string{ws[0], ws[2], ws[1]}) || h.w.Restoring() || len(h.gaps) != 2 {
		t.Errorf("跳过打不开的、重复的、开过的：%v gaps=%v", openPaths(h.w), h.gaps)
	}
	h.idle()
}

// 还原中 ⌘Q：没开的也记进 windows（顺序不变），约好的计时器到点也不再开；有要问的、在问的时候不开，
// 取消退出就当没按过：接着开完，存下的 windows 一直是三个（验收 142：下次启动照样都回来）。
func TestWindowsQuitDuringRestore(t *testing.T) {
	ws, _ := winWorkspaces(t, "甲", "乙", "丙")
	dir := t.TempDir()
	saveWindows(t, dir, ws...)
	h := newWinHarness(t, dir)
	h.stagger()
	h.w.Restore("")
	h.idle()
	h.w.Ready(h.w.Apps[0])
	h.quit()
	if len(h.w.Apps) != 0 {
		t.Fatalf("退出后还开着：%v", openPaths(h.w))
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws) {
		t.Fatalf("还原中退出：windows = %q", st.Windows)
	}
	h.tick()
	if len(h.w.Apps) != 0 {
		t.Fatalf("退出了计时器到点还开：%v", openPaths(h.w))
	}

	// 有没保存的：问的时候不开，取消就接着开
	h = newWinHarness(t, dir)
	h.stagger()
	h.w.Restore("")
	a := h.w.Apps[0]
	h.idle()
	h.open(a).Buf.Set("甲改了\n")
	h.w.Ready(a)
	h.quit()
	if a.Leave == nil || !a.Leave.Open {
		t.Fatalf("应问甲：leave=%+v", a.Leave)
	}
	h.tick()
	if len(h.w.Apps) != 1 || len(h.later) != 0 {
		t.Fatalf("问着的时候开了：%v later=%d", openPaths(h.w), len(h.later))
	}
	h.answer(a, 0)
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws) || len(h.later) != 1 || h.quits != 0 {
		t.Fatalf("取消退出：windows = %q later=%d quits=%d", st.Windows, len(h.later), h.quits)
	}
	h.tick()
	h.w.Ready(h.w.Focused())
	h.tick()
	if !slices.Equal(openPaths(h.w), ws) || h.w.Restoring() {
		t.Fatalf("取消后接着开：%v", openPaths(h.w))
	}
	h.idle()
	h.quit()
	h.answer(a, 1)
	if len(h.w.Apps) != 0 || h.quits != 1 {
		t.Fatalf("再退：%v quits=%d", openPaths(h.w), h.quits)
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws) {
		t.Errorf("再退之后 windows = %q", st.Windows)
	}
}

// 还原中关掉唯一开着的窗口：不退出、不开最近的工作区，队列照走（没画完第一帧就关的不用等它）；关掉的下次不开回来；
// 开完了再关最后一个才退出。
func TestWindowsCloseDuringRestore(t *testing.T) {
	ws, _ := winWorkspaces(t, "甲", "乙", "丙")
	dir := t.TempDir()
	saveWindows(t, dir, ws...)
	h := newWinHarness(t, dir)
	h.stagger()
	h.w.Restore("")
	h.idle()
	h.close(h.w.Apps[0]) // 还没画完第一帧
	if len(h.w.Apps) != 0 || h.quits != 0 || !h.w.Restoring() || len(h.later) != 1 {
		t.Fatalf("关掉甲：%v quits=%d restoring=%v later=%d", openPaths(h.w), h.quits, h.w.Restoring(), len(h.later))
	}
	if st := loadWinState(t, dir); !slices.Equal(st.Windows, ws[1:]) {
		t.Errorf("关掉甲之后 windows = %q", st.Windows)
	}
	h.tick()
	b := h.w.Focused()
	if !slices.Equal(openPaths(h.w), ws[1:2]) || b.Slot != 1 {
		t.Fatalf("接着开乙：%v slot=%d", openPaths(h.w), b.Slot)
	}
	h.idle()
	h.w.Ready(b)
	h.close(b) // 画完了才关，计时器已经约了
	if len(h.w.Apps) != 0 || h.quits != 0 || len(h.later) != 1 {
		t.Fatalf("关掉乙：%v quits=%d later=%d", openPaths(h.w), h.quits, len(h.later))
	}
	h.tick()
	if !slices.Equal(openPaths(h.w), ws[2:]) || h.w.Restoring() || h.quits != 0 {
		t.Fatalf("开完丙：%v quits=%d", openPaths(h.w), h.quits)
	}
	h.idle()
	h.close(h.w.Apps[0])
	if len(h.w.Apps) != 0 || h.quits != 1 {
		t.Errorf("开完了关最后一个：%v quits=%d", openPaths(h.w), h.quits)
	}
}

// 只开一个窗口（最常见的）：当场开完、调 Restored，不约计时器、不等第一帧；没记 windows 开最近的也一样。
func TestWindowsRestoreSingle(t *testing.T) {
	ws, _ := winWorkspaces(t, "甲", "乙")
	dir := t.TempDir()
	for i, arg := range []string{ws[0], "", ws[1]} {
		if i == 2 {
			saveWindows(t, dir, ws[1])
		}
		h := newWinHarness(t, dir)
		h.stagger()
		restored := 0
		h.w.Restored = func() { restored++ }
		h.w.Restore(arg)
		h.w.Ready(h.w.Apps[0])
		if len(h.w.Apps) != 1 || h.w.Restoring() || restored != 1 || len(h.gaps) != 0 {
			t.Errorf("命令行给 %q：%v restoring=%v restored=%d gaps=%v", arg, openPaths(h.w), h.w.Restoring(), restored, h.gaps)
		}
		h.idle()
		h.quit()
	}
}

// 还原间隔的默认值是按实测定的：前一个窗口启动时多拿的 drawable 第一帧后 ~2.5 s 才还，
// 间隔 2 s 两个窗口的启动峰值还是 322，2.5 s 才降到 259。要改先重新量两个窗口的启动峰值（AGENTS 有量法），再改这里。
func TestRestoreGapDefault(t *testing.T) {
	if RestoreGap != 2500*time.Millisecond {
		t.Errorf("RestoreGap = %v，实测定的是 2.5s", RestoreGap)
	}
}

// 窗口位置存在状态目录的 window-state.json 里（格式同 mygo 默认的那份）：来回一趟不变，没有文件是 nil、不报错，
// 坏了报错（mygo 当没有），目录不在就建。
func TestWindowStateStore(t *testing.T) {
	dir := t.TempDir()
	s := WindowStates(dir)
	if st, err := s.LoadWindowStates(); st != nil || err != nil {
		t.Fatalf("空目录：%v %v", st, err)
	}
	want := map[string]mygo.WindowState{
		"main":     {X: 10, Y: 30, Width: 1440, Height: 880},
		"main-2":   {X: 34, Y: 54, Width: 1440, Height: 880, Maximized: true},
		"settings": {X: 200, Y: 100, Width: 600, Height: 480, FullScreen: true},
	}
	if err := s.SaveWindowStates(want); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "window-state.json"))
	if err != nil || !bytes.Contains(b, []byte(`"maximized": true`)) || !bytes.Contains(b, []byte(`"fullScreen": true`)) ||
		bytes.Count(b, []byte(`"maximized"`)) != 1 || !bytes.HasPrefix(b, []byte("{\n  \"main\": {\n    \"x\": 10,")) {
		t.Fatalf("window-state.json：%v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "window-state.json.tmp")); err == nil {
		t.Error("留下了 .tmp")
	}
	if got, err := s.LoadWindowStates(); err != nil || !maps.Equal(got, want) {
		t.Errorf("读回来：%v %v", got, err)
	}
	os.WriteFile(filepath.Join(dir, "window-state.json"), []byte("{"), 0o644)
	if st, err := s.LoadWindowStates(); st != nil || err == nil {
		t.Errorf("坏了的文件：%v %v", st, err)
	}
	sub := WindowStates(filepath.Join(dir, "新目录"))
	if err := sub.SaveWindowStates(want); err != nil {
		t.Fatal(err)
	}
	if got, _ := sub.LoadWindowStates(); !maps.Equal(got, want) {
		t.Errorf("新目录里：%v", got)
	}
}
