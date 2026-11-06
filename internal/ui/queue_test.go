package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/testutil"
)

// slowOp 在仓库里排一条 30 秒的 git 命令（alias 跑 sleep），等着取消。
func slowOp(a *app.App, r *app.Repo) {
	a.RunOp(r, "慢命令", "-c", "alias.slow=!sleep 30", "slow")
}

// choose 选开着的菜单里的一项。
func choose(t *testing.T, tt *wui.Tester, item ...string) {
	t.Helper()
	if err := chooseMenu(t, tt, item...); err != nil {
		t.Fatalf("选 %q: %v；菜单=%q", item, err, shownMenu(t, tt))
	}
	tt.Frame()
	tt.Frame()
}

// namedWs 在临时目录里写一个叫 name.code-workspace 的工作区文件。
func namedWs(t *testing.T, name string, roots ...string) string {
	t.Helper()
	var folders []string
	for _, r := range roots {
		folders = append(folders, `{"path":"`+r+`"}`)
	}
	p := filepath.Join(t.TempDir(), name+".code-workspace")
	if err := os.WriteFile(p, []byte(`{"folders":[`+strings.Join(folders, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// git 写命令的队列：标题栏和 Git 栏段头的队列按钮、队列菜单里移出队列和取消；
// 全部抓取排在别的命令后面时状态栏有进度、点 × 停止；全部拉取跳过没有上游的；退出前问在跑的命令。
func TestQueueUI(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	rb := testutil.NewRepo(t, "b")
	rb.Write("b.txt", "b\n")
	rb.Commit("b")
	a, tt, wait := newUI(t, testutil.Workspace(t, root, rb.Root), "")
	a.St.BottomBar = false
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("两个仓库的状态", loaded(a))
	a.Exec("view.git")
	r := a.Repos[0]
	allIdle := func() bool { return a.Batch == nil && idle(a.Repos[0])() && idle(a.Repos[1])() }

	// 慢命令在跑，暂存全部排在后面
	slowOp(a, r)
	a.StageAll(r)
	tt.Frame()
	if n := countText(tt, "排队 1"); n != 2 {
		t.Errorf("标题栏和 Git 栏段头应各有一个\"排队 1\"：%d", n)
	}
	mustClick(t, tt, "demo 的 git 队列")
	menu := shownMenu(t, tt)
	for _, want := range []string{"正在跑：$ git -c alias.slow=!sleep 30 slow", "取消慢命令", "排队 1 条", "1. 移出队列：暂存全部"} {
		if !slices.Contains(menu, want) {
			t.Errorf("队列菜单里没有 %q：%q", want, menu)
		}
	}
	choose(t, tt, "1. 移出队列：暂存全部")
	if len(r.Queue) != 0 || r.Running() == nil || !slices.Contains(outputTitles(a), "$ git add -A") {
		t.Fatalf("移出队列：queue=%d running=%v 输出=%q", len(r.Queue), r.Running(), outputTitles(a))
	}
	if countText(tt, "排队 1") != 0 {
		t.Error("移出之后还写着排队")
	}
	mustClick(t, tt, "demo 的 git 队列")
	choose(t, tt, "取消慢命令")
	wait("取消后重读状态", idle(r))
	mustText(t, tt, "已取消：慢命令")
	if _, ok := tt.Find("demo 的 git 队列"); ok {
		t.Error("队列空了按钮还在")
	}
	if r.Status.Staged != 0 {
		t.Error("移出队列的暂存全部跑了")
	}

	// 全部抓取排在慢命令后面：状态栏有进度；点 × 停止，后面的仓库不跑
	slowOp(a, r)
	mustClick(t, tt, "全部抓取")
	if a.Batch == nil || len(r.Queue) != 1 {
		t.Fatalf("全部抓取应排在慢命令后面：batch=%v queue=%d", a.Batch, len(r.Queue))
	}
	mustText(t, tt, "抓取 1/2：demo…")
	mustClick(t, tt, "停止全部抓取")
	if a.Batch != nil || len(r.Queue) != 0 || r.Running() == nil {
		t.Fatalf("停止：batch=%v queue=%d running=%v", a.Batch, len(r.Queue), r.Running())
	}
	mustText(t, tt, "全部抓取已停止：成功 0 个，跳过 1 个，没跑 1 个")
	if tt.HasText("抓取 1/2：demo…") {
		t.Error("停了状态栏还有进度")
	}

	// 退出前问在跑的命令：取消就不退
	a.RequestLeave("")
	tt.Frame()
	mustText(t, tt, "还有 git 命令在跑")
	mustText(t, tt, "demo：慢命令 还没跑完。退出会中断正在跑的、不跑排着的。")
	mustClick(t, tt, "取消")
	if a.Leave != nil || a.ForceClose || r.Running() == nil {
		t.Fatalf("取消了还要退出：%+v %v", a.Leave, a.ForceClose)
	}
	a.CancelOp(r)
	wait("取消", allIdle)

	// 都空着：两个仓库都抓（没有远程，什么也不做）；拉取都没有上游，跳过
	mustClick(t, tt, "全部抓取")
	wait("全部抓取完", allIdle)
	mustText(t, tt, "全部抓取完成：成功 2 个")
	mustClick(t, tt, "全部拉取")
	wait("全部拉取完", allIdle)
	mustText(t, tt, "全部拉取完成：成功 0 个，跳过 2 个")
}

// 工作区菜单里的最近工作区：列出别的（不列现在这个），点了换过去；终端里有程序在跑时先问；
// 清除最近工作区之后菜单里没有了，那一项变灰。
func TestRecentWorkspacesUI(t *testing.T) {
	ra, rb := testutil.NewRepo(t, "a"), testutil.NewRepo(t, "b")
	for _, r := range []*testutil.Repo{ra, rb} {
		r.Write("README.md", "# "+filepath.Base(r.Root)+"\n")
		r.Commit("一")
	}
	wsA, wsB := namedWs(t, "甲", ra.Root), namedWs(t, "乙", rb.Root)
	itemA, itemB := "    "+app.WsLabel(wsA), "    "+app.WsLabel(wsB)
	a, tt, wait := termUI(t, wsA, termState(t, true))
	a.Start()
	wait("甲的状态", func() bool { return idle(a.Repos[0])() })
	claimed := func() {
		p := readyPane(t, a, tt)
		wait("认领到 shell 的进程号", func() bool { return p.Job() == "" }) // 刚起的 shell 算"可能有命令在跑"
	}
	claimed()
	a.OpenWorkspace(wsB)
	wait("乙的状态", func() bool { return a.WsName() == "乙" && idle(a.Repos[0])() })
	claimed()

	mustClick(t, tt, "工作区 乙")
	menu := shownMenu(t, tt)
	for _, want := range []string{"打开文件夹…", "打开工作区文件…", "最近的工作区", itemA, "清除最近工作区", "添加仓库…"} {
		if !slices.Contains(menu, want) {
			t.Errorf("工作区菜单里没有 %q：%q", want, menu)
		}
	}
	if slices.Contains(menu, itemB) {
		t.Errorf("列出了现在这个工作区：%q", menu)
	}
	choose(t, tt, itemA)
	if a.WsPath != wsA || a.Leave != nil {
		t.Fatalf("没换到甲：%s %+v", a.WsPath, a.Leave)
	}
	wait("甲的状态", func() bool { return idle(a.Repos[0])() })

	// 终端里有程序在跑：换之前问，取消就不换
	p := readyPane(t, a, tt)
	tt.Type("sleep 30")
	tt.Key(0, wui.KeyEnter)
	wait("sleep 在前台", func() bool { return p.Job() == "sleep" })
	mustClick(t, tt, "工作区 甲")
	choose(t, tt, itemB)
	mustText(t, tt, "关掉终端？")
	mustText(t, tt, "“sleep”还在运行，关掉终端会结束它。")
	mustClick(t, tt, "取消")
	if a.Leave != nil || a.WsPath != wsA || p.Term == nil {
		t.Fatalf("取消了还是换了：%+v %s", a.Leave, a.WsPath)
	}
	mustClick(t, tt, "工作区 甲")
	choose(t, tt, itemB)
	mustClick(t, tt, "结束并切换")
	if a.WsPath != wsB || p.Term != nil {
		t.Fatalf("结束并切换：%s term=%v", a.WsPath, p.Term)
	}
	wait("乙的状态", func() bool { return idle(a.Repos[0])() })

	mustClick(t, tt, "工作区 乙")
	choose(t, tt, "清除最近工作区")
	mustText(t, tt, "已清除最近工作区")
	mustClick(t, tt, "工作区 乙")
	if menu := shownMenu(t, tt); slices.Contains(menu, itemA) || slices.Contains(menu, itemB) {
		t.Errorf("清除后还列着：%q", menu)
	}
	if err := chooseMenu(t, tt, "清除最近工作区"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("清除后\"清除最近工作区\"应变灰：%v", err)
	}
	tt.CloseMenu()
}
