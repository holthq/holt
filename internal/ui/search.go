package ui

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/search"
	"github.com/lemoeade/grove/internal/workspace"
)

// searchView 是侧栏的搜索页：搜索框和三个开关（区分大小写、全字匹配、正则），可展开的包含 / 排除，
// 一行摘要，下面是按文件分组的结果。点一处在预览标签里打开并选中它，双击 / 回车固定标签；
// 点文件行收起 / 展开，← → 也行；在搜索框里按 ↓ 进结果列表，↑ 翻最近搜索。
// 搜索框左边的箭头（⇧⌘H）展开替换框：结果里画出换成什么，行上的按钮逐处 / 逐个文件替换、排除，"全部替换"先问。
// 搜索框空着时下面是最近搜索。
func searchView(a *app.App, c *wui.Context) {
	t := c.Theme()
	s := &a.Search
	wui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if len(a.Repos) == 0 {
			noRepos(a, c)
			return
		}
		searchInputs(a, c)
		text, failed := a.SearchSummary()
		if text != "" || s.Running || s.CanUndoReplace() { // 撤销上次替换在搜索框清空之后也还在
			row := wui.Row(c).Padding(0, 12, 4, 12).Gap(6).AlignItems(wui.Center).Shrink(0)
			if s.CanOpenSearchEditor() {
				row.ContextMenu(func(m *wui.Menu) {
					if m.Item(i18n.T("在编辑器里打开")).Chosen() {
						a.OpenSearchEditor()
					}
				})
			}
			row.Children(func() {
				st := wui.Text(c, text).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Tooltip(text)
				if failed {
					st.TextColor(t.Danger)
				}
				switch {
				case s.CanSearchMore():
					more := i18n.Tf("再读 %d", app.MoreStep)
					if summaryLink(c, more, i18n.Tf("再多搜 %d 处（最多 %d 处）", app.MoreStep, search.MaxLimit)).Clicked() {
						a.SearchMore()
					}
				case s.AtMaxLimit():
					wui.Text(c, i18n.T("最多 50 000 处")).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Shrink(0)
				}
				if s.Done && !s.Running && summaryLink(c, i18n.T("重新搜索"), i18n.T("按同样的条件再搜一遍：结果里的文件改了会自己跟着变，新配上的文件要重新搜")).Clicked() {
					a.Research()
				}
				if s.CanUndoReplace() && summaryLink(c, i18n.T("撤销上次替换"), s.UndoReplaceTip()).Clicked() {
					a.UndoReplace()
				}
				if s.Running {
					wui.Spinner(c)
				}
				if s.Done && s.Res.Err == nil && len(s.Rows) > 0 {
					squareButton(c, iconSearchDoc, i18n.T("在编辑器里打开"), app.Tip(i18n.T("在编辑器里打开：整页的纯文本，每处带上下文（⌥回车）"), "search.openInEditor"),
						rowToolSize, iconRow, false, a.OpenSearchEditor)
				}
			})
		}
		switch {
		case !s.Done && !s.Running && s.Query.Text == "" && len(a.St.SearchHistory) > 0:
			recentSearches(a, c)
		case !s.Done && !s.Running:
			message(c, iconSearch, i18n.T("在所有仓库里搜文字"), i18n.T("敲字就搜，回车立刻搜"), t.TextMuted)
		case s.Done && len(s.Rows) == 0:
			wui.Spacer(c).Grow(1)
		default:
			searchResults(a, c)
		}
	})
	if s.Focus {
		s.Focus = false
		s.Field.Focus(c)
	}
	if s.FocusRepl {
		s.FocusRepl = false
		s.ReplField.Focus(c)
	}
}

