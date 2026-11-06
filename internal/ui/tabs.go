package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/workspace"
)

// mainArea 是主区：一组编辑器，或分屏的几组（左右 / 上下、可以嵌套），组之间一条 4 pt 能拖的缝。
func mainArea(a *app.App, c *wui.Context) {
	edNode(a, c, a.Editors(), "")
}

type groupKey struct{ g *app.Group }

// edNode 画编辑器组的拆分树：叶子是一组（自己一张卡片），别的是左右（Row）/ 上下排开的两半，中间一条能拖的缝
// （和终端分屏同一个 splitDivider）。
func edNode(a *app.App, c *wui.Context, n *app.EdNode, path string) {
	if n.G != nil {
		groupCard(a, c.Key(groupKey{n.G}), n.G)
		return
	}
	if n.Ratio <= 0 || n.Ratio >= 1 {
		n.Ratio = 0.5
	}
	// 松开拖着的标签是在画某一组时发生的，那时树可能就地收起 / 分开了：这一帧照拖之前的两半画完，下一帧才是新的
	na, nb := n.A, n.B
	// Row 和 Column 各建一个元素，只建用到的那个（见 termNode）
	key := "edsplit-" + path
	var box wui.Element
	if n.Row {
		box = wui.Row(c.Key(key))
	} else {
		box = wui.Column(c.Key(key))
	}
	box.Grow(1).Basis(0).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch)
	box.Children(func() {
		wui.Column(c.Key("a")).Grow(n.Ratio).Basis(0).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch).Children(func() {
			edNode(a, c, na, path+"a")
		})
		splitDivider(c, n.Row, &n.Ratio, box)
		wui.Column(c.Key("b")).Grow(1 - n.Ratio).Basis(0).MinWidth(0).MinHeight(0).AlignItems(wui.Stretch).Children(func() {
			edNode(a, c, nb, path+"b")
		})
	})
}

// groupCard 是一组编辑器：自己一张卡片，一条标签条，下面是当前标签的内容——编辑器、图片、只读的历史版本，
// 或 Diff（底部一行说明比较的两侧、+/− 统计和 局部 | 全文、并排 | 统一 两组切换）。
// 分屏时键盘进到哪组哪组就是有键盘的组（顶上一条强调色细线，和终端分屏同一个记号）；拖着标签进到内容区
// 按五块画落点预览，松开才搬 / 分（dropZone）。
func groupCard(a *app.App, c *wui.Context, g *app.Group) {
	box := wui.Column(c).Grow(1).MinHeight(0).MinWidth(0)
	if fw := box.FocusWithin(); fw != g.HadFocus {
		g.HadFocus = fw
		if fw {
			a.FocusGroup(g)
		}
	}
	if b := box.Bounds(); b.W > 0 {
		g.W, g.H = b.W, b.H
	}
	ds, sel := a.GroupDocs(g)
	var d *docs.Doc
	if sel >= 0 && sel < len(ds) {
		d = ds[sel]
	}
	if d == nil {
		g.TabShown = nil // 标签都关了：别留着最后那个（连同它的缓冲区）
	}
	split, focused := a.Split(), g == a.Grp
	// 分屏时程序换了组（搬标签、收组、‹ ›）：先把键盘给这张卡片，内容里的编辑器再拿过去；Diff、预览、图片
	// 拿不了也行，键盘至少离开了原来那组，回去点它才算进去
	want := split && focused && a.FocusDoc && d != nil && !d.Loading
	if want {
		box.Focus()
	}
	rows := treeDropOn(a, box, g)
	card(c, box, func() {
		t := c.Theme()
		if len(ds) > 0 {
			tabBar(a, c.Key("tabs"), g, d, rows)
		}
		body := wui.Column(c.Key("body")).Grow(1).MinHeight(0).AlignItems(wui.Stretch)
		side := dropSide(a, g, body)
		if rows && len(ds) == 0 { // 没有标签条：整个内容区当落点
			side = app.SideCenter
		}
		body.Children(func() {
			groupContent(a, c, d, focused)
			if side != app.SideNone {
				b := body.Bounds()
				x, y, w, h := app.DropRect(side, b.W, b.H)
				wui.Box(c.Key("dropZone")).Absolute().Left(x + 4).Top(y + 4).Width(max(w-8, 0)).Height(max(h-8, 0)).
					Radius(cardRadius).Background(t.Accent.Alpha(0.14))
			}
		})
		if split && focused {
			wui.Box(c.Key("bar")).Absolute().Top(0).Left(0).Right(0).Height(2).Background(t.Accent.Alpha(0.8))
		}
	})
	if want {
		a.FocusDoc = false
	}
}

// dropSide 是拖着的标签在这组内容区上的落点：按指针离四边多近分五块（app.DropSide），放了不动的为 SideNone；
// 松开就搬（中间）或分屏（四边）。
func dropSide(a *app.App, g *app.Group, body wui.Element) app.Side {
	b := body.Bounds()
	if v, ok := wui.Drop[*docs.Doc](body); ok {
		x, y, _ := body.PointerPosition()
		a.DropDoc(v, g, app.DropSide(x, y, b.W, b.H))
		return app.SideNone
	}
	v, _ := wui.DragOver[*docs.Doc](body)
	if v == nil {
		return app.SideNone
	}
	x, y, _ := body.PointerPosition()
	if s := app.DropSide(x, y, b.W, b.H); a.CanDrop(v, g, s) {
		return s
	}
	return app.SideNone
}

// treeDropOn 让一组编辑器（标签条 + 内容区）接文件树拖来的行：松手把里面的文件在这组打开成固定标签（app.OpenDropped）。
// 返回拖着停在上面、松手会开（里面有文件）——标签条淡淡一层强调色；只有目录时不开，拖着的行画不可放的光标。
// 内容区自己接的是拖着的标签（dropSide），各接各的类型，互不挡。
func treeDropOn(a *app.App, box wui.Element, g *app.Group) bool {
	if v, ok := wui.Drop[app.TreeDrag](box); ok {
		a.OpenDropped(v, g)
		return false
	}
	v, ok := wui.DragOver[app.TreeDrag](box)
	if ok && !v.HasFiles() {
		a.TreeDropNone = true
	}
	return ok && v.HasFiles()
}

// groupContent 是一组的当前标签的内容；没有标签时是空状态。
func groupContent(a *app.App, c *wui.Context, d *docs.Doc, focused bool) {
	t := c.Theme()
	switch {
	case d == nil && a.WsPath == "":
		message(c, iconLayers, i18n.T("没有打开的工作区"), app.KeyHint("file.openFolder", "%s 打开一个文件夹，或用侧栏的按钮打开工作区文件", "从文件菜单打开一个文件夹，或用侧栏的按钮打开工作区文件"), t.TextMuted)
	case d == nil && len(a.Repos) == 0:
		message(c, iconLayers, i18n.T("没有仓库"), app.KeyHint("repo.add", "用侧栏的 + 或 %s 添加一个 Git 仓库", "用侧栏的 + 添加一个 Git 仓库"), t.TextMuted)
	case d == nil:
		wui.Column(c).Grow(1).MinHeight(0).AlignItems(wui.Center).Justify(wui.Center).Gap(10).Padding(16).Children(func() {
			wui.Icon(c, iconFilePen).FontSize(iconEmpty).TextColor(t.TextMuted.Alpha(0.8))
			wui.Text(c, i18n.T("没有打开的文件")).FontSize(13).FontWeight(600).TextColor(t.TextMuted)
			wui.Text(c, app.KeyHint("file.quickOpen", "在侧栏里点一个文件或一项变更，或 %s 按名字找", "在侧栏里点一个文件或一项变更，或用快速打开按名字找")).FontSize(11).TextColor(t.TextMuted.Alpha(0.8)).Center()
			if wui.Button(c, i18n.T("打开文件…")).Clicked() {
				a.ChooseFiles()
			}
		})
	case d.Kind == docs.Diff:
		diffContent(a, c, d)
	case d.Kind == docs.ReplacePreview:
		replaceContent(a, c, d)
	case d.Kind == docs.Compare:
		compareContent(a, c, d)
	case d.Kind == docs.Rebase:
		rebaseContent(a, c, d, focused)
	case d.Kind == docs.Term:
		termDoc(a, c, d, focused)
	default:
		docContent(a, c, d, focused)
	}
}

