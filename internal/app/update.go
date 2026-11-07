package app

// CheckUpdates 是应用菜单和关于窗口的"检查更新…"：交给 mygo 的更新窗口（后台检查、下载、安装、
// 重启都在那里）。没编进更新（开发版、go run）时窗口会说为什么不能更新；main 里没接（测试）就什么也不做。
func (a *App) CheckUpdates() {
	if a.OpenUpdates != nil {
		a.OpenUpdates()
	}
}
