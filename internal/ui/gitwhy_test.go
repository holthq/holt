package ui

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// 灰着的 Git 操作写明原因：和 origin 一致时"更多 git 操作"菜单里推送灰着、标签后面括着原因，推送按钮的提示也是；
// 点时再查：菜单开着时领先 1，选之前在外面推了、刷新成和 origin 一致，选了不排命令、提示原因。
func TestGitWhyMenus(t *testing.T) {
	repo := testutil.NewRepo(t, "why")
	repo.Write("a.txt", "a\n")
	repo.Commit("base")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "-u", "origin", "main")

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.UseRepo(r)
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("状态", idle(r))
	tt.Frame()

	mustClick(t, tt, "Git 操作")
	menu := tt.Menu()
	for _, want := range []string{"推送（和 origin 一致）", "同步（拉取再推送）（和 origin 一致）", "暂存全部（没有要暂存的改动）"} {
		if !slices.Contains(menu, want) {
			t.Errorf("菜单里没有 %q: %q", want, menu)
		}
	}
	if err := tt.ChooseMenuItem("推送（和 origin 一致）"); err == nil {
		t.Error("和 origin 一致时推送应灰着")
	}
	tt.CloseMenu()

	// 领先 1：推送能用
	repo.Write("b.txt", "b\n")
	repo.Commit("ahead")
	a.RefreshRepo(r, app.WhyManual)
	wait("领先", func() bool { return idle(r)() && r.Status.Ahead == 1 })
	tt.Frame()
	mustClick(t, tt, "Git 操作")
	if menu := tt.Menu(); !slices.Contains(menu, "推送") {
		t.Fatalf("领先时推送应能用: %q", menu)
	}
	// 菜单开着时别人（另一个 agent）推了：刷新成和 origin 一致
	repo.Git("push", "-q")
	a.RefreshRepo(r, app.WhyManual)
	wait("一致", func() bool { return idle(r)() && r.Status.Ahead == 0 })
	a.Toast = ""
	if err := chooseMenu(t, tt, "推送"); err != nil {
		t.Fatalf("选推送: %v", err)
	}
	tt.Frame()
	if len(r.Queue) > 0 || r.Running() != nil {
		t.Fatal("状态变了以后还排了推送")
	}
	mustText(t, tt, "和 origin 一致") // 提示出原因
}
