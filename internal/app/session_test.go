package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 关窗口时存下标签（顺序、固定没有、光标、滚动、当前哪个）、当前仓库和展开的仓库 / 目录，
// 同一个工作区再打开时放回去；磁盘上没了的文件跳过，Diff 标签也记（见 TestSessionGitTabs），"最近打开"不被打乱。
func TestSessionRestore(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	two := testutil.NewRepo(t, "two")
	two.Write("docs/guide.md", "# 指南\n")
	two.Commit("加文档")
	ws := testutil.Workspace(t, root, two.Root)
	stateDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(outside, []byte("外面的\n"), 0o644)

	a, wait := newTestAppIn(t, ws, stateDir)
	r1, r2 := a.Repos[0], a.Repos[1]
	at := a.OpenFile(r1, "a.txt", true)
	a.OpenFile(r1, "README.md", false)
	a.OpenFile(r1, "u.txt", true)
	a.OpenDiff(r1, git.Change{Kind: git.Unstaged, Path: "a.txt"}, true)
	a.OpenPath(outside)
	wait("读完", func() bool {
		return !slices.ContainsFunc(a.Docs, func(d *docs.Doc) bool { return d.Loading })
	})
	at.JumpTo(docs.Jump{Line: 30, Col: 4})
	at.Top = 22 // 没画过编辑器：按记着的存
	a.SelectDoc(slices.Index(a.Docs, at))
	a.Selected = 1
	r1.Expanded, r2.Expanded = false, true
	r2.Files.SetOpen("docs", true)
	a.St.Recent = []string{"/x/last.go"}
	a.SaveState()
	a.Close()
	os.Remove(filepath.Join(root, "u.txt"))

	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[ws]
	if s == nil || len(s.Tabs) != 5 || s.Active != 0 || s.Repo != two.Root ||
		!slices.Equal(s.Open, []string{two.Root}) || !slices.Equal(s.Dirs[two.Root], []string{"docs"}) {
		t.Fatalf("存的是 %+v", s)
	}
	if got := s.Tabs[0]; got != (workspace.Tab{Path: at.Abs, Pinned: true, Line: 30, Col: 4, Top: 22}) {
		t.Fatalf("a.txt 存成 %+v", got)
	}

	// 同一个工作区再打开：New 放回展开状态，Start 里的 restoreTabs 放回标签
	b, wait := newTestAppIn(t, ws, stateDir)
	if b.Current().Root != two.Root || b.Repos[0].Expanded || !b.Repos[1].Expanded || !b.Repos[1].Files.Open["docs"] {
		t.Fatalf("展开状态：当前 %s，%v %v %v", b.Current().Name, b.Repos[0].Expanded, b.Repos[1].Expanded, b.Repos[1].Files.Open)
	}
	b.restoreTabs()
	t.Cleanup(b.Close)
	var titles []string
	for _, d := range b.Docs {
		titles = append(titles, d.Title)
	}
	if !slices.Equal(titles, []string{"a.txt", "README.md", "diff: a.txt", "out.txt"}) {
		t.Fatalf("标签 %v（u.txt 没了要跳过）", titles)
	}
	ba := b.Docs[0]
	if b.CurrentDoc() != ba || !ba.Pinned || b.Docs[1].Pinned || !b.Docs[2].Pinned || !b.Docs[3].Pinned || ba.Top != 22 {
		t.Fatalf("当前 %v，固定 %v %v %v %v，Top %d", b.CurrentDoc().Title, ba.Pinned, b.Docs[1].Pinned, b.Docs[2].Pinned, b.Docs[3].Pinned, ba.Top)
	}
	wait("读完", func() bool {
		return !slices.ContainsFunc(b.Docs, func(d *docs.Doc) bool { return d.Loading })
	})
	if p, _ := ba.Cursor(); p != (editor.Pos{Line: 30, Col: 4}) {
		t.Fatalf("光标 %+v", p)
	}
	if !slices.Equal(b.St.Recent, []string{"/x/last.go"}) {
		t.Fatalf("最近打开被打乱了：%v", b.St.Recent)
	}
	if b.CanBack() {
		t.Fatal("重开的标签不该进 ‹ › 的历史")
	}

	// 没有记录的工作区：照旧只展开当前仓库
	c, _ := newTestApp(t, testutil.Workspace(t, root, two.Root))
	if !c.Repos[0].Expanded || c.Repos[1].Expanded || len(c.Docs) != 0 {
		t.Fatal("没有记录时应只展开第一个仓库")
	}
}

