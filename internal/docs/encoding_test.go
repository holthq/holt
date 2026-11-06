package docs

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"

	"github.com/holthq/holt/internal/testutil"
)

// 样本都在这里用 x/text 现编，不放二进制文件。
const (
	zhsText = "配置文件读取失败时，程序会返回错误并记录日志。这个函数负责把用户输入的路径转换成绝对路径，然后检查文件是否存在；如果不存在就创建一个新的空文件。注意：并发调用时需要加锁。"
	zhxText = "王堃和陶喆在镕基路上讨论了一下缓存的问题：读写分离之后命中率提高了不少，但是偶尔还会出现脏读。" // 堃喆镕不在 GB2312 里
	zhtText = "設定檔讀取失敗時，程式會回傳錯誤並記錄日誌。這個函式負責把使用者輸入的路徑轉換成絕對路徑，然後檢查檔案是否存在；如果不存在就建立一個新的空檔案。注意：並行呼叫時需要加鎖。"
	jaText  = "設定ファイルの読み込みに失敗した場合、プログラムはエラーを返してログに記録します。この関数は、ユーザーが入力したパスを絶対パスに変換し、ファイルが存在するかどうかを確認します。"
	koText  = "설정 파일을 읽지 못하면 프로그램은 오류를 반환하고 로그에 기록합니다. 이 함수는 사용자가 입력한 경로를 절대 경로로 바꾼 다음 파일이 있는지 확인합니다."
)

