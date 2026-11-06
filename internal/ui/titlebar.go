package ui

import (
	"log"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/files"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// titleBar 占着窗口按钮那一行（整行可拖）：
//
//	左：侧栏开关、工作区菜单、‹ ›、面包屑（组和组之间 8 pt）
//	中：快速打开（⌘P）
//	右：当前仓库的分支胶囊、抓取 / 拉取 / 推送、⋯ Git 操作，右栏和底栏开关
//
// 左右两组都 Grow(1)（基准宽 0），所以中间的按钮正好居中。行高固定 barHeight，红绿灯（TrafficLights）
// 和图标的中心都在 barHeight/2；字的墨迹比图标低半点，标题栏上的字都加 textNudge 往上挪回来。
func titleBar(a *app.App, c *wui.Context) {
	bar := c.TitleBar()
	if bar != a.LastBar { // 红绿灯占的位置变了（启动、全屏）记一笔，对齐用；按窗口记
		a.LastBar = bar
		log.Printf("标题栏：窗口按钮 %+v，行高 %v，红绿灯放在 %+v", bar, barHeight, TrafficLights)
	}
	r := a.Current()
	tb := wui.Row(c).Height(barHeight).Padding(0, bar.Right+8, 0, bar.Left+8).Gap(10).
		AlignItems(wui.Center).Shrink(0).DragWindow()
	tb.Children(func() {
		wui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(wui.Center).Children(func() {
			toggleButton(c, iconSidebar, app.Tip(i18n.T("侧栏"), "view.sidebar"), a.St.Sidebar, func() { a.Exec("view.sidebar") })
			wsButton(a, c)
			// ‹ › 挨着，看着是一对
			wui.Row(c).AlignItems(wui.Center).Shrink(0).Children(func() {
				back, fwd := app.Tip(i18n.T("后退"), "nav.back"), app.Tip(i18n.T("前进"), "nav.forward")
				toolButton(c, iconChevronLeft, back, !a.CanBack(), a.Back)
				toolButton(c, iconChevronRight, fwd, !a.CanForward(), a.Forward)
			})
			breadcrumb(a, c, tb)
		})
		quickOpen(a, c)
		wui.Row(c).Grow(1).MinWidth(0).Gap(2).AlignItems(wui.Center).Justify(wui.End).Children(func() {
			if r != nil {
				repoControls(a, c, r)
				divider(c)
			}
			toggleButton(c, iconRightbar, app.Tip(i18n.T("右栏（历史）"), "view.right"), a.St.RightBar, func() { a.Exec("view.right") })
			toggleButton(c, iconBottombar, app.Tip(i18n.T("底栏（终端 / 输出）"), "view.bottom"), a.St.BottomBar, func() { a.Exec("view.bottom") })
		})
	})
}

// wsButton 是工作区名（空工作区写"没有打开的工作区"），点开是工作区菜单。
func wsButton(a *app.App, c *wui.Context) {
	t := c.Theme()
	name, label := a.WsName(), i18n.Tf("工作区 %s", a.WsName())
	if a.WsPath == "" {
		name = i18n.T("没有打开的工作区")
		label = name
	}
	b := wui.ButtonBase(c).Label(label).Gap(4).AlignItems(wui.Center).Padding(3, 6).Radius(6).
		Cursor(wui.CursorPointer).Tooltip(a.WsPath).MinWidth(0).MaxWidth(200).Shrink(0) // 面包屑先缩
	if b.Hovered() {
		b.Background(toolFill(t, 0.06))
	}
	b.Children(func() {
		wui.Icon(c, iconLayers).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, name).FontSize(12.5).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, textNudge, 0)
		wui.Icon(c, iconChevron).FontSize(iconInline).TextColor(t.TextMuted)
	})
	menuButton(a, c, b, menuOwner{"workspace", ""}, func(m menu) { wsMenu(a, m) })
}

