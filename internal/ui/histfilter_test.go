package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 临时仓库上走一遍右栏历史的筛选：搜索框按提交信息 / 作者 / 哈希开头找，"所有分支"开关（记进 state.json），
// 文件树右键"查看目录历史 / 查看文件历史"（文件跟着重命名往前找）和胶囊上的 ×，展开的提交的详情。
func TestHistoryFilter(t *testing.T) {
	repo := testutil.NewRepo(t, "hist")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	repo.Write("b.txt", "b\n")
	repo.Git("add", "-A")
	repo.Git("commit", "-q", "-m", "修复登录", "--author", "张三 <z@example.com>")
	repo.Write("a.txt", "a（改过）\n")
	repo.Commit("改 a")
	repo.Write("docs/.keep", "")
	repo.Git("mv", "a.txt", "docs/a.txt")
	repo.Commit("挪 a")
	repo.Git("switch", "-q", "-c", "feature")
	repo.Write("f.txt", "f\n")
	repo.Commit("分支上的提交")
	repo.Git("switch", "-q", "main")
	ws := testutil.Workspace(t, repo.Root)

	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	subjects := func() []string {
		var out []string
		for _, c := range r.Hist.Commits {
			out = append(out, c.Subject)
		}
		return out
	}
	waitHist := func(what string, want ...string) {
		t.Helper()
		wait(what, func() bool { return r.Hist.Loaded && !r.Hist.Loading && slices.Equal(subjects(), want) })
	}
	all := []string{"挪 a", "改 a", "修复登录", "初始提交"}
	waitHist("历史", all...)

	// 搜索框：提交信息、作者、哈希开头；停手一会儿才重读
	search := func(q string) {
		t.Helper()
		if !tt.HasText("搜索提交") {
			mustClickRow(t, tt, "搜索历史") // 点放大镜展开搜索框
		}
		mustClickRow(t, tt, "搜索提交")
		tt.Key(wui.Cmd, wui.KeyA)
		tt.Key(0, wui.KeyBackspace)
		if q != "" {
			tt.Type(q)
		}
		tt.Frame()
	}
	search("登录")
	waitHist("按信息找", "修复登录")
	search("张三")
	waitHist("按作者找", "修复登录")
	short := repo.Git("rev-parse", "--short=7", "HEAD~1")
	search(short)
	waitHist("按哈希找", "改 a")
	search("没有这个")
	waitHist("找不到")
	mustText(t, tt, "没有对得上的提交")
	search("")
	waitHist("清空搜索", all...)

	// 所有分支：多出分支上的提交，记进 state.json
	mustClickRow(t, tt, "所有分支")
	waitHist("所有分支", append([]string{"分支上的提交"}, all...)...)
	if !a.St.HistBranches {
		t.Fatal("所有分支没记下来")
	}
	b, err := app.New(ws, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !b.St.HistBranches {
		t.Error("state.json 里没有所有分支")
	}
	b.Close()
	mustClickRow(t, tt, "所有分支")
	waitHist("只当前分支", all...)

	// 文件树右键：目录历史只看动过 docs 的提交；文件历史跟着重命名往前找到 a.txt 的提交
	a.ShowSidebar(workspace.ModeFiles)
	a.EnsureFiles(r)
	wait("文件树", func() bool { return r.Files.Loaded && tt.HasText("docs") })
	mustRightClickRow(t, tt, "docs", "更多", "查看目录历史")
	waitHist("目录历史", "挪 a")
	mustText(t, tt, "文件历史：docs")
	a.ToggleDir(r, "docs")
	wait("展开 docs", func() bool { return tt.HasText("a.txt") })
	mustRightClickRow(t, tt, "a.txt", "查看文件历史")
	waitHist("文件历史", "挪 a", "改 a", "初始提交")
	mustText(t, tt, "文件历史：docs/a.txt")
	search("改")
	waitHist("文件历史里再搜", "改 a")
	search("")
	waitHist("文件历史", "挪 a", "改 a", "初始提交")
	mustClickRow(t, tt, "不再只看这个文件")
	waitHist("去掉文件筛选", all...)
	if tt.HasText("文件历史：") {
		t.Error("胶囊没去掉")
	}

	// 展开的提交：卡片上是短哈希和作者；完整哈希、邮箱、父提交在"更多"菜单里
	mustClickRow(t, tt, "修复登录")
	wait("展开", func() bool { return r.Hist.Expanded != "" && !r.Hist.FilesLoading })
	mustText(t, tt, "张三")
	mustText(t, tt, repo.Git("rev-parse", "--short=7", "HEAD~2"))
	if _, ok := tt.Find("复制提交哈希"); !ok {
		t.Error("短哈希胶囊没有标签")
	}
	mustClickRow(t, tt, "更多")
	tt.Frame()
	menu := shownMenu(t, tt)
	for _, want := range []string{repo.Git("rev-parse", "HEAD~2"), "张三 <z@example.com>", "父提交 " + repo.Git("rev-parse", "--short=7", "HEAD~3")} {
		if !slices.Contains(menu, want) {
			t.Errorf("更多菜单里没有 %q：%q", want, menu)
		}
	}
}

// "只看 agent 写的"：只留正文里有 agent 的 Co-Authored-By 的提交（普通人的不算），行上有四角星；和搜索叠加；
// "所有仓库"里也筛；关了回来。
func TestHistoryAgentFilter(t *testing.T) {
	repo := testutil.NewRepo(t, "agent")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	repo.Write("a.txt", "b\n")
	repo.Commit("修登录\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	repo.Write("a.txt", "c\n")
	repo.Commit("加文档\n\nCo-authored-by: 张三 <z@example.com>")
	repo.Write("a.txt", "d\n")
	repo.Commit("改样式\n\nco-authored-by: Codex <codex@openai.com>")
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	subjects := func() []string {
		var out []string
		for _, c := range r.Hist.Commits {
			out = append(out, c.Subject)
		}
		return out
	}
	waitHist := func(what string, want ...string) {
		t.Helper()
		wait(what, func() bool { tt.Frame(); return r.Hist.Loaded && !r.Hist.Loading && slices.Equal(subjects(), want) })
	}
	all := []string{"改样式", "加文档", "修登录", "初始提交"}
	waitHist("历史", all...)
	if got := r.Hist.Commits[2].Agent; got != "Claude" {
		t.Errorf("修登录的 agent 是 %q", got)
	}

	mustClickRow(t, tt, "只看 agent 写的")
	waitHist("只看 agent", "改样式", "修登录")
	if !a.HistFiltered(r) || a.HistGraph(r) {
		t.Errorf("只看 agent 时 HistFiltered=%v HistGraph=%v", a.HistFiltered(r), a.HistGraph(r))
	}

	// 和搜索叠加
	mustClickRow(t, tt, "搜索历史")
	mustClickRow(t, tt, "搜索提交")
	tt.Type("登录")
	tt.Frame()
	waitHist("只看 agent + 搜索", "修登录")
	tt.Key(0, wui.KeyEscape) // Esc：有字先清掉，再按一下收起搜索框
	tt.Frame()
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
	waitHist("收起搜索", "改样式", "修登录")
	if a.HistFind || a.HistQuery != "" {
		t.Errorf("Esc 没收起搜索框：find=%v q=%q", a.HistFind, a.HistQuery)
	}

	// 所有仓库里也只看 agent 的
	a.SetHistAll(true)
	wait("所有仓库", func() bool {
		tt.Frame()
		return a.AllHistDone() && !a.AllHistLoading() && app.AllHistShown(a.AllHistRows()) == 2
	})
	a.SetHistAll(false)
	tt.Frame()

	mustClickRow(t, tt, "只看 agent 写的")
	waitHist("关掉只看 agent", all...)
	if !a.HistGraph(r) {
		t.Error("关掉只看 agent 后提交图没回来")
	}
}
