# 架构

本文按子系统介绍 Grove 当前的结构和实现，供修改代码时参考。各版本的改动记录在根目录的 `CHANGELOG.md`。

```
cmd/workbench ──► internal/ui ──► internal/app ──► internal/{git, files, docs, workspace, search, globs, fuzzy, watch,
 （工作台窗口、设置和  （纯界面）       （状态机 + 命令注册表）    graph, outline, history, askpass, termd, tasks,
   关于两个小窗口、                                           snippets, render, fonts}
   单实例、日志）                                            （不碰界面的逻辑；highlight 挂在 docs 底下）
                    └──────────────┴──► internal/i18n（文案表，各层都用）
              ──► internal/{version, crash, appicon}（版本号、日志和崩溃日志、代码画的图标）
```

依赖方向从左到右：`ui` 读取 `app` 的状态、调用其方法，`app` 不依赖具体界面。右侧逻辑包之间只有少量依赖（`search` 用 `docs` / `files` / `git` / `globs`，`docs` 用 `git` / `highlight` / `outline` / `snippets`，`files` 用 `fuzzy` / `git`，`watch` 用 `git`），均不依赖 `app` 和 `ui`。`cmd/workbench` 另外直接用 `askpass` 和 `termd`，因为同一个可执行文件也是这两个子命令的入口。

## 目录

| 目录 | 职责 |
|---|---|
| `cmd/workbench/` | 入口。第一个参数是 `askpass` / `termd` 时交给 `askpass.Main` / `termd.Main`；否则解析参数（工作区路径）、搬旧目录（`migrate.go`）、载字体、建 `app.Windows` 并把 mygo 的窗口回调接上去（`NewWindow`、`OnClose` → `CanClose`、`OnClosed` → `Closed`、`OnFocus`、`OnBeforeQuit` → `RequestQuit`），单实例锁和 `deliver`，日志和崩溃日志，设置窗口和关于窗口，`WORKBENCH_*` 环境变量 |
| `internal/app/` | 状态机：工作区和仓库、面板开关、编辑器组和标签、文件树、Git 栏、历史、搜索、终端、git 写命令队列、对话框状态、命令注册表和由它生成的菜单栏、state.json 读写、多窗口注册表。一个窗口一个 `App` |
| `internal/ui/` | 纯界面，按区域分文件：`view.go` 总布局，`titlebar.go`，`sidebar.go`（文件树），`gitbar.go`，`search.go`，`outline.go`，`openeditors.go`，`tabs.go`（编辑器组和标签内容），`history.go` / `graph.go`（右栏），`bottom.go`（终端 / 输出 / 问题），`statusbar.go`，`palette.go`，`dialogs.go` / `dialogframe.go`，`menus.go`（右键菜单），`settings.go` / `about.go`（两个小窗口），`widgets.go` / `icons.go` / `fileicons.go` / `accent.go` |
| `internal/workspace/` | `.code-workspace` 解析、state.json 的 schema 和旧格式迁移、`Settings`、`Session`（按工作区的标签、展开目录、终端、布局）、几个窗口共用的 `Store`、设置的 JSON 形式（`settingsjson.go`）、`files.exclude` 的读取 |
| `internal/git/` | Git 命令调用。只读：status（porcelain v2）、diff 两侧、log、分支 / 标签 / 远程、blob、ls-files、blame、工作树、子模块；写入：`RunRW` / `RunOp`（非交互环境、超时、进程组）；块级和行级补丁（`hunk.go`）、远程地址转网页（`web.go`）、常见错误说明（`explain.go`）、需要终端的命令识别（`interactive.go`） |
| `internal/askpass/` | 凭据提示的两端：`Main` / `Run` 处理 `Grove askpass <提示>` 子进程（连接套接字、发送一行 JSON、等待回答），`Listen` / `Server` / `Close` 处理应用端；`CleanStale` 清理上次遗留的目录，`Classify` 识别用户名、密码或口令提示 |
| `internal/files/` | 文件树模型：`git ls-files` 建，普通文件夹用 `Walk`；展开收起、筛选、改动标记、被忽略的文件懒读、符号链接、子模块行、合并目录、增量 `Diff` / `Apply`、空目录 |
| `internal/docs/` | 打开的文档：各种 `Kind`、读写、外部改动检测、编码、大文件、历史版本、Diff 两侧、冲突块解析、保存整理、按行 diff（`LineEdits`）、交互式变基的计划、光标信息 |
| `internal/highlight/` | 大文件的后台高亮：`For(name)` 复制 mygo 的语言定义，只替换 `Highlighter`；一个 goroutine 从头分词并记录每行开头的状态，主线程据此处理可见行 |
| `internal/search/` | 跨仓库全文搜索和替换的计算：候选、worker、字面 / 整词 / 正则、glob、编码、`advanced.go` 的 regexp2 后备、`replace.go` |
| `internal/globs/` | 路径 glob（`**` 跨目录，`*` `?` 不跨，`{a,b}`，不分大小写）：`Set.Match` 给搜索的包含 / 排除，`Set.MatchRoot` 给 `files.exclude`，带目录缓存的 `Matcher` |
| `internal/fuzzy/` | 快速打开和命令面板的模糊匹配：优先匹配文件名，无法匹配时再包含目录；连续、词首和完整文件名匹配会加分，命中位置返回给界面用于高亮 |
| `internal/watch/` | 文件监听：每个仓库一个 `Watcher`，封装 `mygo.WatchFiles` 的 HintsOnly 模式，报“工作区变了 / git 变了”两类提示 |
| `internal/graph/` | 提交图的泳道算法，纯算法，不碰 git 和界面 |
| `internal/outline/` | 大纲：解析 Markdown 标题、Go（`go/parser`），其他语言按行正则或花括号嵌套识别；计算粘性滚动的吸顶行（`sticky.go`） |
| `internal/history/` | 本地历史存储：每个文件一个目录，包含 `index.json` 和内容文件；记录时机和恢复流程在 `app/localhist.go` |
| `internal/termd/` | 终端后台进程：帧协议（`proto.go`）、`Server`、会话（pty + shell + 无头终端）、应用端的 `client.go`、自行实现的 pty（`pty_darwin.go`）、`Spawn` |
| `internal/tasks/` | 读取仓库根目录中的任务定义：package.json 的 scripts、Makefile / justfile 的目标、Cargo.toml / go.mod 的常用命令；仅读取文件 |
| `internal/snippets/` | 代码片段：内置的（embed）加状态目录里 `snippets/*.json` 的；`Expand` 把 VS Code 片段体展开成文字和跳位，`Session` 管 Tab / ⇧Tab 跳位 |
| `internal/render/` | Markdown 预览中 Mermaid 图和公式的渲染：`Renderer`（缓存、队列、一个 goroutine）和 `Backend`；`web.go` 使用不可见的 `mygo.WebView` 作为后端 |
| `internal/fonts/` | 编辑器 / 终端等宽字体的选择和内置字体注册；`app` 渲染 Markdown 时也会使用，因此不放在 `ui` 中 |
| `internal/i18n/` | 文案表：`T` / `Tf` / `Tn` / `List`，`zh-CN.json`（测试从代码里收）、`en.json`；`Set` / `Lang` / `Locale` |
| `internal/version/` | 版本号：`App`（`mygo build` 用 ldflags 编进来，`go run` 和测试里是 `dev`）、`Mygo()`、`Go()`、`Platform()`、`String()` |
| `internal/crash/` | 日志目录：`workbench.log`（满 1 MB 轮换）和崩溃日志 |
| `internal/appicon/` | 用有向距离场绘制应用图标（浅色圆角方块上的钢蓝色 G 形 git 分支）；`-update` 生成 `assets/icon.png`，关于窗口使用同一套绘制代码 |
| `internal/testutil/` | 测试用：临时仓库（`TempRepo` / `NewRepo` / `ThreeRepos` / `SubmoduleRepo`）、后台回调队列 `Queue`、推帧等待、`Budget` / `Race`、`WaitTermReady` |
| `tools/measure/` | 测量内存和启动用时的脚本，不包含在应用中：`measure.sh`、`report.py`、`longrun.sh`、`seed/`；Go 端仅有 `TestMeasureScripts` |

## App 和命令注册表

### App

`app.App` 保存一个窗口的全部状态。界面每帧读取状态，只通过命令（`a.Exec(id)`）或 `App` 的方法修改状态。后台 goroutine 不直接访问 `App`，而是将结果交给 `a.Update(func())`，由主线程更新。

切换工作区（`switchTo`）会重写整个 `*a`。后台任务开始前需保存 `up := a.Update`，返回时通过 `up` 更新，并核对原仓库是否仍有效（比较仓库指针或代数 `Gen`）。`switchTo` 时属于窗口、不随工作区重置的字段有：`Windows`、`Slot`、`Win`、`Update`、`Rend`、`LastBar`、`ownStore`、尚未处理的提示框。

### 命令注册表

`app/commands.go` 里每个动作只定义一次：id（如 `view.sidebar`）、名字、分组、快捷键、生效的焦点上下文（全局 / 编辑器 / Diff / 文件树 / 终端 / 提交框 / 搜索结果）、`Enabled`、`Run`、`Checked`。由它生成：

- 菜单栏（`menu.go` 的 `BuildMenu`）。仅在 `menuSignature` 变化时重建，签名包含勾选状态、仓库列表、`keyRev` 和 `KeyRecording`。菜单栏是整个应用的（`mygo.App.SetMenu`），只有前台窗口的 `App` 同步它；菜单项的 Click 按传进来的窗口找 `App`（`target(win)`）。
- 窗口内快捷键（`BindShortcuts`）。macOS 上有菜单栏时菜单先收到按键，因此菜单快捷键会覆盖编辑器的同名快捷键。同一个键在不同上下文里可以是不同命令（⌥⌘F 在 Diff 里是全文 diff，在文件标签里是替换），按键和菜单项都走 `RunKey(mods, key)`：按注册顺序查找第一个当前可执行的命令。
- 悬停提示：`Tip(text, id)` 在句尾加括号里的键，`KeyText` / `KeyHint` / `KeyList` 给句子中间用的键。每帧计算，修改快捷键后立即更新。
- `docs/shortcuts.md`（`TestShortcutDoc -update` 生成）。
- 冲突检查 `TestShortcutConflicts`：id 不重复，同一上下文里同一个键只能有一个命令。
- 命令面板（`>`）。

编辑器自带的动作（复制行、多光标、折叠……）也登记成命令，id 是 `editor.<名>`，来自 `editorTable` / `editorCommands`；它们的键由编辑器自己接（`Key` 留 0），注册表只用于生成菜单、面板和快捷键文档，`inEditorChecked` 管理勾选状态。处理全文的命令在文件超过 `MaxWholeTextBytes`（1 MB）时禁用。`Command.Local` 表示有 `Run` 但键不在窗口上登记，由编辑器接（比如格式化的 ⇧⌥F）。`Section` / `Hint` 分别用于菜单分段和面板说明。

### 自定义快捷键

`app/keybind.go`。用户改过的键存在状态目录的 `keybindings.json`，只记和默认不一样的（`key` 为空表示去掉默认键）；写法由 `ParseKeys` 读（`cmd+shift+p`、`⌘⇧P` 都行），按 JSONC 解（`workspace.StripJSONC`）。每次 `applyKeybindings` 都从默认键（`keyDefaults`）起重算，所以“重置”就是从文件里删掉那条；命令的 `Mods` / `Key` 就是生效的键，菜单、快捷键、提示照旧从注册表读。

冲突按分派域判断（`keyDomain` / `clash`）：同域同键时用户配置优先，原命令的快捷键被移除，`Shadowed` 记录覆盖来源。编辑器自带的命令（`editor.*`）改了键时由 `editorKeys` 生成 `editor.Binding` 交给编辑器（`EditorBindings` / `DiffBindings`），同时移除旧的默认键。不支持和弦快捷键或 when 条件。命令表由整个进程共用。

