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
	"github.com/lemoeade/grove/internal/i18n"
	"github.com/lemoeade/grove/internal/snippets"
	"github.com/lemoeade/grove/internal/testutil"
)

// selTexts 是 d 现在的几处选区的文字，主选区在前（mygo 的主选区是最后一个）。
func selTexts(d *docs.Doc) []string {
	sels := d.St.Selections()
	out := make([]string, len(sels))
	for i, s := range sels {
		out[len(sels)-1-i] = d.Buf.Slice(s.Start(), s.End())
	}
	return out
}

// 补全里的片段：敲 "fo" 给 fori、forr，展开时第二行起补当前行的缩进、行首 Tab 跟文档的缩进；
// 接了补全（缓冲区里出现那段展开）事后认出来，$1 的三处一起选中；三处一起打字跟着挪；Tab 到 $2、⇧Tab 回来，
// 到 $0 光标放过去、会话结束；会话里不给补全；改在片段外、光标跑出片段、别处插入都结束；光标去别的行，那批作废。
func TestSnippetCompletion(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "x.go"), []byte("package main\n\nfunc main() {\n\tfo\n}\n"), 0o644)
	a, wait := newTestAppIn(t, ws, t.TempDir())
	r := a.Repos[0]
	d := a.OpenFile(r, "x.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	if !a.CanRun(Find("edit.insertSnippet")) {
		t.Fatal("Go 文件应能插片段")
	}
	off := d.Buf.Offset(editor.Pos{Line: 3, Col: 3})
	d.St.Select(off, off)
	a.SnippetCompletions(d, editor.CompletionRequest{Offset: off, Prefix: "fo", Version: d.Buf.Version()})
	o := d.SnipOffer
	if o == nil || o.Start != off-2 || len(o.Items) != 2 {
		t.Fatalf("给的补全：%+v", o)
	}
	fori := o.Items[0]
	if want := "for i := 0; i < n; i++ {\n\t\t\n\t}"; fori.Text != want {
		t.Fatalf("fori 展开成 %q", fori.Text)
	}

	// 照 mygo 接补全的做法：换掉 "fo"，光标放在末尾
	d.Buf.Edit(editor.Edit{Start: o.Start, End: off, Text: fori.Text})
	end := o.Start + len(fori.Text)
	d.St.Select(end, end)
	a.SnipTick(d)
	if d.Snip == nil || d.SnipOffer != nil {
		t.Fatal("接了补全没开跳位")
	}
	if got := selTexts(d); !slices.Equal(got, []string{"i", "i", "i"}) || SnippetStatus(d) != i18n.Tf("片段 %d/%d", 1, 2) {
		t.Fatalf("$1：%q，%s", got, SnippetStatus(d))
	}
	a.SnippetCompletions(d, editor.CompletionRequest{Offset: d.St.Cursor(), Prefix: "f", Version: d.Buf.Version()})
	if d.SnipOffer != nil {
		t.Error("跳位会话里不该给补全")
	}

	// 三处一起打 idx（多光标打字）
	var edits []editor.Edit
	for _, s := range d.St.Selections() {
		edits = append(edits, editor.Edit{Start: s.Start(), End: s.End(), Text: "idx"})
	}
	slices.SortFunc(edits, func(x, y editor.Edit) int { return x.Start - y.Start })
	d.Buf.Edit(edits...)
	a.SnipTick(d)
	if d.Snip == nil {
		t.Fatal("在 $1 里打字不该结束")
	}
	if !a.SnipJump(d, 1) || !slices.Equal(selTexts(d), []string{"n"}) || SnippetStatus(d) != i18n.Tf("片段 %d/%d", 2, 2) {
		t.Fatalf("Tab 到 $2：%q %s", selTexts(d), SnippetStatus(d))
	}
	if !a.SnipJump(d, -1) || !slices.Equal(selTexts(d), []string{"idx", "idx", "idx"}) {
		t.Fatalf("⇧Tab 回 $1：%q", selTexts(d))
	}
	a.SnipJump(d, 1)
	if !a.SnipJump(d, 1) || d.Snip != nil {
		t.Fatal("到 $0 会话该结束")
	}
	if p := d.St.CursorPos(); p != (editor.Pos{Line: 4, Col: 2}) || !strings.Contains(d.Buf.String(), "\tfor idx := 0; idx < n; idx++ {\n\t\t\n\t}\n}") {
		t.Fatalf("$0 在 %+v：%q", p, d.Buf.String())
	}
	if a.SnipJump(d, 1) {
		t.Error("没有会话时 Tab 该照编辑器的")
	}

	// 结束会话的几种：改在片段外、光标跑出片段
	fn := func() { // 在第一行末尾插 func，会话停在 $1
		a.InsertSnippet(d, snipNamed(t, a, "go", "func"))
		if d.Snip == nil {
			t.Fatal("插入片段没开跳位")
		}
	}
	d.St.Select(0, 0)
	fn()
	d.Buf.Edit(editor.Edit{Start: d.Buf.Len(), End: d.Buf.Len(), Text: "// x\n"})
	if a.SnipTick(d); d.Snip != nil {
		t.Error("片段外改了会话还在")
	}
	d.St.Select(0, 0)
	fn()
	d.St.Select(d.Buf.Len(), d.Buf.Len())
	if a.SnipTick(d); d.Snip != nil {
		t.Error("光标跑出片段会话还在")
	}
	d.St.Select(0, 0)
	fn()
	a.EndSnippet(d)
	if d.Snip != nil || SnippetStatus(d) != "" {
		t.Error("Esc 没结束")
	}

	// 补全给了没接：光标去了别的行，那批作废；敲字不到一个、不是 ⌃Space 不给；⌃Space 空着给全部
	at := d.Buf.Len()
	d.Buf.Edit(editor.Edit{Start: at, End: at, Text: "ma"})
	d.St.Select(at+2, at+2)
	a.SnippetCompletions(d, editor.CompletionRequest{Offset: at + 2, Prefix: "ma", Version: d.Buf.Version()})
	if d.SnipOffer == nil || len(d.SnipOffer.Items) != 1 {
		t.Fatalf("ma：%+v", d.SnipOffer)
	}
	d.Buf.Edit(editor.Edit{Start: at + 2, End: at + 2, Text: "i"}) // 接着敲：还在那个词里，留着
	d.St.Select(at+3, at+3)
	if a.SnipTick(d); d.SnipOffer == nil || d.Snip != nil {
		t.Fatal("接着敲词不该作废")
	}
	d.St.Select(0, 0)
	if a.SnipTick(d); d.SnipOffer != nil {
		t.Error("光标去了别的行还留着")
	}
	a.SnippetCompletions(d, editor.CompletionRequest{Offset: 0, Version: d.Buf.Version()})
	if d.SnipOffer != nil {
		t.Error("敲字时词是空的不该给")
	}
	a.SnippetCompletions(d, editor.CompletionRequest{Offset: 0, Version: d.Buf.Version(), Explicit: true})
	if d.SnipOffer == nil || len(d.SnipOffer.Items) != len(a.Snippets().For("go")) {
		t.Errorf("⌃Space 该给全部：%+v", d.SnipOffer)
	}
}

