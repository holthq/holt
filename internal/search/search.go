// Package search 是跨仓库全文搜索：候选来自 git ls-files（已跟踪的加上没被忽略的未跟踪文件；不是 Git 仓库的
// 普通文件夹直接列目录），
// 每个 CPU 一个 worker，字面 / 整词 / 正则、区分大小写，包含 / 排除的 glob，可取消，
// 结果默认最多 MaxMatches 处（Query.Limit 往上加）。不是 UTF-8 的文件按编辑器认的编码解成文字再搜，位置都是解码后文字里的。不碰界面，可以在后台 goroutine 里调用；只读文件，不写任何东西。
// 正则是 Go 的 RE2；RE2 编不了、又用到反向引用 / 环视的，换 regexp2 的回溯引擎（advanced.go）。
package search

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/dlclark/regexp2/v2"

	"github.com/holthq/holt/internal/docs"
	"github.com/holthq/holt/internal/files"
	"github.com/holthq/holt/internal/git"
	"github.com/holthq/holt/internal/globs"
	"github.com/holthq/holt/internal/i18n"
)

const (
	// MaxMatches 是一次搜索默认最多收的结果；"再读 5000"一次加这么多（Query.Limit），最多 MaxLimit。
	MaxMatches = 5000
	// MaxLimit 是 Query.Limit 最大能到多少处。
	MaxLimit = 50000
	// MaxFileSize 以上的文件不搜（Query.Big 时放到 MaxBigFileSize）。
	MaxFileSize = 2 << 20
	// MaxBigFileSize 是开着"也搜 2 MB 以上的大文件"时的上限，再大的照旧跳过。
	MaxBigFileSize = 32 << 20
	// previewBefore / previewAfter 是结果预览里匹配前后最多留的字节（压缩过的 JS 一行能有几百 KB）。
	previewBefore = 60
	previewAfter  = 200
)

// Root 是要搜的一个仓库。Files 是它的文件列表（仓库内路径，斜杠分隔）；nil 就现跑一次 git ls-files，
// Plain（不是 Git 仓库的普通文件夹）就现列一次目录（files.Walk）。Sub 不空时只搜这个目录（仓库内路径）下面的。
type Root struct {
	Name  string
	Dir   string
	Files []string
	Plain bool
	Sub   string
	// Exclude 是文件树的 files.exclude：自己列的文件和被忽略的去掉合的（Files 是文件树给的就已经去掉了）
	Exclude globs.Set
}

// Query 是一次搜索的条件。Include / Exclude 是逗号分隔的 glob：不带 / 的比文件名和每一级目录名
// （`*.go`、`node_modules`），带 / 的比仓库内路径（也可以带仓库名开头），`**` 跨目录。
// Ignored 时连被 .gitignore 忽略的文件一起搜（Git 仓库才有），Big 时 2 MB 以上、MaxBigFileSize 以下的也搜。
type Query struct {
	Text          string
	CaseSensitive bool
	WholeWord     bool
	Regexp        bool
	Include       string
	Exclude       string
	Ignored       bool
	Big           bool
	// Limit 是最多收几处，0 是 MaxMatches（"再读 5000"往上加，最多 MaxLimit）。不算进最近搜索。
	Limit int
}

// MatchCap 是按 q 最多收几处。
func (q Query) MatchCap() int {
	if q.Limit > 0 {
		return min(q.Limit, MaxLimit)
	}
	return MaxMatches
}

// MaxSize 是按 q 搜的文件最大多少字节，再大的跳过（替换时也按它）。
func (q Query) MaxSize() int64 {
	if q.Big {
		return MaxBigFileSize
	}
	return MaxFileSize
}

// Match 是一处匹配。Line、Col 从 0 起，Col 和 Span 是解码后文字里的 UTF-8 字节（= 编辑器缓冲区里的位置，
// 开头的 BOM 不算）；Span 是整个匹配（替换用它），跨行时里面的 \r\n 有 CR 个（编辑器缓冲区里没有 \r：
// 选中、在缓冲区里核对按 BufLen）。Len 是 Preview 里从 PreviewCol 起标出来的长度：一行内的匹配就是它的字节数，
// 跨行的（多行搜索、正则）预览里换行画成 ⏎。
type Match struct {
	Repo       string // 仓库名
	Root       string // 仓库根
	Path       string // 仓库内路径
	Line       int
	Col        int
	Len        int
	Span       int
	CR         int
	Preview    string
	PreviewCol int
	Enc        docs.Encoding
}

