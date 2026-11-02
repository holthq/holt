package search

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// replaceIn 在 data 里按 q 搜一次，再把所有的换成 repl。
func replaceIn(t *testing.T, q Query, data, repl string) (string, int, int) {
	t.Helper()
	m, err := Compile(q)
	if err != nil {
		t.Fatal(err)
	}
	out, done, stale := m.Replace([]byte(data), m.Find([]byte(data)), repl)
	return string(out), done, stale
}

func TestReplaceLiteral(t *testing.T) {
	cases := []struct {
		name       string
		q          Query
		data, repl string
		want       string
		done       int
	}{
		{"不区分大小写", Query{Text: "foo"}, "foo Foo FOO\nbarfoo\n", "x", "x x x\nbarx\n", 4},
		{"区分大小写", Query{Text: "Foo", CaseSensitive: true}, "foo Foo FOO\n", "x", "foo x FOO\n", 1},
		{"整词", Query{Text: "foo", WholeWord: true}, "foo foobar _foo foo", "x", "x foobar _foo x", 2},
		{"换成空的就是删掉", Query{Text: "foo"}, "a foo b\n", "", "a  b\n", 1},
		{"$ 原样插进去", Query{Text: "a"}, "a\n", "$1$$", "$1$$\n", 1},
		{"CRLF 的换行跟着文件", Query{Text: "foo"}, "a foo\r\nfoo b\r\n", "1\n2", "a 1\r\n2\r\n1\r\n2 b\r\n", 2},
		{"LF 的不动", Query{Text: "foo"}, "foo\n", "1\n2", "1\n2\n", 1},
		{"BOM 后面", Query{Text: "foo"}, "\xef\xbb\xbffoo foo\n", "bar", "\xef\xbb\xbfbar bar\n", 2},
		{"中文", Query{Text: "测试"}, "测试一下，测试。\n", "试验", "试验一下，试验。\n", 2},
		{"非 ASCII 的不区分大小写", Query{Text: "Äb"}, "äb ÄB\n", "$1", "$1 $1\n", 2},
	}
	for _, c := range cases {
		got, done, stale := replaceIn(t, c.q, c.data, c.repl)
		if got != c.want || done != c.done || stale != 0 {
			t.Errorf("%s：%q（%d 处，%d 处对不上），想要 %q（%d 处）", c.name, got, done, stale, c.want, c.done)
		}
	}
}

func TestReplaceRegexp(t *testing.T) {
	cases := []struct {
		name       string
		q          Query
		data, repl string
		want       string
	}{
		{"$1 $2", Query{Text: `(\w+)@(\w+)`, Regexp: true}, "a@b c@d\n", "$2@$1", "b@a d@c\n"},
		{"${name}", Query{Text: `(?P<k>\w+)=1`, Regexp: true}, "x=1 yy=1\n", "${k}=2", "x=2 yy=2\n"},
		{"$$ 是 $", Query{Text: `\d+`, Regexp: true}, "a 12 b 3\n", "$$", "a $ b $\n"},
		{"跨行的整个换掉", Query{Text: `a\nb`, Regexp: true}, "xa\nby\n", "-", "x-y\n"},
		{"不区分大小写", Query{Text: `fo+`, Regexp: true}, "FOO foo\n", "<$0>", "<FOO> <foo>\n"},
		{"整词", Query{Text: `fo+`, Regexp: true, WholeWord: true}, "foo food\n", "x", "x food\n"},
		{"行首", Query{Text: `^\s*//`, Regexp: true}, "// a\n  // b\nc // d\n", "#", "# a\n# b\nc // d\n"},
	}
	for _, c := range cases {
		got, _, stale := replaceIn(t, c.q, c.data, c.repl)
		if got != c.want || stale != 0 {
			t.Errorf("%s：%q（%d 处对不上），想要 %q", c.name, got, stale, c.want)
		}
	}
}

// 搜完之后文件改了：改了的那一行、没了的行、重叠的、位置错了的都不换，算进 stale；别的照换。
func TestReplaceStale(t *testing.T) {
	m, _ := Compile(Query{Text: "foo"})
	old := []byte("foo\nfoo\nfoo\nfoo bar foo\n")
	ms := m.Find(old)
	if len(ms) != 5 {
		t.Fatalf("搜到 %d 处", len(ms))
	}
	now := []byte("foo\nfxo\nfoo\nfoo baz foo\n")
	out, done, stale := m.Replace(now, ms, "X")
	if string(out) != "X\nfxo\nX\nfoo baz foo\n" || done != 2 || stale != 3 {
		t.Fatalf("改过的文件：%q %d %d", out, done, stale)
	}
	// 变短了：后面的行没了
	out, done, stale = m.Replace([]byte("foo\n"), ms, "X")
	if string(out) != "X\n" || done != 1 || stale != 4 {
		t.Fatalf("变短了：%q %d %d", out, done, stale)
	}
	// 同一处给了两次、顺序乱的：只换一次
	twice := []Match{ms[2], ms[0], ms[0]}
	out, done, stale = m.Replace(old, twice, "X")
	if string(out) != "X\nfoo\nX\nfoo bar foo\n" || done != 2 || stale != 1 {
		t.Fatalf("重复的：%q %d %d", out, done, stale)
	}
	// 一处都没换：原样返回同一块内存
	if out, done, _ := m.Replace(now, ms[1:2], "X"); done != 0 || &out[0] != &now[0] {
		t.Fatal("没有要换的也复制了")
	}
	// 正则：从这里起还能匹配但长度变了，不换
	re, _ := Compile(Query{Text: `a+`, Regexp: true})
	rms := re.Find([]byte("xaay\n"))
	if out, done, stale := re.Replace([]byte("xaaa\n"), rms, "b"); string(out) != "xaaa\n" || done != 0 || stale != 1 {
		t.Fatalf("长度变了的正则：%q %d %d", out, done, stale)
	}
}