改键在设置窗口的“快捷键”页（录键时 `SetKeyRecording`，菜单暂时不接键）；也能直接在编辑器里改 `keybindings.json`，保存时（`keybindingsSaved`）重读，无法解析的条目提示一次，并列在设置页（`KeyErrors`）；整个文件无法读取时（`keyBroken`），设置页停止写入，避免覆盖原文件。写文件是原子的（`writeFileAtomic`）。

## 工作区、设置、会话和状态文件

### 工作区

工作区是一个 `.code-workspace` 文件（`folders` 里的每一项是一个仓库）或者一个文件夹。打开文件夹时，若它位于 git 仓库中，就使用该仓库；否则扫描下一层目录中的 git 仓库（跳过以点开头的目录）。没有找到仓库时，将其作为普通文件夹（`Repo.Plain`，见“普通文件夹”）。工作区之外临时加的仓库和隐藏的仓库记在会话的 `added` / `hidden`，不改工作区文件。

没有工作区时显示空窗口，提供最近的工作区、打开文件夹和克隆入口；终端在家目录启动（`a.home`，不在仓库列表里）。最近的工作区 `RecentWs` 最多 10 个，也交给系统的“最近使用”（`AddRecentDocument`）。

打开其他工作区（`OpenWorkspace`）前，`RequestLeave` 依次检查未保存文件（`LeaveDirty`）、运行中的 git 写命令（`LeaveWrites`）和终端程序（`LeaveJobs`），需要时要求确认，通过后才调用 `switchTo`。“在新窗口中打开”调用多窗口的 `Open`。

工作区文件和 `.vscode/settings.json` 里的 `files.exclude` 由 `workspace.LoadSettings` 读（JSONC），`app/exclude.go` 把它编成每个仓库一份 `globs.Set`（`repoExclude`），文件树和搜索都按该规则排除内容。

### 设置

`workspace.Settings` 保存设置窗口中的选项，由进程内的所有窗口共用。修改后，`SettingsChanged` 将设置同步到各窗口，`settings.go` 负责应用字体、主题、材质、缩放和终端选项等。

设置有三种修改方式，最终都更新同一个 `Settings`：

- 设置窗口（`ui/settings.go`，第二个 `mygo.Window`，和主窗口共用打开它的那个 `App`）。每一项是 `settingRow` 表里的一行，右上角的搜索框按名字和说明筛（`settingsResults`）。
- 设置文档：编辑器区的 `settings.json` 标签（`docs.SettingsDoc`，只在内存里）。内容是 `SettingsJSON` 生成的全部键（零值也写）；⌘S 应用设置，不写入文件。`ParseSettings` 严格解析内容：遇到未知键或类型错误时，通过 `SettingsError` 报告行列，不修改任何设置；通过后执行 `Clamp`，整体替换设置并应用到所有窗口。其他入口修改设置后，未编辑的设置文档会由 `syncSettingsDocs` 重新生成，避免保存旧文档时覆盖新设置。
- 导出 / 导入 / 恢复默认：导出是 `{"workbench": 版本, "exported": 时间, "settings": {全部键}}`；导入时列出会变的键（`ChangedKeys` / `SettingNames`）再确认。

### 会话

`workspace.Session` 按工作区文件的绝对路径记：打开的标签（最多 50 个）、当前标签、当前仓库、展开的仓库和目录、Git 栏各段和各组的收放、终端标签和分屏、编辑器组的布局、工作树块展开的仓库、面板开关。切走、关窗口、退出时存，打开工作区时读回来。

会话保存时的几个细节：

- 历史版本和 Diff 标签也记，`path` 写仓库根（读的时候会丢掉相对路径），真正的文件在 `change` 里；那个提交没了（`GoneCommit`）就不恢复。
- 标签手动选的编码、语言、缩进记在 `Tab.Enc` / `Tab.Lang` / `Tab.Indent`；换行不记。
- 终端标签只记名字、目录、配置和分屏，以及后台会话的 id（见“终端”）。只有一个没改名、在仓库根的终端时不记。

### 状态目录

默认是 `~/Library/Application Support/Grove/`，`WORKBENCH_STATE=目录` 时用那个目录（测试和多开都靠它）。里面有：

| 文件 | 内容 |
|---|---|
| `state.json` | 设置、会话、最近的工作区、退出时开着的窗口，见下面 |
| `window-state.json` | 每个窗口的位置和大小（`WindowStates`，按 `StateKey`），在 `mygo.App.Run` 之前读 |
| `keybindings.json` | 自定义快捷键 |
| `history/` | 本地历史 |
| `termhist/` | 终端重开时补回的输出 |
| `snippets/` | 用户的代码片段 |
| `termd.sock` | 终端后台进程的套接字 |
| `logs/` | `workbench.log`、`termd.log`、崩溃日志（`WORKBENCH_LOGS` 可换） |

### state.json

```json
{"version":4,"sidebar":true,"rightBar":true,"bottomBar":true,"sidebarMode":"files",
 "sidebarW":280,"rightW":320,"bottomH":240,"termW":420,"unified":false,"fullDiff":false,"histAll":true,
 "recentWs":["…"],"windows":["…"],"cloneDir":"…","taskHistory":["…"],"searchHistory":[{"text":"…"}],
 "settings":{"theme":"dark","backdrop":"vibrancy","material":"menu","editorFont":"…","editorSize":13,"indent":"2",
             "autoSave":"delay","shell":"…","termFont":"…","termSize":12.5,"termTheme":"","scrollback":10000,
             "pullMode":"rebase","zoom":110,"historyDays":30,"lang":"en"},
 "histBranches":false,"histNoGraph":false,"commitMsgs":["修好登录","…"],
 "sessions":{"/path/to/x.code-workspace":{
   "tabs":[{"path":"/abs/a.md","pinned":true,"line":3,"col":1,"top":0},
           {"path":"/abs/repo","kind":"version","repo":"/abs/repo","change":{"kind":"commit","path":"a.go","code":"M","commit":"5fd1…","parent":"d1e9…"}},
           {"path":"/abs/repo","kind":"diff","repo":"/abs/repo","change":{"kind":"unstaged","path":"a.go","code":"M"}},
           {"kind":"term","termNum":4}],
   "active":0,"repo":"/abs/repo","open":["/abs/repo"],"dirs":{"/abs/repo":["docs"]},
   "git":{"/abs/repo":"closed"},"groups":{"/abs/repo":["stash","untracked"]},"wtOpen":["/abs/repo"],
   "terms":{"/abs/repo":{"tabs":[{"name":"构建","dir":"/abs/repo/docs"},
                                 {"split":{"row":true,"ratio":0.4,"a":{"hist":"1793001600000-3.vt"},"b":{"dir":"/abs/repo/src","profile":"fish"}}},
                                 {"inEditor":true,"num":4}],"active":1}},
   "layout":{"row":true,"ratio":0.5,"a":{"tabs":[0]},"b":{"tabs":[1,2],"sel":1,"focus":true}}}}}
```

兼容规则：

- 新字段一律使用 `omitempty`，旧文件缺少字段时使用默认值；读取时忽略未知字段（Go 的 JSON 解码默认如此）。新增字段不递增格式版本号，旧应用仍可读取新文件，只是无法使用新增内容。`settings` 里没设的键不写。
- 默认开的开关存反面（`histNoGraph`、`noLocalHistory`、`noStickyScroll`），这样默认状态什么都不写。
- 读取时通过 `Clamp` 清理数据：丢弃相对路径的仓库键和目录，截断超长列表，将越界下标归 0，清空带控制字符的主题名，将无法识别的材质 / 窗口底恢复为默认值。
- 格式版本号仅在字段含义变化时递增。当前为 4，最近一次变更是会话中 `groups` 的含义：以前记录“收起的组”，现在记录“用户点击后，与默认状态相反的组”。冲突 / 已暂存 / 未暂存默认展开，贮藏默认收起（`groupOpenByDefault`），因此包含 `"stash"` 表示“贮藏已展开”。读 3 的文件时把 `groups` 里的 `"stash"` 去掉。
  以前未跟踪单独一组（键 `"untracked"`），现在并进未暂存（`GroupKey(git.Untracked)` 是 `"unstaged"`）；旧文件里的 `"untracked"` 读的时候扔掉（`groupKeys` 不认），不升版本。
- 更早的格式由 `workspace.migratePanels` 兼容：1 的 `panels` 表、更早的四元 bool 数组、更早的 `repos.json`；顶层的 `added` / `hidden` 归给第一个打开的工作区。下次保存就是 4。

所有窗口共用一份 state.json，通过 `workspace.Store` 保存各自的会话。内容未变时不写盘。

## 所有仓库同时可见

这是 Grove 的第一原则：文件树、Git 栏、搜索、历史都同时列出工作区里的每个仓库，没有“当前仓库 + 切换器”的视图。新功能也需遵守，不要新增只显示当前仓库的列表。

“当前仓库”（`Current()`）还在，但只用在少数地方：新终端开在哪、右栏历史的“当前仓库”页、标题栏的分支胶囊。点文件树、Git 栏或者切到某个文件标签也会更新当前仓库（`UseRepo` / `SelectRepo`），⌘1–⌘9 跳到第几个仓库（`JumpRepo`）。

### Git 栏

`GitRows` 将所有仓库整理为一张表：每个仓库一个段头（`Header`，贴顶），下面依次是进行中操作的横幅（`RowOngoing`）、工作树块头（设置里开了"在 Git 栏显示工作树"才有，默认收着）、提交框（`RowCommit`，在改动上面：文件多时不用滚到段底）、
各组的标题和每个变更（冲突 / 已暂存 / 未暂存）、说明行，最后是贮藏组头（设置里开了"在 Git 栏显示贮藏"才有，默认收着，和工作树组头一样只有图标和条数）。
两个开关是 `Settings.ShowWorktrees` / `ShowStashes`（零值就是关，老的 state.json 不用迁移）。`Repo` 没有 `App` 指针，`App.newRepo` 给每个仓库接上
窗口的 `&a.St.Settings`（`Repo.set`，换工作区 `*a = *n` 之后重新指一遍），`Repo.ShowWts` / `ShowStashes` 读它。工作树关着时：`GitRows` 不出工作树块、
`GitOpen` 的自动展开不算 `HasWorktrees`、段头不画工作树数、`NeedsYou` 是空的、`GoToWorktree` 什么都不做，`WtShown` / `WtTracked` 都是假
（不算 `WtInfo`、不盯目录；会话里的 `WtOpen` 留着）。开关变了由 `SettingsChanged` → 每个窗口的 `syncShownGroups` 收尾：关掉时收起 `WtExp`、
`clearRefHistories`、扔掉 `WtInfo`、`syncWtWatch` 停掉监听，打开时按 `WtOpen` 重算、接上监听；两种都 `syncChangeSel`，选中落在没了的行上的清掉。
贮藏关着时只是 `appendStashes` 不出行、段头不画贮藏数，`r.Extra.Stashes` 照常读，`git.stash` / `git.stashPop` 照常能用。
未跟踪的文件和未暂存的改动同在“未暂存”一组（`groupKind`）：顺序照 `Status.Changes`（已跟踪的在前、未跟踪的接在后面），条数是两者之和，
只有未跟踪的时组名也是未暂存；每一行的 `Change.Kind` 还是各自的（字母、颜色、丢弃、暂存、目录展开照旧）。组头的 + 是 `StageUnstaged`：
没有冲突时是不带路径的 `git add -A`（按路径传的话，上万个改动的文件会超出 macOS 的命令行长度限制）；有冲突时按路径 `git add -A -- …`，
不用 `add -A` / `-u`（会把冲突的文件也暂存、等于标成已解决）；丢弃仍是 `git restore .`，不动未跟踪的，这组全是未跟踪的时不显示。段头右边是这个仓库的拉取 / 推送按钮（`syncButton`），和标题栏的同步按钮共用 `syncOf`（提示文字、没上游时灰着、
`Repo.Pulling` / `Pushing` 在跑或排队时转圈），点了走 `App.Pull` / `Push`，没有另一套 git 调用。段的收放是 `GitFold`：`FoldAuto`（有改动展开、干净的收成一行；有工作树（显示工作树开着时）、合并 / 变基停在一半的也展开，让工作树块和横幅看得见）、`FoldOpen`、`FoldClosed`，点过箭头就按用户的，存进会话的 `git`。