// searchInputs 是搜索页顶上的搜索框（左边是展开替换的箭头）、替换框、范围胶囊、开关和包含 / 排除。
func searchInputs(a *app.App, c *wui.Context) {
	s := &a.Search
	wui.Column(c).Padding(2, 8, 6, 4).Gap(5).Shrink(0).Children(func() {
		wui.Row(c).Gap(2).AlignItems(wui.Stretch).Children(func() {
			replaceToggle(a, c)
			wui.Column(c).Gap(5).Grow(1).MinWidth(0).Children(func() {
				queryField(a, c)
				if s.Replacing {
					replaceField(a, c)
				}
			})
		})
		wui.Column(c).Gap(5).Padding(0, 0, 0, 18).Children(func() {
			if sc := s.Scope; sc != nil {
				wui.Row(c).Children(func() {
					full := filepath.Join(sc.Root, filepath.FromSlash(sc.Dir))
					filterPill(c, iconFolder, sc.Label(), i18n.Tf("只在 %s 里搜", full), i18n.T("清除范围"), a.ClearScope)
				})
			}
			searchToggles(a, c)
			if !s.Globs {
				return
			}
			for _, g := range []struct {
				v            *string
				label, place string
			}{
				{&s.Query.Include, i18n.T("包含的文件"), i18n.T("包含，如 *.go, docs/")},
				{&s.Query.Exclude, i18n.T("排除的文件"), i18n.T("排除，如 *_test.go, vendor/")},
			} {
				in := wui.TextInput(c.Key(g.label), g.v).Label(g.label).Placeholder(g.place).
					Tooltip(i18n.T("逗号分开；不带 / 的比文件名或任一级目录名，带 / 的比路径；支持 * ** ? {a,b}")).FontSize(12)
				if in.Changed() {
					a.SearchChanged()
				}
				if in.Submitted() {
					a.SearchNow()
				}
			}
			for _, o := range []struct {
				on        *bool
				text, tip string
			}{
				{&s.Query.Ignored, i18n.T("也搜被忽略的文件（.gitignore）"), i18n.T("被 .gitignore 忽略的文件一个个列出来一起搜（node_modules 那些，会慢）；不是 Git 仓库的文件夹没有这一说")},
				{&s.Query.Big, i18n.T("也搜 2 MB 以上的大文件"), i18n.T("32 MB 以下的大文件也搜；再大的照旧跳过")},
			} {
				if wui.Checkbox(c, o.on, o.text).FontSize(12).Tooltip(o.tip).Changed() {
					a.SearchNow()
				}
			}
		})
	})
}

// summaryLink 是摘要那一行后面的链接样子的按钮（"再读 5000""重新搜索"那些）：次级色，悬停正文色。
func summaryLink(c *wui.Context, text, tip string) wui.Element {
	t := c.Theme()
	l := wui.Text(c, text).FontSize(sideSub).FontWeight(500).TextColor(t.TextMuted).SingleLine().Shrink(0).Cursor(wui.CursorPointer).Label(text).Tooltip(tip)
	if l.Hovered() {
		l.TextColor(t.Text)
	}
	return l
}

// replaceToggle 是搜索框左边的箭头：展开 / 收起替换框，和搜索框（展开时连同替换框）一样高。
func replaceToggle(a *app.App, c *wui.Context) {
	t := c.Theme()
	s := &a.Search
	label := i18n.T("显示替换")
	if s.Replacing {
		label = i18n.T("隐藏替换")
	}
	b := wui.Box(c).Width(16).Radius(4).AlignItems(wui.Center).Justify(wui.Center).Shrink(0).Cursor(wui.CursorPointer).Label(label)
	tooltip(c, b, app.Tip(i18n.T("搜索并替换"), "view.replace"))
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.08))
	}
	b.Children(func() {
		ic := wui.Icon(c, iconChevronRight).FontSize(iconRow).TextColor(t.TextMuted)
		if s.Replacing {
			ic.Rotate(90)
		}
	})
	if b.Clicked() {
		s.Replacing = !s.Replacing
		if s.Replacing {
			s.FocusRepl = true
		}
	}
}

