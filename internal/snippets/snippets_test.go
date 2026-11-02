package snippets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// texts 是一个跳位每处的文字。
func texts(text string, st Stop) []string {
	var out []string
	for _, r := range st.Ranges {
		out = append(out, text[r[0]:r[1]])
	}
	return out
}

// 展开：默认值、同号跟着、第二行起补缩进、行首 Tab 换成文档的缩进、$0 最后（没写的补在末尾）、转义、
// 不认的照原样、默认里套的算跳位（跟着的那处里套的不算）。
func TestExpand(t *testing.T) {
	e := Expand("for ${1:i} := 0; $1 < ${2:n}; $1++ {\n\t$0\n}", "  ", "\t")
	if want := "for i := 0; i < n; i++ {\n  \t\n  }"; e.Text != want {
		t.Fatalf("fori：%q", e.Text)
	}
	if len(e.Stops) != 3 || e.Stops[0].N != 1 || e.Stops[1].N != 2 || e.Stops[2].N != 0 {
		t.Fatalf("跳位：%+v", e.Stops)
	}
	if got := texts(e.Text, e.Stops[0]); !slices.Equal(got, []string{"i", "i", "i"}) {
		t.Errorf("$1：%q", got)
	}
	if got := texts(e.Text, e.Stops[1]); !slices.Equal(got, []string{"n"}) {
		t.Errorf("$2：%q", got)
	}
	if r := e.Stops[2].Ranges[0]; r[0] != r[1] || e.Text[:r[0]] != "for i := 0; i < n; i++ {\n  \t" {
		t.Errorf("$0 在 %v", r)
	}

	cases := []struct{ body, text string }{
		{`\$x \} \\ ${1|a,b|} $TM_FILENAME`, `$x } \ ${1|a,b|} $TM_FILENAME`},
		{"$1 = ${1:x}", "x = x"},
		{"def ${1:name}(${2}):\n\t${0:pass}", "def name():\n        pass"},
		{"${1:unclosed", "${1:unclosed"},
	}
	for _, c := range cases {
		if e := Expand(c.body, "    ", "    "); e.Text != c.text {
			t.Errorf("%q 展开成 %q，应是 %q", c.body, e.Text, c.text)
		}
	}
	if e := Expand(`\$x`, "", "\t"); len(e.Stops) != 1 || e.Stops[0].Ranges[0] != [2]int{2, 2} {
		t.Errorf("没有跳位的：%+v", e.Stops)
	}
	if e := Expand("def f():\n\t${0:pass}", "", "  "); texts(e.Text, e.Stops[0])[0] != "pass" {
		t.Errorf("${0:pass} 应选中 pass：%+v", e.Stops)
	}
	e = Expand("${1:foo ${2:bar}} $1", "", "\t")
	if e.Text != "foo bar foo bar" || !slices.Equal(texts(e.Text, e.Stops[0]), []string{"foo bar", "foo bar"}) || !slices.Equal(texts(e.Text, e.Stops[1]), []string{"bar"}) {
		t.Errorf("套着的：%q %+v", e.Text, e.Stops)
	}
}

// buf 是测试里的缓冲区：一串文字和版本，edit 在几处同时换（像多光标打字）。
type buf struct {
	s   string
	ver uint64
}

func (b *buf) edit(rs [][2]int, text string) {
	for i := len(rs) - 1; i >= 0; i-- {
		b.s = b.s[:rs[i][0]] + text + b.s[rs[i][1]:]
	}
	b.ver++
}

func (b *buf) slice(x, y int) string { return b.s[x:y] }

// 跳位会话：在 $1 的三处一起打字，后面的跳位、End 跟着挪；Tab 到 $2 再打；⇧Tab 回去；到 $0 结束；
// 别处改了对不上就结束；挨着的空跳位，打的字在前一个里。
func TestSession(t *testing.T) {
	e := Expand("for ${1:i} := 0; $1 < ${2:n}; $1++ {\n\t$0\n}", "", "\t")
	b := &buf{s: "// head\n"}
	at := len(b.s)
	b.s += e.Text + "\n// tail\n"
	b.ver = 1
	s := NewSession(at, e, len(b.s), b.ver)
	if s == nil || s.Count() != 2 || s.Current() != 1 {
		t.Fatalf("会话：%+v", s)
	}
	b.edit(s.Ranges(), "idx")
	if !s.Sync(b.ver, len(b.s), b.slice) {
		t.Fatal("在 $1 里打字不该结束")
	}
	if got := b.s[s.Start:s.End]; got != "for idx := 0; idx < n; idx++ {\n\t\n}" {
		t.Fatalf("片段：%q", got)
	}
	if last := s.Move(1); last {
		t.Fatal("$2 不是最后")
	}
	if r := s.Ranges()[0]; b.s[r[0]:r[1]] != "n" || s.Current() != 2 {
		t.Fatalf("$2 在 %v（%q）", r, b.s[r[0]:r[1]])
	}
	b.edit(s.Ranges(), "len(xs)")
	if !s.Sync(b.ver, len(b.s), b.slice) {
		t.Fatal("在 $2 里打字不该结束")
	}
	s.Move(-1)
	if got := texts(b.s, s.Stops[0]); !slices.Equal(got, []string{"idx", "idx", "idx"}) {
		t.Errorf("⇧Tab 回 $1：%q", got)
	}
	s.Move(-1)
	if s.Current() != 1 {
		t.Error("第一个往前不动")
	}
	s.Move(1)
	if !s.Move(1) || !strings.HasSuffix(b.s[:s.Ranges()[0][0]], "len(xs); idx++ {\n\t") {
		t.Fatalf("到 $0：%v", s.Ranges())
	}

	// 别处改了：对不上，会话结束（片段前面、片段里跳位外、片段后面）
	for _, ed := range []struct {
		what string
		at   func(n int) [2]int
		text string
	}{
		{"片段前面", func(int) [2]int { return [2]int{0, 0} }, "x"},
		{"跳位外的 for", func(int) [2]int { return [2]int{at, at + 3} }, "FOR"},
		{"片段后面", func(n int) [2]int { return [2]int{n - 1, n - 1} }, "x"},
	} {
		b := &buf{s: "// head\n" + e.Text + "\n// tail\n", ver: 1}
		s := NewSession(at, e, len(b.s), b.ver)
		b.edit([][2]int{ed.at(len(b.s))}, ed.text)
		if s.Sync(b.ver, len(b.s), b.slice) {
			t.Errorf("%s改了还接着", ed.what)
		}
	}

	// 挨着的空跳位：打的字在 $1 里，$2 挪到后面
	e = Expand("${1}${2}x$0", "", "\t")
	b = &buf{s: e.Text, ver: 1}
	s = NewSession(0, e, len(b.s), b.ver)
	b.edit(s.Ranges(), "ab")
	if !s.Sync(b.ver, len(b.s), b.slice) || s.Stops[0].Ranges[0] != [2]int{0, 2} || s.Stops[1].Ranges[0] != [2]int{2, 2} || s.End != 3 {
		t.Fatalf("空跳位：%+v", s)
	}
	if NewSession(0, Expand("x$0", "", "\t"), 1, 1) != nil {
		t.Error("只有 $0 的不该有会话")
	}
}