行的身份是 `ChangeRow.Key()`（仓库 + 组 + 路径），选中 `GitSel`、悬停 `GitHover`、多选 `GitChosen` 都按键记，因此刷新后即使行的位置变化，仍会保持选中原来的项目。多选的操作（`StageChanges` / `UnstageChanges` / `DiscardChanges` / `OpenDiffs`）先按仓库分组（`byRepo`），再加入各自的队列；一次打开超过 20 个 Diff 时先要求确认。
变更行悬停（或列表有键盘时选中）出的行内按钮（`ui/gitbar.go` 的 `changeActions` / `stageActions`）用 `ActChanges`：指针所在的行在两行以上的多选里时作用于选中的全部，否则只有这一行；按钮的动作经 `gitFrame.do` 留到列表建完再做。列表有焦点时的空格 / ⌘⌫ 是 `changes.toggleStage` / `changes.discard`（上下文 `GitList`，由列表自己接键，命令面板也能用），作用于 `KeyChanges`。暂存 / 取消暂存 / 丢弃会让选中的那行挪走时，`markGitAfter` 先记下它后面（没有就是前面）第一个不在选中里的行，状态重读后 `syncChangeSel` 跟不上当前标签时停在那一行。

提交框每个仓库一个（`CommitOpen` / `CommitMsg`），在段头（和工作树块头）下面、改动分组上面；`ShowCommit` 把段头滚到顶上（`ui.Start`），贴顶的段头不挡它；有已暂存的改动、写了信息、在修改上一次提交、合并的冲突都解决了，或者用户点开了“提交…”时展开（`CommitShown`）。⌘↩ 提交键盘所在的那个。提交按钮是和"撤销上次提交"一样的普通图标按钮（`iconAction`：`rowToolSize` 见方、`toolStyle`，没有底色，悬停才有淡底；`ButtonBase` 所以 Tab 按得到），"提交 N 个文件" / "修改提交"只进读屏和提示；
左边"修改上一次提交"是图标开关（`filterToggleOf`，`rowToolSize` 见方，开着是强调色），提交中（`Committing`）画灰、点了不切；
不能提交（`CanCommit`）时画灰、点了不提交，但不调 `Disabled`（mygo 的 `Disabled` 不出悬停提示），提示里写原因（`CommitWhy`）。
面板里的动作（冲突条、进行中横幅、替换预览条、Diff 选行条和块按钮、搜索的全部替换、变基向导、终端退出浮条）都用同一个 `iconAction`
（`ui/widgets.go`）：只有图标，读屏名是原来按钮上的字，`why` 不空时画灰、提示里写原因、点了没反应。段头的"全部暂存"有可暂存的改动就一直画，不看悬停。最近 30 条提交信息（所有仓库共用）存在 `St.CommitMsgs`，提交框里 ↑↓ 翻。

### 合并的历史

右栏历史除“当前仓库”页外，还有“所有仓库”页（`histAll`）。`AllHistRows` 把每个仓库的一页提交按作者时间（`%aI`）做 k 路归并；各仓库的读取进度不同，因此只合并到所有仓库均已读取的时间（地平线），往下滚 `LoadMoreAllHistory` 给落后的仓库补一页。选中是 `HistAllSel`。所有仓库页不画提交图，每行左边一个仓库圆点（`repoDot`，颜色按仓库顺序轮换）。

## Git

### 只读和刷新

只读命令都带 `GIT_OPTIONAL_LOCKS=0 LC_ALL=C`：前者防止 `git status` 刷新 index，避免与终端中的 git 操作争用锁；后者固定输出语言，便于解析。

一个仓库的状态是 `git status --porcelain=v2 -z --branch` 加 `git.Extra`：进行中的操作（`Ongoing`）、HEAD、未推送的提交（`rev-list -n 200`）、贮藏（最多 50 个）、远程的网页地址、`GitDir` / `CommonDir` / `Linked`（是不是链接工作树）、子模块（`Subs`）、`HeadRoot`（HEAD 没有父提交）、进行中操作的进度（`Progress`）、远程列表。

刷新只有一个入口：`app/watch.go` 的 `RefreshRepo(r, why)`。`why` 是 `WhyManual` / `WhyOpen` / `WhyOp` / `WhySave` / `WhyFileOp` / `WhyWatchGit` / `WhyWatchFiles` / `WhyFocus` 之一，记在 `Repo.LastWhy` 里给日志和测试看。它在后台读状态、分支、文件树（必要时）、重读开着的文档（`syncDocs`），回到主线程一起换上。只有工作区文件变化（`filesOnly`）时不重读历史。读取期间再次收到刷新请求时，先记录到 `r.again`，本次完成后再读一次，避免并发读取。监听停了的仓库（出错、目录没了）没有别的提示来源，窗口回到前台时 `RefreshOnFocus` 给它们补读一遍（3 秒内只读一次）。

### 写命令和队列

写命令走 `git.RunRW` / `RunOp`：不交互（`GIT_TERMINAL_PROMPT=0`，`GIT_EDITOR` / `GIT_SEQUENCE_EDITOR` 换成 `true`，分页器换成 `cat`），默认 2 分钟超时，在自己的进程组里跑，取消时整组先 SIGTERM 再 SIGKILL。

每个仓库有一条队列（`app/queue.go`），通过 `RunOp` 入队，一次只执行一条命令。标题栏的队列按钮带有旋转指示，菜单可用 `CancelOp` 取消运行中的命令，或用 `Dequeue` 移除尚未开始的命令。取消后检查是否留下 `index.lock` 之类的锁文件（`git.StaleLocks`），存在时只提醒，不删除，因为可能有其他 git 进程正在运行。“全部抓取 / 拉取”是 `Batch`：按 Git 栏顺序依次处理各仓库，单个仓库失败不影响后续仓库，状态栏显示进度，`StopBatch` 停止尚未开始的任务。拉取默认 `--ff-only`，设置里可改成 rebase 或 merge。

持续输出的长任务（如克隆）使用 `RunStream` + `Keep`：通过 `readChunks` 分段读取输出，有进度时调用 `Touch` 重置超时计时，因此仅在任务停止推进时超时。

失败时，`git.Explain` 识别常见错误（没有上游、被拒绝、认证失败、合并冲突等），给出简明说明；`AlertDetail` 将 git 原始输出的最后几行放入“详细信息”。

需要终端的 git 命令（`git.NeedsTerminal`：`-i`、`-p`、`commit` 不带 `-m` 之类）在命令面板 `>git` 里输入时不在后台跑，而是开一个终端去跑（`runGitInTerm`）。

### 凭据

git 请求用户名、密码或 SSH 口令时，由 Grove 显示凭据对话框。启动时在 `os.TempDir()` 下建 `grove-askpass-<pid>`：一个 0600 的套接字和一个 0700 的小脚本，脚本就是 `exec Grove askpass "$@"`。只有写命令的环境里带 `GIT_ASKPASS` / `SSH_ASKPASS` / `SSH_ASKPASS_REQUIRE=force` 指向它；读命令不带，不会在刷新时弹框。

`Grove askpass <提示>` 子进程（`askpass.Main`）连接套接字，发送一行 JSON（提示、`GROVE_ASKPASS_ID`），等待回答后写入标准输出。应用端按 id 查找对应的窗口和命令（`askRegister` / `askDone`），由 `Classify` 识别提示种类，加入 `a.Cred` 后显示对话框，用户回答后调用 `AnswerCred`。同一主机的用户名和密码在 `credCache` 中保留 2 分钟，避免在一次“全部拉取”中重复询问。启动时由 `CleanStale` 清理上次遗留的目录。测试的 `TestMain` 也识别 askpass 模式，因此测试二进制也能作为 askpass 脚本的入口。

### 暂存、块和行

Diff 标签里每块改动有暂存 / 取消暂存 / 丢弃按钮（`app/hunks.go`）。`git.HunkPatch` 从那一块生成补丁，暂存是 `git apply --cached -`，取消暂存是 `--cached -R`，丢弃是工作区上 `-R`。

选几行再暂存是行级（`app/lines.go`）：`LinesEdits` 把选中的删除行和新增行配对，`git.LinesPatch` 生成只含这些行的补丁，总是正向套到 index 上（取消暂存时反过来算一份正向的），这样不用处理反向补丁的上下文问题。

编辑器左边的改动条可以点开看那块的原文（`app/peek.go`：`Peek`、`DocHunks`、`HunkAt`、`OnGutterClick`），里面能还原那块（直接改缓冲区，⌘Z 能撤）或者暂存它（要先保存，同样走 `HunkPatch`）。块是编辑器的 `DiffState{Old: d.Base, New: d.Buf}` 算的，和改动条同一种算法；`DiffState` 按标签留着（最近 4 个），缓冲区改了 mygo 只重比改动附近的行，不每次整篇 diff（装订线接管点击每帧都要问有没有块）。编辑器里的块随缓冲区重算，`SyncPeek` 跟着挪。

Diff 里 ⌥J / ⌥K 跳上一个 / 下一个文件，上下一处改动是 `app/diffnav.go` 的 `GoToHunk`。

### 冲突和三方合并

冲突标记由 `docs/conflicts.go` 解析（`ParseConflicts` / `ConflictBlocks`，按 `Buf.Version` 缓存），`Resolved` 看还剩几处。`app/conflicts.go` 的 `ResolveConflict` / `ResolveAll` 只改缓冲区：保留我方、对方、两者。只有 `MarkResolved` 会执行 git 写操作：先保存文件，再运行 `git add`。

冲突的文件在 Git 栏的“冲突”组（`Change{Kind: Conflict}`），单击在编辑器里打开（`OpenConflict`），冲突就在那里解决。右键能开两种 Diff：HEAD 和工作区比，或者“两边”比（`Code: 'S'`，`git ls-files -u` 找到 `:2:` 我方和 `:3:` 对方）。在文件标签里，`ui/merge.go` 每帧按解析结果给冲突块上色、在块头放按钮（`conflictMarks`），⌃⌥1 / 2 / 3 选我方 / 对方 / 两者（`mergeKeys`）。改动条（GutterMarks）只给冲突用。

### 进行中的操作

合并、变基、拣选、还原、bisect 停在一半时，仓库段顶上有横幅（`RowOngoing`），带进度（`ReadProgress`：变基第几步共几步）。按钮是继续 / 跳过 / 中止（`app/ongoing.go`：`ContinueOngoing`、`SkipOngoing`、`AbortOngoing`）。继续和跳过走 `runStep`：退出码非 0 且输出里有 `CONFLICT (` 当成又停下了，不报错；别的非 0 才是失败。

日常操作（`app/gitdaily.go`）：`Merge`、`RebaseOnto`、`CherryPick`、`Revert`、`ResetTo`（soft / mixed / hard，hard 要确认）、`UndoCommit`（软重置一个提交，把信息填回提交框）、`PushForce`（`--force-with-lease`）、`Sync`（拉了再推）。不能做的原因都在 `app/gitwhy.go`：每种 Git 操作一个 `XxxWhy`（`FetchWhy`、`PullWhy`、`PushWhy`、`PushForceWhy`、`SyncWhy`、
`StageAllWhy`、`StashWhy`、`RefWhy` / `MergeWhy` / `RebaseOntoWhy`、`SwitchWhy`、`DeleteBranchWhy`、`RemoveWorktreeWhy`、`UndoWhy`、
`PickWhy`、`ResetWhy`、`RebaseWhy`…），空串 = 能做，按"最根本的先"的固定顺序只报一条。原生菜单用 ui 的 `whyItem` / `withWhy` 括在标签后面，
按钮写进提示（`syncOf`），命令注册表的 `Command.Why` 给命令面板（写在灰着的命令后面）和菜单栏（点了提示原因）；
动作本身开头再查一次（`a.Blocked(XxxWhy(…))`：不为空就 toast、不执行），菜单开着时状态变了也不会照做。
提交后自动推送、同步拉完再推用不查的 `pushNow`（那时状态还没刷新）。停在冲突上时交给横幅（`stopOp`）。`inHead` 判断某提交在不在当前分支上。