// termDoc 是编辑器区的终端标签：和底栏同一套分屏树（termNode），切到这个标签时键盘给它有键盘的那一格。
func termDoc(a *app.App, c *wui.Context, d *docs.Doc, focused bool) {
	r, tab := a.TermTabOf(d)
	if tab == nil { // 终端已经没了（不该有：关终端时连标签一起关）
		message(c, iconTerminal, i18n.T("终端已经关了"), "", c.Theme().TextMuted)
		return
	}
	if focused && a.FocusDoc {
		a.FocusDoc = false
		a.FocusTermTab(tab)
	}
	wui.Box(c).Grow(1).MinHeight(0).Children(func() {
		termNode(a, c.Key(termTabKey{r.Root, tab.Num}), r, tab, tab.Root, "")
	})
}

// termDocTools 是编辑器区终端标签卡片头的右端：同底栏标题行的那几个（查找、分屏、关一格、重新启动），加挪回底栏。
func termDocTools(a *app.App, c *wui.Context, d *docs.Doc) {
	r, tab := a.TermTabOf(d)
	if tab == nil {
		return
	}
	t := c.Theme()
	p := tab.Current()
	switch {
	case p.Err != nil:
		chip(c, iconAlert, i18n.T("启动失败"), t.Danger, t.Danger.Alpha(0.12))
	case p.Exited:
		chip(c, nil, i18n.Tf("已退出 %d", p.ExitCode), t.TextMuted, t.Text.Alpha(0.06))
	}
	if p.Term != nil {
		iconButton(c, iconSearch, app.Tip(i18n.T("在终端里查找…"), "term.find"), func() { a.FindInPane(p) })
	}
	iconButton(c, iconColumns, app.Tip(i18n.T("左右分屏"), "term.splitRight"), func() { a.SplitPane(r, tab, p, true) })
	iconButton(c, iconRows, app.Tip(i18n.T("上下分屏"), "term.splitDown"), func() { a.SplitPane(r, tab, p, false) })
	if tab.Root.Pane == nil {
		iconButton(c, iconPaneClose, app.Tip(i18n.T("关闭有键盘的那一格分屏"), "term.closePane"), func() { a.ClosePane(r, tab, p) })
	}
	iconButton(c, iconRefresh, i18n.T("重新启动终端"), func() { a.RestartPane(r, p) })
	iconButton(c, iconBottombar, i18n.T("挪回底栏"), func() { a.MoveTermToBottom(d) })
}

// tabBar 是一组的卡片头：横向滚动的标签，右端是当前标签的动作（保存、看 diff、在编辑器打开；Diff 标签还有 diffTools）。
// 点别的组的这些按钮先把键盘换到那组，打开的东西也落在那组。rows：文件树的行拖着停在这组上、松手会开。
func tabBar(a *app.App, c *wui.Context, g *app.Group, d *docs.Doc, rows bool) {
	in := func(fn func()) func() { return func() { a.FocusGroup(g); fn() } }
	bar := cardHeader(c).Padding(0, 6, 0, 0).Gap(4) // 左边 0：第一个标签贴着卡片左边
	if rows {                                       // 文件树的行拖着停在这组上：同文件树里能放的目录行
		bar.Background(c.Theme().Accent.Alpha(0.12))
	}
	bar.Children(func() {
		tabStrip(a, c, g)
		if d == nil {
			return
		}
		r := a.RepoOf(d)
		if d.CanPreview() {
			ic, tip := iconEye, i18n.T("Markdown 预览")
			if docs.IsSVG(d.Name()) {
				tip = i18n.T("SVG 预览")
			}
			if d.Preview {
				ic, tip = iconCode, i18n.T("显示源码")
			}
			iconButton(c, ic, app.Tip(tip, "view.markdownPreview"), in(a.TogglePreview))
		}
		if d.Modified() {
			iconButton(c, iconSave, app.Tip(i18n.T("保存（写回磁盘）"), "file.save"), func() { a.Save(d) })
		}
		switch d.Kind {
		case docs.Term:
			termDocTools(a, c, d)
		case docs.Diff:
			diffTools(a, c, d, in, r != nil && d.Change.Code != 'D')
		case docs.ReplacePreview:
			diffTools(a, c, d, in, r != nil)
		case docs.Compare:
			diffTools(a, c, d, in, true)
		}
		switch {
		case d.Kind == docs.File && r != nil && app.ChangedIn(r, d.Rel) != 0:
			iconButton(c, iconDiff, i18n.T("查看它的 diff"), in(func() { a.ShowDiffFor(r, d.Rel) }))
		case d.Kind == docs.Diff && r != nil && d.Change.Kind == git.InCommit && d.Change.Code != 'D':
			iconButton(c, iconPencil, i18n.T("在编辑器里查看此版本"), in(func() { a.OpenVersion(r, d.Change) }))
		case d.Kind == docs.Diff && r != nil && d.Change.Code != 'D', d.Kind == docs.ReplacePreview && r != nil:
			iconButton(c, iconPencil, i18n.T("在编辑器中打开"), in(func() { a.OpenFile(r, d.Rel, true) }))
		case d.Kind == docs.Compare && d.Snapshot != 0: // 本地历史 ↔ 工作区
			iconButton(c, iconUndo, i18n.T("恢复到这一份"), in(func() { a.RestoreSnapshot(d) }))
			iconButton(c, iconPencil, i18n.T("在编辑器中打开"), in(func() { a.OpenPath(d.Right) }))
		case d.Kind == docs.Compare:
			iconButton(c, iconPencil, i18n.T("在编辑器中打开左边的文件"), in(func() { a.CompareLeft(d) }))
		}
	})
}

// stripHidden 是标签条的滚动区比标签条高出、裁掉的那一截：mygo 横向滚动条的轨道（条宽 6，上下各 3）。
const stripHidden = 12

// tabStrip 是一组横向滚动的编辑器标签，占满标签条除了右端按钮以外的地方。
func tabStrip(a *app.App, c *wui.Context, g *app.Group) {
	ds, sel := a.GroupDocs(g)
	scrollStrip(c, &g.TabStrip, true, func() {
		// 画的是一份拷贝：右键菜单里关标签、拖标签放下都在这一圈里改组里的标签
		for i, x := range slices.Clone(ds) {
			tab(a, c.Key(x), g, i, x, i == sel)
		}
	})
}

