package docs

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/lemoeade/grove/internal/git"
	"github.com/lemoeade/grove/internal/i18n"
)

// Encoding 是文件的编码：读的时候认出来（Decode），保存按同样的写回（Encode）。
type Encoding string

const (
	UTF8    Encoding = "UTF-8"
	UTF8BOM Encoding = "UTF-8 BOM"
	UTF16LE Encoding = "UTF-16 LE"
	UTF16BE Encoding = "UTF-16 BE"
	// 这几种是老的单字节、双字节编码（legacy）：编回去和原来逐字节一样才能编辑（见 Doc.Lossy）
	GB18030     Encoding = "GB18030" // GBK 的超集
	Big5        Encoding = "Big5"
	ShiftJIS    Encoding = "Shift_JIS"
	EUCKR       Encoding = "EUC-KR"
	EUCJP       Encoding = "EUC-JP"       // 只能手动选，不自动认
	Windows1252 Encoding = "Windows-1252" // 同上
	ISO88591    Encoding = "ISO-8859-1"   // 同上
	// 没有 BOM 的 UTF-16 只读，可以转成 UTF-8
	UTF16LENoBOM Encoding = "UTF-16 LE（无 BOM）"
	UTF16BENoBOM Encoding = "UTF-16 BE（无 BOM）"

	Unknown Encoding = "未知"  // 不是 UTF-8 也解不通：原样显示，只读
	Binary  Encoding = "二进制" // 有 NUL：不进编辑器
)

// ChoiceGroups 是"用别的编码重新打开"里列的编码，一组之间画分隔线（UTF-8 带不带 BOM、UTF-16 带不带 BOM 按文件自己的）。
var ChoiceGroups = [][]Encoding{{UTF8}, {GB18030, Big5, ShiftJIS, EUCKR, EUCJP}, {Windows1252, ISO88591}, {UTF16LE, UTF16BE}}

// Choices 是 ChoiceGroups 摊平。
var Choices = slices.Concat(ChoiceGroups...)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// Label 是状态栏里的写法。
func (e Encoding) Label() string {
	switch e {
	case "":
		return string(UTF8)
	case Unknown, UTF16LENoBOM, UTF16BENoBOM:
		return i18n.T(string(e))
	}
	return string(e)
}

// Writable 是这种编码能不能原样写回（没认过的当 UTF-8）。老编码（legacy）还要看文件里的字节能不能来回（Doc.Lossy）。
func (e Encoding) Writable() bool {
	switch e {
	case "", UTF8, UTF8BOM, UTF16LE, UTF16BE:
		return true
	}
	return e.legacy()
}

// legacy 是老的单字节、双字节编码：能写回，但要编回去和原来逐字节一样才能编辑，有的字写不进去（Unencodable）。
func (e Encoding) legacy() bool {
	switch e {
	case GB18030, Big5, ShiftJIS, EUCKR, EUCJP, Windows1252, ISO88591:
		return true
	}
	return false
}

// Family 是 e 在 Choices 里对应的那一项：带 BOM 的 UTF-8 算 UTF-8，没有 BOM 的 UTF-16 算 UTF-16；不在里面的为 ""。
func (e Encoding) Family() Encoding {
	switch e {
	case UTF8BOM:
		return UTF8
	case UTF16LENoBOM:
		return UTF16LE
	case UTF16BENoBOM:
		return UTF16BE
	}
	if slices.Contains(Choices, e) {
		return e
	}
	return ""
}

// Convertible 是能不能"转成 UTF-8…"：不是 UTF-8 的文本编码（二进制、认不出的除外）。
func (e Encoding) Convertible() bool {
	switch e {
	case "", UTF8, UTF8BOM, Unknown, Binary:
		return false
	}
	return true
}

