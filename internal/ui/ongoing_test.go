package ui

import (
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 进行中横幅，临时仓库：合并停在冲突上时横幅写着合并什么、还有几个冲突，继续是灰的、没有跳过，
// 放弃走原来的确认；暂存之后小字变了、继续能点，点了出合并提交、横幅没了。变基停在第 1 / 2 步：有跳过，先问，跳过后做完。
func TestOngoingBanner(t *testing.T) {
	repo := testutil.ConflictRepo(t, "merge", "c.txt", "1\n2\n", "1\n我的\n", "1\n他们的\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if r.Extra.Ongoing != git.Merging || r.Status.Conflicts != 1 {
		t.Fatalf("应在合并、1 个冲突: %v %+v", r.Extra.Ongoing, r.Status)
	}
	mustText(t, tt, "合并中：Merge branch 'feature'")
	mustText(t, tt, "还有 1 个文件有冲突")
	if shows(tt, "跳过") {
		t.Error("合并不该有跳过")
	}
	if a.CanRun(app.Find("git.continue")) || a.CanRun(app.Find("git.skip")) || !a.CanRun(app.Find("git.abort")) {
		t.Error("有冲突时 Git 菜单：继续、跳过应是灰的，中止能点")
	}
	accessCheck(t, tt, "合并停在冲突上的横幅")
	if err := tt.RightClick("merge"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(t, tt, "继续合并"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("有冲突时右键里的继续合并应是灰的: %v；菜单=%q", err, shownMenu(t, tt))
	}
	closeMenu(t, tt)
	mustClick(t, tt, "继续合并") // 灰的：点了什么都不做（图标按钮，读屏名是"继续合并"）
	if r.Busy != "" || len(r.Queue) != 0 || a.Toast != "" {
		t.Fatalf("灰的继续被点了: busy %q toast %q", r.Busy, a.Toast)
	}
	mustClick(t, tt, "放弃")
	mustText(t, tt, "中止 merge 里进行到一半的合并？")
	mustClick(t, tt, "取消")
	if r.Extra.Ongoing != git.Merging {
		t.Fatal("取消后应还在合并")
	}

	repo.Write("c.txt", "1\n我的\n")
	a.Stage(r, "c.txt")
	wait("暂存", func() bool { return idle(r)() && r.Status.Conflicts == 0 })
	tt.Frame()
	mustText(t, tt, "冲突都解决了：点继续，或在提交框里写信息提交")
	if !a.CanRun(app.Find("git.continue")) {
		t.Error("冲突解决了 Git 菜单里的继续应能点")
	}
	mustClick(t, tt, "继续合并")
	wait("继续合并", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	tt.Frame()
	if shows(tt, "合并中：Merge branch 'feature'") || shows(tt, "放弃") {
		t.Error("合并做完横幅应没了")
	}
	if parents := strings.Fields(repo.Git("rev-list", "--parents", "-n1", "HEAD")); len(parents) != 3 {
		t.Errorf("应是合并提交: %v", parents)
	}

	// 变基停在第 1 / 2 步
	rb := testutil.RebaseRepo(t, "rebase")
	a, tt, wait = newUI(t, testutil.Workspace(t, rb.Root), "")
	r = a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	mustText(t, tt, "变基中（1 / 2）：topic one")
	mustText(t, tt, "topic → main")
	if !a.CanRun(app.Find("git.skip")) {
		t.Error("变基时 Git 菜单里的跳过应能点")
	}
	accessCheck(t, tt, "变基停在冲突上的横幅")
	if err := tt.RightClick("rebase"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if menu := strings.Join(shownMenu(t, tt), "|"); !strings.Contains(menu, "继续变基") || !strings.Contains(menu, "跳过这个提交…") || !strings.Contains(menu, "中止变基…") {
		t.Errorf("右键菜单: %s", menu)
	}
	closeMenu(t, tt)
	mustClick(t, tt, "跳过")
	mustText(t, tt, "跳过 rebase 停在的这个提交？")
	if r.Busy != "" {
		t.Fatal("问的时候就跑了")
	}
	mustClick(t, tt, "跳过这个提交")
	wait("跳过", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	tt.Frame()
	if shows(tt, "变基中（1 / 2）：topic one") {
		t.Error("变基做完横幅应没了")
	}
	if log := rb.Git("log", "--format=%s"); log != "topic two\nmain change\nbase" {
		t.Errorf("跳过后的历史:\n%s", log)
	}
}
