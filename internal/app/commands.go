package app

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// 这个文件是命令注册表：每个动作只在这里定义一次（id、中文名、分组、快捷键、上下文、
// 能不能用、执行）。菜单栏（menu.go）、窗口快捷键（BindShortcuts）、工具提示里的快捷键（Tip）
// 和 docs/shortcuts.md 都由它生成；快捷键冲突由 commands_test.go 的表测试保证。

// Context 是快捷键生效的焦点上下文。
type Context int

const (
	Global     Context = iota // 整个窗口
	Editor                    // 编辑器有焦点
	DiffTab                   // 当前标签是 Diff 标签
	FileTree                  // 侧栏文件树有焦点
	Terminal                  // 终端有焦点
	CommitBox                 // Git 栏的提交信息框有焦点
	SearchList                // 侧栏搜索页的结果列表有焦点
)

var contextNames = [...]string{"全局", "编辑器", "Diff", "文件树", "终端", "提交框", "搜索结果"}

func (c Context) String() string { return contextNames[c] }

// Command 是一个命令。
type Command struct {
	ID    string
	Title string // 菜单项和命令面板里的名字
	Group string // 菜单栏里的菜单（文件 / 视图 / 终端 / Git / 仓库）；空 = 不进菜单栏（局部快捷键，只进文档）
	Sep   bool   // 菜单里在它前面画一条分隔线

	Mods    ui.Modifiers
	Key     ui.Key // 0 = 没有快捷键
	Context Context
	Section string // 命令面板里分组的后半截（"编辑器 · 折叠"的"折叠"）；空 = 只写菜单或上下文
	Hint    string // Key 为 0 时命令面板里显示的键：编辑器自己接的（mygo 的默认键），这里只显示、不登记

	Enabled func(a *App) bool // nil = 总能用
	Checked func(a *App) bool // 非 nil = 菜单里是勾选项
	Run     func(a *App)      // nil = 由界面元素自己处理（如提交框的 ⌘↩），这里只登记快捷键
	Local   bool              // 有 Run 也不在窗口上登记键：键由界面元素自己接（编辑器的 ⇧⌥F、编辑器自带的命令），Run 给命令面板

	// 快捷键自定义（keybind.go）：Mods / Key / Hint 是生效的键，默认的另存
	Custom   bool           // keybindings.json 里改过（包括去掉）
	Shadowed string         // 键被别的命令的自定义键顶掉了：那条命令的 id
	ed       editor.Command // 编辑器里对应的 mygo 命令（editor.* 和查找那几条）：改键时拿走 mygo 的默认键
	hintMods ui.Modifiers   // Hint 那条默认键（单键的；和弦为 0）：编辑器自带命令按它算冲突
	hintKey  ui.Key
}

// Menus 是菜单栏里自定义菜单的顺序（夹在系统的应用 / 编辑 / 窗口菜单之间）。
var Menus = []string{"文件", "视图", "终端", "Git", "仓库"}

// AppGroup 是放进应用菜单（菜单栏最左边，系统的关于、隐藏、退出那个）的命令的分组。
const AppGroup = "应用"

// withRepo 把对当前仓库的操作包成命令的 Run。
func withRepo(fn func(a *App, r *Repo)) func(a *App) {
	return func(a *App) {
		if r := a.Current(); r != nil {
			fn(a, r)
		}
	}
}

// withTermRepo 是对底栏终端所在仓库（TermRepo：没有仓库时是家目录）做的命令。
func withTermRepo(fn func(a *App, r *Repo)) func(a *App) {
	return func(a *App) { fn(a, a.TermRepo()) }
}

func hasRepo(a *App) bool     { return a.Current() != nil }
func hasWs(a *App) bool       { return a.WsPath != "" }
func hasTermTab(a *App) bool  { _, t, _ := a.ActivePane(); return t != nil }
func hasProfiles(a *App) bool { return len(a.St.Settings.Profiles()) > 0 }
func hasSplit(a *App) bool    { _, t, _ := a.ActivePane(); return t != nil && t.Root.Pane == nil }
func hasDoc(a *App) bool      { return a.CurrentDoc() != nil }
func onDiffTab(a *App) bool {
	d := a.CurrentDoc()
	return d != nil && d.IsDiffView() // 替换预览、比较也是 diff：J / K、⌥⌘L、⌥⌘F 照用
}

// hasGitRepo 是列表里有 Git 仓库（不全是普通文件夹）。
func hasGitRepo(a *App) bool {
	return slices.ContainsFunc(a.Repos, func(r *Repo) bool { return !r.Plain })
}

// hasEditor 是当前标签是读好了的文件或历史版本（有编辑器，Diff 标签没有）。
func hasEditor(a *App) bool {
	d := a.CurrentDoc()
	return d != nil && d.Kind != docs.Diff && d.Buf != nil
}

// inEditor 把编辑器的命令包成命令的 Run：在当前标签的编辑器里执行（查找条、跳行条由编辑器自己画）。
func inEditor(cmd editor.Command) func(a *App) {
	return func(a *App) { a.CurrentDoc().St.Run(cmd) }
}
func sidebarIs(mode string) func(a *App) bool {
	return func(a *App) bool { return a.St.Sidebar && a.St.SidebarMode == mode }
}

// Commands 是全部命令，按菜单里的顺序。在 init 里建：终端的 OnKey（termKey）也查这张表，直接初始化会成环。
var Commands []*Command

func init() { Commands = buildCommands() }

