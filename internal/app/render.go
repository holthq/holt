package app

import "github.com/lemoeade/grove/internal/render"

// Renderer 是这个窗口画 Markdown 预览里 Mermaid 图和公式的渲染器（internal/render）：第一次要时建，
// WebView 等真有东西要画才建，60 秒没活自己销毁。设置里关了时关掉、返回 nil；没有窗口、测试里也没给 RenderOpen 时也是 nil，
// 预览照旧显示代码块和 TeX 原文。
func (a *App) Renderer() *render.Renderer {
	if a.St.Settings.NoDiagrams {
		if a.Rend != nil {
			a.Rend.Close()
			a.Rend = nil
		}
		return nil
	}
	if a.Rend == nil {
		open := a.RenderOpen
		if open == nil && a.Win != nil {
			open = render.WebView(a.Win)
		}
		if open == nil {
			return nil
		}
		up := a.Update // 后台 goroutine 别读 a.Update（switchTo 会整个重写 *a）
		a.Rend = render.New(open, func() { up(func() {}) })
	}
	return a.Rend
}
