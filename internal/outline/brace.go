package outline

import (
	"strings"
)

// braceLang 是按花括号嵌套的语言（C、C++、Java、Kotlin、Swift、C#）的写法：rules 按行认符号，
// 其余几项告诉 blank 哪些是注释和字符串。
type braceLang struct {
	rules    []rule
	triple   bool // """…""" 多行字符串（Java 文本块、Kotlin、Swift、C# 原始字符串）
	verbatim bool // C# 的 @"…"（"" 是一个引号，可以跨行）
	pre      bool // # 开头的是预处理行（C、C++、C#）
	ppIf     bool // 只有 #if / #elseif / #else / #endif 是（Swift：#Preview { 之类是代码）
}

// frameKind 是花括号里的那一层是什么：打开时入栈，关上时出栈。
type frameKind int

const (
	frameFunc   frameKind = iota // 函数体：里面的都不认
	frameType                    // 类、结构体、extension……：里面认成员，别的花括号块（属性、初始化块）不看
	frameNS                      // 命名空间：和文件那一层一样
	frameOpaque                  // 不认识的块，在类型或函数里：里面不看
	frameClear                   // 不认识的块，在文件或命名空间那一层（extern "C" {、宏后面的块）：照常认、父节点往外找
)

type frame struct {
	kind frameKind
	node int // 符号在 nodes 里的下标，块为 -1
	body int // 里面的层数：层数掉到它以下就出栈
}

// pending 是认出来、还没等到自己那个 { 的符号。
type pending struct {
	sym           Symbol
	parent, node  int // node < 0：还没放进大纲（rule.body：等 { 来了才算）
	depth, parens int
	line          int
	ns            bool // namespace：里面是 frameNS
}

// notNames 不会是符号的名字（控制语句、关键字被正则当成"类型 名字("的时候）。
var notNames = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "sizeof": true, "alignof": true,
	"decltype": true, "typeof": true, "defined": true, "else": true, "do": true, "try": true, "new": true, "delete": true,
	"throw": true, "case": true, "using": true, "typedef": true, "static_assert": true, "when": true, "guard": true,
	"repeat": true, "foreach": true, "lock": true, "fixed": true, "checked": true, "unchecked": true, "nameof": true,
	"synchronized": true, "super": true, "this": true, "var": true, "let": true, "val": true, "func": true, "fun": true,
	"operator": true, "class": true, "struct": true, "__attribute__": true, "__declspec": true, "alignas": true, "asm": true,
	"__asm__": true, "_Static_assert": true,
}

// cWords 是顶格的"名字("不会是的 C 关键字（void (*(f(void)))(int) 这种返回函数指针的写法）。
var cWords = map[string]bool{"void": true, "int": true, "char": true, "short": true, "long": true, "float": true, "double": true,
	"unsigned": true, "signed": true, "const": true, "static": true, "extern": true, "inline": true, "volatile": true, "bool": true,
	"auto": true, "struct": true, "union": true, "enum": true, "return": true}

// notTypes 是"类型"那一段的第一个词不会是的（return foo(、new Foo(、else if (……）。
var notTypes = map[string]bool{
	"return": true, "new": true, "throw": true, "else": true, "case": true, "goto": true, "delete": true, "await": true,
	"yield": true, "co_return": true, "co_await": true, "co_yield": true, "using": true, "typedef": true, "if": true,
	"while": true, "for": true, "switch": true, "do": true, "package": true, "import": true, "val": true, "var": true,
	"let": true, "sizeof": true, "friend": true, "operator": true,
}

// contWords 是接着上一行的签名写的行的开头：签名写完了、{ 还没来时，下一行是这些开头的才接着等，不然就不等了。
var contWords = []string{"{", ":", ",", ")", "->", "&&", "||", "throws", "rethrows", "where", "const", "noexcept",
	"override", "final", "requires", "async", "implements", "extends", "permits"}

