package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
)

// rebaseApp 在仓库上开应用、读好状态和当前分支的历史。
func rebaseApp(t *testing.T, repo *testutil.Repo) (*App, *Repo, func(string, func() bool)) {
	t.Helper()
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	r := a.Repos[0]
	a.UseRepo(r)
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	a.SetHistAll(false)
	a.LoadHistory(r, true)
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) > 0 })
	return a, r, wait
}

// linearRepo 建 base 加 n 个提交的仓库，第 i 个提交加 i.txt、信息 "c<i>"；返回各提交（base 在 [0]）。
func linearRepo(t *testing.T, name string, n int) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t, name)
	repo.Write("f.txt", "1\n2\n3\n")
	repo.Commit("base")
	for i := 1; i <= n; i++ {
		repo.Write(fmt.Sprintf("%d.txt", i), fmt.Sprintf("%d\n", i))
		repo.Commit(fmt.Sprintf("c%d", i))
	}
	return repo
}

// 开向导：范围是 HEAD 到选的提交，最老的在上面、全是 pick，Base 是它的父提交（根提交为空）；第一条不能并入；
// squash 的默认信息是上一条（连着并入的几条合在一起）加这一条，改过的不再跟；挪行、重置；不能开的几种情况。
func TestRebasePlan(t *testing.T) {
	repo := linearRepo(t, "plan", 3)
	repo.Git("commit", "-q", "--amend", "-m", "c3", "-m", "正文三")
	a, r, _ := rebaseApp(t, repo)
	h := &r.Hist
	c3, c2, c1, base := h.Commits[0], h.Commits[1], h.Commits[2], h.Commits[3]

	if cs, why := a.RebaseRange(r, c1); why != "" || len(cs) != 3 || cs[0].Hash != c3.Hash {
		t.Fatalf("c1 起的范围 %d 个，why %q", len(cs), why)
	}
	a.OpenRebase(r, c1)
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Rebase || d.Title != "交互式变基 · plan · 3 个提交" || !a.FocusDoc {
		t.Fatalf("向导标签 %+v", d)
	}
	p := d.Rebase
	if p.Base != base.Hash || p.Head != c3.Hash || len(p.Steps) != 3 || p.Steps[0].Commit.Hash != c1.Hash || p.Steps[2].Commit.Hash != c3.Hash {
		t.Fatalf("计划 base %s head %s，%d 步", p.Base, p.Head, len(p.Steps))
	}
	if why := RebaseProblem(p); why != "还没改什么" {
		t.Errorf("没改时 %q", why)
	}

	a.SetRebaseAction(p, 0, docs.RebaseSquash)
	if p.Steps[0].Action != docs.RebasePick || a.Toast != "前面没有能并入的提交" {
		t.Errorf("第一条 squash：%v，toast %q", p.Steps[0].Action, a.Toast)
	}
	a.SetRebaseAction(p, 1, docs.RebaseSquash)
	if got := p.Steps[1].Msg; got != "c1\n\nc2" {
		t.Errorf("c2 squash 的默认信息 %q", got)
	}
	a.SetRebaseAction(p, 2, docs.RebaseSquash)
	if got := p.Steps[2].Msg; got != "c1\n\nc2\n\nc3\n\n正文三" {
		t.Errorf("连着两条 squash 的默认信息 %q", got)
	}
	p.Steps[1].Msg = "一和二" // 像在框里打了字
	p.Defaults()
	if !p.Steps[1].Edited || p.Steps[2].Msg != "一和二\n\nc3\n\n正文三" {
		t.Errorf("改了上一条：edited %v，下一条 %q", p.Steps[1].Edited, p.Steps[2].Msg)
	}
	if RebaseResult(p) != 1 || RebaseCounts(p) != "pick 1、squash 2" || RebaseProblem(p) != "" {
		t.Errorf("结果 %d 个，%q，problem %q", RebaseResult(p), RebaseCounts(p), RebaseProblem(p))
	}
	p.Steps[1].Msg = "  \n"
	if why := RebaseProblem(p); why != "第 2 行的提交信息是空的" {
		t.Errorf("空信息 %q", why)
	}
	a.SetRebaseAction(p, 0, docs.RebaseDrop)
	if !CanMeld(p, 2) || CanMeld(p, 1) || RebaseProblem(p) != "第 2 行前面没有能并入的提交" {
		t.Errorf("第一条丢掉后：problem %q", RebaseProblem(p))
	}

	a.ResetRebase(p)
	a.MoveRebaseStep(p, 2, -1)
	if p.Steps[1].Commit.Hash != c3.Hash || p.Sel != 1 || !p.Changed() {
		t.Errorf("上移：第 2 行 %s，选中 %d", p.Steps[1].Commit.Subject, p.Sel)
	}
	a.MoveRebaseStep(p, 0, -1) // 第一行上不去
	a.ResetRebase(p)
	if p.Changed() || p.Steps[1].Commit.Hash != c2.Hash || p.Steps[1].Action != docs.RebasePick {
		t.Errorf("重置后 %+v", p.Steps)
	}

	// 再开同一个仓库：没开始的换成新的范围
	a.OpenRebase(r, base)
	if d2 := a.CurrentDoc(); d2 != d || len(d.Rebase.Steps) != 4 || d.Rebase.Base != "" {
		t.Errorf("从根提交开：同一个标签 %v，%d 步，base %q", d2 == d, len(d.Rebase.Steps), d.Rebase.Base)
	}
	if d.Title != "交互式变基 · plan · 4 个提交" || len(a.AllDocs()) != 1 {
		t.Errorf("标题 %q，%d 个标签", d.Title, len(a.AllDocs()))
	}
	d.Rebase.Stopped = true
	a.OpenRebase(r, c2)
	if len(d.Rebase.Steps) != 4 || a.Toast != "这个仓库的变基还没结束" {
		t.Errorf("停着的不换：%d 步，toast %q", len(d.Rebase.Steps), a.Toast)
	}
	d.Rebase.Stopped = false

	// 开不了的：所有仓库视图、进行中的操作、合并提交、超过上限
	a.SetHistAll(true)
	if _, why := a.RebaseRange(r, c1); why == "" || a.RebaseView(r) {
		t.Error("所有仓库视图里能开")
	}
	a.SetHistAll(false)
	r.Extra.Ongoing = git.Merging
	if _, why := a.RebaseRange(r, c1); why != "有进行中的操作" {
		t.Errorf("进行中 %q", why)
	}
	r.Extra.Ongoing = git.NoOp
	saved := h.Commits
	m := git.Commit{Hash: "m", Parents: []string{c3.Hash, "x"}}
	h.Commits, r.Extra.Head = append([]git.Commit{m}, saved...), "m"
	if _, why := a.RebaseRange(r, c1); why != "范围里有合并提交" {
		t.Errorf("合并提交 %q", why)
	}
	long := make([]git.Commit, RebaseLimit+2)
	for i := range long {
		long[i] = git.Commit{Hash: fmt.Sprint("h", i), Parents: []string{fmt.Sprint("h", i+1)}}
	}
	h.Commits, r.Extra.Head = long, "h0"
	if _, why := a.RebaseRange(r, long[RebaseLimit-1]); why != "" {
		t.Errorf("正好 %d 个 %q", RebaseLimit, why)
	}
	if _, why := a.RebaseRange(r, long[RebaseLimit]); why != "一次最多 100 个提交" {
		t.Errorf("超过上限 %q", why)
	}
	if _, why := a.RebaseRange(r, git.Commit{Hash: "nope"}); why == "" {
		t.Error("不在列表里的提交能开")
	}
}

