package ui

import (
	"fmt"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// TestDialogFrame 看对话框框：关着不画；头上的图标块按 Kind 上色；居中略偏上、宽度照给的；
// ⏎ 是主按钮（只一次），Esc、点遮罩把 open 置 false；底部按钮在提示前面（按名字找先找到按钮）。
func TestDialogFrame(t *testing.T) {
	open := false
	kind := dlgDanger
	var th *wui.Theme
	var ctx *wui.Context
	var panel wui.Handle // 跨帧量框的位置要用 Handle，Element 只在建的那一帧里有效
	enter, clicked, cancel := 0, 0, 0
	tt := wui.NewTester(func(c *wui.Context) {
		th, ctx = c.Theme(), c
		wui.Column(c).Grow(1).Children(func() { wui.Text(c, "底下的界面") })
		o := dialogOpts{Kind: kind, Title: "删除 x？", Note: "删了找不回来。", Repo: "demo", Width: dlgConfirm, Hint: "删除",
			Enter: func() { enter++ }}
		dialogFrame(c, &open, o, nil, func() {
			if dlgButton(c, "取消", btnPlain, false, false) {
				cancel++
				open = false
			}
			if dlgButton(c, "删除", btnDanger, false, true) {
				clicked++
			}
		}).Bind(&panel)
	}, 1000, 700)
	tt.Frame()
	if tt.HasText("删除 x？") || tt.HasText("Esc") {
		t.Fatalf("关着也画了：%q", tt.Texts())
	}

	open = true
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"删除 x？", "删了找不回来。", "demo", "Esc", "⏎"} {
		mustText(t, tt, s)
	}
	b := panel.Bounds(ctx)
	if b.W != dlgConfirm || b.X+b.W/2 < 499 || b.X+b.W/2 > 501 {
		t.Errorf("宽 %v、中线 %v，要 %v 宽居中", b.W, b.X+b.W/2, dlgConfirm)
	}
	if b.Y < 700*0.15 || b.Y > 700*0.4 {
		t.Errorf("顶边在 %v，要在窗高的三成上下", b.Y)
	}
	// 按名字找"删除"找到的是按钮，不是左下角的"⏎ 删除"
	mustClick(t, tt, "删除")
	if clicked != 1 || !open {
		t.Fatalf("点删除：clicked=%d open=%v", clicked, open)
	}
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if enter+clicked != 2 {
		t.Errorf("⏎ 应按一次主按钮：enter=%d clicked=%d", enter, clicked)
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if open {
		t.Error("Esc 没关")
	}
	open = true
	tt.Frame()
	tt.ClickAt(10, 10)
	tt.Frame()
	if open {
		t.Error("点遮罩没关")
	}
	open = true
	tt.Frame()
	mustClick(t, tt, "取消")
	if open || cancel != 1 {
		t.Errorf("点取消：open=%v cancel=%d", open, cancel)
	}

	// 图标块的颜色：问的、提示是强调色，警告、危险、出错是红的，表单没有
	for k, want := range map[dlgKind]wui.Color{dlgInfo: th.Accent, dlgQuestion: th.Accent, dlgWarning: th.Danger, dlgDanger: th.Danger, dlgError: th.Danger} {
		if ic, fg := dlgIcon(th, k); ic == nil || fg != want {
			t.Errorf("Kind %d：图标 %v 颜色 %v，要 %v", k, ic != nil, fg, want)
		}
	}
	if ic, _ := dlgIcon(th, dlgPlain); ic != nil {
		t.Error("表单不该有图标块")
	}
}

// TestGoToHunk 看 Diff 标签条上的上 / 下一处改动：到头灰掉（点了不动），底栏"第 i / n 处改动"跟着光标；命令面板的也能用。
func TestGoToHunk(t *testing.T) {
	r := testutil.NewRepo(t, "hunks")
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("第 %d 行", i))
	}
	r.Write("a.txt", strings.Join(lines, "\n")+"\n")
	r.Commit("初始")
	for _, i := range []int{5, 30, 55} {
		lines[i-1] = fmt.Sprintf("改了第 %d 行", i)
	}
	r.Write("a.txt", strings.Join(lines, "\n")+"\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, r.Root), "")
	repo := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(repo, app.WhyManual)
	wait("git status", idle(repo))
	a.OpenDiff(repo, git.Change{Kind: git.Unstaged, Code: 'M', Path: "a.txt"}, true)
	wait("a.txt 的 diff", func() bool { return diffDoc(a, "") != nil })
	d := diffDoc(a, "")
	if n := len(d.DiffSt.Hunks()); n != 3 {
		t.Fatalf("a.txt 应有 3 处改动，有 %d", n)
	}
	prev := app.Tip(i18n.T("上一处改动"), "diff.prevHunk")
	next := app.Tip(i18n.T("下一处改动"), "diff.nextHunk")
	at := func(want string) {
		t.Helper()
		tt.Frame()
		mustText(t, tt, want)
	}
	at(i18n.Tf("%d 处改动", 3)) // 光标在第一处之前
	if app.CanGoToHunk(d, -1) || !app.CanGoToHunk(d, 1) {
		t.Fatal("在第一处之前：上一处该灰、下一处该能点")
	}
	mustClick(t, tt, next)
	at(i18n.Tf("第 %d / %d 处改动", 1, 3))
	mustClick(t, tt, prev) // 第一处里面：没有上一处了
	at(i18n.Tf("第 %d / %d 处改动", 1, 3))
	mustClick(t, tt, next)
	mustClick(t, tt, next)
	at(i18n.Tf("第 %d / %d 处改动", 3, 3))
	if app.CanGoToHunk(d, 1) {
		t.Error("最后一处：下一处该灰")
	}
	mustClick(t, tt, next)
	at(i18n.Tf("第 %d / %d 处改动", 3, 3))
	mustClick(t, tt, prev)
	at(i18n.Tf("第 %d / %d 处改动", 2, 3))
	c := app.Find("diff.prevHunk")
	if c.Run == nil || !c.Local || !c.Enabled(a) {
		t.Fatalf("diff.prevHunk 要给命令面板（Run）、键留给编辑器（Local）：%+v", c)
	}
	c.Run(a)
	at(i18n.Tf("第 %d / %d 处改动", 1, 3))
	accessCheck(t, tt, "Diff 标签条")
}

