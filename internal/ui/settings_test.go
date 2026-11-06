package ui

import (
	"reflect"
	"strings"
	"testing"
	"unicode"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/workspace"
)

// 设置窗口（⌘,）：点分组、下拉、开关、输入框改设置，主窗口下一帧就跟上（外观和材质交给窗口、编辑器的缩进和软换行、
// 终端挪到右边），state.json 里马上有；最后换成英文，两个窗口的文案都换掉。
func TestSettingsWindow(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	r0 := testutil.NewRepo(t, "work")
	r0.Write("main.go", "package main\n\nfunc main() {\n\tprintln(\"一行很长很长的字\")\n}\n")
	r0.Commit("初始提交")
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), stateDir)
	var themes []string
	a.OnSettings = func(s *workspace.Settings) { themes = append(themes, s.Theme) }
	opened := 0
	a.OpenSettings = func() { opened++ }
	r := a.Repos[0]
	a.St.BottomBar = true
	d := a.OpenFile(r, "main.go", true)
	wait("读文件", func() bool { return !d.Loading })

	tt.Key(wui.Cmd, wui.KeyComma)
	if opened != 1 {
		t.Fatalf("⌘, 没打开设置窗口（%d）", opened)
	}
	st := wui.NewTester(SettingsView(a), 760, 540)
	st.SetLocale(i18n.Locale())
	st.Frame()
	pick := func(current, want string) {
		t.Helper()
		mustClick(t, st, current)
		mustClick(t, st, want)
	}
	saved := func() workspace.Settings {
		t.Helper()
		s, err := workspace.LoadState(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		return s.Settings
	}

	// 外观
	pick("跟随系统", "深色")
	if a.St.Settings.Theme != "dark" || len(themes) == 0 || themes[len(themes)-1] != "dark" {
		t.Fatalf("外观 %q，交给窗口的 %q", a.St.Settings.Theme, themes)
	}
	// 窗口底默认柔和；纯色存成 flat、通透存成 vibrancy，都马上交给窗口（窗口换材质）；不是通透时材质那一项点不开
	n := len(themes)
	pick("柔和", "纯色")
	if a.St.Settings.Backdrop != "flat" || saved().Backdrop != "flat" || len(themes) != n+1 {
		t.Fatalf("换成纯色：%q 存的 %q，交给窗口 %d 次", a.St.Settings.Backdrop, saved().Backdrop, len(themes)-n)
	}
	st.Click("侧栏")
	st.Frame()
	if _, ok := st.Find("弹出层"); ok {
		t.Fatal("不是通透时材质的下拉不该打开")
	}
	n = len(themes)
	pick("纯色", "通透")
	if a.St.Settings.Backdrop != "vibrancy" || saved().Backdrop != "vibrancy" || len(themes) != n+1 {
		t.Fatalf("换成通透：%q 存的 %q，交给窗口 %d 次", a.St.Settings.Backdrop, saved().Backdrop, len(themes)-n)
	}
	// 材质默认侧栏；通透时换了马上交给窗口（窗口 SetVibrancy）、存进 state.json
	n = len(themes)
	pick("侧栏", "菜单")
	if a.St.Settings.Material != "menu" || saved().Material != "menu" || len(themes) != n+1 {
		t.Fatalf("换材质：%q 存的 %q，交给窗口 %d 次", a.St.Settings.Material, saved().Material, len(themes)-n)
	}
	pick("菜单", "侧栏")
	if a.St.Settings.Material != "" || saved().Material != "" {
		t.Fatalf("换回侧栏应存成空：%q", a.St.Settings.Material)
	}
	pick("通透", "柔和")
	if a.St.Settings.Backdrop != "" || saved().Backdrop != "" {
		t.Fatalf("换回柔和应存成空：%q", a.St.Settings.Backdrop)
	}
	// 强调色默认钢蓝；选中行用淡底默认开着，关掉存成 solidSelection
	pick("钢蓝", "青绿")
	if a.St.Settings.Accent != "teal" || saved().Accent != "teal" {
		t.Fatalf("换强调色：%q 存的 %q", a.St.Settings.Accent, saved().Accent)
	}
	mustClick(t, st, "选中行用淡底")
	if !a.St.Settings.SolidSelection || !saved().SolidSelection {
		t.Fatal("关掉选中行用淡底没生效或没存")
	}
	// 跟随系统存实名（外观已经换成深色，"跟随系统"只剩强调色这一个）
	pick("青绿", "跟随系统")
	if a.St.Settings.Accent != "system" || saved().Accent != "system" {
		t.Fatalf("跟随系统该存成 system：%q 存的 %q", a.St.Settings.Accent, saved().Accent)
	}
	pick("跟随系统", "钢蓝")
	mustClick(t, st, "选中行用淡底")
	if a.St.Settings.Accent != "" || a.St.Settings.SolidSelection || saved().Accent != "" || saved().SolidSelection {
		t.Fatalf("换回默认应存成空：%q %v", a.St.Settings.Accent, a.St.Settings.SolidSelection)
	}

	// 编辑器：缩进按语言（Go 用 Tab）→ 2 个空格；软换行
	if !tt.HasText("Tab 4") {
		t.Fatalf("Go 文件按语言应是 Tab 4：%q", tt.Texts())
	}
	mustClick(t, st, "编辑器")
	pick("按语言", "2 个空格")
	mustClick(t, st, "软换行")
	tt.Frame()
	if !tt.HasText("空格 2") || d.St.TabSize != 2 || !d.St.SoftWrap {
		t.Fatalf("编辑器没跟上：TabSize=%d SoftWrap=%v", d.St.TabSize, d.St.SoftWrap)
	}

	// 文件树：三个开关默认开，关掉存成反过来的 treeNo*，再开回去存成空
	mustClick(t, st, "文件树")
	for _, k := range []struct {
		name string
		v    *bool
	}{
		{"文件树跟随当前文件", &a.St.Settings.TreeNoFollow},
		{"合并只有一个子目录的目录", &a.St.Settings.TreeNoCompact},
		{"缩进参考线", &a.St.Settings.TreeNoGuides},
	} {
		mustClick(t, st, k.name)
		if !*k.v || !reflect.DeepEqual(saved(), a.St.Settings) {
			t.Fatalf("关掉 %s 没生效或没存", k.name)
		}
		mustClick(t, st, k.name)
		if *k.v || !reflect.DeepEqual(saved(), a.St.Settings) {
			t.Fatalf("打开 %s 没生效或没存", k.name)
		}
	}
	if !st.HasText("显示被忽略的文件") {
		t.Fatal("显示被忽略的文件应在文件树这一组")
	}

	// 终端挪到右边：主区右边出现调整终端宽度的拖条
	mustClick(t, st, "终端")
	if _, ok := tt.Find("调整终端宽度"); ok {
		t.Fatal("终端在底栏时不该有调整宽度的拖条")
	}
	// 终端页行多：480 高的窗口里"位置"在下面，先滚下去
	st.Move(300, 300)
	st.Scroll(300, 300, 0, 100000)
	st.Frame()
	if rc, ok := st.Find("底栏"); !ok || rc.H == 0 || rc.Y+rc.H > 480 {
		t.Fatalf("滚到底后\"位置\"还看不到：%+v %v", rc, ok)
	}
	pick("底栏", "右边")
	tt.Frame()
	if _, ok := tt.Find("调整终端宽度"); !ok {
		t.Fatal("终端挪到右边后应有调整宽度的拖条")
	}

	// Git：拉取方式、作者
	mustClick(t, st, "Git")
	st.Move(300, 300)
	st.Scroll(300, 300, 0, -100000) // 终端页滚到了底，Git 页的行多了以后也跟着滚下去了：滚回顶上
	st.Frame()
	pick("只快进（--ff-only）", "变基（--rebase）")
	mustClick(t, st, "作者名字")
	st.Type("某人")
	st.Frame()
	if s := a.St.Settings; s.PullMode != "rebase" || s.AuthorName != "某人" {
		t.Fatalf("Git 设置 %q %q", s.PullMode, s.AuthorName)
	}

	if s := saved(); !reflect.DeepEqual(s, a.St.Settings) {
		t.Fatalf("state.json 里的设置 %+v，要 %+v", s, a.St.Settings)
	}

	// 语言：换成英文，两个窗口的文案下一帧都换
	mustClick(t, st, "语言")
	pick("中文", "English")
	st.Frame()
	tt.Frame()
	if i18n.Lang() != "en" || saved().Lang != "en" {
		t.Fatalf("语言 %q", i18n.Lang())
	}
	for _, s := range []string{"Interface language", "Appearance", "Editor"} {
		if !st.HasText(s) {
			t.Errorf("设置窗口里没有 %q：%q", s, st.Texts())
		}
	}
	if !tt.HasText("Spaces: 2") {
		t.Errorf("主窗口状态栏没换成英文：%q", tt.Texts())
	}
}

