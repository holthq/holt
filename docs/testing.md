# 测试与验收

## 提交前

在仓库根目录运行：

```sh
GOTOOLCHAIN=auto go vet ./...
GOTOOLCHAIN=auto go test -count=1 ./...   # 整套几十秒，大头是 internal/ui
GOTOOLCHAIN=auto go test -race ./...      # 界面测试也跑 -race，一两分钟
WORKBENCH_TEST_WS=<.code-workspace 或目录> GOTOOLCHAIN=auto go test ./...   # 加上读真实工作区的只读测试（没设时它们跳过）
mkdir -p <目录> && WORKBENCH_SHOT=<目录> GOTOOLCHAIN=auto go test -run 'TestScreenshot|TestAboutView' -v ./internal/ui/   # 截图，要更新的复制到 docs/screenshots/
GOTOOLCHAIN=auto go test -run TestCatalog ./internal/i18n/   # 改了界面文案先 -update 收集 zh-CN.json，再给 en.json 补译文
GOTOOLCHAIN=auto go test -run '^$' -bench . -benchtime 20x ./internal/search/ ./internal/fuzzy/   # 搜索 / 模糊匹配的速度
GOTOOLCHAIN=auto go tool mygo build -skip-dmg -skip-notarize -sign -
open -n -g -a build/darwin-arm64/Holt.app --env WORKBENCH_STATE=<目录> --env WORKBENCH_LOGS=<目录>   # 30 秒后 footprint -p <pid> --noCategories -f bytes，然后只杀这个 pid
open -n -g -a build/darwin-arm64/Holt.app --env WORKBENCH_STATE=<目录> --env "WORKBENCH_ALLOPEN=async def"   # "全开"的内存（两个 Diff、搜索、当前仓库的终端都起）
open -n -a build/darwin-arm64/Holt.app --env WORKBENCH_STATE=<目录> --env WORKBENCH_FOCUS=editor   # 有焦点的内存（editor 或 terminal，lsappinfo front 核对在最前），无焦点加 -g；--env WORKBENCH_CARET_REST=off 对比不歇的光标
```

日常测量内存使用 `tools/measure/measure.sh`，方法和要求见 [AGENTS.md](../AGENTS.md) 的“单实例、真窗口和量内存”一节。

截图无法验证的部分（系统菜单、原生对话框、毛玻璃、输入法、原生拖放、真实窗口中的快捷键）需要手动测试，并在 PR 中说明测试内容和方法。

行为变更后，同步更新 README（功能、快捷键）、`CHANGELOG.md` 的“## 未发布”、`docs/architecture.md`、
`docs/shortcuts.md`（`go test -run TestShortcutDoc ./internal/app/ -update`）。新遇到的问题记入 [mygo-pitfalls.md](mygo-pitfalls.md)。

整套测试并行运行且机器负载较高时，部分计时测试偶尔会超时（`internal/watch` 的监听测试、`internal/git` 的 `TestOpTimeoutKeep`）。
可以先单独重跑，判断是否受负载影响；单独运行仍失败时，再进一步排查。

## 测试的分工

### 逻辑包

`internal/app`、`git`、`files`、`docs`、`workspace`、`search`、`fuzzy`、`watch`、`i18n`、`testutil` 等包使用临时目录和临时仓库做单元测试，并运行 `-race`。

- 搜索的验收标准（10 万行 < 1 s）由 `internal/search` 的 `TestHundredThousandLines` 检查。性能测试见 `BenchmarkHundredThousandLines`、
  `BenchmarkWorkspace`（`WORKBENCH_TEST_WS` 指的真实工作区，只读，没设就跳过）和 `internal/fuzzy` 的 `BenchmarkMatch`。
- 监听的验收标准（外部修改文件或提交后 1 s 内更新）由 `internal/watch` 的 `TestWatchRepo` 和 `internal/app` 的
  `TestWatchFollowsExternalChanges` 检查，覆盖外部修改、新建、提交、切换分支及切回，测量改动到界面状态更新的时间。
  监听测试都使用 `t.TempDir()` 中的临时仓库；将 `TMPDIR` 指向外接卷，可在其他文件系统上再测一次。
- 计时断言的预算写成 `testutil.Budget(…)`，`-race` 下自动放宽。
- 命令注册表的表测试在 `internal/app`：`TestShortcutConflicts`（快捷键冲突，按分派域算）、`TestShortcutDoc`
  （`docs/shortcuts.md` 和注册表一致，改了快捷键带 `-update` 重新生成）、`TestMenuStructure`。
- `internal/i18n` 的 `TestCatalog` 检查 `zh-CN.json` 是否包含代码中的全部中文文案、`en.json` 是否完整，以及占位符是否一致。

### 界面

`internal/ui` 使用 `ui.NewTester` 在内存中驱动整个窗口，不打开真实窗口；队列和等待工具来自 `internal/testutil`。

