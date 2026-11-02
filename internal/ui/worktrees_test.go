package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
)

// TestWorktreeBlock 是 Git 栏里的"工作树"块。临时仓库 proj 旁边两个工作树：wt/feat（提交了一次、
// 还有一处改动）和一个分离 HEAD 的。块头和展开后懒算的状态、单击看分支历史和右栏的胶囊、右键在工作区中打开
// （"工作树"标签、状态栏、"已打开"）、分支菜单里在别处检出的分支灰着，新建 / 锁定 / 移除的对话框。
// 写操作都在 t.TempDir() 的临时仓库上。
func TestWorktreeBlock(t *testing.T) {
	r := testutil.NewRepo(t, "proj")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	side := filepath.Dir(r.Root)
	feat, demo := filepath.Join(side, "proj-feat"), filepath.Join(side, "proj-demo")
	r.Git("worktree", "add", "-q", "-b", "wt/feat", feat)
	r.Git("worktree", "add", "-q", "--detach", demo)
	f := r.In(feat)
	f.Write("b.txt", "二\n")
	f.Commit("工作树里的提交")
	f.Write("b.txt", "改\n")
	head := r.Git("rev-parse", "HEAD")

	a, tt, wait := newUI(t, testutil.Workspace(t, r.Root), termState(t, false))
	a.Exec("view.git")
	pr := a.Repos[0]
	a.RefreshRepo(pr, app.WhyManual)
	wait("工作树列表", func() bool { return loaded(a)() && len(pr.Worktrees) == 2 })
	mustText(t, tt, "工作树")
	if tt.HasText("wt/feat") || pr.WtInfo != nil {
		t.Fatalf("块默认收着、不算状态：%q", tt.Texts())
	}

	// 展开：两行，状态懒算出来（改动、领先），块头写"2 · 1 有改动"；分离 HEAD 的写短哈希和灰的目录名
	mustClick(t, tt, "展开 工作树")
	wait("懒算的状态", func() bool { return pr.WtInfo != nil && tt.HasText("↑1") })
	for _, s := range []string{"wt/feat", app.WtDetachedName(head), "proj-demo", "2 · " + i18n.Tf("%s 有改动", "1")} {
		mustText(t, tt, s)
	}
	if tt.HasText("proj-feat") {
		t.Error("目录名以 -<分支最后一段> 结尾，不该再写")
	}
	if _, ok := tt.Find(i18n.Tf("%s 处未提交的改动", "1")); !ok {
		t.Error("wt/feat 那行没有改动数")
	}
	accessCheck(t, tt, "工作树块展开")

	// 单击：右栏看 wt/feat 的历史（有它自己的提交），胶囊 × 回到仓库自己的历史
	mustClick(t, tt, "wt/feat")
	wait("工作树的历史", func() bool {
		return pr.Hist.RefPath != "" && pr.Hist.Loaded && !pr.Hist.Loading && tt.HasText("工作树里的提交")
	})
	mustText(t, tt, i18n.Tf("工作树：%s", "wt/feat"))
	mustText(t, tt, i18n.Tf("%s（工作树）", "wt/feat"))
	accessCheck(t, tt, "右栏看工作树的历史")
	tt.Advance(time.Second) // 和下一次点隔开，不算双击
	mustClick(t, tt, "回到这个仓库自己的历史")
	wait("回到自己的历史", func() bool {
		return pr.Hist.RefPath == "" && pr.Hist.Loaded && !pr.Hist.Loading && !tt.HasText("工作树里的提交")
	})

	// 分支菜单：wt/feat 在 proj-feat 检出着，灰的
	tt.Advance(time.Second)
	mustClick(t, tt, i18n.Tf("分支 %s", "main"))
	elsewhere := "wt/feat · " + i18n.Tf("在 %s 检出", "proj-feat")
	if menu := tt.Menu(); !slices.Contains(menu, elsewhere) {
		t.Fatalf("分支菜单里没有 %q：%q", elsewhere, menu)
	}
	if err := tt.ChooseMenuItem(elsewhere); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("在别处检出的分支应该灰着：%v", err)
	}
	tt.CloseMenu()

	// 右键"在工作区中打开"：多一段，段头有"工作树"标签，状态栏写"· 工作树"，原来那行写"已打开"
	tt.Advance(time.Second)
	mustRightClick(t, tt, "wt/feat", i18n.T("在工作区中打开"))
	wait("打开的工作树", func() bool { return len(a.Repos) == 2 && loaded(a)() })
	fr := a.Repos[1]
	if !fr.Linked || a.Current() != fr || !git.SamePath(fr.MainRoot, r.Root) {
		t.Fatalf("打开的工作树：linked=%v main=%q 当前=%s", fr.Linked, fr.MainRoot, a.Current().Name)
	}
	if _, ok := tt.Find(i18n.T("链接的工作树")); !ok {
		t.Error("段头没有\"工作树\"标签")
	}
	// 状态栏：仓库名、分支图标、"分支 · 工作树"（分开画）
	if _, ok := tt.Find(i18n.Tf("当前仓库 %s", "proj-feat")); !ok {
		t.Error("状态栏的当前仓库不是 proj-feat")
	}
	mustText(t, tt, "wt/feat · "+i18n.T("工作树"))
	mustText(t, tt, i18n.T("已打开"))

	// 锁定：对话框填原因，行上多一把锁
	tt.Advance(time.Second)
	mustRightClick(t, tt, app.WtDetachedName(head), i18n.T("锁定…"))
	mustText(t, tt, i18n.Tf("锁定工作树 %s", app.WtDetachedName(head)))
	tt.Type("在 U 盘上")
	accessCheck(t, tt, "锁定工作树的对话框")
	mustClick(t, tt, i18n.T("锁定"))
	wait("锁上", func() bool {
		_, ok := tt.Find(i18n.Tf("已锁定：%s", "在 U 盘上"))
		return idle(pr)() && ok
	})

	// 新建：分支名跟着改目录，目录已经有了不让建；建好后打开
	a.NewWorktree(pr)
	tt.Frame()
	mustText(t, tt, i18n.T("新建工作树"))
	tt.Type("feat")
	tt.Frame()
	if want := filepath.Join(side, "proj-feat"); a.WtDialog.Dir != want || !tt.HasText(i18n.T("这个目录已经有了")) {
		t.Fatalf("目录 %q（要 %q），已经有了的提示 %v", a.WtDialog.Dir, want, tt.HasText(i18n.T("这个目录已经有了")))
	}
	tt.Type("2")
	tt.Frame()
	newDir := filepath.Join(side, "proj-feat2")
	if a.WtDialog.Dir != newDir || tt.HasText(i18n.T("这个目录已经有了")) || !tt.HasText("git worktree add -b feat2 "+newDir) {
		t.Fatalf("目录 %q；texts=%q", a.WtDialog.Dir, tt.Texts())
	}
	accessCheck(t, tt, "新建工作树的对话框")
	mustClick(t, tt, i18n.T("新建"))
	wait("新建的工作树", func() bool { return len(a.Repos) == 3 && loaded(a)() && idle(pr)() })
	if _, err := os.Stat(filepath.Join(newDir, "a.txt")); err != nil || r.Git("branch", "--list", "feat2") == "" {
		t.Fatalf("工作树没建出来：%v", err)
	}

	// 移除：对话框写路径和"同时删除分支"，删掉后那一段也没了
	a.UseRepo(pr)
	tt.Advance(time.Second)
	mustRightClick(t, tt, "feat2", i18n.T("移除工作树…"))
	mustText(t, tt, i18n.Tf("移除工作树 %s？", "feat2"))
	mustText(t, tt, newDir)
	mustClick(t, tt, i18n.Tf("同时删除分支 %s", "feat2"))
	accessCheck(t, tt, "移除工作树的对话框")
	mustClick(t, tt, i18n.T("移除"))
	wait("移除", func() bool { return len(a.Repos) == 2 && idle(pr)() && r.Git("branch", "--list", "feat2") == "" })
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Errorf("目录还在：%v", err)
	}
}

