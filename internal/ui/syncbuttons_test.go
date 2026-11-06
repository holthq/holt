package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
)

// Git 栏每个仓库的段头上各有一组拉取 / 推送按钮，只作用于那个仓库（本地 bare 仓库当远端）：
// alpha 有上游、领先 1 → "↑1"，点推送只推 alpha；回退一个提交后落后 1 → "↓1"，点拉取追上；
// beta 有 origin 没上游 → 拉取灰着、推送是"推送并设置上游"；gamma 没远程，按钮照样在、不点。
// 正在推送时再点不会多排一条。
func TestGitBarSyncButtons(t *testing.T) {
	ra, rb, _, ws := testutil.ThreeRepos(t)
	bareA := filepath.Join(t.TempDir(), "alpha.git")
	ra.Git("init", "-q", "--bare", bareA)
	ra.Git("remote", "add", "origin", bareA)
	ra.Git("push", "-q", "-u", "origin", "HEAD")
	brA := ra.Git("branch", "--show-current")
	ra.Write("more.txt", "more\n")
	ra.Commit("alpha 4") // 领先 1
	bareB := filepath.Join(t.TempDir(), "beta.git")
	rb.Git("init", "-q", "--bare", bareB)
	rb.Git("remote", "add", "origin", bareB) // 有 origin、没上游
	brB := rb.Git("branch", "--show-current")

	a, tt, wait := newUI(t, ws, "")
	a.St.BottomBar = false
	alpha, beta, gamma := a.Repos[0], a.Repos[1], a.Repos[2]
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("三个仓库的状态", loaded(a))
	tt.Frame()

	for _, s := range []string{"拉取 alpha", "推送 alpha", "拉取 beta", "推送 beta", "拉取 gamma", "推送 gamma", "↑1"} {
		mustText(t, tt, s)
	}
	if s := syncOf(a, beta); !s.noUpstream || !s.pullOff || s.pushOff || s.pushTip != "推送并设置上游（git push -u origin）" {
		t.Errorf("beta 没上游时: %+v", s)
	}
	if s := syncOf(a, alpha); s.noUpstream || s.pullOff || s.pushOff || s.ahead != 1 || s.pushTip != "推送（git push），领先 1" {
		t.Errorf("alpha: %+v", s)
	}

	if dir := os.Getenv("WORKBENCH_SHOT"); dir != "" {
		tt.SetScale(2)
		writeShot(t, tt, dir, "gitbar-sync.png")
		tt.SetScale(1)
		tt.Frame()
	}

	// beta 的拉取灰着：点了什么也不排
	open := beta.GitOpen()
	mustClick(t, tt, "拉取 beta")
	if beta.Running() != nil || len(beta.Queue) > 0 {
		t.Fatal("没上游时拉取不该跑")
	}
	if beta.GitOpen() != open {
		t.Error("点灰着的拉取把 beta 这一段收起 / 展开了")
	}

	// 推送 alpha：远端追上，别的仓库没动
	open = alpha.GitOpen()
	mustClick(t, tt, "推送 alpha")
	if alpha.GitOpen() != open {
		t.Error("点推送把 alpha 这一段收起 / 展开了")
	}
	if a.Current() != alpha {
		t.Errorf("点了 alpha 的推送，当前仓库是 %s", a.Current().Name)
	}
	wait("推送 alpha", func() bool { return idle(alpha)() && alpha.Status.Ahead == 0 })
	if got, head := testutil.GitOut(t, bareA, "rev-parse", brA), ra.Git("rev-parse", "HEAD"); got != head {
		t.Fatalf("推送后远端 %s，本地 %s", got, head)
	}
	if beta.Busy != "" || gamma.Busy != "" || app.Upstream(beta) != "" {
		t.Error("推送 alpha 动了别的仓库")
	}

	// 回退一个提交：落后 1，"↓1"，点拉取追上
	ra.Git("reset", "-q", "--hard", "HEAD~1")
	a.RefreshRepo(alpha, app.WhyManual)
	wait("alpha 落后 1", func() bool { return idle(alpha)() && alpha.Status.Behind == 1 })
	tt.Frame()
	mustText(t, tt, "↓1")
	mustClick(t, tt, "拉取 alpha")
	wait("拉取 alpha", func() bool { return idle(alpha)() && alpha.Status.Behind == 0 })
	if got, head := testutil.GitOut(t, bareA, "rev-parse", brA), ra.Git("rev-parse", "HEAD"); got != head {
		t.Fatalf("拉取后本地 %s，远端 %s", head, got)
	}

	// 推送 beta：没上游，推到 origin 并设上游
	mustClick(t, tt, "推送 beta")
	wait("推送 beta", func() bool { return idle(beta)() && app.Upstream(beta) != "" })
	if got, head := testutil.GitOut(t, bareB, "rev-parse", brB), rb.Git("rev-parse", "HEAD"); got != head {
		t.Fatalf("beta 推送后远端 %s，本地 %s", got, head)
	}

	// 正在推送（排着一条）：按钮转圈、点不了，不会多排一条
	gamma.Queue = append(gamma.Queue, &app.Op{Label: "推送"})
	tt.Frame()
	if s := syncOf(a, gamma); !s.pushing || !s.pushOff || s.pushTip != "正在推送…" {
		t.Errorf("排着推送时: %+v", s)
	}
	mustClick(t, tt, "推送 gamma")
	if len(gamma.Queue) != 1 {
		t.Errorf("正在推送时又排了一条: %d", len(gamma.Queue))
	}
	gamma.Queue = nil
	tt.Frame()
}
