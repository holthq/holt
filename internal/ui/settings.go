package ui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/fonts"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/snippets"
	"github.com/lemoeade/grove/internal/workspace"
)

// 设置窗口里每一项可选的值（state.json 里存的）对应的名字；显示时过 i18n.T。
var (
	themeNames      = map[string]string{"": "跟随系统", "light": "浅色", "dark": "深色"}
	backdropNames   = map[string]string{"": "柔和", "flat": "纯色", "vibrancy": "通透"}
	materialNames   = map[string]string{"": "侧栏", "under-window": "窗口下", "popover": "弹出层", "menu": "菜单", "glass": "玻璃"}
	accentNames     = withDefault(map[string]string{"system": "跟随系统", "steel": "钢蓝", "ink": "墨蓝", "graphite": "石墨", "teal": "青绿", "indigo": "靛紫"}, workspace.DefaultAccent)
	indentNames     = map[string]string{"": "按语言", "tab": "Tab", "2": "2 个空格", "4": "4 个空格", "8": "8 个空格"}
	whitespaceNames = map[string]string{"": "只在选中的文字里", "all": "全部", "trailing": "只在行尾", "none": "不显示"}
	autoSaveNames   = map[string]string{"": "关", "blur": "窗口切走时", "delay": "停手 1 秒后"}
	termThemeNames  = map[string]string{"": "跟随外观", "light": "浅色", "dark": "深色"}
	termDockNames   = map[string]string{"": "底栏", "right": "右边"}
	pullModeNames   = map[string]string{"": "只快进（--ff-only）", "rebase": "变基（--rebase）", "merge": "合并（--no-rebase）"}
	langNames       = map[string]string{"": "中文", "en": "English"}
	iconStyleNames  = map[string]string{"": "彩色底块", "plain": "线形"}
	webKindNames    = map[string]string{"github": "GitHub", "gitlab": "GitLab", "bitbucket": "Bitbucket", "gitea": "Gitea / Forgejo", "generic": "通用（GitHub 的路径）"}
)

// settingsPages 是设置窗口顶上的分组。
var settingsPages = []string{"外观", "编辑器", "格式化", "文件树", "终端", "Git", "片段", "语言", "快捷键"}

