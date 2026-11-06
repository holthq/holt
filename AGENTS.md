# 贡献指南（给人，也给编码 agent）

Holt 是用纯 Go 在 [mygo](https://github.com/egoist/mygo) 上写的 macOS 原生多仓库工作台：文件树 + 编辑器 + 按仓库的
Git 审阅 + 终端。工作区可以是一个 `.code-workspace` 文件，也可以是一个普通目录（里面的目录不一定是 Git 仓库）；
第一次启动是空工作区。

代码注释、界面文案、文档以中文为主；英文界面走 `internal/i18n` 的译文表。国外贡献者从
[CONTRIBUTING.md](CONTRIBUTING.md) 开始（英文摘要），PR 用中文、英文都收。

文档：

- [README.md](README.md) / [README.en.md](README.en.md)：给用户的说明
- [CONTRIBUTING.md](CONTRIBUTING.md)：贡献说明（英文为主，中文在末尾）；国外贡献者看这份
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) / [SECURITY.md](SECURITY.md)：行为准则、安全问题怎么私下报
- [docs/architecture.md](docs/architecture.md)：架构，现在实际是什么样
- [docs/testing.md](docs/testing.md)：测试怎么分工、怎么量
- [docs/release.md](docs/release.md)：版本号、构建、DMG、签名和公证、CI
- [docs/shortcuts.md](docs/shortcuts.md)：快捷键表（从命令注册表生成，别手改）
- [docs/mygo-pitfalls.md](docs/mygo-pitfalls.md)：踩过的坑（mygo、git、macOS），动界面、列表、键盘、终端之前先翻一下对应的小节
- [CHANGELOG.md](CHANGELOG.md)：每个版本改了什么

## 必须遵守

0. **所有仓库同时可见**（第一原则）：文件树和 Git 栏永远列出工作区里所有仓库，一个仓库一段 / 一个根节点；
   不许做"当前仓库 + 切换器"的视图。"当前仓库"只决定终端、历史右栏（"当前仓库"那一页）和标题栏的分支胶囊；
   点哪个仓库的东西、⌘1–9 跳到哪一段，当前仓库就跟到哪个。新功能只在一个仓库上测过不算数（用 `testutil.ThreeRepos`）。
1. **应用只在用户明确操作时写**：提交、暂存、丢弃、保存、推送、增删工作树这类写操作，只在用户点了按钮 / 菜单 /
   快捷键之后才跑；丢数据、改远程、改历史的先弹确认（`a.AskDanger`，名单由 `app/danger_test.go` 按源码数）。
   启动、刷新、开标签、监听都不跑写命令。
2. **测试绝不写真实仓库**：
   - 写操作的测试一律在 `t.TempDir()` 里 `git init` 的临时仓库上做：`internal/testutil` 的 `TempRepo` / `NewRepo` /
     `ThreeRepos` / `SubmoduleRepo`。
   - 读真实工作区的测试（`TestStatusMatchesPorcelain`、`TestWorkbenchView`、`TestScreenshot`、`BenchmarkWorkspace` 等）
     只读（status / diff / log / 截图），只在设了 `WORKBENCH_TEST_WS=<.code-workspace 或目录>` 时跑，没设就跳过。
   - 开发时对自己的真实仓库也只跑读命令。工作树只跑 `worktree list` / `status` / `rev-list` / `log`，不新建、不移除、
     不锁、不 `prune`；要量带很多工作树的仓库，`git clone file://…` 一份到临时目录，在副本上 `worktree add`。
   - 测试也不碰开发机上别的东西：剪贴板（`app.CopyText` 换成桩）、浏览器和访达（`a.OpenURL` / `a.ShowPath` 换成只记不开的）、
     `~/Library/Application Support/Holt/`、`~/Library/Logs/Holt/`、正在跑的 Holt 实例和它的 termd（见下面"单实例"）。
3. **mygo 锁在 go.mod 的版本**。升级前跑全套测试（含 `-race`）；`TestAccessLabels` 用 reflect 读 Tester 没导出的字段，
   字段变了 `TestAccessProbe` 会先报。`TestVersions` 核对编进来的 mygo 版本和 go.mod 一致。CI 里不要 `go get -u`。
4. **看界面靠截图**：编码 agent 一般没有屏幕录制 / 辅助功能权限，看界面靠无头 Tester 截图（`WORKBENCH_SHOT`）和
   `WORKBENCH_CAPTURE`。系统菜单、原生对话框、毛玻璃、输入法、原生拖放这类截不到的，在 PR 里写明要人手测。
   真窗口会抢前台和键盘：别人正用着的机器上优先无头测试，真要起窗口用 `open -g -n --env …` 在后台起。
