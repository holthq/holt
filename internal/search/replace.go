package search

import (
	"bytes"
	"cmp"
	"slices"
	"strings"
)

// 替换：按搜索结果把匹配换掉。这里只算内容，不读写文件；读进来、写回去（或者改编辑器的缓冲区）归调用方。

// Edit 是一处替换：内容里 Start 到 End 的字节换成 Text。
type Edit struct {
	Start, End int
	Text       string
}

// Plan 算出把 ms（同一个文件里的匹配，搜的时候的位置）换成 repl 的改动，按位置排好、不重叠。
// 每一处先核对还在原位：这一行的预览没变，这里还是一处匹配（字面的字一样、整词时两边不是字；正则从这里起还能匹配、长度一样）。
// 对不上的（搜完之后文件改了）不换，算进 stale。正则的 repl 按 regexp.Expand 展开（$1、${name}、$$），字面的原样插进去；
// 高级正则先把整个 data 配一遍再按起点核对（advHits），超时的话一处都不换；
// data 用 \r\n 换行时 repl 里的 \n 换成 \r\n；没有 \r\n 的 data（编辑器缓冲区）里跨行的匹配按 BufLen 核对。
func (m *Matcher) Plan(data []byte, ms []Match, repl string) (edits []Edit, stale int) {
	crlf := bytes.Contains(data, []byte("\r\n"))
	if crlf {
		repl = strings.ReplaceAll(strings.ReplaceAll(repl, "\r\n", "\n"), "\n", "\r\n")
	}
	var hits map[int]advHit
	if m.adv != nil {
		var err error
		if hits, err = m.advHits(data, repl); err != nil {
			return nil, len(ms)
		}
	}
	ms = slices.Clone(ms)
	slices.SortFunc(ms, func(x, y Match) int { return cmp.Or(cmp.Compare(x.Line, y.Line), cmp.Compare(x.Col, y.Col)) })
	line, lineStart, end := 0, bomLen(data), 0 // end 是上一处改到哪
	for _, mt := range ms {
		for lineStart >= 0 && line < mt.Line {
			i := bytes.IndexByte(data[lineStart:], '\n')
			if i < 0 {
				lineStart = -1 // 文件变短了，后面的行都没了
				break
			}
			lineStart += i + 1
			line++
		}
		text, span, ok := m.at(data, lineStart, mt, repl, crlf, hits)
		if s := lineStart + mt.Col; ok && s >= end {
			edits = append(edits, Edit{s, s + span, text})
			end = s + span
			continue
		}
		stale++
	}
	return edits, stale
}

// at 核对 mt 还在 data 里原来的地方（lineStart 是它那一行的起点，-1 是没有这一行），返回它换成的文字和在 data 里的长度
// （crlf 是 data 用 \r\n 换行：磁盘上的，长度是 Span；不然是编辑器缓冲区，长度是 BufLen）。hits 是高级正则的 advHits。
func (m *Matcher) at(data []byte, lineStart int, mt Match, repl string, crlf bool, hits map[int]advHit) (string, int, bool) {
	span := mt.Span
	if !crlf {
		span = mt.BufLen()
	}
	s, e := lineStart+mt.Col, lineStart+mt.Col+span
	if lineStart < 0 || mt.Col < 0 || span <= 0 || e > len(data) {
		return "", 0, false
	}
	lineEnd := len(data)
	if i := bytes.IndexByte(data[lineStart:], '\n'); i >= 0 {
		lineEnd = lineStart + i
	}
	if s > lineEnd {
		return "", 0, false
	}
	if p, pc, _ := matchPreview(data, lineStart, s, e); p != mt.Preview || pc != mt.PreviewCol {
		return "", 0, false
	}
	if m.whole && !wordBounded(data, s, e) {
		return "", 0, false
	}
	if m.adv != nil {
		h, ok := hits[s]
		if !ok || h.span != span {
			return "", 0, false
		}
		return h.text, span, true
	}
	if m.re == nil {
		if !equalLit(data[s:e], m.lit, m.fold) {
			return "", 0, false
		}
		return repl, span, true
	}
	loc := m.re.FindSubmatchIndex(data[s:])
	if loc == nil || loc[0] != 0 || loc[1] != span {
		return "", 0, false
	}
	if !m.expand {
		return repl, span, true
	}
	return string(m.re.Expand(nil, []byte(repl), data[s:], loc)), span, true
}

