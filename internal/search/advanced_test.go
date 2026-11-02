package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// 高级正则：RE2 编不了的反向引用 / 环视走 regexp2。

func TestAdvancedSyntax(t *testing.T) {
	for expr, want := range map[string]bool{
		`(\w+) \1`:   true,
		`foo(?=bar)`: true,
		`foo(?!bar)`: true,
		`(?<=a)b`:    true,
		`(?<!a)b`:    true,
		`(?>a+)b`:    true,
		`\k<x>`:      true,
		`\\1`:        false, // 转义的反斜杠后面跟个 1
		`a\d+`:       false,
		`(?i)foo`:    false,
		`\1`[:1]:     false,
	} {
		if got := advancedSyntax(expr); got != want {
			t.Errorf("advancedSyntax(%q) = %v", expr, got)
		}
	}
	// RE2 编得了的照旧走 RE2：哪怕里面有 (?<name>（Go 1.22 起认）
	for _, expr := range []string{`a+b`, `(?<x>a)b`, `(?P<x>a)`} {
		m, err := Compile(Query{Text: expr, Regexp: true})
		if err != nil || m.Advanced() || m.re == nil {
			t.Errorf("%q：err=%v advanced=%v", expr, err, m.Advanced())
		}
	}
	// 没用到高级写法的错照旧是 RE2 的错；用到了但写错的报 regexp2 的
	if _, err := Compile(Query{Text: `a(`, Regexp: true}); err == nil || !strings.HasPrefix(err.Error(), "正则写错了：") {
		t.Errorf("a(：%v", err)
	}
	if _, err := Compile(Query{Text: `(?<=a(b`, Regexp: true}); err == nil || !strings.HasPrefix(err.Error(), "正则写错了：") {
		t.Errorf("(?<=a(b：%v", err)
	}
}

func TestBackref(t *testing.T) {
	m, err := Compile(Query{Text: `\b(\w+) \1\b`, Regexp: true, CaseSensitive: true})
	if err != nil || !m.Advanced() {
		t.Fatalf("err=%v advanced=%v", err, m.Advanced())
	}
	data := "the the cat\n中文 so so 了\nab ab abc\nNo no\n"
	ms := m.Find([]byte(data))
	var got []string
	for _, mt := range ms {
		got = append(got, mt.Preview[mt.PreviewCol:mt.PreviewCol+mt.Len])
	}
	if want := []string{"the the", "so so", "ab ab"}; !slices.Equal(got, want) {
		t.Fatalf("匹配 %q，该 %q", got, want)
	}
	// 位置是字节的：第二行"中文 "是 7 个字节
	if ms[1].Line != 1 || ms[1].Col != len("中文 ") {
		t.Errorf("第二处在 %d:%d", ms[1].Line, ms[1].Col)
	}
	// 不区分大小写：No no 也算
	ci, _ := Compile(Query{Text: `\b(\w+) \1\b`, Regexp: true})
	if n := len(ci.Find([]byte(data))); n != 4 {
		t.Errorf("不区分大小写 %d 处", n)
	}
}

func TestLookaround(t *testing.T) {
	data := "price: $10, cost: $20, $30x\nfoobar foobaz\n"
	cases := []struct {
		expr string
		want []string // 行:列（从 0 数）
	}{
		{`(?<=\$)\d+`, []string{"0:8", "0:19", "0:24"}},
		{`(?<!\$)\b\d+`, nil},
		{`\$\d+(?!x)`, []string{"0:7", "0:18", "0:23"}}, // $30x 退一步成 $3
		{`foo(?=baz)`, []string{"1:7"}},
		{`foo(?!baz)`, []string{"1:0"}},
	}
	for _, c := range cases {
		m, err := Compile(Query{Text: c.expr, Regexp: true})
		if err != nil || !m.Advanced() {
			t.Fatalf("%s：err=%v", c.expr, err)
		}
		var got []string
		for _, mt := range m.Find([]byte(data)) {
			got = append(got, fmt.Sprintf("%d:%d", mt.Line, mt.Col))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s：%q，该 %q", c.expr, got, c.want)
		}
	}
}