func buildCommands() []*Command {
	cmd, shift, alt, ctrl := ui.Cmd, ui.Shift, ui.Alt, ui.Ctrl
	cs := []*Command{
		// 终端：键盘在终端里时这几个键归终端。排在最前，RunKey 先看到它们，⌘W / ⌘K / ⌘F / ⌘G / ⇧⌘G 就盖过同键的
		// 全局和编辑器命令；⌘D、⇧⌘D、⇧⌘[ / ⇧⌘] 窗口不登记（编辑器自己要 ⌘D），由终端视图的 OnKey 交过来（terminals.go）。
		{ID: "term.closePane", Title: "关闭终端", Mods: cmd, Key: ui.KeyW, Context: Terminal, Run: withTermRepo((*App).CloseActivePane)},
		{ID: "term.clear", Title: "清屏", Mods: cmd, Key: ui.KeyK, Context: Terminal, Run: (*App).ClearTerm},
		{ID: "term.find", Title: "在终端里查找…", Mods: cmd, Key: ui.KeyF, Context: Terminal, Run: (*App).FindInTerm},
		{ID: "term.findNext", Title: "查找下一个", Mods: cmd, Key: ui.KeyG, Context: Terminal, Run: func(a *App) { a.StepTermFind(1) }},
		{ID: "term.findPrev", Title: "查找上一个", Mods: shift | cmd, Key: ui.KeyG, Context: Terminal, Run: func(a *App) { a.StepTermFind(-1) }},
		{ID: "term.splitRight", Title: "左右分屏", Mods: cmd, Key: ui.KeyD, Context: Terminal,
			Run: withTermRepo(func(a *App, r *Repo) { a.SplitTerm(r, true) })},
		{ID: "term.splitDown", Title: "上下分屏", Mods: shift | cmd, Key: ui.KeyD, Context: Terminal,
			Run: withTermRepo(func(a *App, r *Repo) { a.SplitTerm(r, false) })},
		{ID: "term.prevTab", Title: "上一个终端标签", Mods: shift | cmd, Key: ui.KeyBracketLeft, Context: Terminal, Run: func(a *App) { a.StepTermTab(-1) }},
		{ID: "term.nextTab", Title: "下一个终端标签", Mods: shift | cmd, Key: ui.KeyBracketRight, Context: Terminal, Run: func(a *App) { a.StepTermTab(1) }},
		{ID: "term.openText", Title: "把终端内容开成标签", Mods: shift | cmd, Key: ui.KeyC, Context: Terminal, Run: (*App).OpenTermText},

		// 文件
		{ID: "file.new", Title: "新建文件…", Group: "文件", Mods: cmd, Key: ui.KeyN, Enabled: hasRepo, Run: func(a *App) { a.NewHere(false) }},
		{ID: "file.newFolder", Title: "新建文件夹…", Group: "文件", Mods: shift | cmd, Key: ui.KeyN, Enabled: hasRepo, Run: func(a *App) { a.NewHere(true) }},
		{ID: "file.open", Title: "打开文件…", Group: "文件", Sep: true, Run: (*App).ChooseFiles},
		{ID: "file.openFolder", Title: "打开文件夹…", Group: "文件", Mods: cmd, Key: ui.KeyO, Run: (*App).ChooseFolder},
		{ID: "file.openWsFile", Title: "打开工作区文件…", Group: "文件", Run: (*App).ChooseWsFile},
		{ID: "file.clearRecent", Title: "清除最近工作区", Group: "文件", Enabled: func(a *App) bool { return len(a.St.RecentWs) > 0 }, Run: (*App).ClearRecentWs},
		{ID: "file.newWindow", Title: "新建窗口", Group: "文件", Sep: true, Mods: alt | cmd, Key: ui.KeyN, Run: (*App).NewWindow},
		{ID: "file.openFolderNewWindow", Title: "在新窗口中打开文件夹…", Group: "文件", Mods: alt | cmd, Key: ui.KeyO, Run: (*App).ChooseFolderNewWindow},
		{ID: "file.openWsFileNewWindow", Title: "在新窗口中打开工作区文件…", Group: "文件", Run: (*App).ChooseWsFileNewWindow},
		{ID: "file.quickOpen", Title: "快速打开…", Group: "文件", Sep: true, Mods: cmd, Key: ui.KeyP, Run: func(a *App) { a.OpenPalette("") }},
		{ID: "palette.commands", Title: "命令面板…", Group: "文件", Mods: shift | cmd, Key: ui.KeyP, Run: func(a *App) { a.OpenPalette(">") }},
		{ID: "palette.symbols", Title: "跳到工作区里的符号…", Group: "文件", Mods: cmd, Key: ui.KeyT, Run: func(a *App) { a.OpenPalette("#") }},
		{ID: "file.save", Title: "保存", Group: "文件", Sep: true, Mods: cmd, Key: ui.KeyS,
			Enabled: func(a *App) bool { d := a.CurrentDoc(); return d != nil && d.Editable() },
			Run:     func(a *App) { a.Save(a.CurrentDoc()) }},
		{ID: "file.saveAll", Title: "全部保存", Group: "文件", Mods: alt | cmd, Key: ui.KeyS,
			Enabled: func(a *App) bool { return a.DirtyDocs() > 0 }, Run: (*App).SaveEach},
		{ID: "file.close", Title: "关闭标签", Group: "文件", Mods: cmd, Key: ui.KeyW, Run: (*App).CloseCurrent},
		{ID: "file.closeAll", Title: "关闭全部标签", Group: "文件", Mods: shift | cmd, Key: ui.KeyW,
			Enabled: hasDoc, Run: func(a *App) { a.CloseOthers(nil) }},
		{ID: "file.closeSaved", Title: "关闭已保存的标签", Group: "文件", Mods: alt | cmd, Key: ui.KeyW,
			Enabled: hasDoc, Run: (*App).CloseSaved},
		{ID: "file.reopen", Title: "重新打开关闭的标签", Group: "文件", Mods: shift | cmd, Key: ui.KeyT,
			Enabled: (*App).CanReopen, Run: (*App).ReopenClosed},
		{ID: "tree.undo", Title: "撤销上次文件操作", Group: "文件", Enabled: (*App).CanUndoFile, Run: (*App).UndoFile},
		{ID: "file.localHistory", Title: "查看本地历史", Group: "文件", Sep: true, Enabled: (*App).CanShowLocalHistory,
			Run: func(a *App) { a.ShowLocalHistory(a.CurrentDoc().Abs) }},
		{ID: "file.restoreFromHistory", Title: "从本地历史恢复文件…", Group: "文件", Enabled: hasLocalHist,
			Run: func(a *App) { a.OpenPalette(">restore ") }},
		{ID: "file.clearLocalHistory", Title: "清理本地历史…", Group: "文件", Enabled: hasLocalHist, Run: (*App).ClearAllLocalHistory},
		{ID: "git.refresh", Title: "刷新 Git 状态", Group: "文件", Sep: true, Mods: cmd, Key: ui.KeyR, Run: func(a *App) { a.RefreshAll(WhyManual) }},
		{ID: "git.command", Title: "运行 git 命令…", Group: "文件", Mods: cmd, Key: ui.KeyK,
			Enabled: hasRepo, Run: func(a *App) { a.OpenPalette(">git ") }},

		// 视图
		{ID: "view.sidebar", Title: "侧栏", Group: "视图", Mods: cmd, Key: ui.KeyB,
			Checked: func(a *App) bool { return a.St.Sidebar }, Run: func(a *App) { a.ToggleArea(&a.St.Sidebar) }},
		{ID: "view.bottom", Title: "底栏（终端 / 输出）", Group: "视图", Mods: cmd, Key: ui.KeyJ,
			Checked: func(a *App) bool { return a.St.BottomBar }, Run: func(a *App) { a.ToggleArea(&a.St.BottomBar) }},
		{ID: "view.problems", Title: "问题面板", Group: "视图", Mods: shift | cmd, Key: ui.KeyM,
			Checked: (*App).ProblemsShown, Run: (*App).ToggleProblems},
		{ID: "lint.clear", Title: "清空问题", Group: "视图", Enabled: (*App).HasProblems, Run: (*App).ClearProblems},
		{ID: "view.right", Title: "右栏（历史）", Group: "视图", Mods: alt | cmd, Key: ui.KeyB,
			Checked: func(a *App) bool { return a.St.RightBar }, Run: func(a *App) { a.ToggleArea(&a.St.RightBar) }},
		{ID: "view.zen", Title: "禅模式", Group: "视图", Mods: ctrl | cmd, Key: ui.KeyZ,
			Checked: func(a *App) bool { return a.Zen }, Run: (*App).ToggleZen},
		{ID: "view.zoomIn", Title: "放大", Group: "视图", Sep: true, Mods: cmd, Key: ui.KeyEqual, Run: (*App).ZoomIn},
		{ID: "view.zoomOut", Title: "缩小", Group: "视图", Mods: cmd, Key: ui.KeyMinus, Run: (*App).ZoomOut},
		{ID: "view.zoomReset", Title: "实际大小", Group: "视图", Mods: cmd, Key: ui.Key0, Enabled: (*App).Zoomed, Run: (*App).ZoomReset},
		{ID: "view.files", Title: "文件", Group: "视图", Sep: true, Mods: shift | cmd, Key: ui.KeyE,
			Checked: sidebarIs(workspace.ModeFiles), Run: func(a *App) { a.ToggleSidebarMode(workspace.ModeFiles) }},
		{ID: "file.revealCurrent", Title: "在文件树里定位当前文件", Group: "视图", Mods: alt | shift | cmd, Key: ui.KeyE,
			Enabled: (*App).CanRevealCurrent, Run: (*App).RevealCurrent},
		{ID: "view.search", Title: "搜索", Group: "视图", Mods: shift | cmd, Key: ui.KeyF,
			Checked: sidebarIs(workspace.ModeSearch), Run: (*App).ShowSearch},
		{ID: "view.replace", Title: "搜索并替换", Group: "视图", Mods: shift | cmd, Key: ui.KeyH, Run: (*App).ShowReplace},
		{ID: "search.replaceAll", Title: "全部替换…", Group: "视图",
			Enabled: func(a *App) bool { return a.Search.Replacing && a.Search.CanReplace() && len(a.Search.Rows) > 0 }, Run: (*App).ReplaceAll},
		{ID: "search.undoReplace", Title: "撤销上次替换…", Group: "视图",
			Enabled: func(a *App) bool { return a.Search.CanUndoReplace() }, Run: (*App).UndoReplace},
		{ID: "search.openInEditor", Title: "在编辑器里打开搜索结果", Group: "视图",
			Enabled: func(a *App) bool { return a.Search.CanOpenSearchEditor() }, Run: (*App).OpenSearchEditor},
		{ID: "view.git", Title: "Git", Group: "视图", Mods: shift | cmd, Key: ui.KeyG,
			Checked: sidebarIs(workspace.ModeGit), Run: func(a *App) { a.ToggleSidebarMode(workspace.ModeGit) }},
		{ID: "view.outline", Title: "大纲", Group: "视图",
			Checked: sidebarIs(workspace.ModeOutline), Run: func(a *App) { a.ToggleSidebarMode(workspace.ModeOutline) }},
		{ID: "nav.back", Title: "后退", Group: "视图", Sep: true, Mods: ctrl, Key: ui.KeyMinus,
			Enabled: (*App).CanBack, Run: (*App).Back},
		{ID: "nav.forward", Title: "前进", Group: "视图", Mods: ctrl | shift, Key: ui.KeyMinus,
			Enabled: (*App).CanForward, Run: (*App).Forward},
		{ID: "view.splitRight", Title: "向右拆分编辑器", Group: "视图", Sep: true, Mods: cmd, Key: ui.KeyBackslash,
			Enabled: hasDoc, Run: func(a *App) { a.SplitEditor(true) }},
		{ID: "view.splitDown", Title: "向下拆分编辑器", Group: "视图", Mods: alt | cmd, Key: ui.KeyBackslash,
			Enabled: hasDoc, Run: func(a *App) { a.SplitEditor(false) }},
		{ID: "view.openInOtherGroup", Title: "在另一组里再开一份", Group: "视图", Mods: shift | cmd, Key: ui.KeyBackslash,
			Enabled: func(a *App) bool { return a.CanMirror(a.CurrentDoc()) }, Run: (*App).OpenMirrorCurrent},
		{ID: "view.mergeGroups", Title: "合并所有编辑器组", Group: "视图", Enabled: (*App).Split, Run: (*App).MergeGroups},
		{ID: "diff.unified", Title: "统一 diff", Group: "视图", Sep: true, Mods: alt | cmd, Key: ui.KeyL, Context: DiffTab,
			Checked: func(a *App) bool { return a.St.Unified }, Run: (*App).ToggleLayout},
		{ID: "diff.full", Title: "全文 diff", Group: "视图", Mods: alt | cmd, Key: ui.KeyF, Context: DiffTab,
			Checked: func(a *App) bool { return a.St.FullDiff }, Run: (*App).ToggleScope},
		{ID: "diff.nextFile", Title: "下一个文件", Group: "视图", Mods: alt, Key: ui.KeyJ, Context: DiffTab,
			Run: func(a *App) { a.StepFile(1) }},
		{ID: "diff.prevFile", Title: "上一个文件", Group: "视图", Mods: alt, Key: ui.KeyK, Context: DiffTab,
			Run: func(a *App) { a.StepFile(-1) }},
		{ID: "diff.compareWithSel", Title: "比较当前文件与已选项目", Group: "视图", Enabled: (*App).CanCompareCurrent, Run: (*App).CompareCurrentWithSel},
		{ID: "view.softWrap", Title: "软换行", Group: "视图", Sep: true, Mods: alt, Key: ui.KeyZ,
			Checked: func(a *App) bool { return a.St.Settings.SoftWrap }, Run: (*App).ToggleSoftWrap},
		{ID: "view.whitespace", Title: "显示空白字符", Group: "视图",
			Checked: func(a *App) bool { return a.St.Settings.Whitespace == "all" }, Run: (*App).ToggleWhitespace},
		{ID: "view.stickyScroll", Title: "粘性滚动", Group: "视图",
			Checked: func(a *App) bool { return !a.St.Settings.NoStickyScroll }, Run: (*App).ToggleStickyScroll},
		{ID: "files.ignored", Title: "显示被忽略的文件", Group: "视图",
			Checked: func(a *App) bool { return a.St.Settings.ShowIgnored }, Run: (*App).ToggleShowIgnored},
		{ID: "view.openEditors", Title: "打开的编辑器", Group: "视图", // 侧栏文件模式上面那一段，和设置 → 文件树那一项是同一个
			Checked: func(a *App) bool { return a.St.Settings.ShowOpenEditors }, Run: (*App).ToggleOpenEditors},
		{ID: "tree.expandChangedAll", Title: "展开所有仓库有改动的目录", Group: "视图", // 和文件树空白处右键的那一项一样，侧栏切到文件
			Enabled: func(a *App) bool { return slices.ContainsFunc(a.Repos, HasChanges) },
			Run:     func(a *App) { a.ShowSidebar(workspace.ModeFiles); a.ExpandAllChanged() }},
		{ID: "view.markdownPreview", Title: "Markdown / SVG 预览", Group: "视图", Mods: shift | cmd, Key: ui.KeyV,
			Enabled: func(a *App) bool { d := a.CurrentDoc(); return d != nil && d.CanPreview() },
			Checked: func(a *App) bool { d := a.CurrentDoc(); return d != nil && d.Preview }, Run: (*App).TogglePreview},
		{ID: "view.hex", Title: "以十六进制查看", Group: "视图",
			Enabled: func(a *App) bool { return a.CanHex(a.CurrentDoc()) }, Checked: (*App).hexOn, Run: (*App).ToggleHex},
		{ID: "view.blame", Title: "显示 blame 装订线", Group: "视图", Mods: ctrl | cmd, Key: ui.KeyB,
			Enabled: func(a *App) bool { return a.CanBlame(a.CurrentDoc()) },
			Checked: func(a *App) bool { d := a.CurrentDoc(); return d != nil && d.ShowBlame }, Run: (*App).ToggleBlame},
		{ID: "view.inlineBlame", Title: "显示行内 blame", Group: "视图", // 所有标签一起，和设置 Git 页那一项是同一个
			Checked: func(a *App) bool { return a.St.Settings.InlineBlame }, Run: (*App).ToggleInlineBlame},

		// 终端（菜单栏）：对当前仓库当前终端标签里有键盘的那一格
		{ID: "term.new", Title: "新建终端", Group: "终端", Mods: ctrl | shift, Key: ui.KeyBackquote,
			Run: withTermRepo(func(a *App, r *Repo) { a.NewTerminal(r, "") })},
		{ID: "term.newEditor", Title: "在编辑器区新建终端", Group: "终端", Mods: ctrl | shift | cmd, Key: ui.KeyBackquote,
			Run: withTermRepo(func(a *App, r *Repo) { a.NewEditorTerminal(r) })},
		{ID: "term.newProfile", Title: "用配置新建终端…", Group: "终端", Enabled: hasProfiles, Run: func(a *App) { a.OpenPalette(">term ") }},
		{ID: "task.run", Title: "运行任务…", Group: "终端", Mods: shift | cmd, Key: ui.KeyB, Run: func(a *App) { a.OpenPalette("!") }},
		{ID: "term.splitH", Title: "左右分屏", Group: "终端", Sep: true,
			Run: withTermRepo(func(a *App, r *Repo) { a.SplitTerm(r, true) })},
		{ID: "term.splitV", Title: "上下分屏", Group: "终端",
			Run: withTermRepo(func(a *App, r *Repo) { a.SplitTerm(r, false) })},
		{ID: "term.closeSplit", Title: "关闭这一格分屏", Group: "终端", Enabled: hasSplit, Run: withTermRepo((*App).CloseActivePane)},
		{ID: "term.rename", Title: "重命名终端标签…", Group: "终端", Sep: true, Enabled: hasTermTab,
			Run: withTermRepo(func(a *App, r *Repo) { a.showTerm(r); a.StartRenameTerm(r, a.CurTermTab(r)) })},
		{ID: "term.restart", Title: "重新启动终端", Group: "终端", Enabled: hasTermTab, Run: withTermRepo((*App).RestartTerm)},
		{ID: "term.closeTab", Title: "关闭终端标签", Group: "终端", Enabled: hasTermTab,
			Run: withTermRepo(func(a *App, r *Repo) { a.CloseTermTab(r, a.CurTermTab(r)) })},
		{ID: "term.outputTab", Title: "把终端内容开成标签", Group: "终端", Sep: true, Enabled: hasTermOutput, Run: (*App).OpenTermText},
		{ID: "term.copyAll", Title: "复制全部输出", Group: "终端", Enabled: hasTermOutput, Run: (*App).CopyAllOutput},
		{ID: "term.daemonStop", Title: "停止终端后台进程…", Group: "终端", Sep: true, Enabled: termdRunning, Run: (*App).StopTermDaemon},

		// Git
		{ID: "git.fetch", Title: "抓取（fetch --prune）", Group: "Git", Enabled: hasRepo, Run: withRepo((*App).Fetch)},
		{ID: "git.pull", Title: "拉取", Group: "Git", Mods: shift | cmd, Key: ui.KeyU,
			Enabled: hasRepo, Run: withRepo((*App).Pull)},
		{ID: "git.push", Title: "推送", Group: "Git", Mods: alt | cmd, Key: ui.KeyP, Enabled: hasRepo, Run: withRepo((*App).Push)},
		{ID: "git.pushForce", Title: "强制推送（--force-with-lease）…", Group: "Git",
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && PushForceWhy(r) == "" }, Run: withRepo((*App).PushForce)},
		{ID: "git.sync", Title: "同步（拉取再推送）", Group: "Git", Enabled: hasRepo, Run: withRepo((*App).Sync)},
		{ID: "git.fetchAll", Title: "全部抓取", Group: "Git", Sep: true, Mods: shift | cmd, Key: ui.KeyR,
			Enabled: hasGitRepo, Run: (*App).FetchAll},
		{ID: "git.pullAll", Title: "全部拉取", Group: "Git", Mods: alt | shift | cmd, Key: ui.KeyU,
			Enabled: hasGitRepo, Run: (*App).PullAll},
		{ID: "git.cancel", Title: "取消正在跑的 git 命令", Group: "Git",
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.Running() != nil }, Run: withRepo((*App).CancelOp)},
		{ID: "git.stopAll", Title: "停止全部抓取 / 拉取", Group: "Git",
			Enabled: func(a *App) bool { return a.Batch != nil }, Run: (*App).StopBatch},
		{ID: "git.stageAll", Title: "暂存全部", Group: "Git", Sep: true, Enabled: hasRepo, Run: withRepo((*App).StageAll)},
		{ID: "git.unstageAll", Title: "取消全部暂存", Group: "Git", Enabled: hasRepo, Run: withRepo((*App).UnstageAll)},
		{ID: "git.discardAll", Title: "丢弃所有未暂存改动…", Group: "Git", Enabled: hasRepo, Run: withRepo((*App).DiscardAll)},
		{ID: "git.amend", Title: "修改上一次提交…", Group: "Git", Sep: true,
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.Extra.Head != "" },
			Checked: func(a *App) bool { r := a.Current(); return r != nil && r.Amend },
			Run:     withRepo(func(a *App, r *Repo) { a.SetAmend(r, !r.Amend); a.ShowCommit(r) })},
		{ID: "git.undoCommit", Title: "撤销上次提交", Group: "Git",
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && UndoWhy(r) == "" }, Run: withRepo((*App).UndoCommit)},
		{ID: "git.continue", Title: "继续合并 / 变基", Group: "Git",
			Enabled: func(a *App) bool {
				r := a.Current()
				return r != nil && r.Extra.Ongoing != git.NoOp && r.Status.Conflicts == 0
			},
			Run: withRepo((*App).ContinueOngoing)},
		{ID: "git.skip", Title: "跳过这个提交…", Group: "Git",
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.Extra.Ongoing.SkipArgs() != nil },
			Run:     withRepo((*App).SkipOngoing)},
		{ID: "git.abort", Title: "中止合并 / 变基…", Group: "Git",
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.Extra.Ongoing != git.NoOp },
			Run:     withRepo((*App).AbortOngoing)},
		{ID: "git.rebaseInteractive", Title: "交互式变基…", Group: "Git", Enabled: canRebaseHere, Run: (*App).RebaseHere},
		{ID: "git.stash", Title: "贮藏改动（stash）", Group: "Git", Sep: true, Enabled: hasRepo, Run: withRepo((*App).Stash)},
		{ID: "git.stashPop", Title: "弹出贮藏（stash pop）", Group: "Git",
			Enabled: func(a *App) bool { return hasRepo(a) && len(a.Current().Extra.Stashes) > 0 }, Run: withRepo((*App).StashPop)},
		{ID: "git.openWeb", Title: "在网页打开仓库", Group: "Git", Sep: true,
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.Extra.Web.OK() },
			Run:     withRepo(func(a *App, r *Repo) { a.OpenWeb(a.WebOf(r).Base) })},
		{ID: "git.newBranch", Title: "新建分支…", Group: "Git", Sep: true, Mods: alt | shift | cmd, Key: ui.KeyB, // 不用 ⇧⌘B（给了运行任务）
			Enabled: hasRepo, Run: func(a *App) { a.Branch = BranchDialog{Open: true} }},
		{ID: "git.newWorktree", Title: "新建工作树…", Group: "Git", Sep: true,
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.Loaded && r.Err == nil },
			Run:     withRepo((*App).NewWorktree)},
		{ID: "git.refreshWorktrees", Title: "刷新工作树", Group: "Git", Enabled: hasRepo, Run: withRepo((*App).RefreshWorktrees)},
		{ID: "git.pruneWorktrees", Title: "清理失效的工作树…", Group: "Git",
			Enabled: func(a *App) bool { r := a.Current(); return r != nil && r.HasWorktrees() }, Run: withRepo((*App).PruneWorktrees)},
	}

	// 仓库：⌘1–9 切仓库，文件树和 Git 栏滚到它那一段（菜单里显示仓库名，见 menu.go）
	for i := range 9 {
		cs = append(cs, &Command{ID: fmt.Sprintf("repo.%d", i+1), Title: fmt.Sprintf("跳到第 %d 个仓库", i+1), Group: "仓库",
			Mods: cmd, Key: ui.Key0 + ui.Key(i+1),
			Enabled: func(a *App) bool { return i < len(a.Repos) },
			Run:     func(a *App) { a.JumpRepo(i) }})
	}
	cs = append(cs,
		&Command{ID: "repo.add", Title: "添加仓库…", Group: "仓库", Sep: true, Mods: shift | cmd, Key: ui.KeyO, Enabled: hasWs, Run: (*App).ChooseRepoDir},
		&Command{ID: "repo.clone", Title: "克隆仓库…", Group: "仓库", Run: (*App).OpenClone}, // 空工作区也能克隆：克隆完当工作区打开
		&Command{ID: "repo.showHidden", Title: "显示移除的工作区仓库", Group: "仓库",
			Enabled: func(a *App) bool { return len(a.St.Hidden) > 0 }, Run: (*App).ShowAllRepos},

		// 应用菜单（菜单栏最左边那个）
		&Command{ID: "app.about", Title: "关于 Grove", Group: AppGroup, Run: func(a *App) {
			if a.OpenAbout != nil {
				a.OpenAbout()
			}
		}},
		&Command{ID: "app.settings", Title: "设置…", Group: AppGroup, Sep: true, Mods: cmd, Key: ui.KeyComma, Run: func(a *App) {
			if a.OpenSettings != nil {
				a.OpenSettings()
			}
		}},
		&Command{ID: "app.settingsJSON", Title: "编辑设置文件（JSON）…", Group: AppGroup, Run: (*App).OpenSettingsDoc},
		&Command{ID: "app.settingsExport", Title: "导出设置…", Group: AppGroup, Run: func(a *App) { a.ExportSettings(a.Win) }},
		&Command{ID: "app.settingsImport", Title: "导入设置…", Group: AppGroup, Run: func(a *App) { a.ImportSettings(a.Win) }},
		&Command{ID: "app.settingsReset", Title: "恢复默认设置…", Group: AppGroup, Run: (*App).ResetSettings},

		// 编辑器：编辑器有焦点时它自己接这些键；没焦点（焦点在文件树、终端）或从命令面板来时走这里。
		// 不进菜单栏：⌥⌘F 在菜单里是 Diff 的全文 diff，同一个键按当前标签分派（RunKey）
		&Command{ID: "edit.find", Title: "查找…", Mods: cmd, Key: ui.KeyF, Context: Editor, Enabled: hasEditor, Run: inEditor(editor.Find), ed: editor.Find},
		&Command{ID: "edit.replace", Title: "替换…", Mods: alt | cmd, Key: ui.KeyF, Context: Editor, Enabled: hasEditor, Run: inEditor(editor.FindReplace), ed: editor.FindReplace},
		&Command{ID: "edit.findNext", Title: "查找下一个", Mods: cmd, Key: ui.KeyG, Context: Editor, Enabled: hasEditor, Run: inEditor(editor.FindNext), ed: editor.FindNext},
		&Command{ID: "edit.goToLine", Title: "跳到行…", Mods: ctrl, Key: ui.KeyG, Context: Editor, Enabled: hasEditor, Run: inEditor(editor.GoToLine), ed: editor.GoToLine},
		// ⇧⌘O 是添加仓库，跳到符号换成 ⌃⇧O
		&Command{ID: "editor.symbol", Title: "跳到符号…", Mods: ctrl | shift, Key: ui.KeyO, Context: Editor, Enabled: hasEditor,
			Run: func(a *App) { a.OpenPalette("@") }},
		// 外部命令（app/format.go）；这种语言没有命令时照样能按，提示里说为什么。键由编辑器自己接（ui.docKeys）：
		// 登记在窗口上的话编辑器拿不到这个键，macOS 照样把 ⇧⌥F 打出的 Ï 送进编辑器
		&Command{ID: "editor.format", Title: "格式化文档", Mods: shift | alt, Key: ui.KeyF, Context: Editor, Local: true,
			Enabled: func(a *App) bool { return CanFormat(a.CurrentDoc()) }, Run: func(a *App) { a.Format(a.CurrentDoc()) }},
		// 检查（lint.go）：不看“保存时检查”开没开；没有命令时照样能按，提示里说
		&Command{ID: "lint.run", Title: "检查当前文件", Context: Editor,
			Enabled: func(a *App) bool { return CanLint(a.CurrentDoc()) }, Run: func(a *App) { a.Lint(a.CurrentDoc()) }},
		// 代码片段（snippets.go）：命令面板里列当前语言和所有语言的，回车插到光标处；敲前缀时补全里也有
		&Command{ID: "edit.insertSnippet", Title: "插入片段…", Context: Editor,
			Enabled: (*App).CanInsertSnippet, Run: func(a *App) { a.OpenPalette(">snip ") }},

		// 局部快捷键：由对应的界面元素处理，这里登记好让冲突测试和快捷键文档覆盖到
		&Command{ID: "merge.next", Title: "下一处冲突", Mods: alt, Key: ui.KeyF7, Context: Editor},
		&Command{ID: "merge.prev", Title: "上一处冲突", Mods: alt | shift, Key: ui.KeyF7, Context: Editor},
		&Command{ID: "merge.nextAlt", Title: "下一处冲突", Mods: ctrl | alt, Key: ui.KeyDown, Context: Editor},
		&Command{ID: "merge.prevAlt", Title: "上一处冲突", Mods: ctrl | alt, Key: ui.KeyUp, Context: Editor},
		&Command{ID: "merge.ours", Title: "冲突：用我的", Mods: ctrl | alt, Key: ui.Key1, Context: Editor},
		&Command{ID: "merge.theirs", Title: "冲突：用他们的", Mods: ctrl | alt, Key: ui.Key2, Context: Editor},
		&Command{ID: "merge.both", Title: "冲突：两个都要", Mods: ctrl | alt, Key: ui.Key3, Context: Editor},
		// 行级暂存：Diff 里点行号选几行以后，底下那一栏的按钮和命令面板里的这几个
		&Command{ID: "diff.stageLines", Title: "暂存选中的行", Context: DiffTab,
			Enabled: func(a *App) bool { s, _, _ := LinesOps(a.CurrentDoc()); return s }, Run: func(a *App) { a.StageLines(a.CurrentDoc()) }},
		&Command{ID: "diff.unstageLines", Title: "取消暂存选中的行", Context: DiffTab,
			Enabled: func(a *App) bool { _, u, _ := LinesOps(a.CurrentDoc()); return u }, Run: func(a *App) { a.UnstageLines(a.CurrentDoc()) }},
		&Command{ID: "diff.discardLines", Title: "丢弃选中的行…", Context: DiffTab,
			Enabled: func(a *App) bool { _, _, d := LinesOps(a.CurrentDoc()); return d }, Run: func(a *App) { a.DiscardLines(a.CurrentDoc()) }},
		// J / K 由 mygo 的 Diff 自己接（ui/tabs.go hunkKeys），Run 给命令面板和标签条上的按钮
		&Command{ID: "diff.nextHunk", Title: "下一处改动", Key: ui.KeyJ, Context: DiffTab, Local: true,
			Enabled: func(a *App) bool { return CanGoToHunk(a.CurrentDoc(), 1) }, Run: func(a *App) { a.GoToHunk(a.CurrentDoc(), 1) }},
		&Command{ID: "diff.prevHunk", Title: "上一处改动", Key: ui.KeyK, Context: DiffTab, Local: true,
			Enabled: func(a *App) bool { return CanGoToHunk(a.CurrentDoc(), -1) }, Run: func(a *App) { a.GoToHunk(a.CurrentDoc(), -1) }},
		// 编辑器装订线上的改动块（peek.go）：不配默认键，光标在块里时可用；点改动条是同一件事
		&Command{ID: "diff.peekHunk", Title: "就地看这处改动", Context: Editor,
			Enabled: func(a *App) bool {
				d := a.CurrentDoc()
				return d != nil && d.Buf != nil && HunkAt(DocHunks(d), d.St.CursorPos().Line, d.Buf.LineCount()) >= 0
			},
			Run: func(a *App) { d := a.CurrentDoc(); a.TogglePeek(d, d.St.CursorPos().Line) }},
		&Command{ID: "commit", Title: "提交", Mods: cmd, Key: ui.KeyEnter, Context: CommitBox},
		&Command{ID: "search.replaceOne", Title: "替换选中的一处 / 文件", Mods: cmd, Key: ui.KeyEnter, Context: SearchList},
		&Command{ID: "search.dismiss", Title: "排除选中的一处 / 文件", Key: ui.KeyBackspace, Context: SearchList},
		&Command{ID: "tree.expand", Title: "展开目录 / 仓库", Key: ui.KeyRight, Context: FileTree},
		&Command{ID: "tree.collapse", Title: "收起目录 / 仓库，或走到上一级", Key: ui.KeyLeft, Context: FileTree},
		&Command{ID: "tree.open", Title: "固定打开文件", Key: ui.KeyEnter, Context: FileTree},
		&Command{ID: "tree.openSide", Title: "在侧边打开", Mods: alt, Key: ui.KeyEnter, Context: FileTree},
		&Command{ID: "tree.rename", Title: "重命名…", Key: ui.KeyF2, Context: FileTree},
		&Command{ID: "tree.trash", Title: "移到废纸篓…", Mods: cmd, Key: ui.KeyBackspace, Context: FileTree},
		&Command{ID: "tree.copy", Title: "复制文件", Mods: cmd, Key: ui.KeyC, Context: FileTree},
		&Command{ID: "tree.paste", Title: "粘贴文件", Mods: cmd, Key: ui.KeyV, Context: FileTree},
		// ⌘D 在菜单栏里是终端的"左右分屏"：菜单接到按键、RunKey 分派，键盘在文件树里时落到这里
		&Command{ID: "tree.duplicate", Title: "创建副本", Mods: cmd, Key: ui.KeyD, Context: FileTree, Run: (*App).DuplicateChosen},
		// ⌘Z 在窗口里先被菜单栏"编辑"的撤销接走，键盘在文件树里时 treeCommands 接（没有菜单栏时文件树自己接），
		// 这里只进文档；命令面板和菜单栏用的是"文件"里的 tree.undo
		&Command{ID: "tree.undoKey", Title: "撤销上次文件操作", Mods: cmd, Key: ui.KeyZ, Context: FileTree},
	)
	cs = append(cs, editorCommands(cs)...)
	// 当前仓库是普通文件夹（不是 Git 仓库）时 git 命令都不能用；刷新、取消、全部抓取 / 拉取 / 停止不看当前仓库，
	// "运行 git 命令…"留着（能 git init，别的命令 RunCommand 会说不行）。
	for _, c := range cs {
		switch c.ID {
		case "git.refresh", "git.command", "git.cancel", "git.fetchAll", "git.pullAll", "git.stopAll":
			continue
		}
		if en := c.Enabled; strings.HasPrefix(c.ID, "git.") {
			c.Enabled = func(a *App) bool { r := a.Current(); return r != nil && !r.Plain && (en == nil || en(a)) }
		}
	}
	return cs
}

