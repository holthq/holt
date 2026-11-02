package docs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/testutil"
)

// openAs 按 enc（"" 为自动认）读 d，像"用别的编码重新打开"。
func openAs(t *testing.T, d *Doc, enc Encoding) {
	t.Helper()
	d.ForcedEnc = enc
	gen := d.Start()
	if !d.ApplyFile(gen, ReadFileAs(d.Abs, d.Root, d.Rel, d.Buf == nil || d.Big, enc)) {
		t.Fatal("ApplyFile 丢了结果")
	}
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// 老编码能编辑：样本先和 python 编出来的一样（SHA-256 是 python 算好的，脚本 v172/pyref.py），
// 读进来能编辑，改一个字存回和 python 编改过的那份逐字节一样。EUC-JP、Windows-1252、ISO-8859-1 不自动认，手动选。
func TestLegacyEdit(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	for _, c := range []struct {
		enc            Encoding
		auto           bool
		text, old, new string
		orig, edited   string
	}{
		{Big5, true, "# 設定說明\n# 這是一個用 Big5 編碼儲存的繁體中文設定檔，用來檢查編碼識別。\nname = \"工作臺\"\n", "工作臺", "工作檯",
			"20f9bc04abc445898c7a4f45d3ac141a5b479b13e3b53e88ae5972cbd95bdcfe", "8a633681ec906eb0ed312ff697acb4f092a939177b3a806ef985bcec6013c398"},
		{ShiftJIS, true, "# 設定ファイル\n# これは Shift_JIS で保存された日本語のコメントです。\nname = \"作業台\"\n", "作業台", "作業机",
			"d8a3c6825387a5a8c10752613dc1eee0002c26965028b4cfc52974fa52a6aed2", "35507b36ae6a4f35cc42176c8ded1b0e26e8b7784ca3da5bcafd3a24d1e4db21"},
		{EUCKR, true, "# 설정 파일\n# 이것은 EUC-KR로 저장된 한국어 주석입니다.\nname = \"작업대\"\n", "작업대", "작업실",
			"0fe99467f9db87f60eade60897e87ded318f9b112f64560808a084554ed1dc86", "e5e199225ad6d512400f6799aa158a8770759e012937c5bfea710b7606224481"},
		{EUCJP, false, "# 設定ファイル\n# これは EUC-JP で保存された日本語のコメントです。\nname = \"作業台\"\n", "作業台", "作業机",
			"fe8b7a33f41c9555082d29e1b0a647e90ae639057bbbfcf943211118947855ad", "9fb9fe913755e01efa2a83931de1f1056ff5b8c6d6fe6a12cd998eae856979a3"},
		{Windows1252, false, "# Réglages\n# Café crème, naïve façade — prix 5 € « déjà vu ».\nname = \"Größe\"\n", "Größe", "Grüße",
			"960d582559a42fe37dcb065efcbce9d5e4bca0530ebd475498eb26f003457b7f", "73854334791a9c590221994d6c28ab38a8d47e7009eb10ca01314de82defb004"},
		{ISO88591, false, "# Réglages\n# Café crème, naïve façade, ½ © ±.\nname = \"Größe\"\n", "Größe", "Grüße",
			"9bba1b3c55fcbbee263fdc8b6c81e2a77bca6a127068aa554cb3e7d871e2ab94", "776cbca5f234a8c07747cd3c45ac5d98b7d7b197e13ec8b4b217b04b425734cb"},
	} {
		data := Encode(c.text, c.enc)
		if sum(data) != c.orig {
			t.Errorf("%s：样本和 python 编的不一样", c.enc)
			continue
		}
		if !c.enc.Writable() || !c.enc.Convertible() || c.enc.Family() != c.enc || Unencodable(c.text, c.enc) != 0 {
			t.Errorf("%s：Writable %v Convertible %v Family %q", c.enc, c.enc.Writable(), c.enc.Convertible(), c.enc.Family())
		}
		name := strings.ToLower(string(c.enc)) + ".txt"
		os.WriteFile(filepath.Join(root, name), data, 0o644)
		d := NewFile(root, name)
		if c.auto {
			open(t, d)
		} else {
			openAs(t, d, c.enc)
		}
		if d.Enc != c.enc || !d.Editable() || d.Lossy || d.Broken || d.St.ReadOnly || d.Buf.String() != c.text {
			t.Errorf("%s：读成 %q，能编辑 %v，%q", c.enc, d.Enc, d.Editable(), d.Buf.String())
			continue
		}
		i := strings.Index(d.Buf.String(), c.old)
		d.Buf.Edit(editor.Edit{Start: i, End: i + len(c.old), Text: c.new})
		if err := d.Write(); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(d.Abs); sum(got) != c.edited {
			t.Errorf("%s：改一个字存回和 python 编的不一样：% x", c.enc, got)
		}
	}
}

// 编不回原样的只读：Big5 里单独的 0x80 解不通（按 Big5 读是 Broken）；Shift_JIS 里的解得通（U+0080）但编不回去（Lossy）；
// Windows-1252 没定义的 0x81 解不通。都不能存；编不回原样的可以转成 UTF-8，解不通的不能（会写进替换字符）。
func TestLegacyReadOnly(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	write := func(name string, data []byte) *Doc {
		os.WriteFile(filepath.Join(root, name), data, 0o644)
		return NewFile(root, name)
	}
	tw := append(Encode(zhtText, Big5), 0x80, '\n')
	d := write("tw.txt", tw)
	open(t, d)
	if d.Editable() || !d.St.ReadOnly {
		t.Errorf("Big5 + 0x80 自动认成 %q，能编辑 %v", d.Enc, d.Editable())
	}
	openAs(t, d, Big5)
	if d.Enc != Big5 || !d.Broken || d.Editable() || !d.St.ReadOnly || d.Convertible() {
		t.Errorf("按 Big5：%q broken %v 能编辑 %v", d.Enc, d.Broken, d.Editable())
	}

	ja := append(Encode(jaText, ShiftJIS), 0x80, '\n')
	j := write("ja.txt", ja)
	open(t, j)
	if j.Enc != ShiftJIS || !j.Lossy || j.Broken || j.Editable() || !j.St.ReadOnly || !j.Convertible() {
		t.Errorf("Shift_JIS + 0x80：%q lossy %v broken %v 能编辑 %v", j.Enc, j.Lossy, j.Broken, j.Editable())
	}
	if err := j.Write(); err == nil {
		t.Error("编不回原样的 Shift_JIS 不该能保存")
	}

	w := write("w.txt", []byte("caf\xe9 \x81\n"))
	openAs(t, w, Windows1252)
	if w.Enc != Windows1252 || !w.Broken || w.Editable() {
		t.Errorf("Windows-1252 + 0x81：%q broken %v", w.Enc, w.Broken)
	}
	l := write("l.txt", []byte("caf\xe9 \x81\n")) // ISO-8859-1 每个字节都有字（0x81 是 U+0081），来回一样
	openAs(t, l, ISO88591)
	if l.Enc != ISO88591 || l.Broken || l.Lossy || !l.Editable() || l.Buf.String() != "café \u0081\n" {
		t.Errorf("ISO-8859-1：%q broken %v lossy %v %q", l.Enc, l.Broken, l.Lossy, l.Buf.String())
	}
	for name, want := range map[string][]byte{"tw.txt": tw, "ja.txt": ja} {
		if got, _ := os.ReadFile(filepath.Join(root, name)); !bytes.Equal(got, want) {
			t.Errorf("%s 被改了", name)
		}
	}
}

// 写不进去的字：Unencodable 数得出（每个字都算，同一个字只编一次）；保存报 UnencodableError（N、编码），文件不动；
// 转成 UTF-8 照样能存。GB18030、UTF-8 什么都写得进去。
func TestUnencodable(t *testing.T) {
	for _, c := range []struct {
		text string
		enc  Encoding
		n    int
	}{
		{"€😀a€", ISO88591, 3},
		{"€ café", Windows1252, 0},
		{"作業😀😀台", ShiftJIS, 2},
		{"한국어😀", EUCKR, 1},
		{"中😀�", GB18030, 0},
		{"😀", UTF8, 0},
		{"😀", UTF16LE, 0},
	} {
		if got := Unencodable(c.text, c.enc); got != c.n {
			t.Errorf("Unencodable(%q, %s) = %d，应为 %d", c.text, c.enc, got, c.n)
		}
	}

	root, _ := testutil.TempRepo(t)
	data := Encode(jaText+"\n", ShiftJIS)
	os.WriteFile(filepath.Join(root, "ja.txt"), data, 0o644)
	d := NewFile(root, "ja.txt")
	open(t, d)
	if d.Enc != ShiftJIS || !d.Editable() {
		t.Fatalf("读成 %q，能编辑 %v", d.Enc, d.Editable())
	}
	d.Buf.Edit(editor.Edit{Start: 0, End: 0, Text: "😀"})
	err := d.Write()
	if ue := (*UnencodableError)(nil); !errors.As(err, &ue) || ue.N != 1 || ue.Enc != ShiftJIS || err.Error() != "有 1 个字 Shift_JIS 写不了" {
		t.Fatalf("存带 emoji 的：%v", err)
	}
	if got, _ := os.ReadFile(d.Abs); !bytes.Equal(got, data) || !d.Modified() {
		t.Error("没存上也把文件改了")
	}
	if err := d.ConvertUTF8(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(d.Abs); string(got) != "😀"+jaText+"\n" || d.Enc != UTF8 || d.Modified() {
		t.Errorf("转成 UTF-8 存：%q %q", got, d.Enc)
	}
	if !slices.Equal(Choices, []Encoding{UTF8, GB18030, Big5, ShiftJIS, EUCKR, EUCJP, Windows1252, ISO88591, UTF16LE, UTF16BE}) {
		t.Errorf("Choices %q", Choices)
	}
}
