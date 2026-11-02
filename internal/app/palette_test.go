package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/outline"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// paletteRepos 建两个临时仓库：one 里 a.go（10 行）、x/util.go、internal/app/app.go；two 里 y/util.go、app.go。
func paletteRepos(t *testing.T) (one, two *testutil.Repo, ws string) {
	one = testutil.NewRepo(t, "one")
	one.Write("a.go", strings.Repeat("line\n", 10))
	one.Write("x/util.go", "package x\n")
	one.Write("internal/app/app.go", "package app\n")
	one.Commit("初始")
	two = testutil.NewRepo(t, "two")
	two.Write("y/util.go", "package y\n")
	two.Write("app.go", "package main\n")
	two.Commit("初始")
	return one, two, testutil.Workspace(t, one.Root, two.Root)
}

func itemTexts(items []PaletteItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

// ⌘P 里 ⌥回车：文件项在侧边打开（只有一组时向右分出一组、固定、键盘跟过去），
// 键盘已经在最后一组时接着开进这组；名字:行号 的再跳到那一行。
func TestPaletteSide(t *testing.T) {
	_, two, ws := paletteRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	p := &a.Palette
	left := a.OpenFile(a.Repos[0], "x/util.go", true)
	a.OpenPalette("") // 打开面板时才读文件列表
	wait("读文件列表", func() bool { return a.Repos[0].Files.Loaded && a.Repos[1].Files.Loaded })

	a.OpenPalette("two util")
	a.UpdatePalette()
	a.AcceptPaletteSide(p.Sel)
	d := a.CurrentDoc()
	if p.Open || d == nil || d.Root != two.Root || d.Rel != "y/util.go" || !d.Pinned || !a.FocusDoc {
		t.Fatalf("没在侧边固定打开 two/y/util.go：open=%v doc=%+v", p.Open, d)
	}
	if !a.Ed.Row || a.GroupOf(left) != a.Ed.A.G || a.GroupOf(d) != a.Ed.B.G || a.Grp != a.Ed.B.G {
		t.Fatalf("应分成左右两组、新文件和键盘在右：%d 组", len(a.Groups()))
	}

	a.OpenPalette("a.go:7")
	a.UpdatePalette()
	a.AcceptPaletteSide(0)
	d = a.CurrentDoc()
	if d == nil || d.Rel != "a.go" || len(a.Groups()) != 2 || a.GroupOf(d) != a.Ed.B.G {
		t.Fatalf("a.go:7 应进右组：%+v，%d 组", d, len(a.Groups()))
	}
	wait("读 a.go", func() bool { return d.Buf != nil })
	if pos, _ := d.Cursor(); pos.Line != 6 {
		t.Fatalf("光标在 %+v，应在第 7 行", pos)
	}
}

// 快速打开：跨仓库模糊找文件，最近打开的靠前，名字:行号 打开到那一行，:行号 在当前文件跳行。
func TestPaletteFiles(t *testing.T) {
	_, two, ws := paletteRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	p := &a.Palette

	a.Exec("file.quickOpen")
	if !p.Open || p.Text != "" {
		t.Fatalf("⌘P 没打开面板：%+v", p)
	}
	wait("读文件列表", func() bool { return a.Repos[0].Files.Loaded && a.Repos[1].Files.Loaded })
	a.UpdatePalette()
	if len(p.Items) != 0 || !strings.HasPrefix(p.Note, "敲文件名") {
		t.Fatalf("没打开过文件时应只有提示：%v %q", itemTexts(p.Items), p.Note)
	}

	// 一样好时短的靠前；同样长按字母
	p.Text = "util"
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"one/x/util.go", "two/y/util.go"}) || p.Sel != 0 {
		t.Fatalf("util：%v sel=%d", got, p.Sel)
	}
	if it := p.Items[0]; it.Name != len("one/x/") || !slices.Equal(it.Hits, []int{6, 7, 8, 9}) {
		t.Errorf("命中应在文件名里：%+v", it)
	}
	p.Text = "app.go"
	a.UpdatePalette()
	if got := itemTexts(p.Items); len(got) != 2 || got[0] != "two/app.go" {
		t.Fatalf("app.go：%v", got)
	}
	p.Text = "zzz"
	a.UpdatePalette()
	if len(p.Items) != 0 || p.Note != "没有匹配的文件" || p.Sel != -1 {
		t.Fatalf("zzz：%v %q", itemTexts(p.Items), p.Note)
	}

	// 打开 two/y/util.go：面板关掉，固定标签，焦点给编辑器，记进最近打开
	p.Text = "two util"
	a.UpdatePalette()
	a.AcceptPalette(p.Sel)
	d := a.CurrentDoc()
	if p.Open || d == nil || d.Root != two.Root || d.Rel != "y/util.go" || !d.Pinned || !a.FocusDoc {
		t.Fatalf("没固定打开 two/y/util.go：open=%v doc=%+v", p.Open, d)
	}
	if want := filepath.Join(two.Root, "y", "util.go"); len(a.St.Recent) != 1 || a.St.Recent[0] != want {
		t.Fatalf("最近打开：%v", a.St.Recent)
	}
	if st, _ := workspace.LoadState(a.StateDir); !slices.Equal(st.Recent, a.St.Recent) {
		t.Errorf("最近打开没存进 state.json：%v", st.Recent)
	}

	// 再打开 one/a.go：空着时列最近打开的（当前的除外）；匹配时最近打开的靠前
	a.OpenFile(a.Repos[0], "a.go", true)
	a.OpenPalette("")
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"two/y/util.go"}) || !p.Items[0].Recent {
		t.Fatalf("最近打开的：%v", got)
	}
	p.Text = "util"
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"two/y/util.go", "one/x/util.go"}) {
		t.Fatalf("最近打开的应靠前：%v", got)
	}

	// 名字:行号
	p.Text = "a.go:7"
	a.UpdatePalette()
	if len(p.Items) == 0 || p.Items[0].Text != "one/a.go" || p.Note != "回车打开并跳到第 7 行" {
		t.Fatalf("a.go:7：%v %q", itemTexts(p.Items), p.Note)
	}
	a.AcceptPalette(0)
	d = a.CurrentDoc()
	wait("读 a.go", func() bool { return d.Buf != nil })
	if pos, _ := d.Cursor(); pos.Line != 6 {
		t.Fatalf("光标在 %+v，应在第 7 行", pos)
	}

	// :行号:列 在当前文件里跳
	a.OpenPalette(":3:2")
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"跳到第 3 行第 2 列"}) || p.Items[0].Detail != "a.go · 共 11 行" {
		t.Fatalf(":3:2：%v %q", got, p.Items[0].Detail)
	}
	a.AcceptPalette(0)
	if pos, _ := d.Cursor(); pos.Line != 2 || pos.Col != 1 || p.Open {
		t.Fatalf("光标在 %+v", pos)
	}
	if !a.CanBack() {
		t.Error("跳行后应能后退")
	}

	// 没有文件时不能跳行
	a.CloseOthers(nil)
	a.OpenPalette(":3")
	a.UpdatePalette()
	if len(p.Items) != 0 || p.Note != "先打开一个文件再跳行" {
		t.Fatalf("没文件时 :3：%v %q", itemTexts(p.Items), p.Note)
	}

	// 仓库移除后选它的文件只提示
	a.OpenPalette("one/a.go")
	a.UpdatePalette()
	a.RemoveRepo(a.Repos[0])
	a.AcceptPalette(0)
	if a.Toast != "one 已经不在列表里了" || a.CurrentDoc() != nil {
		t.Errorf("toast %q doc %v", a.Toast, a.CurrentDoc())
	}
}

