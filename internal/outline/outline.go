// Package outline 从文件的文字里列出大纲：Markdown 的标题、Go 的声明（go/parser），Python、JS / TS、Rust、
// Shell、YAML 按行用正则认，C、C++、Java、Kotlin、Swift、C# 按行认、数花括号嵌套（brace.go）。
// 纯函数，不碰界面；侧栏的大纲模式和命令面板的 @ 用它。
package outline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"

	mdparser "github.com/egoist/mygo/ui/markdown/parser"

	"github.com/lemoeade/grove/internal/i18n"
)

// MaxBytes 是肯解析的最大文本：再大的没有大纲。
const MaxBytes = 1 << 20

// MaxSymbols 是一个文件最多列的符号（嵌套的也算）；多的截掉，末尾一项说还有几个。
const MaxSymbols = 2000

// Kind 是符号的种类。
type Kind int

const (
	Heading Kind = iota // Markdown 标题
	Func
	Method
	Type
	Const
	Var
	Class
	Section // 一组（Go 的 const ( … )、Rust 的 impl / mod）或"…还有 N 个"
)

// Symbol 是大纲里的一项：Line 从 0 起，Detail 是名字后面灰着写的（Go 方法的接收者、Rust 的关键字）。
// End 是它到哪一行为止（不含）：Go 按语法树、数花括号的语言到配对的 } 那行、Python 按缩进、Markdown 到下一个同级或更高级的标题、
// 别的按行认的到下一个符号；最后一个到文件末尾。粘性滚动（Sticky）用它。
type Symbol struct {
	Name, Detail string
	Kind         Kind
	Line, End    int
	Children     []Symbol
}

// lang 是文件名对应的大纲解析器，没有大纲的语言为 nil。
func lang(name string) func(string) []Symbol {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return parseMarkdown
	case ".go":
		return parseGo
	case ".py", ".pyw":
		return parsePython
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return byLine(jsRules)
	case ".rs":
		return byLine(rustRules)
	case ".sh", ".bash", ".zsh":
		return byLine(shellRules)
	case ".yaml", ".yml":
		return byLine(yamlRules)
	case ".c":
		return cBrace.parse
	case ".h", ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".ino":
		return cppBrace.parse // .h 也可能是 C++：C 的写法 C++ 的规则都认
	case ".java":
		return javaBrace.parse
	case ".kt", ".kts":
		return kotlinBrace.parse
	case ".swift":
		return swiftBrace.parse
	case ".cs", ".csx":
		return csBrace.parse
	}
	return nil
}

// exts 是有大纲的语言（editor.Language 的 Name）认大纲用的扩展名：状态栏手动换了语言时按它挑解析器。
var exts = map[string]string{"Markdown": ".md", "Go": ".go", "Python": ".py", "JavaScript": ".js", "TypeScript": ".ts",
	"Rust": ".rs", "Shell": ".sh", "YAML": ".yaml", "C": ".c", "C++": ".cpp", "Java": ".java", "Kotlin": ".kt",
	"Swift": ".swift", "C#": ".cs"}

// Ext 是 language 这种语言认大纲用的扩展名；没有大纲的为 ""。
func Ext(language string) string { return exts[language] }

// Supported 是 name 这种文件有没有大纲。
func Supported(name string) bool { return lang(name) != nil }

// Parse 是 name 这个文件（文字 text）的大纲：没有大纲的语言、超过 MaxBytes 的文本为 nil。
func Parse(name, text string) []Symbol {
	p := lang(name)
	if p == nil || len(text) > MaxBytes {
		return nil
	}
	return capSymbols(p(text))
}

// capSymbols 把多于 MaxSymbols 的截掉（按出现的顺序数，嵌套的也算），末尾加一项"…还有 N 个"。
func capSymbols(syms []Symbol) []Symbol {
	total := count(syms)
	if total <= MaxSymbols {
		return syms
	}
	left := MaxSymbols
	syms = cut(syms, &left)
	return append(syms, Symbol{Name: i18n.Tf("…还有 %d 个", total-MaxSymbols), Kind: Section, Line: -1})
}

func count(syms []Symbol) int {
	n := len(syms)
	for _, s := range syms {
		n += count(s.Children)
	}
	return n
}

func cut(syms []Symbol, left *int) []Symbol {
	for i := range syms {
		if *left == 0 {
			return syms[:i]
		}
		*left--
		syms[i].Children = cut(syms[i].Children, left)
	}
	return syms
}