提交和修改上一次提交在 `app/commit.go`。

### 分支、标签、远程

`app/gitrefs.go`：`RefDialog` 一个对话框五种用法（`RefDlgKind`：分支改名、新建标签、删除标签、远程改名、添加远程），底下实时预览要跑的命令，`refNameErr` 查重名和 git 不收的字符。删除分支前在后台数有几个提交没合并再问；删除远程分支用 `splitRemote` 拆出远程名。`TagRemote` 找标签该推到哪个远程。标签和远程的读取在 `git/refs.go`。

右栏历史可以看某个分支或标签的历史（`Hist.RefKind`），也可以看某个工作树的。

### 历史

`git.LogQuery` 拼 log 的参数：搜索词、所有分支、路径。右栏的搜索框由 `ParseHistQuery` 解析：`键:值`（`author:` / `作者:`、`path:` / `路径:`、`after:` / `since:` / `之后:`、`before:` / `until:` / `之前:`）、`/正则/`；日期交给 git 自己的写法认（`git.ResolveDate`），所以 `2.weeks.ago` 之类都行。文件历史带 `--follow`；`--follow` 和搜索词 git 不能一起用，所以这时读最多 `MaxFollowSearch`（5000）条，在 Go 里过滤（`matchLocal`）。

一页 `LogPage`（200）条，往下滚读下一页。提交详情里的文件列表是 `diffTreeFiles`：先 `--name-status` 再 `--numstat` 拿增删行数。比较两个提交在 `app/compare.go`（`docs.Key.Parent` 记左边是哪个）。贮藏可以展开看文件（`-u` 的贮藏未跟踪文件在 `^3`），`ApplyStashFiles` 用 `restore --source` 只取几个文件。

blame（`app/blame.go`、`git/blame.go`）：`ReadBlame` 读 `blame --porcelain`，按 `BlameKey`（文件 + 版本）缓存。编辑器左边叠一列（`ui/blame.go`，用编辑器的 Margin），也有只显示光标行的行内 blame。点一段可以在右栏历史里找到那次提交（`ShowInHistory`）。

### 提交图

`internal/graph` 只负责计算：按显示顺序将提交传给 `Lanes.Next(hash, parents)`，返回该行的泳道及上下连接关系（`Row{Lane, Edges}`）。状态保存在 `Lanes` 中，下一页继续使用。`Reserve` 将 HEAD 固定在第 0 道。log 使用 `--date-order`，避免分支连线过长。

`ui/graph.go` 画：泳道间距 14，最多画 6 道，多出来的并到最右边；颜色是淡的钢蓝、灰绿、灰沙……（不用红色），线宽 1.8，节点外面一圈底色描边。“未提交的改动”那行是橙色虚线圈，用橙色虚线（`dirtyLine`）接到 HEAD 那道。有搜索词、只看 agent 时不连线，只画点（`HistDots` / `dotGraph`）。`histNoGraph`（头部右键菜单里的“提交图”）关掉整个图。

右栏历史的列表行（`ui/history.go`）：一个提交一行，一天的第一个提交那一行上面带日期段头，展开的提交下面带卡片；段头和卡片在选中的底外面（`histParts`），展开的文件是单独的列表行，接着画卡片的两边和底（`histFileRow`）。

agent 记号：`git.AgentOf` 认提交正文里的 `Co-Authored-By:` trailer（名字里的词或邮箱域名对上 `agentPats` 表），读 log 时填进 `Commit.Agent`。“只看 agent 写的”（`App.HistAgent`，不进 state.json）读一页再筛，`Hist.read` 记 git log 读了几条，下一页从那里接着读。

### 工作树

`git worktree` 的链接工作树在 Git 栏仓库段里是一块“工作树”（默认收着，展开的记在会话 `wtOpen`）。只有 `<common>/worktrees` 目录存在时才读 `worktree list --porcelain -z`，避免为普通仓库执行不必要的命令。每个工作树的改动状态及领先 / 落后提交数仅在算状态时计算（`WtTracked`：展开着，或收着但有工作树在等你 / 在跑 / 可合并；`WtInfo`，最多 4 个并发 `WtConcurrency`）。

路径比较一律走 `git.SamePath` / `Under`（处理大小写和符号链接）。工作区里同时有主仓库和它的链接工作树时，嵌在主仓库里的那个不再单独算一个仓库（`dropNested`）。单击一个工作树在右栏看它分支的历史（`viewWorktree`），“在工作区中打开”把它加成一个仓库（`OpenWorktree`）。新建 / 锁定 / 解锁 / 移除 / 清理都是对话框 + `RunOp`；清理先 `prune --dry-run`，从 stderr 读要清掉哪些。

工作树在 Git 栏里的样子按"工作树就是 agent 的工位"来（`app/wtstate.go`）：`WtStateOf` 把 agent 状态（`WtAgent`）和 `WtInfo`
算成七种之一（等你 → 运行中 → 可合并 → 有改动 → 空闲 → 没有 agent → 已合并，值越小越需要你），`SortWts` 按它排（主工作树在最前），
`WtTop` 是收起的组头放的那一档。"已合并"就是领先 0（`git branch --merged` 的意思），目录没了的也算。整块只在设置开了"在 Git 栏显示工作树"时有（见上面 Git 栏一节）。组默认收着：列不列行（`Repo.WtShown`）只看
用户点开的（`WtOpen`，存会话；`GoToWorktree` 也会点开），有工作树在等你 / 在跑 / 可合并也不自动展开。算不算状态、盯不盯目录另算（`Repo.WtTracked`）=
`WtOpen` 或 `wtAuto`（有工作树在等你 / 在跑 / 可合并，每帧 `SyncWtAuto` 对一次），所以收着时"需要你"条和组头的符号照样跟得上。
同时只展开一个工作树（`App.WtExp`，不存）：`git.BranchFiles` 先求 merge-base 再列 merge-base → 分支的文件（就是 `main...分支`），
行是 `RowWtSummary` / `RowWtFile` / `RowWtTerm`。"合并到 main"走 `App.Merge`，先 `Ask`；灰着的原因是 `MergeWorktreeWhy`。
"需要你"条是 `NeedsYou`（先等你、再可合并；可合并只有算过 `WtInfo` 的组才认得出），上一个 / 下一个是 `StepNeedsYou` → `GoToWorktree`。

展开着的工作树块里的工作树各有一个监听（`app/wtwatch.go`，`watch.StartTree`，最多 `WtWatchMax` 16 个），改了只刷新那一行。

### 子模块

`git.ReadSubmodules` 读 `.gitmodules` 和 `submodule status`。porcelain v2 的 `S<c><m><u>` 记在 `Change.Sub`。子模块在文件树里是一行（不展开），在 Git 栏里的 Diff 是一段说明（`subDiffNote`：指针从哪个提交到哪个、里面有没改动）。丢弃子模块的改动是 `submodule update`。右键能初始化、更新、在新窗口打开、加进工作区（`OpenSubmodule`）。

### 交互式变基

`app/rebase.go` 和 `docs.RebasePlan`。从历史里某个提交“从这里开始交互式变基…”打开一个向导标签：每行一个提交，选 pick / reword / squash / fixup / drop / edit、拖动排序、直接改信息。只从当前分支的完整历史里开（`RebaseView`），范围必须是一条线、没有合并提交、最多 `RebaseLimit` 100 个。开始时生成 todo：要改信息的（reword、squash 合起来的）写成 `pick` / `fixup` 加一行 `exec git commit --amend -F <信息文件>`，所以整个变基不开编辑器。todo 用 `GIT_SEQUENCE_EDITOR=cp <文件>` 换进去，经 `RunOpEnv` 跑。停在冲突上交给横幅，`followRebase` 跟着进度。

### 克隆

`app/clone.go`。对话框填地址、父目录（默认上次的 `cloneDir`）和目录名（`CloneName` 从地址推）；剪贴板里像 git 地址（`LooksGitURL`）就预填。`git clone --progress` 在父目录里经 `RunStream` 跑，stderr 一段段读成进度（`CloneProgress`），侧栏文件页顶上一行显示。克隆不属于哪个仓库的队列（仓库还没有），跟着窗口走（`a.Clones`），凭据提示和超时钟和写命令一样。日志里的地址先去掉用户名密码（`redactURL`）。克隆完加进列表（空工作区时当工作区打开）；取消时删除未完成克隆的目录。

### 远程网页

`git/web.go`：`ParseWeb` 把远程地址（ssh / https / scp 写法）转成网页地址，`KindOf` 认 GitHub / GitLab / Gitea / Bitbucket，自建的主机在 `Settings.WebHosts` 里配。`WebOf` / `WebRef` 拼出提交、分支、文件某行的链接。

### 普通文件夹

不是 git 仓库的文件夹（`Repo.Plain`）也能开：文件树用 `files.Walk` 遍历（最多 `WalkCap` 50 000 个），监听用 `watch.StartPlain`，Git 栏和历史里不出现；需要 git 的命令遇到 `git.NotRepo` 时禁用。
`Walk` 不进 `node_modules` 和点开头的目录（`.venv`、`.pytest_cache`……），把它们列成第四个返回值（名字后面带 `/`，同 `LsIgnored`），
`LoadFiles` 当被忽略的目录传给 `Tree.Done`：树里灰显、收着，展开时 `ReadLazy` 读一层，`Paths` 里没有它们里面的（⌘P、全文搜索不进）。
这一条不看"显示被忽略的文件"开关，`files.exclude` / "隐藏的文件"照样能藏掉；`.git` 不列也不进，`.DS_Store` 不列。
监听（`ignorePlain`）照旧不看这些目录里面，根下增删这样的目录是根目录的变化，整棵重读时跟上。

## 文件监听

`internal/watch`，每个仓库一个 `Watcher`，用 `mygo.WatchFiles` 的 HintsOnly 模式（只要“这里变了”的提示，不要事件列表）。看两处：

- 工作区，递归。忽略 `.git`、`node_modules` 和 git 忽略的目录（`git check-ignore` 的结果缓存 `ignoredTTL` 30 秒）。
- git 目录里的 `HEAD`、`index`、`packed-refs`、`refs/`，链接工作树还有 `worktrees/<名>` 一层。

变更提示按 `Debounce` 300 ms 合并，最长等待 `MaxDelay` 1 s，然后发一个 `Change`：工作区变了、git 变了，或者路径是 `.` 表示两样都变了。`Change.Dirs` 是变了的目录（祖先目录包含的子目录不重复列出，最多 `MaxDirs` 200 个，多了就 `Full`），文件树按它做增量刷新。仓库目录（或它的 git 目录）被删了或移走了，报 `ErrGone`；监听出错时 app 在输出栏记一条“文件监听停了”，之后靠切回窗口或手动刷新。

`StartPlain` 给普通文件夹（只看工作区），`StartTree` 给工作树块里的链接工作树。

## 文件树

### 模型

`files.Tree` 由 `git ls-files` 建（带模式位，`LsFilesModes`，认得出符号链接和子模块），未跟踪的文件来自 status。普通文件夹用 `Walk`。树里每个仓库是一段，段头贴顶；展开的目录记在会话 `dirs` 里。

文件树的右键菜单（`ui/sidebar.go` 的 `treeMenu` / `rowMenu`、仓库行、钉在顶上的行、压缩目录的每一段）是 Grove 自己画的（issue #48），不用系统菜单
（窗口靠下时会折成带滚动箭头的，见 `docs/mygo-pitfalls.md`），也不用 mygo 的 `PopoverMenu`（不能按住右键拖到项上松开、子菜单没有延迟和安全三角、
灰项说不了原因、点外面那一下会落到底下）：