func TestSplitLine(t *testing.T) {
	for _, c := range []struct {
		in, name  string
		line, col int
		at        bool
	}{
		{"a.go", "a.go", 0, 0, false},
		{"a.go:", "a.go", 0, 0, false},
		{"a.go:12", "a.go", 11, 0, true},
		{"a.go:12:5", "a.go", 11, 4, true},
		{":3", "", 2, 0, true},
		{"a.go:x", "a.go:x", 0, 0, false},
		{"a.go:0", "a.go:0", 0, 0, false},
		{"c:/x:2", "c:/x", 1, 0, true},
	} {
		name, j, at := splitLine(c.in)
		if name != c.name || j.Line != c.line || j.Col != c.col || at != c.at {
			t.Errorf("%q → %q %+v %v", c.in, name, j, at)
		}
	}
}

// 命令面板：> 列命令（菜单顺序、模糊匹配、不能用的灰掉），>git 在当前仓库跑 git 命令。
func TestPaletteCommands(t *testing.T) {
	_, _, ws := paletteRepos(t)
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	p := &a.Palette
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("git status", idle(r))

	a.Exec("palette.commands")
	a.UpdatePalette()
	if p.Text != ">" || len(p.Items) == 0 || p.Items[0].Text != "新建文件…" || p.Items[0].Detail != "文件" {
		t.Fatalf("⇧⌘P：%q %v", p.Text, itemTexts(p.Items))
	}
	p.Text = ">切到"
	a.UpdatePalette()
	// 最后一项总是"在终端运行"：敲的不是命令名时把它当 shell 命令放进终端
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"切到仓库 one", "切到仓库 two", "在终端运行：切到"}) || p.Items[0].Keys != "⌘1" ||
		p.Items[2].Kind != ItemTerm {
		t.Fatalf(">切到：%v", got)
	}
	a.MovePalette(1)
	a.MovePalette(1)
	a.MovePalette(1) // 绕回第一项
	if p.Sel != 0 {
		t.Errorf("↓ 到底应绕回：sel=%d", p.Sel)
	}
	a.MovePalette(-1) // ↑ 绕到最后一项（在终端运行），再 ↑ 是 two
	a.MovePalette(-1)
	a.AcceptPalette(p.Sel)
	if p.Open || a.Current() != a.Repos[1] {
		t.Fatalf("没切到 two：open=%v", p.Open)
	}

	// 不能用的命令灰掉，选它只提示
	a.OpenPalette(">全文diff")
	a.UpdatePalette()
	if len(p.Items) == 0 || p.Items[0].Text != "全文 diff" || !p.Items[0].Off {
		t.Fatalf(">全文diff：%+v", p.Items)
	}
	a.AcceptPalette(0)
	if !p.Open || a.Toast != "“全文 diff”现在不能用" {
		t.Errorf("不能用的命令：open=%v toast=%q", p.Open, a.Toast)
	}
	p.Text = ">git"
	a.UpdatePalette()
	if !slices.Contains(itemTexts(p.Items), "刷新 Git 状态") {
		t.Errorf(">git（没有空格）应还是找命令：%v", itemTexts(p.Items))
	}
	p.Text = ">zzz"
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"在终端运行：zzz"}) || !strings.HasPrefix(p.Note, "没有匹配的命令") {
		t.Errorf(">zzz：%v %q", got, p.Note)
	}

	// >git：⌘K 带着它打开；在当前仓库跑，输出记进底栏
	a.SelectRepo(0)
	a.Exec("git.command")
	a.UpdatePalette()
	if p.Text != ">git " || len(p.Items) != 0 || !strings.HasPrefix(p.Note, "在 one · ") {
		t.Fatalf("⌘K：%q %v %q", p.Text, itemTexts(p.Items), p.Note)
	}
	p.Text = ">git rev-parse --show-toplevel"
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"git rev-parse --show-toplevel"}) {
		t.Fatalf(">git rev-parse：%v", got)
	}
	n := len(a.Output)
	a.St.BottomBar, a.Toast = false, "" // toast 平时由界面取走
	a.AcceptPalette(0)
	if p.Open || r.Busy != "git rev-parse --show-toplevel" {
		t.Fatalf("应关掉面板并开始跑：open=%v busy=%q", p.Open, r.Busy)
	}
	wait("git 命令", func() bool { return idle(r)() && len(a.Output) > n })
	last := a.Output[len(a.Output)-1]
	if last.Failed || last.Title != "$ git rev-parse --show-toplevel" || last.Repo != "one" {
		t.Fatalf("输出：%+v", last)
	}
	if !a.St.BottomBar || a.BottomTab != BottomOutput || a.Toast != "" {
		t.Errorf("跑完应打开底栏的输出：bar=%v tab=%d toast=%q", a.St.BottomBar, a.BottomTab, a.Toast)
	}
	// 跑过的列在下面；选它填回输入框，不直接跑
	a.Exec("git.command")
	a.UpdatePalette()
	if got := itemTexts(p.Items); !slices.Equal(got, []string{"git rev-parse --show-toplevel"}) || p.Items[0].Kind != ItemGitHistory {
		t.Fatalf("历史：%v", got)
	}
	a.AcceptPalette(0)
	if !p.Open || p.Text != ">git rev-parse --show-toplevel" || len(a.Output) != n+1 {
		t.Fatalf("选历史应填回输入框：%q", p.Text)
	}
	// 失败的命令：toast
	p.Text = ">git no-such-subcommand"
	a.UpdatePalette()
	a.AcceptPalette(0)
	wait("失败的命令", func() bool { return idle(r)() && len(a.Output) > n+1 })
	if last := a.Output[len(a.Output)-1]; !last.Failed || a.Toast != "git no-such-subcommand 失败，输出在底栏" {
		t.Errorf("失败：%+v toast=%q", last, a.Toast)
	}
	if !slices.Equal(p.History, []string{"rev-parse --show-toplevel", "no-such-subcommand"}) {
		t.Errorf("历史：%v", p.History)
	}
	// 还在跑时第二条排队，跑完一条跑下一条，输出按顺序记
	a.RunCommand(r, "rev-parse HEAD~0")
	a.RunCommand(r, "status --short")
	if r.Busy != "git rev-parse HEAD~0" || len(r.Queue) != 1 || r.Queue[0].Label != "git status --short" {
		t.Fatalf("应排队：busy=%q queue=%d", r.Busy, len(r.Queue))
	}
	wait("排队的两条", func() bool { return idle(r)() && len(a.Output) >= n+4 })
	if a, b := a.Output[n+2].Title, a.Output[n+3].Title; a != "$ git rev-parse HEAD~0" || b != "$ git status --short" {
		t.Errorf("顺序：%q %q", a, b)
	}
}