// 计划写成 todo：squash / fixup 都是 fixup，一组最后一条之后 exec 合在一起的信息；reword 是 pick + exec；
// 信息没变的 reword 不加 exec；路径里的单引号按 shell 的写法转义；写信息文件出错就报错。
func TestRebaseTodo(t *testing.T) {
	commit := func(h, subject, body string) git.Commit { return git.Commit{Hash: h, Subject: subject, Body: body} }
	p := &docs.RebasePlan{Steps: []docs.RebaseStep{
		{Commit: commit("A", "a", "正文 a"), Action: docs.RebasePick},
		{Commit: commit("B", "b", ""), Action: docs.RebaseSquash},
		{Commit: commit("C", "c", ""), Action: docs.RebaseFixup},
		{Commit: commit("D", "d", ""), Action: docs.RebaseDrop},
		{Commit: commit("E", "e", ""), Action: docs.RebaseReword, Msg: "e2", Def: "e"},
		{Commit: commit("F", "f", ""), Action: docs.RebaseEdit},
		{Commit: commit("G", "g", ""), Action: docs.RebaseSquash},
		{Commit: commit("H", "h", ""), Action: docs.RebaseReword},
	}}
	var msgs []string
	todo, err := RebaseTodo(p, func(text string) (string, error) {
		msgs = append(msgs, text)
		return fmt.Sprintf("/tmp/it's %d.msg", len(msgs)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	amend := func(n int) string {
		return fmt.Sprintf("exec git -c core.editor=true commit --amend -q -F '/tmp/it'\\''s %d.msg'\n", n)
	}
	want := "pick A\nfixup B\nfixup C\ndrop D\n" + amend(1) + "pick E\n" + amend(2) + "edit F\nfixup G\n" + amend(3) + "pick H\n"
	if todo != want {
		t.Errorf("todo:\n%s\n要:\n%s", todo, want)
	}
	if strings.Join(msgs, "|") != "a\n\n正文 a\n\nb|e2|f\n\ng" {
		t.Errorf("信息 %q", msgs)
	}
	if shellQuote("a'b") != `'a'\''b'` {
		t.Errorf("shellQuote %s", shellQuote("a'b"))
	}
	if _, err := RebaseTodo(p, func(string) (string, error) { return "", os.ErrPermission }); err == nil {
		t.Error("写信息出错没报错")
	}

	// 写进临时文件：todo、两个信息…结尾一个换行
	todoPath, files, err := writeRebaseFiles(p)
	if err != nil {
		t.Fatal(err)
	}
	defer removeFiles(files)
	if len(files) != 4 || files[3] != todoPath || !strings.HasSuffix(todoPath, ".todo") || !strings.HasPrefix(filepath.Base(todoPath), "workbench-rebase-") {
		t.Fatalf("临时文件 %q", files)
	}
	if b, _ := os.ReadFile(files[1]); string(b) != "e2\n" {
		t.Errorf("reword 的信息文件 %q", b)
	}
	if b, _ := os.ReadFile(todoPath); !strings.Contains(string(b), shellQuote(files[0])) {
		t.Errorf("todo 里没有信息文件的路径:\n%s", b)
	}
}

// 6 个提交：c6 挪到 c1 后面并入（squash，改了信息）、c2 丢掉、c3 改信息、c4 并入 c3（fixup）、c5 不动 → 3 个；
// 工作区有没提交的改动，先自动贮藏、做完恢复。先问；跑完 toast、关掉向导、删临时文件。
func TestRebaseRun(t *testing.T) {
	repo := linearRepo(t, "run", 6)
	repo.Write("f.txt", "1\n2\n3\n脏\n")
	a, r, wait := rebaseApp(t, repo)
	h := &r.Hist
	c1, base := h.Commits[5], h.Commits[6]
	a.OpenRebase(r, c1)
	d := a.CurrentDoc()
	p := d.Rebase
	for i := 5; i > 1; i-- {
		a.MoveRebaseStep(p, i, -1)
	}
	if p.Steps[1].Commit.Subject != "c6" || p.Sel != 1 {
		t.Fatalf("c6 没挪上去：%s", p.Steps[1].Commit.Subject)
	}
	a.SetRebaseAction(p, 1, docs.RebaseSquash)
	p.Steps[1].Msg = "一和六"
	a.SetRebaseAction(p, 2, docs.RebaseDrop)
	a.SetRebaseAction(p, 3, docs.RebaseReword)
	p.Steps[3].Msg = "三（改）"
	a.SetRebaseAction(p, 4, docs.RebaseFixup)
	if RebaseResult(p) != 3 || RebaseProblem(p) != "" {
		t.Fatalf("结果 %d 个，problem %q", RebaseResult(p), RebaseProblem(p))
	}

	a.StartRebase(d)
	if a.Confirm == nil || a.Confirm.Title != "改写 run 的 6 个提交？" || a.Confirm.Button != "开始变基" ||
		!strings.Contains(a.Confirm.Message, "没提交的改动先自动贮藏") || r.Busy != "" {
		t.Fatalf("先问：%+v，busy %q", a.Confirm, r.Busy)
	}
	a.Confirm.Fn()
	if r.Busy != "交互式变基" || !p.Running || len(p.Files) != 3 || RebaseProblem(p) == "" {
		t.Fatalf("跑起来：busy %q running %v，%d 个临时文件", r.Busy, p.Running, len(p.Files))
	}
	files := p.Files
	wait("变基", func() bool { return idle(r)() && !p.Running })
	if a.Toast != "变基完成：6 个提交 → 3 个" || a.AlertOpen {
		t.Fatalf("toast %q，alert %q %q\n%s", a.Toast, a.AlertTitle, a.AlertMsg, a.Output[len(a.Output)-1].Text)
	}
	if a.GroupOf(d) != nil || r.rebase != nil || len(p.Files) != 0 {
		t.Error("做完向导还开着 / 计划还挂着")
	}
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("临时文件 %s 还在", f)
		}
	}
	if log := repo.Git("log", "--format=%s"); log != "c5\n三（改）\n一和六\nbase" {
		t.Errorf("变基后的历史:\n%s", log)
	}
	if out := repo.Git("show", "--stat", "--format=", "HEAD~2"); !strings.Contains(out, "1.txt") || !strings.Contains(out, "6.txt") {
		t.Errorf("一和六改的文件:\n%s", out)
	}
	if out := repo.Git("ls-files"); strings.Contains(out, "2.txt") {
		t.Errorf("丢掉的 c2 还在:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(repo.Root, "f.txt")); string(b) != "1\n2\n3\n脏\n" || repo.Git("stash", "list") != "" {
		t.Errorf("自动贮藏没恢复：f.txt %q，stash %q", b, repo.Git("stash", "list"))
	}
	last := a.Output[len(a.Output)-1]
	if want := fmt.Sprintf("$ git rebase -i %s（6 个提交：pick 2、reword 1、squash 1、fixup 1、drop 1）", git.Short(base.Hash)); last.Title != want {
		t.Errorf("输出栏 %q，要 %q", last.Title, want)
	}
}

// 停在 edit：不弹失败，向导写停下的原因、临时文件留着（后面 reword 的 exec 还要读），横幅继续完以后向导标成结束、删临时文件。
func TestRebaseEditStops(t *testing.T) {
	repo := linearRepo(t, "edit", 3)
	a, r, wait := rebaseApp(t, repo)
	a.OpenRebase(r, r.Hist.Commits[2])
	d := a.CurrentDoc()
	p := d.Rebase
	a.SetRebaseAction(p, 1, docs.RebaseEdit)
	a.SetRebaseAction(p, 2, docs.RebaseReword)
	p.Steps[2].Msg = "三！"
	a.StartRebase(d)
	a.Confirm.Fn()
	wait("停在 edit", func() bool { return idle(r)() && !p.Running && r.Extra.Ongoing == git.Rebasing })
	if !p.Stopped || p.Ended || p.Reason != "停在 edit 这一步" || a.AlertOpen || a.Toast != "变基停下了：停在 edit 这一步" {
		t.Fatalf("停下：stopped %v reason %q alert %v toast %q", p.Stopped, p.Reason, a.AlertOpen, a.Toast)
	}
	if a.GroupOf(d) == nil || r.rebase != p || len(p.Files) != 2 || RebaseProblem(p) != "这次变基已经开始了" {
		t.Fatalf("停下后向导 / 计划：%d 个文件", len(p.Files))
	}
	for _, f := range p.Files {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("停下时临时文件没了：%v", err)
		}
	}
	files := p.Files
	a.ContinueOngoing(r)
	wait("继续变基", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	if !p.Ended || r.rebase != nil || len(p.Files) != 0 {
		t.Errorf("继续完：ended %v，%d 个文件", p.Ended, len(p.Files))
	}
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("临时文件 %s 还在", f)
		}
	}
	if log := repo.Git("log", "--format=%s"); log != "三！\nc2\nc1\nbase" {
		t.Errorf("继续以后的历史:\n%s", log)
	}
}

