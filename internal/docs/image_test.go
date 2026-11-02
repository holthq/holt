package docs

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/testutil"
)

// 十六进制 dump：一行 16 字节、两组中间多一格、右边 ASCII（不可打印的是点），最后一行不满照样对齐；
// 光标的列换成字节偏移；三位一组的数。
func TestHexDump(t *testing.T) {
	data := []byte("0123456789abcdefHello world\n\x00\x01")
	want := "00000000  30 31 32 33 34 35 36 37  38 39 61 62 63 64 65 66  |0123456789abcdef|\n" +
		"00000010  48 65 6c 6c 6f 20 77 6f  72 6c 64 0a 00 01        |Hello world...|\n"
	got := HexDump(data)
	if got != want {
		t.Fatalf("dump:\n%s\n应是:\n%s", got, want)
	}
	if n := strings.Index(got, "\n"); n != HexLine {
		t.Errorf("一行 %d 字，HexLine 是 %d", n, HexLine)
	}
	if HexDump(nil) != "" {
		t.Error("空的应没有行")
	}
	for _, c := range []struct {
		line, col int
		off       int64
	}{
		{0, 0, 0}, {0, 10, 0}, {0, 12, 0}, {0, 13, 1}, {0, 31, 7}, {0, 33, 7}, {0, 34, 0},
		{0, 35, 8}, {0, 56, 15}, {0, 58, 15}, {0, 59, 0}, {0, 61, 0}, {0, 76, 15}, {0, 77, 0},
		{1, 64, 19}, {2, 10, 32},
	} {
		if got := HexOffset(editor.Pos{Line: c.line, Col: c.col}); got != c.off {
			t.Errorf("第 %d 行第 %d 列：偏移 %d，应是 %d", c.line, c.col, got, c.off)
		}
	}
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1234: "-1,234"} {
		if got := Grouped(n); got != want {
			t.Errorf("Grouped(%d) = %q", n, got)
		}
	}
}

// 缩放一档档走：从 100% 往上 150%、往下 75%；适应时的 33% 走到挨着的档；两头不再走。
func TestZoomStep(t *testing.T) {
	for _, c := range []struct {
		cur  float32
		up   bool
		want float32
	}{
		{1, true, 1.5}, {1, false, 0.75}, {0.33, true, 0.5}, {0.33, false, 0.25}, {16, true, 16}, {0.1, false, 0.1}, {0.25, true, 0.5},
	} {
		if got := ZoomStep(c.cur, c.up); got != c.want {
			t.Errorf("ZoomStep(%v, %v) = %v，应是 %v", c.cur, c.up, got, c.want)
		}
	}
}

// SVG 预览按缓冲区解析、缓冲区变了才重解；解析失败报错但留着上一张；text / foreignObject / image 记成画不出来的。
func TestSVGPreview(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	src := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="red"/><text x="1" y="10">hi</text><svg:image href="x.png"/></svg>`
	os.WriteFile(filepath.Join(root, "pic.svg"), []byte(src), 0o644)
	d := NewFile(root, "pic.svg")
	open(t, d)
	if d.Buf == nil || !d.CanPreview() || d.Hex {
		t.Fatalf("SVG 应按文本开、能预览：info=%q", d.Info)
	}
	img, errText, missing := d.SVGPreview()
	if img == nil || errText != "" {
		t.Fatalf("解析：%v %q", img, errText)
	}
	if w, h := img.Size(); w != 40 || h != 20 {
		t.Errorf("大小 %v×%v", w, h)
	}
	if strings.Join(missing, ",") != "text,image" {
		t.Errorf("画不出来的 %q", missing)
	}
	if again, _, _ := d.SVGPreview(); again != img {
		t.Error("缓冲区没变也重解析了")
	}
	d.Buf.Set(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect`)
	broken, errText, missing := d.SVGPreview()
	if errText == "" || broken != img || len(missing) != 0 {
		t.Errorf("坏了的：图 %v（应留着上一张）、错 %q、画不出来的 %q", broken == img, errText, missing)
	}
}

// 以十六进制读：只读前 HexMax 字节、Total 是整个文件；没有 NUL 的不算二进制（切得回文本）。历史版本一样。
func TestReadHex(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	big := bytes.Repeat([]byte("abcdefghijklmnop"), HexMax/16+10)
	os.WriteFile(filepath.Join(root, "big.txt"), big, 0o644)
	l := ReadHexFile(filepath.Join(root, "big.txt"))
	if l.Err != nil || !l.Hex || l.Total != int64(len(big)) || l.Enc == Binary || l.Buf == nil {
		t.Fatalf("读：err %v hex %v total %d enc %q", l.Err, l.Hex, l.Total, l.Enc)
	}
	if n := l.Buf.LineCount(); n != HexMax/16+1 { // 最后一个换行后面还有一个空行
		t.Errorf("%d 行，应只 dump 前 1 MB（%d 行）", n, HexMax/16+1)
	}
	v := ReadHexVersion(root, git.Change{Commit: "HEAD", Path: "a.txt"})
	if v.Err != nil || !v.Hex || v.Total == 0 || !strings.HasPrefix(v.Buf.String(), "00000000  e7 ac ac 20 31 20") {
		t.Errorf("历史版本：%v %q", v.Err, v.Buf.String()[:40])
	}
}

// 图片 diff 的两边：空的是这边没有，解不出来的记下为什么。
func TestImgSides(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 5, 4)))
	s := imgSides(git.DiffContent{OldName: "a.png", NewName: "b.png", New: b.Bytes()})
	if s[0].Bitmap != nil || s[0].Err != "" || s[0].Name != "a.png" {
		t.Errorf("旧的那边：%+v", s[0])
	}
	if s[1].Bitmap == nil || s[1].Size != b.Len() || s[1].Name != "b.png" {
		t.Errorf("新的那边：%+v", s[1])
	}
	s = imgSides(git.DiffContent{OldName: "a.png", NewName: "a.png", Old: []byte("不是图"), New: b.Bytes()})
	if s[0].Bitmap != nil || !strings.HasPrefix(s[0].Err, "图片解码失败：") {
		t.Errorf("坏图：%+v", s[0])
	}
}
