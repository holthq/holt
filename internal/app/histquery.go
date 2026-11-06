package app

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/holthq/holt/internal/git"
)

// 这个文件是历史搜索框的查询语法：还是那一个框，裸词照旧是"提交信息或作者里有、或哈希开头"，
// 另外认 author: / after: / before: / path:（中文 作者: / 之后: / 之前: / 路径:，冒号全角半角都行），
// "带 空格 的词"（引号里的整段算一个词，键的值也能用引号），和 /正则/（斜杠包起来按 git log -E）。

// HistQuery 是搜索框里的字拆出来的条件。
type HistQuery struct {
	Words   string   // 裸词和引号里的词，用一个空格连起来；Regex 时是斜杠里的正则
	Regex   bool     // Words 是 /正则/
	Authors []string // author:，几个是"任一个"（git 的 --author 本来就是或）
	After   string   // after: 那天（月）0 点起；yyyy-mm[-dd] 换成"yyyy-mm-dd 00:00:00"，别的原样交给 git
	Before  string   // before: 那天（月）0 点之前
	Paths   []string // path:，仓库内的目录或文件，几个是"动过任一个"
	Bad     []string // 看不懂的日期（2026-13-01），搜索框下面一行小字；那个条件不算
}

// histKeys 是认的键（小写）→ 哪个字段。
var histKeys = map[string]string{
	"author": "author", "作者": "author",
	"after": "after", "since": "after", "之后": "after",
	"before": "before", "until": "before", "之前": "before",
	"path": "path", "路径": "path",
}

// isoDate 是 yyyy-mm 或 yyyy-mm-dd（月、日一位两位都行）。
var isoDate = regexp.MustCompile(`^(\d{4})-(\d{1,2})(?:-(\d{1,2}))?$`)

// ParseHistQuery 拆搜索框里的字。
func ParseHistQuery(s string) HistQuery {
	var q HistQuery
	var words []string
	for _, tk := range histTokens(s) {
		if tk.regex {
			q.Words, q.Regex = tk.text, true
			continue
		}
		key, val, ok := "", tk.text, false
		if !tk.quoted {
			key, val, ok = splitKey(tk.text)
		}
		if !ok {
			if tk.text != "" {
				words = append(words, tk.text)
			}
			continue
		}
		if val == "" {
			continue // 键后面还没写
		}
		switch key {
		case "author":
			q.Authors = append(q.Authors, val)
		case "path":
			q.Paths = append(q.Paths, strings.Trim(val, "/"))
		case "after", "before":
			d, ok := normDate(val)
			if !ok {
				q.Bad = append(q.Bad, val)
				continue
			}
			if key == "after" {
				q.After = d
			} else {
				q.Before = d
			}
		}
	}
	if !q.Regex {
		q.Words = strings.Join(words, " ")
	}
	return q
}

// normDate 把 yyyy-mm[-dd] 换成"yyyy-mm-dd 00:00:00"（git 只给 yyyy-mm-dd 时取的是此刻的钟点，yyyy-mm 更是认错）；
// 不是这种写法的原样返回，交给 git 认；是这种写法但没有这一天（2026-13-01）ok 为 false。
func normDate(s string) (string, bool) {
	m := isoDate.FindStringSubmatch(s)
	if m == nil {
		return s, true
	}
	if m[3] == "" {
		m[3] = "1"
	}
	t, err := time.Parse("2006-1-2", m[1]+"-"+m[2]+"-"+m[3])
	if err != nil {
		return "", false
	}
	return t.Format(time.DateOnly) + " 00:00:00", true
}

// apply 把除日期以外的条件填进 git 的查询（日期要在后台认，见 resolveDates）。
func (hq HistQuery) apply(q *git.LogQuery) {
	q.Search, q.Regex, q.Authors, q.Paths = hq.Words, hq.Regex, hq.Authors, hq.Paths
}

// resolveDates 在后台把 after: / before: 换成时间填进 q（相对日期要 git rev-parse）；认不出的不算，返回它们的原文。
func (hq HistQuery) resolveDates(ctx context.Context, root string, q *git.LogQuery) (bad []string) {
	for _, d := range []struct {
		s string
		t *time.Time
	}{{hq.After, &q.After}, {hq.Before, &q.Before}} {
		if d.s == "" {
			continue
		}
		t, err := git.ResolveDate(ctx, root, d.s)
		if err != nil {
			bad = append(bad, d.s)
			continue
		}
		*d.t = t
	}
	return bad
}

// histToken 是搜索框里的一段。
type histToken struct {
	text          string
	quoted, regex bool // 以引号开头（不当键）/ 是 /正则/
}

// histTokens 按空白切开，引号（"" 或 “”）里的空白不切，键的值也可以是引号包起来的（author:"Ada L"）；
// / 开头、后面还有 / 的一段是正则，里面的空白也不切（\/ 是斜杠本身）。
func histTokens(s string) []histToken {
	var out []histToken
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if unicode.IsSpace(rs[i]) {
			i++
			continue
		}
		if rs[i] == '/' {
			if j := regexEnd(rs, i+1); j > i+1 {
				out = append(out, histToken{text: strings.ReplaceAll(string(rs[i+1:j]), `\/`, "/"), regex: true})
				i = j + 1
				continue
			}
		}
		var b strings.Builder
		quoted := false
		start := i
		for i < len(rs) && !unicode.IsSpace(rs[i]) {
			if close := quoteClose(rs[i]); close != 0 {
				j := i + 1
				for j < len(rs) && rs[j] != close {
					j++
				}
				quoted = quoted || i == start
				b.WriteString(string(rs[i+1 : min(j, len(rs))]))
				i = min(j+1, len(rs))
				continue
			}
			b.WriteRune(rs[i])
			i++
		}
		out = append(out, histToken{text: b.String(), quoted: quoted})
	}
	return out
}

// quoteClose 是引号 r 配对的那个，r 不是左引号为 0。
func quoteClose(r rune) rune {
	switch r {
	case '"':
		return '"'
	case '“':
		return '”'
	}
	return 0
}

// regexEnd 是从 rs[from] 起第一个没被 \ 转义、后面是空白或结尾的 /；没有为 -1。
func regexEnd(rs []rune, from int) int {
	for j := from; j < len(rs); j++ {
		switch {
		case rs[j] == '\\':
			j++
		case rs[j] == '/' && (j+1 == len(rs) || unicode.IsSpace(rs[j+1])):
			return j
		}
	}
	return -1
}

// splitKey 认 "键:值"（冒号全角半角都行，键不分大小写）；不是认得的键 ok 为 false。
func splitKey(s string) (key, val string, ok bool) {
	i := strings.IndexAny(s, ":：")
	if i <= 0 {
		return "", "", false
	}
	key, ok = histKeys[strings.ToLower(s[:i])]
	if !ok {
		return "", "", false
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return key, s[i+size:], true
}