// 读片段目录：文件里的同名覆盖内置、新加的加上、前缀数组各算一条、global 所有语言都有、TypeScript 借 JavaScript 的；
// 坏文件记进 Errs、别的照读；文件改了 Stale。
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.json"), []byte(`{"fori": {"prefix": "fori", "body": "for {}", "description": "我的"}, "x": {"prefix": ["xa", "xb"], "body": ["a", "b"]}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "global.json"), []byte(`{"date": {"body": "2026"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "rust.json"), []byte(`{"bad": `), 0o644)
	s := Load(dir)
	if len(s.Errs) != 1 || !strings.HasPrefix(s.Errs[0], "rust.json: ") {
		t.Fatalf("坏文件：%q", s.Errs)
	}
	got := map[string]Snippet{}
	for _, sn := range s.For("go") {
		got[sn.Prefix] = sn
	}
	if f := got["fori"]; !f.File || f.Body != "for {}" || f.Description() != "我的" {
		t.Errorf("fori 没被文件覆盖：%+v", f)
	}
	if f := got["forr"]; f.File || f.Description() != "for range 循环" {
		t.Errorf("内置的 forr：%+v", f)
	}
	if got["xa"].Body != "a\nb" || got["xb"].Name != "x" || got["xb"].Description() != "x" {
		t.Errorf("前缀数组：%+v %+v", got["xa"], got["xb"])
	}
	if got["date"].Lang != Global {
		t.Errorf("global 没进来：%+v", got["date"])
	}
	ts := s.For(LangKey("TypeScript"))
	if !slices.ContainsFunc(ts, func(sn Snippet) bool { return sn.Prefix == "afn" }) || !slices.ContainsFunc(ts, func(sn Snippet) bool { return sn.Prefix == "date" }) {
		t.Errorf("TypeScript：%+v", ts)
	}
	if len(s.For("")) != 1 {
		t.Errorf("纯文本只有 global 的：%+v", s.For(""))
	}
	if langs := s.Langs(); langs[0] != Global || !slices.Contains(langs, "go") || !slices.Contains(langs, "markdown") {
		t.Errorf("语言：%q", langs)
	}
	if s.Stale() {
		t.Fatal("没改就 Stale")
	}
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(filepath.Join(dir, "go.json"), later, later)
	if !s.Stale() {
		t.Fatal("改了没 Stale")
	}
	if Load("").Stale() || len(Load(filepath.Join(dir, "没有")).Errs) != 0 {
		t.Error("没有目录")
	}
}

// 内置的：都解析得了、每种不超过 8 条、说明都在 builtinDescs 里（i18n 收得到）、写样板不盖已有的。
func TestBuiltins(t *testing.T) {
	s := Load("")
	for _, lang := range s.Langs() {
		list := s.Of(lang)
		if len(list) == 0 || len(list) > 8 {
			t.Errorf("%s 有 %d 条", lang, len(list))
		}
		for _, sn := range list {
			if !slices.Contains(builtinDescs, sn.Desc) {
				t.Errorf("%s/%s 的说明 %q 不在 builtinDescs 里", lang, sn.Name, sn.Desc)
			}
			if e := Expand(sn.Body, "", "\t"); len(e.Stops) == 0 {
				t.Errorf("%s/%s 没有 $0", lang, sn.Name)
			}
		}
	}
	dir := filepath.Join(t.TempDir(), "snippets")
	n, err := WriteBuiltins(dir)
	if err != nil || n != 5 {
		t.Fatalf("写样板：%d %v", n, err)
	}
	os.WriteFile(filepath.Join(dir, "go.json"), []byte(`{}`), 0o644)
	os.Remove(filepath.Join(dir, "shell.json"))
	if n, _ := WriteBuiltins(dir); n != 1 {
		t.Errorf("再写一次写了 %d 个", n)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "go.json")); string(b) != "{}" {
		t.Error("盖了已有的 go.json")
	}
}
