package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/testutil"
	"github.com/holthq/holt/internal/testutil/fakelsp"
)

// 语言服务器接线：起一个假服务端（测试程序自己），打开 a.go，看文档同步、诊断、补全、
// 悬停、定义都走到服务端，回答也接回了编辑器。测试程序见 TestMain（askpass_test.go）。
func TestLanguageServer(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "fake.log")
	t.Setenv(fakelsp.Env, "1")
	t.Setenv(fakelsp.LogEnv, logPath)

	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	a.LangOff = false
	a.St.Settings.LanguageServers = map[string]string{"Go": ShellQuote(exe)}
	a.SettingsChanged()
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	d := a.OpenFile(r, "a.go", true)
	wait("读完 a.go", func() bool { return d.Buf != nil && !d.Loading })

	wait("didOpen", func() bool {
		a.LangTick(d)
		return fileHas(logPath, "textDocument/didOpen")
	})
	wait("诊断", func() bool {
		a.SyncProblems(d)
		ds := d.St.Diagnostics()
		return len(ds) > 0 && ds[0].Message == "fake error" && ds[0].Source == "fake"
	})

	// 外部检查和语言服务器两批诊断合在一起，谁都不会被另一批盖掉。
	a.Probs.Gen++
	if a.Probs.sets == nil {
		a.Probs.sets = map[lintSet][]Problem{}
	}
	a.Probs.sets[lintSet{root: r.Root, lang: "Go"}] = []Problem{{
		Root: r.Root, Rel: "a.go", Line: 2, Col: 1, Sev: editor.SeverityWarning, Msg: "lint warn", Source: "fake-lint",
	}}
	if a.Probs.absGen == nil {
		a.Probs.absGen = map[string]int{}
	}
	a.Probs.absGen[d.Abs] = a.Probs.Gen
	wait("合并诊断", func() bool {
		a.SyncProblems(d)
		var lint, lsp bool
		for _, q := range d.St.Diagnostics() {
			lint = lint || q.Message == "lint warn"
			lsp = lsp || q.Message == "fake error"
		}
		return lint && lsp
	})

	a.Completion(d, editor.CompletionRequest{Offset: 0, Prefix: "", Version: d.Buf.Version(), Explicit: true})
	wait("补全请求", func() bool { return fileHas(logPath, "textDocument/completion") })

	a.Hover(d, editor.HoverRequest{Offset: 0, Version: d.Buf.Version()})
	wait("悬停请求", func() bool { return fileHas(logPath, "textDocument/hover") })

	a.Definition(d, 0)
	wait("跳到定义", func() bool {
		cur := d.St.Cursor()
		return cur >= 0 && d.Buf.Pos(cur).Line == 1
	})

	a.Close()
	if !fileHas(logPath, "shutdown") {
		t.Fatalf("关窗口时没给语言服务器发 shutdown，日志：\n%s", langFileText(logPath))
	}
}

func fileHas(path, want string) bool { return strings.Contains(langFileText(path), want) }

func langFileText(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// 设置里写 - 时不起服务器。
func TestLanguageServerOff(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, wait := newTestApp(t, ws)
	t.Cleanup(a.Close)
	a.LangOff = false
	a.St.Settings.LanguageServers = map[string]string{"Go": "-"}
	r := a.Current()
	a.RefreshRepo(r, WhyManual)
	wait("读状态", idle(r))
	d := a.OpenFile(r, "a.go", true)
	wait("读完 a.go", func() bool { return d.Buf != nil && !d.Loading })
	a.LangTick(d)
	if a.lang.servers != nil {
		t.Fatalf("写 - 还起了服务器：%+v", a.lang.servers)
	}
}