// scrollStrip 是卡片头里横向滚动的一排标签（编辑器、底栏的终端）。mygo 的横向滚动条 6 pt 宽、盖在标签的字上，宽度是整个窗口
// 一份的主题设置、单改不了这一个：所以滚动区比标签条高出 stripHidden、连同里面的滚动条裁掉，自己在底边画一条 2 pt 的细条，
// 指针在标签条上才有（VS Code 那样）。grow：占满剩下的地方；不然只有标签那么宽（标签少时后面的东西紧跟着最后一个标签），
// 挤不下了再缩、滚。
func scrollStrip(c *wui.Context, s *wui.ScrollState, grow bool, fn func()) {
	t := c.Theme()
	box := wui.Box(c.Key("stripBox")).MinWidth(0).Shrink(1).Height(headerHeight).Clip()
	if grow {
		box.Grow(1)
	}
	hover := box.Hovered()
	box.Children(func() {
		wui.ScrollHorizontal(c.Key("strip")).TrackScroll(s).Height(headerHeight+stripHidden).Shrink(0).Padding(0, 0, stripHidden, 0).
			AlignItems(wui.Center).Children(fn)
		if w := s.Bounds(c).W; hover && s.MaxX > 0 && w > 0 {
			thumb := max(w*w/(w+s.MaxX), 24)
			wui.Box(c.Key("thumb")).Absolute().Bottom(0).Left((w - thumb) * min(s.X/s.MaxX, 1)).Width(thumb).Height(2).Radius(1).
				Background(t.TextMuted.Alpha(0.45))
		}
	})
}

func tab(a *app.App, c *wui.Context, g *app.Group, i int, d *docs.Doc, active bool) {
	t := c.Theme()
	tr, tt := a.TermTabOf(d) // 编辑器区的终端：双击改名（同底栏的终端标签）
	if tt != nil && a.Rename.Tab == tt {
		renameTab(a, c, tr, tt)
		return
	}
	// 当前标签和卡身同色、盖住卡片头底下那条线；别的标签是头上的一个圆角块，悬停才有底色
	el := tooltip(c, panelTab(c, active).Label(i18n.Tf("标签 %s", d.Title)), d.Where())
	ctxMenu(a, c, el, menuOwner{"tab", fmt.Sprintf("%p", d)}, func(m menu) { tabMenu(a, m, d) })
	hover := el.Hovered()
	if active && g.TabShown != d { // 换了当前标签（点、新开的、关掉别的、后退 / 前进）：滚到看得见
		g.TabShown = d
		el.ScrollIntoView()
	}
	if el.Clicked() {
		a.SelectIn(g, i)
	}
	if el.DoubleClicked() {
		d.Pinned = true
		if tt != nil {
			a.StartRenameTerm(tr, tt)
		}
	}
	// 拖标签换位置：拖到哪个标签上就放到它的位置，那一边画一条强调色的竖线；别的组的标签拖过来插在它左边
	el.Drag(d)
	if el.Dragging() {
		el.Opacity(0.5)
	}
	over, _ := wui.DragOver[*docs.Doc](el)
	if v, ok := wui.Drop[*docs.Doc](el); ok && v != d {
		a.DropOnTab(v, d)
	}
	el.Children(func() {
		if over != nil && over != d {
			ds, _ := a.GroupDocs(g)
			dropMark(c, !slices.Contains(ds, over) || slices.Index(ds, over) > i)
		}
		docGlyph(a, c, d, false)
		if r := a.RepoOf(d); r != nil && r != a.Current() {
			wui.Text(c, r.Name+" /").FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0).Margin(0, 0, textNudge, 0)
		}
		fg := panelTabColor(c, el, active)
		if active && g != a.Grp && !hover { // 别的组的当前标签也是灰的：键盘不在那儿
			fg = t.TextMuted
		}
		title := panelTabText(c, d.Title, fg)
		if !d.Pinned {
			title.Italic()
		}
		if d.Copy > 0 { // 镜像：和正本共用改动
			tip := i18n.Tf("同一个文件的第 %d 份，改动是同一份", d.Copy+1)
			tooltip(c, wui.Icon(c.Key("copy"), iconCopy).FontSize(iconInline).TextColor(t.TextMuted).Shrink(0).Margin(0, 0, textNudge, 0).Label(tip), tip)
		}
		tabClose(a, c, d, active, hover)
	})
}

// docGlyph 是标签前面的图标：文件按名字（fileGlyph），Diff、历史版本、终端、搜索、设置、替换预览、比较、变基各有一个；
// 打开的编辑器那一行用同一个。on 为画在实心的强调色底上。
func docGlyph(a *app.App, c *wui.Context, d *docs.Doc, on bool) {
	var ic *wui.SVG
	switch d.Kind {
	case docs.Diff, docs.Compare:
		ic = iconDiff
	case docs.Version:
		ic = iconHistory
	case docs.TermText, docs.Term:
		ic = iconTerminal
	case docs.SearchDoc:
		ic = iconSearchDoc
	case docs.SettingsDoc:
		ic = iconSettings
	case docs.ReplacePreview:
		ic = iconReplace
	case docs.Rebase:
		ic = iconRebase
	default:
		fileGlyph(a, c, d.Title, on)
		return
	}
	col := c.Theme().TextMuted
	if on {
		col = c.Theme().AccentText.Alpha(0.75)
	}
	wui.Icon(c, ic).FontSize(iconRow).TextColor(col).Shrink(0)
	if d.Kind == docs.Term {
		if _, tab := a.TermTabOf(d); tab != nil {
			agentMark(c, a.TabAgent(tab))
		}
	}
}

// dropMark 是拖着的标签放下去的位置：标签左边（left）或右边一条 2 pt 的强调色竖线。
func dropMark(c *wui.Context, left bool) {
	m := wui.Box(c.Key("drop")).Absolute().Top(4).Bottom(4).Width(2).Radius(1).Background(c.Theme().Accent)
	if left {
		m.Left(0)
	} else {
		m.Right(0)
	}
}

// tabClose 是标签右边 18×18 的一格（位置总占着，免得标签宽度跟着鼠标跳）：
//   - 有没保存的改动、没悬停：一个点（当前标签也是点，正在改的那个最要看出没存）；
//   - 悬停（hover，鼠标在整个标签上），或没改动的当前标签（active）：×，点了关掉（有改动会问要不要保存）；
//   - 别的：空着。
//
// 和 VS Code 一样：有改动是 ●，鼠标移上去才变 ×。
func tabClose(a *app.App, c *wui.Context, d *docs.Doc, active, hover bool) {
	t := c.Theme()
	x := wui.Box(c.Key("x")).Size(18, 18).AlignItems(wui.Center).Justify(wui.Center).Radius(5).Shrink(0)
	switch {
	case d.Modified() && !hover:
		tip := i18n.T("有没保存的改动")
		tooltip(c, x, tip).Children(func() { wui.Box(c).Size(8, 8).Radius(4).Background(t.TextMuted).Label(tip) })
	case active || hover:
		label := i18n.Tf("关闭 %s", d.Title)
		tooltip(c, x.Label(label).Cursor(wui.CursorPointer), label)
		fg := t.TextMuted
		if x.Hovered() {
			x.Background(toolFill(t, 0.06))
			fg = t.Text
		}
		x.Children(func() { wui.Icon(c, iconClose).FontSize(iconRow).TextColor(fg) })
		if x.Clicked() {
			a.CloseDoc(d)
		}
	}
}