// Flat 是摊平的大纲里的一项（先根后子）：Path 是带上祖先的名字（"父 › 子"，命令面板的 @ 用），
// Key 认出同一个符号（Path 加上同名的第几个），侧栏的展开和选中跟着它；Depth 从 0 起，Parent 是父项的下标（顶层 -1）。
type Flat struct {
	Symbol
	Path, Key string
	Depth     int
	Parent    int
}

// Tree 是一个文件的大纲：Syms 是原样的树，Flat 摊平（"…还有 N 个"那项不要），Roots / Kids 按 Key 给侧栏的 Outline。
type Tree struct {
	Syms  []Symbol
	Flat  []Flat
	Roots []string
	Kids  map[string][]string
	ByKey map[string]int // Key → 在 Flat 里的下标
	More  string         // 截掉了的说明（"…还有 N 个"），没截为空
}

// Build 把 syms 摊平成 Tree。
func Build(syms []Symbol) *Tree {
	t := &Tree{Syms: syms, Kids: map[string][]string{}, ByKey: map[string]int{}}
	seen := map[string]int{}
	var walk func(syms []Symbol, prefix string, depth, parent int) []string
	walk = func(syms []Symbol, prefix string, depth, parent int) []string {
		var keys []string
		for _, s := range syms {
			if s.Line < 0 {
				t.More = s.Name
				continue
			}
			p := s.Name
			if prefix != "" {
				p = prefix + " › " + s.Name
			}
			key := p
			if n := seen[p]; n > 0 {
				key = p + "\x00" + strconv.Itoa(n)
			}
			seen[p]++
			i := len(t.Flat)
			t.Flat = append(t.Flat, Flat{Symbol: s, Path: p, Key: key, Depth: depth, Parent: parent})
			t.Flat[i].Children = nil
			t.ByKey[key] = i
			keys = append(keys, key)
			if kids := walk(s.Children, p, depth+1, i); len(kids) > 0 {
				t.Kids[key] = kids
			}
		}
		return keys
	}
	t.Roots = walk(syms, "", 0, -1)
	return t
}

// At 是第 line 行所在的最里层的符号在 Flat 里的下标：行在 line 之前或就是 line 的符号里最靠后的一个
// （一样靠后的取更深的）；line 在所有符号之前为 -1。
func (t *Tree) At(line int) int {
	best := -1
	for i, f := range t.Flat {
		if f.Line <= line && (best < 0 || f.Line > t.Flat[best].Line || f.Line == t.Flat[best].Line && f.Depth > t.Flat[best].Depth) {
			best = i
		}
	}
	return best
}

// parseMarkdown 是 Markdown 的标题，按级别嵌套（跳级也嵌在上一个更高级的下面）。
func parseMarkdown(text string) []Symbol {
	doc := mdparser.Parse(text)
	type open struct {
		level int
		sym   *Symbol
	}
	var roots []Symbol
	var stack []open
	line, at := 0, 0
	for i := 1; i < doc.Len(); i++ {
		b := doc.Block(i)
		if b.Kind != mdparser.Heading {
			continue
		}
		line += strings.Count(text[at:b.Start], "\n")
		at = b.Start
		s := Symbol{Name: strings.TrimSpace(mdparser.PlainText(doc.Inlines(i))), Kind: Heading, Line: line}
		if s.Name == "" {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].level >= b.Level {
			stack[len(stack)-1].sym.End = line
			stack = stack[:len(stack)-1]
		}
		var sym *Symbol
		if len(stack) == 0 {
			roots = append(roots, s)
			sym = &roots[len(roots)-1]
		} else {
			p := stack[len(stack)-1].sym
			p.Children = append(p.Children, s)
			sym = &p.Children[len(p.Children)-1]
		}
		stack = append(stack, open{b.Level, sym})
	}
	for _, o := range stack {
		o.sym.End = lineCount(text)
	}
	return roots
}

// lineCount 是 text 有几行（和编辑器数的一样：末尾的换行后面还有一个空行）。
func lineCount(text string) int { return strings.Count(text, "\n") + 1 }

