package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/history"
	"github.com/lemoeade/grove/internal/testutil"
)

// 本地历史：这里的状态目录都是 t.TempDir()，history 子目录跟着测试删掉。

// localClock 把这个窗口的本地历史换成一只手拨的钟（从 at 开始），返回拨钟的函数。
func localClock(a *App, at time.Time) func(d time.Duration) {
	now := at
	a.LocalHist().Now = func() time.Time { return now }
	return func(d time.Duration) { now = now.Add(d) }
}

// localWhys 是 abs 现在的条目（旧的在前）的 why 和内容。
func localWhys(t *testing.T, a *App, abs string) (whys, texts []string) {
	t.Helper()
	s := a.LocalHist()
	for _, e := range s.Load(abs).Entries {
		data, err := s.Content(abs, e)
		if err != nil {
			t.Fatalf("读 %s 的 %d：%v", abs, e.T, err)
		}
		whys, texts = append(whys, e.Why), append(texts, string(data))
	}
	return whys, texts
}

// 保存（⌘S、退出时的全部保存、自动保存、格式化后保存）都经 diskWrite 记：第一次先记保存前磁盘上的那份，
// 10 s 内连着存的合成一条，内容没变的不记。
func TestLocalHistorySave(t *testing.T) {
	r0 := testutil.NewRepo(t, "lh")
	r0.Write("n.txt", "原来的\n")
	r0.Write("a.go", "package a\n")
	r0.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	tick := localClock(a, time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local))
	abs := filepath.Join(r0.Root, "n.txt")
	d := a.OpenFile(r, "n.txt", true)
	wait("读文件", func() bool { return !d.Loading })
	check := func(what string, whys, texts []string) {
		t.Helper()
		gw, gt := localWhys(t, a, abs)
		if !slices.Equal(gw, whys) || !slices.Equal(gt, texts) {
			t.Fatalf("%s：%q %q，要 %q %q", what, gw, gt, whys, texts)
		}
	}

	d.Buf.Set("一\n")
	if err := a.WriteDoc(d, true); err != nil {
		t.Fatal(err)
	}
	check("第一次保存", []string{"disk", "save"}, []string{"原来的\n", "一\n"})
	tick(3 * time.Second)
	d.Buf.Set("二\n")
	a.WriteDoc(d, true)
	check("10 s 内再存", []string{"disk", "save"}, []string{"原来的\n", "二\n"})
	tick(time.Minute)
	a.WriteDoc(d, true)
	check("没改又存", []string{"disk", "save"}, []string{"原来的\n", "二\n"})

	// 别的程序改了磁盘：再存前先记它那份
	os.WriteFile(abs, []byte("外面改的\n"), 0o644)
	a.Reload(d)
	wait("重读", func() bool { return !d.Loading && d.Buf.String() == "外面改的\n" })
	d.Buf.Set("三\n")
	a.SaveAll()
	check("全部保存", []string{"disk", "save", "disk", "save"}, []string{"原来的\n", "二\n", "外面改的\n", "三\n"})

	tick(time.Minute)
	a.St.Settings.AutoSave = "delay"
	d.Buf.Set("四\n")
	a.AutoSaveTick()
	a.autoVer[d] = autoSave{d.Buf.Version(), time.Now().Add(-AutoSaveDelay)}
	a.AutoSaveTick()
	if d.Modified() {
		t.Fatal("自动保存没存")
	}
	check("自动保存", []string{"disk", "save", "disk", "save", "save"}, []string{"原来的\n", "二\n", "外面改的\n", "三\n", "四\n"})
	a.St.Settings.AutoSave = ""

	// 格式化后保存：记的是格式化过的
	s := &a.St.Settings
	s.FormatOnSave, s.Formatters = true, map[string]string{"Go": "tr a-z A-Z"}
	g := a.OpenFile(r, "a.go", true)
	wait("读 a.go", func() bool { return !g.Loading })
	g.Buf.Set("package a\nvar x\n")
	a.Save(g)
	wait("格式化后保存", func() bool { return !g.Formatting && !g.Modified() })
	if gw, gt := localWhys(t, a, filepath.Join(r0.Root, "a.go")); !slices.Equal(gw, []string{"disk", "save"}) || gt[1] != "PACKAGE A\nVAR X\n" {
		t.Fatalf("格式化后保存：%q %q", gw, gt)
	}
	wait("空闲", idle(r))
}

