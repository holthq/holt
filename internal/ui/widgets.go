package ui

import (
	"github.com/egoist/mygo"
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/fonts"
	"github.com/lemoeade/grove/internal/i18n"
)

// 这个文件是各区域共用的小部件：图标按钮、开关按钮、胶囊、状态字母、居中的提示。

const (
	barHeight    = 38 // 标题栏：固定这么高，红绿灯按 TrafficLights 居中在里面
	headerHeight = 32 // 标签条、右栏和底栏的标题行
	statusHeight = 22 // 状态栏

	cardRadius = 8 // 卡片（编辑器组、底栏、右栏）的圆角
	rowRadius  = 5 // 侧栏、右栏列表里选中行的圆角
	cardGap    = 6 // 卡片之间、卡片和侧栏 / 标题栏 / 状态栏 / 窗口右边之间露出材质的缝，也是拖着改大小的地方
	splitGap   = 4 // 同一张卡片里分出来的格（终端分屏）之间的缝

	defaultSidebarW = 280
	defaultRightW   = 320
	defaultBottomH  = 240
	defaultTermW    = 420
)

// 图标只有三档（参考 Claude 桌面端），各处的 Icon(...).FontSize 只能取这几个（TestIconSizes 扫源码）：
const (
	iconInline = 11 // 行内跟着字的小标：分支、作者、胶囊里的、下拉的 ▾
	iconRow    = 13 // 列表行、标签、卡片头和输入框里的图标，行里的小按钮
	iconTool   = 16 // 标题栏、卡片头、侧栏头的图标按钮，快速打开面板输入框前面那个（框里的字 14）
	iconEmpty  = 26 // 空着的区域中央那个大图标（message），不算一档
)

// 图标按钮的热区：标题栏、卡片头、侧栏头的一律 toolSize 见方（图标 iconTool 居中），列表行里的 rowToolSize。
const (
	toolSize    = 28
	rowToolSize = 20
	segIconW    = 30 // segIconTabs 每段的宽（侧栏顶上的四个图标靠左一组）
)

// textNudge 是标题栏、标签条上的字底下多留的边距：字在行里居中时墨迹的中心比图标低 0.5–0.75 pt
// （TestTitleBarAlign 4 倍渲染量的），底下多留 1，整个往上挪 0.5，和图标、红绿灯一条中线。
const textNudge = 1

// trafficLightH 是红绿灯按钮的高（macOS 量的：启动日志里"标题栏：窗口按钮"的 Height − 2×Y）。
const trafficLightH = 14

// TrafficLights 是红绿灯在窗口里的位置（关闭按钮的左上角，mygo.WindowOptions.TrafficLightPosition）：
// 上下留一样多，按钮正好居中在 barHeight 高的标题栏里，和标题栏上的图标、文字一条中线。
var TrafficLights = mygo.Point{X: 12, Y: (barHeight - trafficLightH) / 2}

func mono() string { return fonts.Mono() }

// tipBelowY：元素上一帧的上沿比这高（离窗口顶不到标题栏加一个单行提示的高），提示就往下弹。
const tipBelowY = barHeight + 32

// tooltip 是 el.Tooltip(s)，只是紧贴在标题栏下面的元素（侧栏的分段、标签条、右栏标题行）提示往下弹：
// mygo 的提示默认在上面，那里只要还放得下就往上，会盖住标题栏上的红绿灯和按钮。样子和 Tooltip 一样。
func tooltip(c *wui.Context, el wui.Element, s string) wui.Element {
	if s == "" || el.Bounds().Y >= tipBelowY {
		return el.Tooltip(s)
	}
	t := c.Theme()
	fill, text := t.Inverse, t.InverseText // 同 mygo 的 Theme.inverse
	if fill == wui.Transparent {
		fill = t.Text
	}
	if text == wui.Transparent {
		text = t.Background
	}
	el.Description(s)
	wui.TooltipBase(c, el, func(tip wui.Element) {
		tip.MaxWidth(t.Space(80)).Padding(t.Space(1.25), t.Space(2)).Radius(t.Space(1.25)).
			Background(fill).TextColor(text).FontSize(t.FontSize-1).Shadow(0, 2, 8, 0, wui.RGBA(0, 0, 0, 0.2))
		tip.Children(func() { wui.Text(c, s) })
		tip.AttachTo(el, wui.AnchorBottom, wui.AnchorTop).Margin(t.Space(1.5), 0, 0, 0)
	})
	return el
}

