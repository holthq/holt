package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这两个测试用用户的真实工作区，只读：git status / diff / log、ls-files，终端里只敲 pwd。

// 整个窗口：5 个仓库都在文件树里、变更数和分支正确；Git 栏点变更开 Diff 标签，
// 并排 / 统一、局部 / 全文能切；右栏读出历史；切仓库终端跟着切。
func TestWorkbenchView(t *testing.T) {
	if _, err := os.Stat(workspace.DefaultPath); err != nil {
		t.Skip(err)
	}
	a, tt, wait := newUI(t, workspace.DefaultPath, "")
	a.Start()
	wait("读完所有仓库", loaded(a))
	if len(a.Repos) != 5 {
		t.Fatalf("有 %d 个仓库，期望 5 个", len(a.Repos))
	}
	for _, r := range a.Repos {
		mustText(t, tt, r.Name)
		if _, ok := tt.Find(r.Name + "（仓库）"); !ok {
			t.Errorf("文件树里没有仓库根 %s", r.Name)
		}
		if r.Status.Count > 0 && !tt.HasText(strconv.Itoa(r.Status.Count)) {
			t.Errorf("仓库 %s 的变更数 %d 没显示", r.Name, r.Status.Count)
		}
		if !tt.HasText(r.Status.Branch) {
			t.Errorf("仓库 %s 的分支 %s 没显示", r.Name, r.Status.Branch)
		}
	}
	mustText(t, tt, "终端 · "+a.Current().Name)

	// 找一个有未暂存改动的仓库，在文件树里点它
	var repo *app.Repo
	for _, r := range a.Repos {
		if r.Status.Unstaged > 0 && r != a.Current() {
			repo = r
			break
		}
	}
	if repo == nil {
		t.Skip("没有别的仓库有未暂存改动，跳过 diff 检查")
	}
	mustClick(t, tt, repo.Name+"（仓库）")
	if a.Current() != repo {
		t.Fatalf("点了 %s，当前仓库却是 %s", repo.Name, a.Current().Name)
	}
	mustText(t, tt, "终端 · "+repo.Name)

	// Git 栏里点第一项未暂存的变更 → 预览 Diff 标签
	tt.Key(wui.Cmd|wui.Shift, wui.KeyG)
	tt.Frame()
	if a.St.SidebarMode != workspace.ModeGit {
		t.Fatalf("⇧⌘G 没切到 Git 栏: %q", a.St.SidebarMode)
	}
	i := slices.IndexFunc(repo.Status.Changes, func(c git.Change) bool { return c.Kind == git.Unstaged })
	ch := repo.Status.Changes[i]
	base := ch.Path[strings.LastIndex(ch.Path, "/")+1:]
	mustClick(t, tt, base)
	wait("diff 读完", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading && d.Kind == docs.Diff })
	d := a.CurrentDoc()
	if d.Info != "" || d.DiffSt == nil {
		t.Fatalf("diff 显示了说明而不是内容: %s", d.Info)
	}
	if d.Pinned {
		t.Error("单击变更应开预览标签")
	}
	added, deleted := d.DiffSt.Stats()
	t.Logf("%s %s: +%d −%d", repo.Name, ch.Path, added, deleted)
	if added == 0 && deleted == 0 {
		t.Errorf("diff 没有统计到任何改动")
	}
	mustText(t, tt, "diff: "+base)
	// 在标签条上：图标是现在的样子，点了换；"只看改动附近"是开关
	scope := app.Tip(i18n.T("只看改动附近"), "diff.full")
	mustClick(t, tt, app.Tip(i18n.T("并排，点了换成统一"), "diff.unified"))
	if !a.St.Unified {
		t.Errorf("切换统一视图失败")
	}
	mustClick(t, tt, app.Tip(i18n.T("统一，点了换成并排"), "diff.unified"))
	if a.St.Unified {
		t.Errorf("切回并排失败")
	}
	mustClick(t, tt, scope)
	if !a.St.FullDiff || !d.DiffSt.ExpandUnchanged {
		t.Errorf("切换全文失败: full=%v expand=%v", a.St.FullDiff, d.DiffSt.ExpandUnchanged)
	}
	mustClick(t, tt, scope)
	if a.St.FullDiff || d.DiffSt.ExpandUnchanged {
		t.Errorf("切回局部失败: full=%v expand=%v", a.St.FullDiff, d.DiffSt.ExpandUnchanged)
	}

	// 右栏默认开着，会自己读当前仓库的 git log
	wait("历史", func() bool { return repo.Hist.Loaded && !repo.Hist.Loading })
	if repo.Hist.Err != nil {
		t.Errorf("读历史失败: %v", repo.Hist.Err)
	} else if len(repo.Hist.Commits) > 0 && !tt.HasText(repo.Hist.Commits[0].Short) {
		t.Errorf("右栏没显示最新提交 %s", repo.Hist.Commits[0].Short)
	}

	hdr, ok := tt.Find("终端 · " + repo.Name)
	if !ok {
		t.Fatal("找不到底栏的终端标签")
	}
	tt.ClickAt(hdr.X+40, hdr.Y+hdr.H+60)
	tt.Type("pwd")
	tt.Key(0, wui.KeyEnter)
	testutil.WaitTerm(t, tt, activeTerm(t, a), "\n"+repo.Root, 15*time.Second)
}

