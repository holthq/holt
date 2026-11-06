package ui

import (
	"fmt"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"

	wui "github.com/egoist/mygo/ui"
)

const bigPin = "big（仓库，钉在顶上）"

type appUI struct {
	a  *app.App
	tt *wui.Tester
}

func (u *appUI) frames(n int) {
	for range n {
		u.tt.Frame()
	}
}

// pinTree 是一个 90 个文件的仓库，文件树读好了。
func pinTree(t *testing.T) (*appUI, func(int)) {
	t.Helper()
	big := testutil.NewRepo(t, "big")
	for i := range 90 {
		big.Write(fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	big.Commit("init")
	a, tt, wait := newUI(t, testutil.Workspace(t, big.Root), "")
	a.ShowSidebar(workspace.ModeFiles)
	r := a.Repos[0]
	if !r.Expanded {
		a.ToggleRepo(r)
	}
	a.EnsureFiles(r)
	wait("文件", func() bool { return r.Files.Loaded && tt.HasText("f00.txt") })
	u := &appUI{a, tt}
	return u, func(i int) {
		a.FileList.ScrollTo(i, wui.Start)
		u.frames(4)
	}
}

// 选中的行滚到列表上面（还在列表多建的那两行里）时吸顶栈还在：原来按"它的顶在栈底上面"算盖住了，整个栈不画。
func TestStickyKeepsOverRowsAbove(t *testing.T) {
	u, scroll := pinTree(t)
	scroll(40)
	for _, sel := range []int{36, 37, 38} {
		u.a.FileSel = sel
		u.frames(3)
		if _, ok := u.tt.Find(bigPin); !ok {
			first, _ := u.a.FileList.Visible()
			t.Fatalf("选中第 %d 行（最上面看得见的是第 %d 行）：吸顶栈没了", sel, first)
		}
	}
}

// 滚轮滚过选中的行：一路都钉着（只有光标刚走到的行才让位）；↑ 走到栈底下的行还是让，光标看得见。
func TestStickyStaysWhileScrollingPastSel(t *testing.T) {
	u, scroll := pinTree(t)
	scroll(30)
	u.a.FileSel = 36
	u.frames(3)
	b, ok := u.tt.Find(bigPin)
	if !ok {
		t.Fatal("滚到中间应该钉着")
	}
	x, y := b.X+40, b.Y+200
	u.tt.Move(x, y)
	for range 40 {
		u.tt.Scroll(x, y, 0, 6)
		u.tt.Frame()
		if _, ok := u.tt.Find(bigPin); !ok {
			first, _ := u.a.FileList.Visible()
			t.Fatalf("滚到第 %d 行时吸顶栈没了（选中第 36 行）", first)
		}
	}
	if first, _ := u.a.FileList.Visible(); first <= 36 {
		t.Fatalf("只滚到第 %d 行，没滚过选中的行", first)
	}
	// 光标走到栈底下的那行：让位；再滚一下又钉上，哪怕那行还在栈底下
	scroll(50)
	u.a.FileSel = 50
	u.frames(4)
	if _, ok := u.tt.Find(bigPin); ok {
		t.Fatal("光标走到被盖住的行应该让位")
	}
	u.tt.Scroll(x, y, 0, 6)
	u.frames(4)
	if _, ok := u.tt.Find(bigPin); !ok {
		t.Fatal("滚动之后应该又钉上")
	}
}

// 真的按 ↑ 走上去：列表滚到光标那行，那行在栈底下时栈让位，光标看得见。
func TestStickyYieldsToArrowUp(t *testing.T) {
	u, scroll := pinTree(t)
	scroll(40)
	row := func(i int) string { return fmt.Sprintf("f%02d.txt", i-1) } // 第 0 行是仓库
	if err := clickRow(u.tt, row(45)); err != nil {
		t.Fatal(err)
	}
	u.frames(3)
	sel := u.a.FileSel
	for sel > 30 {
		u.tt.Key(0, wui.KeyUp)
		u.frames(3)
		if u.a.FileSel != sel-1 {
			t.Fatalf("↑ 之后光标在第 %d 行，应该在第 %d 行", u.a.FileSel, sel-1)
		}
		sel = u.a.FileSel
		pin, pinned := u.tt.Find(bigPin)
		if r := u.a.TreeSpans[sel]; pinned && r.Y < pin.Y+pin.H-0.5 {
			t.Fatalf("光标那行（第 %d 行，%v）被钉着的仓库行（%v）盖住了", sel, r, pin)
		}
	}
}

// 文件树第一次画就滚在中间（启动时侧栏不在文件树，跟随把它滚到了当前文件）：仓库行没画过也钉上，量到真的高。
func TestStickyWithoutRootRowDrawn(t *testing.T) {
	u, _ := pinTree(t)
	a := u.a
	a.ShowSidebar(workspace.ModeGit)
	u.frames(3)
	a.TreeRootH = 0
	a.Reveal(a.Repos[0], "f80.txt")
	a.ShowSidebar(workspace.ModeFiles)
	u.frames(6)
	if _, ok := u.tt.Find(bigPin); !ok {
		t.Fatal("仓库行没画过时应该也钉着")
	}
	if a.TreeRootH == treeRowH {
		t.Fatalf("仓库行的高还是估计的 %v", a.TreeRootH)
	}
}
