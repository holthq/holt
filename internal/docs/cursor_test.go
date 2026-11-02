package docs

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

// 状态栏的行列：列按字数（中文一个字算一列，Tab 算一个），选中的字数按 rune（跨行的换行算一个），几处选区。
func TestCursorInfo(t *testing.T) {
	d := &Doc{Kind: File, Title: "a.txt", Buf: editor.NewBuffer("中文\tab\n第二行\n")}
	d.St.Buffer = d.Buf
	at := len("中文\ta")
	d.St.Select(at, at)
	if line, col, sels, chars := d.CursorInfo(); line != 1 || col != 5 || sels != 1 || chars != 0 {
		t.Errorf("光标：%d %d %d %d", line, col, sels, chars)
	}
	d.St.Select(len("中"), len("中文\tab\n第"))
	if line, col, sels, chars := d.CursorInfo(); line != 2 || col != 2 || sels != 1 || chars != 6 {
		t.Errorf("跨行选中：%d %d %d %d", line, col, sels, chars)
	}
	d.St.SetSelections(editor.Selection{Anchor: 0, Head: len("中")}, editor.Selection{Anchor: at, Head: at})
	if line, col, sels, chars := d.CursorInfo(); line != 1 || col != 5 || sels != 2 || chars != 1 {
		t.Errorf("两处：%d %d %d %d", line, col, sels, chars)
	}
	d.Buf.Edit(editor.Edit{Start: 0, End: 0, Text: "x"}) // 内容变了重算
	if _, col, _, _ := d.CursorInfo(); col != 6 {
		t.Errorf("改了以后列 %d", col)
	}
}

// 缩进换算：Tab 对齐到 size 的整数倍，凑不满一格的留成空格。
func TestReindent(t *testing.T) {
	for _, c := range []struct {
		lead   string
		size   int
		toTabs bool
		want   string
	}{
		{"\t", 4, false, "    "},
		{"  \t", 4, false, "    "},
		{"\t  ", 2, false, "    "},
		{"      ", 4, true, "\t  "},
		{"    \t", 4, true, "\t\t"},
		{"\t", 8, true, "\t"},
	} {
		if got := Reindent(c.lead, c.size, c.toTabs); got != c.want {
			t.Errorf("Reindent(%q, %d, %v) = %q，应为 %q", c.lead, c.size, c.toTabs, got, c.want)
		}
	}
}

// 换行：换成 CRLF 算改过（mygo 的 SetLineEnding 不动版本），换回来不算；大文件选语言也走后台高亮。
func TestEOLAndLang(t *testing.T) {
	d := &Doc{Kind: File, Title: "a.txt", Buf: editor.NewBuffer("a\nb\n")}
	d.SetEOL("\r\n")
	if !d.Modified() || d.Buf.LineEnding() != "\r\n" {
		t.Fatal("换成 CRLF 应算改过")
	}
	d.SetEOL("\n")
	if d.Modified() {
		t.Fatal("换回 LF 不算改过")
	}
	d.Big = true
	d.SetLang("Go")
	if l := d.Buf.Language(); l == nil || l.Name != "Go" || l.Highlighter == nil {
		t.Errorf("大文件选 Go：%+v", l)
	}
	d.SetLang("没有这种语言")
	if d.Lang != "" || d.Buf.Language() != nil {
		t.Errorf("不认识的语言回到按文件名：%q", d.Lang)
	}
}