5. **质量底线**：
   - `gofmt` 干净；`go vet` 干净；`go test ./...` 和 `go test -race ./...` 全过（界面测试也跑 `-race`）。
   - 每个图标按钮有 `Label` / `Tooltip`。`internal/ui` 的 `TestAccessLabels` 自动查；新加的区域、浮层、窗口要在它里面加一步画出来，不然查不到。
   - 快捷键无冲突（`TestShortcutConflicts`，按分派域算）。
   - 空闲内存 ≤ 100 MB，硬上限 300 MB。
   - 界面文案一律 `i18n.T("中文")` / `i18n.Tf(…)`，改了文案跑 `TestCatalog -update` 再补 `en.json`。
   - 术语和 README 一致：已暂存 / 未暂存 / 未跟踪、局部 / 全文、并排 / 统一、抓取 / 拉取 / 推送、贮藏、丢弃改动、预览 / 固定标签、工作区干净……
6. **state.json 向后兼容**：`~/Library/Application Support/Holt/state.json` 带格式版本（`workspace.Version`），旧格式的文件照常读、
   一步步迁到现在的格式（见 [docs/architecture.md](docs/architecture.md)）。新加字段时旧文件照常读（缺的当默认），`TestStateMigration` / `TestStateSessions` 要跟着加。
7. **改了行为要同步文档**：README（功能、快捷键）、`CHANGELOG.md` 的"## 未发布"、`docs/architecture.md`、
   `docs/shortcuts.md`（生成，见下面的命令）；踩到新的坑写进 `docs/mygo-pitfalls.md`。

## 命令

在仓库根目录下跑。go.mod 要求的 Go 可能比本机装的新，所以都带 `GOTOOLCHAIN=auto`。

```sh
GOTOOLCHAIN=auto go vet ./...
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go test -race ./...                                   # 整套要一两分钟，大头是 internal/ui
WORKBENCH_TEST_WS=<.code-workspace 或目录> GOTOOLCHAIN=auto go test ./...   # 连读真实工作区的只读测试一起跑
mkdir -p <目录> && WORKBENCH_SHOT=<目录> GOTOOLCHAIN=auto go test -run 'TestScreenshot|TestAboutView' -v ./internal/ui/
GOTOOLCHAIN=auto go test -run '^$' -bench . -benchtime 20x ./internal/search/ ./internal/fuzzy/   # 搜索 / 模糊匹配的速度
GOTOOLCHAIN=auto go test -run TestShortcutDoc ./internal/app/ -update   # 改了命令 / 快捷键后重新生成 docs/shortcuts.md
GOTOOLCHAIN=auto go test -run TestCatalog ./internal/i18n/ -update      # 改了界面文案后重新收集 zh-CN.json；
                                                                   # 然后不带 -update 再跑，它列出 en.json 缺的 / 多的，手工补译文
GOTOOLCHAIN=auto go test ./internal/appicon -update                     # 改了图标的画法后重新生成 assets/icon.png
GOTOOLCHAIN=auto go tool mygo build -skip-dmg -skip-notarize -sign -    # 出 build/darwin-arm64/Holt.app（ad hoc 签名）
open -g -n build/darwin-arm64/Holt.app --env WORKBENCH_STATE=<临时目录> --env WORKBENCH_LOGS=<临时目录>
footprint -p <pid> --noCategories -f bytes                         # 30 秒后量；量完只杀自己启动的那个 pid
```

### 环境变量

都是开发、测试、量内存用的，用户用不着。

