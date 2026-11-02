package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// conflictApp 在 ConflictRepo 上开应用、读好状态、打开那个冲突文件。
func conflictApp(t *testing.T, base, ours, theirs string) (*App, *Repo, *docs.Doc, func(string, func() bool)) {
	t.Helper()
	repo := testutil.ConflictRepo(t, "merge", "c.txt", base, ours, theirs)
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if r.Status.Conflicts != 1 || r.Extra.Ongoing != git.Merging {
		t.Fatalf("应在合并、1 个冲突: %v %+v", r.Extra.Ongoing, r.Status)
	}
	d := a.OpenFile(r, "c.txt", true)
	wait("读文件", func() bool { return d.Buf != nil })
	return a, r, d, wait
}

// 一块冲突的四种解法，每次之后 ⌘Z 回到原样；只改缓冲区，磁盘上的还带着标记。
func TestResolveConflict(t *testing.T) {
	a, _, d, _ := conflictApp(t, "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")
	blocks, flagged := a.Conflicted(d)
	if len(blocks) != 1 || !flagged || blocks[0].OursLabel != "HEAD" || blocks[0].TheirsLabel != "feature" {
		t.Fatalf("冲突块 %+v，仓库标着 %v", blocks, flagged)
	}
	orig := d.Buf.String()
	for _, c := range []struct {
		how  docs.Take
		want string
	}{
		{docs.TakeOurs, "1\n我的\n3\n"},
		{docs.TakeTheirs, "1\n他们的\n3\n"},
		{docs.TakeBoth, "1\n我的\n他们的\n3\n"},
		{docs.TakeBothReversed, "1\n他们的\n我的\n3\n"},
	} {
		a.ResolveConflict(d, 0, c.how)
		if got := d.Buf.String(); got != c.want {
			t.Errorf("how %d: %q, want %q", c.how, got, c.want)
		}
		if p := d.St.CursorPos(); p.Line != 1 {
			t.Errorf("how %d: 光标在第 %d 行，应在换上的第一行", c.how, p.Line)
		}
		if n := len(d.ConflictBlocks()); n != 0 {
			t.Errorf("how %d: 还剩 %d 块", c.how, n)
		}
		d.Buf.Undo()
		if got := d.Buf.String(); got != orig {
			t.Fatalf("how %d: ⌘Z 后 %q", c.how, got)
		}
	}
	if disk, _ := os.ReadFile(d.Abs); string(disk) != orig {
		t.Errorf("磁盘上的被改了: %q", disk)
	}
}

