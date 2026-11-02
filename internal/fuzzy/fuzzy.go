// Package fuzzy 是快速打开和命令面板用的模糊匹配：查询里的字按顺序出现在目标里就算匹配（不分大小写，
// 空格忽略），再按"连着命中、命中在词首、命中在文件名里、越短越靠前"打分。
//
// 每个目标只扫两遍（先从左往右找到最早能匹配完的位置，再从那里往回找最短的一段），
// 一万多个文件每敲一个字也只要几毫秒。
package fuzzy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// 打分的权重。
const (
	scoreHit       = 16  // 每个命中的字
	bonusRun       = 12  // 紧接着上一个命中
	bonusBoundary  = 10  // 命中在词首：开头、/ _ - . 空格之后、小写后的大写、字母和数字交界
	bonusSlash     = 14  // 命中在路径一段的开头（/ 之后）
	bonusFirst     = 8   // 第一个字就命中在目标开头
	bonusName      = 60  // 全部命中在文件名（最后一个 / 之后）里
	bonusNamePref  = 40  // 文件名以查询开头
	bonusExact     = 80  // 文件名（去不去扩展名都行）或命令名就是查询
	penaltyGap     = 2   // 两个命中之间每隔一个字
	penaltyGapMax  = 20  // 一处间隔最多扣这么多
	penaltyLength  = 1   // 目标每多 8 个字节扣 1
	penaltyLengthD = 8   //
	maxQuery       = 128 // 查询太长就只看前这么多字
)

// Pattern 是准备好的查询：小写、去掉空格。
type Pattern struct {
	runes []rune
	text  string // 小写、去掉空格后的查询
}

// New 准备查询 q。
func New(q string) Pattern {
	var rs []rune
	for _, r := range strings.ToLower(q) {
		if unicode.IsSpace(r) {
			continue
		}
		rs = append(rs, r)
		if len(rs) == maxQuery {
			break
		}
	}
	return Pattern{runes: rs, text: string(rs)}
}

// Empty 是查询里没有字。
func (p Pattern) Empty() bool { return len(p.runes) == 0 }

// Match 在 s 里找查询；ok 时 hits 是命中的每个字在 s 里的字节下标（升序），score 越大越好
// （分数一样时调用方让短的靠前）。
// name 是 s 里"名字"开始的字节下标（文件名在路径里的位置；命令就是 0）：全部命中在名字里加分。
func (p Pattern) Match(s string, name int) (score int, hits []int, ok bool) {
	return p.matchHits(s, name, false)
}

// MatchPath 和 Match 一样，给仓库里的相对路径用（文件树的筛选）：name 为 0 是仓库根下的文件，整个 s 就是文件名，
// 也算全部命中在文件名里（Match 把 0 当成没有名字）。
func (p Pattern) MatchPath(s string, name int) (score int, hits []int, ok bool) {
	return p.matchHits(s, name, true)
}

// ScorePath 和 MatchPath 一样，只要分、不要命中的字：不分配，筛选几万个文件时先用它打分，留下的再 MatchPath。
func (p Pattern) ScorePath(s string, name int) (score int, ok bool) {
	if len(p.runes) == 0 {
		return 0, true
	}
	var buf [maxQuery]int
	return p.find(s, name, true, buf[:len(p.runes)])
}

func (p Pattern) matchHits(s string, name int, path bool) (int, []int, bool) {
	if len(p.runes) == 0 {
		return 0, nil, true
	}
	hits := make([]int, len(p.runes))
	sc, ok := p.find(s, name, path, hits)
	if !ok {
		return 0, nil, false
	}
	return sc, hits, true
}

// find 是 Match / MatchPath 的匹配，命中的字记进 hits（长度是查询的字数）。path 为真时 name 为 0 也算有名字（整个 s）。
func (p Pattern) find(s string, name int, path bool, hits []int) (int, bool) {
	// 整个 s 都匹配不上的（大多数）只从左往右扫一遍；匹配得上的先试只在名字里匹配，不行再在整个 s 里
	if p.end(s, 0) < 0 {
		return 0, false
	}
	if (name > 0 || path) && name < len(s) {
		if sc, ok := p.match(s, name, hits); ok {
			return sc + bonusName, true
		}
		if name == 0 {
			return 0, false
		}
	}
	return p.match(s, 0, hits)
}

// end 是从 s[from:] 的开头往右，最早在哪里（s 里的字节下标）能把查询匹配完；匹配不上为 -1。
func (p Pattern) end(s string, from int) int {
	qi := 0
	for i, r := range s[from:] {
		if unicode.ToLower(r) == p.runes[qi] {
			qi++
			if qi == len(p.runes) {
				return from + i + utf8.RuneLen(r)
			}
		}
	}
	return -1
}

func (p Pattern) match(s string, from int, hits []int) (int, bool) {
	// 从左往右：最早在哪里能把查询匹配完
	end := p.end(s, from)
	if end < 0 {
		return 0, false
	}
	// 从结尾往回：最短的一段（让命中尽量挤在一起）
	qi := len(p.runes) - 1
	for i := end; i > from && qi >= 0; {
		r, n := utf8.DecodeLastRuneInString(s[:i])
		i -= n
		if unicode.ToLower(r) == p.runes[qi] {
			hits[qi] = i
			qi--
		}
	}
	return p.score(s, from, hits), true
}

// score 给一组命中打分。
func (p Pattern) score(s string, from int, hits []int) int {
	sc := 0
	prevEnd := -1
	for k, h := range hits {
		r, n := utf8.DecodeRuneInString(s[h:])
		sc += scoreHit
		switch {
		case h == prevEnd:
			sc += bonusRun
		case prevEnd >= 0:
			gap := utf8.RuneCountInString(s[prevEnd:h])
			sc -= min(gap*penaltyGap, penaltyGapMax)
		}
		if h == from {
			if k == 0 {
				sc += bonusFirst
			}
			sc += bonusBoundary
		} else {
			prev, _ := utf8.DecodeLastRuneInString(s[:h])
			switch {
			case prev == '/':
				sc += bonusSlash
			case boundary(prev, r):
				sc += bonusBoundary
			}
		}
		prevEnd = h + n
	}
	name := s[from:]
	if i := strings.LastIndexByte(s, '/'); i >= from {
		name = s[i+1:]
	}
	low := strings.ToLower(name)
	stem := low
	if i := strings.LastIndexByte(low, '.'); i > 0 {
		stem = low[:i]
	}
	switch {
	case low == p.text || stem == p.text:
		sc += bonusExact
	case strings.HasPrefix(low, p.text):
		sc += bonusNamePref
	}
	return sc - len(s)/penaltyLengthD*penaltyLength
}

// boundary 是 r 在 prev 之后算不算一个词的开头。
func boundary(prev, r rune) bool {
	switch {
	case prev == '_' || prev == '-' || prev == '.' || prev == ' ' || prev == '\\' || prev == ':':
		return true
	case unicode.IsLower(prev) && unicode.IsUpper(r):
		return true
	case unicode.IsLetter(prev) != unicode.IsLetter(r) && (unicode.IsDigit(prev) || unicode.IsDigit(r)):
		return true
	case prev < utf8.RuneSelf && r >= utf8.RuneSelf, prev >= utf8.RuneSelf && r < utf8.RuneSelf:
		return true // ASCII 和中文的交界
	}
	return false
}
