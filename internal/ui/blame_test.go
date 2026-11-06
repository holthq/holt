package ui

import (
	"slices"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// blameRepo 是一个文件三段历史：Alice 写了 6 行，Bob 改了第 3、4 行，Alice 又改了第 6 行；工作区里第 5 行改了没提交。
func blameRepo(t *testing.T) (*testutil.Repo, [3]string) {
	t.Helper()
	r := testutil.NewRepo(t, "work")
	commit := func(author, msg string, when time.Time) string {
		d := when.Format(time.RFC3339)
		r.Git("add", "-A")
		r.Git("-c", "user.name=x", "commit", "-q", "--author="+author, "--date="+d, "-m", msg)
		return r.Git("rev-parse", "HEAD")
	}
	now := time.Now()
	var h [3]string
	r.Write("a.txt", "one\ntwo\nthree\nfour\nfive\nsix\n")
	h[0] = commit("Alice <alice@example.com>", "先写六行", now.Add(-40*24*time.Hour))
	r.Write("a.txt", "one\ntwo\nTHREE\nFOUR\nfive\nsix\n")
	h[1] = commit("Bob <bob@example.com>", "改大写", now.Add(-10*24*time.Hour))
	r.Write("a.txt", "one\ntwo\nTHREE\nFOUR\nfive\nSIX\n")
	h[2] = commit("Alice <alice@example.com>", "最后一行也大写", now.Add(-2*24*time.Hour))
	r.Write("a.txt", "one\ntwo\nTHREE\nFOUR\nFIVE\nSIX\n")
	return r, h
}

// ⌃⌘B 开 blame：每段一块，按行对齐；未提交的一段标出来；底栏写几次提交；单击一段在右栏历史里选中那次提交，
// 右键打开那次提交里这个文件的 diff；改了没保存时淡下去、顶上说一声，保存后重算；再按一次关掉。
func TestBlameGutter(t *testing.T) {
	r0, h := blameRepo(t)
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	d := a.OpenFile(r, "a.txt", true)
	wait("读文件", func() bool { return !d.Loading })
	tt.Key(wui.Ctrl|wui.Cmd, wui.KeyB)
	if !d.ShowBlame {
		t.Fatal("⌃⌘B 没开 blame")
	}
	wait("blame", func() bool { return d.Blame != nil && tt.HasText("Bob · 10 天前") })
	if _, last := d.St.VisibleLines(); last < 5 {
		t.Fatalf("开了装订线，编辑器只看得见 %d 行（没撑满高）", last+1)
	}
	if len(d.Blame.Lines) != 6 {
		t.Fatalf("blame 有 %d 行", len(d.Blame.Lines))
	}
	for _, s := range []string{"Alice · 1 个月前", "Alice · 2 天前", "未提交", "blame · 3 次提交", git.Short(h[1])} {
		if !tt.HasText(s) {
			t.Errorf("装订线上没有 %q：%q", s, tt.Texts())
		}
	}
	// 段按行对齐：Bob 那段从第 3 行起，比第一段低两行
	first, ok1 := tt.Find("blame：" + git.Short(h[0]) + " Alice · 1 个月前 先写六行")
	bob, ok2 := tt.Find("blame：" + git.Short(h[1]) + " Bob · 10 天前 改大写")
	if !ok1 || !ok2 {
		t.Fatalf("找不到段：%v %v", ok1, ok2)
	}
	if want := float32(d.St.LineTop(2) - d.St.LineTop(0)); abs32(bob.Y-first.Y-want) > 0.5 {
		t.Errorf("Bob 那段在 %v，第一段在 %v，差应是两行 %v", bob.Y, first.Y, want)
	}
	if want := float32(d.St.LineTop(4) - d.St.LineTop(2)); abs32(bob.H-want) > 0.5 {
		t.Errorf("Bob 那段高 %v，应是两行 %v", bob.H, want)
	}

	// 单击：右栏历史里选中、展开它
	tt.ClickAt(bob.X+bob.W/2, bob.Y+bob.H/2)
	tt.Frame()
	wait("历史里选中", func() bool { return r.Hist.Expanded == h[1] })
	if !a.St.RightBar || a.St.HistAll {
		t.Errorf("右栏没切到当前仓库的历史：RightBar=%v HistAll=%v", a.St.RightBar, a.St.HistAll)
	}

	// 右键：打开这次提交里这个文件的 diff（和 Alice 的第一版比）
	tt.RightClickAt(bob.X+bob.W/2, bob.Y+bob.H/2)
	tt.Frame()
	if err := tt.ChooseMenuItem("打开这次提交里这个文件的 diff"); err != nil {
		t.Fatalf("右键菜单：%v %q", err, tt.Menu())
	}
	tt.Frame()
	dd := a.CurrentDoc()
	if dd.Kind != docs.Diff || dd.Change.Commit != h[1] || dd.Change.Parent != h[0] || dd.Change.Path != "a.txt" {
		t.Fatalf("打开的是 %v %+v", dd.Kind, dd.Change)
	}
	a.CloseDoc(dd)
	tt.Frame()

	// 改了没保存：淡下去、说一声，不重读；保存后重读，未提交的那段变成两段（第 1、5 行）
	if a.CurrentDoc() != d {
		t.Fatal("关了 diff 标签没回到 a.txt")
	}
	d.Buf.Edit(editAt(d, 0, "ONE"))
	tt.Frame()
	if !tt.HasText("改了没保存，blame 的行号可能对不上；保存后重算") {
		t.Errorf("改了没保存没说：%q", tt.Texts())
	}
	a.Save(d)
	wait("保存后重算", func() bool {
		return !d.Modified() && d.Blame != nil && d.Blame.Lines[0].Hash == git.ZeroHash && !d.BlameLoading
	})

	tt.Key(wui.Ctrl|wui.Cmd, wui.KeyB)
	tt.Frame()
	if d.ShowBlame || tt.HasText("blame · 3 次提交") {
		t.Fatal("再按 ⌃⌘B 没关掉")
	}
	if _, ok := tt.Find("blame 装订线"); ok {
		t.Fatal("关了还有装订线")
	}
}

// editAt 是把第 line 行整行换成 text 的编辑。
func editAt(d *docs.Doc, line int, text string) editor.Edit {
	start := d.Buf.Offset(editor.Pos{Line: line})
	return editor.Edit{Start: start, End: start + len(d.Buf.Line(line)), Text: text}
}

// 行内 blame（设置 Git 页 / view.inlineBlame）：键盘所在的文件标签光标行行尾一条"作者，多久前 · 摘要"
// （四个空格隔开），跟着光标换行；未提交的行写"未提交的改动"；摘要太长截断；改了没保存时拿掉，保存后重读再放；
// 分屏时只放在键盘那组；关掉后没开装订线的标签扔掉读好的 blame，开着装订线的留着。
func TestInlineBlame(t *testing.T) {
	r0, _ := blameRepo(t)
	long := "这是一条特别长的提交摘要，用来看行内 blame 会不会截断：一二三四五六七八九十，一二三四五六七八九十，一二三四五六七八九十，一二三四五"
	r0.Write("b.txt", "x\n")
	r0.Git("add", "b.txt")
	r0.Git("-c", "user.name=x", "commit", "-q", "--author=Carol <carol@example.com>", "-m", long)
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	d := a.OpenFile(r, "a.txt", true)
	wait("读文件", func() bool { return !d.Loading })
	tt.Frame()
	if d.Blame != nil || d.BlameLoading || d.BlameInlay.Text != "" {
		t.Fatal("没开就读了 blame")
	}
	cursor := func(d *docs.Doc, line int) {
		at := d.Buf.Offset(editor.Pos{Line: line, Col: 1})
		d.St.Select(at, at)
		tt.Frame()
	}
	end := func(d *docs.Doc, line int) int {
		return d.Buf.Offset(editor.Pos{Line: line, Col: len(d.Buf.Line(line))})
	}
	a.Exec("view.inlineBlame")
	if !a.St.Settings.InlineBlame {
		t.Fatal("命令没开行内 blame")
	}
	cursor(d, 2)
	wait("行内 blame", func() bool { return d.BlameInlay.Text != "" })
	if in := d.BlameInlay; in.Text != "    Bob，10 天前 · 改大写" || in.Offset != end(d, 2) {
		t.Fatalf("第 3 行：%q @%d", in.Text, in.Offset)
	}
	if d.ShowBlame || tt.HasText("blame · 3 次提交") {
		t.Error("行内 blame 不该开装订线")
	}
	cursor(d, 5)
	if in := d.BlameInlay; in.Text != "    Alice，2 天前 · 最后一行也大写" || in.Offset != end(d, 5) {
		t.Fatalf("第 6 行：%q", in.Text)
	}
	cursor(d, 4)
	if d.BlameInlay.Text != "    未提交的改动" {
		t.Fatalf("没提交的第 5 行：%q", d.BlameInlay.Text)
	}

	// 改了没保存：拿掉；保存后重读、再放（第 1 行成了未提交的）
	d.Buf.Edit(editAt(d, 0, "ONE"))
	cursor(d, 0)
	if d.BlameInlay.Text != "" {
		t.Fatalf("改了没保存还放着 %q", d.BlameInlay.Text)
	}
	a.Save(d)
	wait("保存后重读", func() bool { return !d.BlameLoading && d.BlameInlay.Text == "    未提交的改动" })

	// 摘要太长：60 个字加…；分屏到右边，键盘跟过去：左边那组的 a.txt 拿掉
	b := a.OpenFile(r, "b.txt", false)
	wait("读 b.txt", func() bool { return !b.Loading })
	a.SplitDoc(b, app.SideRight)
	cursor(b, 0)
	wait("b.txt 的行内 blame", func() bool { return b.BlameInlay.Text != "" })
	if want := "    Carol，刚刚 · " + string([]rune(long)[:60]) + "…"; b.BlameInlay.Text != want {
		t.Fatalf("长摘要：%q", b.BlameInlay.Text)
	}
	if a.CurrentDoc() != b || d.BlameInlay.Text != "" {
		t.Fatalf("分屏后左边那组还放着 %q", d.BlameInlay.Text)
	}

	// 编辑器右键（右边那组的编辑器中间）：两种 blame 并排；右键开装订线
	st, ok := tt.Find("行 1，列 2")
	if !ok {
		t.Fatalf("状态栏没有行列：%q", tt.Texts())
	}
	choose := func(item string) {
		t.Helper()
		tt.RightClickAt(st.X-300, 400)
		tt.Frame()
		m := tt.Menu()
		if i := slices.Index(m, "显示 blame 装订线"); i < 0 || m[i+1] != "显示行内 blame" {
			t.Fatalf("编辑器右键 %q", m)
		}
		if err := tt.ChooseMenuItem(item); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	choose("显示 blame 装订线")
	if !b.ShowBlame || !tt.HasText("blame · 1 次提交") {
		t.Fatal("右键没开 b.txt 的装订线")
	}

	// 关掉行内的：没开装订线的 a.txt 扔掉 blame，b.txt 的留着；行尾都拿掉
	a.Exec("view.inlineBlame")
	tt.Frame()
	if a.St.Settings.InlineBlame || d.Blame != nil || b.Blame == nil || b.BlameInlay.Text != "" || !tt.HasText("blame · 1 次提交") {
		t.Fatalf("关掉：开着 %v，a.txt %v，b.txt %v %q", a.St.Settings.InlineBlame, d.Blame != nil, b.Blame != nil, b.BlameInlay.Text)
	}
	tt.Key(wui.Ctrl|wui.Cmd, wui.KeyB) // 两个都关了：扔掉
	tt.Frame()
	if b.ShowBlame || b.Blame != nil {
		t.Fatalf("关装订线：%v %v", b.ShowBlame, b.Blame != nil)
	}
	a.Exec("view.inlineBlame")
	cursor(b, 0) // 右键把光标放到了点的地方（最后的空行）
	wait("再开", func() bool { return b.BlameInlay.Text != "" })
	choose("显示行内 blame")
	if a.St.Settings.InlineBlame || b.Blame != nil || b.BlameInlay.Text != "" {
		t.Fatalf("右键关行内 blame：%v %v %q", a.St.Settings.InlineBlame, b.Blame != nil, b.BlameInlay.Text)
	}
}