// iconButton 是只有图标的方按钮（toolSize 见方）：平时只有次级色的图标，悬停一层淡底、图标变深，按下底再深一点。
// tip 也是它在辅助功能和测试里的名字。
func iconButton(c *wui.Context, ic *wui.SVG, tip string, fn func()) wui.Element {
	return toolButton(c, ic, tip, false, fn)
}

// toolButton 是可以禁用的图标按钮：禁用时变淡、点了没反应。
func toolButton(c *wui.Context, ic *wui.SVG, tip string, disabled bool, fn func()) wui.Element {
	return squareButton(c, ic, tip, tip, toolSize, iconTool, disabled, fn)
}

// rowButton 是列表行里更小的图标按钮（rowToolSize 见方）：Git 栏分组标题上的"全部暂存"、贮藏上的"应用"之类。
// label 是名字（读屏念它、测试按它点），tip 是悬停时的说明（一般带上 git 命令）。
func rowButton(c *wui.Context, ic *wui.SVG, label, tip string, fn func()) wui.Element {
	return squareButton(c, ic, label, tip, rowToolSize, iconRow, false, fn)
}

// squareButton 是图标按钮的本体：size 见方的热区，图标 iconSize（只取 iconInline / iconRow / iconTool）居中。
func squareButton(c *wui.Context, ic *wui.SVG, label, tip string, size, iconSize float32, disabled bool, fn func()) wui.Element {
	b := wui.Box(c).Size(size, size).Label(label)
	tooltip(c, b, tip)
	fg := toolStyle(c, b, false, disabled)
	b.Children(func() {
		wui.Icon(c, ic).FontSize(iconSize).TextColor(fg)
	})
	if !disabled && b.Clicked() {
		fn()
	}
	return b
}

// toggleButton 是区域开关（侧栏 / 右栏 / 底栏）：开着的是一块浅底、图标是字色，关着的没有底、图标是次级色
// （Claude 桌面端的面板开关那样；不用强调色，标题栏上不会有一排蓝的）。
func toggleButton(c *wui.Context, ic *wui.SVG, tip string, on bool, fn func()) {
	b := wui.Box(c).Size(toolSize, toolSize).Label(tip)
	tooltip(c, b, tip)
	fg := toolStyle(c, b, on, false)
	b.Children(func() {
		wui.Icon(c, ic).FontSize(iconTool).TextColor(fg)
	})
	if b.Clicked() {
		fn()
	}
}

// iconAction 是面板里的动作按钮（冲突条、进行中横幅、提交框、替换条、终端退出浮条……）：只有图标，rowToolSize 见方，
// 平时没有底、悬停一层淡底（toolStyle）。用 ButtonBase，能用 Tab 按到（squareButton 的 Box 不行）；label 是读屏名和
// 测试按它点的名字，和原来文字按钮上的字一样。why 不空时画灰、提示里写原因、点了没反应——不调 Disabled（mygo 的
// Disabled 不显示悬停提示）。fg 不是 Transparent 时图标平时用这个颜色（强调色、绿……）。返回能不能算点了。
func iconAction(c *wui.Context, ic *wui.SVG, label, tip, why string, fg wui.Color) bool {
	return iconActionSize(c, rowToolSize, ic, label, tip, why, fg)
}

// iconActionSize 是 iconAction，热区 size 见方（工作树行上的和旁边的按钮一样 wtBtnSize+2）。
func iconActionSize(c *wui.Context, size float32, ic *wui.SVG, label, tip, why string, fg wui.Color) bool {
	full := label
	if why != "" {
		full = withWhy(label, why)
	}
	if tip != "" && tip != label {
		full += "\n" + tip
	}
	b := wui.ButtonBase(c).Size(size, size).Label(label)
	tooltip(c, b, full)
	col := toolStyle(c, b, false, why != "")
	if why == "" && fg != wui.Transparent {
		col = fg
	}
	b.Children(func() {
		wui.Icon(c, ic).FontSize(iconRow).TextColor(col)
	})
	return b.Clicked() && why == ""
}