// 历史版本和 Diff 标签按 SHA 记进会话（Path 写仓库根，老版本看到是目录就跳过），重开时 Key 一样、分组一样、
// 历史版本的光标回到原处；提交被清理掉了的标签留着、换成一句说明，下次不再记；仓库不在了、不是仓库的跳过；
// 终端输出、变基向导不记。
func TestSessionGitTabs(t *testing.T) {
	r := testutil.NewRepo(t, "one")
	r.Write("a.go", "package a\n\nfunc A() {}\n")
	r.Commit("一")
	c1 := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	r.Write("a.go", "package a\n\n// A 是 A。\nfunc A() {}\n\nfunc B() {}\n")
	r.Commit("二")
	c2 := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	r.Write("b.txt", "b\n")
	r.Commit("三")
	c3 := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	r.Git("switch", "-q", "-c", "tmp")
	r.Write("gone.txt", "会被清理掉\n")
	r.Commit("临时")
	c4 := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	r.Git("switch", "-q", "main")
	r.Write("a.go", "package a\n\nfunc A() { /* 改了 */ }\n")
	plain := t.TempDir() // 不是仓库的文件夹
	ws := testutil.Workspace(t, r.Root, plain)
	stateDir := t.TempDir()

	a, wait := newTestAppIn(t, ws, stateDir)
	repo := a.Repos[0]
	ver := a.OpenVersion(repo, git.Change{Kind: git.InCommit, Path: "a.go", Code: 'M', Commit: c2, Parent: c1})
	inCommit := a.OpenDiff(repo, git.Change{Kind: git.InCommit, Path: "a.go", Code: 'M', Commit: c2, Parent: c1}, true)
	rng := a.OpenDiff(repo, git.Change{Kind: git.InCommit, Path: "b.txt", Code: 'A', Commit: c3, Parent: c1, Range: true}, false)
	unstaged := a.OpenDiff(repo, git.Change{Kind: git.Unstaged, Path: "a.go", Code: 'M'}, true)
	goneVer := a.OpenVersion(repo, git.Change{Kind: git.InCommit, Path: "gone.txt", Code: 'A', Commit: c4, Parent: c3})
	term := docs.NewTermText(r.Root, 1, "终端 · one · 终端 1")
	a.addDoc(term, true)
	wait("读完", func() bool {
		return !slices.ContainsFunc(a.Docs, func(d *docs.Doc) bool { return d.Loading })
	})
	ver.JumpTo(docs.Jump{Line: 3, Col: 5})
	a.SplitDoc(ver, SideRight)
	if !a.Split() || a.GroupOf(ver) == a.GroupOf(inCommit) {
		t.Fatal("历史版本没分到右边")
	}
	keys := map[docs.Key]bool{}
	for _, d := range []*docs.Doc{ver, inCommit, rng, unstaged, goneVer} {
		keys[d.Key()] = true
	}
	a.SaveState()
	a.Close()

	st, err := workspace.LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[ws]
	if s == nil || len(s.Tabs) != 5 || s.Layout == nil {
		t.Fatalf("存的是 %+v", s)
	}
	for _, tab := range s.Tabs {
		// 老版本：Path 是绝对路径（不删、下标不错位），os.Stat 是目录（跳过）
		if fi, err := os.Stat(tab.Path); tab.Kind == "" || !filepath.IsAbs(tab.Path) || err != nil || !fi.IsDir() || tab.Repo != r.Root {
			t.Fatalf("%+v：老版本读不过去", tab)
		}
	}
	vt := s.Tabs[slices.IndexFunc(s.Tabs, func(tab workspace.Tab) bool { return tab.Kind == workspace.TabVersion && tab.Change.Commit == c2 })]
	if *vt.Change != (workspace.TabChange{Kind: "commit", Path: "a.go", Code: "M", Commit: c2, Parent: c1}) || vt.Line != 3 || vt.Col != 5 || !vt.Pinned {
		t.Fatalf("历史版本存成 %+v %+v", vt, vt.Change)
	}
	dt := s.Tabs[slices.IndexFunc(s.Tabs, func(tab workspace.Tab) bool { return tab.Kind == workspace.TabDiff && tab.Change.Range })]
	if *dt.Change != (workspace.TabChange{Kind: "commit", Path: "b.txt", Code: "A", Commit: c3, Parent: c1, Range: true}) || dt.Pinned || dt.Line != 0 || dt.Top != 0 {
		t.Fatalf("比较两个提交的 Diff 存成 %+v %+v", dt, dt.Change)
	}
	if testing.Verbose() {
		for _, tab := range []workspace.Tab{vt, s.Tabs[slices.IndexFunc(s.Tabs, func(tab workspace.Tab) bool {
			return tab.Kind == workspace.TabDiff && tab.Change.Kind == "commit" && !tab.Change.Range
		})]} {
			j, _ := json.MarshalIndent(tab, "", "  ")
			t.Logf("%s", j)
		}
	}

	// 提交 c4 被清理掉、加一个不在的仓库和一个不是仓库的文件夹的标签，再打开
	r.Git("branch", "-q", "-D", "tmp")
	r.Git("reflog", "expire", "--expire=now", "--all")
	r.Git("gc", "-q", "--prune=now")
	s.Tabs = append(s.Tabs,
		workspace.Tab{Path: "/nowhere", Kind: workspace.TabVersion, Repo: "/nowhere", Change: &workspace.TabChange{Kind: "commit", Path: "a.go", Commit: c2}},
		workspace.Tab{Path: plain, Kind: workspace.TabDiff, Repo: plain, Change: &workspace.TabChange{Kind: "unstaged", Path: "x"}},
		workspace.Tab{Path: r.Root, Kind: workspace.TabDiff, Repo: r.Root, Change: &workspace.TabChange{Kind: "什么", Path: "a.go"}})
	if err := workspace.SaveState(stateDir, st); err != nil {
		t.Fatal(err)
	}
	b, wait := newTestAppIn(t, ws, stateDir)
	b.restoreTabs()
	t.Cleanup(b.Close)
	if len(b.AllDocs()) != 5 || !b.Split() {
		t.Fatalf("重开了 %d 个，分组 %v", len(b.AllDocs()), b.Split())
	}
	wait("读完", func() bool {
		return !slices.ContainsFunc(b.AllDocs(), func(d *docs.Doc) bool { return d.Loading })
	})
	var bver, bgone, bcommit *docs.Doc
	for _, d := range b.AllDocs() {
		if !keys[d.Key()] {
			t.Fatalf("多出来 %v", d.Key())
		}
		switch d.Key() {
		case ver.Key():
			bver = d
		case goneVer.Key():
			bgone = d
		case inCommit.Key():
			bcommit = d
		}
	}
	if p, _ := bver.Cursor(); p != (editor.Pos{Line: 3, Col: 5}) || b.GroupOf(bver) == b.GroupOf(bcommit) || bver.Info != "" {
		t.Fatalf("历史版本光标 %+v，和提交里的 Diff 同组 %v，%q", p, b.GroupOf(bver) == b.GroupOf(bcommit), bver.Info)
	}
	if bcommit.DiffSt == nil || bcommit.Gone {
		t.Fatalf("提交里的 Diff 没读出来：%q", bcommit.Info)
	}
	if !bgone.Gone || bgone.Info != docs.CommitGoneNote(c4) || !strings.Contains(bgone.Info, git.Short(c4)) {
		t.Fatalf("提交没了：Gone %v，%q", bgone.Gone, bgone.Info)
	}
	if _, ok := sessionTab(bgone); ok {
		t.Fatal("提交没了的标签下次不该再记")
	}
	if _, ok := sessionTab(term); ok {
		t.Fatal("终端输出的标签不该记")
	}

	// ⇧⌘T 重开提交没了的标签也是那句；提交里的 Diff 也一样
	b.CloseDoc(bgone)
	b.ReopenClosed()
	again := b.CurrentDoc()
	wait("重开读完", func() bool { return !again.Loading })
	if again.Key() != goneVer.Key() || again.Info != docs.CommitGoneNote(c4) {
		t.Fatalf("⇧⌘T 重开：%v %q", again.Key(), again.Info)
	}
	gd := b.OpenDiff(b.Repos[0], git.Change{Kind: git.InCommit, Path: "gone.txt", Code: 'A', Commit: c4, Parent: c3}, true)
	wait("Diff 读完", func() bool { return !gd.Loading })
	if !gd.Gone || gd.Info != docs.CommitGoneNote(c4) || gd.DiffSt != nil {
		t.Fatalf("提交没了的 Diff：Gone %v，%q", gd.Gone, gd.Info)
	}
	if got := b.session(); len(got.Tabs) != 4 {
		t.Fatalf("下次记 %d 个（提交没了的两个不记）", len(got.Tabs))
	}
}