// codec 是 x/text 里对应的编解码器；UTF-8、未知、二进制为 nil。
func (e Encoding) codec() encoding.Encoding {
	switch e {
	case GB18030:
		return simplifiedchinese.GB18030
	case Big5:
		return traditionalchinese.Big5
	case ShiftJIS:
		return japanese.ShiftJIS
	case EUCKR:
		return korean.EUCKR
	case EUCJP:
		return japanese.EUCJP
	case Windows1252:
		return charmap.Windows1252
	case ISO88591:
		return charmap.ISO8859_1
	case UTF16LE:
		return unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM)
	case UTF16BE:
		return unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM)
	case UTF16LENoBOM:
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	case UTF16BENoBOM:
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	}
	return nil
}

// Decoded 是解码的结果。
type Decoded struct {
	Text     string
	Enc      Encoding
	Editable bool // 能原样写回：编码可写；老编码（legacy）还要编回去和原来逐字节一样（check 时才比，没比的不算能编辑）
	Lossy    bool // 老编码解得通、可编回去和原来的字节不一样（GB18030、Shift-JIS 里单独的 0x80 之类）：只读
	Broken   bool // 手动选的编码有解不出来的字节（结果里有替换字符）：只读
}

// Decode 把文件内容自动认编码、解成 UTF-8 的文字：带 BOM 的 UTF-8 / UTF-16 去掉 BOM，没有 BOM 的 UTF-16 看奇偶位的 NUL，
// 有 NUL 的是二进制，合法的 UTF-8 原样，不然在 GB18030、Big5、Shift-JIS、EUC-KR 里挑（pickCJK），都不行是未知
// （原样当 UTF-8，只读）。老编码编回去和原来逐字节一样才算能编辑。
func Decode(data []byte) (text string, enc Encoding, editable bool) {
	r := decode(data, "", false, true)
	return r.Text, r.Enc, r.Editable
}

// Sniff 和 Decode 一样自动认编码、解成 UTF-8 的文字，只是老编码不编回去比对（不管能不能写回）：搜索用，
// 搜的是编辑器里会看到的文字，位置就是缓冲区里的位置。二进制的 text 为空，认不出的（Unknown）原样。
func Sniff(data []byte) (text string, enc Encoding) {
	r := decode(data, "", false, false)
	return r.Text, r.Enc
}

// decode 解 data：want 为 "" 时自动认，不然按 want（"用别的编码重新打开"，UTF-8 和 UTF-16 带不带 BOM 按文件自己的）；
// owned 时拿走 data：原样的 UTF-8 / 认不出的直接借用这块内存当字符串，不再拷一份（64 MB 的文件少一份 64 MB），
// 调了之后谁都不能再改 data；check 时老编码编回去逐字节比一遍（GB18030 6 MB 约 8 ms，在后台），不比的不算能编辑。
func decode(data []byte, want Encoding, owned, check bool) Decoded {
	str := func(b []byte) string {
		if owned && len(b) > 0 {
			return unsafe.String(unsafe.SliceData(b), len(b))
		}
		return string(b)
	}
	var r Decoded
	if want != "" {
		r = decodeAs(data, want, str)
	} else {
		r = detect(data, str)
	}
	switch {
	case r.Enc.legacy() && !r.Broken && check:
		r.Lossy = !bytes.Equal(Encode(r.Text, r.Enc), data)
		r.Editable = !r.Lossy
	case r.Enc.legacy():
		r.Editable = false
	default:
		r.Editable = r.Enc.Writable() && !r.Broken
	}
	return r
}

// detect 自动认编码（见 Decode）。
func detect(data []byte, str func([]byte) string) Decoded {
	for _, enc := range []Encoding{UTF16LE, UTF16BE} {
		bom := bomUTF16LE
		if enc == UTF16BE {
			bom = bomUTF16BE
		}
		if bytes.HasPrefix(data, bom) {
			if s, err := enc.codec().NewDecoder().Bytes(data); err == nil {
				return Decoded{Text: string(s), Enc: enc}
			}
		}
	}
	if enc := sniffUTF16(data); enc != "" {
		if s, ok := clean(enc, data); ok && !badControl(s) {
			return Decoded{Text: s, Enc: enc}
		}
	}
	switch {
	case git.IsBinary(data):
		return Decoded{Enc: Binary}
	case bytes.HasPrefix(data, bomUTF8):
		return Decoded{Text: str(data[len(bomUTF8):]), Enc: UTF8BOM}
	case utf8.Valid(data):
		return Decoded{Text: str(data), Enc: UTF8}
	}
	if s, enc := pickCJK(data); enc != "" {
		return Decoded{Text: s, Enc: enc}
	}
	return Decoded{Text: str(data), Enc: Unknown}
}