// filterToggle 是筛选开关（历史的 所有分支 / 提交图，搜索的 Aa ab .* 和包含 / 排除）：22 见方、没有边，
// 开着是强调色的浅底和强调色（面板开关 toggleButton 是浅底 + 字色，不动），关着是次级色、悬停一层淡底。
// 还是切换按钮（读屏念"开 / 关"）；fn 用给它的颜色画里面（图标，或几个等宽字）。返回开关，Changed() 报点了。
func filterToggle(c *wui.Context, key string, on *bool, label, tip string, fn func(fg wui.Color)) wui.Element {
	return filterToggleOf(c, key, on, label, tip, 22, false, fn)
}

// filterToggleOf 是 filterToggle 的本体：size 见方（提交框那一排用 rowToolSize，和旁边的按钮一样高）；
// off 时画灰、点了 *on 不变（Changed 照报，调用的人自己看 off），悬停提示还在（不调 Disabled，mygo 的 Disabled 不显示提示）。
func filterToggleOf(c *wui.Context, key string, on *bool, label, tip string, size float32, off bool, fn func(fg wui.Color)) wui.Element {
	t := c.Theme()
	if off { // 点了也不改 *on：给 ToggleBase 一份拷贝
		v := *on
		on = &v
	}
	b := wui.ToggleBase(c.Key(key), on).Size(size, size).Label(label).AlignItems(wui.Center).Justify(wui.Center).Radius(5).Shrink(0).
		Cursor(wui.CursorPointer)
	tooltip(c, b, tip)
	fg := t.TextMuted
	switch {
	case off:
		fg = t.TextMuted.Alpha(0.4)
		if *on {
			b.Background(toolFill(t, 0.06))
		}
		b.Cursor(wui.CursorDefault)
	case *on:
		b.Background(t.Accent.Alpha(0.14))
		fg = t.Accent
	case b.Pressed():
		b.Background(toolFill(t, 0.12))
		fg = t.Text
	case b.Hovered():
		b.Background(toolFill(t, 0.06))
		fg = t.Text
	}
	b.Children(func() { fn(fg) })
	return b
}

// linkButton 是横幅里的动作（重新读取、保留我的、换编码…、转成 UTF-8…）：没有边和底的正文色字（11.5、500），悬停一层淡的灰底
// （横幅本身的字是次级色，动作靠字色深、字重和悬停的底认出来）。
// 返回按钮（换编码… 接 .Menu），Clicked() 报点了。
func linkButton(c *wui.Context, label, tip string) wui.Element {
	t := c.Theme()
	b := wui.ButtonBase(c).Label(label).Padding(2, 6).Radius(4).AlignItems(wui.Center).Shrink(0).Cursor(wui.CursorPointer).Tooltip(tip)
	switch {
	case b.Pressed():
		b.Background(toolFill(t, 0.12))
	case b.Hovered():
		b.Background(toolFill(t, 0.06))
	}
	b.Children(func() { wui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(t.Text).SingleLine() })
	return b
}

// toolStyle 给图标按钮 b 上统一的样子：图标居中、圆角 6、不被挤小，悬停 / 按下 / 开着时的底色（toolFill），
// 返回图标该用的颜色：开着、悬停、按下时是字色，平时次级色，禁用时更淡。b 的大小由调用的人定。
func toolStyle(c *wui.Context, b wui.Element, on, disabled bool) wui.Color {
	t := c.Theme()
	b.AlignItems(wui.Center).Justify(wui.Center).Radius(6).Shrink(0)
	if disabled {
		return t.TextMuted.Alpha(0.4)
	}
	b.Cursor(wui.CursorPointer)
	var fill float32
	switch {
	case b.Pressed():
		fill = 0.12
	case b.Hovered():
		fill = 0.06
	}
	if on {
		fill += 0.08
	}
	if fill == 0 {
		return t.TextMuted
	}
	b.Background(toolFill(t, fill))
	return t.Text
}

// toolFill 是按钮的底色：浅色是黑色 a，深色是白色 a×4/3（悬停 a = 6%：浅色黑 6%、深色白 8%；开着的开关 8%，
// 按下 12%，开着再悬停 / 按下叠上去）。都是半透明，盖在底下的颜色上。
func toolFill(t *wui.Theme, a float32) wui.Color {
	if t.Dark {
		return wui.RGBA(255, 255, 255, a*4/3)
	}
	return wui.RGBA(0, 0, 0, a)
}

