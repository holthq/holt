package docs

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// 图片（二）和十六进制视图：SVG 预览、缩放、图片 diff，二进制文件按十六进制看。

// IsSVG 是不是 SVG（按文本开，⇧⌘V 预览）。
func IsSVG(name string) bool { return strings.EqualFold(filepath.Ext(name), ".svg") }

// ImgView 是图片视图的缩放和滚动，按标签记、不进会话。Zoom 为 0 是适应（大的缩到放得下，小的照原大小），
// 别的是倍数（1 = 100%）；Shown 是上一帧实际画的倍数（适应时也有），状态栏和工具栏的百分比用它。
// Scroll2 是图片 diff 右边那张的滚动，Pane 是两块画布上一帧的样子（ui 每帧写，缩放时按它算不动的那一点）。
type ImgView struct {
	Zoom, Shown     float32
	Scroll, Scroll2 ui.ScrollState
	Pane            [2]ImgPane
	pix, pixOf      [2]*ui.Bitmap
}

// ImgPane 是一块画布：W × H 是画布（能滚的那块）多大，IW × IH 是图本来多大。
type ImgPane struct{ W, H, IW, IH float32 }

// ZoomSteps 是 + / − 一档档走的倍数；PixelZoom 起按像素格画（看得清每个像素）。
var ZoomSteps = []float32{0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, 6, 8, 12, 16}

const PixelZoom = 4

// ZoomStep 是从 cur 往大（up）或往小走一档：适应时 cur 是适应的倍数，走到挨着它的那档。
func ZoomStep(cur float32, up bool) float32 {
	if up {
		for _, z := range ZoomSteps {
			if z > cur*1.001 {
				return z
			}
		}
		return ZoomSteps[len(ZoomSteps)-1]
	}
	for i := len(ZoomSteps) - 1; i >= 0; i-- {
		if ZoomSteps[i] < cur*0.999 {
			return ZoomSteps[i]
		}
	}
	return ZoomSteps[0]
}

// Pixelated 是 bm（第 side 块画布上的）按像素格画的那份：mygo 的 Pixelated 是位图上的开关、没有关掉的办法，
// 这里用位图的浅拷贝（共用解码好的像素和纹理）打开它，原来那份照旧平滑。
func (v *ImgView) Pixelated(side int, bm *ui.Bitmap) *ui.Bitmap {
	if v.pixOf[side] != bm {
		cp := *bm
		v.pix[side], v.pixOf[side] = cp.Pixelated(), bm
	}
	return v.pix[side]
}

// svgTags 是 mygo 的 SVG 解析画不出来的元素。
var svgTags = regexp.MustCompile(`<(?:[A-Za-z_][\w.-]*:)?(text|foreignObject|image)[\s/>]`)

// SVGPreview 是 SVG 预览按缓冲区现在的文字解析的结果（缓冲区变了才重新解析）：img 是最近一次解析成功的那张
// （这一版解析失败时留着上一张），errText 是这一版的错（"" 为成功），missing 是里面 mygo 画不出来的元素（text、foreignObject、image）。
func (d *Doc) SVGPreview() (img *ui.SVG, errText string, missing []string) {
	if d.Buf == nil {
		return nil, "", nil
	}
	if v := d.Buf.Version(); !d.svgOK || v != d.svgVer {
		src := d.Buf.String()
		d.svgVer, d.svgOK, d.svgErr, d.svgMissing = v, true, "", nil
		if s, err := ui.ParseSVG([]byte(src)); err != nil {
			d.svgErr = err.Error()
		} else {
			d.svgImg = s
		}
		seen := map[string]bool{}
		for _, m := range svgTags.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				d.svgMissing = append(d.svgMissing, m[1])
			}
		}
	}
	return d.svgImg, d.svgErr, d.svgMissing
}

// HexMax 是十六进制视图最多显示的字节数：1 MB 的 dump 大约 5 MB 文字。
const HexMax = 1 << 20

// HexLine 是十六进制 dump 一行的宽（不算换行）：8 位偏移、两格、两组 8 字节（每个字节后面一格，两组中间多一格）、一格、|ASCII|。
const HexLine = 8 + 2 + 3*16 + 1 + 1 + 18

// HexDump 是 data 的十六进制 dump，每行 16 字节：
//
//	00000000  48 65 6c 6c 6f 20 77 6f  72 6c 64 0a 00 00 00 00  |Hello world.....|
//
// 不可打印的字节在右边写成 "."，最后一行不满的照样对齐。
func HexDump(data []byte) string {
	const digits = "0123456789abcdef"
	lines := (len(data) + 15) / 16
	b := make([]byte, 0, lines*(HexLine+1))
	for off := 0; off < len(data); off += 16 {
		row := data[off:min(off+16, len(data))]
		for s := 28; s >= 0; s -= 4 {
			b = append(b, digits[off>>s&0xf])
		}
		b = append(b, ' ', ' ')
		for i := range 16 {
			if i == 8 {
				b = append(b, ' ')
			}
			if i < len(row) {
				b = append(b, digits[row[i]>>4], digits[row[i]&0xf], ' ')
			} else {
				b = append(b, ' ', ' ', ' ')
			}
		}
		b = append(b, ' ', '|')
		for _, c := range row {
			if c < 0x20 || c > 0x7e {
				c = '.'
			}
			b = append(b, c)
		}
		b = append(b, '|', '\n')
	}
	return string(b)
}

