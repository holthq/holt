package docs

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
)

// cursorInfo 是 CursorInfo 上次算的：内容版本、选区没变就接着用。
type cursorInfo struct {
	ok              bool
	ver             uint64
	sels            []editor.Selection
	head            int
	line, col, char int
}

// CursorInfo 是状态栏用的光标位置（主光标所在的行、列都从 1 起，列按字算、Tab 算一个）、有几处选区、选中了几个字
// （按 rune 数，跨行的换行也算一个）。内容和选区都没变时不重算：长行、整篇选中每帧数一遍太贵。
func (d *Doc) CursorInfo() (line, col, sels, chars int) {
	if d.Buf == nil {
		return 1, 1, 1, 0
	}
	ss, head, ver := d.St.Selections(), d.St.Selection().Head, d.Buf.Version()
	c := &d.cursor
	if !c.ok || c.ver != ver || c.head != head || !slices.Equal(c.sels, ss) {
		p := d.Buf.Pos(head)
		l := d.Buf.Line(p.Line)
		c.line, c.col = p.Line+1, utf8.RuneCountInString(l[:min(p.Col, len(l))])+1
		c.char = 0
		for _, s := range ss {
			if !s.Empty() {
				c.char += utf8.RuneCountInString(d.Buf.Slice(s.Start(), s.End()))
			}
		}
		c.ok, c.ver, c.sels, c.head = true, ver, ss, head
	}
	return c.line, c.col, len(ss), c.char
}

// Reindent 把一行开头的缩进 lead（只有空格和 Tab）按 size 宽的 Tab 换成全是空格（toTabs 为假）或尽量用 Tab
// （凑不满一格的留成空格）；Tab 对齐到 size 的整数倍。
func Reindent(lead string, size int, toTabs bool) string {
	w := 0
	for i := 0; i < len(lead); i++ {
		if lead[i] == '\t' {
			w += size - w%size
		} else {
			w++
		}
	}
	if toTabs {
		return strings.Repeat("\t", w/size) + strings.Repeat(" ", w%size)
	}
	return strings.Repeat(" ", w)
}

// ConvertIndent 把每行开头的缩进按 size 换成空格或 Tab（Reindent），一次改完、一步撤销；返回动了几行。只对能保存的。
func (d *Doc) ConvertIndent(size int, toTabs bool) int {
	if !d.Editable() || size <= 0 {
		return 0
	}
	text := d.Buf.String()
	var edits []editor.Edit
	for off := 0; off <= len(text); {
		end := strings.IndexByte(text[off:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += off
		}
		line := text[off:end]
		if n := len(line) - len(strings.TrimLeft(line, " \t")); n > 0 {
			if want := Reindent(line[:n], size, toTabs); want != line[:n] {
				edits = append(edits, editor.Edit{Start: off, End: off + n, Text: want})
			}
		}
		off = end + 1
	}
	if len(edits) > 0 {
		d.Buf.Edit(edits...)
	}
	return len(edits)
}
