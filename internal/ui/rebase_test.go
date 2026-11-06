package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 交互式变基向导：右键历史里的提交开，推送过的有胶囊、底下说要强制推送；列表有键盘时 ↓ 选行、S / D / E 设动作、⌥↓ 挪行，
// 动作按钮的菜单、信息框里打字；重置；开始先问，做完关掉向导、历史变了。再开一次停在 edit：上面一栏写停在第几步、只剩"关闭"，
// 横幅里放弃以后写结束了。
func TestRebaseTab(t *testing.T) {
	repo := testutil.NewRepo(t, "rebase")
	repo.Write("f.txt", "f\n")
	repo.Commit("base")
	for _, n := range []string{"1", "2", "3"} {
		repo.Write(n+".txt", n+"\n")
		repo.Commit("c" + n)
	}
	bare := filepath.Join(t.TempDir(), "up.git")
	repo.Git("init", "-q", "--bare", "-b", "main", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "-q", "origin", "HEAD~1:refs/heads/main") // c1、c2 推送过，c3 没有
	repo.Git("fetch", "-q", "origin")
	repo.Git("branch", "-q", "-u", "origin/main")

	a, tt, wait := newUI(t, testutil.Workspace(t, repo.Root), "")
	r := a.Repos[0]
	a.UseRepo(r)
	a.RefreshRepo(r, app.WhyManual)
	wait("状态", idle(r))
	a.SetHistAll(false)
	a.LoadHistory(r, true)
	h := &r.Hist
	wait("历史", func() bool { return h.Loaded && !h.Loading && len(h.Commits) == 4 })
	tt.Frame()

	// 命令面板：右栏历史里选着一个提交时能用
	cmd := app.Find("git.rebaseInteractive")
	h.Sel = slices.IndexFunc(app.HistRows(r), func(hr app.HistRow) bool { return hr.CI == 2 && hr.File < 0 })
	if h.Sel < 0 || !a.CanRun(cmd) {
		t.Fatalf("选着 c1 时命令不能用（行 %d）", h.Sel)
	}

	mustRightClickRow(t, tt, "c1", "从这里开始交互式变基…")
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Rebase {
		t.Fatalf("没开向导：%+v", d)
	}
	p := d.Rebase
	tt.Frame()
	if !tt.Focused("交互式变基的提交") { // 开的时候给了它
		t.Fatal("向导的列表没拿到键盘")
	}
	mustText(t, tt, "交互式变基 · rebase · 3 个提交")
	mustText(t, tt, "3 个提交 → 3 个")
	mustText(t, tt, "2 个已推送，完成后要强制推送（Git 菜单 → 强制推送）")
	if n := strings.Count(strings.Join(tt.Texts(), "\n"), "已推送\n"); n < 2 || !p.Steps[0].Pushed || p.Steps[2].Pushed {
		t.Errorf("推送过的胶囊 %d 个，steps %v %v", n, p.Steps[0].Pushed, p.Steps[2].Pushed)
	}
	mustClick(t, tt, "开始变基") // 还没改：灰着
	if a.Confirm != nil {
		t.Fatal("没改也能开始")
	}

	// 键盘：点了灰着的按钮，键盘离开了列表；点第一行的哈希拿回来
	mustClick(t, tt, p.Steps[0].Commit.Short)
	if !tt.Focused("交互式变基的提交") || p.Sel != 0 {
		t.Fatalf("点了行列表没拿到键盘（选着 %d）", p.Sel)
	}
	tt.Key(0, wui.KeyDown)
	tt.Key(0, wui.KeyS)
	tt.Frame()
	if p.Sel != 1 || p.Steps[1].Action != docs.RebaseSquash {
		t.Fatalf("↓ S：选中 %d，动作 %v", p.Sel, p.Steps[1].Action)
	}
	mustText(t, tt, "3 个提交 → 2 个")
	if !shows(tt, "第 2 行合在一起的提交信息") {
		t.Error("squash 下面没有信息框")
	}
	tt.Key(wui.Alt, wui.KeyDown)
	tt.Frame()
	if p.Sel != 2 || p.Steps[2].Commit.Subject != "c2" || p.Steps[2].Msg != "c3\n\nc2" {
		t.Fatalf("⌥↓：选中 %d，第 3 行 %s，信息 %q", p.Sel, p.Steps[2].Commit.Subject, p.Steps[2].Msg)
	}
	tt.Key(0, wui.KeyUp)
	tt.Key(0, wui.KeyD)
	tt.Frame()
	if p.Steps[1].Action != docs.RebaseDrop || p.Steps[2].Msg != "c1\n\nc2" {
		t.Errorf("D：第 2 行 %v，squash 的信息 %q", p.Steps[1].Action, p.Steps[2].Msg)
	}
	mustText(t, tt, "3 个提交 → 1 个")
	tt.Key(0, wui.KeyUp)
	tt.Key(0, wui.KeyE)
	tt.Frame()
	mustText(t, tt, "会停在这里；改完用 Git 栏的横幅继续")
	tt.Key(0, wui.KeyS) // 第一条不能并入
	tt.Frame()
	if p.Steps[0].Action != docs.RebaseEdit || !tt.HasText("前面没有能并入的提交") {
		t.Errorf("第一条 S：%v，没有提示", p.Steps[0].Action)
	}
	mustClick(t, tt, "重置")
	if p.Changed() || tt.HasText("会停在这里；改完用 Git 栏的横幅继续") {
		t.Fatal("重置以后还有改动")
	}

	// 动作按钮的菜单：第 2 行 reword，信息框里改
	mustClick(t, tt, "第 2 行做什么：pick")
	if err := chooseMenu(t, tt, "reword — 保留，改提交信息（按 R）"); err != nil {
		t.Fatalf("动作菜单：%v；菜单 %q", err, shownMenu(t, tt))
	}
	tt.Frame()
	if p.Steps[1].Action != docs.RebaseReword || p.Steps[1].Msg != "c2" {
		t.Fatalf("菜单选 reword：%v %q", p.Steps[1].Action, p.Steps[1].Msg)
	}
	mustClick(t, tt, "第 2 行的新提交信息")
	tt.Key(wui.Cmd, wui.KeyA)
	tt.Type("二！")
	tt.Frame()
	if p.Steps[1].Msg != "二！" || !p.Steps[1].Edited {
		t.Errorf("打字以后信息 %q，edited %v", p.Steps[1].Msg, p.Steps[1].Edited)
	}
	accessCheck(t, tt, "交互式变基向导")

	mustClick(t, tt, "开始变基")
	if a.Confirm == nil || a.Confirm.Title != "改写 rebase 的 3 个提交？" || !strings.Contains(a.Confirm.Message, "其中 2 个已经推送过") {
		t.Fatalf("先问：%+v", a.Confirm)
	}
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("变基", func() bool { return idle(r)() && !p.Running })
	tt.Frame()
	if a.GroupOf(d) != nil || !tt.HasText("变基完成：3 个提交 → 3 个") {
		t.Fatalf("做完：向导还开着 %v，提示 %q", a.GroupOf(d) != nil, tt.Texts())
	}
	if log := repo.Git("log", "--format=%s"); log != "c3\n二！\nc1\nbase" {
		t.Errorf("变基后的历史:\n%s", log)
	}

	// 停在 edit：上面一栏、只剩关闭；放弃以后写结束了
	a.LoadHistory(r, true)
	wait("新的历史", func() bool { return h.Loaded && !h.Loading && len(h.Commits) == 4 && h.Commits[0].Hash == r.Extra.Head })
	a.OpenRebase(r, h.Commits[2])
	d = a.CurrentDoc()
	p = d.Rebase
	a.SetRebaseAction(p, 0, docs.RebaseEdit)
	a.StartRebase(d)
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("停在 edit", func() bool { return idle(r)() && !p.Running && r.Extra.Ongoing == git.Rebasing })
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool {
		return strings.HasPrefix(s, "停在第 1 / 3 步：停在 edit 这一步") && strings.HasSuffix(s, "解决后用 Git 栏的横幅继续")
	}) {
		t.Errorf("没有停下的那一栏：%q", tt.Texts())
	}
	if shows(tt, "开始变基") || shows(tt, "重置") || !shows(tt, "关闭") {
		t.Error("停下以后应只剩关闭")
	}
	accessCheck(t, tt, "停下的交互式变基向导")
	a.AbortOngoing(r)
	a.Confirm.Fn()
	a.ConfirmOpen = false
	wait("放弃", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp && p.Ended })
	tt.Frame()
	mustText(t, tt, "这次变基已经结束了，可以关掉这个标签")
	mustClick(t, tt, "关闭")
	if a.GroupOf(d) != nil {
		t.Error("关闭没关掉向导")
	}
}