// parseGo 是 Go 文件的顶层声明：函数、类型（这个文件里的方法挂在它下面，别处的类型的方法平铺成 "(T) M"）、
// const / var（一组一项，名字做子项）。有语法错误的文件用 go/parser 给的那部分。
func parseGo(text string) []Symbol {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "", text, parser.SkipObjectResolution)
	if f == nil {
		return nil
	}
	lineOf := func(p token.Pos) int { return fset.Position(p).Line - 1 }
	endOf := func(n ast.Node) int { return lineOf(n.End()) + 1 } // End 是最后一个字后面：还在最后那一行
	types := map[string]bool{}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
			for _, sp := range g.Specs {
				if ts, ok := sp.(*ast.TypeSpec); ok && ts.Name != nil {
					types[ts.Name.Name] = true
				}
			}
		}
	}
	var out []Symbol
	at := map[string]int{} // 类型名 → 在 out 里的下标
	methods := map[string][]Symbol{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name == nil {
				continue
			}
			if d.Recv == nil || len(d.Recv.List) == 0 {
				out = append(out, Symbol{Name: d.Name.Name, Kind: Func, Line: lineOf(d.Pos()), End: endOf(d)})
				continue
			}
			recv, ptr := recvType(d.Recv.List[0].Type)
			detail := recv
			if ptr {
				detail = "*" + recv
			}
			m := Symbol{Name: d.Name.Name, Detail: detail, Kind: Method, Line: lineOf(d.Pos()), End: endOf(d)}
			if types[recv] {
				methods[recv] = append(methods[recv], m)
			} else {
				m.Name, m.Detail = "("+detail+") "+m.Name, ""
				out = append(out, m)
			}
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, sp := range d.Specs {
					if ts, ok := sp.(*ast.TypeSpec); ok && ts.Name != nil {
						at[ts.Name.Name] = len(out)
						out = append(out, Symbol{Name: ts.Name.Name, Kind: Type, Line: lineOf(ts.Pos()), End: endOf(ts)})
					}
				}
			case token.CONST, token.VAR:
				kind := Const
				if d.Tok == token.VAR {
					kind = Var
				}
				var names []Symbol
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							if n.Name != "_" {
								names = append(names, Symbol{Name: n.Name, Kind: kind, Line: lineOf(n.Pos()), End: endOf(vs)})
							}
						}
					}
				}
				if d.Lparen.IsValid() {
					out = append(out, Symbol{Name: d.Tok.String() + " (…)", Kind: Section, Line: lineOf(d.Pos()), End: endOf(d), Children: names})
				} else {
					out = append(out, names...)
				}
			}
		}
	}
	for name, ms := range methods {
		if i, ok := at[name]; ok {
			out[i].Children = append(out[i].Children, ms...)
		}
	}
	return out
}

// recvType 是方法接收者的类型名（去掉 * 和类型参数），ptr 是不是指针接收者。
func recvType(e ast.Expr) (name string, ptr bool) {
	if s, ok := e.(*ast.StarExpr); ok {
		e, ptr = s.X, true
	}
	switch t := e.(type) {
	case *ast.IndexExpr:
		e = t.X
	case *ast.IndexListExpr:
		e = t.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name, ptr
	}
	return "?", ptr
}

var pyDef = regexp.MustCompile(`^(\s*)(?:async\s+)?(def|class)\s+(\w+)`)

// parsePython 是 def / class，按缩进嵌套（类里的 def 是方法）；一个到缩进不比它深的下一行（空行、只有注释的行不算）为止。
func parsePython(text string) []Symbol {
	type open struct {
		indent int
		sym    *Symbol
	}
	var roots []Symbol
	var stack []open
	lines := strings.Split(text, "\n")
	quote := "" // 在没收尾的三引号字符串里：那几行的缩进不算
	for i, line := range lines {
		code := strings.TrimSpace(line)
		inStr := quote != ""
		for _, q := range []string{`"""`, "'''"} {
			if quote == "" || quote == q {
				if strings.Count(line, q)%2 == 1 {
					if quote == "" {
						quote = q
					} else {
						quote = ""
					}
				}
			}
		}
		if inStr || code == "" || code[0] == '#' {
			continue
		}
		indent := len(strings.ReplaceAll(line[:len(line)-len(strings.TrimLeft(line, " \t"))], "\t", "    "))
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack[len(stack)-1].sym.End = i
			stack = stack[:len(stack)-1]
		}
		m := pyDef.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		s := Symbol{Name: m[3], Kind: Func, Line: i}
		if m[2] == "class" {
			s.Kind = Class
		}
		var sym *Symbol
		if len(stack) == 0 {
			roots = append(roots, s)
			sym = &roots[len(roots)-1]
		} else {
			p := stack[len(stack)-1].sym
			if s.Kind == Func && p.Kind == Class {
				s.Kind = Method
			}
			p.Children = append(p.Children, s)
			sym = &p.Children[len(p.Children)-1]
		}
		stack = append(stack, open{indent, sym})
	}
	for _, o := range stack {
		o.sym.End = len(lines)
	}
	return roots
}

