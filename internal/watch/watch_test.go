package watch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/testutil"
)

// 临时仓库上的三种外部变化（改文件、提交、切分支）都报出来（一般几百毫秒，预算 3 s）；被忽略的目录、node_modules 和
// .git/objects 里的变化不报；仓库目录删了报 ErrGone。
func TestWatchRepo(t *testing.T) {
	r := testutil.NewRepo(t, "w")
	r.Write(".gitignore", "build/\n")
	r.Write("a.txt", "一\n")
	r.Write("sub/b.txt", "二\n")
	r.Commit("初始")
	r.Git("branch", "other")
	r.Write("build/out.bin", "x")
	r.Write("node_modules/m/index.js", "x")

	changes := make(chan Change, 64)
	stopped := make(chan error, 1)
	w, err := Start(r.Root, func(c Change) { changes <- c }, func(err error) { stopped <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// expect 等一个满足 ok 的变化，返回用了多久；之后把这一轮剩下的都收掉。
	expect := func(what string, ok func(Change) bool, act func()) {
		t.Helper()
		start := time.Now()
		act()
		deadline := time.After(testutil.Budget(3 * time.Second)) // 一般几百毫秒；整包顺序跑时负载重，留 3 s
		for {
			select {
			case c := <-changes:
				if ok(c) {
					t.Logf("%s：%v 后报了 %v", what, time.Since(start).Round(time.Millisecond), c)
					quiet(changes, Debounce+200*time.Millisecond)
					return
				}
			case err := <-stopped:
				t.Fatalf("%s：监听停了 %v", what, err)
			case <-deadline:
				t.Fatalf("%s：3 s 内没报", what)
			}
		}
	}
	// 不该报的：被忽略的目录、node_modules、只写 objects 的 git 命令。先做这个：切分支之后 FSEvents
	// 有时会再给根目录补一个提示（"."，两种都算），那是正常的。先等探针报了，建仓库时的事件都收掉。
	// 负载重时建仓库 / 探针那阵还会晚补一个根目录提示（Full），那不算报了被忽略的，记一句接着等；
	// 只有带着 build、node_modules 目录的，或只有 .git 的（objects 漏出来了）才算。
	probe(t, changes, func() { r.Write("probe.txt", "p") })
	r.Write("build/out.bin", "y")
	r.Write("node_modules/m/index.js", "y")
	r.Git("hash-object", "-w", "a.txt")
	for end := time.After(testutil.Budget(Debounce + 500*time.Millisecond)); ; {
		select {
		case c := <-changes:
			if c.Full {
				t.Logf("晚到一个根目录提示：%+v，接着等", c)
				continue
			}
			t.Fatalf("被忽略的变化报了：%+v", c)
		case <-end:
		}
		break
	}

	files := func(c Change) bool { return c.Files }
	gitc := func(c Change) bool { return c.Git }

	// 子目录里的变化带着目录（系统偶尔补一个 "." 就是 Full）
	inSub := func(c Change) bool { return c.Files && (c.Full || slices.Contains(c.Dirs, "sub")) }
	expect("外部改文件", inSub, func() { r.Write("sub/b.txt", "二改\n") })
	expect("外部新建文件", files, func() { r.Write("sub/new/c.txt", "三\n") })
	expect("外部提交", gitc, func() { r.Git("add", "-A"); r.Git("commit", "-q", "-m", "改") })
	expect("外部切分支", gitc, func() { r.Git("switch", "-q", "other") })
	expect("外部删文件", files, func() { os.Remove(filepath.Join(r.Root, "sub", "b.txt")) })

	// 仓库目录删了：停下，报 ErrGone
	if err := os.RemoveAll(r.Root); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, ErrGone) {
			t.Fatalf("删了目录报的是 %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("删了仓库目录，监听没停")
	}
}

// Close 之后不再回调，也不报停。
func TestWatchClose(t *testing.T) {
	r := testutil.NewRepo(t, "c")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	calls := make(chan string, 8)
	w, err := Start(r.Root, func(Change) { calls <- "change" }, func(error) { calls <- "stop" })
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	r.Write("a.txt", "二\n")
	select {
	case c := <-calls:
		t.Fatalf("Close 之后还回调了 %s", c)
	case <-time.After(Debounce + 300*time.Millisecond):
	}
}

// 链接的工作树：git 目录在主仓库的 .git/worktrees 下，提交和切分支也要报。
func TestWatchWorktree(t *testing.T) {
	r := testutil.NewRepo(t, "main")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	wt := filepath.Join(t.TempDir(), "wt")
	r.Git("worktree", "add", "-q", "-b", "wt", wt)
	changes := make(chan Change, 16)
	w, err := Start(wt, func(c Change) { changes <- c }, func(err error) { t.Errorf("停了：%v", err) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("二\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	quiet(changes, Debounce+300*time.Millisecond)
	git := func(args ...string) {
		t.Helper()
		r.Git(append([]string{"-C", wt}, args...)...)
	}
	git("commit", "-qam", "工作树里提交")
	select {
	case c := <-changes:
		if !c.Git {
			t.Fatalf("工作树里提交报的是 %v", c)
		}
	case <-time.After(testutil.Budget(3 * time.Second)): // 一般几百毫秒；整包顺序跑时负载重，和别的一样留 3 s
		t.Fatal("工作树里提交 3 s 内没报")
	}
}

// 主仓库的监听：外部 git worktree add / remove、在链接的工作树里切分支，都要报 Git（工作树列表跟着变；预算 3 s）；
// worktrees/<名字> 再往下（logs）不看。
func TestWatchWorktreeAdded(t *testing.T) {
	r := testutil.NewRepo(t, "main")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	r.Git("branch", "other")
	changes := make(chan Change, 16)
	w, err := Start(r.Root, func(c Change) { changes <- c }, func(err error) { t.Errorf("停了：%v", err) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	wt := filepath.Join(t.TempDir(), "main-feat")
	expect := func(what string, do func()) {
		t.Helper()
		quiet(changes, Debounce+300*time.Millisecond)
		start := time.Now()
		do()
		for {
			select {
			case c := <-changes:
				if c.Git {
					t.Logf("%s：%v 报了 %v", what, time.Since(start).Round(time.Millisecond), c)
					return
				}
			case <-time.After(testutil.Budget(3*time.Second) - time.Since(start)):
				t.Fatalf("%s 3 s 内没报 Git", what)
			}
		}
	}
	expect("worktree add", func() { r.Git("worktree", "add", "-q", "-b", "feat", wt) })
	expect("工作树里 switch", func() { r.Git("-C", wt, "switch", "-q", "other") })
	expect("worktree remove", func() { r.Git("worktree", "remove", wt) })
	if !ignoreGitDir("worktrees/x/logs", true) || ignoreGitDir("worktrees/x", true) || ignoreGitDir("worktrees", true) || !ignoreGitDir("objects", true) {
		t.Error("ignoreGitDir 对 worktrees 的取舍不对")
	}
}

// 只看工作区（"工作树"块展开时给工作树用）：链接工作树和主工作树里新建文件报 Files；被忽略的目录、
// 在工作树里提交（只写 git 目录）不报。
func TestWatchTree(t *testing.T) {
	r := testutil.NewRepo(t, "main")
	r.Write(".gitignore", "build/\n")
	r.Write("a.txt", "一\n")
	r.Commit("初始")
	wt := filepath.Join(t.TempDir(), "wt")
	r.Git("worktree", "add", "-q", "-b", "wt", wt)
	for _, root := range []string{wt, r.Root} {
		t.Run(filepath.Base(root), func(t *testing.T) {
			in := r.In(root)
			in.Write("build/out.bin", "x") // 根下新建目录 FSEvents 会给根一个提示（"."），先建好
			changes := make(chan Change, 16)
			w, err := StartTree(root, func(c Change) { changes <- c }, func(err error) { t.Errorf("停了：%v", err) })
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			probe(t, changes, func() { in.Write("probe.txt", "p") })
			in.Write("build/out.bin", "y")
			in.Git("commit", "-q", "--allow-empty", "-m", "提交")
			select {
			case c := <-changes:
				t.Fatalf("被忽略的目录、提交报了 %v", c)
			case <-time.After(Debounce + 500*time.Millisecond):
			}
			in.Write("b.txt", "二\n")
			select {
			case c := <-changes:
				if !c.Files {
					t.Fatalf("新建文件报的是 %v", c)
				}
			case <-time.After(testutil.Budget(2 * time.Second)):
				t.Fatal("新建文件 2 s 内没报")
			}
		})
	}
}

// 普通文件夹：改文件报 Files；node_modules、点开头的目录里的不报；git init 了报 Git。
func TestWatchPlain(t *testing.T) {
	root := t.TempDir()
	write := func(f, s string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "一")
	write("node_modules/m/i.js", "x")
	write(".cache/z", "x")
	if _, err := Start(root, func(Change) {}, nil); err == nil {
		t.Fatal("普通文件夹 Start 应该失败")
	}
	changes := make(chan Change, 16)
	w, err := StartPlain(root, func(c Change) { changes <- c }, func(err error) { t.Errorf("停了：%v", err) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	probe(t, changes, func() { write("probe.txt", "p") })
	write("node_modules/m/i.js", "y")
	write(".cache/z", "y")
	select {
	case c := <-changes:
		t.Fatalf("不该看的目录报了 %v", c)
	case <-time.After(Debounce + 500*time.Millisecond):
	}
	wait := func(what string, ok func(Change) bool) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case c := <-changes:
				if ok(c) {
					quiet(changes, Debounce+200*time.Millisecond)
					return
				}
			case <-deadline:
				t.Fatalf("%s：2 s 内没报", what)
			}
		}
	}
	write("sub/b.txt", "二")
	wait("新建文件", func(c Change) bool { return c.Files })
	testutil.GitOut(t, root, "init", "-q")
	wait("git init", func(c Change) bool { return c.Git })
}

// probe 在测"不该报"之前用：act 改一个会报的文件，等到它报了再 quiet。FSEvents 按顺序给，
// 探针报了，之前的（建仓库、提交、切分支）就都到了；负载重时它们会晚到，只 quiet 一段时间挡不住。
func probe(t *testing.T, ch chan Change, act func()) {
	t.Helper()
	act()
	deadline := time.After(testutil.Budget(3 * time.Second))
	for {
		select {
		case c := <-ch:
			if c.Files {
				quiet(ch, Debounce+300*time.Millisecond)
				return
			}
		case <-deadline:
			t.Fatal("探针 3 s 内没报")
		}
	}
}

// quiet 收掉 d 之内来的变化。
func quiet(ch chan Change, d time.Duration) {
	end := time.After(d)
	for {
		select {
		case <-ch:
		case <-end:
			return
		}
	}
}

// 合并：目录取并集、祖先盖住子孙、重复的不记；超过 MaxDirs 个或碰到 Full 就 Full、目录清空；
// 根下的提示 "." 是 Full，.git 里的只是 Git。
func TestChangeAdd(t *testing.T) {
	var c Change
	for _, d := range []string{"a/b", "x", "a/b/c", "a", "x", "ab"} {
		c.add(workChange(d))
	}
	if want := []string{"x", "a", "ab"}; !c.Files || c.Full || c.Git || !slices.Equal(c.Dirs, want) {
		t.Errorf("合并后 %+v，要目录 %v", c, want)
	}
	c.add(workChange(".git/index"))
	if !c.Git || c.Full || len(c.Dirs) != 3 {
		t.Errorf(".git 的提示 %+v", c)
	}
	c.add(workChange("."))
	if !c.Full || c.Dirs != nil {
		t.Errorf("根下的提示要 Full：%+v", c)
	}
	c.add(workChange("y"))
	if !c.Full || c.Dirs != nil {
		t.Errorf("Full 之后不再记目录：%+v", c)
	}
	var many Change
	for i := range MaxDirs + 1 {
		many.add(workChange(fmt.Sprintf("d%d", i)))
		if i < MaxDirs && (many.Full || len(many.Dirs) != i+1) {
			t.Fatalf("第 %d 个目录就 Full 了：%v", i+1, many.Full)
		}
	}
	if !many.Full || many.Dirs != nil {
		t.Errorf("超过 %d 个目录要 Full：%v %d", MaxDirs, many.Full, len(many.Dirs))
	}
}