// edFlags 是编辑器自带命令的限制：edits 会改文字（只读的标签上不能用），hunk 要有 HEAD 基准，
// whole 扫一遍整篇文字、在主线程上（超过 MaxWholeTextBytes 不能用）。
type edFlags uint8

const (
	edits edFlags = 1 << iota
	hunk
	whole
)

// MaxWholeTextBytes 是扫整篇文字的编辑器命令能用的最大文件：mygo 的全部折叠在主线程上算所有折叠范围，
// 1 MB 真代码约 0.2 s、折叠范围密的约 1.5 s，64 MB 要一分多钟；把全选拆成每行一个光标 64 MB 要 19 s。
const MaxWholeTextBytes = 1 << 20

// editorTable 是编辑器自带、只进命令面板的命令（mygo 的 editor.Command）。
var editorTable = []struct {
	cmd     editor.Command
	title   string
	section string
	flags   edFlags
}{
	{editor.Fold, "折叠", "折叠", 0},
	{editor.Unfold, "展开", "折叠", 0},
	{editor.ToggleFold, "折叠 / 展开", "折叠", 0},
	{editor.FoldAll, "全部折叠", "折叠", whole},
	{editor.UnfoldAll, "全部展开", "折叠", 0},
	{editor.FoldRecursive, "递归折叠", "折叠", whole},
	{editor.UnfoldRecursive, "递归展开", "折叠", 0},
	{editor.FoldSelection, "折叠选中的行", "折叠", 0},
	{editor.FoldLevel1, "折叠到第 1 级", "折叠", whole},
	{editor.FoldLevel2, "折叠到第 2 级", "折叠", whole},
	{editor.FoldLevel3, "折叠到第 3 级", "折叠", whole},
	{editor.FoldLevel4, "折叠到第 4 级", "折叠", whole},
	{editor.FoldLevel5, "折叠到第 5 级", "折叠", whole},
	{editor.FoldLevel6, "折叠到第 6 级", "折叠", whole},
	{editor.FoldLevel7, "折叠到第 7 级", "折叠", whole},

	{editor.AddCursorAbove, "在上一行加光标", "多光标与选择", 0},
	{editor.AddCursorBelow, "在下一行加光标", "多光标与选择", 0},
	{editor.SelectNext, "加选下一个相同的", "多光标与选择", 0},
	{editor.SelectPrevious, "加选上一个相同的", "多光标与选择", 0},
	{editor.SelectAllMatches, "选中所有相同的", "多光标与选择", whole},
	{editor.SplitSelectionIntoLines, "把选区拆成每行一个光标", "多光标与选择", whole},
	{editor.SelectLine, "选中当前行", "多光标与选择", 0},
	{editor.SelectLarger, "扩大选区", "多光标与选择", 0},
	{editor.SelectSmaller, "缩小选区", "多光标与选择", 0},
	{editor.UndoSelection, "撤销选区变化", "多光标与选择", 0},
	{editor.RedoSelection, "重做选区变化", "多光标与选择", 0},
	{editor.MoveToBracket, "跳到匹配的括号", "多光标与选择", 0},
	{editor.SelectToBracket, "选到匹配的括号", "多光标与选择", 0},

	{editor.MoveLinesUp, "上移行", "行", edits},
	{editor.MoveLinesDown, "下移行", "行", edits},
	{editor.DuplicateLinesUp, "向上复制行", "行", edits},
	{editor.DuplicateLinesDown, "向下复制行", "行", edits},
	{editor.DuplicateSelection, "重复选中的内容", "行", edits},
	{editor.DeleteLine, "删除行", "行", edits},
	{editor.JoinLines, "合并行", "行", edits},
	{editor.Transpose, "对调光标两边的字符", "行", edits},
	{editor.Indent, "增加缩进", "行", edits},
	{editor.Outdent, "减少缩进", "行", edits},
	{editor.ToggleComment, "切换行注释", "行", edits},
	{editor.ToggleBlockComment, "切换块注释", "行", edits},
	{editor.SortLines, "按升序排列行", "行", edits},
	{editor.SortLinesCaseInsensitive, "按升序排列行（不分大小写）", "行", edits},
	{editor.ReverseLines, "反转行的顺序", "行", edits},
	{editor.UniqueLines, "删掉重复的行", "行", edits},
	{editor.TrimTrailingWhitespace, "去掉行尾空白", "行", edits},

	{editor.Uppercase, "转成大写", "大小写", edits},
	{editor.Lowercase, "转成小写", "大小写", edits},
	{editor.TitleCase, "转成词首大写", "大小写", edits},
	{editor.SnakeCase, "转成 snake_case", "大小写", edits},
	{editor.KebabCase, "转成 kebab-case", "大小写", edits},
	{editor.CamelCase, "转成 camelCase", "大小写", edits},
	{editor.PascalCase, "转成 PascalCase", "大小写", edits},

	{editor.FindPrevious, "查找上一个", "查找", 0},
	{editor.UseSelectionForFind, "用选中的文字查找", "查找", 0},
	{editor.FindInSelection, "在选区里查找", "查找", 0},

	{editor.ScrollCursorCenter, "把光标滚到中间", "其他", 0},
	{editor.NextHunk, "下一处改动", "其他", hunk},
	{editor.PreviousHunk, "上一处改动", "其他", hunk},
	{editor.RevertHunk, "还原这处改动", "其他", edits | hunk},
}

