package docs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/highlight"
	"github.com/holthq/holt/internal/testutil"
)

// bigText 是至少 n 字节、像 Go 代码的文字，里面有冲突标记（大文件不该去扫）。
func bigText(n int) string {
	var b strings.Builder
	b.WriteString("package big\n\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n")
	for i := 0; b.Len() < n; i++ {
		b.WriteString("// 一行注释，凑够大小\nfunc f() int { return 1 }\n")
	}
	return b.String()
}

// 大文件（8–64 MB）：读得到、在后台解码好、建好缓冲区，不读 HEAD 版本；超过 64 MB 的文本、超过 8 MB 的图片不读。
func TestReadFileBig(t *testing.T) {
	r := testutil.NewRepo(t, "big")
	text := bigText(MaxEditBytes + 1<<20)
	r.Write("big.go", text)
	r.Write("small.go", "package small\n")
	r.Commit("大文件")
	abs := filepath.Join(r.Root, "big.go")

	l := ReadFile(abs, r.Root, "big.go", true)
	if l.Err != nil || l.Note != "" {
		t.Fatalf("9 MB 应能读：err=%v note=%q", l.Err, l.Note)
	}
	if !l.Big || l.Editable || l.Enc != UTF8 || l.Text != text || l.Data != nil || l.Buf == nil || l.Buf.Len() != len(text) {
		t.Errorf("大文件：big=%v editable=%v enc=%v data=%d buf=%v", l.Big, l.Editable, l.Enc, len(l.Data), l.Buf != nil)
	}
	if l.Base != nil || l.BaseSum.OK {
		t.Error("大文件不该读 HEAD 版本")
	}
	// 重读已经开着的小文件（fresh 为假）不在后台建缓冲区；HEAD 版本照读
	l = ReadFile(filepath.Join(r.Root, "small.go"), r.Root, "small.go", false)
	if l.Big || !l.Editable || l.Buf != nil || l.Text != "package small\n" || l.Base == nil || l.Base.String() != "package small\n" || !l.BaseSum.OK {
		t.Errorf("小文件：big=%v buf=%v base=%v", l.Big, l.Buf != nil, l.Base != nil)
	}

	// 超过 64 MB：稀疏文件，不真的写 65 MB
	huge := filepath.Join(r.Root, "huge.log")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(MaxViewBytes + 1)
	f.Close()
	if l := ReadFile(huge, r.Root, "huge.log", true); l.Note != "文件超过 64 MB，用默认应用打开" || l.Err != nil || l.Buf != nil {
		t.Errorf("65 MB：note=%q err=%v", l.Note, l.Err)
	}
	// 超过 8 MB 的图片照旧不打开
	png := filepath.Join(r.Root, "big.png")
	f, _ = os.Create(png)
	f.Truncate(MaxEditBytes + 1)
	f.Close()
	if l := ReadFile(png, r.Root, "big.png", true); l.Note != "文件超过 8 MB，不打开" || l.Data != nil {
		t.Errorf("9 MB 图片：note=%q", l.Note)
	}
}

