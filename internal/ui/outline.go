package ui

import (
	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/outline"
)

// outlineView 是侧栏的大纲模式：当前标签的文件的符号树（internal/outline）。单击跳到那一行、键盘回到编辑器，
// 方向键挪选中时编辑器跟着滚过去、回车跳过去；光标所在的符号选中并滚到眼前（收着的选最近的展开着的祖先）。
// 展开状态按标签记，新出现的有子项的符号默认展开。只有这一页开着时才解析大纲。
func outlineView(a *app.App, c *wui.Context) {
	t := c.Theme()
	d := a.CurrentDoc()
	if d != nil && d.IsDiffView() {
		d = nil
	}
	var tree *outline.Tree
	if d != nil {
		tree = d.Outline()
	}
	wui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		wui.Row(c).Padding(0, 8, 2, 14).Gap(6).AlignItems(wui.Center).Shrink(0).MinHeight(24).Children(func() {
			wui.Text(c, i18n.T("大纲")).FontSize(sideSub).FontWeight(600).TextColor(t.TextMuted).Shrink(0)
			title := ""
			if d != nil {
				title = d.Title
			}
			wui.Text(c, title).FontSize(sideSub).TextColor(t.TextMuted.Alpha(0.8)).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			if tree != nil && len(tree.Kids) > 0 {
				st := &d.OutlineSt
				iconButton(c, iconExpandAll, i18n.T("展开所有符号"), func() {
					for k := range tree.Kids {
						st.Open.Add(k)
					}
				})
				iconButton(c, iconCollapseAll, i18n.T("收起所有符号"), st.Open.Clear)
			}
		})
		switch {
		case d == nil:
			message(c, iconOutline, i18n.T("没有打开的文件"), "", t.TextMuted)
		case d.Loading:
			if d.SlowLoad {
				loading(c)
			}
		case d.Buf == nil || !outline.Supported(d.Name()):
			message(c, iconOutline, i18n.Tf("%s 没有大纲", d.Title), i18n.T("支持 Markdown、Go、Python、JS / TS、Rust、C / C++、Java、Kotlin、Swift、C#、Shell 和 YAML"), t.TextMuted)
		case d.Buf.Len() > outline.MaxBytes:
			message(c, iconOutline, i18n.T("文件太大，没有大纲"), "", t.TextMuted)
		case tree == nil || len(tree.Flat) == 0:
			message(c, iconOutline, i18n.T("没有符号"), "", t.TextMuted)
		default:
			outlineTree(a, c.Key(outlineKey{d}), d, tree)
			if tree.More != "" {
				wui.Text(c, tree.More).FontSize(sideSub).TextColor(t.TextMuted).Padding(4, 12).Shrink(0)
			}
		}
	})
}

// outlineKey 让每个标签的大纲有自己的一套元素（滚动位置跟着标签走）。
type outlineKey struct{ d *docs.Doc }

