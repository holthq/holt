package ui

import (
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
)

// treeTipGap 是树行提示离行底边多远。
const treeTipGap = 2

// treeTip 是文件树一行（仓库行、目录行、文件行）的悬停提示：指针在 el 上停满 app.TreeTipDelay（1 s）才出，
// 画在行的下方、从 left（名字那一列）起，不盖住上一行；窗口底下放不下时 AttachTo 自己翻到上方。
// 不用 mygo 的 Tooltip：它的 0.6 s 和"热身"（刚有提示消失就立刻弹下一条）都写死在 mygo 里，
// 在树上上下找文件时一路冒提示。只在 el.Hovered() 时调；skip 是这一帧不出（行里的小图标自己的提示在出、按下了、在拖）。
// full 不空时是被省略号截断的名字的全名，放在提示第一行。
func treeTip(a *app.App, c *wui.Context, el wui.Element, key app.FileKey, full, s string, left float32, skip bool) {
	desc := s
	if full != "" && s != "" {
		desc = full + "\n" + s
	} else if full != "" {
		desc = full
	}
	if desc == "" {
		return
	}
	el.Description(desc)
	wait := a.TreeTip.Hover(key, c.Now())
	if skip {
		return
	}
	if wait > 0 {
		if wait < time.Hour {
			c.After(wait)
		}
		return
	}
	t := c.Theme()
	fill, text := t.Inverse, t.InverseText // 同 mygo 的 Theme.inverse（tooltip 也这么取）
	if fill == wui.Transparent {
		fill = t.Text
	}
	if text == wui.Transparent {
		text = t.Background
	}
	// 提示盖在下一行上：里面的字也要 PassThrough，不然按在下一行上的点击落到字上（mygo 的 PassThrough 不管子元素）
	wui.Overlay(c, func() {
		tip := wui.Column(c).Absolute().PassThrough().Role(wui.RoleTooltip)
		tip.MaxWidth(t.Space(80)).Padding(t.Space(1.25), t.Space(2)).Radius(t.Space(1.25)).Gap(2).
			Background(fill).TextColor(text).FontSize(t.FontSize-1).Shadow(0, 2, 8, 0, wui.RGBA(0, 0, 0, 0.2))
		tip.Children(func() {
			if full != "" {
				wui.Text(c, full).FontWeight(600).PassThrough()
			}
			if s != "" {
				wui.Text(c, s).PassThrough()
			}
		})
		tip.AttachTo(el, wui.AnchorBottomLeft, wui.AnchorTopLeft).Margin(treeTipGap, 0, 0, 0).Left(left)
	})
}

// cutName 是名字元素上一帧被省略号截断时的全名，没截断（或还没画过）是 ""。
func cutName(c *wui.Context, name wui.Element, s string, size float32) string {
	b := name.Bounds()
	if b.W <= 0 {
		return ""
	}
	if w, _ := c.MeasureText(0, wui.Span{Text: s, Size: size}); w > b.W+0.5 {
		return s
	}
	return ""
}