- 状态机是 `app.PopMenu`（`app/popmenu.go`，每个窗口一个 `App.TreeMenu`），不碰界面：项由建菜单的函数经 `app.MenuSink` 收集（每帧重收，灰不灰跟着状态），
  位置、命中、子菜单延迟（`MenuSubDelay` 0.2 s）、安全三角、灰项提示（`MenuTipDelay` 0.5 s）、按住拖松开（`MenuHoldMin` 0.15 s 或动过）、键盘都在里面，
  `popmenu_test.go` 直接喂事件测（Tester 按不住右键）。选中一项后再跑一遍建菜单的函数，选中那项的 `Chosen()` 报真（和 mygo 的菜单一样）。
- `ui/gmenu.go` 喂事件和画：`treeMenuPress` 给树里的元素挂 `HandleInput`，右键按下打开菜单，按住时的移动和松开（归按下的那个元素）转给菜单；
  `gmenuView`（`root` 里、`pending` 之前）执行选中的项、画各层面板，开着时在 Overlay 里铺一层全窗口的接收层：点外面关菜单并吞掉这一下，
  滚动、Esc、失去焦点关，键盘也是它接。接收层盖着整个窗口，事件到不了底下的元素，所以 `treeMenuPress` 每帧把挂了菜单的元素
  （位置、`menuZ` 算的叠放次序、建菜单的函数）登记给 `PopMenu.AddTarget`：开着时在别处右键（`Down` 的 button 1，⌃点当右键），
  按登记的找到那一块（列表空白处兼作裁剪），关掉旧的、在指针处开新的；左键和别的键只关、吞掉。文件树有键盘时 ⇧F10 / 菜单键由
  列表的 `HandleInput` 接（`openTreeMenuByKey`，用 `TreeSpans` 里光标行的位置），`PopMenu.OpenByKey` 开、第一项高亮；
  没选就关了，`App.MenuByKey` 让键盘回到文件树。高亮项的底是实心强调色，白字不够 4.5:1 时往黑里调（`gmenuHot`）。面板是 `RoleMenu`、项是 `RoleMenuItem`，测试的 `shownMenu` / `chooseMenu` / `closeMenu` 照旧认。
- 建菜单的函数（`menus.go`）都收 `menu` 接口（`app.MenuSink`），系统菜单（`ContextMenu` / `Menu`）经 `nativeMenu(m)` 适配，所以两边共用；
  灰项说原因用 `whyItem` / `whySub`：系统菜单括在标签后面（`withWhy`），Grove 画的悬停提示。

刷新是增量的（`app/treediff.go`）：监听报的 `Change.Dirs` 交给 `fileHints`，后台只对这些目录重新 `ls-files`，`Tree.Diff` 算出加减，回主线程 `Tree.Apply`。变了的目录超过 200 个或 `Full` 时整个重建。后台计算期间若整棵树已被替换（`Gen` 变化），则丢弃结果。

几种特殊行的处理方式：

- 符号链接：`ReadLinks` 读目标，`Link{Target, Broken, Dir, Up, Deep}`。指向目录的链接能展开（懒读，`lazyLinks`），链接层数超过 `MaxLinkDepth` 8 层或指向祖先时不再展开。
- 被忽略的文件：默认不显示；`ShowIgnored` 打开时，被忽略的目录由 `LsIgnored` 列出（普通文件夹不看开关，是 `Walk` 不往里走的那些目录），并以灰色显示，展开时一层层读（`Lazy` / `ReadLazy`，一层最多 `FilterCap` 500 个）。
- 空目录：git 不记空目录，所以另外找（`FindEmpty`，最多扫 `MaxEmptyScan` 20 000 个目录，跳过被忽略的 `FindEmptySkip`）。大仓库里不一次扫完，展开时再找那一层（`app/emptylazy.go`）。
- 子模块：一行，不展开（`Row.Sub`）。
- 只有一个子目录的目录合成一行（`CompactRows`，`Row.Chain`），每段能单独点。

### 交互

- 筛选：输入框里打字按模糊匹配筛（`Files.Match`，`fuzzy.ScorePath`），只留最好的 500 个（`topHits` 用堆），命中的祖先目录自动展开。
- 跟随：切标签时在树里展开到那个文件、选中、滚到（`FollowCurrent`，设置 `TreeNoFollow` 关掉）；⌥⇧⌘E 手动定位（`RevealCurrent`）。
- 吸顶：滚动时仓库段头和最多 `maxSticky` 3 层祖先目录贴在顶上（`stickyStack`），最底下那层往下投阴影（`stickyShadow`，浅色同编辑器的粘性滚动，深色加深），不画边线。缩进参考线由 `Draw` 画。
- 多选（`FileChosen`）：⌘ 点、⇧ 点；拖动在 `TreeDrag` 里：同仓库是移动，别的仓库来的是复制（先问），按着 ⌥ 是复制；目录、多项、有没保存的标签时先问，写盘只在松手之后。悬停在收起的目录上一会儿会展开（`DwellTree`）。从访达拖到目录行上是复制进来（本来就在这个仓库里的是移动，`DropFiles`），拖到窗口别处是打开。复制逻辑由 `copyIn` / `copyDir` / `copyNode` 实现：`os.CopyFS` 遇到符号链接直接报错，这里链接照原样复制一个、普通文件保留权限位。
- 复制 / 粘贴 / 创建副本（`app/treeclip.go`）：重名时用 `freeName` 起“名字 副本”“名字 副本 2”。
- 新建、重命名、移到废纸篓在 `app/fileops.go`。文件树的 ⌘Z（`app/fileundo.go`）：重命名 / 移动改回去，新建 / 复制进来的移到废纸篓；移到废纸篓的不记（放回靠访达），没有重做；`undoStale` 发现磁盘在那之后变了就不撤。
- 悬停目录显示大小和最近改动（`app/dirtip.go`）：只 `Lstat` 树里列着的文件，第一次悬停时在后台算，有上限。
- 鼠标（issue #46）：没选中的文件 / 目录行没按 ⌘ ⇧ 的单击由行自己接（`ui.treePress`，`HandleInput` 接下按下、移动、松开，不交给列表）：
  按下那一帧就选中（`PressTreeRow`）、画"按下"的底色（`pressFill`，叠在选中的底上），按着拖到别的行是拖着选（`DragTreePress`，按 `TreeSpans` 找行，
  不会在列表边上自动滚），松开才打开文件 / 展开目录（`ReleaseTreePress`）；那次松开 mygo 下一帧还会报成这一行的单击，`TreeRowClicked` 按 `eat` 吃掉。
  `eat` 只管松开后紧接着的那一帧（`ApplyTreeClick` 每帧末尾清掉），不按时间算：按住多久再松开都只做一次（issue #50）。
  只有文件行认双击（固定打开，`treeDoubleClick` 500 ms 内同一行的第二下）；目录 / 仓库行不认双击，每一下单击都展开 / 收起（同 VS Code、Zed），
  行自己数的双击（第二下按下时报的 `DoubleClicked`）对目录不算，只看松开的 `Clicked`。
  ⌘ / ⇧ 点、双击、按在箭头上的照旧交给列表 / 箭头；已经选中的行照旧自己接（可拖）。右键不改选中（原来的多选留着），菜单只对那一行，
  那一行画 1 pt 强调色描边（`menuRing`：菜单开着、`TreeMenu.Owner` 是这一行，`menuOnRow` / `menuOwned`）。箭头不换手形光标。
- 行的悬停提示不用 mygo 的 `Tooltip`（0.6 s，写死在 mygo 里）：`app.TreeTip` 记指针从什么时候起停在哪一行，停满 `TreeTipDelay`（0.5 s）
  `ui/treetip.go` 才在 Overlay 里画，贴在行的下方（`AttachTo`，底下放不下自己翻上去）；出过一条以后换一行直接出（暖启动，指针离开所有行、
  按下 / 右键以后又要等），点了那行（`Dismiss`）指针离开前不再出，
  行里自己带提示的小图标（改动的点、链接图标）悬停时让给它们。名字被截断（`cutName` 量字宽）时提示第一行是全名。
- 仓库段头能拖动排序（`RepoDrag`），改的是工作区文件里 `folders` 的顺序（`workspace.Reorder`）。拖动和移除仓库在界面里只发请求（`MoveReq` / `RemoveReq`），`pending()` 在帧外统一处理，避免在一帧绘制过程中修改列表。
- 树里的行拖到编辑器组上是在那组打开（`OpenDropped`），拖到终端上是贴绝对路径，都不写盘、不问。别的应用拖进来的（`app/datadrop.go`）：拖到编辑器里插入文字或打开文件（`DropOnDoc`），拖到终端里插入路径（`DropOnTerm` / `ShellFiles`）。

### 外观

侧栏（文件树、Git 栏、搜索、大纲、打开的编辑器）的字号统一用 `ui/sidebar.go` 的常量：正文 `sideFont` 14（文件名、仓库名、变更、结果），次要信息 `sideSub` 12（分支、计数、目录、分组标题、说明），搜索结果的代码行 `sideMono` 12.5。文件树的目录 / 文件行至少 `treeRowH` 24 高，吸顶层和“打开的编辑器”的行跟它对齐；Git 栏的仓库段头 34（仓库名 `repoFont` 14.5）、分组标题、贮藏行和工作树组头 26、工作树行 28、展开的工作树摘要行 36。界面整体大小走界面缩放（⌘= / ⌘-），没有单独的侧栏字号设置。

文件按状态上色（改了、新加、冲突、被忽略），`TreeNoStatusColors` 关掉。图标默认是 `ui/fileicons.go` 的彩色小方块（按扩展名；文件树和"打开的编辑器"那一档 `iconTool` 用 `quietGlyphOf`：不画底块、颜色往次级字色混 `quietMix`，一屏几十个不那么重），`TreeIconsPlain` 换成线条图标；目录图标按里面有没有改动淡淡上色（`folderTint`）。

侧栏顶上可以加一段“打开的编辑器”（`ShowOpenEditors`，默认关，`app/openeditors.go`）：按组列出所有标签，最多显示 `OpenEditorsMax` 9 行高，多了滚；收起状态在 `Panels` 里。这一段不另存列表，每帧从 `AllDocs()` 现算。

## 编辑器

编辑器使用 mygo 的 `editor`；Grove 负责文档、标签、编辑器组及相关功能。

### 文档和标签

`docs.Doc` 表示一个打开的文档，`Kind` 有：文件、历史版本（只读）、Diff、交互式变基、替换预览、搜索编辑器、文件比较、编辑器区的终端、设置文档、终端输出文字，另有图片和十六进制两种显示。除了文件，别的都只在内存里。

标签规则（`app/tabs.go`）：

- 单击打开是预览标签（斜体），下一个预览会替换它；双击、回车、编辑以后固定。
- 读文件、历史版本和 diff 都在后台；写盘只在用户保存、或者在关标签 / 退出的确认框里选了保存之后。
- ⇧⌘T 重开关掉的标签，‹ › 前进后退（`Back` / `Forward`，⌃- / ⌃⇧-），同一个文件里跳行也算一步。
- 切当前组的标签（`app/tabswitch.go`）：⇧⌘[ / ⇧⌘]、⌥⌘← / ⌥⌘→ 按标签条顺序走、到头绕回（`StepTab`）；⌃Tab / ⌃⇧Tab 按最近使用走（`StepTabByUse`），顺序来自 `visit` 里记的 `tabMRU`（只在内存里，不存 state.json）。⌃Tab 是每按一次切到上一个用过的（连按在两个标签之间来回），还没有按住 ⌃ 时的标签列表浮层。键盘在终端里时这几个键先给终端标签（注册表里 `term.prevTab` 等排在前面）。
- 外部改了文件：没改过的标签直接重读；改过的标出冲突，让用户选保留自己的还是重读。

### 文件大小

| 大小 | 怎么开 |
|---|---|
| 小于 `MaxDiffBaseBytes` 2 MB | 正常，带改动条（和 HEAD 比） |
| 到 `MaxEditBytes` 8 MB | 可编辑，不与 HEAD 比较（`NoBase`，标签上显示提示），因为 mygo 在主线程上算这份 diff |
| 到 `MaxViewBytes` 64 MB | 只读打开（`Doc.Big`），后台高亮 |
| 更大 | 不打开，显示原因说明 |