// docContent 是文件 / 历史版本标签的内容；focused 是它在有键盘的那组（FocusDoc 只归那组）。
func docContent(a *app.App, c *wui.Context, d *docs.Doc, focused bool) {
	t := c.Theme()
	switch {
	case d.Loading:
		loadingSlow(c, d)
	case d.Info != "":
		message(c, iconAlert, d.Info, d.Where(), t.TextMuted)
	case d.Bitmap != nil:
		imageView(a, c, d, d.Bitmap, focused)
	case d.Buf != nil && d.Preview:
		if d.Conflict {
			conflictBar(a, c, d)
		}
		if encodingReadOnly(d) {
			encodingBar(a, c, d)
		}
		if docs.IsSVG(d.Name()) {
			svgPreview(a, c, d, focused)
			break
		}
		if focused {
			a.FocusDoc = false
		}
		markdownPreview(a, c, d)
	case d.Buf != nil:
		if d.Conflict {
			conflictBar(a, c, d)
		}
		switch {
		case d.Hex:
			hexBar(a, c, d)
		case d.Big:
			bigBar(c, d)
		case d.NoBase:
			noBaseLine(c)
		}
		if encodingReadOnly(d) {
			encodingBar(a, c, d)
		}
		if d.Kind == docs.TermText {
			termTextBar(a, c, d)
		}
		if d.Kind == docs.SearchDoc {
			searchDocBar(a, c, d)
		}
		if d.Kind == docs.SettingsDoc {
			settingsDocBar(a, c, d)
		}
		blocks, flagged := a.Conflicted(d)
		if len(blocks) > 0 || flagged {
			mergeBar(a, c, d, blocks, flagged)
		}
		conflictMarks(c, &d.St, blocks)
		a.EditorOptions(&d.St, d.Indent)
		// 选中里的空白符号画淡些；光标在词上停稳后高亮这个词的其他出现
		codeTheme(&d.St, c.Theme())
		occurMarks(c, d)
		if d.Hex { // 一行 16 字节对齐着看：不折行、没有行号（偏移就是行号）和折叠
			d.St.LineNumbers, d.St.NoFolding, d.St.SoftWrap = editor.LineNumbersOff, true, false
		}
		a.SyncInlineBlame(d, focused) // 设置里开着行内 blame：光标行末尾那一条（只在键盘所在那组的当前标签上）
		a.SyncProblems(d)             // 检查报的问题：这个文件的变了才重设诊断
		d.St.ContextMenu = func(m *wui.Menu) { docMenu(a, nativeMenu(m), d) }
		if d.Top > 0 { // 恢复的标签：等编辑器排过一次版（知道每行多高）再滚
			if _, last := d.St.VisibleLines(); last > 0 {
				d.St.ScrollTo(d.Top)
				d.Top = 0
			} else {
				c.Invalidate()
			}
		}
		a.SnipTick(d) // 代码片段：补全里选了的认出来开始跳位，跳位会话跟着改动挪
		d.St.OnCompletion = nil
		if d.Kind == docs.File && d.Editable() {
			d.St.OnCompletion = func(req editor.CompletionRequest) { a.SnippetCompletions(d, req) }
		}
		keys := docKeys(a, d, blocks)
		if d.Kind == docs.SearchDoc {
			keys = searchDocKeys(a, c, d, keys)
		}
		peekSetup(a, d) // 装订线改动块的就地卡：三种编辑器都把卡叠在上面
		var ed wui.Element
		build := func() wui.Element { return restingEditor(c, d, &d.St, &d.Rest, keys) }
		d.St.RenderFindBar, d.St.RenderGoToLine = nil, nil // 粘性滚动开着时换成记下位置的（stickyBars）
		switch {
		case d.ShowBlame && a.CanBlame(d):
			ed = blameEditor(a, c, d, build)
		case stickyOn(a, d):
			ed = stickyEditor(a, c, d, build)
		default:
			ed = peekEditor(a, c, d, build)
		}
		if focused && a.FocusDoc {
			a.FocusDoc = false
			ed.Focus()
		}
		if !d.St.ReadOnly { // 别的应用拖来的文字插在光标处、访达拖来的文件照旧打开（DropOnDoc）；只读的不接，照旧是窗口打开文件
			if drop, ok := wui.DropData(ed, app.DataDrop); ok {
				a.DropOnDoc(d, drop)
			}
		}
	}
	if focused && !d.Loading && d.Buf == nil {
		a.FocusDoc = false // 打不开就不等它了
	}
}

type previewKey struct{ d *docs.Doc }

// previewGen 是预览里那一栏的 key：渲染器有了新的行内公式（gen 变了）就换一个，段落重排（markdown 的段落缓存只认块变没变）。
type previewGen struct {
	d   *docs.Doc
	gen uint64
}

// markdownPreview 是 Markdown 标签的预览：居中的一栏，图片从文件所在的目录读，点链接见 App.MarkdownLink，带图片的表格见 mdImageTable。
// 点了 #标题 的链接（d.Anchor）后的下一帧在那个标题上放一个看不见、比窗口还高的记号，让它出现在视图里——
// 视图只滚到刚好露出它，记号比视图高，标题就到了最上面；没有这个标题就提示。
func markdownPreview(a *app.App, c *wui.Context, d *docs.Doc) {
	o := a.MarkdownOptions(d)
	o.Block = nil
	name := d.Anchor
	src := d.MarkdownSource()
	if tables := mdHasTable(src); name != "" || tables {
		o.Block = func(c *wui.Context, doc *parser.Document, i int) bool {
			if d.Anchor != "" && doc.Block(i).Kind == parser.Heading && markdown.Slug(parser.PlainText(doc.Inlines(i))) == name {
				wui.Box(c).Absolute().Top(0).Left(0).Width(1).Height(1e4).ScrollIntoView()
				d.Anchor = ""
			}
			return tables && mdImageTable(c, o, doc, i)
		}
	}
	gen := mdRender(a, c, o)
	wui.Scroll(c.Key(previewKey{d})).Grow(1).MinHeight(0).Label(i18n.Tf("预览 %s", d.Title)).Children(func() {
		wui.Column(c.Key(previewGen{d, gen})).FillWidth().MaxWidth(860).AlignSelf(wui.Center).Padding(20, 32, 48).Children(func() {
			markdown.View(c, src, o)
		})
	})
	switch {
	case d.Anchor != "" && d.Anchor == name:
		a.Toast = i18n.Tf("没有叫 #%s 的标题", d.Anchor)
		d.Anchor = ""
	case d.Anchor != "":
		c.Invalidate() // 这一帧画链接时点的，下一帧再找
	}
}

// conflictBar 在文件被别处改过、这里也改了时显示：重读丢掉这里的，或留着这里的。
func conflictBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(t.Warning.Alpha(0.14)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconAlert).FontSize(iconRow).TextColor(t.Warning)
		wui.Text(c, i18n.T("这个文件在磁盘上被改过了，这里的改动还没保存")).FontSize(11.5).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if linkButton(c, i18n.T("重新读取"), i18n.T("丢掉这里的改动，读磁盘上的")).Clicked() {
			a.Reload(d)
		}
		if linkButton(c, i18n.T("保留我的"), i18n.T("下次保存直接覆盖磁盘上的")).Clicked() {
			a.KeepMine(d)
		}
	})
}

// encodingReadOnly 是文件标签因为编码只读（要在上面放 encodingBar）：编码不能写回，或老编码编不回原样、选的编码解不通。
func encodingReadOnly(d *docs.Doc) bool {
	return d.Kind == docs.File && !d.Hex && (!d.Enc.Writable() || d.Lossy || d.Broken)
}

