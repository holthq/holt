package outline

import (
	"fmt"
	"strings"
	"testing"
)

// ends 把摊平的大纲写成 "名字 行-End" 一项一行，比较 End 用。
func ends(t *testing.T, name, text string) string {
	t.Helper()
	var b strings.Builder
	for _, f := range Build(Parse(name, text)).Flat {
		fmt.Fprintf(&b, "%s%s %d-%d\n", strings.Repeat("  ", f.Depth), f.Name, f.Line, f.End)
	}
	return b.String()
}

// 粘性滚动要知道每个符号到哪一行为止（不含）：Go 按语法树、数花括号的到配对的 }、Python 按缩进、Markdown 到下一个同级标题、
// 按行认的到下一个符号，最后一个到文件末尾。
func TestOutlineEnd(t *testing.T) {
	for _, c := range []struct{ name, text, want string }{
		{"a.go", "package a\n\ntype T struct {\n\tx int\n}\n\nfunc (t *T) M() {\n\tprintln()\n}\n\nfunc F() {}\n\nconst (\n\tA = 1\n\tB = 2\n)\n",
			"T 2-5\n  M 6-9\nF 10-11\nconst (…) 12-16\n  A 13-14\n  B 14-15\n"},
		{"a.java", "class A {\n  void f() {\n    if (x) {\n    }\n  }\n  abstract void g();\n}\nclass B {\n",
			"A 0-7\n  f 1-5\n  g 5-6\nB 7-9\n"},
		{"a.py", "class A:\n    def f(self):\n        \"\"\"\nx\n        \"\"\"\n        return 1\n\n    # 注释\n  # 浅的注释\n    def g(self):\n        pass\n\nx = 1\ndef h():\n    pass\n",
			"A 0-12\n  f 1-9\n  g 9-12\nh 13-16\n"},
		{"a.md", "# 一\n\n## 甲\n\n正文\n\n### 子\n\n## 乙\n\n# 二\n",
			"一 0-10\n  甲 2-8\n    子 6-8\n  乙 8-10\n二 10-12\n"},
		{"a.js", "function a() {\n}\n\nconst b = () => 1\nlet x\n",
			"a 0-3\nb 3-6\n"},
	} {
		if got := ends(t, c.name, c.text); got != c.want {
			t.Errorf("%s 的 End：\n%s应为：\n%s", c.name, got, c.want)
		}
	}
}

// 吸顶链：一行一行往下定，包着那一行的最外层符号；头在第一行之前的才吸，最多 max 行，折着的不吸。
func TestStickyChain(t *testing.T) {
	text := "class A {\n" + // 0
		"  void f() {\n" + // 1
		"    int x;\n" + // 2
		"    int y;\n" + // 3
		"    int z;\n" + // 4
		"  }\n" + // 5
		"  void g() {\n" + // 6
		"  }\n" + // 7
		"}\n" // 8
	tr := Build(Parse("a.java", text))
	// shown 是编辑器从上往下显示的行（折叠藏着的不在里面）
	names := func(shown []int, max int, folded map[int]bool) string {
		var out []string
		for _, i := range tr.Sticky(func(row int) int {
			if row < len(shown) {
				return shown[row]
			}
			return 1 << 30
		}, max, folded) {
			out = append(out, tr.Flat[i].Name)
		}
		return strings.Join(out, " › ")
	}
	from := func(first int) []int {
		var out []int
		for l := first; l < 9; l++ {
			out = append(out, l)
		}
		return out
	}
	for _, c := range []struct {
		shown  []int
		max    int
		folded map[int]bool
		want   string
	}{
		{from(0), 5, nil, ""},                                 // 第一行就是 class 头：不吸
		{from(1), 5, nil, "A › f"},                            // f 的头在第一行、被吸着的 A 盖住：第二行（2）在 f 里，f 也吸
		{from(2), 5, nil, "A › f"},                            // 在 f 里面：两行
		{from(4), 5, nil, "A › f"},                            // 第二行是 f 的 } 那一行（5）：f 到 6 为止，还吸着（推挤在界面上做）
		{from(5), 5, nil, "A"},                                // 第二行是 g 的头（6）：不吸 g
		{from(2), 1, nil, "A"},                                // max 1：只要最外层
		{[]int{1, 5, 6, 7, 8}, 5, nil, "A › f"},               // f 折起来只藏了 2–4：不当它折着的话会吸在自己的 } 上面
		{[]int{1, 5, 6, 7, 8}, 5, map[int]bool{1: true}, "A"}, // 当它折着：不吸
		{[]int{0, 8}, 5, map[int]bool{0: true}, ""},           // A 折着
		{nil, 5, nil, ""},                                     // 什么都没显示
	} {
		if got := names(c.shown, c.max, c.folded); got != c.want {
			t.Errorf("显示 %v max %d 折着 %v：吸 %q，应为 %q", c.shown, c.max, c.folded, got, c.want)
		}
	}

	// Go 的方法挂在类型下面，但不在类型的 {} 里：吸的是方法本身
	gt := Build(Parse("a.go", "package a\n\ntype T struct{}\n\nfunc (T) M() {\n\tprintln(1)\n\tprintln(2)\n}\n"))
	var got []string
	for _, i := range gt.Sticky(func(row int) int { return 5 + row }, 5, nil) {
		got = append(got, gt.Flat[i].Name)
	}
	if strings.Join(got, ",") != "M" {
		t.Errorf("Go 方法：%v", got)
	}
}