// rule 是按行认一种符号的正则：name 是名字在第几组，kind 是种类；keyword > 0 时那一组（关键字）定种类、写进 Detail。
// 下面几项只有数花括号的语言（braceLang）用。
type rule struct {
	re      *regexp.Regexp
	name    int
	kind    Kind
	keyword int

	body     bool   // 等到它自己的 { 才算（C / C++ 的函数和类：分号结尾的是原型、前置声明、调用）
	decl     bool   // 在类型里面不用等 {（类里的 void f(); 也列）
	ctor     bool   // 名字（去掉 ~）要和所在的类型同名：构造 / 析构函数
	noMacro  bool   // 全大写的名字不算（顶格的 FOO(…) 多半是宏）
	fallback string // 名字那一组是空的时候用它（Kotlin 的 companion object）
	typ      int    // "类型"那一段在第几组：第一个词在 notTypes 里的不算（return f(、new Foo(）
	detail   int    // 这一组（去掉末尾的 ::）写进 Detail，种类算 Method（C++ 的 Foo::bar）
}

var jsRules = []rule{
	{re: regexp.MustCompile(`^\s*(export\s+)?(default\s+)?(async\s+)?(function\*?|class)\s+(\w+)`), name: 5, kind: Func, keyword: 4},
	{re: regexp.MustCompile(`^\s*(export\s+)?(const|let|var)\s+(\w+)\s*=\s*(async\s*)?\(.*\)\s*=>`), name: 3, kind: Func},
}

var rustRules = []rule{
	{re: regexp.MustCompile(`^\s*(pub(\([\w:]+\))?\s+)?((async|const|unsafe)\s+)*(fn|struct|enum|trait|impl|mod)\s+(\w+)`), name: 6, kind: Func, keyword: 5},
}

var shellRules = []rule{
	{re: regexp.MustCompile(`^(\w[\w-]*)\s*\(\)`), name: 1, kind: Func},
	{re: regexp.MustCompile(`^function\s+(\w[\w-]*)`), name: 1, kind: Func},
}

var yamlRules = []rule{
	{re: regexp.MustCompile(`^([\w.-]+):`), name: 1, kind: Var},
}

// byLine 是按行套 rules 的平铺大纲（每行第一个认出来的那条算）。
func byLine(rules []rule) func(string) []Symbol {
	return func(text string) []Symbol {
		var out []Symbol
		for i, line := range strings.Split(text, "\n") {
			for _, r := range rules {
				m := r.re.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				s := Symbol{Name: m[r.name], Kind: r.kind, Line: i}
				if r.keyword > 0 {
					kw := strings.TrimSuffix(m[r.keyword], "*")
					s.Kind = keywordKind(kw)
					if s.Kind != Func {
						s.Detail = kw
					}
				}
				if n := len(out); n > 0 {
					out[n-1].End = i
				}
				out = append(out, s)
				break
			}
		}
		if n := len(out); n > 0 {
			out[n-1].End = lineCount(text)
		}
		return out
	}
}

// keywordKind 是关键字对应的种类。
func keywordKind(kw string) Kind {
	switch kw {
	case "class", "interface", "record", "object", "companion", "protocol", "actor":
		return Class
	case "struct", "enum", "trait", "union":
		return Type
	case "impl", "mod", "namespace", "extension":
		return Section
	}
	return Func // fn、function、fun、func
}

// C / C++ 的"类型… 名字("：类型是一个或几个词（可以带 :: 和 <…>，空格、* 和 & 隔开），名字可以是 ~析构、operator…
const (
	cAttr = `^\s*(?:\[\[[^\]]*\]\]\s*)*`
	cType = `((?:[A-Za-z_][\w:]*(?:<[^;{}()=]*>)?[\s\*&]+)+)`
	cName = `(~?\w+|operator\s*(?:\(\)|[^\s(]+))\s*\(`
)

