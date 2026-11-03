# 打包与发布

本文介绍 Grove 的版本号规则、构建、DMG 打包、签名、公证和 CI。日常开发使用
`go tool mygo build -skip-dmg -skip-notarize -sign -` 即可，见 README 的“构建”一节。

## 版本号

版本号统一取自 `mygo.json` 的 `version`，公开版本从 0.1.0 开始。

- `mygo build` 把它写进 Info.plist（`CFBundleShortVersionString` / `CFBundleVersion`），并用
  `-ldflags -X github.com/egoist/mygo.packageVersion=…` 编进二进制；`main` 启动时
  `version.App = mygo.App.Version()`。`go run` 和测试没有注入版本号，显示为 `dev`。
- 关于窗口显示“版本 x.y.z”，下方列出编译时使用的 mygo 和 Go 版本。首次启动或更新后，
  状态栏提示“欢迎使用 Grove x.y.z” / “已更新到 Grove x.y.z”（记录在 state.json 的 `appVersion` 中，同一版本不再提示）。
  崩溃日志和 `workbench.log` 的第一行也会记录版本号。
- `TestVersions`（`internal/version`）检查两项：根目录 `CHANGELOG.md` 中必须有以 `## v<版本> ` 开头的小节；
  编译时使用的 mygo 版本必须与 go.mod 中锁定的版本一致。

1.0 之前，仅修复问题时递增补丁版本号（0.1.0 → 0.1.1）；新增功能或改变行为时递增次版本号（0.1.x → 0.2.0）。

日常改动不修改版本号。涉及行为变化的 PR 在 `CHANGELOG.md` 顶部的 `## 未发布` 中补充记录，`mygo.json` 保持上次发布的版本号。

发布新版本的步骤：

1. 改 `mygo.json` 的 `version`；
2. 把 `CHANGELOG.md` 的 `## 未发布` 改成 `## v<版本> — <日期>`，在上方新增一个空的 `## 未发布`；
3. `GOTOOLCHAIN=auto go vet ./... && GOTOOLCHAIN=auto go test -count=1 ./... && GOTOOLCHAIN=auto go test -race ./...`；
4. 按下文构建，在真实窗口中手动检查截图无法验证的部分（菜单、对话框、毛玻璃、拖放、终端）；
5. 合入 main 后，在该提交上打 `v<版本>` 标签。

## 构建

```bash
GOTOOLCHAIN=auto go tool mygo build -skip-dmg -skip-notarize -sign -
```

构建产物为 `build/darwin-arm64/Grove.app`（约 30 MB），使用 ad hoc 签名，可在构建机器上打开。
`mygo.json` 中的应用名为 Grove，bundle id 为 `io.github.lemoeade.grove`，可执行文件为 `Contents/MacOS/Grove`，DMG 文件名为 `grove-<版本>-arm64.dmg`（mygo 使用应用名的 slug）。
源码中的 `cmd/workbench`、`WORKBENCH_*` 沿用了项目旧名，保留原因见 `AGENTS.md`。
复制到其他 Mac 后会被 Gatekeeper 拦截，需要右键选择“打开”，或运行 `xattr -dr com.apple.quarantine Grove.app`。

终端后台进程（termd）不单独打包。同一个可执行文件带上 `termd` 参数（`Grove termd <套接字> <日志>`）后，`main` 在第一行将其交给后台进程入口，
不打开窗口，也不显示在 Dock 中。因此，签名和公证仍只需处理这一个可执行文件。

应用图标是 `assets/icon.png`（1024×1024，`mygo.json` 的 `icon`，mygo 转成 `AppIcon.icns`）。它是
`internal/appicon` 用代码绘制的：修改绘制代码后，运行 `go test ./internal/appicon -update` 重新生成；关于窗口使用同一套绘制代码。
图标不放在 `resources/` 中，因为该目录会原样复制到 `Contents/Resources`，放入图标会导致重复打包。

`resources/` 里有字体（JetBrains Mono Nerd Font）和 `licenses/`（第三方许可证全文，关于窗口的"查看许可证全文"打开它）。

## DMG

```bash
GOTOOLCHAIN=auto go tool mygo build -skip-notarize -sign -
```

这条命令会额外生成 `build/darwin-arm64/grove-<版本>-arm64.dmg`，包含“拖到应用程序”的窗口布局。
mygo 的处理流程：`hdiutil create` 创建可写镜像 → `hdiutil attach -readwrite` 挂载并写入 `.DS_Store`（图标位置）→
`detach` → 转成只读的 ULMO 格式。系统有 `diskutil image create from` 时用它转，没有才用 `hdiutil convert`
（macOS 27 上 `hdiutil convert` 会失败）。

部分机器会因挂载权限拦截 `attach`，因此 README 的日常构建命令带有 `-skip-dmg`。遇到这种情况，可以跳过挂载，
直接从目录创建只读镜像，但这样生成的镜像没有窗口布局：

```bash
mkdir -p dist/stage && ditto build/darwin-arm64/Grove.app dist/stage/Grove.app && ln -s /Applications dist/stage/Applications
hdiutil create -srcfolder dist/stage -volname Grove -fs HFS+ -format ULMO -ov dist/Grove-<版本>-arm64.dmg
hdiutil verify dist/Grove-<版本>-arm64.dmg
```

新系统会提示 `hdiutil create -volname -format` 已弃用，建议使用 `diskutil image create`；目前该命令仍可用。
这样生成的 DMG 也可以按下文公证：先运行 `xcrun notarytool submit … --wait`，再运行 `xcrun stapler staple`。

## 签名和公证（分发给其他用户）