// 两块：解决第一块之后第二块还认得出来；下一处 / 上一处绕着走；"全部用我的"先问，一次 ⌘Z 全回去。
func TestResolveConflictBlocks(t *testing.T) {
	base := "a\n2\n3\n4\n5\n6\n7\nz\n"
	a, _, d, _ := conflictApp(t, base, "a\n我的二\n3\n4\n5\n6\n我的七\nz\n", "a\n他们的二\n3\n4\n5\n6\n他们的七\nz\n")
	blocks := d.ConflictBlocks()
	if len(blocks) != 2 {
		t.Fatalf("应有两块: %+v\n%s", blocks, d.Buf.String())
	}
	orig := d.Buf.String()

	d.JumpTo(docs.Jump{Line: 0})
	a.GoToConflict(d, 1)
	if d.St.CursorPos().Line != blocks[0].Start || a.CurrentConflict(d) != 0 {
		t.Errorf("下一处: 第 %d 行", d.St.CursorPos().Line)
	}
	a.GoToConflict(d, 1)
	if d.St.CursorPos().Line != blocks[1].Start || a.CurrentConflict(d) != 1 {
		t.Errorf("再下一处: 第 %d 行", d.St.CursorPos().Line)
	}
	a.GoToConflict(d, 1) // 绕回第一块
	if d.St.CursorPos().Line != blocks[0].Start {
		t.Errorf("绕回: 第 %d 行", d.St.CursorPos().Line)
	}
	a.GoToConflict(d, -1) // 往上绕到最后一块
	if d.St.CursorPos().Line != blocks[1].Start {
		t.Errorf("上一处绕回: 第 %d 行", d.St.CursorPos().Line)
	}

	a.ResolveConflict(d, 0, docs.TakeTheirs)
	left := d.ConflictBlocks()
	if len(left) != 1 || a.CurrentConflict(d) != 0 {
		t.Fatalf("解决第一块后: %+v", left)
	}
	if lines := strings.Split(d.Buf.String(), "\n"); lines[left[0].Start+1] != "我的七" || lines[left[0].Mid+1] != "他们的七" {
		t.Errorf("剩下的一块不对: %+v\n%s", left[0], d.Buf.String())
	}
	a.ResolveCurrent(d, docs.TakeOurs) // 光标在第一块换上的那行，"当前"是后面那块
	if got := d.Buf.String(); got != "a\n他们的二\n3\n4\n5\n6\n我的七\nz\n" {
		t.Errorf("两块都解决: %q", got)
	}
	d.Buf.Undo()
	d.Buf.Undo()
	if d.Buf.String() != orig {
		t.Fatalf("两次 ⌘Z 后 %q", d.Buf.String())
	}

	a.ResolveAll(d, docs.TakeOurs)
	if !a.ConfirmOpen || a.Confirm == nil || a.Confirm.Title != "2 处冲突全部用我的？" {
		t.Fatalf("全部用我的应先问: %+v", a.Confirm)
	}
	if d.Buf.String() != orig {
		t.Fatal("问的时候就改了")
	}
	a.Confirm.Fn()
	if got := d.Buf.String(); got != "a\n我的二\n3\n4\n5\n6\n我的七\nz\n" {
		t.Errorf("全部用我的: %q", got)
	}
	d.Buf.Undo()
	if d.Buf.String() != orig {
		t.Errorf("全部用我的一次 ⌘Z 没回去: %q", d.Buf.String())
	}
}

// 标记为已解决：没保存的先保存，再 git add；还有标记先问。没开着的文件看磁盘上的标记。
func TestMarkResolved(t *testing.T) {
	a, r, d, wait := conflictApp(t, "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")

	// 还有标记：先问，不点就什么都不做
	a.MarkResolved(d)
	if !a.ConfirmOpen || a.Confirm.Title != "还有 1 处冲突标记，标成已解决？" {
		t.Fatalf("有标记应先问: %+v", a.Confirm)
	}
	a.ConfirmOpen, a.Confirm = false, nil
	a.MarkResolvedPath(r, "c.txt") // 开着的照标签来，也问
	if !a.ConfirmOpen {
		t.Fatal("Git 栏的标记为已解决也应先问")
	}
	a.ConfirmOpen, a.Confirm = false, nil

	a.ResolveConflict(d, 0, docs.TakeTheirs) // 用我的就和 HEAD 一样，git add 之后没有可暂存的了
	if !d.Modified() {
		t.Fatal("解决之后应是没保存")
	}
	if _, flagged := a.Conflicted(d); !flagged {
		t.Fatal("没 git add 之前仓库还当它是冲突")
	}
	a.MarkResolved(d)
	if a.ConfirmOpen {
		t.Fatalf("没有标记了不该问: %+v", a.Confirm)
	}
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	if r.Status.Staged != 1 || d.Modified() {
		t.Errorf("标记后 %+v，还没保存 %v", r.Status, d.Modified())
	}
	if disk, _ := os.ReadFile(filepath.Join(r.Root, "c.txt")); string(disk) != "1\n他们的\n3\n" {
		t.Errorf("磁盘上 %q", disk)
	}
	if _, flagged := a.Conflicted(d); flagged {
		t.Error("暂存后仓库不该再当它是冲突")
	}
}