// parse 是 bl 这种语言的大纲：数花括号的层数，认出来的符号挂在它所在的类 / 结构体 / 命名空间下面，
// 函数体里的都不要；Func 在类型里面算 Method。
func (bl *braceLang) parse(text string) []Symbol {
	var (
		nodes         []Symbol
		parents       []int
		stack         []frame
		pend          *pending
		lex           lexState
		pp            []ppFrame
		depth, parens int
		ppCont        bool // 上一行是以 \ 结尾的预处理行
		tail          byte // 上一个不空的代码行最后一个字
	)
	emit := func(s Symbol, parent int) int {
		nodes, parents = append(nodes, s), append(parents, parent)
		return len(nodes) - 1
	}
	// scope 是现在能不能认符号；能的话父节点是谁（-1 为顶层），它是不是类型
	scope := func() (ok bool, parent int, inType bool) {
		for i := len(stack) - 1; i >= 0; i-- {
			switch f := stack[i]; f.kind {
			case frameClear:
				continue
			case frameFunc, frameOpaque:
				return false, -1, false
			default:
				return true, f.node, f.kind == frameType
			}
		}
		return true, -1, false
	}
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		code := bl.blank(raw, &lex)
		trimmed := strings.TrimSpace(code)
		// 预处理行：不认、不数花括号；#if 只跟第一个分支（#if 0 跟 #else），免得两边各一个 { 把层数数乱
		if bl.pre && (ppCont || bl.isPP(trimmed)) {
			if !ppCont {
				pp = ppDirective(pp, trimmed)
			}
			ppCont = strings.HasSuffix(strings.TrimRight(raw, " \t\r"), "\\")
			continue
		}
		if ppSkipping(pp) || trimmed == "" {
			continue
		}
		// 上面那个符号的签名写完了还没等到 {：这一行不像是接着写的，就不等了
		if pend != nil && pend.line < i && parens == pend.parens && !strings.ContainsRune(",:(", rune(tail)) && !startsCont(trimmed) {
			pend = nil
		}
		if ok, parent, inType := scope(); ok {
			pname := ""
			if parent >= 0 {
				pname = nodes[parent].Name
			}
			if p, now := bl.match(code, inType, pname); p != nil {
				p.parent, p.node, p.depth, p.parens, p.line, p.sym.Line = parent, -1, depth, parens, i, i
				switch {
				case p.ns && strings.HasSuffix(trimmed, ";"):
					// C# 的文件级命名空间（namespace A;）：一直到文件末尾都在它里面
					stack = append(stack, frame{frameNS, emit(p.sym, parent), depth})
					p = nil
				case now:
					p.node = emit(p.sym, parent)
				}
				pend = p
			}
		}
		for j := 0; j < len(code); j++ {
			switch code[j] {
			case '(':
				parens++
			case ')':
				parens = max(parens-1, 0)
			case '{':
				if pend != nil && depth == pend.depth && parens == pend.parens {
					if pend.node < 0 {
						pend.node = emit(pend.sym, pend.parent)
					}
					k := frameFunc
					switch {
					case pend.ns:
						k = frameNS
					case pend.sym.Kind != Func && pend.sym.Kind != Method:
						k = frameType
					}
					stack = append(stack, frame{k, pend.node, depth + 1})
					pend = nil
				} else {
					k := frameOpaque
					if ok, _, inType := scope(); ok && !inType {
						k = frameClear
					}
					stack = append(stack, frame{k, -1, depth + 1})
				}
				depth++
			case '}':
				depth = max(depth-1, 0)
				for len(stack) > 0 && stack[len(stack)-1].body > depth {
					if n := stack[len(stack)-1].node; n >= 0 {
						nodes[n].End = i + 1 // 到配对的 } 那一行
					}
					stack = stack[:len(stack)-1]
				}
				if pend != nil && depth < pend.depth {
					pend = nil
				}
			case ';':
				if pend != nil && depth == pend.depth && parens == pend.parens {
					pend = nil
				}
			}
		}
		tail = trimmed[len(trimmed)-1]
	}
	if len(nodes) == 0 {
		return nil
	}
	for _, f := range stack { // 到文件末尾都没关上的（C# 的文件级命名空间也是）
		if f.node >= 0 {
			nodes[f.node].End = len(lines)
		}
	}
	for i := range nodes {
		if nodes[i].End == 0 { // 没有自己的 {}：原型、类里的声明、Kotlin 的 fun f() = …
			nodes[i].End = nodes[i].Line + 1
		}
	}
	kids := make([][]int, len(nodes))
	var roots []int
	for i, p := range parents {
		if p < 0 {
			roots = append(roots, i)
		} else {
			kids[p] = append(kids[p], i)
		}
	}
	var build func(ids []int) []Symbol
	build = func(ids []int) []Symbol {
		if len(ids) == 0 {
			return nil
		}
		out := make([]Symbol, 0, len(ids))
		for _, id := range ids {
			s := nodes[id]
			s.Children = build(kids[id])
			out = append(out, s)
		}
		return out
	}
	return build(roots)
}

// match 按顺序套 bl.rules，第一条认出来的算：返回那个符号（还没定位置），now 为马上放进大纲
// （不用等它的 {）。inType 是在类型里面，parent 是那个类型的名字（构造函数要和它同名）。
func (bl *braceLang) match(code string, inType bool, parent string) (p *pending, now bool) {
	for _, r := range bl.rules {
		m := r.re.FindStringSubmatch(code)
		if m == nil {
			continue
		}
		name := strings.Trim(m[r.name], "`")
		if name == "" {
			name = r.fallback
		}
		if name == "" || notNames[name] {
			continue
		}
		if r.typ > 0 {
			if f := strings.Fields(strings.NewReplacer("*", " ", "&", " ").Replace(m[r.typ])); len(f) > 0 && notTypes[f[0]] {
				continue
			}
		}
		if r.noMacro && (strings.ToUpper(name) == name || cWords[name]) {
			continue
		}
		if r.ctor && (!inType || strings.TrimPrefix(name, "~") != parent) {
			continue
		}
		s := Symbol{Name: name, Kind: r.kind}
		kw := ""
		if r.keyword > 0 {
			kw = strings.Join(strings.Fields(strings.TrimPrefix(m[r.keyword], "@")), " ")
			if f := strings.Fields(kw); len(f) > 0 {
				s.Kind = keywordKind(f[0])
			}
			if s.Kind != Func {
				s.Detail = kw
			}
		}
		if r.detail > 0 && m[r.detail] != "" {
			s.Detail, s.Kind = strings.TrimSuffix(m[r.detail], "::"), Method
		}
		if s.Kind == Func && inType {
			s.Kind = Method
		}
		return &pending{sym: s, ns: kw == "namespace"}, !r.body || r.decl && inType
	}
	return nil, false
}