// encodingBar 在编码只读的文件标签上面：说清楚为什么只读，"换编码…"按别的编码重新打开，解得通的可以转成 UTF-8（先问）。
func encodingBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	msg := i18n.T("编码不明（不是 UTF-8），按原样显示、只读")
	switch {
	case d.Broken:
		msg = i18n.Tf("按 %s 解码有解不出来的字节，只读", d.Enc.Label())
	case d.Lossy:
		msg = i18n.Tf("按 %s 解码显示，有编回去和原来不一样的字节，只读", d.Enc.Label())
	case d.Enc != docs.Unknown:
		msg = i18n.Tf("按 %s 解码显示，只读", d.Enc.Label())
	}
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(t.Warning.Alpha(0.14)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconAlert).FontSize(iconRow).TextColor(t.Warning)
		wui.Text(c, msg).FontSize(11.5).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		menuButton(a, c, linkButton(c, i18n.T("换编码…"), i18n.T("用别的编码重新打开")), menuOwner{"reopenEnc", d.Where()}, func(m menu) { encodingChoices(a, m, d) })
		if d.Convertible() && linkButton(c, i18n.T("转成 UTF-8…"), i18n.T("按 UTF-8 重写这个文件，之后可以编辑")).Clicked() {
			a.ConvertUTF8(d)
		}
	})
}

// encodingMenu 是状态栏编码那项的菜单（文件、历史版本）：用别的编码重新打开，能转的转成 UTF-8。
func encodingMenu(a *app.App, m menu, d *docs.Doc) {
	encodingChoices(a, m, d)
	if d.Convertible() {
		m.Separator()
		if m.Item(i18n.T("转成 UTF-8…")).Chosen() {
			a.ConvertUTF8(d)
		}
	}
}

// encodingChoices 是"用别的编码重新打开"那几项：当前的打勾，手动选过的多一项"自动识别"。
func encodingChoices(a *app.App, m menu, d *docs.Doc) {
	m.Item(i18n.T("用别的编码重新打开")).Disabled(true)
	cur := d.Enc.Family()
	for i, g := range docs.ChoiceGroups {
		if i > 0 {
			m.Separator()
		}
		for _, enc := range g {
			if m.Item(enc.Label()).Checked(enc == cur).Chosen() && enc != cur {
				a.ReopenWithEncoding(d, enc)
			}
		}
	}
	if d.ForcedEnc != "" {
		m.Separator()
		if m.Item(i18n.T("自动识别编码重新打开")).Chosen() {
			a.ReopenWithEncoding(d, "")
		}
	}
}

// languageMenu 是状态栏语言那项的菜单："纯文本"和 mygo 认识的语言（按英文名排，中文界面也一样），当前的打勾；
// 手动选过的多一项"按文件名自动识别"。
func languageMenu(a *app.App, m menu, d *docs.Doc) {
	cur := ""
	if l := d.Buf.Language(); l != nil {
		cur = l.Name
	}
	if m.Item(i18n.T("纯文本")).Checked(cur == "").Chosen() && cur != "" {
		a.SetDocLang(d, docs.PlainLang)
	}
	m.Separator()
	langs := slices.Clone(editor.Languages())
	slices.SortFunc(langs, func(x, y *editor.Language) int {
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	for _, l := range langs {
		if m.Item(l.Name).Checked(l.Name == cur).Chosen() && l.Name != cur {
			a.SetDocLang(d, l.Name)
		}
	}
	if d.Lang != "" {
		m.Separator()
		if m.Item(i18n.T("按文件名自动识别")).Chosen() {
			a.SetDocLang(d, "")
		}
	}
}

// eolMenu 是状态栏换行那项的菜单（能保存的文件）：LF / CRLF，换了算改过，保存时按它写。
func eolMenu(a *app.App, m menu, d *docs.Doc) {
	cur := d.Buf.LineEnding()
	for _, e := range [...]struct{ label, eol string }{{"LF", "\n"}, {"CRLF", "\r\n"}} {
		if m.Item(e.label).Checked(cur == e.eol).Chosen() {
			a.SetDocEOL(d, e.eol)
		}
	}
}

// indentMenu 是状态栏缩进那项的菜单：这个文件用的缩进（和设置里的几样一样，当前的打勾），能保存的文件还能把每行开头的
// 缩进转成空格 / Tab。
func indentMenu(a *app.App, m menu, d *docs.Doc) {
	cur := a.DocIndent(d)
	for _, v := range workspace.Indents {
		label := i18n.T(indentNames[v])
		if v == "" {
			label = i18n.T("按语言默认")
		}
		if m.Item(label).Checked(v == cur).Chosen() && v != cur {
			a.SetDocIndent(d, v)
		}
	}
	if d.Editable() {
		m.Separator()
		if m.Item(i18n.T("把缩进转成空格")).Chosen() {
			a.ConvertIndent(d, false)
		}
		if m.Item(i18n.T("把缩进转成 Tab")).Chosen() {
			a.ConvertIndent(d, true)
		}
	}
}

// bigBar 是大文件（超过 8 MB，只读打开）标签上面淡淡的一条：说清楚关掉了哪些。
func bigBar(c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(t.TextMuted.Alpha(0.08)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconLock).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, i18n.Tf("大文件（%s），只读：不和 HEAD 对比，没有大纲、冲突检查和 blame", docs.HumanSize(d.Size))).FontSize(11.5).
			TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
}

// noBaseLine 是超过 docs.MaxDiffBaseBytes、没和 HEAD 对比的文件标签上面淡淡的一句（没有底色和边框）。
func noBaseLine(c *wui.Context) {
	wui.Text(c, i18n.T("文件较大，不和 HEAD 对比")).FontSize(11).TextColor(c.Theme().TextMuted).
		SingleLine().Ellipsis("…").Padding(3, 10).Shrink(0)
}

// termTextBar 是终端输出标签上面的一条：几行、什么时候取的、只读，右边刷新（重新取那一格的内容）和复制全部。
func termTextBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(bannerFill(t)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconTerminal).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, i18n.Tn("终端输出的快照（%d 行，%s），只读", d.Lines, d.Snap.Format("15:04:05"))).FontSize(11.5).TextColor(t.TextMuted).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		live := a.CanRefreshTermText(d)
		tip := i18n.T("重新取这一格终端现在的内容")
		if !live {
			tip = i18n.T("这个终端已经关了")
		}
		squareButton(c, iconRefresh, i18n.T("刷新"), tip, toolSize, iconTool, !live, func() { a.RefreshTermText(d) })
		squareButton(c, iconCopy, i18n.T("复制全部"), i18n.T("把这个标签里的全部内容复制到剪贴板"), toolSize, iconTool, false, func() { a.CopyTermText(d) })
	})
}

// settingsDocBar 是设置文档（settings.json，app.OpenSettingsDoc）顶上的一行：⌘S 是套用设置，右边“恢复成现在的设置”。
func settingsDocBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	wui.Row(c).Padding(3, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(bannerFill(t)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconSettings).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, app.KeyHint("file.save", "这是 state.json 里的 settings；%s 保存即生效，不认识的键或值不对会报错不保存",
			"这是 state.json 里的 settings；保存即生效，不认识的键或值不对会报错不保存")).FontSize(11.5).TextColor(t.TextMuted).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if linkButton(c, i18n.T("恢复成现在的设置"), i18n.T("内容换回现在的设置（这里没保存的改动先问）")).Clicked() {
			a.RegenSettingsDoc(d)
		}
	})
}

