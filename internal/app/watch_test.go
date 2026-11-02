package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/watch"
)

// 基本验收：开着监听，外部改文件、外部提交、外部切分支之后跟上——变更数、打开的文件、历史和分支。
// 单独跑每步 0.5–0.75 s；预算 3 s（整包顺序跑时前面的测试留下的负载能拖到 2 s；-race 下再放宽，见 testutil.Budget）。全在临时仓库上。
func TestWatchFollowsExternalChanges(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.LoadHistory(r, true)
	a.EnsureFiles(r)
	readme := a.OpenFile(r, "README.md", true)
	at := a.OpenFile(r, "a.txt", true)
	wait("读状态", func() bool { return idle(r)() && r.Hist.Loaded && r.Files.Loaded && readme.Buf != nil && at.Buf != nil })
	a.StartWatching()
	defer a.Close()
	wait("监听开了", r.Watching)
	if r.Status.Count != 2 {
		t.Fatalf("初始变更 %+v", r.Status)
	}

	within := func(what string, act func(), cond func() bool) {
		t.Helper()
		start := time.Now()
		act()
		wait(what, cond)
		if d := time.Since(start); d > testutil.Budget(3*time.Second) {
			t.Fatalf("%s 用了 %v，超过 %v", what, d, testutil.Budget(3*time.Second))
		} else {
			t.Logf("%s：%v 跟上（%s）", what, d.Round(time.Millisecond), r.LastWhy)
		}
	}

	within("外部改文件", func() {
		os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n\n外面改的\n"), 0o644)
	}, func() bool {
		return idle(r)() && r.Status.Unstaged == 2 && readme.Buf.String() == "# demo\n\n外面改的\n" && !readme.Modified()
	})
	within("外部新建文件", func() {
		os.WriteFile(filepath.Join(root, "new.txt"), []byte("新\n"), 0o644)
	}, func() bool { return idle(r)() && r.Status.Untracked == 2 && hasFile(r, "new.txt") })

	within("外部提交", func() {
		testutil.GitOut(t, root, "add", "-A")
		testutil.GitOut(t, root, "commit", "-q", "-m", "外面的提交")
	}, func() bool {
		return idle(r)() && r.Status.Count == 0 && !r.Hist.Loading && len(r.Hist.Commits) > 0 && r.Hist.Commits[0].Subject == "外面的提交"
	})

	within("外部切分支", func() { testutil.GitOut(t, root, "switch", "-q", "feature") }, func() bool {
		return idle(r)() && r.Status.Branch == "feature" && !r.Hist.Loading && r.Hist.Commits[0].Subject == "feature 提交" &&
			at.Buf.String() == mustRead(t, filepath.Join(root, "a.txt")) && hasFile(r, "feat.txt")
	})

	// 外面又切回来：分支列表和打开的文件都回到 main 的
	within("外部切回 main", func() { testutil.GitOut(t, root, "switch", "-q", "main") }, func() bool {
		return idle(r)() && r.Status.Branch == "main" && at.Buf.String() == mustRead(t, filepath.Join(root, "a.txt"))
	})
}

// 正在读状态的时候又来一次刷新：读完再读一遍，不丢。
func TestRefreshAgainWhileLoading(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	if !r.Loading {
		t.Fatal("没在读")
	}
	// 第一次读的 goroutine 可能已经跑完 git status 了，这时写的文件它看不到
	os.WriteFile(filepath.Join(root, "late.txt"), []byte("晚\n"), 0o644)
	a.RefreshRepo(r, WhyWatchFiles)
	a.RefreshRepo(r, WhyWatchGit) // 合并成一次，取不是"只有文件"的那个
	if r.again != WhyWatchGit {
		t.Fatalf("again = %q", r.again)
	}
	wait("读完两遍", func() bool { return idle(r)() && r.again == "" && r.Status.Untracked == 2 })
	if r.LastWhy != WhyWatchGit {
		t.Fatalf("LastWhy = %q", r.LastWhy)
	}
}

// 监听停了（仓库目录被移走）：输出栏记一条，之后切回窗口会重读这个仓库。
func TestWatchStoppedFallsBackToFocus(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	a, wait := newTestApp(t, ws)
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	a.StartWatching()
	defer a.Close()
	wait("监听开了", func() bool { return idle(r)() && r.Watching() })
	a.RefreshOnFocus() // 在监听的仓库切回窗口不重读
	if r.Loading {
		t.Fatal("在监听的仓库切回窗口也重读了")
	}
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(moved, root)
	wait("监听停了", func() bool { return !r.Watching() && r.WatchErr != nil })
	if n := len(a.Output); n == 0 || a.Output[n-1].Title != "文件监听停了" {
		t.Fatalf("输出栏 %+v", a.Output)
	}
	a.LastRefresh = time.Time{}
	a.RefreshOnFocus()
	if !r.Loading || r.LastWhy != WhyFocus {
		t.Fatalf("监听停了切回窗口没重读：loading=%v why=%q", r.Loading, r.LastWhy)
	}
	wait("读完", idle(r))
}

