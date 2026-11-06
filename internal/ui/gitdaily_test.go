package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// Git 日常操作在界面上的入口（临时仓库 + 临时裸远程）：分支下拉里"合并到当前分支 / 把当前分支变基到"两个子菜单、
// 推送按钮右键的强制推送 / 同步、状态栏的 ↑N 点了同步、历史右键的拣选 / 还原 / 重置三档、提交框的"撤销上次提交"。
func TestGitDailyMenus(t *testing.T) {
	repo := testutil.NewRepo(t, "daily")
	repo.Write("a.txt", "a\n")
	repo.Commit("base")
	repo.Git("switch", "-q", "-c", "feature")
	repo.Write("f.txt", "f\n")
	repo.Commit("feature one")
	repo.Git("switch", "-q", "main")
	repo.Write("b.txt", "b\n")
	repo.Commit("main two")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "-u", "origin", "main")
	repo.Write("c.txt", "c\n")
	repo.Commit("main three")

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.UseRepo(r)
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("状态", idle(r))
	tt.Frame()

	// 分支下拉：两个子菜单在"新建分支…"上面；合并不问
	mustClickRow(t, tt, "分支 main")
	menu := shownMenu(t, tt)
	im, ir, in := slices.Index(menu, "合并到当前分支"), slices.Index(menu, "把当前分支变基到"), slices.Index(menu, "新建分支…")
	if im < 0 || ir != im+1 || in < ir {
		t.Fatalf("分支菜单: %q", menu)
	}
	if err := chooseMenu(t, tt, "合并到当前分支", "main"); err == nil {
		t.Error("子菜单里不该有当前分支")
	}
	if err := chooseMenu(t, tt, "合并到当前分支", "feature"); err != nil {
		t.Fatal(err)
	}
	wait("合并", func() bool { return idle(r)() && r.Status.Ahead == 3 })
	if a.ConfirmOpen {
		t.Fatal("合并不该问")
	}
	if f := strings.Fields(repo.Git("rev-list", "-n", "1", "--parents", "HEAD")); len(f) != 3 {
		t.Fatalf("应是合并提交：%v", f)
	}
	// feature 已经并进来了：变基到它不问、只说一声
	mustClickRow(t, tt, "分支 main")
	if err := chooseMenu(t, tt, "把当前分支变基到", "feature"); err != nil {
		t.Fatal(err)
	}
	wait("不用变基", func() bool { return tt.HasText("main 已经在 feature 上面了，不用变基") })
	if a.ConfirmOpen {
		t.Fatal("不用变基也问了")
	}

	// 推送按钮右键：强制推送（有上游，能用）+ 同步
	if err := rightClickRow(tt, "推送（git push），领先 3"); err != nil {
		t.Fatalf("右键推送按钮: %v；texts=%q", err, tt.Texts())
	}
	tt.Frame()
	if menu := shownMenu(t, tt); !slices.Equal(menu, []string{"强制推送（--force-with-lease）…", "同步（拉取再推送）"}) {
		t.Errorf("推送按钮右键: %q", menu)
	}
	closeMenu(t, tt)

	// 状态栏的 ↑3：点了同步（拉取 ff-only 没东西，再推送）
	mustClickRow(t, tt, "同步 ↑3")
	wait("同步", func() bool { return idle(r)() && r.Status.Ahead == 0 })
	if got, head := testutil.GitOut(t, bare, "rev-parse", "main"), repo.Git("rev-parse", "HEAD"); got != head {
		t.Fatalf("同步后远程 %s，本地 %s", got, head)
	}

	// 历史右键（当前分支）：HEAD 是合并提交，拣选 / 还原灰着带原因；main two 已经在 HEAD 里
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	a.SetHistAll(false)
	a.LoadHistory(r, true)
	h := &r.Hist
	wait("历史", func() bool { return h.Loaded && !h.Loading && len(h.Commits) == 5 })
	tt.Frame()
	if err := rightClickRow(tt, "Merge branch 'feature'"); err != nil {
		t.Fatalf("右键合并提交: %v", err)
	}
	tt.Frame()
	menu = shownMenu(t, tt)
	for _, want := range []string{"拣选这个提交（就是 HEAD）", "还原这个提交（合并提交，到终端里 revert -m）", "重置到这个提交（就是 HEAD）"} {
		if !slices.Contains(menu, want) {
			t.Errorf("合并提交的右键菜单没有 %q: %q", want, menu)
		}
		if err := chooseMenu(t, tt, want); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Errorf("%q 应灰着: %v", want, err)
		}
	}
	closeMenu(t, tt)
	if err := rightClickRow(tt, "main two"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if menu := shownMenu(t, tt); !slices.Contains(menu, "拣选这个提交（HEAD 里已经有了）") || !slices.Contains(menu, "还原这个提交…") {
		t.Errorf("main two 的右键菜单: %q", menu)
	}
	closeMenu(t, tt)

	// 还原先问，确认后多一个 Revert 提交
	mustRightClickRow(t, tt, "main three", "还原这个提交…")
	wait("还原的问话", func() bool { return a.ConfirmOpen })
	if c := a.Confirm; !strings.HasPrefix(c.Title, "还原 ") || c.Button != "还原提交" {
		t.Fatalf("还原的问话: %+v", c)
	}
	mustClickRow(t, tt, "还原提交")
	wait("还原", func() bool { return idle(r)() && len(h.Commits) == 6 && h.Commits[0].Subject == `Revert "main three"` })

	// 撤销上次提交（工作区干净时段收着，在段头右键）：没推送，不问；改动回到暂存区、信息回到提交框，框里也有这个按钮
	mustRightClickRow(t, tt, "daily（仓库）", "撤销上次提交")
	wait("撤销", func() bool { return idle(r)() && r.Status.Ahead == 0 && r.Status.Staged == 1 })
	if a.ConfirmOpen || !strings.HasPrefix(r.CommitMsg, `Revert "main three"`) {
		t.Fatalf("撤销后问了 %v，框里 %q", a.ConfirmOpen, r.CommitMsg)
	}
	tt.Frame()
	if _, ok := tt.Find("撤销上次提交"); !ok {
		t.Errorf("提交框里没有撤销按钮：%q", tt.Texts())
	}

	// 重置三档在子菜单里；硬重置问话里带推送过的提交数和要丢的改动数
	mustRightClickRow(t, tt, "main three", "重置到这个提交", "硬（丢掉改动）…")
	wait("重置的问话", func() bool { return a.ConfirmOpen })
	c := a.Confirm
	for _, want := range []string{"中间的 2 个提交只能从 reflog 找回", "其中 2 个已经推送过", "工作区里 1 处未提交的改动也会丢"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("硬重置的问话没有 %q: %q", want, c.Message)
		}
	}
	mustClickRow(t, tt, "重置并丢弃改动")
	wait("重置", func() bool { return idle(r)() && r.Status.Behind == 2 && r.Status.Staged == 0 })
	if got := repo.Git("log", "-1", "--format=%s"); got != "main three" {
		t.Errorf("重置后 HEAD 是 %q", got)
	}
}
