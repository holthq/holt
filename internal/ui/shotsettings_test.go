package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/testutil"
)

// TestScreenshotSettingsSearch 是设置搜索的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套（-light / -dark），临时仓库 shop：
//
//	settings-search  设置窗口搜“字号”：编辑器、终端两组，名字是强调色
//	settings-json    编辑器区的 settings.json（改了一项没套上）：上面的说明条、状态栏“没应用 · ⌘S”
//	zoom-150         界面缩放 150%（1440×880 的窗口），状态栏右边那一格
//	import-confirm   导入设置的确认：会改几项、忽略了几个键
func TestScreenshotSettingsSearch(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	shop := testutil.NewRepo(t, "shop")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\n// main 打个招呼。\nfunc main() {\n\tfor i := range 3 {\n\t\tfmt.Println(\"shop\", i)\n\t}\n}\n")
	shop.Write("README.md", "# Shop\n\n一个小店。\n")
	shop.Commit("初始")
	suffix := func(dark bool) string {
		if dark {
			return "-dark.png"
		}
		return "-light.png"
	}

	a, tt, wait := newUI(t, testutil.Workspace(t, shop.Root), termState(t, false)) // 底栏关着、shell 是 /bin/sh
	tt.SetScale(2)
	tt.SetTitleBar(shotBar)
	r := a.Repos[0]
	a.RefreshRepo(r, app.WhyManual)
	wait("git status", idle(r))

	sv := wui.NewTester(SettingsView(a), 760, 560)
	sv.SetScale(2)
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	sv.Key(wui.Cmd, wui.KeyF)
	sv.Type("字号")
	sv.Frame()
	for _, dark := range []bool{false, true} {
		sv.SetDark(dark)
		sv.Frame()
		writeShot(t, sv, dir, "settings-search"+suffix(dark))
	}

	shot := func(name string, dark bool) {
		t.Helper()
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame()
		writeShot(t, tt, dir, name+suffix(dark))
	}
	a.OpenFile(r, "src/main.go", true)
	wait("main.go 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil })
	a.Exec("app.settingsJSON")
	d := a.CurrentDoc()
	old := `"softWrap": false`
	i := strings.Index(d.Buf.String(), old)
	d.Buf.Edit(editor.Edit{Start: i, End: i + len(old), Text: `"softWrap": true`})
	for _, dark := range []bool{false, true} {
		shot("settings-json", dark)
	}

	a.Save(d) // 套上（软换行），关的时候不问
	a.CloseDoc(d)
	a.SetZoom(150)
	a.Toast = ""
	for _, dark := range []bool{false, true} {
		shot("zoom-150", dark)
	}
	a.SetZoom(100)
	a.Toast = ""

	pick := filepath.Join(t.TempDir(), "workbench-settings.json")
	if err := os.WriteFile(pick, []byte(`{"workbench": "0.18.1", "settings": {"theme": "dark", "editorSize": 14, "accent": "teal", "termDock": "right", "pullMode": "rebase", "autoPush": true, "inlineBlame": true, "colour": "red"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.OpenDialog = func(mygo.OpenDialogOptions) ([]string, error) { return []string{pick}, nil }
	a.Exec("app.settingsImport")
	wait("导入的确认", func() bool { return a.Confirm != nil })
	for _, dark := range []bool{false, true} {
		shot("import-confirm", dark)
	}
}
