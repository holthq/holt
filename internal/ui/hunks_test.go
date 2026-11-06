package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// 临时仓库上走一遍 Diff 里的块级操作：J / K 跳改动，光标所在那块的"暂存这一块"只暂存它、
// 已暂存的 diff 里"取消暂存这一块"、确认后"丢弃这一块"，⌥J / ⌥K 跳到下一个 / 上一个改动的文件。
func TestHunkOps(t *testing.T) {
	repo := testutil.NewRepo(t, "hunks")
	lines := testutil.DemoLines()
	repo.Write("h.txt", strings.Join(lines, "\n")+"\n")
	repo.Write("z.txt", "z\n")
	repo.Commit("初始提交")
	lines[2], lines[29] = "第 3 行（改过）", "第 30 行（改过）"
	repo.Write("h.txt", strings.Join(lines, "\n")+"\n")
	repo.Write("z.txt", "z（改过）\n")
	ws := testutil.Workspace(t, repo.Root)

	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	h := git.Change{Kind: git.Unstaged, Code: 'M', Path: "h.txt"}
	a.OpenDiff(r, h, true)
	wait("h.txt 的 diff", func() bool { return diffDoc(a, "") != nil })
	d := diffDoc(a, "")
	if n := len(d.DiffSt.Hunks()); n != 2 {
		t.Fatalf("h.txt 应有 2 块改动，有 %d", n)
	}

	// 点进新的一侧，J / K 在两块之间跳
	focusNewSide(t, tt)
	d.DiffSt.GoToHunk(0)
	tt.Frame()
	tt.Key(0, wui.KeyJ)
	tt.Frame()
	if i, in := d.DiffSt.CurrentHunk(); i != 1 || !in {
		t.Fatalf("J 后应在第 2 块: %d %v", i, in)
	}

	tt.Key(0, wui.KeyK)
	tt.Frame()
	if i, _ := d.DiffSt.CurrentHunk(); i != 0 {
		t.Fatalf("K 后应回第 1 块: %d", i)
	}
	if got := d.DiffSt.New.String(); strings.Contains(got, "j") || strings.Contains(got, "k") {
		t.Fatal("J / K 打进只读的 diff 里了")
	}

	// 光标在第 1 块：只暂存它
	mustText(t, tt, "暂存这一块")
	mustText(t, tt, "丢弃这一块")
	if shows(tt, "取消暂存这一块") {
		t.Error("未暂存的 diff 不该有取消暂存这一块")
	}
	mustClick(t, tt, "暂存这一块")
	wait("暂存第 1 块", func() bool { return idle(r)() && r.Status.Staged == 1 })
	cached := repo.Git("diff", "--cached", "--", "h.txt")
	if !strings.Contains(cached, "+第 3 行（改过）") || strings.Contains(cached, "第 30 行（改过）") {
		t.Fatalf("暂存区里应只有第 1 块:\n%s", cached)
	}
	if !strings.Contains(a.Output[len(a.Output)-1].Title, "git apply --cached -") {
		t.Errorf("输出栏没记这条命令: %q", a.Output[len(a.Output)-1].Title)
	}
	wait("未暂存的 diff 重读", func() bool { d := diffDoc(a, ""); return d != nil && len(d.DiffSt.Hunks()) == 1 })

	// 已暂存的 diff：取消暂存这一块
	a.OpenDiff(r, git.Change{Kind: git.Staged, Code: 'M', Path: "h.txt"}, true)
	wait("已暂存的 diff", func() bool { d := diffDoc(a, ""); return d != nil && d.Change.Kind == git.Staged })
	d = diffDoc(a, "")
	focusNewSide(t, tt)
	d.DiffSt.GoToHunk(0)
	tt.Frame()
	if shows(tt, "暂存这一块") || shows(tt, "丢弃这一块") {
		t.Error("已暂存的 diff 不该有暂存 / 丢弃这一块")
	}
	mustClick(t, tt, "取消暂存这一块")
	wait("取消暂存", func() bool { return idle(r)() && r.Status.Staged == 0 })
	if out := repo.Git("diff", "--cached"); out != "" {
		t.Fatalf("取消暂存后暂存区应是空的:\n%s", out)
	}

	// 丢弃第 2 块：先问，取消不动，确认后工作区恢复
	a.OpenDiff(r, h, true)
	wait("h.txt 的 diff", func() bool {
		d := diffDoc(a, "")
		return d != nil && d.Change.Kind == git.Unstaged && len(d.DiffSt.Hunks()) == 2
	})
	d = diffDoc(a, "")
	focusNewSide(t, tt)
	d.DiffSt.GoToHunk(1)
	tt.Frame()
	mustClick(t, tt, "丢弃这一块")
	if a.Confirm == nil || !tt.HasText("丢弃 h.txt 里的这一块改动？") {
		t.Fatalf("没弹确认: texts=%q", tt.Texts())
	}
	mustClick(t, tt, "取消")
	data, _ := os.ReadFile(filepath.Join(repo.Root, "h.txt"))
	if !strings.Contains(string(data), "第 30 行（改过）") {
		t.Fatal("取消后文件不该变")
	}
	focusNewSide(t, tt) // 确认框拿走了键盘，指针也在它的按钮上
	d.DiffSt.GoToHunk(1)
	tt.Frame()
	mustClick(t, tt, "丢弃这一块")
	mustClick(t, tt, "丢弃")
	wait("丢弃第 2 块", func() bool {
		data, _ := os.ReadFile(filepath.Join(repo.Root, "h.txt"))
		return idle(r)() && !strings.Contains(string(data), "第 30 行（改过）")
	})
	data, _ = os.ReadFile(filepath.Join(repo.Root, "h.txt"))
	if !strings.Contains(string(data), "第 3 行（改过）") {
		t.Error("丢弃第 2 块把第 1 块也丢了")
	}

	// ⌥J / ⌥K：下一个 / 上一个改动的文件（预览标签，不堆标签）
	wait("h.txt 的 diff", func() bool { d := diffDoc(a, ""); return d != nil && d.Rel == "h.txt" })
	tabs := len(a.Docs)
	tt.Key(wui.Alt, wui.KeyJ)
	wait("z.txt 的 diff", func() bool { d := diffDoc(a, ""); return d != nil && d.Rel == "z.txt" })
	tt.Key(wui.Alt, wui.KeyJ)
	tt.Frame()
	if d := a.CurrentDoc(); d.Rel != "z.txt" || !tt.HasText("已经是最后一个文件") {
		t.Errorf("到头了应停在 z.txt 并提示: %s %q", d.Rel, tt.Texts())
	}
	tt.Key(wui.Alt, wui.KeyK)
	wait("回到 h.txt", func() bool { d := a.CurrentDoc(); return d != nil && d.Rel == "h.txt" })
	if len(a.Docs) > tabs+1 {
		t.Errorf("一路 ⌥J / ⌥K 开了 %d 个标签", len(a.Docs)-tabs)
	}
}