// breadcrumb 是当前标签的位置：仓库 › 目录 › 文件，目录太深时中间收成"…"，位置不够时见 crumbFit。
// 仓库和目录段点开是那个目录的菜单（在文件树里显示、子目录、文件），文件段点了在文件树里显示它。
// 每段的文字带标签"位置 仓库/路径"，免得测试按文字找文件树里同名的行时找到这里。仓库外的文件只显示路径。
// tb 是整条标题栏，用来算这一帧窗口变了多宽。
func breadcrumb(a *app.App, c *wui.Context, tb wui.Element) {
	t := c.Theme()
	d := a.CurrentDoc()
	if d == nil {
		return
	}
	tip := d.Abs
	if tip == "" {
		tip = d.Where()
	}
	r, rel := a.RepoOf(d), d.Rel
	if d.Kind == docs.Compare && d.Snapshot != 0 { // 本地历史比较：左边是历史里的内容文件，面包屑给工作区里的那个
		tip = d.Right
		r, rel = a.LocalCompareFile(d)
	}
	// 这一行 Grow(1)，宽度就是左边那组剩下的位置、和内容无关：拿上一帧的宽度，窗口这一帧变了多宽，左右两组各分一半。
	// 新出来的这一行还没有上一帧，先不限宽、再要一帧。ClipX 兜底：算差了也不画到快速打开上。
	row := wui.Row(c).Gap(1).AlignItems(wui.Center).MinWidth(0).Grow(1).Shrink(1).ClipX()
	avail := row.LayoutBounds().W
	if avail == 0 {
		c.AnimationFrame()
	} else if w, _ := c.Size(); tb.LayoutBounds().W > 0 {
		avail += (w - tb.LayoutBounds().W) / 2
	}
	row.Children(func() {
		// 标签的类型（文件、Diff、历史版本）的图标挨着最后的文件名那段
		// 文件按类型上色（fileGlyph）
		ic := iconFile
		switch d.Kind {
		case docs.Diff:
			ic = iconDiff
		case docs.Version:
			ic = iconHistory
		case docs.TermText, docs.Term:
			ic = iconTerminal
		case docs.ReplacePreview:
			ic = iconReplace
		case docs.Compare:
			ic = iconDiff
		case docs.SearchDoc:
			ic = iconSearchDoc
		case docs.SettingsDoc:
			ic = iconSettings
		case docs.Rebase:
			ic = iconRebase
		}
		kind := func() { // 墨迹（彩色底块是块的边）离文件名 3.5
			if ic == iconFile {
				gap := float32(-2)
				if a.St.Settings.TreeIconsPlain {
					gap = -3.5
				}
				fileGlyph(a, c, d.Abs, false).Shrink(0).Margin(0, gap, 0, 2)
				return
			}
			wui.Icon(c, ic).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0).Margin(0, -3.5, 0, 2)
		}
		if r == nil || rel == "" {
			kind()
			wui.Text(c, tip).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(tip).
				Margin(0, 0, textNudge, 4)
			return
		}
		dirs := strings.Split(rel, "/")
		file := dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]
		if d.Commit != "" {
			file += " @ " + git.Short(d.Commit)
		}
		l := crumbFit(c, avail, r.Name, dirs, file)
		crumb(a, c, r, "", r.Name, false).MaxWidth(l.repoMax)
		sep := func() {
			wui.Text(c, "›").FontSize(12).TextColor(t.TextMuted.Alpha(0.6)).Shrink(0).Margin(0, 0, textNudge, 0)
		}
		for i := 0; i < len(dirs); i++ {
			sep()
			if i == l.lo && l.hi > l.lo {
				crumbMore(a, c, r, dirs, l.lo, l.hi)
				i = l.hi - 1
				continue
			}
			crumb(a, c, r, strings.Join(dirs[:i+1], "/"), dirs[i], false)
		}
		sep()
		kind()
		crumb(a, c, r, rel, file, true).MaxWidth(l.fileMax).Tooltip(i18n.Tf("%s\n点一下在文件树里显示", tip))
	})
}

// crumbDirs 是面包屑最多显示几层目录：第一层和最后几层，中间的收成"…"。
const crumbDirs = 2

// 面包屑各段最宽、挤的时候最窄多少（DIP）。
const (
	crumbRepoMax, crumbDirMax, crumbFileMax = 220, 160, 260
	crumbRepoMin, crumbDirMin, crumbFileMin = 60, 40, 80
)

// crumbLayout 是面包屑这一帧怎么摆：目录 dirs[lo:hi] 收成一个"…"，仓库段和文件段最宽多少。
type crumbLayout struct {
	lo, hi           int
	repoMax, fileMax float32
}

