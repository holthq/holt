package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
)

// remoteRepos 建全部抓取 / 拉取用的三个临时仓库和一个本地的裸仓库当远程：
// ok 跟踪远程，远程上比它多一个提交（能快进）；bad 跟踪的远程目录已经删了（抓取、拉取都失败）；
// none 没有远程（抓取什么也不做，拉取跳过）。
func remoteRepos(t *testing.T) (ok, bad, none *testutil.Repo, ws, remoteHead string) {
	t.Helper()
	dir := t.TempDir()
	bare := func(name string) string {
		p := filepath.Join(dir, name)
		if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", p).CombinedOutput(); err != nil {
			t.Fatalf("git init --bare: %v\n%s", err, out)
		}
		return p
	}
	origin, gone := bare("origin.git"), bare("gone.git")

	ok = testutil.NewRepo(t, "ok")
	ok.Write("a.txt", "一\n")
	ok.Commit("ok 1")
	ok.Git("remote", "add", "origin", origin)
	ok.Git("push", "-q", "-u", "origin", "main")
	other := testutil.NewRepo(t, "other") // 别人推了一个提交上去
	other.Git("remote", "add", "origin", origin)
	other.Git("pull", "-q", "origin", "main")
	other.Write("b.txt", "二\n")
	other.Commit("other 2")
	other.Git("push", "-q", "origin", "main")
	remoteHead = other.Git("rev-parse", "HEAD")

	bad = testutil.NewRepo(t, "bad")
	bad.Write("c.txt", "三\n")
	bad.Commit("bad 1")
	bad.Git("remote", "add", "origin", gone)
	bad.Git("push", "-q", "-u", "origin", "main")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	none = testutil.NewRepo(t, "none")
	none.Write("d.txt", "四\n")
	none.Commit("none 1")
	return ok, bad, none, testutil.Workspace(t, ok.Root, bad.Root, none.Root), remoteHead
}

// outputsFor 是输出栏里仓库 name 的条目标题。
func outputsFor(a *App, name string) []string {
	var titles []string
	for _, e := range a.Output {
		if e.Repo == name {
			titles = append(titles, e.Title)
		}
	}
	return titles
}

func TestFetchAllPullAll(t *testing.T) {
	ok, _, _, ws, remoteHead := remoteRepos(t)
	a, wait := newTestApp(t, ws)
	if len(a.Repos) != 3 {
		t.Fatalf("有 %d 个仓库", len(a.Repos))
	}
	allIdle := func() bool {
		for _, r := range a.Repos {
			if !idle(r)() {
				return false
			}
		}
		return a.Batch == nil
	}
	a.RefreshAll(WhyManual)
	wait("三个仓库的状态", allIdle)

	// 全部抓取：按 Git 栏的顺序一个跑完再下一个；bad 失败不挡后面的
	a.Exec("git.fetchAll")
	b := a.Batch
	if b == nil || b.Total != 3 || b.Cur != a.Repos[0] || a.Repos[0].Busy != "抓取" || a.Repos[1].Busy != "" {
		t.Fatalf("应从第一个仓库开始：%+v", b)
	}
	a.Exec("git.fetchAll") // 跑着时再按：提示，不排第二遍
	if a.Toast != "正在全部抓取（1/3）" {
		t.Errorf("再按：toast=%q", a.Toast)
	}
	wait("全部抓取", allIdle)
	if a.Toast != "全部抓取完成：成功 2 个，失败 1 个（bad）" {
		t.Errorf("汇总：%q", a.Toast)
	}
	for _, name := range []string{"ok", "bad", "none"} {
		if got := outputsFor(a, name); len(got) != 1 || got[0] != "$ git fetch --prune" {
			t.Errorf("%s 的输出：%v", name, got)
		}
	}
	if r := a.Repos[0]; r.Status.Behind != 1 {
		t.Errorf("抓取后 ok 应落后 1：%+v", r.Status)
	}
	if a.AlertOpen {
		t.Errorf("全部抓取不弹失败窗口：%q", a.AlertMsg)
	}

	// 全部拉取：ok 快进，bad 失败，none 没有上游跳过
	a.Output = nil
	a.Exec("git.pullAll")
	wait("全部拉取", allIdle)
	if a.Toast != "全部拉取完成：成功 1 个，失败 1 个（bad），跳过 1 个" {
		t.Errorf("汇总：%q", a.Toast)
	}
	if got := ok.Git("rev-parse", "HEAD"); got != remoteHead {
		t.Errorf("ok 没快进到远程：%s ≠ %s", got, remoteHead)
	}
	if got := outputsFor(a, "ok"); len(got) != 1 || got[0] != "$ git pull --ff-only" {
		t.Errorf("ok 的输出：%v", got)
	}
	if got := outputsFor(a, "none"); len(got) != 1 || got[0] != "全部拉取" || !strings.Contains(a.Output[len(a.Output)-1].Text, "没有上游") {
		t.Errorf("none 的输出：%v", got)
	}
}