// ⌘P 里的"名字@符号"：名字打分第一的文件在第一行，下面是它大纲里匹配的符号；没开的文件后台读、
// 读的期间提示一句，读完按 abs + 大小 + 修改时间记住；开着的用标签的大纲；回车打开文件跳到符号。
// 第一名没有大纲时整段当文件名（logo@2x.png），有大纲没匹配时提示一句、下面是整段当文件名的结果。
func TestPaletteFileSymbols(t *testing.T) {
	one := testutil.NewRepo(t, "one")
	one.Write("svc.go", "package svc\n\ntype Server struct{}\n\nfunc (s *Server) Start() {}\n\nfunc NewServer() *Server { return nil }\n")
	one.Write("logo@2x.png", "png\n")
	one.Write("empty.go", "package empty\n")
	one.Commit("初始")
	a, wait := newTestApp(t, testutil.Workspace(t, one.Root))
	t.Cleanup(a.Close)
	p := &a.Palette
	a.OpenPalette("")
	wait("读文件列表", func() bool { return a.Repos[0].Files.Loaded })
	a.UpdatePalette()
	if !strings.Contains(p.Note, "名字@符号") {
		t.Fatalf("空着时的提示没有 名字@符号：%q", p.Note)
	}

	p.Text = "svc@start"
	a.UpdatePalette()
	if p.Note != "正在读大纲…" || len(p.Items) != 1 || p.Items[0].Kind != ItemFile || p.Items[0].Text != "one/svc.go" {
		t.Fatalf("没开的文件先读大纲：%v %q", itemTexts(p.Items), p.Note)
	}
	wait("读大纲", func() bool { a.UpdatePalette(); return p.Note == "" })
	if len(p.Items) != 2 || p.Items[1].Kind != ItemSymbol || !strings.HasSuffix(p.Items[1].Text, "Start") || !p.Items[1].Under || p.Items[1].Detail != "第 5 行" {
		t.Fatalf("svc@start：%v %+v", itemTexts(p.Items), p.Items)
	}
	if len(a.olCache.keys) != 1 {
		t.Fatalf("缓存 %d 个", len(a.olCache.keys))
	}
	p.Text = "svc@"
	a.UpdatePalette()
	if got := itemTexts(p.Items); len(got) != 4 || got[0] != "one/svc.go" || !strings.HasSuffix(got[3], "NewServer") || p.Note != "" {
		t.Fatalf("svc@（缓存里的，按文件里的顺序）：%v %q", got, p.Note)
	}
	p.Text = "svc@start"
	a.UpdatePalette()
	a.AcceptPalette(1)
	d := a.CurrentDoc()
	if p.Open || d == nil || d.Rel != "svc.go" || !a.FocusDoc {
		t.Fatalf("回车没打开 svc.go：%v %+v", p.Open, d)
	}
	wait("读 svc.go", func() bool { return d.Buf != nil })
	if pos, _ := d.Cursor(); pos.Line != 4 {
		t.Fatalf("光标在 %+v，应在 Start 那行", pos)
	}

	// 开着的：用标签的大纲（改了没存的也算），不读盘
	d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: "\nfunc Extra() {}\n"})
	a.OpenFile(a.Repos[0], "logo@2x.png", true) // 这组有两个标签才分得出侧边的一组
	a.OpenPalette("svc@extra")
	a.UpdatePalette()
	if got := itemTexts(p.Items); len(got) != 2 || !strings.HasSuffix(got[1], "Extra") || p.Note != "" {
		t.Fatalf("开着的 svc.go@extra：%v %q", got, p.Note)
	}
	a.AcceptPaletteSide(1)
	if len(a.Groups()) != 2 {
		t.Fatalf("⌥↩ 应在侧边打开：%d 组", len(a.Groups()))
	}

	// 有大纲、没匹配：提示一句，下面是整段当文件名的结果（这里没有）
	a.OpenPalette("svc@zzz")
	a.UpdatePalette()
	if p.Note != "svc.go 里没有匹配 zzz 的符号" || len(p.Items) != 0 {
		t.Fatalf("svc@zzz：%v %q", itemTexts(p.Items), p.Note)
	}
	// 第一名没有大纲：整段当文件名
	p.Text = "logo@2x"
	a.UpdatePalette()
	if got := itemTexts(p.Items); len(got) != 1 || got[0] != "one/logo@2x.png" || p.Items[0].Kind != ItemFile {
		t.Fatalf("logo@2x：%v %q", got, p.Note)
	}
	p.Text = "empty@x"
	a.UpdatePalette()
	wait("读 empty.go", func() bool { a.UpdatePalette(); return p.Note != "正在读大纲…" })
	if p.Note != "empty.go 里没有符号" || len(p.Items) != 1 {
		t.Fatalf("empty@x：%v %q", itemTexts(p.Items), p.Note)
	}
}

