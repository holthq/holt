## What changed

<!-- A short description, and the issue it closes if any ("Fixes #123"). -->

## Why

<!-- The problem this solves. Screenshots are welcome for UI changes. -->

## How it was tested

<!-- Commands you ran, what you tried by hand, and on which OS. -->

- [ ] `gofmt` / `go vet ./...` clean
- [ ] `go test ./...` passes (and `-race` for UI / concurrency changes)
- [ ] UI change: headless screenshots attached (`WORKBENCH_SHOT=…`)
- [ ] New UI text: `TestCatalog -update` run and `en.json` updated
- [ ] Commands / shortcuts: `TestShortcutDoc -update` run
- [ ] Tests do not write to real repositories (use `internal/testutil`)

## Platforms

- [ ] macOS (supported)
- [ ] Linux (build only)
- [ ] Windows (build only)
