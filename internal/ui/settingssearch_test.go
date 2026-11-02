package ui

import (
	"slices"
	"strings"
	"testing"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// 设置窗口的搜索：⌘F 到搜索框；中文、英文都认，名字、说明、分组名都算；结果按分组分开、组名做小标题；
// 没有对得上的说一句；搜着的时候分组点不动；Esc 清空，回到原来那一组。
func TestSettingsSearch(t *testing.T) {
	t.Cleanup(func() { i18n.Set("") })
	_, ws := testutil.TempRepo(t)
	a, _, _ := newUI(t, ws, "")
	sv := wui.NewTester(SettingsView(a), 640, 560)
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	mustText(t, sv, "界面缩放（%）")
	if sv.Focused("搜索设置") {
		t.Fatal("一打开键盘就在搜索框里")
	}
	sv.Key(wui.Cmd, wui.KeyF)
	sv.Frame()
	if !sv.Focused("搜索设置") {
		t.Fatal("⌘F 没到搜索框")
	}
	search := func(q string) {
		t.Helper()
		sv.Key(wui.Cmd, wui.KeyA)
		sv.Type(q)
		sv.Frame()
		sv.Frame()
	}
	count := func(s string) int {
		n := 0
		for _, x := range sv.Texts() {
			if x == s {
				n++
			}
		}
		return n
	}

	// groups 是结果里有哪几组的小标题（分组的按钮上也有组名，多出来的那个是小标题）
	groups := func(names ...string) bool {
		for _, p := range []string{"外观", "编辑器", "格式化", "文件树", "终端", "Git", "语言", "Appearance", "Editor", "Terminal"} {
			if want := slices.Contains(names, p); count(p) > 1 != want {
				return false
			}
		}
		return true
	}
	// “字号”在编辑器、终端两组里；外观那组的不在了
	search("字号")
	if !groups("编辑器", "终端") || !sv.HasText("字号") || sv.HasText("界面缩放（%）") || sv.HasText("缩进") {
		t.Fatalf("搜“字号”：%q", sv.Texts())
	}
	// 英文也认（界面是中文）；说明里的字也算；分组名也算（整组都出来）
	search("font size")
	if !groups("编辑器", "终端") || !sv.HasText("字号") {
		t.Fatalf("搜 font size：%q", sv.Texts())
	}
	search("回到 100%")
	mustText(t, sv, "界面缩放（%）")
	search("GIT")
	if !groups("文件树", "Git") { // 文件树那组是“按 Git 状态给文件名着色”
		t.Fatalf("搜 GIT：%q", sv.Texts())
	}
	for _, s := range []string{"拉取方式", "提交后推送", "作者邮箱", "按 Git 状态给文件名着色"} {
		mustText(t, sv, s)
	}
	search("格式化命令")
	mustText(t, sv, "格式化命令")
	search("后台")
	mustText(t, sv, "关掉窗口后终端继续在后台运行")

	// 没有对得上的；搜着的时候分组点不动
	search("zzzz")
	mustText(t, sv, "没有匹配的设置")
	sv.Click("终端")
	sv.Frame()
	sv.Key(wui.Cmd, wui.KeyF) // 点了别处，键盘不在框里了
	sv.Key(0, wui.KeyEscape)
	sv.Frame()
	if sv.HasText("没有匹配的设置") || !sv.HasText("界面缩放（%）") {
		t.Fatalf("Esc 没清空、或搜着的时候分组被点走了：%q", sv.Texts())
	}

	// 英文界面里搜中文、英文都行
	a.St.Settings.Lang = "en"
	a.SettingsChanged()
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	sv.Key(wui.Cmd, wui.KeyF)
	search("font size")
	if !groups("Editor", "Terminal") || sv.HasText("Appearance") && count("Appearance") > 1 {
		t.Fatalf("英文界面搜 font size：%q", sv.Texts())
	}
	search("字号")
	if !groups("Editor", "Terminal") {
		t.Fatalf("英文界面搜“字号”：%q", sv.Texts())
	}
	search("nothing like this")
	mustText(t, sv, "No matching settings")
}

// 界面缩放的键：⌘= ⌘- ⌘0 一次走一档、状态栏多一格显示缩放（100% 时没有）；键盘在终端里也一样，
// 终端自带的字号键（含 ⇧⌘=）让给界面缩放，不会一下走两档。
func TestZoomKeys(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	a, tt, wait := termUI(t, ws, termState(t, true))
	wait("读完状态", loaded(a))
	tt.Frame()
	chip := func(p int) bool { _, ok := tt.Find("界面缩放 " + itoa(p) + "%"); return ok }
	if a.Zoomed() || chip(100) {
		t.Fatal("一开始就有缩放")
	}
	key := func(mods wui.Modifiers, k wui.Key, want int) {
		t.Helper()
		tt.Key(mods, k)
		wait("缩放", func() bool { return a.St.Settings.ZoomPercent() == want })
		tt.Frame()
		tt.Frame()
		if got := a.St.Settings.ZoomPercent(); got != want || (want != 100) != chip(want) {
			t.Fatalf("按键后 %d%%（状态栏那格 %v），要 %d%%", got, chip(want), want)
		}
	}
	key(wui.Cmd, wui.KeyEqual, 110)
	key(wui.Cmd, wui.KeyEqual, 125)
	key(wui.Cmd, wui.KeyMinus, 110)
	key(wui.Cmd, wui.Key0, 100)

	// 键盘在终端里
	p := readyPane(t, a, tt)
	key(wui.Cmd, wui.KeyEqual, 110)
	key(wui.Cmd|wui.Shift, wui.KeyEqual, 125)
	key(wui.Cmd, wui.KeyMinus, 110)
	if !a.TermFocused {
		t.Fatal("缩放把键盘从终端拿走了")
	}
	typeLine(t, tt, p, "echo zoom-$((5*5))", "zoom-25")
	key(wui.Cmd, wui.Key0, 100)

	// 状态栏那格的菜单换一档
	key(wui.Cmd, wui.KeyEqual, 110)
	if err := tt.Click("界面缩放 110%"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("150%"); err != nil {
		t.Fatalf("缩放菜单：%v %q", err, tt.Menu())
	}
	tt.Frame()
	if a.St.Settings.Zoom != 150 || !chip(150) {
		t.Fatalf("菜单选 150%% 后 %d", a.St.Settings.Zoom)
	}

	// 设置窗口、关于窗口每帧自己设一次（mygo 的缩放按窗口）：布局用的宽是窗口的 1/1.5
	for name, view := range map[string]func(*wui.Context){"设置窗口": SettingsView(a), "关于窗口": AboutView(a, "")} {
		var z, w float32
		st := wui.NewTester(func(c *wui.Context) {
			view(c)
			z = c.Zoom()
			w, _ = c.Size()
		}, 600, 480)
		st.Frame()
		if z != 1.5 || w != 400 {
			t.Errorf("%s的缩放 %v、布局宽 %v，要 1.5、400", name, z, w)
		}
	}
}

// 设置文档（settings.json）：命令打开在编辑器区（全部键），上面一条说明和“恢复成现在的设置”；改了状态栏说没应用，
// ⌘S 套上（提示改了几项、存进 state.json，内容换成规范格式）；写错了报第几行第几列、光标跳过去、什么都不改；
// 改过的“恢复成现在的设置”先问；没改过的跟着别处改的设置变（⌘=）；⌥⌘S 也是套上；关掉不进 ⇧⌘T。
func TestSettingsDoc(t *testing.T) {
	_, ws := testutil.TempRepo(t)
	stateDir := t.TempDir()
	a, tt, wait := newUI(t, ws, stateDir)
	a.Exec("app.settingsJSON")
	tt.Frame()
	d := a.CurrentDoc()
	if d == nil || d.Kind != docs.SettingsDoc || d.Title != "settings.json" || d.Modified() || !a.FocusDoc && !tt.Focused("settings.json") {
		t.Fatalf("app.settingsJSON 开的是 %+v", d)
	}
	text := d.Buf.String()
	if !strings.Contains(text, "\"editorSize\": 0,") || strings.Count(text, "\n  \"") != len(workspace.SettingsKeys()) {
		t.Fatalf("设置文档的内容：\n%s", text)
	}
	mustText(t, tt, "恢复成现在的设置")
	mustText(t, tt, "和现在的设置一样")
	replace := func(old, new string) {
		t.Helper()
		i := strings.Index(d.Buf.String(), old)
		if i < 0 {
			t.Fatalf("设置文档里没有 %q", old)
		}
		d.Buf.Edit(editor.Edit{Start: i, End: i + len(old), Text: new})
		tt.Frame()
	}
	toasted := func(s string) bool { return a.Toast == s || tt.HasText(s) } // 画一帧提示就交给窗口了
	save := func() {
		t.Helper()
		a.Toast, a.AlertOpen = "", false
		tt.Frame() // 上一次的出错框关掉（开着时它接走 ⌘S）
		tt.Key(wui.Cmd, wui.KeyS)
		tt.Frame()
	}

	// 改两项，⌘S 套上
	replace(`"theme": ""`, `"theme": "dark"`)
	replace(`"softWrap": false`, `"softWrap": true`)
	mustText(t, tt, "没应用 · ⌘S")
	save()
	if s := a.St.Settings; s.Theme != "dark" || !s.SoftWrap || d.Modified() || !toasted("已应用 2 项设置") {
		t.Fatalf("⌘S 后：主题 %q 软换行 %v 改过 %v 提示 %q", s.Theme, s.SoftWrap, d.Modified(), a.Toast)
	}
	if st, _ := workspace.LoadState(stateDir); st.Settings.Theme != "dark" {
		t.Fatal("套上的设置没存进 state.json")
	}
	// Clamp：读不懂的值换回默认，内容也换成规范的
	replace(`"editorSize": 0`, `"editorSize": 99`)
	save()
	if a.St.Settings.EditorSize != 0 || !strings.Contains(d.Buf.String(), `"editorSize": 0,`) || !toasted("设置没变") || d.Modified() {
		t.Fatalf("超出范围的字号：%v %q", a.St.Settings.EditorSize, a.Toast)
	}

	// 写错了：不认识的键、类型不对、语法错，报行列、光标跳过去、设置不动
	before := a.St.Settings
	replace("{\n", "{\n  \"colour\": 1,\n")
	save()
	if !a.AlertOpen || a.AlertTitle != "settings.json 不对" || a.AlertMsg != "第 2 行第 3 列：不认识的键“colour”" {
		t.Fatalf("不认识的键：%v %q %q", a.AlertOpen, a.AlertTitle, a.AlertMsg)
	}
	if pos := d.St.CursorPos(); pos.Line != 1 || pos.Col != 2 || !d.Modified() || len(workspace.ChangedKeys(before, a.St.Settings)) != 0 {
		t.Fatalf("光标 %+v，改过 %v，设置变了 %v", pos, d.Modified(), workspace.ChangedKeys(before, a.St.Settings))
	}
	replace("  \"colour\": 1,\n", "")
	replace(`"scrollback": 0`, `"scrollback": "很多"`)
	save()
	if !a.AlertOpen || !strings.HasSuffix(a.AlertMsg, "“scrollback”要的是数字，写的是字符串") {
		t.Fatalf("类型不对：%q", a.AlertMsg)
	}

	// 改过的“恢复成现在的设置”先问（红的），确认了换回
	a.AlertOpen = false
	tt.Frame()
	mustClick(t, tt, "恢复成现在的设置")
	if a.Confirm == nil || !a.Confirm.Danger || a.Confirm.Title != "丢掉设置文档里的改动？" {
		t.Fatalf("改过的恢复没问：%+v", a.Confirm)
	}
	mustClick(t, tt, "丢掉改动")
	if d.Modified() || !strings.Contains(d.Buf.String(), `"scrollback": 0,`) || !toasted("已换回现在的设置") {
		t.Fatalf("恢复后：改过 %v 提示 %q", d.Modified(), a.Toast)
	}

	// 没改过的跟着 ⌘= 变；⌥⌘S 也是套上
	tt.Key(wui.Cmd, wui.KeyEqual)
	wait("缩放", func() bool { return a.St.Settings.Zoom == 110 })
	if !strings.Contains(d.Buf.String(), `"zoom": 110,`) || d.Modified() {
		t.Fatal("没改过的设置文档没跟上 ⌘=")
	}
	replace(`"zoom": 110`, `"zoom": 150`)
	a.Toast = ""
	tt.Key(wui.Alt|wui.Cmd, wui.KeyS)
	tt.Frame()
	if a.St.Settings.Zoom != 150 || d.Modified() {
		t.Fatalf("⌥⌘S 没套上：%d %v", a.St.Settings.Zoom, d.Modified())
	}

	// 再开一次是同一个；关掉不进 ⇧⌘T
	a.Exec("app.settingsJSON")
	n := 0
	for _, x := range a.AllDocs() {
		if x.Kind == docs.SettingsDoc {
			n++
		}
	}
	if n != 1 || a.CurrentDoc() != d {
		t.Fatalf("再开一次：%d 个设置文档", n)
	}
	a.CloseDoc(d)
	tt.Frame()
	a.Exec("file.reopen")
	tt.Frame()
	if cur := a.CurrentDoc(); cur != nil && cur.Kind == docs.SettingsDoc {
		t.Fatal("⇧⌘T 把设置文档开回来了")
	}
}
