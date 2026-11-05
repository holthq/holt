package app

import "time"

// 禅模式（view.zen ⌃⌘Z）：三个面板全关，编辑器组在窗口中间（ui/view.go 的 center），标题栏、标签条、状态栏留着。
// 每个窗口各自的，不持久：进的时候记下三个面板的开关，存 state.json 时写的是记下的（SaveState），退出来照原样恢复。
// 禅模式里手动开面板照常能开，不拦、不记。

const (
	ZenWidth  = 1000 // 编辑器组最宽多少 DIP
	ZenMargin = 40   // 窗口宽不到 ZenWidth + 2 × ZenMargin 就不留白
	zenEscGap = time.Second
)

// ToggleZen 进 / 出禅模式。
func (a *App) ToggleZen() {
	if a.Zen {
		a.Zen = false
		a.St.Sidebar, a.St.RightBar, a.St.BottomBar = a.zenWas[0], a.zenWas[1], a.zenWas[2]
	} else {
		a.Zen = true
		a.BottomMax = false
		a.zenWas = [3]bool{a.St.Sidebar, a.St.RightBar, a.St.BottomBar}
		a.St.Sidebar, a.St.RightBar, a.St.BottomBar = false, false, false
		a.FocusDoc = true
	}
	a.zenEsc = time.Time{}
	a.SaveState()
}

// ZenEsc 是禅模式里一下没人接的 Esc（编辑器没东西可取消、没有对话框）：和上一下隔不到 1 s 就退出禅模式，
// 退了返回 true。
func (a *App) ZenEsc(now time.Time) bool {
	if !a.Zen {
		return false
	}
	if !a.zenEsc.IsZero() && now.Sub(a.zenEsc) <= zenEscGap {
		a.ToggleZen()
		return true
	}
	a.zenEsc = now
	return false
}
