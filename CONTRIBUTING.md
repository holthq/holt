# Contributing to Holt

Thanks for helping! Holt is a native, multi-repo workbench written in Go on top of
[mygo](https://github.com/egoist/mygo), forked from [Grove](https://github.com/lemoeade/grove).
Pull requests and issues are welcome in English or Chinese.

The authoritative rules for changing code live in [AGENTS.md](AGENTS.md) (written in Chinese). This file is the
English summary — please read it before opening a PR.

## What Holt is

- One window shows every repo in the workspace: file tree, editors, per-repo Git review, and terminals.
- The app only writes when the user asks (stage, commit, push, save…). Startup, refresh and file watching never
  write to your repositories.
- Every string in the UI goes through `internal/i18n`: Chinese source text plus translations in `en.json`.

## Requirements

- macOS 13+ to run and test the app. Go per `go.mod` (`GOTOOLCHAIN=auto` picks it up). No cgo.
- mygo is pinned in `go.mod`; do not run `go get -u`. Upgrading it is a separate, fully tested change.

## Build and test

```sh
GOTOOLCHAIN=auto go vet ./...
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go test -race ./...     # slower; CI runs it weekly or on demand
GOTOOLCHAIN=auto go tool mygo build -skip-dmg -skip-notarize -sign -
```

The app lands in `build/darwin-arm64/Holt.app`. Try it with a scratch state directory so it does not touch your
normal settings or the single-instance lock:

```sh
open -g -n build/darwin-arm64/Holt.app \
  --env WORKBENCH_STATE=$TMPDIR/holt-state --env WORKBENCH_LOGS=$TMPDIR/holt-logs
```

## Tests

- Tests never write to real repositories. Create temporary repos with `internal/testutil` (`TempRepo` / `NewRepo` /
  `ThreeRepos` / `SubmoduleRepo`).
- Read-only tests against a real workspace only run when `WORKBENCH_TEST_WS` is set; leave it unset for CI.
- Timed assertions use `testutil.Budget` so `-race` runs get proportionally more time.

## UI changes

- Attach screenshots. Reviewers cannot see your screen; render them headlessly:

  ```sh
  mkdir -p /tmp/shots && WORKBENCH_SHOT=/tmp/shots GOTOOLCHAIN=auto \
    go test -run 'TestScreenshot|TestAboutView' -v ./internal/ui/
  ```

- Every icon-only button needs `Label` and `Tooltip`; `TestAccessLabels` checks.
- New UI text goes through `i18n.T("中文")` / `i18n.Tf(…)`, then:

  ```sh
  go test -run TestCatalog ./internal/i18n/ -update   # collects zh-CN.json
  go test -run TestCatalog ./internal/i18n/           # lists missing en.json entries
  ```

  Add the English translation to `internal/i18n/en.json`.
- New actions go into the command registry (`internal/app/commands.go`) first; menus, shortcuts and
  `docs/shortcuts.md` follow from it (`go test -run TestShortcutDoc ./internal/app/ -update`).

## Commits and pull requests

- Keep PRs small and focused; one topic per PR.
- Commit messages in English or Chinese are both fine.
- Describe what changed, why, what you tested, and on which OS.
- Platform status: macOS is the supported platform for now. Linux and Windows are compiled and vetted in CI;
  the terminal daemon (`internal/termd`) is a stub on Windows, and Linux runtime is not exercised yet.
  If you are working on those platforms, say what you ran.

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## 中文

这是英文版的贡献说明，项目规则的正文在 [AGENTS.md](AGENTS.md)：所有仓库同时可见、只在用户操作时写、
测试不碰真实仓库、界面靠截图、文案走 i18n、命令先进注册表。PR 用中文或英文都行；改了界面请附无头截图；
提交前跑 `gofmt`、`go vet ./...`、`go test ./...`。