- 界面测试也运行 `-race`。例如，重命名会在主线程修改文档字段，后台读取文件前需要先复制这些字段；这类竞争只有界面测试能覆盖。
- 新功能的每个入口（快捷键、标题栏按钮、侧栏页、面板项、右键菜单、终端 ⌘点击）都要由 Tester 测试实际触发。
- 设置窗口用第二个 Tester（`ui.SettingsView(a)`），和主窗口共用一个 `App`。
- 英文界面用 `i18n.Set("en")` + `Tester.SetLocale(i18n.Locale())`；`TestEnglishUI` 查主窗口、右键菜单、命令面板里没有中文。
- 编写 Tester 测试时的注意事项见 [mygo-pitfalls.md](mygo-pitfalls.md) 的“测试和 Tester”一节。

### 多仓库

涉及“所有仓库同时可见”的行为都用 `testutil.ThreeRepos` 测试：同一工作区中有三个临时仓库，提交时间交错，其中两个有改动，一个干净。
检查各仓库是否同时列出、第二个仓库的写操作是否只影响自身（对照另一仓库的 HEAD 和状态）、⌘1–9 是否跳转并切换当前仓库，以及合并历史的顺序和胶囊。
新功能必须经过多仓库测试。

### 真实工作区

写操作只在 `testutil.TempRepo` / `testutil.NewRepo` 建的临时仓库上测。读真实工作区的测试（`TestStatusMatchesPorcelain`、`TestWorkbenchView`、
`TestTerminalChineseAndVim`、`TestRepoMenuAddRemove`、`TestScreenshot`、`BenchmarkWorkspace`）只读，只在设了
`WORKBENCH_TEST_WS=<.code-workspace 或目录>` 时运行，未设置时跳过。只读冒烟测试前后，`git status --porcelain=v2` / `git submodule status` /
`HEAD` / `git stash list` 应完全一致。

### 光标和内存

- 用 `Tester.Advance(CaretRest)` 推进模拟时间，确认光标停止闪烁后不再产生新帧（`TestCaretRestEditor` / `TestCaretRestDiff`），同时确认打字、输入法和 ↑ 仍正常。
- 真实窗口的内存按 AGENTS.md 的方法测量：使用独立的 `WORKBENCH_STATE`，并以工作区各仓库的本地克隆作为测试工作区。需要排除 shell 影响时，终端 shell 使用 `/bin/cat`。
  分别测量无焦点 / 有焦点 30 s / 有焦点 90 s 的情况，每 5 秒记录 `footprint` + `ps -o time`，用 `MYGO_FRAME_STATS=all` 记录帧数。
- 发布前测量四组：空闲、全开、编辑器有焦点 90 s、终端有焦点 90 s。启动峰值另以每 0.3 s 一次的分类 footprint 采样检查。测量时不要开启 `WORKBENCH_CAPTURE`。

### Git

涉及写操作的测试都使用临时仓库，真实仓库仅用于只读测试。

- **块级操作**：`git.HunkPatch` 的补丁在 `internal/git` 中实际交给 `git apply` 核对索引 / 工作区（头尾、没有末尾换行、CRLF、特殊文件名、
  两块只动一块、读 diff 后文件又改过）；界面上 `TestHunkOps` 是 `TestGitOps` 的块级部分（暂存 / 取消暂存 / 丢弃一块、J / K、⌥J / ⌥K）。
- **行级暂存**：`TestLinesPatch` 将 `LinesPatch` 的补丁实际交给 `git apply --cached` / `git apply` 核对（已配对的行、多出的行、只选旧侧、没有末尾换行），
  `TestLinesEdits` 检查配对规则；界面的 `TestLineStaging` 用 `DiffState.LineSelection` 选行，暂存 / 取消暂存 / 丢弃各一次，再对照 `git diff --cached`。
- **blame**：`internal/git` 的 `TestBlameParse` 运行实际的 `git blame --porcelain`（`--author` / `--date` 固定作者和时区，`git mv` 改名，最后一行改了不提交）；
  界面的 `TestBlameGutter` 检查装订线随滚动更新、点击后选中历史提交、未保存改动以淡色显示、保存 / 提交后重读，以及历史版本标签和关闭行为。
- **子模块**：`testutil.SubmoduleRepo(t, name)` 创建一个库仓库（两次提交）和一个主仓库；主仓库中的 `vendor/lib` 已初始化，`vendor/lib 2`（路径带空格）已 deinit。
  从本机路径克隆要 `protocol.file.allow=always`，只在测试的环境里开：`SubmoduleRepo` 自己用 `-c`；界面测试的初始化走应用的 `RunOp`，所以测试先
  `t.Setenv("GIT_CONFIG_COUNT", "1")`、`GIT_CONFIG_KEY_0=protocol.file.allow`、`GIT_CONFIG_VALUE_0=always`。应用里不加这个配置。
  `TestReadSubmodules` / `TestStatusSubField` 检查状态读取和 Diff 说明，`TestSubmodules` 覆盖树行、添加为仓库、仓库菜单、胶囊、丢弃、初始化。
- **只读行为**：启动、刷新和打开标签都不执行写命令。

### 网页和剪贴板

测试中将 `App.OpenURL` 替换为只记录地址、不打开浏览器的函数，使用虚构的远程地址，不连接网络。剪贴板操作通过 `app.CopyText`，测试时替换为只记录内容的函数，不修改系统剪贴板。