// queryField 是搜索框：↓ 进结果列表（翻最近搜索时是往回翻），↑ 翻最近搜索。
// ⇧⏎ 在光标处换行，有了换行就换成多行的框（最多 4 行；⏎ 搜、⇧⏎ 换行，没有 ↑ ↓ 翻最近搜索），清空了回到一行。
// 单行的是 TextInput 加上叠在上面的放大镜和清空按钮（searchBox），不用 mygo 的 SearchField：它里面的输入框拿不到，
// ⇧⏎ 只能从 Submitted 加上"这一帧按着的键"猜，真窗口里 ⇧ 在建帧之前就松开了，成了搜索。
func queryField(a *app.App, c *wui.Context) {
	s := &a.Search
	tip := app.Tip(i18n.T("在所有仓库的文件里搜文字，回车立刻搜，⇧回车换行，⌥回车开成搜索编辑器"), "view.search")
	if multiLine(c, &s.MultiQ, &s.Focus, s.Query.Text, &s.Field) {
		f := wui.TextArea(c.Key("qm"), &s.Query.Text).Lines(1, 4).MinHeight(0).Label(i18n.T("搜索文字")).Tooltip(tip).
			FontSize(12).Font(mono()).Bind(&s.Field).Grow(1).MinWidth(0)
		s.Focus = placeCaret(f, &s.CaretQ) || s.Focus
		f.OnTextKey(func(k wui.TextKey) bool {
			if k.Key != wui.KeyEnter || k.Mods != 0 && k.Mods != wui.Alt {
				return false // ⇧⏎ 换行
			}
			text, alt := k.Text(), k.Mods == wui.Alt
			a.Update(func() {
				s.Query.Text = text
				if alt {
					a.SearchToEditor()
				} else {
					a.SearchSubmitted()
				}
			})
			return true
		})
		if f.Changed() {
			a.SearchEdited()
		}
		return
	}
	f := searchBox(a, c)
	s.Field.OnShortcut(c, 0, wui.KeyDown, func() {
		if a.StepSearch(-1) || len(s.Rows) < 2 {
			return
		}
		s.Sel = 1 // 第一处（第 0 行是它的文件）
		s.List.Focus(c)
		a.OpenSearchMatch(s.Rows[1].Match, false)
	})
	if len(a.St.SearchHistory) > 0 {
		s.Field.OnShortcut(c, 0, wui.KeyUp, func() { a.StepSearch(1) })
	}
	shiftEnter(a, f, s.Query.Text, func(text string, caret int) {
		s.Query.Text, s.MultiQ, s.CaretQ = text, true, caret+1
		a.SearchEdited()
	}, func(text string) {
		s.Query.Text = text
		a.SearchToEditor()
	})
	if f.Changed() {
		a.SearchEdited()
	}
	if f.Submitted() {
		a.SearchSubmitted()
	}
}

// searchBox 是侧栏搜索页的搜索框（filterBox），清空时重新搜。
func searchBox(a *app.App, c *wui.Context) wui.Element {
	s := &a.Search
	tip := app.Tip(i18n.T("在所有仓库的文件里搜文字，回车立刻搜，⇧回车换行，⌥回车开成搜索编辑器"), "view.search")
	return filterBox(c, &s.Query.Text, &s.Field, i18n.T("搜索文字"), i18n.T("搜索"), a.SearchEdited).Tooltip(tip)
}

// filterBox 是单行的搜索框：TextInput 留出左边放大镜、右边清空按钮的地方，两个都叠在框上（和 SearchField 一样的位置：
// 放大镜 15 宽、离边 8，清空按钮 16 见方的圆、离边 8，和字隔 6）。有字时 Esc 和点清空按钮都是清空（清完调 cleared，可以为 nil）、
// 焦点留在框里。返回输入框；外面包着的一行撑满给它的宽度。
func filterBox(c *wui.Context, text *string, h *wui.Handle, label, placeholder string, cleared func()) wui.Element {
	t := c.Theme()
	const edge, glyph, knob, gap = 8, 15, 16, 6
	end := float32(edge)
	if *text != "" {
		end += knob + gap
	}
	var f wui.Element
	clear := func() {
		*text = ""
		if cleared != nil {
			cleared()
		}
		h.Focus(c)
	}
	wui.Row(c).Grow(1).MinWidth(0).AlignItems(wui.Center).Children(func() {
		f = wui.TextInput(c.Key("q"), text).Label(label).Placeholder(placeholder).
			FontSize(12).Bind(h).Grow(1).MinWidth(0).PaddingStart(edge + glyph + gap).PaddingEnd(end)
		wui.Row(c).Absolute().Left(edge).Top(0).Bottom(0).Width(glyph).AlignItems(wui.Center).Justify(wui.Center).
			PassThrough().Children(func() {
			wui.Icon(c, iconSearch).FontSize(iconRow).TextColor(t.TextMuted)
		})
		if *text == "" {
			return
		}
		wui.Row(c).Absolute().Right(edge).Top(0).Bottom(0).AlignItems(wui.Center).Children(func() {
			x := wui.Box(c).Size(knob, knob).Radius(knob / 2).Background(t.TextMuted.Alpha(0.5)).Label(i18n.T("清除")).
				Cursor(wui.CursorDefault).Draw(func(p *wui.Painter, r wui.Rect) {
				k := r.W * 0.3
				var path wui.Path
				path.MoveTo(r.X+k, r.Y+k).LineTo(r.X+r.W-k, r.Y+r.H-k).MoveTo(r.X+r.W-k, r.Y+k).LineTo(r.X+k, r.Y+r.H-k)
				p.StrokePath(&path, 1.5, t.Background)
			})
			if x.Clicked() {
				clear()
			}
		})
	})
	if *text != "" {
		h.OnShortcut(c, 0, wui.KeyEscape, clear)
	}
	return f
}

