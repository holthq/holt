package app

// 最大化底栏（view.maximizeBottom ⇧⌘J）：底栏占掉编辑区那块地方（ui/view.go 的 center），再按一下回到原来的高度 / 宽度。
// 每个窗口各自的，只在内存里，不存 state.json；最大化时 St.BottomH / St.TermW 不动。
// 底栏关掉、切到或打开编辑器标签（SelectDoc / FocusGroup / MoveDocTo）、进禅模式时自动还原。

// BottomMaxed 是现在底栏开着并且最大化了（布局、菜单的勾、标题行按钮的图标都看它）。
func (a *App) BottomMaxed() bool { return a.BottomMax && a.St.BottomBar }

// ToggleBottomMax 最大化 / 还原底栏；底栏没开时打开并直接最大化。
func (a *App) ToggleBottomMax() {
	if a.BottomMaxed() {
		a.BottomMax = false
		return
	}
	if !a.St.BottomBar {
		a.St.BottomBar = true
		a.SaveState()
	}
	a.BottomMax = true
}

// showEditor 是要让编辑器区看得见（打开、切到编辑器标签）：底栏最大化着就还原。
func (a *App) showEditor() { a.BottomMax = false }
