package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 子模块走一遍：文件树里两行（右边灰的"子模块" / "未初始化"），回车初始化了的那行问添加为仓库、确认后加进来跳过去，
// 再回车直接跳；仓库菜单的"子模块"列着两个、点了在树里选中；子模块检出了别的提交时 Git 栏那一项带"子模块"胶囊，
// Diff 写两次提交、有两个按钮；丢弃它先问、确认后检出回记录的；没初始化的回车问初始化，确认后检出来。
func TestSubmodules(t *testing.T) {
	// 子模块从本机路径克隆：git 2.38 起要 protocol.file.allow（只在测试的环境里开，应用里不加）
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	m, lib := testutil.SubmoduleRepo(t, "main")
	head := lib.Git("rev-parse", "HEAD")
	subRoot := filepath.Join(m.Root, "vendor", "lib")
	a, tt, wait := newUI(t, testutil.Workspace(t, m.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", func() bool { return idle(r)() && len(r.Extra.Subs) == 2 })

	// 文件树：两行文件，标着子模块的状态
	a.Reveal(r, "vendor/lib")
	wait("文件树", func() bool { return shows(tt, "lib 2") })
	a.Reveal(r, "vendor/lib") // 文件列表读到以后再选中
	rows := a.TreeRows()
	sub := func(p string) byte {
		i := slices.IndexFunc(rows, func(row app.TreeRow) bool { return row.Path == p })
		if i < 0 {
			t.Fatalf("树里没有 %s", p)
		}
		return rows[i].Sub
	}
	if sub("vendor/lib") != ' ' || sub("vendor/lib 2") != '-' || sub("README.md") != 0 {
		t.Fatalf("子模块的行：%q %q %q", sub("vendor/lib"), sub("vendor/lib 2"), sub("README.md"))
	}
	if !shows(tt, "子模块") || !shows(tt, "未初始化") {
		t.Errorf("子模块那两行右边没写状态：%q", tt.Texts())
	}

	// 回车：问添加为仓库，确认后加进来、跳过去；再回车直接跳，不再问
	a.ClickTreeRow(true, false)
	tt.Frame()
	if a.Confirm == nil || !tt.HasText("把子模块 vendor/lib 添加为工作区里的仓库？") {
		t.Fatalf("回车没问添加：%q", tt.Texts())
	}
	mustClick(t, tt, "添加")
	wait("加进来", func() bool { return len(a.Repos) == 2 && idle(a.Repos[1])() })
	if cur := a.Current(); cur == nil || !git.SamePath(cur.Root, subRoot) {
		t.Fatalf("没跳到子模块：%v", cur)
	}
	a.Reveal(r, "vendor/lib")
	a.ClickTreeRow(true, false)
	if a.Confirm != nil || !git.SamePath(a.Current().Root, subRoot) {
		t.Errorf("已经加进来了还在问：%v", a.Confirm)
	}
	a.RemoveRepo(a.Repos[1]) // 后面只看父仓库
	tt.Frame()

	// 仓库菜单：子模块下面两项，点了在树里选中
	open := func() {
		t.Helper()
		tt.Frame()
		if err := tt.RightClick(r.Name + "（仓库）"); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(shownMenu(t, tt), "子模块") {
			t.Fatalf("仓库菜单：%v", shownMenu(t, tt))
		}
	}
	open()
	if err := chooseMenu(t, tt, "子模块", "vendor/lib 2 · "+git.Short(head)+" · 未初始化"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if row, ok := a.TreeRowAt(a.FileSel); !ok || row.Path != "vendor/lib 2" {
		t.Errorf("菜单里点了没在树里选中：%+v", row)
	}

	// 子模块里检出别的提交：Git 栏那一项带胶囊，Diff 写两次提交
	prev := lib.Git("rev-parse", "HEAD~1")
	m.In(subRoot).Git("checkout", "-q", "HEAD~1")
	a.RefreshRepo(r, app.WhyManual)
	wait("子模块变了", func() bool { return idle(r)() && r.Status.Unstaged == 1 })
	tt.Frame()
	ch := r.Status.Changes[0]
	if ch.Sub != "C.." {
		t.Fatalf("那一项是 %+v", ch)
	}
	mustText(t, tt, "子模块") // Git 栏的胶囊
	a.OpenDiff(r, ch, true)
	note := "子模块 vendor/lib：" + git.Short(head) + " → " + git.Short(prev)
	wait("子模块的 Diff", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading && d.Info != "" })
	if d := a.CurrentDoc(); !d.Submodule || d.Info != note {
		t.Errorf("Diff：%v %q，要 %q", d.Submodule, d.Info, note)
	}
	tt.Frame()
	if !shows(tt, note) || !shows(tt, "添加为仓库…") || !shows(tt, "在新窗口中打开子模块") {
		t.Errorf("子模块的 Diff 标签里：%q", tt.Texts())
	}

	// 丢弃：先问，确认后检出回记录的那次
	a.Discard(r, ch)
	tt.Frame()
	if a.Confirm == nil || !tt.HasText("把子模块 vendor/lib 检出回记录的提交？") {
		t.Fatalf("丢弃没问：%q", tt.Texts())
	}
	mustClick(t, tt, "检出")
	wait("检出回来", func() bool { return idle(r)() && r.Status.Count == 0 })
	if got := m.In(subRoot).Git("rev-parse", "HEAD"); got != head {
		t.Errorf("子模块的 HEAD 是 %s，要 %s", got, head)
	}

	// 没初始化的：回车问初始化，确认后检出来
	a.Reveal(r, "vendor/lib 2")
	a.ClickTreeRow(true, false)
	tt.Frame()
	if a.Confirm == nil || !tt.HasText("初始化并更新子模块 vendor/lib 2？") {
		t.Fatalf("没初始化的回车没问：%q", tt.Texts())
	}
	mustClick(t, tt, "初始化并更新")
	wait("初始化", func() bool {
		s, _ := r.Submodule("vendor/lib 2")
		return idle(r)() && s.Inited()
	})
	if _, err := os.Stat(filepath.Join(m.Root, "vendor", "lib 2", "b.txt")); err != nil {
		t.Errorf("初始化以后没检出文件：%v", err)
	}
	tt.Frame()
	if shows(tt, "未初始化") {
		t.Error("初始化以后还写着未初始化")
	}
}