// seg 是 segTabs 的一段：图标（可以没有）、文字和悬停在这一段上的说明（可以没有）。
type seg struct {
	icon  *wui.SVG
	label string
	tip   string
}

// segTabs 是等分的分段标签（侧栏的 文件 | 搜索 | Git、右栏的 当前仓库 | 所有仓库）：浅灰的槽里每段一样宽、
// 图标加文字居中，选中的一段是浮起来的底色。返回槽，Changed() 报换了一段。
func segTabs(c *wui.Context, sel *int, segs ...seg) wui.Element {
	return segTabsOf(c, sel, false, segs)
}

// segIconTabs 是只有图标的 segTabs（侧栏顶上的 文件 / 搜索 / Git / 大纲）：图标 iconTool，读屏念 label，悬停提示照旧。
func segIconTabs(c *wui.Context, sel *int, segs ...seg) wui.Element {
	return segTabsOf(c, sel, true, segs)
}

func segTabsOf(c *wui.Context, sel *int, iconOnly bool, segs []seg) wui.Element {
	t := c.Theme()
	p := wui.SegmentedBase(c, sel, len(segs))
	p.Track.Height(26).Padding(2).Gap(2).Radius(7).Background(t.Text.Alpha(0.055)).AlignItems(wui.Stretch).MinWidth(0)
	p.Track.Children(func() {
		for i, sg := range segs {
			s := p.Segment(i).MinWidth(0).Radius(5)
			if iconOnly { // 只有图标的一组靠左、每段固定宽，不铺满（#56）
				s.Width(segIconW).Shrink(0)
			} else {
				s.Grow(1).Basis(0)
			}
			s.AlignItems(wui.Center).Justify(wui.Center).Cursor(wui.CursorPointer)
			tooltip(c, s, sg.tip)
			if iconOnly {
				s.Label(sg.label)
			}
			fg := t.TextMuted
			switch {
			case i == *sel:
				s.Background(t.Background).Shadow(0, 1, 2, 0, wui.RGBA(0, 0, 0, 0.10))
				fg = t.Text
			case s.Hovered():
				s.Background(t.Text.Alpha(0.05))
				fg = t.Text
			}
			s.Children(func() {
				wui.Row(c).Gap(5).AlignItems(wui.Center).Justify(wui.Center).MinWidth(0).Padding(0, 6).Children(func() {
					if iconOnly {
						wui.Icon(c, sg.icon).FontSize(iconTool).TextColor(fg)
						return
					}
					if sg.icon != nil {
						wui.Icon(c, sg.icon).FontSize(iconRow).TextColor(fg)
					}
					wui.Text(c, sg.label).FontSize(12).FontWeight(500).TextColor(fg).SingleLine().Ellipsis("…").MinWidth(0)
				})
			})
		}
	})
	return p.Track
}

// panelTab 是卡片头里的一个标签（编辑器标签、终端标签、输出 / 问题）：通栏平铺，所有标签一样高（headerHeight）、
// 没有圆角，相邻的靠右边一条 1 px 的 cardLine 分开（第一个贴着卡片左边，卡片自己有描边，不画左边线）。
// 当前标签是卡身底色，盖住卡片头底下那条线（标签"长"在卡身上）；别的没有底色，悬停一层淡底。
// 两种标签的内边距一样，字在同一条线上。返回整个标签，调用的人接着加内容、Label、右键菜单、点击。
func panelTab(c *wui.Context, active bool) wui.Element {
	t := c.Theme()
	b := wui.Row(c).Height(headerHeight).Padding(0, 4, 0, 10).Gap(6).AlignItems(wui.Center).Shrink(0).Cursor(wui.CursorPointer).
		BorderWidth(0, 1, 0, 0).BorderColor(cardLine(t))
	switch {
	case active:
		b.Background(t.Background)
	case b.Hovered():
		b.Background(t.Text.Alpha(0.05))
	}
	return b
}

// panelTabText 是卡片头标签里的标题：12 / 500、单行，长了省略（最多 panelTabMaxW 宽）。
func panelTabText(c *wui.Context, s string, fg wui.Color) wui.Element {
	return wui.Text(c, s).FontSize(12).FontWeight(500).TextColor(fg).SingleLine().Ellipsis("…").MinWidth(0).MaxWidth(panelTabMaxW).Margin(0, 0, textNudge, 0)
}