需要付费的 Apple Developer 账号。以下步骤尚未实际验证，是根据 mygo 代码
（`cmd/mygo/dmg.go`、`bundle.go`）和 Apple 的流程整理的。

1. **证书**：在钥匙串中安装 “Developer ID Application: 名字 (TEAMID)” 证书，用 `security find-identity -v -p codesigning` 确认。
2. **公证凭据**：将凭据存入钥匙串（只需一次；密码使用 appleid.apple.com 生成的 App 专用密码）：

   ```bash
   xcrun notarytool store-credentials grove-notary --apple-id 你的AppleID --team-id TEAMID
   ```

3. **mygo.json**：在 `macos` 中添加以下字段（签名身份也可以不写入文件，构建时通过 `-sign "Developer ID Application: …"` 传入）：

   ```json
   "signingIdentity": "Developer ID Application: 名字 (TEAMID)",
   "notarize": { "keychainProfile": "grove-notary" }
   ```

   配置 `notarize` 后，使用 ad hoc 签名（`-sign -`）构建时必须带 `-skip-notarize`，否则 mygo 会报错。
   README 和 CI 中的命令已包含该参数。
4. **构建**：`GOTOOLCHAIN=auto go tool mygo build`。mygo 用 Developer ID 签名时加 hardened runtime 和安全时间戳，
   同时签名 `Contents/Resources` 中的可执行文件和 dylib（mygo 放入的 libghostty-vt），生成 DMG，再运行 `notarytool submit --wait`。
   通过后，用 `stapler staple` 将公证票据附加到 DMG 和 .app。
5. **核对**：

   ```bash
   codesign -dv --verbose=4 build/darwin-arm64/Grove.app
   spctl -a -vv -t install build/darwin-arm64/grove-<版本>-arm64.dmg
   xcrun stapler validate build/darwin-arm64/grove-<版本>-arm64.dmg
   ```

   验证失败时，用 `xcrun notarytool log <提交 ID> --keychain-profile grove-notary` 查看日志（mygo 出错时会输出这条命令）。

需要手动验证：hardened runtime 下 purego 能否加载 `libghostty-vt.dylib`（同一 Team ID 签名，预计可通过库验证）、
终端能否启动 shell，以及单实例锁能否创建 unix socket。如果验证失败，可能需要在 `mygo.json` 的 `macos.entitlements` 中指定
entitlements 文件，例如设置 `com.apple.security.cs.disable-library-validation`。

## 打开方式和单实例

`mygo.json` 的 `macos.infoPlist` 加了 `CFBundleDocumentTypes`：文件夹（`public.folder`）和任何文件（`public.item`），
`LSHandlerRank` 为 `Alternate`：访达的“打开方式”中会列出 Grove，也可以将文件拖到 Dock 图标上，但不会将 Grove 设为默认程序。
系统可能需要等待片刻，或在 .app 移入“应用程序”后，才会识别这些类型。

单实例由 `mygo.App.RequestSingleInstanceLock()` 实现，按应用名和 identifier 创建 unix socket。
第二个 `Grove 路径` 进程把参数和工作目录交给已运行的实例后退出；`open -a Grove 路径` 和拖到 Dock 的文件通过
`OnOpenFile` 处理。测量内存或测试时，设置 `WORKBENCH_STATE` 后默认不获取单实例锁，以免把参数交给日常使用的实例。
测试单实例本身时再加 `WORKBENCH_SINGLE=1`，并先用 `pgrep -fl Grove` 确认没有其他实例。

## CI

`.github/workflows/ci.yml`，`macos-latest`：

- 每次推送到 main 或提交 PR 时，运行 gofmt、`go vet ./...`、`go test -count=1 ./...`、`mygo build -skip-dmg -skip-notarize -sign -`，
  将 .app 打包为 zip，作为构建产物保留 14 天；
- `go test -race ./internal/...` 耗时较长，仅在手动触发时勾选 `race`，或每周一运行一次。

env 中设置了 `GOTOOLCHAIN=auto`（go.mod 要求 1.27.3）；mygo 使用 go.mod 锁定的版本，CI 中不要运行 `go get -u`。
UI 测试和终端测试在 GitHub runner 上运行不稳定时，先检查环境差异：runner 没有登录会话，shell 为 `/bin/zsh`。
CI 未设置 `WORKBENCH_TEST_WS`，因此会跳过读取真实工作区的只读测试。

`.github/workflows/claude.yml`：仓库的 owner、成员或协作者在 issue 或 PR 评论里写 `@claude`，就会在 `macos-latest` 上运行 Claude
（`anthropics/claude-code-action@v1`）。Claude 可以运行 `go`、`gofmt`、`git`，改完后推送分支并创建 PR。
需要在仓库安装 Claude GitHub App（github.com/apps/claude），并把 Anthropic API 密钥保存为 Secret `ANTHROPIC_API_KEY`。
这类事件只读取默认分支上的工作流文件，所以要合并到 main 后才生效。每次最多 30 轮（`--max-turns`）、60 分钟。
Claude 会跑 `go test`，等于执行 PR 里的代码，所以别在不认识的人提的 PR 上 `@claude`。

## 许可

- 第三方组件的许可证全文位于 `resources/licenses/`（清单见 `README.txt`），可以从关于窗口打开。
- `ghostty.txt` 目前只有来源说明（libghostty-vt 所用的 Ghostty 提交）和许可证链接。**正式发布前需补入 Ghostty 的
  LICENSE 全文**（MIT 要求附上原文）。
- Grove 使用 MIT 许可证（根目录 `LICENSE`）。如需在 Info.plist 中写入版权信息，在 `mygo.json` 中添加 `copyright`
  字段，对应 `NSHumanReadableCopyright`。
