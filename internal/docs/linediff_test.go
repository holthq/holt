package docs

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
)

// 外部格式化套上的改动：套到 old 上得到 new，没动的行不在改动里，改了的行收窄到不同的那几个字（不切开 UTF-8 字符）。
func TestLineEdits(t *testing.T) {
	apply := func(old string, edits []editor.Edit) string {
		b := editor.NewBuffer(old)
		b.Edit(edits...)
		return b.String()
	}
	for _, c := range []struct {
		old, new string
		n        int // 几处改动
	}{
		{"a\nb\nc\n", "a\nb\nc\n", 0},
		{"a\nb\nc\n", "a\nB\nc\n", 1},
		{"a\nb\nc", "a\nb\nc\n", 1},
		{"x\na\nb\nc\ny\n", "a\nb\nz\nc\n", 3},
		{"a\n b\n c\nd\n", "a\n\tb\n\tc\nd\n", 2}, // 几行换几行：一行一处
		{"", "a\n", 1},
		{"a\n", "", 1},
		{"func f() {\n\treturn 1\n}\n\nfunc g()  {\n}\n", "func f() {\n\treturn 1\n}\n\nfunc g() {\n}\n", 1},
	} {
		edits := LineEdits(c.old, c.new)
		if got := apply(c.old, edits); got != c.new || len(edits) != c.n {
			t.Errorf("%q → %q：套上是 %q，%d 处 %+v", c.old, c.new, got, len(edits), edits)
		}
	}
	for _, c := range []struct {
		old, new string
		want     editor.Edit
	}{
		{"x\n\tprintln(\"你好\")\n", "x\n\tPRINTLN(\"你好\")\n", editor.Edit{Start: 3, End: 10, Text: "PRINTLN"}},
		{"a\n b\n", "a\n\tb\n", editor.Edit{Start: 2, End: 3, Text: "\t"}},
		{"var x  = 1\n", "var x = 1\n", editor.Edit{Start: 6, End: 7}},
		{"好\n", "妈\n", editor.Edit{Start: 0, End: 3, Text: "妈"}}, // 头一个字节一样：不切开
		{"é\n", "©\n", editor.Edit{Start: 0, End: 2, Text: "©"}}, // 末一个字节一样：不切开
	} {
		if got := LineEdits(c.old, c.new); len(got) != 1 || got[0] != c.want {
			t.Errorf("%q → %q：%+v，要 %+v", c.old, c.new, got, c.want)
		}
	}
	r := rand.New(rand.NewSource(1))
	words := []string{"a\n", "b\n", "c\n", "d\n", "{\n", "}\n", "好\n", "妈\n", "é\n", "©\n", "a好\n", "好a\n"}
	gen := func(n int) string {
		var sb strings.Builder
		for range n {
			sb.WriteString(words[r.Intn(len(words))])
		}
		return sb.String()
	}
	for range 300 {
		old, new := gen(r.Intn(40)), gen(r.Intn(40))
		edits := LineEdits(old, new)
		if got := apply(old, edits); got != new {
			t.Fatalf("%q → %q：套上是 %q", old, new, got)
		}
		for _, e := range edits {
			if e.Start < len(old) && !utf8.RuneStart(old[e.Start]) || e.End < len(old) && !utf8.RuneStart(old[e.End]) || !utf8.ValidString(e.Text) {
				t.Fatalf("%q → %q：切开了字符 %+v", old, new, e)
			}
		}
	}
	// 差得太多：中间整段换，照样对
	var big, other strings.Builder
	for i := range 3000 {
		big.WriteString(words[i%6])
		other.WriteString(words[(i*7+3)%5])
	}
	if got := apply(big.String(), LineEdits(big.String(), other.String())); got != other.String() {
		t.Fatal("差太多时整段换错了")
	}
}