### 终端

- 界面测试里 shell 用 `/bin/sh`（`setBar` 写进 state.json，不跑用户的 zsh 配置），初始状态（底栏开关、会话中的 `terms`）需在 `newUI` 之前写好。
- `readyPane` 等待 shell 提示符，`typeLine` 输入一行后等待输出（`testutil.WaitTerm` / `WaitTermGone`）。
- 按需启动通过 `App.StartedPanes()` 检查（已启动且仍运行的 shell 数）；会话通过 `SaveState` 后的 `sessions[…].terms` 检查，目录比较前调用 `EvalSymlinks`。
- 终端 ⌘点击用 `Tester.HoldModifiers(Cmd)` + `Move` 沿竖直方向移动，通过 `Terminal.HoveredLink()` 查找链接位置，并用 `Tester.OpenedURLs()` 检查打开的网址。
- 内存按 AGENTS.md 的方法测量：使用实际的 zsh，单独记录子进程的 footprint；关闭各仓库底栏，对比按需启动前后，`WORKBENCH_TERMS=all` 量 3 × 2 的无焦点 / 有焦点 90 s。

### 终端后台进程（termd）

- `internal/termd` 的测试在测试进程内启动 `Server`，套接字是 `SockPath(t.TempDir())`（`t.TempDir()` 太长时会换到 `$TMPDIR` 下按目录取名的短路径），
  `t.Cleanup` 里 `Stop` 并删掉套接字和 `.lock`。
- 只有 `TestSpawnDetached` 会实际启动子进程（测试二进制带 `WORKBENCH_TERMD_HELPER=1` 和 `termd` 参数重入 `Main`），最后关掉。
- 界面测试把 `app.TermdStart` 换成进程内的 `Server`，设置的 `termDaemon` 在 `newUI` 之前写进 state.json。`termKey` 经 `a.Update` 走，输入后需用 `wait(...)` 等待队列处理完成；
  ⇧⌘C 之后再往终端里敲字先 `a.FocusTerm = true; tt.Frame()`。
- **冒烟测试**（实际应用和 termd）：使用独立的 `WORKBENCH_STATE` / `WORKBENCH_LOGS`，使该实例拥有自己的 termd。
  1. 首次启动时带 `WORKBENCH_TERM_CMD='echo MARK-$$'`，等待提示符后用 TERM 终止应用；用 `pgrep -f 'Holt.app/Contents/MacOS/Holt termd'` 确认 termd 仍在运行，且该 shell 仍是其子进程。
  2. 第二次启动不带该变量，确认屏幕上仍是同一个 `MARK-<pid>`（`WORKBENCH_CAPTURE` 截一张看），termd.log 里只有 1 个会话。
  3. 最后 `kill -TERM` 这个 termd（它会挂断 shell、删掉套接字），只终止自己启动的进程。
- **测量 termd 内存**：分别记录应用、termd 和其子 shell 的内存（shell 是 termd 的子进程，不是应用的），窗口保持在后台，只采用 front=other 时的数据：

  ```bash
  A=…/Holt.app; P=$(pgrep -nf "$A/Contents/MacOS/Holt\$"); D=$(pgrep -f "$A/Contents/MacOS/Holt termd")
  footprint -p $P --noCategories -f bytes | grep phys_footprint; footprint -p $D --noCategories -f bytes | grep phys_footprint
  for k in $(pgrep -P $D); do footprint -p $k --noCategories -f bytes | grep phys_footprint; done; ps -o pid,ppid,rss,command -p $D
  ```

### 发布前检查

- **无障碍标签**：`TestAccessLabels` 遍历主窗口的十几种状态、设置的每一页和关于窗口，检查按钮是否有可供读屏使用的名称。新加的区域 / 浮层 / 窗口需要在测试中增加绘制步骤，
  才能纳入检查。`TestAccessProbe` 核对 reflect 读取的字段是否仍匹配，升级 mygo 时会先由它报错。
- **打开路径**：`TestOpenArgs` 在临时仓库上测（工作区里的、不在工作区的、普通目录、找不到的、别的 `.code-workspace`），“添加仓库”后确认 `.code-workspace` 的内容完全未变。
  真实的单实例、`open -a` 要起两个进程：用独立的 `WORKBENCH_STATE` / `WORKBENCH_LOGS` 加 `WORKBENCH_SINGLE=1`，先 `pgrep -fl Holt` 确认没有别的实例
  （否则参数会交给已运行的实例），仅按 pid 终止自己启动的进程。
- **崩溃日志**：`internal/crash` 的测试在临时目录里；真机用 `WORKBENCH_PANIC=1` + `WORKBENCH_LOGS=<临时目录>`，不动 `~/Library/Logs/Holt/`。
- **版本**：`TestVersions` 检查 CHANGELOG 中是否有 `mygo.json` 对应版本的小节，以及 mygo 版本是否与锁定版本一致；发布步骤见 [release.md](release.md)。

## 验收标准

验收标准由上述计时、内存和行为测试检查。所有测试通过，并完成必要的手动测试后，才算验收完成。内存要求为空闲 ≤ 100 MB，硬上限 300 MB。
