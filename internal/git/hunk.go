package git

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// 这个文件是块级（hunk）暂存 / 取消暂存 / 丢弃用的补丁。Diff 视图按行（"\n" 分开，CRLF 当一行）
// 给出每一块改动的位置；把那一块套到一边上得到另一份全文，再和原来的比出只有这一块的补丁，交给
// git apply（--cached 改索引，-R 反着打）。git apply 要上下文对得上才打，所以磁盘上的文件在
// 读 diff 之后又被改过时，它会失败而不是乱改。

// HunkContext 是补丁里改动上下各带几行没改的，和 git diff 默认的一样。
const HunkContext = 3

// ReplaceLines 把 text 的第 from 到 to 行（从 0 起，不含 to；按 "\n" 分行，和 Diff 视图的行号一样）
// 换成 with 的第 wFrom 到 wTo 行，返回新的全文。行号超出范围时 ok 为 false。
func ReplaceLines(text []byte, from, to int, with []byte, wFrom, wTo int) (out []byte, ok bool) {
	a, b := strings.Split(string(text), "\n"), strings.Split(string(with), "\n")
	if from < 0 || from > to || to > len(a) || wFrom < 0 || wFrom > wTo || wTo > len(b) {
		return nil, false
	}
	lines := make([]string, 0, len(a)-(to-from)+(wTo-wFrom))
	lines = append(append(append(lines, a[:from]...), b[wFrom:wTo]...), a[to:]...)
	return []byte(strings.Join(lines, "\n")), true
}

// HunkPatch 是把 path 从 from 改成 to 的补丁（git diff 的格式，一个文件一块，上下各带 HunkContext 行），
// 两份一样时为 ""。from 和 to 应只差连续的一段：去掉相同的头尾，中间就是那一块。
func HunkPatch(path string, from, to []byte) string {
	a, b := gitLines(from), gitLines(to)
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	if p == len(a) && p == len(b) {
		return ""
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	return LinesPatch(path, from, []LineEdit{{Start: p, End: len(a) - s, With: b[p : len(b)-s]}})
}

// LineEdit 是按行的一处改动（行级暂存）：把第 Start 到 End 行（从 0 起，不含 End，和 Diff 视图的行号一样）
// 换成 With。With 的每行带着自己的换行（git 分行的算法，见 gitLines）；为空是删掉这几行，Start == End 是插在第 Start 行前面。
type LineEdit struct {
	Start, End int
	With       []string
}

// LinesPatch 是把 edits 套到 path 的 old 上的补丁（git diff 的格式）：每处改动一块，上下各带 HunkContext 行，
// 隔得近（中间没改的不超过 2×HunkContext 行）的并成一块。edits 要按 Start 排好、互不重叠，行号超出 old 时为 ""；
// 什么都没改也是 ""。套完以后除了最后一行都得有换行：没有换行的最后一行后面插了东西，就把它也算进改动、补上换行。
func LinesPatch(path string, old []byte, edits []LineEdit) string {
	a := gitLines(old)
	var es []LineEdit
	for _, e := range edits {
		if e.Start < 0 || e.Start > e.End || e.End > len(a) || len(es) > 0 && e.Start < es[len(es)-1].End {
			return ""
		}
		if e.Start == e.End && len(e.With) == 0 {
			continue
		}
		if k := len(es) - 1; k >= 0 && es[k].End == e.Start { // 挨着的并起来
			es[k].End, es[k].With = e.End, append(es[k].With, e.With...)
			continue
		}
		e.With = slices.Clone(e.With)
		es = append(es, e)
	}
	// 没换行的最后一行后面接着写：把它也算进改动、补上换行（这样可能和前一处挨上）
	if k, n := len(es)-1, len(a); k >= 0 && es[k].Start == n && n > 0 && !strings.HasSuffix(a[n-1], "\n") {
		e := &es[k]
		e.Start--
		e.With = append([]string{a[n-1] + "\n"}, e.With...)
		if k > 0 && es[k-1].End == e.Start {
			es[k-1].End, es[k-1].With = e.End, append(es[k-1].With, e.With...)
			es = es[:k]
		}
	}
	for i := range es { // 后面还有行的，自己的最后一行要有换行
		e := &es[i]
		for j, l := range e.With {
			if !strings.HasSuffix(l, "\n") && (j < len(e.With)-1 || e.End < len(a)) {
				e.With[j] = l + "\n"
			}
		}
	}
	if len(es) == 0 {
		return ""
	}
	var w strings.Builder
	an, bn := patchName("a/", path), patchName("b/", path)
	fmt.Fprintf(&w, "diff --git %s %s\n--- %s\n+++ %s\n", an, bn, an, bn)
	line := func(prefix byte, l string) {
		w.WriteByte(prefix)
		w.WriteString(l)
		if !strings.HasSuffix(l, "\n") {
			w.WriteString("\n\\ No newline at end of file\n")
		}
	}
	delta := 0 // 前面的块让新的一边多了（少了）几行
	for i := 0; i < len(es); {
		j := i + 1
		for j < len(es) && es[j].Start-es[j-1].End <= 2*HunkContext {
			j++
		}
		group := es[i:j]
		from := max(0, group[0].Start-HunkContext)
		to := min(len(a), group[len(group)-1].End+HunkContext)
		grow := 0
		for _, e := range group {
			grow += len(e.With) - (e.End - e.Start)
		}
		fmt.Fprintf(&w, "@@ -%s +%s @@\n", hunkRange(from, to-from), hunkRange(from+delta, to-from+grow))
		pos := from
		for _, e := range group {
			for _, l := range a[pos:e.Start] {
				line(' ', l)
			}
			for _, l := range a[e.Start:e.End] {
				line('-', l)
			}
			for _, l := range e.With {
				line('+', l)
			}
			pos = e.End
		}
		for _, l := range a[pos:to] {
			line(' ', l)
		}
		delta += grow
		i = j
	}
	return w.String()
}

// Lines 按 git 的算法分行（LineEdit.With 要的那种）：每行带着自己的 "\n"，最后一行没有 "\n" 时就是没有。
// 以 "\n" 结尾的文件比 Diff 视图少一行（视图把最后那个换行后面的空行也算一行）。
func Lines(data []byte) []string { return gitLines(data) }

// gitLines 按 git 的算法分行：每行带着自己的 "\n"，最后一行没有 "\n" 时就是没有。
func gitLines(data []byte) []string {
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// hunkRange 是块头里一边的 "起始行,行数"（从 1 起；没有行时起始写它前面那一行）。
func hunkRange(start, n int) string {
	switch n {
	case 0:
		return strconv.Itoa(start) + ",0"
	case 1:
		return strconv.Itoa(start + 1)
	}
	return strconv.Itoa(start+1) + "," + strconv.Itoa(n)
}

// patchName 是补丁头里的文件名：有引号、反斜杠或控制字符时像 git 一样加引号转义。
func patchName(prefix, path string) string {
	name := prefix + path
	if !strings.ContainsFunc(name, func(r rune) bool { return r == '"' || r == '\\' || r < 0x20 || r == 0x7f }) {
		return name
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(name); i++ {
		switch ch := name[i]; ch {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		default:
			if ch < 0x20 || ch == 0x7f {
				fmt.Fprintf(&b, `\%03o`, ch)
			} else {
				b.WriteByte(ch)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