// BufLen 是这一处在编辑器缓冲区里的长度（\r\n 只算 \n）：打开时选中它。
func (m Match) BufLen() int { return m.Span - m.CR }

// Result 是一次搜索的结果，按仓库、路径、行排好。
type Result struct {
	Matches   []Match
	Files     int  // 有匹配的文件数
	Searched  int  // 搜过的文件数
	Skipped   int  // 太大、二进制、读不了而跳过的
	Truncated bool // 到了 Query.MatchCap()，后面的没搜
	// IgnoredCut 是被忽略的文件太多（一个仓库超过 git.MaxIgnoredFiles 个），后面的没列、没搜
	IgnoredCut bool
	// TimedOut 是高级正则跑超时、当跳过的文件数（也算在 Skipped 里）
	TimedOut int
	// Advanced 是这次的正则走的 regexp2（摘要行末尾"· 高级正则"）
	Advanced bool
	Elapsed  time.Duration
	Err      error // 正则写错、ls-files 失败、被取消
}

// ErrEmpty 是没有要搜的文字。
var ErrEmpty = errors.New("没有要搜的文字")

// Search 在 roots 里搜 q。ctx 取消时尽快返回，Err 为 ctx.Err()。
func Search(ctx context.Context, roots []Root, q Query) Result {
	start := time.Now()
	res := run(ctx, roots, q)
	res.Elapsed = time.Since(start)
	return res
}

type job struct {
	root *Root
	path string
}

func run(ctx context.Context, roots []Root, q Query) Result {
	var res Result
	m, err := Compile(q)
	if err != nil {
		res.Err = err
		return res
	}
	res.Advanced = m.Advanced()
	inc, err := globs.Compile(q.Include)
	if err != nil {
		res.Err = err
		return res
	}
	exc, err := globs.Compile(q.Exclude)
	if err != nil {
		res.Err = err
		return res
	}
	var jobs []job
	for i := range roots {
		r := &roots[i]
		fail := func(err error) Result {
			res.Err = errors.New(r.Name + "：" + err.Error())
			if ctx.Err() != nil {
				res.Err = ctx.Err()
			}
			return res
		}
		if r.Files == nil {
			if r.Plain {
				r.Files, _, _, _, err = files.Walk(ctx, r.Dir) // node_modules、点开头的目录不进
			} else {
				r.Files, err = git.LsFiles(ctx, r.Dir)
			}
			if err != nil {
				return fail(err)
			}
			r.Files, _ = r.Exclude.Matcher().Filter(r.Files)
		}
		list := r.Files
		if q.Ignored && !r.Plain {
			// 文件树的列表里没有被忽略的：另列一遍接在后面（不改 r.Files，那可能是文件树的），下面排序后去重
			ign, cut, err := git.LsFilesIgnored(ctx, r.Dir)
			if err != nil {
				return fail(err)
			}
			res.IgnoredCut = res.IgnoredCut || cut
			ign, _ = r.Exclude.Matcher().Filter(ign)
			list = append(slices.Clip(list), ign...)
		}
		first := len(jobs)
		for _, p := range list {
			if r.Sub != "" && !strings.HasPrefix(p, r.Sub+"/") {
				continue
			}
			if (len(inc) == 0 || inc.Match(r.Name, p)) && !exc.Match(r.Name, p) {
				jobs = append(jobs, job{r, p})
			}
		}
		// ls-files 先列未跟踪的再列已跟踪的；按路径排，结果才是按路径的顺序
		slices.SortFunc(jobs[first:], func(x, y job) int { return strings.Compare(x.path, y.path) })
		if q.Ignored {
			jobs = append(jobs[:first], slices.CompactFunc(jobs[first:], func(x, y job) bool { return x.path == y.path })...)
		}
	}

	// worker 按顺序领文件，结果放进各自的格子，最后按顺序拼起来：领出去的文件总是前面连续的一段，
	// 所以截到 want 处时留下的就是"按顺序的前 want 处"，和 worker 的快慢无关。
	found := make([][]Match, len(jobs))
	limit, want := q.MaxSize(), q.MatchCap()
	var next, total, searched, skipped, timedOut atomic.Int64
	var full atomic.Bool
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), max(1, len(jobs))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf []byte
			for ctx.Err() == nil && !full.Load() {
				i := int(next.Add(1)) - 1
				if i >= len(jobs) {
					return
				}
				j := jobs[i]
				data, enc, ok := readFile(filepath.Join(j.root.Dir, filepath.FromSlash(j.path)), &buf, limit)
				if !ok {
					skipped.Add(1)
					continue
				}
				ms, err := m.file(data, want)
				if err != nil { // 高级正则超时：当跳过
					skipped.Add(1)
					timedOut.Add(1)
					continue
				}
				searched.Add(1)
				if len(ms) == 0 {
					continue
				}
				for k := range ms {
					ms[k].Repo, ms[k].Root, ms[k].Path, ms[k].Enc = j.root.Name, j.root.Dir, j.path, enc
					if enc != "" {
						ms[k].Preview, ms[k].PreviewCol = validPreview(ms[k].Preview, ms[k].PreviewCol)
					}
				}
				found[i] = ms
				if total.Add(int64(len(ms))) >= int64(want) {
					full.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		res.Err = err
		return res
	}
	res.Searched, res.Skipped, res.TimedOut = int(searched.Load()), int(skipped.Load()), int(timedOut.Load())
	res.Matches = make([]Match, 0, min(int(total.Load()), want))
	for _, ms := range found {
		if len(ms) == 0 {
			continue
		}
		if room := want - len(res.Matches); len(ms) > room {
			ms = ms[:room]
		}
		res.Matches = append(res.Matches, ms...)
		res.Files++
		if len(res.Matches) == want {
			break
		}
	}
	res.Truncated = full.Load()
	return res
}

