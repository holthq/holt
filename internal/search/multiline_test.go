package search

import (
	"strings"
	"testing"
)

// 多行的字面：换行配 \n 和 \r\n，别的字照字面；Span 是磁盘上的长度，BufLen 是编辑器缓冲区里的。
func TestMultiLineLiteral(t *testing.T) {
	for _, c := range []struct {
		name, data string
		span, cr   int
	}{
		{"LF", "ab\ncde\n", 4, 0},
		{"CRLF", "ab\r\ncde\r\n", 5, 1},
	} {
		m, err := Compile(Query{Text: "B\ncD"})
		if err != nil {
			t.Fatal(err)
		}
		ms := m.Find([]byte(c.data))
		if len(ms) != 1 {
			t.Fatalf("%s：%+v", c.name, ms)
		}
		mt := ms[0]
		if mt.Line != 0 || mt.Col != 1 || mt.Span != c.span || mt.CR != c.cr || mt.BufLen() != 4 {
			t.Errorf("%s：%+v", c.name, mt)
		}
		if mt.Preview != "ab⏎cde" || mt.PreviewCol != 1 || mt.Preview[mt.PreviewCol:mt.PreviewCol+mt.Len] != "b⏎cd" {
			t.Errorf("%s 的预览：%q %d %d", c.name, mt.Preview, mt.PreviewCol, mt.Len)
		}
		// 磁盘上的换掉，换行跟着文件
		out, done, stale := m.Replace([]byte(c.data), ms, "X\nY")
		want := strings.ReplaceAll("aX\nYe\n", "\n", strings.Repeat("\r", c.cr)+"\n")
		if string(out) != want || done != 1 || stale != 0 {
			t.Errorf("%s 换掉：%q %d %d，想要 %q", c.name, out, done, stale, want)
		}
		// 同一处在编辑器缓冲区（只有 \n）里换
		buf := strings.ReplaceAll(c.data, "\r", "")
		if out, done, stale := m.Replace([]byte(buf), ms, "X\nY"); string(out) != "aX\nYe\n" || done != 1 || stale != 0 {
			t.Errorf("%s 在缓冲区里换：%q %d %d", c.name, out, done, stale)
		}
	}
	// 正则的字照字面；区分大小写；整词
	for _, c := range []struct {
		q    Query
		data string
		n    int
	}{
		{Query{Text: "a.b\n(c"}, "a.b\n(c\naxb\n(c\n", 1},
		{Query{Text: "A\nb", CaseSensitive: true}, "a\nb\nA\nb\n", 1},
		{Query{Text: "a\nb", WholeWord: true}, "a\nb xa\nb a\nbc\n", 1},
		{Query{Text: "\n\n"}, "a\n\nb\r\n\r\nc\n", 2},
	} {
		m, err := Compile(c.q)
		if err != nil {
			t.Fatal(err)
		}
		if ms := m.Find([]byte(c.data)); len(ms) != c.n {
			t.Errorf("%+v：%+v", c.q, ms)
		}
	}
}

// 跨行的正则换掉（$1 $2 里有换行），磁盘上的 CRLF 和缓冲区的 LF 都对。
func TestMultiLineRegexpReplace(t *testing.T) {
	m, err := Compile(Query{Text: `(\w+)\r?\n(\w+)`, Regexp: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ data, want string }{
		{"x ab\ncd y\n", "x cd\nab y\n"},
		{"x ab\r\ncd y\r\n", "x cd\r\nab y\r\n"},
	} {
		ms := m.Find([]byte(c.data))
		if len(ms) != 1 || ms[0].Preview != "x ab⏎cd y" {
			t.Fatalf("%q：%+v", c.data, ms)
		}
		if out, done, stale := m.Replace([]byte(c.data), ms, "$2\n$1"); string(out) != c.want || done != 1 || stale != 0 {
			t.Errorf("%q：%q %d %d", c.data, out, done, stale)
		}
		buf := strings.ReplaceAll(c.data, "\r", "")
		if out, _, stale := m.Replace([]byte(buf), ms, "$2\n$1"); string(out) != "x cd\nab y\n" || stale != 0 {
			t.Errorf("缓冲区 %q：%q %d", buf, out, stale)
		}
	}
	// 搜完之后跨的那几行改了：对不上，不换
	data := "ab\ncd\n"
	ms := m.Find([]byte(data))
	if out, done, stale := m.Replace([]byte("ab\nce\n"), ms, "-"); string(out) != "ab\nce\n" || done != 0 || stale != 1 {
		t.Errorf("改了的：%q %d %d", out, done, stale)
	}
}

// 跨行的预览：换行画成 ⏎，太长的匹配和最后一行截掉带"…"，Tab 当空格（开头的缩进照旧去掉）。
func TestMultiLinePreview(t *testing.T) {
	m, _ := Compile(Query{Text: "b\n"})
	if mt := m.Find([]byte("\tab\n\tc\n"))[0]; mt.Preview != "ab⏎ c" || mt.PreviewCol != 1 || mt.Len != len("b⏎") {
		t.Errorf("Tab：%+v", mt)
	}
	long := strings.Repeat("y", 3*previewAfter)
	if mt := m.Find([]byte("ab\n" + long + "\n"))[0]; !strings.HasSuffix(mt.Preview, "…") || len(mt.Preview) > 2*previewAfter+10 {
		t.Errorf("最后一行太长：%d %q", len(mt.Preview), mt.Preview[len(mt.Preview)-6:])
	}
	re, _ := Compile(Query{Text: `a[\s\S]*z`, Regexp: true})
	text := "a" + strings.Repeat("中\n", previewAfter) + "z\n"
	mt := re.Find([]byte(text))[0]
	hit := mt.Preview[mt.PreviewCol:]
	if !strings.HasSuffix(hit, "…") || mt.Len > previewAfter || !strings.HasPrefix(hit, "a中⏎中⏎") || !strings.Contains(hit[:mt.Len], "⏎") {
		t.Errorf("太长的匹配：%d %q", mt.Len, hit)
	}
	if strings.ContainsRune(mt.Preview[:mt.PreviewCol+mt.Len], '�') {
		t.Errorf("切开了字：%q", mt.Preview)
	}
}