// replaceField 是替换框和"全部替换（N 处）"：框里回车也是全部替换（都先问）；⇧⏎ 换行，和搜索框一样换成多行的框。
func replaceField(a *app.App, c *wui.Context) {
	s := &a.Search
	n, _ := s.Count()
	wui.Row(c).Gap(4).AlignItems(wui.Center).Children(func() {
		tip := i18n.T("空着就是删掉；⇧回车换行；开着的文件改编辑器里的，没开的直接写盘")
		if multiLine(c, &s.MultiR, &s.FocusRepl, s.Replace, &s.ReplField) {
			in := wui.TextArea(c.Key("replm"), &s.Replace).Lines(1, 4).MinHeight(0).Label(i18n.T("替换为")).Tooltip(tip).
				FontSize(12).Font(mono()).Bind(&s.ReplField).Grow(1).MinWidth(0)
			s.FocusRepl = placeCaret(in, &s.CaretR) || s.FocusRepl
			in.OnTextKey(func(k wui.TextKey) bool {
				if k.Key != wui.KeyEnter || k.Mods != 0 {
					return false
				}
				text := k.Text()
				a.Update(func() {
					s.Replace = text
					if n, _ := s.Count(); n > 0 && s.CanReplace() {
						a.ReplaceAll()
					}
				})
				return true
			})
			return
		}
		place := i18n.T("替换为…")
		if s.Query.Regexp {
			place = i18n.T("替换为…（$1 是第一个括号）")
		}
		in := wui.TextInput(c.Key("repl"), &s.Replace).Label(i18n.T("替换为")).Placeholder(place).FontSize(12).
			Tooltip(tip).Bind(&s.ReplField).Grow(1).MinWidth(0)
		shiftEnter(a, in, s.Replace, func(text string, caret int) {
			s.Replace, s.MultiR, s.CaretR = text, true, caret+1
		}, nil)
		if in.Submitted() && n > 0 && s.CanReplace() {
			a.ReplaceAll()
		}
	})
	label := i18n.Tf("全部替换（%d 处）", n)
	wui.Row(c).Gap(4).AlignItems(wui.Center).Children(func() { // 右边的“预览”开第一个文件的替换预览
		if wui.PrimaryButton(c, label).FontSize(11.5).Tooltip(app.Tip(i18n.T("全部替换…"), "search.replaceAll")).
			Disabled(n == 0 || !s.CanReplace()).Grow(1).MinWidth(0).Clicked() {
			a.ReplaceAll()
		}
		if wui.Button(c, i18n.T("预览")).FontSize(11.5).Tooltip(i18n.T("先在一个 diff 标签里看第一个文件换完的样子，不写盘")).
			Disabled(n == 0 || !s.CanReplace()).Shrink(0).Clicked() {
			a.PreviewReplaceFirst()
		}
	})
}

// multiLine 是框这一帧画不画成多行的：字里有换行、或换过行还没清空。清空了回到一行时，
// 原来键盘在多行的框里就把焦点交给新的单行框（focus 是下一帧给焦点的那个开关）。
func multiLine(c *wui.Context, multi, focus *bool, text string, h *wui.Handle) bool {
	switch {
	case strings.Contains(text, "\n"):
		*multi = true
	case *multi && text == "":
		*multi = false
		*focus = *focus || h.Focused(c)
	}
	return *multi
}

