# Holt

[![CI](https://github.com/holthq/holt/actions/workflows/ci.yml/badge.svg)](https://github.com/holthq/holt/actions/workflows/ci.yml)

中文 · [English](README.en.md)

Holt 是一个 macOS 多仓库工作台，可以在同一个窗口里浏览文件、编辑代码、查看 Git 改动和提交历史，以及使用终端。
工作区里的所有仓库都会显示在文件树和 Git 栏中，方便一起查看各个仓库的改动。

Holt 使用 Go 编写，界面由 [mygo](https://github.com/egoist/mygo) 原生绘制。打开五个仓库时，空闲内存占用约为 79 MiB。

Holt 是 [Grove](https://github.com/lemoeade/grove) 的 fork：界面和内核都从那里长出来，保留 MIT 许可和原作者的版权行。

![Holt](docs/screenshots/light.png)

## 三条原则

- **所有仓库同时可见**：文件树和 Git 栏同时列出所有仓库，搜索、历史和终端也按仓库组织。
- **由你决定何时修改**：自动刷新只执行只读的 Git 命令（设置 `GIT_OPTIONAL_LOCKS=0`，不修改索引）。暂存、提交、推送等操作
  都需要手动触发，可能造成数据丢失的操作会先要求确认。
- **不主动上传数据**：只有你发起抓取、拉取、推送或克隆等 Git 操作时，Holt 才会连接远程仓库。

## 功能

| | |
| --- | --- |
| ![Git 栏](docs/screenshots/gitbar.png) | ![提交图](docs/screenshots/history-graph.png) |
| ![编辑器分屏](docs/screenshots/split-editors.png) | ![终端](docs/screenshots/terminals.png) |

- **工作区**：支持打开 `.code-workspace` 文件或文件夹，也支持多窗口。文件树支持跨仓库多选和拖动，可以撤销上一次文件操作。
- **编辑器**：将标签拖到边缘即可分屏。支持大纲、粘性滚动、代码片段和 Markdown 预览（含 Mermaid 和公式），
  也支持 GB18030、Big5 等编码。保存时可运行 gofmt、prettier 等格式化工具，以及 go vet、eslint 等检查工具。
- **Git**：支持按块或按行暂存，在编辑器中解决冲突，以及查看单个仓库的提交图或所有仓库按时间合并的历史。合并、变基（含交互式变基）、
  cherry-pick、贮藏、工作树和子模块操作都可以在界面中完成。Git 栏每个仓库的段头上都有拉取和推送按钮，并显示领先 / 落后的提交数；
  段里提交框在最上面，平时就是一行输入框，写了信息才出现提交按钮，按钮上写会提交几个文件；什么都没暂存时一下子提交全部改动（含未跟踪的文件），▾ 里是提交并推送、修改上一次提交、撤销上次提交。变更行左边是和文件树一样的文件图标，M / A / D 在行尾，放不下时路径先缩。未跟踪的文件和未暂存的改动同在"改动"一组，分组上的按钮指针移上去才有，工作树和贮藏默认不显示，在设置 → Git 里打开"在 Git 栏显示工作树 / 贮藏"（贮藏、弹出贮藏关着时也能用）。
  打开工作树后，每个工作树就是一个 agent 的工位：Git 栏里一行一个状态符号（等你、运行中、可合并、有改动、空闲、已合并），点开能看它比主分支多出的改动、
  审查、合并到 main；工作树组收着时组头右边是最需要你的那一档的符号；侧栏顶上的"需要你"条列出所有仓库里在等你、可合并的工作树，上一个 / 下一个在它们之间跳。
  灰着的 Git 操作会写明原因（"推送（和 origin 一致）""移除工作树（主工作树不能删）"），按钮的提示里带要跑的 git 命令。
  右栏的提交历史一个提交一行、按日期分段，选中的提交在卡片里展开；编码 agent（Claude、Codex、Copilot……）
  通过 Co-Authored-By 参与的提交带一个四角星，可以只看这些提交。
  变更文件行上悬停（或用键盘选中）就出打开文件、丢弃改动、暂存 / 取消暂存的按钮；列表有焦点时空格暂存 / 取消暂存、⌘⌫ 丢弃，多选时作用于选中的全部。
- **终端**：基于 Ghostty 的 libghostty-vt，每个仓库有一组终端，支持分屏。关闭窗口时会保存布局和输出。
  终端里跑着 claude、codex 等 agent 时，标签上会标出它在等你（琥珀色圆点：响铃或通知了，切进去就消失）还是在运行（转圈）；别的 agent 命令可以在设置里加。
- **搜索和导航**：支持跨仓库搜索和替换。⌘P 查找文件，⇧⌘P 打开命令面板，⌘T 跳转到符号；⇧⌘[ / ⇧⌘] 切上一个 / 下一个标签，⌃Tab 切回上一个用过的标签。
- **本地历史**：每次保存都会保留快照，方便找回误改或误删的内容。
- **其他**：支持中英文界面、浅色和深色主题、自定义快捷键；应用菜单和关于窗口里能检查更新（读 GitHub 上的 release）。

## 构建

需要搭载 Apple Silicon 芯片、运行 macOS 13 或更新版本的 Mac。目前还没有安装包，需要自行构建：

```bash
git clone https://github.com/holthq/holt.git
cd grove
GOTOOLCHAIN=auto go tool mygo build -skip-dmg -skip-notarize -sign -
open build/darwin-arm64/Holt.app
```

mygo 已在 `go.mod` 中声明，无需单独安装。如果本机 Go 版本较旧，`GOTOOLCHAIN=auto` 会自动下载所需版本。
签名、公证和 DMG 打包说明见 [docs/release.md](docs/release.md)。

## 用法

按 ⌘O 打开文件夹或 `.code-workspace` 文件，也可以在命令行中运行 `open -a Holt <路径>`。
⌘1–9 跳转到对应序号的仓库，⌘J 显示或隐藏终端，⇧⌘J 把底栏最大化（占掉编辑区，再按一下还原；双击底栏标题行的空白处也行）。
完整快捷键见 [docs/shortcuts.md](docs/shortcuts.md)。

设置和会话保存在 `~/Library/Application Support/Holt/`，日志保存在 `~/Library/Logs/Holt/`。

## 已知限制

- 没有 LSP，所以没有自动补全和跳转到定义。
- 只有 macOS（Apple Silicon）版本，没有自动更新，也没有插件系统。

## 开发

```bash
GOTOOLCHAIN=auto go vet ./...
GOTOOLCHAIN=auto go test ./...
```

界面测试在内存中渲染，不会打开窗口；涉及写操作的测试只使用临时仓库。修改代码前请先阅读 [AGENTS.md](AGENTS.md)。
其他文档见 [docs/](docs/)，版本改动见 [CHANGELOG.md](CHANGELOG.md)。

## 贡献

欢迎提 PR，中文英文都行。先看 [CONTRIBUTING.md](CONTRIBUTING.md)（英文为主，中文在末尾）：怎么构建、怎么跑测试、
界面改动要附无头截图、文案要过 i18n。参与即表示同意 [行为准则](CODE_OF_CONDUCT.md)；安全问题按
[SECURITY.md](SECURITY.md) 私下报，不要开公开 issue。

## 致谢

[mygo](https://github.com/egoist/mygo)，egoist 的 [godiff](https://github.com/egoist/godiff) 和
[kero](https://github.com/egoist/kero)，[Ghostty](https://ghostty.org) 的 libghostty-vt，
[JetBrains Mono](https://www.jetbrains.com/lp/mono/)，[Mermaid](https://mermaid.js.org)，[MathJax](https://www.mathjax.org)。
其他第三方组件和许可证见 [resources/licenses/](resources/licenses/)。

## 许可证

[MIT](LICENSE)
