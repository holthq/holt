package ui

import (
	"os"
	"path/filepath"
	"testing"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/testutil"
	"github.com/lemoeade/grove/internal/workspace"
)

// TestScreenshotAppearance 是外观的截图（WORKBENCH_SHOT=目录 时才跑），浅色、深色各一套，临时仓库 shop：
//
//	main             默认钢蓝：文件树（文件夹、改动数）、两个标签（文件、Diff）、settings.json 的说明条、右栏历史（引用、短哈希）
//	git              左栏 Git：改动的仓库行、改动数、行内的增删
//	image            图片的工具栏：“适应”是当前的
//	about            关于窗口（新名字、新图标）
//	settings-accent  设置的外观页：强调色那一项
//	settings-search  设置窗口搜“字号”：命中的名字加粗、不用强调色
func TestScreenshotAppearance(t *testing.T) {
	dir := os.Getenv("WORKBENCH_SHOT")
	if dir == "" {
		t.Skip("设置 WORKBENCH_SHOT=目录 才渲染截图")
	}
	shop := testutil.NewRepo(t, "shop")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\n// main 打个招呼。\nfunc main() {\n\tfmt.Println(\"shop\")\n}\n")
	shop.Write("src/cart/cart.go", "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n")
	shop.Write("README.md", "# Shop\n\n一个小店。\n")
	os.MkdirAll(filepath.Join(shop.Root, "assets"), 0o755)
	writeDisc(t, shop.Root, "assets/photo.png", 480, 320)
	shop.Commit("初始")
	shop.Git("tag", "v0.1.0")
	shop.Write("src/cart/cart.go", "package cart\n\n// Cart 是购物车。\ntype Cart struct{ Items []string }\n\n// Add 放一件。\nfunc (c *Cart) Add(s string) { c.Items = append(c.Items, s) }\n")
	shop.Commit("购物车能放东西")
	shop.Write("src/main.go", "package main\n\nimport \"fmt\"\n\n// main 打个招呼。\nfunc main() {\n\tfmt.Println(\"shop\", 2)\n}\n")
	shop.Write("notes.txt", "待办\n")
	ws := testutil.Workspace(t, shop.Root)
	suffix := func(dark bool) string {
		if dark {
			return "-dark.png"
		}
		return "-light.png"
	}

	fresh := func() (*app.App, *wui.Tester, func(string, func() bool)) {
		a, tt, wait := newUI(t, ws, termState(t, false)) // 底栏关着、shell 是 /bin/sh
		tt.SetScale(2)
		tt.SetTitleBar(shotBar)
		r := a.Repos[0]
		a.RefreshRepo(r, app.WhyManual)
		wait("git status", idle(r))
		return a, tt, wait
	}
	shots := func(tt *wui.Tester, name string) {
		t.Helper()
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			writeShot(t, tt, dir, name+suffix(dark))
		}
	}

	a, tt, wait := fresh()
	if a.St.Settings.Accent != "" || workspace.DefaultAccent != "steel" {
		t.Fatalf("强调色 %q / 默认 %q，该是默认的钢蓝", a.St.Settings.Accent, workspace.DefaultAccent)
	}
	r := a.Repos[0]
	a.ExpandChanged(r)
	a.OpenFile(r, "src/cart/cart.go", true)
	wait("cart.go 打开", func() bool { d := a.CurrentDoc(); return d != nil && d.Buf != nil && !d.Loading })
	a.ShowDiffFor(r, "src/main.go")
	wait("main.go 的 diff", func() bool { d := a.CurrentDoc(); return d != nil && !d.Loading })
	a.Exec("app.settingsJSON")
	if !a.St.RightBar {
		a.Exec("view.right")
	}
	wait("历史", func() bool { return r.Hist.Loaded && !r.Hist.Loading && len(r.Hist.Commits) == 2 })
	shots(tt, "main")

	a.ShowSidebar(workspace.ModeGit)
	shots(tt, "git")

	a, tt, wait = fresh()
	d := a.OpenFile(a.Repos[0], "assets/photo.png", true)
	wait("photo.png", func() bool { return d.Bitmap != nil && !d.Loading })
	shots(tt, "image")

	ab := wui.NewTester(AboutView(a, ""), 460, 600)
	ab.SetScale(2)
	ab.SetLocale(i18n.Locale())
	shots(ab, "about")

	sv := wui.NewTester(SettingsView(a), 640, 560)
	sv.SetScale(2)
	sv.SetLocale(i18n.Locale())
	sv.Frame()
	shots(sv, "settings-accent")
	sv.Key(wui.Cmd, wui.KeyF)
	sv.Type("字号")
	sv.Frame()
	shots(sv, "settings-search")
}
