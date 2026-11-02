package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 临时仓库（推到临时的裸仓库）上走一遍提交框的几样：历史里"未推送"的标记、勾"修改上一次提交"只改信息、
// ↑ / ↓ 翻以前写过的提交信息、修改已经推送过的提交先问。
func TestCommitAmend(t *testing.T) {
	repo := testutil.NewRepo(t, "amend")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Write("a.txt", "a\n")
	repo.Commit("第一次")
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "-u", "origin", "main")
	repo.Write("b.txt", "b\n")
	repo.Commit("第二次")
	ws := testutil.Workspace(t, repo.Root)

	a, tt, wait := newUI(t, ws, "")
	a.St.CommitMsgs = []string{"甲", "乙"}
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	head := repo.Git("rev-parse", "HEAD")
	if !r.Extra.HasRemote || !r.Extra.Unpushed[head] || len(r.Extra.Unpushed) != 1 || r.Extra.HeadPushed() {
		t.Fatalf("应只有 HEAD 没推送: %+v", r.Extra)
	}

	// 右栏：没推送的那个提交带标记
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 2 })
	if n := countText(tt, "未推送"); n != 1 {
		t.Errorf("历史里应有 1 个未推送标记，有 %d 个", n)
	}

	// 工作区干净、没暂存也能修改上一次提交（段头右键）：框里换成上一次的信息，改了提交，提交数不变
	mustRightClick(t, tt, "amend（仓库）", "修改上一次提交…")
	if !r.Amend {
		t.Fatal("右键菜单没勾上修改上一次提交")
	}
	wait("上一次的信息", func() bool { return r.CommitMsg == "第二次" })
	mustText(t, tt, "只改上一次提交的信息")
	focusCommitBox(t, tt)
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("第二次（改过）")
	tt.Frame()
	tt.Key(wui.Cmd, wui.KeyEnter)
	wait("修改完成", func() bool { return idle(r)() && !r.Amend && r.CommitMsg == "" })
	if got := repo.Git("log", "-1", "--format=%s"); got != "第二次（改过）" {
		t.Errorf("最新提交是 %q", got)
	}
	if n := repo.Git("rev-list", "--count", "HEAD"); n != "2" {
		t.Errorf("修改后应还是 2 个提交，有 %s 个", n)
	}
	if want := []string{"第二次（改过）", "甲", "乙"}; !slices.Equal(a.St.CommitMsgs, want) {
		t.Errorf("提交信息历史 %q，应为 %q", a.St.CommitMsgs, want)
	}
	if !slices.Contains(outputTitles(a), "$ git commit -q -m 第二次（改过） --amend") {
		t.Errorf("输出栏没记这条命令: %q", outputTitles(a))
	}

	// 空着的提交框里 ↑ 往前翻、↓ 往回翻，翻过最近那条回到空；写了别的 ↑ 不动
	a.OpenCommit(r)
	tt.Frame()
	focusCommitBox(t, tt)
	for _, step := range []struct {
		key  wui.Key
		want string
	}{{wui.KeyUp, "第二次（改过）"}, {wui.KeyUp, "甲"}, {wui.KeyUp, "乙"}, {wui.KeyUp, "乙"}, {wui.KeyDown, "甲"},
		{wui.KeyDown, "第二次（改过）"}, {wui.KeyDown, ""}} {
		tt.Key(0, step.key)
		tt.Frame()
		if r.CommitMsg != step.want {
			t.Fatalf("按 %v 后框里是 %q，应为 %q", step.key, r.CommitMsg, step.want)
		}
	}
	tt.Type("x")
	tt.Key(0, wui.KeyUp)
	tt.Frame()
	if r.CommitMsg != "x" {
		t.Errorf("写了字再按 ↑ 不该翻: %q", r.CommitMsg)
	}
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Key(0, wui.KeyBackspace)
	tt.Frame()

	// 推送之后再修改：先问，取消就不动
	repo.Git("push", "-q", "origin", "main")
	a.RefreshRepo(r, app.WhyManual)
	wait("推送后的状态", func() bool { return idle(r)() && r.Extra.HeadPushed() })
	wait("标记消失", func() bool { return countText(tt, "未推送") == 0 })
	mustClick(t, tt, "修改上一次提交")
	wait("上一次的信息", func() bool { return r.CommitMsg == "第二次（改过）" })
	mustClick(t, tt, "修改提交")
	mustText(t, tt, "修改已经推送过的提交？")
	mustClick(t, tt, "取消")
	if got := repo.Git("rev-parse", "HEAD"); got == head || got != repo.Git("rev-parse", "origin/main") {
		t.Errorf("取消后 HEAD 不该变: %s", got)
	}
	// 取消勾选：框里放回勾上前写的（空）
	mustClick(t, tt, "修改上一次提交")
	if r.Amend || r.CommitMsg != "" {
		t.Errorf("取消勾选后 amend=%v 框里 %q", r.Amend, r.CommitMsg)
	}
}

// 临时仓库里合并出冲突：段头标"合并中"，冲突分组上"中止合并"先问，确认后回到合并前。
func TestAbortMerge(t *testing.T) {
	repo := testutil.NewRepo(t, "conflict")
	repo.Write("c.txt", "原来\n")
	repo.Commit("初始提交")
	repo.Git("switch", "-q", "-c", "feature")
	repo.Write("c.txt", "分支上改的\n")
	repo.Commit("分支上改")
	repo.Git("switch", "-q", "main")
	repo.Write("c.txt", "主线上改的\n")
	repo.Commit("主线上改")
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = repo.Root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("应有冲突: %s", out)
	}
	ws := testutil.Workspace(t, repo.Root)

	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if r.Extra.Ongoing != git.Merging || r.Status.Conflicts != 1 {
		t.Fatalf("应在合并、1 个冲突: %v %+v", r.Extra.Ongoing, r.Status)
	}
	mustText(t, tt, "合并中")
	if !a.CanRun(app.Find("git.abort")) {
		t.Error("Git 菜单里的中止合并应能点")
	}
	mustClick(t, tt, "中止合并")
	mustText(t, tt, "中止 conflict 里进行到一半的合并？")
	mustClick(t, tt, "取消")
	if _, err := os.Stat(filepath.Join(repo.Root, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("取消后应还在合并")
	}
	mustClick(t, tt, "中止合并")
	mustClick(t, tt, "中止")
	wait("中止完成", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	if r.Status.Count != 0 || shows(tt, "合并中") || shows(tt, "中止合并") {
		t.Errorf("中止后应干净: %+v", r.Status)
	}
	if got, _ := os.ReadFile(filepath.Join(repo.Root, "c.txt")); string(got) != "主线上改的\n" {
		t.Errorf("中止后 c.txt 是 %q", got)
	}
	if !slices.Contains(outputTitles(a), "$ git merge --abort") {
		t.Errorf("输出栏没记这条命令: %q", outputTitles(a))
	}
}

// focusCommitBox 点进提交信息框（光标放在最后）。
func focusCommitBox(t *testing.T, tt *wui.Tester) {
	t.Helper()
	mustClick(t, tt, "提交信息")
	tt.Key(wui.Cmd, wui.KeyDown)
	tt.Frame()
}

// countText 是界面上正好是 s 的文字有几处。
func countText(tt *wui.Tester, s string) int {
	n := 0
	for _, x := range tt.Texts() {
		if x == s {
			n++
		}
	}
	return n
}

// outputTitles 是输出栏里每条命令的标题。
func outputTitles(a *app.App) []string {
	var out []string
	for _, o := range a.Output {
		out = append(out, o.Title)
	}
	return out
}
