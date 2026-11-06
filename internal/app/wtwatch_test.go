package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/testutil"
)

// Git 栏"工作树"块展开着时盯着各工作树的目录。全在临时仓库上：主仓库 proj 旁边两个工作树，
// proj-feat 没打开（自己开一个监听），proj-open 在工作区里打开着（不另开，靠它自己的监听报过来）。
func TestWatchWorktreeDirty(t *testing.T) {
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.Commit("初始")
	side := filepath.Dir(m.Root)
	feat, open := filepath.Join(side, "proj-feat"), filepath.Join(side, "proj-open")
	m.Git("worktree", "add", "-q", "-b", "wt/feat", feat)
	m.Git("worktree", "add", "-q", "-b", "wt/open", open)
	a, wait := newTestApp(t, wsFile(t, "工作树监听", m.Root, open))
	showGroups(a)
	a.RefreshAll(WhyOpen)
	r, or := a.Repos[0], a.Repos[1]
	wait("读完", func() bool { return idle(r)() && idle(or)() })
	a.StartWatching()
	defer a.Close()
	wait("监听开了", func() bool { return r.Watching() && or.Watching() })
	if len(r.wtWatch) != 0 {
		t.Fatalf("收着的块不该盯：%v", r.wtWatch)
	}

	a.ToggleWorktrees(r)
	wait("展开、懒算、开监听", func() bool { return r.WtInfo != nil && !r.WtLoading && r.WtWatching() == 1 })
	if _, ok := r.wtWatch[wtAt(t, r, feat).Path]; !ok || len(r.wtWatch) != 1 {
		t.Fatalf("该只盯没打开的 proj-feat：%v", r.wtWatch)
	}
	dirty := func(dir string, n int) func() bool {
		return func() bool { i := r.WtInfo[wtAt(t, r, dir).Path]; return i != nil && i.Dirty == n }
	}
	if !dirty(feat, 0)() || !dirty(open, 0)() {
		t.Fatalf("初始：%+v %+v", r.WtInfo[wtAt(t, r, feat).Path], r.WtInfo[wtAt(t, r, open).Path])
	}

	// 没打开的工作树里写文件：它自己的监听 → 只重算那一条（wtGen 不变，没整块重来）。
	// 主仓库自己的监听可能正好插进来一次整块重算：展开时在工作树里跑的 git status 会顺手重写
	// .git/worktrees/<名字>/index（在主仓库的监听范围里），那个 Git 变化晚到就是 wtGen 加一（复核时挂过一次）。
	// 所以先等稳下来再记 wtGen；还碰上了就换一个新文件再写，三次里至少一次看到只重算那一条。
	single := false
	for k := 1; k <= 3 && !single; k++ {
		wait("稳下来", func() bool { return idle(r)() && idle(or)() && !r.WtLoading })
		gen := r.wtGen
		if err := os.WriteFile(filepath.Join(feat, fmt.Sprintf("new%d.txt", k)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wait("proj-feat 有改动", dirty(feat, k))
		if single = r.wtGen == gen; !single {
			t.Logf("第 %d 次碰上了整块重算：wtGen %d → %d，换个文件再试", k, gen, r.wtGen)
		}
	}
	if !single {
		t.Error("三次都是整块重算，没看到只重算那一条")
	}
	// 打开着的那个：它自己的监听报了文件变化 → 主仓库块里那一条跟着重算
	if err := os.WriteFile(filepath.Join(open, "a.txt"), []byte("改\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wait("proj-open 有改动", func() bool { return dirty(open, 1)() && idle(or)() && or.Status.Count == 1 })

	// 收起就停；再展开再开；移除打开着的那个仓库，它在主仓库块里也要自己盯了；关窗口全停
	a.ToggleWorktrees(r)
	if len(r.wtWatch) != 0 {
		t.Errorf("收起后还在盯：%v", r.wtWatch)
	}
	a.ToggleWorktrees(r)
	wait("再展开", func() bool { return r.WtWatching() == 1 })
	a.removeRepo(or)
	wait("移除后两个都盯", func() bool { return r.WtWatching() == 2 })
	a.Close()
	if len(r.wtWatch) != 0 || r.Watching() {
		t.Errorf("关窗口后还在盯：%v", r.wtWatch)
	}
}

// 链接工作树的 HEAD、index 在主仓库的 .git/worktrees/<名字> 下：在工作树里切分支，工作区里打开的那个链接工作树
// 跟上新分支，主仓库"工作树"块里那一条也跟上（两边都是各自原有的监听，块收着也一样）。
func TestWatchLinkedGitdir(t *testing.T) {
	m := testutil.NewRepo(t, "proj")
	m.Write("a.txt", "一\n")
	m.Commit("初始")
	wt := filepath.Join(filepath.Dir(m.Root), "proj-wt")
	m.Git("worktree", "add", "-q", "-b", "wt/a", wt)
	a, wait := newTestApp(t, wsFile(t, "链接", m.Root, wt))
	showGroups(a)
	a.RefreshAll(WhyOpen)
	r, lr := a.Repos[0], a.Repos[1]
	wait("读完", func() bool { return idle(r)() && idle(lr)() })
	if !lr.Linked || lr.Status.Branch != "wt/a" || wtAt(t, r, wt).Branch != "wt/a" {
		t.Fatalf("初始：linked=%v %q %+v", lr.Linked, lr.Status.Branch, r.Worktrees)
	}
	a.StartWatching()
	defer a.Close()
	wait("监听开了", func() bool { return r.Watching() && lr.Watching() })

	m.In(wt).Git("checkout", "-q", "-b", "x")
	wait("两边都跟上分支 x", func() bool {
		return idle(r)() && idle(lr)() && lr.Status.Branch == "x" && wtAt(t, r, wt).Branch == "x"
	})
}