读文件在后台（`docs.Loaded`，`fresh` 判断读回来时标签还是不是那一版），读好了 `setText` 换上。读分两步，在同一个后台 goroutine 里：先 `ReadFileText` / `ReadVersionText` 读文字、`a.Update` 放上（这时 `Doc.BasePending`），再 `Loaded.LoadBase` 读 HEAD（历史版本：父提交）里的那份、`ApplyBase` 放上；`Gen` 变了（重读了）的基准丢掉，重读时槽里先留着原来的基准。新开的文件 / 历史版本标签（`Loading`）主线程在 `readDoc` 里最多等 `quickLoad`（40 ms）：后台读完先试着把"放上去"经无缓冲 channel 交给等着的主线程，同步放上（预览换文件时编辑区不空一帧，issue #50）；没人等了（超时）就照旧走 `a.Update`。基准不在这一步等，还是后台接着读。标签读了 `loadingDelay`（150 ms）还没读完才置 `Doc.SlowLoad`、画"读取中…"，在那之前只露编辑区的底。只读的大文件外部改了，过 2 秒再重读（`reloadBig`），避免日志持续写入时频繁重读。

大文件的高亮在 `internal/highlight`：mygo 自带的高亮器在主线程上每帧追 4 ms，64 MB 要追两三秒。这里一个 goroutine 从头分词、记下每行开头的状态，主线程只按记好的状态分看得见的几行；没算到的行先按状态 0 猜一份（`final` 为假）。`Highlight` 总是返回 true；编辑时停掉旧的 goroutine，从改动那行接着算。

### 编码

`docs/encoding.go`。读的时候自动认（`Decode`）：带 BOM 的 UTF-8 / UTF-16 去掉 BOM；没 BOM 的 UTF-16 看奇偶位的 NUL（`sniffUTF16`）；有 NUL 的算二进制；合法 UTF-8 原样；否则在 GB18030、Big5、Shift-JIS、EUC-KR 里挑（`pickCJK`，阈值按 `encoding_test.go` 的样本调过）；都不行是未知，当 UTF-8 只读。

老编码（GB18030、Big5、Shift-JIS、EUC-KR、EUC-JP、Windows-1252、ISO-8859-1）要编回去和原来逐字节一样才能编辑（`Writable`，不一样标 `Lossy`）；保存按原编码写，有写不进去的字先问。手动“用别的编码重新打开”（`ReopenWithEncoding`）解不通的标 `Broken`、只读；选的编码记在会话 `Tab.Enc`。“转成 UTF-8…”（`ConvertUTF8`）换掉编码，下次保存按 UTF-8 写。

缓冲区里的换行一律是 `\n`，写盘时按原来的换回 CRLF；状态栏能换整个文件的换行（`Doc.SetEOL`）。

### 保存

保存前的整理（`docs/tidy.go`，设置里两个开关 `TrimTrailing` / `FinalNewline`）：去行尾空白、补末尾换行，作为一步撤销；光标所在的行不去，Markdown 里行尾正好两个空格的硬换行留着。

自动保存有三种模式：关闭、窗口失去焦点时（`blur`）、停止输入 1 秒后（`delay`）。所有文件写入都通过 `WriteDoc` → `diskWrite`，本地历史也在这里记录（见“本地历史”）。

### 格式化和检查

格式化和检查均调用外部命令，不内置工具，也不通过 LSP。命令在 login shell 里跑（`Shell` 设置或 `$SHELL`，`-lc`），这样 Homebrew 装的工具在 PATH 上；工作目录是仓库根，仓库外的文件是文件所在目录；超时整个进程组一起杀。

格式化（`app/format.go`）：⇧⌥F、右键或保存时（`FormatOnSave`）。将缓冲区的 UTF-8 文本传入命令的 stdin，读 stdout，和原文按行比（`docs.LineEdits`，每处收窄到真正不同的那几个字），仅应用有变化的部分，作为一次可撤销操作，并保留光标、折叠和滚动位置。运行期间文本再次变化时，不应用结果。默认命令在 `DefaultFormatters`（按 `editor.Language.Name`），`Settings.Formatters` 能改，`"-"` 关掉；`{file}` 换成引号括好的绝对路径。

检查（`app/lint.go`，界面在 `ui/problems.go`）：保存时（设置“格式化”页的“保存时检查”，默认关）或手动“检查当前文件”。默认命令在 `DefaultLinters`（go vet、tsc、eslint、ruff、cargo check、shellcheck），`LinterOf` 先看设置里的 `Linters`；`{file}` / `{dir}` 两个占位符，`LintTimeout` 60 秒。它读的是磁盘上的文件，所以保存那一刻记下缓冲区的版本（`snapOf`）。输出按 `路径:行:列: 信息` 解析（`ParseProblems`），经 mygo 编辑器的 diagnostics 标在行上（`SyncProblems`：先清掉，再按记下的版本设；对不上版本就用现在的文字重算位置），底栏第三个标签“问题”列出所有仓库的结果（按仓库、文件、行排），状态栏显示错误和警告的数目。

### 大纲和粘性滚动

`internal/outline`：Markdown 标题；Go 用 `go/parser`；Python / JS / TS / Rust / Shell / YAML 按行用正则认；C / C++ / Java / Kotlin / Swift / C# 按花括号嵌套认（`brace.go`）。`Parse` → `Build` 摊平成 `Tree`，`At(line)` 找光标所在的符号，`Symbol.End` 是到哪行结束。超过 `MaxBytes` 1 MB 不算，最多 `MaxSymbols` 2000 项。侧栏的大纲模式在 `ui/outline.go`。

粘性滚动（`ui/sticky.go`）把光标上方还没结束的符号的头一行叠在编辑器顶上，最多 `StickyMax` 5 行（`Tree.Sticky` 算哪几个）。超过 `StickyQuick` 256 KB、或上次解析超过 `StickySlow` 1 ms 的文件，打字时先用旧的大纲，停手 `StickyPause` 500 ms 才重算，避免打字时掉帧。

### 跳转和命令面板

⌘P 的面板（`app/palette.go`）按前缀分模式：

| 输入 | 内容 |
|---|---|
| 文字 | 所有仓库的文件（`fuzzy`） |
| `名字@符号` | 某个文件里的符号（`app/palettesym.go`） |
| `@` | 当前文件的符号 |
| `#` | 工作区符号（`app/symindex.go`） |
| `>` | 命令 |
| `>git ` | 跑一条 git 命令 |
| `>term ` / `>snip ` | 用某个终端配置开新终端；插入代码片段 |
| `!` | 任务 |
| `:行` | 跳到行 |

工作区符号的索引第一次用时建：后台几个 worker 读盘、解析，分批交回主线程；之后监听报的目录标脏，下次用时只重新 stat 脏的、版本变了才重新解析。开着的文件用标签的大纲（没保存的改动也算）。索引不常驻、不存盘，每个窗口一份，最多 20 万个符号。

### 编辑器组

`app/groups.go`。主区能左右 / 上下分成几组，可以嵌套，每组一条标签条。标签条（`ui/tabs.go` 的 `tabStrip`）和底栏的终端标签（`ui/bottom.go`）共用 `scrollStrip`：横向滚动、自己在底边画 2 pt 的细条，
滚动位置分别在 `Group.TabStrip` 和 `Repo.TermStrip`，换了当前标签滚到看得见（`Group.TabShown` / `Repo.TermShown`）。每个标签是 `panelTab`：
通栏平铺、同高 `headerHeight`、没有圆角，右边一条 `cardLine`，当前的用卡身底色盖住卡片头底下那条线；第一个贴着卡片左边（卡片头左 padding 0）。拆分树是 `EdNode`（叶子是一组，否则是左右或上下的两半和比例），存进会话的 `layout`。

当前有焦点的组，其标签保存在 `a.Docs` / `a.DocSel` 里，所以“当前标签”的代码不用管组；别的组的标签放在组自己那里，换组时两边对调（`swapTo`）。要遍历所有标签用 `AllDocs()`。

一个标签同时只在一组里：拖标签、⌘\ 都是搬过去。拖标签时落在一组的中间是搬进来，离某条边不到 1/4 是在那边分出新的一组（`DropSide` / `DropRect`）；一组不到两倍最小尺寸（160×120）就不让分。组内最后一个标签移走后，收起该组（`collapse`），由相邻组填满空间。

同一个文件要在两组里同时看，用镜像标签（`app/mirror.go`，⇧⌘\）：和正本共用缓冲区和对比基准，光标、滚动、折叠和查找状态各自独立。读盘、保存和切换编码等操作只对一份文档执行，再通过 `relink` 同步到其他镜像。打开文件总是找正本（`Copy` 为 0）。

最大化底栏（`app/bottommax.go`，⇧⌘J）：`App.BottomMax`，每个窗口独立、只在内存里；`ui/view.go` 的 `center` 在 `BottomMaxed()` 时不画主区和底栏旁边的拖条，底栏 `Grow(1)` 占掉那块，`St.BottomH` / `St.TermW` 不动。关底栏（`ToggleArea` / `ToggleProblems`）、进禅模式、切到或打开编辑器标签（`SelectDoc` / `FocusGroup` / `MoveDocTo` 里的 `showEditor`）时还原。

禅模式（`app/zen.go`，⌃⌘Z）：三个面板全关，编辑器组居中；每个窗口独立，不持久化，存 state.json 时写进入前的面板状态。连按两下 Esc 退出。

### 其他

- 状态栏的语言、缩进、换行、编码都能点（`app/docstatus.go`），改了的语言和缩进只对这个文件、记进会话。
- 代码片段（`internal/snippets`）：内置的加状态目录里的，Tab 展开，Tab / ⇧Tab 跳位。
- 图片：预览、和 HEAD 的图片 Diff；Markdown 和 SVG 用 ⇧⌘V 在源码和预览之间切（`TogglePreview`）；`app/image.go` 的 `ToggleHex` 切十六进制视图，也能用默认应用打开。
- 键盘移动光标后，编辑器让光标上下各留 min(5 行, 视图高 1/5)（`ui/caret.go` 的 `caretMargin`）。mygo 自己只留 3 行；点击和滚轮不受影响。
- 光标在标识符上（没有选区、只有一个光标）停稳 250 ms（`ui.OccurRest`）后，本文件里同名的其他出现（整词、区分大小写）铺上 mygo 的 `Occurrence` 底色（`ui/occur.go` 的 `occurMarks`，写进 `St.TextBackgrounds`，状态在 `Doc.Occur`）；打字、换词马上清掉。mygo 自己只在有选区时标选中文字的其他出现，也没有"光标下的词"这个选项，所以用 `TextBackgrounds` 在 Grove 这边做；按内容版本和词算一次，大于 `StickyQuick` 的文件只搜看得见的行、滚了重搜。搜索编辑器（底色是搜到的那几处）和十六进制视图不标。
- 选中里画出来的空白符号（点、箭头）用 `TextMuted` 的 0.22（mygo 默认 0.45，`ui/occur.go` 的 `codeTheme` 设 `St.Theme.Whitespace`，别的代码颜色留零、照旧跟 mygo 的默认）。
- 编辑器和 Diff 中的光标在停止操作一段时间后不再闪烁（`ui/caret.go`，`WORKBENCH_CARET_REST=off` 关掉）。mygo 的编辑器有键盘时每 500 ms 排一帧闪光标，而 mygo 要 2 秒没有新帧才放掉整窗的帧内存，因此持续闪烁会额外占用一两百 MB 内存，并持续消耗 CPU。
- Diff 有并排和统一两种，全文 / 只看改动两种（`unified` / `fullDiff`）。
- 两个磁盘文件、或文件和剪贴板比较：`app/filecompare.go`（先“选择以比较”，再“与已选的比较”）。

## 搜索和替换

### 搜索

`internal/search` 只负责搜索计算。候选来自 `git ls-files`（或文件树已经读到的列表），减去 `files.exclude` 和包含 / 排除 glob；每个 CPU 一个 worker。字面 / 整词 / 正则、区分大小写都在一个 `Matcher` 里。

