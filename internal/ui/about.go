package ui

import (
	"path/filepath"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/appicon"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/version"
)

// thirdParty 是打包进应用的第三方组件和它们的许可证；许可证全文在资源目录的 licenses/ 里。
var thirdParty = [][2]string{
	{"mygo", "MIT"},
	{"libghostty-vt（Ghostty）", "MIT"},
	{"JetBrains Mono Nerd Font", "SIL OFL 1.1"},
	{"Go、golang.org/x", "BSD-3-Clause"},
	{"go-text/typesetting", "BSD-3-Clause / Unlicense"},
	{"purego", "Apache-2.0"},
	{"regexp2", "MIT"},
	{"Mermaid", "MIT"},
	{"MathJax", "Apache-2.0"},
}

// AboutView 是关于窗口：图标、版本、编进来的 mygo 和 Go 的版本、日志目录（崩溃和错误日志）、第三方许可。
// licenses 是许可证全文所在的目录（应用资源目录的 licenses/）。
func AboutView(a *app.App, licenses string) func(c *wui.Context) {
	var icon *wui.Bitmap // 第一次画时现画，256 像素显示成 88
	return func(c *wui.Context) {
		t := c.Theme()
		applyAccent(a, t)
		c.SetZoom(a.ZoomFactor())
		c.Root().Background(t.Background)
		if icon == nil {
			icon = wui.NewBitmap(appicon.Draw(256))
		}
		show := func(p string) {
			if err := a.ShowPath(p); err != nil {
				a.Fail(i18n.T("打不开"), err)
			}
		}
		wui.Scroll(c).Fill().Children(func() {
			wui.Column(c).Padding(24, 32, 24).Gap(6).AlignItems(wui.Center).Children(func() {
				wui.Image(c, icon).Size(88, 88).Label(i18n.T("Grove 图标"))
				wui.Text(c, "Grove").FontSize(20).FontWeight(600).Margin(6, 0, 0, 0)
				wui.Text(c, i18n.Tf("版本 %s", version.App)).FontSize(12.5).TextColor(t.TextMuted)
				wui.Text(c, i18n.T("多仓库工作台：文件树、编辑器、按仓库的 Git 审阅和终端，所有仓库同时可见。")).
					FontSize(12).TextColor(t.TextMuted).TextAlign(wui.Center).Margin(4, 0, 10, 0)

				wui.Column(c).Gap(4).Padding(10, 14).Radius(8).Background(t.Surface).Selectable().Children(func() {
					aboutRow(c, "mygo", version.Mygo())
					aboutRow(c, "Go", version.Go()+" · "+version.Platform())
					dir := a.LogDir
					if dir == "" {
						dir = "—"
					}
					aboutRow(c, i18n.T("日志"), dir)
				})
				wui.Row(c).Gap(8).Margin(8, 0, 0, 0).Children(func() {
					if wui.Button(c, i18n.T("打开日志目录")).Disabled(a.LogDir == "").
						Tooltip(i18n.T("崩溃日志（crash-….log）和错误日志（workbench.log）在这里")).Clicked() {
						show(a.LogDir)
					}
					if a.LastCrash != "" && wui.Button(c, i18n.T("上次的崩溃日志")).Tooltip(filepath.Base(a.LastCrash)).Clicked() {
						show(a.LastCrash)
					}
				})

				wui.Text(c, i18n.T("许可")).FontSize(12).FontWeight(600).Margin(16, 0, 2, 0)
				wui.Column(c).Gap(3).Children(func() {
					for _, p := range thirdParty {
						wui.Row(c).Gap(8).Children(func() {
							wui.Text(c, p[0]).FontSize(11.5).MinWidth(190).TextAlign(wui.End)
							wui.Text(c, p[1]).FontSize(11.5).TextColor(t.TextMuted).MinWidth(190)
						})
					}
				})
				if wui.Button(c, i18n.T("查看许可证全文")).Margin(8, 0, 0, 0).Disabled(licenses == "").Clicked() {
					show(licenses)
				}
			})
		})
	}
}

// aboutRow 是关于窗口里一行"名字　值"。
func aboutRow(c *wui.Context, name, value string) {
	t := c.Theme()
	wui.Row(c).Gap(10).Children(func() {
		wui.Text(c, name).FontSize(11.5).TextColor(t.TextMuted).MinWidth(44).Shrink(0)
		wui.Text(c, value).Font(mono()).FontSize(11.5).MinWidth(0)
	})
}