// editorCommands 把 editorTable 注册成命令，ID 是 "editor." 加 mygo 的英文名（命令面板也按英文名搜）。
// Key 留 0、Local：不进菜单栏和窗口快捷键，编辑器有焦点时按键由它自己接；Hint 是它的默认键里第一下不归菜单的那条
// （菜单的快捷键先于编辑器接到按键，⌘K 是"运行 git 命令…"，所以 ⌘K 开头的和弦都不显示）。改了键的（keybind.go）
// Key 是新键，由 EditorBindings 交给编辑器。State.Run 不管 ReadOnly，所以 Run 自己再查一遍 Enabled，不只靠面板灰掉。
func editorCommands(menu []*Command) []*Command {
	defs, taken := editor.DefaultBindings(), menuTaken(menu)
	var cs []*Command
	for _, e := range editorTable {
		en := hasEditor
		if f := e.flags; f != 0 {
			en = func(a *App) bool {
				d := a.CurrentDoc()
				return hasEditor(a) && (f&edits == 0 || d.Editable()) && (f&hunk == 0 || d.Base != nil && !d.NoBase) &&
					(f&whole == 0 || d.Buf.Len() <= MaxWholeTextBytes)
			}
		}
		hint, b, ok := edHint(defs, e.cmd, taken)
		if !ok || b.ThenKey != 0 {
			b.Key = 0
		}
		cs = append(cs, &Command{ID: "editor." + e.cmd.String(), Title: e.title, Context: Editor, Section: e.section,
			Hint: hint, Enabled: en, Run: inEditorChecked(en, func(st *editor.State) { st.Run(e.cmd) }), Local: true, ed: e.cmd,
			hintMods: b.Mods, hintKey: b.Key})
	}
	// 折叠导入不是 mygo 的命令（打开文件时编辑器可以自己折一次），排在折叠那几条后面
	imports := &Command{ID: "editor.foldImports", Title: "折叠导入", Context: Editor, Section: "折叠", Enabled: hasEditor,
		Run: inEditorChecked(hasEditor, (*editor.State).FoldImports)}
	i := slices.IndexFunc(cs, func(c *Command) bool { return c.Section != "折叠" })
	return slices.Insert(cs, i, imports)
}