// decodeAs 按手动选的 want 解 data（UTF-8 和 UTF-16 带不带 BOM 按文件自己的）：解不通的照样给出结果
// （替换字符；UTF-8 原样），标 Broken。有 NUL 的除了按 UTF-16 都是二进制。
func decodeAs(data []byte, want Encoding, str func([]byte) string) Decoded {
	switch f := want.Family(); f {
	case "":
		return detect(data, str)
	case UTF16LE, UTF16BE:
		bom, bare := bomUTF16LE, UTF16LENoBOM
		if f == UTF16BE {
			bom, bare = bomUTF16BE, UTF16BENoBOM
		}
		if want = f; !bytes.HasPrefix(data, bom) {
			want = bare
		}
	default:
		if git.IsBinary(data) {
			return Decoded{Enc: Binary}
		}
		if f == UTF8 {
			broken := !utf8.Valid(data)
			if bytes.HasPrefix(data, bomUTF8) {
				return Decoded{Text: str(data[len(bomUTF8):]), Enc: UTF8BOM, Broken: broken}
			}
			return Decoded{Text: str(data), Enc: UTF8, Broken: broken}
		}
	}
	s, err := want.codec().NewDecoder().Bytes(data)
	return Decoded{Text: string(s), Enc: want, Broken: err != nil || bytes.ContainsRune(s, utf8.RuneError)}
}

// clean 按 enc 解 data，解得通、结果里没有替换字符才 ok。
func clean(enc Encoding, data []byte) (string, bool) {
	b, ok := cleanBytes(enc, data)
	if !ok {
		return "", false
	}
	return string(b), true
}

// cleanChunk 是 cleanBytes 每一段最多解出来的字节。
const cleanChunk = 4 << 10

// cleanBytes 是 clean 的 []byte 版：一段一段地解，一出现替换字符就停。pickCJK 挨个试的时候，解不通的多半在前几 KB 就露馅，
// 不用解完（搜索要给每个不是 UTF-8 的文件认编码，量 10 KB 的中文，四种都试从约 157 µs 降到约 99 µs；再加上 pickFrom 的跳过约 34 µs）。
func cleanBytes(enc Encoding, data []byte) ([]byte, bool) {
	dec := enc.codec().NewDecoder()
	out := make([]byte, 0, len(data)+len(data)/2+cleanChunk)
	for {
		out = slices.Grow(out, cleanChunk)
		dst := out[len(out) : len(out)+cleanChunk]
		n, m, err := dec.Transform(dst, data, true)
		if bytes.ContainsRune(dst[:n], utf8.RuneError) {
			return nil, false
		}
		out, data = out[:len(out)+n], data[m:]
		switch {
		case err == nil:
			return out, true
		case err != transform.ErrShortDst:
			return nil, false
		}
	}
}

// sniffUTF16 看前 4 KB 的奇偶位：ASCII 多的 UTF-16 一半字节是 NUL，而且都在同一边（LE 在奇数位、BE 在偶数位）；
// 二进制文件两边都有很多 NUL，不会进来。只是猜，解出来还要干净（clean、badControl）。
func sniffUTF16(data []byte) Encoding {
	n := min(len(data), 4096) &^ 1
	if n < 2 {
		return ""
	}
	var even, odd int
	for i := 0; i < n; i += 2 {
		if data[i] == 0 {
			even++
		}
		if data[i+1] == 0 {
			odd++
		}
	}
	pairs := n / 2
	switch {
	case odd*100 >= pairs*utf16NulMin && even*100 <= pairs*utf16NulMax:
		return UTF16LENoBOM
	case even*100 >= pairs*utf16NulMin && odd*100 <= pairs*utf16NulMax:
		return UTF16BENoBOM
	}
	return ""
}