// SettingsView 是设置窗口（⌘,）的内容：顶上分组和搜索框（⌘F 到框里，Esc 清空），下面这一组的设置；搜着的时候是
// 所有组里对得上的几项，按组分开。改了马上生效（主窗口、开着的终端跟着变），并存进 state.json。
// 右上角 ⋯ 是编辑 JSON、导出、导入、恢复默认。最后一页是快捷键（keybind.go），搜索时对得上的命令也列出来。
func SettingsView(a *app.App) func(c *wui.Context) {
	var page int
	var query string      // 搜索框里的字（不记：关了设置窗口再开是空的）
	var box wui.Handle    // 搜索框
	var families []string // 等宽字体，第一次画字体那一项时读
	var themes []string   // 终端配色：跟随外观 / 浅 / 深，然后是 Ghostty 的主题（第一次画那一项时读）
	var profEdit []profileEdit
	var keys keysView // 快捷键页
	return func(c *wui.Context) {
		t := c.Theme()
		applyAccent(a, t)
		c.SetZoom(a.ZoomFactor())
		c.Root().Background(t.Background)
		s := &a.St.Settings
		done := a.SettingsChanged
		mono := func() []string {
			if families == nil {
				families = []string{""}
				for _, f := range wui.FontFamilies() {
					if f.Monospaced && f.Name != fonts.Mono() {
						families = append(families, f.Name)
					}
				}
			}
			return families
		}
		termThemes := func() []string {
			if themes == nil {
				themes = append(slices.Clone(workspace.TermThemes), terminal.GhosttyThemes()...)
			}
			return themes
		}
		termThemeName := func(x string) string {
			if n, ok := termThemeNames[x]; ok {
				return i18n.T(n)
			}
			return x // Ghostty 的主题名不翻译
		}
		fontName := func(f string) string {
			if f == "" {
				return i18n.Tf("默认（%s）", fonts.Mono())
			}
			return f
		}
		// 回到工作台窗口（编辑 JSON、恢复默认的确认都在那里）
		toMain := func() {
			if a.Win != nil {
				a.Win.Focus()
			}
		}
		pages := make([]string, len(settingsPages))
		for i, p := range settingsPages {
			pages[i] = i18n.T(p)
		}
		// 设置窗口里的每一项：分在哪一组、名字和说明（中文原文，搜索时中英文都认），怎么画（拿到的名字、说明已经过 i18n.T）。
		// 存的是反过来的那几个开关（零值 = 默认开）在画的时候翻过来。
		rows := []settingRow{
			{0, "外观", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.Theme, workspace.Themes, themeNames, done) }},
			{0, "窗口底", "柔和是纯色底上几团淡淡的颜色；纯色一色到底；通透透出桌面的系统材质，稍费内存", func(c *wui.Context, l, d string) {
				choice(c, l, &s.Backdrop, workspace.Backdrops, backdropNames, done).Description(d)
			}},
			{0, "材质", "透出桌面的是哪种系统材质，改了马上换；玻璃要 macOS 26，更早的系统里像弹出层", func(c *wui.Context, l, d string) {
				choice(c, l, &s.Material, workspace.Materials, materialNames, done).Disabled(s.Backdrop != "vibrancy").Description(d)
			}},
			{0, "强调色", "按钮、选中的行、焦点环和选中的文字用的颜色；跟随系统是系统设置里的强调色", func(c *wui.Context, l, d string) {
				choice(c, l, &s.Accent, workspace.Accents, accentNames, done).Description(d)
			}},
			{0, "选中行用淡底", "列表里选中的行是强调色淡淡的一层、左边一道竖条，字不换色；关着是强调色的实心块", func(c *wui.Context, l, d string) {
				soft := !s.SolidSelection
				toggle(c, l, d, &soft, func() {
					s.SolidSelection = !soft
					done()
				})
			}},
			{0, "界面缩放（%）", "放大、缩小、回到 100% 的快捷键；所有窗口一起", func(c *wui.Context, l, _ string) {
				zoom := float32(s.ZoomPercent())
				number(c, l, &zoom, 100, 50, 300, 5, func() { a.SetZoom(int(zoom)) }).Description(zoomDesc())
			}},
			{0, "Markdown 预览里渲染 Mermaid 图和公式", "关了只显示代码块和公式的 TeX 原文；渲染用一个看不见的网页视图，60 秒不用就关掉", func(c *wui.Context, l, d string) {
				on := !s.NoDiagrams
				toggle(c, l, d, &on, func() {
					s.NoDiagrams = !on
					done()
				})
			}},
			{1, "字体", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.EditorFont, mono(), nil, done, fontName) }},
			{1, "字号", "", func(c *wui.Context, l, _ string) { number(c, l, &s.EditorSize, 12.5, 9, 32, 0.5, done) }},
			{1, "缩进", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.Indent, workspace.Indents, indentNames, done) }},
			{1, "软换行", "长行折到窗口宽度", func(c *wui.Context, l, d string) { toggle(c, l, app.Tip(d, "view.softWrap"), &s.SoftWrap, done) }},
			{1, "粘性滚动", "滚动时把当前函数 / 类 / 标题吸在编辑器顶上", func(c *wui.Context, l, d string) {
				sticky := !s.NoStickyScroll
				toggle(c, l, d, &sticky, func() {
					s.NoStickyScroll = !sticky
					done()
				})
			}},
			{1, "显示空白字符", "", func(c *wui.Context, l, _ string) {
				choice(c, l, &s.Whitespace, workspace.Whitespaces, whitespaceNames, done)
			}},
			{1, "自动保存", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.AutoSave, workspace.AutoSaves, autoSaveNames, done) }},
			{1, "保存时去掉行尾空白", "光标所在的行不动；Markdown 里正好两个空格的硬换行留着", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.TrimTrailing, done)
			}},
			{1, "保存时补上末尾换行", "", func(c *wui.Context, l, d string) { toggle(c, l, d, &s.FinalNewline, done) }},
			{1, "本地历史", "每次保存在状态目录里留一份（大于 256 KB、二进制、图片、隐藏的文件不记）；文件历史里和提交按时间排在一起，能比较、恢复。删掉的文件用“从本地历史恢复文件…”拿回来", func(c *wui.Context, l, d string) {
				on := !s.NoLocalHistory // 存的是反面（零值 = 记）
				toggle(c, l, d, &on, func() {
					s.NoLocalHistory = !on
					done()
				})
			}},
			{1, "本地历史保留", "", func(c *wui.Context, l, _ string) {
				days := strconv.Itoa(s.LocalHistoryDays())
				opts := make([]string, len(workspace.HistoryDayOpts))
				for i, n := range workspace.HistoryDayOpts {
					opts[i] = strconv.Itoa(n)
				}
				choice(c, l, &days, opts, nil, func() {
					s.HistoryDays, _ = strconv.Atoi(days)
					done()
				}, func(v string) string { return i18n.Tf("%s 天", v) }).Disabled(s.NoLocalHistory)
				localUsageLine(a, c)
			}},
			{2, "保存时格式化", "保存时先跑这种语言的格式化命令再存；失败了照样存，提示一次", func(c *wui.Context, l, _ string) {
				toggle(c, l, formatOnSaveDesc(), &s.FormatOnSave, done)
			}},
			// 格式化表算一项（名字不画：每行是语言名），说明是这一页底下那段
			{2, "格式化命令", formatterFoot, func(c *wui.Context, _, _ string) {
				formatterTable(a, s).draw(c, done)
			}},
			// 检查：画在格式化那段说明后面另起的一组（afterFoot）
			{2, "保存时检查", "保存后跑这种语言的检查命令，问题标进编辑器（波浪线）、列进底栏的问题面板；没装、失败提示一次", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.LintOnSave, done)
			}},
			{2, "检查命令", lintFoot, func(c *wui.Context, _, _ string) {
				linterTable(a, s).draw(c, done)
			}},
			{3, "文件树跟随当前文件", "换了标签，文件树展开到那个文件、选中它（不抢焦点）；关着时用文件树头上的“定位当前文件”", func(c *wui.Context, l, d string) {
				follow := !s.TreeNoFollow
				toggle(c, l, app.Tip(d, "file.revealCurrent"), &follow, func() {
					s.TreeNoFollow = !follow
					done()
				})
			}},
			{3, "合并只有一个子目录的目录", "只有一个子目录、没有文件的目录跟它的子目录显示成一行，如 internal / ui", func(c *wui.Context, l, d string) {
				compact := !s.TreeNoCompact
				toggle(c, l, d, &compact, func() {
					s.TreeNoCompact = !compact
					done()
				})
			}},
			{3, "缩进参考线", "每一层缩进画一道细竖线，光标所在的那一层深一点", func(c *wui.Context, l, d string) {
				guides := !s.TreeNoGuides
				toggle(c, l, d, &guides, func() {
					s.TreeNoGuides = !guides
					done()
				})
			}},
			{3, "按 Git 状态给文件名着色", "改了的黄、新的和没跟踪的绿、删了的灰加删除线、冲突的红；关着时只有名字后面的字母带色", func(c *wui.Context, l, d string) {
				colors := !s.TreeNoStatusColors
				toggle(c, l, d, &colors, func() {
					s.TreeNoStatusColors = !colors
					done()
				})
			}},
			{3, "图标风格", "彩色底块是类别色的淡色圆角块上一个同色的图标或语言字母，目录按名字上色；线形是单色的线框图标。文件树、标签、快速打开、搜索结果都跟着换", func(c *wui.Context, l, d string) {
				style := "" // 存的是 treeIconsPlain（零值 = 默认的彩色底块）
				if s.TreeIconsPlain {
					style = "plain"
				}
				choice(c, l, &style, []string{"", "plain"}, iconStyleNames, func() {
					s.TreeIconsPlain = style == "plain"
					done()
				}).Description(d)
			}},
			{3, "显示被忽略的文件", "被 .gitignore 忽略的文件和目录灰显在文件树里；搜索和快速打开仍不含它们", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.ShowIgnored, done)
			}},
			{3, "显示打开的编辑器", "文件树上面一段，所有编辑器组的标签各一行；拆分了按组分开。视图菜单里也能开关", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.ShowOpenEditors, done)
			}},
			{3, "隐藏的文件", "glob，逗号分开，从每个仓库的根算起（**/ 开头的哪一级都算）；和工作区文件、仓库 .vscode/settings.json 的 files.exclude 并在一起。文件树、快速打开、搜索都看不到，Git 栏的改动照常列", func(c *wui.Context, l, d string) {
				settingField(c, l, func() {
					if wui.TextInput(c, &s.Exclude).Width(260).Placeholder("**/node_modules, **/*.log").Changed() {
						done()
					}
				}).Description(d)
			}},
			{4, "Shell", "默认是 $SHELL；新开的终端生效，格式化命令也用它", func(c *wui.Context, l, d string) { text(c, l, d, &s.Shell, done) }},
			{4, "其他配置", termProfilesDesc, func(c *wui.Context, _, _ string) { termProfilesField(c, s, &profEdit, done) }},
			{4, "字体", "", func(c *wui.Context, l, _ string) {
				choice(c, l, &s.TermFont, append([]string{""}, mono()[1:]...), nil, done, func(f string) string {
					if f == "" {
						return i18n.T("跟编辑器一样")
					}
					return f
				})
			}},
			{4, "字号", "", func(c *wui.Context, l, _ string) { number(c, l, &s.TermSize, 12.5, 9, 32, 0.5, done) }},
			{4, "配色", "", func(c *wui.Context, l, _ string) {
				choice(c, l, &s.TermTheme, termThemes(), termThemeNames, done, termThemeName)
			}},
			{4, "回滚（MB）", "", func(c *wui.Context, l, _ string) {
				scrollback := float32(s.Scrollback)
				number(c, l, &scrollback, 10, 1, 1000, 1, func() {
					s.Scrollback = int(scrollback)
					done()
				})
			}},
			{4, "光标闪烁", "默认不闪（闪烁的光标让窗口一直在画，多占内存）；新开的终端生效", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.TermBlink, done)
			}},
			{4, "⌥ 作为 Meta 键", "⌥ 加一个键发 Esc 加那个键（Emacs、shell 里的 ⌥B / ⌥F 跳词），⌥ 就打不出特殊字符了；新开的终端生效", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.TermOptionAsAlt, done)
			}},
			{4, "选中即复制", "在终端里拖选文字，松手就复制到剪贴板", func(c *wui.Context, l, d string) { toggle(c, l, d, &s.TermCopyOnSelect, done) }},
			{4, "位置", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.TermDock, workspace.TermDocks, termDockNames, done) }},
			{4, "关掉窗口后终端继续在后台运行", termdDesc, func(c *wui.Context, l, _ string) {
				toggle(c, l, i18n.Tf(termdDesc, termdMB), &s.TermDaemon, func() {
					done()
					a.TermDaemonChanged()
				})
			}},
			{4, "后台", "终端后台进程在不在跑、几个会话；停止…", func(c *wui.Context, l, _ string) { termdField(a, c, l) }},
			{4, "agent 命令", "终端前台是这些命令时，标签上标出 agent 在等你（响铃、通知）或运行中；claude、codex、gemini、aider、opencode 不用写。逗号分开", func(c *wui.Context, l, d string) {
				settingField(c, l, func() {
					if wui.TextInput(c, &s.AgentNames).Width(260).Placeholder("goose, amp").Changed() {
						done()
					}
				}).Description(d)
			}},
			{5, "拉取方式", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.PullMode, workspace.PullModes, pullModeNames, done) }},
			{5, "提交后推送", "提交成功后马上推送当前分支（默认关）", func(c *wui.Context, l, d string) { toggle(c, l, d, &s.AutoPush, done) }},
			{5, "在 Git 栏显示工作树", "工作树组、顶上的“需要你”条和段头的工作树数；关着时都不出现，也不再盯各工作树的目录（默认关）", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.ShowWorktrees, done)
			}},
			{5, "在 Git 栏显示贮藏", "贮藏组和段头的贮藏数；关着时贮藏还在，贮藏、弹出贮藏照常能用（默认关）", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.ShowStashes, done)
			}},
			{5, "行内 blame", "光标所在行的行尾显示作者、多久前和提交摘要（改了没保存时不显示）", func(c *wui.Context, l, d string) {
				toggle(c, l, d, &s.InlineBlame, done)
			}},
			{5, "作者名字", "名字和邮箱都填了才用，否则用 git 自己的配置", func(c *wui.Context, l, d string) { text(c, l, d, &s.AuthorName, done) }},
			{5, "作者邮箱", "", func(c *wui.Context, l, d string) { text(c, l, d, &s.AuthorEmail, done) }},
			{5, "网页风格", webHostsDesc, func(c *wui.Context, _, _ string) {
				webHostsField(a, c, s, done)
			}},
			{6, "片段目录", snippetDirDesc, func(c *wui.Context, l, d string) { snippetDirField(a, c, l, d) }},
			{7, "界面语言", "", func(c *wui.Context, l, _ string) { choice(c, l, &s.Lang, i18n.Langs, langNames, done) }},
		}
		q := strings.ToLower(strings.TrimSpace(query))
		c.OnShortcut(wui.Cmd, wui.KeyF, func() { box.Focus(c) })
		wui.Column(c).Fill().Children(func() {
			wui.Row(c).Padding(14, 20, 12).Gap(10).Shrink(0).AlignItems(wui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				wui.Segmented(c, &page, pages...).Label(i18n.T("设置分组")).Disabled(q != "").Shrink(0)
				wui.Box(c).Grow(1)
				wui.Row(c).Width(200).Shrink(1).MinWidth(120).Children(func() {
					filterBox(c, &query, &box, i18n.T("搜索设置"), i18n.T("搜索设置"), nil).Tooltip(i18n.T("在所有分组里找设置，名字、说明都算，中英文都行（⌘F）"))
				})
				iconButton(c, iconMore, i18n.T("更多"), func() {}).Menu(func(m *wui.Menu) {
					if m.Item(i18n.T("编辑 JSON…")).Chosen() {
						a.Update(a.OpenSettingsDoc)
						toMain()
					}
					m.Separator()
					if m.Item(i18n.T("导出…")).Chosen() {
						a.ExportSettings(a.SettingsWin)
					}
					if m.Item(i18n.T("导入…")).Chosen() {
						a.ImportSettings(a.SettingsWin)
					}
					m.Separator()
					if m.Item(i18n.T("恢复默认…")).Chosen() {
						toMain()
						a.Update(a.ResetSettings)
					}
				})
			})
			if page == keysPage && q == "" {
				shortcutsPage(a, c, &keys, toMain)
				return
			}
			wui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
				wui.Column(c).Padding(20, 28, 28).Gap(14).Children(func() {
					if q != "" {
						found := settingsResults(c, rows, q)
						if keyResults(a, c, q, &keys.rec, func() { page, keys.query, query = keysPage, query, "" }) {
							found = true
						}
						if !found {
							wui.Text(c, i18n.T("没有匹配的设置")).FontSize(12.5).TextColor(t.TextMuted).AlignSelf(wui.Center).Margin(24, 0, 0, 0)
						}
						return
					}
					form := func(after bool) {
						wui.Form(c, func() {
							for i, r := range rows {
								if r.Page == page && afterFoot[r.Label] == after {
									r.Draw(c.Key(i), i18n.T(r.Label), i18n.T(r.Desc))
								}
							}
						})
					}
					form(false)
					switch page {
					case 2:
						wui.Text(c, formatterFootText()).FontSize(11.5).TextColor(t.TextMuted)
						form(true)
						wui.Text(c, lintFootText()).FontSize(11.5).TextColor(t.TextMuted)
					case 4:
						wui.Text(c, i18n.T("Shell、回滚大小、光标闪烁、⌥ 作为 Meta 键和后台运行新开的终端才用（开着的按“重新启动终端”）；字体、配色和选中即复制马上换。")).FontSize(11.5).TextColor(t.TextMuted)
					case 6:
						snippetTables(a, c)
					}
				})
			})
		})
		keyRecorder(a, c, &keys.rec)
	}
}