// crumbFit 算面包屑这一帧怎么摆。目录多于 crumbDirs 层时中间的收起来。位置不够时一步步让：
// 先缩目录段（一行里只有它们 Shrink），按每段缩到 crumbDirMin 算还放不下，就把目录全收进"…"
// （仓库 › … › 文件）；再不够缩文件段，文件段到 crumbFileMin 了才缩仓库段。
// avail 是这一行能用的宽度，0 为还不知道（只收中间的目录）。
func crumbFit(c *wui.Context, avail float32, repo string, dirs []string, file string) crumbLayout {
	l := crumbLayout{repoMax: crumbRepoMax, fileMax: crumbFileMax}
	if n := len(dirs) - crumbDirs; n > 0 {
		l.lo, l.hi = 1, n+1
	}
	if avail <= 0 {
		return l
	}
	w := func(s string) float32 {
		x, _ := c.MeasureText(0, wui.Span{Text: s, Size: 12})
		return x + 8 // 左右各 4 的内边距
	}
	sep := w("›") - 8 + 2 // 分隔符和它两边各 1 的间隔
	more := sep + w("…")
	room := avail - 10.5 - sep // 文件段前面的类型图标（12，左右边距 2 和 −3.5）和分隔符
	repoW, fileW := min(w(repo), crumbRepoMax), min(w(file), crumbFileMax)
	var dirsW float32
	if l.hi > l.lo {
		dirsW = more
	}
	for i, d := range dirs {
		if i < l.lo || i >= l.hi {
			dirsW += sep + min(w(d), crumbDirMin)
		}
	}
	if repoW+dirsW+fileW <= room {
		return l
	}
	if len(dirs) > 0 {
		l.lo, l.hi = 0, len(dirs)
		room -= more
	}
	if f := max(room-repoW, crumbFileMin); f < fileW {
		fileW, l.fileMax = f, f
	}
	if r := max(room-fileW, crumbRepoMin); r < repoW {
		l.repoMax = r
	}
	return l
}

// crumbMore 是面包屑里收起来的目录 dirs[lo:hi]：点开列出它们，每个是那个目录的菜单。
func crumbMore(a *app.App, c *wui.Context, r *app.Repo, dirs []string, lo, hi int) {
	t := c.Theme()
	b := wui.ButtonBase(c).Label(i18n.T("位置 省略的目录")).Padding(2, 4).Radius(5).Shrink(0).Cursor(wui.CursorPointer).
		Tooltip(strings.Join(dirs[lo:hi], "/"))
	if b.Hovered() {
		b.Background(toolFill(t, 0.06))
		a.EnsureFiles(r)
	}
	b.Children(func() { wui.Text(c, "…").FontSize(12).TextColor(t.TextMuted).SingleLine().Margin(0, 0, textNudge, 0) })
	b.Menu(func(m *wui.Menu) {
		for i := lo; i < hi; i++ {
			path := strings.Join(dirs[:i+1], "/")
			m.Submenu(dirs[i], func(m *wui.Menu) { dirMenu(a, m, r, path, 0) })
		}
	})
}

// crumbMenuMax 是面包屑菜单每一层最多列几项，再多用快速打开找。
const crumbMenuMax = 80

// crumb 是面包屑的一段：file 为最后一段（点了在文件树里显示），否则是目录（空 path 为仓库），点开菜单。
func crumb(a *app.App, c *wui.Context, r *app.Repo, path, text string, file bool) wui.Element {
	t := c.Theme()
	label := i18n.Tf("位置 %s", r.Name)
	switch {
	case file:
		label += "/" + files.JoinPath(files.DirOf(path), text) // 历史版本带着 "@ 提交"
	case path != "":
		label += "/" + path
	}
	// 位置不够时只缩目录段，仓库段和文件段不让、由 breadcrumb 按 crumbFit 给最宽多少
	b := wui.ButtonBase(c).Label(label).Padding(2, 4).Radius(5).MinWidth(0).Shrink(1).MaxWidth(crumbDirMax).Cursor(wui.CursorPointer)
	if file || path == "" {
		b.Shrink(0)
	}
	if b.Hovered() {
		b.Background(toolFill(t, 0.06))
		a.EnsureFiles(r) // 先读文件列表，点开菜单时就有了
	}
	color := t.TextMuted
	if file {
		color = t.Text
	}
	b.Children(func() {
		wui.Text(c, text).Label(label).FontSize(12).TextColor(color).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, textNudge, 0)
	})
	if file {
		if b.Clicked() {
			a.Reveal(r, path)
		}
		return b
	}
	b.Tooltip(r.Root + strings.TrimSuffix("/"+path, "/"))
	b.Menu(func(m *wui.Menu) { dirMenu(a, m, r, path, 0) })
	return b
}

