// Holt（以前叫 Grove，更早叫 Workbench）是一个"多仓库工作台"：左边侧栏（文件 / 搜索 / Git），中间标签页里的
// 编辑器和 Diff，右边提交历史，下面每个仓库一个终端和操作输出。读仓库状态都是只读的
// git 命令；会改仓库的操作（暂存、提交、切分支、拉取推送……）和保存文件只在用户明确
// 点了按钮、菜单或按了快捷键后才做。
//
// 一个窗口一个工作区，可以同时开几个窗口（app.Windows）。启动时开回上次退出时开着的窗口（一个画完第一帧再开下一个），没有就开最近的工作区
// （第一次用、或最近的都打不开了是空工作区：侧栏两个按钮打开文件夹 / 工作区文件）；环境变量 WORKBENCH_WORKSPACE
// 或第一个命令行参数（.code-workspace）再开一个（已经开着就叫到前面）。开着时用 ⌘O 或菜单换，⌥⌘N 新窗口。
//
// 只开一个实例：`Holt 目录|文件` 再启动时把路径交给开着的那个（mygo 的单实例锁），
// `open -a Holt 路径` 和拖到 Dock 图标上的经 OnOpenFile 来（分不出是哪种，一起走 app.Windows.Route）：
// .code-workspace 开新窗口（或叫到前面）；目录开着的叫到前面、前台是空工作区就在它里面开、不然开新窗口；
// 文件交给前台窗口的 App.OpenArgs。拖进窗口里的（OnFileDrop）照旧走 OpenArgs，目录问要不要加进当前工作区。
// 崩溃（任何 goroutine 里没接住的 panic）和后台出的错写进 ~/Library/Logs/Holt/（internal/crash）。
//
// `Holt termd <套接字> [日志]` 是终端后台进程（internal/termd）：设置里开了"关掉窗口后终端继续在后台运行"时
// 应用带这个参数再起一个自己，main 第一行就分出去，不开窗口、不抢单实例锁。
//
// `Holt askpass <提示>` 是 git / ssh 要凭据时调的 helper（internal/askpass）：同样第一行就分出去，
// 把提示经 unix 套接字交给开着的应用弹框，回答写到 stdout。
package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	wui "github.com/egoist/mygo/ui"

	"github.com/holthq/holt/internal/app"
	"github.com/holthq/holt/internal/askpass"
	"github.com/holthq/holt/internal/crash"
	"github.com/holthq/holt/internal/fonts"
	"github.com/holthq/holt/internal/i18n"
	"github.com/holthq/holt/internal/render"
	"github.com/holthq/holt/internal/termd"
	"github.com/holthq/holt/internal/ui"
	"github.com/holthq/holt/internal/version"
	"github.com/holthq/holt/internal/workspace"
)