func hasFile(r *Repo, rel string) bool {
	return r.Files.Loaded && !r.Files.Loading && r.Files.Has(rel)
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 增量刷新：子目录里改、新建、删文件和保存，文件树跟上、不整棵 git ls-files；被忽略的新文件不加；
// 新建目录、根下的变化（"."）、切分支（.git 变了）、手动刷新各整棵读一次。变化照监听报的样子造（不开真的监听，
// FSEvents 偶尔补的 "." 会让计数不稳）；全在临时仓库上。
func TestTreeIncremental(t *testing.T) {
	r0 := testutil.NewRepo(t, "inc")
	r0.Write(".gitignore", "*.log\n")
	r0.Write("src/a.go", "package src\n")
	r0.Write("src/b.go", "package src\n")
	r0.Write("docs/x.md", "# x\n")
	r0.Commit("初始")
	r0.Git("switch", "-q", "-c", "other")
	r0.Write("feat/f.go", "package feat\n")
	r0.Commit("feature")
	r0.Git("switch", "-q", "main")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Repos[0]
	var calls atomic.Int32
	old := lsFiles
	lsFiles = func(ctx context.Context, root string) (git.Listing, error) { calls.Add(1); return old(ctx, root) }
	t.Cleanup(func() { lsFiles = old })
	a.RefreshRepo(r, WhyManual)
	a.EnsureFiles(r)
	wait("读文件树", func() bool { return idle(r)() && hasFile(r, "src/a.go") })

	step := func(what string, act func(), c watch.Change, more int32, cond func() bool) {
		t.Helper()
		before := calls.Load()
		act()
		if c.Git || c.Files {
			a.watchChanged(r, c)
		} else {
			a.RefreshRepo(r, WhyManual)
		}
		wait(what, func() bool { return idle(r)() && !r.Files.Loading && cond() })
		if got := calls.Load() - before; got != more {
			t.Errorf("%s：整棵读了 %d 次，要 %d 次", what, got, more)
		}
	}
	src := watch.Change{Files: true, Dirs: []string{"src"}}
	paths := func() []string { return r.Files.Paths }
	step("改文件", func() { r0.Write("src/a.go", "package src\n\n// 改\n") }, src, 0, func() bool { return r.Status.Unstaged == 1 })
	before := paths()
	step("新建文件", func() { r0.Write("src/c.go", "package src\n") }, src, 0, func() bool { return hasFile(r, "src/c.go") })
	if !slices.Contains(paths(), "src/c.go") || slices.Contains(before, "src/c.go") {
		t.Errorf("Paths 该换一份新的、旧的不变：%v / %v", paths(), before)
	}
	step("被忽略的新文件", func() { r0.Write("src/debug.log", "x\n") }, src, 0, func() bool { return true })
	if hasFile(r, "src/debug.log") {
		t.Error("被忽略的文件加进树了")
	}
	step("删文件", func() { os.Remove(filepath.Join(r0.Root, "src/b.go")) }, src, 0, func() bool { return !r.Files.Has("src/b.go") })
	// 删到目录空了：磁盘上的空目录留在树里（整棵读也列空目录）
	step("删到目录空了", func() { os.Remove(filepath.Join(r0.Root, "docs/x.md")) }, watch.Change{Files: true, Dirs: []string{"docs"}}, 0,
		func() bool { return !r.Files.Has("docs/x.md") && r.Files.Dir("docs") != nil && r.Files.Extra["docs"] })
	// 保存：只看那一个文件
	d := a.OpenFile(r, "src/a.go", true)
	wait("读文件", func() bool { return !d.Loading })
	d.Buf.Set("// 头\n" + d.Buf.String())
	before, n := paths(), calls.Load()
	if err := a.WriteDoc(d, false); err != nil {
		t.Fatal(err)
	}
	wait("保存后", idle(r)) // WriteDoc 里已经开始读了
	if calls.Load() != n || !slices.Equal(paths(), before) {
		t.Errorf("保存后整棵读了 %d 次、Paths 变了 %v", calls.Load()-n, !slices.Equal(paths(), before))
	}

	step("新建目录", func() { r0.Write("src/sub/n.go", "package sub\n") }, src, 1, func() bool { return hasFile(r, "src/sub/n.go") })
	step("新建空目录", func() { os.Mkdir(filepath.Join(r0.Root, "src/empty"), 0o755) }, src, 0, func() bool { return r.Files.Dir("src/empty") != nil })
	step("被忽略的空目录", func() { os.Mkdir(filepath.Join(r0.Root, "src/out.log"), 0o755) }, src, 0, func() bool { return true })
	if r.Files.Extra["src/out.log"] {
		t.Error("被忽略的空目录加进树了")
	}
	step("删目录", func() { os.RemoveAll(filepath.Join(r0.Root, "src/sub")) }, src, 1, func() bool { return r.Files.Dir("src/sub") == nil })
	step("根下的文件", func() { r0.Write("top.txt", "t\n") }, watch.Change{Git: true, Files: true, Full: true}, 1, func() bool { return hasFile(r, "top.txt") })
	step("切分支", func() { r0.Git("switch", "-q", "other") }, watch.Change{Git: true}, 1, func() bool { return hasFile(r, "feat/f.go") })
	step("手动刷新", func() {}, watch.Change{}, 1, func() bool { return true })
}

// 量增量刷新：5 万个文件（200 个目录 × 250 个），整棵 ls-files + 建树 vs 增量（监听报一个目录、
// 保存一个文件、目录里新建一个文件）的用时和 HeapAlloc。含符号链接（10 个已跟踪、5 个未跟踪，另有 5 个
// 未跟踪的普通文件）：整棵读对比不带 -s 的老读法和带 -s + 只看未跟踪的那几个（ReadLinks）。
// 慢（造仓库要十几秒），WORKBENCH_MEASURE=1 才跑。
func TestMeasureIncremental(t *testing.T) {
	if os.Getenv("WORKBENCH_MEASURE") == "" {
		t.Skip("WORKBENCH_MEASURE=1 才量")
	}
	r0 := testutil.NewRepo(t, "big")
	for d := range 200 {
		dir := filepath.Join(r0.Root, fmt.Sprintf("d%03d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 250 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", f)), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for k := range 10 {
		if err := os.Symlink(fmt.Sprintf("d%03d", k), filepath.Join(r0.Root, fmt.Sprintf("link%d", k))); err != nil {
			t.Fatal(err)
		}
	}
	r0.Commit("5 万个文件")
	for k := range 5 {
		os.Symlink("../d000/f000.txt", filepath.Join(r0.Root, "d001", fmt.Sprintf("ul%d", k)))
		os.WriteFile(filepath.Join(r0.Root, "d001", fmt.Sprintf("u%d.txt", k)), []byte("u\n"), 0o644)
	}
	const total = 50020
	ctx := context.Background()
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	var tr files.Tree
	full := func() {
		l, err := lsFiles(ctx, r0.Root)
		if err != nil || len(l.Paths) != total || len(l.Links) != 10 || len(l.Others) != 10 {
			t.Fatalf("ls-files %d 个、链接 %d、未跟踪 %d %v", len(l.Paths), len(l.Links), len(l.Others), err)
		}
		links := files.ReadLinks(r0.Root, slices.Concat(l.Links, l.Others))
		tr.Done(tr.Start(), l.Paths, nil, nil)
		tr.Links = links
		if len(links) != 15 {
			t.Fatalf("链接 %d 个", len(links))
		}
	}
	old := func() {
		paths, err := git.LsFiles(ctx, r0.Root)
		if err != nil || len(paths) != total {
			t.Fatalf("ls-files %d %v", len(paths), err)
		}
		var t2 files.Tree
		t2.Done(t2.Start(), paths, nil, nil)
	}
	measure := func(what string, n int, fn func()) {
		t.Helper()
		h0 := heap()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		alloc0 := ms.TotalAlloc
		start := time.Now()
		for range n {
			fn()
		}
		d := time.Since(start) / time.Duration(n)
		runtime.ReadMemStats(&ms)
		t.Logf("%s：每次 %v，分配 %.2f MiB，HeapAlloc %+.2f MiB（之后 %.1f MiB）", what, d.Round(time.Microsecond),
			float64(ms.TotalAlloc-alloc0)/float64(n)/(1<<20), (float64(heap())-float64(h0))/(1<<20), float64(heap())/(1<<20))
	}
	full()
	measure("整棵读，老读法（ls-files ×2 不带 -s + 建树）", 5, old)
	measure("整棵读（ls-files ×2 带 -s + 未跟踪的 ReadLinks + 建树）", 5, full)
	l, _ := lsFiles(ctx, r0.Root)
	cands := slices.Concat(l.Links, l.Others)
	measure("其中 ReadLinks 一遍（10 个已跟踪的链接、5 个未跟踪的链接、5 个未跟踪的普通文件）", 20, func() { files.ReadLinks(r0.Root, cands) })
	measure("增量：监听报 d042（没变）", 20, func() {
		d := tr.Diff(r0.Root, []string{"d042"}, nil)
		if d.Full || len(d.Added)+len(d.Removed) > 0 {
			t.Fatal("没变却有改动")
		}
	})
	measure("增量：保存 d042/f007.txt", 20, func() { tr.Diff(r0.Root, nil, []string{"d042/f007.txt"}) })
	i := 0
	measure("增量：d042 里新建一个文件（Diff + git ls-files 问一次 + Apply）", 10, func() {
		p := fmt.Sprintf("d042/new%03d.txt", i)
		i++
		os.WriteFile(filepath.Join(r0.Root, filepath.FromSlash(p)), []byte("n\n"), 0o644)
		d := tr.Diff(r0.Root, []string{"d042"}, nil)
		out, err := listed(ctx, r0.Root, d.Added)
		if err != nil || !slices.Equal(out, []string{p}) {
			t.Fatalf("新文件 %v %v", out, err)
		}
		tr.Apply(out, d.Removed, d.Unlinked, files.ReadLinks(r0.Root, slices.Concat(out, d.Relinked)))
	})
	if tr.Count != total+10 {
		t.Errorf("Count %d", tr.Count)
	}
}