// settingRow 是设置窗口里的一项（SettingsView 的表）：Label、Desc 是中文原文（显示时过 i18n.T；搜索时中文、英文都认），
// Draw 画这一项，拿到的名字、说明已经过 i18n.T。一项可以画好几行（格式化表、其他配置、网页风格）。
type settingRow struct {
	Page        int
	Label, Desc string
	Draw        func(c *wui.Context, label, desc string)
}

// afterFoot 是画在这一页底下那段说明后面、另起一组的几项（格式化页的检查两项）：说明各管各的那张表。
var afterFoot = map[string]bool{"保存时检查": true, "检查命令": true}

// 说明里讲到快捷键的几项：Desc 是不带键的一句（给搜索用），画的时候拼上命令现在的键（app.Tip / 下面几个），
// 改过的键跟着变、没绑键时不剩空括号。

// zoomDesc 是“界面缩放”的说明：放大 / 缩小 / 实际大小现在的键（zoomKeys），没绑键的不列。
func zoomDesc() string {
	if keys := zoomKeys(); len(keys) > 0 {
		return i18n.Tf("%s；所有窗口一起", i18n.List(keys))
	}
	return i18n.T("所有窗口一起")
}

// formatOnSaveDesc 是“保存时格式化”的说明：保存、全部保存现在的键。
func formatOnSaveDesc() string {
	if k := app.KeyList("file.save", "file.saveAll"); k != "" {
		return i18n.Tf("%s 先跑这种语言的格式化命令再存；失败了照样存，提示一次", k)
	}
	return i18n.T("保存时先跑这种语言的格式化命令再存；失败了照样存，提示一次")
}

