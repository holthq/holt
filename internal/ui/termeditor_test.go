package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 编辑器区的终端：⌃⇧⌘`（term.newEditor）在编辑器区开一个终端标签（组里有、底栏没有），键盘给它；
// 编辑器区标签右键“挪回底栏”、底栏标签右键“挪到编辑器区”来回挪，shell 不重启、屏幕还在；
// 关标签：前台有命令在跑先问，一起关的（关闭全部）留着它；没命令的 ⌘W 直接关、不碰别的标签；
// 会话来回：重开窗口后标签还在、对得上编辑器区的终端，上次的输出喂回去。
func TestTermInEditor(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	r := a.Repos[0]
	wait("git status", idle(r))
	readyPane(t, a, tt)
	termDocs := func() []*docs.Doc {
		return slices.DeleteFunc(slices.Clone(a.AllDocs()), func(d *docs.Doc) bool { return d.Kind != docs.Term })
	}
	// ready 画一帧让编辑器区那一格起 shell、拿到键盘，等出提示符
	ready := func(tab *app.TermTab) *app.TermPane {
		t.Helper()
		tt.Frame()
		p := tab.Current()
		if p.Term == nil {
			t.Fatalf("编辑器区的终端没起来：%+v", p)
		}
		testutil.WaitTermReady(t, tt, p.Term, 300*time.Millisecond, 15*time.Second)
		tt.Frame()
		return p
	}

	tt.Key(wui.Ctrl|wui.Shift|wui.Cmd, wui.KeyBackquote)
	tt.Frame()
	ds := termDocs()
	if len(ds) != 1 || len(r.EdTerms) != 1 || len(a.TermTabs(r)) != 1 || a.CurrentDoc() != ds[0] || ds[0].Title != "终端 2" {
		t.Fatalf("⌃⇧⌘`：标签 %d 个（当前 %v），编辑器区 %d、底栏 %d", len(ds), a.CurrentDoc(), len(r.EdTerms), len(a.TermTabs(r)))
	}
	d, tab := ds[0], r.EdTerms[0]
	p := ready(tab)
	if _, at, ap := a.ActivePane(); at != tab || ap != p || !a.TermFocused {
		t.Fatalf("键盘不在编辑器区的终端里：%v %v", at == tab, a.TermFocused)
	}
	typeLine(t, tt, p, "echo ed-$((6*7))", "ed-42")
	mustText(t, tt, "挪回底栏")

	// 挪回底栏：同一个 shell、屏幕还在，排在最后、选中
	term := p.Term
	mustRightClick(t, tt, "标签 终端 2", "挪回底栏")
	if len(termDocs()) != 0 || len(r.EdTerms) != 0 || len(a.TermTabs(r)) != 2 || r.TermSel != 1 || a.TermTabs(r)[1] != tab {
		t.Fatalf("挪回底栏后：标签 %d，编辑器区 %d，底栏 %d（选中 %d）", len(termDocs()), len(r.EdTerms), len(a.TermTabs(r)), r.TermSel)
	}
	tt.Frame()
	if p.Term != term || !strings.Contains(p.Term.Text(), "ed-42") {
		t.Fatal("挪回底栏后 shell 换了或屏幕丢了")
	}
	typeLine(t, tt, p, "echo bottom-$((2+3))", "bottom-5")

	// 再挪到编辑器区（底栏标签的右键）
	mustRightClick(t, tt, "终端 2", "挪到编辑器区")
	if ds = termDocs(); len(ds) != 1 || len(r.EdTerms) != 1 || len(a.TermTabs(r)) != 1 || r.TermSel != 0 || a.CurrentDoc() != ds[0] {
		t.Fatalf("挪到编辑器区后：标签 %d，编辑器区 %d，底栏 %d", len(ds), len(r.EdTerms), len(a.TermTabs(r)))
	}
	d = ds[0]
	tt.Frame()
	if p.Term != term || !strings.Contains(p.Term.Text(), "bottom-5") {
		t.Fatal("挪到编辑器区后 shell 换了或屏幕丢了")
	}

	// 前台有命令：关闭全部留着它、提示；× 先问，取消不关，确认才关
	typeLine(t, tt, p, "sleep 30", "sleep 30")
	wait("sleep 在前台", func() bool { return p.Job() == "sleep" })
	a.CloseOthers(nil)
	if len(termDocs()) != 1 || !strings.Contains(a.Toast, "1 个终端标签有命令在运行，留着了") {
		t.Fatalf("关闭全部：标签 %d，提示 %q", len(termDocs()), a.Toast)
	}
	a.Toast = ""
	a.CloseDoc(d)
	tt.Frame()
	if a.Confirm == nil || !tt.HasText("关闭终端标签？") || !tt.HasText("“sleep”还在运行，关掉终端会结束它。") {
		t.Fatalf("有命令在跑没问：%+v", a.Confirm)
	}
	mustClick(t, tt, "取消")
	if len(termDocs()) != 1 || p.Term == nil {
		t.Fatal("取消了还是关了")
	}
	mustClick(t, tt, "关闭 终端 2")
	if a.Confirm == nil {
		t.Fatal("点 × 没问")
	}
	mustClick(t, tt, "关闭")
	tt.Frame()
	if len(termDocs()) != 0 || len(r.EdTerms) != 0 || p.Term != nil || len(a.TermTabs(r)) != 1 {
		t.Fatalf("确认后没关：标签 %d，编辑器区 %d", len(termDocs()), len(r.EdTerms))
	}

	// 没命令：键盘在里面 ⌘W 直接关（只关这一格 = 关标签），别的标签不动
	a.OpenFile(r, "README.md", true)
	wait("README 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	a.Exec("term.newEditor")
	tab = r.EdTerms[0]
	p = ready(tab)
	wait("认领到 shell 的进程号", func() bool { return p.Job() == "" })
	tt.Key(wui.Cmd, wui.KeyW)
	tt.Frame()
	if a.Confirm != nil || len(termDocs()) != 0 || len(r.EdTerms) != 0 {
		t.Fatalf("没命令的 ⌘W：confirm=%v，标签 %d", a.Confirm, len(termDocs()))
	}
	if cur := a.CurrentDoc(); cur == nil || cur.Rel != "README.md" {
		t.Fatalf("关了终端标签之后当前标签 %v", cur)
	}

	// 会话来回：编辑器区的终端（当前标签）重开后还在，号重新排、对得上；输出喂回去
	a.Exec("term.newEditor")
	tab = r.EdTerms[0]
	p = ready(tab)
	typeLine(t, tt, p, "echo keep-$((3*3))", "keep-9")
	a.NoteTermDirs()
	a.SaveState()
	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Sessions[a.WsPath]
	i := slices.IndexFunc(s.Tabs, func(t workspace.Tab) bool { return t.Kind == workspace.TabTerm })
	if i < 0 || s.Tabs[i].TermNum != tab.Num || i != s.Active || s.Terms[r.Root] == nil {
		t.Fatalf("会话里没记编辑器区的终端：%+v", s.Tabs)
	}
	ts := s.Terms[r.Root].Tabs
	if len(ts) != 2 || ts[0].InEditor || !ts[1].InEditor || ts[1].Num != tab.Num {
		t.Fatalf("会话里的终端：%+v", ts)
	}
	a.Close()

	a, tt, wait = termUI(t, ws, state)
	a.Start() // 放回上次的标签
	r = a.Repos[0]
	wait("git status", idle(r))
	ds = termDocs()
	if len(ds) != 1 || len(r.EdTerms) != 1 || len(a.TermTabs(r)) != 1 || a.CurrentDoc() != ds[0] {
		t.Fatalf("重开后：标签 %d（当前 %v），编辑器区 %d，底栏 %d", len(ds), a.CurrentDoc(), len(r.EdTerms), len(a.TermTabs(r)))
	}
	if _, rt := a.TermTabOf(ds[0]); rt != r.EdTerms[0] || !slices.ContainsFunc(a.AllDocs(), func(d *docs.Doc) bool { return d.Rel == "README.md" }) {
		t.Fatal("重开后标签对不上编辑器区的终端，或别的标签丢了")
	}
	a.FocusDoc = true
	p = ready(r.EdTerms[0])
	if text := p.Term.Text(); !strings.Contains(text, "上次的输出（") || !strings.Contains(text, "keep-9") {
		t.Fatalf("重开后没喂回上次的输出：\n%s", text[max(0, len(text)-500):])
	}
	typeLine(t, tt, p, "echo again-$((1+2))", "again-3")
}
