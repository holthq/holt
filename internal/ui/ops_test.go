package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 会改仓库的操作只在临时仓库上测：用户的 5 个仓库从头到尾只读。

// diffDoc 是当前标签，当它是读完了的、指定提交（空 = 工作区）的 Diff 标签时。
func diffDoc(a *app.App, commit string) *docs.Doc {
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || d.Loading || d.Commit != commit || d.DiffSt == nil {
		return nil
	}
	return d
}

// 临时仓库上走一遍界面里的 git 操作：Git 栏右键暂存、提交框提交、右栏展开提交看文件的 diff、
// 全文 / 局部切换、分支菜单切分支、新建分支、⌘K 在命令面板里跑 `>git` 命令、确认后删未跟踪文件、
// 出错提示、三个区域的开关和持久化。
func TestGitOps(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	if len(a.Repos) != 1 {
		t.Fatalf("有 %d 个仓库", len(a.Repos))
	}
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	if r.Status.Branch != "main" || r.Status.Unstaged != 1 || r.Status.Untracked != 1 || r.Status.Staged != 0 {
		t.Fatalf("初始状态不对: %+v", r.Status)
	}
	mustText(t, tt, "提交…") // 没暂存时提交框收成一行
	mustText(t, tt, "历史")

	// Git 栏右键 a.txt → 暂存
	if err := rightClickRow(tt, "a.txt"); err != nil {
		t.Fatal(err)
	}
	menu := shownMenu(t, tt)
	for _, want := range []string{"在编辑器中打开", "暂存", "丢弃改动…", "在 Finder 中显示", "复制仓库内路径"} {
		if !slices.Contains(menu, want) {
			t.Errorf("变更右键菜单里没有 %q: %v", want, menu)
		}
	}
	if err := chooseMenu(t, tt, "暂存"); err != nil {
		t.Fatal(err)
	}
	wait("暂存完成", func() bool { return idle(r)() && r.Status.Staged == 1 })
	if r.Status.Unstaged != 0 {
		t.Errorf("暂存后还有未暂存: %+v", r.Status)
	}

	// 有已暂存的改动，提交框自己展开：敲信息，⌘↩ 提交
	mustClickRow(t, tt, "提交信息")
	tt.Type("改第 20 行")
	tt.Frame()
	if r.CommitMsg != "改第 20 行" {
		t.Fatalf("提交信息没进字段: %q", r.CommitMsg)
	}
	tt.Key(wui.Cmd, wui.KeyEnter)
	wait("提交完成", func() bool { return idle(r)() && r.Status.Staged == 0 && r.CommitMsg == "" })
	if got := testutil.GitOut(t, root, "log", "-1", "--format=%s"); got != "改第 20 行" {
		t.Errorf("最新提交是 %q", got)
	}
	if r.Status.Count != 1 || r.Status.Untracked != 1 {
		t.Errorf("提交后应只剩 u.txt 未跟踪: %+v", r.Status)
	}

	// 右栏：3 个提交；点"加 a.txt"展开，点它的 a.txt 开提交里的 Diff 标签（整个文件是新增的）
	h := &r.Hist
	wait("历史读完", func() bool { return h.Loaded && !h.Loading && len(h.Commits) == 3 })
	if h.Commits[0].Subject != "改第 20 行" || h.Commits[2].Subject != "初始提交" {
		t.Fatalf("历史顺序不对: %+v", h.Commits)
	}
	mustText(t, tt, "加 a.txt")
	mustClickRow(t, tt, "加 a.txt")
	second := h.Commits[1].Hash
	wait("提交的文件列表", func() bool { return h.FilesFor == second && !h.FilesLoading })
	if len(h.Files) != 1 || h.Files[0].Path != "a.txt" || h.Files[0].Code != 'A' {
		t.Fatalf("提交里的文件不对: %+v", h.Files)
	}
	mustClickRow(t, tt, "a.txt")
	wait("提交里 a.txt 的 diff", func() bool { return diffDoc(a, second) != nil })
	d := diffDoc(a, second)
	// diff 视图把末尾换行后的空行也算一行，所以是 40 或 41
	if added, deleted := d.DiffSt.Stats(); added < 40 || added > 41 || deleted != 0 {
		t.Errorf("新增文件的 diff 应是 +40 −0，得到 +%d −%d", added, deleted)
	}
	if d.Change.Kind != git.InCommit || d.Pinned {
		t.Errorf("应是提交里的预览 Diff 标签: %+v pinned=%v", d.Change, d.Pinned)
	}
	mustText(t, tt, "diff: a.txt")
	mustText(t, tt, "a.txt @ "+git.Short(second)) // 面包屑

	// 最新那个提交只改了一行：局部 diff 折叠大部分，全文展开整个文件；预览标签被顶掉
	mustClickRow(t, tt, "改第 20 行")
	first := h.Commits[0].Hash
	wait("最新提交的文件", func() bool { return h.FilesFor == first && !h.FilesLoading })
	mustClickRow(t, tt, "a.txt")
	wait("最新提交的 diff", func() bool { return diffDoc(a, first) != nil })
	d = diffDoc(a, first)
	if len(a.Docs) != 1 {
		t.Errorf("预览 Diff 标签应被顶掉，现在有 %d 个标签", len(a.Docs))
	}
	if added, deleted := d.DiffSt.Stats(); added != 1 || deleted != 1 {
		t.Errorf("改一行的 diff 应是 +1 −1，得到 +%d −%d", added, deleted)
	}
	if d.DiffSt.ExpandUnchanged {
		t.Error("默认应是局部 diff")
	}
	mustClickRow(t, tt, app.Tip(i18n.T("只看改动附近"), "diff.full")) // 关掉就是全文
	if !a.St.FullDiff || !d.DiffSt.ExpandUnchanged {
		t.Errorf("点全文后 full=%v expand=%v", a.St.FullDiff, d.DiffSt.ExpandUnchanged)
	}
	tt.Key(wui.Cmd|wui.Alt, wui.KeyF)
	tt.Frame()
	if a.St.FullDiff || d.DiffSt.ExpandUnchanged {
		t.Errorf("⌥⌘F 应切回局部: full=%v expand=%v", a.St.FullDiff, d.DiffSt.ExpandUnchanged)
	}

	// 标题栏的分支胶囊：列出 main（当前）和 feature，切到 feature
	mustClickRow(t, tt, "分支 main")
	menu = shownMenu(t, tt)
	if !slices.Contains(menu, "main") || !slices.Contains(menu, "feature") || !slices.Contains(menu, "新建分支…") {
		t.Fatalf("分支菜单: %v", menu)
	}
	if err := chooseMenu(t, tt, "feature"); err != nil {
		t.Fatal(err)
	}
	wait("切到 feature", func() bool { return idle(r)() && r.Status.Branch == "feature" })
	if got := testutil.GitOut(t, root, "branch", "--show-current"); got != "feature" {
		t.Errorf("实际分支是 %q", got)
	}
	wait("feature 的历史", func() bool {
		return h.Loaded && !h.Loading && len(h.Commits) == 3 && h.Commits[0].Subject == "feature 提交"
	})
	if _, ok := tt.Find("分支 feature"); !ok {
		t.Errorf("标题栏没显示新分支: %q", tt.Texts())
	}

	// "Git 操作"菜单 → 新建分支… → 对话框里敲名字回车
	mustClickRow(t, tt, "Git 操作")
	menu = shownMenu(t, tt)
	for _, want := range []string{"抓取（fetch --prune）（没有远程）", "推送并设置上游（没有上游，也没有 origin）", "暂存全部",
		"贮藏改动（stash）（没有可贮藏的改动）", "新建分支…", "运行 git 命令…"} {
		if !slices.Contains(menu, want) {
			t.Errorf("Git 菜单里没有 %q: %v", want, menu)
		}
	}
	if err := chooseMenu(t, tt, "新建分支…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.Branch.Open || !tt.HasText("在 demo 里从 feature 新建并切换过去") {
		t.Fatalf("新建分支对话框没开: open=%v texts=%q", a.Branch.Open, tt.Texts())
	}
	tt.Type("topic/x")
	tt.Key(0, wui.KeyEnter)
	wait("新分支", func() bool { return idle(r)() && r.Status.Branch == "topic/x" })
	if a.Branch.Open || a.Branch.Name != "" {
		t.Errorf("对话框应已关闭并清空: %+v", a.Branch)
	}

	// ⌘K 打开命令面板并带着 ">git "：回车在当前仓库跑，面板关掉，输出记进底栏的"输出"并切过去
	n0 := len(a.Output)
	tt.Key(wui.Cmd, wui.KeyK)
	tt.Frame()
	if !a.Palette.Open || a.Palette.Text != ">git " || !tt.Focused("快速打开或命令") {
		t.Fatalf("⌘K 没带着 >git 打开面板: open=%v text=%q", a.Palette.Open, a.Palette.Text)
	}
	tt.Type("branch --show-current")
	tt.Frame()
	mustText(t, tt, "在 demo · topic/x 里运行")
	tt.Key(0, wui.KeyEnter)
	wait("命令跑完", func() bool { return idle(r)() && len(a.Output) > n0 })
	tt.Frame()
	if a.Palette.Open || a.Palette.Text != "" {
		t.Errorf("回车后面板应关掉并清空: %+v", a.Palette.Text)
	}
	if last := a.Output[len(a.Output)-1]; last.Failed || last.Title != "$ git branch --show-current" || last.Text != "topic/x" {
		t.Errorf("命令输出: %+v", last)
	}
	if a.BottomTab != app.BottomOutput || !a.St.BottomBar {
		t.Errorf("跑完应切到底栏的输出: tab=%d bar=%v", a.BottomTab, a.St.BottomBar)
	}
	if len(a.Palette.History) != 1 || a.Palette.History[0] != "branch --show-current" {
		t.Errorf("命令历史不对: %v", a.Palette.History)
	}
	// 再开：跑过的列在下面，回车把它填回输入框（不直接跑）；改成一条会失败的命令
	tt.Key(wui.Cmd, wui.KeyK)
	tt.Frame()
	mustText(t, tt, "git branch --show-current")
	tt.Key(0, wui.KeyEnter)
	tt.Frame()
	if !a.Palette.Open || a.Palette.Text != ">git branch --show-current" {
		t.Fatalf("选跑过的命令应填回输入框: open=%v text=%q", a.Palette.Open, a.Palette.Text)
	}
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type(">git no-such-subcommand")
	tt.Key(0, wui.KeyEnter)
	wait("失败的命令", func() bool { return idle(r)() && a.Output[len(a.Output)-1].Failed })
	tt.Frame()
	n := len(a.Output)
	if n < 2 || a.Output[n-2].Title != "$ git branch --show-current" || !a.Output[n-1].Failed {
		t.Errorf("输出里没记下两条命令: %+v", a.Output)
	}
	mustClickRow(t, tt, "输出 "+itoa(n))
	if a.BottomTab != app.BottomOutput || !tt.HasText("$ git no-such-subcommand") {
		t.Errorf("底栏的输出标签没显示命令: tab=%d", a.BottomTab)
	}

	// Git 栏右键 u.txt → 删除文件… → 先取消，再确认
	if err := rightClickRow(tt, "u.txt"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(t, tt, "删除文件…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Confirm == nil || !tt.HasText("删除未跟踪的 u.txt？") {
		t.Fatalf("没弹确认: confirm=%v texts=%q", a.Confirm, tt.Texts())
	}
	mustClickRow(t, tt, "取消")
	if a.Confirm != nil || a.ConfirmOpen {
		t.Error("取消后确认框还在")
	}
	if _, err := os.Stat(filepath.Join(root, "u.txt")); err != nil {
		t.Fatal("取消后文件不该被删")
	}
	mustRightClickRow(t, tt, "u.txt", "删除文件…")
	mustClickRow(t, tt, "删除")
	wait("文件删掉", func() bool { return idle(r)() && r.Status.Count == 0 })
	if _, err := os.Stat(filepath.Join(root, "u.txt")); !os.IsNotExist(err) {
		t.Errorf("u.txt 还在: %v", err)
	}
	mustText(t, tt, "工作区干净")

	// 没有上游时拉取按钮灰着；直接调（点时再查）也不跑，提示原因
	a.Pull(r)
	tt.Frame()
	if r.Running() != nil || len(r.Queue) > 0 || a.AlertOpen {
		t.Error("没有上游时拉取不该跑")
	}
	mustText(t, tt, "没有上游")

	// 区域：⌘J 关底栏，右栏的 × 关右栏，标题栏按钮再开；设置写进 state.json，重开生效
	tt.Key(wui.Cmd, wui.KeyJ)
	tt.Frame()
	if a.St.BottomBar || tt.HasText("终端 · demo") {
		t.Error("⌘J 没关掉底栏")
	}
	mustClickRow(t, tt, app.Tip("关闭右栏", "view.right"))
	if a.St.RightBar || tt.HasText("加 a.txt") {
		t.Error("× 没关掉右栏")
	}
	mustClickRow(t, tt, app.Tip("右栏（历史）", "view.right"))
	if !a.St.RightBar || !tt.HasText("加 a.txt") {
		t.Error("标题栏按钮没打开右栏")
	}
	st, err := workspace.LoadState(stateDir)
	if err != nil || !st.Sidebar || st.SidebarMode != workspace.ModeGit || !st.RightBar || st.BottomBar || st.FullDiff {
		t.Errorf("state.json 没记下区域开关 (%v): %+v", err, st)
	}
	b, err := app.New(ws, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if b.St.BottomBar || !b.St.RightBar || b.St.SidebarMode != workspace.ModeGit {
		t.Errorf("重开后区域 %+v", b.St)
	}
}

// 历史分页：200 个提交时第一页 200 条（标题 200+），"再读"一次后读完。
func TestHistoryPaging(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	cmd := exec.Command("sh", "-c", `for i in $(seq 1 198); do echo $i > n.txt && git add n.txt && git -c user.name=测试 -c user.email=t@example.com commit -q -m "第 $i 次" || exit 1; done`)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("造提交: %v\n%s", err, out)
	}
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	h := &r.Hist
	wait("历史第一页", func() bool { return h.Loaded && !h.Loading })
	if len(h.Commits) != git.LogPage || h.Done {
		t.Fatalf("第一页 %d 条 done=%v", len(h.Commits), h.Done)
	}
	mustText(t, tt, i18n.Tf("已显示 %d 条", 200))
	mustClickRow(t, tt, "再读 200 条")
	wait("第二页", func() bool { return !h.Loading && h.Done })
	if len(h.Commits) != 200 || h.Commits[199].Subject != "初始提交" {
		t.Errorf("读完应是 200 条，最后是初始提交: %d %q", len(h.Commits), h.Commits[len(h.Commits)-1].Subject)
	}
	if tt.HasText("再读 200 条") {
		t.Error("读完后不该再有\"再读\"按钮")
	}
}