// dirMenu 是面包屑里目录段的菜单：在文件树里显示，然后是目录里的子目录（子菜单，最多两层）和文件（选了打开）。
func dirMenu(a *app.App, m *wui.Menu, r *app.Repo, dir string, depth int) {
	a.EnsureFiles(r)
	if depth == 0 {
		if m.Item(i18n.T("在文件树里显示")).Chosen() {
			a.Reveal(r, dir)
		}
		if m.Item(i18n.T("在此打开终端")).Chosen() {
			a.NewTerminal(r, filepath.Join(r.Root, filepath.FromSlash(dir)))
		}
		m.Separator()
	}
	n := r.Files.Dir(dir)
	switch {
	case n == nil && !r.Files.Loaded:
		m.Item(i18n.T("正在读文件列表…")).Disabled(true)
		return
	case n == nil || len(n.Dirs)+len(n.Files) == 0:
		m.Item(i18n.T("（没有文件）")).Disabled(true)
		return
	}
	k := 0
	for _, sub := range n.Dirs[:min(len(n.Dirs), crumbMenuMax)] {
		k++
		if depth < 2 {
			m.Submenu(sub.Name, func(m *wui.Menu) { dirMenu(a, m, r, sub.Path, depth+1) })
		} else if m.Item(sub.Name + "/").Chosen() {
			a.Reveal(r, sub.Path)
		}
	}
	for _, name := range n.Files[:min(len(n.Files), crumbMenuMax-k)] {
		k++
		if m.Item(name).Chosen() {
			a.OpenFile(r, files.JoinPath(dir, name), true)
			a.FocusDoc = true
		}
	}
	if rest := len(n.Dirs) + len(n.Files) - k; rest > 0 {
		m.Item(i18n.Tf("还有 %d 项，用快速打开（%s）找", rest, app.Find("file.quickOpen").Keys())).Disabled(true)
	}
}

// quickOpen 是标题栏中间的"快速打开"按钮（= ⌘P，打开快速打开面板）。
func quickOpen(a *app.App, c *wui.Context) {
	t := c.Theme()
	tip := app.Tip(i18n.T("按文件名快速打开"), "file.quickOpen")
	b := wui.ButtonBase(c).Label(i18n.T("快速打开")).Gap(6).AlignItems(wui.Center).Padding(3, 10).Radius(6).Width(260).MinWidth(120).Shrink(1).
		Background(t.Text.Alpha(0.05)).BorderWidth(1).BorderColor(t.Border).Cursor(wui.CursorPointer).Tooltip(tip)
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.08))
	}
	b.Children(func() {
		wui.Icon(c, iconSearch).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, i18n.T("快速打开")).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0).Margin(0, 0, textNudge, 0)
		wui.Text(c, app.Find("file.quickOpen").Keys()).FontSize(11).TextColor(t.TextMuted.Alpha(0.7)).SingleLine().Shrink(0).Margin(0, 0, textNudge, 0)
	})
	if b.Clicked() {
		a.Exec("file.quickOpen")
	}
}

// repoControls 是标题栏右边当前仓库的分支胶囊、同步按钮和"⋯ Git 操作"。
func repoControls(a *app.App, c *wui.Context, r *app.Repo) {
	t := c.Theme()
	switch {
	case r.Err != nil:
		chip(c, iconAlert, i18n.Tf("%s：git status 失败", r.Name), t.Danger, t.Danger.Alpha(0.12)).Tooltip(r.Err.Error())
		return
	case !r.Loaded:
		wui.Spinner(c)
		return
	}
	if r.Running() != nil || len(r.Queue) > 0 {
		queueButton(a, c, r)
	}
	if r.Plain { // 普通文件夹：灰的"无 Git"、点不了，git 按钮都灰着
		chip(c, iconBranch, i18n.T("无 Git"), t.TextMuted, t.Text.Alpha(0.06)).Margin(0, 4, 0, 0).
			Tooltip(i18n.Tf("%s 不是 Git 仓库", r.Name))
		toolButton(c, iconFetch, i18n.T("抓取（git fetch --prune）"), true, nil)
		toolButton(c, iconPull, i18n.T("拉取"), true, nil)
		toolButton(c, iconPush, i18n.T("推送（git push）"), true, nil)
		toolButton(c, iconMore, i18n.T("Git 操作"), true, nil)
		return
	}
	branchButton(a, c, r)
	s := syncOf(a, r)
	fetchTip, fetchWhy := i18n.T("抓取（git fetch --prune）"), app.FetchWhy(r)
	if fetchWhy != "" {
		fetchTip = withWhy(i18n.T("抓取"), fetchWhy)
	}
	toolButton(c, iconFetch, fetchTip, fetchWhy != "", func() { a.Fetch(r) })
	toolButton(c, iconPull, s.pullTip, s.pullOff, func() { a.Pull(r) })
	push := toolButton(c, iconPush, s.pushTip, s.pushOff, func() { a.Push(r) })
	ctxMenu(a, c, push, menuOwner{"push", r.Root}, func(m menu) { pushMenu(a, m, r) })
	more := wui.ButtonBase(c).Label(i18n.T("Git 操作")).Size(toolSize, toolSize).Tooltip(i18n.T("更多 git 操作"))
	fg := toolStyle(c, more, false, false)
	more.Children(func() {
		wui.Icon(c, iconMore).FontSize(iconTool).TextColor(fg)
	})
	menuButton(a, c, more, menuOwner{"gitMore", r.Root}, func(m menu) { gitMenu(a, m, r) })
}