// isPP 是 line（去掉了首尾空白）是不是预处理行。
func (bl *braceLang) isPP(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	if !bl.ppIf {
		return true
	}
	f := strings.Fields(line[1:])
	return len(f) > 0 && (f[0] == "if" || f[0] == "elseif" || f[0] == "else" || f[0] == "endif")
}

// startsCont 是 line（去掉了首尾空白）像不像接着上一行的签名写的。
func startsCont(line string) bool {
	for _, w := range contWords {
		if strings.HasPrefix(line, w) {
			if rest := line[len(w):]; rest == "" || !isWord(w[len(w)-1]) || !isWord(rest[0]) {
				return true
			}
		}
	}
	return false
}

func isWord(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// lexState 是跨行的注释 / 字符串。
type lexState struct {
	block bool // 在 /* */ 里
	str   byte // 跨行的字符串：'t' 是 """，'v' 是 C# 的 @"
}

// blank 把 line 里注释和字符串、字符的内容换成空格（长度不变，引号留着），st 带着跨行的状态。
// 单行的字符串没收尾就到行末为止。
func (bl *braceLang) blank(line string, st *lexState) string {
	b := []byte(line)
	clear := func(from, to int) {
		for k := from; k < to && k < len(b); k++ {
			b[k] = ' '
		}
	}
	for i := 0; i < len(b); {
		switch {
		case st.block:
			end := strings.Index(line[i:], "*/")
			if end < 0 {
				clear(i, len(b))
				return string(b)
			}
			clear(i, i+end+2)
			i, st.block = i+end+2, false
		case st.str == 't':
			end := strings.Index(line[i:], `"""`)
			if end < 0 {
				clear(i, len(b))
				return string(b)
			}
			clear(i, i+end)
			i, st.str = i+end+3, 0
		case st.str == 'v':
			j := i
			for j < len(b) && (b[j] != '"' || j+1 < len(b) && b[j+1] == '"') {
				if b[j] == '"' {
					j++
				}
				j++
			}
			clear(i, j)
			if j >= len(b) {
				return string(b)
			}
			i, st.str = j+1, 0
		default:
			c := b[i]
			next := byte(0)
			if i+1 < len(b) {
				next = b[i+1]
			}
			switch {
			case c == '/' && next == '/':
				clear(i, len(b))
				return string(b)
			case c == '/' && next == '*':
				clear(i, i+2)
				i, st.block = i+2, true
			case c == '"' && bl.triple && strings.HasPrefix(line[i:], `"""`):
				i, st.str = i+3, 't'
			case c == '@' && bl.verbatim && next == '"':
				i, st.str = i+2, 'v'
			case c == '\'' && i > 0 && '0' <= b[i-1] && b[i-1] <= '9':
				i++ // C++14 的数字分隔（1'000'000）
			case c == '"' || c == '\'':
				j := i + 1
				for j < len(b) && b[j] != c {
					if b[j] == '\\' {
						j++
					}
					j++
				}
				clear(i+1, j)
				i = j + 1
			default:
				i++
			}
		}
	}
	return string(b)
}

// ppFrame 是一层 #if：taken 为已经跟过一个分支，skip 为现在这个分支不看。
type ppFrame struct{ taken, skip, outer bool }

// ppDirective 按预处理行 line（# 开头）改 #if 的栈。
func ppDirective(pp []ppFrame, line string) []ppFrame {
	f := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#")))
	if len(f) == 0 {
		return pp
	}
	switch f[0] {
	case "if", "ifdef", "ifndef":
		zero := f[0] == "if" && len(f) > 1 && f[1] == "0"
		return append(pp, ppFrame{taken: !zero, skip: zero, outer: ppSkipping(pp)})
	case "elif", "else", "elseif":
		if n := len(pp); n > 0 {
			pp[n-1].skip = pp[n-1].taken
			pp[n-1].taken = true
		}
	case "endif":
		if n := len(pp); n > 0 {
			pp = pp[:n-1]
		}
	}
	return pp
}

// ppSkipping 是现在在不看的 #if 分支里。
func ppSkipping(pp []ppFrame) bool {
	n := len(pp)
	return n > 0 && (pp[n-1].skip || pp[n-1].outer)
}
