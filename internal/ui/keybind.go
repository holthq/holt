package ui

import (
	"cmp"
	"strings"

	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/i18n"
)

// 这个文件是快捷键自定义（app/keybind.go）在界面这边的部分：右键菜单和列表的键从注册表读，
// 设置里的"快捷键"页和录键卡片。

// shortcut 给菜单项标上命令 id 现在的键（keybindings.json 改过的照改后的；去掉了就不标）。
func shortcut[T interface {
	Shortcut(wui.Modifiers, wui.Key) T
}](it T, id string) T {
	if mods, key, ok := app.KeyOf(id); ok {
		it.Shortcut(mods, key)
	}
	return it
}

// onKey 在 h 上登记命令 id 现在的键，去掉了就不登记。
func onKey(c *wui.Context, h *wui.Handle, id string, f func()) {
	if mods, key, ok := app.KeyOf(id); ok {
		h.OnShortcut(c, mods, key, f)
	}
}

// keysPage 是设置里"快捷键"页的号（settingsPages 的最后一个）。
const keysPage = 8

// keysResultsMax 是设置的搜索结果里最多列几条命令，多了给一个"在快捷键页里看全部"。
const keysResultsMax = 20

// keysView 是设置 → 快捷键页的状态（SettingsView 里一份，关了设置窗口再开是空的）。
type keysView struct {
	query string
	box   wui.Handle
	list  wui.ListState
	sel   int
	rec   keyRec
}

// keyItem 是快捷键表的一行：段的标题（cmd 为 nil）或一条命令。
type keyItem struct {
	title string
	cmd   *app.Command
}

// keyItems 是对得上 q（小写、去掉了首尾空白）的行：每段先一行标题，段里一条都对不上的整段不要。
func keyItems(q string) []keyItem {
	var out []keyItem
	for _, g := range app.KeyGroups() {
		head := false
		for _, c := range g.Commands {
			if !app.KeyMatches(c, q) {
				continue
			}
			if !head {
				out = append(out, keyItem{title: g.Title})
				head = true
			}
			out = append(out, keyItem{cmd: c})
		}
	}
	return out
}

// shortcutsPage 是设置里的"快捷键"页：顶上搜索框和 ⋯（打开 keybindings.json、重置全部），keybindings.json 里有没用上的
// 条目时一块提示，下面所有命令的表（KeyGroups 的顺序，没有键的也在）。双击一行、选中后回车或点"改…"开录键卡片。
func shortcutsPage(a *app.App, c *wui.Context, k *keysView, toMain func()) {
	t := c.Theme()
	q := strings.ToLower(strings.TrimSpace(k.query))
	items := keyItems(q)
	wui.Column(c).Grow(1).MinHeight(0).Padding(14, 20, 0).Gap(10).Children(func() {
		wui.Row(c).Gap(8).AlignItems(wui.Center).Shrink(0).Children(func() {
			wui.Row(c).Grow(1).MinWidth(0).Children(func() {
				filterBox(c, &k.query, &k.box, i18n.T("搜索快捷键"), i18n.T("命令、id 或键（⇧⌘P、cmd+shift+p）"), nil)
			})
			iconButton(c, iconMore, i18n.T("更多"), func() {}).Menu(func(m *wui.Menu) {
				if m.Item(i18n.T("打开 keybindings.json")).Disabled(a.KeybindingsPath() == "").Chosen() {
					a.Update(a.OpenKeybindings)
					toMain()
				}
				m.Separator()
				if m.Item(i18n.T("重置全部快捷键…")).Disabled(!app.KeysCustomized()).Chosen() {
					toMain()
					a.Update(a.ResetAllKeybindings)
				}
			})
		})
		if errs := app.KeyErrors(); len(errs) > 0 {
			keyErrorsBox(a, c, errs, toMain)
		}
		if len(items) == 0 {
			message(c, iconSearch, i18n.T("没有匹配的命令"), "", t.TextMuted)
			return
		}
		k.list.Key = func(i int) any {
			if it := items[i]; it.cmd != nil {
				return it.cmd.ID
			}
			return "#" + items[i].title
		}
		k.list.Label = func(i int) string {
			if it := items[i]; it.cmd != nil {
				return it.cmd.Label() + " " + cmp.Or(app.ShownKeys(it.cmd), "—")
			}
			return items[i].title
		}
		k.list.Header = func(i int) bool { return items[i].cmd == nil }
		k.list.Selected = &k.sel
		l := newListLook(a, c, 0)
		var list wui.Element
		l.build(c, func() {
			list = wui.List(c.Key("keys"), &k.list, len(items)).Grow(1).MinHeight(0).PaddingY(2).
				Rows(l.rows(func(row wui.ListRow) {
					if it := items[row.Index]; it.cmd != nil {
						keyRow(a, row.Context, it.cmd, row.Selected(), true, &k.rec)
					} else {
						keyHeader(row.Context, it.title, row.Index == 0)
					}
				}))
		})
		if i := k.sel; list.Submitted() && i >= 0 && i < len(items) && items[i].cmd != nil {
			k.rec.start(a, items[i].cmd)
		}
		wui.Text(c, i18n.T("同一处（窗口、终端、文件树……）两条命令同一个键，后改的那条赢，被顶掉的没有快捷键。不支持和弦。")).
			FontSize(11).TextColor(t.TextMuted).Padding(2, 4, 12).Shrink(0)
	})
}

