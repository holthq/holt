package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/testutil"
)

// 行 → 块：块里的行；纯删除的块算它标记的那一行（删在它上面），删在文件末尾的标在最后一行；块外是 -1。
func TestPeekHunkAt(t *testing.T) {
	hs := []editor.DiffHunk{
		{OldStart: 1, OldEnd: 2, NewStart: 1, NewEnd: 3},  // 改了一行成两行：新 1、2
		{OldStart: 5, OldEnd: 7, NewStart: 6, NewEnd: 6},  // 删了两行，标在新第 6 行
		{OldStart: 9, OldEnd: 10, NewStart: 8, NewEnd: 8}, // 删在末尾（新文件 8 行）：标在第 7 行
	}
	for _, c := range []struct{ line, want int }{
		{0, -1}, {1, 0}, {2, 0}, {3, -1}, {5, -1}, {6, 1}, {7, 2},
	} {
		if got := HunkAt(hs, c.line, 8); got != c.want {
			t.Errorf("第 %d 行 → %d，应为 %d", c.line, got, c.want)
		}
	}
	if HunkAt(nil, 0, 1) != -1 {
		t.Error("没有块")
	}
}

// peekRepo 是一个改了两处（第 2 行改了、第 8 行后面加了一行）的临时仓库，打开 a.txt 等它读好（有 HEAD 的对比基准）。
func peekRepo(t *testing.T) (*App, *Repo, *testutil.Repo, func(string, func() bool)) {
	t.Helper()
	repo := testutil.NewRepo(t, "peek")
	repo.Write("a.txt", "1\n2\n3\n4\n5\n6\n7\n8\n9\n")
	repo.Commit("base")
	repo.Write("a.txt", "1\n二\n3\n4\n5\n6\n7\n8\n八点五\n9\n")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	return a, r, repo, wait
}

// 卡开 / 关：点块里的行开，再点同一块关；块外不开。还原：Buffer 那段换回 HEAD 的，文件变脏，⌘Z 撤回；卡留在下一块，没块了就关。
// 光标挪到别处（点了编辑器别处）卡关；光标停到某块开头（上 / 下一处改动）卡跟过去。
func TestPeekRevert(t *testing.T) {
	a, r, _, wait := peekRepo(t)
	d := a.OpenFile(r, "a.txt", true)
	wait("读 a.txt", func() bool { return d.Buf != nil && d.Base != nil })
	d.St.DiffBase = d.Base // 界面建编辑器时设的（EditorOptions 之前就有）
	if hs := DocHunks(d); len(hs) != 2 {
		t.Fatalf("块：%+v", hs)
	}
	if a.TogglePeek(d, 4) || a.Peek.Doc != nil {
		t.Fatal("块外不开")
	}
	if !a.TogglePeek(d, 1) || a.Peek.Doc != d || a.Peek.Hunk != 0 {
		t.Fatalf("点第 2 行：%+v", a.Peek)
	}
	if !a.TogglePeek(d, 1) || a.Peek.Doc != nil {
		t.Fatal("再点同一块该关")
	}

	// 上 / 下一处改动：卡跟过去；点了别处：关
	a.TogglePeek(d, 1)
	a.PeekStep(1)
	if _, i, n, ok := a.PeekHunk(d); !ok || i != 1 || n != 2 || d.St.CursorPos().Line != 8 {
		t.Fatalf("↓：%d/%d 光标 %v", i, n, d.St.CursorPos())
	}
	d.St.GoTo(editor.Pos{Line: 1}) // 上一处改动（editor.PreviousHunk 要建过编辑器才认得 DiffBase）停到块的开头
	a.SyncPeek(d)
	if a.Peek.Doc != d || a.Peek.Hunk != 0 {
		t.Fatalf("上一处改动后卡该跟到第 1 块：%+v", a.Peek)
	}
	d.St.GoTo(editor.Pos{Line: 5, Col: 0})
	a.SyncPeek(d)
	if a.Peek.Doc != nil {
		t.Fatal("光标挪到块外该关")
	}

	// 还原第 1 块
	a.TogglePeek(d, 1)
	a.PeekRevert()
	if got := d.Buf.Line(1); got != "2" {
		t.Errorf("还原后第 2 行 %q", got)
	}
	if !d.Modified() {
		t.Error("还原后文件该变脏")
	}
	if _, i, n, ok := a.PeekHunk(d); !ok || i != 0 || n != 1 {
		t.Errorf("还剩一块，卡该在它上面：%v %d/%d", ok, i, n)
	}
	d.St.Run(editor.Undo)
	if got := d.Buf.Line(1); got != "二" {
		t.Errorf("⌘Z 后第 2 行 %q", got)
	}
	// 两块都还原：没块了就关
	a.TogglePeek(d, 8)
	a.PeekRevert()
	if _, i, n, ok := a.PeekHunk(d); !ok || i != 0 || n != 1 {
		t.Errorf("还原最后一块后卡该落到剩下那块：%v %d/%d", ok, i, n)
	}
	a.PeekRevert()
	if a.Peek.Doc != nil || d.Buf.String() != d.Base.String() {
		t.Errorf("全还原后卡该关、内容同 HEAD：%+v %q", a.Peek, d.Buf.String())
	}
}

// 暂存一块：没保存时不做；保存了以后 git apply --cached，暂存区里只有这一块，工作区不动。在 Diff 中打开：停在同一块。
func TestPeekStage(t *testing.T) {
	a, r, repo, wait := peekRepo(t)
	d := a.OpenFile(r, "a.txt", true)
	wait("读 a.txt", func() bool { return d.Buf != nil && d.Base != nil })
	d.St.DiffBase = d.Base
	a.TogglePeek(d, 8)
	d.St.GoTo(editor.Pos{Line: 8})
	a.Peek.sel = d.St.Selection()
	d.St.Insert("x")
	a.PeekStage()
	if r.Busy != "" || len(r.Queue) != 0 {
		t.Fatal("没保存不该暂存")
	}
	d.St.Run(editor.Undo)
	if d.Modified() {
		t.Fatalf("撤回后该是干净的：%q", d.Buf.String())
	}
	a.ClosePeek()
	a.TogglePeek(d, 8)
	if a.Peek.Doc != d || a.Peek.Hunk != 1 {
		t.Fatalf("卡：%+v", a.Peek)
	}
	a.PeekStage()
	wait("暂存", func() bool { return idle(r)() && strings.Contains(repo.Git("diff", "--cached"), "八点五") })
	cached := repo.Git("diff", "--cached")
	if strings.Contains(cached, "二") || !strings.Contains(cached, "+八点五") {
		t.Errorf("暂存区该只有第 2 块：%s", cached)
	}
	if wt := repo.Git("diff"); !strings.Contains(wt, "+二") || strings.Contains(wt, "八点五") {
		t.Errorf("工作区剩第 1 块没暂存：%s", wt)
	}

	// 在 Diff 中打开：未暂存的 diff 里只剩第 1 块，停在它上面
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.TogglePeek(d, 1)
	a.PeekOpenDiff()
	dd := a.CurrentDoc()
	if dd == nil || dd == d || dd.HunkLine != 2 || !dd.ToHunk || a.Peek.Doc != nil {
		t.Fatalf("Diff 标签：%+v", dd)
	}
	wait("读 diff", func() bool { return dd.DiffSt != nil })
	if i := HunkFromLine(dd.DiffSt, dd.HunkLine-1); i != 0 {
		t.Errorf("该停在第 1 块：%d", i)
	}
}
