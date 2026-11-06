package ui

import (
	"time"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
)

// 光标停手不闪。mygo 的代码编辑器（ui/editor）只要有键盘，就每 500 ms 排一帧让光标闪，没有开关；
// mygo 又要 2 秒没有新帧才放掉跟窗口一样大的那几块帧内存。所以光标在编辑器里、窗口在前面时，
// 内存一直多出一两百 MB，CPU 也一直在走。这里和 mygo 的输入框一样，停手 CaretRest 后光标不再闪。
//
// 编辑器在建它的那一刻问自己有没有键盘（Element.Focused），闪不闪只看这个。所以歇着的时候，每次建它
// 之前先把键盘交给旁边一个看不见的元素，建完马上还给它：编辑器以为自己没有键盘，就不排闪烁的帧，而这
// 一帧结束时键盘还在它手里，按键、输入法照常进它。来了按键、点击，内容或选区变了，键盘离开过，就醒过来。

// CaretRest 是停手多久之后光标不再闪，和 mygo 的输入框一样；0 为一直闪（只在量内存时和一直闪的样子对比用，
// 见 cmd/workbench 的 WORKBENCH_CARET_REST）。
var CaretRest = 30 * time.Second

// restingEditor 建代码编辑器（editor.View），停手 CaretRest 后光标不再闪，换成空心方块
// （编辑器没有键盘时画的样子）标出位置。keys 非 nil 时编辑器先把输入给它（⇧⌥F、冲突的快捷键），它接了编辑器就不处理。
func restingEditor(c *wui.Context, key any, st *editor.State, r *docs.Rest, keys func(wui.InputEvent) bool) wui.Element {
	sink := wui.Box(c).Absolute().Width(0).Height(0)
	// 键盘落在 sink 上只会是歇着时 ⇧Tab 从只读的编辑器退出来，当它还在编辑器里
	focused := c.Focused(r.H) || sink.Focused()
	ver, sel, n := st.Buffer.Version(), st.Selection(), len(st.Selections())
	moved := ver != r.Ver || sel != r.Sel
	rest(c, r, focused, moved || n != r.NSel)
	r.Ver, r.Sel, r.NSel = ver, sel, n
	st.OnInput = func(ev wui.InputEvent) bool {
		switch ev.Kind {
		case wui.InputKeyDown, wui.InputText, wui.InputCommand, wui.InputPointerDown:
			if r.Resting {
				r.Woke = true
			}
			r.Keyed = ev.Kind != wui.InputPointerDown
		}
		return keys != nil && keys(ev)
	}
	st.CursorShape = editor.CursorBar
	if r.Resting {
		st.CursorShape = editor.CursorBlock
		sink.Focus()
	}
	ed := editor.View(c.Key(key), st).Bind(&r.H)
	if r.Resting {
		ed.Focus()
	}
	// 编辑器建的时候已经按自己的余量（3 行）把光标滚进来了，这里键盘挪的光标再补到 caretMargin。
	// 按键在两帧之间到，挪了光标紧接着的这一帧就看得到；Keyed 每帧用完就清，免得没挪光标的按键（Esc、⌘S）
	// 留下来，之后滚轮滚走、内容从外面变了（重新载入、另一个分屏改的）时把视图拽回光标。
	keyed := r.Keyed
	r.Keyed = false
	if moved && keyed {
		if caretMargin(st, float64(ed.Bounds().H)) {
			c.Invalidate()
		}
	}
	return ed
}

// caretMargin 让光标上下各留 min(5 行, 视图高的 1/5) 的余量，像 VS Code 的 cursorSurroundingLines、Vim 的 scrolloff。
// mygo 的编辑器挪了光标只滚到留 3 行、没有选项改它，所以键盘挪了光标、它滚完以后，在这里再看一遍：
// 离下边不够就往下滚（文件末尾靠 scroll past end，最后一行上来后下面是空白），离上边不够就往上滚。
// 只在键盘挪光标后调（restingEditor 里按 Rest.Keyed），点击、滚轮、拖滚动条不走这里。
// 光标那一行软换行折成了几行时不管（看不出光标在第几折，交给 mygo）。返回滚了没有。
func caretMargin(st *editor.State, viewH float64) bool {
	if viewH <= 0 || st.FitContent || st.Buffer == nil {
		return false
	}
	n := st.Buffer.LineCount()
	line := st.CursorPos().Line
	if line < 0 || line >= n {
		return false
	}
	// 一行多高：看得见的行里最矮的（软换行折了的行更高、折叠藏起来的行高 0）
	lh := 0.0
	first, last := st.VisibleLines()
	for i := max(first, 0); i < min(last+1, first+64, n); i++ {
		if h := st.LineTop(i+1) - st.LineTop(i); h > 0.5 && (lh == 0 || h < lh) {
			lh = h
		}
	}
	top, bottom := st.LineTop(line), st.LineTop(line+1)
	if lh == 0 || bottom-top > lh*1.5 {
		return false
	}
	margin := min(5*lh, viewH/5, max(0, (viewH-lh)/2))
	y := st.ScrollY()
	switch {
	case top-margin < y:
		st.SetScrollY(top - margin)
	case bottom+margin > y+viewH:
		st.SetScrollY(bottom + margin - viewH)
	}
	return st.ScrollY() != y
}

// restingDiff 建 Diff 视图（editor.DiffView），停手 CaretRest 后光标不再闪（歇着时不画光标，选区还在）。
// 两侧的编辑器在 DiffView 里面建，拿不到它们，键盘交还不了；借"跳到行"栏：开了马上关，mygo 关它时
// 让键盘回到那一侧的编辑器，而且是在那个编辑器问过自己有没有键盘之后。
func restingDiff(c *wui.Context, key any, d *editor.DiffState, r *docs.Rest) wui.Element {
	sink := wui.Box(c).Absolute().Width(0).Height(0)
	focused := c.FocusWithin(r.H) || sink.Focused()
	sel, side := d.Cursor(), d.ActiveSide()
	rest(c, r, focused, sel != r.Sel.Head || side != r.Side)
	r.Sel, r.Side = editor.Selection{Anchor: sel, Head: sel}, side
	if r.Resting {
		sink.Focus()
		d.Run(editor.GoToLine)
		d.Run(editor.Cancel)
	}
	return editor.DiffView(c.Key(key), d).Bind(&r.H)
}

// rest 走一帧：键盘不在它那里、刚拿到、来了输入或 changed 就醒着重新计时；醒着够 CaretRest 了就歇着，
// 不够就排一帧到点再看。
func rest(c *wui.Context, r *docs.Rest, focused, changed bool) {
	now := c.Now()
	if !focused || !r.Focused || r.Woke || changed {
		r.At, r.Resting, r.Woke = now, false, false
	}
	r.Focused = focused
	if focused && !r.Resting && CaretRest > 0 {
		if wait := r.At.Add(CaretRest).Sub(now); wait > 0 {
			c.After(wait)
		} else {
			r.Resting = true
		}
	}
}
