package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
)

// 检查：ParseProblems 认各家工具的输出行（go vet、tsc、eslint 两种、ruff、shellcheck、cargo），路径统一成仓库内的。
func TestParseProblems(t *testing.T) {
	const root = "/r"
	out := strings.Join([]string{
		"# example.com/shop",
		"./cart.go:12:2: fmt.Printf format %d has arg x of wrong type string",
		"vet: pay/pay.go:3:9: undefined: Total",
		"a.go:5: missing return",
		"src/app.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.",
		"/r/src/x.js:4:7: 'a' is defined but never used. [Error/no-unused-vars]",
		"/r/src/x.js:9:1: Missing semicolon. [Warning/semi]",
		"b.py:1:8: F401 [*] `os` imported but unused",
		"s.sh:3:6: warning: Double quote to prevent globbing and word splitting. [SC2086]",
		"s.sh:1:1: note: Not following: ./lib.sh was not specified as input. [SC1091]",
		"src/main.rs:2:9: error[E0425]: cannot find value `x` in this scope",
		"src/main.rs:5:5: warning: unused variable: `y`",
		"a.go:7:2: hint: maybe simpler\r",
		"/elsewhere/x.go:1:1: outside",
		"../up.go:1:1: outside too",
		"Found 2 errors in 1 file.",
		"",
		"/r/src/y.js",
		"  1:10  error    'foo' is defined but never used  no-unused-vars",
		"  2:1   warning  Unexpected console statement     no-console",
		"",
		"✖ 2 problems (1 error, 1 warning)",
	}, "\n")
	E, W, I, H := editor.SeverityError, editor.SeverityWarning, editor.SeverityInfo, editor.SeverityHint
	want := []Problem{
		{Rel: "cart.go", Line: 12, Col: 2, Sev: E, Msg: "fmt.Printf format %d has arg x of wrong type string"},
		{Rel: "pay/pay.go", Line: 3, Col: 9, Sev: E, Msg: "undefined: Total"},
		{Rel: "a.go", Line: 5, Sev: E, Msg: "missing return"},
		{Rel: "src/app.ts", Line: 3, Col: 7, Sev: E, Msg: "Type 'string' is not assignable to type 'number'. (TS2322)"},
		{Rel: "src/x.js", Line: 4, Col: 7, Sev: E, Msg: "'a' is defined but never used. (no-unused-vars)"},
		{Rel: "src/x.js", Line: 9, Col: 1, Sev: W, Msg: "Missing semicolon. (semi)"},
		{Rel: "b.py", Line: 1, Col: 8, Sev: E, Msg: "F401 [*] `os` imported but unused"},
		{Rel: "s.sh", Line: 3, Col: 6, Sev: W, Msg: "Double quote to prevent globbing and word splitting. [SC2086]"},
		{Rel: "s.sh", Line: 1, Col: 1, Sev: I, Msg: "Not following: ./lib.sh was not specified as input. [SC1091]"},
		{Rel: "src/main.rs", Line: 2, Col: 9, Sev: E, Msg: "cannot find value `x` in this scope (E0425)"},
		{Rel: "src/main.rs", Line: 5, Col: 5, Sev: W, Msg: "unused variable: `y`"},
		{Rel: "a.go", Line: 7, Col: 2, Sev: H, Msg: "maybe simpler"},
		{Rel: "src/y.js", Line: 1, Col: 10, Sev: E, Msg: "'foo' is defined but never used (no-unused-vars)"},
		{Rel: "src/y.js", Line: 2, Col: 1, Sev: W, Msg: "Unexpected console statement (no-console)"},
	}
	for i := range want {
		want[i].Root = root
	}
	got := ParseProblems(out, root)
	if !slices.Equal(got, want) {
		for i := range max(len(got), len(want)) {
			var g, w Problem
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if g != w {
				t.Errorf("第 %d 条：\n得到 %+v\n应是 %+v", i, g, w)
			}
		}
	}
}

