package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 这里的测试都在 t.TempDir() 的临时仓库上跑，不开窗口：后台回调排进队列，由 wait 在测试 goroutine 上跑掉。

func newTestApp(t *testing.T, ws string) (*App, func(what string, cond func() bool)) {
	t.Helper()
	return newTestAppIn(t, ws, t.TempDir())
}

// newTestAppIn 同 newTestApp，state.json 放在 stateDir。
func newTestAppIn(t *testing.T, ws, stateDir string) (*App, func(what string, cond func() bool)) {
	t.Helper()
	a, err := New(ws, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	q := testutil.NewQueue()
	a.Update = q.Post
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			q.Drain()
			if cond() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("等 %s 超时", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return a, wait
}

// idle 是仓库没有在跑的操作、也没有在读状态。
func idle(r *Repo) func() bool {
	return func() bool { return r.Busy == "" && len(r.Queue) == 0 && !r.Loading && r.Loaded }
}

func TestOpsOnTempRepo(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	if r.Status.Unstaged != 1 || r.Status.Untracked != 1 || r.Status.Staged != 0 {
		t.Fatalf("初始状态 %+v", r.Status)
	}

	// 没写提交信息、没暂存：提示而不跑
	a.Commit(r)
	if !a.AlertOpen || a.AlertMsg != "先写一句提交信息" {
		t.Fatalf("没写信息的提交：%v %q", a.AlertOpen, a.AlertMsg)
	}
	a.AlertOpen = false
	r.CommitMsg = "测试提交"

	a.Exec("git.stageAll")
	if r.Busy != "暂存全部" {
		t.Fatalf("Busy = %q", r.Busy)
	}
	a.Exec("git.stash") // 跑着的时候再点：排在后面
	if len(r.Queue) != 1 || r.Queue[0].Label != "贮藏" || !r.Queued("贮藏") || len(a.WritesRunning()) != 2 {
		t.Fatalf("应排队：%d %v", len(r.Queue), a.WritesRunning())
	}
	a.Dequeue(r, 0) // 移出队列：不跑，输出里记一笔
	wait("暂存全部", func() bool { return idle(r)() && r.Status.Staged == 2 })
	if a.Toast != "暂存全部完成" || len(a.Output) != 2 || a.Output[0].Text != "已移出队列：贮藏" || a.Output[1].Title != "$ git add -A" {
		t.Fatalf("toast %q 输出 %+v", a.Toast, a.Output)
	}

	a.Commit(r)
	wait("提交", func() bool { return idle(r)() && r.Status.Count == 0 })
	if r.CommitMsg != "" {
		t.Errorf("提交后信息框没清空：%q", r.CommitMsg)
	}
	if got := testutil.GitOut(t, root, "log", "-1", "--format=%s"); got != "测试提交" {
		t.Errorf("最新提交 %q", got)
	}

	// 丢弃：先问，点了才跑
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("乱改\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.RefreshRepo(r, WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	a.Discard(r, r.Status.Changes[0])
	if !a.ConfirmOpen || a.Confirm.Button != "丢弃" {
		t.Fatalf("丢弃没先问：%+v", a.Confirm)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "乱改\n" {
		t.Fatal("还没确认就丢了")
	}
	a.Confirm.Fn()
	wait("丢弃", func() bool { return idle(r)() && r.Status.Count == 0 })

	// 新建分支、切回去
	a.CreateBranch(r, " topic ", "")
	wait("新建分支", func() bool { return idle(r)() && r.Status.Branch == "topic" })
	a.SwitchBranch(r, git.Branch{Name: "main"})
	wait("切分支", func() bool { return idle(r)() && r.Status.Branch == "main" })

	// 失败的命令：弹提示、记进输出
	a.RunOp(r, "拉取", "pull", "--ff-only")
	wait("拉取", idle(r))
	if !a.AlertOpen || a.AlertTitle != "拉取失败" || !a.Output[len(a.Output)-1].Failed {
		t.Errorf("拉取失败没提示：%v %q", a.AlertOpen, a.AlertTitle)
	}
}

func TestTabsPreviewSaveReopen(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()

	// 预览标签被下一个预览顶掉；固定的留着
	d1 := a.OpenFile(r, "README.md", false)
	wait("读 README", func() bool { return !d1.Loading })
	d2 := a.OpenFile(r, "a.txt", false)
	if len(a.Docs) != 1 || a.CurrentDoc() != d2 {
		t.Fatalf("预览没顶掉：%d 个标签", len(a.Docs))
	}
	wait("读 a.txt", func() bool { return !d2.Loading })
	a.OpenFile(r, "a.txt", true)
	if len(a.Docs) != 1 || !d2.Pinned {
		t.Fatal("再打开同一个文件应固定它")
	}
	d3 := a.OpenFile(r, "README.md", false)
	wait("读 README", func() bool { return !d3.Loading })
	if len(a.Docs) != 2 {
		t.Fatalf("固定的标签被顶掉了：%d", len(a.Docs))
	}

	// ‹ ›
	if !a.CanBack() {
		t.Fatal("应能后退")
	}
	a.Exec("nav.back")
	if a.CurrentDoc() != d2 {
		t.Fatal("后退没回到 a.txt")
	}
	a.Exec("nav.forward")
	if a.CurrentDoc() != d3 {
		t.Fatal("前进没到 README")
	}

	// 改了再保存
	d2.Buf.Set("新的一行\n" + d2.Buf.String())
	if !d2.Modified() || a.DirtyDocs() != 1 {
		t.Fatal("改了没算改过")
	}
	a.SelectDoc(0)
	a.Exec("file.save")
	wait("保存后刷新", idle(r))
	if d2.Modified() || a.Toast != "已保存 a.txt" {
		t.Fatalf("保存后 modified=%v toast=%q", d2.Modified(), a.Toast)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); !strings.HasPrefix(string(data), "新的一行\n") {
		t.Fatal("没写进磁盘")
	}

	// 改过的标签关的时候先问；关全部留下改过的
	d2.Buf.Set("x" + d2.Buf.String())
	a.Exec("file.closeAll")
	if len(a.Docs) != 1 || a.Toast != "1 个没保存的标签留着了" {
		t.Fatalf("关全部后 %d 个标签，toast %q", len(a.Docs), a.Toast)
	}
	a.Exec("file.close")
	if !a.CloseOpen || a.CloseReq != d2 {
		t.Fatal("关改过的标签没问")
	}
	a.CloseOpen = false
	a.RemoveDoc(d2)
	if len(a.Docs) != 0 || a.DocSel != -1 {
		t.Fatalf("关完还有 %d 个，DocSel=%d", len(a.Docs), a.DocSel)
	}

	// ⇧⌘T 按关的顺序倒着重开
	a.Exec("file.reopen")
	if d := a.CurrentDoc(); d == nil || d.Rel != "a.txt" {
		t.Fatalf("重开的是 %+v", d)
	}
	a.Exec("file.reopen")
	if d := a.CurrentDoc(); d == nil || d.Rel != "README.md" || len(a.Docs) != 2 {
		t.Fatalf("第二次重开的是 %+v", d)
	}
	wait("重开的标签读完", func() bool { return !a.Docs[0].Loading && !a.Docs[1].Loading })
}

func TestDiffTabsFollowStatus(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))

	// Diff 上下文的命令只在当前标签是 Diff 时生效
	a.Exec("diff.full")
	if a.St.FullDiff {
		t.Fatal("没有 Diff 标签时 ⌥⌘F 不该生效")
	}
	a.ShowDiffFor(r, "README.md")
	if a.Toast != "README.md 没有未提交的改动" {
		t.Fatalf("toast %q", a.Toast)
	}
	a.ShowDiffFor(r, "a.txt")
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || d.Change.Kind != git.Unstaged {
		t.Fatalf("没打开 Diff 标签：%+v", d)
	}
	// 从文件树（"查看它的 diff"）开的 Diff 标签，Git 栏也选中它那一行：段头、提交框、未暂存标题、a.txt、u.txt（未跟踪，同一组）
	if a.GitSel != 3 {
		t.Errorf("Git 栏选中 %d，应是 a.txt 那行 3", a.GitSel)
	}
	wait("读 diff", func() bool { return !d.Loading })
	if d.DiffSt == nil {
		t.Fatalf("diff 没读出来：%q", d.Info)
	}
	if add, del := d.DiffSt.Stats(); add != 1 || del != 1 {
		t.Errorf("diff +%d −%d", add, del)
	}
	a.Exec("diff.full")
	if !a.St.FullDiff || !d.DiffSt.ExpandUnchanged {
		t.Fatal("⌥⌘F 没切到全文")
	}
	a.Exec("diff.unified")
	if !a.St.Unified {
		t.Fatal("⌥⌘L 没切到统一")
	}

	// 暂存之后未暂存的那项没了：标签留着，显示说明
	a.Stage(r, "a.txt")
	wait("暂存", func() bool { return idle(r)() && r.Status.Staged == 1 })
	if d.Info != GoneNote || d.DiffSt != nil || len(a.Docs) != 1 {
		t.Fatalf("改动没了之后 Info=%q", d.Info)
	}
	// 选中的那行挪走了：停在它原来的下一行（u.txt），不丢选中
	if rows := a.GitRows(); a.GitSel < 0 || a.GitSel >= len(rows) || rows[a.GitSel].Change.Path != "u.txt" {
		t.Errorf("改动没了之后 Git 栏选中 %d，应停在下一行 u.txt", a.GitSel)
	}
	staged := a.OpenDiff(r, r.Status.Changes[0], true)
	wait("读暂存的 diff", func() bool { return !staged.Loading })
	if staged.DiffSt == nil || !staged.DiffSt.ExpandUnchanged {
		t.Fatal("新开的 Diff 标签没跟全文设置")
	}
	if a.GitSel != 3 { // 段头、提交框、已暂存标题、a.txt
		t.Errorf("已暂存的 a.txt 应选中第 3 行，是 %d", a.GitSel)
	}
	// 切到文件标签不选；‹ 回到 Diff 标签又选上
	a.OpenFile(r, "README.md", true)
	if a.GitSel != -1 {
		t.Errorf("文件标签时 Git 栏还选中 %d", a.GitSel)
	}
	a.Back()
	if a.CurrentDoc() != staged || a.GitSel != 3 {
		t.Errorf("后退后标签 %v、选中 %d", a.CurrentDoc().Title, a.GitSel)
	}
}

func TestTreeAndFilter(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	other := testutil.NewRepo(t, "other")
	other.Write("docs/guide.md", "# guide\n")
	other.Write("a.txt", "other a\n")
	other.Commit("init")
	ws = testutil.Workspace(t, root, other.Root)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.EnsureFiles(r)
	wait("读文件列表", func() bool { return r.Files.Loaded })

	rows := a.TreeRows()
	var names []string
	for _, row := range rows {
		names = append(names, row.Name)
	}
	// 第一个仓库展开着，第二个收着
	if got := strings.Join(names, ","); got != "demo,a.txt,README.md,u.txt,other" {
		t.Fatalf("文件树 %s", got)
	}

	// 筛选跨仓库：两个仓库的 a.txt 都在
	a.SetFilter("a.txt")
	o := a.Repos[1]
	wait("读第二个仓库的文件", func() bool { return o.Files.Loaded })
	names = names[:0]
	for _, row := range a.TreeRows() {
		names = append(names, row.Name)
	}
	if got := strings.Join(names, ","); got != "demo,a.txt,other,a.txt" {
		t.Fatalf("筛选结果 %s", got)
	}
	a.OpenFirstMatch()
	if d := a.CurrentDoc(); d == nil || d.Root != root || d.Rel != "a.txt" || !d.Pinned {
		t.Fatalf("回车打开的是 %+v", d)
	}

	// 在文件树里显示：清掉筛选、展开、选中
	a.Reveal(o, "docs/guide.md")
	row, ok := a.TreeRowAt(a.FileSel)
	if a.Filter != "" || !ok || row.Repo != o || row.Path != "docs/guide.md" {
		t.Fatalf("Reveal 后选中 %+v", row)
	}
	if a.St.SidebarMode != workspace.ModeFiles || !a.St.Sidebar {
		t.Error("Reveal 应打开文件侧栏")
	}

	// 键盘：← 走到父目录，再 ← 收起它
	a.TreeLeft()
	if row, _ := a.TreeRowAt(a.FileSel); row.Path != "docs" {
		t.Fatalf("← 后选中 %q", row.Path)
	}
	a.TreeLeft()
	if row, _ := a.TreeRowAt(a.FileSel); row.Open {
		t.Fatal("← 没收起目录")
	}

	// 目录也能显示（面包屑的目录段）：选中并展开；空路径是仓库那一行
	a.Reveal(o, "docs")
	if row, _ := a.TreeRowAt(a.FileSel); !row.Dir || row.Path != "docs" || !row.Open {
		t.Fatalf("Reveal(docs) 后选中 %+v", row)
	}
	a.Reveal(o, "")
	if row, _ := a.TreeRowAt(a.FileSel); !row.Root || row.Repo != o {
		t.Fatalf("Reveal(\"\") 后选中 %+v", row)
	}
}

func TestHistoryRows(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.Exec("view.right")
	a.Exec("view.right")
	if !a.St.RightBar {
		t.Fatal("⌥⌘B 两次后右栏应开着")
	}
	a.EnsureHistory(r)
	wait("读历史", func() bool { return r.Hist.Loaded && idle(r)() })
	rows := HistRows(r)
	if len(rows) != 3 || rows[0].CI != -1 {
		t.Fatalf("历史行 %+v", rows)
	}

	// 第一行"未提交的改动"把侧栏切到 Git
	r.Hist.Sel = 0
	a.OnHistSelected(r)
	if a.St.SidebarMode != workspace.ModeGit {
		t.Fatal("没切到 Git 栏")
	}
	// 点提交展开它，点它的文件开预览 Diff 标签
	r.Hist.Sel = 1
	a.OnHistSelected(r)
	wait("读提交的文件", func() bool { return !r.Hist.FilesLoading && r.Hist.FilesFor != "" })
	rows = HistRows(r)
	if len(rows) != 4 || rows[2].File != 0 {
		t.Fatalf("展开后 %+v", rows)
	}
	r.Hist.Sel = 2
	a.OnHistSelected(r)
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Diff || d.Change.Kind != git.InCommit || d.Pinned {
		t.Fatalf("历史里的文件开的是 %+v", d)
	}
	wait("读提交里的 diff", func() bool { return !d.Loading })
	if d.DiffSt == nil {
		t.Fatalf("提交的 diff：%q", d.Info)
	}
	v := a.OpenVersion(r, d.Change)
	wait("读历史版本", func() bool { return !v.Loading })
	if v.Buf == nil || !v.St.ReadOnly || v.Editable() {
		t.Fatal("历史版本应只读")
	}
}

func TestAreasAndState(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, _ := newTestApp(t, ws)
	if !a.St.Sidebar || !a.St.BottomBar || !a.St.RightBar {
		t.Fatalf("第一次打开应三个区域都开着：%+v", a.St)
	}
	a.Exec("view.sidebar")
	a.Exec("view.bottom")
	if a.St.Sidebar || a.St.BottomBar {
		t.Fatal("⌘B / ⌘J 没切换")
	}
	a.Exec("view.git")
	if !a.St.Sidebar || a.St.SidebarMode != workspace.ModeGit {
		t.Fatalf("⇧⌘G 后侧栏 %v %q", a.St.Sidebar, a.St.SidebarMode)
	}
	a.Exec("view.git") // 再按一次关上
	if a.St.Sidebar {
		t.Fatal("再按 ⇧⌘G 应关侧栏")
	}
	a.Exec("view.files")
	if !a.St.Sidebar || a.St.SidebarMode != workspace.ModeFiles {
		t.Fatal("⇧⌘E 没打开文件侧栏")
	}
	got, err := workspace.LoadState(a.StateDir)
	if err != nil || got.Sidebar != a.St.Sidebar || got.BottomBar != a.St.BottomBar || got.SidebarMode != workspace.ModeFiles {
		t.Fatalf("state.json 没跟上：%+v %v", got, err)
	}
}

// TestStateMigration：旧的 panels map 读进来变成三个区域的开关，再存就是新格式。
func TestStateMigration(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	dir := t.TempDir()
	old := `{"panels":{"changes":true,"diff":true,"editor":false,"files":false,"history":false,"terminal":false},
		"unified":true,"fullDiff":true,"sidebarW":230,"centerPct":60,"changesH":496.5}`
	if err := os.WriteFile(filepath.Join(dir, workspace.StateFile), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := New(ws, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !a.St.Sidebar || a.St.SidebarMode != workspace.ModeGit || a.St.RightBar || a.St.BottomBar || !a.St.Unified || !a.St.FullDiff || a.St.SidebarW != 230 {
		t.Fatalf("迁移后 %+v", a.St)
	}
	a.SaveState()
	data, _ := os.ReadFile(filepath.Join(dir, workspace.StateFile))
	// 会话里的 "panels" 是新布局（Session.Panels）；旧的是顶层那个按面板名记的 map，不该再有
	if strings.Contains(string(data), `"changes":`) || strings.Contains(string(data), `"centerPct"`) || !strings.Contains(string(data), fmt.Sprintf(`"version": %d`, workspace.Version)) {
		t.Fatalf("存回去的不是新格式：%s", data)
	}
}

// showGroups 打开设置里的"在 Git 栏显示工作树 / 贮藏"（默认都关）：测工作树、贮藏的行和监听的测试在 setup 里调。
func showGroups(a *App) {
	a.St.Settings.ShowWorktrees, a.St.Settings.ShowStashes = true, true
	a.syncShownGroups()
}