// badControl 是 s 里有 \t \n \r \f ESC 以外的 C0 控制字符（包括 NUL）：猜成 UTF-16 的二进制文件解出来常有。
func badControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f' && c != 0x1b {
			return true
		}
	}
	return false
}

// 认编码的阈值（百分比），用 encoding_test.go 的样本调定：
//   - 没有 BOM 的 UTF-16：一边的 NUL ≥ utf16NulMin、另一边 ≤ utf16NulMax；
//   - 不是 UTF-8 时只看前 cjkSample 字节；非 ASCII 的字少于 cjkMinRunes 个不打分，按 cjk 的顺序取第一个解得通的；
//   - 按 Shift-JIS 解出来的非 ASCII 字里（全角）假名 ≥ kanaMin 是 Shift-JIS；
//   - 按 EUC-KR 解出来的谚文 ≥ hangulMin、而且这些谚文里 koCommon 的常用音节 ≥ koCommonMin 是 EUC-KR
//     （GB2312 的一级字多半落在 EUC-KR 的谚文区，短的中文能有 100% 的"谚文"，可都是生僻音节，样本里最多 30%，韩文 41–77%）；
//   - GB18030 和 Big5 都解得通时，双字节字的第二个字节在 40–7E 的 ≥ big5LowMin 是 Big5（GB2312 的字第二个字节都 ≥ A1）。
const (
	utf16NulMin = 40
	utf16NulMax = 5
	cjkSample   = 64 << 10
	cjkMinRunes = 8
	kanaMin     = 20
	hangulMin   = 85
	koCommonMin = 35
	big5LowMin  = 15
)

// koCommon 是韩文里最常见的一百来个音节（助词、词尾、常用词），见 hangulMin 那条。
const koCommon = "이다의는에을를하가고지기로한서사자리니어도인대정수일시나아해그들있것으게면습요했원되우주보소거부만와과전상문은제국학장생성경방동연실위여개비내무마오조공진분모회화구세계신적행저속반할던까라데야합입없및또때더각중후년파일오류함"

// cjk 是不是 UTF-8 时挑的几种，也是短文件时的先后。
var cjk = []Encoding{GB18030, Big5, ShiftJIS, EUCKR}

// pickCJK 在 cjk 里挑 data 的编码：先拿前 cjkSample 字节（切在 0x30 以下的 ASCII 字节后面，四种编码的第二个字节都不会落在那里）
// 每种解一遍，留下解得通的；再按假名、谚文、第二字节的比例挑（见上面的阈值）；挑定的整份解一遍，后面解不通就试下一个。
// 都不行 enc 为 ""。
func pickCJK(data []byte) (string, Encoding) {
	return pickFrom(data, true)
}

// pickFrom 是 pickCJK；skip 时 GB18030 解得通以后，只看字节就知道挑不上的（cannotWin）不解，挑出来的和每种都解一遍一样
// （TestPickSkip 比对两种），中文的 GB18030 只解一遍（搜索要给每个不是 UTF-8 的文件认编码）。
// 没解的排在后面的先后照旧：样本是整份的开头、切在 ASCII 后面，整份解得通样本就解得通，后面直接整份试。
func pickFrom(data []byte, skip bool) (string, Encoding) {
	sample := data
	if len(sample) > cjkSample {
		sample = sample[:cjkSample]
		for i := len(sample) - 1; i > 0; i-- {
			if sample[i] < 0x30 {
				sample = sample[:i+1]
				break
			}
		}
	}
	var ok, skipped []Encoding
	texts := map[Encoding][]byte{}
	for _, enc := range cjk {
		if skip && len(ok) > 0 && ok[0] == GB18030 && cannotWin(enc, sample) {
			skipped = append(skipped, enc)
			continue
		}
		if s, good := cleanBytes(enc, sample); good {
			ok = append(ok, enc)
			texts[enc] = s
		}
	}
	if len(ok) == 0 {
		return "", ""
	}
	best := ok[0]
	if len(ok) > 1 && nonASCII(texts[ok[0]]) >= cjkMinRunes {
		best = score(sample, ok, texts)
	}
	order := []Encoding{best}
	for _, enc := range cjk {
		if _, has := texts[enc]; enc != best && (has || slices.Contains(skipped, enc)) {
			order = append(order, enc)
		}
	}
	for _, enc := range order {
		if s, has := texts[enc]; has && len(sample) == len(data) {
			return string(s), enc
		}
		if s, ok := clean(enc, data); ok {
			return s, enc
		}
	}
	return "", ""
}

