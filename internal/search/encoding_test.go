package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"

	"github.com/lemoeade/grove/internal/docs"
	"github.com/lemoeade/grove/internal/testutil"
)

// 不是 UTF-8 的文件按编辑器认的编码解成文字再搜：位置是解码后的 UTF-8 字节，预览是合法的 UTF-8。
// 日文、韩文、繁体要够长才认得出（短的按 GB18030），样本和 docs 的测试同一种长度。

const (
	jaLine = "設定ファイルの読み込みに失敗した場合、プログラムはエラーを返してログに記録します。"
	koLine = "설정 파일을 읽지 못하면 프로그램은 오류를 반환하고 로그에 기록합니다."
	twLine = "設定檔讀取失敗時，程式會回傳錯誤並記錄日誌。這個函式負責把使用者輸入的路徑轉換成絕對路徑。"
)

func encodeWith(t *testing.T, e encoding.Encoding, s string) string {
	t.Helper()
	b, err := e.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSearchEncodings(t *testing.T) {
	r := testutil.NewRepo(t, "enc")
	// fake 的 GB18030 是 81 66：第二个字节是 "f"，后面跟 "oo" 就在字节里凑出一个 "foo"
	fake, err := simplifiedchinese.GB18030.NewDecoder().String("\x81\x66")
	if err != nil {
		t.Fatal(err)
	}
	r.Write("gb.txt", encodeWith(t, simplifiedchinese.GB18030, "第一行\r\n中文"+fake+"oo 测试 foo\r\n"))
	r.Write("ja.txt", encodeWith(t, japanese.ShiftJIS, "# "+jaLine+"\n"))
	r.Write("ko.txt", encodeWith(t, korean.EUCKR, "# "+koLine+"\n"))
	r.Write("tw.txt", encodeWith(t, traditionalchinese.Big5, "# "+twLine+"\n"))
	r.Write("le.txt", string(docs.Encode("ascii\n中文 foo\n", docs.UTF16LE)))
	r.Write("be.txt", string(docs.Encode("ascii\n中文 foo\n", docs.UTF16BE)))
	r.Write("bare.txt", encodeWith(t, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), "no bom here\nfoo 中文\n"))
	r.Write("odd.txt", "x \x80\x80\xff foo\n")
	r.Write("bom.txt", "\xef\xbb\xbf中文 foo\n")
	root := Root{Name: "enc", Dir: r.Root}

	find := func(text string) map[string]Match {
		t.Helper()
		res := Search(context.Background(), []Root{root}, Query{Text: text, CaseSensitive: true})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		out := map[string]Match{}
		for _, m := range res.Matches {
			if !utf8.ValidString(m.Preview) || m.PreviewCol < 0 || m.PreviewCol+m.Len > len(m.Preview) {
				t.Errorf("%s 的预览不合法：%q @%d", m.Path, m.Preview, m.PreviewCol)
				continue
			}
			if m.Enc != docs.Unknown && m.Preview[m.PreviewCol:m.PreviewCol+m.Len] != text {
				t.Errorf("%s 预览里的匹配是 %q", m.Path, m.Preview[m.PreviewCol:m.PreviewCol+m.Len])
			}
			if _, dup := out[m.Path]; dup {
				t.Errorf("%s 不止一处", m.Path)
			}
			out[m.Path] = m
		}
		return out
	}
	check := func(ms map[string]Match, path string, line, col int, enc docs.Encoding) {
		t.Helper()
		m, ok := ms[path]
		switch {
		case !ok:
			t.Errorf("%s 没搜到", path)
		case m.Line != line || m.Col != col || m.Enc != enc:
			t.Errorf("%s：%d:%d %q，要 %d:%d %q", path, m.Line, m.Col, m.Enc, line, col, enc)
		}
	}

	zh := find("中文")
	check(zh, "gb.txt", 1, 0, docs.GB18030)
	check(zh, "le.txt", 1, 0, docs.UTF16LE)
	check(zh, "be.txt", 1, 0, docs.UTF16BE)
	check(zh, "bare.txt", 1, len("foo "), docs.UTF16LENoBOM)
	check(zh, "bom.txt", 0, 0, "") // UTF-8 BOM 照旧：BOM 不算进列
	if len(zh) != 5 {
		t.Errorf("中文：%d 个文件", len(zh))
	}

	foo := find("foo")
	// gb.txt 里字节上凑出来的那个 foo 不算，只有真的 foo：列是 UTF-8 字节
	check(foo, "gb.txt", 1, len("中文"+fake+"oo 测试 "), docs.GB18030)
	if m := foo["gb.txt"]; m.Len != 3 || m.Preview != "中文"+fake+"oo 测试 foo" {
		t.Errorf("gb.txt：%+v", m)
	}
	check(foo, "le.txt", 1, len("中文 "), docs.UTF16LE)
	check(foo, "bare.txt", 1, 0, docs.UTF16LENoBOM)
	// 认不出的按原来的字节搜：ASCII 还搜得到，预览里的坏字节换成 U+FFFD
	check(foo, "odd.txt", 0, len("x \x80\x80\xff "), docs.Unknown)
	if m := foo["odd.txt"]; m.Preview != "x � foo" || m.Preview[m.PreviewCol:] != "foo" {
		t.Errorf("odd.txt 预览 %q @%d", m.Preview, m.PreviewCol)
	}

	// 日文、韩文、繁体：各一个全角字
	check(find("読"), "ja.txt", 0, len("# 設定ファイルの"), docs.ShiftJIS)
	if ms := find("법"); len(ms) != 0 { // 没有这个字
		t.Errorf("법 搜到了 %v", ms)
	}
	check(find("반"), "ko.txt", 0, strings.Index("# "+koLine, "반"), docs.EUCKR)
	check(find("讀"), "tw.txt", 0, len("# 設定檔"), docs.Big5)
}

