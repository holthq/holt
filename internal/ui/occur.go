package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	wui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/holthq/holt/internal/docs"
)

// OccurRest 是光标在一个词上停稳多久后高亮这个词的其他出现（像 VS Code 的 occurrencesHighlight）。
var OccurRest = 250 * time.Millisecond

// occurMax 是最多标多少处：再多也看不过来，省得一个满屏都是的词每次重算都上万个范围。
const occurMax = 2000

// whitespaceAlpha 是选中里画出来的空白符号（点、箭头）的不透明度，乘在 TextMuted 上；
// mygo 默认 0.45，一选一大片箭头比字还抢眼，降到和缩进参考线差不多淡。
const whitespaceAlpha = 0.22

// codeTheme 给编辑器套 Holt 改过的代码颜色：现在只有空白符号；别的字段留零，mygo 按窗口外观补默认的。
// 颜色没变就沿用原来那个，不每帧新建。
func codeTheme(st *editor.State, t *wui.Theme) {
	ws := t.TextMuted.Alpha(whitespaceAlpha)
	if st.Theme == nil || st.Theme.Whitespace != ws {
		st.Theme = &editor.Theme{Whitespace: ws}
	}
}

// occurMarks 高亮光标下那个标识符在本文件里的其他出现（整词、区分大小写），画成 St.TextBackgrounds：
// 只在没有选区、只有一个光标时；词或内容一变（挪到别的词、打字）先清掉，停稳 OccurRest 再标，所以打字时不高亮。
// 有选区时 mygo 自己标选中文字的其他出现，这里不管。
// 文件大于 docs.StickyQuick 时不全文搜，只标看得见的那几行（滚了重标）；小文件按内容版本和词算一次。
// 搜索编辑器的 TextBackgrounds 是搜到的那几处（searchDocMarks），十六进制视图没有词，这两种不标。
func occurMarks(c *wui.Context, d *docs.Doc) {
	st, o := &d.St, &d.Occur
	if st.Buffer == nil || d.Kind == docs.SearchDoc || d.Hex {
		occurClear(st, o)
		o.Word = ""
		return
	}
	snap := st.Buffer.Snapshot()
	ver, sel := snap.Version(), st.Selection()
	n := len(st.Selections())
	if ver != o.Ver || sel != o.Sel || n != o.N {
		word, from := "", 0
		if n == 1 && sel.Anchor == sel.Head {
			word, from = wordAt(snap, sel.Head)
		}
		switch {
		case ver != o.Ver || word != o.Word:
			// 打了字或换了个词：先不标，重新计时
			occurClear(st, o)
			o.At = c.Now()
		case from != o.From && o.Done:
			o.Done = false // 同一个词的另一处：马上换一下哪处不标
		}
		o.Ver, o.Sel, o.N, o.Word, o.From = ver, sel, n, word, from
	}
	if o.Word == "" {
		return
	}
	dark := c.Theme().Dark
	big := snap.Len() > docs.StickyQuick
	first, last := st.VisibleLines()
	if o.Done && o.Dark == dark && (!o.Partial || first == o.First && last == o.Last) {
		return
	}
	if !o.Done {
		if wait := o.At.Add(OccurRest).Sub(c.Now()); wait > 0 {
			c.After(wait)
			return
		}
	}
	base, text := 0, ""
	if big {
		lines := snap.LineCount()
		first, last = max(first, 0), min(last, lines-1)
		if last < first { // 还没排过版
			return
		}
		base = snap.LineStart(first)
		text = snap.Slice(base, snap.LineEnd(last))
	} else {
		text = snap.String()
	}
	col := editor.DefaultTheme(c.Theme()).Occurrence
	st.TextBackgrounds = occurrences(text, base, o.Word, o.From, col)
	o.Done, o.Dark, o.Partial, o.First, o.Last = true, dark, big, first, last
}

// occurClear 清掉 occurMarks 标的底色。
func occurClear(st *editor.State, o *docs.Occur) {
	if o.Done {
		st.TextBackgrounds = nil
		o.Done = false
	}
}

// occurrences 找 text（在文件里从 base 起）里 word 的整词出现，跳过从 skip 起的那一处（光标下的）。
func occurrences(text string, base int, word string, skip int, col wui.Color) []editor.TextBackground {
	var out []editor.TextBackground
	for i := 0; len(out) < occurMax; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			break
		}
		s, e := i+j, i+j+len(word)
		i = e
		if s+base == skip {
			continue
		}
		if r, _ := utf8.DecodeLastRuneInString(text[:s]); s > 0 && isIdent(r) {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(text[e:]); e < len(text) && isIdent(r) {
			continue
		}
		out = append(out, editor.TextBackground{Start: base + s, End: base + e, Color: col})
	}
	return out
}

// wordAt 是 off 处（光标在词里、或紧挨着词的末尾）的标识符和它的起点；不在标识符上、或是数字开头的，word 为 ""。
// 只看光标前后各 256 字节，压成一行的大文件也不整行拷。
func wordAt(snap editor.Snapshot, off int) (word string, from int) {
	line := snap.LineOf(off)
	ls, le := snap.LineStart(line), snap.LineEnd(line)
	a, b := max(ls, off-256), min(le, off+256)
	s := snap.Slice(a, b)
	k := off - a
	if k < 0 || k > len(s) {
		return "", 0
	}
	i, j := k, k
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if !isIdent(r) {
			break
		}
		i -= size
	}
	for j < len(s) {
		r, size := utf8.DecodeRuneInString(s[j:])
		if !isIdent(r) {
			break
		}
		j += size
	}
	if i == j || (i == 0 && a > ls) || (j == len(s) && b < le) { // 词比 256 字节还长：不算
		return "", 0
	}
	if r, _ := utf8.DecodeRuneInString(s[i:]); unicode.IsDigit(r) {
		return "", 0
	}
	return s[i:j], a + i
}

// isIdent 是标识符里的字：字母、数字、下划线。
func isIdent(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