// 英文界面：主窗口、命令面板、文件树的右键菜单里没有一条界面文案还是中文（仓库里的数据除外）。
func TestEnglishUI(t *testing.T) {
	i18n.Set("en")
	t.Cleanup(func() { i18n.Set("") })
	r0 := testutil.NewRepo(t, "work")
	r0.Write("src/main.go", "package main\n")
	r0.Write("README.md", "# Hello\n")
	r0.Commit("init")
	r0.Write("README.md", "# Hello\n\nchanged\n")
	a, tt, wait := newUI(t, testutil.Workspace(t, r0.Root), "")
	r := a.Repos[0]
	a.St.BottomBar = true
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))
	d := a.OpenFile(r, "src/main.go", true)
	wait("读文件", func() bool { return !d.Loading })

	check := func(where string, texts []string) {
		t.Helper()
		for _, s := range texts {
			// 提交者"测试"是临时仓库的数据，不是界面文案
			if strings.ContainsFunc(strings.ReplaceAll(s, "测试", ""), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Errorf("%s还有中文：%q", where, s)
			}
		}
	}
	a.EnsureFiles(r)
	wait("文件树", func() bool { return idle(r)() && r.Files.Loaded && tt.HasText("README.md") })
	check("主窗口", tt.Texts())
	if !tt.HasText("Filter files") || !tt.HasText("Tab 4") {
		t.Errorf("主窗口没换成英文：%q", tt.Texts())
	}

	mustRightClick := func(s string) {
		t.Helper()
		if err := rightClickRow(tt, s); err != nil { // 文件树的行，见 clickRow
			t.Fatalf("右键 %q: %v", s, err)
		}
		tt.Frame()
		check("右键 "+s+" 的菜单", shownMenu(t, tt))
		if len(shownMenu(t, tt)) == 0 {
			t.Fatalf("右键 %q 没有菜单", s)
		}
		closeMenu(t, tt)
		tt.Frame()
	}
	mustRightClick("src")
	mustRightClick("README.md")

	mustClick(t, tt, "Git")
	wait("Git 栏", func() bool { return tt.HasText("Unstaged") || tt.HasText("Changes") })
	check("Git 栏", tt.Texts())
	mustRightClick("README.md")

	tt.Key(wui.Cmd|wui.Shift, wui.KeyP)
	tt.Frame()
	tt.Type("sett")
	tt.Frame()
	check("命令面板", tt.Texts())
	if !tt.HasText("Settings…") {
		t.Errorf("命令面板里没有 Settings…：%q", tt.Texts())
	}
	tt.Key(0, wui.KeyEscape)
	tt.Frame()
}