// focusNewSide 点进 Diff 新的一侧的第一行（中间可能是"展开 n 行未修改"的条），再把指针移开，
// 让块按钮跟着光标而不是指针。
func focusNewSide(t *testing.T, tt *wui.Tester) {
	t.Helper()
	box, ok := tt.Find("新文本")
	if !ok {
		t.Fatalf("没有新的一侧: %q", tt.Texts())
	}
	tt.ClickAt(box.X+box.W/2, box.Y+8)
	tt.Move(4, box.Y+box.H-4)
	tt.Frame()
}

// shows 是界面上有一个元素正好显示 s（HasText 是子串，"暂存这一块" 会匹配到 "取消暂存这一块"）。
func shows(tt *wui.Tester, s string) bool {
	_, ok := tt.Find(s)
	return ok
}

// 全文 diff 打开就停在第一处改动（原来停在文件顶上，300 行的文件看不到改动），
// 局部 / 全文来回切时光标所在那块留在视图里。
func TestDiffFirstHunk(t *testing.T) {
	repo := testutil.NewRepo(t, "firsthunk")
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	repo.Write("p.txt", strings.Join(lines, "\n")+"\n")
	repo.Commit("初始提交")
	lines[199], lines[259] = "line 200（改过）", "line 260（改过）"
	repo.Write("p.txt", strings.Join(lines, "\n")+"\n")
	ws := testutil.Workspace(t, repo.Root)

	a, tt, wait := newUI(t, ws, "")
	a.St.FullDiff = true
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.OpenDiff(r, git.Change{Kind: git.Unstaged, Code: 'M', Path: "p.txt"}, true)
	wait("p.txt 的 diff", func() bool { return diffDoc(a, "") != nil })
	tt.Frame()
	d := diffDoc(a, "")
	check := func(what string, hunk int) {
		t.Helper()
		if i, in := d.DiffSt.CurrentHunk(); i != hunk || !in {
			t.Errorf("%s：光标应在第 %d 块，在 %d（inside=%v）", what, hunk+1, i+1, in)
		}
		if y, ok := changedRow(tt); !ok || y > 0.6 {
			t.Errorf("%s：改动的那行应在视图的上半部分，在 %.2f（ok=%v）", what, y, ok)
		}
	}
	check("全文打开", 0)
	a.ToggleScope()
	tt.Frame()
	check("切到局部", 0)
	d.DiffSt.GoToHunk(1)
	tt.Frame()
	a.ToggleScope()
	tt.Frame()
	check("在第 2 块切回全文", 1)
}

// changedRow 是 Diff 新的一侧第一条新增行（绿底）的上沿在视图里的位置（0 顶、1 底），没有就是 false。
func changedRow(tt *wui.Tester) (float32, bool) {
	box, ok := tt.Find("新文本")
	if !ok || box.H <= 0 {
		return 0, false
	}
	img := tt.Image()
	scale := float32(img.Bounds().Dx()) / 1440 // newUI 的窗口宽
	x := int((box.X + box.W - 30) * scale)
	for y := int(box.Y * scale); y < int((box.Y+box.H)*scale); y++ {
		if c := img.RGBAAt(x, y); int(c.G) > int(c.R)+6 && int(c.G) > int(c.B)+6 {
			return (float32(y)/scale - box.Y) / box.H, true
		}
	}
	return 0, false
}
