package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
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
	mustRightClickRow(t, tt, "amend（仓库）", "修改上一次提交…")
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
	chooseCommitMenu(t, tt, "修改上一次提交…")
	wait("上一次的信息", func() bool { return r.CommitMsg == "第二次（改过）" })
	mustClick(t, tt, "修改提交")
	mustText(t, tt, "修改已经推送过的提交？")
	mustClick(t, tt, "取消")
	if got := repo.Git("rev-parse", "HEAD"); got == head || got != repo.Git("rev-parse", "origin/main") {
		t.Errorf("取消后 HEAD 不该变: %s", got)
	}
	// 取消勾选：框里放回勾上前写的（空）
	chooseCommitMenu(t, tt, "修改上一次提交…")
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

// 提交按钮是强调色的图标按钮：默认侧栏宽度下右边和输入框对齐、不超出（已暂存两位数、三位数时也一样），
// 读屏的名字带文件数；不能提交（没写信息）时点了不提交。
func TestCommitButton(t *testing.T) {
	repo := testutil.NewRepo(t, "btn")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	ws := testutil.Workspace(t, repo.Root)
	a, tt, wait := newUI(t, ws, "")
	r := a.Repos[0]
	a.Exec("view.git")
	n := 0
	for _, want := range []int{12, 120} {
		for ; n < want; n++ {
			repo.Write("f"+strconv.Itoa(n)+".txt", "x\n")
		}
		repo.Git("add", "-A")
		a.RefreshRepo(r, app.WhyManual)
		wait("git status", func() bool { return idle(r)() && r.Status.Staged == want })
		if !r.CommitShown() {
			a.OpenCommit(r)
		}
		r.CommitMsg = ""
		tt.Frame()
		label := i18n.Tf("提交 %s 个文件", strconv.Itoa(want))
		if tt.HasText(label) {
			t.Fatal("没写信息时框里不该有提交按钮")
		}
		r.CommitMsg = "一百二十个"
		tt.Frame()
		btn, ok := tt.Find(label)
		if !ok {
			t.Fatalf("没有读屏名字是 %q 的提交按钮: %q", label, tt.Texts())
		}
		box, ok := tt.Find(i18n.T("提交信息"))
		if !ok {
			t.Fatal("没有提交信息框")
		}
		if btn.X+btn.W > box.X+box.W+0.5 {
			t.Errorf("%d 个文件：提交按钮 %v 超出了输入框 %v 的右边", want, btn, box)
		}
		if btn.H > wtBtnSize+2 {
			t.Errorf("提交按钮高了: %v", btn)
		}
	}

	// 前面排着慢命令，提交排在后面：按钮灰着，点了不再排一条。
	// 不调 Disabled（mygo 的 Disabled 不显示悬停提示），提示里写原因
	slowOp(a, r)
	a.Commit(r)
	tt.Frame()
	label := i18n.Tf("提交 %s 个文件", "120")
	var found *accNode
	nodes := accessTree(t, tt)
	for i := range nodes {
		if nodes[i].Label == label {
			found = &nodes[i]
		}
	}
	switch {
	case found == nil:
		t.Fatalf("无障碍树里没有 %q", label)
	case found.States&accessDisabled != 0:
		t.Error("不能提交时按钮不该是 Disabled：那样没有悬停提示")
	case !strings.Contains(found.Desc, i18n.T("正在提交")):
		t.Errorf("提示里该写原因：%q", found.Desc)
	}
	_ = tt.Click(label)
	tt.Frame()
	if len(r.Queue) != 1 {
		t.Errorf("灰着的按钮点了又排了一条：%d", len(r.Queue))
	}
	a.CancelOp(r) // 慢命令取消了，排着的提交接着跑完
	wait("提交完", func() bool { return idle(r)() && r.Status.Staged == 0 })
	if got := repo.Git("log", "-1", "--format=%s"); got != "一百二十个" {
		t.Errorf("最新提交 %q", got)
	}
}

