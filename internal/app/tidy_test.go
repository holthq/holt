package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/traditionalchinese"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
)

// 保存时整理：去行尾空白（光标所在的行、Markdown 的两空格硬换行不动）、补末尾换行（CRLF 的文件补 CRLF），
// 一步撤销；只读的不动；"停手后"的自动保存不整理，"切走时"的整理。
func TestTidy(t *testing.T) {
	root, ws := testutil.TempRepo(t)
	os.WriteFile(filepath.Join(root, "t.txt"), []byte("a  \r\nb\t\r\nc"), 0o644)
	os.WriteFile(filepath.Join(root, "n.md"), []byte("硬换行  \n三个   \n"), 0o644)
	gbText, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte("中文  \n"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "gb.txt"), append(gbText, 0x80, '\n'), 0o644) // 单独的 0x80 编不回原样：只读
	a, wait := newTestApp(t, ws)
	r := a.Current()
	d, md, gb := a.OpenFile(r, "t.txt", true), a.OpenFile(r, "n.md", true), a.OpenFile(r, "gb.txt", true)
	wait("读文件", func() bool { return !d.Loading && !md.Loading && !gb.Loading })
	disk := func(name string) string { return mustRead(t, filepath.Join(root, name)) }

	// 默认两个都关着：原样保存
	if err := a.WriteDoc(d, true); err != nil || disk("t.txt") != "a  \r\nb\t\r\nc" || a.Toast != "已保存 t.txt" {
		t.Fatalf("默认不该整理：%v %q %q", err, disk("t.txt"), a.Toast)
	}

	a.St.Settings.TrimTrailing, a.St.Settings.FinalNewline = true, true
	d.St.Select(4, 4) // 光标在第二行
	if err := a.WriteDoc(d, true); err != nil {
		t.Fatal(err)
	}
	if got := disk("t.txt"); got != "a\r\nb\t\r\nc\r\n" || a.Toast != "已保存 t.txt · 整理了 2 行" {
		t.Fatalf("整理后：%q %q", got, a.Toast)
	}
	if !d.Buf.Undo() || d.Buf.String() != "a  \nb\t\nc" || !d.Modified() {
		t.Fatalf("一步撤销回不去：%q", d.Buf.String())
	}

	// Markdown：行尾正好两个空格的是硬换行，留着
	md.St.Select(md.Buf.Len(), md.Buf.Len())
	if err := a.WriteDoc(md, true); err != nil || disk("n.md") != "硬换行  \n三个\n" {
		t.Fatalf("Markdown：%v %q", err, disk("n.md"))
	}

	// 只读的（这里是编不回原样的 Big5，太短自己认成 GB18030，手动换成 Big5）不动
	a.ReopenWithEncoding(gb, docs.Big5)
	wait("按 Big5 重读", func() bool { return !gb.Loading && gb.Enc == docs.Big5 })
	before := gb.Buf.String()
	if n := a.TidyDoc(gb); n != 0 || gb.Buf.String() != before || !strings.HasPrefix(before, "中文  \n") || gb.Editable() {
		t.Fatalf("只读的被整理了：%d %q", n, gb.Buf.String())
	}

	// 最后一行既去尾空白又补换行：算一行
	d.Buf.Set("a\nb  ")
	d.St.Select(0, 0)
	if n := a.TidyDoc(d); n != 1 || d.Buf.String() != "a\nb\n" {
		t.Fatalf("最后一行：%d %q", n, d.Buf.String())
	}

	// "停手后"的自动保存不整理（打字中间停一下）
	a.St.Settings.AutoSave = "delay"
	d.Buf.Set("x  \ny  \n")
	d.St.Select(0, 0)
	a.AutoSaveTick()
	a.autoVer[d] = autoSave{d.Buf.Version(), time.Now().Add(-AutoSaveDelay)}
	a.AutoSaveTick()
	if got := disk("t.txt"); got != "x  \r\ny  \r\n" || d.Modified() {
		t.Fatalf("停手后的自动保存：%q", got)
	}

	// "切走时"的整理（光标所在的第一行不动）
	a.St.Settings.AutoSave = "blur"
	d.Buf.Set("x  \ny  \nz")
	d.St.Select(0, 0)
	a.OnBlur()
	if got := disk("t.txt"); got != "x  \r\ny\r\nz\r\n" {
		t.Fatalf("切走时的自动保存：%q", got)
	}
	wait("空闲", idle(r))
}
