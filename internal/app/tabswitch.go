package app

import (
	"slices"

	"github.com/holthq/holt/internal/docs"
)

// 这个文件是在当前编辑器组的标签之间切：⇧⌘[ / ⇧⌘]、⌥⌘← / ⌥⌘→ 按标签条的顺序走（到头绕回），
// ⌃Tab / ⌃⇧Tab 按最近使用的顺序走。键盘在终端里时 ⇧⌘[ / ⇧⌘] / ⌥⌘← / ⌥⌘→ 归终端标签（注册表里终端的排在前面）。

// maxTabMRU 是最近使用的标签记几个。
const maxTabMRU = 100

// noteTabUse 把切到的标签记到最近使用的最前面（visit 里调）。
func (a *App) noteTabUse(k docs.Key) {
	if len(a.tabMRU) > 0 && a.tabMRU[0] == k {
		return
	}
	a.tabMRU = slices.Insert(slices.DeleteFunc(a.tabMRU, func(o docs.Key) bool { return o == k }), 0, k)
	if len(a.tabMRU) > maxTabMRU {
		a.tabMRU = a.tabMRU[:maxTabMRU]
	}
}

// StepTab 切到当前组的下一个（d = 1）/ 上一个（d = -1）标签，到头了绕回来。
func (a *App) StepTab(d int) {
	n := len(a.Docs)
	if n < 2 {
		return
	}
	a.SelectDoc(((max(a.DocSel, 0)+d)%n + n) % n)
}

// TabsByUse 是当前组的标签下标按最近使用排的顺序：当前标签在最前，记过的按记的先后，没记过的按标签条的顺序排在后面。
func (a *App) TabsByUse() []int {
	out := make([]int, 0, len(a.Docs))
	if a.DocSel >= 0 && a.DocSel < len(a.Docs) {
		out = append(out, a.DocSel)
	}
	for _, k := range a.tabMRU {
		if i := slices.IndexFunc(a.Docs, func(d *docs.Doc) bool { return d.Key() == k }); i >= 0 && !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	for i := range a.Docs {
		if !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	return out
}

// StepTabByUse 按最近使用切标签：d = 1 切到上一个用过的（连按两次在两个标签之间来回），
// d = -1 切到最久没用的（倒着走）。
func (a *App) StepTabByUse(d int) {
	order := a.TabsByUse()
	if len(order) < 2 {
		return
	}
	if d > 0 {
		a.SelectDoc(order[1])
	} else {
		a.SelectDoc(order[len(order)-1])
	}
}

// hasTabs 是当前组不止一个标签（切得动）。
func hasTabs(a *App) bool { return len(a.Docs) > 1 }