// formatterFootText 是格式化页底下那段：手动格式化的键跟着 editor.format。
func formatterFootText() string {
	return app.KeyHint("editor.format", "命令从标准输入读文件内容、把格式化好的写到标准输出，用 login shell 在仓库根目录（仓库外的文件：文件所在的目录）跑，10 秒超时；{file} 换成文件的完整路径。空着用内置默认（灰字），写 - 就不格式化这种语言。手动格式化：%s 或编辑器右键。",
		"命令从标准输入读文件内容、把格式化好的写到标准输出，用 login shell 在仓库根目录（仓库外的文件：文件所在的目录）跑，10 秒超时；{file} 换成文件的完整路径。空着用内置默认（灰字），写 - 就不格式化这种语言。手动格式化：编辑器右键。")
}

// lintFootText 是检查那组底下那段：手动检查的键跟着 lint.run（默认没有键）。
func lintFootText() string {
	return app.KeyHint("lint.run", "命令读磁盘上存好的文件，用 login shell 在仓库根目录（仓库外的文件：文件所在的目录）跑，60 秒超时；输出（标准输出、标准错误都看）里认 路径:行:列: 信息 和 tsc 的 路径(行,列): error TS…: 信息 两种行，error / warning / note 这些级别词认得，没写算错误。{file} 换成文件的完整路径（只换掉这个文件的问题），{dir} 换成文件所在的目录（仓库里的路径，根目录是 .；换掉这个目录的），都不带时换掉这个仓库这种语言的全部。空着用内置默认（灰字），写 - 就不检查这种语言。手动检查：%s、编辑器右键“检查”或问题面板头上的按钮。",
		"命令读磁盘上存好的文件，用 login shell 在仓库根目录（仓库外的文件：文件所在的目录）跑，60 秒超时；输出（标准输出、标准错误都看）里认 路径:行:列: 信息 和 tsc 的 路径(行,列): error TS…: 信息 两种行，error / warning / note 这些级别词认得，没写算错误。{file} 换成文件的完整路径（只换掉这个文件的问题），{dir} 换成文件所在的目录（仓库里的路径，根目录是 .；换掉这个目录的），都不带时换掉这个仓库这种语言的全部。空着用内置默认（灰字），写 - 就不检查这种语言。手动检查：编辑器右键“检查”、命令面板“检查当前文件”或问题面板头上的按钮。")
}

