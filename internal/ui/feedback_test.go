package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 窗口矮、Git 栏行多时侧栏的底边在状态栏上面：原来段切换下面那列是 Fill()（高 100%），比剩下的地方高出段切换那一行，
// Git 栏滚到底最后的提交框、文件页底部的筛选框都压在状态栏下面。
func TestSidebarFits(t *testing.T) {
	repo := testutil.NewRepo(t, "tall")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	for i := range 60 {
		repo.Write(fmt.Sprintf("f%02d.txt", i), "x\n")
	}
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, "")
	a.St.BottomBar = false // 不起 shell
	const h = 480
	tt.SetSize(1000, h)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	status := float32(h - statusHeight - cardGap) // 状态栏连着卡片底下的缝
	fits := func(what, s string) {
		t.Helper()
		rc, ok := tt.Find(s)
		if !ok || rc.H <= 0 {
			t.Fatalf("%s：没有 %q: %q", what, s, tt.Texts())
		}
		if rc.Y+rc.H > status {
			t.Errorf("%s：%q 的底边 %.1f 在状态栏（%.1f）下面", what, s, rc.Y+rc.H, status)
		}
	}

	a.ShowSidebar(workspace.ModeFiles)
	tt.Frame()
	fits("文件页", i18n.T("筛选文件"))

	a.ShowSidebar(workspace.ModeGit)
	tt.Frame()
	commit := i18n.Tf("提交 %s 的改动…", r.Name)
	tt.Move(100, 200)
	tt.Scroll(100, 200, 0, 100000)
	wait("Git 栏滚到底", func() bool { rc, ok := tt.Find(commit); return ok && rc.H > 0 })
	fits("Git 栏滚到底", commit)
	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" { // 矮窗口里 Git 栏滚到底：提交框在状态栏上面
		savePNG(t, filepath.Join(dir, "fix-gitbar-bottom.png"), material(tt.Image(), false))
	}
}

// 标签多了标签条横着滚：mygo 自己的 6 pt 滚动条（原来盖在标签的字上）裁掉了，指针在标签条上时底边有一条 2 pt 的细条；
// 换了当前标签（新开的、后退回去的）会滚到看得见。
func TestTabStripScroll(t *testing.T) {
	repo := testutil.NewRepo(t, "strip")
	for i := range 20 {
		repo.Write(fmt.Sprintf("file-%02d.go", i), "package x\n")
	}
	repo.Commit("初始提交")
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, "")
	a.St.BottomBar = false // 不起 shell
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	for i := range 20 {
		a.OpenFile(r, fmt.Sprintf("file-%02d.go", i), true)
	}
	wait("20 个标签", func() bool { return len(a.Docs) == 20 && !a.Docs[19].Loading })
	tt.Frame()
	tabBox := func(name string) image.Rectangle {
		rc, _ := tt.Find(i18n.Tf("标签 %s", name))
		return image.Rect(int(rc.X), int(rc.Y), int(rc.X+rc.W), int(rc.Y+rc.H))
	}
	last := tabBox("file-19.go")
	if last.Empty() || !tabBox("file-00.go").Empty() || a.Grp.TabStrip.MaxX <= 0 {
		t.Fatalf("新开的最后一个标签应滚进视图、第一个滚出去: 最后 %v 第一 %v MaxX %.0f", last, tabBox("file-00.go"), a.Grp.TabStrip.MaxX)
	}

	// 指针在标签条上：底边 2 pt 是细条（一长段深一点的像素），往上 3–9 pt（原来 6 pt 滚动条的地方）没有
	bottom := (last.Min.Y+last.Max.Y)/2 + headerHeight/2
	run := func(y int) int {
		img, longest, n := tt.Image(), 0, 0
		for x := 300; x < last.Max.X; x++ {
			if c := img.RGBAAt(x, y); c.R < 205 && c.G < 205 && c.B < 210 {
				n++
				longest = max(longest, n)
			} else {
				n = 0
			}
		}
		return longest
	}
	if n := run(bottom - 1); n > 40 {
		t.Errorf("指针不在标签条上时不该有细条，底边有 %d px 深色", n)
	}
	tt.Move(float32(last.Min.X-150), float32(bottom-headerHeight/2))
	tt.Frame()
	if n := run(bottom - 1); n < 100 {
		t.Errorf("指针在标签条上时底边应有细条，只有 %d px 深色", n)
	}
	for y := bottom - 9; y < bottom-3; y++ {
		if n := run(y); n > 40 {
			t.Errorf("标签条底边往上 %d pt 有 %d px 深色：mygo 的滚动条没裁掉", bottom-y, n)
		}
	}
	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" { // 标签挤不下、指针在标签条上：底边的细条
		savePNG(t, filepath.Join(dir, "fix-tabstrip.png"), material(tt.Image(), false))
	}

	// 换到第一个标签（后退、命令面板这样不点标签换过去的）：滚回最左
	a.SelectDoc(0)
	tt.Frame()
	if a.DocSel != 0 || tabBox("file-00.go").Empty() || a.Grp.TabStrip.X != 0 {
		t.Errorf("换到第一个标签后它应滚进视图: 当前 %d 框 %v X %.0f", a.DocSel, tabBox("file-00.go"), a.Grp.TabStrip.X)
	}
}