// keyErrorsBox 是快捷键页顶上 keybindings.json 没用上的条目：一行说几条、每条一行灰字，右边"打开 keybindings.json"。
func keyErrorsBox(a *app.App, c *wui.Context, errs []string, toMain func()) {
	t := c.Theme()
	wui.Row(c).Padding(8, 10).Gap(8).Radius(8).Background(quietFill(t)).AlignItems(wui.Start).Shrink(0).Children(func() {
		wui.Icon(c, iconAlert).FontSize(iconRow).TextColor(t.Danger).Shrink(0)
		wui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
			wui.Text(c, i18n.Tn("keybindings.json 里有 %d 条没用上，其余的照常生效", len(errs))).FontSize(12).FontWeight(600)
			for i, e := range errs {
				wui.Text(c.Key(i), e).FontSize(11.5).TextColor(t.TextMuted).MinWidth(0)
			}
		})
		if a.KeybindingsPath() != "" && linkButton(c, i18n.T("打开"), i18n.T("在编辑器里打开 keybindings.json")).Clicked() {
			a.Update(a.OpenKeybindings)
			toMain()
		}
	})
}

// keyHeader 是快捷键表里一段的标题（菜单组、上下文、编辑器的分组）。
func keyHeader(c *wui.Context, title string, first bool) {
	top := float32(14)
	if first {
		top = 4
	}
	wui.Text(c, title).FontSize(11.5).FontWeight(600).TextColor(c.Theme().TextMuted).Padding(top, 10, 4, 10)
}

