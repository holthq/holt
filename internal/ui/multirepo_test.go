package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// above 是界面上 a 画在 b 上面。
func above(t *testing.T, tt *wui.Tester, a, b string) {
	t.Helper()
	ea, ok1 := tt.Find(a)
	eb, ok2 := tt.Find(b)
	if !ok1 || !ok2 {
		t.Fatalf("找不到 %q(%v) / %q(%v)；texts=%q", a, ok1, b, ok2, tt.Texts())
	}
	if ea.Y >= eb.Y {
		t.Errorf("%q（y=%v）应在 %q（y=%v）上面", a, ea.Y, b, eb.Y)
	}
}

// Git 栏同时列出工作区里所有仓库（三个临时仓库，两个有改动、一个干净）：一个仓库一段，没有仓库切换器；
// 点段头折叠、⌘3 / ⌘2 跳段并切当前仓库（右栏的历史跟着换）、点文件切当前仓库；在第二段暂存 + ⌘↩ 提交
// 只动第二个仓库。
func TestGitBarAllRepos(t *testing.T) {
	ra, rb, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	a.St.BottomBar = false // 不起 shell
	alpha, beta, gamma := a.Repos[0], a.Repos[1], a.Repos[2]
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("三个仓库的状态", loaded(a))

	// 三段都在，按工作区里的顺序；干净的 gamma 收成一行"工作区干净"
	for _, s := range []string{"alpha（仓库）", "alpha.txt", "notes.md", "beta（仓库）", "beta.go", "gamma（仓库）", "工作区干净", "提交…"} {
		mustText(t, tt, s)
	}
	above(t, tt, "notes.md", "beta（仓库）")
	above(t, tt, "beta.go", "gamma（仓库）")
	if tt.HasText("切换仓库（⌘1–9）") {
		t.Errorf("gamma 应收着、不该有仓库切换器: %q", tt.Texts())
	}
	if got := tt.HasText(i18n.Tf("提交 %s 的改动…", "alpha")); !got || tt.HasText(i18n.Tf("提交 %s 的改动…", "gamma")) {
		t.Errorf("有改动的段底才有提交框: alpha=%v", got)
	}
	wait("alpha 的历史", func() bool { return tt.HasText("alpha 3") })

	// ⌘3：跳到 gamma、展开它，当前仓库和右栏的历史跟着换
	tt.Key(wui.Cmd, wui.Key3)
	wait("gamma 的历史", func() bool { return tt.HasText("gamma 1") })
	if a.Current() != gamma || !gamma.GitOpen() {
		t.Fatalf("⌘3 后当前 %s，gamma 展开=%v", a.Current().Name, gamma.GitOpen())
	}
	if tt.HasText("alpha 3") {
		t.Error("右栏还是 alpha 的历史")
	}
	tt.Key(wui.Cmd, wui.Key2)
	wait("beta 的历史", func() bool { return tt.HasText("beta 2") })
	if a.Current() != beta {
		t.Fatalf("⌘2 后当前 %s", a.Current().Name)
	}

	// 点段头：收起 / 展开这一段，当前仓库切到它
	mustClickRow(t, tt, "alpha（仓库）")
	if a.Current() != alpha || alpha.GitOpen() || tt.HasText("alpha.txt") {
		t.Errorf("点 alpha 段头后当前 %s，展开=%v", a.Current().Name, alpha.GitOpen())
	}
	mustClickRow(t, tt, "alpha（仓库）")
	mustText(t, tt, "alpha.txt")
	// 工具条的"收起所有仓库" / "展开所有仓库"
	mustClickRow(t, tt, "收起所有仓库")
	if tt.HasText("alpha.txt") || tt.HasText("beta.go") {
		t.Errorf("全部收起后还有行: %q", tt.Texts())
	}
	mustClickRow(t, tt, "展开所有仓库")
	for _, s := range []string{"alpha.txt", "beta.go"} {
		mustText(t, tt, s)
	}
	// 分组标题：箭头收起这一组（标题和"+"还在），点标题再展开
	// 未暂存的 alpha.txt 和未跟踪的 notes.md 同在"未暂存"一组
	mustText(t, tt, "暂存所有未暂存的改动")
	if tt.HasText("未跟踪") {
		t.Errorf("未跟踪不该再单独一组: %q", tt.Texts())
	}
	mustClickRow(t, tt, "收起 未暂存")
	if tt.HasText("notes.md") || tt.HasText("alpha.txt") || !tt.HasText("展开 未暂存") || alpha.GroupOpen(app.GroupUnstaged) {
		t.Fatalf("收起未暂存后: %q", tt.Texts())
	}
	mustClickRow(t, tt, "展开 未暂存")
	mustText(t, tt, "notes.md")
	if !alpha.GroupOpen(app.GroupUnstaged) {
		t.Fatal("点标题没展开")
	}

	// 点 beta 的文件：当前仓库切到 beta，预览 Diff 标签是 beta 的
	mustClickRow(t, tt, "gamma（仓库）") // 收起 gamma，当前仓库先在 gamma
	mustClickRow(t, tt, "beta.go")
	wait("beta.go 的 diff", func() bool { return diffDoc(a, "") != nil })
	if d := diffDoc(a, ""); a.Current() != beta || d.Root != beta.Root || d.Rel != "beta.go" {
		t.Errorf("点 beta.go 后当前 %s，标签 %s", a.Current().Name, d.Rel)
	}

	// alpha 段底的"提交…"：点开就聚焦信息框、当前仓库切到 alpha；空着按 Esc 收回去
	mustClickRow(t, tt, i18n.Tf("提交 %s 的改动…", "alpha"))
	tt.Frame()
	if !alpha.CommitOpen || !tt.Focused("提交信息") || a.Current() != alpha {
		t.Fatalf("点开 alpha 的提交框: open=%v 当前 %s", alpha.CommitOpen, a.Current().Name)
	}
	tt.Key(0, wui.KeyEscape)
	wait("Esc 收起空的提交框", func() bool { return !alpha.CommitOpen && !tt.HasText("提交信息") })

	// 第二段：右键 beta.go 暂存，它的提交框自己展开；⌘↩ 提交只动 beta
	alphaHead := ra.Git("rev-parse", "HEAD")
	mustRightClickRow(t, tt, "beta.go", "暂存")
	wait("暂存 beta.go", func() bool { return idle(beta)() && beta.Status.Staged == 1 })
	mustText(t, tt, i18n.Tf("提交 %s 个文件", "1"))
	mustClickRow(t, tt, "提交信息")
	tt.Type("beta 3")
	tt.Key(wui.Cmd, wui.KeyEnter)
	wait("beta 提交完", func() bool { return idle(beta)() && beta.Status.Count == 0 })
	if got := rb.Git("log", "-1", "--format=%s"); got != "beta 3" {
		t.Errorf("beta 最新提交 %q", got)
	}
	if got := ra.Git("rev-parse", "HEAD"); got != alphaHead {
		t.Error("alpha 的 HEAD 变了")
	}
	a.RefreshRepo(alpha, app.WhyManual)
	wait("alpha", idle(alpha))
	if alpha.Status.Unstaged != 1 || alpha.Status.Untracked != 1 || alpha.Status.Staged != 0 {
		t.Errorf("alpha 的状态变了: %+v", alpha.Status)
	}
	if a.Current() != beta {
		t.Errorf("⌘↩ 提交的是焦点所在段的仓库，当前应是 beta: %s", a.Current().Name)
	}
	// 点过"展开所有仓库"，干净了也还展开着（干净由段头的勾表示，不再写说明行）
	wait("beta 干净了", func() bool {
		return beta.Status.Count == 0 && beta.GitOpen() && !tt.HasText(i18n.Tf("提交 %s 个文件", "1"))
	})
}