// cannotWin 是 GB18030 解得通时 enc 在 score 里一定挑不上：Big5 的第二字节不够，Shift-JIS、EUC-KR 按字节估的假名、谚文上限不够。
// 上限是解得通的时候 share 不会超过的（kanaMost、hangulMost），估松了只是多解一遍。
func cannotWin(enc Encoding, sample []byte) bool {
	switch enc {
	case Big5:
		return big5Low(sample) < big5LowMin
	case ShiftJIS:
		return kanaMost(sample) < kanaMin
	case EUCKR:
		return hangulMost(sample) < hangulMin
	}
	return false
}

// kanaMost 是 data 按 Shift-JIS 解得通时 share(…, isKana) 的上限：照解码器的切法走（80、A1–DF 一个字节，81–9F、E0–FC 开头两个字节，
// 每段都解出一个非 ASCII 的字），全角假名只在 81–83 开头的里面（TestKanaHangulMost 把每个双字节都解一遍查过）。
func kanaMost(data []byte) int {
	var n, hit int
	for i := 0; i < len(data); {
		switch c := data[i]; {
		case c < 0x80:
			i++
			continue
		case c == 0x80 || c >= 0xa1 && c < 0xe0:
			i++
		default:
			if c <= 0x83 {
				hit++
			}
			i += 2
		}
		n++
	}
	if n == 0 {
		return 0
	}
	return hit * 100 / n
}

// hangulMost 是 data 按 EUC-KR 解得通时 share(…, isHangul) 的上限：非 ASCII 的都是两个字节一个字，谚文只在 hangulPair 的里面。
func hangulMost(data []byte) int {
	var n, hit int
	for i := 0; i < len(data); {
		if data[i] < 0x80 {
			i++
			continue
		}
		if i+1 < len(data) && hangulPair(data[i], data[i+1]) {
			hit++
		}
		n++
		i += 2
	}
	if n == 0 {
		return 0
	}
	return hit * 100 / n
}

// hangulPair 是 EUC-KR（CP949）里可能解出谚文的双字节：A0 以前开头的、第二字节在 A1 以前的（扩展的音节）、A4 行（字母）、B0–C8 行（音节）。
func hangulPair(c0, c1 byte) bool {
	return c0 <= 0xa0 || c1 < 0xa1 || c0 == 0xa4 || c0 >= 0xb0 && c0 <= 0xc8
}

// score 在都解得通的几种里挑一个（见阈值的说明）。
func score(sample []byte, ok []Encoding, texts map[Encoding][]byte) Encoding {
	if slices.Contains(ok, ShiftJIS) && share(texts[ShiftJIS], isKana) >= kanaMin {
		return ShiftJIS
	}
	if slices.Contains(ok, EUCKR) && share(texts[EUCKR], isHangul) >= hangulMin && koShare(texts[EUCKR]) >= koCommonMin {
		return EUCKR
	}
	gb, big5 := slices.Contains(ok, GB18030), slices.Contains(ok, Big5)
	switch {
	case gb && big5 && big5Low(sample) >= big5LowMin:
		return Big5
	case gb:
		return GB18030
	}
	return ok[0]
}