// shiftEnter 让单行框的 ⇧⏎ 在光标处插一个换行（选中的换掉），set 拿到新的字和换行后面的光标（字数），框下一帧换成多行的。
// 光标是这一帧建框时的；按键之前又打了字（框里的和 built 不一样）就加在最后。alt 不为 nil 时 ⌥⏎ 拿框里的字调它（搜索框开成搜索编辑器）。
func shiftEnter(a *app.App, f wui.Element, built string, set func(text string, caret int), alt func(text string)) {
	start, end := f.TextSelection()
	f.OnTextKey(func(k wui.TextKey) bool {
		if alt != nil && k.Key == wui.KeyEnter && k.Mods == wui.Alt {
			text := k.Text()
			a.Update(func() { alt(text) })
			return true
		}
		if k.Key != wui.KeyEnter || k.Mods != wui.Shift {
			return false
		}
		now := k.Text()
		r := []rune(now)
		st, en := min(start, len(r)), min(max(end, start), len(r))
		if now != built {
			st, en = len(r), len(r)
		}
		text := string(r[:st]) + "\n" + string(r[en:])
		a.Update(func() { set(text, st+1) })
		return true
	})
}

// placeCaret 在换成多行的框那一帧把光标放到第 *caret−1 个字后面（0 是不放），放了就返回真：这一帧把焦点给它
// （焦点要等新的框建出来才给得上，换的那一帧给的还是旧的单行框）。
func placeCaret(f wui.Element, caret *int) bool {
	if *caret == 0 {
		return false
	}
	f.SetTextSelection(*caret-1, *caret-1)
	*caret = 0
	return true
}

// searchToggles 是三个开关和"包含 / 排除"的展开按钮。
func searchToggles(a *app.App, c *wui.Context) {
	s := &a.Search
	wui.Row(c).Gap(4).AlignItems(wui.Center).Children(func() {
		for _, o := range []struct {
			on          *bool
			text, label string
		}{
			{&s.Query.CaseSensitive, "Aa", i18n.T("区分大小写")},
			{&s.Query.WholeWord, "ab", i18n.T("全字匹配")},
			{&s.Query.Regexp, ".*", i18n.T("正则表达式")},
		} {
			if filterToggle(c, o.label, o.on, o.label, o.label, func(fg wui.Color) {
				wui.Text(c, o.text).Font(mono()).FontSize(11).TextColor(fg).SingleLine()
			}).Changed() {
				a.SearchNow()
			}
		}
		wui.Spacer(c).Grow(1)
		g := filterToggle(c, "globs", &s.Globs, i18n.T("包含 / 排除的文件"), i18n.T("只搜 / 不搜哪些文件"),
			func(fg wui.Color) { wui.Icon(c, iconFilter).FontSize(iconRow).TextColor(fg) })
		if g.Changed() && !s.Globs && (s.Query.Include != "" || s.Query.Exclude != "" || s.Query.Ignored || s.Query.Big) {
			// 收起就是不再按它们筛（两个"也搜"一样）
			s.Query.Include, s.Query.Exclude = "", ""
			s.Query.Ignored, s.Query.Big = false, false
			a.SearchNow()
		}
	})
}

// searchTail 是最近搜索一行后面的灰字：当时开着的开关（Aa ab .*）、包含的、"−"排除的、两个"也搜"。没有是 ""。
func searchTail(e workspace.SearchEntry) string {
	var parts []string
	for _, f := range []struct {
		on   bool
		text string
	}{
		{e.Case, "Aa"}, {e.Word, "ab"}, {e.Regexp, ".*"},
		{e.Include != "", e.Include}, {e.Exclude != "", "−" + e.Exclude},
		{e.Ignored, i18n.T("忽略")}, {e.Big, i18n.T("大")},
	} {
		if f.on {
			parts = append(parts, f.text)
		}
	}
	return strings.Join(parts, " ")
}