// 什么都没暂存：按钮写"提交全部 N 个文件"，一下子暂存（含未跟踪的）并提交；只有一个文件时不写"全部"。
func TestCommitAllButton(t *testing.T) {
	repo := testutil.NewRepo(t, "all")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	repo.Write("a.txt", "b\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	a.OpenCommit(r)
	r.CommitMsg = "一个文件"
	tt.Frame()
	mustText(t, tt, i18n.Tf("提交 %s 个文件", "1"))

	repo.Write("new.txt", "新\n")
	a.RefreshRepo(r, app.WhyManual)
	wait("读到未跟踪的", func() bool { return idle(r)() && r.Status.Untracked == 1 })
	r.CommitMsg = "全部"
	tt.Frame()
	mustClick(t, tt, i18n.Tf("提交全部 %s 个文件", "2"))
	wait("提交完", func() bool { return idle(r)() && r.Status.Count == 0 })
	if got := repo.Git("show", "--format=%s", "--name-only", "HEAD"); !strings.Contains(got, "全部") || !strings.Contains(got, "a.txt") || !strings.Contains(got, "new.txt") {
		t.Errorf("最新提交 %q", got)
	}
	if r.CommitMsg != "" {
		t.Errorf("提交后框里还有 %q", r.CommitMsg)
	}
}

// 已暂存了几个文件时，按钮和 ⌘↩ 只提交它们，别的改动不带上。
func TestCommitStagedOnly(t *testing.T) {
	repo := testutil.NewRepo(t, "part")
	repo.Write("a.txt", "a\n")
	repo.Write("b.txt", "b\n")
	repo.Commit("初始提交")
	repo.Write("a.txt", "a2\n")
	repo.Write("b.txt", "b2\n")
	repo.Git("add", "a.txt")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Staged == 1 })
	r.CommitMsg = "只提交 a"
	tt.Frame()
	mustClick(t, tt, i18n.Tf("提交 %s 个文件", "1"))
	wait("提交完", func() bool { return idle(r)() && r.Status.Staged == 0 && r.Status.Unstaged == 1 })
	if got := repo.Git("show", "--format=", "--name-only", "HEAD"); got != "a.txt" {
		t.Errorf("提交里的文件 %q", got)
	}
}

// ▾ 里的"提交并推送"：自动推送关着也推，没写信息时灰着带原因。
func TestCommitAndPush(t *testing.T) {
	repo := testutil.NewRepo(t, "push")
	repo.Write("a.txt", "a\n")
	repo.Commit("初始提交")
	bare := filepath.Join(t.TempDir(), "origin.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "-u", "origin", "main")
	repo.Write("a.txt", "b\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.Exec("view.git")
	a.RefreshRepo(r, app.WhyManual)
	wait("读状态", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	tt.Frame()

	openCommitMenu(t, tt)
	if !menuDisabled(t, tt, "提交并推送") {
		t.Errorf("没写信息时提交并推送该灰着：%q", shownMenu(t, tt))
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()

	a.OpenCommit(r)
	r.CommitMsg = "推上去"
	tt.Frame()
	chooseCommitMenu(t, tt, "提交并推送")
	wait("推送完", func() bool {
		return idle(r)() && r.Status.Count == 0 && repo.Git("rev-parse", "HEAD") == repo.Git("rev-parse", "origin/main")
	})
	if got := repo.Git("log", "-1", "--format=%s", "origin/main"); got != "推上去" {
		t.Errorf("远程最新提交 %q", got)
	}
}

// openCommitMenu 开提交框的 ▾；没写信息时 ▾ 要指针在框上才有。
func openCommitMenu(t *testing.T, tt *wui.Tester) {
	t.Helper()
	tt.Frame()
	if _, ok := tt.Find("更多提交操作"); !ok {
		for _, s := range []string{i18n.T("提交信息"), commitPlaceholder()} {
			if _, ok := tt.Find(s); ok {
				hoverOn(t, tt, s)
				break
			}
		}
	}
	mustClick(t, tt, "更多提交操作")
	tt.Frame()
}

// menuDisabled 是开着的菜单里 label 这一项灰着。
func menuDisabled(t *testing.T, tt *wui.Tester, label string) bool {
	t.Helper()
	ms := popMenus(t, tt)
	if len(ms) == 0 {
		t.Fatal("菜单没开")
	}
	for _, it := range ms[len(ms)-1].Items {
		if it.Label == i18n.T(label) {
			return it.States&accessDisabled != 0
		}
	}
	t.Fatalf("菜单里没有 %q：%q", label, shownMenu(t, tt))
	return false
}

// chooseCommitMenu 选提交框 ▾ 里的一项。
func chooseCommitMenu(t *testing.T, tt *wui.Tester, item ...string) {
	t.Helper()
	openCommitMenu(t, tt)
	if err := chooseMenu(t, tt, item...); err != nil {
		t.Fatalf("▾ 选 %q: %v；菜单=%q", item, err, shownMenu(t, tt))
	}
	tt.Frame()
	tt.Frame()
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