func encodeWith(t testing.TB, e encoding.Encoding, s string) []byte {
	t.Helper()
	b, err := e.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeWith(t testing.TB, e encoding.Encoding, b []byte) string {
	t.Helper()
	s, err := e.NewDecoder().Bytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(s)
}

func gb(t testing.TB, s string) []byte { return encodeWith(t, simplifiedchinese.GB18030, s) }

func TestDecode(t *testing.T) {
	lone := append(gb(t, "中文"), 0x80, '\n') // 单独的 0x80 读成 €，编回去是 A2 E3
	for _, c := range []struct {
		name     string
		data     []byte
		text     string
		enc      Encoding
		editable bool
	}{
		{"UTF-8", []byte("中文\n"), "中文\n", UTF8, true},
		{"空文件", nil, "", UTF8, true},
		{"UTF-8 BOM", append([]byte{0xEF, 0xBB, 0xBF}, "a\n"...), "a\n", UTF8BOM, true},
		{"UTF-16 LE", []byte{0xFF, 0xFE, 'h', 0, 'i', 0}, "hi", UTF16LE, true},
		{"UTF-16 BE", []byte{0xFE, 0xFF, 0, 'h', 0, 'i'}, "hi", UTF16BE, true},
		{"GB18030", gb(t, "你好，世界\n"), "你好，世界\n", GB18030, true},
		{"GB18030 单独的 0x80", lone, "中文€\n", GB18030, false},
		{"Big5", encodeWith(t, traditionalchinese.Big5, zhtText+"\n"), zhtText + "\n", Big5, true}, // 老编码都能编辑
		{"二进制", []byte{1, 0, 2}, "", Binary, false},
		{"认不出", []byte{0x80, 0x80, 0xFF}, "\x80\x80\xFF", Unknown, false},
	} {
		text, enc, editable := Decode(c.data)
		if text != c.text || enc != c.enc || editable != c.editable {
			t.Errorf("%s：%q %q %v，应为 %q %q %v", c.name, text, enc, editable, c.text, c.enc, c.editable)
		}
	}
	for _, e := range []Encoding{"", UTF8, UTF8BOM, UTF16LE, UTF16BE, GB18030, Big5, ShiftJIS, EUCKR, EUCJP, Windows1252, ISO88591} {
		if !e.Writable() || Encode("x", e) == nil {
			t.Errorf("%q 应能写回", e)
		}
	}
	for _, e := range []Encoding{UTF16LENoBOM, UTF16BENoBOM, Unknown, Binary} {
		if e.Writable() || Encode("x", e) != nil {
			t.Errorf("%q 应只读、不编码", e)
		}
	}
	if Encoding("").Label() != "UTF-8" || UTF16LENoBOM.Label() != "UTF-16 LE（无 BOM）" {
		t.Errorf("写法：%q %q", Encoding("").Label(), UTF16LENoBOM.Label())
	}
	if UTF8BOM.Family() != UTF8 || UTF16BENoBOM.Family() != UTF16BE || Unknown.Family() != "" || Big5.Family() != Big5 {
		t.Error("Family 不对")
	}
}

// 不是 UTF-8 的中日韩文字：长的按假名、谚文（和常用音节）、第二字节挑；短的（非 ASCII 少于 8 个）按 GB18030、Big5…取第一个解得通的。
func TestDetectCJK(t *testing.T) {
	code := func(s string) string { return "package x\n\n// " + s + "\nfunc f() {}\n" }
	for _, c := range []struct {
		name string
		data []byte
		text string
		enc  Encoding
	}{
		{"简体", gb(t, zhsText), zhsText, GB18030},
		{"简体（GBK 扩展字）", gb(t, zhxText), zhxText, GB18030},
		{"简体代码", gb(t, code(zhsText)), code(zhsText), GB18030},
		{"繁体", encodeWith(t, traditionalchinese.Big5, zhtText), zhtText, Big5},
		{"繁体代码", encodeWith(t, traditionalchinese.Big5, code(zhtText)), code(zhtText), Big5},
		{"GBK 里的繁体", gb(t, zhtText), zhtText, GB18030},
		{"日文", encodeWith(t, japanese.ShiftJIS, jaText), jaText, ShiftJIS},
		{"韩文", encodeWith(t, korean.EUCKR, koText), koText, EUCKR},
		{"短的", gb(t, "// 中文\n"), "// 中文\n", GB18030},
		// 短的 Big5 也先按 GB18030（A4A4 在 GB18030 里是假名），会认错，手动换
		{"短的 Big5", encodeWith(t, traditionalchinese.Big5, "中文"), decodeWith(t, simplifiedchinese.GB18030, encodeWith(t, traditionalchinese.Big5, "中文")), GB18030},
	} {
		text, enc, _ := Decode(c.data)
		if enc != c.enc || text != c.text {
			t.Errorf("%s：认成 %q %q，应为 %q", c.name, enc, text, c.enc)
		}
	}
}

// 本项目的中文界面字符串按 GB18030 编，切成 1、3、10、40 行一段，都要认成 GB18030：GB2312 的一级字多半落在 EUC-KR 的谚文区，
// 短的一段能有 100% 的"谚文"（koCommon 那条防的就是这个）。
func TestDetectCorpus(t *testing.T) {
	raw, err := os.ReadFile("../i18n/zh-CN.json")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for _, size := range []int{1, 3, 10, 40} {
		var n, wrong int
		for i := 0; i+size <= len(lines); i += size {
			chunk := strings.Join(lines[i:i+size], "\n")
			data := gb(t, chunk)
			if utf8.Valid(data) {
				continue
			}
			n++
			if text, enc, _ := Decode(data); enc != GB18030 || text != chunk {
				if wrong++; wrong <= 3 {
					t.Errorf("%d 行一段：%q 认成 %q", size, chunk, enc)
				}
			}
		}
		if n < 30 && size < 40 || wrong > 0 {
			t.Errorf("%d 行一段：%d 段错了 %d 段", size, n, wrong)
		}
	}
}

// 超过 64 KB 的只拿前 64 KB 挑（切在 ASCII 后面，不切开字）；挑定的后面解不通，试下一个，都不行是未知。
func TestDetectSample(t *testing.T) {
	long := strings.Repeat(zhsText, 1+cjkSample/len(gb(t, zhsText))) // 没有换行，64 KB 那里正好在字中间也没关系
	data := gb(t, long+"\n")
	if len(data) <= cjkSample {
		t.Fatalf("样本只有 %d 字节", len(data))
	}
	if text, enc, editable := Decode(data); enc != GB18030 || text != long+"\n" || !editable {
		t.Errorf("长的：%q 能编辑 %v", enc, editable)
	}
	if text, enc, _ := Decode(append(data, 0xFF)); enc != Unknown || text != string(data)+"\xFF" {
		t.Errorf("后面坏了：认成 %q", enc)
	}
}

// 没有 BOM 的 UTF-16：ASCII 多的一边全是 NUL；只读。二进制（PNG 头、可执行文件、随机字节、小整数数组）不能认成它。
func TestDetectUTF16NoBOM(t *testing.T) {
	src := "package x\n\n// 读配置文件\nfunc f() int { return 1 }\n"
	for _, c := range []struct {
		order unicode.Endianness
		enc   Encoding
	}{{unicode.LittleEndian, UTF16LENoBOM}, {unicode.BigEndian, UTF16BENoBOM}} {
		data := encodeWith(t, unicode.UTF16(c.order, unicode.IgnoreBOM), src)
		if text, enc, editable := Decode(data); enc != c.enc || text != src || editable {
			t.Errorf("%s：认成 %q 能编辑 %v，%q", c.enc, enc, editable, text)
		}
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0, 0, 0, 0x0D, 'I', 'H', 'D', 'R', 0, 0, 1, 0, 0, 0, 1, 0, 8, 6, 0, 0, 0}
	exe, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	rnd := make([]byte, 64<<10)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range rnd {
		rnd[i] = byte(r.Uint32())
	}
	var ints []byte
	for i := range 2000 {
		ints = append(ints, byte(i%7), 0) // 0–6 的 uint16 小端
	}
	for name, data := range map[string][]byte{"PNG": png, "可执行文件": exe[:min(len(exe), 64<<10)], "随机": rnd, "小整数": ints} {
		if _, enc, _ := Decode(data); enc != Binary {
			t.Errorf("%s 认成了 %q", name, enc)
		}
	}
}

// 读进来、改一个字、按原来的编码写回：BOM、UTF-16 的端序、GB18030 都在。
func TestEncodeRoundTrip(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	for _, c := range []struct {
		name string
		data []byte
		enc  Encoding
	}{
		{"bom.txt", append([]byte{0xEF, 0xBB, 0xBF}, "x\n"...), UTF8BOM},
		{"le.txt", []byte{0xFF, 0xFE, 'x', 0, '\n', 0}, UTF16LE},
		{"be.txt", []byte{0xFE, 0xFF, 0, 'x', 0, '\n'}, UTF16BE},
		{"crlf.txt", []byte("x\r\n"), UTF8},
		{"gb.txt", gb(t, "中文\n"), GB18030},
	} {
		os.WriteFile(filepath.Join(root, c.name), c.data, 0o644)
		d := NewFile(root, c.name)
		open(t, d)
		if d.Enc != c.enc || !d.Editable() {
			t.Errorf("%s：读成 %q，能编辑 %v", c.name, d.Enc, d.Editable())
			continue
		}
		if err := d.Write(); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(d.Abs); !bytes.Equal(got, c.data) {
			t.Errorf("%s：原样写回成了 % x，应为 % x", c.name, got, c.data)
		}
		if got := Encode("y\n", c.enc); c.enc != UTF8 && c.enc != GB18030 {
			text, enc, _ := Decode(got)
			if text != "y\n" || enc != c.enc {
				t.Errorf("%s：Encode 再 Decode 成了 %q %q", c.name, text, enc)
			}
		}
	}
	if Encode("x", Unknown) != nil || Encode("x", UTF16LENoBOM) != nil {
		t.Error("只读的编码不该编码")
	}
}

// GB18030 能编辑：改了存回还是 GB18030，和整份重新编一遍逐字节一样；BOM（留在文字里）、CRLF、GBK 扩展字都在；
// 混着换行的也能编辑。
func TestGB18030Edit(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	text := "\uFEFF" + strings.ReplaceAll(zhxText+"\n第二行 ascii\n", "\n", "\r\n")
	os.WriteFile(filepath.Join(root, "gb.txt"), gb(t, text), 0o644)
	d := NewFile(root, "gb.txt")
	open(t, d)
	if d.Enc != GB18030 || !d.Editable() || d.St.ReadOnly || d.Lossy || !strings.HasPrefix(d.Buf.String(), "\uFEFF王堃") {
		t.Fatalf("GB18030：%q 能编辑 %v，%q", d.Enc, d.Editable(), d.Buf.String())
	}
	d.Buf.Edit(editor.Edit{Start: len("\uFEFF"), End: len("\uFEFF"), Text: "改："})
	if err := d.Write(); err != nil {
		t.Fatal(err)
	}
	want := gb(t, strings.Replace(text, "王", "改：王", 1))
	if got, _ := os.ReadFile(d.Abs); !bytes.Equal(got, want) || !bytes.HasPrefix(got, []byte{0x84, 0x31, 0x95, 0x33}) {
		t.Errorf("存回成了 % x…", got[:min(len(got), 16)])
	}
	open(t, d)
	if d.Enc != GB18030 || !d.Editable() || d.Modified() {
		t.Errorf("再读：%q 能编辑 %v", d.Enc, d.Editable())
	}

	os.WriteFile(filepath.Join(root, "mixed.txt"), gb(t, "一\r\n二\n三\r\n"), 0o644)
	m := NewFile(root, "mixed.txt")
	open(t, m)
	if m.Enc != GB18030 || !m.Editable() || m.Lossy {
		t.Errorf("混着换行的：%q 能编辑 %v", m.Enc, m.Editable())
	}
}

// 编不回原样的 GB18030（单独的 0x80）只读，上面说明；转成 UTF-8 能转（按读出来的 €），之后能编辑。
// 认不出的不能转。写的时候编不了的一个字节都不碰。
func TestConvertUTF8(t *testing.T) {
	root, _ := testutil.TempRepo(t)
	data := append(gb(t, "中文\r\n第二行"), 0x80, '\r', '\n')
	os.WriteFile(filepath.Join(root, "gb.txt"), data, 0o644)
	d := NewFile(root, "gb.txt")
	open(t, d)
	if d.Enc != GB18030 || !d.Lossy || d.Editable() || !d.St.ReadOnly || !d.Convertible() || d.Buf.String() != "中文\n第二行€\n" {
		t.Fatalf("GB18030：%q lossy %v 能编辑 %v，%q", d.Enc, d.Lossy, d.Editable(), d.Buf.String())
	}
	if err := d.Write(); err == nil {
		t.Error("编不回原样的 GB18030 文件不该能保存")
	}
	if got, _ := os.ReadFile(d.Abs); !bytes.Equal(got, data) {
		t.Error("没保存成也把文件改了")
	}
	if err := d.ConvertUTF8(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(d.Abs); string(got) != "中文\r\n第二行€\r\n" {
		t.Errorf("转完盘上是 %q", got)
	}
	if d.Enc != UTF8 || d.Lossy || !d.Editable() || d.St.ReadOnly || d.Modified() {
		t.Errorf("转完：%q 能编辑 %v 改过 %v", d.Enc, d.Editable(), d.Modified())
	}
	open(t, d) // 再读一遍：认成 UTF-8
	if d.Enc != UTF8 {
		t.Errorf("再读成了 %q", d.Enc)
	}

	os.WriteFile(filepath.Join(root, "bad.txt"), []byte{0x80, 0x80, 0xFF}, 0o644)
	bad := NewFile(root, "bad.txt")
	open(t, bad)
	if bad.Enc != Unknown || bad.Editable() || bad.Convertible() || bad.ConvertUTF8() == nil {
		t.Errorf("认不出的：%q 能编辑 %v", bad.Enc, bad.Editable())
	}
	if got, _ := os.ReadFile(bad.Abs); !bytes.Equal(got, []byte{0x80, 0x80, 0xFF}) {
		t.Error("认不出的文件被改了")
	}

	// 保存前再防一手：编码编不了（这里硬塞一个只读的编码）就报错，文件不动
	os.WriteFile(filepath.Join(root, "u.txt"), []byte("x\n"), 0o644)
	u := NewFile(root, "u.txt")
	open(t, u)
	if err := u.write(UTF16LENoBOM); err == nil || !strings.Contains(err.Error(), "不能按 UTF-16 LE（无 BOM） 保存") {
		t.Errorf("按没有 BOM 的 UTF-16 写：%v", err)
	}
	if got, _ := os.ReadFile(u.Abs); string(got) != "x\n" {
		t.Errorf("编不了还是写了：%q", got)
	}
}

// 手动换编码：按选的解（UTF-8 / UTF-16 带不带 BOM 按文件自己的），解不通的照样打开、只读、标 Broken；"" 回到自动认。
// Big5 能编辑，也能转成 UTF-8。
func TestReadFileAs(t *testing.T) {
	r := testutil.NewRepo(t, "enc")
	big5 := encodeWith(t, traditionalchinese.Big5, zhtText+"\n")
	write := func(name string, data []byte) string {
		p := filepath.Join(r.Root, name)
		os.WriteFile(p, data, 0o644)
		return p
	}
	tw := write("tw.txt", big5)
	write("le.txt", []byte{0xFF, 0xFE, 'h', 0, 'i', 0})
	write("bare.txt", []byte{'h', 0, 'i', 0})
	write("bin.dat", []byte{1, 0, 2, 0x80})
	write("gb.txt", gb(t, "中文\n"))
	r.Commit("几种编码")

	for _, c := range []struct {
		name   string
		as     Encoding
		enc    Encoding
		broken bool
		text   string
	}{
		{"tw.txt", "", Big5, false, zhtText + "\n"},
		{"tw.txt", GB18030, GB18030, true, ""},
		{"tw.txt", UTF8, UTF8, true, string(big5)},
		{"le.txt", UTF16LE, UTF16LE, false, "hi"},
		{"le.txt", UTF16BE, UTF16BENoBOM, false, ""}, // FF FE 不是 BE 的 BOM：按没有 BOM 的 BE 解
		{"bare.txt", UTF16LE, UTF16LENoBOM, false, "hi"},
		{"bin.dat", ShiftJIS, Binary, false, ""},
		{"gb.txt", ShiftJIS, ShiftJIS, false, "ﾖﾐﾎﾄ\n"}, // 解得通的照样是乱码：D6 D0 CE C4 是四个半角片假名
	} {
		l := ReadFileAs(filepath.Join(r.Root, c.name), r.Root, c.name, true, c.as)
		if l.Enc != c.enc || l.Broken != c.broken || c.text != "" && l.Text != c.text {
			t.Errorf("%s 按 %q：%q broken %v %q", c.name, c.as, l.Enc, l.Broken, l.Text)
		}
		if l.Editable && (c.broken || !c.enc.Writable()) {
			t.Errorf("%s 按 %q 不该能编辑", c.name, c.as)
		}
	}

	d := NewFile(r.Root, "tw.txt")
	open(t, d)
	if d.Enc != Big5 || !d.Editable() || !d.Convertible() { // Big5 能编辑，也能转成 UTF-8
		t.Fatalf("Big5：%q 能编辑 %v", d.Enc, d.Editable())
	}
	if d.Base == nil || d.Base.String() != d.Buf.String() {
		t.Error("HEAD 版本没按 Big5 解")
	}
	if err := d.ConvertUTF8(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(tw); string(got) != zhtText+"\n" || !d.Editable() {
		t.Errorf("转完：%q", got)
	}
}

// HEAD 版本按文件的编码解（短的自己认会认成别的）；按它解不通的自动认。
func TestBaseBufferAs(t *testing.T) {
	short := encodeWith(t, traditionalchinese.Big5, "中文\n")
	if got := BaseBuffer(bytes.Clone(short)).String(); got == "中文\n" {
		t.Fatal("这个样本自己认就认对了，测不出来")
	}
	if got := BaseBufferAs(bytes.Clone(short), Big5).String(); got != "中文\n" {
		t.Errorf("按 Big5：%q", got)
	}
	if got := BaseBufferAs([]byte("ascii 和中文\n"), ShiftJIS).String(); got != "ascii 和中文\n" {
		t.Errorf("按 Shift-JIS 解不通的应自动认：%q", got)
	}
	if got := BaseBufferAs([]byte{1, 0, 2}, GB18030).String(); got != "\x01\x00\x02" {
		t.Errorf("二进制：%q", got)
	}
}

// 大文件（超过 8 MB）的 GB18030 不比对（照旧只读），手动换编码也只读。
func TestBigGB18030(t *testing.T) {
	unit := "// 一行中文注释\nfunc f() {}\n"
	data := gb(t, strings.Repeat(unit, MaxEditBytes/len(gb(t, unit))+1))
	var l Loaded
	l.decode(bytes.Clone(data), true, "")
	if !l.Big || l.Enc != GB18030 || l.Editable || l.Lossy || l.Buf == nil {
		t.Errorf("大的 GB18030：big %v %q 能编辑 %v lossy %v", l.Big, l.Enc, l.Editable, l.Lossy)
	}
	l = Loaded{}
	l.decode(bytes.Clone(data), true, Big5)
	if !l.Big || l.Enc != Big5 || l.Editable {
		t.Errorf("大文件按 Big5：%q 能编辑 %v", l.Enc, l.Editable)
	}
}

// 量：6 MB 的 GB18030 从读到能编辑，以前（只解码）和现在（前 64 KB 四种都试 + 解码 + 编回去比一遍）各多久。
// WORKBENCH_MEASURE=1 才跑。
func TestMeasureGB18030(t *testing.T) {
	if os.Getenv("WORKBENCH_MEASURE") == "" {
		t.Skip("WORKBENCH_MEASURE=1 才量")
	}
	unit := "\t// " + zhsText + "\n\tif err := load(path); err != nil {\n\t\treturn err\n\t}\n"
	data := gb(t, strings.Repeat(unit, 6<<20/len(gb(t, unit))+1))
	old := func() {
		if !utf8.Valid(data) {
			s, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
			_ = err == nil && !bytes.ContainsRune(s, utf8.RuneError) && len(string(s)) > 0
		}
	}
	nw := func() {
		if r := decode(data, "", false, true); r.Enc != GB18030 || !r.Editable {
			t.Fatalf("认成 %q 能编辑 %v", r.Enc, r.Editable)
		}
	}
	median := func(f func()) time.Duration {
		var ds []time.Duration
		for range 7 {
			t0 := time.Now()
			f()
			ds = append(ds, time.Since(t0))
		}
		slices.Sort(ds)
		return ds[len(ds)/2]
	}
	o, n := median(old), median(nw)
	t.Logf("%.1f MB GB18030：以前 %v，现在 %v，多了 %v", float64(len(data))/(1<<20), o, n, n-o)
}

// kanaMost、hangulMost 是上限：每个双字节都解一遍，解得通的是一个非 ASCII 的字，全角假名只在 81–83 开头的里、谚文只在 hangulPair 里。
func TestKanaHangulMost(t *testing.T) {
	one := func(enc Encoding, b ...byte) (rune, bool) {
		out, err := enc.codec().NewDecoder().Bytes(b)
		if err != nil || bytes.ContainsRune(out, utf8.RuneError) {
			return 0, false
		}
		r, size := utf8.DecodeRune(out)
		if size != len(out) {
			t.Fatalf("%s % X 解出 %q", enc, b, out)
		}
		return r, true
	}
	for c0 := 0x80; c0 <= 0xff; c0++ {
		if r, ok := one(ShiftJIS, byte(c0)); ok && (r < 0x80 || isKana(r)) {
			t.Errorf("Shift-JIS %X → %U", c0, r)
		}
		for c1 := 0x40; c1 <= 0xff; c1++ {
			if c0 >= 0x81 && c0 < 0xa0 || c0 >= 0xe0 && c0 < 0xfd {
				if r, ok := one(ShiftJIS, byte(c0), byte(c1)); ok && (r < 0x80 || isKana(r) && c0 > 0x83) {
					t.Errorf("Shift-JIS %X%X → %U", c0, c1, r)
				}
			}
			if r, ok := one(EUCKR, byte(c0), byte(c1)); ok && (r < 0x80 || isHangul(r) && !hangulPair(byte(c0), byte(c1))) {
				t.Errorf("EUC-KR %X%X → %U", c0, c1, r)
			}
		}
	}
}

// 跳过挑不上的和每种都解一遍挑出来的一样：界面字符串、几种样本随便切开拼起来、随机字节、超过样本长度的。
func TestPickSkip(t *testing.T) {
	var cases [][]byte
	raw, err := os.ReadFile("../i18n/zh-CN.json")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for _, size := range []int{1, 3, 10, 40} {
		for i := 0; i+size <= len(lines); i += size {
			cases = append(cases, gb(t, strings.Join(lines[i:i+size], "\n")))
		}
	}
	pieces := [][]byte{gb(t, zhsText), gb(t, zhxText), gb(t, zhtText), gb(t, jaText), gb(t, koText),
		encodeWith(t, traditionalchinese.Big5, zhtText), encodeWith(t, japanese.ShiftJIS, jaText), encodeWith(t, korean.EUCKR, koText),
		encodeWith(t, japanese.ShiftJIS, "ｾｯﾃｲ ﾌｧｲﾙ")}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 5000 {
		var b []byte
		for range 1 + rng.IntN(4) {
			p := pieces[rng.IntN(len(pieces))]
			i := rng.IntN(len(p))
			b = append(append(b, p[i:i+rng.IntN(len(p)-i+1)]...), " \n"[rng.IntN(2)])
		}
		cases = append(cases, b)
	}
	for range 2000 {
		b := make([]byte, 1+rng.IntN(64))
		for i := range b {
			b[i] = byte(0x80 + rng.IntN(0x80))
			if rng.IntN(4) == 0 {
				b[i] = byte(rng.IntN(0x80))
			}
		}
		cases = append(cases, b)
	}
	long := bytes.Repeat(gb(t, zhsText+"\n"), 1+cjkSample/len(gb(t, zhsText)))
	for _, tail := range [][]byte{{0x80}, {0xff}, pieces[5], pieces[6], pieces[7]} {
		cases = append(cases, append(slices.Clip(long), tail...))
	}
	for _, p := range pieces[5:8] {
		cases = append(cases, append(bytes.Repeat(p, 1+cjkSample/len(p)), 0x80))
	}
	for _, c := range cases {
		text, enc := pickFrom(c, true)
		if want, wenc := pickFrom(c, false); enc != wenc || text != want {
			t.Fatalf("% X：跳过的认成 %q，每种都解认成 %q", c, enc, wenc)
		}
	}
}
