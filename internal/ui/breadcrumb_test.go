package ui

import (
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
)

// 面包屑挤不下时一步步让（crumbFit）：先缩目录段，再把目录全收进"…"，再缩文件名，最后才缩仓库名；
// 窗口多窄（主窗口最窄 960）都不压到居中的"快速打开"上。
func TestBreadcrumbKeepsRepoName(t *testing.T) {
	r0 := testutil.NewRepo(t, "corp-docs-workspace")
	r0.Write("projects/reporting-system/services/scheduler/python-components-inventory.csv", "a,b\n")
	r0.Commit("初始提交")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenFile(r, "projects/reporting-system/services/scheduler/python-components-inventory.csv", true)
	wait("读文件", func() bool { return a.CurrentDoc() != nil && !a.CurrentDoc().Loading })

	const (
		repoLabel = "位置 corp-docs-workspace"
		dirLabel  = "位置 corp-docs-workspace/projects"
		fileLabel = "位置 corp-docs-workspace/projects/reporting-system/services/scheduler/python-components-inventory.csv"
	)
	type boxes struct {
		repo, dir, file, quick wui.Rect
		folded                 bool // 目录全收进了"…"
	}
	at := func(w int) (b boxes) {
		tt.SetSize(w, 880)
		tt.Frame()
		for i, s := range []string{repoLabel, fileLabel, "快速打开"} {
			rc, ok := tt.Find(s)
			if !ok {
				t.Fatalf("宽 %d 时标题栏里没有 %q", w, s)
			}
			*[]*wui.Rect{&b.repo, &b.file, &b.quick}[i] = rc
		}
		b.dir, b.folded = tt.Find(dirLabel)
		b.folded = !b.folded
		if _, ok := tt.Find("位置 省略的目录"); !ok {
			t.Fatalf("宽 %d 时没有收起来的目录", w)
		}
		t.Logf("宽 %d：仓库 %.0f 目录 %.0f（全收起来 %v）文件 %.0f（右边 %.0f），快速打开从 %.0f 起",
			w, b.repo.W, b.dir.W, b.folded, b.file.W, b.file.X+b.file.W, b.quick.X)
		return
	}
	wide := at(2400)
	if wide.folded {
		t.Fatal("2400 宽的窗口不该把目录全收起来")
	}
	for _, w := range []int{1600, 1440, 1280, 1180, 1100, 1000, 960} { // 主窗口最窄 960
		b := at(w)
		if right := b.file.X + b.file.W; right > b.quick.X {
			t.Errorf("宽 %d 时文件段压到快速打开上：右边 %.0f，快速打开从 %.0f 起", w, right, b.quick.X)
		}
		if b.file.W < wide.file.W-0.5 && !b.folded {
			t.Errorf("宽 %d 时目录还没全收起来就先缩了文件段：%.0f → %.0f", w, wide.file.W, b.file.W)
		}
		if b.repo.W < wide.repo.W-0.5 && b.file.W > crumbFileMin+0.5 {
			t.Errorf("宽 %d 时文件段还有 %.0f 就先缩了仓库段：%.0f → %.0f", w, b.file.W, wide.repo.W, b.repo.W)
		}
		if w >= 1180 && b.repo.W < wide.repo.W-0.5 {
			t.Errorf("宽 %d 时仓库段被挤窄了：%.0f → %.0f", w, wide.repo.W, b.repo.W)
		}
	}
}