// keyRow 是快捷键表的一行：命令名、"自定义"胶囊、被顶掉时灰字"（被 X 占了）"，右边键（没有写"—"）；悬停或选中时键左边
// 出"改…"、×（去掉）和自定义的"重置"。inList 是在快捷键页的列表里，不然是设置搜索结果里的一行（加上下文胶囊，全局的不写）。
// 双击开录键卡片。
func keyRow(a *app.App, c *wui.Context, cmd *app.Command, sel, inList bool, rec *keyRec) {
	t := c.Theme()
	on := onAccent(a, sel)
	muted := t.TextMuted
	if on {
		muted = t.AccentText.Alpha(0.75)
	}
	el := wui.Row(c).Padding(4, 10).Gap(8).AlignItems(wui.Center).MinHeight(30)
	if inList && el.RowDoubleClicked() || !inList && el.DoubleClicked() {
		rec.start(a, cmd)
	}
	hover := el.Hovered() || sel
	el.Children(func() {
		wui.Text(c, cmd.Label()).FontSize(12.5).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(cmd.ID)
		if !inList && cmd.Context != app.Global {
			pill(c, i18n.T(cmd.Context.String()), t.TextMuted, on)
		}
		if cmd.Custom {
			pill(c, i18n.T("自定义"), t.TextMuted, on)
		}
		if w := app.Find(cmd.Shadowed); cmd.Shadowed != "" && w != nil {
			wui.Text(c, i18n.Tf("（被 %s 占了）", w.Label())).FontSize(11.5).TextColor(muted).SingleLine().Ellipsis("…").MinWidth(0)
		}
		wui.Box(c).Grow(1)
		keys := app.ShownKeys(cmd)
		if hover {
			textButtonOn(c, i18n.T("改…"), i18n.T("按下新的快捷键"), on, func() { rec.start(a, cmd) })
			if keys != "" {
				rowButton(c, iconClose, i18n.T("去掉快捷键"), i18n.T("去掉快捷键"), func() { a.SetKeybinding(cmd, 0, 0) })
			}
			if cmd.Custom {
				textButtonOn(c, i18n.T("重置"), i18n.Tf("回到默认：%s", cmp.Or(app.DefaultKeys(cmd), "—")), on, func() { a.ResetKeybinding(cmd) })
			}
		}
		col := t.Text
		if on {
			col = t.AccentText
		}
		if keys == "" {
			keys, col = "—", muted
		}
		wui.Text(c, keys).FontSize(12.5).TextColor(col).SingleLine().Shrink(0).MinWidth(64)
	})
}

// keyResults 是设置搜索结果里对得上 q 的命令（"快捷键"一组，在别的组后面），最多 keysResultsMax 条，多了给一个
// "在快捷键页里看全部"（more）。没有对得上的返回 false。
func keyResults(a *app.App, c *wui.Context, q string, rec *keyRec, more func()) bool {
	var hits []*app.Command
	for _, g := range app.KeyGroups() {
		for _, cmd := range g.Commands {
			if app.KeyMatches(cmd, q) {
				hits = append(hits, cmd)
			}
		}
	}
	if len(hits) == 0 {
		return false
	}
	t := c.Theme()
	wui.Column(c.Key("keys")).Gap(2).Children(func() {
		wui.Text(c, i18n.T("快捷键")).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted).Margin(0, 0, 6, 0)
		for _, cmd := range hits[:min(len(hits), keysResultsMax)] {
			keyRow(a, c.Key(cmd.ID), cmd, false, false, rec)
		}
		if len(hits) > keysResultsMax && linkButton(c, i18n.Tn("在快捷键页里看全部 %d 条", len(hits)), "").Clicked() {
			more()
		}
	})
	return true
}

// keyRec 是录键卡片：给哪条命令录、按下的键（got：按过了；mods、key 都是 0 = 不要快捷键）、按了不能用的键（bad）。
type keyRec struct {
	open bool
	cmd  *app.Command
	mods wui.Modifiers
	key  wui.Key
	got  bool
	bad  bool
}

// start 开录键卡片；录着的时候菜单栏不带快捷键（不然 ⌘ 组合键先被菜单接走）。
func (r *keyRec) start(a *app.App, cmd *app.Command) {
	*r = keyRec{open: true, cmd: cmd}
	a.SetKeyRecording(true)
}

// editKeys 是菜单栏"编辑"里那几项的键：录着的时候它们照样先被菜单接到，作为命令（InputCommand）送到有焦点的元素。
var editKeys = map[string]struct {
	mods wui.Modifiers
	key  wui.Key
}{
	"undo": {wui.Cmd, wui.KeyZ}, "redo": {wui.Cmd | wui.Shift, wui.KeyZ}, "cut": {wui.Cmd, wui.KeyX},
	"copy": {wui.Cmd, wui.KeyC}, "paste": {wui.Cmd, wui.KeyV}, "selectAll": {wui.Cmd, wui.KeyA},
}