// 文件历史里本地行和提交按时间排（新的在前），没读完的提交后面先不列；仓库外、未跟踪的文件是路径模式；
// 应用里改名，本地历史跟着挪。
func TestLocalHistoryRows(t *testing.T) {
	r0 := testutil.NewRepo(t, "rows")
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	r0.Write("f.txt", "一\n")
	r0.CommitAt("c1", base)
	r0.Write("f.txt", "一\n二\n")
	r0.CommitAt("c2", base.Add(2*time.Hour))
	r0.Write("u.txt", "未跟踪\n")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	s := a.LocalHist()
	abs := filepath.Join(r0.Root, "f.txt")
	for i, h := range []int{-1, 1, 3} { // 比 c1 早、c1 和 c2 之间、比 c2 晚
		at := base.Add(time.Duration(h) * time.Hour)
		s.Now = func() time.Time { return at }
		if _, err := s.Record(abs, []byte(strings.Repeat("x", i+1)), history.WhySave); err != nil {
			t.Fatal(err)
		}
	}
	s.Now = nil

	a.ShowFileHistory(r, "f.txt", false)
	wait("读文件历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading })
	want := []HistRow{{-1, -1}, {LocalRow, 2}, {0, -1}, {LocalRow, 1}, {1, -1}, {LocalRow, 0}}
	if got := HistRows(r); !slices.Equal(got, want) || r.Hist.localFor != abs {
		t.Fatalf("混排 %v，要 %v（localFor %q）", got, want, r.Hist.localFor)
	}
	if !LocalPass(r, want, 3) || LocalPass(r, want, 1) || LocalPass(r, want, 5) {
		t.Error("竖线只该穿过两个提交中间的那条")
	}
	r.Hist.Done = false // 提交没读完：比最后一个读到的还早的先不列
	if got := HistRows(r); len(got) != 5 {
		t.Errorf("没读完时 %v", got)
	}
	r.Hist.Done = true

	// 选中本地行：开只读比较（预览），快照是那一条
	setHistSel(r, LocalRow, 1)
	a.OnHistSelected(r)
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.Compare || d.Snapshot != r.Hist.Local[1].T || d.Right != abs || d.Pinned {
		t.Fatalf("比较标签 %+v", d)
	}
	wait("读比较", func() bool { return !d.Loading })
	if d.DiffSt == nil || d.Info != "" {
		t.Errorf("比较没读出来：%q", d.Info)
	}

	// 保存这个文件：右栏跟着多一行，选中的那条还选着
	sel := r.Hist.Local[1].T
	f := a.OpenFile(r, "f.txt", true)
	wait("读文件", func() bool { return !f.Loading })
	f.Buf.Set("改了\n")
	a.WriteDoc(f, true)
	if ci, i := histKey(r); len(r.Hist.Local) != 5 || ci != LocalRow || r.Hist.Local[i].T != sel {
		t.Fatalf("保存后 %d 条，选中 %d/%d", len(r.Hist.Local), ci, i)
	}

	// 跟踪的文件走文件历史；未跟踪的、仓库外的是路径模式
	a.ShowLocalHistory(abs)
	if a.LocalHistPath != "" || r.Hist.Path != "f.txt" {
		t.Errorf("跟踪的文件：%q %q", a.LocalHistPath, r.Hist.Path)
	}
	a.RefreshRepo(r, WhyManual)
	wait("读状态", func() bool { return idle(r)() && ChangedIn(r, "u.txt") == '?' })
	u := filepath.Join(r0.Root, "u.txt")
	s.Record(u, []byte("未跟踪\n"), history.WhyDisk)
	a.ShowLocalHistory(u)
	if a.LocalHistPath != u || len(a.LocalRows) != 1 || !a.St.RightBar {
		t.Errorf("未跟踪的：%q %d", a.LocalHistPath, len(a.LocalRows))
	}
	out := filepath.Join(t.TempDir(), "out.txt")
	a.ShowLocalHistory(out)
	if a.LocalHistPath != out || len(a.LocalRows) != 0 {
		t.Errorf("仓库外的：%q %d", a.LocalHistPath, len(a.LocalRows))
	}

	// 改名跟着挪（路径模式开着的也换过去）
	a.ShowLocalHistory(u)
	if err := a.rename(r, "u.txt", "v.txt"); err != nil {
		t.Fatal(err)
	}
	v := filepath.Join(r0.Root, "v.txt")
	es := s.Load(v).Entries
	if len(s.Load(u).Entries) != 0 || len(es) != 2 || es[1].Why != history.WhyRename || es[1].From != u || a.LocalHistPath != v {
		t.Fatalf("改名后 %+v，路径模式 %q", es, a.LocalHistPath)
	}
	if LocalTitle(es[1]) != "改名自 u.txt" {
		t.Errorf("改名条的标题 %q", LocalTitle(es[1]))
	}
	a.ShowFileHistory(r, "f.txt", false)
	if a.LocalHistPath != "" {
		t.Error("看文件历史时路径模式没退出")
	}
	wait("空闲", func() bool { return idle(r)() && !r.Hist.Loading })
}

// 恢复：先记磁盘上现在的那份（恢复前），写回去，开着的标签重读；有没保存的改动先问；比较标签上的按钮也是它。
func TestLocalHistoryRestore(t *testing.T) {
	r0 := testutil.NewRepo(t, "rs")
	r0.Write("r.txt", "v1\n")
	r0.Commit("初始")
	os.Chmod(filepath.Join(r0.Root, "r.txt"), 0o600)
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	at := time.Date(2026, 11, 1, 14, 32, 0, 0, time.Local)
	tick := localClock(a, at)
	stamp := LocalStamp(at) // 当天是 14:32，过了那天是 11-01 14:32：期望跟着它拼，不按跑测试的日子挂
	abs := filepath.Join(r0.Root, "r.txt")
	d := a.OpenFile(r, "r.txt", true)
	wait("读文件", func() bool { return !d.Loading })
	d.Buf.Set("v2\n")
	a.WriteDoc(d, true)
	tick(time.Minute)
	os.WriteFile(abs, []byte("v3\n"), 0o600) // 别的程序改的
	es := a.LocalHist().Load(abs).Entries

	a.RestoreLocal(abs, es[0])
	if got := mustRead(t, abs); got != "v1\n" {
		t.Fatalf("恢复后磁盘上 %q", got)
	}
	if fi, _ := os.Stat(abs); fi.Mode().Perm() != 0o600 {
		t.Errorf("权限变成了 %v", fi.Mode().Perm())
	}
	if a.Toast != "已恢复到 "+stamp+" 的那一份（恢复前的留在本地历史里）" {
		t.Errorf("toast %q", a.Toast)
	}
	wait("标签重读", func() bool { return !d.Loading && d.Buf.String() == "v1\n" })
	whys, texts := localWhys(t, a, abs)
	if !slices.Equal(whys, []string{"disk", "save", "restore"}) || texts[2] != "v3\n" {
		t.Fatalf("恢复前那条：%q %q", whys, texts)
	}

	// 有没保存的改动：先问，点了才恢复（磁盘上那份照样记）
	d.Buf.Set("没存的\n")
	a.RestoreLocal(abs, es[1])
	if !a.ConfirmOpen || a.Confirm.Button != "恢复" || !strings.Contains(a.Confirm.Title, "r.txt 有没保存的改动") {
		t.Fatalf("没先问：%+v", a.Confirm)
	}
	if mustRead(t, abs) != "v1\n" {
		t.Fatal("还没确认就恢复了")
	}
	a.ConfirmOpen = false
	a.Confirm.Fn()
	wait("标签重读", func() bool { return !d.Loading && d.Buf.String() == "v2\n" && !d.Modified() })

	// 比较标签：恢复到这一份；这一条删掉了的提示
	tick(time.Minute)
	c := a.OpenLocalCompare(abs, es[0], true)
	if c.Title != "r.txt · "+stamp+" 的本地历史 ↔ r.txt" || c.Snapshot != es[0].T {
		t.Errorf("比较标签 %q %d", c.Title, c.Snapshot)
	}
	a.RestoreSnapshot(c)
	if mustRead(t, abs) != "v1\n" {
		t.Fatal("比较标签上恢复没写回")
	}
	ro := a.OpenLocalFile(abs, es[0])
	wait("读那一份", func() bool { return !ro.Loading })
	if ro.Editable() || ro.Buf.String() != "v1\n" || ro.Title != "r.txt · "+stamp || ro.Lang != d.Lang {
		t.Errorf("编辑器里打开那一份：可改 %v %q %q %q", ro.Editable(), ro.Buf.String(), ro.Title, ro.Lang)
	}
	a.DeleteLocal(abs, es[0])
	if slices.Contains(a.AllDocs(), c) || slices.Contains(a.AllDocs(), ro) {
		t.Error("删掉的那条的标签还开着")
	}
	c.Snapshot = es[0].T
	a.RestoreSnapshot(c)
	if a.Toast != "这一条本地历史已经没有了" {
		t.Errorf("删掉的那条：%q", a.Toast)
	}
	wait("空闲", idle(r))
}

// 删掉的文件：>restore 列出来，选了写回原处并打开；原处又有了的先问。
func TestRestoreDeleted(t *testing.T) {
	r0 := testutil.NewRepo(t, "del")
	r0.Write("keep.txt", "留着\n")
	r0.Write("sub/gone.txt", "旧的\n")
	r0.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	at := time.Date(2026, 11, 1, 9, 5, 0, 0, time.Local)
	localClock(a, at)
	gone := filepath.Join(r0.Root, "sub", "gone.txt")
	d := a.OpenFile(r, "sub/gone.txt", true)
	wait("读文件", func() bool { return !d.Loading })
	d.Buf.Set("新的\n")
	a.WriteDoc(d, true)
	a.CloseDoc(d)
	k := a.OpenFile(r, "keep.txt", true)
	wait("读文件", func() bool { return !k.Loading })
	k.Buf.Set("还在\n")
	a.WriteDoc(k, true)

	if items, hint := a.deletedItems(""); len(items) != 0 || hint != "本地历史里没有删掉的文件" {
		t.Fatalf("没删的时候 %v %q", itemTexts(items), hint)
	}
	os.RemoveAll(filepath.Join(r0.Root, "sub"))
	items, _ := a.deletedItems("gone")
	if len(items) != 1 || items[0].Kind != ItemDeleted || items[0].path != gone || !strings.HasPrefix(items[0].Detail, "2 条 · ") {
		t.Fatalf("删掉的 %+v", items)
	}
	if items, _ := a.deletedItems("keep"); len(items) != 0 {
		t.Errorf("还在的也列了：%v", itemTexts(items))
	}

	a.OpenPalette(">restore gone")
	a.UpdatePalette()
	if len(a.Palette.Items) != 1 {
		t.Fatalf("面板里 %v", itemTexts(a.Palette.Items))
	}
	a.AcceptPalette(a.Palette.Sel)
	if got := mustRead(t, gone); got != "新的\n" {
		t.Fatalf("恢复后 %q", got)
	}
	if c := a.CurrentDoc(); c == nil || c.Abs != gone || a.Toast != "已恢复 gone.txt（"+LocalStamp(at)+" 的那一份）" {
		t.Fatalf("没打开 / toast %q", a.Toast)
	}

	// 原处又有了：先问
	os.WriteFile(gone, []byte("又写了\n"), 0o644)
	a.RestoreDeleted(gone)
	if !a.ConfirmOpen || a.Confirm.Button != "覆盖" || mustRead(t, gone) != "又写了\n" {
		t.Fatalf("没先问：%+v", a.Confirm)
	}
	a.ConfirmOpen = false
	wait("空闲", idle(r))
}

// 不记的：关了的、状态目录里的、.git 下的、图片、files.exclude 藏掉的、太大的；没状态目录时照常保存。
func TestLocalHistoryExclude(t *testing.T) {
	r0 := testutil.NewRepo(t, "ex")
	r0.Write(".vscode/settings.json", "{\"files.exclude\": {\"*.log\": true}}\n")
	r0.Write("a.txt", "a\n")
	r0.Write("big.txt", strings.Repeat("x", history.MaxSnapshot+1))
	r0.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, r0.Root))
	r := a.Current()
	a.EnsureFiles(r)
	wait("读文件树", func() bool { return r.Files.Loaded && !r.Files.Loading })
	at := func(p string) string { return filepath.Join(r0.Root, p) }
	for p, want := range map[string]bool{
		at("a.txt"): true, at("x.log"): false, at("sub/y.log"): true, at(".git/config"): false, at("p.png"): false,
		filepath.Join(a.StateDir, "state.json"): false, filepath.Join(t.TempDir(), "外面.txt"): true,
	} {
		if got := a.keepsHistory(p); got != want {
			t.Errorf("keepsHistory(%s) = %v，要 %v", p, got, want)
		}
	}

	big := a.OpenFile(r, "big.txt", true)
	wait("读文件", func() bool { return !big.Loading })
	big.Buf.Set(big.Buf.String() + "y")
	if err := a.WriteDoc(big, true); err != nil {
		t.Fatal(err)
	}
	if n := len(a.LocalHist().Load(at("big.txt")).Entries); n != 0 {
		t.Errorf("太大的记了 %d 条", n)
	}

	a.St.Settings.NoLocalHistory = true
	d := a.OpenFile(r, "a.txt", true)
	wait("读文件", func() bool { return !d.Loading })
	d.Buf.Set("b\n")
	a.WriteDoc(d, true)
	if n := len(a.LocalHist().Load(at("a.txt")).Entries); n != 0 || a.keepsHistory(at("a.txt")) {
		t.Errorf("关了还记了 %d 条", n)
	}
	wait("空闲", idle(r))

	// 没有状态目录：不记、照常保存，菜单里的几项是灰的
	b, wait2 := newTestAppIn(t, testutil.Workspace(t, r0.Root), "")
	if b.LocalHist() != nil || hasLocalHist(b) {
		t.Fatal("没状态目录也有本地历史")
	}
	e := b.OpenFile(b.Current(), "a.txt", true)
	wait2("读文件", func() bool { return !e.Loading })
	e.Buf.Set("c\n")
	if err := b.WriteDoc(e, true); err != nil || mustRead(t, at("a.txt")) != "c\n" {
		t.Fatalf("没状态目录的保存：%v", err)
	}
	wait2("空闲", idle(b.Current()))
}