func main() {
	app.Launched = time.Now() // 启动计时（日志里的“就绪”行）
	if len(os.Args) > 1 && os.Args[1] == "termd" {
		os.Exit(termd.Main(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "askpass" {
		os.Exit(askpass.Main(os.Args[2:]))
	}
	log.SetFlags(log.Ldate | log.Ltime)
	if v := mygo.App.Version(); v != "" { // mygo build 从 mygo.json 链接进来的；go run 时没有，是 "dev"
		version.App = v
	}
	args := os.Args[1:]
	wsPath := "" // 空：最近的工作区（app.New）
	if p := os.Getenv("WORKBENCH_WORKSPACE"); p != "" {
		wsPath = p
	}
	if len(args) > 0 && filepath.Ext(args[0]) == ".code-workspace" {
		wsPath, args = args[0], args[1:]
	}

	// 单实例：已经开着一个就把参数交给它，自己退出。用另一份 state.json（WORKBENCH_STATE，量内存、手测）时不排队，
	// 好和平时开着的那个同时跑；WORKBENCH_SINGLE=1 时照样排队（测单实例本身）。
	if os.Getenv("WORKBENCH_STATE") == "" || os.Getenv("WORKBENCH_SINGLE") != "" {
		if !mygo.App.RequestSingleInstanceLock() {
			return
		}
	}

	// 改名 Holt：旧的状态目录、日志目录搬过来（migrate.go）。要在下面第一次取 mygo 的路径之前——取了就建出空的新目录。
	moved := migrateDirs(mygo.App.Name())

	// 日志：WORKBENCH_LOGS=<目录> 换个地方（测崩溃日志时不动平时的）；WORKBENCH_PANIC=1 启动 3 秒后在后台 goroutine 里 panic。
	if d := os.Getenv("WORKBENCH_LOGS"); d != "" {
		mygo.App.SetPath(mygo.PathLogs, d)
	}
	logDir, err := mygo.App.Path(mygo.PathLogs)
	var lastCrash string
	if err == nil {
		var w io.Writer
		if lastCrash, w, err = crash.Setup(logDir, version.String()); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, w))
		}
	}
	if err != nil {
		log.Printf("日志目录 %s: %v", logDir, err)
		logDir = ""
	}
	for _, l := range moved {
		log.Print(l)
	}
	if logDir != "" {
		app.TermdLog = filepath.Join(logDir, "termd.log")
	}

	// state.json 和窗口位置（window-state.json）的目录：平时是 PathUserData；WORKBENCH_STATE 换一个（量内存、手测时
	// 不动平时的，窗口位置也不动）。窗口位置存在哪要在第一个窗口之前告诉 mygo，所以在 Run 之前定。
	stateDir := os.Getenv("WORKBENCH_STATE")
	if stateDir == "" {
		stateDir, _ = mygo.App.Path(mygo.PathUserData)
	}
	if stateDir != "" {
		mygo.App.SetWindowStateStore(app.WindowStates(stateDir))
	}
	// Markdown 预览画 Mermaid 图和公式的渲染页（internal/render）从 wbrender:// 来：协议要在建窗口之前注册。
	if err := render.Register(); err != nil {
		log.Printf("渲染页协议: %v", err)
	}
	// 调试：WORKBENCH_RESTORE_GAP=600ms 改还原时两个窗口之间隔多久（默认 2.5s；量启动峰值、手测还原中 ⌘Q 用）。
	if d, err := time.ParseDuration(os.Getenv("WORKBENCH_RESTORE_GAP")); err == nil {
		app.RestoreGap = d
	}

	// 外面交进来的路径（命令行、第二个实例、open -a、拖到 Dock）：窗口好之前、还原的窗口没开完时来的先攒着。
	// AppKit 可能把启动参数也当文件再交一遍给 OnOpenFile，2 秒内同一个路径只开一次。
	var wins *app.Windows
	var pending []string
	seen := map[string]time.Time{}
	if wsPath != "" {
		if abs, err := filepath.Abs(wsPath); err == nil {
			seen[abs] = time.Now() // 启动时自己开的工作区文件，AppKit 再交一遍时别再说"已经开着"
		}
	}
	deliver := func(paths []string, wd string) {
		var fresh []string
		for _, p := range paths {
			if !filepath.IsAbs(p) {
				p = filepath.Join(wd, p)
			}
			p = filepath.Clean(p)
			if time.Since(seen[p]) < 2*time.Second {
				continue
			}
			seen[p] = time.Now()
			log.Printf("打开 %s", p)
			fresh = append(fresh, p)
		}
		if wins == nil || len(wins.Apps) == 0 || wins.Restoring() {
			pending = append(pending, fresh...)
			return
		}
		wins.Route(fresh)
	}
	mygo.App.OnOpenFile(func(path string) { deliver([]string{path}, "") })
	mygo.App.OnSecondInstance(func(args []string, wd string) { deliver(app.Args(args), wd) })

	mygo.App.SetLanguages(i18n.Locale()) // 界面语言不跟系统走，由设置里的"语言"定（读完设置后 OnSettings 再设一次）
	mygo.App.WhenReady(func() {
		fonts.Load()
		wins = app.NewWindows(stateDir)
		plain := os.Getenv("WORKBENCH_PLAIN") != ""

		// 设置、关于窗口只有一个，跟着打开它的那个工作台窗口（关掉那个窗口时一起关，再按 ⌘, 由别的窗口重开）。
		var settingsWin, aboutWin *mygo.Window
		var settingsApp, aboutApp *app.App
		// 外观和语言：启动时和设置窗口改了之后套上，每个窗口的材质都换。
		onSettings := func(s *workspace.Settings) {
			src := mygo.ThemeSystem
			switch s.Theme {
			case "light":
				src = mygo.ThemeLight
			case "dark":
				src = mygo.ThemeDark
			}
			mygo.Theme.SetSource(src)
			for _, o := range wins.Apps {
				if o.Win != nil && !o.Win.IsDestroyed() && !plain {
					o.Win.SetVibrancy(vibrancy(s))
				}
			}
			mygo.App.SetLanguages(i18n.Locale())
			if settingsWin != nil && !settingsWin.IsDestroyed() {
				settingsWin.SetTitle(i18n.T("设置"))
				settingsWin.Invalidate() // 缩放、别处改的值（⌘=、设置文档、导入）跟着重画
			}
			if aboutWin != nil && !aboutWin.IsDestroyed() {
				aboutWin.Invalidate()
			}
		}
		openSettings := func(a *app.App) {
			if settingsWin != nil && !settingsWin.IsDestroyed() {
				settingsWin.Show()
				settingsWin.Focus()
				return
			}
			settingsApp = a
			settingsWin = mygo.NewWindow(mygo.WindowOptions{
				Title:           i18n.T("设置"),
				Width:           760,
				Height:          540,
				MinWidth:        700,
				MinHeight:       420,
				StateKey:        "settings",
				DisableMaximize: true,
				Content:         wui.View(ui.SettingsView(a)),
			})
			a.SettingsWin = settingsWin
		}
		openAbout := func(a *app.App) {
			if aboutWin != nil && !aboutWin.IsDestroyed() {
				aboutWin.Show()
				aboutWin.Focus()
				return
			}
			licenses := ""
			if res, err := mygo.App.Path(mygo.PathResources); err == nil {
				if p := filepath.Join(res, "licenses"); isDir(p) {
					licenses = p
				}
			}
			aboutApp = a
			aboutWin = mygo.NewWindow(mygo.WindowOptions{
				Title:           i18n.T("关于 Holt"),
				Width:           460,
				Height:          640,
				MinWidth:        400,
				MinHeight:       420,
				StateKey:        "about",
				DisableMaximize: true,
				Content:         wui.View(ui.AboutView(a, licenses)),
			})
		}
		closeAux := func(a *app.App) { // a 为 nil：都关
			if settingsWin != nil && (a == nil || settingsApp == a) {
				settingsWin.Destroy()
				settingsApp.SettingsWin = nil
				settingsWin, settingsApp = nil, nil
			}
			if aboutWin != nil && (a == nil || aboutApp == a) {
				aboutWin.Destroy()
				aboutWin, aboutApp = nil, nil
			}
		}

		// 每个新的 App：跟着进程走的回调和日志目录。
		wins.Setup = func(a *app.App) {
			a.HasMenu = true
			a.LogDir, a.LastCrash = logDir, lastCrash
			if os.Getenv("WORKBENCH_STATE") == "" { // 用另一份 state.json 时（量内存、手测）不动系统的"最近使用"
				a.OnRecent = func(p string) { // 只记工作区文件：从程序坞的"最近使用"打开的目录会当成"加仓库"（OpenArg）
					if filepath.Ext(p) == ".code-workspace" {
						mygo.App.AddRecentDocument(p)
					}
				}
				a.OnClearRecent = func() { mygo.App.ClearRecentDocuments() }
			}
			a.OnSettings = onSettings
			a.OpenSettings = func() { openSettings(a) }
			a.OpenAbout = func() { openAbout(a) }
		}
		// 每个工作台窗口：位置按槽位记（StateKey）；新开的（不是启动时开回来的）错开 from 24 pt、一样大。
		wins.NewWindow = func(a, from *app.App) {
			opts := mygo.WindowOptions{
				Title:     a.Title(),
				Width:     1440,
				Height:    880,
				MinWidth:  960,
				MinHeight: 600,
				StateKey:  a.StateKey(),
				Hidden:    from != nil, // 挪好位置再显示
				// 紧凑的 38 pt 标题栏：红绿灯挪到和标题栏的图标一条中线上（不用 HiddenInset 那 66 pt 高的）
				TitleBarStyle:        mygo.TitleBarHidden,
				TrafficLightPosition: &ui.TrafficLights,
				Vibrancy:             vibrancy(&a.St.Settings),
				Content:              wui.View(ui.View(a)),
			}
			// 调试：WORKBENCH_PLAIN=1 用普通标题栏、不要毛玻璃（窗口底选了通透也不透），用来对比内存。
			if plain {
				opts.TitleBarStyle, opts.Vibrancy = mygo.TitleBarDefault, mygo.VibrancyNone
			}
			win := mygo.NewWindow(opts)
			a.Win, a.Update = win, win.Update
			win.OnReadyToShow(func() { wins.Ready(a) }) // 画完第一帧：还原时隔一会儿开下一个窗口（只来一次）
			if from != nil {
				if from.Win != nil && !from.Win.IsDestroyed() {
					win.SetBounds(cascade(from.Win.Bounds()))
				}
				win.Show()
			}
			// 有没保存的文件、有 git 写命令在跑时先问再关窗口（app.RequestLeave）。
			win.OnClose(func(e *mygo.CloseEvent) {
				if !wins.CanClose(a) {
					e.PreventDefault()
				}
			})
			win.OnClosed(func() {
				closeAux(a)
				wins.Closed(a)
			})
			// 到前台：菜单栏换成它的；文件监听停了的仓库顺手刷新一次（兜底；在监听的仓库不用）。
			win.OnFocus(func() {
				wins.Focus(a)
				a.Update(a.RefreshOnFocus)
			})
			// 切走时存一下标签和展开状态，崩了也不至于全丢；自动保存是"切走时"的话保存改过的文件。
			win.OnBlur(func() { a.Update(a.OnBlur) })
			// 从访达拖进窗口、没落在文件树目录行上的（那儿是复制进去，见 ui.treeDropTarget）：照命令行参数的规矩开，
			// 文件开标签，目录跳到它的仓库或问要不要加进来。
			win.OnFileDrop(func(e *mygo.FileDropEvent) {
				paths := e.Paths
				a.Update(func() { a.OpenArgs(paths, "") })
			})
		}
		wins.Quit = func() {
			closeAux(nil)
			app.AskpassClose()
			mygo.App.Quit()
		}
		// 还原时开下一个窗口的计时器：到点回主线程（计时器上别读 App 的字段）。
		wins.Later = func(d time.Duration, fn func()) {
			time.AfterFunc(d, func() { mygo.RunOnMain(fn) })
		}
		// 还原的窗口都开完了：这期间外面交进来的（启动前 OnOpenFile 来的、命令行参数、第二个实例）一次派出去。
		wins.Restored = func() {
			if len(pending) > 0 {
				paths := pending
				pending = nil
				wins.Route(paths)
			}
		}
		// ⌘Q、程序坞的退出：有没保存的文件、在跑的 git 写命令的窗口一个个问完再退（app.Windows.RequestQuit）。
		// 不注册 OnWindowAllClosed：关最后一个工作台窗口时 Windows.Closed 自己退出。
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if !wins.RequestQuit() {
				e.PreventDefault()
			}
		})

		// 外观和语言在建窗口之前套上（不闪一下默认外观）。
		s := wins.Settings()
		i18n.Set(s.Lang)
		onSettings(&s)
		if err := wins.Restore(wsPath); err != nil {
			log.Fatal(err)
		}
		a := wins.Apps[0] // 调试开关和启动提示只管第一个窗口
		win := a.Win
		a.Update(func() { a.Greet(version.App) }) // 状态栏：换了版本报版本号，上次崩了说一声
		// 调试（量内存）：WORKBENCH_SECOND=<工作区> 启动时再开一个窗口；WORKBENCH_SECOND_CLOSE=<时长> 过这么久把它关掉
		// （看关掉的窗口占的内存还不还回来）。
		if p := os.Getenv("WORKBENCH_SECOND"); p != "" {
			if b := wins.Open(p, nil); b != nil && b != a {
				if d, err := time.ParseDuration(os.Getenv("WORKBENCH_SECOND_CLOSE")); err == nil {
					bw := b.Win
					time.AfterFunc(d, func() { bw.Update(bw.Close) })
				}
			}
		}
		if len(args) > 0 { // 命令行参数（-psn_… 之类的开关不算）
			wd, _ := os.Getwd()
			deliver(app.Args(args), wd)
		}
		if os.Getenv("WORKBENCH_PANIC") != "" {
			go func() {
				time.Sleep(3 * time.Second)
				panic("WORKBENCH_PANIC：测试崩溃日志")
			}()
		}

		// 调试（量内存）：WORKBENCH_FOCUS=editor / terminal 启动时把键盘给当前标签的编辑器或终端，
		// 没有辅助功能权限也能量"光标在编辑器 / 终端里、窗口在前面"的内存；WORKBENCH_CARET_REST=off
		// 让编辑器光标一直闪（最早的样子），用来前后对比。
		switch os.Getenv("WORKBENCH_FOCUS") {
		case "editor":
			a.Update(func() { a.FocusDoc = true })
		case "terminal":
			a.Update(func() { a.FocusTerm = true })
		}
		if os.Getenv("WORKBENCH_CARET_REST") == "off" {
			ui.CaretRest = 0
		}
		// 调试（量内存）：WORKBENCH_TERMS=all 启动时就开所有仓库所有终端标签的 shell（不等第一次显示），
		// 用来量"几个仓库各开几个终端"的空闲内存；最早就是启动时每个仓库开一个。
		if os.Getenv("WORKBENCH_TERMS") == "all" {
			a.Update(a.StartAllTerms)
		}
		// 调试：WORKBENCH_TERM_CMD=命令 在第一个仓库的第一个终端里等出了提示符敲这一行再回车
		// （量终端后台进程跑着命令时的内存、手测关窗口后命令还在跑）。
		if line := os.Getenv("WORKBENCH_TERM_CMD"); line != "" {
			a.Update(func() { a.TypeInTerminal(line) })
		}
		// 调试（量内存）：WORKBENCH_ALLOPEN=搜索词 是量内存用的"全开"（app.AllOpen）：仓库都读完后再开两个 Diff 标签，
		// 侧栏搜这个词，当前仓库的终端标签都起 shell；搜完日志里一行"全开：…（用时 … ms…）"。
		if q := os.Getenv("WORKBENCH_ALLOPEN"); q != "" {
			a.Update(func() { a.AllOpen(q) })
		}

		// 调试：WORKBENCH_CAPTURE=文件.png 时，启动 5 秒（或 WORKBENCH_CAPTURE_AFTER，如 2s）后把窗口内容
		// 存成 PNG，没有录屏权限也能看到真实窗口的样子。状态栏提示只亮 4 秒，要看它得早点截。
		if out := os.Getenv("WORKBENCH_CAPTURE"); out != "" {
			after, err := time.ParseDuration(os.Getenv("WORKBENCH_CAPTURE_AFTER"))
			if err != nil {
				after = 5 * time.Second
			}
			go func() {
				time.Sleep(after)
				png, err := win.CapturePage()
				if err == nil {
					err = os.WriteFile(out, png, 0o644)
				}
				log.Printf("capture %s: %v", out, err)
			}()
		}
	})
	err = mygo.App.Run()
	app.AskpassClose()
	if err != nil {
		log.Fatal(err)
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// cascade 是新窗口的位置：错开 r（前台窗口）右下 24 pt、一样大；出了 r 所在屏幕的可用区就回到它的左上角。
func cascade(r mygo.Rectangle) mygo.Rectangle {
	area := mygo.Screen.DisplayMatching(r).WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		return mygo.Rectangle{X: r.X + 24, Y: r.Y + 24, Width: r.Width, Height: r.Height}
	}
	n := mygo.Rectangle{X: r.X + 24, Y: r.Y + 24, Width: min(r.Width, area.Width), Height: min(r.Height, area.Height)}
	if n.X+n.Width > area.X+area.Width {
		n.X = area.X
	}
	if n.Y+n.Height > area.Y+area.Height {
		n.Y = area.Y
	}
	return n
}

// vibrancy 是主窗口的材质：设置里"窗口底"选了通透才透出桌面，用设置里"材质"选的那种（默认侧栏）；
// 柔和（默认）、纯色没有材质，界面自己画底。调试：WORKBENCH_MATERIAL=hud 之类压过设置里的材质（不是通透时照样不透）。
func vibrancy(s *workspace.Settings) mygo.Vibrancy {
	if s.Backdrop != "vibrancy" {
		return mygo.VibrancyNone
	}
	if m := os.Getenv("WORKBENCH_MATERIAL"); m != "" {
		return mygo.Vibrancy(m)
	}
	if s.Material != "" {
		return mygo.Vibrancy(s.Material)
	}
	return mygo.VibrancySidebar
}
