package ui

import (
	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/app"
	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 冲突：有冲突标记的文件标签上面一条 mergeBar，编辑器里按块染底色、<<<<<<< 行的槽里一个红点，
// 右键菜单里解决光标所在的那块，⌃⌥1 / 2 / 3、⌥F7 之类在编辑器的 OnInput 里接。解决的动作在 app/conflicts.go。

// mergeBar 显示在 conflictBar 下面：第几处冲突、解决这一处（用我的 / 用他们的 / 两个都要）、上一处 / 下一处、
// 全部用某边、两边的 diff。标记都处理完了、仓库还把它当冲突时，换成"标记为已解决"。
func mergeBar(a *app.App, c *wui.Context, d *docs.Doc, blocks []docs.ConflictBlock, flagged bool) {
	t := c.Theme()
	r := a.RepoOf(d)
	button := func(label, tip string) bool {
		return wui.Button(c, label).FontSize(11.5).Tooltip(tip).Clicked()
	}
	stagesDiff := func() {
		if flagged && button(i18n.T("两边的 diff"), stagesTip(r)) {
			a.OpenStagesDiff(r, d.Rel)
		}
	}
	wui.Row(c).Padding(5, 10).Gap(6).Wrap().AlignItems(wui.Center).Shrink(0).Background(t.Danger.Alpha(0.12)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		wui.Icon(c, iconAlert).FontSize(iconRow).TextColor(t.Danger)
		if len(blocks) == 0 {
			wui.Text(c, i18n.T("冲突标记都处理完了")).FontSize(11.5).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if button(i18n.T("标记为已解决"), i18n.T("保存并 git add：这个文件的冲突算解决了；冲突都解决了就在 Git 栏的横幅上继续")) {
				a.MarkResolved(d)
			}
			stagesDiff()
			return
		}
		i := a.CurrentConflict(d)
		wui.Text(c, i18n.Tf("第 %d / %d 处冲突", i+1, len(blocks))).FontSize(11.5).FontWeight(600).SingleLine().Shrink(0)
		if button(i18n.T("用我的"), app.Tip(i18n.T("留下我们这边（<<<<<<< 和 ======= 之间）"), "merge.ours")) {
			a.ResolveConflict(d, i, docs.TakeOurs)
		}
		if button(i18n.T("用他们的"), app.Tip(i18n.T("留下他们那边（======= 和 >>>>>>> 之间）"), "merge.theirs")) {
			a.ResolveConflict(d, i, docs.TakeTheirs)
		}
		if button(i18n.T("两个都要"), app.Tip(i18n.T("我的在前、他们的在后；⌥点是他们的在前"), "merge.both")) {
			how := docs.TakeBoth
			if c.Modifiers()&wui.Alt != 0 {
				how = docs.TakeBothReversed
			}
			a.ResolveConflict(d, i, how)
		}
		rowButton(c, iconChevronUp, i18n.T("上一处冲突"), app.Tip(i18n.T("上一处冲突"), "merge.prev"), func() { a.GoToConflict(d, -1) })
		rowButton(c, iconChevron, i18n.T("下一处冲突"), app.Tip(i18n.T("下一处冲突"), "merge.next"), func() { a.GoToConflict(d, 1) })
		wui.Spacer(c).Grow(1)
		if button(i18n.T("全部用我的"), i18n.T("每处冲突都留下我们这边，先问一声")) {
			a.ResolveAll(d, docs.TakeOurs)
		}
		if button(i18n.T("全部用他们的"), i18n.T("每处冲突都留下他们那边，先问一声")) {
			a.ResolveAll(d, docs.TakeTheirs)
		}
		stagesDiff()
	})
}

// stagesTip 是"两边的 diff"的工具提示；变基时 git 的"我们的"是上游那边，说一声。
func stagesTip(r *app.Repo) string {
	tip := i18n.T("只读：索引里我们的（:2）和他们的（:3）两个版本比一比")
	if r != nil && r.Extra.Ongoing == git.Rebasing {
		tip += "\n" + i18n.T("变基时'我们的'是上游那边")
	}
	return tip
}

// conflictMarks 给冲突块染底色（我的绿、他们的蓝、diff3 的基础灰，标记行深一点），<<<<<<< 行的槽里点一个红点。
// LineBackgrounds 记的是设的时候的行号、不跟着编辑挪，所以每帧照现在的块重设；没有块就清掉。
func conflictMarks(c *wui.Context, st *editor.State, blocks []docs.ConflictBlock) {
	if len(blocks) == 0 {
		st.LineBackgrounds, st.GutterMarks = nil, nil
		return
	}
	t := c.Theme()
	ours, theirs, base := t.Success, t.Accent, t.TextMuted
	bg := make([]editor.LineBackground, 0, len(blocks)*7)
	marks := make([]editor.GutterMark, 0, len(blocks))
	fill := func(first, last int, col wui.Color, alpha float32) {
		if first <= last {
			bg = append(bg, editor.LineBackground{First: first, Last: last, Color: col.Alpha(alpha)})
		}
	}
	for _, b := range blocks {
		fill(b.Start, b.Start, ours, 0.22)
		fill(b.Start+1, b.OursEnd()-1, ours, 0.10)
		if b.Base >= 0 {
			fill(b.Base, b.Base, base, 0.22)
			fill(b.Base+1, b.Mid-1, base, 0.08)
		}
		fill(b.Mid, b.Mid, base, 0.22)
		fill(b.Mid+1, b.End-1, theirs, 0.10)
		fill(b.End, b.End, theirs, 0.22)
		marks = append(marks, editor.GutterMark{Line: b.Start, Color: t.Danger})
	}
	st.LineBackgrounds, st.GutterMarks = bg, marks
}

