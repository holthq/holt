// Package lsp 是 Holt 的语言服务器（LSP）客户端：起进程、JSON-RPC 收发、文档同步，
// 以及补全、悬停、跳转定义和诊断这四样编辑器用得上的东西。这里不认识 Holt 的标签和界面，
// internal/app/lang.go 负责把它接到编辑器上。
package lsp

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Encoding 是 LSP 位置里 character 的单位：UTF-16 码元（协议默认）或 UTF-8 字节。
type Encoding int

const (
	UTF16 Encoding = iota
	UTF8
)

// EncodingOf 把服务端协商出来的 positionEncoding 换成 Encoding；认不出的按协议默认的 UTF-16。
func EncodingOf(name string) Encoding {
	if strings.EqualFold(strings.TrimSpace(name), "utf-8") {
		return UTF8
	}
	return UTF16
}

// Text 是一份文档文字，在 LSP 的（行、character）和 Go 的字节偏移之间换算。
type Text struct {
	s      string
	starts []int // 每行开头的字节偏移
	enc    Encoding
}

// NewText 是协议默认的 UTF-16 位置的文字。
func NewText(s string) *Text { return NewTextEncoding(s, UTF16) }

// NewTextEncoding 是按 enc 换算位置的文字。
func NewTextEncoding(s string, enc Encoding) *Text {
	starts := []int{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &Text{s: s, starts: starts, enc: enc}
}

// String 是这份文字。
func (t *Text) String() string { return t.s }

// Encoding 是换算位置用的单位。
func (t *Text) Encoding() Encoding { return t.enc }

// Lines 是行数（最后一行后面没有换行也是一行）。
func (t *Text) Lines() int { return len(t.starts) }

// LineStart 是第 line 行（从 0 起）开头的字节偏移；越界时是文首或文末。
func (t *Text) LineStart(line int) int {
	if line <= 0 {
		return 0
	}
	if line >= len(t.starts) {
		return len(t.s)
	}
	return t.starts[line]
}

// LineEnd 是第 line 行结尾的字节偏移，不含换行符。
func (t *Text) LineEnd(line int) int {
	if line < 0 {
		return 0
	}
	if line >= len(t.starts) {
		return len(t.s)
	}
	if line+1 < len(t.starts) {
		end := t.starts[line+1] - 1
		if end > 0 && t.s[end-1] == '\r' {
			end--
		}
		return end
	}
	return len(t.s)
}

// Offset 是 LSP 位置对应的字节偏移。行、列超出范围时夹到最后一行 / 行尾。
func (t *Text) Offset(p Position) int {
	line := p.Line
	if line < 0 {
		line = 0
	}
	if line >= len(t.starts) {
		return len(t.s)
	}
	start, end := t.starts[line], t.sLineEnd(line)
	char := p.Character
	if char <= 0 {
		return start
	}
	if t.enc == UTF8 {
		off := start + char
		if off > end {
			off = end
		}
		return off
	}
	units := 0
	for i := start; i < end; {
		r, size := utf8.DecodeRuneInString(t.s[i:end])
		if r == utf8.RuneError && size == 1 {
			size = 1
		}
		n := 1
		if r > 0xFFFF {
			n = 2
		}
		if units+n > char {
			return i
		}
		units += n
		i += size
		if units == char {
			return i
		}
	}
	return end
}

// sLineEnd 是第 line 行结尾的字节偏移（内部用，不检查 line）。
func (t *Text) sLineEnd(line int) int {
	if line+1 < len(t.starts) {
		end := t.starts[line+1] - 1
		if end > 0 && t.s[end-1] == '\r' {
			end--
		}
		return end
	}
	return len(t.s)
}

// Position 是字节偏移对应的 LSP 位置；偏移越界时夹到文首或文末。
func (t *Text) Position(off int) Position {
	if off <= 0 {
		return Position{}
	}
	if off > len(t.s) {
		off = len(t.s)
	}
	line := sort.Search(len(t.starts), func(i int) bool { return t.starts[i] > off }) - 1
	if line < 0 {
		line = 0
	}
	start := t.starts[line]
	character := off - start
	if t.enc == UTF16 {
		units := 0
		for i := start; i < off; {
			r, size := utf8.DecodeRuneInString(t.s[i:off])
			if r == utf8.RuneError && size == 1 {
				size = 1
			}
			if r > 0xFFFF {
				units += 2
			} else {
				units++
			}
			i += size
		}
		character = units
	}
	return Position{Line: line, Character: character}
}

// RangeOffsets 是 LSP 范围对应的字节偏移：起、止（止不小于起）。
func (t *Text) RangeOffsets(r Range) (start, end int) {
	start, end = t.Offset(r.Start), t.Offset(r.End)
	if end < start {
		end = start
	}
	if end > len(t.s) {
		end = len(t.s)
	}
	return start, end
}

// WordAt 是 cursor 所在的标识符的起止字节偏移（没有标识符时是光标前后一个 rune）。
func (t *Text) WordAt(cursor int) (start, end int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(t.s) {
		cursor = len(t.s)
	}
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	start, end = cursor, cursor
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(t.s[:start])
		if !isWord(r) {
			break
		}
		start -= size
	}
	for end < len(t.s) {
		r, size := utf8.DecodeRuneInString(t.s[end:])
		if !isWord(r) {
			break
		}
		end += size
	}
	if start == end {
		if cursor > 0 {
			_, size := utf8.DecodeLastRuneInString(t.s[:cursor])
			start = cursor - size
		}
		if cursor < len(t.s) {
			_, size := utf8.DecodeRuneInString(t.s[cursor:])
			end = cursor + size
		}
	}
	return start, end
}