// 几项的说明在别处也要用（这一页底下那段、带数的那句），单独放着。
const (
	lintFoot         = "命令读磁盘上存好的文件，用 login shell 在仓库根目录（仓库外的文件：文件所在的目录）跑，60 秒超时；输出（标准输出、标准错误都看）里认 路径:行:列: 信息 和 tsc 的 路径(行,列): error TS…: 信息 两种行，error / warning / note 这些级别词认得，没写算错误。{file} 换成文件的完整路径（只换掉这个文件的问题），{dir} 换成文件所在的目录（仓库里的路径，根目录是 .；换掉这个目录的），都不带时换掉这个仓库这种语言的全部。空着用内置默认（灰字），写 - 就不检查这种语言。"
	formatterFoot    = "命令从标准输入读文件内容、把格式化好的写到标准输出，用 login shell 在仓库根目录（仓库外的文件：文件所在的目录）跑，10 秒超时；{file} 换成文件的完整路径。空着用内置默认（灰字），写 - 就不格式化这种语言。"
	termdDesc        = "shell 跑在一个独立的后台进程里，关窗口、退出 Grove 都不断；再打开同一个工作区接回去。多一个常驻进程（约 %d MB）。新开的终端生效"
	webHostsDesc     = "“在网页打开”的地址怎么拼：github.com、bitbucket.org，主机名里带 gitlab、gitea、forgejo、codeberg 的自己认得；自建的主机名不带这些字时在这里指定，别的按 GitHub 的路径拼"
	snippetDirDesc   = "一种语言一个 JSON（go.json、python.json…；global.json 所有语言都有），VS Code 的片段格式：认 $1、${1:默认}、$0，不认变量和选择项。文件里的同名片段覆盖内置的；改了自动重读。敲前缀时补全里有，Tab 插入后 Tab / ⇧Tab 在跳位之间走；命令面板“插入片段…”"
	termProfilesDesc = "别的 shell、带参数、加环境变量开终端，最多 %d 个：底栏「+」右键或 ⌥点、终端菜单、“用配置新建终端…”里挑。新开的终端生效"
)

// matches 是这一项对不对得上搜索词 q（小写、去掉了首尾空白）：名字、说明、所在分组的名字，中文原文或英文译文里有 q 就算，
// 不分大小写。label 是名字对上了（名字着强调色）。
func (r settingRow) matches(q string) (hit, label bool) {
	has := func(s string) bool {
		return s != "" && (strings.Contains(strings.ToLower(s), q) || strings.Contains(strings.ToLower(i18n.English()[s]), q))
	}
	label = has(r.Label)
	return label || has(r.Desc) || has(settingsPages[r.Page]), label
}

// settingsHit 是正在画的这一项名字对上了搜索词：settingField 把名字加粗（设置窗口只有一个，在主线程上一项一项画）。
var settingsHit bool

// settingField 是设置窗口里的一行（wui.Field）：搜索时名字对上了的，名字是正文色 600（只有名字：说明、控件不变）。
func settingField(c *wui.Context, label string, fn func()) wui.Element {
	f := wui.Field(c, label, fn)
	if settingsHit {
		f.TextColor(c.Theme().Text).FontWeight(600)
	}
	return f
}

// settingsResults 是搜着的时候的设置：所有分组里对得上 q 的项，一组一个灰的小标题；返回有没有对得上的
// （命令另算，keyResults；都没有时 SettingsView 写“没有匹配的设置”）。
func settingsResults(c *wui.Context, rows []settingRow, q string) bool {
	t := c.Theme()
	found := false
	for p := range settingsPages {
		var hits []int
		var labels []bool
		for i, r := range rows {
			if hit, label := r.matches(q); hit && r.Page == p {
				hits, labels = append(hits, i), append(labels, label)
			}
		}
		if len(hits) == 0 {
			continue
		}
		found = true
		wui.Column(c.Key(p)).Gap(8).Children(func() {
			wui.Text(c, i18n.T(settingsPages[p])).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
			wui.Form(c, func() {
				for j, i := range hits {
					settingsHit = labels[j]
					rows[i].Draw(c.Key(i), i18n.T(rows[i].Label), i18n.T(rows[i].Desc))
					settingsHit = false
				}
			})
		})
	}
	return found
}

// termdMB 是终端后台进程自己大约占多少内存（MB，不算里面的 shell），是实测的数。
const termdMB = 10

