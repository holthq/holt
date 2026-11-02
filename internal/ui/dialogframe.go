package ui

import (
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/i18n"
)

// dlgKind 是对话框头上的图标块：问的、提示是强调色，警告、危险、出错是红色；dlgPlain 不画（表单）。
type dlgKind int

const (
	dlgPlain dlgKind = iota
	dlgInfo
	dlgQuestion
	dlgWarning
	dlgDanger
	dlgError
)

// dialogOpts 是 dialogFrame 的头和键：标题、一句说明、右端的仓库名（多仓库时说清是谁在问，说明里有了就空着）、宽度（0 = dlgWidth）。
// Enter 是没有输入框时 ⏎ 做的事（主按钮，危险的也是），有输入框的靠输入框自己的 Submitted；Hint 是左下角"⏎ …"写的字，空就只写"Esc 取消"。
type dialogOpts struct {
	Kind  dlgKind
	Title string
	Note  string
	Repo  string
	Width float32
	Hint  string
	Enter func()
	// Single 是只有一个按钮（出错 / 提示的"好"）：没有可取消的，底部提示写成"Esc / ⏎ 好"
	Single bool
}

// 对话框的尺寸（设计稿 C 居中精修）。
const (
	dlgWidth   = 400 // 表单
	dlgConfirm = 380 // 确认、出错
	dlgPad     = 16
	dlgRadius  = 12
	dlgIconBox = 28
)

// dialogFrame 是所有对话框的框（不用 mygo 的 AlertDialog / Modal）：淡遮罩上一张居中略偏上的卡片，
// 头（图标块、标题、说明、仓库名）、body（字段、勾选框、命令预览）、底部一行（左边键的提示，右边 footer 画的按钮）。
// 点遮罩、Esc 把 *open 置 false，关掉以后的清理照旧由调用的人做。基于 wui.DialogBase，命令面板（palette）也是同一种脸。
func dialogFrame(c *wui.Context, open *bool, o dialogOpts, body, footer func()) wui.Element {
	t := c.Theme()
	w := o.Width
	if w == 0 {
		w = dlgWidth
	}
	cw, h := c.Size()
	enter := false
	panel := wui.DialogBase(c, open, func(back, panel wui.Element) {
		// 居中在去掉底下 20% 的那块里：矮的确认框顶边落在窗高三成左右，高的表单照样放得下
		back.Background(wui.RGBA(0, 0, 0, dlgShade(t))).Padding(16, 16, 16+h*0.2, 16)
		panel.Width(w).MaxWidth(max(cw-32, 200)).MaxHeight(max(h*0.8-32, 120)).Padding(14, dlgPad, 14, dlgPad).Gap(12).
			Radius(dlgRadius).Background(t.Background).Border(1, t.Border).Shadow(0, 12, 32, 0, wui.RGBA(0, 0, 0, 0.22)).Label(o.Title)
		if o.Enter != nil && panel.OverlayShortcut(0, wui.KeyEnter) {
			enter = true
		}
		dlgHeader(c, o)
		if body != nil {
			wui.Column(c).Gap(10).MinWidth(0).Children(body)
		}
		// 底部一行反着排：按钮先建（Tab 和按名字找都先到按钮，提示里的"取消""删除分支"是同样的字），画出来提示在左
		wui.Row(c).Reverse().Gap(8).AlignItems(wui.Center).MinWidth(0).Children(func() {
			wui.Row(c).Gap(8).AlignItems(wui.Center).Shrink(0).Children(func() {
				if footer != nil {
					footer()
				}
			})
			wui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(wui.Center).Children(func() {
				switch {
				case o.Single:
					dlgHint(c, "Esc", "/", "⏎", o.Hint)
				default:
					dlgHint(c, "Esc", i18n.T("取消"))
					if o.Hint != "" {
						dlgHint(c, "⏎", o.Hint)
					}
				}
			})
		})
	})
	if enter {
		o.Enter()
	}
	return panel
}

// dlgShade 是遮罩的深浅：浅色 .18，深色底本来就暗、压到 .26 才看得出。
func dlgShade(t *wui.Theme) float32 {
	if t.Dark {
		return 0.26
	}
	return 0.18
}

// dlgHeader 是对话框的头：图标块（dlgPlain 没有）、标题 13.5 / 600 和说明 11.5 次级色、右端的仓库名。
func dlgHeader(c *wui.Context, o dialogOpts) {
	t := c.Theme()
	wui.Row(c).Gap(10).AlignItems(wui.Start).MinWidth(0).Children(func() {
		ic, fg := dlgIcon(t, o.Kind)
		if ic != nil {
			wui.Box(c).Size(dlgIconBox, dlgIconBox).Radius(7).Background(fg.Alpha(0.12)).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).
				Children(func() { wui.Icon(c, ic).FontSize(iconTool).TextColor(fg) })
		}
		col := wui.Column(c).Grow(1).MinWidth(0).Gap(2)
		if ic != nil {
			col.Padding(5, 0, 0, 0) // 一行标题和图标块的中线对齐
		}
		col.Children(func() {
			wui.Text(c, o.Title).FontSize(13.5).FontWeight(600).LineHeight(18.0 / 13.5).MinWidth(0)
			if o.Note != "" {
				wui.Text(c, o.Note).FontSize(11.5).LineHeight(16.0 / 11.5).TextColor(t.TextMuted).MinWidth(0)
			}
		})
		if o.Repo != "" {
			wui.Text(c, o.Repo).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MaxWidth(120).Shrink(0).Padding(3, 0, 0, 0)
		}
	})
}

