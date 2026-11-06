package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// Git 栏变更行上的行内按钮：悬停未暂存 / 未跟踪 / 已暂存的行出的按钮对；点暂存进已暂存组、点取消暂存回来，
// 点按钮不改选中、不开 Diff；丢弃先问、确认后改动没了；多选两行点其中一行的暂存两行都暂存；
// 列表有键盘时选中的行不悬停也出按钮，空格切换暂存，⌘⌫ 丢弃先问；未暂存组头的"丢弃所有未暂存的改动…"。
func TestChangeRowActions(t *testing.T) {
	alpha, _, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("git status", loaded(a))
	tt.Frame()
	ra, rb := a.Repos[0], a.Repos[1]
	hover := func(s string) {
		t.Helper()
		tt.Frame()
		box, ok := tt.Find(s)
		if !ok {
			t.Fatalf("没有 %q: %q", s, tt.Texts())
		}
		tt.Move(box.X+box.W/2, box.Y+box.H/2)
		tt.Frame()
		tt.Frame()
	}
	buttons := func(what string, want, not []string) {
		t.Helper()
		for _, s := range want {
			if _, ok := tt.Find(s); !ok {
				t.Errorf("%s：没有按钮 %q", what, s)
			}
		}
		for _, s := range not {
			if _, ok := tt.Find(s); ok {
				t.Errorf("%s：不该有按钮 %q", what, s)
			}
		}
	}
	kindOf := func(r *app.Repo, p string) git.Kind {
		for _, ch := range r.Status.Changes {
			if ch.Path == p {
				return ch.Kind
			}
		}
		return git.InCommit
	}
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(alpha.Root, p)); return string(b) }

	all := []string{"在编辑器中打开", "丢弃改动…", "删除文件…", "暂存", "取消暂存"}
	hover("alpha.txt")
	buttons("未暂存的行", []string{"在编辑器中打开", "丢弃改动…", "暂存"}, []string{"删除文件…", "取消暂存", "标记为已解决"})
	accessCheck(t, tt, "变更行的行内按钮")
	hover("notes.md")
	buttons("未跟踪的行", []string{"在编辑器中打开", "删除文件…", "暂存"}, []string{"丢弃改动…", "取消暂存"})
	tt.Move(1200, 800) // 指针移开、列表没有键盘：哪一行都不出按钮
	tt.Frame()
	buttons("不悬停", nil, all)
	if n := len(a.Docs); n != 0 {
		t.Fatalf("开始时应没有标签: %d", n)
	}

	// 暂存 → 已暂存组；点按钮不开 Diff、不改选中
	sel := a.GitSel
	hover("alpha.txt")
	mustClick(t, tt, "暂存")
	wait("暂存", func() bool { return idle(ra)() && kindOf(ra, "alpha.txt") == git.Staged })
	tt.Frame()
	if len(a.Docs) != 0 || a.GitSel != sel {
		t.Errorf("点暂存开了 %d 个标签、选中 %d → %d", len(a.Docs), sel, a.GitSel)
	}
	hover("alpha.txt")
	buttons("已暂存的行", []string{"在编辑器中打开", "取消暂存"}, []string{"暂存", "丢弃改动…", "删除文件…"})
	mustClick(t, tt, "取消暂存")
	wait("取消暂存", func() bool { return idle(ra)() && kindOf(ra, "alpha.txt") == git.Unstaged })
	if len(a.Docs) != 0 {
		t.Errorf("点取消暂存开了 %d 个标签", len(a.Docs))
	}

	// 丢弃：先问，确认后改动没了
	hover("alpha.txt")
	mustClick(t, tt, "丢弃改动…")
	mustText(t, tt, "丢弃 alpha.txt 的改动？")
	mustClick(t, tt, "丢弃")
	wait("丢弃", func() bool { return idle(ra)() && kindOf(ra, "alpha.txt") == git.InCommit })
	if got := read("alpha.txt"); got != "一\n二\n三\n" {
		t.Errorf("丢弃后 alpha.txt=%q", got)
	}

	// 多选两行，点其中一行的暂存：两行都暂存（跨仓库）
	tt.Frame()
	if err := clickRow(tt, "notes.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := clickRowWith(tt, wui.Cmd, "beta.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if n := len(a.ChosenChanges()); n != 2 {
		t.Fatalf("应选中两行: %d", n)
	}
	hover("beta.go")
	buttons("多选里的行", []string{"暂存 2 项", "丢弃 2 项改动…"}, []string{"暂存"})
	mustClick(t, tt, "暂存 2 项")
	wait("暂存两项", func() bool {
		return idle(ra)() && idle(rb)() && kindOf(ra, "notes.md") == git.Staged && kindOf(rb, "beta.go") == git.Staged
	})

	// 键盘：选中一行（列表有键盘），不悬停也出按钮；空格切换暂存
	tt.Frame()
	if err := clickRow(tt, "beta.go"); err != nil {
		t.Fatal(err)
	}
	tt.Move(1200, 800)
	tt.Frame()
	tt.Frame()
	buttons("键盘选中的行", []string{"取消暂存"}, nil)
	tt.Key(0, wui.KeySpace)
	wait("空格取消暂存", func() bool { return idle(rb)() && kindOf(rb, "beta.go") == git.Unstaged })
	tt.Frame()
	if rows := a.GitRows(); a.GitSel < 0 || a.GitSel >= len(rows) {
		t.Errorf("空格之后没有选中了: %d", a.GitSel)
	}
	t.Logf("空格之后选中 %d", a.GitSel)
	if err := clickRow(tt, "beta.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, wui.KeySpace)
	wait("空格暂存", func() bool { return idle(rb)() && kindOf(rb, "beta.go") == git.Staged })

	// ⌘⌫：先问（取消不动）
	tt.Frame()
	if err := clickRow(tt, "beta.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, wui.KeySpace)
	wait("空格取消暂存", func() bool { return idle(rb)() && kindOf(rb, "beta.go") == git.Unstaged })
	tt.Frame()
	if err := clickRow(tt, "beta.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyBackspace)
	tt.Frame()
	mustText(t, tt, "丢弃 beta.go 的改动？")
	mustClick(t, tt, "取消")
	if kindOf(rb, "beta.go") != git.Unstaged {
		t.Errorf("取消了还是丢了")
	}

	// 未暂存组头：丢弃所有未暂存的改动…先问
	hover("未暂存")
	mustClick(t, tt, "丢弃所有未暂存的改动…")
	mustText(t, tt, "丢弃 1 项未暂存的改动？")
	mustClick(t, tt, "取消")
}

// TestScreenshotRowActions 是 Git 栏行内按钮的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一张：
// 悬停在长文件名的未暂存行上（名字省略号截断、按钮贴在最右边），已暂存的一行被键盘选中、也出按钮。
func TestScreenshotRowActions(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	repo := testutil.NewRepo(t, "file_transfer_backend")
	long := "services/storage/database_manager_v2_with_a_really_long_name.py"
	repo.Write(long, "a = 1\n")
	repo.Write("api/routes.py", "r = 1\n")
	repo.Write("README.md", "# x\n")
	repo.Commit("初始")
	repo.Write(long, "a = 2\n")
	repo.Write("api/routes.py", "r = 2\n")
	repo.Git("add", "api/routes.py")
	repo.Write("scripts/migrate.py", "m = 1\n")

	stateDir := t.TempDir()
	st := workspace.Default()
	st.Settings.Shell, st.BottomBar, st.SidebarMode = "/bin/sh", false, workspace.ModeGit
	if err := workspace.SaveState(stateDir, st); err != nil {
		t.Fatal(err)
	}
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), stateDir)
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	a.St.SidebarW = 300
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		if err := clickRow(tt, "routes.py"); err != nil { // 选中已暂存的一行，列表有键盘
			t.Fatal(err)
		}
		tt.Frame()
		b, ok := tt.Find("database_manager_v2_with_a_really_long_name.py")
		if !ok {
			t.Fatalf("没有长文件名那行: %q", tt.Texts())
		}
		tt.Move(b.X+10, b.Y+b.H/2)
		tt.Advance(2 * time.Second)
		tt.Frame()
		tt.Frame()
		name := "row-actions-light.png"
		if dark {
			name = "row-actions-dark.png"
		}
		writeShot(t, tt, dir, name)
	}
}
