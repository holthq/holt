package snippets

import (
	"slices"
	"strconv"
	"strings"
)

// Stop 是一个跳位：N 是号（0 是最后那个），Ranges 是它在文字里的几处 [起, 止)（字节；同号的几处一起改）。
type Stop struct {
	N      int
	Ranges [][2]int
}

// Expansion 是一条片段展开后的样子：插进去的文字和跳位（偏移从文字开头算）。
type Expansion struct {
	Text  string
	Stops []Stop
}

// node 是解析出来的一段：文字，或一个占位符（n ≥ 0，kids 是它的默认值，nil 是没写默认）。
type node struct {
	text string
	n    int
	kids []node
}

// parse 把 body 拆成文字和占位符。inner 时遇到没转义的 } 就停（占位符的默认值），返回停在哪。
func parse(s string, i int, inner bool) ([]node, int) {
	var out []node
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, node{text: b.String(), n: -1})
			b.Reset()
		}
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte(`$}\`, s[i+1]) >= 0:
			b.WriteByte(s[i+1])
			i += 2
		case c == '}' && inner:
			flush()
			return out, i
		case c == '$' && i+1 < len(s) && isDigit(s[i+1]):
			j := i + 1
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			n, _ := strconv.Atoi(s[i+1 : j])
			flush()
			out = append(out, node{n: n})
			i = j
		case c == '$' && i+2 < len(s) && s[i+1] == '{' && isDigit(s[i+2]):
			j := i + 2
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			n, _ := strconv.Atoi(s[i+2 : j])
			switch {
			case j < len(s) && s[j] == '}':
				flush()
				out = append(out, node{n: n})
				i = j + 1
				continue
			case j < len(s) && s[j] == ':':
				kids, k := parse(s, j+1, true)
				if k < len(s) {
					flush()
					if kids == nil {
						kids = []node{}
					}
					out = append(out, node{n: n, kids: kids})
					i = k + 1
					continue
				}
			}
			b.WriteByte(c) // ${1|a,b|}、没有收尾的 }：照原样
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	flush()
	return out, i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Expand 展开 body：占位符换成默认值（同号没写默认的跟着写了默认的那处），第二行起补 indent（当前行的缩进），
// 行首的 Tab 换成 unit（文档的一级缩进）。跳位按号从小到大，$0 最后；没写 $0 的在末尾补一个。
func Expand(body, indent, unit string) Expansion {
	nodes, _ := parse(body, 0, false)
	defs := map[int][]node{}
	var find func([]node)
	find = func(ns []node) {
		for _, n := range ns {
			if n.n >= 0 && n.kids != nil {
				if _, ok := defs[n.n]; !ok {
					defs[n.n] = n.kids
				}
				find(n.kids)
			}
		}
	}
	find(nodes)

	var b strings.Builder
	lineStart := false // 刚换行、还没补缩进
	leading := false   // 还在行首的 Tab 里
	ranges := map[int][][2]int{}
	pad := func() {
		if lineStart {
			b.WriteString(indent)
			lineStart, leading = false, true
		}
	}
	write := func(s string) {
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == '\n' {
				b.WriteByte(c)
				lineStart, leading = true, false
				continue
			}
			pad()
			if c == '\t' && leading {
				b.WriteString(unit)
				continue
			}
			leading = false
			b.WriteByte(c)
		}
	}
	var emit func(ns []node, record bool)
	emit = func(ns []node, record bool) {
		for _, n := range ns {
			if n.n < 0 {
				write(n.text)
				continue
			}
			pad() // 占位符在行首时缩进算在它前面
			start := b.Len()
			switch {
			case n.kids != nil:
				emit(n.kids, record)
			case defs[n.n] != nil:
				emit(defs[n.n], false) // 跟着的那几处只要文字，里面套的不算跳位
			}
			if record {
				ranges[n.n] = append(ranges[n.n], [2]int{start, b.Len()})
			}
		}
	}
	emit(nodes, true)

	var nums []int
	for n := range ranges {
		if n != 0 {
			nums = append(nums, n)
		}
	}
	slices.Sort(nums)
	var stops []Stop
	for _, n := range nums {
		stops = append(stops, Stop{N: n, Ranges: ranges[n]})
	}
	end := ranges[0]
	if end == nil {
		end = [][2]int{{b.Len(), b.Len()}}
	}
	stops = append(stops, Stop{N: 0, Ranges: end[:1]})
	return Expansion{Text: b.String(), Stops: stops}
}