// syncState 是一个仓库的拉取 / 推送按钮该怎么画：悬停提示（带 git 命令和领先 / 落后的数）、灰不灰。
// 标题栏的同步按钮和 Git 栏段头的那一组共用它，两处的说法和规矩一样。
type syncState struct {
	pullTip, pushTip string
	pullOff, pushOff bool // 灰着、点了没反应
	pulling, pushing bool // 这个仓库的拉取 / 推送在跑或在排队
	noUpstream       bool
	ahead, behind    int
}

func syncOf(a *app.App, r *app.Repo) syncState {
	s := syncState{
		pullTip:    i18n.Tf("拉取（git %s）", strings.Join(app.PullArgs(a.St.Settings.PullMode), " ")),
		pushTip:    i18n.T("推送（git push）"),
		noUpstream: app.Upstream(r) == "",
		ahead:      r.Status.Ahead,
		behind:     r.Status.Behind,
		pulling:    r.Pulling(),
		pushing:    r.Pushing(),
	}
	if s.noUpstream {
		s.pushTip = i18n.T("推送并设置上游（git push -u origin）")
	}
	if s.behind > 0 {
		s.pullTip += i18n.Tf("，落后 %s", strconv.Itoa(s.behind))
	}
	if s.ahead > 0 {
		s.pushTip += i18n.Tf("，领先 %s", strconv.Itoa(s.ahead))
	}
	// 不能做的原因都从 app 的 PullWhy / PushWhy 来（和菜单、命令面板同一处）
	if why := app.PullWhy(r); why != "" {
		s.pullTip, s.pullOff = withWhy(i18n.T("拉取"), why), true
	}
	if why := app.PushWhy(r); why != "" {
		s.pushTip, s.pushOff = withWhy(i18n.T("推送"), why), true
	}
	if s.pulling {
		s.pullTip = i18n.Tf("正在%s…", i18n.T("拉取"))
	}
	if s.pushing {
		s.pushTip = i18n.Tf("正在%s…", i18n.T("推送"))
	}
	return s
}

// pushMenu 是推送按钮的右键菜单：强制推送、同步。
func pushMenu(a *app.App, m menu, r *app.Repo) {
	if whyItem(m, i18n.T("强制推送（--force-with-lease）…"), app.PushForceWhy(r)).Danger().Chosen() {
		a.PushForce(r)
	}
	if whyItem(m, i18n.T("同步（拉取再推送）"), app.SyncWhy(r)).Chosen() {
		a.Sync(r)
	}
}

// branchButton 是分支名的胶囊，点开是本地分支和远程分支的菜单，可以切换和新建。
func branchButton(a *app.App, c *wui.Context, r *app.Repo) {
	t := c.Theme()
	label := r.Status.Branch
	if s := app.UpDown(r.Status); s != "" {
		label += "  " + s
	}
	b := wui.ButtonBase(c).Label(i18n.Tf("分支 %s", r.Status.Branch)).Gap(5).AlignItems(wui.Center).Padding(2, 6, 2, 8).Radius(999).
		Background(t.Text.Alpha(0.06)).Cursor(wui.CursorPointer).MinWidth(0).Shrink(1).Margin(0, 4, 0, 0).
		Tooltip(i18n.Tf("切换 %s 的分支", r.Name))
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.1))
	}
	b.Children(func() {
		wui.Icon(c, iconBranch).FontSize(iconInline).TextColor(t.TextMuted)
		wui.Text(c, label).FontSize(11).FontWeight(500).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, textNudge, 0)
		wui.Icon(c, iconChevron).FontSize(iconInline).TextColor(t.TextMuted)
	})
	if b.Clicked() {
		openMenuBelow(a, c, b.Bounds(), menuOwner{"branch", r.Root}, func(m menu) { branchMenu(a, m, r) })
	}
}

// copyText 把文字放进剪贴板；测试里换成记下来的（测试不碰剪贴板）。
var copyText = func(s string) { mygo.Clipboard.WriteText(s) }
