package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// gatedBlobs 把读 HEAD 版本换成要等放行的：每次读都等 release 关掉（或被放行一次），数读了几次。
type gatedBlobs struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func gateBlobs(t *testing.T) *gatedBlobs {
	g := &gatedBlobs{release: make(chan struct{})}
	restore := docs.SetBlobReader(func(root, rev, rel string) []byte {
		g.mu.Lock()
		g.calls++
		g.mu.Unlock()
		<-g.release
		return git.Blob(root, rev, rel)
	})
	t.Cleanup(func() {
		g.open()
		restore()
	})
	return g
}

func (g *gatedBlobs) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.release:
	default:
		close(g.release)
	}
}

func (g *gatedBlobs) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// 点文件先出文字：HEAD 版本还没读回来时缓冲区已经放上（没有对比基准、没有改动块），读回来后改动块出现。
func TestOpenFileTextBeforeBase(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.go", "package a\n\nfunc A() {}\n")
	r.Commit("一")
	r.Write("a.go", "package a\n\nfunc A() { println() }\n")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Repos[0]
	a.RefreshRepo(repo, WhyManual)
	wait("仓库读完", idle(repo))
	g := gateBlobs(t)

	d := a.OpenFile(repo, "a.go", true)
	wait("文字", func() bool { return !d.Loading && d.Buf != nil })
	wait("开始读基准", func() bool { return g.count() == 1 })
	if !d.BasePending || d.Base != nil || d.St.DiffBase != nil || DocHunks(d) != nil {
		t.Fatalf("基准还没回来：pending=%v base=%v", d.BasePending, d.Base != nil)
	}
	if d.Buf.String() != "package a\n\nfunc A() { println() }\n" {
		t.Fatalf("文字 %q", d.Buf.String())
	}
	g.open()
	wait("基准", func() bool { return !d.BasePending })
	if d.Base == nil || d.St.DiffBase != d.Base || len(DocHunks(d)) != 1 {
		t.Fatalf("基准回来后应有一块改动：base=%v hunks=%d", d.Base != nil, len(DocHunks(d)))
	}
}

// 基准回来时这次读已经过期（又重读了）：旧的那份不放上，等新的那次。
func TestStaleBaseDropped(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.txt", "one\n")
	r.Commit("一")
	r.Write("a.txt", "two\n")
	d := docs.NewPath(filepath.Join(r.Root, "a.txt"))
	d.Root, d.Rel = r.Root, "a.txt"
	old := d.Start()
	l := docs.ReadFileText(d.Abs, d.Root, d.Rel, true, "")
	if !l.BasePending() || l.Base != nil {
		t.Fatal("ReadFileText 不该读基准")
	}
	if !d.ApplyFile(old, l) || !d.BasePending {
		t.Fatal("文字应放上、等着基准")
	}
	b, sum := l.LoadBase()
	if b == nil || b.String() != "one\n" || !sum.OK {
		t.Fatalf("LoadBase：%v", b)
	}
	gen := d.Start() // 重读
	if d.ApplyBase(old, b, sum) || d.Base != nil {
		t.Fatal("过期的基准不该放上")
	}
	l = docs.ReadFileText(d.Abs, d.Root, d.Rel, false, "")
	d.ApplyFile(gen, l)
	if !d.ApplyBase(gen, b, sum) || d.Base == nil || d.BasePending {
		t.Fatal("这次的基准应放上")
	}
	if d.ApplyBase(gen, nil, docs.Sum{}) {
		t.Fatal("放过一次就不再等基准")
	}
}

// 历史版本标签也先出文字，父提交里的那份随后放上。
func TestOpenVersionTextBeforeBase(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.txt", "one\n")
	r.Commit("一")
	r.Write("a.txt", "one\ntwo\n")
	r.Commit("二")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Repos[0]
	a.RefreshRepo(repo, WhyManual)
	wait("仓库读完", idle(repo))
	cs, err := git.Log(context.Background(), r.Root, 0, 1, git.LogQuery{})
	if err != nil || len(cs) == 0 {
		t.Fatalf("log：%v", err)
	}
	files, err := git.CommitFiles(context.Background(), r.Root, cs[0])
	if err != nil || len(files) != 1 {
		t.Fatalf("提交里的文件：%v %v", files, err)
	}
	g := gateBlobs(t)
	d := a.OpenVersion(repo, files[0])
	wait("文字", func() bool { return !d.Loading && d.Buf != nil })
	if !d.BasePending || d.Base != nil {
		t.Fatal("父提交的那份还没回来")
	}
	g.open()
	wait("基准", func() bool { return !d.BasePending })
	if d.Base == nil || d.Base.String() != "one\n" || len(DocHunks(d)) != 1 {
		t.Fatalf("父提交的那份：%v", d.Base)
	}
}

// 读得慢（这里是读一个还没人写的命名管道）的过了 loadingDelay 才置上 SlowLoad（界面这时才画"读取中…"），读完就放上内容。
func TestSlowLoadAfterDelay(t *testing.T) {
	keep := loadingDelay
	loadingDelay = 10 * time.Millisecond
	t.Cleanup(func() { loadingDelay = keep })
	dir := t.TempDir()
	fifo := filepath.Join(dir, "slow.txt")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("建不了命名管道：", err)
	}
	a, wait := newTestApp(t, testutil.Workspace(t, dir))
	start := time.Now()
	d := a.OpenPath(fifo)
	// 主线程最多等 quickLoad 就回来（issue #50：读得快的同步放上，读得慢的照旧在后台读），不卡死
	if took := time.Since(start); took > quickLoad+testutil.Budget(500*time.Millisecond) {
		t.Fatalf("打开读不完的文件在主线程上等了 %v", took)
	}
	if !d.Loading || d.SlowLoad {
		t.Fatalf("刚开：loading=%v slow=%v", d.Loading, d.SlowLoad)
	}
	wait("SlowLoad", func() bool { return d.SlowLoad })
	f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("hi\n")
	f.Close()
	wait("读完", func() bool { return !d.Loading && d.Buf != nil })
	if d.Buf.String() != "hi\n" {
		t.Fatalf("内容 %q", d.Buf.String())
	}
}