// searchDocBar 是搜索编辑器（app.OpenSearchEditor）顶上的一行：怎么打开一处，"上下文 N 行"菜单，重新搜索。
func searchDocBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	running := app.SearchDocRunning(d)
	wui.Row(c).Padding(3, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(bannerFill(t)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconSearchDoc).FontSize(iconRow).TextColor(t.TextMuted)
		wui.Text(c, i18n.T("双击、回车或 ⌘点一行，打开那个文件到那一行")).FontSize(11.5).TextColor(t.TextMuted).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		linkButton(c, i18n.Tn("上下文 %d 行", d.SearchLines), i18n.T("每处前后显示几行（之后开的搜索编辑器也用它）")).Menu(func(m *wui.Menu) {
			for _, n := range app.SearchContexts {
				if m.Item(i18n.Tn("%d 行", n)).Checked(n == d.SearchLines).Chosen() {
					a.SetSearchLines(d, n)
				}
			}
		})
		if running {
			wui.Spinner(c)
		}
		squareButton(c, iconRefresh, i18n.T("重新搜索"), i18n.T("按这个标签的条件和范围再搜一遍，重写内容"), toolSize, iconTool, running,
			func() { a.ResearchSearchDoc(d) })
	})
	d.St.OnDefinition = func(offset int) { a.OpenSearchHit(d, offset) }
	searchDocMarks(c, d)
}

// searchDocMarks 把搜索编辑器里的每一处标成和结果列表一样的底色（文字重写了、换了主题时重算）。
func searchDocMarks(c *wui.Context, d *docs.Doc) {
	col := c.Theme().Warning.Alpha(0.3)
	if bgs := d.St.TextBackgrounds; len(bgs) > 0 && bgs[0].Color == col {
		return
	}
	var bgs []editor.TextBackground
	for _, h := range d.Hits {
		for _, m := range h.Marks {
			if m.End > m.Start {
				bgs = append(bgs, editor.TextBackground{Start: m.Start, End: m.End, Color: col})
			}
		}
	}
	d.St.TextBackgrounds = bgs
}

// searchDocKeys 给搜索编辑器加上 ⏎ 和双击：打开光标那一行指向的文件（双击等编辑器先把光标放到点的地方）。
func searchDocKeys(a *app.App, c *wui.Context, d *docs.Doc, next func(wui.InputEvent) bool) func(wui.InputEvent) bool {
	return func(ev wui.InputEvent) bool {
		switch {
		case ev.Kind == wui.InputKeyDown && ev.Key == wui.KeyEnter && ev.Mods == 0:
			a.OpenSearchHit(d, d.St.Cursor())
			c.Invalidate()
			return true
		case ev.Kind == wui.InputPointerDown && ev.Button == 0 && ev.Clicks == 2 && ev.Mods == 0:
			a.Update(func() { a.OpenSearchHit(d, d.St.Cursor()) })
		}
		return next(ev)
	}
}

// loadingSlow 是标签读着时的内容：读了 app 的 loadingDelay 还没读完（d.SlowLoad）才画"读取中…"，
// 在那之前只占满、露出编辑区的底（预览标签换文件时不先闪转圈）。
func loadingSlow(c *wui.Context, d *docs.Doc) {
	if d.SlowLoad {
		loading(c)
	}
	wui.Spacer(c).Grow(1)
}

// diffContent 是 Diff 标签的内容和它底部的一行。
func diffContent(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	switch {
	case d.Loading:
		loadingSlow(c, d)
	case d.Info == app.GoneNote:
		message(c, iconCheck, i18n.T(d.Info), d.Where(), t.TextMuted)
	case d.Info != "" && d.Submodule:
		subDiff(a, c, d)
	case d.Info != "":
		message(c, iconAlert, d.Info, d.Where(), t.TextMuted)
	case d.ImgDiff != nil:
		imageDiff(a, c, d)
	case d.DiffSt != nil:
		diffLook(a, d)
		d.DiffSt.Bindings = app.DiffBindings()
		stage, unstage, discard := app.HunkOps(d)
		d.DiffSt.HunkActions = stage || unstage || discard
		d.DiffSt.RenderHunkActions = func(c *wui.Context, bar wui.Element, h *editor.DiffHunkActions) {
			hunkActions(a, c, bar, d, h, stage, unstage, discard)
		}
		d.DiffSt.LineSelection = d.DiffSt.HunkActions
		if !d.DiffSt.LineSelection {
			app.ClearSelectedLines(d)
		}
		restingDiff(c, d, d.DiffSt, &d.Rest).Grow(1).MinHeight(0)
		if n := app.SelectedLineCount(d); n > 0 { // 键盘歇着时在 diff 旁边那个空元素上（restingDiff），Esc 登记在外面一层
			c.OnShortcut(0, wui.KeyEscape, func() { app.ClearSelectedLines(d) })
			linesBar(a, c, d, n, stage, unstage, discard)
		}
	default:
		wui.Spacer(c).Grow(1)
	}
	diffFooter(a, c, d)
}

// diffLook 给 Diff 标签和替换预览的 diff 视图上这一帧的样子：并排 / 统一、字体；刚读出来、切了局部 / 全文时
// 把光标所在（还没动过就是第一处；替换预览从一处进来的是那一处的）那块改动滚进视图。
func diffLook(a *app.App, d *docs.Doc) {
	d.DiffSt.Layout = editor.DiffSplit
	if a.St.Unified {
		d.DiffSt.Layout = editor.DiffUnified
	}
	d.DiffSt.Font = a.EditorFont()
	d.DiffSt.Font.Size -= 0.5
	if d.ToHunk { // 全文 diff 别停在文件顶上、切局部 / 全文别把改动甩出视图（mygo 等视图排过版才滚过去）
		d.ToHunk = false
		i, _ := d.DiffSt.CurrentHunk()
		if d.ReplaceHunk > 0 {
			i, d.ReplaceHunk = d.ReplaceHunk-1, 0
		}
		if d.HunkLine > 0 { // 从编辑器的改动卡进来的
			i, d.HunkLine = app.HunkFromLine(d.DiffSt, d.HunkLine-1), 0
		}
		d.DiffSt.GoToHunk(max(i, 0))
	}
}

// replaceContent 是替换预览标签：顶上 replaceBar，下面是只读的 diff（现在 ↔ 替换后），底栏和 Diff 标签一样。
func replaceContent(a *app.App, c *wui.Context, d *docs.Doc) {
	replaceBar(a, c, d)
	switch {
	case d.Info != "":
		message(c, iconAlert, d.Info, d.Where(), c.Theme().TextMuted)
	case d.DiffSt != nil:
		diffLook(a, d)
		d.DiffSt.Bindings = app.DiffBindings()
		d.DiffSt.HunkActions, d.DiffSt.LineSelection = false, false
		restingDiff(c, d, d.DiffSt, &d.Rest).Grow(1).MinHeight(0)
	default:
		wui.Spacer(c).Grow(1)
	}
	diffFooter(a, c, d)
}

// compareContent 是比较标签（docs.Compare）：只读的 diff，没有暂存 / 丢弃的按钮、不能选行。
func compareContent(a *app.App, c *wui.Context, d *docs.Doc) {
	switch {
	case d.Info != "":
		message(c, iconAlert, d.Info, d.Where(), c.Theme().TextMuted)
	case d.DiffSt != nil:
		diffLook(a, d)
		d.DiffSt.Bindings = app.DiffBindings()
		d.DiffSt.HunkActions, d.DiffSt.LineSelection = false, false
		restingDiff(c, d, d.DiffSt, &d.Rest).Grow(1).MinHeight(0)
	default:
		wui.Spacer(c).Grow(1)
	}
	diffFooter(a, c, d)
}