// outlineTree 是大纲的树：wui.Outline，键是 outline.Flat.Key。
func outlineTree(a *app.App, c *wui.Context, d *docs.Doc, tree *outline.Tree) {
	st := &d.OutlineSt
	if d.OutlineSeen == nil {
		d.OutlineSeen, d.OutlineSel = map[string]bool{}, -1
	}
	if d.OutlineSyms != tree { // 重新解析过：新出现的有子项的符号默认展开，用户收起过的不动
		for k := range tree.Kids {
			if !d.OutlineSeen[k] {
				d.OutlineSeen[k] = true
				st.Open.Add(k)
			}
		}
		d.OutlineSyms = tree
	}
	cur := ""
	if p, ok := d.Cursor(); ok {
		if i := tree.At(p.Line); i >= 0 {
			cur = tree.Flat[i].Key
		}
	}
	follow := cur != d.OutlineCur
	d.OutlineCur = cur

	st.List.Selected = &d.OutlineSel
	st.List.Label = func(i int) string { return symbolLabel(&tree.Flat[tree.ByKey[st.Item(i)]]) }
	clicked := ""
	l := newListLook(a, c, rowRadius)
	focused := st.List.Focused(c)
	var list wui.Element
	l.build(c, func() {
		list = wui.Outline(c, st, tree.Roots, func(k string) []string { return tree.Kids[k] }, func(k string) {
			sel := d.OutlineSel >= 0 && d.OutlineSel < st.Rows() && st.Item(d.OutlineSel) == k
			l.row(c, sel, focused, func() {
				if outlineRow(c, &tree.Flat[tree.ByKey[k]], onAccent(a, sel)) {
					clicked = k
				}
			})
		}).Grow(1).MinHeight(0).PaddingX(4).PaddingY(2)
	})
	rowOf := func(k string) int {
		for r := range st.Rows() {
			if st.Item(r) == k {
				return r
			}
		}
		return -1
	}
	jump := func(focus bool) {
		if d.OutlineSel < 0 || d.OutlineSel >= st.Rows() {
			return
		}
		a.JumpHere(docs.Jump{Line: tree.Flat[tree.ByKey[st.Item(d.OutlineSel)]].Line})
		if focus {
			a.FocusDoc = true
		}
	}
	switch {
	case clicked != "": // 行里的内容接了点击（列表就不选了）：选中它、跳过去、键盘回编辑器
		d.OutlineSel = rowOf(clicked)
		jump(true)
	case list.Submitted():
		jump(true)
	case list.Changed():
		jump(false)
	case follow:
		d.OutlineSel = -1
		if i, ok := tree.ByKey[cur]; ok {
			if r := rowOf(tree.Flat[visibleSymbol(tree, st, i)].Key); r >= 0 {
				d.OutlineSel = r
				st.List.ScrollIntoView(r)
			}
		}
		c.Invalidate() // 这一帧的行已经建好了，下一帧画出新的选中
	}
}

// visibleSymbol 是第 i 个符号自己（祖先都展开着时）或者它最近的看得见的祖先。
func visibleSymbol(tree *outline.Tree, st *wui.OutlineState[string], i int) int {
	var chain []int // 从 i 到顶层
	for j := i; j >= 0; j = tree.Flat[j].Parent {
		chain = append(chain, j)
	}
	v := chain[len(chain)-1]
	for n := len(chain) - 2; n >= 0 && st.Open.Has(tree.Flat[v].Key); n-- {
		v = chain[n]
	}
	return v
}

// outlineRow 画大纲的一行：种类的小字、名字、灰着的 Detail。返回这一行被点了。
func outlineRow(c *wui.Context, f *outline.Flat, sel bool) bool {
	t := c.Theme()
	fg, muted, glyph := t.Text, t.TextMuted, t.TextMuted // 种类的小字都是次级色
	if sel {
		fg, muted, glyph = t.AccentText, t.AccentText.Alpha(0.75), t.AccentText
	}
	el := wui.Row(c).Gap(6).AlignItems(wui.Center).MinWidth(0).Grow(1).Label(symbolLabel(f)).Cursor(wui.CursorPointer)
	clicked := el.Clicked()
	el.Children(func() {
		wui.Text(c, symbolGlyph(f.Kind)).FontSize(sideSub).FontWeight(600).TextColor(glyph).Width(13).Shrink(0)
		wui.Text(c, f.Name).FontSize(sideFont).TextColor(fg).SingleLine().Ellipsis("…").MinWidth(0)
		if f.Detail != "" {
			wui.Text(c, f.Detail).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
	return clicked
}

// symbolGlyph 是符号种类的小字：# 标题、ƒ 函数和方法、T 类型、C 类、• 常量和变量、§ 一组。
func symbolGlyph(k outline.Kind) string {
	switch k {
	case outline.Heading:
		return "#"
	case outline.Func, outline.Method:
		return "ƒ"
	case outline.Type:
		return "T"
	case outline.Class:
		return "C"
	case outline.Section:
		return "§"
	}
	return "•"
}

// symbolLabel 是读屏念的：名字加上 Detail。
func symbolLabel(f *outline.Flat) string {
	if f.Detail == "" {
		return f.Name
	}
	return f.Name + " " + f.Detail
}