// 换顺序造出冲突：停下、原因"有冲突"；向导关了临时文件也留着，横幅里放弃以后才删，HEAD 回到原来的。
func TestRebaseConflictStops(t *testing.T) {
	repo := testutil.NewRepo(t, "conflict")
	repo.Write("f.txt", "1\n2\n3\n")
	repo.Commit("base")
	repo.Write("f.txt", "1\nA\n3\n")
	repo.Commit("改成 A")
	repo.Write("f.txt", "1\nB\n3\n")
	repo.Commit("改成 B")
	head := repo.Git("rev-parse", "HEAD")
	a, r, wait := rebaseApp(t, repo)
	a.OpenRebase(r, r.Hist.Commits[1])
	d := a.CurrentDoc()
	p := d.Rebase
	a.MoveRebaseStep(p, 1, -1)
	a.StartRebase(d)
	a.Confirm.Fn()
	wait("冲突", func() bool { return idle(r)() && !p.Running && r.Extra.Ongoing == git.Rebasing })
	if !p.Stopped || p.Reason != "有冲突" || a.AlertOpen || r.Status.Conflicts == 0 {
		t.Fatalf("冲突停下：stopped %v reason %q alert %v conflicts %d", p.Stopped, p.Reason, a.AlertOpen, r.Status.Conflicts)
	}
	files := p.Files
	a.CloseDoc(d)
	if a.GroupOf(d) != nil || len(p.Files) == 0 {
		t.Fatal("关向导：标签还在，或临时文件删早了")
	}
	if _, err := os.Stat(files[0]); err != nil {
		t.Fatalf("关了向导临时文件就没了：%v", err)
	}
	a.AbortOngoing(r)
	a.Confirm.Fn()
	wait("放弃变基", func() bool { return idle(r)() && r.Extra.Ongoing == git.NoOp })
	if !p.Ended || r.rebase != nil {
		t.Errorf("放弃以后 ended %v", p.Ended)
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Errorf("放弃以后临时文件还在：%v", err)
	}
	if got := repo.Git("rev-parse", "HEAD"); got != head {
		t.Errorf("放弃以后 HEAD %s，要 %s", got, head)
	}
}