// panelTabMaxW 是标签标题最宽多少，长名字省略。
const panelTabMaxW = 200

// panelTabColor 是卡片头标签里文字和图标的颜色：当前的和悬停的字深，别的 TextMuted。
func panelTabColor(c *wui.Context, b wui.Element, active bool) wui.Color {
	t := c.Theme()
	if active || b.Hovered() {
		return t.Text
	}
	return t.TextMuted
}

// cardBg 是卡片的底色：浅色是主题的白底；深色是 #202023（app.CardDark），比窗口的材质亮一档。
func cardBg(t *wui.Theme) wui.Color {
	if t.Dark {
		return app.CardDark
	}
	return t.Background
}

// cardLine 是卡片的描边和卡片头底下的线：浅色是字色 7%，深色是白 8%（一圈亮边）。
func cardLine(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.RGBA(255, 255, 255, 0.08)
	}
	return t.Text.Alpha(0.07)
}

// headerBg 是卡片头的底色：卡片色上浅色盖 3% 的黑、深色盖 3% 的白（混好的不透明色，卡片底透明时也一样）。
func headerBg(t *wui.Theme) wui.Color {
	if t.Dark {
		return cardBg(t).Mix(wui.Hex("#ffffff"), 0.03)
	}
	return cardBg(t).Mix(wui.Hex("#000000"), 0.03)
}

// windowBg 是不通透（柔和、纯色，或者系统不给材质）时窗口的底色：浅 #EDEDF0、深 #1F1F22。
func windowBg(t *wui.Theme) wui.Color {
	if t.Dark {
		return wui.Hex("#1f1f22")
	}
	return wui.Hex("#ededf0")
}

// softBackdrop 是这一帧窗口底要不要画柔和底的色斑：设置里是柔和（默认）、窗口没有材质。
func softBackdrop(a *app.App, c *wui.Context) bool {
	return !c.Vibrancy() && a.St.Settings.Backdrop == ""
}

// blob 是柔和底上的一团颜色：中心在窗口宽高的比例 (x, y)，大小 w×h pt 固定，颜色带着浓淡。
type blob struct {
	x, y, w, h float32
	c          wui.Color
}

// 柔和底的色斑：设计样稿 .wall 的位置和原色，α 直接盖在 windowBg 上（样稿是糊开的壁纸上盖 84% 的窗色，约等于 16%）。
// 晕开 blobBlur（σ 是一半）、再往外撑 blobSpread。先画的在下面。
var (
	lightBlobs = []blob{
		{0.30, 0.75, 250, 250, wui.Hex("#9fe0c8").Alpha(0.14)}, // 薄荷
		{0.55, 0.92, 320, 260, wui.Hex("#f0a3c8").Alpha(0.16)}, // 粉
		{0.88, 0.22, 300, 230, wui.Hex("#ffc48a").Alpha(0.16)}, // 桃
		{0.12, 0.18, 260, 210, wui.Hex("#8fb6ff").Alpha(0.18)}, // 蓝
	}
	darkBlobs = []blob{
		{0.55, 0.92, 320, 260, wui.Hex("#7a3a62").Alpha(0.26)},
		{0.88, 0.22, 300, 230, wui.Hex("#8a4e2a").Alpha(0.26)},
		{0.12, 0.18, 260, 210, wui.Hex("#2d4f9a").Alpha(0.30)},
	}
)

const (
	blobBlur   = 110
	blobSpread = 40
)

// paintBackdrop 在 win（整个窗口）里画柔和底的色斑，底色另外铺。每团是一个晕开的胶囊形：mygo 的 Shadow 只画在
// 矩形外面，矩形是空的（宽或高是 0）时整团都画，所以拿一条线段当投影的盒子、spread 把它撑成 w×h 再往外 blobSpread、
// 再晕开——中间不用补一块 Fill，没有接缝。不用 Element.Blur（整层离屏位图，窗口那么大十几 MiB），也不用 Canvas。
// 卡片不透明，看得到色斑的只有标题栏、侧栏、状态栏和卡片之间的缝。
func paintBackdrop(p *wui.Painter, win wui.Rect, t *wui.Theme) {
	blobs := lightBlobs
	if t.Dark {
		blobs = darkBlobs
	}
	for _, b := range blobs {
		cx, cy := win.X+b.x*win.W, win.Y+b.y*win.H
		short := min(b.w, b.h)
		seg := wui.Rect{X: cx - (b.w-short)/2, Y: cy - (b.h-short)/2, W: b.w - short, H: b.h - short}
		p.Shadow(seg, 1, 0, 0, blobBlur, short/2+blobSpread, b.c)
	}
}