// recentSearches 是搜索框空着时的最近搜索：点一条拿它来搜，× 去掉一条，"清除"全清。
func recentSearches(a *app.App, c *wui.Context) {
	t := c.Theme()
	wui.Row(c).Padding(4, 12, 4, 12).Gap(6).AlignItems(wui.Center).Shrink(0).Children(func() {
		wui.Text(c, i18n.T("最近搜索")).FontSize(sideSub).FontWeight(600).TextColor(t.TextMuted).SingleLine().Grow(1)
		b := wui.Text(c, i18n.T("清除")).FontSize(sideSub).FontWeight(500).TextColor(t.TextMuted).Cursor(wui.CursorPointer).
			Label(i18n.T("清除最近搜索")).Tooltip(i18n.T("清除最近搜索"))
		if b.Hovered() {
			b.TextColor(t.Text)
		}
		if b.Clicked() {
			a.ClearSearches()
		}
	})
	wui.Scroll(c.Key("recent")).Grow(1).MinHeight(0).Padding(0, 4, 4, 4).Children(func() {
		for i, e := range a.St.SearchHistory {
			q, tail := strings.ReplaceAll(e.Text, "\n", "⏎"), searchTail(e)
			label := q
			if tail != "" {
				label += "  " + tail
			}
			el := wui.Row(c.Key(i)).Height(24).Padding(0, 4, 0, 8).Gap(6).Radius(rowRadius).AlignItems(wui.Center).
				Cursor(wui.CursorPointer).Label(i18n.Tf("最近搜索 %s", label)).Tooltip(label)
			hover := el.Hovered()
			if hover {
				el.Background(t.Text.Alpha(0.06))
			}
			el.Children(func() {
				wui.Icon(c, iconHistory).FontSize(iconRow).TextColor(t.TextMuted).Shrink(0)
				wui.Text(c, q).Font(mono()).FontSize(sideMono).SingleLine().Ellipsis("…").MinWidth(0).Shrink(0).MaxWidth(320)
				if tail != "" {
					wui.Text(c, tail).Font(mono()).FontSize(sideSub).TextColor(t.TextMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				}
				wui.Spacer(c).Grow(1)
				if hover {
					rowButton(c, iconClose, i18n.Tf("从最近搜索里去掉 %s", q), i18n.T("从最近搜索里去掉"), func() { a.ForgetSearch(i) })
				}
			})
			if el.Clicked() {
				a.UseSearch(i)
			}
		}
	})
}

// searchResults 是按文件分组的结果列表。键盘在列表上时 ⌘↩ 替换选中的一处 / 文件（替换框开着时），⌫ 排除它。
func searchResults(a *app.App, c *wui.Context) {
	s := &a.Search
	s.List.Selected = &s.Sel
	s.List.Key = func(i int) any {
		if i >= len(s.Rows) {
			return nil
		}
		if r := s.Rows[i]; r.File {
			return -1 - r.Match
		}
		return s.Rows[i].Match
	}
	s.List.Label = func(i int) string {
		if i >= len(s.Rows) || s.Rows[i].Match >= len(s.Res.Matches) {
			return ""
		}
		r := s.Rows[i]
		m := s.Res.Matches[r.Match]
		if r.File {
			return i18n.Tf("结果文件 %s/%s", m.Repo, m.Path)
		}
		return i18n.Tf("结果 %s/%s:%s:%s", m.Repo, m.Path, strconv.Itoa(m.Line+1), strconv.Itoa(m.Col+1))
	}
	s.Hover = -1
	// 行上的按钮、右键菜单会改 Rows：等整个列表建完再做，不然后面几行拿着旧的下标越界
	var later func()
	do := func(fn func()) { later = fn }
	list := sideList(a, c, &s.List, len(s.Rows), func(row wui.ListRow) { searchRow(a, row, s.Rows[row.Index], do) })
	if later != nil {
		later()
	}
	if list.Changed() {
		a.OnSearchRow(s.Hover == s.Sel, false)
	}
	if list.Submitted() {
		a.OnSearchRow(false, true)
	}
	s.List.OnShortcut(c, 0, wui.KeyLeft, func() { a.FoldSearchFile(false) })
	s.List.OnShortcut(c, 0, wui.KeyRight, func() { a.FoldSearchFile(true) })
	// 只在列表自己有焦点时登记：⌫ 在搜索框里是删字
	if s.List.Focused(c) && s.Sel >= 0 && s.Sel < len(s.Rows) {
		sr := s.Rows[s.Sel]
		if s.Replacing && s.CanReplace() {
			onKey(c, &s.List.Handle, "search.replaceOne", func() { replaceRow(a, sr) })
		}
		onKey(c, &s.List.Handle, "search.dismiss", func() { dismissRow(a, sr) })
	}
}

// replaceRow 替换结果列表的一行：文件行是这个文件里没排除的全部，一处就是那一处。
func replaceRow(a *app.App, sr app.SearchRow) {
	if sr.File {
		a.ReplaceFile(sr.Match)
	} else {
		a.ReplaceOne(sr.Match)
	}
}

// dismissRow 排除结果列表的一行：文件行排除整个文件。
func dismissRow(a *app.App, sr app.SearchRow) {
	if sr.File {
		a.DismissFile(sr.Match)
	} else {
		a.DismissMatch(sr.Match)
	}
}

// searchRow 画结果列表的一行：文件（箭头、名字、目录和仓库、处数），或一处（行号和那一行，命中的字高亮；
// 替换框开着时命中的字划掉、后面跟着换成的字）。指针在上面时右边是替换 / 排除按钮。
func searchRow(a *app.App, row wui.ListRow, sr app.SearchRow, do func(func())) {
	c := row.Context
	t := c.Theme()
	s := &a.Search
	m := s.Res.Matches[sr.Match]
	sel := onAccent(a, row.Selected())
	muted, hl := t.TextMuted, t.Warning.Alpha(0.3)
	del, add := t.Danger.Alpha(0.18), t.Success.Alpha(0.2)
	if sel {
		muted, hl = t.AccentText.Alpha(0.75), t.AccentText.Alpha(0.28)
		del, add = t.AccentText.Alpha(0.16), t.AccentText.Alpha(0.32)
	}
	replacing := s.Replacing && s.CanReplace()
	if sr.File {
		el := wui.Row(c).Height(24).Padding(0, 4, 0, 4).Gap(5).AlignItems(wui.Center).Cursor(wui.CursorPointer).Tooltip(m.Repo + "/" + m.Path).
			ContextMenu(func(mn *wui.Menu) { searchFileMenu(a, mn, sr, do) })
		hover := el.Hovered()
		if hover {
			s.Hover = row.Index
		}
		el.Children(func() {
			ic := wui.Icon(c, iconChevronRight).FontSize(iconRow).TextColor(muted).Shrink(0)
			if sr.Open {
				ic.Rotate(90)
			}
			fileGlyph(a, c, m.Path, sel).Shrink(0)
			wui.Text(c, path.Base(m.Path)).FontSize(sideFont).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			where := m.Repo
			if dir := path.Dir(m.Path); dir != "." {
				where += " · " + dir
			}
			if m.Enc != "" && m.Enc != docs.UTF8BOM { // 不是 UTF-8、按认出的编码解了再搜的
				where += " · " + m.Enc.Label()
			}
			wui.Text(c, where).FontSize(sideSub).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2)
			wui.Spacer(c).Grow(1)
			if !hover {
				pill(c, strconv.Itoa(sr.Count), t.TextMuted, sel)
				return
			}
			if replacing {
				label := i18n.Tn("替换这个文件里的 %d 处", sr.Count)
				rowButton(c, iconReplace, label, label, func() { do(func() { a.ReplaceFile(sr.Match) }) })
			}
			rowButton(c, iconClose, i18n.T("排除这个文件"), i18n.T("排除这个文件（不显示，全部替换时跳过）"), func() { do(func() { a.DismissFile(sr.Match) }) })
		})
		return
	}
	el := wui.Row(c).Height(22).Padding(0, 4, 0, 24).Gap(8).AlignItems(wui.Center).
		ContextMenu(func(mn *wui.Menu) { searchMatchMenu(a, mn, sr, do) })
	hover := el.Hovered()
	if hover {
		s.Hover = row.Index
	}
	el.Children(func() {
		wui.Text(c, strconv.Itoa(m.Line+1)).Font(mono()).FontSize(sideSub).TextColor(muted).SingleLine().Shrink(0)
		before, hit, after := previewParts(m)
		font := mono()
		spans := []wui.Span{{Text: before, Font: font, Size: sideMono}}
		if s.Replacing {
			spans = append(spans, wui.Span{Text: hit, Font: font, Size: sideMono, Background: del, Radius: 3, Strikethrough: true})
			if repl := s.Preview(sr.Match); repl != "" {
				spans = append(spans, wui.Span{Text: repl, Font: font, Size: sideMono, Background: add, Radius: 3})
			}
		} else {
			spans = append(spans, wui.Span{Text: hit, Font: font, Size: sideMono, Background: hl, Radius: 3})
		}
		spans = append(spans, wui.Span{Text: after, Font: font, Size: sideMono})
		wui.RichText(c, spans...).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Shrink(1)
		if !hover {
			return
		}
		if replacing {
			rowButton(c, iconReplace, i18n.T("替换这一处"), app.Tip(i18n.T("替换这一处"), "search.replaceOne"), func() { do(func() { a.ReplaceOne(sr.Match) }) })
		}
		rowButton(c, iconClose, i18n.T("排除"), app.Tip(i18n.T("排除这一处（不显示，全部替换时跳过）"), "search.dismiss"), func() { do(func() { a.DismissMatch(sr.Match) }) })
	})
}