// inEditorChecked 是编辑器自带命令的 Run：先查 Enabled（只读的标签上改文字的命令什么都不做），
// 再在当前标签的编辑器里执行；面板关掉后键盘回到编辑器，多光标、选中的结果能接着敲。
func inEditorChecked(en func(a *App) bool, fn func(st *editor.State)) func(a *App) {
	return func(a *App) {
		if !en(a) {
			return
		}
		fn(&a.CurrentDoc().St)
		a.FocusDoc = true
	}
}

// Find 是 id 对应的命令；没有为 nil。
func Find(id string) *Command {
	for _, c := range Commands {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Exec 按 id 执行命令（按钮、菜单、快捷键都走这里）；不能用时什么都不做。
func (a *App) Exec(id string) {
	c := Find(id)
	if c == nil {
		panic("没有命令 " + id)
	}
	a.run(c)
}

func (a *App) run(c *Command) {
	if c.Run == nil || !a.CanRun(c) {
		return
	}
	c.Run(a)
}

// CanRun 是命令现在能不能用：上下文对、Enabled 为真。
func (a *App) CanRun(c *Command) bool {
	if c.Context == DiffTab && !onDiffTab(a) || c.Context == Terminal && !a.TermFocused || c.Context == FileTree && !a.TreeFocused {
		return false
	}
	return c.Enabled == nil || c.Enabled(a)
}

// windowKey 是快捷键由窗口（菜单栏或窗口根）分派的命令：全局、Diff 标签和编辑器的。
func windowKey(c *Command) bool {
	return c.Key != 0 && c.Run != nil && !c.Local && (c.Context == Global || c.Context == DiffTab || c.Context == Editor)
}

// RunKey 执行 mods+key 对应的、现在能用的第一个命令（如 ⌥⌘F：Diff 标签里是全文 diff，文件标签里是替换；
// ⌘W：键盘在终端里是关终端，否则关标签；⌘D：终端里是分屏，文件树里是创建副本），返回有没有执行。
func (a *App) RunKey(mods ui.Modifiers, key ui.Key) bool {
	for _, c := range Commands {
		if (windowKey(c) || (c.Context == Terminal || c.Context == FileTree) && c.Run != nil) && c.Mods == mods && c.Key == key && a.CanRun(c) {
			c.Run(a)
			return true
		}
	}
	return false
}

// BindShortcuts 在窗口根上登记全局、Diff 标签和编辑器的快捷键（同一个键只登记一次，由 RunKey 分派）；
// 在 View 的根上调。焦点里的元素先接键（编辑器有焦点时它自己处理查找、跳行）。
// 有菜单栏时菜单的快捷键先接到按键，这里是没有菜单（测试、WORKBENCH_PLAIN）时的同一套。
func (a *App) BindShortcuts(c *ui.Context) {
	type combo struct {
		mods ui.Modifiers
		key  ui.Key
	}
	seen := map[combo]bool{}
	for _, cmd := range Commands {
		k := combo{cmd.Mods, cmd.Key}
		if !windowKey(cmd) || seen[k] {
			continue
		}
		seen[k] = true
		c.OnShortcut(k.mods, k.key, func() { a.RunKey(k.mods, k.key) })
	}
}

var keyNames = map[ui.Key][2]string{ // 菜单快捷键里的名字、显示的符号
	ui.KeyEnter: {"Enter", "↩"}, ui.KeyMinus: {"-", "-"}, ui.KeyComma: {",", ","}, ui.KeyBackslash: {"\\", "\\"},
	ui.KeyLeft: {"Left", "←"}, ui.KeyRight: {"Right", "→"}, ui.KeyUp: {"Up", "↑"}, ui.KeyDown: {"Down", "↓"},
	ui.KeySpace: {"Space", "空格"}, ui.KeyBracketLeft: {"[", "["}, ui.KeyBracketRight: {"]", "]"},
	ui.KeyBackspace: {"Backspace", "⌫"}, ui.KeyEscape: {"Escape", "Esc"},
	ui.KeySlash: {"/", "/"}, ui.KeySemicolon: {";", ";"}, ui.KeyPeriod: {".", "."},
	ui.KeyBackquote: {"`", "`"}, ui.KeyEqual: {"=", "="}, ui.KeyQuote: {"'", "'"},
	// 默认键里没有、自定义（keybind.go）能用的
	ui.KeyTab: {"Tab", "⇥"}, ui.KeyDelete: {"Delete", "⌦"}, ui.KeyInsert: {"Insert", "Ins"}, ui.KeyHome: {"Home", "↖"},
	ui.KeyEnd: {"End", "↘"}, ui.KeyPageUp: {"PageUp", "⇞"}, ui.KeyPageDown: {"PageDown", "⇟"},
}

func keyName(k ui.Key, display bool) string {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return string(rune('A' + k - ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		return string(rune('0' + k - ui.Key0))
	case k >= ui.KeyF1 && k <= ui.KeyF12:
		return "F" + strconv.Itoa(int(k-ui.KeyF1)+1)
	}
	n := keyNames[k]
	if display {
		return i18n.T(n[1])
	}
	return n[0]
}

// Accel 是命令在菜单里的快捷键写法，如 "Cmd+Alt+L"；没有快捷键为空。
func (c *Command) Accel() string {
	if c.Key == 0 {
		return ""
	}
	var parts []string
	for _, m := range []struct {
		mod  ui.Modifiers
		name string
	}{{ui.Super, "Cmd"}, {ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}} {
		if c.Mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, keyName(c.Key, false)), "+")
}

// Keys 是命令快捷键的显示写法，按 macOS 的顺序（⌃⌥⇧⌘），如 "⌥⌘L"；没有快捷键为空。
func (c *Command) Keys() string {
	if c.Key == 0 {
		return ""
	}
	return keysOf(c.Mods, c.Key)
}

func keysOf(mods ui.Modifiers, key ui.Key) string {
	var b strings.Builder
	for _, m := range []struct {
		mod  ui.Modifiers
		sign string
	}{{ui.Ctrl, "⌃"}, {ui.Alt, "⌥"}, {ui.Shift, "⇧"}, {ui.Super, "⌘"}} {
		if mods&m.mod != 0 {
			b.WriteString(m.sign)
		}
	}
	b.WriteString(keyName(key, true))
	return b.String()
}

// Tip 是按钮的工具提示：text（已经过 i18n.T 的）后面带上命令的快捷键，如 "刷新（⌘R）"。
func Tip(text, id string) string {
	c := Find(id)
	if c == nil {
		panic("没有命令 " + id)
	}
	if k := c.Keys(); k != "" {
		if i18n.Lang() == "en" {
			return text + " (" + k + ")"
		}
		return text + "（" + k + "）"
	}
	return text
}

// KeyText 是命令 id 现在的快捷键（⇧⌘P 这样，keybindings.json 改过的跟着变），没绑键是空串。
func KeyText(id string) string {
	c := Find(id)
	if c == nil {
		panic("没有命令 " + id)
	}
	return c.Keys()
}

// KeyHint 是讲到快捷键的提示文案：命令 id 有键时是 with（格式串，args 后面再跟一个 %s 是键），
// 没键时是 without（同一句去掉讲键的那段，只用 args），不会剩下一对空括号。两句都是原文，这里过 i18n。
// 键只在括号里、整段去掉就行的用 Tip。
func KeyHint(id, with, without string, args ...any) string {
	if k := KeyText(id); k != "" {
		return i18n.Tf(with, append(slices.Clip(args), k)...)
	}
	return i18n.Tf(without, args...)
}

// KeyList 是几个命令现在的快捷键连成的一句列举（⌘S、⌥⌘S），没绑键的不列；都没有是空串。
func KeyList(ids ...string) string {
	var keys []string
	for _, id := range ids {
		if k := KeyText(id); k != "" {
			keys = append(keys, k)
		}
	}
	return i18n.List(keys)
}

// Label 是命令在菜单和命令面板里显示的名字（当前语言的）。
func (c *Command) Label() string {
	if n, ok := strings.CutPrefix(c.ID, "repo."); ok && len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
		return i18n.Tf("跳到第 %d 个仓库", int(n[0]-'0'))
	}
	return i18n.T(c.Title)
}

// ShortcutDoc 是 docs/shortcuts.md 的内容，由注册表生成（commands_test.go 用 -update 写文件、平时比对）。
func ShortcutDoc() string {
	var b strings.Builder
	b.WriteString("# 快捷键\n\n这个文件由 `go test ./internal/app -run TestShortcutDoc -update` 从命令注册表" +
		"（`internal/app/commands.go`）生成，不要手改。\n\n")
	b.WriteString("⌘P 快速打开和 ⇧⌘P 命令面板是同一个浮层：输入以 `>` 开头是命令，`>git ` 开头在当前仓库跑一条 git 命令" +
		"（⌘K 直接带着它打开，编辑器有焦点时也一样：菜单先接到 ⌘K），`:行号` 在当前文件跳行，" +
		"`名字:行号` 打开文件并跳到那一行；文件项上按 ⌥↩ 是在侧边打开。对话框和面板里 Esc 关闭。\n\n" +
		"同一个键在不同上下文里可以是不同的命令，按当前标签分派：⌥⌘F 在 Diff 标签里是全文 diff，在文件标签里是替换。" +
		"编辑器自带的 ⇧⌘G（找上一个）和 Git 侧栏同键：有菜单栏时菜单先接到，是 Git 侧栏；找上一个用查找条里的 ⇧↩。" +
		"编辑器自带的 ⇧⌘H（替换）同样让给侧栏的搜索并替换（所有仓库）；只在当前文件里替换用 ⌥⌘F。\n\n" +
		"终端里按住 ⌘ 点：文件路径（`path:行[:列]`，相对 shell 现在的目录，也认 `~/` 和 `file://`）在编辑器里固定打开到那一行，" +
		"仓库里的目录在文件树里显示，网址用系统浏览器开。\n\n" +
		"键盘在终端里时下面“终端”一节的键归终端，盖过同键的全局和编辑器命令：⌘W 关掉有键盘的那一格（只剩一格就关标签，" +
		"有命令在跑先问），⌘K 清屏（不再是带着 `>git ` 打开命令面板），⌘F / ⌘G / ⇧⌘G 在终端的回看里查找。" +
		"⌃⇧` 新建终端标签不管键盘在哪都行。\n\n" +
		"⌘= / ⌘- / ⌘0 是界面缩放（视图菜单，所有窗口一起，状态栏右边显示不是 100% 的缩放）；键盘在终端里也是：" +
		"终端自带的字号键（⌘= ⇧⌘= ⌘- ⌘0）让给了它，终端字号在设置的终端页改。设置窗口里 ⌘F 到搜索框，有字时 Esc 清空。\n")
	for ctx := Global; ctx <= SearchList; ctx++ {
		var rows []string
		for _, c := range Commands {
			if c.Context == ctx && c.Key != 0 {
				title := c.Title
				if c.Group != "" {
					title = c.Group + " › " + title
				}
				rows = append(rows, fmt.Sprintf("| %s | %s |", c.Keys(), title))
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| 快捷键 | 命令 |\n| --- | --- |\n%s\n", ctx, strings.Join(rows, "\n"))
	}
	var rows []string
	for _, c := range Commands {
		if c.Section != "" {
			rows = append(rows, fmt.Sprintf("| %s | %s | %s |", cmp.Or(c.Hint, "—"), c.Section, c.Title))
		}
	}
	fmt.Fprintf(&b, "\n## 编辑器自带（mygo 默认键）\n\n"+
		"编辑器（mygo）自带的命令都在 ⇧⌘P 命令面板里，中文名、英文名（如 `foldAll`）都搜得到，分组写“编辑器 · 折叠”这样。"+
		"编辑器有焦点时也能按下表的键（编辑器自己接，不在菜单栏里）。写“—”的是 mygo 默认键的第一下被菜单栏占了，只能从命令面板跑："+
		"⌘K 是“运行 git 命令…”，菜单先接到，所以 mygo 的 ⌘K 和弦（⌘K ⌘0 全部折叠、⌘K ⌘L 折叠 / 展开、⌘K ⌘/ 块注释……）在窗口里都用不了；"+
		"⇧⌘U（撤销选区变化之后的重做）是拉取，⌥⌘L（在选区里查找）是统一 diff，⇧⌘G（查找上一个）是 Git 侧栏。\n\n"+
		"会改文字的在只读的标签（历史版本、大文件）上不能用；跳改动、还原改动要有 HEAD 对比；全部折叠、折叠到第 N 级、递归折叠、"+
		"选中所有相同的、把选区拆成每行一个光标要扫一遍整篇，%d MB 以上的文件上不能用。\n\n| 快捷键 | 分组 | 命令 |\n| --- | --- | --- |\n%s\n",
		MaxWholeTextBytes>>20, strings.Join(rows, "\n"))
	b.WriteString("\n## 自定义\n\n" +
		"上面是默认键，可以改：设置 → 快捷键里选中一条命令点“改…”按新键（⌫ 是不要快捷键，Esc 不改），" +
		"或者直接编辑 `<状态目录>/keybindings.json`（平时是 `~/Library/Application Support/Grove/keybindings.json`，" +
		"快捷键页右上 ⋯ → 打开 keybindings.json）。文件是一个数组，一条管一个命令：\n\n```json\n" +
		`[
  {"command": "palette.commands", "key": "cmd+k"},
  {"command": "git.command", "key": ""}
]` + "\n```\n\n" +
		"- `command` 是命令 id：快捷键页里鼠标停在命令名上看得到，那里的搜索框也搜 id。`key` 为空是去掉这条命令的键；" +
		"删掉那一条（或在快捷键页点“重置”）是回到默认。\n" +
		"- `key` 是修饰键加键，用 `+` 连，大小写随意：修饰键 `cmd`（`command`、`meta`、`⌘`）、`ctrl`（`control`、`⌃`）、" +
		"`alt`（`option`、`opt`、`⌥`）、`shift`（`⇧`），符号写在开头时可以不用 `+`（`⇧⌘P`）；" +
		"键是 `a`–`z`、`0`–`9`、`f1`–`f12`、`enter`、`escape`（`esc`）、`backspace`、`tab`、`space`、`delete`、`insert`、" +
		"`home`、`end`、`pageup`、`pagedown`、`left`、`right`、`up`、`down` 和 `` - = , . / ; ' [ ] \\ ` ``" +
		"（也可以写 `minus`、`equal`、`comma`、`period`、`slash`、`semicolon`、`quote`、`bracketleft`、`bracketright`、" +
		"`backslash`、`backquote`）。\n" +
		"- 冲突按键由谁接来算：窗口（全局、Diff 标签、编辑器）、终端、文件树、提交框、搜索结果各是一处。同一处里同一个键，" +
		"上下文相同或有一个是全局才算冲突（⌥⌘F 在 Diff 标签和编辑器里各是一个命令不算）：改了的赢，被顶掉的命令没有键，" +
		"快捷键页那一行写着被谁占了；文件里两条改成同一个键，靠后的赢。\n" +
		"- 编辑器自带的命令（`editor.*`，上一张表）也能改，改了以后 mygo 的默认键不再起作用。\n" +
		"- 不做和弦（⌘K ⌘0 这样按两下的）和 `when` 条件。\n" +
		"- 快捷键页写这个文件时一条一行整个重写，文件里的注释会丢；看不懂的条目原样留着，快捷键页顶上列出来，其余照常生效。" +
		"整个文件读不了（不是 JSON 数组）就全用默认键，快捷键页也不往里写，免得盖掉；在编辑器里改好保存马上生效。\n")
	return b.String()
}