// snipNamed 是 lang 里名字是 name 的片段。
func snipNamed(t *testing.T, a *App, lang, name string) (sn snippets.Snippet) {
	t.Helper()
	for _, x := range a.Snippets().For(lang) {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("没有 %s/%s", lang, name)
	return sn
}

// 片段目录、命令面板：没有文件时只有内置的、"插入片段…"在没有能改的文件时不能用；打开片段目录建目录、
// 复制内置的当样板、在访达里打开（测试里换掉）；加一个文件用到时自己重读（不用重启），坏文件记下来；
// ">snip 前缀" 列出来，回车插到光标处、选中 $1；命令打开带 ">snip " 的面板。
func TestSnippetPalette(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "y.go"), []byte("package main\n\n"), 0o644)
	stateDir := t.TempDir()
	a, wait := newTestAppIn(t, ws, stateDir)
	dir := filepath.Join(stateDir, "snippets")
	if a.SnippetDir() != dir || a.SnippetDirExists() || a.CanRun(Find("edit.insertSnippet")) {
		t.Fatalf("片段目录 %s，建了 %v", a.SnippetDir(), a.SnippetDirExists())
	}
	if _, note := a.paletteItems(">snip "); note != i18n.T("当前标签不是能改的文件，插不了片段") {
		t.Errorf("没有标签时的提示：%q", note)
	}
	var shown string
	a.ShowPath = func(p string) error { shown = p; return nil }
	a.OpenSnippetDir()
	if names, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(names) != 5 || shown != dir {
		t.Fatalf("打开片段目录：%d 个文件，打开了 %q", len(names), shown)
	}

	os.WriteFile(filepath.Join(dir, "go.json"), []byte(`{"hello": {"prefix": "hel", "body": "fmt.Println(\"${1:hi}\")$0", "description": "打招呼"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "rust.json"), []byte(`{`), 0o644)
	a.snipCheck = time.Time{} // 不等那一秒
	set := a.Snippets()
	if !slices.ContainsFunc(set.For("go"), func(sn snippets.Snippet) bool { return sn.Prefix == "hel" && sn.File }) || len(set.Errs) != 1 {
		t.Fatalf("改了文件没重读：%q", set.Errs)
	}

	r := a.Repos[0]
	d := a.OpenFile(r, "y.go", true)
	wait("读好", func() bool { return d.Buf != nil && !d.Loading })
	d.St.Select(d.Buf.Len(), d.Buf.Len())
	a.Exec("edit.insertSnippet")
	if !a.Palette.Open || a.Palette.Text != ">snip " {
		t.Fatalf("命令该打开 >snip 面板：%q", a.Palette.Text)
	}
	a.Palette.Text = ">snip hel"
	a.UpdatePalette()
	if len(a.Palette.Items) == 0 || a.Palette.Items[0].Kind != ItemSnippet || a.Palette.Items[0].Text != "hel" || a.Palette.Items[0].Detail != "打招呼" {
		t.Fatalf("面板：%+v", a.Palette.Items)
	}
	a.FocusDoc = false
	a.AcceptPalette(0)
	if a.Palette.Open || !a.FocusDoc || d.Buf.String() != "package main\n\nfmt.Println(\"hi\")" || d.Snip == nil || !slices.Equal(selTexts(d), []string{"hi"}) {
		t.Fatalf("插入：%q，会话 %v，选中 %q", d.Buf.String(), d.Snip != nil, selTexts(d))
	}
	a.ReloadSnippets()
	if a.snips == nil || len(a.snips.Errs) != 1 {
		t.Error("重新加载")
	}
}