// 右栏的"所有仓库"：各仓库的提交按时间交错、每行左边一个仓库圆点，置顶有改动的仓库；在 beta 的提交上
// 右键"从这里新建分支…"只动 beta，当前仓库切过去；点有改动的仓库那行跳到 Git 栏；选择记进 state.json。
func TestHistoryAllRepos(t *testing.T) {
	ra, rb, _, ws := testutil.ThreeRepos(t)
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	a.St.BottomBar = false
	alpha, beta := a.Repos[0], a.Repos[1]
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("三个仓库的状态", loaded(a))
	wait("alpha 的历史", func() bool { return tt.HasText("alpha 3") })
	if tt.HasText("beta 2") {
		t.Fatal("默认右栏只有当前仓库的历史")
	}

	mustClickRow(t, tt, "所有仓库")
	wait("所有仓库的历史", func() bool { return a.AllHistDone() && !a.AllHistLoading() && tt.HasText("beta beta 2") })
	if !a.St.HistAll {
		t.Error("没切到所有仓库")
	}
	if st, err := workspace.LoadState(stateDir); err != nil || !st.HistAll {
		t.Errorf("state.json 没记下所有仓库 (%v)", err)
	}
	// 从新到旧交错；行标签是"仓库名 提交标题"，胶囊上是仓库名
	order := []string{"gamma gamma 1", "alpha alpha 3", "beta beta 2", "alpha alpha 2", "beta beta 1", "alpha alpha 1"}
	for i := 1; i < len(order); i++ {
		above(t, tt, order[i-1], order[i])
	}
	mustText(t, tt, "未提交的改动")
	above(t, tt, "alpha 未提交的改动", "beta 未提交的改动")
	above(t, tt, "beta 未提交的改动", "gamma gamma 1")
	if tt.HasText("gamma 未提交的改动") {
		t.Error("干净的 gamma 不该在未提交的改动里")
	}
	mustText(t, tt, i18n.T("全部仓库"))
	if n := app.AllHistShown(a.AllHistRows()); n != 6 {
		t.Errorf("所有仓库排出 %d 个提交，应为 6", n)
	}

	// 当前仓库是 alpha；在 beta 2 上右键新建分支：对话框说的是 beta，分支只建在 beta
	if a.Current() != alpha {
		t.Fatalf("当前 %s", a.Current().Name)
	}
	b2 := rb.Git("rev-parse", "--short", "HEAD")
	mustRightClickRow(t, tt, "beta beta 2", "从这里新建分支…")
	if a.Current() != beta || !a.Branch.Open {
		t.Fatalf("右键 beta 的提交后当前 %s，对话框 %v", a.Current().Name, a.Branch.Open)
	}
	mustText(t, tt, i18n.Tf("在 %s 里从 %s 新建并切换过去", "beta", i18n.Tf("提交 %s", b2)))
	tt.Type("topic/b")
	tt.Key(0, wui.KeyEnter)
	wait("beta 的新分支", func() bool { return idle(beta)() && beta.Status.Branch == "topic/b" })
	if got := ra.Git("branch", "--list", "topic/b"); got != "" || alpha.Status.Branch != "main" {
		t.Errorf("alpha 不该有新分支: %q %s", got, alpha.Status.Branch)
	}

	// 选中 alpha 2：展开它的文件，当前仓库切回 alpha；点文件开提交里的 Diff 标签
	mustClickRow(t, tt, "alpha alpha 2")
	wait("alpha 2 的文件", func() bool { return alpha.Hist.FilesFor != "" && !alpha.Hist.FilesLoading })
	if a.Current() != alpha {
		t.Errorf("选 alpha 2 后当前 %s", a.Current().Name)
	}
	a2 := alpha.Hist.Expanded
	mustClickRow(t, tt, "alpha.txt")
	wait("alpha 2 里 alpha.txt 的 diff", func() bool { return diffDoc(a, a2) != nil })
	if d := diffDoc(a, a2); d.Root != alpha.Root || d.Change.Kind != git.InCommit {
		t.Errorf("Diff 标签: root=%s kind=%v", d.Root, d.Change.Kind)
	}

	// 有改动的仓库那行：侧栏切到 Git 栏，当前仓库切过去
	a.Exec("view.files")
	tt.Frame()
	mustClickRow(t, tt, "beta 未提交的改动")
	if a.St.SidebarMode != workspace.ModeGit || a.Current() != beta {
		t.Errorf("点 beta 的未提交改动后 mode=%q 当前 %s", a.St.SidebarMode, a.Current().Name)
	}
	mustText(t, tt, "beta（仓库）")

	// 再点"所有仓库"关掉：只剩 beta 自己的
	mustClickRow(t, tt, "所有仓库")
	wait("beta 的历史", func() bool { return tt.HasText("beta 2") && !tt.HasText("alpha 3") })
	if st, _ := workspace.LoadState(stateDir); st.HistAll {
		t.Error("state.json 没记下切回当前仓库")
	}
}