// readFile 读一个候选文件，返回要搜的文字和编码：比 limit 大、二进制、读不了（删了、是目录）的返回 false。
// 合法的 UTF-8（带不带 BOM）、前 8000 字节没有 NUL 的原样给（enc 为空，不多拷）；别的照编辑器的规矩认编码（docs.Sniff）：
// 解成 UTF-8 的文字，认不出的（Unknown）给原来的字节（ASCII 还搜得到），二进制的跳过。
// buf 在同一个 worker 里重复用，返回的切片下次调用前有效。
func readFile(name string, buf *[]byte, limit int64) ([]byte, docs.Encoding, bool) {
	f, err := os.Open(name)
	if err != nil {
		return nil, "", false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > limit {
		return nil, "", false
	}
	n := int(fi.Size())
	if cap(*buf) < n+1 {
		*buf = make([]byte, n+1, max(n+1, 64<<10))
	}
	b := (*buf)[:n+1]
	read := 0
	for read < len(b) {
		k, err := f.Read(b[read:])
		read += k
		if err != nil {
			break
		}
	}
	if int64(read) > limit {
		return nil, "", false // 读的时候变大了
	}
	b = b[:read]
	if !git.IsBinary(b) && utf8.Valid(b) {
		return b, "", true
	}
	// UTF-16 有 NUL，要在 IsBinary 之前认（Sniff 里先看 BOM 和奇偶位的 NUL）
	text, enc := docs.Sniff(b)
	switch enc {
	case docs.Binary:
		return nil, enc, false
	case docs.Unknown:
		return b, enc, true
	}
	// 只读地借用解出来的字符串，搜完就丢
	return unsafe.Slice(unsafe.StringData(text), len(text)), enc, true
}

// ReadText 照搜索读文件的规矩读 name（同 readFile，自己的缓冲区）：搜索编辑器取上下文行用，行和列跟搜出来的对得上。
func ReadText(name string, limit int64) ([]byte, bool) {
	var buf []byte
	data, _, ok := readFile(name, &buf, limit)
	return data, ok
}

// FindFile 重读一个文件（name 是绝对路径）再搜一遍：搜索结果跟着磁盘变的时候用。和 Search 读文件的规矩一样
// （超过 limit 字节、二进制、读不了、高级正则超时的当没有结果），一个文件最多 MaxMatches 处；Repo、Root、Path 由调用的人填。
// buf 在一个 goroutine 里读几个文件时重复用（同 readFile）。
func (m *Matcher) FindFile(name string, limit int64, buf *[]byte) []Match {
	data, enc, ok := readFile(name, buf, limit)
	if !ok {
		return nil
	}
	ms, err := m.file(data, MaxMatches)
	if err != nil {
		return nil
	}
	for k := range ms {
		ms[k].Enc = enc
		if enc != "" {
			ms[k].Preview, ms[k].PreviewCol = validPreview(ms[k].Preview, ms[k].PreviewCol)
		}
	}
	return ms
}

// validPreview 把预览洗成合法的 UTF-8（认不出编码的文件原样搜，坏字节换成 U+FFFD），PreviewCol 跟着挪。
func validPreview(p string, pc int) (string, int) {
	if utf8.ValidString(p) {
		return p, pc
	}
	cut := min(max(pc, 0), len(p))
	pre := strings.ToValidUTF8(p[:cut], "\uFFFD")
	return pre + strings.ToValidUTF8(p[cut:], "\uFFFD"), pc + len(pre) - cut
}

// Matcher 是编译好的搜索条件。
type Matcher struct {
	lit    []byte         // 字面搜索：要找的字节（不区分大小写时已转成 ASCII 小写）
	fold   bool           // 字面搜索不区分大小写：内容也按 ASCII 转小写再找
	re     *regexp.Regexp // 正则，或带非 ASCII 字母的不区分大小写的字面搜索
	expand bool           // 真的是正则：替换的文字按 $1 展开（re 是转义过的字面时不展开）
	whole  bool
	// adv 是 RE2 编不了的正则（反向引用、环视）：regexp2 的回溯引擎，一次匹配最多跑 advTimeout；
	// quick 是同一个表达式、超时很短，每帧给结果预览展开替换的文字，回溯爆炸的表达式卡不住界面。
	adv, quick *regexp2.Regexp
}

// Compile 检查并编译 q：空的、正则写错的报错。
func Compile(q Query) (*Matcher, error) {
	if q.Text == "" {
		return nil, ErrEmpty
	}
	m := &Matcher{whole: q.WholeWord}
	switch {
	case !q.Regexp && strings.Contains(q.Text, "\n"):
		// 多行的字面：走正则，换行配 \n 和 \r\n
		expr := strings.ReplaceAll(regexp.QuoteMeta(strings.ReplaceAll(q.Text, "\r", "")), "\n", `\r?\n`)
		if !q.CaseSensitive {
			expr = "(?i)" + expr
		}
		m.re = regexp.MustCompile(expr)
	case q.Regexp:
		expr := q.Text
		if !q.CaseSensitive {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile("(?m)" + expr)
		if err != nil && advancedSyntax(q.Text) {
			return m, m.compileAdvanced(q)
		}
		if err != nil {
			return nil, errors.New(i18n.Tf("正则写错了：%s", strings.TrimPrefix(err.Error(), "error parsing regexp: ")))
		}
		m.re, m.expand = re, true
	case q.CaseSensitive || !hasCase(q.Text):
		m.lit = []byte(q.Text)
	case isASCII(q.Text):
		m.lit, m.fold = []byte(strings.ToLower(q.Text)), true
	default:
		m.re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(q.Text))
	}
	return m, nil
}

// file 找 data 里最多 limit 处匹配，填好行、列、预览（仓库和路径由调用方填）。只有高级正则超时会报错（errTimeout 一类）。
func (m *Matcher) file(data []byte, limit int) ([]Match, error) {
	var out []Match
	// 当前扫到的行和它的起点；匹配按位置递增，往前数换行就行。开头的 UTF-8 BOM 不算进第一行的列
	// （编辑器里没有它，跳过去、替换都按编辑器里的列）
	line, lineStart := 0, bomLen(data)
	emit := func(s, e int) bool {
		if e <= s || s < lineStart || m.whole && !wordBounded(data, s, e) {
			return true
		}
		for {
			i := bytes.IndexByte(data[lineStart:s], '\n')
			if i < 0 {
				break
			}
			lineStart += i + 1
			line++
		}
		p, pc, n := matchPreview(data, lineStart, s, e)
		cr := bytes.Count(data[s:e], []byte("\r\n"))
		out = append(out, Match{Line: line, Col: s - lineStart, Len: n, Span: e - s, CR: cr, Preview: p, PreviewCol: pc})
		return len(out) < limit
	}
	switch {
	case m.adv != nil:
		locs, err := m.adv.FindAllStringIndex(bytesString(data), 2*limit) // 位置是字节的
		if err != nil {
			return nil, err
		}
		for _, loc := range locs {
			if !emit(loc[0], loc[1]) {
				break
			}
		}
	case m.re != nil:
		// 最多取 2 倍：`.` 这样的正则在 2 MB 的文件里有两百万处，全取出来光位置就上百 MB
		for _, loc := range m.re.FindAllIndex(data, 2*limit) {
			if !emit(loc[0], loc[1]) {
				break
			}
		}
	default:
		hay := data
		if m.fold {
			hay = asciiLower(data)
		}
		for off := 0; off <= len(hay)-len(m.lit); {
			i := bytes.Index(hay[off:], m.lit)
			if i < 0 {
				break
			}
			s := off + i
			if !emit(s, s+len(m.lit)) {
				break
			}
			off = s + max(1, len(m.lit))
		}
	}
	return out, nil
}

// matchPreview 是 data 里 s 到 e 那一处（lineStart 是它那一行的起点）的预览、匹配在预览里的起点和标出来的长度。
// 一行内的照 preview；跨行的是第一行匹配前的那段、匹配本身（换行画成 ⏎，太长截到 previewAfter）、最后一行匹配后的那段。
func matchPreview(data []byte, lineStart, s, e int) (p string, pc, n int) {
	lineEnd := len(data)
	if i := bytes.IndexByte(data[s:], '\n'); i >= 0 {
		lineEnd = s + i
	}
	if e <= lineEnd {
		p, pc = preview(data[lineStart:lineEnd], s-lineStart, e-lineStart)
		return p, pc, e - s
	}
	head, pc := preview(data[lineStart:lineEnd], s-lineStart, lineEnd-lineStart)
	head = head[:pc] // 第一行匹配后面的都是匹配
	hit := strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\t", " ").Replace(string(data[s:e]))
	if len(hit) > previewAfter {
		cut := previewAfter
		for cut > 0 && !utf8.RuneStart(hit[cut]) {
			cut--
		}
		return head + hit[:cut] + "…", pc, cut
	}
	lastEnd := len(data)
	if i := bytes.IndexByte(data[e:], '\n'); i >= 0 {
		lastEnd = e + i
	}
	after := data[e:lastEnd]
	cut := len(after) > previewAfter
	if cut {
		to := previewAfter
		for to > 0 && !utf8.RuneStart(after[to]) {
			to--
		}
		after = after[:to]
	}
	tail := strings.TrimRight(strings.ReplaceAll(string(after), "\t", " "), " \r")
	if cut {
		tail += "…"
	}
	return head + hit + tail, pc, len(hit)
}

// preview 把一行截成预览：匹配前最多 previewBefore 字节、后面最多 previewAfter 字节（在字符边界上），
// 制表符换成空格，去掉开头的空白；返回预览和匹配在预览里的起点。
func preview(line []byte, s, e int) (string, int) {
	if n := len(line); n > 0 && line[n-1] == '\r' && e < n {
		line = line[:n-1] // \r\n 换行的 \r：编辑器里没有它，预览不该因为它多个"…"
	}
	from, to := max(0, s-previewBefore), min(len(line), e+previewAfter)
	for from > 0 && !utf8.RuneStart(line[from]) {
		from--
	}
	for to < len(line) && !utf8.RuneStart(line[to]) {
		to++
	}
	cut := from > 0
	for from < s && (line[from] == ' ' || line[from] == '\t') {
		from++
	}
	text := strings.ReplaceAll(string(line[from:to]), "\t", " ")
	text = strings.TrimRight(text, " \r")
	pre := s - from
	if cut {
		text, pre = "…"+text, pre+len("…")
	}
	if to < len(line) {
		text += "…"
	}
	return text, pre
}

func wordBounded(data []byte, s, e int) bool {
	if s > 0 {
		r, _ := utf8.DecodeLastRune(data[:s])
		if isWord(r) {
			return false
		}
	}
	if e < len(data) {
		r, _ := utf8.DecodeRune(data[e:])
		if isWord(r) {
			return false
		}
	}
	return true
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// hasCase 是文字里有分大小写的字母（中文没有，这时区不区分大小写都一样，走最快的字面搜索）。
func hasCase(s string) bool {
	for _, r := range s {
		if unicode.ToLower(r) != unicode.ToUpper(r) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiLower 是只把 ASCII 字母转小写的副本，长度不变，位置对得上。
func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}
