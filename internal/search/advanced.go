package search

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/dlclark/regexp2/v2"

	"github.com/lemoeade/grove/internal/i18n"
)

// 高级正则：Go 的 RE2 不认反向引用和环视。表达式 RE2 编不了、里面又有这几种写法时，换 regexp2
// （.NET 语法的回溯引擎，RE2 兼容模式：\d \w \s 和 $ 跟 RE2 一样，组按出现的顺序编号）。回溯会爆炸，
// 所以一次匹配最多跑 advTimeout，超时的文件当跳过；字面搜索和 RE2 编得了的正则一点不变。

var (
	// advTimeout 是高级正则一次匹配最多跑多久（regexp2 的 MatchTimeout）；测试里调短。
	advTimeout = 2 * time.Second
	// previewTimeout 是结果预览里展开替换文字那次匹配的上限：每帧都跑，宁可画原样的替换词也不卡。
	previewTimeout = 50 * time.Millisecond
)

// Advanced 是这个搜索走的 regexp2。
func (m *Matcher) Advanced() bool { return m != nil && m.adv != nil }

// advancedSyntax 是 expr 里有 RE2 不认、regexp2 认的写法：反向引用 \1–\9、\k<name>，
// 环视 (?= (?! (?<= (?<!、命名组 (?<，原子组 (?>。转义过的反斜杠后面的不算。
func advancedSyntax(expr string) bool {
	for i := 0; i < len(expr)-1; i++ {
		if expr[i] == '\\' {
			c := expr[i+1]
			if '1' <= c && c <= '9' || c == 'k' && strings.HasPrefix(expr[i+2:], "<") {
				return true
			}
			i++
			continue
		}
		for _, p := range []string{"(?=", "(?!", "(?<", "(?>"} {
			if strings.HasPrefix(expr[i:], p) {
				return true
			}
		}
	}
	return false
}

// compileAdvanced 用 regexp2 编 q.Text（q.Regexp 且 RE2 编不了的）；写错了报 regexp2 的错。
func (m *Matcher) compileAdvanced(q Query) error {
	opt := regexp2.RE2 | regexp2.Multiline
	if !q.CaseSensitive {
		opt |= regexp2.IgnoreCase
	}
	adv, err := regexp2.Compile(q.Text, opt, regexp2.OptionMaintainCaptureOrder())
	if err != nil {
		return errors.New(i18n.Tf("正则写错了：%s", strings.TrimPrefix(err.Error(), "error parsing regexp: ")))
	}
	quick := regexp2.MustCompile(q.Text, opt, regexp2.OptionMaintainCaptureOrder())
	adv.MatchTimeout, quick.MatchTimeout = advTimeout, previewTimeout
	m.adv, m.quick, m.expand = adv, quick, true
	return nil
}

// bytesString 不拷贝地把 b 当字符串借给 regexp2（它的字节位置只有字符串的接口才给）；b 在用完之前不能改。
func bytesString(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }

// advHit 是高级正则在一个文件里的一处：在 data 里的长度和换成的文字。
type advHit struct {
	span int
	text string
}

// advHits 把整个 data 匹配一遍，按起点（字节）记下每一处换成 repl 的文字：环视要看匹配前后的字，
// 不能像 RE2 那样从这一处切开再配；也省得每一处都把整个文件解码一遍。超时报错。
func (m *Matcher) advHits(data []byte, repl string) (map[int]advHit, error) {
	src := bytesString(data)
	hits := map[int]advHit{}
	hit, err := m.adv.FindStringMatch(src)
	for n := 0; hit != nil && err == nil && n < 2*MaxLimit; n++ {
		loc, names := advLoc(hit)
		hits[loc[0]] = advHit{loc[1] - loc[0], string(expandTemplate(nil, repl, src, loc, names))}
		hit, err = m.adv.FindNextMatch(hit)
	}
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// advLoc 是 regexp2 的一次匹配换成 regexp 那样的组位置（字节，相对传进去的字符串；没参与的组是 -1）和组名（没名字的是空串）。
func advLoc(hit *regexp2.Match) (loc []int, names []string) {
	gs := hit.Groups()
	loc, names = make([]int, 2*len(gs)), make([]string, len(gs))
	for i := range gs {
		if gs[i].Name != strconv.Itoa(i) {
			names[i] = gs[i].Name
		}
		if len(gs[i].Captures) == 0 {
			loc[2*i], loc[2*i+1] = -1, -1
			continue
		}
		s, n := gs[i].ByteRange()
		loc[2*i], loc[2*i+1] = s, s+n
	}
	return loc, names
}

// expandTemplate 照 regexp.Expand 的规矩把 template 展开接在 dst 后面：$1、${1}、$name、${name}，$$ 是 $；
// 名字尽量长（$1x 是 ${1x}），写坏的 $ 原样留着，没有的组、没参与匹配的组换成空。
// loc 是组的起止（相对 src），names 按组号。regexp2 用它；RE2 照旧走 regexp.Expand，两边一样（TestExpandTemplate 对过）。
func expandTemplate(dst []byte, template, src string, loc []int, names []string) []byte {
	for {
		before, after, ok := strings.Cut(template, "$")
		if !ok {
			break
		}
		dst = append(dst, before...)
		template = after
		if strings.HasPrefix(template, "$") {
			dst = append(dst, '$')
			template = template[1:]
			continue
		}
		name, num, rest, ok := templateRef(template)
		if !ok {
			dst = append(dst, '$')
			continue
		}
		template = rest
		if num >= 0 {
			if 2*num+1 < len(loc) && loc[2*num] >= 0 {
				dst = append(dst, src[loc[2*num]:loc[2*num+1]]...)
			}
			continue
		}
		for i, n := range names {
			if n == name && 2*i+1 < len(loc) && loc[2*i] >= 0 {
				dst = append(dst, src[loc[2*i]:loc[2*i+1]]...)
				break
			}
		}
	}
	return append(dst, template...)
}

// templateRef 读 $ 后面的组名（regexp 的 extract）：num 是数字名的组号（不是数字、或者有前导 0 时为 -1）。
func templateRef(s string) (name string, num int, rest string, ok bool) {
	brace := strings.HasPrefix(s, "{")
	if brace {
		s = s[1:]
	}
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i += size
	}
	if i == 0 {
		return "", 0, "", false
	}
	name = s[:i]
	if brace {
		if i >= len(s) || s[i] != '}' {
			return "", 0, "", false
		}
		i++
	}
	num = 0
	for j := 0; j < len(name); j++ {
		if name[j] < '0' || '9' < name[j] || num >= 1e8 {
			num = -1
			break
		}
		num = num*10 + int(name[j]-'0')
	}
	if name[0] == '0' && len(name) > 1 {
		num = -1
	}
	return name, num, s[i:], true
}