// underBackdrop 给盖在窗口底上的不透明元素（Git 栏贴在顶上的段头）画上同一片色斑，接得上、没有一条平带。
func underBackdrop(a *app.App, c *wui.Context, el wui.Element) {
	if !softBackdrop(a, c) {
		return
	}
	t := c.Theme()
	w, h := c.Size()
	el.Draw(func(p *wui.Painter, r wui.Rect) {
		p.Clip(r, 0, func() { paintBackdrop(p, wui.Rect{W: w, H: h}, t) })
	})
}

// sideList 是侧栏的列表（文件树、搜索结果、Git 栏）：选中行左右内缩 4、圆角 5（Finder 侧栏那样）。
// 列表的选中块用主题的 Radius，所以在一份改了 Radius 的主题下建（listLook，淡底的选中行也在那里）。
//
// 滚动（issue #58）：列表 TrackScroll 一份 app.SideScroll。触控板（precise）的照旧交给 mygo；鼠标滚轮的由 sideWheel 接：
// 一格走一个行高（mygo 一行是 40，等于 1.67 个树行，加速后一次好几行，缓动又前重后轻，一顿一顿的），往累计的目标滑。
// 列表自己还要接别的输入的（文件树的 menuPress）把 sideWheel 串在自己的 HandleInput 里。
func sideList(a *app.App, c *wui.Context, st *wui.ListState, n int, rows func(wui.ListRow)) wui.Element {
	l := newListLook(a, c, rowRadius)
	s := sideScroll(a, st)
	if s.Step(c.Now()) {
		c.AnimationFrame()
	}
	var list wui.Element
	l.build(c, func() {
		list = wui.List(c, st, n).Grow(1).MinHeight(0).PaddingX(4).PaddingY(2).TrackScroll(&s.S).Rows(l.rows(rows))
	})
	list.Draw(func(*wui.Painter, wui.Rect) { s.Drawn = s.S.Y }) // 画的时候的偏移（布局夹过、列表自己挪过的）
	list.HandleInput(func(ev wui.InputEvent) bool { return sideWheel(a, c, st, ev) })
	return list
}

// sideScroll 是列表 st 的滚动状态（没有就建一份）。
func sideScroll(a *app.App, st *wui.ListState) *app.SideScroll {
	if a.SideScrolls == nil {
		a.SideScrolls = map[*wui.ListState]*app.SideScroll{}
	}
	s := a.SideScrolls[st]
	if s == nil {
		s = &app.SideScroll{}
		a.SideScrolls[st] = s
	}
	return s
}

// sideWheel 接侧栏列表 st 上的鼠标滚轮（不是 precise 的；带 ⇧ 横着滚、带 ⌃ 的不接）：一格一个行高，加速的按平方根压、
// 一次最多 6 行，约 160 ms 滑到累计的目标，连续几格接成一段，到头夹住，反向立刻转向（app.SideScroll）。
// 系统"减弱动态效果"时直接到位。
func sideWheel(a *app.App, c *wui.Context, st *wui.ListState, ev wui.InputEvent) bool {
	if ev.Kind != wui.InputScroll || ev.Precise || ev.Mods&(wui.Shift|wui.Ctrl) != 0 || ev.DY == 0 {
		return false
	}
	s := sideScroll(a, st)
	rowH := a.TreeRowH
	if rowH <= 0 {
		rowH = treeRowH
	}
	if !s.Wheel(ev.DY, rowH, ev.Time, c.Preferences().ReduceMotion) {
		return s.S.MaxY > 0 // 到头了：吞掉（不交给 mygo 再缓动一下）
	}
	c.Invalidate()
	return true
}

// sideShift 是列表 st 的行这一帧比上一帧画的时候往下挪了多少（滚上去为负）：上一帧记的 Bounds 加上它就是这一帧的位置。
func sideShift(a *app.App, st *wui.ListState) float32 {
	s := sideScroll(a, st)
	return s.Drawn - s.S.Y
}