func TestValidPreview(t *testing.T) {
	for _, c := range []struct {
		p    string
		pc   int
		want string
		at   int
	}{
		{"合法 foo", len("合法 "), "合法 foo", len("合法 ")},
		{"\x80\x81 foo \xff", 3, "� foo �", len("� ")},
		{"…\xe4 foo", len("…\xe4 "), "…� foo", len("…� ")},
		{"\xff", 5, "�", 5 + len("�") - 1}, // 越界的起点不切，只挪
	} {
		if got, at := validPreview(c.p, c.pc); got != c.want || at != c.at {
			t.Errorf("%q @%d → %q @%d，要 %q @%d", c.p, c.pc, got, at, c.want, c.at)
		}
	}
}

// 量一下：1000 个 10 KB 的文件，UTF-8 一份、GB18030 一份，搜同一个中文词（每个文件中间有一处）。
// GB18030 的多一次认编码 + 解码，总时间不超过 UTF-8 的 3 倍。WORKBENCH_MEASURE=1 才跑。
func TestMeasureEncodings(t *testing.T) {
	if os.Getenv("WORKBENCH_MEASURE") == "" {
		t.Skip("WORKBENCH_MEASURE=1 才量")
	}
	unit := "\t// 配置文件读取失败时，程序会返回错误并记录日志。这个函数负责把用户输入的路径转换成绝对路径。\n\tif err := load(path); err != nil {\n\t\treturn err\n\t}\n"
	body := strings.Repeat(unit, 10<<10/len(unit)/2)
	text := body + "\t// 检查点：这里只出现一次\n" + body
	roots := map[string]Root{}
	for name, data := range map[string]string{"utf8": text, "gb": encodeWith(t, simplifiedchinese.GB18030, text)} {
		dir := t.TempDir()
		var list []string
		for i := range 1000 {
			p := fmt.Sprintf("f%04d.go", i)
			if err := os.WriteFile(filepath.Join(dir, p), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			list = append(list, p)
		}
		roots[name] = Root{Name: name, Dir: dir, Files: list, Plain: true}
	}
	median := func(name string) time.Duration {
		var ds []time.Duration
		for i := range 8 {
			res := Search(context.Background(), []Root{roots[name]}, Query{Text: "检查点"})
			if res.Err != nil || len(res.Matches) != 1000 || res.Truncated {
				t.Fatalf("%s：%v，%d 处", name, res.Err, len(res.Matches))
			}
			if i > 0 { // 第一次热一下文件缓存
				ds = append(ds, res.Elapsed)
			}
		}
		slices.Sort(ds)
		return ds[len(ds)/2]
	}
	u, g := median("utf8"), median("gb")
	t.Logf("1000 × %d 字节：UTF-8 %v，GB18030 %v（%.2f 倍，GOMAXPROCS %d）", len(text), u, g, float64(g)/float64(u), runtime.GOMAXPROCS(0))
	if g > 3*u {
		t.Errorf("GB18030 比 UTF-8 慢了 %.1f 倍", float64(g)/float64(u))
	}
}
