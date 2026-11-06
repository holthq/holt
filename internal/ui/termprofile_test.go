package ui

import (
	"slices"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 终端配置：设置 → 终端 的“其他配置”表加一行（名字、命令、参数、环境变量），存进 state.json；
// 底栏「+」右键挑这个配置开一个终端：shell 按配置的参数起、环境里有配置的变量，标签名是配置名；⌥点「+」开命令面板的 ">term "。
func TestTermProfileUI(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	state := termState(t, true)
	a, tt, wait := termUI(t, ws, state)
	r := a.Repos[0]
	wait("git status", idle(r))
	readyPane(t, a, tt)

	st := wui.NewTester(SettingsView(a), 760, 640)
	st.SetLocale(i18n.Locale())
	st.Frame()
	mustClick(t, st, "终端")
	mustText(t, st, "其他配置")
	mustClick(t, st, "添加配置")
	if len(a.St.Settings.TermProfiles) != 1 {
		t.Fatalf("添加配置后 %d 行", len(a.St.Settings.TermProfiles))
	}
	mustText(t, st, "名字和命令都填了才用")
	for _, f := range [][2]string{{"配置的名字", "sh-i"}, {"命令", "sh"}, {"参数", "-i"}, {"环境变量", "PROF_X=hi"}} {
		mustClick(t, st, f[0])
		st.Type(f[1])
		st.Frame()
	}
	want := workspace.TermProfile{Name: "sh-i", Shell: "sh", Args: []string{"-i"}, Env: []string{"PROF_X=hi"}}
	got := a.St.Settings.TermProfiles[0]
	if got.Name != want.Name || got.Shell != want.Shell || !slices.Equal(got.Args, want.Args) || !slices.Equal(got.Env, want.Env) {
		t.Fatalf("填好的配置 %+v", got)
	}
	if saved, err := workspace.LoadState(state); err != nil || len(saved.Settings.TermProfiles) != 1 || saved.Settings.TermProfiles[0].Name != "sh-i" {
		t.Fatalf("没存进 state.json：%v %+v", err, saved.Settings.TermProfiles)
	}
	if st.HasText("名字和命令都填了才用") {
		t.Fatal("填好了还提示")
	}

	tt.Frame()
	plus := app.Tip(i18n.T("新建终端"), "term.new")
	mustRightClick(t, tt, plus, "sh-i")
	p := readyPane(t, a, tt)
	if p.Profile != "sh-i" {
		var labels []string
		for _, tb := range a.TermTabs(r) {
			labels = append(labels, r.TermLabel(tb))
		}
		t.Fatalf("右键菜单开的格配置 %q；标签 %q，选中 %d", p.Profile, labels, r.TermSel)
	}
	mustText(t, tt, "sh-i 2")
	typeLine(t, tt, p, "echo $PROF_X-$((1+1))", "hi-2")

	if err := tt.ClickWith(wui.Alt, plus); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.Palette.Open || a.Palette.Text != ">term " {
		t.Fatalf("⌥点「+」：面板 %v %q", a.Palette.Open, a.Palette.Text)
	}
	mustText(t, tt, "默认")
	a.ClosePalette()
}