// TestWorktreeKeys：工作树的行是 Git 栏列表里能选中的行。临时仓库 aaa 有两处改动、proj 旁边两个工作树
// （wt/feat 和一个分离 HEAD 的）。从 aaa 最后一项改动按 ↓ 跳过提交框、段头、块头停在第一个工作树上，右栏看它的历史，
// 开着的 Diff 不动；再 ↓ 到下一个；回车在工作区中打开；↑ 回到改动，右栏回到当前仓库；单击也让它成为选中行。
func TestWorktreeKeys(t *testing.T) {
	ra := testutil.NewRepo(t, "aaa")
	ra.Write("a.txt", "一\n")
	ra.Commit("初始")
	ra.Write("a.txt", "改\n")
	ra.Write("b.txt", "新\n")
	r := testutil.NewRepo(t, "proj")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	side := filepath.Dir(r.Root)
	r.Git("worktree", "add", "-q", "-b", "wt/feat", filepath.Join(side, "proj-feat"))
	r.Git("worktree", "add", "-q", "--detach", filepath.Join(side, "proj-demo"))
	f := r.In(filepath.Join(side, "proj-feat"))
	f.Write("b.txt", "二\n")
	f.Commit("工作树里的提交")

	a, tt, wait := newUI(t, testutil.Workspace(t, ra.Root, r.Root), termState(t, false))
	a.Exec("view.git")
	pa, pr := a.Repos[0], a.Repos[1]
	a.RefreshRepo(pa, app.WhyManual)
	a.RefreshRepo(pr, app.WhyManual)
	wait("状态和工作树", func() bool { return loaded(a)() && pa.Status.Count == 2 && len(pr.Worktrees) == 2 })
	mustClick(t, tt, "展开 工作树")
	wait("懒算的状态", func() bool { return pr.WtInfo != nil && !pr.WtLoading })
	w1, w2 := pr.Worktrees[0], pr.Worktrees[1]
	title := func(w app.Worktree) string {
		if w.Branch != "" {
			return w.Branch
		}
		return app.WtDetachedName(w.Head)
	}
	selRow := func() app.ChangeRow {
		t.Helper()
		rows := a.GitRows()
		if a.GitSel < 0 || a.GitSel >= len(rows) {
			t.Fatalf("Git 栏没有选中行（%d）", a.GitSel)
		}
		return rows[a.GitSel]
	}
	onWt := func(what string, w app.Worktree) {
		t.Helper()
		wait(what, func() bool {
			return pr.Hist.RefPath == w.Path && pr.Hist.Loaded && !pr.Hist.Loading
		})
		tt.Frame()
		if cr := selRow(); cr.Kind != app.RowWorktree || cr.Wt.Path != w.Path {
			t.Fatalf("%s：选中的是 %v %q", what, cr.Kind, cr.Wt.Path)
		}
		if a.Current() != pr {
			t.Errorf("%s：当前仓库是 %s", what, a.Current().Name)
		}
		mustText(t, tt, i18n.Tf("工作树：%s", title(w)))
	}

	// 选中 aaa 的最后一项改动（未跟踪的 b.txt），预览 Diff
	mustClick(t, tt, "b.txt")
	wait("b.txt 的 Diff", func() bool { d := a.CurrentDoc(); return d != nil && d.Change.Path == "b.txt" })
	diff := a.CurrentDoc()
	if cr := selRow(); cr.Kind != app.RowChange || cr.Change.Path != "b.txt" {
		t.Fatalf("点了 b.txt 选中的是 %v %q", cr.Kind, cr.Change.Path)
	}

	// ↓：跳过 aaa 的提交框、proj 的段头和"工作树"块头，停在第一个工作树；Diff 标签不动
	tt.Key(0, wui.KeyDown)
	onWt("↓ 到第一个工作树", w1)
	if a.CurrentDoc() != diff || len(a.AllDocs()) != 1 {
		t.Errorf("停到工作树上动了 Diff 标签：%d 个标签", len(a.AllDocs()))
	}
	accessCheck(t, tt, "选中工作树的行")

	// 再 ↓：下一个工作树
	tt.Key(0, wui.KeyDown)
	onWt("↓ 到第二个工作树", w2)

	// 状态重读（行号会变）选中还在这个工作树上
	a.RefreshRepo(pr, app.WhyManual)
	wait("重读 proj", idle(pr))
	if cr := selRow(); cr.Kind != app.RowWorktree || cr.Wt.Path != w2.Path {
		t.Fatalf("重读状态后选中跑到 %v %q", cr.Kind, cr.Wt.Path)
	}

	// 回车：在工作区中打开，多一段，段头有"工作树"标签
	tt.Key(0, wui.KeyEnter)
	wait("打开的工作树", func() bool { return len(a.Repos) == 3 && loaded(a)() })
	if a.WtRepo(w2) == nil || !a.Repos[2].Linked {
		t.Fatalf("回车没有在工作区中打开 %s", w2.Path)
	}
	tt.Frame()
	if _, ok := tt.Find(i18n.T("链接的工作树")); !ok {
		t.Error("打开的工作树段头没有\"工作树\"标签")
	}
	mustText(t, tt, i18n.T("已打开"))
	if cr := selRow(); cr.Kind != app.RowWorktree || cr.Wt.Path != w2.Path {
		t.Fatalf("打开之后选中跑到 %v %q", cr.Kind, cr.Wt.Path)
	}
	if dir := os.Getenv("GRAPH_PNG"); dir != "" { // 看选中行上的"已打开"、领先数
		savePNG(t, filepath.Join(dir, "worktree-selected.png"), tt.Image())
	}

	// ↑ 两下：回到第一个工作树，再跳过块头、段头、提交框回到 b.txt；右栏回到当前仓库自己的历史
	tt.Key(0, wui.KeyUp)
	onWt("↑ 回到第一个工作树", w1)
	tt.Key(0, wui.KeyUp)
	wait("↑ 回到改动", func() bool { return pr.Hist.RefPath == "" && a.Current() == pa })
	tt.Frame()
	if cr := selRow(); cr.Kind != app.RowChange || cr.Change.Path != "b.txt" {
		t.Fatalf("↑ 选中的是 %v %q", cr.Kind, cr.Change.Path)
	}
	if tt.HasText(i18n.Tf("工作树：%s", title(w1))) {
		t.Error("回到改动后右栏还在看工作树的历史")
	}

	// 单击工作树的行：它成为列表的选中行，右栏看它
	tt.Advance(time.Second)
	mustClick(t, tt, i18n.Tf("工作树 %s", title(w2)))
	onWt("单击第二个工作树", w2)
}