// HexOffset 是十六进制 dump 里 p（行、列，列按字节）指的那个字节的偏移：在十六进制两组或右边 ASCII 列里是那个字节，
// 在偏移列和空白处是那一行的第一个字节。
func HexOffset(p editor.Pos) int64 {
	i := 0
	switch c := p.Col; {
	case c >= 10 && c < 34:
		i = (c - 10) / 3
	case c >= 35 && c < 59:
		i = 8 + (c-35)/3
	case c >= 61 && c < 77:
		i = c - 61
	}
	return int64(p.Line)*16 + int64(i)
}

// Grouped 是 12,345,678 这样三位一组的数。
func Grouped(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hexify 把读到的 data 做成十六进制 dump（前 HexMax 字节）放进缓冲区：Total 是一共多少字节。
func (l *Loaded) hexify(data []byte) {
	l.Hex, l.Total, l.Big = true, int64(len(data)), false
	l.Text = HexDump(data[:min(len(data), HexMax)])
	l.Buf = editor.NewBuffer(l.Text)
	l.Text = ""
}

// ReadHexFile 读磁盘上文件的前 HexMax 字节做成十六进制 dump（以十六进制查看、重读十六进制标签）；
// 开头有 NUL 的记成二进制（Enc == Binary：没有别的看法，切不回文本）。可以在后台调用。
func ReadHexFile(abs string) Loaded {
	var l Loaded
	l.Info, l.Err = os.Stat(abs)
	if l.Err != nil {
		return l
	}
	if l.Note = sizeNote(l.Info.Size(), false, true); l.Note != "" {
		return l
	}
	f, err := os.Open(abs)
	if err != nil {
		l.Err = err
		return l
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, HexMax))
	if err != nil {
		l.Err = err
		return l
	}
	l.hexify(data)
	l.Total, l.Size = max(l.Info.Size(), int64(len(data))), int64(len(data))
	if git.IsBinary(data) {
		l.Enc = Binary
	}
	return l
}

// ReadHexVersion 是 ReadHexFile 的历史版本：读提交里的那份。
func ReadHexVersion(root string, ch git.Change) Loaded {
	var l Loaded
	data, err := git.Run(root, "cat-file", "blob", ch.Commit+":"+ch.Path)
	if err != nil {
		l.Err, l.Gone = err, GoneCommit(root, ch.Commit)
		return l
	}
	if l.Note = sizeNote(int64(len(data)), false, false); l.Note != "" {
		return l
	}
	l.hexify(data)
	l.Size = int64(len(data))
	if git.IsBinary(data) {
		l.Enc = Binary
	}
	return l
}

// applyHex 把十六进制 dump 放进标签：只读、没有对比基准、纯文本。
func (d *Doc) applyHex(l Loaded) {
	d.Bitmap, d.Hex, d.HexTotal = nil, true, l.Total
	d.Enc, d.Big, d.Lossy, d.Broken = l.Enc, false, false, false
	l.Base, l.BaseSum = nil, Sum{}
	d.setText(l)
	d.St.ReadOnly = true
}

// HexOnly 是十六进制视图是这个标签唯一的看法：二进制、又不是图片（切回去还是十六进制，"以十六进制查看"不让关）。
func (d *Doc) HexOnly() bool { return d.Hex && d.Enc == Binary && !IsImage(d.Name()) }

// HexNote 是十六进制视图顶栏左边那句："十六进制 · 1,234 字节"。
func (d *Doc) HexNote() string {
	return i18n.Tf("十六进制 · %s 字节", Grouped(d.HexTotal))
}

// ImgSide 是图片 diff 的一边：Bitmap 为 nil 是这边没有（新加 / 删掉的）或 Err 解不出来；Size 是字节数。
type ImgSide struct {
	Bitmap *ui.Bitmap
	Name   string
	Size   int
	Err    string
}

// imgSides 把 diff 两边的字节解成图（主线程上，和 ApplyFile 解图一样）：空的是这边没有。
func imgSides(dc git.DiffContent) *[2]ImgSide {
	var s [2]ImgSide
	for i, data := range [2][]byte{dc.Old, dc.New} {
		s[i].Name, s[i].Size = dc.OldName, len(data)
		if i == 1 {
			s[i].Name = dc.NewName
		}
		if len(data) == 0 {
			continue
		}
		bm, err := ui.DecodeBitmap(data)
		if err != nil {
			s[i].Err = i18n.Tf("图片解码失败：%s", err.Error())
			continue
		}
		s[i].Bitmap = bm
	}
	return &s
}