// nonASCII 是 s 里非 ASCII 的字有几个。
func nonASCII(s []byte) int {
	n := 0
	for _, r := range string(s) {
		if r >= 0x80 {
			n++
		}
	}
	return n
}

// share 是 s 的非 ASCII 字里 is 为真的占百分之几。
func share(s []byte, is func(rune) bool) int {
	var n, hit int
	for _, r := range string(s) {
		if r < 0x80 {
			continue
		}
		n++
		if is(r) {
			hit++
		}
	}
	if n == 0 {
		return 0
	}
	return hit * 100 / n
}

// koShare 是 s 的谚文音节里 koCommon 占百分之几。
func koShare(s []byte) int {
	var n, hit int
	for _, r := range string(s) {
		if r >= 0xAC00 && r <= 0xD7A3 {
			n++
			if strings.ContainsRune(koCommon, r) {
				hit++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return hit * 100 / n
}

// isKana 是全角的平假名、片假名：半角片假名不算，中文、韩文按 Shift-JIS 解出来全是半角片假名（A1–DF 是单字节的半角片假名）。
func isKana(r rune) bool {
	return r >= 0x3040 && r <= 0x30FF
}

// isHangul 是谚文音节和兼容字母。
func isHangul(r rune) bool {
	return r >= 0xAC00 && r <= 0xD7A3 || r >= 0x3131 && r <= 0x318E
}

// big5Low 是按双字节扫 data 时，第二个字节在 40–7E 的字占百分之几。
func big5Low(data []byte) int {
	var low, total int
	for i := 0; i < len(data); {
		if data[i] < 0x80 {
			i++
			continue
		}
		if i+1 < len(data) {
			total++
			if t := data[i+1]; t >= 0x40 && t <= 0x7E {
				low++
			}
		}
		i += 2
	}
	if total == 0 {
		return 0
	}
	return low * 100 / total
}

// Encode 把文字按 enc 编回字节：BOM 补回，UTF-16 按原来的端序带 BOM，老编码照编；UTF-8（和没认过的）原样。
// 只读的编码（没有 BOM 的 UTF-16、未知）、编不了的（老编码遇到写不进去的字，见 Unencodable），返回 nil。
func Encode(text string, enc Encoding) []byte {
	switch enc {
	case UTF8BOM:
		return append(bytes.Clone(bomUTF8), text...)
	case UTF16LE, UTF16BE:
		order := unicode.LittleEndian
		if enc == UTF16BE {
			order = unicode.BigEndian
		}
		b, err := unicode.UTF16(order, unicode.UseBOM).NewEncoder().Bytes([]byte(text))
		if err != nil {
			return nil
		}
		return b
	case "", UTF8:
		return []byte(text)
	}
	if !enc.legacy() {
		return nil
	}
	b, err := enc.codec().NewEncoder().Bytes([]byte(text))
	if err != nil {
		return nil
	}
	return b
}

// Unencodable 是 text 里按 enc 写不进去的字有几个（老编码才有，如 Shift_JIS 里的 emoji、ISO-8859-1 里的 €）。
// 不用 x/text 的 ReplaceUnsupported 数：它换成 0x1a，文件里本来就有 0x1a 时分不清。每个不同的非 ASCII 字只编一次。
func Unencodable(text string, enc Encoding) int {
	if !enc.legacy() {
		return 0
	}
	e := enc.codec().NewEncoder()
	ok := map[rune]bool{}
	n := 0
	for _, r := range text {
		if r < 0x80 {
			continue
		}
		good, seen := ok[r]
		if !seen {
			_, err := e.String(string(r))
			good = err == nil
			ok[r] = good
		}
		if !good {
			n++
		}
	}
	return n
}

// decodeBase 是 Diff 标签比内容用的文字：和文件一样解码（二进制的原样）。
func decodeBase(base []byte) string {
	if r := decode(base, "", false, false); r.Enc != Binary {
		return r.Text
	}
	return string(base)
}