// dlgIcon 是 kind 的图标和颜色。
func dlgIcon(t *wui.Theme, k dlgKind) (*wui.SVG, wui.Color) {
	switch k {
	case dlgInfo:
		return iconInfo, t.Accent
	case dlgQuestion:
		return iconQuestion, t.Accent
	case dlgWarning, dlgDanger:
		return iconAlert, t.Danger
	case dlgError:
		return iconCircleX, t.Danger
	}
	return nil, wui.Color{}
}

// dlgHint 是左下角的一条键提示：描边的键胶囊（等宽 10.5）后面一句次级色的字。
// 等宽字里没有 ⏎，退回去的字形只有一半大，所以 ⏎ 用界面字体、大一号（键胶囊一样高）。
// 参数是键和字交替：("Esc", "取消")；只有一个按钮时 ("Esc", "/", "⏎", "好")，两个键共用后面的字。
func dlgHint(c *wui.Context, parts ...string) {
	t := c.Theme()
	wui.Row(c).Gap(4).AlignItems(wui.Center).Shrink(1).MinWidth(0).Children(func() {
		for i, s := range parts {
			if i%2 == 1 {
				wui.Text(c, s).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
				continue
			}
			k := wui.Text(c, s).TextColor(t.TextMuted).Padding(0, 4).Radius(4).Border(1, t.Border).Shrink(0)
			if s == "⏎" {
				k.FontSize(12).LineHeight(10.5 / 12 * 1.3)
			} else {
				k.Font(mono()).FontSize(10.5)
			}
		}
	})
}

// btnKind 是对话框按钮的样子：普通（描边）、主（强调色）、危险（红）。
type btnKind int

const (
	btnPlain btnKind = iota
	btnPrimary
	btnDanger
)

// dlgButton 是对话框底部的按钮，三种一样高（26）、一样的圆角和字号；返回这一帧点没点。
// mygo 的 Button / PrimaryButton 没有红的，覆上底色后它的悬停就不变色了，所以三种都自己画（ButtonBase，同标题栏的按钮）。
// focus 的那个是 ⏎ 的默认按钮（确认框里主按钮拿着焦点，Tab 能换到取消）。
func dlgButton(c *wui.Context, label string, k btnKind, disabled, focus bool) bool {
	t := c.Theme()
	b := wui.ButtonBase(c).Label(label).Height(26).Padding(0, 12).Radius(6).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).
		Disabled(disabled)
	base, hover, press, fg := t.Surface, t.SurfaceHover, t.SurfacePressed, t.Text
	switch k {
	case btnPrimary:
		base, hover, press, fg = t.Accent, t.AccentHover, t.AccentPressed, t.AccentText
	case btnDanger:
		black := wui.RGBA(0, 0, 0, 1)
		base, hover, press, fg = t.Danger, t.Danger.Mix(black, 0.1), t.Danger.Mix(black, 0.18), t.AccentText
	default:
		b.Border(1, t.Border)
	}
	bg := base
	switch {
	case disabled:
		bg, fg = base.Alpha(0.5), fg.Alpha(0.5)
	case b.Pressed():
		bg = press
	case b.Hovered():
		bg = hover
	}
	b.Background(bg)
	if focus && !disabled {
		b.AutoFocus()
	}
	b.Children(func() { wui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(fg).SingleLine() })
	return !disabled && b.Clicked()
}

// dlgField 是表单里的一项：上面 11.5 次级色的名字，下面 fn 画的输入框，有错时再下面一行红字。
// 还是 wui.Field（点名字聚焦输入框、红字读给辅助技术），名字的字号颜色由 Field 往下传（输入框 dlgInput 自己定回来）。
func dlgField(c *wui.Context, label, errMsg string, fn func()) {
	t := c.Theme()
	wui.Field(c, label, fn).Gap(4).MinWidth(0).FontSize(11.5).TextColor(t.TextMuted).Error(errMsg)
}

// dlgInput 给对话框里的输入框统一的样子：26 高、描边、圆角 6、底是窗口底色（不要灰底）、正文色（不跟 dlgField 的次级色）。
func dlgInput(c *wui.Context, in wui.Element) wui.Element {
	t := c.Theme()
	return in.Height(26).Radius(6).Background(t.Background).Border(1, t.Border).FontSize(12.5).TextColor(t.Text)
}

// dlgCommand 是表单底下"会跑的命令"：等宽 11 次级色，浅底圆角，一行放不下截断。
func dlgCommand(c *wui.Context, s string) {
	t := c.Theme()
	wui.Text(c, s).Font(mono()).FontSize(11).TextColor(t.TextMuted).Background(t.Text.Alpha(0.05)).Radius(6).Padding(5, 8).
		SingleLine().Ellipsis("…").MinWidth(0).Tooltip(s)
}
