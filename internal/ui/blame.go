package ui

import (
	"time"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// blame 装订线的宽：标签宽到 blameWideTab 时多放短哈希和摘要，窄的只放作者和时间。
const (
	blameWide    = 240
	blameNarrow  = 170
	blameWideTab = 760
)

type (
	blameRowKey struct{ d *docs.Doc }
	blameSegKey struct{ start int }
)

// blameEditor 建开着 blame 的编辑器（返回编辑器本身，好交键盘）：编辑器先建、左边留出装订线的宽——这一帧的滚轮
// 先落到它身上——再在左边叠一列，每段连续同一次提交改的行一块，按 LineTop - ScrollY 对齐到行上。
func blameEditor(a *app.App, c *wui.Context, d *docs.Doc, build func() wui.Element) wui.Element {
	t := c.Theme()
	a.EnsureBlame(d)
	if app.BlameStale(d) {
		wui.Row(c).Padding(4, 10).Gap(8).AlignItems(wui.Center).Shrink(0).Background(t.Warning.Alpha(0.1)).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			wui.Text(c, i18n.T("改了没保存，blame 的行号可能对不上；保存后重算")).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0)
		})
	}
	row := wui.Row(c.Key(blameRowKey{d})).Grow(1).MinHeight(0).AlignItems(wui.Stretch) // Row 默认竖直居中：不撑满的话编辑器高是 0
	w := float32(blameNarrow)
	if row.Bounds().W >= blameWideTab {
		w = blameWide
	}
	var bars *[]wui.Rect
	if stickyOn(a, d) {
		bars = stickyBars(d)
	}
	var ed wui.Element
	row.Children(func() {
		ed = build().Grow(1).MinWidth(0).Margin(0, 0, 0, w)
		blameColumn(a, c, d, w)
		peekCard(a, c, d, row, w)
		if bars != nil { // 粘性滚动的叠层从装订线右边起
			stickyRows(a, c, d, row, w, bars)
		}
	})
	return ed
}

// blameColumn 是装订线那一列。编辑器画的时候还会滚（光标跳出视图、拖选到边上、软换行量出行高），
// 画到这一列时核对一下用的滚动和行高，变了就再建一帧，各段跟上。
func blameColumn(a *app.App, c *wui.Context, d *docs.Doc, w float32) {
	t := c.Theme()
	col := wui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(w).Clip().
		BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Label(i18n.T("blame 装订线"))
	b := d.Blame
	if b == nil {
		col.Children(func() {
			if d.BlameErr != "" {
				wui.Text(c, d.BlameErr).FontSize(11.5).TextColor(t.TextMuted).Padding(8, 10)
				return
			}
			wui.Row(c).Padding(8, 10).Gap(6).AlignItems(wui.Center).Children(func() {
				wui.Spinner(c)
				wui.Text(c, i18n.T("正在算 blame…")).FontSize(11.5).TextColor(t.TextMuted)
			})
		})
		return
	}
	if app.BlameStale(d) {
		col.Opacity(0.5)
	}
	st := &d.St
	n := d.Buf.LineCount()
	y0, total := st.ScrollY(), st.LineTop(n)
	h := col.Bounds().H
	if h <= 0 {
		h = 1e5
	}
	svc := c.Services()
	col.Draw(func(*wui.Painter, wui.Rect) {
		if st.ScrollY() != y0 || st.LineTop(n) != total {
			svc.Invalidate()
		}
	})

	// 先挑出看得见的段，量出一行多高（软换行折了的行更高，取最矮的）
	type vis struct {
		s        git.BlameSeg
		top, bot float32
	}
	var shown []vis
	lh := float32(0)
	for _, s := range b.Segments() {
		if s.Start >= n {
			break
		}
		top := float32(st.LineTop(s.Start) - y0)
		bot := float32(st.LineTop(min(s.End, n)) - y0)
		if top >= h {
			break
		}
		if bot <= 0 || bot-top < 0.5 { // 滚出去了，或整段折叠着
			continue
		}
		shown = append(shown, vis{s, top, bot})
		if first := float32(st.LineTop(s.Start+1)-y0) - top; first > 0 && (lh == 0 || first < lh) {
			lh = first
		}
	}
	oldest, newest := b.Span()
	cur := st.CursorPos().Line
	wide := w >= blameWide
	col.Children(func() {
		for _, v := range shown {
			blameSeg(a, c, d, v.s, v.top, v.bot-v.top, lh, w, wide, v.s.Start <= cur && cur < v.s.End, blameShade(v.s.Commit, oldest, newest))
		}
	})
}

