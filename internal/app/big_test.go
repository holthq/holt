package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
)

// bigGo 是至少 n 字节、像 Go 代码的文字。
func bigGo(n int) string {
	var b strings.Builder
	b.WriteString("package big\n\n")
	for b.Len() < n {
		b.WriteString("// 一行注释，凑够大小\nfunc f() int { return 1 }\n")
	}
	return b.String()
}

// 仓库里 9 MB 的文件：只读打开、Big，没有 HEAD 基准，不能 blame、不能保存；仓库状态重读后（syncFile）也不读 HEAD 版本。
func TestOpenBigFile(t *testing.T) {
	r := testutil.NewRepo(t, "big")
	text := bigGo(docs.MaxEditBytes + 1<<20)
	r.Write("big.go", text)
	r.Commit("大文件")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Repos[0]
	a.RefreshRepo(repo, WhyManual)
	wait("仓库读完", idle(repo))
	d := a.OpenFile(repo, "big.go", true)
	wait("读大文件", func() bool { return !d.Loading })
	if d.Info != "" || !d.Big || !d.St.ReadOnly || d.Base != nil || d.St.DiffBase != nil || d.Buf.Len() != len(text) {
		t.Fatalf("大文件：info=%q big=%v readOnly=%v base=%v", d.Info, d.Big, d.St.ReadOnly, d.Base != nil)
	}
	if a.CanBlame(d) || d.Editable() {
		t.Error("大文件不该能 blame、保存")
	}
	// HEAD 变了（再提交一次别的改动），syncFile 也不给大文件读 HEAD 版本
	r.Write("other.txt", "x\n")
	r.Commit("别的")
	a.RefreshRepo(repo, WhyManual)
	wait("仓库重读", idle(repo))
	time.Sleep(100 * time.Millisecond)
	wait("syncFile 回来", func() bool { return true })
	if d.Base != nil || d.BaseSum.OK || d.Loading {
		t.Error("大文件不该有 HEAD 基准")
	}
}

// 一直在变的大文件：两次重读之间至少隔 bigReloadGap，期间的变化合并成到点的一次，读的是最新的。
func TestBigReloadThrottle(t *testing.T) {
	keep := bigReloadGap
	bigReloadGap = 600 * time.Millisecond
	t.Cleanup(func() { bigReloadGap = keep })
	r := testutil.NewRepo(t, "big")
	text := bigGo(docs.MaxEditBytes + 1<<20)
	r.Write("big.log", text)
	r.Commit("日志")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Repos[0]
	a.RefreshRepo(repo, WhyManual)
	wait("仓库读完", idle(repo))
	d := a.OpenFile(repo, "big.log", true)
	wait("读大文件", func() bool { return !d.Loading && d.Buf != nil })
	gen, start := d.Gen, d.ReloadAt

	abs := filepath.Join(r.Root, "big.log")
	appendLine := func(s string) {
		f, err := os.OpenFile(abs, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	appendLine("第一次追加\n")
	a.syncDocs(repo)
	wait("排上到点的重读", func() bool { return d.ReloadQueued })
	if d.Gen != gen {
		t.Fatal("刚读过的大文件不该马上重读")
	}
	appendLine("第二次追加\n")
	a.syncDocs(repo)
	// 重读时缓冲区还在，Loading 不置上：等内容换上
	wait("重读", func() bool { return strings.HasSuffix(d.Buf.String(), "第二次追加\n") })
	if d.Gen != gen+1 || time.Since(start) < bigReloadGap || d.ReloadQueued {
		t.Errorf("应只重读一次、隔够 %v：gen %d → %d，隔了 %v", bigReloadGap, gen, d.Gen, time.Since(start))
	}
	if !strings.HasSuffix(d.Buf.String(), "第一次追加\n第二次追加\n") {
		t.Error("到点的重读应读最新的")
	}
	// 隔够了再变：马上重读
	time.Sleep(bigReloadGap)
	appendLine("第三次追加\n")
	gen = d.Gen
	a.syncDocs(repo)
	wait("马上重读", func() bool { return d.Gen != gen })
	if d.ReloadQueued {
		t.Error("隔够了应马上重读")
	}
	wait("读完", func() bool { return strings.HasSuffix(d.Buf.String(), "第三次追加\n") })
}

// HEAD 变了、文件没变（syncFile 后台比摘要）：超过 2 MB 的不建对比基准，只换摘要、还是 NoBase；2 MB 以内的照旧换上新的基准。
func TestNoBaseSync(t *testing.T) {
	r := testutil.NewRepo(t, "nobase")
	big, small := bigGo(docs.MaxDiffBaseBytes+1<<10), "package small\n"
	r.Write("big.go", big)
	r.Write("small.go", small)
	r.Commit("两个")
	r.Write("big.go", big+"// 工作区\n")
	r.Write("small.go", small+"// 工作区\n")
	a, wait := newTestApp(t, testutil.Workspace(t, r.Root))
	repo := a.Repos[0]
	a.RefreshRepo(repo, WhyManual)
	wait("仓库读完", idle(repo))
	b := a.OpenFile(repo, "big.go", true)
	s := a.OpenFile(repo, "small.go", true)
	wait("读好", func() bool {
		return !b.Loading && !s.Loading && !b.BasePending && !s.BasePending && b.Buf != nil && s.Buf != nil
	})
	if !b.NoBase || b.St.DiffBase != nil || s.NoBase || s.St.DiffBase == nil {
		t.Fatalf("打开：big noBase=%v，small noBase=%v", b.NoBase, s.NoBase)
	}
	// 只改 HEAD：hash-object + update-index，工作区的文件不动
	commitBlob := func(name, content string) {
		p := filepath.Join(t.TempDir(), "blob")
		os.WriteFile(p, []byte(content), 0o644)
		sha := strings.TrimSpace(r.Git("hash-object", "-w", p))
		r.Git("update-index", "--cacheinfo", "100644,"+sha+","+name)
	}
	commitBlob("big.go", big+"// HEAD\n")
	commitBlob("small.go", small+"// HEAD\n")
	r.Git("commit", "-q", "-m", "只改 HEAD")
	bsum, ssum := b.BaseSum, s.BaseSum
	a.RefreshRepo(repo, WhyManual)
	wait("仓库重读", idle(repo))
	wait("摘要换了", func() bool { return b.BaseSum != bsum && s.BaseSum != ssum })
	if b.Base != nil || !b.NoBase || b.BaseSum != docs.SumOf([]byte(big+"// HEAD\n")) {
		t.Errorf("超过 2 MB：base=%v noBase=%v", b.Base != nil, b.NoBase)
	}
	if s.Base == nil || s.Base.String() != small+"// HEAD\n" || s.NoBase {
		t.Error("2 MB 以内应换上新的 HEAD 版本")
	}
}
