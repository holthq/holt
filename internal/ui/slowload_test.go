package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// 预览标签换成一个读得慢的文件（这里是还没人写的命名管道）："读取中…"只在 SlowLoad（读了约 150 ms 还没读完）时画，
// 在那之前编辑区只露底；大纲也一样。读完放上内容。
func TestLoadingOnlyWhenSlow(t *testing.T) {
	r := testutil.NewRepo(t, "demo")
	r.Write("a.txt", "hello\n")
	r.Commit("一")
	fifo := filepath.Join(r.Root, "slow.txt")
	if err := testutil.MakeFIFO(fifo, 0o644); err != nil {
		t.Skip("建不了命名管道：", err)
	}
	a, tt, wait := newUI(t, testutil.Workspace(t, r.Root), "")
	repo := a.Repos[0]
	a.RefreshRepo(repo, app.WhyManual)
	wait("git status", idle(repo))
	first := a.OpenFile(repo, "a.txt", false)
	wait("读文件", func() bool { return !first.Loading && first.Buf != nil })
	tt.Frame()

	d := a.OpenFile(repo, "slow.txt", false) // 换掉预览标签
	if a.CurrentDoc() != d || !d.Loading {
		t.Fatalf("应换成正在读的预览标签：loading=%v", d.Loading)
	}
	label := i18n.T("读取中…")
	tt.Frame()
	if tt.HasText(label) != d.SlowLoad { // 机器慢时 150 ms 可能已经过了：画不画只跟着 SlowLoad
		t.Fatalf("SlowLoad=%v 时画了读取中=%v", d.SlowLoad, tt.HasText(label))
	}
	d.SlowLoad = true
	tt.Frame()
	if !tt.HasText(label) {
		t.Fatal("读得慢了应画读取中…")
	}

	f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("slow text\n")
	f.Close()
	wait("读完", func() bool { return !d.Loading && d.Buf != nil })
	tt.Frame()
	if tt.HasText(label) {
		t.Fatal("读完了不该还画读取中…")
	}
}
