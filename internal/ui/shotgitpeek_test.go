package ui

import (
	"os"
	"slices"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotGitPeek 是改动块就地看等 Git 功能的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark）：
//
//	peek         编辑器装订线上改动块的就地卡：第 1 / 3 处改动、HEAD 里的旧行
//	peek-del     纯删除的那块：旧行“六”
//	stash        Git 栏贮藏展开的三个文件（右键菜单是系统菜单，截不到）
//	search-dots  历史搜 after:2026-10：每行一个点，父子相邻的连短线
//	query-bad    author:alice 加一个写错的日期：两个点不连，框下面“日期看不懂”
//	all-graph    所有仓库：两个仓库的提交按时间穿插，各画各的泳道，被隔开的线淡出淡入
//	web-hosts    设置 → Git 的“网页风格”表
func TestScreenshotGitPeek(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	day := func(m, d int) time.Time { return time.Date(2026, time.Month(m), d, 10, 0, 0, 0, time.Local) }
	p, q := testutil.NewRepo(t, "demo"), testutil.NewRepo(t, "beta")
	p.Write("a.txt", "一\n二\n三\n四\n五\n六\n七\n八\n九\n十\n")
	p.Write("docs/x.md", "文档\n")
	p.CommitAs("初始 登录页", "alice <alice@x>", day(9, 20))
	q.Write("q.txt", "q\n")
	q.CommitAs("乙 初始", "carol <carol@x>", day(9, 28))
	p.Write("docs/x.md", "文档\n改\n")
	p.CommitAs("修文档", "bob <bob@x>", day(10, 5))
	q.Write("q.txt", "q\n登录\n")
	q.CommitAs("乙 登录", "carol <carol@x>", day(10, 10))
	p.Write("a.txt", "一\n二\n叁\n四\n五\n六\n七\n八\n九\n十\n")
	p.CommitAs("登录校验", "alice <alice@x>", day(10, 20))
	p.Write("s1.txt", "甲\n")
	p.Write("s2.txt", "乙\n")
	p.CommitAs("两个文件", "bob <bob@x>", day(10, 25))
	p.Write("s1.txt", "甲改\n")
	p.Write("s2.txt", "乙改\n")
	p.Write("u.txt", "新\n")
	p.Git("stash", "push", "-q", "-u", "-m", "三个文件")
	p.Git("remote", "add", "origin", "git@git.example.com:team/sub/demo.git")
	p.Write("a.txt", "一\n贰\n叁\n四\n五\n七\n八\n九\n九点一\n九点二\n十\n")

	stateDir := t.TempDir()
	a, tt, wait := newUI(t, testutil.Workspace(t, p.Root, q.Root), stateDir)
	showGroups(a)
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	a.St.BottomBar = false
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	r := a.Repos[slices.IndexFunc(a.Repos, func(r *app.Repo) bool { return r.Name == "demo" })]
	wait("状态", func() bool { return idle(a.Repos[0])() && idle(a.Repos[1])() && len(r.Extra.Stashes) == 1 })
	a.Selected = slices.Index(a.Repos, r)
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	d := a.OpenFile(r, "a.txt", true)
	wait("读好 a.txt", func() bool { return d.Buf != nil && d.Base != nil && !d.Loading })
	subjects := func() []string {
		var out []string
		for _, c := range r.Hist.Commits {
			out = append(out, c.Subject)
		}
		return out
	}
	search := func(s string, want ...string) {
		t.Helper()
		a.HistQuery = s
		a.HistQueryChanged()
		wait("搜 "+s, func() bool {
			tt.Frame()
			return r.Hist.Loaded && !r.Hist.Loading && slices.Equal(subjects(), want)
		})
	}
	shot := func(tt *wui.Tester, name string, dark bool) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		suffix := "-light.png"
		if dark {
			suffix = "-dark.png"
		}
		writeShot(t, tt, dir, name+suffix)
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.ShowSidebar(workspace.ModeGit)
		a.SetHistAll(false)
		search("", "两个文件", "登录校验", "修文档", "初始 登录页")

		tt.Frame()
		tt.Frame()
		d.St.OnGutterClick(1)
		shot(tt, "peek", dark)
		d.St.OnGutterClick(5)
		shot(tt, "peek-del", dark)
		a.ClosePeek()

		a.ToggleGitGroup(r, app.GroupStash)
		a.ToggleStash(r, r.Extra.Stashes[0])
		wait("贮藏的文件", func() bool { return !r.StashLoading && len(r.StashFiles) == 3 })
		shot(tt, "stash", dark)
		a.ToggleStash(r, r.Extra.Stashes[0])
		a.ToggleGitGroup(r, app.GroupStash)

		search("after:2026-10", "两个文件", "登录校验", "修文档")
		shot(tt, "search-dots", dark)
		search("author:alice after:2026-13-01", "登录校验", "初始 登录页")
		shot(tt, "query-bad", dark)

		search("", "两个文件", "登录校验", "修文档", "初始 登录页")
		a.SetHistAll(true)
		wait("所有仓库", func() bool { tt.Frame(); return a.AllHistDone() && !a.AllHistLoading() && len(a.AllHistRows()) >= 6 })
		shot(tt, "all-graph", dark)
		a.SetHistAll(false)

		a.St.Settings.WebHosts = []workspace.WebHost{{Host: "git.example.com", Kind: "gitlab"}}
		st := wui.NewTester(SettingsView(a), 640, 520)
		st.SetLocale(i18n.Locale())
		st.SetScale(2)
		st.SetDark(dark)
		st.Frame()
		mustClick(t, st, "Git")
		shot(st, "web-hosts", dark)
	}
}