// replaceBar 是替换预览上面的一条：按当前搜索与替换生成、没有写盘，右边“替换这个文件（N 处）”（和结果右键一样不问，换完关掉预览）。
// 结果变了（重搜、排除了几处、改了替换词）就说预览旧了、替换灰掉，旁边“重新生成”。
func replaceBar(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	stale := a.PreviewStale(d)
	bg, ic, fg, msg := bannerFill(t), iconReplace, t.TextMuted, i18n.T("按当前搜索与替换生成，没有写盘")
	if stale {
		bg, ic, fg, msg = t.Warning.Alpha(0.14), iconAlert, t.Warning, i18n.T("搜索结果变了，这个预览是旧的")
	}
	wui.Row(c).Padding(5, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(bg).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, ic).FontSize(iconRow).TextColor(fg)
		wui.Text(c, msg).FontSize(11.5).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if stale && a.Search.CanReplace() && iconAction(c.Key("regen"), iconRefresh, i18n.T("重新生成"), i18n.T("按现在的搜索结果和替换词再生成这个预览"), "", wui.Transparent) {
			a.RegenPreview(d)
		}
		n := a.PreviewCount(d)
		why := ""
		switch {
		case stale:
			why = i18n.T("搜索结果变了，这个预览是旧的")
		case n == 0:
			why = i18n.T("没有要替换的")
		}
		if iconAction(c.Key("replace"), iconReplace, i18n.Tf("替换这个文件（%d 处）", n),
			i18n.T("换掉这个文件里没排除的几处（开着的改编辑器里的，没开的直接写盘），换完关掉预览"), why, t.Accent) {
			a.ReplaceFromPreview(d)
		}
	})
}

// hunkActions 是 Diff 里指针下（或光标所在）那一块改动右上角的按钮：未暂存的能暂存、丢弃这一块，
// 已暂存的能取消暂存这一块。写操作走 git apply，丢弃先问。
func hunkActions(a *app.App, c *wui.Context, bar wui.Element, d *docs.Doc, h *editor.DiffHunkActions, stage, unstage, discard bool) {
	t := c.Theme()
	// 浮在 diff 上：外层一块 Surface 底和描边，里面是图标（iconAction），不再各自是按钮的样子
	bar.Row().Gap(0).Padding(1).Radius(6).Background(t.Surface).Border(1, t.Border)
	bar.Children(func() {
		button := func(key string, ic *wui.SVG, label, tip string, fg wui.Color) bool {
			return iconAction(c.Key(key), ic, label, tip, "", fg)
		}
		if stage && button("stage", iconPlus, i18n.T("暂存这一块"), i18n.T("只把这一块改动加进暂存区（git apply --cached）"), wui.Transparent) {
			a.StageHunk(d, h.Hunk)
		}
		if unstage && button("unstage", iconMinus, i18n.T("取消暂存这一块"), i18n.T("把这一块改动移出暂存区，工作区不动（git apply --cached -R）"), wui.Transparent) {
			a.UnstageHunk(d, h.Hunk)
		}
		if discard && button("discard", iconUndo, i18n.T("丢弃这一块"), i18n.T("把工作区里这一块恢复成暂存区的样子（会先问）"), t.Danger) {
			a.DiscardHunk(d, h.Hunk)
		}
	})
}

// linesBar 是 Diff 里点行号选着几行时底下那一栏（行级暂存）：选了几行，暂存 / 丢弃或取消暂存选中的行，取消选择（Esc）。
// 放在底下，选行时上面的行不会往下挪。
func linesBar(a *app.App, c *wui.Context, d *docs.Doc, n int, stage, unstage, discard bool) {
	t := c.Theme()
	button := func(ic *wui.SVG, label, tip string) bool {
		return iconAction(c.Key(label), ic, label, tip, "", wui.Transparent)
	}
	wui.Row(c).Padding(4, 10).Gap(4).AlignItems(wui.Center).Shrink(0).Background(bannerFill(t)).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		wui.Text(c, i18n.Tn("选中 %d 行", n)).FontSize(11.5).FontWeight(600).SingleLine().Shrink(0)
		wui.Text(c, i18n.T("⇧ 点行号扩大选择")).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
		wui.Spacer(c).Grow(1)
		if stage && button(iconPlus, i18n.T("暂存选中的行"), i18n.T("只把选中的这几行改动加进暂存区（git apply --cached）")) {
			a.StageLines(d)
		}
		if unstage && button(iconMinus, i18n.T("取消暂存选中的行"), i18n.T("把选中的这几行改动移出暂存区，工作区不动（git apply --cached）")) {
			a.UnstageLines(d)
		}
		if discard && button(iconUndo, i18n.T("丢弃选中的行…"), i18n.T("把工作区里选中的这几行恢复成暂存区的样子（会先问）")) {
			a.DiscardLines(d)
		}
		if button(iconClose, i18n.T("取消选择"), i18n.T("取消选中的行（Esc）")) {
			app.ClearSelectedLines(d)
		}
	})
}

// diffFooter 是 Diff 标签底部的状态行：左边是 +/− 统计和比较的两侧，右边是两组切换。
func diffFooter(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	wui.Row(c).Height(26).Padding(0, 8, 0, 10).Gap(8).AlignItems(wui.Center).Shrink(0).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		if d.DiffSt != nil {
			added, deleted := d.DiffSt.Stats()
			wui.Textf(c, "+%d", added).Font(mono()).FontSize(11).FontWeight(600).TextColor(t.Success).Shrink(0)
			wui.Textf(c, "−%d", deleted).Font(mono()).FontSize(11).FontWeight(600).TextColor(t.Danger).Shrink(0)
		}
		cl := compareLabel(d.Change)
		tip := ""
		switch d.Kind {
		case docs.ReplacePreview:
			cl = i18n.T("现在 ↔ 替换后")
		case docs.Compare:
			cl, tip = i18n.T("文件 ↔ 文件"), d.Abs+"\n↔ "+d.Right
			if d.Snapshot != 0 {
				cl = i18n.T("本地历史 ↔ 工作区")
			}
			if d.Right == "" {
				cl, tip = i18n.T("文件 ↔ 剪贴板"), d.Abs+"\n↔ "+i18n.T("剪贴板")
			}
		}
		tip = cmp.Or(tip, cl)
		if d.Change.Kind == git.Conflict && d.Change.Code == 'S' {
			tip += "\n" + stagesTip(a.RepoOf(d))
		}
		wui.Text(c, cl).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(tip)
		wui.Spacer(c).Grow(1)
		if cur, n, _ := app.HunkPos(d); n > 0 { // 局部 | 全文、并排 | 统一在标签条上（diffTools）
			s := i18n.Tn("%d 处改动", n)
			if cur >= 0 {
				s = i18n.Tf("第 %d / %d 处改动", cur+1, n)
			}
			wui.Text(c, s).FontSize(10.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
		}
	})
}

// diffTools 是 Diff 标签在标签条右端的控件：上一处 / 下一处改动（到头灰掉），
// 竖线，只看改动附近（开着 = 不是全文），并排 / 统一（图标画现在的样子，点了换），后面有"在编辑器打开"时再一道竖线。
func diffTools(a *app.App, c *wui.Context, d *docs.Doc, in func(func()) func(), pencil bool) {
	sep := func() { wui.Box(c).Size(1, 14).Background(c.Theme().Border).Shrink(0).Margin(0, 3) }
	toolButton(c, iconChevronUp, app.Tip(i18n.T("上一处改动"), "diff.prevHunk"), !app.CanGoToHunk(d, -1), in(func() { a.GoToHunk(d, -1) }))
	toolButton(c, iconChevron, app.Tip(i18n.T("下一处改动"), "diff.nextHunk"), !app.CanGoToHunk(d, 1), in(func() { a.GoToHunk(d, 1) }))
	sep()
	toggleButton(c, iconFold, app.Tip(i18n.T("只看改动附近"), "diff.full"), !a.St.FullDiff, in(a.ToggleScope))
	ic, tip := iconColumns, i18n.T("并排，点了换成统一")
	if a.St.Unified {
		ic, tip = iconRows, i18n.T("统一，点了换成并排")
	}
	iconButton(c, ic, app.Tip(tip, "diff.unified"), in(a.ToggleLayout))
	if pencil {
		sep()
	}
}