// 检查跑起来（临时仓库，命令都是 shell 一行，不跑真的 go vet）：保存时跑、问题标进编辑器（行列换成偏移）；退出 0 清掉；
// 没装、超时提示里说、记进输出栏；{file}、{dir} 只换掉自己那一片；跑着时又存只再跑一次；跑的时候打了字，位置跟着挪。
func TestLintOnSave(t *testing.T) {
	repo := testutil.NewRepo(t, "lint")
	repo.Write("a.go", "package a\nvar xyz = 1\n")
	repo.Write("b.go", "package a\n")
	repo.Write("sub/c.go", "package sub\n")
	repo.Commit("base")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	t.Cleanup(a.Close)
	s := &a.St.Settings
	s.Shell, s.LintOnSave = "/bin/sh", true
	set := func(cmd string) { s.Linters = map[string]string{"Go": cmd} }
	open := func(name string) *docs.Doc {
		d := a.OpenFile(a.Repos[0], name, true)
		wait("读好 "+name, func() bool { return d.Buf != nil && !d.Loading })
		return d
	}
	idle := func() bool { return len(a.Probs.jobs) == 0 }
	save := func(d *docs.Doc, what string) {
		t.Helper()
		d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: "\n"})
		a.Save(d)
		if idle() {
			t.Fatalf("%s：存了没检查", what)
		}
		wait(what, idle)
	}
	probs := func() []string {
		list, _, _ := a.ProblemList()
		var out []string
		for _, q := range list {
			out = append(out, q.Rel+":"+q.Msg)
		}
		return out
	}
	d := open("a.go")

	set("printf 'a.go:2:5: boom\\n'; exit 1")
	save(d, "保存时检查")
	list, errs, _ := a.ProblemList()
	if len(list) != 1 || errs != 1 || list[0].Line != 2 || list[0].Col != 5 || list[0].Source != "printf" || list[0].Root != repo.Root {
		t.Fatalf("问题：%+v（错误 %d）", list, errs)
	}
	a.SyncProblems(d)
	ds := d.St.Diagnostics()
	at := d.Buf.Offset(editor.Pos{Line: 1, Col: 4})
	if len(ds) != 1 || ds[0].Start != at || ds[0].End != at+3 || ds[0].Message != "boom" || ds[0].Severity != editor.SeverityError {
		t.Fatalf("诊断：%+v（应从 %d 起三个字）", ds, at)
	}

	set("exit 0")
	save(d, "退出 0")
	a.SyncProblems(d)
	if len(probs()) != 0 || len(d.St.Diagnostics()) != 0 {
		t.Fatalf("退出 0 没清掉：%q %+v", probs(), d.St.Diagnostics())
	}

	failed := func() int {
		n := 0
		for _, o := range a.Output {
			if o.Failed && strings.Contains(o.Title, "检查 a.go") {
				n++
			}
		}
		return n
	}
	set("echo 'command not found: lintx' >&2; exit 127")
	save(d, "没装")
	if !strings.Contains(a.Toast, "没找到 lintx") || len(probs()) != 0 || failed() != 1 {
		t.Fatalf("没装：提示 %q，问题 %q，输出栏 %d 条", a.Toast, probs(), failed())
	}
	a.Toast = ""
	save(d, "又没装")
	if a.Toast != "已保存 a.go" || failed() != 1 {
		t.Errorf("保存时同一种语言失败第二次还提示：%q（输出栏 %d 条）", a.Toast, failed())
	}

	old := LintTimeout
	LintTimeout = 200 * time.Millisecond
	t.Cleanup(func() { LintTimeout = old })
	set("sleep 5")
	start := time.Now()
	a.Lint(d) // 手动的：失败都提示
	wait("超时", idle)
	if !strings.Contains(a.Toast, "没跑完") || failed() != 2 || time.Since(start) > 4*time.Second {
		t.Fatalf("超时：提示 %q，输出栏 %d 条，用了 %v", a.Toast, failed(), time.Since(start))
	}
	LintTimeout = old

	// {file}：每个文件只换自己的
	b := open("b.go")
	set(`f={file}; printf '%s:1:1: own\n' "${f##*/}"; exit 1`)
	save(d, "a.go 的")
	save(b, "b.go 的")
	if got := probs(); !slices.Equal(got, []string{"a.go:own", "b.go:own"}) {
		t.Fatalf("{file}：%q", got)
	}
	set("echo {file} >/dev/null; exit 0")
	save(d, "a.go 好了")
	if got := probs(); !slices.Equal(got, []string{"b.go:own"}) {
		t.Fatalf("{file} 只清自己的：%q", got)
	}

	// {dir}：换掉那个目录的（同目录别的文件的也算，子目录的不算）
	c := open("sub/c.go")
	set(`printf '%s/c.go:1:1: in dir\n' {dir}; exit 1`)
	save(c, "sub 的")
	if got := probs(); !slices.Equal(got, []string{"b.go:own", "sub/c.go:in dir"}) {
		t.Fatalf("{dir}：%q", got)
	}
	set("echo {dir} >/dev/null; exit 0")
	save(d, "根目录好了")
	if got := probs(); !slices.Equal(got, []string{"sub/c.go:in dir"}) {
		t.Fatalf("{dir}：%q", got)
	}

	// 没占位符：整个仓库这种语言的一起换；跑着时存三次只再跑一次
	runs := filepath.Join(t.TempDir(), "runs")
	set("echo run >> " + ShellQuote(runs) + "; sleep 0.3; printf 'a.go:2:5: boom\\n'; exit 1")
	for range 3 {
		d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: "\n"})
		a.Save(d)
	}
	// 跑的时候在开头插一行（没存）：回来的诊断跟着挪
	d.Buf.Edit(editor.Edit{Start: 0, End: 0, Text: "// new\n"})
	wait("跑完两次", idle)
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "run") != 2 {
		t.Errorf("跑着时存三次：跑了 %d 次", strings.Count(string(b), "run"))
	}
	if got := probs(); !slices.Equal(got, []string{"a.go:boom"}) {
		t.Fatalf("整个仓库一起换：%q", got)
	}
	a.SyncProblems(d)
	ds = d.St.Diagnostics()
	if len(ds) != 1 || ds[0].Start != at+len("// new\n") {
		t.Errorf("跑的时候打了字：%+v（应从 %d 起）", ds, at+len("// new\n"))
	}

	// 关掉保存时检查：存了不跑；手动的照跑，没有命令时提示
	s.LintOnSave = false
	d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: "\n"})
	a.Save(d)
	if !idle() {
		t.Error("关着保存时检查还跑了")
	}
	set(FormatOff)
	a.Lint(d)
	if !idle() || a.Toast != "Go 的检查在设置里关掉了" {
		t.Errorf("关掉了：%q", a.Toast)
	}
	s.Linters = nil
	if a.LinterOf("Go") != "go vet ./{dir}" || a.LinterOf("Kotlin") != "" {
		t.Errorf("内置默认：%q %q", a.LinterOf("Go"), a.LinterOf("Kotlin"))
	}

	// 清空
	a.ClearProblems()
	a.SyncProblems(d)
	if len(probs()) != 0 || len(d.St.Diagnostics()) != 0 || a.HasProblems() {
		t.Errorf("清空：%q %+v", probs(), d.St.Diagnostics())
	}
}
