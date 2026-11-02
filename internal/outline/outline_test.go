package outline

import (
	"fmt"
	"strings"
	"testing"
)

// show 把大纲写成一行一项："  名字 [Detail] @行"，缩进两格一层，比较起来好读。
func show(syms []Symbol) string {
	var b strings.Builder
	var walk func(syms []Symbol, depth int)
	walk = func(syms []Symbol, depth int) {
		for _, s := range syms {
			b.WriteString(strings.Repeat("  ", depth) + s.Name)
			if s.Detail != "" {
				b.WriteString(" [" + s.Detail + "]")
			}
			fmt.Fprintf(&b, " @%d\n", s.Line)
			walk(s.Children, depth+1)
		}
	}
	walk(syms, 0)
	return b.String()
}

func check(t *testing.T, name, text, want string) []Symbol {
	t.Helper()
	syms := Parse(name, text)
	if got := show(syms); got != want {
		t.Errorf("%s 的大纲：\n%s\n应为：\n%s", name, got, want)
	}
	return syms
}

func TestParseMarkdown(t *testing.T) {
	text := "# 标题\n\n正文\n\n## 第一节\n\n### 小节\n\n## 第二节\n\n#### 跳级\n\n```\n# 不是标题\n```\n\n# 另一个 *强调*\n"
	check(t, "a.md", text, `标题 @0
  第一节 @4
    小节 @6
  第二节 @8
    跳级 @10
另一个 强调 @16
`)
	if Parse("a.txt", text) != nil {
		t.Error("txt 不该有大纲")
	}
	if !Supported("README.MD") || Supported("x.m") {
		t.Error("Supported 不对")
	}
}

func TestParseGo(t *testing.T) {
	text := `package p

import "fmt"

const One = 1

const (
	A = iota
	B
	_
)

var x, y int

type T struct{}

func (t *T) M() {}

func (T) N() {}

func (o Other) Elsewhere() {}

func (g G[K]) Gen() {}

type G[K any] struct{}

func F() { fmt.Println() }
`
	check(t, "p.go", text, `One @4
const (…) @6
  A @7
  B @8
x @12
y @12
T @14
  M [*T] @16
  N [T] @18
(Other) Elsewhere @20
G @24
  Gen [G] @22
F @26
`)
	// 有语法错误：go/parser 给的那部分照样列
	broken := "package p\n\nfunc Good() {}\n\nfunc Bad( {\n\nfunc After() {}\n"
	syms := Parse("b.go", broken)
	if len(syms) == 0 || syms[0].Name != "Good" {
		t.Errorf("有错的文件：%s", show(syms))
	}
}

func TestParsePython(t *testing.T) {
	text := "import os\n\nclass A:\n    def m(self):\n        def inner():\n            pass\n    async def n(self):\n        pass\n\ndef top():\n    pass\n\n\tdef tabbed(): pass\n"
	check(t, "a.py", text, `A @2
  m @3
    inner @4
  n @6
top @9
  tabbed @12
`)
	syms := Parse("a.py", text)
	if syms[0].Kind != Class || syms[0].Children[0].Kind != Method || syms[0].Children[0].Children[0].Kind != Func || syms[1].Kind != Func {
		t.Errorf("种类不对：%+v", syms)
	}
}

func TestParseJS(t *testing.T) {
	text := "import x from 'y'\nexport default function main() {}\nexport class Foo {}\nfunction* gen() {}\nconst add = (a, b) => a + b\nexport const load = async (url) => {}\nlet n = 3\nasync function go() {}\n"
	syms := check(t, "a.ts", text, `main @1
Foo [class] @2
gen @3
add @4
load @5
go @7
`)
	if syms[1].Kind != Class || syms[0].Kind != Func {
		t.Errorf("种类不对：%+v", syms)
	}
	if Parse("a.jsx", text) == nil || Parse("a.mjs", text) == nil {
		t.Error("jsx / mjs 也该有大纲")
	}
}

func TestParseRust(t *testing.T) {
	text := "use std::io;\n\npub struct S;\nenum E { A }\npub(crate) trait Tr {}\nimpl S {\n    pub async fn run() {}\n}\nmod m {}\nconst unsafe fn f() {}\n"
	syms := check(t, "a.rs", text, `S [struct] @2
E [enum] @3
Tr [trait] @4
S [impl] @5
run @6
m [mod] @8
f @9
`)
	if syms[0].Kind != Type || syms[3].Kind != Section || syms[4].Kind != Func {
		t.Errorf("种类不对：%+v", syms)
	}
}

func TestParseYAML(t *testing.T) {
	check(t, "a.yml", "# 注释\nname: ci\non:\n  push: {}\njobs:\n  build:\n    runs-on: x\n", "name @1\non @2\njobs @4\n")
	check(t, "a.sh", "#!/bin/sh\nhello() {\n  echo hi\n}\nfunction bye {\n}\nmy-fn () { :; }\n", "hello @1\nbye @4\nmy-fn @6\n")
}

func TestOutlineCap(t *testing.T) {
	var b strings.Builder
	for i := range MaxSymbols + 5 {
		fmt.Fprintf(&b, "# h%d\n", i)
	}
	syms := Parse("big.md", b.String())
	if len(syms) != MaxSymbols+1 || syms[MaxSymbols].Line != -1 || !strings.Contains(syms[MaxSymbols].Name, "5") {
		t.Fatalf("截断不对：%d 项，最后一项 %+v", len(syms), syms[len(syms)-1])
	}
	tree := Build(syms)
	if len(tree.Flat) != MaxSymbols || tree.More == "" {
		t.Errorf("Build：%d 项、More %q", len(tree.Flat), tree.More)
	}
	// 嵌套的也算数：截在子项中间
	nested := []Symbol{{Name: "a", Line: 0, Children: make([]Symbol, MaxSymbols)}}
	got := capSymbols(nested)
	if count(got) != MaxSymbols+1 || len(got[0].Children) != MaxSymbols-1 {
		t.Errorf("嵌套截断：%d 项、子项 %d", count(got), len(got[0].Children))
	}
	// 太大的文本没有大纲
	if Parse("x.md", "# a\n"+strings.Repeat("x", MaxBytes)) != nil {
		t.Error("超过 MaxBytes 还有大纲")
	}
}

func TestBuildAndAt(t *testing.T) {
	tree := Build(Parse("a.md", "# A\n## x\n# B\n## x\ntext\n# A\n"))
	var keys []string
	for _, f := range tree.Flat {
		keys = append(keys, f.Path+"|"+fmt.Sprint(f.Depth, f.Parent))
	}
	if got := strings.Join(keys, ","); got != "A|0 -1,A › x|1 0,B|0 -1,B › x|1 2,A|0 -1" {
		t.Errorf("Flat：%s", got)
	}
	// 同名的第二个有自己的键，展开状态不串
	if tree.Flat[0].Key == tree.Flat[4].Key || len(tree.Roots) != 3 || len(tree.Kids[tree.Flat[0].Key]) != 1 {
		t.Errorf("键：%q %q，Roots %v", tree.Flat[0].Key, tree.Flat[4].Key, tree.Roots)
	}
	for line, want := range map[int]int{0: 0, 1: 1, 2: 2, 4: 3, 5: 4, 99: 4} {
		if got := tree.At(line); got != want {
			t.Errorf("At(%d) = %d，应为 %d", line, got, want)
		}
	}
	if Build(Parse("a.md", "text\n# A\n")).At(0) != -1 {
		t.Error("第一个符号之前应为 -1")
	}
}