// 真窗口里记过一笔：在窗口左边点遮罩，"新建分支"没关、改名的关了。换成 dialogFrame 后两种开法（⇧⌘B、命令）、
// 左边和右上角点遮罩都关，关了以后状态清空（再开名字是空的）。
func TestBranchDialogBackdrop(t *testing.T) {
	r := testutil.NewRepo(t, "bd")
	r.Write("a.txt", "a\n")
	r.Commit("初始")
	a, tt, wait := newUI(t, testutil.Workspace(t, r.Root), "")
	repo := a.Repos[0]
	a.RefreshRepo(repo, app.WhyManual)
	wait("git status", idle(repo))
	a.ShowSidebar(workspace.ModeGit)
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	for i, at := range [][2]float32{{40, 440}, {1400, 60}, {720, 860}, {20, 440}} {
		if i == 0 {
			tt.Key(wui.Alt|wui.Shift|wui.Cmd, wui.KeyB)
		} else {
			a.Exec("git.newBranch")
		}
		tt.Frame()
		tt.Frame()
		if !a.Branch.Open {
			t.Fatalf("第 %d 次没开", i)
		}
		mustText(t, tt, i18n.Tf("在 %s 里从 %s 新建并切换过去", repo.Name, repo.Status.Branch))
		tt.Type("x")
		tt.Frame()
		tt.ClickAt(at[0], at[1])
		tt.Frame()
		if a.Branch.Open || a.Branch.Name != "" || tt.HasText(i18n.T("分支名")) {
			t.Errorf("点遮罩 %v 没关：open=%v name=%q", at, a.Branch.Open, a.Branch.Name)
		}
	}
}
