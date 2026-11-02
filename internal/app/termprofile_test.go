package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 终端配置：用配置开的格跑配置的命令（没参数 -l）、加配置的环境，termd 的请求一样；分出来的格跟着；
// 标签名换成配置名；命令面板 ">term " 列默认和配置；配置进会话、重开还原回来；设置里删了的配置重开退回默认、提示一次。都不启动 shell。
func TestNewTerminalProfile(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	state := t.TempDir()
	a, wait := newTestAppIn(t, ws, state)
	r := a.Repos[0]
	a.RefreshRepo(r, WhyManual)
	wait("读完状态", idle(r))
	s := &a.St.Settings
	s.Shell = "/bin/sh"
	s.TermProfiles = []workspace.TermProfile{
		{Name: "fish", Shell: "/opt/homebrew/bin/fish", Args: []string{"-i"}, Env: []string{"PROF=1"}},
		{Name: "zsh", Shell: "/bin/zsh"},
	}
	if items, _ := a.paletteItems(">term "); len(items) != 3 || items[0].Kind != ItemProfile || items[1].Text != "fish" || items[1].Detail != "/opt/homebrew/bin/fish -i" {
		t.Fatalf(">term 列表：%+v", items)
	}
	if items, _ := a.paletteItems(">term zs"); len(items) != 1 || items[0].args != "zsh" {
		t.Fatalf(">term zs：%+v", items)
	}
	if items, _ := a.paletteItems(">term qqqq"); slices.ContainsFunc(items, func(it PaletteItem) bool { return it.Kind == ItemProfile }) {
		t.Fatalf("对不上配置时应当成找命令：%+v", items)
	}
	_, def, p0 := a.ActivePane()
	if o := a.paneOptions(r, p0, 1, r.Root); !slices.Equal(o.Command, []string{"/bin/sh", "-l"}) || len(o.Env) != 0 {
		t.Fatalf("默认的格：%q %q", o.Command, o.Env)
	}
	a.NewTerminalProfile(r, "", "fish")
	_, tab, p := a.ActivePane()
	if p.Profile != "fish" || tab == def || r.TermLabel(tab) != "fish 2" || r.TermLabel(def) != "终端 · "+r.Name {
		t.Fatalf("配置 %q，标签 %q / %q", p.Profile, r.TermLabel(tab), r.TermLabel(def))
	}
	if o := a.paneOptions(r, p, 1, r.Root); !slices.Equal(o.Command, []string{"/opt/homebrew/bin/fish", "-i"}) || !slices.Equal(o.Env, []string{"PROF=1"}) {
		t.Fatalf("配置的格：%q %q", o.Command, o.Env)
	}
	req := a.termdReq(r, p, r.Root)
	if req.Shell != "/opt/homebrew/bin/fish" || !slices.Equal(req.Args, []string{"-i"}) || req.Env[len(req.Env)-1] != "PROF=1" {
		t.Fatalf("termd 请求：%q %q …%q", req.Shell, req.Args, req.Env[len(req.Env)-1])
	}
	a.SplitTerm(r, true)
	if _, _, q := a.ActivePane(); q == p || q.Profile != "fish" {
		t.Fatalf("分出来的格没跟着配置：%q", q.Profile)
	}
	a.NewTerminalProfile(r, "", "zsh")
	_, _, z := a.ActivePane()
	if o := a.paneOptions(r, z, 1, r.Root); !slices.Equal(o.Command, []string{"/bin/zsh", "-l"}) {
		t.Fatalf("没参数的配置：%q", o.Command)
	}
	a.NewTerminalProfile(r, "", "没有这个")
	if _, _, n := a.ActivePane(); n.Profile != "" {
		t.Fatalf("没有的配置应开默认的：%q", n.Profile)
	}
	if b := a.StartedPanes(); b != 0 {
		t.Fatalf("起了 %d 个 shell", b)
	}
	a.SaveState()
	a.Close()

	reopen := func() (*App, *Repo) {
		b, wait := newTestAppIn(t, ws, state)
		r := b.Repos[0]
		b.RefreshRepo(r, WhyManual)
		wait("读完状态", idle(r))
		return b, r
	}
	b, r2 := reopen()
	tabs := b.TermTabs(r2)
	if len(tabs) != 4 || tabs[1].Root.Pane != nil || tabs[1].Root.A.Pane.Profile != "fish" || tabs[1].Root.B.Pane.Profile != "fish" ||
		tabs[2].Root.Pane.Profile != "zsh" || tabs[3].Root.Pane.Profile != "" || tabs[0].Root.Pane.Profile != "" || b.Toast != "" {
		t.Fatalf("重开后 %d 个标签，提示 %q", len(tabs), b.Toast)
	}
	b.Close()

	st, err := workspace.LoadState(state)
	if err != nil {
		t.Fatal(err)
	}
	st.Settings.TermProfiles = st.Settings.TermProfiles[1:] // 删掉 fish
	if err := workspace.SaveState(state, st); err != nil {
		t.Fatal(err)
	}
	c, r3 := reopen()
	tabs = c.TermTabs(r3)
	if tabs[1].Root.A.Pane.Profile != "" || tabs[2].Root.Pane.Profile != "zsh" || !strings.Contains(c.Toast, "fish") {
		t.Fatalf("删了的配置：%q %q，提示 %q", tabs[1].Root.A.Pane.Profile, tabs[2].Root.Pane.Profile, c.Toast)
	}
}
