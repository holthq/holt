package docs

import (
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

// Tidy 是保存前的整理：Trim 去掉行尾的空格和制表符，Newline 在末尾没有换行时补一个；
// Keep 里的行（光标所在的）不去尾空白。
type Tidy struct {
	Trim, Newline bool
	Keep          []int
}

// Tidy 按 t 整理能保存的文件标签的缓冲区，作为一步撤销；返回动了几行（补末尾换行算一行）。
// Markdown 里行尾正好两个空格（硬换行）的留着。缓冲区里的换行已经是 \n，写盘时才按原来的换成 CRLF。
func (d *Doc) Tidy(t Tidy) (lines int) {
	if !d.Editable() || !t.Trim && !t.Newline {
		return 0
	}
	text := d.Buf.String()
	var edits []editor.Edit
	if t.Trim {
		md := IsMarkdown(d.Name())
		keep := make(map[int]bool, len(t.Keep))
		for _, l := range t.Keep {
			keep[l] = true
		}
		start := 0
		for i := 0; start <= len(text); i++ {
			end := strings.IndexByte(text[start:], '\n')
			if end < 0 {
				end = len(text)
			} else {
				end += start
			}
			line := text[start:end]
			kept := strings.TrimRight(line, " \t")
			hardBreak := md && kept != "" && line[len(kept):] == "  "
			if len(kept) < len(line) && !keep[i] && !hardBreak {
				edits = append(edits, editor.Edit{Start: start + len(kept), End: end})
			}
			start = end + 1
		}
	}
	lines = len(edits)
	if t.Newline && text != "" && !strings.HasSuffix(text, "\n") {
		if lines == 0 || edits[lines-1].End != len(text) {
			lines++ // 最后一行也去了尾空白时算一行
		}
		edits = append(edits, editor.Edit{Start: len(text), End: len(text), Text: "\n"})
	}
	if len(edits) > 0 {
		d.Buf.Edit(edits...)
	}
	return lines
}

// CursorLines 是所有光标（多光标都算）所在的行，保存整理时不去它们的尾空白。
func (d *Doc) CursorLines() []int {
	if d.Buf == nil {
		return nil
	}
	var lines []int
	for _, s := range d.St.Selections() {
		lines = append(lines, d.Buf.Pos(s.Head).Line)
	}
	return lines
}