// searchMatchMenu 是结果里一处的右键菜单；改结果的动作经 do 等列表建完再做。
func searchMatchMenu(a *app.App, m *wui.Menu, sr app.SearchRow, do func(func())) {
	s := &a.Search
	mt := s.Res.Matches[sr.Match]
	if s.Replacing {
		if shortcut(m.Item(i18n.T("替换这一处")), "search.replaceOne").Disabled(!s.CanReplace()).Chosen() {
			do(func() { a.ReplaceOne(sr.Match) })
		}
		if m.Item(i18n.Tn("替换这个文件里的 %d 处", s.FileCount(sr.Match))).Disabled(!s.CanReplace()).Chosen() {
			do(func() { a.ReplaceFile(sr.Match) })
		}
		if m.Item(i18n.T("预览替换…")).Disabled(!s.CanReplace()).Chosen() {
			a.PreviewReplace(sr.Match, true)
		}
	}
	if shortcut(m.Item(i18n.T("排除这一处")), "search.dismiss").Chosen() {
		do(func() { a.DismissMatch(sr.Match) })
	}
	if m.Item(i18n.T("排除这个文件")).Chosen() {
		do(func() { a.DismissFile(sr.Match) })
	}
	m.Separator()
	if m.Item(i18n.T("在侧边打开")).Chosen() {
		a.OpenSearchMatchSide(sr.Match)
	}
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(filepath.Join(mt.Root, filepath.FromSlash(mt.Path)))
	}
}