// slowOp 排一条 30 秒的 git 命令（alias 跑 sleep），等取消。
func slowOp(a *App, r *Repo) {
	a.RunOp(r, "慢命令", "-c", "alias.slow=!sleep 30", "slow")
}

func TestQueueCancel(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))

	// 同一个仓库：第二条排在第一条后面；取消第一条，第二条接着跑
	slowOp(a, r)
	a.StageAll(r)
	if r.Running() == nil || r.Running().Label != "慢命令" || len(r.Queue) != 1 {
		t.Fatalf("应一条在跑一条排队：%v %d", r.Running(), len(r.Queue))
	}
	a.Exec("git.cancel")
	wait("取消后跑排着的", func() bool { return idle(r)() && r.Status.Staged == 2 })
	if len(a.Output) != 2 || !a.Output[0].Failed || !strings.HasSuffix(a.Output[0].Text, "已取消：慢命令") || a.Output[1].Title != "$ git add -A" {
		t.Fatalf("输出：%+v", a.Output)
	}
	if a.AlertOpen {
		t.Errorf("没留下锁文件不该弹窗：%q", a.AlertMsg)
	}

	// 取消时仓库里有 index.lock：提醒，不删
	lock := filepath.Join(root, ".git", "index.lock")
	slowOp(a, r)
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.CancelOp(r)
	wait("取消", func() bool { return r.Running() == nil && a.AlertOpen })
	if a.AlertTitle != "取消后留下了锁文件" || !strings.Contains(a.AlertMsg, "index.lock") {
		t.Errorf("锁文件提醒：%q %q", a.AlertTitle, a.AlertMsg)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("不该删锁文件：%v", err)
	}
	os.Remove(lock)
	a.AlertOpen = false

	// 全部抓取排在别的命令后面时停止：它移出队列，后面的仓库不跑
	wait("读状态", idle(r))
	slowOp(a, r)
	a.FetchAll()
	if len(r.Queue) != 1 || a.Batch == nil {
		t.Fatalf("抓取应排在慢命令后面：%d", len(r.Queue))
	}
	a.Exec("git.stopAll")
	if a.Batch != nil || len(r.Queue) != 0 || a.Toast != "全部抓取已停止：成功 0 个，跳过 1 个" {
		t.Errorf("停止：batch=%v queue=%d toast=%q", a.Batch, len(r.Queue), a.Toast)
	}
	if w := a.WritesRunning(); len(w) != 1 || w[0] != "demo：慢命令" {
		t.Errorf("还在跑的：%v", w)
	}

	// 关掉（切工作区、退出）：在跑的取消，回调只记输出
	n := len(a.Output)
	a.Close()
	wait("关掉后取消", func() bool { return r.Running() == nil })
	if last := a.Output[len(a.Output)-1]; len(a.Output) != n+1 || !strings.Contains(last.Text, "已取消：慢命令") {
		t.Errorf("关掉后的输出：%+v", last)
	}
}