// withTheme 让 fn 里建的元素用主题 nt，建完换回原来的。
// （SetTheme 会把窗口根的底色重设成主题的底色，所以 root 最后才设根的底色。）
func withTheme(c *wui.Context, nt *wui.Theme, fn func()) {
	t := c.Theme()
	c.SetTheme(nt)
	fn()
	c.SetTheme(t)
}

// card 把 e 画成一张卡片：不透明的底、圆角 8、1 px 极淡的描边，浅色再加一点阴影；里面的东西裁在圆角里。
// fn 里建的元素用卡片的主题：底色换成卡片色，编辑器、Diff、浮在上面的小条跟卡片一色。
func card(c *wui.Context, e wui.Element, fn func()) wui.Element {
	t := c.Theme()
	e.Background(cardBg(t)).Radius(cardRadius).Border(1, cardLine(t)).Clip()
	if !t.Dark {
		e.Shadow(0, 1, 2, 0, wui.RGBA(0, 0, 0, 0.06))
	}
	ct := *t
	ct.Background = cardBg(t)
	withTheme(c, &ct, func() { e.Children(fn) })
	return e
}

// cardHeader 是卡片头（标签行、右栏的标题行）：高 32，淡淡一层底色，底下一条 1 px 的线。
// 线画在底色上、子元素下面，当前标签（panelTab）的底就把它下面那一段盖掉了。
func cardHeader(c *wui.Context) wui.Element {
	t := c.Theme()
	line := cardLine(t)
	return wui.Row(c).Height(headerHeight).AlignItems(wui.Center).Shrink(0).Background(headerBg(t)).
		Draw(func(p *wui.Painter, r wui.Rect) { p.Fill(wui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, line, 0) })
}

// gapLine 是缝（卡片之间、分屏格之间）悬停或拖着时正中那条 2 pt、强调色 60% 的线；平时什么也不画。
func gapLine(c *wui.Context, b wui.Element, vertical bool) {
	if !b.Hovered() && !b.Pressed() {
		return
	}
	line := c.Theme().Accent.Alpha(0.6)
	b.Children(func() {
		if vertical {
			wui.Box(c).Width(2).Background(line)
		} else {
			wui.Box(c).Height(2).Background(line)
		}
	})
}

// textButton 是一个很小的文字按钮，用在分组标题右边（"全部暂存"这种）：次级色的字，悬停换正文色、一层淡的灰底。
func textButton(c *wui.Context, label, tip string, fn func()) {
	textButtonOn(c, label, tip, false, fn)
}

// textButtonOn 同 textButton；onAccent 时它在选中（强调色）的行上，换成反白的底和字，不然看不清。
func textButtonOn(c *wui.Context, label, tip string, onAccent bool, fn func()) {
	textButtonCur(c, label, tip, onAccent, false, fn)
}