// searchFileMenu 是结果里文件行的右键菜单；改结果的动作经 do 等列表建完再做。
func searchFileMenu(a *app.App, m *wui.Menu, sr app.SearchRow, do func(func())) {
	s := &a.Search
	mt := s.Res.Matches[sr.Match]
	if s.Replacing {
		if shortcut(m.Item(i18n.Tn("替换这个文件里的 %d 处", sr.Count)), "search.replaceOne").Disabled(!s.CanReplace()).Chosen() {
			do(func() { a.ReplaceFile(sr.Match) })
		}
		if m.Item(i18n.T("预览替换…")).Disabled(!s.CanReplace()).Chosen() {
			a.PreviewReplace(sr.Match, false)
		}
	}
	if shortcut(m.Item(i18n.T("排除这个文件")), "search.dismiss").Chosen() {
		do(func() { a.DismissFile(sr.Match) })
	}
	m.Separator()
	r := a.RepoFor(mt.Root)
	if m.Item(i18n.T("只在这个目录里搜")).Disabled(r == nil).Chosen() {
		dir := path.Dir(mt.Path)
		if dir == "." {
			dir = ""
		}
		do(func() { a.SearchIn(r, dir) })
	}
	if m.Item(i18n.T("复制路径")).Chosen() {
		copyText(filepath.Join(mt.Root, filepath.FromSlash(mt.Path)))
	}
}

// previewParts 把一处结果的预览切成命中前、命中的字、命中后三段。
func previewParts(m search.Match) (before, hit, after string) {
	p := m.Preview
	lo := min(m.PreviewCol, len(p))
	hi := min(lo+m.Len, len(p))
	return p[:lo], p[lo:hi], p[hi:]
}