// termdField 是终端设置里"后台"一项：终端后台进程在不在跑、几个会话、几个没在任何窗口里，和"停止…"。
// 停止要问一声，问的对话框在工作台窗口里（先把它叫到前面）。
func termdField(a *app.App, c *wui.Context, label string) {
	t := c.Theme()
	st := a.TermdStatus(c.Invalidate)
	msg := i18n.T("没在运行")
	if st.Running {
		msg = i18n.Tn("%d 个会话（%d 个没在任何窗口里）", st.Total, st.Loose)
	}
	settingField(c, label, func() {
		wui.Row(c).Gap(10).AlignItems(wui.Center).Children(func() {
			wui.Text(c, msg).FontSize(12.5).TextColor(t.TextMuted)
			if wui.Button(c, i18n.T("停止…")).Disabled(!st.Running || st.Total == 0).Tooltip(i18n.T("停止终端后台进程…")).Clicked() {
				if a.Win != nil {
					a.Win.Focus()
				}
				a.Update(a.StopTermDaemon)
			}
		})
	})
}

// withDefault 给 names 加上 "" 的名字：存成 "" 的是默认的那个 def。
func withDefault(names map[string]string, def string) map[string]string {
	names[""] = names[def]
	return names
}

// localUsageLine 是“本地历史保留”下面那行灰字：现在占多少、几个文件（后台算），和“在访达中显示”。
func localUsageLine(a *app.App, c *wui.Context) {
	msg := i18n.T("正在算…")
	if b, n, ok := a.LocalUsage(); ok {
		msg = i18n.Tn("现在 %s · %d 个文件", docs.HumanSize(b), n)
	}
	wui.Row(c).Gap(8).AlignItems(wui.Center).Padding(0, 0, 4, 0).Children(func() {
		wui.Text(c, msg).FontSize(11.5).TextColor(c.Theme().TextMuted)
		if linkButton(c, i18n.T("在访达中显示"), i18n.T("本地历史的目录")).Clicked() {
			if d := a.LocalHistDir(); d != "" {
				a.InFinder(d)
			}
		}
	})
}

// choice 是一项下拉选择：values 是存进设置的值；显示的名字由 names 给（再过 i18n.T），
// 或者由 name 算（如字体）。返回这一项（Field），好再加说明。
func choice(c *wui.Context, label string, v *string, values []string, names map[string]string, done func(), name ...func(string) string) wui.Element {
	opts := make([]string, len(values))
	sel := ""
	for i, x := range values {
		if len(name) > 0 {
			opts[i] = name[0](x)
		} else {
			opts[i] = i18n.T(names[x])
		}
		if x == *v {
			sel = opts[i]
		}
	}
	if sel == "" && *v != "" { // 存的值不在列表里（比如卸掉了的字体）：照样显示出来
		opts, values = append(opts, *v), append(values, *v)
		sel = *v
	}
	return settingField(c, label, func() {
		if wui.Select(c, &sel, opts).MinWidth(220).Changed() {
			if i := slices.Index(opts, sel); i >= 0 {
				*v = values[i]
				done()
			}
		}
	})
}

// number 是一项数字；0 存的是默认值 def。返回这一项（Field），好再加说明。
func number(c *wui.Context, label string, v *float32, def, lo, hi, step float64, done func()) wui.Element {
	f := float64(*v)
	if f == 0 {
		f = def
	}
	return settingField(c, label, func() {
		if wui.NumberInput(c, &f, lo, hi, step).Width(120).Changed() {
			*v = float32(f)
			done()
		}
	})
}

// toggle 是一项开关，desc 写在下面。
func toggle(c *wui.Context, label, desc string, v *bool, done func()) {
	f := settingField(c, label, func() {
		if wui.Switch(c, v).Changed() {
			done()
		}
	})
	if desc != "" {
		f.Description(desc)
	}
}

// text 是一项文字输入，desc 写在下面。
func text(c *wui.Context, label, desc string, v *string, done func()) {
	f := settingField(c, label, func() {
		if wui.TextInput(c, v).Width(260).Changed() {
			done()
		}
	})
	if desc != "" {
		f.Description(desc)
	}
}

// webHostsField 是 Git 页的"网页风格"表：一行一台主机 → 风格（下拉五种）、"移除"；最后"添加主机"，
// 当前仓库的主机还没列就先填它。没列的主机按名字认（git.KindOf）。
func webHostsField(a *app.App, c *wui.Context, s *workspace.Settings, done func()) {
	drop := -1
	for i := range s.WebHosts {
		h := &s.WebHosts[i]
		label := ""
		if i == 0 {
			label = i18n.T("网页风格")
		}
		settingField(c, label, func() {
			wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
				if wui.TextInput(c, &h.Host).Width(170).Placeholder("git.example.com").Label(i18n.T("主机")).Changed() {
					h.Host = strings.ToLower(strings.TrimSpace(h.Host))
					done()
				}
				opts := make([]string, len(workspace.WebKinds))
				sel := ""
				for j, k := range workspace.WebKinds {
					opts[j] = i18n.T(webKindNames[k])
					if k == h.Kind {
						sel = opts[j]
					}
				}
				if wui.Select(c, &sel, opts).MinWidth(150).Changed() {
					h.Kind = workspace.WebKinds[slices.Index(opts, sel)]
					done()
				}
				if wui.Button(c, i18n.T("移除")).Tooltip(i18n.Tf("从网页风格表里拿掉 %s", h.Host)).Clicked() {
					drop = i
				}
			})
		})
	}
	if drop >= 0 { // 画完这一遍再删，循环里删会越界
		s.WebHosts = slices.Delete(s.WebHosts, drop, drop+1)
		done()
	}
	label := ""
	if len(s.WebHosts) == 0 {
		label = i18n.T("网页风格")
	}
	f := settingField(c, label, func() {
		if wui.Button(c, i18n.T("添加主机")).Clicked() {
			host, kind := "", "gitlab"
			if r := a.Current(); r != nil && r.Extra.Web.OK() && s.WebKindFor(r.Extra.Web.Host) == "" {
				host, kind = r.Extra.Web.Host, string(r.Extra.Web.Kind)
			}
			s.WebHosts = append(s.WebHosts, workspace.WebHost{Host: host, Kind: kind})
			done()
		}
	})
	f.Description(i18n.T(webHostsDesc))
}