// 排除了的那几处在别处换完之后挪到哪：同一行前面换长了、前面多了行都跟着挪，自己被换掉的没了。
func TestShift(t *testing.T) {
	m, _ := Compile(Query{Text: "foo"})
	data := []byte("\xef\xbb\xbffoo foo\nfoo\n")
	ms := m.Find(data)
	edits, _ := m.Plan(data, ms[:1], "a\nbarbaz")
	out := Apply(data, edits)
	for _, c := range []struct{ line, col, wl, wc int }{{0, 4, 1, 7}, {1, 0, 2, 0}} {
		if l, col, ok := Shift(data, out, edits, c.line, c.col); !ok || l != c.wl || col != c.wc {
			t.Errorf("%d:%d 挪到 %d:%d %v，想要 %d:%d", c.line, c.col, l, col, ok, c.wl, c.wc)
		}
	}
	if _, _, ok := Shift(data, out, edits, 0, 0); ok {
		t.Error("换掉的那一处还在")
	}
	if got := m.Find(out); len(got) != 2 || got[0].Line != 1 || got[0].Col != 7 || got[1].Line != 2 {
		t.Errorf("重搜：%v", where(got))
	}
}

func TestExpandPreview(t *testing.T) {
	re, _ := Compile(Query{Text: `(\w+)@(\w+)`, Regexp: true})
	ms := re.Find([]byte("  mail a@b now\n"))
	if got, ok := re.Expand(ms[0], "$2@$1"); !ok || got != "b@a" {
		t.Errorf("正则的预览：%q %v", got, ok)
	}
	lit, _ := Compile(Query{Text: "a@b"})
	if got, ok := lit.Expand(lit.Find([]byte("a@b\n"))[0], "$2"); !ok || got != "$2" {
		t.Errorf("字面的预览：%q %v", got, ok)
	}
	// 跨行的匹配在预览（只有第一行）里对不上：原样给 repl
	ml, _ := Compile(Query{Text: `a\nb`, Regexp: true})
	if got, ok := ml.Expand(ml.Find([]byte("xa\nb\n"))[0], "-$0-"); ok || got != "-$0-" {
		t.Errorf("跨行的预览：%q %v", got, ok)
	}
}

func TestSpan(t *testing.T) {
	lit, _ := Compile(Query{Text: "ab"})
	if m := lit.Find([]byte("xab\n"))[0]; m.Len != 2 || m.Span != 2 {
		t.Errorf("字面：%+v", m)
	}
	re, _ := Compile(Query{Text: `b\ncd`, Regexp: true})
	if m := re.Find([]byte("ab\ncde\n"))[0]; m.Col != 1 || m.Len != len("b⏎cd") || m.Span != 4 || m.Preview != "ab⏎cde" || m.PreviewCol != 1 {
		t.Errorf("跨行的正则：%+v", m)
	}
	// BOM 不算进第一行的列；\r\n 的 \r 不让预览多个"…"
	if m := lit.Find([]byte("\xef\xbb\xbfab\n"))[0]; m.Col != 0 || m.Preview != "ab" {
		t.Errorf("BOM：%+v", m)
	}
	long := strings.Repeat("x", previewAfter)
	if m := lit.Find([]byte("ab" + long + "\r\n"))[0]; strings.HasSuffix(m.Preview, "…") {
		t.Errorf("\\r 算成了截断：%q", m.Preview[len(m.Preview)-8:])
	}
}

// Root.Sub：只搜一个目录下面的（文件列表现跑的、给好的都一样）。
func TestRootDir(t *testing.T) {
	root := fixture(t)
	for _, files := range [][]string{nil, {"a.go", "docs/b.md", "docs/sub/c.md", "docsx.md", "new.txt"}} {
		for sub, want := range map[string][]string{
			"docs":     {"docs/b.md:3:16", "docs/sub/c.md:1:1"},
			"docs/sub": {"docs/sub/c.md:1:1"},
		} {
			r := root
			r.Files, r.Sub = slices.Clone(files), sub
			res := Search(context.Background(), []Root{r}, Query{Text: "hello"})
			if res.Err != nil || !slices.Equal(where(res.Matches), want) {
				t.Errorf("Sub=%s Files=%v：%v %v", sub, files != nil, where(res.Matches), res.Err)
			}
		}
	}
}

// 1000 个文件、每个 5 处：在内存里全部换一遍不到 2 秒（预算，换成 Plan + Apply 的纯计算）。
func TestReplaceMany(t *testing.T) {
	if testing.Short() {
		t.Skip("慢")
	}
	m, _ := Compile(Query{Text: "needle"})
	data := []byte(strings.Repeat("some text needle and more\n", 5) + strings.Repeat("filler line without it\n", 200))
	start := time.Now()
	for range 1000 {
		ms := m.Find(data)
		out, done, stale := m.Replace(data, ms, "pin")
		if done != 5 || stale != 0 || len(out) != len(data)-5*3 {
			t.Fatalf("换了 %d 处、%d 处对不上", done, stale)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("1000 个文件换了 %v", d)
	}
}
