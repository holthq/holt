package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 终端：工作目录是仓库根，中文回显正常，vim 进得去也退得出。只在用户的仓库里跑 pwd / echo / vim :q!。
func TestTerminalChineseAndVim(t *testing.T) {
	roots, err := workspace.Load(workspace.DefaultPath)
	if err != nil {
		t.Skipf("没有工作区文件: %v", err)
	}
	root := roots[0]
	term, err := terminal.New(terminal.Options{Dir: root, Font: terminal.Font{Family: "monospace", Size: 13}})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	tt := wui.NewTester(func(c *wui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 900, 500)

	tt.Type("pwd")
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, term, "\n"+root, 15*time.Second)

	tt.Type("echo 中文测试")
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, term, "\n中文测试", 15*time.Second)

	tt.Type("vim")
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, term, "IMproved", 15*time.Second)
	tt.Type(":q!")
	tt.Key(0, wui.KeyEnter)
	// 等 vim 真的退出、shell 回到主屏幕，再敲下一条命令；
	// 否则后面的按键会被 vim 读进它的输入缓冲一起丢掉。
	testutil.WaitTermGone(t, tt, term, "IMproved", 15*time.Second)
	tt.Type("echo 回来了")
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, term, "\n回来了", 15*time.Second)
	if strings.Contains(term.Text(), "IMproved") {
		t.Errorf("退出 vim 后屏幕上还有 vim 的内容:\n%s", term.Text())
	}
}

// 终端里 ⌘点击：仓库里的 path:行 在编辑器里固定打开到那一行，网址交给系统浏览器（Tester 只记下来）。
// 在临时仓库里起真 shell，echo 一行路径和网址；先按住 ⌘ 竖着扫，靠 HoveredLink 找到那一行和格子宽。
func TestTerminalLinks(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.St.BottomBar, a.BottomTab = true, app.BottomTerminal
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	term := activeTerm(t, a)
	testutil.WaitTermReady(t, tt, term, 400*time.Millisecond, 15*time.Second)
	view, ok := tt.Find("Terminal")
	if !ok {
		t.Fatalf("底栏里没有终端；texts=%q", tt.Texts())
	}
	tt.ClickAt(view.X+view.W/2, view.Y+view.H-20)
	tt.Type("echo a.txt:20 https://example.org")
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, term, "\na.txt:20 https://example.org", 15*time.Second)
	tt.Frame()

	// 输出那一行从第 0 列开始；在第 3 列竖着扫到最后一个是路径的位置（命令那一行在它上面）
	var at wui.Rect
	var y float32
	for yy := view.Y + 2; yy < view.Y+view.H; yy += 3 {
		tt.Move(view.X+12+20, yy)
		tt.HoldModifiers(wui.Cmd)
		if link, path, rect := term.HoveredLink(); link == "a.txt:20" && path {
			at, y = rect, yy
		}
		tt.HoldModifiers(0)
	}
	if at.W == 0 {
		t.Fatalf("按住 ⌘ 扫不到 a.txt:20；屏幕是:\n%s", term.Text())
	}
	cell := at.W / float32(len("a.txt:20"))

	tt.ClickAtWith(wui.Cmd, view.X+at.X+3.5*cell, y)
	tt.Frame()
	d := a.CurrentDoc()
	if d == nil || d.Rel != "a.txt" || !d.Pinned {
		t.Fatalf("⌘点 a.txt:20 没固定打开 a.txt：%+v", d)
	}
	wait("读 a.txt", func() bool { return d.Buf != nil })
	tt.Frame()
	if pos, _ := d.Cursor(); pos.Line != 19 {
		t.Fatalf("光标在第 %d 行，应在第 20 行", pos.Line+1)
	}
	if len(tt.OpenedURLs()) != 0 {
		t.Fatalf("路径不该交给系统：%q", tt.OpenedURLs())
	}

	// 网址从第 9 列开始
	tt.ClickAtWith(wui.Cmd, view.X+at.X+12.5*cell, y)
	tt.Frame()
	if u := tt.OpenedURLs(); len(u) != 1 || u[0] != "https://example.org" {
		t.Fatalf("⌘点网址交给系统的是 %q", u)
	}
	if a.CurrentDoc() != d {
		t.Fatal("⌘点网址不该换标签")
	}
}

// activeTerm 是当前仓库当前终端标签里有键盘的那一格的终端：底栏画过一帧（shell 懒启动）后才有。
func activeTerm(t *testing.T, a *app.App) *terminal.Terminal {
	t.Helper()
	_, _, p := a.ActivePane()
	if p == nil || p.Term == nil {
		var err error
		if p != nil {
			err = p.Err
		}
		t.Fatalf("终端没起来：%v", err)
	}
	return p.Term
}