// equalLit 是 b 和 lit 一样（fold 时 b 按 ASCII 转小写再比，lit 已经是小写）。
func equalLit(b, lit []byte, fold bool) bool {
	if len(b) != len(lit) {
		return false
	}
	if !fold {
		return bytes.Equal(b, lit)
	}
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lit[i] {
			return false
		}
	}
	return true
}

// Replace 把 data 里的 ms 换成 repl（先 Plan 核对），返回新的内容、换了几处、几处对不上没换。一处都没换时 out 就是 data。
func (m *Matcher) Replace(data []byte, ms []Match, repl string) (out []byte, done, stale int) {
	edits, stale := m.Plan(data, ms, repl)
	return Apply(data, edits), len(edits), stale
}

// Apply 按 edits（Plan 给的：排好、不重叠）改 data，只分配一次；没有改动时返回 data 本身。
func Apply(data []byte, edits []Edit) []byte {
	if len(edits) == 0 {
		return data
	}
	n := len(data)
	for _, e := range edits {
		n += len(e.Text) - (e.End - e.Start)
	}
	out, at := make([]byte, 0, n), 0
	for _, e := range edits {
		out = append(out, data[at:e.Start]...)
		out = append(out, e.Text...)
		at = e.End
	}
	return append(out, data[at:]...)
}

// Expand 是 mt 换成 repl 之后的文字，给结果的预览用。正则在 Preview 里从这一处起重新匹配一次再展开：
// 预览截过、或者匹配跨行时可能对不上，这时返回 repl 和 false（界面照原样画 repl）；字面的就是 repl。
// 高级正则在整条预览里从这一处起找（环视看得到前面的字），超过 previewTimeout 也当对不上。
func (m *Matcher) Expand(mt Match, repl string) (string, bool) {
	if !m.expand {
		return repl, true
	}
	if mt.PreviewCol < 0 || mt.PreviewCol > len(mt.Preview) {
		return repl, false
	}
	if m.adv != nil {
		hit, err := m.quick.FindStringMatchStartingAt(mt.Preview, mt.PreviewCol)
		if err != nil || hit == nil {
			return repl, false
		}
		loc, names := advLoc(hit)
		if loc[0] != mt.PreviewCol {
			return repl, false
		}
		return string(expandTemplate(nil, repl, mt.Preview, loc, names)), true
	}
	src := mt.Preview[mt.PreviewCol:]
	loc := m.re.FindStringSubmatchIndex(src)
	if loc == nil || loc[0] != 0 {
		return repl, false
	}
	return string(m.re.ExpandString(nil, repl, src, loc)), true
}

// bomLen 是 data 开头 UTF-8 BOM 的长度（0 或 3）。
func bomLen(data []byte) int {
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		return 3
	}
	return 0
}

// Find 是 data 里的全部匹配（最多 MaxMatches 处），填好行、列、预览；仓库和路径由调用方填。
// 替换完之后在内存里重搜改过的文件用它（编辑器里没保存的内容不在磁盘上）。
// 高级正则超时的话是空的。
func (m *Matcher) Find(data []byte) []Match {
	ms, _ := m.file(data, MaxMatches)
	return ms
}

// Shift 是 data 里 line:col 那一处在按 edits 改成 out 之后的行和列；它落在被换掉的那一段里时 ok 为假。
func Shift(data, out []byte, edits []Edit, line, col int) (int, int, bool) {
	off := bomLen(data)
	for ; line > 0; line-- {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		off += i + 1
	}
	off += col
	delta := 0
	for _, e := range edits {
		if e.Start > off {
			break
		}
		if e.End > off {
			return 0, 0, false
		}
		delta += len(e.Text) - (e.End - e.Start)
	}
	off += delta
	if off < 0 || off > len(out) {
		return 0, 0, false
	}
	start := bomLen(out)
	n := bytes.Count(out[start:off], []byte("\n"))
	if i := bytes.LastIndexByte(out[:off], '\n'); i >= start {
		start = i + 1
	}
	return n, off - start, true
}