// Git 栏段头的分支名点开那个仓库的分支菜单：不折叠段落、不切当前仓库（别的仓库就地切）；
// 仓库右键菜单（文件树和 Git 栏共用）有"分支"子菜单，顶层不超过 14 项。
func TestGitBarBranchEntry(t *testing.T) {
	_, _, _, ws := testutil.ThreeRepos(t)
	a, tt, wait := newUI(t, ws, "")
	a.St.BottomBar = false
	a.Exec("view.git")
	for _, r := range a.Repos {
		a.RefreshRepo(r, app.WhyManual)
	}
	wait("三个仓库的状态", loaded(a))
	alpha, beta := a.Repos[0], a.Repos[1]
	a.UseRepo(alpha)
	tt.Frame()
	open := beta.GitOpen()
	if err := tt.Click(i18n.Tf("切换 %s 的分支", "beta")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	m := shownMenu(t, tt)
	if !slices.Contains(m, "新建分支…") {
		t.Fatalf("点分支名该开分支菜单：%q", m)
	}
	if beta.GitOpen() != open || a.Current() != alpha {
		t.Errorf("点分支名不该折叠段落、不该切当前仓库：展开 %v→%v，当前 %s", open, beta.GitOpen(), a.Current().Name)
	}
	if o, ok := a.Menu.Owner.(menuOwner); !ok || o.Key != beta.Root {
		t.Errorf("菜单该对着 beta：%+v", a.Menu.Owner)
	}
	closeMenu(t, tt)

	if err := rightClickRow(tt, "beta（仓库）"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	m = shownMenu(t, tt)
	if n := len(slices.DeleteFunc(slices.Clone(m), func(s string) bool { return s == "-" })); n > 14 || !slices.Contains(m, "分支") || !slices.Contains(m, "更多") {
		t.Errorf("仓库菜单（%d 项）：%q", n, m)
	}
	closeMenu(t, tt)
}