// blameShade 是一段的底色有多深：最新的提交最深、最早的最浅，按时间插值。
func blameShade(cm *git.BlameCommit, oldest, newest time.Time) float32 {
	if !newest.After(oldest) {
		return 0.16
	}
	f := float32(cm.Time.Sub(oldest)) / float32(newest.Sub(oldest))
	return 0.03 + 0.13*max(0, min(1, f))
}

// blameSeg 是装订线上的一段：第一行写短哈希、作者 · 多久前和摘要（窄的只写作者和时间），好几行的左边一道竖线连起来。
// 悬停看完整的提交，单击在右栏历史里选中它，右键可以打开这次提交的 diff、上一版、复制哈希。
func blameSeg(a *app.App, c *wui.Context, d *docs.Doc, s git.BlameSeg, top, h, lh, w float32, wide, current bool, shade float32) {
	t := c.Theme()
	cm := s.Commit
	if cm == nil {
		return
	}
	bg := toolFill(t, shade*0.6) // 越新越深的灰
	if cm.Uncommitted() {
		bg = t.Success.Alpha(0.12)
	}
	fg := t.TextMuted
	if current {
		fg = t.Text
	}
	seg := wui.Box(c.Key(blameSegKey{s.Start})).Absolute().Left(0).Top(top).Width(w).Height(h).Background(bg)
	if s.Start > 0 {
		seg.BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.6))
	}
	who := cm.Author + " · " + git.Ago(cm.Time)
	if cm.Uncommitted() {
		seg.Label(i18n.T("还没提交的改动")).Tooltip(i18n.T("还没提交的改动"))
	} else {
		seg.Label(i18n.Tf("blame：%s %s %s", cm.Short, who, cm.Summary)).
			Tooltip(cm.Hash + "\n" + cm.Author + " <" + cm.Email + ">\n" + cm.Time.Format("2006-01-02 15:04") + "\n" + cm.Summary).
			Cursor(wui.CursorPointer).ContextMenu(func(m *wui.Menu) { blameMenu(a, m, d, cm) })
	}
	seg.Children(func() {
		if h > lh*1.5 {
			line := t.TextMuted.Alpha(0.35)
			if current {
				line = t.Accent
			}
			wui.Box(c).Absolute().Left(3).Top(4).Bottom(4).Width(1).Background(line)
		}
		wui.Row(c).Height(lh).PaddingX(9).Gap(6).AlignItems(wui.Center).Children(func() {
			if cm.Uncommitted() {
				wui.Text(c, i18n.T("未提交")).FontSize(11.5).TextColor(t.Success).SingleLine()
				return
			}
			if wide {
				wui.Text(c, cm.Short).Font(mono()).FontSize(11).TextColor(fg).SingleLine().Shrink(0)
			}
			wui.Text(c, who).FontSize(11.5).TextColor(fg).SingleLine().Ellipsis("…").MinWidth(0).Shrink(0).MaxWidth(w - 76)
			if wide {
				wui.Text(c, cm.Summary).FontSize(11.5).TextColor(t.TextMuted.Alpha(0.75)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			}
		})
	})
	if !cm.Uncommitted() && seg.Clicked() {
		if r := a.RepoOf(d); r != nil {
			a.ShowInHistory(r, cm.Hash)
		}
	}
}

// blameMenu 是 blame 一段的右键菜单。
func blameMenu(a *app.App, m *wui.Menu, d *docs.Doc, cm *git.BlameCommit) {
	if m.Item(i18n.T("在历史里显示")).Chosen() {
		if r := a.RepoOf(d); r != nil {
			a.ShowInHistory(r, cm.Hash)
		}
	}
	if m.Item(i18n.T("打开这次提交里这个文件的 diff")).Chosen() {
		a.BlameDiff(d, cm)
	}
	if m.Item(i18n.T("查看上一版")).Disabled(cm.Previous == "").Chosen() {
		a.BlamePrevious(d, cm)
	}
	m.Separator()
	if m.Item(i18n.T("复制哈希")).Chosen() {
		a.CopyBlameHash(cm)
	}
}