// profileEdit 是"其他配置"表一行里参数、环境两个框正在编辑的字：存的是拆好的 []string，边敲边拆了再拼会吃掉空格和引号。
type profileEdit struct{ args, env string }

// termProfilesField 是终端页的"其他配置"表：一行一个终端配置——名字、命令、"移除"，下面参数
// （空格分开，引号照 shell 的）和环境变量（每行一个 K=V）；最后"添加配置"。上面的 Shell 是默认。
func termProfilesField(c *wui.Context, s *workspace.Settings, edit *[]profileEdit, done func()) {
	t := c.Theme()
	if len(*edit) != len(s.TermProfiles) { // 第一次画、别的窗口改了：按存的重新拼
		*edit = make([]profileEdit, len(s.TermProfiles))
		for i, p := range s.TermProfiles {
			args := make([]string, len(p.Args))
			for j, a := range p.Args {
				args[j] = app.ShellQuote(a)
			}
			(*edit)[i] = profileEdit{strings.Join(args, " "), strings.Join(p.Env, "\n")}
		}
	}
	drop := -1
	seen := map[string]bool{}
	for i := range s.TermProfiles {
		p, e := &s.TermProfiles[i], &(*edit)[i]
		name := strings.TrimSpace(p.Name)
		why := ""
		switch {
		case name == "" || strings.TrimSpace(p.Shell) == "":
			why = i18n.T("名字和命令都填了才用")
		case seen[name]:
			why = i18n.T("和前面的重名，不用")
		}
		seen[name] = true
		label := ""
		if i == 0 {
			label = i18n.T("其他配置")
		}
		settingField(c.Key(i), label, func() {
			wui.Column(c).Gap(6).Children(func() {
				wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
					if wui.TextInput(c, &p.Name).Width(100).Placeholder(i18n.T("名字")).Label(i18n.T("配置的名字")).Changed() {
						done()
					}
					if wui.TextInput(c, &p.Shell).Width(152).Placeholder("/opt/homebrew/bin/fish").Label(i18n.T("命令")).Changed() {
						done()
					}
					if wui.Button(c, i18n.T("移除")).Tooltip(i18n.Tf("从其他配置里拿掉 %s", name)).Clicked() {
						drop = i
					}
				})
				if wui.TextInput(c, &e.args).Width(260).Placeholder(i18n.T("参数，空格分开；空着是 -l")).Label(i18n.T("参数")).Changed() {
					p.Args = git.SplitWords(e.args)
					done()
				}
				if wui.TextArea(c, &e.env).Width(260).Height(48).Placeholder(i18n.T("环境变量，每行一个 KEY=value")).Label(i18n.T("环境变量")).Changed() {
					p.Env = nil
					for _, l := range strings.Split(e.env, "\n") {
						if l = strings.TrimSpace(l); l != "" {
							p.Env = append(p.Env, l)
						}
					}
					done()
				}
				if why != "" {
					wui.Text(c, why).FontSize(11).TextColor(t.Danger)
				}
			})
		})
	}
	if drop >= 0 { // 画完这一遍再删
		s.TermProfiles = slices.Delete(s.TermProfiles, drop, drop+1)
		*edit = slices.Delete(*edit, drop, drop+1)
		done()
	}
	label := ""
	if len(s.TermProfiles) == 0 {
		label = i18n.T("其他配置")
	}
	f := settingField(c, label, func() {
		if wui.Button(c, i18n.T("添加配置")).AlignSelf(wui.Start).Disabled(len(s.TermProfiles) >= workspace.MaxTermProfiles).Clicked() { // 按钮不跟着撑满一行
			s.TermProfiles = append(s.TermProfiles, workspace.TermProfile{})
			*edit = append(*edit, profileEdit{})
			done()
		}
	})
	f.Description(i18n.Tf(termProfilesDesc, workspace.MaxTermProfiles))
}

// toolTable 是格式化表和检查表共用的一张表：每种语言一行命令输入框，空着时灰字是内置默认；
// 先列有内置默认的几种（langs），再是设置里另加的（后面有“移除”），最后“添加一种语言”。
type toolTable struct {
	cmds      *map[string]string // s.Formatters / s.Linters
	defaults  map[string]string  // 内置默认
	langs     []string           // 先列的（都有内置默认），按这个顺序
	extra     []string           // 设置里另加的
	what      func(lang string) string
	hint      string // 没有内置默认的语言空着时的灰字
	removeTip func(lang string) string
}

// formatterTable 是格式化表。
func formatterTable(a *app.App, s *workspace.Settings) toolTable {
	return toolTable{&s.Formatters, app.DefaultFormatters, app.FormatterLangs, a.ExtraFormatterLangs(),
		func(l string) string { return i18n.Tf("%s 的格式化命令", l) }, i18n.T("从标准输入读、写到标准输出的命令"),
		func(l string) string { return i18n.Tf("从格式化表里拿掉 %s", l) }}
}

// linterTable 是检查表。
func linterTable(a *app.App, s *workspace.Settings) toolTable {
	return toolTable{&s.Linters, app.DefaultLinters, app.LinterLangs, a.ExtraLinterLangs(),
		func(l string) string { return i18n.Tf("%s 的检查命令", l) }, i18n.T("输出 路径:行:列: 信息 的命令"),
		func(l string) string { return i18n.Tf("从检查表里拿掉 %s", l) }}
}

