package docs

import "strings"

// ConflictBlock 是文本里的一处冲突标记块（git 合并 / 变基 / 拣选留下的），行号从 0 起：
// Start 是 `<<<<<<< ` 行，Base 是 diff3 的 `||||||| ` 行（没有这一段为 -1），Mid 是 `=======` 行，End 是 `>>>>>>> ` 行。
// 我的 = Start 和 Base（或 Mid）之间，基础 = Base 和 Mid 之间，他们的 = Mid 和 End 之间，都不含标记行。
type ConflictBlock struct {
	Start, Mid, Base, End  int
	OursLabel, TheirsLabel string // 标记后面的名字，如 HEAD、feature
}

// OursEnd 是"我的"那段后面的标记行（Base 或 Mid）。
func (b ConflictBlock) OursEnd() int {
	if b.Base >= 0 {
		return b.Base
	}
	return b.Mid
}

// Contains 是第 line 行在这一块里（含标记行）。
func (b ConflictBlock) Contains(line int) bool { return line >= b.Start && line <= b.End }

// ParseConflicts 按行扫出冲突块。只有 `<<<<<<< ` 打开一块之后，`|||||||`（可带后缀）、独立一行的 `=======`、`>>>>>>> `
// 才算标记，所以 Markdown 标题下面的 `=======` 在块外不算；到文件尾也没闭合的块丢掉；块里再出现的 `<<<<<<<` 当普通行。
// 行尾的 \r 去掉再比（CRLF 的文件）。
func ParseConflicts(text string) []ConflictBlock {
	if !strings.Contains(text, "<<<<<<<") {
		return nil
	}
	const (
		outside = iota
		ours
		base
		theirs
	)
	var blocks []ConflictBlock
	var cur ConflictBlock
	state := outside
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch state {
		case outside:
			if label, ok := marker(line, "<<<<<<<"); ok {
				cur, state = ConflictBlock{Start: i, Base: -1, Mid: -1, OursLabel: label}, ours
			}
		case ours:
			if _, ok := marker(line, "|||||||"); ok {
				cur.Base, state = i, base
			} else if line == "=======" {
				cur.Mid, state = i, theirs
			}
		case base:
			if line == "=======" {
				cur.Mid, state = i, theirs
			}
		case theirs:
			if label, ok := marker(line, ">>>>>>>"); ok {
				cur.End, cur.TheirsLabel = i, label
				blocks, state = append(blocks, cur), outside
			}
		}
	}
	return blocks
}

// marker 是 line 是不是以七个 m 打头的标记行（后面没有东西，或空格加名字），返回名字。
func marker(line, m string) (string, bool) {
	rest, ok := strings.CutPrefix(line, m)
	if !ok {
		return "", false
	}
	if rest == "" {
		return "", true
	}
	if rest[0] != ' ' {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// Take 是一块冲突怎么解决。
type Take int

const (
	TakeOurs         Take = iota // 用我的
	TakeTheirs                   // 用他们的
	TakeBoth                     // 两个都要，我的在前
	TakeBothReversed             // 两个都要，他们的在前
)

// Resolved 是块 b 解决成 how 之后的文字，和它替换的字节范围 [start, end)：从 Start 行行首到 End 行的换行之后
// （End 是最后一行、后面没有换行时到文本尾，换上的文字也不带最后那个换行）。
func Resolved(text string, b ConflictBlock, how Take) (start, end int, repl string) {
	offs := lineOffsets(text)
	at := func(line int) int {
		if line >= len(offs) {
			return len(text)
		}
		return offs[line]
	}
	section := func(first, stop int) string { return text[at(first):at(stop)] } // 行 [first, stop)，带各自的换行
	mine, theirs := section(b.Start+1, b.OursEnd()), section(b.Mid+1, b.End)
	switch how {
	case TakeOurs:
		repl = mine
	case TakeTheirs:
		repl = theirs
	case TakeBoth:
		repl = mine + theirs
	case TakeBothReversed:
		repl = theirs + mine
	}
	start, end = at(b.Start), at(b.End+1)
	if !strings.HasSuffix(text[start:end], "\n") { // 文件最后一行就是 >>>>>>>，后面没有换行：换上去的也不加
		repl = strings.TrimSuffix(repl, "\n")
	}
	return start, end, repl
}

// lineOffsets 是每一行行首的字节位置。
func lineOffsets(text string) []int {
	offs := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}

// CurrentConflict 是光标在第 line 行时冲突条说的"第几处"：光标所在的那块；不在块里是光标后面的第一块，
// 后面没有了是最后一块。没有块为 -1。
func CurrentConflict(blocks []ConflictBlock, line int) int {
	for i, b := range blocks {
		if line <= b.End {
			return i
		}
	}
	return len(blocks) - 1
}

// NextConflict 是从第 line 行往 dir 方向（1 下、-1 上）的下一块，不算光标所在的那块；走到头绕回另一头。没有块为 -1。
func NextConflict(blocks []ConflictBlock, line, dir int) int {
	if len(blocks) == 0 {
		return -1
	}
	if dir > 0 {
		for i, b := range blocks {
			if b.Start > line {
				return i
			}
		}
		return 0
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].End < line {
			return i
		}
	}
	return len(blocks) - 1
}