// mergeKeys 是有冲突块时编辑器的 OnInput：⌥F7 / ⌥⇧F7、⌃⌥↓ / ⌃⌥↑ 下一处 / 上一处，⌃⌥1 / 2 / 3 用我的 / 他们的 / 两个都要
// （键从命令注册表里取，和快捷键文档一致）；接了就返回 true，编辑器不再处理。没有块为 nil。
func mergeKeys(a *app.App, d *docs.Doc, blocks []docs.ConflictBlock) func(wui.InputEvent) bool {
	if len(blocks) == 0 {
		return nil
	}
	return func(ev wui.InputEvent) bool {
		if ev.Kind != wui.InputKeyDown {
			return false
		}
		for _, k := range mergeActions {
			if keyIs(ev, app.Find(k.id)) {
				k.run(a, d)
				return true
			}
		}
		return false
	}
}

// keyIs 是按键 ev 是不是命令 cmd 的快捷键（键盘布局打不出那个键时按位置认）。
func keyIs(ev wui.InputEvent, cmd *app.Command) bool {
	return cmd.Key != 0 && ev.Mods == cmd.Mods && (ev.Key == cmd.Key || ev.Key == wui.KeyUnknown && ev.PhysicalKey == cmd.Key)
}

// docKeys 是文件标签编辑器的 OnInput：代码片段跳位会话里的 Tab / ⇧Tab（Esc 结束会话），⇧⌥F 格式化
// （editor.format 的键不在窗口上登记，这里接），再是冲突块的键（mergeKeys）。
// macOS 上 mygo 把键交给编辑器以后照样让输入法打字，⌥ 打出的字（⇧⌥F 是 Ï）跟着送进来：接了 ⇧⌥F 就吞掉紧跟着的
// 那一段字（d.EatText），别的键、点击来了就不再吞（测试的模拟窗口不会自己送这段字）。
func docKeys(a *app.App, d *docs.Doc, blocks []docs.ConflictBlock) func(wui.InputEvent) bool {
	merge := mergeKeys(a, d, blocks)
	return func(ev wui.InputEvent) bool {
		switch ev.Kind {
		case wui.InputText:
			if d.EatText {
				d.EatText = false
				return true
			}
		case wui.InputKeyDown, wui.InputPointerDown:
			d.EatText = false
		}
		if ev.Kind == wui.InputKeyDown && d.Snip != nil && ev.Key == wui.KeyTab {
			switch ev.Mods {
			case 0:
				return a.SnipJump(d, 1)
			case wui.Shift:
				return a.SnipJump(d, -1)
			}
		}
		if ev.Kind == wui.InputKeyDown && ev.Key == wui.KeyEscape && ev.Mods == 0 {
			a.EndSnippet(d) // 不吞：编辑器照样关补全、查找、多光标
		}
		if ev.Kind == wui.InputKeyDown && app.CanFormat(d) && keyIs(ev, app.Find("editor.format")) {
			a.Format(d)
			d.EatText = true
			return true
		}
		return merge != nil && merge(ev)
	}
}

var mergeActions = []struct {
	id  string
	run func(a *app.App, d *docs.Doc)
}{
	{"merge.next", func(a *app.App, d *docs.Doc) { a.GoToConflict(d, 1) }},
	{"merge.prev", func(a *app.App, d *docs.Doc) { a.GoToConflict(d, -1) }},
	{"merge.nextAlt", func(a *app.App, d *docs.Doc) { a.GoToConflict(d, 1) }},
	{"merge.prevAlt", func(a *app.App, d *docs.Doc) { a.GoToConflict(d, -1) }},
	{"merge.ours", func(a *app.App, d *docs.Doc) { a.ResolveCurrent(d, docs.TakeOurs) }},
	{"merge.theirs", func(a *app.App, d *docs.Doc) { a.ResolveCurrent(d, docs.TakeTheirs) }},
	{"merge.both", func(a *app.App, d *docs.Doc) { a.ResolveCurrent(d, docs.TakeBoth) }},
}

// mergeMenu 是编辑器右键菜单里冲突的几项（在"保存"上面）：光标在块里时解决这一块，有块时上一处 / 下一处。
func mergeMenu(a *app.App, m *wui.Menu, d *docs.Doc) {
	blocks := d.ConflictBlocks()
	if len(blocks) == 0 {
		return
	}
	m.Separator()
	if i := a.ConflictAtCursor(d); i >= 0 {
		if shortcut(m.Item(i18n.T("用我的")), "merge.ours").Chosen() {
			a.ResolveConflict(d, i, docs.TakeOurs)
		}
		if shortcut(m.Item(i18n.T("用他们的")), "merge.theirs").Chosen() {
			a.ResolveConflict(d, i, docs.TakeTheirs)
		}
		if shortcut(m.Item(i18n.T("两个都要")), "merge.both").Chosen() {
			a.ResolveConflict(d, i, docs.TakeBoth)
		}
		if m.Item(i18n.T("两个都要（他们的在前）")).Chosen() {
			a.ResolveConflict(d, i, docs.TakeBothReversed)
		}
	}
	if shortcut(m.Item(i18n.T("下一处冲突")), "merge.next").Chosen() {
		a.GoToConflict(d, 1)
	}
	if shortcut(m.Item(i18n.T("上一处冲突")), "merge.prev").Chosen() {
		a.GoToConflict(d, -1)
	}
}