func TestRegexp2Replace(t *testing.T) {
	cases := []struct {
		name, expr, data, repl, want string
		done                         int
	}{
		{"反向引用 + $1", `\b(\w+) \1\b`, "the the cat\nso so\n", "$1", "the cat\nso\n", 2},
		{"后顾：只换 $ 后面的数", `(?<=\$)\d+`, "a $10 b 10 $7\n", "N", "a $N b 10 $N\n", 2},
		{"前瞻 + 命名组", `(?<k>\w+)(?==)`, "a=1 bb=2\n", "${k}_", "a_=1 bb_=2\n", 2},
		{"$$ 和写坏的 $", `(\d)(?!\d)`, "x12 y3\n", "$$$1$", "x1$2$ y$3$\n", 2},
		{"CRLF 跟着文件", `(\w)\1`, "aa\r\nbb\r\n", "$1\n", "a\r\n\r\nb\r\n\r\n", 2},
		{"中文", `(测试)(?=一下)`, "测试一下，测试。\n", "[$1]", "[测试]一下，测试。\n", 1},
	}
	for _, c := range cases {
		out, done, stale := replaceIn(t, Query{Text: c.expr, Regexp: true, CaseSensitive: true}, c.data, c.repl)
		if out != c.want || done != c.done || stale != 0 {
			t.Errorf("%s：%q（%d 处、%d 处对不上），该 %q", c.name, out, done, stale, c.want)
		}
	}

	// 后顾看的上一行在搜完之后被改掉了：这一行的预览没变，可那一处不再匹配，算对不上
	m, _ := Compile(Query{Text: `(?<=\$\n)\d+`, Regexp: true})
	ms := m.Find([]byte("$\n10\n"))
	if len(ms) != 1 {
		t.Fatalf("%d 处", len(ms))
	}
	if _, done, stale := m.Replace([]byte("#\n10\n"), ms, "N"); done != 0 || stale != 1 {
		t.Errorf("前面的 $ 改掉了还换了 %d 处（%d 处对不上）", done, stale)
	}

	// 结果预览里的替换文字：环视看得到预览里匹配前面的字
	m, _ = Compile(Query{Text: `(?<=\$)(\d)(\d*)`, Regexp: true})
	mt := m.Find([]byte("  cost $42 now\n"))[0]
	if got, ok := m.Expand(mt, "<$2$1>"); !ok || got != "<24>" {
		t.Errorf("预览展开成 %q %v", got, ok)
	}
}

// 回溯爆炸的表达式：每个文件超时就跳过、记数，别的文件照搜。
func TestRegexp2Timeout(t *testing.T) {
	defer func(d time.Duration) { advTimeout = d }(advTimeout)
	advTimeout = 200 * time.Millisecond
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bad.txt", strings.Repeat("a", 40)+"b\n")
	write("ok.txt", "aaa\n")
	root := Root{Name: "r", Dir: dir, Files: []string{"bad.txt", "ok.txt"}}
	start := time.Now()
	res := Search(context.Background(), []Root{root}, Query{Text: `(a+)+$(?<!b)`, Regexp: true})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Advanced || res.TimedOut != 1 || res.Skipped != 1 || res.Searched != 1 || len(res.Matches) != 1 || res.Matches[0].Path != "ok.txt" {
		t.Fatalf("advanced=%v timedOut=%d skipped=%d searched=%d matches=%v", res.Advanced, res.TimedOut, res.Skipped, res.Searched, where(res.Matches))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("跑了 %v", d)
	}
	// 重读一个文件、替换前核对，超时的都当没有
	m, _ := Compile(Query{Text: `(a+)+$(?<!b)`, Regexp: true})
	if ms := m.FindFile(filepath.Join(dir, "bad.txt"), MaxFileSize, new([]byte)); ms != nil {
		t.Errorf("FindFile 给了 %d 处", len(ms))
	}
	if _, done, stale := m.Replace([]byte(strings.Repeat("a", 40)+"b\n"), []Match{{Line: 0, Col: 0, Span: 1, Len: 1}}, "x"); done != 0 || stale != 1 {
		t.Errorf("超时的文件换了 %d 处", done)
	}
}

// expandTemplate 和 regexp.Expand 对同样的匹配给同样的文字（RE2 编得了的表达式拿两个引擎各配一次）。
func TestExpandTemplate(t *testing.T) {
	cases := []struct{ expr, src string }{
		{`(\w+)-(\d+)`, "  abc-123 x"},
		{`(?P<word>\w+)@(?P<host>[a-z.]+)`, "mail a@b.c now"},
		{`(a)|(b)`, "b"},
		{`(x)(y)?`, "x"},
		{`(测)(试)`, "一测试二"},
	}
	templates := []string{"$1", "${1}", "$2$1", "$1x", "${1}x", "$10", "$01", "$$", "$", "$-", "${", "${1", "${word}", "$word.", "$host$word", "$nope", "[$0]", "$3", "$名"}
	for _, c := range cases {
		re := regexp.MustCompile(c.expr)
		m, err := Compile(Query{Text: c.expr, Regexp: true, CaseSensitive: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.compileAdvanced(Query{Text: c.expr, Regexp: true, CaseSensitive: true}); err != nil {
			t.Fatal(err)
		}
		loc := re.FindStringSubmatchIndex(c.src)
		hit, err := m.adv.FindStringMatch(c.src)
		if err != nil || hit == nil {
			t.Fatalf("%s：regexp2 没配上（%v）", c.expr, err)
		}
		aloc, names := advLoc(hit)
		if !slices.Equal(loc, aloc) {
			t.Errorf("%s：组的位置 %v，regexp 是 %v", c.expr, aloc, loc)
		}
		for _, tpl := range templates {
			want := string(re.ExpandString(nil, tpl, c.src, loc))
			if got := string(expandTemplate(nil, tpl, c.src, aloc, names)); got != want {
				t.Errorf("%s 按 %q 展开成 %q，regexp 是 %q", c.expr, tpl, got, want)
			}
		}
	}
}