// 大文件进标签：只读、Big，冲突标记块 / 大纲 / Markdown 预览都没有，不能保存，高亮在后台算（highlight.For）；
// 重读换上后台建好的缓冲区，光标留在原来那行，原来在最后一行的跟到新的最后一行。
func TestApplyFileBig(t *testing.T) {
	r := testutil.NewRepo(t, "big")
	text := bigText(MaxEditBytes + 1<<20)
	r.Write("big.go", text)
	r.Write("big.md", "# 标题\n"+text)
	r.Commit("大文件")
	d := NewFile(r.Root, "big.go")
	open(t, d)
	if d.Info != "" || !d.Big || !d.St.ReadOnly || d.Editable() || d.Base != nil || d.St.DiffBase != nil || d.Buf.Language() == nil {
		t.Fatalf("大文件标签：info=%q big=%v readOnly=%v editable=%v", d.Info, d.Big, d.St.ReadOnly, d.Editable())
	}
	if d.ConflictBlocks() != nil || d.Outline() != nil {
		t.Error("大文件不该扫冲突标记、解析大纲")
	}
	if d.Buf.Language() != highlight.For("big.go") || d.Buf.Language().Highlighter == nil {
		t.Error("大文件的高亮应在后台算")
	}
	if err := d.Write(); err == nil {
		t.Error("大文件不该能保存")
	}

	// 光标在中间：重读后还在那行
	d.St.GoTo(editor.Pos{Line: 1000, Col: 3})
	buf := d.Buf
	open(t, d)
	if d.Buf == buf || d.St.Buffer != d.Buf {
		t.Fatal("大文件重读应换上后台建好的缓冲区")
	}
	if p := d.St.CursorPos(); p.Line != 1000 || p.Col != 3 {
		t.Errorf("重读后光标 %+v，应还在第 1000 行", p)
	}
	// 光标在最后一行、文件长了：跟到新的最后一行
	d.St.GoTo(editor.Pos{Line: d.Buf.LineCount() - 1})
	if err := os.WriteFile(d.Abs, []byte(text+"// 追加的\n// 两行\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	open(t, d)
	if p := d.St.CursorPos(); p.Line != d.Buf.LineCount()-1 || !strings.HasSuffix(d.Buf.String(), "// 两行\n") {
		t.Errorf("重读后光标 %+v，应在最后一行 %d", p, d.Buf.LineCount()-1)
	}
	if d.Buf.Language() != highlight.For("big.go") {
		t.Error("重读换上的缓冲区也该在后台高亮")
	}

	md := NewFile(r.Root, "big.md")
	open(t, md)
	if !md.Big || md.CanPreview() || md.MarkdownSource() != "" {
		t.Error("大的 Markdown 不该能预览")
	}
}

// 历史版本里的大文件：只读、Big，不读父提交里的版本。
func TestReadVersionBig(t *testing.T) {
	r := testutil.NewRepo(t, "big")
	r.Write("big.go", "package big\n")
	r.Commit("小")
	r.Write("big.go", bigText(MaxEditBytes+1<<20))
	r.Commit("大")
	cs, err := git.Log(context.Background(), r.Root, 0, 1, git.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := git.CommitFiles(context.Background(), r.Root, cs[0])
	d := NewVersion(r.Root, files[0])
	gen := d.Start()
	d.ApplyVersion(gen, ReadVersion(r.Root, files[0], true))
	if !d.Big || !d.St.ReadOnly || d.Base != nil || d.Buf == nil || d.Size != int64(d.Buf.Len()) {
		t.Errorf("大的历史版本：big=%v base=%v size=%d", d.Big, d.Base != nil, d.Size)
	}
}

// HEAD 版本的摘要：一样的字节摘要一样，nil 是零值；BaseBuffer 和文件一样解码。解码拿走的字节直接当字符串用，不拷。
func TestSameBaseHash(t *testing.T) {
	a, b := SumOf([]byte("第 1 行\n")), SumOf([]byte("第 1 行\n"))
	if a != b || !a.OK || a.Len != len("第 1 行\n") {
		t.Errorf("同样的内容摘要不同：%+v %+v", a, b)
	}
	if SumOf([]byte("第 1 行\r\n")) == a || SumOf(nil) != (Sum{}) || SumOf([]byte{}) == (Sum{}) {
		t.Error("不同的内容、空文件和没有要分得开")
	}
	if BaseBuffer(nil) != nil || BaseBuffer([]byte("\xEF\xBB\xBFx\n")).String() != "x\n" {
		t.Error("BaseBuffer 没像文件一样去掉 BOM")
	}
	data := []byte("package x\n")
	r := decode(data, "", true, true)
	if text := r.Text; r.Enc != UTF8 || text != "package x\n" || unsafe.StringData(text) != &data[0] {
		t.Error("UTF-8 的文字应直接借用读到的字节")
	}
	if text, _, _ := Decode(data); unsafe.StringData(text) == &data[0] {
		t.Error("Decode 不该借用调用方的字节")
	}
}

// 能编辑的文件变大成了 Big：重读（fresh 为假）也在后台建新的缓冲区换上（不走 Reset），换成后台高亮、只读；
// 变回 8 MB 以内：照 mygo 的语言，能编辑。
func TestGrowsBig(t *testing.T) {
	r := testutil.NewRepo(t, "big")
	r.Write("a.go", "package a\n")
	r.Commit("小")
	d := NewFile(r.Root, "a.go")
	open(t, d)
	buf := d.Buf
	if d.Big || d.Buf.Language() != editor.LanguageFor("a.go") || d.St.ReadOnly {
		t.Fatal("小文件应能编辑、照 mygo 的语言")
	}
	if err := os.WriteFile(d.Abs, []byte(bigText(MaxEditBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	open(t, d)
	if !d.Big || d.Buf == buf || d.Buf.Language() != highlight.For("a.go") || !d.St.ReadOnly || d.St.Buffer != d.Buf {
		t.Fatalf("变大了：big=%v 换了缓冲区=%v readOnly=%v", d.Big, d.Buf != buf, d.St.ReadOnly)
	}
	os.WriteFile(d.Abs, []byte("package a\n"), 0o644)
	open(t, d)
	if d.Big || d.Buf.Language() != editor.LanguageFor("a.go") || d.St.ReadOnly || d.Buf.String() != "package a\n" {
		t.Errorf("变回小的：big=%v readOnly=%v", d.Big, d.St.ReadOnly)
	}
	d.Moved("b.go")
	if d.Buf.Language() != editor.LanguageFor("b.go") {
		t.Error("改名后照 mygo 的语言")
	}
}

// 超过 MaxDiffBaseBytes（2 MB）的文件（或 HEAD 里的那份）：不设对比基准（BaseSum 照记），NoBase；没跟踪的、
// 两份都在 2 MB 以内的照旧。历史版本同样。
func TestNoBase(t *testing.T) {
	r := testutil.NewRepo(t, "nobase")
	text := bigText(MaxDiffBaseBytes + 1<<10)
	r.Write("big.go", text)
	r.Write("ok.go", bigText(MaxDiffBaseBytes-1<<10))
	r.Commit("两个")
	r.Write("big.go", text+"// 改过\n")
	r.Write("new.go", text)

	l := ReadFile(filepath.Join(r.Root, "big.go"), r.Root, "big.go", true)
	if l.Big || !l.Editable || l.Base != nil || !l.BaseSum.OK {
		t.Fatalf("2 MB 以上：big=%v base=%v sum=%v", l.Big, l.Base != nil, l.BaseSum.OK)
	}
	d := NewFile(r.Root, "big.go")
	open(t, d)
	if !d.NoBase || d.Base != nil || d.St.DiffBase != nil || d.St.ReadOnly {
		t.Errorf("2 MB 以上的标签：noBase=%v base=%v readOnly=%v", d.NoBase, d.Base != nil, d.St.ReadOnly)
	}
	ok := NewFile(r.Root, "ok.go")
	open(t, ok)
	if ok.NoBase || ok.St.DiffBase == nil {
		t.Error("2 MB 以内照旧和 HEAD 对比")
	}
	nw := NewFile(r.Root, "new.go")
	open(t, nw)
	if nw.NoBase || nw.BaseSum.OK {
		t.Error("没跟踪的没有可比的，不该说不对比")
	}
	// 文件改小了，可 HEAD 里的还大：照旧不对比
	os.WriteFile(d.Abs, []byte("package big\n"), 0o644)
	open(t, d)
	if !d.NoBase || d.St.DiffBase != nil {
		t.Error("HEAD 里的超过 2 MB 也不对比")
	}

	r.Commit("改")
	cs, err := git.Log(context.Background(), r.Root, 0, 1, git.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := git.CommitFiles(context.Background(), r.Root, cs[0])
	for _, ch := range files {
		v := NewVersion(r.Root, ch)
		gen := v.Start()
		v.ApplyVersion(gen, ReadVersion(r.Root, ch, true))
		switch ch.Path {
		case "big.go": // 改小了，可父版本超过 2 MB
			if !v.NoBase || v.Base != nil {
				t.Error("父版本超过 2 MB 也不对比")
			}
		case "new.go": // 新加的没有父版本
			if v.NoBase || v.Base != nil {
				t.Error("新加的文件没有可比的")
			}
		default:
			t.Errorf("没想到的变更 %s", ch.Path)
		}
	}
	r.Write("ok.go", bigText(MaxDiffBaseBytes+2<<10))
	r.Commit("变大")
	cs, _ = git.Log(context.Background(), r.Root, 0, 1, git.LogQuery{})
	files, _ = git.CommitFiles(context.Background(), r.Root, cs[0])
	v := NewVersion(r.Root, files[0])
	gen := v.Start()
	v.ApplyVersion(gen, ReadVersion(r.Root, files[0], true))
	if !v.NoBase || v.Base != nil || v.Big {
		t.Errorf("2 MB 以上的历史版本：noBase=%v base=%v", v.NoBase, v.Base != nil)
	}
}