// textButtonCur 同 textButtonOn；cur 时它是一排里现在的那个（图片工具条的 适应 / 100%）：灰胶囊、正文色的字。
func textButtonCur(c *wui.Context, label, tip string, onAccent, cur bool, fn func()) {
	t := c.Theme()
	b := wui.Box(c).Padding(1, 6).Radius(6).Cursor(wui.CursorPointer).Shrink(0)
	tooltip(c, b, tip)
	fg := t.TextMuted
	switch {
	case onAccent && b.Hovered():
		fg = t.AccentText
		b.Background(t.AccentText.Alpha(0.28))
	case onAccent:
		fg = t.AccentText
		b.Background(t.AccentText.Alpha(0.16))
	case cur:
		fg = t.Text
		b.Background(quietFill(t))
	case b.Hovered():
		fg = t.Text
		b.Background(toolFill(t, 0.06))
	}
	b.Children(func() {
		wui.Text(c, label).FontSize(10.5).FontWeight(500).TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		fn()
	}
}

// pill 是一个小圆角计数标签；在选中（强调色）的行上换成反白。灰的（color 是 TextMuted）底用 quietFill。
func pill(c *wui.Context, text string, color wui.Color, onAccent bool) wui.Element {
	t := c.Theme()
	bg, fg := color.Alpha(0.14), color
	if color == t.TextMuted {
		bg = quietFill(t)
	}
	if onAccent {
		bg, fg = t.AccentText.Alpha(0.22), t.AccentText
	}
	return wui.Box(c).Padding(1, 7).Radius(999).Background(bg).Shrink(0).Children(func() {
		wui.Text(c, text).FontSize(11).FontWeight(600).TextColor(fg).SingleLine()
	})
}

// chip 是小标签：可选图标 + 文字。
func chip(c *wui.Context, ic *wui.SVG, text string, fg, bg wui.Color) wui.Element {
	return wui.Row(c).Gap(5).AlignItems(wui.Center).Padding(2, 8).Radius(999).Background(bg).Shrink(0).Children(func() {
		if ic != nil {
			wui.Icon(c, ic).FontSize(iconInline).TextColor(fg)
		}
		wui.Text(c, text).FontSize(11).FontWeight(500).TextColor(fg).SingleLine().Ellipsis("…")
	})
}

// codeChip 是状态字母（M / A / D / ? …）的小方块。
func codeChip(c *wui.Context, code byte, onAccent bool) {
	t := c.Theme()
	col := codeColor(t, code)
	bg, fg := col.Alpha(0.15), col
	if onAccent {
		bg, fg = t.AccentText.Alpha(0.22), t.AccentText
	}
	wui.Box(c).Size(18, 18).Radius(4).Background(bg).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Children(func() {
		wui.Text(c, string(code)).Font(mono()).FontSize(11).FontWeight(700).TextColor(fg).SingleLine()
	})
}

func codeColor(t *wui.Theme, code byte) wui.Color {
	switch code {
	case 'A', '?':
		return t.Success
	case 'M':
		return t.Warning
	case 'D', 'U':
		return t.Danger
	case 'R', 'C':
		return t.Accent
	}
	return t.TextMuted
}

// message 是区域中央的提示：图标、标题和一行说明。
func message(c *wui.Context, ic *wui.SVG, title, sub string, color wui.Color) {
	t := c.Theme()
	wui.Column(c).Grow(1).MinHeight(0).AlignItems(wui.Center).Justify(wui.Center).Gap(4).Padding(16).Children(func() {
		wui.Icon(c, ic).FontSize(iconEmpty).TextColor(color.Alpha(0.8))
		wui.Text(c, title).FontSize(13).FontWeight(600).TextColor(t.TextMuted).Center()
		if sub != "" {
			wui.Text(c, sub).FontSize(11).TextColor(t.TextMuted.Alpha(0.8)).Center()
		}
	})
}

// loading 是一行"转圈 + 读取中…"。
func loading(c *wui.Context) {
	t := c.Theme()
	wui.Row(c).Padding(16).Gap(8).AlignItems(wui.Center).Shrink(0).Children(func() {
		wui.Spinner(c)
		wui.Text(c, i18n.T("读取中…")).TextColor(t.TextMuted)
	})
}

// divider 是标题栏里两组按钮之间的竖线。
func divider(c *wui.Context) {
	wui.Box(c).Width(1).Height(16).Background(c.Theme().Border).Margin(0, 4).Shrink(0)
}

// resizer 是两块之间可以拖的缝（6 pt，露出材质）：侧栏和卡片之间、右栏 / 底栏的卡片和主区之间。
// size 是那一栏的宽（vertical）或高；before 为那一栏在缝的左边（侧栏），往右拖变大，否则往主区拖变大。
// 平时不画线，悬停 / 拖着时缝的正中一条强调色的线；松手时调 done（存 state.json）。
func resizer(c *wui.Context, label string, vertical, before bool, size *float32, lo, hi float32, done func()) {
	var b wui.Element
	if vertical {
		b = wui.Row(c).Width(cardGap).Justify(wui.Center).Cursor(wui.CursorResizeColumn)
	} else {
		b = wui.Column(c).Height(cardGap).Justify(wui.Center).Cursor(wui.CursorResizeRow)
	}
	b.Shrink(0).AlignItems(wui.Stretch).Label(label).Tooltip(label)
	if dx, dy, ok := b.Dragged(); ok {
		d := dy
		if vertical {
			d = dx
		}
		if !before {
			d = -d
		}
		*size = min(max(*size+d, lo), hi)
	}
	if _, _, ended := b.DragEnded(); ended {
		done()
	}
	gapLine(c, b, vertical)
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