// 右键菜单和仓库增删：移除后持久化、重开不再出现、恢复回到原位；
// 添加子目录归到仓库根，重复的只选中，非 Git 目录报错。state.json 写在临时目录。
func TestRepoMenuAddRemove(t *testing.T) {
	if _, err := os.Stat(workspace.DefaultPath); err != nil {
		t.Skip(err)
	}
	stateDir := t.TempDir()
	a, tt, _ := newUI(t, workspace.DefaultPath, stateDir)
	n := len(a.Repos)
	if n < 2 {
		t.Skipf("只有 %d 个仓库", n)
	}
	tt.Frame()
	victim := a.Repos[1]

	if err := tt.RightClick(victim.Name + "（仓库）"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, want := range []string{"刷新 Git 状态", "重新启动终端", "在 Finder 中显示", "复制路径", "从列表移除"} {
		if !slices.Contains(menu, want) {
			t.Errorf("右键菜单里没有 %q: %v", want, menu)
		}
	}
	if err := tt.ChooseMenuItem("从列表移除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.Repos) != n-1 || a.RepoFor(victim.Root) != nil {
		t.Fatalf("移除后还有 %d 个仓库，%s 还在", len(a.Repos), victim.Name)
	}
	if !slices.Contains(a.St.Hidden, victim.Root) {
		t.Errorf("工作区仓库移除后应记为隐藏: %v", a.St.Hidden)
	}
	data, _ := os.ReadFile(filepath.Join(stateDir, workspace.StateFile))
	if !strings.Contains(string(data), victim.Root) {
		t.Errorf("state.json 没记下隐藏的仓库:\n%s", data)
	}
	if b, err := app.New(workspace.DefaultPath, stateDir); err != nil || len(b.Repos) != n-1 {
		t.Errorf("重开后应该还是 %d 个仓库: err=%v", n-1, err)
	}

	// 工作区按钮的菜单里恢复
	if err := tt.Click("工作区 " + a.WsName()); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("显示移除的工作区仓库"); err != nil {
		t.Fatalf("%v；菜单=%q", err, tt.Menu())
	}
	tt.Frame()
	if len(a.Repos) != n || a.Repos[1].Root != victim.Root {
		t.Fatalf("恢复后应回到原来的位置: %d 个仓库，第二个是 %s", len(a.Repos), a.Repos[1].Name)
	}
	if len(a.St.Hidden) != 0 {
		t.Errorf("恢复后 hidden 应为空: %v", a.St.Hidden)
	}

	// 子目录归到仓库根，已在列表里的只选中（只跑 git rev-parse）。
	sub := victim.Root
	if entries, err := os.ReadDir(victim.Root); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				sub = filepath.Join(victim.Root, e.Name())
				break
			}
		}
	}
	if err := a.AddRepoDir(sub); err != nil {
		t.Fatal(err)
	}
	if len(a.Repos) != n || a.Selected != 1 {
		t.Errorf("添加 %s 应只选中第二个仓库: repos=%d selected=%d", sub, len(a.Repos), a.Selected)
	}
	if err := a.AddRepoDir(filepath.Join(t.TempDir(), "没有")); err == nil {
		t.Error("不存在的目录应该报错")
	}
	if len(a.Repos) != n || len(a.St.Added) != 0 {
		t.Errorf("失败的添加不该改列表: repos=%d added=%v", len(a.Repos), a.St.Added)
	}
	// 不是 Git 仓库的目录：当普通文件夹加进来，移除后从 added 里删掉
	plain := t.TempDir()
	if err := a.AddRepoDir(plain); err != nil || len(a.Repos) != n+1 || !slices.Contains(a.St.Added, plain) ||
		a.Toast != i18n.Tf("已添加 %s（不是 Git 仓库）", filepath.Base(plain)) {
		t.Fatalf("加普通文件夹：err=%v repos=%d added=%v toast=%q", err, len(a.Repos), a.St.Added, a.Toast)
	}
	a.RemoveRepo(a.Repos[n])
	if len(a.Repos) != n || len(a.St.Added) != 0 {
		t.Errorf("移除普通文件夹后: repos=%d added=%v", len(a.Repos), a.St.Added)
	}
	tt.Frame()
	mustText(t, tt, victim.Name)
}