// draw 画整张表。
func (t toolTable) draw(c *wui.Context, done func()) {
	for _, l := range append(slices.Clone(t.langs), t.extra...) {
		t.field(c.Key(l), l, done)
	}
	t.add(c, done)
}

// field 是表里一种语言的一行。
func (t toolTable) field(c *wui.Context, lang string, done func()) {
	v := (*t.cmds)[lang]
	def, builtin := t.defaults[lang]
	set := func() {
		if *t.cmds == nil {
			*t.cmds = map[string]string{}
		}
		if v == "" && builtin {
			delete(*t.cmds, lang)
		} else {
			(*t.cmds)[lang] = v
		}
		done()
	}
	settingField(c, lang, func() {
		wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
			in := wui.TextInput(c, &v).Width(260).Label(t.what(lang))
			if builtin {
				in.Placeholder(def)
			} else {
				in.Placeholder(t.hint)
			}
			if in.Changed() {
				set()
			}
			if !builtin && wui.Button(c, i18n.T("移除")).Tooltip(t.removeTip(lang)).Clicked() {
				delete(*t.cmds, lang)
				done()
			}
		})
	})
}

// add 是表最后的“添加一种语言”：表里还没有的语言里选一种，加一行空的命令。
func (t toolTable) add(c *wui.Context, done func()) {
	opts := []string{i18n.T("选一种语言…")}
	for _, l := range editor.Languages() {
		_, builtin := t.defaults[l.Name]
		if _, listed := (*t.cmds)[l.Name]; !builtin && !listed {
			opts = append(opts, l.Name)
		}
	}
	sel := opts[0]
	settingField(c, i18n.T("添加一种语言"), func() {
		if wui.Select(c, &sel, opts).MinWidth(220).Changed() && sel != opts[0] {
			if *t.cmds == nil {
				*t.cmds = map[string]string{}
			}
			(*t.cmds)[sel] = ""
			done()
		}
	})
}

// snippetDirField 是"片段"页的片段目录一项：路径（还没建的说一声），"打开片段目录"（没有就建、复制一份内置的当样板，在访达里打开）、
// "重新加载"，和读不了的文件。
func snippetDirField(a *app.App, c *wui.Context, label, desc string) {
	t := c.Theme()
	dir := a.SnippetDir()
	set := a.Snippets()
	settingField(c, label, func() {
		wui.Column(c).Gap(6).MinWidth(0).Children(func() {
			wui.Row(c).Gap(8).AlignItems(wui.Center).Children(func() {
				if wui.Button(c, i18n.T("打开片段目录")).Disabled(dir == "").Tooltip(i18n.T("没有就建，并复制一份内置的片段当样板；在访达里打开")).Clicked() {
					a.OpenSnippetDir()
				}
				if wui.Button(c, i18n.T("重新加载")).Tooltip(i18n.T("马上重读片段目录里的 JSON（平时用到时自己会看）")).Clicked() {
					a.ReloadSnippets()
				}
			})
			where := dir
			if !a.SnippetDirExists() {
				where = i18n.Tf("%s（还没建，现在只有内置的）", dir)
			}
			if dir != "" {
				wui.Text(c, where).Font(mono()).FontSize(11).TextColor(t.TextMuted).MinWidth(0)
			}
			for _, e := range set.Errs {
				wui.Text(c, i18n.Tf("读不了 %s", e)).FontSize(11.5).TextColor(t.Danger).MinWidth(0)
			}
			wui.Text(c, desc).FontSize(11.5).TextColor(t.TextMuted).MinWidth(0)
		})
	})
}

// snippetTables 是"片段"页底下的表：一种语言一节（所有语言的在最前），每行前缀、名字、来源（内置 / 文件名）、说明。只看不改：
// 改在片段目录的 JSON 里。
func snippetTables(a *app.App, c *wui.Context) {
	t := c.Theme()
	set := a.Snippets()
	cols := [3]float32{96, 120, 96} // 前缀、名字、来源；说明占剩下的
	row := func(cells [4]string, head bool) {
		wui.Row(c).Gap(10).Padding(3, 0).AlignItems(wui.Start).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			for i, s := range cells {
				x := wui.Text(c, s).FontSize(12).MinWidth(0)
				switch {
				case head:
					x.FontSize(11).FontWeight(600).TextColor(t.TextMuted)
				case i == 0:
					x.Font(mono())
				case i == 2 || i == 3:
					x.TextColor(t.TextMuted)
				}
				if i < 3 {
					x.Width(cols[i]).Shrink(0).SingleLine()
				} else {
					x.Grow(1)
				}
			}
		})
	}
	for _, lang := range set.Langs() {
		wui.Column(c.Key(lang)).Gap(0).Margin(10, 0, 0, 0).Children(func() {
			wui.Text(c, snippetLangName(lang)).FontSize(12.5).FontWeight(600).Margin(0, 0, 4, 0)
			row([4]string{i18n.T("前缀"), i18n.T("名字"), i18n.T("来源"), i18n.T("说明")}, true)
			for _, sn := range set.Of(lang) {
				row([4]string{sn.Prefix, sn.Name, app.SnippetFileOf(sn), sn.Description()}, false)
			}
		})
	}
}

// snippetLangName 是片段文件名对应的语言名（go → Go）；global 是"所有语言"，编辑器不认识的照写文件名。
func snippetLangName(lang string) string {
	if lang == snippets.Global {
		return i18n.T("所有语言")
	}
	for _, l := range editor.Languages() {
		if snippets.LangKey(l.Name) == lang {
			return l.Name
		}
	}
	return lang
}
