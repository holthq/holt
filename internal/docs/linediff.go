package docs

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
)

// lineDiffMax 是按行比的时候最多算到多少处不同；再多（几乎整个文件都换了）就把头尾相同的行中间整段换掉。
const lineDiffMax = 1000

// LineEdits 是把 old 改成 new 要动的地方（按行的 Myers：先去掉头尾相同的行，再比中间；几行换几行时一行一处，
// 每处再收窄到头尾不同的那几个字，narrow），偏移按 old 算，给 Buf.Edit 一步套上（一步撤销）。没动的行、改了的行里
// 没动的字原样留着，光标、折叠、滚动就跟着留着（外部格式化用）。
func LineEdits(old, new string) []editor.Edit {
	a, b := splitLines(old), splitLines(new)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	off := make([]int, len(a)+1) // off[i] 是 old 里第 i 行开头的偏移
	for i, l := range a {
		off[i+1] = off[i] + len(l)
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma) == 0 && len(mb) == 0 {
		return nil
	}
	pairs, ok := myers(ma, mb)
	if !ok {
		return []editor.Edit{narrow(old, editor.Edit{Start: off[pre], End: off[len(a)-suf], Text: strings.Join(mb, "")})}
	}
	var edits []editor.Edit
	i, j := 0, 0
	flush := func(ti, tj int) {
		switch {
		case ti-i == tj-j: // 几行换几行（重新缩进、对齐）：一行一处，光标留在自己那一行
			for k := range ti - i {
				if ma[i+k] != mb[j+k] {
					edits = append(edits, narrow(old, editor.Edit{Start: off[pre+i+k], End: off[pre+i+k+1], Text: mb[j+k]}))
				}
			}
		case i < ti || j < tj:
			edits = append(edits, narrow(old, editor.Edit{Start: off[pre+i], End: off[pre+ti], Text: strings.Join(mb[j:tj], "")}))
		}
	}
	for _, p := range pairs {
		flush(p[0], p[1])
		i, j = p[0]+1, p[1]+1
	}
	flush(len(ma), len(mb))
	return edits
}

// narrow 把整行的改动 e（old 上的偏移）收窄：去掉换下的和换上的头尾相同的字节，退到 UTF-8 字符的开头。
// 光标在改了的行里、不在改了的那几个字上时，就不会被挤到下一行开头。
func narrow(old string, e editor.Edit) editor.Edit {
	o, n := old[e.Start:e.End], e.Text
	p := 0
	for p < len(o) && p < len(n) && o[p] == n[p] {
		p++
	}
	for p > 0 && (p < len(o) && !utf8.RuneStart(o[p]) || p < len(n) && !utf8.RuneStart(n[p])) {
		p--
	}
	s := 0
	for s < len(o)-p && s < len(n)-p && o[len(o)-1-s] == n[len(n)-1-s] {
		s++
	}
	for s > 0 && (!utf8.RuneStart(o[len(o)-s]) || !utf8.RuneStart(n[len(n)-s])) {
		s--
	}
	return editor.Edit{Start: e.Start + p, End: e.End - s, Text: n[p : len(n)-s]}
}

// splitLines 按 \n 切行，每行带着自己的 \n（最后一行没有就没有）。
func splitLines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// myers 是 a、b 的最长公共子序列里一对对相同的行（a 的下标、b 的下标，从前往后）；
// 不同的地方超过 lineDiffMax 处时 ok 为假。
func myers(a, b []string) (pairs [][2]int, ok bool) {
	n, m := len(a), len(b)
	limit := min(n+m, lineDiffMax)
	v := make([]int, 2*limit+2)
	at := func(k int) *int { return &v[k+limit+1] }
	var trace [][]int // trace[d] 是走第 d 步之前、k 在 [-d, d] 上的 v
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v[limit+1-d:limit+2+d]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && *at(k - 1) < *at(k + 1) {
				x = *at(k + 1)
			} else {
				x = *at(k - 1) + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			*at(k) = x
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

// backtrack 从终点沿着 trace 往回走：每一步先退回这一步的斜线（相同的行），再退过那一下增删。
func backtrack(trace [][]int, x, y int) [][2]int {
	var pairs [][2]int
	for d := len(trace) - 1; d >= 0; d-- {
		px, py := 0, 0
		if d > 0 {
			vv := trace[d]
			get := func(k int) int { return vv[k+d] }
			k, pk := x-y, x-y-1
			if k == -d || k != d && get(k-1) < get(k+1) {
				pk = k + 1
			}
			px = get(pk)
			py = px - pk
		}
		for x > px && y > py {
			x, y = x-1, y-1
			pairs = append(pairs, [2]int{x, y})
		}
		x, y = px, py
	}
	for i, j := 0, len(pairs)-1; i < j; i, j = i+1, j-1 {
		pairs[i], pairs[j] = pairs[j], pairs[i]
	}
	return pairs
}