// 紧贴标题栏下面的元素（侧栏的 文件 | 搜索 | Git、标签条上的标签、右栏的 当前仓库 | 所有仓库）提示往下弹，
// 不盖住红绿灯和标题栏；每一段有自己的提示（带快捷键）。靠下的元素（Git 栏分组标题上的按钮）照旧往上弹。
func TestTipsBelowTitleBar(t *testing.T) {
	repo := testutil.NewRepo(t, "tips")
	repo.Write("src/deep/a.txt", "a\n")
	repo.Commit("初始提交")
	repo.Write("new.txt", "没跟踪\n")
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, "")
	a.St.BottomBar = false // 不起 shell
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	d := a.OpenFile(r, "src/deep/a.txt", true)
	wait("读 a.txt", func() bool { return !d.Loading })
	a.UseRepo(r)
	wait("历史", func() bool { return r.Hist.Loaded })

	// hover 把指针停在 anchor（Find 得到的字或名字）上，等提示出来，返回两者的框
	hover := func(anchor, tip string) (wui.Rect, wui.Rect) {
		t.Helper()
		tt.Move(0, 0)
		tt.Advance(time.Second) // 上一个提示收掉、过了"接着就出"的 0.4 s
		tt.Frame()
		ar, ok := tt.Find(anchor)
		if !ok || ar.W <= 0 {
			t.Fatalf("没有 %q: %q", anchor, tt.Texts())
		}
		tt.Move(ar.X+min(ar.W/2, 20), ar.Y+ar.H/2)
		tt.Frame()
		tt.Advance(700 * time.Millisecond)
		tt.Frame()
		tr, ok := tt.Find(tip)
		if !ok || tr.W <= 0 {
			t.Fatalf("停在 %q 上没有提示 %q: %q", anchor, tip, tt.Texts())
		}
		return ar, tr
	}
	below := func(anchor, tip string) {
		t.Helper()
		ar, tr := hover(anchor, tip)
		if tr.Y < ar.Y+ar.H || tr.Y < barHeight {
			t.Errorf("%q 的提示 %q 应在它下面（标题栏下沿 %d）：元素 %v 提示 %v", anchor, tip, barHeight, ar, tr)
		}
	}

	a.ShowSidebar(workspace.ModeFiles)
	tt.Frame()
	below(i18n.T("文件"), app.Tip(i18n.T("文件"), "view.files"))
	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" { // 侧栏"文件"那段的提示在它下面、红绿灯露着
		savePNG(t, filepath.Join(dir, "fix-tip-below.png"), material(tt.Image(), false).SubImage(image.Rect(0, 0, 480, 140)))
	}
	below(i18n.T("搜索"), app.Tip(i18n.T("搜索"), "view.search"))
	below("Git", app.Tip("Git", "view.git"))
	below(i18n.Tf("标签 %s", "a.txt"), "src/deep/a.txt")
	// 右栏的 当前仓库 | 所有仓库 在卡片头下面一行，提示照常在上面，只要不进标题栏
	if ar, tr := hover(i18n.T("所有仓库"), i18n.T("所有仓库：各仓库的提交按时间合并（否则只看当前仓库）")); tr.Y < barHeight {
		t.Errorf("%q 的提示盖到了标题栏（下沿 %d）：元素 %v 提示 %v", i18n.T("所有仓库"), barHeight, ar, tr)
	}

	a.ShowSidebar(workspace.ModeGit)
	tt.Frame()
	label := i18n.T("暂存所有未跟踪文件")
	ar, tr := hover(label, i18n.T("暂存所有未跟踪文件（git add）"))
	if tr.Y+tr.H > ar.Y {
		t.Errorf("Git 栏里 %q 的提示应照旧在上面：元素 %v 提示 %v", label, ar, tr)
	}
}