- 默认跳过二进制和超过 `MaxFileSize` 2 MB 的文件；勾“也搜大文件”放宽到 `MaxBigFileSize` 32 MB，勾“也搜被忽略的”加上被忽略的文件（`Query.Big` / `Ignored`）。
- 最多 `MaxMatches` 5000 处，“再读 5000”往上加，到 `MaxLimit` 50 000 为止。
- 不是 UTF-8 的文件按 `docs.Sniff` 认编码、解成文字再搜，所以位置就是编辑器缓冲区里的位置（`Match.Enc`）。
- 多行的字面词转成正则，换行写成 `\r?\n`。`Match.Len` 只到行尾，`Match.Span` 是整个匹配的长度，`CR` / `BufLen` 用来把 CRLF 文件的字节位置换成缓冲区位置。
- 正则先用 Go 的 RE2；编不了又用了反向引用或环视时换 `regexp2`（`advanced.go`），一次匹配限时 2 秒（`advTimeout`），超时的文件当跳过、结果上标出来（`Result.TimedOut`）。

应用端（`app/search.go`）：停止输入 150 ms 后开始搜索，新搜索会取消上一次搜索；结果按文件分组。完成一批搜索后调用一次 `debug.FreeOSMemory()`，把候选列表和读过的文件内容的内存还给系统。

- 最近搜索 `SearchHistory`（最多 20 条，带开关和 glob），框里 ↑↓ 翻。
- 范围：右键目录“只在这个目录里搜”（`SearchScope`，`Root.Sub`）。
- 排除：结果里点 × 去掉一处或一个文件（`DismissMatch` / `DismissFile`），替换时跳过。
- 跟随：结果里的文件改了，停 `FollowDelay` 300 ms 只重读这些文件再搜（`app/searchfollow.go`），不整个重搜。
- ⇧⏎ 把搜索框换成多行的（`multiLine`）。
- 搜索编辑器（`app/searchdoc.go`）：把结果开成一个只读的文本标签，带上下文行，回车跳到那一处。

### 替换

`search.Plan` 先核对每一处还在原位（文件可能刚改过），再算出改动；正则替换里的 `$1` 由 `Expand` 展开。`app/replace.go` 的 `ReplaceMatches`：开着的文件改缓冲区（能撤销、不存盘），没开的直接写盘（经本地历史）。替换后不重新搜索，而是在内存中调整结果位置（`splice`），被排除的也跟着挪（`moveDismissed`）。替换操作记录在输出栏。

替换预览（`app/replacepreview.go`）：打开一个 Diff 形式的标签，左侧为原文，右侧为替换后的内容；预览开着时文件又改了，标出“过期”并能重生成。“撤销上次替换”（`app/replaceundo.go`）只处理直接写入磁盘的文件：记下替换前的内容（一次最多 `MaxUndoReplace` 20 MB，超出时本次替换无法撤销），撤销时只恢复替换后未再次改动的文件。只记录最近一次操作，不持久化；已打开的文件通过编辑器的 ⌘Z 撤销。

## 终端

### 标签和分屏

`app/terminals.go`。每个仓库一排终端标签，底栏显示当前仓库的那一排；每个标签是一格或分屏的几格（`TermNode` 拆分树，最多 `MaxTermPanes` 8 格），每格一个 shell。空工作区时终端开在家目录，不记进会话。

shell 按需启动：首次需要显示（底栏开着、当前仓库是它、是它当前的标签）才起（`EnsurePane`）。切仓库再切回来还是同一个 shell。标签的名字、顺序、分屏、每格 shell 所在的目录（OSC 7 报的）记进会话。

终端里有没有程序在跑（关标签、关窗口要不要问）：用 `ps` 看 shell 的前台进程组（`Job()`）；目录没报 OSC 7 时用 `lsof` 看 cwd。

按键：上下文 Terminal 里的命令（新终端 ⌃⇧`、分屏、查找、清屏……）走注册表；别的键交给终端（`Options.OnKey`）。设置里有 ⌥ 作 Meta、选中即复制、光标闪烁、Ghostty 主题名。终端配置（`TermProfiles`，最多 10 个）是 shell 加参数，第一格用了配置时标签名跟着它。

终端里 ⌘ 点击（`app/links.go` 的 `OpenLink`）：文件路径（绝对、`~/`、相对终端当前目录，`file://` 也算）在编辑器里打开，带 `:行[:列]` 就跳过去；仓库里的目录在文件树里显示；别的网址交给系统浏览器。shell 报的目录可能是解开符号链接的（`/var` → `/private/var`），对不上时两边都解开再比。

