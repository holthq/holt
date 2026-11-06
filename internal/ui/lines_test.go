package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// 行级暂存走一遍：选着行时底下出一栏"选中 N 行"；只暂存选中的那行（同一块里另一行留在未暂存里），
// 选的都是没改的行时说一声，Esc 取消选择，丢弃选中的行先问，已暂存的 diff 里取消暂存选中的行。
func TestLineStaging(t *testing.T) {
	repo := testutil.NewRepo(t, "lines")
	join := func(l ...string) string { return strings.Join(l, "\n") + "\n" }
	repo.Write("h.txt", join("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"))
	repo.Commit("初始提交")
	repo.Write("h.txt", join("1", "2", "3x", "3y", "4", "5", "6", "7", "9", "10"))
	ws := testutil.Workspace(t, repo.Root)

	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	h := git.Change{Kind: git.Unstaged, Code: 'M', Path: "h.txt"}
	a.OpenDiff(r, h, true)
	wait("h.txt 的 diff", func() bool { return diffDoc(a, "") != nil })
	d := diffDoc(a, "")
	tt.Frame()
	if !d.DiffSt.LineSelection {
		t.Fatal("未暂存的 diff 应能点行号选行")
	}
	if shows(tt, "暂存选中的行") {
		t.Fatal("没选行时不该有那一栏")
	}
	pick := func(side editor.DiffSide, from, to int) {
		t.Helper()
		d := diffDoc(a, "")
		if d == nil {
			t.Fatal("没有 diff 标签")
		}
		d.DiffSt.SetSelectedLines(editor.DiffLineRange{Side: side, Start: from, End: to}, true)
		tt.Frame()
	}

	// 只暂存 3x（配了对的那行）：3y 和删掉的 8 留在未暂存里
	pick(editor.DiffNew, 2, 2)
	mustText(t, tt, "选中 1 行")
	if !shows(tt, "丢弃选中的行…") || !shows(tt, "取消选择") || shows(tt, "取消暂存选中的行") {
		t.Errorf("未暂存的 diff 那一栏的按钮不对：%q", tt.Texts())
	}
	if c := app.Find("diff.stageLines"); !a.CanRun(c) {
		t.Error("选着行时命令面板里的暂存选中的行应能用")
	}
	mustClick(t, tt, "暂存选中的行")
	wait("暂存 3x", func() bool { return idle(r)() && r.Status.Staged == 1 })
	if got := string(git.Blob(r.Root, "", "h.txt")); got != join("1", "2", "3x", "4", "5", "6", "7", "8", "9", "10") {
		t.Fatalf("索引里是 %q", got)
	}
	if !strings.Contains(a.Output[len(a.Output)-1].Title, "git apply --cached -（h.txt 第 3 行起选中的 1 行）") {
		t.Errorf("输出栏没记这条命令: %q", a.Output[len(a.Output)-1].Title)
	}
	wait("diff 重读、选择没了", func() bool {
		d := diffDoc(a, "")
		return d != nil && len(d.DiffSt.Hunks()) == 2 && app.SelectedLineCount(d) == 0
	})
	tt.Frame()
	if shows(tt, "暂存选中的行") {
		t.Error("做完了那一栏还在")
	}

	// 选的都是没改的行：说一声，什么也不跑
	pick(editor.DiffNew, 0, 1)
	mustClick(t, tt, "暂存选中的行")
	if !tt.HasText("选中的行里没有改动") || r.Busy != "" {
		t.Errorf("只选了没改的行：toast=%q busy=%q", a.Toast, r.Busy)
	}

	// Esc 取消选择
	focusNewSide(t, tt)
	pick(editor.DiffNew, 3, 3)
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	if n := app.SelectedLineCount(diffDoc(a, "")); n != 0 || shows(tt, "取消选择") {
		t.Errorf("Esc 后还选着 %d 行", n)
	}

	// 丢弃 3y：先问，确认后工作区里没了，3x 和删掉的 8 还在
	pick(editor.DiffNew, 3, 3)
	mustClick(t, tt, "丢弃选中的行…")
	if a.Confirm == nil || !tt.HasText("丢弃 h.txt 里选中的 1 行？") {
		t.Fatalf("没弹确认: %q", tt.Texts())
	}
	mustClick(t, tt, "丢弃")
	want := join("1", "2", "3x", "4", "5", "6", "7", "9", "10")
	wait("丢弃 3y", func() bool {
		data, _ := os.ReadFile(filepath.Join(repo.Root, "h.txt"))
		return idle(r)() && string(data) == want
	})

	// 已暂存的 diff：取消暂存 3x，索引回到 HEAD，工作区不动
	a.OpenDiff(r, git.Change{Kind: git.Staged, Code: 'M', Path: "h.txt"}, true)
	wait("已暂存的 diff", func() bool { d := diffDoc(a, ""); return d != nil && d.Change.Kind == git.Staged })
	pick(editor.DiffNew, 2, 2)
	if !shows(tt, "取消暂存选中的行") || shows(tt, "暂存选中的行") || shows(tt, "丢弃选中的行…") {
		t.Errorf("已暂存的 diff 那一栏的按钮不对：%q", tt.Texts())
	}
	mustClick(t, tt, "取消暂存选中的行")
	wait("取消暂存 3x", func() bool { return idle(r)() && r.Status.Staged == 0 })
	if out := repo.Git("diff", "--cached"); out != "" {
		t.Fatalf("取消暂存后暂存区应是空的:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(repo.Root, "h.txt")); string(data) != want {
		t.Errorf("取消暂存动了工作区: %q", data)
	}
}