// compareLabel 说明 diff 比较的是哪两边。
func compareLabel(ch git.Change) string {
	switch ch.Kind {
	case git.Staged:
		return i18n.T("HEAD ↔ 索引")
	case git.Unstaged:
		return i18n.T("索引 ↔ 工作区")
	case git.InCommit:
		if ch.Range { // 比较两个提交：Parent 是起点
			return git.Short(ch.Parent) + " ↔ " + git.Short(ch.Commit)
		}
		return i18n.Tf("父提交 ↔ %s", git.Short(ch.Commit))
	case git.Conflict:
		if ch.Code == 'S' {
			return i18n.T("我们的（:2）↔ 他们的（:3）")
		}
		return i18n.T("HEAD ↔ 工作区（有冲突）")
	default:
		return i18n.T("新文件")
	}
}

// docStatus 是状态栏右边关于当前标签的几项：光标位置、语言、换行、编码、大小、状态。
func docStatus(a *app.App, c *wui.Context, d *docs.Doc) {
	t := c.Theme()
	muted := t.TextMuted
	item := func(s string) wui.Element {
		return wui.Text(c, s).FontSize(10.5).TextColor(muted).SingleLine().Shrink(0)
	}
	if d.Bitmap != nil {
		w, h := d.Bitmap.Size()
		item(fmt.Sprintf("%d × %d · %s", w, h, zoomLabel(d.Img.Shown)))
	}
	if d.Hex && d.Buf != nil { // 十六进制：光标那个字节的偏移，没有语言、缩进、换行、编码
		off := docs.HexOffset(d.St.CursorPos())
		item(i18n.Tf("偏移 0x%X（%s）", off, docs.Grouped(off)))
	} else if d.Kind != docs.Diff && d.Buf != nil {
		line, col, sels, chars := d.CursorInfo()
		pos := i18n.Tf("行 %d，列 %d", line, col)
		switch {
		case sels > 1:
			pos += i18n.Tf("（%d 处选中）", sels)
		case chars > 0:
			pos += i18n.Tn("（选中 %d 字）", chars)
		}
		item(pos)
		lang := i18n.T("纯文本")
		if l := d.Buf.Language(); l != nil {
			lang = l.Name
		}
		menuButton(a, c, item(lang).Label(i18n.Tf("语言 %s", lang)).Tooltip(i18n.T("按哪种语言高亮、缩进和注释；点开可以换一种")),
			menuOwner{"status.lang", d.Where()}, func(m menu) { languageMenu(a, m, d) })
		if d.Kind == docs.File {
			ind := a.IndentLabel(d)
			menuButton(a, c, item(ind).Label(i18n.Tf("缩进 %s", ind)).Tooltip(i18n.T("这个文件的缩进；点开可以换，或把缩进转成空格 / Tab")),
				menuOwner{"status.indent", d.Where()}, func(m menu) { indentMenu(a, m, d) })
		}
		if d.Kind != docs.TermText && d.Kind != docs.SearchDoc && d.Kind != docs.SettingsDoc {
			eol := "LF"
			if d.Buf.LineEnding() == "\r\n" {
				eol = "CRLF"
			}
			x := item(eol).Tooltip(i18n.T("换行符"))
			if d.Editable() {
				menuButton(a, c, x.Label(i18n.Tf("换行 %s", eol)).Tooltip(i18n.T("换行符；点开可以换成 LF 或 CRLF，保存时按它写")),
					menuOwner{"status.eol", d.Where()}, func(m menu) { eolMenu(a, m, d) })
			}
			enc := item(d.Enc.Label()).Tooltip(i18n.T("文件的编码；保存按同样的编码写回"))
			if d.Kind == docs.File || d.Kind == docs.Version {
				menuButton(a, c, enc.Label(i18n.Tf("编码 %s", d.Enc.Label())).Tooltip(i18n.T("文件的编码；点开可以用别的编码重新打开")),
					menuOwner{"status.enc", d.Where()}, func(m menu) { encodingMenu(a, m, d) })
			}
		}
	}
	if d.Size > 0 || d.Buf != nil {
		item(docs.HumanSize(d.Size))
	}
	if d.ShowBlame && d.Blame != nil {
		x := item(i18n.Tn("blame · %d 次提交", app.BlameCommits(d.Blame))).Tooltip(app.Tip(i18n.T("关掉 blame 装订线"), "view.blame"))
		if x.Clicked() {
			a.ToggleBlame()
		}
	}
	if d.Formatting {
		item(i18n.T("正在格式化…"))
	}
	if st := app.SnippetStatus(d); st != "" {
		item(st).FontWeight(600).TextColor(t.Text).Tooltip(i18n.T("在片段的跳位之间：Tab 下一个，⇧Tab 上一个，Esc 结束"))
	}
	if n := len(d.ConflictBlocks()); n > 0 {
		x := item(i18n.Tn("%d 处冲突", n)).FontWeight(600).TextColor(t.Danger).Label(i18n.T("下一处冲突")).Tooltip(app.Tip(i18n.T("下一处冲突"), "merge.next"))
		if x.Clicked() {
			a.GoToConflict(d, 1)
		}
	}
	r := a.RepoOf(d)
	switch {
	case d.Kind == docs.Version:
		item(i18n.Tf("只读 · 提交 %s", git.Short(d.Commit)))
	case d.Kind == docs.Diff:
		item(i18n.Tf("只读 · %s", d.Change.Kind.String()))
	case d.Kind == docs.TermText:
		item(i18n.T("只读 · 终端输出"))
	case d.Kind == docs.SearchDoc:
		item(i18n.T("只读 · 搜索结果"))
	case d.Kind == docs.ReplacePreview:
		item(i18n.T("只读 · 替换预览，没有写盘"))
	case d.Kind == docs.Compare && d.Snapshot != 0:
		item(i18n.T("只读 · 本地历史"))
	case d.Kind == docs.Compare:
		item(i18n.T("只读 · 比较"))
	case d.Kind == docs.Term:
		if _, tab := a.TermTabOf(d); tab != nil {
			if p := tab.Current(); p.Exited {
				item(i18n.Tf("已退出 %d", p.ExitCode))
			} else if p.Title != "" {
				item(p.Title)
			}
		}
		item(i18n.T("终端"))
	case d.Kind == docs.Rebase:
		item(i18n.Tf("交互式变基 · %s", app.RebaseCounts(d.Rebase)))
	case d.Kind == docs.SettingsDoc && d.Modified():
		item(app.KeyHint("file.save", "没应用 · %s", "没应用")).FontWeight(600).TextColor(t.Warning)
	case d.Kind == docs.SettingsDoc:
		item(i18n.T("和现在的设置一样"))
	case d.Modified():
		item(app.KeyHint("file.save", "未保存 · %s", "未保存")).FontWeight(600).TextColor(t.Warning)
	case r != nil && app.ChangedIn(r, d.Rel) != 0:
		item(i18n.T("有未提交的改动"))
	}
}
