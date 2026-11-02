package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 分屏记进会话：左右分、右边再上下分，各格自己的目录、比例照记；重开后树一样（不启动 shell），有键盘的是第一格；
// Sids 照写（这里没开 termd，是空的）。
func TestTermSplitRoundTrip(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	for _, d := range []string{"docs", "src"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := t.TempDir()
	a, wait := newTestAppIn(t, ws, state)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读完状态", idle(r))
	a.SplitTerm(r, true)
	_, tab, right := a.ActivePane()
	right.Dir = filepath.Join(root, "docs")
	a.SplitTerm(r, false)
	_, _, low := a.ActivePane()
	low.Dir = filepath.Join(root, "src")
	tab.Root.Ratio = 0.3
	tab.Root.B.Ratio = 0.6
	a.SaveState()
	a.Close()

	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[a.sessionKey()]
	if s == nil || s.Terms[r.Root] == nil || len(s.Terms[r.Root].Tabs) != 1 || s.Terms[r.Root].Tabs[0].Split == nil {
		t.Fatalf("会话里没记分屏：%+v", s)
	}
	if n := len(s.Terms[r.Root].Tabs[0].Split.Leaves()); n != 3 {
		t.Fatalf("记了 %d 格", n)
	}

	b, wait2 := newTestAppIn(t, ws, state)
	r2 := b.Repos[0]
	b.RefreshRepo(r2, WhyManual)
	wait2("读完状态", idle(r2))
	tabs := b.TermTabs(r2)
	if len(tabs) != 1 {
		t.Fatalf("重开后 %d 个标签", len(tabs))
	}
	n := tabs[0].Root
	if n.Pane != nil || !n.Row || n.Ratio != 0.3 || n.A.Pane == nil || n.B.Pane != nil || n.B.Row || n.B.Ratio != 0.6 {
		t.Fatalf("重开后的树不对：%+v", n)
	}
	if n.A.Pane.Dir != "" || n.B.A.Pane.Dir != filepath.Join(root, "docs") || n.B.B.Pane.Dir != filepath.Join(root, "src") {
		t.Fatalf("各格的目录：%q %q %q", n.A.Pane.Dir, n.B.A.Pane.Dir, n.B.B.Pane.Dir)
	}
	if tabs[0].Current() != n.A.Pane || b.StartedPanes() != 0 {
		t.Fatalf("有键盘的应是第一格、一个都不启动：起了 %d 个", b.StartedPanes())
	}
}

// termhist 的清理：这个工作区里格上没记的、别的工作区会话里没记的、没有会话的工作区的目录都删；
// 记着的留着；整个目录超过上限先删最老的。
func TestTermHistGC(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	state := t.TempDir()
	other := filepath.Join(t.TempDir(), "other.code-workspace")
	otherRoot := t.TempDir()
	st := workspace.Default()
	st.Sessions = map[string]*workspace.Session{other: {Terms: map[string]*workspace.Terms{
		otherRoot: {Tabs: []workspace.TermTab{{Split: &workspace.TermSplit{Hist: "1-1.vt"}}}},
	}}}
	if err := workspace.SaveState(state, st); err != nil {
		t.Fatal(err)
	}
	a, wait := newTestAppIn(t, ws, state)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读完状态", idle(r))
	hroot := filepath.Join(state, "termhist")
	mine, theirs, gone := a.termHistDir(), filepath.Join(hroot, termHistKey(other)), filepath.Join(hroot, termHistKey("/没有了.code-workspace"))
	write := func(dir, name string, size int, age time.Duration) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		os.Chtimes(p, at, at)
		return p
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	_, _, p := a.ActivePane()
	p.hist = "5-1.vt"
	kept := write(mine, "5-1.vt", 10, time.Hour)
	stale := write(mine, "4-1.vt", 10, time.Hour)
	theirsKept := write(theirs, "1-1.vt", 10, time.Hour)
	theirsStale := write(theirs, "2-2.vt", 10, time.Hour)
	goneFile := write(gone, "3-3.vt", 10, time.Hour)
	a.gcTermHist()
	if !exists(kept) || exists(stale) || !exists(theirsKept) || exists(theirsStale) || exists(goneFile) || exists(gone) {
		t.Fatalf("清理之后：本工作区记着的 %v、没记的 %v；别的工作区记着的 %v、没记的 %v；没有会话的 %v（目录 %v）",
			exists(kept), exists(stale), exists(theirsKept), exists(theirsStale), exists(goneFile), exists(gone))
	}

	// 上限：最老的那个（别的工作区的，一小时前）先删
	old := TermHistMax
	TermHistMax = 15
	t.Cleanup(func() { TermHistMax = old })
	write(mine, "5-1.vt", 10, time.Minute)
	a.gcTermHist()
	if !exists(kept) || exists(theirsKept) {
		t.Fatalf("超过上限应先删最老的：新的 %v、老的 %v", exists(kept), exists(theirsKept))
	}
}

// 截回滚：留最后 limit 字节以内、从一行的开头起；一行都放不下为空。
func TestTrimHist(t *testing.T) {
	h := []byte("one\r\ntwo\r\nthree\r\n")
	if got := string(trimHist(h, 100)); got != string(h) {
		t.Fatalf("没超的原样：%q", got)
	}
	if got := string(trimHist(h, 9)); got != "three\r\n" {
		t.Fatalf("截了：%q", got)
	}
	if got := trimHist([]byte("a-very-long-line"), 4); got != nil {
		t.Fatalf("一行都放不下：%q", got)
	}
}