// olCache 只留最近用过的 maxOutlineCache 个，取一次算用过。
func TestOutlineCacheLRU(t *testing.T) {
	var c olCache
	for i := range maxOutlineCache + 1 {
		c.put(olKey{abs: strconv.Itoa(i)}, nil)
		if i == 0 {
			continue
		}
		if _, ok := c.get(olKey{abs: "1"}); !ok && i > 1 {
			t.Fatal("1 一直在用，不该被丢掉")
		}
	}
	if len(c.keys) != maxOutlineCache || len(c.trees) != maxOutlineCache {
		t.Fatalf("留了 %d / %d 个", len(c.keys), len(c.trees))
	}
	if _, ok := c.get(olKey{abs: "0"}); ok {
		t.Fatal("最久没用的 0 应被丢掉")
	}
	if _, ok := c.get(olKey{abs: "1", size: 1}); ok {
		t.Fatal("大小变了是另一个键")
	}
}

// 量"名字@符号"：没开着的 1 MB .go 从敲下 @ 到符号列出来（后台读盘 + 解码 + Parse，回主线程重算面板），
// 以及缓存命中时面板重算一次的用时。WORKBENCH_MEASURE=1 才跑。
func TestMeasureFileSymbols(t *testing.T) {
	if os.Getenv("WORKBENCH_MEASURE") == "" {
		t.Skip("WORKBENCH_MEASURE=1 才量")
	}
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; b.Len() < outline.MaxBytes-600; i++ {
		fmt.Fprintf(&b, "// Handler%d 处理第 %d 类请求。\ntype Handler%d struct {\n\tname  string\n\tcount int\n}\n\n", i, i, i)
		fmt.Fprintf(&b, "func (h *Handler%d) Serve(req string) (string, error) {\n\tif req == \"\" {\n\t\treturn \"\", nil\n\t}\n\th.count++\n\treturn h.name + req, nil\n}\n\n", i)
		fmt.Fprintf(&b, "func newHandler%d() *Handler%d {\n\treturn &Handler%d{name: \"h%d\"}\n}\n\n", i, i, i, i)
	}
	one := testutil.NewRepo(t, "one")
	one.Write("big.go", b.String())
	one.Commit("初始")
	abs := filepath.Join(one.Root, "big.go")
	a, wait := newTestApp(t, testutil.Workspace(t, one.Root))
	t.Cleanup(a.Close)
	p := &a.Palette
	a.OpenPalette("")
	wait("读文件列表", func() bool { return a.Repos[0].Files.Loaded })
	t.Logf("big.go %d 字节", b.Len())

	// 只量后台那段（读盘 + 解码 + Parse + Build），7 次取中位
	var bg []time.Duration
	var syms int
	for range 7 {
		start := time.Now()
		data, _ := os.ReadFile(abs)
		text, _, _ := docs.Decode(data)
		tree := outline.Build(outline.Parse("big.go", text))
		bg = append(bg, time.Since(start))
		syms = len(tree.Flat)
	}
	slices.Sort(bg)
	t.Logf("后台：中位 %v、最快 %v、最慢 %v（%d 个符号）", bg[3].Round(100*time.Microsecond), bg[0].Round(100*time.Microsecond), bg[6].Round(100*time.Microsecond), syms)

	// 从敲下 @ 到列出来：每次换 mtime 让缓存认不出（等待每 5 ms 看一次）
	var e2e []time.Duration
	for i := range 5 {
		mt := time.Now().Add(time.Duration(i+1) * time.Second)
		if err := os.Chtimes(abs, mt, mt); err != nil {
			t.Fatal(err)
		}
		p.Text = "big@serve"
		start := time.Now()
		a.UpdatePalette()
		wait("读大纲", func() bool { a.UpdatePalette(); return p.Note == "" && len(p.Items) > 1 })
		e2e = append(e2e, time.Since(start))
		p.Text = ""
		a.UpdatePalette()
	}
	slices.Sort(e2e)
	t.Logf("敲下 @ 到列出：中位 %v、最快 %v、最慢 %v", e2e[2].Round(100*time.Microsecond), e2e[0].Round(100*time.Microsecond), e2e[4].Round(100*time.Microsecond))

	// 缓存命中：换着输入让面板每次都重算
	var hit []time.Duration
	for i, q := range []string{"big@s", "big@se", "big@ser", "big@serv", "big@serve", "big@h1", "big@h12", "big@new", "big@", "big@handler99"} {
		p.Text = q
		start := time.Now()
		a.UpdatePalette()
		hit = append(hit, time.Since(start))
		if p.Note != "" || len(p.Items) < 2 {
			t.Fatalf("%d %q：没命中缓存 %q", i, q, p.Note)
		}
	}
	slices.Sort(hit)
	t.Logf("缓存命中重算一次：中位 %v、最快 %v、最慢 %v（%d 项）", hit[5].Round(10*time.Microsecond), hit[0].Round(10*time.Microsecond), hit[9].Round(10*time.Microsecond), len(p.Items))
}