终端标签也能开在编辑器区（`app/termeditor.go`，⌃⇧⌘`）：一个 `TermTab` 要么在底栏（`Repo.Terms`），要么在编辑器区（`Repo.EdTerms`，对应一个 `docs.Term` 标签）。移动时不重启 shell，只改变显示位置。

终端没有键盘选区，所以有“把终端内容开成标签”（⇧⌘C，`app/termtext.go`）：把一格的屏幕和回滚开成只读标签，在编辑器里选、搜、复制。内容是打开时的快照，可通过横幅刷新。

### 任务

`internal/tasks` 认仓库根上的 package.json scripts、Makefile / justfile 的目标、Cargo.toml / go.mod 的常用命令（最多 200 条，`Stamp` 按那几个文件的大小和修改时间决定要不要重读）。⇧⌘B 或面板 `!` 选一个，在那个仓库里一个以任务名命名的终端标签里跑；文件树里 package.json 这些的右键也能跑。最近跑过的记在 `TaskHistory`（20 条）排前面。

### agent 状态

终端里跑着 claude、codex 这类 agent 时，按格算它的状态，再按工作树汇总（`app/agents.go`）。全在本机算，只看 Grove 自己的终端，不读 agent 的日志、不接它的接口。

- 等你（`AgentWaiting`）：终端响铃（BEL）或要通知（OSC 9 / OSC 777），而键盘不在这一格（`paneLooked`）；记在 `TermPane.bellAt`，用户切进这一格（`FocusPane`、`GoToPane`）时清。认序列靠 mygo 终端的 `OnBell` / `OnNotify`（OSC 0/2/7/52 里当结束符的 BEL 它不算）；termd 的会话在无头终端上接这两个回调，发 `frameBell` 给窗口（没窗口接着时记下，接上时补一帧），窗口端是 `Conn.OnBell`。
- 运行中 / 空闲（`AgentRunning` / `AgentIdle`）：前台命令在 agent 名单里（`DefaultAgents` 加设置的「agent 命令」），最近 10 秒（`agentQuiet`）有输出算运行中，否则空闲。前台命令由后台 goroutine（`agentPoller`）每 2 秒用一次 `ps -A -o pid,pgid,tpgid,args` 读所有开着的格（node / bun / python 跑的脚本按脚本名）；读到的和上一轮一样就不回主线程，免得空闲时每 2 秒多一帧。输出时间：termd 的格每来一帧屏幕记一次（`Conn.OnOutput`，原子量），本地的格没有输出回调，前台是 agent 时看屏幕文字的指纹变没变。
- 终端 → 工作树：格现在的目录（`paneDir`：OSC 7，没有时上次 lsof 读的）落在哪个工作树里（`git.Under` / `SamePath`，套在里面的别的工作树归那个工作树）。`WtAgent(r, w)` 取最需要你的一格（等你 > 运行中 > 空闲），带上开始运行 / 等你的时间和是哪个终端；`GoToPane` 切到那一格（底栏的打开底栏、切仓库和标签，编辑器区的切到那个标签）。
- 界面：终端标签（底栏和编辑器区）的图标后面，等你画 7px 琥珀色圆点，运行中画 13px 的灰色转圈（`ui/agents.go` 的 `agentMark`），空闲和没有 agent 不画。

### 回滚内容的保存

未启用后台进程时，shell 随窗口关闭而结束。为便于重开后查看原有输出，关闭窗口、退出或切换工作区前，每个启动过的分屏都会将回滚内容（`Terminal.History`，VT 序列）写入 `<状态目录>/termhist/<工作区哈希>/<毫秒>-<n>.vt`，文件名保存在会话中对应分屏的 `hist` 中。下次启动该分屏时，先将内容输入新终端，并在前面加一行淡色分隔；读取后删除文件。每个分屏最多保留 `TermHistCap` 256 KB（从行首截取），整个目录上限为 `TermHistMax` 20 MB，超出时删除最早的文件。

### termd

设置里开了“关掉窗口后终端继续在后台运行”，新建分屏的 shell 会在终端后台进程 termd 中运行，窗口里的终端只是镜像。

- 进程：就是 Grove 自己带 `termd` 参数（`Grove termd <sock> <log>`），`main` 第一行分出去，不起 `mygo.App`。由 `termd.Spawn` 用 `Setsid` 起，等它能连上。
- 套接字：状态目录里的 `termd.sock`，0600，不做别的认证（只有这个用户能连）。路径太长（unix 套接字有长度限制）时换成 `$TMPDIR/grove-termd-<sha1>.sock`。同时起了两个时靠 `<sock>.lock` 的 flock 只留一个。一个状态目录一个 termd，所以 `WORKBENCH_STATE` 的实例有自己的。
- 会话：由一个 pty（自行实现的 `pty_darwin.go`，使用 `/dev/ptmx` + `ioctl`）、一个 shell 和一个无头 `terminal.Terminal` 组成。shell 输出传给无头终端，窗口中的终端通过 `terminal.Options.Conn` 获取镜像帧，保持屏幕和回滚内容一致。仅在窗口端尺寸实际变化时发送大小（`ResizeByConn`）。每个会话同时只允许一条连接，新连接会替换旧连接，另一个窗口会显示“被别的窗口接走了”。
- 断开和关掉：关窗口、⌘Q、换工作区是断开，会话留在 termd 里，会话记下 sid，再开同一个工作区接回去；关格、关标签、移除仓库、重新启动是关掉会话（shell 收到 SIGHUP）。
- 响铃：会话的无头终端响铃或要通知时发 `frameBell`（镜像帧里没有），见上面“agent 状态”。
- 退出：shell 退出了、又没窗口接着的会话过 `Linger` 10 秒拿掉；没有会话也没有连接 60 秒后 termd 自己退出、删掉套接字。SIGTERM / SIGINT 挂断所有 shell 再退。
- 设置窗口终端页显示 termd 在不在跑、几个会话（`TermdStatus`），能“停止”（`StopTermDaemon`）。关掉设置只影响新开的格。

## 多窗口和生命周期

### 多窗口

`app/windows.go`。一个窗口一个工作区、一个 `App`；同一个工作区只开在一个窗口里，再开就把那个窗口置于前台。`Windows` 是开着的工作台窗口的注册表（设置、关于窗口不算）。每个窗口有个槽位 `Slot`，决定它的 `StateKey`（1 是 `main`，2 是 `main-2`……，关掉的槽位新窗口先用），mygo 按 `StateKey` 记窗口位置。几个窗口共用一个 `workspace.Store`，跟着进程走的东西（设置、最近的工作区）改了由 `syncShared` 同步到其他窗口。

同一仓库在两个窗口中打开时，各自拥有独立的监听和终端。

外面交进来的路径（命令行参数、第二个实例转过来的、`open -a Grove`、拖到程序坞图标上）都进 `Route`：工作区文件开新窗口（开着就置于前台）；文件和目录交给前台窗口的 `OpenArgs`。`OpenArg` 对目录的处理：已在工作区里的仓库跳到那一段；不在的先问要不要加进来（只记进会话，不改 `.code-workspace`）；空工作区的窗口直接把目录当工作区打开。

### 启动

1. `cmd/workbench/migrate.go` 的 `migrateDirs`：在第一次调 `mygo.App.Path` 之前把旧名字的状态目录搬过来。
2. `crash.Setup` 设日志目录：标准库 `log` 写 `workbench.log`（满 1 MB 轮换）；`debug.SetCrashOutput` 指向 `crash-pending.log`，下次启动时如果它不空，就改名成 `crash-日期-时间.log` 留下（最多 10 份），`LastCrash` 记着，状态栏提示一句（`Greet`），关于窗口里能打开。没有崩溃时，`Greet` 在版本变化后提示新版本号（上次的版本记在 `appVersion`）。
3. 单实例：`mygo.App.RequestSingleInstanceLock`。获取锁失败时，将参数交给已运行的实例后退出；已运行的实例在 `OnSecondInstance` / `OnOpenFile` 里收到，`deliver` 去重（2 秒内同样的路径只算一次）后交给 `Route`。设了 `WORKBENCH_STATE` 的实例默认不拿锁（测试和多开），如需获取锁，设置 `WORKBENCH_SINGLE=1`。
4. `WindowStates` 读 `window-state.json`，要在 `mygo.App.Run` 之前设好。
5. `Windows.Restore` 开回上次退出时开着的窗口（`windows`）。每个窗口启动时需要数块整窗大小的帧内存，同时启动会叠加内存峰值，因此按队列依次恢复：前一个画完第一帧（`OnReadyToShow` → `Ready`）再隔 `RestoreGap` 2.5 秒开下一个。只开一个窗口时不排队。

启动用时记在日志里（`app/ready.go`）：第一个窗口的仓库都读完时，每个仓库一行“就绪”（状态、树、空目录、历史各自的用时），最后一行“全部就绪”从进程启动（`Launched`）算起。`WORKBENCH_ALLOPEN=搜索词`（`app/allopen.go`）是量内存用的“全开”：仓库读完后开两个 Diff、搜一次、起当前仓库的终端，然后打一行日志。一次搜索、关掉一个窗口这类大批释放之后调一次 `debug.FreeOSMemory()`，把内存还给系统。

### 关窗口和退出

关闭窗口时：`OnClose` → `CanClose`，存在未保存文件、运行中的 git 写命令或终端程序时要求确认（`RequestLeave`）；确认完成后关闭窗口，`OnClosed` → `Closed`：记下终端目录（`NoteTermDirs`，shell 没报 OSC 7 的用 `lsof` 读）和回滚、存会话、关掉这个窗口的渲染器。

退出（⌘Q、程序坞、关机）：mygo 先发送 `OnBeforeQuit`，未被拦截时依次关闭窗口；若中途有窗口拦截，已关闭的窗口不会恢复。因此，所有退出确认都在 `OnBeforeQuit` 中拦截处理（`RequestQuit`），逐个窗口询问，确认并关闭后再处理下一个，全部关闭后再次退出。中途取消时停止流程，已关闭的窗口不再恢复。`freeze` 在退出或关最后一个窗口时记下开着的窗口（还原中的连同还没开的），之后一个个关掉也照这个存进 `windows`；`quitting` 记录正在确认的窗口，`ending` 表示确认已完成，不再拦截退出。

### 设置窗口和关于窗口

设置窗口是第二个 `mygo.Window`，关于窗口是第三个（460×600，`StateKey` 是 `about`）。两者各只有一个实例，属于打开它们的工作台窗口；该工作台窗口关闭时一并关闭（`closeAux`）。设置窗口和工作台窗口共用一个 `App`，所以设置窗口里的改动走的也是那个 `App` 的方法。

### 版本

`internal/version`：`App` 是 `mygo build` 用 ldflags 编进来的 `mygo.json` 里的版本，`go run` 和测试里是 `dev`；`TestVersions` 核对 `mygo.json` 和 CHANGELOG 对得上。日志第一行是 `version.String()`（应用版本、mygo 版本、Go 版本、系统）。

## 外观、语言和快捷键

### 窗口底和颜色

窗口底（`Settings.Backdrop`）三种：柔和（默认）、纯色（`flat`）、通透（`vibrancy`，再选系统材质 `material`）。柔和是纯色底上用 `Draw` 画几团很淡的色斑（`paintBackdrop`，Shadow 的模糊），不依赖桌面壁纸；通透在默认壁纸上看起来是平的、HUD 一类材质会把字冲淡，所以不是默认。

各区域是圆角卡片（`ui/widgets.go` 的 `card`），卡片之间 6 pt 的缝就是拖条（`resizer`）。标题栏 38 pt 高，按红绿灯的位置排（`TitleBarInset`）。

强调色在 `ui/accent.go`：默认 `steel`（`DefaultAccent`），`applyAccent` 换整套；列表选中默认是强调色淡淡的一层加左边一道竖条，列表没有键盘时换成中性的灰、不加竖条，悬停更淡（`listLook` / `softFills`：建列表的主题里 Accent 透明，选中块的底由行自己按 `ListFocused` 画；`SolidSelection` 换成实心块），`onAccent` 是强调色上的字色。尽量减少视觉装饰：横幅、悬停这些用很淡的填充（`quietFill` / `bannerFill`），不加边框。

界面缩放（`Settings.Zoom`，按 `ZoomSteps` 一档档走）是每个窗口每帧 `c.SetZoom`，设置窗口、关于窗口也跟着。终端自带的字号键（⌘= ⇧⌘= ⌘- ⌘0）让给界面缩放（`termZoomKeyFor`），终端字号只在设置里改。

### 应用图标

`internal/appicon` 用有向距离场现画（`Draw(size)`），任意大小都抗锯齿。`assets/icon.png`（`mygo build` 用的 1024×1024）由它生成：改了画法跑 `go test ./internal/appicon -update`，`TestIconFile` 核对两者一致。

### 语言

所有界面文案都通过 `i18n.T` / `Tf` / `Tn`（复数）/ `List`（列举的连接词）。原文是代码里的中文；`zh-CN.json` 由 `TestCatalog -update` 从代码里收集（所以文案要写成字面量，不要拼接），`en.json` 是英文。英文的单复数写成 `键|1` 这样的变体（`PluralKeys`）。设置里选语言（`Settings.Lang`：空是中文，`en` 是英文），`i18n.Set` 切换；mygo 自带的文案（编辑器的查找条、系统菜单、对话框按钮）通过 `SetLanguages(i18n.Locale())` 同步语言。

无障碍：每个可点击元素都要有唯一标签（见“开发约定”），`TestAccessLabels` 检查。

### 快捷键

见“App 和命令注册表”里的“自定义快捷键”。

## 本地历史

`internal/history` 只负责存储：`Store{Dir, Now}`，每个文件一个目录 `<状态目录>/history/<路径 sha256 的前 12 位>/`，里面 `index.json` 加内容文件（按毫秒时间 `T` 命名）。每条有 `Why`：保存、保存前磁盘上的、恢复前、改名自。

- 和上一条“保存”隔不到 `MergeWindow` 10 秒就替换它，避免自动保存产生大量记录。
- 超过 `MaxSnapshot` 256 KB 的文件不记，每个文件最多 `MaxEntries` 50 条，全部加起来最多 `MaxTotal` 200 MB。
- `Prune(days)` 删过期的（默认 30 天，`historyDays`）、超总量的从最早删起；启动后 `PruneDelay` 30 秒在后台做一次。`noLocalHistory` 整个关掉。

`app/localhist.go` 决定什么时候记。文件写入统一通过 `diskWrite`（保存、全部保存、格式化后保存、自动保存、转成 UTF-8、替换都走它）：写之前磁盘上那份还没记过就先记一条“保存前磁盘上的”（第一次保存，或者别的程序改过），写成功后记写进去的那份。状态目录里的、`.git` 下面的、图片、`files.exclude` 藏掉的不记（`keepsHistory`）。应用里的重命名和移动跟着挪历史（`localMoved` → `Store.Move`）。

右栏的文件历史里，本地历史的条目和提交按时间混排（`Hist.Local`）。点一条开比较标签（`OpenLocalCompare`，左边是那一条、右边是现在的文件，`docs.Doc.Snapshot` 记着是哪条），能恢复（`RestoreSnapshot`，恢复前先记一条“恢复前”）。误删文件可通过“从本地历史恢复文件…”找回（`RestoreDeleted`）。

## Markdown 渲染

Markdown 预览由 mygo 画，Grove 补充 mygo 无法绘制的三类内容：Mermaid 图和 TeX 公式（mygo 的 SVG 解析器画不了文字），还有带图片的表格（mygo 的单元格只排字，图片显示成 `[说明]`）。

带图片的表格在 `ui/mdtable.go`：预览挂 mygo 的 `Options.Block` 钩子（源码里有 `|` 和 `![` 才挂），碰到单元格里有图片的表格自己搭——列等宽铺满一栏，全空的表头行不画，每个单元格是一个 `ShareSelection` 的小 `markdown.View`（选项存在表格元素的 `ui.Local` 上，图片缓存才留得住）；`cellSource` 给开头像块标记的字加反斜杠，免得单独解析成标题、列表。别的表格照旧给 mygo。

`internal/render`：每个窗口一个 `Renderer`。预览每帧来取（`Get`），命中缓存时直接返回，否则加入队列，暂时使用该图在主题 / 缩放变化前的版本；没有旧版本时显示代码块或 TeX 原文。由一个后台 goroutine 依次渲染，完成后通知界面重绘。缓存最多 512 张 / 32 MB，队列 128 条（队列满时丢弃最早的请求，优先渲染新请求）。空闲 60 秒后销毁后端，下次需要时重新创建。源码本身的错（`SourceError`）只是这一张画不了；别的错当后端坏了，关掉重建。

后端（`web.go`）是一个看不见的 `mygo.WebView`，跑 `page.html` 里的 mermaid.js 和 MathJax，画到 canvas 上取 PNG。资源 embed 在二进制里，经自定义协议 `wbrender://` 给 WebView。Mermaid 用 `htmlLabels: false`，标签画成 SVG 文字，canvas 才取得到。

`app/render.go` 的 `a.Renderer()` 懒建；设置 `NoDiagrams` 关掉，只显示代码块。`ui/mdrender.go` 的 `mdRender` 把它接到 mygo 的 Markdown 视图上：位图的键里带主题和 `Gen()`，大小按界面缩放乘 `ZoomFactor`；`$…$` 行内公式靠 `ExtDollarMath`。预览里的链接（`app/markdown.go` 的 `MarkdownLink`）：`#标题` 滚到那个标题；http、https、mailto 交给系统；别的当文件路径（相对文档所在目录，可带 `:行[:列]`）在编辑器里打开，目录在文件树里显示；别的协议不开。

## 开发约定

新增代码需遵守以下约定：

- 界面只读状态；改状态走命令（`a.Exec(id)`）或 `App` 的方法。后台 goroutine 只经 `a.Update` 回主线程，开始前先拿好 `up := a.Update`（`switchTo` 会重写 `*a`）。
- 新动作先在 `app/commands.go` 登记，菜单、快捷键、面板、提示、快捷键文档都从这里来。改了键跑 `TestShortcutDoc -update`。
- git 的读命令带 `GIT_OPTIONAL_LOCKS=0 LC_ALL=C`；写命令非交互、有超时、经每个仓库的队列（`RunOp`）。
- 只在用户明确操作时写磁盘或改仓库：刷新、监听、打开、切换都不写。文件写盘只经 `WriteDoc` → `diskWrite`。
- 危险操作（丢弃改动、强推、硬重置、删除分支等）通过 `AskDanger` 确认，使用红色按钮；完整名单见 `app/danger_test.go`。
- 对话框只用 `ui/dialogframe.go` 的 `dialogFrame` 和它的 `dlgButton` / `dlgField` / `dlgInput` 等，样子统一。
- 每个可点击元素的标签（Label）需在窗口内唯一，供测试和无障碍功能使用：同名的加上仓库或位置，写法照现有的来（`名（仓库）`、`标签 x`、`编辑 x`、`分支 x`、`位置 仓库/路径`、`打开 …`、`命令 …`）。
- 文案一律 `i18n.T` / `Tf` / `Tn`，写字面量；跑 `TestCatalog -update` 收进 `zh-CN.json`，再补 `en.json`。
- 所有仓库同时可见：不要加“当前仓库 + 切换器”式的列表。
- 控制内存占用：大列表按需计算、后台读取；批量操作后调用 `debug.FreeOSMemory()`；渲染后端、termd 等组件空闲后关闭。

完整开发约定见 `AGENTS.md`，mygo 的使用注意事项见 `docs/mygo-pitfalls.md`，测试方法见 `docs/testing.md`。