```sh
WORKBENCH_TEST_WS=<.code-workspace 或目录>   # 只读测试和截图用的真实工作区；没设时这些测试跳过
WORKBENCH_SHOT=<目录>                        # 截图测试把 PNG 写到这里（目录要先建）
WORKBENCH_SHOT_WS=<工作区>                   # 截图用另一个工作区（真实工作区都干净时，用一份有改动的克隆）
WORKBENCH_SHOT_RENDER=<目录>                 # 截图里的 Mermaid / 公式按目录里录好的真 WebKit 结果回放
WORKBENCH_MEASURE=1                          # 跑大文件的计时测试（TestMeasureBigFile 等），平时跳过
WORKBENCH_WORKSPACE=<路径>                   # 启动时打开这个工作区
WORKBENCH_STATE=<目录>                       # 用那个目录里的 state.json / window-state.json / termd，不动默认的；也不抢单实例锁
WORKBENCH_LOGS=<目录>                        # 日志和崩溃日志写到这里，不写 ~/Library/Logs/Holt
WORKBENCH_SINGLE=1                           # 设了 WORKBENCH_STATE 也抢单实例锁（只在测单实例本身时用）
WORKBENCH_PANIC=1                            # 启动 3 秒后在后台 goroutine 里 panic（测崩溃日志）
WORKBENCH_CAPTURE=<文件.png>                 # 启动 5 秒后把窗口内容存成 PNG（量内存时别开）
WORKBENCH_CAPTURE_AFTER=2s                   # 改成启动后多久截
WORKBENCH_FOCUS=editor|terminal              # 启动时把键盘给编辑器 / 终端（量有焦点的内存）
WORKBENCH_CARET_REST=off                     # 编辑器光标一直闪（不歇），只用来对比内存
WORKBENCH_TERMS=all                          # 启动时就把所有终端都起来（量 n 个仓库 × m 个终端）
WORKBENCH_TERM_CMD='echo MARK-$$'            # 第一个仓库的第一个终端出了提示符就敲这一行（termd 冒烟）
WORKBENCH_ALLOPEN='async def'                # "全开"：仓库读完后开两个 Diff、侧栏搜这个词、当前仓库的终端都起
WORKBENCH_MATERIAL=hud                       # 窗口底是通透时用这种材质（mygo.Vibrancy 的值），压过设置里的「材质」
WORKBENCH_PLAIN=1                            # 普通标题栏、不要毛玻璃，对比内存用
WORKBENCH_FONT=<字体家族>                    # 终端 / 编辑器的等宽字体（"monospace" 是系统默认）
WORKBENCH_SECOND=<工作区>  WORKBENCH_SECOND_CLOSE=30s   # 启动时再开一个窗口；过这么久把它关掉（看还不还内存）
WORKBENCH_RESTORE_GAP=600ms                  # 还原几个窗口时前一个画完第一帧后隔多久开下一个（默认 2.5s）
MYGO_FRAME_STATS=all                         # mygo 的帧统计写进日志（量内存时数帧）
```

`WORKBENCH_TERMD_HELPER`、`WORKBENCH_ASKPASS_SOCK` / `WORKBENCH_ASKPASS_ID` 是进程之间内部传的，别手动设。

**为什么到处是 workbench**：这个项目以前叫 Workbench，后来叫 Grove，现在叫 Holt。改名只改了用户看得到的名字（`mygo.json` 的名字和 bundle id、
窗口标题、关于、`TERM_PROGRAM`、临时文件前缀）。`cmd/workbench`、`WORKBENCH_*` 环境变量、日志文件名 `workbench.log`（冒烟和
`tools/measure` 读它）、导出设置 JSON 的 `"workbench"` 键都故意没改，别顺手改。尤其是 `cmd/workbench`：目录名就是 `go run`
出来的可执行文件名，mygo 拿它当状态目录名；改成 `holt` 的话，在不分大小写的 APFS 上就和装好的 Holt 共用
`~/Library/Application Support/Holt/`。第一次启动时 `cmd/workbench/migrate.go` 把旧的 Grove（更早是 Workbench）状态和日志目录搬到 Holt 下。

## 约定

**计时的验收测试**（监听 1 s 跟上、10 万行搜索 < 1 s）的预算写成 `testutil.Budget(time.Second)`：`-race` 下放宽到 3 倍
（`testutil.Race` 由 `race.go` / `norace.go` 的 build tag 定），平常还是原来的数。新的计时断言也这么写。

**图标和图标按钮**：图标大小只用 `widgets.go` 的三档常量 `iconInline`（11，跟着字的小标）/ `iconRow`（13，列表行、标签、输入框）/
`iconTool`（16，图标按钮），空态中央的大图标用 `iconEmpty`；`TestIconSizes` 扫源码，`Icon(...).FontSize` 写数字就挂。
图标按钮用 `toolButton` / `iconButton`（`toolSize` 28 见方）、`rowButton`（20）、`toggleButton`；自己拼的按钮用 `toolStyle` 上样子，
悬停底色用 `toolFill`，别用 `SurfaceHover`。

**新动作**先进 `internal/app/commands.go` 的注册表（菜单、快捷键、悬停提示、`docs/shortcuts.md` 都从那里来），再在界面上调它。

## 单实例、真窗口和量内存