// 没开着的冲突文件：磁盘上还有标记就问，点了才 git add（标记原样进索引）。
func TestMarkResolvedPath(t *testing.T) {
	repo := testutil.ConflictRepo(t, "merge", "c.txt", "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.MarkResolvedPath(r, "c.txt")
	if !a.ConfirmOpen || a.Confirm.Button != "仍然标记" {
		t.Fatalf("磁盘上有标记应先问: %+v", a.Confirm)
	}
	a.Confirm.Fn()
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	if r.Status.Staged != 1 || !strings.Contains(testutil.GitOut(t, r.Root, "show", ":c.txt"), "<<<<<<< HEAD") {
		t.Errorf("标记后 %+v", r.Status)
	}
}

// 同一个冲突文件的 HEAD ↔ 工作区（'U'）和两边（'S'）是两个标签。
func TestConflictKey(t *testing.T) {
	a, r, _, wait := conflictApp(t, "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")
	u := a.OpenDiff(r, r.Status.Changes[0], true)
	s := a.OpenStagesDiff(r, "c.txt")
	if u == s || u.Key() == s.Key() {
		t.Fatalf("'U' 和 'S' 应是两个标签: %+v %+v", u.Key(), s.Key())
	}
	if s.Title != "c.txt（两边）" || u.Title != "diff: c.txt" {
		t.Errorf("标题 %q %q", u.Title, s.Title)
	}
	if again := a.OpenStagesDiff(r, "c.txt"); again != s {
		t.Error("再开两边的 diff 应回到同一个标签")
	}
	wait("读 diff", func() bool { return !s.Loading && !u.Loading })
	if s.DiffSt == nil || s.Info != "" {
		t.Fatalf("两边的 diff 没读出来: %q", s.Info)
	}
	if old, nw := string(s.Raw[0]), string(s.Raw[1]); old != "1\n我的\n3\n" || nw != "1\n他们的\n3\n" {
		t.Errorf("两边 %q ↔ %q", old, nw)
	}
	a.UseRepo(r)
	a.SelectDoc(slices.Index(a.Docs, s))
	a.syncChangeSel()
	if a.GitSel < 0 || a.GitRows()[a.GitSel].Change.Path != "c.txt" {
		t.Errorf("两边的 diff 也该选中 Git 栏的冲突行: %d", a.GitSel)
	}
}

// 冲突都用了我的：git add 之后和 HEAD 一样、没有已暂存的，提交框照样能提交这次合并（两个父提交）。
func TestCommitMergeAllOurs(t *testing.T) {
	a, r, d, wait := conflictApp(t, "1\n2\n3\n", "1\n我的\n3\n", "1\n他们的\n3\n")
	r.CommitMsg = "合并 feature"
	if r.CanCommit() || r.MergeReady() {
		t.Fatal("还有冲突时不该能提交")
	}
	a.ResolveConflict(d, 0, docs.TakeOurs)
	a.MarkResolved(d)
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	if r.Status.Staged != 0 || !r.MergeReady() || !r.CanCommit() || !r.CommitShown() {
		t.Fatalf("冲突都解决了应能提交合并: %+v，%v %v", r.Status, r.MergeReady(), r.CanCommit())
	}
	a.Commit(r)
	wait("提交", func() bool { return idle(r)() && r.Extra.Ongoing != git.Merging })
	if a.AlertOpen {
		t.Fatalf("提交失败: %q", a.AlertMsg)
	}
	if parents := strings.Fields(testutil.GitOut(t, r.Root, "rev-list", "--parents", "-1", "HEAD")); len(parents) != 3 {
		t.Errorf("应是合并提交: %q", parents)
	}
	if got := testutil.GitOut(t, r.Root, "log", "-1", "--format=%s"); got != "合并 feature" {
		t.Errorf("提交信息 %q", got)
	}
	r.CommitMsg = "再来"
	if r.CanCommit() {
		t.Error("合并提交完了、没有暂存的不该还能提交")
	}
}