var (
	cFunc  = rule{re: regexp.MustCompile(cAttr + cType + cName), name: 2, kind: Func, typ: 1, body: true, decl: true}
	krFunc = rule{re: regexp.MustCompile(`^([A-Za-z_]\w*)\s*\(`), name: 1, kind: Func, body: true, noMacro: true} // 返回类型在上一行

	cBrace = &braceLang{pre: true, rules: []rule{
		{re: regexp.MustCompile(`^\s*(?:typedef\s+)?(struct|union|enum)\s+(?:[A-Za-z_]\w*\s+)*?([A-Za-z_]\w*)\s*(?:\{.*)?$`), name: 2, kind: Type, keyword: 1, body: true},
		cFunc, krFunc,
	}}
	cppBrace = &braceLang{pre: true, rules: []rule{
		{re: regexp.MustCompile(`^\s*(?:inline\s+)?(namespace)\s+([\w:]+)\s*(?:\{.*)?$`), name: 2, kind: Section, keyword: 1, body: true},
		{re: regexp.MustCompile(`^\s*(?:template\s*<[^;{}]*>\s*)?(?:typedef\s+)?(class|struct|union|enum(?:\s+class|\s+struct)?)\s+(?:[A-Za-z_]\w*\s+)*?([A-Za-z_]\w*)\s*(?:final\s*)?(?::[^;{]*)?(?:\{.*)?$`), name: 2, kind: Class, keyword: 1, body: true},
		{re: regexp.MustCompile(cAttr + `((?:[A-Za-z_][\w:]*(?:<[^;{}()=]*>)?[\s\*&]+)*)((?:\w+(?:<[^;{}()=]*>)?::)+)` + cName), name: 3, kind: Func, typ: 1, detail: 2, body: true},
		{re: regexp.MustCompile(`^\s*(?:(?:explicit|inline|constexpr|consteval|virtual)\s+)*(~?\w+)\s*\(`), name: 1, kind: Func, ctor: true, body: true, decl: true},
		cFunc, krFunc,
	}}
	javaBrace = &braceLang{triple: true, rules: []rule{
		{re: regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:(?:public|protected|private|static|final|abstract|sealed|non-sealed|strictfp)\s+)*(class|interface|enum|record|@interface)\s+(\w+)`), name: 2, kind: Class, keyword: 1},
		{re: regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:(?:public|protected|private)\s+)*(\w+)\s*\(`), name: 1, kind: Func, ctor: true},
		{re: regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:(?:public|protected|private|static|final|abstract|synchronized|native|default|strictfp)\s+)*(?:<[\w\s,.?<>\[\]&]*>\s+)?([\w.$]+(?:<[\w\s,.?<>\[\]&]*>)?(?:\[\])*)\s+(\w+)\s*\(`), name: 2, kind: Func, typ: 1},
	}}
	kotlinBrace = &braceLang{triple: true, rules: []rule{
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?(companion\s+object)\b(?:\s+(\w+))?`), name: 2, kind: Class, keyword: 1, fallback: "Companion"},
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?(class|interface|object)\s+(\w+)`), name: 2, kind: Class, keyword: 1},
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?fun\s+(?:<[^>]*>\s*)?(?:[\w.<>?, *]+\.)?(\w+|` + "`[^`]+`" + `)\s*\(`), name: 1, kind: Func},
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?(constructor)\s*\(`), name: 1, kind: Func},
	}}
	swiftBrace = &braceLang{triple: true, pre: true, ppIf: true, rules: []rule{
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?func\s+([^\s(<]+)`), name: 1, kind: Func},
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?(init|deinit)\b`), name: 1, kind: Func},
		{re: regexp.MustCompile(`^\s*(?:[@\w]+(?:\([^)]*\))?\s+)*?(class|struct|enum|protocol|extension|actor)\s+([\w.]+)`), name: 2, kind: Class, keyword: 1},
	}}
	csBrace = &braceLang{triple: true, verbatim: true, pre: true, rules: []rule{
		{re: regexp.MustCompile(`^\s*(namespace)\s+([\w.]+)\s*(?:[{;].*)?$`), name: 2, kind: Section, keyword: 1},
		{re: regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|sealed|abstract|partial|readonly|unsafe|new|file|ref)\s+)*(class|struct|interface|enum|record(?:\s+(?:class|struct))?)\s+(\w+)`), name: 2, kind: Class, keyword: 1},
		{re: regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|extern|unsafe)\s+)*(~?\w+)\s*\(`), name: 1, kind: Func, ctor: true},
		{re: regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|virtual|override|abstract|sealed|async|extern|unsafe|new|partial|readonly|ref)\s+)*([\w.]+(?:<[\w\s,.?<>\[\]&]*>)?(?:\[,*\])*\??)\s+([\w.]*\w)\s*(?:<[\w\s,.?<>\[\]&]*>)?\s*\(`), name: 2, kind: Func, typ: 1},
	}}
)