**单实例**：不带 `WORKBENCH_STATE` 启动的 Holt 会抢单实例锁，已经开着一个（比如你日常在用的那个）就把参数交给它、自己退出。
所以手测、量内存一律带 `WORKBENCH_STATE=<临时目录>`（这样的实例不抢锁、不动默认的 state.json，还有自己的 termd），
日志带 `WORKBENCH_LOGS=<临时目录>`。要测单实例本身才加 `WORKBENCH_SINGLE=1`，而且先 `pgrep -fl Holt` 确认没有别的实例。
只杀自己起的 pid，别按名字杀。

**量内存**用 `tools/measure/`：`measure.sh <标签> <Holt.app>` 从种子（`seed/idle.json` / `allopen.json`）造 `WORKBENCH_STATE`、
拷 `seed/window-state.json`（固定 1440×880）、带 `WORKBENCH_LOGS` 和 `MYGO_FRAME_STATS=all` 用 `open -g -n --env …` 起一次，
每几秒记 footprint 和子 shell，跑够了 TERM 掉，`report.py` 打汇总。别直接跑二进制：那样应用自己会到前台、抢走键盘。
`longrun.sh` 是 8 h 的长跑，适合自己在终端里起。

脚本里的规矩，别绕开：

- 机器的输入空闲（`HIDIdleTime`）≥ 60 s 才起，不够每 20 s 再看、最多 45 min，到了写 `SKIPPED`。
- 起了以后不往窗口送任何事件（"全开"用 `WORKBENCH_ALLOPEN` 摆好，不点）。
- 量前量后各记一次每个仓库的 `git status --porcelain=v2 --branch`，不一样就喊 `REPO CHANGED`。
- 前台换了就记进 `front.txt`；量的那个 Holt（按 pid）到过前台的那几段在汇总里单列、不算。别的冒烟 / 测量开着时先等它关
  （它抢 CPU，而且它一关系统会把前台给别的窗口）。
- 工作区对着内置盘上的一份复制品（ad hoc 签名的新构建读可移除的卷会卡住，见 `docs/mygo-pitfalls.md`），复制品连被忽略的目录一起拷
  （`node_modules` 在不在，空目录、搜索的数都不一样）。
- 机器忙不忙会影响启动用时：`meta.txt` 里有起前、量完的 `uptime`，比用时先看负载。

手动量时也照这几条：

- 别开 `WORKBENCH_CAPTURE`（它会多出一份窗口位图）。
- 量的实例可能中途变成前台（有人点了窗口，或者前台的那个被杀后系统激活了下一个），一到前台光标就闪、帧数涨、内存跳到两百多。
  看日志里的 `front=` 和帧数，只取 `front=other`、帧数不动的那几次。
- 有焦点、无焦点都要量：光标一闪就是一帧，mygo 2 秒没有新帧才放掉窗口大小的帧内存，只量无焦点会漏掉一两百 MB。
- 窗口位置（`window-state.json`）在 `WORKBENCH_STATE` 目录里。新建的目录没有它，窗口开在默认位置，后台量时可能整个被前台应用的窗口盖住，
  mygo 就停帧，空闲只剩 63 MiB 左右，和别的数比不了。用 `tools/measure/seed/window-state.json`。

## 目录

细节见 [docs/architecture.md](docs/architecture.md)。

- `cmd/workbench`：入口，开窗口；第一行先分出 termd 后台进程
- `internal/app`：状态机和命令注册表（`commands.go`）；一个窗口一个 `App`，`app.Windows`（`windows.go`）是开着的窗口的注册表，
  几个窗口共用一个 `workspace.Store`（state.json）
- `internal/ui`：纯界面。界面函数只读状态，通过 `a.Exec(命令 id)` 或 `App` 的方法改状态；后台 goroutine 只经 `a.Update` 回主线程
- 不碰界面的逻辑：`internal/git`、`files`、`docs`、`search`、`globs`、`fuzzy`、`watch`、`workspace`、`graph`、`outline`、`highlight`、
  `history`（本地历史）、`termd`（终端后台进程）、`askpass`、`tasks`、`snippets`、`render`（Mermaid 和公式）
- `internal/i18n`：文案表；`internal/version`、`crash`、`appicon`：版本号、日志和崩溃日志、代码画的图标
- `internal/testutil`：测试辅助（临时仓库、等待、计时预算）
- `tools/measure`：量内存的脚本
- `resources/`：原样拷进 .app 的字体和第三方许可证；`assets/icon.png` 是应用图标
