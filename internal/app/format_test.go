package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/testutil"
)

// 外部格式化（临时仓库，假的格式化命令，不跑真的 gofmt 那些）：只套改了的地方、光标留着（改了的行里也是）、一步撤销；
// 已经格式好、失败（stderr 第一行）、没找到、超时（连子进程一起杀）、跑的时候改了都不动文字，提示里说；
// {file} 换成路径、工作目录是仓库根；"-" 关掉、纯文本没有命令。
func TestFormatDoc(t *testing.T) {
	repo := testutil.NewRepo(t, "fmt")
	repo.Write("a.go", "package a\n\nvar x  = 1\nvar y = 2\n")
	repo.Write("a.txt", "hi\n")
	repo.Commit("base")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	t.Cleanup(a.Close)
	a.St.Settings.Shell = "/bin/sh"
	d := a.OpenFile(a.Repos[0], "a.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	set := func(cmd string) { a.St.Settings.Formatters = map[string]string{"Go": cmd} }
	run := func(what string) {
		t.Helper()
		a.Toast = ""
		a.Format(d)
		if !d.Formatting {
			t.Fatalf("%s：没在后台跑", what)
		}
		wait(what, func() bool { return !d.Formatting })
	}

	set(`sed 's/x  =/x =/'`)
	at := d.Buf.Offset(editor.Pos{Line: 2, Col: 10}) // 改了的那一行的末尾
	d.St.Select(at, at)
	run("只改一行")
	if got := d.Buf.String(); got != "package a\n\nvar x = 1\nvar y = 2\n" || a.Toast != "" {
		t.Fatalf("格式化成 %q，提示 %q", got, a.Toast)
	}
	if p := d.St.CursorPos(); p != (editor.Pos{Line: 2, Col: 9}) {
		t.Errorf("光标跑到了 %+v", p)
	}
	run("再来一次")
	if a.Toast != "已经是格式好的" {
		t.Errorf("已经格式好：%q", a.Toast)
	}

	set("tr a-z A-Z")
	before := d.Buf.String()
	at = d.Buf.Offset(editor.Pos{Line: 2, Col: 9}) // 连着改的两行里上面那行的末尾
	d.St.Select(at, at)
	run("全改")
	if d.Buf.String() != strings.ToUpper(before) {
		t.Fatalf("tr：%q", d.Buf.String())
	}
	if p := d.St.CursorPos(); p != (editor.Pos{Line: 2, Col: 9}) {
		t.Errorf("全改：光标跑到了 %+v", p)
	}
	if !d.Buf.Undo() || d.Buf.String() != before {
		t.Fatalf("一步撤销：%q", d.Buf.String())
	}

	for _, c := range []struct{ cmd, toast string }{
		{"echo 'bad thing' >&2; echo second >&2; exit 1", "格式化失败：bad thing"},
		{"exit 3", "格式化失败：退出码 3"},
		{"no-such-formatter-xyz --flag", "没找到 no-such-formatter-xyz：装上，或在设置里改命令"},
		{"cat >/dev/null", "格式化失败：命令什么都没输出"},
	} {
		set(c.cmd)
		run(c.cmd)
		if a.Toast != c.toast || d.Buf.String() != before {
			t.Errorf("%s：提示 %q，文字 %q", c.cmd, a.Toast, d.Buf.String())
		}
	}
	if last := a.Output[len(a.Output)-1]; !last.Failed || !strings.Contains(last.Title, "格式化 a.go") {
		t.Errorf("失败没记进输出栏：%+v", last)
	}

	old := FormatTimeout
	FormatTimeout = time.Second
	t.Cleanup(func() { FormatTimeout = old })
	set("sleep 20; cat")
	start := time.Now()
	run("超时")
	if a.Toast != "格式化失败：1 秒还没跑完" || d.Buf.String() != before || time.Since(start) > 5*time.Second {
		t.Errorf("超时：提示 %q，用了 %v", a.Toast, time.Since(start))
	}
	FormatTimeout = old

	set("sleep 0.3; tr a-z A-Z")
	a.Format(d)
	d.Buf.Edit(editor.Edit{Start: 0, End: 0, Text: "// 新\n"})
	wait("跑的时候改了", func() bool { return !d.Formatting })
	if a.Toast != "文件改了，没套用格式化" || d.Buf.String() != "// 新\n"+before {
		t.Errorf("跑的时候改了：提示 %q，文字 %q", a.Toast, d.Buf.String())
	}

	set("cat >/dev/null; pwd; echo {file}")
	run("路径")
	root, _ := filepath.EvalSymlinks(repo.Root)
	if got := d.Buf.String(); got != repo.Root+"\n"+d.Abs+"\n" && got != root+"\n"+d.Abs+"\n" { // pwd 可能写 /private/var
		t.Errorf("工作目录、{file}：%q", got)
	}

	set(FormatOff)
	a.Format(d)
	if d.Formatting || a.Toast != "Go 的格式化在设置里关掉了" {
		t.Errorf("关掉了：%v %q", d.Formatting, a.Toast)
	}
	a.St.Settings.Formatters = nil
	if a.FormatterOf("Go") != "gofmt" || a.FormatterOf("Kotlin") != "" {
		t.Errorf("内置默认：%q %q", a.FormatterOf("Go"), a.FormatterOf("Kotlin"))
	}
	txt := a.OpenFile(a.Repos[0], "a.txt", true)
	wait("读好 a.txt", func() bool { return txt.Buf != nil && !txt.Loading })
	a.Format(txt)
	if txt.Formatting || a.Toast != "没有 纯文本 的格式化命令（设置里加）" {
		t.Errorf("纯文本：%v %q", txt.Formatting, a.Toast)
	}
}

// 保存时格式化：⌘S 先格式化再存；失败照样存、同一个标签成功前只提示一次；⌥⌘S 一个一个格式化、存；
// 退出确认里的全部保存（SaveAll）不等格式化；格式化着的时候关了标签就不存。
func TestFormatOnSave(t *testing.T) {
	repo := testutil.NewRepo(t, "fmt")
	repo.Write("a.go", "package a\n")
	repo.Write("b.go", "package b\n")
	repo.Commit("base")
	a, wait := newTestApp(t, testutil.Workspace(t, repo.Root))
	t.Cleanup(a.Close)
	s := &a.St.Settings
	s.Shell, s.FormatOnSave = "/bin/sh", true
	s.Formatters = map[string]string{"Go": "tr a-z A-Z"}
	open := func(name string) *docs.Doc {
		d := a.OpenFile(a.Repos[0], name, true)
		wait("读好 "+name, func() bool { return d.Buf != nil && !d.Loading })
		return d
	}
	disk := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(repo.Root, name))
		return string(b)
	}
	d := open("a.go")
	edit := func(d *docs.Doc, text string) {
		d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: text})
	}
	saved := func(d *docs.Doc) func() bool { return func() bool { return !d.Formatting && !d.Modified() } }

	edit(d, "var x = 1\n")
	a.Save(d)
	if !d.Formatting || disk("a.go") != "package a\n" {
		t.Fatal("没先格式化")
	}
	a.Save(d) // 格式化着再按：不另起
	wait("格式化后存", saved(d))
	if disk("a.go") != "PACKAGE A\nVAR X = 1\n" || a.Toast != "已保存 a.go" {
		t.Fatalf("存的是 %q，提示 %q", disk("a.go"), a.Toast)
	}

	s.Formatters["Go"] = "echo nope >&2; exit 1"
	edit(d, "y\n")
	a.Save(d)
	wait("失败照存", saved(d))
	if disk("a.go") != "PACKAGE A\nVAR X = 1\ny\n" || a.Toast != "已保存 a.go · 格式化失败：nope" || !d.FormatWarned {
		t.Fatalf("失败：%q，提示 %q", disk("a.go"), a.Toast)
	}
	edit(d, "z\n")
	a.Save(d)
	wait("又失败", saved(d))
	if a.Toast != "已保存 a.go" {
		t.Errorf("第二次失败还提示：%q", a.Toast)
	}
	s.Formatters["Go"] = "tr a-z A-Z"
	edit(d, "w\n")
	a.Save(d)
	wait("好了", saved(d))
	if d.FormatWarned || disk("a.go") != "PACKAGE A\nVAR X = 1\nY\nZ\nW\n" {
		t.Errorf("成功后：%v %q", d.FormatWarned, disk("a.go"))
	}

	// ⌥⌘S：两个标签一个一个来
	b := open("b.go")
	edit(d, "q\n")
	edit(b, "r\n")
	a.SaveEach()
	wait("全部保存", func() bool { return a.DirtyDocs() == 0 && !d.Formatting && !b.Formatting })
	if disk("a.go") != "PACKAGE A\nVAR X = 1\nY\nZ\nW\nQ\n" || disk("b.go") != "PACKAGE B\nR\n" {
		t.Errorf("⌥⌘S：%q %q", disk("a.go"), disk("b.go"))
	}

	// 退出确认的全部保存：马上写，不格式化
	edit(b, "s\n")
	a.SaveAll()
	if b.Formatting || b.Modified() || disk("b.go") != "PACKAGE B\nR\ns\n" {
		t.Errorf("退出时的保存：%v %q", b.Formatting, disk("b.go"))
	}

	// 格式化着的时候关了：不存
	s.Formatters["Go"] = "sleep 0.3; cat"
	edit(b, "t\n")
	a.Save(b)
	a.RemoveDoc(b)
	wait("关了", func() bool { return !b.Formatting })
	if disk("b.go") != "PACKAGE B\nR\ns\n" {
		t.Errorf("关了还存了：%q", disk("b.go"))
	}

	// 没有命令的语言照常存
	s.Formatters["Go"] = FormatOff
	edit(d, "plain\n")
	a.Save(d)
	if d.Formatting || d.Modified() {
		t.Error("关掉格式化的语言没马上存")
	}
}
