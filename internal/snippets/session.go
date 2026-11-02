package snippets

import (
	"slices"
	"strings"
)

// Offer 是给编辑器的一批补全里的片段（mygo 的补全不告诉选了哪一项，事后认）：Start 是被换掉的那个词从哪开始，
// Ver 是给的时候缓冲区的版本，Items 和补全一一对应。
type Offer struct {
	Start int
	Ver   uint64
	Items []Expansion
}

// Session 是插进去的片段在跳位之间走（Tab / ⇧Tab）：Start、End 是它在缓冲区里的范围，Stops 的范围是缓冲区里的
// 偏移（最后一个是 $0），Idx 是现在在第几个。Len、Text 是上次对过时缓冲区的长度和 [Start, End) 的文字，Sync 拿它们认改动在哪。
type Session struct {
	Start, End int
	Stops      []Stop
	Idx        int
	Ver        uint64
	Len        int
	Text       string
}

// NewSession 是在 at 插进了 e.Text 之后的会话（n、ver 是插完的缓冲区长度和版本）；除了 $0 没有跳位的为 nil。
func NewSession(at int, e Expansion, n int, ver uint64) *Session {
	if len(e.Stops) < 2 {
		return nil
	}
	s := &Session{Start: at, End: at + len(e.Text), Ver: ver, Len: n, Text: e.Text}
	for _, st := range e.Stops {
		rs := make([][2]int, len(st.Ranges))
		for i, r := range st.Ranges {
			rs[i] = [2]int{r[0] + at, r[1] + at}
		}
		s.Stops = append(s.Stops, Stop{N: st.N, Ranges: rs})
	}
	return s
}

// Count 是跳位有几个（不算 $0），Current 是现在在第几个（从 1 数）：状态栏的“片段 2/3”。
func (s *Session) Count() int   { return len(s.Stops) - 1 }
func (s *Session) Current() int { return s.Idx + 1 }

// Ranges 是现在这个跳位的几处（选中它们）。
func (s *Session) Ranges() [][2]int { return s.Stops[s.Idx].Ranges }

// Move 走到下一个（dir 1）/ 上一个（-1）跳位（跳过改没了的），第一个往前不动；返回走到的是不是 $0（会话该结束了）。
func (s *Session) Move(dir int) (last bool) {
	for i := s.Idx + dir; i >= 0; i += dir {
		if i >= len(s.Stops)-1 {
			s.Idx = len(s.Stops) - 1
			return true
		}
		if len(s.Stops[i].Ranges) > 0 {
			s.Idx = i
			return false
		}
	}
	return false
}

// Sync 跟上缓冲区（版本 ver、长度 n、slice 取文字）。只认改在现在这个跳位里的、几处一样的改法：后面的跳位和 End 跟着挪，
// 套在这个跳位里的跳位丢掉。别处改了（对不上）返回 false，会话该结束了。
func (s *Session) Sync(ver uint64, n int, slice func(a, b int) string) bool {
	if ver == s.Ver {
		return true
	}
	cur := s.Stops[s.Idx].Ranges
	diff := n - s.Len
	k := len(cur)
	if k == 0 || diff%k != 0 || cur[0][0] < s.Start || cur[k-1][1] > s.End {
		return false
	}
	delta := diff / k
	// 一个不属于现在这个跳位的位置挪多少：它前面（含碰着的）有几处，起点碰着空的一处时算在后面
	move := func(p int, isEnd bool) int {
		c := 0
		for _, r := range cur {
			if r[1] < p || r[1] == p && (!isEnd || r[0] < r[1]) {
				c++
			}
		}
		return p + c*delta
	}
	start, end := s.Start, s.End+diff // 现在这个跳位的几处都在片段里面
	if end > n || start > end {
		return false
	}
	newCur := make([][2]int, k)
	for i, r := range cur {
		newCur[i] = [2]int{r[0] + i*delta, r[1] + (i+1)*delta}
		if newCur[i][0] < start || newCur[i][1] > end || newCur[i][0] > newCur[i][1] {
			return false
		}
	}
	text := slice(start, end)
	if len(text) != end-start {
		return false
	}
	typed := text[newCur[0][0]-start : newCur[0][1]-start]
	var want strings.Builder
	at := s.Start
	for i, r := range cur {
		want.WriteString(s.Text[at-s.Start : r[0]-s.Start])
		if got := text[newCur[i][0]-start : newCur[i][1]-start]; got != typed {
			return false
		}
		want.WriteString(typed)
		at = r[1]
	}
	want.WriteString(s.Text[at-s.Start:])
	if want.String() != text {
		return false
	}
	for j := range s.Stops {
		if j == s.Idx {
			s.Stops[j].Ranges = newCur
			continue
		}
		rs := s.Stops[j].Ranges[:0:0]
		for _, r := range s.Stops[j].Ranges {
			if slices.ContainsFunc(cur, func(c [2]int) bool { return c[0] <= r[0] && r[1] <= c[1] && c != r }) {
				continue // 套在改了的那处里面：没了
			}
			if r[0] == r[1] { // 空的一处两头一起挪：碰着现在这一处的，打的字在它前面
				p := move(r[0], false)
				rs = append(rs, [2]int{p, p})
				continue
			}
			rs = append(rs, [2]int{move(r[0], false), move(r[1], true)})
		}
		s.Stops[j].Ranges = rs
	}
	s.Start, s.End, s.Len, s.Ver, s.Text = start, end, n, ver, text
	return true
}