// input 是录键卡片里按下的键：只按修饰键不算（mygo 不当按键送）；Esc 取消；还没按过时 ⏎ 是录的键，按过以后 ⏎ 是确定；
// 单按 ⌫ / ⌦ 是不要快捷键；没有名字的键（KeyBack 这些）不让录。
func (r *keyRec) input(ev wui.InputEvent, save func()) bool {
	k, mods := ev.Key, ev.Mods&(wui.Shift|wui.Ctrl|wui.Alt|wui.Super)
	switch ev.Kind {
	case wui.InputKeyDown:
	case wui.InputCommand:
		e, ok := editKeys[ev.Text]
		if !ok {
			return false
		}
		k, mods = e.key, e.mods
	default:
		return false
	}
	if k == wui.KeyUnknown {
		k = ev.PhysicalKey
	}
	switch {
	case k == wui.KeyEscape && mods == 0:
		r.open = false
	case k == wui.KeyEnter && mods == 0 && r.got:
		save()
	case (k == wui.KeyBackspace || k == wui.KeyDelete) && mods == 0:
		r.mods, r.key, r.got, r.bad = 0, 0, true, false
	case !app.Recordable(k):
		r.bad = true
	default:
		r.mods, r.key, r.got, r.bad = mods, k, true, false
	}
	return true
}

// keyRecorder 是录键卡片（dialogFrame 那种居中小卡）：一行大字是按下的键（还没按是灰字"等你按…"），下面一行说结果
// （和谁冲突、没有冲突），确定写进 keybindings.json。关掉时菜单栏的快捷键回来。
func keyRecorder(a *app.App, c *wui.Context, r *keyRec) {
	if r.cmd != nil && r.open {
		t := c.Theme()
		cmd := r.cmd
		save := func() {
			if r.got {
				a.SetKeybinding(cmd, r.mods, r.key)
			}
			r.open = false
		}
		dialogFrame(c, &r.open, dialogOpts{Title: i18n.T("按下新的快捷键"), Note: cmd.Label(), Width: dlgConfirm, Hint: i18n.T("确定")}, func() {
			wui.Box(c).Focusable().AutoFocus().Label(i18n.T("按下新的快捷键")).Height(64).Radius(8).Background(quietFill(t)).
				AlignItems(wui.Center).Justify(wui.Center).HandleInput(func(ev wui.InputEvent) bool { return r.input(ev, save) }).
				Children(func() {
					switch {
					case !r.got:
						wui.Text(c, i18n.T("等你按…")).FontSize(15).TextColor(t.TextMuted)
					case r.key == 0:
						wui.Text(c, i18n.T("不要快捷键")).FontSize(15).TextColor(t.TextMuted)
					default:
						wui.Text(c, app.KeysOf(r.mods, r.key)).FontSize(24).FontWeight(600)
					}
				})
			line, col := keyRecLine(cmd, r), t.TextMuted
			if r.bad {
				col = t.Danger
			} else if r.got && r.key != 0 && app.KeyConflict(cmd, r.mods, r.key) != nil {
				col = t.Warning
			}
			wui.Text(c, line).FontSize(11.5).TextColor(col).MinWidth(0)
		}, func() {
			if dlgButton(c, i18n.T("取消"), btnPlain, false, false) {
				r.open = false
			}
			if dlgButton(c, i18n.T("确定"), btnPrimary, !r.got, false) {
				save()
			}
		})
	}
	if r.cmd != nil && !r.open {
		r.cmd = nil
		a.SetKeyRecording(false)
	}
}

// keyRecLine 是录键卡片大字下面那一行。
func keyRecLine(cmd *app.Command, r *keyRec) string {
	keys := app.KeysOf(r.mods, r.key)
	switch {
	case r.bad:
		return i18n.T("这个键不能用")
	case !r.got:
		return i18n.T("按一个键或组合键；单按 ⌫ 是不要快捷键")
	case r.key == 0:
		return i18n.T("保存后这条命令没有快捷键")
	case keys == app.ShownKeys(cmd):
		return i18n.T("和现在的一样")
	}
	if o := app.KeyConflict(cmd, r.mods, r.key); o != nil {
		return i18n.Tf("%s — 已被 %s 用着，保存后它没有快捷键", keys, o.Label())
	}
	return i18n.T("没有冲突")
}
